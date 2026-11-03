package npm

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/npm/npmapi"
)

func lsSetup(t *testing.T) (*thHost, nm) {
	t.Helper()
	h := newTHHost(t)
	h.deployed()
	return h, h.nm()
}

func lsReserve(t *testing.T, h *thHost, kind string, id int, owner string) {
	t.Helper()
	ctx := context.Background()
	err := h.db.Write(ctx, func(tx *sql.Tx) error {
		return addReserved(ctx, tx, reservedRow{Kind: kind, ObjectID: id, Owner: owner, Exposure: "admin.example.com", ChangeID: "c0"})
	})
	if err != nil {
		t.Fatalf("reserve %s %d: %v", kind, id, err)
	}
}

func lsWatch(t *testing.T, h *thHost, kind, identity string) {
	t.Helper()
	ctx := context.Background()
	err := h.db.Write(ctx, func(tx *sql.Tx) error { return addWatch(ctx, tx, kind, identity, "c9", 1) })
	if err != nil {
		t.Fatalf("watch %s %q: %v", kind, identity, err)
	}
}

func lsCode(t *testing.T, err error) errcode.Code {
	t.Helper()
	var ec *errcode.Error
	if !errors.As(err, &ec) {
		t.Fatalf("error = %v, want an *errcode.Error", err)
	}
	return ec.Code
}

func TestLsSpecs(t *testing.T) {
	n := newTHHost(t).nm()
	for _, tc := range []struct {
		def  core.ActionDef
		name string
	}{
		{n.proxyhostList(), "npm.proxyhost.list"},
		{n.accesslistList(), "npm.accesslist.list"},
		{n.certificateList(), "npm.certificate.list"},
	} {
		s := tc.def.Spec
		if s.Name != tc.name || s.Version != 1 || s.Class != core.ClassRead || s.Side != core.SideAgent || len(s.LockKeys) != 0 {
			t.Errorf("%s: spec = %+v", tc.name, s)
		}
		if core.RequiredScope(s) != core.ScopeRead {
			t.Errorf("%s: scope = %s, want read", tc.name, core.RequiredScope(s))
		}
		if tc.def.Handler.Read == nil {
			t.Errorf("%s: no Read handler", tc.name)
		}
	}
}

func TestLsProxyHostsAreMarkedReservedAndUnclaimed(t *testing.T) {
	ctx := context.Background()
	h, n := lsSetup(t)
	list := h.npm.SeedAccessList(npmapi.AccessList{Name: "office", Clients: []npmapi.AccessClient{{Address: "10.1.1.1", Directive: "allow"}}})
	cert := h.npm.SeedCertificate(npmapi.Certificate{NiceName: "web.example.com", Provider: "letsencrypt", DomainNames: []string{"web.example.com"}})
	plain := h.npm.SeedProxyHost(npmapi.ProxyHost{
		DomainNames: []string{"web.example.com"}, ForwardScheme: "https", ForwardHost: "web", ForwardPort: 8443,
		CertificateID: cert.ID, AccessListID: list.ID, SSLForced: true, HSTSEnabled: true, AllowWebsocketUpgrade: true,
	})
	reserved := h.npm.SeedProxyHost(npmapi.ProxyHost{DomainNames: []string{"admin.example.com"}, ForwardScheme: "http", ForwardHost: "127.0.0.1", ForwardPort: 81})
	lost := h.npm.SeedProxyHost(npmapi.ProxyHost{DomainNames: []string{"z.example.com", "y.example.com"}, ForwardScheme: "http", ForwardHost: "app", ForwardPort: 80})
	lsReserve(t, h, "proxy_host", reserved.ID, "npm.admin.expose")
	lsWatch(t, h, "proxy_host", "y.example.com,z.example.com")
	lsWatch(t, h, "access_list", "web.example.com")

	out, err := listProxyHostReader{n: n}.Read(ctx, h.env(""), ProxyHostListInput{})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	want := []ProxyHostView{
		{
			ID: plain.ID, Domains: []string{"web.example.com"}, ForwardScheme: "https", ForwardHost: "web", ForwardPort: 8443,
			CertificateID: cert.ID, AccessListID: list.ID, SSLForced: true, HSTS: true, Websockets: true,
		},
		{ID: reserved.ID, Domains: []string{"admin.example.com"}, ForwardScheme: "http", ForwardHost: "127.0.0.1", ForwardPort: 81, Reserved: "npm.admin.expose"},
		{ID: lost.ID, Domains: []string{"z.example.com", "y.example.com"}, ForwardScheme: "http", ForwardHost: "app", ForwardPort: 80, Unclaimed: true},
	}
	if !reflect.DeepEqual(out.Items, want) {
		t.Errorf("items = %+v\nwant   %+v", out.Items, want)
	}
}

func TestLsAccessListsAreMarkedReservedAndUnclaimed(t *testing.T) {
	ctx := context.Background()
	h, n := lsSetup(t)
	plain := h.npm.SeedAccessList(npmapi.AccessList{
		Name: "office", SatisfyAny: true,
		Clients: []npmapi.AccessClient{{Address: "203.0.113.0/24", Directive: "allow"}, {Address: "203.0.113.9", Directive: "deny"}},
	})
	reserved := h.npm.SeedAccessList(npmapi.AccessList{Name: "admin-only", Clients: []npmapi.AccessClient{{Address: "198.51.100.1", Directive: "allow"}}})
	lost := h.npm.SeedAccessList(npmapi.AccessList{Name: "darkpanel-c9-1", Clients: []npmapi.AccessClient{{Address: "192.0.2.1", Directive: "allow"}}})
	lsReserve(t, h, "access_list", reserved.ID, "panel.exposure.add")
	lsWatch(t, h, "access_list", "darkpanel-c9-1")
	lsWatch(t, h, "certificate", "office")

	out, err := listAccessListReader{n: n}.Read(ctx, h.env(""), AccessListListInput{})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	want := []AccessListView{
		{ID: plain.ID, Name: "office", SatisfyAny: true, Clients: []AccessClientInput{{Address: "203.0.113.0/24", Directive: "allow"}, {Address: "203.0.113.9", Directive: "deny"}}},
		{ID: reserved.ID, Name: "admin-only", Clients: []AccessClientInput{{Address: "198.51.100.1", Directive: "allow"}}, Reserved: "panel.exposure.add"},
		{ID: lost.ID, Name: "darkpanel-c9-1", Clients: []AccessClientInput{{Address: "192.0.2.1", Directive: "allow"}}, Unclaimed: true},
	}
	if !reflect.DeepEqual(out.Items, want) {
		t.Errorf("items = %+v\nwant   %+v", out.Items, want)
	}
}

func TestLsCertificatesAreMarkedReservedAndUnclaimed(t *testing.T) {
	ctx := context.Background()
	h, n := lsSetup(t)
	plain := h.npm.SeedCertificate(npmapi.Certificate{
		NiceName: "web.example.com", Provider: "letsencrypt", DomainNames: []string{"web.example.com", "www.web.example.com"}, ExpiresOn: "2027-03-04 05:06:07",
	})
	reserved := h.npm.SeedCertificate(npmapi.Certificate{NiceName: "admin.example.com", Provider: "letsencrypt", DomainNames: []string{"admin.example.com"}})
	lost := h.npm.SeedCertificate(npmapi.Certificate{
		NiceName: "late.example.com, a.late.example.com", Provider: "letsencrypt", DomainNames: []string{"late.example.com", "a.late.example.com"},
	})
	subset := h.npm.SeedCertificate(npmapi.Certificate{NiceName: "late.example.com", Provider: "letsencrypt", DomainNames: []string{"late.example.com"}})
	lsReserve(t, h, "certificate", reserved.ID, "npm.admin.expose")
	lsWatch(t, h, "certificate", "a.late.example.com,late.example.com")
	lsWatch(t, h, "certificate", "web.example.com")
	lsWatch(t, h, "proxy_host", "web.example.com,www.web.example.com")

	out, err := listCertificateReader{n: n}.Read(ctx, h.env(""), CertificateListInput{})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	want := []CertificateView{
		{ID: plain.ID, Name: "web.example.com", Domains: []string{"web.example.com", "www.web.example.com"}, ExpiresOn: "2027-03-04 05:06:07"},
		{ID: reserved.ID, Name: "admin.example.com", Domains: []string{"admin.example.com"}, Reserved: "npm.admin.expose"},
		{ID: lost.ID, Name: "late.example.com, a.late.example.com", Domains: []string{"late.example.com", "a.late.example.com"}, Unclaimed: true},
		{ID: subset.ID, Name: "late.example.com", Domains: []string{"late.example.com"}},
	}
	if !reflect.DeepEqual(out.Items, want) {
		t.Errorf("items = %+v\nwant   %+v", out.Items, want)
	}
}

func TestLsEmptyListsEncodeAsEmptyArrays(t *testing.T) {
	ctx := context.Background()
	h, n := lsSetup(t)
	env := h.env("")
	proxy, err := listProxyHostReader{n: n}.Read(ctx, env, ProxyHostListInput{})
	if err != nil {
		t.Fatal(err)
	}
	access, err := listAccessListReader{n: n}.Read(ctx, env, AccessListListInput{})
	if err != nil {
		t.Fatal(err)
	}
	certs, err := listCertificateReader{n: n}.Read(ctx, env, CertificateListInput{})
	if err != nil {
		t.Fatal(err)
	}
	for name, v := range map[string]any{"proxy hosts": proxy, "access lists": access, "certificates": certs} {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != `{"items":[]}` {
			t.Errorf("%s encode as %s, want {\"items\":[]}", name, b)
		}
	}
}

func TestLsReadsRefuseWithoutAnAnsweringInstance(t *testing.T) {
	ctx := context.Background()
	reads := func(n nm, env core.Env) map[string]func() error {
		return map[string]func() error{
			"proxy hosts":  func() error { _, err := listProxyHostReader{n: n}.Read(ctx, env, ProxyHostListInput{}); return err },
			"access lists": func() error { _, err := listAccessListReader{n: n}.Read(ctx, env, AccessListListInput{}); return err },
			"certificates": func() error { _, err := listCertificateReader{n: n}.Read(ctx, env, CertificateListInput{}); return err },
		}
	}
	bare := newTHHost(t)
	for name, read := range reads(bare.nm(), bare.env("")) {
		if err := read(); lsCode(t, err) != errcode.NotFound {
			t.Errorf("%s without an instance: error = %v, want not_found", name, err)
		}
	}
	h, n := lsSetup(t)
	n.d.api = func(context.Context, core.Env) (npmAPI, error) { return nil, errors.New("npm: connection refused") }
	for name, read := range reads(n, h.env("")) {
		if err := read(); lsCode(t, err) != errcode.NPMUnhealthy {
			t.Errorf("%s with NPM down: error = %v, want npm.unhealthy", name, err)
		}
	}
}
