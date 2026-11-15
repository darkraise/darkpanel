package npm

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

// fakeExec scripts the Engine under the reloader. exits maps a command line (joined by spaces) to its exit code; a
// command in hang never finishes; a command in noCode finishes with no exit code. workers is the list of worker pids
// that ContainerTop answers before the reload signal, and after is the list it answers once the reload was started;
// duringTest, when set, is the list it answers once the test was started and before the signal. afterParent is the
// parent pid of the workers in after, the master's ("10") when empty. startErr is what ExecStart answers for a
// command, which still starts; inspectErr1 is what the first ExecInspect of a command answers.
type fakeExec struct {
	mu          sync.Mutex
	env         []string
	inspectErr  error
	exits       map[string]int
	hang        map[string]bool
	noCode      map[string]bool
	gone        map[string]bool
	startErr    map[string]error
	inspectErr1 map[string]error
	workers     []string
	duringTest  []string
	after       []string
	afterParent string
	tested      bool
	created     []string
	cmds        [][]string
	users       []string
	containers  []string
	stopped     bool
	ids         map[string]string
	started     map[string]bool
	reloaded    bool
}

func newFakeExec() *fakeExec {
	return &fakeExec{
		exits: map[string]int{}, hang: map[string]bool{}, noCode: map[string]bool{}, gone: map[string]bool{}, startErr: map[string]error{}, inspectErr1: map[string]error{}, ids: map[string]string{}, started: map[string]bool{},
		workers: []string{"11"}, after: []string{"12"},
	}
}

func (f *fakeExec) InspectContainer(_ context.Context, _ string) (dockerapi.ContainerJSON, error) {
	if f.inspectErr != nil {
		return dockerapi.ContainerJSON{}, f.inspectErr
	}
	return dockerapi.ContainerJSON{Config: dockerapi.ContainerConfig{Env: f.env}, State: dockerapi.ContainerState{Running: !f.stopped}}, nil
}

func (f *fakeExec) ExecCreate(_ context.Context, container string, o dockerapi.ExecCreate) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	line := strings.Join(o.Cmd, " ")
	id := "x" + line
	f.created = append(f.created, line)
	f.cmds = append(f.cmds, o.Cmd)
	f.users = append(f.users, o.User)
	f.containers = append(f.containers, container)
	f.ids[id] = line
	return id, nil
}

func (f *fakeExec) ExecStart(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started[id] = true
	switch f.ids[id] {
	case "nginx -s reload":
		f.reloaded = true
	case testCmd:
		f.tested = true
	}
	return f.startErr[f.ids[id]]
}

func (f *fakeExec) ExecInspect(_ context.Context, id string) (dockerapi.ExecInspect, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	line := f.ids[id]
	if err := f.inspectErr1[line]; err != nil {
		delete(f.inspectErr1, line)
		return dockerapi.ExecInspect{}, err
	}
	if f.gone[line] {
		delete(f.gone, line)
		return dockerapi.ExecInspect{}, &dockerapi.APIError{StatusCode: 404, Message: "No such exec instance: " + id}
	}
	switch {
	case f.hang[line]:
		return dockerapi.ExecInspect{ID: id, Running: true}, nil
	case f.noCode[line]:
		return dockerapi.ExecInspect{ID: id}, nil
	}
	code := f.exits[line]
	return dockerapi.ExecInspect{ID: id, ExitCode: &code}, nil
}

func (f *fakeExec) ContainerTop(_ context.Context, _, _ string) (dockerapi.TopResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	pids, parent := f.workers, "10"
	switch {
	case f.reloaded:
		pids = f.after
		if f.afterParent != "" {
			parent = f.afterParent
		}
	case f.tested && f.duringTest != nil:
		pids = f.duringTest
	}
	rows := [][]string{{"10", "1", "nginx: master process nginx"}}
	for _, p := range pids {
		rows = append(rows, []string{p, parent, "nginx: worker process"})
	}
	return dockerapi.TopResult{Titles: []string{"PID", "PPID", "COMMAND"}, Processes: rows}, nil
}

const (
	testCmd   = "nginx -t -g error_log off;"
	reloadCmd = "nginx -s reload"
)

func quickReloader(f *fakeExec) *nginxReloader {
	r := newNginxReloader(f)
	r.wait, r.poll = 300*time.Millisecond, 10*time.Millisecond
	return r
}

func TestNginxReloaderConfirmsANewWorker(t *testing.T) {
	f := newFakeExec()
	if err := quickReloader(f).Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.cmds) != 2 || !slices.Equal(f.cmds[0], []string{"nginx", "-t", "-g", "error_log off;"}) || !slices.Equal(f.cmds[1], []string{"nginx", "-s", "reload"}) {
		t.Errorf("commands %q, want the test and then the reload as two exact argument lists", f.cmds)
	}
	for i, c := range f.containers {
		if c != containerName || f.users[i] != "0:0" {
			t.Errorf("command %d ran in %q as %q, want %q as 0:0", i, c, f.users[i], containerName)
		}
	}
}

func TestNginxReloaderFailures(t *testing.T) {
	for _, tc := range []struct {
		name     string
		arrange  func(f *fakeExec)
		wantTest bool
		started  int
	}{
		{"the configuration test fails", func(f *fakeExec) { f.exits[testCmd] = 1 }, true, 1},
		{"the reload signal fails", func(f *fakeExec) { f.exits[reloadCmd] = 1 }, false, 2},
		{"the test returns no exit code", func(f *fakeExec) { f.noCode[testCmd] = true }, false, 1},
		{"the test is still running at the bound", func(f *fakeExec) { f.hang[testCmd] = true }, false, 1},
		{"no new worker follows the signal", func(f *fakeExec) { f.after = f.workers }, false, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeExec()
			tc.arrange(f)
			err := quickReloader(f).Reload(context.Background())
			if err == nil {
				t.Fatal("the reload was confirmed")
			}
			if errors.Is(err, errNginxTest) != tc.wantTest {
				t.Errorf("errors.Is(err, errNginxTest) = %v for %v, want %v", !tc.wantTest, err, tc.wantTest)
			}
			if len(f.created) != tc.started {
				t.Errorf("%d commands were created, want %d", len(f.created), tc.started)
			}
		})
	}
}

func TestNginxReloaderUser(t *testing.T) {
	f := newFakeExec()
	f.env = []string{"PATH=/bin", "PUID=1000", "PGID=1001"}
	if err := quickReloader(f).Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.users[0] != "1000:1001" {
		t.Errorf("user %q, want 1000:1001", f.users[0])
	}
	f = newFakeExec()
	f.env = []string{"PUID=abc"}
	if err := quickReloader(f).Reload(context.Background()); err == nil || len(f.created) != 0 {
		t.Errorf("a non-numeric PUID ran %d commands with error %v, want none and an error", len(f.created), err)
	}
	f = newFakeExec()
	f.inspectErr = dockerapi.ErrUnavailable
	if err := quickReloader(f).Reload(context.Background()); err == nil || errors.Is(err, errNginxTest) {
		t.Errorf("an unreadable container gave %v, want a failure that is not the test's", err)
	}
	f = newFakeExec()
	f.inspectErr = &dockerapi.APIError{StatusCode: 404, Message: "No such container: darkpanel-npm"}
	if err := quickReloader(f).Reload(context.Background()); err == nil || errors.Is(err, errNginxTest) || len(f.created) != 0 {
		t.Errorf("a missing container ran %d commands with error %v, want none and a failure that is not the test's", len(f.created), err)
	}
	f = newFakeExec()
	f.stopped = true
	if err := quickReloader(f).Reload(context.Background()); err == nil || errors.Is(err, errNginxTest) || len(f.created) != 0 {
		t.Errorf("a stopped container ran %d commands with error %v, want none and a failure that is not the test's", len(f.created), err)
	}
}

func TestNginxReloaderRefusesWhileAnEarlierExecRuns(t *testing.T) {
	f := newFakeExec()
	f.hang[testCmd] = true
	r := quickReloader(f)
	if err := r.Reload(context.Background()); err == nil {
		t.Fatal("a hung test was confirmed")
	}
	before := len(f.created)
	err := r.Reload(context.Background())
	if !errors.Is(err, errNginxBusy) {
		t.Fatalf("second reload = %v, want errNginxBusy", err)
	}
	if len(f.created) != before {
		t.Error("a second exec was created while the earlier one runs")
	}
	delete(f.hang, testCmd)
	if err := r.Reload(context.Background()); err != nil {
		t.Fatalf("after the earlier exec ended: %v", err)
	}
}

func TestNginxReloaderForgetsAPendingExecTheEngineNoLongerKnows(t *testing.T) {
	f := newFakeExec()
	f.hang[testCmd] = true
	r := quickReloader(f)
	if err := r.Reload(context.Background()); err == nil {
		t.Fatal("a hung test was confirmed")
	}
	delete(f.hang, testCmd)
	f.gone[testCmd] = true
	if err := r.Reload(context.Background()); err != nil {
		t.Fatalf("the reload after the Engine forgot the pending exec = %v, want nil", err)
	}
	if r.pending != "" {
		t.Errorf("pending = %q after a confirmed reload, want none", r.pending)
	}
}

func TestNginxReloaderKeepsAnExecPendingAfterAnUncertainAnswer(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(f *fakeExec)
	}{
		{"an inspection error before the deadline", func(f *fakeExec) {
			f.inspectErr1[testCmd] = errors.New("connection reset by peer")
		}},
		{"a start whose answer was lost", func(f *fakeExec) { f.startErr[testCmd] = errors.New("connection reset by peer") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeExec()
			f.hang[testCmd] = true
			tc.set(f)
			r := quickReloader(f)
			if err := r.Reload(context.Background()); err == nil {
				t.Fatal("a reload with an uncertain answer was confirmed")
			}
			if err := r.Reload(context.Background()); !errors.Is(err, errNginxBusy) {
				t.Fatalf("the next reload = %v, want errNginxBusy while the earlier exec may still run", err)
			}
		})
	}
}

func TestNginxReloaderNeedsANewWorkerOfTheMasterAfterTheSignal(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(f *fakeExec)
	}{
		{"a new worker under another parent", func(f *fakeExec) { f.afterParent = "99" }},
		{"a worker that appeared during the test", func(f *fakeExec) {
			f.duringTest, f.after = []string{"12"}, []string{"12"}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeExec()
			tc.set(f)
			if err := quickReloader(f).Reload(context.Background()); err == nil {
				t.Fatal("the reload was confirmed by a worker the signal did not start")
			}
		})
	}
}

func TestNginxReloaderLeavesNothingPendingAfterARefusedStart(t *testing.T) {
	f := newFakeExec()
	f.startErr[testCmd] = &dockerapi.APIError{StatusCode: 409, Message: "container is paused"}
	r := quickReloader(f)
	if err := r.Reload(context.Background()); err == nil {
		t.Fatal("a refused start was confirmed")
	}
	delete(f.startErr, testCmd)
	f.hang[testCmd] = true
	before := len(f.created)
	if err := r.Reload(context.Background()); errors.Is(err, errNginxBusy) {
		t.Fatal("the reload after a refused start = errNginxBusy, want a new exec: a refused start did not run")
	}
	if len(f.created) == before {
		t.Error("no exec was created after a refused start")
	}
}

func TestNginxReloaderKeepsPendingAnExecWithNoExitCode(t *testing.T) {
	f := newFakeExec()
	f.hang[testCmd] = true
	r := quickReloader(f)
	if err := r.Reload(context.Background()); err == nil {
		t.Fatal("a hung test was confirmed")
	}
	delete(f.hang, testCmd)
	f.noCode[testCmd] = true
	if err := r.Reload(context.Background()); !errors.Is(err, errNginxBusy) {
		t.Fatalf("the reload while the pending exec has no exit code = %v, want errNginxBusy: its start may still be in flight", err)
	}
}

func TestNginxReloaderDropsAPendingExecAfterItsExpiry(t *testing.T) {
	f := newFakeExec()
	f.hang[testCmd] = true
	r := quickReloader(f)
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	r.now = func() time.Time { return now }
	if err := r.Reload(context.Background()); err == nil {
		t.Fatal("a hung test was confirmed")
	}
	now = now.Add(nginxPendingExpiry - time.Second)
	if err := r.Reload(context.Background()); !errors.Is(err, errNginxBusy) {
		t.Fatalf("the reload before the expiry = %v, want errNginxBusy", err)
	}
	now = now.Add(2 * time.Second)
	before := len(f.created)
	if err := r.Reload(context.Background()); errors.Is(err, errNginxBusy) {
		t.Fatal("the reload after the expiry = errNginxBusy, want the pending exec dropped")
	}
	if len(f.created) == before {
		t.Error("no exec was created after the pending exec expired")
	}
}
