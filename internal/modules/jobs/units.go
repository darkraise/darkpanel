package jobs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
)

// The names of a job's files.
func serviceName(slug string) string { return unitPrefix + slug + ".service" }
func timerName(slug string) string   { return unitPrefix + slug + ".timer" }
func scriptName(slug string) string  { return slug + ".sh" }

// job is what a job's files say: its input, read back.
type job struct {
	Slug, Description, Schedule, User, Command string
	TimeoutSeconds, RandomDelaySeconds         int
	CatchUp                                    bool
}

// jobFiles is the state of one job on the host: the bytes of its three
// files, the run-as user that names the script's group, and whether its
// timer runs.
type jobFiles struct {
	Service string
	Timer   string
	Script  string
	User    string
	Enabled bool
}

// jobState is the state document of a job's effect. A viewer can read
// effects and a script can hold secrets, so the script is named by its
// SHA-256; the script an undo restores is the effect's sealed secret.
type jobState struct {
	Service      string `json:"service"`
	Timer        string `json:"timer"`
	ScriptSHA256 string `json:"scriptSha256"`
	User         string `json:"user"`
	Enabled      bool   `json:"enabled"`
}

func jobDoc(f *jobFiles) core.StateDoc {
	if f == nil {
		return core.StateDoc{State: json.RawMessage("null")}
	}
	b, _ := json.Marshal(jobState{Service: f.Service, Timer: f.Timer, ScriptSHA256: digest(f.Script), User: f.User, Enabled: f.Enabled})
	return core.StateDoc{State: b}
}

// docFiles is the job a state document names, its script given.
func docFiles(d core.StateDoc, script func() ([]byte, error)) (*jobFiles, error) {
	var st *jobState
	if err := json.Unmarshal(d.State, &st); err != nil {
		return nil, err
	}
	if st == nil {
		return nil, nil
	}
	f := &jobFiles{Service: st.Service, Timer: st.Timer, User: st.User, Enabled: st.Enabled}
	if st.ScriptSHA256 == digest("") {
		return f, nil
	}
	b, err := script()
	if err != nil {
		return nil, err
	}
	if digest(string(b)) != st.ScriptSHA256 {
		return nil, errors.New("the sealed script does not match the state's digest")
	}
	f.Script = string(b)
	return f, nil
}

// escapeUnit keeps a value literal in a unit file: % starts a specifier.
func escapeUnit(s string) string { return strings.ReplaceAll(s, "%", "%%") }

// render writes the files of j, enabled or not.
func render(j job, enabled bool) *jobFiles {
	desc := "Darkpanel job " + j.Slug
	if j.Description != "" {
		desc += ": " + j.Description
	}
	var svc strings.Builder
	svc.WriteString("# Written by Darkpanel; change the job in Darkpanel.\n[Unit]\n")
	svc.WriteString("Description=" + escapeUnit(desc) + "\n\n[Service]\nType=oneshot\n")
	svc.WriteString("User=" + j.User + "\n")
	svc.WriteString("TimeoutStartSec=" + strconv.Itoa(j.TimeoutSeconds) + "\n")
	svc.WriteString("ExecStart=/bin/sh " + filepath.Join(scriptDir, scriptName(j.Slug)) + "\n")
	var tmr strings.Builder
	tmr.WriteString("# Written by Darkpanel; change the job in Darkpanel.\n[Unit]\n")
	tmr.WriteString("Description=" + escapeUnit("Schedule of Darkpanel job "+j.Slug) + "\n\n[Timer]\n")
	tmr.WriteString("OnCalendar=" + escapeUnit(j.Schedule) + "\n")
	tmr.WriteString("Persistent=" + strconv.FormatBool(j.CatchUp) + "\n")
	tmr.WriteString("RandomizedDelaySec=" + strconv.Itoa(j.RandomDelaySeconds) + "\n\n[Install]\nWantedBy=timers.target\n")
	script := j.Command
	if !strings.HasSuffix(script, "\n") {
		script += "\n"
	}
	return &jobFiles{Service: svc.String(), Timer: tmr.String(), Script: script, User: j.User, Enabled: enabled}
}

// unitValues are the Key=value lines of a unit file Darkpanel wrote.
func unitValues(text string) map[string]string {
	out := map[string]string{}
	for line := range strings.Lines(text) {
		line = strings.TrimSpace(line)
		if k, v, ok := strings.Cut(line, "="); ok && !strings.HasPrefix(line, "#") {
			out[k] = strings.ReplaceAll(v, "%%", "%")
		}
	}
	return out
}

// parse reads a job back from its files.
func parse(slug string, f *jobFiles) job {
	svc, tmr := unitValues(f.Service), unitValues(f.Timer)
	j := job{Slug: slug, Schedule: tmr["OnCalendar"], User: svc["User"], Command: strings.TrimSuffix(f.Script, "\n")}
	j.Description = strings.TrimPrefix(strings.TrimPrefix(svc["Description"], "Darkpanel job "+slug), ": ")
	j.TimeoutSeconds, _ = strconv.Atoi(svc["TimeoutStartSec"])
	j.RandomDelaySeconds, _ = strconv.Atoi(tmr["RandomizedDelaySec"])
	j.CatchUp = tmr["Persistent"] == "true"
	return j
}

// readJob reads the job slug's files, nil when none of them exists. A file
// the ownership store does not record as the module's is refused: Darkpanel
// never takes over a job it did not write.
func (m module) readJob(ctx context.Context, env core.Env, slug string) (*jobFiles, error) {
	paths := []string{filepath.Join(m.unitDir, serviceName(slug)), filepath.Join(m.unitDir, timerName(slug)), filepath.Join(m.scriptDir, scriptName(slug))}
	var texts [3]string
	found := 0
	for i, p := range paths {
		data, err := m.d.readFile(p)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			continue
		case err != nil:
			return nil, fmt.Errorf("jobs: read %s: %w", p, err)
		}
		ours, err := env.Files.Owns(ctx, ID, p)
		if err != nil {
			return nil, err
		}
		if !ours {
			return nil, invalid("/slug", p+" exists and Darkpanel did not write it; pick another name")
		}
		texts[i] = string(data)
		found++
	}
	if found == 0 {
		return nil, nil
	}
	f := &jobFiles{Service: texts[0], Timer: texts[1], Script: texts[2]}
	f.User = unitValues(f.Service)["User"]
	st, err := env.Units.State(ctx, timerName(slug))
	if err != nil {
		return nil, err
	}
	f.Enabled = st.ActiveState == "active"
	return f, nil
}

// setJob takes the job slug from now to want: nil removes its timer, unit
// files and script; otherwise the files are written and the timer is
// started and enabled, or stopped and disabled, as want says.
func (m module) setJob(ctx context.Context, env core.Env, slug string, now, want *jobFiles) error {
	w, ok := env.Files.(core.UnitSetWriter)
	if !ok {
		return fmt.Errorf("jobs: the file writer cannot write unit sets")
	}
	timer := timerName(slug)
	if now != nil && now.Enabled && (want == nil || !want.Enabled) {
		if err := env.Units.Stop(ctx, timer); err != nil {
			return err
		}
		if err := m.waitTimer(ctx, env, timer, false); err != nil {
			return err
		}
		if err := env.Units.Disable(ctx, timer); err != nil {
			return err
		}
	}
	if want == nil {
		for _, name := range []string{timer, serviceName(slug)} {
			if err := w.RemoveUnitInSet(ctx, ID, name); err != nil {
				return err
			}
		}
		if err := w.RemoveDirFile(ctx, ID, m.scriptDir, scriptName(slug)); err != nil {
			return err
		}
		return env.Units.Reload(ctx)
	}
	gid, err := m.primaryGID(want.User)
	if err != nil {
		return err
	}
	if err := w.WriteDirFile(ctx, ID, m.scriptDir, scriptName(slug), []byte(want.Script), scriptMode, gid); err != nil {
		return err
	}
	for name, text := range map[string]string{serviceName(slug): want.Service, timer: want.Timer} {
		if err := w.WriteUnitInSet(ctx, ID, name, []byte(text)); err != nil {
			return err
		}
	}
	if err := env.Units.Reload(ctx); err != nil {
		return err
	}
	switch {
	case want.Enabled && (now == nil || !now.Enabled):
		if err := env.Units.Enable(ctx, timer); err != nil {
			return err
		}
		if err := env.Units.Start(ctx, timer); err != nil {
			return err
		}
		return m.waitTimer(ctx, env, timer, true)
	case want.Enabled && now.Timer != want.Timer:
		// A running timer keeps the schedule it started with.
		if err := env.Units.Restart(ctx, timer); err != nil {
			return err
		}
		return m.waitTimer(ctx, env, timer, true)
	}
	return nil
}

// waitTimer waits until the timer is active, or not, as systemd runs the
// queued job; unit jobs return before they finish.
func (m module) waitTimer(ctx context.Context, env core.Env, timer string, active bool) error {
	deadline := time.Now().Add(m.wait)
	for {
		st, err := env.Units.State(ctx, timer)
		if err != nil {
			return err
		}
		if (st.ActiveState == "active") == active && st.PendingJob == "" {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("jobs: %s is %s after %s, want active %t", timer, st.ActiveState, m.wait, active)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// verifyJob checks the host holds want for slug, or nothing for nil.
func (m module) verifyJob(ctx context.Context, env core.Env, slug string, want *jobFiles) error {
	got, err := m.readJob(ctx, env, slug)
	if err != nil {
		return err
	}
	if want == nil {
		if got != nil {
			return fmt.Errorf("jobs: job %s is still on the host", slug)
		}
		return nil
	}
	if got == nil || !bytes.Equal(jobDoc(got).State, jobDoc(want).State) {
		return fmt.Errorf("jobs: job %s on the host differs from the change", slug)
	}
	return nil
}
