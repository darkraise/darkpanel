package tailnet

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/tailnet/fake"
)

const exPolicy = `// Darkpanel tailnet policy.
{
	// who may reach what
	"grants": [
		{"src": ["*"], "dst": ["*"], "ip": ["*"]},
	],
}
`

func TestExReadPolicyAnswersWhatTheActionAnswers(t *testing.T) {
	srv := fake.New(t)
	etag := srv.SetPolicy(exPolicy)
	h := newTNHost(t)
	acConnect(t, h, srv)
	n := tnWith(srv)
	got, err := n.readPolicy(context.Background(), h.env(""))
	if err != nil {
		t.Fatal(err)
	}
	if want := (Policy{Text: exPolicy, ETag: etag}); got != want {
		t.Errorf("readPolicy = %+v, want the stored text with its comments and ETag %s", got, etag)
	}
	raw, err := n.policyGet().Handler.Read(context.Background(), h.env(""), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var viaAction Policy
	if err := json.Unmarshal(raw, &viaAction); err != nil || viaAction != got {
		t.Errorf("tailnet.policy.get answered %+v (%v), want %+v", viaAction, err, got)
	}
}

func TestExListDevicesAnswersWhatTheActionAnswers(t *testing.T) {
	srv := fake.New(t)
	h := newTNHost(t)
	acConnect(t, h, srv)
	web := srv.AddDevice(fake.Device{Name: "web-1.example.ts.net", Hostname: "web-1", Addresses: []string{"100.64.0.1"},
		Tags: []string{"tag:server"}, Authorized: true})
	gw := srv.AddDevice(fake.Device{Name: "gw.example.ts.net", Hostname: "gw", Addresses: []string{"100.64.0.2"},
		AdvertisedRoutes: []string{"::/0", "0.0.0.0/0"}})
	bare := srv.AddDevice(fake.Device{Name: "bare.example.ts.net", Hostname: "bare"})
	n := tnWith(srv)
	got, err := n.listDevices(context.Background(), h.env(""))
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, d := range got {
		ids = append(ids, d.ID)
	}
	if want := []string{bare, gw, web}; !slices.Equal(ids, want) {
		t.Fatalf("listDevices = %v, want %v (by name)", ids, want)
	}
	if b := got[0]; b.Addresses == nil || b.Tags == nil || b.AdvertisedRoutes == nil || b.EnabledRoutes == nil {
		t.Errorf("bare device %+v, want empty lists, never nil", b)
	}
	if !got[1].ExitNode || got[1].ExitNodeEnabled {
		t.Errorf("gateway %+v, want an exit node that is not approved", got[1])
	}
	raw, err := n.deviceList().Handler.Read(context.Background(), h.env(""), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var viaAction DeviceList
	if err := json.Unmarshal(raw, &viaAction); err != nil || !reflect.DeepEqual(viaAction.Devices, got) {
		t.Errorf("tailnet.device.list answered %+v (%v), want %+v", viaAction.Devices, err, got)
	}
}

func TestExReadsNeedAConnectedAccount(t *testing.T) {
	srv := fake.New(t)
	h := newTNHost(t)
	ctx := context.Background()
	n := tnWith(srv)
	_, errPolicy := n.readPolicy(ctx, h.env(""))
	_, errDevices := n.listDevices(ctx, h.env(""))
	_, errSharedPolicy := ReadPolicy(ctx, h.env(""))
	_, errSharedDevices := ListDevices(ctx, h.env(""))
	for name, err := range map[string]error{
		"readPolicy": errPolicy, "listDevices": errDevices, "ReadPolicy": errSharedPolicy, "ListDevices": errSharedDevices,
	} {
		if got := errcode.From(err).Code; got != errcode.CloudNotConnected {
			t.Errorf("%s without an account = %v, want %s", name, err, errcode.CloudNotConnected)
		}
	}
	if n := len(srv.Cloud().Requests()); n != 0 {
		t.Errorf("%d provider requests without an account, want none", n)
	}
}

func TestExConnectedReadsOnlyTheAccountRow(t *testing.T) {
	srv := fake.New(t)
	h := newTNHost(t)
	ctx := context.Background()
	if ok, err := Connected(ctx, h.db); err != nil || ok {
		t.Fatalf("Connected without an account = %v, %v, want false", ok, err)
	}
	acConnect(t, h, srv)
	if ok, err := Connected(ctx, h.db); err != nil || !ok {
		t.Fatalf("Connected with an account = %v, %v, want true", ok, err)
	}
	if n := len(srv.Cloud().Requests()); n != 0 {
		t.Errorf("%d provider requests from Connected, want none", n)
	}
}
