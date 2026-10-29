//go:build harness

package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/test/harness/vm"
)

// m3dRow is one listening port of the exposure view, reduced to the verdict
// of its internet class.
type m3dRow struct {
	Proto, Addr, Family string
	Port                uint16
	Verdict             string   // the internet class's verdict
	Allowed             []string // its allowed sources when partial
}

// m3dxShellSafe reports whether s can stand in a POSIX shell command line
// without quoting.
func m3dxShellSafe(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune("_@%+=:,./-", r):
		default:
			return false
		}
	}
	return true
}

// m3dxShell joins args into one shell command line, quoting every argument
// that is empty or holds a character outside the shell-safe set.
func m3dxShell(args ...string) string {
	words := make([]string, len(args))
	for i, a := range args {
		if m3dxShellSafe(a) {
			words[i] = a
		} else {
			words[i] = m3Quote(a)
		}
	}
	return strings.Join(words, " ")
}

// m3dxNmapCommand is the command line that runs the scan o on a VM.
func m3dxNmapCommand(o ScanOpts) string {
	return "sudo nmap " + m3dxShell(NmapArgs(o)...)
}

// m3dxAddrCmd is the command line that adds p to iface, or removes it. An
// IPv6 address is added without duplicate address detection so that it is
// usable at once.
func m3dxAddrCmd(p netip.Prefix, iface string, add bool) string {
	if !add {
		return m3dxShell("sudo", "ip", "addr", "del", p.String(), "dev", iface)
	}
	args := []string{"sudo", "ip", "addr", "add", p.String(), "dev", iface}
	if p.Addr().Is6() {
		args = append(args, "nodad")
	}
	return m3dxShell(args...)
}

// m3dxRunCmd is the command line that starts image detached as the container
// name, on network when it is not empty, with one -p flag per publish entry.
func m3dxRunCmd(image, name, network string, publish []string) string {
	args := []string{"sudo", "docker", "run", "-d", "--name", name}
	if network != "" {
		args = append(args, "--network", network)
	}
	for _, p := range publish {
		args = append(args, "-p", p)
	}
	return m3dxShell(append(args, image)...)
}

// m3dxRunning accepts the status docker inspect reports for a running
// container and refuses every other one.
func m3dxRunning(status string) error {
	if status != "running" {
		return fmt.Errorf("harness: container status is %q, want %q", status, "running")
	}
	return nil
}

// m3dxImage is the digest-pinned reference of the container image the pin
// file lists under name.
func m3dxImage(name string) (string, error) {
	pins, err := LoadPins(PinsFile)
	if err != nil {
		return "", err
	}
	c, err := pins.Container(name)
	if err != nil {
		return "", err
	}
	return c.Ref, nil
}

// m3dxRows decodes the JSON of the firewall.exposure resource into one row
// per listening port, carrying the verdict of its internet class.
func m3dxRows(raw []byte) ([]m3dRow, error) {
	var view struct {
		Rows []struct {
			Proto   string `json:"proto"`
			Addr    string `json:"addr"`
			Port    uint16 `json:"port"`
			Family  string `json:"family"`
			Classes []struct {
				Class   string `json:"class"`
				Verdict string `json:"verdict"`
				Allowed []struct {
					Sources []string `json:"sources"`
				} `json:"allowed"`
			} `json:"classes"`
		} `json:"rows"`
	}
	if err := json.Unmarshal(raw, &view); err != nil {
		return nil, fmt.Errorf("harness: exposure view: %w", err)
	}
	if len(view.Rows) == 0 {
		return nil, errors.New("harness: exposure view has no rows")
	}
	rows := make([]m3dRow, 0, len(view.Rows))
	for _, r := range view.Rows {
		if r.Proto != "tcp" && r.Proto != "udp" {
			return nil, fmt.Errorf("harness: exposure row %s:%d: protocol %q is neither tcp nor udp", r.Addr, r.Port, r.Proto)
		}
		if r.Port == 0 {
			return nil, fmt.Errorf("harness: exposure row %s %s: port is 0", r.Proto, r.Addr)
		}
		row := m3dRow{Proto: r.Proto, Addr: r.Addr, Family: r.Family, Port: r.Port}
		found := false
		for _, c := range r.Classes {
			if c.Class != "internet" {
				continue
			}
			found = true
			switch c.Verdict {
			case "open", "closed", "filtered", "partial", "unknown":
			default:
				return nil, fmt.Errorf("harness: exposure row %s %s:%d: verdict %q is not a known verdict", r.Proto, r.Addr, r.Port, c.Verdict)
			}
			row.Verdict = c.Verdict
			if c.Verdict == "partial" {
				for _, a := range c.Allowed {
					for _, src := range a.Sources {
						if !slices.Contains(row.Allowed, src) {
							row.Allowed = append(row.Allowed, src)
						}
					}
				}
			}
			break
		}
		if !found {
			return nil, fmt.Errorf("harness: exposure row %s %s:%d has no internet class", r.Proto, r.Addr, r.Port)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// m3dxMismatch compares the rows of protocol proto with a scan and returns
// one message per disagreement, in row order. A row whose verdict is partial
// or unknown is skipped: a partial verdict holds for some sources and not
// for others, and an unknown one makes no claim, so neither has a single scan
// result to match. A caller that probes a partial row from an allowed source
// and from an outside one passes copies of the row with the verdict set to
// open and to the outside verdict. A UDP scan hears only from a service that
// replies, so an open verdict is contradicted only by closed, and a closed
// or filtered verdict only by open; open|filtered and closed carry no
// response from the service.
func m3dxMismatch(rows []m3dRow, scanned []PortState, proto string) []string {
	var out []string
	for _, r := range rows {
		if r.Proto != proto || r.Verdict == "partial" || r.Verdict == "unknown" {
			continue
		}
		who := fmt.Sprintf("%s %s (%s): verdict %s", r.Proto, net.JoinHostPort(r.Addr, strconv.Itoa(int(r.Port))), r.Family, r.Verdict)
		i := slices.IndexFunc(scanned, func(s PortState) bool { return s.Port == r.Port && s.Proto == r.Proto })
		if i < 0 {
			out = append(out, who+", not in the scan")
			continue
		}
		state := scanned[i].State
		var bad bool
		switch {
		case proto != "udp":
			bad = state != r.Verdict
		case r.Verdict == "open":
			bad = state == "closed"
		default:
			bad = state == "open"
		}
		if bad {
			out = append(out, who+", scanned "+state)
		}
	}
	return out
}

// m3dScan runs the scan o on scanner and returns every port entry it
// reports; a failed scan fails the test.
func m3dScan(ctx context.Context, t *testing.T, scanner *vm.VM, o ScanOpts) []PortState {
	t.Helper()
	ports, err := m3dScanErr(ctx, scanner, o)
	if err != nil {
		t.Fatal(err)
	}
	return ports
}

// m3dScanErr is m3dScan returning the failure, for a check that retries.
func m3dScanErr(ctx context.Context, scanner *vm.VM, o ScanOpts) ([]PortState, error) {
	cmd := m3dxNmapCommand(o)
	out, err := scanner.Run(ctx, cmd)
	if err != nil {
		return nil, fmt.Errorf("%s: %w\n%s", cmd, err, out)
	}
	ports, err := ParsePorts(out)
	if err != nil {
		return nil, fmt.Errorf("%s: %w\n%s", cmd, err, out)
	}
	return ports, nil
}

// m3dAddSource adds p to iface of scanner and removes it when the test ends.
// p is added with its own prefix length, so a caller passes a host prefix
// (/32, /128) or an address inside the link's network.
func m3dAddSource(ctx context.Context, t *testing.T, scanner *vm.VM, p netip.Prefix, iface string) {
	t.Helper()
	m3Run(ctx, t, scanner, m3dxAddrCmd(p, iface, true))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_, _ = scanner.Run(ctx, m3dxAddrCmd(p, iface, false))
	})
}

// m3dDocker runs sudo docker with args on host and returns the output.
func m3dDocker(ctx context.Context, t *testing.T, host *vm.VM, args ...string) string {
	t.Helper()
	return m3Run(ctx, t, host, "sudo docker "+m3dxShell(args...))
}

// m3dWaitRunning waits up to a minute for the container name to run.
func m3dWaitRunning(ctx context.Context, t *testing.T, host *vm.VM, name string) {
	t.Helper()
	err := eventually(ctx, time.Minute, func(ctx context.Context) error {
		out, err := host.Run(ctx, "sudo docker inspect -f "+m3Quote("{{.State.Status}}")+" "+m3dxShell(name))
		if err != nil {
			return err
		}
		return m3dxRunning(strings.TrimSpace(out))
	})
	if err != nil {
		t.Fatalf("container %s: %v", name, err)
	}
}

// m3dRun starts the pinned image as the container name and removes the
// container when the test ends.
func m3dRun(ctx context.Context, t *testing.T, host *vm.VM, image, name, network string, publish []string) {
	t.Helper()
	ref, err := m3dxImage(image)
	if err != nil {
		t.Fatal(err)
	}
	m3Run(ctx, t, host, m3dxRunCmd(ref, name, network, publish))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_, _ = host.Run(ctx, "sudo docker rm -f "+m3dxShell(name))
	})
}

// m3dRunTCP starts the pinned pebble image, which serves TCP 14000.
func m3dRunTCP(ctx context.Context, t *testing.T, host *vm.VM, name, network string, publish ...string) {
	t.Helper()
	m3dRun(ctx, t, host, "pebble", name, network, publish)
}

// m3dRunUDP starts the pinned pebble-challtestsrv image, which serves TCP and
// UDP 8053.
func m3dRunUDP(ctx context.Context, t *testing.T, host *vm.VM, name, network string, publish ...string) {
	t.Helper()
	m3dRun(ctx, t, host, "pebble-challtestsrv", name, network, publish)
}
