package cli

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/darkraise/darkpanel/internal/agent/api"
	"github.com/darkraise/darkpanel/internal/core/coretest"
)

// policyAgent serves the agent socket's routes on a Unix socket in dir and
// counts its policy reloads; refuse makes each reload fail.
func policyAgent(t *testing.T, dir string, refuse error) (socket string, reloads *atomic.Int32) {
	t.Helper()
	socket = filepath.Join(dir, "agent.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	reloads = &atomic.Int32{}
	h := api.NewSocketHandler(api.Config{Agent: &coretest.FakeAgent{}}, api.SocketConfig{
		Identity: func() (string, bool) { return "h1", true },
		ReloadPolicy: func(context.Context) error {
			reloads.Add(1)
			return refuse
		},
	})
	srv := &http.Server{Handler: h} //nolint:gosec // G112: a test server on a Unix socket
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return socket, reloads
}

func runPolicy(t *testing.T, cfg AgentPolicyConfig, args ...string) (string, string, error) {
	t.Helper()
	cmd := NewAgentPolicyCommand(cfg)
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(context.Background())
	return out.String(), errOut.String(), err
}

func readPolicyText(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // G304: the test's own file
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestPolicyCommand(t *testing.T) {
	uid := uint32(os.Getuid()) //nolint:gosec // G115: a uid fits in 32 bits
	root := func() int { return 0 }
	dir := t.TempDir()
	socket, reloads := policyAgent(t, dir, nil)
	cfg := AgentPolicyConfig{Path: filepath.Join(dir, "etc", "agent-policy.json"), Socket: socket, Owner: uid, Geteuid: root}

	for _, tc := range []struct {
		args       []string
		file, said string
	}{
		{[]string{"deny", "console"}, `{"denyModules":["console"]}`, "Denied on this host: console"},
		{[]string{"deny", "jobs", "console"}, `{"denyModules":["console","jobs"]}`, "Denied on this host: console, jobs"},
		{[]string{"allow", "console"}, `{"denyModules":["jobs"]}`, "Denied on this host: jobs"},
		{[]string{"allow", "jobs", "console"}, `{"denyModules":[]}`, "This host's policy denies no module"},
	} {
		before := reloads.Load()
		out, _, err := runPolicy(t, cfg, tc.args...)
		if err != nil {
			t.Fatalf("%v: %v", tc.args, err)
		}
		if got := strings.TrimSpace(readPolicyText(t, cfg.Path)); got != tc.file {
			t.Errorf("%v: file %s, want %s", tc.args, got, tc.file)
		}
		if !strings.Contains(out, tc.said) || !strings.Contains(out, "The agent read the policy file") {
			t.Errorf("%v: output %q, want %q and the reload", tc.args, out, tc.said)
		}
		if reloads.Load() != before+1 {
			t.Errorf("%v: %d reloads, want one", tc.args, reloads.Load()-before)
		}
	}
	st, err := os.Stat(cfg.Path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o644 {
		t.Errorf("policy file mode %v, want 0644", st.Mode().Perm())
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(cfg.Path), ".agent-policy-*")); len(left) > 0 {
		t.Errorf("temporary files left behind: %v", left)
	}

	t.Run("refused", func(t *testing.T) {
		writeKept := readPolicyText(t, cfg.Path)
		before := reloads.Load()
		for _, tc := range []struct {
			name string
			cfg  AgentPolicyConfig
			args []string
			want string
		}{
			{"not root", AgentPolicyConfig{Path: cfg.Path, Socket: socket, Owner: uid, Geteuid: func() int { return 1000 }}, []string{"deny", "console"}, "root"},
			{"another module", cfg, []string{"deny", "firewall"}, "firewall"},
			{"no module", cfg, []string{"deny"}, "arg"},
		} {
			_, _, err := runPolicy(t, tc.cfg, tc.args...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("%s: err = %v, want one naming %q", tc.name, err, tc.want)
			}
		}
		if got := readPolicyText(t, cfg.Path); got != writeKept {
			t.Errorf("a refused command changed the file to %s", got)
		}
		if reloads.Load() != before {
			t.Error("a refused command reloaded the agent")
		}
	})

	t.Run("an invalid file starts from denying both", func(t *testing.T) {
		if err := os.WriteFile(cfg.Path, []byte(`{"denyModules":["shell"]}`), 0o644); err != nil { //nolint:gosec // G306: the policy file is world-readable
			t.Fatal(err)
		}
		_, errOut, err := runPolicy(t, cfg, "allow", "console")
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.TrimSpace(readPolicyText(t, cfg.Path)); got != `{"denyModules":["jobs"]}` {
			t.Errorf("file %s, want jobs alone denied", got)
		}
		if !strings.Contains(errOut, "shell") {
			t.Errorf("stderr %q, want the invalid file's error", errOut)
		}
	})

	t.Run("no agent running", func(t *testing.T) {
		c := cfg
		c.Socket = filepath.Join(dir, "absent.sock")
		out, _, err := runPolicy(t, c, "deny", "jobs")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "reads the policy file when it starts") {
			t.Errorf("output %q, want the note that the agent reads it at start", out)
		}
	})

	t.Run("the agent refuses the file", func(t *testing.T) {
		d := t.TempDir()
		sock, _ := policyAgent(t, d, errors.New("the policy file is writable by its group"))
		c := AgentPolicyConfig{Path: filepath.Join(d, "agent-policy.json"), Socket: sock, Owner: uid, Geteuid: root}
		_, _, err := runPolicy(t, c, "deny", "jobs")
		if err == nil || !strings.Contains(err.Error(), "did not read it") {
			t.Errorf("err = %v, want the refused reload", err)
		}
	})
}
