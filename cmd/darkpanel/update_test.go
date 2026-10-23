package main

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/darkraise/darkpanel/internal/cli"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/install"
	"github.com/darkraise/darkpanel/internal/paths"
	"github.com/darkraise/darkpanel/internal/update"
)

// fakeUpdater records the update commands' calls.
type fakeUpdater struct {
	calls []string
	paths []paths.Paths
}

func (f *fakeUpdater) Run(_ context.Context, tx string) error {
	f.calls = append(f.calls, "run "+tx)
	return nil
}

func (f *fakeUpdater) Recover(_ context.Context, mode update.RecoverMode, tx string) error {
	f.calls = append(f.calls, fmt.Sprintf("recover %d %q", mode, tx))
	return nil
}

func (f *fakeUpdater) RunRollback(_ context.Context, tx string) error {
	f.calls = append(f.calls, "run-rollback "+tx)
	return nil
}

func (f *fakeUpdater) DirectRollback(_ context.Context, force bool) (core.UpdateTransaction, error) {
	f.calls = append(f.calls, fmt.Sprintf("direct-rollback force=%v", force))
	return core.UpdateTransaction{ID: "01HZY6TX", Kind: core.UpdateKindRollback, FromVersion: "0.3.0", ToVersion: "0.2.0", State: core.UpdateRolledBack}, nil
}

// fakeUpdate replaces the update commands' seams for one test: geteuid
// answers euid and the installed layout is a rooted one.
func fakeUpdate(t *testing.T, euid int) (*fakeUpdater, install.Layout) {
	t.Helper()
	f := &fakeUpdater{}
	layout := install.Rooted(t.TempDir())
	oldEUID, oldResolve, oldOpen := geteuid, selectPaths, openUpdater
	t.Cleanup(func() { geteuid, selectPaths, openUpdater = oldEUID, oldResolve, oldOpen })
	geteuid = func() int { return euid }
	selectPaths = func(_ *cobra.Command, dev bool) (paths.Paths, error) {
		if dev {
			t.Error("an update command resolved the development paths")
		}
		return layout.Paths, nil
	}
	openUpdater = func(_ context.Context, p paths.Paths, _ *slog.Logger) (hostUpdater, func() error, error) {
		f.paths = append(f.paths, p)
		return f, func() error { return nil }, nil
	}
	return f, layout
}

func TestUpdateCommands(t *testing.T) {
	tests := []struct {
		args  []string
		euid  int
		code  int
		calls []string
		out   string
	}{
		{[]string{"update", "run", "01HZY6TX"}, 0, cli.ExitOK, []string{"run 01HZY6TX"}, ""},
		{[]string{"update", "run"}, 0, cli.ExitUsage, nil, "accepts 1 arg(s), received 0"},
		{[]string{"update", "run", "01HZY6TX"}, 1000, cli.ExitUsage, nil, "update run acts on the installed binary and runs as root; run it with sudo"},
		{[]string{"update", "recover"}, 0, cli.ExitOK, []string{fmt.Sprintf("recover %d %q", update.RecoverFailure, "")}, ""},
		{[]string{"update", "recover", "--boot"}, 0, cli.ExitOK, []string{fmt.Sprintf("recover %d %q", update.RecoverBoot, "")}, ""},
		{[]string{"update", "recover", "--deadline", "01HZY6TX"}, 0, cli.ExitOK, []string{fmt.Sprintf("recover %d %q", update.RecoverDeadline, "01HZY6TX")}, ""},
		{[]string{"update", "recover", "--boot", "--deadline", "01HZY6TX"}, 0, cli.ExitUsage, nil, "--boot and --deadline exclude each other"},
		{[]string{"update", "recover"}, 1000, cli.ExitUsage, nil, "update recover acts on the installed binary and runs as root"},
		{[]string{"update", "rollback", "--direct"}, 0, cli.ExitOK, []string{"direct-rollback force=false"}, "rollback 01HZY6TX from 0.3.0 to 0.2.0: rolled-back\n"},
		{[]string{"update", "rollback", "--direct", "--force", "--json"}, 0, cli.ExitOK, []string{"direct-rollback force=true"}, `"state":"rolled-back"`},
		{[]string{"update", "rollback", "--direct", "--transaction", "01HZY6RB"}, 0, cli.ExitOK, []string{"run-rollback 01HZY6RB"}, ""},
		{[]string{"update", "rollback", "--direct", "--transaction", "01HZY6RB", "--force"}, 0, cli.ExitUsage, nil, "--force does not apply with --transaction"},
		{[]string{"update", "rollback", "--direct", "--transaction", "01HZY6RB", "--json"}, 0, cli.ExitUsage, nil, "--json does not apply with --transaction"},
		{[]string{"update", "rollback", "--transaction", "01HZY6RB"}, 0, cli.ExitUsage, nil, "--transaction needs --direct"},
		{[]string{"update", "rollback", "--direct", "--host", "web1"}, 0, cli.ExitUsage, nil, "--host does not apply with --direct"},
		{[]string{"update", "rollback", "--direct", "--local"}, 0, cli.ExitUsage, nil, "--local does not apply with --direct"},
		{[]string{"update", "rollback", "--direct", "--yes"}, 0, cli.ExitUsage, nil, "--yes does not apply with --direct"},
		{[]string{"update", "rollback", "--direct", "--confirmation", "0.2.0"}, 0, cli.ExitUsage, nil, "--confirmation does not apply with --direct"},
		{[]string{"update", "rollback", "--direct", "--input", "-"}, 0, cli.ExitUsage, nil, "--input does not apply with --direct"},
		{[]string{"update", "rollback", "--direct"}, 1000, cli.ExitUsage, nil, "update rollback acts on the installed binary and runs as root"},
		{[]string{"update", "nope"}, 0, cli.ExitUsage, nil, `unknown command "nope" for "darkpanel update"`},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args[1:], " "), func(t *testing.T) {
			f, layout := fakeUpdate(t, tt.euid)
			// A previous binary is installed, so --direct must not hand the
			// rollback to it.
			writePrevBinary(t, layout.Paths)
			out, code := runArgs(tt.args...)
			if code != tt.code || !strings.Contains(out, tt.out) || !slices.Equal(f.calls, tt.calls) {
				t.Errorf("exit %d, output %q, calls %q; want exit %d, %q and calls %q", code, out, f.calls, tt.code, tt.out, tt.calls)
			}
			for _, p := range f.paths {
				if p != layout.Paths {
					t.Errorf("opened the updater on %+v, want the installed paths", p)
				}
			}
			if _, err := os.Stat(layout.Paths.PrevBinary() + ".args"); err == nil {
				t.Error("the previous binary ran")
			}
		})
	}
}

// writePrevBinary installs a previous binary that records its arguments
// beside itself and prints a rollback transaction.
func writePrevBinary(t *testing.T, p paths.Paths) {
	t.Helper()
	if err := os.MkdirAll(p.BinaryDir, 0o700); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
printf '%s\n' "$*" > "$0.args"
echo 'darkpanel.prev: rolling back' >&2
printf '{"id":"01HZY6RB","operationId":"01HZY6RB","parentId":null,"kind":"rollback","fromVersion":"0.3.0","toVersion":"0.2.0","targetVersion":"0.2.0","state":"rolled-back","error":null,"startedAt":"2026-10-20T00:00:00Z","updatedAt":"2026-10-20T00:00:01Z"}\n'
`
	if err := os.WriteFile(p.PrevBinary(), []byte(script), 0o700); err != nil { //nolint:gosec // G306: the fake binary must be executable
		t.Fatal(err)
	}
}

func TestUpdateRollbackFallback(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "agent.sock")
	tests := []struct {
		name    string
		prev    bool
		args    []string
		code    int
		argv    string // the previous binary's arguments; "" when it must not run
		stdout  string
		stderr  string
		wantErr string
	}{
		{"previous binary", true, []string{"--yes", "--json"}, cli.ExitOK, "update rollback --direct --json\n", `"state":"rolled-back"`, "darkpanel.prev: rolling back\n", ""},
		{"previous binary with force", true, []string{"--yes", "--force", "--json"}, cli.ExitOK, "update rollback --direct --json --force\n", `"toVersion":"0.2.0"`, "", ""},
		{"no previous binary", false, []string{"--yes"}, cli.ExitError, "", "", "", "there is no previous binary to roll back to (update.nothing_to_roll_back)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, layout := fakeUpdate(t, 0)
			if tt.prev {
				writePrevBinary(t, layout.Paths)
			}
			var stdout, stderr bytes.Buffer
			args := append([]string{"update", "rollback", "--local"}, tt.args...)
			code := run(args, cli.Options{AgentSocket: missing, Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr})
			if code != tt.code || !strings.Contains(stdout.String(), tt.stdout) ||
				!strings.Contains(stderr.String(), tt.stderr) || !strings.Contains(stderr.String(), tt.wantErr) {
				t.Fatalf("exit %d, stdout %q, stderr %q; want exit %d, %q and %q %q",
					code, stdout.String(), stderr.String(), tt.code, tt.stdout, tt.stderr, tt.wantErr)
			}
			if !strings.Contains(stderr.String(), "running core.update.rollback in this process") {
				t.Errorf("stderr %q does not say the rollback runs in this process", stderr.String())
			}
			argv, err := os.ReadFile(layout.Paths.PrevBinary() + ".args")
			if got := string(argv); (err == nil) != (tt.argv != "") || got != tt.argv {
				t.Errorf("the previous binary ran with %q (%v), want %q", got, err, tt.argv)
			}
		})
	}
}

// writeSignallingPrevBinary installs a previous binary that interrupts and
// hangs up on the process that started it, as a terminal's Ctrl+C or a
// closed SSH session would, records whether it leads its own process group,
// and only then prints its rollback transaction.
func writeSignallingPrevBinary(t *testing.T, p paths.Paths) {
	t.Helper()
	if err := os.MkdirAll(p.BinaryDir, 0o700); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
read -r stat < /proc/$$/stat
set -- $stat
echo "$5" > "$0.pgrp"
kill -INT $PPID
kill -HUP $PPID
sleep 0.2
printf '{"id":"01HZY6RB","operationId":"01HZY6RB","parentId":null,"kind":"rollback","fromVersion":"0.3.0","toVersion":"0.2.0","targetVersion":"0.2.0","state":"rolled-back","error":null,"startedAt":"2026-10-20T00:00:00Z","updatedAt":"2026-10-20T00:00:01Z"}\n'
`
	if err := os.WriteFile(p.PrevBinary(), []byte(script), 0o700); err != nil { //nolint:gosec // G306: the fake binary must be executable
		t.Fatal(err)
	}
}

func TestUpdateRollbackFallbackSurvivesSignals(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "agent.sock")
	_, layout := fakeUpdate(t, 0)
	writeSignallingPrevBinary(t, layout.Paths)
	var stdout, stderr bytes.Buffer
	code := run([]string{"update", "rollback", "--local", "--yes", "--json"},
		cli.Options{AgentSocket: missing, Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr})
	if code != cli.ExitOK || !strings.Contains(stdout.String(), `"state":"rolled-back"`) {
		t.Fatalf("exit %d, stdout %q, stderr %q; want the rollback's transaction", code, stdout.String(), stderr.String())
	}
	pgrp, err := os.ReadFile(layout.Paths.PrevBinary() + ".pgrp")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(pgrp)); got == strconv.Itoa(syscall.Getpgrp()) {
		t.Errorf("the previous binary ran in this process group (%s); a terminal's signals would reach it", got)
	}
}

func TestUpdateHelp(t *testing.T) {
	out := runRoot(t, "update", "--help")
	for _, sub := range []string{"run", "recover", "apply", "rollback", "status", "rollout", "rollout-cancel", "rollouts"} {
		if !strings.Contains(out, "\n  "+sub+" ") {
			t.Errorf("darkpanel update --help does not list %s:\n%s", sub, out)
		}
	}
	out = runRoot(t, "update", "rollback", "--help")
	for _, flag := range []string{"--force", "--local"} {
		if !strings.Contains(out, flag) {
			t.Errorf("darkpanel update rollback --help does not list %s", flag)
		}
	}
	// The machine path stays out of the help, so people use --local --yes,
	// which asks before it rolls back.
	for _, flag := range []string{"--direct", "--transaction"} {
		if strings.Contains(out, flag) {
			t.Errorf("darkpanel update rollback --help lists %s", flag)
		}
	}
}

// migratedControllerDB returns a migrated controller.db at path holding one
// host per entry of versions, keyed by name: "<state> <agent version>",
// with the version "-" for none.
func migratedControllerDB(t *testing.T, path string, versions map[string]string) {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	set, err := store.CoreMigrations(store.SideController)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Migrate(ctx, db, store.MigrateOptions{
		Side: store.SideController, BinaryVersion: "0.3.0", BackupRoot: filepath.Join(filepath.Dir(path), "backups"),
		Keep: 1, Now: time.Now(),
	}, set, nil); err != nil {
		t.Fatal(err)
	}
	err = db.Write(ctx, func(tx *sql.Tx) error {
		i := 0
		for name, v := range versions {
			state, agent, _ := strings.Cut(v, " ")
			var version any = agent
			if agent == "-" {
				version = nil
			}
			i++
			if _, err := tx.Exec(`INSERT INTO core_hosts (id, name, created_at, state, agent_version) VALUES (?, ?, ?, ?, ?)`,
				fmt.Sprintf("01HZY7HOST%016d", i), name, "2026-10-20T00:00:00.000000000Z", state, version); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestNewerAgents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "controller.db")
	migratedControllerDB(t, path, map[string]string{
		"older": "enrolled 0.2.9", "equal": "enrolled 0.3.0", "newer-b": "enrolled 0.3.1", "newer-a": "enrolled 0.4.0-rc.1",
		"pending-newer": "pending 0.9.0", "revoked-newer": "revoked 0.9.0", "unparsed": "enrolled nonsense", "never-seen": "enrolled -",
	})
	got, err := newerAgents(path)(context.Background(), "0.3.0")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"newer-a", "newer-b"}; !slices.Equal(got, want) {
		t.Errorf("newerAgents(0.3.0) = %q, want %q", got, want)
	}
	if _, err := newerAgents(path)(context.Background(), "not a version"); err == nil {
		t.Error("newerAgents accepted a version that does not parse")
	}
}

// TestNewerAgentsMissingDB shows that a controller.db removed after the
// agent started is reported, not created empty and owned by root.
func TestNewerAgentsMissingDB(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "controller.db")
	if got, err := newerAgents(path)(context.Background(), "0.3.0"); err == nil {
		t.Errorf("newerAgents on a missing controller.db = %q, want an error", got)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		t.Errorf("newerAgents created %s", e.Name())
	}
}

// TestNewerAgentsKeepsOwner shows that reading controller.db as root leaves
// every file of it with the controller user's ownership.
func TestNewerAgentsKeepsOwner(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root to own the database as another user")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "controller.db")
	migratedControllerDB(t, path, map[string]string{"web1": "enrolled 0.3.1"})
	for _, f := range []string{path + "-wal", path + "-shm"} {
		if err := os.Remove(f); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	const nobody = 65534
	if err := os.Chown(path, nobody, nobody); err != nil {
		t.Fatal(err)
	}
	if got, err := newerAgents(path)(context.Background(), "0.3.0"); err != nil || !slices.Equal(got, []string{"web1"}) {
		t.Fatalf("newerAgents = %q, %v; want web1", got, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "controller.db") {
			continue
		}
		st, err := os.Stat(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if uid := st.Sys().(*syscall.Stat_t).Uid; uid != nobody {
			t.Errorf("%s is owned by uid %d after a read as root, want %d", e.Name(), uid, nobody)
		}
	}
}

func TestOpenAgentNewerAgents(t *testing.T) {
	s := fakeAgent(t, 0)
	if err := os.MkdirAll(s.layout.Paths.ControllerDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, withController := range []bool{false, true} {
		if withController {
			migratedControllerDB(t, s.layout.Paths.ControllerDB(), map[string]string{"web1": "enrolled 0.3.1"})
		}
		host, err := openAgent(context.Background(), s.layout.Paths, slog.New(slog.DiscardHandler))
		if err != nil {
			t.Fatal(err)
		}
		if err := host.Close(); err != nil {
			t.Fatal(err)
		}
		cfg := s.updates[len(s.updates)-1]
		if got := cfg.NewerAgents != nil; got != withController {
			t.Fatalf("controller.db present %v: NewerAgents set %v", withController, got)
		}
		if withController {
			if got, err := cfg.NewerAgents(context.Background(), "0.3.0"); err != nil || !slices.Equal(got, []string{"web1"}) {
				t.Errorf("NewerAgents(0.3.0) = %q, %v; want web1 from the host's controller.db", got, err)
			}
		}
	}
}
