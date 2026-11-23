package send

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

var event = Event{ID: "01EVENT", Kind: "change.failed", Severity: "critical", HostID: "H1",
	Title: "Change failed on web-1", Body: "docker.container.restart failed: agent.unreachable",
	CreatedAt: time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)}

// recorded is one request a test server received.
type recorded struct {
	method, path string
	header       http.Header
	body         []byte
}

// recorder is an HTTP server answering status and recording each request.
func recorder(t *testing.T, status int, answer string) (*httptest.Server, func() []recorded) {
	t.Helper()
	var mu sync.Mutex
	var got []recorded
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		got = append(got, recorded{r.Method, r.URL.Path, r.Header.Clone(), b})
		mu.Unlock()
		w.WriteHeader(status)
		_, _ = io.WriteString(w, answer)
	}))
	t.Cleanup(srv.Close)
	return srv, func() []recorded {
		mu.Lock()
		defer mu.Unlock()
		return append([]recorded(nil), got...)
	}
}

func one(t *testing.T, label string, reqs []recorded) recorded {
	t.Helper()
	if len(reqs) != 1 {
		t.Fatalf("%s: %d requests, want 1", label, len(reqs))
	}
	return reqs[0]
}

// smtpStub is an in-process SMTP server offering STARTTLS (or speaking
// implicit TLS) and AUTH PLAIN; reject, when set, refuses every recipient.
type smtpStub struct {
	addr     string
	implicit bool
	reject   string

	mu   sync.Mutex
	tls  bool // a TLS session carried MAIL
	auth string
	from string
	rcpt []string
	data string
}

func newSMTPStub(t *testing.T, implicit bool, reject string) (*smtpStub, *x509.CertPool) {
	t.Helper()
	// Borrow httptest's certificate for 127.0.0.1.
	certSrv := httptest.NewTLSServer(http.NotFoundHandler())
	t.Cleanup(certSrv.Close)
	conf := &tls.Config{Certificates: certSrv.TLS.Certificates, MinVersion: tls.VersionTLS12}
	pool := x509.NewCertPool()
	pool.AddCert(certSrv.Certificate())
	var ln net.Listener
	var err error
	if implicit {
		ln, err = tls.Listen("tcp", "127.0.0.1:0", conf)
	} else {
		ln, err = net.Listen("tcp", "127.0.0.1:0")
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	s := &smtpStub{addr: ln.Addr().String(), implicit: implicit, reject: reject}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(c, conf)
		}
	}()
	return s, pool
}

func (s *smtpStub) serve(c net.Conn, conf *tls.Config) {
	defer func() { _ = c.Close() }()
	inTLS := s.implicit
	tp := textproto.NewConn(c)
	_ = tp.PrintfLine("220 stub ESMTP")
	for {
		line, err := tp.ReadLine()
		if err != nil {
			return
		}
		verb, arg, _ := strings.Cut(line, " ")
		switch strings.ToUpper(verb) {
		case "EHLO", "HELO":
			if inTLS {
				_ = tp.PrintfLine("250-stub\r\n250 AUTH PLAIN")
			} else {
				_ = tp.PrintfLine("250-stub\r\n250-STARTTLS\r\n250 AUTH PLAIN")
			}
		case "STARTTLS":
			_ = tp.PrintfLine("220 go ahead")
			tc := tls.Server(c, conf)
			if err := tc.Handshake(); err != nil {
				return
			}
			c, inTLS = tc, true
			tp = textproto.NewConn(c)
		case "AUTH":
			_, b64, _ := strings.Cut(arg, " ")
			raw, _ := base64.StdEncoding.DecodeString(b64)
			s.mu.Lock()
			s.auth = string(raw)
			s.mu.Unlock()
			_ = tp.PrintfLine("235 ok")
		case "MAIL":
			s.mu.Lock()
			s.from, s.tls = arg, inTLS
			s.mu.Unlock()
			_ = tp.PrintfLine("250 ok")
		case "RCPT":
			if s.reject != "" {
				_ = tp.PrintfLine("550 %s", s.reject)
				continue
			}
			s.mu.Lock()
			s.rcpt = append(s.rcpt, arg)
			s.mu.Unlock()
			_ = tp.PrintfLine("250 ok")
		case "DATA":
			_ = tp.PrintfLine("354 go ahead")
			lines, err := tp.ReadDotLines()
			if err != nil {
				return
			}
			s.mu.Lock()
			s.data = strings.Join(lines, "\n")
			s.mu.Unlock()
			_ = tp.PrintfLine("250 queued")
		case "QUIT":
			_ = tp.PrintfLine("221 bye")
			return
		default:
			_ = tp.PrintfLine("250 ok")
		}
	}
}

func port(t *testing.T, addr string) int {
	t.Helper()
	_, p, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	n, err := strconv.Atoi(p)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestEachKindDelivers(t *testing.T) {
	ctx := context.Background()

	t.Run("ntfy", func(t *testing.T) {
		srv, reqs := recorder(t, http.StatusOK, `{"id":"x"}`)
		for _, c := range []struct{ severity, priority string }{{"critical", "5"}, {"warning", "4"}, {"info", "3"}} {
			ev := event
			ev.Severity = c.severity
			code, err := New(Options{}).Send(ctx, Channel{Kind: "ntfy", Server: srv.URL + "/", Topic: "alerts-x7", Token: "tk_1"}, ev)
			if err != nil || code != "200" {
				t.Fatalf("send: %q, %v", code, err)
			}
			r := reqs()[len(reqs())-1]
			if r.method != http.MethodPost || r.path != "/alerts-x7" || r.header.Get("Title") != event.Title ||
				r.header.Get("Priority") != c.priority || r.header.Get("Authorization") != "Bearer tk_1" || string(r.body) != event.Body {
				t.Errorf("%s: request %s %s %v %q", c.severity, r.method, r.path, r.header, r.body)
			}
		}
		if _, err := New(Options{}).Send(ctx, Channel{Kind: "ntfy", Server: srv.URL, Topic: "t"}, event); err != nil {
			t.Fatal(err)
		}
		if r := reqs()[len(reqs())-1]; r.header.Get("Authorization") != "" || r.path != "/t" {
			t.Errorf("without a token: %s %v", r.path, r.header)
		}
	})

	t.Run("telegram", func(t *testing.T) {
		srv, reqs := recorder(t, http.StatusOK, `{"ok":true}`)
		code, err := New(Options{TelegramBase: srv.URL}).Send(ctx, Channel{Kind: "telegram", BotToken: "123:abc", ChatID: "-100"}, event)
		if err != nil || code != "200" {
			t.Fatalf("send: %q, %v", code, err)
		}
		r := one(t, "telegram", reqs())
		var body struct {
			ChatID string `json:"chat_id"`
			Text   string `json:"text"`
		}
		if err := json.Unmarshal(r.body, &body); err != nil {
			t.Fatalf("body %q: %v", r.body, err)
		}
		if r.path != "/bot123:abc/sendMessage" || body.ChatID != "-100" || !strings.Contains(body.Text, event.Title) ||
			!strings.Contains(body.Text, event.Body) || r.header.Get("Content-Type") != "application/json" {
			t.Errorf("request %s %+v %v", r.path, body, r.header)
		}
	})

	t.Run("discord", func(t *testing.T) {
		srv, reqs := recorder(t, http.StatusNoContent, "")
		code, err := New(Options{}).Send(ctx, Channel{Kind: "discord", WebhookURL: srv.URL + "/api/webhooks/1/tok"}, event)
		if err != nil || code != "204" {
			t.Fatalf("send: %q, %v", code, err)
		}
		r := one(t, "discord", reqs())
		var body struct {
			Content string `json:"content"`
		}
		if err := json.Unmarshal(r.body, &body); err != nil {
			t.Fatalf("body %q: %v", r.body, err)
		}
		if r.path != "/api/webhooks/1/tok" || !strings.Contains(body.Content, event.Title) || !strings.Contains(body.Content, event.Body) {
			t.Errorf("request %s %+v", r.path, body)
		}
	})

	for _, implicit := range []bool{false, true} {
		name := map[bool]string{false: "email starttls", true: "email tls"}[implicit]
		t.Run(name, func(t *testing.T) {
			stub, pool := newSMTPStub(t, implicit, "")
			security := map[bool]string{false: "starttls", true: "tls"}[implicit]
			code, err := New(Options{RootCAs: pool}).Send(ctx, Channel{Kind: "email", Host: "127.0.0.1", Port: port(t, stub.addr),
				Security: security, Username: "ops", Password: "pw", From: "Panel <panel@example.com>",
				Recipients: []string{"a@example.com", "b@example.com"}}, event)
			if err != nil || code != "250" {
				t.Fatalf("send: %q, %v", code, err)
			}
			stub.mu.Lock()
			defer stub.mu.Unlock()
			if !stub.tls || stub.auth != "\x00ops\x00pw" || stub.from != "FROM:<panel@example.com>" ||
				strings.Join(stub.rcpt, ",") != "TO:<a@example.com>,TO:<b@example.com>" {
				t.Errorf("tls %v auth %q from %q rcpt %v", stub.tls, stub.auth, stub.from, stub.rcpt)
			}
			for _, want := range []string{"From: Panel <panel@example.com>", "To: a@example.com, b@example.com",
				"Subject: " + event.Title, "Content-Type: text/plain; charset=utf-8", event.Body} {
				if !strings.Contains(stub.data, want) {
					t.Errorf("message lacks %q:\n%s", want, stub.data)
				}
			}
		})
	}

	t.Run("webhook", func(t *testing.T) {
		srv, reqs := recorder(t, http.StatusAccepted, "")
		code, err := New(Options{}).Send(ctx, Channel{Kind: "webhook", URL: srv.URL + "/hook?x=1", Secret: "k3y"}, event)
		if err != nil || code != "202" {
			t.Fatalf("send: %q, %v", code, err)
		}
		r := one(t, "webhook", reqs())
		var body map[string]string
		if err := json.Unmarshal(r.body, &body); err != nil {
			t.Fatalf("body %q: %v", r.body, err)
		}
		want := map[string]string{"id": event.ID, "kind": event.Kind, "severity": event.Severity, "hostId": event.HostID,
			"title": event.Title, "body": event.Body, "createdAt": "2026-11-24T09:00:00Z"}
		for k, v := range want {
			if body[k] != v {
				t.Errorf("body %s = %q, want %q", k, body[k], v)
			}
		}
		if len(body) != len(want) {
			t.Errorf("body %v holds more than %v", body, want)
		}
		mac := hmac.New(sha256.New, []byte("k3y"))
		mac.Write(r.body)
		if got := r.header.Get("X-Darkpanel-Signature"); got != "sha256="+hex.EncodeToString(mac.Sum(nil)) {
			t.Errorf("signature %q", got)
		}
		if r.header.Get("Content-Type") != "application/json" {
			t.Errorf("content type %q", r.header.Get("Content-Type"))
		}
		if _, err := New(Options{}).Send(ctx, Channel{Kind: "webhook", URL: srv.URL}, event); err != nil {
			t.Fatal(err)
		}
		if got := reqs()[1].header.Values("X-Darkpanel-Signature"); len(got) != 0 {
			t.Errorf("without a key the request carries a signature %v", got)
		}
	})
}

func TestSendersRefuseRedirectsAndHideBodies(t *testing.T) {
	ctx := context.Background()
	const canary = "canary-body-51e2"

	target, hits := recorder(t, http.StatusOK, "")
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/elsewhere", http.StatusFound)
	}))
	t.Cleanup(redirect.Close)
	code, err := New(Options{}).Send(ctx, Channel{Kind: "webhook", URL: redirect.URL + "/hook"}, event)
	if err == nil || code != "302" || len(hits()) != 0 {
		t.Errorf("a redirect answered %q, %v; the target was hit %d times", code, err, len(hits()))
	}

	failing, _ := recorder(t, http.StatusInternalServerError, canary)
	for _, ch := range []Channel{
		{Kind: "webhook", URL: failing.URL + "/secret-path-x"},
		{Kind: "ntfy", Server: failing.URL, Topic: "secret-topic-x"},
		{Kind: "discord", WebhookURL: failing.URL + "/api/webhooks/1/secret-path-x"},
	} {
		code, err := New(Options{}).Send(ctx, ch, event)
		if err == nil || code != "500" {
			t.Errorf("%s: a 500 answered %q, %v", ch.Kind, code, err)
			continue
		}
		for _, hidden := range []string{canary, "secret-path-x", "secret-topic-x", failing.URL} {
			if strings.Contains(err.Error(), hidden) {
				t.Errorf("%s: the error %q holds %q", ch.Kind, err, hidden)
			}
		}
	}
	code, err = New(Options{TelegramBase: failing.URL}).Send(ctx, Channel{Kind: "telegram", BotToken: "9:secret-token-x", ChatID: "1"}, event)
	if err == nil || code != "500" || strings.Contains(err.Error(), "secret-token-x") || strings.Contains(err.Error(), canary) {
		t.Errorf("telegram: a 500 answered %q, %v", code, err)
	}

	// A refused connection names no URL either.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := "http://" + ln.Addr().String()
	_ = ln.Close()
	code, err = New(Options{TelegramBase: closed}).Send(ctx, Channel{Kind: "telegram", BotToken: "9:secret-token-x", ChatID: "1"}, event)
	if err == nil || code != "network" || strings.Contains(err.Error(), "secret-token-x") || strings.Contains(err.Error(), ln.Addr().String()) {
		t.Errorf("a refused connection answered %q, %v", code, err)
	}

	// The timeout: 15 s unless Options set another; a slower answer is a
	// failure at the timeout, with code timeout.
	if got := New(Options{}).(*sender).timeout; got != 15*time.Second {
		t.Errorf("the default timeout is %s, want 15s", got)
	}
	release := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { close(release); slow.Close() })
	start := time.Now()
	code, err = New(Options{Timeout: 200 * time.Millisecond}).Send(ctx, Channel{Kind: "webhook", URL: slow.URL}, event)
	if elapsed := time.Since(start); err == nil || code != "timeout" || elapsed > 5*time.Second {
		t.Errorf("a slow answer: %q, %v after %s", code, err, elapsed)
	}

	// An SMTP refusal reports its reply code, never the server's text.
	stub, pool := newSMTPStub(t, false, "no such user "+canary)
	code, err = New(Options{RootCAs: pool}).Send(ctx, Channel{Kind: "email", Host: "127.0.0.1", Port: port(t, stub.addr),
		Security: "starttls", From: "p@example.com", Recipients: []string{"a@example.com"}}, event)
	if err == nil || code != "550" || strings.Contains(err.Error(), canary) {
		t.Errorf("an SMTP refusal answered %q, %v", code, err)
	}
	// STARTTLS that the server does not offer is a failure, not plain text.
	plain, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = plain.Close() })
	go func() {
		c, err := plain.Accept()
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		w := bufio.NewWriter(c)
		r := bufio.NewReader(c)
		_, _ = w.WriteString("220 plain\r\n")
		_ = w.Flush()
		for {
			if _, err := r.ReadString('\n'); err != nil {
				return
			}
			_, _ = w.WriteString("250 ok\r\n")
			_ = w.Flush()
		}
	}()
	code, err = New(Options{}).Send(ctx, Channel{Kind: "email", Host: "127.0.0.1", Port: port(t, plain.Addr().String()),
		Security: "starttls", Username: "u", Password: "pw", From: "p@example.com", Recipients: []string{"a@example.com"}}, event)
	if err == nil || code != "tls" {
		t.Errorf("a server without STARTTLS answered %q, %v", code, err)
	}
}
