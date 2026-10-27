// Package loaded reads the firewall's loaded state (spec 2.1): the
// netfilter tables of the iptables variant UFW uses, the foreign netfilter
// hooks that make a host unsupported and the live values of the supported
// sysctl keys. It also predicts the sysctl effect of a UFW start and
// compares two loaded states (2.3). It reads the host and never writes it.
package loaded

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
	"github.com/darkraise/darkpanel/internal/platform/netstat"
)

const (
	variantNFT    = "nft"
	variantLegacy = "legacy"

	// procNet holds the lists of the legacy tables that exist.
	procNet = "/proc/net"
	// maxStderr bounds the standard error an error message quotes.
	maxStderr = 200
	// legacyWarning starts iptables-nft-save's note that legacy tables exist.
	legacyWarning = "# Warning: iptables-legacy tables present"
)

// family is one IP family as the iptables tools name it.
type family struct {
	fam   fwstate.Family
	tool  string // "iptables" or "ip6tables"
	names string // the procNet file listing the family's legacy tables
}

var families = []family{
	{fam: fwstate.V4, tool: "iptables", names: "ip_tables_names"},
	{fam: fwstate.V6, tool: "ip6tables", names: "ip6_tables_names"},
}

// Reader reads the loaded state of the host it runs on.
type Reader struct {
	Interfaces     func() ([]netstat.Interface, error) // nil: netstat.Interfaces
	NFTablesLoaded func() bool                         // nil: /sys/module/nf_tables exists
}

// Read returns the loaded state: Variant from /usr/sbin/iptables --version,
// V4 and V6 from that variant's save tools, and Unsupported naming what
// makes the host unpredictable. A table the save tool does not print is
// absent; ip6tables not being installed leaves V6 nil. Every other failure
// to run or parse a tool is an error, never an empty firewall.
func (r Reader) Read(ctx context.Context, env core.Env) (fwstate.Loaded, error) {
	return r.read(ctx, env, procNet)
}

func (r Reader) read(ctx context.Context, env core.Env, procNet string) (fwstate.Loaded, error) {
	var st fwstate.Loaded
	for _, f := range families {
		v, ok, err := toolVariant(ctx, env.Exec, f.tool)
		if err != nil {
			return fwstate.Loaded{}, err
		}
		if !ok {
			if f.fam == fwstate.V4 {
				return fwstate.Loaded{}, errors.New("loaded: /usr/sbin/iptables is not installed")
			}
			continue
		}
		if f.fam == fwstate.V4 {
			st.Variant = v
		} else if v != st.Variant {
			st.Unsupported = append(st.Unsupported, fmt.Sprintf("ip6tables uses the %s variant but iptables uses the %s variant", v, st.Variant))
			haveNFT := r.NFTablesLoaded
			if haveNFT == nil {
				haveNFT = func() bool {
					_, statErr := os.Stat("/sys/module/nf_tables")
					return statErr == nil
				}
			}
			// Under the legacy variant, ip6tables-nft-save would make the kernel
			// load nf_tables; the host is unsupported already, so V6 stays nil.
			if v == variantNFT && !haveNFT() {
				continue
			}
		}
		tables := netfilter.Tables()
		if v == variantLegacy {
			if tables, err = legacyTables(procNet, f); err != nil {
				return fwstate.Loaded{}, err
			}
		}
		rs, err := saveTables(ctx, env.Exec, f.tool, v, tables)
		if err != nil {
			return fwstate.Loaded{}, err
		}
		for _, t := range rs.Tables {
			if t.Incompatible != "" {
				st.Unsupported = append(st.Unsupported, fmt.Sprintf("%s-%s table %s: %s", f.tool, v, t.Name, t.Incompatible))
			}
		}
		if f.fam == fwstate.V4 {
			st.V4 = rs
		} else {
			st.V6 = rs
		}
	}
	return st, nil
}

// toolVariant runs /usr/sbin/<tool> --version and returns the variant it
// names; ok is false when the tool is not installed.
func toolVariant(ctx context.Context, x core.Runner, tool string) (variant string, ok bool, err error) {
	out, err := run(ctx, x, "/usr/sbin/"+tool, "--version")
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	line, _, _ := strings.Cut(string(out), "\n")
	switch {
	case strings.HasSuffix(line, " (nf_tables)"):
		return variantNFT, true, nil
	case strings.HasSuffix(line, " (legacy)"):
		return variantLegacy, true, nil
	}
	return "", false, fmt.Errorf("loaded: cannot tell the %s variant from %q", tool, truncate(line))
}

// legacyTables returns the legacy tables of f that exist, from the kernel's
// list. Asking a legacy save tool for any other table would create it, and
// load its kernel module, so only listed tables are ever read. No list
// means the legacy ip_tables module is not loaded and no table exists.
func legacyTables(procNet string, f family) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(procNet, f.names)) //nolint:gosec // G304: a fixed name under /proc/net, read-only
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("loaded: %w", err)
	}
	var out []string
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		name := strings.TrimSpace(sc.Text())
		if slices.Contains(netfilter.Tables(), name) && !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	// Keep the order of netfilter.Tables, whatever order the kernel lists.
	slices.SortFunc(out, func(a, b string) int {
		return slices.Index(netfilter.Tables(), a) - slices.Index(netfilter.Tables(), b)
	})
	return out, nil
}

// saveTables runs /usr/sbin/<tool>-<variant>-save -t <table> for each table
// and merges the tables into one ruleset, in the order given.
func saveTables(ctx context.Context, x core.Runner, tool, variant string, tables []string) (*netfilter.Ruleset, error) {
	merged := &netfilter.Ruleset{}
	path := "/usr/sbin/" + tool + "-" + variant + "-save"
	for _, table := range tables {
		res, err := runResult(ctx, x, path, "-t", table)
		if err != nil {
			return nil, err
		}
		rs, err := netfilter.Parse(bytes.NewReader(res.Stdout))
		if err != nil {
			return nil, fmt.Errorf("loaded: %s -t %s: %w", path, table, err)
		}
		t := rs.Table(table)
		if t == nil || len(rs.Tables) != 1 {
			return nil, fmt.Errorf("loaded: %s -t %s printed %d tables; want exactly table %s", path, table, len(rs.Tables), table)
		}
		merged.Tables = append(merged.Tables, *t)
		// iptables-nft-save 1.8.7 prints its legacy warning with the rules,
		// later versions print it on standard error.
		merged.LegacyPresent = merged.LegacyPresent || rs.LegacyPresent || bytes.Contains(res.Stderr, []byte(legacyWarning))
	}
	return merged, nil
}

// run runs one read-only command and returns its standard output.
func run(ctx context.Context, x core.Runner, path string, args ...string) ([]byte, error) {
	res, err := runResult(ctx, x, path, args...)
	return res.Stdout, err
}

// runResult runs one read-only command. A non-zero exit is an error quoting
// the start of its standard error; a program that is not installed fails
// with an error wrapping fs.ErrNotExist, as the agent's runner reports it.
func runResult(ctx context.Context, x core.Runner, path string, args ...string) (core.CmdResult, error) {
	res, err := x.Run(ctx, core.Cmd{Path: path, Args: args})
	cmd := strings.Join(append([]string{path}, args...), " ")
	if err != nil {
		return core.CmdResult{}, fmt.Errorf("loaded: %s: %w", cmd, err)
	}
	if res.ExitCode != 0 {
		line, _, _ := strings.Cut(strings.TrimSpace(string(res.Stderr)), "\n")
		return core.CmdResult{}, fmt.Errorf("loaded: %s exited with status %d: %s", cmd, res.ExitCode, truncate(line))
	}
	return res, nil
}

func truncate(s string) string {
	if len(s) > maxStderr {
		return s[:maxStderr] + "..."
	}
	return s
}
