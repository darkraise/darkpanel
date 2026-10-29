//go:build harness

package harness

import (
	"net/netip"
	"os"
	"strings"
	"testing"
	"time"
)

// m3dxExpoDoc is the JSON of firewall.exposure (Contracts: ExposureView)
// with a partial row that splits by source port.
const m3dxExpoDoc = `{"rows":[
{"proto":"tcp","addr":"0.0.0.0","port":18302,"family":"v4","container":{"name":"m3d-x2","binding":{"selector":"m3d-x2","hostPort":18302,"proto":"tcp"},"containerPort":14000,"proxied":false},
 "classes":[
  {"class":"internet","verdict":"partial","allowed":[{"sources":["198.18.0.20/32"],"ifaces":["dpinet"],"decidedBy":"binding dp-b-0123456789ab"}]},
  {"class":"private","range":"10.0.0.0/8","verdict":"open","decidedBy":"DOCKER-USER -s 10.0.0.0/8"},
  {"class":"tailnet","verdict":"closed","note":"host firewall; tailnet policy not evaluated"},
  {"class":"localhost","verdict":"open"}]},
{"proto":"tcp","addr":"0.0.0.0","port":18311,"family":"v4","process":"python3",
 "classes":[{"class":"internet","verdict":"partial","allowed":[{"sources":["any"],"srcPorts":["5300-5399"],"decidedBy":"ufw-user-input #12"}]}]}],
"notes":["Docker is unavailable"]}`

func TestM3xExpoDecode(t *testing.T) {
	v, err := m3dExpoDecode([]byte(m3dxExpoDoc))
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Rows) != 2 || len(v.Notes) != 1 {
		t.Fatalf("rows %d, notes %q", len(v.Rows), v.Notes)
	}
	r := v.Rows[0]
	if r.Proto != "tcp" || r.Addr != "0.0.0.0" || r.Port != 18302 || r.Family != "v4" || r.Container == nil || r.Container.Name != "m3d-x2" {
		t.Errorf("row %+v", r)
	}
	c, ok := r.m3dExpoClassOf("internet", "")
	if !ok || c.Verdict != "partial" || len(c.Allowed) != 1 || c.Allowed[0].Sources[0] != "198.18.0.20/32" || c.Allowed[0].Ifaces[0] != "dpinet" {
		t.Errorf("internet class %+v, %v", c, ok)
	}
	if c, ok := r.m3dExpoClassOf("private", "10.0.0.0/8"); !ok || c.Verdict != "open" {
		t.Errorf("private class of 10.0.0.0/8: %+v, %v", c, ok)
	}
	if _, ok := r.m3dExpoClassOf("private", "192.168.0.0/16"); ok {
		t.Error("a private class of a range the row does not list was found")
	}
	if _, ok := r.m3dExpoClassOf("nonesuch", ""); ok {
		t.Error("an unknown class was found")
	}
	if _, err := m3dExpoDecode([]byte(`{"rows":[{"proto":"tcp"}]`)); err == nil {
		t.Error("truncated JSON decoded")
	}
}

func TestM3xExpoExpect(t *testing.T) {
	in, out := netip.MustParseAddr("198.18.0.20"), netip.MustParseAddr("198.18.1.30")
	partial := m3dExpoClass{Class: "internet", Verdict: "partial", Allowed: []m3dExpoAllowed{
		{Sources: []string{"198.18.0.20/32"}, Ifaces: []string{"dpinet"}},
		{Sources: []string{"198.18.1.1-198.18.1.9", "203.0.113.7"}},
	}}
	bySrcPort := m3dExpoClass{Class: "internet", Verdict: "partial", Allowed: []m3dExpoAllowed{{Sources: []string{"any"}, SrcPorts: []string{"5300-5399", "7000"}}}}
	for _, tc := range []struct {
		name    string
		c       m3dExpoClass
		src     netip.Addr
		srcPort uint16
		iface   string
		want    string // "" for an error
	}{
		{"open", m3dExpoClass{Verdict: "open"}, out, 0, "dpinet", "open"},
		{"closed", m3dExpoClass{Verdict: "closed"}, out, 0, "dpinet", "closed"},
		{"filtered", m3dExpoClass{Verdict: "filtered"}, out, 0, "dpinet", "filtered"},
		{"partial, inside by prefix", partial, in, 0, "dpinet", "open"},
		{"partial, outside", partial, out, 0, "dpinet", "notopen"},
		{"partial, inside by range", partial, netip.MustParseAddr("198.18.1.5"), 0, "dpinet", "open"},
		{"partial, range end", partial, netip.MustParseAddr("198.18.1.9"), 0, "dpinet", "open"},
		{"partial, past the range", partial, netip.MustParseAddr("198.18.1.10"), 0, "dpinet", "notopen"},
		{"partial, inside by address", partial, netip.MustParseAddr("203.0.113.7"), 0, "dpinet", "open"},
		{"partial, wrong interface", partial, in, 0, "dppriv", "notopen"},
		{"source port inside", bySrcPort, in, 5350, "dpinet", "open"},
		{"source port single", bySrcPort, in, 7000, "dpinet", "open"},
		{"source port outside", bySrcPort, in, 5400, "dpinet", "notopen"},
		{"source port unknown", bySrcPort, in, 0, "dpinet", ""},
		{"unknown verdict", m3dExpoClass{Verdict: "unknown", Note: "too many partitions"}, in, 0, "dpinet", ""},
		{"unreadable source", m3dExpoClass{Verdict: "partial", Allowed: []m3dExpoAllowed{{Sources: []string{"lan"}}}}, in, 0, "dpinet", ""},
	} {
		got, err := m3dxExpoExpect(tc.c, tc.src, tc.srcPort, tc.iface)
		if tc.want == "" {
			if err == nil {
				t.Errorf("%s: %q, want an error", tc.name, got)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("%s: %q, %v, want %q", tc.name, got, err, tc.want)
		}
	}
}

func TestM3xExpoSourcePorts(t *testing.T) {
	for _, tc := range []struct {
		in       []string
		inside   uint16
		outside  uint16
		wantFail bool
	}{
		{[]string{"5300-5399"}, 5300, 40001, false},
		{[]string{"5300:5399", "7000"}, 5300, 40001, false},
		{[]string{"53"}, 53, 40001, false},
		{[]string{"40001-40010"}, 40001, 40011, false},
		{[]string{"1-65535"}, 0, 0, true},
		{[]string{"x"}, 0, 0, true},
		{[]string{"9-5"}, 0, 0, true},
		{nil, 0, 0, true},
	} {
		inside, outside, err := m3dxExpoSourcePorts(tc.in)
		switch {
		case tc.wantFail && err == nil:
			t.Errorf("%q = %d, %d, want an error", tc.in, inside, outside)
		case !tc.wantFail && (err != nil || inside != tc.inside || outside != tc.outside):
			t.Errorf("%q = %d, %d, %v, want %d, %d", tc.in, inside, outside, err, tc.inside, tc.outside)
		}
	}
}

func TestM3xExpoCompare(t *testing.T) {
	for _, tc := range []struct {
		expect, proto, state string
		ok                   bool
	}{
		{"open", "tcp", "open", true},
		{"open", "tcp", "filtered", false},
		{"closed", "tcp", "closed", true},
		{"closed", "tcp", "filtered", false},
		{"filtered", "tcp", "filtered", true},
		{"filtered", "tcp", "closed", false},
		{"notopen", "tcp", "closed", true},
		{"notopen", "tcp", "filtered", true},
		{"notopen", "tcp", "open", false},
		{"open", "udp", "open", true},
		{"open", "udp", "open|filtered", true},
		{"open", "udp", "closed", false},
		{"closed", "udp", "closed", true},
		{"closed", "udp", "open|filtered", true},
		{"closed", "udp", "open", false},
		{"filtered", "udp", "open", false},
		{"notopen", "udp", "open|filtered", true},
		{"notopen", "udp", "open", false},
	} {
		err := m3dxExpoCompare(tc.expect, PortState{Port: 18304, Proto: tc.proto, State: tc.state}, tc.proto)
		if (err == nil) != tc.ok {
			t.Errorf("m3dxExpoCompare(%s, %s %s) = %v, want ok=%t", tc.expect, tc.proto, tc.state, err, tc.ok)
		}
	}
	if err := m3dxExpoCompare("open", PortState{Port: 1, Proto: "udp", State: "open"}, "tcp"); err == nil {
		t.Error("a UDP result was compared as TCP")
	}
}

func TestM3xExpoTarget(t *testing.T) {
	const inet4, inet6 = "198.18.0.11", "2001:db8:d0::11"
	for _, tc := range []struct {
		addr, family string
		want         string
		ok           bool
	}{
		{"0.0.0.0", "v4", inet4, true},
		{"198.18.0.11", "v4", inet4, true},
		{"::", "v6", inet6, true},
		{"2001:db8:d0::11", "v6", inet6, true},
		{"127.0.0.53", "v4", "", false},
		{"10.77.0.11", "v4", "", false},
		{"172.17.0.1", "v4", "", false},
		{"::1", "v6", "", false},
		{"0.0.0.0", "v6", "", false},
		{"::", "v4", "", false},
		{"0.0.0.0", "ipv4", "", false},
	} {
		got, ok := m3dxExpoTarget(tc.addr, tc.family, inet4, inet6)
		if got != tc.want || ok != tc.ok {
			t.Errorf("m3dxExpoTarget(%s, %s) = %q, %t, want %q, %t", tc.addr, tc.family, got, ok, tc.want, tc.ok)
		}
	}
}

const m3dxExpoWorkflowDoc = `jobs:
  build:
    timeout-minutes: 30
    steps:
      - run: go test -tags harness ./test/harness/...
  vm:
    timeout-minutes: 280
    steps:
      - name: Run the harness
        run: >-
          go test -tags harness -timeout 100m -v ./test/harness/
      - name: Run the M3 firewall harness
        run: >-
          go test -tags harness -timeout 70m -v -run '^TestM3Firewall$' ./test/harness/
          -args -os ${{ matrix.os }} -m3
      - name: Run the M3 Docker harness
        if: ${{ !cancelled() }}
        run: >-
          go test -tags harness -timeout 70m -v -run '^TestM3Docker$' ./test/harness/
          -args -os ${{ matrix.os }} -m3
          -workdir "$RUNNER_TEMP/harness-m3d"
      - if: failure()
        uses: actions/upload-artifact@v7
        with:
          path: |
            ${{ runner.temp }}/harness/**/*.log
            ${{ runner.temp }}/harness-m3d/**/*.log
`

func TestM3xExpoWorkflow(t *testing.T) {
	if err := m3dxExpoWorkflow(m3dxExpoWorkflowDoc, 60*time.Minute); err != nil {
		t.Fatalf("a complete workflow: %v", err)
	}
	for name, tc := range map[string]struct{ from, to, want string }{
		"no Docker step":         {"Run the M3 Docker harness", "Run another harness", "Run the M3 Docker harness"},
		"wrong test":             {"'^TestM3Docker$'", "'^TestM3Firewall$'", "TestM3Docker"},
		"no -m3":                 {"-args -os ${{ matrix.os }} -m3\n          -workdir", "-args -os ${{ matrix.os }}\n          -workdir", "-m3"},
		"timeout under deadline": {"-timeout 70m -v -run '^TestM3Docker$'", "-timeout 65m -v -run '^TestM3Docker$'", "deadline"},
		"job limit too small":    {"timeout-minutes: 280", "timeout-minutes: 200", "timeout-minutes"},
		"no logs":                {"${{ runner.temp }}/harness-m3d/**/*.log", "", "harness-m3d"},
	} {
		if !strings.Contains(m3dxExpoWorkflowDoc, tc.from) {
			t.Fatalf("%s: the fixture lacks %q", name, tc.from)
		}
		if err := m3dxExpoWorkflow(strings.Replace(m3dxExpoWorkflowDoc, tc.from, tc.to, 1), 60*time.Minute); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want an error naming %q", name, err, tc.want)
		}
	}
}

// TestM3xExpoWorkflowFile checks the committed workflow, which is the
// only place TestM3Docker runs.
func TestM3xExpoWorkflowFile(t *testing.T) {
	b, err := os.ReadFile("../../.github/workflows/harness.yml")
	if err != nil {
		t.Fatal(err)
	}
	if err := m3dxExpoWorkflow(string(b), m3dDockDeadline); err != nil {
		t.Error(err)
	}
}

func TestM3xExpoVerdicts(t *testing.T) {
	row := func(proto, addr, family string, port uint16, internet string) m3dExpoRow {
		return m3dExpoRow{Proto: proto, Addr: addr, Port: port, Family: family, Classes: []m3dExpoClass{{Class: "internet", Verdict: internet}, {Class: "localhost", Verdict: "open"}}}
	}
	rows := []m3dExpoRow{
		row("tcp", "0.0.0.0", "v4", 18301, "open"),
		row("tcp", "::", "v6", 18301, "filtered"),
		row("udp", "0.0.0.0", "v4", 18304, "open"),
		row("tcp", "198.18.0.11", "v4", 18301, "open"), // the same port on another address
	}
	if err := m3dxExpoVerdicts(rows, map[string]string{"tcp/v4/18301": "open", "tcp/v6/18301": "filtered", "udp/v4/18304": "open"}); err != nil {
		t.Errorf("matching verdicts: %v", err)
	}
	err := m3dxExpoVerdicts(rows, map[string]string{"tcp/v4/18301": "filtered", "tcp/v6/18301": "filtered", "tcp/v4/18399": "open"})
	if err == nil || !strings.Contains(err.Error(), "tcp/v4/18301") || !strings.Contains(err.Error(), "tcp/v4/18399") || strings.Contains(err.Error(), "tcp/v6/18301") {
		t.Errorf("a wrong and a missing row: %v, want an error naming exactly those two", err)
	}
	mixed := append(rows, row("tcp", "198.18.0.11", "v4", 18301, "filtered"))
	if err := m3dxExpoVerdicts(mixed, map[string]string{"tcp/v4/18301": "open"}); err == nil {
		t.Error("two rows of one key with different verdicts were accepted")
	}
}

func TestM3xExpoBySourcePort(t *testing.T) {
	plain := m3dExpoClass{Verdict: "partial", Allowed: []m3dExpoAllowed{{Sources: []string{"198.18.0.20/32"}}}}
	split := m3dExpoClass{Verdict: "partial", Allowed: []m3dExpoAllowed{{Sources: []string{"any"}, SrcPorts: []string{"5300-5399"}}}}
	if plain.m3dExpoBySourcePort() || !split.m3dExpoBySourcePort() || (m3dExpoClass{Verdict: "open"}).m3dExpoBySourcePort() {
		t.Error("m3dExpoBySourcePort must be true only for a class whose allowed sets name source ports")
	}
}

func TestM3xExpoUDPAnswer(t *testing.T) {
	scanned := []PortState{{Port: 18304, Proto: "udp", State: "open"}, {Port: 18305, Proto: "udp", State: "open|filtered"}, {Port: 18306, Proto: "tcp", State: "open"}}
	cases := []struct {
		port    uint16
		answers bool
		ok      bool
	}{
		{18304, true, true}, {18304, false, false},
		{18305, false, true}, {18305, true, false},
		{18306, true, false}, {18307, false, false},
	}
	for _, c := range cases {
		if err := m3dxUDPAnswer(scanned, c.port, c.answers); (err == nil) != c.ok {
			t.Errorf("m3dxUDPAnswer(%d, answers %v) = %v, want ok %v", c.port, c.answers, err, c.ok)
		}
	}
}
