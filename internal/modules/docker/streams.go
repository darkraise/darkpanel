package docker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/docker/logfilter"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

// logChunkBytes bounds the text of one log event: JSON escaping can turn a
// byte into six, and a serialized event must stay under the transport's
// 64 KiB limit.
const logChunkBytes = 8192

// logDefaultTail is the number of lines a logs stream reads when the
// parameters name none.
const logDefaultTail = "200"

func (k dk) logsStream() core.StreamDef {
	return core.DefineStream("logs", func(ctx context.Context, env core.Env, p LogsParams, emit func(LogEvent) error) error {
		opts, err := logOptions(p)
		if err != nil {
			return err
		}
		c, err := k.d.docker.InspectContainer(ctx, p.Container)
		if err != nil {
			return engineError("container", p.Container, err)
		}
		filter, err := k.logFilter(ctx, env, c)
		if err != nil {
			return err
		}
		s, err := k.d.docker.Logs(ctx, c.ID, opts)
		if err != nil {
			return engineError("container", p.Container, err)
		}
		defer func() { _ = s.Close() }()
		for {
			frame, err := s.Next()
			if err != nil {
				return logEnd(ctx, p.Container, err, logEmitHeld(filter, emit))
			}
			if err := logEmit(frame.Stream, filter.Push(frame.Stream, frame.Data), emit); err != nil {
				return err
			}
		}
	})
}

// logOptions checks the parameters of a logs stream and turns them into the
// Engine's options. Timestamps stay off, so each frame reaches the filter as
// the container wrote it.
func logOptions(p LogsParams) (dockerapi.LogsOptions, error) {
	if p.Container == "" {
		return dockerapi.LogsOptions{}, streamInvalid("/container", "container is required")
	}
	var since time.Time
	if p.Since != "" {
		t, err := time.Parse(time.RFC3339Nano, p.Since)
		if err != nil {
			return dockerapi.LogsOptions{}, streamInvalid("/since", "since is not an RFC 3339 time")
		}
		since = t
	}
	tail := p.Tail
	switch tail {
	case "":
		tail = logDefaultTail
	case "all":
		tail = ""
	default:
		if n, err := strconv.Atoi(tail); err != nil || n < 0 || strconv.Itoa(n) != tail {
			return dockerapi.LogsOptions{}, streamInvalid("/tail", `tail is "all" or a whole number of lines`)
		}
	}
	return dockerapi.LogsOptions{Follow: p.Live, Since: since, Tail: tail, Stdout: true, Stderr: true}, nil
}

// logEnd is what a logs stream returns once Next fails with err, after
// emitting the bytes the filter held, which failed with flushErr.
func logEnd(ctx context.Context, container string, err, flushErr error) error {
	switch {
	case errors.Is(err, io.EOF):
		return flushErr
	case ctx.Err() != nil:
		return nil
	default:
		return fmt.Errorf("docker: logs of %s: %w", container, err)
	}
}

// logEmitHeld emits, masked, the bytes the filter still holds back for each
// stream.
func logEmitHeld(f *logfilter.Filter, emit func(LogEvent) error) error {
	for _, stream := range []string{"stdout", "stderr"} {
		if err := logEmit(stream, f.Flush(stream), emit); err != nil {
			return err
		}
	}
	return nil
}

// logEmit emits data as events of at most logChunkBytes, each cut before a
// UTF-8 rune start so a character is not split between events.
func logEmit(stream string, data []byte, emit func(LogEvent) error) error {
	for len(data) > 0 {
		n := len(data)
		if n > logChunkBytes {
			n = logChunkBytes
			for n > 0 && !utf8.RuneStart(data[n]) {
				n--
			}
			if n == 0 {
				n = logChunkBytes
			}
		}
		if err := emit(LogEvent{Stream: stream, Text: string(data[:n])}); err != nil {
			return err
		}
		data = data[n:]
	}
	return nil
}

func (k dk) statsStream() core.StreamDef {
	return core.DefineStream("stats", func(ctx context.Context, _ core.Env, p StatsParams, emit func(StatsEvent) error) error {
		if p.Container == "" {
			return streamInvalid("/container", "container is required")
		}
		c, err := k.d.docker.InspectContainer(ctx, p.Container)
		if err != nil {
			return engineError("container", p.Container, err)
		}
		s, err := k.d.docker.Stats(ctx, c.ID, true)
		if err != nil {
			return engineError("container", p.Container, err)
		}
		defer func() { _ = s.Close() }()
		for {
			sample, err := s.Next()
			switch {
			case err == nil:
			case errors.Is(err, io.EOF), ctx.Err() != nil:
				return nil
			default:
				return fmt.Errorf("docker: stats of %s: %w", p.Container, err)
			}
			if err := emit(streamStatsEvent(sample)); err != nil {
				return err
			}
		}
	})
}

func streamStatsEvent(s dockerapi.Stats) StatsEvent {
	u := usageOf(s)
	e := StatsEvent{Read: s.Read, CPUPercent: u.CPUPercent, MemUsage: u.MemUsage, MemLimit: u.MemLimit, Pids: s.PidsStats.Current}
	for _, n := range s.Networks {
		e.NetRx += n.RxBytes
		e.NetTx += n.TxBytes
	}
	return e
}

func streamInvalid(path, msg string) error {
	return &errcode.Error{Code: errcode.InvalidInput, Detail: msg, Fields: []errcode.FieldError{{Path: path, Message: msg}}}
}
