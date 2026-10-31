package cloudflare

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/cloudflare/ingress"
)

// exposureRemoveParent is the one composite whose steps may remove a
// hostname routed into the panel network.
const exposureRemoveParent = "panel.exposure.remove"

// hostnameRemove declares cloudflare.hostname.remove, which undoes a
// hostname add as a forward operation: the DNS record, then the ingress
// rule, then the row.
func (c cf) hostnameRemove() core.ActionDef {
	return core.DefineMutating[HostnameInput, HostnameView](core.ActionSpec{
		Name: ID + ".hostname.remove", Summary: "Remove a public hostname's DNS record and ingress rule.", Version: 1,
		Class: core.ClassCompensable, Side: core.SideController, LockKeys: []string{"cloudflare"},
		CompensatedBy: ID + ".hostname.add", Converges: true,
	}, hostnameRemoveHandler{c: c, hostname: hostnameHandler{c: c}})
}

// hostnameRemoveHandler removes a hostname, tracking its progress in the
// hostname row's phase so recovery can finish a removal cut off midway. The
// add's handler is a named field, not embedded: its Inverse and Recover
// must not become this action's, whose compensation is hostname.add.
type hostnameRemoveHandler struct {
	c        cf
	hostname hostnameHandler
}

// hostnameRemoval is what a removal found: the account's api, the hostname
// and tunnel rows, the configuration read, and the hostname's DNS record and
// rule, each nil when already gone. tunnelGone is set when the tunnel no
// longer exists at Cloudflare, which takes its rule with it.
type hostnameRemoval struct {
	a          *api
	row        hostnameRow
	tunnel     tunnelRow
	cfg        ingress.Config
	record     *DNSRecord
	rule       *ingress.Rule
	tunnelGone bool
}

// inspect loads the row of a hostname and reads what is left to remove of
// it. found is false when there is no row. It only reads, so every error is
// Definite.
func (h hostnameRemoveHandler) inspect(ctx context.Context, env core.Env, hostname string) (hostnameRemoval, bool, error) {
	row, found, err := loadHostname(ctx, env.DB.Read(), hostname)
	if err != nil {
		return hostnameRemoval{}, false, core.Definite(err)
	}
	if !found {
		return hostnameRemoval{}, false, nil
	}
	r, err := h.inspectRow(ctx, env, row)
	return r, true, err
}

// inspectRow reads what is left to remove of a stored hostname row and
// refuses a removal that would undo a change it does not own. It only reads,
// so every error is Definite.
func (h hostnameRemoveHandler) inspectRow(ctx context.Context, env core.Env, row hostnameRow) (hostnameRemoval, error) {
	if row.Phase == "adding_rule" || row.Phase == "adding_record" {
		return hostnameRemoval{}, hostnameConflict("hostname %s is still being added by change %s", row.Hostname, row.ChangeID)
	}
	if reserved(row.Service) && env.Call.ParentAction != exposureRemoveParent {
		return hostnameRemoval{}, core.Definite(errcode.New(errcode.CloudflareReserved, fmt.Sprintf(
			"hostname %s routes into the panel network %s, which only %s removes", row.Hostname, panelNetwork, exposureRemoveParent)))
	}
	a, _, err := h.c.connected(ctx, env.DB, env.Sealer, env.Clock)
	if err != nil {
		return hostnameRemoval{}, core.Definite(err)
	}
	tunnel, found, err := loadTunnel(ctx, env.DB.Read(), row.TunnelID)
	if err != nil {
		return hostnameRemoval{}, core.Definite(err)
	}
	// loadTunnel also matches a name; only the row with this id is the
	// hostname's.
	if !found || tunnel.ID != row.TunnelID {
		return hostnameRemoval{}, core.Definite(fmt.Errorf("cloudflare: tunnel %s of hostname %s is not stored", row.TunnelID, row.Hostname))
	}
	r := hostnameRemoval{a: a, row: row, tunnel: tunnel}
	if row.Phase == "active" {
		r.cfg, err = h.c.checkDrift(ctx, a, tunnel)
	} else {
		// A removal of this module is in flight, so the stored hash may
		// predate one of its writes that landed.
		r.cfg, err = a.Configuration(ctx, tunnel.ID)
	}
	switch {
	case hostnameAbsent(err):
		r.tunnelGone = true
	case err != nil:
		return hostnameRemoval{}, core.Definite(err)
	}
	if rule, ok := ingress.Lookup(r.cfg, row.Hostname); ok {
		r.rule = &rule
	}
	if row.RecordID == "" {
		return r, nil
	}
	records, err := a.DNSRecords(ctx, row.ZoneID, row.Hostname, "")
	if err != nil {
		return hostnameRemoval{}, core.Definite(err)
	}
	for i := range records {
		if records[i].ID != row.RecordID {
			continue
		}
		if records[i].Content != hostnameTarget(tunnel.ID) || records[i].Comment != hostnameMarker(row.ChangeID) {
			return hostnameRemoval{}, hostnameConflict("DNS record %s of %s was changed by someone else; it was left in place",
				row.RecordID, row.Hostname)
		}
		r.record = &records[i]
	}
	return r, nil
}

// Plan shows the record and the rule the removal deletes. It only reads.
func (h hostnameRemoveHandler) Plan(ctx context.Context, env core.Env, in HostnameInput) (core.PlanOutcome, error) {
	r, found, err := h.inspect(ctx, env, in.Hostname)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	if !found {
		return core.PlanOutcome{StateHash: tunnelStateHash(in.Hostname)}, nil
	}
	parts := []any{r.row.Phase}
	var diff []core.DiffLine
	if r.record != nil {
		parts = append(parts, r.record.ID)
		diff = append(diff, core.DiffLine{Op: "-",
			Text: "dns record " + r.record.ID + ": CNAME " + r.row.Hostname + " -> " + r.record.Content})
	}
	if r.rule != nil {
		diff = append(diff, core.DiffLine{Op: "-", Text: "ingress rule: " + r.row.Hostname + " -> " + r.rule.Service})
	}
	if len(diff) > 0 {
		diff = append(diff, core.DiffLine{Op: " ",
			Text: "read-then-write: a change another client makes between the read and the delete is not detected"})
	}
	parts = append(parts, ingress.Hash(r.cfg))
	return core.PlanOutcome{Diff: diff, StateHash: tunnelStateHash(parts...)}, nil
}

// Apply removes the hostname's DNS record and rule and then its row. A
// hostname with no row is already removed.
func (h hostnameRemoveHandler) Apply(ctx context.Context, env core.Env, in HostnameInput) (HostnameView, error) {
	r, found, err := h.inspect(ctx, env, in.Hostname)
	if err != nil {
		return HostnameView{}, err
	}
	if !found {
		return HostnameView{Hostname: in.Hostname, Phase: "removed"}, nil
	}
	if err := h.remove(ctx, env, r); err != nil {
		return HostnameView{}, err
	}
	return removedView(r.row), nil
}

// remove deletes the record, then the rule, then the row, moving the row's
// phase on before each provider write. Each step reads the provider before
// it writes and journals an effect only for a write it makes, so running it
// again from any phase finishes what is left.
func (h hostnameRemoveHandler) remove(ctx context.Context, env core.Env, r hostnameRemoval) error {
	row := r.row
	if row.Phase == "active" {
		if err := h.setPhase(ctx, env, row.Hostname, "removing_record"); err != nil {
			return err
		}
	}
	if r.record != nil {
		if err := h.removeRecord(ctx, env, r); err != nil {
			return err
		}
	}
	if row.Phase != "removing_rule" {
		if err := h.setPhase(ctx, env, row.Hostname, "removing_rule"); err != nil {
			return err
		}
	}
	last, gone, err := h.removeRule(ctx, env, r)
	if err != nil {
		return err
	}
	return env.DB.Write(ctx, func(tx *sql.Tx) error {
		if !gone {
			if err := setTunnelHash(ctx, tx, r.tunnel.ID, ingress.Hash(last)); err != nil {
				return err
			}
		}
		return deleteHostname(ctx, tx, row.Hostname)
	})
}

// setPhase moves the hostname row to phase, keeping its zone and record ids.
func (h hostnameRemoveHandler) setPhase(ctx context.Context, env core.Env, hostname, phase string) error {
	return env.DB.Write(ctx, func(tx *sql.Tx) error {
		return setHostnamePhase(ctx, tx, hostname, phase, "", "", env.Clock.Now())
	})
}

// removeRecord deletes the hostname's DNS record as one journaled effect. A
// record already gone when the delete arrives counts as deleted.
func (h hostnameRemoveHandler) removeRecord(ctx context.Context, env core.Env, r hostnameRemoval) error {
	eff := cloudEffect(core.EffectDelete, dnsRecordResource(r.row.ZoneID, r.record.ID), hostnameRecordDoc(r.record))
	eff.After = &core.StateDoc{State: hostnameRecordDoc(nil)}
	seq, err := env.Effects.Intend(ctx, eff, nil)
	if err != nil {
		return err
	}
	if err := r.a.DeleteDNSRecord(ctx, r.row.ZoneID, r.record.ID); err != nil && !hostnameAbsent(err) {
		err = fmt.Errorf("cloudflare: delete DNS record %s of %s: %w", r.record.ID, r.row.Hostname, err)
		if tunnelRefused(err) {
			err = core.Definite(err)
		}
		return hostnameWriteFailed(ctx, env, seq, eff.Resource, err)
	}
	return env.Effects.Made(ctx, seq, eff.Resource, core.StateDoc{State: hostnameRecordDoc(nil)})
}

// removeRule drops the hostname's rule from the tunnel's live configuration
// as one journaled effect, and answers the configuration read last. A rule
// already gone is not written. gone reports that the tunnel no longer exists
// at Cloudflare, so there is no configuration to write or hash.
func (h hostnameRemoveHandler) removeRule(ctx context.Context, env core.Env, r hostnameRemoval) (last ingress.Config, gone bool, err error) {
	if r.tunnelGone {
		return ingress.Config{}, true, nil
	}
	hostname := r.row.Hostname
	cfg, err := r.a.Configuration(ctx, r.tunnel.ID)
	if hostnameAbsent(err) {
		return ingress.Config{}, true, nil
	}
	if err != nil {
		return ingress.Config{}, false, fmt.Errorf("cloudflare: read the configuration of tunnel %s: %w", r.tunnel.Name, err)
	}
	if _, present := ingress.Lookup(cfg, hostname); !present {
		return cfg, false, nil
	}
	before, err := hostnameRuleDoc(cfg, hostname)
	if err != nil {
		return ingress.Config{}, false, err
	}
	eff := cloudEffect(core.EffectModify, ingressResource(r.tunnel.ID, hostname), before)
	eff.After = &core.StateDoc{State: json.RawMessage(`{"rule":null}`)}
	seq, err := env.Effects.Intend(ctx, eff, nil)
	if err != nil {
		return ingress.Config{}, false, err
	}
	next, _ := ingress.Remove(cfg, hostname)
	if err := r.a.PutConfiguration(ctx, r.tunnel.ID, next); err != nil {
		return ingress.Config{}, false, hostnameWriteFailed(ctx, env, seq, eff.Resource,
			fmt.Errorf("cloudflare: write the configuration of tunnel %s: %w", r.tunnel.Name, err))
	}
	readBack, err := r.a.Configuration(ctx, r.tunnel.ID)
	if err != nil {
		// The effect stays intended: the write may have committed.
		return ingress.Config{}, false, fmt.Errorf("cloudflare: read back the configuration of tunnel %s: %w", r.tunnel.Name, err)
	}
	post, err := hostnameRuleDoc(readBack, hostname)
	if err != nil {
		return ingress.Config{}, false, err
	}
	if err := env.Effects.Made(ctx, seq, eff.Resource, core.StateDoc{State: post}); err != nil {
		return ingress.Config{}, false, err
	}
	if _, present := ingress.Lookup(readBack, hostname); present || ingress.Hash(readBack) != ingress.Hash(next) {
		return ingress.Config{}, false, fmt.Errorf("cloudflare: the configuration of tunnel %s differs from the one written", r.tunnel.Name)
	}
	return readBack, false, nil
}

// Verify passes when the hostname has no row and, when the output names its
// tunnel, the tunnel's configuration has no rule for it or the tunnel is gone
// at Cloudflare. It only reads.
func (h hostnameRemoveHandler) Verify(ctx context.Context, env core.Env, in HostnameInput, out HostnameView) error {
	if _, found, err := loadHostname(ctx, env.DB.Read(), in.Hostname); err != nil {
		return err
	} else if found {
		return fmt.Errorf("cloudflare: hostname %s is still stored", in.Hostname)
	}
	if out.Tunnel == "" {
		return nil
	}
	a, _, err := h.c.connected(ctx, env.DB, env.Sealer, env.Clock)
	if err != nil {
		return err
	}
	cfg, err := a.Configuration(ctx, out.Tunnel)
	if hostnameAbsent(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, ok := ingress.Lookup(cfg, in.Hostname); ok {
		return fmt.Errorf("cloudflare: tunnel %s still has an ingress rule for %s", out.Tunnel, in.Hostname)
	}
	return nil
}

// Observe reads the record or the rule an effect names, as the add's
// handler does: an absent record is null and an absent rule {"rule": null}.
func (h hostnameRemoveHandler) Observe(ctx context.Context, env core.Env, e core.Effect) (core.Observation, error) {
	return h.hostname.Observe(ctx, env, e)
}

// Recover finishes a removal cut off midway, forward. It runs as the
// change's lock owner, so at most one hostname row is mid-removal.
func (h hostnameRemoveHandler) Recover(ctx context.Context, env core.Env) (core.RecoveryOutcome, error) {
	rows, err := listHostnames(ctx, env.DB.Read())
	if err != nil {
		return core.RecoveryOutcome{}, err
	}
	var removing []hostnameRow
	for _, r := range rows {
		if r.Phase == "removing_record" || r.Phase == "removing_rule" {
			removing = append(removing, r)
		}
	}
	switch len(removing) {
	case 0:
	case 1:
		r, err := h.inspectRow(ctx, env, removing[0])
		if err != nil {
			return core.RecoveryOutcome{}, err
		}
		if err := h.remove(ctx, env, r); err != nil {
			return core.RecoveryOutcome{}, err
		}
		return removedOutcome(removedView(r.row))
	default:
		return core.RecoveryOutcome{}, fmt.Errorf("cloudflare: %d hostnames are mid-removal, want at most one", len(removing))
	}
	effs, err := env.Effects.List(ctx)
	if err != nil {
		return core.RecoveryOutcome{}, err
	}
	for _, e := range effs {
		rest, ok := strings.CutPrefix(e.Resource, ingressNoun)
		if !ok || e.State != core.EffectMade {
			continue
		}
		tunnelID, hostname, ok := strings.Cut(rest, "/")
		if !ok || tunnelID == "" || hostname == "" {
			return core.RecoveryOutcome{}, fmt.Errorf("cloudflare: effect %d names no ingress rule: %s", e.Seq, e.Resource)
		}
		// The row was deleted before the outcome was recorded.
		return removedOutcome(HostnameView{Hostname: hostname, Tunnel: tunnelID, Phase: "removed", Health: "unknown"})
	}
	return core.RecoveryOutcome{State: core.RecoveryFailed, Reason: "the removal of the hostname had not started"}, nil
}

// removedView is the output of a removal of row.
func removedView(row hostnameRow) HostnameView {
	return HostnameView{
		Hostname: row.Hostname, Tunnel: row.TunnelID, Service: row.Service, RecordID: row.RecordID, Phase: "removed", Health: "unknown",
	}
}

// removedOutcome is the outcome of a recovery that completed a removal, with
// the removed view as the action's output.
func removedOutcome(v HostnameView) (core.RecoveryOutcome, error) {
	out, err := json.Marshal(v)
	if err != nil {
		return core.RecoveryOutcome{}, fmt.Errorf("cloudflare: encode hostname %s: %w", v.Hostname, err)
	}
	return core.RecoveryOutcome{State: core.RecoveryCompleted, Output: out}, nil
}
