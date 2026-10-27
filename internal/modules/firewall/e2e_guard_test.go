package firewall

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/modules/firewall/conf"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/modules/firewall/guard"
	"github.com/darkraise/darkpanel/internal/modules/firewall/loaded"
	"github.com/darkraise/darkpanel/internal/modules/firewall/predict"
	"github.com/darkraise/darkpanel/internal/platform/netstat"
)

// e2eGolden is the golden capture the end-to-end tests start from (part
// F2's, ubuntu:24.04, UFW 0.36.2).
const e2eGolden = "testdata/golden/ubuntu-24.04"

// e2eSSHRule is the Darkpanel rule of the golden "rules" scenario that
// allows SSH (tcp 22, label "ssh").
const e2eSSHRule = "01J00000000000000000000000"

// e2eHost is a whole host for the end-to-end tests: UFW's files, a golden
// capture's, under a temporary root, read by the real conf.Read; the
// kernel's tables, first read by the real loaded.Reader from the capture's
// save output and afterwards changed only by ufw runs, as part P's
// predictor models UFW; the live sysctl values; and a network on which
// sshd holds a session from 203.0.113.7 to 192.0.2.1:22 on eth0. It
// implements core.Runner and core.FileWriter.
type e2eHost struct {
	t      *testing.T
	root   string
	db     *store.DB
	golden string // the capture the kernel's tables are read from
	loaded fwstate.Loaded
	sysctl map[string]string
	ran    []string // the ufw argument vectors run, in order
	clock  *coretest.FakeClock
}

// newE2EHost builds a host from the golden scenario files (UFW's files and
// sysctl values) whose kernel holds the tables of scenario tables.
func newE2EHost(t *testing.T, files, tables string) *e2eHost {
	t.Helper()
	h := &e2eHost{t: t, root: t.TempDir(), db: fwTestDB(t), golden: filepath.Join(e2eGolden, tables), sysctl: map[string]string{},
		clock: coretest.NewFakeClock(time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC))}
	src := filepath.Join(e2eGolden, files, "files")
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(p) //nolint:gosec // G304: a golden capture under testdata
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		h.write("/"+filepath.ToSlash(rel), data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(e2eGolden, files, "sysctl.txt")) //nolint:gosec // G304: a golden capture under testdata
	if err != nil {
		t.Fatal(err)
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		if k, v, ok := strings.Cut(sc.Text(), "="); ok && !strings.Contains(k, "/lo/") {
			h.sysctl[k] = v
		}
	}
	r := loaded.Reader{
		Interfaces:     func() ([]netstat.Interface, error) { return e2eNet{}.Interfaces() },
		NFTablesLoaded: func() bool { return false },
	}
	st, err := r.Read(context.Background(), core.Env{Exec: h, Files: h})
	if err != nil {
		t.Fatalf("read the golden loaded state: %v", err)
	}
	if len(st.Unsupported) > 0 {
		t.Fatalf("golden loaded state unsupported: %v", st.Unsupported)
	}
	st.Sysctl = nil
	h.loaded = st
	return h
}

func (h *e2eHost) write(p string, data []byte) {
	h.t.Helper()
	full := filepath.Join(h.root, p)
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(full, data, 0o644); err != nil { //nolint:gosec // G306: UFW's files are world-readable
		h.t.Fatal(err)
	}
}

func (h *e2eHost) config() fwstate.Config {
	h.t.Helper()
	cfg, err := conf.Read(h.root)
	if err != nil {
		h.t.Fatal(err)
	}
	return cfg
}

// Run answers the loaded-state reader from the golden capture and runs ufw
// as the predictor models it: the command's edit of the files, then, for a
// transition, its prediction of the kernel's tables and sysctl values.
func (h *e2eHost) Run(_ context.Context, cmd core.Cmd) (core.CmdResult, error) {
	switch {
	case strings.HasSuffix(cmd.Path, "tables") && len(cmd.Args) == 1 && cmd.Args[0] == "--version":
		v, err := os.ReadFile(filepath.Join(h.golden, "version.txt"))
		if err != nil {
			return core.CmdResult{}, err
		}
		line, _, _ := strings.Cut(string(v), "\n")
		return core.CmdResult{Stdout: []byte(strings.Replace(line, "iptables", filepath.Base(cmd.Path), 1) + "\n")}, nil
	case strings.HasSuffix(cmd.Path, "-nft-save") && len(cmd.Args) == 2 && cmd.Args[0] == "-t":
		fam := "v4"
		if strings.Contains(cmd.Path, "ip6tables") {
			fam = "v6"
		}
		out, err := os.ReadFile(filepath.Join(h.golden, "save", "nft-"+fam+"-"+cmd.Args[1]+".txt")) //nolint:gosec // G304: a golden capture under testdata
		return core.CmdResult{Stdout: out}, err
	case cmd.Path == ufwPath:
		return h.ufw(cmd.Args), nil
	}
	return core.CmdResult{}, fmt.Errorf("e2e: %s %v is not modelled: %w", cmd.Path, cmd.Args, fs.ErrNotExist)
}

func (h *e2eHost) ufw(args []string) core.CmdResult {
	h.ran = append(h.ran, strings.Join(args, " "))
	cfg := h.config()
	next, err := predict.Edit(cfg, args)
	if err != nil {
		return core.CmdResult{ExitCode: 1, Stderr: []byte("ERROR: " + err.Error() + "\n")}
	}
	for p, e := range next.Files {
		if e.Present && !bytes.Equal(e.Data, cfg.Files[p].Data) {
			h.write(p, e.Data)
		}
	}
	tr := fwstate.TransNone
	switch strings.Join(args, " ") {
	case "reload":
		if cfg.Enabled {
			tr = fwstate.TransReload
		}
	case "--force enable":
		if chainOf(h.loaded.V4, "filter", "ufw-user-input") == nil {
			tr = fwstate.TransEnable
		}
	case "disable":
		tr = fwstate.TransDisable
	}
	if tr == fwstate.TransNone {
		return core.CmdResult{}
	}
	pred, err := predict.Predict(cfg, h.loaded, tr)
	if err != nil {
		return core.CmdResult{ExitCode: 1, Stderr: []byte("ERROR: " + err.Error() + "\n")}
	}
	sysctl, err := loaded.PredictSysctl(next, h.sysctl, tr)
	if err != nil {
		return core.CmdResult{ExitCode: 1, Stderr: []byte("ERROR: " + err.Error() + "\n")}
	}
	pred.Sysctl, h.loaded, h.sysctl = nil, pred, sysctl
	return core.CmdResult{}
}

var errE2EUnused = errors.New("e2e: not used by the firewall")

func (h *e2eHost) WriteFile(context.Context, string, string, []byte, fs.FileMode) error {
	return errE2EUnused
}
func (h *e2eHost) ReplaceBlock(context.Context, string, string, string, []byte) error {
	return errE2EUnused
}
func (h *e2eHost) RemoveBlock(context.Context, string, string, string) error { return errE2EUnused }
func (h *e2eHost) Remove(context.Context, string, string) error              { return errE2EUnused }
func (h *e2eHost) WriteManaged(context.Context, string, string, string, []byte, fs.FileMode, int, int) error {
	return errE2EUnused
}
func (h *e2eHost) RemoveManaged(context.Context, string, string, string) error { return errE2EUnused }
func (h *e2eHost) Rename(context.Context, string, string, string) error        { return errE2EUnused }
func (h *e2eHost) EnsureServiceDir(context.Context, string, string) error      { return errE2EUnused }
func (h *e2eHost) RenameIn(context.Context, string, string, string, string) error {
	return errE2EUnused
}
func (h *e2eHost) RemoveIn(context.Context, string, string, string) error { return errE2EUnused }
func (h *e2eHost) RemoveServiceDir(context.Context, string, string) error { return errE2EUnused }
func (h *e2eHost) Owns(context.Context, string, string) (bool, error)     { return false, errE2EUnused }

// Restore writes or removes one of UFW's files under the root.
func (h *e2eHost) Restore(_ context.Context, _ string, e core.RestoreEntry) error {
	if !e.Present {
		err := os.Remove(filepath.Join(h.root, e.Path))
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	h.write(e.Path, e.Data)
	return nil
}

func (h *e2eHost) ReadSysctl(_ context.Context, _, key string) (string, error) {
	v, ok := h.sysctl[key]
	if !ok {
		return "", fmt.Errorf("e2e: sysctl %s: %w", key, fs.ErrNotExist)
	}
	return v, nil
}

func (h *e2eHost) WriteSysctl(_ context.Context, _, key, value string) error {
	if _, ok := h.sysctl[key]; !ok {
		return fmt.Errorf("e2e: sysctl %s: %w", key, fs.ErrNotExist)
	}
	h.sysctl[key] = value
	return nil
}

// e2eNet is the guard's view of the host's network: sshd listens on 22 and
// holds a session from 203.0.113.7; eth0 is 192.0.2.1/24 and
// 2001:db8::1/64, and every other address routes out of it.
type e2eNet struct{}

var e2eLocal = map[netip.Addr]bool{
	netip.MustParseAddr("127.0.0.1"): true, netip.MustParseAddr("::1"): true,
	netip.MustParseAddr("192.0.2.1"): true, netip.MustParseAddr("2001:db8::1"): true,
}

func (e2eNet) Sockets() ([]netstat.Socket, error) {
	sshd := []netstat.Process{{PID: 700, Comm: "sshd"}}
	return []netstat.Socket{
		{Proto: netstat.ProtoTCP, Family: netstat.IPv4, State: netstat.StateListen, Local: netip.MustParseAddrPort("0.0.0.0:22"), Processes: sshd},
		{Proto: netstat.ProtoTCP, Family: netstat.IPv4, State: netstat.StateEstablished, Local: netip.MustParseAddrPort("192.0.2.1:22"),
			Remote: netip.MustParseAddrPort("203.0.113.7:50000"), Processes: sshd},
	}, nil
}

func (e2eNet) Interfaces() ([]netstat.Interface, error) {
	return []netstat.Interface{
		{Index: 1, Name: "lo", Up: true, Running: true, Loopback: true,
			Addrs: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/8"), netip.MustParsePrefix("::1/128")}},
		{Index: 2, Name: "eth0", Up: true, Running: true,
			Addrs: []netip.Prefix{netip.MustParsePrefix("192.0.2.1/24"), netip.MustParsePrefix("2001:db8::1/64")}},
	}, nil
}

func (e2eNet) RouteGet(q netstat.RouteQuery) (netstat.Route, error) {
	if e2eLocal[q.Dst] {
		return netstat.Route{Type: "local", Dst: netip.PrefixFrom(q.Dst, q.Dst.BitLen()), OutIface: "lo"}, nil
	}
	src := netip.MustParseAddr("192.0.2.1")
	if q.Dst.Is6() {
		src = netip.MustParseAddr("2001:db8::1")
	}
	return netstat.Route{Type: "unicast", Dst: netip.PrefixFrom(q.Dst, q.Dst.BitLen()), PrefSrc: src, OutIface: "eth0"}, nil
}

func (e2eNet) PID() int { return 4242 }

// e2eUnits says no controller runs on the host.
type e2eUnits struct{ core.Units }

func (e2eUnits) State(context.Context, string) (core.UnitState, error) {
	return core.UnitState{LoadState: "not-found"}, nil
}

// fw is the firewall over the host: the real reader of the configured
// state, predictor, comparison, sysctl prediction, evaluator and guard
// (with e2eNet as its host); the kernel's tables are the host's.
func (h *e2eHost) fw() fw {
	check := guard.Checker{Host: e2eNet{}}.Check
	return fw{
		d: deps{
			root: h.root, readConfig: conf.Read, predict: predict.Predict, edit: predict.Edit, files: predict.Files,
			compare: loaded.Compare, sysctl: loaded.PredictSysctl, lockout: lockoutVia(check),
			readLoaded: func(context.Context, core.Env) (fwstate.Loaded, error) {
				st := h.loaded
				st.Sysctl = maps.Clone(h.sysctl)
				return st, nil
			},
		},
		ipv6Refusal: predict.IPv6Refusal,
		check:       check,
	}
}

// env is a plan's Env on the host.
func (h *e2eHost) env() core.Env {
	return core.Env{HostID: "h1", Principal: core.Principal{Actor: "user:u1", Scope: core.ScopeAdmin}, DB: h.db, Exec: h, Files: h,
		Clock: h.clock, Units: e2eUnits{}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

// e2ePlan plans action name of the host's module with input raw.
func e2ePlan(t *testing.T, h *e2eHost, name, raw string) (core.PlanOutcome, error) {
	t.Helper()
	for _, def := range h.fw().actions() {
		if def.Spec.Name == name {
			return def.Handler.Plan(context.Background(), h.env(), json.RawMessage(raw))
		}
	}
	t.Fatalf("no action %s", name)
	return core.PlanOutcome{}, nil
}

// TestE2EGuardCutsSSH: on UFW's golden "rules" state, read by the real
// reader and in sync, deleting the rule that allows SSH fails the lockout
// guard naming the session it would cut, evaluated by the real evaluator;
// overridden it passes; adding a rule cuts nothing.
func TestE2EGuardCutsSSH(t *testing.T) {
	h := newE2EHost(t, "rules", "rules")
	st, err := h.fw().status(context.Background(), h.env())
	if err != nil || !st.Supported || !st.InSync || !st.Active {
		t.Fatalf("status %+v (%v), want supported, active and in sync", st, err)
	}
	out, err := e2ePlan(t, h, "firewall.rule.delete", `{"id":"`+e2eSSHRule+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	g := out.Guards[0]
	if g.OK || g.Overridden || !strings.Contains(g.Message, "ssh from 203.0.113.7 to 192.0.2.1:22 on eth0: drop") {
		t.Errorf("guard %+v, want the SSH session cut", g)
	}
	out, err = e2ePlan(t, h, "firewall.rule.delete", `{"id":"`+e2eSSHRule+`","overrides":["firewall.lockout"]}`)
	if err != nil || !out.Guards[0].OK || !out.Guards[0].Overridden {
		t.Errorf("overridden plan %+v (%v), want the guard overridden", out.Guards, err)
	}
	out, err = e2ePlan(t, h, "firewall.rule.add",
		`{"rule":{"action":"allow","direction":"in","proto":"tcp","from":"any","to":"any","toPort":"8080","family":"both"}}`)
	if err != nil || !out.Guards[0].OK || out.Guards[0].Overridden || !strings.Contains(out.Guards[0].Message, "every protected flow passes") {
		t.Errorf("plan %+v (%v), want the guard passing", out.Guards, err)
	}
	out, err = e2ePlan(t, h, "firewall.defaults.set", `{"incoming":"REJECT"}`)
	if err != nil || !out.Guards[0].OK {
		t.Errorf("defaults plan %+v (%v), want the guard passing while SSH stays allowed", out.Guards, err)
	}
	if len(h.ran) != 0 {
		t.Errorf("plans ran ufw %q", h.ran)
	}
}

// TestE2EUnsupported: an unsupported configuration is refused by every
// mutating action, overrides or not.
func TestE2EUnsupported(t *testing.T) {
	h := newE2EHost(t, "rules", "rules")
	cfg := h.config()
	h.write("/etc/default/ufw", bytes.Replace(cfg.Files["/etc/default/ufw"].Data, []byte("MANAGE_BUILTINS=no"), []byte("MANAGE_BUILTINS=yes"), 1))
	for _, c := range [][2]string{
		{"firewall.state.disable", `{"overrides":["firewall.lockout"]}`},
		{"firewall.rule.delete", `{"id":"` + e2eSSHRule + `"}`},
		{"firewall.state.repair", `{}`},
	} {
		_, err := e2ePlan(t, h, c[0], c[1])
		if e := errcode.From(err); e.Code != errcode.FirewallUnsupportedConfiguration || !strings.Contains(e.Detail, "MANAGE_BUILTINS") {
			t.Errorf("%s = %v, want the unsupported configuration", c[0], err)
		}
	}
}

// TestE2EOutOfSync: with UFW's files of the "rules" scenario and the
// kernel's tables of the "enabled" one, the host is out of sync in UFW's
// chains: every action is refused by a guard that overrides cannot pass,
// except firewall.state.repair, whose guard runs on its target.
func TestE2EOutOfSync(t *testing.T) {
	h := newE2EHost(t, "rules", "enabled")
	st, err := h.fw().status(context.Background(), h.env())
	if err != nil || st.InSync || len(st.OutOfSync) == 0 {
		t.Fatalf("status %+v (%v), want out of sync", st, err)
	}
	for _, c := range [][2]string{
		{"firewall.rule.add", `{"rule":{"action":"allow","direction":"in","proto":"tcp","from":"any","to":"any","toPort":"8080","family":"both"},"overrides":["firewall.lockout"]}`},
		{"firewall.logging.set", `{"level":"medium","overrides":["firewall.lockout"]}`},
	} {
		out, err := e2ePlan(t, h, c[0], c[1])
		if err != nil || out.Guards[0].OK || !strings.Contains(out.Guards[0].Message, "out of sync") {
			t.Errorf("%s plan %+v (%v), want the out-of-sync refusal", c[0], out.Guards, err)
		}
	}
	out, err := e2ePlan(t, h, "firewall.state.repair", `{}`)
	if err != nil || !out.Guards[0].OK || out.Guards[0].Overridden {
		t.Fatalf("repair plan %+v (%v), want its guard run and passing", out, err)
	}
	found := false
	for _, l := range out.Diff {
		found = found || strings.HasPrefix(l.Text, "out of sync: v4 filter/ufw-user-input")
	}
	if !found {
		t.Errorf("repair diff %+v, want the differing user chain", out.Diff)
	}
}
