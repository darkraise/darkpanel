package update

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core/coretest"
)

// indexServer answers /index/releases.json with the body and status that
// answer returns for the n-th request, counting from 1.
func indexServer(t *testing.T, answer func(n int64) (int, string)) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/index/releases.json" {
			http.NotFound(w, r)
			return
		}
		status, body := answer(hits.Add(1))
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

const indexBody = `{"formatVersion": 1, "releases": [{"version": "1.2.0", "channel": "stable"}, {"version": "1.3.0-beta.1", "channel": "beta"}]}`

func TestIndexSourceCachesForAnHour(t *testing.T) {
	srv, hits := indexServer(t, func(int64) (int, string) { return 200, indexBody })
	clock := coretest.NewFakeClock(time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC))
	src := NewIndexSource(srv.URL+"/", srv.Client(), clock)
	ctx := context.Background()
	want := []Release{{Version: "1.2.0", Channel: "stable"}, {Version: "1.3.0-beta.1", Channel: "beta"}}
	for _, step := range []struct {
		advance time.Duration
		hits    int64
	}{{0, 1}, {59 * time.Minute, 1}, {time.Minute, 2}, {30 * time.Minute, 2}} {
		clock.Advance(step.advance)
		got, err := src.Releases(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("Releases = %+v, want %+v", got, want)
		}
		if hits.Load() != step.hits {
			t.Fatalf("after %v: %d fetches, want %d", step.advance, hits.Load(), step.hits)
		}
	}
	got, _ := src.Releases(ctx)
	got[0].Version = "9.9.9"
	if again, _ := src.Releases(ctx); again[0].Version != "1.2.0" {
		t.Error("a caller's change reached the cached index")
	}
}

func TestIndexSourceDoesNotCacheFailures(t *testing.T) {
	srv, hits := indexServer(t, func(n int64) (int, string) {
		if n == 1 {
			return 500, "boom"
		}
		return 200, indexBody
	})
	src := NewIndexSource(srv.URL, srv.Client(), coretest.NewFakeClock(time.Unix(0, 0)))
	if _, err := src.Releases(context.Background()); err == nil {
		t.Fatal("a 500 answer was accepted")
	}
	if _, err := src.Releases(context.Background()); err != nil {
		t.Fatalf("the second fetch failed: %v", err)
	}
	if hits.Load() != 2 {
		t.Errorf("%d fetches, want 2", hits.Load())
	}
}

func TestIndexSourceRefusesBadIndexes(t *testing.T) {
	tests := map[string]struct {
		status int
		body   string
		want   string
	}{
		"not found":     {404, "", "404"},
		"format 2":      {200, `{"formatVersion": 2, "releases": []}`, "format"},
		"no format":     {200, `{"releases": []}`, "format"},
		"not json":      {200, `<html>`, "decode"},
		"over one MiB":  {200, `{"formatVersion": 1, "releases": [], "pad": "` + strings.Repeat("x", 1<<20) + `"}`, "1048576"},
		"trailing data": {200, indexBody + ` {}`, "decode"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			srv, _ := indexServer(t, func(int64) (int, string) { return tt.status, tt.body })
			_, err := NewIndexSource(srv.URL, srv.Client(), coretest.NewFakeClock(time.Unix(0, 0))).Releases(context.Background())
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Releases error = %v, want one mentioning %q", err, tt.want)
			}
		})
	}
}
