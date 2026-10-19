package dispatch

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// ClientSource yields the current agent client of a host.
type ClientSource interface {
	// Client returns the host's client and a generation that changes whenever
	// the host's session is replaced; Hosts re-runs Hello when it changes.
	// A host with no live session answers errcode.HostOffline.
	Client(ctx context.Context, hostID string) (core.AgentClient, uint64, error)
}

// generations numbers every client a StaticClients is given. One counter for
// the process keeps a generation from ever repeating, so a Hosts never
// mistakes a new client for one it already greeted.
var generations atomic.Uint64

// StaticClients is a ClientSource over clients set by Add: the in-process
// agent of --embedded-agent, and tests. Its methods are safe for concurrent
// use.
type StaticClients struct {
	mu      sync.Mutex
	clients map[string]staticClient
}

type staticClient struct {
	client     core.AgentClient
	generation uint64
}

var _ ClientSource = (*StaticClients)(nil)

// NewStaticClients returns a source with no clients.
func NewStaticClients() *StaticClients {
	return &StaticClients{clients: map[string]staticClient{}}
}

// Add sets hostID's client. Every call starts a new generation, so Hosts says
// Hello again, as it does after an agent reconnects.
func (s *StaticClients) Add(hostID string, c core.AgentClient) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clients[hostID] = staticClient{client: c, generation: generations.Add(1)}
}

// Client returns hostID's client and its generation; a host without a client
// is offline.
func (s *StaticClients) Client(_ context.Context, hostID string) (core.AgentClient, uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sc, ok := s.clients[hostID]
	if !ok {
		return nil, 0, errcode.New(errcode.HostOffline, fmt.Sprintf("host %s is offline", hostID))
	}
	return sc.client, sc.generation, nil
}
