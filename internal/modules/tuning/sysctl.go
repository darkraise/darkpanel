package tuning

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// sysctlFile sorts after 99-sysctl.conf, so it wins among the sysctl.d
// files systemd-sysctl applies at boot.
const (
	sysctlFile    = "/etc/sysctl.d/99-zz-darkpanel.conf"
	ufwSysctlFile = "/etc/ufw/sysctl.conf"
	sysctlLockKey = "sysctl"
	fileHeader    = "# Written by Darkpanel (tuning.sysctl.apply); Darkpanel rewrites this file.\n"
)

// sysctlDirs are the directories systemd-sysctl reads, first first: a file
// name in an earlier one hides the same name in a later one.
var sysctlDirs = []string{"/etc/sysctl.d", "/run/sysctl.d", "/usr/local/lib/sysctl.d", "/usr/lib/sysctl.d", "/lib/sysctl.d"}

// SysctlKeyState is one kernel parameter of tuning.sysctl.get.
type SysctlKeyState struct {
	Key         string   `json:"key" jsonschema_description:"The dotted name, as sysctl.d files write it."`
	Help        string   `json:"help,omitempty"`
	Kind        string   `json:"kind,omitempty" jsonschema:"enum=integer,enum=choice,enum=port-range" jsonschema_description:"Empty for a key another module manages."`
	Min         string   `json:"min,omitempty" jsonschema_description:"Integer and port-range kinds: the lowest value, in decimal (fs.file-max exceeds a JavaScript number)."`
	Max         string   `json:"max,omitempty" jsonschema_description:"Integer and port-range kinds: the highest value, in decimal."`
	Choices     []string `json:"choices,omitempty" jsonschema_description:"Choice kind: the values the key takes on this host."`
	Live        string   `json:"live,omitempty" jsonschema_description:"The kernel's value now; a port range as LOW HIGH."`
	Set         string   `json:"set,omitempty" jsonschema_description:"The value 99-zz-darkpanel.conf holds."`
	LaterFiles  []string `json:"laterFiles,omitempty" jsonschema_description:"Files that set the key after 99-zz-darkpanel.conf at boot, in the order they apply."`
	Owner       string   `json:"owner,omitempty" jsonschema_description:"The module that manages the key; tuning does not change it."`
	Unavailable string   `json:"unavailable,omitempty" jsonschema_description:"Why the key cannot be set on this host."`
}

// SysctlState is the answer of tuning.sysctl.get.
type SysctlState struct {
	File string           `json:"file"`
	Keys []SysctlKeyState `json:"keys"`
}

// SysctlInput is the input of tuning.sysctl.apply: every key
// 99-zz-darkpanel.conf holds afterwards.
type SysctlInput struct {
	Values map[string]string `json:"values" jsonschema_description:"Value by dotted key, a port range as LOW HIGH; each is written live and to the file. A key left out leaves the file and keeps its live value; an empty object removes the file."`
}

// SysctlApplied is the answer of tuning.sysctl.apply.
type SysctlApplied struct {
	Set     map[string]string `json:"set"`
	Written []string          `json:"written" jsonschema_description:"The keys whose live value changed."`
}

// SysctlRepaired is the answer of tuning.sysctl.repair.
type SysctlRepaired struct {
	From    string   `json:"from,omitempty" jsonschema_description:"The confirmed tuning.sysctl.apply change the file was rewritten from; empty when none was confirmed and the file was removed."`
	Written []string `json:"written"`
}

// ownedKey is a sysctl key another module declares.
type ownedKey struct{ module, name string }

// owner is the module that declares name, "" when none does; an <iface>
// segment of a declaration takes any segment.
func (m module) owner(name string) string {
	segs := strings.Split(name, ".")
	for _, o := range m.owners() {
		if keysOverlap(strings.Split(o.name, "."), segs) {
			return o.module
		}
	}
	return ""
}

// keysOverlap reports whether two keys, as segments, can name one
// parameter: an <iface> segment of either takes any segment.
func keysOverlap(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] && a[i] != "<iface>" && b[i] != "<iface>" {
			return false
		}
	}
	return true
}

func invalid(path, msg string) error {
	return core.Definite(&errcode.Error{Code: errcode.InvalidInput, Detail: msg, Fields: []errcode.FieldError{{Path: path, Message: msg}}})
}

// assignment is one key = value line of a sysctl file.
type assignment struct{ name, value string }

// assignments are the key = value lines of data, keys dotted, a leading
// "-" (ignore errors) dropped.
func assignments(data []byte) []assignment {
	var out []assignment
	for line := range strings.Lines(string(data)) {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimPrefix(strings.TrimSpace(k), "-")
		out = append(out, assignment{name: strings.ReplaceAll(k, "/", "."), value: fromKernel(v)})
	}
	return out
}

// sets reports whether an assignment's key, which may hold a glob, names
// name.
func (a assignment) sets(name string) bool {
	if a.name == name {
		return true
	}
	ok, err := path.Match(strings.ReplaceAll(a.name, ".", "/"), strings.ReplaceAll(name, ".", "/"))
	return err == nil && ok
}

// render is the file holding values, nil when values is empty.
func render(values map[string]string) []byte {
	if len(values) == 0 {
		return nil
	}
	b := bytes.NewBufferString(fileHeader)
	for _, k := range curated {
		if v, ok := values[k.name]; ok {
			fmt.Fprintf(b, "%s = %s\n", k.name, v)
		}
	}
	return b.Bytes()
}

// readOurs reads 99-zz-darkpanel.conf.
func (m module) readOurs() ([]byte, bool, error) {
	data, err := m.d.readFile(sysctlFile)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, false, nil
	case err != nil:
		return nil, false, fmt.Errorf("tuning: read %s: %w", sysctlFile, err)
	}
	return data, true, nil
}

// sysctlConf is one sysctl file and its assignments.
type sysctlConf struct {
	path string
	set  []assignment
}

// laterFiles are the files applied after 99-zz-darkpanel.conf at boot:
// the sysctl.d files whose names sort after it, then UFW's, which UFW
// applies after systemd-sysctl.
func (m module) laterFiles() ([]sysctlConf, error) {
	ours := path.Base(sysctlFile)
	byName := map[string]string{}
	for _, dir := range sysctlDirs {
		paths, err := m.d.glob(dir + "/*.conf")
		if err != nil {
			return nil, fmt.Errorf("tuning: list %s: %w", dir, err)
		}
		for _, p := range paths {
			if name := path.Base(p); byName[name] == "" {
				byName[name] = p
			}
		}
	}
	var names []string
	for name := range byName {
		if name > ours {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	paths := make([]string, 0, len(names)+1)
	for _, name := range names {
		paths = append(paths, byName[name])
	}
	var out []sysctlConf
	for _, p := range append(paths, ufwSysctlFile) {
		data, err := m.d.readFile(p)
		switch {
		case p == ufwSysctlFile && errors.Is(err, fs.ErrNotExist):
			continue
		case err != nil:
			return nil, fmt.Errorf("tuning: read %s: %w", p, err)
		}
		out = append(out, sysctlConf{path: p, set: assignments(data)})
	}
	return out, nil
}

// congestionChoices are the algorithms the kernel lists as available.
func (m module) congestionChoices() []string {
	data, err := m.d.readFile(availableCongestion)
	if err != nil {
		return nil
	}
	return strings.Fields(string(data))
}

// readLive reads key's value as the file writes it; a key the kernel lacks
// comes back with ok false.
func readLive(ctx context.Context, env core.Env, k sysctlKey) (string, bool, error) {
	v, err := env.Files.ReadSysctl(ctx, ID, k.path())
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "", false, nil
	case err != nil:
		return "", false, err
	}
	return fromKernel(v), true, nil
}

func (m module) sysctlActions() []core.ActionDef {
	spec := func(name, summary string, class core.Class) core.ActionSpec {
		return core.ActionSpec{
			Name: ID + "." + name, Version: 1, Summary: summary, Class: class, Side: core.SideAgent,
			LockKeys: []string{sysctlLockKey}, MinScope: core.ScopeAdmin,
		}
	}
	apply := spec("sysctl.apply", "Set kernel parameters now and in 99-zz-darkpanel.conf; unconfirmed, the change reverts.", core.ClassRevertible)
	apply.RepairedBy = ID + ".sysctl.repair"
	repair := spec("sysctl.repair", "Rewrite 99-zz-darkpanel.conf and the live values from the last confirmed settings.", core.ClassCompensable)
	repair.ForwardOnly, repair.Converges = true, true
	return []core.ActionDef{
		core.DefineRead[struct{}, SysctlState](core.ActionSpec{
			Name: ID + ".sysctl.get", Version: 1, Class: core.ClassRead, Side: core.SideAgent,
			Summary: "Read the kernel parameters Darkpanel tunes: live, set by Darkpanel, and later files.",
		}, sysctlGetter{m: m}),
		core.DefineRevertible[SysctlInput, SysctlApplied](apply, sysctlHandler{m: m}),
		core.DefineMutating[struct{}, SysctlRepaired](repair, sysctlRepairer{m: m}),
	}
}

type sysctlGetter struct{ m module }

func (g sysctlGetter) Read(ctx context.Context, env core.Env, _ struct{}) (SysctlState, error) {
	ours, _, err := g.m.readOurs()
	if err != nil {
		return SysctlState{}, err
	}
	set := map[string]string{}
	for _, a := range assignments(ours) {
		set[a.name] = a.value
	}
	later, err := g.m.laterFiles()
	if err != nil {
		return SysctlState{}, err
	}
	out := SysctlState{File: sysctlFile, Keys: []SysctlKeyState{}}
	for _, k := range curated {
		s := SysctlKeyState{Key: k.name, Help: k.help, Kind: k.kind, Set: set[k.name], Choices: k.choices}
		if k.kind != kindChoice {
			s.Min, s.Max = strconv.FormatInt(k.min, 10), strconv.FormatInt(k.max, 10)
		}
		if k.name == congestionKey {
			s.Choices = g.m.congestionChoices()
		}
		for _, f := range later {
			if slices.ContainsFunc(f.set, func(a assignment) bool { return a.sets(k.name) }) {
				s.LaterFiles = append(s.LaterFiles, f.path)
			}
		}
		live, ok, err := readLive(ctx, env, k)
		if err != nil {
			return SysctlState{}, err
		}
		s.Live = live
		if !ok {
			s.Unavailable = "This kernel has no " + k.name + "."
		}
		out.Keys = append(out.Keys, s)
	}
	for _, o := range g.m.owners() {
		out.Keys = append(out.Keys, SysctlKeyState{Key: o.name, Owner: o.module})
	}
	return out, nil
}

// sysctlTarget is what a change starts from and leaves.
type sysctlTarget struct {
	cur     []byte
	present bool
	next    []byte            // nil: no file
	values  map[string]string // the file's values afterwards
	live    map[string]string // the live values of the keys in values, now
	writes  []string          // the keys whose live value changes, in file order
}

func (t sysctlTarget) fileChanges() bool {
	return !bytes.Equal(t.next, t.cur) || (t.next != nil) != t.present
}

// check validates the input's keys and values and returns them as the file
// writes them.
func (m module) check(values map[string]string) (map[string]string, error) {
	out := map[string]string{}
	var choices []string
	if _, ok := values[congestionKey]; ok {
		choices = m.congestionChoices()
	}
	for _, name := range slices.Sorted(maps.Keys(values)) {
		field := "/values/" + strings.NewReplacer("~", "~0", "/", "~1").Replace(name)
		k, ok := curatedKey(name)
		if !ok {
			if o := m.owner(name); o != "" {
				return nil, invalid(field, name+" is managed by the "+o+" module")
			}
			return nil, invalid(field, name+" is not a kernel parameter Darkpanel tunes")
		}
		v, problem := k.canon(values[name], choices)
		if problem != "" {
			return nil, invalid(field, problem)
		}
		out[name] = v
	}
	return out, nil
}

// target reads the file and the live values of values.
func (m module) target(ctx context.Context, env core.Env, values map[string]string) (sysctlTarget, error) {
	cur, present, err := m.readOurs()
	if err != nil {
		return sysctlTarget{}, err
	}
	t := sysctlTarget{cur: cur, present: present, next: render(values), values: values, live: map[string]string{}}
	for _, k := range curated {
		v, ok := values[k.name]
		if !ok {
			continue
		}
		live, ok, err := readLive(ctx, env, k)
		if err != nil {
			return sysctlTarget{}, err
		}
		if !ok {
			return sysctlTarget{}, invalid("/values/"+k.name, "This kernel has no "+k.name+".")
		}
		t.live[k.name] = live
		if live != v {
			t.writes = append(t.writes, k.name)
		}
	}
	return t, nil
}

// diff lists the live values a change sets and the file lines it removes
// and adds.
func (t sysctlTarget) diff() []core.DiffLine {
	diff := []core.DiffLine{}
	for _, name := range t.writes {
		diff = append(diff, core.DiffLine{Op: " ", Text: fmt.Sprintf("Set %s from %s to %s now", name, t.live[name], t.values[name])})
	}
	if !t.fileChanges() {
		return diff
	}
	if t.next == nil {
		return append(diff, core.DiffLine{Op: "-", Text: "Remove " + sysctlFile + "; the other files and kernel defaults apply at the next boot"})
	}
	split := func(b []byte) []string {
		var out []string
		for _, a := range assignments(b) {
			out = append(out, a.name+" = "+a.value)
		}
		return out
	}
	old, now := split(t.cur), split(t.next)
	for _, l := range old {
		if !slices.Contains(now, l) {
			diff = append(diff, core.DiffLine{Op: "-", Text: l})
		}
	}
	for _, l := range now {
		if !slices.Contains(old, l) {
			diff = append(diff, core.DiffLine{Op: "+", Text: l})
		}
	}
	return diff
}

func (t sysctlTarget) hash(extra string) string {
	b := fmt.Appendf(nil, "%t\n%s\n%s\n", t.present, t.cur, extra)
	for _, k := range curated {
		if v, ok := t.live[k.name]; ok {
			b = fmt.Appendf(b, "%s=%s\n", k.name, v)
		}
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// The effects of tuning.sysctl.apply: one per live value, then the file.
const (
	effectPrefix = ID + ".sysctl:"
	fileEffect   = effectPrefix + "99-zz-darkpanel.conf"
)

// fileState is the state document of the file: its text, null when it is
// absent.
func fileState(data []byte, present bool) core.StateDoc {
	var p *string
	if present {
		s := string(data)
		p = &s
	}
	b, _ := json.Marshal(p)
	return core.StateDoc{State: b}
}

func liveState(v string) core.StateDoc {
	b, _ := json.Marshal(v)
	return core.StateDoc{State: b}
}

// writeFile writes data as the file, or removes it when data is nil.
func writeFile(ctx context.Context, env core.Env, data []byte) error {
	if data == nil {
		return env.Files.Remove(ctx, ID, sysctlFile)
	}
	return env.Files.WriteFile(ctx, ID, sysctlFile, data, 0o644)
}

func writeLive(ctx context.Context, env core.Env, name, value string) error {
	k, _ := curatedKey(name)
	return env.Files.WriteSysctl(ctx, ID, k.path(), k.kernel(value))
}

// sysctlSnapshot is what tuning.sysctl.apply found: the file and the live
// values it changes.
type sysctlSnapshot struct {
	File *string           `json:"file"`
	Live map[string]string `json:"live"`
}

type sysctlHandler struct{ m module }

func (h sysctlHandler) plan(ctx context.Context, env core.Env, in SysctlInput) (sysctlTarget, error) {
	values, err := h.m.check(in.Values)
	if err != nil {
		return sysctlTarget{}, err
	}
	t, err := h.m.target(ctx, env, values)
	if err != nil {
		return sysctlTarget{}, err
	}
	if len(t.writes) == 0 && !t.fileChanges() {
		return sysctlTarget{}, invalid("", "The kernel and "+sysctlFile+" hold these values already.")
	}
	return t, nil
}

func (h sysctlHandler) Plan(ctx context.Context, env core.Env, in SysctlInput) (core.PlanOutcome, error) {
	t, err := h.plan(ctx, env, in)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	return core.PlanOutcome{Diff: t.diff(), StateHash: t.hash("")}, nil
}

func (h sysctlHandler) Snapshot(ctx context.Context, env core.Env, in SysctlInput) ([]byte, error) {
	values, err := h.m.check(in.Values)
	if err != nil {
		return nil, err
	}
	t, err := h.m.target(ctx, env, values)
	if err != nil {
		return nil, err
	}
	s := sysctlSnapshot{Live: map[string]string{}}
	if t.present {
		f := string(t.cur)
		s.File = &f
	}
	for _, name := range t.writes {
		s.Live[name] = t.live[name]
	}
	return json.Marshal(s)
}

// Apply writes the live values, then the file. A value the kernel refuses
// and does not take ends the change with nothing more written.
func (h sysctlHandler) Apply(ctx context.Context, env core.Env, in SysctlInput) (SysctlApplied, error) {
	t, err := h.plan(ctx, env, in)
	if err != nil {
		return SysctlApplied{}, err
	}
	for i, name := range t.writes {
		after := liveState(t.values[name])
		seq, err := env.Effects.Intend(ctx, core.Effect{
			Kind: core.EffectModify, Resource: effectPrefix + name, Local: true, InverseLocal: true,
			Before: liveState(t.live[name]), After: &after,
		}, nil)
		if err != nil {
			return SysctlApplied{}, err
		}
		if err := writeLive(ctx, env, name, t.values[name]); err != nil {
			k, _ := curatedKey(name)
			now, ok, rerr := readLive(ctx, env, k)
			switch {
			case rerr != nil || !ok:
				return SysctlApplied{}, err
			case now != t.live[name]:
				// The kernel holds a value other than the one written: the
				// effect is made as found, so the revert puts the old one back.
				if merr := env.Effects.Made(ctx, seq, effectPrefix+name, liveState(now)); merr != nil {
					return SysctlApplied{}, errors.Join(err, merr)
				}
				return SysctlApplied{}, err
			}
			if serr := env.Effects.Skipped(ctx, seq); serr != nil {
				return SysctlApplied{}, serr
			}
			refused := errcode.New(errcode.InvalidInput, fmt.Sprintf("The kernel refused %s = %s: %v", name, t.values[name], err))
			if i == 0 {
				return SysctlApplied{}, core.Definite(refused)
			}
			return SysctlApplied{}, refused
		}
		if err := env.Effects.Made(ctx, seq, effectPrefix+name, after); err != nil {
			return SysctlApplied{}, err
		}
	}
	if t.fileChanges() {
		after := fileState(t.next, t.next != nil)
		seq, err := env.Effects.Intend(ctx, core.Effect{
			Kind: core.EffectModify, Resource: fileEffect, Local: true, InverseLocal: true,
			Before: fileState(t.cur, t.present), After: &after,
		}, nil)
		if err != nil {
			return SysctlApplied{}, err
		}
		if err := writeFile(ctx, env, t.next); err != nil {
			return SysctlApplied{}, err
		}
		if err := env.Effects.Made(ctx, seq, fileEffect, after); err != nil {
			return SysctlApplied{}, err
		}
	}
	return SysctlApplied{Set: t.values, Written: slices.Concat([]string{}, t.writes)}, nil
}

func (h sysctlHandler) Verify(ctx context.Context, env core.Env, in SysctlInput, _ SysctlApplied) error {
	values, err := h.m.check(in.Values)
	if err != nil {
		return err
	}
	return h.m.verify(ctx, env, values)
}

// verify checks the kernel's values and the file against values.
func (m module) verify(ctx context.Context, env core.Env, values map[string]string) error {
	t, err := m.target(ctx, env, values)
	if err != nil {
		return err
	}
	if len(t.writes) > 0 {
		name := t.writes[0]
		return fmt.Errorf("tuning: the kernel reads %s = %s, want %s", name, t.live[name], values[name])
	}
	if t.fileChanges() {
		return fmt.Errorf("tuning: %s does not hold the values set", sysctlFile)
	}
	return nil
}

func (h sysctlHandler) Observe(ctx context.Context, env core.Env, e core.Effect) (core.Observation, error) {
	if e.Resource == fileEffect {
		data, present, err := h.m.readOurs()
		if err != nil {
			return core.Observation{}, err
		}
		return core.Observation{Current: fileState(data, present), Resource: fileEffect, Matches: 1, Exclusive: true}, nil
	}
	k, ok := curatedKey(strings.TrimPrefix(e.Resource, effectPrefix))
	if !ok {
		return core.Observation{}, fmt.Errorf("tuning: effect %d names %s, not a key of the module", e.Seq, e.Resource)
	}
	v, _, err := readLive(ctx, env, k)
	if err != nil {
		return core.Observation{}, err
	}
	return core.Observation{Current: liveState(v), Resource: e.Resource, Matches: 1, Exclusive: true}, nil
}

// Revert puts back the file and each live value the change still holds as
// it left them, the file first. An effect in doubt blocks the revert;
// tuning.sysctl.repair is the way out.
func (h sysctlHandler) Revert(ctx context.Context, env core.Env, snapshot []byte) (core.RevertOutcome, error) {
	var snap sysctlSnapshot
	if err := json.Unmarshal(snapshot, &snap); err != nil {
		return core.RevertOutcome{}, fmt.Errorf("tuning: decode the snapshot: %w", err)
	}
	effects, err := env.Effects.List(ctx)
	if err != nil {
		return core.RevertOutcome{}, err
	}
	for _, e := range effects {
		if e.State == core.EffectIntended || e.State == core.EffectUnresolved {
			return core.RevertOutcome{}, fmt.Errorf("tuning: the revert left %s as it is: effect %d is %s; resolve it, then repair the change", e.Resource, e.Seq, e.State)
		}
	}
	var outcomes []core.EffectOutcome
	for _, e := range slices.Backward(effects) {
		if e.State != core.EffectMade {
			continue
		}
		obs, err := h.Observe(ctx, env, e)
		if err != nil {
			return core.RevertOutcome{}, err
		}
		if e.Post == nil || !bytes.Equal(e.Post.State, obs.Current.State) {
			current := obs.Current
			outcomes = append(outcomes, core.EffectOutcome{Seq: e.Seq, State: core.EffectUnresolved, Reason: core.ReasonConflict, Current: &current})
			continue
		}
		if e.Resource == fileEffect {
			var data []byte
			if snap.File != nil {
				data = []byte(*snap.File)
			}
			err = writeFile(ctx, env, data)
		} else {
			name := strings.TrimPrefix(e.Resource, effectPrefix)
			prior, ok := snap.Live[name]
			if !ok {
				return core.RevertOutcome{}, fmt.Errorf("tuning: the snapshot holds no value of %s", name)
			}
			err = writeLive(ctx, env, name, prior)
		}
		if err != nil {
			return core.RevertOutcome{}, err
		}
		outcomes = append(outcomes, core.EffectOutcome{Seq: e.Seq, State: core.EffectUndone})
	}
	return core.RevertOutcome{Effects: outcomes}, nil
}

// lastConfirmed is the input of the last confirmed tuning.sysctl.apply
// change and its id, "" when none was confirmed.
func lastConfirmed(ctx context.Context, env core.Env) (map[string]string, string, error) {
	if env.DB == nil {
		return nil, "", errors.New("tuning: no store to read the confirmed settings from")
	}
	var id, input string
	err := env.DB.Read().QueryRowContext(ctx,
		`SELECT id, input_json FROM core_changes WHERE action = ? AND state = ? ORDER BY rowid DESC LIMIT 1`,
		ID+".sysctl.apply", core.StateConfirmed).Scan(&id, &input)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return map[string]string{}, "", nil
	case err != nil:
		return nil, "", fmt.Errorf("tuning: read the last confirmed sysctl change: %w", err)
	}
	var in SysctlInput
	if err := json.Unmarshal([]byte(input), &in); err != nil {
		return nil, "", fmt.Errorf("tuning: decode the input of change %s: %w", id, err)
	}
	if in.Values == nil {
		in.Values = map[string]string{}
	}
	return in.Values, id, nil
}

// sysctlRepairer is tuning.sysctl.repair: it writes the last confirmed
// values live and to the file, or removes the file when none were
// confirmed. Run again it does the same.
type sysctlRepairer struct{ m module }

func (h sysctlRepairer) target(ctx context.Context, env core.Env) (sysctlTarget, string, error) {
	raw, id, err := lastConfirmed(ctx, env)
	if err != nil {
		return sysctlTarget{}, "", err
	}
	values, err := h.m.check(raw)
	if err != nil {
		return sysctlTarget{}, "", err
	}
	t, err := h.m.target(ctx, env, values)
	return t, id, err
}

func (h sysctlRepairer) Plan(ctx context.Context, env core.Env, _ struct{}) (core.PlanOutcome, error) {
	t, id, err := h.target(ctx, env)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	diff := t.diff()
	switch {
	case len(diff) == 0:
	case t.next == nil:
		diff[len(diff)-1].Text = "Remove " + sysctlFile + ": no settings were confirmed"
	default:
		diff = append([]core.DiffLine{{Op: " ", Text: "Rewrite from change " + id}}, diff...)
	}
	return core.PlanOutcome{Diff: diff, StateHash: t.hash(id)}, nil
}

func (h sysctlRepairer) Apply(ctx context.Context, env core.Env, _ struct{}) (SysctlRepaired, error) {
	t, id, err := h.target(ctx, env)
	if err != nil {
		return SysctlRepaired{}, err
	}
	for _, name := range t.writes {
		if err := writeLive(ctx, env, name, t.values[name]); err != nil {
			return SysctlRepaired{}, err
		}
	}
	if t.fileChanges() {
		if err := writeFile(ctx, env, t.next); err != nil {
			return SysctlRepaired{}, err
		}
	}
	return SysctlRepaired{From: id, Written: slices.Concat([]string{}, t.writes)}, nil
}

func (h sysctlRepairer) Verify(ctx context.Context, env core.Env, _ struct{}, _ SysctlRepaired) error {
	t, _, err := h.target(ctx, env)
	if err != nil {
		return err
	}
	return h.m.verify(ctx, env, t.values)
}
