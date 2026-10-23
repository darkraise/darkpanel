package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

type cflLogParams struct {
	Tail  int      `json:"tail,omitempty" jsonschema_description:"Lines to show before following."`
	Names []string `json:"names,omitempty" jsonschema_description:"Containers to follow."`
}

type cflLogLine struct {
	Line string `json:"line"`
}

// cflModule is module "cfl": a stream, cfl.logs, beside an action of
// another noun, cfl.item.get.
type cflModule struct{}

func (cflModule) Manifest() core.Manifest {
	return core.Manifest{ID: "cfl", Version: 1, DefaultEnabled: true, Actions: []core.ActionDef{
		core.DefineRead(core.ActionSpec{Name: "cfl.item.get", Version: 1, Side: core.SideAgent, Summary: "Cfl item get."},
			fixtureHandler[demoItemQuery, demoItem]{}),
	}}
}

func (cflModule) Streams() []core.StreamDef {
	return []core.StreamDef{core.DefineStream("logs", func(context.Context, core.Env, cflLogParams, func(cflLogLine) error) error {
		return nil
	})}
}

// cflEvents is an SSE body: two events, a heartbeat, then an end frame
// with end's data.
func cflEvents(end string) string {
	return "event: data\ndata: {\"line\":\"a\"}\n\n: ping\n\nevent: data\ndata: {\"line\":\"b\"}\n\nevent: end\ndata: " + end + "\n\n"
}

func TestFollowCommand(t *testing.T) {
	tests := []struct {
		name, end  string
		code       int
		wantStderr string
	}{
		{"done", `{"reason":"done"}`, ExitOK, ""},
		{"overflow", `{"reason":"overflow"}`, ExitError, "the stream ended: overflow"},
		{"expired", `{"reason":"expired"}`, ExitError, "the stream ended: expired"},
		{"revoked", `{"reason":"revoked"}`, ExitError, "the stream ended: revoked"},
		{"error", `{"reason":"error","error":{"code":"not_found","detail":"no container c1"}}`, ExitError,
			"the stream ended: error: no container c1 (not_found)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := newFakeAPI(t, testToken, hostList("h1:web", "h2:db"),
				exchange{method: "GET", path: "/api/v1/hosts/h1/streams/cfl/logs?names=x&names=y&tail=10", reply: cflEvents(tt.end)})
			stdout, stderr, code := cliEnv{env: tokenEnv}.run(t, "cfl", "logs", "--follow", "--tail", "10",
				"--names", "x", "--names", "y", "--host", "h1", "--url", base)
			if code != tt.code || stdout != `{"line":"a"}`+"\n"+`{"line":"b"}`+"\n" || !strings.Contains(stderr, tt.wantStderr) {
				t.Errorf("exit %d, stdout %q, stderr %q; want exit %d, both lines and %q", code, stdout, stderr, tt.code, tt.wantStderr)
			}
		})
	}
	base := newFakeAPI(t, testToken, hostList("h1:web"),
		exchange{method: "GET", path: "/api/v1/hosts/h1/streams/cfl/logs", reply: "event: data\ndata: {\"line\":\"a\"}\n\n"})
	if _, stderr, code := (cliEnv{env: tokenEnv}).run(t, "cfl", "logs", "--follow", "--url", base); code != ExitError ||
		!strings.Contains(stderr, "without an end event") {
		t.Errorf("a stream without an end: exit %d, stderr %q", code, stderr)
	}
	base = newFakeAPI(t, testToken, hostList("h1:web"), exchange{method: "GET", path: "/api/v1/hosts/h1/streams/cfl/logs",
		status: http.StatusTooManyRequests, problem: true, reply: `{"status":429,"code":"core.stream_limit","detail":"user:u1 has 4 streams open"}`})
	if _, stderr, code := (cliEnv{env: tokenEnv}).run(t, "cfl", "logs", "--follow", "--url", base); code != ExitError ||
		!strings.Contains(stderr, "user:u1 has 4 streams open (core.stream_limit)") {
		t.Errorf("a refused stream: exit %d, stderr %q", code, stderr)
	}
}

// TestFollowPrintsAsItArrives keeps the stream open until the first event
// has been printed.
func TestFollowPrintsAsItArrives(t *testing.T) {
	printed := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			_, _ = io.WriteString(w, `{"output":{"hosts":[{"id":"h1","name":"web"}]}}`)
			return
		}
		_, _ = io.WriteString(w, "event: data\ndata: {\"line\":\"a\"}\n\n")
		_ = http.NewResponseController(w).Flush()
		<-printed
		_, _ = io.WriteString(w, "event: end\ndata: {\"reason\":\"done\"}\n\n")
	}))
	t.Cleanup(srv.Close)
	pr, pw := io.Pipe()
	root, err := NewRoot(Options{Registry: testRegistry(t), Stdin: strings.NewReader(""), Stdout: pw, Stderr: io.Discard,
		Getenv: envOf(tokenEnv), UserHomeDir: homeAt(t.TempDir()), IsTerminal: func() bool { return false }})
	if err != nil {
		t.Fatal(err)
	}
	root.SetArgs([]string{"cfl", "logs", "--follow", "--url", srv.URL})
	done := make(chan error, 1)
	go func() { done <- root.Execute(); _ = pw.Close() }()
	if line, err := bufio.NewReader(pr).ReadString('\n'); err != nil || line != `{"line":"a"}`+"\n" {
		t.Fatalf("first line %q, %v", line, err)
	}
	close(printed)
	if err := <-done; err != nil {
		t.Fatalf("follow: %v", err)
	}
}

// TestPrintEventsEscapesTerminalControls: a C1 control or a bidirectional
// override in an event reaches the terminal escaped, in JSON that means the
// same; other non-ASCII text is printed as it is.
func TestPrintEventsEscapesTerminalControls(t *testing.T) {
	csi, rlo, eAcute := string(rune(0x9b)), string(rune(0x202e)), string(rune(0xe9))
	event := `{"line":"a` + csi + `31mb` + rlo + `c` + eAcute + `"}`
	in := "event: data\ndata: " + event + "\n\nevent: end\ndata: {\"reason\":\"done\"}\n\n"
	var out strings.Builder
	if err := printEvents(strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	if want := `{"line":"a\` + `u009b31mb\` + `u202ec` + eAcute + `"}` + "\n"; out.String() != want {
		t.Fatalf("printed %q, want %q", out.String(), want)
	}
	var got, orig map[string]string
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(event), &orig); err != nil {
		t.Fatal(err)
	}
	if got["line"] != orig["line"] {
		t.Errorf("the escaped line decodes to %q, want %q", got["line"], orig["line"])
	}
}

func TestPrintEventsRefusals(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"data not JSON", "event: data\ndata: {\"line\":\n\n", "the stream sent an event that is not JSON"},
		{"end not JSON", "event: end\ndata: done\n\n", "the stream sent an end event that is not JSON"},
		{"line too long", "event: data\ndata: \"" + strings.Repeat("x", maxEventLine) + "\"\n\n", "read the stream: bufio.Scanner: token too long"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out strings.Builder
			if err := printEvents(strings.NewReader(tc.in), &out); err == nil || err.Error() != tc.want || out.Len() != 0 {
				t.Errorf("printEvents = %v, printed %q; want %q and nothing printed", err, out.String(), tc.want)
			}
		})
	}
}

func TestQueryOf(t *testing.T) {
	q, err := queryOf(json.RawMessage(`{"follow":true,"tail":10,"names":["x","y"],"since":"1h"}`))
	if err != nil {
		t.Fatal(err)
	}
	if want := "follow=true&names=x&names=y&since=1h&tail=10"; q.Encode() != want {
		t.Errorf("query %s, want %s", q.Encode(), want)
	}
	for _, in := range []string{`{"filter":{"a":1}}`, `{"names":[["x"]]}`, `{"tail":null}`} {
		_, err := queryOf(json.RawMessage(in))
		var u *usageError
		if !errors.As(err, &u) || !strings.Contains(err.Error(), "must be a string, number, boolean or a list of them") {
			t.Errorf("queryOf(%s) = %v, want a usage error", in, err)
		}
	}
}

// cflFollowParams has a parameter that would take the CLI's --follow.
type cflFollowParams struct {
	Follow bool `json:"follow,omitempty" jsonschema_description:"Follow."`
}

func TestStreamCommandRefusesFollowParameter(t *testing.T) {
	ms := core.ModuleStream{Module: "cfl", Def: core.DefineStream("tail", func(context.Context, core.Env, cflFollowParams, func(cflLogLine) error) error {
		return nil
	})}
	_, err := (&app{}).streamCommand(entry{Name: "cfl.tail", Path: []string{"cfl", "tail"}, Stream: &ms})
	if err == nil || !strings.Contains(err.Error(), "would take the CLI's own flag --follow") {
		t.Errorf("streamCommand = %v, want the --follow collision refused", err)
	}
}

func TestFollowRequired(t *testing.T) {
	_, stderr, code := cliEnv{env: tokenEnv}.run(t, "cfl", "logs", "--host", "h1", "--url", "http://127.0.0.1:1")
	if code != ExitUsage || !strings.Contains(stderr, "run it with --follow") {
		t.Errorf("without --follow: exit %d, stderr %q; want a usage error", code, stderr)
	}
	if _, _, code := (cliEnv{env: tokenEnv}).run(t, "cfl", "logs", "--follow", "--local"); code != ExitUsage {
		t.Errorf("--local on a stream: exit %d, want a usage error", code)
	}
}

func TestStreamCommandNames(t *testing.T) {
	es, err := entries(testRegistry(t), handWritten, nil)
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(es, func(e entry) bool { return e.Name == "cfl.logs" })
	if i < 0 || !slices.Equal(es[i].Path, []string{"cfl", "logs"}) || es[i].Stream == nil || es[i].Action != nil {
		t.Fatalf("cfl.logs entry %+v", es)
	}
	if got := StreamPath(core.ModuleIDCore, "events"); !slices.Equal(got, []string{"events"}) {
		t.Errorf("StreamPath of the core = %q", got)
	}
	root, err := NewRoot(Options{Registry: testRegistry(t)})
	if err != nil {
		t.Fatal(err)
	}
	cmd, rest, err := root.Find([]string{"cfl", "logs"})
	if err != nil || len(rest) > 0 || cmd.Flags().Lookup("follow") == nil || cmd.Flags().Lookup("tail") == nil ||
		cmd.Flags().Lookup("names") == nil || cmd.Flags().Lookup("host") == nil || cmd.Annotations[annotationLocal] != "" {
		t.Errorf("darkpanel cfl logs = %v, %v; want the stream command with its flags and no --local", cmd, err)
	}
	if _, ok := FindCommand(root, "cfl.item.get"); !ok {
		t.Error("the module's action lost its command")
	}
}
