package conf

import "testing"

// fz7Head returns at most the first 64 bytes of s, for a failure message.
func fz7Head(s string) string { return s[:min(len(s), 64)] }

// FuzzConfiguredIPv6 feeds arbitrary contents of /etc/default/ufw and ufw.conf
// to ConfiguredIPv6 and an arbitrary name to SysctlKey. SysctlKey is
// idempotent and keeps the length of its input. A final IPV6=yes or IPV6=no
// line decides ConfiguredIPv6 whatever precedes it, in ufw.conf, which wins
// over /etc/default/ufw, and in /etc/default/ufw alone (R74).
func FuzzConfiguredIPv6(f *testing.F) {
	f.Add(confTestDefault, confTestUFWConf, "net.ipv4.conf.eth0/100.rp_filter")
	f.Add("IPV6=yes\n", "", "net/ipv4/ip_forward")
	f.Add("IPV6=\"yes\"\n", "IPV6=no\n", ".a/b.c")
	f.Add("IPV6='yes\n\tIPV6=maybe", "\n\n#IPV6=yes", "/")
	f.Add("", "", "")
	f.Fuzz(func(t *testing.T, defaults, ufwconf, name string) {
		once := SysctlKey(name)
		if twice := SysctlKey(once); twice != once || len(once) != len(name) {
			t.Fatalf("SysctlKey(%q) = %q, then %q", fz7Head(name), fz7Head(once), fz7Head(twice))
		}
		for _, c := range []struct {
			set  string
			want bool
		}{{"\nIPV6=yes\n", true}, {"\nIPV6=no\n", false}} {
			if got := ConfiguredIPv6([]byte(defaults), []byte(ufwconf+c.set)); got != c.want {
				t.Fatalf("ufw.conf ending in %q gives %v for defaults %q, ufw.conf %q", c.set, got, fz7Head(defaults), fz7Head(ufwconf))
			}
			if got := ConfiguredIPv6([]byte(defaults+c.set), nil); got != c.want {
				t.Fatalf("/etc/default/ufw ending in %q gives %v for %q", c.set, got, fz7Head(defaults))
			}
		}
	})
}
