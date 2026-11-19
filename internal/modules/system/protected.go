package system

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/darkraise/darkpanel/internal/agent/runtime"
	"github.com/darkraise/darkpanel/internal/core"
)

// dockerPackages are the packages of Docker's own repository the Docker
// panel installs, updates and removes.
var dockerPackages = []string{"docker-ce", "docker-ce-cli", "containerd.io", "docker-buildx-plugin", "docker-compose-plugin"}

// Protected is a package no package change may remove, and why.
type Protected struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// protectedNames are the protected packages by exact name (spec section 4
// "Protected packages"); the firewall's, the kernel's, the bootloader's and
// dpkg's own markings are decided in protectedReason.
var protectedNames = func() map[string]string {
	m := map[string]string{
		"apt":              "the package manager",
		"dpkg":             "the package manager",
		"systemd":          "the init system",
		"systemd-sysv":     "the init system",
		"dbus":             "the agent reaches systemd and logind over it",
		"openssh-server":   "remote access to the host",
		"sudo":             "provides sudo",
		"sudo-rs":          "provides sudo on Ubuntu 26.04",
		"ufw":              "a firewall front end",
		"tailscale":        "the tailnet connection",
		"netplan.io":       "the network stack",
		"systemd-resolved": "the network stack",
		"ifupdown":         "the network stack",
		"shim-signed":      "the bootloader",
	}
	for _, n := range dockerPackages {
		m[n] = "managed on the Docker panel"
	}
	return m
}()

// protectedRemovals returns the protected packages among removals, sorted
// by name. A name may carry apt's ":<arch>" suffix; the package it names is
// what is checked.
func (m module) protectedRemovals(ctx context.Context, env core.Env, removals []string) ([]Protected, error) {
	if len(removals) == 0 {
		return nil, nil
	}
	kernel, err := m.d.kernelRelease()
	if err != nil {
		return nil, err
	}
	firewall, err := firewallEnabled(ctx, env)
	if err != nil {
		return nil, err
	}
	pkgs, err := dpkgPackages(ctx, env)
	if err != nil {
		return nil, err
	}
	marked := map[string]string{}
	for _, p := range pkgs {
		switch {
		case p.Essential == "yes":
			marked[p.Name] = "dpkg marks it essential"
		case p.Priority == "required":
			marked[p.Name] = "its priority is required"
		}
	}
	var out []Protected
	seen := map[string]bool{}
	for _, name := range removals {
		if seen[name] {
			continue
		}
		seen[name] = true
		base, _, _ := strings.Cut(name, ":")
		if reason := protectedReason(base, kernel, firewall, marked); reason != "" {
			out = append(out, Protected{Name: name, Reason: reason})
		}
	}
	slices.SortFunc(out, func(a, b Protected) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// protectedReason is why the package name is protected, or "".
func protectedReason(name, kernel string, firewall bool, marked map[string]string) string {
	if r, ok := protectedNames[name]; ok {
		return r
	}
	switch {
	case strings.HasPrefix(name, "grub-"):
		return "the bootloader"
	case kernel != "" && (name == "linux-image-"+kernel || name == "linux-modules-"+kernel):
		return "the running kernel"
	case firewall && (name == "iptables" || name == "nftables"):
		return "the firewall module uses it"
	}
	return marked[name]
}

// firewallEnabled reads the firewall module's enabled flag as the
// controller last pushed it. With no flag the module counts as enabled: it
// is enabled by default, and an unknown flag must not lift a protection.
func firewallEnabled(ctx context.Context, env core.Env) (bool, error) {
	var v string
	err := env.DB.Read().QueryRowContext(ctx, "SELECT value FROM core_meta WHERE key = ?",
		runtime.MetaModuleEnabledPrefix+"firewall").Scan(&v)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return true, nil
	case err != nil:
		return false, fmt.Errorf("system: read the firewall module's enabled flag: %w", err)
	}
	return v == "1", nil
}
