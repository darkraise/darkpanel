package tuning

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// The kinds of value a curated key takes.
const (
	kindInteger   = "integer"
	kindChoice    = "choice"
	kindPortRange = "port-range"
)

// sysctlKey is one kernel parameter of spec section 7.
type sysctlKey struct {
	name     string // dotted, as sysctl.d files write it
	help     string
	kind     string
	min, max int64    // integer
	choices  []string // choice; nil for tcp_congestion_control, whose choices the kernel lists
	pattern  string   // the value pattern of the OwnedSysctl declaration, as the kernel reads it back
}

// path is the key under /proc/sys.
func (k sysctlKey) path() string { return strings.ReplaceAll(k.name, ".", "/") }

const congestionKey = "net.ipv4.tcp_congestion_control"

// availableCongestion lists the congestion control algorithms the kernel has.
const availableCongestion = "/proc/sys/net/ipv4/tcp_available_congestion_control"

const (
	intPattern  = `^[0-9]{1,19}$`
	portPattern = "^[0-9]{1,5}\t[0-9]{1,5}$"
)

func integer(name, help string, lo, hi int64) sysctlKey {
	return sysctlKey{name: name, help: help, kind: kindInteger, min: lo, max: hi, pattern: intPattern}
}

// curated are the keys tuning.sysctl.apply writes, in the order the file
// holds them.
var curated = []sysctlKey{
	integer("vm.swappiness", "How readily the kernel moves memory to swap; lower keeps more in RAM.", 0, 200),
	integer("vm.vfs_cache_pressure", "How readily the kernel frees its cache of file and directory names.", 1, 1000),
	integer("vm.dirty_ratio", "Percent of memory unwritten data may fill before writers must wait.", 1, 100),
	integer("vm.dirty_background_ratio", "Percent of memory unwritten data may fill before writing starts.", 1, 100),
	integer("vm.overcommit_memory", "Memory overcommit: 0 guesses, 1 always allows, 2 never beyond the limit.", 0, 2),
	integer("vm.max_map_count", "Most memory map areas one process may have; some databases need more.", 65530, 2147483647),
	integer("fs.file-max", "Most files the whole system may have open.", 8192, 9223372036854775807),
	integer("fs.inotify.max_user_watches", "Most files and directories one user may watch for changes.", 8192, 4194304),
	integer("fs.inotify.max_user_instances", "Most change watchers one user may open.", 128, 65536),
	integer("net.core.somaxconn", "Longest queue of connections waiting for a server to accept them.", 128, 65535),
	integer("net.core.netdev_max_backlog", "Most received packets queued while the kernel catches up.", 1000, 262144),
	{name: "net.core.default_qdisc", help: "Default packet queueing discipline of network interfaces.", kind: kindChoice,
		choices: []string{"fq", "fq_codel", "cake", "pfifo_fast"}, pattern: `^(fq|fq_codel|cake|pfifo_fast)$`},
	{name: congestionKey, help: "TCP congestion control algorithm of new connections.", kind: kindChoice,
		pattern: `^[a-z0-9_]{1,15}$`},
	integer("net.ipv4.tcp_fastopen", "TCP Fast Open: 0 off, 1 outgoing, 2 incoming, 3 both.", 0, 3),
	integer("net.ipv4.tcp_tw_reuse", "Reuse closing connections' ports for new outgoing ones: 0 off, 1 on, 2 loopback.", 0, 2),
	{name: "net.ipv4.ip_local_port_range", help: "Lowest and highest local port of outgoing connections.", kind: kindPortRange,
		min: 1, max: 65535, pattern: portPattern},
}

func curatedKey(name string) (sysctlKey, bool) {
	i := slices.IndexFunc(curated, func(k sysctlKey) bool { return k.name == name })
	if i < 0 {
		return sysctlKey{}, false
	}
	return curated[i], true
}

// canon checks v against the key's range and returns it as the file
// writes it, or what is wrong with it; choices are the congestion control
// algorithms available.
func (k sysctlKey) canon(v string, choices []string) (string, string) {
	switch k.kind {
	case kindInteger:
		n, err := digits(v)
		if err != nil || n < k.min || n > k.max {
			return "", fmt.Sprintf("Use %d to %d", k.min, k.max)
		}
		return strconv.FormatInt(n, 10), ""
	case kindChoice:
		if k.choices != nil {
			choices = k.choices
		}
		if !slices.Contains(choices, v) {
			if len(choices) == 0 {
				return "", "The kernel lists no algorithm in " + availableCongestion
			}
			return "", "Use one of " + strings.Join(choices, ", ")
		}
		return v, ""
	}
	ports := fmt.Sprintf("Give the lowest and highest port, %d to %d", k.min, k.max)
	f := strings.Fields(v)
	if len(f) != 2 {
		return "", ports
	}
	lo, err1 := digits(f[0])
	hi, err2 := digits(f[1])
	switch {
	case err1 != nil || err2 != nil || lo < k.min || hi > k.max:
		return "", ports
	case lo >= hi:
		return "", fmt.Sprintf("The lowest port %d must be below the highest %d", lo, hi)
	}
	return fmt.Sprintf("%d %d", lo, hi), ""
}

func digits(v string) (int64, error) {
	if v == "" || strings.Trim(v, "0123456789") != "" {
		return 0, fmt.Errorf("%q is not a number", v)
	}
	return strconv.ParseInt(v, 10, 64)
}

// kernel is a canonical value as written to /proc/sys: the kernel prints a
// port range with a tab and reads the value back that way.
func (k sysctlKey) kernel(v string) string {
	if k.kind == kindPortRange {
		return strings.Replace(v, " ", "\t", 1)
	}
	return v
}

// fromKernel is a value read from /proc/sys as the file writes it.
func fromKernel(v string) string { return strings.Join(strings.Fields(v), " ") }
