package runtime

import (
	"context"
	"sync"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// MaxHostTransfers is how many uploads and downloads run on a host at once
// (M11 spec 1.2).
const MaxHostTransfers = 2

// errTransfersClosed ends the transfers of a runtime that closed.
var errTransfersClosed = errcode.New(errcode.Unavailable, "the agent restarted; start the transfer again")

// errTransfersOff ends the transfers of a module turned off.
var errTransfersOff = errcode.New(errcode.ModuleUnavailable, "the console was turned off on this host")

// OpenFiles returns the console's file operations as p and the context to
// run them under, and done, which the caller calls once the request ended.
// A transfer, an upload or a download, counts against MaxHostTransfers
// (console.limit beyond) until done, and its context ends when the module
// is turned off or the runtime closes. A host without the console's file
// manager is not_found; a module the policy file denies, whose migrations
// failed or whose enabled flag is 0 is module.unavailable.
func (r *Runtime) OpenFiles(ctx context.Context, p core.Principal, transfer bool) (context.Context, core.ConsoleFiles, func(), error) {
	fm, ok := r.cfg.Registry.FileManager(consoleModule)
	if !ok {
		return nil, nil, nil, errcode.New(errcode.NotFound, "this agent serves no file manager")
	}
	if err := r.usable(consoleModule); err != nil {
		return nil, nil, nil, err
	}
	runCtx, done, err := r.addTransfer(ctx, transfer)
	if err != nil {
		return nil, nil, nil, err
	}
	env := r.moduleEnv(consoleModule)
	env.Principal = p
	return runCtx, fm.Files(env), done, nil
}

// addTransfer checks the console's flag and, for a transfer, counts it. It
// runs under enableMu, so a flag change or a policy reload that ends the
// module's transfers cannot miss one starting beside it.
func (r *Runtime) addTransfer(ctx context.Context, transfer bool) (context.Context, func(), error) {
	r.enableMu.Lock()
	defer r.enableMu.Unlock()
	if r.Policy().Denied(consoleModule) {
		return nil, nil, deniedError(consoleModule)
	}
	enabled, err := r.moduleEnabled(ctx, consoleModule)
	if err != nil {
		return nil, nil, err
	}
	if !enabled {
		return nil, nil, errcode.New(errcode.ModuleUnavailable, "module console is unavailable: it is disabled on this host")
	}
	if !transfer {
		return ctx, func() {}, nil
	}
	return r.transfers.add(ctx, consoleModule)
}

// transferSet is the runtime's running transfers. Its zero value is ready
// to use.
type transferSet struct {
	mu     sync.Mutex
	open   map[*transferEntry]struct{}
	closed bool
	wg     sync.WaitGroup // one per transfer still running
}

type transferEntry struct {
	module string
	cancel context.CancelCauseFunc
}

func (s *transferSet) add(ctx context.Context, module string) (context.Context, func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, nil, errcode.New(errcode.Unavailable, "the agent runtime is closed")
	}
	if len(s.open) >= MaxHostTransfers {
		return nil, nil, errcode.New(errcode.ConsoleLimit, "at most 2 uploads and downloads run on a host at once; wait for one to end")
	}
	if s.open == nil {
		s.open = map[*transferEntry]struct{}{}
	}
	runCtx, cancel := context.WithCancelCause(ctx)
	e := &transferEntry{module: module, cancel: cancel}
	s.open[e] = struct{}{}
	s.wg.Add(1)
	done := sync.OnceFunc(func() {
		s.mu.Lock()
		delete(s.open, e)
		s.mu.Unlock()
		cancel(nil)
		s.wg.Done()
	})
	return runCtx, done, nil
}

// closeModule ends module's transfers ("" every one) with cause.
func (s *transferSet) closeModule(module string, cause error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for e := range s.open {
		if module == "" || e.module == module {
			e.cancel(cause)
		}
	}
}

// closeAll refuses new transfers, ends the running ones and waits until
// each one's request is done.
func (s *transferSet) closeAll() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.closeModule("", errTransfersClosed)
	s.wg.Wait()
}
