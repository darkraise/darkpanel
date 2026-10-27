package firewall

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/modules/firewall/conf"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

// Rule tuples as UFW 0.36 writes them (captured in containers): fhRule22
// is a both-family Darkpanel rule tagged fhTag, fhRule3000 an IPv4 manual
// rule, fhRule53 an IPv6 manual rule.
const (
	fhTag        = "01J00000000000000000000001"
	fhRule22v4   = "### tuple ### allow tcp 22 0.0.0.0/0 any 0.0.0.0/0 in comment=6461726b70616e656c3a30314a3030303030303030303030303030303030303030303031"
	fhRule22v6   = "### tuple ### allow tcp 22 ::/0 any ::/0 in comment=6461726b70616e656c3a30314a3030303030303030303030303030303030303030303031"
	fhRule3000v4 = "### tuple ### allow tcp 3000 0.0.0.0/0 any 10.0.0.0/8 in"
	fhRule53v6   = "### tuple ### deny udp 53 ::/0 any 2001:db8::/32 in"
)

// fhSave is the loaded state every fake host starts with.
const fhSave = "*filter\n:INPUT DROP [0:0]\n:FORWARD DROP [0:0]\n:OUTPUT ACCEPT [0:0]\n:ufw-user-input - [0:0]\n" +
	"-A INPUT -j ufw-user-input\n-A ufw-user-input -p tcp -m tcp --dport 22 -j ACCEPT\nCOMMIT\n"

// fhState is what the scripted ufw edits: the values of the four files ufw
// writes.
type fhState struct {
	Enabled                bool
	LogLevel               string
	IPv6                   bool
	NoSysctl               bool // /etc/default/ufw leaves IPT_SYSCTL unset, as ufw leaves it
	Input, Output, Forward string
	Rules4, Rules6         []string // tuple lines
}

// fhStateOf reads the edited values back from a configuration.
func fhStateOf(cfg fwstate.Config) fhState {
	s := fhState{Enabled: cfg.Enabled, LogLevel: cfg.LogLevel, IPv6: cfg.IPv6, NoSysctl: cfg.SysctlFile == "",
		Input: cfg.DefaultInput, Output: cfg.DefaultOutput, Forward: cfg.DefaultForward}
	for _, t := range cfg.Rules4 {
		s.Rules4 = append(s.Rules4, t.Raw)
	}
	for _, t := range cfg.Rules6 {
		s.Rules6 = append(s.Rules6, t.Raw)
	}
	return s
}

func fhRules(lines []string) []byte {
	var b strings.Builder
	b.WriteString("*filter\n### RULES ###\n")
	for _, l := range lines {
		b.WriteString(l + "\n")
	}
	b.WriteString("### END RULES ###\nCOMMIT\n")
	return []byte(b.String())
}

func fhYes(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

// files renders the four files ufw writes, by absolute path.
func (s fhState) files() map[string][]byte {
	iptSysctl := "IPT_SYSCTL=/etc/ufw/sysctl.conf\n"
	if s.NoSysctl {
		iptSysctl = ""
	}
	return map[string][]byte{
		"/etc/ufw/user.rules":  fhRules(s.Rules4),
		"/etc/ufw/user6.rules": fhRules(s.Rules6),
		"/etc/ufw/ufw.conf":    []byte(fmt.Sprintf("ENABLED=%s\nLOGLEVEL=%s\n", fhYes(s.Enabled), s.LogLevel)),
		"/etc/default/ufw": []byte(fmt.Sprintf("IPV6=%s\nDEFAULT_INPUT_POLICY=%q\nDEFAULT_OUTPUT_POLICY=%q\n"+
			"DEFAULT_FORWARD_POLICY=%q\nMANAGE_BUILTINS=no\n%s",
			fhYes(s.IPv6), s.Input, s.Output, s.Forward, iptSysctl)),
	}
}

// fakeHost is a test host: UFW's files under a temporary root, which the
// real conf.Read parses; a scripted ufw; other programs that only record
// their input; the live sysctl values; the loaded state; and an in-memory
// effects journal. It implements core.Runner and core.FileWriter.
type fakeHost struct {
	t    *testing.T
	root string
	db   *store.DB

	// ufw scripts the ufw vectors by strings.Join(args, " "): the function
	// edits the files' values and returns ufw's exit code. An unscripted
	// vector exits 1 and changes nothing.
	ufw map[string]func(s *fhState) int
	// onRun runs after every other program and may change the host.
	onRun  func(h *fakeHost, cmd core.Cmd) core.CmdResult
	runErr map[string]error // Run fails before the program starts, by "<path> <args>"
	calls  []string         // "run <path> <args>", "restore <path>", "sysctl <key>=<value>", in order
	stdin  map[string][]byte

	sysctl     map[string]string // live values, by /proc/sys key
	sysctlFail map[string]error  // WriteSysctl fails, by key
	sysctlLie  map[string]string // ReadSysctl answers this instead, by key

	loaded     fwstate.Loaded // V4 and V6 readLoaded returns; Sysctl comes from sysctl
	loadErr    error
	predicted  *fwstate.Loaded // what predict returns; nil means loaded
	predictErr error
	diffs      []fwstate.ChainDiff // what compare returns
	keeps      []func(f fwstate.Family, table, chain string) bool

	guardOK     bool
	guardDetail string
	guardErr    error
	guardCalls  int

	journal *fhJournal
	clock   *coretest.FakeClock
}

// newFakeHost writes a host whose UFW is active with fhRule22 and
// fhRule3000, and whose loaded state is fhSave.
func newFakeHost(t *testing.T) *fakeHost {
	t.Helper()
	h := &fakeHost{
		t: t, root: t.TempDir(), db: fwTestDB(t),
		ufw:   map[string]func(s *fhState) int{},
		stdin: map[string][]byte{}, runErr: map[string]error{},
		sysctl: map[string]string{
			"net/ipv4/ip_forward": "0", "net/ipv4/conf/all/rp_filter": "2", "net/ipv4/conf/default/rp_filter": "1",
			"net/ipv4/conf/eth0/rp_filter": "1", "net/ipv6/conf/all/forwarding": "0", "net/ipv6/conf/eth0/accept_ra": "1",
		},
		sysctlFail: map[string]error{}, sysctlLie: map[string]string{},
		guardOK: true,
		journal: &fhJournal{},
		clock:   coretest.NewFakeClock(time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)),
	}
	for p, data := range map[string]string{
		"/etc/ufw/before.rules": "*filter\n# before\nCOMMIT\n", "/etc/ufw/before6.rules": "*filter\n# before6\nCOMMIT\n",
		"/etc/ufw/after.rules": "*filter\n# after\nCOMMIT\n", "/etc/ufw/after6.rules": "*filter\n# after6\nCOMMIT\n",
		"/etc/ufw/sysctl.conf": "net/ipv4/ip_forward=0\n",
	} {
		h.writeFile(p, []byte(data))
	}
	h.setState(fhState{Enabled: true, LogLevel: "low", IPv6: true, Input: "DROP", Output: "ACCEPT", Forward: "DROP",
		Rules4: []string{fhRule22v4, fhRule3000v4}, Rules6: []string{fhRule22v6}})
	h.loaded = fwstate.Loaded{Variant: "nft", V4: fhParse(t, fhSave), V6: fhParse(t, fhSave)}
	return h
}

func fhParse(t *testing.T, text string) *netfilter.Ruleset {
	t.Helper()
	rs, err := netfilter.Parse(strings.NewReader(text))
	if err != nil {
		t.Fatal(err)
	}
	return rs
}

func (h *fakeHost) writeFile(p string, data []byte) {
	h.t.Helper()
	full := filepath.Join(h.root, p)
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(full, data, 0o600); err != nil {
		h.t.Fatal(err)
	}
}

// setState writes the four files ufw writes.
func (h *fakeHost) setState(s fhState) {
	h.t.Helper()
	for p, data := range s.files() {
		h.writeFile(p, data)
	}
}

// config reads the host's configuration as production does.
func (h *fakeHost) config() fwstate.Config {
	h.t.Helper()
	cfg, err := conf.Read(h.root)
	if err != nil {
		h.t.Fatal(err)
	}
	return cfg
}

// state reads the values ufw edits from the host's files.
func (h *fakeHost) state() fhState { return fhStateOf(h.config()) }

func (h *fakeHost) Run(_ context.Context, cmd core.Cmd) (core.CmdResult, error) {
	key := strings.TrimSpace(cmd.Path + " " + strings.Join(cmd.Args, " "))
	h.calls = append(h.calls, "run "+key)
	if err := h.runErr[key]; err != nil {
		return core.CmdResult{}, err
	}
	if cmd.Path != "/usr/sbin/ufw" {
		h.stdin[cmd.Path] = cmd.Stdin
		if h.onRun != nil {
			return h.onRun(h, cmd), nil
		}
		return core.CmdResult{}, nil
	}
	f, ok := h.ufw[strings.Join(cmd.Args, " ")]
	if !ok {
		return core.CmdResult{ExitCode: 1, Stderr: []byte("ERROR: unscripted vector\n")}, nil
	}
	s := h.state()
	code := f(&s)
	h.setState(s)
	return core.CmdResult{ExitCode: code}, nil
}

var errFakeUnused = errors.New("fake: not used by the firewall")

func (h *fakeHost) WriteFile(context.Context, string, string, []byte, fs.FileMode) error {
	return errFakeUnused
}
func (h *fakeHost) ReplaceBlock(context.Context, string, string, string, []byte) error {
	return errFakeUnused
}
func (h *fakeHost) RemoveBlock(context.Context, string, string, string) error { return errFakeUnused }
func (h *fakeHost) Remove(context.Context, string, string) error              { return errFakeUnused }
func (h *fakeHost) WriteManaged(context.Context, string, string, string, []byte, fs.FileMode, int, int) error {
	return errFakeUnused
}
func (h *fakeHost) RemoveManaged(context.Context, string, string, string) error { return errFakeUnused }
func (h *fakeHost) Rename(context.Context, string, string, string) error        { return errFakeUnused }
func (h *fakeHost) EnsureServiceDir(context.Context, string, string) error      { return errFakeUnused }
func (h *fakeHost) RenameIn(context.Context, string, string, string, string) error {
	return errFakeUnused
}
func (h *fakeHost) RemoveIn(context.Context, string, string, string) error { return errFakeUnused }
func (h *fakeHost) RemoveServiceDir(context.Context, string, string) error { return errFakeUnused }
func (h *fakeHost) Owns(context.Context, string, string) (bool, error)     { return false, errFakeUnused }

// Restore writes or removes a file under the root, as platform.Files does.
func (h *fakeHost) Restore(_ context.Context, module string, e core.RestoreEntry) error {
	if module != ID {
		return fmt.Errorf("fake: module %s restores %s", module, e.Path)
	}
	h.calls = append(h.calls, "restore "+e.Path)
	full := filepath.Join(h.root, e.Path)
	if !e.Present {
		if err := os.Remove(full); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := os.WriteFile(full, e.Data, e.Mode); err != nil {
		return err
	}
	return os.Chmod(full, e.Mode)
}

func (h *fakeHost) ReadSysctl(_ context.Context, module, key string) (string, error) {
	if module != ID {
		return "", fmt.Errorf("fake: module %s reads sysctl %s", module, key)
	}
	if v, ok := h.sysctlLie[key]; ok {
		return v, nil
	}
	v, ok := h.sysctl[key]
	if !ok {
		return "", fmt.Errorf("fake: sysctl %s: %w", key, fs.ErrNotExist)
	}
	return v, nil
}

func (h *fakeHost) WriteSysctl(_ context.Context, module, key, value string) error {
	if module != ID {
		return fmt.Errorf("fake: module %s writes sysctl %s", module, key)
	}
	h.calls = append(h.calls, "sysctl "+key+"="+value)
	if err := h.sysctlFail[key]; err != nil {
		return err
	}
	if _, ok := h.sysctl[key]; !ok {
		return fmt.Errorf("fake: sysctl %s: %w", key, fs.ErrNotExist)
	}
	h.sysctl[key] = value
	return nil
}

// env is the Env of change c1 by user:u1 on this host.
func (h *fakeHost) env() core.Env {
	return core.Env{
		HostID: "h1", ChangeID: "c1", Principal: core.Principal{Actor: "user:u1", Scope: core.ScopeAdmin},
		Effects: h.journal, DB: h.db, Exec: h, Files: h, Clock: h.clock,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

// deps are the host-facing functions over this host: the real conf.Read
// and scripted readers, predictor, comparison and guard.
func (h *fakeHost) deps() deps {
	return deps{
		root:       h.root,
		readConfig: conf.Read,
		readLoaded: func(context.Context, core.Env) (fwstate.Loaded, error) {
			st := h.loaded
			st.Sysctl = maps.Clone(h.sysctl)
			return st, h.loadErr
		},
		predict: func(fwstate.Config, fwstate.Loaded, fwstate.Transition) (fwstate.Loaded, error) {
			if h.predicted != nil {
				return *h.predicted, h.predictErr
			}
			return h.loaded, h.predictErr
		},
		edit: func(cfg fwstate.Config, args []string) (fwstate.Config, error) {
			f, ok := h.ufw[strings.Join(args, " ")]
			if !ok {
				return fwstate.Config{}, fmt.Errorf("fake: unscripted vector %q", args)
			}
			s := fhStateOf(cfg)
			f(&s)
			root := h.t.TempDir()
			for p, e := range cfg.Files {
				if e.Present {
					if err := os.MkdirAll(filepath.Dir(filepath.Join(root, p)), 0o750); err != nil {
						return fwstate.Config{}, err
					}
					if err := os.WriteFile(filepath.Join(root, p), e.Data, 0o600); err != nil {
						return fwstate.Config{}, err
					}
				}
			}
			for p, data := range s.files() {
				if err := os.WriteFile(filepath.Join(root, p), data, 0o600); err != nil {
					return fwstate.Config{}, err
				}
			}
			return conf.Read(root)
		},
		files: func(cfg fwstate.Config) (map[string][]byte, error) { return fhStateOf(cfg).files(), nil },
		compare: func(_, _ fwstate.Loaded, keep func(f fwstate.Family, table, chain string) bool) []fwstate.ChainDiff {
			h.keeps = append(h.keeps, keep)
			return h.diffs
		},
		sysctl: func(_ fwstate.Config, cur map[string]string, _ fwstate.Transition) (map[string]string, error) {
			return cur, nil
		},
		lockout: func(context.Context, core.Env, fwstate.Loaded, *core.GuardContext) (bool, string, error) {
			h.guardCalls++
			return h.guardOK, h.guardDetail, h.guardErr
		},
	}
}

// fhJournal is an in-memory core.EffectJournal.
type fhJournal struct {
	effects []core.Effect
	failOn  string // "intend", "made" or "skipped": that method fails
}

func (j *fhJournal) Intend(_ context.Context, e core.Effect, _ []byte) (int, error) {
	if j.failOn == "intend" {
		return 0, errors.New("fake: journal unavailable")
	}
	e.Seq, e.State, e.ChangeID = len(j.effects)+1, core.EffectIntended, "c1"
	j.effects = append(j.effects, e)
	return e.Seq, nil
}

func (j *fhJournal) find(seq int) (*core.Effect, error) {
	if seq < 1 || seq > len(j.effects) || j.effects[seq-1].State != core.EffectIntended {
		return nil, fmt.Errorf("fake: effect %d is not intended", seq)
	}
	return &j.effects[seq-1], nil
}

func (j *fhJournal) Made(_ context.Context, seq int, resource string, post core.StateDoc) error {
	if j.failOn == "made" {
		return errors.New("fake: journal unavailable")
	}
	e, err := j.find(seq)
	if err != nil {
		return err
	}
	e.State, e.Resource, e.Post = core.EffectMade, resource, &post
	return nil
}

func (j *fhJournal) Skipped(_ context.Context, seq int) error {
	if j.failOn == "skipped" {
		return errors.New("fake: journal unavailable")
	}
	e, err := j.find(seq)
	if err != nil {
		return err
	}
	e.State = core.EffectSkipped
	return nil
}

func (j *fhJournal) List(context.Context) ([]core.Effect, error) {
	return append([]core.Effect(nil), j.effects...), nil
}

func (j *fhJournal) Secret(context.Context, int) ([]byte, error) { return nil, nil }

var (
	_ core.Runner        = (*fakeHost)(nil)
	_ core.FileWriter    = (*fakeHost)(nil)
	_ core.EffectJournal = (*fhJournal)(nil)
)
