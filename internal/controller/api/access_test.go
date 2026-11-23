package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/controller/events"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/ext"
)

// accModule has controller-side reads of each kind the choke points tell
// apart: fleet, filtered, and a fleet read raised to admin.
type accModule struct{}

func (accModule) Manifest() core.Manifest {
	h := demoHandler[demoInput, demoOutput]{}
	return core.Manifest{ID: "acc", Version: 1, Actions: []core.ActionDef{
		core.DefineRead(core.ActionSpec{Name: "acc.fleet.get", Version: 1, Side: core.SideController, HostData: core.HostDataFleet}, h),
		core.DefineRead(core.ActionSpec{Name: "acc.rows.list", Version: 1, Side: core.SideController, HostData: core.HostDataFiltered}, h),
		core.DefineRead(core.ActionSpec{Name: "acc.admin.get", Version: 1, Side: core.SideController, HostData: core.HostDataFleet,
			MinScope: core.ScopeAdmin}, h),
	}}
}

// The access fixture: group G holds h-g, group L holds the controller's own
// host h-local, and h-x is in no group.
const (
	accHostG     = "h-g"
	accHostX     = "h-x"
	accHostLocal = "h-local"
)

// accExec runs SQL on the harness database.
func accExec(t *testing.T, h *harness, q string, args ...any) {
	t.Helper()
	if _, err := h.db.Writer().Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

// accHarness is newHarnessWith the access fixture.
func accHarness(t *testing.T, mods ...core.Module) *harness {
	t.Helper()
	h := newHarnessWith(t, mods...)
	at := testStart.UTC().Format(time.RFC3339Nano)
	accExec(t, h, `INSERT INTO core_meta (key, value) VALUES ('local_host_id', ?)`, accHostLocal)
	for _, id := range []string{accHostG, accHostX, accHostLocal} {
		accExec(t, h, `INSERT INTO core_hosts (id, name, is_local, created_at) VALUES (?, ?, ?, ?)`, id, id, id == accHostLocal, at)
	}
	for group, host := range map[string]string{"G": accHostG, "L": accHostLocal} {
		accExec(t, h, `INSERT INTO core_host_groups (id, name, created_at) VALUES (?, ?, ?)`, group, "group "+group, at)
		accExec(t, h, `INSERT INTO core_host_group_members (group_id, host_id) VALUES (?, ?)`, group, host)
	}
	return h
}

// accMember creates an active member with grants ("<target>=<role>") and
// returns its user id and TOTP secret.
func accMember(t *testing.T, h *harness, name string, grants ...string) (string, string) {
	t.Helper()
	ctx := context.Background()
	_, secret, err := h.auth.CreateUser(ctx, name, testPassword)
	if err != nil {
		t.Fatal(err)
	}
	u, err := h.auth.ActivateUser(ctx, name, accCode(t, h, secret))
	if err != nil {
		t.Fatal(err)
	}
	accExec(t, h, `UPDATE core_users SET role = 'member' WHERE id = ?`, u.ID)
	for _, g := range grants {
		target, role, _ := strings.Cut(g, "=")
		accExec(t, h, `INSERT INTO core_user_grants (user_id, target, role) VALUES (?, ?, ?)`, u.ID, target, role)
	}
	return u.ID, secret
}

// accCode advances the clock one TOTP period and returns secret's code.
func accCode(t *testing.T, h *harness, secret string) string {
	t.Helper()
	h.clock.Advance(auth.TOTPPeriod)
	code, err := auth.TOTPCode(secret, h.clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	return code
}

// accSession signs name in and returns a credential that adds its session.
func accSession(t *testing.T, h *harness, name, secret string) func(*http.Request) *http.Request {
	t.Helper()
	res, err := h.auth.Login(context.Background(), auth.LoginRequest{Username: name, Password: testPassword,
		Code: accCode(t, h, secret), Source: "192.0.2.10"})
	if err != nil {
		t.Fatal(err)
	}
	return func(r *http.Request) *http.Request { return withSession(r, res.SessionID, res.CSRFToken) }
}

// accPrincipal is a named credential of TestChokePoints.
type accPrincipal struct {
	name string
	cred func(*http.Request) *http.Request
}

// accPrincipals are the principals of TestChokePoints, in the order of its
// want columns.
func accPrincipals(t *testing.T, h *harness) []accPrincipal {
	t.Helper()
	token := func(scope core.Scope) func(*http.Request) *http.Request {
		tok := h.token(scope)
		return func(r *http.Request) *http.Request { return withToken(r, tok) }
	}
	member := func(name string, grants ...string) accPrincipal {
		_, secret := accMember(t, h, name, grants...)
		return accPrincipal{name, accSession(t, h, name, secret)}
	}
	adminSession := accSession(t, h, "admin", h.secret)
	return []accPrincipal{
		{"admin session", adminSession},
		{"admin read token", token(core.ScopeRead)},
		{"admin operate token", token(core.ScopeOperate)},
		member("opg", "G=operator"),
		member("vstar", "*=viewer"),
		member("none"),
		member("opl", "L=operator"),
		member("opstar", "*=operator"),
	}
}

// accDispatcher answers every call the choke points let through.
func accDispatcher(h *harness) {
	d := h.disp
	changes := map[string]core.ChangeView{
		"c-g": {ID: "c-g", HostID: accHostG, Action: "demo.item.add", Version: 1, State: core.StatePending},
		"c-x": {ID: "c-x", HostID: accHostX, Action: "demo.item.add", Version: 1, State: core.StatePending},
	}
	change := func(_ context.Context, _ core.Principal, id string) (core.ChangeView, error) {
		if v, ok := changes[id]; ok {
			return v, nil
		}
		return core.ChangeView{}, errcode.New(errcode.ChangeNotFound, "no change "+id)
	}
	d.ReadFunc = func(context.Context, core.Principal, string, core.ActionRef, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"ok":true}`), nil
	}
	d.ResourceFunc = func(context.Context, core.Principal, string, string) (json.RawMessage, error) {
		return json.RawMessage(`{}`), nil
	}
	d.PlanFunc = func(_ context.Context, _ core.Principal, hostID string, a core.ActionRef, _ json.RawMessage, _ core.PlanOptions) (core.PlanView, error) {
		return core.PlanView{PlanID: "p1", HostID: hostID, Action: a.Name, Version: a.Version}, nil
	}
	d.ApplyFunc = func(_ context.Context, _ core.Principal, hostID string, a core.ActionRef, _ string, _ core.ApplyOptions) (core.ChangeView, error) {
		return core.ChangeView{ID: "c-new", HostID: hostID, Action: a.Name, Version: a.Version, State: core.StatePending}, nil
	}
	d.ChangeFunc, d.ConfirmFunc, d.RevertFunc, d.UndoFunc = change, change, change, change
	d.ChangesFunc = func(context.Context, core.Principal, core.ChangeFilter) ([]core.ChangeView, error) {
		return []core.ChangeView{changes["c-g"], changes["c-x"]}, nil
	}
	d.EffectsFunc = func(context.Context, core.Principal, string) ([]core.EffectView, error) { return nil, nil }
	d.StepsFunc = func(context.Context, core.Principal, string) ([]core.StepView, error) { return nil, nil }
	d.ReleaseFunc = func(ctx context.Context, p core.Principal, id, _ string) (core.ReleaseResult, error) {
		v, err := change(ctx, p, id)
		return core.ReleaseResult{Change: v}, err
	}
	d.ObserveFunc = func(context.Context, core.Principal, string, int) (core.ObserveResult, error) {
		return core.ObserveResult{}, nil
	}
	d.ResolveFunc = func(context.Context, core.Principal, string, int, core.ResolveRequest) (core.EffectView, error) {
		return core.EffectView{}, nil
	}
	d.RevealFunc = func(context.Context, core.Principal, string, core.ActionRef, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"value":{}}`), nil
	}
}

func TestChokePoints(t *testing.T) {
	h := accHarness(t, accModule{}, mscModule{}, arvModule{})
	h.cfg.Availability = func(context.Context, string, string) (core.ModuleAvailability, error) {
		return core.ModuleAvailability{}, nil
	}
	srv, err := New(h.cfg)
	if err != nil {
		t.Fatal(err)
	}
	h.srv = srv
	accDispatcher(h)
	principals := accPrincipals(t, h)

	const in = `{"input": {"name": "a"}}`
	hostAction := func(host, name, op string) string {
		return "/api/v1/hosts/" + host + "/actions/" + name + "/v1/" + op
	}
	// want holds one status per principal, in accPrincipals' order: admin
	// session, admin read token, admin operate token, operator on G, viewer
	// on *, no grants, operator on L (the controller's host), operator on *.
	cases := []struct {
		name, method, path, body string
		want                     [8]int
	}{
		{"agent read on a G host", "POST", hostAction(accHostG, "demo.item.get", "read"), in,
			[8]int{200, 200, 200, 200, 200, 404, 404, 200}},
		{"agent revertible apply on a G host", "POST", hostAction(accHostG, "demo.item.add", "apply"), `{"planId": "p1"}`,
			[8]int{200, 403, 200, 200, 403, 404, 404, 200}},
		{"agent MinScope admin action", "POST", hostAction(accHostG, "msc.key.rotate", "plan"), in,
			[8]int{200, 403, 403, 403, 403, 404, 404, 403}},
		{"resource read", "GET", "/api/v1/hosts/" + accHostG + "/resources/demo/status", "",
			[8]int{200, 200, 200, 200, 200, 404, 404, 200}},
		{"module availability", "GET", "/api/v1/hosts/" + accHostG + "/modules/demo/availability", "",
			[8]int{200, 200, 200, 200, 200, 404, 404, 200}},
		{"fleet read", "POST", "/api/v1/actions/acc.fleet.get/v1/read", in,
			[8]int{200, 200, 200, 403, 403, 403, 403, 403}},
		{"filtered read", "POST", "/api/v1/actions/acc.rows.list/v1/read", in,
			[8]int{200, 200, 200, 200, 200, 403, 200, 200}},
		{"MinScope admin fleet read", "POST", "/api/v1/actions/acc.admin.get/v1/read", in,
			[8]int{200, 403, 403, 403, 403, 403, 403, 403}},
		{"read with no HostData", "POST", "/api/v1/actions/demo.item.list/v1/read", in,
			[8]int{200, 200, 200, 403, 403, 403, 403, 403}},
		{"controller-side mutating action", "POST", "/api/v1/actions/demo.item.remove/v1/plan", in,
			[8]int{200, 403, 403, 403, 403, 403, 403, 403}},
		{"reveal", "POST", hostAction(accHostG, "arv.key.reveal", "reveal"), in,
			[8]int{200, 403, 403, 403, 403, 403, 403, 403}},
		{"change list", "GET", "/api/v1/changes", "",
			[8]int{200, 200, 200, 200, 200, 200, 200, 200}},
		{"change list of a G host", "GET", "/api/v1/changes?hostId=" + accHostG, "",
			[8]int{200, 200, 200, 200, 200, 200, 200, 200}},
		{"change show on a G host", "GET", "/api/v1/changes/c-g", "",
			[8]int{200, 200, 200, 200, 200, 404, 404, 200}},
		{"change confirm on a G host", "POST", "/api/v1/changes/c-g/confirm", "",
			[8]int{200, 403, 200, 200, 403, 404, 404, 200}},
		{"change revert on a G host", "POST", "/api/v1/changes/c-g/revert", "",
			[8]int{200, 403, 200, 200, 403, 404, 404, 200}},
		{"change undo on a G host", "POST", "/api/v1/changes/c-g/undo", "",
			[8]int{200, 403, 200, 200, 403, 404, 404, 200}},
		{"change effects on a G host", "GET", "/api/v1/changes/c-g/effects", "",
			[8]int{200, 200, 200, 200, 200, 404, 404, 200}},
		{"change steps on a G host", "GET", "/api/v1/changes/c-g/steps", "",
			[8]int{200, 200, 200, 200, 200, 404, 404, 200}},
		{"change release", "POST", "/api/v1/changes/c-g/release", `{"reason": "gone"}`,
			[8]int{200, 403, 403, 403, 403, 403, 403, 403}},
		{"effect observe", "GET", "/api/v1/changes/c-g/effects/1/observe", "",
			[8]int{200, 403, 403, 403, 403, 403, 403, 403}},
		{"effect resolve", "POST", "/api/v1/changes/c-g/effects/1/resolve", `{"resolution": "accept"}`,
			[8]int{200, 403, 403, 403, 403, 403, 403, 403}},
		{"agent read on the controller's host", "POST", hostAction(accHostLocal, "demo.item.get", "read"), in,
			[8]int{200, 200, 200, 404, 200, 404, 200, 200}},
		{"agent apply on the controller's host", "POST", hostAction(accHostLocal, "demo.item.add", "apply"), `{"planId": "p1"}`,
			[8]int{200, 403, 200, 404, 403, 404, 403, 403}},
		{"agent plan on the controller's host", "POST", hostAction(accHostLocal, "demo.item.add", "plan"), in,
			[8]int{200, 403, 200, 404, 403, 404, 403, 403}},
	}
	codes := map[int]errcode.Code{403: errcode.Forbidden, 404: errcode.NotFound}
	for _, c := range cases {
		for i, p := range principals {
			rec := h.serve(p.cred(request(c.method, c.path, c.body)))
			want := c.want[i]
			if rec.Code != want {
				t.Errorf("%s, %s: %d %s, want %d", c.name, p.name, rec.Code, rec.Body, want)
				continue
			}
			if code, ok := codes[want]; ok && strings.HasPrefix(c.path, "/api/v1/changes/c-") && want == 404 {
				decodeProblem(t, rec, want, errcode.ChangeNotFound)
			} else if ok {
				decodeProblem(t, rec, want, code)
			}
		}
	}

	// A member's change list leaves out the change on a host outside its
	// grants; an admin's holds both.
	for i, want := range map[int]string{0: "c-x", 3: "c-g"} {
		body := h.serve(principals[i].cred(request("GET", "/api/v1/changes", ""))).Body.String()
		if !strings.Contains(body, `"`+want+`"`) {
			t.Errorf("change list of %s: %s, want %s in it", principals[i].name, body, want)
		}
	}
	if body := h.serve(principals[3].cred(request("GET", "/api/v1/changes", ""))).Body.String(); strings.Contains(body, "c-x") {
		t.Errorf("change list of operator on G: %s, want no c-x", body)
	}
	// A member with no grants sees an empty list, never a refusal.
	for _, path := range []string{"/api/v1/changes", "/api/v1/changes?hostId=" + accHostG} {
		if body := h.serve(principals[5].cred(request("GET", path, ""))).Body.String(); strings.Contains(body, "c-g") || strings.Contains(body, "c-x") {
			t.Errorf("%s of a member with no grants: %s, want no changes", path, body)
		}
	}
}

func TestEventsServeAMemberWithNoGrants(t *testing.T) {
	h := accHarness(t)
	bus := events.NewBus(nil)
	h.cfg.Events, h.cfg.Clock = bus, h.clock
	srv, err := New(h.cfg)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	id, _ := accMember(t, h, "none")
	_, token, err := h.auth.CreateToken(context.Background(), id, "events", core.ScopeRead, 0)
	if err != nil {
		t.Fatal(err)
	}
	resp, rd := sseOpen(t, ts.URL, token, "stale")
	defer func() { _ = resp.Body.Close() }()
	if got := sseNext(t, rd); !strings.Contains(got, "event: reset") {
		t.Fatalf("first frame %q, want a reset", got)
	}
	for _, host := range []string{accHostG, accHostX, ""} {
		bus.Publish(events.TypeHost, events.HostEvent{HostID: host, Online: true})
	}
	got := make(chan string, 1)
	go func() {
		line, _ := rd.ReadString('\n')
		got <- line
	}()
	select {
	case line := <-got:
		t.Fatalf("a member with no grants received %q, want no events", line)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestMetaRoutesAcceptAnyPrincipal(t *testing.T) {
	h := accHarness(t)
	_, secret := accMember(t, h, "none")
	member := accSession(t, h, "none", secret)
	read, operate := h.token(core.ScopeRead), h.token(core.ScopeOperate)
	creds := map[string]func(*http.Request) *http.Request{
		"member session":      member,
		"admin read token":    func(r *http.Request) *http.Request { return withToken(r, read) },
		"admin operate token": func(r *http.Request) *http.Request { return withToken(r, operate) },
	}
	for name, cred := range creds {
		for _, path := range []string{healthzPath, "/api/v1/openapi.json", "/api/v1/controller/listeners"} {
			rec := h.serve(cred(request("GET", path, "")))
			if rec.Code != http.StatusOK {
				t.Errorf("%s, %s: %d %s, want 200", name, path, rec.Code, rec.Body)
			}
			if path == healthzPath && !strings.Contains(rec.Body.String(), `"schemaVersion"`) {
				t.Errorf("%s, %s: %s, want the detail", name, path, rec.Body)
			}
		}
	}
}

func TestStreamsReResolveAccess(t *testing.T) {
	h := accHarness(t)
	bus := events.NewBus(nil)
	h.cfg.Events, h.cfg.Clock = bus, h.clock
	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pw.Close() })
	opened := make(chan struct{}, 1)
	h.disp.OpenStreamFunc = func(context.Context, core.Principal, string, string, string, url.Values) (io.ReadCloser, error) {
		opened <- struct{}{}
		return &relayBody{Reader: pr}, nil
	}
	srv, err := New(h.cfg)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	accExec(t, h, `INSERT INTO core_host_group_members (group_id, host_id) VALUES ('G', ?)`, accHostX)
	id, _ := accMember(t, h, "viewer", "G=viewer")
	_, token, err := h.auth.CreateToken(context.Background(), id, "streams", core.ScopeRead, 0)
	if err != nil {
		t.Fatal(err)
	}

	stream := relayGet(t, ts, "/api/v1/hosts/"+accHostG+"/streams/sm/logs", token)
	if stream.StatusCode != http.StatusOK {
		t.Fatalf("host stream = %d, want 200", stream.StatusCode)
	}
	<-opened
	resp, rd := sseOpen(t, ts.URL, token, "")
	defer func() { _ = resp.Body.Close() }()
	publish := func() {
		for _, host := range []string{accHostG, accHostX, ""} {
			bus.Publish(events.TypeHost, events.HostEvent{HostID: host, Online: true})
		}
	}
	hostFrame := func(seq uint64, host string) string {
		return fmt.Sprintf("id: %s\nevent: host\ndata: {\"hostId\":%q,\"online\":true}", events.FormatID(bus.Epoch(), seq), host)
	}
	// A composite's step on h-g is not shown: the composite is tied to no host.
	bus.Publish(events.TypeCompositeStep, map[string]string{"changeId": "c-comp", "stepId": "s1", "hostId": accHostG})
	publish()
	for _, want := range []string{hostFrame(2, accHostG), hostFrame(3, accHostX)} {
		if got := sseNext(t, rd); got != want {
			t.Fatalf("frame %q, want %q", got, want)
		}
	}

	// h-g leaves G: at the next recheck the host stream ends revoked, and the
	// events stream sends one reset and from then on only h-x's events.
	accExec(t, h, `DELETE FROM core_host_group_members WHERE group_id = 'G' AND host_id = ?`, accHostG)
	frames := make(chan []string, 1)
	go func() { frames <- relayFrames(stream.Body) }()
	deadline := time.After(10 * time.Second)
	var got []string
	for got == nil {
		select {
		case got = <-frames:
		case <-deadline:
			t.Fatal("the host stream did not end")
		case <-time.After(5 * time.Millisecond):
			h.clock.Advance(SSERecheck)
		}
	}
	if len(got) != 1 || !strings.HasPrefix(got[0], `end {"reason":"revoked"`) {
		t.Errorf("host stream frames %q, want one end frame with reason revoked", got)
	}
	if got := sseNext(t, rd); got != "event: reset\ndata: {}" {
		t.Fatalf("frame after the recheck %q, want one reset", got)
	}
	publish()
	if got, want := sseNext(t, rd), hostFrame(6, accHostX); got != want {
		t.Fatalf("frame %q, want %q", got, want)
	}

	// The events stream ends only once the user is gone.
	if err := h.auth.DeleteUser(context.Background(), "viewer"); err != nil {
		t.Fatal(err)
	}
	ended := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, rd)
		close(ended)
	}()
	deadline = time.After(10 * time.Second)
	for done := false; !done; {
		select {
		case <-ended:
			done = true
		case <-deadline:
			t.Fatal("the events stream did not end once its user was deleted")
		case <-time.After(5 * time.Millisecond):
			h.clock.Advance(SSERecheck)
		}
	}
}

func TestVisibleChangeRefusesOtherScopes(t *testing.T) {
	h := accHarness(t)
	h.disp.ChangeFunc = func(context.Context, core.Principal, string) (core.ChangeView, error) {
		return core.ChangeView{ID: "c1", HostID: accHostG}, nil
	}
	srv, err := New(h.cfg)
	if err != nil {
		t.Fatal(err)
	}
	r := request("GET", "/api/v1/changes/c1", "")
	r = r.WithContext(withCaller(r.Context(), caller{principal: core.Principal{Actor: "system:test", Scope: core.ScopeAdmin}}))
	if _, _, err := srv.visibleChange(r, core.ScopeRead); err != nil {
		t.Fatalf("visibleChange(read) as admin = %v, want the change", err)
	}
	_, _, err = srv.visibleChange(r, core.ScopeOperate)
	if got := errcode.From(err).Code; err == nil || got != errcode.Internal {
		t.Fatalf("visibleChange(operate) = %v, want an internal error", err)
	}
}

func TestExtensionRoutesNeedAllHosts(t *testing.T) {
	h := accHarness(t)
	h.cfg.Extensions, h.cfg.Clock = []ext.Route{{Surface: ext.SurfaceWeb, Method: http.MethodGet, Path: "/api/v1/xr/items",
		Summary: "List items", Scope: core.ScopeRead, Handler: xrHandler}}, h.clock
	srv, err := New(h.cfg)
	if err != nil {
		t.Fatal(err)
	}
	h.srv = srv
	_, viewer := accMember(t, h, "vstar", "*=viewer")
	_, operator := accMember(t, h, "opg", "G=operator")
	if rec := h.serve(accSession(t, h, "vstar", viewer)(request("GET", "/api/v1/xr/items", ""))); rec.Code != http.StatusOK {
		t.Errorf("viewer on *: %d %s, want 200", rec.Code, rec.Body)
	}
	rec := h.serve(accSession(t, h, "opg", operator)(request("GET", "/api/v1/xr/items", "")))
	decodeProblem(t, rec, http.StatusForbidden, errcode.Forbidden)
}
