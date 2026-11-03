package fake

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/modules/npm/npmapi"
)

const (
	fkEmail    = "ops@example.com"
	fkPassword = "correct-horse"
)

// fkRaw sends one request and returns the status and body; an empty token sends no credentials.
func fkRaw(t *testing.T, s *Server, method, path, token, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, s.URL()+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, string(b)
}

// fkLogin seeds the admin when the server has none and returns a client that holds a token, and the token.
func fkLogin(t *testing.T, s *Server) (*npmapi.Client, string) {
	t.Helper()
	if !s.Setup() {
		s.SeedAdmin(fkEmail, fkPassword)
	}
	c := npmapi.New(s.URL(), nil)
	tok, err := c.Token(t.Context(), fkEmail, fkPassword)
	if err != nil {
		t.Fatal(err)
	}
	return c.WithToken(tok), tok
}

func fkAPIError(t *testing.T, err error) *npmapi.APIError {
	t.Helper()
	var ae *npmapi.APIError
	if !errors.As(err, &ae) {
		t.Fatalf("error %v is not an *npmapi.APIError", err)
	}
	return ae
}

func fkHost(domains ...string) npmapi.ProxyHostBody {
	return npmapi.ProxyHostBody{DomainNames: domains, ForwardScheme: "http", ForwardHost: "web", ForwardPort: 8080}
}

func TestFkSetupIsFalseUntilTheFirstUser(t *testing.T) {
	s := New(t)
	c := npmapi.New(s.URL(), nil)
	ctx := t.Context()
	h, err := c.Health(ctx)
	if err != nil || h.Setup || h.Status != "OK" || s.Setup() {
		t.Fatalf("fresh server: health %+v, error %v, Setup() %t; want status OK and setup false", h, err, s.Setup())
	}
	if _, _, ok := s.Admin(); ok {
		t.Fatal("a fresh server reports an admin")
	}
	for name, body := range map[string]string{
		"no admin role":           `{"name":"n","nickname":"n","email":"ops@example.com","roles":[],"is_disabled":false,"auth":{"type":"password","secret":"correct-horse"}}`,
		"no email":                `{"name":"n","nickname":"n","email":"","roles":["admin"],"is_disabled":false,"auth":{"type":"password","secret":"correct-horse"}}`,
		"a body that is not JSON": `{`,
	} {
		if status, _ := fkRaw(t, s, http.MethodPost, "/api/users", "", body); status != http.StatusBadRequest || s.Setup() {
			t.Errorf("%s: status %d, Setup() %t; want 400 and setup still false", name, status, s.Setup())
		}
	}
	if err := c.CreateAdmin(ctx, fkEmail, fkPassword); err != nil {
		t.Fatal(err)
	}
	h, err = c.Health(ctx)
	if err != nil || !h.Setup || !s.Setup() {
		t.Fatalf("after the first user: health %+v, error %v, Setup() %t; want setup true", h, err, s.Setup())
	}
	if email, password, ok := s.Admin(); !ok || email != fkEmail || password != fkPassword {
		t.Errorf("Admin() = %q, %q, %t; want the created user", email, password, ok)
	}
	if err := c.CreateAdmin(ctx, "second@example.com", fkPassword); fkAPIError(t, err).Status != http.StatusForbidden {
		t.Errorf("a second admin: %v, want 403", err)
	}
}

func TestFkTokensNeedTheAdminsCredentials(t *testing.T) {
	s := New(t)
	c := npmapi.New(s.URL(), nil)
	ctx := t.Context()
	if _, err := c.Token(ctx, fkEmail, fkPassword); fkAPIError(t, err).Status != http.StatusBadRequest {
		t.Errorf("a token before any user: %v, want 400", err)
	}
	s.SeedAdmin(fkEmail, fkPassword)
	if _, err := c.Token(ctx, fkEmail, "wrong-password"); fkAPIError(t, err).Status != http.StatusBadRequest {
		t.Errorf("a wrong password: %v, want 400", err)
	}
	if _, err := c.Token(ctx, "other@example.com", fkPassword); fkAPIError(t, err).Status != http.StatusBadRequest {
		t.Errorf("a wrong identity: %v, want 400", err)
	}
	if _, err := c.Token(ctx, " OPS@Example.com ", fkPassword); err != nil {
		t.Errorf("an identity that differs only in case and surrounding spaces: %v, want a token", err)
	}
	a, errA := c.Token(ctx, fkEmail, fkPassword)
	b, errB := c.Token(ctx, fkEmail, fkPassword)
	if errA != nil || errB != nil || a == "" || a == b {
		t.Errorf("tokens %q, %q (%v, %v), want two different non-empty tokens", a, b, errA, errB)
	}
	s.SeedAdmin("Mixed@Example.com", fkPassword)
	if _, err := c.Token(ctx, "Mixed@Example.com", fkPassword); fkAPIError(t, err).Status != http.StatusBadRequest {
		t.Errorf("an admin stored with upper-case letters: %v, want 400, because the lower-cased identity never matches it", err)
	}
}

func TestFkNginxCallsNeedAValidToken(t *testing.T) {
	s := New(t)
	_, tok := fkLogin(t, s)
	for _, p := range []string{"/api/nginx/proxy-hosts", "/api/nginx/access-lists", "/api/nginx/certificates"} {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			for _, token := range []string{"", "bogus"} {
				status, body := fkRaw(t, s, method, p, token, `{}`)
				if status != http.StatusForbidden || !strings.Contains(body, `"message":"Permission Denied"`) {
					t.Errorf("%s %s with token %q: %d %s, want 403 Permission Denied", method, p, token, status, body)
				}
			}
		}
		if status, _ := fkRaw(t, s, http.MethodDelete, p+"/1", "", ""); status != http.StatusForbidden {
			t.Errorf("DELETE %s/1 without a token: %d, want 403", p, status)
		}
		if status, _ := fkRaw(t, s, http.MethodGet, p, tok, ""); status != http.StatusOK {
			t.Errorf("GET %s with a valid token: %d, want 200", p, status)
		}
	}
	if n := len(s.ProxyHosts()) + len(s.AccessLists()) + len(s.Certificates()); n != 0 {
		t.Errorf("refused calls created %d objects", n)
	}
}

func TestFkSeedsReturnTheStoredObjectsAndKeepSnapshotsApart(t *testing.T) {
	s := New(t)
	if s.ProxyHosts() == nil || s.AccessLists() == nil || s.Certificates() == nil {
		t.Fatal("the snapshots of an empty server are nil")
	}
	s.SeedAdmin(fkEmail, fkPassword)
	if email, password, ok := s.Admin(); !s.Setup() || !ok || email != fkEmail || password != fkPassword {
		t.Fatalf("SeedAdmin: Setup() %t, Admin() = %q, %q, %t", s.Setup(), email, password, ok)
	}
	h1 := s.SeedProxyHost(npmapi.ProxyHost{DomainNames: []string{"seed.example.com"}, ForwardScheme: "http", ForwardHost: "web", ForwardPort: 80})
	h2 := s.SeedProxyHost(npmapi.ProxyHost{ID: 99, CreatedOn: "2020-01-01T00:00:00.000Z", DomainNames: []string{"two.example.com"}, ForwardScheme: "http", ForwardHost: "web", ForwardPort: 80})
	if h1.ID != 1 || h1.CreatedOn == "" || h2.ID != 2 || h2.CreatedOn != "2020-01-01T00:00:00.000Z" {
		t.Errorf("seeded proxy hosts %+v and %+v, want ids 1 and 2, a stamped created_on and a kept one", h1, h2)
	}
	l := s.SeedAccessList(npmapi.AccessList{Name: "n", Clients: []npmapi.AccessClient{{Address: "203.0.113.7", Directive: "allow"}}})
	c := s.SeedCertificate(npmapi.Certificate{NiceName: "x", Provider: "letsencrypt", DomainNames: []string{"x.example.com"}})
	if l.ID != 1 || l.CreatedOn == "" || c.ID != 1 || c.CreatedOn == "" || c.NiceName != "x" {
		t.Errorf("seeded access list %+v and certificate %+v, want id 1, a stamped created_on and the seeded nice_name kept", l, c)
	}

	got := s.ProxyHosts()
	if len(got) != 2 || got[0].ID != 1 || got[1].ID != 2 {
		t.Fatalf("ProxyHosts() = %+v, want both seeds in id order", got)
	}
	got[0].DomainNames[0] = "mutated.example.com"
	if s.ProxyHosts()[0].DomainNames[0] != "seed.example.com" {
		t.Error("a snapshot shares its slices with the server")
	}

	client, _ := fkLogin(t, s)
	seen, err := client.ProxyHost(t.Context(), 1)
	if err != nil || seen.DomainNames[0] != "seed.example.com" {
		t.Errorf("the client reads %+v, %v, want the seeded host", seen, err)
	}
	if _, err := client.CreateProxyHost(t.Context(), fkHost("seed.example.com")); fkAPIError(t, err).Status != http.StatusBadRequest {
		t.Errorf("a create for a seeded domain: %v, want 400", err)
	}
}

func TestFkFaultsFailOrLoseTheResponseOnce(t *testing.T) {
	s := New(t)
	c, _ := fkLogin(t, s)
	ctx := t.Context()
	const hosts = "/api/nginx/proxy-hosts"

	s.Fail(http.MethodPost, hosts, Fault{Status: http.StatusServiceUnavailable})
	_, err := c.CreateProxyHost(ctx, fkHost("a.example.com"))
	if ae := fkAPIError(t, err); ae.Status != http.StatusServiceUnavailable || npmapi.IsClientError(err) {
		t.Errorf("a status fault: %v, want a 503 that is not a client error", err)
	}
	if n := len(s.ProxyHosts()); n != 0 {
		t.Errorf("a status fault committed %d proxy hosts, want none", n)
	}

	s.Fail(http.MethodPost, hosts, Fault{AfterCommit: true})
	_, err = c.CreateProxyHost(ctx, fkHost("a.example.com"))
	var ae *npmapi.APIError
	if err == nil || errors.As(err, &ae) || npmapi.IsClientError(err) {
		t.Errorf("a lost response: %v, want a transport error", err)
	}
	if got := s.ProxyHosts(); len(got) != 1 || got[0].DomainNames[0] != "a.example.com" {
		t.Errorf("after a lost response the proxy hosts are %+v, want the committed one", got)
	}

	if _, err := c.CreateProxyHost(ctx, fkHost("b.example.com")); err != nil {
		t.Errorf("the next create: %v, want the faults used up", err)
	}

	s.Fail(http.MethodDelete, hosts+"/1", Fault{Status: http.StatusInternalServerError})
	if _, err := c.ProxyHost(ctx, 1); err != nil {
		t.Errorf("a GET of the faulted path: %v, want it unaffected", err)
	}
	if err := c.DeleteProxyHost(ctx, 1); fkAPIError(t, err).Status != http.StatusInternalServerError {
		t.Errorf("the faulted DELETE: %v, want 500", err)
	}
	if err := c.DeleteProxyHost(ctx, 1); err != nil {
		t.Errorf("the DELETE after the fault: %v", err)
	}
}

func TestFkRequestsLogEveryCallIncludingRefusedOnes(t *testing.T) {
	s := New(t)
	ctx := t.Context()
	anon := npmapi.New(s.URL(), nil)
	if _, err := anon.Health(ctx); err != nil {
		t.Fatal(err)
	}
	s.SeedAdmin(fkEmail, fkPassword)
	tok, err := anon.Token(ctx, fkEmail, fkPassword)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := anon.WithToken(tok).ProxyHosts(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := anon.ProxyHosts(ctx); fkAPIError(t, err).Status != http.StatusForbidden {
		t.Fatalf("an anonymous list: %v, want 403", err)
	}
	reqs := s.Requests()
	want := [][2]string{{"GET", "/api/"}, {"POST", "/api/tokens"}, {"GET", "/api/nginx/proxy-hosts"}, {"GET", "/api/nginx/proxy-hosts"}}
	if len(reqs) != len(want) {
		t.Fatalf("%d requests recorded, want %d: %+v", len(reqs), len(want), reqs)
	}
	for i, w := range want {
		if reqs[i].Method != w[0] || reqs[i].Path != w[1] {
			t.Errorf("request %d = %s %s, want %s %s", i, reqs[i].Method, reqs[i].Path, w[0], w[1])
		}
	}
	if reqs[2].Header.Get("Authorization") != "Bearer "+tok || reqs[3].Header.Get("Authorization") != "" {
		t.Errorf("authorization headers %q and %q, want the token and none", reqs[2].Header.Get("Authorization"), reqs[3].Header.Get("Authorization"))
	}
	if !strings.Contains(string(reqs[1].Body), `"identity":"`+fkEmail+`"`) {
		t.Errorf("the token request body is %q, want the identity", reqs[1].Body)
	}
}
