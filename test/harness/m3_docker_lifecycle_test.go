//go:build harness

package harness

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/firewall/model"
	"github.com/darkraise/darkpanel/test/harness/vm"
)

// m3dDockExemption mirrors dockerfw.Exemption with plain strings.
type m3dDockExemption struct {
	InInterface     string `json:"inInterface,omitempty"`
	OrigDestination string `json:"origDestination"`
	OrigPort        uint16 `json:"origPort,omitempty"`
	Destination     string `json:"destination"`
	Proto           string `json:"proto"`
	Port            uint16 `json:"port,omitempty"`
}

// m3dDockIssue mirrors dockerfw.Issue.
type m3dDockIssue struct {
	Level  string `json:"level"`
	Reason string `json:"reason"`
}

// m3dDockNetwork mirrors firewall.DockerNetwork.
type m3dDockNetwork struct {
	Name      string   `json:"name"`
	ID        string   `json:"id"`
	Bridge    string   `json:"bridge"`
	Subnets   []string `json:"subnets"`
	Protected bool     `json:"protected"`
	Reason    string   `json:"reason,omitempty"`
}

// m3dDockBinding mirrors dockerfw.Binding.
type m3dDockBinding struct {
	Selector string `json:"selector"`
	HostAddr string `json:"hostAddr,omitempty"`
	HostPort uint16 `json:"hostPort"`
	Proto    string `json:"proto"`
}

// m3dDockRule mirrors dockerfw.ContainerRule.
type m3dDockRule struct {
	ID      string         `json:"id"`
	Binding m3dDockBinding `json:"binding"`
	Kind    string         `json:"kind"`
	From    string         `json:"from,omitempty"`
}

// m3dDockBindingView mirrors firewall.DockerBinding.
type m3dDockBindingView struct {
	Binding       m3dDockBinding `json:"binding"`
	ID            string         `json:"id"`
	Containers    []string       `json:"containers"`
	ContainerPort uint16         `json:"containerPort"`
	Allowed       []string       `json:"allowed"`
	Denied        []string       `json:"denied"`
	Proxied       []string       `json:"proxied,omitempty"`
	PrivateReach  []string       `json:"privateReach,omitempty"`
}

// m3dDockStatus mirrors the firewall.docker resource, firewall.DockerStatus.
type m3dDockStatus struct {
	Available       bool               `json:"available"`
	Reason          string             `json:"reason,omitempty"`
	Version         string             `json:"version,omitempty"`
	Driver          string             `json:"driver,omitempty"`
	DockerVariant   string             `json:"dockerVariant,omitempty"`
	UFWVariant      string             `json:"ufwVariant,omitempty"`
	RawProtection   bool               `json:"rawProtection"`
	Supported       bool               `json:"supported"`
	Unsupported     []string           `json:"unsupported,omitempty"`
	Enabled         bool               `json:"enabled"`
	Health          string             `json:"health"`
	Issues          []m3dDockIssue     `json:"issues,omitempty"`
	ForeignRules    []string           `json:"foreignRules,omitempty"`
	ForeignForwards []m3dDockExemption `json:"foreignForwards,omitempty"`
	ManualRoutes    []string           `json:"manualRoutes,omitempty"`
	Networks        []m3dDockNetwork   `json:"networks"`
	Settings        struct {
		PrivateSources    []string           `json:"privateSources"`
		ForwardExemptions []m3dDockExemption `json:"forwardExemptions"`
	} `json:"settings"`
	Rules    []m3dDockRule        `json:"rules"`
	Bindings []m3dDockBindingView `json:"bindings"`
}

// m3dxDockDetected accepts the status of a host where Docker is installed
// and supported and the integration has never been enabled.
func m3dxDockDetected(st m3dDockStatus) error {
	if !st.Available {
		return fmt.Errorf("harness: docker unavailable: %s", st.Reason)
	}
	if !st.Supported {
		return fmt.Errorf("harness: docker host unsupported: %s", strings.Join(st.Unsupported, "; "))
	}
	major, _, _ := strings.Cut(st.Version, ".")
	if n, err := strconv.Atoi(major); err != nil || n < 28 {
		return fmt.Errorf("harness: docker version %q is not 28 or later", st.Version)
	}
	if st.Driver != "iptables" {
		return fmt.Errorf("harness: docker firewall driver %q, want iptables", st.Driver)
	}
	if !st.RawProtection {
		return errors.New("harness: docker's raw protection is not loaded")
	}
	if st.DockerVariant == "" || st.DockerVariant == "both" || st.DockerVariant != st.UFWVariant {
		return fmt.Errorf("harness: docker variant %q and ufw variant %q, want one variant for both", st.DockerVariant, st.UFWVariant)
	}
	if st.Enabled || st.Health != "off" {
		return fmt.Errorf("harness: docker integration enabled %t with health %q, want it not enabled and off", st.Enabled, st.Health)
	}
	if !slices.ContainsFunc(st.Networks, func(n m3dDockNetwork) bool { return n.Name == "bridge" && n.Bridge == "docker0" }) {
		return fmt.Errorf("harness: docker has no default bridge network on docker0: %+v", st.Networks)
	}
	return nil
}

// m3dxDockHealth returns a check that accepts a status whose integration is
// enabled with the health want.
func m3dxDockHealth(want string) func(m3dDockStatus) error {
	return func(st m3dDockStatus) error {
		if !st.Enabled || st.Health != want {
			return fmt.Errorf("harness: docker integration enabled %t with health %q, want it enabled with health %q; issues %+v", st.Enabled, st.Health, want, st.Issues)
		}
		return nil
	}
}

// m3dxDockChains returns the chains of an iptables-save text whose names are
// Darkpanel's, in the order they are declared.
func m3dxDockChains(save string) []string {
	var out []string
	for line := range strings.SplitSeq(save, "\n") {
		decl, ok := strings.CutPrefix(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		name, _, _ := strings.Cut(decl, " ")
		if strings.HasPrefix(name, "darkpanel-") || strings.HasPrefix(name, "dp-b-") {
			out = append(out, name)
		}
	}
	return out
}

// m3dxDockRulesOf returns the text after "-A <chain> " of every rule of
// chain in an iptables-save text, in order.
func m3dxDockRulesOf(save, chain string) []string {
	var out []string
	for line := range strings.SplitSeq(save, "\n") {
		if rule, ok := strings.CutPrefix(strings.TrimSpace(line), "-A "+chain+" "); ok {
			out = append(out, rule)
		}
	}
	return out
}

// m3dxDockCleaned checks what the cleanup of the integration leaves: no
// Darkpanel chain, the foreign DOCKER-USER rules and no other rule except
// one unconditional return, which wantReturn requires.
func m3dxDockCleaned(save string, foreign []string, wantReturn bool) error {
	if chains := m3dxDockChains(save); len(chains) > 0 {
		return fmt.Errorf("harness: Darkpanel chains are still loaded: %q", chains)
	}
	var others []string
	returns := 0
	for _, r := range m3dxDockRulesOf(save, "DOCKER-USER") {
		if r == "-j RETURN" {
			returns++
		} else {
			others = append(others, r)
		}
	}
	switch {
	case !slices.Equal(others, foreign):
		return fmt.Errorf("harness: DOCKER-USER holds the rules %q besides its return, want %q", others, foreign)
	case returns > 1:
		return fmt.Errorf("harness: DOCKER-USER holds %d rules -j RETURN, want at most one", returns)
	case wantReturn && returns != 1:
		return fmt.Errorf("harness: DOCKER-USER holds %d rules -j RETURN, want exactly one", returns)
	}
	return nil
}

// m3dxDockBlockLoaded accepts an iptables-save text that holds the loaded
// block: its two fixed chains, the DNAT gate in DOCKER-USER and a deny chain
// that ends dropping.
func m3dxDockBlockLoaded(save string) error {
	declared := m3dxDockChains(save)
	for _, chain := range []string{"darkpanel-docker-bindings", "darkpanel-docker-deny"} {
		if !slices.Contains(declared, chain) {
			return fmt.Errorf("harness: chain %s is not declared; the Darkpanel chains are %q", chain, declared)
		}
	}
	user := m3dxDockRulesOf(save, "DOCKER-USER")
	if !slices.ContainsFunc(user, func(r string) bool {
		return slices.Contains(strings.Fields(r), "DNAT") && strings.HasSuffix(r, " -j darkpanel-docker-bindings")
	}) {
		return fmt.Errorf("harness: DOCKER-USER has no DNAT rule jumping to darkpanel-docker-bindings: %q", user)
	}
	deny := m3dxDockRulesOf(save, "darkpanel-docker-deny")
	if len(deny) == 0 || deny[len(deny)-1] != "-j DROP" {
		return fmt.Errorf("harness: darkpanel-docker-deny does not end with -j DROP: %q", deny)
	}
	return nil
}

// m3dxDockHostAddr is s without the /32 or /128 that iptables-save prints on
// a host address.
func m3dxDockHostAddr(s string) string {
	if p, err := netip.ParsePrefix(s); err == nil && p.Bits() == p.Addr().BitLen() {
		return p.Addr().String()
	}
	if a, err := netip.ParseAddr(s); err == nil {
		return a.String()
	}
	return s
}

// m3dxDockOption returns the token after the first option name in tokens.
func m3dxDockOption(tokens []string, name string) (string, bool) {
	i := slices.Index(tokens, name)
	if i < 0 || i+1 >= len(tokens) {
		return "", false
	}
	return tokens[i+1], true
}

// m3dxDockIsExemption reports whether rule is the DOCKER-USER rule that
// returns the forwarded connections e describes.
func m3dxDockIsExemption(rule string, e m3dDockExemption) bool {
	if !strings.HasSuffix(rule, " -j RETURN") {
		return false
	}
	tokens := strings.Fields(rule)
	want := []struct {
		option, value string
		address       bool
	}{
		{"--ctstate", "DNAT", false},
		{"--ctorigdst", e.OrigDestination, true},
		{"-d", e.Destination, true},
		{"-p", e.Proto, false},
	}
	optional := []struct {
		option, value string
	}{
		{"--ctorigdstport", ""},
		{"--dport", ""},
		{"-i", e.InInterface},
	}
	if e.OrigPort != 0 {
		optional[0].value = strconv.Itoa(int(e.OrigPort))
	}
	if e.Port != 0 {
		optional[1].value = strconv.Itoa(int(e.Port))
	}
	for _, w := range want {
		got, ok := m3dxDockOption(tokens, w.option)
		if w.address {
			got, w.value = m3dxDockHostAddr(got), m3dxDockHostAddr(w.value)
		}
		if !ok || got != w.value {
			return false
		}
	}
	for _, o := range optional {
		got, ok := m3dxDockOption(tokens, o.option)
		if o.value == "" && ok || o.value != "" && got != o.value {
			return false
		}
	}
	return true
}

// m3dxDockExemptionLine accepts an iptables-save text whose DOCKER-USER
// holds the rule that returns the forwarded connections e describes.
func m3dxDockExemptionLine(save string, e m3dDockExemption) error {
	rules := m3dxDockRulesOf(save, "DOCKER-USER")
	if slices.ContainsFunc(rules, func(r string) bool { return m3dxDockIsExemption(r, e) }) {
		return nil
	}
	return fmt.Errorf("harness: no DOCKER-USER rule returns the exemption %+v; DOCKER-USER holds:\n%s", e, strings.Join(rules, "\n"))
}

// m3dDockTryLocked bounds how long m3dDockSettle tries an apply again.
const m3dDockTryLocked = 2 * time.Minute

// m3dxLockHeld reports whether err is, or wraps, the API's 423 lock.held.
func m3dxLockHeld(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Status == http.StatusLocked && apiErr.Code == string(errcode.LockHeld)
}

// m3dxCleanupRetryable reports whether err is a refusal that a background
// change causes and outlives: lock.held while the change applies, or a
// failing lockout guard. Plans take no lock, so a plan can read the host
// while that change's ufw reload has loaded IPv4 but not yet IPv6, which the
// guard reports as out of sync.
func m3dxCleanupRetryable(err error) bool {
	var guardErr *m3GuardError
	return m3dxLockHeld(err) || errors.As(err, &guardErr) && guardErr.Guard.ID == "firewall.lockout"
}

// m3dxRefused reports whether err is, or wraps, a refusal: an API error or a
// failing guard.
func m3dxRefused(err error) bool {
	var apiErr *APIError
	var guardErr *m3GuardError
	return errors.As(err, &apiErr) || errors.As(err, &guardErr)
}

// m3dxRetry calls apply until it returns an error that retry rejects, or
// none, waiting interval between calls, and returns its last error, joined
// with ctx's when ctx ends first.
func m3dxRetry(ctx context.Context, interval time.Duration, retry func(error) bool, apply func(context.Context) error) error {
	for {
		err := apply(ctx)
		if err == nil || !retry(err) {
			return err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%w; last error: %w", ctx.Err(), err)
		case <-time.After(interval):
		}
	}
}

// m3dxDockRefusal returns nil when err is, or wraps, an API refusal with the
// code given (any code when it is empty) whose detail names every want.
func m3dxDockRefusal(err error, code string, want ...string) error {
	var apiErr *APIError
	switch {
	case err == nil:
		return errors.New("harness: the request succeeded; want a refusal")
	case !errors.As(err, &apiErr):
		return fmt.Errorf("harness: want a refusal, got %w", err)
	case code != "" && apiErr.Code != code:
		return fmt.Errorf("harness: refused with %s (%s); want %s", apiErr.Code, apiErr.Detail, code)
	}
	for _, w := range want {
		if !strings.Contains(apiErr.Detail, w) {
			return fmt.Errorf("harness: refused with %s (%s); want the detail to name %q", apiErr.Code, apiErr.Detail, w)
		}
	}
	return nil
}

// m3dxDockInSubnet returns the second host address of subnet, an address
// that lies in it and is neither its network address nor the gateway Docker
// takes first.
func m3dxDockInSubnet(subnet string) (string, error) {
	p, err := netip.ParsePrefix(subnet)
	if err != nil {
		return "", fmt.Errorf("harness: %w", err)
	}
	if p.Bits() >= p.Addr().BitLen()-1 {
		return "", fmt.Errorf("harness: subnet %s is too small to hold a second host address", subnet)
	}
	return p.Masked().Addr().Next().Next().String(), nil
}

// m3dxDockRoutes returns one string for every route rule of rules, sorted so
// that it does not depend on the order of rules; a changed tag or a lost
// rule changes the result.
func m3dxDockRoutes(rules []model.Rule) []string {
	var out []string
	for _, r := range rules {
		if r.Direction != model.DirRoute {
			continue
		}
		out = append(out, fmt.Sprintf("%s %s %s %s from %s:%s to %s:%s in %s out %s %s %q",
			r.ID, r.Origin, r.Action, r.Proto, r.From, r.FromPort, r.To, r.ToPort, r.InIface, r.OutIface, r.Family, r.Comment))
	}
	slices.Sort(out)
	return out
}

// m3dDockRead reads host's firewall.docker resource.
func (a *API) m3dDockRead(ctx context.Context, host string) (m3dDockStatus, error) {
	var st m3dDockStatus
	err := a.Resource(ctx, host, "firewall.docker", &st)
	return st, err
}

// m3dDockConfirm confirms the pending change id.
func (a *API) m3dDockConfirm(ctx context.Context, id string) (Change, error) {
	var ch Change
	err := a.do(ctx, http.MethodPost, "/api/v1/changes/"+url.PathEscape(id)+"/confirm", nil, &ch)
	return ch, err
}

// m3dDockWait polls host's firewall.docker through api until check accepts
// it, and returns the status it accepted.
func m3dDockWait(ctx context.Context, t *testing.T, api *API, host string, timeout time.Duration, check func(m3dDockStatus) error) m3dDockStatus {
	t.Helper()
	var st m3dDockStatus
	err := eventually(ctx, timeout, func(ctx context.Context) error {
		var err error
		if st, err = api.m3dDockRead(ctx, host); err != nil {
			return err
		}
		return check(st)
	})
	if err != nil {
		t.Fatalf("firewall.docker of host %s: %v", host, err)
	}
	return st
}

// m3dDockApply plans and applies the revertible action on the agent host,
// confirms it and returns the confirmed change.
func m3dDockApply(ctx context.Context, t *testing.T, f *fleet, action string, input any) Change {
	t.Helper()
	ch, err := f.api.m3Apply(ctx, f.agentID, action, input, 300)
	m3RequirePending(t, ch, err)
	if ch, err = f.api.m3dDockConfirm(ctx, ch.ID); err != nil {
		t.Fatalf("confirm %s: %v", action, err)
	}
	if ch.State != core.StateConfirmed {
		t.Fatalf("change %s of %s is %s, want %s: %s", ch.ID, action, ch.State, core.StateConfirmed, m3JSON(ch.Error))
	}
	return ch
}

// m3dDockSettle applies and confirms the action like m3dDockApply, trying the
// plan and apply again for up to m3dDockTryLocked while retry accepts the
// refusal, and logs each such refusal. It returns the last error.
func m3dDockSettle(ctx context.Context, t *testing.T, f *fleet, action string, input any, retry func(error) bool) error {
	applyCtx, cancel := context.WithTimeout(ctx, m3dDockTryLocked)
	defer cancel()
	var ch Change
	err := m3dxRetry(applyCtx, pollInterval, retry, func(ctx context.Context) error {
		var err error
		ch, err = f.api.m3Apply(ctx, f.agentID, action, input, 300)
		if err != nil && retry(err) {
			t.Logf("%s: %v; trying again", action, err)
		}
		return err
	})
	if err != nil {
		return err
	}
	if ch, err = f.api.m3dDockConfirm(ctx, ch.ID); err != nil {
		return fmt.Errorf("confirm %s: %w", action, err)
	}
	if ch.State != core.StateConfirmed {
		return fmt.Errorf("change %s of %s is %s, want %s: %s", ch.ID, action, ch.State, core.StateConfirmed, m3JSON(ch.Error))
	}
	return nil
}

// m3dDockTry applies and confirms the action like m3dDockApply but only logs
// a failure, for the cleanups that undo a scenario. A background change, such
// as the reconcile that follows a container's removal, can briefly hold the
// firewall lock or reload UFW under a plan, so those refusals are tried again
// (m3dxCleanupRetryable).
func m3dDockTry(ctx context.Context, t *testing.T, f *fleet, action string, input any) {
	if err := m3dDockSettle(ctx, t, f, action, input, m3dxCleanupRetryable); err != nil {
		t.Logf("cleanup: %s: %v", action, err)
	}
}

// m3dDockRefused plans the action on the agent host and requires a refusal
// with the code given (any code when it is empty) whose detail names every
// want.
func m3dDockRefused(ctx context.Context, t *testing.T, f *fleet, action string, input any, code string, want ...string) {
	t.Helper()
	_, err := f.api.m3Plan(ctx, f.agentID, action, input)
	if err := m3dxDockRefusal(err, code, want...); err != nil {
		t.Fatalf("%s: %v", action, err)
	}
}

// m3dDockEnable enables the integration on the agent host and waits until it
// is healthy. It first requires a host where the integration is off, and
// registers the cleanup that disables it again. When an earlier scenario left
// the integration enabled, it disables it first, so that one failed cleanup
// does not fail every later scenario.
func m3dDockEnable(ctx context.Context, t *testing.T, f *fleet) {
	t.Helper()
	st := m3dDockWait(ctx, t, f.api, f.agentID, time.Minute, func(st m3dDockStatus) error {
		if !st.Available || !st.Supported {
			return fmt.Errorf("harness: docker available %t, supported %t (%q); want it available and supported", st.Available, st.Supported, st.Unsupported)
		}
		return nil
	})
	if st.Enabled {
		t.Logf("the integration is still enabled (health %q, issues %+v); disabling it first", st.Health, st.Issues)
		if err := m3dDockSettle(ctx, t, f, "firewall.docker.disable", map[string]any{}, m3dxRefused); err != nil {
			t.Fatalf("disable the integration an earlier scenario left enabled: %v", err)
		}
	}
	m3dDockWait(ctx, t, f.api, f.agentID, time.Minute, func(st m3dDockStatus) error {
		if !st.Available || !st.Supported || st.Enabled {
			return fmt.Errorf("harness: docker available %t, supported %t (%q), enabled %t; want it available, supported and not enabled", st.Available, st.Supported, st.Unsupported, st.Enabled)
		}
		return nil
	})
	t.Cleanup(func() {
		st, err := f.api.m3dDockRead(ctx, f.agentID)
		if err != nil {
			t.Logf("cleanup: read firewall.docker: %v", err)
			return
		}
		if st.Enabled {
			m3dDockTry(ctx, t, f, "firewall.docker.disable", map[string]any{})
		}
	})
	m3dDockApply(ctx, t, f, "firewall.docker.enable", map[string]any{})
	m3dDockWait(ctx, t, f.api, f.agentID, 2*time.Minute, m3dxDockHealth("ok"))
}

// m3dDockSave returns the filter table of v for family v4 or v6.
func m3dDockSave(ctx context.Context, t *testing.T, v *vm.VM, family string) string {
	t.Helper()
	switch family {
	case "v4":
		return m3Run(ctx, t, v, "sudo iptables-save -t filter")
	case "v6":
		return m3Run(ctx, t, v, "sudo ip6tables-save -t filter")
	}
	t.Fatalf("harness: family %q is neither v4 nor v6", family)
	return ""
}

// m3dDockRemove removes the containers named on v and ignores errors.
func m3dDockRemove(ctx context.Context, v *vm.VM, names ...string) {
	_, _ = v.Run(ctx, "sudo docker rm -f "+m3dxShell(names...))
}

// m3dDockReach opens a TCP connection from v to host:port. host must be an
// IP address, which is what makes it safe inside the quoted command.
func m3dDockReach(ctx context.Context, v *vm.VM, host string, port int) error {
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return fmt.Errorf("harness: reach %q: %w", host, err)
	}
	_, err = v.Run(ctx, fmt.Sprintf("timeout 5 bash -c '</dev/tcp/%s/%d'", addr, port))
	return err
}

// m3dDockFamilies are the families whose rules the scenarios compare.
var m3dDockFamilies = []string{"v4", "v6"}

// m3dDockRequireLoaded requires the block to be loaded in both families.
func m3dDockRequireLoaded(ctx context.Context, t *testing.T, f *fleet) {
	t.Helper()
	for _, fam := range m3dDockFamilies {
		if err := m3dxDockBlockLoaded(m3dDockSave(ctx, t, f.agent, fam)); err != nil {
			t.Errorf("%s: %v", fam, err)
		}
	}
}

// m3dDockRequireCleaned requires the cleanup to have run in both families.
func m3dDockRequireCleaned(ctx context.Context, t *testing.T, f *fleet, wantReturn bool) {
	t.Helper()
	for _, fam := range m3dDockFamilies {
		if err := m3dxDockCleaned(m3dDockSave(ctx, t, f.agent, fam), nil, wantReturn); err != nil {
			t.Errorf("%s: %v", fam, err)
		}
	}
}

// m3dDockForeign inserts the foreign DOCKER-USER rule text at position 1 of
// v; the cleanup, registered first, deletes it.
func m3dDockForeign(ctx context.Context, t *testing.T, v *vm.VM, text string) {
	t.Helper()
	t.Cleanup(func() { _, _ = v.Run(ctx, "sudo iptables -D DOCKER-USER "+text) })
	m3Run(ctx, t, v, "sudo iptables -I DOCKER-USER 1 "+text)
}

// m3dDockNamed accepts a status whose foreign rules include one naming text.
func m3dDockNamed(text string) func(m3dDockStatus) error {
	return func(st m3dDockStatus) error {
		if !slices.ContainsFunc(st.ForeignRules, func(r string) bool { return strings.Contains(r, text) }) {
			return fmt.Errorf("harness: foreign rules %q, want one naming %s", st.ForeignRules, text)
		}
		return nil
	}
}

// m3dDockBroken accepts an enabled integration that is broken by a foreign
// rule naming text.
func m3dDockBroken(text string) func(m3dDockStatus) error {
	return func(st m3dDockStatus) error {
		if err := m3dxDockHealth("broken")(st); err != nil {
			return err
		}
		return m3dDockNamed(text)(st)
	}
}

// m3dDockSetup runs cmd on v after registering undo, which runs when the
// test ends and ignores errors.
func m3dDockSetup(ctx context.Context, t *testing.T, v *vm.VM, cmd, undo string) {
	t.Helper()
	t.Cleanup(func() { _, _ = v.Run(ctx, undo) })
	m3Run(ctx, t, v, cmd)
}

// m3dDockDetect (spec 2.6 Detection): the agent host runs a supported Docker
// with the integration off and no Darkpanel chain or foreign rule loaded.
func m3dDockDetect(ctx context.Context, t *testing.T, f *fleet) {
	m3dDockWait(ctx, t, f.api, f.agentID, time.Minute, m3dxDockDetected)
	for _, fam := range m3dDockFamilies {
		if err := m3dxDockCleaned(m3dDockSave(ctx, t, f.agent, fam), nil, false); err != nil {
			t.Errorf("%s: %v", fam, err)
		}
	}
}

// m3dDockRefuseUser (adoption): a foreign DOCKER-USER rule makes enable
// refuse, and enable plans once the rule is gone.
func m3dDockRefuseUser(ctx context.Context, t *testing.T, f *fleet) {
	const foreign = "-s 192.0.2.77/32 -j DROP"
	m3dDockForeign(ctx, t, f.agent, foreign)
	m3dDockWait(ctx, t, f.api, f.agentID, time.Minute, m3dDockNamed("192.0.2.77"))
	m3dDockRefused(ctx, t, f, "firewall.docker.enable", map[string]any{}, "firewall.docker_user_in_use", "192.0.2.77")
	m3Run(ctx, t, f.agent, "sudo iptables -D DOCKER-USER "+foreign)
	m3dDockWait(ctx, t, f.api, f.agentID, time.Minute, func(st m3dDockStatus) error {
		if len(st.ForeignRules) > 0 {
			return fmt.Errorf("harness: foreign rules %q, want none", st.ForeignRules)
		}
		return nil
	})
	p, err := f.api.m3Plan(ctx, f.agentID, "firewall.docker.enable", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	diff := p.m3DiffText()
	for _, want := range []string{"docker integration on", "after.rules: Docker block written"} {
		if !strings.Contains(diff, want) {
			t.Errorf("the plan of docker.enable lacks %q:\n%s", want, diff)
		}
	}
	switch g, ok := p.m3Lockout(); {
	case !ok:
		t.Errorf("the plan of docker.enable has no firewall.lockout guard: %s", m3JSON(p.Guards))
	case !g.OK:
		t.Errorf("firewall.lockout failed on docker.enable: %s", g.Message)
	}
}

// m3dDockForeignAfterEnable (adoption): a foreign rule added to an enabled
// integration breaks it and refuses every change until it is discarded.
func m3dDockForeignAfterEnable(ctx context.Context, t *testing.T, f *fleet) {
	const foreign = "-s 192.0.2.78/32 -j DROP"
	m3dDockEnable(ctx, t, f)
	m3dDockForeign(ctx, t, f.agent, foreign)
	m3dDockWait(ctx, t, f.api, f.agentID, time.Minute, m3dDockBroken("192.0.2.78"))
	const inUse = "firewall.docker_user_in_use"
	m3dDockRefused(ctx, t, f, "firewall.rule.add", m3PortRule(8101), inUse, "192.0.2.78")
	m3dDockRefused(ctx, t, f, "firewall.docker.set-sources", map[string]any{"forwardExemptions": []m3dDockExemption{}}, inUse, "192.0.2.78")
	m3dDockRefused(ctx, t, f, "firewall.docker.repair", map[string]any{}, inUse, "192.0.2.78")
	m3dDockRefused(ctx, t, f, "firewall.docker.repair", map[string]any{"discardForeign": true}, "")
	discard := map[string]any{"discardForeign": true, "manual": true}
	p, err := f.api.m3Plan(ctx, f.agentID, "firewall.docker.repair", discard)
	if err != nil {
		t.Fatal(err)
	}
	if diff := p.m3DiffText(); !strings.Contains(diff, "192.0.2.78") {
		t.Errorf("the plan of docker.repair does not name the rule it discards:\n%s", diff)
	}
	m3dDockApply(ctx, t, f, "firewall.docker.repair", discard)
	m3dDockWait(ctx, t, f.api, f.agentID, 2*time.Minute, m3dxDockHealth("ok"))
	if rules := m3dxDockRulesOf(m3dDockSave(ctx, t, f.agent, "v4"), "DOCKER-USER"); slices.Contains(rules, foreign) {
		t.Errorf("the repair left the foreign rule in DOCKER-USER: %q", rules)
	}
}

// m3dDockForeignForward (spec 2.6 non-Docker forwarding): a DNAT forward to a
// network namespace refuses enable until an exemption covers it, and still
// works after the integration is on.
func m3dDockForeignForward(ctx context.Context, t *testing.T, f *fleet) {
	a := f.agent
	m3dDockSetup(ctx, t, a, "sudo ip netns add m3dfwd", "sudo ip netns del m3dfwd")
	m3dDockSetup(ctx, t, a, "sudo ip link add m3dfv0 type veth peer name m3dfv1 netns m3dfwd", "sudo ip link del m3dfv0")
	m3dDockSetup(ctx, t, a, "sudo ip addr add 192.0.2.49/30 dev m3dfv0", "sudo ip addr del 192.0.2.49/30 dev m3dfv0")
	m3dDockSetup(ctx, t, a, "sudo ip link set m3dfv0 up", "sudo ip link set m3dfv0 down")
	m3dDockSetup(ctx, t, a, "sudo ip -n m3dfwd addr add 192.0.2.50/30 dev m3dfv1", "sudo ip -n m3dfwd addr del 192.0.2.50/30 dev m3dfv1")
	m3dDockSetup(ctx, t, a, "sudo ip -n m3dfwd link set m3dfv1 up", "sudo ip -n m3dfwd link set m3dfv1 down")
	m3dDockSetup(ctx, t, a, "sudo ip -n m3dfwd link set lo up", "sudo ip -n m3dfwd link set lo down")
	m3dDockSetup(ctx, t, a, "sudo ip -n m3dfwd route add default via 192.0.2.49", "sudo ip -n m3dfwd route del default via 192.0.2.49")
	m3dDockSetup(ctx, t, a, "sudo systemd-run --unit=m3d-fwd --collect ip netns exec m3dfwd /usr/bin/python3 -m http.server 9100 --bind 192.0.2.50",
		"sudo systemctl stop m3d-fwd")
	dnat := "-d " + agentIPv4 + " -p tcp --dport 9100 -j DNAT --to-destination 192.0.2.50:9100"
	m3dDockSetup(ctx, t, a, "sudo iptables -t nat -I PREROUTING "+dnat, "sudo iptables -t nat -D PREROUTING "+dnat)
	m3dDockSetup(ctx, t, a, "sudo ufw route allow proto tcp to 192.0.2.50 port 9100", "sudo ufw route delete allow proto tcp to 192.0.2.50 port 9100")

	if got := strings.TrimSpace(m3Run(ctx, t, a, "sudo sysctl -n net.ipv4.ip_forward")); got != "1" {
		t.Fatalf("net.ipv4.ip_forward is %q, want 1", got)
	}
	reach := func() {
		t.Helper()
		err := eventually(ctx, 30*time.Second, func(ctx context.Context) error { return m3dDockReach(ctx, f.scanner, agentIPv4, 9100) })
		if err != nil {
			t.Fatalf("the scanner does not reach the forward %s:9100: %v", agentIPv4, err)
		}
	}
	reach()

	want := []m3dDockExemption{{OrigDestination: agentIPv4, OrigPort: 9100, Destination: "192.0.2.50", Proto: "tcp", Port: 9100}}
	m3dDockWait(ctx, t, f.api, f.agentID, time.Minute, func(st m3dDockStatus) error {
		if !slices.Equal(st.ForeignForwards, want) {
			return fmt.Errorf("harness: foreign forwards %+v, want %+v", st.ForeignForwards, want)
		}
		return nil
	})
	m3dDockRefused(ctx, t, f, "firewall.docker.enable", map[string]any{}, "", "192.0.2.50")

	t.Cleanup(func() {
		m3dDockTry(ctx, t, f, "firewall.docker.set-sources", map[string]any{"forwardExemptions": []m3dDockExemption{}})
	})
	m3dDockApply(ctx, t, f, "firewall.docker.set-sources", map[string]any{"forwardExemptions": want})
	m3dDockWait(ctx, t, f.api, f.agentID, time.Minute, func(st m3dDockStatus) error {
		if !slices.Equal(st.Settings.ForwardExemptions, want) || len(st.ForeignForwards) > 0 {
			return fmt.Errorf("harness: exemptions %+v and foreign forwards %+v, want the exemptions %+v and no foreign forward",
				st.Settings.ForwardExemptions, st.ForeignForwards, want)
		}
		return nil
	})

	m3dDockEnable(ctx, t, f)
	save := m3dDockSave(ctx, t, a, "v4")
	if err := m3dxDockBlockLoaded(save); err != nil {
		t.Error(err)
	}
	if err := m3dxDockExemptionLine(save, want[0]); err != nil {
		t.Error(err)
	}
	reach()
}

// m3dDockExemptionOverlap (spec 2.6): an exemption whose destination lies in
// a Docker network is refused.
func m3dDockExemptionOverlap(ctx context.Context, t *testing.T, f *fleet) {
	m3dDockEnable(ctx, t, f)
	st, err := f.api.m3dDockRead(ctx, f.agentID)
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(st.Networks, func(n m3dDockNetwork) bool { return n.Name == "bridge" })
	if i < 0 || len(st.Networks[i].Subnets) == 0 {
		t.Fatalf("docker has no network bridge with a subnet: %+v", st.Networks)
	}
	addr, err := m3dxDockInSubnet(st.Networks[i].Subnets[0])
	if err != nil {
		t.Fatal(err)
	}
	overlap := []m3dDockExemption{{OrigDestination: agentIPv4, OrigPort: 9102, Destination: addr, Proto: "tcp", Port: 9102}}
	m3dDockRefused(ctx, t, f, "firewall.docker.set-sources", map[string]any{"forwardExemptions": overlap}, "firewall.unsupported_integration", "bridge")
}

// m3dDockOpenIncoming (spec 2.6, the incoming-policy invariant): the
// integration cannot be enabled while incoming traffic is accepted, nor
// incoming traffic accepted while it is enabled.
func m3dDockOpenIncoming(ctx context.Context, t *testing.T, f *fleet) {
	got := strings.TrimSpace(m3Run(ctx, t, f.agent, "sudo sed -n 's/^DEFAULT_INPUT_POLICY=//p' /etc/default/ufw"))
	if got != `"DROP"` {
		t.Fatalf("DEFAULT_INPUT_POLICY is %s, want \"DROP\"", got)
	}
	t.Cleanup(func() { _, _ = f.agent.Run(ctx, "sudo ufw default deny incoming") })
	m3dDockApply(ctx, t, f, "firewall.defaults.set", map[string]any{"incoming": "ACCEPT"})
	m3dDockRefused(ctx, t, f, "firewall.docker.enable", map[string]any{}, "firewall.unsupported_integration")
	m3dDockApply(ctx, t, f, "firewall.defaults.set", map[string]any{"incoming": "DROP"})
	m3dDockEnable(ctx, t, f)
	m3dDockRefused(ctx, t, f, "firewall.defaults.set", map[string]any{"incoming": "ACCEPT"}, "firewall.unsupported_integration")
}

// m3dDockDisableCleanup (cleanup of loaded chains): disabling the integration
// and stopping UFW both unload the block, and starting UFW loads it again.
func m3dDockDisableCleanup(ctx context.Context, t *testing.T, f *fleet) {
	m3dDockEnable(ctx, t, f)
	m3dDockRequireLoaded(ctx, t, f)
	m3dDockApply(ctx, t, f, "firewall.docker.disable", map[string]any{})
	m3dDockWait(ctx, t, f.api, f.agentID, time.Minute, func(st m3dDockStatus) error {
		if st.Enabled || st.Health != "off" {
			return fmt.Errorf("harness: docker integration enabled %t with health %q, want it not enabled and off", st.Enabled, st.Health)
		}
		return nil
	})
	m3dDockRequireCleaned(ctx, t, f, true)
	if out := m3Run(ctx, t, f.agent, "sudo grep -l 'BEGIN DARKPANEL docker' /etc/ufw/after.rules /etc/ufw/after6.rules || true"); strings.TrimSpace(out) != "" {
		t.Errorf("the files still hold the block after the disable:\n%s", out)
	}

	m3dDockEnable(ctx, t, f)
	m3dDockApply(ctx, t, f, "firewall.state.disable", map[string]any{})
	m3dDockRequireCleaned(ctx, t, f, true)
	m3dDockWait(ctx, t, f.api, f.agentID, time.Minute, func(st m3dDockStatus) error {
		if !st.Enabled {
			return errors.New("harness: the integration flag was cleared by firewall.state.disable")
		}
		return nil
	})
	if out := m3Run(ctx, t, f.agent, "sudo ufw status"); !strings.HasPrefix(out, "Status: inactive") {
		t.Errorf("ufw status after state.disable:\n%s", out)
	}
	m3dDockApply(ctx, t, f, "firewall.state.enable", map[string]any{})
	m3dDockWait(ctx, t, f.api, f.agentID, 2*time.Minute, m3dxDockHealth("ok"))
	m3dDockRequireLoaded(ctx, t, f)
}

// m3dDockRoutesSurvive (tagged and manual route rules): a container's tagged
// route rule and a manual route rule stay as they are across ufw reload and a
// stop and start of UFW.
func m3dDockRoutesSurvive(ctx context.Context, t *testing.T, f *fleet) {
	m3dDockEnable(ctx, t, f)
	t.Cleanup(func() { m3dDockRemove(ctx, f.agent, "m3d-rt") })
	m3dRunTCP(ctx, t, f.agent, "m3d-rt", "bridge", "18101:14000")
	m3dWaitRunning(ctx, t, f.agent, "m3d-rt")
	m3dDockApply(ctx, t, f, "firewall.container.allow", map[string]any{"binding": m3dDockBinding{Selector: "m3d-rt", HostPort: 18101, Proto: "tcp"}})
	t.Cleanup(func() {
		st, err := f.api.m3dDockRead(ctx, f.agentID)
		if err != nil {
			t.Logf("cleanup: read firewall.docker: %v", err)
			return
		}
		for _, r := range st.Rules {
			if r.Binding.Selector == "m3d-rt" {
				m3dDockTry(ctx, t, f, "firewall.container.remove", map[string]any{"id": r.ID})
			}
		}
	})
	m3dDockSetup(ctx, t, f.agent, "sudo ufw route allow proto tcp from 192.0.2.0/24 to any port 9099",
		"sudo ufw route delete allow proto tcp from 192.0.2.0/24 to any port 9099")
	st := m3WaitStatus(ctx, t, f.api, f.agentID, time.Minute, m3Healthy)
	before := m3dxDockRoutes(st.Rules)
	if len(before) != 2 || !slices.ContainsFunc(before, func(r string) bool { return strings.Contains(r, " darkpanel ") && strings.Contains(r, "ctr ") }) ||
		!slices.ContainsFunc(before, func(r string) bool { return strings.Contains(r, " manual ") }) {
		t.Fatalf("the route rules are %q, want the container's tagged rule and the manual one", before)
	}
	same := func(step string) {
		t.Helper()
		m3WaitStatus(ctx, t, f.api, f.agentID, time.Minute, func(st m3Status) error {
			if err := m3Healthy(st); err != nil {
				return err
			}
			if got := m3dxDockRoutes(st.Rules); !slices.Equal(got, before) {
				return fmt.Errorf("after %s the route rules are\n%q\nwant\n%q", step, got, before)
			}
			return nil
		})
	}
	m3Run(ctx, t, f.agent, "sudo ufw reload")
	same("ufw reload")
	m3dDockApply(ctx, t, f, "firewall.state.disable", map[string]any{})
	m3dDockApply(ctx, t, f, "firewall.state.enable", map[string]any{})
	same("state.disable and state.enable")
	m3dDockRequireLoaded(ctx, t, f)
}

// m3dDockForeignWindow (spec 2.6 foreign rules and revert): a foreign rule
// added while a disable is pending does not stop its revert.
func m3dDockForeignWindow(ctx context.Context, t *testing.T, f *fleet) {
	const foreign = "-s 192.0.2.79/32 -j DROP"
	m3dDockEnable(ctx, t, f)
	ch, err := f.api.m3Apply(ctx, f.agentID, "firewall.docker.disable", map[string]any{}, 120)
	m3RequirePending(t, ch, err)
	m3dDockForeign(ctx, t, f.agent, foreign)
	if _, err := f.api.m3Revert(ctx, ch.ID); err != nil {
		t.Fatal(err)
	}
	m3WaitChange(ctx, t, f.api, ch.ID, core.StateReverted, 2*time.Minute)
	m3dDockWait(ctx, t, f.api, f.agentID, 2*time.Minute, m3dDockBroken("192.0.2.79"))
	save := m3dDockSave(ctx, t, f.agent, "v4")
	if err := m3dxDockBlockLoaded(save); err != nil {
		t.Error(err)
	}
	if rules := m3dxDockRulesOf(save, "DOCKER-USER"); !slices.Contains(rules, foreign) {
		t.Errorf("the revert did not keep the foreign rule in DOCKER-USER: %q", rules)
	}
	m3Run(ctx, t, f.agent, "sudo iptables -D DOCKER-USER "+foreign)
	m3dDockWait(ctx, t, f.api, f.agentID, 2*time.Minute, m3dxDockHealth("ok"))
}

// m3dDockStallWrapper replaces /usr/sbin/@NAME@: once /run/m3d-stall exists,
// the next run takes the file away, announces itself in /run/m3d-stalled and
// waits for /run/m3d-abort before it fails without loading anything. Any other
// run is the program the diversion moved to @NAME@.distrib.
const m3dDockStallWrapper = `#!/bin/bash
if [ -e /run/m3d-stall ]; then
	rm -f /run/m3d-stall
	touch /run/m3d-stalled
	while [ ! -e /run/m3d-abort ]; do
		sleep 1
	done
	exit 1
fi
exec -a @NAME@ /usr/sbin/@NAME@.distrib "$@"
`

// m3dDockForeignCleanup (spec 2.6, an interrupted cleanup): a foreign rule
// added while the cleanup of a disable is stalled, and an agent killed
// there, still lets startup recovery revert the change.
func m3dDockForeignCleanup(ctx context.Context, t *testing.T, f *fleet) {
	const foreign = "-s 192.0.2.80/32 -j DROP"
	m3dDockEnable(ctx, t, f)
	st, err := f.api.m3dDockRead(ctx, f.agentID)
	if err != nil {
		t.Fatal(err)
	}
	if st.DockerVariant != "nft" && st.DockerVariant != "legacy" {
		t.Fatalf("docker variant %q, want nft or legacy", st.DockerVariant)
	}
	for _, name := range []string{"iptables-" + st.DockerVariant + "-restore", "ip6tables-" + st.DockerVariant + "-restore"} {
		path := "/usr/sbin/" + name
		t.Cleanup(func() {
			_, _ = f.agent.Run(ctx, "sudo sh -c 'if [ -e "+path+".distrib ]; then rm -f "+path+" && dpkg-divert --local --rename --remove "+path+"; fi'")
		})
		m3Run(ctx, t, f.agent, "sudo dpkg-divert --local --rename --add "+path)
		script := strings.ReplaceAll(m3dDockStallWrapper, "@NAME@", name)
		if out, err := f.agent.RunInput(ctx, "sudo tee "+path+" >/dev/null && sudo chmod 0755 "+path, strings.NewReader(script)); err != nil {
			t.Fatalf("install the wrapper %s: %v\n%s", path, err, out)
		}
	}
	t.Cleanup(func() {
		if t.Failed() {
			out, _ := f.agent.Run(ctx, "sudo journalctl -u darkpanel-agent --no-pager -o short-iso | tail -150")
			t.Logf("agent journal:\n%s", out)
		}
	})
	t.Cleanup(func() { _, _ = f.agent.Run(ctx, "sudo rm -f /run/m3d-stall /run/m3d-stalled /run/m3d-abort") })
	m3Run(ctx, t, f.agent, "sudo touch /run/m3d-stall")
	// The wait is registered once the goroutine is certain to start, so that
	// no failure can leave it blocked on a channel nothing closes, and the
	// release after it so that the release runs first and unblocks the apply.
	applied := make(chan struct{})
	t.Cleanup(func() { <-applied })
	t.Cleanup(func() {
		_, _ = f.agent.Run(ctx, "sudo sh -c 'touch /run/m3d-abort; sleep 2; rm -f /run/m3d-stall /run/m3d-stalled /run/m3d-abort'")
	})
	go func() {
		defer close(applied)
		_, _ = f.api.m3Apply(ctx, f.agentID, "firewall.docker.disable", map[string]any{}, 120)
	}()
	err = eventually(ctx, 2*time.Minute, func(ctx context.Context) error {
		_, err := f.agent.Run(ctx, "test -e /run/m3d-stalled")
		return err
	})
	if err != nil {
		t.Fatalf("the cleanup of the disable never reached its restore load: %v", err)
	}
	m3dDockForeign(ctx, t, f.agent, foreign)
	if err := f.agent.KillProcess(ctx, "darkpanel"); err != nil {
		t.Fatal(err)
	}
	m3Run(ctx, t, f.agent, "sudo touch /run/m3d-abort")
	waitHosts(ctx, t, f, 3*time.Minute, hostsAt(versionN, f.agentID))
	m3dDockWait(ctx, t, f.api, f.agentID, 4*time.Minute, func(st m3dDockStatus) error {
		if !st.Enabled {
			return errors.New("harness: the integration is not enabled")
		}
		return m3dDockNamed("192.0.2.80")(st)
	})
	save := m3dDockSave(ctx, t, f.agent, "v4")
	if err := m3dxDockBlockLoaded(save); err != nil {
		t.Error(err)
	}
	if rules := m3dxDockRulesOf(save, "DOCKER-USER"); !slices.Contains(rules, foreign) {
		t.Errorf("the recovery did not keep the foreign rule in DOCKER-USER: %q", rules)
	}
	m3Run(ctx, t, f.agent, "sudo iptables -D DOCKER-USER "+foreign)
	m3dDockWait(ctx, t, f.api, f.agentID, 2*time.Minute, m3dxDockHealth("ok"))
}

// m3dDockIPv6NoRules (IPV6=no with Docker's IPv6 rules loaded): starting UFW
// with IPV6=no would remove Docker's IPv6 chains, so it is refused.
func m3dDockIPv6NoRules(ctx context.Context, t *testing.T, f *fleet) {
	t.Cleanup(func() { _, _ = f.agent.Run(ctx, "sudo docker network rm m3dv6") })
	m3dDocker(ctx, t, f.agent, "network", "create", "--ipv6", "--subnet", "fd00:3d::/64", "m3dv6")
	if out := m3Run(ctx, t, f.agent, "sudo ip6tables-save -t filter"); !strings.Contains(out, "DOCKER") {
		t.Fatalf("Docker's IPv6 rules are not loaded after creating an IPv6 network:\n%s", out)
	}
	m3StoppedIPv6No(ctx, t, f)
	m3RequireUnsupported(ctx, t, f, "firewall.state.enable", map[string]any{}, "DOCKER")
}
