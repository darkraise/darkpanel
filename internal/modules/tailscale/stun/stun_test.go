package stun

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"net"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	snBindingSuccess = 0x0101
	snBindingError   = 0x0111
	snMapped         = 0x0001
	snXorMapped      = 0x0020
	snSoftware       = 0x8022
)

// snTx is the transaction id of RFC 5769's sample responses.
var snTx = [12]byte{0xb7, 0xe7, 0xa7, 0x01, 0xbc, 0x34, 0xd6, 0x86, 0xfa, 0x87, 0xdf, 0xae}

// snMessage builds a STUN message of type typ from complete attributes.
func snMessage(typ uint16, tx [12]byte, attrs ...[]byte) []byte {
	body := bytes.Join(attrs, nil)
	b := binary.BigEndian.AppendUint16(nil, typ)
	b = binary.BigEndian.AppendUint16(b, uint16(len(body))) //nolint:gosec // G115: the test attributes are a few bytes
	b = binary.BigEndian.AppendUint32(b, 0x2112a442)
	b = append(b, tx[:]...)
	return append(b, body...)
}

// snAttr builds one attribute: type, length, the value and zero padding to a multiple of four bytes.
func snAttr(typ uint16, value []byte) []byte {
	a := binary.BigEndian.AppendUint16(nil, typ)
	a = binary.BigEndian.AppendUint16(a, uint16(len(value))) //nolint:gosec // G115: the test values are a few bytes
	a = append(a, value...)
	for len(a)%4 != 0 {
		a = append(a, 0)
	}
	return a
}

// snAddress is the value of a MAPPED-ADDRESS attribute, or of an XOR-MAPPED-ADDRESS one when tx is not nil
// (RFC 5389 sections 15.1 and 15.2): a zero byte, the family, the port and the address, the last two XORed with
// the magic cookie, and for IPv6 the transaction id, when tx is given.
func snAddress(ap netip.AddrPort, tx *[12]byte) []byte {
	family, port, addr := byte(1), ap.Port(), ap.Addr().AsSlice()
	if ap.Addr().Is6() {
		family = 2
	}
	if tx != nil {
		mask := append([]byte{0x21, 0x12, 0xa4, 0x42}, tx[:]...)
		port ^= 0x2112
		for i := range addr {
			addr[i] ^= mask[i]
		}
	}
	v := binary.BigEndian.AppendUint16([]byte{0, family}, port)
	return append(v, addr...)
}

func snHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(strings.Join(strings.Fields(s), ""))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSnRequestEncodesABindingRequest(t *testing.T) {
	got := Request(snTx)
	want := snHex(t, "0001 0000 2112a442 b7e7a701 bc34d686 fa87dfae")
	if !bytes.Equal(got, want) {
		t.Fatalf("Request = %x, want %x", got, want)
	}
}

func TestSnParseAcceptsBindingSuccess(t *testing.T) {
	v4 := netip.MustParseAddrPort("192.0.2.1:32853")
	v6 := netip.MustParseAddrPort("[2001:db8:1234:5678:11:2233:4455:6677]:32853")
	other := netip.MustParseAddrPort("198.51.100.9:1")
	tests := []struct {
		name string
		msg  []byte
		want netip.AddrPort
	}{
		{
			// RFC 5769 section 2.2 without its MESSAGE-INTEGRITY and FINGERPRINT attributes.
			"RFC 5769 IPv4 response",
			snHex(t, "0101 001c 2112a442 b7e7a701 bc34d686 fa87dfae  8022 000b 74657374 20766563 746f7220  0020 0008 0001 a147 e112a643"),
			v4,
		},
		{
			// RFC 5769 section 2.3 without its MESSAGE-INTEGRITY and FINGERPRINT attributes.
			"RFC 5769 IPv6 response",
			snHex(t, "0101 0018 2112a442 b7e7a701 bc34d686 fa87dfae  0020 0014 0002 a147 0113a9fa a5d3f179 bc25f4b5 bed2b9d9"),
			v6,
		},
		{"mapped address only", snMessage(snBindingSuccess, snTx, snAttr(snMapped, snAddress(v4, nil))), v4},
		{"mapped IPv6 address only", snMessage(snBindingSuccess, snTx, snAttr(snMapped, snAddress(v6, nil))), v6},
		{
			"xor-mapped address built by the helper",
			snMessage(snBindingSuccess, snTx, snAttr(snXorMapped, snAddress(v6, &snTx))),
			v6,
		},
		{
			"xor-mapped wins when it follows a mapped address",
			snMessage(snBindingSuccess, snTx, snAttr(snMapped, snAddress(other, nil)), snAttr(snXorMapped, snAddress(v4, &snTx))),
			v4,
		},
		{
			"xor-mapped wins when it precedes a mapped address",
			snMessage(snBindingSuccess, snTx, snAttr(snXorMapped, snAddress(v4, &snTx)), snAttr(snMapped, snAddress(other, nil))),
			v4,
		},
		{
			"unknown attributes with odd lengths are skipped",
			snMessage(snBindingSuccess, snTx, snAttr(0x8000, []byte{1, 2, 3}), snAttr(snSoftware, []byte("x")), snAttr(snXorMapped, snAddress(v4, &snTx))),
			v4,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tx, got, err := Parse(tt.msg)
			if err != nil || tx != snTx || got != tt.want {
				t.Fatalf("Parse = %x, %v, %v; want %x, %v, nil", tx, got, err, snTx, tt.want)
			}
		})
	}
}

func TestSnParseRefuses(t *testing.T) {
	v4 := netip.MustParseAddrPort("192.0.2.1:32853")
	good := snMessage(snBindingSuccess, snTx, snAttr(snXorMapped, snAddress(v4, &snTx)))
	badCookie := slices.Clone(good)
	badCookie[4] ^= 0xff
	longer := slices.Clone(good)
	longer[3] += 4
	odd := append(snMessage(snBindingSuccess, snTx), 0, 0)
	odd[3] = 2
	tests := []struct {
		name string
		msg  []byte
		want string
	}{
		{"empty", nil, "shorter than the 20-byte header"},
		{"one byte short of a header", good[:19], "shorter than the 20-byte header"},
		{"wrong magic cookie", badCookie, "magic cookie"},
		{"declared length past the datagram", longer, "does not match"},
		{"bytes after the declared length", append(slices.Clone(good), 0, 0, 0, 0), "does not match"},
		{"declared length not a multiple of four", odd, "does not match"},
		{"binding request", Request(snTx), "not a binding success response"},
		{"binding error response", snMessage(snBindingError, snTx, snAttr(0x0009, []byte{0, 0, 4, 1})), "not a binding success response"},
		{"attribute longer than the message", snMessage(snBindingSuccess, snTx, []byte{0x00, 0x20, 0x00, 0x08, 0x00, 0x01, 0xa1, 0x47}), "overruns"},
		{"unknown address family", snMessage(snBindingSuccess, snTx, snAttr(snXorMapped, []byte{0, 3, 0, 0, 1, 2, 3, 4})), "malformed"},
		{"IPv4 value of the wrong length", snMessage(snBindingSuccess, snTx, snAttr(snXorMapped, []byte{0, 1, 0, 0, 1, 2, 3, 4, 5, 6, 7, 8})), "malformed"},
		{"IPv6 value too short", snMessage(snBindingSuccess, snTx, snAttr(snXorMapped, []byte{0, 2, 0, 0, 1, 2, 3, 4})), "malformed"},
		{"mapped address of the wrong length", snMessage(snBindingSuccess, snTx, snAttr(snMapped, []byte{0, 1, 0, 0})), "malformed"},
		{"no attributes", snMessage(snBindingSuccess, snTx), "no mapped address"},
		{"only a software attribute", snMessage(snBindingSuccess, snTx, snAttr(snSoftware, []byte("test vector"))), "no mapped address"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tx, got, err := Parse(tt.msg)
			if err == nil || !strings.HasPrefix(err.Error(), "stun: ") || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Parse = %x, %v, %v; want an error containing %q", tx, got, err, tt.want)
			}
			if tx != ([12]byte{}) || got.IsValid() {
				t.Fatalf("Parse returned %x, %v with an error", tx, got)
			}
		})
	}
}

// snListen starts a UDP server on 127.0.0.1 and calls reply in its goroutine for each datagram it receives.
func snListen(t *testing.T, reply func(conn net.PacketConn, req []byte, from net.Addr)) netip.AddrPort {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 1500)
		for {
			n, from, err := conn.ReadFrom(buf)
			if err != nil {
				return
			}
			reply(conn, slices.Clone(buf[:n]), from)
		}
	}()
	t.Cleanup(func() {
		_ = conn.Close()
		<-done
	})
	ap := conn.LocalAddr().(*net.UDPAddr).AddrPort()
	return netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port())
}

// snTxOf returns the transaction id of a request, failing the test when it is not a bare Binding request.
func snTxOf(t *testing.T, req []byte) [12]byte {
	t.Helper()
	if len(req) != 20 || req[0] != 0x00 || req[1] != 0x01 || req[2] != 0x00 || req[3] != 0x00 || !bytes.Equal(req[4:8], []byte{0x21, 0x12, 0xa4, 0x42}) {
		t.Errorf("request = %x, want a 20-byte Binding request with no attributes", req)
	}
	return [12]byte(req[8:20])
}

// snAnswer writes the Binding success response that reports from as the mapped address.
func snAnswer(conn net.PacketConn, tx [12]byte, from net.Addr) {
	ap := from.(*net.UDPAddr).AddrPort()
	_, _ = conn.WriteTo(snMessage(snBindingSuccess, tx, snAttr(snXorMapped, snAddress(ap, &tx))), from)
}

func TestSnProbeMeasuresARoundTrip(t *testing.T) {
	const delay = 40 * time.Millisecond
	var mu sync.Mutex
	var txs [][12]byte
	server := snListen(t, func(conn net.PacketConn, req []byte, from net.Addr) {
		tx := snTxOf(t, req)
		mu.Lock()
		txs = append(txs, tx)
		mu.Unlock()
		time.Sleep(delay)
		snAnswer(conn, tx, from)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for range 2 {
		d, err := Probe(ctx, server)
		if err != nil || d < delay || d > 3*time.Second {
			t.Fatalf("Probe = %v, %v; want a duration of at least %v", d, err, delay)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(txs) != 2 || txs[0] == txs[1] {
		t.Fatalf("transaction ids = %x, want two different ids", txs)
	}
}

func TestSnProbeIgnoresDatagramsThatAreNotItsAnswer(t *testing.T) {
	const delay = 60 * time.Millisecond
	v4 := netip.MustParseAddrPort("192.0.2.1:32853")
	server := snListen(t, func(conn net.PacketConn, req []byte, from net.Addr) {
		tx := snTxOf(t, req)
		other := tx
		other[0] ^= 0xff
		for _, junk := range [][]byte{
			[]byte("not a stun message"),
			snMessage(snBindingSuccess, other, snAttr(snXorMapped, snAddress(v4, &other))),
			snMessage(snBindingError, tx, snAttr(0x0009, []byte{0, 0, 4, 1})),
			snMessage(snBindingSuccess, tx, snAttr(snSoftware, []byte("no address"))),
		} {
			_, _ = conn.WriteTo(junk, from)
		}
		time.Sleep(delay)
		snAnswer(conn, tx, from)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	d, err := Probe(ctx, server)
	if err != nil || d < delay {
		t.Fatalf("Probe = %v, %v; want success only after the real answer, at least %v", d, err, delay)
	}
}

func TestSnProbeEndsWithTheContext(t *testing.T) {
	server := snListen(t, func(net.PacketConn, []byte, net.Addr) {})

	t.Run("deadline", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
		defer cancel()
		start := time.Now()
		d, err := Probe(ctx, server)
		if !errors.Is(err, context.DeadlineExceeded) || d != 0 || time.Since(start) > 3*time.Second {
			t.Fatalf("Probe = %v, %v after %v; want context.DeadlineExceeded", d, err, time.Since(start))
		}
	})
	t.Run("cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		time.AfterFunc(50*time.Millisecond, cancel)
		start := time.Now()
		d, err := Probe(ctx, server)
		if !errors.Is(err, context.Canceled) || d != 0 || time.Since(start) > 3*time.Second {
			t.Fatalf("Probe = %v, %v after %v; want context.Canceled", d, err, time.Since(start))
		}
	})
	t.Run("no deadline", func(t *testing.T) {
		start := time.Now()
		d, err := probe(context.Background(), server, 150*time.Millisecond)
		if !errors.Is(err, context.DeadlineExceeded) || d != 0 || time.Since(start) > 3*time.Second {
			t.Fatalf("probe = %v, %v after %v; want context.DeadlineExceeded", d, err, time.Since(start))
		}
	})
	t.Run("the default wait is three seconds", func(t *testing.T) {
		if defaultWait != 3*time.Second {
			t.Fatalf("defaultWait = %v, want 3s", defaultWait)
		}
	})
}

func TestSnProbeRefusesAnInvalidServer(t *testing.T) {
	d, err := Probe(context.Background(), netip.AddrPort{})
	if err == nil || !strings.HasPrefix(err.Error(), "stun: ") || !strings.Contains(err.Error(), "invalid server address") || d != 0 {
		t.Fatalf("Probe = %v, %v; want a stun: invalid server address error", d, err)
	}
}
