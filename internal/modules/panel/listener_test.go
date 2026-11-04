package panel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/core/store"
)

const (
	lsAdd    = "01JADD00000000000000000001"
	lsRemove = "01JREMOVE00000000000000001"
	lsOther  = "01JOTHER0000000000000000001"
	lsChild  = "01JCHILD000000000000000001"
	lsHost   = "h1"
	lsName   = "panel.example.com"
	lsTunnel = "8e6f7a8b-9c0d-4e1f-a2b3-c4d5e6f70819"
)

var (
	lsSubnet  = netip.MustParsePrefix("172.31.255.248/29")
	lsGateway = netip.MustParseAddr("172.31.255.249")
	lsProxy   = netip.MustParseAddr("172.31.255.250")
	lsAddCall = core.CallContext{ParentChangeID: lsAdd, ParentAction: ActionAdd, StepID: "listen"}
	lsRmCall  = core.CallContext{ParentChangeID: lsRemove, ParentAction: ActionRemove, StepID: "revoke"}
)

// lsModule serves the three actions under the panel module id, so these tests do not depend on the module's manifest.
type lsModule struct{}

func (lsModule) Manifest() core.Manifest {
	return core.Manifest{
		ID: ID, Version: 1,
		Actions: []core.ActionDef{listenerOpenAction(), listenerRevokeAction(), statusAction()},
	}
}

// lsJournal is an in-memory core.EffectJournal.
type lsJournal struct {
	changeID string
	list     []core.Effect
}

func (j *lsJournal) Intend(_ context.Context, e core.Effect, _ []byte) (int, error) {
	e.Seq, e.State, e.ChangeID = len(j.list)+1, core.EffectIntended, j.changeID
	j.list = append(j.list, e)
	return e.Seq, nil
}

func (j *lsJournal) find(seq int) (*core.Effect, error) {
	if seq < 1 || seq > len(j.list) || j.list[seq-1].State != core.EffectIntended {
		return nil, fmt.Errorf("fake: effect %d is not intended", seq)
	}
	return &j.list[seq-1], nil
}

func (j *lsJournal) Made(_ context.Context, seq int, resource string, post core.StateDoc) error {
	e, err := j.find(seq)
	if err != nil {
		return err
	}
	e.State, e.Resource, e.Post = core.EffectMade, resource, &post
	return nil
}

func (j *lsJournal) Skipped(_ context.Context, seq int) error {
	e, err := j.find(seq)
	if err != nil {
		return err
	}
	e.State = core.EffectSkipped
	return nil
}

func (j *lsJournal) List(context.Context) ([]core.Effect, error) {
	return append([]core.Effect(nil), j.list...), nil
}

func (j *lsJournal) Secret(context.Context, int) ([]byte, error) {
	return nil, errors.New("fake: no secret")
}

var _ core.EffectJournal = (*lsJournal)(nil)

// lsRacy opens a listener as fdListeners does and then runs onOpen, which plays another change that exposes the
// panel while this one is still opening its listener.
type lsRacy struct {
	*fdListeners
	onOpen func()
}

func (r lsRacy) Open(ctx context.Context, spec core.ListenerSpec) (string, error) {
	id, err := r.fdListeners.Open(ctx, spec)
	r.onOpen()
	return id, err
}

// lsRig is a controller database with the controller's host, the in-memory listeners and one effect journal.
type lsRig struct {
	t  *testing.T
	db *store.DB
	l  *fdListeners
	j  *lsJournal
}

func lsNewRig(t *testing.T) *lsRig {
	t.Helper()
	db := fdDB(t)
	fdInsertHost(t, db, lsHost, true)
	return &lsRig{t: t, db: db, l: fdNewListeners(), j: &lsJournal{}}
}

// env is the controller env of a call; a change id also gives it the effect journal.
func (r *lsRig) env(call core.CallContext, changeID string) core.Env {
	r.t.Helper()
	env := fdEnv(r.t, r.db, r.l, call)
	if changeID != "" {
		r.j.changeID = changeID
		env.ChangeID, env.Effects = changeID, r.j
	}
	return env
}

// live is the live exposure row.
func (r *lsRig) live() (exposureRow, bool) {
	r.t.Helper()
	row, ok, err := liveExposure(context.Background(), r.db.Read())
	if err != nil {
		r.t.Fatal(err)
	}
	return row, ok
}

// expose stores the exposure lsAdd in rowState with removal as its removal change, behind a listener that the
// fake holds in listenerState (open, revoked or closed), and returns the row.
func (r *lsRig) expose(rowState, removal, listenerState string) exposureRow {
	r.t.Helper()
	ctx := context.Background()
	id, err := r.l.Open(ctx, lsSpec(ViaNPM))
	if err != nil {
		r.t.Fatal(err)
	}
	if listenerState != core.ListenerOpen {
		if err := r.l.Revoke(ctx, id); err != nil {
			r.t.Fatal(err)
		}
	}
	if listenerState == core.ListenerClosed {
		if err := r.l.Close(ctx, id); err != nil {
			r.t.Fatal(err)
		}
	}
	row := lsRow(lsAdd, rowState, removal)
	row.ListenerID = id
	fdInsertExposure(r.t, r.db, row)
	return row
}

// lsRow is an exposure through NPM with the given id, state and removal change.
func lsRow(id, state, removal string) exposureRow {
	return exposureRow{
		ID: id, Via: ViaNPM, Hostname: lsName, HostID: lsHost, Subnet: lsSubnet, Gateway: lsGateway, Proxy: lsProxy,
		Port: DefaultPort, ListenerID: "l1", RuleID: RuleID(id), State: state, RemovalChangeID: removal,
		CreatedAt: fdNow, UpdatedAt: fdNow,
	}
}

// lsSpec is the runtime listener the open action asks for.
func lsSpec(via string) core.ListenerSpec {
	header := core.ClientHeaderNPM
	if via == ViaTunnel {
		header = core.ClientHeaderTunnel
	}
	return core.ListenerSpec{
		Addr: netip.AddrPortFrom(lsGateway, DefaultPort), AllowedPeer: lsProxy, ClientHeader: header, PublicHost: lsName, ChangeID: lsAdd,
	}
}

func lsOpenInput(via string) ListenerOpenInput {
	in := ListenerOpenInput{
		Via: via, Hostname: lsName, HostID: lsHost, Subnet: lsSubnet, Gateway: lsGateway, Proxy: lsProxy, Port: DefaultPort,
	}
	if via == ViaTunnel {
		in.Tunnel = lsTunnel
	}
	return in
}

func lsJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// lsDecode decodes raw into a T and refuses a member T does not have.
func lsDecode[T any](t *testing.T, raw json.RawMessage) T {
	t.Helper()
	var v T
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("output %s: %v", raw, err)
	}
	return v
}

func lsDiff(diff []core.DiffLine) string {
	var b strings.Builder
	for _, l := range diff {
		b.WriteString(l.Op + " " + l.Text + "\n")
	}
	return b.String()
}

// lsWantCode fails the test unless err carries code.
func lsWantCode(t *testing.T, what string, err error, code errcode.Code) {
	t.Helper()
	if err == nil || errcode.From(err).Code != code {
		t.Errorf("%s: error %v, want code %s", what, err, code)
	}
}

// lsWantRefusal fails the test unless err carries code and is marked as a refusal that committed nothing.
func lsWantRefusal(t *testing.T, what string, err error, code errcode.Code) {
	t.Helper()
	lsWantCode(t, what, err, code)
	if err != nil && !core.IsDefinite(err) {
		t.Errorf("%s: error %v is not marked definite", what, err)
	}
}

// TestLsDeclarations: the three actions are controller-side; the open action is compensable with an undo, the
// revoke action is compensable, forward-only, converging and break-glass without an undo, the status action is a
// read. All three need the admin scope, and the registry accepts the set.
func TestLsDeclarations(t *testing.T) {
	open, revoke, status := listenerOpenAction(), listenerRevokeAction(), statusAction()
	for _, tc := range []struct {
		def   core.ActionDef
		name  string
		class core.Class
	}{
		{open, ActionOpen, core.ClassCompensable},
		{revoke, ActionRevoke, core.ClassCompensable},
		{status, ActionStatus, core.ClassRead},
	} {
		s := tc.def.Spec
		if s.Name != tc.name || s.Version != 1 || s.Summary == "" || s.Class != tc.class || s.Side != core.SideController || s.MinScope != core.ScopeAdmin {
			t.Errorf("%s: spec %+v, want version 1, a summary, class %s, controller side, admin scope", tc.name, s, tc.class)
		}
		if _, err := schema.ActionSnapshot(tc.def); err != nil {
			t.Errorf("%s: schema: %v", tc.name, err)
		}
	}
	if !slices.Equal(open.Spec.LockKeys, []string{"panel"}) || open.Spec.ForwardOnly || open.Spec.BreakGlass || open.Handler.Inverse == nil || open.Handler.Observe == nil {
		t.Errorf("open: spec %+v, want lock key panel, an undo and an observer, not forward-only and not break-glass", open.Spec)
	}
	if !slices.Equal(revoke.Spec.LockKeys, []string{"panel"}) || !revoke.Spec.ForwardOnly || !revoke.Spec.Converges || !revoke.Spec.BreakGlass || revoke.Handler.Inverse != nil {
		t.Errorf("revoke: spec %+v, want lock key panel, forward-only, converging and break-glass, with no undo", revoke.Spec)
	}
	if len(status.Spec.LockKeys) != 0 || status.Handler.Read == nil {
		t.Errorf("status: spec %+v, want a read handler and no lock", status.Spec)
	}
	if _, err := core.NewRegistry(lsModule{}); err != nil {
		t.Errorf("registry: %v", err)
	}
	for via, name := range map[string]string{ViaNPM: "npm", ViaTunnel: "tunnel"} {
		if err := schema.NewValidator().ValidateInput(open, lsJSON(t, lsOpenInput(via))); err != nil {
			t.Errorf("a valid input via %s is refused by the open schema: %v", name, err)
		}
	}
}

// TestLsOpenRefusals: the open action runs only as a step of panel.exposure.add, checks its input and refuses while
// an exposure is live, at plan and at apply, before it touches a listener or the journal.
func TestLsOpenRefusals(t *testing.T) {
	ctx := context.Background()
	def := listenerOpenAction()
	badAddr := func(subnet, gateway, proxy string) func(in *ListenerOpenInput) {
		return func(in *ListenerOpenInput) {
			in.Subnet, in.Gateway, in.Proxy = netip.MustParsePrefix(subnet), netip.MustParseAddr(gateway), netip.MustParseAddr(proxy)
		}
	}
	tests := []struct {
		name string
		call core.CallContext
		edit func(in *ListenerOpenInput)
		live bool
		want errcode.Code
	}{
		{"no parent", core.CallContext{}, nil, false, errcode.InvalidInput},
		{"the removal as parent", core.CallContext{ParentChangeID: lsAdd, ParentAction: ActionRemove, StepID: "listen"}, nil, false, errcode.InvalidInput},
		{"a parent without a change id", core.CallContext{ParentAction: ActionAdd, StepID: "listen"}, nil, false, errcode.InvalidInput},
		{"an unknown via", lsAddCall, func(in *ListenerOpenInput) { in.Via = "carrier" }, false, errcode.InvalidInput},
		{"a tunnel for npm", lsAddCall, func(in *ListenerOpenInput) { in.Tunnel = lsTunnel }, false, errcode.InvalidInput},
		{"a hostname in capitals", lsAddCall, func(in *ListenerOpenInput) { in.Hostname = "Panel.Example.com" }, false, errcode.InvalidInput},
		{"no port", lsAddCall, func(in *ListenerOpenInput) { in.Port = 0 }, false, errcode.InvalidInput},
		{"a port below 1024", lsAddCall, func(in *ListenerOpenInput) { in.Port = 80 }, false, errcode.InvalidInput},
		{"a subnet that is not a /29", lsAddCall, badAddr("172.31.255.240/28", "172.31.255.241", "172.31.255.242"), false, errcode.InvalidInput},
		{"a subnet outside the panel range", lsAddCall, badAddr("172.31.254.248/29", "172.31.254.249", "172.31.254.250"), false, errcode.InvalidInput},
		{"a subnet that is not masked", lsAddCall, badAddr("172.31.255.249/29", "172.31.255.250", "172.31.255.251"), false, errcode.InvalidInput},
		{"a gateway that is not the first host", lsAddCall, badAddr("172.31.255.248/29", "172.31.255.250", "172.31.255.251"), false, errcode.InvalidInput},
		{"a proxy that is not the second host", lsAddCall, badAddr("172.31.255.248/29", "172.31.255.249", "172.31.255.251"), false, errcode.InvalidInput},
		{"a live exposure", lsAddCall, nil, true, errcode.PanelAlreadyExposed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := lsNewRig(t)
			if tc.live {
				fdInsertExposure(t, r.db, lsRow(lsOther, StateOpen, ""))
			}
			in := lsOpenInput(ViaNPM)
			if tc.edit != nil {
				tc.edit(&in)
			}
			raw := lsJSON(t, in)
			_, err := def.Handler.Plan(ctx, r.env(tc.call, ""), raw)
			lsWantRefusal(t, "plan", err, tc.want)
			_, err = def.Handler.Apply(ctx, r.env(tc.call, lsChild), raw)
			lsWantRefusal(t, "apply", err, tc.want)
			if len(r.l.Rows) != 0 || len(r.j.list) != 0 {
				t.Errorf("the refusals opened %d listeners and journaled %d effects, want none", len(r.l.Rows), len(r.j.list))
			}
			if row, ok := r.live(); ok != tc.live || (ok && row.ID != lsOther) {
				t.Errorf("live exposure %+v (found %t), want only the one stored before: %t", row, ok, tc.live)
			}
		})
	}
}

// TestLsOpenPlan: the plan shows the listener and the exposure it records, writes nothing, and is not blocked by an
// exposure that is gone.
func TestLsOpenPlan(t *testing.T) {
	ctx := context.Background()
	def := listenerOpenAction()
	r := lsNewRig(t)
	fdInsertChange(t, r.db, lsRemove, ActionRemove, "completed")
	fdInsertExposure(t, r.db, lsRow(lsOther, StateRevoked, lsRemove))
	plan, err := def.Handler.Plan(ctx, r.env(lsAddCall, ""), lsJSON(t, lsOpenInput(ViaNPM)))
	if err != nil {
		t.Fatalf("plan beside a gone exposure: %v", err)
	}
	diff := lsDiff(plan.Diff)
	for _, want := range []string{lsName, "172.31.255.249:9443", "172.31.255.250", ViaNPM} {
		if !strings.Contains(diff, want) {
			t.Errorf("diff %q does not name %q", diff, want)
		}
	}
	if plan.StateHash == "" {
		t.Error("the plan has no state hash")
	}
	if len(r.l.Rows) != 0 {
		t.Errorf("the plan opened %d listeners", len(r.l.Rows))
	}
}

// TestLsOpenApply: the apply opens the listener with the trusted peer and the client header of the way in, records
// the exposure under the parent's change id with its rule id, journals one made creation, and verifies; it stops
// verifying once the listener is no longer open.
func TestLsOpenApply(t *testing.T) {
	ctx := context.Background()
	def := listenerOpenAction()
	for _, tc := range []struct{ name, via string }{{"npm", ViaNPM}, {"tunnel", ViaTunnel}} {
		t.Run(tc.name, func(t *testing.T) {
			r := lsNewRig(t)
			in := lsOpenInput(tc.via)
			raw := lsJSON(t, in)
			env := r.env(lsAddCall, lsChild)
			rawOut, err := def.Handler.Apply(ctx, env, raw)
			if err != nil {
				t.Fatalf("apply: %v", err)
			}
			wantOut := ListenerOpenOutput{
				ExposureID: lsAdd, ListenerID: "l1", RuleID: RuleID(lsAdd), Address: netip.AddrPortFrom(lsGateway, DefaultPort),
			}
			if got := lsDecode[ListenerOpenOutput](t, rawOut); got != wantOut {
				t.Errorf("output %+v, want %+v", got, wantOut)
			}
			if len(r.l.Rows) != 1 || r.l.Rows["l1"].Spec != lsSpec(tc.via) || r.l.Rows["l1"].State != core.ListenerOpen {
				t.Errorf("listeners %+v, want l1 open with %+v", r.l.Rows, lsSpec(tc.via))
			}
			row, ok := r.live()
			if !ok {
				t.Fatal("no exposure is recorded")
			}
			created, updated := row.CreatedAt, row.UpdatedAt
			row.CreatedAt, row.UpdatedAt = fdNow, fdNow
			want := lsRow(lsAdd, StateOpen, "")
			want.Via, want.Tunnel = tc.via, in.Tunnel
			if row != want {
				t.Errorf("exposure %+v, want %+v", row, want)
			}
			if !created.Equal(fdNow) || !updated.Equal(fdNow) {
				t.Errorf("exposure times %v and %v, want the clock's %v", created, updated, fdNow)
			}
			if len(r.j.list) != 1 {
				t.Fatalf("%d effects journaled, want 1", len(r.j.list))
			}
			eff := r.j.list[0]
			if eff.Kind != core.EffectCreate || eff.State != core.EffectMade || eff.Resource != "panel.exposure:"+lsAdd || !eff.Local || !eff.InverseLocal || eff.Post == nil {
				t.Errorf("effect %+v, want a made local creation of panel.exposure:%s with a post state", eff, lsAdd)
			}
			if err := def.Handler.Verify(ctx, env, raw, rawOut); err != nil {
				t.Errorf("verify: %v", err)
			}
			if err := r.l.Revoke(ctx, "l1"); err != nil {
				t.Fatal(err)
			}
			if err := def.Handler.Verify(ctx, env, raw, rawOut); err == nil {
				t.Error("verify accepted an exposure whose listener is revoked")
			}
		})
	}
}

// TestLsOpenFailures: a listener that cannot open leaves no exposure and a skipped effect; an exposure that
// appears while the listener opens wins, and the listener this apply opened is revoked and closed.
func TestLsOpenFailures(t *testing.T) {
	ctx := context.Background()
	def := listenerOpenAction()
	raw := lsJSON(t, lsOpenInput(ViaNPM))

	t.Run("the listener cannot open", func(t *testing.T) {
		r := lsNewRig(t)
		failure := errors.New("bind failed")
		r.l.FailOpen = failure
		if _, err := def.Handler.Apply(ctx, r.env(lsAddCall, lsChild), raw); !errors.Is(err, failure) {
			t.Fatalf("apply error %v, want the listener's error", err)
		}
		if _, ok := r.live(); ok {
			t.Error("an exposure is recorded")
		}
		if len(r.j.list) != 1 || r.j.list[0].State != core.EffectSkipped {
			t.Errorf("effects %+v, want one skipped effect", r.j.list)
		}
	})

	t.Run("another exposure appears while the listener opens", func(t *testing.T) {
		r := lsNewRig(t)
		env := r.env(lsAddCall, lsChild)
		other := lsRow(lsOther, StateOpen, "")
		other.ListenerID = "lx"
		env.Listeners = lsRacy{fdListeners: r.l, onOpen: func() { fdInsertExposure(t, r.db, other) }}
		_, err := def.Handler.Apply(ctx, env, raw)
		lsWantCode(t, "apply", err, errcode.PanelAlreadyExposed)
		if row, ok := r.live(); !ok || row.ID != lsOther || row.ListenerID != "lx" {
			t.Errorf("live exposure %+v (found %t), want the other one unchanged", row, ok)
		}
		if got := r.l.Rows["l1"].State; got != core.ListenerClosed {
			t.Errorf("the listener this apply opened is %q, want closed", got)
		}
		if len(r.j.list) != 1 || r.j.list[0].State != core.EffectSkipped {
			t.Errorf("effects %+v, want one skipped effect", r.j.list)
		}
	})
}

var errLsRevoke = errors.New("revoke failed")

// lsNoRevoke is fdListeners whose Revoke always fails.
type lsNoRevoke struct {
	*fdListeners
}

func (lsNoRevoke) Revoke(context.Context, string) error { return errLsRevoke }

// TestLsOpenCleanupFailure: when the insert fails and the listener this apply opened cannot be revoked, the effect
// stays intended and the error is not definite; the observer reports the open listener of that change as a state
// that is neither null nor the creation's, and the inverse closes it.
func TestLsOpenCleanupFailure(t *testing.T) {
	ctx := context.Background()
	def := listenerOpenAction()
	r := lsNewRig(t)
	fdInsertChange(t, r.db, lsRemove, ActionRemove, "completed")
	fdInsertExposure(t, r.db, lsRow(lsAdd, StateRevoked, lsRemove))

	env := r.env(lsAddCall, lsChild)
	env.Listeners = lsNoRevoke{fdListeners: r.l}
	_, err := def.Handler.Apply(ctx, env, lsJSON(t, lsOpenInput(ViaNPM)))
	if err == nil || core.IsDefinite(err) || !errors.Is(err, errLsRevoke) {
		t.Fatalf("apply error %v, want one that is not definite and carries the revoke error", err)
	}
	if !strings.Contains(err.Error(), "revoke the listener l1") {
		t.Errorf("apply error %q does not name the revoke as the failed cleanup", err)
	}
	if len(r.j.list) != 1 || r.j.list[0].State != core.EffectIntended {
		t.Fatalf("effects %+v, want one intended effect", r.j.list)
	}
	if got := r.l.Rows["l1"].State; got != core.ListenerOpen {
		t.Errorf("listener l1 is %q, want open", got)
	}

	at := fdStamp(fdNow)
	fdExec(t, r.db, `INSERT INTO core_listeners (id, address, port, allowed_peer, client_header, public_host, change_id, state,
		created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"l1", lsGateway.String(), DefaultPort, lsProxy.String(), core.ClientHeaderNPM, lsName, lsAdd, core.ListenerOpen, at, at)
	eff := r.j.list[0]
	obs, err := def.Handler.Observe(ctx, r.env(lsAddCall, lsChild), eff)
	if err != nil {
		t.Fatal(err)
	}
	if obs.Matches != 1 || string(obs.Current.State) == "null" || bytes.Equal(obs.Current.State, eff.After.State) {
		t.Errorf("observation %+v, want one match in a state that is neither null nor the creation's %s", obs, eff.After.State)
	}

	if _, err := def.Handler.Inverse(ctx, r.env(lsAddCall, lsChild), eff); err != nil {
		t.Fatalf("inverse: %v", err)
	}
	if got := r.l.Rows["l1"].State; got != core.ListenerClosed {
		t.Errorf("listener l1 is %q after the inverse, want closed", got)
	}
}

// TestLsOpenObserveAndInverse: the observer finds the exposure by the id in the resource, as a made object or as a
// creation not yet named, and answers the null state for any other id; the inverse revokes and closes the listener
// and deletes the row, touches no other exposure, and may run again.
func TestLsOpenObserveAndInverse(t *testing.T) {
	ctx := context.Background()
	def := listenerOpenAction()
	r := lsNewRig(t)
	env := r.env(lsAddCall, lsChild)
	if _, err := def.Handler.Apply(ctx, env, lsJSON(t, lsOpenInput(ViaNPM))); err != nil {
		t.Fatal(err)
	}
	eff := r.j.list[0]

	obs, err := def.Handler.Observe(ctx, env, eff)
	if err != nil {
		t.Fatal(err)
	}
	if obs.Matches != 1 || !obs.Exclusive || obs.Resource != "panel.exposure:"+lsAdd || !bytes.Equal(obs.Current.State, eff.Post.State) {
		t.Errorf("observation %+v, want one exclusive match of panel.exposure:%s in the journaled post state %s", obs, lsAdd, eff.Post.State)
	}
	creation := eff
	creation.Resource = "panel.exposure:?" + lsAdd
	if obs, err := def.Handler.Observe(ctx, env, creation); err != nil || obs.Matches != 1 || obs.Resource != "panel.exposure:"+lsAdd {
		t.Errorf("observation of the creation %+v, %v; want one match naming panel.exposure:%s", obs, err, lsAdd)
	}
	other := eff
	other.Resource = "panel.exposure:" + lsOther
	if obs, err := def.Handler.Observe(ctx, env, other); err != nil || string(obs.Current.State) != "null" {
		t.Errorf("observation of another exposure %+v, %v; want the null state", obs, err)
	}
	foreign := eff
	foreign.Resource = "npm.proxyhost:1"
	if _, err := def.Handler.Observe(ctx, env, foreign); err == nil {
		t.Error("observe accepted a resource that is not an exposure")
	}

	keep := lsNewRig(t)
	keep.expose(StateOpen, "", core.ListenerOpen)
	foreignEffect := core.Effect{Kind: core.EffectCreate, Resource: "panel.exposure:" + lsOther}
	if _, err := def.Handler.Inverse(ctx, keep.env(lsAddCall, lsChild), foreignEffect); err != nil {
		t.Fatalf("inverse of an exposure that is not there: %v", err)
	}
	if row, ok := keep.live(); !ok || row.ID != lsAdd || row.State != StateOpen || keep.l.Rows[row.ListenerID].State != core.ListenerOpen {
		t.Errorf("the inverse of another exposure changed %+v (found %t) or its listener", row, ok)
	}

	for i := range 2 {
		if _, err := def.Handler.Inverse(ctx, env, eff); err != nil {
			t.Fatalf("inverse, run %d: %v", i+1, err)
		}
	}
	if got := r.l.Rows["l1"].State; got != core.ListenerClosed {
		t.Errorf("listener l1 is %q after the inverse, want closed", got)
	}
	if row, ok := r.live(); ok {
		t.Errorf("exposure %+v remains after the inverse", row)
	}
	if obs, err := def.Handler.Observe(ctx, env, eff); err != nil || string(obs.Current.State) != "null" {
		t.Errorf("observation after the inverse %+v, %v; want the null state", obs, err)
	}
}
