package panel

import (
	"encoding/json"
	"errors"
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/schema"
)

// TestFdNames: the constants and the range are the values the spec fixes.
func TestFdNames(t *testing.T) {
	strs := map[string][2]string{
		"NetworkName":      {NetworkName, "darkpanel-panel"},
		"BridgeName":       {BridgeName, "dp-panel"},
		"BridgeOption":     {BridgeOption, "com.docker.network.bridge.name"},
		"Claimant":         {Claimant, "panel"},
		"ConnectorPrefix":  {ConnectorPrefix, "darkpanel-cloudflared-"},
		"ViaNPM":           {ViaNPM, "npm"},
		"ViaTunnel":        {ViaTunnel, "tunnel"},
		"StateOpen":        {StateOpen, "open"},
		"StateRevoked":     {StateRevoked, "revoked"},
		"StateNeedsRepair": {StateNeedsRepair, "needs_repair"},
		"ActionAdd":        {ActionAdd, "panel.exposure.add"},
		"ActionRemove":     {ActionRemove, "panel.exposure.remove"},
		"ActionStatus":     {ActionStatus, "panel.exposure.status"},
		"ActionNetCreate":  {ActionNetCreate, "panel.network.create"},
		"ActionNetRemove":  {ActionNetRemove, "panel.network.remove"},
		"ActionConnCheck":  {ActionConnCheck, "panel.connector.check"},
		"ActionOpen":       {ActionOpen, "panel.listener.open"},
		"ActionRevoke":     {ActionRevoke, "panel.listener.revoke"},
	}
	for name, v := range strs {
		if v[0] != v[1] {
			t.Errorf("%s = %q, want %q", name, v[0], v[1])
		}
	}
	if DefaultPort != 9443 {
		t.Errorf("DefaultPort = %d, want 9443", DefaultPort)
	}
	if PanelRange != netip.MustParsePrefix("172.31.255.0/24") {
		t.Errorf("PanelRange = %s, want 172.31.255.0/24", PanelRange)
	}
}

// TestFdWireJSON: each wire type has the JSON names the contract fixes, optional fields drop out when empty, and the
// document survives a round trip.
func TestFdWireJSON(t *testing.T) {
	created := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	view := ExposureView{
		ID: "c1", Via: ViaNPM, Hostname: "panel.example.org", HostID: "h1", Subnet: netip.MustParsePrefix("172.31.255.248/29"),
		Gateway: netip.MustParseAddr("172.31.255.249"), Proxy: netip.MustParseAddr("172.31.255.250"), Port: 9443,
		State: StateOpen, ListenerID: "l1", CreatedAt: created,
	}
	const viewJSON = `{"id":"c1","via":"npm","hostname":"panel.example.org","hostId":"h1","subnet":"172.31.255.248/29",` +
		`"gateway":"172.31.255.249","proxy":"172.31.255.250","port":9443,"state":"open","listenerId":"l1","createdAt":"2026-11-04T12:00:00Z"}`
	tests := []struct {
		name string
		v    any
		want string
	}{
		{"expose input", ExposeInput{Via: ViaNPM, Hostname: "panel.example.org", HostID: "h1"},
			`{"via":"npm","hostname":"panel.example.org","hostId":"h1"}`},
		{"expose input in full", ExposeInput{Via: ViaTunnel, Hostname: "panel.example.org", HostID: "h1", Tunnel: "0123abcd-0123-4abc-8def-0123456789ab", Port: 9500},
			`{"via":"tunnel","hostname":"panel.example.org","hostId":"h1","tunnel":"0123abcd-0123-4abc-8def-0123456789ab","port":9500}`},
		{"expose output", ExposeOutput{ExposureID: "c1", Via: ViaNPM, Hostname: "panel.example.org", HostID: "h1",
			Subnet: view.Subnet, Gateway: view.Gateway, Proxy: view.Proxy, Port: 9443},
			`{"exposureId":"c1","via":"npm","hostname":"panel.example.org","hostId":"h1","subnet":"172.31.255.248/29",` +
				`"gateway":"172.31.255.249","proxy":"172.31.255.250","port":9443}`},
		{"remove input", RemoveInput{}, `{}`},
		{"remove output", RemoveOutput{ExposureID: "c1", Hostname: "panel.example.org"}, `{"exposureId":"c1","hostname":"panel.example.org"}`},
		{"network create input", NetworkCreateInput{}, `{}`},
		{"network view", NetworkView{Network: NetworkName, ID: "n1", Subnet: view.Subnet, Gateway: view.Gateway, Proxy: view.Proxy},
			`{"network":"darkpanel-panel","id":"n1","subnet":"172.31.255.248/29","gateway":"172.31.255.249","proxy":"172.31.255.250"}`},
		{"network remove input", NetworkRemoveInput{}, `{}`},
		{"network remove output", NetworkRemoveOutput{Network: NetworkName, Removed: true}, `{"network":"darkpanel-panel","removed":true}`},
		{"connector check input", ConnectorCheckInput{Tunnel: "t1"}, `{"tunnel":"t1"}`},
		{"connector check output", ConnectorCheckOutput{Container: "darkpanel-cloudflared-t1", State: "running"},
			`{"container":"darkpanel-cloudflared-t1","state":"running"}`},
		{"listener open input", ListenerOpenInput{Via: ViaNPM, Hostname: "panel.example.org", HostID: "h1", Subnet: view.Subnet,
			Gateway: view.Gateway, Proxy: view.Proxy, Port: 9443},
			`{"via":"npm","hostname":"panel.example.org","hostId":"h1","subnet":"172.31.255.248/29","gateway":"172.31.255.249","proxy":"172.31.255.250","port":9443}`},
		{"listener open output", ListenerOpenOutput{ExposureID: "c1", ListenerID: "l1", RuleID: "R1", Address: netip.MustParseAddrPort("172.31.255.249:9443")},
			`{"exposureId":"c1","listenerId":"l1","ruleId":"R1","address":"172.31.255.249:9443"}`},
		{"listener revoke input", ListenerRevokeInput{}, `{}`},
		{"listener revoke output", ListenerRevokeOutput{ExposureID: "c1", ListenerID: "l1", RuleID: "R1", Revoked: true},
			`{"exposureId":"c1","listenerId":"l1","ruleId":"R1","revoked":true}`},
		{"status input", StatusInput{}, `{}`},
		{"status output without an exposure", StatusOutput{}, `{"exposed":false}`},
		{"status output with an exposure", StatusOutput{Exposed: true, Exposure: &view}, `{"exposed":true,"exposure":` + viewJSON + `}`},
		{"exposure view of a tunnel", ExposureView{ID: "c2", Via: ViaTunnel, Tunnel: "t1", State: StateNeedsRepair},
			`{"id":"c2","via":"tunnel","hostname":"","hostId":"","tunnel":"t1","subnet":"","gateway":"","proxy":"","port":0,` +
				`"state":"needs_repair","listenerId":"","createdAt":"0001-01-01T00:00:00Z"}`},
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

// TestFdWireSchemas: every input and output type reflects into a schema, and the schema of the exposure input lists the
// two routes.
func TestFdWireSchemas(t *testing.T) {
	inputs := []any{ExposeInput{}, RemoveInput{}, NetworkCreateInput{}, NetworkRemoveInput{}, ConnectorCheckInput{}, ListenerOpenInput{},
		ListenerRevokeInput{}, StatusInput{}}
	for _, v := range inputs {
		if _, err := schema.Input(reflect.TypeOf(v)); err != nil {
			t.Errorf("input %T: %v", v, err)
		}
	}
	outputs := []any{ExposeOutput{}, RemoveOutput{}, NetworkView{}, NetworkRemoveOutput{}, ConnectorCheckOutput{}, ListenerOpenOutput{},
		ListenerRevokeOutput{}, StatusOutput{}}
	for _, v := range outputs {
		if _, err := schema.Output(reflect.TypeOf(v)); err != nil {
			t.Errorf("output %T: %v", v, err)
		}
	}
	b, err := schema.Input(reflect.TypeFor[ExposeInput]())
	if err != nil || !strings.Contains(string(b), `"enum":["npm","tunnel"]`) {
		t.Errorf("ExposeInput schema %s (error %v), want the via enum npm, tunnel", b, err)
	}
	b, err = schema.Output(reflect.TypeFor[StatusOutput]())
	if err != nil || !strings.Contains(string(b), `"null"`) {
		t.Errorf("StatusOutput schema %s (error %v), want exposure nullable", b, err)
	}
}

// TestFdValidateExpose: ValidateExpose accepts the inputs the rules allow and refuses every other with
// request.invalid_input naming the JSON pointer of each bad field.
func TestFdValidateExpose(t *testing.T) {
	const tunnel = "0123abcd-0123-4abc-8def-0123456789ab"
	npm := ExposeInput{Via: ViaNPM, Hostname: "panel.example.org", HostID: "h1"}
	tun := ExposeInput{Via: ViaTunnel, Hostname: "panel.example.org", HostID: "h1", Tunnel: tunnel}
	long := strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 61) + ".org"
	tests := []struct {
		name   string
		in     ExposeInput
		mod    func(in *ExposeInput)
		fields []string // the JSON pointers refused, sorted; nil when the input is valid
	}{
		{name: "npm", in: npm},
		{name: "tunnel", in: tun},
		{name: "port 1024", in: npm, mod: func(in *ExposeInput) { in.Port = 1024 }},
		{name: "port 65535", in: npm, mod: func(in *ExposeInput) { in.Port = 65535 }},
		{name: "253 characters", in: npm, mod: func(in *ExposeInput) { in.Hostname = long[:253] }},
		{name: "unknown via", in: npm, mod: func(in *ExposeInput) { in.Via = "ssh" }, fields: []string{"/via"}},
		{name: "empty via", in: npm, mod: func(in *ExposeInput) { in.Via = "" }, fields: []string{"/via"}},
		{name: "empty hostname", in: npm, mod: func(in *ExposeInput) { in.Hostname = "" }, fields: []string{"/hostname"}},
		{name: "upper-case hostname", in: npm, mod: func(in *ExposeInput) { in.Hostname = "Panel.example.org" }, fields: []string{"/hostname"}},
		{name: "one label", in: npm, mod: func(in *ExposeInput) { in.Hostname = "panel" }, fields: []string{"/hostname"}},
		{name: "wildcard", in: npm, mod: func(in *ExposeInput) { in.Hostname = "*.example.org" }, fields: []string{"/hostname"}},
		{name: "trailing dot", in: npm, mod: func(in *ExposeInput) { in.Hostname = "panel.example.org." }, fields: []string{"/hostname"}},
		{name: "leading hyphen", in: npm, mod: func(in *ExposeInput) { in.Hostname = "-panel.example.org" }, fields: []string{"/hostname"}},
		{name: "underscore", in: npm, mod: func(in *ExposeInput) { in.Hostname = "pa_nel.example.org" }, fields: []string{"/hostname"}},
		{name: "IP address", in: npm, mod: func(in *ExposeInput) { in.Hostname = "192.168.1.5" }, fields: []string{"/hostname"}},
		{name: "254 characters", in: npm, mod: func(in *ExposeInput) { in.Hostname = long[:253] + "a" }, fields: []string{"/hostname"}},
		{name: "empty host", in: npm, mod: func(in *ExposeInput) { in.HostID = "" }, fields: []string{"/hostId"}},
		{name: "tunnel for npm", in: npm, mod: func(in *ExposeInput) { in.Tunnel = tunnel }, fields: []string{"/tunnel"}},
		{name: "tunnel missing", in: tun, mod: func(in *ExposeInput) { in.Tunnel = "" }, fields: []string{"/tunnel"}},
		{name: "tunnel not a UUID", in: tun, mod: func(in *ExposeInput) { in.Tunnel = "my-tunnel" }, fields: []string{"/tunnel"}},
		{name: "upper-case tunnel", in: tun, mod: func(in *ExposeInput) { in.Tunnel = strings.ToUpper(tunnel) }, fields: []string{"/tunnel"}},
		{name: "port 1023", in: npm, mod: func(in *ExposeInput) { in.Port = 1023 }, fields: []string{"/port"}},
		{name: "port 65536", in: npm, mod: func(in *ExposeInput) { in.Port = 65536 }, fields: []string{"/port"}},
		{name: "negative port", in: npm, mod: func(in *ExposeInput) { in.Port = -1 }, fields: []string{"/port"}},
		{name: "several fields", in: tun, mod: func(in *ExposeInput) { in.Hostname, in.Port, in.Tunnel = "x", 80, "" },
			fields: []string{"/hostname", "/port", "/tunnel"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := tt.in
			if tt.mod != nil {
				tt.mod(&in)
			}
			err := ValidateExpose(in)
			if tt.fields == nil {
				if err != nil {
					t.Fatalf("ValidateExpose(%+v) = %v, want nil", in, err)
				}
				return
			}
			var coded *errcode.Error
			if !errors.As(err, &coded) || coded.Code != errcode.InvalidInput {
				t.Fatalf("ValidateExpose(%+v) = %v, want request.invalid_input", in, err)
			}
			var got []string
			for _, f := range coded.Fields {
				if f.Message == "" {
					t.Errorf("field %s has no message", f.Path)
				}
				got = append(got, f.Path)
			}
			slices.Sort(got)
			if !slices.Equal(got, tt.fields) {
				t.Errorf("fields %v, want %v", got, tt.fields)
			}
		})
	}
}
