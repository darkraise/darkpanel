package tailnet

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/cloud"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// tn carries the module's provider dependencies and the token cache its
// accounts share; tests replace them.
type tn struct {
	d      deps
	tokens *tokenCache
}

// deps say where and how the module reaches Tailscale.
type deps struct {
	baseURL string                                           // "https://api.tailscale.com"
	http    *http.Client                                     // nil: cloud's default client
	sleep   func(ctx context.Context, d time.Duration) error // nil: cloud's default
}

func productionTN() tn {
	return tn{d: deps{baseURL: "https://api.tailscale.com"}, tokens: newTokenCache()}
}

const (
	provider             = "tailscale"
	oauthPath            = "/api/v2/oauth/token"
	defaultTokenLifetime = time.Hour
	contentHuJSON        = "application/hujson"
	exitRouteV4          = "0.0.0.0/0"
	exitRouteV6          = "::/0"
)

// api is the only code that talks to Tailscale: one tailnet's calls,
// answering provider values and carrying no business rules.
type api struct {
	c       *cloud.Client
	cfg     cloud.Config // c's configuration, for the per-call client of ValidatePolicy
	tailnet string       // path-escaped
}

// newAPI builds the api of one account. The secret is held only by the token
// request the client's Auth callback sends when the cache has no token.
func (t tn) newAPI(a account, secret []byte, clock core.Clock) (*api, error) {
	switch {
	case a.Tailnet == "":
		return nil, errors.New("tailnet: a tailnet name is required")
	case a.ClientID == "":
		return nil, errors.New("tailnet: an OAuth client id is required")
	case len(secret) == 0:
		return nil, errors.New("tailnet: an OAuth client secret is required")
	}
	tokens, err := cloud.New(cloud.Config{Provider: provider, BaseURL: t.d.baseURL, HTTP: t.d.http, Clock: clock, Sleep: t.d.sleep})
	if err != nil {
		return nil, fmt.Errorf("tailnet: %w", err)
	}
	form := []byte(url.Values{
		"client_id": {a.ClientID}, "client_secret": {string(secret)}, "grant_type": {"client_credentials"},
	}.Encode())
	fetch := func(ctx context.Context) (string, time.Duration, error) { return fetchToken(ctx, tokens, form) }
	// The cache never sees the secret, so its key carries the secret's hash:
	// a changed secret then never reuses a token minted from the old one.
	sum := sha256.Sum256(secret)
	key := a.ClientID + "/" + hex.EncodeToString(sum[:])
	cfg := cloud.Config{
		Provider: provider,
		BaseURL:  t.d.baseURL,
		Auth: func(r *http.Request) error {
			tok, err := t.tokens.token(r.Context(), key, fetch, clock.Now())
			if err != nil {
				return err
			}
			r.Header.Set("Authorization", "Bearer "+tok)
			return nil
		},
		HTTP:  t.d.http,
		Clock: clock,
		Sleep: t.d.sleep,
	}
	c, err := cloud.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("tailnet: %w", err)
	}
	return &api{c: c, cfg: cfg, tailnet: url.PathEscape(a.Tailnet)}, nil
}

// fetchToken asks the token endpoint for an access token and its lifetime.
func fetchToken(ctx context.Context, c *cloud.Client, form []byte) (string, time.Duration, error) {
	res, err := c.Do(ctx, cloud.Request{
		Method: http.MethodPost,
		Path:   oauthPath,
		Header: http.Header{"Content-Type": {"application/x-www-form-urlencoded"}, "Accept": {"application/json"}},
		Body:   form,
	})
	if err != nil {
		// A token request writes nothing, so an answer that never came only
		// means the provider is unavailable.
		if errcode.From(err).Code == errcode.CloudUncertainOutcome {
			return "", 0, errcode.Wrap(errcode.CloudUnavailable, err, "tailscale did not issue an access token")
		}
		return "", 0, err
	}
	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   *int64 `json:"expires_in"`
	}
	if err := json.Unmarshal(res.Body, &tok); err != nil {
		return "", 0, errcode.Wrap(errcode.CloudUnavailable, err, "tailscale answered an access token that could not be read")
	}
	if tok.AccessToken == "" {
		return "", 0, errcode.New(errcode.CloudUnavailable, "tailscale answered no access token")
	}
	ttl := defaultTokenLifetime
	if tok.ExpiresIn != nil {
		ttl = time.Duration(*tok.ExpiresIn) * time.Second
	}
	return tok.AccessToken, ttl, nil
}

func (a *api) tailnetPath(rest string) string { return "/api/v2/tailnet/" + a.tailnet + rest }

func (a *api) keyPath(id string) string { return a.tailnetPath("/keys/" + url.PathEscape(id)) }

func devicePath(id, rest string) string { return "/api/v2/device/" + url.PathEscape(id) + rest }

func allFields() url.Values { return url.Values{"fields": {"all"}} }

// requireID refuses an empty id before any request is sent.
func requireID(kind, id string) error {
	if id == "" {
		return core.Definite(errcode.New(errcode.InvalidInput, "a "+kind+" id is required"))
	}
	return nil
}

// notFound turns the 404 of a call that names one object into not_found,
// keeping the cloud client's error, and so its definite mark, in the chain.
func notFound(err error, kind, id string) error {
	if cloud.Status(err) != http.StatusNotFound {
		return err
	}
	return errcode.Wrap(errcode.NotFound, err, fmt.Sprintf("tailscale %s %s was not found", kind, id))
}

// call sends one request with in encoded as JSON (nil sends no body) and
// decodes a 2xx body into out (nil skips it).
func (a *api) call(ctx context.Context, method, path string, query url.Values, in, out any) error {
	req := cloud.Request{Method: method, Path: path, Query: query, Header: http.Header{"Accept": {"application/json"}}}
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return core.Definite(fmt.Errorf("tailnet: encode request: %w", err))
		}
		req.Body = b
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := a.c.Do(ctx, req)
	if err != nil {
		return err
	}
	if out != nil {
		if err := json.Unmarshal(res.Body, out); err != nil {
			return unreadable(err)
		}
	}
	return nil
}

func unreadable(err error) error {
	return errcode.Wrap(errcode.CloudUnavailable, err, "tailscale answered JSON that could not be read")
}

// orEmpty returns s, or an empty list when s is nil.
func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// instant is a provider timestamp as given, except the zero instant, which
// the provider sends for a time that is not set.
func instant(s string) string {
	if t, err := time.Parse(time.RFC3339, s); err == nil && t.IsZero() {
		return ""
	}
	return s
}

// rfc3339 parses an optional provider timestamp; "" is the zero time.
func rfc3339(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, unreadable(err)
	}
	return t.UTC(), nil
}

type deviceJSON struct {
	NodeID            string   `json:"nodeId"`
	Name              string   `json:"name"`
	Hostname          string   `json:"hostname"`
	OS                string   `json:"os"`
	User              string   `json:"user"`
	Addresses         []string `json:"addresses"`
	Tags              []string `json:"tags"`
	Authorized        bool     `json:"authorized"`
	LastSeen          string   `json:"lastSeen"`
	Expires           string   `json:"expires"`
	KeyExpiryDisabled bool     `json:"keyExpiryDisabled"`
	AdvertisedRoutes  []string `json:"advertisedRoutes"`
	EnabledRoutes     []string `json:"enabledRoutes"`
}

func (d deviceJSON) device() Device {
	out := Device{
		ID:                d.NodeID,
		Name:              d.Name,
		Hostname:          d.Hostname,
		OS:                d.OS,
		User:              d.User,
		Addresses:         orEmpty(d.Addresses),
		Tags:              orEmpty(d.Tags),
		Authorized:        d.Authorized,
		LastSeen:          instant(d.LastSeen),
		Expires:           instant(d.Expires),
		KeyExpiryDisabled: d.KeyExpiryDisabled,
		AdvertisedRoutes:  orEmpty(d.AdvertisedRoutes),
		EnabledRoutes:     orEmpty(d.EnabledRoutes),
	}
	out.ExitNode = slices.Contains(out.AdvertisedRoutes, exitRouteV4) || slices.Contains(out.AdvertisedRoutes, exitRouteV6)
	out.ExitNodeEnabled = slices.Contains(out.EnabledRoutes, exitRouteV4) && slices.Contains(out.EnabledRoutes, exitRouteV6)
	return out
}

// Devices lists the tailnet's devices.
func (a *api) Devices(ctx context.Context) ([]Device, error) {
	var res struct {
		Devices []deviceJSON `json:"devices"`
	}
	if err := a.call(ctx, http.MethodGet, a.tailnetPath("/devices"), allFields(), nil, &res); err != nil {
		return nil, err
	}
	out := make([]Device, 0, len(res.Devices))
	for _, d := range res.Devices {
		out = append(out, d.device())
	}
	return out, nil
}

// Device reads one device by its node id.
func (a *api) Device(ctx context.Context, id string) (Device, error) {
	if err := requireID("device", id); err != nil {
		return Device{}, err
	}
	var d deviceJSON
	if err := a.call(ctx, http.MethodGet, devicePath(id, ""), allFields(), nil, &d); err != nil {
		return Device{}, notFound(err, "device", id)
	}
	return d.device(), nil
}

// Authorize authorizes a device, or revokes its authorization.
func (a *api) Authorize(ctx context.Context, id string, authorized bool) error {
	if err := requireID("device", id); err != nil {
		return err
	}
	body := struct {
		Authorized bool `json:"authorized"`
	}{authorized}
	return notFound(a.call(ctx, http.MethodPost, devicePath(id, "/authorized"), nil, body, nil), "device", id)
}

func (r DeviceRoutes) nonNil() DeviceRoutes {
	return DeviceRoutes{Advertised: orEmpty(r.Advertised), Enabled: orEmpty(r.Enabled)}
}

// Routes reads the routes a device advertises and those approved.
func (a *api) Routes(ctx context.Context, id string) (DeviceRoutes, error) {
	if err := requireID("device", id); err != nil {
		return DeviceRoutes{}, err
	}
	var r DeviceRoutes
	if err := a.call(ctx, http.MethodGet, devicePath(id, "/routes"), nil, nil, &r); err != nil {
		return DeviceRoutes{}, notFound(err, "device", id)
	}
	return r.nonNil(), nil
}

// SetRoutes replaces a device's approved routes with routes; an empty list
// removes every approval.
func (a *api) SetRoutes(ctx context.Context, id string, routes []string) (DeviceRoutes, error) {
	if err := requireID("device", id); err != nil {
		return DeviceRoutes{}, err
	}
	body := struct {
		Routes []string `json:"routes"`
	}{orEmpty(routes)}
	var r DeviceRoutes
	if err := a.call(ctx, http.MethodPost, devicePath(id, "/routes"), nil, body, &r); err != nil {
		return DeviceRoutes{}, notFound(err, "device", id)
	}
	return r.nonNil(), nil
}

// Expire expires a device's node key, so that it must log in again.
func (a *api) Expire(ctx context.Context, id string) error {
	if err := requireID("device", id); err != nil {
		return err
	}
	return notFound(a.call(ctx, http.MethodPost, devicePath(id, "/expire"), nil, nil, nil), "device", id)
}

// DeleteDevice removes a device from the tailnet.
func (a *api) DeleteDevice(ctx context.Context, id string) error {
	if err := requireID("device", id); err != nil {
		return err
	}
	return notFound(a.call(ctx, http.MethodDelete, devicePath(id, ""), nil, nil, nil), "device", id)
}

type keyCreateJSON struct {
	Capabilities  keyCapabilitiesJSON `json:"capabilities"`
	ExpirySeconds int                 `json:"expirySeconds,omitempty"`
	Description   string              `json:"description"`
}

type keyCapabilitiesJSON struct {
	Devices struct {
		Create struct {
			Reusable      bool     `json:"reusable"`
			Ephemeral     bool     `json:"ephemeral"`
			Preauthorized bool     `json:"preauthorized"`
			Tags          []string `json:"tags"`
		} `json:"create"`
	} `json:"devices"`
}

// CreateKey creates an auth key and answers it with its secret.
func (a *api) CreateKey(ctx context.Context, r KeyRequest) (CreatedKey, error) {
	var body keyCreateJSON
	create := &body.Capabilities.Devices.Create
	create.Reusable, create.Ephemeral, create.Preauthorized = r.Reusable, r.Ephemeral, r.Preauthorized
	create.Tags = orEmpty(r.Tags)
	if r.ExpirySeconds > 0 {
		body.ExpirySeconds = r.ExpirySeconds
	}
	body.Description = r.Description
	var res struct {
		ID          string `json:"id"`
		Key         string `json:"key"`
		Expires     string `json:"expires"`
		Description string `json:"description"`
	}
	if err := a.call(ctx, http.MethodPost, a.tailnetPath("/keys"), nil, body, &res); err != nil {
		return CreatedKey{}, err
	}
	expires, err := rfc3339(res.Expires)
	if err != nil {
		return CreatedKey{}, err
	}
	return CreatedKey{ID: res.ID, Key: res.Key, Expires: expires, Description: res.Description}, nil
}

type keyJSON struct {
	ID          string `json:"id"`
	KeyType     string `json:"keyType"`
	Description string `json:"description"`
	Created     string `json:"created"`
	Expires     string `json:"expires"`
	Revoked     string `json:"revoked"`
	Invalid     bool   `json:"invalid"`
}

func (k keyJSON) info() (KeyInfo, error) {
	created, err := rfc3339(k.Created)
	if err != nil {
		return KeyInfo{}, err
	}
	expires, err := rfc3339(k.Expires)
	if err != nil {
		return KeyInfo{}, err
	}
	return KeyInfo{ID: k.ID, Description: k.Description, Created: created, Expires: expires, Revoked: k.Revoked != "" || k.Invalid}, nil
}

// Keys lists the tailnet's auth keys.
func (a *api) Keys(ctx context.Context) ([]KeyInfo, error) {
	var res struct {
		Keys []keyJSON `json:"keys"`
	}
	// Without all=true a token derived from an OAuth client lists only OAuth
	// clients; with it the list also holds API access tokens and OAuth
	// clients, which are left out.
	if err := a.call(ctx, http.MethodGet, a.tailnetPath("/keys"), url.Values{"all": {"true"}}, nil, &res); err != nil {
		return nil, err
	}
	out := make([]KeyInfo, 0, len(res.Keys))
	for _, k := range res.Keys {
		if k.KeyType != "" && k.KeyType != "auth" {
			continue
		}
		info, err := k.info()
		if err != nil {
			return nil, err
		}
		out = append(out, info)
	}
	return out, nil
}

// Key reads one auth key.
func (a *api) Key(ctx context.Context, id string) (KeyInfo, error) {
	if err := requireID("auth key", id); err != nil {
		return KeyInfo{}, err
	}
	var k keyJSON
	if err := a.call(ctx, http.MethodGet, a.keyPath(id), nil, nil, &k); err != nil {
		return KeyInfo{}, notFound(err, "auth key", id)
	}
	return k.info()
}

// DeleteKey deletes one auth key.
func (a *api) DeleteKey(ctx context.Context, id string) error {
	if err := requireID("auth key", id); err != nil {
		return err
	}
	return notFound(a.call(ctx, http.MethodDelete, a.keyPath(id), nil, nil, nil), "auth key", id)
}

// Policy reads the policy file as HuJSON, comments kept, and its ETag as sent.
func (a *api) Policy(ctx context.Context) (text []byte, etag string, err error) {
	res, err := a.c.Do(ctx, cloud.Request{Method: http.MethodGet, Path: a.tailnetPath("/acl"), Header: http.Header{"Accept": {contentHuJSON}}})
	if err != nil {
		return nil, "", err
	}
	etag = res.Header.Get("ETag")
	if etag == "" {
		return nil, "", errcode.New(errcode.CloudUnavailable, "tailscale answered the policy file without an ETag")
	}
	return res.Body, etag, nil
}

// SetPolicy writes the policy file if it still has the ETag ifMatch, and
// answers the ETag of the text written.
func (a *api) SetPolicy(ctx context.Context, text []byte, ifMatch string) (etag string, err error) {
	if ifMatch == "" {
		return "", core.Definite(errcode.New(errcode.InvalidInput, "the ETag of the policy file read is required to write it"))
	}
	res, err := a.c.Do(ctx, cloud.Request{
		Method: http.MethodPost,
		Path:   a.tailnetPath("/acl"),
		Header: http.Header{"Content-Type": {contentHuJSON}, "If-Match": {ifMatch}},
		Body:   text,
	})
	if cloud.Status(err) == http.StatusPreconditionFailed {
		return "", core.Definite(errcode.Wrap(errcode.TailnetPolicyConflict, err, "the tailnet policy file changed since it was read"))
	}
	if err != nil {
		return "", err
	}
	if etag := res.Header.Get("ETag"); etag != "" {
		return etag, nil
	}
	// The ETag is not documented on this answer, so the policy is read back;
	// its ETag stands for the write only when the text read is the text posted.
	stored, etag, err := a.Policy(ctx)
	switch {
	case err != nil:
		// The read's error is kept only as text: a definite mark in its chain,
		// such as a refused token request, would claim the write committed
		// nothing.
		return "", errcode.Wrap(errcode.CloudUncertainOutcome, errors.New(err.Error()),
			"tailscale accepted the policy file but it could not be read back; the write may have committed")
	case !bytes.Equal(stored, text):
		return "", errcode.New(errcode.CloudUncertainOutcome,
			"tailscale accepted the policy file but answered another text when it was read back; the write may have committed")
	}
	return etag, nil
}

// ValidatePolicy asks the provider to check a policy file without writing it:
// nil when it is valid, tailnet.policy_invalid with the provider's message when
// it is not.
func (a *api) ValidatePolicy(ctx context.Context, text []byte) error {
	// The cloud client never exposes the body of a refusal, so this call gets a
	// client of its own whose ErrorCode keeps the provider's message.
	var message string
	cfg := a.cfg
	cfg.ErrorCode = func(body []byte) string {
		message = messageOf(body)
		return ""
	}
	c, err := cloud.New(cfg)
	if err != nil {
		return fmt.Errorf("tailnet: %w", err)
	}
	res, err := c.Do(ctx, cloud.Request{
		Method: http.MethodPost,
		Path:   a.tailnetPath("/acl/validate"),
		Header: http.Header{"Content-Type": {contentHuJSON}, "Accept": {"application/json"}},
		Body:   text,
	})
	if cloud.Status(err) == http.StatusBadRequest {
		return invalidPolicy(err, message)
	}
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(res.Body)) == 0 {
		return nil
	}
	var answer struct {
		Message string `json:"message"`
		Data    any    `json:"data"`
	}
	if err := json.Unmarshal(res.Body, &answer); err != nil {
		return unreadable(err)
	}
	if answer.Message == "" && !nonEmpty(answer.Data) {
		return nil
	}
	return invalidPolicy(nil, answer.Message)
}

// messageOf is the message member of a provider answer, or "".
func messageOf(body []byte) string {
	var e struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &e) != nil {
		return ""
	}
	return e.Message
}

func nonEmpty(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case string:
		return x != ""
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	}
	return true
}

func invalidPolicy(err error, message string) error {
	if message == "" {
		message = "tailscale rejected the policy"
	}
	return core.Definite(errcode.Wrap(errcode.TailnetPolicyInvalid, err, message))
}
