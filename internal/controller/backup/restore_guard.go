package backup

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/oklog/ulid/v2"
	"golang.org/x/sys/unix"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/install"
	"github.com/darkraise/darkpanel/internal/paths"
	"github.com/darkraise/darkpanel/internal/platform"
	"github.com/darkraise/darkpanel/internal/update"
)

// RestoreLockPath is the lock a restore and a recovery hold on a production host.
const RestoreLockPath = "/run/darkpanel-restore.lock"

// Host is what a restore and a recovery touch on this host; tests fill every field.
type Host struct {
	Paths    paths.Paths
	UnitFile string                           // the installed controller unit file; production: install.Production().UnitPath(update.ControllerUnit)
	LockFile string                           // RestoreLockPath in production
	Units    core.Units                       // production: platform.NewSystemd
	Euid     func() int                       // os.Geteuid in production
	Owner    func() (uid, gid int, err error) // the darkpanel user's ids (install.ServiceUser)
	Ops      Ops                              // OSOps() in production
	Clock    core.Clock
	NewTx    func() string // a fresh upper-case ULID
	Log      *slog.Logger
	// JournalUID is the uid every journal recovery reads must belong to: 0 in production, where only
	// root restores and recovers, so a journal the controller service wrote is refused.
	JournalUID int
}

// ProductionHost returns the production Host over p; its Close releases the D-Bus connection.
// With withUnits false it opens no D-Bus connection, Units is nil and Close does nothing: recovery
// runs before every controller start and never checks the services, so it must not depend on D-Bus.
func ProductionHost(ctx context.Context, p paths.Paths, withUnits bool) (Host, func() error, error) {
	var units core.Units
	closeUnits := func() error { return nil }
	if withUnits {
		systemd, err := platform.NewSystemd(ctx)
		if err != nil {
			return Host{}, nil, fmt.Errorf("backup: %w", err)
		}
		units, closeUnits = systemd, systemd.Close
	}
	clock := core.SystemClock{}
	return Host{
		Paths:    p,
		UnitFile: install.Production().UnitPath(update.ControllerUnit),
		LockFile: RestoreLockPath,
		Units:    units,
		Euid:     os.Geteuid,
		Owner:    serviceOwner(install.SystemUsers{}),
		Ops:      OSOps(),
		Clock:    clock,
		NewTx:    ulidTx(clock),
		Log:      slog.New(slog.NewTextHandler(os.Stderr, nil)),
		// The journal owner is root even when a non-root caller builds the
		// host: os.Geteuid would let such a caller's own files pass.
		JournalUID: 0,
	}, closeUnits, nil
}

// lock takes an exclusive non-blocking flock on h.LockFile (created 0600) and returns its release.
func (h Host) lock() (func(), error) {
	// A raw descriptor rather than an *os.File: no finalizer closes the lock
	// while its holder still runs.
	fd, err := unix.Open(h.LockFile, unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return nil, fmt.Errorf("backup: open the lock file: %w", err)
	}
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = unix.Close(fd)
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, fmt.Errorf("backup: another restore or recovery holds %s", h.LockFile)
		}
		return nil, fmt.Errorf("backup: lock %s: %w", h.LockFile, err)
	}
	// Closing releases the lock and the file stays. Closing only once keeps a
	// repeated release from closing a descriptor number a later lock reused.
	var once sync.Once
	return func() { once.Do(func() { _ = unix.Close(fd) }) }, nil
}

// checkRestore runs the restore's preconditions in this order and returns the first refusal.
func (h Host) checkRestore(ctx context.Context) error {
	if h.Euid() != 0 {
		return errors.New("backup: a restore must run as root")
	}
	if err := guardInstalled(h); err != nil {
		return err
	}
	for _, unit := range []string{update.ControllerUnit, update.AgentUnit} {
		st, err := h.Units.State(ctx, unit)
		if err != nil {
			return fmt.Errorf("backup: read the state of %s: %w", unit, err)
		}
		if st.LoadState == "not-found" || st.ActiveState == "inactive" || st.ActiveState == "failed" {
			continue
		}
		return fmt.Errorf("backup: stop darkpanel-controller.service and darkpanel-agent.service before a restore (%s is %s)",
			unit, st.ActiveState)
	}
	journals, err := pendingJournals(h.Paths.ControllerDir)
	if err != nil {
		return err
	}
	if len(journals) > 0 {
		return fmt.Errorf("backup: an unfinished restore exists (%s); start the controller or run `darkpanel controller restore --recover` first",
			journals[0])
	}
	release, err := h.lock()
	if err != nil {
		return err
	}
	release()
	return nil
}

// guardInstalled refuses a host without the controller unit file, directory and key.
func guardInstalled(h Host) error {
	notInstalled := errors.New("backup: no controller is installed here; run darkpanel system install --role controller first")
	if _, err := os.Lstat(h.UnitFile); err != nil {
		return guardStatError(err, notInstalled)
	}
	st, err := os.Stat(h.Paths.ControllerDir)
	if err != nil {
		return guardStatError(err, notInstalled)
	}
	if !st.IsDir() {
		return notInstalled
	}
	if _, err := os.Lstat(h.Paths.ControllerKey); err != nil {
		return guardStatError(err, notInstalled)
	}
	return nil
}

// guardStatError answers a missing path with refusal and any other stat error as itself.
func guardStatError(err, refusal error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return refusal
	}
	return fmt.Errorf("backup: %w", err)
}

// pendingJournals returns the restore-<tx>.journal paths in dir sorted by tx ascending.
func pendingJournals(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("backup: %w", err)
	}
	var txs []string
	for _, e := range entries {
		if ok, _ := filepath.Match("restore-*.journal", e.Name()); ok {
			txs = append(txs, strings.TrimSuffix(strings.TrimPrefix(e.Name(), "restore-"), ".journal"))
		}
	}
	slices.Sort(txs)
	journals := make([]string, 0, len(txs))
	for _, tx := range txs {
		journals = append(journals, filepath.Join(dir, "restore-"+tx+".journal"))
	}
	return journals, nil
}

// serviceOwner returns a function that looks up the ids of the darkpanel user.
func serviceOwner(users install.Users) func() (uid, gid int, err error) {
	return func() (int, int, error) {
		uid, gid, found, err := users.Lookup(install.ServiceUser)
		if err != nil {
			return 0, 0, fmt.Errorf("backup: look up the %s user: %w", install.ServiceUser, err)
		}
		if !found {
			return 0, 0, fmt.Errorf("backup: the %s user does not exist", install.ServiceUser)
		}
		return uid, gid, nil
	}
}

// ulidTx returns a function that makes a fresh upper-case ULID at clock's time.
func ulidTx(clock core.Clock) func() string {
	return func() string {
		return ulid.MustNew(ulid.Timestamp(clock.Now()), rand.Reader).String()
	}
}
