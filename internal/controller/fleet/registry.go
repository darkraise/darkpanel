package fleet

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/darkraise/darkpanel/internal/agent/api"
	agentruntime "github.com/darkraise/darkpanel/internal/agent/runtime"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/transport"
)

// LastSeenInterval is the shortest time between two writes of one host's
// core_hosts.last_seen_at (spec section 4), so a busy session does not
// queue a database write per request.
const LastSeenInterval = 15 * time.Second

// RegistryConfig configures NewRegistry.
type RegistryConfig struct {
	DB    *store.DB // core_hosts.last_seen_at
	Clock core.Clock
	// CheckPeer is Authority.CheckPeer: Register checks the session's serial
	// again with it.
	CheckPeer transport.PeerCheck
	// Version and ControllerID are the controller's half of every hello.
	Version      string
	ControllerID string
	// Heartbeat tunes each session's heartbeat: a zero field is the spec's
	// value and a negative one is refused. OnAlive is ignored.
	Heartbeat transport.HeartbeatConfig
	// Log receives sessions starting and ending; nil discards.
	Log *slog.Logger
	// OnOnline receives true when a host gets its first live session and
	// false when the host's live session ends; a session replaced by a newer
	// one reports nothing. It runs under the registry's lock, so the calls
	// of one host arrive in order; it must return at once and must not call
	// the registry. Nil drops them.
	OnOnline func(hostID string, online bool)
}

// Registry holds the live agent session of each host and is the
// controller's ClientSource. Online state is the registry's alone and is
// never stored, so every host is offline when the controller starts. Its
// methods are safe for concurrent use.
type Registry struct {
	cfg    RegistryConfig
	ctx    context.Context
	cancel context.CancelFunc

	mu         sync.Mutex
	live       map[string]*liveSession
	written    map[string]time.Time // the last last_seen_at written per host
	generation uint64
	closed     bool

	wg     sync.WaitGroup // heartbeats and session watchers
	writes sync.WaitGroup // last_seen_at writes
}

type liveSession struct {
	hostID     string
	session    *transport.Session
	client     *api.Client
	generation uint64
}

// NewRegistry returns a registry with no sessions.
func NewRegistry(cfg RegistryConfig) (*Registry, error) {
	hb := cfg.Heartbeat
	switch {
	case cfg.DB == nil || cfg.Clock == nil || cfg.CheckPeer == nil:
		return nil, errors.New("fleet: the registry needs a database, a clock and a peer check")
	case cfg.Version == "" || cfg.ControllerID == "":
		return nil, errors.New("fleet: the registry needs the controller's version and id for the hello")
	case hb.Interval < 0 || hb.Timeout < 0 || hb.Failures < 0:
		return nil, errors.New("fleet: heartbeat settings must be zero or positive")
	}
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	if cfg.OnOnline == nil {
		cfg.OnOnline = func(string, bool) {}
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Registry{
		cfg: cfg, ctx: ctx, cancel: cancel,
		live: map[string]*liveSession{}, written: map[string]time.Time{},
	}, nil
}

// Register makes s hostID's live session. The caller has authenticated s's
// peer as hostID with the certificate serial. Register checks serial again
// while it holds the lock Disconnect takes: a revocation that committed
// after the handshake's check and whose Disconnect found no session yet is
// seen here, so a revoked host never keeps a session. A refused or late
// session (after Close) is closed and its error returned. An earlier
// session of the host is closed, and s leaves the registry when it ends,
// whether its peer, its heartbeat, Disconnect or Close ended it.
func (r *Registry) Register(ctx context.Context, hostID, serial string, s *transport.Session) error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		_ = s.Close()
		return errors.New("fleet: the registry is closed")
	}
	if err := r.cfg.CheckPeer(ctx, hostID, serial); err != nil {
		r.mu.Unlock()
		_ = s.Close()
		return fmt.Errorf("fleet: register host %s: %w", hostID, err)
	}
	r.generation++
	ls := &liveSession{hostID: hostID, session: s, generation: r.generation}
	ls.client = api.NewClient(api.ClientConfig{
		Dial: func(ctx context.Context) (net.Conn, error) {
			c, err := s.Open(ctx)
			if err != nil {
				return nil, err
			}
			return &seenConn{Conn: c, seen: func() { r.seen(hostID) }}, nil
		},
		Controller: &api.ControllerHello{
			Protocol: agentruntime.ProtocolVersion, Version: r.cfg.Version, ControllerID: r.cfg.ControllerID,
		},
	})
	old := r.live[hostID]
	r.live[hostID] = ls
	if old == nil {
		r.cfg.OnOnline(hostID, true)
	}
	r.wg.Add(2)
	r.mu.Unlock()

	if old != nil {
		_ = old.session.Close()
		r.cfg.Log.Info("fleet: a new session replaced the host's session", "host", hostID)
	} else {
		r.cfg.Log.Info("fleet: host online", "host", hostID)
	}
	go r.heartbeat(ls)
	go r.watch(ls)
	return nil
}

func (r *Registry) heartbeat(ls *liveSession) {
	defer r.wg.Done()
	cfg := r.cfg.Heartbeat
	cfg.OnAlive = func() { r.seen(ls.hostID) }
	if err := transport.Heartbeat(r.ctx, ls.session, cfg); err != nil {
		r.cfg.Log.Warn("fleet: the agent stopped answering; its session is closed", "host", ls.hostID, "err", err)
	}
}

// watch removes ls when its session ends, unless a newer session has
// replaced it already.
func (r *Registry) watch(ls *liveSession) {
	defer r.wg.Done()
	<-ls.session.Done()
	r.mu.Lock()
	current := r.live[ls.hostID] == ls
	if current {
		delete(r.live, ls.hostID)
		r.cfg.OnOnline(ls.hostID, false)
	}
	r.mu.Unlock()
	if current {
		r.cfg.Log.Info("fleet: host offline", "host", ls.hostID)
	}
}

// Client returns hostID's agent client and its session's generation, which
// is positive and new for every session. A host without a live session
// answers host.offline.
func (r *Registry) Client(_ context.Context, hostID string) (core.AgentClient, uint64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ls, ok := r.live[hostID]
	if !ok {
		return nil, 0, errcode.New(errcode.HostOffline, fmt.Sprintf("host %s is offline", hostID))
	}
	return ls.client, ls.generation, nil
}

// Disconnect closes hostID's live session, if any. A revoked or deleted
// host calls it after its transaction commits, never inside it: Register's
// check must be able to see the committed revocation. The handshake
// refuses the host's redials.
func (r *Registry) Disconnect(hostID string) {
	r.mu.Lock()
	ls := r.live[hostID]
	delete(r.live, hostID)
	if ls != nil {
		r.cfg.OnOnline(hostID, false)
	}
	r.mu.Unlock()
	if ls != nil {
		_ = ls.session.Close()
		r.cfg.Log.Info("fleet: host disconnected", "host", hostID)
	}
}

// Close closes every session and waits for the registry's goroutines;
// later sessions are closed as they register.
func (r *Registry) Close() {
	r.mu.Lock()
	r.closed = true
	live := r.live
	r.live = map[string]*liveSession{}
	for hostID := range live {
		r.cfg.OnOnline(hostID, false)
	}
	r.mu.Unlock()
	r.cancel()
	for _, ls := range live {
		_ = ls.session.Close()
	}
	r.wg.Wait()
	r.writes.Wait()
}

// seen records that hostID answered: it writes last_seen_at unless the
// last write is younger than LastSeenInterval. The write runs on its own
// goroutine, so neither a request nor the heartbeat waits for the
// database.
func (r *Registry) seen(hostID string) {
	now := r.cfg.Clock.Now()
	r.mu.Lock()
	if last, ok := r.written[hostID]; r.closed || ok && now.Sub(last) < LastSeenInterval {
		r.mu.Unlock()
		return
	}
	r.written[hostID] = now
	r.writes.Add(1)
	r.mu.Unlock()
	go func() {
		defer r.writes.Done()
		err := r.cfg.DB.Write(r.ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(r.ctx, `UPDATE core_hosts SET last_seen_at = ? WHERE id = ?`, formatTime(now), hostID)
			return err
		})
		if err != nil {
			r.cfg.Log.Warn("fleet: cannot record when the host was last seen", "host", hostID, "err", err)
		}
	}()
}

// seenConn reports the first bytes the agent sends on one stream: an
// answer to a request.
type seenConn struct {
	net.Conn
	once sync.Once
	seen func()
}

func (c *seenConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.once.Do(c.seen)
	}
	return n, err
}
