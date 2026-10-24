package tslocal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
)

// DERPMap is the node's current DERP map; a node without a network map has
// none (Regions is nil).
type DERPMap struct {
	Regions            map[int]*DERPRegion
	OmitDefaultRegions bool `json:"omitDefaultRegions,omitempty"`
}

// DERPRegion is one region of a DERP map.
type DERPRegion struct {
	RegionID   int
	RegionCode string
	RegionName string
	Avoid      bool `json:",omitempty"`
	Nodes      []*DERPNode
}

// DERPNode is one server of a DERP region. CertName, when set, is the name
// clients verify, or "sha256-raw:<hex>" for a pinned certificate.
type DERPNode struct {
	Name     string
	RegionID int
	HostName string
	CertName string `json:",omitempty"`
	IPv4     string `json:",omitempty"`
	IPv6     string `json:",omitempty"`
	STUNPort int    `json:",omitempty"` // 0 means 3478; -1 disables STUN
	STUNOnly bool   `json:",omitempty"`
	DERPPort int    `json:",omitempty"` // 0 means 443
}

// DERPMap reads the node's current DERP map.
func (c *Client) DERPMap(ctx context.Context) (*DERPMap, error) {
	var m DERPMap
	if err := c.getJSON(ctx, http.MethodGet, "/localapi/v0/derpmap", &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// certDomainPattern is a DNS name; the domain travels in the URL path.
var certDomainPattern = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*$`)

// CertPair returns the certificate chain and private key tailscaled holds
// (or obtains) for domain, one of the node's CertDomains.
func (c *Client) CertPair(ctx context.Context, domain string) (certPEM, keyPEM []byte, err error) {
	if len(domain) > 253 || !certDomainPattern.MatchString(domain) {
		return nil, nil, fmt.Errorf("tslocal: invalid certificate domain %q", domain)
	}
	b, err := c.send(ctx, http.MethodGet, "/localapi/v0/cert/"+domain+"?type=pair", nil, http.StatusOK)
	if err != nil {
		return nil, nil, err
	}
	// The pair answer is the key's PEM block followed by the certificate
	// blocks.
	i := bytes.Index(b, []byte("--\n--"))
	if i < 0 {
		return nil, nil, errors.New("tslocal: certificate pair: no delimiter between key and certificate")
	}
	i += len("--\n")
	// The key slice's capacity is capped so that appending to it cannot
	// overwrite the start of the certificate.
	keyPEM, certPEM = b[:i:i], b[i:]
	if !bytes.Contains(keyPEM, []byte(" PRIVATE KEY-----")) || bytes.Contains(certPEM, []byte(" PRIVATE KEY-----")) {
		return nil, nil, errors.New("tslocal: certificate pair: the first block is not the only private key")
	}
	return certPEM, keyPEM, nil
}

// WhoIs is the node and user holding a tailnet address.
type WhoIs struct {
	Node        *WhoIsNode
	UserProfile *UserProfile
}

// WhoIsNode is a node as WhoIs reports it.
type WhoIsNode struct {
	ID        int64
	StableID  string
	Name      string // MagicDNS name with a trailing dot
	Addresses []netip.Prefix
	Tags      []string
}

// UserProfile is a tailnet user.
type UserProfile struct {
	ID          int64
	LoginName   string
	DisplayName string
}

// WhoIs looks up the node holding addr; ErrPeerNotFound when none does.
func (c *Client) WhoIs(ctx context.Context, addr netip.Addr) (*WhoIs, error) {
	var w WhoIs
	err := c.getJSON(ctx, http.MethodGet, "/localapi/v0/whois?addr="+url.QueryEscape(addr.String()), &w)
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
		return nil, ErrPeerNotFound
	}
	if err != nil {
		return nil, err
	}
	return &w, nil
}

// DERPRegionReport is tailscaled's connectivity diagnosis of one DERP
// region. It uses a throwaway node key, so it shows reachability, not an
// authenticated relay between two nodes.
type DERPRegionReport struct {
	Info     []string
	Warnings []string
	Errors   []string
}

// DebugDERPRegion diagnoses the DERP region with the given ID or code.
func (c *Client) DebugDERPRegion(ctx context.Context, region string) (*DERPRegionReport, error) {
	var r DERPRegionReport
	q := url.Values{"region": {region}}
	b, err := c.send(ctx, http.MethodPost, "/localapi/v0/debug-derp-region?"+q.Encode(), nil, http.StatusOK)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("tslocal: decode DERP region report: %w", err)
	}
	return &r, nil
}
