package cli

import (
	"strings"
	"testing"
	"time"
)

func TestChangeList(t *testing.T) {
	pending := changeJSON("c1", "demo.rule.set", "revertible", "pending",
		`"revertDeadline":"2026-10-18T12:02:00Z","output":{"name":"web"},"error":null,"compensatedBy":null`)
	base := newFakeAPI(t, testToken,
		exchange{method: "GET", path: "/api/v1/changes?limit=5&state=pending", reply: `{"changes":[` + pending + `]}`},
		hostList("h1:web"),
		exchange{method: "GET", path: "/api/v1/changes?before=c9&hostId=h1", reply: `{"changes":[]}`},
		exchange{method: "GET", path: "/api/v1/changes", reply: `{"changes":[` + pending + `]}`},
	)
	cli := cliEnv{env: tokenEnv}
	out, errOut, code := cli.run(t, "change", "list", "--state", "pending", "--limit", "5", "--url", base)
	want := "id  hostId  action         version  class       state    actor     updatedAt             revertDeadline\n" +
		"c1  h1      demo.rule.set  1        revertible  pending  token:t1  2026-10-18T12:00:01Z  2026-10-18T12:02:00Z\n"
	if code != 0 || out != want {
		t.Errorf("list: exit %d\nout %q\nwant %q\nerr %q", code, out, want, errOut)
	}
	out, errOut, code = cli.run(t, "change", "list", "--host", "web", "--before", "c9", "--url", base)
	if code != 0 || out != "(none)\n" {
		t.Errorf("empty list: exit %d, out %q, err %q", code, out, errOut)
	}
	out, errOut, code = cli.run(t, "change", "list", "--json", "--url", base)
	if code != 0 || out != `{"changes":[`+pending+"]}\n" {
		t.Errorf("--json: exit %d, out %q, err %q", code, out, errOut)
	}
}

func TestChangeShow(t *testing.T) {
	interrupted := changeJSON("c2", "demo.item.add", "compensable", "interrupted",
		`"revertDeadline":null,"output":null,"error":{"code":"internal","message":"Internal error"},"compensatedBy":"demo.item.remove"`)
	base := newFakeAPI(t, testToken,
		exchange{method: "GET", path: "/api/v1/changes/c2", reply: interrupted},
		exchange{method: "GET", path: "/api/v1/changes/nope", status: 404, problem: true,
			reply: `{"type":"about:blank","title":"Change not found","status":404,"detail":"no change nope","code":"change.not_found"}`},
	)
	cli := cliEnv{env: tokenEnv}
	out, errOut, code := cli.run(t, "change", "show", "c2", "--url", base)
	for _, line := range []string{"id: c2\n", "state: interrupted\n", "error.code: internal\n", "compensatedBy: demo.item.remove\n",
		"\nThis change was interrupted and may be partly applied; its compensating action is demo.item.remove:\n" +
			"  darkpanel demo item remove\n"} {
		if !strings.Contains(out, line) {
			t.Errorf("show: out %q lacks %q (exit %d, err %q)", out, line, code, errOut)
		}
	}
	if code != 0 {
		t.Errorf("show: exit %d", code)
	}
	if _, errOut, code := cli.run(t, "change", "show", "nope", "--url", base); code != ExitError ||
		errOut != "darkpanel: no change nope (change.not_found)\n" {
		t.Errorf("not found: exit %d, err %q", code, errOut)
	}
}

func TestChangeConfirmAndRevert(t *testing.T) {
	base := newFakeAPI(t, testToken,
		exchange{method: "POST", path: "/api/v1/changes/c1/confirm", reply: changeJSON("c1", "demo.rule.set", "revertible", "confirmed", noExtra)},
		exchange{method: "POST", path: "/api/v1/changes/c2/revert", reply: changeJSON("c2", "demo.rule.set", "revertible", "reverted", noExtra)},
		exchange{method: "POST", path: "/api/v1/changes/c3/revert", reply: changeJSON("c3", "demo.rule.set", "revertible", "failed",
			`"revertDeadline":null,"output":null,"error":{"code":"change.revert_failed","message":"Revert failed"},"compensatedBy":null`)},
	)
	cli := cliEnv{env: tokenEnv}
	if out, errOut, code := cli.run(t, "change", "confirm", "c1", "--url", base); code != 0 || out != "Change c1: confirmed\n" {
		t.Errorf("confirm: exit %d, out %q, err %q", code, out, errOut)
	}
	if out, errOut, code := cli.run(t, "change", "revert", "c2", "--url", base); code != 0 || out != "Change c2: reverted\n" {
		t.Errorf("revert: exit %d, out %q, err %q", code, out, errOut)
	}
	if out, errOut, code := cli.run(t, "change", "revert", "c3", "--url", base); code != ExitError || out != "Change c3: failed\n" ||
		errOut != "darkpanel: change c3 is failed, not reverted: Revert failed (change.revert_failed)\n" {
		t.Errorf("failed revert: exit %d, out %q, err %q", code, out, errOut)
	}
	for _, args := range [][]string{{"change", "confirm"}, {"change", "revert", "a", "b"}, {"change", "show"}} {
		if _, errOut, code := cli.run(t, args...); code != ExitUsage {
			t.Errorf("%q: exit %d, err %q", args, code, errOut)
		}
	}
}

// changeFastPoll makes the change polling of one test immediate.
func changeFastPoll(t *testing.T) {
	t.Helper()
	saved := changePoll
	changePoll = time.Millisecond
	t.Cleanup(func() { changePoll = saved })
}

func TestChangeWaitsForAnAsynchronousApply(t *testing.T) {
	changeFastPoll(t)
	completed := changeJSON("c1", "demo.item.add", "compensable", "completed",
		`"revertDeadline":null,"output":{"id":"i9","token":"tok-secret"},"error":null,"compensatedBy":"demo.item.remove"`)
	base := newFakeAPI(t, testToken,
		hostList("h1:web"),
		exchange{method: "POST", path: "/api/v1/hosts/h1/actions/demo.item.add/v1/plan",
			body:  `{"input":{"name":"n1","apiKey":"key-1","passphrase":"pass-1"}}`,
			reply: planJSON("p1", "demo.item.add", "h1", "", `[]`)},
		exchange{method: "POST", path: "/api/v1/hosts/h1/actions/demo.item.add/v1/apply", body: `{"planId":"p1"}`, status: 202,
			reply: changeJSON("c1", "demo.item.add", "compensable", "applying", noExtra)},
		exchange{method: "GET", path: "/api/v1/changes/c1", reply: changeJSON("c1", "demo.item.add", "compensable", "applying", noExtra)},
		exchange{method: "GET", path: "/api/v1/changes/c1", reply: completed},
	)
	out, errOut, code := cliEnv{env: tokenEnv, stdin: "key-1\npass-1\n"}.run(t,
		"demo", "item", "add", "--name", "n1", "--yes", "--url", base)
	if code != 0 || !strings.Contains(out, "Change c1: completed\n") || strings.Contains(out, "applying") {
		t.Errorf("exit %d, out %q, err %q; want the completed change only", code, out, errOut)
	}
}

func TestChangeUndo(t *testing.T) {
	changeFastPoll(t)
	needsRepair := changeJSON("c2", "demo.item.add", "compensable", "needs_repair",
		`"revertDeadline":null,"output":null,"error":{"code":"core.needs_repair","message":"effect 1 (demo.item:i1) is unresolved",`+
			`"cause":"change.revert_failed"},"compensatedBy":null,"owner":null`)
	base := newFakeAPI(t, testToken,
		exchange{method: "POST", path: "/api/v1/changes/c1/undo", status: 202,
			reply: changeJSON("c1", "demo.item.add", "compensable", "undoing", noExtra)},
		exchange{method: "GET", path: "/api/v1/changes/c1", reply: changeJSON("c1", "demo.item.add", "compensable", "undoing", noExtra)},
		exchange{method: "GET", path: "/api/v1/changes/c1", reply: changeJSON("c1", "demo.item.add", "compensable", "reverted", noExtra)},
		exchange{method: "POST", path: "/api/v1/changes/c2/undo", status: 202,
			reply: changeJSON("c2", "demo.item.add", "compensable", "undoing", noExtra)},
		exchange{method: "GET", path: "/api/v1/changes/c2", reply: needsRepair},
	)
	cli := cliEnv{env: tokenEnv}
	if out, errOut, code := cli.run(t, "change", "undo", "c1", "--url", base); code != 0 || out != "Change c1: reverted\n" {
		t.Errorf("undo: exit %d, out %q, err %q", code, out, errOut)
	}
	out, errOut, code := cli.run(t, "change", "undo", "c2", "--url", base)
	wantOut := "Change c2: needs_repair\n" +
		"Needs repair: effect 1 (demo.item:i1) is unresolved (core.needs_repair, caused by change.revert_failed)\n" +
		"Repair it with: darkpanel change repair c2\n" +
		"Or give it up with: darkpanel change release c2 --reason <why>\n"
	if code != ExitError || out != wantOut ||
		errOut != "darkpanel: change c2 needs_repair: effect 1 (demo.item:i1) is unresolved (core.needs_repair)\n" {
		t.Errorf("undo to needs_repair: exit %d\nout %q\nwant %q\nerr %q", code, out, wantOut, errOut)
	}
}

func TestChangeRepair(t *testing.T) {
	changeFastPoll(t)
	base := newFakeAPI(t, testToken,
		exchange{method: "POST", path: "/api/v1/changes/c1/repair", body: `{}`, status: 202,
			reply: `{"change":` + changeJSON("c1", "demo.item.add", "compensable", "undoing", noExtra) + `}`},
		exchange{method: "GET", path: "/api/v1/changes/c1", reply: changeJSON("c1", "demo.item.add", "compensable", "reverted", noExtra)},
		exchange{method: "POST", path: "/api/v1/changes/c2/repair", body: `{"input":{"outcome":"keep"}}`,
			reply: `{"plan":` + planJSON("p9", "demo.item.add", "h1", "", `[]`) + `}`},
		exchange{method: "POST", path: "/api/v1/hosts/h1/actions/demo.item.add/v1/apply", body: `{"planId":"p9"}`,
			reply: changeJSON("c3", "demo.item.add", "compensable", "completed", noExtra)},
		exchange{method: "POST", path: "/api/v1/changes/c4/repair", body: `{"step":"db","as":"applied"}`, status: 202,
			reply: `{"change":` + changeJSON("c4", "cfx.deploy.add", "composite", "completed", noExtra) + `}`},
		exchange{method: "POST", path: "/api/v1/changes/c5/repair", body: `{}`,
			reply: `{"plan":` + planJSON("p8", "other.thing.do", "h1", "", `[]`) + `}`},
	)
	cli := cliEnv{env: tokenEnv}
	if out, errOut, code := cli.run(t, "change", "repair", "c1", "--url", base); code != 0 || out != "Change c1: reverted\n" {
		t.Errorf("repair by undo: exit %d, out %q, err %q", code, out, errOut)
	}
	out, errOut, code := cli.run(t, "change", "repair", "c2", "--input", `{"outcome":"keep"}`, "--yes", "--url", base)
	wantOut := "Plan for demo.item.add/v1 on host h1:\n  + add demo.item.add\n    keep\nChange c3: completed\n"
	if code != 0 || out != wantOut {
		t.Errorf("repair by plan: exit %d\nout %q\nwant %q\nerr %q", code, out, wantOut, errOut)
	}
	if out, errOut, code := cli.run(t, "change", "repair", "c4", "--step", "db", "--as", "applied", "--url", base); code != 0 ||
		out != "Change c4: completed\n" {
		t.Errorf("step decision: exit %d, out %q, err %q", code, out, errOut)
	}
	if _, errOut, code := cli.run(t, "change", "repair", "c5", "--yes", "--url", base); code != ExitError ||
		!strings.Contains(errOut, "other.thing.do/v1, which this darkpanel does not know") {
		t.Errorf("plan of an unknown action: exit %d, err %q", code, errOut)
	}
	for _, args := range [][]string{
		{"change", "repair", "c1", "--step", "db"},
		{"change", "repair", "c1", "--as", "applied"},
		{"change", "repair", "c1", "--step", "db", "--as", "maybe"},
		{"change", "repair", "c1", "--input", `[1]`},
		{"change", "repair", "c1", "--input", `{}`, "--step", "db", "--as", "applied"},
	} {
		if _, errOut, code := cli.run(t, append(args, "--url", base)...); code != ExitUsage {
			t.Errorf("%q: exit %d, err %q; want a usage error", args, code, errOut)
		}
	}
}

func TestChangeRelease(t *testing.T) {
	base := newFakeAPI(t, testToken,
		exchange{method: "POST", path: "/api/v1/changes/c1/release", body: `{"reason":"host rebuilt"}`,
			reply: `{"change":` + changeJSON("c1", "demo.item.add", "compensable", "abandoned", noExtra) +
				`,"abandoned":[{"changeId":"c1","seq":1,"kind":"create","resource":"demo.item:i1","state":"abandoned"}]}`},
	)
	cli := cliEnv{env: tokenEnv}
	out, errOut, code := cli.run(t, "change", "release", "c1", "--reason", "host rebuilt", "--url", base)
	if code != 0 || !strings.HasPrefix(out, "Change c1: abandoned\nAbandoned effects:\n") ||
		!strings.Contains(out, "demo.item:i1") || !strings.Contains(out, "create") {
		t.Errorf("release: exit %d, out %q, err %q", code, out, errOut)
	}
	if _, errOut, code := cli.run(t, "change", "release", "c1", "--url", base); code != ExitUsage {
		t.Errorf("release without a reason: exit %d, err %q", code, errOut)
	}
}

func TestChangeResolve(t *testing.T) {
	base := newFakeAPI(t, testToken,
		exchange{method: "POST", path: "/api/v1/changes/c1/effects/2/resolve",
			body:  `{"resolution":"reconcile","resource":"demo.item:i7"}`,
			reply: `{"changeId":"c1","seq":2,"state":"made","resolution":"reconcile","resolvedBy":"token:t1"}`},
	)
	cli := cliEnv{env: tokenEnv}
	out, errOut, code := cli.run(t, "change", "resolve", "c1", "reconcile", "--effect", "2", "--resource", "demo.item:i7", "--url", base)
	if want := "changeId: c1\nseq: 2\nstate: made\nresolution: reconcile\nresolvedBy: token:t1\n"; code != 0 || out != want {
		t.Errorf("resolve: exit %d\nout %q\nwant %q\nerr %q", code, out, want, errOut)
	}
	for _, args := range [][]string{
		{"change", "resolve", "c1", "ignore", "--effect", "2"},
		{"change", "resolve", "c1", "accept"},
		{"change", "resolve", "c1"},
	} {
		if _, errOut, code := cli.run(t, append(args, "--url", base)...); code != ExitUsage {
			t.Errorf("%q: exit %d, err %q; want a usage error", args, code, errOut)
		}
	}
}

func TestChangeEffectsAndSteps(t *testing.T) {
	base := newFakeAPI(t, testToken,
		exchange{method: "GET", path: "/api/v1/changes/c1/effects", reply: `{"effects":[{"seq":1,"kind":"modify","state":"made"}]}`},
		exchange{method: "GET", path: "/api/v1/changes/c1/effects/1/observe",
			reply: `{"effect":{"seq":1},"observation":{"matches":1},"allowed":["accept","reconcile"]}`},
		exchange{method: "GET", path: "/api/v1/changes/c1/steps", reply: `{"steps":[{"id":"db","state":"applied"}]}`},
		exchange{method: "GET", path: "/api/v1/changes/c1/steps", reply: `{"steps":[]}`},
	)
	cli := cliEnv{env: tokenEnv}
	if out, errOut, code := cli.run(t, "change", "effects", "c1", "--url", base); code != 0 ||
		out != "seq  kind    state\n1    modify  made\n" {
		t.Errorf("effects: exit %d, out %q, err %q", code, out, errOut)
	}
	if out, errOut, code := cli.run(t, "change", "effects", "c1", "--observe", "1", "--url", base); code != 0 ||
		out != "effect.seq: 1\nobservation.matches: 1\nallowed: accept, reconcile\n" {
		t.Errorf("observe: exit %d, out %q, err %q", code, out, errOut)
	}
	if out, errOut, code := cli.run(t, "change", "steps", "c1", "--url", base); code != 0 || out != "id  state\ndb  applied\n" {
		t.Errorf("steps: exit %d, out %q, err %q", code, out, errOut)
	}
	if out, errOut, code := cli.run(t, "change", "steps", "c1", "--json", "--url", base); code != 0 || out != `{"steps":[]}`+"\n" {
		t.Errorf("steps --json: exit %d, out %q, err %q", code, out, errOut)
	}
	if _, errOut, code := cli.run(t, "change", "effects", "c1", "--observe", "0", "--url", base); code != ExitUsage {
		t.Errorf("--observe 0: exit %d, err %q", code, errOut)
	}
}

func TestChangeShowsLifecycleFields(t *testing.T) {
	base := newFakeAPI(t, testToken,
		exchange{method: "GET", path: "/api/v1/changes/c5", reply: changeJSON("c5", "demo.rule.set", "revertible", "needs_repair",
			`"revertDeadline":null,"output":null,"error":{"code":"core.needs_repair","message":"m","cause":"change.revert_failed"},`+
				`"compensatedBy":null,"parentChangeId":null,"owner":"repair","repairedBy":"c6","repairOutcome":null,"override":null`)},
	)
	out, errOut, code := cliEnv{env: tokenEnv}.run(t, "change", "show", "c5", "--url", base)
	for _, line := range []string{"owner: repair\n", "error.cause: change.revert_failed\n", "repairedBy: c6\n"} {
		if !strings.Contains(out, line) {
			t.Errorf("show: out %q lacks %q (exit %d, err %q)", out, line, code, errOut)
		}
	}
}
