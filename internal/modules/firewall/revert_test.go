package firewall

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/modules/firewall/model"
)

// rvTestSetup returns a host on which change c1, after its snapshot, added
// efTestRule (effect 1) and set the outgoing default to DROP (effect 2),
// with the post-apply hashes recorded. The inverses and "ufw reload" are
// scripted.
func rvTestSetup(t *testing.T) (*fakeHost, snapshot) {
	t.Helper()
	ctx := context.Background()
	h := newFakeHost(t)
	env, d := h.env(), h.deps()
	raw, err := capture(ctx, env, d, &core.GuardContext{Complete: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := attach(ctx, env); err != nil {
		t.Fatal(err)
	}
	v4, v6 := efTestLines()
	add, err := addRuleOp(efTestRule)
	if err != nil {
		t.Fatal(err)
	}
	def, err := defaultOp("outgoing", "DROP")
	if err != nil {
		t.Fatal(err)
	}
	h.ufw[strings.Join(add.Args, " ")] = func(s *fhState) int {
		s.Rules4, s.Rules6 = append(s.Rules4, v4), append(s.Rules6, v6)
		return 0
	}
	h.ufw["default deny outgoing"] = func(s *fhState) int { s.Output = "DROP"; return 0 }
	h.ufw[strings.Join(model.DeleteArgs(efTestRule), " ")] = func(s *fhState) int {
		s.Rules4 = slices.DeleteFunc(s.Rules4, func(l string) bool { return l == v4 })
		s.Rules6 = slices.DeleteFunc(s.Rules6, func(l string) bool { return l == v6 })
		return 0
	}
	h.ufw["default allow outgoing"] = func(s *fhState) int { s.Output = "ACCEPT"; return 0 }
	h.ufw["reload"] = func(*fhState) int { return 0 }
	for _, op := range []ufwOp{add, def} {
		if err := runUfw(ctx, env, d, op); err != nil {
			t.Fatal(err)
		}
	}
	if err := recordPostHashes(ctx, env, d, string(raw)); err != nil {
		t.Fatal(err)
	}
	snap, err := getSnapshot(ctx, h.db, string(raw))
	if err != nil {
		t.Fatal(err)
	}
	h.calls = nil
	return h, snap
}

// rvTestManualEdit adds a manual rule outside the change's effects.
func rvTestManualEdit(h *fakeHost) {
	s := h.state()
	s.Rules6 = append(s.Rules6, fhRule53v6)
	h.setState(s)
}

// rvTestRemoveOwnRule removes the change's rule as an operator would.
func rvTestRemoveOwnRule(h *fakeHost) {
	v4, v6 := efTestLines()
	s := h.state()
	s.Rules4 = slices.DeleteFunc(s.Rules4, func(l string) bool { return l == v4 })
	s.Rules6 = slices.DeleteFunc(s.Rules6, func(l string) bool { return l == v6 })
	h.setState(s)
}

func rvTestNoPostHashes(t *testing.T, h *fakeHost) {
	t.Helper()
	if err := h.db.Write(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec("UPDATE firewall_snapshots SET post_hashes = NULL")
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

type rvTestEffect struct {
	seq     int
	state   core.EffectState
	reason  string
	current string // the Current State's JSON; "" for none
}

func TestRevert(t *testing.T) {
	undone := []rvTestEffect{{1, core.EffectUndone, "", ""}, {2, core.EffectUndone, "", ""}}
	for _, c := range []struct {
		name         string
		set          func(t *testing.T, h *fakeHost, snap snapshot)
		wantErr      string // a substring of the error; "" for none
		wantOverride bool
		wantEffects  []rvTestEffect
		wantRestored bool     // every state file is the snapshot's
		wantRuns     []string // the ufw vectors run, in order; nil to skip the check
		wantGuard    int
		wantPinned   bool
		wantManual   bool // the manual rule outside the change survives
	}{
		{
			name: "untouched: whole restore", set: func(*testing.T, *fakeHost, snapshot) {},
			wantEffects: undone, wantRestored: true, wantRuns: []string{"reload"},
		},
		{
			name:        "mid-apply crash: no post hashes, files as after the last effect",
			set:         func(t *testing.T, h *fakeHost, _ snapshot) { rvTestNoPostHashes(t, h) },
			wantEffects: undone, wantRestored: true, wantRuns: []string{"reload"},
		},
		{
			name: "no post hashes and no settled effect: nothing to undo",
			set: func(t *testing.T, h *fakeHost, snap snapshot) {
				rvTestNoPostHashes(t, h)
				for i := range h.journal.effects {
					h.journal.effects[i].State = core.EffectSkipped
				}
				for _, e := range snap.Entries {
					if err := h.Restore(context.Background(), ID, e); err != nil {
						t.Fatal(err)
					}
				}
				h.calls = nil
			},
			wantRestored: true, wantRuns: []string{},
		},
		{
			name:        "manual edit elsewhere: inverses newest first",
			set:         func(_ *testing.T, h *fakeHost, _ snapshot) { rvTestManualEdit(h) },
			wantEffects: undone, wantManual: true,
			wantRuns: []string{"default allow outgoing", strings.Join(model.DeleteArgs(efTestRule), " "), "reload"},
		},
		{
			name:        "conflict, guard passes: the rest undone, the conflict left",
			set:         func(_ *testing.T, h *fakeHost, _ snapshot) { rvTestRemoveOwnRule(h) },
			wantEffects: []rvTestEffect{{1, core.EffectUnresolved, core.ReasonConflict, "null"}, {2, core.EffectUndone, "", ""}},
			wantRuns:    []string{"default allow outgoing", "reload"}, wantGuard: 1, wantPinned: true,
			// The operator's removal of the change's rule and the undone
			// default leave the files as the snapshot holds them.
			wantRestored: true,
		},
		{
			name: "conflict, guard fails: emergency restore",
			set: func(_ *testing.T, h *fakeHost, _ snapshot) {
				rvTestRemoveOwnRule(h)
				h.guardOK, h.guardDetail = false, "ssh from 203.0.113.7 is dropped"
			},
			wantOverride: true, wantRestored: true, wantGuard: 1, wantPinned: true,
			wantEffects: []rvTestEffect{{1, core.EffectUnresolved, core.ReasonConflict, "null"}, {2, core.EffectUndone, "", ""}},
		},
		{
			name: "unresolved at start, guard passes: nothing written",
			set: func(_ *testing.T, h *fakeHost, _ snapshot) {
				h.journal.effects[1].State, h.journal.effects[1].Reason = core.EffectUnresolved, core.ReasonAmbiguous
			},
			wantErr: "unresolved", wantRuns: []string{}, wantGuard: 1, wantPinned: true,
		},
		{
			name: "unresolved at start, guard fails: emergency restore over it",
			set: func(_ *testing.T, h *fakeHost, _ snapshot) {
				h.journal.effects[1].State, h.journal.effects[1].Reason = core.EffectUnresolved, core.ReasonAmbiguous
				h.guardOK = false
			},
			wantOverride: true, wantRestored: true, wantGuard: 1, wantPinned: true,
			wantEffects: []rvTestEffect{{1, core.EffectUndone, "", ""}, {2, core.EffectUnresolved, core.ReasonAmbiguous, `"DROP"`}},
		},
		{
			name: "intended at start blocks like unresolved",
			set: func(_ *testing.T, h *fakeHost, _ snapshot) {
				h.journal.effects[1].State = core.EffectIntended
			},
			wantErr: "unresolved", wantRuns: []string{}, wantGuard: 1, wantPinned: true,
		},
		{
			name: "inverse fails, guard passes: stops at the first failure",
			set: func(_ *testing.T, h *fakeHost, _ snapshot) {
				rvTestManualEdit(h)
				h.ufw["default allow outgoing"] = func(*fhState) int { return 1 }
			},
			wantErr: "stopped", wantRuns: []string{"default allow outgoing"}, wantGuard: 1, wantPinned: true, wantManual: true,
		},
		{
			name: "inverse fails, guard fails: emergency restore",
			set: func(_ *testing.T, h *fakeHost, _ snapshot) {
				rvTestManualEdit(h)
				h.ufw["default allow outgoing"] = func(*fhState) int { return 1 }
				h.guardOK = false
			},
			wantOverride: true, wantRestored: true, wantGuard: 1, wantPinned: true, wantEffects: undone,
		},
		{
			name: "whole restore fails, guard passes",
			set: func(_ *testing.T, h *fakeHost, _ snapshot) {
				h.ufw["reload"] = func(*fhState) int { return 1 }
			},
			wantErr: "stopped", wantRuns: []string{"reload"}, wantGuard: 1, wantPinned: true, wantRestored: true,
		},
		{
			name: "whole restore fails, guard fails: the emergency restore fails too",
			set: func(_ *testing.T, h *fakeHost, _ snapshot) {
				h.ufw["reload"] = func(*fhState) int { return 1 }
				h.guardOK = false
			},
			wantErr: "emergency restore", wantRuns: []string{"reload", "reload"}, wantGuard: 1, wantPinned: true, wantRestored: true,
		},
		{
			name: "guard cannot be evaluated: treated as failing",
			set: func(_ *testing.T, h *fakeHost, _ snapshot) {
				rvTestRemoveOwnRule(h)
				h.guardErr = errors.New("routes unreadable")
			},
			wantOverride: true, wantRestored: true, wantGuard: 1, wantPinned: true,
			wantEffects: []rvTestEffect{{1, core.EffectUnresolved, core.ReasonConflict, "null"}, {2, core.EffectUndone, "", ""}},
		},
		{
			name: "an effect cannot be observed: left as uncertain",
			set: func(_ *testing.T, h *fakeHost, _ snapshot) {
				h.journal.effects[0].Resource = "firewall.thing:x"
			},
			wantEffects: []rvTestEffect{{1, core.EffectUnresolved, core.ReasonUncertain, ""}, {2, core.EffectUndone, "", ""}},
			wantRuns:    []string{"default allow outgoing", "reload"}, wantGuard: 1, wantPinned: true,
		},
		{
			name: "interrupted restore: finished whatever the checks say",
			set: func(t *testing.T, h *fakeHost, snap snapshot) {
				rvTestManualEdit(h)
				if err := beginRestore(context.Background(), h.db, snap.ID, fwstate.TransReload, h.clock.Now()); err != nil {
					t.Fatal(err)
				}
			},
			wantEffects: undone, wantRestored: true, wantRuns: []string{"reload"},
		},
		{
			name: "interrupted emergency restore: finished, still an override",
			set: func(t *testing.T, h *fakeHost, snap snapshot) {
				h.journal.effects[1].State, h.journal.effects[1].Reason = core.EffectUnresolved, core.ReasonAmbiguous
				if err := beginRestore(context.Background(), h.db, snap.ID, fwstate.TransReload, h.clock.Now()); err != nil {
					t.Fatal(err)
				}
			},
			wantOverride: true, wantRestored: true, wantRuns: []string{"reload"}, wantPinned: true,
			wantEffects: []rvTestEffect{{1, core.EffectUndone, "", ""}, {2, core.EffectUnresolved, core.ReasonAmbiguous, ""}},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			h, snap := rvTestSetup(t)
			c.set(t, h, snap)
			out, err := revert(ctx, h.env(), h.deps(), []byte(snap.ID))
			switch {
			case c.wantErr == "" && err != nil:
				t.Fatalf("revert: %v", err)
			case c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)):
				t.Fatalf("revert = %v, want an error containing %q", err, c.wantErr)
			}
			if out.Override != c.wantOverride {
				t.Errorf("override = %v, want %v", out.Override, c.wantOverride)
			}
			var got []rvTestEffect
			for _, o := range out.Effects {
				e := rvTestEffect{o.Seq, o.State, o.Reason, ""}
				if o.Current != nil {
					e.current = string(o.Current.State)
				}
				got = append(got, e)
			}
			if !reflect.DeepEqual(got, c.wantEffects) {
				t.Errorf("effects = %+v, want %+v", got, c.wantEffects)
			}
			restored := reflect.DeepEqual(fileHashes(h.config().Files), snap.Hashes)
			if restored != c.wantRestored {
				t.Errorf("files restored = %v, want %v", restored, c.wantRestored)
			}
			if c.wantRuns != nil {
				runs := []string{}
				for _, call := range rsTestRuns(h) {
					runs = append(runs, strings.TrimPrefix(call, "run /usr/sbin/ufw "))
				}
				if !reflect.DeepEqual(runs, c.wantRuns) {
					t.Errorf("ufw ran %q, want %q", runs, c.wantRuns)
				}
			}
			if h.guardCalls != c.wantGuard {
				t.Errorf("guard evaluated %d times, want %d", h.guardCalls, c.wantGuard)
			}
			s, err := getSnapshot(ctx, h.db, snap.ID)
			if err != nil {
				t.Fatal(err)
			}
			if s.Pinned != c.wantPinned {
				t.Errorf("pinned = %v, want %v", s.Pinned, c.wantPinned)
			}
			if manual := slices.Contains(h.state().Rules6, fhRule53v6); manual != c.wantManual {
				t.Errorf("manual rule kept = %v, want %v", manual, c.wantManual)
			}
			if c.wantOverride {
				var detail overrideDetail
				if err := json.Unmarshal(out.Detail, &detail); err != nil {
					t.Fatal(err)
				}
				if detail.Snapshot != snap.ID || detail.Guard == "" || !detail.At.Equal(h.clock.Now()) {
					t.Errorf("override detail = %+v", detail)
				}
			}
		})
	}
}

func TestRevertOverrideDetail(t *testing.T) {
	ctx := context.Background()
	h, snap := rvTestSetup(t)
	h.journal.effects[1].State, h.journal.effects[1].Reason = core.EffectUnresolved, core.ReasonAmbiguous
	h.guardOK, h.guardDetail = false, "ssh from 203.0.113.7 to 192.0.2.1:22 on eth0 is dropped"
	delete(h.sysctl, "net/ipv6/conf/eth0/accept_ra")
	// The restore's verify compares first and must agree; the record then
	// compares the loaded state before and after the restore.
	d, compared := h.deps(), 0
	d.compare = func(_, _ fwstate.Loaded, _ func(f fwstate.Family, table, chain string) bool) []fwstate.ChainDiff {
		if compared++; compared == 1 {
			return nil
		}
		return []fwstate.ChainDiff{{Family: fwstate.V4, Table: "filter", Chain: "ufw-user-output", OnlyA: []string{"a"}, OnlyB: []string{"b"}}}
	}
	out, err := revert(ctx, h.env(), d, []byte(snap.ID))
	if err != nil {
		t.Fatal(err)
	}
	var detail overrideDetail
	if err := json.Unmarshal(out.Detail, &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Guard != h.guardDetail {
		t.Errorf("guard = %q", detail.Guard)
	}
	var paths []string
	for _, f := range detail.Files {
		paths = append(paths, f.Path)
		if f.Now != snap.Hashes[f.Path] || f.Was == f.Now || f.Was == "" {
			t.Errorf("file %+v", f)
		}
	}
	if want := []string{"/etc/ufw/user.rules", "/etc/ufw/user6.rules", "/etc/default/ufw"}; !reflect.DeepEqual(paths, want) {
		t.Errorf("overwritten files = %v, want %v", paths, want)
	}
	if len(detail.Chains) != 1 || detail.Chains[0].Chain != "ufw-user-output" || detail.Chains[0].Removed[0] != "a" || detail.Chains[0].Added[0] != "b" {
		t.Errorf("chains = %+v", detail.Chains)
	}
	if len(detail.Effects) != 1 || detail.Effects[0].Seq != 2 || string(detail.Effects[0].Was) != `"DROP"` {
		t.Errorf("effects = %+v", detail.Effects)
	}
	if !reflect.DeepEqual(detail.Vanished, []string{"net/ipv6/conf/eth0/accept_ra"}) {
		t.Errorf("vanished = %v", detail.Vanished)
	}
}

func TestRevertRefusesAForeignSnapshot(t *testing.T) {
	ctx := context.Background()
	h, snap := rvTestSetup(t)
	if _, err := revert(ctx, h.env(), h.deps(), []byte("01J0000000000000000000NONE")); err == nil {
		t.Error("revert of a missing snapshot succeeded")
	}
	other := h.env()
	other.ChangeID = "c2"
	if _, err := revert(ctx, other, h.deps(), []byte(snap.ID)); err == nil {
		t.Error("change c2 reverted change c1's snapshot")
	}
	if len(h.calls) != 0 {
		t.Errorf("a refused revert ran %v", h.calls)
	}
}

func TestExpectedHashes(t *testing.T) {
	snap := snapshot{Hashes: map[string]string{"/etc/ufw/user.rules": "u0", "/etc/ufw/before.rules": "b0", "/etc/default/ufw": "d0"}}
	ufwPost := core.StateDoc{State: mustJSON("DROP"), Version: mustJSON(ufwVersion{Files: map[string]string{"/etc/ufw/user.rules": "u1", "/etc/default/ufw": "d1"}})}
	filePost := core.StateDoc{State: mustJSON("b1")}
	sysctlPost := core.StateDoc{State: mustJSON("1")}
	if got := expectedHashes(snap, []core.Effect{{Resource: sysctlResource("net/ipv4/ip_forward"), Post: &sysctlPost}}); got != nil {
		t.Errorf("hashes from a sysctl effect alone = %v, want none", got)
	}
	got := expectedHashes(snap, []core.Effect{
		{Resource: defaultResource("outgoing"), Post: &ufwPost},
		{Resource: fileResource("/etc/ufw/before.rules"), Post: &filePost},
		{Resource: sysctlResource("net/ipv4/ip_forward"), Post: &sysctlPost},
	})
	want := map[string]string{"/etc/ufw/user.rules": "u1", "/etc/ufw/before.rules": "b1", "/etc/default/ufw": "d1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("expected hashes = %v, want %v", got, want)
	}
	if snap.Hashes["/etc/ufw/user.rules"] != "u0" {
		t.Error("expectedHashes changed the snapshot's hashes")
	}
}

func TestUnpinChangeSnapshot(t *testing.T) {
	ctx := context.Background()
	h, snap := rvTestSetup(t)
	if err := setPinned(ctx, h.db, snap.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := unpinChangeSnapshot(ctx, h.env(), "c1"); err != nil {
		t.Fatal(err)
	}
	if s, _ := getSnapshot(ctx, h.db, snap.ID); s.Pinned {
		t.Error("the snapshot is still pinned")
	}
	if err := unpinChangeSnapshot(ctx, h.env(), "c9"); !errors.Is(err, errSnapshotNotFound) {
		t.Errorf("unpin of a change without a snapshot = %v", err)
	}
}

func TestAxRevertFinishesAnInterruptedEmergencyRestore(t *testing.T) {
	for _, c := range []struct {
		name        string
		set         func(h *fakeHost)
		wantEffects []rvTestEffect
	}{
		{
			name: "over a conflict",
			set:  rvTestRemoveOwnRule,
			wantEffects: []rvTestEffect{
				{1, core.EffectUnresolved, core.ReasonConflict, "null"},
				{2, core.EffectUndone, "", ""},
			},
		},
		{
			name:        "after a whole restore that failed",
			set:         func(*fakeHost) {},
			wantEffects: []rvTestEffect{{1, core.EffectUndone, "", ""}, {2, core.EffectUndone, "", ""}},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			h, snap := rvTestSetup(t)
			c.set(h)
			h.guardOK, h.guardDetail = false, "ssh from 203.0.113.7 to 192.0.2.1:22 on eth0 is dropped"
			h.runErr["/usr/sbin/ufw reload"] = errors.New("killed")
			if _, err := revert(ctx, h.env(), h.deps(), []byte(snap.ID)); err == nil || !strings.Contains(err.Error(), "emergency restore") {
				t.Fatalf("the cut-off revert = %v, want the emergency restore's failure", err)
			}
			if _, pending, _ := restoreIntent(ctx, h.db, snap.ID); !pending {
				t.Fatal("the cut-off emergency restore left no record")
			}
			delete(h.runErr, "/usr/sbin/ufw reload")
			h.guardOK, h.guardCalls = true, 0
			out, err := revert(ctx, h.env(), h.deps(), []byte(snap.ID))
			if err != nil {
				t.Fatal(err)
			}
			if !out.Override {
				t.Fatal("the finished emergency restore is not an override")
			}
			var got []rvTestEffect
			for _, o := range out.Effects {
				e := rvTestEffect{o.Seq, o.State, o.Reason, ""}
				if o.Current != nil {
					e.current = string(o.Current.State)
				}
				got = append(got, e)
			}
			if !reflect.DeepEqual(got, c.wantEffects) {
				t.Errorf("effects = %+v, want %+v", got, c.wantEffects)
			}
			var detail overrideDetail
			if err := json.Unmarshal(out.Detail, &detail); err != nil {
				t.Fatal(err)
			}
			if detail.Snapshot != snap.ID || detail.Guard != "ssh from 203.0.113.7 to 192.0.2.1:22 on eth0 is dropped" || !detail.At.Equal(h.clock.Now()) {
				t.Errorf("override detail = %+v", detail)
			}
			if h.guardCalls != 0 {
				t.Errorf("finishing the restore evaluated the guard %d times", h.guardCalls)
			}
			if !reflect.DeepEqual(fileHashes(h.config().Files), snap.Hashes) {
				t.Error("the state files are not the snapshot's")
			}
			if _, pending, _ := restoreIntent(ctx, h.db, snap.ID); pending {
				t.Error("the finished restore left its record")
			}
			if s, err := getSnapshot(ctx, h.db, snap.ID); err != nil || !s.Pinned {
				t.Errorf("snapshot pinned = %v, %v", s.Pinned, err)
			}
		})
	}
}

func TestAxRevertFinishesAnUnreadableEmergencyRecord(t *testing.T) {
	for _, c := range []struct {
		name   string
		record json.RawMessage
	}{
		{"empty", json.RawMessage{}},
		{"malformed", json.RawMessage(`{"detail":`)},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			h, snap := rvTestSetup(t)
			if err := beginRestoreRecord(ctx, h.db, snap.ID, restoreRecord{Transition: fwstate.TransReload, Emergency: c.record}, h.clock.Now()); err != nil {
				t.Fatal(err)
			}
			out, err := revert(ctx, h.env(), h.deps(), []byte(snap.ID))
			if err != nil {
				t.Fatal(err)
			}
			if !out.Override {
				t.Error("an unreadable emergency record finished as a plain revert")
			}
			var got []rvTestEffect
			for _, o := range out.Effects {
				e := rvTestEffect{o.Seq, o.State, o.Reason, ""}
				if o.Current != nil {
					e.current = string(o.Current.State)
				}
				got = append(got, e)
			}
			if want := []rvTestEffect{{1, core.EffectUnresolved, core.ReasonUncertain, ""}, {2, core.EffectUnresolved, core.ReasonUncertain, ""}}; !reflect.DeepEqual(got, want) {
				t.Errorf("effects = %+v, want %+v", got, want)
			}
			var detail overrideDetail
			if err := json.Unmarshal(out.Detail, &detail); err != nil {
				t.Fatal(err)
			}
			if detail.Snapshot != snap.ID || !strings.Contains(detail.Guard, "record cannot be read") || len(detail.Effects) != 2 || detail.ChainsUnread == "" {
				t.Errorf("override detail = %+v", detail)
			}
			if !reflect.DeepEqual(fileHashes(h.config().Files), snap.Hashes) {
				t.Error("the state files are not the snapshot's")
			}
			if _, pending, _ := restoreIntent(ctx, h.db, snap.ID); pending {
				t.Error("the finished restore left its record")
			}
			if s, err := getSnapshot(ctx, h.db, snap.ID); err != nil || !s.Pinned {
				t.Errorf("snapshot pinned = %v, %v", s.Pinned, err)
			}
		})
	}
}

// TestRevertReaddsAManualRuleWithoutItsComment (ruling cv-103a): a manual
// rule whose comment ufw cannot be given ("-x") is re-added by its undo
// without the comment, so the observation differs from its Before and the
// revert ends needing repair, never reverted.
func TestRevertReaddsAManualRuleWithoutItsComment(t *testing.T) {
	const (
		withDash = "### tuple ### allow tcp 9000 0.0.0.0/0 any 0.0.0.0/0 in comment=2d78"
		bare     = "### tuple ### allow tcp 9000 0.0.0.0/0 any 0.0.0.0/0 in"
	)
	ctx := context.Background()
	h := newFakeHost(t)
	s := h.state()
	s.Rules4 = append(s.Rules4, withDash)
	h.setState(s)
	env, d := h.env(), h.deps()
	raw, err := capture(ctx, env, d, &core.GuardContext{Complete: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := attach(ctx, env); err != nil {
		t.Fatal(err)
	}
	rule := findRules(h.config(), obTestID(t, []string{withDash}, nil))[0].Rule
	op := deleteRuleOp(rule)
	h.ufw[strings.Join(op.Args, " ")] = func(s *fhState) int {
		s.Rules4 = slices.DeleteFunc(s.Rules4, func(l string) bool { return l == withDash })
		return 0
	}
	if err := runUfw(ctx, env, d, op); err != nil {
		t.Fatal(err)
	}
	if err := recordPostHashes(ctx, env, d, string(raw)); err != nil {
		t.Fatal(err)
	}
	rvTestManualEdit(h)
	var undo []string
	readd := func(args []string) {
		h.ufw[strings.Join(args, " ")] = func(s *fhState) int {
			undo = args
			s.Rules4 = append(s.Rules4, bare)
			return 0
		}
	}
	readd(model.AddArgs(rule))
	for pos := 1; pos <= len(h.state().Rules4)+1; pos++ {
		readd(model.InsertArgs(pos, rule))
	}
	h.ufw["reload"] = func(*fhState) int { return 0 }
	h.calls = nil
	_, err = revert(ctx, env, d, raw)
	if err == nil || !strings.Contains(err.Error(), "the revert stopped") {
		t.Fatalf("revert = %v, want it stopped by the missing comment", err)
	}
	if undo == nil || slices.Contains(undo, "-x") {
		t.Errorf("undo ran %q, want the rule without its comment", undo)
	}
	if s, err := getSnapshot(ctx, h.db, string(raw)); err != nil || !s.Pinned {
		t.Errorf("snapshot pinned = %v, %v", s.Pinned, err)
	}
}

// TestRevertOverrideDetailUnread: when the emergency restore cannot read
// the state files or the loaded state before it writes, the override
// detail says so instead of a blank hash or an empty chain list.
func TestRevertOverrideDetailUnread(t *testing.T) {
	ctx := context.Background()
	h, snap := rvTestSetup(t)
	rvTestRemoveOwnRule(h)
	h.guardOK = false
	d := h.deps()
	readConfig, readLoaded := d.readConfig, d.readLoaded
	configFailed, loadedFailed := false, false
	d.readConfig = func(root string) (fwstate.Config, error) {
		if h.guardCalls > 0 && !configFailed {
			configFailed = true
			return fwstate.Config{}, errors.New("disk error")
		}
		return readConfig(root)
	}
	d.readLoaded = func(ctx context.Context, env core.Env) (fwstate.Loaded, error) {
		if h.guardCalls > 0 && !loadedFailed {
			loadedFailed = true
			return fwstate.Loaded{}, errors.New("nft busy")
		}
		return readLoaded(ctx, env)
	}
	out, err := revert(ctx, h.env(), d, []byte(snap.ID))
	if err != nil {
		t.Fatal(err)
	}
	if !configFailed || !loadedFailed {
		t.Fatalf("reads failed: config %v, loaded %v; want both", configFailed, loadedFailed)
	}
	var detail overrideDetail
	if err := json.Unmarshal(out.Detail, &detail); err != nil {
		t.Fatal(err)
	}
	if len(detail.Files) != len(snap.Entries) {
		t.Errorf("files = %+v, want every entry", detail.Files)
	}
	for _, f := range detail.Files {
		if f.Was != wasUnread {
			t.Errorf("file %+v, want was %q", f, wasUnread)
		}
	}
	if len(detail.Chains) != 0 || !strings.Contains(detail.ChainsUnread, "nft busy") {
		t.Errorf("chains = %+v, unread = %q; want the read error", detail.Chains, detail.ChainsUnread)
	}
}

// axImageRepair returns a host on which a repair of change c1, whose
// snapshot holds a before-image, reloaded UFW (effect 1) and so replaced
// the loaded UFW chains, and on which a manual rule was added afterwards to
// user6.rules and the loaded IPv6 chains, as ufw adds one, so the revert
// goes by inverses. imaged is the loaded state of the image.
func axImageRepair(t *testing.T) (h *fakeHost, snap snapshot, imaged fwstate.Loaded) {
	t.Helper()
	ctx := context.Background()
	h = newFakeHost(t)
	h.loaded.V4 = fhParse(t, rsTestSave4)
	imaged = h.loaded
	env, d := h.env(), h.deps()
	raw, err := capture(ctx, env, d, &core.GuardContext{Complete: true}, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := attach(ctx, env); err != nil {
		t.Fatal(err)
	}
	h.ufw["reload"] = func(*fhState) int { return 0 }
	h.ufw["--force enable"] = func(s *fhState) int { s.Enabled = true; return 0 }
	if err := runUfw(ctx, env, d, transitionOp(reloadArgs)); err != nil {
		t.Fatal(err)
	}
	if err := recordPostHashes(ctx, env, d, string(raw)); err != nil {
		t.Fatal(err)
	}
	if snap, err = getSnapshot(ctx, h.db, string(raw)); err != nil {
		t.Fatal(err)
	}
	h.loaded.V4 = fhParse(t, strings.Replace(rsTestSave4, "--dport 22", "--dport 2222", 1))
	rvTestManualEdit(h)
	h.loaded.V6 = fhParse(t, strings.Replace(fhSave, "COMMIT\n", "-A ufw-user-input -s 2001:db8::/32 -p udp -m udp --dport 53 -j DROP\nCOMMIT\n", 1))
	h.onRun = func(h *fakeHost, cmd core.Cmd) core.CmdResult {
		if cmd.Path == "/usr/sbin/ip6tables-nft-restore" {
			h.loaded = imaged
		}
		return core.CmdResult{}
	}
	h.calls = nil
	return h, snap, imaged
}

func TestAxRevertByInversesKeepsAManualEdit(t *testing.T) {
	ctx := context.Background()
	h, snap, _ := axImageRepair(t)
	manual := h.loaded.V6
	_, err := revert(ctx, h.env(), h.deps(), []byte(snap.ID))
	if err == nil || !strings.Contains(err.Error(), "/etc/ufw/user6.rules") {
		t.Fatalf("revert = %v, want an error naming /etc/ufw/user6.rules", err)
	}
	if got, want := rsTestRuns(h), []string{"run /usr/sbin/ufw --force enable"}; !reflect.DeepEqual(got, want) {
		t.Errorf("runs = %q, want %q", got, want)
	}
	if !reflect.DeepEqual(h.loaded.V6, manual) {
		t.Error("the revert removed the manual rule from the loaded IPv6 chains")
	}
	if !slices.Contains(h.state().Rules6, fhRule53v6) {
		t.Error("the revert removed the manual rule from user6.rules")
	}
	if h.guardCalls != 1 {
		t.Errorf("the guard was evaluated %d times, want 1", h.guardCalls)
	}
	if s, err := getSnapshot(ctx, h.db, snap.ID); err != nil || !s.Pinned {
		t.Errorf("snapshot pinned = %v, %v", s.Pinned, err)
	}
}

func TestAxRevertByInversesLoadsTheBeforeImage(t *testing.T) {
	ctx := context.Background()
	h, snap, imaged := axImageRepair(t)
	// The files and the loaded IPv6 chains are back as the snapshot has
	// them, and the operator set a sysctl the repair wrote: the conflict
	// sends the revert by inverses with every state file unchanged.
	s := h.state()
	s.Rules6 = slices.DeleteFunc(s.Rules6, func(l string) bool { return l == fhRule53v6 })
	h.setState(s)
	h.loaded.V6 = imaged.V6
	if err := writeSysctlEffect(ctx, h.env(), h.deps(), "net/ipv4/conf/eth0/rp_filter", "2"); err != nil {
		t.Fatal(err)
	}
	h.sysctl["net/ipv4/conf/eth0/rp_filter"] = "0"
	h.calls = nil
	want := planImage(snap.State.Image, h.loaded)
	out, err := revert(ctx, h.env(), h.deps(), []byte(snap.ID))
	if err != nil {
		t.Fatal(err)
	}
	if out.Override || len(out.Effects) != 2 || out.Effects[0] != (core.EffectOutcome{Seq: 1, State: core.EffectUndone}) ||
		out.Effects[1].Seq != 2 || out.Effects[1].Reason != core.ReasonConflict {
		t.Errorf("outcome = %+v", out)
	}
	if got, want := rsTestRuns(h), []string{"run /usr/sbin/ufw --force enable", "run /usr/sbin/iptables-nft-restore -n", "run /usr/sbin/ip6tables-nft-restore -n"}; !reflect.DeepEqual(got, want) {
		t.Errorf("runs = %q, want %q", got, want)
	}
	if string(h.stdin["/usr/sbin/iptables-nft-restore"]) != string(want.Restore4) {
		t.Errorf("the loaded input =\n%s\nwant\n%s", h.stdin["/usr/sbin/iptables-nft-restore"], want.Restore4)
	}
	if h.sysctl["net/ipv4/conf/eth0/rp_filter"] != "0" {
		t.Errorf("rp_filter = %s, want the operator's 0", h.sysctl["net/ipv4/conf/eth0/rp_filter"])
	}
}

func TestAxRevertByInversesLeavesAChangedBuiltinChain(t *testing.T) {
	ctx := context.Background()
	h, snap, imaged := axImageRepair(t)
	s := h.state()
	s.Rules6 = slices.DeleteFunc(s.Rules6, func(l string) bool { return l == fhRule53v6 })
	h.setState(s)
	h.loaded.V6 = imaged.V6
	if err := writeSysctlEffect(ctx, h.env(), h.deps(), "net/ipv4/conf/eth0/rp_filter", "2"); err != nil {
		t.Fatal(err)
	}
	h.sysctl["net/ipv4/conf/eth0/rp_filter"] = "0"
	changed := strings.Replace(rsTestSave4, "-A INPUT -j ufw-user-input\n", "-A INPUT -j ufw-user-input\n-A INPUT -s 192.0.2.9/32 -j DROP\n", 1)
	h.loaded.V4 = fhParse(t, changed)
	_, err := revert(ctx, h.env(), h.deps(), []byte(snap.ID))
	if err == nil || !strings.Contains(err.Error(), "v4 filter/INPUT") {
		t.Fatalf("revert = %v, want an error naming v4 filter/INPUT", err)
	}
	if strings.Contains(string(h.stdin["/usr/sbin/iptables-nft-restore"]), "-F INPUT") {
		t.Error("the load flushes the changed INPUT chain")
	}
	if h.guardCalls != 1 {
		t.Errorf("the guard was evaluated %d times, want 1", h.guardCalls)
	}
	if s, err := getSnapshot(ctx, h.db, snap.ID); err != nil || !s.Pinned {
		t.Errorf("snapshot pinned = %v, %v", s.Pinned, err)
	}
}
