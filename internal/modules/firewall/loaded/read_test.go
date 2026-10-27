package loaded

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/platform"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
	"github.com/darkraise/darkpanel/internal/platform/netstat"
)

// The golden captures of testdata/capture.sh, one directory per image and
// scenario.
const ldGoldenDir = "../testdata/golden"

var (
	ldImages    = []string{"ubuntu-22.04", "ubuntu-24.04", "debian-12", "debian-13"}
	ldScenarios = []string{"fresh", "enabled", "rules", "disabled", "ipv6no", "logging", "manual", "sysctl"}
)

// ldResult is what the fake runner answers for one argument vector.
type ldResult struct {
	out, stderr string
	code        int
	err         error
}

// ldRunner is a fake core.Runner keyed by "<path> <args...>". It refuses a
// vector the agent's exec allowlist would refuse and any vector it has no
// answer for, and records every call.
type ldRunner struct {
	answers map[string]ldResult
	calls   []string
}

func (r *ldRunner) Run(_ context.Context, cmd core.Cmd) (core.CmdResult, error) {
	key := strings.Join(append([]string{cmd.Path}, cmd.Args...), " ")
	r.calls = append(r.calls, key)
	allowed := false
	for _, rule := range platform.ProductionRules() {
		if rule.Path == cmd.Path && rule.Match(cmd.Args) == nil {
			allowed = true
		}
	}
	if !allowed {
		return core.CmdResult{}, fmt.Errorf("ldRunner: %q is not in the exec allowlist", key)
	}
	a, ok := r.answers[key]
	if !ok {
		return core.CmdResult{}, fmt.Errorf("ldRunner: unexpected command %q", key)
	}
	return core.CmdResult{Stdout: []byte(a.out), Stderr: []byte(a.stderr), ExitCode: a.code}, a.err
}

// ldMissing is the runner's error for a program that is not installed.
func ldMissing(path string) error {
	return fmt.Errorf("platform: run %s: %w", path, &fs.PathError{Op: "fork/exec", Path: path, Err: syscall.ENOENT})
}

// ldGolden answers the version and save commands of one golden scenario.
func ldGolden(t *testing.T, image, scenario string) *ldRunner {
	t.Helper()
	dir := filepath.Join(ldGoldenDir, image, scenario)
	version, err := os.ReadFile(filepath.Join(dir, "version.txt")) //nolint:gosec // G304: a fixed name under the golden directory
	if err != nil {
		t.Fatal(err)
	}
	line, _, _ := strings.Cut(string(version), "\n")
	r := &ldRunner{answers: map[string]ldResult{
		"/usr/sbin/iptables --version":  {out: line + "\n"},
		"/usr/sbin/ip6tables --version": {out: "ip6" + line + "\n"},
	}}
	for _, fam := range []string{"v4", "v6"} {
		tool := "iptables"
		if fam == "v6" {
			tool = "ip6tables"
		}
		for _, table := range netfilter.Tables() {
			data, err := os.ReadFile(filepath.Join(dir, "save", "nft-"+fam+"-"+table+".txt")) //nolint:gosec // G304: a fixed name under the golden directory
			if err != nil {
				t.Fatal(err)
			}
			r.answers["/usr/sbin/"+tool+"-nft-save -t "+table] = ldResult{out: string(data)}
		}
	}
	return r
}

// ldFiles is a fake core.FileWriter that answers ReadSysctl from a map; a
// key not in the map fails as the agent's Files fails for a key the kernel
// lacks. Every other method is left to the nil embedded interface and
// panics.
type ldFiles struct {
	core.FileWriter
	sysctl map[string]string
	reads  []string
}

func (f *ldFiles) ReadSysctl(_ context.Context, module, key string) (string, error) {
	f.reads = append(f.reads, module+" "+key)
	v, ok := f.sysctl[key]
	if !ok {
		return "", fmt.Errorf("platform: open sysctl %s: %w", key, syscall.ENOENT)
	}
	return v, nil
}

// ldEnv is an Env whose runner and files are the fakes.
func ldEnv(x core.Runner, sysctl map[string]string) core.Env {
	return core.Env{Exec: x, Files: &ldFiles{sysctl: sysctl}}
}

// ldReader is a Reader that sees no interfaces and no nf_tables module, so
// it reads nothing of the host it runs on.
func ldReader() Reader {
	return Reader{
		Interfaces:     func() ([]netstat.Interface, error) { return nil, nil },
		NFTablesLoaded: func() bool { return false },
	}
}

func ldParse(t *testing.T, text string) *netfilter.Ruleset {
	t.Helper()
	rs, err := netfilter.Parse(strings.NewReader(text))
	if err != nil {
		t.Fatal(err)
	}
	return rs
}

func TestReadGolden(t *testing.T) {
	for _, img := range ldImages {
		for _, sc := range ldScenarios {
			x := ldGolden(t, img, sc)
			st, err := ldReader().read(context.Background(), ldEnv(x, nil), t.TempDir())
			if err != nil {
				t.Fatalf("%s/%s: %v", img, sc, err)
			}
			if st.Variant != "nft" || len(st.Unsupported) != 0 {
				t.Errorf("%s/%s: variant %q, unsupported %q", img, sc, st.Variant, st.Unsupported)
			}
			for fam, rs := range map[string]*netfilter.Ruleset{"v4": st.V4, "v6": st.V6} {
				if rs == nil {
					t.Fatalf("%s/%s: %s is nil", img, sc, fam)
				}
				var names []string
				for _, tb := range rs.Tables {
					names = append(names, tb.Name)
					data, err := os.ReadFile(filepath.Join(ldGoldenDir, img, sc, "save", "nft-"+fam+"-"+tb.Name+".txt")) //nolint:gosec // G304: a fixed name under the golden directory
					if err != nil {
						t.Fatal(err)
					}
					if want := ldParse(t, string(data)).Tables[0]; !reflect.DeepEqual(tb, want) {
						t.Errorf("%s/%s: %s table %s differs from the capture", img, sc, fam, tb.Name)
					}
				}
				if !slices.Equal(names, netfilter.Tables()) {
					t.Errorf("%s/%s: %s tables %v", img, sc, fam, names)
				}
			}
			if len(x.calls) != 12 {
				t.Errorf("%s/%s: %d commands: %q", img, sc, len(x.calls), x.calls)
			}
		}
	}
}

const ldLegacyFilter = `# Generated by iptables-save v1.8.9 on Sat Oct 24 04:31:06 2026
*filter
:INPUT ACCEPT [0:0]
:FORWARD ACCEPT [0:0]
:OUTPUT ACCEPT [0:0]
-A INPUT -s 198.51.100.7/32 -j DROP
COMMIT
# Completed on Sat Oct 24 04:31:06 2026
`

const ldLegacyRaw = `*raw
:PREROUTING ACCEPT [0:0]
:OUTPUT ACCEPT [0:0]
COMMIT
`

func TestReadLegacy(t *testing.T) {
	procNet := t.TempDir()
	if err := os.WriteFile(filepath.Join(procNet, "ip_tables_names"), []byte("raw\nfilter\nbogus\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	x := &ldRunner{answers: map[string]ldResult{
		"/usr/sbin/iptables --version":             {out: "iptables v1.8.9 (legacy)\n"},
		"/usr/sbin/ip6tables --version":            {out: "ip6tables v1.8.9 (legacy)\n"},
		"/usr/sbin/iptables-legacy-save -t filter": {out: ldLegacyFilter},
		"/usr/sbin/iptables-legacy-save -t raw":    {out: ldLegacyRaw},
	}}
	st, err := ldReader().read(context.Background(), ldEnv(x, nil), procNet)
	if err != nil {
		t.Fatal(err)
	}
	if st.Variant != "legacy" || len(st.Unsupported) != 0 {
		t.Fatalf("variant %q, unsupported %q", st.Variant, st.Unsupported)
	}
	if len(st.V4.Tables) != 2 || st.V4.Tables[0].Name != "filter" || st.V4.Tables[1].Name != "raw" {
		t.Fatalf("v4 tables %+v", st.V4.Tables)
	}
	if got := st.V4.Table("filter").Chain("INPUT").Rules[0].Text; got != "-A INPUT -s 198.51.100.7/32 -j DROP" {
		t.Errorf("filter INPUT rule %q", got)
	}
	if st.V6 == nil || len(st.V6.Tables) != 0 {
		t.Errorf("v6 = %+v, want an empty ruleset: no legacy IPv6 table exists", st.V6)
	}
}

func TestReadWithoutIP6tables(t *testing.T) {
	x := ldGolden(t, "debian-12", "rules")
	x.answers["/usr/sbin/ip6tables --version"] = ldResult{err: ldMissing("/usr/sbin/ip6tables")}
	st, err := ldReader().read(context.Background(), ldEnv(x, nil), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if st.V6 != nil || st.V4 == nil {
		t.Errorf("v4 %v, v6 %v; want only v4", st.V4 != nil, st.V6 != nil)
	}
	for _, c := range x.calls {
		if strings.Contains(c, "ip6tables-") {
			t.Errorf("ran %q without ip6tables", c)
		}
	}
}

func TestReadMixedVariants(t *testing.T) {
	x := ldGolden(t, "debian-12", "rules")
	x.answers["/usr/sbin/ip6tables --version"] = ldResult{out: "ip6tables v1.8.9 (legacy)\n"}
	st, err := ldReader().read(context.Background(), ldEnv(x, nil), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"ip6tables uses the legacy variant but iptables uses the nft variant"}
	if !slices.Equal(st.Unsupported, want) {
		t.Errorf("unsupported %q, want %q", st.Unsupported, want)
	}
	if st.V6 == nil || len(st.V6.Tables) != 0 {
		t.Errorf("v6 %+v, want the legacy variant's tables: none listed", st.V6)
	}
}

func TestReadIncompatible(t *testing.T) {
	x := ldGolden(t, "debian-12", "fresh")
	x.answers["/usr/sbin/iptables-nft-save -t filter"] = ldResult{out: "# Table `filter' contains incompatible base-chains, use 'nft' tool to list them.\n" +
		"*filter\n:INPUT ACCEPT [0:0]\n:FORWARD ACCEPT [0:0]\n:OUTPUT ACCEPT [0:0]\nCOMMIT\n"}
	st, err := ldReader().read(context.Background(), ldEnv(x, nil), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"iptables-nft table filter: Table `filter' contains incompatible base-chains, use 'nft' tool to list them."}
	if !slices.Equal(st.Unsupported, want) {
		t.Errorf("unsupported %q, want %q", st.Unsupported, want)
	}
}

func TestReadErrors(t *testing.T) {
	cases := []struct {
		name string
		key  string
		res  ldResult
		want string
	}{
		{"no iptables", "/usr/sbin/iptables --version", ldResult{err: ldMissing("/usr/sbin/iptables")}, "loaded: /usr/sbin/iptables is not installed"},
		{"old iptables", "/usr/sbin/iptables --version", ldResult{out: "iptables v1.6.1\n"}, `loaded: cannot tell the iptables variant from "iptables v1.6.1"`},
		{"version fails", "/usr/sbin/ip6tables --version", ldResult{code: 1, stderr: "boom\nmore"}, "loaded: /usr/sbin/ip6tables --version exited with status 1: boom"},
		{"runner error", "/usr/sbin/iptables-nft-save -t nat", ldResult{err: context.DeadlineExceeded}, "loaded: /usr/sbin/iptables-nft-save -t nat: context deadline exceeded"},
		{"save fails", "/usr/sbin/ip6tables-nft-save -t raw", ldResult{code: 1, stderr: "ip6tables-nft-save: table raw: permission denied\n"}, "loaded: /usr/sbin/ip6tables-nft-save -t raw exited with status 1: ip6tables-nft-save: table raw: permission denied"},
		{"empty save", "/usr/sbin/iptables-nft-save -t mangle", ldResult{}, "loaded: /usr/sbin/iptables-nft-save -t mangle: netfilter: no tables in the input"},
		{"wrong table", "/usr/sbin/iptables-nft-save -t security", ldResult{out: "*raw\n:OUTPUT ACCEPT [0:0]\nCOMMIT\n"}, "loaded: /usr/sbin/iptables-nft-save -t security printed 1 tables; want exactly table security"},
		{"bad save", "/usr/sbin/iptables-nft-save -t filter", ldResult{out: "*filter\n:INPUT ACCEPT [0:0]\n"}, "loaded: /usr/sbin/iptables-nft-save -t filter: netfilter: table filter has no COMMIT"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			x := ldGolden(t, "debian-12", "rules")
			x.answers[c.key] = c.res
			_, err := ldReader().read(context.Background(), ldEnv(x, nil), t.TempDir())
			if err == nil || err.Error() != c.want {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
			if c.name == "empty save" && !errors.Is(err, netfilter.ErrNoTables) {
				t.Errorf("err does not wrap netfilter.ErrNoTables")
			}
		})
	}
}

func TestLegacyTablesUnreadable(t *testing.T) {
	procNet := t.TempDir()
	if err := os.Mkdir(filepath.Join(procNet, "ip_tables_names"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := legacyTables(procNet, families[0]); err == nil {
		t.Fatal("a list that cannot be read is no error")
	}
}
