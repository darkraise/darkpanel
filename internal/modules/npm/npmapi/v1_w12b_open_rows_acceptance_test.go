package npmapi

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"syscall"
	"testing"
)

func TestA16_AnAnswerShorterThanItsLengthIsNoUsableAnswer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		conn, buf, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		defer func() { _ = conn.Close() }()
		_, _ = buf.WriteString("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 100\r\nConnection: close\r\n\r\n{\"id\":1")
		_ = buf.Flush()
	}))
	defer srv.Close()
	_, err := New(srv.URL, &http.Client{}).ProxyHost(context.Background(), 1)
	const want = "npmapi: read GET /api/nginx/proxy-hosts/1 answer: no usable answer"
	if err == nil || err.Error() != want {
		t.Errorf("error %v, want %q", err, want)
	}
}

func TestA16_ARefusedConnectionNeedsNoRealPort(t *testing.T) {
	dials := 0
	hc := &http.Client{Transport: &http.Transport{DialContext: func(context.Context, string, string) (net.Conn, error) {
		dials++
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}
	}}}
	_, err := New("http://npm.invalid", hc).Token(context.Background(), "admin@example.com", "placeholder-password")
	if err == nil || !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("error %v, want a refused connection", err)
	}
	if dials != 1 {
		t.Errorf("the client dialed %d times, want once through the dialer under test", dials)
	}
}

// The failure table in transport_test.go is the test the statement is about.
func TestA16_TheFailureTableCoversAShortReadAndDialsNoRealPortForARefusedConnection(t *testing.T) {
	src, err := os.ReadFile("transport_test.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)
	if !strings.Contains(text, "Content-Length: 100") {
		t.Error("transport_test.go has no row whose answer declares Content-Length: 100")
	}
	if !strings.Contains(text, "npmapi: read GET /api/nginx/proxy-hosts/1 answer: no usable answer") {
		t.Error("transport_test.go does not expect the short read error text")
	}
	if !strings.Contains(text, "syscall.ECONNREFUSED") {
		t.Error("transport_test.go has no dialer that returns syscall.ECONNREFUSED")
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, `"a refused connection"`) {
			for _, real := range []string{"srv.URL", "closedURL", "httptest", "127.0.0.1", "Listen"} {
				if strings.Contains(line, real) {
					t.Errorf("the refused-connection row %q still reaches a real port through %s", strings.TrimSpace(line), real)
				}
			}
			return
		}
	}
	t.Error("transport_test.go has no row named a refused connection")
}
