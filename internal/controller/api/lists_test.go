package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/controller/audit"
	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/controller/coreactions"
	"github.com/darkraise/darkpanel/internal/controller/dispatch"
	"github.com/darkraise/darkpanel/internal/controller/events"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/install"
	"github.com/darkraise/darkpanel/internal/modules"
	"github.com/darkraise/darkpanel/internal/registry"
)

// controllerDB is a migrated controller database with every module's tables.
func controllerDB(t *testing.T) *store.DB {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(dir, "controller.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	coreSet, err := store.CoreMigrations(store.SideController)
	if err != nil {
		t.Fatal(err)
	}
	var sets []store.MigrationSet
	for _, m := range modules.All() {
		man := m.Manifest()
		if fsys, ok := man.Migrations[core.SideController]; ok && fsys != nil {
			sets = append(sets, store.MigrationSet{Module: man.ID, FS: fsys})
		}
	}
	opts := store.MigrateOptions{Side: store.SideController, BinaryVersion: "test",
		BackupRoot: filepath.Join(dir, "backups"), Keep: 5, Now: testStart}
	if res, err := store.Migrate(ctx, db, opts, coreSet, sets); err != nil || len(res.Failed) != 0 {
		t.Fatalf("migrate: %+v, %v", res, err)
	}
	return db
}

// listsEnv is a controller with the real dispatcher and engine over fake
// agents, each listing the changes the test gives its host.
type listsEnv struct {
	t       *testing.T
	db      *store.DB
	audit   *audit.Store
	clock   *coretest.FakeClock
	bus     *events.Bus
	srv     *Server
	changes map[string][]core.ChangeView // each host's changes, newest first
	seq     int
	admin   string // the admin's user id
	tokens  map[string]string
}

func listHost(i int) string { return fmt.Sprintf("h-%03d", i) }

// newListsEnv has hosts hosts, group G of the first two, an admin and a
// member with viewer on G, each with an admin or read token.
func newListsEnv(t *testing.T, hosts int) *listsEnv {
	t.Helper()
	ctx := context.Background()
	e := &listsEnv{t: t, db: controllerDB(t), clock: coretest.NewFakeClock(testStart), bus: events.NewBus(nil),
		changes: map[string][]core.ChangeView{}, tokens: map[string]string{}}
	e.audit = audit.New(e.db, e.clock)
	authSvc := auth.New(e.db, coretest.Sealer{}, e.clock, e.audit, auth.Options{Params: testParams})
	clients := dispatch.NewStaticClients()
	for i := range hosts {
		id := listHost(i)
		e.exec(`INSERT INTO core_hosts (id, name, is_local, created_at) VALUES (?, ?, 0, ?)`, id, "host-"+id, e.at())
		clients.Add(id, e.agent(id))
	}
	e.exec(`INSERT INTO core_host_groups (id, name, created_at) VALUES ('G', 'visible', ?)`, e.at())
	e.exec(`INSERT INTO core_host_group_members (group_id, host_id) VALUES ('G', ?), ('G', ?)`, listHost(0), listHost(1))

	for _, name := range []string{"admin", "member"} {
		_, secret, err := authSvc.CreateUser(ctx, name, testPassword)
		if err != nil {
			t.Fatal(err)
		}
		e.clock.Advance(auth.TOTPPeriod)
		code, err := auth.TOTPCode(secret, e.clock.Now())
		if err != nil {
			t.Fatal(err)
		}
		u, err := authSvc.ActivateUser(ctx, name, code)
		if err != nil {
			t.Fatal(err)
		}
		scope := core.ScopeAdmin
		if name == "member" {
			e.exec(`UPDATE core_users SET role = 'member' WHERE id = ?`, u.ID)
			e.exec(`INSERT INTO core_user_grants (user_id, target, role) VALUES (?, 'G', 'viewer')`, u.ID)
			scope = core.ScopeRead
		} else {
			e.admin = u.ID
		}
		if _, e.tokens[name], err = authSvc.CreateToken(ctx, u.ID, "lists", scope, 0); err != nil {
			t.Fatal(err)
		}
		e.tokens[name+"-id"] = u.ID
	}

	reg, err := registry.BuildWith(coreactions.Module(coreactions.Deps{Auth: authSvc, Audit: e.audit,
		Modules: modules.All(), Extra: registry.CoreActions(nil, ""), OwnedPaths: install.CoreOwnedPaths()}), nil)
	if err != nil {
		t.Fatal(err)
	}
	discard := slog.New(slog.DiscardHandler)
	agents := dispatch.NewHosts(dispatch.HostsConfig{DB: e.db, Registry: reg, Clients: clients, Clock: e.clock, Log: discard})
	eng, err := lifecycle.New(lifecycle.Config{
		Side: core.SideController, Registry: reg, Validator: schema.NewValidator(), Journal: &coretest.MemJournal{},
		Redact: schema.Redact,
		Base:   core.Env{DB: e.db, Sealer: coretest.Sealer{}, Clock: e.clock, Log: discard, Agents: agents},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(eng.Close)
	disp, err := dispatch.New(dispatch.Config{Registry: reg, Engine: eng, Hosts: agents, DB: e.db, Clock: e.clock, Log: discard})
	if err != nil {
		t.Fatal(err)
	}
	log, _ := testLog()
	if e.srv, err = New(Config{Dispatcher: disp, Auth: authSvc, Registry: reg, WebCA: fakeWebCA{},
		SchemaVersion: func(context.Context) (int, error) { return 3, nil }, HostID: testHostID, UI: testUI,
		Log: log, Events: e.bus, Clock: e.clock}); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *listsEnv) at() string { return e.clock.Now().UTC().Format("2006-01-02T15:04:05.000000000Z") }

func (e *listsEnv) exec(q string, args ...any) {
	e.t.Helper()
	if _, err := e.db.Writer().Exec(q, args...); err != nil {
		e.t.Fatalf("%s: %v", q, err)
	}
}

func (e *listsEnv) nextID(prefix string) string {
	e.seq++
	return fmt.Sprintf("%s%06d", prefix, e.seq)
}

// agent answers Hello as id and lists id's changes as an agent's engine does.
func (e *listsEnv) agent(id string) *coretest.FakeAgent {
	return &coretest.FakeAgent{
		HelloFunc: func(context.Context) (core.AgentHello, error) {
			return core.AgentHello{ProtocolVersion: 1, HostID: id}, nil
		},
		ChangesFunc: func(_ context.Context, f core.ChangeFilter) ([]core.ChangeView, error) {
			out := []core.ChangeView{}
			for _, v := range e.changes[id] {
				if f.Before != "" && v.ID >= f.Before {
					continue
				}
				if len(out) == f.Limit {
					break
				}
				out = append(out, v)
			}
			return out, nil
		},
	}
}

// agentChange adds a change on host, newer than every change so far.
func (e *listsEnv) agentChange(host string) string {
	id := e.nextID("c")
	v := core.ChangeView{ID: id, HostID: host, Action: "demo.item.add", Version: 1, Actor: "user:" + e.admin,
		Class: core.ClassRevertible, State: core.StateCompleted, CreatedAt: testStart, UpdatedAt: testStart}
	e.changes[host] = append([]core.ChangeView{v}, e.changes[host]...)
	return id
}

// controllerChange adds a host-less change of the controller's engine by
// actor; with stepHosts it is a composite with one step on each.
func (e *listsEnv) controllerChange(actor string, stepHosts ...string) string {
	id := e.nextID("c")
	action, class := "core.token.create", "compensable"
	if len(stepHosts) > 0 {
		action, class = "system.docker.install", "composite"
	}
	e.exec(`INSERT INTO core_plans (id, host_id, action, version, actor, input_hash, state_hash, diff_json, guards_json,
		confirmation, created_at, expires_at) VALUES (?, '', ?, 1, ?, 'h', 's', '[]', '[]', '', ?, ?)`,
		"P"+id, action, actor, e.at(), e.at())
	e.exec(`INSERT INTO core_changes (id, plan_id, host_id, action, version, class, compensated_by, actor, state,
		input_json, created_at, updated_at) VALUES (?, ?, '', ?, 1, ?, '', ?, 'completed', '{}', ?, ?)`,
		id, "P"+id, action, class, actor, e.at(), e.at())
	for i, host := range stepHosts {
		e.exec(`INSERT INTO core_composite_steps (change_id, step_id, ordinal, host_id, action, input_template, state, updated_at)
			VALUES (?, ?, ?, ?, 'system.docker.install/v1', '{}', 'applied', ?)`, id, fmt.Sprintf("s%d", i), i, host, e.at())
	}
	return id
}

// auditEntry records an audit row on host ("" for none) by actor.
func (e *listsEnv) auditEntry(host, actor string) string {
	id := e.nextID("a")
	if err := e.audit.Record(context.Background(), core.AuditEntry{ID: id, Actor: actor, HostID: host,
		Action: "demo.write", Version: 1, Phase: "apply", Result: "ok", At: testStart, Input: json.RawMessage(`{}`)}); err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *listsEnv) serve(who, method, path, body string) *httptest.ResponseRecorder {
	e.t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	r.RemoteAddr = "192.0.2.10:1234"
	rec := httptest.NewRecorder()
	e.srv.Handler().ServeHTTP(rec, withToken(r, e.tokens[who]))
	if rec.Code != http.StatusOK {
		e.t.Fatalf("%s %s %s: %d %s, want 200", who, method, path, rec.Code, rec.Body)
	}
	return rec
}

// changePages follows GET /api/v1/changes in pages of limit and returns the
// ids, failing when a page short of limit is followed by more rows.
func (e *listsEnv) changePages(who string, limit int) []string {
	e.t.Helper()
	var ids []string
	before := ""
	for {
		path := fmt.Sprintf("/api/v1/changes?limit=%d", limit)
		if before != "" {
			path += "&before=" + before
		}
		var page changesResponse
		if err := json.Unmarshal(e.serve(who, "GET", path, "").Body.Bytes(), &page); err != nil {
			e.t.Fatal(err)
		}
		for _, c := range page.Changes {
			ids = append(ids, c.ID)
		}
		if len(page.Changes) < limit {
			return ids
		}
		before = page.Changes[len(page.Changes)-1].ID
	}
}

func TestMemberListsShowOnlyVisibleHosts(t *testing.T) {
	e := newListsEnv(t, 200)
	var visible []string
	for i := range 60 {
		host := listHost(i * 3 % 200)
		if i%30 == 0 {
			host = listHost(i / 30)
		}
		id := e.agentChange(host)
		if host == listHost(0) || host == listHost(1) {
			visible = append(visible, id)
		}
	}
	slices.Reverse(visible)
	if got := e.changePages("member", 100); !slices.Equal(got, visible) {
		t.Errorf("member changes %v, want %v", got, visible)
	}
	if got := e.changePages("admin", 100); len(got) != 60 {
		t.Errorf("admin sees %d changes, want 60", len(got))
	}

	var hosts struct {
		Output core.HostList `json:"output"`
	}
	if err := json.Unmarshal(e.serve("member", "POST", "/api/v1/actions/core.host.list/v1/read", `{"input": {}}`).Body.Bytes(), &hosts); err != nil {
		t.Fatal(err)
	}
	if len(hosts.Output.Hosts) != 2 {
		t.Fatalf("member host list has %d hosts, want 2", len(hosts.Output.Hosts))
	}
	for _, h := range hosts.Output.Hosts {
		if h.Scope != core.ScopeRead || (h.ID != listHost(0) && h.ID != listHost(1)) {
			t.Errorf("member host row %s with scope %q, want h-000 or h-001 with read", h.ID, h.Scope)
		}
	}

	ts := httptest.NewServer(e.srv.Handler())
	t.Cleanup(ts.Close)
	resp, rd := sseOpen(t, ts.URL, e.tokens["member"], "")
	defer func() { _ = resp.Body.Close() }()
	for i := range 200 {
		e.bus.Publish(events.TypeHost, events.HostEvent{HostID: listHost(i), Online: true})
	}
	e.bus.Publish(events.TypeHost, events.HostEvent{HostID: listHost(1), Online: false})
	for _, want := range []string{`"hostId":"h-000","online":true`, `"hostId":"h-001","online":true`, `"hostId":"h-001","online":false`} {
		if got := sseNext(t, rd); !strings.Contains(got, want) {
			t.Fatalf("member event %q, want %s", got, want)
		}
	}
}

func TestMemberPagesAreFull(t *testing.T) {
	e := newListsEnv(t, 200)
	var visible, visibleAudit []string
	for i := range 40 {
		visible = append(visible, e.agentChange(listHost(i%2)))
		visibleAudit = append(visibleAudit, e.auditEntry(listHost(i%2), "user:"+e.admin))
	}
	for i := range 2600 {
		e.agentChange(listHost(2 + i%198))
		e.auditEntry(listHost(2+i%198), "user:"+e.admin)
	}
	adminChange := e.controllerChange("user:" + e.admin)
	ownChange := e.controllerChange("user:" + e.tokens["member-id"])
	composite := e.controllerChange("user:"+e.admin, listHost(0), listHost(150))
	adminAudit := e.auditEntry("", "user:"+e.admin)
	visible = append(visible, ownChange)
	slices.Reverse(visible)
	slices.Reverse(visibleAudit)

	if got := e.changePages("member", 25); !slices.Equal(got, visible) {
		t.Errorf("member change pages %v, want %v", got, visible)
	}
	first := e.changePages("admin", 500)[:3]
	if want := []string{composite, ownChange, adminChange}; !slices.Equal(first, want) {
		t.Errorf("admin's newest changes %v, want %v", first, want)
	}
	e.serve("member", "GET", "/api/v1/changes/"+ownChange, "")
	for _, id := range []string{adminChange, composite} {
		if rec := e.serveAny("member", "GET", "/api/v1/changes/"+id); rec.Code != http.StatusNotFound {
			t.Errorf("member GET change %s: %d %s, want 404", id, rec.Code, rec.Body)
		}
	}

	auditPages := func(who string) []string {
		var ids []string
		before := ""
		for {
			in := `{"limit": 25}`
			if before != "" {
				in = fmt.Sprintf(`{"limit": 25, "before": %q}`, before)
			}
			var out struct {
				Output coreactions.AuditList `json:"output"`
			}
			if err := json.Unmarshal(e.serve(who, "POST", "/api/v1/actions/core.audit.list/v1/read", `{"input": `+in+`}`).Body.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
			if len(out.Output.Entries) != 25 && out.Output.NextBefore != nil {
				t.Fatalf("%s audit page of %d entries with a next page, want 25", who, len(out.Output.Entries))
			}
			for _, a := range out.Output.Entries {
				ids = append(ids, a.ID)
			}
			if out.Output.NextBefore == nil {
				return ids
			}
			before = *out.Output.NextBefore
		}
	}
	if got := auditPages("member"); !slices.Equal(got, visibleAudit) {
		t.Errorf("member audit pages %v, want %v", got, visibleAudit)
	}
	var out struct {
		Output coreactions.AuditList `json:"output"`
	}
	if err := json.Unmarshal(e.serve("admin", "POST", "/api/v1/actions/core.audit.list/v1/read", `{"input": {"limit": 5}}`).Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(out.Output.Entries, func(a coreactions.AuditEntry) bool { return a.ID == adminAudit }) {
		t.Errorf("admin's newest audit entries %+v, want %s", out.Output.Entries, adminAudit)
	}
}

func (e *listsEnv) serveAny(who, method, path string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, nil)
	r.RemoteAddr = "192.0.2.10:1234"
	rec := httptest.NewRecorder()
	e.srv.Handler().ServeHTTP(rec, withToken(r, e.tokens[who]))
	return rec
}
