package cloudflare

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/cloud"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/cloudflare/ingress"
)

// cf carries the module's provider dependencies; tests replace them.
type cf struct{ d deps }

// deps say where and how the module reaches Cloudflare.
type deps struct {
	baseURL string                                           // "https://api.cloudflare.com/client/v4"
	http    *http.Client                                     // nil: cloud's default client
	sleep   func(ctx context.Context, d time.Duration) error // nil: cloud's default
}

func productionCF() cf {
	return cf{d: deps{baseURL: "https://api.cloudflare.com/client/v4"}}
}

// The page sizes the list calls ask for. Spec 7.1 records zones as taking 5
// to 50 per page, tunnels up to 1000 and DNS records far more.
const (
	perPage      = 100
	zonesPerPage = 50
)

// api is the only code that talks to Cloudflare: one account's calls,
// answering provider values and carrying no business rules.
type api struct {
	c       *cloud.Client
	account string
}

// newAPI builds the api of one account. The token is held only by the
// client's Auth callback.
func (c cf) newAPI(accountID string, token []byte, clock core.Clock) (*api, error) {
	if accountID == "" {
		return nil, errors.New("cloudflare: an account id is required")
	}
	if len(token) == 0 {
		return nil, errors.New("cloudflare: an API token is required")
	}
	bearer := "Bearer " + string(token)
	client, err := cloud.New(cloud.Config{
		Provider: "cloudflare",
		BaseURL:  c.d.baseURL,
		Auth: func(r *http.Request) error {
			r.Header.Set("Authorization", bearer)
			return nil
		},
		HTTP:      c.d.http,
		Clock:     clock,
		Sleep:     c.d.sleep,
		ErrorCode: errorCode,
	})
	if err != nil {
		return nil, fmt.Errorf("cloudflare: %w", err)
	}
	return &api{c: client, account: accountID}, nil
}

// errorCode is the decimal text of errors[0].code in a Cloudflare answer, or
// "" when it has none.
func errorCode(body []byte) string {
	var e struct {
		Errors []struct {
			Code *int64 `json:"code"`
		} `json:"errors"`
	}
	if json.Unmarshal(body, &e) != nil || len(e.Errors) == 0 || e.Errors[0].Code == nil {
		return ""
	}
	return strconv.FormatInt(*e.Errors[0].Code, 10)
}

// Tunnel is one Cloudflare tunnel.
type Tunnel struct {
	ID          string
	Name        string
	Status      string // inactive | degraded | healthy | down
	CreatedAt   time.Time
	Connections []Connection
}

// Connection is one connection a tunnel's connector holds to Cloudflare.
type Connection struct {
	ID, ColoName, OriginIP, ClientVersion string
	OpenedAt                              time.Time
}

// Zone is one DNS zone of the account.
type Zone struct{ ID, Name string }

// DNSRecord is one DNS record of a zone.
type DNSRecord struct {
	ID, Type, Name, Content, Comment string
	Proxied                          bool
}

type tunnelJSON struct {
	ID          string           `json:"id"`
	Name        string           `json:"name"`
	Status      string           `json:"status"`
	CreatedAt   time.Time        `json:"created_at"`
	Connections []connectionJSON `json:"connections"`
}

type connectionJSON struct {
	ID            string    `json:"id"`
	ColoName      string    `json:"colo_name"`
	OriginIP      string    `json:"origin_ip"`
	OpenedAt      time.Time `json:"opened_at"`
	ClientVersion string    `json:"client_version"`
}

type recordJSON struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	Proxied bool   `json:"proxied"`
	Comment string `json:"comment"`
}

func (t tunnelJSON) tunnel() Tunnel {
	out := Tunnel{ID: t.ID, Name: t.Name, Status: t.Status, CreatedAt: t.CreatedAt}
	for _, c := range t.Connections {
		out.Connections = append(out.Connections, c.connection(c.ClientVersion))
	}
	return out
}

func (c connectionJSON) connection(version string) Connection {
	return Connection{ID: c.ID, ColoName: c.ColoName, OriginIP: c.OriginIP, ClientVersion: version, OpenedAt: c.OpenedAt}
}

func (r recordJSON) record() DNSRecord {
	return DNSRecord{ID: r.ID, Type: r.Type, Name: r.Name, Content: r.Content, Comment: r.Comment, Proxied: r.Proxied}
}

// envelope is Cloudflare's answer around every result.
type envelope struct {
	Success    bool            `json:"success"`
	Result     json.RawMessage `json:"result"`
	ResultInfo *resultInfo     `json:"result_info"`
}

type resultInfo struct {
	Page       int `json:"page"`
	PerPage    int `json:"per_page"`
	TotalCount int `json:"total_count"`
	TotalPages int `json:"total_pages"`
}

// pages is the number of pages the list holds. Spec 7.1 documents
// total_pages for zones only, so it is otherwise derived from the counts.
func (i resultInfo) pages() int {
	if i.TotalPages > 0 || i.PerPage <= 0 {
		return i.TotalPages
	}
	return (i.TotalCount + i.PerPage - 1) / i.PerPage
}

// call sends one request and decodes the envelope's result into out (nil
// skips it). Failed answers keep the cloud client's error; a 2xx answer that
// is not a successful envelope is cloud.unavailable for a read or delete,
// which committed nothing or may be retried, and cloud.uncertain_outcome for a
// write, which may have committed.
func (a *api) call(ctx context.Context, method, path string, query url.Values, body []byte, out any) (*resultInfo, error) {
	req := cloud.Request{Method: method, Path: path, Query: query, Header: http.Header{"Accept": {"application/json"}}}
	if body != nil {
		req.Body = body
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := a.c.Do(ctx, req)
	if err != nil {
		return nil, err
	}
	var env envelope
	if err := json.Unmarshal(res.Body, &env); err != nil {
		return nil, answerError(method, err, "cloudflare answered JSON that could not be read")
	}
	if !env.Success {
		detail := "cloudflare reported a failure"
		if code := errorCode(res.Body); code != "" {
			detail += " (" + code + ")"
		}
		return nil, answerError(method, nil, detail)
	}
	if out != nil {
		if err := json.Unmarshal(env.Result, out); err != nil {
			return nil, answerError(method, err, "cloudflare answered a result that could not be read")
		}
	}
	return env.ResultInfo, nil
}

func answerError(method string, err error, detail string) error {
	if method == http.MethodGet || method == http.MethodDelete {
		return errcode.Wrap(errcode.CloudUnavailable, err, detail)
	}
	return errcode.Wrap(errcode.CloudUncertainOutcome, err, detail+"; the request may have committed")
}

// list reads every page of a list call, size items per page, appending each
// page's results to *out.
func list[T any](ctx context.Context, a *api, path string, query url.Values, size int, out *[]T) error {
	q := url.Values{}
	for k, v := range query {
		q[k] = v
	}
	q.Set("per_page", strconv.Itoa(size))
	for page := 1; ; page++ {
		q.Set("page", strconv.Itoa(page))
		var items []T
		info, err := a.call(ctx, http.MethodGet, path, q, nil, &items)
		if err != nil {
			return err
		}
		*out = append(*out, items...)
		// The request's own page number bounds the loop even when an answer
		// does not advance its page.
		if info == nil || info.Page >= info.pages() || page >= info.pages() {
			return nil
		}
	}
}

// notFound turns the 404 of a call that names one object into not_found,
// keeping the cloud client's error in the chain.
func notFound(err error, kind, id string) error {
	if cloud.Status(err) != http.StatusNotFound {
		return err
	}
	return errcode.Wrap(errcode.NotFound, err, fmt.Sprintf("cloudflare %s %s was not found", kind, id))
}

func (a *api) accountPath(rest string) string {
	return "/accounts/" + url.PathEscape(a.account) + rest
}

func (a *api) tunnelPath(id, rest string) string {
	return a.accountPath("/cfd_tunnel/" + url.PathEscape(id) + rest)
}

func recordsPath(zoneID string) string {
	return "/zones/" + url.PathEscape(zoneID) + "/dns_records"
}

func recordPath(zoneID, id string) string {
	return recordsPath(zoneID) + "/" + url.PathEscape(id)
}

// VerifyToken checks that Cloudflare accepts the token. The account endpoint
// may not accept a user-owned token, so a 401 or 403 there is asked again of
// the user endpoint.
func (a *api) VerifyToken(ctx context.Context) error {
	_, err := a.call(ctx, http.MethodGet, a.accountPath("/tokens/verify"), nil, nil, nil)
	if err == nil {
		return nil
	}
	if s := cloud.Status(err); s != http.StatusUnauthorized && s != http.StatusForbidden {
		return err
	}
	if _, uerr := a.call(ctx, http.MethodGet, "/user/tokens/verify", nil, nil, nil); uerr == nil {
		return nil
	}
	return err
}

// Tunnels lists the account's live tunnels, only those named name unless it
// is "".
func (a *api) Tunnels(ctx context.Context, name string) ([]Tunnel, error) {
	q := url.Values{"is_deleted": {"false"}}
	if name != "" {
		q.Set("name", name)
	}
	var raw []tunnelJSON
	if err := list(ctx, a, a.accountPath("/cfd_tunnel"), q, perPage, &raw); err != nil {
		return nil, err
	}
	out := make([]Tunnel, 0, len(raw))
	for _, t := range raw {
		out = append(out, t.tunnel())
	}
	return out, nil
}

// Tunnel reads one tunnel.
func (a *api) Tunnel(ctx context.Context, id string) (Tunnel, error) {
	var t tunnelJSON
	if _, err := a.call(ctx, http.MethodGet, a.tunnelPath(id, ""), nil, nil, &t); err != nil {
		return Tunnel{}, notFound(err, "tunnel", id)
	}
	return t.tunnel(), nil
}

// CreateTunnel creates a remotely managed tunnel named name.
func (a *api) CreateTunnel(ctx context.Context, name string) (Tunnel, error) {
	body, err := json.Marshal(struct {
		Name      string `json:"name"`
		ConfigSrc string `json:"config_src"`
	}{name, "cloudflare"})
	if err != nil {
		return Tunnel{}, core.Definite(fmt.Errorf("cloudflare: encode tunnel: %w", err))
	}
	var t tunnelJSON
	if _, err := a.call(ctx, http.MethodPost, a.accountPath("/cfd_tunnel"), nil, body, &t); err != nil {
		return Tunnel{}, err
	}
	return t.tunnel(), nil
}

// DeleteTunnel deletes one tunnel.
func (a *api) DeleteTunnel(ctx context.Context, id string) error {
	_, err := a.call(ctx, http.MethodDelete, a.tunnelPath(id, ""), nil, nil, nil)
	return notFound(err, "tunnel", id)
}

// TunnelToken reads the token a connector runs the tunnel with.
func (a *api) TunnelToken(ctx context.Context, id string) (string, error) {
	var token string
	if _, err := a.call(ctx, http.MethodGet, a.tunnelPath(id, "/token"), nil, nil, &token); err != nil {
		return "", notFound(err, "tunnel", id)
	}
	return token, nil
}

// Connections lists the connections of a tunnel's connectors. The endpoint
// is documented both as connectors that each hold their connections and as
// the connections themselves.
func (a *api) Connections(ctx context.Context, id string) ([]Connection, error) {
	var raw []struct {
		connectionJSON
		Version string          `json:"version"`
		Conns   json.RawMessage `json:"conns"`
	}
	if _, err := a.call(ctx, http.MethodGet, a.tunnelPath(id, "/connections"), nil, nil, &raw); err != nil {
		return nil, notFound(err, "tunnel", id)
	}
	var out []Connection
	for _, e := range raw {
		if e.Conns == nil {
			out = append(out, e.connection(e.ClientVersion))
			continue
		}
		var conns []connectionJSON
		if err := json.Unmarshal(e.Conns, &conns); err != nil {
			return nil, answerError(http.MethodGet, err, "cloudflare answered a result that could not be read")
		}
		for _, c := range conns {
			out = append(out, c.connection(e.Version))
		}
	}
	return out, nil
}

// Configuration reads a tunnel's configuration; a tunnel never configured
// has an empty one.
func (a *api) Configuration(ctx context.Context, id string) (ingress.Config, error) {
	var res struct {
		Config ingress.Config `json:"config"`
	}
	if _, err := a.call(ctx, http.MethodGet, a.tunnelPath(id, "/configurations"), nil, nil, &res); err != nil {
		return ingress.Config{}, notFound(err, "tunnel", id)
	}
	return res.Config, nil
}

// PutConfiguration replaces a tunnel's configuration with c.
func (a *api) PutConfiguration(ctx context.Context, id string, c ingress.Config) error {
	// c's own encoding is spliced in, not passed through json.Marshal, whose
	// HTML escaping would alter the members the ingress package keeps as read.
	config, err := c.MarshalJSON()
	if err != nil {
		return core.Definite(fmt.Errorf("cloudflare: encode configuration: %w", err))
	}
	body := append(append([]byte(`{"config":`), config...), '}')
	_, err = a.call(ctx, http.MethodPut, a.tunnelPath(id, "/configurations"), nil, body, nil)
	return notFound(err, "tunnel", id)
}

// Zones lists the zones the token can read.
func (a *api) Zones(ctx context.Context) ([]Zone, error) {
	var raw []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := list(ctx, a, "/zones", url.Values{"account.id": {a.account}}, zonesPerPage, &raw); err != nil {
		return nil, err
	}
	out := make([]Zone, 0, len(raw))
	for _, z := range raw {
		out = append(out, Zone{ID: z.ID, Name: z.Name})
	}
	return out, nil
}

// DNSRecords lists a zone's records, only those with the given name and the
// exact comment unless either is "".
func (a *api) DNSRecords(ctx context.Context, zoneID, name, comment string) ([]DNSRecord, error) {
	q := url.Values{}
	if name != "" {
		q.Set("name", name)
	}
	if comment != "" {
		q.Set("comment.exact", comment)
	}
	var raw []recordJSON
	if err := list(ctx, a, recordsPath(zoneID), q, perPage, &raw); err != nil {
		return nil, err
	}
	out := make([]DNSRecord, 0, len(raw))
	for _, r := range raw {
		out = append(out, r.record())
	}
	return out, nil
}

// DNSRecord reads one record.
func (a *api) DNSRecord(ctx context.Context, zoneID, id string) (DNSRecord, error) {
	var r recordJSON
	if _, err := a.call(ctx, http.MethodGet, recordPath(zoneID, id), nil, nil, &r); err != nil {
		return DNSRecord{}, notFound(err, "DNS record", id)
	}
	return r.record(), nil
}

// CreateDNSRecord creates r, with an automatic TTL, and answers the record
// Cloudflare stored, its id included.
func (a *api) CreateDNSRecord(ctx context.Context, zoneID string, r DNSRecord) (DNSRecord, error) {
	body, err := json.Marshal(struct {
		Type    string `json:"type"`
		Name    string `json:"name"`
		Content string `json:"content"`
		Proxied bool   `json:"proxied"`
		Comment string `json:"comment"`
		TTL     int    `json:"ttl"`
	}{r.Type, r.Name, r.Content, r.Proxied, r.Comment, 1})
	if err != nil {
		return DNSRecord{}, core.Definite(fmt.Errorf("cloudflare: encode DNS record: %w", err))
	}
	var created recordJSON
	if _, err := a.call(ctx, http.MethodPost, recordsPath(zoneID), nil, body, &created); err != nil {
		return DNSRecord{}, err
	}
	return created.record(), nil
}

// DeleteDNSRecord deletes one record.
func (a *api) DeleteDNSRecord(ctx context.Context, zoneID, id string) error {
	_, err := a.call(ctx, http.MethodDelete, recordPath(zoneID, id), nil, nil, nil)
	return notFound(err, "DNS record", id)
}

// zoneFor picks the zone whose name is the longest suffix of hostname,
// compared without case and ignoring one trailing dot of the hostname.
func zoneFor(zones []Zone, hostname string) (Zone, bool) {
	host := strings.ToLower(strings.TrimSuffix(hostname, "."))
	var best Zone
	bestLen := 0
	for _, z := range zones {
		name := strings.ToLower(z.Name)
		if name == "" || len(name) <= bestLen {
			continue
		}
		if host == name || strings.HasSuffix(host, "."+name) {
			best, bestLen = z, len(name)
		}
	}
	return best, bestLen > 0
}
