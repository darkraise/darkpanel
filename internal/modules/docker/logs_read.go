package docker

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strconv"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/docker/logfilter"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

func (k dk) containerLogs() core.ActionDef {
	return core.DefineRead[LogsInput, LogsOutput](core.ActionSpec{
		Name: ID + ".container.logs", Summary: "Read the last lines of a container's logs.", Version: 1,
		Side: core.SideAgent,
	}, containerLogsHandler{k: k})
}

// containerLogsHandler reads a bounded tail of one container's logs, masked
// with the secrets of the module that owns the container.
type containerLogsHandler struct{ k dk }

func (h containerLogsHandler) Read(ctx context.Context, env core.Env, in LogsInput) (LogsOutput, error) {
	if in.Container == "" {
		return LogsOutput{}, inspectInvalid("/container", "container is required")
	}
	tail := in.Tail
	if tail == 0 {
		tail = 200
	}
	if tail < 1 || tail > 5000 {
		return LogsOutput{}, inspectInvalid("/tail", "tail must be between 1 and 5000")
	}
	var since time.Time
	if in.Since != "" {
		t, err := time.Parse(time.RFC3339, in.Since)
		if err != nil {
			return LogsOutput{}, inspectInvalid("/since", "since must be an RFC 3339 time")
		}
		since = t
	}
	c, err := h.k.d.docker.InspectContainer(ctx, in.Container)
	if err != nil {
		return LogsOutput{}, engineError("container", in.Container, err)
	}
	// The filter is built before the logs are opened, so an owned container
	// whose secrets cannot be read is never shown unmasked.
	f, err := h.k.logFilter(ctx, env, c)
	if err != nil {
		return LogsOutput{}, err
	}
	s, err := h.k.d.docker.Logs(ctx, c.ID, dockerapi.LogsOptions{
		Tail: strconv.Itoa(tail), Since: since, Timestamps: in.Timestamps, Stdout: true, Stderr: true,
	})
	if err != nil {
		return LogsOutput{}, engineError("container", in.Container, err)
	}
	defer func() { _ = s.Close() }()
	out, err := readLogLines(s, f)
	if err != nil {
		return LogsOutput{}, engineError("container", in.Container, err)
	}
	return out, nil
}

// readLogLines reads s to its end and splits each stream's masked output into
// lines, in the order the lines complete. Each stream's unterminated last
// line comes at the end, stdout first.
func readLogLines(s logStream, f *logfilter.Filter) (LogsOutput, error) {
	out := LogsOutput{Lines: []LogLine{}}
	pending := map[string][]byte{}
	for {
		frame, err := s.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return LogsOutput{}, err
		}
		buf := append(pending[frame.Stream], f.Push(frame.Stream, frame.Data)...)
		for {
			i := bytes.IndexByte(buf, '\n')
			if i < 0 {
				break
			}
			out.Lines = append(out.Lines, LogLine{Stream: frame.Stream, Text: string(buf[:i])})
			buf = buf[i+1:]
		}
		pending[frame.Stream] = buf
	}
	for _, stream := range []string{"stdout", "stderr"} {
		if rest := append(pending[stream], f.Flush(stream)...); len(rest) > 0 {
			out.Lines = append(out.Lines, LogLine{Stream: stream, Text: string(rest)})
		}
	}
	return out, nil
}
