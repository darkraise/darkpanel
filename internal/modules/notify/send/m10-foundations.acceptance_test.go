package send_test

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/modules/notify/send"
)

// The test notification the spec names: sent to each kind's test server in
// that provider's format.
var qaTest = send.Event{ID: "01QATESTEVENT", Kind: "notify.test", Severity: "info",
	Title: "Test notification from Darkpanel", Body: "Channel qa can deliver Darkpanel notifications.", CreatedAt: time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)}

type qaRequest struct {
	method, path string
	header       http.Header
	body         []byte
}

func qaServer(t *testing.T, tls bool) (*httptest.Server, func() []qaRequest) {
	t.Helper()
	var mu sync.Mutex
	var got []qaRequest
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		got = append(got, qaRequest{r.Method, r.URL.Path, r.Header.Clone(), b})
		mu.Unlock()
		w.WriteHeader(200)
	})
	var srv *httptest.Server
	if tls {
		srv = httptest.NewTLSServer(h)
	} else {
		srv = httptest.NewServer(h)
	}
	t.Cleanup(srv.Close)
	return srv, func() []qaRequest {
		mu.Lock()
		defer mu.Unlock()
		return append([]qaRequest(nil), got...)
	}
}

func qaSend(t *testing.T, s send.Sender, ch send.Channel) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	code, err := s.Send(ctx, ch, qaTest)
	if err != nil {
		t.Fatalf("%s: send failed with %q: %v", ch.Kind, code, err)
	}
}

func TestA19_ntfy(t *testing.T) {
	srv, got := qaServer(t, false)
	qaSend(t, send.New(send.Options{}), send.Channel{Kind: "ntfy", Server: srv.URL, Topic: "qa-topic", Token: "tk_qa"})
	reqs := got()
	if len(reqs) != 1 {
		t.Fatalf("ntfy server saw %d requests, want 1", len(reqs))
	}
	r := reqs[0]
	if r.method != "POST" || r.path != "/qa-topic" {
		t.Errorf("ntfy request %s %s, want POST /qa-topic", r.method, r.path)
	}
	if string(r.body) != qaTest.Body {
		t.Errorf("ntfy body %q, want the message text %q", r.body, qaTest.Body)
	}
	if r.header.Get("Title") != qaTest.Title {
		t.Errorf("ntfy Title header %q, want %q", r.header.Get("Title"), qaTest.Title)
	}
	if r.header.Get("Authorization") != "Bearer tk_qa" {
		t.Errorf("ntfy Authorization %q, want a bearer token", r.header.Get("Authorization"))
	}
}

func TestA19_telegram(t *testing.T) {
	srv, got := qaServer(t, false)
	qaSend(t, send.New(send.Options{TelegramBase: srv.URL}), send.Channel{Kind: "telegram", BotToken: "123:QABOT", ChatID: "-1001"})
	reqs := got()
	if len(reqs) != 1 {
		t.Fatalf("Telegram server saw %d requests, want 1", len(reqs))
	}
	r := reqs[0]
	if r.method != "POST" || r.path != "/bot123:QABOT/sendMessage" {
		t.Errorf("Telegram request %s %s, want POST /bot<token>/sendMessage", r.method, r.path)
	}
	var m struct {
		ChatID string `json:"chat_id"`
		Text   string `json:"text"`
	}
	if err := json.Unmarshal(r.body, &m); err != nil || m.ChatID != "-1001" || !strings.Contains(m.Text, qaTest.Title) {
		t.Errorf("Telegram body %s (%v), want chat_id -1001 and the title in text", r.body, err)
	}
}

func TestA19_discord(t *testing.T) {
	srv, got := qaServer(t, true)
	pool := srv.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
	qaSend(t, send.New(send.Options{RootCAs: pool}), send.Channel{Kind: "discord", WebhookURL: srv.URL + "/api/webhooks/1/qa"})
	reqs := got()
	if len(reqs) != 1 {
		t.Fatalf("Discord server saw %d requests, want 1", len(reqs))
	}
	var m struct {
		Content string `json:"content"`
	}
	if r := reqs[0]; r.method != "POST" || r.path != "/api/webhooks/1/qa" || json.Unmarshal(r.body, &m) != nil || !strings.Contains(m.Content, qaTest.Title) {
		t.Errorf("Discord request %s %s %s, want POST to the webhook with content holding the title", r.method, r.path, r.body)
	}
}

func TestA19_webhook_signature(t *testing.T) {
	srv, got := qaServer(t, false)
	qaSend(t, send.New(send.Options{}), send.Channel{Kind: "webhook", URL: srv.URL + "/in", Secret: "qa-key"})
	reqs := got()
	if len(reqs) != 1 {
		t.Fatalf("webhook server saw %d requests, want 1", len(reqs))
	}
	r := reqs[0]
	mac := hmac.New(sha256.New, []byte("qa-key"))
	mac.Write(r.body)
	if want := "sha256=" + hex.EncodeToString(mac.Sum(nil)); r.header.Get("X-Darkpanel-Signature") != want {
		t.Errorf("X-Darkpanel-Signature %q, want %q (HMAC-SHA256 of the body)", r.header.Get("X-Darkpanel-Signature"), want)
	}
	var m struct {
		ID, Kind, Title string
	}
	if err := json.Unmarshal(r.body, &m); err != nil || m.ID != qaTest.ID || m.Kind != "notify.test" || m.Title != qaTest.Title {
		t.Errorf("webhook JSON body %s (%v), want the event id, kind and title", r.body, err)
	}
	if ct := r.header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("webhook Content-Type %q, want JSON", ct)
	}
	// Without a key no signature is sent.
	srv2, got2 := qaServer(t, false)
	qaSend(t, send.New(send.Options{}), send.Channel{Kind: "webhook", URL: srv2.URL})
	if got2()[0].header.Get("X-Darkpanel-Signature") != "" {
		t.Errorf("a webhook with no key sent a signature")
	}
}

// qaSMTP is a one-connection SMTP server that records the envelope and data.
func qaSMTP(t *testing.T) (port int, result func() (from string, rcpts []string, data string)) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	var mu sync.Mutex
	var from, data string
	var rcpts []string
	done := make(chan struct{})
	go func() {
		defer close(done)
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		rd := bufio.NewReader(c)
		say := func(s string) { _, _ = io.WriteString(c, s+"\r\n") }
		say("220 qa ESMTP")
		for {
			line, err := rd.ReadString('\n')
			if err != nil {
				return
			}
			up := strings.ToUpper(strings.TrimSpace(line))
			switch {
			case strings.HasPrefix(up, "EHLO"), strings.HasPrefix(up, "HELO"):
				say("250 qa")
			case strings.HasPrefix(up, "MAIL FROM:"):
				mu.Lock()
				from = strings.TrimSpace(line[len("MAIL FROM:"):])
				mu.Unlock()
				say("250 ok")
			case strings.HasPrefix(up, "RCPT TO:"):
				mu.Lock()
				rcpts = append(rcpts, strings.TrimSpace(line[len("RCPT TO:"):]))
				mu.Unlock()
				say("250 ok")
			case up == "DATA":
				say("354 go")
				var b strings.Builder
				for {
					l, err := rd.ReadString('\n')
					if err != nil || l == ".\r\n" {
						break
					}
					b.WriteString(l)
				}
				mu.Lock()
				data = b.String()
				mu.Unlock()
				say("250 queued")
			case up == "QUIT":
				say("221 bye")
				return
			default:
				say("250 ok")
			}
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port, func() (string, []string, string) {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
		mu.Lock()
		defer mu.Unlock()
		return from, rcpts, data
	}
}

func TestA19_email(t *testing.T) {
	port, result := qaSMTP(t)
	qaSend(t, send.New(send.Options{}), send.Channel{Kind: "email", Host: "127.0.0.1", Port: port, Security: "none",
		From: "darkpanel@example.test", Recipients: []string{"ops@example.test", "oncall@example.test"}})
	from, rcpts, data := result()
	if !strings.Contains(from, "darkpanel@example.test") {
		t.Errorf("MAIL FROM %q, want the channel's sender", from)
	}
	if len(rcpts) != 2 || !strings.Contains(rcpts[0], "ops@example.test") || !strings.Contains(rcpts[1], "oncall@example.test") {
		t.Errorf("RCPT TO %v, want both recipients", rcpts)
	}
	for _, want := range []string{"From: darkpanel@example.test", "To: ops@example.test, oncall@example.test", "Subject: Test notification from Darkpanel", "Channel qa can deliver Darkpanel notifications."} {
		if !strings.Contains(data, want) {
			t.Errorf("the message lacks %q:\n%s", want, data)
		}
	}
}
