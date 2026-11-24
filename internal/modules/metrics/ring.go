package metrics

import (
	"math"
	"slices"
	"sync"
	"time"
)

// ringSize is one hour of samples at one every 15 s.
const ringSize = 240

// maxMinutes is the most minute averages one read returns.
const maxMinutes = 60

// ring holds the last ringSize samples in memory.
type ring struct {
	mu   sync.Mutex
	buf  []Sample
	next int // where the next sample goes once buf is full
	cpus int // the logical CPUs at the last sample
}

func newRing() *ring { return &ring{buf: make([]Sample, 0, ringSize)} }

func (r *ring) add(s Sample) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.buf) < ringSize {
		r.buf = append(r.buf, s)
		return
	}
	r.buf[r.next] = s
	r.next = (r.next + 1) % ringSize
}

// setCPUs records the host's logical CPUs at the last sample.
func (r *ring) setCPUs(n int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cpus = n
}

// cpuCount is the host's logical CPUs at the last sample, 0 before it.
func (r *ring) cpuCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cpus
}

// snapshot is the samples held, oldest first.
func (r *ring) snapshot() []Sample {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append(slices.Clone(r.buf[r.next:]), r.buf[:r.next]...)
}

// minuteAverages averages samples (oldest first) by minute: the minutes that
// start after since (any minute when since is zero) and ended by now, oldest
// first, at most maxMinutes.
func minuteAverages(samples []Sample, since, now time.Time) []Sample {
	out := []Sample{}
	for i := 0; i < len(samples) && len(out) < maxMinutes; {
		start := samples[i].TS.UTC().Truncate(time.Minute)
		j := i
		for j < len(samples) && samples[j].TS.UTC().Truncate(time.Minute).Equal(start) {
			j++
		}
		if (since.IsZero() || start.After(since)) && !start.Add(time.Minute).After(now) {
			out = append(out, average(start, samples[i:j]))
		}
		i = j
	}
	return out
}

// average is the mean of samples, stamped start; a filesystem or interface
// is averaged over the samples that have it.
func average(start time.Time, samples []Sample) Sample {
	n := float64(len(samples))
	var cpu, memUsed, memTotal, swapUsed, swapTotal, l1, l5, l15 float64
	type fsSum struct{ used, total, n float64 }
	type netSum struct{ rx, tx, n float64 }
	fs, net := map[string]*fsSum{}, map[string]*netSum{}
	for _, s := range samples {
		cpu += s.CPU
		memUsed += float64(s.MemUsed)
		memTotal += float64(s.MemTotal)
		swapUsed += float64(s.SwapUsed)
		swapTotal += float64(s.SwapTotal)
		l1, l5, l15 = l1+s.Load1, l5+s.Load5, l15+s.Load15
		for _, f := range s.FS {
			if fs[f.Mount] == nil {
				fs[f.Mount] = &fsSum{}
			}
			a := fs[f.Mount]
			a.used, a.total, a.n = a.used+float64(f.Used), a.total+float64(f.Total), a.n+1
		}
		for _, r := range s.Net {
			if net[r.Iface] == nil {
				net[r.Iface] = &netSum{}
			}
			a := net[r.Iface]
			a.rx, a.tx, a.n = a.rx+r.RxBps, a.tx+r.TxBps, a.n+1
		}
	}
	avg := Sample{TS: start, CPU: cpu / n, MemUsed: bytesOf(memUsed / n), MemTotal: bytesOf(memTotal / n),
		SwapUsed: bytesOf(swapUsed / n), SwapTotal: bytesOf(swapTotal / n), Load1: l1 / n, Load5: l5 / n, Load15: l15 / n,
		FS: []FSUsage{}, Net: []NetRate{}}
	for _, m := range sortedKeys(fs) {
		a := fs[m]
		avg.FS = append(avg.FS, FSUsage{Mount: m, Used: bytesOf(a.used / a.n), Total: bytesOf(a.total / a.n)})
	}
	for _, name := range sortedKeys(net) {
		a := net[name]
		avg.Net = append(avg.Net, NetRate{Iface: name, RxBps: a.rx / a.n, TxBps: a.tx / a.n})
	}
	return avg
}

func bytesOf(v float64) uint64 { return uint64(math.Round(v)) }

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
