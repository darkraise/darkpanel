package tslocal

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// newFakeTailscaled serves mux on a Unix socket in a short temporary
// directory and returns a client for it. Like tailscaled, the server
// refuses a request whose Host is not local-tailscaled.sock or that carries
// a Referer or Origin header.
func newFakeTailscaled(t *testing.T, mux *http.ServeMux) *Client {
	t.Helper()
	dir, err := os.MkdirTemp("", "dp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	l, err := net.Listen("unix", filepath.Join(dir, "ts.sock"))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "local-tailscaled.sock" || r.Referer() != "" || r.Header.Get("Origin") != "" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, "invalid localapi request\n")
			return
		}
		mux.ServeHTTP(w, r)
	}))
	_ = srv.Listener.Close()
	srv.Listener = l
	srv.Start()
	t.Cleanup(srv.Close)
	c := New(filepath.Join(dir, "ts.sock"))
	t.Cleanup(c.Close)
	return c
}

const tsStatusJSON = `{
  "Version": "1.102.0-t1234", "TUN": true, "BackendState": "Running", "AuthURL": "",
  "TailscaleIPs": ["100.101.102.103", "fd7a:115c:a1e0::1"],
  "Self": {"ID": "nSelf", "HostName": "vps", "DNSName": "vps.tail1234.ts.net.", "OS": "linux",
    "TailscaleIPs": ["100.101.102.103"], "Tags": ["tag:server"], "Online": true, "Relay": "fra"},
  "Health": [], "MagicDNSSuffix": "tail1234.ts.net",
  "CurrentTailnet": {"Name": "example.com", "MagicDNSSuffix": "tail1234.ts.net", "MagicDNSEnabled": true},
  "CertDomains": ["vps.tail1234.ts.net"],
  "Peer": {
    "nodekey:aa": {"ID": "nA", "HostName": "laptop", "DNSName": "laptop.tail1234.ts.net.", "TailscaleIPs": ["100.64.0.2"],
      "AllowedIPs": ["100.64.0.2/32", "10.0.0.0/24"], "PrimaryRoutes": ["10.0.0.0/24"],
      "Addrs": ["203.0.113.5:41641"], "CurAddr": "203.0.113.5:41641", "Relay": "fra", "Online": true, "Active": true,
      "LastHandshake": "2026-10-23T10:00:00Z", "RxBytes": 10, "TxBytes": 20, "ExitNodeOption": true},
    "nodekey:bb": {"ID": "nB", "HostName": "phone", "CurAddr": "", "Relay": "nyc", "PeerRelay": "", "Online": false,
      "LastHandshake": "0001-01-01T00:00:00Z", "KeyExpiry": "2027-01-01T00:00:00Z"}
  },
  "ExitNodeStatus": {"ID": "nA", "Online": true, "TailscaleIPs": ["100.64.0.2/32"]},
  "User": {"1": {"ID": 1, "LoginName": "owner@example.com"}}
}`

func TestStatus(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /localapi/v0/status", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, tsStatusJSON)
	})
	s, err := newFakeTailscaled(t, mux).Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if s.BackendState != "Running" || s.Self.DNSName != "vps.tail1234.ts.net." || s.TailscaleIPs[1] != netip.MustParseAddr("fd7a:115c:a1e0::1") ||
		s.CurrentTailnet.Name != "example.com" || s.ExitNodeStatus.ID != "nA" {
		t.Fatalf("status = %+v", s)
	}
	a, b := s.Peer["nodekey:aa"], s.Peer["nodekey:bb"]
	if a.CurAddr != "203.0.113.5:41641" || a.PrimaryRoutes[0] != netip.MustParsePrefix("10.0.0.0/24") || !a.ExitNodeOption ||
		b.Relay != "nyc" || b.CurAddr != "" || b.KeyExpiry == nil || !b.LastHandshake.IsZero() {
		t.Fatalf("peers = %+v %+v", a, b)
	}
}

func TestErrorsAndUnavailable(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /localapi/v0/status", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":"backend not ready"}`)
	})
	mux.HandleFunc("GET /localapi/v0/prefs", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, "prefs access denied\n")
	})
	c := newFakeTailscaled(t, mux)
	var apiErr *APIError
	if _, err := c.Status(t.Context()); !errors.As(err, &apiErr) || apiErr.StatusCode != 500 || apiErr.Message != "backend not ready" {
		t.Fatalf("json error = %v", err)
	}
	if _, err := c.Prefs(t.Context()); !errors.As(err, &apiErr) || apiErr.Message != "prefs access denied" {
		t.Fatalf("text error = %v", err)
	}
	missing := New(filepath.Join(t.TempDir(), "none.sock"))
	defer missing.Close()
	if _, err := missing.Status(t.Context()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("missing socket err = %v, want ErrUnavailable", err)
	}
}

func TestPrefsRoundTripKeepsUnmodelledFields(t *testing.T) {
	in := `{"ControlURL":"https://controlplane.tailscale.com","RouteAll":false,"ExitNodeID":"","ExitNodeIP":"",` +
		`"CorpDNS":true,"RunSSH":false,"WantRunning":false,"LoggedOut":true,"ShieldsUp":false,"AdvertiseTags":null,` +
		`"Hostname":"","AdvertiseRoutes":null,"NetfilterMode":2,"NoSNAT":false,"AutoUpdate":{"Check":true,"Apply":null},"Config":null}`
	var p Prefs
	if err := json.Unmarshal([]byte(in), &p); err != nil {
		t.Fatal(err)
	}
	if !p.CorpDNS || !p.LoggedOut || p.ControlURL != "https://controlplane.tailscale.com" {
		t.Fatalf("prefs = %+v", p)
	}
	p.WantRunning = true
	p.LoggedOut = false
	p.Hostname = "agent-1"
	p.AdvertiseTags = []string{"tag:darkpanel"}
	out, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got["NetfilterMode"] != float64(2) || got["AutoUpdate"].(map[string]any)["Check"] != true || got["WantRunning"] != true ||
		got["LoggedOut"] != false || got["Hostname"] != "agent-1" || !reflect.DeepEqual(got["AdvertiseTags"], []any{"tag:darkpanel"}) {
		t.Fatalf("encoded = %s", out)
	}
	if _, ok := got["Config"]; !ok {
		t.Fatalf("encoded prefs dropped an unmodelled field: %s", out)
	}

	fresh, err := json.Marshal(Prefs{WantRunning: true})
	if err != nil {
		t.Fatal(err)
	}
	var freshMap map[string]any
	_ = json.Unmarshal(fresh, &freshMap)
	if freshMap["WantRunning"] != true || len(freshMap) != 12 {
		t.Fatalf("prefs built in code = %s", fresh)
	}
}

func TestEditPrefsSendsMaskedPrefs(t *testing.T) {
	mux := http.NewServeMux()
	var body map[string]any
	mux.HandleFunc("PATCH /localapi/v0/prefs", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		_, _ = io.WriteString(w, `{"ShieldsUp":true,"RunSSH":false,"AdvertiseRoutes":[],"NetfilterMode":2}`)
	})
	c := newFakeTailscaled(t, mux)
	yes, no := true, false
	none := []netip.Prefix(nil)
	ip := netip.MustParseAddr("100.64.0.2")
	p, err := c.EditPrefs(t.Context(), PrefsPatch{ShieldsUp: &yes, RunSSH: &no, AdvertiseRoutes: &none, ExitNodeIP: &ip})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"ShieldsUp": true, "ShieldsUpSet": true,
		"RunSSH": false, "RunSSHSet": true,
		"AdvertiseRoutes": []any{}, "AdvertiseRoutesSet": true,
		"ExitNodeIP": "100.64.0.2", "ExitNodeIPSet": true,
	}
	if !reflect.DeepEqual(body, want) {
		t.Fatalf("patch body = %#v", body)
	}
	if !p.ShieldsUp || p.RunSSH {
		t.Fatalf("returned prefs = %+v", p)
	}
	routes := []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0"), netip.MustParsePrefix("::/0")}
	b, err := json.Marshal(PrefsPatch{AdvertiseRoutes: &routes})
	if err != nil || string(b) != `{"AdvertiseRoutes":["0.0.0.0/0","::/0"],"AdvertiseRoutesSet":true}` {
		t.Fatalf("exit-node advertisement = %s, %v", b, err)
	}
	if b, _ := json.Marshal(PrefsPatch{}); string(b) != `{}` {
		t.Fatalf("empty patch = %s", b)
	}
}

func TestPrefsPatchEveryBranch(t *testing.T) {
	yes := true
	id := "nodeA"
	b, err := json.Marshal(PrefsPatch{WantRunning: &yes, RouteAll: &yes, ExitNodeID: &id})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"WantRunning": true, "WantRunningSet": true,
		"RouteAll": true, "RouteAllSet": true,
		"ExitNodeID": "nodeA", "ExitNodeIDSet": true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("patch = %s", b)
	}
}

func TestExitNodeIDAndIPAreExclusive(t *testing.T) {
	id := "nodeA"
	none := ""
	ip := netip.MustParseAddr("100.64.0.2")
	if _, err := json.Marshal(PrefsPatch{ExitNodeID: &id, ExitNodeIP: &ip}); err == nil || !strings.Contains(err.Error(), "ExitNodeID and ExitNodeIP") {
		t.Fatalf("patch naming both: err = %v", err)
	}
	if _, err := json.Marshal(Prefs{ExitNodeID: id, ExitNodeIP: ip}); err == nil || !strings.Contains(err.Error(), "ExitNodeID and ExitNodeIP") {
		t.Fatalf("prefs naming both: err = %v", err)
	}
	// Switching from one form to the other names one and clears the other.
	b, err := json.Marshal(PrefsPatch{ExitNodeID: &none, ExitNodeIP: &ip})
	if err != nil || !strings.Contains(string(b), `"ExitNodeIDSet":true`) || !strings.Contains(string(b), `"ExitNodeIP":"100.64.0.2"`) {
		t.Fatalf("clearing the ID while setting the address = %s, %v", b, err)
	}

	reached := false
	mux := http.NewServeMux()
	mux.HandleFunc("PATCH /localapi/v0/prefs", func(http.ResponseWriter, *http.Request) { reached = true })
	c := newFakeTailscaled(t, mux)
	if _, err := c.EditPrefs(t.Context(), PrefsPatch{ExitNodeID: &id, ExitNodeIP: &ip}); err == nil || reached {
		t.Fatalf("EditPrefs sent a patch naming both: err = %v, reached = %v", err, reached)
	}
}

func TestStartRefusesPrefsBuiltInCode(t *testing.T) {
	reached := false
	mux := http.NewServeMux()
	mux.HandleFunc("POST /localapi/v0/start", func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusNoContent)
	})
	c := newFakeTailscaled(t, mux)
	err := c.Start(t.Context(), StartOptions{UpdatePrefs: &Prefs{WantRunning: true}})
	if err == nil || !strings.Contains(err.Error(), "not read from tailscaled") || reached {
		t.Fatalf("err = %v, reached = %v", err, reached)
	}
	if err := c.Start(t.Context(), StartOptions{AuthKey: "tskey-auth-x"}); err != nil || !reached {
		t.Fatalf("start without UpdatePrefs = %v (reached %v)", err, reached)
	}
}

func TestStartAndLogout(t *testing.T) {
	mux := http.NewServeMux()
	var start map[string]any
	mux.HandleFunc("POST /localapi/v0/start", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&start); err != nil {
			t.Error(err)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	loggedOut := false
	mux.HandleFunc("POST /localapi/v0/logout", func(w http.ResponseWriter, _ *http.Request) {
		loggedOut = true
		w.WriteHeader(http.StatusNoContent)
	})
	c := newFakeTailscaled(t, mux)
	var p Prefs
	if err := json.Unmarshal([]byte(`{"CorpDNS":true,"NetfilterMode":2,"WantRunning":false}`), &p); err != nil {
		t.Fatal(err)
	}
	p.WantRunning = true
	if err := c.Start(t.Context(), StartOptions{AuthKey: "tskey-auth-x", UpdatePrefs: &p}); err != nil {
		t.Fatal(err)
	}
	up, _ := start["UpdatePrefs"].(map[string]any)
	if start["AuthKey"] != "tskey-auth-x" || up["WantRunning"] != true || up["NetfilterMode"] != float64(2) || up["CorpDNS"] != true {
		t.Fatalf("start body = %#v", start)
	}
	if err := c.Logout(t.Context()); err != nil || !loggedOut {
		t.Fatalf("logout = %v (called %v)", err, loggedOut)
	}
}
