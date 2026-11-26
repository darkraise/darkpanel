package dispatch

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"slices"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// ReasonChannelUnserved is the module.unavailable message for an agent that
// does not report the channel's module (spec 1.2).
const ReasonChannelUnserved = "Update the agent to use the terminal"

var _ core.DispatcherChannels = (*Dispatcher)(nil)

// channelOpener is an agent client that opens module channels, as
// api.Client does.
type channelOpener interface {
	OpenChannel(ctx context.Context, p core.Principal, module, name string, params url.Values) (io.ReadWriteCloser, error)
}

// OpenChannel opens module's channel name on hostID's agent for p (spec
// 1.2). The channel must be registered; an agent whose last Hello does not
// report the module, or whose client opens no channels, is
// module.unavailable with ReasonChannelUnserved; then the agent must be in
// the support window and the module usable on the host. Closing the
// returned connection closes the agent's channel.
func (d *Dispatcher) OpenChannel(ctx context.Context, p core.Principal, hostID, module, name string, params url.Values) (io.ReadWriteCloser, error) {
	if _, ok := d.cfg.Registry.Channel(module, name); !ok {
		return nil, errcode.New(errcode.NotFound, fmt.Sprintf("no channel %s.%s", module, name))
	}
	c, err := d.cfg.Hosts.Agent(ctx, hostID)
	if err != nil {
		return nil, err
	}
	hello, _ := d.cfg.Hosts.greetingOf(hostID)
	opener, ok := c.(channelOpener)
	if !ok || !slices.ContainsFunc(hello.Modules, func(m core.ModuleStatus) bool { return m.ID == module }) {
		return nil, errcode.New(errcode.ModuleUnavailable, ReasonChannelUnserved)
	}
	if _, err := d.agent(ctx, hostID, module+"."+name); err != nil {
		return nil, err
	}
	return opener.OpenChannel(ctx, p, module, name, params)
}

// ModuleUsable reports whether module is enabled and available on hostID,
// as its stored module state says; the channel bridge asks it at each
// recheck.
func (d *Dispatcher) ModuleUsable(ctx context.Context, hostID, module string) error {
	return d.cfg.Hosts.usable(ctx, hostID, module)
}
