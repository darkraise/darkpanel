package tailnet

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/cloud"
	"github.com/darkraise/darkpanel/internal/modules/tailnet/policy"
)

func TestAPWireShapesKeepTheirJSONMembers(t *testing.T) {
	tests := []struct {
		name string
		v    any
		want string
	}{
		{"connect input", ConnectInput{Tailnet: "example.com", ClientID: "c", ClientSecret: "s", TagsAllowed: []string{"tag:a"}},
			`{"tailnet":"example.com","clientId":"c","clientSecret":"s","tagsAllowed":["tag:a"]}`},
		{"account view", AccountView{Tailnet: "example.com", ClientID: "c", TagsAllowed: []string{"tag:a"},
			Capabilities: []cloud.Capability{{Name: "devices", State: "ok"}}, ConnectedAt: time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)},
			`{"tailnet":"example.com","clientId":"c","tagsAllowed":["tag:a"],"capabilities":[{"name":"devices","state":"ok"}],"connectedAt":"2026-10-30T12:00:00Z"}`},
		{"disconnect input", DisconnectInput{AbandonCleanup: true}, `{"abandonCleanup":true}`},
		{"disconnect input default", DisconnectInput{}, `{}`},
		{"disconnected", Disconnected{Abandoned: []string{"k1"}}, `{"abandoned":["k1"]}`},
		{"account status", AccountStatus{}, `{"connected":false}`},
		{"device", Device{}, `{"id":"","name":"","hostname":"","os":"","user":"","addresses":null,"tags":null,"authorized":false,` +
			`"keyExpiryDisabled":false,"advertisedRoutes":null,"enabledRoutes":null,"exitNode":false,"exitNodeEnabled":false}`},
		{"device routes", DeviceRoutes{Advertised: []string{"a"}, Enabled: []string{}}, `{"advertisedRoutes":["a"],"enabledRoutes":[]}`},
		{"set routes input", SetRoutesInput{Device: "n1", Routes: []string{"a"}}, `{"device":"n1","routes":["a"]}`},
		{"device ref", DeviceRef{ID: "n1", Name: "web"}, `{"id":"n1","name":"web"}`},
		{"auth key input", AuthKeyInput{Tags: []string{"tag:a"}}, `{"tags":["tag:a"]}`},
		{"auth key input full", AuthKeyInput{Tags: []string{"tag:a"}, Reusable: true, Ephemeral: true, Preauthorized: true, ExpirySeconds: 60},
			`{"tags":["tag:a"],"reusable":true,"ephemeral":true,"preauthorized":true,"expirySeconds":60}`},
		{"created key", CreatedKey{ID: "k", Key: "s", Expires: time.Date(2026, 10, 30, 13, 0, 0, 0, time.UTC), Description: "d"},
			`{"id":"k","key":"s","expires":"2026-10-30T13:00:00Z","description":"d"}`},
		{"auth key row", AuthKeyRow{Description: "d", Kind: "change", OwnerID: "o", State: "live"},
			`{"description":"d","kind":"change","ownerId":"o","state":"live","requestedAt":"0001-01-01T00:00:00Z","expires":"0001-01-01T00:00:00Z"}`},
		{"policy", Policy{Text: "t", ETag: "e"}, `{"text":"t","etag":"e"}`},
		{"policy set input", PolicySetInput{Text: "t"}, `{"text":"t"}`},
		{"policy patch input", PolicyPatchInput{Edits: []policy.Edit{{Op: "set", Path: "/a", Value: json.RawMessage("1")}}},
			`{"edits":[{"op":"set","path":"/a","value":1}]}`},
		{"policy written", PolicyWritten{ETag: "e"}, `{"etag":"e"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := json.Marshal(tc.v)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Errorf("JSON\n got %s\nwant %s", got, tc.want)
			}
			back := reflect.New(reflect.TypeOf(tc.v))
			if err := json.Unmarshal(got, back.Interface()); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if again, _ := json.Marshal(back.Elem().Interface()); string(again) != tc.want {
				t.Errorf("round trip\n got %s\nwant %s", again, tc.want)
			}
		})
	}
}

func TestAPSecretFieldsAreTagged(t *testing.T) {
	for _, f := range []struct {
		typ   reflect.Type
		field string
	}{
		{reflect.TypeFor[ConnectInput](), "ClientSecret"},
		{reflect.TypeFor[CreatedKey](), "Key"},
	} {
		sf, ok := f.typ.FieldByName(f.field)
		if !ok || sf.Tag.Get("secret") != "true" {
			t.Errorf("%s.%s lacks the secret:\"true\" tag", f.typ, f.field)
		}
	}
}
