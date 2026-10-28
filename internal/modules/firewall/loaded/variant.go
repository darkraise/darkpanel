package loaded

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"slices"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

// ReadVariant returns the named tables of one family as one iptables
// variant ("nft" or "legacy") holds them, in the order requested. The result
// is nil when that variant cannot be read: nf_tables is not loaded, or the
// variant's save tool is not installed. A legacy table that does not exist
// is left out, so the ruleset may hold fewer tables than were asked for.
func (r Reader) ReadVariant(ctx context.Context, env core.Env, f fwstate.Family, variant string, tables []string) (*netfilter.Ruleset, error) {
	return r.readVariant(ctx, env, procNet, f, variant, tables)
}

// readVariant is ReadVariant with the directory that stands for /proc/net.
// It checks its arguments before running anything. Nothing runs while
// nf_tables is not loaded, since asking would make the kernel load the
// module, and a legacy table the kernel does not list is never asked for,
// since asking would create it.
func (r Reader) readVariant(ctx context.Context, env core.Env, procNet string, f fwstate.Family, variant string, tables []string) (*netfilter.Ruleset, error) {
	fi := slices.IndexFunc(families, func(c family) bool { return c.fam == f })
	if fi < 0 {
		return nil, fmt.Errorf("loaded: unknown family %q", f)
	}
	fam := families[fi]
	if variant != variantNFT && variant != variantLegacy {
		return nil, fmt.Errorf("loaded: unknown iptables variant %q", variant)
	}
	var want []string
	for _, t := range tables {
		if !slices.Contains(netfilter.Tables(), t) {
			return nil, fmt.Errorf("loaded: unknown table %q", t)
		}
		if !slices.Contains(want, t) {
			want = append(want, t)
		}
	}
	switch variant {
	case variantNFT:
		nftLoaded := r.NFTablesLoaded
		if nftLoaded == nil {
			nftLoaded = nfTablesLoaded
		}
		if !nftLoaded() {
			return nil, nil
		}
	case variantLegacy:
		listed, err := legacyTables(procNet, fam)
		if err != nil {
			return nil, err
		}
		want = slices.DeleteFunc(want, func(t string) bool { return !slices.Contains(listed, t) })
	}
	rs, err := saveTables(ctx, env.Exec, fam.tool, variant, want)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return rs, nil
}
