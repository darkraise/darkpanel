package docker

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

// rdStream replays frames and then ends with end (io.EOF when nil).
type rdStream struct {
	frames []dockerapi.LogFrame
	end    error
	closed bool
}

func (s *rdStream) Next() (dockerapi.LogFrame, error) {
	if len(s.frames) == 0 {
		if s.end != nil {
			return dockerapi.LogFrame{}, s.end
		}
		return dockerapi.LogFrame{}, io.EOF
	}
	f := s.frames[0]
	s.frames = s.frames[1:]
	return f, nil
}

func (s *rdStream) Close() error { s.closed = true; return nil }

// rdDocker answers Logs with a scripted stream and records how it was asked; everything else is the fake's.
type rdDocker struct {
	*fakeDocker
	stream *rdStream
	ids    []string
	opts   []dockerapi.LogsOptions
}

func (d *rdDocker) Logs(_ context.Context, id string, o dockerapi.LogsOptions) (logStream, error) {
	d.ids = append(d.ids, id)
	d.opts = append(d.opts, o)
	return d.stream, nil
}

func rdOut(s string) dockerapi.LogFrame { return dockerapi.LogFrame{Stream: "stdout", Data: []byte(s)} }
func rdErr(s string) dockerapi.LogFrame { return dockerapi.LogFrame{Stream: "stderr", Data: []byte(s)} }

// rdLogsHost puts the containers web (unlabelled) and proxy (owned by npm) on a host that holds secrets for
// its modules and whose Logs answers frames.
func rdLogsHost(t *testing.T, secrets [][]byte, end error, frames ...dockerapi.LogFrame) (*dkHost, dk, *rdDocker) {
	t.Helper()
	h := newDKHost(t)
	h.secrets = secrets
	h.docker.put(rdWeb())
	h.docker.put(rdWeb(rdOwned, func(c *dockerapi.ContainerJSON) { c.ID, c.Name = dkID("proxy"), "/proxy" }))
	d := &rdDocker{fakeDocker: h.docker, stream: &rdStream{frames: frames, end: end}}
	k := h.dk()
	k.d.docker = d
	return h, k, d
}

func TestRdLogsSplitsFramesIntoLinesPerStream(t *testing.T) {
	h, k, d := rdLogsHost(t, nil, nil, rdOut("one\ntw"), rdErr("oops\n"), rdOut("o\nthr"), rdOut("ee"))
	raw, err := rdRead(t, k.containerLogs(), h, `{"container":"web"}`)
	if err != nil {
		t.Fatal(err)
	}
	rdSame(t, raw, `{"lines":[
	  {"stream":"stdout","text":"one"},{"stream":"stderr","text":"oops"},{"stream":"stdout","text":"two"},{"stream":"stdout","text":"three"}]}`)
	want := dockerapi.LogsOptions{Tail: "200", Stdout: true, Stderr: true}
	if len(d.opts) != 1 || d.opts[0] != want || d.ids[0] != dkID("web") {
		t.Errorf("Logs asked %v for %v, want one request for %s with %+v", d.opts, d.ids, dkID("web"), want)
	}
	if !d.stream.closed {
		t.Error("the log stream was left open")
	}
}

func TestRdLogsPassesTheOptionsOn(t *testing.T) {
	h, k, d := rdLogsHost(t, nil, nil)
	raw, err := rdRead(t, k.containerLogs(), h, `{"container":"web","tail":5000,"since":"2026-10-01T10:00:00+02:00","timestamps":true}`)
	if err != nil {
		t.Fatal(err)
	}
	rdSame(t, raw, `{"lines":[]}`)
	if len(d.opts) != 1 {
		t.Fatalf("Logs asked %d times", len(d.opts))
	}
	o := d.opts[0]
	if o.Tail != "5000" || !o.Timestamps || !o.Since.Equal(time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)) || o.Follow || !o.Stdout || !o.Stderr {
		t.Errorf("options %+v", o)
	}
}

func TestRdLogsRefusesBadInputBeforeAskingTheEngine(t *testing.T) {
	for _, tc := range []struct{ name, input, path string }{
		{"an empty container", `{"container":""}`, "/container"},
		{"a negative tail", `{"container":"web","tail":-1}`, "/tail"},
		{"a tail above 5000", `{"container":"web","tail":5001}`, "/tail"},
		{"a since that is not RFC 3339", `{"container":"web","since":"yesterday"}`, "/since"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, k, d := rdLogsHost(t, nil, nil)
			_, err := rdRead(t, k.containerLogs(), h, tc.input)
			f := errcode.From(err).Fields
			if rdCode(err) != errcode.InvalidInput || len(f) != 1 || f[0].Path != tc.path {
				t.Fatalf("error %v (fields %+v), want request.invalid_input at %s", err, f, tc.path)
			}
			if len(d.opts) != 0 {
				t.Error("the engine was asked for logs")
			}
		})
	}
}

func TestRdLogsMapsEngineAnswers(t *testing.T) {
	h, k, _ := rdLogsHost(t, nil, nil)
	if _, err := rdRead(t, k.containerLogs(), h, `{"container":"ghost"}`); rdCode(err) != errcode.NotFound {
		t.Errorf("an absent container: %v", err)
	}
}

func TestRdLogsMasksTheSecretsOfAnOwnedContainerAcrossFrames(t *testing.T) {
	h, k, _ := rdLogsHost(t, [][]byte{[]byte("hunter2")}, nil, rdOut("token=hun"), rdErr("hunter2\n"), rdOut("ter2 end\nlast hun"), rdOut("t"))
	raw, err := rdRead(t, k.containerLogs(), h, `{"container":"proxy"}`)
	if err != nil {
		t.Fatal(err)
	}
	rdSame(t, raw, `{"lines":[
	  {"stream":"stderr","text":"[redacted]"},{"stream":"stdout","text":"token=[redacted] end"},{"stream":"stdout","text":"last hunt"}]}`)
}

func TestRdLogsLeavesAnUnownedContainerUnfiltered(t *testing.T) {
	h, k, _ := rdLogsHost(t, [][]byte{[]byte("hunter2")}, nil, rdOut("hunter2\n"))
	raw, err := rdRead(t, k.containerLogs(), h, `{"container":"web"}`)
	if err != nil {
		t.Fatal(err)
	}
	rdSame(t, raw, `{"lines":[{"stream":"stdout","text":"hunter2"}]}`)
}

func TestRdLogsFailsClosedWhenTheSecretsCannotBeRead(t *testing.T) {
	h, k, d := rdLogsHost(t, nil, nil, rdOut("hunter2\n"))
	k.d.secrets = func(context.Context, core.Env, string) ([][]byte, error) { return nil, errors.New("registry down") }
	raw, err := rdRead(t, k.containerLogs(), h, `{"container":"proxy"}`)
	if err == nil || len(raw) != 0 || len(d.opts) != 0 {
		t.Fatalf("answer %s, error %v, %d Logs requests: want an error before any request", raw, err, len(d.opts))
	}
}

func TestRdLogsReturnsAStreamFailureAndClosesTheStream(t *testing.T) {
	boom := errors.New("connection reset")
	h, k, d := rdLogsHost(t, nil, boom, rdOut("partial\n"))
	raw, err := rdRead(t, k.containerLogs(), h, `{"container":"web"}`)
	if !errors.Is(err, boom) || len(raw) != 0 {
		t.Fatalf("answer %s, error %v, want the stream's failure", raw, err)
	}
	if !d.stream.closed {
		t.Error("the log stream was left open")
	}
}
