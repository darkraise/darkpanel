// Package metrics is the host metrics module (M10 spec section 5.1): an
// agent-side sampler of /proc and statfs every 15 s, one hour of samples in
// memory, and the read of their minute averages.
package metrics

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	"github.com/darkraise/darkpanel/internal/core"
)

// ID is the module id.
const ID = "metrics"

// sampleEvery is the sampler's interval.
const sampleEvery = 15 * time.Second

// Source is where the sampler reads the host; tests pass fixtures. FS is
// rooted at /, so its paths have no leading slash.
type Source struct {
	FS     fs.FS
	Statfs func(path string) (used, total uint64, err error)
}

// New returns the module sampling src.
func New(src Source) core.Module {
	r := newRing()
	return module{s: &sampler{src: src, ring: r}}
}

// Module returns the module sampling this host.
func Module() core.Module { return New(Source{FS: os.DirFS("/"), Statfs: statfs}) }

func statfs(path string) (uint64, uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, 0, fmt.Errorf("statfs %s: %w", path, err)
	}
	bsize := uint64(st.Bsize) //nolint:gosec // G115: the kernel reports a positive block size
	return (st.Blocks - st.Bfree) * bsize, st.Blocks * bsize, nil
}

type module struct{ s *sampler }

var _ core.Background = module{}

// Manifest declares the module: one agent read, no tables. It is enabled by
// default, so hosts enrolled before it existed, which store no flag, run it.
func (m module) Manifest() core.Manifest {
	return core.Manifest{
		ID:             ID,
		Version:        1,
		DefaultEnabled: true,
		Actions:        []core.ActionDef{samplesAction(m.s.ring)},
	}
}

// BackgroundTasks is the sampler.
func (m module) BackgroundTasks() []core.BackgroundTask {
	return []core.BackgroundTask{{ID: ID + ".sample", Every: sampleEvery, Run: m.s.pass}}
}

// sampler takes one sample per pass. CPU and network figures are rates
// between two passes, so the first pass only records the counters.
type sampler struct {
	src  Source
	ring *ring

	mu      sync.Mutex
	primed  bool
	prevAt  time.Time
	prevCPU cpuTimes
	prevNet map[string]netCounters
}

func (s *sampler) read(name string) ([]byte, error) {
	b, err := fs.ReadFile(s.src.FS, name)
	if err != nil {
		return nil, fmt.Errorf("metrics: read /%s: %w", name, err)
	}
	return b, nil
}

func (s *sampler) pass(_ context.Context, tc core.TaskContext) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	files := map[string][]byte{}
	for _, name := range []string{"proc/stat", "proc/meminfo", "proc/loadavg", "proc/net/dev", "proc/self/mounts"} {
		b, err := s.read(name)
		if err != nil {
			return err
		}
		files[name] = b
	}
	now := tc.Env.Clock.Now().UTC()
	cpu, err := parseProcStat(files["proc/stat"])
	if err != nil {
		return fmt.Errorf("metrics: %w", err)
	}
	mem, err := parseMeminfo(files["proc/meminfo"])
	if err != nil {
		return fmt.Errorf("metrics: %w", err)
	}
	load, err := parseLoadavg(files["proc/loadavg"])
	if err != nil {
		return fmt.Errorf("metrics: %w", err)
	}
	net, err := parseNetDev(files["proc/net/dev"])
	if err != nil {
		return fmt.Errorf("metrics: %w", err)
	}
	mounts, err := parseMounts(files["proc/self/mounts"])
	if err != nil {
		return fmt.Errorf("metrics: %w", err)
	}
	prevAt, prevCPU, prevNet, primed := s.prevAt, s.prevCPU, s.prevNet, s.primed
	s.prevAt, s.prevCPU, s.prevNet, s.primed = now, cpu, net, true
	busy, ok := cpuBusy(prevCPU, cpu)
	if !primed || !ok || !now.After(prevAt) {
		return nil
	}
	sample := Sample{TS: now, CPU: busy, MemUsed: mem.used, MemTotal: mem.total, SwapUsed: mem.swapUsed,
		SwapTotal: mem.swapTotal, Load1: load[0], Load5: load[1], Load15: load[2], FS: []FSUsage{},
		Net: netRates(prevNet, net, now.Sub(prevAt).Seconds())}
	for _, m := range mounts {
		// A mount that cannot be measured (gone, or not readable) is left out.
		if used, total, err := s.src.Statfs(m); err == nil {
			sample.FS = append(sample.FS, FSUsage{Mount: m, Used: used, Total: total})
		}
	}
	s.ring.add(sample)
	return nil
}
