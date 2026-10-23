package listen

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// logBuffer collects the service's log; checks may run on another
// goroutine during the shutdown test.
type logBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *logBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// fakeInterfaces stands in for SystemInterfaceAddrs.
type fakeInterfaces struct {
	mu      sync.Mutex
	addrs   []netip.Addr
	present bool
	calls   int
}

func (f *fakeInterfaces) set(present bool, list ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.present = present
	f.addrs = addrs(list...)
}

func (f *fakeInterfaces) list(name string) ([]netip.Addr, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if name != TailscaleInterface || !f.present {
		return nil, false, nil
	}
	return append(f.addrs, netip.MustParseAddr("fe80::1")), true, nil
}

func (f *fakeInterfaces) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

type serviceFixture struct {
	db     *store.DB
	clock  *coretest.FakeClock
	auth   *Authority
	ifs    *fakeInterfaces
	binder *fakeBinder
	log    *logBuffer
}

func newServiceFixture(t *testing.T) *serviceFixture {
	t.Helper()
	db := openControllerDB(t)
	clock := coretest.NewFakeClock(testStart)
	a, err := LoadOrCreateAuthority(context.Background(), db, newSealer(t), clock)
	if err != nil {
		t.Fatal(err)
	}
	return &serviceFixture{db: db, clock: clock, auth: a, ifs: &fakeInterfaces{}, binder: &fakeBinder{}, log: &logBuffer{}}
}

func (f *serviceFixture) start(t *testing.T, values ...string) (*Service, error) {
	t.Helper()
	specs, err := ParseSpecs(values)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Start(context.Background(), Config{
		Specs: specs, Hostname: "panel", DB: f.db, Authority: f.auth, Clock: f.clock,
		Handler: schemeHandler(), Log: slog.New(slog.NewTextHandler(f.log, nil)),
		Interfaces: f.ifs.list, Bind: f.binder.bind,
	})
	if err == nil {
		t.Cleanup(func() { _ = s.Shutdown(context.Background()) })
	}
	return s, err
}

func endpointsOf(st State) []Endpoint {
	out := make([]Endpoint, 0, len(st.Listeners))
	for _, b := range st.Listeners {
		out = append(out, b.Endpoint)
	}
	return out
}

func savedAddrs(t *testing.T, db *store.DB) string {
	t.Helper()
	var v string
	if err := db.Read().QueryRowContext(context.Background(),
		`SELECT value FROM core_meta WHERE key = 'tailscale_addrs'`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

// rootClient trusts only the authority's root and checks the name
// localhost, because the fake binder serves every endpoint on 127.0.0.1.
func rootClient(t *testing.T, a *Authority) *http.Client {
	t.Helper()
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(a.RootPEM()) {
		t.Fatal("RootPEM did not parse")
	}
	return &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool, ServerName: "localhost"},
	}}
}

func TestServiceDefaultsWithoutTailscale(t *testing.T) {
	f := newServiceFixture(t)
	s, err := f.start(t)
	if err != nil {
		t.Fatal(err)
	}
	st := s.State()
	want := []Endpoint{
		{Addr: netip.MustParseAddrPort("127.0.0.1:8080")},
		{Addr: netip.MustParseAddrPort("[::1]:8080"), Optional: true},
	}
	if !reflect.DeepEqual(endpointsOf(st), want) {
		t.Fatalf("listeners = %v, want %v", endpointsOf(st), want)
	}
	if st.TailscaleBound || st.Message != "No Tailscale address found; serving loopback only." {
		t.Fatalf("state = %v, %q", st.TailscaleBound, st.Message)
	}
	if got := get(t, &http.Client{Timeout: 10 * time.Second}, "http://"+st.Listeners[0].Addr.String()+"/"); got != "tls=false" {
		t.Fatalf("loopback answered %q", got)
	}
	if _, err := s.leaf.GetCertificate(&tls.ClientHelloInfo{}); err == nil {
		t.Fatal("a leaf was issued with no HTTPS listener")
	}
}

func TestServiceBindsSavedAddressesBeforeTailscaled(t *testing.T) {
	f := newServiceFixture(t)
	if err := SaveTailscaleAddrs(context.Background(), f.db, addrs("100.64.0.1")); err != nil {
		t.Fatal(err)
	}
	s, err := f.start(t)
	if err != nil {
		t.Fatal(err)
	}
	st := s.State()
	ts := Endpoint{Addr: netip.MustParseAddrPort("100.64.0.1:8443"), TLS: true, FreeBind: true}
	if eps := endpointsOf(st); len(eps) != 3 || eps[0] != ts {
		t.Fatalf("listeners = %v, want the saved Tailscale address first", eps)
	}
	if !st.TailscaleBound || st.Message != "Serving on Tailscale: https://100.64.0.1:8443." {
		t.Fatalf("state = %v, %q", st.TailscaleBound, st.Message)
	}
	if got := get(t, rootClient(t, f.auth), "https://"+st.Listeners[0].Addr.String()+"/"); got != "tls=true" {
		t.Fatalf("Tailscale listener answered %q", got)
	}
	c, err := s.leaf.GetCertificate(&tls.ClientHelloInfo{})
	if err != nil || len(c.Leaf.IPAddresses) != 1 || c.Leaf.IPAddresses[0].String() != "100.64.0.1" {
		t.Fatalf("leaf = %v, %v; want the IP SAN 100.64.0.1", c, err)
	}
}

func TestServiceFollowsAddressChanges(t *testing.T) {
	f := newServiceFixture(t)
	f.ifs.set(true, "100.64.0.1")
	s, err := f.start(t)
	if err != nil {
		t.Fatal(err)
	}
	if savedAddrs(t, f.db) != `["100.64.0.1"]` || !s.State().TailscaleBound {
		t.Fatal("the first read was not saved and bound")
	}

	f.ifs.set(true, "fd7a:115c:a1e0::2", "100.64.0.2")
	f.clock.Advance(CheckInterval)
	st := s.State()
	if savedAddrs(t, f.db) != `["100.64.0.2","fd7a:115c:a1e0::2"]` {
		t.Fatalf("saved = %s", savedAddrs(t, f.db))
	}
	if st.Message != "Serving on Tailscale: https://100.64.0.2:8443, https://[fd7a:115c:a1e0::2]:8443." {
		t.Fatalf("message = %q", st.Message)
	}
	const changed = "listen: Serving on Tailscale: https://100.64.0.2:8443, https://[fd7a:115c:a1e0::2]:8443."
	if n := strings.Count(f.log.String(), changed); n != 1 {
		t.Fatalf("the change was logged %d times, want once:\n%s", n, f.log.String())
	}
	c, err := s.leaf.GetCertificate(&tls.ClientHelloInfo{})
	if err != nil || len(c.Leaf.IPAddresses) != 2 || c.Leaf.IPAddresses[0].String() != "100.64.0.2" {
		t.Fatalf("leaf after the change = %v, %v", c, err)
	}

	f.ifs.set(false)
	f.clock.Advance(CheckInterval)
	if !s.State().TailscaleBound || savedAddrs(t, f.db) != `["100.64.0.2","fd7a:115c:a1e0::2"]` {
		t.Fatal("an absent interface dropped the last addresses")
	}
	if n := strings.Count(f.log.String(), changed); n != 1 {
		t.Fatalf("an unchanged state was logged again (%d times)", n)
	}

	f.ifs.set(true)
	f.clock.Advance(CheckInterval)
	st = s.State()
	if st.TailscaleBound || len(st.Listeners) != 2 || savedAddrs(t, f.db) != `[]` {
		t.Fatalf("after the addresses went away: state %v, saved %s", st, savedAddrs(t, f.db))
	}
	if st.Message != "No Tailscale address found; serving loopback only." {
		t.Fatalf("message = %q", st.Message)
	}
}

func TestServiceRenewsTheLeaf(t *testing.T) {
	f := newServiceFixture(t)
	s, err := f.start(t, "https://192.168.1.5:9443")
	if err != nil {
		t.Fatal(err)
	}
	st := s.State()
	if st.TailscaleBound || st.Message != "Tailscale listener disabled by --listen." {
		t.Fatalf("state = %v, %q", st.TailscaleBound, st.Message)
	}
	first, err := s.leaf.GetCertificate(&tls.ClientHelloInfo{})
	if err != nil || len(first.Leaf.IPAddresses) != 1 || first.Leaf.IPAddresses[0].String() != "192.168.1.5" {
		t.Fatalf("leaf = %v, %v; want the IP SAN 192.168.1.5", first, err)
	}
	// Jump close to 60 days with the check disarmed: ticking every 10 seconds
	// through the fake clock would take minutes. check re-arms itself.
	s.mu.Lock()
	s.timer.Stop()
	s.mu.Unlock()
	f.clock.Advance(LeafRenewAfter - CheckInterval)
	s.check()
	if c, _ := s.leaf.GetCertificate(&tls.ClientHelloInfo{}); c != first {
		t.Fatal("the leaf was renewed before 60 days")
	}
	f.clock.Advance(CheckInterval)
	if c, _ := s.leaf.GetCertificate(&tls.ClientHelloInfo{}); c == first {
		t.Fatal("the leaf was not renewed after 60 days")
	}
	if f.ifs.count() != 0 {
		t.Fatal("the interfaces were read with the tailscale listener disabled")
	}
}

func TestServiceStartFailsOnABusyAddress(t *testing.T) {
	f := newServiceFixture(t)
	f.binder.fail = map[netip.AddrPort]error{netip.MustParseAddrPort("127.0.0.1:8080"): syscall.EADDRINUSE}
	_, err := f.start(t)
	if err == nil || !strings.Contains(err.Error(), "listen: bind http://127.0.0.1:8080") {
		t.Fatalf("Start = %v, want the bind failure", err)
	}
	if f.clock.Pending() != 0 {
		t.Fatal("a failed Start left the check timer armed")
	}
}

func TestServiceShutdownStopsTheChecks(t *testing.T) {
	f := newServiceFixture(t)
	s, err := f.start(t)
	if err != nil {
		t.Fatal(err)
	}
	reads := f.ifs.count()
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(time.Minute)
	if f.ifs.count() != reads || f.clock.Pending() != 0 || len(s.State().Listeners) != 0 {
		t.Fatal("checks or listeners outlived Shutdown")
	}
	if msg := s.State().Message; msg != "The listeners are shut down." {
		t.Fatalf("message after Shutdown = %q", msg)
	}
	const shut = "listen: The listeners are shut down."
	if n := strings.Count(f.log.String(), shut); n != 1 {
		t.Fatalf("the shutdown was logged %d times, want once:\n%s", n, f.log.String())
	}
	_ = s.Shutdown(context.Background())
	if n := strings.Count(f.log.String(), shut); n != 1 {
		t.Fatalf("a second Shutdown logged again (%d times)", n)
	}
}

func TestServiceRetriesAFailedSave(t *testing.T) {
	f := newServiceFixture(t)
	f.ifs.set(true, "100.64.0.1")
	s, err := f.start(t)
	if err != nil {
		t.Fatal(err)
	}
	if got := savedAddrs(t, f.db); got != `["100.64.0.1"]` {
		t.Fatalf("saved = %s", got)
	}
	ctx := context.Background()
	for _, q := range []string{
		`CREATE TRIGGER dp_refuse_ins BEFORE INSERT ON core_meta BEGIN SELECT RAISE(ABORT, 'refused'); END`,
		`CREATE TRIGGER dp_refuse_upd BEFORE UPDATE ON core_meta BEGIN SELECT RAISE(ABORT, 'refused'); END`,
	} {
		if _, err := f.db.Writer().ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}

	f.ifs.set(true, "100.64.0.2")
	f.clock.Advance(CheckInterval)
	if got := savedAddrs(t, f.db); got != `["100.64.0.1"]` {
		t.Fatalf("saved = %s despite the refusing triggers", got)
	}
	if !strings.Contains(f.log.String(), "listen: cannot save the Tailscale addresses") {
		t.Fatalf("the failed save was not logged:\n%s", f.log.String())
	}
	if msg := s.State().Message; msg != "Serving on Tailscale: https://100.64.0.2:8443." {
		t.Fatalf("message = %q", msg)
	}

	for _, q := range []string{`DROP TRIGGER dp_refuse_ins`, `DROP TRIGGER dp_refuse_upd`} {
		if _, err := f.db.Writer().ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	f.clock.Advance(CheckInterval)
	if got := savedAddrs(t, f.db); got != `["100.64.0.2"]` {
		t.Fatalf("saved = %s; the failed save was not retried", got)
	}
}

func TestServiceTailscaleOnly(t *testing.T) {
	f := newServiceFixture(t)
	s, err := f.start(t, "tailscale")
	if err != nil {
		t.Fatal(err)
	}
	st := s.State()
	if len(st.Listeners) != 0 || st.TailscaleBound || st.Message != "No Tailscale address found; not serving on the tailnet." {
		t.Fatalf("state = %v, %v, %q", st.Listeners, st.TailscaleBound, st.Message)
	}

	f.ifs.set(true, "100.64.0.1")
	f.binder.mu.Lock()
	f.binder.fail = map[netip.AddrPort]error{netip.MustParseAddrPort("100.64.0.1:8443"): syscall.EADDRINUSE}
	f.binder.mu.Unlock()
	f.clock.Advance(CheckInterval)
	st = s.State()
	if st.TailscaleBound || st.Message != "Tailscale addresses found but not bound; see the serve log." {
		t.Fatalf("state = %v, %q", st.TailscaleBound, st.Message)
	}
	if !strings.Contains(f.log.String(), "listen: Tailscale addresses found but not bound; see the serve log.") {
		t.Fatalf("the refused bind was not logged:\n%s", f.log.String())
	}
}

func TestServiceShutdownDuringChecks(t *testing.T) {
	f := newServiceFixture(t)
	f.ifs.set(true, "100.64.0.1")
	s, err := f.start(t)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range 200 {
			f.ifs.set(true, netip.AddrFrom4([4]byte{100, 64, 0, byte(i % 4)}).String())
			f.clock.Advance(CheckInterval)
		}
	}()
	_ = s.State()
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the check loop did not finish after Shutdown")
	}
	if f.clock.Pending() != 0 || len(s.State().Listeners) != 0 {
		t.Fatal("a check re-armed or rebound after Shutdown")
	}
}

func TestServiceSharesOneTLSConfig(t *testing.T) {
	f := newServiceFixture(t)
	specs, err := ParseSpecs([]string{"tailscale"})
	if err != nil {
		t.Fatal(err)
	}
	f.ifs.set(true, "100.64.0.1")
	s, err := Start(context.Background(), Config{
		Specs: specs, Hostname: "panel", DB: f.db, Authority: f.auth, Clock: f.clock,
		Handler: schemeHandler(), Log: slog.New(slog.DiscardHandler), Interfaces: f.ifs.list, Bind: f.binder.bind,
		Sources: &SourceDeps{LocalHostID: "H1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Shutdown(context.Background()) })
	issued := s.leaf.cur.Load()
	cfg, err := s.TLSConfig()
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.TLSConfig()
	if err != nil || cfg == nil || cfg != again || cfg != s.set.cfg.TLS {
		t.Fatal("TLSConfig is not the one configuration the HTTPS listeners clone")
	}
	if s.leaf.cur.Load() != issued {
		t.Error("TLSConfig replaced the leaf the HTTPS listeners issued")
	}
	if cfg.MinVersion != tls.VersionTLS12 {
		t.Errorf("MinVersion = %x, want TLS 1.2", cfg.MinVersion)
	}
	if got, err := cfg.GetCertificate(&tls.ClientHelloInfo{}); err != nil || got != issued.cert {
		t.Fatalf("GetCertificate = %v, %v; want the CA leaf", got, err)
	}
}

// With plain listeners only, nothing issues a leaf until TLSConfig does: a
// leaf without IP SANs, which the next Ensure with addresses replaces.
func TestServiceTLSConfigIssuesALeafForPlainListeners(t *testing.T) {
	f := newServiceFixture(t)
	s, err := f.start(t)
	if err != nil {
		t.Fatal(err)
	}
	if s.leaf.cur.Load() != nil {
		t.Fatal("plain listeners issued a leaf")
	}
	cfg, err := s.TLSConfig()
	if err != nil {
		t.Fatal(err)
	}
	first, err := cfg.GetCertificate(&tls.ClientHelloInfo{ServerName: "panel"})
	if err != nil {
		t.Fatalf("GetCertificate after TLSConfig: %v", err)
	}
	leaf, err := x509.ParseCertificate(first.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(leaf.DNSNames, []string{"panel", "localhost"}) || len(leaf.IPAddresses) != 0 {
		t.Fatalf("leaf SANs %v %v, want the hostname and localhost without IP addresses", leaf.DNSNames, leaf.IPAddresses)
	}
	if _, err := s.TLSConfig(); err != nil || s.leaf.cur.Load().cert != first {
		t.Fatalf("a second TLSConfig = %v or replaced the leaf", err)
	}
	if issued, err := s.leaf.Ensure(context.Background(), addrs("100.64.0.1")); err != nil || !issued {
		t.Fatalf("Ensure with an address = %v, %v; want a new leaf with its SAN", issued, err)
	}
	if got, err := cfg.GetCertificate(&tls.ClientHelloInfo{}); err != nil || got == first {
		t.Fatalf("GetCertificate = %v, %v; want the reissued leaf through the same configuration", got, err)
	}
}

// Listeners that clone the shared configuration resume each other's TLS 1.3
// sessions once its ticket keys are set; a fresh configuration per connection
// never does.
func TestServiceTLSConfigResumesSessionsAcrossClones(t *testing.T) {
	f := newServiceFixture(t)
	s, err := f.start(t)
	if err != nil {
		t.Fatal(err)
	}
	shared, err := s.TLSConfig()
	if err != nil {
		t.Fatal(err)
	}
	shared.SetSessionTicketKeys([][32]byte{{1}})
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(f.auth.RootPEM()) {
		t.Fatal("RootPEM did not parse")
	}
	handshakes := func(server func() *tls.Config) (resumed []bool) {
		cache := tls.NewLRUClientSessionCache(2)
		for range 2 {
			cc, sc := net.Pipe()
			done := make(chan error, 1)
			go func() {
				defer func() { _ = sc.Close() }()
				conn := tls.Server(sc, server())
				if err := conn.Handshake(); err != nil {
					done <- err
					return
				}
				_, err := conn.Write([]byte{0})
				done <- err
			}()
			cli := tls.Client(cc, &tls.Config{
				MinVersion: tls.VersionTLS13, RootCAs: pool, ServerName: "localhost", ClientSessionCache: cache,
			})
			// The client stores the session ticket when it reads the byte
			// the server writes after the handshake.
			if err := cli.Handshake(); err != nil {
				t.Fatal(err)
			}
			if _, err := cli.Read(make([]byte, 1)); err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			resumed = append(resumed, cli.ConnectionState().DidResume)
			_ = cc.Close()
		}
		return resumed
	}
	if got := handshakes(shared.Clone); !reflect.DeepEqual(got, []bool{false, true}) {
		t.Errorf("handshakes through clones of the shared configuration resumed %v, want [false true]", got)
	}
	if got := handshakes(s.leaf.TLSConfig); !reflect.DeepEqual(got, []bool{false, false}) {
		t.Errorf("handshakes through a fresh configuration each resumed %v, want [false false]", got)
	}
}

func TestServiceRenewsTheLeafTLSConfigIssued(t *testing.T) {
	f := newServiceFixture(t)
	s, err := f.start(t)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := s.TLSConfig()
	if err != nil {
		t.Fatal(err)
	}
	first, err := cfg.GetCertificate(&tls.ClientHelloInfo{})
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.timer.Stop()
	s.mu.Unlock()
	f.clock.Advance(LeafRenewAfter - CheckInterval)
	s.check()
	if c, _ := cfg.GetCertificate(&tls.ClientHelloInfo{}); c != first {
		t.Fatal("the leaf was renewed before 60 days")
	}
	f.clock.Advance(CheckInterval)
	c, err := cfg.GetCertificate(&tls.ClientHelloInfo{})
	if err != nil || c == first {
		t.Fatalf("GetCertificate = %v, %v; want a renewed leaf after 60 days", c, err)
	}
	leaf, err := x509.ParseCertificate(c.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(leaf.DNSNames, []string{"panel", "localhost"}) || len(leaf.IPAddresses) != 0 {
		t.Fatalf("renewed leaf SANs %v %v, want the hostname and localhost without IP addresses", leaf.DNSNames, leaf.IPAddresses)
	}
}
