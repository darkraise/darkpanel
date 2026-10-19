package transport

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Heartbeat defaults (spec section 4): three missed pings, 45 s, take a
// host offline.
const (
	HeartbeatInterval = 15 * time.Second
	HeartbeatTimeout  = 10 * time.Second
	HeartbeatFailures = 3
)

// HeartbeatConfig tunes Heartbeat; the zero value is the spec's.
type HeartbeatConfig struct {
	Interval, Timeout time.Duration // zero means HeartbeatInterval, HeartbeatTimeout
	Failures          int           // zero means HeartbeatFailures
	OnAlive           func()        // called after every successful ping; may be nil
}

func (c HeartbeatConfig) withDefaults() HeartbeatConfig {
	if c.Interval == 0 {
		c.Interval = HeartbeatInterval
	}
	if c.Timeout == 0 {
		c.Timeout = HeartbeatTimeout
	}
	if c.Failures == 0 {
		c.Failures = HeartbeatFailures
	}
	return c
}

// pinger is the part of a Session the heartbeat uses.
type pinger interface {
	Ping() (time.Duration, error)
	Done() <-chan struct{}
	Close() error
}

// Heartbeat pings s every Interval, waiting at most Timeout for each answer,
// until ctx ends (nil) or s closes (nil). After Failures consecutive failed
// pings it closes s and returns an error.
func Heartbeat(ctx context.Context, s *Session, cfg HeartbeatConfig) error {
	return heartbeat(ctx, s, cfg)
}

func heartbeat(ctx context.Context, s pinger, cfg HeartbeatConfig) error {
	cfg = cfg.withDefaults()
	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()
	failed := 0
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-s.Done():
			return nil
		case <-ticker.C:
		}
		err := ping(ctx, s, cfg.Timeout)
		if ctx.Err() != nil {
			return nil
		}
		if err == nil {
			failed = 0
			if cfg.OnAlive != nil {
				cfg.OnAlive()
			}
			continue
		}
		select {
		case <-s.Done():
			// The peer or the owner ended the session; that is not a failure
			// of the heartbeat.
			return nil
		default:
		}
		if failed++; failed >= cfg.Failures {
			return errors.Join(fmt.Errorf("transport: %d heartbeats in a row failed: %w", failed, err), s.Close())
		}
	}
}

// ping waits at most timeout for one answer. A ping that outlives the wait
// ends on its own: yamux bounds it by the session's write timeout.
func ping(ctx context.Context, s pinger, timeout time.Duration) error {
	answered := make(chan error, 1)
	go func() {
		_, err := s.Ping()
		answered <- err
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-answered:
		return err
	case <-timer.C:
		return fmt.Errorf("transport: no heartbeat answer within %s", timeout)
	case <-ctx.Done():
		return ctx.Err()
	}
}
