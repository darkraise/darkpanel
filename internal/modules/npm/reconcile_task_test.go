package npm

import (
	"context"
	"errors"
	"fmt"
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

// w12gTask runs one pass of the reconcile task against h. Apply runs the action's own handler, as the engine would, and
// counts the calls.
func w12gTask(t *testing.T, h *thHost, task core.BackgroundTask, n nm, applies *int) error {
	t.Helper()
	change := fmt.Sprintf("r%d", w12gChanges.Add(1))
	h.addChange(change, "npm.instance.reconcile")
	env := h.env(change)
	tc := core.TaskContext{Env: env, Apply: func(ctx context.Context, action string, input any) (core.ChangeView, error) {
		*applies++
		if action != "npm.instance.reconcile" {
			t.Errorf("the task applied %q, want npm.instance.reconcile", action)
		}
		_, err := n.instanceReconcile().Handler.Apply(ctx, env, nwJSON(t, input))
		return core.ChangeView{}, err
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
