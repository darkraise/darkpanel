package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/docker/logfilter"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

// stDocker wraps the fake engine with log and stats streams the test controls,
// and records how the streams were opened.
type stDocker struct {
	*fakeDocker
	frames  []dockerapi.LogFrame
	samples []dockerapi.Stats
	block   bool  // after the frames, Next waits for the context instead of ending the stream
	endErr  error // after the frames, Next fails with this instead of io.EOF

	logIDs    []string
	logOpts   []dockerapi.LogsOptions
	statIDs   []string
	statFlags []bool
	closed    int
}

func (d *stDocker) Logs(ctx context.Context, id string, o dockerapi.LogsOptions) (logStream, error) {
	d.logIDs = append(d.logIDs, id)
	d.logOpts = append(d.logOpts, o)
	return &stLogStream{ctx: ctx, d: d, frames: d.frames}, nil
}

func (d *stDocker) Stats(ctx context.Context, id string, stream bool) (statsStream, error) {
	d.statIDs = append(d.statIDs, id)
	d.statFlags = append(d.statFlags, stream)
	return &stStatsStream{ctx: ctx, d: d, samples: d.samples}, nil
}

// stEnd is what a stream answers once its scripted items are used up.
func (d *stDocker) stEnd(ctx context.Context) error {
	switch {
	case d.endErr != nil:
		return d.endErr
	case d.block:
		<-ctx.Done()
		return ctx.Err()
	}
	return io.EOF
}

type stLogStream struct {
	ctx    context.Context
	d      *stDocker
	frames []dockerapi.LogFrame
}

func (s *stLogStream) Next() (dockerapi.LogFrame, error) {
	if len(s.frames) == 0 {
		return dockerapi.LogFrame{}, s.d.stEnd(s.ctx)
	}
	f := s.frames[0]
	s.frames = s.frames[1:]
	return f, nil
}

func (s *stLogStream) Close() error { s.d.closed++; return nil }

type stStatsStream struct {
	ctx     context.Context
	d       *stDocker
	samples []dockerapi.Stats
}

func (s *stStatsStream) Next() (dockerapi.Stats, error) {
	if len(s.samples) == 0 {
		return dockerapi.Stats{}, s.d.stEnd(s.ctx)
	}
	v := s.samples[0]
	s.samples = s.samples[1:]
	return v, nil
}

func (s *stStatsStream) Close() error { s.d.closed++; return nil }

// stSetup puts the containers into a fresh host and points a dk at the wrapped engine.
func stSetup(t *testing.T, cs ...dockerapi.ContainerJSON) (*dkHost, dk, *stDocker) {
	t.Helper()
	h := newDKHost(t)
	for _, c := range cs {
		h.docker.put(c)
	}
	d := &stDocker{fakeDocker: h.docker}
	k := h.dk()
	k.d.docker = d
	return h, k, d
}

// stCollect runs def with the JSON params and returns the events it emitted.
func stCollect(ctx context.Context, h *dkHost, def core.StreamDef, params string) ([]any, error) {
	var got []any
	err := def.Run(ctx, h.env(""), json.RawMessage(params), func(e any) error {
		got = append(got, e)
		return nil
	})
	return got, err
}

// stText joins the text of the log events of one stream.
func stText(events []any, stream string) string {
	var b strings.Builder
	for _, e := range events {
		if ev := e.(LogEvent); ev.Stream == stream {
			b.WriteString(ev.Text)
		}
	}
	return b.String()
}

func stOwned(name string) dockerapi.ContainerJSON {
	return dkContainer(name, dkRunning, dkLabels(map[string]string{core.LabelModule: "npm"}))
}

func stFrame(stream, text string) dockerapi.LogFrame {
	return dockerapi.LogFrame{Stream: stream, Data: []byte(text)}
}

func TestStStreamDefs(t *testing.T) {
	k := newDKHost(t).dk()
	logs, stats := k.logsStream(), k.statsStream()
	if logs.Name != "logs" || logs.ParamsType != reflect.TypeFor[LogsParams]() || logs.EventType != reflect.TypeFor[LogEvent]() {
		t.Errorf("logs stream = %s, %v, %v; want logs, LogsParams, LogEvent", logs.Name, logs.ParamsType, logs.EventType)
	}
	if stats.Name != "stats" || stats.ParamsType != reflect.TypeFor[StatsParams]() || stats.EventType != reflect.TypeFor[StatsEvent]() {
		t.Errorf("stats stream = %s, %v, %v; want stats, StatsParams, StatsEvent", stats.Name, stats.ParamsType, stats.EventType)
	}
}

func TestStLogsEmitsFramesInOrder(t *testing.T) {
	h, k, d := stSetup(t, dkContainer("web", dkRunning))
	d.frames = []dockerapi.LogFrame{stFrame("stdout", "one\n"), stFrame("stderr", "two\n")}
	got, err := stCollect(context.Background(), h, k.logsStream(), `{"container":"web"}`)
	want := []any{LogEvent{Stream: "stdout", Text: "one\n"}, LogEvent{Stream: "stderr", Text: "two\n"}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("events %v (%v), want %v", got, err, want)
	}
	if !reflect.DeepEqual(d.logIDs, []string{dkID("web")}) || d.closed != 1 {
		t.Errorf("logs opened for %v and closed %d times, want the container's full id once", d.logIDs, d.closed)
	}
}

func TestStLogsOptions(t *testing.T) {
	noon := time.Date(2026, 10, 30, 11, 0, 0, 0, time.UTC)
	tests := []struct {
		name, params string
		want         dockerapi.LogsOptions
	}{
		{"defaults", `{"container":"web"}`, dockerapi.LogsOptions{Stdout: true, Stderr: true, Tail: "200"}},
		{"follow and every line", `{"container":"web","live":true,"tail":"all"}`, dockerapi.LogsOptions{Follow: true, Stdout: true, Stderr: true}},
		{"count and since", `{"container":"web","tail":"5","since":"2026-10-30T11:00:00Z"}`, dockerapi.LogsOptions{Stdout: true, Stderr: true, Tail: "5", Since: noon}},
		{"since with an offset", `{"container":"web","since":"2026-10-30T13:00:00+02:00"}`, dockerapi.LogsOptions{Stdout: true, Stderr: true, Tail: "200", Since: noon}},
		{"no lines", `{"container":"web","tail":"0"}`, dockerapi.LogsOptions{Stdout: true, Stderr: true, Tail: "0"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, k, d := stSetup(t, dkContainer("web", dkRunning))
			if _, err := stCollect(context.Background(), h, k.logsStream(), tc.params); err != nil {
				t.Fatal(err)
			}
			if len(d.logOpts) != 1 {
				t.Fatalf("logs opened %d times, want once", len(d.logOpts))
			}
			got := d.logOpts[0]
			if !got.Since.Equal(tc.want.Since) {
				t.Errorf("since %v, want %v", got.Since, tc.want.Since)
			}
			got.Since, tc.want.Since = time.Time{}, time.Time{}
			if got != tc.want {
				t.Errorf("options %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestStLogsRefusesBadParams(t *testing.T) {
	tests := []struct{ name, params, path string }{
		{"empty container", `{"container":""}`, "/container"},
		{"no container", `{}`, "/container"},
		{"since not a time", `{"container":"web","since":"yesterday"}`, "/since"},
		{"negative tail", `{"container":"web","tail":"-1"}`, "/tail"},
		{"tail not a number", `{"container":"web","tail":"many"}`, "/tail"},
		{"fractional tail", `{"container":"web","tail":"1.5"}`, "/tail"},
		{"unknown member", `{"container":"web","bogus":1}`, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, k, d := stSetup(t, dkContainer("web", dkRunning))
			got, err := stCollect(context.Background(), h, k.logsStream(), tc.params)
			e := errcode.From(err)
			if e.Code != errcode.InvalidInput || len(got) != 0 {
				t.Fatalf("got %v and %v, want %s and no events", got, err, errcode.InvalidInput)
			}
			if tc.path != "" && (len(e.Fields) != 1 || e.Fields[0].Path != tc.path) {
				t.Errorf("fields %+v, want one at %s", e.Fields, tc.path)
			}
			if calls := h.docker.calls(); len(calls) != 0 || len(d.logIDs) != 0 {
				t.Errorf("the engine was called (%v, %v) before the parameters were checked", calls, d.logIDs)
			}
		})
	}
}

func TestStLogsEngineRefusals(t *testing.T) {
	t.Run("unknown container", func(t *testing.T) {
		h, k, d := stSetup(t)
		_, err := stCollect(context.Background(), h, k.logsStream(), `{"container":"ghost"}`)
		if e := errcode.From(err); e.Code != errcode.NotFound || len(d.logIDs) != 0 {
			t.Errorf("err = %v with logs opened for %v, want %s and none", err, d.logIDs, errcode.NotFound)
		}
	})
	t.Run("engine unavailable", func(t *testing.T) {
		h, k, _ := stSetup(t, dkContainer("web", dkRunning))
		h.docker.fail("InspectContainer", fmt.Errorf("%w: dial unix", dockerapi.ErrUnavailable))
		_, err := stCollect(context.Background(), h, k.logsStream(), `{"container":"web"}`)
		if e := errcode.From(err); e.Code != errcode.DockerEngineUnavailable {
			t.Errorf("err = %v, want %s", err, errcode.DockerEngineUnavailable)
		}
	})
}

func TestStLogsMasksSecretsOfOwnedContainers(t *testing.T) {
	h, k, d := stSetup(t, stOwned("npm-app"), dkContainer("plain", dkRunning))
	var asked []string
	k.d.secrets = func(_ context.Context, _ core.Env, id string) ([][]byte, error) {
		asked = append(asked, id)
		return [][]byte{[]byte("hunter2")}, nil
	}

	d.frames = []dockerapi.LogFrame{stFrame("stdout", "login pass=hun"), stFrame("stdout", "ter2 ok\n"), stFrame("stderr", "hunter2\n")}
	got, err := stCollect(context.Background(), h, k.logsStream(), `{"container":"npm-app"}`)
	if err != nil {
		t.Fatal(err)
	}
	if out, errText := stText(got, "stdout"), stText(got, "stderr"); out != "login pass="+logfilter.Mask+" ok\n" || errText != logfilter.Mask+"\n" {
		t.Errorf("stdout %q and stderr %q, want the secret masked in both, across frames", out, errText)
	}
	if !reflect.DeepEqual(asked, []string{dkID("npm-app")}) {
		t.Errorf("secrets asked for %v, want the owned container's id", asked)
	}

	asked = nil
	d.frames = []dockerapi.LogFrame{stFrame("stdout", "pass=hunter2\n")}
	got, err = stCollect(context.Background(), h, k.logsStream(), `{"container":"plain"}`)
	if text := stText(got, "stdout"); err != nil || text != "pass=hunter2\n" || len(asked) != 0 {
		t.Errorf("unlabelled container: text %q, secrets asked %v (%v), want the text unchanged and no lookup", text, asked, err)
	}
}

func TestStLogsFlushesHeldBytesAtTheEnd(t *testing.T) {
	h, k, d := stSetup(t, stOwned("npm-app"))
	k.d.secrets = func(context.Context, core.Env, string) ([][]byte, error) { return [][]byte{[]byte("hunter2")}, nil }
	d.frames = []dockerapi.LogFrame{stFrame("stdout", "ends with hunt")}
	got, err := stCollect(context.Background(), h, k.logsStream(), `{"container":"npm-app"}`)
	if text := stText(got, "stdout"); err != nil || text != "ends with hunt" {
		t.Errorf("text %q (%v), want the held partial match emitted when the stream ends", text, err)
	}
}

func TestStLogsSecretLookupFailure(t *testing.T) {
	h, k, d := stSetup(t, stOwned("npm-app"))
	broken := errors.New("sealed store unreadable")
	k.d.secrets = func(context.Context, core.Env, string) ([][]byte, error) { return nil, broken }
	d.frames = []dockerapi.LogFrame{stFrame("stdout", "pass=hunter2\n")}
	got, err := stCollect(context.Background(), h, k.logsStream(), `{"container":"npm-app"}`)
	if !errors.Is(err, broken) || len(got) != 0 || len(d.logIDs) != 0 {
		t.Errorf("got %v and %v with logs opened for %v, want the lookup error, no events and no stream", got, err, d.logIDs)
	}
}

func TestStLogsSplitsLargeFrames(t *testing.T) {
	h, k, d := stSetup(t, dkContainer("web", dkRunning))
	out := strings.Repeat("a", 20000)
	errText := strings.Repeat("€", 5000)
	d.frames = []dockerapi.LogFrame{stFrame("stdout", out), stFrame("stderr", errText)}
	got, err := stCollect(context.Background(), h, k.logsStream(), `{"container":"web"}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range got {
		ev := e.(LogEvent)
		if len(ev.Text) > 8192 || !utf8.ValidString(ev.Text) {
			t.Fatalf("an event of %d bytes (valid UTF-8: %v), want at most 8192 bytes and whole characters", len(ev.Text), utf8.ValidString(ev.Text))
		}
	}
	if stText(got, "stdout") != out || stText(got, "stderr") != errText {
		t.Error("the events do not join to the frames' text")
	}
	if len(got) < 4 {
		t.Errorf("%d events for 35000 bytes, want the frames split", len(got))
	}
}

func TestStLogsEnds(t *testing.T) {
	t.Run("reader gone", func(t *testing.T) {
		h, k, d := stSetup(t, dkContainer("web", dkRunning))
		d.frames = []dockerapi.LogFrame{stFrame("stdout", "a\n"), stFrame("stdout", "b\n"), stFrame("stdout", "c\n")}
		gone := errors.New("reader gone")
		emitted := 0
		err := k.logsStream().Run(context.Background(), h.env(""), json.RawMessage(`{"container":"web"}`), func(any) error {
			emitted++
			return gone
		})
		if !errors.Is(err, gone) || emitted != 1 || d.closed != 1 {
			t.Errorf("err = %v after %d events, stream closed %d times; want the emit error after 1 event and one close", err, emitted, d.closed)
		}
	})
	t.Run("context cancelled while following", func(t *testing.T) {
		h, k, d := stSetup(t, dkContainer("web", dkRunning))
		d.frames, d.block = []dockerapi.LogFrame{stFrame("stdout", "a\n")}, true
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		err := k.logsStream().Run(ctx, h.env(""), json.RawMessage(`{"container":"web","live":true}`), func(any) error {
			cancel()
			return nil
		})
		if err != nil || d.closed != 1 {
			t.Errorf("err = %v, stream closed %d times; want nil and one close", err, d.closed)
		}
	})
	t.Run("stream fails", func(t *testing.T) {
		h, k, d := stSetup(t, dkContainer("web", dkRunning))
		d.frames, d.endErr = []dockerapi.LogFrame{stFrame("stdout", "a\n")}, errors.New("connection reset")
		got, err := stCollect(context.Background(), h, k.logsStream(), `{"container":"web"}`)
		if !errors.Is(err, d.endErr) || len(got) != 1 || d.closed != 1 {
			t.Errorf("got %v and %v, closed %d times; want the frame, the stream's error and one close", got, err, d.closed)
		}
	})
}

func TestStLogsFlushesHeldBytesOnEveryEnd(t *testing.T) {
	secrets := func(context.Context, core.Env, string) ([][]byte, error) { return [][]byte{[]byte("hunter2")}, nil }
	frames := []dockerapi.LogFrame{stFrame("stdout", "out hunt"), stFrame("stderr", "err hun")}
	t.Run("stream fails", func(t *testing.T) {
		h, k, d := stSetup(t, stOwned("npm-app"))
		k.d.secrets = secrets
		d.frames, d.endErr = frames, errors.New("connection reset")
		got, err := stCollect(context.Background(), h, k.logsStream(), `{"container":"npm-app"}`)
		if out, errText := stText(got, "stdout"), stText(got, "stderr"); !errors.Is(err, d.endErr) || out != "out hunt" || errText != "err hun" {
			t.Errorf("stdout %q and stderr %q (%v), want the held bytes of both streams and the stream's error", out, errText, err)
		}
	})
	t.Run("context cancelled while following", func(t *testing.T) {
		h, k, d := stSetup(t, stOwned("npm-app"))
		k.d.secrets = secrets
		d.frames, d.block = frames, true
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var got []any
		err := k.logsStream().Run(ctx, h.env(""), json.RawMessage(`{"container":"npm-app","live":true}`), func(e any) error {
			got = append(got, e)
			cancel()
			return nil
		})
		if out, errText := stText(got, "stdout"), stText(got, "stderr"); err != nil || out != "out hunt" || errText != "err hun" || d.closed != 1 {
			t.Errorf("stdout %q and stderr %q (%v), closed %d times; want the held bytes of both streams, nil and one close", out, errText, err, d.closed)
		}
	})
}

func TestStLogsMasksASecretAcrossLines(t *testing.T) {
	h, k, d := stSetup(t, stOwned("npm-app"))
	k.d.secrets = func(context.Context, core.Env, string) ([][]byte, error) {
		return [][]byte{[]byte("BEGIN KEY\nmiddle\nEND KEY")}, nil
	}
	d.frames = []dockerapi.LogFrame{stFrame("stdout", "key: BEGIN KEY\n"), stFrame("stdout", "middle\nEND"), stFrame("stdout", " KEY\nok\n")}
	got, err := stCollect(context.Background(), h, k.logsStream(), `{"container":"npm-app","live":true}`)
	if text := stText(got, "stdout"); err != nil || text != "key: "+logfilter.Mask+"\nok\n" {
		t.Errorf("text %q (%v), want the multi-line secret masked across frames", text, err)
	}
	if len(d.logOpts) != 1 || d.logOpts[0].Timestamps {
		t.Errorf("logs opened with %+v, want one open without timestamps so frames reach the filter whole", d.logOpts)
	}
}

func TestStLogFilter(t *testing.T) {
	broken := errors.New("sealed store unreadable")
	tests := []struct {
		name     string
		labels   map[string]string
		secrets  [][]byte
		err      error
		wantAsk  bool
		wantPush string
	}{
		{"unlabelled container is not filtered", nil, [][]byte{[]byte("hunter2")}, nil, false, "pass=hunter2"},
		{"owned container is filtered", map[string]string{core.LabelModule: "npm"}, [][]byte{[]byte("hunter2")}, nil, true, "pass=" + logfilter.Mask},
		{"owned container without secrets", map[string]string{core.LabelModule: "npm"}, nil, nil, true, "pass=hunter2"},
		{"lookup error is returned as is", map[string]string{core.LabelModule: "npm"}, nil, broken, true, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newDKHost(t)
			k := h.dk()
			var asked []string
			k.d.secrets = func(_ context.Context, _ core.Env, id string) ([][]byte, error) {
				asked = append(asked, id)
				return tc.secrets, tc.err
			}
			c := dkContainer("app", dkLabels(tc.labels))
			f, err := k.logFilter(context.Background(), h.env(""), c)
			if tc.err != nil {
				if !errors.Is(err, tc.err) || f != nil {
					t.Errorf("logFilter = %v, %v; want a nil filter and %v unchanged", f, err, tc.err)
				}
			} else if got := string(f.Push("stdout", []byte("pass=hunter2"))); err != nil || got != tc.wantPush {
				t.Errorf("filtered %q (%v), want %q", got, err, tc.wantPush)
			}
			if tc.wantAsk && !reflect.DeepEqual(asked, []string{c.ID}) {
				t.Errorf("secrets asked for %v, want the container's id", asked)
			}
			if !tc.wantAsk && len(asked) != 0 {
				t.Errorf("secrets asked for %v, want no lookup for an unlabelled container", asked)
			}
		})
	}
}

func stSample() dockerapi.Stats {
	return dockerapi.Stats{
		Read:        "2026-10-30T12:00:01.5Z",
		CPUStats:    dockerapi.CPUStats{CPUUsage: dockerapi.CPUUsage{TotalUsage: 300}, SystemCPUUsage: 2000, OnlineCPUs: 2},
		PreCPUStats: dockerapi.CPUStats{CPUUsage: dockerapi.CPUUsage{TotalUsage: 100}, SystemCPUUsage: 1000, OnlineCPUs: 2},
		MemoryStats: dockerapi.MemoryStats{Usage: 1000, Limit: 4096, Stats: map[string]uint64{"inactive_file": 200}},
		PidsStats:   dockerapi.PidsStats{Current: 7},
		Networks: map[string]dockerapi.NetworkStats{
			"eth0": {RxBytes: 10, TxBytes: 20},
			"eth1": {RxBytes: 5, TxBytes: 7},
		},
	}
}

func TestStStatsEmitsSamples(t *testing.T) {
	h, k, d := stSetup(t, dkContainer("web", dkRunning))
	idle := dockerapi.Stats{
		Read:        "2026-10-30T12:00:02.5Z",
		CPUStats:    dockerapi.CPUStats{CPUUsage: dockerapi.CPUUsage{TotalUsage: 300}, SystemCPUUsage: 2000, OnlineCPUs: 2},
		PreCPUStats: dockerapi.CPUStats{CPUUsage: dockerapi.CPUUsage{TotalUsage: 300}, SystemCPUUsage: 2000, OnlineCPUs: 2},
		MemoryStats: dockerapi.MemoryStats{Usage: 500, Limit: 4096},
	}
	d.samples = []dockerapi.Stats{stSample(), idle}
	got, err := stCollect(context.Background(), h, k.statsStream(), `{"container":"web"}`)
	if err != nil || len(got) != 2 {
		t.Fatalf("events %v (%v), want two", got, err)
	}
	first, second := got[0].(StatsEvent), got[1].(StatsEvent)
	if math.Abs(first.CPUPercent-40) > 1e-9 {
		t.Errorf("first sample cpuPercent = %v, want 40 (200/1000 of the host's time over 2 CPUs)", first.CPUPercent)
	}
	first.CPUPercent = 0
	if want := (StatsEvent{Read: "2026-10-30T12:00:01.5Z", MemUsage: 800, MemLimit: 4096, NetRx: 15, NetTx: 27, Pids: 7}); first != want {
		t.Errorf("first event %+v, want %+v", first, want)
	}
	if want := (StatsEvent{Read: "2026-10-30T12:00:02.5Z", MemUsage: 500, MemLimit: 4096}); second != want {
		t.Errorf("second event %+v, want %+v", second, want)
	}
	if !reflect.DeepEqual(d.statIDs, []string{dkID("web")}) || !reflect.DeepEqual(d.statFlags, []bool{true}) || d.closed != 1 {
		t.Errorf("stats opened for %v (stream %v), closed %d times; want the full id once, streaming, closed once", d.statIDs, d.statFlags, d.closed)
	}
}

func TestStStatsRefusals(t *testing.T) {
	tests := []struct {
		name, params string
		code         errcode.Code
	}{
		{"empty container", `{"container":""}`, errcode.InvalidInput},
		{"unknown member", `{"container":"web","follow":true}`, errcode.InvalidInput},
		{"unknown container", `{"container":"ghost"}`, errcode.NotFound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, k, d := stSetup(t, dkContainer("web", dkRunning))
			got, err := stCollect(context.Background(), h, k.statsStream(), tc.params)
			if e := errcode.From(err); e.Code != tc.code || len(got) != 0 || len(d.statIDs) != 0 {
				t.Errorf("got %v and %v with stats opened for %v, want %s and no stream", got, err, d.statIDs, tc.code)
			}
		})
	}
}

func TestStStatsEnds(t *testing.T) {
	t.Run("reader gone", func(t *testing.T) {
		h, k, d := stSetup(t, dkContainer("web", dkRunning))
		d.samples = []dockerapi.Stats{stSample(), stSample()}
		gone := errors.New("reader gone")
		emitted := 0
		err := k.statsStream().Run(context.Background(), h.env(""), json.RawMessage(`{"container":"web"}`), func(any) error {
			emitted++
			return gone
		})
		if !errors.Is(err, gone) || emitted != 1 || d.closed != 1 {
			t.Errorf("err = %v after %d events, closed %d times; want the emit error after 1 event and one close", err, emitted, d.closed)
		}
	})
	t.Run("context cancelled", func(t *testing.T) {
		h, k, d := stSetup(t, dkContainer("web", dkRunning))
		d.samples, d.block = []dockerapi.Stats{stSample()}, true
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		err := k.statsStream().Run(ctx, h.env(""), json.RawMessage(`{"container":"web"}`), func(any) error {
			cancel()
			return nil
		})
		if err != nil || d.closed != 1 {
			t.Errorf("err = %v, closed %d times; want nil and one close", err, d.closed)
		}
	})
	t.Run("stream fails", func(t *testing.T) {
		h, k, d := stSetup(t, dkContainer("web", dkRunning))
		d.endErr = errors.New("connection reset")
		_, err := stCollect(context.Background(), h, k.statsStream(), `{"container":"web"}`)
		if !errors.Is(err, d.endErr) || d.closed != 1 {
			t.Errorf("err = %v, closed %d times; want the stream's error and one close", err, d.closed)
		}
	})
}
