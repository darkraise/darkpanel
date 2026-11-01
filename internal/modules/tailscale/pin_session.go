package tailscale

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"time"

	"github.com/darkraise/darkpanel/internal/agent/api"
	"github.com/darkraise/darkpanel/internal/agent/daemon"
	"github.com/darkraise/darkpanel/internal/modules/tailscale/link"
	"github.com/darkraise/darkpanel/internal/paths"
)

// pinTxProbe bounds every single probe of tailscaled or of the agent's controller session.
const pinTxProbe = 5 * time.Second

// agentSession is the agent's controller session on an installed host, which is where the pin's transient unit
// runs.
func agentSession(ctx context.Context) (up, viaTailnet bool, err error) {
	return sessionAt(ctx, paths.Production())
}

// sessionAt reads the agent's controller session from outside the agent process: the enrolled controller
// address from the agent's credentials, and whether the session is up from the agent socket's hello. viaTailnet
// reports whether the controller address is a Tailscale address; an agent that is not enrolled has no session.
func sessionAt(ctx context.Context, p paths.Paths) (up, viaTailnet bool, err error) {
	creds, err := daemon.LoadCredentials(p.AgentTLSDir)
	if errors.Is(err, daemon.ErrNotEnrolled) {
		return false, false, nil
	}
	if err != nil {
		return false, false, fmt.Errorf("tailscale: read the agent credentials: %w", err)
	}
	if host, _, err := net.SplitHostPort(creds.Controller.Address); err == nil {
		if addr, err := netip.ParseAddr(host); err == nil {
			viaTailnet = link.IsTailscaleAddr(addr)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, pinTxProbe)
	defer cancel()
	client := api.NewClient(api.ClientConfig{Dial: func(ctx context.Context) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", p.AgentSocket)
	}})
	hello, err := client.Hello(ctx)
	if err != nil {
		return false, viaTailnet, fmt.Errorf("tailscale: ask the agent about its controller session: %w", err)
	}
	return hello.ControllerConnected != nil && *hello.ControllerConnected, viaTailnet, nil
}
