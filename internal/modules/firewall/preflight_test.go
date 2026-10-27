package firewall

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

// pfRunner answers "<tool> --version" from versions, keyed by the tool's
// path; any other program fails.
type pfRunner struct{ versions map[string]string }

func (r pfRunner) Run(_ context.Context, cmd core.Cmd) (core.CmdResult, error) {
	v, ok := r.versions[cmd.Path]
	if !ok || len(cmd.Args) != 1 || cmd.Args[0] != "--version" {
		return core.CmdResult{}, fmt.Errorf("pf: run %s %v: %w", cmd.Path, cmd.Args, fs.ErrNotExist)
	}
	return core.CmdResult{Stdout: []byte(v + "\n")}, nil
}

const pfStatus = "Package: iptables\nStatus: install ok installed\nVersion: 1.8.10-3ubuntu2\n\n" +
	"Package: ufw\nStatus: install ok installed\nPriority: optional\nDescription: program for managing a Netfilter firewall\n" +
	" UFW is a front end.\nVersion: %s\n\nPackage: zsh\nStatus: install ok installed\nVersion: 5.9-6\n"

// pfHost writes a host whose UFW and nft tools of both families are
// present, with ufw version, and returns its root.
func pfHost(t *testing.T, version string) string {
	t.Helper()
	root := t.TempDir()
	for _, p := range []string{"/usr/sbin/ufw", "/usr/sbin/iptables", "/usr/sbin/ip6tables",
		"/usr/sbin/iptables-nft-save", "/usr/sbin/iptables-nft-restore",
		"/usr/sbin/ip6tables-nft-save", "/usr/sbin/ip6tables-nft-restore"} {
		pfWrite(t, root, p, "#!/bin/sh\n")
	}
	pfWrite(t, root, "/var/lib/dpkg/status", fmt.Sprintf(pfStatus, version))
	return root
}

func pfWrite(t *testing.T, root, p, data string) {
	t.Helper()
	full := filepath.Join(root, p)
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

var pfNFT = map[string]string{"/usr/sbin/iptables": "iptables v1.8.10 (nf_tables)", "/usr/sbin/ip6tables": "ip6tables v1.8.10 (nf_tables)"}

// pfRun runs every preflight check of a fw rooted at root in order and
// returns the first failure's id and reason, as the agent reports it.
func pfRun(t *testing.T, root string, versions map[string]string) (string, error) {
	t.Helper()
	f := fw{d: deps{root: root}}
	env := core.Env{Exec: pfRunner{versions: versions}}
	checks := f.preflight()
	if len(checks) != 3 {
		t.Fatalf("%d preflight checks, want 3", len(checks))
	}
	for _, c := range checks {
		if err := c.Run(context.Background(), env); err != nil {
			return c.ID, err
		}
	}
	return "", nil
}

// TestPreflightPasses: UFW 0.35 or later, in any version spelling dpkg
// records, with the save and restore tools of the variant each family
// uses, passes; ip6tables is optional.
func TestPreflightPasses(t *testing.T) {
	for _, version := range []string{"0.36.2-6", "0.36.1-4ubuntu0.1", "1:0.36.2-9", "0.35", "0.40"} {
		if id, err := pfRun(t, pfHost(t, version), pfNFT); err != nil {
			t.Errorf("version %s: check %s: %v", version, id, err)
		}
	}
	root := pfHost(t, "0.36.2-1")
	for _, p := range []string{"/usr/sbin/ip6tables", "/usr/sbin/ip6tables-nft-save", "/usr/sbin/ip6tables-nft-restore"} {
		if err := os.Remove(filepath.Join(root, p)); err != nil {
			t.Fatal(err)
		}
	}
	if id, err := pfRun(t, root, map[string]string{"/usr/sbin/iptables": "iptables v1.8.10 (nf_tables)"}); err != nil {
		t.Errorf("without ip6tables: check %s: %v", id, err)
	}
	root = pfHost(t, "0.36.2-1")
	pfWrite(t, root, "/usr/sbin/iptables-legacy-save", "")
	pfWrite(t, root, "/usr/sbin/iptables-legacy-restore", "")
	versions := map[string]string{"/usr/sbin/iptables": "iptables v1.8.9 (legacy)", "/usr/sbin/ip6tables": "ip6tables v1.8.9 (nf_tables)"}
	if id, err := pfRun(t, root, versions); err != nil {
		t.Errorf("legacy IPv4: check %s: %v", id, err)
	}
}

// TestPreflightRefuses: each missing prerequisite fails its check with a
// reason that names it.
func TestPreflightRefuses(t *testing.T) {
	cases := []struct {
		name     string
		edit     func(t *testing.T, root string)
		versions map[string]string
		id, want string
	}{
		{"no ufw", func(t *testing.T, root string) { pfRemove(t, root, "/usr/sbin/ufw") }, pfNFT,
			"ufw", "/usr/sbin/ufw is missing"},
		{"ufw a directory", func(t *testing.T, root string) {
			pfRemove(t, root, "/usr/sbin/ufw")
			if err := os.Mkdir(filepath.Join(root, "/usr/sbin/ufw"), 0o750); err != nil {
				t.Fatal(err)
			}
		}, pfNFT, "ufw", "/usr/sbin/ufw is not a regular file"},
		{"no dpkg status", func(t *testing.T, root string) { pfRemove(t, root, "/var/lib/dpkg/status") }, pfNFT,
			"ufw-version", "/var/lib/dpkg/status"},
		{"ufw not installed", func(t *testing.T, root string) {
			pfWrite(t, root, "/var/lib/dpkg/status", "Package: ufw\nStatus: deinstall ok config-files\nVersion: 0.36.2-6\n")
		}, pfNFT, "ufw-version", "the ufw package is not installed"},
		{"ufw package unknown", func(t *testing.T, root string) {
			pfWrite(t, root, "/var/lib/dpkg/status", "Package: ufw-extras\nStatus: install ok installed\nVersion: 1.0\n")
		}, pfNFT, "ufw-version", "the ufw package is not installed"},
		{"ufw too old", func(t *testing.T, root string) {
			pfWrite(t, root, "/var/lib/dpkg/status", fmt.Sprintf(pfStatus, "0.34.1-2"))
		}, pfNFT, "ufw-version", "UFW 0.34.1-2 is older than 0.35"},
		{"ufw version unreadable", func(t *testing.T, root string) {
			pfWrite(t, root, "/var/lib/dpkg/status", fmt.Sprintf(pfStatus, "latest"))
		}, pfNFT, "ufw-version", `cannot read UFW's version "latest"`},
		{"no iptables", func(t *testing.T, root string) { pfRemove(t, root, "/usr/sbin/iptables") }, nil,
			"iptables-tools", "/usr/sbin/iptables is missing"},
		{"unknown variant", nil, map[string]string{"/usr/sbin/iptables": "iptables v1.8.10", "/usr/sbin/ip6tables": "ip6tables v1.8.10 (nf_tables)"},
			"iptables-tools", `cannot tell the variant of /usr/sbin/iptables from "iptables v1.8.10"`},
		{"no restore tool", func(t *testing.T, root string) { pfRemove(t, root, "/usr/sbin/iptables-nft-restore") }, pfNFT,
			"iptables-tools", "/usr/sbin/iptables-nft-restore is missing"},
		{"no IPv6 save tool", func(t *testing.T, root string) { pfRemove(t, root, "/usr/sbin/ip6tables-nft-save") }, pfNFT,
			"iptables-tools", "/usr/sbin/ip6tables-nft-save is missing"},
		{"legacy tools missing", nil, map[string]string{"/usr/sbin/iptables": "iptables v1.8.9 (legacy)", "/usr/sbin/ip6tables": "ip6tables v1.8.9 (legacy)"},
			"iptables-tools", "/usr/sbin/iptables-legacy-save is missing"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := pfHost(t, "0.36.2-6")
			if c.edit != nil {
				c.edit(t, root)
			}
			id, err := pfRun(t, root, c.versions)
			if id != c.id || err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("check %s: %v; want check %s failing with %q", id, err, c.id, c.want)
			}
		})
	}
}

func pfRemove(t *testing.T, root, p string) {
	t.Helper()
	if err := os.Remove(filepath.Join(root, p)); err != nil {
		t.Fatal(err)
	}
}

// TestUpstreamAtLeast compares the upstream part of Debian versions.
func TestUpstreamAtLeast(t *testing.T) {
	for _, c := range []struct {
		version string
		ok      bool
	}{
		{"0.35", true}, {"0.35.0", true}, {"0.36.1-4ubuntu0.1", true}, {"2:0.36", true}, {"1.0", true},
		{"0.34.9", false}, {"0.3.5", false}, {"0.34-1", false},
	} {
		ok, err := upstreamAtLeast(c.version, []int{0, 35})
		if err != nil || ok != c.ok {
			t.Errorf("upstreamAtLeast(%q) = %v, %v; want %v", c.version, ok, err, c.ok)
		}
	}
	for _, bad := range []string{"", "-1", "x.35", "0..35"} {
		if _, err := upstreamAtLeast(bad, []int{0, 35}); err == nil {
			t.Errorf("upstreamAtLeast(%q) is no error", bad)
		}
	}
}
