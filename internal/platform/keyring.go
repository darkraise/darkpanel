package platform

import (
	"crypto/sha1" //nolint:gosec // G505: an OpenPGP version 4 fingerprint is defined as a SHA-1
	"encoding/binary"
	"encoding/hex"
	"errors"
	"math"
	"strings"
)

// KeyringFingerprints lists the OpenPGP version 4 fingerprints of every public key and public subkey packet of
// the binary keyring b, in order. Keys of another version are skipped.
func KeyringFingerprints(b []byte) ([]string, error) {
	var fps []string
	for len(b) > 0 {
		first := b[0]
		if first&0x80 == 0 {
			return nil, errors.New("platform: the keyring data is not an OpenPGP packet")
		}
		var tag byte
		var header, length int
		if first&0x40 != 0 {
			tag = first & 0x3f
			if len(b) < 2 {
				return nil, errors.New("platform: an OpenPGP packet header runs past the keyring data")
			}
			switch l := b[1]; {
			case l < 192:
				header, length = 2, int(l)
			case l < 224:
				if len(b) < 3 {
					return nil, errors.New("platform: an OpenPGP packet header runs past the keyring data")
				}
				header, length = 3, (int(l)-192)<<8+int(b[2])+192
			case l == 255:
				if len(b) < 6 {
					return nil, errors.New("platform: an OpenPGP packet header runs past the keyring data")
				}
				header, length = 6, int(binary.BigEndian.Uint32(b[2:6]))
			default:
				return nil, errors.New("platform: the keyring holds an OpenPGP packet of partial length")
			}
		} else {
			tag = first >> 2 & 0x0f
			switch first & 0x03 {
			case 0:
				header = 2
			case 1:
				header = 3
			case 2:
				header = 5
			case 3:
				header = 1
			}
			if len(b) < header {
				return nil, errors.New("platform: an OpenPGP packet header runs past the keyring data")
			}
			switch header {
			case 2:
				length = int(b[1])
			case 3:
				length = int(binary.BigEndian.Uint16(b[1:3]))
			case 5:
				length = int(binary.BigEndian.Uint32(b[1:5]))
			case 1:
				length = len(b) - 1
			}
		}
		if length > len(b)-header {
			return nil, errors.New("platform: an OpenPGP packet runs past the keyring data")
		}
		body := b[header : header+length]
		b = b[header+length:]
		if (tag != 6 && tag != 14) || len(body) == 0 || body[0] != 4 {
			continue
		}
		if len(body) > math.MaxUint16 {
			return nil, errors.New("platform: a version 4 key packet is longer than its fingerprint can encode")
		}
		h := sha1.New()                                                         //nolint:gosec // G401: an OpenPGP version 4 fingerprint is defined as a SHA-1
		h.Write(binary.BigEndian.AppendUint16([]byte{0x99}, uint16(len(body)))) //nolint:gosec // G115: the length was checked against math.MaxUint16 above
		h.Write(body)
		fps = append(fps, strings.ToUpper(hex.EncodeToString(h.Sum(nil))))
	}
	return fps, nil
}
