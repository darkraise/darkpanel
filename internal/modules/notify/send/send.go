// Package send delivers one notification on one channel (M10 spec section
// 4): ntfy, Telegram, Discord, email and webhooks. A send is one attempt;
// retrying is the outbox's job. Redirects are refused, every attempt ends at
// the timeout, and no error carries a provider's body, a channel's URL or a
// credential.
package send

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Timeout bounds one attempt.
const Timeout = 15 * time.Second

// The codes of a failure that has no HTTP status or SMTP reply code.
const (
	CodeTimeout = "timeout"
	CodeNetwork = "network"
	CodeTLS     = "tls"
	CodeInvalid = "invalid"
)

// maxDrain is how much of a response body is read, and discarded, so the
// connection can be reused.
const maxDrain = 64 << 10

// Channel is what a send needs of a channel: its kind, its settings and its
// opened write-only values.
type Channel struct {
	ID, Kind string

	Server, Topic, Token string // ntfy
	BotToken, ChatID     string // telegram
	WebhookURL           string // discord

	Host                         string // email
	Port                         int
	Security, Username, Password string
	From                         string
	Recipients                   []string

	URL, Secret string // webhook
}

// Event is the notification sent.
type Event struct {
	ID, Kind, Severity, HostID, Title, Body string
	CreatedAt                               time.Time
}

// Sender makes one attempt to deliver ev on ch. code is the HTTP status or
// SMTP reply code the provider answered, or one of the Code constants; err
// is nil only for a delivery the provider accepted.
type Sender interface {
	Send(ctx context.Context, ch Channel, ev Event) (code string, err error)
}

// Options configures New; the zero value is production's.
type Options struct {
	Timeout      time.Duration  // 0 is Timeout
	TelegramBase string         // "" is https://api.telegram.org
	RootCAs      *x509.CertPool // nil is the system roots
}

type sender struct {
	timeout      time.Duration
	telegramBase string
	roots        *x509.CertPool
	client       *http.Client
}

// New returns the Sender of every channel kind, sharing one HTTP client.
func New(o Options) Sender {
	s := &sender{timeout: o.Timeout, telegramBase: o.TelegramBase, roots: o.RootCAs}
	if s.timeout <= 0 {
		s.timeout = Timeout
	}
	if s.telegramBase == "" {
		s.telegramBase = "https://api.telegram.org"
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{RootCAs: o.RootCAs, MinVersion: tls.VersionTLS12}
	s.client = &http.Client{
		Timeout:       s.timeout,
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return s
}

func (s *sender) Send(ctx context.Context, ch Channel, ev Event) (string, error) {
	switch ch.Kind {
	case "ntfy":
		return s.ntfy(ctx, ch, ev)
	case "telegram":
		return s.telegram(ctx, ch, ev)
	case "discord":
		return s.discord(ctx, ch, ev)
	case "email":
		return s.email(ctx, ch, ev)
	case "webhook":
		return s.webhook(ctx, ch, ev)
	}
	return CodeInvalid, fmt.Errorf("send: unknown channel kind %q", ch.Kind)
}

// failureCode is the code of a failed attempt: timeout or network. The
// error itself is dropped, since it names the URL.
func failureCode(err error) string {
	var ne net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &ne) && ne.Timeout() {
		return CodeTimeout
	}
	return CodeNetwork
}

// post makes one POST of body to target; what names the provider in errors.
func (s *sender) post(ctx context.Context, what, target string, header http.Header, body []byte) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return CodeInvalid, fmt.Errorf("send: the %s request could not be built", what)
	}
	for k, v := range header {
		req.Header[k] = v
	}
	resp, err := s.client.Do(req)
	if err != nil {
		code := failureCode(err)
		return code, fmt.Errorf("send: the %s request failed: %s", what, code)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxDrain))
	code := strconv.Itoa(resp.StatusCode)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return code, fmt.Errorf("send: %s answered HTTP %s", what, code)
	}
	return code, nil
}

// oneLine is s with every control character, line breaks included,
// replaced by a space, for a header.
func oneLine(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
}

// truncate is s cut to at most n runes, ending in an ellipsis when cut.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// text is the plain message of ev: its title, a blank line and its body.
func text(ev Event) string {
	return ev.Title + "\n\n" + ev.Body
}
