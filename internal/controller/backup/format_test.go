package backup

import (
	"archive/tar"
	"bytes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"testing/iotest"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/chacha20poly1305"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// fmUpdate rewrites the committed vectors: go test -run '^TestFmWriteVectors$' ./internal/controller/backup -args -update
var fmUpdate = flag.Bool("update", false, "rewrite testdata/short.dpbk and testdata/multi.dpbk")

const fmPassphrase = "darkpanel test passphrase"

func fmPass() []byte { return []byte(fmPassphrase) }

func fmSalt() [16]byte {
	var b [16]byte
	for i := range b {
		b[i] = 0xA0 + byte(i)
	}
	return b
}

func fmPrefix() [16]byte {
	var b [16]byte
	for i := range b {
		b[i] = 0xB0 + byte(i)
	}
	return b
}

// fmRnd answers the 32 random bytes Write asks for: the salt, then the nonce prefix.
func fmRnd() io.Reader {
	salt, prefix := fmSalt(), fmPrefix()
	return bytes.NewReader(append(salt[:], prefix[:]...))
}

func fmMix(x uint64) uint64 {
	x += 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}

// fmStream is size bytes of deterministic pseudo-random data: byte i depends
// only on the seed and i, whatever sizes the reads have.
type fmStream struct {
	pos, size uint64
	seed      uint64
}

func newFmStream(size, seed uint64) *fmStream { return &fmStream{size: size, seed: seed} }

func (s *fmStream) Read(p []byte) (int, error) {
	if s.pos >= s.size {
		return 0, io.EOF
	}
	if rest := s.size - s.pos; uint64(len(p)) > rest {
		p = p[:rest]
	}
	var word uint64
	for i := range p {
		at := s.pos + uint64(i)
		if i == 0 || at%8 == 0 {
			word = fmMix(s.seed ^ (at/8)*0x2545f4914f6cdd1d)
		}
		p[i] = byte(word >> (8 * (at % 8))) //nolint:gosec // G115: keeping the low byte is the point
	}
	s.pos += uint64(len(p))
	return len(p), nil
}

func fmBytes(n int, seed uint64) []byte {
	b := make([]byte, n)
	if _, err := io.ReadFull(newFmStream(uint64(n), seed), b); err != nil && n > 0 { //nolint:gosec // G115: n is a test size
		panic(err)
	}
	return b
}

func fmKeyFile(seed uint64) []byte {
	return []byte("darkpanel-key-v1:" + base64.StdEncoding.EncodeToString(fmBytes(32, seed)) + "\n")
}

// fmVector is one committed test vector and the members it must hold.
type fmVector struct {
	file    string
	db, key []byte
}

func fmVectors() []fmVector {
	return []fmVector{
		{"short.dpbk", fmBytes(3000, 1), fmKeyFile(3)},
		{"multi.dpbk", fmBytes(150*1024+777, 2), fmKeyFile(3)},
	}
}

func fmMembers(db, key []byte) []Member {
	return []Member{
		{Name: MemberDB, Size: int64(len(db)), Body: bytes.NewReader(db)},
		{Name: MemberKey, Size: int64(len(key)), Body: bytes.NewReader(key)},
	}
}

func fmWrite(t *testing.T, db, key []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := Write(&buf, fmRnd(), fmPass(), TestParams, fmMembers(db, key)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	return buf.Bytes()
}

func fmVectorFile(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name)) //nolint:gosec // G304: a fixed name under testdata
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// fmResult is what Read gave a collecting sink.
type fmResult struct {
	db, key []byte
	order   []string
	header  Header
	err     error
}

func fmRead(r io.Reader, pass []byte) fmResult {
	var res fmResult
	res.header, res.err = Read(r, pass, func(name string, size int64, body io.Reader) error {
		b, err := io.ReadAll(body)
		if err != nil {
			return err
		}
		if int64(len(b)) != size {
			return fmt.Errorf("the sink read %d bytes of %s, size says %d", len(b), name, size)
		}
		res.order = append(res.order, name)
		switch name {
		case MemberDB:
			res.db = b
		case MemberKey:
			res.key = b
		}
		return nil
	})
	return res
}

// fmSplit cuts a backup file into its header and its frames, each frame
// with its 4-byte length.
func fmSplit(t *testing.T, file []byte) ([]byte, [][]byte) {
	t.Helper()
	if len(file) < HeaderSize {
		t.Fatalf("file of %d bytes has no header", len(file))
	}
	hdr, rest := file[:HeaderSize], file[HeaderSize:]
	var frames [][]byte
	for len(rest) > 0 {
		if len(rest) < 4 {
			t.Fatalf("%d bytes left, not a frame", len(rest))
		}
		n := 4 + int(binary.BigEndian.Uint32(rest))
		if n > len(rest) {
			t.Fatalf("frame of %d bytes, %d left", n, len(rest))
		}
		frames = append(frames, rest[:n])
		rest = rest[n:]
	}
	return hdr, frames
}

func fmJoin(hdr []byte, frames ...[]byte) []byte {
	out := bytes.Clone(hdr)
	for _, f := range frames {
		out = append(out, f...)
	}
	return out
}

// fmAEAD derives the key of the vectors' passphrase, salt and parameters
// on its own, so the tests below do not rely on Write or Read for it.
func fmAEAD(t *testing.T, hdr []byte) cipher.AEAD {
	t.Helper()
	salt := hdr[14:30]
	key := argon2.IDKey(fmPass(), salt, binary.BigEndian.Uint32(hdr[5:]), binary.BigEndian.Uint32(hdr[9:]), hdr[13], 32)
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		t.Fatal(err)
	}
	return aead
}

func fmNonce(hdr []byte, idx int) []byte {
	n := make([]byte, chacha20poly1305.NonceSizeX)
	copy(n, hdr[30:46])
	binary.BigEndian.PutUint64(n[16:], uint64(idx)) //nolint:gosec // G115: idx counts frames of a test file
	return n
}

// fmOpen decrypts a whole backup file with the format of the spec written
// out again, and returns its plaintext (the tar).
func fmOpen(t *testing.T, file []byte) []byte {
	t.Helper()
	hdr, frames := fmSplit(t, file)
	aead := fmAEAD(t, hdr)
	var plain []byte
	for i, f := range frames {
		ct := f[4:]
		if len(ct) < 16 || len(ct) > ChunkSize+16 {
			t.Fatalf("frame %d holds %d ciphertext bytes", i, len(ct))
		}
		last := byte(0)
		if i == len(frames)-1 {
			last = 1
		}
		pt, err := aead.Open(nil, fmNonce(hdr, i), ct, append(bytes.Clone(hdr), last))
		if err != nil {
			t.Fatalf("frame %d does not open as the spec seals it: %v", i, err)
		}
		if i < len(frames)-1 && len(pt) != ChunkSize {
			t.Fatalf("frame %d holds %d plaintext bytes; only the last may hold fewer than %d", i, len(pt), ChunkSize)
		}
		plain = append(plain, pt...)
	}
	return plain
}

// fmBuilder returns a function that seals chunks into a backup file the way
// the spec says, with TestParams, the vectors' passphrase, salt and prefix.
func fmBuilder(t *testing.T) func(chunks [][]byte) []byte {
	t.Helper()
	hdr := make([]byte, HeaderSize)
	copy(hdr, Magic)
	hdr[4] = FormatVersion
	binary.BigEndian.PutUint32(hdr[5:], TestParams.T)
	binary.BigEndian.PutUint32(hdr[9:], TestParams.M)
	hdr[13] = TestParams.P
	salt, prefix := fmSalt(), fmPrefix()
	copy(hdr[14:], salt[:])
	copy(hdr[30:], prefix[:])
	aead := fmAEAD(t, hdr)
	return func(chunks [][]byte) []byte {
		out := bytes.Clone(hdr)
		for i, c := range chunks {
			last := byte(0)
			if i == len(chunks)-1 {
				last = 1
			}
			ct := aead.Seal(nil, fmNonce(hdr, i), c, append(bytes.Clone(hdr), last))
			out = binary.BigEndian.AppendUint32(out, uint32(len(ct))) //nolint:gosec // G115: at most ChunkSize+16
			out = append(out, ct...)
		}
		return out
	}
}

func fmChunks(plain []byte) [][]byte {
	var out [][]byte
	for len(plain) > ChunkSize {
		out = append(out, plain[:ChunkSize])
		plain = plain[ChunkSize:]
	}
	return append(out, plain)
}

// fmEntry is one tar entry; a zero Typeflag means a regular file.
type fmEntry struct {
	hdr  tar.Header
	body []byte
}

func fmStd(db, key []byte) []fmEntry {
	return []fmEntry{{hdr: tar.Header{Name: MemberDB}, body: db}, {hdr: tar.Header{Name: MemberKey}, body: key}}
}

func fmTar(t *testing.T, entries []fmEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		h := e.hdr
		h.Size = int64(len(e.body))
		if h.Typeflag == 0 {
			h.Typeflag = tar.TypeReg
		}
		h.Mode = 0o600
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(e.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestFmParamsValidate(t *testing.T) {
	for _, tt := range []struct {
		name  string
		p     Params
		field string
	}{
		{"defaults", DefaultParams, ""},
		{"test params", TestParams, ""},
		{"lower bounds", Params{T: 1, M: 65536, P: 1}, ""},
		{"upper bounds", Params{T: 10, M: 1 << 20, P: 16}, ""},
		{"t zero", Params{T: 0, M: 65536, P: 1}, "argon2.t"},
		{"t above ten", Params{T: 11, M: 65536, P: 1}, "argon2.t"},
		{"m below 64 MiB", Params{T: 1, M: 65535, P: 1}, "argon2.m"},
		{"m above 1 GiB", Params{T: 1, M: 1<<20 + 1, P: 1}, "argon2.m"},
		{"p zero", Params{T: 1, M: 65536, P: 0}, "argon2.p"},
		{"p above sixteen", Params{T: 1, M: 65536, P: 17}, "argon2.p"},
	} {
		err := tt.p.Validate()
		if tt.field == "" {
			if err != nil {
				t.Errorf("%s: Validate = %v, want nil", tt.name, err)
			}
			continue
		}
		var ce *errcode.Error
		if !errors.As(err, &ce) || ce.Code != errcode.InvalidInput || !strings.Contains(ce.Detail, tt.field) {
			t.Errorf("%s: Validate = %v, want request.invalid_input naming %s", tt.name, err, tt.field)
		}
	}
	if DefaultParams != (Params{T: 3, M: 65536, P: 4}) || TestParams != (Params{T: 1, M: 65536, P: 1}) {
		t.Errorf("DefaultParams = %+v, TestParams = %+v", DefaultParams, TestParams)
	}
}

func TestFmReadVectors(t *testing.T) {
	wantFrames := map[string]int{"short.dpbk": 1, "multi.dpbk": 3}
	for _, v := range fmVectors() {
		file := fmVectorFile(t, v.file)
		if _, frames := fmSplit(t, file); len(frames) != wantFrames[v.file] {
			t.Errorf("%s holds %d frames, want %d", v.file, len(frames), wantFrames[v.file])
		}
		res := fmRead(bytes.NewReader(file), fmPass())
		if res.err != nil {
			t.Errorf("%s: Read = %v", v.file, res.err)
			continue
		}
		if !bytes.Equal(res.db, v.db) || !bytes.Equal(res.key, v.key) {
			t.Errorf("%s: members differ from the generator's (db %d bytes, key %d bytes)", v.file, len(res.db), len(res.key))
		}
		if !slices.Equal(res.order, []string{MemberDB, MemberKey}) {
			t.Errorf("%s: the sink saw %v, want controller.db then controller.key", v.file, res.order)
		}
		if want := (Header{Params: TestParams, Salt: fmSalt(), NoncePrefix: fmPrefix()}); res.header != want {
			t.Errorf("%s: header = %+v, want %+v", v.file, res.header, want)
		}
	}
	if len(fmVectors()[1].db) < 150*1024 {
		t.Error("the multi-chunk vector's database is under 150 KiB")
	}
}

func TestFmWriteVectors(t *testing.T) {
	for _, v := range fmVectors() {
		got := fmWrite(t, v.db, v.key)
		path := filepath.Join("testdata", v.file)
		if *fmUpdate {
			if err := os.MkdirAll("testdata", 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, got, 0o600); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if want := fmVectorFile(t, v.file); !bytes.Equal(got, want) {
			t.Errorf("Write produced %d bytes that differ from %s (%d bytes); regenerate the vectors only for a deliberate format change", len(got), path, len(want))
		}
	}
}

func TestFmWriteLayout(t *testing.T) {
	v := fmVectors()[0]
	file := fmWrite(t, v.db, v.key)
	hdr, frames := fmSplit(t, file)
	salt, prefix := fmSalt(), fmPrefix()
	if string(hdr[:4]) != "DPBK" || hdr[4] != 1 || binary.BigEndian.Uint32(hdr[5:]) != 1 || binary.BigEndian.Uint32(hdr[9:]) != 65536 ||
		hdr[13] != 1 || !bytes.Equal(hdr[14:30], salt[:]) || !bytes.Equal(hdr[30:46], prefix[:]) {
		t.Errorf("header = %x", hdr)
	}
	if len(frames) != 1 {
		t.Fatalf("a 5.5 KiB archive is %d frames, want 1", len(frames))
	}
	tr := tar.NewReader(bytes.NewReader(fmOpen(t, file)))
	for _, want := range []struct {
		name string
		body []byte
	}{{MemberDB, v.db}, {MemberKey, v.key}} {
		h, err := tr.Next()
		if err != nil {
			t.Fatalf("tar member %s: %v", want.name, err)
		}
		if h.Name != want.name || h.Typeflag != tar.TypeReg || h.Mode != 0o600 || h.Uid != 0 || h.Gid != 0 || h.ModTime.Unix() != 0 || h.Size != int64(len(want.body)) {
			t.Errorf("tar header of %s = %+v", want.name, *h)
		}
		if body, err := io.ReadAll(tr); err != nil || !bytes.Equal(body, want.body) {
			t.Errorf("tar body of %s differs (%d bytes, %v)", want.name, len(body), err)
		}
	}
	if _, err := tr.Next(); !errors.Is(err, io.EOF) {
		t.Errorf("tar has a third entry: %v", err)
	}
}

// TestFmWriteChunking pins where chunks end: a plaintext that is an exact
// multiple of the chunk size ends in a full last chunk, never an empty one.
func TestFmWriteChunking(t *testing.T) {
	key := fmKeyFile(3)
	// The tar around a database of n bytes is c + n rounded up to 512.
	c := len(fmOpen(t, fmWrite(t, fmBytes(1000, 5), key))) - 1024
	for _, tt := range []struct {
		name       string
		dbSize     int
		wantPlain  int
		wantFrames int
		wantLast   int // plaintext bytes of the last frame
	}{
		{"one full chunk", ChunkSize - c, ChunkSize, 1, ChunkSize},
		{"two full chunks", 2*ChunkSize - c, 2 * ChunkSize, 2, ChunkSize},
		{"one block over a chunk", ChunkSize - c + 512, ChunkSize + 512, 2, 512},
	} {
		if tt.dbSize%512 != 0 {
			t.Fatalf("%s: database of %d bytes is not a multiple of 512; the tar overhead is %d", tt.name, tt.dbSize, c)
		}
		db := fmBytes(tt.dbSize, 6)
		file := fmWrite(t, db, key)
		_, frames := fmSplit(t, file)
		if len(frames) != tt.wantFrames {
			t.Errorf("%s: %d frames, want %d", tt.name, len(frames), tt.wantFrames)
			continue
		}
		if got := len(frames[len(frames)-1]) - 4 - 16; got != tt.wantLast {
			t.Errorf("%s: the last frame holds %d plaintext bytes, want %d", tt.name, got, tt.wantLast)
		}
		if plain := fmOpen(t, file); len(plain) != tt.wantPlain {
			t.Errorf("%s: plaintext is %d bytes, want %d", tt.name, len(plain), tt.wantPlain)
		}
		if res := fmRead(bytes.NewReader(file), fmPass()); res.err != nil || !bytes.Equal(res.db, db) || !bytes.Equal(res.key, key) {
			t.Errorf("%s: Read = %v, members equal %v", tt.name, res.err, bytes.Equal(res.db, db))
		}
	}
}

func TestFmReadArchiveEnds(t *testing.T) {
	build := fmBuilder(t)
	key := fmKeyFile(3)
	exact := ChunkSize - len(fmTar(t, fmStd(nil, key))) // a database that makes the tar exactly one chunk
	db := fmBytes(exact, 8)
	plain := fmTar(t, fmStd(db, key))
	if len(plain) != ChunkSize {
		t.Fatalf("tar is %d bytes, want %d", len(plain), ChunkSize)
	}
	short := fmTar(t, fmStd(fmBytes(100, 9), key))
	for _, tt := range []struct {
		name   string
		chunks [][]byte
		db     []byte
		want   error
	}{
		{"an empty last frame after a full chunk", append(fmChunks(plain), []byte{}), db, nil},
		{"an empty last frame after a short chunk", [][]byte{short, {}}, fmBytes(100, 9), nil},
		{"zero padding after the archive", fmChunks(append(bytes.Clone(short), make([]byte, 9000)...)), fmBytes(100, 9), nil},
		{"a byte after the archive", fmChunks(append(append(bytes.Clone(short), make([]byte, 100)...), 1)), nil, ErrMembers},
	} {
		res := fmRead(bytes.NewReader(build(tt.chunks)), fmPass())
		if !errors.Is(res.err, tt.want) || (tt.want == nil && res.err != nil) {
			t.Errorf("%s: Read = %v, want %v", tt.name, res.err, tt.want)
			continue
		}
		if tt.want == nil && (!bytes.Equal(res.db, tt.db) || !bytes.Equal(res.key, key)) {
			t.Errorf("%s: members differ", tt.name)
		}
	}
}

func TestFmReadMembers(t *testing.T) {
	build := fmBuilder(t)
	db := fmBytes(2000, 10)
	key := fmKeyFile(3)
	keyAt := func(n int) fmEntry {
		return fmEntry{hdr: tar.Header{Name: MemberKey}, body: bytes.Repeat([]byte{'k'}, n)}
	}
	dbEntry := fmEntry{hdr: tar.Header{Name: MemberDB}, body: db}
	keyEntry := fmEntry{hdr: tar.Header{Name: MemberKey}, body: key}
	for _, tt := range []struct {
		name    string
		entries []fmEntry
		want    error
	}{
		{"exactly the two members", []fmEntry{dbEntry, keyEntry}, nil},
		{"a key member at the limit", []fmEntry{dbEntry, keyAt(MaxKeyMember)}, nil},
		{"a key member over the limit", []fmEntry{dbEntry, keyAt(MaxKeyMember + 1)}, ErrMembers},
		{"the members in the wrong order", []fmEntry{keyEntry, dbEntry}, ErrMembers},
		{"an extra member", []fmEntry{dbEntry, keyEntry, {hdr: tar.Header{Name: "extra.txt"}, body: []byte("x")}}, ErrMembers},
		{"the database only", []fmEntry{dbEntry}, ErrMembers},
		{"the key only", []fmEntry{keyEntry}, ErrMembers},
		{"no member", nil, ErrMembers},
		{"the database twice", []fmEntry{dbEntry, dbEntry}, ErrMembers},
		{"a symbolic link for the database", []fmEntry{{hdr: tar.Header{Name: MemberDB, Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"}}, keyEntry}, ErrMembers},
		{"a hard link for the key", []fmEntry{dbEntry, {hdr: tar.Header{Name: MemberKey, Typeflag: tar.TypeLink, Linkname: MemberDB}}}, ErrMembers},
		{"a directory for the database", []fmEntry{{hdr: tar.Header{Name: MemberDB, Typeflag: tar.TypeDir}}, keyEntry}, ErrMembers},
		{"a path with a slash", []fmEntry{{hdr: tar.Header{Name: "dir/" + MemberDB}, body: db}, keyEntry}, ErrMembers},
		{"a dot-slash path", []fmEntry{{hdr: tar.Header{Name: "./" + MemberDB}, body: db}, keyEntry}, ErrMembers},
		{"an absolute path", []fmEntry{dbEntry, {hdr: tar.Header{Name: "/" + MemberKey}, body: key}}, ErrMembers},
	} {
		res := fmRead(bytes.NewReader(build(fmChunks(fmTar(t, tt.entries)))), fmPass())
		switch {
		case tt.want == nil && res.err != nil:
			t.Errorf("%s: Read = %v", tt.name, res.err)
		case tt.want != nil && !errors.Is(res.err, tt.want):
			t.Errorf("%s: Read = %v, want %v", tt.name, res.err, tt.want)
		}
	}
}

func TestFmReadDamage(t *testing.T) {
	type mutate func(hdr []byte, frames [][]byte) []byte
	flip := func(frame int, at int) mutate {
		return func(hdr []byte, frames [][]byte) []byte {
			frames = slices.Clone(frames)
			if frame < 0 {
				frame = len(frames) + frame
			}
			f := bytes.Clone(frames[frame])
			if at < 0 {
				at = len(f) + at
			}
			f[at] ^= 1
			frames[frame] = f
			return fmJoin(hdr, frames...)
		}
	}
	flipHeader := func(at int, x byte) mutate {
		return func(hdr []byte, frames [][]byte) []byte {
			h := bytes.Clone(hdr)
			h[at] ^= x
			return fmJoin(h, frames...)
		}
	}
	for _, tt := range []struct {
		name string
		file string
		do   mutate
		want error
	}{
		{"the last frame is missing", "multi.dpbk", func(h []byte, f [][]byte) []byte { return fmJoin(h, f[:len(f)-1]...) }, ErrTruncated},
		{"every frame is missing", "short.dpbk", func(h []byte, _ [][]byte) []byte { return fmJoin(h) }, ErrTruncated},
		{"the file ends inside the last frame", "multi.dpbk", func(h []byte, f [][]byte) []byte {
			b := fmJoin(h, f...)
			return b[:len(b)-10]
		}, ErrTruncated},
		{"the file ends inside the first frame", "short.dpbk", func(h []byte, f [][]byte) []byte { return fmJoin(h, f[0][:20]) }, ErrTruncated},
		{"the file ends inside a frame length", "multi.dpbk", func(h []byte, f [][]byte) []byte { return fmJoin(h, f[0], f[1][:2]) }, ErrTruncated},
		{"a byte of the first frame is flipped", "multi.dpbk", flip(0, 104), ErrAuth},
		{"a byte of the last frame is flipped", "multi.dpbk", flip(-1, 9), ErrAuth},
		{"the tag of the last frame is flipped", "short.dpbk", flip(-1, -1), ErrAuth},
		{"a frame length grows past the bound", "multi.dpbk", flip(0, 3), ErrAuth},
		{"a frame length is below the tag", "multi.dpbk", func(h []byte, f [][]byte) []byte {
			f = slices.Clone(f)
			bad := bytes.Clone(f[0])
			binary.BigEndian.PutUint32(bad, 15)
			f[0] = bad
			return fmJoin(h, f...)
		}, ErrAuth},
		{"two frames are swapped", "multi.dpbk", func(h []byte, f [][]byte) []byte { return fmJoin(h, f[1], f[0], f[2]) }, ErrAuth},
		{"a middle frame is dropped", "multi.dpbk", func(h []byte, f [][]byte) []byte { return fmJoin(h, f[0], f[2]) }, ErrAuth},
		{"a frame is repeated", "multi.dpbk", func(h []byte, f [][]byte) []byte { return fmJoin(h, f[0], f[0], f[1], f[2]) }, ErrAuth},
		{"a frame follows the last frame", "short.dpbk", func(h []byte, f [][]byte) []byte { return fmJoin(h, f[0], f[0]) }, ErrAuth},
		{"a byte follows the last frame", "short.dpbk", func(h []byte, f [][]byte) []byte { return append(fmJoin(h, f...), 0) }, ErrAuth},
		{"the salt is changed", "short.dpbk", flipHeader(14, 1), ErrAuth},
		{"the nonce prefix is changed", "short.dpbk", flipHeader(30, 1), ErrAuth},
		{"argon2 t is changed to another valid value", "short.dpbk", flipHeader(8, 2), ErrAuth},
		{"the version is changed", "short.dpbk", flipHeader(4, 1), ErrNotBackup},
		{"the magic is changed", "short.dpbk", flipHeader(0, 1), ErrNotBackup},
	} {
		file := fmVectorFile(t, tt.file)
		hdr, frames := fmSplit(t, file)
		_, err := Read(bytes.NewReader(tt.do(hdr, frames)), fmPass(), func(string, int64, io.Reader) error { return nil })
		if !errors.Is(err, tt.want) {
			t.Errorf("%s: Read = %v, want %v", tt.name, err, tt.want)
		}
		for _, other := range []error{ErrNotBackup, ErrHeader, ErrAuth, ErrTruncated, ErrMembers} {
			if !errors.Is(other, tt.want) && errors.Is(err, other) {
				t.Errorf("%s: Read = %v, which also matches %v", tt.name, err, other)
			}
		}
	}
}

func TestFmReadWrongPassphrase(t *testing.T) {
	file := fmVectorFile(t, "short.dpbk")
	called := false
	_, err := Read(bytes.NewReader(file), []byte("not the passphrase"), func(string, int64, io.Reader) error {
		called = true
		return nil
	})
	if !errors.Is(err, ErrAuth) || errors.Is(err, ErrTruncated) {
		t.Errorf("Read with a wrong passphrase = %v, want ErrAuth", err)
	}
	if called {
		t.Error("a member reached the sink before its frame authenticated")
	}
}

func fmHeaderBytes(version byte, t, m uint32, p uint8) []byte {
	h := make([]byte, HeaderSize)
	copy(h, Magic)
	h[4] = version
	binary.BigEndian.PutUint32(h[5:], t)
	binary.BigEndian.PutUint32(h[9:], m)
	h[13] = p
	return h
}

// TestFmReadHeader: a header out of bounds is refused before any key is
// derived (argon2 would panic on t or p of zero and allocate m KiB).
func TestFmReadHeader(t *testing.T) {
	good := fmHeaderBytes(1, 1, 65536, 1)
	for _, tt := range []struct {
		name string
		in   []byte
		want error
	}{
		{"empty input", nil, ErrNotBackup},
		{"the magic only", []byte("DPBK"), ErrNotBackup},
		{"another magic", append([]byte("XPBK"), good[4:]...), ErrNotBackup},
		{"version 2", fmHeaderBytes(2, 1, 65536, 1), ErrNotBackup},
		{"version 0", fmHeaderBytes(0, 1, 65536, 1), ErrNotBackup},
		{"the header cut after the version", good[:5], ErrTruncated},
		{"the header cut inside the nonce prefix", good[:HeaderSize-1], ErrTruncated},
		{"t zero", fmHeaderBytes(1, 0, 65536, 1), ErrHeader},
		{"t eleven", fmHeaderBytes(1, 11, 65536, 1), ErrHeader},
		{"t at the maximum of uint32", fmHeaderBytes(1, 1<<32-1, 65536, 1), ErrHeader},
		{"m below 64 MiB", fmHeaderBytes(1, 1, 65535, 1), ErrHeader},
		{"m zero", fmHeaderBytes(1, 1, 0, 1), ErrHeader},
		{"m above 1 GiB", fmHeaderBytes(1, 1, 1<<20+1, 1), ErrHeader},
		{"p zero", fmHeaderBytes(1, 1, 65536, 0), ErrHeader},
		{"p seventeen", fmHeaderBytes(1, 1, 65536, 17), ErrHeader},
		{"p 255", fmHeaderBytes(1, 1, 65536, 255), ErrHeader},
		{"m at the maximum of uint32", fmHeaderBytes(1, 1, 1<<32-1, 1), ErrHeader},
		{"a valid header and no frame", good, ErrTruncated},
	} {
		_, err := Read(bytes.NewReader(tt.in), fmPass(), func(string, int64, io.Reader) error { return nil })
		if !errors.Is(err, tt.want) {
			t.Errorf("%s: Read = %v, want %v", tt.name, err, tt.want)
		}
	}
}

func TestFmReadSink(t *testing.T) {
	file := fmVectorFile(t, "short.dpbk")
	errSink := errors.New("sink refused")
	_, err := Read(bytes.NewReader(file), fmPass(), func(string, int64, io.Reader) error { return errSink })
	if !errors.Is(err, errSink) {
		t.Errorf("Read with a failing sink = %v, want the sink's error", err)
	}
	var names []string
	if _, err := Read(bytes.NewReader(file), fmPass(), func(name string, _ int64, _ io.Reader) error {
		names = append(names, name)
		return nil
	}); err != nil || !slices.Equal(names, []string{MemberDB, MemberKey}) {
		t.Errorf("Read with a sink that ignores the bodies = %v after %v, want nil after both members", err, names)
	}
}

func TestFmReadShortReads(t *testing.T) {
	v := fmVectors()[0]
	file := fmVectorFile(t, v.file)
	for name, r := range map[string]io.Reader{
		"one byte at a time":  iotest.OneByteReader(bytes.NewReader(file)),
		"data with its error": iotest.DataErrReader(bytes.NewReader(file)),
	} {
		res := fmRead(r, fmPass())
		if res.err != nil || !bytes.Equal(res.db, v.db) || !bytes.Equal(res.key, v.key) {
			t.Errorf("%s: Read = %v, members equal %v", name, res.err, bytes.Equal(res.db, v.db))
		}
	}
}

func TestFmWriteRefuses(t *testing.T) {
	keyAt := func(n int) Member {
		return Member{Name: MemberKey, Size: int64(n), Body: bytes.NewReader(bytes.Repeat([]byte{'k'}, n))}
	}
	dbOf := func(n int) Member {
		return Member{Name: MemberDB, Size: int64(n), Body: bytes.NewReader(make([]byte, n))}
	}
	for _, tt := range []struct {
		name    string
		p       Params
		members []Member
		want    error
	}{
		{"no member", TestParams, nil, ErrMembers},
		{"the database only", TestParams, []Member{dbOf(10)}, ErrMembers},
		{"the members in the wrong order", TestParams, []Member{keyAt(10), dbOf(10)}, ErrMembers},
		{"a third member", TestParams, []Member{dbOf(10), keyAt(10), dbOf(1)}, ErrMembers},
		{"another name", TestParams, []Member{{Name: "other", Size: 1, Body: bytes.NewReader([]byte{1})}, keyAt(10)}, ErrMembers},
		{"a key member over the limit", TestParams, []Member{dbOf(10), keyAt(MaxKeyMember + 1)}, ErrMembers},
		{"a negative size", TestParams, []Member{{Name: MemberDB, Size: -1, Body: bytes.NewReader(nil)}, keyAt(10)}, ErrMembers},
	} {
		var buf bytes.Buffer
		err := Write(&buf, fmRnd(), fmPass(), tt.p, tt.members)
		if !errors.Is(err, tt.want) {
			t.Errorf("%s: Write = %v, want %v", tt.name, err, tt.want)
		}
		if buf.Len() != 0 {
			t.Errorf("%s: Write wrote %d bytes before refusing", tt.name, buf.Len())
		}
	}
	var buf bytes.Buffer
	err := Write(&buf, fmRnd(), fmPass(), Params{T: 0, M: 65536, P: 1}, fmMembers([]byte("db"), []byte("key")))
	var ce *errcode.Error
	if !errors.As(err, &ce) || ce.Code != errcode.InvalidInput || buf.Len() != 0 {
		t.Errorf("Write with t=0 = %v after %d bytes, want request.invalid_input and nothing written", err, buf.Len())
	}
	if err := Write(io.Discard, fmRnd(), fmPass(), TestParams, []Member{dbOf(10), keyAt(MaxKeyMember)}); err != nil {
		t.Errorf("Write with a key member at the limit = %v", err)
	}
	if err := Write(io.Discard, fmRnd(), fmPass(), TestParams, []Member{dbOf(10), {Name: MemberKey, Size: 10, Body: bytes.NewReader([]byte("short"))}}); err == nil {
		t.Error("Write accepted a member whose body ends before its size")
	}
	body := bytes.NewReader([]byte("0123456789"))
	if err := Write(io.Discard, fmRnd(), fmPass(), TestParams, []Member{{Name: MemberDB, Size: 4, Body: body}, keyAt(10)}); err != nil || body.Len() != 6 {
		t.Errorf("Write with a longer body = %v, %d bytes left unread; want nil and 6", err, body.Len())
	}
}

var errFmWrite = errors.New("disk full")

type fmFailWriter struct{ budget int }

func (w *fmFailWriter) Write(p []byte) (int, error) {
	if len(p) > w.budget {
		return 0, errFmWrite
	}
	w.budget -= len(p)
	return len(p), nil
}

func TestFmWriteFailures(t *testing.T) {
	v := fmVectors()[0]
	if err := Write(io.Discard, bytes.NewReader(make([]byte, 16)), fmPass(), TestParams, fmMembers(v.db, v.key)); err == nil {
		t.Error("Write accepted 16 random bytes where it needs 32")
	}
	for name, budget := range map[string]int{"the header": 0, "the first frame": HeaderSize + 10} {
		err := Write(&fmFailWriter{budget: budget}, fmRnd(), fmPass(), TestParams, fmMembers(v.db, v.key))
		if !errors.Is(err, errFmWrite) {
			t.Errorf("Write failing at %s = %v, want the writer's error", name, err)
		}
	}
}

func TestFmClearsPassphrase(t *testing.T) {
	v := fmVectors()[0]
	zeroed := func(b []byte) bool { return !slices.ContainsFunc(b, func(c byte) bool { return c != 0 }) }
	pass := fmPass()
	if err := Write(io.Discard, fmRnd(), pass, TestParams, fmMembers(v.db, v.key)); err != nil || !zeroed(pass) {
		t.Errorf("after Write: err %v, passphrase zeroed %v", err, zeroed(pass))
	}
	pass = fmPass()
	if res := fmRead(bytes.NewReader(fmVectorFile(t, v.file)), pass); res.err != nil || !zeroed(pass) {
		t.Errorf("after Read: err %v, passphrase zeroed %v", res.err, zeroed(pass))
	}
	pass = []byte("wrong")
	if res := fmRead(bytes.NewReader(fmVectorFile(t, v.file)), pass); !errors.Is(res.err, ErrAuth) || !zeroed(pass) {
		t.Errorf("after a failed Read: err %v, passphrase zeroed %v", res.err, zeroed(pass))
	}
	pass = fmPass()
	if _, err := Read(bytes.NewReader(nil), pass, nil); !errors.Is(err, ErrNotBackup) || !zeroed(pass) {
		t.Errorf("after a refused header: err %v, passphrase zeroed %v", err, zeroed(pass))
	}
}

// TestFmMemoryBound streams a 128 MiB database through Write and Read; the
// allocation of each is argon2id's 64 MiB plus a margin, not the database.
func TestFmMemoryBound(t *testing.T) {
	const dbSize = 128 << 20
	key := fmKeyFile(3)
	limit := uint64(64+32) << 20
	path := filepath.Join(t.TempDir(), "big.dpbk")
	out, err := os.Create(path) //nolint:gosec // G304: a path under t.TempDir()
	if err != nil {
		t.Fatal(err)
	}
	written := sha256.New()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	err = Write(out, fmRnd(), fmPass(), TestParams, []Member{
		{Name: MemberDB, Size: dbSize, Body: io.TeeReader(newFmStream(dbSize, 7), written)},
		{Name: MemberKey, Size: int64(len(key)), Body: bytes.NewReader(key)},
	})
	runtime.ReadMemStats(&after)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if grew := after.TotalAlloc - before.TotalAlloc; grew >= limit {
		t.Errorf("Write allocated %d MiB for a %d MiB database, want under %d MiB", grew>>20, dbSize>>20, limit>>20)
	}
	if st, err := os.Stat(path); err != nil || st.Size() < dbSize {
		t.Fatalf("backup file: %v, %v", st, err)
	}

	in, err := os.Open(path) //nolint:gosec // G304: a path under t.TempDir()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = in.Close() }()
	read := sha256.New()
	var sawDB int64
	runtime.ReadMemStats(&before)
	_, err = Read(in, fmPass(), func(name string, size int64, body io.Reader) error {
		dst := io.Discard
		if name == MemberDB {
			dst = read
			sawDB = size
		}
		_, err := io.Copy(dst, body)
		return err
	})
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if grew := after.TotalAlloc - before.TotalAlloc; grew >= limit {
		t.Errorf("Read allocated %d MiB for a %d MiB database, want under %d MiB", grew>>20, dbSize>>20, limit>>20)
	}
	if sawDB != dbSize || !bytes.Equal(read.Sum(nil), written.Sum(nil)) {
		t.Errorf("Read gave %d database bytes, digest equal %v", sawDB, bytes.Equal(read.Sum(nil), written.Sum(nil)))
	}
}
