package dispatch

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"slices"
	"sync"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/schemas"
)

// The most module streams open at once (spec 1.6).
const (
	MaxStreamsPerPrincipal = 4
	MaxStreamsPerHost      = 16
)

var _ core.DispatcherStreams = (*Dispatcher)(nil)

// OpenStream opens module's stream name on hostID's agent for the relay
// (spec 1.6). The stream must be registered, its module usable on the
// host, and the agent must serve it with the schema hash of this
// controller's registry; then p, counted by its actor, and the host must
// each be under their limit of open streams. The agent's stream list is
// asked on every open, so an agent updated in between is never relayed
// with stale schemas. Closing the returned reader closes the agent's
// stream and frees both slots.
func (d *Dispatcher) OpenStream(ctx context.Context, p core.Principal, hostID, module, name string, params url.Values) (io.ReadCloser, error) {
	def, ok := d.cfg.Registry.Stream(module, name)
	if !ok {
		return nil, errcode.New(errcode.NotFound, fmt.Sprintf("no stream %s.%s", module, name))
	}
	c, err := d.agent(ctx, hostID, module+"."+name)
	if err != nil {
		return nil, err
	}
	hash, err := schemas.StreamHash(def)
	if err != nil {
		return nil, fmt.Errorf("dispatch: stream %s.%s: %w", module, name, err)
	}
	// The slots are taken before the discovery request, which uses one of
	// the session's action slots: a principal at its limit must not keep
	// costing the host's actions one each.
	release, err := d.streams.take(p.Actor, hostID)
	if err != nil {
		return nil, err
	}
	body, err := openServed(ctx, c, p, hostID, module, name, hash, params)
	if err != nil {
		release()
		return nil, err
	}
	return &heldStream{ReadCloser: body, release: release}, nil
}

// openServed opens the stream once c serves it with hash.
func openServed(ctx context.Context, c core.AgentClient, p core.Principal, hostID, module, name, hash string,
	params url.Values) (io.ReadCloser, error) {
	served, err := c.Streams(ctx)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(served, core.StreamInfo{Module: module, Name: name, SchemaHash: hash}) {
		return nil, errcode.New(errcode.ModuleUnavailable,
			fmt.Sprintf("stream %s.%s on host %s: agent version differs", module, name, hostID))
	}
	return c.OpenStream(ctx, p, module, name, params)
}

// heldStream gives its slots back when it is closed.
type heldStream struct {
	io.ReadCloser
	release func()
}

func (s *heldStream) Close() error {
	err := s.ReadCloser.Close()
	s.release()
	return err
}

// streamSlots counts the open module streams of each principal and each
// host. Its zero value is ready to use.
type streamSlots struct {
	mu          sync.Mutex
	byPrincipal map[string]int
	byHost      map[string]int
}

// take counts one more stream of actor on hostID, or answers
// core.stream_limit when either is at its limit. release gives both slots
// back; calling it again does nothing.
func (s *streamSlots) take(actor, hostID string) (release func(), err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.byPrincipal == nil {
		s.byPrincipal, s.byHost = map[string]int{}, map[string]int{}
	}
	switch {
	case s.byPrincipal[actor] >= MaxStreamsPerPrincipal:
		return nil, errcode.New(errcode.StreamLimit,
			fmt.Sprintf("%s has %d streams open, the most allowed; close one first", actor, MaxStreamsPerPrincipal))
	case s.byHost[hostID] >= MaxStreamsPerHost:
		return nil, errcode.New(errcode.StreamLimit,
			fmt.Sprintf("host %s has %d streams open, the most allowed; close one first", hostID, MaxStreamsPerHost))
	}
	s.byPrincipal[actor]++
	s.byHost[hostID]++
	var once sync.Once
	return func() { once.Do(func() { s.give(actor, hostID) }) }, nil
}

func (s *streamSlots) give(actor, hostID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.byPrincipal[actor]--; s.byPrincipal[actor] == 0 {
		delete(s.byPrincipal, actor)
	}
	if s.byHost[hostID]--; s.byHost[hostID] == 0 {
		delete(s.byHost, hostID)
	}
}
