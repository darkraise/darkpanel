package firewall

import (
	"bytes"
	"strings"
	"testing"
)

// fz7Head returns at most the first 64 bytes of s, for a failure message.
func fz7Head(s string) string { return s[:min(len(s), 64)] }

// FuzzInstalledVersion feeds an arbitrary dpkg status database to
// installedVersion and an arbitrary version to upstreamAtLeast: an installed
// version is non-empty, has no surrounding space and occurs in the database
// beside the package name and the word installed; a version that
// upstreamAtLeast reads is at least 0, and at least 0.34 when it is at least
// 0.35 (R74).
func FuzzInstalledVersion(f *testing.F) {
	f.Add([]byte("Package: ufw\nStatus: install ok installed\nVersion: 0.36.2-6\n"), "0.36.2-6")
	f.Add([]byte("Package: curl\nStatus: install ok installed\nVersion: 8\n\nPackage: ufw\nStatus: install ok installed\nVersion: 2:0.35\nDescription: x\n y\n"), "2:0.35")
	f.Add([]byte("Package: ufw\nStatus: deinstall ok config-files\nVersion: 0.36.2-6\n"), "0.34.1-2")
	f.Add([]byte("Package: ufw-extras\nStatus: install ok installed\nVersion: 1.0\n"), "")
	f.Add([]byte("Package: ufw\r\nStatus: install ok installed\r\nVersion:  0.36 \r\n"), "-1")
	f.Add([]byte(""), "x.35")
	f.Add([]byte("Package: ufw\nStatus: install ok installed\n"), "0..35")
	f.Fuzz(func(t *testing.T, data []byte, version string) {
		v, ok := installedVersion(data, "ufw")
		if ok {
			if v == "" || v != strings.TrimSpace(v) {
				t.Fatalf("installed version %q for %q", fz7Head(v), fz7Head(string(data)))
			}
			for _, part := range []string{"ufw", "installed", v} {
				if !bytes.Contains(data, []byte(part)) {
					t.Fatalf("installed version %q is not in %q", fz7Head(v), fz7Head(string(data)))
				}
			}
		}
		for _, s := range []string{version, v} {
			atLeast, err := upstreamAtLeast(s, []int{0, 35})
			if err != nil {
				if atLeast {
					t.Fatalf("upstreamAtLeast(%q) is true with the error %v", fz7Head(s), err)
				}
				continue
			}
			if zero, err := upstreamAtLeast(s, []int{0}); err != nil || !zero {
				t.Fatalf("upstreamAtLeast(%q, 0) = %v, %v", fz7Head(s), zero, err)
			}
			if lower, err := upstreamAtLeast(s, []int{0, 34}); atLeast && (err != nil || !lower) {
				t.Fatalf("upstreamAtLeast(%q) holds for 0.35 but not for 0.34: %v, %v", fz7Head(s), lower, err)
			}
		}
	})
}
