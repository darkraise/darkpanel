package tailscale

import (
	"context"
	"fmt"
	"net/netip"
	"slices"
	"sync"

	"github.com/darkraise/darkpanel/internal/platform/netstat"
	"github.com/darkraise/darkpanel/internal/platform/tslocal"
)

var (
	_ localAPI = thLocal{}
	_ linkHost = thLink{}
)

// tsLocal is the fake local API. Its Status and Prefs fields share a name with localAPI methods, so thLocal
// is the value that satisfies localAPI. A call answers Errs[<method name>] when that holds an error;
// otherwise it answers from the fields. A test that changes a field while an action runs does it in update.
type tsLocal struct {
	Status  *tslocal.Status
	Prefs   *tslocal.Prefs
	DERP    *tslocal.DERPMap
	Cert    [2][]byte // the certificate chain, then the private key
	Errs    map[string]error
	Edits   []tslocal.PrefsPatch
	Starts  []tslocal.StartOptions
	Logouts int
	// CertCalls lists the domains CertPair was asked for.
	CertCalls []string
	// LoginInteractives counts the LoginInteractive calls.
	LoginInteractives int

	mu sync.Mutex
}

// thLocal adapts a tsLocal to localAPI.
type thLocal struct{ *tsLocal }

// update runs f while holding the fake's lock.
func (l *tsLocal) update(f func()) {
	l.mu.Lock()
	defer l.mu.Unlock()
	f()
}

func (l *tsLocal) failure(method string) error { return l.Errs[method] }

func (a thLocal) Status(context.Context) (*tslocal.Status, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.failure("Status"); err != nil {
		return nil, err
	}
	if a.tsLocal.Status == nil {
		return nil, fmt.Errorf("%w: the fake has no status", tslocal.ErrUnavailable)
	}
	s := *a.tsLocal.Status
	return &s, nil
}

func (a thLocal) Prefs(context.Context) (*tslocal.Prefs, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.failure("Prefs"); err != nil {
		return nil, err
	}
	if a.tsLocal.Prefs == nil {
		return nil, fmt.Errorf("%w: the fake has no preferences", tslocal.ErrUnavailable)
	}
	p := *a.tsLocal.Prefs
	return &p, nil
}

// EditPrefs records the patch, applies each field it sets to Prefs and answers a copy of Prefs.
func (l *tsLocal) EditPrefs(_ context.Context, patch tslocal.PrefsPatch) (*tslocal.Prefs, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.Edits = append(l.Edits, patch)
	if err := l.failure("EditPrefs"); err != nil {
		return nil, err
	}
	if l.Prefs == nil {
		return nil, fmt.Errorf("%w: the fake has no preferences", tslocal.ErrUnavailable)
	}
	if patch.WantRunning != nil {
		l.Prefs.WantRunning = *patch.WantRunning
	}
	if patch.RouteAll != nil {
		l.Prefs.RouteAll = *patch.RouteAll
	}
	if patch.ShieldsUp != nil {
		l.Prefs.ShieldsUp = *patch.ShieldsUp
	}
	if patch.RunSSH != nil {
		l.Prefs.RunSSH = *patch.RunSSH
	}
	if patch.ExitNodeID != nil {
		l.Prefs.ExitNodeID = *patch.ExitNodeID
	}
	if patch.ExitNodeIP != nil {
		l.Prefs.ExitNodeIP = *patch.ExitNodeIP
	}
	if patch.AdvertiseRoutes != nil {
		l.Prefs.AdvertiseRoutes = slices.Clone(*patch.AdvertiseRoutes)
	}
	p := *l.Prefs
	return &p, nil
}

// Start records the options and changes nothing else.
func (l *tsLocal) Start(_ context.Context, o tslocal.StartOptions) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.Starts = append(l.Starts, o)
	return l.failure("Start")
}

// Logout counts the call and changes nothing else.
func (l *tsLocal) Logout(context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.Logouts++
	return l.failure("Logout")
}

// LoginInteractive counts the call and changes nothing else.
func (l *tsLocal) LoginInteractive(context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.LoginInteractives++
	return l.failure("LoginInteractive")
}

// DERPMap answers an empty map when DERP is nil.
func (l *tsLocal) DERPMap(context.Context) (*tslocal.DERPMap, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.failure("DERPMap"); err != nil {
		return nil, err
	}
	if l.DERP == nil {
		return &tslocal.DERPMap{}, nil
	}
	m := *l.DERP
	return &m, nil
}

// CertPair records the domain and answers Cert.
func (l *tsLocal) CertPair(_ context.Context, domain string) (certPEM, keyPEM []byte, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.CertCalls = append(l.CertCalls, domain)
	if err := l.failure("CertPair"); err != nil {
		return nil, nil, err
	}
	return slices.Clone(l.Cert[0]), slices.Clone(l.Cert[1]), nil
}

// tsLink is the fake socket table, interface list and routing table. Its Sockets field shares a name with
// a linkHost method, so thLink is the value that satisfies linkHost.
type tsLink struct {
	Sockets []netstat.Socket
	Ifaces  []netstat.Interface
	Routes  map[netip.Addr]netstat.Route // RouteGet answers by destination; a missing entry is netstat.ErrNoRoute
	Pid     int
}

// thLink adapts a tsLink to linkHost.
type thLink struct{ *tsLink }

func (a thLink) Sockets() ([]netstat.Socket, error) { return slices.Clone(a.tsLink.Sockets), nil }

func (l *tsLink) Interfaces() ([]netstat.Interface, error) { return slices.Clone(l.Ifaces), nil }

func (l *tsLink) RouteGet(q netstat.RouteQuery) (netstat.Route, error) {
	if r, ok := l.Routes[q.Dst]; ok {
		return r, nil
	}
	return netstat.Route{}, netstat.ErrNoRoute
}

func (l *tsLink) PID() int { return l.Pid }
