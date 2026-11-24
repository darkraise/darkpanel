package fleet

import (
	"context"
	"io/fs"
	"maps"
	"net/netip"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// Acceptance tests A7 to A9 of the remembered tailnet devices spec: what the
// controller records of a host's agent sessions in core_host_tailnet.

func qaaLogLines(logs, needle string) []string {
	var out []string
	for _, l := range strings.Split(logs, "\n") {
		if strings.Contains(l, needle) {
			out = append(out, l)
		}
	}
	return out
}

func TestQA_A7_ReportedAddressesAndStableIDAddNothing(t *testing.T) {
	ctx := context.Background()
	e := newTailnetEnv(t)
	const other = `{"backendState":"Running","self":{"id":"nOther","stableId":"nOther","StableID":"nOther","addresses":["100.64.0.9","fd7a:115c:a1e0::9"]},
		"addresses":["100.64.0.9"],"stableId":"nOther"}`
	e.answer(other, nil)
	t0 := formatTime(e.env.clock.Now())
	s := e.connect(t, "192.168.1.20:41000")
	_ = s.Close()
	s = e.connect(t, "100.64.0.7:41000")
	e.waitRows(t, map[string]string{"100.64.0.7": " " + t0})
	_ = s.Close()
	e.r.Close()

	var n int
	if err := e.env.db.Read().QueryRow(`SELECT count(*) FROM core_host_tailnet WHERE addr IN ('100.64.0.9', 'fd7a:115c:a1e0::9') OR stable_id <> ''`).Scan(&n); err != nil || n != 0 {
		t.Errorf("rows naming the other node or a stable id: %d, %v; want none", n, err)
	}
	svc := auth.New(e.env.db, newSealer(t), e.env.clock, nil, auth.Options{})
	for _, addrs := range [][]netip.Addr{
		{netip.MustParseAddr("100.64.0.9")},
		{netip.MustParseAddr("fd7a:115c:a1e0::9")},
		{netip.MustParseAddr("100.64.0.9"), netip.MustParseAddr("fd7a:115c:a1e0::9")},
	} {
		if got, err := svc.IsManagedTailnetNode(ctx, e.env.db.Read(), addrs); err != nil || got {
			t.Errorf("IsManagedTailnetNode(%v) = %v, %v; want false: the node a host's report names is not a host's", addrs, got, err)
		}
	}
	if got, err := svc.IsManagedTailnetNode(ctx, e.env.db.Read(), []netip.Addr{netip.MustParseAddr("100.64.0.7")}); err != nil || !got {
		t.Errorf("IsManagedTailnetNode(the recorded session address) = %v, %v; want true", got, err)
	}
}

func TestQA_A8_SessionAddressIsRecordedAndOthersLogOnce(t *testing.T) {
	for _, tc := range []struct {
		name, remote, addr string
		recorded           bool
		logged             int
	}{
		{"a tailnet IPv4 address", "100.64.0.7:41000", "100.64.0.7", true, 0},
		{"a tailnet IPv6 address", "[fd7a:115c:a1e0::7]:41000", "fd7a:115c:a1e0::7", true, 0},
		{"a LAN address", "192.168.1.20:41000", "192.168.1.20", false, 1},
		{"a public address", "203.0.113.5:41000", "203.0.113.5", false, 1},
		{"an address just above the tailnet range", "100.128.0.1:41000", "100.128.0.1", false, 1},
		{"loopback", "127.0.0.1:41000", "127.0.0.1", false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTailnetEnv(t)
			logs := e.logTo()
			e.answer(`{"backendState":"Running","self":{"id":"nH1","addresses":["100.64.0.50"]}}`, nil)
			now := formatTime(e.env.clock.Now())
			s := e.connect(t, tc.remote)
			want := map[string]string{}
			if tc.recorded {
				want[tc.addr] = " " + now
				e.waitRows(t, want)
			}
			_ = s.Close()
			e.r.Close()
			if got := e.rows(t); !maps.Equal(got, want) {
				t.Errorf("rows %v, want %v", got, want)
			}
			lines := qaaLogLines(logs.String(), tc.addr)
			if len(lines) != tc.logged {
				t.Errorf("%d log lines name %s, want %d: %q", len(lines), tc.addr, tc.logged, lines)
			}
			if tc.logged == 1 && !strings.Contains(lines[0], "level=INFO") {
				t.Errorf("the line %q is not at Info", lines[0])
			}
		})
	}
}

// qaaUpTo is the core controller migrations up to and including number.
func qaaUpTo(t *testing.T, number int) store.MigrationSet {
	t.Helper()
	set, err := store.CoreMigrations(store.SideController)
	if err != nil {
		t.Fatal(err)
	}
	out := fstest.MapFS{}
	entries, err := fs.ReadDir(set.FS, ".")
	if err != nil {
		t.Fatal(err)
	}
	for _, en := range entries {
		n, _ := strconv.Atoi(strings.SplitN(en.Name(), "_", 2)[0])
		if n == 0 || n > number {
			continue
		}
		b, err := fs.ReadFile(set.FS, en.Name())
		if err != nil {
			t.Fatal(err)
		}
		out[en.Name()] = &fstest.MapFile{Data: b}
	}
	return store.MigrationSet{Module: set.Module, FS: out}
}

func TestQA_A9_MigrationEmptiesTheHostTableAndAddsTheDeviceTable(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(dir, "controller.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	opts := store.MigrateOptions{Side: store.SideController, BinaryVersion: "test", BackupRoot: filepath.Join(dir, "backups"), Keep: 5, Now: time.Now()}
	if _, err := store.Migrate(ctx, db, opts, qaaUpTo(t, 18), nil); err != nil {
		t.Fatal(err)
	}
	var before int
	if err := db.Read().QueryRow(`SELECT count(*) FROM sqlite_master WHERE name = 'core_user_tailnet_devices'`).Scan(&before); err != nil || before != 0 {
		t.Fatalf("the device table exists before its migration (%d, %v)", before, err)
	}
	addHost(t, db, "H1", "enrolled")
	for _, q := range []string{
		`INSERT INTO core_host_tailnet (host_id, addr, stable_id, seen_at) VALUES ('H1', '100.64.0.9', 'nSpoofed', '2026-11-20T00:00:00.000000000Z')`,
		`INSERT INTO core_host_tailnet (host_id, addr, stable_id, seen_at) VALUES ('H1', '100.64.0.77', '', '2026-11-20T00:00:00.000000000Z')`,
	} {
		if _, err := db.Writer().ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	set, err := store.CoreMigrations(store.SideController)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Migrate(ctx, db, opts, set, nil); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := db.Read().QueryRow(`SELECT count(*) FROM core_host_tailnet`).Scan(&rows); err != nil || rows != 0 {
		t.Errorf("core_host_tailnet holds %d rows after the migration, want none (%v)", rows, err)
	}
	cols := func(table string) []string {
		q, err := db.Read().Query(`SELECT name FROM pragma_table_info(?) ORDER BY cid`, table)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = q.Close() }()
		var out []string
		for q.Next() {
			var c string
			if err := q.Scan(&c); err != nil {
				t.Fatal(err)
			}
			out = append(out, c)
		}
		return out
	}
	if got := strings.Join(cols("core_host_tailnet"), ","); got != "host_id,addr,stable_id,seen_at" {
		t.Errorf("core_host_tailnet columns %s, want host_id,addr,stable_id,seen_at (stable_id kept)", got)
	}
	if got := strings.Join(cols("core_user_tailnet_devices"), ","); got != "user_id,stable_id,node_name,remembered_at,last_used_at" {
		t.Errorf("core_user_tailnet_devices columns %s", got)
	}

	// A host that reconnects afterwards is recorded again, with an empty stable id.
	e := newTailnetEnvOn(t, db)
	e.answer(`{"backendState":"Running","self":{"id":"nH1","addresses":["100.64.0.9"]}}`, nil)
	now := formatTime(e.env.clock.Now())
	s := e.connect(t, "100.64.0.7:41000")
	e.waitRows(t, map[string]string{"100.64.0.7": " " + now})
	_ = s.Close()
	e.r.Close()
}
