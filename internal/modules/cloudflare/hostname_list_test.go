package cloudflare

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/modules/cloudflare/fake"
)

var hsReader = core.Principal{Actor: "user:u2", UserID: "u2", Scope: core.ScopeRead, Source: "203.0.113.8"}

// hsModule serves the hostname list action under the cloudflare module id.
type hsModule struct{ c cf }

func (m hsModule) Manifest() core.Manifest {
	return core.Manifest{ID: ID, Version: 1, Actions: []core.ActionDef{m.c.hostnameList()}}
}

// hsList runs the hostname list action as a reader and decodes its output.
func hsList(t *testing.T, h *cfHost, c cf) (HostnameList, string) {
	t.Helper()
	reg, err := core.NewRegistry(hsModule{c})
	if err != nil {
		t.Fatal(err)
	}
	eng, err := lifecycle.New(lifecycle.Config{
		Side: core.SideController, Registry: reg, Validator: schema.NewValidator(), Journal: &coretest.MemJournal{},
		Redact: schema.Redact,
		Base:   core.Env{DB: h.db, Sealer: coretest.Sealer{}, Clock: h.clock, Log: slog.New(slog.DiscardHandler)},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(eng.Close)
	raw, err := eng.Read(context.Background(), hsReader, core.ActionRef{Name: "cloudflare.hostname.list", Version: 1}, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var out HostnameList
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out, string(raw)
}

func TestHSListReturnsEveryRowSortedByHostname(t *testing.T) {
	h, srv := newCFHost(t), fake.New(t)
	b := haAdd(t, h, srv, "b.example.com")
	a := haAdd(t, h, srv, "a.example.com")
	ctx := context.Background()
	checked := h.clock.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	mid := hostnameRow{Hostname: "mid.example.com", TunnelID: a.TunnelID, Service: "http://10.0.0.9:80", ZoneID: a.ZoneID,
		Phase: "removing_rule", ChangeID: "01HMID", Health: "unknown", UpdatedAt: h.clock.Now()}
	err := h.db.Write(ctx, func(tx *sql.Tx) error {
		if err := insertHostname(ctx, tx, mid); err != nil {
			return err
		}
		return setHostnameHealth(ctx, tx, "a.example.com", "record_missing", checked)
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := hsList(t, h, cfWith(srv))
	if len(got.Hostnames) != 3 {
		t.Fatalf("hostnames = %+v, want three rows", got.Hostnames)
	}
	names := got.Hostnames[0].Hostname + " " + got.Hostnames[1].Hostname + " " + got.Hostnames[2].Hostname
	if names != "a.example.com b.example.com mid.example.com" {
		t.Errorf("order = %q, want it sorted by hostname", names)
	}
	ga, gb, gm := got.Hostnames[0], got.Hostnames[1], got.Hostnames[2]
	if ga.Tunnel != a.TunnelID || ga.Service != a.Service || ga.RecordID != a.RecordID || ga.RecordID == "" || ga.Phase != "active" {
		t.Errorf("row a = %+v, want the stored tunnel, service, record and the active phase", ga)
	}
	if ga.Health != "record_missing" || ga.CheckedAt == nil || !ga.CheckedAt.Equal(checked) {
		t.Errorf("row a health = %q checked at %v, want record_missing at %v", ga.Health, ga.CheckedAt, checked)
	}
	if gb.RecordID != b.RecordID {
		t.Errorf("row b record = %q, want %q", gb.RecordID, b.RecordID)
	}
	if gm.Phase != "removing_rule" || gm.Health != "unknown" || gm.CheckedAt != nil || gm.RecordID != "" {
		t.Errorf("row mid = %+v, want its phase, unknown health, no check time and no record", gm)
	}
}

func TestHSListWithoutRowsIsAnEmptyArray(t *testing.T) {
	h, srv := newCFHost(t), fake.New(t)
	out, raw := hsList(t, h, cfWith(srv))
	if len(out.Hostnames) != 0 || !strings.Contains(raw, `"hostnames":[]`) {
		t.Errorf("output = %s, want an empty hostnames array", raw)
	}
}
