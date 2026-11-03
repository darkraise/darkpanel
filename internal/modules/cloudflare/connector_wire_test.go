package cloudflare

import (
	"encoding/json"
	"net/netip"
	"reflect"
	"testing"

	"github.com/darkraise/darkpanel/internal/core/schema"
)

func TestCwWireShapesKeepTheirJSONNames(t *testing.T) {
	const tunnel = "9f1c2e3a-4b5d-4c6e-8f70-1a2b3c4d5e6f"
	conn := Connector{Tunnel: tunnel, Container: "darkpanel-cloudflared-" + tunnel, ContainerID: "c0ffee", State: "running"}
	const connJSON = `{"tunnel":"` + tunnel + `","container":"darkpanel-cloudflared-` + tunnel + `","containerId":"c0ffee","state":"running"}`
	tests := []struct {
		name string
		v    any
		want string
	}{
		{"run input", ConnectorRunInput{Tunnel: tunnel, Token: "tok"}, `{"tunnel":"` + tunnel + `","token":"tok"}`},
		{"connector", conn, connJSON},
		{"stop input", ConnectorStopInput{Tunnel: tunnel}, `{"tunnel":"` + tunnel + `"}`},
		{"stopped", ConnectorStopped{Tunnel: tunnel, Removed: true}, `{"tunnel":"` + tunnel + `","removed":true}`},
		{"stopped with nothing left", ConnectorStopped{Tunnel: tunnel}, `{"tunnel":"` + tunnel + `","removed":false}`},
		{"list input", ConnectorListInput{}, `{}`},
		{"empty list", ConnectorList{Connectors: []Connector{}}, `{"connectors":[]}`},
		{"list", ConnectorList{Connectors: []Connector{conn}}, `{"connectors":[` + connJSON + `]}`},
		{"attach input", NetworkAttachInput{Tunnel: tunnel, Network: "web", Claimant: "expose:app.example.com"},
			`{"tunnel":"` + tunnel + `","network":"web","claimant":"expose:app.example.com"}`},
		{"attach input with a target and an address",
			NetworkAttachInput{Tunnel: tunnel, Network: "web", Claimant: "panel", Target: "app", Address: netip.MustParseAddr("172.31.255.2")},
			`{"tunnel":"` + tunnel + `","network":"web","claimant":"panel","target":"app","address":"172.31.255.2"}`},
		{"attachment", NetworkAttachment{Tunnel: tunnel, Network: "web", Claimant: "c", Preexisting: true, Attached: true},
			`{"tunnel":"` + tunnel + `","network":"web","claimant":"c","preexisting":true,"attached":true}`},
		{"detach input", NetworkDetachInput{Tunnel: tunnel, Claimant: "c"}, `{"tunnel":"` + tunnel + `","network":"","claimant":"c"}`},
		{"deploy input without a tunnel", ConnectorDeployInput{HostID: "h1"}, `{"hostId":"h1"}`},
		{"deploy input with a tunnel", ConnectorDeployInput{HostID: "h1", Tunnel: "darkpanel-c1"}, `{"hostId":"h1","tunnel":"darkpanel-c1"}`},
		{"deploy output", ConnectorDeployOutput{Tunnel: tunnel, Created: true, Connector: conn},
			`{"tunnel":"` + tunnel + `","created":true,"connector":` + connJSON + `}`},
		{"expose input", ExposeInput{HostID: "h1", Tunnel: "darkpanel-c1", Container: "app", Port: 8080, Network: "web", Hostname: "app.example.com"},
			`{"hostId":"h1","tunnel":"darkpanel-c1","container":"app","port":8080,"network":"web","hostname":"app.example.com"}`},
		{"expose output", ExposeOutput{Hostname: "app.example.com", Service: "http://app:8080", Network: "web"},
			`{"hostname":"app.example.com","service":"http://app:8080","network":"web"}`},
		{"expose remove input", ExposeRemoveInput{HostID: "h1", Hostname: "app.example.com"}, `{"hostId":"h1","hostname":"app.example.com"}`},
		{"expose remove output", ExposeRemoveOutput{Hostname: "app.example.com"}, `{"hostname":"app.example.com"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := json.Marshal(tc.v)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Errorf("JSON %s, want %s", got, tc.want)
			}
			back := reflect.New(reflect.TypeOf(tc.v))
			if err := json.Unmarshal([]byte(tc.want), back.Interface()); err != nil {
				t.Fatal(err)
			}
			again, err := json.Marshal(back.Elem().Interface())
			if err != nil || string(again) != tc.want {
				t.Errorf("round trip %s (%v), want %s", again, err, tc.want)
			}
		})
	}
}

// cwProperty returns the schema keywords of one property of a type's schema, which is a reference to the one
// definition it holds.
func cwProperty(t *testing.T, doc json.RawMessage, property string) map[string]any {
	t.Helper()
	var s struct {
		Ref  string `json:"$ref"`
		Defs map[string]struct {
			Properties map[string]map[string]any `json:"properties"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(doc, &s); err != nil {
		t.Fatal(err)
	}
	for _, def := range s.Defs {
		if p, ok := def.Properties[property]; ok {
			return p
		}
	}
	t.Fatalf("the schema has no property %q: %s", property, doc)
	return nil
}

// TestCwTokenIsASecretAndPortIsBounded: the token a connector run takes is marked secret in the struct tag and in the
// schema, so the audit log, the snapshots and the CLI never show it, and the port of an exposure is bounded in its
// schema.
func TestCwTokenIsASecretAndPortIsBounded(t *testing.T) {
	sf, ok := reflect.TypeFor[ConnectorRunInput]().FieldByName("Token")
	if !ok || sf.Tag.Get("secret") != "true" {
		t.Errorf("ConnectorRunInput.Token tag %q, want secret:\"true\"", sf.Tag)
	}
	if sf, ok := reflect.TypeFor[ConnectorRunInput]().FieldByName("Tunnel"); !ok || sf.Tag.Get("secret") != "" {
		t.Errorf("ConnectorRunInput.Tunnel tag %q, want no secret tag", sf.Tag)
	}
	in, err := schema.Input(reflect.TypeFor[ConnectorRunInput]())
	if err != nil {
		t.Fatal(err)
	}
	if cwProperty(t, in, "token")["x-secret"] != true || cwProperty(t, in, "tunnel")["x-secret"] != nil {
		t.Errorf("connector run input schema: want token alone marked secret: %s", in)
	}
	expose, err := schema.Input(reflect.TypeFor[ExposeInput]())
	if err != nil {
		t.Fatal(err)
	}
	port := cwProperty(t, expose, "port")
	if port["minimum"] != float64(1) || port["maximum"] != float64(65535) {
		t.Errorf("expose input port schema %v, want minimum 1 and maximum 65535", port)
	}
}
