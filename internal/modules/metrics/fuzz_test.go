package metrics

import (
	"math"
	"strings"
	"testing"
)

func seed(f *testing.F, names ...string) {
	f.Helper()
	for _, name := range names {
		f.Add(fixture(f, name))
	}
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 }

// FuzzProcStat checks that the busy percent of any two parsed cpu lines is
// finite and within 0 to 100.
func FuzzProcStat(f *testing.F) {
	stat0, stat1 := fixture(f, "stat-0"), fixture(f, "stat-1")
	f.Add(stat0, stat1)
	f.Add([]byte("cpu 1 2 3 4\n"), []byte("cpu 18446744073709551615 0 0 0\n"))
	f.Fuzz(func(t *testing.T, a, b []byte) {
		prev, err1 := parseProcStat(a)
		cur, err2 := parseProcStat(b)
		if err1 != nil || err2 != nil {
			return
		}
		if prev.idle > prev.total || cur.idle > cur.total {
			t.Errorf("parseProcStat counts more idle than total ticks: %+v %+v", prev, cur)
		}
		if busy, ok := cpuBusy(prev, cur); ok && (!finite(busy) || busy > 100) {
			t.Errorf("cpuBusy(%q, %q) = %v", a, b, busy)
		}
	})
}

// FuzzMeminfo checks that used memory and swap never exceed their totals.
func FuzzMeminfo(f *testing.F) {
	seed(f, "meminfo")
	f.Add([]byte("MemTotal: 18446744073709551615 kB\nMemAvailable: 0\n"))
	f.Fuzz(func(t *testing.T, in []byte) {
		m, err := parseMeminfo(in)
		if err != nil {
			return
		}
		if m.used > m.total || m.swapUsed > m.swapTotal {
			t.Errorf("parseMeminfo(%q) = %+v", in, m)
		}
	})
}

// FuzzLoadavg checks that every accepted load average is finite and
// non-negative.
func FuzzLoadavg(f *testing.F) {
	seed(f, "loadavg")
	f.Add([]byte("NaN Inf -1 1/2 3"))
	f.Fuzz(func(t *testing.T, in []byte) {
		v, err := parseLoadavg(in)
		if err != nil {
			return
		}
		for _, x := range v {
			if !finite(x) {
				t.Errorf("parseLoadavg(%q) = %v", in, v)
			}
		}
	})
}

// FuzzNetDev checks that the rates between two parsed snapshots are finite
// and non-negative, at most 16, and never for lo.
func FuzzNetDev(f *testing.F) {
	f.Add(fixture(f, "netdev-0"), fixture(f, "netdev-1"), 15.0)
	f.Add([]byte("eth0: 1 0 0 0 0 0 0 0 1\n"), []byte("eth0: 18446744073709551615 0 0 0 0 0 0 0 0\n"), 0.001)
	f.Fuzz(func(t *testing.T, a, b []byte, seconds float64) {
		prev, err1 := parseNetDev(a)
		cur, err2 := parseNetDev(b)
		if err1 != nil || err2 != nil || !finite(seconds) {
			return
		}
		rates := netRates(prev, cur, seconds)
		if len(rates) > maxIfaces {
			t.Errorf("netRates gave %d interfaces", len(rates))
		}
		for _, r := range rates {
			if r.Iface == "lo" || !finite(r.RxBps) || !finite(r.TxBps) {
				t.Errorf("netRates(%q, %q, %v) gave %+v", a, b, seconds, r)
			}
		}
	})
}

// FuzzMounts checks that the mount points are absolute, distinct, sorted
// and at most 16.
func FuzzMounts(f *testing.F) {
	seed(f, "mounts")
	f.Add([]byte("/dev/sda1 /a\\040b\\134 ext4 rw 0 0\n/dev/sdb /x\\7 xfs\n"))
	f.Fuzz(func(t *testing.T, in []byte) {
		mounts, err := parseMounts(in)
		if err != nil {
			return
		}
		if len(mounts) > maxMounts {
			t.Errorf("parseMounts gave %d mounts", len(mounts))
		}
		for i, m := range mounts {
			if !strings.HasPrefix(m, "/") || (i > 0 && mounts[i-1] >= m) {
				t.Errorf("parseMounts(%q) = %q", in, mounts)
			}
		}
	})
}
