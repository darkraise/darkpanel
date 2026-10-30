package cloudflare

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/cloud"
	"github.com/darkraise/darkpanel/internal/controller/cloud/cloudfake"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/cloudflare/fake"
	"github.com/darkraise/darkpanel/internal/modules/cloudflare/ingress"
)

func apNoSleep(context.Context, time.Duration) error { return nil }

// cfWith is the module's dependencies pointed at the fake server, with
// retries that do not wait.
func cfWith(srv *fake.Server) cf {
	return cf{d: deps{baseURL: srv.URL(), http: srv.Client(), sleep: apNoSleep}}
}

var apClock = coretest.NewFakeClock(time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC))

const apAccount = "acct1"

// apRaw is a bare cloudfake server and an api over it; the tests register
// the provider's answers they need.
func apRaw(t *testing.T) (*cloudfake.Server, *api) {
	t.Helper()
	srv := cloudfake.New(t)
	a, err := cf{d: deps{baseURL: srv.URL(), http: srv.Client(), sleep: apNoSleep}}.newAPI(apAccount, []byte("tok"), apClock)
	if err != nil {
		t.Fatal(err)
	}
	return srv, a
}

// apReply writes Cloudflare's envelope around result.
func apReply(w http.ResponseWriter, status int, success bool, code int, result any) {
	errs := []map[string]any{}
	if !success {
		errs = append(errs, map[string]any{"code": code, "message": "failed"})
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"success": success, "errors": errs, "messages": []any{}, "result": result})
}

func apOK(result any) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { apReply(w, 200, true, 0, result) }
}

func apQuery(t *testing.T, r cloudfake.Request) url.Values {
	t.Helper()
	q, err := url.ParseQuery(r.RawQuery)
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func apCount(srv *cloudfake.Server, method string) int {
	n := 0
	for _, r := range srv.Requests() {
		if r.Method == method {
			n++
		}
	}
	return n
}

func apWantCode(t *testing.T, err error, code errcode.Code) *errcode.Error {
	t.Helper()
	if err == nil {
		t.Fatalf("no error, want %s", code)
	}
	e := errcode.From(err)
	if e.Code != code {
		t.Fatalf("error = %v, want code %s", err, code)
	}
	return e
}

func TestAPRequestsCarryTheTokenAndTheAccountPath(t *testing.T) {
	srv, a := apRaw(t)
	srv.Handle(http.MethodGet, "/accounts/"+apAccount+"/cfd_tunnel", apOK([]any{}))
	if _, err := a.Tunnels(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Tunnels(context.Background(), "darkpanel-c1"); err != nil {
		t.Fatal(err)
	}
	reqs := srv.Requests()
	if len(reqs) != 2 {
		t.Fatalf("%d requests, want one per call", len(reqs))
	}
	if got := reqs[0].Header.Get("Authorization"); got != "Bearer tok" {
		t.Errorf("Authorization %q, want Bearer tok", got)
	}
	if got := reqs[0].Header.Get("Accept"); !strings.Contains(got, "application/json") {
		t.Errorf("Accept %q, want JSON", got)
	}
	first, second := apQuery(t, reqs[0]), apQuery(t, reqs[1])
	if first.Get("is_deleted") != "false" || first.Has("name") {
		t.Errorf("first query %v, want is_deleted=false and no name", first)
	}
	if second.Get("is_deleted") != "false" || second.Get("name") != "darkpanel-c1" {
		t.Errorf("second query %v, want is_deleted=false and the name", second)
	}
}

func TestAPListsFollowEveryPage(t *testing.T) {
	srv, a := apRaw(t)
	srv.Handle(http.MethodGet, "/zones", func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		info := map[string]any{"page": 1, "per_page": 1, "total_pages": 2, "count": 1, "total_count": 2}
		zone := map[string]string{"id": "z1", "name": "example.com"}
		if page == "2" {
			info["page"], zone = 2, map[string]string{"id": "z2", "name": "example.org"}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "errors": []any{}, "messages": []any{}, "result": []any{zone}, "result_info": info})
	})
	zones, err := a.Zones(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := []Zone{{ID: "z1", Name: "example.com"}, {ID: "z2", Name: "example.org"}}; !reflect.DeepEqual(zones, want) {
		t.Errorf("zones %+v, want %+v", zones, want)
	}
}

func TestAPTunnelDecodesTheProvidersFields(t *testing.T) {
	srv, a := apRaw(t)
	srv.Handle(http.MethodGet, "/accounts/"+apAccount+"/cfd_tunnel/t1", apOK(map[string]any{
		"id": "t1", "name": "darkpanel-c1", "status": "healthy", "created_at": "2026-10-29T08:00:00.123456Z", "deleted_at": nil,
		"connections": []any{map[string]any{
			"id": "conn1", "colo_name": "FRA", "origin_ip": "203.0.113.5", "opened_at": "2026-10-30T09:00:00Z",
			"client_version": "2026.9.0", "is_pending_reconnect": false,
		}},
	}))
	got, err := a.Tunnel(context.Background(), "t1")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "t1" || got.Name != "darkpanel-c1" || got.Status != "healthy" ||
		!got.CreatedAt.Equal(time.Date(2026, 10, 29, 8, 0, 0, 123456000, time.UTC)) {
		t.Errorf("tunnel %+v, want the provider's id, name, status and creation time", got)
	}
	if len(got.Connections) != 1 {
		t.Fatalf("connections %+v, want one", got.Connections)
	}
	c := got.Connections[0]
	if c.ID != "conn1" || c.ColoName != "FRA" || c.OriginIP != "203.0.113.5" || c.ClientVersion != "2026.9.0" ||
		!c.OpenedAt.Equal(time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)) {
		t.Errorf("connection %+v, want the provider's fields", c)
	}
}

func TestAPConnectionsReadBothAnswerShapes(t *testing.T) {
	conn := func(id, colo string) map[string]any {
		return map[string]any{"id": id, "colo_name": colo, "origin_ip": "203.0.113.5", "opened_at": "2026-10-30T09:00:00Z"}
	}
	flat := conn("conn1", "FRA")
	flat["client_version"] = "2026.9.0"
	tests := []struct {
		name   string
		result []any
		want   []string
	}{
		{"connectors with their connections", []any{map[string]any{"id": "client1", "version": "2026.9.0", "conns": []any{conn("conn1", "FRA"), conn("conn2", "AMS")}}},
			[]string{"conn1 FRA 203.0.113.5 2026.9.0", "conn2 AMS 203.0.113.5 2026.9.0"}},
		{"flat connections", []any{flat}, []string{"conn1 FRA 203.0.113.5 2026.9.0"}},
		{"none", []any{}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv, a := apRaw(t)
			srv.Handle(http.MethodGet, "/accounts/"+apAccount+"/cfd_tunnel/t1/connections", apOK(tc.result))
			got, err := a.Connections(context.Background(), "t1")
			if err != nil {
				t.Fatal(err)
			}
			var lines []string
			for _, c := range got {
				lines = append(lines, strings.Join([]string{c.ID, c.ColoName, c.OriginIP, c.ClientVersion}, " "))
				if !c.OpenedAt.Equal(time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)) {
					t.Errorf("connection %s opened at %v", c.ID, c.OpenedAt)
				}
			}
			if !reflect.DeepEqual(lines, tc.want) {
				t.Errorf("connections %q, want %q", lines, tc.want)
			}
		})
	}
}

func TestAPCreateTunnelAsksForARemotelyManagedTunnel(t *testing.T) {
	srv, a := apRaw(t)
	srv.Handle(http.MethodPost, "/accounts/"+apAccount+"/cfd_tunnel", apOK(map[string]any{"id": "t1", "name": "darkpanel-c1", "status": "inactive", "created_at": "2026-10-30T10:00:00Z"}))
	got, err := a.CreateTunnel(context.Background(), "darkpanel-c1")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "t1" || got.Name != "darkpanel-c1" {
		t.Errorf("tunnel %+v, want the created one", got)
	}
	req := srv.Requests()[0]
	var body map[string]any
	if err := json.Unmarshal(req.Body, &body); err != nil {
		t.Fatal(err)
	}
	if want := map[string]any{"name": "darkpanel-c1", "config_src": "cloudflare"}; !reflect.DeepEqual(body, want) {
		t.Errorf("body %v, want %v", body, want)
	}
	if ct := req.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type %q, want JSON", ct)
	}
}

func TestAPTunnelTokenAndDelete(t *testing.T) {
	srv, a := apRaw(t)
	srv.Handle(http.MethodGet, "/accounts/"+apAccount+"/cfd_tunnel/t1/token", apOK("eyJhIjoiYiJ9"))
	srv.Handle(http.MethodDelete, "/accounts/"+apAccount+"/cfd_tunnel/t1", apOK(map[string]string{"id": "t1"}))
	tok, err := a.TunnelToken(context.Background(), "t1")
	if err != nil || tok != "eyJhIjoiYiJ9" {
		t.Errorf("token %q (%v), want the provider's string", tok, err)
	}
	if err := a.DeleteTunnel(context.Background(), "t1"); err != nil {
		t.Fatal(err)
	}
	if got := srv.Requests()[1]; got.Method != http.MethodDelete || got.Path != "/accounts/"+apAccount+"/cfd_tunnel/t1" {
		t.Errorf("second request %s %s, want the tunnel's delete", got.Method, got.Path)
	}
}

func TestAPConfigurationRoundTripKeepsUnknownMembers(t *testing.T) {
	srv, a := apRaw(t)
	const config = `{"ingress":[{"hostname":"app.example.com","service":"http://web:80","originRequest":{"noTLSVerify":true,"connectTimeout":"10s"}},{"service":"http_status:404"}],"warp-routing":{"enabled":false}}`
	srv.Handle(http.MethodGet, "/accounts/"+apAccount+"/cfd_tunnel/t1/configurations",
		apOK(map[string]any{"tunnel_id": "t1", "version": 3, "source": "cloudflare", "config": json.RawMessage(config)}))
	srv.Handle(http.MethodGet, "/accounts/"+apAccount+"/cfd_tunnel/t2/configurations",
		apOK(map[string]any{"tunnel_id": "t2", "version": 0, "source": "cloudflare", "config": nil}))
	srv.Handle(http.MethodPut, "/accounts/"+apAccount+"/cfd_tunnel/t1/configurations", apOK(map[string]any{"tunnel_id": "t1", "version": 4}))

	cfg, err := a.Configuration(context.Background(), "t1")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Ingress) != 2 || cfg.Ingress[0].Hostname != "app.example.com" || cfg.Ingress[0].Service != "http://web:80" ||
		!cfg.Ingress[0].NoTLSVerify || cfg.Ingress[1].Service != ingress.CatchAll {
		t.Fatalf("configuration %+v, want the two rules", cfg)
	}
	if err := a.PutConfiguration(context.Background(), "t1", cfg); err != nil {
		t.Fatal(err)
	}
	put := srv.Requests()[1]
	if put.Method != http.MethodPut {
		t.Fatalf("second request is %s, want PUT", put.Method)
	}
	var body, want map[string]any
	if err := json.Unmarshal(put.Body, &body); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`{"config":`+config+`}`), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(body, want) {
		t.Errorf("PUT body %s, want the configuration wrapped under config with its other members", put.Body)
	}

	empty, err := a.Configuration(context.Background(), "t2")
	if err != nil || len(empty.Ingress) != 0 {
		t.Errorf("configuration of a tunnel never configured = %+v (%v), want an empty one", empty, err)
	}
}

func TestAPDNSRecords(t *testing.T) {
	srv, a := apRaw(t)
	rec := map[string]any{"id": "r1", "type": "CNAME", "name": "app.example.com", "content": "t1.cfargotunnel.com", "proxied": true, "comment": "darkpanel:c1"}
	srv.Handle(http.MethodGet, "/zones/z1/dns_records", apOK([]any{rec}))
	srv.Handle(http.MethodPost, "/zones/z1/dns_records", apOK(rec))
	srv.Handle(http.MethodGet, "/zones/z1/dns_records/r1", apOK(rec))
	srv.Handle(http.MethodDelete, "/zones/z1/dns_records/r1", apOK(map[string]string{"id": "r1"}))
	ctx := context.Background()
	want := DNSRecord{ID: "r1", Type: "CNAME", Name: "app.example.com", Content: "t1.cfargotunnel.com", Proxied: true, Comment: "darkpanel:c1"}

	list, err := a.DNSRecords(ctx, "z1", "app.example.com", "darkpanel:c1")
	if err != nil || !reflect.DeepEqual(list, []DNSRecord{want}) {
		t.Fatalf("records %+v (%v), want %+v", list, err, want)
	}
	if _, err := a.DNSRecords(ctx, "z1", "", ""); err != nil {
		t.Fatal(err)
	}
	created, err := a.CreateDNSRecord(ctx, "z1", DNSRecord{Type: "CNAME", Name: "app.example.com", Content: "t1.cfargotunnel.com", Proxied: true, Comment: "darkpanel:c1"})
	if err != nil || created != want {
		t.Fatalf("created %+v (%v), want %+v", created, err, want)
	}
	one, err := a.DNSRecord(ctx, "z1", "r1")
	if err != nil || one != want {
		t.Fatalf("record %+v (%v), want %+v", one, err, want)
	}
	if err := a.DeleteDNSRecord(ctx, "z1", "r1"); err != nil {
		t.Fatal(err)
	}

	reqs := srv.Requests()
	filtered, unfiltered := apQuery(t, reqs[0]), apQuery(t, reqs[1])
	if filtered.Get("name") != "app.example.com" || filtered.Get("comment.exact") != "darkpanel:c1" {
		t.Errorf("filtered query %v, want the name and the exact comment", filtered)
	}
	if unfiltered.Has("name") || unfiltered.Has("comment.exact") {
		t.Errorf("unfiltered query %v, want no filter", unfiltered)
	}
	var body map[string]any
	if err := json.Unmarshal(reqs[2].Body, &body); err != nil {
		t.Fatal(err)
	}
	wantBody := map[string]any{"type": "CNAME", "name": "app.example.com", "content": "t1.cfargotunnel.com", "proxied": true, "comment": "darkpanel:c1", "ttl": float64(1)}
	if !reflect.DeepEqual(body, wantBody) {
		t.Errorf("create body %v, want %v", body, wantBody)
	}
	if last := reqs[4]; last.Method != http.MethodDelete || last.Path != "/zones/z1/dns_records/r1" {
		t.Errorf("last request %s %s, want the record's delete", last.Method, last.Path)
	}
}

func TestAPMissingObjectsAreNotFound(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name   string
		method string
		path   string
		call   func(a *api) error
	}{
		{"tunnel", http.MethodGet, "/accounts/" + apAccount + "/cfd_tunnel/t9", func(a *api) error { _, err := a.Tunnel(ctx, "t9"); return err }},
		{"tunnel delete", http.MethodDelete, "/accounts/" + apAccount + "/cfd_tunnel/t9", func(a *api) error { return a.DeleteTunnel(ctx, "t9") }},
		{"tunnel token", http.MethodGet, "/accounts/" + apAccount + "/cfd_tunnel/t9/token", func(a *api) error { _, err := a.TunnelToken(ctx, "t9"); return err }},
		{"configuration", http.MethodGet, "/accounts/" + apAccount + "/cfd_tunnel/t9/configurations", func(a *api) error { _, err := a.Configuration(ctx, "t9"); return err }},
		{"record", http.MethodGet, "/zones/z1/dns_records/r9", func(a *api) error { _, err := a.DNSRecord(ctx, "z1", "r9"); return err }},
		{"record delete", http.MethodDelete, "/zones/z1/dns_records/r9", func(a *api) error { return a.DeleteDNSRecord(ctx, "z1", "r9") }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv, a := apRaw(t)
			srv.Handle(tc.method, tc.path, func(w http.ResponseWriter, _ *http.Request) { apReply(w, 404, false, 1003, nil) })
			err := tc.call(a)
			e := apWantCode(t, err, errcode.NotFound)
			if !strings.Contains(e.Detail, "9") {
				t.Errorf("detail %q does not name the object", e.Detail)
			}
			if cloud.Status(err) != http.StatusNotFound {
				t.Errorf("cloud.Status = %d, want 404 kept in the error chain", cloud.Status(err))
			}
		})
	}
}

func TestAPFailuresMapToTheCloudCodes(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name     string
		method   string
		path     string
		status   int
		body     string
		call     func(a *api) error
		code     errcode.Code
		detail   string
		definite bool
	}{
		{"success false on a read", http.MethodGet, "/accounts/" + apAccount + "/cfd_tunnel", 200,
			`{"success":false,"errors":[{"code":1000,"message":"x"}],"result":null}`,
			func(a *api) error { _, err := a.Tunnels(ctx, ""); return err }, errcode.CloudUnavailable, "", false},
		{"success false on a write", http.MethodPost, "/accounts/" + apAccount + "/cfd_tunnel", 200,
			`{"success":false,"errors":[{"code":1000,"message":"x"}],"result":null}`,
			func(a *api) error { _, err := a.CreateTunnel(ctx, "n"); return err }, errcode.CloudUncertainOutcome, "", false},
		{"unreadable answer on a read", http.MethodGet, "/zones", 200, `not json`,
			func(a *api) error { _, err := a.Zones(ctx); return err }, errcode.CloudUnavailable, "", false},
		{"refused write", http.MethodPost, "/accounts/" + apAccount + "/cfd_tunnel", 400,
			`{"success":false,"errors":[{"code":1010,"message":"bad"}],"result":null}`,
			func(a *api) error { _, err := a.CreateTunnel(ctx, "n"); return err }, errcode.InvalidInput, "cloudflare answered 400 (1010)", true},
		{"missing permission", http.MethodGet, "/zones", 403,
			`{"success":false,"errors":[{"code":10000,"message":"Authentication error"}],"result":null}`,
			func(a *api) error { _, err := a.Zones(ctx); return err }, errcode.CloudPermissionMissing, "cloudflare answered 403 (10000)", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv, a := apRaw(t)
			srv.Handle(tc.method, tc.path, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			})
			err := tc.call(a)
			e := apWantCode(t, err, tc.code)
			if tc.detail != "" && e.Detail != tc.detail {
				t.Errorf("detail %q, want %q", e.Detail, tc.detail)
			}
			if core.IsDefinite(err) != tc.definite {
				t.Errorf("definite = %v, want %v", core.IsDefinite(err), tc.definite)
			}
		})
	}
}

func TestAPReadsAreRetriedAndWritesAreNot(t *testing.T) {
	srv, a := apRaw(t)
	srv.Handle(http.MethodGet, "/accounts/"+apAccount+"/cfd_tunnel", apOK([]any{}))
	srv.Handle(http.MethodPost, "/accounts/"+apAccount+"/cfd_tunnel", apOK(map[string]any{"id": "t1", "name": "n"}))
	srv.Fail(http.MethodGet, "/accounts/"+apAccount+"/cfd_tunnel", cloudfake.Fault{Status: 503})
	srv.Fail(http.MethodPost, "/accounts/"+apAccount+"/cfd_tunnel", cloudfake.Fault{Status: 500})

	if _, err := a.Tunnels(context.Background(), ""); err != nil {
		t.Fatalf("a read after one 503: %v", err)
	}
	if n := apCount(srv, http.MethodGet); n != 2 {
		t.Errorf("%d GET requests, want the failed one and its retry", n)
	}
	_, err := a.CreateTunnel(context.Background(), "n")
	_ = apWantCode(t, err, errcode.CloudUncertainOutcome)
	if n := apCount(srv, http.MethodPost); n != 1 {
		t.Errorf("%d POST requests, want exactly one", n)
	}
}

func TestAPZoneForPicksTheLongestMatchingSuffix(t *testing.T) {
	zones := []Zone{{ID: "z1", Name: "example.com"}, {ID: "z2", Name: "sub.example.com"}, {ID: "z3", Name: "example.org"}}
	tests := []struct {
		hostname string
		want     string
	}{
		{"app.example.com", "z1"},
		{"a.sub.example.com", "z2"},
		{"sub.example.com", "z2"},
		{"example.com", "z1"},
		{"APP.Example.COM", "z1"},
		{"app.example.com.", "z1"},
		{"notexample.com", ""},
		{"app.example.net", ""},
		{"", ""},
	}
	for _, tc := range tests {
		t.Run(tc.hostname, func(t *testing.T) {
			z, ok := zoneFor(zones, tc.hostname)
			if ok != (tc.want != "") || z.ID != tc.want {
				t.Errorf("zoneFor(%q) = %+v, %v; want zone %q", tc.hostname, z, ok, tc.want)
			}
		})
	}
}

func TestAPNewAPIRefusesMissingCredentials(t *testing.T) {
	c := cf{d: deps{baseURL: "http://127.0.0.1:1", sleep: apNoSleep}}
	if _, err := c.newAPI("", []byte("tok"), apClock); err == nil {
		t.Error("an empty account id was accepted")
	}
	if _, err := c.newAPI(apAccount, nil, apClock); err == nil {
		t.Error("an empty token was accepted")
	}
}

func TestAPAgainstTheFake(t *testing.T) {
	ctx := context.Background()
	srv := fake.New(t)
	a, err := cfWith(srv).newAPI(fake.AccountID, []byte(fake.Token), apClock)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.VerifyToken(ctx); err != nil {
		t.Fatalf("verify: %v", err)
	}
	zid := srv.AddZone("example.com")
	zones, err := a.Zones(ctx)
	if err != nil || len(zones) != 1 || zones[0].ID != zid || zones[0].Name != "example.com" {
		t.Fatalf("zones %+v (%v), want example.com", zones, err)
	}

	tun, err := a.CreateTunnel(ctx, "darkpanel-c1")
	if err != nil || tun.ID == "" || tun.Name != "darkpanel-c1" {
		t.Fatalf("create tunnel %+v (%v)", tun, err)
	}
	srv.AddTunnel("other")
	if raw, _ := json.Marshal(srv.Tunnels()); !strings.Contains(string(raw), `"darkpanel-c1"`) {
		t.Errorf("the fake's tunnels %s lack the created one", raw)
	}
	named, err := a.Tunnels(ctx, "darkpanel-c1")
	if err != nil || len(named) != 1 || named[0].ID != tun.ID {
		t.Errorf("tunnels by name %+v (%v), want the created one only", named, err)
	}
	if all, err := a.Tunnels(ctx, ""); err != nil || len(all) != 2 {
		t.Errorf("all tunnels %+v (%v), want two", all, err)
	}

	srv.SetConnections(tun.ID, 2)
	got, err := a.Tunnel(ctx, tun.ID)
	if err != nil || len(got.Connections) != 2 || got.Status == "" {
		t.Errorf("tunnel with connections %+v (%v), want two connections and a status", got, err)
	}
	if conns, err := a.Connections(ctx, tun.ID); err != nil || len(conns) != 2 {
		t.Errorf("connections %+v (%v), want two", conns, err)
	}
	srv.SetConnections(tun.ID, 0)
	if token, err := a.TunnelToken(ctx, tun.ID); err != nil || token == "" {
		t.Errorf("token %q (%v), want one", token, err)
	}

	cfg := ingress.Config{Ingress: []ingress.Rule{{Hostname: "app.example.com", Service: "http://web:80"}, {Service: ingress.CatchAll}}}
	if err := a.PutConfiguration(ctx, tun.ID, cfg); err != nil {
		t.Fatal(err)
	}
	if raw := srv.Configuration(tun.ID); !strings.Contains(string(raw), "app.example.com") {
		t.Errorf("the fake's configuration %s lacks the rule", raw)
	}
	read, err := a.Configuration(ctx, tun.ID)
	if err != nil || len(read.Ingress) != 2 || read.Ingress[0].Hostname != "app.example.com" {
		t.Errorf("configuration %+v (%v), want the two rules back", read, err)
	}

	rec, err := a.CreateDNSRecord(ctx, zid, DNSRecord{Type: "CNAME", Name: "app.example.com", Content: tun.ID + ".cfargotunnel.com", Proxied: true, Comment: "darkpanel:c1"})
	if err != nil || rec.ID == "" || len(srv.Records(zid)) != 1 {
		t.Fatalf("create record %+v (%v), fake holds %d", rec, err, len(srv.Records(zid)))
	}
	if found, err := a.DNSRecords(ctx, zid, "app.example.com", "darkpanel:c1"); err != nil || len(found) != 1 || found[0] != rec {
		t.Errorf("records by name and comment %+v (%v), want %+v", found, err, rec)
	}
	if other, err := a.DNSRecords(ctx, zid, "", "darkpanel:other"); err != nil || len(other) != 0 {
		t.Errorf("records of another comment %+v (%v), want none", other, err)
	}
	if err := a.DeleteDNSRecord(ctx, zid, rec.ID); err != nil || len(srv.Records(zid)) != 0 {
		t.Fatalf("delete record: %v, fake holds %d", err, len(srv.Records(zid)))
	}
	_, err = a.DNSRecord(ctx, zid, rec.ID)
	_ = apWantCode(t, err, errcode.NotFound)

	if err := a.DeleteTunnel(ctx, tun.ID); err != nil {
		t.Fatalf("delete tunnel: %v", err)
	}
	_, err = a.Tunnel(ctx, tun.ID)
	_ = apWantCode(t, err, errcode.NotFound)
}

func TestAPVerifyTokenFallsBackToTheUserEndpoint(t *testing.T) {
	ctx := context.Background()
	srv := fake.New(t)
	refused := cloudfake.Fault{Status: http.StatusUnauthorized, Body: `{"success":false,"errors":[{"code":1000,"message":"Invalid API Token"}],"messages":[],"result":null}`}
	for _, prefix := range []string{"", "/client/v4"} {
		srv.Cloud().Fail(http.MethodGet, prefix+"/accounts/"+fake.AccountID+"/tokens/verify", refused)
	}
	a, err := cfWith(srv).newAPI(fake.AccountID, []byte(fake.Token), apClock)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.VerifyToken(ctx); err != nil {
		t.Fatalf("a token the user endpoint accepts: VerifyToken = %v", err)
	}
	user := 0
	for _, r := range srv.Cloud().Requests() {
		if strings.HasSuffix(r.Path, "/user/tokens/verify") {
			user++
		}
	}
	if user != 1 {
		t.Errorf("%d requests to /user/tokens/verify, want 1", user)
	}
}

func TestAPAgainstTheFakeWithoutAccess(t *testing.T) {
	ctx := context.Background()
	srv := fake.New(t)

	wrong, err := cfWith(srv).newAPI(fake.AccountID, []byte("not-the-token"), apClock)
	if err != nil {
		t.Fatal(err)
	}
	_, err = wrong.Zones(ctx)
	if c := errcode.From(err).Code; c != errcode.CloudAuthFailed && c != errcode.CloudPermissionMissing {
		t.Errorf("wrong token: %v, want an authentication or permission refusal", err)
	}
	if err := wrong.VerifyToken(ctx); err == nil {
		t.Error("verify accepted a wrong token")
	}

	a, err := cfWith(srv).newAPI(fake.AccountID, []byte(fake.Token), apClock)
	if err != nil {
		t.Fatal(err)
	}
	srv.SetPermissions()
	_, err = a.Zones(ctx)
	e := apWantCode(t, err, errcode.CloudPermissionMissing)
	if !strings.HasPrefix(e.Detail, "cloudflare answered 403 (") {
		t.Errorf("detail %q, want the provider's error code after the status", e.Detail)
	}
}
