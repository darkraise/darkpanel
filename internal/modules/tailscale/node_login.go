package tailscale

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/tailscale/link"
	"github.com/darkraise/darkpanel/internal/platform/tslocal"
)

const (
	nodeLoginResource = ID + ".node:self"
	// nodeLoginCommitWait is how long a login whose answer was lost may still complete: tailscaled keeps trying
	// the control server after the call returns.
	nodeLoginCommitWait = 2 * time.Minute
	// nodeLoginWait keeps a login inside the default 30 s apply budget.
	nodeLoginWait = 20 * time.Second
	nodeLoginPoll = 500 * time.Millisecond
)

var (
	nodeLoginTag      = regexp.MustCompile(`^tag:[a-z0-9][a-z0-9-]{0,62}$`)
	nodeLoginHostname = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)
)

// nodeLogin is tailscale.node.login: it logs the node in with an auth key, or logs it in again with relogin. Its
// undo logs out a node that was logged out before; the identity a relogin replaced cannot be restored.
func (t ts) nodeLogin() core.ActionDef {
	return core.DefineMutating[LoginInput, LoginResult](core.ActionSpec{
		Name: ID + ".node.login", Summary: "Log the node in to a tailnet with an auth key.",
		Version: 1, Class: core.ClassCompensable, Side: core.SideAgent, LockKeys: []string{lockKey},
		Guards:    []core.GuardSpec{{ID: link.GuardID, Overridable: true}},
		Available: t.available,
	}, nodeLoginHandler{t: t})
}

// nodeLoginHandler is node.login's handler.
type nodeLoginHandler struct{ t ts }

// nodeLoginLoggedIn reads whether the node is logged in; known is false while the backend has not started.
func nodeLoginLoggedIn(st *tslocal.Status) (loggedIn, known bool) {
	switch st.BackendState {
	case "NoState":
		return false, false
	case "NeedsLogin":
		return false, true
	}
	return true, true
}

// nodeLoginSettled reports whether a login has finished: the node runs, or waits for an admin's approval.
func nodeLoginSettled(st *tslocal.Status) bool {
	return st.BackendState == "Running" || st.BackendState == "NeedsMachineAuth"
}

// nodeLoginDoc is the login effect's state document: State is what an undo restores, Version what it cannot.
func nodeLoginDoc(st *tslocal.Status) core.StateDoc {
	loggedIn, _ := nodeLoginLoggedIn(st)
	doc := nodeLoginFlagDoc(loggedIn)
	doc.Version, _ = json.Marshal(map[string]string{"backendState": st.BackendState})
	return doc
}

// nodeLoginFlagDoc is a state document holding only the login flag.
func nodeLoginFlagDoc(loggedIn bool) core.StateDoc {
	b, _ := json.Marshal(map[string]bool{"loggedIn": loggedIn})
	return core.StateDoc{State: b}
}

// nodeLoginSelfName is the node's MagicDNS name, or "this node" when it has none.
func nodeLoginSelfName(st *tslocal.Status) string {
	if st.Self == nil || st.Self.DNSName == "" {
		return "this node"
	}
	return strings.TrimSuffix(st.Self.DNSName, ".")
}

// nodeLoginInvalid is a definite request.invalid_input refusal, with one field error at path when path is set.
func nodeLoginInvalid(path, detail string) error {
	e := &errcode.Error{Code: errcode.InvalidInput, Detail: detail}
	if path != "" {
		e.Fields = []errcode.FieldError{{Path: path, Message: detail}}
	}
	return core.Definite(e)
}

// nodeLoginNotStarted refuses a plan on a node whose backend has not started, which says nothing of its login.
func nodeLoginNotStarted() error {
	return core.Definite(errcode.New(errcode.TailscaleUnavailable, "tailscaled has not finished starting; plan again in a moment"))
}

// nodeLoginValidate checks the input's shape.
func nodeLoginValidate(in LoginInput) error {
	switch {
	case in.AuthKey == "":
		return nodeLoginInvalid("/authKey", "the auth key is empty")
	case strings.ContainsFunc(in.AuthKey, unicode.IsSpace):
		return nodeLoginInvalid("/authKey", "the auth key contains whitespace")
	case in.Hostname != "" && !nodeLoginHostname.MatchString(in.Hostname):
		return nodeLoginInvalid("/hostname", "the host name must be one DNS label: letters, digits and inner hyphens, at most 63 characters")
	}
	for i, tag := range in.AdvertiseTags {
		if !nodeLoginTag.MatchString(tag) {
			return nodeLoginInvalid("/advertiseTags/"+strconv.Itoa(i), tag+" is not a tag:<name> with a lower-case name")
		}
	}
	return nil
}

// nodeLoginRefusal classifies a failed call with secret taken out of every text: a socket that does not answer and
// a 4xx answer guarantee tailscaled did nothing, so they come back definite; any other error is in doubt.
func nodeLoginRefusal(err error, what, secret string) error {
	redact := func(s string) string {
		if secret == "" {
			return s
		}
		return strings.ReplaceAll(s, secret, "[redacted]")
	}
	cause := errors.New(redact(err.Error()))
	var api *tslocal.APIError
	switch {
	case errors.Is(err, tslocal.ErrUnavailable):
		return core.Definite(errcode.Wrap(errcode.TailscaleUnavailable, cause, "tailscaled does not answer on its socket"))
	case errors.As(err, &api) && api.StatusCode >= 400 && api.StatusCode <= 499:
		return core.Definite(errcode.Wrap(errcode.InvalidInput, cause, "tailscaled refused the "+what+": "+redact(api.Message)))
	}
	// A new error, not a wrap: the original's text may carry the secret.
	return errors.New("tailscale: " + what + ": " + cause.Error())
}

// nodeLoginStatus reads the node's status.
func nodeLoginStatus(ctx context.Context, local localAPI) (*tslocal.Status, error) {
	st, err := local.Status(ctx)
	if err != nil {
		return nil, fmt.Errorf("tailscale: read the status: %w", err)
	}
	return st, nil
}

func (h nodeLoginHandler) Plan(ctx context.Context, env core.Env, in LoginInput) (core.PlanOutcome, error) {
	if err := nodeLoginValidate(in); err != nil {
		return core.PlanOutcome{}, err
	}
	st, err := nodeLoginStatus(ctx, h.t.d.local())
	if err != nil {
		return core.PlanOutcome{}, err
	}
	loggedIn, known := nodeLoginLoggedIn(st)
	switch {
	case !known:
		return core.PlanOutcome{}, nodeLoginNotStarted()
	case loggedIn && !in.Relogin:
		return core.PlanOutcome{}, nodeLoginInvalid("/relogin", "the node is logged in as "+nodeLoginSelfName(st)+"; set relogin to log it in again")
	}
	lp, _, err := h.t.currentLinkPrefs(ctx)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	after := lp
	after.WantRunning = true
	change := link.Change{Kind: "prefs", After: after}
	diff := []core.DiffLine{{Op: "-", Text: "node: logged out"}, {Op: "+", Text: "node: logged in with an auth key"}}
	if loggedIn {
		change.Kind = "relogin"
		diff = []core.DiffLine{{Op: "-", Text: "node: logged in as " + nodeLoginSelfName(st)}, {Op: "+", Text: "node: logged in again with an auth key"}}
	}
	g, err := h.t.linkGuard(ctx, env, in.Guarded, lp, change)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	if in.Hostname != "" {
		diff = append(diff, core.DiffLine{Op: "+", Text: "hostname: " + in.Hostname})
	}
	if len(in.AdvertiseTags) > 0 {
		diff = append(diff, core.DiffLine{Op: "+", Text: "advertiseTags: " + strings.Join(in.AdvertiseTags, ", ")})
	}
	var self string
	if st.Self != nil {
		self = st.Self.ID
	}
	sum := sha256.Sum256([]byte(strconv.FormatBool(loggedIn) + "|" + self))
	return core.PlanOutcome{Diff: diff, Guards: []core.Guard{g}, StateHash: hex.EncodeToString(sum[:])}, nil
}

// Apply journals the effect before Start, so a lost answer leaves an intended effect for the engine to settle.
func (h nodeLoginHandler) Apply(ctx context.Context, env core.Env, in LoginInput) (LoginResult, error) {
	if err := nodeLoginValidate(in); err != nil {
		return LoginResult{}, err
	}
	local := h.t.d.local()
	st, err := nodeLoginStatus(ctx, local)
	if err != nil {
		return LoginResult{}, err
	}
	// The engine planned again just before, but tailscaled may have restarted since: an unknown login journaled as
	// logged out would make the undo log out a node that was logged in.
	was, known := nodeLoginLoggedIn(st)
	if !known {
		return LoginResult{}, nodeLoginNotStarted()
	}
	p, err := local.Prefs(ctx)
	if err != nil {
		return LoginResult{}, fmt.Errorf("tailscale: read the preferences: %w", err)
	}
	prefs := *p
	prefs.WantRunning, prefs.LoggedOut = true, false
	if in.Hostname != "" {
		prefs.Hostname = in.Hostname
	}
	if len(in.AdvertiseTags) > 0 {
		prefs.AdvertiseTags = slices.Clone(in.AdvertiseTags)
	}
	after := nodeLoginFlagDoc(true)
	seq, err := env.Effects.Intend(ctx, core.Effect{
		Kind: core.EffectModify, Resource: nodeLoginResource, Local: false, CommitWait: nodeLoginCommitWait,
		Before: nodeLoginFlagDoc(was), After: &after,
	}, nil)
	if err != nil {
		return LoginResult{}, err
	}
	if err := local.Start(ctx, tslocal.StartOptions{AuthKey: in.AuthKey, UpdatePrefs: &prefs}); err != nil {
		refusal := nodeLoginRefusal(err, "login", in.AuthKey)
		if !core.IsDefinite(refusal) {
			return LoginResult{}, refusal
		}
		if err := env.Effects.Skipped(ctx, seq); err != nil {
			return LoginResult{}, err
		}
		return LoginResult{}, refusal
	}
	// Start has already replaced the preferences, so whatever this call answers, the effect stays intended.
	if err := local.LoginInteractive(ctx); err != nil {
		return LoginResult{}, errors.New("tailscale: login: " + strings.ReplaceAll(err.Error(), in.AuthKey, "[redacted]"))
	}
	settled, err := nodeLoginAwait(ctx, env, local)
	if err != nil {
		return LoginResult{}, err
	}
	if err := env.Effects.Made(ctx, seq, nodeLoginResource, nodeLoginDoc(settled)); err != nil {
		return LoginResult{}, err
	}
	res := LoginResult{BackendState: settled.BackendState, Addresses: []string{}}
	if settled.Self != nil {
		res.DNSName = strings.TrimSuffix(settled.Self.DNSName, ".")
	}
	addrs := slices.Clone(settled.TailscaleIPs)
	slices.SortStableFunc(addrs, func(a, b netip.Addr) int {
		switch {
		case a.Is4() == b.Is4():
			return 0
		case a.Is4():
			return -1
		}
		return 1
	})
	for _, a := range addrs {
		res.Addresses = append(res.Addresses, a.String())
	}
	return res, nil
}

// nodeLoginAwait polls the status until the login settles, for at most nodeLoginWait.
func nodeLoginAwait(ctx context.Context, env core.Env, local localAPI) (*tslocal.Status, error) {
	deadline := env.Clock.Now().Add(nodeLoginWait)
	for {
		st, err := nodeLoginStatus(ctx, local)
		if err != nil {
			return nil, err
		}
		if nodeLoginSettled(st) {
			return st, nil
		}
		if !env.Clock.Now().Before(deadline) {
			return nil, fmt.Errorf("tailscale: the node did not log in within %s (backend state %s)", nodeLoginWait, st.BackendState)
		}
		tick := make(chan struct{})
		timer := env.Clock.AfterFunc(nodeLoginPoll, func() { close(tick) })
		select {
		case <-tick:
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		}
	}
}

// Verify checks the host from the input alone, as recovery calls it without an output.
func (h nodeLoginHandler) Verify(ctx context.Context, _ core.Env, _ LoginInput, _ LoginResult) error {
	st, err := nodeLoginStatus(ctx, h.t.d.local())
	if err != nil {
		return err
	}
	if !nodeLoginSettled(st) {
		return fmt.Errorf("tailscale: the backend state is %s after the login, want Running or NeedsMachineAuth", st.BackendState)
	}
	return nil
}

func (h nodeLoginHandler) Observe(ctx context.Context, _ core.Env, e core.Effect) (core.Observation, error) {
	if e.Resource != nodeLoginResource {
		return core.Observation{}, fmt.Errorf("tailscale: observe %s: the login handler observes only %s", e.Resource, nodeLoginResource)
	}
	st, err := nodeLoginStatus(ctx, h.t.d.local())
	if err != nil {
		return core.Observation{}, err
	}
	return core.Observation{Current: nodeLoginDoc(st), Matches: 1, Exclusive: true}, nil
}

// Inverse logs out a node the login found logged out. After a relogin it does nothing: the node stays logged in,
// which is the state the undo restores, and the identity the relogin replaced cannot be brought back.
func (h nodeLoginHandler) Inverse(ctx context.Context, _ core.Env, e core.Effect) (core.InverseResult, error) {
	var before struct {
		LoggedIn *bool `json:"loggedIn"`
	}
	if err := json.Unmarshal(e.Before.State, &before); err != nil {
		return core.InverseResult{}, fmt.Errorf("tailscale: decode the before state of effect %d: %w", e.Seq, err)
	}
	if before.LoggedIn == nil {
		return core.InverseResult{}, fmt.Errorf("tailscale: the before state of effect %d has no loggedIn flag", e.Seq)
	}
	if *before.LoggedIn {
		return core.InverseResult{}, nil
	}
	if err := h.t.d.local().Logout(ctx); err != nil {
		return core.InverseResult{}, nodeLoginRefusal(err, "logout", "")
	}
	return core.InverseResult{}, nil
}
