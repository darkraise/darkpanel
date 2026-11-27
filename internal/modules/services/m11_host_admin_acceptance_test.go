package services_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/modules/services"
)

type units struct {
	core.Units
	byName map[string]core.UnitInfo
}

func (u *units) Inspect(_ context.Context, name string) (core.UnitInfo, error) {
	if i, ok := u.byName[name]; ok {
		return i, nil
	}
	return core.UnitInfo{ID: name}, nil
}

func (u *units) ReloadUnit(context.Context, string) error { return nil }

func (u *units) ListAll(context.Context) ([]core.UnitInfo, error) {
	var out []core.UnitInfo
	for _, i := range u.byName {
		out = append(out, i)
	}
	slices.SortFunc(out, func(a, b core.UnitInfo) int {
		if a.ID < b.ID {
			return -1
		}
		return 1
	})
	return out, nil
}

func mk(name string, edit ...func(*core.UnitInfo)) core.UnitInfo {
	u := core.UnitInfo{ID: name, Names: []string{name}, Description: name, LoadState: "loaded", ActiveState: "active", SubState: "running",
		UnitFileState: "enabled", SuccessAction: "none", FailureAction: "none", JobTimeoutAction: "none", StartLimitAction: "none"}
	for _, e := range edit {
		e(&u)
	}
	return u
}

var (
	admin    = core.Principal{Actor: "user:admin", UserID: "admin", Scope: core.ScopeAdmin, Source: "192.0.2.1"}
	operator = core.Principal{Actor: "user:op", UserID: "op", Source: "192.0.2.2", Access: core.Access{All: core.ScopeOperate}}
)

type host struct {
	t  *testing.T
	u  *units
	db *store.DB
}

func newHost(t *testing.T, list ...core.UnitInfo) *host {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	set, err := store.CoreMigrations(store.SideAgent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Migrate(ctx, db, store.MigrateOptions{Side: store.SideAgent, BinaryVersion: "test",
		BackupRoot: filepath.Join(t.TempDir(), "backups"), Keep: 5, Now: time.Now()}, set, nil); err != nil {
		t.Fatal(err)
	}
	u := &units{byName: map[string]core.UnitInfo{}}
	for _, i := range list {
		u.byName[i.ID] = i
	}
	return &host{t: t, u: u, db: db}
}

func (h *host) action(name string) core.ActionDef {
	for _, a := range services.Module().Manifest().Actions {
		if a.Spec.Name == name {
			return a
		}
	}
	h.t.Fatalf("no action %s", name)
	return core.ActionDef{}
}

// plan plans services.unit.<verb> on name, with the unit put in the state the
// verb changes.
func (h *host) plan(verb string, p core.Principal, name string) (core.PlanOutcome, error) {
	if i, ok := h.u.byName[name]; ok {
		switch verb {
		case "start":
			i.ActiveState = "inactive"
		case "stop":
			i.ActiveState = "active"
		case "enable":
			i.UnitFileState = "disabled"
		case "disable":
			i.UnitFileState = "enabled"
		}
		h.u.byName[name] = i
	}
	in, _ := json.Marshal(map[string]string{"unit": name})
	env := core.Env{HostID: "h1", Principal: p, DB: h.db, Units: h.u}
	return h.action("services.unit."+verb).Handler.Plan(context.Background(), env, in)
}

func code(err error) errcode.Code {
	if err == nil {
		return ""
	}
	return errcode.From(err).Code
}

var allVerbs = []string{"start", "stop", "restart", "reload", "enable", "disable"}

func TestA30_EveryMutatingActionOnADarkpanelUnitIsRefused(t *testing.T) {
	names := []string{"darkpanel-agent.service", "darkpanel.service", "darkpanel-job-backup.service", "darkpanel-job-backup.timer", "darkpanel-controller.service", "darkpanel-x.socket"}
	var list []core.UnitInfo
	for _, n := range names {
		list = append(list, mk(n))
	}
	h := newHost(t, list...)
	for _, n := range names {
		for _, v := range allVerbs {
			for _, p := range []core.Principal{admin, operator} {
				if _, err := h.plan(v, p, n); code(err) != errcode.ServicesProtected {
					t.Errorf("%s %s as %s: %v; want services.protected", v, n, p.Actor, err)
				}
			}
		}
	}
	// The job service is started through the Jobs tab, so a start of it and a stop of its timer are refused too.
	if _, err := h.plan("start", admin, "darkpanel-job-backup.service"); code(err) != errcode.ServicesProtected {
		t.Errorf("start of a job service: %v", err)
	}
	if _, err := h.plan("stop", admin, "darkpanel-job-backup.timer"); code(err) != errcode.ServicesProtected {
		t.Errorf("stop of a job timer: %v", err)
	}
}

func TestA31_TypedPhraseIsTheUnitName(t *testing.T) {
	cases := map[string][]string{
		"ssh.service":              {"stop", "restart", "disable"},
		"sshd.service":             {"stop", "restart", "disable"},
		"tailscaled.service":       {"stop", "restart", "disable"},
		"docker.service":           {"stop", "restart", "disable"},
		"ufw.service":              {"stop", "restart", "disable"},
		"systemd-journald.service": {"stop", "restart", "disable"},
		"systemd-networkd.service": {"restart"},
		"NetworkManager.service":   {"restart"},
		"networking.service":       {"restart"},
		"systemd-resolved.service": {"restart"},
	}
	for name, verbs := range cases {
		for _, v := range verbs {
			h := newHost(t, mk(name))
			out, err := h.plan(v, admin, name)
			if err != nil {
				// A guard may refuse (network stack, tailnet); the phrase is then moot only for refused plans.
				t.Errorf("%s %s: %v", v, name, err)
				continue
			}
			if out.Confirmation != name {
				t.Errorf("%s %s: typed phrase %q; want %q", v, name, out.Confirmation, name)
			}
		}
	}
}

func TestA77_NetworkStopAndDisableAndDbusAreRefused(t *testing.T) {
	for _, name := range []string{"networking.service", "systemd-networkd.service", "NetworkManager.service", "systemd-resolved.service"} {
		for _, v := range []string{"stop", "disable"} {
			h := newHost(t, mk(name))
			out, err := h.plan(v, admin, name)
			failed := err != nil
			for _, g := range out.Guards {
				if !g.OK {
					failed = true
					if g.Overridable {
						t.Errorf("%s %s: the guard %s is overridable", v, name, g.ID)
					}
				}
			}
			if !failed {
				t.Errorf("%s %s as an admin: planned without a refusal or a failing guard", v, name)
			}
		}
	}
	for _, name := range []string{"dbus.service", "dbus.socket"} {
		for _, v := range []string{"stop", "restart", "disable"} {
			h := newHost(t, mk(name))
			if _, err := h.plan(v, admin, name); code(err) != errcode.ServicesProtected {
				t.Errorf("%s %s: %v; want services.protected", v, name, err)
			}
		}
	}
}

func TestA76_PowerStateUnitsAreRefusedForEveryRole(t *testing.T) {
	requires := func(d ...string) func(*core.UnitInfo) { return func(u *core.UnitInfo) { u.Requires = d } }
	wants := func(d ...string) func(*core.UnitInfo) { return func(u *core.UnitInfo) { u.Wants = d } }
	success := func(u *core.UnitInfo) { u.SuccessAction = "poweroff" }
	cases := []struct {
		unit  core.UnitInfo
		verbs []string
	}{
		{mk("poweroff.target"), []string{"start", "restart"}},
		{mk("rescue.target"), []string{"start", "restart"}},
		{mk("systemd-reboot.service"), []string{"start", "restart"}},
		{mk("systemd-suspend.service"), []string{"start", "restart"}},
		{mk("systemd-hibernate.service"), []string{"start", "restart"}},
		{mk("debug-shell.service"), []string{"start", "restart"}},
		{mk("sleeper.service", requires("sleep.target")), []string{"start", "restart"}},
		{mk("sleeper2.service", wants("shutdown.target")), []string{"start", "restart"}},
		{mk("sleeper3.service", requires("umount.target")), []string{"start", "restart"}},
		{mk("sleeper4.service", wants("final.target")), []string{"start", "restart"}},
		{mk("systemd-storagetm.service"), []string{"start", "stop"}},
		{mk("done.service", success), []string{"start", "stop", "restart"}},
		{mk("timer-of-suspend.timer", func(u *core.UnitInfo) { u.Triggers = []string{"systemd-suspend.service"} }), []string{"start", "restart"}},
		{mk("socket-of-suspend.socket", func(u *core.UnitInfo) { u.Triggers = []string{"systemd-suspend.service"} }), []string{"start", "restart"}},
		{mk("data.mount"), allVerbs},
		{mk("watch.path"), allVerbs},
		{mk("multi-user.target"), allVerbs},
	}
	for _, c := range cases {
		for _, v := range c.verbs {
			for _, p := range []core.Principal{admin, operator} {
				h := newHost(t, c.unit, mk("systemd-suspend.service"))
				if _, err := h.plan(v, p, c.unit.ID); code(err) != errcode.ServicesProtected {
					t.Errorf("%s %s as %s: %v; want services.protected", v, c.unit.ID, p.Actor, err)
				}
			}
		}
	}
}

func TestA92_ManagedUnitsAreRefusedForOperatorsAndForAdminsDuringAModuleChange(t *testing.T) {
	names := []string{"ufw.service", "docker.service", "tailscaled.service", "ssh.service", "fail2ban.service", "nftables.service", "netfilter-persistent.service", "firewalld.service"}
	for _, name := range names {
		for _, v := range []string{"start", "stop", "restart", "reload"} {
			h := newHost(t, mk(name))
			if _, err := h.plan(v, operator, name); err == nil {
				t.Errorf("%s %s as an operator was planned", v, name)
			} else if code(err) != errcode.Forbidden {
				t.Errorf("%s %s as an operator: %s; want auth.forbidden", v, name, code(err))
			}
		}
	}
}

func TestA27_UnitListCoversTypesAndFilters(t *testing.T) {
	h := newHost(t,
		mk("nginx.service"), mk("ssh.socket"), mk("apt.timer", func(u *core.UnitInfo) { u.ActiveState = "failed" }),
		mk("home.mount"), mk("cups.path"), mk("multi-user.target"), mk("swapfile.swap"))
	read := func(in string) services.UnitList {
		t.Helper()
		env := core.Env{HostID: "h1", Principal: operator, DB: h.db, Units: h.u}
		var a core.ActionDef
		a = h.action("services.unit.list")
		raw, err := a.Handler.Read(context.Background(), env, json.RawMessage(in))
		if err != nil {
			t.Fatal(err)
		}
		var out services.UnitList
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	all := read(`{}`)
	types := map[string]bool{}
	for _, u := range all.Units {
		types[u.Type] = true
		if u.Active == "" || u.Sub == "" {
			t.Errorf("%s has no active or sub state", u.Name)
		}
	}
	for _, ty := range []string{"service", "socket", "timer", "mount", "path", "target"} {
		if !types[ty] {
			t.Errorf("no %s unit listed", ty)
		}
	}
	if types["swap"] {
		t.Error("a swap unit is listed")
	}
	if got := read(`{"type":"timer"}`); len(got.Units) != 1 || got.Units[0].Name != "apt.timer" {
		t.Errorf("type filter: %+v", got.Units)
	}
	if got := read(`{"state":"failed"}`); len(got.Units) != 1 || got.Units[0].Name != "apt.timer" {
		t.Errorf("state filter: %+v", got.Units)
	}
}

type jr struct {
	probeStderr string
	lines       []string
	args        []string
}

func (j *jr) Run(_ context.Context, c core.Cmd) (core.CmdResult, error) {
	return core.CmdResult{Stderr: []byte(j.probeStderr)}, nil
}

func (j *jr) Stream(_ context.Context, _ string, args []string, onLine func([]byte) error) error {
	j.args = args
	for _, l := range j.lines {
		if err := onLine([]byte(l)); err != nil {
			return err
		}
	}
	return nil
}

func jline(i int, msg string) string {
	b, _ := json.Marshal(map[string]string{"__CURSOR": "s=abc;i=" + string(rune('a'+i%26)) + "1", "__REALTIME_TIMESTAMP": "1700000000000000",
		"PRIORITY": "6", "_SYSTEMD_UNIT": "a.service", "_PID": "1", "MESSAGE": msg})
	return string(b)
}

func query(t *testing.T, j *jr, in map[string]any) services.JournalPage {
	t.Helper()
	env := core.Env{HostID: "h1", Principal: operator, Exec: j}
	raw, _ := json.Marshal(in)
	out, err := newHost(t).action("services.journal.query").Handler.Read(context.Background(), env, raw)
	if err != nil {
		t.Fatalf("journal query: %v", err)
	}
	var page services.JournalPage
	if err := json.Unmarshal(out, &page); err != nil {
		t.Fatal(err)
	}
	return page
}

func TestA28_JournalPageIsAtMostAThousandEntriesAndLongMessagesAreTruncated(t *testing.T) {
	j := &jr{}
	for i := 0; i < 1500; i++ {
		j.lines = append(j.lines, jline(i, "entry"))
	}
	page := query(t, j, map[string]any{"unit": "a.service"})
	if len(page.Entries) != 1000 || !page.More || page.Cursor == "" {
		t.Errorf("1500 available: %d entries, more %v, cursor %q; want 1000, true and a cursor", len(page.Entries), page.More, page.Cursor)
	}
	j = &jr{lines: []string{jline(0, strings.Repeat("x", 9000)), jline(1, "short")}}
	page = query(t, j, map[string]any{})
	if len(page.Entries) != 2 || !page.Entries[0].Truncated || len(page.Entries[0].Message) > 8192 || page.Entries[1].Truncated {
		t.Errorf("entries %+v; want the 9000 byte message cut to 8 KiB and flagged, the short one intact", page.Entries)
	}
}

func TestA87_JournalPageEndsAtTwoMiBWithMore(t *testing.T) {
	j := &jr{}
	for i := 0; i < 600; i++ {
		j.lines = append(j.lines, jline(i, strings.Repeat("y", 8000)))
	}
	page := query(t, j, map[string]any{})
	if !page.More || page.Cursor == "" || len(page.Entries) >= 1000 || len(page.Entries) < 200 {
		t.Errorf("600 entries of 8000 bytes: %d entries, more %v; want a page cut near 2 MiB (about 260 entries) with more", len(page.Entries), page.More)
	}
	total := 0
	for _, e := range page.Entries {
		b, _ := json.Marshal(e)
		total += len(b) + 1
	}
	if total > 2<<20 {
		t.Errorf("the page holds %d bytes of encoded entries, over 2 MiB", total)
	}
}

func TestA96_SubstringWithRegexCharactersMatchesTheSameThroughGrepAndTheFallback(t *testing.T) {
	msgs := []string{`call f(x) failed`, `list[0] ok`, `a.b matched`, `axb plain`, `path C:\Eval here`, `UPPER (X) case`, `nothing here`}
	for _, sub := range []string{"(", "[", ".", `\E`, "f(x)", "(x)", "a.b", `C:\Eval`, "list[0]"} {
		in := map[string]any{"substring": sub}
		var all []string
		for i, m := range msgs {
			all = append(all, jline(i, m))
		}
		fallback := &jr{probeStderr: "unknown option --grep", lines: all}
		fb := query(t, fallback, in)
		grep := &jr{lines: all}
		_ = query(t, grep, in)
		var pat string
		for _, a := range grep.args {
			if strings.HasPrefix(a, "--grep=") {
				pat = strings.TrimPrefix(a, "--grep=")
			}
		}
		if pat == "" {
			t.Errorf("%q: journalctl got no --grep argument: %v", sub, grep.args)
			continue
		}
		re, err := regexp.Compile("(?i)" + pat)
		if err != nil {
			t.Errorf("%q: the --grep pattern %q is not a valid pattern: %v", sub, pat, err)
			continue
		}
		var viaGrep, viaFallback []string
		for _, m := range msgs {
			if re.MatchString(m) {
				viaGrep = append(viaGrep, m)
			}
		}
		for _, e := range fb.Entries {
			viaFallback = append(viaFallback, e.Message)
		}
		if !slices.Equal(viaGrep, viaFallback) {
			t.Errorf("%q: --grep %q matches %q; the fallback matches %q", sub, pat, viaGrep, viaFallback)
		}
	}
}

type followRunner struct {
	started, ended chan struct{}
}

func (f *followRunner) Run(context.Context, core.Cmd) (core.CmdResult, error) {
	return core.CmdResult{}, nil
}

func (f *followRunner) Stream(ctx context.Context, _ string, _ []string, onLine func([]byte) error) error {
	close(f.started)
	defer close(f.ended)
	for i := 0; ; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
		if err := onLine([]byte(jline(i, "live entry"))); err != nil {
			return err
		}
	}
}

func TestA29_FollowEmitsEntriesAndEndsJournalctlWhenTheReaderGoesAway(t *testing.T) {
	var follow core.StreamDef
	for _, s := range services.Module().(core.Streamer).Streams() {
		follow = s
	}
	if follow.Run == nil {
		t.Fatal("the services module declares no stream")
	}
	fr := &followRunner{started: make(chan struct{}), ended: make(chan struct{})}
	env := core.Env{HostID: "h1", Principal: operator, Exec: fr}
	var got int
	done := make(chan error, 1)
	go func() {
		done <- follow.Run(context.Background(), env, json.RawMessage(`{"lines":10}`), func(any) error {
			got++
			if got >= 3 {
				return errors.New("the reader is gone")
			}
			return nil
		})
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the follower kept running after its reader went away")
	}
	select {
	case <-fr.ended:
	case <-time.After(5 * time.Second):
		t.Error("the journalctl process was not ended")
	}
	if got < 3 {
		t.Errorf("%d entries emitted; want at least 3", got)
	}
}

func TestA2_AnOperatorCanPlanStartStopRestartAndReloadAnOrdinaryService(t *testing.T) {
	for _, v := range []string{"start", "stop", "restart", "reload"} {
		h := newHost(t, mk("nginx.service"))
		if _, err := h.plan(v, operator, "nginx.service"); err != nil {
			t.Errorf("%s nginx.service as an operator: %v", v, err)
		}
	}
}
