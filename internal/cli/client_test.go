package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// exchange is one request the fake API expects, in order, and its answer.
type exchange struct {
	method, path string // path includes the query string
	body         string // the expected JSON body; "" expects no body
	status       int    // 0 answers 200
	reply        string // the JSON answer; "" answers no body
	problem      bool   // answer as application/problem+json
}

// fakeAPI answers the expected exchanges in order and fails the test on any
// other request. Every request must carry Authorization: Bearer <token>, or
// no Authorization header when token is "".
type fakeAPI struct {
	t     *testing.T
	token string
	mu    sync.Mutex
	want  []exchange
	n     int
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.n >= len(f.want) {
		f.t.Errorf("unexpected request %s %s", r.Method, r.URL.RequestURI())
		w.WriteHeader(http.StatusTeapot)
		return
	}
	x := f.want[f.n]
	f.n++
	if r.Method != x.method || r.URL.RequestURI() != x.path {
		f.t.Errorf("request %d: %s %s, want %s %s", f.n, r.Method, r.URL.RequestURI(), x.method, x.path)
	}
	wantAuth := ""
	if f.token != "" {
		wantAuth = "Bearer " + f.token
	}
	if got := r.Header.Get("Authorization"); got != wantAuth {
		f.t.Errorf("request %d (%s): Authorization %q, want %q", f.n, x.path, got, wantAuth)
	}
	body, _ := io.ReadAll(r.Body)
	wantType := ""
	if x.body != "" {
		wantType = "application/json"
		if !jsonEqual(body, []byte(x.body)) {
			f.t.Errorf("request %d (%s): body %s, want %s", f.n, x.path, body, x.body)
		}
	} else if len(body) > 0 {
		f.t.Errorf("request %d (%s): unexpected body %s", f.n, x.path, body)
	}
	if got := r.Header.Get("Content-Type"); got != wantType {
		f.t.Errorf("request %d (%s): Content-Type %q, want %q", f.n, x.path, got, wantType)
	}
	if x.reply != "" {
		w.Header().Set("Content-Type", "application/json")
		if x.problem {
			w.Header().Set("Content-Type", "application/problem+json")
		}
	}
	status := x.status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = io.WriteString(w, x.reply)
}

func (f *fakeAPI) check() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.n != len(f.want) {
		f.t.Errorf("the fake API served %d of %d expected requests", f.n, len(f.want))
	}
}

// newFakeAPI serves want over HTTP on 127.0.0.1 and returns the base URL.
func newFakeAPI(t *testing.T, token string, want ...exchange) string {
	t.Helper()
	f := &fakeAPI{t: t, token: token, want: want}
	srv := httptest.NewServer(f)
	t.Cleanup(func() { srv.Close(); f.check() })
	return srv.URL
}

// newFakeSocket serves want on a Unix socket and returns its path. The
// socket carries no token.
func newFakeSocket(t *testing.T, want ...exchange) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "dp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "c.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeAPI{t: t, want: want}
	srv := &http.Server{Handler: f, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close(); f.check() })
	return path
}

func jsonEqual(a, b []byte) bool {
	var x, y any
	return json.Unmarshal(a, &x) == nil && json.Unmarshal(b, &y) == nil && reflect.DeepEqual(x, y)
}

// writeConfig writes the CLI config file under home's .config with mode.
func writeConfig(t *testing.T, home, content string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(home, ".config", "darkpanel", "cli.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func homeAt(dir string) func() (string, error) { return func() (string, error) { return dir, nil } }

func TestConnectNetworkPrecedence(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, `{"url":"http://127.0.0.3:8080","token":"dpt_config"}`, 0o600)
	cases := []struct {
		name, flag          string
		env                 map[string]string
		wantBase, wantToken string
	}{
		{"flag first", "http://127.0.0.1:9000/", map[string]string{EnvURL: "http://127.0.0.2:8080", EnvToken: "dpt_env"},
			"http://127.0.0.1:9000", "dpt_env"},
		{"environment URL withholds the file token", "", map[string]string{EnvURL: "http://127.0.0.2:8080"}, "http://127.0.0.2:8080", ""},
		{"flag URL withholds the file token", "http://127.0.0.1:9000", map[string]string{}, "http://127.0.0.1:9000", ""},
		{"the file's own URL again", "HTTP://127.0.0.3:8080/", map[string]string{}, "http://127.0.0.3:8080", "dpt_config"},
		{"config file last", "", map[string]string{}, "http://127.0.0.3:8080", "dpt_config"},
	}
	for _, c := range cases {
		cl, err := connectNetwork(c.flag, envOf(c.env), homeAt(home))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if cl.base != c.wantBase || cl.token != c.wantToken {
			t.Errorf("%s: base %q token %q, want %q %q", c.name, cl.base, cl.token, c.wantBase, c.wantToken)
		}
	}
	cl, err := connectNetwork("", envOf(nil), homeAt(t.TempDir()))
	if err != nil || cl.base != DefaultURL || cl.token != "" {
		t.Errorf("no config: base %q token %q err %v, want %q and no token", cl.base, cl.token, err, DefaultURL)
	}
}

func TestConnectNetworkRefusesATokenOverPlainHTTP(t *testing.T) {
	withToken := envOf(map[string]string{EnvToken: "dpt_secret"})
	cases := []struct {
		url     string
		refused bool
	}{
		{"http://192.0.2.10:8080", true},
		{"http://panel.example:8080", true},
		{"https://192.0.2.10:8443", false},
		{"http://localhost:8080", false},
		{"http://LOCALHOST:8080", true},
		{"http://127.0.0.2:8080", false},
		{"http://[::1]:8080", false},
	}
	for _, c := range cases {
		_, err := connectNetwork(c.url, withToken, homeAt(t.TempDir()))
		if c.refused {
			if ExitCode(err) != ExitUsage || !strings.Contains(err.Error(), "plain http") || strings.Contains(err.Error(), "dpt_secret") {
				t.Errorf("%s with a token: %v, want a usage error about plain http that hides the token", c.url, err)
			}
		} else if err != nil {
			t.Errorf("%s with a token: %v", c.url, err)
		}
	}
	if _, err := connectNetwork("http://192.0.2.10:8080", envOf(nil), homeAt(t.TempDir())); err != nil {
		t.Errorf("plain http to another host without a token: %v", err)
	}
	home := t.TempDir()
	writeConfig(t, home, `{"url":"http://192.0.2.10:8080","token":"dpt_config"}`, 0o600)
	if _, err := connectNetwork("", envOf(nil), homeAt(home)); ExitCode(err) != ExitUsage {
		t.Errorf("a config file pairing a token with plain http to another host: %v, want a usage error", err)
	}
	home = t.TempDir()
	writeConfig(t, home, `{"token":"dpt_config"}`, 0o600)
	cl, err := connectNetwork("", envOf(nil), homeAt(home))
	if err != nil || cl.base != DefaultURL || cl.token != "dpt_config" {
		t.Errorf("config file token without a URL: base %q token %q err %v, want the token for %s", cl.base, cl.token, err, DefaultURL)
	}
	cl, err = connectNetwork("https://192.0.2.10:8443", envOf(nil), homeAt(home))
	if err != nil || cl.token != "" {
		t.Errorf("config file token without a URL, --url elsewhere: token %q err %v, want no token", cl.token, err)
	}
}

func TestConfigPath(t *testing.T) {
	home := t.TempDir()
	cases := []struct {
		xdg, want string
	}{
		{"/srv/config", "/srv/config/darkpanel/cli.json"},
		{"relative/config", filepath.Join(home, ".config", "darkpanel", "cli.json")},
		{"", filepath.Join(home, ".config", "darkpanel", "cli.json")},
	}
	for _, c := range cases {
		got, err := configPath(envOf(map[string]string{"XDG_CONFIG_HOME": c.xdg}), homeAt(home))
		if err != nil || got != c.want {
			t.Errorf("XDG_CONFIG_HOME %q: %q, %v; want %q", c.xdg, got, err, c.want)
		}
	}
}

func TestConfigFileRefusals(t *testing.T) {
	refused := func(name, home, path, want string) {
		t.Helper()
		_, err := connectNetwork("", envOf(nil), homeAt(home))
		if err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "chmod 600 "+path) ||
			strings.Contains(err.Error(), "dpt_secret") {
			t.Errorf("%s: error %v, want a refusal saying %q and naming chmod 600 %s", name, err, want, path)
		}
	}
	for _, mode := range []os.FileMode{0o640, 0o604, 0o644, 0o620, 0o602, 0o610} {
		home := t.TempDir()
		path := writeConfig(t, home, `{"token":"dpt_secret"}`, mode)
		refused(fmt.Sprintf("mode %o", mode), home, path, "accessible by group or others")
	}
	home := t.TempDir()
	target := writeConfig(t, t.TempDir(), `{"token":"dpt_secret"}`, 0o600)
	link := filepath.Join(home, ".config", "darkpanel", "cli.json")
	if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	refused("symbolic link", home, link, "symbolic link")
	if os.Geteuid() == 0 {
		home := t.TempDir()
		path := writeConfig(t, home, `{"token":"dpt_secret"}`, 0o600)
		if err := os.Chown(path, 65534, 65534); err != nil {
			t.Fatal(err)
		}
		refused("another owner", home, path, "not owned by the current user")
	}
	home = t.TempDir()
	writeConfig(t, home, `{"token":"dpt_secret","fingerprint":"x"}`, 0o600)
	if _, err := connectNetwork("", envOf(nil), homeAt(home)); err == nil || !strings.Contains(err.Error(), `"fingerprint"`) {
		t.Errorf("unknown field: error %v", err)
	}
	if _, err := connectNetwork("ftp://127.0.0.1", envOf(nil), homeAt(t.TempDir())); ExitCode(err) != ExitUsage {
		t.Errorf("ftp URL: error %v, want a usage error", err)
	}
}

func TestClientDo(t *testing.T) {
	base := newFakeAPI(t, "dpt_t1",
		exchange{method: "POST", path: "/api/v1/actions/core.host.list/v1/read", body: `{"input":{}}`,
			reply: `{"output":{"hosts":[]}}`},
		exchange{method: "GET", path: "/api/v1/changes?limit=5", reply: `{"changes":[]}`},
		exchange{method: "POST", path: "/api/v1/actions/core.token.create/v1/plan", body: `{"input":{"name":""}}`,
			status: 422, problem: true,
			reply: `{"type":"about:blank","title":"Invalid input","status":422,"detail":"invalid input",` +
				`"code":"request.invalid_input","errors":[{"path":"/name","message":"too short"}]}`},
		exchange{method: "GET", path: "/api/v1/changes", status: 401, problem: true,
			reply: `{"type":"about:blank","title":"Authentication required","status":401,` +
				`"detail":"authentication required","code":"auth.required"}`},
		exchange{method: "GET", path: "/api/v1/changes/c1", status: 502},
	)
	c, err := connectNetwork(base, envOf(map[string]string{EnvToken: "dpt_t1"}), homeAt(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	var out struct {
		Output json.RawMessage `json:"output"`
	}
	if err := c.do(ctx, "POST", "/api/v1/actions/core.host.list/v1/read", map[string]any{"input": map[string]any{}}, &out); err != nil {
		t.Fatal(err)
	}
	if string(out.Output) != `{"hosts":[]}` {
		t.Errorf("output %s", out.Output)
	}
	if err := c.do(ctx, "GET", "/api/v1/changes?limit=5", nil, nil); err != nil {
		t.Fatal(err)
	}
	err = c.do(ctx, "POST", "/api/v1/actions/core.token.create/v1/plan", map[string]any{"input": map[string]any{"name": ""}}, nil)
	var p *problemError
	if !errors.As(err, &p) || p.Code != errcode.InvalidInput || p.Status != 422 || len(p.Fields) != 1 || p.Fields[0].Path != "/name" {
		t.Errorf("422: error %#v", err)
	}
	if err := c.do(ctx, "GET", "/api/v1/changes", nil, nil); ExitCode(ranError(err)) != ExitAuth {
		t.Errorf("401: error %v, want exit %d", err, ExitAuth)
	}
	if err := c.do(ctx, "GET", "/api/v1/changes/c1", nil, nil); err == nil || !strings.Contains(err.Error(), "502") {
		t.Errorf("502 without problem details: error %v", err)
	}
}

func TestSocketClient(t *testing.T) {
	path := newFakeSocket(t, exchange{method: "GET", path: "/api/v1/controller/web-ca",
		reply: `{"pem":"-----BEGIN CERTIFICATE-----\n","fingerprint":"ab"}`})
	var out struct {
		PEM         string `json:"pem"`
		Fingerprint string `json:"fingerprint"`
	}
	if err := newSocketClient(path).do(context.Background(), "GET", "/api/v1/controller/web-ca", nil, &out); err != nil {
		t.Fatal(err)
	}
	if out.Fingerprint != "ab" {
		t.Errorf("fingerprint %q", out.Fingerprint)
	}
	missing := filepath.Join(t.TempDir(), "none.sock")
	if err := newSocketClient(missing).do(context.Background(), "GET", "/healthz", nil, nil); err == nil ||
		!strings.Contains(err.Error(), missing) {
		t.Errorf("missing socket: error %v, want one naming %s", err, missing)
	}
}
