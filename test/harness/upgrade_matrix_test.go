//go:build harness

package harness

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/install"
	"github.com/darkraise/darkpanel/internal/paths"
	"github.com/darkraise/darkpanel/internal/update"
	"github.com/darkraise/darkpanel/test/harness/release"
	"github.com/darkraise/darkpanel/test/harness/vm"
)

var upgFlag = flag.Bool("upg", false, "run TestUpgradeMatrix; needs -os and -m2, and is a go test run of its own")

const (
	// upgSourcesFile is the sources file, read from the package directory.
	upgSourcesFile = "upgrade_sources.txt"
	// upgSourceDeadline bounds one source's run: boot, install, two rollouts.
	// upgDeadline bounds the whole test, so it holds upgDeadline /
	// upgSourceDeadline sources; the workflow step that runs it must keep its
	// -timeout at least upgDeadline plus 10 minutes, for the cleanups that
	// stop the VMs.
	upgSourceDeadline = 25 * time.Minute
	upgDeadline       = 60 * time.Minute
)

// TestUpgradeMatrix is S5's upgrade path (spec 9, Upgrade matrix): for each
// source of upgrade_sources.txt, a controller and an agent installed at the
// source version are updated to the build under test, labelled 1.1.0, while
// hostinfo.summary answers on the agent host one minor behind; a rollout of
// the broken 1.1.1 then rolls back within two minutes and leaves the fleet at
// 1.1.0. Each source boots a lab of its own, and the workflow selects the
// test with -run, -os, -m2 and -upg.
func TestUpgradeMatrix(t *testing.T) {
	if *osFlag == "" || *m2Flag == "" || !*upgFlag {
		t.Skip("no -os, -m2 or -upg given: the VM tests run in .github/workflows/harness.yml")
	}
	sources, err := upgLoadSources(upgSourcesFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := upgCheckBuilds(*m2Flag, sources); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), upgDeadline)
	t.Cleanup(cancel)
	o, targetImg, scannerImg := harnessImages(ctx, t)
	for _, src := range sources {
		t.Run("from-"+src.Version, func(t *testing.T) {
			sctx, cancel := context.WithTimeout(ctx, upgSourceDeadline)
			t.Cleanup(cancel)
			upgRunSource(sctx, t, o, targetImg, scannerImg, src)
		})
	}
}

// upgRunSource boots a lab, serves the releases of src, and runs the three
// steps of the matrix on it.
func upgRunSource(ctx context.Context, t *testing.T, o OS, targetImg, scannerImg string, src upgSource) {
	t.Helper()
	lab := NewLab(t)
	key, err := vm.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	vms := lab.Boot(ctx, t, m5Configs(lab, o, targetImg, scannerImg, key))
	f := &fleet{os: o, controller: vms[0], agent: vms[1], scanner: vms[2]}
	upgServeReleases(ctx, t, f, upgReleases(*m2Flag, src))
	steps := []struct {
		name string
		gate bool // later steps need this one
		run  func(t *testing.T)
	}{
		{"install", true, func(t *testing.T) { upgInstall(ctx, t, f, src.Version) }},
		{"update", true, func(t *testing.T) { upgUpdate(ctx, t, f, src.Version) }},
		{"rollback", false, func(t *testing.T) { upgRollback(ctx, t, f) }},
	}
	for _, s := range steps {
		if !t.Run(s.name, s.run) && s.gate {
			return
		}
	}
}

// upgServeReleases signs versions with the harness key, copies them to the
// scanner and serves them there as the release base URL, as serveReleases
// does for the M2 versions.
func upgServeReleases(ctx context.Context, t *testing.T, f *fleet, versions []release.Version) {
	t.Helper()
	key, err := release.LoadKey(harnessKey)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := release.Publish(dir, key, versions); err != nil {
		t.Fatal(err)
	}
	if err := copyTree(ctx, f.scanner, dir, releaseDir); err != nil {
		t.Fatal(err)
	}
	cmd := fmt.Sprintf("sudo systemd-run --unit=%s /usr/bin/python3 -m http.server 8000 --bind %s --directory %s",
		releaseUnit, scannerIPv4, releaseDir)
	if _, err := f.scanner.Run(ctx, cmd); err != nil {
		t.Fatal(err)
	}
	err = eventually(ctx, time.Minute, func(ctx context.Context) error {
		_, err := f.controller.Run(ctx, "curl -fsS --max-time 10 -o /dev/null "+releaseURL+"/index/releases.json")
		return err
	})
	if err != nil {
		journal, _ := f.scanner.Run(ctx, "sudo journalctl -u "+releaseUnit+" --no-pager -n 30")
		t.Fatalf("the release server does not answer: %v\n%s journal:\n%s", err, releaseUnit, journal)
	}
}

// upgInstall is scenarioInstall at version: install.sh of the build under
// test installs version as the controller and as an agent enrolled with a
// token the API created, and both report version.
func upgInstall(ctx context.Context, t *testing.T, f *fleet, version string) {
	for _, v := range []*vm.VM{f.controller, f.agent} {
		if err := v.CopyFile(ctx, filepath.Join(*m2Flag, "install.sh"), installScript, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	installRole(ctx, t, f.controller, version, "--role controller --agent-listen "+agentAddress)
	for unit, user := range map[string]string{update.ControllerUnit: install.ServiceUser, update.AgentUnit: "root"} {
		out, err := f.controller.Run(ctx, `stat -c %U "/proc/$(systemctl show -p MainPID --value `+unit+`)"`)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.TrimSpace(out); got != user {
			t.Errorf("%s runs as %q, want %q", unit, got, user)
		}
	}
	createAdmin(ctx, t, f)
	login(ctx, t, f)
	hosts := waitHosts(ctx, t, f, 2*time.Minute, func(hosts []core.Host) error {
		h, ok := localHost(hosts)
		if !ok {
			return errors.New("no local host listed")
		}
		return hostsAt(version, h.ID)(hosts)
	})
	local, _ := localHost(hosts)
	f.localID = local.ID

	var grant core.EnrollmentGrant
	if _, err := f.api.Apply(ctx, "", "core.host.create/v1", core.HostCreateInput{Name: "agent", ControllerAddress: agentAddress}, &grant); err != nil {
		t.Fatal(err)
	}
	f.agentID = grant.Host.ID
	writeToken(ctx, t, f.agent, grant.Token)
	installRole(ctx, t, f.agent, version, "--role agent --token-file "+enrollFile)
	removeToken(ctx, t, f.agent)
	waitHosts(ctx, t, f, 2*time.Minute, hostsAt(version, f.localID, f.agentID))
	for _, v := range []*vm.VM{f.controller, f.agent} {
		requireBinaryVersion(ctx, t, v, paths.Production().Binary(), version)
	}
}

// upgUpdate is scenarioUpdateGood from version to the build under test: a
// rollout updates the controller's host, then the agent host, each within two
// minutes, and hostinfo.summary on the agent host works while it is one minor
// behind. No reservation is left.
func upgUpdate(ctx context.Context, t *testing.T, f *fleet, from string) {
	requireSettled(ctx, t, f, from)
	ro := startRollout(ctx, t, f, upgUnderTest)
	var behindErr error
	behind := false
	// The agent host is one minor behind only from the controller host's
	// commit to the agent's restart, a few seconds, so the rollout is polled
	// faster than pollInterval.
	ro = followRollout(ctx, t, f, ro.ID, 250*time.Millisecond, func(cur core.Rollout) {
		ctl, _ := rolloutHost(cur, f.localID)
		ag, _ := rolloutHost(cur, f.agentID)
		if !behind && ctl.State == core.RolloutHostCommitted && ag.State != core.RolloutHostCommitted {
			if behindErr = oneMinorBehind(ctx, f, from, upgUnderTest); behindErr == nil {
				behind = true
			}
		}
	})
	if ro.State != core.RolloutSucceeded {
		t.Fatalf("rollout %s from %s to %s is %s (%v), want %s", ro.ID, from, upgUnderTest, ro.State, ro.Error, core.RolloutSucceeded)
	}
	if !behind {
		t.Errorf("hostinfo.summary never answered on the agent host while it ran %s and the controller %s; last error: %v", from, upgUnderTest, behindErr)
	}
	for _, id := range []string{f.localID, f.agentID} {
		h, _ := rolloutHost(ro, id)
		if h.State != core.RolloutHostCommitted || h.OperationID == nil {
			t.Errorf("rollout host %s is %s, want %s with an operation", id, h.State, core.RolloutHostCommitted)
			continue
		}
		rec := lastRecord(ctx, t, f, id, *h.OperationID)
		if rec.State != core.UpdateCommitted || rec.ToVersion != upgUnderTest {
			t.Errorf("host %s's update ended %s at %s, want %s at %s", id, rec.State, rec.ToVersion, core.UpdateCommitted, upgUnderTest)
		}
		if took := recordTook(rec); took > updateBudget {
			t.Errorf("host %s's update took %v, over %v", id, took, updateBudget)
		}
	}
	for _, v := range []*vm.VM{f.controller, f.agent} {
		requireNoLocks(ctx, t, v)
	}
	requireFleetVersion(ctx, t, f, upgUnderTest)
}

// upgRollback is scenarioHealthFailure on the updated fleet: the controller
// of the broken 1.1.1 reports degraded, so its host rolls back within two
// minutes, the rollout stops there, and both hosts keep running 1.1.0.
func upgRollback(ctx context.Context, t *testing.T, f *fleet) {
	requireSettled(ctx, t, f, upgUnderTest)
	agentBefore := newestTransaction(ctx, t, f, f.agentID)
	ro := startRollout(ctx, t, f, upgBroken)
	ro = followRollout(ctx, t, f, ro.ID, pollInterval, nil)
	if ro.State != core.RolloutFailed {
		t.Errorf("rollout %s to %s is %s, want %s", ro.ID, upgBroken, ro.State, core.RolloutFailed)
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
		t.Errorf("the controller host's update to %s ended %s, want %s", upgBroken, rec.State, core.UpdateRolledBack)
	}
	requireRolledBackFor(t, rec, `with status "degraded"`)
	if took := recordTook(rec); took > updateBudget {
		t.Errorf("the rollback took %v, over %v", took, updateBudget)
	}
	requireBinaryVersion(ctx, t, f.controller, paths.Production().FailedBinary(), upgBroken)
	requireNoLocks(ctx, t, f.controller)
	if after := newestTransaction(ctx, t, f, f.agentID); after != agentBefore {
		t.Errorf("the agent host ran update %s; the rollout must stop at the first failure", after)
	}
	requireFleetVersion(ctx, t, f, upgUnderTest)
}
