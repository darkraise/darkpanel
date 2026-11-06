package derp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/derp/server"
	"github.com/darkraise/darkpanel/internal/platform"
)

const (
	unIP          = "203.0.113.9"
	unUnitRes     = "derp.unit:darkpanel-derper.service"
	unUnitFileRes = "derp.file:/etc/systemd/system/darkpanel-derper.service"
	unBinaryRes   = "derp.file:/usr/local/lib/darkpanel/derper"
	unDirRes      = "derp.dir:/var/lib/darkpanel/derp"
	unUserRes     = "derp.user:darkpanel-derp"
	unServerRes   = "derp.server:this"
)

var (
	unResources = []string{unUnitRes, unUnitFileRes, unBinaryRes, unDirRes, unUserRes, unServerRes}
	unActive    = core.UnitState{LoadState: "loaded", ActiveState: "active", SubState: "running", MainPID: 700}
	unInactive  = core.UnitState{LoadState: "loaded", ActiveState: "inactive", SubState: "dead"}
	unGone      = core.UnitState{LoadState: "not-found"}
	unFullDiff  = []core.DiffLine{
		{Op: "-", Text: "unit darkpanel-derper.service: stop and disable"},
		{Op: "-", Text: "file /etc/systemd/system/darkpanel-derper.service"},
		{Op: "-", Text: "file /usr/local/lib/darkpanel/derper, with its .new and .prev copies"},
		{Op: "-", Text: "directory /var/lib/darkpanel/derp, with the node key and the certificates in it"},
		{Op: "-", Text: "user darkpanel-derp"},
		{Op: "-", Text: "the DERP server record of this host"},
	}
)

func unMaps(h *dpHost) {
	if h.files == nil {
		h.files = map[string][]byte{}
	}
	if h.owned == nil {
		h.owned = map[string]bool{}
	}
	if h.users == nil {
		h.users = map[string][2]int{}
	}
	if h.units.States == nil {
		h.units.States = map[string]core.UnitState{}
	}
}

// unOwnUser records the service user as the module's in the agent database, where the module records it.
func unOwnUser(t *testing.T, h *dpHost) {
	t.Helper()
	err := platform.NewOwnership(h.db).Record(context.Background(), core.OwnedEntry{
		Path: server.ServiceUser, Module: ID, Kind: core.OwnedUser, CreatedAt: h.clock.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
}

func unUserOwned(t *testing.T, h *dpHost) bool {
	t.Helper()
	owned, err := platform.NewOwnership(h.db).Owns(context.Background(), ID, server.ServiceUser)
	if err != nil {
		t.Fatal(err)
	}
	return owned
}

func unServerExists(t *testing.T, h *dpHost) bool {
	t.Helper()
	_, found, err := loadLocalServer(context.Background(), h.db.Read())
	if err != nil {
		t.Fatal(err)
	}
	return found
}

// unInstalled puts the host in the state a finished install leaves: an active unit, the unit file, the binary and
// its previous copy, the service directory with the node key and a certificate, the service user, and the row.
func unInstalled(t *testing.T, h *dpHost) {
	t.Helper()
	unMaps(h)
	h.units.States[server.UnitName] = unActive
	for _, p := range []string{server.UnitPath, server.BinaryPath, server.BinaryPrevPath, server.StateDir} {
		h.owned[p] = true
	}
	h.files[server.UnitPath] = []byte("[Unit]\n")
	h.files[server.BinaryPath] = []byte("derper")
	h.files[server.BinaryPrevPath] = []byte("derper-prev")
	h.files[server.KeyPath] = []byte("node key")
	h.files[server.CertDir+"/"+unIP+".crt"] = []byte("certificate")
	h.reads[installWantsLink] = []byte{} // the fake systemd makes no enablement link
	h.users[server.ServiceUser] = [2]int{990, 990}
	unOwnUser(t, h)
	at := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	err := insertLocalServer(context.Background(), h.db, localServer{
		ChangeID: "c0", Mode: server.ModeIP, HostName: unIP, VerifyClients: true, DerperVersion: "v1.104.1",
		BinarySHA256: strings.Repeat("a", 64), InstalledAt: at, UpdatedAt: at,
	})
	if err != nil {
		t.Fatal(err)
	}
}

// unDrive runs call while advancing the host's clock by one poll whenever it waits; tick, when set, runs once, at
// the first wait, before the clock moves.
func unDrive(t *testing.T, h *dpHost, tick func(), call func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		call()
	}()
	ticked := false
	for deadline := time.Now().Add(10 * time.Second); ; {
		select {
		case <-done:
			return
		default:
		}
		if h.clock.Pending() > 0 {
			if tick != nil && !ticked {
				ticked = true
				tick()
			}
			h.clock.Advance(uninstallPoll)
		} else {
			time.Sleep(time.Millisecond)
		}
		if time.Now().After(deadline) {
			t.Fatal("the call did not finish")
		}
	}
}

func unPlan(t *testing.T, h *dpHost) core.PlanOutcome {
	t.Helper()
	out, err := h.dp().serverUninstall().Handler.Plan(context.Background(), h.env(""), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// unApply applies derp.server.uninstall as change c1.
func unApply(t *testing.T, h *dpHost, tick func()) (ServerUninstalled, error) {
	t.Helper()
	var raw json.RawMessage
	var err error
	unDrive(t, h, tick, func() {
		raw, err = h.dp().serverUninstall().Handler.Apply(context.Background(), h.env("c1"), json.RawMessage(`{}`))
	})
	if err != nil {
		return ServerUninstalled{}, err
	}
	var out ServerUninstalled
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("the answer %s does not decode as a ServerUninstalled: %v", raw, err)
	}
	return out, nil
}

// unStops makes the unit report inactive, as it does once systemd has stopped it.
func unStops(h *dpHost) func() {
	return func() { h.units.States[server.UnitName] = unInactive }
}

func unEffects(t *testing.T, h *dpHost) []core.Effect {
	t.Helper()
	effects, err := h.env("c1").Effects.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return effects
}

func unSame(t *testing.T, got json.RawMessage, want string) bool {
	t.Helper()
	var a, b any
	if err := json.Unmarshal(got, &a); err != nil {
		t.Fatalf("document %s: %v", got, err)
	}
	if err := json.Unmarshal([]byte(want), &b); err != nil {
		t.Fatalf("document %s: %v", want, err)
	}
	return reflect.DeepEqual(a, b)
}

// unDoc is the state document of resource: the unit's holds its active and enabled flags, every other one its
// presence.
func unDoc(resource string, on bool) string {
	if resource == unUnitRes {
		return fmt.Sprintf(`{"active":%t,"enabled":%t}`, on, on)
	}
	return fmt.Sprintf(`{"present":%t}`, on)
}

func TestUnDefinition(t *testing.T) {
	def := newDPHost(t).dp().serverUninstall()
	s := def.Spec
	if s.Name != "derp.server.uninstall" || s.Version != 1 || s.Class != core.ClassCompensable || s.Side != core.SideAgent ||
		!reflect.DeepEqual(s.LockKeys, []string{"derp"}) || s.CompensatedBy != "derp.server.install" || !s.Converges ||
		s.ApplyTimeout != 45*time.Second || s.LongRunning || s.Task || s.Available != nil || s.ForwardOnly {
		t.Errorf("spec %+v, want a compensable agent action locking derp, compensated by derp.server.install, converging, 45 s, with no availability check", s)
	}
	if def.InputType != reflect.TypeFor[ServerUninstallInput]() || def.OutputType != reflect.TypeFor[ServerUninstalled]() {
		t.Errorf("input %v and output %v, want ServerUninstallInput and ServerUninstalled", def.InputType, def.OutputType)
	}
	if h := def.Handler; h.Plan == nil || h.Apply == nil || h.Verify == nil || h.Observe == nil || h.Inverse != nil || h.Task != nil {
		t.Error("the handler needs Plan, Apply, Verify and Observe, and no Inverse or Task")
	}
}

func TestUnPlan(t *testing.T) {
	t.Run("nothing installed plans nothing", func(t *testing.T) {
		h := newDPHost(t)
		unMaps(h)
		h.units.States[server.UnitName] = unGone
		out := unPlan(t, h)
		if len(out.Diff) != 0 || len(out.Guards) != 0 || out.Confirmation != "" {
			t.Errorf("plan %+v, want an empty diff, no guard and no confirmation", out)
		}
	})
	t.Run("an installed server lists every removal", func(t *testing.T) {
		h := newDPHost(t)
		unInstalled(t, h)
		out := unPlan(t, h)
		if !reflect.DeepEqual(out.Diff, unFullDiff) {
			t.Errorf("diff\n got %+v\nwant %+v", out.Diff, unFullDiff)
		}
		if out.StateHash == "" || out.StateHash != unPlan(t, h).StateHash || out.Confirmation != "" {
			t.Errorf("state hash %q (stable %v) and confirmation %q, want a stable hash and no confirmation",
				out.StateHash, out.StateHash == unPlan(t, h).StateHash, out.Confirmation)
		}
	})
	t.Run("a loaded unit that is not running is only disabled, and the hash follows it", func(t *testing.T) {
		h := newDPHost(t)
		unInstalled(t, h)
		running := unPlan(t, h)
		h.units.States[server.UnitName] = unInactive
		stopped := unPlan(t, h)
		want := append([]core.DiffLine{{Op: "-", Text: "unit darkpanel-derper.service: disable"}}, unFullDiff[1:]...)
		if !reflect.DeepEqual(stopped.Diff, want) {
			t.Errorf("diff\n got %+v\nwant %+v", stopped.Diff, want)
		}
		if stopped.StateHash == running.StateHash {
			t.Error("the state hash did not change when the unit stopped")
		}
	})
	t.Run("a unit systemd does not know has no line", func(t *testing.T) {
		h := newDPHost(t)
		unInstalled(t, h)
		h.units.States[server.UnitName] = unGone
		if out := unPlan(t, h); !reflect.DeepEqual(out.Diff, unFullDiff[1:]) {
			t.Errorf("diff\n got %+v\nwant %+v", out.Diff, unFullDiff[1:])
		}
	})
}

// TestUnApplyRemovesEverything: the removals run in install's reverse order, each journaled as a delete effect,
// and the unit is stopped and awaited before its user is deleted.
func TestUnApplyRemovesEverything(t *testing.T) {
	h := newDPHost(t)
	unInstalled(t, h)
	out, err := unApply(t, h, unStops(h))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(out.Removed, unResources) {
		t.Errorf("removed %v, want %v", out.Removed, unResources)
	}
	wantCalls := []string{"stop " + server.UnitName, "disable " + server.UnitName, "reload"}
	if !reflect.DeepEqual(h.units.Calls, wantCalls) {
		t.Errorf("unit calls %q, want %q", h.units.Calls, wantCalls)
	}
	if want := [][]string{{"/usr/sbin/userdel", server.ServiceUser}}; !reflect.DeepEqual(h.ran, want) {
		t.Errorf("commands run %q, want %q", h.ran, want)
	}
	for _, p := range []string{server.UnitPath, server.BinaryPath, server.BinaryNewPath, server.BinaryPrevPath} {
		if _, ok := h.files[p]; ok || h.owned[p] {
			t.Errorf("%s is still there (file %v, owned %v)", p, ok, h.owned[p])
		}
	}
	if h.owned[server.StateDir] {
		t.Error("the service directory is still recorded as the module's")
	}
	if unUserOwned(t, h) || unServerExists(t, h) {
		t.Error("the user record or the server row is still there")
	}

	effects := unEffects(t, h)
	if len(effects) != len(unResources) {
		t.Fatalf("%d effects journaled, want %d", len(effects), len(unResources))
	}
	for i, e := range effects {
		res := unResources[i]
		if e.Resource != res || e.Kind != core.EffectDelete || !e.Local || e.State != core.EffectMade {
			t.Errorf("effect %d = %s %s local %v %s, want a made local delete of %s", i+1, e.Kind, e.Resource, e.Local, e.State, res)
			continue
		}
		if !unSame(t, e.Before.State, unDoc(res, true)) || e.After == nil || !unSame(t, e.After.State, unDoc(res, false)) ||
			e.Post == nil || !unSame(t, e.Post.State, unDoc(res, false)) {
			t.Errorf("effect %d (%s): before %s, after %+v, post %+v, want %s then %s", i+1, res, e.Before.State, e.After, e.Post, unDoc(res, true), unDoc(res, false))
		}
	}
}

// TestUnApplyConverges: running the action again, or from a state an earlier run left half way, removes what is
// left and nothing more.
func TestUnApplyConverges(t *testing.T) {
	t.Run("again after a complete run", func(t *testing.T) {
		h := newDPHost(t)
		unInstalled(t, h)
		if _, err := unApply(t, h, unStops(h)); err != nil {
			t.Fatal(err)
		}
		h.units.States[server.UnitName] = unGone
		calls, ran, effects := len(h.units.Calls), len(h.ran), len(unEffects(t, h))
		out, err := unApply(t, h, nil)
		if err != nil {
			t.Fatal(err)
		}
		if out.Removed == nil || len(out.Removed) != 0 {
			t.Errorf("removed %#v, want an empty, non-nil list", out.Removed)
		}
		if len(h.units.Calls) != calls || len(h.ran) != ran || len(unEffects(t, h)) != effects {
			t.Errorf("the second run called %v, ran %v and journaled %d effects, want nothing new", h.units.Calls[calls:], h.ran[ran:], len(unEffects(t, h))-effects)
		}
	})
	t.Run("the unit file and the binary are already gone", func(t *testing.T) {
		h := newDPHost(t)
		unInstalled(t, h)
		h.units.States[server.UnitName] = unGone
		delete(h.owned, server.UnitPath)
		delete(h.owned, server.BinaryPath)
		delete(h.owned, server.BinaryPrevPath)
		out, err := unApply(t, h, nil)
		if err != nil {
			t.Fatal(err)
		}
		if want := []string{unDirRes, unUserRes, unServerRes}; !reflect.DeepEqual(out.Removed, want) {
			t.Errorf("removed %v, want %v", out.Removed, want)
		}
		if len(h.units.Calls) != 0 {
			t.Errorf("unit calls %q, want none", h.units.Calls)
		}
	})
	t.Run("a loaded unit that is not running is disabled without a stop", func(t *testing.T) {
		h := newDPHost(t)
		unInstalled(t, h)
		h.units.States[server.UnitName] = unInactive
		if _, err := unApply(t, h, nil); err != nil {
			t.Fatal(err)
		}
		if want := []string{"disable " + server.UnitName, "reload"}; !reflect.DeepEqual(h.units.Calls, want) {
			t.Errorf("unit calls %q, want %q", h.units.Calls, want)
		}
	})
	t.Run("the user is recorded but already deleted", func(t *testing.T) {
		h := newDPHost(t)
		unInstalled(t, h)
		h.units.States[server.UnitName] = unInactive
		delete(h.users, server.ServiceUser)
		out, err := unApply(t, h, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(out.Removed, unResources) {
			t.Errorf("removed %v, want %v", out.Removed, unResources)
		}
		if len(h.ran) != 0 || unUserOwned(t, h) {
			t.Errorf("commands run %q, user still recorded %v, want no userdel and the record forgotten", h.ran, unUserOwned(t, h))
		}
	})
}

// TestUnLeavesWhatIsNotOurs: a unit file and a user the module did not record are not its to remove.
func TestUnLeavesWhatIsNotOurs(t *testing.T) {
	h := newDPHost(t)
	unMaps(h)
	h.units.States[server.UnitName] = unGone
	h.files[server.UnitPath] = []byte("[Unit]\nDescription=someone else's\n")
	h.users[server.ServiceUser] = [2]int{990, 990}
	if out := unPlan(t, h); len(out.Diff) != 0 {
		t.Errorf("diff %+v, want none", out.Diff)
	}
	out, err := unApply(t, h, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Removed) != 0 || len(h.ran) != 0 || len(h.units.Calls) != 0 {
		t.Errorf("removed %v, ran %q, unit calls %q, want nothing", out.Removed, h.ran, h.units.Calls)
	}
	if _, ok := h.files[server.UnitPath]; !ok {
		t.Error("the unit file that is not the module's was removed")
	}
}

// TestUnStopTimeout: a unit that does not stop within the wait stops the removal there, with the first effect
// still intended for the engine to settle.
func TestUnStopTimeout(t *testing.T) {
	h := newDPHost(t)
	unInstalled(t, h)
	start := h.clock.Now()
	_, err := unApply(t, h, nil)
	if err == nil || !strings.Contains(err.Error(), "did not stop") {
		t.Fatalf("err = %v, want the unit did not stop", err)
	}
	if waited := h.clock.Now().Sub(start); waited < uninstallStopWait {
		t.Errorf("gave up after %s, want at least %s", waited, uninstallStopWait)
	}
	effects := unEffects(t, h)
	if len(effects) != 1 || effects[0].Resource != unUnitRes || effects[0].State != core.EffectIntended {
		t.Errorf("effects %+v, want the unit's alone, intended", effects)
	}
	if want := []string{"stop " + server.UnitName}; !reflect.DeepEqual(h.units.Calls, want) {
		t.Errorf("unit calls %q, want %q", h.units.Calls, want)
	}
	if len(h.ran) != 0 || !h.owned[server.UnitPath] || !unUserOwned(t, h) || !unServerExists(t, h) {
		t.Error("something was removed after the unit failed to stop")
	}
}

// TestUnUserdelFails: a refused userdel leaves its effect intended and the row in place, after the effects before
// it were made.
func TestUnUserdelFails(t *testing.T) {
	h := newDPHost(t)
	unInstalled(t, h)
	h.units.States[server.UnitName] = unInactive
	h.script("/usr/sbin/userdel darkpanel-derp", dpRun{res: core.CmdResult{
		ExitCode: 8, Stderr: []byte("userdel: user darkpanel-derp is currently used by process 123\n"),
	}})
	_, err := unApply(t, h, nil)
	if err == nil || !strings.Contains(err.Error(), "userdel") || !strings.Contains(err.Error(), "currently used by process 123") {
		t.Fatalf("err = %v, want the userdel failure with its last stderr line", err)
	}
	effects := unEffects(t, h)
	if len(effects) != 5 {
		t.Fatalf("%d effects, want 5 (the row's is not journaled yet)", len(effects))
	}
	for i, e := range effects {
		want := core.EffectMade
		if i == 4 {
			want = core.EffectIntended
		}
		if e.Resource != unResources[i] || e.State != want {
			t.Errorf("effect %d = %s %s, want %s %s", i+1, e.Resource, e.State, unResources[i], want)
		}
	}
	if !unServerExists(t, h) || !unUserOwned(t, h) {
		t.Error("the row or the user record was removed after userdel failed")
	}
}

// TestUnObserve: the observation of each effect's resource is the document its effect expects, present before the
// removal and absent after.
func TestUnObserve(t *testing.T) {
	h := newDPHost(t)
	unInstalled(t, h)
	observe := func(res string) core.Observation {
		t.Helper()
		obs, err := h.dp().serverUninstall().Handler.Observe(context.Background(), h.env(""), core.Effect{Resource: res, Kind: core.EffectDelete})
		if err != nil {
			t.Fatalf("observe %s: %v", res, err)
		}
		return obs
	}
	check := func(on bool) {
		t.Helper()
		for _, res := range unResources {
			obs := observe(res)
			if !unSame(t, obs.Current.State, unDoc(res, on)) || obs.Matches != 1 || !obs.Exclusive {
				t.Errorf("observe %s = %s (matches %d, exclusive %v), want %s", res, obs.Current.State, obs.Matches, obs.Exclusive, unDoc(res, on))
			}
		}
	}
	check(true)
	if _, err := unApply(t, h, unStops(h)); err != nil {
		t.Fatal(err)
	}
	delete(h.reads, installWantsLink) // systemctl disable removed the enablement link
	check(false)
	if _, err := h.dp().serverUninstall().Handler.Observe(context.Background(), h.env(""), core.Effect{Resource: "derp.file:/etc/passwd"}); err == nil {
		t.Error("observe of a resource the action never journals succeeded")
	}
}

func TestUnVerify(t *testing.T) {
	ctx := context.Background()
	h := newDPHost(t)
	unInstalled(t, h)
	verify := func() error {
		return h.dp().serverUninstall().Handler.Verify(ctx, h.env("c1"), json.RawMessage(`{}`), json.RawMessage(`{"removed":[]}`))
	}
	if err := verify(); err == nil {
		t.Fatal("verify passed on a host that still has the server")
	}
	if _, err := unApply(t, h, unStops(h)); err != nil {
		t.Fatal(err)
	}
	if err := verify(); err != nil {
		t.Fatalf("verify after the removal: %v", err)
	}
	h.owned[server.BinaryPath] = true
	if err := verify(); err == nil || !strings.Contains(err.Error(), server.BinaryPath) {
		t.Errorf("err = %v, want the binary named", err)
	}
	delete(h.owned, server.BinaryPath)
	h.units.States[server.UnitName] = unActive
	if err := verify(); err == nil || !strings.Contains(err.Error(), server.UnitName) {
		t.Errorf("err = %v, want the unit named", err)
	}
}

// TestW10DuUninstallLeavesAUserItDidNotCreate: an install whose useradd failed records no ownership, so the user
// someone else creates afterwards is not the module's to delete.
func TestW10DuUninstallLeavesAUserItDidNotCreate(t *testing.T) {
	ctx := context.Background()
	s := inNewSetup(t)
	s.h.script(inUseradd, dpRun{res: core.CmdResult{ExitCode: 9, Stderr: []byte("useradd: cannot lock /etc/passwd")}})
	if _, err := s.d.serverInstall().Handler.Task(ctx, s.env, inInput("ip", inIP, nil)); err == nil {
		t.Fatal("the install succeeded although useradd failed")
	}
	s.h.users[server.ServiceUser] = [2]int{900, 900}
	unMaps(s.h)
	s.h.units.States[server.UnitName] = unGone
	if out := unPlan(t, s.h); len(out.Diff) != 0 {
		t.Errorf("plan %+v, want nothing to remove", out.Diff)
	}
	out, err := unApply(t, s.h, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.h.users[server.ServiceUser]; !ok || len(out.Removed) != 0 ||
		slices.ContainsFunc(s.h.ran, func(v []string) bool { return v[0] == installUserdel }) {
		t.Errorf("user kept: %v, removed %v, commands run %q, want the user kept and no userdel", ok, out.Removed, s.h.ran)
	}
}

// w10duFailingUnits is a systemd that cannot be reached.
type w10duFailingUnits struct{ core.Units }

func (w10duFailingUnits) State(context.Context, string) (core.UnitState, error) {
	return core.UnitState{}, errors.New("w10du: systemd is unreachable")
}

// TestW10DuUninstallApplyReadFailureIsDefinite: a failed read of the facts changed nothing, so Apply reports it
// as definite, as Plan does.
func TestW10DuUninstallApplyReadFailureIsDefinite(t *testing.T) {
	h := newDPHost(t)
	unInstalled(t, h)
	env := h.env("c1")
	env.Units = w10duFailingUnits{env.Units}
	_, err := h.dp().serverUninstall().Handler.Apply(context.Background(), env, json.RawMessage(`{}`))
	if err == nil || !core.IsDefinite(err) {
		t.Errorf("Apply = %v, want a definite error", err)
	}
	if effects := unEffects(t, h); len(effects) != 0 {
		t.Errorf("effects %+v, want none journaled", effects)
	}
}

// TestW10DuUninstallUnitEffect: the unit's effect changes the unit's document also when the unit is loaded but not
// running, and a unit that is neither running nor enabled has nothing to remove.
func TestW10DuUninstallUnitEffect(t *testing.T) {
	t.Run("a loaded unit that is not running", func(t *testing.T) {
		h := newDPHost(t)
		unInstalled(t, h)
		h.units.States[server.UnitName] = unInactive
		if _, err := unApply(t, h, nil); err != nil {
			t.Fatal(err)
		}
		e := unEffects(t, h)[0]
		if e.Resource != unUnitRes || e.After == nil || !unSame(t, e.Before.State, `{"active":false,"enabled":true}`) ||
			!unSame(t, e.After.State, `{"active":false,"enabled":false}`) {
			t.Errorf("effect %s: before %s, after %+v, want enabled before and not after, neither running", e.Resource, e.Before.State, e.After)
		}
	})
	t.Run("a loaded unit that is neither running nor enabled", func(t *testing.T) {
		h := newDPHost(t)
		unInstalled(t, h)
		h.units.States[server.UnitName] = unInactive
		delete(h.reads, installWantsLink)
		if out := unPlan(t, h); !reflect.DeepEqual(out.Diff, unFullDiff[1:]) {
			t.Errorf("diff\n got %+v\nwant %+v", out.Diff, unFullDiff[1:])
		}
		if _, err := unApply(t, h, nil); err != nil {
			t.Fatal(err)
		}
		if want := []string{"reload"}; !reflect.DeepEqual(h.units.Calls, want) {
			t.Errorf("unit calls %q, want %q", h.units.Calls, want)
		}
	})
}
