package derp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/tailnet"
	"github.com/darkraise/darkpanel/internal/modules/tailscale"
)

const pbIP = "203.0.113.10"

var pbPin = "sha256-raw:" + strings.Repeat("ab", 32)

// pbStatus is derp.server.status of an installed ip-mode server.
func pbStatus() ServerStatus {
	return ServerStatus{
		Installed: true, Mode: "ip", HostName: pbIP, VerifyClients: true, DerperVersion: "v1.104.1", UnitActive: true,
		CertName: pbPin, AvailableVersion: "v1.104.1", OnTailnet: true, Tags: []string{"tag:derp"}, Addresses: []string{"100.64.0.10"},
	}
}

// pbAgent is the fake agent of host id with its answer maps ready to fill.
func pbAgent(h *dpHost, id string) *dpAgent {
	a := h.agent(id)
	if a.Reads == nil {
		a.Reads = map[string]func(json.RawMessage) (any, error){}
	}
	if a.Resources == nil {
		a.Resources = map[string]any{}
	}
	return a
}

// pbServe makes host id answer derp.server.status with s, marking each address of the request's whois list that
// visible names (address to node name) as visible.
func pbServe(h *dpHost, id string, s ServerStatus, visible map[string]string) {
	pbAgent(h, id).Reads["derp.server.status/v1"] = func(raw json.RawMessage) (any, error) {
		var in ServerStatusInput
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &in); err != nil {
				return nil, err
			}
		}
		out := s
		out.Visible = nil
		for _, a := range in.Whois {
			node, ok := visible[a]
			out.Visible = append(out.Visible, Visibility{Address: a, Visible: ok, Node: node})
		}
		return out, nil
	}
}

// pbNodeProbe makes host id answer derp.node.probe with both probes OK.
func pbNodeProbe(h *dpHost, id string, tlsMs, stunMs float64) {
	pbAgent(h, id).Reads["derp.node.probe/v1"] = func(json.RawMessage) (any, error) {
		return NodeProbe{
			TLS: ProbeResult{OK: true, LatencyMs: tlsMs}, STUN: ProbeResult{OK: true, LatencyMs: stunMs},
			Region: &RegionDiagnostics{Info: []string{"region reachable from " + id}, Warnings: []string{}, Errors: []string{}},
		}, nil
	}
}

// pbFailProbe makes host id answer derp.node.probe with err.
func pbFailProbe(h *dpHost, id string, err error) {
	pbAgent(h, id).Reads["derp.node.probe/v1"] = func(json.RawMessage) (any, error) { return nil, err }
}

// pbCalls decodes, in order, the inputs of the reads named read ("derp.node.probe/v1") that agent a received.
func pbCalls[T any](t *testing.T, a *dpAgent, read string) []T {
	t.Helper()
	var out []T
	for _, c := range a.Calls {
		body, ok := strings.CutPrefix(c, read+" ")
		if !ok {
			continue
		}
		var v T
		if err := json.Unmarshal([]byte(body), &v); err != nil {
			t.Fatalf("call %q: %v", c, err)
		}
		out = append(out, v)
	}
	return out
}

// pbWorld is a controller with the DERP host h1 (alpha, a row in region 900), the probe hosts h2 (bravo) and h3
// (charlie), a connected tailnet without devices, and every probe answering OK.
func pbWorld(t *testing.T) *dpHost {
	t.Helper()
	h := newDPHost(t)
	h.addHost("h1", "alpha", false)
	h.addHost("h2", "bravo", false)
	h.addHost("h3", "charlie", false)
	pbServe(h, "h1", pbStatus(), nil)
	pbNodeProbe(h, "h2", 20, 30)
	pbNodeProbe(h, "h3", 25, 35)
	h.tls = map[string]dpProbe{pbIP: {d: 10 * time.Millisecond}}
	h.stun = map[netip.AddrPort]dpProbe{netip.MustParseAddrPort(pbIP + ":3478"): {d: 5 * time.Millisecond}}
	h.tailnet = true
	err := upsertServerRecord(context.Background(), h.ctlDB, serverRecord{
		HostID: "h1", Mode: "ip", HostName: pbIP, RegionID: 900, RegionCode: "eu-derp", DerperVersion: "v1.104.1",
		CertName: pbPin, UnitActive: true, CheckedAt: h.clock.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func pbRun(h *dpHost, in ServerProbeInput) (ServerProbe, error) {
	return h.dp().probeServer(context.Background(), h.ctlEnv(""), in)
}

// pbNames lists the sources as "<source>=<name>".
func pbNames(p ServerProbe) []string {
	var out []string
	for _, s := range p.Sources {
		out = append(out, s.Source+"="+s.Name)
	}
	return out
}

func pbNear(got, want float64) bool { return math.Abs(got-want) < 0.001 }

// pbNodeWorld gives h1 and h2 and h3 a tailscale.status and h1 a whois view in which only two addresses are visible.
func pbNodeWorld(h *dpHost) {
	pbServe(h, "h1", pbStatus(), map[string]string{"100.64.0.10": "alpha.example.ts.net", "100.64.0.20": "bravo.example.ts.net"})
	pbAgent(h, "h1").Resources["tailscale.status"] = tailscale.Status{
		BackendState: "Running", PreferredDERP: "eu-derp",
		Self: &tailscale.Node{Addresses: []string{"100.64.0.10", "fd7a:115c:a1e0::10"}},
	}
	pbAgent(h, "h2").Resources["tailscale.status"] = tailscale.Status{
		BackendState: "Running", PreferredDERP: "nyc", Self: &tailscale.Node{Addresses: []string{"100.64.0.20"}},
		Peers: []tailscale.Peer{{Connection: "direct"}, {Connection: "derp", Region: "eu-derp"}},
	}
	pbAgent(h, "h3").Resources["tailscale.status"] = tailscale.Status{
		BackendState: "Running", PreferredDERP: "nyc", Self: &tailscale.Node{Addresses: []string{"100.64.0.30"}},
		Peers: []tailscale.Peer{{Connection: "derp", Region: "nyc"}},
	}
}

func TestPbDeclaration(t *testing.T) {
	def := newDPHost(t).dp().serverProbe()
	s := def.Spec
	if s.Name != "derp.server.probe" || s.Version != 1 || s.Class != core.ClassRead || s.Side != core.SideController ||
		s.Internal || s.Reveal || s.MinScope != "" || len(s.LockKeys) != 0 || len(s.Guards) != 0 || s.Available != nil || s.Summary == "" {
		t.Errorf("spec %+v, want the controller read derp.server.probe v1 with no lock, guard, minimum scope or availability check", s)
	}
	if def.InputType != reflect.TypeFor[ServerProbeInput]() || def.OutputType != reflect.TypeFor[ServerProbe]() {
		t.Errorf("types %v and %v, want ServerProbeInput and ServerProbe", def.InputType, def.OutputType)
	}
	if def.Handler.Read == nil || def.Handler.Reveal != nil || def.Handler.Plan != nil || def.Handler.Apply != nil {
		t.Errorf("handler %+v, want the read handler only", def.Handler)
	}
}

// TestPbProbeReportsEverySource: the controller and every other host are probed in that order; an agent that
// cannot be read becomes a failed source carrying its client-safe detail; the region comes from the stored row
// and the DERP host's probe input carries the server's name, pin and region.
func TestPbProbeReportsEverySource(t *testing.T) {
	h := pbWorld(t)
	const failed = "tailscaled does not answer on its socket"
	pbFailProbe(h, "h3", errcode.New(errcode.TailscaleUnavailable, failed))
	got, err := pbRun(h, ServerProbeInput{HostID: "h1"})
	if err != nil {
		t.Fatal(err)
	}
	if got.HostID != "h1" || got.RegionID != 900 || got.RegionCode != "eu-derp" || !got.Server.Installed || got.Server.HostName != pbIP || got.Server.CertName != pbPin {
		t.Errorf("probe %+v, want host h1, region 900 eu-derp and the server's status", got)
	}
	if want := []string{"controller=controller", "h2=bravo", "h3=charlie"}; !slices.Equal(pbNames(got), want) {
		t.Fatalf("sources %v, want %v", pbNames(got), want)
	}
	ctl, bravo, charlie := got.Sources[0], got.Sources[1], got.Sources[2]
	if !ctl.TLS.OK || !pbNear(ctl.TLS.LatencyMs, 10) || !ctl.STUN.OK || !pbNear(ctl.STUN.LatencyMs, 5) || ctl.Error != "" || ctl.Region != nil {
		t.Errorf("controller source %+v, want TLS 10 ms and STUN 5 ms, no error and no region diagnostics", ctl)
	}
	if !bravo.TLS.OK || !pbNear(bravo.TLS.LatencyMs, 20) || !bravo.STUN.OK || !pbNear(bravo.STUN.LatencyMs, 30) || bravo.Error != "" ||
		bravo.Region == nil || !slices.Equal(bravo.Region.Info, []string{"region reachable from h2"}) {
		t.Errorf("bravo source %+v, want TLS 20 ms, STUN 30 ms and its region diagnostics", bravo)
	}
	if charlie.Error != failed || charlie.TLS.OK || charlie.TLS.Error != failed || charlie.STUN.OK || charlie.STUN.Error != failed {
		t.Errorf("charlie source %+v, want the failure %q on the source, its TLS and its STUN", charlie, failed)
	}
	if !got.Ready || got.Warnings == nil || len(got.Warnings) != 0 || got.Nodes == nil {
		t.Errorf("ready %v, warnings %#v, nodes %#v; want ready, empty warnings and an empty node list, none null", got.Ready, got.Warnings, got.Nodes)
	}
	in := pbCalls[NodeProbeInput](t, h.agent("h2"), "derp.node.probe/v1")
	if want := (NodeProbeInput{HostName: pbIP, CertName: pbPin, RegionID: 900}); len(in) != 1 || in[0] != want {
		t.Errorf("bravo was asked %+v, want exactly %+v", in, want)
	}
}

// TestPbProbeAgentsInInputOrder: a given agent list is probed in its order, the DERP host included, no other host
// is asked, and a plain failure is reported as "the agent did not answer".
func TestPbProbeAgentsInInputOrder(t *testing.T) {
	h := pbWorld(t)
	h.addHost("h4", "delta", false)
	pbNodeProbe(h, "h1", 1, 1)
	pbNodeProbe(h, "h4", 1, 1)
	pbFailProbe(h, "h2", errors.New("connection refused"))
	got, err := pbRun(h, ServerProbeInput{HostID: "h1", Agents: []string{"h3", "h1", "h2"}})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"controller=controller", "h3=charlie", "h1=alpha", "h2=bravo"}; !slices.Equal(pbNames(got), want) {
		t.Fatalf("sources %v, want %v", pbNames(got), want)
	}
	if e := got.Sources[3].Error; e != "the agent did not answer" || got.Sources[3].TLS.Error != e || got.Sources[3].STUN.Error != e {
		t.Errorf("bravo source %+v, want the plain failure reported as %q", got.Sources[3], "the agent did not answer")
	}
	if !got.Ready {
		t.Error("not ready, want ready: charlie, an agent other than the DERP host, passes")
	}
	if n := len(pbCalls[NodeProbeInput](t, h.agent("h4"), "derp.node.probe/v1")); n != 0 {
		t.Errorf("delta, not in the list, was probed %d times", n)
	}
}

func TestPbProbeRefusals(t *testing.T) {
	nine := []string{"h2", "h3", "h4", "h5", "h6", "h7", "h8", "h9", "h10"}
	tests := []struct {
		name   string
		setup  func(h *dpHost)
		in     ServerProbeInput
		code   errcode.Code
		detail string
		field  string
	}{
		{"a host without a server", func(h *dpHost) { pbServe(h, "h1", ServerStatus{}, nil) }, ServerProbeInput{HostID: "h1"},
			errcode.DERPNotReady, "no DERP server is installed on alpha", ""},
		{"an unknown host", nil, ServerProbeInput{HostID: "h9"}, errcode.NotFound, "", ""},
		{"an unknown agent", nil, ServerProbeInput{HostID: "h1", Agents: []string{"h2", "h9"}}, errcode.InvalidInput, "", "/agents/1"},
		{"more agents than the module probes", nil, ServerProbeInput{HostID: "h1", Agents: nine}, errcode.InvalidInput, "", "/agents"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := pbWorld(t)
			if tt.setup != nil {
				tt.setup(h)
			}
			_, err := pbRun(h, tt.in)
			coded := errcode.From(err)
			if err == nil || coded.Code != tt.code || !core.IsDefinite(err) {
				t.Fatalf("err = %#v, want a definite %s refusal", err, tt.code)
			}
			if tt.detail != "" && coded.Detail != tt.detail {
				t.Errorf("detail %q, want %q", coded.Detail, tt.detail)
			}
			if tt.field != "" && (len(coded.Fields) != 1 || coded.Fields[0].Path != tt.field) {
				t.Errorf("fields %+v, want one at %s", coded.Fields, tt.field)
			}
		})
	}
}

// TestPbProbeNodes: with a connected tailnet the nodes are its devices (first address each, one without an
// address skipped); the whois request carries exactly those addresses; a node that is a Darkpanel host takes its
// host id and its preferred region from that host's tailscale.status; observed use is a peer relayed through
// this server's region code and nothing else.
func TestPbProbeNodes(t *testing.T) {
	h := pbWorld(t)
	h.devices = []tailnet.Device{
		{ID: "d1", Name: "alpha-node", Addresses: []string{"100.64.0.10", "fd7a:115c:a1e0::10"}},
		{ID: "d2", Name: "bravo-node", Addresses: []string{"100.64.0.20"}},
		{ID: "d3", Name: "charlie-node", Addresses: []string{"100.64.0.30"}},
		{ID: "d4", Name: "phone", Addresses: []string{"100.64.0.40"}},
		{ID: "d5", Name: "no-address"},
	}
	pbNodeWorld(h)
	got, err := pbRun(h, ServerProbeInput{HostID: "h1"})
	if err != nil {
		t.Fatal(err)
	}
	want := []NodeUse{
		{Name: "alpha-node", Address: "100.64.0.10", HostID: "h1", Visible: true, Preferred: "eu-derp"},
		{Name: "bravo-node", Address: "100.64.0.20", HostID: "h2", Visible: true, Preferred: "nyc", Observed: true},
		{Name: "charlie-node", Address: "100.64.0.30", HostID: "h3", Preferred: "nyc"},
		{Name: "phone", Address: "100.64.0.40"},
	}
	if !reflect.DeepEqual(got.Nodes, want) {
		t.Errorf("nodes %+v, want %+v", got.Nodes, want)
	}
	calls := pbCalls[ServerStatusInput](t, h.agent("h1"), "derp.server.status/v1")
	if len(calls) != 1 || !slices.Equal(calls[0].Whois, []string{"100.64.0.10", "100.64.0.20", "100.64.0.30", "100.64.0.40"}) {
		t.Errorf("status requests %+v, want one whois list of the four first addresses", calls)
	}
}

// TestPbProbeNodesWithoutATailnet: without a tailnet account the nodes are the hosts' own nodes, named after the
// host and ordered by host id; a host whose tailscale.status cannot be read is not a node.
func TestPbProbeNodesWithoutATailnet(t *testing.T) {
	h := pbWorld(t)
	h.tailnet = false
	h.devices = []tailnet.Device{{ID: "d1", Name: "ignored", Addresses: []string{"100.64.0.99"}}}
	pbNodeWorld(h)
	delete(pbAgent(h, "h3").Resources, "tailscale.status")
	got, err := pbRun(h, ServerProbeInput{HostID: "h1"})
	if err != nil {
		t.Fatal(err)
	}
	want := []NodeUse{
		{Name: "alpha", Address: "100.64.0.10", HostID: "h1", Visible: true, Preferred: "eu-derp"},
		{Name: "bravo", Address: "100.64.0.20", HostID: "h2", Visible: true, Preferred: "nyc", Observed: true},
	}
	if !reflect.DeepEqual(got.Nodes, want) {
		t.Errorf("nodes %+v, want %+v", got.Nodes, want)
	}
}

// TestPbProbeCapsTheNodesItChecks: the status read takes at most 256 whois addresses, so a larger tailnet is cut
// to its first 256 devices and the probe says so.
func TestPbProbeCapsTheNodesItChecks(t *testing.T) {
	h := pbWorld(t)
	for i := range 300 {
		h.devices = append(h.devices, tailnet.Device{
			ID: fmt.Sprintf("d%d", i), Name: fmt.Sprintf("n%03d", i), Addresses: []string{fmt.Sprintf("100.64.%d.%d", i/200, i%200+1)},
		})
	}
	got, err := pbRun(h, ServerProbeInput{HostID: "h1"})
	if err != nil {
		t.Fatal(err)
	}
	calls := pbCalls[ServerStatusInput](t, h.agent("h1"), "derp.server.status/v1")
	if len(calls) != 1 || len(calls[0].Whois) != 256 || len(got.Nodes) != 256 || got.Nodes[255].Name != "n255" {
		t.Fatalf("whois %d, nodes %d, want 256 of each ending at n255", len(calls[0].Whois), len(got.Nodes))
	}
	if len(got.Warnings) != 1 || !strings.Contains(got.Warnings[0], "300") || !strings.Contains(got.Warnings[0], "256") {
		t.Errorf("warnings %q, want one naming 300 devices and the 256 checked", got.Warnings)
	}
}

// TestPbProbeControllerFailures: a failing controller probe is a failed result in the controller source, not an
// error of the read, and the probe is then not ready whatever the agents report.
func TestPbProbeControllerFailures(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(h *dpHost)
		tlsFail bool
		stunBad bool
		srcErr  string
	}{
		{"TLS does not answer", func(h *dpHost) { h.tls = map[string]dpProbe{} }, true, false, ""},
		{"STUN does not answer", func(h *dpHost) { h.stun = map[netip.AddrPort]dpProbe{} }, false, true, ""},
		{"the name does not resolve", func(h *dpHost) {
			pbServe(h, "h1", ServerStatus{Installed: true, Mode: "letsencrypt", HostName: "derp.example.com", UnitActive: true}, nil)
			h.resolved = map[string][]netip.Addr{}
		}, true, true, "derp.example.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := pbWorld(t)
			tt.setup(h)
			got, err := pbRun(h, ServerProbeInput{HostID: "h1"})
			if err != nil {
				t.Fatal(err)
			}
			ctl := got.Sources[0]
			if ctl.Source != "controller" || ctl.TLS.OK == tt.tlsFail || ctl.STUN.OK == tt.stunBad {
				t.Errorf("controller source %+v, want TLS failing %v and STUN failing %v", ctl, tt.tlsFail, tt.stunBad)
			}
			if tt.tlsFail && ctl.TLS.Error == "" || tt.stunBad && ctl.STUN.Error == "" {
				t.Errorf("controller source %+v, want an error text on each failing probe", ctl)
			}
			if tt.srcErr == "" && ctl.Error != "" || tt.srcErr != "" && (!strings.Contains(ctl.Error, tt.srcErr) || ctl.TLS.Error != ctl.Error || ctl.STUN.Error != ctl.Error) {
				t.Errorf("controller source error %q, want one naming %q carried by both results (empty when the probes themselves failed)", ctl.Error, tt.srcErr)
			}
			if got.Ready {
				t.Error("ready, want not ready with the controller failing")
			}
		})
	}
}

func pbSource(source, name string, tlsOK, stunOK bool) ProbeSource {
	s := ProbeSource{Source: source, Name: name, TLS: ProbeResult{OK: tlsOK}, STUN: ProbeResult{OK: stunOK}}
	if !tlsOK {
		s.TLS.Error = "tls failed"
	}
	if !stunOK {
		s.STUN.Error = "stun failed"
	}
	return s
}

func TestPbProbeReady(t *testing.T) {
	ctl := pbSource("controller", "controller", true, true)
	h1 := pbSource("h1", "alpha", true, true)
	h2, h3 := pbSource("h2", "bravo", true, true), pbSource("h3", "charlie", true, true)
	h2TLS, h3STUN := pbSource("h2", "bravo", false, true), pbSource("h3", "charlie", true, false)
	alone := []string{"no agent other than the DERP host probed 203.0.113.10; only the controller's probe backs the region"}
	none := []string{}
	tests := []struct {
		name     string
		sources  []ProbeSource
		ready    bool
		reason   string
		warnings []string
	}{
		{"an agent other than the DERP host passes", []ProbeSource{ctl, h2}, true, "", none},
		{"the controller alone", []ProbeSource{ctl}, true, "", alone},
		{"the DERP host's own probe does not count", []ProbeSource{ctl, h1}, true, "", alone},
		{"the controller's TLS fails", []ProbeSource{pbSource("controller", "controller", false, true), h2}, false, "controller: tls: tls failed", none},
		{"the controller's STUN fails", []ProbeSource{pbSource("controller", "controller", true, false), h2}, false, "controller: stun: stun failed", none},
		{"no agent passes", []ProbeSource{ctl, h2TLS, h3STUN}, false, "bravo: tls: tls failed", none},
		{"one agent passes", []ProbeSource{ctl, h2TLS, h3}, true, "", none},
		{"the DERP host passes but the other agent fails", []ProbeSource{ctl, h1, h2TLS}, false, "bravo: tls: tls failed", none},
		{"no controller source", []ProbeSource{h2}, false, "controller: not probed", none},
	}
	for _, tt := range tests {
		p := ServerProbe{HostID: "h1", Server: ServerStatus{HostName: "203.0.113.10"}, Sources: tt.sources}
		ready, reason, warnings := probeReady(p)
		if ready != tt.ready || reason != tt.reason || !reflect.DeepEqual(warnings, tt.warnings) {
			t.Errorf("%s: ready %v, reason %q, warnings %#v; want %v, %q, %#v", tt.name, ready, reason, warnings, tt.ready, tt.reason, tt.warnings)
		}
	}
}
