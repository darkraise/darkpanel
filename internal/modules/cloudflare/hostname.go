package cloudflare

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"strings"

	"github.com/darkraise/darkpanel/internal/controller/cloud"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/cloudflare/ingress"
)

// panelNetwork is the address range of the panel network (spec 8). Only the
// panel's own exposure may route a hostname into it.
var panelNetwork = netip.MustParsePrefix("172.31.255.0/24")

// The resource nouns of the hostname effects: a tunnel's ingress rule for one
// hostname, and one DNS record of a zone.
const (
	ingressNoun   = "cloudflare.ingress:"
	dnsRecordNoun = "cloudflare.dnsrecord:"
)

// reserved reports whether service routes to an address of the panel
// network. A service that does not parse, has no host or names its host by a
// name rather than an address is not reserved.
func reserved(service string) bool {
	u, err := url.Parse(service)
	if err != nil || u.Host == "" {
		return false
	}
	addr, err := netip.ParseAddr(u.Hostname())
	if err != nil {
		return false
	}
	return panelNetwork.Contains(addr.Unmap())
}

// ingressResource names the ingress rule of hostname in a tunnel's
// configuration.
func ingressResource(tunnelID, hostname string) string {
	return ingressNoun + tunnelID + "/" + hostname
}

// dnsRecordResource names one DNS record of a zone.
func dnsRecordResource(zoneID, recordID string) string {
	return dnsRecordNoun + zoneID + "/" + recordID
}

// hostnameMarker is the comment of the DNS record a change creates, which
// also finds the record when the answer to its creation was lost.
func hostnameMarker(changeID string) string { return "darkpanel:" + changeID }

// hostnameTarget is the CNAME content that routes a hostname to a tunnel.
func hostnameTarget(tunnelID string) string { return tunnelID + ".cfargotunnel.com" }

// hostnameViewOf is the client-facing form of a hostname row.
func hostnameViewOf(h hostnameRow) HostnameView {
	v := HostnameView{
		Hostname: h.Hostname, Tunnel: h.TunnelID, Service: h.Service, RecordID: h.RecordID, Phase: h.Phase, Health: h.Health,
	}
	if !h.CheckedAt.IsZero() {
		at := h.CheckedAt
		v.CheckedAt = &at
	}
	return v
}

// checkDrift reads a tunnel's configuration and refuses it with
// cloud.conflict when it no longer hashes to the stored hash; a tunnel with
// no stored hash has not drifted. The refusal is not marked Definite: a
// caller that has intended an effect decides that.
func (c cf) checkDrift(ctx context.Context, a *api, t tunnelRow) (ingress.Config, error) {
	cfg, err := a.Configuration(ctx, t.ID)
	if err != nil {
		return ingress.Config{}, err
	}
	if t.ConfigHash != "" && t.ConfigHash != ingress.Hash(cfg) {
		return ingress.Config{}, errcode.New(errcode.CloudConflict, fmt.Sprintf(
			"the configuration of tunnel %s changed outside Darkpanel; accept the current configuration with cloudflare.tunnel.adopt",
			t.Name))
	}
	return cfg, nil
}

// hostnameAbsent reports whether err says the object asked for does not
// exist at Cloudflare.
func hostnameAbsent(err error) bool {
	var coded *errcode.Error
	return (errors.As(err, &coded) && coded.Code == errcode.NotFound) || cloud.Status(err) == http.StatusNotFound
}

// hostnameConflict is the Definite refusal of an inverse that found the
// object it would undo changed, and changed nothing.
func hostnameConflict(format string, args ...any) error {
	return core.Definite(errcode.New(errcode.CloudConflict, fmt.Sprintf(format, args...)))
}

// hostnameRuleDoc is the state document of hostname's rule in cfg:
// {"rule": <the rule, or null>}.
func hostnameRuleDoc(cfg ingress.Config, hostname string) (json.RawMessage, error) {
	var doc struct {
		Rule *ingress.Rule `json:"rule"`
	}
	if r, ok := ingress.Lookup(cfg, hostname); ok {
		doc.Rule = &r
	}
	b, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("cloudflare: encode the rule of %s: %w", hostname, err)
	}
	return b, nil
}

// hostnameRecordDoc is the state document of a DNS record, null when r is
// nil.
func hostnameRecordDoc(r *DNSRecord) json.RawMessage {
	if r == nil {
		return json.RawMessage("null")
	}
	// Marshalling a struct of strings and a boolean cannot fail.
	b, _ := json.Marshal(struct {
		Name    string `json:"name"`
		Content string `json:"content"`
		Proxied bool   `json:"proxied"`
		Comment string `json:"comment"`
	}{r.Name, r.Content, r.Proxied, r.Comment})
	return b
}

// hostnameOfChange finds the hostname row a change added; ok is false when
// there is none.
func hostnameOfChange(ctx context.Context, q queryer, changeID string) (hostnameRow, bool, error) {
	rows, err := listHostnames(ctx, q)
	if err != nil {
		return hostnameRow{}, false, err
	}
	for _, r := range rows {
		if r.ChangeID == changeID {
			return r, true, nil
		}
	}
	return hostnameRow{}, false, nil
}

// hostnameHandler observes, undoes and recovers the effects of a hostname
// add: the ingress rule cloudflare.ingress:<tunnel id>/<hostname> and the
// DNS record cloudflare.dnsrecord:<zone id>/<record id>.
type hostnameHandler struct{ c cf }

// Observe reads the rule or the DNS record an effect names. A record not
// yet named is found by its marker comment.
func (h hostnameHandler) Observe(ctx context.Context, env core.Env, e core.Effect) (core.Observation, error) {
	if rest, ok := strings.CutPrefix(e.Resource, ingressNoun); ok {
		tunnelID, hostname, ok := strings.Cut(rest, "/")
		if !ok || tunnelID == "" || hostname == "" {
			return core.Observation{}, fmt.Errorf("cloudflare: %q is not an ingress rule effect", e.Resource)
		}
		a, _, err := h.c.connected(ctx, env.DB, env.Sealer, env.Clock)
		if err != nil {
			return core.Observation{}, err
		}
		cfg, err := a.Configuration(ctx, tunnelID)
		if err != nil && !hostnameAbsent(err) {
			return core.Observation{}, err
		}
		doc, err := hostnameRuleDoc(cfg, hostname)
		if err != nil {
			return core.Observation{}, err
		}
		return core.Observation{Current: core.StateDoc{State: doc}, Resource: e.Resource, Matches: 1, Exclusive: true}, nil
	}
	rest, ok := strings.CutPrefix(e.Resource, dnsRecordNoun)
	if !ok {
		return core.Observation{}, fmt.Errorf("cloudflare: %q is not a hostname effect", e.Resource)
	}
	if marker, ok := strings.CutPrefix(rest, "?"); ok {
		return h.observeCreation(ctx, env, e, marker)
	}
	zoneID, id, ok := strings.Cut(rest, "/")
	if !ok || zoneID == "" || id == "" {
		return core.Observation{}, fmt.Errorf("cloudflare: %q is not a DNS record effect", e.Resource)
	}
	a, _, err := h.c.connected(ctx, env.DB, env.Sealer, env.Clock)
	if err != nil {
		return core.Observation{}, err
	}
	rec, err := a.DNSRecord(ctx, zoneID, id)
	doc := hostnameRecordDoc(&rec)
	switch {
	case hostnameAbsent(err):
		doc = hostnameRecordDoc(nil)
	case err != nil:
		return core.Observation{}, err
	}
	return core.Observation{Current: core.StateDoc{State: doc}, Resource: e.Resource, Matches: 1, Exclusive: true}, nil
}

// observeCreation finds the records of a creation whose answer was lost:
// those of the change's hostname in its zone that carry the marker comment.
func (h hostnameHandler) observeCreation(ctx context.Context, env core.Env, e core.Effect, marker string) (core.Observation, error) {
	if e.Recovery != nil {
		marker = e.Recovery.Value
	}
	row, found, err := hostnameOfChange(ctx, env.DB.Read(), e.ChangeID)
	if err != nil {
		return core.Observation{}, err
	}
	if !found {
		return core.Observation{}, fmt.Errorf("cloudflare: change %s has no hostname row to find its DNS record by", e.ChangeID)
	}
	a, _, err := h.c.connected(ctx, env.DB, env.Sealer, env.Clock)
	if err != nil {
		return core.Observation{}, err
	}
	listed, err := a.DNSRecords(ctx, row.ZoneID, row.Hostname, marker)
	if err != nil {
		return core.Observation{}, err
	}
	var kept []DNSRecord
	for _, r := range listed {
		if r.Comment == marker {
			kept = append(kept, r)
		}
	}
	obs := core.Observation{Current: core.StateDoc{State: hostnameRecordDoc(nil)}, Matches: len(kept), Exclusive: true}
	switch len(kept) {
	case 0:
	case 1:
		obs.Current = core.StateDoc{State: hostnameRecordDoc(&kept[0])}
		obs.Resource = dnsRecordResource(row.ZoneID, kept[0].ID)
	default:
		for i := range kept {
			obs.Candidates = append(obs.Candidates, core.Candidate{
				Resource: dnsRecordResource(row.ZoneID, kept[i].ID), State: core.StateDoc{State: hostnameRecordDoc(&kept[i])},
			})
		}
	}
	return obs, nil
}

// Inverse removes the DNS record or the ingress rule one made effect added,
// and refuses, changing nothing, when the object is no longer what the add
// left.
func (h hostnameHandler) Inverse(ctx context.Context, env core.Env, e core.Effect) (core.InverseResult, error) {
	a, _, err := h.c.connected(ctx, env.DB, env.Sealer, env.Clock)
	if err != nil {
		// Nothing was sent to Cloudflare.
		return core.InverseResult{}, core.Definite(err)
	}
	if rest, ok := strings.CutPrefix(e.Resource, dnsRecordNoun); ok {
		zoneID, id, ok := strings.Cut(rest, "/")
		if !ok || zoneID == "" || id == "" || strings.HasPrefix(rest, "?") {
			return core.InverseResult{}, fmt.Errorf("cloudflare: %q is not a DNS record effect", e.Resource)
		}
		return core.InverseResult{}, h.removeRecord(ctx, env, a, e, zoneID, id)
	}
	rest, ok := strings.CutPrefix(e.Resource, ingressNoun)
	tunnelID, hostname, cut := strings.Cut(rest, "/")
	if !ok || !cut || tunnelID == "" || hostname == "" {
		return core.InverseResult{}, fmt.Errorf("cloudflare: %q is not a hostname effect", e.Resource)
	}
	return core.InverseResult{}, h.removeRule(ctx, env, a, e, tunnelID, hostname)
}

// removeRecord deletes the record the change created, once it is still the
// change's proxied CNAME to a tunnel, and moves the change's hostname row on
// to removing its rule. A record already gone counts as removed.
func (h hostnameHandler) removeRecord(ctx context.Context, env core.Env, a *api, e core.Effect, zoneID, id string) error {
	rec, err := a.DNSRecord(ctx, zoneID, id)
	switch {
	case hostnameAbsent(err):
	case err != nil:
		// Nothing was written yet.
		return core.Definite(err)
	case rec.Type != "CNAME" || !strings.HasSuffix(rec.Content, ".cfargotunnel.com") || rec.Comment != hostnameMarker(e.ChangeID):
		return hostnameConflict("DNS record %s (%s) is no longer the CNAME change %s created; it was left in place", rec.Name, id, e.ChangeID)
	default:
		if err := a.DeleteDNSRecord(ctx, zoneID, id); err != nil && !hostnameAbsent(err) {
			if tunnelRefused(err) {
				return core.Definite(err)
			}
			return err
		}
	}
	row, found, err := hostnameOfChange(ctx, env.DB.Read(), e.ChangeID)
	if err != nil || !found {
		return err
	}
	return env.DB.Write(ctx, func(tx *sql.Tx) error {
		return setHostnamePhase(ctx, tx, row.Hostname, "removing_rule", "", "", env.Clock.Now())
	})
}

// removeRule drops hostname's rule from the tunnel's configuration, once it
// still routes where the change's row says, then stores the hash of the
// configuration read back and deletes the row. A rule already gone counts as
// removed.
func (h hostnameHandler) removeRule(ctx context.Context, env core.Env, a *api, e core.Effect, tunnelID, hostname string) error {
	tunnel, found, err := loadTunnel(ctx, env.DB.Read(), tunnelID)
	if err != nil {
		return core.Definite(err)
	}
	// loadTunnel also matches a name; only the row with this id is the
	// effect's.
	if !found || tunnel.ID != tunnelID {
		return hostnameConflict("tunnel %s is no longer managed; the rule for %s was left in place", tunnelID, hostname)
	}
	row, rowFound, err := loadHostname(ctx, env.DB.Read(), hostname)
	if err != nil {
		return core.Definite(err)
	}
	ours := rowFound && row.ChangeID == e.ChangeID
	cfg, err := h.c.checkDrift(ctx, a, tunnel)
	if err != nil {
		// Nothing was written yet.
		return core.Definite(err)
	}
	readBack := cfg
	if rule, present := ingress.Lookup(cfg, hostname); present {
		if !ours || rule.Service != row.Service || rule.NoTLSVerify != row.NoTLSVerify {
			return hostnameConflict("the rule for %s in tunnel %s is no longer the one change %s added; it was left in place",
				hostname, tunnel.Name, e.ChangeID)
		}
		out, _ := ingress.Remove(cfg, hostname)
		if readBack, err = h.c.putConfiguration(ctx, a, tunnel, out); err != nil {
			return err
		}
	}
	return env.DB.Write(ctx, func(tx *sql.Tx) error {
		if err := setTunnelHash(ctx, tx, tunnel.ID, ingress.Hash(readBack)); err != nil {
			return err
		}
		if !ours {
			return nil
		}
		return deleteHostname(ctx, tx, hostname)
	})
}

// putConfiguration writes cfg as the tunnel's configuration and answers the
// configuration read back, which must hash as cfg does. Only a Definite
// error guarantees that nothing was written.
func (c cf) putConfiguration(ctx context.Context, a *api, t tunnelRow, cfg ingress.Config) (ingress.Config, error) {
	if err := a.PutConfiguration(ctx, t.ID, cfg); err != nil {
		return ingress.Config{}, fmt.Errorf("cloudflare: write the configuration of tunnel %s: %w", t.Name, err)
	}
	return c.readBack(ctx, a, t, cfg)
}

// readBack reads the tunnel's configuration after a write of sent and
// requires it to hash as sent does. Its errors are never Definite: the
// write before it may have committed.
func (c cf) readBack(ctx context.Context, a *api, t tunnelRow, sent ingress.Config) (ingress.Config, error) {
	readBack, err := a.Configuration(ctx, t.ID)
	if err != nil {
		return ingress.Config{}, fmt.Errorf("cloudflare: read back the configuration of tunnel %s: %w", t.Name, err)
	}
	if ingress.Hash(readBack) != ingress.Hash(sent) {
		return ingress.Config{}, fmt.Errorf("cloudflare: tunnel %s answered a configuration other than the one written", t.Name)
	}
	return readBack, nil
}

// Recover settles an add change after a crash or a live failure, from its
// hostname row and its effects alone: it completes a change whose rule and
// record were both made, rolls back a rule whose record was not made, and
// fails a change whose rule was never written. Every branch is safe to run
// twice.
func (h hostnameHandler) Recover(ctx context.Context, env core.Env) (core.RecoveryOutcome, error) {
	row, found, err := hostnameOfChange(ctx, env.DB.Read(), env.ChangeID)
	if err != nil {
		return core.RecoveryOutcome{}, err
	}
	effs, err := env.Effects.List(ctx)
	if err != nil {
		return core.RecoveryOutcome{}, err
	}
	var rule, record *core.Effect
	for i := range effs {
		e := &effs[i]
		switch {
		case strings.HasPrefix(e.Resource, ingressNoun) && rule == nil:
			rule = e
		case strings.HasPrefix(e.Resource, dnsRecordNoun) && record == nil:
			record = e
		default:
			continue
		}
		if e.State != core.EffectMade && e.State != core.EffectSkipped {
			return core.RecoveryOutcome{State: core.RecoveryNeedsRepair,
				Reason: fmt.Sprintf("effect %d on %s is %s", e.Seq, e.Resource, e.State)}, nil
		}
	}
	switch {
	case !found || rule == nil || rule.State == core.EffectSkipped:
		if found {
			if err := env.DB.Write(ctx, func(tx *sql.Tx) error { return deleteHostname(ctx, tx, row.Hostname) }); err != nil {
				return core.RecoveryOutcome{}, err
			}
		}
		return core.RecoveryOutcome{State: core.RecoveryFailed, Reason: "the ingress rule was not written"}, nil
	case record != nil && record.State == core.EffectMade:
		// Also for a row already active: the journal, not the row, names the
		// record the change created.
		rest, _ := strings.CutPrefix(record.Resource, dnsRecordNoun)
		_, recordID, ok := strings.Cut(rest, "/")
		if !ok || recordID == "" {
			return core.RecoveryOutcome{}, fmt.Errorf("cloudflare: effect %d names no DNS record: %s", record.Seq, record.Resource)
		}
		now := env.Clock.Now()
		err := env.DB.Write(ctx, func(tx *sql.Tx) error {
			if err := setHostnamePhase(ctx, tx, row.Hostname, "active", "", recordID, now); err != nil {
				return err
			}
			return setHostnameHealth(ctx, tx, row.Hostname, "ok", now)
		})
		if err != nil {
			return core.RecoveryOutcome{}, err
		}
		row.Phase, row.RecordID, row.Health, row.CheckedAt, row.UpdatedAt = "active", recordID, "ok", now, now
		return hostnameCompleted(row)
	case row.Phase == "active":
		return hostnameCompleted(row)
	default:
		if _, err := h.Inverse(ctx, env, *rule); err != nil {
			return core.RecoveryOutcome{}, err
		}
		return core.RecoveryOutcome{State: core.RecoveryRolledBack}, nil
	}
}

// hostnameCompleted is the outcome of a recovery that finds the hostname
// active, with its view as the action's output.
func hostnameCompleted(row hostnameRow) (core.RecoveryOutcome, error) {
	out, err := json.Marshal(hostnameViewOf(row))
	if err != nil {
		return core.RecoveryOutcome{}, fmt.Errorf("cloudflare: encode hostname %s: %w", row.Hostname, err)
	}
	return core.RecoveryOutcome{State: core.RecoveryCompleted, Output: out}, nil
}
