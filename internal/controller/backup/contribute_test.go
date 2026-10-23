package backup

import (
	"testing"

	"github.com/darkraise/darkpanel/internal/core/ext"
)

// TestContributeAddsNothingYet: until the backup routes exist the
// contribution mounts no route and starts no service.
func TestContributeAddsNothingYet(t *testing.T) {
	routes, services, err := Contribute(ext.Deps{})
	if err != nil || len(routes) != 0 || len(services) != 0 {
		t.Errorf("Contribute = %v, %v, %v; want nothing", routes, services, err)
	}
}
