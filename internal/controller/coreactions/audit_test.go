package coreactions

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// fillAudit records a failed login, a controller-side change and an ingested
// agent event, in that order.
func (e *testEnv) fillAudit() {
	e.t.Helper()
	ctx := context.Background()
	if err := e.audit.Record(ctx, core.AuditEntry{
		Actor: "anonymous", Action: "core.auth.login", Version: 1, Phase: "login", Result: "error",
		ErrorCode: "auth.invalid_credentials", Source: "127.0.0.1", At: t0, Input: json.RawMessage(`{"username":"mallory"}`),
	}); err != nil {
		e.t.Fatal(err)
	}
	if err := e.audit.Append(ctx, nil, core.JournalEntry{
		At: t0, Actor: "user:u1", Action: "core.token.create", Version: 1, Phase: "apply", ChangeID: "c1",
		Input: json.RawMessage(`{"name":"ci","scope":"read","nested":{"list":[1,{"deep":true}]}}`), Result: "ok",
	}); err != nil {
		e.t.Fatal(err)
	}
	if err := e.audit.Ingest(ctx, "h1", []core.JournalEntry{{
		Seq: 1, At: t0, Actor: "user:u1", Action: "demo.item.add", Version: 2, Phase: "confirm", ChangeID: "c2",
		Input: json.RawMessage(`{"password":"[redacted]"}`), Result: "ok",
	}}); err != nil {
		e.t.Fatal(err)
	}
}

func TestAuditList(t *testing.T) {
	e := newTestEnv(t)
	e.fillAudit()
	raw, err := e.engine.Read(context.Background(), operator, ref("core.audit.list"), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"entries":[
		{"id":"00000000000000000000000003","at":"2026-10-17T00:00:00Z","actor":"user:u1","hostId":"h1",
		 "action":"demo.item.add","version":2,"phase":"confirm","changeId":"c2","input":{"password":"[redacted]"},
		 "result":"ok","errorCode":null,"source":null},
		{"id":"00000000000000000000000002","at":"2026-10-17T00:00:00Z","actor":"user:u1","hostId":null,
		 "action":"core.token.create","version":1,"phase":"apply","changeId":"c1",
		 "input":{"name":"ci","scope":"read","nested":{"list":[1,{"deep":true}]}},
		 "result":"ok","errorCode":null,"source":null},
		{"id":"00000000000000000000000001","at":"2026-10-17T00:00:00Z","actor":"anonymous","hostId":null,
		 "action":"core.auth.login","version":1,"phase":"login","changeId":null,"input":{"username":"mallory"},
		 "result":"error","errorCode":"auth.invalid_credentials","source":"127.0.0.1"}
	],"nextBefore":null}`
	if !jsonEqual(t, raw, want) {
		t.Errorf("output = %s\nwant %s", raw, want)
	}
}

func TestAuditListFiltersAndPages(t *testing.T) {
	e := newTestEnv(t)
	e.fillAudit()
	ids := func(l AuditList) []string {
		var out []string
		for _, entry := range l.Entries {
			out = append(out, entry.ID)
		}
		return out
	}
	for _, tt := range []struct {
		name string
		in   AuditListInput
		want []string
		next string
	}{
		{"actor", AuditListInput{Actor: "anonymous"}, []string{"00000000000000000000000001"}, ""},
		{"host", AuditListInput{HostID: "h1"}, []string{"00000000000000000000000003"}, ""},
		{"action", AuditListInput{Action: "core.token.create"}, []string{"00000000000000000000000002"}, ""},
		{"first page", AuditListInput{Limit: 2}, []string{"00000000000000000000000003", "00000000000000000000000002"}, "00000000000000000000000002"},
		{"second page", AuditListInput{Limit: 2, Before: "00000000000000000000000002"}, []string{"00000000000000000000000001"}, ""},
	} {
		var got AuditList
		if err := e.read(operator, "core.audit.list", tt.in, &got); err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		next := ""
		if got.NextBefore != nil {
			next = *got.NextBefore
		}
		if g := ids(got); !slices.Equal(g, tt.want) || next != tt.next {
			t.Errorf("%s: ids %v, next %q; want %v, %q", tt.name, g, next, tt.want, tt.next)
		}
	}

	var empty AuditList
	if err := e.read(operator, "core.audit.list", AuditListInput{Actor: "nobody"}, &empty); err != nil {
		t.Fatal(err)
	}
	if empty.Entries == nil || len(empty.Entries) != 0 {
		t.Errorf("entries = %#v, want an empty list", empty.Entries)
	}
}

func TestAuditListLimits(t *testing.T) {
	e := newTestEnv(t)
	for _, raw := range []string{`{"limit":0}`, `{"limit":501}`, `{"limit":-1}`, `{"since":"x"}`} {
		_, err := e.engine.Read(context.Background(), operator, ref("core.audit.list"), json.RawMessage(raw))
		var ce *errcode.Error
		if !errors.As(err, &ce) || ce.Code != errcode.InvalidInput {
			t.Errorf("%s: err = %v, want request.invalid_input", raw, err)
		}
	}
	for _, raw := range []string{`{"limit":1}`, `{"limit":500}`} {
		if _, err := e.engine.Read(context.Background(), operator, ref("core.audit.list"), json.RawMessage(raw)); err != nil {
			t.Errorf("%s: %v", raw, err)
		}
	}
}
