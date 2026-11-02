package derp

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/derp/server"
	"github.com/darkraise/darkpanel/internal/platform/tslocal"
)

var errNpBoom = errors.New("np: boom")

var npPin = server.PinPrefix + strings.Repeat("c", 64)

func npAddrPort(s string) netip.AddrPort { return netip.MustParseAddrPort(s) }

// npRead runs derp.node.probe on d.
func npRead(t *testing.T, d dp, env core.Env, input string) (NodeProbe, error) {
	t.Helper()
	raw, err := d.nodeProbe().Handler.Read(context.Background(), env, json.RawMessage(input))
	if err != nil {
		return NodeProbe{}, err
	}
	var out NodeProbe
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("the answer %s does not decode as a NodeProbe: %v", raw, err)
	}
	return out, nil
}

// npNoProbe makes d fail the test when it probes or resolves anything.
func npNoProbe(t *testing.T, d dp) dp {
	d.d.tlsProbe = func(context.Context, probeTarget) (time.Duration, error) {
		t.Error("a TLS probe ran")
		return 0, errNpBoom
	}
	d.d.stun = func(context.Context, netip.AddrPort) (time.Duration, error) {
		t.Error("a STUN probe ran")
		return 0, errNpBoom
	}
	d.d.resolve = func(context.Context, string) ([]netip.Addr, error) {
		t.Error("a name was resolved")
		return nil, errNpBoom
	}
	return d
}

func TestNpDefinition(t *testing.T) {
	def := newDPHost(t).dp().nodeProbe()
	s := def.Spec
	if s.Name != "derp.node.probe" || s.Version != 1 || s.Class != core.ClassRead || s.Side != core.SideAgent ||
		len(s.LockKeys) != 0 || s.Available != nil || s.Internal || s.LongRunning || s.Task {
		t.Errorf("spec %+v, want a plain agent read derp.node.probe/v1 without locks or an availability check", s)
	}
	if def.InputType != reflect.TypeFor[NodeProbeInput]() || def.OutputType != reflect.TypeFor[NodeProbe]() {
		t.Errorf("input %v and output %v, want NodeProbeInput and NodeProbe", def.InputType, def.OutputType)
	}
	if def.Handler.Read == nil {
		t.Error("the action has no Read handler")
	}
}

// TestNpTargetIPMode: an ip-mode server is reached by its address, pinned, with STUN on the same address.
func TestNpTargetIPMode(t *testing.T) {
	for _, ip := range []string{"203.0.113.9", "2001:db8::1"} {
		t.Run(ip, func(t *testing.T) {
			d := npNoProbe(t, newDPHost(t).dp())
			got, stunAddr, err := d.target(context.Background(), ServerStatus{Installed: true, Mode: server.ModeIP, HostName: ip, CertName: npPin})
			if err != nil {
				t.Fatal(err)
			}
			if want := (probeTarget{HostName: ip, Port: 443, CertName: npPin}); got != want {
				t.Errorf("target %+v, want %+v", got, want)
			}
			if want := netip.AddrPortFrom(netip.MustParseAddr(ip), server.STUNPort); stunAddr != want {
				t.Errorf("STUN address %v, want %v", stunAddr, want)
			}
		})
	}
}

// TestNpTargetLetsEncrypt: a Let's Encrypt server is reached by name, verified against the system roots, at the
// first address its name resolves to, IPv4 preferred.
func TestNpTargetLetsEncrypt(t *testing.T) {
	for _, tc := range []struct {
		name     string
		resolved []string
		want     string
	}{
		{"both families", []string{"2001:db8::7", "203.0.113.7", "203.0.113.8"}, "203.0.113.7"},
		{"only IPv6", []string{"2001:db8::7", "2001:db8::8"}, "2001:db8::7"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := npNoProbe(t, newDPHost(t).dp())
			var addrs []netip.Addr
			for _, a := range tc.resolved {
				addrs = append(addrs, netip.MustParseAddr(a))
			}
			var asked string
			d.d.resolve = func(_ context.Context, host string) ([]netip.Addr, error) {
				asked = host
				return addrs, nil
			}
			got, stunAddr, err := d.target(context.Background(), ServerStatus{Installed: true, Mode: server.ModeLetsEncrypt, HostName: "derp.example.com"})
			if err != nil {
				t.Fatal(err)
			}
			if want := (probeTarget{HostName: "derp.example.com", Address: tc.want, Port: 443}); got != want {
				t.Errorf("target %+v, want %+v", got, want)
			}
			if want := netip.AddrPortFrom(netip.MustParseAddr(tc.want), server.STUNPort); stunAddr != want {
				t.Errorf("STUN address %v, want %v", stunAddr, want)
			}
			if asked != "derp.example.com" {
				t.Errorf("resolved %q, want derp.example.com", asked)
			}
		})
	}
}

func TestNpTargetRefusals(t *testing.T) {
	resolveWith := func(addrs []netip.Addr, err error) func(context.Context, string) ([]netip.Addr, error) {
		return func(context.Context, string) ([]netip.Addr, error) { return addrs, err }
	}
	for _, tc := range []struct {
		name    string
		s       ServerStatus
		resolve func(context.Context, string) ([]netip.Addr, error)
		named   string
	}{
		{"no server installed", ServerStatus{}, nil, ""},
		{"an unknown mode", ServerStatus{Installed: true, Mode: "dns", HostName: "derp.example.com"}, nil, ""},
		{"a name in ip mode", ServerStatus{Installed: true, Mode: server.ModeIP, HostName: "derp.example.com"}, nil, "derp.example.com"},
		{"a name that does not resolve", ServerStatus{Installed: true, Mode: server.ModeLetsEncrypt, HostName: "derp.example.com"},
			resolveWith(nil, errNpBoom), "derp.example.com"},
		{"a name with no address", ServerStatus{Installed: true, Mode: server.ModeLetsEncrypt, HostName: "derp.example.com"},
			resolveWith([]netip.Addr{}, nil), "derp.example.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := npNoProbe(t, newDPHost(t).dp())
			if tc.resolve != nil {
				d.d.resolve = tc.resolve
			}
			_, _, err := d.target(context.Background(), tc.s)
			if err == nil {
				t.Fatal("target succeeded, want an error")
			}
			if tc.named != "" && !strings.Contains(err.Error(), tc.named) {
				t.Errorf("err = %v, want it to name %s", err, tc.named)
			}
		})
	}
}

// TestNpTargetRefusesAStatusTheProbeMustNotReach: the controller checks the status an agent reports as the agent
// checks a probe input, so a reported loopback, link-local, private or Tailscale address, a name that is not a
// lower-case DNS name, or a malformed pin is refused before anything is resolved or probed.
func TestNpTargetRefusesAStatusTheProbeMustNotReach(t *testing.T) {
	for _, s := range []ServerStatus{
		{Mode: server.ModeIP, HostName: "127.0.0.1", CertName: npPin},
		{Mode: server.ModeIP, HostName: "::1", CertName: npPin},
		{Mode: server.ModeIP, HostName: "169.254.169.254", CertName: npPin},
		{Mode: server.ModeIP, HostName: "fe80::1", CertName: npPin},
		{Mode: server.ModeIP, HostName: "10.0.0.5", CertName: npPin},
		{Mode: server.ModeIP, HostName: "100.64.0.10", CertName: npPin},
		{Mode: server.ModeIP, HostName: "::ffff:127.0.0.1", CertName: npPin},
		{Mode: server.ModeIP, HostName: "203.0.113.9", CertName: "sha256-raw:xyz"},
		{Mode: server.ModeLetsEncrypt, HostName: "localhost"},
		{Mode: server.ModeLetsEncrypt, HostName: "Derp.Example.com"},
		{Mode: server.ModeLetsEncrypt, HostName: "203.0.113.9"},
	} {
		t.Run(s.Mode+" "+s.HostName, func(t *testing.T) {
			d := npNoProbe(t, newDPHost(t).dp())
			s.Installed = true
			if _, _, err := d.target(context.Background(), s); err == nil {
				t.Fatal("target succeeded, want a refusal")
			}
		})
	}
}

// TestNpResolveKeepsOnlyAddressesTheProbeMayReach: a name's loopback, link-local, unspecified and multicast answers
// are dropped before the first remaining address is chosen, IPv4 preferred; a name with nothing left, such as a
// DERP host's own name that /etc/hosts maps to 127.0.1.1, is refused with that cause, and neither probe runs.
func TestNpResolveKeepsOnlyAddressesTheProbeMayReach(t *testing.T) {
	addrs := func(ss ...string) []netip.Addr {
		var out []netip.Addr
		for _, s := range ss {
			out = append(out, netip.MustParseAddr(s))
		}
		return out
	}
	le := ServerStatus{Installed: true, Mode: server.ModeLetsEncrypt, HostName: "derp.example.com"}
	for _, tc := range []struct {
		name     string
		resolved []netip.Addr
		want     string
	}{
		{"loopback before a public address", addrs("127.0.0.1", "2001:db8::7", "203.0.113.7"), "203.0.113.7"},
		{"link-local IPv6 before a public IPv6 address", addrs("fe80::1", "::1", "2001:db8::7"), "2001:db8::7"},
		{"a mapped loopback before a public address", addrs("::ffff:127.0.0.1", "198.51.100.4"), "198.51.100.4"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := npNoProbe(t, newDPHost(t).dp())
			d.d.resolve = func(context.Context, string) ([]netip.Addr, error) { return tc.resolved, nil }
			got, stunAddr, err := d.target(context.Background(), le)
			if err != nil {
				t.Fatal(err)
			}
			if want := (probeTarget{HostName: "derp.example.com", Address: tc.want, Port: 443}); got != want {
				t.Errorf("target %+v, want %+v", got, want)
			}
			if want := netip.AddrPortFrom(netip.MustParseAddr(tc.want), server.STUNPort); stunAddr != want {
				t.Errorf("STUN address %v, want %v", stunAddr, want)
			}
		})
	}
	for _, tc := range []struct {
		name     string
		resolved []netip.Addr
		named    []string
	}{
		{"the DERP host's own name in /etc/hosts", addrs("127.0.1.1"), []string{"derp.example.com", "127.0.1.1", "/etc/hosts"}},
		{"only refused classes", addrs("::1", "fe80::1%eth0", "0.0.0.0", "224.0.0.1"), []string{"derp.example.com", "fe80::1%eth0"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newDPHost(t)
			d := npNoProbe(t, h.dp())
			d.d.resolve = func(context.Context, string) ([]netip.Addr, error) { return tc.resolved, nil }
			_, _, err := d.target(context.Background(), le)
			if err == nil {
				t.Fatal("target succeeded, want a refusal")
			}
			got, rerr := npRead(t, d, h.env(""), `{"hostName":"derp.example.com"}`)
			if rerr != nil {
				t.Fatal(rerr)
			}
			for _, part := range tc.named {
				if !strings.Contains(err.Error(), part) {
					t.Errorf("target: err = %v, want it to name %s", err, part)
				}
				if !strings.Contains(got.TLS.Error, part) || !strings.Contains(got.STUN.Error, part) {
					t.Errorf("read: tls %+v and stun %+v, want both failing with an error naming %s", got.TLS, got.STUN, part)
				}
			}
		})
	}
}

func TestNpProbeOnce(t *testing.T) {
	for _, tc := range []struct {
		name     string
		target   probeTarget
		stunAddr netip.AddrPort
		tls      map[string]dpProbe
		stun     map[netip.AddrPort]dpProbe
		wantTLS  ProbeResult
		wantSTUN ProbeResult
	}{
		{
			"both answer", probeTarget{HostName: "203.0.113.9", Port: 443}, npAddrPort("203.0.113.9:3478"),
			map[string]dpProbe{"203.0.113.9": {d: 12 * time.Millisecond}},
			map[netip.AddrPort]dpProbe{npAddrPort("203.0.113.9:3478"): {d: 1500 * time.Microsecond}},
			ProbeResult{OK: true, LatencyMs: 12}, ProbeResult{OK: true, LatencyMs: 1.5},
		},
		{
			"the TLS probe fails, keyed by the address", probeTarget{HostName: "derp.example.com", Address: "198.51.100.4", Port: 443},
			npAddrPort("198.51.100.4:3478"),
			map[string]dpProbe{"198.51.100.4": {err: errNpBoom}},
			map[netip.AddrPort]dpProbe{npAddrPort("198.51.100.4:3478"): {d: 2 * time.Millisecond}},
			ProbeResult{Error: "np: boom"}, ProbeResult{OK: true, LatencyMs: 2},
		},
		{
			"the STUN probe fails", probeTarget{HostName: "203.0.113.9", Port: 443}, npAddrPort("203.0.113.9:3478"),
			map[string]dpProbe{"203.0.113.9": {d: 3 * time.Millisecond}},
			map[netip.AddrPort]dpProbe{npAddrPort("203.0.113.9:3478"): {err: errNpBoom}},
			ProbeResult{OK: true, LatencyMs: 3}, ProbeResult{Error: "np: boom"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newDPHost(t)
			h.tls, h.stun = tc.tls, tc.stun
			gotTLS, gotSTUN := h.dp().probeOnce(context.Background(), tc.target, tc.stunAddr)
			if gotTLS != tc.wantTLS || gotSTUN != tc.wantSTUN {
				t.Errorf("tls %+v and stun %+v, want %+v and %+v", gotTLS, gotSTUN, tc.wantTLS, tc.wantSTUN)
			}
		})
	}
}

// TestNpReadProbes: the read probes the target the input describes and reports both measurements; the pin and
// the address reach the TLS probe, and STUN goes to the address.
func TestNpReadProbes(t *testing.T) {
	for _, tc := range []struct {
		name         string
		input        string
		resolved     []netip.Addr
		wantTarget   probeTarget
		wantSTUN     string
		wantResolved string
	}{
		{
			"an ip-mode server with its pin", `{"hostName":"203.0.113.9","certName":"` + npPin + `"}`, nil,
			probeTarget{HostName: "203.0.113.9", Port: 443, CertName: npPin}, "203.0.113.9:3478", "",
		},
		{
			"a name with the address the controller resolved", `{"hostName":"derp.example.com","address":"198.51.100.4"}`, nil,
			probeTarget{HostName: "derp.example.com", Address: "198.51.100.4", Port: 443}, "198.51.100.4:3478", "",
		},
		{
			"a name this agent resolves once for both probes", `{"hostName":"derp.example.com"}`,
			[]netip.Addr{netip.MustParseAddr("2001:db8::7"), netip.MustParseAddr("203.0.113.7")},
			probeTarget{HostName: "derp.example.com", Address: "203.0.113.7", Port: 443}, "203.0.113.7:3478", "derp.example.com",
		},
		{
			"a name whose first answer is loopback", `{"hostName":"derp.example.com"}`,
			[]netip.Addr{netip.MustParseAddr("127.0.0.1"), netip.MustParseAddr("203.0.113.7")},
			probeTarget{HostName: "derp.example.com", Address: "203.0.113.7", Port: 443}, "203.0.113.7:3478", "derp.example.com",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newDPHost(t)
			d := h.dp()
			var gotTarget probeTarget
			var gotSTUN []netip.AddrPort
			var resolvedHost string
			d.d.tlsProbe = func(_ context.Context, target probeTarget) (time.Duration, error) {
				gotTarget = target
				return 12 * time.Millisecond, nil
			}
			d.d.stun = func(_ context.Context, addr netip.AddrPort) (time.Duration, error) {
				gotSTUN = append(gotSTUN, addr)
				return 1500 * time.Microsecond, nil
			}
			d.d.resolve = func(_ context.Context, host string) ([]netip.Addr, error) {
				resolvedHost = host
				return tc.resolved, nil
			}
			got, err := npRead(t, d, h.env(""), tc.input)
			if err != nil {
				t.Fatal(err)
			}
			want := NodeProbe{TLS: ProbeResult{OK: true, LatencyMs: 12}, STUN: ProbeResult{OK: true, LatencyMs: 1.5}}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("probe\n got %+v\nwant %+v", got, want)
			}
			if gotTarget != tc.wantTarget {
				t.Errorf("TLS target %+v, want %+v", gotTarget, tc.wantTarget)
			}
			if !reflect.DeepEqual(gotSTUN, []netip.AddrPort{npAddrPort(tc.wantSTUN)}) {
				t.Errorf("STUN servers %v, want [%s]", gotSTUN, tc.wantSTUN)
			}
			if resolvedHost != tc.wantResolved {
				t.Errorf("resolved %q, want %q", resolvedHost, tc.wantResolved)
			}
		})
	}
}

// TestNpReadProbeFailures: a failed measurement is a result, not a failed read; a name this agent cannot resolve
// fails both measurements without probing, because the TLS probe never resolves a name itself.
func TestNpReadProbeFailures(t *testing.T) {
	h := newDPHost(t)
	d := npNoProbe(t, h.dp())
	d.d.resolve = func(context.Context, string) ([]netip.Addr, error) { return nil, errNpBoom }
	got, err := npRead(t, d, h.env(""), `{"hostName":"derp.example.com"}`)
	if err != nil {
		t.Fatal(err)
	}
	for name, r := range map[string]ProbeResult{"tls": got.TLS, "stun": got.STUN} {
		if r.OK || r.LatencyMs != 0 || !strings.Contains(r.Error, "derp.example.com") {
			t.Errorf("%s %+v, want a failure naming derp.example.com", name, r)
		}
	}

	h = newDPHost(t)
	h.tls = map[string]dpProbe{"203.0.113.9": {err: errNpBoom}}
	h.stun = map[netip.AddrPort]dpProbe{npAddrPort("203.0.113.9:3478"): {err: errNpBoom}}
	got, err = npRead(t, h.dp(), h.env(""), `{"hostName":"203.0.113.9"}`)
	if err != nil {
		t.Fatal(err)
	}
	want := NodeProbe{TLS: ProbeResult{Error: "np: boom"}, STUN: ProbeResult{Error: "np: boom"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("probe\n got %+v\nwant %+v", got, want)
	}
}

func TestNpReadRegion(t *testing.T) {
	newHost := func(t *testing.T) *dpHost {
		h := newDPHost(t)
		h.tls = map[string]dpProbe{"203.0.113.9": {d: time.Millisecond}}
		h.stun = map[netip.AddrPort]dpProbe{npAddrPort("203.0.113.9:3478"): {d: time.Millisecond}}
		return h
	}
	t.Run("the report of the region", func(t *testing.T) {
		h := newHost(t)
		h.local.Regions = map[string]*tslocal.DERPRegionReport{
			"900": {Info: []string{"derper answered"}, Errors: []string{"no STUN reply"}},
		}
		got, err := npRead(t, h.dp(), h.env(""), `{"hostName":"203.0.113.9","regionId":900}`)
		if err != nil {
			t.Fatal(err)
		}
		want := &RegionDiagnostics{Info: []string{"derper answered"}, Warnings: []string{}, Errors: []string{"no STUN reply"}}
		if !reflect.DeepEqual(got.Region, want) {
			t.Errorf("region %+v, want %+v", got.Region, want)
		}
	})
	t.Run("no region id skips the diagnosis", func(t *testing.T) {
		h := newHost(t)
		h.local.Errs = map[string]error{"DebugDERPRegion": errNpBoom}
		got, err := npRead(t, h.dp(), h.env(""), `{"hostName":"203.0.113.9"}`)
		if err != nil || got.Region != nil {
			t.Errorf("region %+v, err %v, want no region and no error", got.Region, err)
		}
	})
	t.Run("a failed diagnosis is reported in the region", func(t *testing.T) {
		h := newHost(t)
		h.local.Errs = map[string]error{"DebugDERPRegion": errNpBoom}
		got, err := npRead(t, h.dp(), h.env(""), `{"hostName":"203.0.113.9","regionId":900}`)
		if err != nil {
			t.Fatal(err)
		}
		if !got.TLS.OK || !got.STUN.OK {
			t.Errorf("tls %+v and stun %+v, want both measured", got.TLS, got.STUN)
		}
		if got.Region == nil || len(got.Region.Errors) != 1 || !strings.Contains(got.Region.Errors[0], "np: boom") ||
			len(got.Region.Info) != 0 || len(got.Region.Warnings) != 0 {
			t.Errorf("region %+v, want one error carrying the failure and nothing else", got.Region)
		}
	})
}

// TestNpReadValidation: the input is checked before anything is probed, with a field error at the input member.
func TestNpReadValidation(t *testing.T) {
	for _, tc := range []struct {
		name, input, path string
	}{
		{"an empty host name", `{"hostName":""}`, "/hostName"},
		{"a host name with a slash", `{"hostName":"a/b.example.com"}`, "/hostName"},
		{"an upper-case host name", `{"hostName":"Derp.Example.com"}`, "/hostName"},
		{"a single-label host name", `{"hostName":"derp"}`, "/hostName"},
		{"a loopback host name", `{"hostName":"127.0.0.1"}`, "/hostName"},
		{"a link-local host name", `{"hostName":"169.254.169.254"}`, "/hostName"},
		{"an unspecified host name", `{"hostName":"::"}`, "/hostName"},
		{"an address that is a name", `{"hostName":"derp.example.com","address":"derp2.example.com"}`, "/address"},
		{"a loopback address", `{"hostName":"derp.example.com","address":"::1"}`, "/address"},
		{"a link-local address", `{"hostName":"derp.example.com","address":"169.254.169.254"}`, "/address"},
		{"a pin without the prefix", `{"hostName":"203.0.113.9","certName":"letsencrypt"}`, "/certName"},
		{"a short pin", `{"hostName":"203.0.113.9","certName":"sha256-raw:abcd"}`, "/certName"},
		{"an upper-case pin", `{"hostName":"203.0.113.9","certName":"sha256-raw:` + strings.Repeat("A", 64) + `"}`, "/certName"},
		{"a region below 900", `{"hostName":"203.0.113.9","regionId":5}`, "/regionId"},
		{"a region above 999", `{"hostName":"203.0.113.9","regionId":1000}`, "/regionId"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newDPHost(t)
			_, err := npRead(t, npNoProbe(t, h.dp()), h.env(""), tc.input)
			coded := errcode.From(err)
			if err == nil || coded.Code != errcode.InvalidInput || len(coded.Fields) == 0 || coded.Fields[0].Path != tc.path {
				t.Errorf("err = %v (fields %+v), want request.invalid_input with a field error at %s", err, coded.Fields, tc.path)
			}
		})
	}
}

func TestNpReadNodeProbe(t *testing.T) {
	ctx := context.Background()
	h := newDPHost(t)
	h.addHost("h2", "web-2", false)
	want := NodeProbe{
		TLS:    ProbeResult{OK: true, LatencyMs: 12},
		STUN:   ProbeResult{Error: "no reply"},
		Region: &RegionDiagnostics{Info: []string{}, Warnings: []string{}, Errors: []string{"e"}},
	}
	ag := h.agent("h2")
	ag.Reads = map[string]func(json.RawMessage) (any, error){
		"derp.node.probe/v1": func(json.RawMessage) (any, error) { return want, nil },
	}
	got, err := readNodeProbe(ctx, h.ctlEnv(""), "h2", NodeProbeInput{HostName: "203.0.113.9", RegionID: 900})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("probe\n got %+v\nwant %+v", got, want)
	}
	if _, err := readNodeProbe(ctx, h.ctlEnv(""), "h2", NodeProbeInput{HostName: "derp.example.com", Address: "198.51.100.4", CertName: npPin}); err != nil {
		t.Fatal(err)
	}
	wantCalls := []string{
		`derp.node.probe/v1 {"hostName":"203.0.113.9","regionId":900}`,
		`derp.node.probe/v1 {"hostName":"derp.example.com","address":"198.51.100.4","certName":"` + npPin + `"}`,
	}
	if !reflect.DeepEqual(ag.Calls, wantCalls) {
		t.Errorf("agent calls %q, want %q", ag.Calls, wantCalls)
	}

	ag.Reads["derp.node.probe/v1"] = func(json.RawMessage) (any, error) { return nil, errNpBoom }
	if _, err := readNodeProbe(ctx, h.ctlEnv(""), "h2", NodeProbeInput{HostName: "203.0.113.9"}); !errors.Is(err, errNpBoom) || !strings.Contains(err.Error(), "h2") {
		t.Errorf("err = %v, want the agent's failure, naming host h2", err)
	}
	ag.Reads["derp.node.probe/v1"] = func(json.RawMessage) (any, error) { return "oops", nil }
	if _, err := readNodeProbe(ctx, h.ctlEnv(""), "h2", NodeProbeInput{HostName: "203.0.113.9"}); err == nil || !strings.Contains(err.Error(), "h2") {
		t.Errorf("err = %v, want a decode failure naming host h2", err)
	}
}
