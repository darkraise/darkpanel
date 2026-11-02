package derp

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/darkraise/darkpanel/internal/core/store"
)

//go:embed migrations/agent/*.sql
var agentMigrationsFS embed.FS

//go:embed migrations/controller/*.sql
var controllerMigrationsFS embed.FS

// agentMigrations returns the module's agent-side migrations, the value of
// Manifest.Migrations[core.SideAgent].
func agentMigrations() fs.FS { return migrationsSub(agentMigrationsFS, "migrations/agent") }

// controllerMigrations returns the module's controller-side migrations, the
// value of Manifest.Migrations[core.SideController].
func controllerMigrations() fs.FS {
	return migrationsSub(controllerMigrationsFS, "migrations/controller")
}

func migrationsSub(fsys embed.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		// fs.Sub fails only for an invalid path, and this one is a constant.
		panic(err)
	}
	return sub
}

// storeTimeFormat is the fixed-width UTC form of every stored time, so times
// compare as text.
const storeTimeFormat = "2006-01-02T15:04:05.000000000Z"

// storeTime is t in the store format; a zero time is stored as ”.
func storeTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(storeTimeFormat)
}

// parseStoreTime reads a time storeTime wrote; ” is the zero time.
func parseStoreTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	return time.Parse(storeTimeFormat, s)
}

// queryer is what a read needs: a database's read handle or a transaction.
type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// rowScanner is a *sql.Row or *sql.Rows.
type rowScanner interface{ Scan(dest ...any) error }

// localServer is the derp_server row: the DERP server installed on this host.
type localServer struct {
	ChangeID, Mode, HostName              string
	VerifyClients                         bool
	DerperVersion, BinarySHA256, CertName string
	CertNotAfter, InstalledAt, UpdatedAt  time.Time
}

const localServerColumns = "change_id, mode, host_name, verify_clients, derper_version, binary_sha256, cert_name, cert_not_after, installed_at, updated_at"

const (
	insertLocalServerSQL = "INSERT INTO derp_server (id, " + localServerColumns + ") VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)"
	loadLocalServerSQL   = "SELECT " + localServerColumns + " FROM derp_server WHERE id = 1"
	setLocalBinarySQL    = "UPDATE derp_server SET derper_version = ?, binary_sha256 = ?, updated_at = ? WHERE id = 1"
	setLocalCertSQL      = "UPDATE derp_server SET cert_name = ?, cert_not_after = ?, updated_at = ? WHERE id = 1"
	deleteLocalServerSQL = "DELETE FROM derp_server WHERE id = 1"
)

// loadLocalServer returns the installed server; ok is false when there is none.
func loadLocalServer(ctx context.Context, q queryer) (localServer, bool, error) {
	var (
		s                            localServer
		notAfter, installed, updated string
	)
	err := q.QueryRowContext(ctx, loadLocalServerSQL).Scan(&s.ChangeID, &s.Mode, &s.HostName, &s.VerifyClients, &s.DerperVersion,
		&s.BinarySHA256, &s.CertName, &notAfter, &installed, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return localServer{}, false, nil
	}
	if err == nil {
		err = errors.Join(scanTime(&s.CertNotAfter, "cert not after", notAfter), scanTime(&s.InstalledAt, "installed at", installed),
			scanTime(&s.UpdatedAt, "updated at", updated))
	}
	if err != nil {
		return localServer{}, false, fmt.Errorf("derp: read the server: %w", err)
	}
	return s, true, nil
}

// insertLocalServer records the installed server; a second server is refused.
func insertLocalServer(ctx context.Context, db *store.DB, s localServer) error {
	err := db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, insertLocalServerSQL, s.ChangeID, s.Mode, s.HostName, s.VerifyClients, s.DerperVersion,
			s.BinarySHA256, s.CertName, storeTime(s.CertNotAfter), storeTime(s.InstalledAt), storeTime(s.UpdatedAt))
		return err
	})
	if err != nil {
		return fmt.Errorf("derp: record the server: %w", err)
	}
	return nil
}

// setLocalBinary records the derper version and binary hash the server runs.
func setLocalBinary(ctx context.Context, db *store.DB, version, sha string, at time.Time) error {
	return storeUpdate(ctx, db, "record the server's binary", setLocalBinarySQL, version, sha, storeTime(at))
}

// setLocalCert records the server's IP certificate pin and expiry.
func setLocalCert(ctx context.Context, db *store.DB, certName string, notAfter, at time.Time) error {
	return storeUpdate(ctx, db, "record the server's certificate", setLocalCertSQL, certName, storeTime(notAfter), storeTime(at))
}

// deleteLocalServer removes the server row; no row is not an error.
func deleteLocalServer(ctx context.Context, db *store.DB) error {
	err := db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, deleteLocalServerSQL)
		return err
	})
	if err != nil {
		return fmt.Errorf("derp: remove the server: %w", err)
	}
	return nil
}

// updateRow is one derp_updates row: a binary update's agent-local transaction.
type updateRow struct {
	ChangeID, FromVersion, FromSHA256, ToVersion, ToSHA256, Phase, Result string
	UpdatedAt                                                             time.Time
}

const updateColumns = "change_id, from_version, from_sha256, to_version, to_sha256, phase, result, updated_at"

const (
	insertUpdateSQL   = "INSERT INTO derp_updates (" + updateColumns + ") VALUES (?, ?, ?, ?, ?, ?, ?, ?)"
	loadUpdateSQL     = "SELECT " + updateColumns + " FROM derp_updates WHERE change_id = ?"
	setUpdatePhaseSQL = "UPDATE derp_updates SET phase = ?, result = ?, updated_at = ? WHERE change_id = ?"
)

// insertUpdate records a new binary update.
func insertUpdate(ctx context.Context, db *store.DB, u updateRow) error {
	err := db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, insertUpdateSQL, u.ChangeID, u.FromVersion, u.FromSHA256, u.ToVersion, u.ToSHA256, u.Phase,
			u.Result, storeTime(u.UpdatedAt))
		return err
	})
	if err != nil {
		return fmt.Errorf("derp: record update %s: %w", u.ChangeID, err)
	}
	return nil
}

// loadUpdate returns the binary update of a change; ok is false when there is none.
func loadUpdate(ctx context.Context, q queryer, changeID string) (updateRow, bool, error) {
	var (
		u       updateRow
		updated string
	)
	err := q.QueryRowContext(ctx, loadUpdateSQL, changeID).Scan(&u.ChangeID, &u.FromVersion, &u.FromSHA256, &u.ToVersion,
		&u.ToSHA256, &u.Phase, &u.Result, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return updateRow{}, false, nil
	}
	if err == nil {
		err = scanTime(&u.UpdatedAt, "updated at", updated)
	}
	if err != nil {
		return updateRow{}, false, fmt.Errorf("derp: read update %s: %w", changeID, err)
	}
	return u, true, nil
}

// setUpdatePhase moves a binary update to phase with result.
func setUpdatePhase(ctx context.Context, db *store.DB, changeID, phase, result string, at time.Time) error {
	return storeUpdate(ctx, db, "set the phase of update "+changeID, setUpdatePhaseSQL, phase, result, storeTime(at), changeID)
}

// serverRecord is one derp_servers row: a host the derp.servers service found running a DERP server.
type serverRecord struct {
	HostID, Mode, HostName              string
	RegionID                            int
	RegionCode, DerperVersion, CertName string
	CertNotAfter                        time.Time
	UnitActive                          bool
	CheckedAt                           time.Time
	Error                               string
}

const serverRecordColumns = "host_id, mode, host_name, region_id, region_code, derper_version, cert_name, cert_not_after, unit_active, checked_at, error"

const (
	loadServerRecordsSQL  = "SELECT " + serverRecordColumns + " FROM derp_servers ORDER BY host_id"
	loadServerRecordSQL   = "SELECT " + serverRecordColumns + " FROM derp_servers WHERE host_id = ?"
	upsertServerRecordSQL = "INSERT INTO derp_servers (" + serverRecordColumns + ") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) " +
		"ON CONFLICT (host_id) DO UPDATE SET mode = excluded.mode, host_name = excluded.host_name, region_id = excluded.region_id, " +
		"region_code = excluded.region_code, derper_version = excluded.derper_version, cert_name = excluded.cert_name, " +
		"cert_not_after = excluded.cert_not_after, unit_active = excluded.unit_active, checked_at = excluded.checked_at, error = excluded.error"
	deleteServerRecordSQL = "DELETE FROM derp_servers WHERE host_id = ?"
)

// loadServerRecords returns every server record, by host id.
func loadServerRecords(ctx context.Context, q queryer) ([]serverRecord, error) {
	rows, err := q.QueryContext(ctx, loadServerRecordsSQL)
	if err != nil {
		return nil, fmt.Errorf("derp: read the server records: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []serverRecord
	for rows.Next() {
		r, err := serverRecordScan(rows)
		if err != nil {
			return nil, fmt.Errorf("derp: read the server records: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("derp: read the server records: %w", err)
	}
	return out, nil
}

// loadServerRecord returns the server record of a host; ok is false when there is none.
func loadServerRecord(ctx context.Context, q queryer, hostID string) (serverRecord, bool, error) {
	r, err := serverRecordScan(q.QueryRowContext(ctx, loadServerRecordSQL, hostID))
	if errors.Is(err, sql.ErrNoRows) {
		return serverRecord{}, false, nil
	}
	if err != nil {
		return serverRecord{}, false, fmt.Errorf("derp: read the server record of %s: %w", hostID, err)
	}
	return r, true, nil
}

func serverRecordScan(row rowScanner) (serverRecord, error) {
	var (
		r                 serverRecord
		notAfter, checked string
	)
	if err := row.Scan(&r.HostID, &r.Mode, &r.HostName, &r.RegionID, &r.RegionCode, &r.DerperVersion, &r.CertName, &notAfter,
		&r.UnitActive, &checked, &r.Error); err != nil {
		return serverRecord{}, err
	}
	if err := errors.Join(scanTime(&r.CertNotAfter, "cert not after", notAfter), scanTime(&r.CheckedAt, "checked at", checked)); err != nil {
		return serverRecord{}, err
	}
	return r, nil
}

// upsertServerRecord records a host's server, replacing the record it had.
func upsertServerRecord(ctx context.Context, db *store.DB, r serverRecord) error {
	err := db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, upsertServerRecordSQL, r.HostID, r.Mode, r.HostName, r.RegionID, r.RegionCode, r.DerperVersion,
			r.CertName, storeTime(r.CertNotAfter), r.UnitActive, storeTime(r.CheckedAt), r.Error)
		return err
	})
	if err != nil {
		return fmt.Errorf("derp: record the server of %s: %w", r.HostID, err)
	}
	return nil
}

// deleteServerRecord removes a host's server record; no row is not an error.
func deleteServerRecord(ctx context.Context, db *store.DB, hostID string) error {
	err := db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, deleteServerRecordSQL, hostID)
		return err
	})
	if err != nil {
		return fmt.Errorf("derp: remove the server record of %s: %w", hostID, err)
	}
	return nil
}

// scanTime parses the stored text of the time called name into dst.
func scanTime(dst *time.Time, name, text string) error {
	t, err := parseStoreTime(text)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	*dst = t
	return nil
}

// storeUpdate runs one UPDATE in a write transaction and fails when it matched no row.
func storeUpdate(ctx context.Context, db *store.DB, what, query string, args ...any) error {
	err := db.Write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return errors.New("no such row")
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("derp: %s: %w", what, err)
	}
	return nil
}
