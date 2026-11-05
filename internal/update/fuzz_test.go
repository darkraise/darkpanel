package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

var fz9HexPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func fz9Head(s string) string {
	if len(s) > 64 {
		return s[:64]
	}
	return s
}

// FuzzParseVersion checks that a version ParseVersion accepts prints and parses back to itself, and that Compare
// is reflexive and antisymmetric over any two accepted versions.
func FuzzParseVersion(f *testing.F) {
	for _, pair := range [][2]string{
		{"1.0.0-rc.2", "1.0.0-rc.10"},
		{"1.0.0-rc.1", "1.0.0"},
		{"1.2.3", "1.10.0"},
		{"1.0.0-alpha", "1.0.0-alpha.1"},
		{"1.0.0-beta.007", "1.0.0-beta.7"},
		{"1.0.0-99999999999999999999", "1.0.0-100000000000000000000"},
		{"99999999999999999999.0.0", "0.0.0"},
		{"v1.2.3", "1.2"},
		{"1.2.3+build", "1.2.3-"},
	} {
		f.Add(pair[0], pair[1])
	}
	f.Fuzz(func(t *testing.T, a, b string) {
		va, errA := ParseVersion(a)
		vb, errB := ParseVersion(b)
		for _, c := range []struct {
			in  string
			v   Version
			err error
		}{{a, va, errA}, {b, vb, errB}} {
			if c.err != nil {
				continue
			}
			if back, err := ParseVersion(c.v.String()); err != nil || back != c.v {
				t.Fatalf("ParseVersion(%q) = %+v prints as %q, which parses as %+v, %v", fz9Head(c.in), c.v, c.v.String(), back, err)
			}
			if got := c.v.Compare(c.v); got != 0 {
				t.Fatalf("%+v.Compare(itself) = %d, want 0", c.v, got)
			}
		}
		if errA != nil || errB != nil {
			return
		}
		if ab, ba := va.Compare(vb), vb.Compare(va); ab != -ba {
			t.Fatalf("Compare(%q, %q) = %d but Compare(%q, %q) = %d", fz9Head(a), fz9Head(b), ab, fz9Head(b), fz9Head(a), ba)
		}
	})
}

// FuzzChecksumFor checks that a hash checksumFor returns is 64 lower-case hexadecimal characters and is the one
// a line of the input lists for the requested name.
func FuzzChecksumFor(f *testing.F) {
	sum := strings.Repeat("ab", 32)
	const name = "darkpanel_1.3.0_linux_amd64.tar.gz"
	f.Add(sum+"  "+name+"\n", name)
	f.Add(sum+" *"+name+"\n", name)
	f.Add(sum+"  other.tar.gz\n"+sum+"  "+name, name)
	f.Add(sum+"  "+name+"\n"+sum+"  "+name+"\n", name)
	f.Add(strings.ToUpper(sum)+"  "+name+"\n", name)
	f.Add(sum+"\t"+name+"\n", name)
	f.Add("", name)
	f.Fuzz(func(t *testing.T, checksums, name string) {
		got, err := checksumFor([]byte(checksums), name)
		if err != nil {
			return
		}
		if !fz9HexPattern.MatchString(got) {
			t.Fatalf("checksumFor(%q, %q) = %q, want 64 lower-case hexadecimal characters", fz9Head(checksums), fz9Head(name), got)
		}
		lines := strings.Split(checksums, "\n")
		if !slices.Contains(lines, got+"  "+name) && !slices.Contains(lines, got+" *"+name) {
			t.Fatalf("checksumFor(%q, %q) = %q, which no line of the input lists for the name", fz9Head(checksums), fz9Head(name), got)
		}
	})
}

type fz9Entry struct {
	name string
	typ  byte
	body string
	link string
}

func fz9Tar(tb testing.TB, entries ...fz9Entry) []byte {
	tb.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Typeflag: e.typ, Mode: 0o755, Linkname: e.link}
		if e.typ == tar.TypeReg {
			hdr.Size = int64(len(e.body))
		}
		if err := tw.WriteHeader(hdr); err != nil {
			tb.Fatal(err)
		}
		if e.typ == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				tb.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		tb.Fatal(err)
	}
	return buf.Bytes()
}

// FuzzExtractBinary checks that extractBinary, given a gzipped tar, writes at most one file, and that file is the
// destination. The input is the uncompressed tar, which the target compresses itself so that mutations reach the
// tar reader.
func FuzzExtractBinary(f *testing.F) {
	const reg = tar.TypeReg
	for _, entries := range [][]fz9Entry{
		{{name: "darkpanel", typ: reg, body: "binary"}},
		{{name: "./darkpanel", typ: reg, body: "binary"}},
		{{name: "../darkpanel", typ: reg, body: "binary"}},
		{{name: "sub/darkpanel", typ: reg, body: "binary"}},
		{{name: "LICENSE", typ: reg, body: "text"}, {name: "darkpanel", typ: reg, body: "binary"}},
		{{name: "darkpanel", typ: reg, body: "one"}, {name: "darkpanel", typ: reg, body: "two"}},
		{{name: "darkpanel", typ: tar.TypeSymlink, link: "/etc/passwd"}},
		{{name: "darkpanel", typ: tar.TypeDir}},
		{{name: "LICENSE", typ: reg, body: "text"}},
		{},
	} {
		f.Add(fz9Tar(f, entries...))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		root := t.TempDir()
		var gz bytes.Buffer
		zw := gzip.NewWriter(&gz)
		if _, err := zw.Write(raw); err != nil {
			t.Fatal(err)
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		tarball := filepath.Join(root, "release.tar.gz")
		if err := os.WriteFile(tarball, gz.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
		out := filepath.Join(root, "out")
		if err := os.Mkdir(out, 0o700); err != nil {
			t.Fatal(err)
		}
		dst := filepath.Join(out, "darkpanel")

		err := extractBinary(tarball, dst)

		var files []string
		walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			switch {
			case !d.IsDir():
				files = append(files, path)
			case path != root && path != out:
				t.Fatalf("extractBinary(%q) created the directory %s", fz9Head(string(raw)), path)
			}
			return nil
		})
		if walkErr != nil {
			t.Fatal(walkErr)
		}
		for _, p := range files {
			if p != tarball && p != dst {
				t.Fatalf("extractBinary(%q) wrote %s, want at most the destination", fz9Head(string(raw)), p)
			}
		}
		if err == nil && !slices.Contains(files, dst) {
			t.Fatalf("extractBinary(%q) succeeded and wrote no destination", fz9Head(string(raw)))
		}
	})
}
