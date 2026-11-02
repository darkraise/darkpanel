package derp

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/modules/tailnet/policy"
)

func sdWireTypes() []any {
	return []any{
		ServerInstallInput{}, ServerInstalled{}, ServerUninstallInput{}, ServerUninstalled{}, BinaryUpdateInput{}, BinaryUpdated{},
		CertRenewInput{}, CertRenewed{}, ServerStatusInput{}, ServerStatus{}, Visibility{}, NodeProbeInput{}, NodeProbe{}, ProbeResult{},
		RegionDiagnostics{}, ServerProbeInput{}, ServerProbe{}, ProbeSource{}, NodeUse{}, Servers{}, ServerView{}, RegionEditsInput{},
		RegionEdits{}, FallbackCheckInput{}, FallbackCheck{}, DeployInput{}, Deployed{}, UpgradeInput{}, Upgraded{}, CertRotateInput{},
		CertRotated{}, RegionSetInput{}, RegionRemoveInput{}, ServerRemoveInput{}, RegionChanged{}, ServerRemoved{},
	}
}

const sdEmptyStatus = `{"installed":false,"mode":"","hostName":"","verifyClients":false,"derperVersion":"","unitActive":false,` +
	`"certName":"","certNotAfter":null,"availableVersion":"","linkViaTailnet":false,"onTailnet":false,"tags":[],"addresses":[],"visible":[]}`

func TestSdWireJSON(t *testing.T) {
	notAfter := time.Date(2027, 11, 2, 12, 0, 0, 0, time.UTC)
	checked := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	no := false
	emptyStatus := ServerStatus{Tags: []string{}, Addresses: []string{}, Visible: []Visibility{}}
	tests := []struct {
		name string
		v    any
		want string
	}{
		{"install input", ServerInstallInput{Mode: "ip", HostName: "203.0.113.10"}, `{"mode":"ip","hostName":"203.0.113.10"}`},
		{"install input without verification", ServerInstallInput{Mode: "letsencrypt", HostName: "derp.example.com", VerifyClients: &no},
			`{"mode":"letsencrypt","hostName":"derp.example.com","verifyClients":false}`},
		{"installed", ServerInstalled{DerperVersion: "v1.104.1", CertName: "sha256-raw:ab", CertNotAfter: &notAfter},
			`{"derperVersion":"v1.104.1","certName":"sha256-raw:ab","certNotAfter":"2027-11-02T12:00:00Z"}`},
		{"installed with a Let's Encrypt certificate", ServerInstalled{DerperVersion: "v1.104.1"}, `{"derperVersion":"v1.104.1","certName":"","certNotAfter":null}`},
		{"uninstall input", ServerUninstallInput{}, `{}`},
		{"uninstalled", ServerUninstalled{Removed: []string{"derp.unit:darkpanel-derper.service"}}, `{"removed":["derp.unit:darkpanel-derper.service"]}`},
		{"binary update input", BinaryUpdateInput{Version: "v1.104.1"}, `{"version":"v1.104.1"}`},
		{"binary updated", BinaryUpdated{From: "v1.103.0", To: "v1.104.1"}, `{"from":"v1.103.0","to":"v1.104.1"}`},
		{"renew input", CertRenewInput{}, `{}`},
		{"renewed", CertRenewed{CertName: "sha256-raw:new", CertNotAfter: notAfter, PriorCertName: "sha256-raw:old"},
			`{"certName":"sha256-raw:new","certNotAfter":"2027-11-02T12:00:00Z","priorCertName":"sha256-raw:old"}`},
		{"status input", ServerStatusInput{}, `{}`},
		{"status input with addresses", ServerStatusInput{Whois: []string{"100.64.0.7"}}, `{"whois":["100.64.0.7"]}`},
		{"status of an empty host", emptyStatus, sdEmptyStatus},
		{"status of a server", ServerStatus{Installed: true, Mode: "ip", HostName: "203.0.113.10", VerifyClients: true, DerperVersion: "v1.104.1",
			UnitActive: true, CertName: "sha256-raw:ab", CertNotAfter: &notAfter, AvailableVersion: "v1.104.1", LinkViaTailnet: true, OnTailnet: true,
			Tags: []string{"tag:derp"}, Addresses: []string{"100.64.0.10"}, Visible: []Visibility{{Address: "100.64.0.7", Visible: true, Node: "web.example.ts.net"}}},
			`{"installed":true,"mode":"ip","hostName":"203.0.113.10","verifyClients":true,"derperVersion":"v1.104.1","unitActive":true,` +
				`"certName":"sha256-raw:ab","certNotAfter":"2027-11-02T12:00:00Z","availableVersion":"v1.104.1","linkViaTailnet":true,"onTailnet":true,` +
				`"tags":["tag:derp"],"addresses":["100.64.0.10"],"visible":[{"address":"100.64.0.7","visible":true,"node":"web.example.ts.net"}]}`},
		{"node probe input", NodeProbeInput{HostName: "derp.example.com", Address: "203.0.113.10", CertName: "sha256-raw:ab", RegionID: 900},
			`{"hostName":"derp.example.com","address":"203.0.113.10","certName":"sha256-raw:ab","regionId":900}`},
		{"node probe input without options", NodeProbeInput{HostName: "derp.example.com"}, `{"hostName":"derp.example.com"}`},
		{"node probe", NodeProbe{TLS: ProbeResult{OK: true, LatencyMs: 12.5}, STUN: ProbeResult{Error: "timeout"}},
			`{"tls":{"ok":true,"latencyMs":12.5,"error":""},"stun":{"ok":false,"latencyMs":0,"error":"timeout"},"region":null}`},
		{"node probe with a region report", NodeProbe{Region: &RegionDiagnostics{Info: []string{"ok"}, Warnings: []string{}, Errors: []string{}}},
			`{"tls":{"ok":false,"latencyMs":0,"error":""},"stun":{"ok":false,"latencyMs":0,"error":""},"region":{"info":["ok"],"warnings":[],"errors":[]}}`},
		{"server probe input", ServerProbeInput{HostID: "h2"}, `{"hostId":"h2"}`},
		{"server probe input with agents", ServerProbeInput{HostID: "h2", Agents: []string{"h3"}}, `{"hostId":"h2","agents":["h3"]}`},
		{"server probe", ServerProbe{HostID: "h2", RegionID: 900, RegionCode: "fra-x", Server: emptyStatus,
			Sources: []ProbeSource{{Source: "controller", Name: "controller", TLS: ProbeResult{OK: true, LatencyMs: 1}, STUN: ProbeResult{OK: true, LatencyMs: 2}}},
			Nodes:   []NodeUse{{Name: "web", Address: "100.64.0.7", HostID: "h3", Visible: true, Preferred: "fra-x", Observed: true}}, Ready: true, Warnings: []string{}},
			`{"hostId":"h2","regionId":900,"regionCode":"fra-x","server":` + sdEmptyStatus + `,"sources":[{"source":"controller","name":"controller",` +
				`"tls":{"ok":true,"latencyMs":1,"error":""},"stun":{"ok":true,"latencyMs":2,"error":""},"region":null,"error":""}],` +
				`"nodes":[{"name":"web","address":"100.64.0.7","hostId":"h3","visible":true,"preferred":"fra-x","observed":true}],"ready":true,"warnings":[]}`},
		{"servers", Servers{Servers: []ServerView{{HostID: "h2", Host: "derp-1", Mode: "ip", HostName: "203.0.113.10", RegionID: 900, RegionCode: "fra-x",
			DerperVersion: "v1.104.1", CertName: "sha256-raw:ab", CertNotAfter: &notAfter, UnitActive: true, Health: "ok", Reasons: []string{}, CheckedAt: checked}},
			AvailableVersion: "v1.104.1"},
			`{"servers":[{"hostId":"h2","host":"derp-1","mode":"ip","hostName":"203.0.113.10","regionId":900,"regionCode":"fra-x","derperVersion":"v1.104.1",` +
				`"certName":"sha256-raw:ab","certNotAfter":"2027-11-02T12:00:00Z","unitActive":true,"health":"ok","reasons":[],"checkedAt":"2026-11-02T12:00:00Z",` +
				`"error":""}],"availableVersion":"v1.104.1"}`},
		{"region edits input to remove", RegionEditsInput{Op: "remove", RegionID: 900}, `{"op":"remove","regionId":900}`},
		{"region edits input to set", RegionEditsInput{Op: "set", HostID: "h2", RegionID: 900, RegionCode: "fra-x", RegionName: "Frankfurt X",
			OmitDefaultRegions: true, Acknowledge: "nodes will relay only through fra-x"},
			`{"op":"set","hostId":"h2","regionId":900,"regionCode":"fra-x","regionName":"Frankfurt X","omitDefaultRegions":true,"acknowledge":"nodes will relay only through fra-x"}`},
		{"region edits input to rotate", RegionEditsInput{Op: "rotate", HostID: "h2", RegionID: 900, CertName: "sha256-raw:new"},
			`{"op":"rotate","hostId":"h2","regionId":900,"certName":"sha256-raw:new"}`},
		{"region edits", RegionEdits{Edits: []policy.Edit{{Op: "delete", Path: "/derpMap/Regions/900"}}, Warnings: []string{}},
			`{"edits":[{"op":"delete","path":"/derpMap/Regions/900"}],"warnings":[]}`},
		{"fallback check input", FallbackCheckInput{HostID: "h2", RegionID: 900}, `{"hostId":"h2","regionId":900}`},
		{"fallback check", FallbackCheck{Path: "region:901"}, `{"path":"region:901"}`},
		{"deploy input", DeployInput{HostID: "h2", Mode: "ip", HostName: "203.0.113.10"}, `{"hostId":"h2","mode":"ip","hostName":"203.0.113.10"}`},
		{"deploy input without verification", DeployInput{HostID: "h2", Mode: "ip", HostName: "203.0.113.10", VerifyClients: &no},
			`{"hostId":"h2","mode":"ip","hostName":"203.0.113.10","verifyClients":false}`},
		{"deployed", Deployed{DerperVersion: "v1.104.1", CertName: "sha256-raw:ab", CertNotAfter: &notAfter},
			`{"derperVersion":"v1.104.1","certName":"sha256-raw:ab","certNotAfter":"2027-11-02T12:00:00Z"}`},
		{"upgrade input", UpgradeInput{HostID: "h2"}, `{"hostId":"h2"}`},
		{"upgraded", Upgraded{From: "v1.103.0", To: "v1.104.1"}, `{"from":"v1.103.0","to":"v1.104.1"}`},
		{"rotate input", CertRotateInput{HostID: "h2"}, `{"hostId":"h2"}`},
		{"rotated", CertRotated{CertName: "sha256-raw:new", CertNotAfter: notAfter}, `{"certName":"sha256-raw:new","certNotAfter":"2027-11-02T12:00:00Z"}`},
		{"region set input", RegionSetInput{HostID: "h2", RegionID: 900, RegionCode: "fra-x", RegionName: "Frankfurt X"},
			`{"hostId":"h2","regionId":900,"regionCode":"fra-x","regionName":"Frankfurt X"}`},
		{"region set input with omission", RegionSetInput{HostID: "h2", RegionID: 900, RegionCode: "fra-x", RegionName: "Frankfurt X", OmitDefaultRegions: true,
			Acknowledge: "nodes will relay only through fra-x"},
			`{"hostId":"h2","regionId":900,"regionCode":"fra-x","regionName":"Frankfurt X","omitDefaultRegions":true,"acknowledge":"nodes will relay only through fra-x"}`},
		{"region remove input", RegionRemoveInput{RegionID: 900}, `{"regionId":900}`},
		{"server remove input", ServerRemoveInput{HostID: "h2"}, `{"hostId":"h2"}`},
		{"region changed", RegionChanged{ETag: "e1"}, `{"etag":"e1"}`},
		{"server removed", ServerRemoved{Removed: []string{"a", "b"}}, `{"removed":["a","b"]}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, err := json.Marshal(tt.v)
			if err != nil {
				t.Fatal(err)
			}
			if string(b) != tt.want {
				t.Errorf("got  %s\nwant %s", b, tt.want)
			}
			back := reflect.New(reflect.TypeOf(tt.v))
			if err := json.Unmarshal(b, back.Interface()); err != nil {
				t.Fatal(err)
			}
			if again, _ := json.Marshal(back.Elem().Interface()); string(again) != tt.want {
				t.Errorf("round trip changed the document: %s", again)
			}
		})
	}
}

func TestSdWireDescriptions(t *testing.T) {
	for _, v := range sdWireTypes() {
		rt := reflect.TypeOf(v)
		for i := range rt.NumField() {
			f := rt.Field(i)
			if f.Tag.Get("json") == "" {
				t.Errorf("%s.%s has no json name", rt.Name(), f.Name)
			}
			if f.Tag.Get("jsonschema_description") == "" {
				t.Errorf("%s.%s has no jsonschema_description", rt.Name(), f.Name)
			}
		}
	}
}

func sdSchemaDef(t *testing.T, raw json.RawMessage) (def map[string]any) {
	t.Helper()
	var doc struct {
		Ref  string                    `json:"$ref"`
		Defs map[string]map[string]any `json:"$defs"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("schema %s: %v", raw, err)
	}
	def = doc.Defs[strings.TrimPrefix(doc.Ref, "#/$defs/")]
	if def == nil {
		t.Fatalf("schema %s: no definition for %q", raw, doc.Ref)
	}
	return def
}

func sdProps(t *testing.T, v any) (props map[string]map[string]any, required []string) {
	t.Helper()
	raw, err := schema.Input(reflect.TypeOf(v))
	if err != nil {
		t.Fatalf("input schema of %T: %v", v, err)
	}
	def := sdSchemaDef(t, raw)
	props = map[string]map[string]any{}
	members, _ := def["properties"].(map[string]any)
	for k, m := range members {
		props[k], _ = m.(map[string]any)
	}
	list, _ := def["required"].([]any)
	for _, r := range list {
		required = append(required, r.(string))
	}
	return props, required
}

func TestSdWireSchemas(t *testing.T) {
	for _, v := range []any{ServerInstallInput{}, ServerUninstallInput{}, BinaryUpdateInput{}, CertRenewInput{}, ServerStatusInput{}, NodeProbeInput{},
		ServerProbeInput{}, RegionEditsInput{}, FallbackCheckInput{}, DeployInput{}, UpgradeInput{}, CertRotateInput{}, RegionSetInput{}, RegionRemoveInput{},
		ServerRemoveInput{}} {
		if _, err := schema.Input(reflect.TypeOf(v)); err != nil {
			t.Errorf("input schema of %T: %v", v, err)
		}
	}
	for _, v := range []any{ServerInstalled{}, ServerUninstalled{}, BinaryUpdated{}, CertRenewed{}, ServerStatus{}, NodeProbe{}, ServerProbe{}, Servers{},
		RegionEdits{}, FallbackCheck{}, Deployed{}, Upgraded{}, CertRotated{}, RegionChanged{}, ServerRemoved{}} {
		if _, err := schema.Output(reflect.TypeOf(v)); err != nil {
			t.Errorf("output schema of %T: %v", v, err)
		}
	}

	props, required := sdProps(t, ServerInstallInput{})
	if !reflect.DeepEqual(props["mode"]["enum"], []any{"letsencrypt", "ip"}) || props["hostName"]["maxLength"] != float64(253) {
		t.Errorf("install input mode %v, hostName %v", props["mode"], props["hostName"])
	}
	if !reflect.DeepEqual(required, []string{"mode", "hostName"}) && !reflect.DeepEqual(required, []string{"hostName", "mode"}) {
		t.Errorf("install input requires %v, want mode and hostName only (verifyClients is optional)", required)
	}

	props, _ = sdProps(t, BinaryUpdateInput{})
	if props["version"]["pattern"] != `^v[0-9]+\.[0-9]+\.[0-9]+$` {
		t.Errorf("update version pattern %v", props["version"]["pattern"])
	}
	props, _ = sdProps(t, ServerStatusInput{})
	if props["whois"]["maxItems"] != float64(256) {
		t.Errorf("whois %v", props["whois"])
	}
	props, _ = sdProps(t, ServerProbeInput{})
	if props["agents"]["maxItems"] != float64(8) || props["hostId"]["minLength"] != float64(1) {
		t.Errorf("probe input agents %v, hostId %v", props["agents"], props["hostId"])
	}
	props, _ = sdProps(t, RegionEditsInput{})
	if !reflect.DeepEqual(props["op"]["enum"], []any{"set", "remove", "rotate"}) || props["regionCode"]["pattern"] != `^[a-z][a-z0-9-]{0,31}$` ||
		props["regionName"]["maxLength"] != float64(64) {
		t.Errorf("region edits op %v, regionCode %v, regionName %v", props["op"], props["regionCode"], props["regionName"])
	}
	for _, v := range []any{RegionEditsInput{}, RegionSetInput{}, RegionRemoveInput{}, FallbackCheckInput{}, NodeProbeInput{}} {
		props, _ := sdProps(t, v)
		if props["regionId"]["minimum"] != float64(900) || props["regionId"]["maximum"] != float64(999) {
			t.Errorf("%T regionId = %v, want 900 to 999", v, props["regionId"])
		}
	}
}
