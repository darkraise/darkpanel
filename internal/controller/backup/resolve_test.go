package backup

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/darkraise/darkpanel/internal/controller/audit"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/ext"
	"github.com/darkraise/darkpanel/internal/core/store"
)

const rsID = "R1"

// rsEnv is a controller database, scripted agents and the resolve handler.
type rsEnv struct {
	db    *store.DB
	audit *audit.Store
	h     http.Handler

	mu            sync.Mutex
	lookups       map[string]core.PlanLookup // by plan id
	lookupErrs    map[string]error           // by plan id
	agentErrs     map[string]error           // by host id: what Agent returns
	retireAnswers map[string]core.PlanLookup // by plan id; the default is retired
	asked         []string                   // plan ids looked up; "(private)" is added to a private lookup
	retired       []string                   // plan ids Retire was called with
}

// rsHosts is the core.HostDirectory over an rsEnv's scripts.
type rsHosts struct{ e *rsEnv }

func (h rsHosts) Agent(_ context.Context, hostID string) (core.AgentClient, error) {
	e := h.e
	e.mu.Lock()
	defer e.mu.Unlock()
	if err, ok := e.agentErrs[hostID]; ok {
		return nil, err
	}
	return &coretest.FakeAgent{
		LookupFunc: func(_ context.Context, planID string, private bool) (core.PlanLookup, error) {
			e.mu.Lock()
			defer e.mu.Unlock()
			if private {
				planID += " (private)"
			}
			e.asked = append(e.asked, planID)
			if err, ok := e.lookupErrs[planID]; ok {
				return core.PlanLookup{}, err
			}
			lk, ok := e.lookups[planID]
			if !ok {
				return core.PlanLookup{}, fmt.Errorf("rs test: no scripted lookup for plan %s", planID)
			}
			return lk, nil
		},
		RetireFunc: func(_ context.Context, planID string) (core.PlanLookup, error) {
			e.mu.Lock()
			defer e.mu.Unlock()
			e.retired = append(e.retired, planID)
			if lk, ok := e.retireAnswers[planID]; ok {
				return lk, nil
			}
			return core.PlanLookup{Status: core.LookupRetired}, nil
		},
	}, nil
}

func rsNewEnv(t *testing.T) *rsEnv {
	t.Helper()
	db := fxDB(t, t.TempDir())
	clock := coretest.NewFakeClock(fxNow)
	e := &rsEnv{
		db: db, audit: audit.New(db, clock),
		lookups: map[string]core.PlanLookup{}, lookupErrs: map[string]error{},
		agentErrs: map[string]error{}, retireAnswers: map[string]core.PlanLookup{},
	}
	deps := ext.Deps{
		DB: db, Sealer: coretest.Sealer{}, Clock: clock, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Hosts: rsHosts{e}, Audit: e.audit, LocalHostID: "host-local",
	}
	e.h = New(Config{}).resolveHandler(deps)
	return e
}

func (e *rsEnv) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	if err := e.db.Write(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(), query, args...)
		return err
	}); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

// quarantine marks the change id as restore marks one that was applying, and
// gives it one lock row.
func (e *rsEnv) quarantine(t *testing.T, id string) {
	t.Helper()
	e.exec(t, `UPDATE core_changes SET state = ?, quarantined_from = ? WHERE id = ?`,
		core.StateQuarantined, core.StateApplying, id)
	fxInsertLock(t, e.db, "lock:"+id, id)
}

// change adds a quarantined single change owned, before the restore, by the engine.
func (e *rsEnv) change(t *testing.T, id, action string) {
	t.Helper()
	fxInsertChange(t, e.db, fxChange{ID: id, HostID: "h1", Action: action, State: core.StateApplying, Owner: core.OwnerEngine})
	e.quarantine(t, id)
}

// rsStep is a composite step and what the step's agent answers for its child plan.
type rsStep struct {
	fxStep
	lookup *core.PlanLookup
	err    error
}

// rsAt is the step "s-<plan>" on host whose child plan the agent answers with lk.
func rsAt(host, plan string, lk core.PlanLookup) rsStep {
	return rsStep{fxStep: fxStep{ID: "s-" + plan, HostID: host, Action: "a/v1", State: core.StepApplying, ChildPlanID: plan}, lookup: &lk}
}

func rsTerminal(state string, made int) core.PlanLookup {
	return core.PlanLookup{Status: core.LookupTerminal, State: state, Made: made}
}

var (
	rsAbsent     = core.PlanLookup{Status: core.LookupAbsent}
	rsRetiredLk  = core.PlanLookup{Status: core.LookupRetired}
	rsRunningLk  = core.PlanLookup{Status: core.LookupRunning}
	rsRecovering = core.PlanLookup{Status: core.LookupRecovering}
)

// composite adds a quarantined composite with the steps in order.
func (e *rsEnv) composite(t *testing.T, id, direction string, steps ...rsStep) {
	t.Helper()
	fx := make([]fxStep, len(steps))
	for i, s := range steps {
		fx[i] = s.fxStep
		if s.lookup != nil {
			e.lookups[s.ChildPlanID] = *s.lookup
		}
		if s.err != nil {
			e.lookupErrs[s.ChildPlanID] = s.err
		}
	}
	fxInsertComposite(t, e.db, id, core.StateApplying, direction, fx)
	e.quarantine(t, id)
}

// rollout adds a quarantined rollout that was running.
func (e *rsEnv) rollout(t *testing.T, id string) {
	t.Helper()
	fxInsertRollout(t, e.db, id, "quarantined", "h1")
	e.exec(t, `UPDATE core_rollouts SET quarantined_from = 'running' WHERE id = ?`, id)
	fxInsertLock(t, e.db, "lock:"+id, id)
}

func (e *rsEnv) resolve(id, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/controller/quarantine/"+id+"/resolve", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("record", id)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func rsOutcome(outcome string) string { return `{"outcome":"` + outcome + `"}` }

// rsRow is a record's stored state.
type rsRow struct{ State, ErrorCode, Message, Owner, From string }

func (e *rsEnv) row(t *testing.T, id string) rsRow {
	t.Helper()
	var r rsRow
	err := e.db.Read().QueryRowContext(context.Background(),
		`SELECT state, coalesce(error_code, ''), coalesce(error_message, ''), recovery_owner, coalesce(quarantined_from, '')
		 FROM core_changes WHERE id = ?`, id).Scan(&r.State, &r.ErrorCode, &r.Message, &r.Owner, &r.From)
	if errors.Is(err, sql.ErrNoRows) {
		err = e.db.Read().QueryRowContext(context.Background(),
			`SELECT state, coalesce(error_code, ''), coalesce(error_message, ''), '', coalesce(quarantined_from, '')
			 FROM core_rollouts WHERE id = ?`, id).Scan(&r.State, &r.ErrorCode, &r.Message, &r.Owner, &r.From)
	}
	if err != nil {
		t.Fatalf("read record %s: %v", id, err)
	}
	return r
}

func (e *rsEnv) locks(t *testing.T, owner string) int {
	t.Helper()
	var n int
	if err := e.db.Read().QueryRowContext(context.Background(),
		`SELECT count(*) FROM core_locks WHERE owner = ?`, owner).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (e *rsEnv) auditRows(t *testing.T) []core.AuditEntry {
	t.Helper()
	got, _, err := e.audit.List(context.Background(), core.AuditQuery{Action: "controller.quarantine.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

type rsProblem struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

func rsDecodeProblem(t *testing.T, rec *httptest.ResponseRecorder) rsProblem {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("Content-Type = %q, want application/problem+json (status %d, body %.300s)", ct, rec.Code, rec.Body.String())
	}
	var p rsProblem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode the problem: %v", err)
	}
	return p
}

type rsCase struct {
	name    string
	build   func(t *testing.T, e *rsEnv)
	outcome string

	// A refusal: status is not 200, code and detail are the problem's.
	status int
	code   string
	detail string

	// An outcome: the answered Record, the stored row and the locks left.
	kind      string
	from      string   // the Record's QuarantinedFrom
	wantLocks []string // the Record's Locks
	state     string   // the record's state afterwards
	message   string   // error_message afterwards
	errorCode string   // error_code afterwards

	retired   []string // plan ids the agents were asked to retire
	noLookups bool     // no agent was asked anything
}

func TestRsResolve(t *testing.T) {
	applied := func(host, plan string, state string) rsStep { return rsAt(host, plan, rsTerminal(state, 1)) }
	cases := []rsCase{
		{
			name:  "change: repair",
			build: func(t *testing.T, e *rsEnv) { e.change(t, rsID, "firewall.rule-add") }, outcome: "repair",
			status: 200, kind: "change", from: core.StateApplying, wantLocks: []string{"lock:R1"},
			state: core.StateNeedsRepair, errorCode: "core.needs_repair", message: "restore: quarantined; repair it after release",
			noLookups: true,
		},
		{
			name:  "change: done is refused",
			build: func(t *testing.T, e *rsEnv) { e.change(t, rsID, "firewall.rule-add") }, outcome: "done",
			status: 422, code: "request.invalid_input", detail: "a controller-side change is reconciled by repair",
			state: core.StateQuarantined, noLookups: true,
		},
		{
			name:  "change: never is refused",
			build: func(t *testing.T, e *rsEnv) { e.change(t, rsID, "firewall.rule-add") }, outcome: "never",
			status: 422, code: "request.invalid_input", detail: "a controller-side change is reconciled by repair",
			state: core.StateQuarantined, noLookups: true,
		},
		{
			name:  "cloud: repair",
			build: func(t *testing.T, e *rsEnv) { e.change(t, rsID, "cloudflare.hostname-add") }, outcome: "repair",
			status: 200, kind: "cloud", from: core.StateApplying, wantLocks: []string{"lock:R1"},
			state: core.StateNeedsRepair, errorCode: "core.needs_repair", message: "restore: quarantined; repair it after release",
			noLookups: true,
		},
		{
			name:  "cloud: done is refused",
			build: func(t *testing.T, e *rsEnv) { e.change(t, rsID, "tailnet.devices-authorize") }, outcome: "done",
			status: 422, code: "request.invalid_input", detail: "a controller-side change is reconciled by repair",
			state: core.StateQuarantined, noLookups: true,
		},
		{
			name: "composite: repair",
			build: func(t *testing.T, e *rsEnv) {
				e.composite(t, rsID, "forward", rsAt("h1", "p1", rsTerminal(core.StateConfirmed, 1)))
			}, outcome: "repair",
			status: 200, kind: "composite", from: core.StateApplying, wantLocks: []string{"lock:R1"},
			state: core.StateNeedsRepair, errorCode: "core.needs_repair", message: "restore: quarantined; repair it after release",
			noLookups: true,
		},
		{
			name: "composite: done when every step is applied",
			build: func(t *testing.T, e *rsEnv) {
				e.composite(t, rsID, "forward",
					applied("h1", "p1", core.StateConfirmed), applied("h2", "p2", core.StateCompleted),
					applied("h1", "p3", core.StateRepaired), applied("h2", "p4", core.StateKept))
			}, outcome: "done",
			status: 200, kind: "composite", from: core.StateApplying, wantLocks: []string{}, state: core.StateCompleted,
		},
		{
			name: "composite: done ignores a read step",
			build: func(t *testing.T, e *rsEnv) {
				read := rsStep{fxStep: fxStep{ID: "s-read", HostID: "h1", Action: "a/v1", State: core.StepRead, ChildPlanID: "p-read"}}
				e.composite(t, rsID, "forward", read, applied("h1", "p1", core.StateConfirmed))
			}, outcome: "done",
			status: 200, kind: "composite", from: core.StateApplying, wantLocks: []string{}, state: core.StateCompleted,
		},
		{
			name: "composite: never when no step was applied",
			build: func(t *testing.T, e *rsEnv) {
				none := rsStep{fxStep: fxStep{ID: "s-none", HostID: "h1", Action: "a/v1", State: core.StepPending}}
				e.composite(t, rsID, "forward",
					rsAt("h1", "p-abs", rsAbsent), rsAt("h2", "p-ret", rsRetiredLk), rsAt("h1", "p-abs2", rsAbsent),
					rsAt("h2", "p-rev", rsTerminal(core.StateReverted, 0)), rsAt("h1", "p-fail", rsTerminal(core.StateFailed, 0)),
					rsAt("h2", "p-int", rsTerminal(core.StateInterrupted, 0)), rsAt("h1", "p-aband", rsTerminal(core.StateAbandoned, 0)),
					none)
			}, outcome: "never",
			status: 200, kind: "composite", from: core.StateApplying, wantLocks: []string{}, state: core.StateAbandoned,
			message: "restore: never applied", retired: []string{"p-abs", "p-abs2"},
		},
		{
			name: "composite: never accepts retire answering absent",
			build: func(t *testing.T, e *rsEnv) {
				e.composite(t, rsID, "forward", rsAt("h1", "p-abs", rsAbsent))
				e.retireAnswers["p-abs"] = rsAbsent
			}, outcome: "never",
			status: 200, kind: "composite", from: core.StateApplying, wantLocks: []string{}, state: core.StateAbandoned,
			message: "restore: never applied", retired: []string{"p-abs"},
		},
		{
			name: "composite: never when a retire finds a change",
			build: func(t *testing.T, e *rsEnv) {
				e.composite(t, rsID, "forward", rsAt("h1", "p-late", rsAbsent))
				e.retireAnswers["p-late"] = rsRunningLk
			}, outcome: "never",
			status: 409, code: "plan.state_changed", state: core.StateQuarantined, retired: []string{"p-late"},
		},
		{
			name: "composite: never when a made effect remains",
			build: func(t *testing.T, e *rsEnv) {
				e.composite(t, rsID, "forward", rsAt("h1", "p-abs", rsAbsent), rsAt("h2", "p-made", rsTerminal(core.StateFailed, 1)))
			}, outcome: "never",
			status: 422, code: "request.invalid_input", detail: "partly applied", state: core.StateQuarantined,
		},
		{
			name: "composite: done when a step was not applied",
			build: func(t *testing.T, e *rsEnv) {
				e.composite(t, rsID, "forward", applied("h1", "p1", core.StateConfirmed), rsAt("h2", "p2", rsAbsent))
			}, outcome: "done",
			status: 422, code: "request.invalid_input", detail: "partly applied", state: core.StateQuarantined,
		},
		{
			name: "composite: never when a step was applied",
			build: func(t *testing.T, e *rsEnv) {
				e.composite(t, rsID, "forward", rsAt("h1", "p1", rsAbsent), applied("h2", "p2", core.StateConfirmed))
			}, outcome: "never",
			status: 422, code: "request.invalid_input", detail: "partly applied", state: core.StateQuarantined,
		},
		{
			name: "composite: done while a step runs",
			build: func(t *testing.T, e *rsEnv) {
				e.composite(t, rsID, "forward", applied("h1", "p1", core.StateConfirmed), rsAt("h2", "p-run", rsRunningLk))
			}, outcome: "done",
			status: 409, code: "core.change_busy", detail: "s-p-run", state: core.StateQuarantined,
		},
		{
			name: "composite: done while a step recovers",
			build: func(t *testing.T, e *rsEnv) {
				e.composite(t, rsID, "forward", rsAt("h2", "p-rec", rsRecovering), applied("h1", "p1", core.StateConfirmed))
			}, outcome: "done",
			status: 409, code: "core.change_busy", detail: "s-p-rec", state: core.StateQuarantined,
		},
		{
			name: "composite: never retires nothing while a step runs",
			build: func(t *testing.T, e *rsEnv) {
				e.composite(t, rsID, "forward", rsAt("h1", "p-abs", rsAbsent), rsAt("h2", "p-run", rsRunningLk))
			}, outcome: "never",
			status: 409, code: "core.change_busy", detail: "s-p-run", state: core.StateQuarantined,
		},
		{
			name: "composite: an agent that is not connected",
			build: func(t *testing.T, e *rsEnv) {
				e.composite(t, rsID, "forward", applied("h1", "p1", core.StateConfirmed), applied("h2", "p2", core.StateConfirmed))
				e.agentErrs["h2"] = errcode.New(errcode.HostOffline, "host h2 is offline")
			}, outcome: "done",
			status: 503, code: "host.offline", state: core.StateQuarantined,
		},
		{
			name: "composite: a lookup that fails",
			build: func(t *testing.T, e *rsEnv) {
				bad := rsAt("h1", "p-bad", rsAbsent)
				bad.err = errcode.New(errcode.HostOffline, "the session was lost")
				e.composite(t, rsID, "forward", bad)
			}, outcome: "never",
			status: 503, code: "host.offline", state: core.StateQuarantined,
		},
		{
			name: "composite: a step that runs on the controller",
			build: func(t *testing.T, e *rsEnv) {
				ctl := rsStep{fxStep: fxStep{ID: "s-ctl", Action: "a/v1", State: core.StepApplying, ChildPlanID: "p-ctl"}}
				e.composite(t, rsID, "forward", applied("h1", "p1", core.StateConfirmed), ctl)
			}, outcome: "done",
			status: 422, code: "request.invalid_input", detail: "s-ctl", state: core.StateQuarantined, noLookups: true,
		},
		{
			name: "composite: done on an unwinding composite",
			build: func(t *testing.T, e *rsEnv) {
				e.composite(t, rsID, "unwind", applied("h1", "p1", core.StateConfirmed))
			}, outcome: "done",
			status: 422, code: "request.invalid_input", detail: "unwinding", state: core.StateQuarantined,
		},
		{
			name: "composite: never on an unwinding composite",
			build: func(t *testing.T, e *rsEnv) {
				e.composite(t, rsID, "unwind", rsAt("h1", "p-abs", rsAbsent), rsAt("h2", "p-rev", rsTerminal(core.StateReverted, 0)))
			}, outcome: "never",
			status: 200, kind: "composite", from: core.StateApplying, wantLocks: []string{}, state: core.StateAbandoned,
			message: "restore: never applied", retired: []string{"p-abs"},
		},
		{
			name:  "rollout: never",
			build: func(t *testing.T, e *rsEnv) { e.rollout(t, rsID) }, outcome: "never",
			status: 200, kind: "rollout", from: "running", wantLocks: []string{}, state: "cancelled",
			message: "restore: never applied", noLookups: true,
		},
		{
			name:  "rollout: done is refused",
			build: func(t *testing.T, e *rsEnv) { e.rollout(t, rsID) }, outcome: "done",
			status: 422, code: "request.invalid_input", detail: "a rollout resolves never", state: core.StateQuarantined, noLookups: true,
		},
		{
			name:  "rollout: repair is refused",
			build: func(t *testing.T, e *rsEnv) { e.rollout(t, rsID) }, outcome: "repair",
			status: 422, code: "request.invalid_input", detail: "a rollout resolves never", state: core.StateQuarantined, noLookups: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := rsNewEnv(t)
			c.build(t, e)
			locksBefore := e.locks(t, rsID)

			rec := e.resolve(rsID, rsOutcome(c.outcome))
			if rec.Code != c.status {
				t.Fatalf("status = %d, want %d (body %.300s)", rec.Code, c.status, rec.Body.String())
			}
			row := e.row(t, rsID)
			if row.State != c.state {
				t.Errorf("state = %q, want %q", row.State, c.state)
			}
			if !slices.Equal(e.retired, c.retired) {
				t.Errorf("retired plans = %v, want %v", e.retired, c.retired)
			}
			if c.noLookups && len(e.asked) != 0 {
				t.Errorf("agents were asked about %v, want no lookup", e.asked)
			}
			if slices.ContainsFunc(e.asked, func(s string) bool { return strings.HasSuffix(s, "(private)") }) {
				t.Errorf("a lookup asked for private output: %v", e.asked)
			}

			if c.status != 200 {
				p := rsDecodeProblem(t, rec)
				if p.Code != c.code || !strings.Contains(p.Detail, c.detail) {
					t.Errorf("problem = %+v, want code %s and a detail containing %q", p, c.code, c.detail)
				}
				if got := e.locks(t, rsID); got != locksBefore {
					t.Errorf("a refused resolve changed the record's locks: %d, want %d", got, locksBefore)
				}
				if rows := e.auditRows(t); len(rows) != 0 {
					t.Errorf("a refused resolve wrote %d audit rows", len(rows))
				}
				return
			}

			var got Record
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode the record: %v", err)
			}
			if got.ID != rsID || got.Kind != c.kind || got.QuarantinedFrom != c.from {
				t.Errorf("record = %+v, want id %s, kind %s, quarantinedFrom %s", got, rsID, c.kind, c.from)
			}
			if got.Locks == nil || !slices.Equal(got.Locks, c.wantLocks) {
				t.Errorf("answered locks = %#v, want %#v (never null)", got.Locks, c.wantLocks)
			}
			if row.Message != c.message || row.ErrorCode != c.errorCode || row.From != c.from {
				t.Errorf("stored row = %+v, want error code %q, message %q, quarantined_from %q", row, c.errorCode, c.message, c.from)
			}
			if c.state == core.StateNeedsRepair && row.Owner != "" {
				t.Errorf("recovery_owner = %q, want it cleared", row.Owner)
			}
			if want := len(c.wantLocks); e.locks(t, rsID) != want {
				t.Errorf("lock rows left = %d, want %d", e.locks(t, rsID), want)
			}

			rows := e.auditRows(t)
			if len(rows) != 1 {
				t.Fatalf("audit rows = %d, want 1", len(rows))
			}
			a := rows[0]
			if a.Actor != "local:root@host-local" || a.Action != "controller.quarantine.resolve" || a.Phase != "resolve" ||
				a.Result != "ok" || a.ChangeID != rsID || a.HostID != "" || len(a.Input) != 0 {
				t.Errorf("audit row = %+v, want actor local:root@host-local, phase resolve, result ok, change %s and no input", a, rsID)
			}
		})
	}
}

func TestRsUnknownRecord(t *testing.T) {
	e := rsNewEnv(t)
	e.change(t, rsID, "firewall.rule-add")
	rec := e.resolve("NOPE", rsOutcome("repair"))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body %.300s)", rec.Code, rec.Body.String())
	}
	if p := rsDecodeProblem(t, rec); p.Code != "not_found" {
		t.Errorf("code = %q, want not_found", p.Code)
	}
	if rows := e.auditRows(t); len(rows) != 0 {
		t.Errorf("an unknown record wrote %d audit rows", len(rows))
	}
}

func TestRsRecordThatIsNotQuarantinedIsStateChanged(t *testing.T) {
	e := rsNewEnv(t)
	e.change(t, rsID, "firewall.rule-add")
	fxInsertChange(t, e.db, fxChange{ID: "R2", HostID: "h1", Action: "firewall.rule-add", State: core.StateApplying})
	fxInsertChange(t, e.db, fxChange{ID: "R3", HostID: "h1", Action: "firewall.rule-add", State: core.StateNeedsRepair})

	if rec := e.resolve(rsID, rsOutcome("repair")); rec.Code != http.StatusOK {
		t.Fatalf("first resolve: status = %d, want 200 (body %.300s)", rec.Code, rec.Body.String())
	}
	for _, id := range []string{rsID, "R2", "R3"} {
		rec := e.resolve(id, rsOutcome("repair"))
		if rec.Code != http.StatusConflict {
			t.Fatalf("resolve of %s: status = %d, want 409 (body %.300s)", id, rec.Code, rec.Body.String())
		}
		if p := rsDecodeProblem(t, rec); p.Code != "plan.state_changed" {
			t.Errorf("resolve of %s: code = %q, want plan.state_changed", id, p.Code)
		}
	}
	if rows := e.auditRows(t); len(rows) != 1 {
		t.Errorf("audit rows = %d, want 1 (only the first resolve)", len(rows))
	}
}

func TestRsRefusesABadRequest(t *testing.T) {
	bodies := map[string]string{
		"empty body":      ``,
		"not JSON":        `repair`,
		"empty outcome":   `{"outcome":""}`,
		"unknown outcome": `{"outcome":"maybe"}`,
		"unknown field":   `{"outcome":"repair","force":true}`,
		"no outcome":      `{}`,
		"two values":      `{"outcome":"repair"}{"outcome":"repair"}`,
		"over 64 KiB":     `{"outcome":"` + strings.Repeat("a", 70000) + `"}`,
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			e := rsNewEnv(t)
			e.change(t, rsID, "firewall.rule-add")
			rec := e.resolve(rsID, body)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422 (body %.300s)", rec.Code, rec.Body.String())
			}
			if p := rsDecodeProblem(t, rec); p.Code != "request.invalid_input" {
				t.Errorf("code = %q, want request.invalid_input", p.Code)
			}
			if got := e.row(t, rsID).State; got != core.StateQuarantined {
				t.Errorf("state = %q, want the record untouched", got)
			}
		})
	}
}
