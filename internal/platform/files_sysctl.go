package platform

import (
	"context"
	"fmt"
)

// ReadSysctl returns the value of a declared kernel parameter.
func (f *Files) ReadSysctl(context.Context, string, string) (string, error) {
	return "", fmt.Errorf("platform: %s is not built yet", "ReadSysctl")
}

// WriteSysctl sets a declared kernel parameter.
func (f *Files) WriteSysctl(context.Context, string, string, string) error {
	return fmt.Errorf("platform: %s is not built yet", "WriteSysctl")
}
