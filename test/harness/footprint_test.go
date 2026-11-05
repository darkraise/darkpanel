//go:build harness

package harness

import (
	"cmp"
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/coreactions"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/paths"
	"github.com/darkraise/darkpanel/internal/update"
	"github.com/darkraise/darkpanel/test/harness/vm"
)

var fpFlag = flag.Bool("fp", false, "run TestFootprint; needs -os and -m2, and is a go test run of its own")

// The footprint budgets (bytes, decimal megabytes) and the sampling plan of TestFootprint.
const (
	fpAgentMax      = 50_000_000
	fpControllerMax = 150_000_000
	fpWarmup        = 5 * time.Minute
	fpWindow        = 10 * time.Minute
	fpSampleEvery   = 10 * time.Second
	fpHostsEvery    = 15 * time.Second
	fpExtraAgents   = 9
	// fpDeadline bounds TestFootprint. The workflow step that runs it must keep its -timeout at least fpDeadline
	// plus 10 minutes, for the cleanups that stop the VMs.
	fpDeadline = 45 * time.Minute
)

// fpRun is what the steps of TestFootprint share.
type fpRun struct {
	ctx context.Context
	f   *fleet
	// extra are the hosts of the extra agents, in the order they were created.
	extra []core.Host
	// agent and controller are the RSS samples in bytes.
	agent, controller []int64
	// problems are the failures of the window that did not stop it: a refused host list, a host that went offline.
	problems []string
}

// TestFootprint is the footprint gate of spec section 9 (R76, S6): the real agent of an enrolled VM, idle with
// every module enabled, stays within fpAgentMax of resident memory, and the controller, which manages its own host,
// that VM and fpExtraAgents harness-only agents on that VM and has its host list requested every fpHostsEvery,
// stays within fpControllerMax. Both are sampled every fpSampleEvery for fpWindow after fpWarmup. The workflow
// selects it with -run, -os, -m2 and -fp.
func TestFootprint(t *testing.T) {
	if *osFlag == "" || *m2Flag == "" || !*fpFlag {
		t.Skip("no -os, -m2 or -fp given: the VM tests run in .github/workflows/harness.yml")
	}
	if _, err := os.Stat(filepath.Join(*m2Flag, fpBinaryName)); err != nil {
		t.Fatalf("%v; the build job builds it with -tags harnesskeys,harness", err)
	}
	if err := fpCheckLayout(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), fpDeadline)
	t.Cleanup(cancel)
	r := &fpRun{ctx: ctx, f: m5dLab(ctx, t)}
	t.Cleanup(func() { r.stopExtraAgents(t) })
	steps := []struct {
		name string
		gate bool // later steps need this one
		run  func(t *testing.T)
	}{
		{"extra-agents", true, r.extraAgents},
		{"warm-up", true, r.warmUp},
		{"window", true, r.window},
		{"budgets", false, r.budgets},
	}
	for _, s := range steps {
		if !t.Run(s.name, s.run) && s.gate {
			return
		}
	}
}

// extraAgents makes fpExtraAgents more hosts on the agent VM, each an agent of the harness build under its own
// --state-dir, run as a systemd-run unit. A host's modules but hostinfo are disabled before its agent first
// connects, so no background task of the firewall module starts beside the real agent's. The step checks that
// this held: every extra host lists only hostinfo enabled, its unit is active and its journal shows no reconcile
// pass; and that the real agent's host lists every module enabled.
func (r *fpRun) extraAgents(t *testing.T) {
	v := r.f.agent
	for _, cmd := range fpPrepareCmds() {
		m3Run(r.ctx, t, v, cmd)
	}
	if err := v.CopyFile(r.ctx, filepath.Join(*m2Flag, fpBinaryName), fpBinaryPath, 0o755); err != nil {
		t.Fatal(err)
	}
	for n := 1; n <= fpExtraAgents; n++ {
		var grant core.EnrollmentGrant
		input := core.HostCreateInput{Name: fmt.Sprintf("fp-%d", n), ControllerAddress: agentAddress}
		if _, err := r.f.api.Apply(r.ctx, "", "core.host.create/v1", input, &grant); err != nil {
			t.Fatal(err)
		}
		r.extra = append(r.extra, grant.Host)
		for _, id := range fpDisableTargets(grant.Host) {
			in := coreactions.ModuleInput{HostID: grant.Host.ID, ModuleID: id}
			if _, err := r.f.api.Apply(r.ctx, "", "core.module.disable/v1", in, nil); err != nil {
				t.Fatalf("disable %s on %s: %v", id, grant.Host.Name, err)
			}
		}
		tokenFile := fmt.Sprintf("/root/fp-token-%d", n)
		if _, err := v.RunInput(r.ctx, "sudo sh -c 'umask 077 && cat > "+tokenFile+"'", strings.NewReader(grant.Token)); err != nil {
			t.Fatal(err)
		}
		out, err := v.Run(r.ctx, fpEnrollCmd(n, tokenFile))
		m3Run(r.ctx, t, v, "sudo rm -f "+tokenFile)
		if err != nil {
			t.Fatalf("enroll extra agent %d: %v\n%s", n, err, out)
		}
		m3Run(r.ctx, t, v, fpRunCmd(n))
	}
	ids := make([]string, len(r.extra))
	for i, h := range r.extra {
		ids[i] = h.ID
	}
	hosts := waitHosts(r.ctx, t, r.f, 3*time.Minute, hostsAt(versionN, ids...))
	for n, h := range r.extra {
		listed, _ := findHost(hosts, h.ID)
		if err := fpOnlyHostinfoEnabled(listed); err != nil {
			t.Error(err)
		}
		if state := strings.TrimSpace(m3Run(r.ctx, t, v, "systemctl is-active "+fpUnit(n+1))); state != "active" {
			t.Errorf("%s is %q, want active", fpUnit(n+1), state)
		}
		journal := m3Run(r.ctx, t, v, "sudo journalctl -u "+fpUnit(n+1)+" --no-pager -o cat")
		if err := fpNoReconcile(journal); err != nil {
			t.Errorf("%s: %v", fpUnit(n+1), err)
		}
	}
	realAgent, _ := findHost(hosts, r.f.agentID)
	if err := fpAllEnabled(realAgent); err != nil {
		t.Error(err)
	}
	if err := fpFleetOnline(hosts, fpExtraAgents+2); err != nil {
		t.Error(err)
	}
}

// warmUp lets every process settle for fpWarmup.
func (r *fpRun) warmUp(t *testing.T) {
	select {
	case <-r.ctx.Done():
		t.Fatal(r.ctx.Err())
	case <-time.After(fpWarmup):
	}
}

// window runs the sampling plan: the RSS of the real agent and of the controller every fpSampleEvery and the
// controller's host list every fpHostsEvery, for fpWindow.
func (r *fpRun) window(t *testing.T) {
	start := time.Now()
	for _, ev := range fpSchedule(fpWindow, fpSampleEvery, fpHostsEvery) {
		select {
		case <-r.ctx.Done():
			t.Fatal(r.ctx.Err())
		case <-time.After(time.Until(start.Add(ev.At))):
		}
		switch ev.Kind {
		case fpSampleEvent:
			r.sample(t, ev.At)
		case fpHostsEvent:
			hosts, err := r.f.api.Hosts(r.ctx)
			if err == nil {
				err = fpFleetOnline(hosts, fpExtraAgents+2)
			}
			if err != nil {
				r.problems = append(r.problems, fmt.Sprintf("host list at %s: %v", ev.At, err))
			}
		}
	}
}

// sample reads both processes' resident memory. A failed read is logged and left out; budgets requires enough
// samples to make the maximum mean something.
func (r *fpRun) sample(t *testing.T, at time.Duration) {
	for _, s := range []struct {
		name string
		v    *vm.VM
		unit string
		into *[]int64
	}{
		{"agent", r.f.agent, update.AgentUnit, &r.agent},
		{"controller", r.f.controller, update.ControllerUnit, &r.controller},
	} {
		pid, err := fpMainPID(r.ctx, s.v, s.unit)
		var rss int64
		if err == nil {
			rss, err = fpRSS(r.ctx, s.v, pid)
		}
		if err != nil {
			t.Logf("%s at %s: no sample: %v", s.name, at, err)
			continue
		}
		*s.into = append(*s.into, rss)
		t.Logf("%s at %s: %d bytes", s.name, at, rss)
	}
}

// budgets compares the maxima with the budgets and reports the problems of the window.
func (r *fpRun) budgets(t *testing.T) {
	for _, p := range r.problems {
		t.Error(p)
	}
	want := fpSampleCount(fpWindow, fpSampleEvery)
	if err := fpCheckBudget("agent", r.agent, fpAgentMax, want); err != nil {
		t.Error(err)
	}
	if err := fpCheckBudget("controller", r.controller, fpControllerMax, want); err != nil {
		t.Error(err)
	}
}

// stopExtraAgents stops the extra agents' units, so that a failed run leaves no process on the VM, and logs each
// stop that fails for a unit that was loaded. It runs on a context of its own: the test's may have ended.
func (r *fpRun) stopExtraAgents(t *testing.T) {
	if r.f == nil || r.f.agent == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	for n := 1; n <= fpExtraAgents; n++ {
		if _, err := r.f.agent.Run(ctx, fpStopCmd(n)); err != nil && !strings.Contains(err.Error(), "not loaded") {
			t.Logf("stop %s: %v", fpUnit(n), err)
		}
	}
}

const (
	fpBinaryName = "darkpanel-" + versionN + "-state"
	// fpStateRoot is short on purpose: the longest path under a state directory is the agent socket,
	// <dir>/run/agent.sock, and a Unix socket path holds 108 bytes with its NUL.
	fpStateRoot = "/var/lib/dpx"
	// fpBinaryPath lies outside every path uninstall removes.
	fpBinaryPath = fpStateRoot + "/bin/darkpanel-state"
	fpSocketMax  = 107
	fpHostinfo   = "hostinfo"
)

// fpStateDir is the --state-dir of extra agent n.
func fpStateDir(n int) string { return fmt.Sprintf("%s/%d", fpStateRoot, n) }

// fpUnit is the systemd-run unit of extra agent n.
func fpUnit(n int) string { return fmt.Sprintf("dpx-agent-%d", n) }

// fpPrepareCmds make the state root and the directory of the extra agents' binary.
func fpPrepareCmds() []string {
	return []string{"sudo install -d -m 0755 " + fpStateRoot + " " + fpStateRoot + "/bin"}
}

// fpEnrollCmd enrolls extra agent n with the token in tokenFile.
func fpEnrollCmd(n int, tokenFile string) string {
	return fmt.Sprintf("sudo %s agent enroll --state-dir %s --token-file %s", fpBinaryPath, fpStateDir(n), tokenFile)
}

// fpRunCmd starts extra agent n as a transient unit.
func fpRunCmd(n int) string {
	return fmt.Sprintf("sudo systemd-run --unit=%s --collect %s agent run --state-dir %s", fpUnit(n), fpBinaryPath, fpStateDir(n))
}

// fpStopCmd stops extra agent n.
func fpStopCmd(n int) string { return "sudo systemctl stop " + fpUnit(n) }

// fpPIDCmd prints the main PID of unit, 0 when it is not running.
func fpPIDCmd(unit string) string { return "systemctl show -p MainPID --value " + unit }

// fpRSSCmd prints the resident memory of pid in kB.
func fpRSSCmd(pid int) string { return fmt.Sprintf("awk '/^VmRSS/{print $2}' /proc/%d/status", pid) }

// fpSocketProblem fails when the agent socket under the state directory dir is longer than a Unix socket path
// may be.
func fpSocketProblem(dir string) error {
	if sock := paths.Under(dir).AgentSocket; len(sock) > fpSocketMax {
		return fmt.Errorf("harness: the agent socket %s is %d bytes, over the %d a Unix socket path holds", sock, len(sock), fpSocketMax)
	}
	return nil
}

// fpCheckLayout applies fpSocketProblem to every extra agent's state directory.
func fpCheckLayout() error {
	for n := 1; n <= fpExtraAgents; n++ {
		if err := fpSocketProblem(fpStateDir(n)); err != nil {
			return err
		}
	}
	return nil
}

// fpParsePID reads the output of fpPIDCmd: a positive PID, as 0 means the unit has no running process.
func fpParsePID(out string) (int, error) {
	pid, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil || pid <= 0 {
		return 0, fmt.Errorf("harness: MainPID %q is not a running process", out)
	}
	return pid, nil
}

// fpParseRSS reads the output of fpRSSCmd, a number of kB that /proc counts in units of 1024 bytes, and returns
// bytes.
func fpParseRSS(out string) (int64, error) {
	kb, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	if err != nil || kb <= 0 {
		return 0, fmt.Errorf("harness: VmRSS %q is not a positive number of kB", out)
	}
	return kb * 1024, nil
}

// fpMainPID returns the main PID of unit on v.
func fpMainPID(ctx context.Context, v *vm.VM, unit string) (int, error) {
	out, err := v.Run(ctx, fpPIDCmd(unit))
	if err != nil {
		return 0, err
	}
	return fpParsePID(out)
}

// fpRSS returns the resident memory of pid on v in bytes.
func fpRSS(ctx context.Context, v *vm.VM, pid int) (int64, error) {
	out, err := v.Run(ctx, fpRSSCmd(pid))
	if err != nil {
		return 0, err
	}
	return fpParseRSS(out)
}

// fpSampleCount is the number of samples taken every every in window.
func fpSampleCount(window, every time.Duration) int {
	if every <= 0 || window < every {
		return 0
	}
	return int(window / every)
}

// fpCheckBudget fails when samples hold fewer than nine tenths of the want samples, or none, or when the largest
// of them is over limit.
func fpCheckBudget(name string, samples []int64, limit int64, want int) error {
	if least := want * 9 / 10; len(samples) == 0 || len(samples) < least {
		return fmt.Errorf("harness: %s: %d RSS samples, want at least %d of %d", name, len(samples), least, want)
	}
	if top := slices.Max(samples); top > limit {
		return fmt.Errorf("harness: %s: the largest of %d RSS samples is %d bytes, over the %d-byte budget", name, len(samples), top, limit)
	}
	return nil
}

// fpKind is what an event of the sampling plan does. The order of the kinds is the order of events due at the
// same time.
type fpKind int

const (
	fpSampleEvent fpKind = iota
	fpHostsEvent
)

// fpEvent is one event of the sampling plan, At after the window starts.
type fpEvent struct {
	At   time.Duration
	Kind fpKind
}

// fpSchedule returns an event at every multiple of each interval up to and including window, sorted by time with
// the sample before the host list at the same time. An interval of zero or less adds none.
func fpSchedule(window, sampleEvery, hostsEvery time.Duration) []fpEvent {
	var evs []fpEvent
	for _, s := range []struct {
		every time.Duration
		kind  fpKind
	}{{sampleEvery, fpSampleEvent}, {hostsEvery, fpHostsEvent}} {
		if s.every <= 0 {
			continue
		}
		for at := s.every; at <= window; at += s.every {
			evs = append(evs, fpEvent{At: at, Kind: s.kind})
		}
	}
	slices.SortFunc(evs, func(a, b fpEvent) int {
		if c := cmp.Compare(a.At, b.At); c != 0 {
			return c
		}
		return cmp.Compare(a.Kind, b.Kind)
	})
	return evs
}

// fpDisableTargets returns the sorted ids of h's listed modules other than hostinfo.
func fpDisableTargets(h core.Host) []string {
	var ids []string
	for _, m := range h.Modules {
		if m.ID != fpHostinfo {
			ids = append(ids, m.ID)
		}
	}
	slices.Sort(ids)
	return ids
}

// fpOnlyHostinfoEnabled fails unless h lists hostinfo enabled and every other module disabled.
func fpOnlyHostinfoEnabled(h core.Host) error {
	var bad []string
	listed := false
	for _, m := range h.Modules {
		if m.ID == fpHostinfo {
			listed = true
		}
		if m.Enabled != (m.ID == fpHostinfo) {
			bad = append(bad, fmt.Sprintf("%s enabled %v", m.ID, m.Enabled))
		}
	}
	if !listed {
		bad = append(bad, "no hostinfo module listed")
	}
	if len(bad) > 0 {
		return fmt.Errorf("harness: host %s, want only hostinfo enabled: %s", h.Name, strings.Join(bad, ", "))
	}
	return nil
}

// fpAllEnabled fails unless h lists modules and every one of them is enabled.
func fpAllEnabled(h core.Host) error {
	if len(h.Modules) == 0 {
		return fmt.Errorf("harness: host %s lists no module, want every module enabled", h.Name)
	}
	var off []string
	for _, m := range h.Modules {
		if !m.Enabled {
			off = append(off, m.ID)
		}
	}
	if len(off) > 0 {
		return fmt.Errorf("harness: host %s has %v disabled, want every module enabled", h.Name, off)
	}
	return nil
}

// fpFleetOnline fails unless hosts holds want hosts, each enrolled and online.
func fpFleetOnline(hosts []core.Host, want int) error {
	if len(hosts) != want {
		return fmt.Errorf("harness: the host list has %d hosts, want %d", len(hosts), want)
	}
	var down []string
	for _, h := range hosts {
		if h.State != core.HostEnrolled || !h.Online {
			down = append(down, h.Name)
		}
	}
	if len(down) > 0 {
		return fmt.Errorf("harness: hosts not enrolled and online: %s", strings.Join(down, ", "))
	}
	return nil
}

// fpNoReconcile fails on the first line of journal that mentions a reconcile, in any case.
func fpNoReconcile(journal string) error {
	for _, line := range strings.Split(journal, "\n") {
		if strings.Contains(strings.ToLower(line), "reconcile") {
			return fmt.Errorf("harness: the journal shows a reconcile pass: %q", line)
		}
	}
	return nil
}
