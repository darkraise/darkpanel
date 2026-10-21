package coreactions

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/core/secret"
	"github.com/darkraise/darkpanel/internal/core/store"
)

var t0 = time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)

// fakeModule is a compiled-in module with no actions, for the core.module
// actions to manage.
type fakeModule struct {
	id      string
	version int
}

func (f fakeModule) Manifest() core.Manifest { return core.Manifest{ID: f.id, Version: f.version} }

// testEnv is the controller side of a server: a migrated controller database,
// the identity service, an in-memory audit log, the core module and a
// lifecycle engine running it.
type testEnv struct {
	t      *testing.T
	db     *store.DB
	clock  *coretest.FakeClock
	auth   *auth.Service
	audit  *coretest.MemAudit
	hosts  coretest.MapHosts
	reg    *core.Registry
	engine *lifecycle.Engine
}

// root is the controller socket's principal; break-glass actions run as it.
var root = core.Principal{Actor: "local:root@h1", Scope: core.ScopeAdmin, Source: "socket"}

func newTestEnv(t *testing.T, mods ...core.Module) *testEnv {
	t.Helper()
	return newTestEnvWith(t, nil, mods...)
}

// newTestEnvWith is newTestEnv with adjust, when not nil, changing the core
// module's dependencies once the database, clock and services exist.
func newTestEnvWith(t *testing.T, adjust func(e *testEnv, d *Deps), mods ...core.Module) *testEnv {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(dir, "controller.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	set, err := store.CoreMigrations(store.SideController)
	if err != nil {
		t.Fatal(err)
	}
	opts := store.MigrateOptions{Side: store.SideController, BinaryVersion: "test",
		BackupRoot: filepath.Join(dir, "backups"), Keep: 5, Now: t0}
	if _, err := store.Migrate(ctx, db, opts, set, nil); err != nil {
		t.Fatal(err)
	}
	e := &testEnv{t: t, db: db, clock: coretest.NewFakeClock(t0), audit: &coretest.MemAudit{}, hosts: coretest.MapHosts{}}
	params := auth.Params{Memory: 8, Iterations: 1, Parallelism: 1, SaltLen: 16, KeyLen: 32}
	e.auth = auth.New(db, coretest.Sealer{}, e.clock, e.audit, auth.Options{Params: params})
	d := Deps{Auth: e.auth, Audit: e.audit, Modules: mods}
	if adjust != nil {
		adjust(e, &d)
	}
	coreModule := Module(d)
	if e.reg, err = core.NewRegistry(append([]core.Module{coreModule}, mods...)...); err != nil {
		t.Fatal(err)
	}
	e.engine, err = lifecycle.New(lifecycle.Config{
		Side: core.SideController, Registry: e.reg, Validator: schema.NewValidator(), Journal: e.audit,
		Redact: redact,
		Base: core.Env{DB: db, Sealer: coretest.Sealer{}, Clock: e.clock,
			Log: slog.New(slog.DiscardHandler), Agents: e.hosts},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.engine.Close)
	return e
}

// redact is the production redaction (plan 4 wires the same function).
func redact(t reflect.Type, raw json.RawMessage) json.RawMessage {
	s, err := schema.Output(t)
	if err != nil {
		return json.RawMessage(`"` + secret.Redacted + `"`)
	}
	return secret.RedactJSON(raw, s)
}

func ref(name string) core.ActionRef { return core.ActionRef{Name: name, Version: 1} }

func encodeInput(t *testing.T, in any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// read runs a read action and decodes its output into out.
func (e *testEnv) read(p core.Principal, name string, in, out any) error {
	raw, err := e.engine.Read(context.Background(), p, ref(name), encodeInput(e.t, in))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, out); err != nil {
		e.t.Fatalf("%s output %s: %v", name, raw, err)
	}
	return nil
}

func (e *testEnv) exec(query string, args ...any) {
	e.t.Helper()
	if _, err := e.db.Writer().Exec(query, args...); err != nil {
		e.t.Fatal(err)
	}
}

func (e *testEnv) addHost(id, name string, local bool) {
	e.t.Helper()
	e.exec(`INSERT INTO core_hosts (id, name, is_local, created_at) VALUES (?, ?, ?, ?)`,
		id, name, local, t0.Format(time.RFC3339))
}

func (e *testEnv) setModuleState(hostID, moduleID string, enabled, available bool, reason string) {
	e.t.Helper()
	var r any
	if reason != "" {
		r = reason
	}
	e.exec(`INSERT INTO core_module_state (host_id, module_id, enabled, available, reason, checked_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (host_id, module_id) DO UPDATE SET enabled = excluded.enabled,
		available = excluded.available, reason = excluded.reason, checked_at = excluded.checked_at`,
		hostID, moduleID, enabled, available, r, t0.Format(time.RFC3339))
}

func wantCode(t *testing.T, err error, code errcode.Code) {
	t.Helper()
	var e *errcode.Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("err = %v, want code %s", err, code)
	}
}

func ptr[T any](v T) *T { return &v }

// jsonEqual reports whether got and want are the same JSON value.
func jsonEqual(t *testing.T, got json.RawMessage, want string) bool {
	t.Helper()
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("got %s: %v", got, err)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("want %s: %v", want, err)
	}
	return reflect.DeepEqual(g, w)
}

func TestManifest(t *testing.T) {
	m := Module(Deps{}).Manifest()
	if m.ID != core.ModuleIDCore || m.Version != 1 || !m.DefaultEnabled {
		t.Errorf("Manifest = %+v, want id core, version 1, enabled by default", m)
	}
	if m.Resources != nil || m.Migrations != nil || m.Preflight != nil {
		t.Errorf("Manifest declares resources, migrations or preflight checks: %+v", m)
	}
	if _, err := core.NewRegistry(Module(Deps{})); err != nil {
		t.Errorf("the core module with zero Deps does not register: %v", err)
	}
}

func TestHostList(t *testing.T) {
	e := newTestEnv(t, fakeModule{"demo", 3}, fakeModule{"extra", 2})
	e.addHost("h2", "beta", false)
	e.addHost("h1", "alpha", true)
	e.hosts["h1"] = &coretest.FakeAgent{}
	e.setModuleState("h1", "demo", true, true, "")
	e.setModuleState("h1", "extra", true, false, "tool missing")
	e.setModuleState("h1", "gone", true, true, "")

	var got core.HostList
	if err := e.read(root, "core.host.list", NoInput{}, &got); err != nil {
		t.Fatal(err)
	}
	want := core.HostList{Hosts: []core.Host{
		{ID: "h1", Name: "alpha", IsLocal: true, State: core.HostPending, Online: true, Modules: []core.ModuleState{
			{ID: "demo", Version: 3, Enabled: true, Available: true, CheckedAt: ptr(t0)},
			{ID: "extra", Version: 2, Enabled: true, Reason: ptr("tool missing"), CheckedAt: ptr(t0)},
		}},
		{ID: "h2", Name: "beta", State: core.HostPending, Modules: []core.ModuleState{
			{ID: "demo", Version: 3, Reason: ptr(ReasonNotReported)},
			{ID: "extra", Version: 2, Reason: ptr(ReasonNotReported)},
		}},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("hosts = %+v\nwant    %+v", got, want)
	}
}

// TestHostListReportsAgents covers the fields the agents' Hello and sessions
// supply: a host is online only while the directory hands out its client,
// and supported only when its last Hello is inside the window.
func TestHostListReportsAgents(t *testing.T) {
	e := newTestEnvWith(t, func(_ *testEnv, d *Deps) { d.Version = "1.3.0" })
	e.addHost("h1", "alpha", true)
	e.addHost("h2", "beta", false)
	e.addHost("h3", "gamma", false)
	e.addHost("h4", "delta", false)
	seen := t0.Add(time.Minute)
	report := `UPDATE core_hosts SET state = ?, last_seen_at = ?, agent_version = ?, protocol_version = ?, os = 'linux', arch = ?
		WHERE id = ?`
	e.exec(report, core.HostEnrolled, formatTime(seen), "1.3.0", 1, "amd64", "h1")
	e.exec(report, core.HostEnrolled, formatTime(seen), "1.2.4", 1, "arm64", "h2")
	e.exec(report, core.HostRevoked, formatTime(seen), "1.4.0", 1, "amd64", "h3")
	e.hosts["h1"] = &coretest.FakeAgent{}

	var got core.HostList
	if err := e.read(root, "core.host.list", NoInput{}, &got); err != nil {
		t.Fatal(err)
	}
	reported := func(id, name, state, version, arch string, local, online, supported bool) core.Host {
		return core.Host{ID: id, Name: name, IsLocal: local, State: state, Online: online, LastSeenAt: ptr(seen),
			AgentVersion: ptr(version), ProtocolVersion: ptr(1), OS: ptr("linux"), Arch: ptr(arch),
			Supported: supported, Modules: []core.ModuleState{}}
	}
	want := []core.Host{
		reported("h1", "alpha", core.HostEnrolled, "1.3.0", "amd64", true, true, true),
		reported("h2", "beta", core.HostEnrolled, "1.2.4", "arm64", false, false, true),
		{ID: "h4", Name: "delta", State: core.HostPending, Modules: []core.ModuleState{}},
		reported("h3", "gamma", core.HostRevoked, "1.4.0", "amd64", false, false, false),
	}
	if !reflect.DeepEqual(got.Hosts, want) {
		t.Errorf("hosts = %+v\nwant    %+v", got.Hosts, want)
	}
}

// TestHostListReportsIngestion: a host the controller has not ingested yet
// has a null ingestion state, apart from a healthy one and a failing one.
func TestHostListReportsIngestion(t *testing.T) {
	e := newTestEnv(t)
	e.addHost("h1", "alpha", true)
	e.addHost("h2", "beta", false)
	e.addHost("h3", "gamma", false)
	failedAt := t0.Add(time.Minute)
	e.exec(`INSERT INTO core_ingest_status (host_id, since, error) VALUES ('h2', ?, NULL), ('h3', ?, ?)`,
		formatTime(t0), formatTime(failedAt), "audit: ingest host h3: agent unreachable")

	var got core.HostList
	if err := e.read(root, "core.host.list", NoInput{}, &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]*core.IngestionStatus{
		"h1": nil,
		"h2": {OK: true, Since: t0},
		"h3": {Since: failedAt, Error: ptr("audit: ingest host h3: agent unreachable")},
	}
	if len(got.Hosts) != len(want) {
		t.Fatalf("hosts = %+v, want %d", got.Hosts, len(want))
	}
	for _, h := range got.Hosts {
		if !reflect.DeepEqual(h.Ingestion, want[h.ID]) {
			t.Errorf("%s ingestion = %+v, want %+v", h.ID, h.Ingestion, want[h.ID])
		}
	}
}

// deadlineDirectory answers every host as hung, recording the deadline each
// call carried; it returns at once so the test need not wait it out.
type deadlineDirectory struct{ deadlines map[string]time.Duration }

func (d deadlineDirectory) Agent(ctx context.Context, hostID string) (core.AgentClient, error) {
	dl, ok := ctx.Deadline()
	if !ok {
		return nil, errors.New("no deadline")
	}
	d.deadlines[hostID] = time.Until(dl)
	return nil, context.DeadlineExceeded
}

// TestHostListBoundsEachHost checks that each host's online check carries its
// own deadline, so one agent whose Hello hangs cannot hold the list.
func TestHostListBoundsEachHost(t *testing.T) {
	e := newTestEnv(t)
	e.addHost("h1", "alpha", true)
	e.addHost("h2", "beta", false)
	dir := deadlineDirectory{deadlines: map[string]time.Duration{}}
	got, err := hostList{}.Read(context.Background(), core.Env{DB: e.db, Agents: dir}, NoInput{})
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range got.Hosts {
		if host.Online {
			t.Errorf("host %s is online behind a hung Hello", host.ID)
		}
		left, ok := dir.deadlines[host.ID]
		if !ok || left <= 0 || left > onlineTimeout {
			t.Errorf("host %s: online check had %v left (recorded %v), want at most %v", host.ID, left, ok, onlineTimeout)
		}
	}
}

func TestHostListJSON(t *testing.T) {
	e := newTestEnv(t, fakeModule{"demo", 1})
	e.addHost("h1", "alpha", true)
	e.hosts["h1"] = &coretest.FakeAgent{}
	e.setModuleState("h1", "demo", true, true, "")
	raw, err := e.engine.Read(context.Background(), root, ref("core.host.list"), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"hosts":[{"id":"h1","name":"alpha","isLocal":true,"state":"pending","online":true,"lastSeenAt":null,` +
		`"agentVersion":null,"protocolVersion":null,"os":null,"arch":null,"supported":false,"modules":[` +
		`{"id":"demo","version":1,"enabled":true,"available":true,"reason":null,"checkedAt":"2026-10-17T00:00:00Z"}],` +
		`"ingestion":null}]}`
	if !jsonEqual(t, raw, want) {
		t.Errorf("output = %s\nwant     %s", raw, want)
	}

	empty := newTestEnv(t)
	raw, err = empty.engine.Read(context.Background(), root, ref("core.host.list"), json.RawMessage(`{}`))
	if err != nil || string(raw) != `{"hosts":[]}` {
		t.Errorf("no hosts: output = %s, %v; want {\"hosts\":[]}", raw, err)
	}
}

func TestModuleList(t *testing.T) {
	e := newTestEnv(t, fakeModule{"demo", 3})
	e.addHost("h1", "alpha", true)
	e.setModuleState("h1", "demo", false, true, "")

	var got ModuleList
	if err := e.read(root, "core.module.list", ModuleListInput{HostID: "h1"}, &got); err != nil {
		t.Fatal(err)
	}
	want := ModuleList{Modules: []core.ModuleState{{ID: "demo", Version: 3, Available: true, CheckedAt: ptr(t0)}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("modules = %+v, want %+v", got, want)
	}

	wantCode(t, e.read(root, "core.module.list", ModuleListInput{HostID: "nope"}, &got), errcode.NotFound)
	wantCode(t, e.read(root, "core.module.list", ModuleListInput{}, &got), errcode.InvalidInput)
}
