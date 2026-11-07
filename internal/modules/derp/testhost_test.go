package derp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
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
	"github.com/darkraise/darkpanel/internal/modules/derp/server"
	"github.com/darkraise/darkpanel/internal/modules/tailnet"
	"github.com/darkraise/darkpanel/internal/platform"
	"github.com/darkraise/darkpanel/internal/platform/netstat"
	"github.com/darkraise/darkpanel/internal/platform/tslocal"
)

var (
	_ core.Runner        = (*dpHost)(nil)
	_ core.FileWriter    = (*dpHost)(nil)
	_ core.Units         = (*dpUnits)(nil)
	_ core.EffectJournal = (*dpJournal)(nil)
	_ localAPI           = thLocal{}
)

// thStart is the time the test host's clock starts at.
var thStart = time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

// thDPVersion and thArch are the darkpanel version and architecture the test host reports.
const (
	thDPVersion = "1.4.0"
	thArch      = "amd64"
)

// thUserID is the uid and gid a faked useradd gives the module's user.
const thUserID = 990

var errTHUnused = errors.New("dpHost: not used by the derp module")

// thServiceName is a file name inside the service directory or its certs subdirectory.
var thServiceName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

// dpHost is the fake host a derp test runs on: a fake local API, units, command runner, file provider, agent and
// controller databases and controller-side agents. It stands in for core.Runner and core.FileWriter, so every
// command goes through the production exec allowlist and every file write is checked against the module's owned
// paths.
type dpHost struct {
	t         *testing.T
	local     *dpLocal           // the fake local API
	units     *dpUnits           // records calls; State answers from a map
	files     map[string][]byte  // what Files wrote, by path (service-dir entries as dir + "/" + name)
	reads     map[string][]byte  // what deps.readFile answers before files; a missing path is fs.ErrNotExist
	ran       [][]string         // every exec vector run: path then args
	scripts   map[string][]dpRun // queued answers per "path arg0 arg1 ...", consumed in order; unscripted runs answer exit 0
	db        *store.DB          // agent database with the core and derp agent migrations
	ctlDB     *store.DB          // controller database with the core and derp controller migrations
	journal   *dpJournal         // an in-memory core.EffectJournal
	clock     *coretest.FakeClock
	owned     map[string]bool   // paths Files and the ownership store recorded as the module's
	users     map[string][2]int // lookupUser answers (uid, gid); useradd adds ServiceUser, userdel removes it
	sockets   []netstat.Socket
	link      bool   // linkViaTailnet's answer
	fetchBin  []byte // fetch's answer (sha computed); fetchErr overrides
	fetchErr  error
	fetches   []string           // "dpVersion want arch" per call
	tls       map[string]dpProbe // tlsProbe answers keyed by Address, else HostName; a missing key errors
	stun      map[netip.AddrPort]dpProbe
	resolved  map[string][]netip.Addr
	agents    map[string]*dpAgent // controller side: host id -> fake agent
	policy    tailnet.Policy      // policyErr overrides
	policyErr error
	devices   []tailnet.Device
	tailnet   bool // connected's answer

	seq int // the last plan and change number addChange used
	mu  sync.Mutex
}

// dpRun is one scripted answer of a command.
type dpRun struct {
	res core.CmdResult
	err error
}

// dpProbe is one faked probe answer.
type dpProbe struct {
	d   time.Duration
	err error
}

// newDPHost returns a host with a logged-in tailscaled 1.104.1 (BackendState Running, tags ["tag:derp"],
// 100.64.0.10) on amd64, darkpanel version "1.4.0", no DERP server, and agent and controller databases with the
// core and derp migrations applied. The clock reads thStart.
func newDPHost(t *testing.T) *dpHost {
	t.Helper()
	dir := t.TempDir()
	addr := netip.MustParseAddr("100.64.0.10")
	self := &tslocal.PeerStatus{
		ID: "n1", HostName: "derp-host", DNSName: "derp-host.example.ts.net.", OS: "linux",
		TailscaleIPs: []netip.Addr{addr}, Tags: []string{"tag:derp"}, Online: true,
	}
	return &dpHost{
		t: t,
		local: &dpLocal{
			Status: &tslocal.Status{
				Version: "1.104.1", BackendState: "Running", TailscaleIPs: []netip.Addr{addr}, Self: self,
				MagicDNSSuffix: "example.ts.net", CurrentTailnet: &tslocal.TailnetStatus{Name: "example.com", MagicDNSSuffix: "example.ts.net", MagicDNSEnabled: true},
			},
			WhoIs:   map[netip.Addr]*tslocal.WhoIs{},
			Regions: map[string]*tslocal.DERPRegionReport{},
			Errs:    map[string]error{},
		},
		units:    &dpUnits{States: map[string]core.UnitState{server.UnitName: {LoadState: "not-found"}}},
		files:    map[string][]byte{},
		reads:    map[string][]byte{},
		scripts:  map[string][]dpRun{},
		db:       thOpenDB(t, dir, store.SideAgent, agentMigrations()),
		ctlDB:    thOpenDB(t, dir, store.SideController, controllerMigrations()),
		journal:  &dpJournal{},
		clock:    coretest.NewFakeClock(thStart),
		owned:    map[string]bool{},
		users:    map[string][2]int{},
		tls:      map[string]dpProbe{},
		stun:     map[netip.AddrPort]dpProbe{},
		resolved: map[string][]netip.Addr{},
		agents:   map[string]*dpAgent{},
	}
}

// thOpenDB opens the database of side in dir with the core migrations of side and the module's migrations fsys.
func thOpenDB(t *testing.T, dir, side string, fsys fs.FS) *store.DB {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(dir, side+".db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	coreSet, err := store.CoreMigrations(side)
	if err != nil {
		t.Fatal(err)
	}
	res, err := store.Migrate(ctx, db, store.MigrateOptions{
		Side: side, BinaryVersion: "test", BackupRoot: filepath.Join(dir, side+"-backups"), Keep: 5, Now: thStart,
	}, coreSet, []store.MigrationSet{{Module: ID, FS: fsys}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Failed) > 0 {
		t.Fatalf("migrations failed: %+v", res.Failed)
	}
	return db
}

// dp returns a dp whose deps all point at the host's fakes.
func (h *dpHost) dp() dp {
	return dp{d: deps{
		local:          func() localAPI { return thLocal{h.local} },
		fetch:          h.fetch,
		version:        thDPVersion,
		arch:           thArch,
		readFile:       h.readFile,
		readCertFile:   h.readFile,
		lookupUser:     h.lookupUser,
		sockets:        h.listSockets,
		linkViaTailnet: h.linkViaTailnet,
		tlsProbe:       h.tlsProbe,
		stun:           h.stunProbe,
		resolve:        h.resolve,
		policy:         h.readPolicy,
		devices:        h.listDevices,
		connected:      h.connected,
	}}
}

func (h *dpHost) fetch(_ context.Context, dpVersion, want, arch string) ([]byte, string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.fetches = append(h.fetches, dpVersion+" "+want+" "+arch)
	if h.fetchErr != nil {
		return nil, "", h.fetchErr
	}
	if h.fetchBin == nil {
		return nil, "", errors.New("dpHost: nothing is faked for fetch")
	}
	sum := sha256.Sum256(h.fetchBin)
	return bytes.Clone(h.fetchBin), hex.EncodeToString(sum[:]), nil
}

// readFile answers reads first, then what Files wrote.
func (h *dpHost) readFile(name string) ([]byte, error) {
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

func (h *dpHost) lookupUser(name string) (uid, gid int, ok bool, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ids, ok := h.users[name]
	return ids[0], ids[1], ok, nil
}

func (h *dpHost) listSockets() ([]netstat.Socket, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.sockets), nil
}

func (h *dpHost) linkViaTailnet() (bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.link, nil
}

func (h *dpHost) tlsProbe(_ context.Context, t probeTarget) (time.Duration, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if p, ok := h.tls[t.Address]; ok && t.Address != "" {
		return p.d, p.err
	}
	if p, ok := h.tls[t.HostName]; ok {
		return p.d, p.err
	}
	return 0, fmt.Errorf("dpHost: nothing is faked for %s", t.HostName)
}

func (h *dpHost) stunProbe(_ context.Context, addr netip.AddrPort) (time.Duration, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if p, ok := h.stun[addr]; ok {
		return p.d, p.err
	}
	return 0, fmt.Errorf("dpHost: nothing is faked for %s", addr)
}

func (h *dpHost) resolve(_ context.Context, host string) ([]netip.Addr, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if addrs, ok := h.resolved[host]; ok {
		return slices.Clone(addrs), nil
	}
	return nil, fmt.Errorf("dpHost: no such host %s", host)
}

func (h *dpHost) readPolicy(context.Context, core.Env) (tailnet.Policy, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.policyErr != nil {
		return tailnet.Policy{}, h.policyErr
	}
	return h.policy, nil
}

func (h *dpHost) listDevices(context.Context, core.Env) ([]tailnet.Device, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.devices), nil
}

func (h *dpHost) connected(context.Context, *store.DB) (bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.tailnet, nil
}

// thPrincipal is the user every test call acts as.
var thPrincipal = core.Principal{Actor: "user:u1", UserID: "u1", Scope: core.ScopeAdmin}

// effects returns the journal for a call of the change, or nil for a plan or a read (an empty changeID).
func (h *dpHost) effects(changeID string) core.EffectJournal {
	if changeID == "" {
		return nil
	}
	h.journal.mu.Lock()
	h.journal.change = changeID
	h.journal.mu.Unlock()
	return h.journal
}

// env returns the agent-side environment of a call of the change; an empty changeID is a plan or a read, which
// has no effects journal.
func (h *dpHost) env(changeID string) core.Env {
	return core.Env{
		HostID:    "h1",
		ChangeID:  changeID,
		Principal: thPrincipal,
		Effects:   h.effects(changeID),
		DB:        h.db,
		Exec:      h,
		Files:     h,
		Sealer:    coretest.Sealer{},
		Clock:     h.clock,
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		Units:     h.units,
	}
}

// ctlEnv returns the controller-side environment of a call of the change. Its Agents answer through the host's
// dpAgents.
func (h *dpHost) ctlEnv(changeID string) core.Env {
	h.mu.Lock()
	hosts := coretest.MapHosts{}
	for id, a := range h.agents {
		hosts[id] = a.fake()
	}
	h.mu.Unlock()
	return core.Env{
		ChangeID:  changeID,
		Principal: thPrincipal,
		Effects:   h.effects(changeID),
		DB:        h.ctlDB,
		Sealer:    coretest.Sealer{},
		Clock:     h.clock,
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		Agents:    hosts,
	}
}

// script queues answers for the command whose path and arguments, joined by single spaces, are vector.
// Each run of that command consumes one answer; a command with none left answers exit 0 and no output.
func (h *dpHost) script(vector string, runs ...dpRun) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.scripts[vector] = append(h.scripts[vector], runs...)
}

// agent returns the fake agent of a host, creating it on first use.
func (h *dpHost) agent(hostID string) *dpAgent {
	h.mu.Lock()
	defer h.mu.Unlock()
	a, ok := h.agents[hostID]
	if !ok {
		a = &dpAgent{Reads: map[string]func(json.RawMessage) (any, error){}, Resources: map[string]any{}}
		h.agents[hostID] = a
	}
	return a
}

// addHost adds a core_hosts row to the controller database.
func (h *dpHost) addHost(id, name string, local bool) {
	h.t.Helper()
	err := h.ctlDB.Write(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO core_hosts (id, name, is_local, created_at) VALUES (?, ?, ?, ?)`,
			id, name, local, storeTime(h.clock.Now()))
		return err
	})
	if err != nil {
		h.t.Fatal(err)
	}
}

// addChange adds a controller change of action in state, with its plan, created now; it returns the change id.
func (h *dpHost) addChange(action, state string, input any) string {
	h.t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		h.t.Fatal(err)
	}
	h.mu.Lock()
	h.seq++
	n := h.seq
	h.mu.Unlock()
	planID, changeID := fmt.Sprintf("p%d", n), fmt.Sprintf("c%d", n)
	now := h.clock.Now()
	err = h.ctlDB.Write(context.Background(), func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO core_plans (id, host_id, action, version, actor, input_hash, state_hash, diff_json,
			guards_json, confirmation, created_at, expires_at) VALUES (?, '', ?, 1, ?, '', '', '[]', '[]', '', ?, ?)`,
			planID, action, thPrincipal.Actor, storeTime(now), storeTime(now.Add(time.Hour))); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO core_changes (id, plan_id, host_id, action, version, class, compensated_by, actor, state,
			input_json, created_at, updated_at) VALUES (?, ?, '', ?, 1, 'composite', '', ?, ?, ?, ?, ?)`,
			changeID, planID, action, thPrincipal.Actor, state, string(raw), storeTime(now), storeTime(now))
		return err
	})
	if err != nil {
		h.t.Fatal(err)
	}
	return changeID
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

// Run records the vector, refuses one the production allowlist does not admit and a useradd or userdel of another
// user than the module's, and answers the next scripted result of the vector. It runs nothing; a useradd or
// userdel that answers exit 0 adds or removes the module's user in users.
func (h *dpHost) Run(_ context.Context, cmd core.Cmd) (core.CmdResult, error) {
	vector := append([]string{cmd.Path}, cmd.Args...)
	key := strings.Join(vector, " ")
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ran = append(h.ran, vector)
	if !thAllowed(cmd.Path, cmd.Args) {
		return core.CmdResult{}, fmt.Errorf("dpHost: %s is not in the production exec allowlist", key)
	}
	userCmd := cmd.Path == "/usr/sbin/useradd" || cmd.Path == "/usr/sbin/userdel"
	if userCmd && cmd.Args[len(cmd.Args)-1] != server.ServiceUser {
		return core.CmdResult{}, fmt.Errorf("dpHost: the derp module may not run %s", key)
	}
	var res core.CmdResult
	var err error
	if queue := h.scripts[key]; len(queue) > 0 {
		h.scripts[key] = queue[1:]
		res, err = queue[0].res, queue[0].err
	}
	if userCmd && err == nil && res.ExitCode == 0 {
		if cmd.Path == "/usr/sbin/useradd" {
			h.users[server.ServiceUser] = [2]int{thUserID, thUserID}
		} else {
			delete(h.users, server.ServiceUser)
		}
	}
	return res, err
}

// thOwned refuses what the module's manifest would not let its file provider touch as one of kinds.
func thOwned(module, path string, kinds ...string) error {
	if module != ID {
		return fmt.Errorf("dpHost: module %q is not %q", module, ID)
	}
	for _, op := range ownedPaths() {
		if op.Path == path && slices.Contains(kinds, op.Kind) {
			return nil
		}
	}
	return fmt.Errorf("dpHost: %s is not an owned path of kind %s", path, strings.Join(kinds, " or "))
}

func (h *dpHost) WriteFile(_ context.Context, module, path string, data []byte, _ fs.FileMode) error {
	if err := thOwned(module, path, core.OwnedFile, core.OwnedUnit); err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.files[path] = bytes.Clone(data)
	h.owned[path] = true
	return nil
}

func (h *dpHost) Remove(_ context.Context, module, path string) error {
	if err := thOwned(module, path, core.OwnedFile, core.OwnedUnit); err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.files, path)
	delete(h.owned, path)
	return nil
}

// Rename moves one binary path's bytes over another's, as the platform's atomic rename does.
func (h *dpHost) Rename(_ context.Context, module, from, to string) error {
	if err := thOwned(module, from, core.OwnedFile); err != nil {
		return err
	}
	if err := thOwned(module, to, core.OwnedFile); err != nil {
		return err
	}
	if from == to {
		return fmt.Errorf("dpHost: rename %s onto itself", from)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	b, ok := h.files[from]
	if !ok {
		return &fs.PathError{Op: "rename", Path: from, Err: fs.ErrNotExist}
	}
	h.files[to] = b
	h.owned[to] = true
	delete(h.files, from)
	delete(h.owned, from)
	return nil
}

func (h *dpHost) EnsureServiceDir(_ context.Context, module, dir string) error {
	if err := thOwned(module, dir, core.OwnedServiceDir); err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.owned[dir] = true
	return nil
}

// thServiceEntry splits a name inside the service directory into its subdirectory ("" or the certs
// subdirectory) and file name, refusing any other form.
func thServiceEntry(name string) (sub, file string, err error) {
	sub, file, nested := strings.Cut(name, "/")
	if !nested {
		sub, file = "", name
	}
	if (nested && sub != server.CertSubdir) || !thServiceName.MatchString(file) {
		return "", "", fmt.Errorf("dpHost: %q is not a name in %s", name, server.StateDir)
	}
	return sub, file, nil
}

func (h *dpHost) RenameIn(_ context.Context, module, dir, from, to string) error {
	if err := thOwned(module, dir, core.OwnedServiceDir); err != nil {
		return err
	}
	fromSub, _, err := thServiceEntry(from)
	if err != nil {
		return err
	}
	toSub, _, err := thServiceEntry(to)
	if err != nil {
		return err
	}
	if fromSub != toSub {
		return fmt.Errorf("dpHost: rename %s to %s in %s: not in the same directory", from, to, dir)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	b, ok := h.files[dir+"/"+from]
	if !ok {
		return &fs.PathError{Op: "rename", Path: dir + "/" + from, Err: fs.ErrNotExist}
	}
	h.files[dir+"/"+to] = b
	delete(h.files, dir+"/"+from)
	return nil
}

func (h *dpHost) RemoveIn(_ context.Context, module, dir, name string) error {
	if err := thOwned(module, dir, core.OwnedServiceDir); err != nil {
		return err
	}
	if _, _, err := thServiceEntry(name); err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.files, dir+"/"+name)
	return nil
}

func (h *dpHost) RemoveServiceDir(_ context.Context, module, dir string) error {
	if err := thOwned(module, dir, core.OwnedServiceDir); err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for path := range h.files {
		if strings.HasPrefix(path, dir+"/") {
			delete(h.files, path)
		}
	}
	delete(h.owned, dir)
	return nil
}

// Owns reports whether Files recorded path and has not removed it, or the ownership store records it.
func (h *dpHost) Owns(ctx context.Context, module, path string) (bool, error) {
	if module != ID {
		return false, fmt.Errorf("dpHost: module %q is not %q", module, ID)
	}
	h.mu.Lock()
	owned := h.owned[path]
	h.mu.Unlock()
	if owned {
		return true, nil
	}
	return platform.NewOwnership(h.db).Owns(ctx, ID, path)
}

func (h *dpHost) WriteManaged(context.Context, string, string, string, []byte, fs.FileMode, int, int) error {
	return errTHUnused
}

func (h *dpHost) RemoveManaged(context.Context, string, string, string) error { return errTHUnused }

func (h *dpHost) ReplaceBlock(context.Context, string, string, string, []byte) error {
	return errTHUnused
}

func (h *dpHost) RemoveBlock(context.Context, string, string, string) error { return errTHUnused }

func (h *dpHost) Restore(context.Context, string, core.RestoreEntry) error { return errTHUnused }

func (h *dpHost) ReadSysctl(context.Context, string, string) (string, error) {
	return "", errTHUnused
}

func (h *dpHost) WriteSysctl(context.Context, string, string, string) error { return errTHUnused }

// dpLocal is the fake local API. Its Status and WhoIs fields share a name with localAPI methods, so thLocal is the
// value that satisfies localAPI. A call answers Errs[<method name>] when that holds an error; otherwise it answers
// from the fields. A test that changes a field while an action runs does it in update.
type dpLocal struct {
	Status  *tslocal.Status
	WhoIs   map[netip.Addr]*tslocal.WhoIs // a missing address is tslocal's not-found error
	Regions map[string]*tslocal.DERPRegionReport
	Errs    map[string]error // by method name: "Status", "WhoIs", "DebugDERPRegion"

	mu sync.Mutex
}

// thLocal adapts a dpLocal to localAPI.
type thLocal struct{ *dpLocal }

// update runs f while holding the fake's lock.
func (l *dpLocal) update(f func()) {
	l.mu.Lock()
	defer l.mu.Unlock()
	f()
}

func (a thLocal) Status(context.Context) (*tslocal.Status, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.Errs["Status"]; err != nil {
		return nil, err
	}
	if a.dpLocal.Status == nil {
		return nil, fmt.Errorf("%w: the fake has no status", tslocal.ErrUnavailable)
	}
	s := *a.dpLocal.Status
	return &s, nil
}

func (a thLocal) WhoIs(_ context.Context, addr netip.Addr) (*tslocal.WhoIs, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.Errs["WhoIs"]; err != nil {
		return nil, err
	}
	w, ok := a.dpLocal.WhoIs[addr]
	if !ok {
		return nil, tslocal.ErrPeerNotFound
	}
	c := *w
	return &c, nil
}

func (a thLocal) DebugDERPRegion(_ context.Context, region string) (*tslocal.DERPRegionReport, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.Errs["DebugDERPRegion"]; err != nil {
		return nil, err
	}
	r, ok := a.Regions[region]
	if !ok {
		return nil, &tslocal.APIError{StatusCode: 404, Message: "no such region"}
	}
	c := *r
	return &c, nil
}

// dpUnits is the module's unit client: every call on derper's unit is recorded (State answers from States) and
// changes no state; every other unit and call is refused and recorded nowhere.
type dpUnits struct {
	States map[string]core.UnitState
	Calls  []string // "reload", "enable <unit>", "disable <unit>", "start <unit>", "stop <unit>", "restart <unit>"

	mu sync.Mutex
}

func (u *dpUnits) refuse(method, unit string) error {
	return fmt.Errorf("dpUnits: the derp module may not %s unit %s", method, unit)
}

// record appends call for units when every one is derper's unit.
func (u *dpUnits) record(method string, units ...string) error {
	joined := strings.Join(units, ",")
	if len(units) == 0 || slices.ContainsFunc(units, func(n string) bool { return n != server.UnitName }) {
		return u.refuse(method, joined)
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.Calls = append(u.Calls, strings.ToLower(method)+" "+joined)
	return nil
}

func (u *dpUnits) State(_ context.Context, unit string) (core.UnitState, error) {
	if unit != server.UnitName {
		return core.UnitState{}, u.refuse("State", unit)
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if st, ok := u.States[unit]; ok {
		return st, nil
	}
	return core.UnitState{LoadState: "not-found"}, nil
}

func (u *dpUnits) Reload(context.Context) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.Calls = append(u.Calls, "reload")
	return nil
}

func (u *dpUnits) Enable(_ context.Context, unitFiles ...string) error {
	return u.record("Enable", unitFiles...)
}

func (u *dpUnits) Disable(_ context.Context, unitFiles ...string) error {
	return u.record("Disable", unitFiles...)
}

func (u *dpUnits) Start(_ context.Context, unit string) error   { return u.record("Start", unit) }
func (u *dpUnits) Stop(_ context.Context, unit string) error    { return u.record("Stop", unit) }
func (u *dpUnits) Restart(_ context.Context, unit string) error { return u.record("Restart", unit) }

func (u *dpUnits) Kill(_ context.Context, unit string, _ syscall.Signal) error {
	return u.refuse("Kill", unit)
}

func (u *dpUnits) ResetFailed(_ context.Context, unit string) error {
	return u.refuse("ResetFailed", unit)
}

func (u *dpUnits) StartTransientService(_ context.Context, name string, _ core.TransientService) error {
	return u.refuse("StartTransientService", name)
}

func (u *dpUnits) StartTransientTimer(_ context.Context, name string, _ core.TransientTimer) error {
	return u.refuse("StartTransientTimer", name)
}

func (u *dpUnits) List(_ context.Context, pattern string) ([]core.UnitStatus, error) {
	return nil, u.refuse("List", pattern)
}

// dpAgent is a controller-side fake agent: its reads and resources answer from the maps, and every read is
// recorded in Calls.
type dpAgent struct {
	Reads     map[string]func(input json.RawMessage) (any, error) // by "<name>/v<version>"
	Resources map[string]any                                      // by resource name
	Calls     []string                                            // "<name>/v<version> <input JSON>" per Read

	mu sync.Mutex
}

// fake is the agent as a core.AgentClient.
func (a *dpAgent) fake() *coretest.FakeAgent {
	return &coretest.FakeAgent{
		ReadFunc: func(_ context.Context, _ core.Principal, ref core.ActionRef, input json.RawMessage) (json.RawMessage, error) {
			key := fmt.Sprintf("%s/v%d", ref.Name, ref.Version)
			a.mu.Lock()
			a.Calls = append(a.Calls, key+" "+string(input))
			read := a.Reads[key]
			a.mu.Unlock()
			if read == nil {
				return nil, fmt.Errorf("dpHost: no read %s", key)
			}
			v, err := read(input)
			if err != nil {
				return nil, err
			}
			return json.Marshal(v)
		},
		ResourceFunc: func(_ context.Context, _ core.Principal, name string) (json.RawMessage, error) {
			a.mu.Lock()
			v, ok := a.Resources[name]
			a.mu.Unlock()
			if !ok {
				return nil, fmt.Errorf("dpHost: no resource %s", name)
			}
			return json.Marshal(v)
		},
	}
}

// dpJournal is an in-memory core.EffectJournal.
type dpJournal struct {
	mu      sync.Mutex
	change  string
	list    []core.Effect
	secrets map[int][]byte
}

// effects returns a copy of the journaled effects.
func (j *dpJournal) effects() []core.Effect {
	j.mu.Lock()
	defer j.mu.Unlock()
	return slices.Clone(j.list)
}

func (j *dpJournal) Intend(_ context.Context, e core.Effect, secret []byte) (int, error) {
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
func (j *dpJournal) intended(seq int) (int, error) {
	if seq < 1 || seq > len(j.list) {
		return 0, fmt.Errorf("dpJournal: no effect %d", seq)
	}
	if st := j.list[seq-1].State; st != core.EffectIntended {
		return 0, fmt.Errorf("dpJournal: effect %d is %s, not intended", seq, st)
	}
	return seq - 1, nil
}

func (j *dpJournal) Made(_ context.Context, seq int, resource string, post core.StateDoc) error {
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

func (j *dpJournal) Skipped(_ context.Context, seq int) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	i, err := j.intended(seq)
	if err != nil {
		return err
	}
	j.list[i].State = core.EffectSkipped
	return nil
}

func (j *dpJournal) List(context.Context) ([]core.Effect, error) { return j.effects(), nil }

func (j *dpJournal) Secret(_ context.Context, seq int) ([]byte, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if seq < 1 || seq > len(j.list) {
		return nil, fmt.Errorf("dpJournal: no effect %d", seq)
	}
	return bytes.Clone(j.secrets[seq]), nil
}
