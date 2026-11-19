package system

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// sudoProvider2604 is the package providing sudo on Ubuntu 26.04, as
// testdata/ubuntu-26.04/sudo-provider.txt shows.
const sudoProvider2604 = "sudo-rs"

func TestValidateNames(t *testing.T) {
	h := newSysHost(t)
	h.run("/usr/bin/apt-cache pkgnames", "jq\ncurl\nlibjq1\n", 0)
	h.installed("curl", "openssh-server")
	ctx := context.Background()
	for _, c := range []struct {
		names []string
		kind  nameKind
		ok    bool
	}{
		{[]string{"jq"}, nameAvailable, true},
		{[]string{"jq-"}, nameAvailable, false},
		{[]string{"jq+"}, nameInstalled, false},
		{[]string{"j.q"}, nameAvailable, false},
		{[]string{"-o"}, nameAvailable, false},
		{[]string{"JQ"}, nameAvailable, false},
		{[]string{"nosuch"}, nameAvailable, false},
		{[]string{"curl"}, nameInstalled, true},
		{[]string{"jq"}, nameInstalled, false},
		// Additions to the plan's cases.
		{[]string{"jq", "curl", "libjq1"}, nameAvailable, true},
		{[]string{"jq", "nosuch"}, nameAvailable, false},
		{[]string{"curl:amd64"}, nameInstalled, false},
		{[]string{"j"}, nameAvailable, false},
		{[]string{"curl\n"}, nameInstalled, false},
		{[]string{"a" + strings.Repeat("b", 64)}, nameAvailable, false},
	} {
		err := validateNames(ctx, h.env(), c.names, c.kind)
		if (err == nil) != c.ok {
			t.Errorf("%v kind %d: err = %v", c.names, c.kind, err)
		}
		if err != nil && errcode.From(err).Code != "system.name_invalid" {
			t.Errorf("%v: code %s", c.names, errcode.From(err).Code)
		}
	}
	if got := h.calls(); slices.ContainsFunc(got, func(v string) bool { return strings.HasPrefix(v, "/usr/bin/apt-get") }) {
		t.Fatalf("apt-get ran during validation: %v", got)
	}
}

func TestValidateNamesNamesTheFirstBadName(t *testing.T) {
	h := newSysHost(t)
	h.run("/usr/bin/apt-cache pkgnames", "jq\n", 0)
	err := validateNames(context.Background(), h.env(), []string{"jq", "nosuch", "other"}, nameAvailable)
	if err == nil || !strings.Contains(errcode.From(err).Detail, "nosuch") || strings.Contains(errcode.From(err).Detail, "other") {
		t.Fatalf("err = %v; want a refusal naming nosuch only", err)
	}
}

func TestValidateNamesRunsNothingForABadPattern(t *testing.T) {
	h := newSysHost(t)
	if err := validateNames(context.Background(), h.env(), []string{"-o"}, nameAvailable); err == nil {
		t.Fatal("-o was accepted")
	}
	if got := h.calls(); len(got) != 0 {
		t.Fatalf("a bad pattern ran %v", got)
	}
}

func TestProtectedRemovals(t *testing.T) {
	h := newSysHost(t)
	h.unameR("6.8.0-45-generic")
	h.firewallEnabled(true)
	h.dpkgFields(map[string]string{"bash": "Essential: yes", "coreutils": "Priority: required"})
	ctx := context.Background()
	in := []string{"jq", "apt", "dpkg", "systemd", "systemd-sysv", "docker-ce-cli", "containerd.io", "docker-buildx-plugin", "docker-compose-plugin", "bash", "coreutils", "dbus", "netplan.io", "grub-efi-amd64", "shim-signed", "iptables", "nftables", "sudo", "openssh-server", "docker-ce", "linux-image-6.8.0-45-generic", "linux-modules-6.8.0-45-generic", "linux-image-6.8.0-40-generic", "systemd-resolved", "ifupdown", "tailscale", "ufw"}
	got, err := h.mod().protectedRemovals(ctx, h.env(), in)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range got {
		names = append(names, p.Name)
		if p.Reason == "" {
			t.Errorf("%s has no reason", p.Name)
		}
	}
	want := []string{"apt", "bash", "containerd.io", "coreutils", "dbus", "docker-buildx-plugin", "docker-ce", "docker-ce-cli", "docker-compose-plugin", "dpkg", "grub-efi-amd64", "ifupdown", "iptables", "linux-image-6.8.0-45-generic", "linux-modules-6.8.0-45-generic", "netplan.io", "nftables", "openssh-server", "shim-signed", "sudo", "systemd", "systemd-resolved", "systemd-sysv", "tailscale", "ufw"}
	if !slices.Equal(names, want) {
		t.Fatalf("protected = %v\nwant %v", names, want)
	}
	h26 := newSysHost(t)
	h26.osRelease("ubuntu", "resolute", "amd64")
	if got, _ := h26.mod().protectedRemovals(ctx, h26.env(), []string{sudoProvider2604}); len(got) != 1 {
		t.Fatalf("26.04 sudo provider %s not protected", sudoProvider2604)
	}
	h.firewallEnabled(false)
	got, _ = h.mod().protectedRemovals(ctx, h.env(), []string{"iptables", "nftables"})
	if len(got) != 0 {
		t.Fatalf("iptables/nftables protected with the firewall off: %v", got)
	}
}

// Additions to the plan's cases.

func TestProtectedRemovalsSeesThroughAnArchitectureSuffix(t *testing.T) {
	h := newSysHost(t)
	h.dpkgFields(map[string]string{"libc6": "Priority: required"})
	got, err := h.mod().protectedRemovals(context.Background(), h.env(), []string{"dpkg:amd64", "libc6:i386", "jq:arm64"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "dpkg:amd64" || got[1].Name != "libc6:i386" {
		t.Fatalf("protected = %v; want dpkg:amd64 and libc6:i386", got)
	}
}

func TestProtectedRemovalsTreatsAnUnknownFirewallFlagAsEnabled(t *testing.T) {
	h := newSysHost(t) // no module_enabled.firewall row: the firewall module is enabled by default
	got, err := h.mod().protectedRemovals(context.Background(), h.env(), []string{"iptables"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("iptables not protected without a firewall flag: %v", got)
	}
}

func TestProtectedRemovalsNeedsTheExactKernel(t *testing.T) {
	h := newSysHost(t)
	h.unameR("6.8.0-45-generic")
	got, err := h.mod().protectedRemovals(context.Background(), h.env(), []string{"linux-image-6.8.0-45", "linux-image-6.8.0-45-generic-extra", "linux-headers-6.8.0-45-generic", "grub2-common"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("protected = %v; want none", got)
	}
}
