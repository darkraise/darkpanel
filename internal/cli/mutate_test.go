package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

// planJSON is a Plan answer (spec section 12); host and confirmation ""
// are null, and guards is the JSON list of guards.
func planJSON(planID, action, host, confirmation, guards string) string {
	nullable := func(s string) string {
		if s == "" {
			return "null"
		}
		return fmt.Sprintf("%q", s)
	}
	return fmt.Sprintf(`{"planId":%q,"hostId":%s,"action":%q,"version":1,"inputHash":"ih",`+
		`"diff":[{"op":"+","text":"add %s"},{"op":" ","text":"keep"}],"guards":%s,`+
		`"expiresAt":"2026-10-18T12:05:00Z","confirmation":%s}`,
		planID, nullable(host), action, action, guards, nullable(confirmation))
}

// changeJSON is a Change answer; extra holds the fields after "state".
func changeJSON(id, action, class, state, extra string) string {
	return fmt.Sprintf(`{"id":%q,"hostId":"h1","action":%q,"version":1,"class":%q,"state":%q,"actor":"token:t1",`+
		`"createdAt":"2026-10-18T12:00:00Z","updatedAt":"2026-10-18T12:00:01Z",%s}`, id, action, class, state, extra)
}

const noExtra = `"revertDeadline":null,"output":null,"error":null,"compensatedBy":null`

func TestEveryActionHasACommand(t *testing.T) {
	reg := testRegistry(t)
	root, err := NewRoot(Options{Registry: reg})
	if err != nil {
		t.Fatal(err)
	}
	for _, def := range reg.Actions() {
		cmd, ok := FindCommand(root, def.Spec.Name)
		if !ok {
			t.Errorf("%s has no command", def.Spec.Name)
			continue
		}
		mutating := def.Spec.Class != core.ClassRead
		if (cmd.Flags().Lookup("yes") != nil) != mutating {
			t.Errorf("%s: --yes present %v, want %v", def.Spec.Name, !mutating, mutating)
		}
		if (cmd.Flags().Lookup("confirmation") != nil) != (def.Spec.Class == core.ClassIrreversible) {
			t.Errorf("%s: --confirmation only on irreversible actions", def.Spec.Name)
		}
		if (cmd.Flags().Lookup("confirm") != nil) != (def.Spec.Class == core.ClassRevertible) {
			t.Errorf("%s: --confirm only on revertible actions", def.Spec.Name)
		}
	}
}

func TestMutatingCompensable(t *testing.T) {
	base := newFakeAPI(t, testToken,
		hostList("h1:web"),
		exchange{method: "POST", path: "/api/v1/hosts/h1/actions/demo.item.add/v1/plan",
			body:  `{"input":{"name":"n1","apiKey":"key-1","passphrase":"pass-1"}}`,
			reply: planJSON("p1", "demo.item.add", "h1", "", `[{"id":"name-free","ok":true,"message":"the name is free"}]`)},
		exchange{method: "POST", path: "/api/v1/hosts/h1/actions/demo.item.add/v1/apply", body: `{"planId":"p1"}`,
			reply: changeJSON("c1", "demo.item.add", "compensable", "completed",
				`"revertDeadline":null,"output":{"id":"i9","token":"tok-secret"},"error":null,"compensatedBy":"demo.item.remove"`)},
	)
	out, errOut, code := cliEnv{env: tokenEnv, stdin: "key-1\npass-1\n"}.run(t,
		"demo", "item", "add", "--name", "n1", "--yes", "--url", base)
	want := "Plan for demo.item.add/v1 on host h1:\n" +
		"  + add demo.item.add\n" +
		"    keep\n" +
		"Guards:\n" +
		"  ok      name-free: the name is free\n" +
		"Change c1: completed\n" +
		"id: i9\n" +
		"token: tok-secret\n" +
		"\nThe token above will not be shown again; store it now.\n"
	if code != 0 || out != want {
		t.Errorf("exit %d\nout %q\nwant %q\nerr %q", code, out, want, errOut)
	}
}

func TestMutatingRevertible(t *testing.T) {
	limits := filepath.Join(t.TempDir(), "limits.json")
	if err := os.WriteFile(limits, []byte(`{"limits":{"max":10}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	input := `{"input":{"limits":{"max":10},"name":"web","port":443}}`
	base := newFakeAPI(t, testToken,
		hostList("h1:web"),
		exchange{method: "POST", path: "/api/v1/hosts/h1/actions/demo.rule.set/v1/plan", body: input,
			reply: planJSON("p2", "demo.rule.set", "h1", "", `[]`)},
		exchange{method: "POST", path: "/api/v1/hosts/h1/actions/demo.rule.set/v1/apply", body: `{"planId":"p2"}`,
			reply: changeJSON("c2", "demo.rule.set", "revertible", "pending",
				`"revertDeadline":"2026-10-18T12:02:00Z","output":{"name":"web","port":443},"error":null,"compensatedBy":null`)},
		hostList("h1:web"),
		exchange{method: "POST", path: "/api/v1/hosts/h1/actions/demo.rule.set/v1/plan", body: input,
			reply: planJSON("p3", "demo.rule.set", "h1", "", `[]`)},
		exchange{method: "POST", path: "/api/v1/hosts/h1/actions/demo.rule.set/v1/apply", body: `{"planId":"p3","confirmNow":true}`,
			reply: changeJSON("c3", "demo.rule.set", "revertible", "confirmed", noExtra)},
	)
	cli := cliEnv{env: tokenEnv}
	args := []string{"demo", "rule", "set", "--name", "web", "--port", "443", "--input", limits, "--yes", "--url", base}
	out, errOut, code := cli.run(t, args...)
	if code != 0 || !strings.Contains(out, "Change c2: pending\n") ||
		!strings.Contains(out, "Confirm it before 2026-10-18T12:02:00Z with: darkpanel change confirm c2\n") ||
		!strings.Contains(out, "name: web\nport: 443\n") {
		t.Errorf("pending: exit %d, out %q, err %q", code, out, errOut)
	}
	out, errOut, code = cli.run(t, append(args, "--confirm")...)
	if code != 0 || !strings.Contains(out, "Change c3: confirmed\n") || strings.Contains(out, "Confirm it before") {
		t.Errorf("--confirm: exit %d, out %q, err %q", code, out, errOut)
	}
}

func TestMutatingIrreversible(t *testing.T) {
	plan := func(id string) exchange {
		return exchange{method: "POST", path: "/api/v1/hosts/h1/actions/demo.item.remove/v1/plan", body: `{"input":{"id":"i1"}}`,
			reply: planJSON(id, "demo.item.remove", "h1", "i1", `[]`)}
	}
	apply := func(id string) exchange {
		return exchange{method: "POST", path: "/api/v1/hosts/h1/actions/demo.item.remove/v1/apply",
			body: `{"planId":"` + id + `","confirmation":"i1"}`, reply: changeJSON("c"+id, "demo.item.remove", "irreversible", "completed", noExtra)}
	}
	base := newFakeAPI(t, testToken,
		hostList("h1:web"), plan("p1"), apply("p1"),
		hostList("h1:web"), plan("p2"),
		hostList("h1:web"), plan("p3"),
		hostList("h1:web"), plan("p4"), apply("p4"),
		hostList("h1:web"), plan("p5"),
	)
	args := []string{"demo", "item", "remove", "--id", "i1", "--url", base}
	cli := cliEnv{env: tokenEnv}
	if out, errOut, code := cli.run(t, append(args, "--yes", "--confirmation", "i1")...); code != 0 || !strings.Contains(out, "Change cp1: completed") {
		t.Errorf("--confirmation: exit %d, out %q, err %q", code, out, errOut)
	}
	if _, errOut, code := cli.run(t, append(args, "--yes", "--confirmation", "i2")...); code != ExitRefused || !strings.Contains(errOut, `"i1"`) {
		t.Errorf("wrong --confirmation: exit %d, err %q", code, errOut)
	}
	if _, errOut, code := cli.run(t, append(args, "--yes")...); code != ExitRefused || !strings.Contains(errOut, "--confirmation i1") {
		t.Errorf("no terminal, no --confirmation: exit %d, err %q", code, errOut)
	}
	term := cliEnv{env: tokenEnv, terminal: true, stdin: "y\ni1\n"}
	if out, errOut, code := term.run(t, args...); code != 0 || !strings.Contains(out, "Change cp4: completed") ||
		errOut != "Apply? [y/N] This cannot be undone. Type i1 to confirm: " {
		t.Errorf("terminal: exit %d, out %q, err %q", code, out, errOut)
	}
	term.stdin = "y\ni2\n"
	if _, errOut, code := term.run(t, args...); code != ExitRefused || !strings.Contains(errOut, "does not match") {
		t.Errorf("terminal, wrong phrase: exit %d, err %q", code, errOut)
	}
}

func TestMutatingRefusals(t *testing.T) {
	plan := func(id, guards string) exchange {
		return exchange{method: "POST", path: "/api/v1/actions/core.token.create/v1/plan", body: `{"input":{"name":"ci","scope":"read"}}`,
			reply: planJSON(id, "core.token.create", "", "", guards)}
	}
	base := newFakeAPI(t, testToken,
		plan("p1", `[{"id":"owner-active","ok":false,"message":"the owner is not active"}]`),
		plan("p2", `[]`),
		plan("p3", `[]`),
		plan("p4", `[]`),
	)
	args := []string{"token", "create", "--name", "ci", "--scope", "read", "--url", base}
	cli := cliEnv{env: tokenEnv}
	if out, errOut, code := cli.run(t, append(args, "--yes")...); code != ExitRefused ||
		!strings.Contains(out, "FAILED  owner-active: the owner is not active") || !strings.Contains(errOut, "owner-active") {
		t.Errorf("failing guard: exit %d, out %q, err %q", code, out, errOut)
	}
	if _, errOut, code := cli.run(t, args...); code != ExitRefused || !strings.Contains(errOut, "--yes") {
		t.Errorf("no terminal: exit %d, err %q", code, errOut)
	}
	for _, answer := range []string{"n\n", ""} {
		_, errOut, code := cliEnv{env: tokenEnv, terminal: true, stdin: answer}.run(t, args...)
		if code != ExitRefused || !strings.Contains(errOut, "not applied") {
			t.Errorf("answer %q: exit %d, err %q", answer, code, errOut)
		}
	}
}

func TestMutatingFailedChangeAndJSON(t *testing.T) {
	base := newFakeAPI(t, testToken,
		exchange{method: "POST", path: "/api/v1/actions/core.token.create/v1/plan", body: `{"input":{"name":"ci","scope":"read"}}`,
			reply: planJSON("p1", "core.token.create", "", "", `[]`)},
		exchange{method: "POST", path: "/api/v1/actions/core.token.create/v1/apply", body: `{"planId":"p1"}`,
			reply: changeJSON("c1", "core.token.create", "compensable", "failed",
				`"revertDeadline":null,"output":null,"error":{"code":"internal","message":"Internal error"},"compensatedBy":"core.token.revoke"`)},
		exchange{method: "POST", path: "/api/v1/actions/core.token.create/v1/plan", body: `{"input":{"name":"ci","scope":"read"}}`,
			reply: planJSON("p2", "core.token.create", "", "", `[]`)},
		exchange{method: "POST", path: "/api/v1/actions/core.token.create/v1/apply", body: `{"planId":"p2"}`,
			reply: changeJSON("c2", "core.token.create", "compensable", "completed", noExtra)},
	)
	args := []string{"token", "create", "--name", "ci", "--scope", "read", "--yes", "--url", base}
	cli := cliEnv{env: tokenEnv}
	if out, errOut, code := cli.run(t, args...); code != ExitError || !strings.Contains(out, "Change c1: failed") ||
		errOut != "darkpanel: change c1 failed: Internal error (internal)\n" {
		t.Errorf("failed change: exit %d, out %q, err %q", code, out, errOut)
	}
	out, errOut, code := cli.run(t, append(args, "--json")...)
	if code != 0 || out != changeJSON("c2", "core.token.create", "compensable", "completed", noExtra)+"\n" ||
		!strings.Contains(errOut, "Plan for core.token.create/v1:") {
		t.Errorf("--json: exit %d, out %q, err %q", code, out, errOut)
	}
}

func TestBreakGlassMutatingUsesTheSocket(t *testing.T) {
	sock := newFakeSocket(t,
		exchange{method: "POST", path: "/api/v1/actions/core.user.delete/v1/plan", body: `{"input":{"username":"bob"}}`,
			reply: planJSON("p1", "core.user.delete", "", "bob", `[{"id":"not-last-admin","ok":true,"message":"another admin remains"}]`)},
		exchange{method: "POST", path: "/api/v1/actions/core.user.delete/v1/apply", body: `{"planId":"p1","confirmation":"bob"}`,
			reply: changeJSON("c1", "core.user.delete", "irreversible", "completed", `"revertDeadline":null,"output":{},"error":null,"compensatedBy":null`)},
	)
	out, errOut, code := cliEnv{}.run(t, "user", "delete", "--username", "bob", "--yes", "--confirmation", "bob", "--socket", sock)
	if code != 0 || !strings.Contains(out, "Change c1: completed\n") {
		t.Errorf("exit %d, out %q, err %q", code, out, errOut)
	}
}

func TestMutatingSanitizesThePlan(t *testing.T) {
	base := newFakeAPI(t, testToken,
		exchange{method: "POST", path: "/api/v1/actions/core.token.create/v1/plan", body: `{"input":{"name":"ci","scope":"read"}}`,
			reply: `{"planId":"p1","hostId":null,"action":"core.token.create","version":1,"inputHash":"ih",` +
				`"diff":[{"op":"+","text":"name \u001b[2Jci\u202e"}],"guards":[{"id":"g\u001b","ok":false,"message":"no\u2066pe"}],` +
				`"expiresAt":"2026-10-18T12:05:00Z","confirmation":null}`},
	)
	out, errOut, code := cliEnv{env: tokenEnv}.run(t, "token", "create", "--name", "ci", "--scope", "read", "--yes", "--url", base)
	if code != ExitRefused || !strings.Contains(out, `  + name \x1b[2Jci\u202e`) || !strings.Contains(out, `FAILED  g\x1b: no\u2066pe`) ||
		strings.ContainsAny(out+errOut, "\x1b\u202e\u2066") {
		t.Errorf("exit %d, out %q, err %q", code, out, errOut)
	}
}

// covPlan is the plan exchange of cov.rule.set on h1 for input, answered
// with guards.
func covPlan(planID, input, guards string) exchange {
	return exchange{method: "POST", path: "/api/v1/hosts/h1/actions/cov.rule.set/v1/plan", body: `{"input":` + input + `}`,
		reply: planJSON(planID, "cov.rule.set", "h1", "", guards)}
}

func TestGuardedInputFlags(t *testing.T) {
	root, err := NewRoot(Options{Registry: testRegistry(t)})
	if err != nil {
		t.Fatal(err)
	}
	cmd, ok := FindCommand(root, "cov.rule.set")
	if !ok {
		t.Fatal("cov.rule.set has no command")
	}
	for _, name := range []string{"guard", "overrides"} {
		if cmd.Flags().Lookup(name) != nil {
			t.Errorf("a guarded action has --%s", name)
		}
	}
	override := cmd.Flags().Lookup("override")
	if override == nil || !strings.Contains(override.Usage, "cov.lockout, cov.other") || strings.Contains(override.Usage, "cov.quota") {
		t.Errorf("--override %+v, want it listing the overridable guards", override)
	}
	if cmd.Flags().Lookup("name") == nil || cmd.Flags().Lookup("input") == nil {
		t.Error("a guarded action lost its own input flags")
	}
	if cmd, _ := FindCommand(root, "demo.item.add"); cmd.Flags().Lookup("override") != nil {
		t.Error("an action without guards has --override")
	}
}

func TestOverrideReplans(t *testing.T) {
	overridden := `[{"id":"cov.lockout","ok":true,"message":"locks you out","overridable":true,"overridden":true}]`
	base := newFakeAPI(t, testToken,
		hostList("h1:web"),
		covPlan("p1", `{"name":"web","overrides":["cov.lockout","cov.other"]}`, overridden),
		exchange{method: "POST", path: "/api/v1/hosts/h1/actions/cov.rule.set/v1/apply", body: `{"planId":"p1"}`,
			reply: changeJSON("c1", "cov.rule.set", "revertible", "pending", noExtra)},
		hostList("h1:web"),
		covPlan("p2", `{"name":"web"}`, `[]`),
		exchange{method: "POST", path: "/api/v1/hosts/h1/actions/cov.rule.set/v1/apply", body: `{"planId":"p2"}`,
			reply: changeJSON("c2", "cov.rule.set", "revertible", "pending", noExtra)},
	)
	cli := cliEnv{env: tokenEnv}
	args := []string{"cov", "rule", "set", "--name", "web", "--yes", "--url", base}
	if out, errOut, code := cli.run(t, append(args, "--override", "cov.lockout", "--override", "cov.other")...); code != 0 ||
		!strings.Contains(out, "Change c1: pending") {
		t.Errorf("--override: exit %d, out %q, err %q", code, out, errOut)
	}
	if out, errOut, code := cli.run(t, args...); code != 0 || !strings.Contains(out, "Change c2: pending") {
		t.Errorf("without --override: exit %d, out %q, err %q", code, out, errOut)
	}
}

func TestFailedOverridableGuardHint(t *testing.T) {
	for _, tc := range []struct {
		guards string
		hint   bool
	}{
		{`[{"id":"cov.lockout","ok":false,"message":"locks you out","overridable":true,"overridden":false}]`, true},
		{`[{"id":"cov.quota","ok":false,"message":"over quota","overridable":false,"overridden":false}]`, false},
	} {
		base := newFakeAPI(t, testToken, hostList("h1:web"), covPlan("p1", `{"name":"web"}`, tc.guards))
		out, errOut, code := cliEnv{env: tokenEnv}.run(t, "cov", "rule", "set", "--name", "web", "--yes", "--url", base)
		if code != ExitRefused || !strings.Contains(out, "FAILED") || !strings.Contains(errOut, "nothing was applied") {
			t.Errorf("%s: exit %d, out %q, err %q; want the guard refused", tc.guards, code, out, errOut)
		}
		if got := strings.Contains(out, "override it with --override cov.lockout\n"); got != tc.hint {
			t.Errorf("%s: hint shown %v, want %v; out %q", tc.guards, got, tc.hint, out)
		}
	}
}

func TestConfirmUnchanged(t *testing.T) {
	base := newFakeAPI(t, testToken,
		hostList("h1:web"),
		covPlan("p1", `{"name":"web","overrides":["cov.lockout"]}`, `[]`),
		exchange{method: "POST", path: "/api/v1/hosts/h1/actions/cov.rule.set/v1/apply", body: `{"planId":"p1","confirmNow":true}`,
			reply: changeJSON("c1", "cov.rule.set", "revertible", "confirmed", noExtra)},
	)
	out, errOut, code := cliEnv{env: tokenEnv}.run(t,
		"cov", "rule", "set", "--name", "web", "--override", "cov.lockout", "--yes", "--confirm", "--url", base)
	if code != 0 || !strings.Contains(out, "Change c1: confirmed") || strings.Contains(out, "Confirm it before") {
		t.Errorf("--confirm with --override: exit %d, out %q, err %q", code, out, errOut)
	}
}
