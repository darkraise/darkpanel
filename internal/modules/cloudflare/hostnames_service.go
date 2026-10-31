package cloudflare

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/ext"
	"github.com/darkraise/darkpanel/internal/modules/cloudflare/ingress"
)

const (
	hostnamesService = "cloudflare.hostnames"
	hostnamesActor   = "system:" + hostnamesService
	hostnamesEvery   = 5 * time.Minute
)

// Controller contributes the hostnames service. It reads deps only from the
// service's Run, since the server calls it with whatever deps it has.
func (m module) Controller(deps ext.Deps) ([]ext.Route, []ext.Service, error) {
	return nil, []ext.Service{{
		Name: hostnamesService,
		Run:  func(ctx context.Context) error { return m.c.runHostnames(ctx, deps) },
	}}, nil
}

// runHostnames runs a pass at start, then one every five minutes until ctx
// ends. The timer is armed again only after its pass, so passes never
// overlap. A pass's error ends the service for the supervisor to restart.
func (c cf) runHostnames(ctx context.Context, deps ext.Deps) error {
	pass := func() error {
		if err := c.checkHostnames(ctx, deps, deps.Clock.Now()); err != nil && ctx.Err() == nil {
			return err
		}
		return nil
	}
	if err := pass(); err != nil {
		return err
	}
	due := make(chan struct{}, 1)
	arm := func() core.Timer {
		return deps.Clock.AfterFunc(hostnamesEvery, func() {
			select {
			case due <- struct{}{}:
			default:
			}
		})
	}
	timer := arm()
	defer func() { timer.Stop() }()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-due:
			if err := pass(); err != nil {
				return err
			}
			timer = arm()
		}
	}
}

// checkHostnames is one pass over the managed tunnels that have hostnames: it
// stores each tunnel's drift and each hostname's health, and audits every
// change of either. It only reads from Cloudflare and never plans a change.
// A tunnel with an add or a remove in flight is left to that operation.
func (c cf) checkHostnames(ctx context.Context, deps ext.Deps, now time.Time) error {
	a, _, err := c.connected(ctx, deps.DB, deps.Sealer, deps.Clock)
	if err != nil {
		if errcode.From(err).Code == errcode.CloudNotConnected {
			return nil
		}
		return err
	}
	tunnels, err := listTunnels(ctx, deps.DB.Read())
	if err != nil {
		return err
	}
	hosts, err := listHostnames(ctx, deps.DB.Read())
	if err != nil {
		return err
	}
	byTunnel := map[string][]hostnameRow{}
	for _, h := range hosts {
		byTunnel[h.TunnelID] = append(byTunnel[h.TunnelID], h)
	}
	for _, t := range tunnels {
		rows := byTunnel[t.ID]
		if len(rows) == 0 || !hostnamesSettled(rows) {
			continue
		}
		if err := c.checkTunnel(ctx, deps, a, t, rows, now); err != nil {
			return err
		}
	}
	return nil
}

// hostnamesSettled reports whether every row is active, so no add or remove
// is changing the tunnel's configuration or records.
func hostnamesSettled(rows []hostnameRow) bool {
	for _, r := range rows {
		if r.Phase != "active" {
			return false
		}
	}
	return true
}

// checkTunnel checks one tunnel and its hostname rows, stores the result in
// one write and audits what it changed. An unreadable configuration makes
// every hostname unknown and leaves the tunnel row as stored.
func (c cf) checkTunnel(ctx context.Context, deps ext.Deps, a *api, t tunnelRow, rows []hostnameRow, now time.Time) error {
	checked := make(map[string]hostnameRow, len(rows))
	health := make(map[string]string, len(rows))
	cfg, cfgErr := a.Configuration(ctx, t.ID)
	if cfgErr != nil && ctx.Err() != nil {
		return fmt.Errorf("cloudflare: check tunnel %s: %w", t.Name, cfgErr)
	}
	drift := t.ConfigHash != "" && cfgErr == nil && t.ConfigHash != ingress.Hash(cfg)
	for _, r := range rows {
		checked[r.Hostname] = r
		if cfgErr != nil {
			health[r.Hostname] = "unknown"
			continue
		}
		h, err := hostnameHealth(ctx, a, cfg, r)
		if err != nil {
			return err
		}
		health[r.Hostname] = h
	}

	var audits []json.RawMessage
	err := deps.DB.Write(ctx, func(tx *sql.Tx) error {
		audits = audits[:0]
		// An add or a remove may have started or finished since the rows were
		// read; its writes win over this check's.
		cur, found, err := loadTunnel(ctx, tx, t.ID)
		if err != nil || !found || cur.ID != t.ID || cur.ConfigHash != t.ConfigHash {
			return err
		}
		all, err := listHostnames(ctx, tx)
		if err != nil {
			return err
		}
		var current []hostnameRow
		for _, r := range all {
			if r.TunnelID == t.ID {
				current = append(current, r)
			}
		}
		if !hostnamesSettled(current) {
			return nil
		}
		if cfgErr == nil {
			if err := setTunnelCheck(ctx, tx, t.ID, drift, now); err != nil {
				return err
			}
			if cur.Drift != drift {
				audits = append(audits, hostnamesAuditInput(struct {
					Tunnel string `json:"tunnel"`
					Drift  bool   `json:"drift"`
				}{t.ID, drift}))
			}
		}
		for _, r := range current {
			was, ok := checked[r.Hostname]
			if !ok || was.ChangeID != r.ChangeID || was.RecordID != r.RecordID {
				continue
			}
			to := health[r.Hostname]
			if err := setHostnameHealth(ctx, tx, r.Hostname, to, now); err != nil {
				return err
			}
			if r.Health != to {
				audits = append(audits, hostnamesAuditInput(struct {
					Hostname string `json:"hostname"`
					From     string `json:"from"`
					To       string `json:"to"`
				}{r.Hostname, r.Health, to}))
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	// The check is stored, so its audit rows are written even when the pass
	// is cancelled meanwhile.
	record := context.WithoutCancel(ctx)
	var errs []error
	for _, input := range audits {
		if err := deps.Audit.Record(record, core.AuditEntry{
			Actor: hostnamesActor, Action: hostnamesService, Phase: "check", Result: "ok", At: now, Input: input,
		}); err != nil {
			errs = append(errs, fmt.Errorf("cloudflare: audit the check of tunnel %s: %w", t.Name, err))
		}
	}
	return errors.Join(errs...)
}

// hostnameHealth checks one active hostname against its tunnel's
// configuration and its DNS record. Only a cancelled context is an error; any
// other failed read makes the health unknown.
func hostnameHealth(ctx context.Context, a *api, cfg ingress.Config, r hostnameRow) (string, error) {
	if _, ok := ingress.Lookup(cfg, r.Hostname); !ok {
		return "rule_missing", nil
	}
	rec, err := a.DNSRecord(ctx, r.ZoneID, r.RecordID)
	switch {
	case hostnameAbsent(err):
		return "record_missing", nil
	case err != nil && ctx.Err() != nil:
		return "", fmt.Errorf("cloudflare: check hostname %s: %w", r.Hostname, err)
	case err != nil:
		return "unknown", nil
	case rec.Content != hostnameTarget(r.TunnelID) || !rec.Proxied || rec.Comment != hostnameMarker(r.ChangeID):
		return "record_changed", nil
	}
	return "ok", nil
}

// hostnamesAuditInput encodes the input of one check audit row.
func hostnamesAuditInput(v any) json.RawMessage {
	// Marshalling a struct of strings and a boolean cannot fail.
	b, _ := json.Marshal(v)
	return b
}
