// Package jobs is the scheduled jobs module (M11 spec section 6): systemd timers the panel owns. It is off until an admin turns it on.
package jobs

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform"
)

// ID is the module id.
const ID = "jobs"

const (
	lockKey    = "jobs"
	unitPrefix = "darkpanel-job-"
	// UnitPattern names the unit set of every job's service and timer.
	UnitPattern   = `^darkpanel-job-[a-z0-9][a-z0-9-]{0,39}\.(service|timer)$`
	scriptDir     = "/var/lib/darkpanel/jobs"
	scriptPattern = `^[a-z0-9][a-z0-9-]{0,39}\.sh$`
	scriptMode    = 0o700
	passwdFile    = "/etc/passwd"
	shellsFile    = "/etc/shells"
	maxCommand    = 16 << 10
	maxDesc       = 200
)

// The bounds of a job's timeout and random delay, in seconds.
const (
	minTimeout     = 60
	maxTimeout     = 24 * 3600
	defaultTimeout = 3600
	maxRandomDelay = 3600
)

var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

//go:embed migrations/agent/*.sql
var migrationsFS embed.FS

// deps is what the module reaches outside core.Env; tests replace it.
type deps struct {
	readFile func(name string) ([]byte, error)
	glob     func(pattern string) ([]string, error)
}

type module struct {
	d                  deps
	unitDir, scriptDir string        // core.UnitSetDir and scriptDir; tests use their own
	wait               time.Duration // how long a timer may take to start or stop
}

// Module returns the jobs module.
func Module() core.Module {
	return module{d: deps{readFile: os.ReadFile, glob: filepath.Glob}, unitDir: core.UnitSetDir, scriptDir: scriptDir, wait: 30 * time.Second}
}

// Manifest declares the module.
func (m module) Manifest() core.Manifest {
	agent, err := fs.Sub(migrationsFS, "migrations/agent")
	if err != nil {
		panic(err)
	}
	return core.Manifest{
		ID: ID, Version: 1, DefaultEnabled: false, Actions: m.actions(),
		OwnedPaths: []core.OwnedPath{
			{Path: core.UnitSetDir, Kind: core.OwnedUnitSet, Pattern: UnitPattern},
			{Path: scriptDir, Kind: core.OwnedDir, Pattern: scriptPattern},
		},
		Migrations: map[core.Side]fs.FS{core.SideAgent: agent},
	}
}

// JobInput is the input of jobs.job.create and jobs.job.update.
type JobInput struct {
	Slug               string `json:"slug" jsonschema:"pattern=^[a-z0-9][a-z0-9-]{0\\,39}$" jsonschema_description:"The job's name: lower case letters, digits and hyphens."`
	Description        string `json:"description,omitempty" jsonschema:"maxLength=200" jsonschema_description:"A one-line description."`
	Schedule           string `json:"schedule" jsonschema:"minLength=1,maxLength=256" jsonschema_description:"A systemd calendar expression, such as daily or *-*-* 03:00:00."`
	User               string `json:"user" jsonschema:"pattern=^[a-z_][a-z0-9_-]{0\\,31}$" jsonschema_description:"The account the job runs as: root or a login user."`
	Command            string `json:"command" secret:"true" jsonschema:"minLength=1,maxLength=16384" jsonschema_description:"The shell commands the job runs, at most 16 KiB."`
	TimeoutSeconds     int    `json:"timeoutSeconds,omitempty" jsonschema:"minimum=60,maximum=86400" jsonschema_description:"How long a run may take, 1 minute to 24 hours; 1 hour when omitted."`
	CatchUp            bool   `json:"catchUp,omitempty" jsonschema_description:"Run once at boot when a scheduled run was missed."`
	RandomDelaySeconds int    `json:"randomDelaySeconds,omitempty" jsonschema:"minimum=0,maximum=3600" jsonschema_description:"Delay each run by up to this many seconds, at most 1 hour."`
}

// SlugInput names a job.
type SlugInput struct {
	Slug string `json:"slug" jsonschema:"pattern=^[a-z0-9][a-z0-9-]{0\\,39}$" jsonschema_description:"The job's name."`
}

// Changed is the answer of the job actions.
type Changed struct {
	Slug string `json:"slug"`
	Note string `json:"note,omitempty" jsonschema_description:"Set by create, update and enable when jobs is off on this host: the job's timer starts when jobs is turned on."`
}

func invalid(path, msg string) error {
	return core.Definite(&errcode.Error{Code: errcode.InvalidInput, Detail: msg, Fields: []errcode.FieldError{{Path: path, Message: msg}}})
}

func (m module) actions() []core.ActionDef {
	spec := func(verb, summary string, class core.Class) core.ActionSpec {
		return core.ActionSpec{
			Name: ID + ".job." + verb, Version: 1, Summary: summary, Class: class, Side: core.SideAgent,
			LockKeys: []string{lockKey}, MinScope: core.ScopeAdmin, ApplyTimeout: 45 * time.Second,
		}
	}
	create := spec("create", "Create a job: a root-only script, a service that systemd hands it to and a timer that starts it.", core.ClassCompensable)
	create.StepUp = true
	update := spec("update", "Change a job's schedule, user, command or limits.", core.ClassCompensable)
	update.StepUp = true
	del := spec("delete", "Delete a job: stop its timer and remove its files.", core.ClassCompensable)
	del.UndoStepUp = true
	run := spec("run", "Run a job now.", core.ClassCompensable)
	run.ForwardOnly, run.Converges = true, true
	return []core.ActionDef{
		core.DefineRead[ListInput, JobList](core.ActionSpec{
			Name: ID + ".job.list", Version: 1, Class: core.ClassRead, Side: core.SideAgent,
			Summary: "List Darkpanel jobs, the host's other timers and its cron entries.",
		}, lister{m: m}),
		core.DefineRead[ScheduleInput, ScheduleCheck](core.ActionSpec{
			Name: ID + ".schedule.check", Version: 1, Class: core.ClassRead, Side: core.SideAgent, MinScope: core.ScopeAdmin,
			Summary: "Check a calendar expression and show its next three runs.",
		}, scheduleChecker{}),
		core.DefineMutating[JobInput, Changed](create, writeHandler{m: m, verb: "create"}),
		core.DefineMutating[JobInput, Changed](update, writeHandler{m: m, verb: "update"}),
		core.DefineMutating[SlugInput, Changed](del, stateHandler{m: m, verb: "delete"}),
		core.DefineMutating[SlugInput, Changed](spec("enable", "Start a job's timer and start it at boot.", core.ClassCompensable),
			stateHandler{m: m, verb: "enable"}),
		core.DefineMutating[SlugInput, Changed](spec("disable", "Stop a job's timer and keep it off at boot.", core.ClassCompensable),
			stateHandler{m: m, verb: "disable"}),
		core.DefineMutating[SlugInput, Changed](run, runHandler{m: m}),
	}
}

// account is a passwd line's user.
type account struct {
	uid, gid int
	shell    string
}

// lookup reads name's passwd line.
func (m module) lookup(name string) (account, bool, error) {
	data, err := m.d.readFile(passwdFile)
	if err != nil {
		return account{}, false, fmt.Errorf("jobs: read %s: %w", passwdFile, err)
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		f := strings.Split(sc.Text(), ":")
		if len(f) != 7 || f[0] != name {
			continue
		}
		uid, err1 := strconv.Atoi(f[2])
		gid, err2 := strconv.Atoi(f[3])
		if err1 != nil || err2 != nil {
			return account{}, false, fmt.Errorf("jobs: %s: bad line for %s", passwdFile, name)
		}
		return account{uid: uid, gid: gid, shell: f[6]}, true, nil
	}
	return account{}, false, sc.Err()
}

// checkUser admits root and login users: a UID from 1000 to 59999 with a
// shell /etc/shells lists that is not a nologin or false shell.
func (m module) checkUser(name string) error {
	a, ok, err := m.lookup(name)
	switch {
	case err != nil:
		return err
	case !ok || !platform.LoginName(name):
		return invalid("/user", "No account "+name)
	case name == "root" && a.uid == 0:
		return nil
	case a.uid < 1000 || a.uid > 59999:
		return invalid("/user", name+" is a system account; pick root or a login user")
	}
	shells, err := m.d.readFile(shellsFile)
	if err != nil {
		return fmt.Errorf("jobs: read %s: %w", shellsFile, err)
	}
	login := !strings.HasSuffix(a.shell, "nologin") && !strings.HasSuffix(a.shell, "/false")
	if !login || !slices.Contains(strings.Fields(string(shells)), a.shell) {
		return invalid("/user", name+" has no login shell; pick root or a login user")
	}
	return nil
}

// check validates in against the host and returns the job it describes.
func (m module) check(ctx context.Context, env core.Env, in JobInput) (job, error) {
	j := job{Slug: in.Slug, Description: in.Description, Schedule: in.Schedule, User: in.User, Command: in.Command,
		TimeoutSeconds: in.TimeoutSeconds, RandomDelaySeconds: in.RandomDelaySeconds, CatchUp: in.CatchUp}
	if j.TimeoutSeconds == 0 {
		j.TimeoutSeconds = defaultTimeout
	}
	switch {
	case !slugPattern.MatchString(j.Slug):
		return job{}, invalid("/slug", "Use lower case letters, digits and hyphens, at most 40")
	case len(j.Description) > maxDesc || strings.ContainsFunc(j.Description, unicode.IsControl):
		return job{}, invalid("/description", "Use one line of at most 200 characters")
	case j.Command == "" || len(j.Command) > maxCommand || strings.ContainsRune(j.Command, 0):
		return job{}, invalid("/command", "Use 1 byte to 16 KiB of shell with no NUL byte")
	case j.TimeoutSeconds < minTimeout || j.TimeoutSeconds > maxTimeout:
		return job{}, invalid("/timeoutSeconds", "Use 1 minute to 24 hours")
	case j.RandomDelaySeconds < 0 || j.RandomDelaySeconds > maxRandomDelay:
		return job{}, invalid("/randomDelaySeconds", "Use 0 to 1 hour")
	}
	if err := m.checkUser(j.User); err != nil {
		return job{}, err
	}
	if _, err := checkSchedule(ctx, env, j.Schedule); err != nil {
		return job{}, err
	}
	return j, nil
}

func digest(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

func effectResource(slug string) string { return effectPrefix + slug }

// effectPrefix starts the resource of a job's effect.
const effectPrefix = ID + ".job:"

// change is one action's move of a job from now to want.
type change struct {
	slug      string
	now, want *jobFiles
}

// apply records the effect, moves the job and marks the effect made.
func (m module) apply(ctx context.Context, env core.Env, c change) (Changed, error) {
	after := jobDoc(c.want)
	var before []byte
	if c.now != nil {
		before = []byte(c.now.Script)
	}
	seq, err := env.Effects.Intend(ctx, core.Effect{
		Kind: core.EffectModify, Resource: effectResource(c.slug), Local: true, InverseLocal: true,
		Before: jobDoc(c.now), After: &after,
	}, before)
	if err != nil {
		return Changed{}, err
	}
	paused, err := m.setJob(ctx, env, c.slug, c.now, c.want)
	if err != nil {
		return Changed{}, err
	}
	out := Changed{Slug: c.slug}
	if paused {
		out.Note = pausedNote
	}
	return out, env.Effects.Made(ctx, seq, effectResource(c.slug), after)
}

// pausedNote is the note of a change that left its job paused.
const pausedNote = "jobs is off on this host; the job starts when jobs is turned on"

// undo is the shared Observe and Inverse of the compensable job actions:
// the job's effect is its whole state, which the undo puts back.
type undo struct{ m module }

func (u undo) Observe(ctx context.Context, env core.Env, e core.Effect) (core.Observation, error) {
	slug := strings.TrimPrefix(e.Resource, effectPrefix)
	now, err := u.m.readJob(ctx, env, slug)
	if err != nil {
		return core.Observation{}, err
	}
	return core.Observation{Current: jobDoc(now), Resource: e.Resource, Matches: 1, Exclusive: true}, nil
}

func (u undo) Inverse(ctx context.Context, env core.Env, e core.Effect) (core.InverseResult, error) {
	slug := strings.TrimPrefix(e.Resource, effectPrefix)
	want, err := docFiles(e.Before, func() ([]byte, error) { return env.Effects.Secret(ctx, e.Seq) })
	if err != nil {
		return core.InverseResult{}, core.Definite(fmt.Errorf("jobs: read the effect's prior state: %w", err))
	}
	now, err := u.m.readJob(ctx, env, slug)
	if err != nil {
		return core.InverseResult{}, err
	}
	_, err = u.m.setJob(ctx, env, slug, now, want)
	return core.InverseResult{}, err
}

// writeHandler is jobs.job.create and jobs.job.update.
type writeHandler struct {
	m    module
	verb string
}

func (h writeHandler) plan(ctx context.Context, env core.Env, in JobInput) (change, error) {
	j, err := h.m.check(ctx, env, in)
	if err != nil {
		return change{}, err
	}
	now, err := h.m.readJob(ctx, env, j.Slug)
	if err != nil {
		return change{}, err
	}
	switch {
	case h.verb == "create" && now != nil:
		return change{}, invalid("/slug", "A job "+j.Slug+" exists already; update it or pick another name")
	case h.verb == "update" && now == nil:
		return change{}, invalid("/slug", "There is no job "+j.Slug)
	}
	want := render(j, now == nil || now.Enabled)
	if now != nil && bytes.Equal(jobDoc(now).State, jobDoc(want).State) {
		return change{}, invalid("", "Job "+j.Slug+" is like this already")
	}
	return change{slug: j.Slug, now: now, want: want}, nil
}

func (h writeHandler) Plan(ctx context.Context, env core.Env, in JobInput) (core.PlanOutcome, error) {
	c, err := h.plan(ctx, env, in)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	op, verb := "+", "Create"
	if c.now != nil {
		op, verb = " ", "Rewrite"
	}
	diff := []core.DiffLine{
		{Op: op, Text: fmt.Sprintf("%s job %s: %s, as %s", verb, c.slug, in.Schedule, in.User)},
		{Op: op, Text: filepath.Join(h.m.unitDir, serviceName(c.slug))},
		{Op: op, Text: filepath.Join(h.m.unitDir, timerName(c.slug))},
		{Op: op, Text: filepath.Join(scriptDir, scriptName(c.slug)) + " (mode 0700, root only; systemd hands it to the job)"},
	}
	return core.PlanOutcome{Diff: diff, StateHash: digest(string(jobDoc(c.now).State), string(jobDoc(c.want).State))}, nil
}

func (h writeHandler) Apply(ctx context.Context, env core.Env, in JobInput) (Changed, error) {
	c, err := h.plan(ctx, env, in)
	if err != nil {
		return Changed{}, err
	}
	return h.m.apply(ctx, env, c)
}

func (h writeHandler) Verify(ctx context.Context, env core.Env, in JobInput, _ Changed) error {
	j, err := h.m.check(ctx, env, in)
	if err != nil {
		return err
	}
	now, err := h.m.readJob(ctx, env, j.Slug)
	if err != nil {
		return err
	}
	return h.m.verifyJob(ctx, env, j.Slug, render(j, now != nil && now.Enabled))
}

func (h writeHandler) Observe(ctx context.Context, env core.Env, e core.Effect) (core.Observation, error) {
	return undo{m: h.m}.Observe(ctx, env, e)
}

func (h writeHandler) Inverse(ctx context.Context, env core.Env, e core.Effect) (core.InverseResult, error) {
	return undo{m: h.m}.Inverse(ctx, env, e)
}

// stateHandler is jobs.job.delete, enable and disable.
type stateHandler struct {
	m    module
	verb string
}

func (h stateHandler) plan(ctx context.Context, env core.Env, in SlugInput) (change, error) {
	if !slugPattern.MatchString(in.Slug) {
		return change{}, invalid("/slug", "Use lower case letters, digits and hyphens, at most 40")
	}
	now, err := h.m.readJob(ctx, env, in.Slug)
	if err != nil {
		return change{}, err
	}
	if now == nil {
		return change{}, invalid("/slug", "There is no job "+in.Slug)
	}
	var want *jobFiles
	if h.verb != "delete" {
		w := *now
		w.Enabled = h.verb == "enable"
		if w.Enabled == now.Enabled {
			return change{}, invalid("/slug", "Job "+in.Slug+" is "+h.verb+"d already")
		}
		want = &w
	}
	return change{slug: in.Slug, now: now, want: want}, nil
}

func (h stateHandler) Plan(ctx context.Context, env core.Env, in SlugInput) (core.PlanOutcome, error) {
	c, err := h.plan(ctx, env, in)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	var diff []core.DiffLine
	switch h.verb {
	case "delete":
		diff = []core.DiffLine{{Op: "-", Text: "Stop and delete job " + c.slug + " with its units and script"}}
	case "enable":
		diff = []core.DiffLine{{Op: "+", Text: "Start the timer of job " + c.slug + " and start it at boot"}}
	default:
		diff = []core.DiffLine{{Op: "-", Text: "Stop the timer of job " + c.slug + " and keep it off at boot"}}
	}
	return core.PlanOutcome{Diff: diff, StateHash: digest(string(jobDoc(c.now).State), h.verb)}, nil
}

func (h stateHandler) Apply(ctx context.Context, env core.Env, in SlugInput) (Changed, error) {
	c, err := h.plan(ctx, env, in)
	if err != nil {
		return Changed{}, err
	}
	return h.m.apply(ctx, env, c)
}

func (h stateHandler) Verify(ctx context.Context, env core.Env, in SlugInput, _ Changed) error {
	if h.verb == "delete" {
		return h.m.verifyJob(ctx, env, in.Slug, nil)
	}
	now, err := h.m.readJob(ctx, env, in.Slug)
	if err != nil {
		return err
	}
	if now == nil || now.Enabled != (h.verb == "enable") {
		return fmt.Errorf("jobs: the timer of job %s is not %sd", in.Slug, h.verb)
	}
	return nil
}

func (h stateHandler) Observe(ctx context.Context, env core.Env, e core.Effect) (core.Observation, error) {
	return undo{m: h.m}.Observe(ctx, env, e)
}

func (h stateHandler) Inverse(ctx context.Context, env core.Env, e core.Effect) (core.InverseResult, error) {
	return undo{m: h.m}.Inverse(ctx, env, e)
}

// runHandler is jobs.job.run: it starts the job's service once.
type runHandler struct{ m module }

func (h runHandler) Plan(ctx context.Context, env core.Env, in SlugInput) (core.PlanOutcome, error) {
	now, err := stateHandler{m: h.m, verb: "run"}.exists(ctx, env, in.Slug)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	return core.PlanOutcome{Diff: []core.DiffLine{{Op: " ", Text: "Run job " + in.Slug + " now (" + serviceName(in.Slug) + ")"}},
		StateHash: digest(string(jobDoc(now).State))}, nil
}

func (h runHandler) Apply(ctx context.Context, env core.Env, in SlugInput) (Changed, error) {
	if _, err := (stateHandler{m: h.m}).exists(ctx, env, in.Slug); err != nil {
		return Changed{}, err
	}
	err := env.Units.Start(ctx, serviceName(in.Slug))
	if jobsOff(err) {
		// The guard refused before systemd was asked: nothing ran.
		err = core.Definite(err)
	}
	return Changed{Slug: in.Slug}, err
}

func (h runHandler) Verify(context.Context, core.Env, SlugInput, Changed) error { return nil }

// exists reads the job slug, refusing a job that is not there.
func (h stateHandler) exists(ctx context.Context, env core.Env, slug string) (*jobFiles, error) {
	if !slugPattern.MatchString(slug) {
		return nil, invalid("/slug", "Use lower case letters, digits and hyphens, at most 40")
	}
	now, err := h.m.readJob(ctx, env, slug)
	if err != nil {
		return nil, err
	}
	if now == nil {
		return nil, invalid("/slug", "There is no job "+slug)
	}
	return now, nil
}
