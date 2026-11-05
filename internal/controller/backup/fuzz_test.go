package backup

import (
	"archive/tar"
	"bufio"
	"bytes"
	"crypto/cipher"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/crypto/chacha20poly1305"

	"github.com/darkraise/darkpanel/internal/paths"
)

const fz10Tx = "01JTXAAAAAAAAAAAAAAAAAAAAA"

func fz10Head(s string) string {
	if len(s) > 64 {
		return s[:64]
	}
	return s
}

func fz10Vector(tb testing.TB, name string) []byte {
	tb.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name)) //nolint:gosec // G304: a fixed name under testdata
	if err != nil {
		tb.Fatal(err)
	}
	return b
}

// FuzzReadHeader checks that formatReadHeader refuses a header with ErrNotBackup, ErrHeader or ErrTruncated, and
// that a header it accepts passes Params.Validate and encodes back to the bytes it was read from.
func FuzzReadHeader(f *testing.F) {
	good := formatEncodeHeader(Header{Params: TestParams})
	f.Add(good)
	f.Add(formatEncodeHeader(Header{Params: DefaultParams, Salt: [16]byte{1}, NoncePrefix: [16]byte{2}}))
	f.Add(formatEncodeHeader(Header{Params: Params{T: 10, M: 1 << 20, P: 16}}))
	f.Add(formatEncodeHeader(Header{Params: Params{T: 0, M: 65536, P: 1}}))
	f.Add(formatEncodeHeader(Header{Params: Params{T: 1, M: 65535, P: 1}}))
	f.Add(append(bytes.Clone(good), "trailing"...))
	f.Add(good[:HeaderSize-1])
	f.Add(good[:5])
	f.Add([]byte("DPBK"))
	f.Add([]byte("NOTADPBKFILE"))
	f.Add(fz10Vector(f, "short.dpbk")[:HeaderSize])
	f.Add(fz10Vector(f, "multi.dpbk")[:HeaderSize])
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, in []byte) {
		raw := make([]byte, HeaderSize)
		h, err := formatReadHeader(bytes.NewReader(in), raw)
		if err != nil {
			if !errors.Is(err, ErrNotBackup) && !errors.Is(err, ErrHeader) && !errors.Is(err, ErrTruncated) {
				t.Fatalf("formatReadHeader(%q) error = %v, want ErrNotBackup, ErrHeader or ErrTruncated", fz10Head(string(in)), err)
			}
			return
		}
		if err := h.Params.Validate(); err != nil {
			t.Fatalf("formatReadHeader(%q) accepted the parameters %+v, which Validate refuses: %v", fz10Head(string(in)), h.Params, err)
		}
		if got := formatEncodeHeader(h); !bytes.Equal(got, in[:HeaderSize]) || !bytes.Equal(got, raw) {
			t.Fatalf("formatReadHeader(%q) accepted a header that encodes as %q", fz10Head(string(in)), fz10Head(string(got)))
		}
	})
}

// The key and header FuzzFrames uses: argon2id would run once per input under the real derivation, so the target
// opens the frames with a fixed key instead.
func fz10Stream(tb testing.TB) (cipher.AEAD, []byte) {
	tb.Helper()
	aead, err := chacha20poly1305.NewX(bytes.Repeat([]byte{0x5a}, 32))
	if err != nil {
		tb.Fatal(err)
	}
	return aead, formatEncodeHeader(Header{Params: TestParams, Salt: [16]byte{3}, NoncePrefix: [16]byte{4}})
}

// fz10Tar is the tar Write produces for a database member and a key member.
func fz10Tar(tb testing.TB, db, key []byte) []byte {
	tb.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, m := range []struct {
		name string
		body []byte
	}{{MemberDB, db}, {MemberKey, key}} {
		hdr := &tar.Header{Typeflag: tar.TypeReg, Name: m.name, Size: int64(len(m.body)), Mode: 0o600}
		if err := tw.WriteHeader(hdr); err != nil {
			tb.Fatal(err)
		}
		if _, err := tw.Write(m.body); err != nil {
			tb.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		tb.Fatal(err)
	}
	return buf.Bytes()
}

// fz10Seal seals plain into frames with the fixed key, as the frame writer does for the archive Write builds.
func fz10Seal(tb testing.TB, plain []byte) []byte {
	tb.Helper()
	aead, raw := fz10Stream(tb)
	var buf bytes.Buffer
	fw := frameNewWriter(&buf, aead, raw)
	if _, err := fw.Write(plain); err != nil {
		tb.Fatal(err)
	}
	if err := fw.finish(); err != nil {
		tb.Fatal(err)
	}
	return buf.Bytes()
}

// FuzzFrames checks that frameNewReader and formatReadMembers refuse, with ErrAuth, ErrTruncated or ErrMembers,
// every frame stream the frame writer did not produce. The input is a frame stream as it is, or, with seal set,
// the plaintext archive that the target seals first so that mutations reach the tar reader; an unsealed stream
// that is accepted must equal what the writer produces for the members read from it.
func FuzzFrames(f *testing.F) {
	db, key := bytes.Repeat([]byte("db"), 100), []byte("darkpanel-key-v1:placeholder\n")
	whole := fz10Seal(f, fz10Tar(f, db, key))
	f.Add(whole, false)
	f.Add(whole[:len(whole)-1], false)
	f.Add(whole[:len(whole)/2], false)
	f.Add(append(bytes.Clone(whole), 0), false)
	f.Add(fz10Seal(f, nil), false)
	f.Add(fz10Vector(f, "short.dpbk")[HeaderSize:], false)
	f.Add(fz10Vector(f, "multi.dpbk")[HeaderSize:], false)
	f.Add(fz10Tar(f, db, key), true)
	f.Add(append(fz10Tar(f, db, key), 'x'), true)
	f.Add(fz10Tar(f, db, bytes.Repeat([]byte("k"), MaxKeyMember+1)), true)
	f.Add([]byte{}, true)
	f.Fuzz(func(t *testing.T, in []byte, seal bool) {
		stream := in
		if seal {
			stream = fz10Seal(t, in)
		}
		aead, raw := fz10Stream(t)
		fr := frameNewReader(bufio.NewReaderSize(bytes.NewReader(stream), formatReadBuf), aead, raw)
		var members [][]byte
		err := formatReadMembers(fr, func(_ string, _ int64, body io.Reader) error {
			b, err := io.ReadAll(body)
			if err != nil {
				return fmt.Errorf("%w: %w", ErrMembers, err)
			}
			members = append(members, b)
			return nil
		})
		if err == nil {
			err = fr.drainZero()
		}
		if err != nil {
			if !errors.Is(err, ErrAuth) && !errors.Is(err, ErrTruncated) && !errors.Is(err, ErrMembers) {
				t.Fatalf("frames %q (sealed %v) error = %v, want ErrAuth, ErrTruncated or ErrMembers", fz10Head(string(in)), seal, err)
			}
			return
		}
		if seal {
			return
		}
		if len(members) != 2 || !bytes.Equal(fz10Seal(t, fz10Tar(t, members[0], members[1])), stream) {
			t.Fatalf("frames %q were accepted but are not what the frame writer produces for their members", fz10Head(string(in)))
		}
	})
}

// FuzzDecodeJournal checks that decodeJournal accepts only format 1 with a known phase, and that checkJournal
// accepts only a journal whose tx is an upper-case ULID that its path carries and whose files, when it lists any,
// are the controller's four targets in order with the key recorded as present.
func FuzzDecodeJournal(f *testing.F) {
	h := Host{Paths: paths.Under("/fz10")}
	good := JournalPath(h.Paths.ControllerDir, fz10Tx)
	var files []JournalFile
	for _, target := range Targets(h.Paths) {
		files = append(files, JournalFile{Path: target, Existed: true})
	}
	for _, phase := range journalPhases {
		for _, withFiles := range []bool{false, true} {
			j := Journal{Format: 1, Tx: fz10Tx, Phase: phase}
			if withFiles {
				j.Files = files
				j.Renames = []Rename{{Phase: phase, From: files[0].Path, To: files[0].Path + ".pre", State: RenameIntended}}
			}
			data, err := json.Marshal(j)
			if err != nil {
				f.Fatal(err)
			}
			f.Add(data, good)
		}
	}
	f.Add([]byte(`{"format":2,"tx":"`+fz10Tx+`","phase":"begun"}`), good)
	f.Add([]byte(`{"format":1,"tx":"`+fz10Tx+`","phase":"unknown"}`), good)
	f.Add([]byte(`{"format":1,"tx":"`+strings.ToLower(fz10Tx)+`","phase":"begun"}`), good)
	f.Add([]byte(`{"format":1,"tx":"`+fz10Tx+`","phase":"begun"}`), "/fz10/controller/restore-other.journal")
	f.Add([]byte(`{"format":1,"tx":"`+fz10Tx+`","phase":"journaled","files":[{"path":"/etc/passwd","existed":true}]}`), good)
	f.Add([]byte(`null`), good)
	f.Add([]byte(``), good)
	f.Fuzz(func(t *testing.T, data []byte, path string) {
		j, err := decodeJournal(path, data)
		if err != nil {
			return
		}
		if j.Format != 1 || !slices.Contains(journalPhases, j.Phase) {
			t.Fatalf("decodeJournal(%q) accepted format %d and phase %q", fz10Head(string(data)), j.Format, j.Phase)
		}
		if checkJournal(h, path, j) != nil {
			return
		}
		if len(j.Tx) != 26 || strings.Trim(j.Tx, "0123456789ABCDEFGHJKMNPQRSTVWXYZ") != "" || JournalPath(h.Paths.ControllerDir, j.Tx) != path {
			t.Fatalf("checkJournal accepted the tx %q for the path %q", j.Tx, fz10Head(path))
		}
		if len(j.Files) == 0 {
			return
		}
		targets := Targets(h.Paths)
		if len(j.Files) != len(targets) {
			t.Fatalf("checkJournal accepted %d files, want %d", len(j.Files), len(targets))
		}
		for i, jf := range j.Files {
			if jf.Path != targets[i] || (jf.Path == h.Paths.ControllerKey && !jf.Existed) {
				t.Fatalf("checkJournal accepted file %d as %+v, want %s present", i, jf, targets[i])
			}
		}
	})
}
