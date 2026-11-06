package derp

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/modules/tailnet"
	"github.com/darkraise/darkpanel/internal/modules/tailscale"
)

const (
	fbHost = "h1"           // the DERP host whose upgrade or rotation is checked; it serves region 900
	fbIP   = "203.0.113.10" // its -hostname
)

// fbRegion is one custom region besides 900 in the fixture policy, and the host that serves it.
type fbRegion struct {
	id        int
	host      string // the core_hosts id
	ip        string // the server's -hostname, an ip-mode address
	row       bool   // the host has a derp_servers row for the region
	ready     bool   // the controller reaches the server over TLS and STUN
	statusErr bool   // the host's derp.server.status read fails
}

// fbPolicy is a policy file whose derpMap holds regions, each with one node at its address.
func fbPolicy(t *testing.T, omit bool, regions []fbRegion) string {
	t.Helper()
	m := map[string]any{}
	for _, r := range regions {
		id := strconv.Itoa(r.id)
		m[id] = map[string]any{
			"RegionID": r.id, "RegionCode": "r" + id, "RegionName": "Region " + id,
			"Nodes": []any{map[string]any{"Name": id + "a", "RegionID": r.id, "HostName": r.ip, "DERPPort": 443, "STUNPort": 3478}},
		}
	}
	b, err := json.Marshal(map[string]any{"derpMap": map[string]any{"OmitDefaultRegions": omit, "Regions": m}})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// fbAgent makes host id answer derp.server.status with st (or an error) and every derp.node.probe with an OK
// TLS and STUN result, and tailscale.status as a running node.
func fbAgent(h *dpHost, id string, st ServerStatus, statusErr bool) {
	a := h.agent(id)
	if a.Reads == nil {
		a.Reads = map[string]func(json.RawMessage) (any, error){}
	}
	if a.Resources == nil {
		a.Resources = map[string]any{}
	}
	a.Reads["derp.server.status/v1"] = func(json.RawMessage) (any, error) {
		if statusErr {
			return nil, errors.New("the agent does not answer")
		}
		return st, nil
	}
	a.Reads["derp.node.probe/v1"] = func(json.RawMessage) (any, error) {
		return NodeProbe{TLS: ProbeResult{OK: true, LatencyMs: 12}, STUN: ProbeResult{OK: true, LatencyMs: 9}}, nil
	}
	a.Resources["tailscale.status"] = tailscale.Status{BackendState: "Running"}
}

func fbStatus(ip string, link bool) ServerStatus {
	return ServerStatus{
		Installed: true, Mode: "ip", HostName: ip, DerperVersion: "v1.104.1", UnitActive: true,
		LinkViaTailnet: link, OnTailnet: true, Tags: []string{"tag:derp"}, Addresses: []string{"100.64.0.10"},
	}
}

// fbSetup is a controller with the DERP host fbHost (region 900) and the other regions' hosts. noMap leaves the
// derpMap out of the policy; omit sets its OmitDefaultRegions.
func fbSetup(t *testing.T, link, noMap, omit bool, others []fbRegion) *dpHost {
	t.Helper()
	h := newDPHost(t)
	h.tailnet = true
	if h.tls == nil {
		h.tls = map[string]dpProbe{}
	}
	if h.stun == nil {
		h.stun = map[netip.AddrPort]dpProbe{}
	}
	regions := append([]fbRegion{{id: 900, host: fbHost, ip: fbIP, row: true}}, others...)
	h.policy = tailnet.Policy{Text: "{}", ETag: "e1"}
	if !noMap {
		h.policy.Text = fbPolicy(t, omit, regions)
	}
	h.addHost(fbHost, "derp-a", false)
	fbAgent(h, fbHost, fbStatus(fbIP, link), false)
	for _, r := range others {
		h.addHost(r.host, "derp-"+r.host, false)
		fbAgent(h, r.host, fbStatus(r.ip, false), r.statusErr)
		if r.ready {
			h.tls[r.ip] = dpProbe{d: 20 * time.Millisecond}
			h.stun[netip.MustParseAddrPort(r.ip+":3478")] = dpProbe{d: 15 * time.Millisecond}
		}
	}
	for _, r := range regions {
		if !r.row {
			continue
		}
		rec := serverRecord{
			HostID: r.host, Mode: "ip", HostName: r.ip, RegionID: r.id, RegionCode: "r" + strconv.Itoa(r.id),
			DerperVersion: "v1.104.1", UnitActive: true, CheckedAt: h.clock.Now(),
		}
		if err := upsertServerRecord(context.Background(), h.ctlDB, rec); err != nil {
			t.Fatal(err)
		}
	}
	return h
}

// fbRun reads derp.fallback.check through its handler.
func fbRun(t *testing.T, h *dpHost, in FallbackCheckInput) (FallbackCheck, error) {
	t.Helper()
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	_, out, err := h.dp().fallbackCheck().Handler.Reveal(context.Background(), h.ctlEnv(""), raw)
	if err != nil {
		return FallbackCheck{}, err
	}
	var got FallbackCheck
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("output %s: %v", out, err)
	}
	return got, nil
}

func fbCode(err error) errcode.Code {
	var coded *errcode.Error
	if errors.As(err, &coded) {
		return coded.Code
	}
	return ""
}

// TestFbPaths: the check answers the first path that holds, in the order link, default-regions, then the first
// other custom region (by id) whose host passes the probe, and refuses derp.no_fallback when none does.
func TestFbPaths(t *testing.T) {
	r901 := fbRegion{id: 901, host: "h2", ip: "203.0.113.20", row: true, ready: true}
	r902 := fbRegion{id: 902, host: "h3", ip: "203.0.113.30", row: true, ready: true}
	r903 := fbRegion{id: 903, host: "h4", ip: "203.0.113.40", row: true, ready: true}
	down := func(r fbRegion) fbRegion { r.ready = false; return r }
	noRow := func(r fbRegion) fbRegion { r.row = false; return r }
	failing := func(r fbRegion) fbRegion { r.statusErr = true; return r }
	outside := func(r fbRegion) fbRegion { r.id = 7; return r }
	tests := []struct {
		name         string
		link         bool
		noMap, omit  bool
		policyUnread bool // the policy must not be read at all
		others       []fbRegion
		want         string // the path; "" for the derp.no_fallback refusal
	}{
		{name: "the link does not run over the tailnet", link: false, omit: true, policyUnread: true, want: "link"},
		{name: "the policy has no derpMap", link: true, noMap: true, want: "default-regions"},
		{name: "the derpMap keeps the default regions", link: true, omit: false, want: "default-regions"},
		{name: "another custom region answers", link: true, omit: true, others: []fbRegion{r901}, want: "region:901"},
		{name: "the other region's server does not answer", link: true, omit: true, others: []fbRegion{down(r901)}},
		{name: "no other region", link: true, omit: true},
		{name: "the first answering region by id", link: true, omit: true, others: []fbRegion{r903, r902, down(r901)}, want: "region:902"},
		{name: "a region without a server row", link: true, omit: true, others: []fbRegion{noRow(r901)}},
		{name: "a region outside 900 to 999 is no candidate", link: true, omit: true, others: []fbRegion{outside(r901)}},
		{name: "a region whose host cannot be read is skipped", link: true, omit: true, others: []fbRegion{failing(r901), r902}, want: "region:902"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := fbSetup(t, tc.link, tc.noMap, tc.omit, tc.others)
			if tc.policyUnread {
				h.policyErr = errors.New("the policy must not be read")
			}
			got, err := fbRun(t, h, FallbackCheckInput{HostID: fbHost, RegionID: 900})
			if tc.want != "" {
				if err != nil || got.Path != tc.want {
					t.Fatalf("answer %+v, %v; want path %q", got, err, tc.want)
				}
				return
			}
			var coded *errcode.Error
			wantDetail := "the DERP host's link depends on region 900 and no other region is reachable; add a second region or clear omitDefaultRegions"
			if !errors.As(err, &coded) || coded.Code != errcode.DERPNoFallback || !strings.HasPrefix(coded.Detail, wantDetail) || !core.IsDefinite(err) {
				t.Fatalf("answer %+v, %v; want a definite %s refusal saying %q", got, err, errcode.DERPNoFallback, wantDetail)
			}
		})
	}
}

// TestFbReadErrors: a policy that cannot be read and a host that does not answer are errors of their own, not a
// refusal that claims no region is reachable.
func TestFbReadErrors(t *testing.T) {
	h := fbSetup(t, true, false, true, nil)
	h.policyErr = errcode.New(errcode.CloudNotConnected, "connect the tailnet account")
	if _, err := fbRun(t, h, FallbackCheckInput{HostID: fbHost, RegionID: 900}); fbCode(err) != errcode.CloudNotConnected {
		t.Errorf("unreadable policy: %v, want %s", err, errcode.CloudNotConnected)
	}
	h = fbSetup(t, true, false, true, nil)
	fbAgent(h, fbHost, fbStatus(fbIP, true), true)
	if _, err := fbRun(t, h, FallbackCheckInput{HostID: fbHost, RegionID: 900}); err == nil || fbCode(err) == errcode.DERPNoFallback {
		t.Errorf("unreadable host: %v, want an error other than %s", err, errcode.DERPNoFallback)
	}
}

// TestFbDeclaration: derp.fallback.check is an internal controller read without locks, with a schema.
func TestFbDeclaration(t *testing.T) {
	def := newDPHost(t).dp().fallbackCheck()
	s := def.Spec
	if s.Name != "derp.fallback.check" || s.Version != 1 || s.Class != core.ClassRead || s.Side != core.SideController ||
		!s.Internal || s.Reveal || len(s.LockKeys) != 0 || def.Handler.Reveal == nil || def.Handler.Read != nil {
		t.Errorf("spec %+v, handlers reveal %v read %v; want an internal controller read v1 without locks, served by Reveal only", s, def.Handler.Reveal != nil, def.Handler.Read != nil)
	}
	if _, err := schema.ActionSnapshot(def); err != nil {
		t.Errorf("schema: %v", err)
	}
}

// TestW10SpFallbackNamesWhyEachRegionWasRuledOut: the refusal says, for every other region it tried, why that
// region does not count: the failing check of its probe, or that its probe could not be read.
func TestW10SpFallbackNamesWhyEachRegionWasRuledOut(t *testing.T) {
	down := fbRegion{id: 901, host: "h2", ip: "203.0.113.20", row: true}
	failing := fbRegion{id: 902, host: "h3", ip: "203.0.113.30", row: true, statusErr: true}
	h := fbSetup(t, true, false, true, []fbRegion{down, failing})
	_, err := fbRun(t, h, FallbackCheckInput{HostID: fbHost, RegionID: 900})
	var coded *errcode.Error
	if !errors.As(err, &coded) || coded.Code != errcode.DERPNoFallback || !core.IsDefinite(err) {
		t.Fatalf("err = %v, want a definite %s refusal", err, errcode.DERPNoFallback)
	}
	const base = "the DERP host's link depends on region 900 and no other region is reachable; add a second region or clear omitDefaultRegions"
	if !strings.HasPrefix(coded.Detail, base+": ") {
		t.Errorf("detail %q, want it to start with %q", coded.Detail, base+": ")
	}
	for _, want := range []string{"region 901: controller: tls: ", "region 902: the probe could not be read"} {
		if !strings.Contains(coded.Detail, want) {
			t.Errorf("detail %q, want it to contain %q", coded.Detail, want)
		}
	}
	if strings.Contains(coded.Detail, "the agent does not answer") {
		t.Errorf("detail %q repeats the transport error of an uncoded failure", coded.Detail)
	}
}
