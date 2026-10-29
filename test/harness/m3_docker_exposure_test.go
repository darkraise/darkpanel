//go:build harness

package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// m3dExpoSrcB6 is a second scanner source address for IPv6 inside the lab's
// 2001:db8:d0::/64 link; the first is scannerIPv6.
const m3dExpoSrcB6 = "2001:db8:d0::30"

// m3dExpoAllowed mirrors exposure.Allowed.
type m3dExpoAllowed struct {
	Sources   []string `json:"sources"`
	Ifaces    []string `json:"ifaces,omitempty"`
	SrcPorts  []string `json:"srcPorts,omitempty"`
	DecidedBy string   `json:"decidedBy"`
}

// m3dExpoClass mirrors exposure.Verdict, the verdict of one source class.
type m3dExpoClass struct {
	Class     string           `json:"class"`
	Range     string           `json:"range,omitempty"`
	Verdict   string           `json:"verdict"`
	DecidedBy string           `json:"decidedBy,omitempty"`
	Allowed   []m3dExpoAllowed `json:"allowed,omitempty"`
	Note      string           `json:"note,omitempty"`
}

// m3dExpoContainer mirrors firewall.ExposureContainer.
type m3dExpoContainer struct {
	Name          string         `json:"name"`
	Binding       m3dDockBinding `json:"binding"`
	ContainerPort uint16         `json:"containerPort"`
	Proxied       bool           `json:"proxied"`
}

// m3dExpoRow mirrors firewall.ExposureRow.
type m3dExpoRow struct {
	Proto     string            `json:"proto"`
	Addr      string            `json:"addr"`
	Port      uint16            `json:"port"`
	Family    string            `json:"family"`
	Process   string            `json:"process,omitempty"`
	Container *m3dExpoContainer `json:"container,omitempty"`
	Classes   []m3dExpoClass    `json:"classes"`
}

// m3dExpoView mirrors firewall.ExposureView, the firewall.exposure resource.
type m3dExpoView struct {
	Rows  []m3dExpoRow `json:"rows"`
	Notes []string     `json:"notes,omitempty"`
}

// m3dExpoDecode decodes the JSON of the firewall.exposure resource.
func m3dExpoDecode(raw []byte) (m3dExpoView, error) {
	var v m3dExpoView
	if err := json.Unmarshal(raw, &v); err != nil {
		return m3dExpoView{}, fmt.Errorf("harness: firewall.exposure: %w", err)
	}
	return v, nil
}

// m3dExpoClassOf returns the class of r with that name and range, which is ""
// for every class but a private one.
func (r m3dExpoRow) m3dExpoClassOf(class, rng string) (m3dExpoClass, bool) {
	for _, c := range r.Classes {
		if c.Class == class && c.Range == rng {
			return c, true
		}
	}
	return m3dExpoClass{}, false
}

// m3dExpoBySourcePort reports whether any allowed group of c names a source
// port.
func (c m3dExpoClass) m3dExpoBySourcePort() bool {
	return slices.ContainsFunc(c.Allowed, func(a m3dExpoAllowed) bool { return len(a.SrcPorts) > 0 })
}

// m3dxExpoPortRange parses a source port entry, <n>, <from>-<to> or
// <from>:<to>, with ports from 1 to 65535.
func m3dxExpoPortRange(entry string) (lo, hi uint16, err error) {
	s := strings.TrimSpace(entry)
	a, b, found := strings.Cut(s, "-")
	if !found {
		a, b, found = strings.Cut(s, ":")
	}
	if !found {
		b = a
	}
	l, errLo := strconv.ParseUint(strings.TrimSpace(a), 10, 16)
	h, errHi := strconv.ParseUint(strings.TrimSpace(b), 10, 16)
	if errLo != nil || errHi != nil || l < 1 || l > h {
		return 0, 0, fmt.Errorf("harness: source port entry %q is not <n>, <from>-<to> or <from>:<to> within 1-65535", entry)
	}
	return uint16(l), uint16(h), nil
}

// m3dxExpoSourceHas reports whether the source entry, any, a CIDR, an
// address or a range <from>-<to>, holds src.
func m3dxExpoSourceHas(entry string, src netip.Addr) (bool, error) {
	s := strings.TrimSpace(entry)
	if s == "any" {
		return true, nil
	}
	if p, err := netip.ParsePrefix(s); err == nil {
		return p.Contains(src), nil
	}
	if a, err := netip.ParseAddr(s); err == nil {
		return a == src, nil
	}
	if from, to, ok := strings.Cut(s, "-"); ok {
		f, errFrom := netip.ParseAddr(strings.TrimSpace(from))
		e, errTo := netip.ParseAddr(strings.TrimSpace(to))
		if errFrom == nil && errTo == nil && f.Is4() == e.Is4() && f.Compare(e) <= 0 {
			return f.Is4() == src.Is4() && f.Compare(src) <= 0 && src.Compare(e) <= 0, nil
		}
	}
	return false, fmt.Errorf("harness: source entry %q is not any, a CIDR, an address or a range", entry)
}

// m3dxExpoHolds reports whether the allowed group a holds the probe: its
// sources, interfaces and source ports are read as a conjunction.
func m3dxExpoHolds(a m3dExpoAllowed, src netip.Addr, srcPort uint16, iface string) (bool, error) {
	fromSrc := false
	for _, s := range a.Sources {
		in, err := m3dxExpoSourceHas(s, src)
		if err != nil {
			return false, err
		}
		fromSrc = fromSrc || in
	}
	onIface := len(a.Ifaces) == 0 || slices.Contains(a.Ifaces, iface)
	onPort := true
	if len(a.SrcPorts) > 0 {
		if srcPort == 0 {
			return false, fmt.Errorf("harness: the verdict depends on source ports %q and the probe has none", a.SrcPorts)
		}
		onPort = false
		for _, p := range a.SrcPorts {
			lo, hi, err := m3dxExpoPortRange(p)
			if err != nil {
				return false, err
			}
			onPort = onPort || lo <= srcPort && srcPort <= hi
		}
	}
	return fromSrc && onIface && onPort, nil
}

// m3dxExpoExpect is the scan result the class promises for one probe from src
// and srcPort (0: none) arriving on iface: open, closed, filtered, or notopen
// for a partial verdict that does not hold the probe.
func m3dxExpoExpect(c m3dExpoClass, src netip.Addr, srcPort uint16, iface string) (string, error) {
	switch c.Verdict {
	case "open", "closed", "filtered":
		return c.Verdict, nil
	case "partial":
	default:
		return "", fmt.Errorf("harness: class %s has verdict %q (%s), which promises no scan result", c.Class, c.Verdict, c.Note)
	}
	held := false
	for _, a := range c.Allowed {
		ok, err := m3dxExpoHolds(a, src, srcPort, iface)
		if err != nil {
			return "", err
		}
		held = held || ok
	}
	if held {
		return "open", nil
	}
	return "notopen", nil
}

// m3dxExpoSourcePorts returns a source port inside the intervals, the first
// interval's first port, and one outside them: the lowest port from 40001 up
// that no interval covers, or else 1024 or 1025.
func m3dxExpoSourcePorts(srcPorts []string) (inside, outside uint16, err error) {
	if len(srcPorts) == 0 {
		return 0, 0, errors.New("harness: no source port intervals")
	}
	type interval struct{ lo, hi uint16 }
	var ivs []interval
	for _, e := range srcPorts {
		lo, hi, err := m3dxExpoPortRange(e)
		if err != nil {
			return 0, 0, err
		}
		ivs = append(ivs, interval{lo, hi})
	}
	covered := func(p int) bool {
		return slices.ContainsFunc(ivs, func(iv interval) bool { return int(iv.lo) <= p && p <= int(iv.hi) })
	}
	for p := 40001; p <= 65535; p++ {
		if !covered(p) {
			return ivs[0].lo, uint16(p), nil //nolint:gosec // p is at most 65535
		}
	}
	for _, p := range []uint16{1024, 1025} {
		if !covered(int(p)) {
			return ivs[0].lo, p, nil
		}
	}
	return 0, 0, fmt.Errorf("harness: source port intervals %q leave no port outside them", srcPorts)
}

// m3dxExpoCompare checks a scan result against an expectation: open, closed,
// filtered or notopen. TCP is exact. A UDP scan hears only from a service
// that replies, so open is contradicted only by closed, and every other
// expectation only by open; open|filtered is no answer.
func m3dxExpoCompare(expect string, got PortState, proto string) error {
	switch expect {
	case "open", "closed", "filtered", "notopen":
	default:
		return fmt.Errorf("harness: unknown expectation %q", expect)
	}
	if got.Proto != proto {
		return fmt.Errorf("harness: %s/%d is a %s result, want %s", proto, got.Port, got.Proto, proto)
	}
	var ok bool
	switch {
	case proto == "udp" && expect == "open":
		ok = got.State != "closed"
	case proto == "udp":
		ok = got.State != "open"
	default:
		ok = got.State == expect || expect == "notopen" && (got.State == "closed" || got.State == "filtered")
	}
	if !ok {
		return fmt.Errorf("harness: %s/%d is %s, want %s", proto, got.Port, got.State, expect)
	}
	return nil
}

// m3dxExpoTarget returns the address the scanner can probe for a row of addr
// and family: inet4 or inet6 when the row listens on the wildcard or on that
// address, and false for a loopback, private-link or bridge listener.
func m3dxExpoTarget(addr, family, inet4, inet6 string) (string, bool) {
	a, err := netip.ParseAddr(addr)
	if err != nil {
		return "", false
	}
	var want netip.Addr
	var target string
	switch family {
	case "v4":
		want, target = netip.IPv4Unspecified(), inet4
	case "v6":
		want, target = netip.IPv6Unspecified(), inet6
	default:
		return "", false
	}
	t, err := netip.ParseAddr(target)
	if err != nil || a != want && a != t {
		return "", false
	}
	return target, true
}

// m3dxExpoVerdicts checks the internet class of rows against want, which maps
// <proto>/<family>/<port> to a verdict. It reports, in key order, every key
// without a row, every key whose verdict differs and every key whose rows
// (one per address) disagree.
func m3dxExpoVerdicts(rows []m3dExpoRow, want map[string]string) error {
	got := map[string][]string{}
	for _, r := range rows {
		key := fmt.Sprintf("%s/%s/%d", r.Proto, r.Family, r.Port)
		if _, ok := want[key]; !ok {
			continue
		}
		v := ""
		if c, ok := r.m3dExpoClassOf("internet", ""); ok {
			v = c.Verdict
		}
		if !slices.Contains(got[key], v) {
			got[key] = append(got[key], v)
		}
	}
	keys := make([]string, 0, len(want))
	for k := range want {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var msgs []string
	for _, k := range keys {
		switch vs := got[k]; {
		case len(vs) == 0:
			msgs = append(msgs, k+" has no row")
		case len(vs) > 1:
			msgs = append(msgs, fmt.Sprintf("%s has rows with different verdicts (%s)", k, strings.Join(vs, ", ")))
		case vs[0] != want[k]:
			msgs = append(msgs, fmt.Sprintf("%s is %s, want %s", k, vs[0], want[k]))
		}
	}
	if len(msgs) == 0 {
		return nil
	}
	return errors.New("harness: " + strings.Join(msgs, "; "))
}

const m3dxExpoWorkflowStep = "Run the M3 Docker harness"

var (
	m3dxExpoTimeoutRE = regexp.MustCompile(`-timeout (\d+)m\b`)
	m3dxExpoLimitRE   = regexp.MustCompile(`timeout-minutes:\s*(\d+)`)
)

// m3dxExpoSteps splits the text of a workflow into its steps, which start at
// lines of six spaces and "- ".
func m3dxExpoSteps(yml string) []string {
	var steps []string
	for _, line := range strings.Split(yml, "\n") {
		if strings.HasPrefix(line, "      - ") || len(steps) == 0 {
			steps = append(steps, "")
		}
		steps[len(steps)-1] += line + "\n"
	}
	return steps
}

// m3dxExpoJob returns the lines of the job name, from its key to the next
// key at the same or a lower indentation.
func m3dxExpoJob(yml, name string) (string, bool) {
	var job []string
	in := false
	for _, line := range strings.Split(yml, "\n") {
		trimmed := strings.TrimSpace(line)
		indent := len(line) - len(strings.TrimLeft(line, " "))
		switch {
		case !in && line == "  "+name+":":
			in = true
		case in && trimmed != "" && !strings.HasPrefix(trimmed, "#") && indent <= 2:
			return strings.Join(job, "\n"), true
		}
		if in {
			job = append(job, line)
		}
	}
	return strings.Join(job, "\n"), in
}

// m3dxExpoWorkflow checks the text of harness.yml: the step that runs
// TestM3Docker, a -timeout of at least deadline plus 10 minutes, the failure
// artifact of its working directory, and a vm job limit that covers every
// -timeout of the job plus 30 minutes of setup.
func m3dxExpoWorkflow(yml string, deadline time.Duration) error {
	var step string
	for _, s := range m3dxExpoSteps(yml) {
		for _, line := range strings.Split(s, "\n") {
			if strings.TrimPrefix(strings.TrimSpace(line), "- ") == "name: "+m3dxExpoWorkflowStep {
				step = s
			}
		}
	}
	if step == "" {
		return fmt.Errorf("harness: the workflow has no step named %q", m3dxExpoWorkflowStep)
	}
	for _, want := range []string{"-run '^TestM3Docker$'", " -m3", "-os ${{ matrix.os }}", `-workdir "$RUNNER_TEMP/harness-m3d"`} {
		if !strings.Contains(step, want) {
			return fmt.Errorf("harness: the step %q lacks %s", m3dxExpoWorkflowStep, want)
		}
	}
	m := m3dxExpoTimeoutRE.FindStringSubmatch(step)
	if m == nil {
		return fmt.Errorf("harness: the step %q has no -timeout <n>m, which must cover the deadline", m3dxExpoWorkflowStep)
	}
	n, _ := strconv.Atoi(m[1])
	if time.Duration(n)*time.Minute < deadline+10*time.Minute {
		return fmt.Errorf("harness: the step %q has -timeout %sm, under the deadline %s plus 10 minutes", m3dxExpoWorkflowStep, m[1], deadline)
	}
	if !strings.Contains(yml, "harness-m3d/**/*.log") {
		return errors.New("harness: the failure artifact does not collect harness-m3d/**/*.log")
	}
	job, ok := m3dxExpoJob(yml, "vm")
	if !ok {
		return errors.New("harness: the workflow has no vm job")
	}
	limit := m3dxExpoLimitRE.FindStringSubmatch(job)
	if limit == nil {
		return errors.New("harness: the vm job has no timeout-minutes")
	}
	total := 30
	for _, t := range m3dxExpoTimeoutRE.FindAllStringSubmatch(job, -1) {
		v, _ := strconv.Atoi(t[1])
		total += v
	}
	if have, _ := strconv.Atoi(limit[1]); have < total {
		return fmt.Errorf("harness: the vm job has timeout-minutes %d, want at least %d (its -timeout values plus 30 minutes of setup)", have, total)
	}
	return nil
}

// m3dExpoRead reads firewall.exposure of the agent and fails the test when it
// cannot be read or decoded.
func m3dExpoRead(ctx context.Context, t *testing.T, f *fleet) ([]byte, m3dExpoView) {
	t.Helper()
	var raw json.RawMessage
	if err := f.api.Resource(ctx, f.agentID, "firewall.exposure", &raw); err != nil {
		t.Fatal(err)
	}
	view, err := m3dExpoDecode(raw)
	if err != nil {
		t.Fatal(err)
	}
	return raw, view
}

// m3dExpoPublishUDP starts the UDP container name on the default bridge,
// publishing publish, and removes it when the test ends.
func m3dExpoPublishUDP(ctx context.Context, t *testing.T, f *fleet, name string, publish ...string) {
	t.Helper()
	t.Cleanup(func() { m3dDockRemove(ctx, f.agent, name) })
	m3dRunUDP(ctx, t, f.agent, name, "bridge", publish...)
	m3dWaitRunning(ctx, t, f.agent, name)
}

// m3dExpoAllowUDP allows the UDP binding of selector and port from any
// source.
func m3dExpoAllowUDP(ctx context.Context, t *testing.T, f *fleet, selector string, port uint16) {
	t.Helper()
	m3dDockApply(ctx, t, f, "firewall.container.allow", map[string]any{
		"binding": m3dDockBinding{Selector: selector, HostPort: port, Proto: "udp"},
	})
}

// m3dExpoProbe scans the one TCP port of o and records a failure when the
// result differs from expect.
func m3dExpoProbe(ctx context.Context, t *testing.T, f *fleet, o ScanOpts, expect string) {
	t.Helper()
	scanned := m3dScan(ctx, t, f.scanner, o)
	if len(scanned) == 0 {
		t.Errorf("scan %s of %s port %v from %v reported no port", o.Family, o.Target, o.Ports, o.Source)
		return
	}
	if err := m3dxExpoCompare(expect, scanned[0], "tcp"); err != nil {
		t.Errorf("scan %s of %s from %v: %v", o.Family, o.Target, o.Source, err)
	}
}

// m3dExpoCheck is spec 2.7's check of a view: it has no notes, the scan
// agrees with every row whose verdict is one result, and every partial TCP
// row not split by source port answers each scanner source as its allowed
// sources say.
func m3dExpoCheck(ctx context.Context, t *testing.T, f *fleet, raw []byte, view m3dExpoView) {
	t.Helper()
	if len(view.Notes) > 0 {
		t.Errorf("exposure notes %q, want none", view.Notes)
	}
	rows, err := m3dxRows(raw)
	if err != nil {
		t.Fatal(err)
	}
	families := []struct{ family, flag string }{{"v4", "-4"}, {"v6", "-6"}}
	for _, fam := range families {
		for _, proto := range []string{"tcp", "udp"} {
			var scannable []m3dRow
			var ports []uint16
			target := ""
			for _, r := range rows {
				if r.Proto != proto || r.Family != fam.family {
					continue
				}
				if tg, ok := m3dxExpoTarget(r.Addr, r.Family, agentIPv4, agentIPv6); ok {
					target = tg
					scannable = append(scannable, r)
					ports = append(ports, r.Port)
				}
			}
			if len(scannable) == 0 {
				continue
			}
			slices.Sort(ports)
			scanned := m3dScan(ctx, t, f.scanner, ScanOpts{Family: fam.flag, UDP: proto == "udp", Ports: slices.Compact(ports), Target: target})
			var plain []m3dRow
			for _, r := range scannable {
				if r.Verdict != "partial" && r.Verdict != "unknown" {
					plain = append(plain, r)
				}
			}
			for _, msg := range m3dxMismatch(plain, scanned, proto) {
				t.Error(msg)
			}
		}
	}

	sources := map[string][]string{"v4": {scannerIPv4, m3dTrafficSrcB}, "v6": {scannerIPv6, m3dExpoSrcB6}}
	flags := map[string]string{"v4": "-4", "v6": "-6"}
	split := map[string]map[string]bool{"v4": {}, "v6": {}}
	for _, r := range view.Rows {
		target, ok := m3dxExpoTarget(r.Addr, r.Family, agentIPv4, agentIPv6)
		if !ok || r.Proto != "tcp" {
			continue
		}
		class, ok := r.m3dExpoClassOf("internet", "")
		if !ok {
			t.Errorf("row %s %s:%d has no internet class", r.Proto, r.Addr, r.Port)
			continue
		}
		switch {
		case class.Verdict == "unknown" && r.Port >= 18301 && r.Port <= 18399:
			t.Errorf("row %s %s:%d has an unknown internet verdict: %s", r.Proto, r.Addr, r.Port, class.Note)
		case class.Verdict == "unknown":
			t.Logf("row %s %s:%d has an unknown internet verdict: %s", r.Proto, r.Addr, r.Port, class.Note)
		case class.Verdict == "partial" && !class.m3dExpoBySourcePort():
			for i, s := range sources[r.Family] {
				src := netip.MustParseAddr(s)
				expect, err := m3dxExpoExpect(class, src, 0, m3dTrafficIface)
				if err != nil {
					t.Errorf("row %s %s:%d: %v", r.Proto, r.Addr, r.Port, err)
					continue
				}
				if r.Port == 18302 {
					split[r.Family][expect] = true
				}
				o := ScanOpts{Family: flags[r.Family], Ports: []uint16{r.Port}, Target: target}
				if i > 0 {
					o.Source, o.Iface = src, m3dTrafficIface
				}
				m3dExpoProbe(ctx, t, f, o, expect)
			}
		}
	}
	for _, fam := range families {
		if !split[fam.family]["open"] || !split[fam.family]["notopen"] {
			t.Errorf("port 18302 over %s: the sources must split into open and notopen, got %v", fam.family, split[fam.family])
		}
	}
}

// m3dExpoScan requires the exposure view to equal the scan: a partial rule
// scanned from inside and outside it, UDP, and IPv6 published to an
// IPv4-only bridge.
func m3dExpoScan(ctx context.Context, t *testing.T, f *fleet) {
	m3dDockEnable(ctx, t, f)
	m3dAddSource(ctx, t, f.scanner, netip.MustParsePrefix(m3dTrafficSrcB+"/32"), m3dTrafficIface)
	m3dAddSource(ctx, t, f.scanner, netip.MustParsePrefix(m3dExpoSrcB6+"/128"), m3dTrafficIface)
	m3dTrafficNetwork(ctx, t, f, "m3dxn", "--ipv6", "--subnet", "fd00:3d:2::/64")
	m3dTrafficPublish(ctx, t, f, "m3d-x1", "bridge", "18301:14000")
	m3dTrafficAllow(ctx, t, f, "m3d-x1", 18301, "")
	m3dTrafficPublish(ctx, t, f, "m3d-x2", "m3dxn", "18302:14000")
	m3dTrafficAllow(ctx, t, f, "m3d-x2", 18302, scannerIPv4+"/32")
	m3dTrafficAllow(ctx, t, f, "m3d-x2", 18302, scannerIPv6+"/128")
	m3dTrafficPublish(ctx, t, f, "m3d-x3", "bridge", "18303:14000")
	m3dExpoPublishUDP(ctx, t, f, "m3d-x4", "18304:8053/udp")
	m3dExpoAllowUDP(ctx, t, f, "m3d-x4", 18304)
	m3dExpoPublishUDP(ctx, t, f, "m3d-x5", "18305:8053/udp")
	m3dTrafficPublish(ctx, t, f, "m3d-x6", "bridge", "18306:14000")
	m3dTrafficPublish(ctx, t, f, "m3d-x7", "bridge", "18307:14000")
	m3dTrafficAllow(ctx, t, f, "m3d-x7", 18307, "")

	want := map[string]string{
		"tcp/v4/18301": "open", "tcp/v6/18301": "open",
		"tcp/v4/18302": "partial", "tcp/v6/18302": "partial",
		"tcp/v4/18303": "filtered", "tcp/v6/18303": "filtered",
		"udp/v4/18304": "open", "udp/v4/18305": "filtered",
		"tcp/v4/18306": "filtered", "tcp/v6/18306": "filtered",
		"tcp/v4/18307": "open", "tcp/v6/18307": "open",
	}
	var raw []byte
	var view m3dExpoView
	err := eventually(ctx, 2*time.Minute, func(ctx context.Context) error {
		raw, view = m3dExpoRead(ctx, t, f)
		return m3dxExpoVerdicts(view.Rows, want)
	})
	if err != nil {
		t.Fatalf("firewall.exposure: %v", err)
	}
	m3dExpoCheck(ctx, t, f, raw, view)
}

// m3dExpoSourcePort requires a rule restricted by source port to be scanned
// as the view says from a source port inside its range and from one outside.
func m3dExpoSourcePort(ctx context.Context, t *testing.T, f *fleet) {
	m3dDockSetup(ctx, t, f.agent,
		"sudo systemd-run --unit=m3d-sp --collect /usr/bin/python3 -m http.server 18311 --bind 0.0.0.0",
		"sudo systemctl stop m3d-sp")
	m3dDockSetup(ctx, t, f.agent,
		"sudo ufw allow proto tcp from any port 5300:5399 to any port 18311",
		"sudo ufw delete allow proto tcp from any port 5300:5399 to any port 18311")
	m3WaitStatus(ctx, t, f.api, f.agentID, time.Minute, m3Healthy)
	var class m3dExpoClass
	err := eventually(ctx, time.Minute, func(ctx context.Context) error {
		_, view := m3dExpoRead(ctx, t, f)
		for _, r := range view.Rows {
			if r.Proto != "tcp" || r.Family != "v4" || r.Port != 18311 {
				continue
			}
			c, ok := r.m3dExpoClassOf("internet", "")
			if ok && c.Verdict == "partial" && c.m3dExpoBySourcePort() {
				class = c
				return nil
			}
			return fmt.Errorf("harness: row tcp/v4/18311 internet class is %+v, want partial by source port", c)
		}
		return errors.New("harness: no row tcp/v4/18311")
	})
	if err != nil {
		t.Fatalf("firewall.exposure: %v", err)
	}
	var srcPorts []string
	for _, a := range class.Allowed {
		srcPorts = append(srcPorts, a.SrcPorts...)
	}
	inside, outside, err := m3dxExpoSourcePorts(srcPorts)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []struct {
		port   uint16
		expect string
	}{{inside, "open"}, {outside, "notopen"}} {
		expect, err := m3dxExpoExpect(class, netip.MustParseAddr(scannerIPv4), p.port, m3dTrafficIface)
		if err != nil {
			t.Fatal(err)
		}
		if expect != p.expect {
			t.Fatalf("source port %d: the view promises %s, want %s", p.port, expect, p.expect)
		}
		scanned := m3dScan(ctx, t, f.scanner, ScanOpts{Family: "-4", Ports: []uint16{18311}, SourcePort: p.port, Target: agentIPv4})
		if len(scanned) == 0 {
			t.Fatalf("scan from source port %d reported no port", p.port)
		}
		if err := m3dxExpoCompare(expect, scanned[0], "tcp"); err != nil {
			t.Errorf("source port %d: %v", p.port, err)
		}
	}
}

// m3dExpoPrivateClass requires a source inside a configured privateSources
// range to be the private class, which reaches a published port that the
// internet class cannot.
func m3dExpoPrivateClass(ctx context.Context, t *testing.T, f *fleet) {
	const srcRange = "198.18.1.0/24"
	m3dDockEnable(ctx, t, f)
	m3dAddSource(ctx, t, f.scanner, netip.MustParsePrefix(m3dTrafficSrcB+"/32"), m3dTrafficIface)
	m3dTrafficPublish(ctx, t, f, "m3d-q1", "bridge", "18321:14000")
	st, err := f.api.m3dDockRead(ctx, f.agentID)
	if err != nil {
		t.Fatalf("read firewall.docker: %v", err)
	}
	orig := st.Settings.PrivateSources
	row := func(view m3dExpoView) (m3dExpoRow, error) {
		for _, r := range view.Rows {
			if r.Proto == "tcp" && r.Family == "v4" && r.Port == 18321 {
				return r, nil
			}
		}
		return m3dExpoRow{}, errors.New("harness: no row tcp/v4/18321")
	}
	err = eventually(ctx, time.Minute, func(ctx context.Context) error {
		_, view := m3dExpoRead(ctx, t, f)
		r, err := row(view)
		if err != nil {
			return err
		}
		if c, ok := r.m3dExpoClassOf("internet", ""); !ok || c.Verdict != "filtered" {
			return fmt.Errorf("harness: row tcp/v4/18321 internet class is %+v, want filtered", c)
		}
		if _, ok := r.m3dExpoClassOf("private", srcRange); ok {
			return fmt.Errorf("harness: row tcp/v4/18321 has a private class of %s before it is configured", srcRange)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("firewall.exposure: %v", err)
	}
	t.Cleanup(func() { m3dDockTry(ctx, t, f, "firewall.docker.set-sources", map[string]any{"privateSources": orig}) })
	m3dDockApply(ctx, t, f, "firewall.docker.set-sources", map[string]any{"privateSources": append(slices.Clone(orig), srcRange)})
	err = eventually(ctx, time.Minute, func(ctx context.Context) error {
		_, view := m3dExpoRead(ctx, t, f)
		r, err := row(view)
		if err != nil {
			return err
		}
		if c, ok := r.m3dExpoClassOf("private", srcRange); !ok || c.Verdict != "open" {
			return fmt.Errorf("harness: row tcp/v4/18321 private class of %s is %+v, want open", srcRange, c)
		}
		if c, ok := r.m3dExpoClassOf("internet", ""); !ok || c.Verdict != "filtered" {
			return fmt.Errorf("harness: row tcp/v4/18321 internet class is %+v, want filtered", c)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("firewall.exposure: %v", err)
	}
	m3dExpoProbe(ctx, t, f, ScanOpts{Family: "-4", Ports: []uint16{18321}, Target: agentIPv4}, "filtered")
	m3dExpoProbe(ctx, t, f, ScanOpts{Family: "-4", Ports: []uint16{18321}, Target: agentIPv4, Source: netip.MustParseAddr(m3dTrafficSrcB), Iface: m3dTrafficIface}, "open")
	m3dTrafficView(ctx, t, f, "m3d-q1", 18321, func(b m3dDockBindingView) error {
		if !slices.Contains(b.PrivateReach, srcRange) {
			return fmt.Errorf("harness: m3d-q1 port 18321 privateReach %q lacks %s", b.PrivateReach, srcRange)
		}
		return nil
	})
}
