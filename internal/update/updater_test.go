package update

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/paths"
	"github.com/darkraise/darkpanel/internal/update/trustedkeys"
)

// envReleases are the stable releases every updater test environment
// publishes, plus one beta.
var envReleases = []Release{
	{Version: "1.1.0", Channel: core.ChannelStable},
	{Version: "1.2.0", Channel: core.ChannelStable},
	{Version: "1.3.0", Channel: core.ChannelStable},
	{Version: "1.3.1", Channel: core.ChannelStable},
	{Version: "1.4.0", Channel: core.ChannelStable},
	{Version: "1.5.0", Channel: core.ChannelStable},
	{Version: "1.6.0-beta.1", Channel: core.ChannelBeta},
}

// binaryOf is the content of the fake darkpanel binary of version.
func binaryOf(version string) string { return "darkpanel " + version }

// updaterEnv is a host under a temporary root: BinaryDir with the installed
// fake binary, UpdateDir, a migrated agent.db, a fake systemd, a fake clock
// and a release server publishing envReleases signed by one key. Its probes
// report the version of whatever binary is installed, as restarted services
// would.
type updaterEnv struct {
	p      paths.Paths
	db     *store.DB
	sd     *fakeSystemd
	clock  *coretest.FakeClock
	rel    *releaseServer
	signer testSigner

	mu        sync.Mutex
	selfTests []string
	selfErr   error
	// agentHello, when set, replaces the agent probe's answer.
	agentHello func(installed string) (core.AgentHello, error)
	newer      []string
}

func newUpdaterEnv(t *testing.T, installed string) *updaterEnv {
	t.Helper()
	root := t.TempDir()
	e := &updaterEnv{
		p:      paths.Under(root),
		sd:     newFakeSystemd(),
		clock:  coretest.NewFakeClock(recordTime),
		rel:    newReleaseServer(t),
		signer: newTestSigner(t),
	}
	for _, dir := range []string{e.p.UpdateDir, e.p.BinaryDir, e.p.AgentDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeBinaryFile(t, e.p.Binary(), binaryOf(installed))
	e.db = openAgentDB(t, e.p.AgentDB())
	for _, r := range envReleases {
		e.rel.publish(t, r.Version, r.Channel, "amd64", []byte(binaryOf(r.Version)), e.signer)
	}
	e.rel.setIndex(t, envReleases...)
	e.sd.setState(AgentUnit, "active")
	return e
}

// openAgentDB opens and migrates an agent database at path.
func openAgentDB(t *testing.T, path string) *store.DB {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	set, err := store.CoreMigrations(store.SideAgent)
	if err != nil {
		t.Fatal(err)
	}
	opts := store.MigrateOptions{
		Side: store.SideAgent, BinaryVersion: "test", BackupRoot: filepath.Join(filepath.Dir(path), "backups"),
		Keep: 5, Now: recordTime,
	}
	if _, err := store.Migrate(ctx, db, opts, set, nil); err != nil {
		t.Fatal(err)
	}
	return db
}

// installedVersion is the version of the binary at path, "" when it is not
// a fake binary.
func installedVersion(path string) string {
	b, err := os.ReadFile(path) //nolint:gosec // G304: a test path
	if err != nil {
		return ""
	}
	v, ok := strings.CutPrefix(string(b), "darkpanel ")
	if !ok {
		return ""
	}
	return v
}

// config is the updater configuration of a process running version.
func (e *updaterEnv) config(version string) Config {
	return Config{
		Paths: e.p, DB: e.db, Systemd: e.sd, Clock: e.clock, Version: version, Arch: "amd64",
		BaseURL: e.rel.URL, Keys: []trustedkeys.Key{e.signer.Key}, HTTP: e.rel.client(),
		Index: NewIndexSource(e.rel.URL, e.rel.client(), e.clock),
		Probes: Probes{
			Controller: func(context.Context) (ControllerHealth, error) {
				return ControllerHealth{Status: "ok", Version: installedVersion(e.p.Binary())}, nil
			},
			Agent: func(context.Context) (core.AgentHello, error) {
				installed := installedVersion(e.p.Binary())
				e.mu.Lock()
				hook := e.agentHello
				e.mu.Unlock()
				if hook != nil {
					return hook(installed)
				}
				return core.AgentHello{BinaryVersion: installed}, nil
			},
		},
		SelfTest: func(_ context.Context, binary string) error {
			e.mu.Lock()
			defer e.mu.Unlock()
			e.selfTests = append(e.selfTests, binary)
			return e.selfErr
		},
		NewerAgents: func(context.Context, string) ([]string, error) {
			e.mu.Lock()
			defer e.mu.Unlock()
			return slices.Clone(e.newer), nil
		},
	}
}

// updater is an Updater of a process running version.
func (e *updaterEnv) updater(t *testing.T, version string) *Updater {
	t.Helper()
	u, err := New(e.config(version))
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// lockRows lists core_locks as "key owner", sorted.
func lockRows(t *testing.T, db *store.DB) []string {
	t.Helper()
	rows, err := db.Read().Query("SELECT key || ' ' || owner FROM core_locks ORDER BY key, owner")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// wantNoLocks fails unless core_locks is empty.
func wantNoLocks(t *testing.T, db *store.DB) {
	t.Helper()
	if rows := lockRows(t, db); len(rows) != 0 {
		t.Errorf("core_locks = %v, want empty", rows)
	}
}

// insertChange stores a change of class in state, with the plan it needs.
func insertChange(t *testing.T, db *store.DB, id string, class core.Class, state string) {
	t.Helper()
	at := recordTime.Format("2006-01-02T15:04:05.000000000Z")
	err := db.Write(context.Background(), func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO core_plans (id, host_id, action, version, actor, input_hash, state_hash,
			diff_json, guards_json, confirmation, created_at, expires_at)
			VALUES (?, 'h1', 'demo.x', 1, 'user:a', '', '', '[]', '[]', '', ?, ?)`, "plan-"+id, at, at); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO core_changes (id, plan_id, host_id, action, version, class, compensated_by,
			actor, state, input_json, created_at, updated_at)
			VALUES (?, ?, 'h1', 'demo.x', 1, ?, '', 'user:a', ?, '{}', ?, ?)`, id, "plan-"+id, string(class), state, at, at)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// wantCode fails unless err carries code.
func wantCode(t *testing.T, err error, code errcode.Code) {
	t.Helper()
	var e *errcode.Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("error = %v, want code %s", err, code)
	}
}

// mustRecord reads the record of id.
func mustRecord(t *testing.T, p paths.Paths, id string) Record {
	t.Helper()
	r, err := ReadRecord(p, id)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestNewRequiresItsDependencies(t *testing.T) {
	e := newUpdaterEnv(t, "1.2.0")
	full := e.config("1.2.0")
	for name, change := range map[string]func(*Config){
		"paths":   func(c *Config) { c.Paths = paths.Paths{} },
		"db":      func(c *Config) { c.DB = nil },
		"systemd": func(c *Config) { c.Systemd = nil },
		"clock":   func(c *Config) { c.Clock = nil },
		"version": func(c *Config) { c.Version = "" },
	} {
		cfg := full
		change(&cfg)
		if _, err := New(cfg); err == nil {
			t.Errorf("New without %s succeeded", name)
		}
	}
	if _, err := New(full); err != nil {
		t.Fatalf("New: %v", err)
	}
}

func TestNewDefaultHTTPClient(t *testing.T) {
	e := newUpdaterEnv(t, "1.2.0")
	cfg := e.config("1.2.0")
	supplied := cfg.HTTP
	if u, err := New(cfg); err != nil || u.cfg.HTTP != supplied {
		t.Fatalf("New replaced the supplied client: %v", err)
	}
	cfg.HTTP = nil
	u, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	c := u.cfg.HTTP
	tr, ok := c.Transport.(*http.Transport)
	// A total Timeout would also cut off a large tarball's body.
	if c == http.DefaultClient || c.Timeout != 0 || !ok || tr.TLSHandshakeTimeout == 0 || tr.ResponseHeaderTimeout == 0 {
		t.Errorf("default client = %+v, transport %+v", c, c.Transport)
	}
}

func TestStartUpdate(t *testing.T) {
	ctx := context.Background()
	e := newUpdaterEnv(t, "1.2.0")
	u := e.updater(t, "1.2.0")
	tr, err := u.StartUpdate(ctx, "01CHANGE", core.UpdateApplyInput{Version: "1.5.0", Channel: core.ChannelStable})
	if err != nil {
		t.Fatal(err)
	}
	if tr.State != core.UpdatePending || tr.OperationID != tr.ID || tr.FromVersion != "1.2.0" ||
		tr.ToVersion != "1.3.1" || tr.TargetVersion != "1.5.0" || tr.Kind != core.UpdateKindUpdate {
		t.Errorf("transaction = %+v", tr)
	}
	r := mustRecord(t, e.p, tr.ID)
	wantSteps := []Release{
		{Version: "1.3.1", Channel: core.ChannelStable},
		{Version: "1.4.0", Channel: core.ChannelStable},
		{Version: "1.5.0", Channel: core.ChannelStable},
	}
	if r.ChangeID != "01CHANGE" || !reflect.DeepEqual(r.Steps, wantSteps) {
		t.Errorf("record = %+v", r)
	}
	if units := e.sd.unitsOf("StartTransientService"); !slices.Equal(units, []string{RunnerUnit(tr.ID)}) {
		t.Fatalf("transient units = %v", units)
	}
	starts := e.sd.callsOf("StartTransientService")
	svc := starts[0].Service
	if !slices.Equal(svc.ExecStart, []string{e.p.Binary(), "update", "run", tr.ID}) ||
		!slices.Equal(svc.OnFailure, []string{RollbackUnit}) {
		t.Errorf("runner unit = %+v", svc)
	}
	if got, want := lockRows(t, e.db), []string{"* " + lifecycle.ReservationOwner(tr.ID)}; !slices.Equal(got, want) {
		t.Errorf("core_locks = %v, want %v", got, want)
	}
}

func TestStartUpdateRefusals(t *testing.T) {
	ctx := context.Background()
	stable := func(v string) core.UpdateApplyInput {
		return core.UpdateApplyInput{Version: v, Channel: core.ChannelStable}
	}
	tests := []struct {
		name  string
		setup func(t *testing.T, e *updaterEnv)
		in    core.UpdateApplyInput
		code  errcode.Code
	}{
		{"downgrade without force", nil, stable("1.1.0"), errcode.UpdateDowngradeRefused},
		{"unpublished", nil, stable("1.9.0"), errcode.UpdateNotFound},
		{"beta release on stable", nil, stable("1.6.0-beta.1"), errcode.UpdateNotFound},
		{"unconfirmed revertible change", func(t *testing.T, e *updaterEnv) {
			insertChange(t, e.db, "01REV", core.ClassRevertible, core.StatePending)
		}, stable("1.3.1"), errcode.UpdateUnconfirmedChanges},
		{"a change holds a lock", func(t *testing.T, e *updaterEnv) {
			if _, err := e.db.Writer().Exec("INSERT INTO core_locks (key, owner) VALUES ('ufw', '01CH')"); err != nil {
				t.Fatal(err)
			}
		}, stable("1.3.1"), errcode.UpdateUnconfirmedChanges},
		{"another operation", func(t *testing.T, e *updaterEnv) {
			if err := lifecycle.Reserve(ctx, e.db, lifecycle.ReserveAll, lifecycle.ReservationOwner("01OTHER")); err != nil {
				t.Fatal(err)
			}
		}, stable("1.3.1"), errcode.UpdateInProgress},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newUpdaterEnv(t, "1.2.0")
			if tt.setup != nil {
				tt.setup(t, e)
			}
			before := lockRows(t, e.db)
			_, err := e.updater(t, "1.2.0").StartUpdate(ctx, "01CHANGE", tt.in)
			wantCode(t, err, tt.code)
			if after := lockRows(t, e.db); !slices.Equal(after, before) {
				t.Errorf("core_locks = %v, want %v", after, before)
			}
			if recs, _ := ListRecords(e.p); len(recs) != 0 {
				t.Errorf("records = %+v, want none", recs)
			}
			if n := len(e.sd.callsOf("StartTransientService")); n != 0 {
				t.Errorf("%d transient units started", n)
			}
		})
	}
}

func TestStartUpdateUnitFailure(t *testing.T) {
	ctx := context.Background()
	e := newUpdaterEnv(t, "1.2.0")
	u := e.updater(t, "1.2.0")
	e.sd.failCall("StartTransientService", "", errors.New("no bus"))
	_, err := u.StartUpdate(ctx, "01CHANGE", core.UpdateApplyInput{Version: "1.3.1", Channel: core.ChannelStable})
	if err == nil {
		t.Fatal("StartUpdate succeeded although the runner unit did not start")
	}
	recs, lerr := ListRecords(e.p)
	if lerr != nil || len(recs) != 1 {
		t.Fatalf("records = %+v, %v", recs, lerr)
	}
	if r := recs[0]; r.State != core.UpdateFailed || r.Error == nil || !strings.Contains(r.Error.Message, "no bus") {
		t.Errorf("record = %+v", r)
	}
	wantNoLocks(t, e.db)
}

func TestStartRunnerWaitsWithinItsBudget(t *testing.T) {
	if runnerStartWait > LockTimeout/2 {
		t.Fatalf("runnerStartWait %s leaves too little of LockTimeout %s", runnerStartWait, LockTimeout)
	}
	type hook = func(ctx context.Context, unit string) error
	for name, onState := range map[string]func(e *updaterEnv) hook{
		"state queries hang": func(*updaterEnv) hook {
			return func(ctx context.Context, _ string) error { <-ctx.Done(); return ctx.Err() }
		},
		"the unit stays inactive": func(e *updaterEnv) hook {
			return func(_ context.Context, unit string) error { e.sd.setState(unit, "inactive"); return nil }
		},
	} {
		t.Run(name, func(t *testing.T) {
			e := newUpdaterEnv(t, "1.2.0")
			u := e.updater(t, "1.2.0")
			u.runnerStartWait = 100 * time.Millisecond
			e.sd.onState = onState(e)
			start := time.Now()
			done := make(chan error, 1)
			go func() {
				_, err := u.StartUpdate(context.Background(), "01CHANGE",
					core.UpdateApplyInput{Version: "1.3.1", Channel: core.ChannelStable})
				done <- err
			}()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("StartUpdate is still waiting for the runner unit")
			}
			if d := time.Since(start); d > time.Second {
				t.Errorf("StartUpdate held the update lock for %s", d)
			}
			if recs, err := ListRecords(e.p); err != nil || len(recs) != 1 || recs[0].State != core.UpdatePending {
				t.Errorf("records = %+v, %v", recs, err)
			}
		})
	}
}

func TestCheckApplyHasNoSideEffects(t *testing.T) {
	e := newUpdaterEnv(t, "1.2.0")
	steps, err := e.updater(t, "1.2.0").CheckApply(context.Background(),
		core.UpdateApplyInput{Version: "1.4.0", Channel: core.ChannelStable})
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{steps[0].Version, steps[1].Version}; !slices.Equal(got, []string{"1.3.1", "1.4.0"}) {
		t.Errorf("steps = %v", got)
	}
	wantNoLocks(t, e.db)
	if recs, _ := ListRecords(e.p); len(recs) != 0 {
		t.Errorf("records = %+v", recs)
	}
}

func TestStatus(t *testing.T) {
	ctx := context.Background()
	e := newUpdaterEnv(t, "1.2.0")
	u := e.updater(t, "1.2.0")
	// 22 operations of one record each, then a second step of the newest.
	for i := range 22 {
		id := "01OP" + string(rune('A'+i))
		r := testRecord(id, id, time.Duration(i)*time.Minute)
		r.ChangeID = "01CH" + string(rune('A'+i))
		if err := WriteRecord(e.p, r); err != nil {
			t.Fatal(err)
		}
	}
	child := testRecord("01STEP", "01OPV", 30*time.Minute)
	if err := WriteRecord(e.p, child); err != nil {
		t.Fatal(err)
	}
	ids := func(st core.UpdateStatus) []string {
		var out []string
		for _, tr := range st.Transactions {
			out = append(out, tr.ID)
		}
		return out
	}
	st, err := u.Status(ctx, core.UpdateStatusInput{OperationID: "01OPV"})
	if err != nil || !slices.Equal(ids(st), []string{"01STEP", "01OPV"}) {
		t.Errorf("by operation = %v, %v", ids(st), err)
	}
	st, err = u.Status(ctx, core.UpdateStatusInput{ChangeID: "01CHV"})
	if err != nil || !slices.Equal(ids(st), []string{"01STEP", "01OPV"}) {
		t.Errorf("by change = %v, %v", ids(st), err)
	}
	st, err = u.Status(ctx, core.UpdateStatusInput{ChangeID: "01NONE"})
	if err != nil || st.Transactions == nil || len(st.Transactions) != 0 {
		t.Errorf("unknown change = %+v, %v", st, err)
	}
	st, err = u.Status(ctx, core.UpdateStatusInput{})
	if err != nil {
		t.Fatal(err)
	}
	got := ids(st)
	if len(got) != 21 || got[0] != "01STEP" || got[1] != "01OPV" || got[20] != "01OPC" || slices.Contains(got, "01OPB") {
		t.Errorf("newest 20 operations = %v", got)
	}
	_, err = u.Status(ctx, core.UpdateStatusInput{OperationID: "01OPA", ChangeID: "01CHA"})
	wantCode(t, err, errcode.InvalidInput)
}

func TestTransaction(t *testing.T) {
	ctx := context.Background()
	e := newUpdaterEnv(t, "1.2.0")
	u := e.updater(t, "1.2.0")
	if err := WriteRecord(e.p, testRecord("01TX", "01TX", 0)); err != nil {
		t.Fatal(err)
	}
	tr, err := u.Transaction(ctx, "01TX")
	if err != nil || tr.ID != "01TX" || tr.State != core.UpdatePending {
		t.Errorf("Transaction = %+v, %v", tr, err)
	}
	_, err = u.Transaction(ctx, "01MISSING")
	wantCode(t, err, errcode.NotFound)
}

func TestStartUpdateOrdersAfterTheNewestRecord(t *testing.T) {
	e := newUpdaterEnv(t, "1.2.0")
	// A record made when the clock read later, as before the clock was set
	// back.
	old := testRecord("01ZZOLD", "01ZZOLD", time.Hour)
	old.State = core.UpdateFailed
	if err := WriteRecord(e.p, old); err != nil {
		t.Fatal(err)
	}
	tr, err := e.updater(t, "1.2.0").StartUpdate(context.Background(), "01CHANGE",
		core.UpdateApplyInput{Version: "1.3.1", Channel: core.ChannelStable})
	if err != nil {
		t.Fatal(err)
	}
	if !tr.StartedAt.After(old.StartedAt) {
		t.Errorf("new record started at %s, not after the newest record's %s", tr.StartedAt, old.StartedAt)
	}
	all, err := ListRecords(e.p)
	if err != nil || len(all) != 2 || all[1].ID != tr.ID {
		t.Errorf("records = %+v, %v; want the new one last", all, err)
	}
}
