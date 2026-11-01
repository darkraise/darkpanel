package tailscale

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
	"time"
)

var thNow = time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)

func thColumns(t *testing.T, h *tsHost, table string) []string {
	t.Helper()
	rows, err := h.db.Read().Query(`SELECT name FROM pragma_table_info(?) ORDER BY cid`, table)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var cols []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			t.Fatal(err)
		}
		cols = append(cols, c)
	}
	return cols
}

func thPin(id string, at time.Time) pinRow {
	return pinRow{
		ChangeID: id, Version: "1.102.0", Arch: "amd64", PriorVersion: "1.104.1", PriorHeld: true, PriorBackend: "Running",
		LinkTailnet: true, Phase: "recorded", UpdatedAt: at,
	}
}

func thInsertPin(t *testing.T, h *tsHost, p pinRow) {
	t.Helper()
	ctx := context.Background()
	if err := h.db.Write(ctx, func(tx *sql.Tx) error { return insertPin(ctx, tx, p) }); err != nil {
		t.Fatal(err)
	}
}

func thSteps(t *testing.T, h *tsHost, changeID string) [][3]string {
	t.Helper()
	rows, err := h.db.Read().Query(`SELECT seq, step, result FROM tailscale_pin_steps WHERE change_id = ? ORDER BY seq`, changeID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out [][3]string
	next := 1
	for rows.Next() {
		var seq int
		var step, result string
		if err := rows.Scan(&seq, &step, &result); err != nil {
			t.Fatal(err)
		}
		if seq != next {
			t.Errorf("step %d has seq %d, want %d", len(out), seq, next)
		}
		next++
		out = append(out, [3]string{"", step, result})
	}
	return out
}

func TestTHMigrationShape(t *testing.T) {
	h := newTSHost(t)
	for table, want := range map[string][]string{
		"tailscale_pins": {"change_id", "version", "arch", "prior_version", "prior_held", "prior_backend", "link_tailnet", "phase",
			"staged_deb", "staged_sha256", "unpinned_by", "updated_at"},
		"tailscale_pin_steps": {"change_id", "seq", "step", "result", "at"},
		"tailscale_installs":  {"change_id", "version", "installed_at", "removed_at"},
	} {
		if got := thColumns(t, h, table); !reflect.DeepEqual(got, want) {
			t.Errorf("%s columns %v, want %v", table, got, want)
		}
		var strict int
		if err := h.db.Read().QueryRow(`SELECT strict FROM pragma_table_list WHERE name = ?`, table).Scan(&strict); err != nil || strict != 1 {
			t.Errorf("%s: strict = %d, %v; want a STRICT table", table, strict, err)
		}
	}
}

func TestTHMigrationConstraints(t *testing.T) {
	h := newTSHost(t)
	ctx := context.Background()
	insert := func(p pinRow) error {
		return h.db.Write(ctx, func(tx *sql.Tx) error { return insertPin(ctx, tx, p) })
	}
	bad := thPin("c-phase", thNow)
	bad.Phase = "paused"
	if err := insert(bad); err == nil {
		t.Error("a phase outside the list was stored")
	}
	for _, phase := range []string{"recorded", "unheld", "staged", "installed", "held", "checking", "done", "rolling_back", "rolled_back", "failed"} {
		p := thPin("c-"+phase, thNow)
		p.Phase = phase
		if err := insert(p); err != nil {
			t.Errorf("phase %s refused: %v", phase, err)
		}
	}
	if err := insert(thPin("c-recorded", thNow)); err == nil {
		t.Error("a pin was stored twice under one change id")
	}
	if err := h.db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE tailscale_pins SET prior_held = 2 WHERE change_id = 'c-held'`)
		return err
	}); err == nil {
		t.Error("prior_held 2 was stored")
	}
	if err := addPinStep(ctx, h.db, "no-such-pin", "unheld", "ok", thNow); err == nil {
		t.Error("a step of an unknown pin was stored: the foreign key must hold")
	}
}

func TestTHPinRoundTrip(t *testing.T) {
	h := newTSHost(t)
	ctx := context.Background()
	if _, ok, err := loadPin(ctx, h.db.Read(), "c1"); ok || err != nil {
		t.Fatalf("loadPin of none = %v, %v", ok, err)
	}
	want := thPin("c1", thNow)
	want.StagedDeb, want.StagedSHA256 = "tailscale_1.104.1_amd64.deb", "ab12"
	want.UnpinnedBy = "c9"
	thInsertPin(t, h, want)
	got, ok, err := loadPin(ctx, h.db.Read(), "c1")
	if err != nil || !ok || !reflect.DeepEqual(got, want) || !got.UpdatedAt.Equal(thNow) {
		t.Fatalf("loadPin = %+v, %v, %v\nwant %+v", got, ok, err, want)
	}

	// Times are stored as fixed-width UTC text, whatever zone they arrive in.
	zone := time.FixedZone("x", 2*3600)
	p := thPin("c2", time.Date(2026, 10, 31, 14, 0, 0, 5, zone))
	thInsertPin(t, h, p)
	var raw string
	if err := h.db.Read().QueryRow(`SELECT updated_at FROM tailscale_pins WHERE change_id = 'c2'`).Scan(&raw); err != nil || raw != "2026-10-31T12:00:00.000000005Z" {
		t.Errorf("stored time %q, %v; want 2026-10-31T12:00:00.000000005Z", raw, err)
	}
}

func TestTHPinUpdates(t *testing.T) {
	h := newTSHost(t)
	ctx := context.Background()
	thInsertPin(t, h, thPin("c1", thNow))
	later := thNow.Add(time.Minute)

	if err := setPinPhase(ctx, h.db, "c1", "unheld", later); err != nil {
		t.Fatal(err)
	}
	p, _, _ := loadPin(ctx, h.db.Read(), "c1")
	if p.Phase != "unheld" || !p.UpdatedAt.Equal(later) {
		t.Errorf("after setPinPhase: %+v", p)
	}

	evenLater := later.Add(time.Minute)
	if err := setPinStaged(ctx, h.db, "c1", "tailscale_1.104.1_amd64.deb", "ab12", evenLater); err != nil {
		t.Fatal(err)
	}
	p, _, _ = loadPin(ctx, h.db.Read(), "c1")
	if p.StagedDeb != "tailscale_1.104.1_amd64.deb" || p.StagedSHA256 != "ab12" || p.Phase != "unheld" || !p.UpdatedAt.Equal(evenLater) {
		t.Errorf("after setPinStaged: %+v; the phase must stay as it was", p)
	}

	if err := setPinUnpinned(ctx, h.db, "c1", "c7", evenLater.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	p, _, _ = loadPin(ctx, h.db.Read(), "c1")
	if p.UnpinnedBy != "c7" || p.Phase != "unheld" {
		t.Errorf("after setPinUnpinned: %+v", p)
	}

	if err := setPinPhase(ctx, h.db, "c1", "paused", later); err == nil {
		t.Error("an unknown phase was accepted")
	}
	if p, _, _ := loadPin(ctx, h.db.Read(), "c1"); p.Phase != "unheld" {
		t.Errorf("a refused phase changed the row to %s", p.Phase)
	}
	for name, err := range map[string]error{
		"setPinPhase":    setPinPhase(ctx, h.db, "nope", "done", later),
		"setPinStaged":   setPinStaged(ctx, h.db, "nope", "d", "s", later),
		"setPinUnpinned": setPinUnpinned(ctx, h.db, "nope", "c", later),
	} {
		if err == nil {
			t.Errorf("%s of an unknown pin succeeded", name)
		}
	}
}

func TestTHPinSteps(t *testing.T) {
	h := newTSHost(t)
	ctx := context.Background()
	thInsertPin(t, h, thPin("c1", thNow))
	thInsertPin(t, h, thPin("c2", thNow))
	for i, s := range [][2]string{{"unhold", "ok"}, {"stage prior", "ok"}, {"install", "exit 100"}} {
		if err := addPinStep(ctx, h.db, "c1", s[0], s[1], thNow.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	if err := addPinStep(ctx, h.db, "c2", "unhold", "ok", thNow); err != nil {
		t.Fatal(err)
	}
	got := thSteps(t, h, "c1")
	want := [][3]string{{"", "unhold", "ok"}, {"", "stage prior", "ok"}, {"", "install", "exit 100"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("steps of c1 %v, want %v", got, want)
	}
	if got := thSteps(t, h, "c2"); len(got) != 1 {
		t.Errorf("steps of c2 %v, want one: each pin numbers its own steps from 1", got)
	}
}

func TestTHActivePin(t *testing.T) {
	h := newTSHost(t)
	ctx := context.Background()
	active := func() (string, bool) {
		p, ok, err := activePin(ctx, h.db.Read())
		if err != nil {
			t.Fatal(err)
		}
		return p.ChangeID, ok
	}
	if id, ok := active(); ok {
		t.Fatalf("activePin with no pins = %q", id)
	}
	a, b, c := thPin("a", thNow), thPin("b", thNow.Add(10*time.Minute)), thPin("c", thNow.Add(20*time.Minute))
	a.Phase, b.Phase, c.Phase = "done", "done", "checking"
	thInsertPin(t, h, a)
	thInsertPin(t, h, b)
	thInsertPin(t, h, c)
	if id, ok := active(); !ok || id != "b" {
		t.Errorf("activePin = %q, %v; want b: the newest pin in phase done (c is still checking)", id, ok)
	}
	if err := setPinUnpinned(ctx, h.db, "b", "u1", thNow.Add(30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if id, ok := active(); !ok || id != "a" {
		t.Errorf("activePin after b was unpinned = %q, %v; want a", id, ok)
	}
	if err := setPinUnpinned(ctx, h.db, "a", "u2", thNow.Add(31*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if id, ok := active(); ok {
		t.Errorf("activePin with every done pin unpinned = %q", id)
	}

	// Two pins with one update time: the later change id is the newer.
	d, e := thPin("d1", thNow.Add(time.Hour)), thPin("d2", thNow.Add(time.Hour))
	d.Phase, e.Phase = "done", "done"
	thInsertPin(t, h, d)
	thInsertPin(t, h, e)
	if id, _ := active(); id != "d2" {
		t.Errorf("activePin on a tie = %q, want d2", id)
	}
}

func TestTHInstalls(t *testing.T) {
	h := newTSHost(t)
	ctx := context.Background()
	if _, _, ok, err := liveInstall(ctx, h.db.Read()); ok || err != nil {
		t.Fatalf("liveInstall with none = %v, %v", ok, err)
	}
	insert := func(id, version string, at time.Time) error {
		return h.db.Write(ctx, func(tx *sql.Tx) error { return insertInstall(ctx, tx, id, version, at) })
	}
	if err := insert("i1", "1.102.0", thNow); err != nil {
		t.Fatal(err)
	}
	if err := insert("i1", "1.102.0", thNow); err == nil {
		t.Error("an install was recorded twice under one change id")
	}
	if id, v, ok, err := liveInstall(ctx, h.db.Read()); err != nil || !ok || id != "i1" || v != "1.102.0" {
		t.Errorf("liveInstall = %q, %q, %v, %v", id, v, ok, err)
	}
	if err := insert("i2", "1.104.1", thNow.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if id, _, _, _ := liveInstall(ctx, h.db.Read()); id != "i2" {
		t.Errorf("liveInstall = %q, want the newest install i2", id)
	}
	if err := setInstallRemoved(ctx, h.db, "i2", thNow.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if id, _, ok, _ := liveInstall(ctx, h.db.Read()); !ok || id != "i1" {
		t.Errorf("liveInstall after i2 was removed = %q, %v; want i1", id, ok)
	}
	if err := setInstallRemoved(ctx, h.db, "i1", thNow.Add(3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, _, ok, _ := liveInstall(ctx, h.db.Read()); ok {
		t.Error("liveInstall found an install after every one was removed")
	}
	if err := setInstallRemoved(ctx, h.db, "nope", thNow); err == nil {
		t.Error("removing an unknown install succeeded")
	}
}
