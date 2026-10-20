package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
)

// The health window after a swap (spec section 8, Time budget): the deadline
// timer fires HealthDeadline after the swap, and the runner polls every
// HealthInterval until then.
const (
	HealthDeadline = 80 * time.Second
	HealthInterval = 2 * time.Second
)

// maxHealthBytes bounds the controller's /healthz answer.
const maxHealthBytes = 64 << 10

// ControllerHealth is the part of the controller socket's /healthz answer
// the health rule reads.
type ControllerHealth struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

// ControllerProbe reads GET /healthz over the controller socket at socket,
// which answers the version with no credentials.
func ControllerProbe(socket string) func(ctx context.Context) (ControllerHealth, error) {
	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		},
		DisableKeepAlives: true,
	}}
	return func(ctx context.Context) (ControllerHealth, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://controller/healthz", nil)
		if err != nil {
			return ControllerHealth{}, fmt.Errorf("update: controller health: %w", err)
		}
		resp, err := client.Do(req)
		if err != nil {
			return ControllerHealth{}, fmt.Errorf("update: controller health: %w", err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return ControllerHealth{}, fmt.Errorf("update: controller health answered %s", resp.Status)
		}
		var h ControllerHealth
		if err := json.NewDecoder(io.LimitReader(resp.Body, maxHealthBytes)).Decode(&h); err != nil {
			return ControllerHealth{}, fmt.Errorf("update: controller health: %w", err)
		}
		return h, nil
	}
}

// Probes are the health checks of the restarted services.
type Probes struct {
	// Controller is nil when the host has no controller unit.
	Controller func(ctx context.Context) (ControllerHealth, error)
	Agent      func(ctx context.Context) (core.AgentHello, error)
}

// Wait applies the health rule (spec section 8) every HealthInterval of
// clock until both probes pass, which returns nil. A refused connection, a
// timeout or a report of version from is "not yet". A controller reporting
// version to with a status other than ok fails at once; still not passing at
// deadline fails too. moved, when not nil, is asked before each round of
// probes: once another process has finished the record, nobody acts on the
// answer, so Wait returns errMoved instead of polling to the deadline.
func (p Probes) Wait(ctx context.Context, clock core.Clock, from, to string, deadline time.Time, moved func() bool) error {
	if p.Agent == nil {
		return errors.New("update: health check: no agent probe")
	}
	for {
		if moved != nil && moved() {
			return errMoved
		}
		passed, err := p.check(ctx, to)
		if err != nil {
			return err
		}
		if passed {
			return nil
		}
		now := clock.Now()
		if !now.Before(deadline) {
			return fmt.Errorf("update: health check: the services did not report version %s healthy by the deadline (they may still run %s)", to, from)
		}
		if err := sleepOn(ctx, clock, min(HealthInterval, deadline.Sub(now))); err != nil {
			return err
		}
	}
}

// check runs each probe once, each bounded by HealthInterval, and reports
// whether both passed.
func (p Probes) check(ctx context.Context, to string) (bool, error) {
	controllerOK := true
	if p.Controller != nil {
		pctx, cancel := context.WithTimeout(ctx, HealthInterval)
		h, err := p.Controller(pctx)
		cancel()
		switch {
		case err != nil || h.Version != to:
			controllerOK = false
		case h.Status != "ok":
			return false, fmt.Errorf("update: health check: the controller reports version %s with status %q", to, h.Status)
		}
	}
	pctx, cancel := context.WithTimeout(ctx, HealthInterval)
	hello, err := p.Agent(pctx)
	cancel()
	agentOK := err == nil && hello.BinaryVersion == to &&
		(hello.ControllerConnected == nil || *hello.ControllerConnected)
	return controllerOK && agentOK, nil
}

// sleepOn waits d on clock, or until ctx ends.
func sleepOn(ctx context.Context, clock core.Clock, d time.Duration) error {
	fired := make(chan struct{})
	t := clock.AfterFunc(d, func() { close(fired) })
	select {
	case <-fired:
		return nil
	case <-ctx.Done():
		t.Stop()
		return ctx.Err()
	}
}
