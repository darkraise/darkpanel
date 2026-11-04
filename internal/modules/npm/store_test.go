package npm

import (
	"context"
	"database/sql"
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

func TestSdMigrationTables(t *testing.T) {
	h := newTHHost(t)
	rows, err := h.db.Read().Query(`SELECT name, strict FROM pragma_table_list WHERE name LIKE 'npm\_%' ESCAPE '\' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var got []string
	for rows.Next() {
		var name string
		var strict int
		if err := rows.Scan(&name, &strict); err != nil {
			t.Fatal(err)
		}
		if strict != 1 {
			t.Errorf("table %s is not STRICT", name)
		}
		got = append(got, name)
	}
	want := []string{"npm_attachments", "npm_instances", "npm_reserved", "npm_upgrades", "npm_watch"}
	if !slices.Equal(got, want) {
		t.Errorf("tables %v, want %v", got, want)
	}
}

func TestSdMigrationConstraints(t *testing.T) {
	h := newTHHost(t)
	ctx := context.Background()
	exec := func(q string) error {
		return h.db.Write(ctx, func(tx *sql.Tx) error { _, err := tx.ExecContext(ctx, q); return err })
	}
	const inst = `INSERT INTO npm_instances (id, container_id, image, admin_email, password_ct, public, created_at, updated_at) VALUES `
	const upg = `INSERT INTO npm_upgrades (change_id, from_image, to_image, config, phase, updated_at) VALUES `
	bad := map[string]string{
		"a second instance row":     inst + `(2, 'c', 'i', 'e', x'00', 'no', '', '')`,
		"an unknown public mark":    inst + `(1, 'c', 'i', 'e', x'00', 'maybe', '', '')`,
		"an unknown reserved kind":  `INSERT INTO npm_reserved VALUES ('server', 1, 'npm.admin.expose', 'h', 'c1')`,
		"an unknown reserved owner": `INSERT INTO npm_reserved VALUES ('proxy_host', 1, 'someone', 'h', 'c1')`,
		"a preexisting flag of 2":   `INSERT INTO npm_attachments VALUES ('n', 'c', 'c1', 2)`,
		"an unknown upgrade phase":  upg + `('c1', 'a', 'b', '{}', 'sideways', '')`,
		"a text object id":          `INSERT INTO npm_reserved VALUES ('proxy_host', 'x', 'npm.admin.expose', 'h', 'c1')`,
	}
	for name, q := range bad {
		if err := exec(q); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	for _, q := range []string{
		inst + `(1, 'c', 'i', 'e', x'00', 'confirmed', '', '')`,
		`INSERT INTO npm_reserved VALUES ('certificate', 1, 'panel.exposure.add', 'h', 'c1')`,
		`INSERT INTO npm_attachments VALUES ('n', 'c', 'c1', 1)`,
		upg + `('c1', 'a', 'b', '{}', 'recorded', '')`,
	} {
		if err := exec(q); err != nil {
			t.Errorf("%s: %v", q, err)
		}
	}
	var archives, result string
	if err := h.db.Read().QueryRow(`SELECT archives, result FROM npm_upgrades WHERE change_id = 'c1'`).Scan(&archives, &result); err != nil || archives != "[]" || result != "" {
		t.Errorf("upgrade defaults %q and %q (error %v), want [] and an empty result", archives, result, err)
	}
}

func TestSdStoreTime(t *testing.T) {
	if storeTime(time.Time{}) != "" {
		t.Error("the zero time is not stored as an empty string")
	}
	at := time.Date(2026, 11, 2, 12, 30, 15, 123456789, time.FixedZone("x", 3600))
	text := storeTime(at)
	if text != "2026-11-02T11:30:15.123456789Z" {
		t.Errorf("storeTime = %q", text)
	}
	back, err := parseStoreTime(text)
	if err != nil || !back.Equal(at) {
		t.Errorf("parseStoreTime(%q) = %v, %v; want %v", text, back, err, at)
	}
	if zero, err := parseStoreTime(""); err != nil || !zero.IsZero() {
		t.Errorf("parseStoreTime(\"\") = %v, %v; want the zero time", zero, err)
	}
	if _, err := parseStoreTime("yesterday"); err == nil {
		t.Error("parseStoreTime accepted yesterday")
	}
}

func TestSdInstanceRow(t *testing.T) {
	h := newTHHost(t)
	ctx := context.Background()
	if _, ok, err := loadInstance(ctx, h.db.Read()); err != nil || ok {
		t.Fatalf("empty store: ok %v, error %v", ok, err)
	}
	want := instanceRow{ContainerID: "c1", Image: "img1", AdminEmail: "ops@darkpanel.dev", PasswordCT: []byte{1, 2, 3}, Public: "no", CreatedAt: thStart, UpdatedAt: thStart}
	save := func(r instanceRow) {
		t.Helper()
		if err := h.db.Write(ctx, func(tx *sql.Tx) error { return saveInstance(ctx, tx, r) }); err != nil {
			t.Fatal(err)
		}
	}
	save(want)
	got, ok, err := loadInstance(ctx, h.db.Read())
	if err != nil || !ok {
		t.Fatalf("load: ok %v, error %v", ok, err)
	}
	if got.ContainerID != "c1" || got.Image != "img1" || got.AdminEmail != "ops@darkpanel.dev" || !slices.Equal(got.PasswordCT, []byte{1, 2, 3}) ||
		got.Public != "no" || !got.CreatedAt.Equal(thStart) || !got.UpdatedAt.Equal(thStart) {
		t.Errorf("loaded %+v, want %+v", got, want)
	}
	want.Public, want.Image, want.UpdatedAt = "confirmed", "img2", thStart.Add(time.Hour)
	save(want)
	got, _, _ = loadInstance(ctx, h.db.Read())
	if got.Public != "confirmed" || got.Image != "img2" || !got.UpdatedAt.Equal(thStart.Add(time.Hour)) || !got.CreatedAt.Equal(thStart) {
		t.Errorf("after a second save %+v, want the one row updated", got)
	}
	var n int
	if err := h.db.Read().QueryRow(`SELECT count(*) FROM npm_instances`).Scan(&n); err != nil || n != 1 {
		t.Errorf("%d instance rows (error %v), want 1", n, err)
	}
	for range 2 {
		if err := h.db.Write(ctx, func(tx *sql.Tx) error { return deleteInstance(ctx, tx) }); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok, _ := loadInstance(ctx, h.db.Read()); ok {
		t.Error("the instance survived deleteInstance")
	}
}

func TestSdReservedRows(t *testing.T) {
	h := newTHHost(t)
	ctx := context.Background()
	rows := []reservedRow{
		{Kind: "proxy_host", ObjectID: 7, Owner: "npm.admin.expose", Exposure: "npm.example.org", ChangeID: "c1"},
		{Kind: "certificate", ObjectID: 7, Owner: "npm.admin.expose", Exposure: "npm.example.org", ChangeID: "c1"},
		{Kind: "proxy_host", ObjectID: 8, Owner: "panel.exposure.add", Exposure: "panel.example.org", ChangeID: "c2"},
	}
	if err := h.db.Write(ctx, func(tx *sql.Tx) error {
		for _, r := range rows {
			if err := addReserved(ctx, tx, r); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		got, ok, err := reservedOf(ctx, h.db.Read(), r.Kind, r.ObjectID)
		if err != nil || !ok || got != r {
			t.Errorf("reservedOf(%s, %d) = %+v, %v, %v; want %+v", r.Kind, r.ObjectID, got, ok, err, r)
		}
	}
	if _, ok, err := reservedOf(ctx, h.db.Read(), "access_list", 7); err != nil || ok {
		t.Errorf("an unreserved object: ok %v, error %v", ok, err)
	}
	if err := h.db.Write(ctx, func(tx *sql.Tx) error { return addReserved(ctx, tx, rows[0]) }); err == nil {
		t.Error("an object reserved twice was accepted")
	}
	if err := h.db.Write(ctx, func(tx *sql.Tx) error { return proxyHostEditRelease(ctx, tx, "npm.example.org", "npm.admin.expose") }); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		kind string
		id   int
		held bool
	}{{"proxy_host", 7, false}, {"certificate", 7, false}, {"proxy_host", 8, true}} {
		if _, ok, _ := reservedOf(ctx, h.db.Read(), c.kind, c.id); ok != c.held {
			t.Errorf("%s %d reserved = %v after releasing npm.example.org, want %v", c.kind, c.id, ok, c.held)
		}
	}
	if err := h.db.Write(ctx, func(tx *sql.Tx) error { return proxyHostEditRelease(ctx, tx, "nobody.example.org", "npm.admin.expose") }); err != nil {
		t.Errorf("releasing an exposure with no rows: %v", err)
	}
}

func TestSdClaims(t *testing.T) {
	h := newTHHost(t)
	ctx := context.Background()
	all := []claimRow{
		{Network: "net2", Claimant: "a", ChangeID: "c1"},
		{Network: "net1", Claimant: "b", ChangeID: "c2", Preexisting: true},
		{Network: "net1", Claimant: "a", ChangeID: "c3"},
	}
	if err := h.db.Write(ctx, func(tx *sql.Tx) error {
		for _, c := range all {
			if err := addClaim(ctx, tx, c); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.db.Write(ctx, func(tx *sql.Tx) error { return addClaim(ctx, tx, all[0]) }); err == nil {
		t.Error("a claimant claiming a network twice was accepted")
	}
	got, err := claims(ctx, h.db.Read(), "net1")
	want := []claimRow{all[2], all[1]}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("claims(net1) = %+v, %v; want %+v by claimant", got, err, want)
	}
	got, err = claims(ctx, h.db.Read(), "")
	want = []claimRow{all[2], all[1], all[0]}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("claims(\"\") = %+v, %v; want %+v by network and claimant", got, err, want)
	}
	if got, err := claims(ctx, h.db.Read(), "net3"); err != nil || len(got) != 0 {
		t.Errorf("claims(net3) = %+v, %v; want none", got, err)
	}
	for range 2 {
		if err := h.db.Write(ctx, func(tx *sql.Tx) error { return dropClaim(ctx, tx, "net1", "a") }); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := claims(ctx, h.db.Read(), "net1"); len(got) != 1 || got[0].Claimant != "b" {
		t.Errorf("claims(net1) after dropping a = %+v, want only b", got)
	}
}

func TestSdUpgradeRow(t *testing.T) {
	h := newTHHost(t)
	ctx := context.Background()
	cfg := containerConfig{
		Create: dockerapi.ContainerCreate{
			ContainerConfig: dockerapi.ContainerConfig{Image: "img1", Labels: map[string]string{"darkpanel.module": "npm"}, ExposedPorts: map[string]struct{}{"80/tcp": {}}},
			HostConfig: &dockerapi.HostConfig{
				NetworkMode:  "darkpanel-npm",
				PortBindings: map[string][]dockerapi.PortBinding{"81/tcp": {{HostIP: "127.0.0.1", HostPort: "81"}}},
				Mounts:       []dockerapi.Mount{{Type: "volume", Source: "darkpanel-npm-data", Target: "/data"}},
			},
		},
		Name: "darkpanel-npm",
		Networks: map[string]*dockerapi.EndpointSettings{
			"app": {IPAMConfig: &dockerapi.EndpointIPAMConfig{IPv4Address: netip.MustParseAddr("172.31.255.2")}, Aliases: []string{"npm"}},
		},
	}
	want := upgradeRow{ChangeID: "c1", FromImage: "img1", ToImage: "img2", Config: cfg, Phase: "recorded", UpdatedAt: thStart}
	if _, ok, err := loadUpgrade(ctx, h.db.Read(), "c1"); err != nil || ok {
		t.Fatalf("empty store: ok %v, error %v", ok, err)
	}
	if err := insertUpgrade(ctx, h.db, want); err != nil {
		t.Fatal(err)
	}
	if err := insertUpgrade(ctx, h.db, want); err == nil {
		t.Error("a second upgrade of one change was accepted")
	}
	got, ok, err := loadUpgrade(ctx, h.db.Read(), "c1")
	if err != nil || !ok {
		t.Fatalf("load: ok %v, error %v", ok, err)
	}
	if !reflect.DeepEqual(got.Config, cfg) || got.FromImage != "img1" || got.ToImage != "img2" || got.Phase != "recorded" || got.Result != "" ||
		len(got.Archives) != 0 || !got.UpdatedAt.Equal(thStart) {
		t.Errorf("loaded %+v, want %+v", got, want)
	}

	archives := []archiveRef{{Volume: "darkpanel-npm-data", File: "c1-data.tar", SHA256: strings.Repeat("a", 64)}}
	later := thStart.Add(time.Minute)
	if err := setUpgradePhase(ctx, h.db, "c1", "archived", "", archives, later); err != nil {
		t.Fatal(err)
	}
	got, _, _ = loadUpgrade(ctx, h.db.Read(), "c1")
	if got.Phase != "archived" || !reflect.DeepEqual(got.Archives, archives) || !got.UpdatedAt.Equal(later) {
		t.Errorf("after archiving %+v, want phase archived with the archives", got)
	}
	if err := setUpgradePhase(ctx, h.db, "c1", "failed", "the API never came up", nil, later); err != nil {
		t.Fatal(err)
	}
	got, _, _ = loadUpgrade(ctx, h.db.Read(), "c1")
	if got.Phase != "failed" || got.Result != "the API never came up" || !reflect.DeepEqual(got.Archives, archives) {
		t.Errorf("after failing %+v, want the archives kept by a nil argument", got)
	}
	if err := setUpgradePhase(ctx, h.db, "c1", "rolled_back", "", []archiveRef{}, later); err != nil {
		t.Fatal(err)
	}
	got, _, _ = loadUpgrade(ctx, h.db.Read(), "c1")
	if len(got.Archives) != 0 {
		t.Errorf("archives %+v after an empty replacement, want none", got.Archives)
	}
	if err := setUpgradePhase(ctx, h.db, "nobody", "done", "", nil, later); err == nil {
		t.Error("setUpgradePhase of an unknown change succeeded")
	}
	if err := setUpgradePhase(ctx, h.db, "c1", "sideways", "", nil, later); err == nil {
		t.Error("setUpgradePhase accepted an unknown phase")
	}
}

func TestSdWatch(t *testing.T) {
	h := newTHHost(t)
	ctx := context.Background()
	add := func(kind, identity string, seq int) {
		t.Helper()
		if err := h.db.Write(ctx, func(tx *sql.Tx) error { return addWatch(ctx, tx, kind, identity, "c1", seq) }); err != nil {
			t.Fatal(err)
		}
	}
	add("access_list", "darkpanel-c1-3", 3)
	add("access_list", "darkpanel-c1-3", 3)
	add("certificate", "a.example.org", 4)
	got, err := watched(ctx, h.db.Read(), "access_list")
	if err != nil || !reflect.DeepEqual(got, map[string]bool{"darkpanel-c1-3": true}) {
		t.Errorf("watched(access_list) = %v, %v", got, err)
	}
	if got, err := watched(ctx, h.db.Read(), "proxy_host"); err != nil || got == nil || len(got) != 0 {
		t.Errorf("watched(proxy_host) = %#v, %v; want an empty, non-nil map", got, err)
	}
}
