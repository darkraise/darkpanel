package npm

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/modules/npm/fake"
	"github.com/darkraise/darkpanel/internal/modules/npm/npmapi"
	"github.com/darkraise/darkpanel/internal/modules/npm/proxy"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

const (
	paAdmin = "npm.admin.expose"
	paPanel = "panel.exposure.add"
)

// paModule registers one action, so the registry's own checks run on it.
type paModule struct{ def core.ActionDef }

func (m paModule) Manifest() core.Manifest {
	return core.Manifest{ID: ID, Version: 1, Actions: []core.ActionDef{m.def}}
}

// paRefused asserts err is a definite refusal with the given code and returns it.
func paRefused(t *testing.T, err error, code errcode.Code) *errcode.Error {
	t.Helper()
	var coded *errcode.Error
	if !errors.As(err, &coded) || coded.Code != code {
		t.Fatalf("error %v, want code %s", err, code)
	}
	if !core.IsDefinite(err) {
		t.Errorf("error %v is not marked definite", err)
	}
	return coded
}

func paJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// paHost is a deployed instance whose container has the address 172.18.0.2,
// the hostname 4fa6e0f0c678 and the alias npm, and a module whose panel
// network is 172.31.255.0/29 with gateway .1.
func paHost(t *testing.T) (*thHost, nm) {
	t.Helper()
	h := newTHHost(t)
	h.deployed()
	c, ok := h.docker.container(containerName)
	if !ok {
		t.Fatal("deployed() left no container")
	}
	c.Config.Hostname = "4fa6e0f0c678"
	c.NetworkSettings.Networks = map[string]*dockerapi.EndpointSettings{
		networkName: {NetworkID: "n1", IPAddress: netip.MustParseAddr("172.18.0.2"), Aliases: []string{"npm"}},
	}
	h.docker.putContainer(c)
	n := h.nm()
	n.d.panel = func(context.Context, core.Env) (proxy.Reserved, error) {
		return proxy.Reserved{
			PanelNetwork: netip.MustParsePrefix("172.31.255.0/29"),
			PanelGateway: netip.MustParseAddr("172.31.255.1"),
		}, nil
	}
	return h, n
}

func paEnv(h *thHost, change, parent string) core.Env {
	if parent == "" {
		return h.env(change)
	}
	return h.child(change, parent)
}

func paInput(domains ...string) ProxyHostInput {
	return ProxyHostInput{Domains: domains, ForwardScheme: "http", ForwardHost: "web", ForwardPort: 8080}
}

func paApply(t *testing.T, h *thHost, n nm, change, parent string, in ProxyHostInput) (ProxyHostView, error) {
	t.Helper()
	h.addChange(change, "npm.proxyhost.add")
	raw, err := n.proxyhostAdd().Handler.Apply(context.Background(), paEnv(h, change, parent), paJSON(t, in))
	if err != nil {
		return ProxyHostView{}, err
	}
	var out ProxyHostView
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out, nil
}

func TestPaValidate(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(in *ProxyHostInput)
		field  string // "" accepted, "*" refused at any field
	}{
		{name: "valid"},
		{name: "wildcard domain", mutate: func(in *ProxyHostInput) { in.Domains = []string{"*.example.com"} }},
		{name: "https with certificate and forced ssl", mutate: func(in *ProxyHostInput) {
			in.ForwardScheme, in.CertificateID, in.SSLForced, in.HSTS = "https", 3, true, true
		}},
		{name: "no domains", mutate: func(in *ProxyHostInput) { in.Domains = nil }, field: "/domains"},
		{name: "uppercase domain", mutate: func(in *ProxyHostInput) { in.Domains = []string{"A.example.com"} }, field: "/domains/0"},
		{name: "domain with a path", mutate: func(in *ProxyHostInput) { in.Domains = []string{"a.example.com/x"} }, field: "/domains/0"},
		{name: "single label domain", mutate: func(in *ProxyHostInput) { in.Domains = []string{"localhost"} }, field: "/domains/0"},
		{name: "duplicate domain", mutate: func(in *ProxyHostInput) { in.Domains = []string{"a.example.com", "a.example.com"} }, field: "/domains/1"},
		{name: "scheme", mutate: func(in *ProxyHostInput) { in.ForwardScheme = "ftp" }, field: "/forwardScheme"},
		{name: "empty host", mutate: func(in *ProxyHostInput) { in.ForwardHost = "" }, field: "/forwardHost"},
		{name: "host with a scheme", mutate: func(in *ProxyHostInput) { in.ForwardHost = "http://web" }, field: "/forwardHost"},
		{name: "an IPv6 literal host", mutate: func(in *ProxyHostInput) { in.ForwardHost = "2001:db8::1" }, field: "/forwardHost"},
		{name: "the IPv6 loopback host", mutate: func(in *ProxyHostInput) { in.ForwardHost = "::1" }, field: "/forwardHost"},
		{name: "an IPv6 host with a zone", mutate: func(in *ProxyHostInput) { in.ForwardHost = "fe80::1%eth0" }, field: "/forwardHost"},
		{name: "port zero", mutate: func(in *ProxyHostInput) { in.ForwardPort = 0 }, field: "/forwardPort"},
		{name: "port too high", mutate: func(in *ProxyHostInput) { in.ForwardPort = 65536 }, field: "/forwardPort"},
		{name: "forced ssl without a certificate", mutate: func(in *ProxyHostInput) { in.SSLForced = true }, field: "*"},
		{name: "hsts without forced ssl", mutate: func(in *ProxyHostInput) { in.CertificateID, in.HSTS = 3, true }, field: "*"},
		{name: "negative access list", mutate: func(in *ProxyHostInput) { in.AccessListID = -1 }, field: "*"},
		{name: "negative certificate", mutate: func(in *ProxyHostInput) { in.CertificateID = -1 }, field: "*"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := paInput("a.example.com")
			if tt.mutate != nil {
				tt.mutate(&in)
			}
			err := validateProxyHostInput(in)
			if tt.field == "" {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				return
			}
			coded := paRefused(t, err, errcode.InvalidInput)
			if tt.field != "*" && !slices.ContainsFunc(coded.Fields, func(f errcode.FieldError) bool { return f.Path == tt.field }) {
				t.Errorf("fields %v, want one at %s", coded.Fields, tt.field)
			}
		})
	}
}

func TestPaBody(t *testing.T) {
	in := ProxyHostInput{
		Domains: []string{"b.example.com", "a.example.com"}, ForwardScheme: "https", ForwardHost: "172.31.255.1", ForwardPort: 9443,
		CertificateID: 3, AccessListID: 2, SSLForced: true, HSTS: true, Websockets: true,
	}
	for _, tt := range []struct {
		parent string
		panel  bool
	}{{"", false}, {paAdmin, false}, {paPanel, true}} {
		want := npmapi.ProxyHostBody{
			DomainNames: []string{"a.example.com", "b.example.com"}, ForwardScheme: "https", ForwardHost: "172.31.255.1", ForwardPort: 9443,
			CertificateID: 3, AccessListID: 2, SSLForced: true, HSTSEnabled: true, AllowWebsocketUpgrade: true,
			Locations: []npmapi.Location{proxy.Location("https", "172.31.255.1", 9443, tt.panel)},
		}
		if got := proxyHostBody(in, tt.parent); !reflect.DeepEqual(got, want) {
			t.Errorf("parent %q: body %+v, want %+v", tt.parent, got, want)
		}
	}
	if got := proxyHostBody(in, paPanel).Locations[0].AdvancedConfig; !strings.Contains(got, "proxy_read_timeout 3600s;") {
		t.Errorf("panel location %q lacks the read timeout", got)
	}
	if got := proxyHostBody(in, "").Locations[0].AdvancedConfig; strings.Contains(got, "proxy_read_timeout") {
		t.Errorf("ordinary location %q has a read timeout", got)
	}
}

func TestPaState(t *testing.T) {
	host := npmapi.ProxyHost{
		ID: 9, CreatedOn: "2026-11-02T10:00:00.000Z", ModifiedOn: "2026-11-02T10:00:01.000Z",
		DomainNames: []string{"b.example.com", "a.example.com"}, ForwardScheme: "http", ForwardHost: "web", ForwardPort: 8080,
		CertificateID: 3, AccessListID: 2, SSLForced: true, HSTSEnabled: true,
		Locations: []npmapi.Location{{Path: "/", ForwardScheme: "http", ForwardHost: "web", ForwardPort: 8080, AdvancedConfig: "x"}},
	}
	var got map[string]any
	if err := json.Unmarshal(proxyHostState(v1r10Env(), host), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"domains": []any{"a.example.com", "b.example.com"}, "forwardScheme": "http", "forwardHost": "web", "forwardPort": float64(8080),
		"certificateId": float64(3), "accessListId": float64(2), "sslForced": true, "hsts": true, "websockets": false,
		"locations": []any{map[string]any{
			"path": "/", "forwardScheme": "http", "forwardHost": "web", "forwardPort": float64(8080), "advancedConfig": v1r10Fingerprint("x"),
		}},
		"advancedConfig": v1r10Fingerprint(""),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("state %v, want %v", got, want)
	}
	host.Locations = nil
	if err := json.Unmarshal(proxyHostState(v1r10Env(), host), &got); err != nil || !reflect.DeepEqual(got["locations"], []any{}) {
		t.Errorf("a host without locations: locations %v (%v), want an empty array", got["locations"], err)
	}
}

func TestPaCreatedOn(t *testing.T) {
	want := time.Date(2026, 11, 2, 10, 0, 1, 0, time.UTC)
	for _, s := range []string{"2026-11-02T10:00:01Z", "2026-11-02T10:00:01.000Z", "2026-11-02 10:00:01"} {
		if got, err := proxyHostCreatedOn(s); err != nil || !got.Equal(want) {
			t.Errorf("%q parses to %v (%v), want %v", s, got, err, want)
		}
	}
	if _, err := proxyHostCreatedOn("yesterday"); err == nil {
		t.Error("an unparseable time was accepted")
	}
}

func TestPaPlan(t *testing.T) {
	ctx := context.Background()
	t.Run("diff", func(t *testing.T) {
		h, n := paHost(t)
		cert := h.npm.SeedCertificate(npmapi.Certificate{NiceName: "c", Provider: "letsencrypt", DomainNames: []string{"a.example.com"}})
		list := h.npm.SeedAccessList(npmapi.AccessList{Name: "al"})
		plain, err := n.proxyhostAdd().Handler.Plan(ctx, h.env(""), paJSON(t, paInput("b.example.com", "a.example.com")))
		if err != nil {
			t.Fatal(err)
		}
		wantPlain := []core.DiffLine{
			{Op: "+", Text: "proxy host a.example.com, b.example.com"},
			{Op: " ", Text: "forward http://web:8080"},
		}
		if !slices.Equal(plain.Diff, wantPlain) || plain.StateHash == "" {
			t.Errorf("diff %v hash %q, want %v and a hash", plain.Diff, plain.StateHash, wantPlain)
		}
		full := ProxyHostInput{
			Domains: []string{"a.example.com"}, ForwardScheme: "https", ForwardHost: "web", ForwardPort: 8443,
			CertificateID: cert.ID, AccessListID: list.ID, SSLForced: true, HSTS: true, Websockets: true,
		}
		out, err := n.proxyhostAdd().Handler.Plan(ctx, h.env(""), paJSON(t, full))
		if err != nil {
			t.Fatal(err)
		}
		wantFull := []core.DiffLine{
			{Op: "+", Text: "proxy host a.example.com"},
			{Op: " ", Text: "forward https://web:8443"},
			{Op: " ", Text: "certificate " + strconv.Itoa(cert.ID)},
			{Op: " ", Text: "access list " + strconv.Itoa(list.ID)},
			{Op: " ", Text: "force SSL"},
			{Op: " ", Text: "HSTS"},
			{Op: " ", Text: "websockets"},
		}
		if !slices.Equal(out.Diff, wantFull) {
			t.Errorf("diff %v, want %v", out.Diff, wantFull)
		}
		if len(h.npm.ProxyHosts()) != 0 {
			t.Error("a plan created a proxy host")
		}
		again, err := n.proxyhostAdd().Handler.Plan(ctx, h.env(""), paJSON(t, paInput("b.example.com", "a.example.com")))
		if err != nil || again.StateHash != plain.StateHash {
			t.Errorf("a second plan hashes %q (%v), want %q", again.StateHash, err, plain.StateHash)
		}
		other, err := n.proxyhostAdd().Handler.Plan(ctx, h.env(""), paJSON(t, paInput("c.example.com")))
		if err != nil || other.StateHash == plain.StateHash {
			t.Errorf("another domain hashes %q (%v), want a different hash", other.StateHash, err)
		}
	})

	t.Run("refusals", func(t *testing.T) {
		tests := []struct {
			name  string
			setup func(h *thHost)
			in    ProxyHostInput
			field string
		}{
			{name: "invalid input", in: ProxyHostInput{}, field: "/domains"},
			{name: "domain already has a proxy host", setup: func(h *thHost) {
				h.npm.SeedProxyHost(npmapi.ProxyHost{DomainNames: []string{"x.example.com", "a.example.com"}, ForwardScheme: "http", ForwardHost: "old", ForwardPort: 80})
			}, in: paInput("b.example.com", "a.example.com"), field: "/domains/1"},
			{name: "certificate missing", in: func() ProxyHostInput {
				in := paInput("a.example.com")
				in.CertificateID = 99
				return in
			}()},
			{name: "access list missing", in: func() ProxyHostInput {
				in := paInput("a.example.com")
				in.AccessListID = 99
				return in
			}()},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				h, n := paHost(t)
				if tt.setup != nil {
					tt.setup(h)
				}
				_, err := n.proxyhostAdd().Handler.Plan(ctx, h.env(""), paJSON(t, tt.in))
				coded := paRefused(t, err, errcode.InvalidInput)
				if tt.field != "" && !slices.ContainsFunc(coded.Fields, func(f errcode.FieldError) bool { return f.Path == tt.field }) {
					t.Errorf("fields %v, want one at %s", coded.Fields, tt.field)
				}
			})
		}
	})
}

func TestPaReservedUpstreams(t *testing.T) {
	ctx := context.Background()
	h, n := paHost(t)
	tests := []struct {
		host    string
		port    int
		parent  string
		refused bool
	}{
		{"127.0.0.1", 81, "", true},
		{"localhost", 81, "", true},
		{"ip6-localhost", 81, "", true},
		{"4fa6e0f0c678", 81, "", true},
		{"npm", 81, "", true},
		{"npm", 81, paAdmin, false},
		{"npm", 8080, "", true},
		{"npm", 3000, "", true},
		{"172.18.0.2", 3000, "", true},
		{"172.18.0.2", 3000, paPanel, true},
		{"172.18.0.2", 3000, paAdmin, false},
		{"172.18.0.2", 81, "", true},
		{"172.31.255.1", 9443, "", true},
		{"172.31.255.2", 9443, "", true},
		{"127.0.0.1", 81, paAdmin, false},
		{"172.18.0.2", 81, paAdmin, false},
		{"127.0.0.1", 81, paPanel, true},
		{"172.31.255.1", 9443, paPanel, false},
		{"172.31.255.2", 9443, paPanel, false},
		{"172.31.255.1", 9443, paAdmin, true},
		{"10.0.0.5", 81, "", false},
		{"web", 8080, "", false},
		{"127.1", 81, "", true},
		{"2887778049", 9443, "", true},
		{"0x7f.1", 81, paAdmin, false},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s:%d as child of %q", tt.host, tt.port, tt.parent), func(t *testing.T) {
			in := paInput("x.example.com")
			in.ForwardHost, in.ForwardPort = tt.host, tt.port
			_, err := n.proxyhostAdd().Handler.Plan(ctx, paEnv(h, "", tt.parent), paJSON(t, in))
			if tt.refused {
				_ = paRefused(t, err, errcode.NPMReserved)
				return
			}
			if err != nil {
				t.Fatalf("refused: %v", err)
			}
		})
	}
	in := paInput("x.example.com")
	in.ForwardHost, in.ForwardPort = "127.0.0.1", 81
	_, err := paApply(t, h, n, "c1", "", in)
	_ = paRefused(t, err, errcode.NPMReserved)
	if len(h.npm.ProxyHosts()) != 0 || len(h.journal.effects()) != 0 {
		t.Error("a refused apply created a proxy host or journaled an effect")
	}
}

func TestPaApply(t *testing.T) {
	ctx := context.Background()

	t.Run("creates the proxy host", func(t *testing.T) {
		h, n := paHost(t)
		out, err := paApply(t, h, n, "c1", "", paInput("b.example.com", "a.example.com"))
		if err != nil {
			t.Fatal(err)
		}
		hosts := h.npm.ProxyHosts()
		if len(hosts) != 1 {
			t.Fatalf("%d proxy hosts, want 1", len(hosts))
		}
		created := hosts[0]
		if !slices.Equal(created.DomainNames, []string{"a.example.com", "b.example.com"}) || created.ForwardHost != "web" || created.ForwardPort != 8080 ||
			len(created.Locations) != 1 || created.Locations[0] != proxy.Location("http", "web", 8080, false) {
			t.Errorf("created %+v, want the domains sorted, the target and the custom location", created)
		}
		if out.ID != created.ID || !slices.Equal(out.Domains, []string{"a.example.com", "b.example.com"}) || out.ForwardHost != "web" || out.Reserved != "" {
			t.Errorf("output %+v, want the created host unreserved", out)
		}
		effects := h.journal.effects()
		if len(effects) != 1 {
			t.Fatalf("%d effects, want 1", len(effects))
		}
		e := effects[0]
		if e.Kind != core.EffectCreate || e.Resource != "npm.proxyhost:"+strconv.Itoa(created.ID) || e.State != core.EffectMade || e.Local || e.CommitWait != commitWait {
			t.Errorf("effect %+v, want a made non-local create of npm.proxyhost:%d with commit wait %s", e, created.ID, commitWait)
		}
		if e.Recovery == nil || e.Recovery.Kind != "name" || e.Recovery.Value != "a.example.com,b.example.com" || string(e.Before.State) != "null" {
			t.Errorf("recovery %+v before %s, want the sorted domain set and the null state", e.Recovery, e.Before.State)
		}
		if e.Post == nil || string(e.Post.State) != string(proxyHostState(h.env(""), created)) {
			t.Errorf("post %+v, want the proxy host's state document", e.Post)
		}
		if rows := reservedCount(t, h); rows != 0 {
			t.Errorf("npm_reserved holds %d rows, want none for an unreserved change", rows)
		}
	})

	t.Run("child of the admin exposure records its objects", func(t *testing.T) {
		h, n := paHost(t)
		cert := h.npm.SeedCertificate(npmapi.Certificate{NiceName: "darkpanel-c0-1", Provider: "letsencrypt", DomainNames: []string{"npm.example.com"}})
		in := ProxyHostInput{
			Domains: []string{"npm.example.com"}, ForwardScheme: "http", ForwardHost: "127.0.0.1", ForwardPort: 81,
			CertificateID: cert.ID, SSLForced: true, HSTS: true,
		}
		out, err := paApply(t, h, n, "c1", paAdmin, in)
		if err != nil {
			t.Fatal(err)
		}
		if out.Reserved != paAdmin {
			t.Errorf("output reserved %q, want %s", out.Reserved, paAdmin)
		}
		for kind, id := range map[string]int{"proxy_host": out.ID, "certificate": cert.ID} {
			row, ok, err := reservedOf(ctx, h.db.Read(), kind, id)
			want := reservedRow{Kind: kind, ObjectID: id, Owner: paAdmin, Exposure: "npm.example.com", ChangeID: "c1"}
			if err != nil || !ok || row != want {
				t.Errorf("reserved %s %d: %+v (found %v, %v), want %+v", kind, id, row, ok, err, want)
			}
		}
	})

	t.Run("child of the panel exposure sets the read timeout", func(t *testing.T) {
		h, n := paHost(t)
		in := ProxyHostInput{Domains: []string{"panel.example.com"}, ForwardScheme: "https", ForwardHost: "172.31.255.1", ForwardPort: 9443}
		out, err := paApply(t, h, n, "c1", paPanel, in)
		if err != nil {
			t.Fatal(err)
		}
		want := "proxy_set_header X-Darkpanel-Client $realip_remote_addr;\nproxy_read_timeout 3600s;"
		if got := h.npm.ProxyHosts()[0].Locations[0].AdvancedConfig; got != want {
			t.Errorf("location configuration %q, want %q", got, want)
		}
		row, ok, err := reservedOf(ctx, h.db.Read(), "proxy_host", out.ID)
		if err != nil || !ok || row.Owner != paPanel || row.Exposure != "panel.example.com" {
			t.Errorf("reserved row %+v (found %v, %v), want owner %s and exposure panel.example.com", row, ok, err, paPanel)
		}
	})

	t.Run("NPM refuses", func(t *testing.T) {
		h, n := paHost(t)
		h.npm.Fail("POST", "/api/nginx/proxy-hosts", fake.Fault{Status: 400})
		_, err := paApply(t, h, n, "c1", paAdmin, ProxyHostInput{Domains: []string{"npm.example.com"}, ForwardScheme: "http", ForwardHost: "127.0.0.1", ForwardPort: 81})
		_ = paRefused(t, err, errcode.InvalidInput)
		if effects := h.journal.effects(); len(effects) != 1 || effects[0].State != core.EffectSkipped {
			t.Errorf("effects %+v, want one skipped", effects)
		}
		if rows := reservedCount(t, h); rows != 0 {
			t.Errorf("npm_reserved holds %d rows after a refusal", rows)
		}
	})

	t.Run("NPM fails", func(t *testing.T) {
		h, n := paHost(t)
		h.npm.Fail("POST", "/api/nginx/proxy-hosts", fake.Fault{Status: 500})
		_, err := paApply(t, h, n, "c1", "", paInput("a.example.com"))
		if err == nil || core.IsDefinite(err) {
			t.Fatalf("error %v, want an uncertain failure", err)
		}
		if effects := h.journal.effects(); len(effects) != 1 || effects[0].State != core.EffectIntended {
			t.Errorf("effects %+v, want one left intended", effects)
		}
	})
}

func reservedCount(t *testing.T, h *thHost) int {
	t.Helper()
	var n int
	if err := h.db.Read().QueryRowContext(context.Background(), "SELECT count(*) FROM npm_reserved").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestPaLostResponse: NPM commits the proxy host and the answer is lost; the
// effect stays intended and Observe finds the host by its domain set.
func TestPaLostResponse(t *testing.T) {
	ctx := context.Background()
	h, n := paHost(t)
	h.npm.Fail("POST", "/api/nginx/proxy-hosts", fake.Fault{AfterCommit: true})
	_, err := paApply(t, h, n, "c1", "", paInput("b.example.com", "a.example.com"))
	if err == nil || core.IsDefinite(err) {
		t.Fatalf("error %v, want an uncertain failure", err)
	}
	hosts := h.npm.ProxyHosts()
	if len(hosts) != 1 {
		t.Fatalf("%d proxy hosts, want the committed one", len(hosts))
	}
	e := h.journal.effects()[0]
	if e.State != core.EffectIntended || e.Resource != "npm.proxyhost:?a.example.com,b.example.com" {
		t.Fatalf("effect %+v, want one intended creation of the domain set", e)
	}
	created, err := proxyHostCreatedOn(hosts[0].CreatedOn)
	if err != nil {
		t.Fatal(err)
	}
	def := n.proxyhostAdd()

	e.CommitBy = created.Add(-time.Second).Add(e.CommitWait)
	obs, err := def.Handler.Observe(ctx, h.env("c1"), e)
	wantResource := "npm.proxyhost:" + strconv.Itoa(hosts[0].ID)
	if err != nil || obs.Matches != 1 || obs.Resource != wantResource || !obs.Exclusive || string(obs.Current.State) != string(proxyHostState(h.env(""), hosts[0])) {
		t.Errorf("observation %+v (%v), want one exclusive match of %s", obs, err, wantResource)
	}

	e.CommitBy = created.Add(time.Hour).Add(e.CommitWait)
	obs, err = def.Handler.Observe(ctx, h.env("c1"), e)
	if err != nil || obs.Matches != 0 || string(obs.Current.State) != "null" {
		t.Errorf("a host created before the intent: %+v (%v), want no match and the null state", obs, err)
	}

	e.CommitBy = created.Add(-time.Second).Add(e.CommitWait)
	e.Resource, e.Recovery = "npm.proxyhost:?a.example.com", &core.RecoveryID{Kind: "name", Value: "a.example.com"}
	obs, err = def.Handler.Observe(ctx, h.env("c1"), e)
	if err != nil || obs.Matches != 0 {
		t.Errorf("a subset of the domains: %+v (%v), want no match, the domain set being exact", obs, err)
	}
}

func TestPaObserveMade(t *testing.T) {
	ctx := context.Background()
	h, n := paHost(t)
	out, err := paApply(t, h, n, "c1", "", paInput("a.example.com"))
	if err != nil {
		t.Fatal(err)
	}
	def := n.proxyhostAdd()
	e := core.Effect{ChangeID: "c1", Kind: core.EffectCreate, Resource: "npm.proxyhost:" + strconv.Itoa(out.ID), State: core.EffectMade}
	obs, err := def.Handler.Observe(ctx, h.env("c1"), e)
	if err != nil || obs.Matches != 1 || string(obs.Current.State) != string(proxyHostState(h.env(""), h.npm.ProxyHosts()[0])) {
		t.Errorf("observation %+v (%v), want the host's state document", obs, err)
	}
	e.Resource = "npm.proxyhost:999"
	obs, err = def.Handler.Observe(ctx, h.env("c1"), e)
	if err != nil || obs.Matches != 1 || string(obs.Current.State) != "null" {
		t.Errorf("absent host: %+v (%v), want the null state", obs, err)
	}
}

func TestPaInverse(t *testing.T) {
	ctx := context.Background()
	admin := ProxyHostInput{Domains: []string{"npm.example.com"}, ForwardScheme: "http", ForwardHost: "127.0.0.1", ForwardPort: 81}

	t.Run("deletes the host and drops its reservations", func(t *testing.T) {
		h, n := paHost(t)
		cert := h.npm.SeedCertificate(npmapi.Certificate{NiceName: "c", Provider: "letsencrypt", DomainNames: []string{"npm.example.com"}})
		in := admin
		in.CertificateID, in.SSLForced = cert.ID, true
		out, err := paApply(t, h, n, "c1", paAdmin, in)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := n.proxyhostAdd().Handler.Inverse(ctx, h.env("u1"), h.journal.effects()[0]); err != nil {
			t.Fatal(err)
		}
		if len(h.npm.ProxyHosts()) != 0 {
			t.Error("the proxy host is still there")
		}
		if _, ok, _ := reservedOf(ctx, h.db.Read(), "proxy_host", out.ID); ok {
			t.Error("the proxy host is still reserved")
		}
		if _, ok, _ := reservedOf(ctx, h.db.Read(), "certificate", cert.ID); ok {
			t.Error("the certificate is still reserved")
		}
	})

	t.Run("a host that is already gone is done", func(t *testing.T) {
		h, n := paHost(t)
		if _, err := paApply(t, h, n, "c1", "", paInput("a.example.com")); err != nil {
			t.Fatal(err)
		}
		e := h.journal.effects()[0]
		def := n.proxyhostAdd()
		if _, err := def.Handler.Inverse(ctx, h.env("u1"), e); err != nil {
			t.Fatal(err)
		}
		if _, err := def.Handler.Inverse(ctx, h.env("u1"), e); err != nil {
			t.Errorf("a second inverse: %v", err)
		}
	})

	t.Run("NPM refuses the delete", func(t *testing.T) {
		h, n := paHost(t)
		out, err := paApply(t, h, n, "c1", "", paInput("a.example.com"))
		if err != nil {
			t.Fatal(err)
		}
		h.npm.Fail("DELETE", "/api/nginx/proxy-hosts/"+strconv.Itoa(out.ID), fake.Fault{Status: 403})
		_, err = n.proxyhostAdd().Handler.Inverse(ctx, h.env("u1"), h.journal.effects()[0])
		if err == nil || !core.IsDefinite(err) {
			t.Errorf("error %v, want a definite refusal", err)
		}
		if len(h.npm.ProxyHosts()) != 1 {
			t.Error("the proxy host was deleted despite the refusal")
		}
	})

	t.Run("NPM fails", func(t *testing.T) {
		h, n := paHost(t)
		out, err := paApply(t, h, n, "c1", "", paInput("a.example.com"))
		if err != nil {
			t.Fatal(err)
		}
		h.npm.Fail("DELETE", "/api/nginx/proxy-hosts/"+strconv.Itoa(out.ID), fake.Fault{Status: 500})
		_, err = n.proxyhostAdd().Handler.Inverse(ctx, h.env("u1"), h.journal.effects()[0])
		if err == nil || core.IsDefinite(err) {
			t.Errorf("error %v, want an uncertain failure", err)
		}
	})
}

func TestPaVerify(t *testing.T) {
	ctx := context.Background()
	h, n := paHost(t)
	def := n.proxyhostAdd()
	in := paJSON(t, paInput("a.example.com"))
	if def.Handler.Verify(ctx, h.env("c1"), in, json.RawMessage(`{}`)) == nil {
		t.Error("verify passed before the proxy host exists")
	}
	if _, err := paApply(t, h, n, "c1", "", paInput("a.example.com")); err != nil {
		t.Fatal(err)
	}
	if err := def.Handler.Verify(ctx, h.env("c1"), in, json.RawMessage(`{}`)); err != nil {
		t.Errorf("verify after the apply: %v", err)
	}
}

func TestPaResolved(t *testing.T) {
	ctx := context.Background()
	h := newTHHost(t)
	def := h.nm().proxyhostAdd()
	if def.Handler.Resolved == nil {
		t.Fatal("the action has no resolver")
	}
	creation := func(state core.EffectState) core.Effect {
		return core.Effect{
			ChangeID: "c1", Seq: 2, Kind: core.EffectCreate, Resource: "npm.proxyhost:?a.example.com,b.example.com",
			Recovery: &core.RecoveryID{Kind: "name", Value: "a.example.com,b.example.com"}, State: state,
		}
	}
	resolve := func(state core.EffectState, resolution string) {
		t.Helper()
		err := h.db.Write(ctx, func(tx *sql.Tx) error { return def.Handler.Resolved(ctx, tx, creation(state), resolution) })
		if err != nil {
			t.Fatal(err)
		}
	}
	resolve(core.EffectMade, "reconcile")
	resolve(core.EffectAccepted, "accept")
	if got, err := watched(ctx, h.db.Read(), "proxy_host"); err != nil || len(got) != 0 {
		t.Fatalf("watched %v (%v) after a made and an accepted creation, want none", got, err)
	}
	resolve(core.EffectSkipped, "reconcile")
	got, err := watched(ctx, h.db.Read(), "proxy_host")
	if err != nil || !got["a.example.com,b.example.com"] {
		t.Errorf("watched %v (%v), want the skipped creation's domain set", got, err)
	}
}

func TestPaDefinition(t *testing.T) {
	_, n := paHost(t)
	def := n.proxyhostAdd()
	s := def.Spec
	if s.Name != "npm.proxyhost.add" || s.Version != 1 || s.Class != core.ClassCompensable || s.Side != core.SideAgent ||
		!slices.Equal(s.LockKeys, []string{"npm"}) {
		t.Errorf("spec %+v", s)
	}
	if def.Handler.Inverse == nil || def.Handler.Observe == nil || def.Handler.Resolved == nil {
		t.Errorf("inverse %v observe %v resolved %v, want all three", def.Handler.Inverse != nil, def.Handler.Observe != nil, def.Handler.Resolved != nil)
	}
	if _, err := core.NewRegistry(paModule{def}); err != nil {
		t.Errorf("the registry refuses the action: %v", err)
	}
	v := schema.NewValidator()
	valid := paJSON(t, paInput("a.example.com"))
	if err := v.ValidateInput(def, valid); err != nil {
		t.Errorf("a valid input was refused: %v", err)
	}
	var withExtra map[string]any
	if err := json.Unmarshal(valid, &withExtra); err != nil {
		t.Fatal(err)
	}
	withExtra["x"] = 1
	err := v.ValidateInput(def, paJSON(t, withExtra))
	var coded *errcode.Error
	if !errors.As(err, &coded) || !slices.ContainsFunc(coded.Fields, func(f errcode.FieldError) bool { return f.Path == "/x" }) {
		t.Errorf("an unknown field: %v, want a refusal naming /x", err)
	}
}

// TestW10NvHostProblemSaysWhyAHostIsRefused: an IPv6 literal is refused because NPM writes the forward host into
// proxy_pass without brackets, and a zone is refused for its own reason.
func TestW10NvHostProblemSaysWhyAHostIsRefused(t *testing.T) {
	tests := []struct {
		host string
		want string // a word the problem holds; "" accepted
	}{
		{"fe80::1%eth0", "zone"},
		{"2001:db8::1", "brackets"},
		{"::1", "brackets"},
		{"::ffff:10.0.0.5", "brackets"},
		{"10.0.0.5", ""},
		{"web", ""},
		{"web.internal", ""},
	}
	for _, tc := range tests {
		got := proxyhostAddHostProblem(tc.host)
		if (tc.want == "") != (got == "") || !strings.Contains(got, tc.want) {
			t.Errorf("proxyhostAddHostProblem(%q) = %q, want a problem naming %q (none when empty)", tc.host, got, tc.want)
		}
	}
}

// TestW10NvAddEngineReadFailureIsNotARefusal: a failed read of the Engine says nothing about the upstream, so neither
// the plan nor the apply marks it as a refusal.
func TestW10NvAddEngineReadFailureIsNotARefusal(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name string
		err  error
		code errcode.Code // "": the error carries the Engine's own answer
	}{
		{"the Engine is unreachable", dockerapi.ErrUnavailable, errcode.DockerEngineUnavailable},
		{"the Engine answers 500", thAPIError(500, "boom"), ""},
	}
	for _, tc := range tests {
		for _, stage := range []string{"plan", "apply"} {
			t.Run(tc.name+" at "+stage, func(t *testing.T) {
				h, n := paHost(t)
				h.docker.Fail("InspectContainer", tc.err)
				var err error
				if stage == "plan" {
					_, err = n.proxyhostAdd().Handler.Plan(ctx, h.env(""), paJSON(t, paInput("a.example.com")))
				} else {
					_, err = paApply(t, h, n, "c1", "", paInput("a.example.com"))
				}
				if err == nil {
					t.Fatal("the read failure was swallowed")
				}
				if core.IsDefinite(err) {
					t.Errorf("error %v is marked Definite, though a failed read refuses nothing", err)
				}
				var coded *errcode.Error
				var answer *dockerapi.APIError
				switch {
				case tc.code != "" && (!errors.As(err, &coded) || coded.Code != tc.code):
					t.Errorf("error %v, want code %s", err, tc.code)
				case tc.code == "" && !errors.As(err, &answer):
					t.Errorf("error %v does not carry the Engine's answer", err)
				}
				if len(h.npm.ProxyHosts()) != 0 || len(h.journal.effects()) != 0 {
					t.Error("a failed read created a proxy host or journaled an effect")
				}
			})
		}
	}
}

// TestW10NvAddRefusesASecondAdminExposure: while one exposure of NPM's admin UI is recorded, another npm.admin.expose
// is refused with npm.reserved naming the hostname already exposed, and nothing is created for it. The panel's own
// exposure is not an admin exposure, and the refusal ends with the exposure it names.
func TestW10NvAddRefusesASecondAdminExposure(t *testing.T) {
	ctx := context.Background()
	admin := func(domain string) ProxyHostInput {
		return ProxyHostInput{Domains: []string{domain}, ForwardScheme: "http", ForwardHost: "127.0.0.1", ForwardPort: 81}
	}
	h, n := paHost(t)
	if _, err := paApply(t, h, n, "c1", paAdmin, admin("npm.example.com")); err != nil {
		t.Fatal(err)
	}
	effects, reserved := len(h.journal.effects()), reservedCount(t, h)

	for _, stage := range []string{"plan", "apply"} {
		t.Run("a second admin exposure at "+stage, func(t *testing.T) {
			var err error
			if stage == "plan" {
				_, err = n.proxyhostAdd().Handler.Plan(ctx, paEnv(h, "", paAdmin), paJSON(t, admin("npm2.example.com")))
			} else {
				_, err = paApply(t, h, n, "c2", paAdmin, admin("npm2.example.com"))
			}
			coded := paRefused(t, err, errcode.NPMReserved)
			if !strings.Contains(coded.Detail, "npm.example.com") {
				t.Errorf("detail %q does not name the hostname already exposed", coded.Detail)
			}
			if len(h.npm.ProxyHosts()) != 1 || len(h.journal.effects()) != effects || reservedCount(t, h) != reserved {
				t.Error("a refused exposure created a proxy host, journaled an effect or reserved an object")
			}
		})
	}

	t.Run("the panel's exposure is allowed", func(t *testing.T) {
		panel := ProxyHostInput{Domains: []string{"panel.example.com"}, ForwardScheme: "http", ForwardHost: "172.31.255.1", ForwardPort: 9443}
		if _, err := paApply(t, h, n, "c3", paPanel, panel); err != nil {
			t.Errorf("the panel exposure was refused: %v", err)
		}
	})

	t.Run("a released exposure allows another", func(t *testing.T) {
		err := h.db.Write(ctx, func(tx *sql.Tx) error { return proxyHostEditRelease(ctx, tx, "npm.example.com", paAdmin) })
		if err != nil {
			t.Fatal(err)
		}
		if _, err := paApply(t, h, n, "c4", paAdmin, admin("npm2.example.com")); err != nil {
			t.Errorf("the admin exposure was refused after the first was released: %v", err)
		}
	})
}

// TestW10NvInverseReleasesOnlyItsOwnReservations: the undo of one add drops the reservations that add made, and
// keeps those other exposures hold, even when they were recorded under the same change.
func TestW10NvInverseReleasesOnlyItsOwnReservations(t *testing.T) {
	ctx := context.Background()
	h, n := paHost(t)
	cert := h.npm.SeedCertificate(npmapi.Certificate{NiceName: "c", Provider: "letsencrypt", DomainNames: []string{"npm.example.com"}})
	in := ProxyHostInput{Domains: []string{"npm.example.com"}, ForwardScheme: "http", ForwardHost: "127.0.0.1", ForwardPort: 81,
		CertificateID: cert.ID, SSLForced: true}
	out, err := paApply(t, h, n, "c1", paAdmin, in)
	if err != nil {
		t.Fatal(err)
	}
	others := []reservedRow{
		{Kind: "proxy_host", ObjectID: out.ID + 100, Owner: paPanel, Exposure: "panel.example.com", ChangeID: "c1"},
		{Kind: "certificate", ObjectID: cert.ID + 100, Owner: paPanel, Exposure: "panel.example.com", ChangeID: "c1"},
	}
	err = h.db.Write(ctx, func(tx *sql.Tx) error {
		for _, r := range others {
			if err := addReserved(ctx, tx, r); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := n.proxyhostAdd().Handler.Inverse(ctx, h.env("u1"), h.journal.effects()[0]); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := reservedOf(ctx, h.db.Read(), "proxy_host", out.ID); ok {
		t.Error("the undone host is still reserved")
	}
	if _, ok, _ := reservedOf(ctx, h.db.Read(), "certificate", cert.ID); ok {
		t.Error("the certificate the undone add reserved is still reserved")
	}
	for _, r := range others {
		if _, ok, _ := reservedOf(ctx, h.db.Read(), r.Kind, r.ObjectID); !ok {
			t.Errorf("the undo dropped the reservation of %s %d, which another exposure holds", r.Kind, r.ObjectID)
		}
	}
}
