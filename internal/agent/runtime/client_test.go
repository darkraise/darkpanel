package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

func wantCode(t *testing.T, err error, code errcode.Code) *errcode.Error {
	t.Helper()
	var e *errcode.Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("err = %v, want code %s", err, code)
	}
	return e
}

func TestHelloReportsTheHostAndItsModules(t *testing.T) {
	f := newFixture(t)
	h, err := f.rt.Hello(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := core.AgentHello{
		ProtocolVersion: ProtocolVersion, BinaryVersion: "test", OS: goruntime.GOOS, HostID: testHost,
		Modules: f.rt.statuses(),
	}
	if !reflect.DeepEqual(h, want) || len(h.Modules) != 4 {
		t.Errorf("Hello = %+v, want %+v", h, want)
	}
}

func TestPreflightChecksAgain(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.host.mu.Lock()
	f.host.ready = true
	f.host.mu.Unlock()
	st, err := f.rt.Preflight(ctx, "flaky")
	if err != nil || st != (core.ModuleStatus{ID: "flaky", Version: 2, Available: true}) {
		t.Fatalf("Preflight(flaky) = %+v, %v; want available", st, err)
	}
	h, err := f.rt.Hello(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !h.Modules[2].Available {
		t.Errorf("Hello after the preflight = %+v, want flaky available", h.Modules[2])
	}
	st, err = f.rt.Preflight(ctx, "bad")
	if err != nil || st.Available || st.Reason != ReasonMigrationFailed || f.host.badChecks != 0 {
		t.Errorf("Preflight(bad) = %+v, %v (checks %d); want the migration failure without a check", st, err, f.host.badChecks)
	}
	for _, id := range []string{"core", "nope"} {
		_, err := f.rt.Preflight(ctx, id)
		_ = wantCode(t, err, errcode.NotFound)
	}
}

func TestReadsRefuseABrokenModule(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	_, err := f.rt.Read(ctx, alice, core.ActionRef{Name: "bad.item.get", Version: 1}, json.RawMessage(`{"name":"a"}`))
	e := wantCode(t, err, errcode.ModuleUnavailable)
	if e.Detail != "module bad is unavailable: "+ReasonMigrationFailed {
		t.Errorf("detail = %q", e.Detail)
	}
	// A failed preflight is the dispatcher's to refuse; the agent serves it.
	if _, err := f.rt.Read(ctx, alice, core.ActionRef{Name: "flaky.item.get", Version: 1}, json.RawMessage(`{"name":"a"}`)); err != nil {
		t.Errorf("Read of flaky = %v, want served", err)
	}
	_, err = f.rt.Read(ctx, alice, core.ActionRef{Name: "kv.item.nope", Version: 1}, json.RawMessage(`{}`))
	_ = wantCode(t, err, errcode.ActionUnknown)
}

func TestResourceReadsWithTheAgentEnv(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	out, err := f.rt.Resource(ctx, alice, "kv.status")
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"count":0,"token":"[redacted]"}` {
		t.Errorf("kv.status = %s, want the token redacted", out)
	}
	_, err = f.rt.Resource(ctx, alice, "kv.nope")
	_ = wantCode(t, err, errcode.NotFound)
	_, err = f.rt.Resource(ctx, alice, "boom.status")
	var coded *errcode.Error
	if err == nil || errors.As(err, &coded) || !strings.Contains(err.Error(), "panicked") {
		t.Errorf("boom.status = %v, want a plain panic error", err)
	}
}

func TestChangesThroughTheClient(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	bob := core.Principal{Actor: "user:bob", UserID: "bob", Scope: core.ScopeOperate, Source: "192.0.2.8"}
	plan := func(input string) core.PlanView {
		t.Helper()
		v, err := f.rt.Plan(ctx, alice, refSet, json.RawMessage(input))
		if err != nil {
			t.Fatal(err)
		}
		if v.HostID != testHost || v.Action != refSet.Name {
			t.Fatalf("plan = %+v", v)
		}
		return v
	}
	first, err := f.rt.Apply(ctx, alice, plan(`{"name":"a","value":"1"}`).PlanID, core.ApplyOptions{})
	if err != nil || first.State != core.StatePending {
		t.Fatalf("Apply = %+v, %v", first, err)
	}
	if v, err := f.rt.Confirm(ctx, bob, first.ID); err != nil || v.State != core.StateConfirmed {
		t.Fatalf("Confirm = %+v, %v", v, err)
	}
	second, err := f.rt.Apply(ctx, alice, plan(`{"name":"b","value":"1"}`).PlanID, core.ApplyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if v, err := f.rt.Revert(ctx, bob, second.ID); err != nil || v.State != core.StateReverted {
		t.Fatalf("Revert = %+v, %v", v, err)
	}
	if v, err := f.rt.Change(ctx, first.ID); err != nil || v.State != core.StateConfirmed || v.HostID != testHost {
		t.Errorf("Change = %+v, %v", v, err)
	}
	list, err := f.rt.Changes(ctx, core.ChangeFilter{})
	if err != nil || len(list) != 2 || list[0].ID != second.ID {
		t.Errorf("Changes = %+v, %v; want both, newest first", list, err)
	}

	entries, err := f.rt.Journal(ctx, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.Phase+"/"+e.Source)
	}
	if want := []string{"apply/192.0.2.7", "confirm/192.0.2.8", "apply/192.0.2.7", "revert/192.0.2.8"}; !reflect.DeepEqual(got, want) {
		t.Errorf("journal = %v, want %v", got, want)
	}
	if err := f.rt.Ack(ctx, entries[1].Seq); err != nil {
		t.Fatal(err)
	}
	left, err := f.rt.Journal(ctx, 0, 10)
	if err != nil || len(left) != 2 || left[0].Seq != entries[2].Seq {
		t.Errorf("journal after Ack = %+v, %v; want the last two entries", left, err)
	}
}
