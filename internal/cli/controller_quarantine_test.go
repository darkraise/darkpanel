package cli

import (
	"net/http"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// cqRun runs the quarantine group as a top-level command, so the tests do not
// depend on where the assembly mounts it.
func cqRun(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	return cliEnv{commands: []*cobra.Command{(&app{}).quarantineCommand()}}.run(t, args...)
}

// cqFields splits out into lines of whitespace-separated words, so a test does
// not depend on column padding.
func cqFields(out string) [][]string {
	var lines [][]string
	for _, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		lines = append(lines, strings.Fields(l))
	}
	return lines
}

func cqSameLines(got, want [][]string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if strings.Join(got[i], "\x00") != strings.Join(want[i], "\x00") {
			return false
		}
	}
	return true
}

const (
	cqBase    = "/api/v1/controller/quarantine"
	cqRecordC = `{"id":"c1","kind":"change","action":"core.host.create","hostId":"h1","quarantinedFrom":"applying","locks":["net.a","net.b"]}`
	cqRecordR = `{"id":"r1","kind":"rollout","action":"core.update.rollout","hostId":"","quarantinedFrom":"running","locks":[]}`
	cqActive  = `"active":true,"tx":"01JTX","restoredAt":"2026-11-04T12:00:00Z"`
	cqHeader  = "Restore quarantine is active: restore 01JTX, restored 2026-11-04T12:00:00Z.\n"
)

func TestCqList(t *testing.T) {
	table := [][]string{
		{"ID", "KIND", "ACTION", "HOST", "FROM", "LOCKS"},
		{"c1", "change", "core.host.create", "h1", "applying", "net.a,net.b"},
		{"r1", "rollout", "core.update.rollout", "-", "running", "-"},
	}
	records := `"records":[` + cqRecordC + `,` + cqRecordR + `]`
	cases := []struct {
		name, reply, head string
		table             [][]string
		tail              string
	}{
		{"active with records", `{` + cqActive + `,` + records + `}`, cqHeader, table, ""},
		{"active without records", `{` + cqActive + `,"records":[]}`, cqHeader, nil, "No records are quarantined.\n"},
		{"kept records after release", `{"active":false,` + records + `}`, "The controller is not in restore quarantine.\n", table, ""},
		{"nothing", `{"active":false,"records":[]}`, "The controller is not in restore quarantine.\n", nil, "No records are quarantined.\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sock := newFakeSocket(t, exchange{method: "GET", path: cqBase, reply: c.reply})
			out, errOut, code := cqRun(t, "quarantine", "list", "--socket", sock)
			if code != 0 || errOut != "" {
				t.Fatalf("exit %d, err %q", code, errOut)
			}
			rest, ok := strings.CutPrefix(out, c.head)
			if !ok {
				t.Fatalf("output %q does not start with %q", out, c.head)
			}
			if c.table == nil {
				if rest != c.tail {
					t.Errorf("after the heading: %q, want %q", rest, c.tail)
				}
				return
			}
			if got := cqFields(rest); !cqSameLines(got, c.table) {
				t.Errorf("table = %v, want %v", got, c.table)
			}
		})
	}
}

func TestCqListJSONPrintsTheAnswerAsSent(t *testing.T) {
	reply := `{` + cqActive + `,"records":[` + cqRecordC + `],"future":1}`
	sock := newFakeSocket(t, exchange{method: "GET", path: cqBase, reply: reply})
	out, errOut, code := cqRun(t, "quarantine", "list", "--json", "--socket", sock)
	if code != 0 || errOut != "" || !jsonEqual([]byte(out), []byte(reply)) {
		t.Errorf("--json: exit %d, out %q, err %q; want the answer unchanged", code, out, errOut)
	}
}

func TestCqListNeverWritesServerControlCharacters(t *testing.T) {
	reply := `{"active":false,"records":[{"id":"c1","kind":"change","action":"evil\u001b[2Jaction","hostId":"","quarantinedFrom":"applying","locks":[]}]}`
	sock := newFakeSocket(t, exchange{method: "GET", path: cqBase, reply: reply})
	out, _, code := cqRun(t, "quarantine", "list", "--socket", sock)
	if code != 0 || strings.Contains(out, "\x1b") || !strings.Contains(out, `evil\x1b[2Jaction`) {
		t.Errorf("exit %d, out %q; want the escape written as text", code, out)
	}
}

func TestCqResolve(t *testing.T) {
	answer := func(id, kind string) string {
		return `{"id":"` + id + `","kind":"` + kind + `","action":"x","hostId":"","quarantinedFrom":"applying","locks":[]}`
	}
	cases := []struct {
		args    []string
		path    string
		body    string
		reply   string
		wantOut string
	}{
		{[]string{"c1", "done"}, cqBase + "/c1/resolve", `{"outcome":"done"}`, answer("c1", "change"),
			"Resolved change c1 as done; its locks are released.\n"},
		{[]string{"k1", "never"}, cqBase + "/k1/resolve", `{"outcome":"never"}`, answer("k1", "composite"),
			"Resolved composite k1 as never; its locks are released.\n"},
		{[]string{"k1", "repair"}, cqBase + "/k1/resolve", `{"outcome":"repair"}`, answer("k1", "composite"),
			"Resolved composite k1 as repair; its locks stay held.\nRepair it after release: darkpanel change repair k1\n"},
		{[]string{"a/b", "done"}, cqBase + "/a%2Fb/resolve", `{"outcome":"done"}`, answer("a/b", "change"),
			"Resolved change a/b as done; its locks are released.\n"},
	}
	for _, c := range cases {
		t.Run(strings.Join(c.args, " "), func(t *testing.T) {
			sock := newFakeSocket(t, exchange{method: "POST", path: c.path, body: c.body, reply: c.reply})
			out, errOut, code := cqRun(t, append([]string{"quarantine", "resolve"}, append(c.args, "--socket", sock)...)...)
			if code != 0 || errOut != "" || out != c.wantOut {
				t.Errorf("exit %d\nout %q\nwant %q\nerr %q", code, out, c.wantOut, errOut)
			}
		})
	}
	reply := answer("c1", "change")
	sock := newFakeSocket(t, exchange{method: "POST", path: cqBase + "/c1/resolve", body: `{"outcome":"repair"}`, reply: reply})
	out, errOut, code := cqRun(t, "quarantine", "resolve", "c1", "repair", "--json", "--socket", sock)
	if code != 0 || errOut != "" || !jsonEqual([]byte(out), []byte(reply)) {
		t.Errorf("--json: exit %d, out %q, err %q; want the answer unchanged", code, out, errOut)
	}
}

func TestCqEnrollToken(t *testing.T) {
	reply := `{"token":"dpt_secret_value","expiresAt":"2026-11-04T13:00:00Z"}`
	ex := exchange{method: "POST", path: cqBase + "/enroll-token", body: `{"hostId":"web-1"}`, reply: reply}
	sock := newFakeSocket(t, ex, ex)
	out, errOut, code := cqRun(t, "quarantine", "enroll-token", "web-1", "--socket", sock)
	want := "Enrollment token for host web-1 (valid until 2026-11-04T13:00:00Z, shown once):\ndpt_secret_value\n"
	if code != 0 || errOut != "" || out != want {
		t.Errorf("exit %d\nout %q\nwant %q\nerr %q", code, out, want, errOut)
	}
	out, errOut, code = cqRun(t, "quarantine", "enroll-token", "web-1", "--json", "--socket", sock)
	if code != 0 || errOut != "" || !jsonEqual([]byte(out), []byte(reply)) {
		t.Errorf("--json: exit %d, out %q, err %q; want the answer unchanged", code, out, errOut)
	}
}

func TestCqRelease(t *testing.T) {
	held := `{"heldLocks":[{"key":"dns.x","record":"q2"},{"key":"net.a","record":"q1"}]}`
	sock := newFakeSocket(t,
		exchange{method: "POST", path: cqBase + "/release", body: `{"keepUnresolved":false}`, reply: `{"heldLocks":[]}`},
		exchange{method: "POST", path: cqBase + "/release", body: `{"keepUnresolved":true}`, reply: held},
		exchange{method: "POST", path: cqBase + "/release", body: `{"keepUnresolved":true}`, reply: held},
	)
	out, errOut, code := cqRun(t, "quarantine", "release", "--socket", sock)
	if want := "Restore quarantine released. No locks stay held.\n"; code != 0 || errOut != "" || out != want {
		t.Errorf("plain release: exit %d\nout %q\nwant %q\nerr %q", code, out, want, errOut)
	}

	out, errOut, code = cqRun(t, "quarantine", "release", "--keep-unresolved", "--socket", sock)
	head := "Restore quarantine released. These locks stay held by quarantined records:\n"
	rest, ok := strings.CutPrefix(out, head)
	wantTable := [][]string{{"KEY", "RECORD"}, {"dns.x", "q2"}, {"net.a", "q1"}}
	if code != 0 || errOut != "" || !ok || !cqSameLines(cqFields(rest), wantTable) {
		t.Errorf("--keep-unresolved: exit %d\nout %q\nwant %q then %v\nerr %q", code, out, head, wantTable, errOut)
	}

	out, errOut, code = cqRun(t, "quarantine", "release", "--keep-unresolved", "--json", "--socket", sock)
	if code != 0 || errOut != "" || !jsonEqual([]byte(out), []byte(held)) {
		t.Errorf("--json: exit %d, out %q, err %q; want the answer unchanged", code, out, errOut)
	}
}

func TestCqArgumentsAreCheckedBeforeAnyRequest(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string // in the error, when set
	}{
		{"outcome not one of the three", []string{"resolve", "c1", "maybe"}, `the outcome is done, never or repair, not "maybe"`},
		{"outcome in the wrong case", []string{"resolve", "c1", "DONE"}, `not "DONE"`},
		{"no outcome", []string{"resolve", "c1"}, ""},
		{"no record", []string{"resolve"}, ""},
		{"a third argument", []string{"resolve", "c1", "done", "extra"}, ""},
		{"an empty record id", []string{"resolve", "", "done"}, "record"},
		{"enroll-token without a host", []string{"enroll-token"}, ""},
		{"enroll-token with two hosts", []string{"enroll-token", "a", "b"}, ""},
		{"enroll-token with an empty host", []string{"enroll-token", ""}, "host"},
		{"list with an argument", []string{"list", "extra"}, ""},
		{"release with an argument", []string{"release", "extra"}, ""},
		{"release with an unknown flag", []string{"release", "--force"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sock := newFakeSocket(t) // any request fails the test
			_, errOut, code := cqRun(t, append(append([]string{"quarantine"}, c.args...), "--socket", sock)...)
			if code != ExitUsage {
				t.Errorf("exit %d, err %q; want %d", code, errOut, ExitUsage)
			}
			if c.want != "" && !strings.Contains(errOut, c.want) {
				t.Errorf("err %q does not contain %q", errOut, c.want)
			}
		})
	}
	if out, errOut, code := cqRun(t, "quarantine"); code != 0 || !strings.Contains(out, "list") || errOut != "" {
		t.Errorf("bare quarantine: exit %d, out %q, err %q; want its help", code, out, errOut)
	}
	if _, errOut, code := cqRun(t, "quarantine", "bogus"); code != ExitUsage || !strings.Contains(errOut, `unknown command "bogus"`) {
		t.Errorf("quarantine bogus: exit %d, err %q; want a usage error naming the command", code, errOut)
	}
}

func TestCqPrintsAProblemAnswerLikeOtherCommands(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		ex      exchange
		wantErr string
	}{
		{"release refused", []string{"release"},
			exchange{method: "POST", path: cqBase + "/release", body: `{"keepUnresolved":false}`, status: http.StatusConflict, problem: true,
				reply: `{"status":409,"detail":"2 records are still quarantined; resolve each one, or release with --keep-unresolved","code":"core.quarantined"}`},
			"darkpanel: 2 records are still quarantined; resolve each one, or release with --keep-unresolved (core.quarantined)\n"},
		{"resolve busy", []string{"resolve", "k1", "done"},
			exchange{method: "POST", path: cqBase + "/k1/resolve", body: `{"outcome":"done"}`, status: http.StatusConflict, problem: true,
				reply: `{"status":409,"detail":"step s1 is still running","code":"core.change_busy"}`},
			"darkpanel: step s1 is still running (core.change_busy)\n"},
		{"enroll-token with a field error", []string{"enroll-token", "web-1"},
			exchange{method: "POST", path: cqBase + "/enroll-token", body: `{"hostId":"web-1"}`, status: http.StatusUnprocessableEntity, problem: true,
				reply: `{"status":422,"detail":"not valid","code":"request.invalid_input","errors":[{"path":"/hostId","message":"is the local host"}]}`},
			"darkpanel: not valid (request.invalid_input)\n  /hostId: is the local host\n"},
		{"control characters in the detail", []string{"list"},
			exchange{method: "GET", path: cqBase, status: http.StatusConflict, problem: true,
				reply: `{"status":409,"detail":"held\u001b[31m","code":"core.quarantined"}`},
			`darkpanel: held\x1b[31m (core.quarantined)` + "\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sock := newFakeSocket(t, c.ex)
			out, errOut, code := cqRun(t, append(append([]string{"quarantine"}, c.args...), "--socket", sock)...)
			if code != ExitError || out != "" || errOut != c.wantErr {
				t.Errorf("exit %d, out %q\nerr %q\nwant exit %d and %q", code, out, errOut, ExitError, c.wantErr)
			}
		})
	}
}
