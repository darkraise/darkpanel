package users

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
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform"
)

// The SSH daemon's files. sshd takes the first value of a keyword, and
// sshd_config includes the drop-ins before anything else, so the drop-in
// Darkpanel writes as 00- wins over 50-cloud-init.conf.
const (
	sshdConfig = "/etc/ssh/sshd_config"
	dropInDir  = "/etc/ssh/sshd_config.d"
	dropInFile = dropInDir + "/00-darkpanel.conf"
	sshUnit    = "ssh.service"
	// sshdLockKey serialises the module's sshd changes.
	sshdLockKey = "sshd"
)

// dropInHeader opens the file users.sshd.apply writes.
const dropInHeader = "# Written by Darkpanel (users.sshd.apply); Darkpanel rewrites this file.\n"

// The Include lines that read the drop-ins, absolute or relative to /etc/ssh.
var dropInIncludes = []string{dropInDir + "/*.conf", "sshd_config.d/*.conf"}

var (
	sshdUserPattern  = regexp.MustCompile(`^[a-z_*?][a-z0-9_*?-]{0,31}(@[A-Za-z0-9.:/*?-]{1,253})?$`)
	sshdGroupPattern = regexp.MustCompile(`^[a-z_*?][a-z0-9_*?-]{0,31}$`)
)

// The permitRootLogin values Darkpanel writes.
var rootLoginValues = []string{"yes", "prohibit-password", "no"}

// SshdSettings are the sshd settings Darkpanel writes; an omitted field is
// left to sshd's other files and its defaults.
type SshdSettings struct {
	PasswordAuthentication       *bool    `json:"passwordAuthentication,omitempty" jsonschema:"nullable"`
	KbdInteractiveAuthentication *bool    `json:"kbdInteractiveAuthentication,omitempty" jsonschema:"nullable"`
	PermitRootLogin              string   `json:"permitRootLogin,omitempty" jsonschema:"enum=yes,enum=prohibit-password,enum=no"`
	MaxAuthTries                 *int     `json:"maxAuthTries,omitempty" jsonschema:"nullable,minimum=1,maximum=10"`
	LoginGraceTime               *int     `json:"loginGraceTime,omitempty" jsonschema:"nullable,minimum=10,maximum=600" jsonschema_description:"Seconds."`
	AllowUsers                   []string `json:"allowUsers,omitempty" jsonschema:"maxItems=64" jsonschema_description:"User patterns, USER or USER@HOST; managed only when no other file under /etc/ssh sets an allow or deny list."`
	AllowGroups                  []string `json:"allowGroups,omitempty" jsonschema:"maxItems=64" jsonschema_description:"Group patterns; managed only when no other file under /etc/ssh sets an allow or deny list."`
	ClientAliveInterval          *int     `json:"clientAliveInterval,omitempty" jsonschema:"nullable,minimum=0,maximum=3600" jsonschema_description:"Seconds."`
	ClientAliveCountMax          *int     `json:"clientAliveCountMax,omitempty" jsonschema:"nullable,minimum=0,maximum=10"`
	X11Forwarding                *bool    `json:"x11Forwarding,omitempty" jsonschema:"nullable"`
}

// SshdEffective is what sshd -T reports for the fields of SshdSettings:
// another file can set a value outside the bounds Darkpanel writes, so it
// has none.
type SshdEffective struct {
	PasswordAuthentication       *bool    `json:"passwordAuthentication,omitempty" jsonschema:"nullable"`
	KbdInteractiveAuthentication *bool    `json:"kbdInteractiveAuthentication,omitempty" jsonschema:"nullable"`
	PermitRootLogin              string   `json:"permitRootLogin,omitempty" jsonschema:"enum=yes,enum=prohibit-password,enum=forced-commands-only,enum=no"`
	MaxAuthTries                 *int     `json:"maxAuthTries,omitempty" jsonschema:"nullable"`
	LoginGraceTime               *int     `json:"loginGraceTime,omitempty" jsonschema:"nullable" jsonschema_description:"Seconds; 0 is no limit."`
	AllowUsers                   []string `json:"allowUsers,omitempty"`
	AllowGroups                  []string `json:"allowGroups,omitempty"`
	ClientAliveInterval          *int     `json:"clientAliveInterval,omitempty" jsonschema:"nullable" jsonschema_description:"Seconds."`
	ClientAliveCountMax          *int     `json:"clientAliveCountMax,omitempty" jsonschema:"nullable"`
	X11Forwarding                *bool    `json:"x11Forwarding,omitempty" jsonschema:"nullable"`
}

// SshdState is the answer of users.sshd.get.
type SshdState struct {
	Effective  SshdEffective `json:"effective" jsonschema_description:"The values sshd -T reports."`
	Set        SshdSettings  `json:"set" jsonschema_description:"The values 00-darkpanel.conf holds; empty when the file is absent."`
	ReadOnly   []string      `json:"readOnly" jsonschema_description:"The fields Darkpanel leaves to another file: allowUsers and allowGroups when listsSetBy is set."`
	ListsSetBy string        `json:"listsSetBy,omitempty" jsonschema_description:"The file under /etc/ssh that sets AllowUsers, AllowGroups, DenyUsers or DenyGroups."`
	MatchIn    string        `json:"matchIn,omitempty" jsonschema_description:"A file holding a Match block, whose overrides sshd -T and the lockout guard do not see."`
}

// SshdInput is the input of users.sshd.apply: the whole of what
// 00-darkpanel.conf holds afterwards.
type SshdInput struct {
	core.Guarded
	SshdSettings
}

// SshdApplied is the answer of users.sshd.apply.
type SshdApplied struct {
	Set      SshdSettings `json:"set"`
	Reloaded bool         `json:"reloaded" jsonschema_description:"ssh.service was active and reloaded; under socket activation new connections read the file."`
}

// SshdRepaired is the answer of users.sshd.repair.
type SshdRepaired struct {
	From     string `json:"from,omitempty" jsonschema_description:"The confirmed users.sshd.apply change the file was rewritten from; empty when none was confirmed and the file was removed."`
	Reloaded bool   `json:"reloaded"`
}

// sshdField is one setting of SshdSettings: its sshd keyword and how its
// value reads and writes.
type sshdField struct {
	keyword string
	list    bool
	text    func(s *SshdSettings) (string, bool) // the value as written, false when unset
	parse   func(s *SshdSettings, v string) error
}

func boolField(keyword string, p func(*SshdSettings) **bool) sshdField {
	return sshdField{keyword: keyword,
		text: func(s *SshdSettings) (string, bool) {
			if *p(s) == nil {
				return "", false
			}
			return yesNo(**p(s)), true
		},
		parse: func(s *SshdSettings, v string) error {
			if v != "yes" && v != "no" {
				return fmt.Errorf("%s is %q, not yes or no", keyword, v)
			}
			b := v == "yes"
			*p(s) = &b
			return nil
		},
	}
}

func intField(keyword string, p func(*SshdSettings) **int) sshdField {
	return sshdField{keyword: keyword,
		text: func(s *SshdSettings) (string, bool) {
			if *p(s) == nil {
				return "", false
			}
			return strconv.Itoa(**p(s)), true
		},
		parse: func(s *SshdSettings, v string) error {
			n, err := strconv.Atoi(v)
			if err != nil {
				return fmt.Errorf("%s is %q, not a number", keyword, v)
			}
			*p(s) = &n
			return nil
		},
	}
}

func listField(keyword string, p func(*SshdSettings) *[]string) sshdField {
	return sshdField{keyword: keyword, list: true,
		text: func(s *SshdSettings) (string, bool) {
			return strings.Join(*p(s), " "), len(*p(s)) > 0
		},
		parse: func(s *SshdSettings, v string) error {
			*p(s) = append(*p(s), strings.Fields(v)...)
			return nil
		},
	}
}

// sshdFields are the settings in the order the file holds them.
var sshdFields = []sshdField{
	boolField("PasswordAuthentication", func(s *SshdSettings) **bool { return &s.PasswordAuthentication }),
	boolField("KbdInteractiveAuthentication", func(s *SshdSettings) **bool { return &s.KbdInteractiveAuthentication }),
	{keyword: "PermitRootLogin",
		text: func(s *SshdSettings) (string, bool) { return s.PermitRootLogin, s.PermitRootLogin != "" },
		parse: func(s *SshdSettings, v string) error {
			v = rootLogin(v)
			if !slices.Contains(rootLoginValues, v) && v != "forced-commands-only" {
				return fmt.Errorf("PermitRootLogin is %q", v)
			}
			s.PermitRootLogin = v
			return nil
		}},
	intField("MaxAuthTries", func(s *SshdSettings) **int { return &s.MaxAuthTries }),
	intField("LoginGraceTime", func(s *SshdSettings) **int { return &s.LoginGraceTime }),
	listField("AllowUsers", func(s *SshdSettings) *[]string { return &s.AllowUsers }),
	listField("AllowGroups", func(s *SshdSettings) *[]string { return &s.AllowGroups }),
	intField("ClientAliveInterval", func(s *SshdSettings) **int { return &s.ClientAliveInterval }),
	intField("ClientAliveCountMax", func(s *SshdSettings) **int { return &s.ClientAliveCountMax }),
	boolField("X11Forwarding", func(s *SshdSettings) **bool { return &s.X11Forwarding }),
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// rootLogin reads a PermitRootLogin value; sshd -T prints prohibit-password
// by its older name.
func rootLogin(v string) string {
	if v == "without-password" {
		return "prohibit-password"
	}
	return v
}

// render is the drop-in holding s, nil when s sets nothing.
func render(s SshdSettings) []byte {
	var b bytes.Buffer
	for _, f := range sshdFields {
		if v, ok := f.text(&s); ok {
			fmt.Fprintf(&b, "%s %s\n", f.keyword, v)
		}
	}
	if b.Len() == 0 {
		return nil
	}
	return append([]byte(dropInHeader), b.Bytes()...)
}

// directive is one keyword line of an sshd configuration file.
type directive struct {
	keyword string // lower case
	value   string
}

// directives are the keyword lines of data: comments and blank lines left
// out, the keyword split from its value by spaces or "=".
func directives(data []byte) []directive {
	var out []directive
	for line := range lines(data) {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		end := strings.IndexAny(line, " \t=")
		if end < 0 {
			out = append(out, directive{keyword: strings.ToLower(line)})
			continue
		}
		value := strings.TrimLeft(line[end:], " \t")
		value = strings.TrimSpace(strings.TrimPrefix(value, "="))
		out = append(out, directive{keyword: strings.ToLower(line[:end]), value: value})
	}
	return out
}

// parseSettings reads the settings of the drop-in Darkpanel wrote; a value
// edited by hand into one sshd -t would refuse is left out, and
// users.sshd.repair rewrites the file.
func parseSettings(data []byte) SshdSettings {
	var s SshdSettings
	for _, d := range directives(data) {
		for _, f := range sshdFields {
			if strings.ToLower(f.keyword) != d.keyword {
				continue
			}
			v := d.value
			if !f.list {
				v = strings.ToLower(v)
			}
			_ = f.parse(&s, v)
		}
	}
	return s
}

// sshdFile is one configuration file sshd reads.
type sshdFile struct {
	path string
	data []byte
}

// sshdFiles is what sshd reads besides sshd -T: the drop-in Darkpanel
// writes, and the other files in the order sshd reads them, the drop-ins
// before the rest of sshd_config.
type sshdFiles struct {
	ours    []byte
	present bool
	others  []sshdFile
}

// errNoSSHServer is the availability reason on a host without OpenSSH's
// server.
var errNoSSHServer = errors.New("the OpenSSH server is not installed: " + sshdConfig + " is missing")

// readSSHD reads sshd's files and refuses an sshd_config that does not
// include the drop-ins before any other directive.
func (m module) readSSHD() (sshdFiles, error) {
	config, err := m.d.readFile(sshdConfig)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return sshdFiles{}, errNoSSHServer
	case err != nil:
		return sshdFiles{}, fmt.Errorf("users: read %s: %w", sshdConfig, err)
	}
	ds := directives(config)
	if len(ds) == 0 || ds[0].keyword != "include" || !slices.ContainsFunc(strings.Fields(ds[0].value), func(p string) bool {
		return slices.Contains(dropInIncludes, p)
	}) {
		return sshdFiles{}, fmt.Errorf("%s does not include %s/*.conf before its other settings", sshdConfig, dropInDir)
	}
	paths, err := m.d.glob(dropInDir + "/*.conf")
	if err != nil {
		return sshdFiles{}, fmt.Errorf("users: list %s: %w", dropInDir, err)
	}
	slices.Sort(paths)
	var f sshdFiles
	for _, p := range paths {
		data, err := m.d.readFile(p)
		if err != nil {
			return sshdFiles{}, fmt.Errorf("users: read %s: %w", p, err)
		}
		if p == dropInFile {
			f.ours, f.present = data, true
			continue
		}
		f.others = append(f.others, sshdFile{path: p, data: data})
	}
	f.others = append(f.others, sshdFile{path: sshdConfig, data: config})
	return f, nil
}

// sets is the first other file holding one of keywords, "" when none does.
func (f sshdFiles) sets(keywords ...string) string {
	for _, o := range f.others {
		if slices.ContainsFunc(directives(o.data), func(d directive) bool { return slices.Contains(keywords, d.keyword) }) {
			return o.path
		}
	}
	return ""
}

// listsSetBy is the other file that sets an allow or deny list.
func (f sshdFiles) listsSetBy() string {
	return f.sets("allowusers", "allowgroups", "denyusers", "denygroups")
}

// matchIn is the other file holding a Match block.
func (f sshdFiles) matchIn() string { return f.sets("match") }

// firstValue is the value sshd takes for keyword from the other files,
// outside Match blocks, "" when none sets it.
func (f sshdFiles) firstValue(keyword string) string {
	for _, o := range f.others {
		for _, d := range directives(o.data) {
			if d.keyword == "match" {
				break
			}
			if d.keyword == keyword {
				return strings.ToLower(d.value)
			}
		}
	}
	return ""
}

// sshdT is sshd -T's output: each keyword's values, one per line printed.
type sshdT map[string][]string

func (t sshdT) first(keyword string) string {
	if v := t[keyword]; len(v) > 0 {
		return v[0]
	}
	return ""
}

func readSshdT(ctx context.Context, env core.Env) (sshdT, []byte, error) {
	res, err := env.Exec.Run(ctx, core.Cmd{Path: platform.SshdPath, Args: platform.SshdEffective()})
	if err != nil {
		return nil, nil, fmt.Errorf("users: run sshd -T: %w", err)
	}
	if res.ExitCode != 0 {
		return nil, nil, fmt.Errorf("users: sshd -T exited %d: %s", res.ExitCode, strings.TrimSpace(string(res.Stderr)))
	}
	t := sshdT{}
	for line := range lines(res.Stdout) {
		k, v, _ := strings.Cut(line, " ")
		t[k] = append(t[k], strings.TrimSpace(v))
	}
	return t, res.Stdout, nil
}

// effective reads the settings of sshdFields from sshd -T.
func (t sshdT) effective() SshdEffective {
	var s SshdSettings
	for _, f := range sshdFields {
		for _, v := range t[strings.ToLower(f.keyword)] {
			if !f.list {
				v = strings.ToLower(v)
			}
			if f.parse(&s, v) != nil || !f.list {
				break
			}
		}
	}
	return SshdEffective(s)
}

func (m module) sshdAvailable(context.Context, core.Env) error {
	_, err := m.readSSHD()
	return err
}

func (m module) sshdActions() []core.ActionDef {
	spec := func(name, summary string, class core.Class) core.ActionSpec {
		return core.ActionSpec{
			Name: ID + "." + name, Version: 1, Summary: summary, Class: class, Side: core.SideAgent,
			LockKeys: []string{sshdLockKey}, MinScope: core.ScopeAdmin, Available: m.sshdAvailable,
		}
	}
	apply := spec("sshd.apply", "Write sshd's settings to 00-darkpanel.conf and reload sshd; unconfirmed, the change reverts.", core.ClassRevertible)
	apply.RepairedBy = ID + ".sshd.repair"
	apply.Guards = []core.GuardSpec{{ID: GuardNoWayIn, Overridable: true}}
	repair := spec("sshd.repair", "Rewrite 00-darkpanel.conf from the last confirmed settings, or remove it, and reload sshd.", core.ClassCompensable)
	repair.ForwardOnly, repair.Converges = true, true
	return []core.ActionDef{
		core.DefineRead[struct{}, SshdState](core.ActionSpec{
			Name: ID + ".sshd.get", Version: 1, Class: core.ClassRead, Side: core.SideAgent, Available: m.sshdAvailable,
			Summary: "Read sshd's effective settings and those Darkpanel set.",
		}, sshdGetter{m: m}),
		core.DefineRevertible[SshdInput, SshdApplied](apply, sshdHandler{m: m}),
		core.DefineMutating[struct{}, SshdRepaired](repair, sshdRepairer{m: m}),
	}
}

type sshdGetter struct{ m module }

func (g sshdGetter) Read(ctx context.Context, env core.Env, _ struct{}) (SshdState, error) {
	f, err := g.m.readSSHD()
	if err != nil {
		return SshdState{}, err
	}
	set := parseSettings(f.ours)
	t, _, err := readSshdT(ctx, env)
	if err != nil {
		return SshdState{}, err
	}
	out := SshdState{Effective: t.effective(), Set: set, ReadOnly: []string{}, ListsSetBy: f.listsSetBy(), MatchIn: f.matchIn()}
	if out.ListsSetBy != "" {
		out.ReadOnly = []string{"allowUsers", "allowGroups"}
	}
	return out, nil
}

// check validates s against spec section 3's ranges and the lists another
// file sets.
func (s SshdSettings) check(f sshdFiles) error {
	ranged := func(path string, v *int, lo, hi int) error {
		if v != nil && (*v < lo || *v > hi) {
			return invalid(path, fmt.Sprintf("must be between %d and %d", lo, hi))
		}
		return nil
	}
	if s.PermitRootLogin != "" && !slices.Contains(rootLoginValues, s.PermitRootLogin) {
		return invalid("/permitRootLogin", "must be yes, prohibit-password or no")
	}
	for _, err := range []error{
		ranged("/maxAuthTries", s.MaxAuthTries, 1, 10), ranged("/loginGraceTime", s.LoginGraceTime, 10, 600),
		ranged("/clientAliveInterval", s.ClientAliveInterval, 0, 3600), ranged("/clientAliveCountMax", s.ClientAliveCountMax, 0, 10),
		checkPatterns("/allowUsers", s.AllowUsers, sshdUserPattern), checkPatterns("/allowGroups", s.AllowGroups, sshdGroupPattern),
	} {
		if err != nil {
			return err
		}
	}
	if by := f.listsSetBy(); by != "" && (len(s.AllowUsers) > 0 || len(s.AllowGroups) > 0) {
		path := "/allowUsers"
		if len(s.AllowUsers) == 0 {
			path = "/allowGroups"
		}
		return invalid(path, by+" sets an allow or deny list, so Darkpanel leaves AllowUsers and AllowGroups to it")
	}
	return nil
}

func checkPatterns(path string, patterns []string, re *regexp.Regexp) error {
	if len(patterns) > 64 {
		return invalid(path, "at most 64 patterns")
	}
	for i, p := range patterns {
		switch {
		case !re.MatchString(p):
			return invalid(fmt.Sprintf("%s/%d", path, i), fmt.Sprintf("%q is not a pattern sshd takes here", p))
		case slices.Index(patterns, p) < i:
			return invalid(fmt.Sprintf("%s/%d", path, i), p+" is listed twice")
		}
	}
	return nil
}

// sshdEffect is the one modify effect of users.sshd.apply.
const sshdEffect = ID + ".sshd:00-darkpanel.conf"

// dropInState is the state document of the drop-in: its text, null when it
// is absent.
func dropInState(data []byte, present bool) core.StateDoc {
	var p *string
	if present {
		s := string(data)
		p = &s
	}
	b, _ := json.Marshal(p)
	return core.StateDoc{State: b}
}

// readDropIn reads 00-darkpanel.conf.
func (m module) readDropIn() ([]byte, bool, error) {
	data, err := m.d.readFile(dropInFile)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, false, nil
	case err != nil:
		return nil, false, fmt.Errorf("users: read %s: %w", dropInFile, err)
	}
	return data, true, nil
}

// writeDropIn writes data as the drop-in, or removes it when data is nil.
func writeDropIn(ctx context.Context, env core.Env, data []byte) error {
	if data == nil {
		return env.Files.Remove(ctx, ID, dropInFile)
	}
	return env.Files.WriteFile(ctx, ID, dropInFile, data, 0o644)
}

// sshdTest runs sshd -t; a refusal comes back with sshd's message.
func sshdTest(ctx context.Context, env core.Env) error {
	res, err := env.Exec.Run(ctx, core.Cmd{Path: platform.SshdPath, Args: platform.SshdTest()})
	if err != nil {
		return fmt.Errorf("users: run sshd -t: %w", err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("sshd -t exited %d: %s", res.ExitCode, strings.TrimSpace(string(res.Stderr)))
	}
	return nil
}

// reloadSSHD reloads ssh.service when it is active; under socket activation
// the next connection reads the files.
func reloadSSHD(ctx context.Context, env core.Env) (bool, error) {
	st, err := env.Units.State(ctx, sshUnit)
	if err != nil {
		return false, fmt.Errorf("users: read %s: %w", sshUnit, err)
	}
	if st.ActiveState != "active" {
		return false, nil
	}
	r, ok := env.Units.(core.UnitReloader)
	if !ok {
		return false, errors.New("users: the unit client cannot reload " + sshUnit)
	}
	if err := r.ReloadUnit(ctx, sshUnit); err != nil {
		return false, fmt.Errorf("users: reload %s: %w", sshUnit, err)
	}
	return true, nil
}

// dropInDiff lists the drop-in's lines next leaves out of cur and those it
// adds.
func dropInDiff(cur, next []byte) []core.DiffLine {
	if next == nil {
		return []core.DiffLine{{Op: "-", Text: "Remove " + dropInFile + "; sshd's other files and defaults apply"}}
	}
	split := func(b []byte) []string {
		var out []string
		for line := range lines(b) {
			if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
				out = append(out, line)
			}
		}
		return out
	}
	old, now := split(cur), split(next)
	diff := []core.DiffLine{}
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

type sshdHandler struct{ m module }

// plan reads what the change starts from: the files, the drop-in it
// writes and sshd -T.
func (h sshdHandler) plan(ctx context.Context, env core.Env, in SshdInput) (sshdFiles, []byte, sshdT, []byte, error) {
	f, err := h.m.readSSHD()
	if err != nil {
		return sshdFiles{}, nil, nil, nil, core.Definite(errcode.New(errcode.InvalidInput, err.Error()))
	}
	if err := in.check(f); err != nil {
		return sshdFiles{}, nil, nil, nil, err
	}
	next := render(in.SshdSettings)
	if bytes.Equal(next, f.ours) && (next != nil) == f.present {
		return sshdFiles{}, nil, nil, nil, invalid("", dropInFile+" holds these settings already")
	}
	t, out, err := readSshdT(ctx, env)
	if err != nil {
		return sshdFiles{}, nil, nil, nil, err
	}
	return f, next, t, out, nil
}

func (h sshdHandler) Plan(ctx context.Context, env core.Env, in SshdInput) (core.PlanOutcome, error) {
	f, next, t, out, err := h.plan(ctx, env, in)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	g, err := h.m.sshdGuard(ctx, env, in, f, t)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	sum := sha256.Sum256(fmt.Appendf(nil, "%t\n%s\n%s", f.present, f.ours, out))
	return core.PlanOutcome{Diff: dropInDiff(f.ours, next), Guards: []core.Guard{g}, StateHash: hex.EncodeToString(sum[:])}, nil
}

func (h sshdHandler) Snapshot(context.Context, core.Env, SshdInput) ([]byte, error) {
	data, present, err := h.m.readDropIn()
	if err != nil {
		return nil, err
	}
	return dropInState(data, present).State, nil
}

// Apply writes the drop-in and runs sshd -t, which puts the previous file
// back at once when it refuses the new one; sshd reloads only after it
// passes.
func (h sshdHandler) Apply(ctx context.Context, env core.Env, in SshdInput) (SshdApplied, error) {
	f, next, _, _, err := h.plan(ctx, env, in)
	if err != nil {
		return SshdApplied{}, err
	}
	after := dropInState(next, next != nil)
	seq, err := env.Effects.Intend(ctx, core.Effect{
		Kind: core.EffectModify, Resource: sshdEffect, Local: true, InverseLocal: true,
		Before: dropInState(f.ours, f.present), After: &after,
	}, nil)
	if err != nil {
		return SshdApplied{}, err
	}
	if err := writeDropIn(ctx, env, next); err != nil {
		return SshdApplied{}, err
	}
	if err := sshdTest(ctx, env); err != nil {
		prior := f.ours
		if !f.present {
			prior = nil
		}
		if rerr := writeDropIn(ctx, env, prior); rerr != nil {
			return SshdApplied{}, fmt.Errorf("users: %w; putting the previous %s back failed: %w", err, dropInFile, rerr)
		}
		if serr := env.Effects.Skipped(ctx, seq); serr != nil {
			return SshdApplied{}, serr
		}
		return SshdApplied{}, core.Definite(errcode.New(errcode.InvalidInput,
			"sshd refused the settings, so the previous "+dropInFile+" is back: "+err.Error()))
	}
	if err := env.Effects.Made(ctx, seq, sshdEffect, after); err != nil {
		return SshdApplied{}, err
	}
	reloaded, err := reloadSSHD(ctx, env)
	if err != nil {
		return SshdApplied{}, err
	}
	return SshdApplied{Set: in.SshdSettings, Reloaded: reloaded}, nil
}

// Verify compares sshd -T with every setting the input writes; with none,
// the drop-in is gone.
func (h sshdHandler) Verify(ctx context.Context, env core.Env, in SshdInput, _ SshdApplied) error {
	return verifySettings(ctx, env, h.m, in.SshdSettings)
}

func verifySettings(ctx context.Context, env core.Env, m module, want SshdSettings) error {
	if render(want) == nil {
		if _, present, err := m.readDropIn(); err != nil || present {
			return errors.Join(err, fmt.Errorf("users: %s is still there", dropInFile))
		}
		return nil
	}
	t, _, err := readSshdT(ctx, env)
	if err != nil {
		return err
	}
	got := SshdSettings(t.effective())
	for _, f := range sshdFields {
		w, ok := f.text(&want)
		if !ok {
			continue
		}
		g, _ := f.text(&got)
		if f.list {
			ws, gs := strings.Fields(w), strings.Fields(g)
			slices.Sort(ws)
			slices.Sort(gs)
			w, g = strings.Join(ws, " "), strings.Join(gs, " ")
		}
		if g != w {
			return fmt.Errorf("users: sshd -T reports %s %q, want %q", f.keyword, g, w)
		}
	}
	return nil
}

func (h sshdHandler) Observe(context.Context, core.Env, core.Effect) (core.Observation, error) {
	data, present, err := h.m.readDropIn()
	if err != nil {
		return core.Observation{}, err
	}
	return core.Observation{Current: dropInState(data, present), Resource: sshdEffect, Matches: 1, Exclusive: true}, nil
}

// Revert puts the snapshot's drop-in back when the file is still as the
// change left it, runs sshd -t and reloads sshd. An effect in doubt blocks
// the revert; users.sshd.repair is the way out.
func (h sshdHandler) Revert(ctx context.Context, env core.Env, snapshot []byte) (core.RevertOutcome, error) {
	var prior *string
	if err := json.Unmarshal(snapshot, &prior); err != nil {
		return core.RevertOutcome{}, fmt.Errorf("users: decode the snapshot: %w", err)
	}
	effects, err := env.Effects.List(ctx)
	if err != nil {
		return core.RevertOutcome{}, err
	}
	var outcomes []core.EffectOutcome
	for _, e := range effects {
		switch e.State {
		case core.EffectIntended, core.EffectUnresolved:
			return core.RevertOutcome{}, fmt.Errorf("users: the revert left %s as it is: effect %d is %s; resolve it, then repair the change", dropInFile, e.Seq, e.State)
		case core.EffectMade:
		default:
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
		var data []byte
		if prior != nil {
			data = []byte(*prior)
		}
		if err := writeDropIn(ctx, env, data); err != nil {
			return core.RevertOutcome{}, err
		}
		if err := sshdTest(ctx, env); err != nil {
			return core.RevertOutcome{}, fmt.Errorf("users: the previous %s is back, but sshd was not reloaded: %w", dropInFile, err)
		}
		if _, err := reloadSSHD(ctx, env); err != nil {
			return core.RevertOutcome{}, err
		}
		outcomes = append(outcomes, core.EffectOutcome{Seq: e.Seq, State: core.EffectUndone})
	}
	return core.RevertOutcome{Effects: outcomes}, nil
}

// lastConfirmed is the input of the last confirmed users.sshd.apply change
// and its id, "" when none was confirmed.
func lastConfirmed(ctx context.Context, env core.Env) (SshdSettings, string, error) {
	if env.DB == nil {
		return SshdSettings{}, "", errors.New("users: no store to read the confirmed settings from")
	}
	var id, input string
	err := env.DB.Read().QueryRowContext(ctx,
		`SELECT id, input_json FROM core_changes WHERE action = ? AND state = ? ORDER BY rowid DESC LIMIT 1`,
		ID+".sshd.apply", core.StateConfirmed).Scan(&id, &input)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return SshdSettings{}, "", nil
	case err != nil:
		return SshdSettings{}, "", fmt.Errorf("users: read the last confirmed sshd change: %w", err)
	}
	var in SshdInput
	if err := json.Unmarshal([]byte(input), &in); err != nil {
		return SshdSettings{}, "", fmt.Errorf("users: decode the input of change %s: %w", id, err)
	}
	return in.SshdSettings, id, nil
}

// sshdRepairer is users.sshd.repair: it writes the last confirmed settings,
// or removes the drop-in when none were confirmed, then runs sshd -t and
// reloads sshd. Run again it does the same.
type sshdRepairer struct{ m module }

func (h sshdRepairer) target(ctx context.Context, env core.Env) (sshdFiles, []byte, string, error) {
	f, err := h.m.readSSHD()
	if err != nil {
		return sshdFiles{}, nil, "", core.Definite(errcode.New(errcode.InvalidInput, err.Error()))
	}
	s, id, err := lastConfirmed(ctx, env)
	if err != nil {
		return sshdFiles{}, nil, "", err
	}
	return f, render(s), id, nil
}

func (h sshdRepairer) Plan(ctx context.Context, env core.Env, _ struct{}) (core.PlanOutcome, error) {
	f, next, id, err := h.target(ctx, env)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	sum := sha256.Sum256(fmt.Appendf(nil, "%t\n%s\n%s", f.present, f.ours, id))
	hash := hex.EncodeToString(sum[:])
	switch {
	case next == nil && !f.present, next != nil && f.present && bytes.Equal(next, f.ours):
		return core.PlanOutcome{Diff: []core.DiffLine{}, StateHash: hash}, nil
	case next == nil:
		return core.PlanOutcome{Diff: []core.DiffLine{{Op: "-", Text: "Remove " + dropInFile + ": no settings were confirmed"}}, StateHash: hash}, nil
	}
	diff := append([]core.DiffLine{{Op: " ", Text: "Rewrite " + dropInFile + " from change " + id}}, dropInDiff(f.ours, next)...)
	return core.PlanOutcome{Diff: diff, StateHash: hash}, nil
}

func (h sshdRepairer) Apply(ctx context.Context, env core.Env, _ struct{}) (SshdRepaired, error) {
	f, next, id, err := h.target(ctx, env)
	if err != nil {
		return SshdRepaired{}, err
	}
	if next != nil || f.present {
		if err := writeDropIn(ctx, env, next); err != nil {
			return SshdRepaired{}, err
		}
	}
	if err := sshdTest(ctx, env); err != nil {
		return SshdRepaired{}, fmt.Errorf("users: %s is rewritten, but sshd was not reloaded: %w", dropInFile, err)
	}
	reloaded, err := reloadSSHD(ctx, env)
	if err != nil {
		return SshdRepaired{}, err
	}
	return SshdRepaired{From: id, Reloaded: reloaded}, nil
}

func (h sshdRepairer) Verify(ctx context.Context, env core.Env, _ struct{}, _ SshdRepaired) error {
	s, _, err := lastConfirmed(ctx, env)
	if err != nil {
		return err
	}
	return verifySettings(ctx, env, h.m, s)
}
