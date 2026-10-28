package firewall

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/modules/firewall/loaded"
)

func vsEffect(resource string, state core.EffectState, post string) core.Effect {
	e := core.Effect{Resource: resource, State: state}
	if post != "" {
		e.Post = &core.StateDoc{State: json.RawMessage(post)}
	}
	return e
}

// vsBefore are the live values from before a change.
func vsBefore() map[string]string {
	return map[string]string{
		"net/ipv4/ip_forward":           "0",
		"net/ipv4/conf/all/rp_filter":   "1",
		"net/ipv4/conf/eth0/rp_filter":  "1",
		"net/ipv6/conf/all/forwarding":  "0",
		"net/ipv6/conf/eth0/forwarding": "0",
		"net/ipv6/conf/eth0/accept_ra":  "1",
	}
}

func vsConfig(settings ...fwstate.SysctlSetting) fwstate.Config {
	return fwstate.Config{Enabled: true, SysctlFile: "/etc/ufw/sysctl.conf", Sysctl: settings}
}

// TestSysctlRecordOf: a change started UFW when its last made effect on
// the enabled state left it enabled; it wrote the keys of its made sysctl
// effects, at the value each was observed to hold.
func TestSysctlRecordOf(t *testing.T) {
	enabled := "firewall.state:enabled"
	cases := []struct {
		name    string
		effects []core.Effect
		want    sysctlRecord
	}{
		{"none", nil, sysctlRecord{written: map[string]string{}}},
		{"enable then reload", []core.Effect{vsEffect(enabled, core.EffectMade, "true"), vsEffect(enabled, core.EffectMade, "true")},
			sysctlRecord{started: true, written: map[string]string{}}},
		{"disable", []core.Effect{vsEffect(enabled, core.EffectMade, "false")}, sysctlRecord{written: map[string]string{}}},
		{"skipped and intended effects do not count", []core.Effect{vsEffect(enabled, core.EffectSkipped, ""), vsEffect(enabled, core.EffectIntended, "")},
			sysctlRecord{written: map[string]string{}}},
		{"other resources", []core.Effect{vsEffect("firewall.default:incoming", core.EffectMade, `"DROP"`), vsEffect("firewall.file:/etc/ufw/user.rules", core.EffectMade, `"sha256:aa"`)},
			sysctlRecord{written: map[string]string{}}},
		{"sysctl writes", []core.Effect{vsEffect("firewall.sysctl:net/ipv4/ip_forward", core.EffectMade, `"1"`), vsEffect("firewall.sysctl:net/ipv4/conf/eth0/rp_filter", core.EffectMade, "null")},
			sysctlRecord{written: map[string]string{"net/ipv4/ip_forward": "1"}}},
	}
	for _, c := range cases {
		if got, err := sysctlRecordOf(c.effects); err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: record %+v, %v, want %+v", c.name, got, err, c.want)
		}
	}
	for name, e := range map[string]core.Effect{
		"enabled state not JSON": vsEffect(enabled, core.EffectMade, "tru"),
		"enabled state null":     vsEffect(enabled, core.EffectMade, "null"),
		"sysctl value not JSON":  vsEffect("firewall.sysctl:net/ipv4/ip_forward", core.EffectMade, `"1`),
	} {
		if _, err := sysctlRecordOf([]core.Effect{e}); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

// TestExpectedSysctl: a change that did not start UFW predicts nothing;
// one that did predicts every key UFW's sysctl file sets and every key the
// kernel changes as a side effect, not the keys the transition leaves
// alone; a value the change wrote itself replaces the prediction.
func TestExpectedSysctl(t *testing.T) {
	f := fw{d: deps{sysctl: loaded.PredictSysctl}}
	file := vsConfig(fwstate.SysctlSetting{Key: "net/ipv4/ip_forward", Value: "1"},
		fwstate.SysctlSetting{Key: "net/ipv4/conf/all/rp_filter", Value: "1"},
		fwstate.SysctlSetting{Key: "net/ipv6/conf/all/forwarding", Value: "1"},
		fwstate.SysctlSetting{Key: "net/ipv4/conf/lo/rp_filter", Value: "0"})

	want, err := f.expectedSysctl(file, vsBefore(), sysctlRecord{})
	if err != nil || len(want) != 0 {
		t.Errorf("a change that did not start UFW: %v, %v; want nothing", want, err)
	}
	want, err = f.expectedSysctl(file, vsBefore(), sysctlRecord{started: true})
	wantStarted := map[string]string{
		"net/ipv4/ip_forward":           "1",
		"net/ipv4/conf/all/rp_filter":   "1",
		"net/ipv6/conf/all/forwarding":  "1",
		"net/ipv6/conf/eth0/forwarding": "1",
	}
	if err != nil || !reflect.DeepEqual(want, wantStarted) {
		t.Errorf("a start: %v, %v; want %v", want, err, wantStarted)
	}
	want, err = f.expectedSysctl(file, vsBefore(), sysctlRecord{started: true, written: map[string]string{
		"net/ipv4/ip_forward": "0", "net/ipv4/conf/eth0/rp_filter": "2"}})
	wantStarted["net/ipv4/ip_forward"], wantStarted["net/ipv4/conf/eth0/rp_filter"] = "0", "2"
	if err != nil || !reflect.DeepEqual(want, wantStarted) {
		t.Errorf("a start with writes: %v, %v; want %v", want, err, wantStarted)
	}
	want, err = f.expectedSysctl(vsConfig(), vsBefore(), sysctlRecord{written: map[string]string{"net/ipv4/ip_forward": "1"}})
	if err != nil || !reflect.DeepEqual(want, map[string]string{"net/ipv4/ip_forward": "1"}) {
		t.Errorf("a write without a start: %v, %v; want the written value only", want, err)
	}
	bad := vsConfig(fwstate.SysctlSetting{Key: "net/ipv4/ip_forward", Value: "5"})
	if _, err := f.expectedSysctl(bad, vsBefore(), sysctlRecord{started: true}); errcode.From(err).Code != errcode.FirewallUnsupportedConfiguration {
		t.Errorf("a value the owned path refuses: %v, want unsupported configuration", err)
	}
}

// TestSysctlMismatches: a difference names the key, the predicted and the
// live value, sorted by key; a key the kernel no longer has is skipped.
func TestSysctlMismatches(t *testing.T) {
	want := map[string]string{"net/ipv4/ip_forward": "1", "net/ipv6/conf/eth0/forwarding": "1", "net/ipv4/conf/eth9/rp_filter": "1", "net/ipv4/conf/all/rp_filter": "1"}
	live := map[string]string{"net/ipv4/ip_forward": "0", "net/ipv6/conf/eth0/forwarding": "0", "net/ipv4/conf/all/rp_filter": "1"}
	got := sysctlMismatches(want, live)
	wantText := []string{`net/ipv4/ip_forward: live "0", predicted "1"`, `net/ipv6/conf/eth0/forwarding: live "0", predicted "1"`}
	if !reflect.DeepEqual(got, wantText) {
		t.Errorf("mismatches %q, want %q", got, wantText)
	}
	if got := sysctlMismatches(want, map[string]string{"net/ipv4/ip_forward": "1", "net/ipv6/conf/eth0/forwarding": "1", "net/ipv4/conf/all/rp_filter": "1"}); len(got) != 0 {
		t.Errorf("mismatches %q, want none", got)
	}
	if strings.Join(sysctlMismatches(nil, live), "") != "" {
		t.Error("nothing predicted must report nothing")
	}
}

// vsHost is a fake host whose ufw adds pnRule, whose sysctl file sets
// net/ipv4/ip_forward to 1 while the live value is 0, and whose sysctl
// prediction is part C's.
func vsHost(t *testing.T) (*fakeHost, fw) {
	t.Helper()
	h, f, _ := pnHost(t)
	h.writeFile("/etc/ufw/sysctl.conf", []byte("net/ipv4/ip_forward=1\n"))
	f.d.sysctl = loaded.PredictSysctl
	return h, f
}

// TestVerifySysctl: after a change that reloaded UFW, verify compares the
// values UFW's sysctl file sets with the live ones; a change that did not
// start UFW checks nothing new.
func TestVerifySysctl(t *testing.T) {
	ctx := context.Background()
	h, f := vsHost(t)
	m := hkMutation(f, nil)
	env := h.env()
	if _, err := m.Snapshot(ctx, env, hkInput{}); err != nil {
		t.Fatal(err)
	}
	out, err := m.Apply(ctx, env, hkInput{})
	if err != nil {
		t.Fatal(err)
	}
	err = m.Verify(ctx, env, hkInput{}, out)
	if e := errcode.From(err); e.Code != errcode.FirewallPredictionMismatch || !strings.Contains(e.Detail, `net/ipv4/ip_forward: live "0", predicted "1"`) {
		t.Errorf("Verify = %v, want a prediction mismatch naming the key, the live and the predicted value", err)
	}
	h.sysctl["net/ipv4/ip_forward"] = "1"
	if err := m.Verify(ctx, env, hkInput{}, out); err != nil {
		t.Errorf("Verify with the kernel as predicted: %v", err)
	}

	h, f = vsHost(t)
	s := h.state()
	s.Enabled = false
	h.setState(s)
	m = hkMutation(f, nil)
	if _, err := m.Snapshot(ctx, h.env(), hkInput{}); err != nil {
		t.Fatal(err)
	}
	if out, err = m.Apply(ctx, h.env(), hkInput{}); err != nil {
		t.Fatal(err)
	}
	if err := m.Verify(ctx, h.env(), hkInput{}, out); err != nil {
		t.Errorf("Verify of a change that did not start UFW: %v", err)
	}
}

// TestVerifySysctlRestore: a restore's own sysctl writes replace the
// prediction of UFW's file, and are checked.
func TestVerifySysctlRestore(t *testing.T) {
	ctx := context.Background()
	h, f := vsHost(t)
	h.writeFile("/etc/ufw/sysctl.conf", []byte("net/ipv4/ip_forward=0\n"))
	h.ufw["--force enable"] = func(s *fhState) int { s.Enabled = true; return 0 }
	h.ufw["disable"] = func(s *fhState) int { s.Enabled = false; return 0 }
	h.loaded = fwstate.Loaded{Variant: "nft", V4: fhParse(t, tgStopped), V6: fhParse(t, tgStopped)}
	cfg := h.config()
	var entries []core.RestoreEntry
	for _, p := range fwstate.StateFiles {
		entries = append(entries, cfg.Files[p])
	}
	snap := snapshot{ID: "01J00000000000000000000009", Entries: entries, State: snapshotState{Enabled: true,
		Sysctl: map[string]string{"net/ipv4/ip_forward": "1"}}}
	s := h.state()
	s.Enabled = false
	h.setState(s)

	m := hkMutation(f, func(fwstate.Config, fwstate.Loaded) (change, error) { return change{restore: &snap}, nil })
	if _, err := m.Snapshot(ctx, h.env(), hkInput{}); err != nil {
		t.Fatal(err)
	}
	out, err := m.Apply(ctx, h.env(), hkInput{})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Verify(ctx, h.env(), hkInput{}, out); err != nil {
		t.Errorf("Verify of a restore that wrote the snapshot's value: %v", err)
	}
	h.sysctl["net/ipv4/ip_forward"] = "0"
	err = m.Verify(ctx, h.env(), hkInput{}, out)
	if e := errcode.From(err); e.Code != errcode.FirewallPredictionMismatch || !strings.Contains(e.Detail, `net/ipv4/ip_forward: live "0", predicted "1"`) {
		t.Errorf("Verify = %v, want a prediction mismatch for the value the restore wrote", err)
	}
}
