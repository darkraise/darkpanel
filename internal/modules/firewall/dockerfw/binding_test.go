package dockerfw

import (
	"errors"
	"net/netip"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

func bdBinding(edit ...func(*Binding)) Binding {
	b := Binding{Selector: "web", HostPort: 8080, Proto: "tcp"}
	for _, e := range edit {
		e(&b)
	}
	return b
}

// bdFields returns the field errors of err, which must be a
// request.invalid_input refusal with a message on every field.
func bdFields(t *testing.T, err error) []errcode.FieldError {
	t.Helper()
	var e *errcode.Error
	if !errors.As(err, &e) || e.Code != errcode.InvalidInput {
		t.Fatalf("error = %v, want %s", err, errcode.InvalidInput)
	}
	for _, f := range e.Fields {
		if f.Message == "" {
			t.Errorf("field %s has no message", f.Path)
		}
	}
	return e.Fields
}

// bdPaths is the field paths of bdFields.
func bdPaths(t *testing.T, err error) []string {
	t.Helper()
	var paths []string
	for _, f := range bdFields(t, err) {
		paths = append(paths, f.Path)
	}
	return paths
}

func TestBdBindingID(t *testing.T) {
	cases := []struct {
		name string
		b    Binding
		id   string
	}{
		{"all addresses", bdBinding(), "76d26265fc2a"},
		{"a compose service on one IPv4 address", Binding{Selector: "myproj/web", HostAddr: netip.MustParseAddr("192.0.2.10"), HostPort: 8080, Proto: "tcp"}, "977930f28594"},
		{"UDP on an IPv6 address", Binding{Selector: "web", HostAddr: netip.MustParseAddr("::1"), HostPort: 53, Proto: "udp"}, "17a68bb7b4fc"},
	}
	for _, c := range cases {
		if got := c.b.ID(); got != c.id {
			t.Errorf("%s: ID() = %s, want %s", c.name, got, c.id)
		}
		if got, want := c.b.Chain(), "dp-b-"+c.id; got != want {
			t.Errorf("%s: Chain() = %s, want %s", c.name, got, want)
		}
	}
	if n := len(bdBinding().Chain()); n > 28 {
		t.Errorf("a chain name of %d characters exceeds iptables' limit of 28", n)
	}
	base := bdBinding().ID()
	for name, edit := range map[string]func(*Binding){
		"selector": func(b *Binding) { b.Selector = "web2" },
		"address":  func(b *Binding) { b.HostAddr = netip.MustParseAddr("192.0.2.10") },
		"port":     func(b *Binding) { b.HostPort = 8081 },
		"protocol": func(b *Binding) { b.Proto = "udp" },
	} {
		if got := bdBinding(edit).ID(); got == base {
			t.Errorf("changing the %s keeps the id %s", name, got)
		}
	}
}

func TestBdBindingValidate(t *testing.T) {
	long := func(n int) string { return "a" + strings.Repeat("b", n-1) }
	cases := []struct {
		name  string
		edit  func(*Binding)
		paths []string
	}{
		{"a container name", func(*Binding) {}, nil},
		{"a compose service", func(b *Binding) { b.Selector = "my-proj_1/Web.api" }, nil},
		{"a name of 128 characters", func(b *Binding) { b.Selector = long(128) }, nil},
		{"an IPv4 host address", func(b *Binding) { b.HostAddr = netip.MustParseAddr("192.0.2.10") }, nil},
		{"an IPv6 host address", func(b *Binding) { b.HostAddr = netip.MustParseAddr("2001:db8::1") }, nil},
		{"the unspecified address is a rule's concern", func(b *Binding) { b.HostAddr = netip.MustParseAddr("0.0.0.0") }, nil},
		{"port 65535 over UDP", func(b *Binding) { b.HostPort, b.Proto = 65535, "udp" }, nil},
		{"no selector", func(b *Binding) { b.Selector = "" }, []string{"/selector"}},
		{"a name of 129 characters", func(b *Binding) { b.Selector = long(129) }, []string{"/selector"}},
		{"a name that starts with a dash", func(b *Binding) { b.Selector = "-web" }, []string{"/selector"}},
		{"a name with a space", func(b *Binding) { b.Selector = "my web" }, []string{"/selector"}},
		{"Docker's leading slash", func(b *Binding) { b.Selector = "/web" }, []string{"/selector"}},
		{"two slashes", func(b *Binding) { b.Selector = "a/b/c" }, []string{"/selector"}},
		{"a service without a name", func(b *Binding) { b.Selector = "shop/" }, []string{"/selector"}},
		{"an upper-case compose project", func(b *Binding) { b.Selector = "Shop/web" }, []string{"/selector"}},
		{"a host address with a zone", func(b *Binding) { b.HostAddr = netip.MustParseAddr("fe80::1%eth0") }, []string{"/hostAddr"}},
		{"an IPv4-mapped host address", func(b *Binding) { b.HostAddr = netip.MustParseAddr("::ffff:192.0.2.1") }, []string{"/hostAddr"}},
		{"port 0", func(b *Binding) { b.HostPort = 0 }, []string{"/hostPort"}},
		{"no protocol", func(b *Binding) { b.Proto = "" }, []string{"/proto"}},
		{"icmp", func(b *Binding) { b.Proto = "icmp" }, []string{"/proto"}},
		{"upper-case TCP", func(b *Binding) { b.Proto = "TCP" }, []string{"/proto"}},
		{"every field", func(b *Binding) {
			b.Selector, b.HostAddr, b.HostPort, b.Proto = "", netip.MustParseAddr("fe80::1%eth0"), 0, "sctp"
		}, []string{"/selector", "/hostAddr", "/hostPort", "/proto"}},
	}
	for _, c := range cases {
		err := bdBinding(c.edit).Validate()
		if c.paths == nil {
			if err != nil {
				t.Errorf("%s: Validate() = %v, want nil", c.name, err)
			}
			continue
		}
		if got := bdPaths(t, err); strings.Join(got, " ") != strings.Join(c.paths, " ") {
			t.Errorf("%s: field paths = %q, want %q", c.name, got, c.paths)
		}
	}
}

func TestBdBindingSelects(t *testing.T) {
	cases := []struct {
		name                    string
		selector                string
		cname, project, service string
		want                    bool
	}{
		{"the container name", "web", "web", "", "", true},
		{"the container name with Docker's leading slash", "web", "/web", "", "", true},
		{"a longer name", "web", "web2", "", "", false},
		{"a name is case sensitive", "Web", "web", "", "", false},
		{"a name selector ignores the compose labels", "api", "shop-api-1", "shop", "api", false},
		{"a compose service", "shop/api", "shop-api-1", "shop", "api", true},
		{"another project", "shop/api", "other-api-1", "other", "api", false},
		{"another service", "shop/api", "shop-worker-1", "shop", "worker", false},
		{"a container without compose labels", "shop/api", "api", "", "", false},
		{"a service selector ignores the container name", "shop/api", "shop/api", "", "", false},
	}
	for _, c := range cases {
		b := bdBinding(func(b *Binding) { b.Selector = c.selector })
		if got := b.Selects(c.cname, c.project, c.service); got != c.want {
			t.Errorf("%s: Selects(%q, %q, %q) = %t, want %t", c.name, c.cname, c.project, c.service, got, c.want)
		}
	}
}
