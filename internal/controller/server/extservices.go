package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	goruntime "runtime"
	"sync"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/ext"
)

// The restart delays of a controller service (spec 1.12) and how often a
// suspended runner checks again.
const (
	serviceRestartMin  = time.Second
	serviceRestartMax  = 5 * time.Minute
	serviceDefaultPoll = 10 * time.Second
)

// serviceRunnerConfig configures startServiceRunner.
type serviceRunnerConfig struct {
	Services  []ext.Service
	Suspended func(ctx context.Context) (bool, error) // nil: never suspended
	Poll      time.Duration                           // how often a suspended runner re-checks; 0 means 10 s
	Clock     core.Clock                              // nil: core.SystemClock{}
	Log       *slog.Logger                            // nil discards
}

// serviceRunner runs the controller services contributed through the
// extension package, each on its own goroutine.
type serviceRunner struct {
	cfg    serviceRunnerConfig
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// startServiceRunner starts every service once Suspended answers false, and
// restarts a service whose Run returned an error after a delay from 1 s
// doubling to 5 min, reset by a run that lasted 5 min or longer. A Run that
// returns nil or panics before its context ends is restarted the same way:
// a service runs until shutdown. Suspended is asked again before every
// restart, and an error from it counts as suspended, so a restored
// controller never starts a service it cannot prove is released.
func startServiceRunner(cfg serviceRunnerConfig) *serviceRunner {
	if cfg.Poll <= 0 {
		cfg.Poll = serviceDefaultPoll
	}
	if cfg.Clock == nil {
		cfg.Clock = core.SystemClock{}
	}
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &serviceRunner{cfg: cfg, ctx: ctx, cancel: cancel}
	for _, svc := range cfg.Services {
		r.wg.Add(1)
		go r.run(svc)
	}
	return r
}

// stop cancels every service's context and waits for each Run to return.
func (r *serviceRunner) stop() {
	r.cancel()
	r.wg.Wait()
}

func (r *serviceRunner) run(svc ext.Service) {
	defer r.wg.Done()
	delay := serviceRestartMin
	for {
		if !r.waitReleased(svc.Name) {
			return
		}
		started := r.cfg.Clock.Now()
		err := r.call(svc)
		if r.ctx.Err() != nil {
			if err != nil && !errors.Is(err, context.Canceled) {
				r.cfg.Log.Warn("server: controller service failed while stopping", "service", svc.Name, "error", err)
			}
			return
		}
		if r.cfg.Clock.Now().Sub(started) >= serviceRestartMax {
			delay = serviceRestartMin
		}
		if err == nil {
			err = errors.New("server: the service returned before shutdown")
		}
		r.cfg.Log.Error("server: controller service stopped; restarting it", "service", svc.Name, "error", err, "delay", delay)
		if !r.sleep(delay) {
			return
		}
		delay = min(2*delay, serviceRestartMax)
	}
}

// call runs svc once. A panic is recovered so that one service cannot stop
// the controller; as in lifecycle.Protect its value is reported only when it
// is a runtime error, because a service may have put a credential in it.
func (r *serviceRunner) call(svc ext.Service) (err error) {
	defer func() {
		v := recover()
		if v == nil {
			return
		}
		if re, ok := v.(goruntime.Error); ok {
			err = fmt.Errorf("server: service %s panicked: %w", svc.Name, re)
			return
		}
		err = fmt.Errorf("server: service %s panicked with a %T", svc.Name, v)
	}()
	return svc.Run(r.ctx)
}

// waitReleased returns once Suspended answers false, or false when the
// runner stops first.
func (r *serviceRunner) waitReleased(name string) bool {
	for {
		if r.ctx.Err() != nil {
			return false
		}
		if r.cfg.Suspended == nil {
			return true
		}
		suspended, err := r.cfg.Suspended(r.ctx)
		if err != nil {
			if r.ctx.Err() != nil {
				return false
			}
			r.cfg.Log.Warn("server: cannot tell whether controller services are suspended; waiting", "service", name, "error", err)
			suspended = true
		}
		if !suspended {
			return true
		}
		if !r.sleep(r.cfg.Poll) {
			return false
		}
	}
}

// sleep waits d on the runner's clock and reports false when the runner
// stops first.
func (r *serviceRunner) sleep(d time.Duration) bool {
	fired := make(chan struct{})
	t := r.cfg.Clock.AfterFunc(d, func() { close(fired) })
	select {
	case <-fired:
		return true
	case <-r.ctx.Done():
		t.Stop()
		return false
	}
}
