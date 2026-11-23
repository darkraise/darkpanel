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
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/install"
	"github.com/darkraise/darkpanel/internal/modules"
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

	reg, err := registry.BuildWith(coreactions.Module(coreactions.Deps{Auth: authSvc, Audit: audit,
		Modules: modules.All(), Extra: registry.CoreActions(nil, ""), OwnedPaths: install.CoreOwnedPaths()}))
	if err != nil {
		t.Fatal(err)
	}
	eng, err := lifecycle.New(lifecycle.Config{
		Side: core.SideController, Registry: reg, Validator: schema.NewValidator(), Journal: &coretest.MemJournal{},
		Redact: schema.Redact,
		Base: core.Env{DB: db, Sealer: coretest.Sealer{}, Clock: clock, Log: slog.New(slog.DiscardHandler),
			Agents: coretest.MapHosts{}},
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

	// Reads whose fixture holds an h-in row; the others are host-less rows
	// (devices, tunnels, hostnames) or answer about the caller alone.
	wantIn := map[string]bool{"core.host.list": true, "core.audit.list": true}
	leaks := []string{"h-out", "outside-host", "100.64.0.2"}
	served := 0
	for _, def := range reg.Actions() {
		s := def.Spec
		if s.Class != core.ClassRead || s.Side != core.SideController || s.Internal || s.BreakGlass || s.Reveal {
			continue
		}
		input := `{}`
		if s.HostData == core.HostDataInput {
			input = `{"hostId": "h-in"}`
		}
		if s.Name == "derp.server.probe" {
			input = `{"hostId": "h-in", "agents": []}`
		}
		r := httptest.NewRequest(http.MethodPost, "/api/v1/actions/"+s.Name+"/v1/read",
			strings.NewReader(`{"input": `+input+`}`))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		r.RemoteAddr = "192.0.2.10:1234"
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, r)
		body := rec.Body.String()
		if s.HostData == core.HostDataFleet {
			if rec.Code != http.StatusForbidden || !strings.Contains(body, "auth.forbidden") {
				t.Errorf("%s (fleet): %d %s, want 403 auth.forbidden", s.Name, rec.Code, body)
			}
			continue
		}
		// No DERP server runs on the fixture host, so the probe's own answer is
		// not found for h-in; its body is still checked for leaks below.
		probeMiss := s.Name == "derp.server.probe" && rec.Code == http.StatusNotFound && strings.Contains(body, "host h-in not found")
		if rec.Code != http.StatusOK && !probeMiss {
			t.Errorf("%s (%s): %d %s, want 200", s.Name, s.HostData, rec.Code, body)
			continue
		}
		served++
		if wantIn[s.Name] && !strings.Contains(body, "h-in") && !strings.Contains(body, "inside-host") {
			t.Errorf("%s: body %s holds no h-in row", s.Name, body)
		}
		for _, leak := range leaks {
			if strings.Contains(body, leak) {
				t.Errorf("%s: body leaks %q: %s", s.Name, leak, body)
			}
		}
	}
	if served == 0 {
		t.Error("no controller-side read was served to the member")
	}
}
