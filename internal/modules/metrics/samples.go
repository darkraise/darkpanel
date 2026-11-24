package metrics

import (
	"context"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// ActionSamples is the agent-side read of the minute averages.
const ActionSamples = ID + ".samples.get"

// Sample is one sample, or the average of one minute's samples.
type Sample struct {
	TS        time.Time `json:"ts" jsonschema_description:"When the sample was taken; for a minute average, the start of the minute."`
	CPU       float64   `json:"cpu" jsonschema_description:"CPU busy percent across all cores."`
	MemUsed   uint64    `json:"memUsed" jsonschema_description:"Memory in use in bytes: the total less the available memory."`
	MemTotal  uint64    `json:"memTotal"`
	SwapUsed  uint64    `json:"swapUsed"`
	SwapTotal uint64    `json:"swapTotal"`
	Load1     float64   `json:"load1"`
	Load5     float64   `json:"load5"`
	Load15    float64   `json:"load15"`
	FS        []FSUsage `json:"fs" jsonschema_description:"Local filesystems, the first 16 by mount point."`
	Net       []NetRate `json:"net" jsonschema_description:"Interfaces other than lo, the first 16 by name."`
}

// FSUsage is one mounted filesystem's use in bytes.
type FSUsage struct {
	Mount string `json:"mount"`
	Used  uint64 `json:"used"`
	Total uint64 `json:"total"`
}

// NetRate is one interface's receive and transmit bytes per second.
type NetRate struct {
	Iface string  `json:"iface"`
	RxBps float64 `json:"rxBps"`
	TxBps float64 `json:"txBps"`
}

// SamplesInput is the input of metrics.samples.get.
type SamplesInput struct {
	Since string `json:"since,omitempty" jsonschema_description:"RFC 3339; only minutes that start after it. Empty for every minute held."`
}

// SamplesOutput is the output of metrics.samples.get.
type SamplesOutput struct {
	Now     time.Time `json:"now" jsonschema_description:"The agent's clock."`
	Samples []Sample  `json:"samples" jsonschema_description:"The averages of complete minutes after since, oldest first, at most 60."`
	CPUs    int       `json:"cpus" jsonschema_description:"The host's online logical CPUs at the last sample; 0 before the first."`
}

// samplesAction declares metrics.samples.get. It is an ordinary read, not an
// internal one: an internal read journals every call, and the controller
// polls it every minute.
func samplesAction(r *ring) core.ActionDef {
	return core.DefineRead[SamplesInput, SamplesOutput](core.ActionSpec{
		Name:    ActionSamples,
		Summary: "Return the 1-minute averages of the host's recent metrics.",
		Version: 1,
		Side:    core.SideAgent,
	}, samplesHandler{r: r})
}

type samplesHandler struct{ r *ring }

func (h samplesHandler) Read(_ context.Context, env core.Env, in SamplesInput) (SamplesOutput, error) {
	var since time.Time
	if in.Since != "" {
		t, err := time.Parse(time.RFC3339, in.Since)
		if err != nil {
			return SamplesOutput{}, errcode.New(errcode.InvalidInput, "since is not an RFC 3339 time")
		}
		since = t
	}
	now := env.Clock.Now().UTC()
	return SamplesOutput{Now: now, Samples: minuteAverages(h.r.snapshot(), since, now), CPUs: h.r.cpuCount()}, nil
}
