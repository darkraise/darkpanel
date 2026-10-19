package wire

import (
	"encoding/json"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

var (
	t0 = time.Date(2026, 10, 19, 12, 0, 0, 123456789, time.UTC)
	t1 = time.Date(2026, 10, 19, 12, 5, 0, 0, time.UTC)
)

// roundTrip sends v through JSON as the agent API does and returns what
// the other side decodes.
func roundTrip[T any](t *testing.T, v T) T {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out T
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func check[C any](t *testing.T, name string, got, want C) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s round trip\n got %#v\nwant %#v", name, got, want)
	}
}

func TestRoundTrips(t *testing.T) {
	yes := true
	deadline := t1
	for _, p := range []core.Principal{
		{Actor: "user:01J", UserID: "01J", Scope: core.ScopeAdmin, Source: "100.64.0.2"},
		{Actor: "token:01K", UserID: "01J", TokenID: "01K", Scope: core.ScopeRead, Source: "100.64.0.3"},
	} {
		check(t, "Principal", roundTrip(t, FromPrincipal(p)).Core(), p)
	}
	for _, h := range []core.AgentHello{
		{ProtocolVersion: 1, BinaryVersion: "0.2.0", OS: "linux", Arch: "amd64", HostID: "01H", JournalEpoch: "01E",
			ControllerConnected: &yes,
			Modules:             []core.ModuleStatus{{ID: "hostinfo", Version: 1, Available: true}, {ID: "fw", Version: 2, Reason: "ufw missing"}},
			SchemaHashes:        map[string]core.SchemaHash{"hostinfo.summary@1": {Input: "aa", Output: "bb"}}},
		{ProtocolVersion: 1, BinaryVersion: "0.2.0", OS: "linux", Arch: "arm64", HostID: "01H", JournalEpoch: "01E"},
	} {
		check(t, "Hello", roundTrip(t, FromHello(h)).Core(), h)
	}
	for _, pv := range []core.PlanView{
		{PlanID: "01P", HostID: "01H", Action: "demo.rule.set", Version: 2, InputHash: "ff", Confirmation: "delete web",
			Diff: []core.DiffLine{{Op: "+", Text: "allow 22"}}, Guards: []core.Guard{{ID: "ssh", OK: true, Message: "kept"}}, ExpiresAt: t0},
		{PlanID: "01P", HostID: "01H", Action: "demo.rule.set", Version: 1, ExpiresAt: t0},
	} {
		check(t, "PlanView", roundTrip(t, FromPlanView(pv)).Core(), pv)
	}
	for _, o := range []core.ApplyOptions{
		{ConfirmTimeout: 90 * time.Second, Confirmation: "delete web", ConfirmNow: true},
		{},
	} {
		check(t, "ApplyOptions", roundTrip(t, FromApplyOptions(o)).Core(), o)
	}
	for _, c := range []core.ChangeView{
		{ID: "01C", HostID: "01H", Action: "demo.rule.set", Actor: "user:01J", CompensatedBy: "demo.rule.unset", Version: 1,
			Class: core.ClassRevertible, State: core.StatePending, CreatedAt: t0, UpdatedAt: t1, RevertDeadline: &deadline,
			Output: json.RawMessage(`{"rule":"allow 22"}`), Error: &core.ChangeError{Code: "plan.expired", Message: "too late"}},
		{ID: "01C", HostID: "01H", Action: "demo.rule.get", Version: 1, Class: core.ClassCompensable, State: core.StateCompleted,
			CreatedAt: t0, UpdatedAt: t0},
	} {
		check(t, "ChangeView", roundTrip(t, FromChangeView(c)).Core(), c)
	}
	for _, e := range []core.JournalEntry{
		{Seq: 7, At: t0, Actor: "user:01J", Action: "demo.rule.set", Version: 1, Phase: "apply", ChangeID: "01C",
			Input: json.RawMessage(`{"rule":"allow 22"}`), Result: "error", ErrorCode: "plan.expired", Source: "100.64.0.2"},
		{Seq: 8, At: t1, Actor: "system:revert-timer", Action: "demo.rule.set", Version: 1, Phase: "revert", ChangeID: "01C", Result: "ok"},
	} {
		check(t, "JournalEntry", roundTrip(t, FromJournalEntry(e)).Core(), e)
	}
	check(t, "Ack", roundTrip(t, Ack{Seq: 9}), Ack{Seq: 9})
}

func TestTimesAreSentInUTC(t *testing.T) {
	local := time.Date(2026, 10, 19, 19, 0, 0, 0, time.FixedZone("ICT", 7*3600))
	b, err := json.Marshal(FromPlanView(core.PlanView{ExpiresAt: local}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"expiresAt":"2026-10-19T12:00:00Z"`) {
		t.Errorf("PlanView JSON %s, want expiresAt in UTC", b)
	}
	if got := roundTrip(t, FromPlanView(core.PlanView{ExpiresAt: local})).Core().ExpiresAt; !got.Equal(local) || got.Location() != time.UTC {
		t.Errorf("ExpiresAt = %v, want the same instant in UTC", got)
	}
}

func TestNullFields(t *testing.T) {
	b, err := json.Marshal(FromChangeView(core.ChangeView{}))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{`"revertDeadline":null`, `"output":null`, `"error":null`} {
		if !strings.Contains(string(b), f) {
			t.Errorf("ChangeView JSON %s lacks %s", b, f)
		}
	}
	b, err = json.Marshal(FromHello(core.AgentHello{}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"controllerConnected":null`) {
		t.Errorf("Hello JSON %s, want controllerConnected null for an agent that is not enrolled", b)
	}
	b, err = json.Marshal(FromApplyOptions(core.ApplyOptions{ConfirmTimeout: 90 * time.Second}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"confirmTimeoutSeconds":90`) {
		t.Errorf("ApplyOptions JSON %s, want the timeout in whole seconds", b)
	}
}

var camelCase = regexp.MustCompile(`^[a-z][a-zA-Z0-9]*$`)

// TestJSONNamesAreCamelCase walks every wire type, and the core types they
// embed, and requires a camelCase JSON name on every exported field.
func TestJSONNamesAreCamelCase(t *testing.T) {
	seen := map[reflect.Type]bool{}
	var walk func(reflect.Type)
	walk = func(typ reflect.Type) {
		for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice || typ.Kind() == reflect.Map {
			typ = typ.Elem()
		}
		if typ.Kind() != reflect.Struct || seen[typ] || typ == reflect.TypeFor[time.Time]() {
			return
		}
		seen[typ] = true
		for i := range typ.NumField() {
			f := typ.Field(i)
			if !f.IsExported() {
				continue
			}
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			if !camelCase.MatchString(name) {
				t.Errorf("%s.%s has JSON name %q, want camelCase", typ, f.Name, name)
			}
			walk(f.Type)
		}
	}
	for _, v := range []any{Principal{}, Hello{}, SchemaHash{}, ModuleStatus{}, PlanView{}, ApplyOptions{}, ChangeView{},
		ChangeError{}, JournalEntry{}, Ack{}} {
		walk(reflect.TypeOf(v))
	}
}

func TestChangeFilterQuery(t *testing.T) {
	for _, f := range []core.ChangeFilter{
		{State: "pending", HostID: "01H", Before: "01C", Limit: 50},
		{},
	} {
		q := ChangeFilterQuery(f)
		got, err := ParseChangeFilter(q)
		if err != nil {
			t.Fatal(err)
		}
		if got != f {
			t.Errorf("ParseChangeFilter(%v) = %+v, want %+v", q, got, f)
		}
	}
	if q := ChangeFilterQuery(core.ChangeFilter{State: "pending", Limit: 5}).Encode(); q != "limit=5&state=pending" {
		t.Errorf("query %q, want only the set fields", q)
	}
}

func TestParseChangeFilterRefuses(t *testing.T) {
	for _, q := range []string{"limit=0", "limit=-1", "limit=ten", "limit=1&limit=2", "host=01H"} {
		v, err := url.ParseQuery(q)
		if err != nil {
			t.Fatal(err)
		}
		_, err = ParseChangeFilter(v)
		if e := errcode.From(err); err == nil || e.Code != errcode.InvalidInput {
			t.Errorf("ParseChangeFilter(%q) = %v, want request.invalid_input", q, err)
		}
	}
}
