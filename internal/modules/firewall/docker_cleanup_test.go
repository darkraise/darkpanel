package firewall

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/firewall/dockerfw"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

// clRan is the host's calls that wrote a file or ran a program, in order.
func clRan(r *kfRig) []string {
	var out []string
	for _, c := range r.h.calls {
		if strings.HasPrefix(c, "run ") || strings.HasPrefix(c, "restore ") {
			out = append(out, c)
		}
	}
	return out
}

// clPending is the owners of the cleanup intents still recorded.
func clPending(t *testing.T, r *kfRig) []string {
	t.Helper()
	got, err := pendingCleanups(context.Background(), r.h.db)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// clBlockHeld reports whether file path still holds a Docker block.
func clBlockHeld(t *testing.T, r *kfRig, path string) bool {
	t.Helper()
	_, ok, err := dockerfw.BlockOf(r.h.config().Files[path].Data)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

// clChains lists the darkpanel-* and dp-b-* chains loaded in either family.
func clChains(r *kfRig) []string {
	var out []string
	for _, rs := range []*netfilter.Ruleset{r.h.loaded.V4, r.h.loaded.V6} {
		for _, c := range rs.Table("filter").Chains {
			if strings.HasPrefix(c.Name, "darkpanel-") || strings.HasPrefix(c.Name, "dp-b-") {
				out = append(out, c.Name)
			}
		}
	}
	return out
}

const (
	clReload  = "run /usr/sbin/ufw reload"
	clRestore = "run /usr/sbin/iptables-nft-restore -n"
	clRestor6 = "run /usr/sbin/ip6tables-nft-restore -n"
)

// TestClCleanup: the cleanup writes both files without the block, removes
// the block's chains in both families, leaves DOCKER-USER with -j RETURN,
// reloads an active UFW after the chains are gone, and clears its intent;
// in both modes when no foreign rule is there.
func TestClCleanup(t *testing.T) {
	ctx := context.Background()
	for name, mode := range map[string]cleanupMode{"refuse": cleanupRefuse, "preserve": cleanupPreserve} {
		t.Run(name, func(t *testing.T) {
			r := kfHost(t, nil, nil)
			r.standalone()
			if len(clChains(r)) == 0 || !clBlockHeld(t, r, "/etc/ufw/after.rules") {
				t.Fatalf("the rig starts without a block: chains %q", clChains(r))
			}
			if err := r.f.cleanupChains(ctx, r.env, "uninstall", mode); err != nil {
				t.Fatal(err)
			}
			for _, p := range []string{"/etc/ufw/after.rules", "/etc/ufw/after6.rules"} {
				data := string(r.h.config().Files[p].Data)
				if clBlockHeld(t, r, p) || strings.Contains(data, "DOCKER-USER") || !strings.Contains(data, "# after") {
					t.Errorf("%s = %q, want the file without the block and otherwise as it was", p, data)
				}
			}
			if got := clChains(r); len(got) != 0 {
				t.Errorf("chains %q are still loaded", got)
			}
			for _, f := range []fwstate.Family{fwstate.V4, fwstate.V6} {
				if got := r.loadedLines(f); !slices.Equal(got, kfOne(kfReturn)) {
					t.Errorf("%s DOCKER-USER = %q, want only -j RETURN", f, got)
				}
			}
			want := []string{"restore /etc/ufw/after.rules", "restore /etc/ufw/after6.rules", clRestore, clRestor6, clReload, clRestore, clRestor6}
			if got := clRan(r); !slices.Equal(got, want) {
				t.Errorf("ran %q, want %q: files, the chains, the reload, then DOCKER-USER again", got, want)
			}
			if !strings.Contains(r.loads[0].Input, "-X darkpanel-docker-bindings") || !strings.Contains(r.loads[0].Input, "-X dp-b-0123456789ab") {
				t.Errorf("the IPv4 cleanup input %q does not delete the block's chains", r.loads[0].Input)
			}
			if got := clPending(t, r); len(got) != 0 {
				t.Errorf("intents %q left after a complete cleanup", got)
			}
			if kfRecordHeld(t, r, "uninstall") {
				t.Error("a foreign-rule record is left")
			}
		})
	}
}

// TestClRefuse: foreign rules in either family refuse the cleanup, naming
// them, before an intent, a file or a chain is touched.
func TestClRefuse(t *testing.T) {
	ctx := context.Background()
	b6 := kfLines(t, fwstate.V6)
	for _, c := range []struct {
		name   string
		v4, v6 []string
		want   string
	}{
		{"IPv4", kfJoin(kfOne(kfForeign1), kfLines(t, fwstate.V4)), nil, "192.0.2.0/24"},
		{"IPv6", nil, kfJoin(b6, kfOne("-A DOCKER-USER -s 2001:db8::/32 -j DROP")), "2001:db8::/32"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := kfHost(t, c.v4, c.v6)
			r.standalone()
			err := r.f.cleanupChains(ctx, r.env, "c1", cleanupRefuse)
			var ec *errcode.Error
			if !errors.As(err, &ec) || ec.Code != errcode.FirewallDockerUserInUse || !strings.Contains(ec.Detail, c.want) {
				t.Fatalf("cleanupChains = %v, want %s naming %s", err, errcode.FirewallDockerUserInUse, c.want)
			}
			if got := clRan(r); len(got) != 0 {
				t.Errorf("ran %q, want nothing", got)
			}
			if got := clPending(t, r); len(got) != 0 || kfRecordHeld(t, r, "c1") {
				t.Errorf("a refused cleanup left intents %q or a record", got)
			}
			if !clBlockHeld(t, r, "/etc/ufw/after.rules") || len(clChains(r)) == 0 {
				t.Error("a refused cleanup removed the block or its chains")
			}
		})
	}
}

// TestClPreserve: in preserve mode the foreign rules stay in DOCKER-USER
// where they were, around -j RETURN, and the chains go.
func TestClPreserve(t *testing.T) {
	ctx := context.Background()
	r := kfHost(t, kfJoin(kfOne(kfForeign1), kfLines(t, fwstate.V4), kfOne(kfForeign2)), nil)
	r.standalone()
	if err := r.f.cleanupChains(ctx, r.env, "c1", cleanupPreserve); err != nil {
		t.Fatal(err)
	}
	if got, want := r.loadedLines(fwstate.V4), kfJoin(kfOne(kfForeign1), kfOne(kfReturn), kfOne(kfForeign2)); !slices.Equal(got, want) {
		t.Errorf("IPv4 DOCKER-USER = %q, want %q", got, want)
	}
	if got := clChains(r); len(got) != 0 {
		t.Errorf("chains %q are still loaded", got)
	}
	if got := clPending(t, r); len(got) != 0 || kfRecordHeld(t, r, "c1") {
		t.Errorf("intents %q or a record left", got)
	}
}

// TestClInChange: inside a change the files and the chains are journaled as
// effects, the chain effect goes from loaded to absent, and the cleanup
// leaves the UFW reload to the change.
func TestClInChange(t *testing.T) {
	ctx := context.Background()
	r := kfHost(t, nil, nil)
	if err := r.f.cleanupChains(ctx, r.env, r.env.ChangeID, cleanupRefuse); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range r.h.journal.effects {
		got = append(got, string(e.State)+" "+e.Resource)
	}
	want := []string{"made firewall.file:/etc/ufw/after.rules", "made firewall.file:/etc/ufw/after6.rules", "made firewall.chains:docker"}
	if !slices.Equal(got, want) {
		t.Fatalf("effects %q, want %q", got, want)
	}
	chains := r.h.journal.effects[2]
	var before, after string
	if err := json.Unmarshal(chains.Before.State, &before); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(chains.After.State, &after); err != nil {
		t.Fatal(err)
	}
	if before != "loaded" || after != "absent" || !chains.Local || !chains.InverseLocal {
		t.Errorf("chain effect before %q after %q local %v/%v, want loaded -> absent, local", before, after, chains.Local, chains.InverseLocal)
	}
	if slices.Contains(clRan(r), clReload) {
		t.Errorf("ran %q: inside a change the cleanup does not reload UFW", clRan(r))
	}
	if got := clChains(r); len(got) != 0 {
		t.Errorf("chains %q are still loaded", got)
	}
}

// TestClNothingToClean: a host without the block in its files or the
// chains in the kernel is left alone, without a reload, and keeps no
// intent.
func TestClNothingToClean(t *testing.T) {
	ctx := context.Background()
	r := kfHost(t, nil, nil)
	r.standalone()
	for _, p := range []string{"/etc/ufw/after.rules", "/etc/ufw/after6.rules"} {
		r.h.writeFile(p, []byte("*filter\n# after\nCOMMIT\n"))
	}
	r.h.loaded = fwstate.Loaded{Variant: "nft", V4: fhParse(t, fhSave), V6: fhParse(t, fhSave)}
	if err := r.f.cleanupChains(ctx, r.env, "c1", cleanupRefuse); err != nil {
		t.Fatal(err)
	}
	if got := clRan(r); len(got) != 0 {
		t.Errorf("ran %q, want nothing", got)
	}
	if got := clPending(t, r); len(got) != 0 {
		t.Errorf("intents %q left", got)
	}
}

// TestClFinishCleanups: a cleanup a crash cut off after its files were
// written is finished from the record: the block's chains go, the lines
// still in DOCKER-USER are not mistaken for foreign rules, the recorded
// foreign rule is loaded back (the record's own block lines are not), and
// a second call finds nothing to do.
func TestClFinishCleanups(t *testing.T) {
	ctx := context.Background()
	r := kfHost(t, kfJoin(kfOne(kfForeign1), kfLines(t, fwstate.V4)), nil)
	r.standalone()
	for _, p := range []string{"/etc/ufw/after.rules", "/etc/ufw/after6.rules"} {
		r.h.writeFile(p, []byte("*filter\n# after\nCOMMIT\n"))
	}
	rec := foreignRecord{
		Layouts: map[fwstate.Family]dockerfw.Layout{fwstate.V4: {Before: kfOne(kfForeign1)}, fwstate.V6: {}},
		Block:   kfNext(t),
	}
	if err := beginCleanup(ctx, r.h.db, "c9", r.h.clock.Now()); err != nil {
		t.Fatal(err)
	}
	if err := putForeign(ctx, r.h.db, "c9", rec, r.h.clock.Now()); err != nil {
		t.Fatal(err)
	}
	if err := r.f.finishCleanups(ctx, r.env); err != nil {
		t.Fatal(err)
	}
	if got, want := r.loadedLines(fwstate.V4), kfJoin(kfOne(kfForeign1), kfOne(kfReturn)); !slices.Equal(got, want) {
		t.Errorf("IPv4 DOCKER-USER = %q, want %q", got, want)
	}
	if got, want := r.loadedLines(fwstate.V6), kfOne(kfReturn); !slices.Equal(got, want) {
		t.Errorf("IPv6 DOCKER-USER = %q, want %q", got, want)
	}
	if got := clChains(r); len(got) != 0 {
		t.Errorf("chains %q are still loaded", got)
	}
	if got := clPending(t, r); len(got) != 0 || kfRecordHeld(t, r, "c9") {
		t.Errorf("intents %q or a record left", got)
	}
	if slices.Contains(clRan(r), "restore /etc/ufw/after.rules") {
		t.Error("the repeat rewrote a file that holds no block")
	}
	n := len(r.h.calls)
	if err := r.f.finishCleanups(ctx, r.env); err != nil || len(r.h.calls) != n {
		t.Errorf("a second finishCleanups = %v and made %d calls, want nothing", err, len(r.h.calls)-n)
	}
}

// TestClFailedLoadIsFinished: a chain load that fails leaves the intent
// and the record, and finishCleanups completes the cleanup, keeping the
// foreign rule that sat in DOCKER-USER the first time.
func TestClFailedLoadIsFinished(t *testing.T) {
	ctx := context.Background()
	r := kfHost(t, kfJoin(kfOne(kfForeign1), kfLines(t, fwstate.V4)), nil)
	r.standalone()
	inner, broken := r.h.onRun, true
	r.h.onRun = func(h *fakeHost, cmd core.Cmd) core.CmdResult {
		if broken && cmd.Path == "/usr/sbin/ip6tables-nft-restore" {
			return core.CmdResult{ExitCode: 1, Stderr: []byte("ip6tables-restore: resource problem")}
		}
		return inner(h, cmd)
	}
	err := r.f.cleanupChains(ctx, r.env, "c1", cleanupPreserve)
	if err == nil || !strings.Contains(err.Error(), "ip6tables-nft-restore") {
		t.Fatalf("cleanupChains = %v, want the failed IPv6 load", err)
	}
	if got := clPending(t, r); !slices.Equal(got, kfOne("c1")) || !kfRecordHeld(t, r, "c1") {
		t.Fatalf("intents %q, record %v, want c1's intent and record kept", got, kfRecordHeld(t, r, "c1"))
	}
	broken = false
	if err := r.f.finishCleanups(ctx, r.env); err != nil {
		t.Fatal(err)
	}
	if got, want := r.loadedLines(fwstate.V4), kfJoin(kfOne(kfForeign1), kfOne(kfReturn)); !slices.Equal(got, want) {
		t.Errorf("IPv4 DOCKER-USER = %q, want %q", got, want)
	}
	if got := clChains(r); len(got) != 0 {
		t.Errorf("chains %q are still loaded", got)
	}
	if got := clPending(t, r); len(got) != 0 || kfRecordHeld(t, r, "c1") {
		t.Errorf("intents %q or a record left", got)
	}
}

// TestClVerifyFails: a load that exits 0 but leaves a block chain loaded
// is an error and the intent stays.
func TestClVerifyFails(t *testing.T) {
	ctx := context.Background()
	r := kfHost(t, nil, nil)
	r.standalone()
	inner := r.h.onRun
	r.h.onRun = func(h *fakeHost, cmd core.Cmd) core.CmdResult {
		if cmd.Path == "/usr/sbin/iptables-nft-restore" && strings.Contains(string(cmd.Stdin), "-X ") {
			return core.CmdResult{}
		}
		return inner(h, cmd)
	}
	err := r.f.cleanupChains(ctx, r.env, "c1", cleanupRefuse)
	if err == nil || !strings.Contains(err.Error(), "darkpanel-docker-bindings") {
		t.Fatalf("cleanupChains = %v, want an error naming the chain still loaded", err)
	}
	if got := clPending(t, r); !slices.Equal(got, kfOne("c1")) {
		t.Errorf("intents %q, want c1's intent kept", got)
	}
}

// TestClCleanChainsWiring: the inverse of the chains effect calls
// deps.cleanChains, which production sets to the preserving cleanup.
func TestClCleanChainsWiring(t *testing.T) {
	if productionFW().d.cleanChains == nil {
		t.Fatal("productionFW leaves deps.cleanChains nil")
	}
	ctx := context.Background()
	r := kfHost(t, kfJoin(kfLines(t, fwstate.V4), kfOne(kfForeign1)), nil)
	r.standalone()
	r.f.d.cleanChains = r.f.cleanChainsPreserving
	if err := r.f.d.cleanChains(ctx, r.env, "c1"); err != nil {
		t.Fatalf("the preserving cleanup = %v, want the foreign rule kept without a refusal", err)
	}
	if got, want := r.loadedLines(fwstate.V4), kfJoin(kfOne(kfReturn), kfOne(kfForeign1)); !slices.Equal(got, want) {
		t.Errorf("IPv4 DOCKER-USER = %q, want %q", got, want)
	}
}
