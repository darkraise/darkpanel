package dockerapi

import (
	"archive/tar"
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// stFrame encodes one multiplexed log frame.
func stFrame(stream byte, data string) []byte {
	hdr := make([]byte, 8, 8+len(data))
	hdr[0] = stream
	binary.BigEndian.PutUint32(hdr[4:], uint32(len(data))) //nolint:gosec // G115: test frames are a few bytes
	return append(hdr, data...)
}

func stReadAll(t *testing.T, s *LogStream) []LogFrame {
	t.Helper()
	var out []LogFrame
	for {
		f, err := s.Next()
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, f)
	}
}

func TestLogsMultiplexed(t *testing.T) {
	mux := http.NewServeMux()
	var query string
	mux.HandleFunc("GET /v1.49/containers/{id}/logs", func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/vnd.docker.multiplexed-stream")
		_, _ = w.Write(append(stFrame(1, "out line\n"), stFrame(2, "err line\n")...))
	})
	c := newFakeDaemon(t, "1.49", mux)
	s, err := c.Logs(t.Context(), "web", LogsOptions{
		Follow: true, Stdout: true, Stderr: true, Tail: "100",
		Since: time.Unix(1761210902, 5000).UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	frames := stReadAll(t, s)
	if len(frames) != 2 || frames[0].Stream != "stdout" || string(frames[0].Data) != "out line\n" ||
		frames[1].Stream != "stderr" || string(frames[1].Data) != "err line\n" {
		t.Fatalf("frames = %+v", frames)
	}
	if query != "follow=true&since=1761210902.000005000&stderr=true&stdout=true&tail=100&timestamps=false" {
		t.Fatalf("query = %s", query)
	}
}

func TestLogsNeedsAStream(t *testing.T) {
	mux := http.NewServeMux()
	reached := false
	mux.HandleFunc("/", func(http.ResponseWriter, *http.Request) { reached = true })
	c := newFakeDaemon(t, "1.49", mux)
	if _, err := c.Logs(t.Context(), "web", LogsOptions{Follow: true}); err == nil || !strings.Contains(err.Error(), "neither Stdout nor Stderr") {
		t.Fatalf("err = %v", err)
	}
	if reached {
		t.Fatal("a request without a stream selection reached the daemon")
	}
}

func TestLogsRawForTTY(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1.49/containers/{id}/logs", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.docker.raw-stream")
		_, _ = io.WriteString(w, "plain tty output\n")
	})
	s, err := newFakeDaemon(t, "1.49", mux).Logs(t.Context(), "tty", LogsOptions{Stdout: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	var b strings.Builder
	for _, f := range stReadAll(t, s) {
		if f.Stream != "stdout" {
			t.Fatalf("raw frame stream = %q", f.Stream)
		}
		b.Write(f.Data)
	}
	if b.String() != "plain tty output\n" {
		t.Fatalf("output = %q", b.String())
	}
}

func TestLogsMalformedFrames(t *testing.T) {
	big := make([]byte, 8)
	big[0] = 1
	binary.BigEndian.PutUint32(big[4:], maxFrame+1)
	cases := map[string]struct {
		body []byte
		want string
	}{
		"unknown stream":  {stFrame(7, "x"), "unknown stream 7"},
		"oversized frame": {big, "exceeds"},
		"short header":    {[]byte{1, 0, 0}, "truncated frame header"},
		"short payload":   {stFrame(1, "abcdef")[:10], "truncated frame"},
		"no payload":      {stFrame(1, "abcdef")[:8], "truncated frame"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("GET /v1.49/containers/{id}/logs", func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/vnd.docker.multiplexed-stream")
				_, _ = w.Write(tc.body)
			})
			s, err := newFakeDaemon(t, "1.49", mux).Logs(t.Context(), "web", LogsOptions{Stdout: true})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = s.Close() }()
			if _, err := s.Next(); err == nil || errors.Is(err, io.EOF) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestStatsOneSample(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1.49/containers/{id}/stats", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "stream=false" {
			t.Errorf("query = %s", r.URL.RawQuery)
		}
		_, _ = io.WriteString(w, `{"read":"2026-10-23T10:00:01Z","preread":"2026-10-23T10:00:00Z",`+
			`"cpu_stats":{"cpu_usage":{"total_usage":2000},"system_cpu_usage":100000,"online_cpus":4},`+
			`"precpu_stats":{"cpu_usage":{"total_usage":1000},"system_cpu_usage":90000,"online_cpus":4},`+
			`"memory_stats":{"usage":5242880,"limit":1073741824,"stats":{"inactive_file":1048576}},`+
			`"pids_stats":{"current":3},"networks":{"eth0":{"rx_bytes":10,"tx_bytes":20}}}`)
	})
	s, err := newFakeDaemon(t, "1.49", mux).Stats(t.Context(), "web", false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	st, err := s.Next()
	if err != nil {
		t.Fatal(err)
	}
	if st.CPUStats.CPUUsage.TotalUsage != 2000 || st.PreCPUStats.SystemCPUUsage != 90000 || st.CPUStats.OnlineCPUs != 4 ||
		st.MemoryStats.Stats["inactive_file"] != 1048576 || st.PidsStats.Current != 3 || st.Networks["eth0"].TxBytes != 20 {
		t.Fatalf("stats = %+v", st)
	}
	if _, err := s.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("second sample err = %v, want io.EOF", err)
	}
}

func TestEvents(t *testing.T) {
	mux := http.NewServeMux()
	var query string
	mux.HandleFunc("GET /v1.49/events", func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		_, _ = io.WriteString(w, `{"status":"start","id":"c1","Type":"container","Action":"start",`+
			`"Actor":{"ID":"c1","Attributes":{"name":"web","darkpanel.module":"npm"}},"scope":"local","time":1761210902,"timeNano":1761210902000000001}`+"\n")
	})
	s, err := newFakeDaemon(t, "1.49", mux).Events(t.Context(), EventsOptions{
		Since:   time.Unix(1761210900, 0),
		Filters: Filters{"type": {"container", "network"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	ev, err := s.Next()
	if err != nil {
		t.Fatal(err)
	}
	if ev.Type != "container" || ev.Action != "start" || ev.Actor.Attributes["name"] != "web" || ev.TimeNano != 1761210902000000001 {
		t.Fatalf("event = %+v", ev)
	}
	if query != "filters=%7B%22type%22%3A%5B%22container%22%2C%22network%22%5D%7D&since=1761210900.000000000" {
		t.Fatalf("query = %s", query)
	}
}

func TestArchiveCopy(t *testing.T) {
	var tarball bytes.Buffer
	tw := tar.NewWriter(&tarball)
	if err := tw.WriteHeader(&tar.Header{Name: "hello", Mode: 0o644, Size: 5}); err != nil {
		t.Fatal(err)
	}
	_, _ = tw.Write([]byte("world"))
	_ = tw.Close()

	mux := http.NewServeMux()
	var put []byte
	mux.HandleFunc("PUT /v1.49/containers/{id}/archive", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "noOverwriteDirNonDir=true&path=%2Fdata" || r.Header.Get("Content-Type") != "application/x-tar" {
			t.Errorf("query %s, content type %s", r.URL.RawQuery, r.Header.Get("Content-Type"))
		}
		put, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /v1.49/containers/{id}/archive", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("path") != "/data/hello" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"message":"Could not find the file"}`)
			return
		}
		w.Header().Set("Content-Type", "application/x-tar")
		_, _ = w.Write(put)
	})
	c := newFakeDaemon(t, "1.49", mux)
	ctx := t.Context()
	if err := c.CopyToContainer(ctx, "npm", "/data", bytes.NewReader(tarball.Bytes())); err != nil {
		t.Fatal(err)
	}
	rc, err := c.CopyFromContainer(ctx, "npm", "/data/hello")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rc.Close() }()
	tr := tar.NewReader(rc)
	hdr, err := tr.Next()
	if err != nil || hdr.Name != "hello" {
		t.Fatalf("archive entry = %+v, %v", hdr, err)
	}
	if b, _ := io.ReadAll(tr); string(b) != "world" {
		t.Fatalf("content = %q", b)
	}
	if _, err := c.CopyFromContainer(ctx, "npm", "/missing"); !IsNotFound(err) {
		t.Fatalf("missing path err = %v", err)
	}
}
