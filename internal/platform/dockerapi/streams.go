package dockerapi

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// maxFrame bounds one multiplexed log frame; the daemon splits long lines
// well below it.
const maxFrame = 8 << 20

// LogsOptions selects a container's log output.
type LogsOptions struct {
	Follow     bool
	Since      time.Time // zero: from the start
	Tail       string    // "": every line; otherwise a line count
	Timestamps bool
	Stdout     bool
	Stderr     bool
}

// LogFrame is one chunk of log output. Stream is "stdout" or "stderr"; a
// container with a TTY has one combined stream, reported as "stdout".
type LogFrame struct {
	Stream string
	Data   []byte
}

// LogStream reads a container's log output frame by frame.
type LogStream struct {
	body        io.ReadCloser
	r           *bufio.Reader
	multiplexed bool
}

// Logs opens a container's log output.
func (c *Client) Logs(ctx context.Context, id string, opts LogsOptions) (*LogStream, error) {
	q := url.Values{
		"follow":     {strconv.FormatBool(opts.Follow)},
		"timestamps": {strconv.FormatBool(opts.Timestamps)},
		"stdout":     {strconv.FormatBool(opts.Stdout)},
		"stderr":     {strconv.FormatBool(opts.Stderr)},
	}
	if !opts.Since.IsZero() {
		q.Set("since", unixArg(opts.Since))
	}
	if opts.Tail != "" {
		q.Set("tail", opts.Tail)
	}
	res, err := c.do(ctx, request{method: http.MethodGet, path: "/containers/" + url.PathEscape(id) + "/logs", query: q})
	if err != nil {
		return nil, err
	}
	// From API 1.42 the content type says whether the output is
	// multiplexed (no TTY) or raw (TTY).
	return &LogStream{
		body:        res.Body,
		r:           bufio.NewReader(res.Body),
		multiplexed: res.Header.Get("Content-Type") == "application/vnd.docker.multiplexed-stream",
	}, nil
}

// Next returns the next frame, or io.EOF at the end of the output.
func (s *LogStream) Next() (LogFrame, error) {
	if !s.multiplexed {
		buf := make([]byte, 32<<10)
		n, err := s.r.Read(buf)
		if n > 0 {
			return LogFrame{Stream: "stdout", Data: buf[:n]}, nil
		}
		return LogFrame{}, err
	}
	var hdr [8]byte
	if _, err := io.ReadFull(s.r, hdr[:]); err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return LogFrame{}, fmt.Errorf("dockerapi: logs: truncated frame header")
		}
		return LogFrame{}, err
	}
	var stream string
	switch hdr[0] {
	case 1:
		stream = "stdout"
	case 2:
		stream = "stderr"
	default:
		return LogFrame{}, fmt.Errorf("dockerapi: logs: unknown stream %d", hdr[0])
	}
	size := binary.BigEndian.Uint32(hdr[4:])
	if size > maxFrame {
		return LogFrame{}, fmt.Errorf("dockerapi: logs: frame of %d bytes exceeds %d", size, maxFrame)
	}
	data := make([]byte, size)
	if _, err := io.ReadFull(s.r, data); err != nil {
		return LogFrame{}, fmt.Errorf("dockerapi: logs: truncated frame: %w", err)
	}
	return LogFrame{Stream: stream, Data: data}, nil
}

// Close ends the stream.
func (s *LogStream) Close() error { return s.body.Close() }

// unixArg renders t as the API's "seconds.nanoseconds" time argument.
func unixArg(t time.Time) string {
	return fmt.Sprintf("%d.%09d", t.Unix(), t.Nanosecond())
}

// Stats is one resource-usage sample of a container.
type Stats struct {
	Read        string                  `json:"read"`
	PreRead     string                  `json:"preread"`
	CPUStats    CPUStats                `json:"cpu_stats"`
	PreCPUStats CPUStats                `json:"precpu_stats"`
	MemoryStats MemoryStats             `json:"memory_stats"`
	PidsStats   PidsStats               `json:"pids_stats"`
	Networks    map[string]NetworkStats `json:"networks"`
}

// CPUStats is a container's cumulative CPU time in nanoseconds against the
// host's.
type CPUStats struct {
	CPUUsage       CPUUsage `json:"cpu_usage"`
	SystemCPUUsage uint64   `json:"system_cpu_usage"`
	OnlineCPUs     uint32   `json:"online_cpus"`
}

// CPUUsage is a container's CPU time.
type CPUUsage struct {
	TotalUsage uint64 `json:"total_usage"`
}

// MemoryStats is a container's memory use in bytes.
type MemoryStats struct {
	Usage uint64            `json:"usage"`
	Limit uint64            `json:"limit"`
	Stats map[string]uint64 `json:"stats"`
}

// PidsStats is a container's process count.
type PidsStats struct {
	Current uint64 `json:"current"`
}

// NetworkStats is one interface's traffic in bytes.
type NetworkStats struct {
	RxBytes uint64 `json:"rx_bytes"`
	TxBytes uint64 `json:"tx_bytes"`
}

// Stats opens a container's resource-usage samples: one sample when stream
// is false (the daemon takes two readings so the CPU delta is filled),
// otherwise one sample per second until the stream is closed.
func (c *Client) Stats(ctx context.Context, id string, stream bool) (*Stream[Stats], error) {
	q := url.Values{"stream": {strconv.FormatBool(stream)}}
	res, err := c.do(ctx, request{method: http.MethodGet, path: "/containers/" + url.PathEscape(id) + "/stats", query: q})
	if err != nil {
		return nil, err
	}
	return newStream[Stats](res), nil
}

// Event is one daemon event.
type Event struct {
	Type     string
	Action   string
	Actor    EventActor
	Scope    string `json:"scope"`
	Time     int64  `json:"time"`
	TimeNano int64  `json:"timeNano"`
}

// EventActor is the object an event concerns; Attributes carries its name
// and labels.
type EventActor struct {
	ID         string
	Attributes map[string]string
}

// EventsOptions selects daemon events.
type EventsOptions struct {
	Since   time.Time // zero: only new events
	Filters Filters
}

// Events opens the daemon's event stream; it stays open until the stream
// is closed or the context ends.
func (c *Client) Events(ctx context.Context, opts EventsOptions) (*Stream[Event], error) {
	q := url.Values{}
	if !opts.Since.IsZero() {
		q.Set("since", unixArg(opts.Since))
	}
	if err := opts.Filters.set(q); err != nil {
		return nil, err
	}
	res, err := c.do(ctx, request{method: http.MethodGet, path: "/events", query: q})
	if err != nil {
		return nil, err
	}
	return newStream[Event](res), nil
}

// CopyFromContainer returns a tar archive of path inside a container; the
// caller closes it.
func (c *Client) CopyFromContainer(ctx context.Context, id, path string) (io.ReadCloser, error) {
	q := url.Values{"path": {path}}
	res, err := c.do(ctx, request{method: http.MethodGet, path: "/containers/" + url.PathEscape(id) + "/archive", query: q})
	if err != nil {
		return nil, err
	}
	return res.Body, nil
}

// CopyToContainer extracts a tar archive into the directory path inside a
// container, refusing to replace a directory with a file or the reverse.
func (c *Client) CopyToContainer(ctx context.Context, id, path string, archive io.Reader) error {
	q := url.Values{"path": {path}, "noOverwriteDirNonDir": {"true"}}
	_, err := c.call(ctx, request{
		method: http.MethodPut, path: "/containers/" + url.PathEscape(id) + "/archive", query: q,
		body: archive, contentType: "application/x-tar",
	}, nil)
	return err
}
