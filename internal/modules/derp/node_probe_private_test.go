package derp

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/derp/server"
)

// v1r16Reaching makes d's probes succeed at once and records what they were asked.
func v1r16Reaching(d dp) (dp, *[]string) {
	probed := &[]string{}
	d.d.tlsProbe = func(_ context.Context, target probeTarget) (time.Duration, error) {
		*probed = append(*probed, "tls "+target.HostName+" "+target.Address)
		return time.Millisecond, nil
	}
	d.d.stun = func(_ context.Context, addr netip.AddrPort) (time.Duration, error) {
		*probed = append(*probed, "stun "+addr.String())
		return time.Millisecond, nil
	}
	return d, probed
}

// v1r16Leaf is the DER of a self-signed certificate valid between the two times.
func v1r16Leaf(t *testing.T, notBefore, notAfter time.Time) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "203.0.113.9"},
		NotBefore: notBefore, NotAfter: notAfter, KeyUsage: x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return der
}

func TestV1r16NodeProbeNeedsOperate(t *testing.T) {
	h := newDPHost(t)
	probe := h.dp().nodeProbe().Spec
	if got := core.RequiredScope(probe); got != core.ScopeOperate {
		t.Errorf("derp.node.probe needs scope %s, want %s", got, core.ScopeOperate)
	}
	if probe.Class != core.ClassRead || probe.Side != core.SideAgent {
		t.Errorf("derp.node.probe is class %s on the %s, want a read on the agent", probe.Class, probe.Side)
	}
	if got := core.RequiredScope(h.dp().serverProbe().Spec); got != core.ScopeRead {
		t.Errorf("derp.server.probe needs scope %s, want %s: it builds its targets from server records", got, core.ScopeRead)
	}
}

// TestV1r16ProbeRefusesPrivateLiterals: RFC 1918, CGNAT and ULA targets, as host name or address and in their
// IPv4-mapped forms, are refused before anything is probed or resolved.
func TestV1r16ProbeRefusesPrivateLiterals(t *testing.T) {
	private := []string{
		"10.0.0.1", "10.255.255.254", "172.16.0.1", "172.31.255.254", "192.168.1.1", "192.168.255.254",
		"100.64.0.1", "100.127.255.254", "fc00::1", "fd12:3456:789a::1",
		"::ffff:10.0.0.1", "::ffff:192.168.0.1", "::ffff:100.64.0.1", "::ffff:172.16.0.1",
	}
	for _, ip := range private {
		for _, tc := range []struct{ field, input string }{
			{"/hostName", `{"hostName":"` + ip + `"}`},
			{"/address", `{"hostName":"derp.example.com","address":"` + ip + `"}`},
		} {
			t.Run(tc.field+" "+ip, func(t *testing.T) {
				h := newDPHost(t)
				_, err := npRead(t, npNoProbe(t, h.dp()), h.env(""), tc.input)
				coded := errcode.From(err)
				if err == nil || coded.Code != errcode.InvalidInput || !core.IsDefinite(err) || len(coded.Fields) == 0 || coded.Fields[0].Path != tc.field {
					t.Errorf("err = %v (fields %+v), want a definite request.invalid_input at %s", err, coded.Fields, tc.field)
				}
			})
		}
	}
	for _, ip := range []string{"172.15.255.255", "172.32.0.1", "100.63.255.255", "100.128.0.1", "192.167.255.255", "192.169.0.1", "9.255.255.255", "11.0.0.1", "2001:db8::1", "fbff:ffff::1"} {
		t.Run("public "+ip, func(t *testing.T) {
			h := newDPHost(t)
			d, probed := v1r16Reaching(h.dp())
			got, err := npRead(t, d, h.env(""), `{"hostName":"`+ip+`"}`)
			if err != nil || !got.TLS.OK || !got.STUN.OK || len(*probed) != 2 {
				t.Errorf("probe %+v, err %v, probed %v; want both probes to run", got, err, *probed)
			}
		})
	}
}

// TestV1r16ProbeResolveDropsPrivateAnswers: private, CGNAT, ULA and mapped answers are dropped before the first
// public one is chosen, and a name with only such answers is probed at nothing and reported as a plain failure.
func TestV1r16ProbeResolveDropsPrivateAnswers(t *testing.T) {
	addrs := func(ss ...string) []netip.Addr {
		var out []netip.Addr
		for _, s := range ss {
			out = append(out, netip.MustParseAddr(s))
		}
		return out
	}
	t.Run("a public answer among private ones", func(t *testing.T) {
		h := newDPHost(t)
		d, probed := v1r16Reaching(h.dp())
		d.d.resolve = func(context.Context, string) ([]netip.Addr, error) {
			return addrs("10.0.0.7", "192.168.1.5", "100.64.0.9", "fd00::1", "::ffff:172.16.0.4", "::ffff:10.1.1.1", "203.0.113.7"), nil
		}
		got, err := npRead(t, d, h.env(""), `{"hostName":"derp.example.com"}`)
		if err != nil || !got.TLS.OK || !got.STUN.OK {
			t.Fatalf("probe %+v, err %v, want both probes to run", got, err)
		}
		if want := []string{"tls derp.example.com 203.0.113.7", "stun 203.0.113.7:3478"}; !slices.Equal(*probed, want) {
			t.Errorf("probed %v, want %v", *probed, want)
		}
	})
	t.Run("only private answers", func(t *testing.T) {
		h := newDPHost(t)
		d, probed := v1r16Reaching(h.dp())
		d.d.resolve = func(context.Context, string) ([]netip.Addr, error) {
			return addrs("10.0.0.7", "192.168.1.5", "fd00::1", "::ffff:100.64.0.9"), nil
		}
		got, err := npRead(t, d, h.env(""), `{"hostName":"derp.example.com"}`)
		if err != nil {
			t.Fatal(err)
		}
		want := ProbeResult{Error: "failed"}
		if got.TLS != want || got.STUN != want {
			t.Errorf("tls %+v and stun %+v, want both %+v", got.TLS, got.STUN, want)
		}
		if len(*probed) != 0 {
			t.Errorf("probed %v, want nothing", *probed)
		}
		out, _ := json.Marshal(got)
		for _, leak := range []string{"10.0.0.7", "192.168.1.5", "derp.example.com"} {
			if strings.Contains(string(out), leak) {
				t.Errorf("the answer %s names %s", out, leak)
			}
		}
		_, _, err = d.target(context.Background(), ServerStatus{Installed: true, Mode: server.ModeLetsEncrypt, HostName: "derp.example.com"})
		if err == nil {
			t.Error("the controller's own target accepted a name with only private answers")
		}
	})
}

// TestV1r16ProbeErrorsUseTheFixedVocabulary: every failure of a TLS or STUN measurement is exactly one value of the
// vocabulary, wrapped as the probes wrap them, and no raw text, such as the internal name a certificate carries,
// reaches the result.
func TestV1r16ProbeErrorsUseTheFixedVocabulary(t *testing.T) {
	const internal = "intranet.corp.example"
	wrap := func(err error) error { return fmt.Errorf("derp: probe derp.example.com: %w", err) }
	hostErr := x509.HostnameError{Certificate: &x509.Certificate{DNSNames: []string{internal}}, Host: "derp.example.com"}
	refused := &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}
	stunAddr := npAddrPort("203.0.113.9:3478")
	run := func(t *testing.T, tlsErr, stunErr error) (ProbeResult, ProbeResult) {
		t.Helper()
		h := newDPHost(t)
		h.tls = map[string]dpProbe{"203.0.113.9": {d: time.Millisecond, err: tlsErr}}
		h.stun = map[netip.AddrPort]dpProbe{stunAddr: {d: time.Millisecond, err: stunErr}}
		return h.dp().probeOnce(context.Background(), probeTarget{HostName: "203.0.113.9", Port: 443}, stunAddr)
	}
	check := func(t *testing.T, got ProbeResult, want string) {
		t.Helper()
		if got.OK || got.LatencyMs != 0 || got.Error != want {
			t.Errorf("result %+v, want a failure %q", got, want)
		}
		out, _ := json.Marshal(got)
		for _, leak := range []string{internal, "derp.example.com", "boom"} {
			if strings.Contains(string(out), leak) {
				t.Errorf("the result %s carries %q", out, leak)
			}
		}
	}
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"a deadline", context.DeadlineExceeded, "timeout"},
		{"a dial timeout", &net.OpError{Op: "dial", Net: "tcp", Err: os.ErrDeadlineExceeded}, "timeout"},
		{"a refused connection", refused, "connection_refused"},
		{"no route to the network", &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.ENETUNREACH)}, "unreachable"},
		{"no route to the host", &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.EHOSTUNREACH)}, "unreachable"},
		{"a verification that names an internal certificate", &tls.CertificateVerificationError{Err: hostErr}, "certificate_not_trusted"},
		{"an unknown authority", &tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}}, "certificate_not_trusted"},
		{"a TLS alert", tls.AlertError(40), "tls_handshake_failed"},
		{"a server that does not speak TLS", tls.RecordHeaderError{Msg: "first record does not look like a TLS handshake"}, "tls_handshake_failed"},
		{"a plain HTTP answer to the handshake", http.ErrSchemeMismatch, "tls_handshake_failed"},
		{"anything else", errors.New("boom " + internal), "failed"},
	} {
		t.Run("tls "+tc.name, func(t *testing.T) {
			got, stun := run(t, wrap(tc.err), nil)
			check(t, got, tc.want)
			if !stun.OK {
				t.Errorf("STUN %+v, want OK", stun)
			}
		})
	}
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"no answer", fmt.Errorf("stun: no answer from %v: %w", stunAddr, context.DeadlineExceeded), "no_stun_answer"},
		{"a refused port", fmt.Errorf("stun: read from %v: %w", stunAddr, os.NewSyscallError("read", syscall.ECONNREFUSED)), "connection_refused"},
		{"no route", fmt.Errorf("stun: send to %v: %w", stunAddr, os.NewSyscallError("sendto", syscall.ENETUNREACH)), "unreachable"},
		{"anything else", errors.New("stun: open socket: boom " + internal), "failed"},
	} {
		t.Run("stun "+tc.name, func(t *testing.T) {
			tlsRes, got := run(t, nil, tc.err)
			check(t, got, tc.want)
			if !tlsRes.OK {
				t.Errorf("TLS %+v, want OK", tlsRes)
			}
		})
	}
}

// TestV1r16RealTLSFailuresUseTheVocabulary: the errors the production TLS probe produces against real local servers
// map to the vocabulary, so the classification does not rest on the shapes the test above builds by hand.
func TestV1r16RealTLSFailuresUseTheVocabulary(t *testing.T) {
	stun := func(context.Context, netip.AddrPort) (time.Duration, error) { return time.Millisecond, nil }
	d := dp{d: deps{tlsProbe: probeTLS, stun: stun}}
	probe := func(ctx context.Context, hostPort, pin string) ProbeResult {
		t.Helper()
		host, portText, err := net.SplitHostPort(hostPort)
		if err != nil {
			t.Fatal(err)
		}
		port, err := strconv.Atoi(portText)
		if err != nil {
			t.Fatal(err)
		}
		got, _ := d.probeOnce(ctx, probeTarget{HostName: host, Port: port, CertName: pin}, netip.AddrPortFrom(netip.MustParseAddr(host), server.STUNPort))
		return got
	}
	tlsServer := func(status int) (hostPort, pin string) {
		srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }))
		srv.Config.ErrorLog = log.New(io.Discard, "", 0)
		srv.StartTLS()
		t.Cleanup(srv.Close)
		return strings.TrimPrefix(srv.URL, "https://"), server.Pin(srv.Certificate().Raw)
	}
	ctx := context.Background()

	okAddr, okPin := tlsServer(http.StatusOK)
	if got := probe(ctx, okAddr, okPin); !got.OK || got.Error != "" {
		t.Errorf("a pinned server that answers 200: %+v, want OK", got)
	}
	failAddr, failPin := tlsServer(http.StatusInternalServerError)
	for _, tc := range []struct {
		name, hostPort, pin, want string
	}{
		{"an answer other than 200", failAddr, failPin, "bad_answer"},
		{"another pin", okAddr, server.PinPrefix + strings.Repeat("0", 64), "pin_mismatch"},
		{"a certificate no root signed", okAddr, "", "certificate_not_trusted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := probe(ctx, tc.hostPort, tc.pin); got.OK || got.Error != tc.want {
				t.Errorf("result %+v, want a failure %q", got, tc.want)
			}
		})
	}

	plain := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(plain.Close)
	if got := probe(ctx, strings.TrimPrefix(plain.URL, "http://"), ""); got.OK || got.Error != "tls_handshake_failed" {
		t.Errorf("a server that does not speak TLS: %+v, want tls_handshake_failed", got)
	}

	closed, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closedAddr := closed.Addr().String()
	_ = closed.Close()
	if got := probe(ctx, closedAddr, ""); got.OK || got.Error != "connection_refused" {
		t.Errorf("a port nobody listens on: %+v, want connection_refused", got)
	}

	silent, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var held []net.Conn
	go func() {
		for {
			c, err := silent.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			held = append(held, c)
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		_ = silent.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range held {
			_ = c.Close()
		}
	})
	short, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	if got := probe(short, silent.Addr().String(), ""); got.OK || got.Error != "timeout" {
		t.Errorf("a server that never answers: %+v, want timeout", got)
	}
}

// TestV1r16PinnedLeafFailuresUseTheVocabulary: the errors of the pin check itself.
func TestV1r16PinnedLeafFailuresUseTheVocabulary(t *testing.T) {
	now := time.Now()
	valid := v1r16Leaf(t, now.Add(-time.Hour), now.Add(time.Hour))
	expired := v1r16Leaf(t, now.Add(-48*time.Hour), now.Add(-24*time.Hour))
	stunAddr := npAddrPort("203.0.113.9:3478")
	for _, tc := range []struct {
		name string
		run  func() error
		want string
	}{
		{"a leaf with another pin", func() error {
			return pinnedLeaf(server.PinPrefix+strings.Repeat("0", 64))([][]byte{valid}, nil)
		}, "pin_mismatch"},
		{"an expired leaf with the right pin", func() error { return pinnedLeaf(server.Pin(expired))([][]byte{expired}, nil) }, "certificate_not_trusted"},
		{"no certificate", func() error { return pinnedLeaf(server.Pin(valid))(nil, nil) }, "certificate_not_trusted"},
		{"bytes that are no certificate", func() error { return pinnedLeaf(server.Pin(valid))([][]byte{[]byte("junk")}, nil) }, "certificate_not_trusted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run()
			if err == nil {
				t.Fatal("the pin check accepted the leaf")
			}
			h := newDPHost(t)
			h.tls = map[string]dpProbe{"203.0.113.9": {err: fmt.Errorf("derp: probe 203.0.113.9: %w", err)}}
			h.stun = map[netip.AddrPort]dpProbe{stunAddr: {d: time.Millisecond}}
			got, _ := h.dp().probeOnce(context.Background(), probeTarget{HostName: "203.0.113.9", Port: 443}, stunAddr)
			if got.OK || got.Error != tc.want {
				t.Errorf("result %+v, want a failure %q", got, tc.want)
			}
		})
	}
}

// TestV1r16RawProbeErrorsAreLoggedAtDebugOnly: the agent keeps the raw error of a failed probe in its debug log, and
// in no log of a higher level and no answer.
func TestV1r16RawProbeErrorsAreLoggedAtDebugOnly(t *testing.T) {
	const internal = "intranet.corp.example"
	raw := errors.New("derp: probe: certificate is valid for " + internal)
	for _, tc := range []struct {
		name   string
		level  slog.Level
		logged bool
	}{
		{"debug", slog.LevelDebug, true},
		{"info", slog.LevelInfo, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newDPHost(t)
			h.tls = map[string]dpProbe{"203.0.113.9": {err: raw}}
			h.stun = map[netip.AddrPort]dpProbe{npAddrPort("203.0.113.9:3478"): {err: raw}}
			var buf bytes.Buffer
			env := h.env("")
			env.Log = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: tc.level}))
			got, err := npRead(t, h.dp(), env, `{"hostName":"203.0.113.9"}`)
			if err != nil {
				t.Fatal(err)
			}
			if got.TLS.Error != "failed" || got.STUN.Error != "failed" {
				t.Errorf("tls %+v and stun %+v, want both failing as \"failed\"", got.TLS, got.STUN)
			}
			if out, _ := json.Marshal(got); strings.Contains(string(out), internal) {
				t.Errorf("the answer %s carries the raw error", out)
			}
			if logged := strings.Contains(buf.String(), internal); logged != tc.logged {
				t.Errorf("the raw error is in the %s-level log: %v, want %v (log: %s)", tc.name, logged, tc.logged, buf.String())
			}
		})
	}
}
