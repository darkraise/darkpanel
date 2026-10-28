package firewall

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
)

// rsTestSnap captures a snapshot of h and reads it back.
func rsTestSnap(t *testing.T, h *fakeHost, withImage bool) snapshot {
	t.Helper()
	raw, err := capture(context.Background(), h.env(), h.deps(), nil, withImage)
	if err != nil {
		t.Fatal(err)
	}
	s, err := getSnapshot(context.Background(), h.db, string(raw))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func rsTestRuns(h *fakeHost) []string {
	var out []string
	for _, c := range h.calls {
		if strings.HasPrefix(c, "run ") {
			out = append(out, c)
		}
	}
	return out
}

func TestChooseTransition(t *testing.T) {
	for _, c := range []struct {
		snap, cur bool
		want      fwstate.Transition
	}{
		{true, true, fwstate.TransReload},
		{true, false, fwstate.TransEnable},
		{false, true, fwstate.TransDisable},
		{false, false, fwstate.TransNone},
	} {
		if got := chooseTransition(c.snap, c.cur); got != c.want {
			t.Errorf("chooseTransition(%v, %v) = %s, want %s", c.snap, c.cur, got, c.want)
		}
	}
}

func TestRestoreWholeSnapshot(t *testing.T) {
	ctx := context.Background()
	h := newFakeHost(t)
	snap := rsTestSnap(t, h, false)
	s := h.state()
	s.Rules4, s.LogLevel = s.Rules4[:1], "high"
	h.setState(s)
	if err := os.Remove(filepath.Join(h.root, "/etc/ufw/after6.rules")); err != nil {
		t.Fatal(err)
	}
	h.sysctl["net/ipv4/ip_forward"] = "1"
	h.sysctl["net/ipv4/conf/eth0/rp_filter"] = "0"
	h.sysctl["net/ipv4/conf/all/rp_filter"] = "0"
	delete(h.sysctl, "net/ipv6/conf/eth0/accept_ra")
	h.ufw["reload"] = func(*fhState) int { return 0 }
	h.calls = nil
	rep, err := restore(ctx, h.env(), h.deps(), snap)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Transition != fwstate.TransReload || !reflect.DeepEqual(rep.Vanished, []string{"net/ipv6/conf/eth0/accept_ra"}) || rep.Excluded != nil {
		t.Errorf("report = %+v", rep)
	}
	var want []string
	for _, e := range snap.Entries {
		want = append(want, "restore "+e.Path)
	}
	want = append(want, "run /usr/sbin/ufw reload",
		"sysctl net/ipv4/ip_forward=0", "sysctl net/ipv4/conf/all/rp_filter=2", "sysctl net/ipv6/conf/all/forwarding=0",
		"sysctl net/ipv4/conf/default/rp_filter=1", "sysctl net/ipv4/conf/eth0/rp_filter=1")
	if !reflect.DeepEqual(h.calls, want) {
		t.Errorf("calls =\n%s\nwant\n%s", strings.Join(h.calls, "\n"), strings.Join(want, "\n"))
	}
	if got := fileHashes(h.config().Files); !reflect.DeepEqual(got, snap.Hashes) {
		t.Errorf("files after the restore = %v, want %v", got, snap.Hashes)
	}
	if _, ok, _ := pendingRestore(ctx, h.db); ok {
		t.Error("a finished restore left its record")
	}
	keep := h.keeps[0]
	for _, c := range []struct {
		table, chain string
		want         bool
	}{{"filter", "ufw-user-input", true}, {"filter", "INPUT", true}, {"filter", "DOCKER", false}, {"nat", "INPUT", false}, {"nat", "darkpanel-nat", true}} {
		if got := keep(fwstate.V4, c.table, c.chain); got != c.want {
			t.Errorf("restore verifies %s/%s = %v, want %v", c.table, c.chain, got, c.want)
		}
	}
}

func TestRestoreChoosesTheTransition(t *testing.T) {
	for _, c := range []struct {
		name          string
		snapEnabled   bool
		curEnabled    bool
		breakConf     bool
		wantRuns      []string
		wantRecording fwstate.Transition
	}{
		{"enable", true, false, false, []string{"run /usr/sbin/ufw --force enable"}, fwstate.TransEnable},
		{"disable", false, true, false, []string{"run /usr/sbin/ufw disable"}, fwstate.TransDisable},
		{"none", false, false, false, nil, fwstate.TransNone},
		{"unreadable, snapshot active", true, true, true, []string{"run /usr/sbin/ufw --force enable"}, fwstate.TransEnable},
		{"unreadable, snapshot inactive", false, false, true, []string{"run /usr/sbin/ufw disable"}, fwstate.TransDisable},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			h := newFakeHost(t)
			s := h.state()
			s.Enabled = c.snapEnabled
			h.setState(s)
			snap := rsTestSnap(t, h, false)
			s.Enabled = c.curEnabled
			h.setState(s)
			if c.breakConf {
				if err := os.Remove(filepath.Join(h.root, "/etc/ufw/ufw.conf")); err != nil {
					t.Fatal(err)
				}
			}
			h.ufw["--force enable"] = func(s *fhState) int { s.Enabled = true; return 0 }
			h.ufw["disable"] = func(s *fhState) int { s.Enabled = false; return 0 }
			h.calls = nil
			rep, err := restore(ctx, h.env(), h.deps(), snap)
			if err != nil {
				t.Fatal(err)
			}
			if rep.Transition != c.wantRecording {
				t.Errorf("transition = %s, want %s", rep.Transition, c.wantRecording)
			}
			if got := rsTestRuns(h); !reflect.DeepEqual(got, c.wantRuns) {
				t.Errorf("runs = %v, want %v", got, c.wantRuns)
			}
		})
	}
}

func TestRestoreFinishesARecordedRestore(t *testing.T) {
	ctx := context.Background()
	h := newFakeHost(t)
	s := h.state()
	s.Enabled = false
	h.setState(s)
	snap := rsTestSnap(t, h, false)
	// A first run recorded "disable" and wrote the files, then the process
	// died before ufw ran: ufw.conf already says disabled, UFW still runs.
	if err := beginRestore(ctx, h.db, snap.ID, fwstate.TransDisable, h.clock.Now()); err != nil {
		t.Fatal(err)
	}
	h.ufw["disable"] = func(s *fhState) int { s.Enabled = false; return 0 }
	h.calls = nil
	rep, err := restore(ctx, h.env(), h.deps(), snap)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Transition != fwstate.TransDisable || !reflect.DeepEqual(rsTestRuns(h), []string{"run /usr/sbin/ufw disable"}) {
		t.Errorf("transition %s runs %v, want the recorded disable", rep.Transition, rsTestRuns(h))
	}
	if _, ok, _ := pendingRestore(ctx, h.db); ok {
		t.Error("the finished restore left its record")
	}
}

func TestRestoreFailures(t *testing.T) {
	for _, c := range []struct {
		name        string
		set         func(h *fakeHost)
		keepsIntent bool
	}{
		{"ufw fails", func(h *fakeHost) { h.ufw["reload"] = func(*fhState) int { return 1 } }, true},
		{"ufw refused", func(h *fakeHost) { h.runErr["/usr/sbin/ufw reload"] = errors.New("not allowed") }, true},
		{"loaded state unreadable", func(h *fakeHost) { h.loadErr = errors.New("save failed") }, true},
		{"enabled differs", func(h *fakeHost) {
			h.ufw["reload"] = func(s *fhState) int { s.Enabled = false; return 0 }
		}, true},
		{"chains differ", func(h *fakeHost) {
			h.diffs = []fwstate.ChainDiff{{Family: fwstate.V4, Table: "filter", Chain: "ufw-user-input"}}
		}, true},
		{"prediction fails", func(h *fakeHost) { h.predictErr = errors.New("unsupported") }, true},
		{"sysctl write fails", func(h *fakeHost) { h.sysctlFail["net/ipv4/ip_forward"] = errors.New("read-only") }, true},
		{"sysctl reads back wrong", func(h *fakeHost) { h.sysctlLie["net/ipv4/conf/eth0/rp_filter"] = "2" }, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			h := newFakeHost(t)
			snap := rsTestSnap(t, h, false)
			h.ufw["reload"] = func(*fhState) int { return 0 }
			c.set(h)
			if _, err := restore(ctx, h.env(), h.deps(), snap); err == nil {
				t.Fatal("restore succeeded")
			}
			if _, ok, _ := pendingRestore(ctx, h.db); ok != c.keepsIntent {
				t.Errorf("record kept = %v, want %v", ok, c.keepsIntent)
			}
		})
	}
}

// rsTestSave4 is fhSave with a foreign chain jumped to from INPUT.
const rsTestSave4 = "*filter\n:INPUT DROP [0:0]\n:FORWARD DROP [0:0]\n:OUTPUT ACCEPT [0:0]\n:fw-foreign - [0:0]\n" +
	":ufw-user-input - [0:0]\n-A INPUT -j fw-foreign\n-A INPUT -j ufw-user-input\n" +
	"-A ufw-user-input -p tcp -m tcp --dport 22 -j ACCEPT\nCOMMIT\n"

func TestRestoreLoadsABeforeImage(t *testing.T) {
	ctx := context.Background()
	h := newFakeHost(t)
	h.loaded.V4 = fhParse(t, rsTestSave4)
	snap := rsTestSnap(t, h, true)
	imaged := h.loaded
	h.loaded.V4 = fhParse(t, strings.Replace(rsTestSave4, "--dport 22", "--dport 2222", 1))
	h.loaded.V6 = fhParse(t, strings.Replace(fhSave, ":INPUT DROP", ":INPUT ACCEPT", 1))
	want := planImage(snap.State.Image, h.loaded)
	h.onRun = func(h *fakeHost, cmd core.Cmd) core.CmdResult {
		if cmd.Path == "/usr/sbin/ip6tables-nft-restore" {
			h.loaded = imaged
		}
		return core.CmdResult{}
	}
	h.calls = nil
	rep, err := restore(ctx, h.env(), h.deps(), snap)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Transition != fwstate.TransNone || rep.Excluded != nil {
		t.Errorf("report = %+v", rep)
	}
	if got := rsTestRuns(h); !reflect.DeepEqual(got, []string{"run /usr/sbin/iptables-nft-restore -n", "run /usr/sbin/ip6tables-nft-restore -n"}) {
		t.Errorf("runs = %v", got)
	}
	if string(h.stdin["/usr/sbin/iptables-nft-restore"]) != string(want.Restore4) || string(h.stdin["/usr/sbin/ip6tables-nft-restore"]) != string(want.Restore6) {
		t.Errorf("restore input =\n%s\n%s", h.stdin["/usr/sbin/iptables-nft-restore"], h.stdin["/usr/sbin/ip6tables-nft-restore"])
	}
	const literal4 = "*filter\n:INPUT DROP [0:0]\n:FORWARD DROP [0:0]\n:OUTPUT ACCEPT [0:0]\n:ufw-user-input - [0:0]\n" +
		"-F INPUT\n-F FORWARD\n-F OUTPUT\n-A INPUT -j fw-foreign\n-A INPUT -j ufw-user-input\n" +
		"-A ufw-user-input -p tcp -m tcp --dport 22 -j ACCEPT\nCOMMIT\n"
	if got := string(h.stdin["/usr/sbin/iptables-nft-restore"]); got != literal4 {
		t.Errorf("IPv4 restore input =\n%s\nwant\n%s", got, literal4)
	}
	if len(h.keeps) != 0 {
		t.Error("a before-image restore compared chains with the prediction")
	}
}

func TestRestoreLeavesAChangedBuiltinChain(t *testing.T) {
	ctx := context.Background()
	h := newFakeHost(t)
	h.loaded.V4 = fhParse(t, rsTestSave4)
	snap := rsTestSnap(t, h, true)
	changed := strings.Replace(rsTestSave4, "-A INPUT -j ufw-user-input\n", "-A INPUT -j ufw-user-input\n-A INPUT -s 192.0.2.9/32 -j DROP\n", 1)
	h.loaded.V4 = fhParse(t, changed)
	h.calls = nil
	rep, err := restore(ctx, h.env(), h.deps(), snap)
	if err == nil || !strings.Contains(err.Error(), "v4 filter/INPUT") {
		t.Fatalf("restore = %v, want an error naming v4 filter/INPUT", err)
	}
	if !reflect.DeepEqual(rep.Excluded, []chainRef{{fwstate.V4, "filter", "INPUT"}}) {
		t.Errorf("excluded = %v", rep.Excluded)
	}
	if strings.Contains(string(h.stdin["/usr/sbin/iptables-nft-restore"]), "-F INPUT") {
		t.Error("the restore input flushes the changed INPUT chain")
	}
	if _, ok, _ := pendingRestore(ctx, h.db); ok {
		t.Error("a restore that did what it could left its record")
	}
}

func TestRestoreBeforeImageFails(t *testing.T) {
	ctx := context.Background()
	h := newFakeHost(t)
	snap := rsTestSnap(t, h, true)
	h.onRun = func(*fakeHost, core.Cmd) core.CmdResult {
		return core.CmdResult{ExitCode: 2, Stderr: []byte("line 3 failed")}
	}
	if _, err := restore(ctx, h.env(), h.deps(), snap); err == nil || !strings.Contains(err.Error(), "line 3 failed") {
		t.Errorf("restore = %v, want the restore program's failure", err)
	}
	h.onRun = nil
	h.loaded.V4 = fhParse(t, strings.Replace(fhSave, "--dport 22", "--dport 23", 1))
	if _, err := restore(ctx, h.env(), h.deps(), snap); err == nil || !strings.Contains(err.Error(), "differ from the before-image") {
		t.Errorf("restore = %v, want a mismatch with the before-image", err)
	}
}

func TestWriteBackSysctlOrder(t *testing.T) {
	ctx := context.Background()
	h := newFakeHost(t)
	want := map[string]string{
		"net/ipv6/conf/eth0/accept_ra": "2", "net/ipv4/conf/default/rp_filter": "0", "net/ipv4/ip_forward": "1",
		"net/ipv4/conf/all/rp_filter": "1", "net/ipv4/conf/eth1/rp_filter": "1", "net/ipv6/conf/all/forwarding": "1",
	}
	vanished, err := writeBackSysctl(ctx, h.env(), want, h.sysctl)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(vanished, []string{"net/ipv4/conf/eth1/rp_filter"}) {
		t.Errorf("vanished = %v", vanished)
	}
	wantCalls := []string{"sysctl net/ipv4/ip_forward=1", "sysctl net/ipv4/conf/all/rp_filter=1", "sysctl net/ipv6/conf/all/forwarding=1",
		"sysctl net/ipv4/conf/default/rp_filter=0", "sysctl net/ipv6/conf/eth0/accept_ra=2"}
	if !reflect.DeepEqual(h.calls, wantCalls) {
		t.Errorf("calls = %v, want %v", h.calls, wantCalls)
	}
	if slices.Contains(h.calls, "sysctl net/ipv4/conf/eth1/rp_filter=1") {
		t.Error("a vanished interface's key was written")
	}
}

// TestRestoreWithoutAnAllKey: a host rebooted with ipv6.disable=1 has no
// net/ipv6/conf/all/forwarding, so a restore of a snapshot that holds it
// reports the key instead of failing on it every time.
func TestRestoreWithoutAnAllKey(t *testing.T) {
	ctx := context.Background()
	h := newFakeHost(t)
	snap := rsTestSnap(t, h, false)
	if _, ok := snap.State.Sysctl["net/ipv6/conf/all/forwarding"]; !ok {
		t.Fatal("the snapshot holds no net/ipv6/conf/all/forwarding")
	}
	delete(h.sysctl, "net/ipv6/conf/all/forwarding")
	h.ufw["reload"] = func(*fhState) int { return 0 }
	rep, err := restore(ctx, h.env(), h.deps(), snap)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(rep.Vanished, "net/ipv6/conf/all/forwarding") {
		t.Errorf("vanished = %v, want the all key", rep.Vanished)
	}
	if _, ok, _ := pendingRestore(ctx, h.db); ok {
		t.Error("the restore left its record")
	}
}

func TestRestoreRefusesIPv6NoOverForeignRules(t *testing.T) {
	ctx := context.Background()
	h := newFakeHost(t)
	s := h.state()
	s.IPv6 = false
	h.setState(s)
	snap := rsTestSnap(t, h, false)
	s.IPv6 = true
	h.setState(s)
	h.loaded.V6 = fhParse(t, rsTestSave4)
	h.ufw["reload"] = func(*fhState) int { return 0 }
	h.calls = nil
	_, err := restore(ctx, h.env(), h.deps(), snap)
	var ec *errcode.Error
	if !errors.As(err, &ec) || ec.Code != errcode.FirewallUnsupportedConfiguration || !strings.Contains(ec.Detail, "chain fw-foreign") {
		t.Fatalf("restore = %v, want firewall.unsupported_configuration naming chain fw-foreign", err)
	}
	if !h.config().IPv6 {
		t.Error("a refused restore wrote the snapshot's IPV6=no")
	}
	if got := rsTestRuns(h); got != nil {
		t.Errorf("runs = %v, want none", got)
	}
	if _, ok, _ := pendingRestore(ctx, h.db); !ok {
		t.Error("a refused restore cleared its record")
	}
}

func TestAxRestoreAfterAnotherInterruptedRestore(t *testing.T) {
	const stopped = "*filter\n:INPUT DROP [0:0]\n:FORWARD DROP [0:0]\n:OUTPUT ACCEPT [0:0]\nCOMMIT\n"
	for _, c := range []struct {
		name       string
		other      bool // a restore of another snapshot was cut off
		confEnable bool // ufw.conf says ENABLED=yes when the restore starts
		running    bool // UFW's chains are loaded
		snapActive bool
		want       []string
	}{
		{"cut off before ufw stopped", true, false, true, true, []string{"run /usr/sbin/ufw reload"}},
		{"cut off before ufw started", true, true, false, false, nil},
		{"cut off while stopped, snapshot active", true, false, false, true, []string{"run /usr/sbin/ufw --force enable"}},
		{"no record: the configuration says", false, false, true, true, []string{"run /usr/sbin/ufw --force enable"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			h := newFakeHost(t)
			s := h.state()
			s.Enabled = c.snapActive
			h.setState(s)
			snap := rsTestSnap(t, h, false)
			other := rsTestSnap(t, h, false)
			if c.other {
				if err := beginRestore(ctx, h.db, other.ID, fwstate.TransDisable, h.clock.Now()); err != nil {
					t.Fatal(err)
				}
			}
			s.Enabled = c.confEnable
			h.setState(s)
			if !c.running {
				h.loaded.V4 = fhParse(t, stopped)
				h.loaded.V6 = fhParse(t, stopped)
			}
			for _, args := range []string{"reload", "--force enable", "disable"} {
				h.ufw[args] = func(*fhState) int { return 0 }
			}
			h.calls = nil
			if _, err := restore(ctx, h.env(), h.deps(), snap); err != nil {
				t.Fatal(err)
			}
			if got := rsTestRuns(h); !reflect.DeepEqual(got, c.want) {
				t.Errorf("ufw ran %v, want %v", got, c.want)
			}
			if _, pending, _ := pendingRestore(ctx, h.db); pending {
				t.Error("the restore left a record")
			}
		})
	}
}

func TestAxRestoreOverUnreadableChains(t *testing.T) {
	ctx := context.Background()
	h := newFakeHost(t)
	s := h.state()
	s.Enabled = true
	h.setState(s)
	snap := rsTestSnap(t, h, false)
	other := rsTestSnap(t, h, false)
	if err := beginRestore(ctx, h.db, other.ID, fwstate.TransDisable, h.clock.Now()); err != nil {
		t.Fatal(err)
	}
	s.Enabled = false
	h.setState(s)
	for _, args := range []string{"reload", "--force enable", "disable"} {
		h.ufw[args] = func(*fhState) int { return 0 }
	}
	h.loadErr = errors.New("loaded: iptables-nft-save exited 1")
	h.calls = nil
	if _, err := restore(ctx, h.env(), h.deps(), snap); err == nil {
		t.Fatal("restore succeeded without the loaded chains")
	}
	if _, ok, _ := restoreRecordOf(ctx, h.db, snap.ID); ok {
		t.Fatal("a restore that could not read the loaded chains recorded its transition")
	}
	if got := rsTestRuns(h); got != nil {
		t.Errorf("ufw ran %v, want nothing", got)
	}
	h.loadErr = nil
	h.calls = nil
	if _, err := restore(ctx, h.env(), h.deps(), snap); err != nil {
		t.Fatal(err)
	}
	if got, want := rsTestRuns(h), []string{"run /usr/sbin/ufw reload"}; !reflect.DeepEqual(got, want) {
		t.Errorf("the retry ran %v, want %v", got, want)
	}
}
