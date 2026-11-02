package derp

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"net"
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/derp/artifact"
	"github.com/darkraise/darkpanel/internal/modules/derp/server"
	"github.com/darkraise/darkpanel/internal/platform"
	"github.com/darkraise/darkpanel/internal/platform/netstat"
)

const (
	inIP      = "93.184.216.34"
	inName    = "derp.example.com"
	inUseradd = "/usr/sbin/useradd --system --no-create-home --shell /usr/sbin/nologin darkpanel-derp"
)

var inBinary = []byte("derper-binary-bytes")

func inSum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func inBool(b bool) *bool { return &b }

func inInput(mode, host string, verify *bool) json.RawMessage {
	b, err := json.Marshal(ServerInstallInput{Mode: mode, HostName: host, VerifyClients: verify})
	if err != nil {
		panic(err)
	}
	return b
}

// inCert returns a self-signed certificate for ip valid until notAfter, as DER and as PEM.
func inCert(t *testing.T, ip string, notAfter time.Time) (der, pemBytes []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: ip},
		NotBefore: notAfter.Add(-365 * 24 * time.Hour), NotAfter: notAfter, IPAddresses: []net.IP{net.ParseIP(ip)},
	}
	der, err = x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return der, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

// inCall is one host change of the install and the newest effect the journal held when it was made.
type inCall struct{ Call, Effect string }

// inTrace records every host change of an install, so a test can check that its effect was journaled first.
type inTrace struct {
	h     *dpHost
	calls []inCall
}

func (tr *inTrace) note(ctx context.Context, call string) {
	effects, _ := tr.h.journal.List(ctx)
	last := ""
	if n := len(effects); n > 0 {
		last = effects[n-1].Resource + " " + string(effects[n-1].State)
	}
	tr.calls = append(tr.calls, inCall{call, last})
}

type inRunner struct {
	core.Runner
	tr *inTrace
}

func (r inRunner) Run(ctx context.Context, cmd core.Cmd) (core.CmdResult, error) {
	call := strings.Join(append([]string{cmd.Path}, cmd.Args...), " ")
	if strings.HasSuffix(cmd.Path, "/useradd") {
		owned, _ := platform.NewOwnership(r.tr.h.db).Owns(ctx, ID, server.ServiceUser)
		call += fmt.Sprintf(" (recorded as owned: %t)", owned)
	}
	r.tr.note(ctx, call)
	return r.Runner.Run(ctx, cmd)
}

type inFiles struct {
	core.FileWriter
	tr *inTrace
}

func (f inFiles) WriteFile(ctx context.Context, module, path string, data []byte, mode fs.FileMode) error {
	f.tr.note(ctx, fmt.Sprintf("write %s %04o", path, uint32(mode.Perm())))
	return f.FileWriter.WriteFile(ctx, module, path, data, mode)
}

func (f inFiles) EnsureServiceDir(ctx context.Context, module, dir string) error {
	f.tr.note(ctx, "ensure "+dir)
	return f.FileWriter.EnsureServiceDir(ctx, module, dir)
}

func (f inFiles) Remove(ctx context.Context, module, path string) error {
	f.tr.note(ctx, "remove "+path)
	return f.FileWriter.Remove(ctx, module, path)
}

func (f inFiles) RemoveServiceDir(ctx context.Context, module, dir string) error {
	f.tr.note(ctx, "remove-dir "+dir)
	return f.FileWriter.RemoveServiceDir(ctx, module, dir)
}

type inUnits struct {
	core.Units
	tr *inTrace
}

func (u inUnits) Reload(ctx context.Context) error {
	u.tr.note(ctx, "reload")
	return u.Units.Reload(ctx)
}

func (u inUnits) Enable(ctx context.Context, files ...string) error {
	u.tr.note(ctx, "enable "+strings.Join(files, ","))
	return u.Units.Enable(ctx, files...)
}

func (u inUnits) Start(ctx context.Context, unit string) error {
	u.tr.note(ctx, "start "+unit)
	return u.Units.Start(ctx, unit)
}

// inSetup is a host with nothing installed and change c1's environment, whose runner, files and units trace
// every host change. The fetch answers inBinary.
type inSetup struct {
	h   *dpHost
	d   dp
	tr  *inTrace
	env core.Env
}

func inNewSetup(t *testing.T) *inSetup {
	t.Helper()
	h := newDPHost(t)
	h.fetchBin = inBinary
	s := &inSetup{h: h, d: h.dp(), tr: &inTrace{h: h}}
	s.env = h.env("c1")
	s.env.Exec = inRunner{s.env.Exec, s.tr}
	s.env.Files = inFiles{s.env.Files, s.tr}
	s.env.Units = inUnits{s.env.Units, s.tr}
	return s
}

// planEnv is the environment of a plan: no change, so no journal.
func (s *inSetup) planEnv() core.Env { return s.h.env("") }

// setUnit makes the fake systemd answer state for darkpanel-derper.service.
func (s *inSetup) setUnit(state core.UnitState) {
	s.h.units.States = map[string]core.UnitState{server.UnitName: state}
}

func (s *inSetup) active() {
	s.setUnit(core.UnitState{LoadState: "loaded", ActiveState: "active", SubState: "running", MainPID: 2000})
}

// certFile puts a certificate for inIP where derper keeps it and returns its DER and NotAfter.
func (s *inSetup) certFile(t *testing.T) (der []byte, notAfter time.Time) {
	t.Helper()
	der, pemBytes := inCert(t, inIP, s.h.clock.Now().Add(365*24*time.Hour))
	s.h.reads[server.CertDir+"/"+inIP+".crt"] = pemBytes
	_, notAfter, err := server.ParseCert(pemBytes)
	if err != nil {
		t.Fatal(err)
	}
	return der, notAfter
}

func (s *inSetup) effects(t *testing.T) []core.Effect {
	t.Helper()
	effects, err := s.h.journal.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return effects
}

func (s *inSetup) row(t *testing.T) (localServer, bool) {
	t.Helper()
	row, ok, err := loadLocalServer(context.Background(), s.h.db.Read())
	if err != nil {
		t.Fatal(err)
	}
	return row, ok
}

// inInstalled runs the install of ip mode, verification off, on a host whose derper is up, and returns the
// setup with the install's effects and input.
func inInstalled(t *testing.T) *inSetup {
	t.Helper()
	s := inNewSetup(t)
	s.active()
	s.certFile(t)
	if _, err := s.d.serverInstall().Handler.Task(context.Background(), s.env, inInput("ip", inIP, inBool(false))); err != nil {
		t.Fatal(err)
	}
	return s
}

// inDrive runs call while advancing the host's clock by one poll whenever it waits.
func inDrive(t *testing.T, h *dpHost, call func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		call()
	}()
	for deadline := time.Now().Add(20 * time.Second); ; {
		select {
		case <-done:
			return
		default:
		}
		if h.clock.Pending() > 0 {
			h.clock.Advance(installPoll)
		} else {
			time.Sleep(time.Millisecond)
		}
		if time.Now().After(deadline) {
			t.Fatal("the call did not finish")
		}
	}
}

func inCoded(t *testing.T, err error, code errcode.Code) *errcode.Error {
	t.Helper()
	var coded *errcode.Error
	if !errors.As(err, &coded) || coded.Code != code {
		t.Fatalf("error %v, want code %s", err, code)
	}
	return coded
}

func inListener(proto netstat.Proto, addr string, state netstat.State, comm string) netstat.Socket {
	return netstat.Socket{
		Proto: proto, Family: netstat.IPv4, Local: netip.MustParseAddrPort(addr), State: state,
		Processes: []netstat.Process{{PID: 812, Comm: comm}},
	}
}

func TestInSpec(t *testing.T) {
	def := newDPHost(t).dp().serverInstall()
	sp, h := def.Spec, def.Handler
	if sp.Name != "derp.server.install" || sp.Version != 1 || sp.Class != core.ClassCompensable || sp.Side != core.SideAgent ||
		!slices.Equal(sp.LockKeys, []string{"derp"}) || sp.ApplyTimeout != 15*time.Minute || !sp.Task || !sp.LongRunning ||
		sp.Available == nil || len(sp.Guards) != 0 || sp.Internal || sp.CompensatedBy != "" || sp.Converges {
		t.Errorf("spec %+v, want a long-running compensable agent task locking derp for 15 minutes, with an availability check", sp)
	}
	if def.InputType != reflect.TypeFor[ServerInstallInput]() || def.OutputType != reflect.TypeFor[ServerInstalled]() {
		t.Errorf("input %v and output %v, want ServerInstallInput and ServerInstalled", def.InputType, def.OutputType)
	}
	if h.Plan == nil || h.Task == nil || h.Verify == nil || h.Observe == nil || h.Inverse == nil || h.Apply != nil || h.Recover != nil {
		t.Errorf("handler has Plan %v Task %v Verify %v Observe %v Inverse %v Apply %v Recover %v, want a task handler with an undo and no recovery handler",
			h.Plan != nil, h.Task != nil, h.Verify != nil, h.Observe != nil, h.Inverse != nil, h.Apply != nil, h.Recover != nil)
	}
}

func TestInPlan(t *testing.T) {
	ctx := context.Background()
	line := func(op, text string) core.DiffLine { return core.DiffLine{Op: op, Text: text} }
	common := func() []core.DiffLine {
		return []core.DiffLine{
			line("+", "user darkpanel-derp"),
			line("+", "file /usr/local/lib/darkpanel/derper (derper "+artifact.DerperVersion+" for amd64)"),
			line("+", "file /etc/systemd/system/darkpanel-derper.service"),
			line("+", "directory /var/lib/darkpanel/derp (owned by darkpanel-derp)"),
			line("+", "unit darkpanel-derper.service, enabled and started"),
		}
	}
	tests := []struct {
		name string
		in   json.RawMessage
		want []core.DiffLine
	}{
		{"letsencrypt, verification on by default", inInput("letsencrypt", inName, nil),
			append(common(), line("+", "certificate for derp.example.com from Let's Encrypt; derper requests and renews it, and port 80 serves the challenge"))},
		{"ip, verification off", inInput("ip", inIP, inBool(false)),
			append(common(),
				line("+", "certificate for 93.184.216.34, self-signed by derper at its first start; clients pin it"),
				line(" ", "! client verification is off: any node that knows the region can relay through it"))},
		{"ip, verification on", inInput("ip", inIP, inBool(true)),
			append(common(), line("+", "certificate for 93.184.216.34, self-signed by derper at its first start; clients pin it"))},
	}
	hashes := map[string]string{}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := inNewSetup(t)
			def := s.d.serverInstall()
			out, err := def.Handler.Plan(ctx, s.planEnv(), tc.in)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(out.Diff, tc.want) || out.Confirmation != "" || len(out.Guards) != 0 || len(out.StateHash) != 64 {
				t.Errorf("plan %+v, want diff %+v, no confirmation, no guard and a 64-digit hash", out, tc.want)
			}
			if len(s.h.ran) != 0 || len(s.h.files) != 0 || len(s.h.units.Calls) != 0 || len(s.h.fetches) != 0 || len(s.effects(t)) != 0 {
				t.Errorf("plan ran %v, wrote %v, called units %v, fetched %v and journaled %v, want nothing", s.h.ran, s.h.files, s.h.units.Calls, s.h.fetches, s.effects(t))
			}
			again, err := def.Handler.Plan(ctx, s.planEnv(), tc.in)
			if err != nil || again.StateHash != out.StateHash {
				t.Errorf("a second plan hash %q (error %v), want %q", again.StateHash, err, out.StateHash)
			}
			if other, ok := hashes[out.StateHash]; ok {
				t.Errorf("plans %q and %q share the state hash %s", other, tc.name, out.StateHash)
			}
			hashes[out.StateHash] = tc.name
		})
	}
}

func TestInRefusals(t *testing.T) {
	ctx := context.Background()
	const ssh22 = "0.0.0.0:22"
	tests := []struct {
		name   string
		in     json.RawMessage
		setup  func(s *inSetup)
		code   errcode.Code
		field  string
		detail []string
	}{
		{"a mode that is neither", inInput("dns", inName, nil), nil, errcode.InvalidInput, "/mode", nil},
		{"a private address in ip mode", inInput("ip", "10.0.0.5", nil), nil, errcode.InvalidInput, "/hostName", nil},
		{"a name with capitals in letsencrypt mode", inInput("letsencrypt", "Derp.Example.com", nil), nil, errcode.InvalidInput, "/hostName", nil},
		{"a server already installed", inInput("ip", inIP, nil), func(s *inSetup) {
			now := s.h.clock.Now()
			err := insertLocalServer(ctx, s.h.db, localServer{
				ChangeID: "c0", Mode: "ip", HostName: inIP, DerperVersion: artifact.DerperVersion, BinarySHA256: inSum(inBinary),
				InstalledAt: now, UpdatedAt: now,
			})
			if err != nil {
				t.Fatal(err)
			}
		}, errcode.InvalidInput, "", []string{"a DERP server is already installed on this host"}},
		{"port 443 in use", inInput("ip", inIP, nil), func(s *inSetup) {
			s.h.sockets = []netstat.Socket{
				inListener(netstat.ProtoTCP, ssh22, netstat.StateListen, "sshd"),
				inListener(netstat.ProtoTCP, "0.0.0.0:443", netstat.StateListen, "nginx"),
				inListener(netstat.ProtoTCP, "10.0.0.1:80", netstat.StateEstablished, "curl"),
			}
		}, errcode.DERPPortInUse, "", []string{"443/tcp", "nginx"}},
		{"every port in use", inInput("ip", inIP, nil), func(s *inSetup) {
			s.h.sockets = []netstat.Socket{
				inListener(netstat.ProtoTCP, "0.0.0.0:80", netstat.StateListen, "nginx"),
				inListener(netstat.ProtoTCP, "[::]:443", netstat.StateListen, "nginx"),
				inListener(netstat.ProtoUDP, "0.0.0.0:3478", netstat.StateClose, "coturn"),
			}
		}, errcode.DERPPortInUse, "", []string{"80/tcp", "443/tcp", "3478/udp", "coturn"}},
		{"a user that is not the module's", inInput("ip", inIP, nil), func(s *inSetup) {
			s.h.users[server.ServiceUser] = [2]int{900, 900}
		}, errcode.InvalidInput, "", []string{"darkpanel-derp"}},
		{"a binary that is not the module's", inInput("ip", inIP, nil), func(s *inSetup) {
			s.h.reads[server.BinaryPath] = []byte("someone else's derper")
		}, errcode.InvalidInput, "", []string{server.BinaryPath}},
		{"a unit file that is not the module's", inInput("ip", inIP, nil), func(s *inSetup) {
			s.h.reads[server.UnitPath] = []byte("[Service]\n")
		}, errcode.InvalidInput, "", []string{server.UnitPath}},
		{"a service directory that is not the module's", inInput("ip", inIP, nil), func(s *inSetup) {
			s.h.reads[server.StateDir] = []byte{}
		}, errcode.InvalidInput, "", []string{server.StateDir}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := inNewSetup(t)
			if tc.setup != nil {
				tc.setup(s)
			}
			h := s.d.serverInstall().Handler
			_, planErr := h.Plan(ctx, s.planEnv(), tc.in)
			_, taskErr := h.Task(ctx, s.env, tc.in)
			for what, err := range map[string]error{"plan": planErr, "task": taskErr} {
				if !core.IsDefinite(err) {
					t.Errorf("%s: error %v is not definite", what, err)
				}
				coded := inCoded(t, err, tc.code)
				if tc.field != "" && (len(coded.Fields) != 1 || coded.Fields[0].Path != tc.field) {
					t.Errorf("%s: fields %+v, want one at %s", what, coded.Fields, tc.field)
				}
				for _, want := range tc.detail {
					if !strings.Contains(coded.Detail, want) {
						t.Errorf("%s: detail %q lacks %q", what, coded.Detail, want)
					}
				}
			}
			if len(s.h.ran) != 0 || len(s.h.files) != 0 || len(s.h.units.Calls) != 0 || len(s.h.fetches) != 0 || len(s.effects(t)) != 0 {
				t.Errorf("a refusal ran %v, wrote %v, called units %v, fetched %v or journaled %v", s.h.ran, s.h.files, s.h.units.Calls, s.h.fetches, s.effects(t))
			}
		})
	}
}

func TestInPlanAcceptsWhatTheModuleOwns(t *testing.T) {
	s := inNewSetup(t)
	s.h.users[server.ServiceUser] = [2]int{900, 900}
	for _, p := range []string{server.ServiceUser, server.BinaryPath, server.UnitPath, server.StateDir} {
		s.h.owned[p] = true
	}
	for _, p := range []string{server.BinaryPath, server.UnitPath, server.StateDir} {
		s.h.reads[p] = []byte("left by an interrupted install")
	}
	if _, err := s.d.serverInstall().Handler.Plan(context.Background(), s.planEnv(), inInput("ip", inIP, nil)); err != nil {
		t.Errorf("plan over leftovers the module owns: %v", err)
	}
}

func TestInTaskInstalls(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name   string
		mode   string
		host   string
		verify *bool
		cfg    server.Config
	}{
		{"ip mode, verification off", "ip", inIP, inBool(false), server.Config{Mode: server.ModeIP, HostName: inIP, VerifyClients: false}},
		{"letsencrypt mode, verification on by default", "letsencrypt", inName, nil, server.Config{Mode: server.ModeLetsEncrypt, HostName: inName, VerifyClients: true}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := inNewSetup(t)
			s.active()
			var wantPin string
			var wantNotAfter time.Time
			if tc.cfg.Mode == server.ModeIP {
				der, notAfter := s.certFile(t)
				wantPin, wantNotAfter = server.Pin(der), notAfter
			}
			def := s.d.serverInstall()
			raw, err := def.Handler.Task(ctx, s.env, inInput(tc.mode, tc.host, tc.verify))
			if err != nil {
				t.Fatal(err)
			}
			var out ServerInstalled
			if err := json.Unmarshal(raw, &out); err != nil {
				t.Fatal(err)
			}
			if out.DerperVersion != artifact.DerperVersion || out.CertName != wantPin ||
				(wantNotAfter.IsZero() != (out.CertNotAfter == nil)) || (out.CertNotAfter != nil && !out.CertNotAfter.Equal(wantNotAfter)) {
				t.Errorf("output %s, want derper %s, pin %q and expiry %v", raw, artifact.DerperVersion, wantPin, wantNotAfter)
			}

			unit := server.UnitFile(tc.cfg)
			if !slices.Equal(s.h.fetches, []string{"1.4.0 " + artifact.DerperVersion + " amd64"}) {
				t.Errorf("fetches %v, want one of this agent's own release", s.h.fetches)
			}
			if string(s.h.files[server.BinaryPath]) != string(inBinary) || string(s.h.files[server.UnitPath]) != string(unit) {
				t.Errorf("files %v, want the fetched binary and the unit text of %+v", s.h.files, tc.cfg)
			}
			if want := [][]string{strings.Fields(inUseradd)}; !reflect.DeepEqual(s.h.ran, want) {
				t.Errorf("ran %v, want only useradd", s.h.ran)
			}
			wantCalls := []inCall{
				{inUseradd + " (recorded as owned: true)", "derp.user:?darkpanel-derp intended"},
				{"write /usr/local/lib/darkpanel/derper 0755", "derp.file:?/usr/local/lib/darkpanel/derper intended"},
				{"write /etc/systemd/system/darkpanel-derper.service 0644", "derp.file:?/etc/systemd/system/darkpanel-derper.service intended"},
				{"ensure /var/lib/darkpanel/derp", "derp.dir:?/var/lib/darkpanel/derp intended"},
				{"reload", "derp.unit:?darkpanel-derper.service intended"},
				{"enable darkpanel-derper.service", "derp.unit:?darkpanel-derper.service intended"},
				{"start darkpanel-derper.service", "derp.unit:?darkpanel-derper.service intended"},
			}
			if !slices.Equal(s.tr.calls, wantCalls) {
				t.Errorf("host changes with the newest journaled effect:\n%+v\nwant\n%+v", s.tr.calls, wantCalls)
			}

			wantEffects := []struct{ resource, identity, post string }{
				{"derp.user:darkpanel-derp", "darkpanel-derp", `{"exists":true}`},
				{"derp.file:" + server.BinaryPath, server.BinaryPath, `{"sha256":"` + inSum(inBinary) + `"}`},
				{"derp.file:" + server.UnitPath, server.UnitPath, `{"sha256":"` + inSum(unit) + `"}`},
				{"derp.dir:" + server.StateDir, server.StateDir, `{"exists":true}`},
				{"derp.unit:" + server.UnitName, server.UnitName, `{"enabled":true,"active":true}`},
				{"derp.server:this", "this", fmt.Sprintf(`{"mode":%q,"hostName":%q,"verifyClients":%t}`, tc.mode, tc.host, tc.cfg.VerifyClients)},
			}
			effects := s.effects(t)
			if len(effects) != len(wantEffects) {
				t.Fatalf("%d effects, want %d: %+v", len(effects), len(wantEffects), effects)
			}
			for i, w := range wantEffects {
				e := effects[i]
				if e.Kind != core.EffectCreate || e.State != core.EffectMade || e.Resource != w.resource || !e.Local || !e.InverseLocal ||
					string(e.Before.State) != "null" || e.Post == nil || string(e.Post.State) != w.post ||
					e.Recovery == nil || e.Recovery.Kind != "name" || e.Recovery.Value != w.identity {
					t.Errorf("effect %d is %+v, want a made, local creation of %s recovered by name %q with post %s", i+1, e, w.resource, w.identity, w.post)
				}
			}

			row, ok := s.row(t)
			want := localServer{
				ChangeID: "c1", Mode: tc.mode, HostName: tc.host, VerifyClients: tc.cfg.VerifyClients, DerperVersion: artifact.DerperVersion,
				BinarySHA256: inSum(inBinary), CertName: wantPin, CertNotAfter: wantNotAfter, InstalledAt: s.h.clock.Now(), UpdatedAt: s.h.clock.Now(),
			}
			if !ok || row.CertName != want.CertName || !row.CertNotAfter.Equal(want.CertNotAfter) || !row.InstalledAt.Equal(want.InstalledAt) || !row.UpdatedAt.Equal(want.UpdatedAt) {
				t.Errorf("derp_server row %+v (found %v), want %+v", row, ok, want)
			}
			row.CertNotAfter, row.InstalledAt, row.UpdatedAt = time.Time{}, time.Time{}, time.Time{}
			want.CertNotAfter, want.InstalledAt, want.UpdatedAt = time.Time{}, time.Time{}, time.Time{}
			if row != want {
				t.Errorf("derp_server row %+v, want %+v", row, want)
			}
			if owned, err := platform.NewOwnership(s.h.db).Owns(ctx, ID, server.ServiceUser); err != nil || !owned {
				t.Errorf("the user is recorded as the module's: %v (error %v)", owned, err)
			}
			if err := def.Handler.Verify(ctx, s.env, inInput(tc.mode, tc.host, tc.verify), raw); err != nil {
				t.Errorf("verify after the install: %v", err)
			}
		})
	}
}

func TestInTaskKeepsAnOwnedUser(t *testing.T) {
	s := inNewSetup(t)
	s.active()
	s.certFile(t)
	s.h.users[server.ServiceUser] = [2]int{900, 900}
	s.h.owned[server.ServiceUser] = true
	if _, err := s.d.serverInstall().Handler.Task(context.Background(), s.env, inInput("ip", inIP, inBool(false))); err != nil {
		t.Fatal(err)
	}
	effects := s.effects(t)
	if len(s.h.ran) != 0 || len(effects) != 5 || effects[0].Resource != "derp.file:"+server.BinaryPath {
		t.Errorf("ran %v and journaled %d effects starting %q, want no useradd and the five effects after the user", s.h.ran, len(effects), effects[0].Resource)
	}
}

func TestInTaskFetchFailureChangesNothing(t *testing.T) {
	s := inNewSetup(t)
	s.h.fetchErr = errcode.New(errcode.UpdateNotFound, "darkpanel 1.4.0 is not a published release; a DERP server needs a released darkpanel")
	_, err := s.d.serverInstall().Handler.Task(context.Background(), s.env, inInput("ip", inIP, nil))
	_ = inCoded(t, err, errcode.UpdateNotFound)
	if !core.IsDefinite(err) {
		t.Errorf("error %v is not definite", err)
	}
	if len(s.h.fetches) != 1 || len(s.h.ran) != 0 || len(s.h.files) != 0 || len(s.h.units.Calls) != 0 || len(s.effects(t)) != 0 {
		t.Errorf("a failed fetch left fetches %v, ran %v, files %v, unit calls %v and effects %v", s.h.fetches, s.h.ran, s.h.files, s.h.units.Calls, s.effects(t))
	}
}

func TestInTaskFailedUseraddLeavesItsEffectIntended(t *testing.T) {
	s := inNewSetup(t)
	s.h.script(inUseradd, dpRun{res: core.CmdResult{ExitCode: 9, Stderr: []byte("useradd: cannot lock /etc/passwd")}})
	_, err := s.d.serverInstall().Handler.Task(context.Background(), s.env, inInput("ip", inIP, nil))
	if err == nil || core.IsDefinite(err) {
		t.Fatalf("error %v, want an error that is not definite", err)
	}
	effects := s.effects(t)
	if len(effects) != 1 || effects[0].Resource != "derp.user:?darkpanel-derp" || effects[0].State != core.EffectIntended || len(s.h.files) != 0 || len(s.h.units.Calls) != 0 {
		t.Errorf("effects %+v, files %v and unit calls %v, want only the user's effect, still intended, and nothing after it", effects, s.h.files, s.h.units.Calls)
	}
}

func TestInTaskWaitsForTheCertificate(t *testing.T) {
	s := inNewSetup(t)
	s.active()
	der, _ := s.certFile(t)
	crtPath := server.CertDir + "/" + inIP + ".crt"
	reads := 0
	readFile := s.d.d.readFile
	s.d.d.readFile = func(name string) ([]byte, error) {
		if name == crtPath {
			if reads++; reads < 5 {
				return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
			}
		}
		return readFile(name)
	}
	start := s.h.clock.Now()
	var out json.RawMessage
	var err error
	inDrive(t, s.h, func() {
		out, err = s.d.serverInstall().Handler.Task(context.Background(), s.env, inInput("ip", inIP, nil))
	})
	if err != nil {
		t.Fatal(err)
	}
	var got ServerInstalled
	if err := json.Unmarshal(out, &got); err != nil || got.CertName != server.Pin(der) {
		t.Errorf("output %s (error %v), want the pin of the certificate that appeared", out, err)
	}
	if waited := s.h.clock.Now().Sub(start); waited < 3*installPoll || waited >= certWait {
		t.Errorf("waited %v, want a few polls and less than %v", waited, certWait)
	}
}

func TestInTaskGivesUpWhenDerperDoesNotComeUp(t *testing.T) {
	tests := []struct {
		name   string
		unit   core.UnitState
		cert   bool
		detail string
	}{
		{"the unit never becomes active", core.UnitState{LoadState: "loaded", ActiveState: "failed", SubState: "failed"}, true, "did not become active"},
		{"the certificate never appears", core.UnitState{LoadState: "loaded", ActiveState: "active", SubState: "running"}, false, "certificate"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := inNewSetup(t)
			s.setUnit(tc.unit)
			if tc.cert {
				s.certFile(t)
			}
			start := s.h.clock.Now()
			var err error
			inDrive(t, s.h, func() {
				_, err = s.d.serverInstall().Handler.Task(context.Background(), s.env, inInput("ip", inIP, nil))
			})
			coded := inCoded(t, err, errcode.DERPVerifyFailed)
			if !strings.Contains(coded.Detail, tc.detail) {
				t.Errorf("detail %q, want it to contain %q", coded.Detail, tc.detail)
			}
			if waited := s.h.clock.Now().Sub(start); waited < certWait {
				t.Errorf("gave up after %v, want %v", waited, certWait)
			}
			effects := s.effects(t)
			if len(effects) != 5 || effects[4].State != core.EffectMade {
				t.Errorf("effects %+v, want the five up to the unit, made, so the undo removes them", effects)
			}
			if _, ok := s.row(t); ok {
				t.Error("a derp_server row exists for an install that did not verify")
			}
		})
	}
}

func TestInObserve(t *testing.T) {
	ctx := context.Background()
	t.Run("what the install made", func(t *testing.T) {
		s := inInstalled(t)
		s.h.reads[server.StateDir] = []byte{}
		h := s.d.serverInstall().Handler
		for _, e := range s.effects(t) {
			obs, err := h.Observe(ctx, s.env, e)
			if err != nil {
				t.Fatalf("%s: %v", e.Resource, err)
			}
			if string(obs.Current.State) != string(e.Post.State) || obs.Matches != 1 || obs.Resource != e.Resource || !obs.Exclusive {
				t.Errorf("%s observed as %+v, want state %s, one match, the same resource and an exclusive identity", e.Resource, obs, e.Post.State)
			}
		}
	})
	t.Run("what an effect not yet made names, on a host with nothing", func(t *testing.T) {
		s := inNewSetup(t)
		s.setUnit(core.UnitState{LoadState: "not-found"})
		h := s.d.serverInstall().Handler
		for _, resource := range []string{
			"derp.user:?darkpanel-derp", "derp.file:?" + server.BinaryPath, "derp.file:?" + server.UnitPath,
			"derp.dir:?" + server.StateDir, "derp.unit:?" + server.UnitName, "derp.server:?this",
		} {
			obs, err := h.Observe(ctx, s.env, core.Effect{Kind: core.EffectCreate, Resource: resource})
			if err != nil {
				t.Fatalf("%s: %v", resource, err)
			}
			if string(obs.Current.State) != "null" || obs.Matches != 0 || !obs.Exclusive {
				t.Errorf("%s observed as %+v, want state null, no match and an exclusive identity", resource, obs)
			}
		}
	})
	t.Run("a resource the install does not make", func(t *testing.T) {
		s := inNewSetup(t)
		for _, resource := range []string{"derp.file:/etc/passwd", "derp.unit:ssh.service", "derp.binary:derper", "derp.user:root"} {
			if _, err := s.d.serverInstall().Handler.Observe(ctx, s.env, core.Effect{Resource: resource}); err == nil {
				t.Errorf("%s: no error, want a refusal", resource)
			}
		}
	})
}

func TestInInverse(t *testing.T) {
	ctx := context.Background()
	const reload = "reload"
	stop, disable := "stop "+server.UnitName, "disable "+server.UnitName
	// effect picks the effect of the installed setup that names resource.
	effect := func(t *testing.T, s *inSetup, resource string) core.Effect {
		t.Helper()
		for _, e := range s.effects(t) {
			if e.Resource == resource {
				return e
			}
		}
		t.Fatalf("no effect on %s", resource)
		return core.Effect{}
	}
	inverse := func(t *testing.T, s *inSetup, resource string) {
		t.Helper()
		res, err := s.d.serverInstall().Handler.Inverse(ctx, s.env, effect(t, s, resource))
		if err != nil || res.Resource != "" {
			t.Fatalf("inverse of %s: %+v, %v, want no error and no recreated object", resource, res, err)
		}
	}
	t.Run("the server row", func(t *testing.T) {
		s := inInstalled(t)
		inverse(t, s, "derp.server:this")
		if _, ok := s.row(t); ok {
			t.Error("the derp_server row is still there")
		}
	})
	t.Run("an active unit is stopped and disabled", func(t *testing.T) {
		s := inInstalled(t)
		before := len(s.h.units.Calls)
		inverse(t, s, "derp.unit:"+server.UnitName)
		if got := s.h.units.Calls[before:]; !slices.Equal(got, []string{stop, disable}) {
			t.Errorf("unit calls %v, want stop, then disable", got)
		}
	})
	t.Run("a unit that is gone is left alone", func(t *testing.T) {
		s := inInstalled(t)
		s.setUnit(core.UnitState{LoadState: "not-found"})
		before := len(s.h.units.Calls)
		inverse(t, s, "derp.unit:"+server.UnitName)
		if got := s.h.units.Calls[before:]; len(got) != 0 {
			t.Errorf("unit calls %v, want none", got)
		}
	})
	t.Run("the unit file is removed and systemd reloads", func(t *testing.T) {
		s := inInstalled(t)
		before := len(s.h.units.Calls)
		inverse(t, s, "derp.file:"+server.UnitPath)
		if _, ok := s.h.files[server.UnitPath]; ok || !slices.Equal(s.h.units.Calls[before:], []string{reload}) {
			t.Errorf("unit file kept: %v, unit calls %v, want it removed and one reload", ok, s.h.units.Calls[before:])
		}
	})
	t.Run("the binary is removed without a reload", func(t *testing.T) {
		s := inInstalled(t)
		before := len(s.h.units.Calls)
		inverse(t, s, "derp.file:"+server.BinaryPath)
		if _, ok := s.h.files[server.BinaryPath]; ok || len(s.h.units.Calls) != before {
			t.Errorf("binary kept: %v, unit calls %v, want it removed and no unit call", ok, s.h.units.Calls[before:])
		}
	})
	t.Run("the service directory goes with what derper wrote in it", func(t *testing.T) {
		s := inInstalled(t)
		s.h.files[server.KeyPath] = []byte("node key")
		s.h.files[server.CertDir+"/"+inIP+".crt"] = []byte("certificate")
		inverse(t, s, "derp.dir:"+server.StateDir)
		for p := range s.h.files {
			if strings.HasPrefix(p, server.StateDir+"/") {
				t.Errorf("%s survived the removal of the service directory", p)
			}
		}
		if last := s.tr.calls[len(s.tr.calls)-1]; last.Call != "remove-dir "+server.StateDir {
			t.Errorf("last host change %+v, want the removal of the service directory", last)
		}
	})
	t.Run("the user is deleted and forgotten", func(t *testing.T) {
		s := inInstalled(t)
		inverse(t, s, "derp.user:"+server.ServiceUser)
		last := s.h.ran[len(s.h.ran)-1]
		if !slices.Equal(last, []string{"/usr/sbin/userdel", server.ServiceUser}) {
			t.Errorf("last command %v, want userdel", last)
		}
		if owned, err := platform.NewOwnership(s.h.db).Owns(ctx, ID, server.ServiceUser); err != nil || owned {
			t.Errorf("the user is still recorded as the module's: %v (error %v)", owned, err)
		}
	})
	t.Run("a user that is already gone is only forgotten", func(t *testing.T) {
		s := inInstalled(t)
		delete(s.h.users, server.ServiceUser)
		ran := len(s.h.ran)
		inverse(t, s, "derp.user:"+server.ServiceUser)
		if len(s.h.ran) != ran {
			t.Errorf("commands %v, want no userdel", s.h.ran[ran:])
		}
		if owned, err := platform.NewOwnership(s.h.db).Owns(ctx, ID, server.ServiceUser); err != nil || owned {
			t.Errorf("the user is still recorded as the module's: %v (error %v)", owned, err)
		}
	})
}

func TestInVerify(t *testing.T) {
	ctx := context.Background()
	in := inInput("ip", inIP, inBool(false))
	t.Run("an installed server whose unit is active", func(t *testing.T) {
		s := inInstalled(t)
		h := s.d.serverInstall().Handler
		if err := h.Verify(ctx, s.env, in, json.RawMessage(`{}`)); err != nil {
			t.Errorf("verify without the output, as recovery calls it: %v", err)
		}
	})
	t.Run("an inactive unit", func(t *testing.T) {
		s := inInstalled(t)
		s.setUnit(core.UnitState{LoadState: "loaded", ActiveState: "inactive"})
		if err := s.d.serverInstall().Handler.Verify(ctx, s.env, in, json.RawMessage(`{}`)); err == nil {
			t.Error("verify accepted an inactive unit")
		}
	})
	t.Run("no server row", func(t *testing.T) {
		s := inInstalled(t)
		if err := deleteLocalServer(ctx, s.h.db); err != nil {
			t.Fatal(err)
		}
		if err := s.d.serverInstall().Handler.Verify(ctx, s.env, in, json.RawMessage(`{}`)); err == nil {
			t.Error("verify accepted a host without a server row")
		}
	})
	t.Run("a row of another host name", func(t *testing.T) {
		s := inInstalled(t)
		if err := s.d.serverInstall().Handler.Verify(ctx, s.env, inInput("ip", "93.184.216.35", inBool(false)), json.RawMessage(`{}`)); err == nil {
			t.Error("verify accepted a server installed for another address")
		}
	})
}
