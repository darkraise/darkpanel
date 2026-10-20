package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/agent/api"
	"github.com/darkraise/darkpanel/internal/cli"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/install"
	"github.com/darkraise/darkpanel/internal/modules"
)

// fakeInstall replaces the installer and the systemd provider for one test
// and records the Config system install passed.
func fakeInstall(t *testing.T, euid int, terminal bool) (*[]install.Config, *agentSeams) {
	t.Helper()
	s := fakeAgent(t, euid)
	var got []install.Config
	oldInstall, oldTerm := runInstall, stdinTerminal
	t.Cleanup(func() { runInstall, stdinTerminal = oldInstall, oldTerm })
	runInstall = func(_ context.Context, cfg install.Config) error {
		got = append(got, cfg)
		return nil
	}
	stdinTerminal = func(io.Reader) bool { return terminal }
	return &got, s
}

func TestSystemInstallRefusals(t *testing.T) {
	tests := []struct {
		args []string
		euid int
		out  string
	}{
		{[]string{"system", "install", "--role", "controller"}, 1000, "system install runs as root; run it with sudo"},
		{[]string{"system", "install"}, 0, `install: role "": want controller or agent`},
		{[]string{"system", "install", "--role", "web"}, 0, `install: role "web": want controller or agent`},
		{[]string{"system", "install", "--role", "agent", "--agent-listen", "192.0.2.7:7443"}, 0, "--agent-listen and --agent-advertise are for the controller role"},
		{[]string{"system", "install", "--role", "agent", "--agent-advertise", "panel.example:7443"}, 0, "--agent-listen and --agent-advertise are for the controller role"},
		{[]string{"system", "install", "--role", "controller", "--agent-listen", "192.0.2.7"}, 0, `agent address "192.0.2.7" is not host:port`},
		{[]string{"system", "install", "--role", "controller", "--agent-advertise", "panel.example:0"}, 0, `agent address "panel.example:0" is not host:port`},
		{[]string{"system", "nope"}, 0, `unknown command "nope" for "darkpanel system"`},
	}
	for _, tt := range tests {
		got, _ := fakeInstall(t, tt.euid, false)
		out, code := runArgs(tt.args...)
		if code != cli.ExitUsage || !strings.Contains(out, tt.out) {
			t.Errorf("darkpanel %s: exit %d, output %q; want exit 2 and %q", strings.Join(tt.args, " "), code, out, tt.out)
		}
		if len(*got) != 0 {
			t.Errorf("darkpanel %s ran the installer", strings.Join(tt.args, " "))
		}
	}
}

func TestSystemInstallConfig(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		stdin      string
		terminal   bool
		role       install.Role
		token      string
		controller bool
	}{
		{"controller", []string{"--role", "controller", "--agent-listen", "192.0.2.7:7443", "--agent-advertise", "panel.example:7443"},
			"ignored\n", false, install.RoleController, "", true},
		{"agent with a piped token", []string{"--role", "agent"}, "  piped-token \n", false, install.RoleAgent, "piped-token", false},
		{"agent on a terminal", []string{"--role", "agent"}, "typed\n", true, install.RoleAgent, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, s := fakeInstall(t, 0, tt.terminal)
			args := append([]string{"system", "install"}, tt.args...)
			if stdout, stderr, code := runStdin(strings.NewReader(tt.stdin), args...); code != cli.ExitOK {
				t.Fatalf("exit %d: %s%s", code, stdout, stderr)
			}
			if len(*got) != 1 {
				t.Fatalf("the installer ran %d times, want once", len(*got))
			}
			cfg := (*got)[0]
			if cfg.Layout != s.layout || cfg.Role != tt.role || cfg.Token != tt.token || cfg.BinaryVersion != "dev" ||
				cfg.Systemd != s.systemd || cfg.Exec == nil || cfg.Users != (install.SystemUsers{}) || cfg.Clock == nil || cfg.Out == nil {
				t.Errorf("Config = %+v, want the installed layout, role %s, token %q and the production providers", cfg, tt.role, tt.token)
			}
			if got := cfg.Controller != nil; got != tt.controller {
				t.Errorf("Controller set %v, want %v", got, tt.controller)
			}
			if a, ok := cfg.Agent.(installAgent); !ok || a.socket != s.layout.Paths.AgentSocket || a.tlsDir != s.layout.Paths.AgentTLSDir {
				t.Errorf("Agent = %#v, want the installed agent socket and credentials", cfg.Agent)
			}
			var wantModules []string
			for _, m := range modules.All() {
				wantModules = append(wantModules, m.Manifest().ID)
			}
			var gotModules []string
			for _, m := range cfg.Modules {
				gotModules = append(gotModules, m.ID)
			}
			if !slices.Equal(gotModules, wantModules) {
				t.Errorf("Modules = %v, want %v", gotModules, wantModules)
			}
			if tt.role == install.RoleController && (!slices.Equal(cfg.AgentListen, []string{"192.0.2.7:7443"}) || cfg.AgentAdvertise != "panel.example:7443") {
				t.Errorf("agent listener flags = %v %q", cfg.AgentListen, cfg.AgentAdvertise)
			}
			if s.systemd.closed != 1 {
				t.Errorf("systemd closed %d times, want once", s.systemd.closed)
			}
		})
	}
}

// serveUnix serves h on a Unix socket until the test ends and returns its
// path.
func serveUnix(t *testing.T, h http.Handler) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "dp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "s.sock")
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: h} //nolint:gosec // G112: a test server on a private socket
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })
	return path
}

func TestSocketController(t *testing.T) {
	var applied []string
	mux := http.NewServeMux()
	health := "ok"
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"status":"`+health+`","version":"dev"}`)
	})
	mux.HandleFunc("POST /api/v1/actions/core.host.enroll-local/v1/plan", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Input json.RawMessage `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || string(body.Input) != "{}" {
			t.Errorf("plan body input %s (%v), want {}", body.Input, err)
		}
		_, _ = io.WriteString(w, `{"planId":"01HZY8PLAN","confirmation":"enroll-local web1"}`)
	})
	mux.HandleFunc("POST /api/v1/actions/core.host.enroll-local/v1/apply", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			PlanID       string `json:"planId"`
			Confirmation string `json:"confirmation"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		applied = append(applied, body.PlanID+" "+body.Confirmation)
		_, _ = io.WriteString(w, `{"id":"01HZY8CHG","state":"applied","output":{"token":"dpe1.token"},"error":null}`)
	})
	c := newSocketController(serveUnix(t, mux))
	ctx := context.Background()
	if err := c.Healthy(ctx); err != nil {
		t.Errorf("Healthy with status ok: %v", err)
	}
	health = "degraded"
	if err := c.Healthy(ctx); err == nil || !strings.Contains(err.Error(), `status "degraded"`) {
		t.Errorf("Healthy with status degraded: %v, want an error naming the status", err)
	}
	token, err := c.EnrollLocal(ctx)
	if err != nil || token != "dpe1.token" {
		t.Errorf("EnrollLocal = %q, %v; want the apply's output token", token, err)
	}
	if want := []string{"01HZY8PLAN enroll-local web1"}; !slices.Equal(applied, want) {
		t.Errorf("applied %q, want the plan id with the plan's confirmation phrase %q", applied, want)
	}

	refusing := http.NewServeMux()
	refusing.HandleFunc("POST /api/v1/actions/core.host.enroll-local/v1/plan", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"title":"Forbidden","status":403,"detail":"the controller socket accepts only root","code":"auth.forbidden"}`)
	})
	if _, err := newSocketController(serveUnix(t, refusing)).EnrollLocal(ctx); err == nil ||
		!strings.Contains(err.Error(), "the controller socket accepts only root (auth.forbidden)") {
		t.Errorf("EnrollLocal against a refusal: %v, want the problem's detail and code", err)
	}
	missing := newSocketController(filepath.Join(t.TempDir(), "missing.sock"))
	if err := missing.Healthy(ctx); err == nil {
		t.Error("Healthy without a controller socket succeeded")
	}
}

// helloAgent answers only Hello, as an agent whose controller session is
// connected reports.
type helloAgent struct {
	core.AgentClient
	connected *bool
}

func (h helloAgent) Hello(context.Context) (core.AgentHello, error) {
	return core.AgentHello{HostID: "01HZY5S4ZK8V3J1M2N3P4Q5R6S", ControllerConnected: h.connected}, nil
}

func TestInstallAgentCheck(t *testing.T) {
	yes, no := true, false
	tests := []struct {
		name      string
		socket    bool
		connected *bool
		checks    int
	}{
		{"connected", true, &yes, 0},
		{"not connected", true, &no, 1},
		{"not enrolled", true, nil, 1},
		{"no agent socket", false, nil, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := fakeAgent(t, 0)
			s.checkRes, s.checkErr = install.CheckUnreachable, errors.New("dial: no route")
			checks := 0
			daemonCheck = func(_ context.Context, dir string) (install.CheckResult, error) {
				checks++
				if dir != "/tls" {
					t.Errorf("checked %s, want /tls", dir)
				}
				return s.checkRes, s.checkErr
			}
			socket := filepath.Join(t.TempDir(), "missing.sock")
			if tt.socket {
				socket = serveUnix(t, api.NewSocketHandler(api.Config{Agent: helloAgent{connected: tt.connected}},
					api.SocketConfig{Identity: func() (string, bool) { return "01HZY5S4ZK8V3J1M2N3P4Q5R6S", tt.connected != nil }}))
			}
			res, err := installAgent{socket: socket, tlsDir: "/tls"}.Check(context.Background())
			if checks != tt.checks {
				t.Errorf("ran %d handshakes, want %d", checks, tt.checks)
			}
			wantRes, wantErr := install.CheckAccepted, error(nil)
			if tt.checks > 0 {
				wantRes, wantErr = s.checkRes, s.checkErr
			}
			if res != wantRes || !errors.Is(err, wantErr) {
				t.Errorf("Check = %d, %v; want %d, %v", res, err, wantRes, wantErr)
			}
		})
	}
	s := fakeAgent(t, 0)
	if err := (installAgent{tlsDir: "/tls"}).Enroll(context.Background(), "dpe1.token"); err != nil {
		t.Fatal(err)
	}
	if want := []string{"/tls dpe1.token"}; !slices.Equal(s.enrolls, want) {
		t.Errorf("enrolled %q, want %q", s.enrolls, want)
	}
}
