// Package tslocal is a hand-written client for tailscaled's local API over
// its Unix socket (spec 1.8). Wire types are plain structs carrying only the
// fields Darkpanel reads or writes; no Tailscale library is imported.
package tslocal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"
)

// DefaultSocket is tailscaled's local API socket on Linux.
const DefaultSocket = "/var/run/tailscale/tailscaled.sock"

// apiHost is the Host header tailscaled requires; it refuses any other
// host, and any request with a Referer or Origin header.
const apiHost = "local-tailscaled.sock"

// maxBody bounds a response read into memory.
const maxBody = 16 << 20

var (
	// ErrUnavailable wraps a failure to reach tailscaled's socket.
	ErrUnavailable = errors.New("tslocal: tailscaled unavailable")
	// ErrPeerNotFound is WhoIs's answer for an address no node holds.
	ErrPeerNotFound = errors.New("tslocal: no node holds that address")
)

// APIError is an unexpected status from tailscaled.
type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("tslocal: tailscaled answered %d: %s", e.StatusCode, e.Message)
}

// Client talks to one tailscaled. It is safe for concurrent use.
type Client struct {
	hc *http.Client
}

// New returns a client for the tailscaled listening on socket.
func New(socket string) *Client {
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			conn, err := d.DialContext(ctx, "unix", socket)
			if err != nil {
				return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
			}
			return conn, nil
		},
		DisableCompression: true,
		MaxIdleConns:       2,
		IdleConnTimeout:    30 * time.Second,
	}
	return &Client{hc: &http.Client{Transport: tr}}
}

// Close releases idle connections.
func (c *Client) Close() { c.hc.CloseIdleConnections() }

// send performs one call and returns the body when the status is want.
func (c *Client) send(ctx context.Context, method, path string, body any, want int) ([]byte, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("tslocal: encode %s %s: %w", method, path, err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://"+apiHost+path, rd)
	if err != nil {
		return nil, fmt.Errorf("tslocal: %s %s: %w", method, path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tslocal: %s %s: %w", method, path, err)
	}
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(res.Body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("tslocal: read %s %s: %w", method, path, err)
	}
	if res.StatusCode != want {
		return nil, &APIError{StatusCode: res.StatusCode, Message: errorMessage(b)}
	}
	return b, nil
}

// errorMessage extracts tailscaled's error text: JSON {"error": ...} from
// most handlers, plain text from the rest.
func errorMessage(b []byte) string {
	var j struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(b, &j) == nil && j.Error != "" {
		return j.Error
	}
	return strings.TrimSpace(string(b))
}

func (c *Client) getJSON(ctx context.Context, method, path string, out any) error {
	b, err := c.send(ctx, method, path, nil, http.StatusOK)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("tslocal: decode %s: %w", path, err)
	}
	return nil
}

// Status is GET /localapi/v0/status. BackendState is one of NoState,
// NeedsLogin, NeedsMachineAuth, Stopped, Starting and Running.
type Status struct {
	Version        string
	BackendState   string
	AuthURL        string
	TailscaleIPs   []netip.Addr
	Self           *PeerStatus
	Health         []string
	MagicDNSSuffix string
	CurrentTailnet *TailnetStatus
	CertDomains    []string
	Peer           map[string]*PeerStatus // keyed by node public key
	ExitNodeStatus *ExitNodeStatus
}

// PeerStatus is a node as status reports it. A peer is reached directly
// when CurAddr is set, through DERP region Relay otherwise, and through a
// peer relay when PeerRelay is set (tailscaled 1.86 and later).
type PeerStatus struct {
	ID             string
	HostName       string
	DNSName        string
	OS             string
	TailscaleIPs   []netip.Addr
	AllowedIPs     []netip.Prefix
	PrimaryRoutes  []netip.Prefix
	Tags           []string
	Addrs          []string
	CurAddr        string
	Relay          string
	PeerRelay      string
	RxBytes        int64
	TxBytes        int64
	LastHandshake  time.Time
	Online         bool
	Active         bool
	ExitNode       bool
	ExitNodeOption bool
	KeyExpiry      *time.Time
}

// TailnetStatus is the tailnet the node belongs to.
type TailnetStatus struct {
	Name            string
	MagicDNSSuffix  string
	MagicDNSEnabled bool
}

// ExitNodeStatus is the node's current exit node.
type ExitNodeStatus struct {
	ID           string
	Online       bool
	TailscaleIPs []netip.Prefix
}

// Status reads the node's status with its peers.
func (c *Client) Status(ctx context.Context) (*Status, error) {
	var s Status
	if err := c.getJSON(ctx, http.MethodGet, "/localapi/v0/status", &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// errExitNodeBoth is returned when preferences name an exit node by both ID
// and address; tailscaled documents that at most one of the two may be set.
var errExitNodeBoth = errors.New("tslocal: ExitNodeID and ExitNodeIP are both set; at most one may be")

// Prefs is the node's preferences. Prefs read from tailscaled keep every
// field tailscaled sent, modelled or not, and send them back unchanged, so
// Start with UpdatePrefs replaces the preferences without resetting fields
// this type does not model.
type Prefs struct {
	ControlURL      string
	RouteAll        bool // accept routes
	ExitNodeID      string
	ExitNodeIP      netip.Addr
	CorpDNS         bool
	RunSSH          bool
	WantRunning     bool
	LoggedOut       bool
	ShieldsUp       bool
	AdvertiseTags   []string
	Hostname        string
	AdvertiseRoutes []netip.Prefix // 0.0.0.0/0 and ::/0 advertise an exit node

	raw map[string]json.RawMessage
}

// prefsFields is Prefs without its methods, for encoding the modelled
// fields.
type prefsFields Prefs

// UnmarshalJSON decodes the modelled fields and keeps every field.
func (p *Prefs) UnmarshalJSON(b []byte) error {
	var f prefsFields
	if err := json.Unmarshal(b, &f); err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	*p = Prefs(f)
	p.raw = raw
	return nil
}

// MarshalJSON encodes every kept field with the modelled fields' current
// values over them.
func (p Prefs) MarshalJSON() ([]byte, error) {
	if p.ExitNodeID != "" && p.ExitNodeIP.IsValid() {
		return nil, errExitNodeBoth
	}
	b, err := json.Marshal(prefsFields(p))
	if err != nil {
		return nil, err
	}
	if len(p.raw) == 0 {
		return b, nil
	}
	var typed map[string]json.RawMessage
	if err := json.Unmarshal(b, &typed); err != nil {
		return nil, err
	}
	out := maps.Clone(p.raw)
	maps.Copy(out, typed)
	return json.Marshal(out)
}

// Prefs reads the node's preferences.
func (c *Client) Prefs(ctx context.Context) (*Prefs, error) {
	var p Prefs
	if err := c.getJSON(ctx, http.MethodGet, "/localapi/v0/prefs", &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// PrefsPatch is a masked preferences edit: only the non-nil fields change.
// It is sent as tailscaled's MaskedPrefs, each value with its <Field>Set
// flag.
type PrefsPatch struct {
	WantRunning     *bool
	RouteAll        *bool
	ShieldsUp       *bool
	RunSSH          *bool
	ExitNodeID      *string
	ExitNodeIP      *netip.Addr
	AdvertiseRoutes *[]netip.Prefix
}

// MarshalJSON encodes the patch as MaskedPrefs. Setting an exit node by ID
// while clearing the address (or the reverse) is allowed; naming both is not.
func (p PrefsPatch) MarshalJSON() ([]byte, error) {
	if p.ExitNodeID != nil && *p.ExitNodeID != "" && p.ExitNodeIP != nil && p.ExitNodeIP.IsValid() {
		return nil, errExitNodeBoth
	}
	m := map[string]any{}
	set := func(name string, v any) {
		m[name] = v
		m[name+"Set"] = true
	}
	if p.WantRunning != nil {
		set("WantRunning", *p.WantRunning)
	}
	if p.RouteAll != nil {
		set("RouteAll", *p.RouteAll)
	}
	if p.ShieldsUp != nil {
		set("ShieldsUp", *p.ShieldsUp)
	}
	if p.RunSSH != nil {
		set("RunSSH", *p.RunSSH)
	}
	if p.ExitNodeID != nil {
		set("ExitNodeID", *p.ExitNodeID)
	}
	if p.ExitNodeIP != nil {
		set("ExitNodeIP", *p.ExitNodeIP)
	}
	if p.AdvertiseRoutes != nil {
		routes := *p.AdvertiseRoutes
		if routes == nil {
			routes = []netip.Prefix{}
		}
		set("AdvertiseRoutes", routes)
	}
	return json.Marshal(m)
}

// EditPrefs applies a masked edit and returns the resulting preferences.
func (c *Client) EditPrefs(ctx context.Context, patch PrefsPatch) (*Prefs, error) {
	b, err := c.send(ctx, http.MethodPatch, "/localapi/v0/prefs", patch, http.StatusOK)
	if err != nil {
		return nil, err
	}
	var p Prefs
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("tslocal: decode prefs: %w", err)
	}
	return &p, nil
}

// StartOptions is POST /localapi/v0/start's ipn.Options: an auth key to log
// in with and, optionally, the complete preferences to start with.
type StartOptions struct {
	AuthKey     string `json:",omitempty"`
	UpdatePrefs *Prefs `json:",omitempty"`
}

// Start (re)starts the backend, logging in with opts.AuthKey when set.
// UpdatePrefs replaces every preference, so it must be the node's current
// Prefs, read with Client.Prefs, with the wanted fields changed; Prefs built
// in code are refused because they would reset every unmodelled preference.
func (c *Client) Start(ctx context.Context, opts StartOptions) error {
	if opts.UpdatePrefs != nil && len(opts.UpdatePrefs.raw) == 0 {
		return errors.New("tslocal: start: UpdatePrefs were not read from tailscaled and would reset unmodelled preferences")
	}
	_, err := c.send(ctx, http.MethodPost, "/localapi/v0/start", opts, http.StatusNoContent)
	return err
}

// Logout logs the node out of its tailnet.
func (c *Client) Logout(ctx context.Context) error {
	_, err := c.send(ctx, http.MethodPost, "/localapi/v0/logout", nil, http.StatusNoContent)
	return err
}
