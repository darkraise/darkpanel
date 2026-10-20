package listen

import (
	"context"
	"log/slog"
	"net/netip"
	"reflect"
	"strings"
	"testing"
)

func TestTailscaleWatcher(t *testing.T) {
	ctx := context.Background()
	db := openControllerDB(t)
	if err := SaveTailscaleAddrs(ctx, db, addrs("100.64.0.9")); err != nil {
		t.Fatal(err)
	}
	ifs := &fakeInterfaces{}
	log := &logBuffer{}
	w, err := NewTailscaleWatcher(ctx, db, ifs.list, slog.New(slog.NewTextHandler(log, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if got := w.Addrs(); !reflect.DeepEqual(got, addrs("100.64.0.9")) {
		t.Fatalf("without tailscale0 the watcher starts from %v, want the saved address", got)
	}
	if ifs.count() != 1 {
		t.Fatalf("the watcher read the interfaces %d times at the start, want once", ifs.count())
	}

	steps := []struct {
		name    string
		present bool
		list    []string
		want    []netip.Addr
		saved   string
	}{
		{"addresses appear", true, []string{"fd7a:115c:a1e0::2", "100.64.0.2", "192.0.2.1"},
			addrs("100.64.0.2", "fd7a:115c:a1e0::2"), `["100.64.0.2","fd7a:115c:a1e0::2"]`},
		{"interface gone", false, nil, addrs("100.64.0.2", "fd7a:115c:a1e0::2"), `["100.64.0.2","fd7a:115c:a1e0::2"]`},
		{"addresses gone", true, nil, nil, `[]`},
	}
	for _, s := range steps {
		ifs.set(s.present, s.list...)
		w.Refresh(ctx)
		if got := w.Addrs(); !reflect.DeepEqual(got, s.want) {
			t.Fatalf("%s: addresses %v, want %v", s.name, got, s.want)
		}
		if got := savedAddrs(t, db); got != s.saved {
			t.Fatalf("%s: saved %s, want %s", s.name, got, s.saved)
		}
	}
	if !strings.Contains(log.String(), "listen: Tailscale addresses changed") {
		t.Fatalf("the change was not logged:\n%s", log.String())
	}

	ifs.set(true, "100.64.0.3")
	w.Refresh(ctx)
	got := w.Addrs()
	got[0] = netip.MustParseAddr("100.64.0.4")
	if w.Addrs()[0] != netip.MustParseAddr("100.64.0.3") {
		t.Fatal("Addrs returned the watcher's own slice")
	}
}

func TestTailscaleWatcherReadsTheInterfaceAtStart(t *testing.T) {
	ctx := context.Background()
	db := openControllerDB(t)
	if err := SaveTailscaleAddrs(ctx, db, addrs("100.64.0.9")); err != nil {
		t.Fatal(err)
	}
	ifs := &fakeInterfaces{}
	ifs.set(true, "100.64.0.1")
	w, err := NewTailscaleWatcher(ctx, db, ifs.list, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := w.Addrs(); !reflect.DeepEqual(got, addrs("100.64.0.1")) || savedAddrs(t, db) != `["100.64.0.1"]` {
		t.Fatalf("addresses %v, saved %s; tailscale0 should replace the saved address", got, savedAddrs(t, db))
	}
}

func TestTailscaleWatcherRefusesABadSavedValue(t *testing.T) {
	ctx := context.Background()
	db := openControllerDB(t)
	if _, err := db.Writer().ExecContext(ctx, `INSERT INTO core_meta (key, value) VALUES ('tailscale_addrs', 'nope')`); err != nil {
		t.Fatal(err)
	}
	if _, err := NewTailscaleWatcher(ctx, db, (&fakeInterfaces{}).list, nil); err == nil {
		t.Fatal("a bad saved value was accepted")
	}
}
