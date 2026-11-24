package metrics

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"math"
	"math/bits"
	"slices"
	"strconv"
	"strings"
)

// maxMounts and maxIfaces bound the filesystems and interfaces of a sample.
const (
	maxMounts = 16
	maxIfaces = 16
)

// cpuTimes is the aggregate cpu line of /proc/stat in clock ticks: total
// counts user through steal (guest time is already inside user and nice),
// idle counts idle and iowait.
type cpuTimes struct {
	total, idle uint64
}

// parseProcStat reads the aggregate "cpu" line of /proc/stat.
func parseProcStat(data []byte) (cpuTimes, error) {
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 0 || f[0] != "cpu" {
			continue
		}
		f = f[1:]
		if len(f) < 4 {
			return cpuTimes{}, errors.New("stat: the cpu line has fewer than four fields")
		}
		var t cpuTimes
		for i, s := range f[:min(len(f), 8)] {
			n, err := strconv.ParseUint(s, 10, 64)
			if err != nil {
				return cpuTimes{}, fmt.Errorf("stat: cpu field %d is not a counter", i+1)
			}
			var carry uint64
			if t.total, carry = bits.Add64(t.total, n, 0); carry != 0 {
				return cpuTimes{}, errors.New("stat: the cpu counters overflow")
			}
			if i == 3 || i == 4 {
				t.idle += n // at most total, which did not overflow
			}
		}
		return t, nil
	}
	return cpuTimes{}, errors.New("stat: no cpu line")
}

// cpuBusy is the percent of the ticks between prev and cur not spent idle;
// ok is false when the counters did not advance (or went back, after a
// counter reset).
func cpuBusy(prev, cur cpuTimes) (busy float64, ok bool) {
	if cur.total <= prev.total || cur.idle < prev.idle {
		return 0, false
	}
	total, idle := cur.total-prev.total, cur.idle-prev.idle
	if idle > total {
		return 0, true
	}
	return float64(total-idle) / float64(total) * 100, true
}

// memInfo is memory and swap in bytes.
type memInfo struct {
	used, total, swapUsed, swapTotal uint64
}

// parseMeminfo reads /proc/meminfo: used is MemTotal less MemAvailable, swap
// used SwapTotal less SwapFree. MemTotal and MemAvailable are required, as
// every supported kernel reports both.
func parseMeminfo(data []byte) (memInfo, error) {
	var total, avail, swapTotal, swapFree uint64
	fields := map[string]*uint64{"MemTotal": &total, "MemAvailable": &avail, "SwapTotal": &swapTotal, "SwapFree": &swapFree}
	found := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		key, value, ok := strings.Cut(sc.Text(), ":")
		dst := fields[key]
		if !ok || dst == nil {
			continue
		}
		num, unit, _ := strings.Cut(strings.TrimSpace(value), " ")
		n, err := strconv.ParseUint(num, 10, 64)
		if err != nil {
			return memInfo{}, fmt.Errorf("meminfo: %s is not a number", key)
		}
		if unit == "kB" {
			if n > math.MaxUint64/1024 {
				return memInfo{}, fmt.Errorf("meminfo: %s is out of range", key)
			}
			n *= 1024
		}
		*dst = n
		found[key] = true
	}
	if !found["MemTotal"] || !found["MemAvailable"] {
		return memInfo{}, errors.New("meminfo: MemTotal or MemAvailable missing")
	}
	if avail > total || swapFree > swapTotal {
		return memInfo{}, errors.New("meminfo: available or free exceeds the total")
	}
	return memInfo{used: total - avail, total: total, swapUsed: swapTotal - swapFree, swapTotal: swapTotal}, nil
}

// parseLoadavg reads the three load averages of /proc/loadavg.
func parseLoadavg(data []byte) ([3]float64, error) {
	var v [3]float64
	f := strings.Fields(string(data))
	if len(f) < 3 {
		return v, errors.New("loadavg: want at least three fields")
	}
	for i := range v {
		n, err := strconv.ParseFloat(f[i], 64)
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 {
			return [3]float64{}, fmt.Errorf("loadavg: field %d is not a non-negative number", i+1)
		}
		v[i] = n
	}
	return v, nil
}

// netCounters is one interface's receive and transmit byte counters.
type netCounters struct {
	rx, tx uint64
}

// parseNetDev reads /proc/net/dev: each interface but lo with its receive
// bytes (the first field after the colon) and transmit bytes (the ninth).
func parseNetDev(data []byte) (map[string]netCounters, error) {
	out := map[string]netCounters{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		name, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue // the two header lines
		}
		name = strings.TrimSpace(name)
		if name == "" || strings.ContainsAny(name, " \t|") {
			continue
		}
		f := strings.Fields(rest)
		if len(f) < 9 {
			return nil, fmt.Errorf("net/dev: interface %q has fewer than nine fields", name)
		}
		rx, err1 := strconv.ParseUint(f[0], 10, 64)
		tx, err2 := strconv.ParseUint(f[8], 10, 64)
		if err1 != nil || err2 != nil {
			return nil, fmt.Errorf("net/dev: interface %q has a byte count that is not a counter", name)
		}
		if name != "lo" {
			out[name] = netCounters{rx: rx, tx: tx}
		}
	}
	return out, nil
}

// netRates is bytes per second over seconds for the first maxIfaces
// interfaces of cur by name; an interface new since prev, or whose counters
// went back, has no rate in this sample, and nothing has one over less than
// a second.
func netRates(prev, cur map[string]netCounters, seconds float64) []NetRate {
	names := make([]string, 0, len(cur))
	for name := range cur {
		names = append(names, name)
	}
	slices.Sort(names)
	out := []NetRate{}
	for _, name := range names[:min(len(names), maxIfaces)] {
		p, ok := prev[name]
		c := cur[name]
		if !ok || c.rx < p.rx || c.tx < p.tx || !(seconds >= 1) {
			continue
		}
		out = append(out, NetRate{Iface: name, RxBps: float64(c.rx-p.rx) / seconds, TxBps: float64(c.tx-p.tx) / seconds})
	}
	return out
}

// localFS are the filesystem types a sample measures; any ext* also counts.
var localFS = map[string]bool{"xfs": true, "btrfs": true, "zfs": true, "vfat": true}

// parseMounts reads /proc/self/mounts: the mount points of local
// filesystems, each once, the first maxMounts in order.
func parseMounts(data []byte) ([]string, error) {
	seen := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 0 {
			continue
		}
		if len(f) < 3 {
			return nil, errors.New("mounts: a line has fewer than three fields")
		}
		if !localFS[f[2]] && !strings.HasPrefix(f[2], "ext") {
			continue
		}
		if mount := unescapeMount(f[1]); strings.HasPrefix(mount, "/") {
			seen[mount] = true
		}
	}
	out := make([]string, 0, len(seen))
	for m := range seen {
		out = append(out, m)
	}
	slices.Sort(out)
	return out[:min(len(out), maxMounts)], nil
}

// unescapeMount decodes the octal escapes (\040 for a space) the kernel
// writes in a mount point.
func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) && isOctal(s[i+1]) && isOctal(s[i+2]) && isOctal(s[i+3]) {
			b.WriteByte((s[i+1]-'0')<<6 | (s[i+2]-'0')<<3 | (s[i+3] - '0'))
			i += 3
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func isOctal(c byte) bool { return c >= '0' && c <= '7' }
