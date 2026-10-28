package firewall

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/dockerfw"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/modules/firewall/loaded"
)

const (
	kfwForeign = "-A DOCKER-USER -s 192.0.2.0/24 -j DROP"
	kfwReturn  = "-A DOCKER-USER -j RETURN"
	kfwPlain4  = "*filter\n# after\nCOMMIT\n"
	kfwPlain6  = "*filter\n# after6\nCOMMIT\n"
)

// kfwBlockLines are the DOCKER-USER lines of the block the fake host holds.
var kfwBlockLines = []string{
	"-A DOCKER-USER -i docker0 -j RETURN",
	"-A DOCKER-USER -m conntrack --ctstate DNAT -j darkpanel-docker-deny",
	kfwReturn,
}

func kfwBlockText() string {
	return "# BEGIN DARKPANEL docker\n*filter\n:DOCKER-USER - [0:0]\n:darkpanel-docker-deny - [0:0]\n" +
		strings.Join(kfwBlockLines, "\n") + "\n-A darkpanel-docker-deny -j DROP\nCOMMIT\n# END DARKPANEL docker\n"
}

// kfwSave is a loaded filter table whose DOCKER-USER holds rules and which,
// when owned, holds the block's other chain.
func kfwSave(rules []string, owned bool) string {
	var b strings.Builder
	b.WriteString("*filter\n:INPUT DROP [0:0]\n:FORWARD DROP [0:0]\n:OUTPUT ACCEPT [0:0]\n:ufw-user-input - [0:0]\n:DOCKER-USER - [0:0]\n")
	if owned {
		b.WriteString(":darkpanel-docker-deny - [0:0]\n")
	}
	b.WriteString("-A INPUT -j ufw-user-input\n-A ufw-user-input -p tcp -m tcp --dport 22 -j ACCEPT\n")
	for _, r := range rules {
		b.WriteString(r + "\n")
	}
	if owned {
		b.WriteString("-A darkpanel-docker-deny -j DROP\n")
	}
	b.WriteString("COMMIT\n")
	return b.String()
}

// kfwSet makes the fake host's loaded state hold rules in DOCKER-USER in
// both families.
func kfwSet(t *testing.T, h *fakeHost, rules []string, owned bool) {
	t.Helper()
	h.loaded.V4, h.loaded.V6 = fhParse(t, kfwSave(rules, owned)), fhParse(t, kfwSave(rules, owned))
}

// kfwChain lists the IPv4 DOCKER-USER rules the fake host has loaded.
func kfwChain(h *fakeHost) []string {
	var out []string
	for _, tb := range h.loaded.V4.Tables {
		if c := tb.Chain("DOCKER-USER"); c != nil {
			for _, r := range c.Rules {
				out = append(out, r.Text)
			}
		}
	}
	return out
}

// kfwWith inserts rule into rules at index at.
func kfwWith(rules []string, at int, rule string) []string {
	return slices.Insert(slices.Clone(rules), at, rule)
}

// kfwHost is a fake host whose UFW is active with the Docker block in both
// after files (or without it) and DOCKER-USER loaded as the block makes it.
// "ufw reload" refills DOCKER-USER from the block when the files hold it,
// and a restore program loads the DOCKER-USER lines of its input; the real
// comparison checks every restore against what the host predicts.
func kfwHost(t *testing.T, block bool) (*fakeHost, deps) {
	t.Helper()
	h := newFakeHost(t)
	kfwFiles(h, block)
	if block {
		kfwSet(t, h, kfwBlockLines, true)
	} else {
		kfwSet(t, h, []string{kfwReturn}, false)
	}
	h.ufw["reload"] = func(*fhState) int {
		if strings.Contains(string(h.config().Files["/etc/ufw/after.rules"].Data), "DARKPANEL docker") {
			kfwSet(t, h, kfwBlockLines, true)
		}
		return 0
	}
	h.onRun = func(h *fakeHost, cmd core.Cmd) core.CmdResult {
		if !strings.HasSuffix(cmd.Path, "-restore") {
			return core.CmdResult{}
		}
		var rules []string
		for _, l := range strings.Split(string(cmd.Stdin), "\n") {
			if strings.HasPrefix(l, "-A DOCKER-USER ") {
				rules = append(rules, l)
			}
		}
		owned := h.loaded.V4.Tables[0].Chain("darkpanel-docker-deny") != nil
		kfwSet(t, h, rules, owned)
		return core.CmdResult{}
	}
	d := h.deps()
	d.compare = loaded.Compare
	kfwPredict(t, h, block)
	return h, d
}

func kfwFiles(h *fakeHost, block bool) {
	a4, a6 := kfwPlain4, kfwPlain6
	if block {
		a4, a6 = a4+kfwBlockText(), a6+kfwBlockText()
	}
	h.writeFile("/etc/ufw/after.rules", []byte(a4))
	h.writeFile("/etc/ufw/after6.rules", []byte(a6))
}

// kfwPredict sets the loaded state a restore of a snapshot with or without
// the block is predicted to give.
func kfwPredict(t *testing.T, h *fakeHost, block bool) {
	t.Helper()
	rules, owned := []string{kfwReturn}, false
	if block {
		rules, owned = kfwBlockLines, true
	}
	h.predicted = &fwstate.Loaded{Variant: "nft", V4: fhParse(t, kfwSave(rules, owned)), V6: fhParse(t, kfwSave(rules, owned))}
}

// kfwSnapshot captures the host as it is.
func kfwSnapshot(t *testing.T, h *fakeHost, d deps) snapshot {
	t.Helper()
	env := h.env()
	env.ChangeID = ""
	raw, err := capture(context.Background(), env, d, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := getSnapshot(context.Background(), h.db, string(raw))
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func kfwInput(h *fakeHost, program string) []string {
	in := strings.TrimSpace(string(h.stdin["/usr/sbin/"+program+"-nft-restore"]))
	if in == "" {
		return nil
	}
	return strings.Split(in, "\n")
}

func kfwInputOf(rules ...string) []string {
	return append(append([]string{"*filter", ":DOCKER-USER - [0:0]"}, rules...), "COMMIT")
}

// TestKfwRestore: a whole restore reloads
// DOCKER-USER with the block's lines and the foreign rules that were loaded,
// before or after them, and a foreign rule between the block's lines goes
// before the first and is reported. A restore to a state without the block
// leaves the cleanup of the loaded chains to the module's cleanup, which
// keeps foreign rules itself. Nothing is reloaded when no foreign rule is
// loaded, and the verification does not count a foreign rule as a
// difference.
func TestKfwRestore(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name      string
		snapBlock bool
		nowBlock  bool
		now       []string // DOCKER-USER now
		wantInput []string // the IPv4 reload of DOCKER-USER; nil for none
		wantChain []string // DOCKER-USER afterwards
		wantOrder []string
		wantClean bool
	}{
		{
			name: "a foreign rule after the block's lines", snapBlock: true, nowBlock: true,
			now:       kfwWith(kfwBlockLines, 3, kfwForeign),
			wantInput: kfwInputOf(append(slices.Clone(kfwBlockLines), kfwForeign)...),
			wantChain: append(slices.Clone(kfwBlockLines), kfwForeign),
		},
		{
			name: "a foreign rule before the block's lines", snapBlock: true, nowBlock: true,
			now:       kfwWith(kfwBlockLines, 0, kfwForeign),
			wantInput: kfwInputOf(append([]string{kfwForeign}, kfwBlockLines...)...),
			wantChain: append([]string{kfwForeign}, kfwBlockLines...),
		},
		{
			name: "a foreign rule between the block's lines", snapBlock: true, nowBlock: true,
			now:       kfwWith(kfwBlockLines, 1, kfwForeign),
			wantInput: kfwInputOf(append([]string{kfwForeign}, kfwBlockLines...)...),
			wantChain: append([]string{kfwForeign}, kfwBlockLines...),
			wantOrder: kfwOrder(kfwWith(kfwBlockLines, 1, kfwForeign)),
		},
		{
			name: "no foreign rule", snapBlock: true, nowBlock: true,
			now: kfwBlockLines, wantChain: kfwBlockLines,
		},
		{
			name: "the snapshot has no block and the chains are loaded", snapBlock: false, nowBlock: true,
			now:       kfwWith(kfwBlockLines, 3, kfwForeign),
			wantChain: []string{kfwForeign, kfwReturn}, wantClean: true,
		},
		{
			name: "the snapshot has no block and none is loaded", snapBlock: false, nowBlock: false,
			now: []string{kfwForeign, kfwReturn}, wantChain: []string{kfwForeign, kfwReturn},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			h, d := kfwHost(t, c.snapBlock)
			snap := kfwSnapshot(t, h, d)
			kfwFiles(h, c.nowBlock)
			kfwSet(t, h, c.now, c.nowBlock)
			rvTestManualEdit(h)
			var cleaned []string
			d.cleanChains = func(_ context.Context, _ core.Env, owner string) error {
				cleaned = append(cleaned, owner)
				h.calls = append(h.calls, "clean "+owner)
				kfwSet(t, h, []string{kfwForeign, kfwReturn}, false)
				return nil
			}
			h.calls, h.stdin = nil, map[string][]byte{}

			rep, err := restore(ctx, h.env(), d, snap)
			if err != nil {
				t.Fatal(err)
			}
			if got := kfwInput(h, "iptables"); !reflect.DeepEqual(got, c.wantInput) {
				t.Errorf("DOCKER-USER reload input %q, want %q", got, c.wantInput)
			}
			if got := kfwChain(h); !reflect.DeepEqual(got, c.wantChain) {
				t.Errorf("DOCKER-USER holds %q, want %q", got, c.wantChain)
			}
			if !slices.Equal(rep.Interleaved, c.wantOrder) {
				t.Errorf("interleaved order %q, want %q", rep.Interleaved, c.wantOrder)
			}
			if c.wantClean != (len(cleaned) == 1) || (c.wantClean && cleaned[0] != "c1") {
				t.Errorf("cleanups %q, want one for change c1: %v", cleaned, c.wantClean)
			}
			if c.wantClean && (kfwIndex(h.calls, "clean c1") > kfwIndex(h.calls, "run /usr/sbin/ufw reload") ||
				kfwIndex(h.calls, "clean c1") > kfwIndex(h.calls, "restore /etc/")) {
				t.Errorf("calls %q, want the cleanup before the first file is written and before the transition", h.calls)
			}
			if _, ok, err := foreignOf(ctx, h.db, "restore:"+snap.ID); err != nil || ok {
				t.Errorf("a foreign record is left (%v, %v)", ok, err)
			}
		})
	}
}

// kfwOrder is the original order a report gives when both families hold
// rules: the IPv4 rules, then the IPv6 rules, each behind its family.
func kfwOrder(rules []string) []string {
	var out []string
	for _, fam := range []string{"v4", "v6"} {
		for _, r := range rules {
			out = append(out, fam+" "+r)
		}
	}
	return out
}

func kfwIndex(calls []string, call string) int {
	return slices.IndexFunc(calls, func(c string) bool { return strings.HasPrefix(c, call) })
}

// kfwRevertHost is a fake host on which change c1 set the outgoing default
// to DROP (effect 1) with the Docker block in the files, as its snapshot
// holds them; the post-apply hashes are recorded.
func kfwRevertHost(t *testing.T) (*fakeHost, deps, snapshot) {
	t.Helper()
	ctx := context.Background()
	h, d := kfwHost(t, true)
	env := h.env()
	raw, err := capture(ctx, env, d, &core.GuardContext{Complete: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := attach(ctx, env); err != nil {
		t.Fatal(err)
	}
	h.ufw["default deny outgoing"] = func(s *fhState) int { s.Output = "DROP"; return 0 }
	h.ufw["default allow outgoing"] = func(s *fhState) int { s.Output = "ACCEPT"; return 0 }
	op, err := defaultOp("outgoing", "DROP")
	if err != nil {
		t.Fatal(err)
	}
	if err := runUfw(ctx, env, d, op); err != nil {
		t.Fatal(err)
	}
	if err := recordPostHashes(ctx, env, d, string(raw)); err != nil {
		t.Fatal(err)
	}
	snap, err := getSnapshot(ctx, h.db, string(raw))
	if err != nil {
		t.Fatal(err)
	}
	h.calls, h.stdin = nil, map[string][]byte{}
	return h, d, snap
}

// TestKfwRevert: a revert never refuses a foreign
// DOCKER-USER rule. A whole restore and the inverses of a change that
// someone edited around both leave the rule loaded after the block's
// lines; a foreign rule between the block's lines ends the revert with an
// error that gives the original order.
func TestKfwRevert(t *testing.T) {
	ctx := context.Background()
	t.Run("whole restore", func(t *testing.T) {
		h, d, snap := kfwRevertHost(t)
		kfwSet(t, h, kfwWith(kfwBlockLines, 3, kfwForeign), true)
		out, err := revert(ctx, h.env(), d, []byte(snap.ID))
		if err != nil || len(out.Effects) != 1 || out.Effects[0].State != core.EffectUndone {
			t.Fatalf("revert = %+v, %v, want effect 1 undone", out, err)
		}
		if got, want := kfwChain(h), append(slices.Clone(kfwBlockLines), kfwForeign); !slices.Equal(got, want) {
			t.Errorf("DOCKER-USER holds %q, want %q", got, want)
		}
	})
	t.Run("inverses", func(t *testing.T) {
		h, d, _ := kfwRevertHost(t)
		kfwSet(t, h, kfwWith(kfwBlockLines, 3, kfwForeign), true)
		rvTestManualEdit(h)
		out, err := revert(ctx, h.env(), d, []byte(kfwSnapshotID(t, h)))
		if err != nil || len(out.Effects) != 1 || out.Effects[0].State != core.EffectUndone {
			t.Fatalf("revert = %+v, %v, want effect 1 undone", out, err)
		}
		if !slices.Contains(unRan(h, 0), "default allow outgoing") {
			t.Fatalf("ufw ran %q, want the inverses, not a whole restore", unRan(h, 0))
		}
		if got, want := kfwChain(h), append(slices.Clone(kfwBlockLines), kfwForeign); !slices.Equal(got, want) {
			t.Errorf("DOCKER-USER holds %q, want %q", got, want)
		}
	})
	t.Run("a failed inverse loads the foreign rules back", func(t *testing.T) {
		h, d, _ := kfwRevertHost(t)
		kfwSet(t, h, kfwWith(kfwBlockLines, 3, kfwForeign), true)
		rvTestManualEdit(h)
		h.ufw["default allow outgoing"] = func(*fhState) int { return 1 }
		if _, err := revert(ctx, h.env(), d, []byte(kfwSnapshotID(t, h))); err == nil {
			t.Fatal("revert succeeded, want the failed inverse reported")
		}
		if _, ok, err := foreignOf(ctx, h.db, "revert:c1"); err != nil || ok {
			t.Errorf("the revert:c1 record is left (%v, %v)", ok, err)
		}
		if got, want := kfwChain(h), append(slices.Clone(kfwBlockLines), kfwForeign); !slices.Equal(got, want) {
			t.Errorf("DOCKER-USER holds %q, want %q", got, want)
		}
	})
	t.Run("inverses of a cleanup", func(t *testing.T) {
		h, d := kfwHost(t, true)
		env := h.env()
		raw, err := capture(ctx, env, d, &core.GuardContext{Complete: true}, false)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := attach(ctx, env); err != nil {
			t.Fatal(err)
		}
		load := h.onRun
		h.onRun = func(h *fakeHost, cmd core.Cmd) core.CmdResult {
			if strings.Contains(string(cmd.Stdin), "-X darkpanel-docker-deny") {
				kfwSet(t, h, []string{kfwReturn}, false)
				return core.CmdResult{}
			}
			return load(h, cmd)
		}
		if err := (fw{d: d}).cleanupChains(ctx, env, "c1", cleanupPreserve); err != nil {
			t.Fatal(err)
		}
		if err := recordPostHashes(ctx, env, d, string(raw)); err != nil {
			t.Fatal(err)
		}
		h.calls, h.stdin = nil, map[string][]byte{}
		rvTestManualEdit(h)
		out, err := revert(ctx, h.env(), d, []byte(kfwSnapshotID(t, h)))
		if err != nil || len(out.Effects) == 0 {
			t.Fatalf("revert = %+v, %v, want the cleanup's effects undone", out, err)
		}
		for _, e := range out.Effects {
			if e.State != core.EffectUndone {
				t.Errorf("effect %+v, want it undone", e)
			}
		}
		owned := h.loaded.V4.Tables[0].Chain("darkpanel-docker-deny") != nil
		if got := kfwChain(h); !slices.Equal(got, kfwBlockLines) || !owned {
			t.Errorf("DOCKER-USER holds %q (block chain loaded: %v), want the block's lines with its chain loaded again", got, owned)
		}
	})
	t.Run("an interrupted restore, finished", func(t *testing.T) {
		h, d, snap := kfwRevertHost(t)
		if err := beginRestoreRecord(ctx, h.db, snap.ID, restoreRecord{Transition: fwstate.TransReload}, time.Now()); err != nil {
			t.Fatal(err)
		}
		kfwSet(t, h, kfwWith(kfwBlockLines, 1, kfwForeign), true)
		if _, err := revert(ctx, h.env(), d, []byte(snap.ID)); err == nil || !strings.Contains(err.Error(), "original order") {
			t.Fatalf("revert = %v, want an error that lists the original order", err)
		}
	})
	t.Run("interleaved", func(t *testing.T) {
		h, d, snap := kfwRevertHost(t)
		kfwSet(t, h, kfwWith(kfwBlockLines, 1, kfwForeign), true)
		_, err := revert(ctx, h.env(), d, []byte(snap.ID))
		if err == nil || !strings.Contains(err.Error(), "original order") ||
			strings.Index(err.Error(), kfwBlockLines[0]) > strings.Index(err.Error(), kfwForeign) ||
			strings.Index(err.Error(), kfwForeign) > strings.Index(err.Error(), kfwBlockLines[1]) {
			t.Fatalf("revert = %v, want an error that lists the original order", err)
		}
		if got, want := kfwChain(h), append([]string{kfwForeign}, kfwBlockLines...); !slices.Equal(got, want) {
			t.Errorf("DOCKER-USER holds %q, want the foreign rule before the block's lines: %q", got, want)
		}
		if s, err := getSnapshot(ctx, h.db, snap.ID); err != nil || !s.Pinned {
			t.Errorf("snapshot pinned = %v (%v), want it pinned for the repair", s.Pinned, err)
		}
	})
}

func kfwSnapshotID(t *testing.T, h *fakeHost) string {
	t.Helper()
	id, err := snapshotOfChange(context.Background(), h.db, "c1")
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// TestKfwReloadsRecorded: a record that a cut-off run left
// (the foreign rules before the transition flushed DOCKER-USER) is loaded
// again before the retry writes anything.
func TestKfwReloadsRecorded(t *testing.T) {
	ctx := context.Background()
	h, d, snap := kfwRevertHost(t)
	rec := foreignRecord{
		Layouts: map[fwstate.Family]dockerfw.Layout{fwstate.V4: {After: []string{kfwForeign}}},
		Block:   map[fwstate.Family][]string{fwstate.V4: kfwBlockLines},
	}
	if err := putForeign(ctx, h.db, "restore:"+snap.ID, rec, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := revert(ctx, h.env(), d, []byte(snap.ID)); err != nil {
		t.Fatal(err)
	}
	load, file := kfwIndex(h.calls, "run /usr/sbin/iptables-nft-restore -n"), kfwIndex(h.calls, "restore /etc/")
	if load < 0 || file < 0 || load > file {
		t.Errorf("calls %q, want the recorded reload before the first file is written", h.calls)
	}
	if got, want := kfwChain(h), append(slices.Clone(kfwBlockLines), kfwForeign); !slices.Equal(got, want) {
		t.Errorf("DOCKER-USER holds %q, want %q", got, want)
	}
	if _, ok, err := foreignOf(ctx, h.db, "restore:"+snap.ID); err != nil || ok {
		t.Errorf("the record is left (%v, %v)", ok, err)
	}
}

// TestKfwEmergencyOrder: the connectivity
// exception's record gives the original order of a foreign rule that sat
// between the block's lines.
func TestKfwEmergencyOrder(t *testing.T) {
	ctx := context.Background()
	h, d, snap := kfwRevertHost(t)
	s := h.state()
	s.Output = "ACCEPT"
	h.setState(s)
	h.guardOK, h.guardDetail = false, "the guard fails"
	kfwSet(t, h, kfwWith(kfwBlockLines, 1, kfwForeign), true)
	out, err := revert(ctx, h.env(), d, []byte(snap.ID))
	if err != nil || !out.Override {
		t.Fatalf("revert = %+v, %v, want the override", out, err)
	}
	var detail overrideDetail
	if err := json.Unmarshal(out.Detail, &detail); err != nil {
		t.Fatal(err)
	}
	if want := kfwOrder(kfwWith(kfwBlockLines, 1, kfwForeign)); !slices.Equal(detail.InterleavedForeign, want) {
		t.Errorf("interleaved order %q, want %q", detail.InterleavedForeign, want)
	}
	if got, want := kfwChain(h), append([]string{kfwForeign}, kfwBlockLines...); !slices.Equal(got, want) {
		t.Errorf("DOCKER-USER holds %q, want %q", got, want)
	}
}

// TestKfwCleanupUnjournaled: the owned-chain cleanup that a restore and the
// undo of a chains effect run inside a revert journals nothing in the change
// being reverted, removes the block's chains and keeps the foreign rule.
func TestKfwCleanupUnjournaled(t *testing.T) {
	ctx := context.Background()
	check := func(t *testing.T, h *fakeHost, env core.Env, n int) {
		t.Helper()
		if got := kfwJournaled(t, env); got != n {
			t.Errorf("the journal holds %d effects after the cleanup, want %d", got, n)
		}
		if got := kfwChain(h); !slices.Contains(got, kfwForeign) || h.loaded.V4.Tables[0].Chain("darkpanel-docker-deny") != nil {
			t.Errorf("DOCKER-USER holds %q, want the foreign rule kept and the block's chains removed", got)
		}
	}
	t.Run("a restore", func(t *testing.T) {
		h, d := kfwHost(t, false)
		snap := kfwSnapshot(t, h, d)
		kfwFiles(h, true)
		kfwSet(t, h, kfwWith(kfwBlockLines, 3, kfwForeign), true)
		rvTestManualEdit(h)
		kfwCleanReal(t, h, &d)
		env := h.env()
		n := kfwJournaled(t, env)
		if _, err := restore(ctx, env, d, snap); err != nil {
			t.Fatal(err)
		}
		check(t, h, env, n)
	})
	t.Run("the undo of a chains effect", func(t *testing.T) {
		h, d := kfwHost(t, true)
		kfwSet(t, h, kfwWith(kfwBlockLines, 3, kfwForeign), true)
		kfwCleanReal(t, h, &d)
		env := h.env()
		n := kfwJournaled(t, env)
		e := core.Effect{Seq: 1, Kind: core.EffectModify, Resource: deResourceChains,
			Before: core.StateDoc{State: json.RawMessage(`"absent"`)}}
		if err := undoDocker(ctx, env, d, nounChains, "docker", e); err != nil {
			t.Fatal(err)
		}
		check(t, h, env, n)
	})
}

// kfwCleanReal gives d the production cleanup and makes the fake host drop
// the block's chains, leaving -j RETURN, when the cleanup's input deletes
// them.
func kfwCleanReal(t *testing.T, h *fakeHost, d *deps) {
	t.Helper()
	load := h.onRun
	h.onRun = func(h *fakeHost, cmd core.Cmd) core.CmdResult {
		if strings.Contains(string(cmd.Stdin), "-X darkpanel-docker-deny") {
			kfwSet(t, h, []string{kfwReturn}, false)
			return core.CmdResult{}
		}
		return load(h, cmd)
	}
	d.cleanChains = fw{d: *d}.cleanChainsPreserving
}

// kfwJournaled counts the effects env's journal holds.
func kfwJournaled(t *testing.T, env core.Env) int {
	t.Helper()
	effects, err := env.Effects.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return len(effects)
}
