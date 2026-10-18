package cli

import (
	"bytes"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/darkraise/darkpanel/internal/core"
)

// cliEnv runs the CLI in a test: stdin is the given text, the terminal
// check answers terminal, and secrets answer the no-echo prompts in order.
type cliEnv struct {
	reg      *core.Registry
	env      map[string]string
	stdin    string
	terminal bool
	secrets  []string
	commands []*cobra.Command
}

func (c cliEnv) run(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	reg := c.reg
	if reg == nil {
		reg = testRegistry(t)
	}
	secrets := slices.Clone(c.secrets)
	var out, errb bytes.Buffer
	root, err := NewRoot(Options{
		Registry: reg, Commands: c.commands,
		Stdin: strings.NewReader(c.stdin), Stdout: &out, Stderr: &errb,
		Getenv: envOf(c.env), UserHomeDir: homeAt(t.TempDir()),
		IsTerminal: func() bool { return c.terminal },
		ReadSecret: func() (string, error) {
			if len(secrets) == 0 {
				return "", io.EOF
			}
			s := secrets[0]
			secrets = secrets[1:]
			return s, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	root.SetArgs(args)
	err = root.Execute()
	PrintError(&errb, err)
	return out.String(), errb.String(), ExitCode(err)
}

// hostList is the core.host.list read the CLI makes to resolve --host;
// hosts are "<id>:<name>".
func hostList(hosts ...string) exchange {
	var items []string
	for i, h := range hosts {
		id, name, _ := strings.Cut(h, ":")
		items = append(items, fmt.Sprintf(`{"id":%q,"name":%q,"isLocal":%t,"online":true,"modules":[]}`, id, name, i == 0))
	}
	return exchange{method: "POST", path: "/api/v1/actions/core.host.list/v1/read", body: `{"input":{}}`,
		reply: `{"output":{"hosts":[` + strings.Join(items, ",") + `]}}`}
}

const testToken = "dpt_01JTEST_secret" //nolint:gosec // G101: a token the fake API expects, not a credential

var tokenEnv = map[string]string{EnvToken: testToken}

func TestNewRootTree(t *testing.T) {
	version := &cobra.Command{Use: "version", RunE: func(*cobra.Command, []string) error { return nil }}
	root, err := NewRoot(Options{Registry: testRegistry(t), Commands: []*cobra.Command{version}})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"demo.item.get", "demo.item.list", "core.host.list", "core.audit.list", "core.user.list",
		"demo.rule", "demo.status"} {
		cmd, ok := FindCommand(root, name)
		if !ok || cmd.CommandPath() != "darkpanel "+strings.Join(CommandPath(name), " ") {
			t.Errorf("FindCommand(%q) = %v, %v", name, cmd, ok)
		}
	}
	if _, ok := FindCommand(root, "demo.nothing.here"); ok {
		t.Error("FindCommand found an unregistered action")
	}
	if c, _, err := root.Find([]string{"version"}); err != nil || c != version {
		t.Errorf("version command: %v, %v", c, err)
	}
	get, _ := FindCommand(root, "demo.item.get")
	for _, f := range []string{"id", "verbose", "host", "url", "json", "input"} {
		if get.Flags().Lookup(f) == nil {
			t.Errorf("demo item get has no --%s", f)
		}
	}
	users, _ := FindCommand(root, "core.user.list")
	if users.Flags().Lookup("socket") == nil || users.Flags().Lookup("dev") == nil || users.Flags().Lookup("url") != nil {
		t.Error("user list must take --socket and --dev, not --url")
	}
	hosts, _ := FindCommand(root, "core.host.list")
	if hosts.Flags().Lookup("host") != nil {
		t.Error("a controller-side action takes no --host")
	}
}

func TestNewRootRefusesClashes(t *testing.T) {
	reg, err := core.NewRegistry(fakeCore{"core.change.list"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewRoot(Options{Registry: reg}); err == nil || !strings.Contains(err.Error(), `"change"`) {
		t.Errorf("core.change.list: error %v", err)
	}
	reg, err = core.NewRegistry(fakeCore{"core.agent.list"})
	if err != nil {
		t.Fatal(err)
	}
	agent := &cobra.Command{Use: "agent"}
	if _, err := NewRoot(Options{Registry: reg, Commands: []*cobra.Command{agent}}); err == nil ||
		!strings.Contains(err.Error(), `"agent"`) {
		t.Errorf("core.agent.list against a hand-written agent command: error %v", err)
	}
}

func TestReadAgentAction(t *testing.T) {
	base := newFakeAPI(t, testToken,
		hostList("h1:web"),
		exchange{method: "POST", path: "/api/v1/hosts/h1/actions/demo.item.get/v2/read", body: `{"input":{"id":"i1"}}`,
			reply: `{"output":{"id":"i1","name":"first","tags":["a","b"]}}`},
		hostList("h1:web", "h2:db"),
		exchange{method: "POST", path: "/api/v1/hosts/h2/actions/demo.item.list/v1/read", body: `{"input":{}}`,
			reply: `{"output":{"items":[{"id":"i1","name":"first","tags":[]}]}}`},
		hostList("h1:web", "h2:db"),
		exchange{method: "POST", path: "/api/v1/hosts/h1/actions/demo.item.get/v2/read", body: `{"input":{"id":"i2","verbose":true}}`,
			reply: `{"output":{"id":"i2","name":"second","tags":null}}`},
	)
	cli := cliEnv{env: tokenEnv}
	out, errOut, code := cli.run(t, "demo", "item", "get", "--id", "i1", "--url", base)
	if code != 0 || out != "id: i1\nname: first\ntags: a, b\n" {
		t.Errorf("one host: exit %d, out %q, err %q", code, out, errOut)
	}
	out, errOut, code = cli.run(t, "demo", "item", "list", "--host", "db", "--url", base)
	if code != 0 || out != "id  name   tags\ni1  first  []\n" {
		t.Errorf("--host by name: exit %d, out %q, err %q", code, out, errOut)
	}
	out, errOut, code = cli.run(t, "demo", "item", "get", "--id", "i2", "--verbose", "--host", "h1", "--json", "--url", base)
	if code != 0 || out != `{"id":"i2","name":"second","tags":null}`+"\n" {
		t.Errorf("--json: exit %d, out %q, err %q", code, out, errOut)
	}
}

func TestHostResolutionFailures(t *testing.T) {
	base := newFakeAPI(t, testToken, hostList("h1:web", "h2:db"), hostList("h1:web", "h2:db"), hostList())
	cli := cliEnv{env: tokenEnv}
	_, errOut, code := cli.run(t, "demo", "item", "list", "--url", base)
	if code != ExitUsage || !strings.Contains(errOut, "h1 (web), h2 (db)") || !strings.Contains(errOut, "--host") {
		t.Errorf("two hosts without --host: exit %d, err %q", code, errOut)
	}
	_, errOut, code = cli.run(t, "demo", "item", "list", "--host", "mail", "--url", base)
	if code != ExitUsage || !strings.Contains(errOut, `"mail"`) || !strings.Contains(errOut, "h1 (web), h2 (db)") {
		t.Errorf("unknown host: exit %d, err %q", code, errOut)
	}
	_, errOut, code = cli.run(t, "demo", "item", "list", "--url", base)
	if code != ExitError || !strings.Contains(errOut, "no hosts") {
		t.Errorf("no hosts: exit %d, err %q", code, errOut)
	}
}

func TestReadControllerActionsAndResources(t *testing.T) {
	base := newFakeAPI(t, testToken,
		exchange{method: "POST", path: "/api/v1/actions/core.audit.list/v1/read", body: `{"input":{"limit":5,"actor":"user:u1"}}`,
			reply: `{"output":{"entries":[{"id":"a1","actor":"user:u1","action":"core.token.create"}],"nextBefore":null}}`},
		hostList("h1:web"),
		exchange{method: "GET", path: "/api/v1/hosts/h1/resources/demo/rule", reply: `{"name":"web","port":443}`},
		exchange{method: "POST", path: "/api/v1/actions/core.token.list/v1/read", body: `{"input":{}}`, status: 401, problem: true,
			reply: `{"type":"about:blank","title":"Authentication required","status":401,"detail":"authentication required","code":"auth.required"}`},
	)
	cli := cliEnv{env: tokenEnv}
	out, errOut, code := cli.run(t, "audit", "list", "--limit", "5", "--actor", "user:u1", "--url", base)
	want := "entries:\nid  actor    action\na1  user:u1  core.token.create\nnextBefore: -\n"
	if code != 0 || out != want {
		t.Errorf("audit list: exit %d, out %q, err %q", code, out, errOut)
	}
	out, errOut, code = cli.run(t, "demo", "rule", "get", "--url", base)
	if code != 0 || out != "name: web\nport: 443\n" {
		t.Errorf("resource: exit %d, out %q, err %q", code, out, errOut)
	}
	_, errOut, code = cli.run(t, "token", "list", "--url", base)
	if code != ExitAuth || errOut != "darkpanel: authentication required (auth.required)\n" {
		t.Errorf("auth.required: exit %d, err %q", code, errOut)
	}
}

func TestBreakGlassReadUsesTheSocket(t *testing.T) {
	sock := newFakeSocket(t, exchange{method: "POST", path: "/api/v1/actions/core.user.list/v1/read", body: `{"input":{}}`,
		reply: `{"output":{"users":[{"id":"u1","username":"ann","role":"admin","state":"active","createdAt":"2026-10-18T00:00:00Z"}]}}`})
	out, errOut, code := cliEnv{env: tokenEnv}.run(t, "user", "list", "--socket", sock)
	want := "id  username  role   state   createdAt\nu1  ann       admin  active  2026-10-18T00:00:00Z\n"
	if code != 0 || out != want {
		t.Errorf("user list: exit %d, out %q, err %q", code, out, errOut)
	}
}

func TestUsageErrorsSendNothing(t *testing.T) {
	base := newFakeAPI(t, testToken)
	cli := cliEnv{env: tokenEnv}
	for _, args := range [][]string{
		{"demo", "item", "get", "--url", base},
		{"demo", "item", "get", "--id", "x", "extra", "--url", base},
		{"demo", "item", "get", "--id", "x", "--bogus", "--url", base},
		{"demo", "bogus"},
		{"bogus"},
		{"audit", "list", "--limit", "many", "--url", base},
	} {
		if _, errOut, code := cli.run(t, args...); code != ExitUsage {
			t.Errorf("darkpanel %q: exit %d, err %q", args, code, errOut)
		}
	}
}
