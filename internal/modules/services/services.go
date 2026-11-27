// Package services is the services and journal module (M11 spec section 2): systemd units and the journal.
package services

import (
	"context"
	"fmt"
	"os"
	"slices"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/tailscale/link"
	"github.com/darkraise/darkpanel/internal/platform/netstat"
)

// ID is the module id.
const ID = "services"

// module carries what the rules read of the host besides systemd.
type module struct {
	// agentOnTailnet reports whether the agent's own connection to its
	// controller runs over the Tailscale interface.
	agentOnTailnet func(ctx context.Context) (bool, error)
	journal        *journalState
}

// Module returns the services module.
func Module() core.Module {
	return module{agentOnTailnet: liveAgentOnTailnet, journal: &journalState{}}
}

// Manifest declares the module.
func (m module) Manifest() core.Manifest {
	return core.Manifest{ID: ID, Version: 1, DefaultEnabled: true, Actions: slices.Concat(m.reads(), m.journalReads(), m.mutations())}
}

// liveAgentOnTailnet looks for an established TCP connection this process
// opened from a Tailscale address: the agent dials its controller, and a
// connection routed through tailscale0 leaves from the node's address.
func liveAgentOnTailnet(context.Context) (bool, error) {
	return agentOnTailnet(netstat.Sockets, os.Getpid())
}

// agentOnTailnet is liveAgentOnTailnet over the socket table sockets
// returns, for the process pid.
func agentOnTailnet(sockets func() ([]netstat.Socket, error), pid int) (bool, error) {
	socks, err := sockets()
	if err != nil {
		return false, fmt.Errorf("services: read the socket table: %w", err)
	}
	listening := map[uint16]bool{}
	for _, s := range socks {
		if s.Proto == netstat.ProtoTCP && s.State == netstat.StateListen {
			listening[s.Local.Port()] = true
		}
	}
	for _, s := range socks {
		if s.Proto != netstat.ProtoTCP || s.State != netstat.StateEstablished || listening[s.Local.Port()] {
			continue
		}
		for _, p := range s.Processes {
			if p.PID == pid && link.IsTailscaleAddr(s.Local.Addr()) {
				return true, nil
			}
		}
	}
	return false, nil
}
