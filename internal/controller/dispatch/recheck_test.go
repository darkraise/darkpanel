package dispatch

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
)

// recheckAgent answers Preflight per module from preflight, or err.
type recheckAgent struct {
	mu        sync.Mutex
	preflight map[string]core.ModuleStatus
	err       error
}

func (a *recheckAgent) answer(_ context.Context, moduleID string) (core.ModuleStatus, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.err != nil {
		return core.ModuleStatus{}, a.err
	}
	return a.preflight[moduleID], nil
}

func newRecheckFixture(t *testing.T) (*Dispatcher, *harness, *recheckAgent) {
	t.Helper()
	h := newHarness(t)
	agent := &recheckAgent{preflight: map[string]core.ModuleStatus{}}
	rt := h.rt
	h.clients.Add(testHost, &coretest.FakeAgent{
		HelloFunc: rt.Hello, ReadFunc: rt.Read, PlanFunc: rt.Plan, ApplyFunc: rt.Apply, ChangeFunc: rt.Change,
		SetModuleEnabledFunc: rt.SetModuleEnabled, PreflightFunc: agent.answer,
	})
	d, err := New(h.config(&coretest.MemAudit{}))
	if err != nil {
		t.Fatal(err)
	}
	return d, h, agent
}

func moduleRow(t *testing.T, h *harness, module string) (enabled, available int, reason, checked string) {
	t.Helper()
	err := h.db.Read().QueryRow(`SELECT enabled, available, COALESCE(reason, ''), checked_at FROM core_module_state
		WHERE host_id = ? AND module_id = ?`, testHost, module).Scan(&enabled, &available, &reason, &checked)
	if err != nil {
		t.Fatal(err)
	}
	return
}

func insertModuleRow(t *testing.T, h *harness) {
	t.Helper()
	err := h.db.Write(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO core_module_state (host_id, module_id, enabled, available, reason, checked_at)
			VALUES (?, 'docker', 1, 0, 'daemon not running', '2026-11-19T00:00:00Z')`, testHost)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRecheckModulesUpsertsAvailabilityOnly(t *testing.T) {
	d, h, agent := newRecheckFixture(t)
	insertModuleRow(t, h)
	agent.preflight["docker"] = core.ModuleStatus{ID: "docker", Available: true}
	key := availabilityKey{host: testHost, module: "docker"}
	d.avail.put(key, availabilityEntry{answer: core.ModuleAvailability{}, askedAt: time.Now(), checkedAt: "2026-11-19T00:00:00Z"})
	if err := d.RecheckModules(context.Background(), testHost, []string{"docker"}); err != nil {
		t.Fatal(err)
	}
	enabled, available, reason, checked := moduleRow(t, h, "docker")
	if enabled != 1 || available != 1 || reason != "" || checked == "2026-11-19T00:00:00Z" {
		t.Fatalf("got enabled=%d available=%d reason=%q checked=%s", enabled, available, reason, checked)
	}
	if _, ok := d.avail.get(key); ok {
		t.Fatal("cache entry not retired")
	}
}

func TestRecheckModulesRecordsTheReasonAndLeavesARowOnAnUnreachableAgent(t *testing.T) {
	d, h, agent := newRecheckFixture(t)
	insertModuleRow(t, h)
	agent.preflight["docker"] = core.ModuleStatus{ID: "docker", Available: false, Reason: "docker is not installed"}
	if err := d.RecheckModules(context.Background(), testHost, []string{"docker"}); err != nil {
		t.Fatal(err)
	}
	if _, available, reason, _ := moduleRow(t, h, "docker"); available != 0 || reason != "docker is not installed" {
		t.Fatalf("got available=%d reason=%q", available, reason)
	}
	agent.err = errors.New("host offline")
	_, _, _, before := moduleRow(t, h, "docker")
	if err := d.RecheckModules(context.Background(), testHost, []string{"docker"}); err != nil {
		t.Fatalf("an unreachable agent is logged, not returned: %v", err)
	}
	if _, available, reason, checked := moduleRow(t, h, "docker"); available != 0 || reason != "docker is not installed" || checked != before {
		t.Fatalf("row changed on an unreachable agent: available=%d reason=%q checked=%s", available, reason, checked)
	}
}
