package tailnet

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/audit"
	"github.com/darkraise/darkpanel/internal/core"
)

// TestW10AuKeySweepWritesThroughTheAuditStore runs a sweep against the
// controller's own audit store on a migrated database, which refuses a phase
// outside its CHECK list, as the in-memory audit of the other tests does not.
func TestW10AuKeySweepWritesThroughTheAuditStore(t *testing.T) {
	ctx := context.Background()
	f := swSetup(t)
	log := audit.New(f.h.db, f.h.clock)
	f.deps.Audit = log
	description := "darkpanel-" + swChange(1)
	id := f.key(t, description)
	f.row(t, description, "retired", "", time.Hour)
	if err := f.t.sweep(ctx, f.deps, f.h.clock.Now(), false); err != nil {
		t.Fatal(err)
	}
	if f.alive(id) {
		t.Error("the sweep left the key alive")
	}
	got, _, err := log.List(ctx, core.AuditQuery{Actor: keySweepActor})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("audit rows = %+v, want the one of the deleted key", got)
	}
	var in map[string]string
	if err := json.Unmarshal(got[0].Input, &in); err != nil {
		t.Fatal(err)
	}
	if e := got[0]; e.Action != keySweepService || e.Phase != "delete" || e.Result != "ok" || in["keyId"] != id {
		t.Errorf("audit row %+v input %s, want phase delete, result ok and key %s", e, e.Input, id)
	}
}
