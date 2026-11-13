//go:build harness

package harness

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/controller/coreactions"
	"github.com/darkraise/darkpanel/internal/modules/panel"
)

// m6puExposed is the output of a panel.exposure.add through NPM that took the highest free /29.
func m6puExposed() panel.ExposeOutput {
	return panel.ExposeOutput{
		ExposureID: "01JD0000000000000000000010", Via: panel.ViaNPM, Hostname: "panel.example.test", HostID: "h1",
		Subnet: netip.MustParsePrefix("172.31.255.248/29"), Gateway: netip.MustParseAddr("172.31.255.249"),
		Proxy: netip.MustParseAddr("172.31.255.250"), Port: panel.DefaultPort,
	}
}

func m6puInput() panel.ExposeInput {
	return panel.ExposeInput{Via: panel.ViaNPM, Hostname: "panel.example.test", HostID: "h1"}
}

func TestM6pUnitSpoofHeaders(t *testing.T) {
	h := m6pSpoofHeaders()
	for _, name := range []string{"X-Forwarded-For", "X-Real-Ip", "X-Darkpanel-Client", "X-Forwarded-Proto"} {
		if len(h.Values(name)) != 1 {
			t.Errorf("header %s has values %q, want one", name, h.Values(name))
		}
	}
	if len(h) != 4 {
		t.Errorf("%d headers, want the four the proxy path must not trust", len(h))
	}
	if got := h.Get("X-Forwarded-Proto"); got != "http" {
		t.Errorf("X-Forwarded-Proto %q, want http, the value a plain-HTTP client claims", got)
	}
	all := strings.Join([]string{h.Get("X-Forwarded-For"), h.Get("X-Real-Ip"), h.Get("X-Darkpanel-Client")}, " ")
	for _, addr := range m6pForgedAddrs {
		if !strings.Contains(all, addr) {
			t.Errorf("forged address %s is in no header", addr)
		}
		if _, err := netip.ParseAddr(addr); err != nil {
			t.Errorf("forged address %q: %v", addr, err)
		}
	}
}

// m6puLogin is the audit row of a login attempt of user from source; "" records none.
func m6puLogin(user, source string) coreactions.AuditEntry {
	e := coreactions.AuditEntry{Action: "core.auth.login", Phase: "login", Input: coreactions.AuditInput(`{"username":"` + user + `"}`)}
	if source != "" {
		e.Source = &source
	}
	return e
}

func TestM6pUnitCheckAudit(t *testing.T) {
	const proxy = "172.31.255.250"
	good := []coreactions.AuditEntry{
		m6puLogin("m6p-public", "198.18.0.20 via "+proxy),
		m6puLogin("m6p-private", "10.77.0.11 via "+proxy),
		m6puLogin("admin", "127.0.0.1"),
		m6puLogin("admin", "198.18.0.20 via "+proxy),
		{Action: "core.audit.list", Phase: "apply", Input: coreactions.AuditInput(`{"username":"m6p-plain"}`)},
	}
	want := map[string]string{"m6p-public": "198.18.0.20 via " + proxy, "m6p-private": "10.77.0.11 via " + proxy, "admin": "198.18.0.20 via " + proxy}
	tests := []struct {
		name    string
		entries []coreactions.AuditEntry
		want    map[string]string
		absent  []string
		err     string // "" means accepted
	}{
		{"every source recorded as itself", good, want, []string{"m6p-plain"}, ""},
		{"a second row of another source is fine", good, map[string]string{"admin": "127.0.0.1"}, nil, ""},
		{"no row of a source", good[:1], map[string]string{"m6p-private": "10.77.0.11 via " + proxy}, nil, "no login of m6p-private"},
		{"a source recorded as another", append([]coreactions.AuditEntry{m6puLogin("m6p-private", "198.18.0.20 via "+proxy)}, good[:1]...),
			map[string]string{"m6p-private": "10.77.0.11 via " + proxy}, nil, "m6p-private"},
		{"a row without a source", []coreactions.AuditEntry{m6puLogin("m6p-public", "")}, map[string]string{"m6p-public": "198.18.0.20 via " + proxy}, nil, "m6p-public"},
		{"the forged client header recorded", []coreactions.AuditEntry{m6puLogin("m6p-public", m6pForgedClient+" via "+proxy)},
			map[string]string{"m6p-public": m6pForgedClient + " via " + proxy}, nil, "forged address " + m6pForgedClient},
		{"the forged forwarded-for recorded", []coreactions.AuditEntry{m6puLogin("m6p-public", "203.0.113.99")}, nil, nil, "forged address 203.0.113.99"},
		{"a plain-HTTP login that reached the panel", append([]coreactions.AuditEntry{m6puLogin("m6p-plain", "198.18.0.20 via "+proxy)}, good...),
			want, []string{"m6p-plain"}, "m6p-plain"},
	}
	for _, tc := range tests {
		err := m6pCheckAudit(tc.entries, tc.want, tc.absent)
		switch {
		case tc.err == "" && err != nil:
			t.Errorf("%s: %v, want it accepted", tc.name, err)
		case tc.err != "" && (err == nil || !strings.Contains(err.Error(), tc.err)):
			t.Errorf("%s: %v, want an error naming %q", tc.name, err, tc.err)
		}
	}
}

func TestM6pUnitParseReply(t *testing.T) {
	tests := []struct {
		name, out string
		want      m6pReply
		err       bool
	}{
		{"a status", `{"status":401,"body":"{\"code\":\"auth.invalid_credentials\"}"}` + "\n", m6pReply{Status: 401, Body: `{"code":"auth.invalid_credentials"}`}, false},
		{"an error", `{"error":"socket hang up"}`, m6pReply{Error: "socket hang up"}, false},
		{"noise before the line", "Unable to find image\n\n" + `{"status":200,"body":""}` + "\n", m6pReply{Status: 200}, false},
		{"no output", "", m6pReply{}, true},
		{"not JSON", "node: command not found\n", m6pReply{}, true},
		{"neither status nor error", `{"body":"x"}`, m6pReply{}, true},
	}
	for _, tc := range tests {
		got, err := m6pParseReply(tc.out)
		if (err != nil) != tc.err || got != tc.want {
			t.Errorf("%s: m6pParseReply = %+v, %v, want %+v (error %v)", tc.name, got, err, tc.want, tc.err)
		}
	}
}

func TestM6pUnitCheckRefusal(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		err    bool
	}{
		{"the panel's refusal", 401, `{"type":"about:blank","status":401,"code":"auth.invalid_credentials"}`, false},
		{"NPM's bad gateway", 502, `<html>502 Bad Gateway</html>`, true},
		{"the panel's rate limit", 429, `{"code":"auth.rate_limited"}`, true},
		{"a 401 that is not the panel's", 401, `Unauthorized`, true},
		{"a login that succeeded", 200, `{"csrfToken":"x"}`, true},
		{"the refusal of a forged protocol", 403, `{"code":"forbidden"}`, true},
	}
	for _, tc := range tests {
		if err := m6pCheckRefusal(tc.status, []byte(tc.body)); (err != nil) != tc.err {
			t.Errorf("%s: m6pCheckRefusal = %v, want error %v", tc.name, err, tc.err)
		}
	}
}

func TestM6pUnitCheckPlain(t *testing.T) {
	const host = "panel.example.test"
	tests := []struct {
		name     string
		status   int
		location string
		err      bool
	}{
		{"forced SSL", http.StatusMovedPermanently, "https://panel.example.test/api/v1/auth/login", false},
		{"a temporary redirect", http.StatusTemporaryRedirect, "https://panel.example.test/", false},
		{"served", http.StatusOK, "", true},
		{"redirected to plain HTTP", http.StatusMovedPermanently, "http://panel.example.test/", true},
		{"redirected to another host", http.StatusMovedPermanently, "https://other.example.test/", true},
		{"not found", http.StatusNotFound, "", true},
		{"a redirect without a location", http.StatusMovedPermanently, "", true},
	}
	for _, tc := range tests {
		if err := m6pCheckPlain(tc.status, tc.location, host); (err != nil) != tc.err {
			t.Errorf("%s: m6pCheckPlain = %v, want error %v", tc.name, err, tc.err)
		}
	}
}

func TestM6pUnitDirectRefused(t *testing.T) {
	for out, wantErr := range map[string]bool{"000": false, "000\n": false, "200": true, "401": true, "": true} {
		if err := m6pDirectRefused(out); (err != nil) != wantErr {
			t.Errorf("m6pDirectRefused(%q) = %v, want error %v", out, err, wantErr)
		}
	}
}

func TestM6pUnitCheckExposed(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*panel.ExposeOutput, *panel.ExposeInput)
		id     string
		err    string // "" means accepted
	}{
		{"the highest free /29", func(*panel.ExposeOutput, *panel.ExposeInput) {}, "", ""},
		{"a port that was asked for", func(o *panel.ExposeOutput, in *panel.ExposeInput) { in.Port, o.Port = 9555, 9555 }, "", ""},
		{"an exposure id that is not the change", func(*panel.ExposeOutput, *panel.ExposeInput) {}, "01JD0000000000000000000099", "exposure id"},
		{"another hostname", func(o *panel.ExposeOutput, _ *panel.ExposeInput) { o.Hostname = "other.example.test" }, "", "does not echo"},
		{"the default port ignored", func(o *panel.ExposeOutput, _ *panel.ExposeInput) { o.Port = 8080 }, "", "does not echo"},
		{"a /28", func(o *panel.ExposeOutput, _ *panel.ExposeInput) {
			o.Subnet = netip.MustParsePrefix("172.31.255.240/28")
		}, "", "a /29"},
		{"a /29 outside the panel range", func(o *panel.ExposeOutput, _ *panel.ExposeInput) {
			o.Subnet = netip.MustParsePrefix("172.30.255.248/29")
			o.Gateway, o.Proxy = netip.MustParseAddr("172.30.255.249"), netip.MustParseAddr("172.30.255.250")
		}, "", "inside 172.31.255.0/24"},
		{"a gateway that is the network address", func(o *panel.ExposeOutput, _ *panel.ExposeInput) { o.Gateway = netip.MustParseAddr("172.31.255.248") }, "", "first and second"},
		{"a proxy that is the third address", func(o *panel.ExposeOutput, _ *panel.ExposeInput) { o.Proxy = netip.MustParseAddr("172.31.255.251") }, "", "first and second"},
	}
	for _, tc := range tests {
		out, in := m6puExposed(), m6puInput()
		tc.mutate(&out, &in)
		id := tc.id
		if id == "" {
			id = out.ExposureID
		}
		err := m6pCheckExposed(out, id, in)
		switch {
		case tc.err == "" && err != nil:
			t.Errorf("%s: %v, want it accepted", tc.name, err)
		case tc.err != "" && (err == nil || !strings.Contains(err.Error(), tc.err)):
			t.Errorf("%s: %v, want an error naming %q", tc.name, err, tc.err)
		}
	}
}

// m6puInspect is docker network inspect of the panel network with NPM at the proxy address.
const m6puInspect = `[{"Name":"darkpanel-panel","EnableIPv6":false,
 "Labels":{"darkpanel.module":"panel"},"Options":{"com.docker.network.bridge.name":"dp-panel"},
 "IPAM":{"Driver":"default","Config":[{"Subnet":"172.31.255.248/29","Gateway":"172.31.255.249"}]},
 "Containers":{"abc":{"Name":"darkpanel-npm","IPv4Address":"172.31.255.250/29","IPv6Address":""}}}]`

func TestM6pUnitCheckNetwork(t *testing.T) {
	replace := func(from, to string) func(string) string {
		return func(doc string) string {
			if n := strings.Count(doc, from); n != 1 {
				t.Fatalf("the fixture holds %q %d times, want once", from, n)
			}
			return strings.Replace(doc, from, to, 1)
		}
	}
	tests := []struct {
		name string
		edit func(string) string
		err  string // "" means accepted
	}{
		{"the network as add creates it", func(doc string) string { return doc }, ""},
		{"another name", replace(`"Name":"darkpanel-panel"`, `"Name":"other"`), "network"},
		{"no module label", replace(`"darkpanel.module":"panel"`, `"darkpanel.module":"npm"`), "labels"},
		{"another bridge name", replace(`"dp-panel"`, `"br-1234"`), "options"},
		{"IPv6 on", replace(`"EnableIPv6":false`, `"EnableIPv6":true`), "IPv6"},
		{"a second IPAM config", replace(`"Gateway":"172.31.255.249"}]`, `"Gateway":"172.31.255.249"},{"Subnet":"fd00::/64"}]`), "IPAM"},
		{"another subnet", replace(`"Subnet":"172.31.255.248/29"`, `"Subnet":"172.31.255.240/29"`), "IPAM"},
		{"NPM at another address", replace(`"IPv4Address":"172.31.255.250/29"`, `"IPv4Address":"172.31.255.251/29"`), "darkpanel-npm is at"},
		{"NPM not attached", replace(`"Name":"darkpanel-npm"`, `"Name":"other"`), "not on the panel network"},
		{"not JSON", func(string) string { return "Error: No such network" }, "docker network inspect"},
	}
	for _, tc := range tests {
		err := m6pCheckNetwork([]byte(tc.edit(m6puInspect)), m6puExposed())
		switch {
		case tc.err == "" && err != nil:
			t.Errorf("%s: %v, want it accepted", tc.name, err)
		case tc.err != "" && (err == nil || !strings.Contains(err.Error(), tc.err)):
			t.Errorf("%s: %v, want an error naming %q", tc.name, err, tc.err)
		}
	}
}

func TestM6pUnitCheckStatus(t *testing.T) {
	view := func(mutate func(*panel.ExposureView)) panel.StatusOutput {
		o := m6puExposed()
		v := panel.ExposureView{ID: o.ExposureID, Via: o.Via, Hostname: o.Hostname, HostID: o.HostID, Subnet: o.Subnet, Gateway: o.Gateway,
			Proxy: o.Proxy, Port: o.Port, State: panel.StateOpen, ListenerID: "l1"}
		mutate(&v)
		return panel.StatusOutput{Exposed: true, Exposure: &v}
	}
	tests := []struct {
		name   string
		status panel.StatusOutput
		banner string
		err    bool
	}{
		{"the exposure the add made", view(func(*panel.ExposureView) {}), "Panel exposed via npm at panel.example.test", false},
		{"not exposed", panel.StatusOutput{}, "", true},
		{"exposed without a view", panel.StatusOutput{Exposed: true}, "", true},
		{"another exposure", view(func(v *panel.ExposureView) { v.ID = "01JD0000000000000000000099" }), "", true},
		{"another proxy address", view(func(v *panel.ExposureView) { v.Proxy = netip.MustParseAddr("172.31.255.251") }), "", true},
		{"a revoked listener", view(func(v *panel.ExposureView) { v.State = panel.StateRevoked }), "", true},
		{"no listener id", view(func(v *panel.ExposureView) { v.ListenerID = "" }), "", true},
	}
	for _, tc := range tests {
		banner, err := m6pCheckStatus(tc.status, m6puExposed())
		if (err != nil) != tc.err || banner != tc.banner {
			t.Errorf("%s: m6pCheckStatus = %q, %v, want %q (error %v)", tc.name, banner, err, tc.banner, tc.err)
		}
	}
}

func TestM6pUnitIPv6HTTPSRule(t *testing.T) {
	if got, want := m6pIPv6HTTPSRule(true), "sudo ufw allow proto tcp from ::/0 to any port 443"; got != want {
		t.Errorf("m6pIPv6HTTPSRule(true) = %q, want %q", got, want)
	}
	if got, want := m6pIPv6HTTPSRule(false), "sudo ufw delete allow proto tcp from ::/0 to any port 443"; got != want {
		t.Errorf("m6pIPv6HTTPSRule(false) = %q, want %q", got, want)
	}
}

func TestM6pUnitAgentPortCommand(t *testing.T) {
	if got, want := m6pAgentPortCommand(), "sudo ufw allow 7443/tcp"; got != want {
		t.Errorf("m6pAgentPortCommand = %q, want %q", got, want)
	}
}

func TestM6pUnitSettleConfirmed(t *testing.T) {
	tests := []struct {
		name     string
		states   []string // what the change reads as, poll by poll, until confirmed; the last repeats
		want     string
		confirms int32
	}{
		{"a composite awaiting confirmation", []string{"applying", "awaiting_confirmation"}, "completed", 1},
		{"a change that completes on its own", []string{"applying", "completed"}, "completed", 0},
		{"a change reverted before any confirm", []string{"applying", "reverted"}, "reverted", 0},
	}
	for _, tc := range tests {
		var polls, confirms atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodPost && r.URL.Path == "/api/v1/changes/c1/confirm":
				confirms.Add(1)
				_, _ = w.Write([]byte(`{"id":"c1","state":"completed"}`))
			case r.Method == http.MethodGet && r.URL.Path == "/api/v1/changes/c1":
				state := "completed"
				if confirms.Load() == 0 {
					state = tc.states[min(int(polls.Add(1)), len(tc.states))-1]
				}
				_, _ = w.Write([]byte(`{"id":"c1","state":"` + state + `"}`))
			default:
				http.NotFound(w, r)
			}
		}))
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		ch, err := m6pSettleConfirmed(ctx, m4TestAPI(t, srv), "c1")
		cancel()
		srv.Close()
		if err != nil || ch.State != tc.want || confirms.Load() != tc.confirms {
			t.Errorf("%s: m6pSettleConfirmed = %+v, %v after %d confirms, want %s after %d", tc.name, ch, err, confirms.Load(), tc.want, tc.confirms)
		}
	}
}

func TestM6pUnitForwardedProtoLine(t *testing.T) {
	tests := []struct {
		name, out string
		err       bool
	}{
		{"NPM's include", "add_header X-Served-By $host;\nproxy_set_header Host $host;\nproxy_set_header X-Forwarded-Proto  $scheme;\nproxy_set_header X-Forwarded-For $remote_addr;\n", false},
		{"indented in a location", "  location / {\n    proxy_set_header X-Forwarded-Proto $scheme;\n  }\n", false},
		{"commented out", "# proxy_set_header X-Forwarded-Proto $scheme;\n", true},
		{"the client's value passed on", "proxy_set_header X-Forwarded-Proto $http_x_forwarded_proto;\n", true},
		{"a fixed value", "proxy_set_header X-Forwarded-Proto https;\n", true},
		{"only another header", "proxy_set_header X-Forwarded-For $remote_addr;\n", true},
		{"no output", "", true},
	}
	for _, tc := range tests {
		if err := m6pForwardedProtoLine(tc.out); (err != nil) != tc.err {
			t.Errorf("%s: m6pForwardedProtoLine = %v, want error %v", tc.name, err, tc.err)
		}
	}
}

func TestM6pUnitHostAddrs(t *testing.T) {
	raw := []byte(`[{"ifname":"lo","addr_info":[{"family":"inet","local":"127.0.0.1"},{"family":"inet6","local":"::1"}]},` +
		`{"ifname":"dpinet","addr_info":[{"family":"inet","local":"198.18.0.10"},{"family":"inet6","local":"2001:db8:d0::10"},{"family":"inet6","local":"fd00:d0::10"}]},` +
		`{"ifname":"darkpanel-npm","addr_info":[{"family":"inet","local":"172.20.0.1"}]}]`)
	got, err := m6pHostAddrs(raw)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"127.0.0.1", "::1", "198.18.0.10", "2001:db8:d0::10", "fd00:d0::10", "172.20.0.1"}
	if !slices.Equal(got, want) {
		t.Errorf("m6pHostAddrs = %q, want %q", got, want)
	}
	if _, err := m6pHostAddrs([]byte("not json")); err == nil {
		t.Error("m6pHostAddrs of text that is not JSON succeeded, want an error")
	}
}

func TestM6pUnitHiddenClient(t *testing.T) {
	login := func(user, source string) coreactions.AuditEntry {
		return coreactions.AuditEntry{ID: "e-" + user, Action: auth.AuditLoginAction, Input: coreactions.AuditInput(`{"username":"` + user + `"}`), Source: &source}
	}
	host := []string{"127.0.0.1", "198.18.0.10", "172.20.0.1"}
	const proxy = "172.21.0.2"
	for _, tc := range []struct {
		name    string
		entries []coreactions.AuditEntry
		want    string // "" when the probe passes
	}{
		{"a host address", []coreactions.AuditEntry{login(m6pUserULA, "172.20.0.1 via "+proxy)}, ""},
		{"a host address among other logins", []coreactions.AuditEntry{login(m6pUserPublic, "198.18.0.20 via "+proxy), login(m6pUserULA, "198.18.0.10 via "+proxy)}, ""},
		{"the real client", []coreactions.AuditEntry{login(m6pUserULA, "fd00:d0::20 via "+proxy)},
			`harness: outcome other-address: the audit log records "fd00:d0::20 via 172.21.0.2" for m6p-ula, not an address of the controller's host via 172.21.0.2; stop the plan and return row 6 to the owner`},
		{"a host address through another proxy", []coreactions.AuditEntry{login(m6pUserULA, "172.20.0.1 via 172.21.0.9")},
			`harness: outcome other-address: the audit log records "172.20.0.1 via 172.21.0.9" for m6p-ula, not an address of the controller's host via 172.21.0.2; stop the plan and return row 6 to the owner`},
		{"no login of the user", []coreactions.AuditEntry{login(m6pUserPublic, "198.18.0.20 via "+proxy)},
			"harness: outcome no-request: the audit log holds no login of m6p-ula; the probe is inconclusive"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := m6pHiddenClient(tc.entries, m6pUserULA, proxy, host)
			switch {
			case tc.want == "" && err != nil:
				t.Errorf("m6pHiddenClient = %v, want nil", err)
			case tc.want != "" && (err == nil || err.Error() != tc.want):
				t.Errorf("m6pHiddenClient = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestM6pUnitCurlCode(t *testing.T) {
	cmd := m6pCurlCode("fd00:d0::20", "fd00:d0::10", "POST", "/api/v1/auth/login", `{"username":"m6p-ula"}`)
	for _, part := range []string{"curl", "--interface", "fd00:d0::20", "panel.example.test:443:[fd00:d0::10]", "https://panel.example.test/api/v1/auth/login", "%{http_code}", "POST", `"username":"m6p-ula"`} {
		if !strings.Contains(cmd, part) {
			t.Errorf("command %q lacks %q", cmd, part)
		}
	}
	if get := m6pCurlCode("2001:db8:d0::20", "2001:db8:d0::10", "GET", "/healthz", ""); strings.Contains(get, "--data") || strings.Contains(get, "Content-Type") {
		t.Errorf("a GET without a body sends one: %q", get)
	}
}
