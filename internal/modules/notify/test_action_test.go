package notify

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

func (e *env) channelTest(p core.Principal, id string) (TestResult, error) {
	raw, err := e.engine.Read(context.Background(), p, core.ActionRef{Name: "notify.channel.test", Version: 1},
		encode(e.t, ChannelIDInput{ID: id}))
	if err != nil {
		return TestResult{}, err
	}
	var out TestResult
	if err := json.Unmarshal(raw, &out); err != nil {
		e.t.Fatalf("notify.channel.test output %s: %v", raw, err)
	}
	return out, nil
}

func (e *env) count(table string) int {
	e.t.Helper()
	var n int
	if err := e.db.Read().QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

// smtpRefusing is a plain SMTP server on 127.0.0.1 that refuses every
// recipient with 550.
func smtpRefusing(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = c.Close() }()
				r, w := bufio.NewReader(c), bufio.NewWriter(c)
				reply := func(s string) { _, _ = w.WriteString(s + "\r\n"); _ = w.Flush() }
				reply("220 stub")
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					switch verb := strings.ToUpper(strings.Fields(line + " x")[0]); verb {
					case "RCPT":
						reply("550 mailbox unavailable, provider text")
					case "QUIT":
						reply("221 bye")
						return
					default:
						reply("250 ok")
					}
				}
			}()
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestChannelTestIsOneAttempt(t *testing.T) {
	e := newEnv(t, nil)
	var mu sync.Mutex
	var bodies []map[string]string
	status := http.StatusServiceUnavailable
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var body map[string]string
		_ = json.Unmarshal(b, &body)
		mu.Lock()
		bodies = append(bodies, body)
		s := status
		mu.Unlock()
		w.WriteHeader(s)
		_, _ = io.WriteString(w, "provider text")
	}))
	t.Cleanup(srv.Close)

	// The routing would match no test event, and the channel is off: the
	// test ignores both.
	var hook Channel
	e.run("notify.channel.create", ChannelInput{Kind: "webhook", Name: "hook", MinSeverity: "critical",
		Kinds: []string{"change.failed"}, Webhook: &WebhookInput{URL: srv.URL + "/hook"}}, &hook)
	e.run("notify.channel.update", ChannelUpdateInput{ID: hook.ID, Name: "hook", MinSeverity: "critical",
		Kinds: []string{"change.failed"}, Enabled: false, Webhook: &WebhookInput{}}, nil)
	changes := e.count("core_changes")
	audits := func() []core.AuditEntry {
		entries, _, err := e.audit.List(context.Background(), core.AuditQuery{Limit: 1000, Action: "notify.channel.test"})
		if err != nil {
			t.Fatal(err)
		}
		return entries
	}

	got, err := e.channelTest(admin, hook.ID)
	if err != nil || got != (TestResult{Delivered: false, Code: "503"}) {
		t.Fatalf("a 503 answered %+v, %v; want delivered false, code 503", got, err)
	}
	mu.Lock()
	sent := len(bodies)
	first := bodies[0]
	mu.Unlock()
	if sent != 1 || first["title"] != "Test notification from Darkpanel" || first["kind"] != core.NotifyTest {
		t.Errorf("%d sends, first %v; want one test notification", sent, first)
	}
	a := audits()
	if len(a) != 1 || a[0].Actor != admin.Actor || a[0].Result != "503" || !strings.Contains(string(a[0].Input), hook.ID) {
		t.Errorf("audit %+v; want one row by %s with result 503 naming %s", a, admin.Actor, hook.ID)
	}

	mu.Lock()
	status = http.StatusOK
	mu.Unlock()
	got, err = e.channelTest(admin, hook.ID)
	if err != nil || got != (TestResult{Delivered: true, Code: "200"}) {
		t.Errorf("a 200 answered %+v, %v", got, err)
	}
	if a := audits(); len(a) != 2 || a[0].Result != "delivered" {
		t.Errorf("audit after a delivery %+v", a)
	}

	var mail Channel
	e.run("notify.channel.create", ChannelInput{Kind: "email", Name: "mail", MinSeverity: "info", Email: &EmailInput{
		Host: "127.0.0.1", Port: smtpRefusing(t), Security: "none", From: "p@example.com", Recipients: []string{"a@example.com"}}}, &mail)
	changes++
	got, err = e.channelTest(admin, mail.ID)
	if err != nil || got != (TestResult{Delivered: false, Code: "550"}) {
		t.Errorf("an SMTP refusal answered %+v, %v; want code 550", got, err)
	}
	if n := e.count("core_changes"); n != changes {
		t.Errorf("the tests made %d changes", n-changes)
	}
	if a := audits(); len(a) != 3 {
		t.Errorf("%d audit rows after three tests", len(a))
	}

	// A member, or an admin's token below admin, is refused without a send.
	for _, p := range []core.Principal{
		{Actor: "user:u2", UserID: "u2", Source: "127.0.0.1", Access: core.Access{Fleet: true, All: core.ScopeOperate}},
		{Actor: "user:u1", UserID: "u1", Scope: core.ScopeRead, Source: "127.0.0.1", Access: core.Access{Fleet: true, All: core.ScopeRead}},
	} {
		_, err := e.channelTest(p, hook.ID)
		wantCode(t, err, errcode.Forbidden)
	}
	mu.Lock()
	sent = len(bodies)
	mu.Unlock()
	if sent != 2 || len(audits()) != 3 {
		t.Errorf("after the refusals: %d sends, %d audit rows; want 2 and 3", sent, len(audits()))
	}
	_, err = e.channelTest(admin, "nope")
	wantCode(t, err, errcode.NotFound)
	if _, err := strconv.Atoi(got.Code); err != nil {
		t.Errorf("code %q is not a reply code", got.Code)
	}
}
