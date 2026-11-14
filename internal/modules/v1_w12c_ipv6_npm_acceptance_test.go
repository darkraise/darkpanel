package modules_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"

	"github.com/spf13/cobra"

	"github.com/darkraise/darkpanel/internal/cli"
)

func TestA27_GeneratedDeployPublicCommandPlansV2WithFirewallAndIPv6(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	var bodies [][]byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		paths, bodies = append(paths, r.URL.Path), append(bodies, body)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"type":"about:blank","title":"stop","status":400,"code":"request.invalid_input","detail":"the test stops here"}`)
	}))
	defer srv.Close()

	var stdout, stderr bytes.Buffer
	root, err := cli.NewRoot(cli.Options{
		Registry: built(t), Stdout: &stdout, Stderr: &stderr,
		Commands:    []*cobra.Command{{Use: "serve"}, {Use: "version"}},
		Getenv:      func(k string) string { return map[string]string{cli.EnvToken: "dpt_01JTEST_secret"}[k] },
		UserHomeDir: func() (string, error) { return t.TempDir(), nil },
		IsTerminal:  func() bool { return false },
	})
	if err != nil {
		t.Fatalf("cli.NewRoot: %v", err)
	}
	cmd, ok := cli.FindCommand(root, "npm.instance.deploy-public")
	if !ok {
		t.Fatal("npm.instance.deploy-public has no generated command")
	}
	if cmd.CommandPath() != "darkpanel npm instance deploy-public" {
		t.Errorf("command path %q", cmd.CommandPath())
	}
	for _, f := range []string{"host-id", "acme-email", "integration", "firewall", "ipv6"} {
		flag := cmd.Flags().Lookup(f)
		if flag == nil {
			t.Errorf("the command has no --%s", f)
		} else if (f == "integration" || f == "firewall" || f == "ipv6") && flag.Value.Type() != "bool" {
			t.Errorf("--%s is a %s, want a boolean", f, flag.Value.Type())
		}
	}

	root.SetArgs([]string{"npm", "instance", "deploy-public", "--host-id", "h1", "--acme-email", "ops@example.com",
		"--integration=false", "--firewall=true", "--ipv6=true", "--yes", "--url", srv.URL})
	_ = root.Execute()

	mu.Lock()
	defer mu.Unlock()
	if len(paths) == 0 {
		t.Fatalf("the command sent no request; stderr: %s", stderr.String())
	}
	if want := "/api/v1/actions/npm.instance.deploy-public/v2/plan"; paths[0] != want {
		t.Errorf("first request %s, want %s", paths[0], want)
	}
	var req struct {
		Input map[string]any `json:"input"`
	}
	if err := json.Unmarshal(bodies[0], &req); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"hostId": "h1", "acmeEmail": "ops@example.com", "integration": false, "firewall": true, "ipv6": true}
	if !reflect.DeepEqual(req.Input, want) {
		t.Errorf("plan input %v, want %v", req.Input, want)
	}
}
