package jobs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform"
)

// ListInput is the input of jobs.job.list.
type ListInput struct{}

// JobList is the answer of jobs.job.list. Command text is only in an
// admin's answer.
type JobList struct {
	Jobs   []Job        `json:"jobs"`
	Timers []OtherTimer `json:"timers"`
	Cron   []CronEntry  `json:"cron"`
}

// Job is one Darkpanel job. Times are RFC 3339 in UTC, "" when there is none.
type Job struct {
	Slug               string `json:"slug"`
	Description        string `json:"description,omitempty"`
	Schedule           string `json:"schedule"`
	User               string `json:"user"`
	Command            string `json:"command,omitempty"`
	TimeoutSeconds     int    `json:"timeoutSeconds"`
	CatchUp            bool   `json:"catchUp"`
	RandomDelaySeconds int    `json:"randomDelaySeconds"`
	Enabled            bool   `json:"enabled"`
	Running            bool   `json:"running"`
	NextRun            string `json:"nextRun,omitempty"`
	LastStart          string `json:"lastStart,omitempty"`
	LastResult         string `json:"lastResult,omitempty"` // systemd's result: "success", "exit-code", "timeout", ...
	ExitStatus         *int32 `json:"exitStatus,omitempty" jsonschema:"nullable"`
}

// OtherTimer is a timer Darkpanel did not write; it is read-only here.
type OtherTimer struct {
	Name    string `json:"name"`
	Unit    string `json:"unit"`
	Source  string `json:"source"`
	NextRun string `json:"nextRun,omitempty"`
	LastRun string `json:"lastRun,omitempty"`
}

// CronEntry is one line of a crontab; it is read-only here.
type CronEntry struct {
	Source   string `json:"source"`
	Schedule string `json:"schedule"`
	User     string `json:"user"`
	Command  string `json:"command,omitempty"`
}

// The crontabs jobs.job.list reads.
const (
	systemCrontab = "/etc/crontab"
	cronDir       = "/etc/cron.d"
	spoolDir      = "/var/spool/cron/crontabs"
)

func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

type lister struct{ m module }

func (l lister) Read(ctx context.Context, env core.Env, _ ListInput) (JobList, error) {
	admin := core.RequireScope(env, core.ScopeAdmin) == nil
	out := JobList{Jobs: []Job{}, Timers: []OtherTimer{}, Cron: []CronEntry{}}
	tr, ok := env.Units.(core.TimerReader)
	if !ok {
		return JobList{}, fmt.Errorf("jobs: the unit client cannot list timers")
	}
	timers, err := tr.Timers(ctx)
	if err != nil {
		return JobList{}, err
	}
	slugs, err := l.m.slugs(ctx, env)
	if err != nil {
		return JobList{}, err
	}
	byName := map[string]core.TimerInfo{}
	for _, t := range timers {
		byName[t.Name] = t
	}
	for _, slug := range slugs {
		f, err := l.m.readJob(ctx, env, slug)
		if err != nil || f == nil {
			return JobList{}, err
		}
		j := parse(slug, f)
		item := Job{Slug: slug, Description: j.Description, Schedule: j.Schedule, User: j.User, TimeoutSeconds: j.TimeoutSeconds,
			CatchUp: j.CatchUp, RandomDelaySeconds: j.RandomDelaySeconds, Enabled: f.Enabled, NextRun: stamp(byName[timerName(slug)].Next)}
		if admin {
			item.Command = j.Command
		}
		run, err := tr.LastRun(ctx, serviceName(slug))
		if err != nil {
			return JobList{}, err
		}
		item.Running = run.ActiveState == "activating" || run.ActiveState == "active"
		if !run.Started.IsZero() {
			item.LastStart, item.LastResult = stamp(run.Started), run.Result
			status := run.ExitStatus
			item.ExitStatus = &status
		}
		out.Jobs = append(out.Jobs, item)
	}
	for _, t := range timers {
		if slices.ContainsFunc(slugs, func(slug string) bool { return t.Name == timerName(slug) }) {
			continue
		}
		out.Timers = append(out.Timers, OtherTimer{Name: t.Name, Unit: t.Unit, Source: t.FragmentPath, NextRun: stamp(t.Next), LastRun: stamp(t.Last)})
	}
	if out.Cron, err = l.m.cron(admin); err != nil {
		return JobList{}, err
	}
	return out, nil
}

// slugs are the jobs whose service unit the ownership store records as the
// module's, sorted.
func (m module) slugs(ctx context.Context, env core.Env) ([]string, error) {
	paths, err := m.d.glob(filepath.Join(m.unitDir, unitPrefix+"*.service"))
	if err != nil {
		return nil, err
	}
	var out []string
	for _, p := range paths {
		ours, err := env.Files.Owns(ctx, ID, p)
		if err != nil {
			return nil, err
		}
		slug := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(p), unitPrefix), ".service")
		if ours && slugPattern.MatchString(slug) {
			out = append(out, slug)
		}
	}
	slices.Sort(out)
	return out, nil
}

// cron reads the system crontab, /etc/cron.d and the users' crontabs; a
// missing file or directory has no entries.
func (m module) cron(admin bool) ([]CronEntry, error) {
	out := []CronEntry{}
	add := func(path, user string) error {
		data, err := m.d.readFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("jobs: read %s: %w", path, err)
		}
		for _, e := range parseCrontab(path, user, string(data)) {
			if !admin {
				e.Command = ""
			}
			out = append(out, e)
		}
		return nil
	}
	if err := add(systemCrontab, ""); err != nil {
		return nil, err
	}
	for _, dir := range []string{cronDir, spoolDir} {
		paths, err := m.d.glob(filepath.Join(dir, "*"))
		if err != nil {
			return nil, err
		}
		slices.Sort(paths)
		for _, p := range paths {
			user := ""
			if dir == spoolDir {
				user = filepath.Base(p)
			}
			if err := add(p, user); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// cronVariable is a crontab line that sets a variable, which cron allows
// with blanks around the =.
var cronVariable = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*[ \t]*=`)

// parseCrontab reads the entries of a crontab. user is the owner of a
// user's crontab; "" means a system crontab, whose lines name the user.
// Comments, blank lines and variable assignments are skipped.
func parseCrontab(path, user, text string) []CronEntry {
	var out []CronEntry
	for line := range strings.Lines(text) {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || cronVariable.MatchString(line) {
			continue
		}
		n := 5 // minute, hour, day of month, month, day of week
		if strings.HasPrefix(line, "@") {
			n = 1
		}
		if user == "" {
			n++
		}
		head, command, ok := cutFields(line, n)
		if !ok {
			continue
		}
		e := CronEntry{Source: path, Schedule: strings.Join(head, " "), User: user, Command: command}
		if user == "" {
			e.Schedule, e.User = strings.Join(head[:n-1], " "), head[n-1]
		}
		out = append(out, e)
	}
	return out
}

// cutFields splits the first n blank-separated fields off line and returns
// them and the rest as written; ok is false when the rest is empty.
func cutFields(line string, n int) ([]string, string, bool) {
	var head []string
	rest := line
	for range n {
		rest = strings.TrimLeft(rest, " \t")
		i := strings.IndexAny(rest, " \t")
		if i < 0 {
			return nil, "", false
		}
		head, rest = append(head, rest[:i]), rest[i:]
	}
	rest = strings.TrimSpace(rest)
	return head, rest, rest != ""
}

// ScheduleInput is the input of jobs.schedule.check.
type ScheduleInput struct {
	Schedule string `json:"schedule" jsonschema:"minLength=1,maxLength=256" jsonschema_description:"A systemd calendar expression."`
}

// ScheduleCheck is the next three runs of a calendar expression, RFC 3339
// in UTC.
type ScheduleCheck struct {
	Next []string `json:"next"`
}

type scheduleChecker struct{}

func (scheduleChecker) Read(ctx context.Context, env core.Env, in ScheduleInput) (ScheduleCheck, error) {
	next, err := checkSchedule(ctx, env, in.Schedule)
	if err != nil {
		return ScheduleCheck{}, err
	}
	return ScheduleCheck{Next: next}, nil
}

// checkSchedule asks systemd-analyze for the next three runs of expr; an
// expression it cannot parse is refused with its message.
func checkSchedule(ctx context.Context, env core.Env, expr string) ([]string, error) {
	if expr == "" || len(expr) > 256 || strings.ContainsFunc(expr, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return nil, invalid("/schedule", "Use one line of at most 256 characters")
	}
	res, err := env.Exec.Run(ctx, core.Cmd{Path: platform.SystemdAnalyzePath, Args: platform.CalendarArgs(expr)})
	if err != nil {
		return nil, fmt.Errorf("jobs: run systemd-analyze: %w", err)
	}
	if res.ExitCode != 0 {
		msg := strings.TrimSpace(string(res.Stderr))
		if msg == "" {
			msg = fmt.Sprintf("systemd-analyze refused the schedule (exit %d)", res.ExitCode)
		}
		return nil, core.Definite(&errcode.Error{Code: errcode.InvalidInput, Detail: msg, Fields: []errcode.FieldError{{Path: "/schedule", Message: msg}}})
	}
	return parseCalendar(string(res.Stdout))
}

// parseCalendar reads the elapses systemd-analyze calendar prints: the
// "(in UTC)" lines, or, on a host whose zone is UTC, which prints none, the
// "Next elapse" and "Iteration" lines.
func parseCalendar(out string) ([]string, error) {
	var utc, local []string
	for line := range strings.Lines(out) {
		k, v, ok := strings.Cut(strings.TrimSpace(line), ": ")
		if !ok {
			continue
		}
		switch {
		case k == "(in UTC)":
			utc = append(utc, v)
		case k == "Next elapse" || strings.HasPrefix(k, "Iteration #"):
			local = append(local, v)
		}
	}
	if len(utc) == 0 {
		utc = local
	}
	next := []string{}
	for _, v := range utc {
		t, err := time.Parse("Mon 2006-01-02 15:04:05 UTC", strings.TrimSpace(v))
		if err != nil {
			return nil, fmt.Errorf("jobs: systemd-analyze printed the elapse %q: %w", v, err)
		}
		next = append(next, stamp(t))
	}
	return next, nil
}
