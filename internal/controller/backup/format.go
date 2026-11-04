package backup

import (
	"archive/tar"
	"bufio"
	"crypto/cipher"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"time"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/chacha20poly1305"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

const (
	// Magic opens every backup file.
	Magic = "DPBK"
	// FormatVersion is the format version byte that follows Magic.
	FormatVersion = byte(1)
	// HeaderSize is the length of the header: magic 4, version 1, t 4, m 4, p 1, salt 16, nonce prefix 16.
	HeaderSize = 46
	// ChunkSize is the most plaintext one frame holds.
	ChunkSize = 64 << 10
	// MemberDB is the archive member holding the controller database.
	MemberDB = "controller.db"
	// MemberKey is the archive member holding the controller key file.
	MemberKey = "controller.key"
	// MaxKeyMember is the largest key member, in bytes, a backup may hold.
	MaxKeyMember = 4096
)

const (
	formatKeySize   = 32
	formatSaltSize  = 16
	formatTagSize   = chacha20poly1305.Overhead
	formatMaxChunks = 1 << 32
	formatReadBuf   = 4096
)

// Params are the argon2id parameters a backup's key is derived with.
type Params struct {
	T uint32 `json:"t"` // 1-10
	M uint32 `json:"m"` // KiB, 65536-1048576
	P uint8  `json:"p"` // 1-16
}

// DefaultParams are the argon2id parameters of a backup that names none.
var DefaultParams = Params{T: 3, M: 65536, P: 4}

// TestParams are the smallest parameters the format accepts; tests only.
var TestParams = Params{T: 1, M: 65536, P: 1}

// Validate refuses a parameter out of bounds with request.invalid_input naming argon2.t, argon2.m or argon2.p.
func (p Params) Validate() error {
	switch {
	case p.T < 1 || p.T > 10:
		return errcode.New(errcode.InvalidInput, "argon2.t must be between 1 and 10")
	case p.M < 65536 || p.M > 1<<20:
		return errcode.New(errcode.InvalidInput, "argon2.m must be between 65536 and 1048576 KiB")
	case p.P < 1 || p.P > 16:
		return errcode.New(errcode.InvalidInput, "argon2.p must be between 1 and 16")
	}
	return nil
}

// Member is one archive member: exactly Size bytes are read from Body.
type Member struct {
	Name string
	Size int64
	Body io.Reader
}

// Header is a parsed backup header.
type Header struct {
	Params      Params
	Salt        [16]byte
	NoncePrefix [16]byte
}

// The errors Read wraps with %w; callers use errors.Is.
var (
	// ErrNotBackup: the magic or the version is not this format's.
	ErrNotBackup = errors.New("backup: not a darkpanel backup file")
	// ErrHeader: the header's argon2id parameters are out of bounds.
	ErrHeader = errors.New("backup: the backup header is out of bounds")
	// ErrAuth: a frame failed to open, a frame length is out of bounds or data follows the last frame.
	ErrAuth = errors.New("backup: wrong passphrase or damaged backup file")
	// ErrTruncated: the file ends before a frame sealed as the last one.
	ErrTruncated = errors.New("backup: the backup file is truncated")
	// ErrMembers: the archive does not hold exactly controller.db then controller.key.
	ErrMembers = errors.New("backup: the archive must hold exactly controller.db and controller.key")
)

// Write writes the header (salt then nonce prefix read from rnd, 32 bytes) and the frames of a tar
// holding exactly members[0] = MemberDB and members[1] = MemberKey (regular files, mode 0600,
// uid and gid 0, mod time Unix 0). Memory stays O(ChunkSize) plus argon2id's m.
// Write zeroes passphrase.
func Write(w io.Writer, rnd io.Reader, passphrase []byte, p Params, members []Member) error {
	defer clear(passphrase)
	if err := p.Validate(); err != nil {
		return err
	}
	if err := formatCheckMembers(members); err != nil {
		return err
	}
	var hdr Header
	hdr.Params = p
	var random [2 * formatSaltSize]byte
	if _, err := io.ReadFull(rnd, random[:]); err != nil {
		return fmt.Errorf("backup: read the salt and nonce prefix: %w", err)
	}
	copy(hdr.Salt[:], random[:formatSaltSize])
	copy(hdr.NoncePrefix[:], random[formatSaltSize:])

	key := argon2.IDKey(passphrase, hdr.Salt[:], p.T, p.M, p.P, formatKeySize)
	clear(passphrase)
	defer clear(key)
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return fmt.Errorf("backup: %w", err)
	}

	raw := formatEncodeHeader(hdr)
	if _, err := w.Write(raw); err != nil {
		return fmt.Errorf("backup: write the header: %w", err)
	}
	fw := frameNewWriter(w, aead, raw)
	tw := tar.NewWriter(fw)
	for _, m := range members {
		th := &tar.Header{
			Typeflag: tar.TypeReg,
			Name:     m.Name,
			Size:     m.Size,
			Mode:     0o600,
			ModTime:  time.Unix(0, 0),
		}
		if err := tw.WriteHeader(th); err != nil {
			return fmt.Errorf("backup: write member %s: %w", m.Name, err)
		}
		if _, err := io.Copy(tw, io.LimitReader(m.Body, m.Size)); err != nil {
			return fmt.Errorf("backup: write member %s: %w", m.Name, err)
		}
	}
	if err := tw.Close(); err != nil {
		return fmt.Errorf("backup: finish the archive: %w", err)
	}
	return fw.finish()
}

func formatCheckMembers(members []Member) error {
	if len(members) != 2 || members[0].Name != MemberDB || members[1].Name != MemberKey {
		return fmt.Errorf("%w: members must be %s then %s", ErrMembers, MemberDB, MemberKey)
	}
	for _, m := range members {
		if m.Size < 0 {
			return fmt.Errorf("%w: %s has a negative size", ErrMembers, m.Name)
		}
	}
	if members[1].Size > MaxKeyMember {
		return fmt.Errorf("%w: %s is over %d bytes", ErrMembers, MemberKey, MaxKeyMember)
	}
	return nil
}

func formatEncodeHeader(h Header) []byte {
	raw := make([]byte, 0, HeaderSize)
	raw = append(raw, Magic...)
	raw = append(raw, FormatVersion)
	raw = binary.BigEndian.AppendUint32(raw, h.Params.T)
	raw = binary.BigEndian.AppendUint32(raw, h.Params.M)
	raw = append(raw, h.Params.P)
	raw = append(raw, h.Salt[:]...)
	return append(raw, h.NoncePrefix[:]...)
}

// formatNonceAD returns the nonce buffer (the header's prefix, the chunk
// index still zero) and the associated-data buffer (the header, then the
// last-chunk flag) a frame writer or reader reuses for every frame.
func formatNonceAD(raw []byte) (nonce, ad []byte) {
	nonce = make([]byte, chacha20poly1305.NonceSizeX)
	copy(nonce, raw[HeaderSize-16:HeaderSize])
	ad = make([]byte, HeaderSize+1)
	copy(ad, raw)
	return nonce, ad
}

// frameWriter seals the plaintext written to it into frames. It holds a full
// chunk back until more plaintext arrives, so only finish seals the last one.
type frameWriter struct {
	w     io.Writer
	aead  cipher.AEAD
	nonce []byte
	ad    []byte
	plain []byte
	n     int
	out   []byte
	index uint64
}

func frameNewWriter(w io.Writer, aead cipher.AEAD, raw []byte) *frameWriter {
	nonce, ad := formatNonceAD(raw)
	return &frameWriter{
		w:     w,
		aead:  aead,
		nonce: nonce,
		ad:    ad,
		plain: make([]byte, ChunkSize),
		out:   make([]byte, 4+ChunkSize+formatTagSize),
	}
}

func (f *frameWriter) Write(p []byte) (int, error) {
	written := 0
	for len(p) > 0 {
		if f.n == len(f.plain) {
			if err := f.seal(false); err != nil {
				return written, err
			}
		}
		c := copy(f.plain[f.n:], p)
		f.n += c
		written += c
		p = p[c:]
	}
	return written, nil
}

func (f *frameWriter) finish() error {
	return f.seal(true)
}

func (f *frameWriter) seal(last bool) error {
	if f.index >= formatMaxChunks {
		return errors.New("backup: the archive needs more than 2^32 chunks")
	}
	binary.BigEndian.PutUint64(f.nonce[16:], f.index)
	f.ad[HeaderSize] = formatLastFlag(last)
	frame := f.aead.Seal(f.out[:4], f.nonce, f.plain[:f.n], f.ad)
	binary.BigEndian.PutUint32(frame, uint32(len(frame)-4)) //nolint:gosec // G115: at most ChunkSize+16
	if _, err := f.w.Write(frame); err != nil {
		return fmt.Errorf("backup: write a frame: %w", err)
	}
	f.index++
	f.n = 0
	return nil
}

func formatLastFlag(last bool) byte {
	if last {
		return 1
	}
	return 0
}

// Read checks the header's bounds before deriving the key, then decrypts frame by frame and calls
// sink once per member, in order, with a reader of exactly size bytes. Read returns nil only when
// the last frame authenticated with 0x01, the tar held exactly the two members and nothing but tar
// padding followed; bytes a sink received are unauthenticated as a whole until then.
// Read zeroes passphrase and returns the zero Header with every error.
func Read(r io.Reader, passphrase []byte, sink func(name string, size int64, body io.Reader) error) (Header, error) {
	defer clear(passphrase)
	br := bufio.NewReaderSize(r, formatReadBuf)
	raw := make([]byte, HeaderSize)
	hdr, err := formatReadHeader(br, raw)
	if err != nil {
		return Header{}, err
	}

	key := argon2.IDKey(passphrase, hdr.Salt[:], hdr.Params.T, hdr.Params.M, hdr.Params.P, formatKeySize)
	clear(passphrase)
	defer clear(key)
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return Header{}, fmt.Errorf("backup: %w", err)
	}

	fr := frameNewReader(br, aead, raw)
	if err := formatReadMembers(fr, sink); err != nil {
		return Header{}, err
	}
	if err := fr.drainZero(); err != nil {
		return Header{}, err
	}
	return hdr, nil
}

// formatReadHeader reads and bounds-checks the header into raw; nothing in
// it reaches argon2 unchecked, since argon2 panics on t or p of zero and
// allocates m KiB.
func formatReadHeader(r io.Reader, raw []byte) (Header, error) {
	if _, err := io.ReadFull(r, raw[:5]); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return Header{}, fmt.Errorf("%w: the file is shorter than its magic and version", ErrNotBackup)
		}
		return Header{}, fmt.Errorf("backup: read the header: %w", err)
	}
	if string(raw[:4]) != Magic || raw[4] != FormatVersion {
		return Header{}, ErrNotBackup
	}
	if _, err := io.ReadFull(r, raw[5:]); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return Header{}, fmt.Errorf("%w: the file ends inside the header", ErrTruncated)
		}
		return Header{}, fmt.Errorf("backup: read the header: %w", err)
	}
	var h Header
	h.Params = Params{
		T: binary.BigEndian.Uint32(raw[5:9]),
		M: binary.BigEndian.Uint32(raw[9:13]),
		P: raw[13],
	}
	if err := h.Params.Validate(); err != nil {
		return Header{}, fmt.Errorf("%w: %w", ErrHeader, err)
	}
	copy(h.Salt[:], raw[14:30])
	copy(h.NoncePrefix[:], raw[30:46])
	return h, nil
}

// formatReadMembers walks the tar in fr and hands its two members to sink.
// Once fr has failed, its error explains every tar error that follows.
func formatReadMembers(fr *frameReader, sink func(name string, size int64, body io.Reader) error) error {
	tr := tar.NewReader(fr)
	for _, want := range []string{MemberDB, MemberKey} {
		h, err := tr.Next()
		if err != nil {
			if fr.err != nil {
				return fr.err
			}
			if errors.Is(err, io.EOF) {
				return fmt.Errorf("%w: %s is missing", ErrMembers, want)
			}
			return fmt.Errorf("%w: %w", ErrMembers, err)
		}
		if h.Typeflag != tar.TypeReg || h.Name != want {
			return fmt.Errorf("%w: expected the regular file %s", ErrMembers, want)
		}
		if want == MemberKey && h.Size > MaxKeyMember {
			return fmt.Errorf("%w: %s is over %d bytes", ErrMembers, MemberKey, MaxKeyMember)
		}
		if err := sink(want, h.Size, io.LimitReader(tr, h.Size)); err != nil {
			return err
		}
	}
	_, err := tr.Next()
	switch {
	case fr.err != nil:
		return fr.err
	case err == nil:
		return fmt.Errorf("%w: the archive holds a third member", ErrMembers)
	case !errors.Is(err, io.EOF):
		return fmt.Errorf("%w: %w", ErrMembers, err)
	}
	return nil
}

// frameReader opens the frames of a backup file one at a time and reads out
// their plaintext; plain only ever holds plaintext of a frame that opened.
type frameReader struct {
	r     *bufio.Reader
	aead  cipher.AEAD
	nonce []byte
	ad    []byte
	ct    []byte
	plain []byte
	off   int
	index uint64
	last  bool
	err   error
}

func frameNewReader(r *bufio.Reader, aead cipher.AEAD, raw []byte) *frameReader {
	nonce, ad := formatNonceAD(raw)
	return &frameReader{
		r:     r,
		aead:  aead,
		nonce: nonce,
		ad:    ad,
		ct:    make([]byte, ChunkSize+formatTagSize),
		plain: make([]byte, 0, ChunkSize),
	}
}

func (f *frameReader) Read(p []byte) (int, error) {
	if err := f.fill(); err != nil {
		return 0, err
	}
	n := copy(p, f.plain[f.off:])
	f.off += n
	return n, nil
}

// fill opens frames until plaintext is available; it returns io.EOF after
// the last frame and the sticky error of a failed frame.
func (f *frameReader) fill() error {
	for f.off == len(f.plain) {
		if f.last {
			return io.EOF
		}
		if f.err != nil {
			return f.err
		}
		if f.err = f.next(); f.err != nil {
			return f.err
		}
	}
	return nil
}

// drainZero reads the rest of the plaintext to the last frame, which it must
// reach for the last frame to authenticate; it refuses anything but zeros.
func (f *frameReader) drainZero() error {
	for {
		for _, b := range f.plain[f.off:] {
			if b != 0 {
				return fmt.Errorf("%w: data follows the end of the archive", ErrMembers)
			}
		}
		f.off = len(f.plain)
		if err := f.fill(); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

func (f *frameReader) next() error {
	f.plain, f.off = f.plain[:0], 0
	var size [4]byte
	if _, err := io.ReadFull(f.r, size[:]); err != nil {
		return formatFrameReadErr(err, f.index)
	}
	n := binary.BigEndian.Uint32(size[:])
	if n < formatTagSize || n > ChunkSize+formatTagSize {
		return fmt.Errorf("%w: frame %d has a length out of bounds", ErrAuth, f.index)
	}
	if f.index >= formatMaxChunks {
		return fmt.Errorf("%w: more than 2^32 frames", ErrAuth)
	}
	ct := f.ct[:n]
	if _, err := io.ReadFull(f.r, ct); err != nil {
		return formatFrameReadErr(err, f.index)
	}
	_, err := f.r.Peek(1)
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("backup: read the backup file: %w", err)
	}
	binary.BigEndian.PutUint64(f.nonce[16:], f.index)
	if err == nil {
		if pt, ok := f.open(ct, false); ok {
			f.plain = pt
			f.index++
			return nil
		}
		if _, ok := f.open(ct, true); ok {
			return fmt.Errorf("%w: data follows the last frame", ErrAuth)
		}
		return fmt.Errorf("%w: frame %d does not open", ErrAuth, f.index)
	}
	if pt, ok := f.open(ct, true); ok {
		f.plain = pt
		f.last = true
		f.index++
		return nil
	}
	if _, ok := f.open(ct, false); ok {
		return fmt.Errorf("%w: the file ends after frame %d", ErrTruncated, f.index)
	}
	return fmt.Errorf("%w: frame %d does not open", ErrAuth, f.index)
}

// open decrypts ct into the plaintext buffer without touching ct, so a
// failed attempt can be retried with the other flag.
func (f *frameReader) open(ct []byte, last bool) ([]byte, bool) {
	f.ad[HeaderSize] = formatLastFlag(last)
	pt, err := f.aead.Open(f.plain[:0], f.nonce, ct, f.ad)
	return pt, err == nil
}

func formatFrameReadErr(err error, index uint64) error {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return fmt.Errorf("%w: the file ends inside or before frame %d", ErrTruncated, index)
	}
	return fmt.Errorf("backup: read the backup file: %w", err)
}
