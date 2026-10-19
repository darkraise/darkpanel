//go:build harness

package harness

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/test/harness/vm"
)

var (
	osFlag      = flag.String("os", "", "OS id from images.json to test; empty skips the VM tests")
	binFlag     = flag.String("bin", "../../bin/darkpanel-linux-amd64", "darkpanel linux/amd64 binary to install")
	commitFlag  = flag.String("commit", "", "full commit SHA the binary was built from; required with -os")
	workdirFlag = flag.String("workdir", "", "directory for overlays, seeds and logs; empty uses a test temporary directory")
	cacheFlag   = flag.String("cache", "", "image cache directory; empty uses darkpanel-harness in the user cache directory")
)

// The "internet" link (ADR 0008): addresses outside every private range, so
// ufw-docker's private-range exemptions cannot hide an exposure.
const (
	targetIPv4  = "198.18.0.10"
	targetIPv6  = "2001:db8:d0::10"
	scannerIPv4 = "198.18.0.20"
	scannerIPv6 = "2001:db8:d0::20"
)

const (
	installPath = "/usr/local/bin/darkpanel"
	serveUnit   = "darkpanel-harness"
	// serveState is XDG_STATE_HOME of the serve unit; XDG_RUNTIME_DIR=/run
	// keeps the controller socket path short.
	serveState = "/var/lib/darkpanel-harness"
	// healthz asks the loopback listener inside the VM; --max-time ends a
	// request that connects and then hangs.
	healthz = "curl -fsS --max-time 10 http://127.0.0.1:8080/healthz"
	// testDeadline stays under the workflow's 45-minute go test timeout, so
	// the cleanups that stop the VMs still run.
	testDeadline = 40 * time.Minute
)

// nmapArgs is the scan of spec section 18, in grepable output.
const nmapArgs = "-Pn -n -sS -p- --min-rate 2000 --max-retries 2 --max-rtt-timeout 500ms -oG -"

// services are the units every target keeps active, also after a reboot.
var services = []string{"ufw", "docker", "tailscaled"}

// pollInterval is how often eventually retries; TestEventually shortens it.
var pollInterval = 2 * time.Second

// pair is one OS's target and the scanner beside it.
type pair struct {
	os              OS
	target, scanner *vm.VM
}

func TestM1(t *testing.T) {
	if *osFlag == "" {
		t.Skip("no -os given: the VM tests run in .github/workflows/harness.yml")
	}
	if *commitFlag == "" {
		t.Fatal("-commit is required with -os")
	}
	ctx, cancel := context.WithTimeout(context.Background(), testDeadline)
	defer cancel()
	p := bootPair(ctx, t)
	steps := []struct {
		name string
		gate bool // later steps need this one
		run  func(t *testing.T)
	}{
		{"os-release", false, func(t *testing.T) { checkOSRelease(ctx, t, p) }},
		{"services", false, func(t *testing.T) { checkServices(ctx, t, p.target) }},
		{"install", true, func(t *testing.T) { checkInstall(ctx, t, p.target) }},
		{"serve", true, func(t *testing.T) { checkServe(ctx, t, p.target) }},
		{"scan-ipv4", false, func(t *testing.T) { checkScan(ctx, t, p.scanner, "", targetIPv4) }},
		{"scan-ipv6", false, func(t *testing.T) { checkScan(ctx, t, p.scanner, "-6", targetIPv6) }},
		{"kill-process", false, func(t *testing.T) { checkKillProcess(ctx, t, p.target) }},
		{"reboot", false, func(t *testing.T) { checkReboot(ctx, t, p.target) }},
	}
	for _, s := range steps {
		if !t.Run(s.name, s.run) && s.gate {
			return
		}
	}
}

// bootPair boots the target of -os and the scanner on one multicast link.
func bootPair(ctx context.Context, t *testing.T) pair {
	t.Helper()
	pins, err := LoadPins(PinsFile)
	if err != nil {
		t.Fatal(err)
	}
	o, err := pins.Find(*osFlag)
	if err != nil {
		t.Fatal(err)
	}
	so, err := pins.Find(pins.Scanner)
	if err != nil {
		t.Fatal(err)
	}
	cache := *cacheFlag
	if cache == "" {
		d, err := os.UserCacheDir()
		if err != nil {
			t.Fatal(err)
		}
		cache = filepath.Join(d, "darkpanel-harness")
	}
	dir := *workdirFlag
	if dir == "" {
		dir = t.TempDir()
	}
	targetImg, err := vm.Fetch(ctx, o.Image.URL, o.Image.SHA256, cache)
	if err != nil {
		t.Fatal(err)
	}
	scannerImg, err := vm.Fetch(ctx, so.Image.URL, so.Image.SHA256, cache)
	if err != nil {
		t.Fatal(err)
	}
	key, err := vm.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	// Each matrix job has a runner of its own; the random port keeps two runs
	// on one machine off each other's link.
	port := 20000 + rand.IntN(20000) //nolint:gosec // G404: a link port, not a secret
	t.Logf("%s: run directory %s, multicast link %s:%d", o.ID, dir, vm.McastGroup, port)
	cfgs := []vm.Config{
		{
			Name: "target", Dir: dir, Image: targetImg, MemoryMiB: 2048, CPUs: 2,
			MgmtMAC: "52:54:00:d0:01:10", InetMAC: "52:54:00:d0:00:10",
			InetAddrs: []string{targetIPv4 + "/15", targetIPv6 + "/64"},
			McastPort: port, Provision: TargetScript(o), Key: key,
		},
		{
			Name: "scanner", Dir: dir, Image: scannerImg, MemoryMiB: 1024, CPUs: 1,
			MgmtMAC: "52:54:00:d0:01:20", InetMAC: "52:54:00:d0:00:20",
			InetAddrs: []string{scannerIPv4 + "/15", scannerIPv6 + "/64"},
			McastPort: port, Provision: ScannerScript(), Key: key,
		},
	}
	vms := make([]*vm.VM, len(cfgs))
	errs := make([]error, len(cfgs))
	var wg sync.WaitGroup
	for i, c := range cfgs {
		wg.Go(func() { vms[i], errs[i] = vm.Boot(ctx, c) })
	}
	wg.Wait()
	for _, v := range vms {
		if v != nil {
			t.Cleanup(v.Shutdown)
		}
	}
	if err := errors.Join(errs...); err != nil {
		t.Fatalf("boot: %v\nlogs in %s", err, dir)
	}
	return pair{os: o, target: vms[0], scanner: vms[1]}
}

func checkOSRelease(ctx context.Context, t *testing.T, p pair) {
	out, err := p.target.Run(ctx, `. /etc/os-release && echo "$ID $VERSION_ID"`)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSpace(out), p.os.Family+" "+p.os.VersionID; got != want {
		t.Errorf("/etc/os-release says %q, want %q", got, want)
	}
}

// checkServices waits until UFW, Docker and tailscaled are each active, and
// checks that UFW allows 22/tcp. Each unit is asked on its own: systemctl
// is-active with several units exits 0 when any one of them is active.
func checkServices(ctx context.Context, t *testing.T, v *vm.VM) {
	err := eventually(ctx, 2*time.Minute, func(ctx context.Context) error {
		for _, u := range services {
			if state := unitState(ctx, v, u); state != "active" {
				return fmt.Errorf("%s is %q", u, state)
			}
		}
		return nil
	})
	if err != nil {
		t.Errorf("services not active: %v", err)
	}
	out, err := v.Run(ctx, "sudo ufw status")
	if err != nil {
		t.Fatal(err)
	}
	allows22 := false
	for _, line := range strings.Split(out, "\n") {
		if f := strings.Fields(line); len(f) >= 2 && f[0] == "22/tcp" && slices.Contains(f, "ALLOW") {
			allows22 = true
		}
	}
	if !strings.Contains(out, "Status: active") || !allows22 {
		t.Errorf("ufw status shows no active firewall allowing 22/tcp:\n%s", out)
	}
}

func checkInstall(ctx context.Context, t *testing.T, v *vm.VM) {
	if err := v.CopyFile(ctx, *binFlag, installPath, 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := v.Run(ctx, installPath+" version --json")
	if err != nil {
		t.Fatal(err)
	}
	var ver struct {
		Commit string `json:"commit"`
		UI     bool   `json:"ui"`
	}
	if err := json.Unmarshal([]byte(out), &ver); err != nil {
		t.Fatalf("version --json: %v\n%s", err, out)
	}
	if len(ver.Commit) < 7 || !strings.HasPrefix(*commitFlag, ver.Commit) || !ver.UI {
		t.Errorf("version --json = %s; want a commit abbreviating %s and ui true", strings.TrimSpace(out), *commitFlag)
	}
}

// checkServe starts serve --dev as a transient systemd unit and waits for
// /healthz inside the VM.
func checkServe(ctx context.Context, t *testing.T, v *vm.VM) {
	cmd := fmt.Sprintf("sudo systemd-run --unit=%s --setenv=XDG_STATE_HOME=%s --setenv=XDG_RUNTIME_DIR=/run %s serve --dev",
		serveUnit, serveState, installPath)
	if _, err := v.Run(ctx, cmd); err != nil {
		t.Fatal(err)
	}
	err := eventually(ctx, time.Minute, func(ctx context.Context) error {
		out, err := v.Run(ctx, healthz)
		if err != nil {
			return err
		}
		var h struct {
			Status string `json:"status"`
		}
		if err := json.Unmarshal([]byte(out), &h); err != nil || h.Status != "ok" {
			return fmt.Errorf("healthz answered %q", out)
		}
		return nil
	})
	if err != nil {
		journal, _ := v.Run(ctx, "sudo journalctl -u "+serveUnit+" --no-pager -n 50")
		t.Fatalf("%v\n%s journal:\n%s", err, serveUnit, journal)
	}
}

// checkScan scans every TCP port of addr from the scanner while the panel
// runs, and wants only 22/tcp open: the panel is not reachable from the
// internet by default.
func checkScan(ctx context.Context, t *testing.T, scanner *vm.VM, family, addr string) {
	out, err := scanner.Run(ctx, fmt.Sprintf("sudo nmap %s %s %s", family, nmapArgs, addr))
	if err != nil {
		t.Fatal(err)
	}
	open, err := OpenPorts(out, addr)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !slices.Equal(open, []string{"22/tcp"}) {
		t.Errorf("open ports on %s = %v, want only 22/tcp\n%s", addr, open, out)
	}
}

// checkKillProcess kills the running panel and checks that it is gone.
func checkKillProcess(ctx context.Context, t *testing.T, v *vm.VM) {
	if state := unitState(ctx, v, serveUnit); state != "active" {
		t.Fatalf("%s is %q before the kill; the panel must run through the scans", serveUnit, state)
	}
	if err := v.KillProcess(ctx, "darkpanel"); err != nil {
		t.Fatal(err)
	}
	err := eventually(ctx, 30*time.Second, func(ctx context.Context) error {
		_, err := v.Run(ctx, "! systemctl is-active --quiet "+serveUnit+" && ! "+healthz)
		return err
	})
	if err != nil {
		t.Fatalf("%s not shown stopped after the kill: %v", serveUnit, err)
	}
}

// checkReboot reboots the target, checks for itself that a new boot began,
// and waits for the services to come back.
func checkReboot(ctx context.Context, t *testing.T, v *vm.VM) {
	before := bootID(ctx, t, v)
	if err := v.Reboot(ctx); err != nil {
		t.Fatal(err)
	}
	if after := bootID(ctx, t, v); after == before {
		t.Fatalf("boot_id %s did not change across Reboot", before)
	}
	checkServices(ctx, t, v)
}

func bootID(ctx context.Context, t *testing.T, v *vm.VM) string {
	t.Helper()
	out, err := v.Run(ctx, "cat /proc/sys/kernel/random/boot_id")
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(out)
}

func unitState(ctx context.Context, v *vm.VM, unit string) string {
	out, _ := v.Run(ctx, "systemctl is-active "+unit+" || true")
	return strings.TrimSpace(out)
}

// eventually calls check every pollInterval until it returns nil or timeout
// passes. Each call gets a context that ends with the timeout, so a check
// that blocks cannot outlast it.
func eventually(ctx context.Context, timeout time.Duration, check func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		err := check(ctx)
		if err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%w; last error: %w", ctx.Err(), err)
		case <-time.After(pollInterval):
		}
	}
}

func TestEventually(t *testing.T) {
	old := pollInterval
	pollInterval = 10 * time.Millisecond
	t.Cleanup(func() { pollInterval = old })
	ctx := context.Background()

	calls := 0
	err := eventually(ctx, time.Second, func(context.Context) error {
		if calls++; calls < 3 {
			return errors.New("not yet")
		}
		return nil
	})
	if err != nil || calls != 3 {
		t.Errorf("eventually = %v after %d calls, want success on the third", err, calls)
	}

	start := time.Now()
	err = eventually(ctx, 100*time.Millisecond, func(ctx context.Context) error {
		<-ctx.Done() // a check that blocks, as a hung request would
		return ctx.Err()
	})
	if elapsed := time.Since(start); !errors.Is(err, context.DeadlineExceeded) || elapsed > time.Second {
		t.Errorf("eventually over a blocked check = %v after %v; want the deadline at 100ms", err, elapsed)
	}

	err = eventually(ctx, 50*time.Millisecond, func(context.Context) error { return errors.New("still down") })
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "still down") {
		t.Errorf("eventually over a failing check = %v; want the deadline and the last error", err)
	}
}
