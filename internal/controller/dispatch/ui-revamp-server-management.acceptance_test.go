package dispatch

import (
	"context"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
)

// A49: the recheck updates available, reason and checked_at of each named
// module and leaves enabled as it is, whichever way availability moves.
func TestA49_RecheckUpdatesAvailabilityReasonAndCheckedAtButNotEnabled(t *testing.T) {
	d, h, agent := newRecheckFixture(t)
	insertModuleRow(t, h) // docker: enabled, unavailable ("daemon not running"), checked 2026-11-19T00:00:00Z
	agent.preflight["docker"] = core.ModuleStatus{ID: "docker", Available: true}
	d.avail.put(availabilityKey{host: testHost, module: "docker"}, availabilityEntry{askedAt: time.Now(), checkedAt: "2026-11-19T00:00:00Z"})
	if err := d.RecheckModules(context.Background(), testHost, []string{"docker"}); err != nil {
		t.Fatal(err)
	}
	enabled, available, reason, checked := moduleRow(t, h, "docker")
	if enabled != 1 || available != 1 || reason != "" || checked == "2026-11-19T00:00:00Z" {
		t.Fatalf("after install: enabled=%d available=%d reason=%q checked=%s", enabled, available, reason, checked)
	}
	agent.preflight["docker"] = core.ModuleStatus{ID: "docker", Available: false, Reason: "Docker Engine is not installed."}
	if err := d.RecheckModules(context.Background(), testHost, []string{"docker"}); err != nil {
		t.Fatal(err)
	}
	enabled, available, reason, _ = moduleRow(t, h, "docker")
	if enabled != 1 || available != 0 || reason != "Docker Engine is not installed." {
		t.Fatalf("after uninstall: enabled=%d available=%d reason=%q", enabled, available, reason)
	}
	if _, ok := d.avail.get(availabilityKey{host: testHost, module: "docker"}); ok {
		t.Fatal("the 60-second availability cache entry was not retired")
	}
}
