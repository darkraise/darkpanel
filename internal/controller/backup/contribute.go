// Package backup is the controller's backup, restore and restore-quarantine
// surface (spec 10). Its routes reach the controller through
// Backup.Contribute, which the assembly calls at startup beside the modules'
// controller contributions; the routes are served on the controller socket.
package backup

import (
	"context"
	"database/sql"
	"net/http"
	"path/filepath"
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

	// afterCopy is a test seam: it runs once the temporary copy of the
	// database exists and before the copy is opened, so a test can swap the
	// file. It is always nil in production.
	afterCopy func(path string)
}

// New returns a Backup over cfg with no backup running.
func New(cfg Config) *Backup {
	return &Backup{cfg: cfg}
}

// Contribute returns the five backup and quarantine routes, all on the
// controller socket, and starts no service. Only the backup route streams.
// It also removes the temporary database copies a crashed backup left beside
// the database.
func (b *Backup) Contribute(deps ext.Deps) ([]ext.Route, []ext.Service, error) {
	if deps.DB != nil {
		sweepCopies(filepath.Dir(deps.DB.Path()), deps.Log)
	}
	return []ext.Route{
		{
			Surface: ext.SurfaceSocket, Method: http.MethodPost, Path: "/api/v1/controller/backup", Stream: true,
			Summary: "Stream an encrypted backup of the controller database and key",
			Handler: b.backupHandler(deps),
		},
		{
			Surface: ext.SurfaceSocket, Method: http.MethodGet, Path: "/api/v1/controller/quarantine",
			Summary: "List the records a restore quarantined",
			Handler: b.quarantineHandler(deps),
		},
		{
			Surface: ext.SurfaceSocket, Method: http.MethodPost, Path: "/api/v1/controller/quarantine/{record}/resolve",
			Summary: "Resolve one quarantined record",
			Handler: b.resolveHandler(deps),
		},
		{
			Surface: ext.SurfaceSocket, Method: http.MethodPost, Path: "/api/v1/controller/quarantine/enroll-token",
			Summary: "Issue an enrollment token for a restored host",
			Handler: b.enrollTokenHandler(deps),
		},
		{
			Surface: ext.SurfaceSocket, Method: http.MethodPost, Path: "/api/v1/controller/quarantine/release",
			Summary: "Release the restore quarantine",
			Handler: b.releaseHandler(deps),
		},
	}, nil, nil
}
