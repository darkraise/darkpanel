package tailscale

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/agent/daemon"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/paths"
	"github.com/darkraise/darkpanel/internal/platform"
	"github.com/darkraise/darkpanel/internal/transport"
)

const (
	pnPrior   = "1.82.0"
	pnTarget  = "1.90.1"
	pnAptGet  = "/usr/bin/apt-get"
	pnAptMark = "/usr/bin/apt-mark"
	pnDpkg    = "/usr/bin/dpkg"
	pnArchive = "/var/cache/apt/archives"
)

func pnDebName(version string) string { return "tailscale_" + version + "_amd64.deb" }
func pnDebPath(version string) string { return debDir + "/" + pnDebName(version) }
func pnDeb(version string) []byte     { return []byte("deb-of-" + version) }

func pnSum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func pnVec(program string, args []string) string {
	return strings.Join(append([]string{program}, args...), " ")
}

func pnRan(program string, args ...string) []string { return append([]string{program}, args...) }

func pnInput(version string) json.RawMessage {
	b, err := json.Marshal(PinInput{Version: version})
	if err != nil {
		panic(err)
	}
	return b
}

// pnApt stands in for apt-get, apt-mark and dpkg on the test host. It passes every vector to the host's runner,
// which records it, refuses what the production allowlist refuses and answers scripted results, and for a vector
// that exited 0 it leaves what the real program would: the hold, the package files in apt's archive and the
// version in dpkg's status file. phases holds the phase of change c1's pin row as each vector started.
type pnApt struct {
	h       *tsHost
	version string
	held    bool
	backend map[string]string // the backend state tailscaled reports once a version is installed
	missing map[string]bool   // versions apt cannot download
	phases  []string
}

func pnNewApt(h *tsHost, version string, held bool) *pnApt {
	a := &pnApt{h: h, version: version, held: held, backend: map[string]string{}, missing: map[string]bool{}}
	a.sync()
	return a
}

func (a *pnApt) sync() {
	a.h.mu.Lock()
	defer a.h.mu.Unlock()
	a.h.reads[thDpkgPath] = thDpkgStatus(a.version)
}

func (a *pnApt) install(version string) {
	a.version = version
	a.sync()
	if state, ok := a.backend[version]; ok {
		a.h.local.update(func() { a.h.local.Status.BackendState = state })
	}
}

func (a *pnApt) Run(ctx context.Context, cmd core.Cmd) (core.CmdResult, error) {
	pin, _, _ := loadPin(ctx, a.h.db.Read(), "c1")
	a.phases = append(a.phases, pin.Phase)
	res, err := a.h.Run(ctx, cmd)
	if err != nil || res.ExitCode != 0 {
		return res, err
	}
	switch cmd.Path {
	case pnAptMark:
		switch cmd.Args[0] {
		case "showhold":
			if res.Stdout == nil && a.held {
				res.Stdout = []byte("tailscale\n")
			}
		case "hold":
			a.held = true
		case "unhold":
			a.held = false
		}
	case pnAptGet:
		last := cmd.Args[len(cmd.Args)-1]
		switch {
		case slices.Contains(cmd.Args, "--download-only"):
			version := strings.TrimPrefix(last, "tailscale=")
			if a.missing[version] {
				return core.CmdResult{ExitCode: 100, Stderr: []byte("E: Version '" + version + "' for 'tailscale' was not found\n")}, nil
			}
			a.h.mu.Lock()
			a.h.reads[pnArchive+"/"+pnDebName(version)] = pnDeb(version)
			a.h.mu.Unlock()
		case strings.HasPrefix(last, "tailscale="):
			a.install(strings.TrimPrefix(last, "tailscale="))
		case strings.HasSuffix(last, ".deb"):
			version := strings.TrimSuffix(strings.TrimPrefix(path.Base(last), "tailscale_"), "_amd64.deb")
			a.h.mu.Lock()
			got := a.h.files[last]
			a.h.mu.Unlock()
			if !bytes.Equal(got, pnDeb(version)) {
				return core.CmdResult{ExitCode: 100, Stderr: []byte("E: Sub-process /usr/bin/dpkg returned an error code (1)\n")}, nil
			}
			a.install(version)
		}
	}
	return res, nil
}

// pnSetup is a host with tailscale pnPrior installed, running, its repository files in place, and change c1's
// environment, whose runner is the pnApt.
type pnSetup struct {
	h   *tsHost
	t   ts
	apt *pnApt
	env core.Env
}

func pnNewSetup(t *testing.T, held bool) *pnSetup {
	t.Helper()
	h := newTSHost(t)
	tt := h.ts()
	keyring, sources, err := tt.repoFiles()
	if err != nil {
		t.Fatal(err)
	}
	h.reads[keyringPath], h.reads[sourcesPath] = keyring, sources
	apt := pnNewApt(h, pnPrior, held)
	env := h.env("c1")
	env.Exec = apt
	return &pnSetup{h: h, t: tt, apt: apt, env: env}
}

// planEnv is the environment of a plan: no change, so no journal.
func (s *pnSetup) planEnv() core.Env {
	env := s.h.env("")
	env.Exec = s.apt
	return env
}

// pnDrive runs call while advancing the host's clock by one poll whenever it waits; tick, when set, runs before
// each advance with the number of advances so far.
func pnDrive(t *testing.T, h *tsHost, tick func(n int), call func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		call()
	}()
	n := 0
	for deadline := time.Now().Add(20 * time.Second); ; {
		select {
		case <-done:
			return
		default:
		}
		if h.clock.Pending() > 0 {
			if tick != nil {
				tick(n)
			}
			n++
			h.clock.Advance(pinTxPoll)
		} else {
			time.Sleep(time.Millisecond)
		}
		if time.Now().After(deadline) {
			t.Fatal("the call did not finish")
		}
	}
}

// pnStep is a recorded step: its name and result.
type pnStep struct{ Step, Result string }

func pnSteps(t *testing.T, h *tsHost, id string) []pnStep {
	t.Helper()
	rows, err := h.db.Read().QueryContext(context.Background(), `SELECT step, result FROM tailscale_pin_steps WHERE change_id = ? ORDER BY seq`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []pnStep
	for rows.Next() {
		var s pnStep
		if err := rows.Scan(&s.Step, &s.Result); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// pnStepNames lists the steps' names; a result that starts with "failed" is marked with a trailing "!".
func pnStepNames(steps []pnStep) []string {
	var out []string
	for _, s := range steps {
		name := s.Step
		if strings.HasPrefix(s.Result, "failed: ") {
			name += "!"
		} else if s.Result != "ok" {
			name += "?" + s.Result
		}
		out = append(out, name)
	}
	return out
}

func pnPin(t *testing.T, h *tsHost) pinRow {
	t.Helper()
	p, ok, err := loadPin(context.Background(), h.db.Read(), "c1")
	if err != nil || !ok {
		t.Fatalf("pin row of c1: found %v, error %v", ok, err)
	}
	return p
}

// pnPinEffect returns the effect the pin journaled, whatever else the change journaled.
func pnPinEffect(t *testing.T, h *tsHost) core.Effect {
	t.Helper()
	var found []core.Effect
	for _, e := range h.journal.effects() {
		if e.Resource == "tailscale.package:tailscale" {
			found = append(found, e)
		}
	}
	if len(found) != 1 {
		t.Fatalf("%d effects on tailscale.package:tailscale, want 1: %+v", len(found), h.journal.effects())
	}
	return found[0]
}

func pnCoded(t *testing.T, err error, code errcode.Code) *errcode.Error {
	t.Helper()
	var coded *errcode.Error
	if !errors.As(err, &coded) || coded.Code != code {
		t.Fatalf("error %v, want code %s", err, code)
	}
	return coded
}

func TestPnSessionFake(t *testing.T) {
	ctx := context.Background()
	s := &tsSession{}
	if up, via, err := s.answer(ctx); !up || via || err != nil || s.Calls != 1 {
		t.Errorf("default answer = %v %v %v after %d calls, want up, not via the tailnet, no error, 1 call", up, via, err, s.Calls)
	}
	boom := errors.New("socket closed")
	s = &tsSession{Answers: []tsSessionAnswer{{Up: true, ViaTailnet: true}, {Err: boom}}}
	for i, want := range []tsSessionAnswer{{Up: true, ViaTailnet: true}, {Err: boom}, {Err: boom}} {
		up, via, err := s.answer(ctx)
		if up != want.Up || via != want.ViaTailnet || !errors.Is(err, want.Err) {
			t.Errorf("call %d = %v %v %v, want %+v", i, up, via, err, want)
		}
	}
	h := newTSHost(t)
	if up, _, err := h.ts().d.session(ctx); !up || err != nil || h.session.Calls != 1 {
		t.Errorf("the host's ts answers %v, %v after %d calls, want its session fake consulted once", up, err, h.session.Calls)
	}
}

func TestPnProductionTSWiresTheSession(t *testing.T) {
	if productionTS().d.session == nil {
		t.Fatal("productionTS has no session")
	}
}

// pnCredentials saves an enrolled agent's credentials for a controller at address in dir.
func pnCredentials(t *testing.T, dir, address string) {
	t.Helper()
	caPub, caKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test agent root"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, caPub, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	uri, err := url.Parse(transport.HostURI("h1"))
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(12 * time.Hour),
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, URIs: []*url.URL{uri}, KeyUsage: x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, pub, caKey)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	c := &daemon.Credentials{Key: key, Cert: cert, CA: ca, Controller: daemon.Endpoint{Address: address, SAN: transport.ControllerURI("C1")}}
	if _, err := daemon.SaveGeneration(dir, c, daemon.AnyCurrent); err != nil {
		t.Fatal(err)
	}
}

// pnServeAgent answers GET hello on the Unix socket with body, as the agent socket does.
func pnServeAgent(t *testing.T, socket, body string) {
	t.Helper()
	l, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/agent/v1/hello" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	_ = srv.Listener.Close()
	srv.Listener = l
	srv.Start()
	t.Cleanup(srv.Close)
}

func TestPnSessionAt(t *testing.T) {
	tests := []struct {
		name    string
		address string // "" leaves the agent not enrolled
		hello   string // "" starts no agent socket
		up, via bool
		wantErr bool
	}{
		{name: "not enrolled", up: false, via: false},
		{name: "enrolled over the tailnet and connected", address: "100.64.0.9:7443", hello: `{"controllerConnected":true}`, up: true, via: true},
		{name: "enrolled over an IPv6 tailnet address", address: "[fd7a:115c:a1e0::9]:7443", hello: `{"controllerConnected":true}`, up: true, via: true},
		{name: "enrolled over a LAN address", address: "192.0.2.5:7443", hello: `{"controllerConnected":true}`, up: true, via: false},
		{name: "enrolled by name", address: "controller.example.com:7443", hello: `{"controllerConnected":true}`, up: true, via: false},
		{name: "the controller session is down", address: "100.64.0.9:7443", hello: `{"controllerConnected":false}`, up: false, via: true},
		{name: "the agent reports no controller", address: "100.64.0.9:7443", hello: `{"controllerConnected":null}`, up: false, via: true},
		{name: "the agent socket does not answer", address: "100.64.0.9:7443", up: false, via: true, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root, err := os.MkdirTemp("", "pn")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(root) })
			p := paths.Under(root)
			if tc.address != "" {
				pnCredentials(t, p.AgentTLSDir, tc.address)
			}
			if err := os.MkdirAll(path.Dir(p.AgentSocket), 0o700); err != nil {
				t.Fatal(err)
			}
			if tc.hello != "" {
				pnServeAgent(t, p.AgentSocket, tc.hello)
			}
			up, via, err := sessionAt(context.Background(), p)
			if (err != nil) != tc.wantErr || up != tc.up || via != tc.via {
				t.Errorf("sessionAt = up %v, via %v, error %v; want up %v, via %v, error %v", up, via, err, tc.up, tc.via, tc.wantErr)
			}
		})
	}
}

func TestPnPackagePinSpec(t *testing.T) {
	def := pnNewSetup(t, false).t.packagePin()
	sp, h := def.Spec, def.Handler
	if sp.Name != "tailscale.package.pin" || sp.Version != 1 || sp.Class != core.ClassCompensable || sp.Side != core.SideAgent ||
		!slices.Equal(sp.LockKeys, []string{"tailscale"}) || sp.ApplyTimeout != 20*time.Minute || !sp.Task || !sp.LongRunning ||
		sp.Available == nil || len(sp.Guards) != 0 || sp.Internal {
		t.Errorf("spec %+v, want a long-running compensable agent task locking tailscale for 20 minutes, with an availability check and no guard", sp)
	}
	if def.InputType != reflect.TypeFor[PinInput]() || def.OutputType != reflect.TypeFor[Pinned]() {
		t.Errorf("input %v and output %v, want PinInput and Pinned", def.InputType, def.OutputType)
	}
	if h.Plan == nil || h.Task == nil || h.Verify == nil || h.Apply != nil {
		t.Errorf("handler has Plan %v Task %v Verify %v Apply %v, want a task handler", h.Plan != nil, h.Task != nil, h.Verify != nil, h.Apply != nil)
	}
}

func TestPnPlan(t *testing.T) {
	ctx := context.Background()
	line := func(op, text string) core.DiffLine { return core.DiffLine{Op: op, Text: text} }
	rollback := line(" ", "roll back to tailscale 1.82.0 if the node is not back within 120 s")
	tests := []struct {
		name    string
		held    bool
		version string
		want    []core.DiffLine
	}{
		{"a new version, not held", false, pnTarget, []core.DiffLine{
			line("-", "tailscale 1.82.0"), line("+", "tailscale 1.90.1"), line("+", "hold tailscale at 1.90.1"), rollback}},
		{"a new version, held", true, pnTarget, []core.DiffLine{
			line("-", "tailscale 1.82.0"), line("+", "tailscale 1.90.1"), line(" ", "tailscale is held now; the hold moves to 1.90.1"), rollback}},
		{"the installed version", false, pnPrior, []core.DiffLine{
			line(" ", "tailscale 1.82.0 stays installed"), line("+", "hold tailscale at 1.82.0"), rollback}},
	}
	hashes := map[string]string{}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := pnNewSetup(t, tc.held)
			out, err := s.t.packagePin().Handler.Plan(ctx, s.planEnv(), pnInput(tc.version))
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(out.Diff, tc.want) || out.Confirmation != "" || len(out.Guards) != 0 || len(out.StateHash) != 64 {
				t.Errorf("plan %+v, want diff %+v, no confirmation, no guard and a 64-digit hash", out, tc.want)
			}
			if want := [][]string{pnRan(pnAptMark, "showhold")}; !reflect.DeepEqual(s.h.ran, want) || len(s.h.files) != 0 {
				t.Errorf("plan ran %v and wrote %v, want only the read of the holds", s.h.ran, s.h.files)
			}
			again, err := s.t.packagePin().Handler.Plan(ctx, s.planEnv(), pnInput(tc.version))
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

func TestPnRefusals(t *testing.T) {
	tests := []struct {
		name    string
		version string
		setup   func(s *pnSetup)
		code    errcode.Code
		field   string
	}{
		{"a version that is not a package version", "1.90.1 --reinstall", nil, errcode.InvalidInput, "/version"},
		{"a version that starts with a letter", "v1.90.1", nil, errcode.InvalidInput, "/version"},
		{"no package", pnTarget, func(s *pnSetup) { s.apt.version = ""; s.apt.sync() }, errcode.TailscaleUnavailable, ""},
		{"an OS without a repository", pnTarget, func(s *pnSetup) {
			s.t.d.osRelease = func() (osInfo, error) { return osInfo{ID: "ubuntu", Codename: "focal"}, nil }
		}, errcode.TailscaleUnavailable, ""},
		{"a prior version whose package name the allowlist would refuse", pnTarget, func(s *pnSetup) {
			s.apt.version = "1.82.0~rc1"
			s.apt.sync()
		}, errcode.TailscalePinFailed, ""},
		{"a prior version with a capital letter", pnTarget, func(s *pnSetup) { s.apt.version = "1.82.0-T1"; s.apt.sync() }, errcode.TailscalePinFailed, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := pnNewSetup(t, false)
			if tc.setup != nil {
				tc.setup(s)
			}
			h := s.t.packagePin().Handler
			_, planErr := h.Plan(context.Background(), s.planEnv(), pnInput(tc.version))
			_, taskErr := h.Task(context.Background(), s.env, pnInput(tc.version))
			for what, err := range map[string]error{"plan": planErr, "task": taskErr} {
				if !core.IsDefinite(err) {
					t.Errorf("%s: error %v is not definite", what, err)
				}
				coded := pnCoded(t, err, tc.code)
				if tc.field != "" && (len(coded.Fields) != 1 || coded.Fields[0].Path != tc.field) {
					t.Errorf("%s: fields %+v, want one at %s", what, coded.Fields, tc.field)
				}
			}
			if _, ok, _ := loadPin(context.Background(), s.h.db.Read(), "c1"); ok || len(s.h.journal.effects()) != 0 || len(s.h.files) != 0 || len(s.h.ran) != 0 {
				t.Errorf("a refusal left a pin row (%v), effects %v or files %v, or ran %v", ok, s.h.journal.effects(), s.h.files, s.h.ran)
			}
		})
	}
}

func TestPnTaskPinsAndHolds(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run(map[bool]string{false: "not held", true: "held"}[held], func(t *testing.T) {
			ctx := context.Background()
			s := pnNewSetup(t, held)
			def := s.t.packagePin()
			raw, err := def.Handler.Task(ctx, s.env, pnInput(pnTarget))
			if err != nil {
				t.Fatal(err)
			}
			var out Pinned
			if err := json.Unmarshal(raw, &out); err != nil || out != (Pinned{Version: pnTarget, PriorVersion: pnPrior, PriorHeld: held}) {
				t.Errorf("output %s (error %v), want the new version, the prior version and the prior hold", raw, err)
			}
			wantRan := [][]string{
				pnRan(pnAptMark, "showhold"),
				pnRan(pnAptMark, "unhold", "tailscale"),
				pnRan(pnAptGet, platform.AptUpdateTailscale()...),
				pnRan(pnAptGet, platform.AptStageVersion(pnPrior)...),
				pnRan(pnAptGet, platform.AptStageVersion(pnTarget)...),
				pnRan(pnAptGet, platform.AptInstallVersion(pnTarget)...),
				pnRan(pnAptMark, "hold", "tailscale"),
			}
			if !reflect.DeepEqual(s.h.ran, wantRan) {
				t.Errorf("ran %v, want %v", s.h.ran, wantRan)
			}
			// Each phase is journaled before the vector it covers runs.
			if want := []string{"", "unheld", "staged", "staged", "staged", "installed", "held"}; !slices.Equal(s.apt.phases, want) {
				t.Errorf("pin phase at each vector %v, want %v", s.apt.phases, want)
			}
			want := pinRow{
				ChangeID: "c1", Version: pnTarget, Arch: "amd64", PriorVersion: pnPrior, PriorHeld: held, PriorBackend: "Running",
				Phase: "done", StagedDeb: pnDebName(pnPrior), StagedSHA256: pnSum(pnDeb(pnPrior)),
			}
			got := pnPin(t, s.h)
			if !got.UpdatedAt.Equal(s.h.clock.Now()) {
				t.Errorf("updated_at %v, want %v", got.UpdatedAt, s.h.clock.Now())
			}
			got.UpdatedAt = time.Time{}
			if got != want {
				t.Errorf("pin row %+v, want %+v", got, want)
			}
			steps := []string{"unhold", "stage-prior", "stage-target", "copy-prior", "install", "hold", "check"}
			if got := pnStepNames(pnSteps(t, s.h, "c1")); !slices.Equal(got, steps) {
				t.Errorf("steps %v, want %v, each ok", got, steps)
			}
			if !bytes.Equal(s.h.files[pnDebPath(pnPrior)], pnDeb(pnPrior)) || len(s.h.files) != 1 {
				t.Errorf("files %v, want only the prior package staged in %s", s.h.files, debDir)
			}
			e := pnPinEffect(t, s.h)
			wantBefore := `{"version":"1.82.0","held":` + map[bool]string{false: "false", true: "true"}[held] + `}`
			if e.Kind != core.EffectModify || !e.Local || !e.InverseLocal || e.State != core.EffectMade || string(e.Before.State) != wantBefore ||
				e.After == nil || string(e.After.State) != `{"version":"1.90.1","held":true}` || e.Post == nil || string(e.Post.State) != string(e.After.State) {
				t.Errorf("effect %+v, want a made, local modification from %s to the new version, held", e, wantBefore)
			}
			if err := def.Handler.Verify(ctx, s.env, pnInput(pnTarget), raw); err != nil {
				t.Errorf("verify after the pin: %v", err)
			}
			if err := def.Handler.Verify(ctx, s.env, pnInput(pnTarget), json.RawMessage(`{}`)); err != nil {
				t.Errorf("verify without the output, as recovery calls it: %v", err)
			}
		})
	}
}

func TestPnTaskWritesAbsentRepositoryFiles(t *testing.T) {
	s := pnNewSetup(t, false)
	delete(s.h.reads, keyringPath)
	delete(s.h.reads, sourcesPath)
	if _, err := s.t.packagePin().Handler.Task(context.Background(), s.env, pnInput(pnTarget)); err != nil {
		t.Fatal(err)
	}
	keyring, sources, _ := s.t.repoFiles()
	if !bytes.Equal(s.h.files[keyringPath], keyring) || !bytes.Equal(s.h.files[sourcesPath], sources) {
		t.Errorf("files %v, want the keyring and the source list written", s.h.files)
	}
	if pnPin(t, s.h).Phase != "done" {
		t.Error("the pin did not finish")
	}
	if es := s.h.journal.effects(); len(es) != 1 || es[0].Resource != "tailscale.package:tailscale" {
		t.Errorf("journaled %+v, want only the package effect: the repository files are owned but not journaled, since the pin observes and undoes only the package", es)
	}
}

func TestPnTaskRefusesAForeignRepository(t *testing.T) {
	s := pnNewSetup(t, false)
	s.h.reads[sourcesPath] = []byte("deb https://example.com/apt stable main\n")
	_, err := s.t.packagePin().Handler.Task(context.Background(), s.env, pnInput(pnTarget))
	_ = pnCoded(t, err, errcode.TailscaleRepoConflict)
	if want := [][]string{pnRan(pnAptMark, "showhold")}; !reflect.DeepEqual(s.h.ran, want) || len(s.h.files) != 0 {
		t.Errorf("ran %v and wrote %v, want only the read of the holds", s.h.ran, s.h.files)
	}
	if p := pnPin(t, s.h); p.Phase != "rolled_back" {
		t.Errorf("pin phase %s, want rolled_back: nothing had changed", p.Phase)
	}
	if got := pnSteps(t, s.h, "c1"); len(got) != 0 {
		t.Errorf("steps %v, want none", got)
	}
	if e := pnPinEffect(t, s.h); e.State != core.EffectSkipped {
		t.Errorf("effect state %s, want skipped", e.State)
	}
}

func TestPnTaskRollsBackAFailedStep(t *testing.T) {
	failed := core.CmdResult{ExitCode: 100, Stderr: []byte("E: Could not get lock\n")}
	showhold := pnRan(pnAptMark, "showhold")
	tests := []struct {
		name     string
		held     bool
		setup    func(s *pnSetup)
		failStep string
		failedAt int        // the number of vectors run up to and including the failing one
		after    [][]string // the vectors the rollback ran
		steps    []string
		restarts int
	}{
		{"the prior version cannot be downloaded", false, func(s *pnSetup) { s.apt.missing[pnPrior] = true }, "stage-prior", 4,
			[][]string{showhold, showhold},
			[]string{"unhold", "stage-prior!", "rollback-verify"}, 0},
		{"the prior version cannot be downloaded and was held", true, func(s *pnSetup) { s.apt.missing[pnPrior] = true }, "stage-prior", 4,
			[][]string{showhold, pnRan(pnAptMark, "hold", "tailscale"), showhold},
			[]string{"unhold", "stage-prior!", "rollback-hold", "rollback-verify"}, 0},
		{"the target version cannot be downloaded", false, func(s *pnSetup) { s.apt.missing[pnTarget] = true }, "stage-target", 5,
			[][]string{showhold, showhold},
			[]string{"unhold", "stage-prior", "stage-target!", "rollback-verify"}, 0},
		{"the install fails", false, func(s *pnSetup) {
			s.h.script(pnVec(pnAptGet, platform.AptInstallVersion(pnTarget)), tsRun{res: failed})
		}, "install", 6,
			[][]string{showhold, pnRan(pnDpkg, "--configure", "-a"), pnRan(pnAptGet, platform.AptInstallDeb(pnDebPath(pnPrior))...), showhold},
			[]string{"unhold", "stage-prior", "stage-target", "copy-prior", "install!", "rollback-configure", "rollback-install", "rollback-restart", "rollback-verify"}, 1},
		{"the hold fails", true, func(s *pnSetup) {
			s.h.script(pnVec(pnAptMark, []string{"hold", "tailscale"}), tsRun{res: failed})
		}, "hold", 7,
			[][]string{showhold, pnRan(pnAptGet, platform.AptInstallDeb(pnDebPath(pnPrior))...), pnRan(pnAptMark, "hold", "tailscale"), showhold},
			[]string{"unhold", "stage-prior", "stage-target", "copy-prior", "install", "hold!", "rollback-install", "rollback-hold", "rollback-restart", "rollback-verify"}, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := pnNewSetup(t, tc.held)
			tc.setup(s)
			_, err := s.t.packagePin().Handler.Task(context.Background(), s.env, pnInput(pnTarget))
			coded := pnCoded(t, err, errcode.TailscalePinFailed)
			if !strings.Contains(coded.Detail, tc.failStep) || !strings.Contains(coded.Detail, "tailscale is back at "+pnPrior) {
				t.Errorf("detail %q, want it to name %s and the restored version", coded.Detail, tc.failStep)
			}
			if len(s.h.ran) < tc.failedAt || !reflect.DeepEqual(s.h.ran[tc.failedAt:], tc.after) {
				t.Errorf("ran %v; after the first %d vectors want %v", s.h.ran, tc.failedAt, tc.after)
			}
			if got := pnStepNames(pnSteps(t, s.h, "c1")); !slices.Equal(got, tc.steps) {
				t.Errorf("steps %v, want %v", got, tc.steps)
			}
			if p := pnPin(t, s.h); p.Phase != "rolled_back" {
				t.Errorf("pin phase %s, want rolled_back", p.Phase)
			}
			if s.apt.version != pnPrior || s.apt.held != tc.held {
				t.Errorf("host has version %s, held %v; want %s, held %v", s.apt.version, s.apt.held, pnPrior, tc.held)
			}
			if len(s.h.units.Restarts) != tc.restarts {
				t.Errorf("tailscaled restarted %v, want %d restarts", s.h.units.Restarts, tc.restarts)
			}
			if e := pnPinEffect(t, s.h); e.State != core.EffectSkipped {
				t.Errorf("effect state %s, want skipped: the host is as before", e.State)
			}
		})
	}
}

func TestPnTaskRollsBackAFailedCheck(t *testing.T) {
	tests := []struct {
		name    string
		answers []tsSessionAnswer
		backend string // the backend state after the new version is installed
		link    bool
	}{
		{"the node does not return to its backend state", nil, "Stopped", false},
		{"the controller link stays down", []tsSessionAnswer{{Up: true, ViaTailnet: true}, {Up: false}}, "Running", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := pnNewSetup(t, false)
			s.h.session.Answers = tc.answers
			s.apt.backend[pnTarget], s.apt.backend[pnPrior] = tc.backend, "Running"
			start := s.h.clock.Now()
			var err error
			pnDrive(t, s.h, nil, func() {
				_, err = s.t.packagePin().Handler.Task(context.Background(), s.env, pnInput(pnTarget))
			})
			coded := pnCoded(t, err, errcode.TailscalePinFailed)
			if !strings.Contains(coded.Detail, "check") || !strings.Contains(coded.Detail, "tailscale is back at "+pnPrior) {
				t.Errorf("detail %q, want it to name the check and the restored version", coded.Detail)
			}
			if waited := s.h.clock.Now().Sub(start); waited < pinCheckWindow || waited > pinCheckWindow+2*pinTxPoll {
				t.Errorf("the check waited %v, want the %v window", waited, pinCheckWindow)
			}
			if p := pnPin(t, s.h); p.Phase != "rolled_back" || p.LinkTailnet != tc.link {
				t.Errorf("pin row %+v, want rolled_back with link_tailnet %v", p, tc.link)
			}
			want := []string{"unhold", "stage-prior", "stage-target", "copy-prior", "install", "hold", "check!",
				"rollback-unhold", "rollback-install", "rollback-restart", "rollback-verify"}
			if got := pnStepNames(pnSteps(t, s.h, "c1")); !slices.Equal(got, want) {
				t.Errorf("steps %v, want %v", got, want)
			}
			if s.apt.version != pnPrior || s.apt.held || !reflect.DeepEqual(s.h.units.Restarts, []string{tailscaledUnit}) {
				t.Errorf("host has version %s, held %v, restarts %v; want %s, not held, one restart", s.apt.version, s.apt.held, s.h.units.Restarts, pnPrior)
			}
			if slices.ContainsFunc(s.h.ran, func(v []string) bool { return v[0] == pnDpkg }) {
				t.Error("dpkg --configure -a ran for a pin whose install finished")
			}
			if e := pnPinEffect(t, s.h); e.State != core.EffectSkipped {
				t.Errorf("effect state %s, want skipped", e.State)
			}
			if tc.link && s.h.session.Calls < 3 {
				t.Errorf("the session was asked %d times, want it polled", s.h.session.Calls)
			}
		})
	}
}

func TestPnTaskWaitsForTheNodeAndTheLink(t *testing.T) {
	s := pnNewSetup(t, false)
	s.h.session.Answers = []tsSessionAnswer{{Up: true, ViaTailnet: true}, {Up: false}, {Up: true}}
	s.apt.backend[pnTarget] = "Starting"
	start := s.h.clock.Now()
	var err error
	pnDrive(t, s.h, func(n int) {
		if n == 2 {
			s.h.local.update(func() { s.h.local.Status.BackendState = "Running" })
		}
	}, func() {
		_, err = s.t.packagePin().Handler.Task(context.Background(), s.env, pnInput(pnTarget))
	})
	if err != nil {
		t.Fatal(err)
	}
	if waited := s.h.clock.Now().Sub(start); waited != 4*pinTxPoll || s.h.session.Calls != 3 {
		t.Errorf("waited %v with %d session calls, want 4 polls and 3 calls (the first records the link)", waited, s.h.session.Calls)
	}
	if p := pnPin(t, s.h); p.Phase != "done" || !p.LinkTailnet {
		t.Errorf("pin row %+v, want done with link_tailnet", p)
	}
}

func TestPnTaskReportsAFailedRollback(t *testing.T) {
	s := pnNewSetup(t, false)
	s.apt.backend[pnTarget] = "Stopped"
	s.h.script(pnVec(pnAptGet, platform.AptInstallDeb(pnDebPath(pnPrior))), tsRun{res: core.CmdResult{ExitCode: 100, Stderr: []byte("E: dpkg was interrupted\n")}})
	var err error
	pnDrive(t, s.h, nil, func() {
		_, err = s.t.packagePin().Handler.Task(context.Background(), s.env, pnInput(pnTarget))
	})
	coded := pnCoded(t, err, errcode.TailscalePinFailed)
	if !strings.Contains(coded.Detail, "rollback-install") || !strings.Contains(coded.Detail, "needs repair") {
		t.Errorf("detail %q, want the failed rollback step and a call for repair", coded.Detail)
	}
	if p := pnPin(t, s.h); p.Phase != "failed" {
		t.Errorf("pin phase %s, want failed", p.Phase)
	}
	steps := pnSteps(t, s.h, "c1")
	if last := steps[len(steps)-1]; last.Step != "rollback-install" || !strings.HasPrefix(last.Result, "failed: ") || !strings.Contains(last.Result, "dpkg was interrupted") {
		t.Errorf("last step %+v, want rollback-install failed with apt's last line", last)
	}
	if e := pnPinEffect(t, s.h); e.State != core.EffectIntended {
		t.Errorf("effect state %s, want intended: the host is not as before", e.State)
	}
}

// pnRollbackHost inserts a pin row of change c1 in the phase and, when staged, the prior package the pin copied.
func pnRollbackHost(t *testing.T, held bool, row pinRow, stage bool) *pnSetup {
	t.Helper()
	s := pnNewSetup(t, held)
	s.apt.backend[pnPrior] = "Running"
	if row.Phase != "recorded" && row.Phase != "unheld" && row.Phase != "staged" {
		s.apt.install(pnTarget)
	}
	row.ChangeID, row.Version, row.Arch, row.PriorVersion, row.PriorBackend = "c1", pnTarget, "amd64", pnPrior, "Running"
	row.UpdatedAt = s.h.clock.Now()
	if stage {
		if row.StagedDeb == "" {
			row.StagedDeb = pnDebName(pnPrior)
		}
		row.StagedSHA256 = pnSum(pnDeb(pnPrior))
		s.h.files[pnDebPath(pnPrior)] = pnDeb(pnPrior)
	}
	ctx := context.Background()
	if err := s.h.db.Write(ctx, func(tx *sql.Tx) error { return insertPin(ctx, tx, row) }); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestPnRollbackByPhase(t *testing.T) {
	showhold := pnRan(pnAptMark, "showhold")
	unhold := pnRan(pnAptMark, "unhold", "tailscale")
	hold := pnRan(pnAptMark, "hold", "tailscale")
	configure := pnRan(pnDpkg, "--configure", "-a")
	install := pnRan(pnAptGet, platform.AptInstallDeb(pnDebPath(pnPrior))...)
	tests := []struct {
		name         string
		phase        string
		held         bool // the hold when the rollback starts
		priorHeld    bool
		staged       bool
		stagedDeb    string
		wantRan      [][]string
		wantSteps    []string
		wantRestarts int
	}{
		{"recorded: nothing changed", "recorded", false, false, false, "", nil, nil, 0},
		{"unheld restores the prior hold", "unheld", false, true, false, "",
			[][]string{showhold, hold, showhold}, []string{"rollback-hold", "rollback-verify"}, 0},
		{"staged installs nothing", "staged", false, false, true, "",
			[][]string{showhold, showhold}, []string{"rollback-verify"}, 0},
		{"installed configures dpkg first", "installed", false, false, true, "",
			[][]string{showhold, configure, install, showhold}, []string{"rollback-configure", "rollback-install", "rollback-restart", "rollback-verify"}, 1},
		{"held lifts the hold", "held", true, false, true, "",
			[][]string{showhold, unhold, install, showhold}, []string{"rollback-unhold", "rollback-install", "rollback-restart", "rollback-verify"}, 1},
		{"checking, with the staged package named by its full path", "checking", true, false, true, pnDebPath(pnPrior),
			[][]string{showhold, unhold, install, showhold}, []string{"rollback-unhold", "rollback-install", "rollback-restart", "rollback-verify"}, 1},
		{"done restores a prior hold", "done", true, true, true, "",
			[][]string{showhold, unhold, install, hold, showhold}, []string{"rollback-unhold", "rollback-install", "rollback-hold", "rollback-restart", "rollback-verify"}, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := pnRollbackHost(t, tc.held, pinRow{Phase: tc.phase, PriorHeld: tc.priorHeld, StagedDeb: tc.stagedDeb}, tc.staged)
			if err := (packagePinHandler{s.t}).rollback(context.Background(), s.env, pnPin(t, s.h)); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(s.h.ran, tc.wantRan) {
				t.Errorf("ran %v, want %v", s.h.ran, tc.wantRan)
			}
			if got := pnStepNames(pnSteps(t, s.h, "c1")); !slices.Equal(got, tc.wantSteps) {
				t.Errorf("steps %v, want %v", got, tc.wantSteps)
			}
			if p := pnPin(t, s.h); p.Phase != "rolled_back" {
				t.Errorf("phase %s, want rolled_back", p.Phase)
			}
			if s.apt.version != pnPrior || s.apt.held != tc.priorHeld || len(s.h.units.Restarts) != tc.wantRestarts {
				t.Errorf("host has version %s, held %v, %d restarts; want %s, held %v, %d", s.apt.version, s.apt.held, len(s.h.units.Restarts), pnPrior, tc.priorHeld, tc.wantRestarts)
			}
		})
	}
}

func TestPnRollbackRefusesWhatItCannotTrust(t *testing.T) {
	tests := []struct {
		name  string
		row   pinRow
		stage bool
		setup func(s *pnSetup)
		want  string
	}{
		{"a staged package that is not what was recorded", pinRow{Phase: "checking"}, true,
			func(s *pnSetup) { s.h.files[pnDebPath(pnPrior)] = []byte("tampered") }, "SHA-256"},
		{"no staged package recorded", pinRow{Phase: "checking"}, false, nil, "staged"},
		{"a staged package outside the managed directory", pinRow{Phase: "checking", StagedDeb: "/tmp/tailscale_1.82.0_amd64.deb"}, true, nil, "not in " + debDir},
		{"a staged package that is gone", pinRow{Phase: "checking"}, true, func(s *pnSetup) { delete(s.h.files, pnDebPath(pnPrior)) }, "staged package"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := pnRollbackHost(t, true, tc.row, tc.stage)
			if tc.setup != nil {
				tc.setup(s)
			}
			err := (packagePinHandler{s.t}).rollback(context.Background(), s.env, pnPin(t, s.h))
			if err == nil || !strings.Contains(err.Error(), "rollback-install") || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("rollback = %v, want a failure of rollback-install naming %q", err, tc.want)
			}
			for _, v := range s.h.ran {
				if v[0] == pnAptGet {
					t.Errorf("apt-get ran %v with a package that cannot be trusted", v)
				}
			}
			if p := pnPin(t, s.h); p.Phase != "failed" {
				t.Errorf("phase %s, want failed", p.Phase)
			}
			steps := pnSteps(t, s.h, "c1")
			if last := steps[len(steps)-1]; last.Step != "rollback-install" || !strings.HasPrefix(last.Result, "failed: ") {
				t.Errorf("last step %+v, want rollback-install failed", last)
			}
		})
	}
}

func TestPnRollbackVerifyWaitsAndGivesUp(t *testing.T) {
	s := pnRollbackHost(t, true, pinRow{Phase: "checking"}, true)
	s.apt.backend[pnPrior] = "Stopped"
	start := s.h.clock.Now()
	var err error
	pnDrive(t, s.h, nil, func() { err = (packagePinHandler{s.t}).rollback(context.Background(), s.env, pnPin(t, s.h)) })
	if err == nil || !strings.Contains(err.Error(), "rollback-verify") || !strings.Contains(err.Error(), "Stopped") {
		t.Fatalf("rollback = %v, want rollback-verify to fail naming the backend state", err)
	}
	if waited := s.h.clock.Now().Sub(start); waited < pinCheckWindow || waited > pinCheckWindow+2*pinTxPoll {
		t.Errorf("waited %v, want the %v window", waited, pinCheckWindow)
	}
	if p := pnPin(t, s.h); p.Phase != "failed" {
		t.Errorf("phase %s, want failed", p.Phase)
	}
}

func TestPnVerify(t *testing.T) {
	ctx := context.Background()
	s := pnNewSetup(t, true)
	h := s.t.packagePin().Handler
	if err := h.Verify(ctx, s.env, pnInput(pnTarget), json.RawMessage(`{}`)); err == nil || !strings.Contains(err.Error(), pnPrior) {
		t.Errorf("verify with another version installed = %v, want an error naming it", err)
	}
	s.apt.install(pnTarget)
	s.apt.held = false
	if err := h.Verify(ctx, s.env, pnInput(pnTarget), json.RawMessage(`{}`)); err == nil || !strings.Contains(err.Error(), "held") {
		t.Errorf("verify without the hold = %v, want an error about the hold", err)
	}
	s.apt.held = true
	if err := h.Verify(ctx, s.env, pnInput(pnTarget), json.RawMessage(`{}`)); err != nil {
		t.Errorf("verify of a pinned host: %v", err)
	}
}
