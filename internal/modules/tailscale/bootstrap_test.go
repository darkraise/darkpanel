package tailscale

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform"
	"github.com/darkraise/darkpanel/internal/platform/tslocal"
)

const (
	jnKey     = "tskey-auth-kABCDEF1234-SECRETSECRETSECRET"
	jnVersion = "1.104.1"
	jnAptGet  = "/usr/bin/apt-get"
)

// jnLocal is the fake tailscaled of a join. Until Start it answers like the host's fake local API, except that
// its first downFor Status calls fail as a socket that is not there yet; after Start, Status answers after in order,
// the last one for ever.
type jnLocal struct {
	localAPI

	mu         sync.Mutex
	downFor    int
	started    bool
	after      []*tslocal.Status
	sinceStart int
}

func (l *jnLocal) Status(ctx context.Context) (*tslocal.Status, error) {
	l.mu.Lock()
	if l.downFor > 0 {
		l.downFor--
		l.mu.Unlock()
		return nil, fmt.Errorf("%w: connection refused", tslocal.ErrUnavailable)
	}
	if l.started && len(l.after) > 0 {
		st := *l.after[min(l.sinceStart, len(l.after)-1)]
		l.sinceStart++
		l.mu.Unlock()
		return &st, nil
	}
	l.mu.Unlock()
	return l.localAPI.Status(ctx)
}

func (l *jnLocal) Start(ctx context.Context, o tslocal.StartOptions) error {
	if err := l.localAPI.Start(ctx, o); err != nil {
		return err
	}
	l.mu.Lock()
	l.started = true
	l.mu.Unlock()
	return nil
}

func jnStatus(state string, addrs ...string) *tslocal.Status {
	st := &tslocal.Status{BackendState: state}
	for _, a := range addrs {
		st.TailscaleIPs = append(st.TailscaleIPs, netip.MustParseAddr(a))
	}
	return st
}

// jnRunner passes every vector to the host's runner, records the time the context of each had left, and leaves
// tailscale installed in dpkg's status file when the install vector (APT-2) succeeds, which the host fake does not.
type jnRunner struct {
	h         *tsHost
	deadlines *[]time.Duration
}

func (r jnRunner) Run(ctx context.Context, cmd core.Cmd) (core.CmdResult, error) {
	if dl, ok := ctx.Deadline(); ok {
		*r.deadlines = append(*r.deadlines, time.Until(dl))
	}
	res, err := r.h.Run(ctx, cmd)
	if err == nil && res.ExitCode == 0 && cmd.Path == jnAptGet && slices.Equal(cmd.Args, platform.AptInstallTailscale()) {
		r.h.mu.Lock()
		r.h.reads[thDpkgPath] = thDpkgStatus(jnVersion)
		r.h.mu.Unlock()
	}
	return res, err
}

// jnSetup is a supported host without tailscale whose tailscaled, once it exists, is logged out.
type jnSetup struct {
	h         *tsHost
	t         ts
	local     *jnLocal
	d         JoinDeps
	out       *bytes.Buffer
	deadlines []time.Duration
}

func jnNewSetup(t *testing.T) *jnSetup {
	t.Helper()
	h := newTSHost(t)
	h.reads[thDpkgPath] = thDpkgStatus("")
	h.local.Status = jnStatus("NeedsLogin")
	s := &jnSetup{h: h, out: &bytes.Buffer{}}
	tt := h.ts()
	s.local = &jnLocal{localAPI: tt.d.local()}
	tt.d.local = func() localAPI { return s.local }
	s.t = tt
	s.d = JoinDeps{Exec: jnRunner{h: h, deadlines: &s.deadlines}, Files: h, DB: h.db, Clock: h.clock, Out: s.out}
	return s
}

// installed makes the package present from the start.
func (s *jnSetup) installed() { s.h.reads[thDpkgPath] = thDpkgStatus(jnVersion) }

// jnDrive runs call while advancing the clock by one poll whenever it waits.
func jnDrive(t *testing.T, h *tsHost, call func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		call()
	}()
	for deadline := time.Now().Add(20 * time.Second); ; {
		select {
		case <-done:
			return
		default:
		}
		if h.clock.Pending() > 0 {
			h.clock.Advance(joinPoll)
		} else {
			time.Sleep(time.Millisecond)
		}
		if time.Now().After(deadline) {
			t.Fatal("the call did not finish")
		}
	}
}

func (s *jnSetup) join(t *testing.T) ([]netip.Addr, error) {
	t.Helper()
	var addrs []netip.Addr
	var err error
	jnDrive(t, s.h, func() { addrs, err = s.t.join(context.Background(), s.d, jnKey) })
	return addrs, err
}

func jnInstallRow(t *testing.T, h *tsHost) (id, version string, ok bool) {
	t.Helper()
	id, version, ok, err := liveInstall(context.Background(), h.db.Read())
	if err != nil {
		t.Fatal(err)
	}
	return id, version, ok
}

func TestJnJoinInstallsAndJoins(t *testing.T) {
	s := jnNewSetup(t)
	s.local.after = []*tslocal.Status{jnStatus("Starting"), jnStatus("Running", "fd7a:115c:a1e0::7", "100.64.0.7")}
	start := s.h.clock.Now()
	addrs, err := s.join(t)
	if err != nil {
		t.Fatal(err)
	}
	if want := []netip.Addr{netip.MustParseAddr("100.64.0.7"), netip.MustParseAddr("fd7a:115c:a1e0::7")}; !slices.Equal(addrs, want) {
		t.Errorf("addresses %v, want %v (IPv4 first)", addrs, want)
	}
	wantRan := [][]string{append([]string{jnAptGet}, platform.AptUpdateTailscale()...), append([]string{jnAptGet}, platform.AptInstallTailscale()...)}
	if !reflect.DeepEqual(s.h.ran, wantRan) {
		t.Errorf("ran %v, want APT-1 then APT-2 and nothing else", s.h.ran)
	}
	if len(s.deadlines) != 2 || s.deadlines[0] <= 14*time.Minute || s.deadlines[0] > 15*time.Minute || s.deadlines[1] <= 14*time.Minute || s.deadlines[1] > 15*time.Minute {
		t.Errorf("apt-get calls had %v left, want both bounded by 15 minutes", s.deadlines)
	}
	keyring, sources, _ := s.t.repoFiles()
	if !bytes.Equal(s.h.files[keyringPath], keyring) || !bytes.Equal(s.h.files[sourcesPath], sources) || len(s.h.files) != 2 {
		t.Errorf("files %v, want the keyring and the source list written", s.h.files)
	}
	if len(s.h.local.Starts) != 1 {
		t.Fatalf("Start called %d times, want once", len(s.h.local.Starts))
	}
	if o := s.h.local.Starts[0]; o.AuthKey != jnKey || o.UpdatePrefs == nil || !o.UpdatePrefs.WantRunning || o.UpdatePrefs.LoggedOut || !o.UpdatePrefs.CorpDNS {
		t.Errorf("Start options %+v, want the key and the current preferences with WantRunning and not LoggedOut", o)
	}
	if s.h.local.LoginInteractives != 1 {
		t.Errorf("%d login-interactive calls, want one after Start", s.h.local.LoginInteractives)
	}
	if id, v, ok := jnInstallRow(t, s.h); !ok || id != "install" || v != jnVersion {
		t.Errorf("recorded install %q %q (found %v), want install %s", id, v, ok, jnVersion)
	}
	if waited := s.h.clock.Now().Sub(start); waited != joinPoll {
		t.Errorf("waited %v, want one poll of %v for the Starting state", waited, joinPoll)
	}
	out := s.out.String()
	if !strings.Contains(out, "installing the package") || !strings.Contains(out, "joined the tailnet as 100.64.0.7, fd7a:115c:a1e0::7") || strings.Contains(out, jnKey) {
		t.Errorf("progress output %q, want the install and the join named, and no key", out)
	}
	if len(s.h.journal.effects()) != 0 {
		t.Errorf("effects %v, want none: the installer has no change", s.h.journal.effects())
	}
}

func TestJnJoinLeavesAnInstalledPackage(t *testing.T) {
	s := jnNewSetup(t)
	s.installed()
	s.local.after = []*tslocal.Status{jnStatus("Running", "100.64.0.7")}
	if _, err := s.join(t); err != nil {
		t.Fatal(err)
	}
	if len(s.h.ran) != 0 || len(s.h.files) != 0 {
		t.Errorf("ran %v and wrote %v for a host that has the package", s.h.ran, s.h.files)
	}
	if _, _, ok := jnInstallRow(t, s.h); ok {
		t.Error("an install was recorded for a package Darkpanel did not install")
	}
	if !strings.Contains(s.out.String(), "version "+jnVersion+" is already installed") || len(s.h.local.Starts) != 1 {
		t.Errorf("output %q with %d Start calls, want the package noted and the login made", s.out.String(), len(s.h.local.Starts))
	}
}

func TestJnJoinWaitsForTailscaled(t *testing.T) {
	s := jnNewSetup(t)
	s.installed()
	s.local.downFor = 3
	s.local.after = []*tslocal.Status{jnStatus("Running", "100.64.0.7")}
	start := s.h.clock.Now()
	if _, err := s.join(t); err != nil {
		t.Fatal(err)
	}
	if waited := s.h.clock.Now().Sub(start); waited != 3*joinPoll {
		t.Errorf("waited %v, want 3 polls of %v for the socket", waited, joinPoll)
	}
}

func TestJnJoinGivesUpOnTailscaled(t *testing.T) {
	s := jnNewSetup(t)
	s.installed()
	s.local.downFor = 1 << 20
	start := s.h.clock.Now()
	_, err := s.join(t)
	if err == nil || !strings.HasPrefix(err.Error(), "tailscale: join: tailscaled:") || !strings.Contains(err.Error(), "60s") || !errors.Is(err, tslocal.ErrUnavailable) {
		t.Fatalf("error %v, want a failure of step tailscaled after 60s that keeps the socket error", err)
	}
	if waited := s.h.clock.Now().Sub(start); waited != joinWait || len(s.h.local.Starts) != 0 {
		t.Errorf("waited %v with %d Start calls, want %v and none", waited, len(s.h.local.Starts), joinWait)
	}
}

func TestJnJoinKeepsAJoinedNode(t *testing.T) {
	s := jnNewSetup(t)
	s.installed()
	s.h.local.Status = jnStatus("Running", "100.64.0.9")
	addrs, err := s.join(t)
	if err != nil || !slices.Equal(addrs, []netip.Addr{netip.MustParseAddr("100.64.0.9")}) {
		t.Fatalf("addresses %v, error %v, want the node's address", addrs, err)
	}
	if len(s.h.local.Starts) != 0 || s.h.local.LoginInteractives != 0 || !strings.Contains(s.out.String(), "already joined as 100.64.0.9") {
		t.Errorf("%d Start calls and output %q, want no login for a node that is on the tailnet", len(s.h.local.Starts), s.out.String())
	}
}

func TestJnJoinGivesUpWithoutAnAddress(t *testing.T) {
	s := jnNewSetup(t)
	s.installed()
	s.local.after = []*tslocal.Status{jnStatus("NeedsLogin")}
	start := s.h.clock.Now()
	_, err := s.join(t)
	if err == nil || !strings.HasPrefix(err.Error(), "tailscale: join: address:") || !strings.Contains(err.Error(), "60s") || !strings.Contains(err.Error(), "NeedsLogin") {
		t.Fatalf("error %v, want a failure of step address after 60s naming the backend state", err)
	}
	if waited := s.h.clock.Now().Sub(start); waited != joinWait {
		t.Errorf("waited %v, want %v from the start of the join", waited, joinWait)
	}
}

func TestJnJoinNeverShowsTheKey(t *testing.T) {
	s := jnNewSetup(t)
	s.installed()
	s.h.local.Errs = map[string]error{"Start": errors.New("tailscaled refused the key " + jnKey + ": invalid")}
	_, err := s.join(t)
	if err == nil || !strings.HasPrefix(err.Error(), "tailscale: join: login:") || strings.Contains(err.Error(), jnKey) || !strings.Contains(err.Error(), "[redacted]") {
		t.Errorf("error %v, want a failure of step login with the key redacted", err)
	}
	if strings.Contains(s.out.String(), jnKey) {
		t.Errorf("progress output %q shows the key", s.out.String())
	}
}

func TestJnJoinFailsWhenTheLoginDoesNotStart(t *testing.T) {
	s := jnNewSetup(t)
	s.installed()
	s.h.local.Errs = map[string]error{"LoginInteractive": errors.New("login access denied for " + jnKey)}
	_, err := s.join(t)
	if err == nil || !strings.HasPrefix(err.Error(), "tailscale: join: login:") || strings.Contains(err.Error(), jnKey) || !strings.Contains(err.Error(), "login access denied") {
		t.Errorf("error %v, want a failure of step login naming the refusal without the key", err)
	}
	if len(s.h.local.Starts) != 1 || s.h.local.LoginInteractives != 1 {
		t.Errorf("%d Start and %d login-interactive calls, want one each", len(s.h.local.Starts), s.h.local.LoginInteractives)
	}
}

func TestJnJoinNamesTheFailedStep(t *testing.T) {
	apt1 := strings.Join(append([]string{jnAptGet}, platform.AptUpdateTailscale()...), " ")
	tests := []struct {
		name   string
		setup  func(s *jnSetup)
		want   string
		wantIn string // text the error must also hold
		ranApt int
	}{
		{"the repository differs", func(s *jnSetup) { s.h.reads[sourcesPath] = []byte("deb https://example.com/apt stable main\n") }, "join: repository:", "", 0},
		{"the repository refresh fails", func(s *jnSetup) {
			s.h.script(apt1, tsRun{res: core.CmdResult{ExitCode: 100, Stderr: []byte("E: Failed to fetch https://pkgs.tailscale.com\n")}})
		}, "join: install:", "Failed to fetch", 1},
		{"apt-get finishes without the package", func(s *jnSetup) { s.d.Exec = s.h }, "join: install:", "not installed", 2},
		{"dpkg's status cannot be read", func(s *jnSetup) { s.t.d.readFile = func(string) ([]byte, error) { return nil, os.ErrPermission } }, "join: inspect:", "permission", 0},
		{"the preferences cannot be read", func(s *jnSetup) {
			s.installed()
			s.h.local.Errs = map[string]error{"Prefs": errors.New("prefs unavailable")}
		}, "join: login:", "prefs unavailable", 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := jnNewSetup(t)
			tc.setup(s)
			_, err := s.join(t)
			if err == nil || !strings.HasPrefix(err.Error(), "tailscale: "+tc.want) || !strings.Contains(err.Error(), tc.wantIn) {
				t.Fatalf("error %v, want a failure starting %q holding %q", err, "tailscale: "+tc.want, tc.wantIn)
			}
			apt := 0
			for _, v := range s.h.ran {
				if v[0] == jnAptGet {
					apt++
				}
			}
			if apt != tc.ranApt || len(s.h.local.Starts) != 0 {
				t.Errorf("apt-get ran %d times and Start %d times, want %d and none", apt, len(s.h.local.Starts), tc.ranApt)
			}
			if _, _, ok := jnInstallRow(t, s.h); ok {
				t.Error("an install was recorded for a join that failed before the package was installed")
			}
		})
	}
}

func TestJnJoinReportsARepositoryConflict(t *testing.T) {
	s := jnNewSetup(t)
	s.h.reads[sourcesPath] = []byte("deb https://example.com/apt stable main\n")
	_, err := s.join(t)
	var coded *errcode.Error
	if !errors.As(err, &coded) || coded.Code != errcode.TailscaleRepoConflict || len(s.h.files) != 0 {
		t.Errorf("error %v with files %v, want %s and nothing written", err, s.h.files, errcode.TailscaleRepoConflict)
	}
}

func TestJnJoinRecordsAReinstall(t *testing.T) {
	s := jnNewSetup(t)
	ctx := context.Background()
	if err := s.h.db.Write(ctx, func(tx *sql.Tx) error {
		return insertInstall(ctx, tx, "install", "1.90.1", s.h.clock.Now().Add(-time.Hour))
	}); err != nil {
		t.Fatal(err)
	}
	if err := setInstallRemoved(ctx, s.h.db, "install", s.h.clock.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	s.local.after = []*tslocal.Status{jnStatus("Running", "100.64.0.7")}
	if _, err := s.join(t); err != nil {
		t.Fatal(err)
	}
	if id, v, ok := jnInstallRow(t, s.h); !ok || id != "install" || v != jnVersion {
		t.Errorf("recorded install %q %q (found %v), want the earlier row live again at %s", id, v, ok, jnVersion)
	}
}

func TestJnJoinChecksItsArguments(t *testing.T) {
	h := newTSHost(t)
	full := JoinDeps{Exec: h, Files: h, DB: h.db, Clock: h.clock, Out: io.Discard}
	for _, tc := range []struct {
		name string
		deps func(d *JoinDeps)
		key  string
		want string
	}{
		{"no runner", func(d *JoinDeps) { d.Exec = nil }, jnKey, "JoinDeps.Exec"},
		{"no file provider", func(d *JoinDeps) { d.Files = nil }, jnKey, "JoinDeps.Files"},
		{"no database", func(d *JoinDeps) { d.DB = nil }, jnKey, "JoinDeps.DB"},
		{"no clock", func(d *JoinDeps) { d.Clock = nil }, jnKey, "JoinDeps.Clock"},
		{"no output", func(d *JoinDeps) { d.Out = nil }, jnKey, "JoinDeps.Out"},
		{"no key", func(*JoinDeps) {}, "", "auth key"},
		{"a key with whitespace", func(*JoinDeps) {}, "tskey-auth-a b", "auth key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := full
			tc.deps(&d)
			addrs, err := Join(context.Background(), d, tc.key)
			if err == nil || !strings.HasPrefix(err.Error(), "tailscale: join:") || !strings.Contains(err.Error(), tc.want) || addrs != nil {
				t.Errorf("Join = %v, %v; want a refusal naming %q", addrs, err, tc.want)
			}
			if tc.key != "" && strings.Contains(err.Error(), tc.key) {
				t.Errorf("error %q shows the key", err)
			}
			if len(h.ran) != 0 {
				t.Errorf("ran %v before the arguments were checked", h.ran)
			}
		})
	}
}

func TestJnJoinOverTheSocket(t *testing.T) {
	if got := joinSocket(""); got != tslocal.DefaultSocket {
		t.Errorf("default socket %q, want %q", got, tslocal.DefaultSocket)
	}
	dir, err := os.MkdirTemp("", "jn")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "ts.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/localapi/v0/status" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, `{"BackendState":"NeedsLogin"}`)
	}))
	_ = srv.Listener.Close()
	srv.Listener = l
	srv.Start()
	t.Cleanup(srv.Close)
	tt, closeClient := joinTS(JoinDeps{Socket: sock})
	defer closeClient()
	st, err := tt.d.local().Status(context.Background())
	if err != nil || st.BackendState != "NeedsLogin" {
		t.Errorf("Status over the socket = %+v, %v, want the fake tailscaled's answer", st, err)
	}
	if tt.d.readFile == nil || tt.d.osRelease == nil || tt.d.arch == "" {
		t.Error("the join's ts lacks the production readFile, osRelease or arch")
	}
}

// TestJnProductionProvidersOverAFreshDatabase builds the join's dependencies as the installer can before the
// agent has ever run: the production runner, and a file provider over an agent database that was just migrated.
func TestJnProductionProvidersOverAFreshDatabase(t *testing.T) {
	h := newTSHost(t)
	dir := t.TempDir()
	target := filepath.Join(dir, "tailscale.list")
	files := platform.NewFiles(platform.FilesConfig{
		Owned:     map[string][]core.OwnedPath{ID: {{Path: target, Kind: core.OwnedFile}}},
		Ownership: platform.NewOwnership(h.db),
		Clock:     h.clock,
		TestMode:  true,
	})
	d := JoinDeps{Exec: platform.NewRunner(platform.ProductionRules()), Files: files, DB: h.db, Clock: core.SystemClock{}, Out: io.Discard}
	if err := d.check(jnKey); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := d.Files.WriteFile(ctx, ID, target, []byte("deb x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if owned, err := d.Files.Owns(ctx, ID, target); err != nil || !owned {
		t.Errorf("Owns = %v, %v, want the written file recorded as the module's", owned, err)
	}
	if err := d.Files.Remove(ctx, ID, target); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec.Run(ctx, core.Cmd{Path: "/bin/sh", Args: []string{"-c", "true"}}); err == nil {
		t.Error("the production runner ran a program outside the allowlist")
	}
}
