package controller

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/coreactions"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/ext"
	"github.com/darkraise/darkpanel/internal/core/store"
)

const (
	// pollInterval is how often the poller reads every host.
	pollInterval = time.Minute
	// pollTimeout bounds one host's read.
	pollTimeout = 20 * time.Second
	// maxSkew is how far an agent's clock may be from the controller's
	// before its answer is refused: alerts compare stored minutes with the
	// controller's clock, so a skewed host would otherwise fall silent.
	maxSkew = 2 * time.Minute
)

// The poll status codes the module writes itself; any other is the error
// code of the failed read.
const (
	CodeClockSkew = "metrics.clock_skew"
	CodeBadAnswer = "metrics.bad_answer"
)

// samplesRef is the agent read the poller calls.
var samplesRef = core.ActionRef{Name: moduleID + ".samples.get", Version: 1}

// Side is the module's controller half: what its manifest declares and what
// its ext.Controller contributes.
type Side struct {
	Actions  []core.ActionDef
	Services func(deps ext.Deps) []ext.Service
}

// New returns the module's controller half: metrics.history.get, the alert
// rule actions and metrics.alert.list, and the poller (which evaluates the
// rules after each pass) and rollup services. Its services also register
// the rules' group referrer, so core.group.delete refuses a group a rule
// targets.
func New() Side {
	return Side{
		Actions: append([]core.ActionDef{historyAction(), alertListAction()}, ruleActions()...),
		Services: func(deps ext.Deps) []ext.Service {
			coreactions.RegisterGroupReferrer(moduleID+".rules", ruleReferrer)
			if deps.DB == nil || deps.Dispatch == nil {
				// An assembly with no controller database or dispatcher has
				// nothing to poll into.
				return nil
			}
			p := newPoller(deps.DB, deps.Dispatch, deps.Clock, deps.Log)
			p.after = newEvaluator(deps.DB, deps.Hosts, deps.Notifier, deps.Clock, deps.Log).evaluate
			r := newRollup(deps.DB, deps.Clock, deps.Log)
			return []ext.Service{{Name: moduleID + ".poll", Run: p.Run}, {Name: moduleID + ".rollup", Run: r.Run}}
		},
	}
}

// poller copies each host's minute averages into controller.db once a
// minute, passing the newest stored minute so an outage of up to an hour
// backfills. It reads with an ordinary read, which writes no audit row.
type poller struct {
	db       *store.DB
	dispatch ext.Dispatcher
	clock    core.Clock
	log      *slog.Logger
	state    map[string]string               // each host's last poll outcome: "ok" or its code
	after    func(ctx context.Context) error // runs after each pass: the alert evaluator
}

func newPoller(db *store.DB, d ext.Dispatcher, clock core.Clock, log *slog.Logger) *poller {
	if clock == nil {
		clock = core.SystemClock{}
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &poller{db: db, dispatch: d, clock: clock, log: log, state: map[string]string{}}
}

// Run polls every pollInterval until ctx ends, evaluating the alert rules
// after each pass. A failed pass is logged and the next one starts on time.
func (p *poller) Run(ctx context.Context) error {
	for {
		if err := p.pass(ctx); err != nil && ctx.Err() == nil {
			p.log.Warn("metrics: poll the hosts", "error", err)
		}
		if p.after != nil {
			if err := p.after(ctx); err != nil && ctx.Err() == nil {
				p.log.Warn("metrics: evaluate the alert rules", "error", err)
			}
		}
		fired := make(chan struct{})
		t := p.clock.AfterFunc(pollInterval, func() { close(fired) })
		select {
		case <-ctx.Done():
			t.Stop()
			return nil
		case <-fired:
		}
	}
}

// pass polls each host that is not revoked and has the module enabled (no
// core_module_state row means the module's default, enabled).
func (p *poller) pass(ctx context.Context) error {
	rows, err := p.db.Read().QueryContext(ctx, `SELECT h.id FROM core_hosts h
		LEFT JOIN core_module_state s ON s.host_id = h.id AND s.module_id = ?
		WHERE h.state != 'revoked' AND coalesce(s.enabled, 1) = 1 ORDER BY h.id`, moduleID)
	if err != nil {
		return fmt.Errorf("metrics: read the hosts to poll: %w", err)
	}
	var hosts []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return fmt.Errorf("metrics: read the hosts to poll: %w", err)
		}
		hosts = append(hosts, id)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("metrics: read the hosts to poll: %w", err)
	}
	for _, id := range hosts {
		if ctx.Err() != nil {
			return nil
		}
		if err := p.pollHost(ctx, id); err != nil {
			p.log.Warn("metrics: poll a host", "host", id, "error", err)
		}
	}
	return nil
}

// pollHost reads hostID's minutes after its newest stored one and stores
// them with its poll status in one transaction. An offline host is not
// polled and keeps its status.
func (p *poller) pollHost(ctx context.Context, hostID string) error {
	since, err := lastMinute(ctx, p.db.Read(), hostID)
	if err != nil {
		return err
	}
	in := map[string]string{}
	if since != "" {
		in["since"] = since
	}
	raw, _ := json.Marshal(in)
	rctx, cancel := context.WithTimeout(ctx, pollTimeout)
	out, err := p.dispatch.Read(rctx, core.Principal{Actor: "system:" + moduleID + ".poll", Scope: core.ScopeRead}, hostID, samplesRef, raw)
	cancel()
	now := p.clock.Now().UTC().Truncate(time.Second)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		code := errcode.From(err).Code
		if code == errcode.HostOffline {
			return nil
		}
		return p.fail(ctx, hostID, string(code), now, nil)
	}
	var ans answer
	if err := json.Unmarshal(out, &ans); err != nil || ans.Now.IsZero() {
		return p.fail(ctx, hostID, CodeBadAnswer, now, nil)
	}
	skewDur := ans.Now.Sub(now)
	skew := int64(math.Round(skewDur.Seconds()))
	if skewDur > maxSkew || skewDur < -maxSkew {
		return p.fail(ctx, hostID, CodeClockSkew, now, &skew)
	}
	points := storable(ans.Samples, now)
	err = p.db.Write(ctx, func(tx *sql.Tx) error {
		if err := storeMinutes(ctx, tx, hostID, points); err != nil {
			return err
		}
		return recordOK(ctx, tx, hostID, now, skew, ans.CPUs)
	})
	if err != nil {
		return err
	}
	p.changed(hostID, "ok")
	return nil
}

func (p *poller) fail(ctx context.Context, hostID, code string, now time.Time, skew *int64) error {
	err := p.db.Write(ctx, func(tx *sql.Tx) error { return recordFailure(ctx, tx, hostID, code, now, skew) })
	if err != nil {
		return err
	}
	p.changed(hostID, code)
	return nil
}

// changed logs one line when hostID's poll outcome differs from its last;
// a first successful poll logs nothing.
func (p *poller) changed(hostID, outcome string) {
	last, seen := p.state[hostID]
	p.state[hostID] = outcome
	switch {
	case last == outcome || (!seen && outcome == "ok"):
	case outcome == "ok":
		p.log.Info("metrics: polling the host works again", "host", hostID)
	default:
		p.log.Warn("metrics: polling the host failed", "host", hostID, "code", outcome)
	}
}
