package cloudfake

import (
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func cfDo(t *testing.T, s *Server, method, path, body string) (*http.Response, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, s.URL()+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return s.Client().Do(req)
}

func cfBody(t *testing.T, res *http.Response) string {
	t.Helper()
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestHandleAndRecord(t *testing.T) {
	s := New(t)
	s.Handle(http.MethodPost, "/v2/keys", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write(b)
	})
	res, err := cfDo(t, s, http.MethodPost, "/v2/keys?x=1", `{"a":1}`)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusCreated || cfBody(t, res) != `{"a":1}` {
		t.Fatalf("answer = %d", res.StatusCode)
	}
	reqs := s.Requests()
	if len(reqs) != 1 {
		t.Fatalf("recorded %d requests, want 1", len(reqs))
	}
	got := reqs[0]
	if got.Method != http.MethodPost || got.Path != "/v2/keys" || got.RawQuery != "x=1" || string(got.Body) != `{"a":1}` {
		t.Fatalf("recorded %+v", got)
	}
}

func TestFaultStatusSkipsTheHandler(t *testing.T) {
	s := New(t)
	var calls atomic.Int32
	s.Handle(http.MethodGet, "/items", func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	})
	s.Fail(http.MethodGet, "/items", Fault{Status: http.StatusTooManyRequests, RetryAfter: "7", Body: "slow down", Times: 2})
	for i := range 2 {
		res, err := cfDo(t, s, http.MethodGet, "/items", "")
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != http.StatusTooManyRequests || res.Header.Get("Retry-After") != "7" || cfBody(t, res) != "slow down" {
			t.Fatalf("attempt %d: answer %d", i, res.StatusCode)
		}
	}
	res, err := cfDo(t, s, http.MethodGet, "/items", "")
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("third answer = %d, want 200 once the faults are used", res.StatusCode)
	}
	cfBody(t, res)
	if calls.Load() != 1 {
		t.Fatalf("handler ran %d times, want 1", calls.Load())
	}
	if n := len(s.Requests()); n != 3 {
		t.Fatalf("recorded %d requests, want 3 including the faulted ones", n)
	}
}

func TestFaultTimesZeroMeansOnce(t *testing.T) {
	s := New(t)
	s.Handle(http.MethodDelete, "/items/1", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	s.Fail(http.MethodDelete, "/items/1", Fault{Status: http.StatusBadGateway})
	want := []int{http.StatusBadGateway, http.StatusNoContent}
	for i, code := range want {
		res, err := cfDo(t, s, http.MethodDelete, "/items/1", "")
		if err != nil {
			t.Fatal(err)
		}
		cfBody(t, res)
		if res.StatusCode != code {
			t.Fatalf("answer %d = %d, want %d", i, res.StatusCode, code)
		}
	}
}

func TestFaultMatchesMethodAndPath(t *testing.T) {
	s := New(t)
	s.Handle(http.MethodGet, "/a", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	s.Handle(http.MethodPost, "/a", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	s.Fail(http.MethodPost, "/a", Fault{Status: http.StatusInternalServerError})
	res, err := cfDo(t, s, http.MethodGet, "/a", "")
	if err != nil {
		t.Fatal(err)
	}
	cfBody(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET answer = %d, want 200: the fault is for POST", res.StatusCode)
	}
}

func TestLoseResponseCommitsAndDropsTheAnswer(t *testing.T) {
	s := New(t)
	var committed atomic.Int32
	s.Handle(http.MethodPost, "/tunnels", func(w http.ResponseWriter, _ *http.Request) {
		committed.Add(1)
		w.WriteHeader(http.StatusOK)
	})
	s.Fail(http.MethodPost, "/tunnels", Fault{LoseResponse: true})
	res, err := cfDo(t, s, http.MethodPost, "/tunnels", "{}")
	if err == nil {
		cfBody(t, res)
		t.Fatalf("request answered %d, want a transport failure", res.StatusCode)
	}
	if committed.Load() != 1 {
		t.Fatalf("handler ran %d times, want 1: the write must commit", committed.Load())
	}
}

func TestRequestsReturnsCopies(t *testing.T) {
	s := New(t)
	s.Handle(http.MethodPut, "/c", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	res, err := cfDo(t, s, http.MethodPut, "/c", "abc")
	if err != nil {
		t.Fatal(err)
	}
	cfBody(t, res)
	first := s.Requests()
	first[0].Body[0] = 'X'
	first[0].Header.Set("X-Test", "changed")
	again := s.Requests()
	if string(again[0].Body) != "abc" || again[0].Header.Get("X-Test") != "" {
		t.Fatal("Requests returned the recorded request itself, not a copy")
	}
}
