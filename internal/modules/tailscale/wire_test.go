package tailscale

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/schema"
)

func thWireTypes() []any {
	return []any{
		Status{}, Node{}, Peer{}, Latency{}, RegionLatency{}, Measurement{}, PrefsFields{}, PrefsSetInput{}, PrefsResult{},
		PrefsRepairInput{}, PrefsRepaired{}, LoginInput{}, LoginResult{}, LogoutInput{}, LogoutResult{}, PackageInstallInput{},
		PackageInstalled{}, PackageUpdateInput{}, PackageUpdated{}, PinInput{}, Pinned{}, UnpinInput{}, Unpinned{},
		CertGetInput{}, CertPair{},
	}
}

// TestTHWireTypesExist names every wire type, so a missing or renamed one fails the build.
func TestTHWireTypesExist(t *testing.T) {
	if n := len(thWireTypes()); n != 25 {
		t.Errorf("%d wire types listed, want 25", n)
	}
}

func TestTHWireJSON(t *testing.T) {
	handshake := time.Date(2026, 10, 31, 11, 59, 0, 0, time.UTC)
	yes, no := true, false
	none, exit := []string{}, ""
	tests := []struct {
		name string
		v    any
		want string
	}{
		{"status logged out", Status{Version: "1.104.1", BackendState: "NeedsLogin", Peers: []Peer{}, Health: []string{}},
			`{"version":"1.104.1","backendState":"NeedsLogin","self":null,"tailnet":"","magicDNSSuffix":"","preferredDerp":"","runSSH":false,"prefs":{},"peers":[],"health":[]}`},
		{"status running", Status{Version: "1.104.1", BackendState: "Running",
			Self:    &Node{ID: "n1", HostName: "web", DNSName: "web.example.ts.net", OS: "linux", Addresses: []string{"100.64.0.1"}, Tags: []string{}},
			Tailnet: "example.com", MagicDNSSuffix: "example.ts.net", PreferredDERP: "nyc", RunSSH: true, Prefs: PrefsFields{RunSSH: &yes}, Peers: []Peer{}, Health: []string{"a warning"}},
			`{"version":"1.104.1","backendState":"Running","self":{"id":"n1","hostName":"web","dnsName":"web.example.ts.net","os":"linux","addresses":["100.64.0.1"],"tags":[]},` +
				`"tailnet":"example.com","magicDNSSuffix":"example.ts.net","preferredDerp":"nyc","runSSH":true,"prefs":{"runSSH":true},"peers":[],"health":["a warning"]}`},
		{"peer", Peer{Node: Node{ID: "n2", HostName: "db", DNSName: "db.example.ts.net", OS: "linux", Addresses: []string{"100.64.0.2"}, Tags: []string{"tag:db"}},
			Online: true, Connection: "direct", Endpoint: "203.0.113.5:41641", ExitNodeOption: true, LastHandshake: &handshake},
			`{"id":"n2","hostName":"db","dnsName":"db.example.ts.net","os":"linux","addresses":["100.64.0.2"],"tags":["tag:db"],` +
				`"online":true,"connection":"direct","endpoint":"203.0.113.5:41641","region":"","exitNode":false,"exitNodeOption":true,"lastHandshake":"2026-10-31T11:59:00Z"}`},
		{"peer without a handshake", Peer{Node: Node{Addresses: []string{}, Tags: []string{}}, Connection: "none"},
			`{"id":"","hostName":"","dnsName":"","os":"","addresses":[],"tags":[],"online":false,"connection":"none","endpoint":"","region":"","exitNode":false,"exitNodeOption":false,"lastHandshake":null}`},
		{"latency", Latency{Regions: []RegionLatency{{RegionID: 1, Code: "nyc", Name: "New York City",
			STUN: Measurement{Available: true, MedianMs: 12.5}, HTTPS: Measurement{Reason: "timeout"}}}},
			`{"regions":[{"regionId":1,"code":"nyc","name":"New York City","stun":{"available":true,"medianMs":12.5,"reason":""},"https":{"available":false,"medianMs":0,"reason":"timeout"}}]}`},
		{"no prefs", PrefsFields{}, `{}`},
		{"a false bool is set", PrefsFields{WantRunning: &no}, `{"wantRunning":false}`},
		{"an empty route list is set", PrefsFields{AdvertiseRoutes: &none}, `{"advertiseRoutes":[]}`},
		{"an empty exit node is set", PrefsFields{ExitNode: &exit}, `{"exitNode":""}`},
		{"every pref", PrefsFields{WantRunning: &yes, AdvertiseRoutes: &none, ExitNode: &exit, AdvertiseExitNode: &no, AcceptRoutes: &yes, ShieldsUp: &no, RunSSH: &yes},
			`{"wantRunning":true,"advertiseRoutes":[],"exitNode":"","advertiseExitNode":false,"acceptRoutes":true,"shieldsUp":false,"runSSH":true}`},
		{"prefs set input flattens its embedded types", PrefsSetInput{Guarded: core.Guarded{Overrides: []string{"tailscale.link"}}, PrefsFields: PrefsFields{ShieldsUp: &yes}},
			`{"overrides":["tailscale.link"],"shieldsUp":true}`},
		{"prefs result", PrefsResult{Prefs: PrefsFields{RunSSH: &yes}}, `{"prefs":{"runSSH":true}}`},
		{"prefs repair input", PrefsRepairInput{Outcome: "keep"}, `{"outcome":"keep"}`},
		{"prefs repair input default", PrefsRepairInput{}, `{}`},
		{"prefs repaired", PrefsRepaired{RepairOutcome: "kept", Prefs: PrefsFields{ShieldsUp: &yes}}, `{"repairOutcome":"kept","prefs":{"shieldsUp":true}}`},
		{"login input", LoginInput{AuthKey: "tskey-auth-x", Hostname: "web", AdvertiseTags: []string{"tag:web"}, Relogin: true},
			`{"authKey":"tskey-auth-x","hostname":"web","advertiseTags":["tag:web"],"relogin":true}`},
		{"login input with only a key", LoginInput{AuthKey: "k"}, `{"authKey":"k"}`},
		{"login result", LoginResult{BackendState: "Running", DNSName: "web.example.ts.net", Addresses: []string{"100.64.0.7"}},
			`{"backendState":"Running","dnsName":"web.example.ts.net","addresses":["100.64.0.7"]}`},
		{"logout input", LogoutInput{}, `{}`},
		{"logout result", LogoutResult{BackendState: "NeedsLogin"}, `{"backendState":"NeedsLogin"}`},
		{"install input", PackageInstallInput{}, `{}`},
		{"installed", PackageInstalled{Version: "1.104.1"}, `{"version":"1.104.1"}`},
		{"update input", PackageUpdateInput{}, `{}`},
		{"updated", PackageUpdated{From: "1.102.0", To: "1.104.1"}, `{"from":"1.102.0","to":"1.104.1"}`},
		{"pin input", PinInput{Version: "1.102.0"}, `{"version":"1.102.0"}`},
		{"pinned", Pinned{Version: "1.102.0", PriorVersion: "1.104.1"}, `{"version":"1.102.0","priorVersion":"1.104.1","priorHeld":false}`},
		{"unpin input", UnpinInput{}, `{}`},
		{"unpinned", Unpinned{Version: "1.102.0", Held: true}, `{"version":"1.102.0","held":true}`},
		{"cert input", CertGetInput{}, `{}`},
		{"cert pair", CertPair{Name: "web.example.ts.net", CertPEM: "C", KeyPEM: "K", NotAfter: time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)},
			`{"name":"web.example.ts.net","certPem":"C","keyPem":"K","notAfter":"2026-12-01T00:00:00Z"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b, err := json.Marshal(tc.v)
			if err != nil {
				t.Fatal(err)
			}
			if string(b) != tc.want {
				t.Errorf("got  %s\nwant %s", b, tc.want)
			}
			back := reflect.New(reflect.TypeOf(tc.v))
			if err := json.Unmarshal(b, back.Interface()); err != nil {
				t.Fatal(err)
			}
			if again, _ := json.Marshal(back.Elem().Interface()); string(again) != tc.want {
				t.Errorf("round trip changed the document: %s", again)
			}
		})
	}
}

// TestTHWirePrefsSetInputDecodes: an input that carries the guard fields and a mask sets both parts.
func TestTHWirePrefsSetInputDecodes(t *testing.T) {
	var in PrefsSetInput
	if err := json.Unmarshal([]byte(`{"overrides":["tailscale.link"],"wantRunning":false,"exitNode":"100.64.0.9"}`), &in); err != nil {
		t.Fatal(err)
	}
	if len(in.Overrides) != 1 || in.WantRunning == nil || *in.WantRunning || in.ExitNode == nil || *in.ExitNode != "100.64.0.9" || in.ShieldsUp != nil {
		t.Errorf("decoded %+v", in)
	}
}

// TestTHWireDescriptions: every field of a wire type carries a description for the generated schema. An
// embedded type is checked through its own fields, and core.Guarded is the core's.
func TestTHWireDescriptions(t *testing.T) {
	var check func(rt reflect.Type)
	check = func(rt reflect.Type) {
		for i := range rt.NumField() {
			f := rt.Field(i)
			switch {
			case f.Anonymous && f.Type == reflect.TypeOf(core.Guarded{}):
			case f.Anonymous:
				check(f.Type)
			case f.Tag.Get("jsonschema_description") == "":
				t.Errorf("%s.%s has no jsonschema_description", rt.Name(), f.Name)
			}
		}
	}
	for _, v := range thWireTypes() {
		check(reflect.TypeOf(v))
	}
}

// thSchemaDef decodes a generated schema and returns the definition its root refers to, with every
// definition by name.
func thSchemaDef(t *testing.T, raw json.RawMessage) (def map[string]any, defs map[string]map[string]any) {
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
	return def, doc.Defs
}

func thProps(t *testing.T, def map[string]any) map[string]map[string]any {
	t.Helper()
	props, _ := def["properties"].(map[string]any)
	out := map[string]map[string]any{}
	for k, v := range props {
		out[k], _ = v.(map[string]any)
	}
	return out
}

func thRequired(def map[string]any) []string {
	var out []string
	list, _ := def["required"].([]any)
	for _, v := range list {
		out = append(out, v.(string))
	}
	return out
}

// TestTHWireSchemas: the schema generator accepts every input and output type, marks the secrets, and keeps
// the version pattern and the nullable members.
func TestTHWireSchemas(t *testing.T) {
	for _, v := range []any{PrefsSetInput{}, PrefsRepairInput{}, LoginInput{}, LogoutInput{}, PackageInstallInput{}, PackageUpdateInput{}, PinInput{}, UnpinInput{}, CertGetInput{}} {
		if _, err := schema.Input(reflect.TypeOf(v)); err != nil {
			t.Errorf("input schema of %T: %v", v, err)
		}
	}
	for _, v := range []any{Status{}, Latency{}, PrefsResult{}, PrefsRepaired{}, LoginResult{}, LogoutResult{}, PackageInstalled{}, PackageUpdated{}, Pinned{}, Unpinned{}, CertPair{}} {
		if _, err := schema.Output(reflect.TypeOf(v)); err != nil {
			t.Errorf("output schema of %T: %v", v, err)
		}
	}

	raw, err := schema.Input(reflect.TypeOf(LoginInput{}))
	if err != nil {
		t.Fatal(err)
	}
	def, _ := thSchemaDef(t, raw)
	props := thProps(t, def)
	if props["authKey"]["x-secret"] != true {
		t.Errorf("authKey is not marked secret: %v", props["authKey"])
	}
	if req := thRequired(def); !reflect.DeepEqual(req, []string{"authKey"}) {
		t.Errorf("login input requires %v, want only authKey", req)
	}

	raw, err = schema.Input(reflect.TypeOf(PrefsSetInput{}))
	if err != nil {
		t.Fatal(err)
	}
	def, _ = thSchemaDef(t, raw)
	props = thProps(t, def)
	for _, name := range []string{"wantRunning", "advertiseRoutes", "exitNode", "advertiseExitNode", "acceptRoutes", "shieldsUp", "runSSH", "overrides"} {
		if props[name] == nil {
			t.Errorf("prefs.set input lacks %s", name)
		}
	}
	if req := thRequired(def); len(req) != 0 {
		t.Errorf("prefs.set input requires %v, want every field optional", req)
	}

	raw, err = schema.Input(reflect.TypeOf(PinInput{}))
	if err != nil {
		t.Fatal(err)
	}
	def, _ = thSchemaDef(t, raw)
	if got := thProps(t, def)["version"]["pattern"]; got != `^[0-9][0-9A-Za-z.+~-]{0,63}$` {
		t.Errorf("version pattern %v", got)
	}

	raw, err = schema.Output(reflect.TypeOf(CertPair{}))
	if err != nil {
		t.Fatal(err)
	}
	def, _ = thSchemaDef(t, raw)
	if props := thProps(t, def); props["keyPem"]["x-secret"] != true || props["certPem"]["x-secret"] != nil {
		t.Errorf("cert pair secrets: keyPem %v, certPem %v", props["keyPem"], props["certPem"])
	}

	raw, err = schema.Output(reflect.TypeOf(Status{}))
	if err != nil {
		t.Fatal(err)
	}
	def, defs := thSchemaDef(t, raw)
	if b, _ := json.Marshal(thProps(t, def)["self"]); !strings.Contains(string(b), `"null"`) {
		t.Errorf("status.self is not nullable: %s", b)
	}
	if b, _ := json.Marshal(thProps(t, defs["Peer"])["lastHandshake"]); !strings.Contains(string(b), `"null"`) {
		t.Errorf("peer.lastHandshake is not nullable: %s", b)
	}
}
