package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/agent/runtime"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

const qaPausedNote = "jobs is off on this host; the job starts when jobs is turned on"

// qaHost is the rig, whose unit client refuses Start, Restart and Enable of
// a job unit while off is set, as the runtime's guard does while jobs is off
// or denied; the module's own off and on hooks run as the runtime calls them.
type qaHost struct {
	t *testing.T
	r *rig
}

func newQAHost(t *testing.T) *qaHost {
	t.Helper()
	r := newRig(t)
	r.units.states = map[string]core.UnitState{}
	return &qaHost{t: t, r: r}
}

// turnOff is the runtime's sequence for jobs turned off: the flag first,
// then the module's off hook.
func (h *qaHost) turnOff() {
	h.t.Helper()
	h.r.units.mu.Lock()
	h.r.units.off = true
	h.r.units.mu.Unlock()
	if err := h.r.m.OnEnabled(context.Background(), h.r.env(adminP), false); err != nil {
		h.t.Errorf("off hook: %v", err)
	}
}

func (h *qaHost) turnOn() {
	h.t.Helper()
	h.r.units.mu.Lock()
	h.r.units.off = false
	h.r.units.refusals = 0
	h.r.units.mu.Unlock()
	if err := h.r.m.OnEnabled(context.Background(), h.r.env(adminP), true); err != nil {
		h.t.Errorf("on hook: %v", err)
	}
}

func (h *qaHost) planApply(action, input string) (core.ChangeView, error) {
	h.t.Helper()
	ctx := context.Background()
	pv, err := h.r.engine.Plan(ctx, adminP, core.ActionRef{Name: action, Version: 1}, json.RawMessage(input), core.PlanOptions{})
	if err != nil {
		return core.ChangeView{}, err
	}
	return h.r.engine.Apply(ctx, adminP, pv.PlanID, core.ApplyOptions{})
}

func (h *qaHost) note(cv core.ChangeView) string {
	h.t.Helper()
	var out struct {
		Note string `json:"note"`
	}
	if err := json.Unmarshal(cv.Output, &out); err != nil {
		h.t.Fatalf("output %s: %v", cv.Output, err)
	}
	return out.Note
}

// timerEnabled is whether the last Enable or Disable the unit client got for
// the timer was an Enable.
func (h *qaHost) timerEnabled(timer string) bool {
	h.r.units.mu.Lock()
	defer h.r.units.mu.Unlock()
	enabled := false
	for _, c := range h.r.units.calls {
		switch {
		case strings.HasPrefix(c, "Enable ") && strings.Contains(c, timer):
			enabled = true
		case strings.HasPrefix(c, "Disable ") && strings.Contains(c, timer):
			enabled = false
		}
	}
	return enabled
}

func (h *qaHost) active(timer string) bool {
	h.t.Helper()
	st, err := h.r.units.State(context.Background(), timer)
	if err != nil {
		h.t.Fatal(err)
	}
	return st.ActiveState == "active"
}

func (h *qaHost) started(timer string) bool {
	h.r.units.mu.Lock()
	defer h.r.units.mu.Unlock()
	return slices.Contains(h.r.units.calls, "Start "+timer)
}

func (h *qaHost) called(call string) bool {
	h.r.units.mu.Lock()
	defer h.r.units.mu.Unlock()
	return slices.Contains(h.r.units.calls, call)
}

func (h *qaHost) resetCalls() {
	h.r.units.mu.Lock()
	defer h.r.units.mu.Unlock()
	h.r.units.calls = nil
}

func (h *qaHost) done(cv core.ChangeView, err error, what string) core.ChangeView {
	h.t.Helper()
	if err != nil || cv.State != core.StateCompleted {
		h.t.Fatalf("%s: state %q, error %+v, %v; want completed", what, cv.State, cv.Error, err)
	}
	return cv
}

func (h *qaHost) apply(action, input string) core.ChangeView {
	h.t.Helper()
	cv, err := h.planApply(action, input)
	return h.done(cv, err, action)
}

func (h *qaHost) undo(id string) core.ChangeView {
	h.t.Helper()
	v, err := h.r.engine.Undo(context.Background(), adminP, id, nil)
	if err != nil {
		h.t.Fatalf("undo: %v", err)
	}
	for deadline := time.Now().Add(10 * time.Second); v.State == core.StateUndoing && time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if v, err = h.r.engine.Change(context.Background(), id); err != nil {
			h.t.Fatal(err)
		}
	}
	return v
}

func qaInput(slug string) string {
	return strings.Replace(backupInput, `"backup"`, `"`+slug+`"`, 1)
}

func TestA2_UndoOfADeleteRestoresScriptServiceAndTimer(t *testing.T) {
	h := newQAHost(t)
	h.apply("jobs.job.create", backupInput)
	files := []string{h.r.path("units", "darkpanel-job-backup.service"), h.r.path("units", "darkpanel-job-backup.timer"), h.r.path("jobs", "backup.sh")}
	before := map[string]string{}
	for _, f := range files {
		before[f] = h.r.text(f)
	}
	cv := h.apply("jobs.job.delete", `{"slug":"backup"}`)
	for _, f := range files {
		if _, err := os.Stat(f); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("after the delete %s is still there (%v)", f, err)
		}
	}
	if v := h.undo(cv.ID); v.State != core.StateReverted {
		t.Fatalf("undo ended %q (%+v), want reverted", v.State, v.Error)
	}
	for _, f := range files {
		if got := h.r.text(f); got != before[f] {
			t.Errorf("%s after the undo:\n%s\nwant what the job had before it was deleted:\n%s", f, got, before[f])
		}
	}
	if !h.active("darkpanel-job-backup.timer") {
		t.Error("the timer of the restored job is not running")
	}
}

// qaRuntimeList lists jobs through the agent runtime, whose guard wraps the
// jobs module's unit client, over the jobs the rig wrote. The module is the
// shipped one with its own manifest; only its file reads point at the rig.
func qaRuntimeList(t *testing.T, r *rig) []string {
	t.Helper()
	reg, err := core.NewRegistry(qaJobs{r.m})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	dir := t.TempDir()
	rt, err := runtime.Open(ctx, runtime.Config{
		DB: r.db, Sealer: coretest.Sealer{}, Clock: coretest.NewFakeClock(time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)),
		Log: slog.New(slog.DiscardHandler), Registry: reg, HostID: "h1", BinaryVersion: "test",
		BackupDir: filepath.Join(dir, "backups"), Exec: runner{}, FilesTestMode: true, Units: r.units,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rt.Close)
	if err := rt.SetModuleEnabled(ctx, "jobs", true); err != nil {
		t.Fatal(err)
	}
	raw, err := rt.Read(ctx, adminP, core.ActionRef{Name: "jobs.job.list", Version: 1}, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("jobs.job.list through the runtime: %v", err)
	}
	var got struct {
		Jobs []struct {
			Slug string `json:"slug"`
		} `json:"jobs"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	var slugs []string
	for _, j := range got.Jobs {
		slugs = append(slugs, j.Slug)
	}
	return slugs
}

// qaJobs is the shipped jobs module with its own manifest.
type qaJobs struct{ m module }

func (q qaJobs) Manifest() core.Manifest { return q.m.Manifest() }
func (q qaJobs) OnEnabled(ctx context.Context, env core.Env, enabled bool) error {
	return q.m.OnEnabled(ctx, env, enabled)
}

func TestA24_ListSkipsAVanishedJobThroughTheRuntimesEnv(t *testing.T) {
	h := newQAHost(t)
	for _, slug := range []string{"alpha", "bravo", "charlie", "delta"} {
		h.apply("jobs.job.create", qaInput(slug))
	}
	// bravo is deleted between the glob and the read; delta is caught mid-delete:
	// its service is gone and its script is still there.
	for _, name := range []string{serviceName("bravo"), timerName("bravo")} {
		h.r.gone[h.r.path("units", name)] = true
	}
	h.r.gone[filepath.Join(h.r.m.scriptDir, scriptName("bravo"))] = true
	if err := os.Remove(h.r.path("units", serviceName("delta"))); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(h.r.path("jobs", scriptName("delta"))); err != nil {
		t.Fatalf("delta's script should still exist: %v", err)
	}
	if got, want := qaRuntimeList(t, h.r), []string{"alpha", "charlie"}; !slices.Equal(got, want) {
		t.Errorf("listed through the runtime %q, want %q", got, want)
	}
}

// qaCalendarRunner runs systemd-analyze on the host for real.
type qaCalendarRunner struct{ runner }

func (qaCalendarRunner) Run(ctx context.Context, cmd core.Cmd) (core.CmdResult, error) {
	out, err := exec.CommandContext(ctx, cmd.Path, cmd.Args...).CombinedOutput() //nolint:gosec // G204: the command the module asks the runner for
	res := core.CmdResult{Stdout: out}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		res.ExitCode = ee.ExitCode()
		res.Stdout, res.Stderr = nil, out
		return res, nil
	}
	return res, err
}

func (qaCalendarRunner) Stream(context.Context, string, []string, func([]byte) error) error {
	return io.EOF
}

func TestA25_ScheduleThatNeverRunsIsRefused(t *testing.T) {
	r := newRig(t)
	env := r.env(adminP)
	if _, err := exec.LookPath("systemd-analyze"); err == nil {
		env.Exec = qaCalendarRunner{}
	}
	create := findAction(t, r.m, "jobs.job.create")
	check := func(schedule string) error {
		_, err := create.Handler.Plan(context.Background(), env, json.RawMessage(strings.Replace(backupInput, `*-*-* 03:00:00`, schedule, 1)))
		return err
	}
	err := check("2020-01-01")
	var e *errcode.Error
	if !errors.As(err, &e) || e.Code != errcode.InvalidInput {
		t.Fatalf("create with schedule 2020-01-01: %v, want invalid_input", err)
	}
	if len(e.Fields) != 1 || e.Fields[0].Path != "/schedule" || e.Fields[0].Message != "This schedule never runs" {
		t.Errorf("fields %+v, want one on /schedule saying %q", e.Fields, "This schedule never runs")
	}
	if err := check("daily"); err != nil {
		t.Errorf("control: create with schedule daily: %v", err)
	}
}

func TestA26_ChangeMeetingJobsTurnedOffEndsPausedAndTheTimerStartsWhenJobsComesBack(t *testing.T) {
	const timer = "darkpanel-job-backup.timer"
	for _, inter := range []string{"the guard refuses the start", "the start passed the guard and the off hook stopped the timer"} {
		t.Run(inter+"/create", func(t *testing.T) {
			h := newQAHost(t)
			var offDone chan struct{}
			if strings.HasPrefix(inter, "the guard") {
				h.turnOff()
			} else {
				// Right after the start passed, the timer is still activating and the
				// off hook runs from another goroutine, as the runtime's does.
				offDone = make(chan struct{})
				h.r.units.afterStart = func(unit string) {
					if unit != timer {
						return
					}
					h.r.units.mu.Lock()
					h.r.units.states[timer] = core.UnitState{LoadState: "loaded", ActiveState: "activating"}
					h.r.units.afterStart = nil
					h.r.units.mu.Unlock()
					go func() {
						defer close(offDone)
						h.turnOff()
						h.r.units.mu.Lock()
						delete(h.r.units.states, timer)
						h.r.units.mu.Unlock()
					}()
				}
			}
			t0 := time.Now()
			cv, err := h.planApply("jobs.job.create", backupInput)
			took := time.Since(t0)
			h.done(cv, err, "create")
			if offDone != nil {
				<-offDone
			}
			if took >= h.r.m.wait {
				t.Errorf("the change took %v, the wait deadline %v: it must complete at once", took, h.r.m.wait)
			}
			if got := h.note(cv); got != qaPausedNote {
				t.Errorf("note %q, want %q", got, qaPausedNote)
			}
			if h.active(timer) || h.timerEnabled(timer) {
				t.Errorf("timer active %v, enabled %v; want stopped and disabled", h.active(timer), h.timerEnabled(timer))
			}
			h.turnOn()
			if !h.active(timer) {
				t.Error("turning jobs back on did not start the timer")
			}
		})
	}

	t.Run("enable", func(t *testing.T) {
		h := newQAHost(t)
		h.apply("jobs.job.create", backupInput)
		h.apply("jobs.job.disable", `{"slug":"backup"}`)
		h.turnOff()
		cv, err := h.planApply("jobs.job.enable", `{"slug":"backup"}`)
		h.done(cv, err, "enable")
		if got := h.note(cv); got != qaPausedNote {
			t.Errorf("note %q, want %q", got, qaPausedNote)
		}
		if h.active(timer) || h.timerEnabled(timer) {
			t.Errorf("timer active %v, enabled %v; want stopped and disabled", h.active(timer), h.timerEnabled(timer))
		}
		h.turnOn()
		if !h.active(timer) {
			t.Error("turning jobs back on did not start the timer")
		}
	})

	t.Run("undo of a delete", func(t *testing.T) {
		h := newQAHost(t)
		h.apply("jobs.job.create", backupInput)
		del := h.apply("jobs.job.delete", `{"slug":"backup"}`)
		h.turnOff()
		if v := h.undo(del.ID); v.State != core.StateReverted {
			t.Fatalf("undo while jobs is off ended %q (%+v), want reverted", v.State, v.Error)
		}
		if h.active(timer) || h.timerEnabled(timer) {
			t.Errorf("timer active %v, enabled %v; want stopped and disabled", h.active(timer), h.timerEnabled(timer))
		}
		h.turnOn()
		if !h.active(timer) {
			t.Error("turning jobs back on did not start the restored job's timer")
		}
	})

	t.Run("disabled while paused", func(t *testing.T) {
		h := newQAHost(t)
		h.turnOff()
		h.apply("jobs.job.create", backupInput) // ends paused
		h.apply("jobs.job.disable", `{"slug":"backup"}`)
		h.resetCalls()
		h.turnOn()
		if h.started(timer) || h.active(timer) {
			t.Errorf("a job disabled while paused was started when jobs came back (started %v, active %v)", h.started(timer), h.active(timer))
		}
	})
}

func TestA27_OffStopsAndDisablesEveryTimerAndOnStartsTheOnesThatWereRunning(t *testing.T) {
	h := newQAHost(t)
	slugs := []string{"aa", "bb", "cc", "dd", "ee"}
	for _, s := range slugs {
		h.apply("jobs.job.create", qaInput(s))
	}
	tm := func(s string) string { return "darkpanel-job-" + s + ".timer" }
	// aa: active. bb: stopped but still enabled. cc: a start job is queued.
	// dd: disabled and stopped. ee: activating.
	h.r.units.set(tm("bb"), false)
	h.r.units.mu.Lock()
	h.r.units.states[tm("cc")] = core.UnitState{LoadState: "loaded", ActiveState: "inactive", PendingJob: "start"}
	h.r.units.states[tm("ee")] = core.UnitState{LoadState: "loaded", ActiveState: "activating"}
	h.r.units.mu.Unlock()
	h.apply("jobs.job.disable", `{"slug":"dd"}`)

	h.resetCalls()
	h.turnOff()
	for _, s := range slugs {
		for _, verb := range []string{"Stop ", "Disable "} {
			if !h.called(verb + tm(s)) {
				t.Errorf("turning jobs off did not %s%s", strings.ToLower(verb), tm(s))
			}
		}
	}
	// What the off hook stopped is no longer queued or activating.
	h.r.units.mu.Lock()
	clear(h.r.units.states)
	h.r.units.mu.Unlock()

	h.resetCalls()
	h.turnOn()
	for _, c := range []struct {
		slug string
		want bool
	}{{"aa", true}, {"bb", false}, {"cc", true}, {"dd", false}, {"ee", true}} {
		if got := h.started(tm(c.slug)); got != c.want {
			t.Errorf("after turning jobs on, Start of %s = %v, want %v", tm(c.slug), got, c.want)
		}
	}
}
