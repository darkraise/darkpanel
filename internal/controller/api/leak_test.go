package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/controller/coreactions"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/install"
	"github.com/darkraise/darkpanel/internal/modules"
	"github.com/darkraise/darkpanel/internal/modules/derp"
	"github.com/darkraise/darkpanel/internal/modules/tailscale"
	"github.com/darkraise/darkpanel/internal/registry"
)

// engineDispatcher serves reads through a real controller-side engine and
// everything else as the fake does.
type engineDispatcher struct {
	*coretest.FakeDispatcher
	eng *lifecycle.Engine
}

func (d engineDispatcher) Read(ctx context.Context, p core.Principal, _ string, a core.ActionRef, input json.RawMessage) (json.RawMessage, error) {
	return d.eng.Read(ctx, p, a, input)
}

func TestNoControllerReadLeaksAnotherHost(t *testing.T) {
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
	clock := coretest.NewFakeClock(testStart)
	audit := &coretest.MemAudit{}
	authSvc := auth.New(db, coretest.Sealer{}, clock, audit, auth.Options{Params: testParams})

	at := testStart.UTC().Format("2006-01-02T15:04:05.000000000Z")
	for _, q := range []string{
		`INSERT INTO core_hosts (id, name, is_local, created_at) VALUES ('h-in', 'inside-host', 0, '` + at + `')`,
		`INSERT INTO core_hosts (id, name, is_local, created_at) VALUES ('h-out', 'outside-host', 0, '` + at + `')`,
		`INSERT INTO core_host_groups (id, name, created_at) VALUES ('G', 'inside', '` + at + `')`,
		`INSERT INTO core_host_group_members (group_id, host_id) VALUES ('G', 'h-in')`,
		// A rollout over both hosts that failed on h-out, whose error names
		// h-out, and one over h-out alone.
		`INSERT INTO core_rollouts (id, version, channel, state, outcome, error_code, error_message, created_at, updated_at)
			VALUES ('r-both', '1.2.0', 'stable', 'failed', 'failed', 'update.interrupted', 'host h-out: it broke', '` + at + `', '` + at + `')`,
		`INSERT INTO core_rollout_hosts (rollout_id, host_id, position, state) VALUES ('r-both', 'h-in', 0, 'committed')`,
		`INSERT INTO core_rollout_hosts (rollout_id, host_id, position, state, error_code, error_message)
			VALUES ('r-both', 'h-out', 1, 'failed', 'update.interrupted', 'it broke')`,
		`INSERT INTO core_rollouts (id, version, channel, state, created_at, updated_at)
			VALUES ('r-out', '1.2.0', 'stable', 'succeeded', '` + at + `', '` + at + `')`,
		`INSERT INTO core_rollout_hosts (rollout_id, host_id, position, state) VALUES ('r-out', 'h-out', 0, 'committed')`,
		`INSERT INTO derp_servers (host_id, mode, host_name, derper_version, unit_active, checked_at)
			VALUES ('h-in', 'ip', '192.0.2.1', 'v1.104.1', 1, '` + at + `')`,
		`INSERT INTO derp_servers (host_id, mode, host_name, derper_version, unit_active, checked_at)
			VALUES ('h-out', 'ip', '100.64.0.2', 'v1.104.1', 1, '` + at + `')`,
	} {
		if _, err := db.Writer().Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	for _, host := range []string{"h-in", "h-out"} {
		e := core.AuditEntry{ID: "a-" + host, Actor: "user:x", HostID: host, Action: "demo.write", Version: 1,
			Phase: "apply", Result: "ok", At: testStart, Input: json.RawMessage(`{}`)}
		if err := audit.Record(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	agents := coretest.MapHosts{"h-in": leakAgent("100.64.0.1"), "h-out": leakAgent("100.64.0.2")}

	_, secret, err := authSvc.CreateUser(ctx, "member", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	code, err := auth.TOTPCode(secret, clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	member, err := authSvc.ActivateUser(ctx, "member", code)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`UPDATE core_users SET role = 'member' WHERE id = '` + member.ID + `'`,
		`INSERT INTO core_user_grants (user_id, target, role) VALUES ('` + member.ID + `', 'G', 'viewer')`,
	} {
		if _, err := db.Writer().Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	_, token, err := authSvc.CreateToken(ctx, member.ID, "leak", core.ScopeRead, 0)
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(auth.TOTPPeriod)
	code, err = auth.TOTPCode(secret, clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	login, err := authSvc.Login(ctx, auth.LoginRequest{Username: "member", Password: testPassword, Code: code, Source: "192.0.2.10"})
	if err != nil {
		t.Fatal(err)
	}
	creds := map[string]func(*http.Request) *http.Request{
		"session": func(r *http.Request) *http.Request { return withSession(r, login.SessionID, login.CSRFToken) },
		"token":   func(r *http.Request) *http.Request { return withToken(r, token) },
	}

	reg, err := registry.BuildWith(coreactions.Module(coreactions.Deps{Auth: authSvc, Audit: audit,
		Modules: modules.All(), Extra: registry.CoreActions(nil, ""), OwnedPaths: install.CoreOwnedPaths()}))
	if err != nil {
		t.Fatal(err)
	}
	eng, err := lifecycle.New(lifecycle.Config{
		Side: core.SideController, Registry: reg, Validator: schema.NewValidator(), Journal: &coretest.MemJournal{},
		Redact: schema.Redact,
		Base: core.Env{DB: db, Sealer: coretest.Sealer{}, Clock: clock, Log: slog.New(slog.DiscardHandler),
			Agents: agents},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(eng.Close)
	log, _ := testLog()
	srv, err := New(Config{Dispatcher: engineDispatcher{&coretest.FakeDispatcher{}, eng}, Auth: authSvc, Registry: reg,
		WebCA: fakeWebCA{}, SchemaVersion: func(context.Context) (int, error) { return 3, nil },
		HostID: testHostID, UI: testUI, Log: log})
	if err != nil {
		t.Fatal(err)
	}

	// Reads whose fixture holds an h-in row. The device, tunnel and hostname
	// rows are tied to no host and are hidden from members whole; the module
	// and token lists answer about the named host or the caller alone.
	wantIn := map[string]bool{"core.host.list": true, "core.audit.list": true, "core.update.rollouts": true,
		"npm.instance.list": true, "derp.server.list": true, "derp.server.probe": true}
	leaks := []string{"h-out", "outside-host", "100.64.0.2"}
	for how, credential := range creds {
		served := map[string]bool{}
		for _, def := range reg.Actions() {
			s := def.Spec
			if s.Class != core.ClassRead || s.Side != core.SideController || s.Internal || s.BreakGlass || s.Reveal {
				continue
			}
			input := `{}`
			if s.HostData == core.HostDataInput {
				input = `{"hostId": "h-in"}`
			}
			r := httptest.NewRequest(http.MethodPost, "/api/v1/actions/"+s.Name+"/v1/read",
				strings.NewReader(`{"input": `+input+`}`))
			r.Header.Set("Content-Type", "application/json")
			r.RemoteAddr = "192.0.2.10:1234"
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, credential(r))
			body := rec.Body.String()
			if s.HostData == core.HostDataFleet {
				if rec.Code != http.StatusForbidden || !strings.Contains(body, "auth.forbidden") {
					t.Errorf("%s, %s (fleet): %d %s, want 403 auth.forbidden", how, s.Name, rec.Code, body)
				}
				continue
			}
			if rec.Code != http.StatusOK {
				t.Errorf("%s, %s (%s): %d %s, want 200", how, s.Name, s.HostData, rec.Code, body)
				continue
			}
			served[s.Name] = true
			if wantIn[s.Name] && !strings.Contains(body, "h-in") {
				t.Errorf("%s, %s: body %s holds no h-in row", how, s.Name, body)
			}
			for _, leak := range leaks {
				if strings.Contains(body, leak) {
					t.Errorf("%s, %s: body leaks %q: %s", how, s.Name, leak, body)
				}
			}
		}
		for name := range wantIn {
			if !served[name] {
				t.Errorf("%s: %s was not served to the member", how, name)
			}
		}
	}
}

// leakAgent answers a host's tailscale.status with a node at addr and its
// derp.server.status as an installed server with no probe target, so the
// probe measures nothing over the network; every other read fails.
func leakAgent(addr string) *coretest.FakeAgent {
	return &coretest.FakeAgent{
		ResourceFunc: func(_ context.Context, _ core.Principal, name string) (json.RawMessage, error) {
			if name != "tailscale.status" {
				return nil, errcode.New(errcode.NotFound, "no resource "+name)
			}
			return json.Marshal(tailscale.Status{BackendState: "Running", Self: &tailscale.Node{Addresses: []string{addr}}})
		},
		ReadFunc: func(_ context.Context, _ core.Principal, a core.ActionRef, input json.RawMessage) (json.RawMessage, error) {
			if a.Name != "derp.server.status" {
				return nil, errcode.New(errcode.NotFound, "no read "+a.Name)
			}
			var in derp.ServerStatusInput
			if err := json.Unmarshal(input, &in); err != nil {
				return nil, err
			}
			st := derp.ServerStatus{Installed: true, Tags: []string{}, Addresses: []string{addr}, Visible: []derp.Visibility{}}
			for _, w := range in.Whois {
				st.Visible = append(st.Visible, derp.Visibility{Address: w, Visible: true, Node: "node-" + w})
			}
			return json.Marshal(st)
		},
	}
}
