//go:build harness

package harness

import (
	"strings"
	"testing"
)

func TestM3xTrafficExpect(t *testing.T) {
	scan := []PortState{
		{Port: 18201, Proto: "tcp", State: "open"},
		{Port: 18202, Proto: "tcp", State: "filtered"},
		{Port: 18203, Proto: "tcp", State: "closed"},
		{Port: 18204, Proto: "udp", State: "open|filtered"},
	}
	for _, tc := range []struct {
		port  uint16
		proto string
		want  string
		ok    bool
	}{
		{18201, "tcp", "open", true},
		{18201, "tcp", "notopen", false},
		{18202, "tcp", "filtered", true},
		{18202, "tcp", "notopen", true},
		{18202, "tcp", "open", false},
		{18203, "tcp", "closed", true},
		{18203, "tcp", "notopen", true},
		{18203, "tcp", "filtered", false},
		{18204, "tcp", "notopen", false}, // the port was scanned as UDP: the TCP entry is missing
		{18205, "tcp", "open", false},    // never scanned
	} {
		err := m3dxTrafficExpect(scan, tc.proto, tc.port, tc.want)
		if (err == nil) != tc.ok {
			t.Errorf("m3dxTrafficExpect(%s/%d, %q) = %v, want ok=%t", tc.proto, tc.port, tc.want, err, tc.ok)
		}
	}
	if err := m3dxTrafficExpect(scan, "tcp", 18201, "reachable"); err == nil || !strings.Contains(err.Error(), "reachable") {
		t.Errorf("an unknown expectation: %v, want an error naming it", err)
	}
	if err := m3dxTrafficExpect(scan, "tcp", 18202, "open"); err == nil || !strings.Contains(err.Error(), "filtered") {
		t.Errorf("a mismatch: %v, want an error naming the scanned state", err)
	}
}

func TestM3xTrafficExpectAll(t *testing.T) {
	scan := []PortState{{Port: 18203, Proto: "tcp", State: "open"}, {Port: 18204, Proto: "tcp", State: "filtered"}}
	if err := m3dxTrafficExpectAll(scan, map[uint16]string{18203: "open", 18204: "filtered"}); err != nil {
		t.Errorf("matching scan: %v", err)
	}
	err := m3dxTrafficExpectAll(scan, map[uint16]string{18203: "filtered", 18204: "open"})
	if err == nil || !strings.Contains(err.Error(), "18203") || !strings.Contains(err.Error(), "18204") {
		t.Errorf("two mismatches: %v, want one error naming both ports", err)
	}
}

func TestM3xTrafficLeaks(t *testing.T) {
	var quiet strings.Builder
	for range 30 {
		quiet.WriteString("18209 fail\n18210 fail\n")
	}
	for _, tc := range []struct {
		name      string
		log       string
		unallowed []uint16
		min       int
		want      string // "" accepts
	}{
		{"nothing connected", quiet.String(), []uint16{18209, 18210}, 20, ""},
		{"a leak", quiet.String() + "18209 ok\n", []uint16{18209, 18210}, 20, "18209"},
		{"allowed port may answer", quiet.String() + "18211 ok\n", []uint16{18209, 18210}, 20, ""},
		{"too few attempts", "18209 fail\n", []uint16{18209}, 20, "1 attempts"},
		{"a port never probed", quiet.String(), []uint16{18209, 18299}, 20, "18299"},
		{"empty log", "", []uint16{18209}, 1, "18209"},
		{"malformed line", "18209\n", []uint16{18209}, 1, "malformed"},
		{"unknown result", "18209 maybe\n", []uint16{18209}, 1, "malformed"},
	} {
		err := m3dxTrafficLeaks(tc.log, tc.unallowed, tc.min)
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: %v, want nil", tc.name, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%s: %v, want an error naming %q", tc.name, err, tc.want)
		}
	}
}

func TestM3xTrafficProbed(t *testing.T) {
	var log strings.Builder
	for range 19 {
		log.WriteString("18209 fail\n18210 fail\n")
	}
	if err := m3dxTrafficProbed(log.String(), []uint16{18209, 18210}, 20); err == nil || !strings.Contains(err.Error(), "19 attempts") {
		t.Errorf("19 attempts each: %v, want an error naming the 19 attempts", err)
	}
	log.WriteString("18209 fail\n")
	if err := m3dxTrafficProbed(log.String(), []uint16{18209, 18210}, 20); err == nil || !strings.Contains(err.Error(), "18210") {
		t.Errorf("one port still short: %v, want an error naming 18210", err)
	}
	log.WriteString("18210 ok\n")
	if err := m3dxTrafficProbed(log.String(), []uint16{18209, 18210}, 20); err != nil {
		t.Errorf("20 attempts each, one connected: %v, want nil; a leak is m3dxTrafficLeaks's to report", err)
	}
	if err := m3dxTrafficProbed("18209\n", []uint16{18209}, 1); err == nil || !strings.Contains(err.Error(), "malformed") {
		t.Errorf("malformed line: %v, want an error naming it", err)
	}
}

func TestM3xTrafficConnect(t *testing.T) {
	got, err := m3dxTrafficConnect(4242, "203.0.113.2", 14000)
	if want := "sudo nsenter -t 4242 -n -- timeout 5 bash -c '</dev/tcp/203.0.113.2/14000'"; err != nil || got != want {
		t.Errorf("m3dxTrafficConnect(IPv4) = %q, %v, want %q", got, err, want)
	}
	got, err = m3dxTrafficConnect(4242, "2001:db8:dd:1::2", 14000)
	if want := "sudo nsenter -t 4242 -n -- timeout 5 bash -c '</dev/tcp/2001:db8:dd:1::2/14000'"; err != nil || got != want {
		t.Errorf("m3dxTrafficConnect(IPv6) = %q, %v, want %q", got, err, want)
	}
	for _, tc := range []struct {
		pid  int
		addr string
		port uint16
	}{
		{0, "203.0.113.2", 14000},
		{4242, "web; reboot", 14000},
		{4242, "fe80::1%eth0", 14000},
		{4242, "", 14000},
		{4242, "203.0.113.2", 0},
	} {
		if got, err := m3dxTrafficConnect(tc.pid, tc.addr, tc.port); err == nil {
			t.Errorf("m3dxTrafficConnect(%d, %q, %d) = %q, want an error", tc.pid, tc.addr, tc.port, got)
		}
	}
}

func TestM3xTrafficRuleID(t *testing.T) {
	st := m3dDockStatus{Rules: []m3dDockRule{
		{ID: "01J00000000000000000000001", Binding: m3dDockBinding{Selector: "m3d-t1", HostPort: 18201, Proto: "tcp"}, Kind: "allow"},
		{ID: "01J00000000000000000000002", Binding: m3dDockBinding{Selector: "m3d-k1", HostPort: 18206, Proto: "tcp"}, Kind: "allow"},
		{ID: "01J00000000000000000000003", Binding: m3dDockBinding{Selector: "m3d-k1", HostPort: 18206, Proto: "tcp"}, Kind: "deny", From: "198.18.0.20/32"},
	}}
	for _, tc := range []struct {
		selector string
		port     uint16
		kind     string
		from     string
		want     string
	}{
		{"m3d-t1", 18201, "allow", "", "01J00000000000000000000001"},
		{"m3d-k1", 18206, "allow", "", "01J00000000000000000000002"},
		{"m3d-k1", 18206, "deny", "198.18.0.20/32", "01J00000000000000000000003"},
		{"m3d-k1", 18206, "deny", "", ""},
		{"m3d-k1", 18207, "allow", "", ""},
		{"nonesuch", 18201, "allow", "", ""},
	} {
		got, err := m3dxTrafficRuleID(st, tc.selector, tc.port, tc.kind, tc.from)
		if tc.want == "" && err == nil || tc.want != "" && (err != nil || got != tc.want) {
			t.Errorf("m3dxTrafficRuleID(%s, %d, %s, %q) = %q, %v, want %q", tc.selector, tc.port, tc.kind, tc.from, got, err, tc.want)
		}
	}
}
