// Package backup is the controller's backup, restore and restore-quarantine
// surface (spec 10). Its routes and services reach the controller through
// Contribute, which the assembly calls at startup beside the modules'
// controller contributions; the routes are served on the controller socket.
package backup

import "github.com/darkraise/darkpanel/internal/core/ext"

// Contribute returns the backup and quarantine routes and services. It
// contributes nothing until the backup and restore commands exist.
func Contribute(_ ext.Deps) ([]ext.Route, []ext.Service, error) {
	return nil, nil, nil
}
