package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/platform"
)

// fakeJournal is a core.Runner standing in for journalctl: Stream prints
// its entries, applying --grep as a caseless Go regexp (RE2 reads \Q...\E
// as PCRE2 does), and with --follow then waits for ctx.
type fakeJournal struct {
	grep    bool // the probe's answer
	entries []map[string]any

	mu      sync.Mutex
	probes  int
	streams [][]string
	started chan struct{} // one value per follow that printed its entries
	ended   chan struct{} // one value per follow ended by its ctx
}

func (f *fakeJournal) Run(_ context.Context, cmd core.Cmd) (core.CmdResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if cmd.Path != platform.JournalctlPath || !slices.Equal(cmd.Args, platform.JournalctlProbe()) {
		return core.CmdResult{}, fmt.Errorf("unexpected run %s %v", cmd.Path, cmd.Args)
	}
	f.probes++
	if !f.grep {
		return core.CmdResult{ExitCode: 1, Stderr: []byte("Compiled without pattern matching support")}, nil
	}
	return core.CmdResult{ExitCode: 1}, nil // the probe matches nothing
}

func (f *fakeJournal) Stream(ctx context.Context, path string, args []string, onLine func([]byte) error) error {
	if path != platform.JournalctlPath {
		return fmt.Errorf("unexpected stream %s", path)
	}
	f.mu.Lock()
	f.streams = append(f.streams, args)
	f.mu.Unlock()
	var re *regexp.Regexp
	for _, a := range args {
		if p, ok := strings.CutPrefix(a, "--grep="); ok {
			re = regexp.MustCompile("(?i)" + p)
		}
	}
	shown := 0
	for _, e := range f.entries {
		if re != nil && !re.MatchString(e["MESSAGE"].(string)) {
			continue
		}
		shown++
		line, err := json.Marshal(e)
		if err != nil {
			return err
		}
		if err := onLine(line); err != nil {
			return err
		}
	}
	if slices.Contains(args, "--follow") {
		f.started <- struct{}{}
		<-ctx.Done()
		f.ended <- struct{}{}
		return fmt.Errorf("platform: %s: %w", path, ctx.Err())
	}
	if re != nil && shown == 0 {
		return &platform.StreamExit{Path: path, Code: 1}
	}
	return nil
}

func (f *fakeJournal) lastStream() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.streams[len(f.streams)-1]
}

// entry is a journalctl JSON line with message msg; i makes its time and
// cursor unique.
func entry(i int, msg string) map[string]any {
	return map[string]any{
		"__CURSOR":              fmt.Sprintf("s=0a1b;i=%x;b=2c3d;m=%x;t=%x;x=4e5f", i, i, i),
		"__REALTIME_TIMESTAMP":  fmt.Sprint(1764000000000000 + int64(i)),
		"__MONOTONIC_TIMESTAMP": "12345",
		"_BOOT_ID":              "0123456789abcdef",
		"PRIORITY":              "6",
		"_SYSTEMD_UNIT":         "nginx.service",
		"_PID":                  "4242",
		"MESSAGE":               msg,
	}
}

func journalRig(f *fakeJournal) (module, core.Env) {
	return module{journal: &journalState{}}, core.Env{Exec: f}
}

func query(t *testing.T, m module, env core.Env, in JournalQuery) JournalPage {
	t.Helper()
	page, err := queryHandler{m: m}.Read(context.Background(), env, in)
	if err != nil {
		t.Fatalf("query %+v: %v", in, err)
	}
	return page
}

func TestJournalQueryPage(t *testing.T) {
	t.Run("a page holds 1000 entries", func(t *testing.T) {
		f := &fakeJournal{}
		for i := range 1500 {
			f.entries = append(f.entries, entry(i, fmt.Sprintf("line %d", i)))
		}
		m, env := journalRig(f)
		page := query(t, m, env, JournalQuery{Unit: "nginx.service"})
		if len(page.Entries) != 1000 || !page.More || page.Cursor != f.entries[999]["__CURSOR"] {
			t.Fatalf("page: %d entries, more %v, cursor %q; want 1000, true, the 1000th entry's", len(page.Entries), page.More, page.Cursor)
		}
		e := page.Entries[0]
		if e.Time != 1764000000000000 || e.Priority == nil || *e.Priority != 6 || e.Unit != "nginx.service" || e.PID != 4242 || e.Message != "line 0" || e.Truncated {
			t.Errorf("first entry = %+v", e)
		}
		args := f.lastStream()
		for _, want := range []string{"--unit=nginx.service", "--reverse", "--lines=1001", "--output=json", "--no-pager"} {
			if !slices.Contains(args, want) {
				t.Errorf("journalctl %v lacks %s", args, want)
			}
		}

		next := query(t, m, env, JournalQuery{Cursor: page.Cursor, Priority: new(3), Since: 1764000000, Until: 1764003600})
		args = f.lastStream()
		for _, want := range []string{"--after-cursor=" + page.Cursor, "--priority=3", "--since=@1764000000", "--until=@1764003600"} {
			if !slices.Contains(args, want) {
				t.Errorf("journalctl %v lacks %s", args, want)
			}
		}
		if len(next.Entries) != 1000 {
			t.Errorf("next page has %d entries", len(next.Entries))
		}
	})
	t.Run("a page ends before 2 MiB", func(t *testing.T) {
		f := &fakeJournal{}
		msg := strings.Repeat("x", 4000)
		for i := range 3 << 20 / 4000 {
			f.entries = append(f.entries, entry(i, msg))
		}
		m, env := journalRig(f)
		page := query(t, m, env, JournalQuery{})
		size := 0
		for _, e := range page.Entries {
			b, _ := json.Marshal(e)
			size += len(b) + 1
		}
		if !page.More || page.Cursor == "" || size > 2<<20 || size < 2<<20-5000 || len(page.Entries) >= 1000 {
			t.Errorf("page: %d entries of %d bytes, more %v, cursor %q; want it to end just before 2 MiB with more", len(page.Entries), size, page.More, page.Cursor)
		}
	})
	t.Run("the last page says no more", func(t *testing.T) {
		f := &fakeJournal{entries: []map[string]any{entry(1, "one"), entry(2, "two")}}
		m, env := journalRig(f)
		page := query(t, m, env, JournalQuery{})
		if len(page.Entries) != 2 || page.More || page.Cursor != "" {
			t.Errorf("page = %+v", page)
		}
	})
	t.Run("filters are checked", func(t *testing.T) {
		m, env := journalRig(&fakeJournal{})
		for _, in := range []JournalQuery{
			{Unit: "nginx"}, {Unit: "--file=/x.service"}, {Priority: new(8)}, {Since: -1},
			{Since: 20, Until: 10}, {Cursor: "s=1;--file"}, {Substring: "a\nb"}, {Substring: strings.Repeat("a", 257)},
		} {
			_, err := queryHandler{m: m}.Read(context.Background(), env, in)
			if codeOf(err) != errcode.InvalidInput {
				t.Errorf("query %+v: err %v, want invalid_input", in, err)
			}
		}
	})
}

func TestJournalTruncates(t *testing.T) {
	ten := strings.Repeat("y", 10<<10)
	wide := strings.Repeat("é", 5000) // 2 bytes each
	f := &fakeJournal{entries: []map[string]any{entry(1, ten), entry(2, wide), entry(3, "short")}}
	m, env := journalRig(f)
	page := query(t, m, env, JournalQuery{})
	if e := page.Entries[0]; len(e.Message) != 8192 || !e.Truncated || e.Message != ten[:8192] {
		t.Errorf("10 KiB message: %d bytes, truncated %v; want 8192, true", len(e.Message), e.Truncated)
	}
	if e := page.Entries[1]; len(e.Message) != 8192 || !e.Truncated || !strings.HasSuffix(e.Message, "é") {
		t.Errorf("wide message: %d bytes, truncated %v; want 8192 of whole characters", len(e.Message), e.Truncated)
	}
	if e := page.Entries[2]; e.Message != "short" || e.Truncated {
		t.Errorf("short message = %+v", e)
	}

	// A message that is not text arrives as byte values; a repeated field
	// as an array.
	raw := entry(4, "")
	raw["MESSAGE"] = []int{104, 105, 0xff}
	raw["_SYSTEMD_UNIT"] = []string{"cron.service", "other.service"}
	line, _ := json.Marshal(raw)
	e, _, _, ok := parseEntry(line)
	if !ok || e.Message != "hi�" || e.Unit != "cron.service" {
		t.Errorf("parseEntry(%s) = %+v, %v", line, e, ok)
	}
	if _, _, _, ok := parseEntry([]byte(`{"MESSAGE":"cut at 64 KiB`)); ok {
		t.Error("a cut line parsed")
	}
}

func TestJournalSubstring(t *testing.T) {
	const sub = `a(b[c.d\E`
	fixture := []map[string]any{
		entry(1, `x A(B[C.D\e y`), // the substring in another case
		entry(2, `a(b[c.d`),       // no \E
		entry(3, `aXb[c.d\E`),     // "." is no wildcard and "(" no group
		entry(4, `a(b[c.d\E`),
		entry(5, `a(b[cxd\E`),
	}
	want := []string{`x A(B[C.D\e y`, `a(b[c.d\E`}
	for _, tc := range []struct {
		name string
		grep bool
	}{{"journalctl matches", true}, {"the agent matches", false}} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeJournal{grep: tc.grep, entries: fixture}
			m, env := journalRig(f)
			page := query(t, m, env, JournalQuery{Substring: sub})
			var got []string
			for _, e := range page.Entries {
				got = append(got, e.Message)
			}
			if !slices.Equal(got, want) {
				t.Errorf("messages = %q, want %q", got, want)
			}
			args := f.lastStream()
			hasGrep := slices.Contains(args, `--grep=\Qa(b[c.d\E\\E\Q\E`) && slices.Contains(args, "--case-sensitive=no")
			if hasGrep != tc.grep {
				t.Errorf("journalctl %v: --grep given %v, want %v", args, hasGrep, tc.grep)
			}
			if !tc.grep && slices.ContainsFunc(args, func(a string) bool { return strings.HasPrefix(a, "--lines=") }) {
				t.Errorf("journalctl %v limits lines while the agent filters", args)
			}
			if page := query(t, m, env, JournalQuery{Substring: "nowhere"}); len(page.Entries) != 0 || page.More {
				t.Errorf("a substring found nowhere: %+v", page)
			}
			if f.probes != 1 {
				t.Errorf("probed %d times, want once", f.probes)
			}
		})
	}
	if got := literalPattern(`\E\E`); got != `\Q\E\\E\Q\E\\E\Q\E` {
		t.Errorf("literalPattern = %s", got)
	}
}

func TestJournalFollowLimit(t *testing.T) {
	f := &fakeJournal{entries: []map[string]any{entry(1, "booted")}, started: make(chan struct{}, 16), ended: make(chan struct{}, 16)}
	m, env := journalRig(f)
	run := m.Streams()[0].Run
	follow := func(ctx context.Context, params string, events chan<- Entry) error {
		return run(ctx, env, json.RawMessage(params), func(e any) error {
			events <- e.(Entry)
			return nil
		})
	}

	var cancels []context.CancelFunc
	done := make(chan error, 16)
	events := make(chan Entry, 16)
	for range 8 {
		ctx, cancel := context.WithCancel(context.Background())
		cancels = append(cancels, cancel)
		go func() { done <- follow(ctx, `{"unit":"nginx.service"}`, events) }()
	}
	for range 8 {
		select {
		case <-f.started:
		case err := <-done:
			t.Fatalf("a follower ended: %v", err)
		case <-time.After(5 * time.Second):
			t.Fatal("eight followers did not start")
		}
	}
	if e := <-events; e.Message != "booted" {
		t.Errorf("event = %+v", e)
	}
	args := f.lastStream()
	if !slices.Contains(args, "--follow") || !slices.Contains(args, "--lines=100") || slices.Contains(args, "--reverse") {
		t.Errorf("follow ran journalctl %v", args)
	}

	ninth, stop := context.WithTimeout(context.Background(), 2*time.Second)
	err := follow(ninth, `{}`, events)
	stop()
	if codeOf(err) != errcode.StreamLimit {
		t.Fatalf("ninth follower: err %v, want %s", err, errcode.StreamLimit)
	}

	// The reader goes away: the runtime cancels the stream's ctx, which
	// ends its journalctl and frees the slot.
	cancels[0]()
	select {
	case <-f.ended:
	case <-time.After(5 * time.Second):
		t.Fatal("journalctl outlived its reader")
	}
	if err := <-done; err != nil {
		t.Errorf("a follower whose reader left returned %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancels = append(cancels, cancel)
	go func() { done <- follow(ctx, `{"lines":500}`, events) }()
	select {
	case <-f.started:
	case err := <-done:
		t.Fatalf("a follower after one left was refused: %v", err)
	}
	if !slices.Contains(f.lastStream(), "--lines=500") {
		t.Errorf("follow ran journalctl %v", f.lastStream())
	}
	for _, c := range cancels {
		c()
	}
	for range 8 {
		if err := <-done; err != nil {
			t.Errorf("a follower whose reader left returned %v", err)
		}
	}

	// An emit that fails, as it does once the reader is gone, ends the
	// follow with its error.
	gone := errors.New("reader gone")
	err = run(context.Background(), env, json.RawMessage(`{}`), func(any) error { return gone })
	if !errors.Is(err, gone) {
		t.Errorf("follow after a failed emit: %v", err)
	}
	if err := follow(context.Background(), `{"lines":501}`, events); codeOf(err) != errcode.InvalidInput {
		t.Errorf("lines 501: err %v, want invalid_input", err)
	}
	// The runtime decodes a stream's query parameters by its schema.
	raw, err := schema.ParamsJSON(m.Streams()[0].ParamsType, url.Values{"unit": {"cron.service"}, "priority": {"3"}, "since": {"1764000000"}, "substring": {"x"}, "lines": {"20"}})
	if err != nil || string(raw) != `{"lines":20,"priority":3,"since":1764000000,"substring":"x","unit":"cron.service"}` {
		t.Errorf("parameters decode to %s, %v", raw, err)
	}
}

func codeOf(err error) errcode.Code {
	if err == nil {
		return ""
	}
	return errcode.From(err).Code
}
