package cloudflare

import (
	"context"
	"database/sql"
	"io/fs"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/store"
)

const (
	csTunnelA = "0a1b2c3d-0000-4000-8000-00000000000a"
	csTunnelB = "0a1b2c3d-0000-4000-8000-00000000000b"
)

// csWrite runs fn in a write transaction of the host's agent database and fails the test on its error.
func csWrite(t *testing.T, db *store.DB, fn func(ctx context.Context, tx *sql.Tx) error) {
	t.Helper()
	ctx := context.Background()
	if err := db.Write(ctx, func(tx *sql.Tx) error { return fn(ctx, tx) }); err != nil {
		t.Fatal(err)
	}
}

func csConnector(tunnel, container string, at time.Time) connectorRow {
	return connectorRow{TunnelID: tunnel, ContainerID: container, TokenCT: []byte("sealed-" + tunnel), ChangeID: "01JCHANGE" + tunnel[len(tunnel)-1:], CreatedAt: at}
}

// TestCsConnectors: a connector row keeps every column, including its time to the nanosecond, is found by tunnel and
// by container, is listed in tunnel order, refuses a second row for a tunnel and is deleted by itself.
func TestCsConnectors(t *testing.T) {
	ctx := context.Background()
	h := newAGHost(t)
	db := h.db.Read()
	if rows, err := listConnectors(ctx, db); err != nil || rows == nil || len(rows) != 0 {
		t.Fatalf("listConnectors on an empty table = %#v, %v; want an empty, non-nil slice", rows, err)
	}
	if _, ok, err := loadConnector(ctx, db, csTunnelA); err != nil || ok {
		t.Fatalf("loadConnector on an empty table = %t, %v; want not found", ok, err)
	}
	at := time.Date(2026, 11, 3, 12, 0, 0, 123456789, time.UTC)
	b, a := csConnector(csTunnelB, "cid-b", at.Add(time.Minute)), csConnector(csTunnelA, "cid-a", at)
	csWrite(t, h.db, func(ctx context.Context, tx *sql.Tx) error {
		if err := insertConnector(ctx, tx, b); err != nil {
			return err
		}
		return insertConnector(ctx, tx, a)
	})
	got, ok, err := loadConnector(ctx, db, csTunnelA)
	if err != nil || !ok || !reflect.DeepEqual(got, a) {
		t.Fatalf("loadConnector = %+v, %t, %v; want %+v", got, ok, err, a)
	}
	got, ok, err = connectorByContainer(ctx, db, "cid-b")
	if err != nil || !ok || !reflect.DeepEqual(got, b) {
		t.Fatalf("connectorByContainer = %+v, %t, %v; want %+v", got, ok, err, b)
	}
	if _, ok, err := connectorByContainer(ctx, db, "cid-none"); err != nil || ok {
		t.Errorf("connectorByContainer of an unknown container = %t, %v; want not found", ok, err)
	}
	all, err := listConnectors(ctx, db)
	if err != nil || !reflect.DeepEqual(all, []connectorRow{a, b}) {
		t.Errorf("listConnectors = %+v, %v; want %+v then %+v", all, err, a, b)
	}
	err = h.db.Write(ctx, func(tx *sql.Tx) error { return insertConnector(ctx, tx, csConnector(csTunnelA, "cid-other", at)) })
	if err == nil || !strings.Contains(err.Error(), csTunnelA) {
		t.Errorf("a second connector for one tunnel: error %v, want one naming the tunnel", err)
	}
	csWrite(t, h.db, func(ctx context.Context, tx *sql.Tx) error { return deleteConnector(ctx, tx, csTunnelA) })
	csWrite(t, h.db, func(ctx context.Context, tx *sql.Tx) error { return deleteConnector(ctx, tx, csTunnelA) })
	if _, ok, _ := loadConnector(ctx, db, csTunnelA); ok {
		t.Error("the connector remains after its delete")
	}
	if _, ok, _ := loadConnector(ctx, db, csTunnelB); !ok {
		t.Error("deleting one connector removed another")
	}
}

// TestCsClaims: claims are listed for one network or for all of a tunnel's, ordered by network and claimant, keep
// their preexisting flag, never mix tunnels, refuse a duplicate and are deleted one at a time.
func TestCsClaims(t *testing.T) {
	ctx := context.Background()
	h := newAGHost(t)
	db := h.db.Read()
	claims := []claimRow{
		{TunnelID: csTunnelA, Network: "web", Claimant: "expose:b.example.com", ChangeID: "c2", Preexisting: false},
		{TunnelID: csTunnelA, Network: "api", Claimant: "expose:z.example.com", ChangeID: "c3", Preexisting: true},
		{TunnelID: csTunnelA, Network: "web", Claimant: "expose:a.example.com", ChangeID: "c1", Preexisting: false},
		{TunnelID: csTunnelB, Network: "web", Claimant: "expose:other.example.com", ChangeID: "c4", Preexisting: false},
	}
	csWrite(t, h.db, func(ctx context.Context, tx *sql.Tx) error {
		for _, c := range claims {
			if err := insertClaim(ctx, tx, c); err != nil {
				return err
			}
		}
		return nil
	})
	all, err := connectorClaims(ctx, db, csTunnelA, "")
	want := []claimRow{claims[1], claims[2], claims[0]}
	if err != nil || !reflect.DeepEqual(all, want) {
		t.Fatalf("connectorClaims of every network = %+v, %v; want %+v", all, err, want)
	}
	web, err := connectorClaims(ctx, db, csTunnelA, "web")
	if err != nil || !reflect.DeepEqual(web, []claimRow{claims[2], claims[0]}) {
		t.Errorf("connectorClaims on web = %+v, %v; want the two web claims in claimant order", web, err)
	}
	none, err := connectorClaims(ctx, db, "0a1b2c3d-0000-4000-8000-0000000000ff", "")
	if err != nil || none == nil || len(none) != 0 {
		t.Errorf("connectorClaims of a tunnel without claims = %#v, %v; want an empty, non-nil slice", none, err)
	}
	err = h.db.Write(ctx, func(tx *sql.Tx) error { return insertClaim(ctx, tx, claims[0]) })
	if err == nil || !strings.Contains(err.Error(), claims[0].Claimant) {
		t.Errorf("a duplicate claim: error %v, want one naming the claimant", err)
	}
	csWrite(t, h.db, func(ctx context.Context, tx *sql.Tx) error {
		if err := deleteClaim(ctx, tx, csTunnelA, "web", "expose:a.example.com"); err != nil {
			return err
		}
		return deleteClaim(ctx, tx, csTunnelA, "web", "expose:nobody.example.com")
	})
	left, _ := connectorClaims(ctx, db, csTunnelA, "")
	if !reflect.DeepEqual(left, []claimRow{claims[1], claims[0]}) {
		t.Errorf("claims after the delete = %+v, want the two others of the tunnel", left)
	}
	if other, _ := connectorClaims(ctx, db, csTunnelB, ""); len(other) != 1 {
		t.Errorf("the other tunnel's claims = %+v, want its one claim", other)
	}
}

// TestCsMigrationsEnforceTheSchema: the agent migration creates both tables as STRICT tables whose preexisting column
// accepts 0 and 1 only, and the module's two migration sets are separate.
func TestCsMigrationsEnforceTheSchema(t *testing.T) {
	ctx := context.Background()
	h := newAGHost(t)
	err := h.db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO cloudflare_attachments (tunnel_id, network, claimant, change_id, preexisting) VALUES ('t', 'n', 'c', 'x', 2)`)
		return err
	})
	if err == nil {
		t.Error("a preexisting value of 2 was accepted")
	}
	err = h.db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO cloudflare_connectors (tunnel_id, container_id, token_ct, change_id, created_at) VALUES ('t', 'c', 'text, not a blob', 'x', 'y')`)
		return err
	})
	if err == nil {
		t.Error("a text value was accepted in the token_ct blob column of a STRICT table")
	}
	agent, err := fs.ReadFile(agentMigrations(), "0001_cloudflare.sql")
	if err != nil || !strings.Contains(string(agent), "cloudflare_connectors") || strings.Contains(string(agent), "cloudflare_hostnames") {
		t.Errorf("agent 0001_cloudflare.sql = %q, %v; want the migration that creates the connector tables only", agent, err)
	}
	ctl, err := fs.ReadFile(controllerMigrations(), "0001_cloudflare.sql")
	if err != nil || !strings.Contains(string(ctl), "cloudflare_hostnames") || strings.Contains(string(ctl), "cloudflare_connectors") {
		t.Errorf("controller 0001_cloudflare.sql = %q, %v; want the controller tables only", ctl, err)
	}
}

// TestCsTokenSealIsBoundToTheTunnel: the token is sealed with the associated data of its tunnel, so a ciphertext does
// not open under another tunnel's.
func TestCsTokenSealIsBoundToTheTunnel(t *testing.T) {
	if got := connectorSealAD(csTunnelA); got != "cloudflare_connectors.token_ct."+csTunnelA {
		t.Fatalf("connectorSealAD = %q", got)
	}
	sealer := coretest.Sealer{}
	ct, err := sealer.Seal(connectorSealAD(csTunnelA), []byte(agToken(csTunnelA)))
	if err != nil {
		t.Fatal(err)
	}
	if pt, err := sealer.Open(connectorSealAD(csTunnelA), ct); err != nil || string(pt) != agToken(csTunnelA) {
		t.Errorf("open under its own tunnel = %q, %v", pt, err)
	}
	if _, err := sealer.Open(connectorSealAD(csTunnelB), ct); err == nil {
		t.Error("a token sealed for one tunnel opened under another tunnel's associated data")
	}
}
