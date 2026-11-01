package tailscale

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform/tslocal"
)

// thManifestModule lets the registry check a manifest built from the module's pieces.
type thManifestModule struct{ m core.Manifest }

func (x thManifestModule) Manifest() core.Manifest { return x.m }

func TestTHConstants(t *testing.T) {
	if lockKey != "tailscale" || tailscaledUnit != "tailscaled.service" || controllerUnit != "darkpanel-controller.service" ||
		keyringPath != "/usr/share/keyrings/tailscale-archive-keyring.gpg" || sourcesPath != "/etc/apt/sources.list.d/tailscale.list" ||
		debDir != "/var/lib/darkpanel/agent/tailscale-debs" || pinCheckWindow != 120*time.Second || joinWait != 60*time.Second {
		t.Errorf("constants: %q %q %q %q %q %q %v %v", lockKey, tailscaledUnit, controllerUnit, keyringPath, sourcesPath, debDir, pinCheckWindow, joinWait)
	}
}

// TestTHProductionTS: the production wiring opens no connection, and a test binary never reaches the host's
// tailscaled socket.
func TestTHProductionTS(t *testing.T) {
	p := productionTS()
	if p.d.local == nil || p.d.link == nil || p.d.stun == nil || p.d.https == nil || p.d.osRelease == nil || p.d.hostname == nil || p.d.readFile == nil {
		t.Fatalf("incomplete wiring: %+v", p.d)
	}
	if p.d.arch != runtime.GOARCH {
		t.Errorf("arch %q, want %q", p.d.arch, runtime.GOARCH)
	}
	_, err := p.d.local().Status(context.Background())
	if !errors.Is(err, tslocal.ErrUnavailable) || !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Status in a test binary = %v, want a dial of a socket that does not exist", err)
	}
	want, _ := os.Hostname()
	if got, err := p.d.hostname(); err != nil || got != want {
		t.Errorf("hostname = %q, %v; want %q", got, err, want)
	}
	file := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(file, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if b, err := p.d.readFile(file); err != nil || string(b) != "data" {
		t.Errorf("readFile = %q, %v", b, err)
	}
	if p.d.link.PID() != os.Getpid() {
		t.Errorf("PID = %d, want %d", p.d.link.PID(), os.Getpid())
	}
	ifs, err := p.d.link.Interfaces()
	if err != nil || len(ifs) == 0 {
		t.Fatalf("Interfaces = %v, %v", ifs, err)
	}
	var loopback bool
	for _, i := range ifs {
		loopback = loopback || i.Loopback
	}
	if !loopback {
		t.Error("Interfaces lists no loopback interface")
	}
}

func TestTHModuleWiresTheProductionTS(t *testing.T) {
	m, ok := Module().(module)
	if !ok || m.t.d.local == nil || m.t.d.readFile == nil {
		t.Fatalf("Module() = %#v, want a module over the production ts", Module())
	}
}

func TestTHHTTPSProbe(t *testing.T) {
	var hits, redirected atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch r.URL.Path {
		case "/derp/latency-check":
			if r.Method != http.MethodGet {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			_, _ = w.Write([]byte("body that is discarded"))
		case "/empty":
			w.WriteHeader(http.StatusNoContent)
		case "/moved":
			http.Redirect(w, r, "/target", http.StatusFound)
		case "/target":
			redirected.Add(1)
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	t.Cleanup(srv.Close)
	probe := httpsProbe(srv.Client())
	ctx := context.Background()

	for _, path := range []string{"/derp/latency-check", "/empty"} {
		d, err := probe(ctx, srv.URL+path)
		if err != nil || d <= 0 || d > 5*time.Second {
			t.Errorf("probe %s = %v, %v; want a positive round trip", path, d, err)
		}
	}
	for path, want := range map[string]string{"/broken": "500", "/moved": "302"} {
		if _, err := probe(ctx, srv.URL+path); err == nil || !strings.Contains(err.Error(), want) || !strings.HasPrefix(err.Error(), "tailscale:") {
			t.Errorf("probe %s error = %v, want a tailscale: error naming status %s", path, err, want)
		}
	}
	if redirected.Load() != 0 {
		t.Error("the probe followed a redirect")
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := probe(cancelled, srv.URL+"/empty"); !errors.Is(err, context.Canceled) {
		t.Errorf("probe with a cancelled context = %v", err)
	}

	// A client that does not trust the server's certificate must fail: the probe does not skip verification.
	if _, err := httpsProbe(&http.Client{Timeout: 2 * time.Second})(ctx, srv.URL+"/empty"); err == nil {
		t.Error("the probe accepted a certificate the client does not trust")
	}
	if _, err := probe(ctx, "://bad"); err == nil {
		t.Error("the probe accepted a malformed URL")
	}
}

func TestTHReadOSRelease(t *testing.T) {
	read := func(content string) func(string) ([]byte, error) {
		return func(name string) ([]byte, error) {
			if name != "/etc/os-release" {
				return nil, fs.ErrNotExist
			}
			return []byte(content), nil
		}
	}
	for name, c := range map[string]struct {
		content string
		want    osInfo
	}{
		"ubuntu":              {"NAME=\"Ubuntu\"\nVERSION_CODENAME=noble\nID=ubuntu\nID_LIKE=debian\n", osInfo{"ubuntu", "noble"}},
		"debian quoted":       {"PRETTY_NAME=\"Debian GNU/Linux 12 (bookworm)\"\nID=\"debian\"\nVERSION_CODENAME='bookworm'\n", osInfo{"debian", "bookworm"}},
		"comments and blanks": {"# a comment\n\nID=debian\n  VERSION_CODENAME=trixie  \n#ID=ubuntu\n", osInfo{"debian", "trixie"}},
		"no codename":         {"ID=debian\nVERSION_ID=\"13\"\n", osInfo{ID: "debian"}},
		"empty":               {"", osInfo{}},
	} {
		got, err := readOSRelease(read(c.content))
		if err != nil || got != c.want {
			t.Errorf("%s: %+v, %v; want %+v", name, got, err, c.want)
		}
	}
	if _, err := readOSRelease(func(string) ([]byte, error) { return nil, fs.ErrNotExist }); !errors.Is(err, fs.ErrNotExist) || !strings.HasPrefix(err.Error(), "tailscale:") {
		t.Errorf("a missing file gave %v, want a tailscale: error wrapping fs.ErrNotExist", err)
	}
}

func TestTHAvailable(t *testing.T) {
	h := newTSHost(t)
	tt, ctx := h.ts(), context.Background()

	if err := tt.available(ctx, h.env("")); err != nil {
		t.Errorf("available with a running tailscaled = %v", err)
	}
	h.local.Errs = map[string]error{"Status": tslocal.ErrUnavailable}
	err := tt.available(ctx, h.env(""))
	coded := errcode.From(err)
	if err == nil || coded.Code != errcode.TailscaleUnavailable || coded.Detail != "tailscaled does not answer on its socket" {
		t.Fatalf("available with no socket = %#v, want tailscale.unavailable with the reason", err)
	}
	if !errors.Is(err, tslocal.ErrUnavailable) {
		t.Error("the cause is not kept in the chain")
	}
}

// thHang is a local API whose Status answers only when its context ends.
type thHang struct {
	thLocal
	entered chan struct{}
}

func (a thHang) Status(ctx context.Context) (*tslocal.Status, error) {
	close(a.entered)
	<-ctx.Done()
	return nil, ctx.Err()
}

// TestTHAvailableGivesUpAfterFiveSeconds: a tailscaled that accepts the connection and never answers makes the
// action unavailable once five seconds of the env's clock have passed.
func TestTHAvailableGivesUpAfterFiveSeconds(t *testing.T) {
	h := newTSHost(t)
	hang := thHang{thLocal: thLocal{h.local}, entered: make(chan struct{})}
	tt := h.ts()
	tt.d.local = func() localAPI { return hang }

	done := make(chan error, 1)
	go func() { done <- tt.available(context.Background(), h.env("")) }()
	<-hang.entered
	h.clock.Advance(4999 * time.Millisecond)
	select {
	case err := <-done:
		t.Fatalf("available returned %v before five seconds", err)
	default:
	}
	h.clock.Advance(time.Millisecond)
	select {
	case err := <-done:
		if errcode.From(err).Code != errcode.TailscaleUnavailable {
			t.Errorf("available = %v, want tailscale.unavailable", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("available did not return after the clock passed five seconds")
	}
}

func TestTHOwnedPathsAndExternalUnits(t *testing.T) {
	wantPaths := []core.OwnedPath{
		{Path: "/usr/share/keyrings/tailscale-archive-keyring.gpg", Kind: core.OwnedFile},
		{Path: "/etc/apt/sources.list.d/tailscale.list", Kind: core.OwnedFile},
		{Path: "/var/lib/darkpanel/agent/tailscale-debs", Kind: core.OwnedManagedDir, Pattern: `^tailscale_[0-9][0-9A-Za-z.+~-]{0,63}_(amd64|arm64)\.deb$`},
	}
	if got := ownedPaths(); !reflect.DeepEqual(got, wantPaths) {
		t.Errorf("ownedPaths = %+v\nwant %+v", got, wantPaths)
	}
	wantUnits := []core.ExternalUnit{
		{Name: "tailscaled.service", Ops: []string{core.UnitOpState, core.UnitOpRestart}},
		{Name: "darkpanel-controller.service", Ops: []string{core.UnitOpState}},
	}
	if got := externalUnits(); !reflect.DeepEqual(got, wantUnits) {
		t.Errorf("externalUnits = %+v\nwant %+v", got, wantUnits)
	}
	// The registry's own rules accept both declarations.
	m := core.Manifest{
		ID: ID, Version: 1, DefaultEnabled: true, OwnedPaths: ownedPaths(), ExternalUnits: externalUnits(),
		Migrations: map[core.Side]fs.FS{core.SideAgent: agentMigrations()},
	}
	if _, err := core.NewRegistry(thManifestModule{m}); err != nil {
		t.Errorf("the registry refuses the module's declarations: %v", err)
	}
}

func TestTHInstalledVersion(t *testing.T) {
	h := newTSHost(t)
	ctx := context.Background()
	for name, c := range map[string]struct {
		status string
		want   string
	}{
		"installed":                      {string(thDpkgStatus("1.104.1")), "1.104.1"},
		"epoch and revision are kept":    {string(thDpkgStatus("1:1.104.1-1~noble")), "1:1.104.1-1~noble"},
		"not listed":                     {string(thDpkgStatus("")), ""},
		"removed with its configuration": {"Package: tailscale\nStatus: deinstall ok config-files\nVersion: 1.102.0\n", ""},
		"unpacked only":                  {"Package: tailscale\nStatus: install ok unpacked\nVersion: 1.102.0\n", ""},
		"half installed":                 {"Package: tailscale\nStatus: install ok half-installed\nVersion: 1.102.0\n", ""},
		"a package whose name starts with tailscale": {"Package: tailscaled-extra\nStatus: install ok installed\nVersion: 3.0\n", ""},
		"the last stanza without a trailing newline": {"Package: curl\nStatus: install ok installed\nVersion: 8\n\nPackage: tailscale\nStatus: install ok installed\nVersion: 1.2.3", "1.2.3"},
		"an empty file": {"", ""},
	} {
		h.reads["/var/lib/dpkg/status"] = []byte(c.status)
		got, err := h.ts().installedVersion(ctx, h.env(""))
		if err != nil || got != c.want {
			t.Errorf("%s: %q, %v; want %q", name, got, err, c.want)
		}
	}
	delete(h.reads, "/var/lib/dpkg/status")
	if _, err := h.ts().installedVersion(ctx, h.env("")); !errors.Is(err, fs.ErrNotExist) || !strings.HasPrefix(err.Error(), "tailscale:") {
		t.Errorf("a missing status file gave %v, want a tailscale: error wrapping fs.ErrNotExist", err)
	}
}

func TestTHHeld(t *testing.T) {
	h := newTSHost(t)
	ctx := context.Background()
	for name, c := range map[string]struct {
		stdout string
		want   bool
	}{
		"held":               {"tailscale\n", true},
		"held among others":  {"linux-image-generic\ntailscale\nvim\n", true},
		"nothing held":       {"", false},
		"only a longer name": {"tailscaled\ntailscale-archive-keyring\n", false},
	} {
		h.script("/usr/bin/apt-mark showhold", tsRun{res: core.CmdResult{Stdout: []byte(c.stdout)}})
		got, err := h.ts().held(ctx, h.env(""))
		if err != nil || got != c.want {
			t.Errorf("%s: %v, %v; want %v", name, got, err, c.want)
		}
	}
	h.script("/usr/bin/apt-mark showhold", tsRun{res: core.CmdResult{ExitCode: 100, Stderr: []byte("E: lock\n")}})
	if _, err := h.ts().held(ctx, h.env("")); err == nil {
		t.Error("held ignored a failed apt-mark")
	}
}

func TestTHRun(t *testing.T) {
	h := newTSHost(t)
	ctx, env := context.Background(), h.env("")

	h.script("/usr/bin/apt-get install -o DPkg::Lock::Timeout=120 -y tailscale", tsRun{res: core.CmdResult{Stdout: []byte("done")}})
	res, err := run(ctx, env, "/usr/bin/apt-get", "install", "-o", "DPkg::Lock::Timeout=120", "-y", "tailscale")
	if err != nil || string(res.Stdout) != "done" {
		t.Fatalf("run = %+v, %v", res, err)
	}
	if want := []string{"/usr/bin/apt-get", "install", "-o", "DPkg::Lock::Timeout=120", "-y", "tailscale"}; !reflect.DeepEqual(h.ran[0], want) {
		t.Errorf("ran %v, want %v", h.ran[0], want)
	}

	h.script("/usr/bin/apt-get remove -o DPkg::Lock::Timeout=120 -y tailscale", tsRun{res: core.CmdResult{
		ExitCode: 100, Stdout: []byte("partial"), Stderr: []byte("Reading package lists...\nE: Could not get lock /var/lib/dpkg/lock-frontend\n"),
	}})
	res, err = run(ctx, env, "/usr/bin/apt-get", "remove", "-o", "DPkg::Lock::Timeout=120", "-y", "tailscale")
	if err == nil || res.ExitCode != 100 || string(res.Stdout) != "partial" {
		t.Fatalf("run of a failing command = %+v, %v; want the result and an error", res, err)
	}
	for _, want := range []string{"tailscale:", "/usr/bin/apt-get", "remove", "100", "E: Could not get lock /var/lib/dpkg/lock-frontend"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
	for _, leak := range []string{"Reading package lists", "DPkg::Lock::Timeout", "-y"} {
		if strings.Contains(err.Error(), leak) {
			t.Errorf("error %q holds %q: only the first argument and the last stderr line belong in it", err, leak)
		}
	}

	boom := errors.New("not allowlisted")
	h.script("/usr/bin/apt-mark showhold", tsRun{err: boom})
	if _, err := run(ctx, env, "/usr/bin/apt-mark", "showhold"); !errors.Is(err, boom) || !strings.Contains(err.Error(), "/usr/bin/apt-mark") {
		t.Errorf("a runner error gave %v, want it wrapped with the path", err)
	}
	if _, err := run(ctx, env, "/usr/bin/apt-mark", "hold", "evil"); err == nil {
		t.Error("run passed a vector the production allowlist refuses")
	}
}
