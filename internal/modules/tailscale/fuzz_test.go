package tailscale

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

// fz8Head returns at most the first 64 bytes of b, for a failure message.
func fz8Head(b []byte) []byte { return b[:min(len(b), 64)] }

// FuzzReadOSRelease feeds arbitrary /etc/os-release content to readOSRelease:
// it never panics, asks only for /etc/os-release, and reports a distribution
// id and a codename of one line each (R74).
func FuzzReadOSRelease(f *testing.F) {
	f.Add([]byte("NAME=\"Ubuntu\"\nVERSION_CODENAME=noble\nID=ubuntu\nID_LIKE=debian\n"))
	f.Add([]byte("PRETTY_NAME=\"Debian GNU/Linux 12 (bookworm)\"\nID=\"debian\"\nVERSION_CODENAME='bookworm'\n"))
	f.Add([]byte("# a comment\n\nID=debian\n  VERSION_CODENAME=trixie  \n#ID=ubuntu\n"))
	f.Add([]byte("ID=\"\nVERSION_CODENAME='\nID='x\"\n"))
	f.Add([]byte(""))
	f.Fuzz(func(t *testing.T, data []byte) {
		var asked []string
		info, err := readOSRelease(func(name string) ([]byte, error) {
			asked = append(asked, name)
			return data, nil
		})
		if err != nil {
			t.Fatalf("readOSRelease of %q: %v", fz8Head(data), err)
		}
		if len(asked) != 1 || asked[0] != "/etc/os-release" {
			t.Fatalf("readOSRelease read %q", asked)
		}
		if strings.Contains(info.ID, "\n") || strings.Contains(info.Codename, "\n") {
			t.Fatalf("readOSRelease of %q returned %+v", fz8Head(data), info)
		}
	})
}

// fz8Installed reports whether some stanza of a dpkg status file, stanzas
// being separated by blank lines, names the package tailscale, has a Status
// ending in " installed" and has the given Version, reading only lines that do
// not start with white space.
func fz8Installed(status, version string) bool {
	var named, installed, versioned bool
	end := func() bool {
		ok := named && installed && versioned
		named, installed, versioned = false, false, false
		return ok
	}
	for line := range strings.Lines(status) {
		if strings.TrimSpace(line) == "" {
			if end() {
				return true
			}
			continue
		}
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch key {
		case "Package":
			named = named || value == "tailscale"
		case "Status":
			installed = installed || strings.HasSuffix(value, " installed")
		case "Version":
			versioned = versioned || value == version
		}
	}
	return end()
}

// FuzzDpkgStatus feeds an arbitrary dpkg status database, through a fake file
// reader, to installedVersion: it never panics or fails, and it returns a
// non-empty version only for a Package: tailscale stanza whose Status ends in
// installed and which holds that version (R74).
func FuzzDpkgStatus(f *testing.F) {
	f.Add(thDpkgStatus("1.104.1"))
	f.Add(thDpkgStatus(""))
	f.Add([]byte("Package: tailscale\nStatus: deinstall ok config-files\nVersion: 1.102.0\n"))
	f.Add([]byte("Package: tailscale\nStatus: install ok unpacked\nVersion: 1.102.0\n"))
	f.Add([]byte("Package: tailscaled-extra\nStatus: install ok installed\nVersion: 3.0\n"))
	f.Add([]byte("Package: curl\nStatus: install ok installed\nVersion: 8\n\nPackage: tailscale\nStatus: install ok installed\nVersion: 1.2.3"))
	f.Add([]byte("Package: tailscale\n Status: install ok installed\nVersion: 1.0\n"))
	f.Add([]byte(""))
	f.Fuzz(func(t *testing.T, data []byte) {
		tt := ts{d: deps{readFile: func(string) ([]byte, error) { return data, nil }}}
		version, err := tt.installedVersion(context.Background(), core.Env{})
		if err != nil {
			t.Fatalf("installedVersion of %q: %v", fz8Head(data), err)
		}
		if version != "" && !fz8Installed(string(data), version) {
			t.Fatalf("installedVersion of %q returned %q, which no installed tailscale stanza holds", fz8Head(data), fz8Head([]byte(version)))
		}
	})
}

// FuzzKeyringFingerprints feeds arbitrary bytes to keyringFingerprints, seeded
// with the embedded keyring: it never panics, an error returns no
// fingerprints, and every fingerprint is 40 upper-case hexadecimal
// characters (R74).
func FuzzKeyringFingerprints(f *testing.F) {
	fingerprint := regexp.MustCompile(`^[0-9A-F]{40}$`)
	f.Add(repoKeyring)
	f.Add(repoKeyring[:len(repoKeyring)/2])
	f.Add([]byte{0x99, 0x00, 0x05, 0x04, 1, 2, 3, 4})
	f.Add([]byte{0xc6, 0x05, 0x04, 1, 2, 3, 4})
	f.Add([]byte{0x00})
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, b []byte) {
		fps, err := keyringFingerprints(b)
		if err != nil {
			if fps != nil {
				t.Fatalf("fingerprints returned with error %v for %q", err, fz8Head(b))
			}
			return
		}
		for _, fp := range fps {
			if !fingerprint.MatchString(fp) {
				t.Fatalf("fingerprint %q for %q", fp, fz8Head(b))
			}
		}
	})
}

// TestFz8DpkgStatusSeparator checks that only spaces separate the words of a
// Status field, as dpkg writes it.
func TestFz8DpkgStatusSeparator(t *testing.T) {
	for name, tc := range map[string]struct{ status, want string }{
		"form feed":      {"Package:tailscale\nStatus:0 0\finstalled\nVersion:0", ""},
		"tab":            {"Package: tailscale\nStatus: install ok\tinstalled\nVersion: 1.0\n", ""},
		"repeated space": {"Package: tailscale\nStatus: install  ok  installed\nVersion: 1.0\n", "1.0"},
		"plain":          {"Package: tailscale\nStatus: install ok installed\nVersion: 1.0\n", "1.0"},
	} {
		t.Run(name, func(t *testing.T) {
			tt := ts{d: deps{readFile: func(string) ([]byte, error) { return []byte(tc.status), nil }}}
			if got, err := tt.installedVersion(context.Background(), core.Env{}); err != nil || got != tc.want {
				t.Fatalf("installedVersion = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}
