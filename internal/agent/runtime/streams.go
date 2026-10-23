package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sync"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/schemas"
)

// MaxStreamEvent is the largest encoded event a module stream may emit (spec
// 1.6).
const MaxStreamEvent = 64 << 10

var _ core.AgentStreams = (*Runtime)(nil)

// streamParams validates stream parameters; it compiles each parameter type
// once for the process.
var streamParams = schema.NewValidator()

// errStreamClosed is the error end of a stream whose runtime closed under
// it: the controller may open it again on the runtime that replaces this one.
var errStreamClosed = errcode.New(errcode.Unavailable, "the agent runtime closed; open the stream again")

// Streams lists every registered module stream with its schema hash.
func (r *Runtime) Streams(context.Context) ([]core.StreamInfo, error) {
	out := []core.StreamInfo{}
	for _, s := range r.cfg.Registry.Streams() {
		hash, err := schemas.StreamHash(s.Def)
		if err != nil {
			return nil, fmt.Errorf("runtime: stream %s.%s: %w", s.Module, s.Def.Name, err)
		}
		out = append(out, core.StreamInfo{Module: s.Module, Name: s.Def.Name, SchemaHash: hash})
	}
	return out, nil
}

// OpenStream starts module's stream name with the parameters in params and
// returns its body: one {"data": <event>} line per event, then one
// {"end": ...} line. Run's context ends when ctx ends, when the reader is
// closed, or when Close closes the runtime; the stream then ends with the
// error unavailable. A stream the registry lacks is not_found, a module
// whose migrations failed is module.unavailable, parameters the stream's
// schema refuses are request.invalid_input, and a closed runtime is
// unavailable.
func (r *Runtime) OpenStream(ctx context.Context, p core.Principal, module, name string, params url.Values) (io.ReadCloser, error) {
	def, ok := r.cfg.Registry.Stream(module, name)
	if !ok {
		return nil, errcode.New(errcode.NotFound, fmt.Sprintf("no stream %s.%s", module, name))
	}
	if err := r.usable(module + "." + name); err != nil {
		return nil, err
	}
	raw, err := schema.ParamsJSON(def.ParamsType, params)
	if err != nil {
		return nil, err
	}
	if err := streamParams.ValidateType(def.ParamsType, raw); err != nil {
		return nil, err
	}
	all, err := r.streams.add()
	if err != nil {
		return nil, err
	}
	env := r.moduleEnv(module)
	env.Principal = p
	runCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(all, cancel)
	pipe := newStreamPipe(cancel)
	go func() {
		defer r.streams.wg.Done()
		defer stop()
		defer cancel()
		err := lifecycle.Protect(r.cfg.Log, module+"."+name, "stream", func() error {
			return def.Run(runCtx, env, raw, func(event any) error { return pipe.emit(runCtx, event) })
		})
		var end []byte
		switch {
		case pipe.readerGone() || ctx.Err() != nil:
			// Nobody reads the end any more.
		case all.Err() != nil:
			end = pipe.endLine(r.cfg.Log, errStreamClosed)
		default:
			end = pipe.endLine(r.cfg.Log, err)
		}
		pipe.finish(end)
	}()
	return pipe, nil
}

// streamSet is the runtime's running module streams. Its zero value is
// ready to use.
type streamSet struct {
	mu     sync.Mutex
	ctx    context.Context // every stream's Run context ends with it
	cancel context.CancelFunc
	closed bool
	wg     sync.WaitGroup // one per Run still running
}

// add counts one more stream and returns the context its Run must also end
// with; once close began it answers unavailable.
func (s *streamSet) add() (context.Context, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errcode.New(errcode.Unavailable, "the agent runtime is closed")
	}
	if s.ctx == nil {
		s.ctx, s.cancel = context.WithCancel(context.Background())
	}
	s.wg.Add(1)
	return s.ctx, nil
}

// close ends every stream's Run context and waits until each Run returned.
// A Run blocked in emit returns at once, because emit stops waiting for the
// reader when its context ends.
func (s *streamSet) close() {
	s.mu.Lock()
	s.closed = true
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Unlock()
	s.wg.Wait()
}

// streamPipe hands a stream's lines to its reader one at a time. Unlike
// io.Pipe, a writer stops waiting when its context ends, so Close never
// waits on a reader that stopped reading.
type streamPipe struct {
	lines  chan []byte   // event lines; closed by finish
	done   chan struct{} // closed by Close
	fin    chan struct{} // closed when finish begins
	once   sync.Once
	cancel context.CancelFunc

	mu       sync.Mutex
	failed   error  // an event that could not be sent; every later emit returns it
	finished bool   // finish ran; emit must not send any more
	end      []byte // the end line, written before lines closes

	buf []byte // the reader's unread rest of a line
	eof bool
}

func newStreamPipe(cancel context.CancelFunc) *streamPipe {
	return &streamPipe{lines: make(chan []byte), done: make(chan struct{}), fin: make(chan struct{}), cancel: cancel}
}

var (
	errStreamReaderGone = errors.New("runtime: the stream's reader is gone")
	errStreamFinished   = errors.New("runtime: the stream has ended")
)

// emit sends one event line and waits until the reader takes it; it fails
// once the reader is gone, ctx ended, an event was too large, or the stream
// ended.
func (p *streamPipe) emit(ctx context.Context, event any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch {
	case p.failed != nil:
		return p.failed
	case p.finished:
		return errStreamFinished
	}
	b, err := json.Marshal(event)
	if err != nil {
		p.failed = fmt.Errorf("runtime: encode a stream event: %w", err)
		return p.failed
	}
	if len(b) > MaxStreamEvent {
		p.failed = errcode.New(errcode.TooLarge, fmt.Sprintf("a stream event of %d bytes exceeds %d bytes", len(b), MaxStreamEvent))
		return p.failed
	}
	line := make([]byte, 0, len(b)+10)
	line = append(line, `{"data":`...)
	line = append(line, b...)
	line = append(line, "}\n"...)
	select {
	case p.lines <- line:
		return nil
	case <-p.done:
		return errStreamReaderGone
	case <-p.fin:
		return errStreamFinished
	case <-ctx.Done():
		return ctx.Err()
	}
}

// readerGone reports whether Close ran.
func (p *streamPipe) readerGone() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

// streamProblem is the error of an end line: the code and the client-safe
// detail, as the agent API's problem details carry them.
type streamProblem struct {
	Code   errcode.Code `json:"code"`
	Detail string       `json:"detail"`
}

type streamEnd struct {
	Reason string         `json:"reason"`
	Error  *streamProblem `json:"error,omitempty"`
}

// endLine is the end line: done when err is nil and no emit failed, else
// error with the first emit failure or err. Like the agent API's problem
// details, a 5xx code carries only its title; an internal error is logged.
func (p *streamPipe) endLine(log *slog.Logger, err error) []byte {
	p.mu.Lock()
	if p.failed != nil {
		err = p.failed
	}
	p.mu.Unlock()
	e := streamEnd{Reason: "done"}
	if err != nil {
		c := errcode.From(err)
		code := c.Code
		if !errcode.Known(code) {
			code = errcode.Internal
		}
		problem := &streamProblem{Code: code, Detail: c.Detail}
		if code.Status() >= http.StatusInternalServerError {
			problem.Detail = code.Title()
		}
		if code == errcode.Internal {
			log.Error("runtime: stream failed", "code", c.Code, "err", err)
		}
		if problem.Detail == "" {
			problem.Detail = code.Title()
		}
		e = streamEnd{Reason: "error", Error: problem}
	}
	b, merr := json.Marshal(map[string]streamEnd{"end": e})
	if merr != nil {
		panic(fmt.Sprintf("runtime: marshal a stream end: %v", merr)) // strings only
	}
	return append(b, '\n')
}

// finish ends the stream after Run returned: emit fails from now on, and
// the reader gets end, if any, after the last event line, then io.EOF.
func (p *streamPipe) finish(end []byte) {
	close(p.fin)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.finished = true
	p.end = end
	close(p.lines)
}

// Read returns the next part of the current line; a line whose reader
// buffer is large enough arrives in one Read.
func (p *streamPipe) Read(b []byte) (int, error) {
	for len(p.buf) == 0 {
		if p.eof {
			return 0, io.EOF
		}
		select {
		case line, ok := <-p.lines:
			if !ok {
				p.buf, p.eof = p.end, true
				continue
			}
			p.buf = line
		case <-p.done:
			return 0, io.ErrClosedPipe
		}
	}
	n := copy(b, p.buf)
	p.buf = p.buf[n:]
	return n, nil
}

// Close ends the stream for its reader and cancels Run's context.
func (p *streamPipe) Close() error {
	p.once.Do(func() {
		close(p.done)
		p.cancel()
	})
	return nil
}
