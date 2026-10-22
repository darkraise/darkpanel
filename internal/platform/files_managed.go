package platform

import (
	"context"
	"fmt"
	"io/fs"
)

// WriteManaged writes one file directly inside a declared managed directory.
func (f *Files) WriteManaged(context.Context, string, string, string, []byte, fs.FileMode, int, int) error {
	return fmt.Errorf("platform: %s is not built yet", "WriteManaged")
}

// RemoveManaged removes one file of a declared managed directory.
func (f *Files) RemoveManaged(context.Context, string, string, string) error {
	return fmt.Errorf("platform: %s is not built yet", "RemoveManaged")
}
