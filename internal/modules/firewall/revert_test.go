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
