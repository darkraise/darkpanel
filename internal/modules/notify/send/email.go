package send

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"time"
)

// email sends ev to the channel's recipients: over implicit TLS, after
// STARTTLS (never in the clear when the server does not offer it), or in
// the clear to the loopback host the channel was checked against.
func (s *sender) email(ctx context.Context, ch Channel, ev Event) (string, error) {
	from, err := mail.ParseAddress(ch.From)
	if err != nil {
		return CodeInvalid, errors.New("send: the sender address does not parse")
	}
	rcpts := make([]string, 0, len(ch.Recipients))
	for _, r := range ch.Recipients {
		a, err := mail.ParseAddress(r)
		if err != nil {
			return CodeInvalid, errors.New("send: a recipient address does not parse")
		}
		rcpts = append(rcpts, a.Address)
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	deadline, _ := ctx.Deadline()
	conf := &tls.Config{ServerName: ch.Host, RootCAs: s.roots, MinVersion: tls.VersionTLS12}
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(ch.Host, strconv.Itoa(ch.Port)))
	if err != nil {
		code := failureCode(err)
		return code, fmt.Errorf("send: the SMTP connection failed: %s", code)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	if ch.Security == "tls" {
		tc := tls.Client(conn, conf)
		if err := tc.HandshakeContext(ctx); err != nil {
			return smtpFailure(err, CodeTLS)
		}
		conn = tc
	}
	c, err := smtp.NewClient(conn, ch.Host)
	if err != nil {
		return smtpFailure(err, CodeNetwork)
	}
	defer func() { _ = c.Close() }()
	if ch.Security == "starttls" {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return CodeTLS, errors.New("send: the SMTP server offers no STARTTLS")
		}
		if err := c.StartTLS(conf); err != nil {
			return smtpFailure(err, CodeTLS)
		}
	}
	if ch.Username != "" {
		// PlainAuth refuses to send the password without TLS, except to a
		// loopback host.
		if err := c.Auth(smtp.PlainAuth("", ch.Username, ch.Password, ch.Host)); err != nil {
			return smtpFailure(err, CodeTLS)
		}
	}
	if err := c.Mail(from.Address); err != nil {
		return smtpFailure(err, CodeNetwork)
	}
	for _, r := range rcpts {
		if err := c.Rcpt(r); err != nil {
			return smtpFailure(err, CodeNetwork)
		}
	}
	w, err := c.Data()
	if err != nil {
		return smtpFailure(err, CodeNetwork)
	}
	if _, err := w.Write(message(ch, ev)); err != nil {
		return smtpFailure(err, CodeNetwork)
	}
	if err := w.Close(); err != nil {
		return smtpFailure(err, CodeNetwork)
	}
	_ = c.Quit()
	return "250", nil
}

// smtpFailure is the code of a failed SMTP step: the server's reply code,
// never its text; timeout or network for a broken connection; otherwise
// other.
func smtpFailure(err error, other string) (string, error) {
	var tpe *textproto.Error
	var ne net.Error
	code := other
	switch {
	case errors.As(err, &tpe):
		code = strconv.Itoa(tpe.Code)
		return code, fmt.Errorf("send: the SMTP server answered %s", code)
	case errors.Is(err, context.DeadlineExceeded) || errors.As(err, &ne) && ne.Timeout():
		code = CodeTimeout
	case errors.Is(err, net.ErrClosed) || errors.As(err, &ne):
		code = CodeNetwork
	}
	return code, fmt.Errorf("send: the SMTP exchange failed: %s", code)
}

// message is ev as a plain text mail from the channel's sender to its
// recipients, its body quoted-printable.
func message(ch Channel, ev Event) []byte {
	var b strings.Builder
	header := func(k, v string) { b.WriteString(k + ": " + v + "\r\n") }
	to := make([]string, len(ch.Recipients))
	for i, r := range ch.Recipients {
		to[i] = oneLine(r)
	}
	at := ev.CreatedAt
	if at.IsZero() {
		at = time.Now()
	}
	header("From", oneLine(ch.From))
	header("To", strings.Join(to, ", "))
	header("Subject", mime.QEncoding.Encode("utf-8", oneLine(ev.Title)))
	header("Date", at.UTC().Format(time.RFC1123Z))
	if ev.ID != "" {
		header("Message-ID", "<"+oneLine(ev.ID)+"@darkpanel>")
	}
	header("MIME-Version", "1.0")
	header("Content-Type", "text/plain; charset=utf-8")
	header("Content-Transfer-Encoding", "quoted-printable")
	b.WriteString("\r\n")
	qp := quotedprintable.NewWriter(&b)
	_, _ = qp.Write([]byte(strings.ReplaceAll(strings.ReplaceAll(ev.Body, "\r\n", "\n"), "\n", "\r\n")))
	_ = qp.Close()
	return []byte(b.String())
}
