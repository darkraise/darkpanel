//go:build harness

package harness

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/paths"
	"github.com/darkraise/darkpanel/internal/update"
	"github.com/darkraise/darkpanel/test/harness/vm"
)

// updateBudget is S5's bound: a host's update commits, or rolls back, within
// two minutes.
const updateBudget = 2 * time.Minute

// rolloutTimeout bounds one rollout of both hosts: each update's budget, the
// driver's 5 s polls and the controller's own restart.
const rolloutTimeout = 8 * time.Minute

// scenarioUpdateGood is scenario 5: a rollout to the good build updates the
// controller's host, then the agent host, each within two minutes, and
// hostinfo.summary on the agent host works while it is one minor behind
// (S5). No reservation is left.
func scenarioUpdateGood(ctx context.Context, t *testing.T, f *fleet) {
	requireSettled(ctx, t, f, versionN)
	ro := startRollout(ctx, t, f, versionGood)
	var behindErr error
	behind := false
	// The agent host is one minor behind only from the controller host's
	// commit to the agent's restart, a few seconds, so the rollout is
	// polled faster than pollInterval.
	ro = followRollout(ctx, t, f, ro.ID, 250*time.Millisecond, func(cur core.Rollout) {
		ctl, _ := rolloutHost(cur, f.localID)
		ag, _ := rolloutHost(cur, f.agentID)
		if !behind && ctl.State == core.RolloutHostCommitted && ag.State != core.RolloutHostCommitted {
			if behindErr = oneMinorBehind(ctx, f, versionN, versionGood); behindErr == nil {
				behind = true
			}
		}
	})
	if ro.State != core.RolloutSucceeded {
		t.Fatalf("rollout %s to %s is %s (%v), want %s", ro.ID, versionGood, ro.State, ro.Error, core.RolloutSucceeded)
	}
	if !behind {
		t.Errorf("hostinfo.summary never answered on the agent host while it ran %s and the controller %s; last error: %v", versionN, versionGood, behindErr)
	}
	for _, id := range []string{f.localID, f.agentID} {
		h, _ := rolloutHost(ro, id)
		if h.State != core.RolloutHostCommitted || h.OperationID == nil {
			t.Errorf("rollout host %s is %s, want %s with an operation", id, h.State, core.RolloutHostCommitted)
			continue
		}
		rec := lastRecord(ctx, t, f, id, *h.OperationID)
		if rec.State != core.UpdateCommitted || rec.ToVersion != versionGood {
			t.Errorf("host %s's update ended %s at %s, want %s at %s", id, rec.State, rec.ToVersion, core.UpdateCommitted, versionGood)
		}
		if took := recordTook(rec); took > updateBudget {
			t.Errorf("host %s's update took %v, over %v", id, took, updateBudget)
		}
	}
	for _, v := range []*vm.VM{f.controller, f.agent} {
		requireNoLocks(ctx, t, v)
	}
	requireFleetVersion(ctx, t, f, versionGood)
}

// oneMinorBehind reads hostinfo.summary of the agent host, provided the host
// list shows it online at agentVersion and the controller's host at
// controllerVersion.
func oneMinorBehind(ctx context.Context, f *fleet, agentVersion, controllerVersion string) error {
	hosts, err := f.api.Hosts(ctx)
	if err != nil {
		return err
	}
	if err := hostsAt(controllerVersion, f.localID)(hosts); err != nil {
		return err
	}
	if err := hostsAt(agentVersion, f.agentID)(hosts); err != nil {
		return err
	}
	var sum struct {
		Hostname string `json:"hostname"`
	}
	return f.api.Resource(ctx, f.agentID, "hostinfo.summary", &sum)
}

// scenarioHealthFailure is scenario 6: the controller of the broken build
// reports degraded, so its host rolls back at once, within two minutes.
func scenarioHealthFailure(ctx context.Context, t *testing.T, f *fleet) {
	rec := failedUpdate(ctx, t, f, versionBroken, nil)
	requireRolledBackFor(t, rec, `with status "degraded"`)
	if took := recordTook(rec); took > updateBudget {
		t.Errorf("the rollback took %v, over %v", took, updateBudget)
	}
}

// scenarioCrashLoop is scenario 7: the services of the crashing build exhaust
// their start limit, and OnFailure= runs the rollback before the runner's
// deadline.
func scenarioCrashLoop(ctx context.Context, t *testing.T, f *fleet) {
	rec := failedUpdate(ctx, t, f, versionCrash, nil)
	requireRolledBackFor(t, rec, "a service or the runner failed before the update was committed")
	if took := recordTook(rec); took > updateBudget {
		t.Errorf("the rollback took %v, over %v", took, updateBudget)
	}
}

// scenarioRebootInWindow is scenario 8: the controller's host reboots inside
// the health window, after the swap, and the boot-time recovery rolls the
// record back. The runner is frozen first: the reboot goes over SSH and the
// shutdown takes seconds, enough for a free runner to pass its health check
// and commit before the host goes down.
func scenarioRebootInWindow(ctx context.Context, t *testing.T, f *fleet) {
	rec := failedUpdate(ctx, t, f, versionReboot, func() {
		tx := watchSwap(ctx, t, f.controller, "systemctl kill --signal=STOP "+update.RunnerUnit("$tx"))
		t.Logf("rebooting the controller's host in the health window of %s", tx)
		if err := f.controller.Reboot(ctx); err != nil {
			t.Fatal(err)
		}
	})
	requireRolledBackFor(t, rec, "the host restarted before the update was committed")
	for _, unit := range []string{update.ControllerUnit, update.AgentUnit} {
		if state := unitState(ctx, f.controller, unit); state != "active" {
			t.Errorf("%s is %q after the reboot", unit, state)
		}
	}
}

// scenarioRunnerFrozen is scenario 9: the runner is frozen right after the
// swap, so only the deadline timer can act, and it rolls back within two
// minutes of the swap.
func scenarioRunnerFrozen(ctx context.Context, t *testing.T, f *fleet) {
	var tx string
	rec := failedUpdate(ctx, t, f, versionReboot, func() {
		tx = watchSwap(ctx, t, f.controller, "systemctl kill --signal=STOP "+update.RunnerUnit("$tx"))
		t.Logf("froze the runner of %s", tx)
		// The deadline rollback kills the runner only when the runner holds
		// the update lock; one frozen outside it would stay stopped.
		t.Cleanup(func() {
			cctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_, _ = f.controller.Run(cctx, "sudo systemctl kill --signal=KILL "+update.RunnerUnit(tx)+" || true")
		})
	})
	requireRolledBackFor(t, rec, "the services did not report healthy before the deadline")
	out, err := f.controller.Run(ctx, "sudo cat "+paths.Production().UpdateRecord(tx))
	if err != nil {
		t.Fatal(err)
	}
	var r update.Record
	if err := json.Unmarshal([]byte(out), &r); err != nil || r.Deadline == nil {
		t.Fatalf("record %s: %v, deadline %v\n%s", tx, err, r.Deadline, out)
	}
	if took := r.UpdatedAt.Sub(r.Deadline.Add(-update.HealthDeadline)); took > updateBudget {
		t.Errorf("the rollback ended %v after the swap, over %v", took, updateBudget)
	}
}

// scenarioMigrationRollback is scenario 10: the migrating build backs the
// controller database up, migrates both databases and fails its health
// check; the good build then runs on the migrated databases, which keep the
// rows the migration wrote.
func scenarioMigrationRollback(ctx context.Context, t *testing.T, f *fleet) {
	backups := paths.Production().ControllerBackups()
	before := listDir(ctx, t, f.controller, backups)
	rec := failedUpdate(ctx, t, f, versionMigration, nil)
	requireRolledBackFor(t, rec, `with status "degraded"`)
	if got := newEntries(before, listDir(ctx, t, f.controller, backups), versionGood+"-"+versionMigration+"-"); len(got) == 0 {
		t.Errorf("%s gained no backup of the update from %s to %s", backups, versionGood, versionMigration)
	}
	for _, db := range []string{paths.Production().ControllerDB(), paths.Production().AgentDB()} {
		if n, err := sqliteCount(ctx, f.controller, db, "SELECT count(*) FROM harness_marker"); err != nil || n < 1 {
			t.Errorf("%s holds %d harness_marker rows (%v), want the migration's row", db, n, err)
		}
	}
}

// failedUpdate rolls version out, runs during (unless nil) once the rollout
// exists, and checks what every failed update leaves: the rollout failed at
// the controller's host, whose operation rolled back with the new binary
// kept as darkpanel.failed and no reservation left; the agent host was
// skipped and ran no update; both hosts run the good build. It returns the
// record that rolled back.
func failedUpdate(ctx context.Context, t *testing.T, f *fleet, version string, during func()) core.UpdateTransaction {
	t.Helper()
	requireSettled(ctx, t, f, versionGood)
	agentBefore := newestTransaction(ctx, t, f, f.agentID)
	ro := startRollout(ctx, t, f, version)
	if during != nil {
		during()
	}
	ro = followRollout(ctx, t, f, ro.ID, pollInterval, nil)
	if ro.State != core.RolloutFailed {
		t.Errorf("rollout %s to %s is %s, want %s", ro.ID, version, ro.State, core.RolloutFailed)
	}
	ctl, _ := rolloutHost(ro, f.localID)
	if ctl.State != core.RolloutHostFailed || ctl.OperationID == nil {
		t.Fatalf("the controller's host is %s in rollout %s, want %s with an operation", ctl.State, ro.ID, core.RolloutHostFailed)
	}
	if ag, _ := rolloutHost(ro, f.agentID); ag.State != core.RolloutHostSkipped {
		t.Errorf("the agent host is %s in rollout %s, want %s", ag.State, ro.ID, core.RolloutHostSkipped)
	}
	rec := lastRecord(ctx, t, f, f.localID, *ctl.OperationID)
	if rec.State != core.UpdateRolledBack {
		t.Errorf("the controller host's update to %s ended %s, want %s", version, rec.State, core.UpdateRolledBack)
	}
	requireBinaryVersion(ctx, t, f.controller, paths.Production().FailedBinary(), version)
	requireNoLocks(ctx, t, f.controller)
	if after := newestTransaction(ctx, t, f, f.agentID); after != agentBefore {
		t.Errorf("the agent host ran update %s; the rollout must stop at the first failure", after)
	}
	requireFleetVersion(ctx, t, f, versionGood)
	return rec
}

// requireRolledBackFor checks that rec rolled back with an error message
// containing reason, which names the path that rolled it back.
func requireRolledBackFor(t *testing.T, rec core.UpdateTransaction, reason string) {
	t.Helper()
	if rec.Error == nil || !strings.Contains(rec.Error.Message, reason) {
		t.Errorf("update %s rolled back for %+v, want a message containing %q", rec.ID, rec.Error, reason)
	}
}

// startRollout rolls version out to both hosts, the controller's first.
func startRollout(ctx context.Context, t *testing.T, f *fleet, version string) core.Rollout {
	t.Helper()
	var ro core.Rollout
	in := core.RolloutInput{Hosts: []string{f.localID, f.agentID}, Version: version, Channel: releaseChannel}
	if _, err := f.api.Apply(ctx, "", "core.update.rollout/v1", in, &ro); err != nil {
		t.Fatal(err)
	}
	return ro
}

// followRollout polls rollout id every interval until it is no longer
// active, calling observe (unless nil) with each state read, and returns
// the last one. API errors, such as those of the controller restarting
// during its own update, count as not yet.
func followRollout(ctx context.Context, t *testing.T, f *fleet, id string, interval time.Duration, observe func(core.Rollout)) core.Rollout {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, rolloutTimeout)
	defer cancel()
	var last core.Rollout
	var lastErr error
	for {
		ro, err := readRollout(ctx, f, id)
		if err == nil {
			last = ro
			if observe != nil {
				observe(ro)
			}
			if !activeRollout(ro.State) {
				return ro
			}
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			t.Fatalf("rollout %s is still %s after %v: %v; last error: %v", id, last.State, rolloutTimeout, ctx.Err(), lastErr)
		case <-time.After(interval):
		}
	}
}

// readRollout returns rollout id from the newest rollouts.
func readRollout(ctx context.Context, f *fleet, id string) (core.Rollout, error) {
	var l core.RolloutList
	if err := f.api.Read(ctx, "", "core.update.rollouts/v1", core.RolloutsInput{Limit: 5}, &l); err != nil {
		return core.Rollout{}, err
	}
	for _, ro := range l.Rollouts {
		if ro.ID == id {
			return ro, nil
		}
	}
	return core.Rollout{}, fmt.Errorf("rollout %s is not among the newest five", id)
}

// rolloutHost returns host id's row of ro.
func rolloutHost(ro core.Rollout, id string) (core.RolloutHost, bool) {
	for _, h := range ro.Hosts {
		if h.HostID == id {
			return h, true
		}
	}
	return core.RolloutHost{}, false
}

// activeRollout reports whether a rollout in state can still update a host.
func activeRollout(state string) bool {
	return state == core.RolloutRunning || state == core.RolloutDraining
}

// requireSettled waits until no rollout is active and both hosts run version
// online. Every update scenario starts from it, so a scenario that failed
// does not leave the next one starting on a fleet that is still updating.
func requireSettled(ctx context.Context, t *testing.T, f *fleet, version string) {
	t.Helper()
	err := eventually(ctx, 3*time.Minute, func(ctx context.Context) error {
		var l core.RolloutList
		if err := f.api.Read(ctx, "", "core.update.rollouts/v1", core.RolloutsInput{Limit: 5}, &l); err != nil {
			return err
		}
		for _, ro := range l.Rollouts {
			if activeRollout(ro.State) {
				return fmt.Errorf("rollout %s to %s is %s", ro.ID, ro.Version, ro.State)
			}
		}
		hosts, err := f.api.Hosts(ctx)
		if err != nil {
			return err
		}
		return hostsAt(version, f.localID, f.agentID)(hosts)
	})
	if err != nil {
		t.Fatalf("the fleet did not settle on %s: %v", version, err)
	}
}

// requireFleetVersion waits until the host list shows both hosts online at
// version, and checks that both installed binaries report it.
func requireFleetVersion(ctx context.Context, t *testing.T, f *fleet, version string) {
	t.Helper()
	waitHosts(ctx, t, f, 3*time.Minute, hostsAt(version, f.localID, f.agentID))
	for _, v := range []*vm.VM{f.controller, f.agent} {
		requireBinaryVersion(ctx, t, v, paths.Production().Binary(), version)
	}
}

// lastRecord returns the newest record of operation op on host. The agent
// may still be restarting after a rollback, so API errors are retried.
func lastRecord(ctx context.Context, t *testing.T, f *fleet, host, op string) core.UpdateTransaction {
	t.Helper()
	var st core.UpdateStatus
	err := eventually(ctx, time.Minute, func(ctx context.Context) error {
		if err := f.api.Read(ctx, host, "core.update.status/v1", core.UpdateStatusInput{OperationID: op}, &st); err != nil {
			return err
		}
		if len(st.Transactions) == 0 {
			return fmt.Errorf("host %s has no record of operation %s", host, op)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return st.Transactions[0]
}

// newestTransaction returns the id of host's newest update record, or "".
func newestTransaction(ctx context.Context, t *testing.T, f *fleet, host string) string {
	t.Helper()
	var st core.UpdateStatus
	err := eventually(ctx, time.Minute, func(ctx context.Context) error {
		return f.api.Read(ctx, host, "core.update.status/v1", core.UpdateStatusInput{}, &st)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Transactions) == 0 {
		return ""
	}
	return st.Transactions[0].ID
}

// recordTook is how long rec's step ran, by the host's own clock.
func recordTook(rec core.UpdateTransaction) time.Duration { return rec.UpdatedAt.Sub(rec.StartedAt) }

// requireNoLocks waits until v's agent database holds no lock: a terminal
// update releases its reservation just after it records its state.
func requireNoLocks(ctx context.Context, t *testing.T, v *vm.VM) {
	t.Helper()
	err := eventually(ctx, 30*time.Second, func(ctx context.Context) error {
		n, err := sqliteCount(ctx, v, paths.Production().AgentDB(), "SELECT count(*) FROM core_locks")
		if err == nil && n != 0 {
			err = fmt.Errorf("core_locks holds %d rows", n)
		}
		return err
	})
	if err != nil {
		t.Error(err)
	}
}

// sqliteCount runs a count query, free of single quotes, on db on v as root.
func sqliteCount(ctx context.Context, v *vm.VM, db, query string) (int, error) {
	out, err := v.Run(ctx, "sudo sqlite3 "+db+" '"+query+"'")
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(out))
}

// listDir lists dir on v as root; a missing dir lists nothing.
func listDir(ctx context.Context, t *testing.T, v *vm.VM, dir string) []string {
	t.Helper()
	out, err := v.Run(ctx, "sudo sh -c 'ls -1 "+dir+" 2>/dev/null || true'")
	if err != nil {
		t.Fatal(err)
	}
	return strings.Fields(out)
}

// newEntries returns the names of after, starting with prefix, that before
// lacks.
func newEntries(before, after []string, prefix string) []string {
	var out []string
	for _, name := range after {
		if strings.HasPrefix(name, prefix) && !slices.Contains(before, name) {
			out = append(out, name)
		}
	}
	return out
}

// watchSwap waits on v for the update in its health window and runs then
// there, at once, with $tx set; it returns the transaction id.
func watchSwap(ctx context.Context, t *testing.T, v *vm.VM, then string) string {
	t.Helper()
	p := paths.Production()
	out, err := v.RunInput(ctx, "sudo sh -s", strings.NewReader(swapWatch(p.UpdateDir, p.Binary(), then, 3*time.Minute)))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(out)
}

// swapWatch is a shell script, run as root, that waits up to timeout for an
// update record in dir to reach swapped and for binary to be that record's
// new binary, then runs then with $tx set to the record's transaction id and
// prints the id. The runner records swapped before it renames the new
// binary into place, so waiting for the hash too puts then inside the health
// window.
func swapWatch(dir, binary, then string, timeout time.Duration) string {
	return fmt.Sprintf(`set -eu
end=$(( $(date +%%s) + %d ))
while [ "$(date +%%s)" -le "$end" ]; do
	for f in %s/*.json; do
		[ -f "$f" ] || continue
		grep -q '"state": "swapped"' "$f" || continue
		want=$(sed -n 's/.*"newSha256": "\([0-9a-f]*\)".*/\1/p' "$f")
		[ -n "$want" ] && [ "$(sha256sum %s | cut -d ' ' -f 1)" = "$want" ] || continue
		tx=$(basename "$f" .json)
		%s
		echo "$tx"
		exit 0
	done
	sleep 0.1
done
echo "no update record reached swapped with its new binary in place" >&2
exit 1
`, int(timeout/time.Second), dir, binary, then)
}

func TestSwapWatch(t *testing.T) {
	root := t.TempDir()
	p := paths.Under(root)
	if err := os.MkdirAll(p.UpdateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "darkpanel")
	if err := os.WriteFile(binary, []byte("the new binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("the new binary"))
	record := func(id, state, newSHA string) {
		t.Helper()
		r := update.Record{ID: id, OperationID: id, Kind: core.UpdateKindUpdate, FromVersion: "0.3.0", ToVersion: "0.3.3",
			TargetVersion: "0.3.3", State: state, NewSHA256: newSHA}
		if err := update.WriteRecord(p, r); err != nil {
			t.Fatal(err)
		}
	}
	watch := func(then string) (string, error) {
		script := swapWatch(p.UpdateDir, binary, then, time.Second)
		out, err := exec.Command("sh", "-c", script).CombinedOutput() //nolint:gosec // G204: the test's own script
		return string(out), err
	}

	record("01OLDCOMMITTED", core.UpdateCommitted, hex.EncodeToString(sum[:]))
	if out, err := watch(""); err == nil {
		t.Errorf("swapWatch over committed records only = %q, want a timeout", out)
	}
	record("01SWAPPEDOTHER", core.UpdateSwapped, strings.Repeat("0", 64))
	if out, err := watch(""); err == nil || !strings.Contains(out, "no update record reached swapped with its new binary in place") {
		t.Errorf("swapWatch while the binary is not yet the new one = %q, %v; want a timeout", out, err)
	}
	record("01SWAPPEDOTHER", core.UpdateRolledBack, strings.Repeat("0", 64))
	record("01SWAPPEDNEW", core.UpdateSwapped, hex.EncodeToString(sum[:]))
	thenFile := filepath.Join(root, "then")
	out, err := watch(`echo "frozen $tx" > ` + thenFile)
	if err != nil || out != "01SWAPPEDNEW\n" {
		t.Errorf("swapWatch = %q, %v; want the swapped transaction", out, err)
	}
	if got, err := os.ReadFile(thenFile); err != nil || string(got) != "frozen 01SWAPPEDNEW\n" { //nolint:gosec // G304: the test's own file
		t.Errorf("then ran with %q, %v; want $tx set to the swapped transaction", got, err)
	}
	if script := swapWatch(p.UpdateDir, binary, "systemctl kill --signal=STOP "+update.RunnerUnit("$tx"), time.Minute); !strings.Contains(script,
		"systemctl kill --signal=STOP darkpanel-update-$tx.service") {
		t.Errorf("swapWatch does not run its command with the runner unit of $tx:\n%s", script)
	}
}

func TestRolloutHelpers(t *testing.T) {
	ro := core.Rollout{ID: "r1", State: core.RolloutDraining, Hosts: []core.RolloutHost{
		{HostID: "h0", Position: 0, State: core.RolloutHostFailed},
		{HostID: "h1", Position: 1, State: core.RolloutHostSkipped},
	}}
	if h, ok := rolloutHost(ro, "h1"); !ok || h.State != core.RolloutHostSkipped {
		t.Errorf("rolloutHost(h1) = %+v, %v", h, ok)
	}
	if _, ok := rolloutHost(ro, "h9"); ok {
		t.Error("rolloutHost found a host the rollout does not list")
	}
	for state, want := range map[string]bool{
		core.RolloutRunning: true, core.RolloutDraining: true,
		core.RolloutSucceeded: false, core.RolloutFailed: false, core.RolloutCancelled: false,
	} {
		if got := activeRollout(state); got != want {
			t.Errorf("activeRollout(%s) = %v, want %v", state, got, want)
		}
	}
	before := []string{"0.2.0-0.3.0-20261020T120000Z", "none-0.2.0-20261020T110000Z"}
	after := []string{"0.2.0-0.3.0-20261020T120000Z", "0.3.0-0.3.4-20261020T130000Z", "0.3.0-0.3.1-20261020T125000Z"}
	if got := newEntries(before, after, "0.3.0-0.3.4-"); !slices.Equal(got, []string{"0.3.0-0.3.4-20261020T130000Z"}) {
		t.Errorf("newEntries = %v", got)
	}
	if got := newEntries(after, after, "0.3.0-0.3.4-"); len(got) != 0 {
		t.Errorf("newEntries of an unchanged directory = %v", got)
	}
	start := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	rec := core.UpdateTransaction{StartedAt: start, UpdatedAt: start.Add(95 * time.Second)}
	if got := recordTook(rec); got != 95*time.Second {
		t.Errorf("recordTook = %v, want 1m35s", got)
	}
}
