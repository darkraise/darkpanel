package listen

import (
	"context"
	"net/netip"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core/store"
)

// openControllerDB returns a temporary controller database with the core
// migrations applied.
func openControllerDB(t *testing.T) *store.DB {
	t.Helper()
	return openControllerDBAt(t, filepath.Join(t.TempDir(), "controller.db"))
}

// openControllerDBAt opens the controller database at path, applying the
// core migrations when they are pending.
func openControllerDBAt(t *testing.T, path string) *store.DB {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	set, err := store.CoreMigrations(store.SideController)
	if err != nil {
		t.Fatal(err)
	}
	opts := store.MigrateOptions{
		Side: store.SideController, BinaryVersion: "test",
		BackupRoot: filepath.Join(filepath.Dir(path), "backups"), Keep: 5, Now: time.Now(),
	}
	if _, err := store.Migrate(ctx, db, opts, set, nil); err != nil {
		t.Fatal(err)
	}
	return db
}

func addrs(s ...string) []netip.Addr {
	out := make([]netip.Addr, 0, len(s))
	for _, a := range s {
		out = append(out, netip.MustParseAddr(a))
	}
	return out
}

func TestTailscaleAddrs(t *testing.T) {
	got := TailscaleAddrs(addrs(
		"fd7a:115c:a1e0::9", "100.127.255.255", "100.64.0.1", "100.128.0.1", "100.63.255.255",
		"fe80::1", "fd7a:115c:a1e1::1", "192.168.1.5", "::ffff:100.100.1.1", "100.64.0.1",
	))
	want := addrs("100.64.0.1", "100.100.1.1", "100.127.255.255", "fd7a:115c:a1e0::9")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("TailscaleAddrs = %v, want %v", got, want)
	}
	if got := TailscaleAddrs(addrs("192.168.1.5")); got != nil {
		t.Fatalf("TailscaleAddrs without a match = %v, want nil", got)
	}
}

func TestSystemInterfaceAddrs(t *testing.T) {
	got, present, err := SystemInterfaceAddrs("lo")
	if err != nil {
		t.Fatal(err)
	}
	if !present {
		t.Skip("this host has no interface lo")
	}
	if !slices.Contains(got, netip.MustParseAddr("127.0.0.1")) {
		t.Fatalf("lo addresses %v lack 127.0.0.1", got)
	}
	got, present, err = SystemInterfaceAddrs("dp-absent-if0")
	if err != nil || present || got != nil {
		t.Fatalf("absent interface = %v, %v, %v; want nil, false, nil", got, present, err)
	}
}

func TestTailscaleAddrsRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := openControllerDB(t)
	got, err := LoadTailscaleAddrs(ctx, db)
	if err != nil || got != nil {
		t.Fatalf("Load on a new database = %v, %v; want nil, nil", got, err)
	}
	want := addrs("100.64.0.1", "fd7a:115c:a1e0::1")
	if err := SaveTailscaleAddrs(ctx, db, want); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := db.Read().QueryRowContext(ctx, `SELECT value FROM core_meta WHERE key = 'tailscale_addrs'`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != `["100.64.0.1","fd7a:115c:a1e0::1"]` {
		t.Fatalf("stored value = %s", stored)
	}
	if got, err = LoadTailscaleAddrs(ctx, db); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("Load = %v, %v; want %v", got, err, want)
	}
	if err := SaveTailscaleAddrs(ctx, db, nil); err != nil {
		t.Fatal(err)
	}
	if got, err = LoadTailscaleAddrs(ctx, db); err != nil || got != nil {
		t.Fatalf("Load after saving none = %v, %v; want nil, nil", got, err)
	}
}

func TestLoadTailscaleAddrsRefusesBadValue(t *testing.T) {
	ctx := context.Background()
	db := openControllerDB(t)
	for _, v := range []string{`not json`, `["100.64.0.1","bogus"]`} {
		if _, err := db.Writer().ExecContext(ctx,
			`INSERT INTO core_meta (key, value) VALUES ('tailscale_addrs', ?)
			 ON CONFLICT (key) DO UPDATE SET value = excluded.value`, v); err != nil {
			t.Fatal(err)
		}
		_, err := LoadTailscaleAddrs(ctx, db)
		if err == nil || !strings.Contains(err.Error(), "core_meta tailscale_addrs") {
			t.Errorf("Load of %s: error = %v, want a core_meta tailscale_addrs error", v, err)
		}
	}
}
