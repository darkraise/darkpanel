//go:build harness

package harness

import (
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/modules"
	"github.com/darkraise/darkpanel/internal/paths"
)

func TestFpUnitParsePID(t *testing.T) {
	for _, c := range []struct {
		out     string
		want    int
		wantErr string // "" accepts
	}{
		{"4321\n", 4321, ""},
		{"  17 \n", 17, ""},
		{"0\n", 0, "not a running process"},
		{"", 0, "not a running process"},
		{"-3\n", 0, "not a running process"},
		{"4321 4322\n", 0, "not a running process"},
		{"n/a\n", 0, "not a running process"},
	} {
		got, err := fpParsePID(c.out)
		if (c.wantErr == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), c.wantErr)) || got != c.want {
			t.Errorf("fpParsePID(%q) = %d, %v; want %d, %q", c.out, got, err, c.want, c.wantErr)
		}
	}
}

// The awk command prints the second field of the VmRSS line: a number of kB, which /proc counts in units of 1024
// bytes. The budgets are decimal megabytes in bytes, so the parser returns bytes.
func TestFpUnitParseRSS(t *testing.T) {
	for _, c := range []struct {
		out     string
		want    int64
		wantErr string
	}{
		{"12345\n", 12345 * 1024, ""},
		{"1\n", 1024, ""},
		{" 39062 \n", 39062 * 1024, ""},
		{"", 0, "not a positive number"},
		{"0\n", 0, "not a positive number"},
		{"-4\n", 0, "not a positive number"},
		{"12 kB\n", 0, "not a positive number"},
		{"1.5\n", 0, "not a positive number"},
		{"awk: cannot open /proc/9/status\n", 0, "not a positive number"},
	} {
		got, err := fpParseRSS(c.out)
		if (c.wantErr == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), c.wantErr)) || got != c.want {
			t.Errorf("fpParseRSS(%q) = %d, %v; want %d, %q", c.out, got, err, c.want, c.wantErr)
		}
	}
	// 50 000 000 bytes is 48828.125 KiB: a sample of 48829 KiB is over the agent budget, one of 48828 KiB is not.
	over, _ := fpParseRSS("48829\n")
	under, _ := fpParseRSS("48828\n")
	if over <= fpAgentMax || under > fpAgentMax {
		t.Errorf("48829 KiB = %d and 48828 KiB = %d bytes, want one over and one within %d", over, under, int64(fpAgentMax))
	}
}

func TestFpUnitBudget(t *testing.T) {
	full := func(n int, v int64) []int64 { return slices.Repeat([]int64{v}, n) }
	within := full(60, fpAgentMax)
	maxLast := append(full(59, 10_000_000), fpAgentMax+1)
	for _, c := range []struct {
		name    string
		samples []int64
		limit   int64
		want    int
		wantErr string // "" accepts
	}{
		{"every sample at the limit", within, fpAgentMax, 60, ""},
		{"nine tenths of the samples", full(54, 1), fpAgentMax, 60, ""},
		{"the maximum is the last sample", maxLast, fpAgentMax, 60, "50000001 bytes, over the 50000000"},
		{"the maximum is the first sample", append([]int64{fpControllerMax + 1}, full(59, 1)...), fpControllerMax, 60, "150000001 bytes, over the 150000000"},
		{"no samples", nil, fpAgentMax, 60, "0 RSS samples"},
		{"too few samples", full(53, 1), fpAgentMax, 60, "53 RSS samples, want at least 54 of 60"},
	} {
		err := fpCheckBudget("agent", c.samples, c.limit, c.want)
		if (c.wantErr == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), c.wantErr)) {
			t.Errorf("%s: fpCheckBudget = %v, want %q", c.name, err, c.wantErr)
		}
	}
}

func TestFpUnitSchedule(t *testing.T) {
	if got := fpSampleCount(fpWindow, fpSampleEvery); got != 60 {
		t.Errorf("fpSampleCount = %d, want 60 samples of 10 s in 10 minutes", got)
	}
	evs := fpSchedule(fpWindow, fpSampleEvery, fpHostsEvery)
	var samples, hosts int
	for i, ev := range evs {
		switch ev.Kind {
		case fpSampleEvent:
			samples++
		case fpHostsEvent:
			hosts++
		}
		if ev.At <= 0 || ev.At > fpWindow {
			t.Errorf("event %d at %v is outside (0, %v]", i, ev.At, fpWindow)
		}
		if i > 0 && (ev.At < evs[i-1].At || (ev.At == evs[i-1].At && ev.Kind < evs[i-1].Kind)) {
			t.Errorf("event %d (%v %d) comes before event %d (%v %d)", i, ev.At, ev.Kind, i-1, evs[i-1].At, evs[i-1].Kind)
		}
	}
	if samples != 60 || hosts != 40 {
		t.Errorf("%d sample and %d host list events, want 60 and 40", samples, hosts)
	}
	if evs[0].At != fpSampleEvery || evs[len(evs)-1].At != fpWindow {
		t.Errorf("the plan runs from %v to %v, want %v to %v", evs[0].At, evs[len(evs)-1].At, fpSampleEvery, fpWindow)
	}
	// At 30 s both are due; the sample comes first, so the host list does not delay it.
	if i := slices.IndexFunc(evs, func(e fpEvent) bool { return e.At == 30*time.Second }); i < 0 || i+1 >= len(evs) {
		t.Errorf("no sample and host list pair at 30 s in %d events", len(evs))
	} else if evs[i].Kind != fpSampleEvent || evs[i+1].Kind != fpHostsEvent {
		t.Errorf("events at 30 s: %+v %+v, want the sample first", evs[i], evs[i+1])
	}
	if got := fpSchedule(time.Minute, 0, fpHostsEvery); len(got) != 4 {
		t.Errorf("a zero sample interval left %d events, want only the 4 host lists", len(got))
	}
	if got := fpSampleCount(time.Second, 0); got != 0 {
		t.Errorf("fpSampleCount with a zero interval = %d, want 0", got)
	}
}

func TestFpUnitExtraAgentCommands(t *testing.T) {
	if fpExtraAgents != 9 {
		t.Fatalf("fpExtraAgents = %d, want 9: the controller manages eleven hosts, one more than ten", fpExtraAgents)
	}
	if err := fpCheckLayout(); err != nil {
		t.Fatal(err)
	}
	units := map[string]bool{}
	for n := 1; n <= fpExtraAgents; n++ {
		dir, unit := fpStateDir(n), fpUnit(n)
		if units[unit] {
			t.Errorf("unit %s is used twice", unit)
		}
		units[unit] = true
		if !strings.HasPrefix(dir, fpStateRoot+"/") || strings.Count(strings.TrimPrefix(dir, fpStateRoot+"/"), "/") != 0 {
			t.Errorf("state dir %q is not directly under %s", dir, fpStateRoot)
		}
		// A Unix socket path holds 107 bytes; the longest path under a state directory is the agent's socket.
		if sock := paths.Under(dir).AgentSocket; len(sock) > fpSocketMax {
			t.Errorf("agent socket %s is %d bytes, over %d", sock, len(sock), fpSocketMax)
		}
		run := fpRunCmd(n)
		for _, want := range []string{"sudo systemd-run", "--unit=" + unit, fpBinaryPath + " agent run", "--state-dir " + dir} {
			if !strings.Contains(run, want) {
				t.Errorf("fpRunCmd(%d) = %q lacks %q", n, run, want)
			}
		}
		enroll := fpEnrollCmd(n, "/root/fp-token-1")
		for _, want := range []string{"sudo " + fpBinaryPath + " agent enroll", "--state-dir " + dir, "--token-file /root/fp-token-1"} {
			if !strings.Contains(enroll, want) {
				t.Errorf("fpEnrollCmd(%d) = %q lacks %q", n, enroll, want)
			}
		}
		if stop := fpStopCmd(n); !strings.Contains(stop, unit) || !strings.HasPrefix(stop, "sudo systemctl stop ") {
			t.Errorf("fpStopCmd(%d) = %q", n, stop)
		}
	}
	if err := fpSocketProblem(fpStateRoot + "/" + strings.Repeat("a", 90)); err == nil || !strings.Contains(err.Error(), "over the 107") {
		t.Errorf("a 90-character state directory: %v, want an error naming the 107-byte limit", err)
	}
	if fpBinaryName != "darkpanel-0.2.0-state" || !strings.HasPrefix(fpBinaryPath, fpStateRoot+"/") {
		t.Errorf("binary %q at %q", fpBinaryName, fpBinaryPath)
	}
	if got := fpPIDCmd("darkpanel-agent.service"); got != "systemctl show -p MainPID --value darkpanel-agent.service" {
		t.Errorf("fpPIDCmd = %q", got)
	}
	if got := fpRSSCmd(77); got != "awk '/^VmRSS/{print $2}' /proc/77/status" {
		t.Errorf("fpRSSCmd = %q", got)
	}
}

func fpModules(enabled map[string]bool) []core.ModuleState {
	var ms []core.ModuleState
	for _, id := range []string{"hostinfo", "firewall", "docker", "tailscale", "tailnet", "derp", "npm", "cloudflare", "panel"} {
		on, listed := enabled[id]
		if listed {
			ms = append(ms, core.ModuleState{ID: id, Enabled: on})
		}
	}
	return ms
}

func TestFpUnitModuleChecks(t *testing.T) {
	all := map[string]bool{"hostinfo": true, "firewall": true, "docker": true, "tailscale": true, "tailnet": true, "derp": true, "npm": true, "cloudflare": true, "panel": true}
	onlyInfo := map[string]bool{"hostinfo": true, "firewall": false, "docker": false, "tailscale": false, "tailnet": false, "derp": false, "npm": false, "cloudflare": false, "panel": false}
	h := func(ms map[string]bool) core.Host { return core.Host{Name: "fp-1", Modules: fpModules(ms)} }

	want := []string{"cloudflare", "derp", "docker", "firewall", "npm", "panel", "tailnet", "tailscale"}
	if got := fpDisableTargets(h(all)); !slices.Equal(got, want) {
		t.Errorf("fpDisableTargets = %v, want %v (every module but hostinfo, sorted)", got, want)
	}
	if got := fpDisableTargets(core.Host{}); len(got) != 0 {
		t.Errorf("fpDisableTargets of a host with no modules = %v", got)
	}

	for _, c := range []struct {
		name    string
		host    core.Host
		check   func(core.Host) error
		wantErr string
	}{
		{"only hostinfo: accepted", h(onlyInfo), fpOnlyHostinfoEnabled, ""},
		{"only hostinfo: firewall still on", h(map[string]bool{"hostinfo": true, "firewall": true, "docker": false}), fpOnlyHostinfoEnabled, "firewall enabled true"},
		{"only hostinfo: hostinfo off", h(map[string]bool{"hostinfo": false, "firewall": false}), fpOnlyHostinfoEnabled, "hostinfo enabled false"},
		{"only hostinfo: not listed", h(map[string]bool{"firewall": false}), fpOnlyHostinfoEnabled, "no hostinfo module listed"},
		{"only hostinfo: no modules", core.Host{Name: "fp-1"}, fpOnlyHostinfoEnabled, "no hostinfo module listed"},
		{"all enabled: accepted", h(all), fpAllEnabled, ""},
		{"all enabled: docker off", h(map[string]bool{"hostinfo": true, "docker": false}), fpAllEnabled, "[docker]"},
		{"all enabled: no modules", core.Host{Name: "agent"}, fpAllEnabled, "every module enabled"},
	} {
		err := c.check(c.host)
		if (c.wantErr == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), c.wantErr)) {
			t.Errorf("%s: %v, want %q", c.name, err, c.wantErr)
		}
	}
}

func TestFpUnitFleetOnline(t *testing.T) {
	up := func(name string) core.Host { return core.Host{Name: name, State: core.HostEnrolled, Online: true} }
	hosts := []core.Host{up("controller"), up("agent"), up("fp-1")}
	for _, c := range []struct {
		name    string
		hosts   []core.Host
		want    int
		wantErr string
	}{
		{"all up", hosts, 3, ""},
		{"one offline", []core.Host{up("controller"), {Name: "fp-1", State: core.HostEnrolled}, up("agent")}, 3, "fp-1"},
		{"one pending", []core.Host{up("controller"), {Name: "fp-2", State: core.HostPending, Online: true}, up("agent")}, 3, "fp-2"},
		{"a host is missing", hosts[:2], 3, "2 hosts, want 3"},
		{"an extra host", append(slices.Clone(hosts), up("stray")), 3, "4 hosts, want 3"},
	} {
		err := fpFleetOnline(c.hosts, c.want)
		if (c.wantErr == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), c.wantErr)) {
			t.Errorf("%s: %v, want %q", c.name, err, c.wantErr)
		}
	}
}

func TestFpUnitNoReconcile(t *testing.T) {
	quiet := "level=INFO msg=\"agent: serving\" socket=/var/lib/dpx/3/run/agent.sock\nlevel=INFO msg=\"daemon: controller session\" state=up\n"
	if err := fpNoReconcile(quiet); err != nil {
		t.Errorf("a quiet journal: %v", err)
	}
	loud := quiet + "level=WARN msg=\"reconcile skipped: DOCKER-USER holds foreign rules\"\n"
	if err := fpNoReconcile(loud); err == nil || !strings.Contains(err.Error(), "DOCKER-USER holds foreign rules") {
		t.Errorf("a journal with a reconcile line: %v, want an error quoting it", err)
	}
	if err := fpNoReconcile("task firewall.RECONCILE pass\n"); err == nil {
		t.Error("an upper-case RECONCILE passes: the match must ignore case")
	}
}

// TestFpUnitMetricsFixture runs the fixture script twice on a migrated controller database, as the poller may have
// stored some of its minutes: every row fits the schema, each table holds the counts metricsFixture checks, and the
// newest and oldest rows lie inside the minute and hour retention, in the stored timestamp format.
func TestFpUnitMetricsFixture(t *testing.T) {
	ctx := context.Background()
	path := fpUnitControllerDB(t)
	now := time.Date(2026, 11, 24, 12, 34, 56, 0, time.UTC)
	ids := []string{"01JW7RAGENT0000000000000A1", "01JW7RAGENT0000000000000A2"}
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, id := range ids {
		if _, err := db.ExecContext(ctx, `INSERT INTO core_hosts (id, name, is_local, created_at, state) VALUES (?, ?, 0, ?, 'enrolled')`,
			id, "host-"+id, now.Format(time.RFC3339Nano)); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		for _, id := range ids {
			if _, err := db.ExecContext(ctx, fpFixtureSQL(id, now)); err != nil {
				t.Fatalf("the fixture does not fit the controller schema: %v", err)
			}
		}
	}
	for _, c := range fpFixtureCounts(ids) {
		var n int
		if err := db.QueryRowContext(ctx, c.query).Scan(&n); err != nil {
			t.Fatalf("%s: %v", c.query, err)
		}
		if n != c.least {
			t.Errorf("%s holds %d rows, want %d", c.table, n, c.least)
		}
	}
	for _, c := range []struct{ query, want string }{
		{"SELECT max(ts) FROM metrics_minute", "2026-11-24T12:33:00Z"},
		{"SELECT min(ts) FROM metrics_minute", "2026-11-17T12:35:00Z"},
		{"SELECT max(ts) FROM metrics_hour", "2026-11-24T11:00:00Z"},
		{"SELECT min(ts) FROM metrics_hour", "2026-08-26T13:00:00Z"},
		{"SELECT group_concat(DISTINCT mount) FROM metrics_minute_fs", "/,/srv"},
		{"SELECT count(*) FROM metrics_minute_net WHERE ts NOT GLOB '????-??-??T??:??:00Z'", "0"},
	} {
		var got string
		if err := db.QueryRowContext(ctx, c.query).Scan(&got); err != nil {
			t.Fatalf("%s: %v", c.query, err)
		}
		if got != c.want {
			t.Errorf("%s = %q, want %q", c.query, got, c.want)
		}
	}
	if minute, hour := now.Add(-7*24*time.Hour), now.Add(-90*24*time.Hour); fpFixtureMinutes*time.Minute >= now.Sub(minute) ||
		fpFixtureHours*time.Hour >= now.Sub(hour) {
		t.Error("the fixture reaches past the retention of minute or hour rows")
	}
}

// fpUnitControllerDB returns the path of a new controller database with the core and every module's migrations.
func fpUnitControllerDB(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "controller.db")
	db, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	coreSet, err := store.CoreMigrations(store.SideController)
	if err != nil {
		t.Fatal(err)
	}
	var sets []store.MigrationSet
	for _, m := range modules.All() {
		man := m.Manifest()
		if fsys, ok := man.Migrations[core.SideController]; ok && fsys != nil {
			sets = append(sets, store.MigrationSet{Module: man.ID, FS: fsys})
		}
	}
	if _, err := store.Migrate(ctx, db, store.MigrateOptions{
		Side: store.SideController, BinaryVersion: "dev", BackupRoot: filepath.Join(dir, "backups"), Keep: 1, Now: time.Now(),
	}, coreSet, sets); err != nil {
		t.Fatal(err)
	}
	return path
}
