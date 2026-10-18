package dispatch

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

var bob = core.Principal{Actor: "user:bob", UserID: "bob", Scope: core.ScopeOperate, Source: "192.0.2.8"}

// applyAgent plans and applies kv.item.set on h1.
func (h *harness) applyAgent(d *Dispatcher, name string, opts core.ApplyOptions) core.ChangeView {
	h.t.Helper()
	pl := h.plan(d, testHost, refSet, `{"name":"`+name+`","value":"1"}`)
	v, err := d.Apply(context.Background(), alice, testHost, refSet, pl.PlanID, opts)
	if err != nil {
		h.t.Fatal(err)
	}
	return v
}

// applyCtl plans and applies ctl.note.set on the controller.
func (h *harness) applyCtl(d *Dispatcher, value string) core.ChangeView {
	h.t.Helper()
	pl := h.plan(d, "", refCtlSet, `{"name":"n","value":"`+value+`"}`)
	v, err := d.Apply(context.Background(), alice, "", refCtlSet, pl.PlanID, core.ApplyOptions{})
	if err != nil {
		h.t.Fatal(err)
	}
	return v
}

func TestChangeReturnsTheRealChange(t *testing.T) {
	h, d := newDispatcher(t)
	ctx := context.Background()
	agentChange := h.applyAgent(d, "a", core.ApplyOptions{})
	ctlChange := h.applyCtl(d, "x")
	got, err := d.Change(ctx, alice, agentChange.ID)
	if err != nil || got.Action != refSet.Name || got.Version != 1 || got.HostID != testHost || got.State != core.StatePending {
		t.Errorf("agent change = %+v, %v", got, err)
	}
	got, err = d.Change(ctx, alice, ctlChange.ID)
	if err != nil || got.Action != refCtlSet.Name || got.HostID != "" || got.State != core.StateCompleted {
		t.Errorf("controller change = %+v, %v", got, err)
	}
	_, err = d.Change(ctx, alice, "01UNKNOWN")
	_ = wantCode(t, err, errcode.ChangeNotFound)
	// An index row whose host does not know the change.
	if _, err := h.db.Writer().Exec(`INSERT INTO core_change_index (change_id, host_id, action, version, created_at)
		VALUES ('01LOST', 'h1', 'kv.item.set', 1, '2026-10-17T12:00:00.000000000Z')`); err != nil {
		t.Fatal(err)
	}
	_, err = d.Change(ctx, alice, "01LOST")
	_ = wantCode(t, err, errcode.ChangeNotFound)
}

func TestChangeFindsAChangeMissingFromTheIndex(t *testing.T) {
	h, d, _ := newAudited(t)
	ctx := context.Background()
	first := h.applyAgent(d, "a", core.ApplyOptions{})
	// The controller stopped after the agent committed, before it indexed.
	unindex := func() {
		t.Helper()
		if _, err := h.db.Writer().Exec("DELETE FROM core_change_index"); err != nil {
			t.Fatal(err)
		}
	}
	indexed := func(id string) {
		t.Helper()
		if n := h.count("SELECT count(*) FROM core_change_index WHERE change_id = ? AND host_id = ?", id, testHost); n != 1 {
			t.Errorf("index rows of %s on h1 = %d, want 1", id, n)
		}
	}
	unindex()
	got, err := d.Change(ctx, alice, first.ID)
	if err != nil || got.ID != first.ID || got.HostID != testHost || got.State != core.StatePending {
		t.Fatalf("Change = %+v, %v; want the pending change on h1", got, err)
	}
	indexed(first.ID)
	unindex()
	if v, err := d.Confirm(ctx, bob, first.ID); err != nil || v.State != core.StateConfirmed {
		t.Errorf("Confirm = %+v, %v", v, err)
	}
	indexed(first.ID)
	second := h.applyAgent(d, "b", core.ApplyOptions{})
	unindex()
	if v, err := d.Revert(ctx, bob, second.ID); err != nil || v.State != core.StateReverted {
		t.Errorf("Revert = %+v, %v", v, err)
	}
	indexed(second.ID)
	_, err = d.Change(ctx, alice, "01UNKNOWN")
	_ = wantCode(t, err, errcode.ChangeNotFound)
}

func TestChangeLookupAcceptsOnlyTheHostAsked(t *testing.T) {
	h, d := newDispatcher(t)
	ctx := context.Background()
	answer := func(_ context.Context, id string) (core.ChangeView, error) {
		return core.ChangeView{ID: id, HostID: "h2", Action: refSet.Name, Version: 1}, nil
	}
	h.hosts.Add(testHost, &coretest.FakeAgent{HelloFunc: h.rt.Hello,
		ChangeFunc: func(ctx context.Context, id string) (core.ChangeView, error) { return answer(ctx, id) }})
	_, err := d.Change(ctx, alice, "01OTHER")
	_ = wantCode(t, err, errcode.ChangeNotFound)
	if n := h.count("SELECT count(*) FROM core_change_index"); n != 0 {
		t.Errorf("index rows = %d, want none for a view of another host", n)
	}
	answer = func(context.Context, string) (core.ChangeView, error) {
		return core.ChangeView{}, errors.New("agent unreachable")
	}
	_, err = d.Change(ctx, alice, "01OTHER")
	var coded *errcode.Error
	if err == nil || errors.As(err, &coded) || !strings.Contains(err.Error(), "agent unreachable") {
		t.Errorf("Change = %v, want the host's plain error", err)
	}
}

func TestChangeRefusesAHostThatAnswersForAnother(t *testing.T) {
	h, d := newDispatcher(t)
	v := h.applyAgent(d, "a", core.ApplyOptions{})
	h.hosts.Add(testHost, &coretest.FakeAgent{
		HelloFunc: h.rt.Hello,
		ChangeFunc: func(_ context.Context, id string) (core.ChangeView, error) {
			return core.ChangeView{ID: id, HostID: "h2", Action: refSet.Name, Version: 1}, nil
		},
	})
	_, err := d.Change(context.Background(), alice, v.ID)
	var coded *errcode.Error
	if err == nil || errors.As(err, &coded) || !strings.Contains(err.Error(), "answered for host h2") {
		t.Errorf("Change = %v, want a plain error naming the mismatch", err)
	}
}

func TestConfirmAndRevertActOnTheOwnerAndIngest(t *testing.T) {
	h, d, store := newAudited(t)
	ctx := context.Background()
	first := h.applyAgent(d, "a", core.ApplyOptions{})
	v, err := d.Confirm(ctx, bob, first.ID)
	if err != nil || v.ID != first.ID || v.State != core.StateConfirmed {
		t.Fatalf("Confirm = %+v, %v", v, err)
	}
	second := h.applyAgent(d, "b", core.ApplyOptions{})
	if v, err = d.Revert(ctx, bob, second.ID); err != nil || v.ID != second.ID || v.State != core.StateReverted {
		t.Fatalf("Revert = %+v, %v", v, err)
	}
	entries, _, err := store.List(ctx, core.AuditQuery{Actor: bob.Actor})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.Phase+"/"+e.ChangeID+"/"+e.HostID+"/"+e.Source)
	}
	want := []string{"revert/" + second.ID + "/h1/192.0.2.8", "confirm/" + first.ID + "/h1/192.0.2.8"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("audit of bob = %v, want %v", got, want)
	}
	_, err = d.Confirm(ctx, bob, first.ID)
	_ = wantCode(t, err, errcode.ChangeNotPending)
	ctl := h.applyCtl(d, "x")
	_, err = d.Revert(ctx, bob, ctl.ID)
	_ = wantCode(t, err, errcode.ChangeNotPending)
	_, err = d.Revert(ctx, bob, "01UNKNOWN")
	_ = wantCode(t, err, errcode.ChangeNotFound)
}

func ids(vs []core.ChangeView) []string {
	out := []string{}
	for _, v := range vs {
		out = append(out, v.ID)
	}
	return out
}

func TestChangesMergeAndPageLikeTheEngine(t *testing.T) {
	h, d := newDispatcher(t)
	ctx := context.Background()
	// c[0], c[2] and c[4] are agent changes, the others controller changes.
	var c []string
	for i := range 3 {
		c = append(c, h.applyAgent(d, string(rune('a'+i)), core.ApplyOptions{ConfirmNow: true}).ID)
		h.clock.Advance(time.Second)
		c = append(c, h.applyCtl(d, string(rune('a'+i))).ID)
		h.clock.Advance(time.Second)
	}
	list := func(f core.ChangeFilter) []string {
		t.Helper()
		vs, err := d.Changes(ctx, alice, f)
		if err != nil {
			t.Fatal(err)
		}
		return ids(vs)
	}
	cases := []struct {
		name string
		f    core.ChangeFilter
		want []string
	}{
		{"everything, newest first", core.ChangeFilter{}, []string{c[5], c[4], c[3], c[2], c[1], c[0]}},
		{"limit below the total", core.ChangeFilter{Limit: 4}, []string{c[5], c[4], c[3], c[2]}},
		{"cursor in the middle", core.ChangeFilter{Before: c[3], Limit: 2}, []string{c[2], c[1]}},
		{"last page is short", core.ChangeFilter{Before: c[2], Limit: 4}, []string{c[1], c[0]}},
		{"one host", core.ChangeFilter{HostID: testHost}, []string{c[4], c[2], c[0]}},
		{"state", core.ChangeFilter{State: core.StateCompleted}, []string{c[5], c[3], c[1]}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := list(tc.f); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Changes = %v, want %v", got, tc.want)
			}
		})
	}
	var paged []string
	for before := ""; ; {
		page := list(core.ChangeFilter{Before: before, Limit: 2})
		paged = append(paged, page...)
		if len(page) < 2 {
			break
		}
		before = page[len(page)-1]
	}
	if want := []string{c[5], c[4], c[3], c[2], c[1], c[0]}; !reflect.DeepEqual(paged, want) {
		t.Errorf("pages of 2 = %v, want %v", paged, want)
	}
}

func TestChangesPassTheLimitToEverySource(t *testing.T) {
	h, d := newDispatcher(t)
	var seen []core.ChangeFilter
	h.hosts.Add(testHost, &coretest.FakeAgent{
		HelloFunc: h.rt.Hello,
		ChangesFunc: func(_ context.Context, f core.ChangeFilter) ([]core.ChangeView, error) {
			seen = append(seen, f)
			return []core.ChangeView{}, nil
		},
	})
	for _, limit := range []int{0, 7, 900} {
		if _, err := d.Changes(context.Background(), alice, core.ChangeFilter{Limit: limit, Before: "01B", State: "pending"}); err != nil {
			t.Fatal(err)
		}
	}
	want := []core.ChangeFilter{
		{State: "pending", HostID: testHost, Before: "01B", Limit: 100},
		{State: "pending", HostID: testHost, Before: "01B", Limit: 7},
		{State: "pending", HostID: testHost, Before: "01B", Limit: 500},
	}
	if !reflect.DeepEqual(seen, want) {
		t.Errorf("agent filters = %+v, want %+v", seen, want)
	}
}

func TestChangesFailWhenAHostCannotBeListed(t *testing.T) {
	h, d := newDispatcher(t)
	ctx := context.Background()
	h.applyAgent(d, "a", core.ApplyOptions{})
	if _, err := h.db.Writer().Exec(
		"INSERT INTO core_hosts (id, name, is_local, created_at) VALUES ('h9', 'gone', 0, '2026-10-17T00:00:00Z')"); err != nil {
		t.Fatal(err)
	}
	_, err := d.Changes(ctx, alice, core.ChangeFilter{})
	_ = wantCode(t, err, errcode.NotFound)
	vs, err := d.Changes(ctx, alice, core.ChangeFilter{HostID: testHost})
	if err != nil || len(vs) != 1 {
		t.Errorf("Changes of h1 = %+v, %v; want its one change", vs, err)
	}
}

func TestChangesRefuseAHostListingOutsideTheRequest(t *testing.T) {
	h, d := newDispatcher(t)
	ctx := context.Background()
	var listed []core.ChangeView
	h.hosts.Add(testHost, &coretest.FakeAgent{
		HelloFunc:   h.rt.Hello,
		ChangesFunc: func(context.Context, core.ChangeFilter) ([]core.ChangeView, error) { return listed, nil },
	})
	for _, tc := range []struct {
		name string
		v    core.ChangeView
		f    core.ChangeFilter
		want string
	}{
		{"another host", core.ChangeView{ID: "01A", HostID: "h2"}, core.ChangeFilter{HostID: testHost}, "answered for host h2"},
		{"at the cursor", core.ChangeView{ID: "01B", HostID: testHost}, core.ChangeFilter{Before: "01B"}, "at or above cursor 01B"},
	} {
		listed = []core.ChangeView{tc.v}
		_, err := d.Changes(ctx, alice, tc.f)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: Changes = %v, want an error containing %q", tc.name, err, tc.want)
		}
	}
}

func TestChangeRefusesAViewOfAnotherChange(t *testing.T) {
	h, d := newDispatcher(t)
	ctx := context.Background()
	v := h.applyAgent(d, "a", core.ApplyOptions{})
	h.hosts.Add(testHost, &coretest.FakeAgent{
		HelloFunc: h.rt.Hello,
		ChangeFunc: func(context.Context, string) (core.ChangeView, error) {
			return core.ChangeView{ID: "01OTHER", HostID: testHost, Action: refSet.Name, Version: 1}, nil
		},
	})
	_, err := d.Change(ctx, alice, v.ID)
	var coded *errcode.Error
	if err == nil || errors.As(err, &coded) || !strings.Contains(err.Error(), "answered as change 01OTHER") {
		t.Errorf("Change of an indexed change = %v, want a plain error naming the mismatch", err)
	}
	if _, err := h.db.Writer().Exec("DELETE FROM core_change_index"); err != nil {
		t.Fatal(err)
	}
	_, err = d.Change(ctx, alice, v.ID)
	_ = wantCode(t, err, errcode.ChangeNotFound)
	if n := h.count("SELECT count(*) FROM core_change_index"); n != 0 {
		t.Errorf("index rows = %d, want none for a view of another change", n)
	}
}
