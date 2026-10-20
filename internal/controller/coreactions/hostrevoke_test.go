package coreactions

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// fakeSessions records the hosts whose live sessions were closed and, with
// db set, the state each host had in the committed database at that moment.
type fakeSessions struct {
	mu    sync.Mutex
	hosts []string
	db    *store.DB
	seen  []string // "deleted" for a host without a row
}

func (f *fakeSessions) Disconnect(hostID string) {
	state := ""
	if f.db != nil {
		err := f.db.Read().QueryRow(`SELECT state FROM core_hosts WHERE id = ?`, hostID).Scan(&state)
		if errors.Is(err, sql.ErrNoRows) {
			state = "deleted"
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hosts = append(f.hosts, hostID)
	f.seen = append(f.seen, state)
}

func (f *fakeSessions) committed() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.seen...)
}

func (f *fakeSessions) closed() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.hosts...)
}

// newHostEnv is a test env whose core module closes sessions through the
// returned fake, which reads the host's committed state, with the local host
// h1 named alpha.
func newHostEnv(t *testing.T) (*testEnv, *fakeSessions) {
	t.Helper()
	s := &fakeSessions{}
	e := newTestEnvWith(t, func(e *testEnv, d *Deps) { s.db, d.Sessions = e.db, s }, fakeModule{"demo", 1})
	e.addHost("h1", "alpha", true)
	return e, s
}

func (e *testEnv) setHostState(hostID, state string) {
	e.t.Helper()
	e.exec(`UPDATE core_hosts SET state = ? WHERE id = ?`, state, hostID)
}

func (e *testEnv) addCert(hostID, serial string) {
	e.t.Helper()
	e.exec(`INSERT INTO core_agent_certs (serial, host_id, not_before, not_after, created_at) VALUES (?, ?, ?, ?, ?)`,
		serial, hostID, formatTime(t0), formatTime(t0.Add(365*24*time.Hour)), formatTime(t0))
}

func (e *testEnv) count(query string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.db.Read().QueryRow(query, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

// applyDirect calls name's Apply handler without the engine, as if the
// state changed after the engine's recheck.
func (e *testEnv) applyDirect(name string, in any) error {
	e.t.Helper()
	def, ok := e.reg.Action(ref(name))
	if !ok {
		e.t.Fatalf("%s is not registered", name)
	}
	env := core.Env{DB: e.db, Clock: e.clock, Agents: e.hosts, Log: slog.New(slog.DiscardHandler), Principal: operator}
	_, err := def.Handler.Apply(context.Background(), env, encodeInput(e.t, in))
	return err
}

func TestHostRevoke(t *testing.T) {
	e, sessions := newHostEnv(t)
	e.addHost("h2", "web1", false)
	e.setHostState("h2", core.HostEnrolled)
	e.addCert("h2", "a1")
	e.addCert("h1", "b1")
	e.exec(`INSERT INTO core_enroll_tokens (host_id, hash, expires_at, created_at) VALUES ('h2', x'00', ?, ?)`,
		formatTime(t0.Add(time.Hour)), formatTime(t0))
	in := core.HostIDInput{HostID: "h2"}

	if err := e.verify("core.host.revoke", in); err == nil {
		t.Error("Verify passed for an enrolled host")
	}
	pv, err := e.plan(operator, "core.host.revoke", in)
	if err != nil {
		t.Fatal(err)
	}
	if pv.Confirmation != "web1" {
		t.Errorf("confirmation = %q, want the host name web1", pv.Confirmation)
	}
	_, err = e.apply(operator, pv.PlanID, "")
	wantCode(t, err, errcode.PlanConfirmationNeeded)
	v, err := e.apply(operator, pv.PlanID, "web1")
	if err != nil || v.State != core.StateCompleted {
		t.Fatalf("apply: %+v, %v", v, err)
	}
	var got core.Host
	if err := json.Unmarshal(v.Output, &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != "h2" || got.State != core.HostRevoked || got.Online {
		t.Errorf("output = %+v, want h2 revoked and offline", got)
	}
	if n := e.count(`SELECT count(*) FROM core_agent_certs WHERE host_id = 'h2' AND revoked_at IS NOT NULL`); n != 1 {
		t.Errorf("revoked certificates of h2 = %d, want 1", n)
	}
	if n := e.count(`SELECT count(*) FROM core_agent_certs WHERE revoked_at IS NULL`); n != 1 {
		t.Errorf("unrevoked certificates = %d, want the local host's one", n)
	}
	if n := e.count(`SELECT count(*) FROM core_enroll_tokens`); n != 0 {
		t.Errorf("enrollment tokens = %d, want none", n)
	}
	if got := sessions.closed(); !reflect.DeepEqual(got, []string{"h2"}) {
		t.Errorf("closed sessions = %v, want [h2]", got)
	}
	// The agent listener re-checks the host when the agent redials, so the
	// session closes only once the revocation is committed.
	if got := sessions.committed(); !reflect.DeepEqual(got, []string{core.HostRevoked}) {
		t.Errorf("committed state at Disconnect = %v, want [revoked]", got)
	}
	if err := e.verify("core.host.revoke", in); err != nil {
		t.Errorf("Verify after the revocation: %v", err)
	}

	_, err = e.plan(operator, "core.host.revoke", core.HostIDInput{HostID: "nope"})
	wantCode(t, err, errcode.NotFound)
}

func TestHostDelete(t *testing.T) {
	e, sessions := newHostEnv(t)
	e.addHost("h2", "web1", false)
	e.setModuleState("h2", "demo", true, true, "")
	e.addCert("h2", "a1")
	e.exec(`INSERT INTO core_host_update (host_id, channel) VALUES ('h2', 'beta')`)
	in := core.HostIDInput{HostID: "h2"}

	if err := e.verify("core.host.delete", in); err == nil {
		t.Error("Verify passed while the host exists")
	}
	pv, err := e.plan(operator, "core.host.delete", in)
	if err != nil {
		t.Fatal(err)
	}
	if pv.Confirmation != "web1" {
		t.Errorf("confirmation = %q, want the host name web1", pv.Confirmation)
	}
	v, err := e.apply(operator, pv.PlanID, "web1")
	if err != nil || v.State != core.StateCompleted {
		t.Fatalf("apply: %+v, %v", v, err)
	}
	var got core.Host
	if err := json.Unmarshal(v.Output, &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != "h2" || got.Name != "web1" || got.State != core.HostPending || got.Online {
		t.Errorf("output = %+v, want the deleted pending host web1, offline", got)
	}
	for _, table := range []string{"core_hosts WHERE id", "core_module_state WHERE host_id", "core_agent_certs WHERE host_id",
		"core_host_update WHERE host_id"} {
		if n := e.count(`SELECT count(*) FROM ` + table + ` = 'h2'`); n != 0 {
			t.Errorf("%s = 'h2': %d rows left", table, n)
		}
	}
	if got := sessions.closed(); !reflect.DeepEqual(got, []string{"h2"}) {
		t.Errorf("closed sessions = %v, want [h2]", got)
	}
	if got := sessions.committed(); !reflect.DeepEqual(got, []string{"deleted"}) {
		t.Errorf("committed state at Disconnect = %v, want [deleted]", got)
	}
	if err := e.verify("core.host.delete", in); err != nil {
		t.Errorf("Verify after the deletion: %v", err)
	}
}

func TestHostDeleteRefusals(t *testing.T) {
	e, sessions := newHostEnv(t)
	e.addHost("h2", "web1", false)
	e.setHostState("h2", core.HostEnrolled)
	e.addHost("h3", "web2", false)
	e.setHostState("h3", core.HostRevoked)

	for _, id := range []string{"h1", "h2"} {
		pv, err := e.plan(operator, "core.host.delete", core.HostIDInput{HostID: id})
		if err != nil {
			t.Fatal(err)
		}
		_, err = e.apply(operator, pv.PlanID, pv.Confirmation)
		wantCode(t, err, errcode.PlanGuardFailed)
	}

	// A host enrolled again between the engine's recheck and the delete.
	e.setHostState("h3", core.HostEnrolled)
	wantCode(t, e.applyDirect("core.host.delete", core.HostIDInput{HostID: "h3"}), errcode.PlanStateChanged)
	wantCode(t, e.applyDirect("core.host.delete", core.HostIDInput{HostID: "h1"}), errcode.PlanStateChanged)
	if n := e.count(`SELECT count(*) FROM core_hosts`); n != 3 {
		t.Errorf("hosts = %d, want all 3 kept", n)
	}
	if got := sessions.closed(); len(got) != 0 {
		t.Errorf("closed sessions = %v, want none", got)
	}
	_, err := e.plan(operator, "core.host.delete", core.HostIDInput{HostID: "nope"})
	wantCode(t, err, errcode.NotFound)
}

func TestHostRevokeWithoutSessions(t *testing.T) {
	e := newTestEnv(t)
	e.addHost("h2", "web1", false)
	e.hosts["h2"] = &coretest.FakeAgent{}
	e.run(operator, "core.host.revoke", core.HostIDInput{HostID: "h2"}, nil)
	if n := e.count(`SELECT count(*) FROM core_hosts WHERE state = 'revoked'`); n != 1 {
		t.Errorf("revoked hosts = %d, want 1", n)
	}
}
