package coreactions

import (
	"context"
	"errors"
	"net/netip"
	"reflect"
	"sync"
	"testing"

	"github.com/darkraise/darkpanel/internal/controller/fleet"
	"github.com/darkraise/darkpanel/internal/controller/listen"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/transport"
)

const hjKey = "tskey-auth-hj-secret"

var _ core.EnrollJoiner = (*hjJoiner)(nil)

// hjJoiner is a module that joins a new host to a network: the tailnet module's part of core.host.create.
type hjJoiner struct {
	defaultOff bool
	diff       []core.DiffLine
	state      string
	planErr    error
	prepareErr error

	mu       sync.Mutex
	plans    []core.JoinRequest
	prepares []core.JoinRequest
	hostSeen []bool // whether the host row existed when PrepareJoin ran
}

func (j *hjJoiner) Manifest() core.Manifest {
	return core.Manifest{ID: "tailnet", Version: 1, DefaultEnabled: !j.defaultOff}
}

func (j *hjJoiner) PlanJoin(_ context.Context, _ core.Env, req core.JoinRequest) ([]core.DiffLine, string, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.plans = append(j.plans, req)
	if j.planErr != nil {
		return nil, "", j.planErr
	}
	return j.diff, j.state, nil
}

func (j *hjJoiner) PrepareJoin(ctx context.Context, env core.Env, req core.JoinRequest) (string, error) {
	var n int
	if err := env.DB.Read().QueryRowContext(ctx, `SELECT count(*) FROM core_hosts WHERE id = ?`, req.HostID).Scan(&n); err != nil {
		return "", err
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.prepares = append(j.prepares, req)
	j.hostSeen = append(j.hostSeen, n > 0)
	if j.prepareErr != nil {
		return "", j.prepareErr
	}
	return hjKey, nil
}

func (j *hjJoiner) calls() (plans, prepares int) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return len(j.plans), len(j.prepares)
}

func newHJJoiner() *hjJoiner {
	return &hjJoiner{diff: []core.DiffLine{{Op: "+", Text: "joiner: a one-use auth key"}}, state: "joiner-state-1"}
}

// hjEnv is the enrollment test env with the joiner as a compiled-in module, the controller on a tailnet and
// the joiner enabled on the local host.
func hjEnv(t *testing.T, j *hjJoiner, advertise string, addrs ...string) *enrollEnv {
	t.Helper()
	out := &enrollEnv{sessions: &fakeSessions{}}
	out.testEnv = newTestEnvWith(t, func(e *testEnv, d *Deps) {
		a, err := fleet.LoadOrCreateAuthority(context.Background(), e.db, coretest.Sealer{}, e.clock)
		if err != nil {
			t.Fatal(err)
		}
		out.authority = a
		d.Authority, d.AgentAdvertise, d.Sessions = a, advertise, out.sessions
	}, fakeModule{"demo", 1}, j)
	out.addHost("h1", "alpha", true)
	out.setModuleState("h1", "tailnet", true, true, "")
	hjSaveAddrs(t, out, addrs...)
	return out
}

func hjSaveAddrs(t *testing.T, e *enrollEnv, addrs ...string) {
	t.Helper()
	var list []netip.Addr
	for _, a := range addrs {
		list = append(list, netip.MustParseAddr(a))
	}
	if err := listen.SaveTailscaleAddrs(context.Background(), e.db, list); err != nil {
		t.Fatal(err)
	}
}

// hjPlanOutcome calls the Plan handler of core.host.create directly, to read the state hash a plan view hides.
func hjPlanOutcome(t *testing.T, e *enrollEnv, in core.HostCreateInput) core.PlanOutcome {
	t.Helper()
	def, ok := e.reg.Action(ref("core.host.create"))
	if !ok {
		t.Fatal("core.host.create is not registered")
	}
	env := core.Env{DB: e.db, Clock: e.clock, Principal: operator}
	out, err := def.Handler.Plan(context.Background(), env, encodeInput(t, in))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func hjHosts(e *enrollEnv, name string) int {
	return e.count(`SELECT count(*) FROM core_hosts WHERE name = ?`, name)
}

func hjDetail(t *testing.T, err error) string {
	t.Helper()
	var coded *errcode.Error
	if !errors.As(err, &coded) || coded.Code != errcode.InvalidInput {
		t.Fatalf("err = %#v, want request.invalid_input", err)
	}
	return coded.Detail
}

func TestHJHostCreateWithATailnetJoin(t *testing.T) {
	j := newHJJoiner()
	e := hjEnv(t, j, "", "fd7a:115c:a1e0::1", "100.64.0.7", "100.64.0.3")
	in := core.HostCreateInput{Name: "web1", TailnetJoin: true, Tags: []string{"tag:web"}}

	v := e.run(operator, "core.host.create", in, nil)
	g := e.checkGrant(v.Output, "100.64.0.3:7443")

	token, bundle, err := transport.ParseBootstrap(g.Bundle)
	if err != nil || bundle == nil {
		t.Fatalf("the grant's bundle %q does not parse: %v", g.Bundle[:min(len(g.Bundle), 12)], err)
	}
	if token != bundle.Token || bundle.Token.String() != g.Token {
		t.Errorf("the bundle holds a different token than the grant: %+v", bundle.Token)
	}
	if bundle.TailnetKey != hjKey || bundle.ControllerTailnetAddr != "100.64.0.3:7443" {
		t.Errorf("bundle %+v, want the joiner's key and the first IPv4 tailnet address with the agent port", bundle)
	}

	// The engine plans again when it applies, to compare the state hash, so PlanJoin runs at least once.
	plans, prepares := j.calls()
	if plans < 1 || prepares != 1 {
		t.Fatalf("PlanJoin ran %d times and PrepareJoin %d times, want PlanJoin at least once and PrepareJoin once", plans, prepares)
	}
	req := j.prepares[0]
	if req.HostID != g.Host.ID || !reflect.DeepEqual(req.Tags, []string{"tag:web"}) || !req.Expires.Equal(t0.Add(fleet.TokenTTL)) {
		t.Errorf("PrepareJoin got %+v, want the new host's id, the tags and the token's expiry", req)
	}
	if j.hostSeen[0] {
		t.Error("the host row existed when PrepareJoin ran: the key is requested before the host is written")
	}
	if g.Host.State != core.HostPending {
		t.Errorf("host state %s, want pending", g.Host.State)
	}
	noSecret(t, []string{g.Token, g.Bundle, hjKey}, e.change(v.ID), e.auditEntries())
	if err := e.verify("core.host.create", in); err != nil {
		t.Errorf("Verify: %v", err)
	}
}

// TestHJHostCreateJoinUsesTheFirstIPv6WhenThereIsNoIPv4: the address is bracketed.
func TestHJHostCreateJoinUsesTheFirstIPv6WhenThereIsNoIPv4(t *testing.T) {
	e := hjEnv(t, newHJJoiner(), testAdvertise, "fd7a:115c:a1e0::9", "fd7a:115c:a1e0::1")
	v := e.run(operator, "core.host.create", core.HostCreateInput{Name: "web1", TailnetJoin: true}, nil)
	g := e.checkGrant(v.Output, "[fd7a:115c:a1e0::1]:7443")
	if _, bundle, err := transport.ParseBootstrap(g.Bundle); err != nil || bundle == nil || bundle.ControllerTailnetAddr != "[fd7a:115c:a1e0::1]:7443" {
		t.Errorf("bundle %+v, %v", bundle, err)
	}
}

func TestHJHostCreateJoinPlan(t *testing.T) {
	j := newHJJoiner()
	e := hjEnv(t, j, testAdvertise, "100.64.0.7")
	host := core.DiffLine{Op: "+", Text: "host web1: pending, with an enrollment token for 100.64.0.7:7443, shown once and valid for one hour"}
	compensated := core.DiffLine{Op: " ", Text: "compensated by core.host.delete"}

	pv, err := e.plan(operator, "core.host.create", core.HostCreateInput{Name: "web1", TailnetJoin: true, Tags: []string{"tag:web", "tag:ci"}})
	if err != nil {
		t.Fatal(err)
	}
	want := []core.DiffLine{
		host, {Op: "+", Text: "joiner: a one-use auth key"},
		{Op: "+", Text: "bootstrap bundle: the token plus a one-use tailnet auth key for tag:web, tag:ci, shown once"},
		compensated,
	}
	if !reflect.DeepEqual(pv.Diff, want) {
		t.Errorf("diff\n%+v\nwant\n%+v", pv.Diff, want)
	}
	if len(pv.Guards) != 1 || pv.Guards[0].ID != "name-free" || !pv.Guards[0].OK {
		t.Errorf("guards %+v", pv.Guards)
	}
	if len(j.plans) != 1 || j.plans[0].HostID != "" || !reflect.DeepEqual(j.plans[0].Tags, []string{"tag:web", "tag:ci"}) || !j.plans[0].Expires.Equal(t0.Add(fleet.TokenTTL)) {
		t.Errorf("PlanJoin got %+v: the host id is chosen at apply", j.plans)
	}
	if _, prepares := j.calls(); prepares != 0 {
		t.Error("planning created a key")
	}

	pv, err = e.plan(operator, "core.host.create", core.HostCreateInput{Name: "web1", TailnetJoin: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := pv.Diff[len(pv.Diff)-2].Text; got != "bootstrap bundle: the token plus a one-use tailnet auth key for no tags, shown once" {
		t.Errorf("bundle line %q", got)
	}
}

// TestHJHostCreateJoinPlanStateHash: the hash follows everything the apply depends on and leaves a plain create alone.
func TestHJHostCreateJoinPlanStateHash(t *testing.T) {
	j := newHJJoiner()
	e := hjEnv(t, j, testAdvertise, "100.64.0.7")
	join := core.HostCreateInput{Name: "web1", TailnetJoin: true, Tags: []string{"tag:web"}}

	base := hjPlanOutcome(t, e, join).StateHash
	if again := hjPlanOutcome(t, e, join).StateHash; again != base {
		t.Error("the same plan hashed differently")
	}
	j.state = "joiner-state-2"
	if hjPlanOutcome(t, e, join).StateHash == base {
		t.Error("the joiner's state is not in the hash")
	}
	j.state = "joiner-state-1"
	hjSaveAddrs(t, e, "100.64.0.8")
	if hjPlanOutcome(t, e, join).StateHash == base {
		t.Error("the tailnet address is not in the hash")
	}
	hjSaveAddrs(t, e, "100.64.0.7")
	other := join
	other.Tags = []string{"tag:ci"}
	if hjPlanOutcome(t, e, other).StateHash == base {
		t.Error("the tags are not in the hash")
	}
	plain := hjPlanOutcome(t, e, core.HostCreateInput{Name: "web1"}).StateHash
	if plain != stateHash("web1", "false") {
		t.Error("a create without a join must hash as it did before the join existed")
	}
}

// TestHJPlainHostCreateIgnoresTheJoiner: without tailnetJoin the answer, the address and the calls are what they were.
func TestHJPlainHostCreateIgnoresTheJoiner(t *testing.T) {
	j := newHJJoiner()
	e := hjEnv(t, j, testAdvertise, "100.64.0.7")
	v := e.run(operator, "core.host.create", core.HostCreateInput{Name: "web1"}, nil)
	g := e.checkGrant(v.Output, testAdvertise)
	if g.Bundle != "" {
		t.Error("a create without tailnetJoin returned a bundle")
	}
	if plans, prepares := j.calls(); plans != 0 || prepares != 0 {
		t.Errorf("the joiner ran %d plans and %d prepares", plans, prepares)
	}
}

func TestHJHostCreateJoinRefusals(t *testing.T) {
	e := hjEnv(t, newHJJoiner(), testAdvertise, "100.64.0.7")
	for name, c := range map[string]struct {
		in    core.HostCreateInput
		field string
	}{
		"tags without tailnetJoin":           {core.HostCreateInput{Name: "web1", Tags: []string{"tag:web"}}, "/tags"},
		"controllerAddress with tailnetJoin": {core.HostCreateInput{Name: "web1", TailnetJoin: true, ControllerAddress: "web.example:7443"}, "/controllerAddress"},
	} {
		_, err := e.plan(operator, "core.host.create", c.in)
		wantField(t, err, c.field)
		wantField(t, e.applyDirect("core.host.create", c.in), c.field)
		if hjHosts(e, "web1") != 0 || e.count(`SELECT count(*) FROM core_enroll_tokens`) != 0 {
			t.Errorf("%s: a refused create wrote a host or a token", name)
		}
	}
}

func TestHJHostCreateJoinNeedsTheJoinerModule(t *testing.T) {
	join := core.HostCreateInput{Name: "web1", TailnetJoin: true}

	// No compiled-in module implements core.EnrollJoiner.
	bare := newEnrollEnv(t, testAdvertise)
	hjSaveAddrs(t, bare, "100.64.0.7")
	_, err := bare.plan(operator, "core.host.create", join)
	if d := hjDetail(t, err); d != "tailnet join needs the tailnet module" {
		t.Errorf("without a joiner: %q", d)
	}
	if d := hjDetail(t, bare.applyDirect("core.host.create", join)); d != "tailnet join needs the tailnet module" {
		t.Errorf("apply without a joiner: %q", d)
	}

	// The module is disabled on the controller's own host.
	e := hjEnv(t, newHJJoiner(), testAdvertise, "100.64.0.7")
	e.setModuleState("h1", "tailnet", false, true, "")
	_, err = e.plan(operator, "core.host.create", join)
	if d := hjDetail(t, err); d != "tailnet join needs the tailnet module" {
		t.Errorf("with the joiner disabled: %q", d)
	}
	e.setModuleState("h1", "tailnet", true, true, "")
	if _, err := e.plan(operator, "core.host.create", join); err != nil {
		t.Errorf("with the joiner enabled: %v", err)
	}

	// No row yet: the module's default decides.
	for defaultOff, wantOK := range map[bool]bool{false: true, true: false} {
		j := newHJJoiner()
		j.defaultOff = defaultOff
		e := hjEnv(t, j, testAdvertise, "100.64.0.7")
		e.exec(`DELETE FROM core_module_state WHERE module_id = 'tailnet'`)
		_, err := e.plan(operator, "core.host.create", join)
		if (err == nil) != wantOK {
			t.Errorf("no module state row, default off %v: err %v", defaultOff, err)
		}
	}
}

func TestHJHostCreateJoinNeedsATailnetAddress(t *testing.T) {
	e := hjEnv(t, newHJJoiner(), testAdvertise)
	join := core.HostCreateInput{Name: "web1", TailnetJoin: true}
	_, err := e.plan(operator, "core.host.create", join)
	if d := hjDetail(t, err); d != "the controller is not on a tailnet" {
		t.Errorf("plan: %q", d)
	}
	if d := hjDetail(t, e.applyDirect("core.host.create", join)); d != "the controller is not on a tailnet" {
		t.Errorf("apply: %q", d)
	}
}

func TestHJHostCreateJoinerFailures(t *testing.T) {
	join := core.HostCreateInput{Name: "web1", TailnetJoin: true, Tags: []string{"tag:web"}}

	j := newHJJoiner()
	j.planErr = errcode.New(errcode.CloudNotConnected, "no tailnet is connected")
	e := hjEnv(t, j, testAdvertise, "100.64.0.7")
	_, err := e.plan(operator, "core.host.create", join)
	wantCode(t, err, errcode.CloudNotConnected)

	j = newHJJoiner()
	j.prepareErr = errcode.New(errcode.CloudUncertainOutcome, "the key may exist")
	e = hjEnv(t, j, testAdvertise, "100.64.0.7")
	wantCode(t, e.applyDirect("core.host.create", join), errcode.CloudUncertainOutcome)
	if hjHosts(e, "web1") != 0 || e.count(`SELECT count(*) FROM core_enroll_tokens`) != 0 {
		t.Error("a failed PrepareJoin left a host or a token")
	}
}

// TestHJHostCreateJoinTakenName: a name taken since the plan is refused before any key is requested.
func TestHJHostCreateJoinTakenName(t *testing.T) {
	j := newHJJoiner()
	e := hjEnv(t, j, testAdvertise, "100.64.0.7")
	wantCode(t, e.applyDirect("core.host.create", core.HostCreateInput{Name: "alpha", TailnetJoin: true}), errcode.PlanStateChanged)
	if _, prepares := j.calls(); prepares != 0 {
		t.Errorf("PrepareJoin ran %d times for a name that is taken", prepares)
	}
}

// TestHJControllerTailnetAddr covers the address rule directly.
func TestHJControllerTailnetAddr(t *testing.T) {
	e := hjEnv(t, newHJJoiner(), "")
	ctx := context.Background()
	for name, c := range map[string]struct {
		saved []string
		want  string
	}{
		"an IPv4 address":       {[]string{"100.64.0.7"}, "100.64.0.7:7443"},
		"the first of two IPv4": {[]string{"100.100.1.1", "100.64.0.7"}, "100.64.0.7:7443"},
		"IPv4 before IPv6":      {[]string{"fd7a:115c:a1e0::1", "100.64.0.7"}, "100.64.0.7:7443"},
		"an IPv6 address only":  {[]string{"fd7a:115c:a1e0::1"}, "[fd7a:115c:a1e0::1]:7443"},
	} {
		hjSaveAddrs(t, e, c.saved...)
		got, err := controllerTailnetAddr(ctx, e.db)
		if err != nil || got != c.want {
			t.Errorf("%s: %q, %v; want %q", name, got, err, c.want)
		}
	}
	hjSaveAddrs(t, e)
	if _, err := controllerTailnetAddr(ctx, e.db); hjDetail(t, err) != "the controller is not on a tailnet" {
		t.Errorf("with none saved: %v", err)
	}
	e.exec(`DELETE FROM core_meta WHERE key = 'tailscale_addrs'`)
	if _, err := controllerTailnetAddr(ctx, e.db); hjDetail(t, err) != "the controller is not on a tailnet" {
		t.Errorf("with no row: %v", err)
	}
}
