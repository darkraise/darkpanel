package cli

import (
	"strings"
	"testing"
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
