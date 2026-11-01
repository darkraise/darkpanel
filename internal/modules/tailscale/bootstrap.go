package tailscale

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/modules/tailscale/link"
	"github.com/darkraise/darkpanel/internal/platform/tslocal"
)

const (
	// joinPoll is how long the join waits between two status reads.
	joinPoll = 500 * time.Millisecond
	// joinInstallID is the change id of the install the join records: the installer has no change.
	joinInstallID = "install"
)

// JoinDeps is what Join uses on the host being installed.
type JoinDeps struct {
	Exec   core.Runner     // the production allowlisted runner
	Files  core.FileWriter // the production file provider over the new agent.db
	DB     *store.DB       // the new agent.db, migrated with the module's migrations; Join writes its tailscale_installs row
	Clock  core.Clock
	Out    io.Writer // progress lines
	Socket string    // "" is tslocal.DefaultSocket
}

// check refuses missing dependencies and an auth key that cannot be one; the error never carries the key.
func (d JoinDeps) check(authKey string) error {
	for _, f := range []struct {
		name    string
		missing bool
	}{
		{"Exec", d.Exec == nil},
		{"Files", d.Files == nil},
		{"DB", d.DB == nil},
		{"Clock", d.Clock == nil},
		{"Out", d.Out == nil},
	} {
		if f.missing {
			return fmt.Errorf("tailscale: join: JoinDeps.%s is required", f.name)
		}
	}
	if authKey == "" || strings.ContainsAny(authKey, " \t\r\n") {
		return errors.New("tailscale: join: the auth key is empty or contains whitespace")
	}
	return nil
}

// joinSocket is the local API socket the join talks to.
func joinSocket(s string) string {
	if s == "" {
		return tslocal.DefaultSocket
	}
	return s
}

// joinTS is the production ts talking to d's socket, and the function that closes its client.
func joinTS(d JoinDeps) (ts, func()) {
	t := productionTS()
	client := tslocal.New(joinSocket(d.Socket))
	t.d.local = func() localAPI { return client }
	return t, client.Close
}

// Join installs tailscale when it is absent (ensureRepo, then APT-1 and APT-2, recording a tailscale_installs row
// under change id "install"), logs in with authKey through the local API (Start with AuthKey and WantRunning, then
// LoginInteractive, spec 1.8 Login by key), and waits up to joinWait for a Tailscale address; it returns the node's
// addresses. A failure names its step.
func Join(ctx context.Context, d JoinDeps, authKey string) ([]netip.Addr, error) {
	if err := d.check(authKey); err != nil {
		return nil, err
	}
	t, closeClient := joinTS(d)
	defer closeClient()
	return t.join(ctx, d, authKey)
}

// join is Join's body. The auth key is a one-use secret: no progress line or error it returns carries it. A node
// already on the tailnet is left alone, so rerunning the install with a fresh bundle is safe.
func (t ts) join(ctx context.Context, d JoinDeps, authKey string) ([]netip.Addr, error) {
	// The env has no Effects: the installer has no change, so ensureRepo journals nothing.
	env := core.Env{Exec: d.Exec, Files: d.Files, DB: d.DB, Clock: d.Clock, Log: slog.New(slog.DiscardHandler)}
	say := func(msg string) { _, _ = io.WriteString(d.Out, "tailscale: "+msg+"\n") }
	fail := func(step string, err error) error { return fmt.Errorf("tailscale: join: %s: %w", step, err) }
	redact := func(step string, err error) error {
		// A new error, not a wrap: the original's text may carry the key.
		return errors.New("tailscale: join: " + step + ": " + strings.ReplaceAll(err.Error(), authKey, "[redacted]"))
	}
	wait := strconv.Itoa(int(joinWait.Seconds())) + "s"

	version, err := t.installedVersion(ctx, env)
	if err != nil {
		return nil, fail("inspect", err)
	}
	if version != "" {
		say("version " + version + " is already installed")
	} else {
		say("installing the package")
		if _, err := t.ensureRepo(ctx, env); err != nil {
			return nil, fail("repository", err)
		}
		installCtx, cancel := context.WithTimeout(ctx, packageInstallTimeout)
		err := aptInstall(installCtx, env)
		cancel()
		if err != nil {
			return nil, fail("install", err)
		}
		if version, err = t.installedVersion(ctx, env); err != nil {
			return nil, fail("install", err)
		}
		if version == "" {
			return nil, fail("install", errors.New("apt-get finished but tailscale is not installed"))
		}
		if err := joinRecordInstall(ctx, d.DB, version, d.Clock.Now()); err != nil {
			return nil, fail("install", err)
		}
		say("installed version " + version)
	}

	// One budget, from the package being in place, covers tailscaled's socket, the login and the address.
	deadline := d.Clock.Now().Add(joinWait)
	local := t.d.local()
	st, statusErr, err := joinPollStatus(ctx, d.Clock, local, deadline, func(*tslocal.Status) bool { return true })
	switch {
	case err != nil:
		return nil, fail("tailscaled", err)
	case st == nil:
		return nil, fail("tailscaled", fmt.Errorf("did not answer on its socket within %s: %w", wait, statusErr))
	}
	if joinSettled(st) {
		addrs := joinAddrs(st)
		say("already joined as " + joinList(addrs))
		return addrs, nil
	}

	p, err := local.Prefs(ctx)
	if err != nil {
		return nil, fail("login", err)
	}
	prefs := *p
	prefs.WantRunning, prefs.LoggedOut = true, false
	if err := local.Start(ctx, tslocal.StartOptions{AuthKey: authKey, UpdatePrefs: &prefs}); err != nil {
		return nil, redact("login", err)
	}
	if err := local.LoginInteractive(ctx); err != nil {
		return nil, redact("login", err)
	}
	say("logged in; waiting for a Tailscale address")

	st, _, err = joinPollStatus(ctx, d.Clock, local, deadline, joinSettled)
	if err != nil {
		return nil, fail("address", err)
	}
	if st == nil || !joinSettled(st) {
		state := "unknown"
		if st != nil {
			state = st.BackendState
		}
		return nil, fail("address", fmt.Errorf("no Tailscale address within %s (backend state %s)", wait, state))
	}
	addrs := joinAddrs(st)
	say("joined the tailnet as " + joinList(addrs))
	return addrs, nil
}

// joinRecordInstall records the join's install of version under joinInstallID. It clears removed_at, so a
// reinstall after an uninstall that kept the database reuses the row instead of failing on the key.
func joinRecordInstall(ctx context.Context, db *store.DB, version string, at time.Time) error {
	const upsert = "INSERT INTO tailscale_installs (change_id, version, installed_at) VALUES (?, ?, ?) " +
		"ON CONFLICT (change_id) DO UPDATE SET version = excluded.version, installed_at = excluded.installed_at, removed_at = ''"
	err := db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, upsert, joinInstallID, version, storeTime(at))
		return err
	})
	if err != nil {
		return fmt.Errorf("tailscale: record install %s: %w", joinInstallID, err)
	}
	return nil
}

// joinPollStatus reads the status until done accepts an answer, waiting joinPoll between reads. It returns the
// last answer (nil when none came) and the last Status error once done accepts, the clock reaches deadline, or
// the context ends; err is the context's error in the last case.
func joinPollStatus(ctx context.Context, clock core.Clock, local localAPI, deadline time.Time,
	done func(*tslocal.Status) bool,
) (st *tslocal.Status, statusErr, err error) {
	for {
		callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		answer, callErr := local.Status(callCtx)
		cancel()
		if callErr != nil {
			statusErr = callErr
		} else {
			st = answer
			if done(st) {
				return st, statusErr, nil
			}
		}
		if ctx.Err() != nil {
			return st, statusErr, ctx.Err()
		}
		if !clock.Now().Before(deadline) {
			return st, statusErr, nil
		}
		if err := joinSleep(ctx, clock); err != nil {
			return st, statusErr, err
		}
	}
}

// joinSleep waits joinPoll on clock, or until the context ends.
func joinSleep(ctx context.Context, clock core.Clock) error {
	tick := make(chan struct{})
	timer := clock.AfterFunc(joinPoll, func() { close(tick) })
	select {
	case <-tick:
		return nil
	case <-ctx.Done():
		timer.Stop()
		return ctx.Err()
	}
}

// joinSettled reports whether the node is on the tailnet: running with a Tailscale address.
func joinSettled(st *tslocal.Status) bool {
	return st.BackendState == "Running" && len(joinAddrs(st)) > 0
}

// joinAddrs are the node's Tailscale addresses, IPv4 first and otherwise in tailscaled's order.
func joinAddrs(st *tslocal.Status) []netip.Addr {
	var addrs []netip.Addr
	for _, a := range st.TailscaleIPs {
		if link.IsTailscaleAddr(a) {
			addrs = append(addrs, a)
		}
	}
	slices.SortStableFunc(addrs, func(a, b netip.Addr) int {
		switch {
		case a.Is4() == b.Is4():
			return 0
		case a.Is4():
			return -1
		}
		return 1
	})
	return addrs
}

// joinList is addrs as one progress-line phrase.
func joinList(addrs []netip.Addr) string {
	s := make([]string, len(addrs))
	for i, a := range addrs {
		s[i] = a.String()
	}
	return strings.Join(s, ", ")
}
