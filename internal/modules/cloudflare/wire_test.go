package cloudflare

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/cloud"
)

func TestAPWireShapesKeepTheirJSONNames(t *testing.T) {
	at := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		v    any
		want string
	}{
		{"hostname view", HostnameView{Hostname: "app.example.com", Tunnel: "t1", Service: "http://web:80", Phase: "active", Health: "ok"},
			`{"hostname":"app.example.com","tunnel":"t1","service":"http://web:80","phase":"active","health":"ok"}`},
		{"hostname view with a record and a check", HostnameView{Hostname: "app.example.com", Tunnel: "t1", Service: "http://web:80", RecordID: "r1", Phase: "active", Health: "ok", CheckedAt: &at},
			`{"hostname":"app.example.com","tunnel":"t1","service":"http://web:80","recordId":"r1","phase":"active","health":"ok","checkedAt":"2026-10-30T12:00:00Z"}`},
		{"tunnel view", TunnelView{ID: "t1", Name: "darkpanel-c1", Status: "healthy", Connections: []ConnectionView{{ID: "k1", Colo: "FRA", OriginIP: "203.0.113.5", OpenedAt: at, Version: "2026.9.0"}}, Drift: true, Hostnames: 2, CreatedAt: at},
			`{"id":"t1","name":"darkpanel-c1","status":"healthy","connections":[{"id":"k1","colo":"FRA","originIp":"203.0.113.5","openedAt":"2026-10-30T12:00:00Z","version":"2026.9.0"}],"drift":true,"hostnames":2,"createdAt":"2026-10-30T12:00:00Z"}`},
		{"tunnel list", TunnelList{Tunnels: []TunnelView{}}, `{"tunnels":[]}`},
		{"hostname list", HostnameList{Hostnames: []HostnameView{}}, `{"hostnames":[]}`},
		{"account view", AccountView{AccountID: "a1", Capabilities: []cloud.Capability{{Name: "tunnels", State: "ok"}}, ConnectedAt: at},
			`{"accountId":"a1","capabilities":[{"name":"tunnels","state":"ok"}],"connectedAt":"2026-10-30T12:00:00Z"}`},
		{"account status without an account", AccountStatus{}, `{"connected":false}`},
		{"disconnect input", DisconnectInput{AbandonCleanup: true}, `{"abandonCleanup":true}`},
		{"disconnect input by default", DisconnectInput{}, `{}`},
		{"disconnected", Disconnected{Abandoned: []string{"hostname app.example.com"}}, `{"abandoned":["hostname app.example.com"]}`},
		{"hostname add with noTLSVerify", HostnameAddInput{Tunnel: "t1", Hostname: "app.example.com", Service: "https://web:443", NoTLSVerify: true},
			`{"tunnel":"t1","hostname":"app.example.com","service":"https://web:443","noTLSVerify":true}`},
		{"hostname add without noTLSVerify", HostnameAddInput{Tunnel: "t1", Hostname: "app.example.com", Service: "http://web:80"},
			`{"tunnel":"t1","hostname":"app.example.com","service":"http://web:80"}`},
		{"hostname input", HostnameInput{Hostname: "app.example.com"}, `{"hostname":"app.example.com"}`},
		{"tunnel input", TunnelInput{Tunnel: "darkpanel-c1"}, `{"tunnel":"darkpanel-c1"}`},
		{"tunnel ref", TunnelRef{ID: "t1", Name: "darkpanel-c1"}, `{"id":"t1","name":"darkpanel-c1"}`},
		{"tunnel token", TunnelToken{Token: "tok"}, `{"token":"tok"}`},
		{"connect input", ConnectInput{AccountID: "a1", APIToken: "tok"}, `{"accountId":"a1","apiToken":"tok"}`},
		{"empty inputs", []any{TunnelCreateInput{}, TunnelListInput{}, HostnameListInput{}, AccountGetInput{}}, `[{},{},{},{}]`},
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
			if _, ok := tc.v.([]any); ok {
				return
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

func TestAPSecretFieldsAreTagged(t *testing.T) {
	for _, f := range []struct {
		typ   reflect.Type
		field string
	}{
		{reflect.TypeFor[ConnectInput](), "APIToken"},
		{reflect.TypeFor[TunnelToken](), "Token"},
	} {
		sf, ok := f.typ.FieldByName(f.field)
		if !ok || sf.Tag.Get("secret") != "true" {
			t.Errorf("%s.%s tag %q, want secret:\"true\"", f.typ.Name(), f.field, sf.Tag)
		}
	}
}
