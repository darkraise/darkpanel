package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/darkraise/darkpanel/internal/agent/wire"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// File manager limits (M11 spec 1.2 and section 5).
const (
	MaxHostTransfers = 2
	MaxFileWrite     = 1 << 20
	MaxFileUpload    = 512 << 20
	// TransferChunkTimeout bounds each chunk of a transfer: a browser or an
	// agent that moves no byte for this long ends it.
	TransferChunkTimeout = 60 * time.Second
	transferChunk        = 32 << 10
)

// fileModule is the module whose enablement a transfer rechecks.
const fileModule = "console"

// fileOp is how the controller serves one file endpoint: its method,
// whether it needs the step-up window, whether it is a transfer, the query
// parameters it passes on and the Content-Type of its answer. The
// controller sets that type itself: an agent's own could have a file
// rendered as a page of the panel's origin.
type fileOp struct {
	method   string
	stepUp   bool
	transfer bool
	query    []string
	answer   string
}

const (
	jsonAnswer = "application/json"
	textAnswer = "text/plain; charset=utf-8"
	byteAnswer = "application/octet-stream"
)

var fileOps = map[string]fileOp{
	"list":     {method: http.MethodGet, query: []string{"path", "cursor"}, answer: jsonAnswer},
	"stat":     {method: http.MethodGet, query: []string{"path"}, answer: jsonAnswer},
	"read":     {method: http.MethodGet, stepUp: true, query: []string{"path"}, answer: textAnswer},
	"download": {method: http.MethodGet, stepUp: true, transfer: true, query: []string{"path"}, answer: byteAnswer},
	"write":    {method: http.MethodPost, stepUp: true, query: []string{"path", "sha256"}, answer: jsonAnswer},
	"upload":   {method: http.MethodPost, stepUp: true, transfer: true, query: []string{"path"}, answer: jsonAnswer},
	"mkdir":    {method: http.MethodPost, stepUp: true},
	"rename":   {method: http.MethodPost, stepUp: true},
	"delete":   {method: http.MethodPost, stepUp: true},
	"chmod":    {method: http.MethodPost, stepUp: true},
	"chown":    {method: http.MethodPost, stepUp: true},
}

// transferRoute reports whether r is an upload or a download, which are
// identified passively like streams: their chunks touch the session
// instead.
func transferRoute(r *http.Request) bool {
	seg := strings.Split(r.URL.Path, "/")
	if len(seg) != 7 || seg[0] != "" || seg[1] != "api" || seg[2] != "v1" || seg[3] != "hosts" || seg[4] == "" || seg[5] != "files" {
		return false
	}
	op, ok := fileOps[seg[6]]
	return ok && op.transfer && op.method == r.Method
}

// takeTransfer counts one more transfer on host unless it holds
// MaxHostTransfers.
func (l *longLived) takeTransfer(host string) (release func(), err error) {
	return l.take(&l.transfers, host, MaxHostTransfers,
		errcode.New(errcode.ConsoleLimit, fmt.Sprintf("at most %d uploads and downloads run on a host at once; wait for one to end", MaxHostTransfers)))
}

// handleFile proxies a file endpoint of a host to its agent (spec section
// 5). Every refusal comes before any byte reaches the agent. Listing and
// stat need admin on the host; everything else also needs the step-up
// window. Write and upload bodies and every answer are streamed, never
// held whole.
func (s *Server) handleFile(w http.ResponseWriter, r *http.Request) {
	c, _ := callerFrom(r.Context())
	host, name := r.PathValue("host"), r.PathValue("op")
	op, known := fileOps[name]
	files, served := s.cfg.Dispatcher.(core.DispatcherFiles)
	var err error
	switch {
	case r.Method == http.MethodHead:
		// ServeMux routes HEAD to the GET route; a HEAD download would open
		// a transfer for nothing.
		w.Header().Set("Allow", http.MethodGet)
		err = errcode.New(errcode.MethodNotAllowed, "a file read uses GET")
	case !known || op.method != r.Method:
		err = errcode.New(errcode.NotFound, "no file operation "+r.Method+" "+name)
	case !served:
		err = errcode.New(errcode.NotFound, "this controller serves no file manager")
	default:
		err = authorizeHost(c.principal, host, core.ScopeAdmin)
	}
	if err == nil && op.stepUp {
		err = s.requireElevated(r.Context(), c)
	}
	var req core.FileRequest
	if err == nil {
		req, err = fileRequest(w, r, name, op)
	}
	if err != nil {
		writeProblem(w, s.log, err)
		return
	}
	if !op.transfer {
		ans, err := files.File(r.Context(), c.principal, host, req)
		if err != nil {
			writeProblem(w, s.log, err)
			return
		}
		s.answerFile(w, r, op, ans, nil)
		return
	}
	release, err := s.longLived.takeTransfer(host)
	if err != nil {
		writeProblem(w, s.log, err)
		return
	}
	defer release()
	t := s.startTransfer(w, r, c, host, files)
	defer t.stop()
	if req.Body != nil {
		req.Body = &chunkReader{r: req.Body, t: t}
	}
	ans, err := files.File(t.ctx, c.principal, host, req)
	if err != nil {
		// The deadline that unblocks an ended upload also cancels the
		// request's context, so the transfer's own end is checked first.
		cause := context.Cause(t.ctx)
		switch {
		case slices.ContainsFunc(transferEnds, func(e error) bool { return errors.Is(cause, e) }):
			err = cause
		case r.Context().Err() != nil:
			return // the browser left
		}
		_ = t.rc.SetWriteDeadline(time.Now().Add(s.transferIdle))
		writeProblem(w, s.log, err)
		return
	}
	s.answerFile(w, r, op, ans, t)
}

// fileRequest checks r's body and keeps the query parameters op passes on.
// A write or an upload needs application/octet-stream with a
// Content-Length within its limit; a metadata operation's JSON is decoded
// under the API's usual limits and sent again as the agent's FileChange.
func fileRequest(w http.ResponseWriter, r *http.Request, name string, op fileOp) (core.FileRequest, error) {
	req := core.FileRequest{Op: name, Query: url.Values{}}
	q := r.URL.Query()
	for _, k := range op.query {
		if v, ok := q[k]; ok {
			req.Query[k] = v
		}
	}
	switch {
	case name == "write" || name == "upload":
		limit := int64(MaxFileWrite)
		if name == "upload" {
			limit = MaxFileUpload
		}
		mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		switch {
		case err != nil || mediaType != "application/octet-stream":
			return req, errcode.New(errcode.UnsupportedMediaType, "send the file with Content-Type: application/octet-stream")
		case r.ContentLength < 0:
			return req, errcode.New(errcode.InvalidInput, "a file body needs its Content-Length")
		case r.ContentLength > limit:
			return req, errcode.New(errcode.TooLarge, fmt.Sprintf("the file body exceeds %d MiB", limit>>20))
		}
		req.Body, req.Size = io.LimitReader(r.Body, r.ContentLength), r.ContentLength
	case op.method == http.MethodPost:
		var change wire.FileChange
		if err := decodeBody(w, r, &change); err != nil {
			return req, err
		}
		b, err := json.Marshal(change)
		if err != nil {
			return req, err
		}
		req.Body, req.Size, req.JSON = bytes.NewReader(b), int64(len(b)), true
	}
	return req, nil
}

// answerFile streams the agent's answer to the browser, a chunk at a time.
// A transfer renews the write deadline before each chunk; a download is
// sent as an attachment. An answer cut short aborts the connection, so the
// browser never takes part of a file for the whole of it.
func (s *Server) answerFile(w http.ResponseWriter, r *http.Request, op fileOp, ans core.FileAnswer, t *transfer) {
	if ans.Body == nil {
		w.WriteHeader(ans.Status)
		return
	}
	defer func() { _ = ans.Body.Close() }()
	h := w.Header()
	h.Set("Content-Type", op.answer)
	if ans.Size >= 0 {
		h.Set("Content-Length", strconv.FormatInt(ans.Size, 10))
	}
	if ans.SHA256 != "" {
		h.Set(wire.HeaderSHA256, ans.SHA256)
	}
	if r.PathValue("op") == "download" {
		name := path.Base(r.URL.Query().Get("path"))
		if d := mime.FormatMediaType("attachment", map[string]string{"filename": name}); d != "" && name != "." && name != "/" {
			h.Set("Content-Disposition", d)
		} else {
			h.Set("Content-Disposition", "attachment")
		}
	}
	if t != nil {
		t.started.Store(true)
	}
	w.WriteHeader(ans.Status)
	rc := http.NewResponseController(w)
	buf := make([]byte, transferChunk)
	var sent int64
	for {
		n, err := ans.Body.Read(buf)
		if n > 0 {
			// The check comes after the renewal: an end that unblocks the
			// write after it is not undone by it.
			if t != nil {
				_ = rc.SetWriteDeadline(time.Now().Add(s.transferIdle))
				if t.ctx.Err() != nil {
					panic(http.ErrAbortHandler)
				}
			}
			if _, werr := w.Write(buf[:n]); werr != nil {
				panic(http.ErrAbortHandler)
			}
			sent += int64(n)
			if t != nil {
				_ = rc.Flush()
				t.moved()
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) || (ans.Size >= 0 && sent != ans.Size) {
				if t != nil && t.ctx.Err() != nil {
					s.log.Info("api: a file transfer ended", "reason", context.Cause(t.ctx))
				}
				panic(http.ErrAbortHandler)
			}
			return
		}
	}
}

// chunkReader is an upload's body as the agent request reads it: at most
// transferChunk at a time, each read under a fresh read deadline.
type chunkReader struct {
	r io.Reader
	t *transfer
}

func (c *chunkReader) Read(p []byte) (int, error) {
	if len(p) > transferChunk {
		p = p[:transferChunk]
	}
	// The agent request may read once more after the transfer ended; it
	// must not renew the deadline of a connection that moved on. The second
	// check is as in answerFile.
	if c.t.ctx.Err() != nil {
		return 0, context.Cause(c.t.ctx)
	}
	_ = c.t.rc.SetReadDeadline(time.Now().Add(c.t.s.transferIdle))
	if c.t.ctx.Err() != nil {
		return 0, context.Cause(c.t.ctx)
	}
	n, err := c.r.Read(p)
	if n > 0 {
		c.t.moved()
	}
	return n, err
}

// transfer is one running upload or download: a long-lived request (spec
// 1.2) whose context ends when the caller loses admin on the host or the
// console there, at sign-out or expiry, at CloseStreams, or when no chunk
// moved for TransferChunkTimeout.
type transfer struct {
	s       *Server
	r       *http.Request
	rc      *http.ResponseController
	ctx     context.Context
	cancel  context.CancelCauseFunc
	idle    *time.Timer
	done    chan struct{}
	touch   chan struct{}
	started atomic.Bool // the answer's header is out
	unblock func() bool
}

// The errors a transfer ends with when the answer has not started.
var (
	errTransferRevoked = errcode.New(errcode.Forbidden, "the transfer ended: access to the host or its console was revoked")
	errTransferExpired = errcode.New(errcode.AuthRequired, "the transfer ended: the session expired")
	errTransferStopped = errcode.New(errcode.Unavailable, "the transfer ended: the controller is shutting down")
	errTransferStalled = errcode.New(errcode.Unavailable, "the transfer ended: no data moved for 60 s")
	// A 5xx answer hides its detail, so the failed check is refused like a
	// revocation, with the text that tells the browser to try again.
	errTransferCheck = errcode.New(errcode.Forbidden, "The panel couldn't check your session; try again")
	transferEnds     = []error{errTransferRevoked, errTransferExpired, errTransferStopped, errTransferStalled, errTransferCheck}
)

func (s *Server) startTransfer(w http.ResponseWriter, r *http.Request, c caller, host string, files core.DispatcherFiles) *transfer {
	ctx, cancel := context.WithCancelCause(r.Context())
	t := &transfer{s: s, r: r, rc: http.NewResponseController(w), ctx: ctx, cancel: cancel,
		done: make(chan struct{}), touch: make(chan struct{}, 1)}
	t.idle = time.AfterFunc(s.transferIdle, func() { cancel(errTransferStalled) })
	// The listener's deadlines count from the request's start; a transfer
	// renews its own per chunk instead (spec 1.2). An upload's answer and a
	// download's request body need none until then.
	_ = t.rc.SetReadDeadline(time.Time{})
	_ = t.rc.SetWriteDeadline(time.Time{})
	// A transfer a check ended may be blocked on the browser: an upload's
	// next chunk, or a download's write once its answer started. Ending it
	// otherwise leaves the deadlines alone, for the connection's next
	// request.
	t.unblock = context.AfterFunc(ctx, func() {
		cause := context.Cause(ctx)
		if slices.ContainsFunc(transferEnds, func(e error) bool { return errors.Is(cause, e) }) {
			_ = t.rc.SetReadDeadline(time.Now())
			if t.started.Load() {
				_ = t.rc.SetWriteDeadline(time.Now())
			}
		}
	})
	go t.watch(c, host, files)
	return t
}

// moved records that a chunk moved: it renews the stall timer and asks the
// watcher to touch the session.
func (t *transfer) moved() {
	t.idle.Reset(t.s.transferIdle)
	select {
	case t.touch <- struct{}{}:
	default:
	}
}

func (t *transfer) stop() {
	t.idle.Stop()
	t.unblock()
	t.cancel(nil)
	<-t.done
}

// watch rechecks the caller every SSERecheck and touches the session at
// most once a minute while chunks move, until the transfer ends.
func (t *transfer) watch(c caller, host string, files core.DispatcherFiles) {
	defer close(t.done)
	clock := t.s.clock()
	check := make(chan struct{}, 1)
	checkT := clock.AfterFunc(SSERecheck, sseSignal(check))
	defer func() { checkT.Stop() }()
	var revoked <-chan struct{}
	if p, ok := core.ProxiedFrom(t.r.Context()); ok {
		revoked = p.Revoked
	}
	expires := c.identity.ExpiresAt
	var touched time.Time
	for {
		select {
		case <-t.ctx.Done():
			return
		case <-check:
			p, reason := t.s.sseRecheck(t.r, &expires)
			if reason == "" && authorizeHost(p, host, core.ScopeAdmin) != nil {
				reason = closeRevoked
			}
			if reason == "" {
				reason = usableReason(files.ModuleUsable(t.ctx, host, fileModule))
			}
			switch reason {
			case "":
				checkT = clock.AfterFunc(SSERecheck, sseSignal(check))
			case closeExpired:
				t.cancel(errTransferExpired)
			case closeError:
				t.cancel(errTransferCheck)
			default:
				t.cancel(errTransferRevoked)
			}
		case <-t.touch:
			t.s.touchSession(t.ctx, t.r, c.session, &touched)
		case <-revoked:
			t.cancel(errTransferRevoked)
		case <-t.s.streamsDone:
			t.cancel(errTransferStopped)
		}
	}
}
