//go:build harness

package harness

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	mrand "math/rand/v2"
	"net"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/controller/listen"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/install"
	"github.com/darkraise/darkpanel/internal/paths"
	"github.com/darkraise/darkpanel/internal/update"
	"github.com/darkraise/darkpanel/test/harness/release"
	"github.com/darkraise/darkpanel/test/harness/vm"
)

var m2Flag = flag.String("m2", "", "directory of make harness-builds (darkpanel-<version> and install.sh); empty skips TestM2")

// The M2 fleet (spec section 13): the controller has M1's target addresses,
// the agent the next ones, and both have a private link that stands in for
// the tailnet.
const (
	agentIPv4      = "198.18.0.11"
	agentIPv6      = "2001:db8:d0::11"
	controllerPriv = "10.77.0.10"
	agentPriv      = "10.77.0.11"
)

// The harness releases: N, then one minor up a good build, a build
// that fails its health check, one that crashes at start, a good build for
// the reboot and the frozen runner, and one that also adds a migration.
const (
	versionN         = "0.2.0"
	versionGood      = "0.3.0"
	versionBroken    = "0.3.1"
	versionCrash     = "0.3.2"
	versionReboot    = "0.3.3"
	versionMigration = "0.3.4"
	releaseChannel   = "stable"
)

var m2Versions = []string{versionN, versionGood, versionBroken, versionCrash, versionReboot, versionMigration}

const (
	// harnessKey is the committed key the harnesskeys builds trust.
	harnessKey = "../../internal/update/trustedkeys/testdata/harness.key"
	// releaseURL is the release base URL a harnesskeys build embeds: the
	// scanner, which serves releaseDir with releaseUnit.
	releaseURL  = "http://" + scannerIPv4 + ":8000"
	releaseDir  = "/srv/harness-releases"
	releaseUnit = "harness-releases"
	// installScript is where the targets keep the harness's install.sh,
	// outside every path uninstall removes.
	installScript = "/var/tmp/install.sh"
	// enrollFile holds an enrollment token on the agent while it is used.
	enrollFile = "/root/enroll-token"
	adminName  = "admin"
	// apiURL is the controller's loopback listener, which the harness reaches
	// through the controller VM's SSH connection.
	apiURL = "http://127.0.0.1:8080"
	// m2Deadline and TestM1's testDeadline together stay under the workflow's
	// 100-minute go test timeout, so the cleanups that stop the VMs still
	// run.
	m2Deadline = 55 * time.Minute
)

// agentAddress is where agents dial the controller: its private-link address.
var agentAddress = net.JoinHostPort(controllerPriv, strconv.Itoa(listen.AgentPort))

// fleet is one OS's M2 run: the VMs, the API session, and the hosts and
// admin the scenarios create.
type fleet struct {
	os                         OS
	controller, agent, scanner *vm.VM
	api                        *API
	localID, agentID           string
	password, totpSecret       string
	// lastCode is when the newest TOTP code was used. The controller never
	// accepts a step twice, so the next code must come from a later step.
	lastCode time.Time
}

func TestM2(t *testing.T) {
	if *osFlag == "" || *m2Flag == "" {
		t.Skip("no -os or -m2 given: the VM tests run in .github/workflows/harness.yml")
	}
	ctx, cancel := context.WithTimeout(context.Background(), m2Deadline)
	defer cancel()
	f := bootFleet(ctx, t)
	serveReleases(ctx, t, f)
	steps := []struct {
		name string
		gate bool // later steps need this one
		run  func(t *testing.T)
	}{
		{"install", true, func(t *testing.T) { scenarioInstall(ctx, t, f) }},
		{"self-management", true, func(t *testing.T) { scenarioSelfManagement(ctx, t, f) }},
		{"offline", false, func(t *testing.T) { scenarioOffline(ctx, t, f) }},
		{"revoke", true, func(t *testing.T) { scenarioRevoke(ctx, t, f) }},
	}
	for _, s := range steps {
		if !t.Run(s.name, s.run) && s.gate {
			return
		}
	}
}

// bootFleet boots the controller, the agent and the scanner of -os. All
// three share the internet link; the controller and the agent also share
// the private link.
func bootFleet(ctx context.Context, t *testing.T) *fleet {
	t.Helper()
	o, targetImg, scannerImg := harnessImages(ctx, t)
	dir := t.TempDir()
	if *workdirFlag != "" {
		// TestM1's VMs used the run directory first; a directory of its own
		// keeps their logs.
		dir = filepath.Join(*workdirFlag, "m2")
	}
	key, err := vm.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	port := 20000 + mrand.IntN(20000) //nolint:gosec // G404: a link port, not a secret
	privPort := port + 1
	t.Logf("%s: run directory %s, internet link %s:%d, private link %s:%d", o.ID, dir, vm.McastGroup, port, vm.McastGroup, privPort)
	vms := bootVMs(ctx, t, dir, []vm.Config{
		{
			Name: "controller", Dir: dir, Image: targetImg, MemoryMiB: 2048, CPUs: 2,
			MgmtMAC: "52:54:00:d0:01:10", InetMAC: "52:54:00:d0:00:10", PrivMAC: "52:54:00:d0:02:10",
			InetAddrs: []string{targetIPv4 + "/15", targetIPv6 + "/64"}, PrivAddrs: []string{controllerPriv + "/24"},
			McastPort: port, PrivMcastPort: privPort, Provision: TargetScript(o), Key: key,
		},
		{
			Name: "agent", Dir: dir, Image: targetImg, MemoryMiB: 2048, CPUs: 2,
			MgmtMAC: "52:54:00:d0:01:11", InetMAC: "52:54:00:d0:00:11", PrivMAC: "52:54:00:d0:02:11",
			InetAddrs: []string{agentIPv4 + "/15", agentIPv6 + "/64"}, PrivAddrs: []string{agentPriv + "/24"},
			McastPort: port, PrivMcastPort: privPort, Provision: TargetScript(o), Key: key,
		},
		{
			Name: "scanner", Dir: dir, Image: scannerImg, MemoryMiB: 2048, CPUs: 1,
			MgmtMAC: "52:54:00:d0:01:20", InetMAC: "52:54:00:d0:00:20",
			InetAddrs: []string{scannerIPv4 + "/15", scannerIPv6 + "/64"},
			McastPort: port, Provision: ScannerScript(), Key: key,
		},
	})
	return &fleet{os: o, controller: vms[0], agent: vms[1], scanner: vms[2]}
}

// serveReleases signs the builds of -m2 as the harness releases, copies them
// to the scanner and serves them there as the release base URL.
func serveReleases(ctx context.Context, t *testing.T, f *fleet) {
	t.Helper()
	key, err := release.LoadKey(harnessKey)
	if err != nil {
		t.Fatal(err)
	}
	versions := make([]release.Version, 0, len(m2Versions))
	for _, v := range m2Versions {
		versions = append(versions, release.Version{Version: v, Channel: releaseChannel, Binary: filepath.Join(*m2Flag, "darkpanel-"+v)})
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

// copyTree copies the directory src to dst on v as root, files 0644.
func copyTree(ctx context.Context, v *vm.VM, src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := path.Join(dst, filepath.ToSlash(rel))
		if d.IsDir() {
			_, err := v.Run(ctx, "sudo mkdir -p "+target)
			return err
		}
		return v.CopyFile(ctx, p, target, 0o644)
	})
}

// scenarioInstall is scenario 1: install.sh installs N as the controller and
// as an agent enrolled with a token the API created, and both report N. The
// controller runs as the darkpanel user, its agent as root.
func scenarioInstall(ctx context.Context, t *testing.T, f *fleet) {
	for _, v := range []*vm.VM{f.controller, f.agent} {
		if err := v.CopyFile(ctx, filepath.Join(*m2Flag, "install.sh"), installScript, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	installRole(ctx, t, f.controller, versionN, "--role controller --agent-listen "+agentAddress)
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
		return hostsAt(versionN, h.ID)(hosts)
	})
	local, _ := localHost(hosts)
	f.localID = local.ID

	var grant core.EnrollmentGrant
	if _, err := f.api.Apply(ctx, "", "core.host.create/v1", core.HostCreateInput{Name: "agent", ControllerAddress: agentAddress}, &grant); err != nil {
		t.Fatal(err)
	}
	f.agentID = grant.Host.ID
	writeToken(ctx, t, f.agent, grant.Token)
	installRole(ctx, t, f.agent, versionN, "--role agent --token-file "+enrollFile)
	removeToken(ctx, t, f.agent)
	waitHosts(ctx, t, f, 2*time.Minute, hostsAt(versionN, f.localID, f.agentID))
	for _, v := range []*vm.VM{f.controller, f.agent} {
		requireBinaryVersion(ctx, t, v, paths.Production().Binary(), versionN)
	}
}

// installRole runs install.sh for version with args, quoting the services'
// journals when it fails.
func installRole(ctx context.Context, t *testing.T, v *vm.VM, version, args string) {
	t.Helper()
	cmd := "sudo sh " + installScript + " --version " + version + " --base-url " + releaseURL + " " + args
	if _, err := v.Run(ctx, cmd); err != nil {
		journal, _ := v.Run(ctx, "sudo journalctl -u "+update.ControllerUnit+" -u "+update.AgentUnit+" --no-pager -n 80")
		t.Fatalf("%v\njournal:\n%s", err, journal)
	}
}

// createAdmin creates the first admin over the controller socket, as the
// README's first-admin section does, and activates it. Without a terminal
// the CLI reads the password, and then the code, from standard input; the
// code depends on the secret it prints, so the first run gets the password
// alone, prints the secret and stops at the missing code, naming user
// activate, which the second run is.
func createAdmin(ctx context.Context, t *testing.T, f *fleet) {
	t.Helper()
	f.password = rand.Text()
	out, err := f.controller.RunInput(ctx, "sudo darkpanel user create --username "+adminName+" --yes", strings.NewReader(f.password+"\n"))
	if err == nil || !strings.Contains(err.Error(), "no code given") {
		t.Fatalf("user create = %v, want it to stop at the missing code\n%s", err, out)
	}
	if f.totpSecret, err = totpSecret(out); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	code := nextCode(ctx, t, f)
	if out, err := f.controller.RunInput(ctx, "sudo darkpanel user activate --username "+adminName+" --yes", strings.NewReader(code+"\n")); err != nil {
		t.Fatalf("user activate: %v\n%s", err, out)
	}
}

// login opens a new API session as the admin.
func login(ctx context.Context, t *testing.T, f *fleet) {
	t.Helper()
	f.api = NewAPI(apiURL, f.controller.DialContext)
	if err := f.api.Login(ctx, adminName, f.password, nextCode(ctx, t, f)); err != nil {
		t.Fatal(err)
	}
}

// nextCode waits for a TOTP step after the last code used and returns that
// step's code.
func nextCode(ctx context.Context, t *testing.T, f *fleet) string {
	t.Helper()
	select {
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	case <-time.After(time.Until(nextStep(f.lastCode))):
	}
	f.lastCode = time.Now()
	code, err := auth.TOTPCode(f.totpSecret, f.lastCode)
	if err != nil {
		t.Fatal(err)
	}
	return code
}

// nextStep is the start of the TOTP step after the one at t.
func nextStep(t time.Time) time.Time {
	period := int64(auth.TOTPPeriod / time.Second)
	return time.Unix((t.Unix()/period+1)*period, 0)
}

// totpSecret returns the secret user create prints on its line "Secret: ".
func totpSecret(out string) (string, error) {
	for line := range strings.SplitSeq(out, "\n") {
		if s, ok := strings.CutPrefix(strings.TrimSpace(line), "Secret: "); ok && s != "" {
			return s, nil
		}
	}
	return "", errors.New("harness: user create printed no Secret: line")
}

// writeToken writes an enrollment token to enrollFile on v, owned by root
// with mode 0600, as agent enroll and install.sh require. It goes through
// standard input, so no command line or error quotes it.
func writeToken(ctx context.Context, t *testing.T, v *vm.VM, token string) {
	t.Helper()
	if _, err := v.RunInput(ctx, "sudo sh -c 'umask 077 && cat > "+enrollFile+"'", strings.NewReader(token)); err != nil {
		t.Fatal(err)
	}
}

func removeToken(ctx context.Context, t *testing.T, v *vm.VM) {
	t.Helper()
	if _, err := v.Run(ctx, "sudo rm -f "+enrollFile); err != nil {
		t.Fatal(err)
	}
}

// scenarioSelfManagement is scenario 2: hostinfo.summary of the controller's
// own host and of the agent host through the API, both over mTLS; the host
// list shows both online with versions. Only the controller listens beyond
// loopback, on its private address alone, and the scans find only 22/tcp on
// both hosts.
func scenarioSelfManagement(ctx context.Context, t *testing.T, f *fleet) {
	hosts := waitHosts(ctx, t, f, time.Minute, hostsAt(versionN, f.localID, f.agentID))
	for _, h := range hosts {
		if h.LastSeenAt == nil {
			t.Errorf("host %s shows no last-seen time", h.Name)
		}
	}
	for id, name := range map[string]string{f.localID: "controller", f.agentID: "agent"} {
		var sum struct {
			Hostname string `json:"hostname"`
		}
		if err := f.api.Resource(ctx, id, "hostinfo.summary", &sum); err != nil {
			t.Error(err)
		} else if sum.Hostname != name {
			t.Errorf("hostinfo.summary of host %s names %q, want %q", id, sum.Hostname, name)
		}
	}
	for _, c := range []struct {
		v    *vm.VM
		want []string // darkpanel's listeners beyond loopback
	}{{f.controller, []string{agentAddress}}, {f.agent, nil}} {
		out, err := c.v.Run(ctx, "sudo ss -Htlnp")
		if err != nil {
			t.Fatal(err)
		}
		if _, public := darkpanelListeners(out); !slices.Equal(public, c.want) {
			t.Errorf("darkpanel listens beyond loopback on %v, want %v\n%s", public, c.want, out)
		}
	}
	for _, s := range []struct{ name, family, addr string }{
		{"scan-controller-ipv4", "", targetIPv4}, {"scan-controller-ipv6", "-6", targetIPv6},
		{"scan-agent-ipv4", "", agentIPv4}, {"scan-agent-ipv6", "-6", agentIPv6},
	} {
		t.Run(s.name, func(t *testing.T) { checkScan(ctx, t, f.scanner, s.family, s.addr) })
	}
}

// scenarioOffline is scenario 3: with its service stopped the agent host is
// offline within 60 s and an action on it fails at once with host.offline;
// started again, it returns.
func scenarioOffline(ctx context.Context, t *testing.T, f *fleet) {
	if _, err := f.agent.Run(ctx, "sudo systemctl stop "+update.AgentUnit); err != nil {
		t.Fatal(err)
	}
	waitHosts(ctx, t, f, time.Minute, func(hosts []core.Host) error {
		if h, ok := findHost(hosts, f.agentID); !ok || h.Online {
			return fmt.Errorf("host %s is listed %v, online %v", f.agentID, ok, h.Online)
		}
		return nil
	})
	start := time.Now()
	err := f.api.Read(ctx, f.agentID, "core.update.status/v1", core.UpdateStatusInput{}, nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != string(errcode.HostOffline) {
		t.Errorf("an action on the offline host = %v, want %s", err, errcode.HostOffline)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("the action on the offline host took %v; it must fail at once", elapsed)
	}
	if _, err := f.agent.Run(ctx, "sudo systemctl start "+update.AgentUnit); err != nil {
		t.Fatal(err)
	}
	waitHosts(ctx, t, f, 2*time.Minute, hostsAt(versionN, f.agentID))
}

// scenarioRevoke is scenario 4: core.host.revoke closes the agent's live
// session and its redials are refused, so it stays offline while its
// journal shows failed sessions; core.host.token and agent enroll re-enroll
// it.
func scenarioRevoke(ctx context.Context, t *testing.T, f *fleet) {
	waitHosts(ctx, t, f, 2*time.Minute, hostsAt(versionN, f.agentID))
	since := vmNow(ctx, t, f.agent)
	var h core.Host
	if _, err := f.api.Apply(ctx, "", "core.host.revoke/v1", core.HostIDInput{HostID: f.agentID}, &h); err != nil {
		t.Fatal(err)
	}
	if h.State != core.HostRevoked {
		t.Errorf("core.host.revoke answered state %q, want %q", h.State, core.HostRevoked)
	}
	waitHosts(ctx, t, f, 30*time.Second, func(hosts []core.Host) error {
		if h, ok := findHost(hosts, f.agentID); !ok || h.Online || h.State != core.HostRevoked {
			return fmt.Errorf("host %s: %+v", f.agentID, h)
		}
		return nil
	})
	// The first failed session is the one revoke closed; every later one is a
	// refused redial.
	err := eventually(ctx, 2*time.Minute, func(ctx context.Context) error {
		out, err := f.agent.Run(ctx, fmt.Sprintf("sudo journalctl -u %s --since @%d --no-pager -o cat", update.AgentUnit, since))
		if err != nil {
			return err
		}
		if n := strings.Count(out, "daemon: controller session"); n < 2 {
			return fmt.Errorf("%d failed sessions in the agent's journal since the revoke, want 2 or more:\n%s", n, out)
		}
		return nil
	})
	if err != nil {
		t.Error(err)
	}
	hosts, err := f.api.Hosts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if h, _ := findHost(hosts, f.agentID); h.Online {
		t.Errorf("the revoked host %s came back online", f.agentID)
	}

	var grant core.EnrollmentGrant
	if _, err := f.api.Apply(ctx, "", "core.host.token/v1", core.HostTokenInput{HostID: f.agentID, ControllerAddress: agentAddress}, &grant); err != nil {
		t.Fatal(err)
	}
	writeToken(ctx, t, f.agent, grant.Token)
	out, err := f.agent.Run(ctx, "sudo darkpanel agent enroll --token-file "+enrollFile)
	removeToken(ctx, t, f.agent)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "enrolled as host "+f.agentID) {
		t.Errorf("agent enroll printed %q, want it enrolled as host %s", out, f.agentID)
	}
	waitHosts(ctx, t, f, 3*time.Minute, hostsAt(versionN, f.agentID))
}

// vmNow is v's clock in Unix seconds, for journalctl --since.
func vmNow(ctx context.Context, t *testing.T, v *vm.VM) int64 {
	t.Helper()
	out, err := v.Run(ctx, "date +%s")
	if err != nil {
		t.Fatal(err)
	}
	n, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// waitHosts polls the host list until check accepts it, for up to timeout,
// and returns the list it accepted. An API error, such as that of a
// controller restarting, counts as not yet.
func waitHosts(ctx context.Context, t *testing.T, f *fleet, timeout time.Duration, check func([]core.Host) error) []core.Host {
	t.Helper()
	var hosts []core.Host
	err := eventually(ctx, timeout, func(ctx context.Context) error {
		var err error
		if hosts, err = f.api.Hosts(ctx); err != nil {
			return err
		}
		return check(hosts)
	})
	if err != nil {
		t.Fatalf("host list: %v", err)
	}
	return hosts
}

// hostsAt accepts a host list in which every host of ids is enrolled,
// online, and runs version.
func hostsAt(version string, ids ...string) func([]core.Host) error {
	return func(hosts []core.Host) error {
		for _, id := range ids {
			h, ok := findHost(hosts, id)
			switch {
			case !ok:
				return fmt.Errorf("host %s is not listed", id)
			case h.State != core.HostEnrolled || !h.Online:
				return fmt.Errorf("host %s (%s) is %s and online %v, want enrolled and online", h.Name, id, h.State, h.Online)
			case h.AgentVersion == nil || *h.AgentVersion != version:
				got := "no version"
				if h.AgentVersion != nil {
					got = *h.AgentVersion
				}
				return fmt.Errorf("host %s (%s) runs %s, want %s", h.Name, id, got, version)
			}
		}
		return nil
	}
}

// requireBinaryVersion checks the version the binary at path reports.
func requireBinaryVersion(ctx context.Context, t *testing.T, v *vm.VM, path, version string) {
	t.Helper()
	out, err := v.Run(ctx, path+" version --json")
	if err != nil {
		t.Fatal(err)
	}
	var ver struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal([]byte(out), &ver); err != nil {
		t.Fatalf("%s version --json: %v\n%s", path, err, out)
	}
	if ver.Version != version {
		t.Errorf("%s reports version %q, want %q", path, ver.Version, version)
	}
}

func TestTOTPSecret(t *testing.T) {
	out := "Change 01HZY: completed\nScan this QR code with an authenticator app, or enter the secret by hand:\n" +
		"█▀▀▀▀▀█\nURI: otpauth://totp/Darkpanel:admin?secret=JBSWY3DPEHPK3PXP&issuer=Darkpanel\n" +
		"Secret: JBSWY3DPEHPK3PXP\nThe secret will not be shown again.\n"
	if got, err := totpSecret(out); err != nil || got != "JBSWY3DPEHPK3PXP" {
		t.Errorf("totpSecret = %q, %v", got, err)
	}
	for _, bad := range []string{"", "Change 01HZY: completed\n", "Secret: \n"} {
		if got, err := totpSecret(bad); err == nil {
			t.Errorf("totpSecret(%q) = %q, want an error", bad, got)
		}
	}
}

func TestNextStep(t *testing.T) {
	cases := []struct{ at, want int64 }{{0, 30}, {29, 30}, {30, 60}, {1_700_000_015, 1_700_000_040}}
	for _, c := range cases {
		if got := nextStep(time.Unix(c.at, 500)); !got.Equal(time.Unix(c.want, 0)) {
			t.Errorf("nextStep(%d) = %d, want %d", c.at, got.Unix(), c.want)
		}
	}
	// A code of the next step differs from the last one's, which the
	// controller would refuse as a replay.
	at := time.Unix(1_700_000_015, 0)
	last, _ := auth.TOTPCode("JBSWY3DPEHPK3PXP", at)
	next, _ := auth.TOTPCode("JBSWY3DPEHPK3PXP", nextStep(at))
	if last == next {
		t.Errorf("the codes of two steps are both %s", last)
	}
}

func TestHostsAt(t *testing.T) {
	v := func(s string) *string { return &s }
	hosts := []core.Host{
		{ID: "h0", Name: "controller", IsLocal: true, State: core.HostEnrolled, Online: true, AgentVersion: v("0.2.0")},
		{ID: "h1", Name: "agent", State: core.HostEnrolled, Online: true, AgentVersion: v("0.3.0")},
		{ID: "h2", Name: "old", State: core.HostRevoked, Online: false, AgentVersion: v("0.2.0")},
		{ID: "h3", Name: "new", State: core.HostPending},
		{ID: "h5", Name: "stopped", State: core.HostEnrolled, Online: false, AgentVersion: v("0.2.0")},
	}
	cases := []struct {
		version string
		ids     []string
		want    string // "" accepts
	}{
		{"0.2.0", []string{"h0"}, ""},
		{"0.3.0", []string{"h1"}, ""},
		{"0.2.0", []string{"h0", "h1"}, "host agent (h1) runs 0.3.0, want 0.2.0"},
		{"0.2.0", []string{"h2"}, "host old (h2) is revoked and online false, want enrolled and online"},
		{"0.2.0", []string{"h3"}, "host new (h3) is pending and online false, want enrolled and online"},
		{"0.2.0", []string{"h5"}, "host stopped (h5) is enrolled and online false, want enrolled and online"},
		{"0.2.0", []string{"h9"}, "host h9 is not listed"},
	}
	for _, c := range cases {
		err := hostsAt(c.version, c.ids...)(hosts)
		if got := fmt.Sprint(err); (c.want == "" && err != nil) || (c.want != "" && got != c.want) {
			t.Errorf("hostsAt(%s, %v) = %v, want %q", c.version, c.ids, err, c.want)
		}
	}
	h := core.Host{ID: "h4", Name: "fresh", State: core.HostEnrolled, Online: true}
	if err := hostsAt("0.2.0", "h4")([]core.Host{h}); fmt.Sprint(err) != "host fresh (h4) runs no version, want 0.2.0" {
		t.Errorf("hostsAt of a host without a version = %v", err)
	}
}
