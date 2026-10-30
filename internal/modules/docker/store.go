package docker

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/modules/docker/compose"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

//go:embed migrations/agent/*.sql
var migrationsFS embed.FS

// agentMigrations returns the module's agent-side migrations, the value of
// Manifest.Migrations[core.SideAgent].
func agentMigrations() fs.FS {
	sub, err := fs.Sub(migrationsFS, "migrations/agent")
	if err != nil {
		// fs.Sub fails only for an invalid path, and this one is a constant.
		panic(err)
	}
	return sub
}

// projectTimeFormat is the fixed-width form of every stored time, so times
// compare as text.
const projectTimeFormat = "2006-01-02T15:04:05.000000000Z"

const (
	projectLabelName     = "com.docker.compose.project"
	projectLabelDir      = "com.docker.compose.project.working_dir"
	projectLabelFiles    = "com.docker.compose.project.config_files"
	projectLabelEnvFiles = "com.docker.compose.project.environment_file"
)

const projectColumns = "name, directory, files_json, env_files_json, profiles_json, run_hash, first_seen, last_run"

// projectRow is one docker_projects row.
type projectRow struct {
	Name, Directory           string
	Files, EnvFiles, Profiles []string
	RunHash                   string
	FirstSeen                 time.Time
	LastRun                   time.Time // zero before the first run
}

// discoverProjects records every project the containers' compose labels name
// that has no row yet; it never deletes or updates a row.
func discoverProjects(ctx context.Context, db *store.DB, now time.Time, cs []dockerapi.ContainerSummary) error {
	err := db.Write(ctx, func(tx *sql.Tx) error {
		seen := map[string]bool{}
		for _, c := range cs {
			name := c.Labels[projectLabelName]
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			files, err := projectJSON(projectSplit(c.Labels[projectLabelFiles]))
			if err != nil {
				return err
			}
			envFiles, err := projectJSON(projectSplit(c.Labels[projectLabelEnvFiles]))
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx,
				"INSERT OR IGNORE INTO docker_projects (name, directory, files_json, env_files_json, profiles_json, run_hash, first_seen) VALUES (?, ?, ?, ?, '[]', '', ?)",
				name, c.Labels[projectLabelDir], files, envFiles, now.UTC().Format(projectTimeFormat)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("docker: record projects: %w", err)
	}
	return nil
}

// loadProject returns the row of project name; ok is false when there is none.
func loadProject(ctx context.Context, db *store.DB, name string) (projectRow, bool, error) {
	p, err := projectScan(db.Read().QueryRowContext(ctx, "SELECT "+projectColumns+" FROM docker_projects WHERE name = ?", name))
	if errors.Is(err, sql.ErrNoRows) {
		return projectRow{}, false, nil
	}
	if err != nil {
		return projectRow{}, false, fmt.Errorf("docker: read project %s: %w", name, err)
	}
	return p, true, nil
}

// listProjects returns every recorded project ordered by name.
func listProjects(ctx context.Context, db *store.DB) ([]projectRow, error) {
	rows, err := db.Read().QueryContext(ctx, "SELECT "+projectColumns+" FROM docker_projects ORDER BY name")
	if err != nil {
		return nil, fmt.Errorf("docker: list projects: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []projectRow
	for rows.Next() {
		p, err := projectScan(rows)
		if err != nil {
			return nil, fmt.Errorf("docker: list projects: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("docker: list projects: %w", err)
	}
	return out, nil
}

// setProjectRun records a run of project name: its profiles, the hash of its
// configuration and the time of the run.
func setProjectRun(ctx context.Context, tx *sql.Tx, name string, profiles []string, runHash string, at time.Time) error {
	if profiles == nil {
		profiles = []string{}
	}
	stored, err := projectJSON(profiles)
	if err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, "UPDATE docker_projects SET profiles_json = ?, run_hash = ?, last_run = ? WHERE name = ?",
		stored, runHash, at.UTC().Format(projectTimeFormat), name)
	if err != nil {
		return fmt.Errorf("docker: record run of project %s: %w", name, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("docker: record run of project %s: %w", name, err)
	}
	if n == 0 {
		return fmt.Errorf("docker: record run of project %s: no such project", name)
	}
	return nil
}

// configHash is the hex SHA-256 over each file's and env file's path and
// bytes, in order; "" when one cannot be read.
func (k dk) configHash(p projectRow) string {
	h := sha256.New()
	for _, path := range append(append([]string{}, p.Files...), p.EnvFiles...) {
		// The paths come from container labels, so a FIFO, a device or a
		// huge file there must not stall or exhaust the read.
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return ""
		}
		info, err := k.d.lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() > compose.MaxConfigBytes {
			return ""
		}
		data, err := k.d.readFile(path)
		if err != nil || len(data) > compose.MaxConfigBytes {
			return ""
		}
		// Each length prefix ends one field, so a byte moved from one file
		// to the next changes the hash.
		_, _ = h.Write(binary.BigEndian.AppendUint64(nil, uint64(len(path))))
		_, _ = h.Write([]byte(path))
		_, _ = h.Write(binary.BigEndian.AppendUint64(nil, uint64(len(data))))
		_, _ = h.Write(data)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func projectScan(row interface{ Scan(dest ...any) error }) (projectRow, error) {
	var (
		p                         projectRow
		files, envFiles, profiles string
		firstSeen                 string
		lastRun                   sql.NullString
	)
	if err := row.Scan(&p.Name, &p.Directory, &files, &envFiles, &profiles, &p.RunHash, &firstSeen, &lastRun); err != nil {
		return p, err
	}
	var err error
	if p.Files, err = projectStrings(files); err != nil {
		return p, fmt.Errorf("project %s: files: %w", p.Name, err)
	}
	if p.EnvFiles, err = projectStrings(envFiles); err != nil {
		return p, fmt.Errorf("project %s: env files: %w", p.Name, err)
	}
	if p.Profiles, err = projectStrings(profiles); err != nil {
		return p, fmt.Errorf("project %s: profiles: %w", p.Name, err)
	}
	if p.FirstSeen, err = time.Parse(projectTimeFormat, firstSeen); err != nil {
		return p, fmt.Errorf("project %s: first seen: %w", p.Name, err)
	}
	if lastRun.Valid {
		if p.LastRun, err = time.Parse(projectTimeFormat, lastRun.String); err != nil {
			return p, fmt.Errorf("project %s: last run: %w", p.Name, err)
		}
	}
	return p, nil
}

// projectStrings decodes a stored JSON array; it is never nil.
func projectStrings(stored string) ([]string, error) {
	out := []string{}
	if err := json.Unmarshal([]byte(stored), &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []string{}
	}
	return out, nil
}

func projectJSON(values []string) (string, error) {
	data, err := json.Marshal(values)
	if err != nil {
		return "", fmt.Errorf("docker: encode project list: %w", err)
	}
	return string(data), nil
}

// projectSplit splits a comma-separated label value, dropping empty parts;
// it is never nil, so the list is stored as [].
func projectSplit(label string) []string {
	out := []string{}
	for part := range strings.SplitSeq(label, ",") {
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
