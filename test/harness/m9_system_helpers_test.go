//go:build harness

package harness

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/darkraise/darkpanel/internal/controller/coreactions"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/test/harness/vm"
)

var m9Flag = flag.Bool("m9", false, "run the M9 system module scenarios; needs -os and -m2")

// m9Deadline bounds one TestM9System scenario, each of which boots a lab of
// its own. The m9-system job runs them all in one step.
const m9Deadline = 40 * time.Minute

// m9ApplyTimeout covers the longest apply timeout of the system module, the
// 60 minutes of an upgrade, within the scenario's deadline.
const m9ApplyTimeout = 30 * time.Minute

// m9KeyringFile is the Docker repository keyring the system module carries;
// the scenarios write the same file so the module sees its own repository.
const m9KeyringFile = "../../internal/modules/system/docker_keyring.gpg"

const (
	m9KeyringPath = "/usr/share/keyrings/docker-archive-keyring.gpg"
	m9ListPath    = "/etc/apt/sources.list.d/docker.list"
)

// m9DockerPackages are the packages system.docker.engine-install installs.
var m9DockerPackages = []string{"docker-ce", "docker-ce-cli", "containerd.io", "docker-buildx-plugin", "docker-compose-plugin"}

// m9AgentScript provisions the M9 agent like TargetScript, without Docker
// Engine or tailscaled: the Docker scenarios install Docker themselves. It
// installs unattended-upgrades, which system.autoupdates.set needs and not
// every cloud image ships, and stops the apt timers, whose runs would hold the
// dpkg lock under a scenario's apt actions.
func m9AgentScript() string {
	return scriptHeader + `apt_get update
apt_get install --no-install-recommends ca-certificates curl sqlite3 ufw nftables unattended-upgrades
ufw allow 22/tcp
` + agentRule + `ufw --force enable
systemctl enable --now ufw
systemctl disable --now apt-daily.timer apt-daily-upgrade.timer
`
}

// m9Configs is m5Configs with the agent provisioned by m9AgentScript.
func m9Configs(lab *Lab, o OS, targetImg, scannerImg string, key ssh.Signer) []vm.Config {
	cfgs := m5Configs(lab, o, targetImg, scannerImg, key)
	cfgs[1].Provision = m9AgentScript()
	return cfgs
}

// m9VM is the agent of an M9 lab, with the API session of its controller.
type m9VM struct {
	ctx    context.Context
	f      *fleet
	name   string // the agent's hostname, the phrase of its irreversible actions
	addr   string // the agent's internet-link address
	os     string
	hostID string
}

// m9Read holds the fields the scenarios read from the system and firewall
// resources; each resource fills its own.
type m9Read struct {
	Upgradable      []struct{ Name, From, To string } `json:"upgradable"`
	DpkgInterrupted bool                              `json:"dpkgInterrupted"`
	IntegrationOn   bool                              `json:"enabled"` // firewall.docker
	State           string                            `json:"state"`
	UpdateAvailable bool                              `json:"updateAvailable"`
	Conflicts       []string                          `json:"conflicts"`
	Services        []string                          `json:"services"`
}

// bootM9VM boots a lab of -os, installs the harness build on the controller
// and the agent, enrolls the agent and returns it. It skips the test unless
// -os, -m2 and -m9 are given.
func bootM9VM(t *testing.T) *m9VM {
	t.Helper()
	if *osFlag == "" || *m2Flag == "" || !*m9Flag {
		t.Skip("no -os, -m2 or -m9 given: the VM tests run in .github/workflows/harness.yml")
	}
	ctx, cancel := context.WithTimeout(context.Background(), m9Deadline)
	t.Cleanup(cancel)
	lab := NewLab(t)
	o, targetImg, scannerImg := harnessImages(ctx, t)
	key, err := vm.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	vms := lab.Boot(ctx, t, m9Configs(lab, o, targetImg, scannerImg, key))
	f := &fleet{os: o, controller: vms[0], agent: vms[1], scanner: vms[2]}
	serveReleases(ctx, t, f)
	scenarioInstall(ctx, t, f)
	v := &m9VM{ctx: ctx, f: f, addr: agentIPv4, os: o.ID, hostID: f.agentID}
	v.name = strings.TrimSpace(v.sshMust(t, "hostname"))
	m9Current = v
	t.Cleanup(func() { m9Current = nil })
	return v
}

// onlyOn skips the test unless -os is one of oses.
func onlyOn(t *testing.T, oses ...string) {
	t.Helper()
	if !slices.Contains(oses, *osFlag) {
		t.Skipf("runs only on %s", strings.Join(oses, ", "))
	}
}

// sshMust runs cmd as root on the agent and returns its output.
func (v *m9VM) sshMust(t *testing.T, cmd string) string {
	t.Helper()
	out, err := v.f.agent.Run(v.ctx, "sudo bash -c "+m3Quote(cmd))
	if err != nil {
		t.Fatalf("%s: %v\n%s", cmd, err, out)
	}
	return out
}

// sshFails runs cmd as root on the agent and fails the test if it succeeds.
func (v *m9VM) sshFails(t *testing.T, cmd string) {
	t.Helper()
	if out, err := v.f.agent.Run(v.ctx, "sudo bash -c "+m3Quote(cmd)); err == nil {
		t.Fatalf("%s succeeded, want it to fail\n%s", cmd, out)
	}
}

// plan plans version 1 of action on the agent and returns its diff, one
// line per entry. A refused plan or a failed guard is an error that names
// the refusal.
func (v *m9VM) plan(action, input string) (string, error) {
	p, err := m4Plan(v.ctx, v.f.api, v.hostID, action, json.RawMessage(input))
	if err != nil {
		return "", err
	}
	if err := m9xGuards(action, p.Guards); err != nil {
		return "", err
	}
	return m9xDiff(p.Diff), nil
}

// m9xGuards is an error naming the first failed guard.
func m9xGuards(action string, guards []core.Guard) error {
	for _, g := range guards {
		if !g.OK {
			return fmt.Errorf("harness: plan %s: guard %s failed: %s", action, g.ID, g.Message)
		}
	}
	return nil
}

// m9xDiff joins a plan's diff lines as "<op> <text>".
func m9xDiff(diff []core.DiffLine) string {
	lines := make([]string, len(diff))
	for i, d := range diff {
		lines[i] = d.Op + " " + d.Text
	}
	return strings.Join(lines, "\n")
}

// m9xPhrase checks the confirmation phrase a plan asks for against want; an
// empty want accepts any.
func m9xPhrase(action, got, want string) error {
	if want != "" && got != want {
		return fmt.Errorf("harness: plan %s asks for the phrase %q, want %q", action, got, want)
	}
	return nil
}

// apply plans version 1 of action on the agent, checks that the plan asks
// for phrase unless phrase is empty, applies it confirmed with the phrase it
// names, waits for the change to settle and fails the test unless it
// completed or was confirmed. It returns the change id.
func (v *m9VM) apply(t *testing.T, action, input, phrase string) string {
	t.Helper()
	p, err := m4Plan(v.ctx, v.f.api, v.hostID, action, json.RawMessage(input))
	if err != nil {
		t.Fatal(err)
	}
	if err := m9xGuards(action, p.Guards); err != nil {
		t.Fatal(err)
	}
	if err := m9xPhrase(action, p.Confirmation, phrase); err != nil {
		t.Fatal(err)
	}
	path, err := actionPath(v.hostID, action+"/v1", "apply")
	if err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"planId": p.PlanID, "confirmNow": true}
	if p.Confirmation != "" {
		body["confirmation"] = p.Confirmation
	}
	var applied Change
	if err := v.f.api.do(v.ctx, http.MethodPost, path, body, &applied); err != nil {
		t.Fatalf("apply %s: %v", action, err)
	}
	ch, err := v.settle(applied.ID, m9ApplyTimeout, m5Terminal)
	if err != nil {
		t.Fatalf("change %s of %s: %v", applied.ID, action, err)
	}
	if ch.State != core.StateCompleted && ch.State != core.StateConfirmed {
		t.Fatalf("%s ended %s, want %s or %s: error %s", action, ch.State, core.StateCompleted, core.StateConfirmed, m3JSON(ch.Error))
	}
	return ch.ID
}

// applyComposite plans version 1 of the controller-side action, which takes
// its host in input, applies it with a confirm window of window and returns
// the change id once the change awaits confirmation or settled.
func (v *m9VM) applyComposite(t *testing.T, action, input string, window time.Duration) string {
	t.Helper()
	p, err := m4Plan(v.ctx, v.f.api, "", action, json.RawMessage(input))
	if err != nil {
		t.Fatal(err)
	}
	if err := m9xGuards(action, p.Guards); err != nil {
		t.Fatal(err)
	}
	path, err := actionPath("", action+"/v1", "apply")
	if err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"planId": p.PlanID, "confirmTimeoutSeconds": int64(window / time.Second)}
	if p.Confirmation != "" {
		body["confirmation"] = p.Confirmation
	}
	var applied Change
	if err := v.f.api.do(v.ctx, http.MethodPost, path, body, &applied); err != nil {
		t.Fatalf("apply %s: %v", action, err)
	}
	ch, err := v.settle(applied.ID, m9ApplyTimeout, func(state string) bool {
		return state == core.StateAwaitingConfirmation || m5Terminal(state)
	})
	if err != nil {
		t.Fatalf("change %s of %s: %v", applied.ID, action, err)
	}
	return ch.ID
}

// settle polls change id until done accepts its state, for up to d.
func (v *m9VM) settle(id string, d time.Duration, done func(string) bool) (Change, error) {
	var ch Change
	err := eventually(v.ctx, d, func(ctx context.Context) error {
		c, err := v.f.api.m3Change(ctx, id)
		if err != nil {
			return err
		}
		if !done(c.State) {
			return fmt.Errorf("harness: change %s is %s", id, c.State)
		}
		ch = c
		return nil
	})
	return ch, err
}

// confirm confirms the change, which must be awaiting confirmation, and fails
// the test unless it is kept: a composite ends completed once its steps are
// confirmed, a plain change ends confirmed.
func (v *m9VM) confirm(t *testing.T, change string) {
	t.Helper()
	ch, err := v.f.api.m3Change(v.ctx, change)
	if err != nil {
		t.Fatal(err)
	}
	if ch.State != core.StateAwaitingConfirmation {
		t.Fatalf("change %s is %s, want %s: error %s", change, ch.State, core.StateAwaitingConfirmation, m3JSON(ch.Error))
	}
	if _, err := v.f.api.m3dDockConfirm(v.ctx, change); err != nil {
		t.Fatalf("confirm %s: %v", change, err)
	}
	if ch, err = v.settle(change, m9ApplyTimeout, m5Terminal); err != nil {
		t.Fatalf("change %s: %v", change, err)
	}
	if ch.State != core.StateCompleted && ch.State != core.StateConfirmed {
		t.Fatalf("change %s ended %s after its confirmation, want %s or %s: error %s",
			change, ch.State, core.StateCompleted, core.StateConfirmed, m3JSON(ch.Error))
	}
}

// awaitState waits up to d for the change to reach state, failing the test
// when it settles in another.
func (v *m9VM) awaitState(t *testing.T, change, state string, d time.Duration) {
	t.Helper()
	ch, err := v.settle(change, d, func(s string) bool { return s == state || m5Terminal(s) })
	if err != nil {
		t.Fatalf("change %s: %v", change, err)
	}
	if ch.State != state {
		t.Fatalf("change %s ended %s, want %s: error %s", change, ch.State, state, m3JSON(ch.Error))
	}
}

// undo undoes the change and fails the test unless it ends reverted.
func (v *m9VM) undo(t *testing.T, change string) {
	t.Helper()
	if err := m4Undo(v.ctx, v.f.api, change); err != nil {
		t.Fatal(err)
	}
}

// readJSON reads the resource module.resource of the agent.
func (v *m9VM) readJSON(t *testing.T, module, resource string) m9Read {
	t.Helper()
	var r m9Read
	if err := v.f.api.Resource(v.ctx, v.hostID, module+"."+resource, &r); err != nil {
		t.Fatal(err)
	}
	return r
}

// auditFor returns the audit entries of the change, one line each with the
// action, phase, result and input.
func (v *m9VM) auditFor(t *testing.T, change string) string {
	t.Helper()
	var list coreactions.AuditList
	in := coreactions.AuditListInput{HostID: v.hostID, Limit: coreactions.MaxAuditLimit}
	if err := v.f.api.Read(v.ctx, "", "core.audit.list/v1", in, &list); err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, e := range list.Entries {
		if e.ChangeID != nil && *e.ChangeID == change {
			lines = append(lines, e.Action+" "+e.Phase+" "+e.Result+" "+string(e.Input))
		}
	}
	return strings.Join(lines, "\n")
}

// host returns the agent's host as the controller lists it.
func (v *m9VM) host(ctx context.Context) (core.Host, error) {
	hosts, err := v.f.api.Hosts(ctx)
	if err != nil {
		return core.Host{}, err
	}
	h, ok := findHost(hosts, v.hostID)
	if !ok {
		return core.Host{}, fmt.Errorf("harness: host %s is not listed", v.hostID)
	}
	return h, nil
}

// m9xAvailable is nil when module is available on h.
func m9xAvailable(h core.Host, module string) error {
	for _, m := range h.Modules {
		if m.ID != module {
			continue
		}
		if !m.Available {
			reason := ""
			if m.Reason != nil {
				reason = *m.Reason
			}
			return fmt.Errorf("harness: module %s is unavailable on %s: %s", module, h.Name, reason)
		}
		return nil
	}
	return fmt.Errorf("harness: host %s lists no module %s", h.Name, module)
}

// waitModuleAvailable waits up to d for module to become available on the
// agent.
func (v *m9VM) waitModuleAvailable(t *testing.T, module string, d time.Duration) {
	t.Helper()
	err := eventually(v.ctx, d, func(ctx context.Context) error {
		h, err := v.host(ctx)
		if err != nil {
			return err
		}
		return m9xAvailable(h, module)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// waitOnlineState waits up to d for the agent's Online to be online.
func (v *m9VM) waitOnlineState(t *testing.T, online bool, d time.Duration) {
	t.Helper()
	err := eventually(v.ctx, d, func(ctx context.Context) error {
		h, err := v.host(ctx)
		if err != nil {
			return err
		}
		if h.Online != online {
			return fmt.Errorf("harness: host %s online %t, want %t", h.Name, h.Online, online)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// waitOffline waits up to d for the controller to list the agent offline.
func (v *m9VM) waitOffline(t *testing.T, d time.Duration) { t.Helper(); v.waitOnlineState(t, false, d) }

// waitOnline waits up to d for the controller to list the agent online.
func (v *m9VM) waitOnline(t *testing.T, d time.Duration) { t.Helper(); v.waitOnlineState(t, true, d) }

// m9xBootTime parses the btime line of /proc/stat.
func m9xBootTime(stat string) (time.Time, error) {
	for line := range strings.SplitSeq(stat, "\n") {
		if rest, ok := strings.CutPrefix(line, "btime "); ok {
			s, err := strconv.ParseInt(strings.TrimSpace(rest), 10, 64)
			if err != nil {
				return time.Time{}, fmt.Errorf("harness: /proc/stat btime %q: %w", rest, err)
			}
			return time.Unix(s, 0), nil
		}
	}
	return time.Time{}, errors.New("harness: /proc/stat has no btime line")
}

// bootTime reads the agent's boot time, retrying while SSH is down after a
// reboot.
func (v *m9VM) bootTime(t *testing.T) time.Time {
	t.Helper()
	var at time.Time
	err := eventually(v.ctx, 5*time.Minute, func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		out, err := v.f.agent.Run(ctx, "cat /proc/stat")
		if err != nil {
			return err
		}
		at, err = m9xBootTime(out)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return at
}

// m9xSecondVersion returns the version on the second line of apt-cache
// madison's output, the newest one older than the candidate, or "".
func m9xSecondVersion(madison string) string {
	lines := strings.Split(strings.TrimSpace(madison), "\n")
	if len(lines) < 2 {
		return ""
	}
	fields := strings.Split(lines[1], "|")
	if len(fields) < 2 {
		return ""
	}
	return strings.TrimSpace(fields[1])
}

// m9xDockerList is the list file system.docker.engine-install writes.
func m9xDockerList(arch, id, codename string) string {
	return fmt.Sprintf("deb [arch=%s signed-by=%s] https://download.docker.com/linux/%s %s stable\n", arch, m9KeyringPath, id, codename)
}

// dockerRepo writes Docker's repository files as the system module writes
// them and refreshes the package lists.
func (v *m9VM) dockerRepo(t *testing.T) {
	t.Helper()
	if err := v.f.agent.CopyFile(v.ctx, m9KeyringFile, m9KeyringPath, 0o644); err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(v.sshMust(t, `. /etc/os-release && echo "$(dpkg --print-architecture) $ID $VERSION_CODENAME"`))
	if len(fields) != 3 {
		t.Fatalf("os-release: %q", fields)
	}
	list := m9xDockerList(fields[0], fields[1], fields[2])
	v.sshMust(t, "printf %s "+m3Quote(list)+" > "+m9ListPath+" && apt-get -o DPkg::Lock::Timeout=300 -q update")
}

// olderDockerVersion returns the docker-ce version before the newest one in
// Docker's repository, which it sets up.
func (v *m9VM) olderDockerVersion(t *testing.T) string {
	t.Helper()
	v.dockerRepo(t)
	older := m9xSecondVersion(v.sshMust(t, "apt-cache madison docker-ce"))
	if older == "" {
		t.Fatal("Docker's repository offers one docker-ce version only")
	}
	return older
}

// installDockerVersion installs Docker Engine from Docker's repository over
// SSH: docker-ce and docker-ce-cli at version, or the newest when version is
// empty.
func (v *m9VM) installDockerVersion(t *testing.T, version string) {
	t.Helper()
	v.dockerRepo(t)
	names := slices.Clone(m9DockerPackages)
	if version != "" {
		names[0], names[1] = "docker-ce="+version, "docker-ce-cli="+version
	}
	v.sshMust(t, "DEBIAN_FRONTEND=noninteractive apt-get -o DPkg::Lock::Timeout=300 -y -q install --allow-downgrades "+m3dxShell(names...))
	v.sshMust(t, "systemctl is-active docker")
}

// uninstallDocker turns the firewall's Docker integration off when it is on,
// then uninstalls Docker Engine through the system module.
func (v *m9VM) uninstallDocker(t *testing.T, purge bool) {
	t.Helper()
	if v.readJSON(t, "firewall", "docker").IntegrationOn {
		if err := m3dDockSettle(v.ctx, t, v.f, "firewall.docker.disable", struct{}{}, m3dxCleanupRetryable); err != nil {
			t.Fatal(err)
		}
	}
	phrase := v.name
	if purge {
		phrase = "purge docker on " + v.name
	}
	v.apply(t, "system.docker.uninstall", `{"purge":`+strconv.FormatBool(purge)+`}`, phrase)
}

// scanPort reports whether the scanner finds port open on addr over TCP.
func scanPort(t *testing.T, addr string, port int) bool {
	t.Helper()
	v := m9Scanner(t)
	if port < 1 || port > 65535 {
		t.Fatalf("harness: port %d is outside 1 to 65535", port)
	}
	ports := m3dScan(v.ctx, t, v.f.scanner, ScanOpts{Family: "-4", Ports: []uint16{uint16(port)}, Target: addr}) //nolint:gosec // G115: port is checked to be 1 to 65535 above
	return slices.ContainsFunc(ports, func(p PortState) bool { return int(p.Port) == port && p.State == "open" })
}

// m9Current is the m9VM of the running scenario, which scanPort scans from;
// the scenarios run one at a time (-vms 3 holds one lab).
var m9Current *m9VM

// m9Scanner returns the running scenario's VM.
func m9Scanner(t *testing.T) *m9VM {
	t.Helper()
	if m9Current == nil {
		t.Fatal("harness: scanPort runs only inside an M9 scenario")
	}
	return m9Current
}

// m9xDowngradeOnly reports whether an apt-get -s simulation changes pkg
// alone and removes nothing.
func m9xDowngradeOnly(sim, pkg string) bool {
	insts := 0
	for line := range strings.SplitSeq(sim, "\n") {
		switch {
		case strings.HasPrefix(line, "Remv "):
			return false
		case strings.HasPrefix(line, "Inst "):
			if f := strings.Fields(line); len(f) < 2 || f[1] != pkg {
				return false
			}
			insts++
		}
	}
	return insts == 1
}
