package tailscale

import (
	"cmp"
	"context"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform/tslocal"
)

const (
	// latencyProbes is how many probes make one measurement.
	latencyProbes = 3
	// latencyProbeTimeout bounds each probe.
	latencyProbeTimeout = 3 * time.Second
	// latencyConcurrency is how many measurements run at once.
	latencyConcurrency = 8
	// latencyCheckPath is the DERP server's latency check path.
	latencyCheckPath = "/derp/latency-check"
	// latencyDefaultSTUNPort is the STUN port of a node whose STUNPort is 0.
	latencyDefaultSTUNPort = 3478
	// latencyDefaultDERPPort is the HTTPS port of a node whose DERPPort is 0.
	latencyDefaultDERPPort = 443
)

// latencyResource is tailscale.latency: the node's STUN and DERP HTTPS latency to each region of its DERP map.
func (t ts) latencyResource() core.ResourceDef {
	return core.DefineResource(ID+".latency", t.latencyRead)
}

// latencyRead measures every region of the node's DERP map. The regions are measured together and each region's
// two measurements are independent, so one unreachable region does not fail the read.
func (t ts) latencyRead(ctx context.Context, _ core.Env) (Latency, error) {
	m, err := t.d.local().DERPMap(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return Latency{}, ctx.Err()
		}
		return Latency{}, errcode.Wrap(errcode.TailscaleUnavailable, err, "tailscaled does not answer on its socket")
	}
	var regions []*tslocal.DERPRegion
	if m != nil {
		for _, r := range m.Regions {
			if r != nil {
				regions = append(regions, r)
			}
		}
	}
	slices.SortFunc(regions, func(a, b *tslocal.DERPRegion) int { return cmp.Compare(a.RegionID, b.RegionID) })

	out := make([]RegionLatency, len(regions))
	sem := make(chan struct{}, latencyConcurrency)
	var wg sync.WaitGroup
	run := func(job func()) {
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer func() { <-sem; wg.Done() }()
			job()
		}()
	}
	for i, r := range regions {
		out[i] = RegionLatency{RegionID: r.RegionID, Code: r.RegionCode, Name: r.RegionName}
		run(func() { out[i].STUN = t.latencyStun(ctx, r) })
		run(func() { out[i].HTTPS = t.latencyHTTPS(ctx, r) })
	}
	wg.Wait()
	if ctx.Err() != nil {
		return Latency{}, ctx.Err()
	}
	return Latency{Regions: out}, nil
}

// latencyStun measures r's STUN latency with the first of its endpoints that answers.
func (t ts) latencyStun(ctx context.Context, r *tslocal.DERPRegion) Measurement {
	servers := latencyStunServers(r)
	if len(servers) == 0 {
		return Measurement{Reason: "the region has no STUN endpoint"}
	}
	median, ok, _ := latencyMeasure(ctx, servers, t.d.stun)
	if !ok {
		return Measurement{Reason: fmt.Sprintf("no STUN answer from %d endpoint(s); UDP may be blocked", len(servers))}
	}
	return latencyAvailable(median)
}

// latencyHTTPS measures r's DERP HTTPS latency with the first of its nodes that answers.
func (t ts) latencyHTTPS(ctx context.Context, r *tslocal.DERPRegion) Measurement {
	urls := latencyProbeURLs(r)
	if len(urls) == 0 {
		return Measurement{Reason: "the region has no DERP node"}
	}
	median, ok, lastErr := latencyMeasure(ctx, urls, t.d.https)
	if !ok {
		return Measurement{Reason: fmt.Sprintf("no HTTPS answer from %d node(s): %v", len(urls), lastErr)}
	}
	return latencyAvailable(median)
}

// latencyAvailable is the measurement of a median.
func latencyAvailable(median time.Duration) Measurement {
	return Measurement{Available: true, MedianMs: float64(median) / float64(time.Millisecond)}
}

// latencyStunServers is r's STUN endpoints: the IPv4 endpoints of all its nodes in node order, then the IPv6
// endpoints, each once. A node whose STUN port is disabled or invalid has none.
func latencyStunServers(r *tslocal.DERPRegion) []netip.AddrPort {
	var v4, v6 []netip.AddrPort
	seen := map[netip.AddrPort]bool{}
	add := func(list *[]netip.AddrPort, field string, port uint16, wantV4 bool) {
		a, err := netip.ParseAddr(field)
		if err != nil || a.Is4() != wantV4 {
			return
		}
		ap := netip.AddrPortFrom(a, port)
		if !seen[ap] {
			seen[ap] = true
			*list = append(*list, ap)
		}
	}
	for _, n := range r.Nodes {
		if n == nil {
			continue
		}
		port := n.STUNPort
		if port == 0 {
			port = latencyDefaultSTUNPort
		}
		if port < 1 || port > 65535 {
			continue
		}
		add(&v4, n.IPv4, uint16(port), true)
		add(&v6, n.IPv6, uint16(port), false)
	}
	return append(v4, v6...)
}

// latencyProbeURLs is the DERP latency check URL of each of r's DERP nodes, in node order.
func latencyProbeURLs(r *tslocal.DERPRegion) []string {
	var urls []string
	for _, n := range r.Nodes {
		if n == nil || n.STUNOnly || n.DERPPort < 0 || n.HostName == "" {
			continue
		}
		host := n.HostName
		if a, err := netip.ParseAddr(host); err == nil && a.Is6() {
			host = "[" + host + "]"
		}
		if n.DERPPort != 0 && n.DERPPort != latencyDefaultDERPPort {
			host += ":" + strconv.Itoa(n.DERPPort)
		}
		urls = append(urls, "https://"+host+latencyCheckPath)
	}
	return urls
}

// latencyMedian is the median of d, which must not be empty: the middle value, or the mean of the two middle
// values for an even count. It leaves d in its order.
func latencyMedian(d []time.Duration) time.Duration {
	s := slices.Clone(d)
	slices.Sort(s)
	if n := len(s); n%2 == 0 {
		return (s[n/2-1] + s[n/2]) / 2
	}
	return s[len(s)/2]
}

// latencyMeasure measures with the first of targets whose first probe answers, and returns the median of that
// target's answers. It returns answered false, with the last probe error, when no target answers or ctx ends first.
func latencyMeasure[T any](ctx context.Context, targets []T, probe func(context.Context, T) (time.Duration, error)) (median time.Duration, answered bool, lastErr error) {
	one := func(target T) (time.Duration, error) {
		ctx, cancel := context.WithTimeout(ctx, latencyProbeTimeout)
		defer cancel()
		return probe(ctx, target)
	}
	for _, target := range targets {
		if err := ctx.Err(); err != nil {
			return 0, false, err
		}
		first, err := one(target)
		// A target whose first probe fails is given up at once: a dead endpoint must not cost three timeouts.
		if err != nil {
			lastErr = err
			continue
		}
		answers := []time.Duration{first}
		for range latencyProbes - 1 {
			if ctx.Err() != nil {
				break
			}
			if d, err := one(target); err == nil {
				answers = append(answers, d)
			}
		}
		return latencyMedian(answers), true, nil
	}
	if lastErr == nil {
		lastErr = ctx.Err()
	}
	return 0, false, lastErr
}
