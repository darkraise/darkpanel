//go:build harness

package harness

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/modules/derp/artifact"
	"github.com/darkraise/darkpanel/internal/modules/derp/server"
	"github.com/darkraise/darkpanel/test/harness/vm"
)

var (
	m5dFlag       = flag.Bool("m5d", false, "run TestM5DERP; needs -os and -m2, and is a go test run of its own")
	m5dDerperFlag = flag.String("derper", filepath.Join("..", "..", "bin", "harness", artifact.BinaryName(artifact.DerperVersion, "amd64")),
		"linux/amd64 derper binary to install; go run ./release/releasetool derper 0.0.0 bin/harness -arch amd64 builds it")
)

// m5dDeadline bounds TestM5DERP. The workflow step that runs it must keep its
// -timeout at least m5dDeadline plus 10 minutes, for the cleanups that stop the VMs.
const m5dDeadline = 20 * time.Minute

// m5dRun is what the steps of TestM5DERP share.
type m5dRun struct {
	ctx context.Context
	f   *fleet
	cfg server.Config
}

// TestM5DERP is the DERP half of M5 that needs no tailnet (spec 4.3, Verification): derper, as the module
// installs it, runs as darkpanel-derp, listens on its three ports, answers /derp/probe with the certificate
// it keeps in its service directory, and answers STUN. The workflow selects it with -run, -os, -m2 and -m5d.
func TestM5DERP(t *testing.T) {
	if *osFlag == "" || *m2Flag == "" || !*m5dFlag {
		t.Skip("no -os, -m2 or -m5d given: the VM tests run in .github/workflows/harness.yml")
	}
	if err := m5dCheckBinary(*m5dDerperFlag); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), m5dDeadline)
	t.Cleanup(cancel)
	r := &m5dRun{ctx: ctx, f: m5dLab(ctx, t), cfg: m5dConfig(agentIPv4)}
	steps := []struct {
		name string
		gate bool // later steps need this one
		run  func(t *testing.T)
	}{
		{"install", true, r.install},
		{"unit-and-process", true, r.unitAndProcess},
		{"listeners", false, r.listeners},
		{"probe-and-pin", false, r.probeAndPin},
		{"stun", false, r.stun},
	}
	for _, s := range steps {
		if !t.Run(s.name, s.run) && s.gate {
			return
		}
	}
}

// m5dLab boots the controller, the agent and the scanner on a lab of their own, installs the harness build and
// enrolls the agent, as m5Lab does, and returns the fleet: the steps also need the scanner.
func m5dLab(ctx context.Context, t *testing.T) *fleet {
	t.Helper()
	lab := NewLab(t)
	o, targetImg, scannerImg := harnessImages(ctx, t)
	key, err := vm.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	vms := lab.Boot(ctx, t, m5Configs(lab, o, targetImg, scannerImg, key))
	f := &fleet{os: o, controller: vms[0], agent: vms[1], scanner: vms[2]}
	serveReleases(ctx, t, f)
	scenarioInstall(ctx, t, f)
	return f
}

// install does on the agent what derp.server.install does for the unit text, the user, the directory and the
// unit, with the binary of -derper, and opens the three ports in UFW, which the module does through
// firewall.rules.apply.
func (r *m5dRun) install(t *testing.T) {
	v := r.f.agent
	for _, cmd := range m5dPrepareCommands() {
		m3Run(r.ctx, t, v, cmd)
	}
	if err := v.CopyFile(r.ctx, *m5dDerperFlag, server.BinaryPath, 0o755); err != nil {
		t.Fatal(err)
	}
	unit := filepath.Join(t.TempDir(), server.UnitName)
	if err := os.WriteFile(unit, server.UnitFile(r.cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := v.CopyFile(r.ctx, unit, server.UnitPath, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range append(m5dStartCommands(), m5dFirewallCommands()...) {
		m3Run(r.ctx, t, v, cmd)
	}
}

// unitAndProcess: the unit is active, derper's main process belongs to darkpanel-derp, and the key file it wrote
// is that user's, mode 0600.
func (r *m5dRun) unitAndProcess(t *testing.T) {
	v := r.f.agent
	err := eventually(r.ctx, 2*time.Minute, func(ctx context.Context) error {
		if state := unitState(ctx, v, server.UnitName); state != "active" {
			return fmt.Errorf("%s is %q", server.UnitName, state)
		}
		// Type=simple turns the unit active at exec, before derper has written its key file.
		if _, err := v.Run(ctx, "sudo test -e "+server.KeyPath); err != nil {
			return fmt.Errorf("%s not written yet: %w", server.KeyPath, err)
		}
		return nil
	})
	if err != nil {
		journal, _ := v.Run(r.ctx, "sudo journalctl -u "+server.UnitName+" --no-pager -n 40")
		t.Fatalf("%v\njournal:\n%s", err, journal)
	}
	owner := strings.TrimSpace(m3Run(r.ctx, t, v, `stat -c %U "/proc/$(systemctl show -p MainPID --value `+server.UnitName+`)"`))
	if owner != server.ServiceUser {
		t.Errorf("derper runs as %q, want %q", owner, server.ServiceUser)
	}
	key := strings.TrimSpace(m3Run(r.ctx, t, v, "sudo stat -c '%U %a' "+server.KeyPath))
	if want := server.ServiceUser + " 600"; key != want {
		t.Errorf("%s is %q (owner and mode), want %q", server.KeyPath, key, want)
	}
}

// listeners: derper holds 80/tcp, 443/tcp and 3478/udp, and nothing else holds them.
func (r *m5dRun) listeners(t *testing.T) {
	err := eventually(r.ctx, time.Minute, func(ctx context.Context) error {
		out, err := r.f.agent.Run(ctx, "sudo ss -H -ltunp")
		if err != nil {
			return err
		}
		ls, err := m5dParseListeners(out)
		if err != nil {
			return err
		}
		return m5dCheckListeners(ls, "derper")
	})
	if err != nil {
		t.Fatalf("listeners: %v", err)
	}
}

// probeAndPin: GET /derp/probe from the scanner answers 200, and the leaf it was served has the pin of the
// certificate file derper keeps in its certificate directory.
func (r *m5dRun) probeAndPin(t *testing.T) {
	var leaf []byte
	err := eventually(r.ctx, time.Minute, func(ctx context.Context) error {
		status, der, err := m5dProbe(ctx, r.f.scanner, r.cfg.HostName)
		if err != nil {
			return err
		}
		if status != http.StatusOK {
			return fmt.Errorf("GET /derp/probe answered %d, want 200", status)
		}
		leaf = der
		return nil
	})
	if err != nil {
		t.Fatalf("GET https://%s/derp/probe from the scanner: %v", r.cfg.HostName, err)
	}
	crt, _, ok := server.CertFiles(r.cfg)
	if !ok {
		t.Fatal("harness: ip mode names no certificate file")
	}
	file := []byte(m3Run(r.ctx, t, r.f.agent, "sudo cat "+server.CertDir+"/"+crt))
	if err := m5dCheckPin(leaf, file, time.Now()); err != nil {
		t.Error(err)
	}
}

// stun: derper answers a STUN binding request from the scanner with the scanner's own address.
func (r *m5dRun) stun(t *testing.T) {
	target := netip.AddrPortFrom(netip.MustParseAddr(r.cfg.HostName), server.STUNPort)
	err := eventually(r.ctx, 30*time.Second, func(ctx context.Context) error { return m5dStun(ctx, r.f.scanner, target) })
	if err != nil {
		r.logStunDiagnostics(t)
		t.Errorf("STUN binding to %s from the scanner: %v", target, err)
	}
}

// m5dDiagnosticTimeout bounds each command of logStunDiagnostics.
const m5dDiagnosticTimeout = 15 * time.Second

// logStunDiagnostics logs what m5dStunDiagnostics collects. A command that fails is logged with its error and
// never fails the test.
func (r *m5dRun) logStunDiagnostics(t *testing.T) {
	for _, d := range m5dStunDiagnostics() {
		v := r.f.agent
		if d.Role == "scanner" {
			v = r.f.scanner
		}
		ctx, cancel := context.WithTimeout(r.ctx, m5dDiagnosticTimeout)
		out, err := v.Run(ctx, d.Command)
		cancel()
		out = m5dCapDiagnostic(out, strings.Contains(d.Command, "journalctl"))
		if err != nil {
			t.Logf("diagnostic %s: %s\n$ %s\n%s\nerror: %v", d.Role, d.Label, d.Command, out, err)
			continue
		}
		t.Logf("diagnostic %s: %s\n$ %s\n%s", d.Role, d.Label, d.Command, out)
	}
}
