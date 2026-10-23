// Package cloudfake is the shared scaffolding of the in-process provider
// fakes (spec 1.9): an HTTP server that records every request and injects
// faults, on which each module's fake registers its handlers.
package cloudfake

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
)

// Server is an in-process provider stand-in: a plain-HTTP httptest server
// that records every request and injects faults.
type Server struct {
	srv *httptest.Server
	mux *http.ServeMux

	mu       sync.Mutex
	requests []Request
	faults   map[string][]Fault // by "<METHOD> <path>", oldest first
}

// Fault is one injected failure. Status answers with that status (and
// RetryAfter as the Retry-After header when set) without running the
// handler. LoseResponse runs the handler, so the write commits, and then
// closes the connection without sending its answer.
type Fault struct {
	Status       int
	RetryAfter   string
	Body         string
	LoseResponse bool
	Times        int
}

// Request is one recorded request.
type Request struct {
	Method, Path, RawQuery string
	Header                 http.Header
	Body                   []byte
}

// New starts a server closed by tb.Cleanup.
func New(tb testing.TB) *Server {
	tb.Helper()
	s := &Server{mux: http.NewServeMux(), faults: map[string][]Fault{}}
	s.srv = httptest.NewServer(http.HandlerFunc(s.serve))
	tb.Cleanup(s.srv.Close)
	return s
}

// URL is the server's base URL, without a trailing slash.
func (s *Server) URL() string { return s.srv.URL }

// Client is an HTTP client for the server.
func (s *Server) Client() *http.Client { return s.srv.Client() }

// Handle routes method and path (a net/http ServeMux pattern without the
// method) to h.
func (s *Server) Handle(method, path string, h http.HandlerFunc) {
	s.mux.HandleFunc(method+" "+path, h)
}

// Fail queues f for the next f.Times requests matching method and path
// (Times 0 means 1). A queued fault is used before the handler runs. path is
// compared with the request's URL path exactly.
func (s *Server) Fail(method, path string, f Fault) {
	n := max(f.Times, 1)
	s.mu.Lock()
	defer s.mu.Unlock()
	key := method + " " + path
	for range n {
		s.faults[key] = append(s.faults[key], f)
	}
}

// Requests returns the recorded requests in arrival order, faulted ones
// included.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Request, len(s.requests))
	for i, r := range s.requests {
		out[i] = Request{Method: r.Method, Path: r.Path, RawQuery: r.RawQuery, Header: r.Header.Clone(), Body: slices.Clone(r.Body)}
	}
	return out
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	f, faulted := s.record(r, body)
	switch {
	case faulted && f.LoseResponse:
		s.loseResponse(w, r)
	case faulted:
		if f.RetryAfter != "" {
			w.Header().Set("Retry-After", f.RetryAfter)
		}
		w.WriteHeader(f.Status)
		_, _ = io.WriteString(w, f.Body)
	default:
		s.mux.ServeHTTP(w, r)
	}
}

// record appends r to the log and takes the next fault queued for it.
func (s *Server) record(r *http.Request, body []byte) (Fault, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, Request{
		Method: r.Method, Path: r.URL.Path, RawQuery: r.URL.RawQuery, Header: r.Header.Clone(), Body: body,
	})
	key := r.Method + " " + r.URL.Path
	queue := s.faults[key]
	if len(queue) == 0 {
		return Fault{}, false
	}
	s.faults[key] = queue[1:]
	return queue[0], true
}

// loseResponse runs the handler into a recorder, so whatever it commits is
// committed, and then drops the connection: the client sees a transport
// failure after the provider acted.
func (s *Server) loseResponse(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(httptest.NewRecorder(), r)
	hj, ok := w.(http.Hijacker)
	if !ok {
		panic("cloudfake: the response writer cannot be hijacked")
	}
	conn, _, err := hj.Hijack()
	if err != nil {
		panic("cloudfake: hijack: " + err.Error())
	}
	_ = conn.Close()
}
