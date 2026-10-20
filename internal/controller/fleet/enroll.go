package fleet

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/netip"
	"time"

	"github.com/darkraise/darkpanel/internal/agent/wire"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/transport"
)

// maxEnrollRequest bounds everything an enrollment connection may send: the
// body limit plus room for the request line and headers.
const maxEnrollRequest = wire.MaxBody + 64<<10

// serveEnroll answers exactly one request on an enrollment connection; the
// caller closes the connection afterwards.
func (s *Server) serveEnroll(peer *transport.Peer) {
	conn := peer.Conn
	source := enrollSource(conn.RemoteAddr())
	log := s.cfg.Log.With("source", source)
	_ = conn.SetDeadline(time.Now().Add(s.cfg.EnrollTimeout))
	req, err := http.ReadRequest(bufio.NewReader(io.LimitReader(conn, maxEnrollRequest)))
	if err != nil {
		log.Debug("fleet: unreadable enrollment request", "err", err)
		return
	}
	ctx, cancel := context.WithTimeout(s.ctx, s.cfg.EnrollTimeout)
	defer cancel()
	answer := &enrollAnswer{header: http.Header{}}
	s.enroll(answer, req.WithContext(ctx), source)
	if err := answer.response(req).Write(conn); err != nil {
		log.Debug("fleet: cannot send the enrollment answer", "err", err)
	}
}

// enroll handles one enrollment request from source. Every request counts
// against the rate limits, whatever it asks for. A refusal carries only its
// code's fixed detail: Authority.Enroll answers every token or CSR failure
// as enroll.token_invalid with one detail, and its causes are logged here,
// never sent.
func (s *Server) enroll(w http.ResponseWriter, r *http.Request, source string) {
	log := s.cfg.Log.With("source", source)
	if err := s.cfg.Limiter.Allow(source); err != nil {
		writeProblem(w, log, err)
		return
	}
	if r.URL.Path != transport.EnrollPath {
		writeProblem(w, log, errcode.New(errcode.NotFound, "the enrollment connection serves only "+transport.EnrollPath))
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeProblem(w, log, errcode.New(errcode.MethodNotAllowed, transport.EnrollPath+" takes POST"))
		return
	}
	var req transport.EnrollRequest
	if err := decodeBody(w, r, &req); err != nil {
		writeProblem(w, log, err)
		return
	}
	e, err := s.cfg.Authority.Enroll(r.Context(), req.Token, []byte(req.CSR))
	if err != nil {
		log.Info("fleet: enrollment refused", "err", err)
		writeProblem(w, log, err)
		return
	}
	log.Info("fleet: host enrolled", "host", e.HostID)
	writeJSON(w, log, http.StatusOK, transport.EnrollResponse{Certificate: string(e.Certificate), CA: string(e.CA)})
}

// enrollSource is the rate limiter's key for a client address, the rule of
// the login limiter: an IPv4-mapped IPv6 address becomes its IPv4 form and
// any other IPv6 address its /64 prefix, because one host commonly holds a
// whole /64 and could otherwise spend the global limit alone. An address
// that does not parse is used as it is.
func enrollSource(addr net.Addr) string {
	ap, err := netip.ParseAddrPort(addr.String())
	if err != nil {
		return addr.String()
	}
	a := ap.Addr().WithZone("").Unmap()
	if a.Is6() {
		p, _ := a.Prefix(64)
		return p.String()
	}
	return a.String()
}

// enrollAnswer collects the one answer of an enrollment connection, which
// has no http.Server to write it.
type enrollAnswer struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (a *enrollAnswer) Header() http.Header { return a.header }

func (a *enrollAnswer) WriteHeader(status int) {
	if a.status == 0 {
		a.status = status
	}
}

func (a *enrollAnswer) Write(p []byte) (int, error) {
	a.WriteHeader(http.StatusOK)
	return a.body.Write(p)
}

// response is the answer to req, announcing that the connection closes.
func (a *enrollAnswer) response(req *http.Request) *http.Response {
	a.WriteHeader(http.StatusOK)
	return &http.Response{
		StatusCode: a.status, ProtoMajor: 1, ProtoMinor: 1, Header: a.header,
		Body: io.NopCloser(&a.body), ContentLength: int64(a.body.Len()), Close: true, Request: req,
	}
}
