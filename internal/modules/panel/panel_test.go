package panel

import (
	"reflect"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

// TestManifestIsASkeleton: until the module is built it is registered,
// enabled by default and declares nothing.
func TestManifestIsASkeleton(t *testing.T) {
	want := core.Manifest{ID: ID, Version: 1, DefaultEnabled: true}
	if got := Module().Manifest(); !reflect.DeepEqual(got, want) {
		t.Errorf("manifest %+v, want %+v", got, want)
	}
}
