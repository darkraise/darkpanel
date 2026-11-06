package derp

import (
	"context"
	"sync"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/ext"
	"github.com/darkraise/darkpanel/internal/modules/derp/artifact"
)

const (
	serversService = ID + ".servers"
	// serversRegionUnknown is the record error of a pass that read the server but not the regions.
	serversRegionUnknown = "the tailnet policy could not be read, so the region may be out of date"
	// serversReadWait is the longest one host's status read may take in a pass.
	serversReadWait = 15 * time.Second
	// serversReadParallel is the most hosts a pass reads at once.
	serversReadParallel = 8
)

var serversPrincipal = core.Principal{Actor: "system:" + serversService, Scope: core.ScopeAdmin}

// serverList declares derp.server.list: the DERP servers the derp.servers service found, with their health.
func (d dp) serverList() core.ActionDef {
	return core.DefineRead[ServerListInput, Servers](core.ActionSpec{
		Name:    ID + ".server.list",
		Summary: "List the DERP servers the controller found, with their health.",
		Version: 1,
		Side:    core.SideController,
	}, serverListReader{})
}

// serverListReader lists the stored server records; it calls no agent and no provider.
type serverListReader struct{}

// Read answers every server record whose host still exists, by host id, with its health as of now.
func (serverListReader) Read(ctx context.Context, env core.Env, _ ServerListInput) (Servers, error) {
	records, err := loadServerRecords(ctx, env.DB.Read())
	if err != nil {
		return Servers{}, err
	}
	hosts, err := probeHosts(ctx, env)
	if err != nil {
		return Servers{}, err
	}
	names := make(map[string]string, len(hosts))
	for _, h := range hosts {
		names[h.ID] = h.Name
	}
	now := env.Clock.Now()
	views := make([]ServerView, 0, len(records))
	for _, r := range records {
		name, ok := names[r.HostID]
		if !ok {
			continue
		}
		health, reasons := serverHealth(r, now)
		v := ServerView{
			HostID: r.HostID, Host: name, Mode: r.Mode, HostName: r.HostName, RegionID: r.RegionID, RegionCode: r.RegionCode,
			DerperVersion: r.DerperVersion, CertName: r.CertName, UnitActive: r.UnitActive, Health: health, Reasons: reasons,
			CheckedAt: r.CheckedAt, Error: r.Error,
		}
		if !r.CertNotAfter.IsZero() {
			notAfter := r.CertNotAfter.UTC()
			v.CertNotAfter = &notAfter
		}
		views = append(views, v)
	}
	return Servers{Servers: views, AvailableVersion: artifact.DerperVersion}, nil
}

// serverHealth grades a server record at now: broken when derper is stopped or its certificate has expired, degraded
// for any other reason, else ok. reasons holds one sentence per condition found and is never nil.
func serverHealth(r serverRecord, now time.Time) (health string, reasons []string) {
	reasons = []string{}
	broken := false
	if !r.UnitActive {
		reasons = append(reasons, "derper is not running")
		broken = true
	}
	if !r.CertNotAfter.IsZero() {
		day := r.CertNotAfter.UTC().Format(time.DateOnly)
		switch {
		case now.After(r.CertNotAfter):
			reasons = append(reasons, "the certificate expired on "+day)
			broken = true
		case r.CertNotAfter.Sub(now) <= expiryWarning:
			reasons = append(reasons, "the certificate expires on "+day)
		}
	}
	if now.Sub(r.CheckedAt) > staleAfter {
		reasons = append(reasons, "the last check was at "+r.CheckedAt.UTC().Format(time.RFC3339))
	}
	switch r.Error {
	case "":
	case serversRegionUnknown:
		reasons = append(reasons, r.Error)
	default:
		reasons = append(reasons, "the host could not be read: "+r.Error)
	}
	switch {
	case broken:
		return "broken", reasons
	case len(reasons) > 0:
		return "degraded", reasons
	}
	return "ok", reasons
}

// Controller contributes the derp.servers service. It reads deps only from the service's Run, since the server
// calls it with whatever deps it has.
func (m module) Controller(deps ext.Deps) ([]ext.Route, []ext.Service, error) {
	return nil, []ext.Service{{
		Name: serversService,
		Run:  func(ctx context.Context) error { return m.d.runServers(ctx, deps) },
	}}, nil
}

// runServers runs a pass at start, then one every servicePeriod until ctx ends. The timer is armed again only after
// its pass, so passes never overlap. A pass's error ends the service for the supervisor to restart.
func (d dp) runServers(ctx context.Context, deps ext.Deps) error {
	pass := func() error {
		if err := d.checkServers(ctx, deps, deps.Clock.Now()); err != nil && ctx.Err() == nil {
			return err
		}
		return nil
	}
	if err := pass(); err != nil {
		return err
	}
	due := make(chan struct{}, 1)
	arm := func() core.Timer {
		return deps.Clock.AfterFunc(servicePeriod, func() {
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

// checkServers is one pass: it reads every host's DERP status and stores what it found. Every read comes before the
// first write, so a pass cancelled during its reads changes nothing. A host that cannot be read is data, not an error.
func (d dp) checkServers(ctx context.Context, deps ext.Deps, now time.Time) error {
	env := core.Env{Principal: serversPrincipal, DB: deps.DB, Sealer: deps.Sealer, Clock: deps.Clock, Log: deps.Log, Agents: deps.Hosts}
	hosts, err := probeHosts(ctx, env)
	if err != nil {
		return err
	}
	records, err := loadServerRecords(ctx, deps.DB.Read())
	if err != nil {
		return err
	}
	stored := make(map[string]serverRecord, len(records))
	for _, r := range records {
		stored[r.HostID] = r
	}

	statuses, unreadable, err := d.readServerStatuses(ctx, env, hosts)
	if err != nil {
		return err
	}
	anyInstalled := false
	for _, st := range statuses {
		anyInstalled = anyInstalled || st.Installed
	}

	var (
		regions      derpMap
		regionsKnown bool
	)
	if anyInstalled {
		regions, regionsKnown = d.serversRegions(ctx, env)
		if err := ctx.Err(); err != nil {
			return err
		}
	}

	known := make(map[string]bool, len(hosts))
	for _, h := range hosts {
		known[h.ID] = true
		rec, had := stored[h.ID]
		if msg, failed := unreadable[h.ID]; failed {
			if had {
				rec.Error = msg
				if err := upsertServerRecord(ctx, deps.DB, rec); err != nil {
					return err
				}
			}
			continue
		}
		st := statuses[h.ID]
		if !st.Installed {
			if had {
				if err := deleteServerRecord(ctx, deps.DB, h.ID); err != nil {
					return err
				}
			}
			continue
		}
		next := serverRecord{
			HostID: h.ID, Mode: st.Mode, HostName: st.HostName, RegionID: rec.RegionID, RegionCode: rec.RegionCode,
			DerperVersion: st.DerperVersion, CertName: st.CertName, UnitActive: st.UnitActive, CheckedAt: now,
		}
		if st.CertNotAfter != nil {
			next.CertNotAfter = st.CertNotAfter.UTC()
		}
		if regionsKnown {
			region, _ := regions.regionOf(st.HostName)
			next.RegionID, next.RegionCode = region.RegionID, region.RegionCode
		} else {
			next.Error = serversRegionUnknown
		}
		if err := upsertServerRecord(ctx, deps.DB, next); err != nil {
			return err
		}
	}
	for _, r := range records {
		if !known[r.HostID] {
			if err := deleteServerRecord(ctx, deps.DB, r.HostID); err != nil {
				return err
			}
		}
	}
	return nil
}

// readServerStatuses reads every host's DERP status, at most serversReadParallel at a time, and answers the statuses
// of the hosts that answered and the client-safe failure text of those that did not. Each read has its own bound, so
// one silent agent costs serversReadWait and not the pass. The bound runs on env.Clock so a pass's clock drives it.
// When ctx itself ends during the reads, its error is the answer.
func (d dp) readServerStatuses(ctx context.Context, env core.Env, hosts []probeHost) (map[string]ServerStatus, map[string]string, error) {
	type result struct {
		status ServerStatus
		err    error
	}
	results := make([]result, len(hosts))
	slots := make(chan struct{}, serversReadParallel)
	var wg sync.WaitGroup
	for i, h := range hosts {
		wg.Add(1)
		slots <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			rctx, cancel := context.WithCancel(ctx)
			defer cancel()
			timer := env.Clock.AfterFunc(serversReadWait, cancel)
			defer timer.Stop()
			st, err := readStatus(rctx, env, h.ID, nil)
			results[i] = result{status: st, err: err}
		}()
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	statuses := make(map[string]ServerStatus, len(hosts))
	unreadable := map[string]string{}
	for i, h := range hosts {
		if err := results[i].err; err != nil {
			unreadable[h.ID] = serversReadFailure(err)
			continue
		}
		statuses[h.ID] = results[i].status
	}
	return statuses, unreadable, nil
}

// serversReadFailure is the client-safe text stored for a host whose status read failed: the detail of a coded
// refusal, else a generic sentence, since an uncoded error may carry transport internals.
func serversReadFailure(err error) string {
	if coded := errcode.From(err); coded.Code != errcode.Internal && coded.Detail != "" {
		return coded.Detail
	}
	return probeAgentNoAnswer
}

// serversRegions is the derpMap a pass takes regions from: the policy's when a tailnet is connected, an empty map
// (every region 0) when none is. ok is false when the connected check, the policy read or its parse failed; the
// stored regions then stand, since a provider outage says nothing about the published regions, and each record
// carries serversRegionUnknown so its server is degraded until a pass reads them again. Each failure is
// logged with its code only: the raw text of a provider error may carry credentials.
func (d dp) serversRegions(ctx context.Context, env core.Env) (regions derpMap, ok bool) {
	connected, err := d.d.connected(ctx, env.DB)
	if err != nil {
		env.Log.Warn("derp: the tailnet connected check failed; keeping the stored regions", "code", errcode.From(err).Code)
		return derpMap{}, false
	}
	if !connected {
		return derpMap{}, true
	}
	p, err := d.d.policy(ctx, env)
	if err != nil {
		env.Log.Warn("derp: the tailnet policy read failed; keeping the stored regions", "code", errcode.From(err).Code)
		return derpMap{}, false
	}
	regions, err = parseDERPMap(p.Text)
	if err != nil {
		env.Log.Warn("derp: the tailnet policy does not parse; keeping the stored regions", "code", errcode.From(err).Code)
		return derpMap{}, false
	}
	return regions, true
}
