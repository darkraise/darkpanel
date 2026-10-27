package firewall

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
)

// sysctlRecord is what a change's journal says about the kernel's sysctl
// values: whether the change started UFW, whose start applies UFW's sysctl
// file, and the values the change wrote itself (a restore's, spec 2.5).
type sysctlRecord struct {
	started bool
	written map[string]string // /proc/sys key to the value its effect observed
}

// sysctlRecordOf reads a record from the effects of one change. Only made
// effects count; a change started UFW when its last made effect on the
// enabled state left UFW enabled (an enable, or a reload).
func sysctlRecordOf(effects []core.Effect) sysctlRecord {
	r := sysctlRecord{written: map[string]string{}}
	for _, e := range effects {
		if e.State != core.EffectMade || e.Post == nil {
			continue
		}
		noun, ident, err := splitResource(e.Resource)
		if err != nil {
			continue
		}
		switch noun {
		case nounState:
			var enabled bool
			if json.Unmarshal(e.Post.State, &enabled) == nil {
				r.started = enabled
			}
		case nounSysctl:
			var value *string
			if json.Unmarshal(e.Post.State, &value) == nil && value != nil {
				r.written[ident] = *value
			}
		}
	}
	return r
}

// expectedSysctl returns the supported sysctl values a change's transition
// sets, by /proc/sys key (spec 2.1, 2.3): given the configuration cfg
// after the change and the live values before it, a start predicts every
// key UFW's sysctl file sets and every key the kernel changes as a side
// effect, but not the keys the start leaves alone, which other programs
// may change at any time. The values the change wrote itself replace the
// prediction.
func (f fw) expectedSysctl(cfg fwstate.Config, before map[string]string, r sysctlRecord) (map[string]string, error) {
	want := map[string]string{}
	if r.started {
		pred, err := f.d.sysctl(cfg, before, fwstate.TransEnable)
		if err != nil {
			return nil, fmt.Errorf("firewall: predict the sysctl values: %w", err)
		}
		inFile := make(map[string]bool, len(cfg.Sysctl))
		for _, s := range cfg.Sysctl {
			inFile[s.Key] = true
		}
		for k, v := range pred {
			if inFile[k] || v != before[k] {
				want[k] = v
			}
		}
	}
	maps.Copy(want, r.written)
	return want, nil
}

// sysctlMismatches describes, sorted by key, each predicted value that the
// live one differs from. A key the kernel no longer has belongs to an
// interface that has gone and is skipped.
func sysctlMismatches(want, live map[string]string) []string {
	var out []string
	for _, k := range slices.Sorted(maps.Keys(want)) {
		if got, ok := live[k]; ok && got != want[k] {
			out = append(out, fmt.Sprintf("%s: live %q, predicted %q", k, got, want[k]))
		}
	}
	return out
}
