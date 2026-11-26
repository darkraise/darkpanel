package services

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform"
)

const (
	journalPageEntries  = 1000
	journalPageBytes    = 2 << 20 // of encoded entries
	journalMessageBytes = 8192
	journalQueryTime    = 10 * time.Second
	journalProbeTime    = 5 * time.Second
	journalFollowers    = 8
	followLinesDefault  = 100
	followLinesMax      = 500
	substringMax        = 256
)

// JournalQuery filters services.journal.query. A page runs from the newest
// matching entry back; cursor continues from the last entry of the page
// before.
type JournalQuery struct {
	Unit      string `json:"unit,omitempty"`
	Priority  *int   `json:"priority,omitempty" jsonschema:"minimum=0,maximum=7"` // this priority and more urgent
	Since     int64  `json:"since,omitempty" jsonschema:"minimum=0"`              // seconds since the epoch
	Until     int64  `json:"until,omitempty" jsonschema:"minimum=0"`              // seconds since the epoch
	Substring string `json:"substring,omitempty" jsonschema:"maxLength=256"`      // in the message, any case
	Cursor    string `json:"cursor,omitempty" jsonschema:"maxLength=512"`
}

// JournalFollow filters the services.journal-follow stream, which starts
// with the last lines entries before following.
type JournalFollow struct {
	Unit      string `json:"unit,omitempty"`
	Priority  *int   `json:"priority,omitempty" jsonschema:"minimum=0,maximum=7"`
	Since     int64  `json:"since,omitempty" jsonschema:"minimum=0"`
	Substring string `json:"substring,omitempty" jsonschema:"maxLength=256"`
	Lines     int    `json:"lines,omitempty" jsonschema:"minimum=0,maximum=500"` // 0 means 100
}

// Entry is one journal entry.
type Entry struct {
	Time      int64  `json:"time"` // microseconds since the epoch
	Priority  *int   `json:"priority" jsonschema:"nullable"`
	Unit      string `json:"unit,omitempty"`
	PID       int    `json:"pid,omitempty"`
	Message   string `json:"message"` // at most 8 KiB
	Truncated bool   `json:"truncated,omitempty"`
}

// JournalPage is the answer of services.journal.query.
type JournalPage struct {
	Entries []Entry `json:"entries"`
	More    bool    `json:"more"`
	Cursor  string  `json:"cursor,omitempty"` // set when more is
}

// journalState is what the journal reads keep for the agent's life: the
// pattern probe, run on first use, and the open followers.
type journalState struct {
	probe     sync.Once
	grep      bool
	mu        sync.Mutex
	followers int
}

func (m module) journalReads() []core.ActionDef {
	return []core.ActionDef{core.DefineRead[JournalQuery, JournalPage](core.ActionSpec{
		Name: ID + ".journal.query", Version: 1, Class: core.ClassRead, Side: core.SideAgent,
		Summary: "Read a page of journal entries by unit, priority, time range and message text.",
	}, queryHandler{m: m})}
}

// Streams lists the module's streams: services.journal-follow follows the
// journal. It is not services.journal, whose CLI command would clash with
// services.journal.query's.
func (m module) Streams() []core.StreamDef {
	return []core.StreamDef{core.DefineStream("journal-follow", m.follow)}
}

// filter is a checked query or follow.
type filter struct {
	args      platform.JournalArgs
	substring string // matched in the agent; "" when journalctl matches
}

// checkFilter validates the filters both reads take and builds their
// journalctl arguments.
func (m module) checkFilter(ctx context.Context, env core.Env, unit string, priority *int, since int64, substring string) (filter, error) {
	f := filter{args: platform.JournalArgs{Priority: -1, Since: since}}
	if unit != "" {
		if err := checkUnitName(unit); err != nil {
			return filter{}, err
		}
		f.args.Unit = unit
	}
	if priority != nil {
		if *priority < 0 || *priority > 7 {
			return filter{}, invalid("/priority", "priority is 0 (emergency) to 7 (debug)")
		}
		f.args.Priority = *priority
	}
	if since < 0 {
		return filter{}, invalid("/since", "since is seconds since the epoch")
	}
	if substring != "" {
		if len(substring) > substringMax || !utf8.ValidString(substring) || strings.ContainsFunc(substring, isControl) {
			return filter{}, invalid("/substring", fmt.Sprintf("substring is at most %d characters of text without control characters", substringMax))
		}
		if m.grepSupported(ctx, env) {
			f.args.Grep = literalPattern(substring)
		} else {
			f.substring = strings.ToLower(substring)
		}
	}
	return f, nil
}

func isControl(r rune) bool { return r < 0x20 || r == 0x7f }

// literalPattern quotes s for PCRE2 as \Q<s>\E. An \E inside s would end
// the quote, so each one becomes \E (end), \\E (a backslash and an E) and
// \Q (quote again).
func literalPattern(s string) string {
	return `\Q` + strings.ReplaceAll(s, `\E`, `\E\\E\Q`) + `\E`
}

// grepSupported runs the pattern probe once and reports its answer.
func (m module) grepSupported(ctx context.Context, env core.Env) bool {
	m.journal.probe.Do(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), journalProbeTime)
		defer cancel()
		res, err := env.Exec.Run(ctx, core.Cmd{Path: platform.JournalctlPath, Args: platform.JournalctlProbe()})
		// The probe matches nothing, and journalctl exits 1 for that too:
		// only an error on stderr says it cannot match patterns.
		m.journal.grep = err == nil && (res.ExitCode == 0 || res.ExitCode == 1) && len(bytes.TrimSpace(res.Stderr)) == 0
	})
	return m.journal.grep
}

// rawEntry is one line of journalctl's JSON output. A field is a string,
// an array of byte values when it is not text, or an array of either when
// the entry holds it more than once.
type rawEntry struct {
	Cursor   string          `json:"__CURSOR"`
	Realtime string          `json:"__REALTIME_TIMESTAMP"`
	Priority json.RawMessage `json:"PRIORITY"`
	Unit     json.RawMessage `json:"_SYSTEMD_UNIT"`
	PID      json.RawMessage `json:"_PID"`
	Message  json.RawMessage `json:"MESSAGE"`
}

// parseEntry decodes a line into its entry, the full message and its
// cursor. A line that is not an entry, such as one cut at the stream's
// 64 KiB, reports false.
func parseEntry(line []byte) (Entry, string, string, bool) {
	var r rawEntry
	if err := json.Unmarshal(line, &r); err != nil || r.Cursor == "" {
		return Entry{}, "", "", false
	}
	t, _ := strconv.ParseInt(r.Realtime, 10, 64)
	e := Entry{Time: t, Unit: fieldText(r.Unit)}
	if p, err := strconv.Atoi(fieldText(r.Priority)); err == nil {
		e.Priority = &p
	}
	e.PID, _ = strconv.Atoi(fieldText(r.PID))
	msg := fieldText(r.Message)
	e.Message, e.Truncated = truncate(msg, journalMessageBytes)
	return e, msg, r.Cursor, true
}

// fieldText is a field's value as text: the first value of a repeated
// field, and invalid UTF-8 replaced.
func fieldText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.ToValidUTF8(s, "�")
	}
	var b []uint8
	if json.Unmarshal(raw, &b) == nil {
		return strings.ToValidUTF8(string(b), "�")
	}
	var many []json.RawMessage
	if json.Unmarshal(raw, &many) == nil && len(many) > 0 {
		return fieldText(many[0])
	}
	return ""
}

// truncate cuts s to at most n bytes at a character boundary.
func truncate(s string, n int) (string, bool) {
	if len(s) <= n {
		return s, false
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n], true
}

// matches reports whether msg holds the lowercased substring, as
// journalctl's caseless --grep does.
func (f filter) matches(msg string) bool {
	return f.substring == "" || strings.Contains(strings.ToLower(msg), f.substring)
}

var (
	errPageFull    = errors.New("services: the page is full")
	errQueryTimout = errors.New("services: the journal query ran out of time")
)

type queryHandler struct{ m module }

// Read reads one page: at most 1000 entries and 2 MiB of encoded entries,
// newest first, within 10 seconds. A page cut short by either limit or by
// the time says more, with the cursor of the last entry read.
func (h queryHandler) Read(ctx context.Context, env core.Env, in JournalQuery) (JournalPage, error) {
	f, err := h.m.checkFilter(ctx, env, in.Unit, in.Priority, in.Since, in.Substring)
	if err != nil {
		return JournalPage{}, err
	}
	if in.Until < 0 || (in.Until > 0 && in.Until < in.Since) {
		return JournalPage{}, invalid("/until", "until is seconds since the epoch, not before since")
	}
	if in.Cursor != "" && !platform.JournalCursorPattern.MatchString(in.Cursor) {
		return JournalPage{}, invalid("/cursor", "cursor is not a journal cursor")
	}
	f.args.Until, f.args.AfterCursor, f.args.Reverse = in.Until, in.Cursor, true
	if f.substring == "" {
		// journalctl counts only what it prints; one more entry than a page
		// says whether there is more.
		f.args.Lines = journalPageEntries + 1
	}

	ctx, cancel := context.WithTimeoutCause(ctx, journalQueryTime, errQueryTimout)
	defer cancel()
	page := JournalPage{Entries: []Entry{}}
	size, last := 0, ""
	err = env.Exec.Stream(ctx, platform.JournalctlPath, platform.JournalctlRead(f.args), func(line []byte) error {
		e, msg, cursor, ok := parseEntry(line)
		if !ok {
			return nil
		}
		if !f.matches(msg) {
			last = cursor
			return nil
		}
		enc, err := json.Marshal(e)
		if err != nil {
			return err
		}
		if len(page.Entries) == journalPageEntries || size+len(enc)+1 > journalPageBytes {
			return errPageFull
		}
		page.Entries = append(page.Entries, e)
		size += len(enc) + 1
		last = cursor
		return nil
	})
	var exit *platform.StreamExit
	switch {
	case f.args.Grep != "" && len(page.Entries) == 0 && errors.As(err, &exit) && exit.Code == 1 && exit.Stderr == "":
		// journalctl --grep exits 1, saying nothing, when nothing matched.
	case errors.Is(err, errPageFull):
		page.More, page.Cursor = true, last
	case err != nil && errors.Is(context.Cause(ctx), errQueryTimout) && last != "":
		page.More, page.Cursor = true, last
	case err != nil && errors.Is(context.Cause(ctx), errQueryTimout):
		return JournalPage{}, errcode.New(errcode.Internal, "the journal query found nothing within 10 seconds; narrow the filters")
	case err != nil:
		return JournalPage{}, fmt.Errorf("services: read the journal: %w", err)
	}
	return page, nil
}

// follow emits the last p.Lines entries, then each new one, until the
// reader goes away; journalctl ends with ctx. A host serves at most eight
// followers.
func (m module) follow(ctx context.Context, env core.Env, p JournalFollow, emit func(Entry) error) error {
	f, err := m.checkFilter(ctx, env, p.Unit, p.Priority, p.Since, p.Substring)
	if err != nil {
		return err
	}
	switch {
	case p.Lines < 0 || p.Lines > followLinesMax:
		return invalid("/lines", fmt.Sprintf("lines is 0 to %d", followLinesMax))
	case p.Lines == 0:
		f.args.Lines = followLinesDefault
	default:
		f.args.Lines = p.Lines
	}
	f.args.Follow = true
	release, err := m.journal.takeFollower()
	if err != nil {
		return err
	}
	defer release()
	err = env.Exec.Stream(ctx, platform.JournalctlPath, platform.JournalctlRead(f.args), func(line []byte) error {
		e, msg, _, ok := parseEntry(line)
		if !ok || !f.matches(msg) {
			return nil
		}
		return emit(e)
	})
	if ctx.Err() != nil {
		return nil
	}
	return err
}

// takeFollower counts one more follower, or answers core.stream_limit at
// eight.
func (s *journalState) takeFollower() (func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.followers >= journalFollowers {
		return nil, errcode.New(errcode.StreamLimit, fmt.Sprintf("this host follows its journal %d times, the most allowed; close one first", journalFollowers))
	}
	s.followers++
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			s.followers--
			s.mu.Unlock()
		})
	}, nil
}
