//go:build harness

package harness

import (
	"fmt"
	"net"
	"net/netip"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestLabSlug(t *testing.T) {
	hostname := regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	cases := []struct{ name, prefix string }{
		{"TestM3", "testm3-"},
		{"TestM3/refuse_ssh_lockout", "testm3-refuse-ssh-lockou-"},
		{"TestM4/Compose project with a Git include", "testm4-compose-project-w-"},
		{"Test__Odd//Name__", "test-odd-name-"},
		{"/", "t-"},
	}
	seen := map[string]string{}
	for _, c := range cases {
		s := labSlug(c.name)
		if !strings.HasPrefix(s, c.prefix) || len(s) != len(c.prefix)+6 {
			t.Errorf("labSlug(%q) = %q, want %q and six hex digits", c.name, s, c.prefix)
		}
		if !hostname.MatchString(s) || len(s) > labSlugMax+7 {
			t.Errorf("labSlug(%q) = %q is not a short hostname label", c.name, s)
		}
		if s != labSlug(c.name) {
			t.Errorf("labSlug(%q) is not deterministic", c.name)
		}
		if other, dup := seen[s]; dup {
			t.Errorf("labSlug gives %q to both %q and %q", s, other, c.name)
		}
		seen[s] = c.name
	}
	if a, b := labSlug("TestM5/a b"), labSlug("TestM5/a-b"); a == b {
		t.Errorf("names that read alike share the slug %q", a)
	}
}

func TestLabAllocator(t *testing.T) {
	a := newLabAllocator(labPortMin)
	dir := t.TempDir()
	first, err := a.lab("TestM3", dir)
	if err != nil {
		t.Fatal(err)
	}
	again, err := a.lab("TestM3", dir)
	if err != nil || again.Index != first.Index || again.McastPort != first.McastPort {
		t.Errorf("a second lab of one name = %+v, %v; want index %d again", again, err, first.Index)
	}
	if want := filepath.Join(dir, first.Slug); first.Dir != want {
		t.Errorf("Dir = %q, want %q", first.Dir, want)
	}
	if first.PrivMcastPort != first.McastPort+1 || first.McastPort != labPortMin+2*first.Index {
		t.Errorf("ports %d and %d, want %d and the next", first.McastPort, first.PrivMcastPort, labPortMin+2*first.Index)
	}
	indexes := map[int]bool{first.Index: true}
	ports := map[int]bool{first.McastPort: true, first.PrivMcastPort: true}
	for i := 1; i < labIndexes; i++ {
		l, err := a.lab(fmt.Sprintf("TestLab/%d", i), dir)
		if err != nil {
			t.Fatalf("lab %d: %v", i, err)
		}
		if l.Index < 1 || l.Index > labIndexes || indexes[l.Index] {
			t.Fatalf("lab %d got index %d, used or out of range", i, l.Index)
		}
		indexes[l.Index] = true
		if ports[l.McastPort] || ports[l.PrivMcastPort] || l.PrivMcastPort > 65535 {
			t.Fatalf("lab %d got ports %d and %d, used or out of range", i, l.McastPort, l.PrivMcastPort)
		}
		ports[l.McastPort], ports[l.PrivMcastPort] = true, true
	}
	if _, err := a.lab("TestLab/one-too-many", dir); err == nil {
		t.Error("a lab past the last index was handed out")
	}
	if b := newLabAllocator(labPortMin); mustIndex(t, b, "TestM3") != first.Index {
		t.Error("a name's first index depends on more than the name")
	}
	if labPortMin <= 40001 || labPortMin+labPortSpan+2*labIndexes+1 > 65535 {
		t.Errorf("lab ports [%d, %d] overlap TestM1's and TestM2's or leave the port range", labPortMin, labPortMin+labPortSpan+2*labIndexes+1)
	}
}

func mustIndex(t *testing.T, a *labAllocator, name string) int {
	t.Helper()
	i, err := a.index(name)
	if err != nil {
		t.Fatal(err)
	}
	return i
}

func TestLabAddresses(t *testing.T) {
	inet4 := netip.MustParsePrefix("198.18.0.0/15")
	inet6 := netip.MustParsePrefix("2001:db8:d0::/48")
	priv := netip.MustParsePrefix("10.77.0.0/16")
	m2priv := netip.MustParsePrefix("10.77.0.0/24")
	for _, index := range []int{1, 16, 254} {
		l := &Lab{Slug: "testm3-abcdef", Index: index}
		macs := map[string]bool{}
		for _, nic := range []NIC{NICInet, NICMgmt, NICPriv} {
			for _, host := range []int{1, 10, 254} {
				m := l.MAC(nic, host)
				hw, err := net.ParseMAC(m)
				if err != nil || !strings.HasPrefix(m, "52:54:00:") || hw[0]&1 != 0 || macs[m] {
					t.Errorf("index %d: MAC(%d, %d) = %q is invalid, multicast or repeated", index, nic, host, m)
				}
				macs[m] = true
			}
		}
		a4, err := netip.ParseAddr(l.InetIPv4(10))
		if err != nil || !inet4.Contains(a4) {
			t.Errorf("index %d: InetIPv4 = %q, want an address in %s", index, l.InetIPv4(10), inet4)
		}
		a6, err := netip.ParseAddr(l.InetIPv6(10))
		if err != nil || !inet6.Contains(a6) || netip.PrefixFrom(a6, 64).Masked() == netip.MustParsePrefix("2001:db8:d0::/64") {
			t.Errorf("index %d: InetIPv6 = %q, want an address in %s outside M1's /64", index, l.InetIPv6(10), inet6)
		}
		if got := l.InetAddrs(10); len(got) != 2 || got[0] != l.InetIPv4(10)+"/15" || got[1] != l.InetIPv6(10)+"/64" {
			t.Errorf("index %d: InetAddrs = %v", index, got)
		}
		p := l.PrivPrefix()
		if !priv.Contains(p.Addr()) || p.Bits() != 24 || p.Overlaps(m2priv) {
			t.Errorf("index %d: PrivPrefix = %s, want a /24 in %s apart from TestM2's %s", index, p, priv, m2priv)
		}
		if a, err := netip.ParseAddr(l.PrivAddr(11)); err != nil || !p.Contains(a) {
			t.Errorf("index %d: PrivAddr = %q, want an address in %s", index, l.PrivAddr(11), p)
		}
		if got := l.VMName("scanner"); got != "testm3-abcdef-scanner" {
			t.Errorf("VMName = %q", got)
		}
	}
	for _, bad := range []func(l *Lab){
		func(l *Lab) { l.MAC(NICInet, 0) },
		func(l *Lab) { l.PrivAddr(255) },
		func(l *Lab) { l.VMName("Target") },
		func(l *Lab) { l.VMName("a--b") },
		func(l *Lab) { l.VMName(strings.Repeat("a", labRoleMax+1)) },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Error("an invalid host number or role did not panic")
				}
			}()
			bad(&Lab{Slug: "t-000000", Index: 1})
		}()
	}
}
