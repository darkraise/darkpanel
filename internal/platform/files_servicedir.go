package platform

import (
	"context"
	"fmt"
)

// EnsureServiceDir creates or checks a declared service-user directory.
func (f *Files) EnsureServiceDir(context.Context, string, string) error {
	return fmt.Errorf("platform: %s is not built yet", "EnsureServiceDir")
}

// RenameIn renames a file inside a declared service-user directory.
func (f *Files) RenameIn(context.Context, string, string, string, string) error {
	return fmt.Errorf("platform: %s is not built yet", "RenameIn")
}

// RemoveIn removes a file inside a declared service-user directory.
func (f *Files) RemoveIn(context.Context, string, string, string) error {
	return fmt.Errorf("platform: %s is not built yet", "RemoveIn")
}

// RemoveServiceDir removes a declared service-user directory.
func (f *Files) RemoveServiceDir(context.Context, string, string) error {
	return fmt.Errorf("platform: %s is not built yet", "RemoveServiceDir")
}
