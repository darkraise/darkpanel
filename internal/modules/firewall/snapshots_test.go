package firewall

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/schema"
)

// TestSnapshotsList: newest first, the host's first snapshot its baseline.
func TestSnapshotsList(t *testing.T) {
	ctx := context.Background()
	h, f, _ := ruHost(t)
	first := snCapture(t, h, f)
	h.clock.Advance(time.Minute)
	s := h.state()
	s.Enabled = false
	h.setState(s)
	second := snCapture(t, h, f)

	list, err := f.snapshots(ctx, h.env())
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	want := []SnapshotInfo{
		{ID: second, CreatedAt: t0.Add(time.Minute), Actor: "user:u1", Enabled: false},
		{ID: first, CreatedAt: t0, Actor: "user:u1", Baseline: true, Enabled: true},
	}
	if !reflect.DeepEqual(list.Snapshots, want) {
		t.Errorf("snapshots\n%+v\nwant\n%+v", list.Snapshots, want)
	}
	h2, f2, _ := ruHost(t)
	if list, err := f2.snapshots(ctx, h2.env()); err != nil || list.Snapshots == nil || len(list.Snapshots) != 0 {
		t.Errorf("snapshots of a new host = %+v, %v; want an empty list", list, err)
	}
}

// TestSnapshotCompare: two snapshots, or one against the host now, as a
// plan lists the change, with the state files that differ.
func TestSnapshotCompare(t *testing.T) {
	ctx := context.Background()
	h, f, _ := ruHost(t)
	first := snCapture(t, h, f)
	s := h.state()
	s.Rules4, s.Rules6, s.LogLevel = s.Rules4[1:], nil, "medium"
	h.setState(s)
	h.sysctl["net/ipv4/ip_forward"] = "1"
	second := snCapture(t, h, f)
	h.sysctl["net/ipv4/ip_forward"] = "0"

	def := f.snapshotCompare()
	if def.Spec.Name != "firewall.snapshot.compare" || def.Spec.Class != core.ClassRead || def.Spec.Side != core.SideAgent {
		t.Errorf("spec %+v", def.Spec)
	}
	raw := json.RawMessage(`{"from":"` + first + `","to":"` + second + `"}`)
	if err := schema.NewValidator().ValidateInput(def, raw); err != nil {
		t.Fatal(err)
	}
	res, err := def.Handler.Read(ctx, h.env(), raw)
	if err != nil {
		t.Fatal(err)
	}
	var cmp SnapshotComparison
	if err := json.Unmarshal(res, &cmp); err != nil {
		t.Fatal(err)
	}
	wantDiff := []core.DiffLine{
		{Op: "-", Text: "rule " + fhTag + ": allow in proto tcp from any to any port 22"},
		{Op: "-", Text: "logging low"}, {Op: "+", Text: "logging medium"},
		{Op: "-", Text: "sysctl net/ipv4/ip_forward=0"}, {Op: "+", Text: "sysctl net/ipv4/ip_forward=1"},
	}
	if cmp.From != first || cmp.To != second || !reflect.DeepEqual(cmp.Diff, wantDiff) {
		t.Errorf("comparison %s → %s\n%+v\nwant\n%+v", cmp.From, cmp.To, cmp.Diff, wantDiff)
	}
	var paths []string
	for _, fc := range cmp.Files {
		paths = append(paths, fc.Path)
		if fc.From == fc.To || fc.From == "" || fc.To == "" {
			t.Errorf("file change %+v, want two different hashes", fc)
		}
	}
	if want := []string{"/etc/ufw/user.rules", "/etc/ufw/user6.rules", "/etc/ufw/ufw.conf"}; !reflect.DeepEqual(paths, want) {
		t.Errorf("changed files %v, want %v", paths, want)
	}

	now, err := f.compareSnapshots(ctx, h.env(), SnapshotCompareInput{From: second})
	if err != nil {
		t.Fatal(err)
	}
	if now.To != "now" || !reflect.DeepEqual(now.Diff, []core.DiffLine{{Op: "-", Text: "sysctl net/ipv4/ip_forward=1"}, {Op: "+", Text: "sysctl net/ipv4/ip_forward=0"}}) || len(now.Files) != 0 {
		t.Errorf("comparison with now %+v", now)
	}
	for _, in := range []SnapshotCompareInput{{From: "01J000000000000000000000ZZ"}, {From: first, To: "01J000000000000000000000ZY"}} {
		_, err := f.compareSnapshots(ctx, h.env(), in)
		if e := errcode.From(err); e.Code != errcode.InvalidInput || len(e.Fields) != 1 {
			t.Errorf("compare %+v = %v, want an invalid input", in, err)
		}
	}
}
