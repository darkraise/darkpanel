package npm

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

// w12gChanges numbers the changes the passes of w12gTask add, since a change id is a primary key.
var w12gChanges atomic.Int32

// w12gTask runs one pass of the reconcile task against h. Apply runs the action's own Apply and Verify, as the engine
// would, and counts the calls.
func w12gTask(t *testing.T, h *thHost, task core.BackgroundTask, n nm, applies *int) error {
	t.Helper()
	change := fmt.Sprintf("pass-%d", w12gChanges.Add(1))
	h.addChange(change, "npm.instance.reconcile")
	env := h.env(change)
	tc := core.TaskContext{Env: env, Apply: func(ctx context.Context, action string, input any) (core.ChangeView, error) {
		*applies++
		if action != "npm.instance.reconcile" {
			t.Errorf("the task applied %q, want npm.instance.reconcile", action)
		}
		handler := n.instanceReconcile().Handler
		out, err := handler.Apply(ctx, env, nwJSON(t, input))
		if err == nil {
			err = handler.Verify(ctx, env, nwJSON(t, input), out)
		}
		if err != nil {
			coded := errcode.From(err)
			return core.ChangeView{ID: change, State: core.StateFailed,
				Error: &core.ChangeError{Code: string(coded.Code), Message: coded.Detail}}, nil
		}
		return core.ChangeView{ID: change, State: core.StateCompleted}, nil
	}}
	return task.Run(context.Background(), tc)
}

func w12gNM(h *thHost) nm {
	n := h.nm()
	n.d.resolve = w12eNoAnswer
	return n
}

func TestW12gReconcilePassAppliesOnAHit(t *testing.T) {
	h, user := w12gHit(t)
	n := w12gNM(h)
	applies := 0
	if err := w12gTask(t, h, n.reconcileTask(), n, &applies); err != nil {
		t.Fatal(err)
	}
	if applies != 1 || nwEndpoint(t, h, user.ID) != nil {
		t.Errorf("the pass applied %d times and NPM is on the network %v; want once and no", applies, nwEndpoint(t, h, user.ID) != nil)
	}
}

func TestW12gReconcileAPassWithNoHitAppliesNothing(t *testing.T) {
	h, user := nwSetup(t)
	w12aSubnet(t, h, user)
	nwConnect(t, h, user, nwStatic(w12gAddr))
	w12aSeed(h, "10.9.0.40", 3000)
	n := w12gNM(h)
	applies := 0
	if err := w12gTask(t, h, n.reconcileTask(), n, &applies); err != nil || applies != 0 {
		t.Errorf("a pass with no hit applied %d times with error %v, want none", applies, err)
	}
}

func TestW12gReconcileSkips(t *testing.T) {
	t.Run("no instance", func(t *testing.T) {
		h := newTHHost(t)
		n := w12gNM(h)
		applies := 0
		if err := w12gTask(t, h, n.reconcileTask(), n, &applies); err != nil || applies != 0 {
			t.Errorf("applied %d times with error %v, want none", applies, err)
		}
	})
	t.Run("NPM is not running", func(t *testing.T) {
		h, _ := w12gHit(t)
		if _, err := h.docker.StopContainer(context.Background(), containerName, time.Second); err != nil {
			t.Fatal(err)
		}
		n := w12gNM(h)
		applies := 0
		if err := w12gTask(t, h, n.reconcileTask(), n, &applies); err != nil || applies != 0 {
			t.Errorf("applied %d times with error %v, want none", applies, err)
		}
	})
}

func TestW12gReconcileReportsTheSameHitOnNPMsOwnNetworkOnce(t *testing.T) {
	h, _ := nwSetup(t)
	own, _ := h.docker.network(networkName)
	ep := nwEndpoint(t, h, own.ID)
	if ep == nil {
		t.Fatal("NPM is not on its own network")
	}
	w12aSeed(h, ep.IPAddress.String(), 3000)
	n := w12gNM(h)
	task := n.reconcileTask()
	applies := 0
	for range 3 {
		err := w12gTask(t, h, task, n, &applies)
		var coded *errcode.Error
		if err != nil && (!errors.As(err, &coded) || coded.Code != errcode.NPMReserved) {
			t.Fatalf("pass error %v", err)
		}
	}
	if applies != 1 {
		t.Errorf("the same hit was applied %d times, want once", applies)
	}
}

// TestW12gReconcileAppliesAgainOnceTheRememberedHitWentAway: a pass with no hit forgets the remembered hit on NPM's own
// network, so the same hit coming back is applied, and reported, again.
func TestW12gReconcileAppliesAgainOnceTheRememberedHitWentAway(t *testing.T) {
	h, _ := nwSetup(t)
	own, _ := h.docker.network(networkName)
	ep := nwEndpoint(t, h, own.ID)
	if ep == nil {
		t.Fatal("NPM is not on its own network")
	}
	w12aSeed(h, "own.example", 3000)
	answer := ep.IPAddress.String()
	n := h.nm()
	n.d.resolve = func(ctx context.Context, host string) ([]netip.Addr, error) {
		if answer == "" {
			return w12eNoAnswer(ctx, host)
		}
		return []netip.Addr{netip.MustParseAddr(answer)}, nil
	}
	task := n.reconcileTask()
	applies := 0
	pass := func() {
		t.Helper()
		err := w12gTask(t, h, task, n, &applies)
		var coded *errcode.Error
		if err != nil && (!errors.As(err, &coded) || coded.Code != errcode.NPMReserved) {
			t.Fatalf("pass error %v", err)
		}
	}
	pass()
	pass()
	answer = ""
	pass()
	answer = ep.IPAddress.String()
	pass()
	if applies != 2 {
		t.Errorf("the hit was applied %d times, want twice: once before and once after the pass with no hit", applies)
	}
}

func TestW12gReconcileRetriesAFailureThatIsNotTheOwnNetworkHit(t *testing.T) {
	for _, tc := range []struct {
		name  string
		first func() (core.ChangeView, error)
	}{
		{"the lock was taken before the change began", func() (core.ChangeView, error) {
			return core.ChangeView{}, errcode.New(errcode.LockHeld, "lock npm is held")
		}},
		{"the change failed in its handler", func() (core.ChangeView, error) {
			return core.ChangeView{State: core.StateFailed,
				Error: &core.ChangeError{Code: string(errcode.NPMUnhealthy), Message: "nginx could not be reloaded"}}, nil
		}},
		{"the change failed with npm.reserved on a network that is not darkpanel-npm", func() (core.ChangeView, error) {
			return core.ChangeView{State: core.StateFailed,
				Error: &core.ChangeError{Code: string(errcode.NPMReserved), Message: "darkpanel-npm still holds a forwarded address"}}, nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := w12gHit(t)
			n := w12gNM(h)
			task := n.reconcileTask()
			applies := 0
			env := h.env("r-first")
			first := core.TaskContext{Env: env, Apply: func(context.Context, string, any) (core.ChangeView, error) {
				applies++
				return tc.first()
			}}
			if err := task.Run(context.Background(), first); err == nil {
				t.Fatal("the failed apply returned no error to the runner")
			}
			if err := w12gTask(t, h, task, n, &applies); err != nil {
				t.Fatal(err)
			}
			if applies != 2 {
				t.Errorf("%d applies, want the same hit applied again after a failure that is not npm.reserved", applies)
			}
		})
	}
}

func TestW12gReconcileTaskDeclaration(t *testing.T) {
	h, _ := nwSetup(t)
	task := h.nm().reconcileTask()
	if task.ID != "npm.reconcile" || task.Every != 5*time.Minute || len(task.LockKeys) != 1 || task.LockKeys[0] != "npm" || task.Watch == nil {
		t.Errorf("task %+v, want npm.reconcile every 5 minutes under the npm key with a watch", task)
	}
	tasks := module{n: h.nm()}.BackgroundTasks()
	if len(tasks) != 1 || tasks[0].ID != "npm.reconcile" {
		t.Errorf("the module's background tasks %+v, want npm.reconcile", tasks)
	}
}

func TestW12gReconcileWatchWakesOnStartAndConnect(t *testing.T) {
	h, _ := nwSetup(t)
	h.docker.events = make(chan dockerapi.Event, 8)
	n := h.nm()
	npmC, _ := h.docker.container(containerName)
	npmID := npmC.ID
	var wakes atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := n.reconcileTask().Watch(ctx, h.env(""), func() { wakes.Add(1) }); err != nil {
			t.Errorf("watch: %v", err)
		}
	}()
	send := func(ev dockerapi.Event) { h.docker.events <- ev }
	send(dockerapi.Event{Type: "container", Action: "start", Actor: dockerapi.EventActor{ID: npmID, Attributes: map[string]string{"name": containerName}}})
	send(dockerapi.Event{Type: "container", Action: "start", Actor: dockerapi.EventActor{ID: "other", Attributes: map[string]string{"name": "web"}}})
	send(dockerapi.Event{Type: "network", Action: "connect", Actor: dockerapi.EventActor{ID: "n1", Attributes: map[string]string{"container": npmID}}})
	send(dockerapi.Event{Type: "network", Action: "connect", Actor: dockerapi.EventActor{ID: "n1", Attributes: map[string]string{"container": "other"}}})
	deadline := time.Now().Add(2 * time.Second)
	for wakes.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	cancel()
	wg.Wait()
	if got := wakes.Load(); got != 2 {
		t.Errorf("%d wakes, want 2 (NPM's start and NPM's connect)", got)
	}
}

// TestW12gReconcileRetriesAnExternalHitWhoseLookupFailed: a pass whose Apply could not resolve the name of a hit fails
// with npm.forward_unresolved; the next pass applies again at once instead of skipping it as a remembered failure.
func TestW12gReconcileRetriesAnExternalHitWhoseLookupFailed(t *testing.T) {
	h, user := nwSetup(t)
	w12aSubnet(t, h, user)
	nwConnect(t, h, user, nwStatic(w12gAddr))
	nwClaim(t, h, nwNet, nwClaimant, "c0", false)
	w12aSeed(h, "app.example.net", 3000)
	n := h.nm()
	lookups := 0
	n.d.resolve = func(_ context.Context, host string) ([]netip.Addr, error) {
		lookups++
		if lookups == 2 { // the first pass's Apply
			return nil, a9Timeout(host)
		}
		return []netip.Addr{netip.MustParseAddr(w12gAddr)}, nil
	}
	task := n.reconcileTask()
	applies := 0
	err := w12gTask(t, h, task, n, &applies)
	var coded *errcode.Error
	if !errors.As(err, &coded) || coded.Code != errcode.NPMForwardUnresolved {
		t.Fatalf("first pass error %v, want npm.forward_unresolved from Apply", err)
	}
	if err := w12gTask(t, h, task, n, &applies); err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if applies != 2 || nwEndpoint(t, h, user.ID) != nil {
		t.Errorf("applied %d times and NPM is on the network %v; want twice and no", applies, nwEndpoint(t, h, user.ID) != nil)
	}
}

// TestW12jReconcileThrottlesAHitWhoseLookupKeepsFailing: once the same hits failed twice in a row with
// npm.forward_unresolved, a pass that finds them again within reconcileEvery applies nothing and returns no error, so
// the runner's failure count does not hold back wakes; after reconcileEvery it applies again, and hits that went away
// and came back apply at once.
func TestW12jReconcileThrottlesAHitWhoseLookupKeepsFailing(t *testing.T) {
	h, user := nwSetup(t)
	w12aSubnet(t, h, user)
	nwConnect(t, h, user, nwStatic(w12gAddr))
	nwClaim(t, h, nwNet, nwClaimant, "c0", false)
	w12aSeed(h, "app.example.net", 3000)
	n := h.nm()
	lookups := 0
	fail := map[int]bool{}
	answer := w12gAddr
	n.d.resolve = func(_ context.Context, host string) ([]netip.Addr, error) {
		lookups++
		if fail[lookups] {
			return nil, a9Timeout(host)
		}
		return []netip.Addr{netip.MustParseAddr(answer)}, nil
	}
	task := n.reconcileTask()
	applies := 0
	pass := func(wantApplies int, wantCode errcode.Code) {
		t.Helper()
		err := w12gTask(t, h, task, n, &applies)
		if got := errcode.From(err).Code; (err == nil) != (wantCode == "") || (err != nil && got != wantCode) {
			t.Fatalf("pass error %v, want code %q", err, wantCode)
		}
		if applies != wantApplies {
			t.Fatalf("%d applies, want %d", applies, wantApplies)
		}
	}
	fail[2], fail[4] = true, true // the Apply of the first two passes
	pass(1, errcode.NPMForwardUnresolved)
	pass(2, errcode.NPMForwardUnresolved)
	pass(2, "") // throttled: the scan's lookup is the 5th
	h.clock.Advance(reconcileEvery)
	fail[7] = true
	pass(3, errcode.NPMForwardUnresolved)
	pass(3, "")
	answer = "10.9.0.99" // the hit goes away
	pass(3, "")
	answer = w12gAddr
	pass(4, "")
	if nwEndpoint(t, h, user.ID) != nil {
		t.Error("NPM stayed on the network once the lookup answered")
	}
}
