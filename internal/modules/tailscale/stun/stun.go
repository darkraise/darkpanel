// Package stun is a hand-written RFC 5389 STUN binding client.
package stun

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"net"
	"net/netip"
	"os"
	"time"
)

const (
	headerSize  = 20
	magicCookie = 0x2112a442

	typeBindingRequest = 0x0001
	typeBindingSuccess = 0x0101

	attrMappedAddress    = 0x0001
	attrXORMappedAddress = 0x0020
	attrSoftware         = 0x8022
	attrFingerprint      = 0x8028

	software       = "tailnode"
	fingerprintXOR = 0x5354554e

	defaultWait = 3 * time.Second
)

// Request encodes a Binding request with transaction id tx. derper answers only a Binding request that carries
// the SOFTWARE attribute "tailnode" and ends in a FINGERPRINT attribute, and silently drops any other.
func Request(tx [12]byte) []byte {
	const attrsSize uint16 = 4 + uint16(len(software)) + 8
	b := binary.BigEndian.AppendUint16(make([]byte, 0, headerSize+attrsSize), typeBindingRequest)
	b = binary.BigEndian.AppendUint16(b, attrsSize)
	b = binary.BigEndian.AppendUint32(b, magicCookie)
	b = append(b, tx[:]...)

	b = binary.BigEndian.AppendUint16(b, attrSoftware)
	b = binary.BigEndian.AppendUint16(b, uint16(len(software)))
	b = append(b, software...)

	fingerprint := crc32.ChecksumIEEE(b) ^ fingerprintXOR
	b = binary.BigEndian.AppendUint16(b, attrFingerprint)
	b = binary.BigEndian.AppendUint16(b, 4)
	return binary.BigEndian.AppendUint32(b, fingerprint)
}

// Parse decodes a Binding success response, returning its transaction id and mapped address,
// or an error naming what is wrong. MESSAGE-INTEGRITY and FINGERPRINT are not verified.
func Parse(b []byte) (tx [12]byte, mapped netip.AddrPort, err error) {
	if len(b) < headerSize {
		return tx, netip.AddrPort{}, fmt.Errorf("stun: message of %d bytes is shorter than the %d-byte header", len(b), headerSize)
	}
	if cookie := binary.BigEndian.Uint32(b[4:8]); cookie != magicCookie {
		return tx, netip.AddrPort{}, fmt.Errorf("stun: magic cookie is 0x%08x, want 0x%08x", cookie, uint32(magicCookie))
	}
	if n := int(binary.BigEndian.Uint16(b[2:4])); n%4 != 0 || n != len(b)-headerSize {
		return tx, netip.AddrPort{}, fmt.Errorf("stun: header length %d does not match the %d bytes that follow", n, len(b)-headerSize)
	}
	if typ := binary.BigEndian.Uint16(b[0:2]); typ != typeBindingSuccess {
		return tx, netip.AddrPort{}, fmt.Errorf("stun: message type 0x%04x is not a binding success response", typ)
	}
	copy(tx[:], b[8:headerSize])

	var plain, xored netip.AddrPort
	body := b[headerSize:]
	for len(body) > 0 {
		typ, n := binary.BigEndian.Uint16(body[0:2]), int(binary.BigEndian.Uint16(body[2:4]))
		padded := (n + 3) &^ 3
		if len(body)-4 < padded {
			return [12]byte{}, netip.AddrPort{}, fmt.Errorf("stun: attribute 0x%04x overruns the message", typ)
		}
		value := body[4 : 4+n]
		body = body[4+padded:]
		switch typ {
		case attrMappedAddress:
			ap, err := decodeAddress(typ, value, nil)
			if err != nil {
				return [12]byte{}, netip.AddrPort{}, err
			}
			if !plain.IsValid() {
				plain = ap
			}
		case attrXORMappedAddress:
			ap, err := decodeAddress(typ, value, &tx)
			if err != nil {
				return [12]byte{}, netip.AddrPort{}, err
			}
			if !xored.IsValid() {
				xored = ap
			}
		}
	}
	switch {
	case xored.IsValid():
		return tx, xored, nil
	case plain.IsValid():
		return tx, plain, nil
	}
	return [12]byte{}, netip.AddrPort{}, errors.New("stun: response has no mapped address")
}

// decodeAddress reads the value of a MAPPED-ADDRESS attribute, or of an XOR-MAPPED-ADDRESS one when tx is not nil.
func decodeAddress(typ uint16, v []byte, tx *[12]byte) (netip.AddrPort, error) {
	var size int
	switch {
	case len(v) >= 2 && v[1] == 1:
		size = 4
	case len(v) >= 2 && v[1] == 2:
		size = 16
	}
	if size == 0 || len(v) != 4+size {
		return netip.AddrPort{}, fmt.Errorf("stun: mapped address attribute 0x%04x is malformed", typ)
	}
	port := binary.BigEndian.Uint16(v[2:4])
	addr := make([]byte, size)
	copy(addr, v[4:])
	if tx != nil {
		mask := binary.BigEndian.AppendUint32(nil, magicCookie)
		mask = append(mask, tx[:]...)
		port ^= magicCookie >> 16
		for i := range addr {
			addr[i] ^= mask[i]
		}
	}
	a, _ := netip.AddrFromSlice(addr)
	return netip.AddrPortFrom(a, port), nil
}

// Probe sends one RFC 5389 Binding request (a random 96-bit transaction id, in tailscale's request form, SOFTWARE and FINGERPRINT) to server over a
// UDP socket it opens with net.ListenPacket("udp", ""), and returns the round-trip time of the first Binding
// success response with the same transaction id and a valid XOR-MAPPED-ADDRESS (or MAPPED-ADDRESS) attribute.
// It honours ctx's deadline; with none it waits at most 3 s. Any other datagram is ignored.
func Probe(ctx context.Context, server netip.AddrPort) (time.Duration, error) {
	return probe(ctx, server, defaultWait)
}

func probe(ctx context.Context, server netip.AddrPort, wait time.Duration) (time.Duration, error) {
	if !server.IsValid() {
		return 0, errors.New("stun: invalid server address")
	}
	var tx [12]byte
	if _, err := rand.Read(tx[:]); err != nil {
		return 0, fmt.Errorf("stun: transaction id: %w", err)
	}
	conn, err := net.ListenPacket("udp", "")
	if err != nil {
		return 0, fmt.Errorf("stun: open socket: %w", err)
	}
	defer func() { _ = conn.Close() }()

	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, wait)
		defer cancel()
	}
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return 0, fmt.Errorf("stun: set deadline: %w", err)
	}
	defer context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Now()) })()

	start := time.Now()
	if _, err := conn.WriteTo(Request(tx), net.UDPAddrFromAddrPort(server)); err != nil {
		return 0, fmt.Errorf("stun: send to %v: %w", server, err)
	}
	buf := make([]byte, 1500)
	for {
		n, _, err := conn.ReadFrom(buf)
		if err != nil {
			switch {
			case ctx.Err() != nil:
				return 0, fmt.Errorf("stun: no answer from %v: %w", server, ctx.Err())
			case errors.Is(err, os.ErrDeadlineExceeded):
				return 0, fmt.Errorf("stun: no answer from %v: %w", server, context.DeadlineExceeded)
			}
			return 0, fmt.Errorf("stun: read from %v: %w", server, err)
		}
		if got, _, err := Parse(buf[:n]); err == nil && got == tx {
			return time.Since(start), nil
		}
	}
}
