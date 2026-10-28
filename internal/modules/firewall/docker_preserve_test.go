package firewall

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/dockerfw"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

// Two foreign DOCKER-USER rules, as iptables-save prints them.
const (
	kfForeign1 = "-A DOCKER-USER -s 192.0.2.0/24 -j DROP"
	kfForeign2 = "-A DOCKER-USER -s 198.51.100.0/24 -j DROP"
	kfReturn   = "-A DOCKER-USER -j RETURN"
)

// kfInput is the block every kfHost writes: the default private sources
// and one binding with its chain.
func kfInput() dockerfw.BlockInput {
	return dockerfw.BlockInput{
		Settings: dockerfw.DefaultSettings(),
		Lines: []dockerfw.BindingLine{{Proto: "tcp", ContainerAddr: netip.MustParseAddr("172.17.0.2"), ContainerPort: 80,
			HostPort: 8080, Chain: "dp-b-0123456789ab"}},
		Chains: []dockerfw.BindingChain{{Name: "dp-b-0123456789ab"}},
	}
}

// kfBlock is the block's rules in family f, parsed as UFW loads them.
func kfBlock(t *testing.T, f fwstate.Family) *netfilter.Ruleset {
	t.Helper()
	rs, err := dockerfw.BlockRules(dockerfw.Render(kfInput(), f))
	if err != nil {
		t.Fatal(err)
	}
	return rs
}

func kfTexts(rules []netfilter.Rule) []string {
	var out []string
	for _, r := range rules {
		out = append(out, r.Text)
	}
	return out
}

// kfLines are the block's DOCKER-USER lines in family f.
func kfLines(t *testing.T, f fwstate.Family) []string {
	t.Helper()
	c := kfBlock(t, f).Table("filter").Chain(dockerfw.ChainDockerUser)
	if c == nil || len(c.Rules) < 4 {
		t.Fatalf("the %s block has no DOCKER-USER chain with several rules: %+v", f, c)
	}
	return kfTexts(c.Rules)
}

// kfSave is the loaded filter table of family f: UFW's chains, the block's
// chains and, as DOCKER-USER, the lines user.
func kfSave(t *testing.T, f fwstate.Family, user []string) string {
	t.Helper()
	in := "ufw-user-input"
	if f == fwstate.V6 {
		in = "ufw6-user-input"
	}
	chains := kfBlock(t, f).Table("filter").Chains
	var b strings.Builder
	fmt.Fprintf(&b, "*filter\n:INPUT DROP [0:0]\n:FORWARD DROP [0:0]\n:OUTPUT ACCEPT [0:0]\n:%s - [0:0]\n", in)
	for _, c := range chains {
		fmt.Fprintf(&b, ":%s - [0:0]\n", c.Name)
	}
	fmt.Fprintf(&b, "-A INPUT -j %s\n-A %s -p tcp -m tcp --dport 22 -j ACCEPT\n", in, in)
	for _, c := range chains {
		lines := kfTexts(c.Rules)
		if c.Name == dockerfw.ChainDockerUser {
			lines = user
		}
		for _, l := range lines {
			b.WriteString(l + "\n")
		}
	}
	b.WriteString("COMMIT\n")
	return b.String()
}

// kfChainLines are the rules of DOCKER-USER in the loaded ruleset of f.
func kfChainLines(rs *netfilter.Ruleset) []string {
	return kfTexts(chainOf(rs, "filter", dockerfw.ChainDockerUser).Rules)
}

// kfApply applies a *-restore -n input to the filter table of rs as
// iptables-restore does: ":chain" creates or flushes a chain, "-F" flushes
// and "-X" deletes one, "-A" appends. The input fails, and changes
// nothing, when it appends to a chain that does not exist or leaves a rule
// jumping to one.
func kfApply(rs *netfilter.Ruleset, input string) (*netfilter.Ruleset, error) {
	type chain struct {
		name, policy string
		rules        []string
	}
	var chains []chain
	for _, c := range rs.Table("filter").Chains {
		policy := "-"
		if c.Builtin {
			policy = c.Policy
		}
		chains = append(chains, chain{c.Name, policy, kfTexts(c.Rules)})
	}
	find := func(name string) int { return slices.IndexFunc(chains, func(c chain) bool { return c.name == name }) }
	for _, line := range strings.Split(input, "\n") {
		line = strings.TrimSpace(line)
		fields := strings.Fields(line)
		switch {
		case line == "" || line == "*filter" || line == "COMMIT":
		case strings.HasPrefix(line, ":"):
			name := strings.TrimPrefix(fields[0], ":")
			if i := find(name); i >= 0 {
				chains[i].rules = nil
			} else {
				chains = append(chains, chain{name: name, policy: "-"})
			}
		case fields[0] == "-A" || fields[0] == "-F" || fields[0] == "-X":
			i := find(fields[1])
			if i < 0 {
				return nil, fmt.Errorf("iptables-restore: %q: No chain/target/match by that name", line)
			}
			switch fields[0] {
			case "-A":
				chains[i].rules = append(chains[i].rules, line)
			case "-F":
				chains[i].rules = nil
			case "-X":
				chains = slices.Delete(chains, i, i+1)
			}
		default:
			return nil, fmt.Errorf("iptables-restore: unexpected line %q", line)
		}
	}
	var b strings.Builder
	b.WriteString("*filter\n")
	for _, c := range chains {
		fmt.Fprintf(&b, ":%s %s [0:0]\n", c.name, c.policy)
	}
	for _, c := range chains {
		for _, r := range c.rules {
			if j := slices.Index(strings.Fields(r), "-j"); j >= 0 {
				target := strings.Fields(r)[j+1]
				if !slices.Contains([]string{"ACCEPT", "DROP", "RETURN", "REJECT", "LOG"}, target) && find(target) < 0 {
					return nil, fmt.Errorf("iptables-restore: rule %q jumps to %s, which does not exist", r, target)
				}
			}
			b.WriteString(r + "\n")
		}
	}
	b.WriteString("COMMIT\n")
	return netfilter.Parse(strings.NewReader(b.String()))
}

// kfLoad is one *-restore -n run.
type kfLoad struct{ Path, Input string }

// kfRig is a fake host with UFW active, the Docker block in after.rules
// and after6.rules, and the block's chains loaded in both families. Its
// *-restore programs change the loaded state as iptables-restore would;
// every run is in loads. The env is that of a change (it has the journal);
// standalone clears Effects.
type kfRig struct {
	h     *fakeHost
	f     fw
	env   core.Env
	loads []kfLoad
	log   *bytes.Buffer
}

// kfHost builds a kfRig whose loaded DOCKER-USER chains hold the lines v4
// and v6; nil is the block's own lines.
func kfHost(t *testing.T, v4, v6 []string) *kfRig {
	t.Helper()
	h := newFakeHost(t)
	h.ufw["reload"] = func(*fhState) int { return 0 }
	for f, path := range map[fwstate.Family]string{fwstate.V4: "/etc/ufw/after.rules", fwstate.V6: "/etc/ufw/after6.rules"} {
		file, err := dockerfw.WithBlock([]byte("*filter\n# after\nCOMMIT\n"), dockerfw.Render(kfInput(), f))
		if err != nil {
			t.Fatal(err)
		}
		h.writeFile(path, file)
	}
	if v4 == nil {
		v4 = kfLines(t, fwstate.V4)
	}
	if v6 == nil {
		v6 = kfLines(t, fwstate.V6)
	}
	h.loaded = fwstate.Loaded{Variant: "nft", V4: fhParse(t, kfSave(t, fwstate.V4, v4)), V6: fhParse(t, kfSave(t, fwstate.V6, v6))}
	r := &kfRig{h: h, log: &bytes.Buffer{}}
	h.onRun = func(h *fakeHost, cmd core.Cmd) core.CmdResult {
		target := &h.loaded.V4
		switch cmd.Path {
		case "/usr/sbin/iptables-nft-restore":
		case "/usr/sbin/ip6tables-nft-restore":
			target = &h.loaded.V6
		default:
			return core.CmdResult{ExitCode: 127, Stderr: []byte("kf: unexpected program " + cmd.Path)}
		}
		if !slices.Equal(cmd.Args, []string{"-n"}) {
			return core.CmdResult{ExitCode: 2, Stderr: []byte("kf: unexpected arguments")}
		}
		r.loads = append(r.loads, kfLoad{cmd.Path, string(cmd.Stdin)})
		next, err := kfApply(*target, string(cmd.Stdin))
		if err != nil {
			return core.CmdResult{ExitCode: 2, Stderr: []byte(err.Error())}
		}
		*target = next
		return core.CmdResult{}
	}
	r.f, _ = newFW(h)
	r.env = h.env()
	r.env.Log = slog.New(slog.NewTextHandler(r.log, nil))
	return r
}

// standalone makes the env that of a call outside any change.
func (r *kfRig) standalone() { r.env.Effects, r.env.ChangeID = nil, "" } //nolint:unused // shared helper for the tests of calls outside a change in this package

// known are after.rules and after6.rules as they are on disk now.
func (r *kfRig) known() [][]byte {
	files := r.h.config().Files
	return [][]byte{files["/etc/ufw/after.rules"].Data, files["/etc/ufw/after6.rules"].Data}
}

// flush empties DOCKER-USER of family f to lines, as a transition that
// declares the chain does.
func (r *kfRig) flush(t *testing.T, f fwstate.Family, lines []string) {
	t.Helper()
	input := "*filter\n:DOCKER-USER - [0:0]\n" + strings.Join(lines, "\n") + "\nCOMMIT\n"
	target := &r.h.loaded.V4
	if f == fwstate.V6 {
		target = &r.h.loaded.V6
	}
	next, err := kfApply(*target, input)
	if err != nil {
		t.Fatal(err)
	}
	*target = next
}

func (r *kfRig) loadedLines(f fwstate.Family) []string {
	if f == fwstate.V6 {
		return kfChainLines(r.h.loaded.V6)
	}
	return kfChainLines(r.h.loaded.V4)
}

// kfTransition is a transition that notes it ran in the host's calls and
// flushes both DOCKER-USER chains to their block lines, which is what the
// declaration of DOCKER-USER in the files does.
func kfTransition(t *testing.T, r *kfRig) func() error {
	return func() error {
		r.h.calls = append(r.h.calls, "transition")
		r.flush(t, fwstate.V4, kfLines(t, fwstate.V4))
		r.flush(t, fwstate.V6, kfLines(t, fwstate.V6))
		return nil
	}
}

func kfNext(t *testing.T) map[fwstate.Family][]string {
	return map[fwstate.Family][]string{fwstate.V4: kfLines(t, fwstate.V4), fwstate.V6: kfLines(t, fwstate.V6)}
}

// kfJoin concatenates line lists into a new slice.
func kfJoin(parts ...[]string) []string {
	var out []string
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func kfOne(s string) []string { return []string{s} }

func kfRecordHeld(t *testing.T, r *kfRig, owner string) bool {
	t.Helper()
	_, ok, err := foreignOf(context.Background(), r.h.db, owner)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

// TestKfForeignStore: a record round-trips through the table, replaces the
// owner's earlier one, stays apart from another owner's, and deleting a
// missing record is not an error.
func TestKfForeignStore(t *testing.T) {
	ctx := context.Background()
	db := dsDB(t)
	at := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	if _, ok, err := foreignOf(ctx, db, "c1"); err != nil || ok {
		t.Fatalf("foreignOf of nothing = %v, %v, want not found", ok, err)
	}
	rec := foreignRecord{
		Layouts: map[fwstate.Family]dockerfw.Layout{
			fwstate.V4: {Before: kfOne(kfForeign1), After: kfOne(kfForeign2)},
			fwstate.V6: {Interleaved: true, Before: kfOne("-A DOCKER-USER -s 2001:db8::/32 -j DROP"), Order: kfOne("-A DOCKER-USER -j RETURN")},
		},
		Block: map[fwstate.Family][]string{fwstate.V4: kfOne(kfReturn), fwstate.V6: nil},
	}
	if err := putForeign(ctx, db, "c1", rec, at); err != nil {
		t.Fatal(err)
	}
	got, ok, err := foreignOf(ctx, db, "c1")
	if err != nil || !ok || !reflect.DeepEqual(got, rec) {
		t.Fatalf("foreignOf = %+v, %v, %v, want %+v", got, ok, err, rec)
	}
	other := foreignRecord{Layouts: map[fwstate.Family]dockerfw.Layout{fwstate.V4: {After: kfOne(kfForeign2)}}}
	if err := putForeign(ctx, db, "uninstall", other, at); err != nil {
		t.Fatal(err)
	}
	replaced := foreignRecord{Layouts: map[fwstate.Family]dockerfw.Layout{fwstate.V4: {Before: kfOne(kfForeign2)}}}
	if err := putForeign(ctx, db, "c1", replaced, at.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := foreignOf(ctx, db, "c1"); !reflect.DeepEqual(got, replaced) {
		t.Errorf("after a second put foreignOf = %+v, want %+v", got, replaced)
	}
	if got, _, _ := foreignOf(ctx, db, "uninstall"); !reflect.DeepEqual(got, other) {
		t.Errorf("another owner's record = %+v, want %+v", got, other)
	}
	for i := 0; i < 2; i++ {
		if err := deleteForeign(ctx, db, "c1"); err != nil {
			t.Fatalf("deleteForeign #%d = %v", i+1, err)
		}
	}
	if _, ok, _ := foreignOf(ctx, db, "c1"); ok {
		t.Error("the record is still there after deleteForeign")
	}
	if _, ok, _ := foreignOf(ctx, db, "uninstall"); !ok {
		t.Error("deleteForeign removed another owner's record")
	}
}

// TestKfKeepForeign: a transition that flushes DOCKER-USER is followed by a
// reload of each family's chain with the new lines and the foreign rules in
// their places, whatever the layout; the record is gone afterwards.
func TestKfKeepForeign(t *testing.T) {
	ctx := context.Background()
	b4 := kfLines(t, fwstate.V4)
	for _, c := range []struct {
		name   string
		loaded []string
		toOff  bool // the restored state has no block loaded
		want   []string
	}{
		{name: "before the block", loaded: kfJoin(kfOne(kfForeign1), b4), want: kfJoin(kfOne(kfForeign1), b4)},
		{name: "after the block", loaded: kfJoin(b4, kfOne(kfForeign1)), want: kfJoin(b4, kfOne(kfForeign1))},
		{name: "on both sides", loaded: kfJoin(kfOne(kfForeign1), b4, kfOne(kfForeign2)), want: kfJoin(kfOne(kfForeign1), b4, kfOne(kfForeign2))},
		{name: "none", loaded: b4, want: b4},
		{name: "integration off keeps them beside the return", loaded: kfJoin(kfOne(kfForeign1), b4, kfOne(kfForeign2)), toOff: true,
			want: kfJoin(kfOne(kfForeign1), kfOne(kfReturn), kfOne(kfForeign2))},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := kfHost(t, c.loaded, nil)
			next := kfNext(t)
			if c.toOff {
				next = nil
			}
			il, err := r.f.keepForeign(ctx, r.env, "c1", r.known(), next, kfTransition(t, r))
			if err != nil || il {
				t.Fatalf("keepForeign = %v, %v, want false, nil", il, err)
			}
			if got := r.loadedLines(fwstate.V4); !slices.Equal(got, c.want) {
				t.Errorf("IPv4 DOCKER-USER = %q, want %q", got, c.want)
			}
			wantV6 := kfLines(t, fwstate.V6)
			if c.toOff {
				wantV6 = kfOne(kfReturn)
			}
			if got := r.loadedLines(fwstate.V6); !slices.Equal(got, wantV6) {
				t.Errorf("IPv6 DOCKER-USER = %q, want %q", got, wantV6)
			}
			var ran []string
			for _, call := range r.h.calls {
				if strings.HasPrefix(call, "run ") || call == "transition" {
					ran = append(ran, call)
				}
			}
			wantRan := []string{"transition", "run /usr/sbin/iptables-nft-restore -n", "run /usr/sbin/ip6tables-nft-restore -n"}
			if !slices.Equal(ran, wantRan) {
				t.Errorf("ran %q, want %q: the reload comes after the transition, IPv4 first", ran, wantRan)
			}
			if kfRecordHeld(t, r, "c1") {
				t.Error("the record is still there after a complete reload")
			}
		})
	}
}

// TestKfKeepForeignInterleaved: foreign rules between block lines are
// loaded before the block, the call reports it, and the original order is
// in the log.
func TestKfKeepForeignInterleaved(t *testing.T) {
	ctx := context.Background()
	b4 := kfLines(t, fwstate.V4)
	r := kfHost(t, kfJoin(b4[:3], kfOne(kfForeign1), b4[3:]), nil)
	il, err := r.f.keepForeign(ctx, r.env, "c1", r.known(), kfNext(t), kfTransition(t, r))
	if err != nil || !il {
		t.Fatalf("keepForeign = %v, %v, want true, nil", il, err)
	}
	if got, want := r.loadedLines(fwstate.V4), kfJoin(kfOne(kfForeign1), b4); !slices.Equal(got, want) {
		t.Errorf("IPv4 DOCKER-USER = %q, want the foreign rule before the block: %q", got, want)
	}
	if log := r.log.String(); !strings.Contains(log, "192.0.2.0/24") || !strings.Contains(log, "between the block's lines") {
		t.Errorf("log %q does not show the original order", log)
	}
	if kfRecordHeld(t, r, "c1") {
		t.Error("the record is still there")
	}
}

// TestKfKeepForeignNoChain: a host whose DOCKER-USER chain is not loaded
// has nothing to keep, so nothing is loaded after the transition.
func TestKfKeepForeignNoChain(t *testing.T) {
	ctx := context.Background()
	r := kfHost(t, nil, nil)
	r.h.loaded = fwstate.Loaded{Variant: "nft", V4: fhParse(t, fhSave), V6: fhParse(t, fhSave)}
	ran := false
	il, err := r.f.keepForeign(ctx, r.env, "c1", r.known(), kfNext(t), func() error { ran = true; return nil })
	if err != nil || il || !ran {
		t.Fatalf("keepForeign = %v, %v, transition ran %v, want false, nil, true", il, err, ran)
	}
	if len(r.loads) != 0 {
		t.Errorf("loaded %+v, want no restore", r.loads)
	}
	if kfRecordHeld(t, r, "c1") {
		t.Error("a record is held for a host without DOCKER-USER")
	}
}

// TestKfKeepForeignBrokenMarkers: a known file whose markers are broken is
// an error before anything is recorded or run.
func TestKfKeepForeignBrokenMarkers(t *testing.T) {
	ctx := context.Background()
	r := kfHost(t, nil, nil)
	bad := []byte("# BEGIN DARKPANEL docker\n# BEGIN DARKPANEL docker\n# END DARKPANEL docker\n")
	ran := false
	_, err := r.f.keepForeign(ctx, r.env, "c1", [][]byte{bad}, kfNext(t), func() error { ran = true; return nil })
	if err == nil || ran || len(r.loads) != 0 || kfRecordHeld(t, r, "c1") {
		t.Errorf("keepForeign = %v, transition ran %v, loads %d, record %v, want an error and nothing done",
			err, ran, len(r.loads), kfRecordHeld(t, r, "c1"))
	}
}

// TestKfKeepForeignTransitionFails: a failed transition returns its error,
// loads nothing and keeps the record, so that the next attempt (or
// finishForeign) can give the rules back after the chain was flushed.
func TestKfKeepForeignTransitionFails(t *testing.T) {
	ctx := context.Background()
	b4 := kfLines(t, fwstate.V4)
	r := kfHost(t, kfJoin(kfOne(kfForeign1), b4), nil)
	boom := errors.New("ufw reload failed")
	il, err := r.f.keepForeign(ctx, r.env, "c1", r.known(), kfNext(t), func() error {
		r.flush(t, fwstate.V4, b4)
		return boom
	})
	if !errors.Is(err, boom) || il || len(r.loads) != 0 {
		t.Fatalf("keepForeign = %v, %v with %d loads, want the transition's error and no load", il, err, len(r.loads))
	}
	rec, ok, err := foreignOf(ctx, r.h.db, "c1")
	if err != nil || !ok || !slices.Equal(rec.Layouts[fwstate.V4].Before, kfOne(kfForeign1)) {
		t.Fatalf("record = %+v, %v, %v, want the rule before the block", rec, ok, err)
	}
	if il, err := r.f.finishForeign(ctx, r.env, "c1"); err != nil || il {
		t.Fatalf("finishForeign = %v, %v", il, err)
	}
	if got, want := r.loadedLines(fwstate.V4), kfJoin(kfOne(kfForeign1), b4); !slices.Equal(got, want) {
		t.Errorf("IPv4 DOCKER-USER after finishForeign = %q, want %q", got, want)
	}
	if kfRecordHeld(t, r, "c1") {
		t.Error("finishForeign left the record")
	}
}

// TestKfKeepForeignRetry: a second attempt with the earlier record keeps
// the rules the first attempt's flush removed, adds the ones that arrived
// since, and does not repeat a rule the chain still holds.
func TestKfKeepForeignRetry(t *testing.T) {
	ctx := context.Background()
	b4 := kfLines(t, fwstate.V4)
	for _, c := range []struct {
		name  string
		chain []string // DOCKER-USER when the second attempt starts
		want  []string
	}{
		{name: "flushed", chain: b4, want: kfJoin(kfOne(kfForeign1), b4)},
		{name: "a rule arrived after the flush", chain: kfJoin(b4, kfOne(kfForeign2)), want: kfJoin(kfOne(kfForeign1), b4, kfOne(kfForeign2))},
		{name: "not flushed", chain: kfJoin(kfOne(kfForeign1), b4), want: kfJoin(kfOne(kfForeign1), b4)},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := kfHost(t, kfJoin(kfOne(kfForeign1), b4), nil)
			if _, err := r.f.keepForeign(ctx, r.env, "c1", r.known(), kfNext(t), func() error { return errors.New("cut off") }); err == nil {
				t.Fatal("the first attempt succeeded")
			}
			r.flush(t, fwstate.V4, c.chain)
			if il, err := r.f.keepForeign(ctx, r.env, "c1", r.known(), kfNext(t), kfTransition(t, r)); err != nil || il {
				t.Fatalf("second keepForeign = %v, %v", il, err)
			}
			if got := r.loadedLines(fwstate.V4); !slices.Equal(got, c.want) {
				t.Errorf("IPv4 DOCKER-USER = %q, want %q", got, c.want)
			}
		})
	}
}

// TestKfKeepForeignLoadFails: a failed load of one family is an error that
// keeps the record, and finishForeign then completes it, reloading each
// family and clearing the record.
func TestKfKeepForeignLoadFails(t *testing.T) {
	ctx := context.Background()
	b4 := kfLines(t, fwstate.V4)
	r := kfHost(t, kfJoin(kfOne(kfForeign1), b4), nil)
	inner, broken := r.h.onRun, true
	r.h.onRun = func(h *fakeHost, cmd core.Cmd) core.CmdResult {
		if broken && cmd.Path == "/usr/sbin/ip6tables-nft-restore" {
			return core.CmdResult{ExitCode: 1, Stderr: []byte("ip6tables-restore: out of memory")}
		}
		return inner(h, cmd)
	}
	_, err := r.f.keepForeign(ctx, r.env, "c1", r.known(), kfNext(t), kfTransition(t, r))
	if err == nil || !strings.Contains(err.Error(), "ip6tables-nft-restore") || !kfRecordHeld(t, r, "c1") {
		t.Fatalf("keepForeign = %v, record held %v, want an error naming ip6tables-nft-restore and the record kept", err, kfRecordHeld(t, r, "c1"))
	}
	broken = false
	r.flush(t, fwstate.V4, b4)
	if il, err := r.f.finishForeign(ctx, r.env, "c1"); err != nil || il {
		t.Fatalf("finishForeign = %v, %v", il, err)
	}
	if got, want := r.loadedLines(fwstate.V4), kfJoin(kfOne(kfForeign1), b4); !slices.Equal(got, want) {
		t.Errorf("IPv4 DOCKER-USER = %q, want %q", got, want)
	}
	if kfRecordHeld(t, r, "c1") {
		t.Error("the record is still there")
	}
}

// TestKfFinishForeign: with no record finishForeign runs nothing; with an
// interleaved one it loads the foreign rules first and says so.
func TestKfFinishForeign(t *testing.T) {
	ctx := context.Background()
	r := kfHost(t, nil, nil)
	if il, err := r.f.finishForeign(ctx, r.env, "c1"); err != nil || il || len(r.loads) != 0 {
		t.Fatalf("finishForeign without a record = %v, %v with %d loads, want false, nil, 0", il, err, len(r.loads))
	}
	rec := foreignRecord{Layouts: map[fwstate.Family]dockerfw.Layout{
		fwstate.V4: {Interleaved: true, Before: kfOne(kfForeign1), Order: kfJoin(kfOne(kfReturn), kfOne(kfForeign1))},
	}}
	if err := putForeign(ctx, r.h.db, "c1", rec, r.h.clock.Now()); err != nil {
		t.Fatal(err)
	}
	il, err := r.f.finishForeign(ctx, r.env, "c1")
	if err != nil || !il {
		t.Fatalf("finishForeign = %v, %v, want true, nil", il, err)
	}
	if got, want := r.loadedLines(fwstate.V4), kfJoin(kfOne(kfForeign1), kfOne(kfReturn)); !slices.Equal(got, want) {
		t.Errorf("IPv4 DOCKER-USER = %q, want %q", got, want)
	}
	if len(r.loads) != 1 || r.loads[0].Path != "/usr/sbin/iptables-nft-restore" {
		t.Errorf("loads %+v, want one IPv4 load: the record has no IPv6 layout", r.loads)
	}
	if kfRecordHeld(t, r, "c1") {
		t.Error("the record is still there")
	}
}
