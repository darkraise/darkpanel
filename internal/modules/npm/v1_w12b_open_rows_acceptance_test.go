package npm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/npm/fake"
	"github.com/darkraise/darkpanel/internal/modules/npm/npmapi"
)

func a9NotFound(_ context.Context, host string) ([]netip.Addr, error) {
	return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
}

func a9Timeout(host string) error {
	return &net.DNSError{Err: "i/o timeout", Name: host, IsTimeout: true}
}

// a9Counter counts the lookups of each name and answers them with answer.
type a9Counter struct {
	mu     sync.Mutex
	counts map[string]int
	answer func(ctx context.Context, host string) ([]netip.Addr, error)
}

func (c *a9Counter) resolve(ctx context.Context, host string) ([]netip.Addr, error) {
	c.mu.Lock()
	if c.counts == nil {
		c.counts = map[string]int{}
	}
	c.counts[host]++
	c.mu.Unlock()
	return c.answer(ctx, host)
}

func (c *a9Counter) snapshot() map[string]int {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := map[string]int{}
	for k, v := range c.counts {
		out[k] = v
	}
	return out
}

func a9Seed(h *thHost) {
	for range 3 {
		w12aSeed(h, "dup.example.net", 3000,
			npmapi.Location{Path: "/a", ForwardScheme: "http", ForwardHost: "dup.example.net", ForwardPort: 3001},
			npmapi.Location{Path: "/b", ForwardScheme: "http", ForwardHost: "other.example.net", ForwardPort: 3002})
	}
	w12aSeed(h, "other.example.net", 3003)
}

func a9Once(t *testing.T, got map[string]int) {
	t.Helper()
	for _, name := range []string{"dup.example.net", "other.example.net"} {
		if got[name] != 1 {
			t.Errorf("%s was resolved %d times, want once (all lookups %v)", name, got[name], got)
		}
	}
	for name, n := range got {
		if n != 1 {
			t.Errorf("%s was resolved %d times, want once", name, n)
		}
	}
}

func TestA9_ANameNamedByManyHostsAndLocationsIsResolvedOnceByAnAttachCheck(t *testing.T) {
	h, user := nwSetup(t)
	w12aSubnet(t, h, user)
	a9Seed(h)
	n := h.nm()
	c := &a9Counter{answer: a9NotFound}
	n.d.resolve = c.resolve
	in := nwJSON(t, AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web"})
	if _, err := n.networkAttach().Handler.Plan(context.Background(), h.env(""), in); err != nil {
		t.Fatalf("plan: %v", err)
	}
	a9Once(t, c.snapshot())
}

func TestA9_ANameNamedByManyHostsAndLocationsIsResolvedOnceByAnUpgradeCheck(t *testing.T) {
	s := w12aUpSetup(t)
	a9Seed(s.h)
	c := &a9Counter{answer: a9NotFound}
	s.n.d.resolve = c.resolve
	if _, err := s.n.instanceUpgrade().Handler.Plan(context.Background(), s.h.env(""), json.RawMessage(`{}`)); err != nil {
		t.Fatalf("plan: %v", err)
	}
	a9Once(t, c.snapshot())
}

func TestA9_TwentyNamesThatEachTakeTheFullTimeoutEndWithinTheTimeoutPlusOneSecond(t *testing.T) {
	h, user := nwSetup(t)
	w12aSubnet(t, h, user)
	for i := range 20 {
		w12aSeed(h, fmt.Sprintf("slow%02d.example.net", i), 3000)
	}
	n := h.nm()
	c := &a9Counter{answer: func(ctx context.Context, _ string) ([]netip.Addr, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	n.d.resolve = c.resolve
	in := nwJSON(t, AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web"})
	start := time.Now()
	_, err := n.networkAttach().Handler.Plan(context.Background(), h.env(""), in)
	took := time.Since(start)
	if err == nil {
		t.Error("the check passed, want a refusal for names that never answered")
	}
	// The deadline is the limit itself, so a check that ends on it ends a few milliseconds past it; one that resolved
	// the names eight at a time would take 15 seconds.
	if limit := resolveTimeout + time.Second + 500*time.Millisecond; took > limit {
		t.Errorf("the check took %s, want about %s", took, resolveTimeout+time.Second)
	}
	for name, count := range c.snapshot() {
		if count != 1 {
			t.Errorf("%s was resolved %d times, want once", name, count)
		}
	}
}

func a10Log(err error) string {
	var logs bytes.Buffer
	slog.New(slog.NewTextHandler(&logs, nil)).Error("api: request failed", "code", errcode.From(err).Code, "err", err)
	return logs.String()
}

type a10Class struct {
	name  string
	fault fake.Fault
	text  string
}

func a10Classes() []a10Class {
	return []a10Class{
		{"no answer", fake.Fault{AfterCommit: true}, "Nginx Proxy Manager did not answer"},
		{"an HTTP status", fake.Fault{Status: http.StatusInternalServerError}, "Nginx Proxy Manager answered HTTP 500"},
		{"a refused login", fake.Fault{Status: http.StatusUnauthorized}, "Nginx Proxy Manager answered HTTP 401"},
		{"an answer that does not decode", fake.Fault{Status: http.StatusOK, Body: "{"}, "Nginx Proxy Manager gave an answer that did not decode"},
	}
}

func a10Queue(h *thHost, f fake.Fault) {
	for range 4 {
		h.npm.Fail(http.MethodGet, "/api/nginx/proxy-hosts", f)
	}
}

// a10Check asserts err is npm.unhealthy whose detail carries the class text and none of the other classes', in the
// error value, the log line and the detail a change records.
func a10Check(t *testing.T, err error, c a10Class) {
	t.Helper()
	var coded *errcode.Error
	if !errors.As(err, &coded) || coded.Code != errcode.NPMUnhealthy {
		t.Fatalf("error %v, want code %s", err, errcode.NPMUnhealthy)
	}
	for _, other := range a10Classes() {
		if other.text == c.text {
			continue
		}
		if strings.Contains(coded.Detail, other.text) && !strings.Contains(c.text, other.text) {
			t.Errorf("detail %q carries the text of another class %q", coded.Detail, other.text)
		}
	}
	for what, got := range map[string]string{"detail (the change record)": coded.Detail, "error value": err.Error(), "log line": a10Log(err)} {
		if !strings.Contains(got, c.text) {
			t.Errorf("%s %q does not carry %q", what, got, c.text)
		}
	}
	if strings.Contains(c.text, "HTTP 401") && !strings.Contains(coded.Detail, "401") {
		t.Errorf("detail %q hides the status 401", coded.Detail)
	}
}

func TestA10_AFailedListingMakesAnAttachPlanUnhealthy(t *testing.T) {
	for _, c := range a10Classes() {
		t.Run(c.name, func(t *testing.T) {
			h, user := nwSetup(t)
			w12aSubnet(t, h, user)
			a10Queue(h, c.fault)
			_, err := h.nm().networkAttach().Handler.Plan(context.Background(), h.env(""), nwJSON(t, AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web"}))
			a10Check(t, err, c)
		})
	}
}

func TestA10_AFailedListingMakesAnAttachApplyUnhealthy(t *testing.T) {
	for _, c := range a10Classes() {
		t.Run(c.name, func(t *testing.T) {
			h, user := nwSetup(t)
			w12aSubnet(t, h, user)
			a10Queue(h, c.fault)
			_, err := nwApplyAttach(t, h, "c2", AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web"})
			a10Check(t, err, c)
		})
	}
}

func TestA10_AFailedListingMakesTheUndoOfADetachUnhealthy(t *testing.T) {
	for _, c := range a10Classes() {
		t.Run(c.name, func(t *testing.T) {
			h, user := nwSetup(t)
			w12aSubnet(t, h, user)
			nwSeedAttached(t, h, user, nwClaimant, false)
			if _, err := nwApplyDetach(t, h, "c2", DetachInput{Network: nwNet, Claimant: nwClaimant}); err != nil {
				t.Fatal(err)
			}
			var endpoint core.Effect
			for _, e := range h.journal.effects() {
				if e.Kind == core.EffectModify {
					endpoint = e
				}
			}
			a10Queue(h, c.fault)
			_, err := h.nm().networkDetach().Handler.Inverse(context.Background(), h.env("u1"), endpoint)
			a10Check(t, err, c)
		})
	}
}

func TestA10_AFailedListingMakesAnUpgradeUnhealthyAtPlanAndApply(t *testing.T) {
	for _, c := range a10Classes() {
		t.Run(c.name, func(t *testing.T) {
			s := w12aUpSetup(t)
			a10Queue(s.h, c.fault)
			_, err := s.n.instanceUpgrade().Handler.Plan(context.Background(), s.h.env(""), json.RawMessage(`{}`))
			a10Check(t, err, c)
			_, err = s.apply()
			a10Check(t, err, c)
		})
	}
}

const a11Detail = "forward host %s could not be resolved: the resolver did not answer; retry"

func TestA11_ATransientResolverFailureRefusesAnAttachWithUnavailable(t *testing.T) {
	for _, apply := range []bool{false, true} {
		t.Run(fmt.Sprintf("apply=%v", apply), func(t *testing.T) {
			h, user := nwSetup(t)
			w12aSubnet(t, h, user)
			w12aSeed(h, "flaky.example.net", 3000)
			n := h.nm()
			n.d.resolve = func(_ context.Context, host string) ([]netip.Addr, error) { return nil, a9Timeout(host) }
			in := AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web"}
			var err error
			if apply {
				h.addChange("c2", "npm.network.attach")
				_, err = n.networkAttach().Handler.Apply(context.Background(), h.env("c2"), nwJSON(t, in))
			} else {
				_, err = n.networkAttach().Handler.Plan(context.Background(), h.env(""), nwJSON(t, in))
			}
			a11Check(t, err, "flaky.example.net")
		})
	}
}

func a11Check(t *testing.T, err error, host string) {
	t.Helper()
	want := fmt.Sprintf(a11Detail, host)
	var coded *errcode.Error
	if !errors.As(err, &coded) || coded.Code != errcode.Unavailable || coded.Detail != want {
		t.Fatalf("error %v, want %s %q", err, errcode.Unavailable, want)
	}
	if !strings.Contains(err.Error(), want) || !strings.Contains(a10Log(err), want) {
		t.Errorf("the error value %q or the log line carry no %q", err.Error(), want)
	}
}

func TestA11_ATransientResolverFailureRefusesAnUpgradeWithUnavailable(t *testing.T) {
	s := w12aUpSetup(t)
	w12aSeed(s.h, "flaky.example.net", 3000)
	s.n.d.resolve = func(_ context.Context, host string) ([]netip.Addr, error) { return nil, a9Timeout(host) }
	_, err := s.n.instanceUpgrade().Handler.Plan(context.Background(), s.h.env(""), json.RawMessage(`{}`))
	a11Check(t, err, "flaky.example.net")
	_, err = s.apply()
	a11Check(t, err, "flaky.example.net")
}

func TestA11_ANameThatDoesNotExistIsSkipped(t *testing.T) {
	h, user := nwSetup(t)
	w12aSubnet(t, h, user)
	w12aSeed(h, "gone.example.net", 3000)
	n := h.nm()
	n.d.resolve = a9NotFound
	if _, err := n.networkAttach().Handler.Plan(context.Background(), h.env(""), nwJSON(t, AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web"})); err != nil {
		t.Errorf("attach plan: %v, want a pass", err)
	}
	s := w12aUpSetup(t)
	w12aSeed(s.h, "gone.example.net", 3000)
	s.n.d.resolve = a9NotFound
	if _, err := s.n.instanceUpgrade().Handler.Plan(context.Background(), s.h.env(""), json.RawMessage(`{}`)); err != nil {
		t.Errorf("upgrade plan: %v, want a pass", err)
	}
}

func TestA12_AForwardHostWithAPathIsCheckedByItsHostPart(t *testing.T) {
	h, user := nwSetup(t)
	w12aSubnet(t, h, user)
	w12aSeed(h, "10.9.0.7/x/y", 3000)
	in := nwJSON(t, AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web"})
	_, err := h.nm().networkAttach().Handler.Plan(context.Background(), h.env(""), in)
	var coded *errcode.Error
	if !errors.As(err, &coded) || coded.Code != errcode.NPMReserved || !strings.Contains(coded.Detail, "10.9.0.7") {
		t.Errorf("error %v, want %s naming 10.9.0.7", err, errcode.NPMReserved)
	}

	h2, user2 := nwSetup(t)
	w12aSubnet(t, h2, user2)
	w12aSeed(h2, "named.example.net/p", 3000)
	n := h2.nm()
	c := &a9Counter{answer: func(_ context.Context, _ string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("10.9.0.8")}, nil
	}}
	n.d.resolve = c.resolve
	_, err = n.networkAttach().Handler.Plan(context.Background(), h2.env(""), in)
	if !errors.As(err, &coded) || coded.Code != errcode.NPMReserved || !strings.Contains(coded.Detail, "10.9.0.8") {
		t.Errorf("error %v, want %s naming 10.9.0.8, the answer for the part before the slash", err, errcode.NPMReserved)
	}
	if got := c.snapshot(); got["named.example.net"] != 1 || len(got) != 1 {
		t.Errorf("the resolver was asked %v, want only the host part named.example.net", got)
	}
}

func TestA12_AForwardHostHoldingAnNginxVariableIsNotCounted(t *testing.T) {
	h, user := nwSetup(t)
	w12aSubnet(t, h, user)
	w12aSeed(h, "$upstream", 3000)
	w12aSeed(h, "app-$env.example.net", 3000)
	w12aSeed(h, "x.example.net", 3000, npmapi.Location{Path: "/v", ForwardScheme: "http", ForwardHost: "$backend", ForwardPort: 3001})
	n := h.nm()
	c := &a9Counter{answer: func(_ context.Context, _ string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("10.9.0.7")}, nil
	}}
	n.d.resolve = c.resolve
	if _, err := n.networkAttach().Handler.Plan(context.Background(), h.env(""), nwJSON(t, AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web"})); err != nil {
		var coded *errcode.Error
		if errors.As(err, &coded) && coded.Code == errcode.NPMReserved && !strings.Contains(coded.Detail, "x.example.net") {
			t.Errorf("a variable forward host was counted: %v", err)
		}
	}
	for name := range c.snapshot() {
		if strings.Contains(name, "$") {
			t.Errorf("the resolver was asked for the variable forward host %q", name)
		}
	}
}

func TestA12_TheThreatModelSaysVariableForwardHostsAreNotCounted(t *testing.T) {
	b, err := os.ReadFile("../../../docs/security/threat-model.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		l := strings.ToLower(line)
		if strings.Contains(l, "nginx variable") && strings.Contains(l, "forward") && strings.Contains(l, "not counted") {
			return
		}
	}
	t.Error("docs/security/threat-model.md has no line saying that a forward host holding an nginx variable is not counted")
}

func a13Unhealthy(t *testing.T, err error, where string) {
	t.Helper()
	var coded *errcode.Error
	if !errors.As(err, &coded) || coded.Code != errcode.NPMUnhealthy {
		t.Fatalf("%s: error %v, want %s", where, err, errcode.NPMUnhealthy)
	}
	if s := errcode.NPMUnhealthy.Status(); s < 500 || errcode.NPMUnhealthy.Title() == "" {
		t.Errorf("%s: npm.unhealthy answers %d %q, want a 5xx with a title, which is all a plan caller sees", where, s, errcode.NPMUnhealthy.Title())
	}
	for what, got := range map[string]string{"detail (the change record)": coded.Detail, "error value": err.Error(), "log line": a10Log(err)} {
		l := strings.ToLower(got)
		if !strings.Contains(l, "start nginx proxy manager") && !strings.Contains(l, "redeploy") {
			t.Errorf("%s: %s %q names neither starting Nginx Proxy Manager nor redeploying", where, what, got)
		}
	}
}

func a13Listings(h *thHost) int {
	n := 0
	for _, q := range h.npm.Requests() {
		if q.Method == http.MethodGet && q.Path == "/api/nginx/proxy-hosts" {
			n++
		}
	}
	return n
}

func TestA13_AnUpgradeListsTheProxyHostsOnce(t *testing.T) {
	s := w12aUpSetup(t)
	w12aSeed(s.h, "10.99.0.7", 3000)
	w12aSeed(s.h, "other.example.net", 3000)
	before := a13Listings(s.h)
	if _, err := s.n.instanceUpgrade().Handler.Plan(context.Background(), s.h.env(""), json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if got := a13Listings(s.h) - before; got != 1 {
		t.Errorf("the upgrade plan listed the proxy hosts %d times, want once", got)
	}
}

func TestA13_AnUpgradeChecksTheOldContainersNetworksAndDarkpanelNPM(t *testing.T) {
	ctx := context.Background()
	s := w12aUpSetup(t)
	w12aSeed(s.h, "172.30.0.9", 3000)
	_, err := s.n.instanceUpgrade().Handler.Plan(ctx, s.h.env(""), json.RawMessage(`{}`))
	var coded *errcode.Error
	if !errors.As(err, &coded) || coded.Code != errcode.NPMReserved || !strings.Contains(coded.Detail, "172.30.0.9") || !strings.Contains(coded.Detail, "app-net") {
		t.Errorf("a forward to an address on the old container's own network: %v, want %s naming it and the network", err, errcode.NPMReserved)
	}
	s = w12aUpSetup(t)
	if err := s.h.docker.DisconnectNetwork(ctx, networkName, containerName, true); err != nil {
		t.Fatal(err)
	}
	w12aSeed(s.h, "172.20.0.9", 3000)
	_, err = s.n.instanceUpgrade().Handler.Plan(ctx, s.h.env(""), json.RawMessage(`{}`))
	if !errors.As(err, &coded) || coded.Code != errcode.NPMReserved || !strings.Contains(coded.Detail, "172.20.0.9") || !strings.Contains(coded.Detail, "darkpanel-npm") {
		t.Errorf("a forward to an address on darkpanel-npm, which NPM has left: %v, want %s naming it and the network", err, errcode.NPMReserved)
	}
}

func TestA13_AnUpgradeNeedsNPMsAPIAlsoForAContainerWithNoNetworks(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, s *upSetup)
	}{
		{"on its networks", func(*testing.T, *upSetup) {}},
		{"on no network", func(t *testing.T, s *upSetup) {
			for _, nw := range []string{networkName, upAppNet} {
				if err := s.h.docker.DisconnectNetwork(ctx, nw, containerName, true); err != nil {
					t.Fatal(err)
				}
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := w12aUpSetup(t)
			tc.setup(t, s)
			a10Queue(s.h, fake.Fault{Status: http.StatusInternalServerError})
			_, err := s.n.instanceUpgrade().Handler.Plan(ctx, s.h.env(""), json.RawMessage(`{}`))
			a13Unhealthy(t, err, "plan")
			_, err = s.apply()
			a13Unhealthy(t, err, "apply")
		})
	}
}

func TestA13_AMissingDarkpanelNPMNetworkIsADefiniteNotFoundNamingIt(t *testing.T) {
	ctx := context.Background()
	s := w12aUpSetup(t)
	if err := s.h.docker.DisconnectNetwork(ctx, networkName, containerName, true); err != nil {
		t.Fatal(err)
	}
	if err := s.h.docker.RemoveNetwork(ctx, networkName); err != nil {
		t.Fatal(err)
	}
	for what, call := range map[string]func() error{
		"plan": func() error {
			_, err := s.n.instanceUpgrade().Handler.Plan(ctx, s.h.env(""), json.RawMessage(`{}`))
			return err
		},
		"apply": func() error { _, err := s.apply(); return err },
	} {
		err := call()
		var coded *errcode.Error
		if !errors.As(err, &coded) || coded.Code != errcode.NotFound || !strings.Contains(coded.Detail, "darkpanel-npm") {
			t.Errorf("%s: error %v, want %s naming darkpanel-npm", what, err, errcode.NotFound)
		}
		if !core.IsDefinite(err) {
			t.Errorf("%s: error %v is not Definite", what, err)
		}
		if s := errcode.NotFound.Status(); s < 400 || s > 499 {
			t.Errorf("%s: not_found answers %d, want a 4xx so the name reaches a plan caller", what, s)
		}
	}
}

func TestA14_TheUpgradeRefusalIsCheckedAtPlanAtApplyAndWithNPMStopped(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, s *upSetup)
	}{
		{"NPM running", func(*testing.T, *upSetup) {}},
		{"NPM stopped", func(t *testing.T, s *upSetup) {
			if _, err := s.h.docker.StopContainer(ctx, containerName, 0); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := w12aUpSetup(t)
			tc.setup(t, s)
			a10Queue(s.h, fake.Fault{AfterCommit: true})
			_, err := s.n.instanceUpgrade().Handler.Plan(ctx, s.h.env(""), json.RawMessage(`{}`))
			a13Unhealthy(t, err, "plan")
			_, err = s.apply()
			a13Unhealthy(t, err, "apply")
			if len(s.effects()) != 0 || s.phase() != "no row" || s.dock.counts["StopContainer"] != 0 || len(s.dock.created) != 0 {
				t.Error("a refused upgrade journaled an effect, recorded a phase, stopped NPM or created a container")
			}
		})
	}
}

func TestA15_AForwardToANetworkOrBroadcastAddressDoesNotRefuseAnAttach(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		addr    string
		refused bool
	}{
		{"10.9.0.0", false},
		{"10.9.0.255", false},
		{"10.9.0.1", false},
		{"10.9.0.7", true},
	} {
		t.Run(tc.addr, func(t *testing.T) {
			h, user := nwSetup(t)
			w12aSubnet(t, h, user)
			w12aSeed(h, tc.addr, 3000)
			_, err := h.nm().networkAttach().Handler.Plan(ctx, h.env(""), nwJSON(t, AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web"}))
			var coded *errcode.Error
			switch {
			case tc.refused && (!errors.As(err, &coded) || coded.Code != errcode.NPMReserved):
				t.Errorf("error %v, want %s for a usable address", err, errcode.NPMReserved)
			case !tc.refused && err != nil:
				t.Errorf("error %v, want a pass", err)
			}
		})
	}
}

func TestA15_TheFirstAddressOfAnIPv4Slash31StillRefuses(t *testing.T) {
	h, user := nwSetup(t)
	h.docker.putNetwork(thNetwork(nwNet, thSubnet("10.9.1.0/31", "10.9.1.1")))
	_ = user
	w12aSeed(h, "10.9.1.0", 3000)
	_, err := h.nm().networkAttach().Handler.Plan(context.Background(), h.env(""), nwJSON(t, AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web"}))
	var coded *errcode.Error
	if !errors.As(err, &coded) || coded.Code != errcode.NPMReserved || !strings.Contains(coded.Detail, "10.9.1.0") {
		t.Errorf("error %v, want %s naming 10.9.1.0", err, errcode.NPMReserved)
	}
}

// a17Halt is the unauthenticated NPM client of a deploy whose wait for NPM ends through end.
type a17Halt struct {
	npmAPI
	end func(ctx context.Context) error
}

func (a a17Halt) Health(ctx context.Context) (npmapi.Health, error) {
	return npmapi.Health{}, a.end(ctx)
}

func a17Deploy(ctx context.Context, t *testing.T, end func(context.Context) error) *errcode.Error {
	t.Helper()
	h := newTHHost(t)
	n := h.nm()
	live := n.d.anon()
	n.d.anon = func() npmAPI { return a17Halt{npmAPI: live, end: end} }
	var err error
	dpDrive(t, h, deployPoll, func() {
		_, err = n.instanceDeploy().Handler.Apply(ctx, h.env("c1"), dpInput(dpEmail))
	})
	var coded *errcode.Error
	if !errors.As(err, &coded) {
		t.Fatalf("error %v, want a coded error", err)
	}
	return coded
}

// a17Expiring is a context whose deadline passes when expire is called, so that the deadline falls inside the wait
// however slow the setup before it runs.
type a17Expiring struct {
	context.Context
	mu   sync.Mutex
	done chan struct{}
	err  error
}

func newA17Expiring() *a17Expiring {
	return &a17Expiring{Context: context.Background(), done: make(chan struct{})}
}

func (c *a17Expiring) Done() <-chan struct{} { return c.done }

func (c *a17Expiring) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

func (c *a17Expiring) expire() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err == nil {
		c.err = context.DeadlineExceeded
		close(c.done)
	}
}

func TestA17_ADeployWhoseWaitPassedItsDeadlineSaysItRanOutOfTime(t *testing.T) {
	ctx := newA17Expiring()
	coded := a17Deploy(ctx, t, func(inner context.Context) error {
		ctx.expire()
		<-inner.Done()
		return inner.Err()
	})
	const want = "the deploy ran out of time while it waited for Nginx Proxy Manager to start"
	if !strings.Contains(coded.Detail, want) {
		t.Errorf("detail %q, want it to carry %q", coded.Detail, want)
	}
}

func TestA17_ACancelledWaitKeepsItsDetail(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	coded := a17Deploy(ctx, t, func(context.Context) error {
		cancel()
		return context.Canceled
	})
	const want = "the deploy was cancelled while it waited for Nginx Proxy Manager to answer"
	if !strings.Contains(coded.Detail, want) {
		t.Errorf("detail %q, want it to carry %q", coded.Detail, want)
	}
	if strings.Contains(coded.Detail, "ran out of time") {
		t.Errorf("detail %q calls a cancellation a timeout", coded.Detail)
	}
}

// a18Written is the "/" location's advanced configuration NPM holds after an update of a proxy host whose "/"
// location held operator.
func a18Written(t *testing.T, operator string) string {
	t.Helper()
	h := newTHHost(t)
	h.deployed()
	h.addChange("c0", "npm.admin.expose")
	body := proxyHostBody(puInput(), "")
	body.Locations[0].AdvancedConfig += "\n" + operator
	host := h.npm.SeedProxyHost(puHostOf(body))
	h.addChange("c1", "npm.proxyhost.update")
	next := puInput(func(in *ProxyHostInput) { in.ForwardPort = 9090 })
	if _, err := h.nm().proxyhostUpdate().Handler.Apply(context.Background(), h.env("c1"), puJSON(t, ProxyHostUpdateInput{ID: host.ID, ProxyHostInput: next})); err != nil {
		t.Fatal(err)
	}
	for _, got := range puHosts(t, h) {
		for _, l := range got.Locations {
			if l.Path == "/" {
				return l.AdvancedConfig
			}
		}
	}
	t.Fatal("NPM holds no / location after the update")
	return ""
}

// a18Count counts the lines of cfg that set X-Darkpanel-Client.
func a18Count(cfg string) int {
	n := 0
	for _, l := range strings.Split(cfg, "\n") {
		f := strings.Fields(l)
		if len(f) >= 2 && strings.EqualFold(f[0], "proxy_set_header") && strings.EqualFold(strings.Trim(f[1], `"'`), "x-darkpanel-client") {
			n++
		}
	}
	return n
}

func TestA18_AnOperatorHeaderLineIsReplacedByTheModulesLine(t *testing.T) {
	for _, tc := range []struct {
		name, line string
	}{
		{"the module's own spelling", "proxy_set_header X-Darkpanel-Client $remote_addr;"},
		{"lower case", "proxy_set_header x-darkpanel-client 203.0.113.9;"},
		{"upper case", "PROXY_SET_HEADER X-DARKPANEL-CLIENT 203.0.113.9;"},
		{"indented", "    proxy_set_header X-Darkpanel-Client 203.0.113.9;"},
		{"indented with a tab", "\tproxy_set_header X-Darkpanel-Client 203.0.113.9;"},
		{"a tab between the words", "proxy_set_header\tX-Darkpanel-Client\t203.0.113.9;"},
		{"repeated spaces between the words", "proxy_set_header     X-Darkpanel-Client      203.0.113.9;"},
		{"a double-quoted header name", `proxy_set_header "X-Darkpanel-Client" 203.0.113.9;`},
		{"a single-quoted header name", `proxy_set_header 'x-darkpanel-client' 203.0.113.9;`},
		{"a trailing comment", "proxy_set_header X-Darkpanel-Client 203.0.113.9; # operator"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := a18Written(t, tc.line+"\nallow 10.0.0.0/8;")
			if got := a18Count(cfg); got != 1 {
				t.Errorf("the header is set %d times in %q, want once", got, cfg)
			}
			if !strings.Contains(cfg, "allow 10.0.0.0/8;") {
				t.Errorf("configuration %q lost the operator's other line", cfg)
			}
		})
	}
}

func TestA18_ALineHoldingMoreThanTheDirectiveIsKept(t *testing.T) {
	const line = "proxy_set_header X-Darkpanel-Client 203.0.113.9; }"
	cfg := a18Written(t, line)
	if !strings.Contains(cfg, line) {
		t.Errorf("configuration %q dropped the line %q, which holds more than the directive", cfg, line)
	}
}
