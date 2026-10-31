package tailnet

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/ext"
)

const (
	keySweepService = "tailnet.keysweep"
	keySweepActor   = "system:" + keySweepService

	keySweepEvery       = 5 * time.Minute
	keyOrphanSweepEvery = 24 * time.Hour

	// keyRequestGrace is how long a create may still be answered: a
	// requested row younger than this may yet learn its key.
	keyRequestGrace = 15*time.Second + 5*time.Minute

	// keyWindowSlack is how long after its expiry a key's row keeps the
	// five-minute pass deleting it.
	keyWindowSlack = 24 * time.Hour
)

// keyNamespace matches the descriptions Darkpanel gives auth keys: a change
// id or a host id. A key outside it is never read as Darkpanel's.
var keyNamespace = regexp.MustCompile(`^darkpanel-(enroll-)?[0-9A-HJKMNP-TV-Z]{26}$`)

// Controller contributes the auth key sweep service. It reads deps only from
// the service's Run, since the server calls it with whatever deps it has.
func (m module) Controller(deps ext.Deps) ([]ext.Route, []ext.Service, error) {
	return nil, []ext.Service{{
		Name: keySweepService,
		Run:  func(ctx context.Context) error { return m.t.runKeySweep(ctx, deps) },
	}}, nil
}

// runKeySweep runs an orphan pass at start, then a pass every five minutes
// and an orphan pass every day until ctx ends. Each timer is armed again only
// after its pass, so passes never overlap and a fired timer's kind always
// fits in the channel.
func (t tn) runKeySweep(ctx context.Context, deps ext.Deps) error {
	pass := func(orphans bool) {
		err := t.sweep(ctx, deps, deps.Clock.Now(), orphans)
		if err != nil && ctx.Err() == nil {
			deps.Log.Warn("tailnet: auth key sweep failed", "orphans", orphans, "error", err)
		}
	}
	// A controller restarted more often than daily must still reach an
	// orphan pass.
	pass(true)

	due := make(chan bool, 2)
	arm := func(orphans bool) core.Timer {
		every := keySweepEvery
		if orphans {
			every = keyOrphanSweepEvery
		}
		return deps.Clock.AfterFunc(every, func() {
			select {
			case due <- orphans:
			default:
			}
		})
	}
	short, daily := arm(false), arm(true)
	defer func() {
		short.Stop()
		daily.Stop()
	}()
	for {
		select {
		case <-ctx.Done():
			return nil
		case orphans := <-due:
			pass(orphans)
			if orphans {
				daily = arm(true)
			} else {
				short = arm(false)
			}
		}
	}
}

// sweep is one pass over the auth keys in Darkpanel's namespace. It deletes
// the keys their rows leave to the sweep, and with orphans also keys with no
// row or a deleted one; it never settles an effect and never creates a key.
// A failure is collected and the pass goes on, so one provider failure does
// not hide the others.
func (t tn) sweep(ctx context.Context, deps ext.Deps, now time.Time, orphans bool) error {
	a, _, err := t.connected(ctx, deps.DB, deps.Sealer, deps.Clock)
	if err != nil {
		if errcode.From(err).Code == errcode.CloudNotConnected {
			return nil
		}
		return err
	}
	rows, err := listKeys(ctx, deps.DB.Read())
	if err != nil {
		return err
	}
	var errs []error
	byDescription := make(map[string]keyRow, len(rows))
	for _, r := range rows {
		if orphans && r.State == "requested" && now.Sub(r.RequestedAt) > keyRequestGrace {
			err := deps.DB.Write(ctx, func(tx *sql.Tx) error {
				return setKeyState(ctx, tx, r.Description, "uncertain", "", now)
			})
			if err != nil {
				errs = append(errs, err)
			} else {
				r.State = "uncertain"
			}
		}
		byDescription[r.Description] = r
	}

	keys, err := describedKeys(ctx, a)
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	listed := make(map[string]bool, len(keys))
	for _, k := range keys {
		if !keyNamespace.MatchString(k.Description) {
			continue
		}
		listed[k.Description] = true
		r, ok := byDescription[k.Description]
		if !keySweepable(r, ok, now, orphans) {
			continue
		}
		if err := t.sweepKey(ctx, deps, a, k, r.State == "retired", now); err != nil {
			errs = append(errs, err)
		}
	}

	// A retired row whose key is not listed and whose window is over has no
	// key left to delete; left retired it would block disconnecting the
	// account for ever.
	for _, r := range byDescription {
		if r.State != "retired" || listed[r.Description] || !now.After(keyWindowEnd(r)) {
			continue
		}
		err := deps.DB.Write(ctx, func(tx *sql.Tx) error {
			return setKeyState(ctx, tx, r.Description, "deleted", "", now)
		})
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// keySweepable reports whether a pass deletes a key whose row is r (ok false
// when it has none).
func keySweepable(r keyRow, ok bool, now time.Time, orphans bool) bool {
	if !ok {
		return orphans
	}
	switch r.State {
	case "uncertain", "retired":
		return orphans || !now.After(keyWindowEnd(r))
	case "deleted":
		return orphans
	default:
		// requested, live and used keys belong to a change still deciding or
		// to a key in use.
		return false
	}
}

// keyWindowEnd is when the five-minute pass stops deleting a row's key: its
// request time plus its expiry plus a day.
func keyWindowEnd(r keyRow) time.Time {
	return r.RequestedAt.Add(time.Duration(r.ExpirySeconds)*time.Second + keyWindowSlack)
}

// sweepKey deletes one key, audits the deletion and, for a retired row,
// marks the row deleted.
func (t tn) sweepKey(ctx context.Context, deps ext.Deps, a *api, k KeyInfo, retired bool, now time.Time) error {
	// A not-found answer counts as deleted: the cloud client resends a DELETE
	// whose answer was lost, so a delete that committed can come back 404.
	if err := a.DeleteKey(ctx, k.ID); err != nil && !authkeyGone(err) {
		return err
	}
	// The key is gone, so its record is written even when the pass is
	// cancelled meanwhile.
	record := context.WithoutCancel(ctx)
	input, err := json.Marshal(map[string]string{"keyId": k.ID, "description": k.Description})
	if err != nil {
		return fmt.Errorf("tailnet: encode the sweep audit of key %s: %w", k.ID, err)
	}
	var errs []error
	if err := deps.Audit.Record(record, core.AuditEntry{
		Actor: keySweepActor, Action: keySweepService, Phase: "delete", Result: "ok", At: now, Input: input,
	}); err != nil {
		errs = append(errs, fmt.Errorf("tailnet: audit the sweep of key %s: %w", k.ID, err))
	}
	if retired {
		err := deps.DB.Write(record, func(tx *sql.Tx) error {
			return setKeyState(record, tx, k.Description, "deleted", k.ID, now)
		})
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
