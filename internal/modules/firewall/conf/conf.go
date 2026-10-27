// Package conf reads UFW's configured state (spec 2.1) from its files:
// ufw.conf, /etc/default/ufw, the rule tuples of user.rules and
// user6.rules, the framework files and the sysctl file IPT_SYSCTL names. It
// also says which parts of a configuration Darkpanel cannot predict.
package conf

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/modules/firewall/model"
)

// The files Read parses itself, besides the rule files.
const (
	ufwConf     = "/etc/ufw/ufw.conf"
	defaultsUFW = "/etc/default/ufw"
	userRules   = "/etc/ufw/user.rules"
	userRules6  = "/etc/ufw/user6.rules"
)

// LogLevels are the values of ufw.conf's LOGLEVEL, lowest first.
var LogLevels = []string{"off", "low", "medium", "high", "full"}

var policies = []string{"ACCEPT", "DROP", "REJECT"}

// Read reads the configured state from the files under root: production
// passes "/", tests a temporary tree. Every state file and, when
// IPT_SYSCTL is set, /etc/ufw/sysctl.conf is recorded in Files, absent ones
// with Present false; IPT_SYSCTL naming any other file is unsupported. A state file that
// is not a regular file or exceeds fwstate.MaxFileSize, and a missing
// ufw.conf or /etc/default/ufw, are errors; anything Darkpanel cannot
// predict is a reason in Unsupported.
func Read(root string) (fwstate.Config, error) {
	c := fwstate.Config{Files: map[string]core.RestoreEntry{}}
	for _, p := range fwstate.StateFiles {
		e, err := readEntry(root, p)
		if err != nil {
			return fwstate.Config{}, err
		}
		c.Files[p] = e
	}
	for _, p := range []string{ufwConf, defaultsUFW} {
		if !c.Files[p].Present {
			return fwstate.Config{}, fmt.Errorf("conf: %s is missing", p)
		}
	}
	r := reader{c: &c}
	cv, dv := r.shell(ufwConf), r.shell(defaultsUFW)
	c.Enabled = r.yesNo(ufwConf, cv, "ENABLED")
	c.LogLevel = strings.ToLower(cv["LOGLEVEL"])
	if !slices.Contains(LogLevels, c.LogLevel) {
		r.unsupported("%s: LOGLEVEL is missing or not one of %s", ufwConf, strings.Join(LogLevels, ", "))
	}
	c.IPv6 = r.yesNo(defaultsUFW, dv, "IPV6")
	c.ManageBuiltins = r.yesNo(defaultsUFW, dv, "MANAGE_BUILTINS")
	if c.ManageBuiltins {
		r.unsupported("%s: MANAGE_BUILTINS=yes makes UFW flush the built-in chains, which Darkpanel cannot predict", defaultsUFW)
	}
	c.DefaultInput = r.policy(dv, "DEFAULT_INPUT_POLICY")
	c.DefaultOutput = r.policy(dv, "DEFAULT_OUTPUT_POLICY")
	c.DefaultForward = r.policy(dv, "DEFAULT_FORWARD_POLICY")

	required := []string{userRules, "/etc/ufw/before.rules", "/etc/ufw/after.rules"}
	if c.IPv6 {
		required = append(required, userRules6, "/etc/ufw/before6.rules", "/etc/ufw/after6.rules")
	}
	for _, p := range required {
		if !c.Files[p].Present {
			r.unsupported("%s is missing", p)
		}
	}
	c.Rules4 = r.tuples(userRules, false)
	c.Rules6 = r.tuples(userRules6, true)
	for _, name := range []string{"before.init", "after.init"} {
		fi, err := os.Stat(filepath.Join(root, "etc/ufw", name))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return fwstate.Config{}, fmt.Errorf("conf: stat /etc/ufw/%s: %w", name, err)
		}
		if fi.Mode()&0o111 != 0 {
			r.unsupported("/etc/ufw/%s is executable: UFW runs it at start, which Darkpanel cannot predict", name)
		}
	}
	if err := r.sysctl(root, dv["IPT_SYSCTL"]); err != nil {
		return fwstate.Config{}, err
	}
	return c, nil
}

// reader collects the unsupported reasons while Read fills the config.
type reader struct{ c *fwstate.Config }

func (r reader) unsupported(format string, args ...any) {
	r.c.Unsupported = append(r.c.Unsupported, fmt.Sprintf(format, args...))
}

func (r reader) shell(file string) map[string]string {
	vars, bad := parseShell(r.c.Files[file].Data)
	for _, n := range bad {
		r.unsupported("%s line %d: not a plain KEY=value line, which UFW's init script and its Python code may read differently", file, n)
	}
	return vars
}

// yesNo reads a yes/no key. UFW's Python code lower-cases the value and its
// init script accepts only yes and YES, so any other spelling would make
// the two disagree.
func (r reader) yesNo(file string, vars map[string]string, key string) bool {
	v, ok := vars[key]
	switch {
	case !ok:
		return false
	case v == "yes" || v == "YES":
		return true
	case v == "no" || v == "NO":
		return false
	}
	r.unsupported("%s: %s=%q is neither yes nor no", file, key, v)
	return false
}

// policy reads a default policy. The init script passes the value to
// iptables-restore as written, which accepts only upper case.
func (r reader) policy(vars map[string]string, key string) string {
	v := vars[key]
	if !slices.Contains(policies, v) {
		r.unsupported("%s: %s is missing or not ACCEPT, DROP or REJECT", defaultsUFW, key)
	}
	return v
}

// tuples parses the tuple lines of one rule file; a line UFW would skip or
// reinterpret is a reason, not an error.
func (r reader) tuples(path string, v6 bool) []model.Tuple {
	var out []model.Tuple
	for i, line := range strings.Split(string(r.c.Files[path].Data), "\n") {
		if !strings.HasPrefix(line, model.TuplePrefix) {
			continue
		}
		t, err := model.ParseTuple(line, v6)
		if err != nil {
			r.unsupported("%s line %d: %v", path, i+1, err)
			continue
		}
		out = append(out, t)
	}
	return out
}

// sysctlFile is the only sysctl file Darkpanel supports: snapshots restore
// it through an owned path the manifest declares, which cannot name a path
// read from the host.
const sysctlFile = "/etc/ufw/sysctl.conf"

// sysctl records the file IPT_SYSCTL names and its settings. Any other file
// than sysctlFile is unsupported and not read; Files then records
// sysctlFile, so a snapshot still holds UFW's default file.
func (r reader) sysctl(root, name string) error {
	if name == "" {
		return nil
	}
	r.c.SysctlFile = name
	supported := filepath.IsAbs(name) && filepath.Clean(name) == sysctlFile
	if !supported {
		r.unsupported("%s: IPT_SYSCTL names %q; only %s is supported", defaultsUFW, name, sysctlFile)
	}
	e, err := readEntry(root, sysctlFile)
	if err != nil {
		return err
	}
	r.c.Files[sysctlFile] = e
	if !supported {
		return nil
	}
	r.c.SysctlFile = sysctlFile
	name = sysctlFile
	for _, s := range parseSysctl(e.Data) {
		r.c.Sysctl = append(r.c.Sysctl, s)
		switch {
		case strings.ContainsAny(s.Key, "*?["):
			r.unsupported("%s sets %s with a glob, which Darkpanel cannot predict", name, s.Key)
		case FilteringSysctl(s.Key):
			r.unsupported("%s sets %s, which changes forwarding or filtering in a way Darkpanel cannot predict", name, s.Key)
		}
	}
	return nil
}

// readEntry reads one file under root as a restore entry for its absolute
// path. Symbolic links are not followed.
func readEntry(root, path string) (core.RestoreEntry, error) {
	e := core.RestoreEntry{Path: path}
	f, err := os.OpenFile(filepath.Join(root, path), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0) //nolint:gosec // G304: a UFW state file or the sysctl file UFW names, read-only
	if errors.Is(err, fs.ErrNotExist) {
		return e, nil
	}
	if err != nil {
		return e, fmt.Errorf("conf: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		return e, fmt.Errorf("conf: stat %s: %w", path, err)
	}
	if !fi.Mode().IsRegular() {
		return e, fmt.Errorf("conf: %s is not a regular file", path)
	}
	data, err := io.ReadAll(io.LimitReader(f, fwstate.MaxFileSize+1))
	if err != nil {
		return e, fmt.Errorf("conf: read %s: %w", path, err)
	}
	if len(data) > fwstate.MaxFileSize {
		return e, fmt.Errorf("conf: %s is larger than %d bytes", path, fwstate.MaxFileSize)
	}
	e.Present, e.Mode, e.Data = true, fi.Mode().Perm(), data
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		e.UID, e.GID = int(st.Uid), int(st.Gid)
	}
	return e, nil
}

var shellAssignRe = regexp.MustCompile(`^([A-Z_][A-Z0-9_]*)=([A-Za-z0-9_./:@%+,-]*|"[A-Za-z0-9_./:@%+, -]*")[ \t]*$`)

// parseShell accepts only the forms that UFW's init script and its Python
// _get_defaults both read the same way: blank lines, comment lines and
// KEY=value assignments at column 0 with an upper-case key and a plain or
// double-quoted value, the last assignment winning. It returns the
// variables and the 1-based numbers of every other line.
func parseShell(data []byte) (map[string]string, []int) {
	vars := map[string]string{}
	var bad []int
	for i, line := range strings.Split(string(data), "\n") {
		if t := strings.TrimLeft(line, " \t"); t == "" || t[0] == '#' {
			continue
		}
		m := shellAssignRe.FindStringSubmatch(line)
		if m == nil {
			bad = append(bad, i+1)
			continue
		}
		vars[m[1]] = strings.Trim(m[2], `"`)
	}
	return vars, bad
}

// parseSysctl reads a sysctl file as "sysctl -p" does: comments start with
// # or ;, a leading - only suppresses errors, and a line without "=" is
// skipped.
func parseSysctl(data []byte) []fwstate.SysctlSetting {
	var out []fwstate.SysctlSetting
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		key, val, ok := strings.Cut(strings.TrimPrefix(line, "-"), "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			continue
		}
		out = append(out, fwstate.SysctlSetting{Key: SysctlKey(key), Value: strings.TrimSpace(val)})
	}
	return out
}

// SysctlKey converts a sysctl name to its /proc/sys form as procps does:
// when the first separator is a dot, dots and slashes swap, so
// "net.ipv4.conf.eth0/100.rp_filter" names "net/ipv4/conf/eth0.100/rp_filter";
// a name that starts with a slash-separated component is already in that
// form.
func SysctlKey(name string) string {
	i := strings.IndexAny(name, "./")
	if i < 0 || name[i] == '/' {
		return name
	}
	b := []byte(name)
	for j := i; j < len(b); j++ {
		switch b[j] {
		case '.':
			b[j] = '/'
		case '/':
			b[j] = '.'
		}
	}
	return string(b)
}

var (
	supportedSysctlRe = regexp.MustCompile(`^(net/ipv4/ip_forward|net/ipv6/conf/[^/]+/(forwarding|accept_ra)|net/ipv4/conf/[^/]+/(rp_filter|accept_source_route))$`)
	filteringSysctlRe = regexp.MustCompile(`^(net/ipv4/conf/[^/]+/forwarding|.*/route_localnet|net/bridge/bridge-nf-call-.*)$`)
)

// SupportedSysctl reports whether key (in /proc/sys form) is one of the
// sysctl keys the firewall predicts, snapshots and restores (spec 2.1):
// net/ipv4/ip_forward and, for all, default or an interface,
// net/ipv6/conf/*/forwarding and accept_ra and net/ipv4/conf/*/rp_filter
// and accept_source_route.
func SupportedSysctl(key string) bool { return supportedSysctlRe.MatchString(key) }

// FilteringSysctl reports whether key (in /proc/sys form) changes
// forwarding or filtering outside the supported set, which makes a sysctl
// file that sets it unsupported: net/ipv4/conf/*/forwarding,
// */route_localnet and net/bridge/bridge-nf-call-*.
func FilteringSysctl(key string) bool { return filteringSysctlRe.MatchString(key) }

// DefaultsIPv6 returns IPV6 of /etc/default/ufw's contents data as Read
// reads it: true only for yes or YES.
func DefaultsIPv6(data []byte) bool {
	c := fwstate.Config{Files: map[string]core.RestoreEntry{defaultsUFW: {Path: defaultsUFW, Present: true, Data: data}}}
	r := reader{c: &c}
	return r.yesNo(defaultsUFW, r.shell(defaultsUFW), "IPV6")
}
