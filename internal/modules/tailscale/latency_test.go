package tailscale

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform/tslocal"
)

var errLTDown = errors.New("connection refused")

// ltFake answers the STUN and HTTPS probes from a script. A target's answers are consumed in order; a negative
// duration is a failed probe, and a probe beyond the script's end fails. It records the calls per target.
type ltFake struct {
	mu        sync.Mutex
	answers   map[string][]time.Duration
	calls     map[string]int
	unbounded []string
	// first, when set, runs outside the lock on the first probe of each target; its error fails that probe.
	first func(target string) error
}

func ltNew() *ltFake {
	return &ltFake{answers: map[string][]time.Duration{}, calls: map[string]int{}}
}

// ltSteady is three answers of d.
func ltSteady(d time.Duration) []time.Duration { return []time.Duration{d, d, d} }

func (f *ltFake) probe(ctx context.Context, target string) (time.Duration, error) {
	f.mu.Lock()
	n := f.calls[target]
	f.calls[target]++
	list := f.answers[target]
	if dl, ok := ctx.Deadline(); !ok || time.Until(dl) > 3*time.Second {
		f.unbounded = append(f.unbounded, target)
	}
	f.mu.Unlock()
	if n == 0 && f.first != nil {
		if err := f.first(target); err != nil {
			return 0, err
		}
	}
	if n >= len(list) || list[n] < 0 {
		return 0, errLTDown
	}
	return list[n], nil
}

func (f *ltFake) stun(ctx context.Context, server netip.AddrPort) (time.Duration, error) {
	return f.probe(ctx, server.String())
}

func (f *ltFake) https(ctx context.Context, url string) (time.Duration, error) {
	return f.probe(ctx, url)
}

func (f *ltFake) callCount(target string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[target]
}

func ltNode(host, v4 string) *tslocal.DERPNode {
	return &tslocal.DERPNode{Name: host, HostName: host, IPv4: v4}
}

func ltRegion(id int, code, name string, nodes ...*tslocal.DERPNode) *tslocal.DERPRegion {
	return &tslocal.DERPRegion{RegionID: id, RegionCode: code, RegionName: name, Nodes: nodes}
}

func ltMap(regions ...*tslocal.DERPRegion) *tslocal.DERPMap {
	m := &tslocal.DERPMap{Regions: map[int]*tslocal.DERPRegion{}}
	for _, r := range regions {
		m.Regions[r.RegionID] = r
	}
	return m
}

func ltURL(host string) string { return "https://" + host + "/derp/latency-check" }

// ltRead reads tailscale.latency on the host with the fake as both probes, and requires every probe to have been
// bounded to three seconds.
func ltRead(t *testing.T, h *tsHost, f *ltFake) (Latency, string) {
	t.Helper()
	tt := h.ts()
	tt.d.stun, tt.d.https = f.stun, f.https
	b, err := tt.latencyResource().Read(context.Background(), h.env(""))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	var got Latency
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("the answer %s does not decode as a Latency: %v", b, err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.unbounded) > 0 {
		t.Errorf("probes of %v ran without a deadline of at most 3 s", f.unbounded)
	}
	return got, string(b)
}

func TestLtResourceDefinition(t *testing.T) {
	def := newTSHost(t).ts().latencyResource()
	if def.Name != "tailscale.latency" || def.OutputType != reflect.TypeFor[Latency]() {
		t.Errorf("resource %s with output %v, want tailscale.latency with Latency", def.Name, def.OutputType)
	}
}

func TestLtMedian(t *testing.T) {
	ms := time.Millisecond
	for _, tt := range []struct {
		in   []time.Duration
		want time.Duration
	}{
		{[]time.Duration{7 * ms}, 7 * ms},
		{[]time.Duration{30 * ms, 10 * ms, 20 * ms}, 20 * ms},
		{[]time.Duration{40 * ms, 20 * ms}, 30 * ms},
		{[]time.Duration{4 * ms, 1 * ms, 3 * ms, 2 * ms}, 2500 * time.Microsecond},
	} {
		in := slices.Clone(tt.in)
		if got := latencyMedian(in); got != tt.want {
			t.Errorf("latencyMedian(%v) = %v, want %v", tt.in, got, tt.want)
		}
		if !slices.Equal(in, tt.in) {
			t.Errorf("latencyMedian reordered its argument: %v", in)
		}
	}
}

func TestLtStunServers(t *testing.T) {
	tests := []struct {
		name  string
		nodes []*tslocal.DERPNode
		want  []string
	}{
		{"default port, IPv4 of every node before IPv6", []*tslocal.DERPNode{
			{Name: "1a", HostName: "a.example.net", IPv4: "192.0.2.1", IPv6: "2001:db8::1"},
			{Name: "1b", HostName: "b.example.net", IPv4: "192.0.2.2", STUNPort: 3479},
		}, []string{"192.0.2.1:3478", "192.0.2.2:3479", "[2001:db8::1]:3478"}},
		{"a disabled port, a nil node, an invalid address and a bad port are skipped", []*tslocal.DERPNode{
			nil,
			{Name: "1a", IPv4: "192.0.2.1", STUNPort: -1},
			{Name: "1b", IPv4: "not-an-ip", IPv6: "2001:db8::2"},
			{Name: "1c", IPv4: "192.0.2.3", STUNPort: 70000},
			{Name: "1d", IPv4: "192.0.2.4", STUNPort: 0},
		}, []string{"192.0.2.4:3478", "[2001:db8::2]:3478"}},
		{"a STUN-only node counts and a repeated address is listed once", []*tslocal.DERPNode{
			{Name: "1a", IPv4: "192.0.2.1", STUNOnly: true},
			{Name: "1b", IPv4: "192.0.2.1"},
		}, []string{"192.0.2.1:3478"}},
		{"no node", nil, nil},
		{"a node with no address", []*tslocal.DERPNode{{Name: "1a", HostName: "a.example.net"}}, nil},
	}
	for _, tt := range tests {
		var got []string
		for _, ap := range latencyStunServers(&tslocal.DERPRegion{Nodes: tt.nodes}) {
			got = append(got, ap.String())
		}
		if !slices.Equal(got, tt.want) {
			t.Errorf("%s: servers %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestLtProbeURLs(t *testing.T) {
	tests := []struct {
		name  string
		nodes []*tslocal.DERPNode
		want  []string
	}{
		{"node order, the default port left out", []*tslocal.DERPNode{
			{Name: "1a", HostName: "a.example.net"}, {Name: "1b", HostName: "b.example.net", DERPPort: 443},
		}, []string{ltURL("a.example.net"), ltURL("b.example.net")}},
		{"another port is part of the host", []*tslocal.DERPNode{
			{Name: "1a", HostName: "a.example.net", DERPPort: 8443},
		}, []string{"https://a.example.net:8443/derp/latency-check"}},
		{"STUN-only nodes, disabled DERP, nil nodes and nodes without a host name are skipped", []*tslocal.DERPNode{
			nil,
			{Name: "1a", HostName: "a.example.net", STUNOnly: true},
			{Name: "1b", HostName: "b.example.net", DERPPort: -1},
			{Name: "1c"},
			{Name: "1d", HostName: "d.example.net"},
		}, []string{ltURL("d.example.net")}},
		{"no node", nil, nil},
		{"a host name that changes the meaning of the URL is skipped", []*tslocal.DERPNode{
			{Name: "1a", HostName: "a.example.net/admin"},
			{Name: "1b", HostName: "a.example.net?x=1"},
			{Name: "1c", HostName: "a.example.net#x"},
			{Name: "1d", HostName: "user@a.example.net"},
			{Name: "1e", HostName: `a.example.net\x`},
			{Name: "1f", HostName: "a.example .net"},
			{Name: "1g", HostName: "a.example.net\t"},
			{Name: "1h", HostName: "a.example.net\n"},
			{Name: "1i", HostName: "a.example\x00.net"},
			{Name: "1j", HostName: "a.example\x7f.net"},
			{Name: "1k", HostName: "ok.example.net"},
		}, []string{ltURL("ok.example.net")}},
		{"a colon outside an IPv6 literal is skipped", []*tslocal.DERPNode{
			{Name: "1a", HostName: "a.example.net:8080"},
			{Name: "1b", HostName: "10.0.0.1:80"},
			{Name: "1c", HostName: "[2001:db8::1]"},
			{Name: "1d", HostName: "2001:db8::1:zz"},
			{Name: "1e", HostName: "ok.example.net"},
		}, []string{ltURL("ok.example.net")}},
		{"IP literals: IPv6 in brackets, a port after the brackets", []*tslocal.DERPNode{
			{Name: "1a", HostName: "192.0.2.1", DERPPort: 8443},
			{Name: "1b", HostName: "2001:db8::1"},
			{Name: "1c", HostName: "2001:db8::2", DERPPort: 8443},
		}, []string{
			"https://192.0.2.1:8443/derp/latency-check",
			"https://[2001:db8::1]/derp/latency-check",
			"https://[2001:db8::2]:8443/derp/latency-check",
		}},
		{"a port above 65535 is skipped and 65535 is kept", []*tslocal.DERPNode{
			{Name: "1a", HostName: "a.example.net", DERPPort: 65536},
			{Name: "1b", HostName: "b.example.net", DERPPort: 70000},
			{Name: "1c", HostName: "c.example.net", DERPPort: 65535},
			{Name: "1d", HostName: "d.example.net", DERPPort: 443},
		}, []string{"https://c.example.net:65535/derp/latency-check", ltURL("d.example.net")}},
		{"an IPv6 literal with a zone is skipped", []*tslocal.DERPNode{
			{Name: "1a", HostName: "fe80::1%eth0"},
			{Name: "1b", HostName: "fe80::1%a/b"},
			{Name: "1c", HostName: "ok.example.net"},
		}, []string{ltURL("ok.example.net")}},
	}
	for _, tt := range tests {
		got := latencyProbeURLs(&tslocal.DERPRegion{Nodes: tt.nodes})
		if !slices.Equal(got, tt.want) {
			t.Errorf("%s: urls %v, want %v", tt.name, got, tt.want)
		}
	}
}

// TestLtMeasuresEachRegionTwice: each region has a STUN and an HTTPS measurement, each the median of three
// probes sent to one endpoint, and the read touches nothing on the host.
func TestLtMeasuresEachRegionTwice(t *testing.T) {
	h := newTSHost(t)
	h.local.DERP = ltMap(ltRegion(1, "nyc", "New York City", ltNode("derp1.example.net", "192.0.2.1")))
	f := ltNew()
	ms := time.Millisecond
	f.answers["192.0.2.1:3478"] = []time.Duration{30 * ms, 10 * ms, 20 * ms}
	f.answers[ltURL("derp1.example.net")] = []time.Duration{90 * ms, 45 * ms, 15 * ms}

	got, raw := ltRead(t, h, f)
	want := []RegionLatency{{
		RegionID: 1, Code: "nyc", Name: "New York City",
		STUN:  Measurement{Available: true, MedianMs: 20},
		HTTPS: Measurement{Available: true, MedianMs: 45},
	}}
	if !reflect.DeepEqual(got.Regions, want) {
		t.Errorf("regions\n got %+v\nwant %+v", got.Regions, want)
	}
	if n := f.callCount("192.0.2.1:3478"); n != 3 {
		t.Errorf("%d STUN probes, want 3", n)
	}
	if n := f.callCount(ltURL("derp1.example.net")); n != 3 {
		t.Errorf("%d HTTPS probes, want 3", n)
	}
	if !strings.Contains(raw, `"stun":{"available":true,"medianMs":20,"reason":""}`) {
		t.Errorf("answer %s lacks the STUN measurement in the wire shape", raw)
	}
	if len(h.ran) != 0 || len(h.files) != 0 || len(h.local.Edits) != 0 || len(h.journal.effects()) != 0 {
		t.Errorf("the read changed the host: ran %v, files %d", h.ran, len(h.files))
	}
}

// TestLtPartialAnswers: a probe that fails after the first answered is dropped and the median is of the
// answers; an endpoint whose first probe fails is given up without sending its other two probes.
func TestLtPartialAnswers(t *testing.T) {
	h := newTSHost(t)
	h.local.DERP = ltMap(ltRegion(1, "nyc", "New York City", ltNode("derp1.example.net", "192.0.2.1")))
	f := ltNew()
	ms := time.Millisecond
	f.answers["192.0.2.1:3478"] = []time.Duration{20 * ms, -1, 40 * ms}
	f.answers[ltURL("derp1.example.net")] = []time.Duration{-1, 10 * ms, 10 * ms}

	got, _ := ltRead(t, h, f)
	r := got.Regions[0]
	if want := (Measurement{Available: true, MedianMs: 30}); r.STUN != want {
		t.Errorf("STUN = %+v, want %+v", r.STUN, want)
	}
	if n := f.callCount("192.0.2.1:3478"); n != 3 {
		t.Errorf("%d STUN probes, want 3 (a later failure does not stop the endpoint)", n)
	}
	if want := (Measurement{Reason: "no HTTPS answer from 1 node(s): connection refused"}); r.HTTPS != want {
		t.Errorf("HTTPS = %+v, want %+v", r.HTTPS, want)
	}
	if n := f.callCount(ltURL("derp1.example.net")); n != 1 {
		t.Errorf("%d HTTPS probes, want 1 (the first failed)", n)
	}
}

// TestLtFallsBackToTheNextEndpoint: a region measures with the first endpoint that answers its first probe.
func TestLtFallsBackToTheNextEndpoint(t *testing.T) {
	h := newTSHost(t)
	h.local.DERP = ltMap(ltRegion(2, "fra", "Frankfurt", ltNode("a.example.net", "192.0.2.1"), ltNode("b.example.net", "192.0.2.2")))
	f := ltNew()
	f.answers["192.0.2.1:3478"] = []time.Duration{-1}
	f.answers["192.0.2.2:3478"] = ltSteady(25 * time.Millisecond)
	f.answers[ltURL("a.example.net")] = []time.Duration{-1}
	f.answers[ltURL("b.example.net")] = ltSteady(60 * time.Millisecond)

	got, _ := ltRead(t, h, f)
	r := got.Regions[0]
	if r.STUN != (Measurement{Available: true, MedianMs: 25}) || r.HTTPS != (Measurement{Available: true, MedianMs: 60}) {
		t.Errorf("measurements STUN %+v HTTPS %+v, want 25 ms and 60 ms from the second endpoints", r.STUN, r.HTTPS)
	}
	for target, want := range map[string]int{
		"192.0.2.1:3478": 1, "192.0.2.2:3478": 3, ltURL("a.example.net"): 1, ltURL("b.example.net"): 3,
	} {
		if n := f.callCount(target); n != want {
			t.Errorf("%d probes of %s, want %d", n, target, want)
		}
	}
}

// TestLtUnavailableReasons: a measurement that cannot be made says why, with a zero median.
func TestLtUnavailableReasons(t *testing.T) {
	h := newTSHost(t)
	h.local.DERP = ltMap(
		ltRegion(1, "nostun", "No STUN", &tslocal.DERPNode{Name: "1a", HostName: "a.example.net", IPv4: "192.0.2.1", STUNPort: -1}),
		ltRegion(2, "blocked", "UDP blocked", ltNode("b.example.net", "192.0.2.2"), ltNode("c.example.net", "192.0.2.3")),
		ltRegion(3, "stunonly", "STUN only", &tslocal.DERPNode{Name: "3a", HostName: "d.example.net", IPv4: "192.0.2.4", STUNOnly: true}),
		ltRegion(4, "empty", "No nodes"),
	)
	f := ltNew()
	f.answers[ltURL("a.example.net")] = ltSteady(5 * time.Millisecond)
	f.answers["192.0.2.4:3478"] = ltSteady(6 * time.Millisecond)

	got, _ := ltRead(t, h, f)
	want := []RegionLatency{
		{RegionID: 1, Code: "nostun", Name: "No STUN",
			STUN:  Measurement{Reason: "the region has no STUN endpoint"},
			HTTPS: Measurement{Available: true, MedianMs: 5}},
		{RegionID: 2, Code: "blocked", Name: "UDP blocked",
			STUN:  Measurement{Reason: "no STUN answer from 2 endpoint(s); UDP may be blocked"},
			HTTPS: Measurement{Reason: "no HTTPS answer from 2 node(s): connection refused"}},
		{RegionID: 3, Code: "stunonly", Name: "STUN only",
			STUN:  Measurement{Available: true, MedianMs: 6},
			HTTPS: Measurement{Reason: "the region has no DERP node"}},
		{RegionID: 4, Code: "empty", Name: "No nodes",
			STUN:  Measurement{Reason: "the region has no STUN endpoint"},
			HTTPS: Measurement{Reason: "the region has no DERP node"}},
	}
	if !reflect.DeepEqual(got.Regions, want) {
		t.Errorf("regions\n got %+v\nwant %+v", got.Regions, want)
	}
	if n := f.callCount("192.0.2.1:3478"); n != 0 {
		t.Errorf("%d STUN probes of a region whose STUN is disabled, want 0", n)
	}
}

// TestLtRegionsSortedAndNilsSkipped: regions come back by ID whatever the map order, and a nil entry of the map
// is not a region.
func TestLtRegionsSortedAndNilsSkipped(t *testing.T) {
	h := newTSHost(t)
	h.local.DERP = ltMap(
		ltRegion(3, "sin", "Singapore", ltNode("c.example.net", "192.0.2.3")),
		ltRegion(1, "nyc", "New York City", ltNode("a.example.net", "192.0.2.1")),
		ltRegion(2, "fra", "Frankfurt", ltNode("b.example.net", "192.0.2.2")),
	)
	h.local.DERP.Regions[9] = nil
	f := ltNew()
	for _, n := range []struct{ host, ip string }{{"a.example.net", "192.0.2.1"}, {"b.example.net", "192.0.2.2"}, {"c.example.net", "192.0.2.3"}} {
		f.answers[n.ip+":3478"] = ltSteady(time.Millisecond)
		f.answers[ltURL(n.host)] = ltSteady(2 * time.Millisecond)
	}
	got, _ := ltRead(t, h, f)
	var ids []int
	for _, r := range got.Regions {
		ids = append(ids, r.RegionID)
		if !r.STUN.Available || !r.HTTPS.Available {
			t.Errorf("region %d: %+v, want both measurements", r.RegionID, r)
		}
	}
	if !slices.Equal(ids, []int{1, 2, 3}) {
		t.Errorf("region ids %v, want [1 2 3]", ids)
	}
}

// TestLtEmptyDERPMap: a node without a network map has no regions, which is an answer, not an error.
func TestLtEmptyDERPMap(t *testing.T) {
	for name, m := range map[string]*tslocal.DERPMap{"none": nil, "empty": {}} {
		h := newTSHost(t)
		h.local.DERP = m
		got, raw := ltRead(t, h, ltNew())
		if got.Regions == nil || len(got.Regions) != 0 || raw != `{"regions":[]}` {
			t.Errorf("%s: answer %s, want {\"regions\":[]}", name, raw)
		}
	}
}

// TestLtDERPMapFailure: a tailscaled that does not answer is tailscale.unavailable with the cause kept; a read
// cut off by its own context is that context's error.
func TestLtDERPMapFailure(t *testing.T) {
	h := newTSHost(t)
	h.local.Errs = map[string]error{"DERPMap": tslocal.ErrUnavailable}
	_, err := h.ts().latencyResource().Read(context.Background(), h.env(""))
	coded := errcode.From(err)
	if err == nil || coded.Code != errcode.TailscaleUnavailable || coded.Detail != "tailscaled does not answer on its socket" || !errors.Is(err, tslocal.ErrUnavailable) {
		t.Errorf("err = %#v, want tailscale.unavailable with the reason and the cause", err)
	}

	h = newTSHost(t)
	h.local.DERP = ltMap(ltRegion(1, "nyc", "New York City", ltNode("a.example.net", "192.0.2.1")))
	ctx, cancel := context.WithCancel(context.Background())
	tt := h.ts()
	tt.d.stun = func(context.Context, netip.AddrPort) (time.Duration, error) { cancel(); return 0, errLTDown }
	tt.d.https = func(context.Context, string) (time.Duration, error) { return 0, errLTDown }
	if _, err := tt.latencyResource().Read(ctx, h.env("")); !errors.Is(err, context.Canceled) {
		t.Errorf("a read cancelled during the probes = %v, want context.Canceled", err)
	}
}

// TestLtRegionsRunTogether: three regions are probed at the same time. Each region's first STUN probe waits
// until all three have started, so regions probed one after another fail the wait.
func TestLtRegionsRunTogether(t *testing.T) {
	h := newTSHost(t)
	h.local.DERP = ltMap(
		ltRegion(1, "nyc", "New York City", ltNode("a.example.net", "192.0.2.1")),
		ltRegion(2, "fra", "Frankfurt", ltNode("b.example.net", "192.0.2.2")),
		ltRegion(3, "sin", "Singapore", ltNode("c.example.net", "192.0.2.3")),
	)
	f := ltNew()
	var arrived atomic.Int32
	ready := make(chan struct{})
	f.first = func(target string) error {
		if !strings.HasPrefix(target, "192.0.2.") {
			return nil
		}
		if arrived.Add(1) == 3 {
			close(ready)
		}
		select {
		case <-ready:
			return nil
		case <-time.After(5 * time.Second):
			return errors.New("the regions are probed one after another")
		}
	}
	for _, ip := range []string{"192.0.2.1", "192.0.2.2", "192.0.2.3"} {
		f.answers[ip+":3478"] = ltSteady(time.Millisecond)
	}
	got, _ := ltRead(t, h, f)
	for _, r := range got.Regions {
		if !r.STUN.Available {
			t.Errorf("region %d STUN = %+v, want available", r.RegionID, r.STUN)
		}
	}
}
