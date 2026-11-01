package tailscale

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/netip"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/platform"
	"github.com/darkraise/darkpanel/internal/platform/netstat"
	"github.com/darkraise/darkpanel/internal/platform/tslocal"
)

var (
	_ core.Runner        = (*tsHost)(nil)
	_ core.FileWriter    = (*tsHost)(nil)
	_ core.Units         = (*tsUnits)(nil)
	_ core.EffectJournal = (*tsJournal)(nil)
)

// thHostname is the host name the test host reports.
const thHostname = "ts-host-1"

// thDpkgPath and thOSReleasePath are the files the module reads through deps.readFile.
const (
	thDpkgPath      = "/var/lib/dpkg/status"
	thOSReleasePath = "/etc/os-release"
)

var (
	errTHUnused = errors.New("tsHost: not used by the tailscale module")
	errTHNoFake = errors.New("tsHost: nothing is faked for this probe; the test sets deps itself")
)

// thDebPattern is the managed-dir file-name pattern the module declares for staged packages.
var thDebPattern = regexp.MustCompile(`^tailscale_[0-9][0-9A-Za-z.+~-]{0,63}_(amd64|arm64)\.deb$`)

// tsRun is one scripted answer of a command.
type tsRun struct {
	res core.CmdResult
	err error
}

// tsHost is the fake host a tailscale test runs on: a fake local API, socket table, units, command runner,
// file provider and agent database. It stands in for core.Runner and core.FileWriter, so every command goes
// through the production exec allowlist and every file write is checked against the module's owned paths.
type tsHost struct {
	t       *testing.T
	local   *tsLocal
	link    *tsLink
	units   *tsUnits
	files   map[string][]byte // what Files wrote, by path (managed files as dir + "/" + name)
	reads   map[string][]byte // what deps.readFile answers before it looks at files; a missing path is fs.ErrNotExist
	ran     [][]string        // every exec vector run: path, then args
	scripts map[string][]tsRun
	db      *store.DB
	journal *tsJournal
	clock   *coretest.FakeClock
	owned   map[string]bool // paths Files recorded as owned
	session *tsSession      // the fake of deps.session

	mu sync.Mutex
}

// newTSHost returns a host running a logged-in tailscaled 1.104.1 on Ubuntu 24.04 (noble) for amd64, with the
// tailscale package installed, no controller unit, and an agent database with the core and tailscale
// migrations applied. The clock reads 2026-10-31 12:00 UTC.
func newTSHost(t *testing.T) *tsHost {
	t.Helper()
	ctx := context.Background()
	start := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(dir, "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	coreSet, err := store.CoreMigrations(store.SideAgent)
	if err != nil {
		t.Fatal(err)
	}
	res, err := store.Migrate(ctx, db, store.MigrateOptions{
		Side: store.SideAgent, BinaryVersion: "test", BackupRoot: filepath.Join(dir, "backups"), Keep: 5, Now: start,
	}, coreSet, []store.MigrationSet{{Module: ID, FS: agentMigrations()}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Failed) > 0 {
		t.Fatalf("migrations failed: %+v", res.Failed)
	}
	addr := netip.MustParseAddr("100.64.0.7")
	self := &tslocal.PeerStatus{
		ID: "n1", HostName: thHostname, DNSName: thHostname + ".example.ts.net.", OS: "linux",
		TailscaleIPs: []netip.Addr{addr}, Online: true,
	}
	return &tsHost{
		t: t,
		local: &tsLocal{
			Status: &tslocal.Status{
				Version: "1.104.1", BackendState: "Running", TailscaleIPs: []netip.Addr{addr}, Self: self,
				MagicDNSSuffix: "example.ts.net", CurrentTailnet: &tslocal.TailnetStatus{Name: "example.com", MagicDNSSuffix: "example.ts.net", MagicDNSEnabled: true},
			},
			Prefs: &tslocal.Prefs{WantRunning: true, CorpDNS: true},
		},
		link: &tsLink{
			Ifaces: []netstat.Interface{
				{Index: 1, Name: "lo", Up: true, Running: true, Loopback: true, Addrs: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/8")}},
				{Index: 2, Name: "eth0", Up: true, Running: true, Addrs: []netip.Prefix{netip.MustParsePrefix("192.0.2.10/24")}},
				{Index: 3, Name: "tailscale0", Up: true, Running: true, Addrs: []netip.Prefix{netip.PrefixFrom(addr, 32)}},
			},
			Pid: 4242,
		},
		units: &tsUnits{States: map[string]core.UnitState{
			tailscaledUnit: {LoadState: "loaded", ActiveState: "active", SubState: "running", MainPID: 900},
			controllerUnit: {LoadState: "not-found"},
		}},
		files: map[string][]byte{},
		reads: map[string][]byte{
			thDpkgPath:      thDpkgStatus("1.104.1"),
			thOSReleasePath: []byte("PRETTY_NAME=\"Ubuntu 24.04.1 LTS\"\nNAME=\"Ubuntu\"\nVERSION_ID=\"24.04\"\nVERSION_CODENAME=noble\nID=ubuntu\nID_LIKE=debian\n"),
		},
		scripts: map[string][]tsRun{},
		db:      db,
		journal: &tsJournal{},
		clock:   coretest.NewFakeClock(start),
		owned:   map[string]bool{},
		session: &tsSession{},
	}
}

// thDpkgStatus is a /var/lib/dpkg/status with a few unrelated packages and, when version is not empty, the
// tailscale package installed at that version.
func thDpkgStatus(version string) []byte {
	var b strings.Builder
	b.WriteString("Package: tailscale-archive-keyring\nStatus: install ok installed\nVersion: 9.9.9\nArchitecture: all\n\n")
	b.WriteString("Package: curl\nStatus: install ok installed\nVersion: 8.5.0-2ubuntu10\nArchitecture: amd64\nDescription: command line tool\n for transferring data\n\n")
	if version != "" {
		fmt.Fprintf(&b, "Package: tailscale\nStatus: install ok installed\nPriority: optional\nArchitecture: amd64\nVersion: %s\nDescription: The easiest, most secure way to use WireGuard\n\n", version)
	}
	b.WriteString("Package: zlib1g\nStatus: install ok installed\nVersion: 1:1.3.dfsg-3.1ubuntu2\nArchitecture: amd64\n")
	return []byte(b.String())
}

// ts returns a ts whose deps all point at the host's fakes. The STUN and HTTPS probes answer an error until a
// test sets t.d.stun or t.d.https.
func (h *tsHost) ts() ts {
	return ts{d: deps{
		local:     func() localAPI { return thLocal{h.local} },
		link:      thLink{h.link},
		stun:      func(context.Context, netip.AddrPort) (time.Duration, error) { return 0, errTHNoFake },
		https:     func(context.Context, string) (time.Duration, error) { return 0, errTHNoFake },
		osRelease: func() (osInfo, error) { return readOSRelease(h.readFile) },
		arch:      "amd64",
		hostname:  func() (string, error) { return thHostname, nil },
		readFile:  h.readFile,
		session:   h.session.answer,
	}}
}

// readFile answers reads first, then what Files wrote.
func (h *tsHost) readFile(name string) ([]byte, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if b, ok := h.reads[name]; ok {
		return bytes.Clone(b), nil
	}
	if b, ok := h.files[name]; ok {
		return bytes.Clone(b), nil
	}
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}

// env returns the environment of a call of the change; an empty changeID is a plan or a read, which has no
// effects journal.
func (h *tsHost) env(changeID string) core.Env {
	env := core.Env{
		HostID:    "h1",
		ChangeID:  changeID,
		Principal: core.Principal{Actor: "user:u1", UserID: "u1", Scope: core.ScopeAdmin},
		DB:        h.db,
		Exec:      h,
		Files:     h,
		Sealer:    coretest.Sealer{},
		Clock:     h.clock,
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		Units:     h.units,
	}
	if changeID != "" {
		h.journal.mu.Lock()
		h.journal.change = changeID
		h.journal.mu.Unlock()
		env.Effects = h.journal
	}
	return env
}

// script queues answers for the command whose path and arguments, joined by single spaces, are vector.
// Each run of that command consumes one answer; a command with none left answers exit 0 and no output.
func (h *tsHost) script(vector string, runs ...tsRun) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.scripts[vector] = append(h.scripts[vector], runs...)
}

// thAllowed reports whether the production exec allowlist admits the command.
func thAllowed(path string, args []string) bool {
	for _, rule := range platform.ProductionRules() {
		if rule.Path == path && rule.Match(args) == nil {
			return true
		}
	}
	return false
}

// Run records the vector, refuses one the production allowlist does not admit, and answers the next scripted
// result of the vector. It runs nothing.
func (h *tsHost) Run(_ context.Context, cmd core.Cmd) (core.CmdResult, error) {
	vector := append([]string{cmd.Path}, cmd.Args...)
	key := strings.Join(vector, " ")
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ran = append(h.ran, vector)
	if !thAllowed(cmd.Path, cmd.Args) {
		return core.CmdResult{}, fmt.Errorf("tsHost: %s is not in the production exec allowlist", key)
	}
	queue := h.scripts[key]
	if len(queue) == 0 {
		return core.CmdResult{}, nil
	}
	h.scripts[key] = queue[1:]
	return queue[0].res, queue[0].err
}

// thOwned refuses what the module's manifest would not let its file provider write.
func thOwned(module, path, kind string) error {
	if module != ID {
		return fmt.Errorf("tsHost: module %q is not %q", module, ID)
	}
	for _, op := range ownedPaths() {
		if op.Path == path && op.Kind == kind {
			return nil
		}
	}
	return fmt.Errorf("tsHost: %s is not an owned path of kind %s", path, kind)
}

func (h *tsHost) WriteFile(_ context.Context, module, path string, data []byte, _ fs.FileMode) error {
	if err := thOwned(module, path, core.OwnedFile); err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.files[path] = bytes.Clone(data)
	h.owned[path] = true
	return nil
}

func (h *tsHost) Remove(_ context.Context, module, path string) error {
	if err := thOwned(module, path, core.OwnedFile); err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.files, path)
	delete(h.owned, path)
	return nil
}

func (h *tsHost) WriteManaged(_ context.Context, module, dir, name string, data []byte, _ fs.FileMode, _, _ int) error {
	if err := thOwned(module, dir, core.OwnedManagedDir); err != nil {
		return err
	}
	if !thDebPattern.MatchString(name) {
		return fmt.Errorf("tsHost: %q does not match the managed-dir pattern", name)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.files[dir+"/"+name] = bytes.Clone(data)
	h.owned[dir+"/"+name] = true
	return nil
}

func (h *tsHost) RemoveManaged(_ context.Context, module, dir, name string) error {
	if err := thOwned(module, dir, core.OwnedManagedDir); err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.files, dir+"/"+name)
	delete(h.owned, dir+"/"+name)
	return nil
}

// Owns reports whether Files wrote path and has not removed it.
func (h *tsHost) Owns(_ context.Context, module, path string) (bool, error) {
	if module != ID {
		return false, fmt.Errorf("tsHost: module %q is not %q", module, ID)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.owned[path], nil
}

func (h *tsHost) ReplaceBlock(context.Context, string, string, string, []byte) error {
	return errTHUnused
}

func (h *tsHost) RemoveBlock(context.Context, string, string, string) error { return errTHUnused }

func (h *tsHost) Restore(context.Context, string, core.RestoreEntry) error { return errTHUnused }

func (h *tsHost) ReadSysctl(context.Context, string, string) (string, error) {
	return "", errTHUnused
}

func (h *tsHost) WriteSysctl(context.Context, string, string, string) error { return errTHUnused }

func (h *tsHost) Rename(context.Context, string, string, string) error { return errTHUnused }

func (h *tsHost) EnsureServiceDir(context.Context, string, string) error { return errTHUnused }

func (h *tsHost) RenameIn(context.Context, string, string, string, string) error {
	return errTHUnused
}

func (h *tsHost) RemoveIn(context.Context, string, string, string) error { return errTHUnused }

func (h *tsHost) RemoveServiceDir(context.Context, string, string) error { return errTHUnused }

// tsUnits is the module's unit client: State of tailscaled.service and darkpanel-controller.service answers
// from States, Restart of tailscaled.service is recorded, and every other call is refused, as the module's
// declared external units would refuse it.
type tsUnits struct {
	States   map[string]core.UnitState
	Restarts []string

	mu sync.Mutex
}

func (u *tsUnits) refuse(method, unit string) error {
	return fmt.Errorf("tsUnits: the module may not %s unit %s", method, unit)
}

func (u *tsUnits) State(_ context.Context, unit string) (core.UnitState, error) {
	if unit != tailscaledUnit && unit != controllerUnit {
		return core.UnitState{}, u.refuse("State", unit)
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if st, ok := u.States[unit]; ok {
		return st, nil
	}
	return core.UnitState{LoadState: "not-found"}, nil
}

func (u *tsUnits) Restart(_ context.Context, unit string) error {
	if unit != tailscaledUnit {
		return u.refuse("Restart", unit)
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.Restarts = append(u.Restarts, unit)
	return nil
}

func (u *tsUnits) StartTransientService(_ context.Context, name string, _ core.TransientService) error {
	return u.refuse("StartTransientService", name)
}

func (u *tsUnits) StartTransientTimer(_ context.Context, name string, _ core.TransientTimer) error {
	return u.refuse("StartTransientTimer", name)
}

func (u *tsUnits) Start(_ context.Context, unit string) error { return u.refuse("Start", unit) }
func (u *tsUnits) Stop(_ context.Context, unit string) error  { return u.refuse("Stop", unit) }
func (u *tsUnits) Kill(_ context.Context, unit string, _ syscall.Signal) error {
	return u.refuse("Kill", unit)
}
func (u *tsUnits) ResetFailed(_ context.Context, unit string) error {
	return u.refuse("ResetFailed", unit)
}
func (u *tsUnits) Enable(_ context.Context, unitFiles ...string) error {
	return u.refuse("Enable", strings.Join(unitFiles, ","))
}
func (u *tsUnits) Disable(_ context.Context, unitFiles ...string) error {
	return u.refuse("Disable", strings.Join(unitFiles, ","))
}
func (u *tsUnits) Reload(context.Context) error { return u.refuse("Reload", "") }
func (u *tsUnits) List(_ context.Context, pattern string) ([]core.UnitStatus, error) {
	return nil, u.refuse("List", pattern)
}

// tsJournal is an in-memory core.EffectJournal.
type tsJournal struct {
	mu      sync.Mutex
	change  string
	list    []core.Effect
	secrets map[int][]byte
}

// effects returns a copy of the journaled effects.
func (j *tsJournal) effects() []core.Effect {
	j.mu.Lock()
	defer j.mu.Unlock()
	return slices.Clone(j.list)
}

func (j *tsJournal) Intend(_ context.Context, e core.Effect, secret []byte) (int, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	e.Seq = len(j.list) + 1
	e.State = core.EffectIntended
	e.ChangeID = j.change
	j.list = append(j.list, e)
	if secret != nil {
		if j.secrets == nil {
			j.secrets = map[int][]byte{}
		}
		j.secrets[e.Seq] = bytes.Clone(secret)
	}
	return e.Seq, nil
}

// intended returns the index of effect seq when it is still intended.
func (j *tsJournal) intended(seq int) (int, error) {
	if seq < 1 || seq > len(j.list) {
		return 0, fmt.Errorf("fake: no effect %d", seq)
	}
	if st := j.list[seq-1].State; st != core.EffectIntended {
		return 0, fmt.Errorf("fake: effect %d is %s, not intended", seq, st)
	}
	return seq - 1, nil
}

func (j *tsJournal) Made(_ context.Context, seq int, resource string, post core.StateDoc) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	i, err := j.intended(seq)
	if err != nil {
		return err
	}
	j.list[i].State = core.EffectMade
	j.list[i].Resource = resource
	j.list[i].Post = &post
	return nil
}

func (j *tsJournal) Skipped(_ context.Context, seq int) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	i, err := j.intended(seq)
	if err != nil {
		return err
	}
	j.list[i].State = core.EffectSkipped
	return nil
}

func (j *tsJournal) List(context.Context) ([]core.Effect, error) { return j.effects(), nil }

func (j *tsJournal) Secret(_ context.Context, seq int) ([]byte, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if seq < 1 || seq > len(j.list) {
		return nil, fmt.Errorf("fake: no effect %d", seq)
	}
	return bytes.Clone(j.secrets[seq]), nil
}

// tsSession is the fake of deps.session: the agent's session to the controller as the pin's transient unit sees it.
// Call i answers Answers[min(i, len(Answers)-1)]; with no answers every call answers Up true.
type tsSession struct {
	Answers []tsSessionAnswer
	Calls   int

	mu sync.Mutex
}

// tsSessionAnswer is one answer of the fake session.
type tsSessionAnswer struct {
	Up, ViaTailnet bool
	Err            error
}

func (s *tsSession) answer(context.Context) (up, viaTailnet bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := tsSessionAnswer{Up: true}
	if len(s.Answers) > 0 {
		a = s.Answers[min(s.Calls, len(s.Answers)-1)]
	}
	s.Calls++
	return a.Up, a.ViaTailnet, a.Err
}
