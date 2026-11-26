package console

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/darkraise/darkpanel/internal/agent/wire"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// The terminal's limits (M11 spec section 5): the controller enforces the
// per-user cap, the agent the per-host cap and the idle limit.
const (
	MaxHostTerminals = 4
	IdleLimit        = 30 * time.Minute
)

// The close reasons the terminal adds to the channel's.
const (
	CloseIdle         = "idle"
	CloseLimit        = string(errcode.ConsoleLimit)
	CloseEnded        = "ended"        // the shell exited
	CloseDisconnected = "disconnected" // the stream ended without a close frame
	CloseRestarted    = "agent restarted"
)

// The journal actions of a terminal.
const (
	ActionTerminalOpen  = "console.terminal.open"
	ActionTerminalClose = "console.terminal.close"
)

// maxReason bounds the peer's close reason the journal keeps.
const maxReason = 64

// exitDrain is how long the output of a shell that exited may keep
// arriving before the terminal closes.
const exitDrain = time.Second

// TerminalParams are the parameters of channel console.terminal: the size
// of the browser's terminal.
type TerminalParams struct {
	Cols uint16 `json:"cols" jsonschema:"minimum=20,maximum=500" jsonschema_description:"Columns of the terminal."`
	Rows uint16 `json:"rows" jsonschema:"minimum=5,maximum=200" jsonschema_description:"Rows of the terminal."`
}

// terminalRecord is the Input of a terminal's journal entries. A close
// recovered at start has no duration or byte counts: its end is unknown.
type terminalRecord struct {
	Source     string `json:"source"`
	Reason     string `json:"reason,omitempty"`
	DurationMs *int64 `json:"durationMs,omitempty"`
	BytesIn    *int64 `json:"bytesIn,omitempty"`
	BytesOut   *int64 `json:"bytesOut,omitempty"`
}

// Channels declares the terminal.
func (m *module) Channels() []core.ChannelDef {
	return []core.ChannelDef{core.DefineChannel("terminal", m.openTerminal)}
}

var (
	_ core.Channeler      = (*module)(nil)
	_ core.EnablementHook = (*module)(nil)
)

// terminals counts the host's open terminals by id.
type terminals struct {
	mu   sync.Mutex
	open map[string]bool
}

func newTerminals() *terminals { return &terminals{open: map[string]bool{}} }

// add takes a place for a new terminal; false when the host has
// MaxHostTerminals open.
func (s *terminals) add() (string, bool, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", false, err
	}
	id := hex.EncodeToString(b[:])
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.open) >= MaxHostTerminals {
		return "", false, nil
	}
	s.open[id] = true
	return id, true, nil
}

func (s *terminals) remove(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.open, id)
}

// openTerminal runs root's shell on c until the shell exits, the peer
// closes, the terminal is idle for IdleLimit or the agent closes the
// channel. No terminal byte is logged or stored.
func (m *module) openTerminal(ctx context.Context, env core.Env, p TerminalParams, c core.ChannelConn) error {
	if err := core.RequireScope(env, core.ScopeAdmin); err != nil {
		return err
	}
	id, ok, err := m.terms.add()
	if err != nil {
		return err
	}
	if !ok {
		_ = c.Close(CloseLimit)
		return nil
	}
	defer m.terms.remove(id)
	sess, err := env.Shell.Start(ctx, p.Cols, p.Rows)
	if err != nil {
		return err
	}
	opened := env.Clock.Now()
	if err := m.recordOpen(ctx, env, id, opened); err != nil {
		_ = sess.Close()
		return err
	}
	t := &terminal{env: env, c: c, sess: sess, lastInput: opened, stop: make(chan struct{})}
	reason := t.run(ctx)
	_ = c.Close(reason)
	rec := terminalRecord{Source: env.Principal.Source, Reason: reason}
	ms := env.Clock.Now().Sub(opened).Milliseconds()
	in, out := t.bytesIn, t.bytesOut
	rec.DurationMs, rec.BytesIn, rec.BytesOut = &ms, &in, &out
	// The close is journaled whatever ended the channel's context.
	return m.recordClose(context.WithoutCancel(ctx), env, id, env.Principal.Actor, rec)
}

// recordOpen keeps id among the open terminals, then journals the open.
func (m *module) recordOpen(ctx context.Context, env core.Env, id string, at time.Time) error {
	err := env.DB.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "INSERT INTO console_terminals (id, actor, source, opened_at) VALUES (?, ?, ?, ?)",
			id, env.Principal.Actor, env.Principal.Source, at.UTC().Format(time.RFC3339Nano))
		return err
	})
	if err != nil {
		return fmt.Errorf("console: keep the terminal: %w", err)
	}
	err = record(ctx, env, ActionTerminalOpen, "open", env.Principal.Actor, at, terminalRecord{Source: env.Principal.Source})
	if err != nil {
		_ = forget(context.WithoutCancel(ctx), env, id)
	}
	return err
}

// recordClose journals a close, then forgets id: a crash between the two
// journals a second close at the next start rather than none.
func (m *module) recordClose(ctx context.Context, env core.Env, id, actor string, rec terminalRecord) error {
	if err := record(ctx, env, ActionTerminalClose, "close", actor, env.Clock.Now(), rec); err != nil {
		return err
	}
	return forget(ctx, env, id)
}

func forget(ctx context.Context, env core.Env, id string) error {
	return env.DB.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "DELETE FROM console_terminals WHERE id = ?", id)
		return err
	})
}

func record(ctx context.Context, env core.Env, action, phase, actor string, at time.Time, rec terminalRecord) error {
	input, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	err = env.Journal.Record(ctx, core.JournalEntry{
		At: at, Actor: actor, Action: action, Version: 1, Phase: phase, Input: input, Result: "ok", Source: rec.Source,
	})
	if err != nil {
		return fmt.Errorf("console: journal %s: %w", action, err)
	}
	return nil
}

// OnEnabled runs as the agent starts and on every flag change: it journals
// the close, with reason "agent restarted", of every terminal kept open
// that this process does not run, as one a crash left behind, and the
// result "interrupted" of every file operation a crash cut off.
func (m *module) OnEnabled(ctx context.Context, env core.Env, _ bool) error {
	return errors.Join(m.sweepTerminals(ctx, env), m.sweepFiles(ctx, env))
}

func (m *module) sweepTerminals(ctx context.Context, env core.Env) error {
	type left struct{ id, actor, source string }
	var orphans []left
	err := func() error {
		// The lock holds while the rows are read: a terminal leaves the
		// open set only after its row is gone, so a row read here whose
		// id is not open belongs to no running terminal.
		m.terms.mu.Lock()
		defer m.terms.mu.Unlock()
		rows, err := env.DB.Read().QueryContext(ctx, "SELECT id, actor, source FROM console_terminals ORDER BY opened_at")
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var l left
			if err := rows.Scan(&l.id, &l.actor, &l.source); err != nil {
				return err
			}
			if !m.terms.open[l.id] {
				orphans = append(orphans, l)
			}
		}
		return rows.Err()
	}()
	if err != nil {
		return fmt.Errorf("console: read the open terminals: %w", err)
	}
	var errs []error
	for _, l := range orphans {
		errs = append(errs, m.recordClose(ctx, env, l.id, l.actor, terminalRecord{Source: l.source, Reason: CloseRestarted}))
	}
	return errors.Join(errs...)
}

// terminal is one open terminal: frames from the peer go to the shell,
// the shell's output comes back as data frames.
type terminal struct {
	env  core.Env
	c    core.ChannelConn
	sess core.ShellSession

	mu        sync.Mutex
	reason    string // why the terminal ends; the first one stands
	lastInput time.Time
	idle      core.Timer
	stop      chan struct{} // closed when reason is set

	bytesIn, bytesOut int64 // written only by their pump, read after both returned
}

// end sets the terminal's reason to end, unless it has one.
func (t *terminal) end(reason string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.endLocked(reason)
}

func (t *terminal) endLocked(reason string) {
	if t.reason != "" {
		return
	}
	t.reason = reason
	close(t.stop)
}

// run serves the terminal until it ends, closes the shell and returns the
// reason.
func (t *terminal) run(ctx context.Context) string {
	t.mu.Lock()
	t.idle = t.env.Clock.AfterFunc(IdleLimit, t.checkIdle)
	t.mu.Unlock()
	var wg sync.WaitGroup
	outDone := make(chan struct{})
	wg.Go(func() {
		defer close(outDone)
		t.pumpOutput(ctx)
	})
	wg.Go(func() { t.watchExit(outDone) })
	wg.Go(func() { t.pumpInput(ctx) })
	select {
	case <-t.stop:
	case <-ctx.Done():
		t.end(closeReason(ctx, CloseDisconnected))
	}
	t.mu.Lock()
	t.idle.Stop()
	reason := t.reason
	t.mu.Unlock()
	_ = t.sess.Close()
	_ = t.c.Close(reason)
	wg.Wait()
	return reason
}

// closeReason is the reason the agent closed the channel with, else
// fallback.
func closeReason(ctx context.Context, fallback string) string {
	var cc *core.ChannelClosed
	if errors.As(context.Cause(ctx), &cc) && cc.Reason != "" {
		return cc.Reason
	}
	return fallback
}

// checkIdle ends the terminal once IdleLimit passed since the last data
// frame, else waits for the rest.
func (t *terminal) checkIdle() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.reason != "" {
		return
	}
	if left := IdleLimit - t.env.Clock.Now().Sub(t.lastInput); left > 0 {
		t.idle = t.env.Clock.AfterFunc(left, t.checkIdle)
		return
	}
	t.endLocked(CloseIdle)
}

func (t *terminal) pumpOutput(ctx context.Context) {
	buf := make([]byte, wire.MaxFramePayload)
	for {
		n, err := t.sess.Read(buf)
		if n > 0 {
			if werr := t.c.WriteFrame(wire.FrameData, buf[:n]); werr != nil {
				t.end(closeReason(ctx, CloseDisconnected))
				return
			}
			t.bytesOut += int64(n)
		}
		if err != nil {
			return
		}
	}
}

// watchExit sends the exit frame once the shell exited on its own and its
// output drained, then ends the terminal.
func (t *terminal) watchExit(outDone <-chan struct{}) {
	code, _ := t.sess.Wait()
	drain := time.NewTimer(exitDrain)
	defer drain.Stop()
	select {
	case <-outDone:
	case <-drain.C:
	}
	t.mu.Lock()
	ended := t.reason != ""
	t.mu.Unlock()
	if ended {
		return
	}
	payload, _ := json.Marshal(struct {
		Code int `json:"code"`
	}{code})
	_ = t.c.WriteFrame(wire.FrameExit, payload)
	t.end(CloseEnded)
}

func (t *terminal) pumpInput(ctx context.Context) {
	for {
		typ, p, err := t.c.ReadFrame()
		if err != nil {
			t.end(closeReason(ctx, CloseDisconnected))
			return
		}
		switch typ {
		case wire.FrameData:
			t.mu.Lock()
			t.lastInput = t.env.Clock.Now()
			t.mu.Unlock()
			t.bytesIn += int64(len(p))
			if _, err := t.sess.Write(p); err != nil {
				t.end(CloseEnded)
				return
			}
		case wire.FrameResize:
			var size struct {
				Cols uint16 `json:"cols"`
				Rows uint16 `json:"rows"`
			}
			// A size outside the parameters' bounds is ignored.
			if json.Unmarshal(p, &size) == nil && size.Cols >= 20 && size.Cols <= 500 && size.Rows >= 5 && size.Rows <= 200 {
				_ = t.sess.Resize(size.Cols, size.Rows)
			}
		case wire.FrameClose:
			var cl struct {
				Reason string `json:"reason"`
			}
			if json.Unmarshal(p, &cl) != nil || cl.Reason == "" || len(cl.Reason) > maxReason {
				cl.Reason = CloseDisconnected
			}
			t.end(cl.Reason)
			return
		}
	}
}
