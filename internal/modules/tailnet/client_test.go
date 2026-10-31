package tailnet

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/cloud/cloudfake"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/tailnet/fake"
)

// tnWith points a tn at the fake server, with a sleep that returns at once so
// that retries do not wait.
func tnWith(srv *fake.Server) tn {
	return tn{
		d: deps{
			baseURL: srv.URL(),
			http:    srv.Client(),
			sleep:   func(context.Context, time.Duration) error { return nil },
		},
		tokens: newTokenCache(),
	}
}

func apClock() *coretest.FakeClock {
	return coretest.NewFakeClock(time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC))
}

// apAPI builds an api for the fake's tailnet with the given client secret.
func apAPI(t *testing.T, tnv tn, secret string, clock core.Clock) *api {
	t.Helper()
	a, err := tnv.newAPI(account{ID: "acc1", Tailnet: fake.Tailnet, ClientID: fake.ClientID}, []byte(secret), clock)
	if err != nil {
		t.Fatalf("newAPI: %v", err)
	}
	return a
}

// apFake returns a fake server and an api for it.
func apFake(t *testing.T) (*fake.Server, *api) {
	t.Helper()
	srv := fake.New(t)
	return srv, apAPI(t, tnWith(srv), fake.ClientSecret, apClock())
}

func apTailnet(p string) string { return "/api/v2/tailnet/" + fake.Tailnet + p }

func apCode(t *testing.T, err error) errcode.Code {
	t.Helper()
	if err == nil {
		t.Fatal("no error")
	}
	return errcode.From(err).Code
}

func apRequests(srv *fake.Server, method, path string) []cloudfake.Request {
	var out []cloudfake.Request
	for _, r := range srv.Cloud().Requests() {
		if r.Method == method && r.Path == path {
			out = append(out, r)
		}
	}
	return out
}

// apAnswer queues one answer in place of the fake's handler.
func apAnswer(srv *fake.Server, method, path string, status int, body string) {
	srv.Cloud().Fail(method, path, cloudfake.Fault{Status: status, Body: body})
}

func apSameJSON(t *testing.T, got []byte, want string) {
	t.Helper()
	var g, w any
	if len(got) == 0 {
		g = nil
	} else if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("request body %q is not JSON: %v", got, err)
	}
	if want != "" {
		if err := json.Unmarshal([]byte(want), &w); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(g, w) {
		t.Errorf("request body %s, want %s", got, want)
	}
}

const apDevicesJSON = `{"devices":[
 {"id":"1111","nodeId":"nAAA1CNTRL","name":"web-1.tail1234.ts.net","hostname":"web-1","os":"linux",
  "user":"alice@example.com","addresses":["100.64.0.1","fd7a:115c:a1e0::1"],"tags":["tag:server"],
  "authorized":true,"lastSeen":"2026-10-29T10:00:00Z","expires":"2027-04-27T10:00:00Z","keyExpiryDisabled":false,
  "advertisedRoutes":["10.0.0.0/24","0.0.0.0/0","::/0"],"enabledRoutes":["10.0.0.0/24","0.0.0.0/0","::/0"]},
 {"id":"2222","nodeId":"nBBB2CNTRL","name":"db-1.tail1234.ts.net","hostname":"db-1","os":"linux",
  "user":"tagged-devices","addresses":["100.64.0.2"],"authorized":false,"lastSeen":"2026-10-28T10:00:00Z",
  "expires":"0001-01-01T00:00:00Z","keyExpiryDisabled":true,
  "advertisedRoutes":["0.0.0.0/0","::/0"],"enabledRoutes":["0.0.0.0/0"]}]}`

func apDeviceOne() Device {
	return Device{
		ID: "nAAA1CNTRL", Name: "web-1.tail1234.ts.net", Hostname: "web-1", OS: "linux", User: "alice@example.com",
		Addresses: []string{"100.64.0.1", "fd7a:115c:a1e0::1"}, Tags: []string{"tag:server"}, Authorized: true,
		LastSeen: "2026-10-29T10:00:00Z", Expires: "2027-04-27T10:00:00Z",
		AdvertisedRoutes: []string{"10.0.0.0/24", "0.0.0.0/0", "::/0"}, EnabledRoutes: []string{"10.0.0.0/24", "0.0.0.0/0", "::/0"},
		ExitNode: true, ExitNodeEnabled: true,
	}
}

func apDeviceTwo() Device {
	return Device{
		ID: "nBBB2CNTRL", Name: "db-1.tail1234.ts.net", Hostname: "db-1", OS: "linux", User: "tagged-devices",
		Addresses: []string{"100.64.0.2"}, Tags: []string{}, LastSeen: "2026-10-28T10:00:00Z", KeyExpiryDisabled: true,
		AdvertisedRoutes: []string{"0.0.0.0/0", "::/0"}, EnabledRoutes: []string{"0.0.0.0/0"}, ExitNode: true,
	}
}

func TestAPProductionTN(t *testing.T) {
	p := productionTN()
	if p.d.baseURL != "https://api.tailscale.com" || p.tokens == nil {
		t.Errorf("productionTN = %+v, want the Tailscale API base URL and a token cache", p.d)
	}
	m, ok := Module().(module)
	if !ok || m.t.d.baseURL != "https://api.tailscale.com" || m.t.tokens == nil {
		t.Errorf("Module() = %#v, want a module holding productionTN()", Module())
	}
}

func TestAPNewAPIRefusesIncompleteAccounts(t *testing.T) {
	srv := fake.New(t)
	for name, c := range map[string]struct {
		a      account
		secret string
	}{
		"no tailnet":   {account{ID: "a", ClientID: "c"}, "s"},
		"no client id": {account{ID: "a", Tailnet: "example.com"}, "s"},
		"no secret":    {account{ID: "a", Tailnet: "example.com", ClientID: "c"}, ""},
	} {
		if _, err := tnWith(srv).newAPI(c.a, []byte(c.secret), apClock()); err == nil {
			t.Errorf("%s: newAPI succeeded", name)
		}
	}
}

func TestAPDevicesMapsProviderFields(t *testing.T) {
	srv, a := apFake(t)
	apAnswer(srv, http.MethodGet, apTailnet("/devices"), 200, apDevicesJSON)
	got, err := a.Devices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := []Device{apDeviceOne(), apDeviceTwo()}; !reflect.DeepEqual(got, want) {
		t.Errorf("devices\n got %+v\nwant %+v", got, want)
	}
	reqs := apRequests(srv, http.MethodGet, apTailnet("/devices"))
	if len(reqs) != 1 || reqs[0].RawQuery != "fields=all" {
		t.Errorf("requests %+v, want one GET with fields=all", reqs)
	}
}

func TestAPDeviceMapsOneDevice(t *testing.T) {
	srv, a := apFake(t)
	one := `{"id":"1111","nodeId":"nAAA1CNTRL","name":"web-1.tail1234.ts.net","hostname":"web-1","os":"linux",
	 "user":"alice@example.com","addresses":["100.64.0.1","fd7a:115c:a1e0::1"],"tags":["tag:server"],"authorized":true,
	 "lastSeen":"2026-10-29T10:00:00Z","expires":"2027-04-27T10:00:00Z","keyExpiryDisabled":false,
	 "advertisedRoutes":["10.0.0.0/24","0.0.0.0/0","::/0"],"enabledRoutes":["10.0.0.0/24","0.0.0.0/0","::/0"]}`
	apAnswer(srv, http.MethodGet, "/api/v2/device/nAAA1CNTRL", 200, one)
	got, err := a.Device(context.Background(), "nAAA1CNTRL")
	if err != nil {
		t.Fatal(err)
	}
	if want := apDeviceOne(); !reflect.DeepEqual(got, want) {
		t.Errorf("device\n got %+v\nwant %+v", got, want)
	}
	if reqs := apRequests(srv, http.MethodGet, "/api/v2/device/nAAA1CNTRL"); len(reqs) != 1 || reqs[0].RawQuery != "fields=all" {
		t.Errorf("requests %+v, want one GET with fields=all", reqs)
	}
}

func TestAPRoutes(t *testing.T) {
	srv, a := apFake(t)
	ctx := context.Background()
	apAnswer(srv, http.MethodGet, "/api/v2/device/n1/routes", 200, `{"advertisedRoutes":["10.0.0.0/24"],"enabledRoutes":null}`)
	got, err := a.Routes(ctx, "n1")
	if err != nil {
		t.Fatal(err)
	}
	if want := (DeviceRoutes{Advertised: []string{"10.0.0.0/24"}, Enabled: []string{}}); !reflect.DeepEqual(got, want) {
		t.Errorf("Routes = %+v, want %+v", got, want)
	}

	apAnswer(srv, http.MethodPost, "/api/v2/device/n1/routes", 200, `{"advertisedRoutes":["10.0.0.0/24"],"enabledRoutes":["10.0.0.0/24"]}`)
	set, err := a.SetRoutes(ctx, "n1", []string{"10.0.0.0/24"})
	if err != nil {
		t.Fatal(err)
	}
	if want := (DeviceRoutes{Advertised: []string{"10.0.0.0/24"}, Enabled: []string{"10.0.0.0/24"}}); !reflect.DeepEqual(set, want) {
		t.Errorf("SetRoutes = %+v, want %+v", set, want)
	}
	apAnswer(srv, http.MethodPost, "/api/v2/device/n1/routes", 200, `{"advertisedRoutes":[],"enabledRoutes":[]}`)
	if _, err := a.SetRoutes(ctx, "n1", nil); err != nil {
		t.Fatal(err)
	}
	posts := apRequests(srv, http.MethodPost, "/api/v2/device/n1/routes")
	if len(posts) != 2 {
		t.Fatalf("%d route writes, want 2", len(posts))
	}
	apSameJSON(t, posts[0].Body, `{"routes":["10.0.0.0/24"]}`)
	apSameJSON(t, posts[1].Body, `{"routes":[]}`)
}

func TestAPIdAddressedCalls(t *testing.T) {
	ctx := context.Background()
	calls := []struct {
		name        string
		call        func(a *api, id string) error
		method      string
		path        string // for id "n1"
		body        string
		definite404 bool
	}{
		{"authorize", func(a *api, id string) error { return a.Authorize(ctx, id, true) }, http.MethodPost, "/api/v2/device/n1/authorized", `{"authorized":true}`, true},
		{"deauthorize", func(a *api, id string) error { return a.Authorize(ctx, id, false) }, http.MethodPost, "/api/v2/device/n1/authorized", `{"authorized":false}`, true},
		{"expire", func(a *api, id string) error { return a.Expire(ctx, id) }, http.MethodPost, "/api/v2/device/n1/expire", "", true},
		{"delete device", func(a *api, id string) error { return a.DeleteDevice(ctx, id) }, http.MethodDelete, "/api/v2/device/n1", "", false},
		{"delete key", func(a *api, id string) error { return a.DeleteKey(ctx, id) }, http.MethodDelete, apTailnet("/keys/n1"), "", false},
	}
	for _, c := range calls {
		t.Run(c.name+" sends", func(t *testing.T) {
			srv, a := apFake(t)
			apAnswer(srv, c.method, c.path, 200, "")
			if err := c.call(a, "n1"); err != nil {
				t.Fatal(err)
			}
			reqs := apRequests(srv, c.method, c.path)
			if len(reqs) != 1 {
				t.Fatalf("%d requests to %s %s, want 1", len(reqs), c.method, c.path)
			}
			apSameJSON(t, reqs[0].Body, c.body)
		})
		t.Run(c.name+" maps 404 to not_found", func(t *testing.T) {
			srv, a := apFake(t)
			apAnswer(srv, c.method, c.path, 404, `{"message":"not found"}`)
			err := c.call(a, "n1")
			if got := apCode(t, err); got != errcode.NotFound {
				t.Errorf("code %s, want %s", got, errcode.NotFound)
			}
			if core.IsDefinite(err) != c.definite404 {
				t.Errorf("IsDefinite = %v, want %v", core.IsDefinite(err), c.definite404)
			}
		})
		t.Run(c.name+" refuses an empty id", func(t *testing.T) {
			srv, a := apFake(t)
			err := c.call(a, "")
			if got := apCode(t, err); got != errcode.InvalidInput || !core.IsDefinite(err) {
				t.Errorf("code %s definite %v, want request.invalid_input and definite", got, core.IsDefinite(err))
			}
			for _, r := range srv.Cloud().Requests() {
				if r.Path != "/api/v2/oauth/token" {
					t.Errorf("request %s %s sent for an empty id", r.Method, r.Path)
				}
			}
		})
	}
}

func TestAPDeviceNotFound(t *testing.T) {
	srv, a := apFake(t)
	apAnswer(srv, http.MethodGet, "/api/v2/device/nZZZ", 404, `{"message":"device not found"}`)
	_, err := a.Device(context.Background(), "nZZZ")
	if got := apCode(t, err); got != errcode.NotFound {
		t.Errorf("code %s, want %s", got, errcode.NotFound)
	}
	if _, err := a.Device(context.Background(), ""); apCode(t, err) != errcode.InvalidInput {
		t.Errorf("empty id: %v, want request.invalid_input", err)
	}
}

func TestAPKeysMapProviderFields(t *testing.T) {
	srv, a := apFake(t)
	ctx := context.Background()
	apAnswer(srv, http.MethodGet, apTailnet("/keys"), 200,
		`{"keys":[{"id":"k1"},{"id":"c1","keyType":"client","description":"ci"},{"id":"k2","keyType":"auth","description":"darkpanel-01J00000000000000000000001","created":"2026-10-30T10:00:00Z","expires":"2026-10-31T10:00:00Z"}]}`)
	got, err := a.Keys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if reqs := apRequests(srv, http.MethodGet, apTailnet("/keys")); len(reqs) != 1 || reqs[0].RawQuery != "all=true" {
		t.Errorf("key list requests %+v, want one GET with all=true", reqs)
	}
	want := []KeyInfo{
		{ID: "k1"},
		{ID: "k2", Description: "darkpanel-01J00000000000000000000001",
			Created: time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC), Expires: time.Date(2026, 10, 31, 10, 0, 0, 0, time.UTC)},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Keys = %+v, want %+v", got, want)
	}

	apAnswer(srv, http.MethodGet, apTailnet("/keys/k9"), 200,
		`{"id":"k9","description":"darkpanel-01J00000000000000000000009","created":"2026-10-30T10:00:00Z","expires":"2026-10-31T10:00:00Z","revoked":"2026-10-30T12:00:00Z"}`)
	k, err := a.Key(ctx, "k9")
	if err != nil {
		t.Fatal(err)
	}
	wantKey := KeyInfo{ID: "k9", Description: "darkpanel-01J00000000000000000000009", Revoked: true,
		Created: time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC), Expires: time.Date(2026, 10, 31, 10, 0, 0, 0, time.UTC)}
	if !reflect.DeepEqual(k, wantKey) {
		t.Errorf("Key = %+v, want %+v", k, wantKey)
	}

	apAnswer(srv, http.MethodGet, apTailnet("/keys/k8"), 200, `{"id":"k8","keyType":"auth","invalid":true}`)
	if expired, err := a.Key(ctx, "k8"); err != nil || !expired.Revoked {
		t.Errorf("a key answered invalid: Key = %+v, %v; want Revoked", expired, err)
	}
}

func TestAPCreateKeySendsTheRequestAndTheProviderStoresIt(t *testing.T) {
	srv, a := apFake(t)
	ctx := context.Background()
	const desc = "darkpanel-01J00000000000000000000001"
	created, err := a.CreateKey(ctx, KeyRequest{Description: desc, Tags: []string{"tag:server"}, Reusable: true, Preauthorized: true, ExpirySeconds: 3600})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.Key == "" || created.Description != desc {
		t.Errorf("CreatedKey = %+v, want an id, a secret and the description %s", created, desc)
	}
	if got := len(srv.Keys()); got != 1 {
		t.Fatalf("the provider holds %d keys, want 1", got)
	}
	info, err := a.Key(ctx, created.ID)
	if err != nil || info.Description != desc {
		t.Errorf("Key(%s) = %+v, %v; want the description %s", created.ID, info, err, desc)
	}
	list, err := a.Keys(ctx)
	if err != nil || len(list) != 1 || list[0].ID != created.ID {
		t.Errorf("Keys = %+v, %v; want the created key", list, err)
	}

	if _, err := a.CreateKey(ctx, KeyRequest{Description: desc + "2", Tags: []string{"tag:server"}}); err != nil {
		t.Fatal(err)
	}
	posts := apRequests(srv, http.MethodPost, apTailnet("/keys"))
	if len(posts) != 2 {
		t.Fatalf("%d key creations, want 2", len(posts))
	}
	apSameJSON(t, posts[0].Body, `{"capabilities":{"devices":{"create":{"reusable":true,"ephemeral":false,"preauthorized":true,"tags":["tag:server"]}}},
	 "expirySeconds":3600,"description":"`+desc+`"}`)
	apSameJSON(t, posts[1].Body, `{"capabilities":{"devices":{"create":{"reusable":false,"ephemeral":false,"preauthorized":false,"tags":["tag:server"]}}},
	 "description":"`+desc+`2"}`)

	if err := a.DeleteKey(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if got := len(srv.Keys()); got != 1 {
		t.Errorf("the provider holds %d keys after the delete, want 1", got)
	}
}

func TestAPCreateKeyWithALostResponseIsUncertainAndNeverRetried(t *testing.T) {
	srv, a := apFake(t)
	srv.Cloud().Fail(http.MethodPost, apTailnet("/keys"), cloudfake.Fault{LoseResponse: true})
	_, err := a.CreateKey(context.Background(), KeyRequest{Description: "darkpanel-01J00000000000000000000001", Tags: []string{"tag:server"}})
	if got := apCode(t, err); got != errcode.CloudUncertainOutcome {
		t.Errorf("code %s, want %s", got, errcode.CloudUncertainOutcome)
	}
	if core.IsDefinite(err) {
		t.Error("a lost response is not definite")
	}
	if n := len(apRequests(srv, http.MethodPost, apTailnet("/keys"))); n != 1 {
		t.Errorf("%d key creations sent, want 1", n)
	}
	if n := len(srv.Keys()); n != 1 {
		t.Errorf("the provider holds %d keys, want the committed 1", n)
	}
}

func TestAPPolicyRoundTrip(t *testing.T) {
	srv, a := apFake(t)
	ctx := context.Background()
	first := "// owners\n{\n\t\"acls\": [],\n}\n"
	etag := srv.SetPolicy(first)
	text, gotETag, err := a.Policy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if string(text) != first || gotETag != etag {
		t.Errorf("Policy = %q, %q; want %q, %q", text, gotETag, first, etag)
	}
	if reqs := apRequests(srv, http.MethodGet, apTailnet("/acl")); len(reqs) != 1 || reqs[0].Header.Get("Accept") != "application/hujson" {
		t.Errorf("policy read requests %+v, want one with Accept application/hujson", reqs)
	}

	next := "{\n\t// changed\n\t\"acls\": [],\n}\n"
	newETag, err := a.SetPolicy(ctx, []byte(next), etag)
	if err != nil {
		t.Fatal(err)
	}
	if newETag == "" || newETag == etag {
		t.Errorf("new ETag %q, want one different from %q", newETag, etag)
	}
	if stored, storedETag := srv.Policy(); stored != next || storedETag != newETag {
		t.Errorf("provider policy %q (%q), want %q (%q)", stored, storedETag, next, newETag)
	}
	posts := apRequests(srv, http.MethodPost, apTailnet("/acl"))
	if len(posts) != 1 || posts[0].Header.Get("If-Match") != etag ||
		posts[0].Header.Get("Content-Type") != "application/hujson" || string(posts[0].Body) != next {
		t.Errorf("policy write requests %+v, want one with If-Match %q, hujson content and the new text", posts, etag)
	}
}

func TestAPSetPolicyWithAStaleETagIsADefiniteConflict(t *testing.T) {
	srv, a := apFake(t)
	stale := srv.SetPolicy("{}")
	srv.SetPolicy("{\"acls\": []}")
	_, err := a.SetPolicy(context.Background(), []byte("{\"groups\": {}}"), stale)
	if got := apCode(t, err); got != errcode.TailnetPolicyConflict {
		t.Errorf("code %s, want %s", got, errcode.TailnetPolicyConflict)
	}
	if !core.IsDefinite(err) {
		t.Error("a 412 refusal committed nothing and must be definite")
	}
	if text, _ := srv.Policy(); text != "{\"acls\": []}" {
		t.Errorf("provider policy %q changed by a refused write", text)
	}
}

func TestAPPolicyAnswersWithoutAnETag(t *testing.T) {
	srv, a := apFake(t)
	ctx := context.Background()
	apAnswer(srv, http.MethodGet, apTailnet("/acl"), 200, "{}")
	_, _, err := a.Policy(ctx)
	if got := apCode(t, err); got != errcode.CloudUnavailable {
		t.Errorf("Policy without an ETag: code %s, want %s", got, errcode.CloudUnavailable)
	}

	apAnswer(srv, http.MethodPost, apTailnet("/acl"), 200, "{}")
	_, err = a.SetPolicy(ctx, []byte("{}"), `"e1"`)
	if got := apCode(t, err); got != errcode.CloudUncertainOutcome {
		t.Errorf("SetPolicy without an ETag: code %s, want %s", got, errcode.CloudUncertainOutcome)
	}
	if core.IsDefinite(err) {
		t.Error("a write the provider accepted is not definite")
	}
}

func TestAPSetPolicyReadsTheETagBackWhenTheAnswerHasNone(t *testing.T) {
	srv, a := apFake(t)
	ctx := context.Background()
	next := "{\n\t// read back\n\t\"acls\": [],\n}\n"
	stored := srv.SetPolicy(next)
	apAnswer(srv, http.MethodPost, apTailnet("/acl"), 200, next)
	got, err := a.SetPolicy(ctx, []byte(next), `"e1"`)
	if err != nil || got != stored {
		t.Fatalf("SetPolicy = %q, %v; want the read-back ETag %q", got, err, stored)
	}
	if reads := apRequests(srv, http.MethodGet, apTailnet("/acl")); len(reads) != 1 || reads[0].Header.Get("Accept") != "application/hujson" {
		t.Errorf("read-back requests %+v, want one GET with Accept application/hujson", reads)
	}

	srv.SetPolicy("{\"groups\": {}}")
	apAnswer(srv, http.MethodPost, apTailnet("/acl"), 200, next)
	_, err = a.SetPolicy(ctx, []byte(next), `"e1"`)
	if got := apCode(t, err); got != errcode.CloudUncertainOutcome || core.IsDefinite(err) {
		t.Errorf("read-back of another text: code %s definite %v, want cloud.uncertain_outcome and not definite", got, core.IsDefinite(err))
	}
}

func TestAPSetPolicyRequiresIfMatch(t *testing.T) {
	srv, a := apFake(t)
	_, err := a.SetPolicy(context.Background(), []byte("{}"), "")
	if got := apCode(t, err); got != errcode.InvalidInput || !core.IsDefinite(err) {
		t.Errorf("code %s definite %v, want request.invalid_input and definite", got, core.IsDefinite(err))
	}
	if n := len(apRequests(srv, http.MethodPost, apTailnet("/acl"))); n != 0 {
		t.Errorf("%d policy writes sent without an ETag", n)
	}
}

func TestAPValidatePolicy(t *testing.T) {
	const bad = `{"message":"unknown tag tag:nope","data":[{"user":"a","errors":["x"]}]}`
	path := apTailnet("/acl/validate")
	tests := []struct {
		name   string
		status int // 0: the fake answers
		body   string
		want   errcode.Code // "" is valid
		detail string
	}{
		{"the fake accepts a valid policy", 0, "", "", ""},
		{"an empty 200 is valid", 200, "", "", ""},
		{"a 200 with an empty object is valid", 200, "{}", "", ""},
		{"a 200 with a message is invalid", 200, bad, errcode.TailnetPolicyInvalid, "unknown tag tag:nope"},
		{"a 400 with a message is invalid", 400, bad, errcode.TailnetPolicyInvalid, "unknown tag tag:nope"},
		{"a 400 without a message is invalid", 400, "not json", errcode.TailnetPolicyInvalid, "rejected"},
		{"a 403 is a missing permission", 403, `{"message":"forbidden"}`, errcode.CloudPermissionMissing, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv, a := apFake(t)
			if tc.status != 0 {
				apAnswer(srv, http.MethodPost, path, tc.status, tc.body)
			}
			text := "// c\n{\"acls\": []}\n"
			err := a.ValidatePolicy(context.Background(), []byte(text))
			if tc.want == "" {
				if err != nil {
					t.Fatalf("ValidatePolicy = %v, want nil", err)
				}
			} else {
				if got := apCode(t, err); got != tc.want {
					t.Fatalf("code %s, want %s", got, tc.want)
				}
				if d := errcode.From(err).Detail; !strings.Contains(d, tc.detail) {
					t.Errorf("detail %q does not contain %q", d, tc.detail)
				}
				if tc.want == errcode.TailnetPolicyInvalid && !core.IsDefinite(err) {
					t.Error("an invalid policy committed nothing and must be definite")
				}
			}
			posts := apRequests(srv, http.MethodPost, path)
			if len(posts) != 1 || string(posts[0].Body) != text || posts[0].Header.Get("Content-Type") != "application/hujson" {
				t.Errorf("validate requests %+v, want one with the text as hujson", posts)
			}
		})
	}
}

func TestAPScopesLimitWhatTheClientMayCall(t *testing.T) {
	srv, a := apFake(t)
	srv.SetScopes("devices:core")
	if _, err := a.Devices(context.Background()); err != nil {
		t.Fatalf("Devices with devices:core: %v", err)
	}
	_, err := a.Keys(context.Background())
	if got := apCode(t, err); got != errcode.CloudPermissionMissing {
		t.Errorf("Keys without auth_keys: code %s, want %s", got, errcode.CloudPermissionMissing)
	}
}

func TestAPReadsAreRetriedAndWritesAreNot(t *testing.T) {
	srv, a := apFake(t)
	ctx := context.Background()
	srv.Cloud().Fail(http.MethodGet, apTailnet("/devices"), cloudfake.Fault{Status: 503})
	if _, err := a.Devices(ctx); err != nil {
		t.Fatalf("Devices after one 503: %v", err)
	}
	if n := len(apRequests(srv, http.MethodGet, apTailnet("/devices"))); n != 2 {
		t.Errorf("%d device reads, want 2", n)
	}

	srv.Cloud().Fail(http.MethodPost, "/api/v2/device/n1/routes", cloudfake.Fault{Status: 503})
	_, err := a.SetRoutes(ctx, "n1", []string{"10.0.0.0/24"})
	if got := apCode(t, err); got != errcode.CloudUncertainOutcome {
		t.Errorf("SetRoutes after a 503: code %s, want %s", got, errcode.CloudUncertainOutcome)
	}
	if n := len(apRequests(srv, http.MethodPost, "/api/v2/device/n1/routes")); n != 1 {
		t.Errorf("%d route writes, want 1", n)
	}
}
