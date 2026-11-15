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
// that ContainerTop answers before the reload signal, and after is the list it answers once the reload was started.
type fakeExec struct {
	mu         sync.Mutex
	env        []string
	inspectErr error
	exits      map[string]int
	hang       map[string]bool
	noCode     map[string]bool
	workers    []string
	after      []string
	created    []string
	cmds       [][]string
	users      []string
	containers []string
	stopped    bool
	ids        map[string]string
	started    map[string]bool
	reloaded   bool
}

func newFakeExec() *fakeExec {
	return &fakeExec{
		exits: map[string]int{}, hang: map[string]bool{}, noCode: map[string]bool{}, ids: map[string]string{}, started: map[string]bool{},
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
	if f.ids[id] == "nginx -s reload" {
		f.reloaded = true
	}
	return nil
}

func (f *fakeExec) ExecInspect(_ context.Context, id string) (dockerapi.ExecInspect, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	line := f.ids[id]
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
	pids := f.workers
	if f.reloaded {
		pids = f.after
	}
	rows := [][]string{{"10", "1", "nginx: master process nginx"}}
	for _, p := range pids {
		rows = append(rows, []string{p, "10", "nginx: worker process"})
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
