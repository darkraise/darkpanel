package stun

import (
	"net/netip"
	"testing"
)

// fz8Head returns at most the first 64 bytes of b, for a failure message.
func fz8Head(b []byte) []byte { return b[:min(len(b), 64)] }

// FuzzParse feeds arbitrary bytes to Parse, and as the value of a mapped
// address attribute to decodeAddress, with an arbitrary transaction id: no
// call panics; a binding request built by Request never parses as a success;
// a refused message returns no transaction id or address, and an accepted one
// a valid address and the id it holds; the XOR-mapped reading of a value
// differs from the plain one by the magic cookie and the transaction id, and
// both are refused or accepted together (R74).
func FuzzParse(f *testing.F) {
	v4 := netip.MustParseAddrPort("192.0.2.1:32853")
	v6 := netip.MustParseAddrPort("[2001:db8:1234:5678:11:2233:4455:6677]:32853")
	f.Add(Request(snTx), snTx[:])
	f.Add(snMessage(snBindingSuccess, snTx, snAttr(snXorMapped, snAddress(v4, &snTx))), snTx[:])
	f.Add(snMessage(snBindingSuccess, snTx, snAttr(snMapped, snAddress(v6, nil)), snAttr(snSoftware, []byte("tailnode"))), snTx[:])
	f.Add(snMessage(snBindingError, snTx, snAttr(snXorMapped, snAddress(v4, &snTx))), snTx[:])
	f.Add(snMessage(snBindingSuccess, snTx), snTx[:])
	f.Add(snAddress(v4, nil), snTx[:])
	f.Add(snAddress(v6, &snTx), snTx[:])
	f.Add([]byte{}, []byte{})
	f.Fuzz(func(t *testing.T, msg, txBytes []byte) {
		var tx [12]byte
		copy(tx[:], txBytes)
		if _, _, err := Parse(Request(tx)); err == nil {
			t.Fatalf("a binding request with the id %x parsed as a success", tx)
		}

		gotTx, mapped, err := Parse(msg)
		switch {
		case err != nil && (gotTx != [12]byte{} || mapped.IsValid()):
			t.Fatalf("Parse of %x refused the message and returned %x, %v", fz8Head(msg), gotTx, mapped)
		case err == nil && (!mapped.IsValid() || string(gotTx[:]) != string(msg[8:headerSize])):
			t.Fatalf("Parse of %x returned %x, %v", fz8Head(msg), gotTx, mapped)
		}

		plain, errPlain := decodeAddress(attrMappedAddress, msg, nil)
		xored, errXored := decodeAddress(attrXORMappedAddress, msg, &tx)
		if (errPlain == nil) != (errXored == nil) {
			t.Fatalf("decodeAddress of %x: plain %v, XOR %v", fz8Head(msg), errPlain, errXored)
		}
		if errPlain != nil {
			return
		}
		mask := append([]byte{0x21, 0x12, 0xa4, 0x42}, tx[:]...)
		pb, xb := plain.Addr().AsSlice(), xored.Addr().AsSlice()
		if xored.Port() != plain.Port()^0x2112 || len(pb) != len(xb) {
			t.Fatalf("decodeAddress of %x: ports %d and %d, address lengths %d and %d", fz8Head(msg), plain.Port(), xored.Port(), len(pb), len(xb))
		}
		for i := range pb {
			if pb[i]^xb[i] != mask[i] {
				t.Fatalf("decodeAddress of %x: byte %d of the XOR reading is not masked by the cookie and the id", fz8Head(msg), i)
			}
		}
	})
}
