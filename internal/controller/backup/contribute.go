// Package backup is the controller's backup, restore and restore-quarantine
// surface (spec 10). Its routes and services reach the controller through
// Contribute, which the assembly calls at startup beside the modules'
// controller contributions; the routes are served on the controller socket.
package backup

import (
	"context"
	"database/sql"
	"sync/atomic"
	"time"

	"github.com/darkraise/darkpanel/internal/core/ext"
)

// TokenIssuer issues single-use enrollment tokens; *fleet.Authority implements it.
type TokenIssuer interface {
	IssueToken(ctx context.Context, tx *sql.Tx, hostID, address string) (string, time.Time, error)
}

// Config is what the assembly gives the backup package beside ext.Deps.
type Config struct {
	KeyFile        string      // the controller key file, paths.Paths.ControllerKey
	Tokens         TokenIssuer // nil: enroll-token is refused
	AgentAdvertise string      // the address enrollment tokens name; "" refuses enroll-token
}

// Backup holds the backup routes' state.
type Backup struct {
	cfg     Config
	running atomic.Bool // true while a backup streams; at most one at a time
}

// New returns a Backup over cfg with no backup running.
func New(cfg Config) *Backup {
	return &Backup{cfg: cfg}
}

// Contribute returns the backup and quarantine routes and services. It
// contributes nothing until the backup and restore commands exist.
func (b *Backup) Contribute(_ ext.Deps) ([]ext.Route, []ext.Service, error) {
	return nil, nil, nil
}

// Contribute returns the backup and quarantine routes and services. It
// contributes nothing until the backup and restore commands exist.
func Contribute(_ ext.Deps) ([]ext.Route, []ext.Service, error) {
	return nil, nil, nil
}
