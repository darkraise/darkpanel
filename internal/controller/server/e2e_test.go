package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/cli"
	"github.com/darkraise/darkpanel/internal/controller/api"
	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/controller/listen"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/registry"
)

// adminPassword is the first admin's password in the end-to-end test.
const adminPassword = "e2e correct horse battery" //nolint:gosec // G101: a test password, not a credential

// e2e drives one assembled server over HTTP and through the CLI.
type e2e struct {
	t       *testing.T
	s       *Server
	clock   *coretest.FakeClock
	url     string
	home    string
	session string // the session cookie's value
	csrf    string
}

// reply is one HTTP answer.
type reply struct {
	status int
	header http.Header
	body   []byte
}

// problemCode is the code of an RFC 9457 answer, "" for any other body.
func (r reply) problemCode() errcode.Code {
	var p struct{ Code errcode.Code }
	if r.header.Get("Content-Type") != "application/problem+json" || json.Unmarshal(r.body, &p) != nil {
		return ""
	}
	return p.Code
}

// credentials of a request: a session cookie, its CSRF token and a bearer
// token, each sent when not empty.
type credentials struct{ session, csrf, bearer string }

func (e *e2e) do(method, path string, body any, c credentials) reply {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			e.t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, e.url+path, rd)
	if err != nil {
		e.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.session != "" {
		req.AddCookie(&http.Cookie{Name: api.SessionCookiePlain, Value: c.session}) //nolint:gosec // G124: a request cookie carries only name and value
	}
	if c.csrf != "" {
		req.Header.Set(api.CSRFHeader, c.csrf)
	}
	if c.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearer)
	}
	resp, err := plainClient.Do(req)
	if err != nil {
		e.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		e.t.Fatal(err)
	}
	return reply{status: resp.StatusCode, header: resp.Header, body: b}
}

// admin is the logged-in session with its CSRF token.
func (e *e2e) admin() credentials { return credentials{session: e.session, csrf: e.csrf} }

// want fails unless r has the status, decoding a 2xx body into out.
func (e *e2e) want(r reply, status int, out any) {
	e.t.Helper()
	if r.status != status {
		e.t.Fatalf("status %d, want %d: %s", r.status, status, r.body)
	}
	if out != nil {
		if err := json.Unmarshal(r.body, out); err != nil {
			e.t.Fatalf("decode %s: %v", r.body, err)
		}
	}
}

func (e *e2e) wantProblem(r reply, status int, code errcode.Code) {
	e.t.Helper()
	if r.status != status || r.problemCode() != code {
		e.t.Errorf("got %d %s, want %d %s", r.status, r.body, status, code)
	}
}

// change is the part of the API's Change the test reads.
type change struct {
	ID     string          `json:"id"`
	Action string          `json:"action"`
	State  string          `json:"state"`
	Output json.RawMessage `json:"output"`
}

// apply plans and applies a controller-side action with the admin session.
func (e *e2e) apply(name string, input any) change {
	e.t.Helper()
	var plan struct {
		PlanID string `json:"planId"`
	}
	e.want(e.do("POST", "/api/v1/actions/"+name+"/v1/plan", map[string]any{"input": input}, e.admin()), 200, &plan)
	var ch change
	e.want(e.do("POST", "/api/v1/actions/"+name+"/v1/apply", map[string]any{"planId": plan.PlanID}, e.admin()), 200, &ch)
	if ch.State != "completed" {
		e.t.Fatalf("%s: change %s is %s", name, ch.ID, ch.State)
	}
	return ch
}

// cli runs the darkpanel CLI in-process, as cmd/darkpanel builds it, with
// the given environment and standard input.
func (e *e2e) cli(env map[string]string, stdin string, args ...string) (stdout, stderr string, code int) {
	e.t.Helper()
	reg, err := registry.Build()
	if err != nil {
		e.t.Fatal(err)
	}
	var out, errs bytes.Buffer
	root, err := cli.NewRoot(cli.Options{
		Registry: reg, Stdin: strings.NewReader(stdin), Stdout: &out, Stderr: &errs,
		Getenv:      func(k string) string { return env[k] },
		UserHomeDir: func() (string, error) { return e.home, nil },
		IsTerminal:  func() bool { return false },
	})
	if err != nil {
		e.t.Fatal(err)
	}
	root.SetArgs(args)
	err = root.ExecuteContext(context.Background())
	cli.PrintError(&errs, err)
	return out.String(), errs.String(), cli.ExitCode(err)
}

var secretLine = regexp.MustCompile(`(?m)^Secret: ([A-Z2-7]+)$`)

// TestEndToEnd is the end-to-end test of spec section "Verification" over
// one assembled server.
func TestEndToEnd(t *testing.T) {
	opts := embeddedOptions(t)
	opts.Hostname = func() (string, error) { return "e2e-host", nil }
	opts.Listen = append(opts.Listen, listen.Spec{Kind: listen.KindHTTPS, Addr: netip.MustParseAddrPort("127.0.0.1:0")})
	e := &e2e{t: t, clock: opts.Clock.(*coretest.FakeClock), home: t.TempDir()}
	e.s = start(t, opts)
	e.url = baseURL(t, e.s)
	hostID := e.s.LocalHostID()
	sock := e.s.SocketPath()

	// The OpenAPI document covers every network route with the real
	// registry (register row 69, obligation 5).
	if err := api.CheckOpenAPI(e.s.Registry()); err != nil {
		t.Fatalf("CheckOpenAPI: %v", err)
	}

	// The web root serves the embedded UI's index page. Without a built UI
	// it answers not_found, which is all a checkout without web/dist shows.
	root := e.do("GET", "/", nil, credentials{})
	if index, err := fs.ReadFile(api.EmbeddedUI(), "index.html"); err == nil {
		if root.status != 200 || root.problemCode() == errcode.NotFound || !bytes.Equal(root.body, index) {
			t.Errorf("GET / = %d %q, want the embedded index.html", root.status, root.body)
		}
	} else {
		if os.Getenv("DARKPANEL_REQUIRE_UI") == "1" {
			t.Fatalf("DARKPANEL_REQUIRE_UI=1 but the web UI is not built: %v", err)
		}
		t.Logf("the web UI is not built (%v); GET / can only answer not_found", err)
		e.wantProblem(root, 404, errcode.NotFound)
	}

	// The first admin is created through the controller socket by the CLI,
	// with a code computed from the printed secret (register row 11).
	out, errOut, code := e.cli(nil, adminPassword+"\n", "user", "create", "--username", "admin", "--socket", sock, "--yes")
	if code != cli.ExitUsage || !strings.Contains(errOut, "no code given; activate the user with: darkpanel user activate --username admin") {
		t.Fatalf("user create: exit %d\n%s\n%s", code, out, errOut)
	}
	m := secretLine.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("user create printed no secret:\n%s", out)
	}
	totpSecret := m[1]
	totp, err := auth.TOTPCode(totpSecret, e.clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	if out, errOut, code := e.cli(nil, totp+"\n", "user", "activate", "--username", "admin", "--socket", sock, "--yes"); code != cli.ExitOK {
		t.Fatalf("user activate: exit %d\n%s\n%s", code, out, errOut)
	}

	// Log in over HTTP with password and a code of the next step.
	e.clock.Advance(auth.TOTPPeriod)
	totp, _ = auth.TOTPCode(totpSecret, e.clock.Now())
	r := e.do("POST", "/api/v1/auth/login", map[string]string{"username": "admin", "password": adminPassword, "code": totp}, credentials{})
	var login struct {
		CSRFToken string `json:"csrfToken"`
		User      struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	e.want(r, 200, &login)
	for _, c := range (&http.Response{Header: r.header}).Cookies() {
		if c.Name == api.SessionCookiePlain {
			e.session = c.Value
		}
	}
	if e.session == "" || login.CSRFToken == "" {
		t.Fatalf("login set no session cookie or CSRF token: %v %s", r.header, r.body)
	}
	e.csrf = login.CSRFToken

	t.Run("deny by default", func(t *testing.T) { e.denyByDefault(t, hostID) })

	// A break-glass action is refused on the network listener, even for an
	// admin session.
	e.wantProblem(e.do("POST", "/api/v1/actions/core.user.list/v1/read", map[string]any{"input": map[string]any{}}, e.admin()),
		403, errcode.Forbidden)

	// hostinfo.summary through the API.
	resource := "/api/v1/hosts/" + hostID + "/resources/hostinfo/summary"
	var summary struct {
		Hostname         string `json:"hostname"`
		DarkpanelVersion string `json:"darkpanelVersion"`
	}
	e.want(e.do("GET", resource, nil, e.admin()), 200, &summary)
	if summary.Hostname == "" || summary.DarkpanelVersion != "dev" {
		t.Errorf("hostinfo.summary = %+v", summary)
	}

	// A token: its secret is in the apply response only (register row 8).
	created := e.apply("core.token.create", map[string]any{"name": "cli", "scope": "read"})
	var newToken struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(created.Output, &newToken); err != nil || !strings.HasPrefix(newToken.Token, auth.TokenPrefix) {
		t.Fatalf("core.token.create output %s (%v), want a token", created.Output, err)
	}
	token := newToken.Token
	var stored change
	e.want(e.do("GET", "/api/v1/changes/"+created.ID, nil, e.admin()), 200, &stored)
	if !strings.Contains(string(stored.Output), `"token":"[redacted]"`) || strings.Contains(string(stored.Output), token) {
		t.Errorf("stored change output %s, want the token redacted", stored.Output)
	}

	// The bearer token takes precedence over the session cookie.
	var who struct {
		Token *struct {
			ID string `json:"id"`
		} `json:"token"`
		CSRFToken *string `json:"csrfToken"`
	}
	e.want(e.do("GET", "/api/v1/auth/session", nil, credentials{session: e.session, bearer: token}), 200, &who)
	if who.Token == nil || who.Token.ID != newToken.ID || who.CSRFToken != nil {
		t.Errorf("session with a token and a cookie answered %+v, want the token", who)
	}
	e.wantProblem(e.do("GET", "/api/v1/auth/session", nil, credentials{session: e.session, bearer: "dpt_bogus"}), 401, errcode.AuthRequired)

	// The CLI reads hostinfo.summary with the token (register rows 3, 13).
	env := map[string]string{cli.EnvURL: e.url, cli.EnvToken: token}
	out, errOut, code = e.cli(env, "", "hostinfo", "summary", "get", "--json")
	var cliSummary struct {
		Hostname string `json:"hostname"`
	}
	if code != cli.ExitOK || json.Unmarshal([]byte(out), &cliSummary) != nil || cliSummary.Hostname != summary.Hostname {
		t.Errorf("darkpanel hostinfo summary get --json: exit %d\n%s\n%s", code, out, errOut)
	}
	if _, errOut, code := e.cli(map[string]string{cli.EnvURL: e.url}, "", "hostinfo", "summary", "get"); code != cli.ExitAuth {
		t.Errorf("without a token: exit %d (%s), want %d", code, errOut, cli.ExitAuth)
	}

	// The audit log lists the login and the token creation, with every
	// secret redacted or absent (register rows 8, 14).
	out, errOut, code = e.cli(env, "", "audit", "list", "--json")
	if code != cli.ExitOK {
		t.Fatalf("darkpanel audit list --json: exit %d\n%s", code, errOut)
	}
	e.checkAudit(out, login.User.ID, token, totpSecret)

	// Network change listings leave out the break-glass user changes
	// (register row 68).
	var listed struct {
		Changes []change `json:"changes"`
	}
	e.want(e.do("GET", "/api/v1/changes", nil, e.admin()), 200, &listed)
	var actions []string
	for _, c := range listed.Changes {
		actions = append(actions, c.Action)
	}
	if !slices.Equal(actions, []string{"core.token.create"}) {
		t.Errorf("GET /api/v1/changes lists %v, want only core.token.create", actions)
	}

	// A disabled module answers module.unavailable and the others keep
	// working; enabling it again serves it (register row 5).
	module := map[string]any{"hostId": hostID, "moduleId": "hostinfo"}
	e.apply("core.module.disable", module)
	e.wantProblem(e.do("GET", resource, nil, e.admin()), 409, errcode.ModuleUnavailable)
	if _, errOut, code := e.cli(env, "", "hostinfo", "summary", "get"); code != cli.ExitError || !strings.Contains(errOut, "module.unavailable") {
		t.Errorf("CLI read of a disabled module: exit %d %s", code, errOut)
	}
	if _, _, code := e.cli(env, "", "token", "list"); code != cli.ExitOK {
		t.Errorf("token list with hostinfo disabled: exit %d", code)
	}
	e.apply("core.module.enable", module)
	e.want(e.do("GET", resource, nil, e.admin()), 200, nil)

	// /healthz answers the status alone, and details to a token.
	var health struct {
		Status        string `json:"status"`
		Version       string `json:"version"`
		SchemaVersion int    `json:"schemaVersion"`
	}
	e.want(e.do("GET", "/healthz", nil, credentials{}), 200, &health)
	if health.Status != "ok" || health.Version != "" {
		t.Errorf("anonymous /healthz = %+v", health)
	}
	e.want(e.do("GET", "/healthz", nil, credentials{bearer: token}), 200, &health)
	if health.Version != "dev" || health.SchemaVersion < 4 {
		t.Errorf("/healthz with a token = %+v", health)
	}

	t.Run("https pinned by the CLI", func(t *testing.T) { e.pinnedHTTPS(t, sock, token, summary.Hostname) })
}

// pinnedHTTPS reads the web CA's fingerprint through the controller socket,
// pins it in the CLI config file and reads hostinfo.summary with token over
// the HTTPS listener. Neither an unpinned nor a wrongly pinned CLI connects.
func (e *e2e) pinnedHTTPS(t *testing.T, sock, token, hostname string) {
	httpsURL := ""
	for _, b := range e.s.Listeners() {
		if b.Endpoint.TLS {
			httpsURL = "https://" + b.Addr.String()
		}
	}
	if httpsURL == "" {
		t.Fatalf("no HTTPS listener in %v", e.s.Listeners())
	}
	out, errOut, code := e.cli(nil, "", "controller", "web-ca", "--socket", sock, "--json")
	var ca struct {
		Fingerprint string `json:"fingerprint"`
	}
	if code != cli.ExitOK || json.Unmarshal([]byte(out), &ca) != nil || len(ca.Fingerprint) != 64 {
		t.Fatalf("darkpanel controller web-ca --json: exit %d\n%s\n%s", code, out, errOut)
	}
	dir := filepath.Join(e.home, ".config", "darkpanel")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(dir, "cli.json")
	defer func() { _ = os.Remove(config) }()
	pin := func(fingerprint string) {
		t.Helper()
		if err := os.WriteFile(config, []byte(`{"caFingerprint":"`+fingerprint+`"}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	env := map[string]string{cli.EnvURL: httpsURL, cli.EnvToken: token}
	if _, errOut, code := e.cli(env, "", "hostinfo", "summary", "get"); code == cli.ExitOK {
		t.Errorf("an unpinned CLI connected over HTTPS: %s", errOut)
	}
	pin(ca.Fingerprint)
	out, errOut, code = e.cli(env, "", "hostinfo", "summary", "get", "--json")
	var got struct {
		Hostname string `json:"hostname"`
	}
	if code != cli.ExitOK || json.Unmarshal([]byte(out), &got) != nil || got.Hostname != hostname {
		t.Errorf("pinned darkpanel hostinfo summary get --json over %s: exit %d\n%s\n%s", httpsURL, code, out, errOut)
	}
	pin(strings.Repeat("0", 64))
	if _, errOut, code := e.cli(env, "", "hostinfo", "summary", "get"); code == cli.ExitOK || !strings.Contains(errOut, "caFingerprint") {
		t.Errorf("a wrong pin: exit %d %s, want the fingerprint refused", code, errOut)
	}
}

// denyByDefault is register row 12 over the assembled handler: every method
// on every route the OpenAPI document lists, and on paths no route serves,
// refuses a request without valid credentials, and an unsafe method with a
// session but no CSRF token.
func (e *e2e) denyByDefault(t *testing.T, hostID string) {
	doc, err := schema.OpenAPI(e.s.Registry(), api.Routes())
	if err != nil {
		t.Fatal(err)
	}
	var d struct {
		Paths map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(doc, &d); err != nil {
		t.Fatal(err)
	}
	fill := strings.NewReplacer("{host}", hostID, "{id}", "c1")
	targets := []string{"/api", "/api/v1/nope", "/api/v1/hosts/" + hostID + "/nope", "/", "/hosts/" + hostID}
	for p := range d.Paths {
		targets = append(targets, fill.Replace(p))
	}
	slices.Sort(targets)
	if len(targets) < 20 {
		t.Fatalf("only %d paths to check: %v", len(targets), targets)
	}
	public := func(method, path string) bool {
		get := method == "GET"
		switch {
		case path == "/api/v1/auth/login":
			return method == "POST"
		case path == "/healthz":
			return get
		case path == "/api" || strings.HasPrefix(path, "/api/"):
			return false
		}
		return get // the web UI's files and its index fallback
	}
	for _, path := range targets {
		for _, method := range []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"} {
			if public(method, path) {
				continue
			}
			body := map[string]any{"input": map[string]any{}, "planId": "p"}
			for name, c := range map[string]credentials{
				"no credentials": {},
				"invalid bearer": {bearer: "dpt_01J_bogus"}, //nolint:gosec // G101: a malformed token, not a credential
				"invalid cookie": {session: "bogus"},
			} {
				r := e.do(method, path, body, c)
				if r.status != 401 || r.problemCode() != errcode.AuthRequired {
					t.Errorf("%s %s with %s: %d %s, want 401 auth.required", method, path, name, r.status, r.body)
				}
			}
			if method != "GET" && method != "OPTIONS" {
				r := e.do(method, path, body, credentials{session: e.session})
				if r.status != 403 || r.problemCode() != errcode.CSRF {
					t.Errorf("%s %s with a session and no CSRF token: %d %s, want 403 auth.csrf", method, path, r.status, r.body)
				}
			}
		}
	}
}

// checkAudit reads `darkpanel audit list --json`: the login and the token
// creation are there, the user changes' secrets are redacted, and neither
// the token, the TOTP secret nor the password appears.
func (e *e2e) checkAudit(out, userID, token, totpSecret string) {
	t := e.t
	for _, s := range []string{token, totpSecret, adminPassword} {
		if strings.Contains(out, s) {
			t.Errorf("audit list output contains a secret (%d characters)", len(s))
		}
	}
	var list struct {
		Entries []struct {
			Actor  string          `json:"actor"`
			Action string          `json:"action"`
			Phase  string          `json:"phase"`
			Result string          `json:"result"`
			Source *string         `json:"source"`
			Input  json.RawMessage `json:"input"`
		} `json:"entries"`
	}
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		t.Fatalf("audit list: %v\n%s", err, out)
	}
	found := map[string]string{}
	for _, en := range list.Entries {
		key := en.Action + " " + en.Phase + " " + en.Result
		found[key] = en.Actor
		switch en.Action {
		case "core.user.create":
			if !strings.Contains(string(en.Input), `"password":"[redacted]"`) {
				t.Errorf("core.user.create audit input %s, want the password redacted", en.Input)
			}
			if en.Source == nil || *en.Source != "socket" {
				t.Errorf("core.user.create audit source %v, want socket", en.Source)
			}
		case "core.user.activate":
			if !strings.Contains(string(en.Input), `"code":"[redacted]"`) {
				t.Errorf("core.user.activate audit input %s, want the code redacted", en.Input)
			}
		}
	}
	socketActor := "local:root@" + e.s.LocalHostID()
	if uid := os.Getuid(); uid != 0 {
		socketActor = fmt.Sprintf("local:uid-%d@%s", uid, e.s.LocalHostID())
	}
	for key, actor := range map[string]string{
		"core.auth.login login ok":    "user:" + userID,
		"core.token.create apply ok":  "user:" + userID,
		"core.user.create apply ok":   socketActor,
		"core.user.activate apply ok": socketActor,
	} {
		if got, ok := found[key]; got != actor {
			t.Errorf("audit entry %q: actor %q (present %v), want %q", key, got, ok, actor)
		}
	}
	for key := range found {
		if strings.HasSuffix(key, " error") {
			t.Errorf("audit has an unexpected failed entry %q", key)
		}
	}
}
