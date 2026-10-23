package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sys/unix"

	"github.com/darkraise/darkpanel/internal/agent/api"
	"github.com/darkraise/darkpanel/internal/agent/runtime"
	"github.com/darkraise/darkpanel/internal/agent/socket"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/install"
	"github.com/darkraise/darkpanel/internal/paths"
	"github.com/darkraise/darkpanel/internal/transport"
	"github.com/darkraise/darkpanel/internal/update"
)

// UnenrolledHostID is the runtime's host id until the agent enrolls. It
// cannot be a real host id, which is an upper-case ULID.
const UnenrolledHostID = "unenrolled"

// CredentialPoll is how often an agent without credentials looks for the
// generation an enrollment writes.
const CredentialPoll = 5 * time.Second

// Recoverer is the update library's recovery; *update.Updater implements it.
type Recoverer interface {
	Recover(ctx context.Context, mode update.RecoverMode, tx string) error
}

// Config is what darkpanel agent run needs.
type Config struct {
	Paths paths.Paths
	// Runtime is the agent runtime's configuration. The daemon sets HostID
	// and ControllerConnected; Clock and Log are also the daemon's.
	Runtime runtime.Config
	// Updater runs Recover(RecoverStart) at start.
	Updater Recoverer
	// AllowPeer is the agent socket's peer check; nil admits root alone.
	AllowPeer func(uid uint32) bool
	// Heartbeat paces the agent's pings of the controller; the zero value is
	// transport's defaults.
	Heartbeat transport.HeartbeatConfig
	// NeverConnected makes Hello report no live controller session while
	// enrolled, whatever the session's state, for a build that must fail an
	// update's health check; false in production.
	NeverConnected bool
}

func (c Config) check() error {
	switch {
	case c.Paths.AgentDir == "" || c.Paths.AgentSocket == "" || c.Paths.AgentTLSDir == "":
		return errors.New("daemon: Paths.AgentDir, AgentSocket and AgentTLSDir are required")
	case c.Updater == nil:
		return errors.New("daemon: Config.Updater is required")
	case c.Runtime.Clock == nil || c.Runtime.Log == nil:
		return errors.New("daemon: Config.Runtime.Clock and Log are required")
	}
	return nil
}

// errIdentityChanged ends the dial loop when the current generation names
// another host than the runtime serves, or the credentials appeared or went.
var errIdentityChanged = errors.New("daemon: the agent's identity changed")

// identity is who the runtime serves as.
type identity struct {
	hostID   string
	enrolled bool
}

func identityOf(c *Credentials) identity {
	if c == nil {
		return identity{hostID: UnenrolledHostID}
	}
	return identity{hostID: c.HostID(), enrolled: true}
}

type daemon struct {
	cfg       Config
	log       *slog.Logger
	clock     core.Clock
	connected atomic.Bool // a controller session is live

	// mu is held for reading by every request on the socket or a session,
	// and for writing while the runtime is replaced.
	mu    sync.RWMutex
	ident identity
	rt    *runtime.Runtime
	sock  http.Handler
}

// Run is darkpanel agent run: it takes the agent lock, opens the runtime as
// the host the credentials name, runs the update library's start recovery,
// serves the agent socket, and keeps one session to the controller until ctx
// ends, when it returns nil. When an enrollment writes credentials for
// another host, it reopens the runtime as that host. Only the dial loop waits
// on the controller (R28).
func Run(ctx context.Context, cfg Config) error {
	if err := cfg.check(); err != nil {
		return err
	}
	unlock, err := lockAgent(cfg.Paths.AgentDir)
	if err != nil {
		return err
	}
	defer unlock()
	d := &daemon{cfg: cfg, log: cfg.Runtime.Log, clock: cfg.Runtime.Clock}
	c, err := LoadCredentials(cfg.Paths.AgentTLSDir)
	if err != nil && !errors.Is(err, ErrNotEnrolled) {
		d.log.Error("daemon: the saved credentials cannot be read; serving as unenrolled", "error", err)
	}
	if err := d.open(ctx, identityOf(c)); err != nil {
		return err
	}
	defer func() { d.rt.Close() }()
	// A runner may hold the update lock while it stages a binary, so a
	// failed recovery is logged and the agent keeps starting: exiting would
	// start a recover pass through OnFailure= in the middle of the update.
	if err := cfg.Updater.Recover(ctx, update.RecoverStart, ""); err != nil {
		d.log.Warn("daemon: update recovery at start", "error", err)
	}
	srv, err := socket.Listen(socket.Config{Path: cfg.Paths.AgentSocket, Handler: d, Log: d.log, Allow: cfg.AllowPeer})
	if err != nil {
		return fmt.Errorf("daemon: %w", err)
	}
	served := make(chan error, 1)
	go func() { served <- srv.Serve() }()
	defer func() {
		_ = srv.Close()
		if err := <-served; err != nil {
			d.log.Error("daemon: agent socket", "error", err)
		}
	}()
	for {
		err := d.dialLoop(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if !errors.Is(err, errIdentityChanged) {
			return err
		}
		c, err := LoadCredentials(cfg.Paths.AgentTLSDir)
		if err != nil && !errors.Is(err, ErrNotEnrolled) {
			continue
		}
		if err := d.reopen(ctx, identityOf(c)); err != nil {
			return err
		}
	}
}

// lockAgent takes the exclusive lock uninstall checks and
// returns its release.
func lockAgent(dir string) (func(), error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("daemon: create %s: %w", dir, err)
	}
	path := filepath.Join(dir, install.AgentLockName)
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return nil, fmt.Errorf("daemon: open %s: %w", path, err)
	}
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = unix.Close(fd)
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, fmt.Errorf("daemon: another agent runs and holds %s", path)
		}
		return nil, fmt.Errorf("daemon: lock %s: %w", path, err)
	}
	return func() { _ = unix.Close(fd) }, nil
}

// open opens the runtime as id and builds the socket handler over it.
func (d *daemon) open(ctx context.Context, id identity) error {
	rc := d.cfg.Runtime
	rc.HostID = id.hostID
	rc.ControllerConnected = func() *bool {
		if !id.enrolled {
			return nil
		}
		v := d.connected.Load() && !d.cfg.NeverConnected
		return &v
	}
	rt, err := runtime.Open(ctx, rc)
	if err != nil {
		return fmt.Errorf("daemon: %w", err)
	}
	d.ident, d.rt = id, rt
	d.sock = api.NewSocketHandler(api.Config{Agent: rt, Log: d.log}, api.SocketConfig{
		Identity: func() (string, bool) { return id.hostID, id.enrolled },
	})
	d.log.Info("daemon: serving", "host", id.hostID, "enrolled", id.enrolled)
	return nil
}

// reopen replaces the runtime once no request runs on it.
func (d *daemon) reopen(ctx context.Context, id identity) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.rt.Close()
	return d.open(ctx, id)
}

// ServeHTTP serves the agent socket.
func (d *daemon) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	d.sock.ServeHTTP(w, r)
}

// dialLoop keeps one session to the controller the credentials name,
// redialing with transport.Backoff, until ctx ends or the identity changes.
func (d *daemon) dialLoop(ctx context.Context) error {
	b := transport.NewBackoff()
	for {
		c, err := LoadCredentials(d.cfg.Paths.AgentTLSDir)
		var delay time.Duration
		switch {
		case errors.Is(err, ErrNotEnrolled) && !d.ident.enrolled:
			delay = CredentialPoll
		case err == nil && c.HostID() == d.ident.hostID && d.ident.enrolled:
			if err := d.session(ctx, c, b); err != nil && ctx.Err() == nil {
				d.log.Warn("daemon: controller session", "error", err)
			}
			delay = b.Next()
		case err == nil, errors.Is(err, ErrNotEnrolled):
			return errIdentityChanged
		default:
			d.log.Error("daemon: read the credentials", "error", err)
			delay = b.Next()
		}
		if err := sleep(ctx, d.clock, delay); err != nil {
			return err
		}
	}
}

// session connects with c and serves the agent API on the session until it
// ends, or until the certificate was renewed over it. A session the
// controller accepted resets the backoff and deletes the credential
// generations older than c's.
func (d *daemon) session(ctx context.Context, c *Credentials, b *transport.Backoff) error {
	dialCtx, cancel := context.WithTimeout(ctx, DialTimeout)
	sess, err := connect(dialCtx, c)
	cancel()
	if err != nil {
		return err
	}
	b.Reset()
	if err := PruneGenerations(d.cfg.Paths.AgentTLSDir, c.Generation); err != nil {
		d.log.Warn("daemon: delete old credentials", "error", err)
	}
	d.connected.Store(true)
	defer d.connected.Store(false)
	d.log.Info("daemon: connected to the controller", "address", c.Controller.Address, "generation", c.Generation)

	sctx, stop := context.WithCancel(ctx)
	defer stop()
	srv := &http.Server{
		Handler:           d.guard(api.NewHandler(api.Config{Agent: d.rt, Log: d.log, OnHello: d.onHello})),
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          slog.NewLogLogger(d.log.Handler(), slog.LevelWarn),
	}
	renewed := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		d.renewLoop(sctx, sess, c, renewed)
	}()
	go func() {
		defer wg.Done()
		_ = srv.Serve(sess)
	}()
	go func() {
		defer wg.Done()
		// The controller's heartbeat takes the host offline; the agent's
		// own notices a controller that vanished without closing the
		// connection, so it redials instead of waiting on a dead session.
		if err := transport.Heartbeat(sctx, sess, d.cfg.Heartbeat); err != nil {
			d.log.Warn("daemon: the controller stopped answering", "error", err)
		}
	}()
	select {
	case <-sess.Done():
	case <-ctx.Done():
	case <-renewed:
		// Reconnecting proves the new generation now, while the previous
		// one is still on disk, and lets the controller revoke the old
		// serial at the first handshake with the new one.
		d.log.Info("daemon: reconnecting with the renewed certificate")
	}
	stop()
	_ = srv.Close()
	_ = sess.Close()
	wg.Wait()
	d.log.Info("daemon: disconnected from the controller")
	return nil
}

// guard holds mu for reading around each request, so the runtime is never
// replaced under one. A module stream may run for hours, so a stream
// request holds mu only until its answer starts: by then the runtime has
// opened the stream, and a replacement's Runtime.Close ends it.
func (d *daemon) guard(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d.mu.RLock()
		if !api.OpensStream(r) {
			defer d.mu.RUnlock()
			h.ServeHTTP(w, r)
			return
		}
		sw := &unlockOnAnswer{ResponseWriter: w, unlock: sync.OnceFunc(d.mu.RUnlock)}
		defer sw.unlock()
		h.ServeHTTP(sw, r)
	})
}

// unlockOnAnswer calls unlock once the answer's header or first byte is
// written.
type unlockOnAnswer struct {
	http.ResponseWriter
	unlock func()
}

func (u *unlockOnAnswer) WriteHeader(code int) {
	u.unlock()
	u.ResponseWriter.WriteHeader(code)
}

func (u *unlockOnAnswer) Write(b []byte) (int, error) {
	u.unlock()
	return u.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController flush the stream through it.
func (u *unlockOnAnswer) Unwrap() http.ResponseWriter { return u.ResponseWriter }

func (d *daemon) onHello(h api.ControllerHello) {
	d.log.Info("daemon: controller hello", "protocol", h.Protocol, "version", h.Version, "controller", h.ControllerID)
}

// sleep waits d on clock, or until ctx ends.
func sleep(ctx context.Context, clock core.Clock, d time.Duration) error {
	done := make(chan struct{})
	t := clock.AfterFunc(d, func() { close(done) })
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		t.Stop()
		return ctx.Err()
	}
}
