package daemon

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/fleet"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/secret"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/transport"
)

// testController is the agent listener of a controller, built from the real
// fleet.Authority behind transport.Handshake: enrollment connections serve
// one POST transport.EnrollPath, agent connections become controller
// sessions that serve POST transport.RenewPath.
type testController struct {
	t     *testing.T
	db    *store.DB
	clock *coretest.FakeClock
	auth  *fleet.Authority
	ln    net.Listener
	addr  string
	ctx   context.Context
	stop  context.CancelFunc
	wg    sync.WaitGroup

	mu       sync.Mutex
	sessions []*agentSession // every accepted agent session, oldest first
	refused  int             // handshakes that failed
	// rewrite, when set, alters an enrollment answer before it is sent.
	rewrite func(req transport.EnrollRequest, resp *transport.EnrollResponse)
	// renewErr, when set, refuses every renewal.
	renewErr error
	renewals int // renewal requests received
}

// agentSession is one accepted agent connection.
type agentSession struct {
	hostID, serial string
	sess           *transport.Session
}

// newTestController starts a controller whose authority's clock reads start;
// it stops when the test ends.
func newTestController(t *testing.T, start time.Time) *testController {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(dir, "controller.db"))
	if err != nil {
		t.Fatal(err)
	}
	set, err := store.CoreMigrations(store.SideController)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Migrate(ctx, db, store.MigrateOptions{
		Side: store.SideController, BinaryVersion: "test", BackupRoot: filepath.Join(dir, "backups"), Keep: 5, Now: start,
	}, set, nil); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, secret.KeySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	sealer, err := secret.NewSealer(key)
	if err != nil {
		t.Fatal(err)
	}
	clock := coretest.NewFakeClock(start)
	auth, err := fleet.LoadOrCreateAuthority(ctx, db, sealer, clock)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	c := &testController{t: t, db: db, clock: clock, auth: auth, ln: ln, addr: ln.Addr().String()}
	c.ctx, c.stop = context.WithCancel(ctx)
	c.wg.Add(1)
	go c.accept()
	t.Cleanup(func() {
		c.stop()
		_ = ln.Close()
		c.mu.Lock()
		for _, s := range c.sessions {
			_ = s.sess.Close()
		}
		c.mu.Unlock()
		c.wg.Wait()
		_ = db.Close()
	})
	return c
}

func (c *testController) accept() {
	defer c.wg.Done()
	for {
		raw, err := c.ln.Accept()
		if err != nil {
			return
		}
		c.wg.Add(1)
		go func() {
			defer c.wg.Done()
			c.serve(raw)
		}()
	}
}

func (c *testController) serve(raw net.Conn) {
	ctx, cancel := context.WithTimeout(c.ctx, 10*time.Second)
	peer, err := transport.Handshake(ctx, raw, transport.ServerConfig{
		Certificate: c.auth.GetCertificate, AgentRoots: c.auth.Pool(), CheckPeer: c.auth.CheckPeer,
	})
	cancel()
	if err != nil {
		c.mu.Lock()
		c.refused++
		c.mu.Unlock()
		return
	}
	if peer.Protocol == transport.ALPNEnroll {
		c.serveEnroll(peer.Conn)
		return
	}
	if err := c.auth.Handshake(c.ctx, peer.HostID, peer.Serial); err != nil {
		c.t.Errorf("controller: record the handshake: %v", err)
	}
	sess, err := transport.ControllerSession(peer.Conn)
	if err != nil {
		c.t.Errorf("controller: %v", err)
		return
	}
	c.mu.Lock()
	c.sessions = append(c.sessions, &agentSession{hostID: peer.HostID, serial: peer.Serial, sess: sess})
	c.mu.Unlock()
	srv := &http.Server{Handler: c.renewHandler(peer.HostID), ReadHeaderTimeout: 5 * time.Second}
	_ = srv.Serve(sess)
}

// serveEnroll answers the one request of an enrollment connection.
func (c *testController) serveEnroll(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	req, err := http.ReadRequest(bufio.NewReader(conn))
	if err != nil {
		return
	}
	rec := newRecorder()
	var in transport.EnrollRequest
	switch {
	case req.Method != http.MethodPost || req.URL.Path != transport.EnrollPath:
		rec.problem(errcode.New(errcode.NotFound, "no such route"))
	case json.NewDecoder(req.Body).Decode(&in) != nil:
		rec.problem(errcode.New(errcode.InvalidJSON, "bad body"))
	default:
		e, err := c.auth.Enroll(c.ctx, in.Token, []byte(in.CSR))
		if err != nil {
			rec.problem(err)
			break
		}
		out := transport.EnrollResponse{Certificate: string(e.Certificate), CA: string(e.CA)}
		c.mu.Lock()
		rewrite := c.rewrite
		c.mu.Unlock()
		if rewrite != nil {
			rewrite(in, &out)
		}
		rec.json(out)
	}
	_ = rec.response().Write(conn)
}

// renewHandler serves the renewal of hostID's certificate on its session.
func (c *testController) renewHandler(hostID string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := newRecorder()
		var in transport.RenewRequest
		switch {
		case r.Method != http.MethodPost || r.URL.Path != transport.RenewPath:
			rec.problem(errcode.New(errcode.NotFound, "no such route"))
		case json.NewDecoder(r.Body).Decode(&in) != nil:
			rec.problem(errcode.New(errcode.InvalidJSON, "bad body"))
		default:
			c.mu.Lock()
			c.renewals++
			refuse := c.renewErr
			c.mu.Unlock()
			if refuse != nil {
				rec.problem(refuse)
				break
			}
			cert, err := c.auth.Renew(r.Context(), hostID, []byte(in.CSR))
			if err != nil {
				rec.problem(err)
				break
			}
			rec.json(transport.RenewResponse{Certificate: string(cert)})
		}
		for k, v := range rec.header {
			w.Header()[k] = v
		}
		w.WriteHeader(rec.status)
		_, _ = w.Write(rec.body.Bytes())
	})
}

// recorder builds one answer, as P8's endpoints answer: JSON, or problem
// details carrying the errcode code.
type recorder struct {
	status int
	header http.Header
	body   bytes.Buffer
}

func newRecorder() *recorder { return &recorder{status: http.StatusOK, header: http.Header{}} }

func (r *recorder) json(v any) {
	r.header.Set("Content-Type", "application/json")
	_ = json.NewEncoder(&r.body).Encode(v)
}

func (r *recorder) problem(err error) {
	code, detail := errcode.Internal, "internal error"
	var e *errcode.Error
	if errors.As(err, &e) {
		code, detail = e.Code, e.Detail
	}
	r.status = code.Status()
	r.header.Set("Content-Type", "application/problem+json")
	_ = json.NewEncoder(&r.body).Encode(map[string]any{
		"type": "about:blank", "title": code.Title(), "status": r.status, "detail": detail, "code": code,
	})
}

func (r *recorder) response() *http.Response {
	r.header.Set("Content-Length", strconv.Itoa(r.body.Len()))
	return &http.Response{
		StatusCode: r.status, ProtoMajor: 1, ProtoMinor: 1, Header: r.header,
		Body: io.NopCloser(bytes.NewReader(r.body.Bytes())), ContentLength: int64(r.body.Len()), Close: true,
	}
}

// addHost inserts a host row in state.
func (c *testController) addHost(id, state string) {
	c.t.Helper()
	if _, err := c.db.Writer().ExecContext(context.Background(),
		`INSERT INTO core_hosts (id, name, created_at, state) VALUES (?, ?, '2026-10-19T00:00:00.000000000Z', ?)`,
		id, "host-"+id, state); err != nil {
		c.t.Fatal(err)
	}
}

// token issues hostID an enrollment token naming this controller.
func (c *testController) token(hostID string) string {
	c.t.Helper()
	var token string
	err := c.db.Write(context.Background(), func(tx *sql.Tx) error {
		var err error
		token, _, err = c.auth.IssueToken(context.Background(), tx, hostID, c.addr)
		return err
	})
	if err != nil {
		c.t.Fatal(err)
	}
	return token
}

// hostState is the host's core_hosts state.
func (c *testController) hostState(hostID string) string {
	c.t.Helper()
	var state string
	if err := c.db.Read().QueryRow(`SELECT state FROM core_hosts WHERE id = ?`, hostID).Scan(&state); err != nil {
		c.t.Fatal(err)
	}
	return state
}

// agentSessions returns a copy of the accepted sessions.
func (c *testController) agentSessions() []*agentSession {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]*agentSession(nil), c.sessions...)
}
