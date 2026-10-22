package platform

import (
	"context"
	"fmt"

	"github.com/darkraise/darkpanel/internal/core"
)

// Restore writes or removes one file a snapshot restores.
func (f *Files) Restore(context.Context, string, core.RestoreEntry) error {
	return fmt.Errorf("platform: %s is not built yet", "Restore")
}
