package cloudflare

import (
	"context"
	"testing"

	"github.com/darkraise/darkpanel/internal/controller/audit"
	"github.com/darkraise/darkpanel/internal/core"
)

// TestW10AuHostnameCheckWritesThroughTheAuditStore runs the check against the
// controller's own audit store on a migrated database, which refuses a phase
// outside its CHECK list, as the in-memory audit of the other tests does not.
func TestW10AuHostnameCheckWritesThroughTheAuditStore(t *testing.T) {
	f := newHSFixture(t)
	log := audit.New(f.h.db, f.h.clock)
	f.deps.Audit = log
	f.pass()
	got, _, err := log.List(context.Background(), core.AuditQuery{Action: hostnamesService})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("audit rows = %+v, want one per hostname whose health changed", got)
	}
	for _, e := range got {
		if e.Actor != hostnamesActor || e.Phase != "check" || e.Result != "ok" {
			t.Errorf("audit row %+v, want actor %s, phase check and result ok", e, hostnamesActor)
		}
	}
}
