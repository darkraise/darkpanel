package cloudflare

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/cloudflare/ingress"
)

// exposureParent is the one composite whose steps may route a hostname into
// the panel network.
const exposureParent = "panel.exposure.add"

// hostnameAdd declares cloudflare.hostname.add, which routes a public
// hostname through a managed tunnel: an ingress rule, then a proxied CNAME.
func (c cf) hostnameAdd() core.ActionDef {
	return core.DefineMutating[HostnameAddInput, HostnameView](core.ActionSpec{
		Name: ID + ".hostname.add", Summary: "Route a public hostname to a service through a managed Cloudflare tunnel.", Version: 1,
		Class: core.ClassCompensable, Side: core.SideController, LockKeys: []string{"cloudflare"},
	}, hostnameAddHandler{hostnameHandler{c: c}})
}

// hostnameAddHandler adds the ingress rule, then the DNS record, tracking
// its progress in the hostname row's phase so recovery can finish or roll
// back a change cut off between them.
type hostnameAddHandler struct{ hostnameHandler }

// hostnameAddition is what an add found: the account's api, the tunnel row,
// the hostname's zone, the configuration read and the one to write, and the
// zone's records for the hostname.
type hostnameAddition struct {
	a         *api
	tunnel    tunnelRow
	zone      Zone
	cfg, next ingress.Config
	recordIDs []string
}

// hostnameInvalid is the Definite refusal of an input field.
func hostnameInvalid(path, detail string) error {
	return core.Definite(&errcode.Error{
		Code: errcode.InvalidInput, Detail: detail, Fields: []errcode.FieldError{{Path: path, Message: detail}},
	})
}

// hostnameProblem says what is wrong with a hostname, "" when nothing is.
func hostnameProblem(hostname string) string {
	switch {
	case hostname == "":
		return "the hostname is required"
	case len(hostname) > 253:
		return "the hostname is at most 253 characters"
	}
	labels := strings.Split(hostname, ".")
	if len(labels) < 2 {
		return "the hostname has at least two labels"
	}
	for _, l := range labels {
		if l == "" || len(l) > 63 {
			return "each label of the hostname is 1 to 63 characters"
		}
		if strings.HasPrefix(l, "-") || strings.HasSuffix(l, "-") {
			return "no label of the hostname starts or ends with a hyphen"
		}
		for _, r := range l {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
				return "the hostname holds only lower-case letters, digits, hyphens and dots"
			}
		}
	}
	return ""
}

// serviceProblem says what is wrong with a service, "" when nothing is.
func serviceProblem(service string) string {
	if code, ok := strings.CutPrefix(service, "http_status:"); ok {
		n, err := strconv.Atoi(code)
		if err != nil || len(code) != 3 || n < 100 || n > 599 {
			return "an http_status service names a status from 100 to 599"
		}
		return ""
	}
	const want = "the service is http_status:<code> or an http, https, tcp or ssh URL with a host and a port"
	u, err := url.Parse(service)
	if err != nil || u.Opaque != "" || u.Hostname() == "" {
		return want
	}
	switch u.Scheme {
	case "http", "https", "tcp", "ssh":
	default:
		return want
	}
	if port, err := strconv.Atoi(u.Port()); err != nil || port < 1 || port > 65535 {
		return "the service names a port from 1 to 65535"
	}
	if u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || strings.Contains(service, "#") {
		return "the service has no user, path, query or fragment"
	}
	return ""
}

// inspect runs the add's checks in order and answers the first refusal. It
// only reads. A refusal is Definite; an error reading the store or
// Cloudflare, and cloud.not_connected, are returned as they are.
func (h hostnameAddHandler) inspect(ctx context.Context, env core.Env, in HostnameAddInput) (hostnameAddition, error) {
	if p := hostnameProblem(in.Hostname); p != "" {
		return hostnameAddition{}, hostnameInvalid("/hostname", p)
	}
	if p := serviceProblem(in.Service); p != "" {
		return hostnameAddition{}, hostnameInvalid("/service", p)
	}
	tunnel, found, err := loadTunnel(ctx, env.DB.Read(), in.Tunnel)
	if err != nil {
		return hostnameAddition{}, err
	}
	if !found {
		return hostnameAddition{}, core.Definite(errcode.New(errcode.NotFound, "no managed tunnel "+in.Tunnel))
	}
	if reserved(in.Service) && env.Call.ParentAction != exposureParent {
		return hostnameAddition{}, core.Definite(errcode.New(errcode.CloudflareReserved, fmt.Sprintf(
			"service %s is in the panel network %s, which only %s routes to", in.Service, panelNetwork, exposureParent)))
	}
	a, _, err := h.c.connected(ctx, env.DB, env.Sealer, env.Clock)
	if err != nil {
		return hostnameAddition{}, err
	}
	if _, managed, err := loadHostname(ctx, env.DB.Read(), in.Hostname); err != nil {
		return hostnameAddition{}, err
	} else if managed {
		return hostnameAddition{}, hostnameInvalid("/hostname", "hostname "+in.Hostname+" is already managed by Darkpanel")
	}
	zones, err := a.Zones(ctx)
	if err != nil {
		return hostnameAddition{}, err
	}
	zone, ok := zoneFor(zones, in.Hostname)
	if !ok {
		return hostnameAddition{}, hostnameInvalid("/hostname", "no zone of the Cloudflare account holds hostname "+in.Hostname)
	}
	cfg, err := h.c.checkDrift(ctx, a, tunnel)
	if err != nil {
		if errcode.From(err).Code == errcode.CloudConflict {
			return hostnameAddition{}, core.Definite(err)
		}
		return hostnameAddition{}, err
	}
	if _, exists := ingress.Lookup(cfg, in.Hostname); exists {
		return hostnameAddition{}, hostnameInvalid("/hostname",
			"tunnel "+tunnel.Name+" already has an ingress rule for "+in.Hostname)
	}
	next, _, err := ingress.Add(cfg, ingress.Rule{Hostname: in.Hostname, Service: in.Service, NoTLSVerify: in.NoTLSVerify})
	if err != nil {
		return hostnameAddition{}, hostnameInvalid("/hostname", "the ingress rule for "+in.Hostname+" cannot be added: "+err.Error())
	}
	records, err := a.DNSRecords(ctx, zone.ID, in.Hostname, "")
	if err != nil {
		return hostnameAddition{}, err
	}
	ids := make([]string, 0, len(records))
	for _, r := range records {
		ids = append(ids, r.ID)
	}
	if len(ids) > 0 {
		return hostnameAddition{}, hostnameInvalid("/hostname",
			fmt.Sprintf("zone %s already has %d DNS records for %s", zone.Name, len(ids), in.Hostname))
	}
	return hostnameAddition{a: a, tunnel: tunnel, zone: zone, cfg: cfg, next: next, recordIDs: ids}, nil
}

// stateHash is the hex SHA-256 of what the add read, so an apply after
// another client changed the configuration or the records fails with
// plan.state_changed.
func (ad hostnameAddition) stateHash() string {
	parts := append([]string{ad.tunnel.ID, ingress.Hash(ad.cfg), ad.zone.ID}, ad.recordIDs...)
	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(sum[:])
}

// Plan shows the rule and the record the add writes. It only reads.
func (h hostnameAddHandler) Plan(ctx context.Context, env core.Env, in HostnameAddInput) (core.PlanOutcome, error) {
	ad, err := h.inspect(ctx, env, in)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	rule := "ingress rule: " + in.Hostname + " -> " + in.Service
	if in.NoTLSVerify {
		rule += " (noTLSVerify)"
	}
	return core.PlanOutcome{
		Diff: []core.DiffLine{
			{Op: "+", Text: rule},
			{Op: "+", Text: "dns record: CNAME " + in.Hostname + " -> " + hostnameTarget(ad.tunnel.ID) +
				" (proxied, comment darkpanel:<change id>)"},
			{Op: " ", Text: "the configuration and the DNS record are written after reading them; " +
				"a change another client makes in between is not detected"},
		},
		StateHash: ad.stateHash(),
	}, nil
}

// Apply records the hostname row, writes the rule and then the record, each
// journaled as its own effect, and marks the row active. A write that fails
// without a Definite refusal leaves its effect intended for the engine to
// settle; Cloudflare writes are never retried here.
func (h hostnameAddHandler) Apply(ctx context.Context, env core.Env, in HostnameAddInput) (HostnameView, error) {
	if env.ChangeID == "" {
		return HostnameView{}, core.Definite(errors.New("cloudflare: hostname.add needs a change id"))
	}
	ad, err := h.inspect(ctx, env, in)
	if err != nil {
		// The checks only read, so nothing was written.
		return HostnameView{}, core.Definite(err)
	}
	row := hostnameRow{
		Hostname: in.Hostname, TunnelID: ad.tunnel.ID, Service: in.Service, NoTLSVerify: in.NoTLSVerify, ZoneID: ad.zone.ID,
		Phase: "adding_rule", ChangeID: env.ChangeID, Health: "unknown", UpdatedAt: env.Clock.Now(),
	}
	if err := env.DB.Write(ctx, func(tx *sql.Tx) error { return insertHostname(ctx, tx, row) }); err != nil {
		return HostnameView{}, err
	}

	before, err := hostnameRuleDoc(ad.cfg, in.Hostname)
	if err != nil {
		return HostnameView{}, err
	}
	after, err := hostnameRuleDoc(ad.next, in.Hostname)
	if err != nil {
		return HostnameView{}, err
	}
	ruleEff := cloudEffect(core.EffectModify, ingressResource(ad.tunnel.ID, in.Hostname), before)
	ruleEff.After = &core.StateDoc{State: after}
	seq, err := env.Effects.Intend(ctx, ruleEff, nil)
	if err != nil {
		return HostnameView{}, err
	}
	if err := ad.a.PutConfiguration(ctx, ad.tunnel.ID, ad.next); err != nil {
		return HostnameView{}, hostnameWriteFailed(ctx, env, seq, ruleEff.Resource,
			fmt.Errorf("cloudflare: write the configuration of tunnel %s: %w", ad.tunnel.Name, err))
	}
	readBack, err := h.c.readBack(ctx, ad.a, ad.tunnel, ad.next)
	if err != nil {
		return HostnameView{}, err
	}
	post, err := hostnameRuleDoc(readBack, in.Hostname)
	if err != nil {
		return HostnameView{}, err
	}
	if err := env.Effects.Made(ctx, seq, ruleEff.Resource, core.StateDoc{State: post}); err != nil {
		return HostnameView{}, err
	}
	err = env.DB.Write(ctx, func(tx *sql.Tx) error {
		if err := setTunnelHash(ctx, tx, ad.tunnel.ID, ingress.Hash(readBack)); err != nil {
			return err
		}
		return setHostnamePhase(ctx, tx, in.Hostname, "adding_record", "", "", env.Clock.Now())
	})
	if err != nil {
		return HostnameView{}, err
	}

	marker := hostnameMarker(env.ChangeID)
	want := DNSRecord{Type: "CNAME", Name: in.Hostname, Content: hostnameTarget(ad.tunnel.ID), Proxied: true, Comment: marker}
	recEff := cloudEffect(core.EffectCreate, dnsRecordNoun+"?"+marker, nil)
	recEff.Recovery = &core.RecoveryID{Kind: "marker", Value: marker}
	recEff.After = &core.StateDoc{State: hostnameRecordDoc(&want)}
	if seq, err = env.Effects.Intend(ctx, recEff, nil); err != nil {
		return HostnameView{}, err
	}
	rec, err := ad.a.CreateDNSRecord(ctx, ad.zone.ID, want)
	if err != nil {
		return HostnameView{}, hostnameWriteFailed(ctx, env, seq, recEff.Resource,
			fmt.Errorf("cloudflare: create the DNS record of %s: %w", in.Hostname, err))
	}
	if rec.ID == "" {
		// The effect stays intended: the record is found by its marker.
		return HostnameView{}, fmt.Errorf("cloudflare: the DNS record of %s was answered without an id", in.Hostname)
	}
	if err := env.Effects.Made(ctx, seq, dnsRecordResource(ad.zone.ID, rec.ID), core.StateDoc{State: hostnameRecordDoc(&rec)}); err != nil {
		return HostnameView{}, err
	}

	now := env.Clock.Now()
	err = env.DB.Write(ctx, func(tx *sql.Tx) error {
		if err := setHostnamePhase(ctx, tx, in.Hostname, "active", "", rec.ID, now); err != nil {
			return err
		}
		return setHostnameHealth(ctx, tx, in.Hostname, "ok", now)
	})
	if err != nil {
		return HostnameView{}, err
	}
	row.Phase, row.RecordID, row.Health, row.CheckedAt, row.UpdatedAt = "active", rec.ID, "ok", now, now
	return hostnameViewOf(row), nil
}

// hostnameWriteFailed journals a failed provider write: skipped when the
// refusal was Definite, otherwise left intended for the engine to settle.
func hostnameWriteFailed(ctx context.Context, env core.Env, seq int, resource string, err error) error {
	if !core.IsDefinite(err) {
		return err
	}
	if serr := env.Effects.Skipped(ctx, seq); serr != nil {
		return errors.Join(err, fmt.Errorf("cloudflare: journal %s: %w", resource, serr))
	}
	return err
}

// Verify passes when the hostname row is active for this change, the
// configuration holds the requested rule and the recorded DNS record is
// still the change's proxied CNAME. It only reads and needs no output.
func (h hostnameAddHandler) Verify(ctx context.Context, env core.Env, in HostnameAddInput, _ HostnameView) error {
	row, found, err := loadHostname(ctx, env.DB.Read(), in.Hostname)
	if err != nil {
		return err
	}
	if !found || row.Phase != "active" || row.ChangeID != env.ChangeID || row.RecordID == "" {
		return fmt.Errorf("cloudflare: hostname %s is not active for change %s", in.Hostname, env.ChangeID)
	}
	a, _, err := h.c.connected(ctx, env.DB, env.Sealer, env.Clock)
	if err != nil {
		return err
	}
	cfg, err := a.Configuration(ctx, row.TunnelID)
	if err != nil {
		return err
	}
	if rule, ok := ingress.Lookup(cfg, in.Hostname); !ok || rule.Service != in.Service || rule.NoTLSVerify != in.NoTLSVerify {
		return fmt.Errorf("cloudflare: tunnel %s has no ingress rule routing %s to %s", row.TunnelID, in.Hostname, in.Service)
	}
	rec, err := a.DNSRecord(ctx, row.ZoneID, row.RecordID)
	switch {
	case hostnameAbsent(err):
		return fmt.Errorf("cloudflare: DNS record %s of %s is missing", row.RecordID, in.Hostname)
	case err != nil:
		return err
	}
	if rec.Type != "CNAME" || rec.Content != hostnameTarget(row.TunnelID) || !rec.Proxied || rec.Comment != hostnameMarker(env.ChangeID) {
		return fmt.Errorf("cloudflare: DNS record %s of %s is not the proxied CNAME to tunnel %s commented %s",
			row.RecordID, in.Hostname, row.TunnelID, hostnameMarker(env.ChangeID))
	}
	return nil
}
