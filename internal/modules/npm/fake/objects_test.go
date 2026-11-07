package fake

import (
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/modules/npm/npmapi"
)

func TestFkProxyHostDomainsAreUnique(t *testing.T) {
	s := New(t)
	c, _ := fkLogin(t, s)
	ctx := t.Context()
	a, err := c.CreateProxyHost(ctx, fkHost("a.example.com", "b.example.com"))
	if err != nil {
		t.Fatal(err)
	}
	if a.ID != 1 || a.CreatedOn == "" || a.ModifiedOn == "" {
		t.Errorf("created %+v, want id 1 with created_on and modified_on", a)
	}
	_, err = c.CreateProxyHost(ctx, fkHost("c.example.com", "b.example.com"))
	if ae := fkAPIError(t, err); ae.Status != http.StatusBadRequest || !npmapi.IsClientError(err) {
		t.Errorf("a create with a taken domain: %v, want 400", err)
	}
	if n := len(s.ProxyHosts()); n != 1 {
		t.Errorf("%d proxy hosts after a refused create, want 1", n)
	}
	if _, err := c.UpdateProxyHost(ctx, a.ID, fkHost("a.example.com")); err != nil {
		t.Fatalf("an update that keeps its own domain: %v", err)
	}
	b, err := c.CreateProxyHost(ctx, fkHost("b.example.com"))
	if err != nil {
		t.Fatalf("a create for a domain the update freed: %v", err)
	}
	if _, err := c.UpdateProxyHost(ctx, b.ID, fkHost("a.example.com")); fkAPIError(t, err).Status != http.StatusBadRequest {
		t.Errorf("an update onto another host's domain: %v, want 400", err)
	}
	if _, err := c.UpdateProxyHost(ctx, 99, fkHost("z.example.com")); !npmapi.IsNotFound(err) {
		t.Errorf("an update of an absent host: %v, want 404", err)
	}
}

func TestFkProxyHostKeepsWhatItWasGiven(t *testing.T) {
	s := New(t)
	c, _ := fkLogin(t, s)
	ctx := t.Context()
	body := npmapi.ProxyHostBody{
		DomainNames: []string{"app.example.com"}, ForwardScheme: "https", ForwardHost: "web", ForwardPort: 8443, CertificateID: 7, AccessListID: 3,
		SSLForced: true, HSTSEnabled: true, AllowWebsocketUpgrade: true, AdvancedConfig: "client_max_body_size 1m;",
		Locations: []npmapi.Location{{Path: "/", ForwardScheme: "https", ForwardHost: "web", ForwardPort: 8443, AdvancedConfig: "proxy_set_header X-Darkpanel-Client $realip_remote_addr;"}},
	}
	created, err := c.CreateProxyHost(ctx, body)
	if err != nil {
		t.Fatal(err)
	}
	want := npmapi.ProxyHost{
		ID: created.ID, CreatedOn: created.CreatedOn, ModifiedOn: created.ModifiedOn, DomainNames: body.DomainNames, ForwardScheme: "https", ForwardHost: "web",
		ForwardPort: 8443, CertificateID: 7, AccessListID: 3, SSLForced: true, HSTSEnabled: true, AllowWebsocketUpgrade: true,
		Locations: body.Locations, AdvancedConfig: "client_max_body_size 1m;",
	}
	if !reflect.DeepEqual(created, want) {
		t.Errorf("created %+v, want %+v", created, want)
	}
	got, err := c.ProxyHost(ctx, created.ID)
	if err != nil || !reflect.DeepEqual(got, created) {
		t.Errorf("read back %+v, %v, want the created object", got, err)
	}
	list, err := c.ProxyHosts(ctx)
	if err != nil || len(list) != 1 || !reflect.DeepEqual(list[0], created) {
		t.Errorf("listed %+v, %v, want the created object", list, err)
	}
}

func TestFkProxyHostValidation(t *testing.T) {
	s := New(t)
	_, tok := fkLogin(t, s)
	for name, body := range map[string]string{
		"no domains":                         `{"domain_names":[],"forward_scheme":"http","forward_host":"web","forward_port":80}`,
		"a scheme other than http and https": `{"domain_names":["a.example.com"],"forward_scheme":"ftp","forward_host":"web","forward_port":80}`,
		"no forward host":                    `{"domain_names":["a.example.com"],"forward_scheme":"http","forward_host":"","forward_port":80}`,
		"port 0":                             `{"domain_names":["a.example.com"],"forward_scheme":"http","forward_host":"web","forward_port":0}`,
		"port 65536":                         `{"domain_names":["a.example.com"],"forward_scheme":"http","forward_host":"web","forward_port":65536}`,
		"a body that is not JSON":            `{`,
	} {
		if status, _ := fkRaw(t, s, http.MethodPost, "/api/nginx/proxy-hosts", tok, body); status != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", name, status)
		}
	}
	if n := len(s.ProxyHosts()); n != 0 {
		t.Errorf("refused creates stored %d proxy hosts", n)
	}
}

func TestFkAccessListsKeepNamesAndExpandClientsOnRequest(t *testing.T) {
	s := New(t)
	c, tok := fkLogin(t, s)
	ctx := t.Context()
	body := npmapi.AccessListBody{
		Name: "darkpanel-c1-2", SatisfyAny: true,
		Clients: []npmapi.AccessClient{{Address: "203.0.113.0/24", Directive: "allow"}, {Address: "198.51.100.7", Directive: "deny"}},
	}
	a, err := c.CreateAccessList(ctx, body)
	if err != nil {
		t.Fatal(err)
	}
	b, err := c.CreateAccessList(ctx, body)
	if err != nil {
		t.Fatalf("a second list with the same name: %v, want it kept as given", err)
	}
	if a.ID != 1 || b.ID != 2 || a.Name != body.Name || a.CreatedOn == "" || !a.SatisfyAny || !reflect.DeepEqual(a.Clients, body.Clients) {
		t.Errorf("created %+v and %+v, want ids 1 and 2 with the given name, flag and clients", a, b)
	}
	list, err := c.AccessLists(ctx)
	if err != nil || len(list) != 2 || !reflect.DeepEqual(list[0].Clients, body.Clients) {
		t.Errorf("listed %+v, %v, want both with their clients", list, err)
	}
	if _, raw := fkRaw(t, s, http.MethodGet, "/api/nginx/access-lists", tok, ""); strings.Contains(raw, `"clients"`) {
		t.Errorf("a list without expand=clients carries clients: %s", raw)
	}
	if _, raw := fkRaw(t, s, http.MethodGet, "/api/nginx/access-lists?expand=clients", tok, ""); !strings.Contains(raw, `"clients"`) {
		t.Errorf("a list with expand=clients lacks clients: %s", raw)
	}
	for name, bad := range map[string]string{
		"no name":                               `{"name":"","clients":[]}`,
		"a directive other than allow and deny": `{"name":"x","clients":[{"address":"203.0.113.7","directive":"block"}]}`,
		"an empty address":                      `{"name":"x","clients":[{"address":"","directive":"allow"}]}`,
	} {
		if status, _ := fkRaw(t, s, http.MethodPost, "/api/nginx/access-lists", tok, bad); status != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", name, status)
		}
	}
	if n := len(s.AccessLists()); n != 2 {
		t.Errorf("%d access lists after the refused creates, want 2", n)
	}
}

func TestFkCertificatesTakeTheirDomainsAsNiceNameAndRefuseLetsencryptMeta(t *testing.T) {
	s := New(t)
	c, tok := fkLogin(t, s)
	ctx := t.Context()
	sent, err := c.CreateCertificate(ctx, npmapi.CertificateBody{
		NiceName: "darkpanel-c1-3", DomainNames: []string{"app.example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if sent.ID != 1 || sent.NiceName != "app.example.com" || sent.Provider != "letsencrypt" || sent.CreatedOn == "" || sent.ExpiresOn == "" ||
		!reflect.DeepEqual(sent.DomainNames, []string{"app.example.com"}) {
		t.Errorf("created %+v, want id 1, the domain as nice_name whatever the request sent, provider letsencrypt and both timestamps", sent)
	}
	unnamed, err := c.CreateCertificate(ctx, npmapi.CertificateBody{
		DomainNames: []string{"b.example.com", "a.example.com"},
	})
	if err != nil || unnamed.NiceName != "b.example.com, a.example.com" || !reflect.DeepEqual(unnamed.DomainNames, []string{"b.example.com", "a.example.com"}) {
		t.Errorf("a certificate without a name: %+v, %v, want the domains joined by \", \" in the order given", unnamed, err)
	}
	if got := s.Certificates(); len(got) != 2 || got[0].NiceName != "app.example.com" || got[1].NiceName != "b.example.com, a.example.com" {
		t.Errorf("stored certificates %+v, want the same nice_names as the answers", got)
	}
	if _, err := c.CreateCertificate(ctx, npmapi.CertificateBody{
		DomainNames: []string{"app.example.com"},
	}); err != nil {
		t.Errorf("a second certificate for the same domain: %v, want it allowed", err)
	}
	for name, bad := range map[string]string{
		"a contact email in meta": `{"provider":"letsencrypt","domain_names":["a.example.com"],"meta":{"letsencrypt_email":"ops@example.com"}}`,
		"an agreement in meta":    `{"provider":"letsencrypt","domain_names":["a.example.com"],"meta":{"letsencrypt_agree":true}}`,
		"another provider":        `{"provider":"other","domain_names":["a.example.com"]}`,
		"no domains":              `{"provider":"letsencrypt","domain_names":[]}`,
	} {
		if status, _ := fkRaw(t, s, http.MethodPost, "/api/nginx/certificates", tok, bad); status != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", name, status)
		}
	}
	if status, _ := fkRaw(t, s, http.MethodPost, "/api/nginx/certificates", tok, `{"provider":"letsencrypt","domain_names":["c.example.com"]}`); status != http.StatusCreated {
		t.Errorf("the body NPM 2.16.0 accepts: status %d, want 201", status)
	}
	if n := len(s.Certificates()); n != 4 {
		t.Errorf("%d certificates after the refused creates and one accepted raw create, want 4", n)
	}
}

func TestFkDeleteRemovesTheObjectAndAnAbsentOneIsNotFound(t *testing.T) {
	s := New(t)
	c, _ := fkLogin(t, s)
	ctx := t.Context()
	host, err := c.CreateProxyHost(ctx, fkHost("a.example.com"))
	if err != nil {
		t.Fatal(err)
	}
	list, err := c.CreateAccessList(ctx, npmapi.AccessListBody{Name: "office"})
	if err != nil {
		t.Fatal(err)
	}
	cert, err := c.CreateCertificate(ctx, npmapi.CertificateBody{DomainNames: []string{"a.example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteProxyHost(ctx, host.ID); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteAccessList(ctx, list.ID); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteCertificate(ctx, cert.ID); err != nil {
		t.Fatal(err)
	}
	if len(s.ProxyHosts())+len(s.AccessLists())+len(s.Certificates()) != 0 {
		t.Error("deleted objects are still stored")
	}
	if _, err := c.ProxyHost(ctx, host.ID); !npmapi.IsNotFound(err) {
		t.Errorf("a get of a deleted proxy host: %v, want 404", err)
	}
	if _, err := c.AccessList(ctx, list.ID); !npmapi.IsNotFound(err) {
		t.Errorf("a get of a deleted access list: %v, want 404", err)
	}
	if _, err := c.Certificate(ctx, cert.ID); !npmapi.IsNotFound(err) {
		t.Errorf("a get of a deleted certificate: %v, want 404", err)
	}
	if !npmapi.IsNotFound(c.DeleteProxyHost(ctx, host.ID)) || !npmapi.IsNotFound(c.DeleteAccessList(ctx, list.ID)) || !npmapi.IsNotFound(c.DeleteCertificate(ctx, cert.ID)) {
		t.Error("a second delete is not a 404")
	}
	again, err := c.CreateProxyHost(ctx, fkHost("a.example.com"))
	if err != nil || again.ID == host.ID {
		t.Errorf("a create after the delete gave %+v, %v, want a new id and the domain free again", again, err)
	}
}

func TestFkStampsObjectsWithItsClock(t *testing.T) {
	s := New(t)
	clock := coretest.NewFakeClock(time.Date(2026, 11, 2, 10, 0, 0, 123_000_000, time.UTC))
	s.SetClock(clock.Now)
	c, _ := fkLogin(t, s)
	ctx := t.Context()
	const first, second = "2026-11-02T10:00:00.123Z", "2026-11-02T10:01:00.123Z"
	if CreatedOnLayout != "2006-01-02T15:04:05.000Z" {
		t.Fatalf("CreatedOnLayout = %q", CreatedOnLayout)
	}
	host, err := c.CreateProxyHost(ctx, fkHost("a.example.com"))
	if err != nil {
		t.Fatal(err)
	}
	if host.CreatedOn != first || host.ModifiedOn != first {
		t.Errorf("created at %q and modified at %q, want both %q", host.CreatedOn, host.ModifiedOn, first)
	}
	clock.Advance(time.Minute)
	updated, err := c.UpdateProxyHost(ctx, host.ID, fkHost("a.example.com", "b.example.com"))
	if err != nil {
		t.Fatal(err)
	}
	if updated.CreatedOn != first || updated.ModifiedOn != second {
		t.Errorf("after an update created_on %q and modified_on %q, want %q and %q", updated.CreatedOn, updated.ModifiedOn, first, second)
	}
	list, err := c.CreateAccessList(ctx, npmapi.AccessListBody{Name: "office"})
	if err != nil || list.CreatedOn != second {
		t.Errorf("access list created at %q (%v), want %q", list.CreatedOn, err, second)
	}
	cert, err := c.CreateCertificate(ctx, npmapi.CertificateBody{DomainNames: []string{"a.example.com"}})
	if err != nil || cert.CreatedOn != second || cert.ExpiresOn != "2027-01-31T10:01:00.123Z" {
		t.Errorf("certificate created at %q, expiring %q (%v), want %q and 2027-01-31T10:01:00.123Z", cert.CreatedOn, cert.ExpiresOn, err, second)
	}
}
