//go:build harness

package harness

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/update"
)

const (
	// m3dTrafficIface is the internet link's interface name on both VMs.
	m3dTrafficIface = "dpinet"
	// m3dTrafficSrcB is a second scanner source address inside the lab's
	// 198.18.0.0/15 link; the first is scannerIPv4.
	m3dTrafficSrcB = "198.18.1.30"
)

// m3dTrafficProbeScript connects to every port in a loop and logs one line per
// attempt, "<port> ok" or "<port> fail". It takes the log path, the host and
// the ports from its arguments.
const m3dTrafficProbeScript = `import socket, sys, time
log, host = sys.argv[1], sys.argv[2]
ports = [int(p) for p in sys.argv[3:]]
family = socket.AF_INET6 if ":" in host else socket.AF_INET
with open(log, "a") as out:
    while True:
        for port in ports:
            s = socket.socket(family, socket.SOCK_STREAM)
            s.settimeout(0.4)
            try:
                s.connect((host, port))
                result = "ok"
            except OSError:
                result = "fail"
            finally:
                s.close()
            out.write("%d %s\n" % (port, result))
            out.flush()
        time.sleep(0.1)
`

// m3dxTrafficExpect checks the scan entry of proto and port against want:
// open, closed, filtered, or notopen (closed or filtered).
func m3dxTrafficExpect(states []PortState, proto string, port uint16, want string) error {
	switch want {
	case "open", "closed", "filtered", "notopen":
	default:
		return fmt.Errorf("harness: unknown expectation %q", want)
	}
	i := slices.IndexFunc(states, func(s PortState) bool { return s.Proto == proto && s.Port == port })
	if i < 0 {
		return fmt.Errorf("harness: %s/%d is not in the scan", proto, port)
	}
	got := states[i].State
	if got == want || want == "notopen" && (got == "closed" || got == "filtered") {
		return nil
	}
	return fmt.Errorf("harness: %s/%d is %s, want %s", proto, port, got, want)
}

// m3dxTrafficExpectAll checks every TCP port of want, in ascending port
// order, and joins the mismatches into one error.
func m3dxTrafficExpectAll(states []PortState, want map[uint16]string) error {
	ports := make([]uint16, 0, len(want))
	for p := range want {
		ports = append(ports, p)
	}
	slices.Sort(ports)
	var msgs []string
	for _, p := range ports {
		if err := m3dxTrafficExpect(states, "tcp", p, want[p]); err != nil {
			msgs = append(msgs, err.Error())
		}
	}
	if len(msgs) == 0 {
		return nil
	}
	return errors.New(strings.Join(msgs, "; "))
}

// m3dxTrafficLeaks reads a probe log and requires that no unallowed port
// ever connected and that each was probed at least minAttempts times, so a
// probe that never ran cannot pass as one that saw nothing.
func m3dxTrafficLeaks(log string, unallowed []uint16, minAttempts int) error {
	attempts, oks, err := m3dxTrafficCount(log)
	if err != nil {
		return err
	}
	for _, p := range unallowed {
		if oks[p] > 0 {
			return fmt.Errorf("harness: port %d connected in %d of %d attempts", p, oks[p], attempts[p])
		}
	}
	return m3dxTrafficProbed(log, unallowed, minAttempts)
}

// m3dxTrafficProbed requires a probe log to hold at least minAttempts
// attempts for every port of ports, whatever their results.
func m3dxTrafficProbed(log string, ports []uint16, minAttempts int) error {
	attempts, _, err := m3dxTrafficCount(log)
	if err != nil {
		return err
	}
	for _, p := range ports {
		if attempts[p] < minAttempts {
			return fmt.Errorf("harness: port %d had %d attempts, want at least %d", p, attempts[p], minAttempts)
		}
	}
	return nil
}

// m3dxTrafficCount counts the attempts, and the connected ones, per port of a
// probe log.
func m3dxTrafficCount(log string) (attempts, oks map[uint16]int, err error) {
	attempts = map[uint16]int{}
	oks = map[uint16]int{}
	for _, line := range strings.Split(log, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		f := strings.Fields(line)
		if len(f) != 2 || f[1] != "ok" && f[1] != "fail" {
			return nil, nil, fmt.Errorf("harness: malformed probe log line %q", line)
		}
		p, err := strconv.ParseUint(f[0], 10, 16)
		if err != nil {
			return nil, nil, fmt.Errorf("harness: malformed probe log line %q", line)
		}
		attempts[uint16(p)]++
		if f[1] == "ok" {
			oks[uint16(p)]++
		}
	}
	return attempts, oks, nil
}

// m3dxTrafficConnect returns the command that opens a TCP connection to
// addr:port from inside the network namespace of process pid. addr is parsed
// and printed again, which keeps shell metacharacters out of the command.
func m3dxTrafficConnect(pid int, addr string, port uint16) (string, error) {
	a, err := netip.ParseAddr(addr)
	switch {
	case pid <= 0:
		return "", fmt.Errorf("harness: pid %d is not a process", pid)
	case port == 0:
		return "", errors.New("harness: port 0 is not a port")
	case err != nil:
		return "", fmt.Errorf("harness: address %q: %w", addr, err)
	case a.Zone() != "":
		return "", fmt.Errorf("harness: address %q has a zone", addr)
	}
	return fmt.Sprintf("sudo nsenter -t %d -n -- timeout 5 bash -c '</dev/tcp/%s/%d'", pid, a, port), nil
}

// m3dxTrafficRuleID returns the id of the rule of st with the selector, host
// port, kind and source given (from is empty for any source).
func m3dxTrafficRuleID(st m3dDockStatus, selector string, port uint16, kind, from string) (string, error) {
	for _, r := range st.Rules {
		if r.Binding.Selector == selector && r.Binding.HostPort == port && r.Kind == kind && r.From == from {
			return r.ID, nil
		}
	}
	return "", fmt.Errorf("harness: no %s rule for %s port %d from %q among %+v", kind, selector, port, from, st.Rules)
}

// m3dTrafficWant scans the agent host from the scanner until every port of
// want shows its expected state, and fails the test with the last mismatch
// after 90 seconds.
func m3dTrafficWant(ctx context.Context, t *testing.T, f *fleet, o ScanOpts, want map[uint16]string) {
	t.Helper()
	o.Ports = o.Ports[:0:0]
	for p := range want {
		o.Ports = append(o.Ports, p)
	}
	slices.Sort(o.Ports)
	if o.Target == "" {
		switch o.Family {
		case "-4":
			o.Target = agentIPv4
		case "-6":
			o.Target = agentIPv6
		}
	}
	if o.Source.IsValid() {
		o.Iface = m3dTrafficIface
	}
	err := eventually(ctx, 90*time.Second, func(ctx context.Context) error {
		scanned, err := m3dScanErr(ctx, f.scanner, o)
		if err != nil {
			return err
		}
		return m3dxTrafficExpectAll(scanned, want)
	})
	if err != nil {
		t.Fatalf("scan %s of %s from %v: %v", o.Family, o.Target, o.Source, err)
	}
}

// m3dTrafficPublish starts the Pebble container name on network, publishing
// publish, and removes it when the test ends.
func m3dTrafficPublish(ctx context.Context, t *testing.T, f *fleet, name, network string, publish ...string) {
	t.Helper()
	t.Cleanup(func() { m3dDockRemove(ctx, f.agent, name) })
	m3dRunTCP(ctx, t, f.agent, name, network, publish...)
	m3dWaitRunning(ctx, t, f.agent, name)
}

// m3dTrafficNetwork creates the Docker network name with opts and removes it
// when the test ends, after the containers started later are gone.
func m3dTrafficNetwork(ctx context.Context, t *testing.T, f *fleet, name string, opts ...string) {
	t.Helper()
	t.Cleanup(func() { _, _ = f.agent.Run(ctx, "sudo docker network rm "+m3dxShell(name)) })
	m3dDocker(ctx, t, f.agent, append(append([]string{"network", "create"}, opts...), name)...)
}

// m3dTrafficAllowed is the input of container.allow and container.deny.
func m3dTrafficAllowed(selector string, port uint16, from string) map[string]any {
	in := map[string]any{"binding": m3dDockBinding{Selector: selector, HostPort: port, Proto: "tcp"}}
	if from != "" {
		in["from"] = from
	}
	return in
}

// m3dTrafficAllow allows the binding of selector and port from from (any
// source when empty).
func m3dTrafficAllow(ctx context.Context, t *testing.T, f *fleet, selector string, port uint16, from string) {
	t.Helper()
	m3dDockApply(ctx, t, f, "firewall.container.allow", m3dTrafficAllowed(selector, port, from))
}

// m3dTrafficDeny denies from on the binding of selector and port.
func m3dTrafficDeny(ctx context.Context, t *testing.T, f *fleet, selector string, port uint16, from string) {
	t.Helper()
	m3dDockApply(ctx, t, f, "firewall.container.deny", m3dTrafficAllowed(selector, port, from))
}

// m3dTrafficRemove removes the stored rule of the binding, kind and source.
func m3dTrafficRemove(ctx context.Context, t *testing.T, f *fleet, selector string, port uint16, kind, from string) {
	t.Helper()
	st, err := f.api.m3dDockRead(ctx, f.agentID)
	if err != nil {
		t.Fatalf("read firewall.docker: %v", err)
	}
	id, err := m3dxTrafficRuleID(st, selector, port, kind, from)
	if err != nil {
		t.Fatal(err)
	}
	m3dDockApply(ctx, t, f, "firewall.container.remove", map[string]any{"id": id})
}

// m3dTrafficAddr returns field (IPAddress or GlobalIPv6Address) of container
// on network.
func m3dTrafficAddr(ctx context.Context, t *testing.T, f *fleet, container, network, field string) string {
	t.Helper()
	return strings.TrimSpace(m3dDocker(ctx, t, f.agent, "inspect", "-f", "{{.NetworkSettings.Networks."+network+"."+field+"}}", container))
}

// m3dTrafficPID returns the process id of container's init process.
func m3dTrafficPID(ctx context.Context, t *testing.T, f *fleet, container string) int {
	t.Helper()
	out := strings.TrimSpace(m3dDocker(ctx, t, f.agent, "inspect", "-f", "{{.State.Pid}}", container))
	pid, err := strconv.Atoi(out)
	if err != nil {
		t.Fatalf("container %s: pid %q: %v", container, out, err)
	}
	return pid
}

// m3dTrafficProbe starts a transient unit on the scanner that connects to
// host and ports in a loop. The stop function it returns stops the unit and
// returns the probe log; it also runs when the test ends, and a second call
// returns the same log.
func m3dTrafficProbe(ctx context.Context, t *testing.T, f *fleet, unit, host string, ports ...uint16) (stop func(minAttempts int) string) {
	t.Helper()
	log := "/var/tmp/" + unit + ".log"
	args := []string{log, host}
	for _, p := range ports {
		args = append(args, strconv.Itoa(int(p)))
	}
	_, _ = f.scanner.Run(ctx, "sudo rm -f "+m3dxShell(log))
	m3Run(ctx, t, f.scanner, "sudo systemd-run "+m3dxShell("--unit="+unit)+" --collect /usr/bin/python3 -c "+m3Quote(m3dTrafficProbeScript)+" "+m3dxShell(args...))
	var out string
	stopped := false
	stop = func(minAttempts int) string {
		if stopped {
			return out
		}
		stopped = true
		// A filtered port costs an attempt about half a second, so a short
		// scenario can end before the probe has made minAttempts attempts.
		if err := eventually(ctx, time.Minute, func(ctx context.Context) error {
			if minAttempts <= 0 {
				return nil
			}
			got, err := f.scanner.Run(ctx, "sudo cat "+m3dxShell(log))
			if err != nil {
				return err
			}
			return m3dxTrafficProbed(got, ports, minAttempts)
		}); err != nil {
			t.Logf("wait for %d attempts in %s: %v", minAttempts, log, err)
		}
		if _, err := f.scanner.Run(ctx, "sudo systemctl stop "+m3dxShell(unit)); err != nil {
			t.Logf("stop %s: %v", unit, err)
		}
		var err error
		if out, err = f.scanner.Run(ctx, "sudo cat "+m3dxShell(log)); err != nil {
			t.Logf("read %s: %v", log, err)
		}
		return out
	}
	t.Cleanup(func() { stop(0) })
	return stop
}

// m3dTrafficView waits for the binding view of selector and port to satisfy
// check.
func m3dTrafficView(ctx context.Context, t *testing.T, f *fleet, selector string, port uint16, check func(m3dDockBindingView) error) {
	t.Helper()
	m3dDockWait(ctx, t, f.api, f.agentID, time.Minute, func(st m3dDockStatus) error {
		for _, b := range st.Bindings {
			if b.Binding.Selector == selector && b.Binding.HostPort == port {
				return check(b)
			}
		}
		return fmt.Errorf("harness: no binding view for %s port %d", selector, port)
	})
}

// m3dTrafficUnallowed requires a published port to stay closed until a
// binding allows it.
func m3dTrafficUnallowed(ctx context.Context, t *testing.T, f *fleet) {
	m3dDockEnable(ctx, t, f)
	v4 := ScanOpts{Family: "-4"}
	m3dTrafficPublish(ctx, t, f, "m3d-t1", "bridge", "18201:14000")
	m3dTrafficWant(ctx, t, f, v4, map[uint16]string{18201: "filtered"})
	m3dTrafficAllow(ctx, t, f, "m3d-t1", 18201, "")
	m3dTrafficWant(ctx, t, f, v4, map[uint16]string{18201: "open"})
	m3dTrafficView(ctx, t, f, "m3d-t1", 18201, func(b m3dDockBindingView) error {
		if !slices.Equal(b.Allowed, []string{"any"}) {
			return fmt.Errorf("harness: m3d-t1 port 18201 allowed %q, want [any]", b.Allowed)
		}
		return nil
	})
	m3dTrafficRemove(ctx, t, f, "m3d-t1", 18201, "allow", "")
	m3dTrafficWant(ctx, t, f, v4, map[uint16]string{18201: "filtered"})
}

// m3dTrafficNewNetwork requires a Docker network created after the
// integration was enabled to be protected before any reconcile sees it.
func m3dTrafficNewNetwork(ctx context.Context, t *testing.T, f *fleet) {
	m3dDockEnable(ctx, t, f)
	m3dTrafficNetwork(ctx, t, f, "m3dn1", "-o", "com.docker.network.bridge.name=m3dcust")
	m3dTrafficPublish(ctx, t, f, "m3d-n1", "m3dn1", "18202:14000")
	states := m3dScan(ctx, t, f.scanner, ScanOpts{Family: "-4", Ports: []uint16{18202}, Target: agentIPv4})
	if err := m3dxTrafficExpectAll(states, map[uint16]string{18202: "filtered"}); err != nil {
		t.Fatalf("new network, before any reconcile: %v", err)
	}
	m3dTrafficAllow(ctx, t, f, "m3d-n1", 18202, "")
	m3dTrafficWant(ctx, t, f, ScanOpts{Family: "-4"}, map[uint16]string{18202: "open"})
}

// m3dTrafficContainers requires container-to-container and outbound traffic
// to keep working with the integration on.
func m3dTrafficContainers(ctx context.Context, t *testing.T, f *fleet) {
	m3dDockEnable(ctx, t, f)
	subnet4 := netip.MustParsePrefix("203.0.113.0/24")
	subnet6 := netip.MustParsePrefix("2001:db8:dd:1::/64")
	m3dTrafficNetwork(ctx, t, f, "m3dpub", "--subnet", subnet4.String(), "--ipv6", "--subnet", subnet6.String())
	names := []string{"m3d-c1", "m3d-c2"}
	addrs := map[string][2]string{}
	for _, n := range names {
		m3dTrafficPublish(ctx, t, f, n, "m3dpub")
	}
	for _, n := range names {
		a4 := m3dTrafficAddr(ctx, t, f, n, "m3dpub", "IPAddress")
		a6 := m3dTrafficAddr(ctx, t, f, n, "m3dpub", "GlobalIPv6Address")
		if p, err := netip.ParseAddr(a4); err != nil || !subnet4.Contains(p) {
			t.Fatalf("container %s IPv4 address %q is not in %s", n, a4, subnet4)
		}
		if p, err := netip.ParseAddr(a6); err != nil || !subnet6.Contains(p) {
			t.Fatalf("container %s IPv6 address %q is not in %s", n, a6, subnet6)
		}
		addrs[n] = [2]string{a4, a6}
	}
	connect := func(from, to string, port uint16) {
		t.Helper()
		cmd, err := m3dxTrafficConnect(m3dTrafficPID(ctx, t, f, from), to, port)
		if err != nil {
			t.Fatal(err)
		}
		if err := eventually(ctx, 30*time.Second, func(ctx context.Context) error {
			out, err := f.agent.Run(ctx, cmd)
			if err != nil {
				return fmt.Errorf("%s: %w\n%s", cmd, err, out)
			}
			return nil
		}); err != nil {
			t.Fatalf("connect from %s to %s port %d: %v", from, to, port, err)
		}
	}
	for _, pair := range [][2]string{{"m3d-c2", "m3d-c1"}, {"m3d-c1", "m3d-c2"}} {
		for _, a := range addrs[pair[1]] {
			connect(pair[0], a, 14000)
		}
	}
	connect("m3d-c2", scannerIPv4, 8000)
}

// m3dTrafficDisjoint requires two bindings to one container port to refuse
// each other's allowed source.
func m3dTrafficDisjoint(ctx context.Context, t *testing.T, f *fleet) {
	m3dDockEnable(ctx, t, f)
	m3dAddSource(ctx, t, f.scanner, netip.MustParsePrefix(m3dTrafficSrcB+"/32"), m3dTrafficIface)
	m3dTrafficPublish(ctx, t, f, "m3d-d1", "bridge", "18203:14000", "18204:14000")
	bind := func(port uint16) m3dDockBinding {
		return m3dDockBinding{Selector: "m3d-d1", HostPort: port, Proto: "tcp"}
	}
	m3dDockApply(ctx, t, f, "firewall.containers.apply", map[string]any{"ops": []map[string]any{
		{"op": "allow", "binding": bind(18203), "from": scannerIPv4 + "/32"},
		{"op": "allow", "binding": bind(18204), "from": m3dTrafficSrcB + "/32"},
	}})
	m3dTrafficWant(ctx, t, f, ScanOpts{Family: "-4"}, map[uint16]string{18203: "open", 18204: "filtered"})
	m3dTrafficWant(ctx, t, f, ScanOpts{Family: "-4", Source: netip.MustParseAddr(m3dTrafficSrcB)}, map[uint16]string{18203: "filtered", 18204: "open"})
}

// m3dTrafficDenyKeeps requires an explicit deny on one binding to leave the
// allowance of another binding of the same container port alone.
func m3dTrafficDenyKeeps(ctx context.Context, t *testing.T, f *fleet) {
	m3dDockEnable(ctx, t, f)
	m3dAddSource(ctx, t, f.scanner, netip.MustParsePrefix(m3dTrafficSrcB+"/32"), m3dTrafficIface)
	m3dTrafficPublish(ctx, t, f, "m3d-k1", "bridge", "18205:14000", "18206:14000")
	m3dTrafficAllow(ctx, t, f, "m3d-k1", 18205, "")
	m3dTrafficAllow(ctx, t, f, "m3d-k1", 18206, "")
	m3dTrafficDeny(ctx, t, f, "m3d-k1", 18206, scannerIPv4+"/32")
	m3dTrafficWant(ctx, t, f, ScanOpts{Family: "-4"}, map[uint16]string{18205: "open", 18206: "filtered"})
	m3dTrafficWant(ctx, t, f, ScanOpts{Family: "-4", Source: netip.MustParseAddr(m3dTrafficSrcB)}, map[uint16]string{18205: "open", 18206: "open"})
}

// m3dTrafficProxied requires an unallowed IPv6 binding that docker-proxy
// serves for an IPv4-only container to be closed.
func m3dTrafficProxied(ctx context.Context, t *testing.T, f *fleet) {
	m3dDockEnable(ctx, t, f)
	m3dTrafficPublish(ctx, t, f, "m3d-p1", "bridge", "18207:14000")
	both := func(state string) {
		t.Helper()
		for _, fam := range []string{"-4", "-6"} {
			m3dTrafficWant(ctx, t, f, ScanOpts{Family: fam}, map[uint16]string{18207: state})
		}
	}
	both("notopen")
	m3dTrafficAllow(ctx, t, f, "m3d-p1", 18207, "")
	both("open")
	m3dTrafficView(ctx, t, f, "m3d-p1", 18207, func(b m3dDockBindingView) error {
		if len(b.Proxied) == 0 {
			return errors.New("harness: m3d-p1 port 18207 has no proxied family")
		}
		return nil
	})
	m3dTrafficRemove(ctx, t, f, "m3d-p1", 18207, "allow", "")
	both("notopen")
}

// m3dTrafficRecreate requires containers recreated under continuous
// scanning, across an agent restart too, never to expose an unallowed binding.
func m3dTrafficRecreate(ctx context.Context, t *testing.T, f *fleet) {
	m3dDockEnable(ctx, t, f)
	v4 := ScanOpts{Family: "-4"}
	m3dTrafficPublish(ctx, t, f, "m3d-r1", "bridge", "18208:14000")
	m3dTrafficPublish(ctx, t, f, "m3d-r2", "bridge", "18209:14000")
	m3dTrafficAllow(ctx, t, f, "m3d-r1", 18208, "")
	m3dTrafficWant(ctx, t, f, v4, map[uint16]string{18208: "open"})
	m3dTrafficWant(ctx, t, f, v4, map[uint16]string{18209: "notopen"})
	stop4 := m3dTrafficProbe(ctx, t, f, "m3d-probe4", agentIPv4, 18209)
	stop6 := m3dTrafficProbe(ctx, t, f, "m3d-probe6", agentIPv6, 18209)
	recreate := func() {
		t.Helper()
		m3dDockRemove(ctx, f.agent, "m3d-r2")
		m3dTrafficPublish(ctx, t, f, "m3d-r2", "bridge", "18209:14000")
		m3dDockRemove(ctx, f.agent, "m3d-r1")
		m3dTrafficPublish(ctx, t, f, "m3d-r1", "bridge", "18208:14000")
	}
	for range 3 {
		recreate()
		m3dTrafficWant(ctx, t, f, v4, map[uint16]string{18208: "open"})
	}
	m3Run(ctx, t, f.agent, "sudo systemctl restart "+update.AgentUnit)
	recreate()
	waitHosts(ctx, t, f, 3*time.Minute, hostsAt(versionN, f.agentID))
	m3dTrafficWant(ctx, t, f, v4, map[uint16]string{18208: "open"})
	m3dTrafficWant(ctx, t, f, v4, map[uint16]string{18209: "notopen"})
	const minAttempts = 20
	for _, p := range []struct {
		name string
		log  string
	}{{"IPv4", stop4(minAttempts)}, {"IPv6", stop6(minAttempts)}} {
		if err := m3dxTrafficLeaks(p.log, []uint16{18209}, minAttempts); err != nil {
			t.Errorf("%s probe: %v\n%s", p.name, err, p.log)
		}
	}
}
