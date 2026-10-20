package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/darkraise/darkpanel/internal/agent/api"
	"github.com/darkraise/darkpanel/internal/agent/socket"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// agentCalls records what the fake agent or a fallback was asked; the
// socket serves each request on its own goroutine.
type agentCalls struct {
	mu  sync.Mutex
	got []string
}

func (c *agentCalls) add(format string, args ...any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.got = append(c.got, fmt.Sprintf(format, args...))
}

func (c *agentCalls) list() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.got)
}

// socketDir is a directory short enough for Unix socket paths.
func socketDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "dp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// agentSocket serves the agent socket's handler over agent, as agent run
// serves it, for the enrolled host h1, and returns the socket's path.
func agentSocket(t *testing.T, agent *coretest.FakeAgent) string {
	t.Helper()
	if agent.PreflightFunc == nil {
		agent.PreflightFunc = func(_ context.Context, id string) (core.ModuleStatus, error) {
			return core.ModuleStatus{ID: id, Version: 1, Available: true}, nil
		}
	}
	path := filepath.Join(socketDir(t), "agent.sock")
	h := api.NewSocketHandler(api.Config{Agent: agent, Log: slog.New(slog.DiscardHandler)},
		api.SocketConfig{Identity: func() (string, bool) { return "h1", true }})
	srv, err := socket.Listen(socket.Config{Path: path, Handler: h, Allow: func(uint32) bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- srv.Serve() }()
	t.Cleanup(func() {
		_ = srv.Close()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	return path
}

// staleSocket is a socket file in dir that nothing listens on, as a
// crashed agent leaves it: a dial is refused.
func staleSocket(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "stale.sock")
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	ln.SetUnlinkOnClose(false)
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLocalReadAndResource(t *testing.T) {
	rec := &agentCalls{}
	agent := &coretest.FakeAgent{
		ReadFunc: func(_ context.Context, _ core.Principal, a core.ActionRef, input json.RawMessage) (json.RawMessage, error) {
			rec.add("read %s/v%d %s", a.Name, a.Version, input)
			return json.RawMessage(`{"id":"i1","name":"first","tags":["a","b"]}`), nil
		},
		ResourceFunc: func(_ context.Context, _ core.Principal, name string) (json.RawMessage, error) {
			rec.add("resource %s", name)
			return json.RawMessage(`{"name":"web","port":443}`), nil
		},
	}
	cli := cliEnv{agentSocket: agentSocket(t, agent)}
	out, errOut, code := cli.run(t, "demo", "item", "get", "--id", "i1", "--local")
	if code != 0 || out != "id: i1\nname: first\ntags: a, b\n" {
		t.Errorf("read: exit %d, out %q, err %q", code, out, errOut)
	}
	out, errOut, code = cli.run(t, "demo", "item", "get", "--id", "i1", "--json", "--local")
	if code != 0 || out != `{"id":"i1","name":"first","tags":["a","b"]}`+"\n" {
		t.Errorf("read --json: exit %d, out %q, err %q", code, out, errOut)
	}
	out, errOut, code = cli.run(t, "demo", "rule", "get", "--local")
	if code != 0 || out != "name: web\nport: 443\n" {
		t.Errorf("resource: exit %d, out %q, err %q", code, out, errOut)
	}
	want := []string{`read demo.item.get/v2 {"id":"i1"}`, `read demo.item.get/v2 {"id":"i1"}`, "resource demo.rule"}
	if got := rec.list(); !slices.Equal(got, want) {
		t.Errorf("the agent saw %q, want %q", got, want)
	}
}

// The socket's plan and change are converted to the API's Plan and Change
// shapes, so a command prints the same with --local as through the
// controller API, with and without --json.
func TestLocalMutatingPrintsLikeTheAPI(t *testing.T) {
	plan := exchange{method: "POST", path: "/api/v1/hosts/h1/actions/demo.item.add/v1/plan",
		body:  `{"input":{"name":"n1","apiKey":"key-1","passphrase":"pass-1"}}`,
		reply: planJSON("p1", "demo.item.add", "h1", "", `[{"id":"name-free","ok":true,"message":"the name is free"}]`)}
	apply := exchange{method: "POST", path: "/api/v1/hosts/h1/actions/demo.item.add/v1/apply", body: `{"planId":"p1"}`,
		reply: changeJSON("c1", "demo.item.add", "compensable", "completed",
			`"revertDeadline":null,"output":{"id":"i9","token":"tok-secret"},"error":null,"compensatedBy":"demo.item.remove"`)}
	base := newFakeAPI(t, testToken, hostList("h1:web"), plan, apply, hostList("h1:web"), plan, apply)
	rec := &agentCalls{}
	agent := &coretest.FakeAgent{
		PlanFunc: func(_ context.Context, _ core.Principal, a core.ActionRef, input json.RawMessage) (core.PlanView, error) {
			if !jsonEqual(input, []byte(`{"name":"n1","apiKey":"key-1","passphrase":"pass-1"}`)) {
				t.Errorf("plan input %s", input)
			}
			rec.add("plan %s/v%d", a.Name, a.Version)
			return core.PlanView{PlanID: "p1", HostID: "h1", Action: "demo.item.add", Version: 1, InputHash: "ih",
				Diff:      []core.DiffLine{{Op: "+", Text: "add demo.item.add"}, {Op: " ", Text: "keep"}},
				Guards:    []core.Guard{{ID: "name-free", OK: true, Message: "the name is free"}},
				ExpiresAt: time.Date(2026, 10, 18, 12, 5, 0, 0, time.UTC)}, nil
		},
		ApplyFunc: func(_ context.Context, _ core.Principal, planID string, opts core.ApplyOptions) (core.ChangeView, error) {
			rec.add("apply %s %+v", planID, opts)
			return core.ChangeView{ID: "c1", HostID: "h1", Action: "demo.item.add", Actor: "token:t1", CompensatedBy: "demo.item.remove",
				Version: 1, Class: core.ClassCompensable, State: core.StateCompleted,
				CreatedAt: time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 10, 18, 12, 0, 1, 0, time.UTC),
				Output: json.RawMessage(`{"id":"i9","token":"tok-secret"}`)}, nil
		},
	}
	sock := agentSocket(t, agent)
	for _, extra := range [][]string{nil, {"--json"}} {
		args := slices.Concat([]string{"demo", "item", "add", "--name", "n1", "--yes"}, extra)
		apiOut, apiErr, apiCode := cliEnv{env: tokenEnv, stdin: "key-1\npass-1\n"}.run(t, slices.Concat(args, []string{"--url", base})...)
		out, errOut, code := cliEnv{stdin: "key-1\npass-1\n", agentSocket: sock}.run(t, slices.Concat(args, []string{"--local"})...)
		if apiCode != 0 || code != 0 || out != apiOut || errOut != apiErr {
			t.Errorf("%q: --local exit %d, out %q, err %q\nAPI exit %d, out %q, err %q", extra, code, out, errOut, apiCode, apiOut, apiErr)
		}
	}
	want := []string{"plan demo.item.add/v1", "apply p1 {ConfirmTimeout:0s Confirmation: ConfirmNow:false}"}
	if got := rec.list(); !slices.Equal(got, slices.Concat(want, want)) {
		t.Errorf("the agent saw %q", got)
	}
}

func TestLocalConfirmations(t *testing.T) {
	limits := filepath.Join(t.TempDir(), "limits.json")
	if err := os.WriteFile(limits, []byte(`{"limits":{"max":10}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := &agentCalls{}
	agent := &coretest.FakeAgent{
		PlanFunc: func(_ context.Context, _ core.Principal, a core.ActionRef, input json.RawMessage) (core.PlanView, error) {
			var in struct{ ID string }
			_ = json.Unmarshal(input, &in)
			// demo.item.remove is irreversible: its plan names the item id
			// as the confirmation phrase.
			return core.PlanView{PlanID: "p-" + in.ID, HostID: "h1", Action: a.Name, Version: a.Version, Confirmation: in.ID}, nil
		},
		ApplyFunc: func(_ context.Context, _ core.Principal, planID string, opts core.ApplyOptions) (core.ChangeView, error) {
			rec.add("apply %s %q %t", planID, opts.Confirmation, opts.ConfirmNow)
			if planID == "p-busy" {
				return core.ChangeView{}, errcode.New(errcode.PlanGuardFailed, "guard free failed: the item is in use")
			}
			return core.ChangeView{ID: "c1", HostID: "h1", Action: "demo", Version: 1, State: core.StateCompleted}, nil
		},
	}
	cli := cliEnv{agentSocket: agentSocket(t, agent)}
	tests := []struct {
		name    string
		args    []string
		code    int
		errPart string
		calls   []string
	}{
		{name: "irreversible without its phrase", args: []string{"demo", "item", "remove", "--id", "i1", "--yes"},
			code: ExitRefused, errPart: "pass --confirmation i1"},
		{name: "irreversible with its phrase", args: []string{"demo", "item", "remove", "--id", "i1", "--yes", "--confirmation", "i1"},
			calls: []string{`apply p-i1 "i1" false`}},
		{name: "a refusal keeps its code", args: []string{"demo", "item", "remove", "--id", "busy", "--yes", "--confirmation", "busy"},
			code: ExitRefused, errPart: "darkpanel: guard free failed: the item is in use (plan.guard_failed)\n",
			calls: []string{`apply p-busy "busy" false`}},
		{name: "revertible confirmed at once",
			args:  []string{"demo", "rule", "set", "--name", "web", "--port", "443", "--input", limits, "--yes", "--confirm"},
			calls: []string{`apply p- "" true`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := len(rec.list())
			out, errOut, code := cli.run(t, append(tt.args, "--local")...)
			if code != tt.code || !strings.Contains(errOut, tt.errPart) {
				t.Errorf("exit %d, out %q, err %q; want exit %d and an error containing %q", code, out, errOut, tt.code, tt.errPart)
			}
			if got := rec.list()[before:]; !slices.Equal(got, tt.calls) {
				t.Errorf("applies %q, want %q", got, tt.calls)
			}
		})
	}
}

// localTestCommands are hand-written commands as the assembly passes them;
// own has a persistent pre-run hook of its own, which cobra runs instead of
// the root's.
func localTestCommands(hookRan *bool) []*cobra.Command {
	version := &cobra.Command{Use: "version", RunE: func(*cobra.Command, []string) error { return nil }}
	own := &cobra.Command{Use: "own", PersistentPreRunE: func(*cobra.Command, []string) error {
		*hookRan = true
		return nil
	}}
	own.AddCommand(&cobra.Command{Use: "sub", RunE: func(*cobra.Command, []string) error { return nil }})
	return []*cobra.Command{version, own}
}

func TestLocalRefusals(t *testing.T) {
	missing := filepath.Join(socketDir(t), "missing")
	notAgent := func(path string) string {
		return "darkpanel: --local reaches only this host's agent; darkpanel " + path + " is not an agent-side command\n"
	}
	withHost := "darkpanel: --host does not apply with --local: the agent socket serves this host only\n"
	withURL := "darkpanel: --url does not apply with --local: the agent socket serves this host only\n"
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"demo", "item", "get", "--id", "i1", "--host", "h1"}, withHost},
		{[]string{"demo", "rule", "get", "--host", "web"}, withHost},
		{[]string{"demo", "item", "get", "--id", "i1", "--url", "http://127.0.0.1:1"}, withURL},
		{[]string{"demo", "rule", "get", "--url", "http://127.0.0.1:1"}, withURL},
		{[]string{"host", "list"}, notAgent("host list")},
		{[]string{"token", "create", "--name", "ci", "--scope", "read", "--yes"}, notAgent("token create")},
		{[]string{"change", "list"}, notAgent("change list")},
		{[]string{"controller", "web-ca"}, notAgent("controller web-ca")},
		{[]string{"user", "create", "--username", "ann"}, notAgent("user create")},
		{[]string{"demo"}, notAgent("demo")},
		{[]string{"version"}, notAgent("version")},
		{[]string{"own", "sub"}, notAgent("own sub")},
	}
	for _, tt := range tests {
		hookRan := false
		cli := cliEnv{agentSocket: missing, commands: localTestCommands(&hookRan)}
		out, errOut, code := cli.run(t, append(tt.args, "--local")...)
		if code != ExitUsage || errOut != tt.want || out != "" || hookRan {
			t.Errorf("darkpanel %q --local: exit %d, out %q, err %q, own hook ran %t; want exit 2 and %q",
				tt.args, code, out, errOut, hookRan, tt.want)
		}
	}
	hookRan := false
	if _, errOut, code := (cliEnv{commands: localTestCommands(&hookRan)}).run(t, "own", "sub"); code != 0 || !hookRan {
		t.Errorf("own sub without --local: exit %d, err %q, hook ran %t", code, errOut, hookRan)
	}
}

func TestLocalLeavesHelpAndCompletion(t *testing.T) {
	for _, args := range [][]string{{"help"}, {"help", "demo", "item", "get"}, {"demo", "item", "get", "--help"}, {"completion", "bash"}} {
		out, errOut, code := cliEnv{}.run(t, args...)
		if code != 0 || out == "" || errOut != "" {
			t.Errorf("darkpanel %q: exit %d, out %d bytes, err %q", args, code, len(out), errOut)
		}
	}
	out, _, _ := cliEnv{}.run(t, "help", "demo", "item", "get")
	if !strings.Contains(out, "--local") {
		t.Errorf("demo item get's help does not show --local:\n%s", out)
	}
}

func TestLocalFallback(t *testing.T) {
	dir := socketDir(t)
	missing := filepath.Join(dir, "missing")
	stale := staleSocket(t, dir)
	tooLong := filepath.Join(dir, strings.Repeat("s", 120))
	notice := func(path, action string) string {
		return "darkpanel: the agent socket " + path + " does not answer; running " + action + " in this process\n"
	}
	tests := []struct {
		name     string
		sock     string
		terminal bool
		stdin    string
		args     []string
		code     int
		out      string
		errOut   string // a prefix of standard error
		calls    []string
	}{
		{name: "missing socket", sock: missing, args: []string{"demo", "item", "remove", "--id", "i1", "--yes"},
			out: "removed: i1\n", errOut: notice(missing, "demo.item.remove"), calls: []string{`remove {"id":"i1"}`}},
		{name: "refused connection, --json", sock: stale, args: []string{"demo", "item", "remove", "--id", "i1", "--yes", "--json"},
			out: `{"removed":"i1"}` + "\n", errOut: notice(stale, "demo.item.remove"), calls: []string{`remove {"id":"i1"}`}},
		{name: "no --yes and no terminal", sock: missing, args: []string{"demo", "item", "remove", "--id", "i1"}, code: ExitRefused,
			errOut: notice(missing, "demo.item.remove") + "darkpanel: standard input is not a terminal; pass --yes to apply\n"},
		{name: "agreed on the terminal", sock: missing, terminal: true, stdin: "y\n", args: []string{"demo", "item", "remove", "--id", "i1"},
			out: "removed: i1\n", errOut: notice(missing, "demo.item.remove") + "Apply? [y/N] ", calls: []string{`remove {"id":"i1"}`}},
		{name: "declined on the terminal", sock: missing, terminal: true, stdin: "n\n", args: []string{"demo", "item", "remove", "--id", "i1"},
			code: ExitRefused, errOut: notice(missing, "demo.item.remove") + "Apply? [y/N] darkpanel: not applied\n"},
		{name: "a read asks nothing", sock: missing, args: []string{"demo", "item", "get", "--id", "i1", "--json"},
			out: `{"id":"i1"}` + "\n", errOut: notice(missing, "demo.item.get"), calls: []string{`get {"id":"i1"}`}},
		{name: "the fallback's error", sock: missing, args: []string{"demo", "item", "remove", "--id", "gone", "--yes"}, code: ExitError,
			errOut: notice(missing, "demo.item.remove") + "darkpanel: there is no previous binary (update.nothing_to_roll_back)\n",
			calls:  []string{`remove {"id":"gone"}`}},
		{name: "no fallback", sock: missing, args: []string{"demo", "item", "list"}, code: ExitError,
			errOut: "darkpanel: the agent socket " + missing + " does not answer: dial unix " + missing + ": connect: no such file or directory\n"},
		{name: "a resource has no fallback", sock: missing, args: []string{"demo", "status", "get"}, code: ExitError,
			errOut: "darkpanel: the agent socket " + missing + " does not answer: "},
		{name: "another dial error runs no fallback", sock: tooLong, args: []string{"demo", "item", "remove", "--id", "i1", "--yes"},
			code: ExitError, errOut: "darkpanel: the agent socket " + tooLong + " does not answer: "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &agentCalls{}
			fallbacks := map[string]LocalFallback{
				"demo.item.remove": func(_ context.Context, input json.RawMessage) (json.RawMessage, error) {
					rec.add("remove %s", input)
					var in struct{ ID string }
					if err := json.Unmarshal(input, &in); err != nil {
						return nil, err
					}
					if in.ID == "gone" {
						return nil, errcode.New(errcode.UpdateNothingToRollBack, "there is no previous binary")
					}
					return json.Marshal(map[string]string{"removed": in.ID})
				},
				"demo.item.get": func(_ context.Context, input json.RawMessage) (json.RawMessage, error) {
					rec.add("get %s", input)
					return json.RawMessage(`{"id":"i1"}`), nil
				},
			}
			cli := cliEnv{agentSocket: tt.sock, fallbacks: fallbacks, terminal: tt.terminal, stdin: tt.stdin}
			out, errOut, code := cli.run(t, append(tt.args, "--local")...)
			if code != tt.code || out != tt.out || !strings.HasPrefix(errOut, tt.errOut) {
				t.Errorf("exit %d, out %q, err %q; want exit %d, out %q, err starting %q", code, out, errOut, tt.code, tt.out, tt.errOut)
			}
			if got := rec.list(); !slices.Equal(got, tt.calls) {
				t.Errorf("fallback calls %q, want %q", got, tt.calls)
			}
		})
	}
}
