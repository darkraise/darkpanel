package npmapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// apCall is one request the test server received.
type apCall struct {
	Method, Path, Query string
	Header              http.Header
	Body                string
}

// apServer answers every request with one scripted answer and records the requests.
type apServer struct {
	srv   *httptest.Server
	mu    sync.Mutex
	calls []apCall
}

func apStart(t *testing.T, status int, body string) *apServer {
	t.Helper()
	s := &apServer{}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.calls = append(s.calls, apCall{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Header: r.Header.Clone(), Body: string(b)})
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

// client is a client of the server that holds a token.
func (s *apServer) client() *Client { return New(s.srv.URL, nil).WithToken("tok-1") }

// only returns the one request the server received.
func (s *apServer) only(t *testing.T) apCall {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.calls) != 1 {
		t.Fatalf("the server received %d requests, want 1: %+v", len(s.calls), s.calls)
	}
	return s.calls[0]
}

// apSame fails unless got and want are the same JSON document.
func apSame(t *testing.T, got, want string) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal([]byte(got), &g); err != nil {
		t.Fatalf("request body %q is not JSON: %v", got, err)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("expectation %q is not JSON: %v", want, err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Errorf("request body\n%s\nwant\n%s", got, want)
	}
}

const (
	apCreatedOn = "2026-11-02T10:00:00.000Z"
	apLocation  = "proxy_set_header X-Darkpanel-Client $realip_remote_addr;"

	apHostAnswer = `{"id":3,"created_on":"2026-11-02T10:00:00.000Z","modified_on":"2026-11-02T10:00:01.000Z","domain_names":["app.example.com"],` +
		`"forward_scheme":"http","forward_host":"web","forward_port":8080,"certificate_id":4,"access_list_id":0,"ssl_forced":true,"hsts_enabled":true,` +
		`"allow_websocket_upgrade":false,"locations":[{"path":"/","forward_scheme":"http","forward_host":"web","forward_port":8080,` +
		`"advanced_config":"proxy_set_header X-Darkpanel-Client $realip_remote_addr;"}],"advanced_config":"","enabled":true,"meta":{}}`
	apHostBody = `{"domain_names":["app.example.com"],"forward_scheme":"http","forward_host":"web","forward_port":8080,"certificate_id":4,"access_list_id":0,` +
		`"ssl_forced":true,"hsts_enabled":true,"allow_websocket_upgrade":false,"locations":[{"path":"/","forward_scheme":"http","forward_host":"web",` +
		`"forward_port":8080,"advanced_config":"proxy_set_header X-Darkpanel-Client $realip_remote_addr;"}],"advanced_config":""}`

	apListAnswer = `{"id":5,"created_on":"2026-11-02T10:00:00.000Z","name":"office","satisfy_any":true,"pass_auth":false,` +
		`"clients":[{"address":"203.0.113.0/24","directive":"allow"}],"owner_user_id":1}`
	apListBody = `{"name":"office","satisfy_any":true,"pass_auth":false,"clients":[{"address":"203.0.113.0/24","directive":"allow"}],"items":[]}`

	apCertAnswer = `{"id":9,"created_on":"2026-11-02T10:00:00.000Z","nice_name":"app.example.com","provider":"letsencrypt",` +
		`"domain_names":["app.example.com"],"expires_on":"2027-01-31T10:00:00.000Z","meta":{}}`
	apCertBody = `{"provider":"letsencrypt","domain_names":["app.example.com"]}`
)

var (
	apHost = ProxyHost{
		ID: 3, CreatedOn: apCreatedOn, ModifiedOn: "2026-11-02T10:00:01.000Z", DomainNames: []string{"app.example.com"},
		ForwardScheme: "http", ForwardHost: "web", ForwardPort: 8080, CertificateID: 4, SSLForced: true, HSTSEnabled: true,
		Locations: []Location{{Path: "/", ForwardScheme: "http", ForwardHost: "web", ForwardPort: 8080, AdvancedConfig: apLocation}},
	}
	apHostIn = ProxyHostBody{
		DomainNames: []string{"app.example.com"}, ForwardScheme: "http", ForwardHost: "web", ForwardPort: 8080, CertificateID: 4,
		SSLForced: true, HSTSEnabled: true, Locations: apHost.Locations,
	}
	apList = AccessList{
		ID: 5, CreatedOn: apCreatedOn, Name: "office", SatisfyAny: true, Clients: []AccessClient{{Address: "203.0.113.0/24", Directive: "allow"}},
	}
	apListIn = AccessListBody{Name: "office", SatisfyAny: true, Clients: apList.Clients}
	apCert   = Certificate{
		ID: 9, CreatedOn: apCreatedOn, NiceName: "app.example.com", Provider: "letsencrypt", DomainNames: []string{"app.example.com"},
		ExpiresOn: "2027-01-31T10:00:00.000Z",
	}
	apCertIn = CertificateBody{
		DomainNames: []string{"app.example.com"},
	}
)

func TestApHealthIsAnonymousAndReadsStatusAndSetup(t *testing.T) {
	for _, setup := range []bool{false, true} {
		s := apStart(t, 200, `{"status":"OK","setup":`+strconv.FormatBool(setup)+`,"version":{"major":2,"minor":13,"revision":5}}`)
		h, err := New(s.srv.URL, nil).Health(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if h != (Health{Status: "OK", Setup: setup}) {
			t.Errorf("health = %+v, want status OK and setup %t", h, setup)
		}
		c := s.only(t)
		if c.Method != http.MethodGet || c.Path != "/api/" || c.Header.Get("Authorization") != "" {
			t.Errorf("request %+v, want an anonymous GET /api/", c)
		}
	}
}

func TestApCreateAdminSendsTheAdminUserBody(t *testing.T) {
	s := apStart(t, 201, `{"id":1,"email":"ops@example.com","roles":["admin"]}`)
	if err := New(s.srv.URL, nil).CreateAdmin(t.Context(), "ops@example.com", "correct-horse"); err != nil {
		t.Fatal(err)
	}
	c := s.only(t)
	if c.Method != http.MethodPost || c.Path != "/api/users" || c.Header.Get("Authorization") != "" || c.Header.Get("Content-Type") != "application/json" {
		t.Errorf("request %+v, want an anonymous JSON POST /api/users", c)
	}
	apSame(t, c.Body, `{"name":"Darkpanel","nickname":"darkpanel","email":"ops@example.com","roles":["admin"],"is_disabled":false,`+
		`"auth":{"type":"password","secret":"correct-horse"}}`)
}

func TestApTokenSendsTheCredentialsAndReturnsTheToken(t *testing.T) {
	s := apStart(t, 200, `{"token":"tok-77","expires":"2026-11-03T10:00:00.000Z"}`)
	tok, err := New(s.srv.URL, nil).Token(t.Context(), "ops@example.com", "correct-horse")
	if err != nil || tok != "tok-77" {
		t.Fatalf("token = %q, %v; want tok-77", tok, err)
	}
	c := s.only(t)
	if c.Method != http.MethodPost || c.Path != "/api/tokens" || c.Header.Get("Authorization") != "" {
		t.Errorf("request %+v, want an anonymous POST /api/tokens", c)
	}
	apSame(t, c.Body, `{"identity":"ops@example.com","secret":"correct-horse"}`)

	empty := apStart(t, 200, `{"token":"","expires":""}`)
	if tok, err := New(empty.srv.URL, nil).Token(t.Context(), "ops@example.com", "correct-horse"); err == nil || tok != "" {
		t.Errorf("an answer without a token gave %q, %v; want an error", tok, err)
	}
}

func TestApWithTokenReturnsACopyThatSendsTheBearerToken(t *testing.T) {
	s := apStart(t, 200, `[]`)
	anon := New(s.srv.URL, nil)
	authed := anon.WithToken("abc")
	if authed == anon {
		t.Fatal("WithToken returned the same client")
	}
	if _, err := authed.ProxyHosts(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := anon.ProxyHosts(t.Context()); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.calls) != 2 || s.calls[0].Header.Get("Authorization") != "Bearer abc" || s.calls[1].Header.Get("Authorization") != "" {
		t.Errorf("calls %+v, want a bearer token on the first and none on the second", s.calls)
	}
}

type apCountingTransport struct{ n *int }

func (c apCountingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	*c.n++
	return http.DefaultTransport.RoundTrip(r)
}

func TestApUsesTheHTTPClientItIsGiven(t *testing.T) {
	s := apStart(t, 200, `{"status":"OK","setup":true}`)
	n := 0
	c := New(s.srv.URL, &http.Client{Transport: apCountingTransport{n: &n}})
	if _, err := c.Health(t.Context()); err != nil || n != 1 {
		t.Fatalf("Health = %v after %d round trips through the given client, want 1", err, n)
	}
}

type apRow struct {
	name   string
	call   func(c *Client) (any, error)
	method string
	path   string
	query  string
	status int
	answer string
	body   string
	want   any
}

func TestApObjectCallsUseNPMsEndpoints(t *testing.T) {
	ctx := t.Context()
	const hosts, lists, certs = "/api/nginx/proxy-hosts", "/api/nginx/access-lists", "/api/nginx/certificates"
	rows := []apRow{
		{name: "list proxy hosts", call: func(c *Client) (any, error) { return c.ProxyHosts(ctx) },
			method: "GET", path: hosts, status: 200, answer: "[" + apHostAnswer + "]", want: []ProxyHost{apHost}},
		{name: "get a proxy host", call: func(c *Client) (any, error) { return c.ProxyHost(ctx, 3) },
			method: "GET", path: hosts + "/3", status: 200, answer: apHostAnswer, want: apHost},
		{name: "create a proxy host", call: func(c *Client) (any, error) { return c.CreateProxyHost(ctx, apHostIn) },
			method: "POST", path: hosts, status: 201, answer: apHostAnswer, body: apHostBody, want: apHost},
		{name: "update a proxy host", call: func(c *Client) (any, error) { return c.UpdateProxyHost(ctx, 3, apHostIn) },
			method: "PUT", path: hosts + "/3", status: 200, answer: apHostAnswer, body: apHostBody, want: apHost},
		{name: "delete a proxy host", call: func(c *Client) (any, error) { return nil, c.DeleteProxyHost(ctx, 3) },
			method: "DELETE", path: hosts + "/3", status: 200, answer: `true`},
		{name: "list access lists", call: func(c *Client) (any, error) { return c.AccessLists(ctx) },
			method: "GET", path: lists, query: "expand=clients", status: 200, answer: "[" + apListAnswer + "]", want: []AccessList{apList}},
		{name: "get an access list", call: func(c *Client) (any, error) { return c.AccessList(ctx, 5) },
			method: "GET", path: lists + "/5", query: "expand=clients", status: 200, answer: apListAnswer, want: apList},
		{name: "create an access list", call: func(c *Client) (any, error) { return c.CreateAccessList(ctx, apListIn) },
			method: "POST", path: lists, status: 201, answer: apListAnswer, body: apListBody, want: apList},
		{name: "delete an access list", call: func(c *Client) (any, error) { return nil, c.DeleteAccessList(ctx, 5) },
			method: "DELETE", path: lists + "/5", status: 200, answer: `true`},
		{name: "list certificates", call: func(c *Client) (any, error) { return c.Certificates(ctx) },
			method: "GET", path: certs, status: 200, answer: "[" + apCertAnswer + "]", want: []Certificate{apCert}},
		{name: "get a certificate", call: func(c *Client) (any, error) { return c.Certificate(ctx, 9) },
			method: "GET", path: certs + "/9", status: 200, answer: apCertAnswer, want: apCert},
		{name: "create a certificate", call: func(c *Client) (any, error) { return c.CreateCertificate(ctx, apCertIn) },
			method: "POST", path: certs, status: 201, answer: apCertAnswer, body: apCertBody, want: apCert},
		{name: "delete a certificate", call: func(c *Client) (any, error) { return nil, c.DeleteCertificate(ctx, 9) },
			method: "DELETE", path: certs + "/9", status: 200, answer: `true`},
	}
	for _, tc := range rows {
		t.Run(tc.name, func(t *testing.T) {
			s := apStart(t, tc.status, tc.answer)
			got, err := tc.call(s.client())
			if err != nil {
				t.Fatal(err)
			}
			c := s.only(t)
			if c.Method != tc.method || c.Path != tc.path || c.Query != tc.query {
				t.Errorf("request %s %s?%s, want %s %s?%s", c.Method, c.Path, c.Query, tc.method, tc.path, tc.query)
			}
			if c.Header.Get("Authorization") != "Bearer tok-1" || c.Header.Get("Accept") != "application/json" {
				t.Errorf("headers %v, want the bearer token and Accept: application/json", c.Header)
			}
			if tc.body == "" {
				if c.Body != "" {
					t.Errorf("request body %q, want none", c.Body)
				}
			} else {
				apSame(t, c.Body, tc.body)
				if c.Header.Get("Content-Type") != "application/json" {
					t.Errorf("Content-Type %q, want application/json", c.Header.Get("Content-Type"))
				}
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("answer %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestApBodiesNeverSendNullLists(t *testing.T) {
	ctx := t.Context()
	for _, tc := range []struct {
		name string
		call func(c *Client) error
		keys []string
	}{
		{"a proxy host", func(c *Client) error {
			_, err := c.CreateProxyHost(ctx, ProxyHostBody{ForwardScheme: "http", ForwardHost: "web", ForwardPort: 80})
			return err
		}, []string{"domain_names", "locations"}},
		{"an access list", func(c *Client) error {
			_, err := c.CreateAccessList(ctx, AccessListBody{Name: "office"})
			return err
		}, []string{"clients", "items"}},
		{"a certificate", func(c *Client) error {
			_, err := c.CreateCertificate(ctx, CertificateBody{Provider: "letsencrypt"})
			return err
		}, []string{"domain_names"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := apStart(t, 201, `{}`)
			if err := tc.call(s.client()); err != nil {
				t.Fatal(err)
			}
			var m map[string]any
			if err := json.Unmarshal([]byte(s.only(t).Body), &m); err != nil {
				t.Fatal(err)
			}
			for _, k := range tc.keys {
				if arr, ok := m[k].([]any); !ok || len(arr) != 0 {
					t.Errorf("%s = %#v, want an empty array", k, m[k])
				}
			}
		})
	}
}

func TestApCertificateBodyIsAlwaysLetsEncryptAndOmitsAnEmptyName(t *testing.T) {
	s := apStart(t, 201, apCertAnswer)
	_, err := s.client().CreateCertificate(t.Context(), CertificateBody{
		Provider: "other", DomainNames: []string{"a.example.com", "b.example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	apSame(t, s.only(t).Body, `{"provider":"letsencrypt","domain_names":["a.example.com","b.example.com"]}`)
}

func TestApListsAreNeverNil(t *testing.T) {
	ctx := t.Context()
	for _, answer := range []string{`[]`, `null`} {
		s := apStart(t, 200, answer)
		c := s.client()
		hosts, err1 := c.ProxyHosts(ctx)
		lists, err2 := c.AccessLists(ctx)
		certs, err3 := c.Certificates(ctx)
		if err1 != nil || err2 != nil || err3 != nil {
			t.Fatalf("errors %v, %v, %v", err1, err2, err3)
		}
		if hosts == nil || lists == nil || certs == nil || len(hosts)+len(lists)+len(certs) != 0 {
			t.Errorf("answer %s gave %#v, %#v, %#v, want empty non-nil slices", answer, hosts, lists, certs)
		}
	}
}

// w10ncBody is a response body that counts the bytes read from it and records how many were left when it was closed.
type w10ncBody struct {
	r      *bytes.Reader
	read   int
	unread int
	closed bool
}

func (b *w10ncBody) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	b.read += n
	return n, err
}

func (b *w10ncBody) Close() error {
	b.unread, b.closed = b.r.Len(), true
	return nil
}

// w10ncTransport answers every request with one status and body.
type w10ncTransport struct {
	status int
	body   *w10ncBody
}

func (t w10ncTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: t.status, Header: http.Header{}, Body: t.body, Request: r}, nil
}

// TestW10NcBodiesAreDrainedWithinALimit: a body the client does not decode is read to its end when it is short enough,
// so the connection can be reused, and is never read further than a fixed bound, so a hostile NPM cannot make the
// client read without end.
func TestW10NcBodiesAreDrainedWithinALimit(t *testing.T) {
	refusal := func(size int) []byte {
		doc := `{"error":{"message":"boom"}}`
		return []byte(doc + strings.Repeat(" ", size-len(doc)))
	}
	tests := []struct {
		name    string
		status  int
		body    []byte
		drained bool // the whole body must have been read when the client closed it
		maxRead int
	}{
		{"a refusal over the read limit is drained", 500, refusal(100 << 10), true, 2 * errorBodyLimit},
		{"a huge refusal is read only to a bound", 500, refusal(1 << 20), false, 2 * errorBodyLimit},
		{"an answer nobody decodes is drained", 200, bytes.Repeat([]byte(" "), 50<<10), true, errorBodyLimit},
		{"a huge answer nobody decodes is read only to a bound", 200, bytes.Repeat([]byte(" "), 1<<20), false, errorBodyLimit},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := &w10ncBody{r: bytes.NewReader(tt.body)}
			c := New("http://npm.invalid", &http.Client{Transport: w10ncTransport{status: tt.status, body: body}})
			err := c.DeleteProxyHost(t.Context(), 3)
			if tt.status == 200 {
				if err != nil {
					t.Fatalf("DeleteProxyHost: %v", err)
				}
			} else {
				var ae *APIError
				if !errors.As(err, &ae) || ae.Status != tt.status || ae.Message != "boom" {
					t.Fatalf("error %#v, want an APIError %d with the message boom", err, tt.status)
				}
			}
			if !body.closed {
				t.Error("the body was not closed")
			}
			if tt.drained && body.unread != 0 {
				t.Errorf("%d bytes were left unread at close, want the body read to its end", body.unread)
			}
			if body.read > tt.maxRead {
				t.Errorf("%d bytes were read, want at most %d", body.read, tt.maxRead)
			}
		})
	}
}

// TestW10NcAnswerOverTheLimitIsRefused: an answer of exactly answerBodyLimit bytes decodes, and one byte more fails
// with an error that names the limit.
func TestW10NcAnswerOverTheLimitIsRefused(t *testing.T) {
	const skeleton = `{"status":""}`
	doc := func(size int) string { return `{"status":"` + strings.Repeat("a", size-len(skeleton)) + `"}` }

	t.Run("an answer of exactly the limit decodes", func(t *testing.T) {
		s := apStart(t, 200, doc(answerBodyLimit))
		h, err := s.client().Health(t.Context())
		if err != nil || len(h.Status) != answerBodyLimit-len(skeleton) {
			t.Fatalf("Health = %d status bytes, %v; want %d and no error", len(h.Status), err, answerBodyLimit-len(skeleton))
		}
	})
	t.Run("an answer one byte over the limit is refused", func(t *testing.T) {
		s := apStart(t, 200, doc(answerBodyLimit+1))
		_, err := s.client().Health(t.Context())
		want := fmt.Sprintf("exceeds %d bytes", answerBodyLimit)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("Health error %v, want one that says the answer %s", err, want)
		}
	})
}
