package tailnet

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/cloud"
	"github.com/darkraise/darkpanel/internal/controller/cloud/cloudfake"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/modules/tailnet/fake"
)

var dvAdmin = core.Principal{Actor: "user:u1", UserID: "u1", Scope: core.ScopeAdmin, Source: "203.0.113.7"}

type dvModule struct{ actions []core.ActionDef }

func (m dvModule) Manifest() core.Manifest {
	return core.Manifest{ID: ID, Version: 1, Actions: m.actions}
}

type dvFixture struct {
	t   *testing.T
	srv *fake.Server
	h   *tnHost
	tn  tn
	eng *lifecycle.Engine
}

// dvNew builds a controller engine serving the three device actions over the
// fake Tailscale API, with the account connected when connect is set.
func dvNew(t *testing.T, connect bool) *dvFixture {
	t.Helper()
	srv := fake.New(t)
	h := newTNHost(t)
	if connect {
		acConnect(t, h, srv)
	}
	n := tnWith(srv)
	reg, err := core.NewRegistry(dvModule{[]core.ActionDef{n.deviceList(), n.deviceAuthorize(), n.deviceSetRoutes()}})
	if err != nil {
		t.Fatal(err)
	}
	eng, err := lifecycle.New(lifecycle.Config{
		Side: core.SideController, Registry: reg, Validator: schema.NewValidator(), Journal: &coretest.MemJournal{}, Redact: schema.Redact,
		Base: core.Env{DB: h.db, Sealer: coretest.Sealer{}, Clock: h.clock, Log: slog.New(slog.NewTextHandler(io.Discard, nil))},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(eng.Close)
	return &dvFixture{t: t, srv: srv, h: h, tn: n, eng: eng}
}

func (f *dvFixture) plan(action, input string) (core.PlanView, error) {
	f.t.Helper()
	return f.eng.Plan(context.Background(), dvAdmin, core.ActionRef{Name: action, Version: 1}, json.RawMessage(input), core.PlanOptions{})
}

// do plans action and applies the plan.
func (f *dvFixture) do(action, input string) (core.PlanView, core.ChangeView, error) {
	f.t.Helper()
	pv, err := f.plan(action, input)
	if err != nil {
		return pv, core.ChangeView{}, err
	}
	cv, err := f.eng.Apply(context.Background(), dvAdmin, pv.PlanID, core.ApplyOptions{})
	return pv, cv, err
}

// undo undoes a change and waits until the undo has settled.
func (f *dvFixture) undo(changeID string) core.ChangeView {
	f.t.Helper()
	ctx := context.Background()
	if _, err := f.eng.Undo(ctx, dvAdmin, changeID, nil); err != nil {
		f.t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		v, err := f.eng.Change(ctx, changeID)
		if err != nil {
			f.t.Fatal(err)
		}
		if v.State != core.StateUndoing || time.Now().After(deadline) {
			return v
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (f *dvFixture) effects(changeID string) []string {
	f.t.Helper()
	rows, err := f.h.db.Read().QueryContext(context.Background(),
		`SELECT kind, resource, state, reason, local, commit_wait_ns FROM core_effects WHERE change_id = ? ORDER BY seq`, changeID)
	if err != nil {
		f.t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var kind, resource, state, reason string
		var local int
		var wait int64
		if err := rows.Scan(&kind, &resource, &state, &reason, &local, &wait); err != nil {
			f.t.Fatal(err)
		}
		out = append(out, fmt.Sprintf("%s %s %s %s local=%d wait=%d", kind, resource, state, reason, local, wait))
	}
	if err := rows.Err(); err != nil {
		f.t.Fatal(err)
	}
	return out
}

// posts counts the POST requests the fake received on a path ending in suffix.
func (f *dvFixture) posts(suffix string) int {
	n := 0
	for _, r := range f.srv.Cloud().Requests() {
		if r.Method == http.MethodPost && strings.HasSuffix(r.Path, suffix) {
			n++
		}
	}
	return n
}

func dvJSON(t *testing.T, raw []byte) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("%s: %v", raw, err)
	}
	return v
}

func TestDvListSortsAndNormalizesDevices(t *testing.T) {
	f := dvNew(t, true)
	web := f.srv.AddDevice(fake.Device{Name: "web-1.example.ts.net", Hostname: "web-1", OS: "linux", User: "a@example.com",
		Addresses: []string{"100.64.0.1"}, Tags: []string{"tag:server"}, Authorized: true})
	gw := f.srv.AddDevice(fake.Device{Name: "gw.example.ts.net", Hostname: "gw", OS: "linux", User: "a@example.com",
		Addresses: []string{"100.64.0.2"}, Authorized: true,
		AdvertisedRoutes: []string{"::/0", "10.0.0.0/24", "0.0.0.0/0"}, EnabledRoutes: []string{"10.0.0.0/24"}})
	bare := f.srv.AddDevice(fake.Device{Name: "bare.example.ts.net", Hostname: "bare"})

	raw, err := f.eng.Read(context.Background(), dvAdmin, core.ActionRef{Name: "tailnet.device.list", Version: 1}, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "null") {
		t.Errorf("the list carries a null member: %s", raw)
	}
	var got DeviceList
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, d := range got.Devices {
		ids = append(ids, d.ID)
	}
	if want := []string{bare, gw, web}; !slices.Equal(ids, want) {
		t.Fatalf("devices %v, want %v (sorted by name)", ids, want)
	}
	b, g, w := got.Devices[0], got.Devices[1], got.Devices[2]
	if b.Authorized || b.Addresses == nil || b.Tags == nil || b.AdvertisedRoutes == nil || b.EnabledRoutes == nil || b.ExitNode {
		t.Errorf("bare device %+v, want empty lists, unauthorized, not an exit node", b)
	}
	if want := []string{"0.0.0.0/0", "10.0.0.0/24", "::/0"}; !slices.Equal(g.AdvertisedRoutes, want) || !slices.Equal(g.EnabledRoutes, []string{"10.0.0.0/24"}) {
		t.Errorf("gateway routes %v / %v, want the advertised routes sorted and one enabled", g.AdvertisedRoutes, g.EnabledRoutes)
	}
	if !g.ExitNode || g.ExitNodeEnabled {
		t.Errorf("gateway exit node %v enabled %v, want an exit node that is not approved", g.ExitNode, g.ExitNodeEnabled)
	}
	if w.Hostname != "web-1" || w.OS != "linux" || w.User != "a@example.com" || !w.Authorized ||
		!slices.Equal(w.Addresses, []string{"100.64.0.1"}) || !slices.Equal(w.Tags, []string{"tag:server"}) {
		t.Errorf("web device %+v", w)
	}
}

func TestDvListNeedsAConnectedAccount(t *testing.T) {
	f := dvNew(t, false)
	_, err := f.eng.Read(context.Background(), dvAdmin, core.ActionRef{Name: "tailnet.device.list", Version: 1}, json.RawMessage(`{}`))
	if e := errcode.From(err); e.Code != errcode.CloudNotConnected {
		t.Errorf("list = %v, want %s", err, errcode.CloudNotConnected)
	}
}

func TestDvAuthorizeAndUndo(t *testing.T) {
	f := dvNew(t, true)
	id := f.srv.AddDevice(fake.Device{Name: "web-1.example.ts.net", Hostname: "web-1"})

	pv, cv, err := f.do("tailnet.device.authorize", `{"device":"`+id+`"}`)
	if err != nil || cv.State != core.StateCompleted {
		t.Fatalf("authorize = %v, %v (%+v)", cv.State, err, cv.Error)
	}
	wantDiff := []core.DiffLine{
		{Op: " ", Text: "device web-1.example.ts.net (" + id + ")"},
		{Op: "-", Text: "authorized: false"},
		{Op: "+", Text: "authorized: true"},
	}
	if !reflect.DeepEqual(pv.Diff, wantDiff) {
		t.Errorf("plan diff %+v, want %+v", pv.Diff, wantDiff)
	}
	if d, ok := f.srv.Device(id); !ok || !d.Authorized {
		t.Errorf("the fake's device %+v (%v), want it authorized", d, ok)
	}
	var out Device
	if err := json.Unmarshal(cv.Output, &out); err != nil || out.ID != id || !out.Authorized {
		t.Errorf("output %s (%v), want the authorized device", cv.Output, err)
	}
	want := fmt.Sprintf("modify tailnet.device:%s made  local=0 wait=%d", id, int64(cloud.CommitWait))
	if got := f.effects(cv.ID); !slices.Equal(got, []string{want}) {
		t.Errorf("effects %q, want %q", got, want)
	}

	if v := f.undo(cv.ID); v.State != core.StateReverted {
		t.Fatalf("undo left the change %s (%+v), want reverted", v.State, v.Error)
	}
	if d, _ := f.srv.Device(id); d.Authorized {
		t.Error("the device is still authorized after the undo")
	}
	if got := f.effects(cv.ID); len(got) != 1 || !strings.Contains(got[0], " undone ") {
		t.Errorf("effects %q after the undo, want the one effect undone", got)
	}
}

func TestDvAuthorizeConvergesWhenAlreadyAuthorized(t *testing.T) {
	f := dvNew(t, true)
	id := f.srv.AddDevice(fake.Device{Name: "web-1.example.ts.net", Hostname: "web-1", Authorized: true})

	pv, cv, err := f.do("tailnet.device.authorize", `{"device":"`+id+`"}`)
	if err != nil || cv.State != core.StateCompleted {
		t.Fatalf("authorize = %v, %v (%+v)", cv.State, err, cv.Error)
	}
	if len(pv.Diff) != 0 || len(f.effects(cv.ID)) != 0 || f.posts("/authorized") != 0 {
		t.Errorf("diff %+v, effects %q, authorize calls %d; want an empty plan that writes nothing", pv.Diff, f.effects(cv.ID), f.posts("/authorized"))
	}
	if v := f.undo(cv.ID); v.State != core.StateReverted {
		t.Errorf("undo of an empty change left it %s, want reverted", v.State)
	}
	if d, _ := f.srv.Device(id); !d.Authorized {
		t.Error("undoing a change that authorized nothing de-authorized the device")
	}
}

func TestDvAuthorizeRefusals(t *testing.T) {
	f := dvNew(t, true)
	tests := []struct {
		name, input string
		code        errcode.Code
		field       string
	}{
		{"unknown device", `{"device":"nope"}`, errcode.NotFound, ""},
		{"empty device", `{"device":""}`, errcode.InvalidInput, "/device"},
	}
	for _, tt := range tests {
		_, err := f.plan("tailnet.device.authorize", tt.input)
		e := errcode.From(err)
		if e.Code != tt.code {
			t.Errorf("%s: plan = %v, want %s", tt.name, err, tt.code)
		}
		if tt.field != "" && !slices.ContainsFunc(e.Fields, func(fe errcode.FieldError) bool { return fe.Path == tt.field }) {
			t.Errorf("%s: fields %+v, want one naming %s", tt.name, e.Fields, tt.field)
		}
	}
}

func TestDvAuthorizeWriteFailures(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		state      string
		effect     string
		changeCode string
	}{
		{"refused by the provider", http.StatusForbidden, core.StateFailed, " skipped ", string(errcode.CloudPermissionMissing)},
		{"outcome unknown", http.StatusInternalServerError, core.StateNeedsRepair, " unresolved uncertain ", string(errcode.NeedsRepair)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := dvNew(t, true)
			id := f.srv.AddDevice(fake.Device{Name: "web-1.example.ts.net", Hostname: "web-1"})
			f.srv.Cloud().Fail(http.MethodPost, "/api/v2/device/"+id+"/authorized", cloudfake.Fault{Status: tt.status})

			_, cv, err := f.do("tailnet.device.authorize", `{"device":"`+id+`"}`)
			if err != nil {
				t.Fatal(err)
			}
			if cv.State != tt.state || cv.Error == nil || cv.Error.Code != tt.changeCode {
				t.Errorf("change %s with error %+v, want %s with code %s", cv.State, cv.Error, tt.state, tt.changeCode)
			}
			if got := f.effects(cv.ID); len(got) != 1 || !strings.Contains(got[0], tt.effect) {
				t.Errorf("effects %q, want one containing %q", got, tt.effect)
			}
			if d, _ := f.srv.Device(id); d.Authorized {
				t.Error("the device was authorized although the call failed")
			}
		})
	}
}

func TestDvSetRoutesAndUndo(t *testing.T) {
	f := dvNew(t, true)
	id := f.srv.AddDevice(fake.Device{Name: "gw.example.ts.net", Hostname: "gw", Authorized: true,
		AdvertisedRoutes: []string{"::/0", "10.0.0.0/24", "0.0.0.0/0"}})

	pv, cv, err := f.do("tailnet.device.set-routes", `{"device":"`+id+`","routes":["::/0","10.0.0.0/24","0.0.0.0/0","10.0.0.0/24"]}`)
	if err != nil || cv.State != core.StateCompleted {
		t.Fatalf("set-routes = %v, %v (%+v)", cv.State, err, cv.Error)
	}
	wantDiff := []core.DiffLine{
		{Op: " ", Text: "device gw.example.ts.net (" + id + ")"},
		{Op: "+", Text: "route 0.0.0.0/0"},
		{Op: "+", Text: "route 10.0.0.0/24"},
		{Op: "+", Text: "route ::/0"},
	}
	if !reflect.DeepEqual(pv.Diff, wantDiff) {
		t.Errorf("plan diff %+v, want %+v", pv.Diff, wantDiff)
	}
	enabled := []string{"0.0.0.0/0", "10.0.0.0/24", "::/0"}
	if d, _ := f.srv.Device(id); !slices.Equal(d.EnabledRoutes, enabled) {
		t.Errorf("the fake's enabled routes %v, want %v", d.EnabledRoutes, enabled)
	}
	var out DeviceRoutes
	if err := json.Unmarshal(cv.Output, &out); err != nil || !slices.Equal(out.Enabled, enabled) || !slices.Equal(out.Advertised, enabled) {
		t.Errorf("output %s (%v), want both lists sorted", cv.Output, err)
	}
	want := fmt.Sprintf("modify tailnet.routes:%s made  local=0 wait=%d", id, int64(cloud.CommitWait))
	if got := f.effects(cv.ID); !slices.Equal(got, []string{want}) {
		t.Errorf("effects %q, want %q", got, want)
	}

	raw, err := f.eng.Read(context.Background(), dvAdmin, core.ActionRef{Name: "tailnet.device.list", Version: 1}, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var list DeviceList
	if err := json.Unmarshal(raw, &list); err != nil || !list.Devices[0].ExitNodeEnabled {
		t.Errorf("list %s (%v), want the exit node approved", raw, err)
	}

	if v := f.undo(cv.ID); v.State != core.StateReverted {
		t.Fatalf("undo left the change %s (%+v), want reverted", v.State, v.Error)
	}
	if d, _ := f.srv.Device(id); len(d.EnabledRoutes) != 0 {
		t.Errorf("enabled routes %v after the undo, want none", d.EnabledRoutes)
	}
}

func TestDvSetRoutesRestoresThePreviousList(t *testing.T) {
	f := dvNew(t, true)
	id := f.srv.AddDevice(fake.Device{Name: "gw.example.ts.net", Hostname: "gw", Authorized: true,
		AdvertisedRoutes: []string{"10.0.0.0/24", "192.168.1.0/24"}, EnabledRoutes: []string{"192.168.1.0/24", "10.0.0.0/24"}})

	pv, cv, err := f.do("tailnet.device.set-routes", `{"device":"`+id+`","routes":["10.0.0.0/24"]}`)
	if err != nil || cv.State != core.StateCompleted {
		t.Fatalf("set-routes = %v, %v (%+v)", cv.State, err, cv.Error)
	}
	if want := []core.DiffLine{{Op: " ", Text: "device gw.example.ts.net (" + id + ")"}, {Op: "-", Text: "route 192.168.1.0/24"}}; !reflect.DeepEqual(pv.Diff, want) {
		t.Errorf("plan diff %+v, want %+v", pv.Diff, want)
	}
	if d, _ := f.srv.Device(id); !slices.Equal(d.EnabledRoutes, []string{"10.0.0.0/24"}) {
		t.Errorf("enabled routes %v, want only 10.0.0.0/24", d.EnabledRoutes)
	}
	if v := f.undo(cv.ID); v.State != core.StateReverted {
		t.Fatalf("undo left the change %s (%+v)", v.State, v.Error)
	}
	if d, _ := f.srv.Device(id); !slices.Equal(d.EnabledRoutes, []string{"10.0.0.0/24", "192.168.1.0/24"}) {
		t.Errorf("enabled routes %v after the undo, want the previous list", d.EnabledRoutes)
	}
}

func TestDvSetRoutesConvergesWhenUnchanged(t *testing.T) {
	f := dvNew(t, true)
	id := f.srv.AddDevice(fake.Device{Name: "gw.example.ts.net", Hostname: "gw", Authorized: true,
		AdvertisedRoutes: []string{"10.0.0.0/24"}, EnabledRoutes: []string{"10.0.0.0/24"}})

	pv, cv, err := f.do("tailnet.device.set-routes", `{"device":"`+id+`","routes":["10.0.0.0/24"]}`)
	if err != nil || cv.State != core.StateCompleted {
		t.Fatalf("set-routes = %v, %v (%+v)", cv.State, err, cv.Error)
	}
	if len(pv.Diff) != 0 || len(f.effects(cv.ID)) != 0 || f.posts("/routes") != 0 {
		t.Errorf("diff %+v, effects %q, route writes %d; want an empty plan that writes nothing", pv.Diff, f.effects(cv.ID), f.posts("/routes"))
	}
}

func TestDvSetRoutesRefusals(t *testing.T) {
	f := dvNew(t, true)
	id := f.srv.AddDevice(fake.Device{Name: "gw.example.ts.net", Hostname: "gw", Authorized: true, AdvertisedRoutes: []string{"10.0.0.0/24"}})
	tests := []struct {
		name, input string
		code        errcode.Code
		field       string
	}{
		{"unknown device", `{"device":"nope","routes":[]}`, errcode.NotFound, ""},
		{"empty device", `{"device":"","routes":[]}`, errcode.InvalidInput, "/device"},
		{"not a prefix", `{"device":"` + id + `","routes":["10.0.0.0/33"]}`, errcode.InvalidInput, "/routes"},
		{"not advertised", `{"device":"` + id + `","routes":["192.168.1.0/24"]}`, errcode.InvalidInput, "/routes"},
	}
	for _, tt := range tests {
		_, err := f.plan("tailnet.device.set-routes", tt.input)
		e := errcode.From(err)
		if e.Code != tt.code {
			t.Errorf("%s: plan = %v, want %s", tt.name, err, tt.code)
		}
		if tt.field != "" && !slices.ContainsFunc(e.Fields, func(fe errcode.FieldError) bool { return fe.Path == tt.field }) {
			t.Errorf("%s: fields %+v, want one naming %s", tt.name, e.Fields, tt.field)
		}
	}
	if f.posts("/routes") != 0 {
		t.Error("a refused plan wrote routes")
	}
}

func TestDvObserveReadsTheDevice(t *testing.T) {
	f := dvNew(t, true)
	id := f.srv.AddDevice(fake.Device{Name: "gw.example.ts.net", Hostname: "gw", Authorized: true,
		AdvertisedRoutes: []string{"10.0.0.0/24", "::/0"}, EnabledRoutes: []string{"::/0", "10.0.0.0/24"}})
	authorize, routes := f.tn.deviceAuthorize(), f.tn.deviceSetRoutes()
	tests := []struct {
		def      core.ActionDef
		resource string
		want     string
	}{
		{authorize, "tailnet.device:" + id, `{"authorized":true}`},
		{authorize, "tailnet.device:missing", `null`},
		{routes, "tailnet.routes:" + id, `{"enabledRoutes":["10.0.0.0/24","::/0"]}`},
		{routes, "tailnet.routes:missing", `null`},
	}
	for _, tt := range tests {
		obs, err := tt.def.Handler.Observe(context.Background(), f.h.env(""), core.Effect{Kind: core.EffectModify, Resource: tt.resource, State: core.EffectMade})
		if err != nil {
			t.Errorf("%s: %v", tt.resource, err)
			continue
		}
		if !reflect.DeepEqual(dvJSON(t, obs.Current.State), dvJSON(t, []byte(tt.want))) || obs.Matches != 1 {
			t.Errorf("%s: observed %s with %d matches, want %s", tt.resource, obs.Current.State, obs.Matches, tt.want)
		}
	}
}

func TestDvDefinitions(t *testing.T) {
	n := tnWith(fake.New(t))
	tests := []struct {
		def    core.ActionDef
		name   string
		class  core.Class
		locks  []string
		undoer bool
	}{
		{n.deviceList(), "tailnet.device.list", core.ClassRead, nil, false},
		{n.deviceAuthorize(), "tailnet.device.authorize", core.ClassCompensable, []string{"tailnet"}, true},
		{n.deviceSetRoutes(), "tailnet.device.set-routes", core.ClassCompensable, []string{"tailnet"}, true},
	}
	for _, tt := range tests {
		s := tt.def.Spec
		if s.Name != tt.name || s.Version != 1 || s.Class != tt.class || s.Side != core.SideController || !slices.Equal(s.LockKeys, tt.locks) ||
			s.CompensatedBy != "" || s.ForwardOnly || (tt.def.Handler.Inverse != nil) != tt.undoer {
			t.Errorf("%s: spec %+v, undo handler %v; want class %s, locks %v, undo handler %v", tt.name, s, tt.def.Handler.Inverse != nil, tt.class, tt.locks, tt.undoer)
		}
	}
}
