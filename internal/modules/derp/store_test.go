package derp

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"
)

var sdNow = time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

func sdLocalServer() localServer {
	return localServer{
		ChangeID: "c1", Mode: "ip", HostName: "203.0.113.10", VerifyClients: true, DerperVersion: "v1.104.1",
		BinarySHA256: strings.Repeat("a", 64), CertName: "sha256-raw:" + strings.Repeat("b", 64),
		CertNotAfter: sdNow.AddDate(1, 0, 0), InstalledAt: sdNow, UpdatedAt: sdNow,
	}
}

func sdText(t *testing.T, h *dpHost, query string) string {
	t.Helper()
	var s string
	if err := h.db.Read().QueryRow(query).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSdLocalServer(t *testing.T) {
	h := newDPHost(t)
	ctx := context.Background()

	if _, ok, err := loadLocalServer(ctx, h.db.Read()); err != nil || ok {
		t.Fatalf("loadLocalServer on an empty table = %v, %v; want none", ok, err)
	}
	want := sdLocalServer()
	if err := insertLocalServer(ctx, h.db, want); err != nil {
		t.Fatal(err)
	}
	got, ok, err := loadLocalServer(ctx, h.db.Read())
	if err != nil || !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("loadLocalServer = %+v, %v, %v; want %+v", got, ok, err, want)
	}
	if raw := sdText(t, h, `SELECT installed_at FROM derp_server`); raw != "2026-11-02T12:00:00.000000000Z" {
		t.Errorf("installed_at is stored as %q, want fixed-width RFC 3339 UTC text", raw)
	}
	if err := insertLocalServer(ctx, h.db, sdLocalServer()); err == nil {
		t.Error("a second server row was accepted: at most one server is installed")
	}
	bad := sdLocalServer()
	bad.Mode = "dns"
	if err := deleteLocalServer(ctx, h.db); err != nil {
		t.Fatal(err)
	}
	if err := insertLocalServer(ctx, h.db, bad); err == nil {
		t.Error("a mode outside letsencrypt and ip was accepted")
	}
}

func TestSdLocalServerUpdates(t *testing.T) {
	h := newDPHost(t)
	ctx := context.Background()
	if err := setLocalBinary(ctx, h.db, "v1.105.0", "x", sdNow); err == nil {
		t.Error("setLocalBinary without a server row succeeded")
	}
	if err := setLocalCert(ctx, h.db, "p", sdNow, sdNow); err == nil {
		t.Error("setLocalCert without a server row succeeded")
	}
	if err := insertLocalServer(ctx, h.db, sdLocalServer()); err != nil {
		t.Fatal(err)
	}
	later := sdNow.Add(time.Hour)
	if err := setLocalBinary(ctx, h.db, "v1.105.0", strings.Repeat("c", 64), later); err != nil {
		t.Fatal(err)
	}
	got, _, _ := loadLocalServer(ctx, h.db.Read())
	want := sdLocalServer()
	want.DerperVersion, want.BinarySHA256, want.UpdatedAt = "v1.105.0", strings.Repeat("c", 64), later
	if !reflect.DeepEqual(got, want) {
		t.Errorf("after setLocalBinary: %+v, want %+v", got, want)
	}
	notAfter := sdNow.AddDate(2, 0, 0)
	if err := setLocalCert(ctx, h.db, "sha256-raw:new", notAfter, later.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, _, _ = loadLocalServer(ctx, h.db.Read())
	want.CertName, want.CertNotAfter, want.UpdatedAt = "sha256-raw:new", notAfter, later.Add(time.Hour)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("after setLocalCert: %+v, want %+v", got, want)
	}
}

func TestSdLocalServerZeroTimeAndDelete(t *testing.T) {
	h := newDPHost(t)
	ctx := context.Background()
	s := sdLocalServer()
	s.Mode, s.CertName, s.CertNotAfter = "letsencrypt", "", time.Time{}
	if err := insertLocalServer(ctx, h.db, s); err != nil {
		t.Fatal(err)
	}
	if raw := sdText(t, h, `SELECT cert_not_after FROM derp_server`); raw != "" {
		t.Errorf("a zero time is stored as %q, want an empty string", raw)
	}
	got, ok, err := loadLocalServer(ctx, h.db.Read())
	if err != nil || !ok || !got.CertNotAfter.IsZero() {
		t.Fatalf("loadLocalServer = %+v, %v, %v; want a zero CertNotAfter", got, ok, err)
	}
	for range 2 {
		if err := deleteLocalServer(ctx, h.db); err != nil {
			t.Fatalf("deleteLocalServer: %v (a missing row is not an error)", err)
		}
	}
	if _, ok, _ := loadLocalServer(ctx, h.db.Read()); ok {
		t.Error("the server row survived deleteLocalServer")
	}
}

func TestSdUpdates(t *testing.T) {
	h := newDPHost(t)
	ctx := context.Background()
	if _, ok, err := loadUpdate(ctx, h.db.Read(), "c9"); err != nil || ok {
		t.Fatalf("loadUpdate of an unknown change = %v, %v", ok, err)
	}
	u := updateRow{
		ChangeID: "c9", FromVersion: "v1.103.0", FromSHA256: strings.Repeat("a", 64), ToVersion: "v1.104.1",
		ToSHA256: strings.Repeat("b", 64), Phase: "staged", UpdatedAt: sdNow,
	}
	if err := insertUpdate(ctx, h.db, u); err != nil {
		t.Fatal(err)
	}
	got, ok, err := loadUpdate(ctx, h.db.Read(), "c9")
	if err != nil || !ok || !reflect.DeepEqual(got, u) {
		t.Fatalf("loadUpdate = %+v, %v, %v; want %+v", got, ok, err, u)
	}
	if err := insertUpdate(ctx, h.db, u); err == nil {
		t.Error("a second update row for one change was accepted")
	}
	later := sdNow.Add(time.Minute)
	if err := setUpdatePhase(ctx, h.db, "c9", "failed", "derper did not start", later); err != nil {
		t.Fatal(err)
	}
	u.Phase, u.Result, u.UpdatedAt = "failed", "derper did not start", later
	if got, _, _ := loadUpdate(ctx, h.db.Read(), "c9"); !reflect.DeepEqual(got, u) {
		t.Errorf("after setUpdatePhase: %+v, want %+v", got, u)
	}
	if err := setUpdatePhase(ctx, h.db, "c9", "teleported", "", later); err == nil {
		t.Error("a phase outside the table's list was accepted")
	}
	if got, _, _ := loadUpdate(ctx, h.db.Read(), "c9"); got.Phase != "failed" {
		t.Errorf("a refused phase changed the row to %q", got.Phase)
	}
	if err := setUpdatePhase(ctx, h.db, "nope", "done", "", later); err == nil {
		t.Error("setUpdatePhase of an unknown change succeeded")
	}
}

func sdRecord(host string) serverRecord {
	return serverRecord{
		HostID: host, Mode: "ip", HostName: "203.0.113.10", RegionID: 900, RegionCode: "fra-x", DerperVersion: "v1.104.1",
		CertName: "sha256-raw:ab", CertNotAfter: sdNow.AddDate(1, 0, 0), UnitActive: true, CheckedAt: sdNow,
	}
}

func TestSdServerRecords(t *testing.T) {
	h := newDPHost(t)
	ctx := context.Background()
	q := h.ctlDB.Read()
	if recs, err := loadServerRecords(ctx, q); err != nil || len(recs) != 0 {
		t.Fatalf("loadServerRecords on an empty table = %v, %v", recs, err)
	}
	if _, ok, err := loadServerRecord(ctx, q, "h2"); err != nil || ok {
		t.Fatalf("loadServerRecord of an unknown host = %v, %v", ok, err)
	}
	for _, id := range []string{"h3", "h2"} {
		if err := upsertServerRecord(ctx, h.ctlDB, sdRecord(id)); err != nil {
			t.Fatal(err)
		}
	}
	recs, err := loadServerRecords(ctx, q)
	if err != nil || len(recs) != 2 || recs[0].HostID != "h2" || recs[1].HostID != "h3" {
		t.Fatalf("loadServerRecords = %+v, %v; want h2 then h3", recs, err)
	}
	if got, ok, err := loadServerRecord(ctx, q, "h2"); err != nil || !ok || !reflect.DeepEqual(got, sdRecord("h2")) {
		t.Fatalf("loadServerRecord = %+v, %v, %v", got, ok, err)
	}

	changed := sdRecord("h2")
	changed.RegionID, changed.RegionCode, changed.UnitActive, changed.Error = 0, "", false, "agent unreachable"
	changed.CertNotAfter = time.Time{}
	changed.CheckedAt = sdNow.Add(time.Minute)
	if err := upsertServerRecord(ctx, h.ctlDB, changed); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := loadServerRecord(ctx, q, "h2"); !reflect.DeepEqual(got, changed) || !got.CertNotAfter.IsZero() {
		t.Errorf("after the second upsert: %+v, want %+v", got, changed)
	}
	if recs, _ := loadServerRecords(ctx, q); len(recs) != 2 {
		t.Errorf("an upsert of a known host added a row: %d rows", len(recs))
	}

	bad := sdRecord("h4")
	bad.Mode = "dns"
	if err := upsertServerRecord(ctx, h.ctlDB, bad); err == nil {
		t.Error("a mode outside letsencrypt and ip was accepted")
	}
	for range 2 {
		if err := deleteServerRecord(ctx, h.ctlDB, "h2"); err != nil {
			t.Fatalf("deleteServerRecord: %v (a missing row is not an error)", err)
		}
	}
	if _, ok, _ := loadServerRecord(ctx, q, "h2"); ok {
		t.Error("the record survived deleteServerRecord")
	}
}
