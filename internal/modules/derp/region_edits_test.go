package derp

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/netip"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/tailnet"
	"github.com/darkraise/darkpanel/internal/modules/tailnet/policy"
)

const (
	reIP   = "203.0.113.10"
	reAck  = "nodes will relay only through eu-derp"
	reUS   = `{"RegionID": 901, "RegionCode": "us-derp", "RegionName": "US", "Nodes": [{"Name": "901a", "RegionID": 901, "HostName": "198.51.100.5"}]}`
	reBase = `{
	// operators keep this comment
	"acls": [{"action": "accept", "src": ["*"], "dst": ["*:*"]}],
	"tagOwners": {"tag:derp": ["autogroup:admin"]}
}`
	reGrantsBase = `{
	"acls": [{"action": "accept", "src": ["*"], "dst": ["*:*"]}],
	"grants": [{"src": ["tag:web"], "dst": ["tag:web"], "ip": ["*"]}]
}`
)

var (
	rePin    = "sha256-raw:" + strings.Repeat("ab", 32)
	reNewPin = "sha256-raw:" + strings.Repeat("cd", 32)
)

func reAgent(h *dpHost, id string) *dpAgent {
	a := h.agent(id)
	if a.Reads == nil {
		a.Reads = map[string]func(json.RawMessage) (any, error){}
	}
	return a
}

// reStatus is derp.server.status of the ip-mode server on h1; its tags are deliberately not sorted.
func reStatus() ServerStatus {
	return ServerStatus{
		Installed: true, Mode: "ip", HostName: reIP, VerifyClients: true, DerperVersion: "v1.104.1", UnitActive: true,
		CertName: rePin, OnTailnet: true, Tags: []string{"tag:web", "tag:derp"}, Addresses: []string{"100.64.0.10"},
	}
}

// reUseServer makes host h1 report the server s and every controller probe reach it at addr.
func reUseServer(h *dpHost, s ServerStatus, addr netip.Addr) {
	reAgent(h, "h1").Reads["derp.server.status/v1"] = func(json.RawMessage) (any, error) { return s, nil }
	h.tls = map[string]dpProbe{s.HostName: {d: 10 * time.Millisecond}, addr.String(): {d: 10 * time.Millisecond}}
	h.stun = map[netip.AddrPort]dpProbe{netip.AddrPortFrom(addr, 3478): {d: 5 * time.Millisecond}}
	h.resolved = map[string][]netip.Addr{s.HostName: {addr}}
}

// reHost is a controller with the DERP host h1 (alpha) running reStatus, a probe host h2 (bravo) whose probes
// pass, a connected tailnet whose one device is h1's node with reStatus's tags, and the policy reBase. No deploy
// change exists yet.
func reHost(t *testing.T) *dpHost {
	t.Helper()
	h := newDPHost(t)
	h.addHost("h1", "alpha", false)
	h.addHost("h2", "bravo", false)
	reUseServer(h, reStatus(), netip.MustParseAddr(reIP))
	reAgent(h, "h2").Reads["derp.node.probe/v1"] = func(json.RawMessage) (any, error) {
		return NodeProbe{TLS: ProbeResult{OK: true, LatencyMs: 20}, STUN: ProbeResult{OK: true, LatencyMs: 30}}, nil
	}
	h.devices = []tailnet.Device{{ID: "d1", Name: "alpha", Addresses: []string{"100.64.0.10"}, Tags: []string{"tag:derp", "tag:web"}}}
	h.tailnet = true
	h.policy = tailnet.Policy{Text: reBase, ETag: "e1"}
	return h
}

// reDeploy adds a derp.server.deploy change of hostID in state, created age ago.
func reDeploy(t *testing.T, h *dpHost, hostID, state string, age time.Duration) {
	t.Helper()
	id := h.addChange("derp.server.deploy", state, DeployInput{HostID: hostID, Mode: "ip", HostName: reIP})
	at := h.clock.Now().Add(-age).UTC().Format("2006-01-02T15:04:05.000000000Z")
	err := h.ctlDB.Write(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(), `UPDATE core_changes SET created_at = ?, updated_at = ? WHERE id = ?`, at, at, id)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// reReady is reHost with a completed deploy of h1.
func reReady(t *testing.T) *dpHost {
	t.Helper()
	h := reHost(t)
	reDeploy(t, h, "h1", core.StateCompleted, time.Hour)
	return h
}

func reSetInput() RegionEditsInput {
	return RegionEditsInput{Op: "set", HostID: "h1", RegionID: 900, RegionCode: "eu-derp", RegionName: "Europe DERP"}
}

func reOmitInput(ack string) RegionEditsInput {
	in := reSetInput()
	in.OmitDefaultRegions, in.Acknowledge = true, ack
	return in
}

func reReveal(t *testing.T, h *dpHost, in RegionEditsInput) (string, RegionEdits, error) {
	t.Helper()
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	identity, value, err := h.dp().regionEdits().Handler.Reveal(context.Background(), h.ctlEnv(""), raw)
	if err != nil {
		return identity, RegionEdits{}, err
	}
	var out RegionEdits
	if err := json.Unmarshal(value, &out); err != nil {
		t.Fatalf("value %s: %v", value, err)
	}
	return identity, out, nil
}

func reRun(t *testing.T, h *dpHost, in RegionEditsInput) (RegionEdits, error) {
	t.Helper()
	_, out, err := reReveal(t, h, in)
	return out, err
}

func reApply(t *testing.T, text string, edits []policy.Edit) string {
	t.Helper()
	out, err := policy.Apply([]byte(text), edits)
	if err != nil {
		t.Fatalf("applying %+v: %v", edits, err)
	}
	return string(out)
}

func reJSON(t *testing.T, raw []byte) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("%s: %v", raw, err)
	}
	return v
}

// reSetUp runs the set op on the host's policy, applies its edits and leaves the result as the host's policy.
func reSetUp(t *testing.T, h *dpHost, in RegionEditsInput) {
	t.Helper()
	out, err := reRun(t, h, in)
	if err != nil {
		t.Fatal(err)
	}
	h.policy.Text = reApply(t, h.policy.Text, out.Edits)
}

// reEdit is an expected edit; a nil Value means the edit carries none.
type reEdit struct {
	Op, Path string
	Value    any
}

func reCheck(t *testing.T, label string, got []policy.Edit, want []reEdit) {
	t.Helper()
	if got == nil {
		t.Errorf("%s: the edits are null, want a list", label)
	}
	if len(got) != len(want) {
		raw, _ := json.Marshal(got)
		t.Errorf("%s: edits %s, want %d of them", label, raw, len(want))
		return
	}
	for i, w := range want {
		g := got[i]
		if g.Op != w.Op || g.Path != w.Path {
			t.Errorf("%s: edit %d is %s %s, want %s %s", label, i, g.Op, g.Path, w.Op, w.Path)
			continue
		}
		if w.Value == nil {
			if len(g.Value) != 0 {
				t.Errorf("%s: edit %d carries the value %s, want none", label, i, g.Value)
			}
			continue
		}
		if v := reJSON(t, g.Value); !reflect.DeepEqual(v, w.Value) {
			t.Errorf("%s: edit %d value %v, want %v", label, i, v, w.Value)
		}
	}
}

func reGrants(t *testing.T, text string) []any {
	t.Helper()
	canon, err := policy.Canonical([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Grants []any `json:"grants"`
	}
	if err := json.Unmarshal(canon, &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Grants
}

func reRegionOf(reg map[string]any) map[string]any { return map[string]any{"900": reg} }

func reRegionObject() map[string]any {
	return map[string]any{
		"RegionID": 900.0, "RegionCode": "eu-derp", "RegionName": "Europe DERP",
		"Nodes": []any{map[string]any{
			"Name": "900a", "RegionID": 900.0, "HostName": reIP, "IPv4": reIP, "CertName": rePin, "DERPPort": 443.0, "STUNPort": 3478.0,
		}},
	}
}

func TestReDeclaration(t *testing.T) {
	def := newDPHost(t).dp().regionEdits()
	s := def.Spec
	if s.Name != "derp.region.edits" || s.Version != 1 || s.Class != core.ClassRead || s.Side != core.SideController ||
		!s.Internal || s.Reveal || s.MinScope != "" || len(s.LockKeys) != 0 || s.Available != nil || s.Summary == "" {
		t.Errorf("spec %+v, want the internal controller read derp.region.edits v1 with no lock or availability check", s)
	}
	if def.InputType != reflect.TypeFor[RegionEditsInput]() || def.OutputType != reflect.TypeFor[RegionEdits]() {
		t.Errorf("types %v and %v, want RegionEditsInput and RegionEdits", def.InputType, def.OutputType)
	}
	if def.Handler.Reveal == nil || def.Handler.Read != nil || def.Handler.Plan != nil || def.Handler.Apply != nil {
		t.Errorf("handler %+v, want the reveal handler only", def.Handler)
	}
}

// TestReRegionObject: the node of the region object is the server's, with its address and pin in ip mode only.
func TestReRegionObject(t *testing.T) {
	node := func(extra map[string]any) map[string]any {
		n := map[string]any{"Name": "900a", "RegionID": 900.0, "DERPPort": 443.0, "STUNPort": 3478.0}
		for k, v := range extra {
			n[k] = v
		}
		return n
	}
	tests := []struct {
		name   string
		status ServerStatus
		node   map[string]any
	}{
		{"letsencrypt", ServerStatus{Mode: "letsencrypt", HostName: "derp.example.com"}, node(map[string]any{"HostName": "derp.example.com"})},
		{"ip with an IPv4 address", ServerStatus{Mode: "ip", HostName: reIP, CertName: rePin},
			node(map[string]any{"HostName": reIP, "IPv4": reIP, "CertName": rePin})},
		{"ip with an IPv6 address", ServerStatus{Mode: "ip", HostName: "2001:db8::10", CertName: rePin},
			node(map[string]any{"HostName": "2001:db8::10", "IPv6": "2001:db8::10", "CertName": rePin})},
	}
	for _, tt := range tests {
		raw, err := json.Marshal(editRegion(tt.status, reSetInput()))
		if err != nil {
			t.Fatal(err)
		}
		want := map[string]any{"RegionID": 900.0, "RegionCode": "eu-derp", "RegionName": "Europe DERP", "Nodes": []any{tt.node}}
		if got := reJSON(t, raw); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: region object %v, want %v", tt.name, got, want)
		}
	}
}

// TestReSetEdits: the edits that create the region, per policy shape; applying them makes the region appear with
// the grant to the server's first sorted tag, and a second run on the result has nothing left to edit.
func TestReSetEdits(t *testing.T) {
	grant := reJSON(t, visibilityGrant("tag:derp"))
	if m, _ := grant.(map[string]any); !reflect.DeepEqual(m["dst"], []any{"tag:derp"}) {
		t.Fatalf("grant %v, want it addressed to the tag it was built for", grant)
	}
	region := reRegionObject()
	withUS := `{"derpMap": {"Regions": {"901": ` + reUS + `}}, "grants": [{"src": ["tag:web"], "dst": ["tag:web"], "ip": ["*"]}]}`
	omittedUS := `{"derpMap": {"OmitDefaultRegions": true, "Regions": {"901": ` + reUS + `}}}`
	tests := []struct {
		name   string
		policy string
		omit   bool
		want   []reEdit
	}{
		{"no derpMap and no grants", reBase, false, []reEdit{
			{"set", "/derpMap", map[string]any{"Regions": reRegionOf(region)}},
			{"set", "/grants", []any{grant}},
		}},
		{"no derpMap, default regions omitted", reBase, true, []reEdit{
			{"set", "/derpMap", map[string]any{"OmitDefaultRegions": true, "Regions": reRegionOf(region)}},
			{"set", "/grants", []any{grant}},
		}},
		{"a derpMap with another region and a grants list", withUS, false, []reEdit{
			{"set", "/derpMap/Regions/900", region},
			{"addItem", "/grants", grant},
		}},
		{"a derpMap without regions", `{"derpMap": {"OmitDefaultRegions": false}}`, false, []reEdit{
			{"set", "/derpMap/Regions", reRegionOf(region)},
			{"set", "/grants", []any{grant}},
		}},
		{"omitting default regions in an existing map", withUS, true, []reEdit{
			{"set", "/derpMap/Regions/900", region},
			{"set", "/derpMap/OmitDefaultRegions", true},
			{"addItem", "/grants", grant},
		}},
		{"default regions already omitted", omittedUS, true, []reEdit{
			{"set", "/derpMap/Regions/900", region},
			{"set", "/grants", []any{grant}},
		}},
	}
	for i, tt := range tests {
		h := reReady(t)
		h.policy.Text = tt.policy
		in := reSetInput()
		if tt.omit {
			in = reOmitInput(reAck)
		}
		identity, out, err := reReveal(t, h, in)
		if err != nil {
			t.Errorf("%s: %v", tt.name, err)
			continue
		}
		if i == 0 && identity != "DERP region set edits for region 900" {
			t.Errorf("identity %q, want %q", identity, "DERP region set edits for region 900")
		}
		reCheck(t, tt.name, out.Edits, tt.want)
		if out.Warnings == nil || len(out.Warnings) != 0 {
			t.Errorf("%s: warnings %#v, want an empty list", tt.name, out.Warnings)
		}
		applied := reApply(t, tt.policy, out.Edits)
		m, err := parseDERPMap(applied)
		if err != nil {
			t.Errorf("%s: the edited policy does not parse: %v", tt.name, err)
			continue
		}
		if r, ok := m.regionOf(reIP); !ok || r.RegionID != 900 || r.RegionCode != "eu-derp" || m.OmitDefaultRegions != tt.omit {
			t.Errorf("%s: region %+v (found %v), omitDefaultRegions %v; want region 900 eu-derp and omitDefaultRegions %v", tt.name, r, ok, m.OmitDefaultRegions, tt.omit)
		}
		h.policy.Text = applied
		again, err := reRun(t, h, in)
		if err != nil || again.Edits == nil || len(again.Edits) != 0 {
			t.Errorf("%s: a second run gives %+v, %v; want no edits", tt.name, again.Edits, err)
		}
	}
}

func TestReSetKeepsTheCommentsOfThePolicy(t *testing.T) {
	h := reReady(t)
	out, err := reRun(t, h, reSetInput())
	if err != nil {
		t.Fatal(err)
	}
	if applied := reApply(t, reBase, out.Edits); !strings.Contains(applied, "// operators keep this comment") {
		t.Errorf("the edited policy lost the operator's comment:\n%s", applied)
	}
}

func TestReSetNeedsAConfirmedDeploy(t *testing.T) {
	tests := []struct {
		name    string
		deploys func(t *testing.T, h *dpHost)
		ok      bool
	}{
		{"no deploy", func(*testing.T, *dpHost) {}, false},
		{"only another host's deploy", func(t *testing.T, h *dpHost) { reDeploy(t, h, "h2", core.StateCompleted, time.Hour) }, false},
		{"the deploy failed", func(t *testing.T, h *dpHost) { reDeploy(t, h, "h1", core.StateFailed, time.Hour) }, false},
		{"the newest deploy awaits confirmation", func(t *testing.T, h *dpHost) {
			reDeploy(t, h, "h1", core.StateCompleted, 2*time.Hour)
			reDeploy(t, h, "h1", core.StateAwaitingConfirmation, time.Hour)
		}, false},
		{"the newest deploy is completed", func(t *testing.T, h *dpHost) {
			reDeploy(t, h, "h1", core.StateAwaitingConfirmation, 2*time.Hour)
			reDeploy(t, h, "h1", core.StateCompleted, time.Hour)
		}, true},
	}
	for _, tt := range tests {
		h := reHost(t)
		tt.deploys(t, h)
		_, err := reRun(t, h, reSetInput())
		if tt.ok {
			if err != nil {
				t.Errorf("%s: %v, want the edits", tt.name, err)
			}
			continue
		}
		if coded := errcode.From(err); err == nil || coded.Code != errcode.DERPNotReady || !strings.Contains(coded.Detail, "deploy") || !core.IsDefinite(err) {
			t.Errorf("%s: err = %#v, want a definite derp.not_ready naming the deploy", tt.name, err)
		}
	}
}

func TestReSetRefusals(t *testing.T) {
	foreign := `{"derpMap": {"Regions": {"900": {"RegionID": 900, "RegionCode": "x", "RegionName": "X",
		"Nodes": [{"Name": "900a", "RegionID": 900, "HostName": "203.0.113.99"}]}}}}`
	noTag := reStatus()
	noTag.Tags = nil
	tests := []struct {
		name   string
		setup  func(h *dpHost)
		in     RegionEditsInput
		code   errcode.Code
		detail string
		field  string
	}{
		{"the probe does not reach the server", func(h *dpHost) { h.tls = map[string]dpProbe{} }, reSetInput(), errcode.DERPNotReady, "controller: tls:", ""},
		{"the region belongs to another server", func(h *dpHost) { h.policy.Text = foreign }, reSetInput(), errcode.DERPRegionTaken, "203.0.113.99", ""},
		{"default regions omitted without an acknowledgement", nil, reOmitInput(""), errcode.InvalidInput, "", "/acknowledge"},
		{"default regions omitted with another acknowledgement", nil, reOmitInput("nodes will relay only through other"), errcode.InvalidInput, "", "/acknowledge"},
		{"the server's node has no tag", func(h *dpHost) { reUseServer(h, noTag, netip.MustParseAddr(reIP)) }, reSetInput(), errcode.DERPNotReady,
			"the DERP host's node has no tag; the visibility grant needs one", ""},
	}
	for _, tt := range tests {
		h := reReady(t)
		if tt.setup != nil {
			tt.setup(h)
		}
		_, err := reRun(t, h, tt.in)
		coded := errcode.From(err)
		if err == nil || coded.Code != tt.code || !core.IsDefinite(err) {
			t.Errorf("%s: err = %#v, want a definite %s refusal", tt.name, err, tt.code)
			continue
		}
		if tt.detail != "" && !strings.Contains(coded.Detail, tt.detail) {
			t.Errorf("%s: detail %q, want it to contain %q", tt.name, coded.Detail, tt.detail)
		}
		if tt.field != "" && (len(coded.Fields) != 1 || coded.Fields[0].Path != tt.field) {
			t.Errorf("%s: fields %+v, want one at %s", tt.name, coded.Fields, tt.field)
		}
	}
}

// TestReSetChecksWhatTheAgentReports: the server's status is the DERP host agent's report, so the set op refuses
// it unless its host name and mode are the confirmed deploy's and pass server.CheckConfig, its ip-mode pin is a pin,
// every tag is a tag name, and the tailnet's device holding the node's address carries the grant's tag.
func TestReSetChecksWhatTheAgentReports(t *testing.T) {
	with := func(f func(s *ServerStatus)) ServerStatus {
		s := reStatus()
		f(&s)
		return s
	}
	tests := []struct {
		name    string
		status  ServerStatus
		devices []tailnet.Device
		detail  string
	}{
		{"a wildcard tag", with(func(s *ServerStatus) { s.Tags = []string{"*", "tag:derp"} }), nil, `"*"`},
		{"an autogroup as a tag", with(func(s *ServerStatus) { s.Tags = []string{"autogroup:member"} }), nil, "autogroup:member"},
		{"an IPv4-mapped host name", with(func(s *ServerStatus) { s.HostName = "::ffff:" + reIP }), nil, "::ffff:" + reIP},
		{"a private host name", with(func(s *ServerStatus) { s.HostName = "10.0.0.5" }), nil, "10.0.0.5"},
		{"a malformed pin", with(func(s *ServerStatus) { s.CertName = "sha256-raw:xyz" }), nil, "sha256-raw:xyz"},
		{"another host name than the deploy's", with(func(s *ServerStatus) { s.HostName = "203.0.113.99" }), nil, "203.0.113.99"},
		{"another mode than the deploy's", with(func(s *ServerStatus) { s.Mode, s.HostName, s.CertName = "letsencrypt", "derp.example.com", "" }), nil, "letsencrypt"},
		{"a device without the tag", reStatus(), []tailnet.Device{{ID: "d1", Name: "alpha", Addresses: []string{"100.64.0.10"}, Tags: []string{"tag:web"}}}, "tag:derp"},
		{"no device with the node's address", reStatus(), []tailnet.Device{{ID: "d9", Name: "other", Addresses: []string{"100.64.0.99"}, Tags: []string{"tag:derp"}}}, "100.64.0.10"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := reReady(t)
			addr := netip.MustParseAddr(reIP)
			if a, err := netip.ParseAddr(tt.status.HostName); err == nil {
				addr = a
			}
			reUseServer(h, tt.status, addr)
			if tt.devices != nil {
				h.devices = tt.devices
			}
			_, err := reRun(t, h, reSetInput())
			coded := errcode.From(err)
			if err == nil || coded.Code != errcode.DERPNotReady || !core.IsDefinite(err) || !strings.Contains(coded.Detail, tt.detail) {
				t.Errorf("err = %#v, want a definite derp.not_ready naming %s", err, tt.detail)
			}
		})
	}
}

// TestReSetOmitNeedsNoAcknowledgementWithAnotherReadyRegion:another custom region whose server passes the probe
// makes the acknowledgement unnecessary; when that server stops answering it is required again.
func TestReSetOmitNeedsNoAcknowledgementWithAnotherReadyRegion(t *testing.T) {
	h := reReady(t)
	h.addHost("h3", "charlie", false)
	second := ServerStatus{Installed: true, Mode: "ip", HostName: "203.0.113.20", DerperVersion: "v1.104.1", UnitActive: true, CertName: rePin, Tags: []string{"tag:derp"}}
	reAgent(h, "h3").Reads["derp.server.status/v1"] = func(json.RawMessage) (any, error) { return second, nil }
	h.tls["203.0.113.20"] = dpProbe{d: 10 * time.Millisecond}
	h.stun[netip.MustParseAddrPort("203.0.113.20:3478")] = dpProbe{d: 5 * time.Millisecond}
	err := upsertServerRecord(context.Background(), h.ctlDB, serverRecord{
		HostID: "h3", Mode: "ip", HostName: "203.0.113.20", RegionID: 901, RegionCode: "us-derp", DerperVersion: "v1.104.1",
		CertName: rePin, UnitActive: true, CheckedAt: h.clock.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	h.policy.Text = `{"derpMap": {"Regions": {"901": {"RegionID": 901, "RegionCode": "us-derp", "RegionName": "US",
		"Nodes": [{"Name": "901a", "RegionID": 901, "HostName": "203.0.113.20"}]}}}}`
	out, err := reRun(t, h, reOmitInput(""))
	if err != nil {
		t.Fatalf("with a ready second region: %v", err)
	}
	if len(out.Edits) < 2 || out.Edits[1].Op != "set" || out.Edits[1].Path != "/derpMap/OmitDefaultRegions" {
		t.Errorf("edits %+v, want the region set and then omitDefaultRegions set", out.Edits)
	}
	delete(h.tls, "203.0.113.20")
	_, err = reRun(t, h, reOmitInput(""))
	if coded := errcode.From(err); err == nil || coded.Code != errcode.InvalidInput || len(coded.Fields) != 1 || coded.Fields[0].Path != "/acknowledge" {
		t.Errorf("with the second server unreachable: err = %#v, want invalid input at /acknowledge", err)
	}
}

// TestReSetWarnsWhenOnlyTheControllerProbed: with no host other than the DERP host the controller's probe alone
// admits the region and its warning is passed on.
func TestReSetWarnsWhenOnlyTheControllerProbed(t *testing.T) {
	h := reReady(t)
	err := h.ctlDB.Write(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(), `DELETE FROM core_hosts WHERE id = 'h2'`)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := reRun(t, h, reSetInput())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"no agent other than the DERP host probed 203.0.113.10; only the controller's probe backs the region"}
	if !reflect.DeepEqual(out.Warnings, want) || len(out.Edits) != 2 {
		t.Errorf("warnings %q and %d edits, want %q and the two set edits", out.Warnings, len(out.Edits), want)
	}
}

func TestReInputValidation(t *testing.T) {
	longName := strings.Repeat("n", 65)
	tests := []struct {
		name  string
		in    RegionEditsInput
		field string
	}{
		{"an unknown op", RegionEditsInput{Op: "bogus", RegionID: 900}, "/op"},
		{"a region id below the range", RegionEditsInput{Op: "remove", RegionID: 899}, "/regionId"},
		{"a region id above the range", RegionEditsInput{Op: "remove", RegionID: 1000}, "/regionId"},
		{"set without a host", RegionEditsInput{Op: "set", RegionID: 900, RegionCode: "eu-derp", RegionName: "x"}, "/hostId"},
		{"set with a bad code", RegionEditsInput{Op: "set", HostID: "h1", RegionID: 900, RegionCode: "EU Derp", RegionName: "x"}, "/regionCode"},
		{"set without a name", RegionEditsInput{Op: "set", HostID: "h1", RegionID: 900, RegionCode: "eu-derp"}, "/regionName"},
		{"set with a long name", RegionEditsInput{Op: "set", HostID: "h1", RegionID: 900, RegionCode: "eu-derp", RegionName: longName}, "/regionName"},
		{"rotate without a host", RegionEditsInput{Op: "rotate", RegionID: 900, CertName: reNewPin}, "/hostId"},
		{"rotate with a bad pin", RegionEditsInput{Op: "rotate", HostID: "h1", RegionID: 900, CertName: "sha256-raw:xyz"}, "/certName"},
	}
	for _, tt := range tests {
		_, err := reRun(t, reHost(t), tt.in)
		coded := errcode.From(err)
		if err == nil || coded.Code != errcode.InvalidInput || len(coded.Fields) != 1 || coded.Fields[0].Path != tt.field || !core.IsDefinite(err) {
			t.Errorf("%s: err = %#v, want a definite invalid input at %s", tt.name, err, tt.field)
		}
	}
}

// TestReRemove: removing a region deletes it; the last one also clears omitDefaultRegions and takes the visibility
// grant (and only that grant) with it; another custom region keeps both.
func TestReRemove(t *testing.T) {
	grant := reJSON(t, visibilityGrant("tag:derp"))
	del := reEdit{"delete", "/derpMap/Regions/900", nil}
	tests := []struct {
		name    string
		policy  string
		set     RegionEditsInput
		want    []reEdit
		regions int
	}{
		{"the last region takes its grant with it", reGrantsBase, reSetInput(), []reEdit{del, {"removeItem", "/grants", grant}}, 0},
		{"the last region clears omitDefaultRegions", reGrantsBase, reOmitInput(reAck),
			[]reEdit{del, {"set", "/derpMap/OmitDefaultRegions", false}, {"removeItem", "/grants", grant}}, 0},
		{"another custom region keeps the grant", `{"derpMap": {"Regions": {"901": ` + reUS + `}}}`, reSetInput(), []reEdit{del}, 1},
	}
	for _, tt := range tests {
		h := reReady(t)
		h.policy.Text = tt.policy
		reSetUp(t, h, tt.set)
		out, err := reRun(t, h, RegionEditsInput{Op: "remove", RegionID: 900})
		if err != nil {
			t.Errorf("%s: %v", tt.name, err)
			continue
		}
		reCheck(t, tt.name, out.Edits, tt.want)
		if out.Warnings == nil || len(out.Warnings) != 0 {
			t.Errorf("%s: warnings %#v, want an empty list", tt.name, out.Warnings)
		}
		applied := reApply(t, h.policy.Text, out.Edits)
		m, err := parseDERPMap(applied)
		if err != nil {
			t.Errorf("%s: the edited policy does not parse: %v", tt.name, err)
			continue
		}
		if len(m.customRegions()) != tt.regions || m.OmitDefaultRegions {
			t.Errorf("%s: custom regions %v, omitDefaultRegions %v; want %d regions and omitDefaultRegions off", tt.name, m.customRegions(), m.OmitDefaultRegions, tt.regions)
		}
		if grants := reGrants(t, applied); len(grants) != 1 {
			t.Errorf("%s: %d grants left, want 1", tt.name, len(grants))
		}
	}
}

// TestReRemoveOfAnAbsentRegion: nothing to remove is an empty list, and the read needs no server, deploy or probe.
func TestReRemoveOfAnAbsentRegion(t *testing.T) {
	h := newDPHost(t)
	h.tailnet = true
	h.policy = tailnet.Policy{Text: reBase}
	out, err := reRun(t, h, RegionEditsInput{Op: "remove", RegionID: 900})
	if err != nil || out.Edits == nil || len(out.Edits) != 0 || out.Warnings == nil || len(out.Warnings) != 0 {
		t.Errorf("remove of an absent region = %+v, %v; want empty edits and warnings, not null", out, err)
	}
}

func reRotateRegion() string {
	return fmt.Sprintf(`{"RegionID": 900, "RegionCode": "eu-derp", "RegionName": "Europe DERP", "Avoid": true, "Nodes": [
		{"Name": "900a", "RegionID": 900, "HostName": %q, "IPv4": %q, "CertName": %q, "CanPort80": true, "DERPPort": 443, "STUNPort": 3478},
		{"Name": "900b", "RegionID": 900, "HostName": "203.0.113.50", "CertName": "sha256-raw:%s"}]}`, reIP, reIP, rePin, strings.Repeat("01", 32))
}

func reRotateHost(t *testing.T, serverHostName string) *dpHost {
	t.Helper()
	h := newDPHost(t)
	h.tailnet = true
	h.policy = tailnet.Policy{Text: `{"derpMap": {"Regions": {"900": ` + reRotateRegion() + `}}}`}
	err := upsertServerRecord(context.Background(), h.ctlDB, serverRecord{
		HostID: "h1", Mode: "ip", HostName: serverHostName, RegionID: 900, RegionCode: "eu-derp", DerperVersion: "v1.104.1",
		CertName: rePin, UnitActive: true, CheckedAt: h.clock.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func reRotateInput() RegionEditsInput {
	return RegionEditsInput{Op: "rotate", HostID: "h1", RegionID: 900, CertName: reNewPin}
}

// TestReRotate: the new pin goes into the server's node only, on the region as the policy holds it, with every
// field the module does not know kept; the result converges.
func TestReRotate(t *testing.T) {
	h := reRotateHost(t, reIP)
	identity, out, err := reReveal(t, h, reRotateInput())
	if err != nil {
		t.Fatal(err)
	}
	if identity != "DERP region rotate edits for region 900" {
		t.Errorf("identity %q, want %q", identity, "DERP region rotate edits for region 900")
	}
	want := reJSON(t, []byte(reRotateRegion()))
	want.(map[string]any)["Nodes"].([]any)[0].(map[string]any)["CertName"] = reNewPin
	reCheck(t, "rotate", out.Edits, []reEdit{{"set", "/derpMap/Regions/900", want}})
	if out.Warnings == nil || len(out.Warnings) != 0 {
		t.Errorf("warnings %#v, want an empty list", out.Warnings)
	}
	h.policy.Text = reApply(t, h.policy.Text, out.Edits)
	m, err := parseDERPMap(h.policy.Text)
	if err != nil {
		t.Fatal(err)
	}
	if r, ok := m.regionOf(reIP); !ok || r.Nodes[0].CertName != reNewPin || r.Nodes[1].CertName == reNewPin {
		t.Errorf("region after the edit %+v (found %v), want the new pin on the first node only", r, ok)
	}
	again, err := reRun(t, h, reRotateInput())
	if err != nil || again.Edits == nil || len(again.Edits) != 0 {
		t.Errorf("a second run gives %+v, %v; want no edits", again.Edits, err)
	}
}

func TestReRotateWithNothingToChange(t *testing.T) {
	absent := reRotateInput()
	absent.RegionID = 901
	tests := []struct {
		name    string
		host    string
		in      RegionEditsInput
		warning string
	}{
		{"the region is absent", reIP, absent, ""},
		{"the region has no node of the server", "203.0.113.77", reRotateInput(), "203.0.113.77"},
	}
	for _, tt := range tests {
		out, err := reRun(t, reRotateHost(t, tt.host), tt.in)
		if err != nil || out.Edits == nil || len(out.Edits) != 0 || out.Warnings == nil {
			t.Errorf("%s: %+v, %v; want empty edits", tt.name, out, err)
			continue
		}
		if tt.warning == "" && len(out.Warnings) != 0 || tt.warning != "" && (len(out.Warnings) != 1 || !strings.Contains(out.Warnings[0], tt.warning)) {
			t.Errorf("%s: warnings %q, want one naming %q (none when empty)", tt.name, out.Warnings, tt.warning)
		}
	}
}

func TestReRotateRefusesAnUnknownServer(t *testing.T) {
	h := reRotateHost(t, reIP)
	in := reRotateInput()
	in.HostID = "h9"
	_, err := reRun(t, h, in)
	if coded := errcode.From(err); err == nil || coded.Code != errcode.DERPNotReady || !core.IsDefinite(err) {
		t.Errorf("err = %#v, want a definite derp.not_ready for a host with no server row", err)
	}
}
