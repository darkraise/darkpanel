package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sync"
	"time"

	"github.com/darkraise/darkpanel/internal/agent/wire"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
	"github.com/darkraise/darkpanel/internal/core/schema"
)

// The close reasons the agent sends on a channel.
const (
	CloseAgentRestarted = "agent restarted" // the runtime closed
	CloseBadFrame       = "bad frame"       // the peer sent a frame the codec refuses
	CloseModuleOff      = "module turned off"
	CloseEnded          = "ended"          // the module's handler returned
	CloseFailed         = "channel failed" // the handler failed; the error is logged
)

// closeWriteWait bounds the close frame's write, so a peer that stopped
// reading cannot hold Close.
const closeWriteWait = time.Second

// ChannelAccepter is a channel connection that has to answer before frames
// flow, as the agent API's upgrade writes its 101: OpenChannel calls Accept
// once the channel passed its checks, before the module's Open.
type ChannelAccepter interface{ Accept() error }

// OpenChannel serves module's channel name with the parameters in params on
// rw, as p, and returns once the channel ended. A channel the registry lacks
// is not_found; a module the policy file denies, whose migrations failed or
// whose enabled flag is 0 is module.unavailable; parameters the channel's
// schema refuses are request.invalid_input; a closed runtime is unavailable.
// These errors come before rw is accepted or used. Once the channel opened,
// OpenChannel returns nil whatever ends it, having closed rw: a handler's
// error is logged and the peer gets the close reason "channel failed".
func (r *Runtime) OpenChannel(ctx context.Context, p core.Principal, module, name string, params url.Values, rw io.ReadWriteCloser) error {
	def, ok := r.cfg.Registry.Channel(module, name)
	if !ok {
		return errcode.New(errcode.NotFound, fmt.Sprintf("no channel %s.%s", module, name))
	}
	if err := r.usable(module + "." + name); err != nil {
		return err
	}
	raw, err := schema.ParamsJSON(def.ParamsType, params)
	if err != nil {
		return err
	}
	if err := streamParams.ValidateType(def.ParamsType, raw); err != nil {
		return err
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	c := &channelConn{rw: rw, cancel: cancel}
	if err := r.addChannel(ctx, module, c); err != nil {
		return err
	}
	defer r.channels.remove(module, c)
	if a, ok := rw.(ChannelAccepter); ok {
		if err := a.Accept(); err != nil {
			_ = rw.Close()
			return fmt.Errorf("runtime: accept channel %s.%s: %w", module, name, err)
		}
	}
	env := r.moduleEnv(module)
	env.Principal = p
	err = lifecycle.Protect(r.cfg.Log, module+"."+name, "channel", func() error {
		return def.Open(runCtx, env, raw, c)
	})
	reason := CloseEnded
	// A peer that went away ends the handler's reads with io.EOF.
	if err != nil && !errors.Is(err, io.EOF) && !c.isClosed() {
		r.cfg.Log.Warn("runtime: channel failed", "channel", module+"."+name, "actor", p.Actor, "error", err)
		reason = CloseFailed
	}
	_ = c.Close(reason)
	return nil
}

// addChannel counts c among module's open channels once the module may open
// one. It runs under enableMu, so a flag change or a policy reload that
// closes the module's channels cannot miss one opening beside it.
func (r *Runtime) addChannel(ctx context.Context, module string, c *channelConn) error {
	r.enableMu.Lock()
	defer r.enableMu.Unlock()
	if r.Policy().Denied(module) {
		return deniedError(module)
	}
	enabled, err := r.moduleEnabled(ctx, module)
	if err != nil {
		return err
	}
	if !enabled {
		return errcode.New(errcode.ModuleUnavailable, fmt.Sprintf("module %s is unavailable: it is disabled on this host", module))
	}
	return r.channels.add(module, c)
}

// CloseModuleChannels closes every open channel of module with the reason
// "module turned off". Each module's Open then sees its context end and its
// reads fail.
func (r *Runtime) CloseModuleChannels(module string) {
	r.channels.closeModule(module, CloseModuleOff)
}

// channelSet is the runtime's open channels by module. Its zero value is
// ready to use.
type channelSet struct {
	mu     sync.Mutex
	open   map[string]map[*channelConn]struct{}
	closed bool
	wg     sync.WaitGroup // one per channel still open
}

func (s *channelSet) add(module string, c *channelConn) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errcode.New(errcode.Unavailable, "the agent runtime is closed")
	}
	if s.open == nil {
		s.open = map[string]map[*channelConn]struct{}{}
	}
	if s.open[module] == nil {
		s.open[module] = map[*channelConn]struct{}{}
	}
	s.open[module][c] = struct{}{}
	s.wg.Add(1)
	return nil
}

func (s *channelSet) remove(module string, c *channelConn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.open[module][c]; ok {
		delete(s.open[module], c)
		s.wg.Done()
	}
}

func (s *channelSet) snapshot(module string) []*channelConn {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*channelConn
	for m, set := range s.open {
		if module != "" && m != module {
			continue
		}
		for c := range set {
			out = append(out, c)
		}
	}
	return out
}

func (s *channelSet) closeModule(module, reason string) {
	for _, c := range s.snapshot(module) {
		_ = c.Close(reason)
	}
}

// closeAll refuses new channels, closes every open one with reason and
// waits until each OpenChannel returned.
func (s *channelSet) closeAll(reason string) {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.closeModule("", reason)
	s.wg.Wait()
}

// channelConn is the agent's end of one channel.
type channelConn struct {
	rw     io.ReadWriteCloser
	cancel context.CancelFunc

	wmu       sync.Mutex // one frame at a time
	closeOnce sync.Once
	mu        sync.Mutex
	closed    bool
}

var _ core.ChannelConn = (*channelConn)(nil)

// errChannelClosed is a write on a closed channel.
var errChannelClosed = errors.New("runtime: the channel is closed")

func (c *channelConn) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

// ReadFrame reads the peer's next frame; the peer's close frame is returned
// like any other. A frame the codec refuses closes the channel with reason
// "bad frame".
func (c *channelConn) ReadFrame() (byte, []byte, error) {
	t, p, err := wire.ReadFrame(c.rw)
	if errors.Is(err, wire.ErrFrame) {
		_ = c.Close(CloseBadFrame)
	}
	return t, p, err
}

func (c *channelConn) WriteFrame(t byte, p []byte) error {
	if c.isClosed() {
		return errChannelClosed
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return wire.WriteFrame(c.rw, t, p)
}

// Close marks the channel closed, ends the handler's context, writes the
// close frame, waiting at most closeWriteWait when rw has write deadlines,
// and closes rw.
func (c *channelConn) Close(reason string) error {
	var err error
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closed = true
		c.mu.Unlock()
		c.cancel()
		if d, ok := c.rw.(interface{ SetWriteDeadline(time.Time) error }); ok {
			_ = d.SetWriteDeadline(time.Now().Add(closeWriteWait))
		}
		payload, _ := json.Marshal(struct {
			Reason string `json:"reason"`
		}{reason})
		c.wmu.Lock()
		_ = wire.WriteFrame(c.rw, wire.FrameClose, payload)
		c.wmu.Unlock()
		err = c.rw.Close()
	})
	return err
}
