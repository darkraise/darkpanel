package cloudflare

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/cloudflare/fake"
)

// trRig is a connected account, a fake Cloudflare account and the module under test.
type trRig struct {
	h   *cfHost
	srv *fake.Server
	c   cf
}

func trNew(t *testing.T) trRig {
	t.Helper()
	h := newCFHost(t)
	srv := fake.New(t)
	acConnect(t, h, srv)
	return trRig{h: h, srv: srv, c: cfWith(srv)}
}

// trTunnel creates a tunnel in the fake and records its row, as tunnel.create leaves them.
func trTunnel(t *testing.T, h *cfHost, srv *fake.Server, name string) tunnelRow {
	t.Helper()
	row := tunnelRow{ID: srv.AddTunnel(name), Name: name, ChangeID: "tr-seed", CreatedAt: h.clock.Now()}
	trInsertRow(t, h, row)
	return row
}

func trInsertRow(t *testing.T, h *cfHost, row tunnelRow) {
	t.Helper()
	err := h.db.Write(context.Background(), func(tx *sql.Tx) error { return insertTunnel(context.Background(), tx, row) })
	if err != nil {
		t.Fatal(err)
	}
}

func trWrite(t *testing.T, h *cfHost, fn func(ctx context.Context, tx *sql.Tx) error) {
	t.Helper()
	ctx := context.Background()
	if err := h.db.Write(ctx, func(tx *sql.Tx) error { return fn(ctx, tx) }); err != nil {
		t.Fatal(err)
	}
}

func trInput(tunnel string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"tunnel":%q}`, tunnel))
}

func trCode(err error) errcode.Code {
	if err == nil {
		return ""
	}
	return errcode.From(err).Code
}

func trSameJSON(t *testing.T, what string, got json.RawMessage, want string) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("%s: %q is not JSON: %v", what, got, err)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Errorf("%s = %s, want %s", what, got, want)
	}
}

func (r trRig) list(t *testing.T) TunnelList {
	t.Helper()
	raw, err := r.c.tunnelList().Handler.Read(context.Background(), r.h.env(""), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var out TunnelList
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestTrList(t *testing.T) {
	r := trNew(t)
	a := trTunnel(t, r.h, r.srv, "darkpanel-a")
	b := trTunnel(t, r.h, r.srv, "darkpanel-b")
	gone := tunnelRow{ID: "tun-gone", Name: "darkpanel-c", ChangeID: "tr-seed", CreatedAt: r.h.clock.Now()}
	trInsertRow(t, r.h, gone)
	r.srv.SetConnections(a.ID, 2)
	trWrite(t, r.h, func(ctx context.Context, tx *sql.Tx) error {
		return setTunnelCheck(ctx, tx, b.ID, true, r.h.clock.Now())
	})
	trWrite(t, r.h, func(ctx context.Context, tx *sql.Tx) error {
		return insertHostname(ctx, tx, hostnameRow{
			Hostname: "app.example.com", TunnelID: a.ID, Service: "http://127.0.0.1:8080", ZoneID: "z1", RecordID: "r1",
			Phase: "active", ChangeID: "tr-host", Health: "unknown", UpdatedAt: r.h.clock.Now(),
		})
	})

	out := r.list(t)
	if len(out.Tunnels) != 3 {
		t.Fatalf("%d tunnels, want 3: %+v", len(out.Tunnels), out.Tunnels)
	}
	va, vb, vc := out.Tunnels[0], out.Tunnels[1], out.Tunnels[2]
	if va.ID != a.ID || va.Name != "darkpanel-a" || va.Status != "healthy" || len(va.Connections) != 2 || va.Hostnames != 1 || va.Drift {
		t.Errorf("tunnel a: %+v", va)
	}
	if vb.ID != b.ID || vb.Status != "inactive" || len(vb.Connections) != 0 || vb.Hostnames != 0 || !vb.Drift {
		t.Errorf("tunnel b: %+v", vb)
	}
	if vc.ID != gone.ID || vc.Status != "unknown" || len(vc.Connections) != 0 || vc.Hostnames != 0 {
		t.Errorf("tunnel c, which Cloudflare no longer has: %+v", vc)
	}
	if !va.CreatedAt.Equal(a.CreatedAt) {
		t.Errorf("created at %v, want %v", va.CreatedAt, a.CreatedAt)
	}
	enc, err := json.Marshal(out)
	if err != nil || strings.Count(string(enc), `"connections":[]`) != 2 {
		t.Errorf("encoded list %s (err %v), want an empty array for each tunnel without connections", enc, err)
	}
}

func TestTrListWithoutAccountOrTunnels(t *testing.T) {
	r := trNew(t)
	if out := r.list(t); out.Tunnels == nil || len(out.Tunnels) != 0 {
		t.Errorf("no tunnels: %+v, want an empty non-nil list", out)
	}
	bare := newCFHost(t)
	trInsertRow(t, bare, tunnelRow{ID: "tun-1", Name: "darkpanel-a", ChangeID: "tr-seed", CreatedAt: bare.clock.Now().Add(-time.Hour)})
	raw, err := r.c.tunnelList().Handler.Read(context.Background(), bare.env(""), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var out TunnelList
	if err := json.Unmarshal(raw, &out); err != nil || len(out.Tunnels) != 1 || out.Tunnels[0].Status != "unknown" {
		t.Errorf("without an account: %+v (err %v), want the stored tunnel with status unknown", out, err)
	}
}

func TestTrReadDeclarations(t *testing.T) {
	r := trNew(t)
	list := r.c.tunnelList().Spec
	if list.Name != "cloudflare.tunnel.list" || list.Version != 1 || list.Class != core.ClassRead || list.Side != core.SideController ||
		len(list.LockKeys) != 0 || list.Internal {
		t.Errorf("list spec %+v", list)
	}
	tok := r.c.tunnelToken()
	if s := tok.Spec; s.Name != "cloudflare.tunnel.token" || s.Version != 1 || s.Class != core.ClassRead || s.Side != core.SideController ||
		!s.Internal || len(s.LockKeys) != 0 {
		t.Errorf("token spec %+v", s)
	}
	if tok.Handler.Reveal == nil || tok.Handler.Read != nil {
		t.Error("the token action has a Reveal handler and no Read handler")
	}
}

func TestTrToken(t *testing.T) {
	r := trNew(t)
	row := trTunnel(t, r.h, r.srv, "darkpanel-a")
	a, _, err := r.c.connected(context.Background(), r.h.db, r.h.env("").Sealer, r.h.clock)
	if err != nil {
		t.Fatal(err)
	}
	want, err := a.TunnelToken(context.Background(), row.ID)
	if err != nil || want == "" {
		t.Fatalf("provider token %q (err %v)", want, err)
	}
	def := r.c.tunnelToken()
	for _, ref := range []string{row.ID, row.Name} {
		identity, value, err := def.Handler.Reveal(context.Background(), r.h.env(""), trInput(ref))
		if err != nil {
			t.Fatalf("reveal by %q: %v", ref, err)
		}
		trSameJSON(t, "value", value, fmt.Sprintf(`{"token":%q}`, want))
		if !strings.Contains(identity, row.Name) || strings.Contains(identity, want) {
			t.Errorf("identity %q must name the tunnel and not hold the token", identity)
		}
	}
	for _, tc := range []struct {
		name   string
		env    core.Env
		tunnel string
		want   errcode.Code
	}{
		{"empty", r.h.env(""), "", errcode.InvalidInput},
		{"unmanaged", r.h.env(""), "darkpanel-other", errcode.NotFound},
	} {
		if _, _, err := def.Handler.Reveal(context.Background(), tc.env, trInput(tc.tunnel)); trCode(err) != tc.want {
			t.Errorf("%s: error %v, want %s", tc.name, err, tc.want)
		}
	}
	bare := newCFHost(t)
	trInsertRow(t, bare, tunnelRow{ID: row.ID, Name: row.Name, ChangeID: "tr-seed", CreatedAt: bare.clock.Now()})
	if _, _, err := def.Handler.Reveal(context.Background(), bare.env(""), trInput(row.ID)); trCode(err) != errcode.CloudNotConnected {
		t.Errorf("a stored tunnel without an account: error %v, want cloud.not_connected", err)
	}
}
