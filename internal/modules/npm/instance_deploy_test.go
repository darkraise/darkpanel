package npm

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/npm/fake"
	"github.com/darkraise/darkpanel/internal/modules/npm/npmapi"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
	"github.com/darkraise/darkpanel/internal/platform/netstat"
)

const dpEmail = "ops@darkpanel.dev"

var dpPassword = base64.RawURLEncoding.EncodeToString(thRandomBytes(32))

func dpInput(email string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"acmeEmail":%q}`, email))
}

func dpCode(t *testing.T, err error) errcode.Code {
	t.Helper()
	var coded *errcode.Error
	if !errors.As(err, &coded) {
		t.Fatalf("error %v is not an *errcode.Error", err)
	}
	return coded.Code
}

// dpApply runs the deploy's Apply as change id and decodes its output.
func dpApply(h *thHost, n nm, id string) (Deployed, error) {
	out, err := n.instanceDeploy().Handler.Apply(context.Background(), h.env(id), dpInput(dpEmail))
	var d Deployed
	if err == nil {
		if err := json.Unmarshal(out, &d); err != nil {
			h.t.Fatalf("output %s: %v", out, err)
		}
	}
	return d, err
}

// dpDrive runs call and advances the host's clock by step whenever call waits on it.
func dpDrive(t *testing.T, h *thHost, step time.Duration, call func()) {
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
			h.clock.Advance(step)
		} else {
			time.Sleep(time.Millisecond)
		}
		if time.Now().After(deadline) {
			t.Fatal("the call did not finish")
		}
	}
}

// dpHealth is the NPM client with Health answering 502 for its first failing calls (all of them when failing is
// negative).
type dpHealth struct {
	npmAPI
	mu      sync.Mutex
	failing int
	calls   int
}

func (a *dpHealth) Health(ctx context.Context) (npmapi.Health, error) {
	a.mu.Lock()
	a.calls++
	fail := a.failing < 0 || a.calls <= a.failing
	a.mu.Unlock()
	if fail {
		return npmapi.Health{}, &npmapi.APIError{Status: http.StatusBadGateway, Message: "not up yet"}
	}
	return a.npmAPI.Health(ctx)
}

func dpFlaky(h *thHost, failing int) (nm, *dpHealth) {
	n := h.nm()
	live := n.d.anon()
	probe := &dpHealth{npmAPI: live, failing: failing}
	n.d.anon = func() npmAPI { return probe }
	return n, probe
}

func dpEffects(t *testing.T, h *thHost) []core.Effect {
	t.Helper()
	return h.journal.effects()
}

// dpMutations are the Docker calls that change the Engine.
var dpMutations = []string{"CreateNetwork", "CreateVolume", "CreateContainer", "StartContainer", "StopContainer", "RemoveContainer",
	"RemoveVolume", "RemoveNetwork", "PullImage", "ConnectNetwork", "DisconnectNetwork", "CopyToContainer"}

func dpMutated(h *thHost) []string {
	var got []string
	for _, c := range h.docker.calls() {
		if slices.Contains(dpMutations, c) {
			got = append(got, c)
		}
	}
	return got
}

func TestDpSpec(t *testing.T) {
	def := newTHHost(t).nm().instanceDeploy()
	s := def.Spec
	if s.Name != "npm.instance.deploy" || s.Version != 1 || s.Class != core.ClassCompensable || s.Side != core.SideAgent ||
		!slices.Equal(s.LockKeys, []string{"npm"}) || s.ApplyTimeout != 20*time.Minute || !s.LongRunning || s.Available == nil || s.Summary == "" {
		t.Errorf("spec %+v", s)
	}
	h := def.Handler
	if h.Plan == nil || h.Apply == nil || h.Verify == nil || h.Observe == nil || h.Inverse == nil {
		t.Errorf("handler lacks a stage: plan %t apply %t verify %t observe %t inverse %t",
			h.Plan != nil, h.Apply != nil, h.Verify != nil, h.Observe != nil, h.Inverse != nil)
	}
}

func TestDpAvailable(t *testing.T) {
	boom := errors.New("npm: boom")
	for _, c := range []struct {
		name    string
		version string
		infoErr error
		raw     bool
		rawErr  error
		want    errcode.Code
		wantErr error
	}{
		{name: "docker 29 with raw protection", version: "29.9.1", raw: true},
		{name: "docker 28.0", version: "28.0.0", raw: true},
		{name: "a version with a suffix", version: "28.3.2-ce", raw: true},
		{name: "docker 27", version: "27.5.1", raw: true, want: errcode.ModuleUnavailable},
		{name: "docker 9", version: "9.0.0", raw: true, want: errcode.ModuleUnavailable},
		{name: "no version", version: "", raw: true, want: errcode.ModuleUnavailable},
		{name: "a version nobody can read", version: "latest", raw: true, want: errcode.ModuleUnavailable},
		{name: "no raw protection", version: "29.9.1", raw: false, want: errcode.ModuleUnavailable},
		{name: "raw protection unreadable", version: "29.9.1", rawErr: boom, wantErr: boom},
		{name: "engine down", infoErr: dockerapi.ErrUnavailable, raw: true, want: errcode.DockerEngineUnavailable},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newTHHost(t)
			n := h.nm()
			n.d.info = func(context.Context) (dockerapi.SystemInfo, error) {
				return dockerapi.SystemInfo{ServerVersion: c.version}, c.infoErr
			}
			n.d.raw = func(context.Context, core.Env) (bool, error) { return c.raw, c.rawErr }
			err := n.instanceDeploy().Spec.Available(context.Background(), h.env(""))
			switch {
			case c.wantErr != nil:
				if !errors.Is(err, c.wantErr) {
					t.Errorf("error %v, want %v", err, c.wantErr)
				}
			case c.want == "":
				if err != nil {
					t.Errorf("error %v, want the action available", err)
				}
			default:
				if err == nil || dpCode(t, err) != c.want {
					t.Errorf("error %v, want code %s", err, c.want)
				}
			}
		})
	}
}

func TestDpPlan(t *testing.T) {
	ctx := context.Background()
	plan := func(h *thHost, n nm, email string) (core.PlanOutcome, error) {
		return n.instanceDeploy().Handler.Plan(ctx, h.env(""), dpInput(email))
	}
	t.Run("a clean host", func(t *testing.T) {
		h := newTHHost(t)
		out, err := plan(h, h.nm(), dpEmail)
		if err != nil {
			t.Fatal(err)
		}
		want := []core.DiffLine{
			{Op: "+", Text: "Docker network darkpanel-npm"},
			{Op: "+", Text: "volume darkpanel-npm-data"},
			{Op: "+", Text: "volume darkpanel-npm-letsencrypt"},
			{Op: "+", Text: "container darkpanel-npm from " + Image},
			{Op: "+", Text: "ports 80/tcp and 443/tcp on every address, 81/tcp on 127.0.0.1 only"},
			{Op: "+", Text: "administrator " + dpEmail + " with a generated password kept sealed on this host"},
		}
		if !slices.Equal(out.Diff, want) {
			t.Errorf("diff %+v, want %+v", out.Diff, want)
		}
		again, _ := plan(h, h.nm(), dpEmail)
		other, _ := plan(h, h.nm(), "other@darkpanel.dev")
		if len(out.StateHash) != 64 || out.StateHash != again.StateHash || out.StateHash == other.StateHash {
			t.Errorf("state hashes %q, %q, %q: want a stable hash of the image and the email", out.StateHash, again.StateHash, other.StateHash)
		}
		if got := dpMutated(h); len(got) != 0 {
			t.Errorf("the plan changed the Engine: %v", got)
		}
		if len(h.journal.effects()) != 0 {
			t.Error("the plan wrote effects")
		}
	})
	t.Run("emails", func(t *testing.T) {
		for _, email := range []string{dpEmail, "ops@mail.acme-corp.io", "Ops.Admin+npm@Darkpanel.DEV"} {
			h := newTHHost(t)
			if _, err := plan(h, h.nm(), email); err != nil {
				t.Errorf("email %q refused: %v", email, err)
			}
		}
		for _, email := range []string{"", "ops", "ops@", "@darkpanel.dev", "Ops <ops@darkpanel.dev>", "ops@darkpanel.dev, a@b.dev",
			"ops@localhost", "ops@box", "ops@example.com", "ops@mail.example.org", "ops@example.net",
			"ops@192.0.2.1", "ops@[192.0.2.1]", "ops@[2001:db8::1]", "ops@corp.internal", "ops@server.lan", "ops@printer.local", "ops@x.invalid", "ops@harness.test", "ops@x.example", "ops@router.home.arpa",
			"ops@127.1", "ops@1.2.3", "ops@mail.123"} {
			h := newTHHost(t)
			_, err := plan(h, h.nm(), email)
			var coded *errcode.Error
			if err == nil || !core.IsDefinite(err) || !errors.As(err, &coded) || coded.Code != errcode.InvalidInput ||
				len(coded.Fields) != 1 || coded.Fields[0].Path != "/acmeEmail" {
				t.Errorf("email %q: error %v, want a Definite request.invalid_input naming /acmeEmail", email, err)
			}
		}
	})
	t.Run("an instance already deployed", func(t *testing.T) {
		h := newTHHost(t)
		h.deployed()
		_, err := plan(h, h.nm(), dpEmail)
		if err == nil || !core.IsDefinite(err) || dpCode(t, err) != errcode.InvalidInput {
			t.Errorf("error %v, want a Definite request.invalid_input", err)
		}
	})
	t.Run("an Engine object in the way", func(t *testing.T) {
		labels := map[string]string{core.LabelModule: ID}
		for name, seed := range map[string]func(h *thHost){
			"the network":     func(h *thHost) { h.docker.putNetwork(thNetwork(networkName)) },
			"the data volume": func(h *thHost) { h.docker.putVolume(dockerapi.Volume{Name: dataVolume, Labels: labels}) },
			"the cert volume": func(h *thHost) { h.docker.putVolume(dockerapi.Volume{Name: certVolume}) },
			"the container":   func(h *thHost) { h.docker.putContainer(thContainer(containerName)) },
			"a foreign look-alike": func(h *thHost) {
				h.docker.putContainer(thContainer(containerName, thLabelled(map[string]string{"app": "other"})))
			},
		} {
			t.Run(name, func(t *testing.T) {
				h := newTHHost(t)
				seed(h)
				_, err := plan(h, h.nm(), dpEmail)
				if err == nil || !core.IsDefinite(err) || dpCode(t, err) != errcode.DockerInUse {
					t.Errorf("error %v, want a Definite docker.in_use", err)
				}
			})
		}
		t.Run("a network whose name only contains ours", func(t *testing.T) {
			h := newTHHost(t)
			h.docker.putNetwork(thNetwork("darkpanel-npm-extra"))
			if _, err := plan(h, h.nm(), dpEmail); err != nil {
				t.Errorf("error %v, want the plan to pass", err)
			}
		})
	})
	t.Run("ports", func(t *testing.T) {
		sock := func(proto netstat.Proto, port uint16, state netstat.State, comm string) netstat.Socket {
			s := netstat.Socket{Proto: proto, Local: netip.AddrPortFrom(netip.IPv4Unspecified(), port), State: state}
			if comm != "" {
				s.Processes = []netstat.Process{{PID: 7, Comm: comm}}
			}
			return s
		}
		for _, c := range []struct {
			name    string
			sockets []netstat.Socket
			busy    []string
		}{
			{"nothing listens", nil, nil},
			{"port 80", []netstat.Socket{sock(netstat.ProtoTCP, 80, netstat.StateListen, "nginx")}, []string{"80/tcp is in use by nginx"}},
			{"port 443", []netstat.Socket{sock(netstat.ProtoTCP, 443, netstat.StateListen, "")}, []string{"443/tcp is in use by an unknown process"}},
			{"both", []netstat.Socket{sock(netstat.ProtoTCP, 80, netstat.StateListen, "apache2"), sock(netstat.ProtoTCP, 443, netstat.StateListen, "apache2")},
				[]string{"80/tcp is in use by apache2", "443/tcp is in use by apache2"}},
			{"the same port on both address families", []netstat.Socket{
				sock(netstat.ProtoTCP, 80, netstat.StateListen, "nginx"),
				{Proto: netstat.ProtoTCP, Local: netip.AddrPortFrom(netip.IPv6Unspecified(), 80), State: netstat.StateListen,
					Processes: []netstat.Process{{PID: 7, Comm: "nginx"}}},
			}, []string{"80/tcp is in use by nginx"}},
			{"a port whose first listener has no process", []netstat.Socket{
				sock(netstat.ProtoTCP, 80, netstat.StateListen, ""),
				sock(netstat.ProtoTCP, 80, netstat.StateListen, "nginx"),
			}, []string{"80/tcp is in use by nginx"}},
			{"an established connection", []netstat.Socket{sock(netstat.ProtoTCP, 80, netstat.StateEstablished, "curl")}, nil},
			{"udp", []netstat.Socket{sock(netstat.ProtoUDP, 443, netstat.StateClose, "quic")}, nil},
			{"the admin port", []netstat.Socket{sock(netstat.ProtoTCP, 81, netstat.StateListen, "other")}, nil},
		} {
			t.Run(c.name, func(t *testing.T) {
				h := newTHHost(t)
				n := h.nm()
				n.d.sockets = func() ([]netstat.Socket, error) { return c.sockets, nil }
				_, err := plan(h, n, dpEmail)
				if len(c.busy) == 0 {
					if err != nil {
						t.Errorf("error %v, want the plan to pass", err)
					}
					return
				}
				var coded *errcode.Error
				if err == nil || !core.IsDefinite(err) || !errors.As(err, &coded) || coded.Code != errcode.DERPPortInUse || coded.Detail != strings.Join(c.busy, "; ") {
					t.Errorf("error %v, want a Definite derp.port_in_use saying %q", err, strings.Join(c.busy, "; "))
				}
			})
		}
		t.Run("an installed DERP server", func(t *testing.T) {
			h := newTHHost(t)
			h.files["/etc/systemd/system/darkpanel-derper.service"] = []byte("[Unit]\n")
			_, err := plan(h, h.nm(), dpEmail)
			var coded *errcode.Error
			if err == nil || !core.IsDefinite(err) || !errors.As(err, &coded) || coded.Code != errcode.DERPPortInUse ||
				coded.Detail != "a DERP server is installed (darkpanel-derper.service)" {
				t.Errorf("error %v, want a Definite derp.port_in_use naming the installed DERP server", err)
			}
		})
		t.Run("the socket table unreadable", func(t *testing.T) {
			h := newTHHost(t)
			n := h.nm()
			boom := errors.New("npm: no /proc")
			n.d.sockets = func() ([]netstat.Socket, error) { return nil, boom }
			if _, err := plan(h, n, dpEmail); !errors.Is(err, boom) {
				t.Errorf("error %v, want %v", err, boom)
			}
		})
	})
	t.Run("the Engine down", func(t *testing.T) {
		h := newTHHost(t)
		h.docker.Fail("ListNetworks", dockerapi.ErrUnavailable)
		_, err := plan(h, h.nm(), dpEmail)
		if err == nil || dpCode(t, err) != errcode.DockerEngineUnavailable {
			t.Errorf("error %v, want docker.engine_unavailable", err)
		}
	})
}

func TestDpApplyLowerCasesTheAdminEmail(t *testing.T) {
	h := newTHHost(t)
	out, err := h.nm().instanceDeploy().Handler.Apply(context.Background(), h.env("c1"), dpInput("Ops.Admin+npm@Darkpanel.DEV"))
	if err != nil {
		t.Fatal(err)
	}
	var d Deployed
	if err := json.Unmarshal(out, &d); err != nil {
		t.Fatal(err)
	}
	const want = "ops.admin+npm@darkpanel.dev"
	email, _, ok := h.npm.Admin()
	row, found, err := loadInstance(context.Background(), h.db.Read())
	if err != nil || !found || !ok || email != want || row.AdminEmail != want || d.AdminEmail != want {
		t.Errorf("admin %q (%t), row %q (%t, %v), output %q; want each %q", email, ok, row.AdminEmail, found, err, d.AdminEmail, want)
	}
}

func TestDpApply(t *testing.T) {
	h := newTHHost(t)
	n := h.nm()
	out, err := dpApply(h, n, "c1")
	if err != nil {
		t.Fatal(err)
	}
	c, ok := h.docker.container(containerName)
	if !ok {
		t.Fatal("no container was created")
	}
	if want := (Deployed{ContainerID: c.ID, Image: Image, AdminEmail: dpEmail}); out != want {
		t.Errorf("output %+v, want %+v", out, want)
	}

	t.Run("the Engine", func(t *testing.T) {
		labels := map[string]string{core.LabelModule: ID}
		nw, ok := h.docker.network(networkName)
		if !ok || nw.Driver != "bridge" || nw.Labels[core.LabelModule] != "npm" {
			t.Errorf("network %+v, %v", nw, ok)
		}
		for _, v := range []string{dataVolume, certVolume} {
			if got, ok := h.docker.volume(v); !ok || got.Labels[core.LabelModule] != labels[core.LabelModule] {
				t.Errorf("volume %s = %+v, %v", v, got, ok)
			}
		}
		if !c.State.Running || c.Config.Image != Image || c.Config.Labels[core.LabelModule] != "npm" || c.NetworkSettings.Networks[networkName] == nil || len(c.Mounts) != 2 {
			t.Errorf("container %+v", c)
		}
		if got := c.NetworkSettings.Ports["81/tcp"]; len(got) != 1 || got[0].HostIP != "127.0.0.1" || got[0].HostPort != "81" {
			t.Errorf("published 81/tcp %v, want 127.0.0.1:81 only", got)
		}
		for _, p := range []string{"80/tcp", "443/tcp"} {
			if got := c.NetworkSettings.Ports[p]; len(got) != 1 || (got[0].HostIP != "" && got[0].HostIP != "0.0.0.0") || got[0].HostPort != p[:strings.IndexByte(p, '/')] {
				t.Errorf("published %s %v, want every address", p, got)
			}
		}
		if _, err := h.docker.InspectImage(context.Background(), Image); err != nil {
			t.Errorf("the image is not on the host: %v", err)
		}
		if got := h.docker.calls(); !slices.Contains(got, "PullImage") {
			t.Errorf("calls %v: the image was not pulled", got)
		}
	})

	t.Run("NPM", func(t *testing.T) {
		email, password, ok := h.npm.Admin()
		if !ok || email != dpEmail || password != dpPassword || !h.npm.Setup() {
			t.Errorf("administrator %q, %q, %t", email, password, ok)
		}
		var posts []string
		for _, r := range h.npm.Requests() {
			if r.Method == http.MethodPost {
				posts = append(posts, r.Path)
			}
		}
		if !slices.Equal(posts, []string{"/api/users", "/api/tokens"}) {
			t.Errorf("NPM was sent POST %v, want the user and then a token", posts)
		}
		if len(password) != 43 || strings.ContainsAny(password, "+/=") {
			t.Errorf("password %q: want 32 bytes in base64url without padding", password)
		}
	})

	t.Run("the instance row", func(t *testing.T) {
		row, ok, err := loadInstance(context.Background(), h.db.Read())
		if err != nil || !ok {
			t.Fatalf("row %+v, %v, %v", row, ok, err)
		}
		if row.ContainerID != c.ID || row.Image != Image || row.AdminEmail != dpEmail || row.Public != "no" ||
			!row.CreatedAt.Equal(thStart) || !row.UpdatedAt.Equal(thStart) {
			t.Errorf("row %+v", row)
		}
		if bytes.Contains(row.PasswordCT, []byte(dpPassword)) {
			t.Error("the stored password is not sealed")
		}
		pw, err := h.env("").Sealer.Open("npm_instances.password_ct.1", row.PasswordCT)
		if err != nil || string(pw) != dpPassword {
			t.Errorf("sealed password opens to %q, %v", pw, err)
		}
	})

	t.Run("the journal", func(t *testing.T) {
		effs := dpEffects(t, h)
		type want struct {
			kind     core.EffectKind
			resource string
			recovery string
			local    bool
		}
		wants := []want{
			{core.EffectCreate, "npm.network:" + thID("network:"+networkName), networkName, false},
			{core.EffectCreate, "npm.volume:" + dataVolume, dataVolume, false},
			{core.EffectCreate, "npm.volume:" + certVolume, certVolume, false},
			{core.EffectCreate, "npm.container:" + c.ID, containerName, false},
			{core.EffectModify, "npm.container:" + c.ID, "", false},
			{core.EffectCreate, "npm.instance:" + containerName, containerName, true},
		}
		if len(effs) != len(wants) {
			t.Fatalf("%d effects, want %d: %+v", len(effs), len(wants), effs)
		}
		for i, w := range wants {
			e := effs[i]
			wait := commitWait
			if w.local {
				wait = 0
			}
			if e.Kind != w.kind || e.Resource != w.resource || e.State != core.EffectMade || e.Local != w.local || e.InverseLocal != w.local ||
				e.CommitWait != wait || e.ChangeID != "c1" || e.Seq != i+1 {
				t.Errorf("effect %d = %+v, want a made %s of %s (local %t, commit wait %s)", i+1, e, w.kind, w.resource, w.local, wait)
			}
			switch {
			case w.kind == core.EffectCreate:
				if e.Recovery == nil || e.Recovery.Kind != "name" || e.Recovery.Value != w.recovery || string(e.Before.State) != "null" {
					t.Errorf("effect %d recovery %+v before %s, want name %s and a null before", i+1, e.Recovery, e.Before.State, w.recovery)
				}
			case e.Recovery != nil:
				t.Errorf("effect %d declares a recovery identity", i+1)
			}
			if e.Post == nil {
				t.Errorf("effect %d has no post-state", i+1)
			}
		}
		var before, post map[string]any
		_ = json.Unmarshal(effs[4].Before.State, &before)
		_ = json.Unmarshal(effs[4].Post.State, &post)
		if before["id"] != c.ID || before["running"] != false || post["running"] != true || post["startedAt"] == nil {
			t.Errorf("the start's before %v and post %v, want the container stopped and then running", before, post)
		}
		if _, ok := before["startedAt"]; ok {
			t.Errorf("the start's before %v holds a timestamp no undo can restore", before)
		}

		ctx := context.Background()
		secret, err := h.env("c1").Effects.Secret(ctx, 6)
		if err != nil || string(secret) != dpPassword {
			t.Errorf("secret of the instance effect = %q, %v; want the administrator password", secret, err)
		}
		for seq := 1; seq <= 5; seq++ {
			if s, _ := h.env("c1").Effects.Secret(ctx, seq); len(s) != 0 {
				t.Errorf("effect %d carries a secret", seq)
			}
		}
		raw, _ := json.Marshal(effs)
		if bytes.Contains(raw, []byte(dpPassword)) {
			t.Error("an effect holds the password")
		}
	})
}

func TestDpApplyRefuses(t *testing.T) {
	t.Run("a bad email", func(t *testing.T) {
		h := newTHHost(t)
		_, err := h.nm().instanceDeploy().Handler.Apply(context.Background(), h.env("c1"), dpInput("ops@localhost"))
		if err == nil || !core.IsDefinite(err) || dpCode(t, err) != errcode.InvalidInput {
			t.Errorf("error %v, want a Definite request.invalid_input", err)
		}
		if len(h.journal.effects()) != 0 || len(dpMutated(h)) != 0 {
			t.Error("a refused deploy changed something")
		}
	})
	t.Run("an Engine object created since the plan", func(t *testing.T) {
		h := newTHHost(t)
		h.docker.putVolume(dockerapi.Volume{Name: certVolume})
		_, err := dpApply(h, h.nm(), "c1")
		if err == nil || !core.IsDefinite(err) || dpCode(t, err) != errcode.DockerInUse {
			t.Errorf("error %v, want a Definite docker.in_use", err)
		}
		if len(h.journal.effects()) != 0 || len(dpMutated(h)) != 0 {
			t.Errorf("a refused deploy changed something: %v", dpMutated(h))
		}
	})
	t.Run("an Engine object created during the image pull", func(t *testing.T) {
		h := newTHHost(t)
		n := h.nm()
		n.d.docker = &w10nvPullDocker{thDocker: h.docker, during: func() {
			h.docker.putVolume(dockerapi.Volume{Name: certVolume})
		}}
		_, err := dpApply(h, n, "c1")
		if err == nil || !core.IsDefinite(err) || dpCode(t, err) != errcode.DockerInUse {
			t.Errorf("error %v, want a Definite docker.in_use", err)
		}
		if got := dpMutated(h); !slices.Equal(got, []string{"PullImage"}) {
			t.Errorf("Engine changes %v, want the pull alone: nothing may be created or adopted after it", got)
		}
		if len(h.journal.effects()) != 0 {
			t.Errorf("effects %+v, want none", h.journal.effects())
		}
	})
	t.Run("a port taken since the plan", func(t *testing.T) {
		h := newTHHost(t)
		n := h.nm()
		n.d.sockets = func() ([]netstat.Socket, error) {
			return []netstat.Socket{{Proto: netstat.ProtoTCP, State: netstat.StateListen, Local: netip.AddrPortFrom(netip.IPv4Unspecified(), 443)}}, nil
		}
		_, err := dpApply(h, n, "c1")
		if err == nil || !core.IsDefinite(err) || dpCode(t, err) != errcode.DERPPortInUse {
			t.Errorf("error %v, want a Definite derp.port_in_use", err)
		}
		if len(dpMutated(h)) != 0 {
			t.Errorf("a refused deploy changed the Engine: %v", dpMutated(h))
		}
	})
	t.Run("a second deploy", func(t *testing.T) {
		h := newTHHost(t)
		if _, err := dpApply(h, h.nm(), "c1"); err != nil {
			t.Fatal(err)
		}
		effects, calls := len(h.journal.effects()), len(dpMutated(h))
		_, err := dpApply(h, h.nm(), "c2")
		if err == nil || !core.IsDefinite(err) || dpCode(t, err) != errcode.InvalidInput {
			t.Errorf("error %v, want a Definite request.invalid_input", err)
		}
		if len(h.journal.effects()) != effects || len(dpMutated(h)) != calls {
			t.Error("the refused second deploy changed something")
		}
	})
}

func TestDpImage(t *testing.T) {
	t.Run("an image the host has is not pulled", func(t *testing.T) {
		h := newTHHost(t)
		h.docker.putImage(Image)
		if _, err := dpApply(h, h.nm(), "c1"); err != nil {
			t.Fatal(err)
		}
		if slices.Contains(h.docker.calls(), "PullImage") {
			t.Errorf("calls %v: a present image was pulled", h.docker.calls())
		}
	})
	t.Run("a failed pull changes nothing", func(t *testing.T) {
		for name, fail := range map[string]error{"refused": thAPIError(404, "manifest unknown"), "unreachable": dockerapi.ErrUnavailable, "other": errors.New("npm: boom")} {
			t.Run(name, func(t *testing.T) {
				h := newTHHost(t)
				h.docker.Fail("PullImage", fail)
				_, err := dpApply(h, h.nm(), "c1")
				if err == nil || !core.IsDefinite(err) {
					t.Errorf("error %v, want a Definite error", err)
				}
				if len(h.journal.effects()) != 0 {
					t.Errorf("effects %+v after a failed pull", h.journal.effects())
				}
				if got := dpMutated(h); !slices.Equal(got, []string{"PullImage"}) {
					t.Errorf("Engine changes %v, want the pull only", got)
				}
			})
		}
	})
}

func TestDpApplyFailsAtEachStep(t *testing.T) {
	steps := []struct {
		name  string
		op    string
		skip  int // calls of op that pass before the failing one
		index int // the failing effect's position in the journal
	}{
		{"the network", "CreateNetwork", 0, 0},
		{"the data volume", "CreateVolume", 0, 1},
		{"the certificate volume", "CreateVolume", 1, 2},
		{"the container", "CreateContainer", 0, 3},
		{"the start", "StartContainer", 0, 4},
	}
	for _, s := range steps {
		for _, refused := range []bool{true, false} {
			name := s.name + " answered 4xx"
			failure := thAPIError(http.StatusConflict, "refused")
			if !refused {
				name, failure = s.name+" lost", thAPIError(http.StatusInternalServerError, "daemon error")
			}
			t.Run(name, func(t *testing.T) {
				h := newTHHost(t)
				for range s.skip {
					h.docker.Fail(s.op, nil)
				}
				h.docker.Fail(s.op, failure)
				_, err := dpApply(h, h.nm(), "c1")
				if err == nil || core.IsDefinite(err) != refused {
					t.Fatalf("error %v (definite %t), want definite %t", err, core.IsDefinite(err), refused)
				}
				effs := h.journal.effects()
				if len(effs) != s.index+1 {
					t.Fatalf("%d effects, want the failing one to be the last of %d", len(effs), s.index+1)
				}
				for i, e := range effs[:s.index] {
					if e.State != core.EffectMade {
						t.Errorf("effect %d is %s, want made", i+1, e.State)
					}
				}
				want := core.EffectIntended
				if refused {
					want = core.EffectSkipped
				}
				if last := effs[s.index]; last.State != want {
					t.Errorf("the failing effect %d is %s, want %s", s.index+1, last.State, want)
				}
				if _, ok, _ := loadInstance(context.Background(), h.db.Read()); ok {
					t.Error("an instance row was written")
				}
				if h.npm.Setup() {
					t.Error("NPM was set up")
				}
			})
		}
	}
}

func TestDpBootstrap(t *testing.T) {
	ctx := context.Background()
	failed := func(t *testing.T, h *thHost, err error) *errcode.Error {
		t.Helper()
		var coded *errcode.Error
		if err == nil || !errors.As(err, &coded) || coded.Code != errcode.NPMSetupFailed {
			t.Fatalf("error %v, want npm.setup_failed", err)
		}
		if core.IsDefinite(err) {
			t.Error("a failed bootstrap is Definite: the container runs and the administrator may exist")
		}
		if strings.Contains(err.Error(), dpPassword) || strings.Contains(coded.Detail, dpPassword) {
			t.Error("the error holds the password")
		}
		effs := h.journal.effects()
		if len(effs) != 6 {
			t.Fatalf("%d effects, want the five Engine effects and the instance", len(effs))
		}
		for i, e := range effs[:5] {
			if e.State != core.EffectMade {
				t.Errorf("effect %d is %s, want made: the operator undoes them", i+1, e.State)
			}
		}
		if effs[5].State != core.EffectIntended {
			t.Errorf("the instance effect is %s, want intended", effs[5].State)
		}
		if s, _ := h.env("c1").Effects.Secret(ctx, 6); string(s) != dpPassword {
			t.Error("the journal lost the password the administrator may have been given")
		}
		if _, ok, _ := loadInstance(ctx, h.db.Read()); ok {
			t.Error("an instance row was written")
		}
		return coded
	}
	for _, c := range []struct {
		name   string
		fail   func(h *thHost)
		detail string
	}{
		{"the user is refused", func(h *thHost) { h.npm.Fail(http.MethodPost, "/api/users", fake.Fault{Status: 500}) }, "(HTTP 500)"},
		{"the user answer is lost", func(h *thHost) { h.npm.Fail(http.MethodPost, "/api/users", fake.Fault{AfterCommit: true}) }, ""},
		{"the token is refused", func(h *thHost) { h.npm.Fail(http.MethodPost, "/api/tokens", fake.Fault{Status: 503}) }, "(HTTP 503)"},
		{"an administrator already exists", func(h *thHost) { h.npm.SeedAdmin("someone@darkpanel.dev", "another-password-0123456789") }, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newTHHost(t)
			c.fail(h)
			_, err := dpApply(h, h.nm(), "c1")
			coded := failed(t, h, err)
			if !strings.Contains(coded.Detail, c.detail) {
				t.Errorf("detail %q, want it to say %q", coded.Detail, c.detail)
			}
		})
	}
	t.Run("an existing administrator is left alone", func(t *testing.T) {
		h := newTHHost(t)
		h.npm.SeedAdmin("someone@darkpanel.dev", "another-password-0123456789")
		_, err := dpApply(h, h.nm(), "c1")
		_ = failed(t, h, err)
		for _, r := range h.npm.Requests() {
			if r.Method == http.MethodPost {
				t.Errorf("NPM was sent %s %s", r.Method, r.Path)
			}
		}
		if email, _, _ := h.npm.Admin(); email != "someone@darkpanel.dev" {
			t.Errorf("administrator %q was replaced", email)
		}
	})

	t.Run("NPM comes up late", func(t *testing.T) {
		h := newTHHost(t)
		n, probe := dpFlaky(h, 3)
		var err error
		dpDrive(t, h, deployPoll, func() { _, err = dpApply(h, n, "c1") })
		if err != nil {
			t.Fatal(err)
		}
		if probe.calls < 4 {
			t.Errorf("%d health calls, want the three failures and a success", probe.calls)
		}
		if waited := h.clock.Now().Sub(thStart); waited != 3*deployPoll {
			t.Errorf("waited %s, want three polls of %s", waited, deployPoll)
		}
	})
	t.Run("NPM never comes up", func(t *testing.T) {
		h := newTHHost(t)
		n, _ := dpFlaky(h, -1)
		var err error
		dpDrive(t, h, deployPoll, func() { _, err = dpApply(h, n, "c1") })
		coded := failed(t, h, err)
		if !strings.Contains(coded.Detail, "(HTTP 502)") || !strings.Contains(coded.Detail, deployReady.String()) {
			t.Errorf("detail %q, want the last status and the wait", coded.Detail)
		}
		if waited := h.clock.Now().Sub(thStart); waited < deployReady || waited > deployReady+deployPoll {
			t.Errorf("waited %s, want %s", waited, deployReady)
		}
		if len(h.npm.Requests()) != 0 {
			t.Errorf("NPM was sent %v while it was down", h.npm.Requests())
		}
	})
	t.Run("the call is cancelled while it waits", func(t *testing.T) {
		h := newTHHost(t)
		n, _ := dpFlaky(h, -1)
		cctx, cancel := context.WithCancel(ctx)
		defer cancel()
		errc := make(chan error, 1)
		go func() {
			_, err := n.instanceDeploy().Handler.Apply(cctx, h.env("c1"), dpInput(dpEmail))
			errc <- err
		}()
		for deadline := time.Now().Add(20 * time.Second); h.clock.Pending() == 0; time.Sleep(time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatal("the deploy never waited")
			}
		}
		cancel()
		if err := <-errc; !errors.Is(err, context.Canceled) {
			t.Errorf("error %v, want the cancellation", err)
		}
		if h.clock.Pending() != 0 {
			t.Error("a timer is still armed")
		}
	})
}

// dpDeployed applies the deploy as change id and returns the host.
func dpDeployed(t *testing.T) (*thHost, nm) {
	t.Helper()
	h := newTHHost(t)
	n := h.nm()
	if _, err := dpApply(h, n, "c1"); err != nil {
		t.Fatal(err)
	}
	return h, n
}

func TestDpInverse(t *testing.T) {
	ctx := context.Background()
	h, n := dpDeployed(t)
	h.docker.Fail("Info", nil)
	h2 := n.instanceDeploy().Handler
	effs := h.journal.effects()
	undo := h.env("c1")
	calls := len(h.docker.calls())
	for i := len(effs) - 1; i >= 0; i-- {
		e := effs[i]
		obs, err := h2.Observe(ctx, undo, e)
		if err != nil {
			t.Fatalf("observe effect %d: %v", e.Seq, err)
		}
		if !bytes.Equal(obs.Current.State, e.Post.State) {
			t.Fatalf("effect %d observed as %s before its undo, want its post-state %s", e.Seq, obs.Current.State, e.Post.State)
		}
		res, err := h2.Inverse(ctx, undo, e)
		if err != nil || res.Resource != "" {
			t.Fatalf("inverse of effect %d = %+v, %v", e.Seq, res, err)
		}
		e.State = core.EffectUndoing
		obs, err = h2.Observe(ctx, undo, e)
		if err != nil {
			t.Fatalf("observe effect %d after its undo: %v", e.Seq, err)
		}
		want := e.Before.State
		if e.Kind == core.EffectCreate {
			want = json.RawMessage("null")
		}
		if !bytes.Equal(obs.Current.State, want) {
			t.Errorf("effect %d observed as %s after its undo, want %s", e.Seq, obs.Current.State, want)
		}
	}
	var removals []string
	for _, c := range h.docker.calls()[calls:] {
		if slices.Contains(dpMutations, c) {
			removals = append(removals, c)
		}
	}
	if want := []string{"StopContainer", "RemoveContainer", "RemoveVolume", "RemoveVolume", "RemoveNetwork"}; !slices.Equal(removals, want) {
		t.Errorf("Engine changes %v, want %v", removals, want)
	}
	if _, ok := h.docker.container(containerName); ok {
		t.Error("the container is still there")
	}
	if _, ok := h.docker.network(networkName); ok {
		t.Error("the network is still there")
	}
	for _, v := range []string{dataVolume, certVolume} {
		if _, ok := h.docker.volume(v); ok {
			t.Errorf("volume %s is still there", v)
		}
	}
	if _, ok, _ := loadInstance(ctx, h.db.Read()); ok {
		t.Error("the instance row is still there")
	}
	if _, ok := h.docker.container("darkpanel-npm"); ok {
		t.Error("container found by name")
	}

	t.Run("a second undo finds everything gone", func(t *testing.T) {
		for i := len(effs) - 1; i >= 0; i-- {
			if _, err := h2.Inverse(ctx, undo, effs[i]); err != nil {
				t.Errorf("inverse of effect %d: %v", effs[i].Seq, err)
			}
		}
	})
	t.Run("the host is ready for another deploy", func(t *testing.T) {
		if _, err := n.instanceDeploy().Handler.Plan(ctx, h.env(""), dpInput(dpEmail)); err != nil {
			t.Fatalf("a plan after the undo: %v", err)
		}
	})
}

func TestDpInverseRefusals(t *testing.T) {
	ctx := context.Background()
	inverse := func(t *testing.T, h *thHost, n nm, seq int) error {
		t.Helper()
		effs := h.journal.effects()
		_, err := n.instanceDeploy().Handler.Inverse(ctx, h.env("c1"), effs[seq-1])
		return err
	}
	cases := []struct {
		name string
		op   string
		seq  int
	}{
		{"the network", "RemoveNetwork", 1},
		{"a volume", "RemoveVolume", 2},
		{"the container", "RemoveContainer", 4},
		{"the stop", "StopContainer", 5},
	}
	for _, c := range cases {
		t.Run(c.name+" refused", func(t *testing.T) {
			h, n := dpDeployed(t)
			h.docker.Fail(c.op, thAPIError(http.StatusConflict, "in use"))
			if err := inverse(t, h, n, c.seq); err == nil || !core.IsDefinite(err) {
				t.Errorf("error %v, want a Definite error: the Engine refused", err)
			}
		})
		t.Run(c.name+" lost", func(t *testing.T) {
			h, n := dpDeployed(t)
			h.docker.Fail(c.op, thAPIError(http.StatusInternalServerError, "daemon error"))
			if err := inverse(t, h, n, c.seq); err == nil || core.IsDefinite(err) {
				t.Errorf("error %v, want an error that is not Definite", err)
			}
		})
	}
	t.Run("the Engine down", func(t *testing.T) {
		h, n := dpDeployed(t)
		h.docker.Fail("RemoveNetwork", dockerapi.ErrUnavailable)
		if err := inverse(t, h, n, 1); err == nil || dpCode(t, err) != errcode.DockerEngineUnavailable {
			t.Errorf("error %v, want docker.engine_unavailable", err)
		}
	})
	t.Run("a volume a container still mounts", func(t *testing.T) {
		h, n := dpDeployed(t)
		h.docker.putContainer(thContainer("other", thMounted(dataVolume, "/data")))
		if err := inverse(t, h, n, 2); err == nil || !core.IsDefinite(err) {
			t.Errorf("error %v, want a Definite refusal", err)
		}
		if _, ok := h.docker.volume(dataVolume); !ok {
			t.Error("the volume was removed")
		}
	})
	t.Run("an effect that is not the deploy's", func(t *testing.T) {
		h, n := dpDeployed(t)
		_, err := n.instanceDeploy().Handler.Inverse(ctx, h.env("c1"), core.Effect{Kind: core.EffectCreate, Resource: "npm.proxyhost:5"})
		if err == nil {
			t.Error("an inverse of a proxy host was accepted")
		}
	})
}

func TestDpObserve(t *testing.T) {
	ctx := context.Background()
	observe := func(h *thHost, resource string, state core.EffectState) core.Observation {
		t.Helper()
		obs, err := h.nm().instanceDeploy().Handler.Observe(ctx, h.env("c1"), core.Effect{Kind: core.EffectCreate, Resource: resource, State: state})
		if err != nil {
			t.Fatal(err)
		}
		return obs
	}
	t.Run("no instance", func(t *testing.T) {
		h := newTHHost(t)
		obs := observe(h, "npm.instance:?darkpanel-npm", core.EffectIntended)
		if obs.Matches != 0 || string(obs.Current.State) != "null" {
			t.Errorf("observation %+v, want no match and the null state", obs)
		}
		obs = observe(h, "npm.instance:darkpanel-npm", core.EffectUndone)
		if obs.Matches != 1 || string(obs.Current.State) != "null" || !obs.Exclusive {
			t.Errorf("observation %+v, want the null state of the named instance", obs)
		}
	})
	t.Run("a deployed instance", func(t *testing.T) {
		h := newTHHost(t)
		inst := h.deployed()
		for _, resource := range []string{"npm.instance:?darkpanel-npm", "npm.instance:darkpanel-npm"} {
			obs := observe(h, resource, core.EffectIntended)
			var doc map[string]any
			if err := json.Unmarshal(obs.Current.State, &doc); err != nil {
				t.Fatal(err)
			}
			if obs.Matches != 1 || !obs.Exclusive || obs.Resource != map[bool]string{true: "npm.instance:darkpanel-npm", false: ""}[strings.Contains(resource, "?")] ||
				doc["containerId"] != inst.ContainerID || doc["image"] != Image || doc["adminEmail"] != inst.Email || len(doc) != 3 {
				t.Errorf("%s: observation %+v (state %v)", resource, obs, doc)
			}
			if strings.Contains(string(obs.Current.State), inst.Password) {
				t.Error("the state holds the password")
			}
		}
	})
	t.Run("an Engine object", func(t *testing.T) {
		h := newTHHost(t)
		h.deployed()
		obs := observe(h, "npm.network:?darkpanel-npm", core.EffectIntended)
		if obs.Matches != 1 || obs.Resource != "npm.network:"+thID("network:"+networkName) {
			t.Errorf("observation %+v, want the network", obs)
		}
	})
}

func TestDpVerify(t *testing.T) {
	ctx := context.Background()
	verify := func(h *thHost) error {
		return h.nm().instanceDeploy().Handler.Verify(ctx, h.env("c1"), dpInput(dpEmail), json.RawMessage(`{}`))
	}
	t.Run("a healthy instance", func(t *testing.T) {
		h, n := dpDeployed(t)
		if err := n.instanceDeploy().Handler.Verify(ctx, h.env("c1"), dpInput(dpEmail), json.RawMessage(`{}`)); err != nil {
			t.Errorf("error %v", err)
		}
		h2 := newTHHost(t)
		h2.deployed()
		if err := verify(h2); err != nil {
			t.Errorf("error %v", err)
		}
	})
	for name, c := range map[string]struct {
		prepare func(h *thHost)
	}{
		"no row": {func(h *thHost) {
			_ = h.db.Write(ctx, func(tx *sql.Tx) error { return deleteInstance(ctx, tx) })
		}},
		"a stopped container":          {func(h *thHost) { _, _ = h.docker.StopContainer(ctx, containerName, time.Second) }},
		"a password NPM does not know": {func(h *thHost) { h.npm.SeedAdmin("ops@darkpanel.dev", "changed-behind-our-back-0123456789") }},
	} {
		t.Run(name, func(t *testing.T) {
			h := newTHHost(t)
			h.deployed()
			c.prepare(h)
			if err := verify(h); err == nil || dpCode(t, err) != errcode.NPMUnhealthy {
				t.Errorf("error %v, want npm.unhealthy", err)
			}
		})
	}
	t.Run("a missing container", func(t *testing.T) {
		h := newTHHost(t)
		h.deployed()
		_ = h.docker.RemoveContainer(ctx, containerName, dockerapi.RemoveContainerOptions{Force: true})
		if err := verify(h); err == nil {
			t.Error("a deploy without its container verified")
		}
	})
}

func TestDpNoSecrets(t *testing.T) {
	h := newTHHost(t)
	n := h.nm()
	plan, err := n.instanceDeploy().Handler.Plan(context.Background(), h.env(""), dpInput(dpEmail))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dpApply(h, n, "c1"); err != nil {
		t.Fatal(err)
	}
	c, _ := h.docker.container(containerName)
	for what, v := range map[string]any{"plan": plan, "effects": h.journal.effects(), "container": c} {
		raw, _ := json.Marshal(v)
		if bytes.Contains(raw, []byte(dpPassword)) {
			t.Errorf("the %s holds the administrator password", what)
		}
	}
	if len(c.Config.Env) != 0 {
		t.Errorf("the container has an environment %v: the password must not reach it", c.Config.Env)
	}
}

// w10nvPullDocker is the test Engine whose image pull runs a hook before it answers, as another client acts while the
// image downloads.
type w10nvPullDocker struct {
	*thDocker
	during func()
}

func (d *w10nvPullDocker) PullImage(ctx context.Context, ref string) (pullStream, error) {
	s, err := d.thDocker.PullImage(ctx, ref)
	d.during()
	return s, err
}
