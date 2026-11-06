package derp

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// fallbackCheck declares derp.fallback.check: the controller's own check that a DERP host keeps a management path
// that does not use its region.
func (d dp) fallbackCheck() core.ActionDef {
	return core.DefineInternalRead[FallbackCheckInput, FallbackCheck](core.ActionSpec{
		Name:    ID + ".fallback.check",
		Summary: "Name a management path to a DERP host that does not use its region, for the controller's own use.",
		Version: 1,
		Side:    core.SideController,
	}, d.fallbackRead)
}

// fallbackRead answers the first path that holds: the host's link does not run over the tailnet, the tailnet keeps
// Tailscale's default regions, or another custom region's server is reachable. None holds: derp.no_fallback.
func (d dp) fallbackRead(ctx context.Context, env core.Env, in FallbackCheckInput) (core.Revealed[FallbackCheck], error) {
	answer := func(path string) (core.Revealed[FallbackCheck], error) {
		return core.Revealed[FallbackCheck]{Identity: "DERP fallback check for host " + in.HostID, Value: FallbackCheck{Path: path}}, nil
	}
	status, err := readStatus(ctx, env, in.HostID, nil)
	if err != nil {
		return core.Revealed[FallbackCheck]{}, err
	}
	if !status.LinkViaTailnet {
		return answer("link")
	}
	pol, err := d.d.policy(ctx, env)
	if err != nil {
		return core.Revealed[FallbackCheck]{}, err
	}
	m, err := parseDERPMap(pol.Text)
	if err != nil {
		return core.Revealed[FallbackCheck]{}, fmt.Errorf("derp: read the policy's derpMap: %w", err)
	}
	if !m.Present || !m.OmitDefaultRegions {
		return answer("default-regions")
	}
	records, err := loadServerRecords(ctx, env.DB.Read())
	if err != nil {
		return core.Revealed[FallbackCheck]{}, err
	}
	var reasons []string
	for _, id := range m.customRegions() {
		if id == in.RegionID {
			continue
		}
		host, ok := fallbackHost(records, id, in.HostID)
		if !ok {
			continue
		}
		p, err := d.probeServer(ctx, env, ServerProbeInput{HostID: host})
		if err != nil {
			// One unreachable or unreadable host only rules out its own region.
			if ctx.Err() != nil {
				return core.Revealed[FallbackCheck]{}, ctx.Err()
			}
			reasons = append(reasons, "region "+strconv.Itoa(id)+": "+fallbackWhy(err))
			continue
		}
		ready, reason, _ := probeReady(p)
		if ready {
			return answer("region:" + strconv.Itoa(id))
		}
		reasons = append(reasons, "region "+strconv.Itoa(id)+": "+reason)
	}
	detail := fmt.Sprintf(
		"the DERP host's link depends on region %d and no other region is reachable; add a second region or clear omitDefaultRegions",
		in.RegionID)
	if len(reasons) > 0 {
		detail += ": " + strings.Join(reasons, "; ")
	}
	return core.Revealed[FallbackCheck]{}, core.Definite(errcode.New(errcode.DERPNoFallback, detail))
}

// fallbackWhy is the client-safe reason a probe error rules out its region: the detail of a coded refusal, else a
// generic sentence, since an uncoded error may carry transport internals.
func fallbackWhy(err error) string {
	if coded := errcode.From(err); coded.Code != errcode.Internal && coded.Detail != "" {
		return coded.Detail
	}
	return "the probe could not be read"
}

// fallbackHost returns the host of the first record serving region id other than the checked host.
func fallbackHost(records []serverRecord, id int, exclude string) (string, bool) {
	for _, r := range records {
		if r.RegionID == id && r.HostID != exclude {
			return r.HostID, true
		}
	}
	return "", false
}
