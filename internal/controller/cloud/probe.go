package cloud

import (
	"context"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// The states of a probed capability.
const (
	CapabilityOK         = "ok"
	CapabilityMissing    = "missing"
	CapabilityUnverified = "unverified"
)

// Capability is one probed provider capability.
type Capability struct {
	Name   string `json:"name"`
	State  string `json:"state"`            // "ok" | "missing" | "unverified"
	Detail string `json:"detail,omitempty"` // a missing capability: the error code
}

// Probe is one harmless read that proves a read capability.
type Probe struct {
	Name string
	Read func(ctx context.Context) error
}

// ProbeReads runs every probe and reports each as ok or missing, then
// appends writes as unverified, in the order given. A write capability is
// known only once a write succeeds, so connecting an account never writes.
func ProbeReads(ctx context.Context, probes []Probe, writes []string) []Capability {
	out := make([]Capability, 0, len(probes)+len(writes))
	for _, p := range probes {
		if err := p.Read(ctx); err != nil {
			out = append(out, Capability{Name: p.Name, State: CapabilityMissing, Detail: string(errcode.From(err).Code)})
			continue
		}
		out = append(out, Capability{Name: p.Name, State: CapabilityOK})
	}
	for _, w := range writes {
		out = append(out, Capability{Name: w, State: CapabilityUnverified})
	}
	return out
}
