package loaded

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"slices"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

const (
	// nftModule exists while the kernel's nf_tables module is loaded.
	nftModule = "/sys/module/nf_tables"
	// nftMissing is the reason for a host that loads nf_tables without the
	// nft tool to read its tables with (spec 2.1).
	nftMissing = "install the nftables package so Darkpanel can read every rule"
)

// nftSafeName is a table name nft reads as a name: the exec allowlist's
// name pattern without a leading dash, which nft would read as an option.
var nftSafeName = regexp.MustCompile(`^[A-Za-z0-9_.][A-Za-z0-9_.-]{0,63}$`)

// nftFamilies are the families the exec allowlist lets nft list.
var nftFamilies = []string{"ip", "ip6", "inet", "arp", "bridge", "netdev"}

func nfTablesLoaded() bool {
	_, err := os.Stat(nftModule)
	return err == nil
}

func otherVariant(v string) string {
	if v == variantNFT {
		return variantLegacy
	}
	return variantNFT
}

// foreign returns the reasons foreign netfilter hooks make the host
// unsupported (spec 2.1): any rule, or a policy other than ACCEPT, in the
// other iptables variant's tables, and any native nftables base chain with
// a rule or a policy other than accept, except fail2ban's table in exactly
// the shape its actions create, which is returned as a neighbour (M11 spec 4).
//
// The legacy tables are read only when iptables-nft-save warned that some
// exist, since it reads the same kernel list, and then only the listed
// ones. Nothing nftables is read while nf_tables is not loaded: there is
// nothing to read, and asking would make the kernel load the module.
func foreign(ctx context.Context, x core.Runner, procNet string, nftLoaded bool, fams []famState) ([]string, []fwstate.Neighbour, error) {
	var reasons []string
	covered := map[nftTable]bool{}
	for _, s := range fams {
		other := otherVariant(s.variant)
		var tables []string
		switch {
		case other == variantLegacy && s.rs.LegacyPresent:
			t, err := legacyTables(procNet, s.f)
			if err != nil {
				return nil, nil, err
			}
			tables = t
		case other == variantNFT && nftLoaded:
			tables = netfilter.Tables()
		}
		if s.variant == variantNFT || other == variantNFT && nftLoaded {
			for _, t := range netfilter.Tables() {
				covered[nftTable{Family: s.f.nft, Name: t}] = true
			}
		}
		rs, err := saveTables(ctx, x, s.f.tool, other, tables)
		if err != nil {
			return nil, nil, err
		}
		reasons = append(reasons, foreignTables(s.f.tool+"-"+other, s.variant, rs)...)
	}
	if !nftLoaded {
		return reasons, nil, nil
	}
	native, neighbours, err := nativeTables(ctx, x, covered)
	if err != nil {
		return nil, nil, err
	}
	return append(reasons, native...), neighbours, nil
}

// foreignTables names every table, chain and policy of the other variant
// that decides something.
func foreignTables(tool, variant string, rs *netfilter.Ruleset) []string {
	var reasons []string
	for _, t := range rs.Tables {
		if t.Incompatible != "" {
			reasons = append(reasons, fmt.Sprintf("%s table %s: %s", tool, t.Name, t.Incompatible))
		}
		for _, c := range t.Chains {
			if len(c.Rules) > 0 {
				reasons = append(reasons, fmt.Sprintf("%s table %s chain %s holds %d rule(s) outside the %s variant UFW uses",
					tool, t.Name, c.Name, len(c.Rules), variant))
			}
			if c.Builtin && c.Policy != "ACCEPT" {
				reasons = append(reasons, fmt.Sprintf("%s table %s chain %s has policy %s outside the %s variant UFW uses",
					tool, t.Name, c.Name, c.Policy, variant))
			}
		}
	}
	return reasons
}

// nativeTables lists the nftables tables and reads each one that no save
// tool has read. A name nft could take for an option is never passed to it.
func nativeTables(ctx context.Context, x core.Runner, covered map[nftTable]bool) ([]string, []fwstate.Neighbour, error) {
	out, err := run(ctx, x, "/usr/sbin/nft", "list", "tables")
	if errors.Is(err, fs.ErrNotExist) {
		return []string{nftMissing}, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var neighbours []fwstate.Neighbour
	tables, bad := parseNFTTables(out)
	var reasons []string
	for _, line := range bad {
		reasons = append(reasons, fmt.Sprintf("nft list tables printed %q, which Darkpanel cannot read", truncate(line)))
	}
	for _, t := range tables {
		if covered[t] {
			continue
		}
		if !slices.Contains(nftFamilies, t.Family) || !nftSafeName.MatchString(t.Name) {
			reasons = append(reasons, fmt.Sprintf("nft table %q in family %q: table name not readable safely", truncate(t.Name), truncate(t.Family)))
			continue
		}
		out, err := run(ctx, x, "/usr/sbin/nft", "list", "table", t.Family, t.Name)
		if err != nil {
			return nil, nil, err
		}
		if t == f2bTable {
			if n, ok := parseF2B(out); ok {
				neighbours = append(neighbours, fwstate.Neighbour{Kind: "fail2ban", Banned: n.Banned})
				continue
			}
		}
		chains, err := parseNFTTable(out)
		if err != nil {
			reasons = append(reasons, fmt.Sprintf("nft table %s %s: %v", t.Family, t.Name, err))
			continue
		}
		for _, c := range chains {
			if c.Base && (c.Rules > 0 || c.Policy != "accept") {
				policy := c.Policy
				if policy == "" {
					policy = "(none printed)"
				}
				reasons = append(reasons, fmt.Sprintf("nft table %s %s: base chain %q on hook %q has %d rule(s) and policy %s",
					t.Family, t.Name, c.Name, c.Hook, c.Rules, policy))
			}
		}
	}
	return reasons, neighbours, nil
}
