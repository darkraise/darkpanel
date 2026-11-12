package server

import (
	"github.com/darkraise/darkpanel/internal/controller/listen"
	"github.com/darkraise/darkpanel/internal/core"
)

// Listeners are the network listeners currently bound.
func (s *Server) Listeners() []listen.Bound { return s.listenService.State().Listeners }

// AgentListeners are the agent listener's bound sockets: none with the
// embedded agent, and none after Shutdown.
func (s *Server) AgentListeners() []listen.Bound {
	if s.agentListen == nil {
		return nil
	}
	return s.agentListen.Bound()
}

// LocalHostID is the id of this host's core_hosts row.
func (s *Server) LocalHostID() string { return s.localHostID }

// SocketPath is the controller socket's path.
func (s *Server) SocketPath() string { return s.opts.Paths.ControllerSocket }

// Registry is the server's registry, built with its services.
func (s *Server) Registry() *core.Registry { return s.registry }
