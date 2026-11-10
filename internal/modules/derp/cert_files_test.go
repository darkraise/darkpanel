package derp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/derp/server"
)

// v1r15Split returns h's deps with two changes: a plain read of a path in the certificate directory fails the test,
// and the certificate reads go to the host's files.
func v1r15Split(t *testing.T, h *dpHost) dp {
	t.Helper()
	d := h.dp()
	plain := d.d.readFile
	d.d.readFile = func(name string) ([]byte, error) {
		if strings.HasPrefix(name, server.CertDir+"/") {
			t.Errorf("readFile opened %s; the certificate directory is read through readCertFile", name)
		}
		return plain(name)
	}
	d.d.readCertFile = h.readFile
	return d
}

// TestV1r15ProductionCertReadRefusesPlantedFiles: the production read is certFileReader over CertDir with a 1 MiB
// limit: it follows no link beneath CertDir, refuses a file over 1 MiB and reads one of exactly 1 MiB. The other
// planted files are TestV1r15ReadRegularFile's.
func TestV1r15ProductionCertReadRefusesPlantedFiles(t *testing.T) {
	const secret = "placeholder-secret-content"
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	put := func(name string, data []byte) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	crt := put("crt", []byte("CERT"))
	outside := put("outside", []byte(secret))
	exact := put("exact", bytes.Repeat([]byte("a"), 1<<20))
	over := put("over", bytes.Repeat([]byte("a"), 1<<20+1))
	link := filepath.Join(dir, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}

	if _, err := productionDP().d.readCertFile("/nonexistent/a.crt"); err == nil || !strings.Contains(err.Error(), server.CertDir) {
		t.Fatalf("the production read of a path outside CertDir = %v, want a refusal naming %s", err, server.CertDir)
	}
	read := certFileReader(dir)
	for _, tc := range []struct {
		name    string
		path    string
		wantLen int
		refused bool
	}{
		{"a regular certificate", crt, 4, false},
		{"exactly 1 MiB", exact, 1 << 20, false},
		{"one byte over 1 MiB", over, 0, true},
		{"a symlink to another file", link, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := read(tc.path)
			if tc.refused != (err != nil) || len(data) != tc.wantLen {
				t.Errorf("read %d bytes, error %v; want refused=%v and %d bytes", len(data), err, tc.refused, tc.wantLen)
			}
			if strings.Contains(string(data), secret) || (err != nil && strings.Contains(err.Error(), secret)) {
				t.Error("the read returned the content of a file behind a link")
			}
		})
	}
}

// TestV1r15StatusReadsTheCertificateThroughReadCertFile: derp.server.status reads derper's certificate through the
// descriptor read, and a certificate that read refuses is derp.not_ready, as an unreadable one always was.
func TestV1r15StatusReadsTheCertificateThroughReadCertFile(t *testing.T) {
	livePEM, livePin := ssCert(t, ssIP, ssLiveAfter)
	crt := server.CertDir + "/" + ssIP + ".crt"
	setup := func(t *testing.T) *dpHost {
		t.Helper()
		h := newDPHost(t)
		ssMaps(h)
		ssInstall(t, h, ssRow(server.ModeIP, ssIP))
		h.units.States[server.UnitName] = core.UnitState{LoadState: "loaded", ActiveState: "active", SubState: "running", MainPID: 700}
		h.files[crt] = livePEM
		return h
	}
	t.Run("a readable certificate", func(t *testing.T) {
		h := setup(t)
		got, _, err := ssRead(t, v1r15Split(t, h), h.env(""), `{}`)
		if err != nil {
			t.Fatal(err)
		}
		if got.CertName != livePin {
			t.Errorf("certificate %q, want %q", got.CertName, livePin)
		}
	})
	t.Run("a certificate the read refuses", func(t *testing.T) {
		h := setup(t)
		d := v1r15Split(t, h)
		d.d.readCertFile = func(string) ([]byte, error) { return nil, errors.New("platform: a symbolic link in the path") }
		_, _, err := ssRead(t, d, h.env(""), `{}`)
		if coded := errcode.From(err); err == nil || coded.Code != errcode.DERPNotReady {
			t.Errorf("err = %v, want derp.not_ready", err)
		}
	})
}

// TestV1r15CertRenewReadsCertificateAndKeyThroughReadCertFile: the renewal's plan, its wait for the new pair and
// its key check go through the descriptor read, and a key the read refuses refuses the renewal before anything moves.
func TestV1r15CertRenewReadsCertificateAndKeyThroughReadCertFile(t *testing.T) {
	ctx := context.Background()
	t.Run("plan and apply", func(t *testing.T) {
		h := crInstalled(t)
		d := v1r15Split(t, h.dpHost)
		if _, err := d.certRenew().Handler.Plan(ctx, h.env(""), json.RawMessage(`{}`)); err != nil {
			t.Fatal(err)
		}
		var err error
		crDrive(t, h.dpHost, h.derperRestarts(), func() {
			_, err = d.certRenew().Handler.Apply(ctx, h.env("c1"), json.RawMessage(`{}`))
		})
		if err != nil {
			t.Fatal(err)
		}
	})
	t.Run("a key the read refuses", func(t *testing.T) {
		h := crInstalled(t)
		d := v1r15Split(t, h.dpHost)
		d.d.readCertFile = func(name string) ([]byte, error) {
			if name == crKey {
				return nil, errors.New("platform: a symbolic link in the path")
			}
			return h.readFile(name)
		}
		_, err := d.certRenew().Handler.Plan(ctx, h.env(""), json.RawMessage(`{}`))
		if coded := errcode.From(err); err == nil || coded.Code != errcode.DERPNotReady || !core.IsDefinite(err) || !strings.Contains(coded.Detail, "key") {
			t.Fatalf("err = %v, want a definite derp.not_ready naming the key file", err)
		}
		if len(h.units.Calls) != 0 || len(crEffects(t, h.dpHost)) != 0 {
			t.Errorf("unit calls %q and %d effects, want none", h.units.Calls, len(crEffects(t, h.dpHost)))
		}
	})
}

// TestV1r15CertRenewNamesAFileTheReadRefuses: a certificate or key file derper has not written is not ready yet, but
// one the read refuses is named as refused, so a planted link is not mistaken for a wait.
func TestV1r15CertRenewNamesAFileTheReadRefuses(t *testing.T) {
	link := func(name string) error {
		return fmt.Errorf("platform: %s has a symbolic link in its path: %w", name, syscall.ELOOP)
	}
	for _, tc := range []struct {
		name      string
		refused   string // the file the read refuses, or "" to delete crCrt
		err       func(name string) error
		want, not string
	}{
		{"a certificate derper has not written", "", nil, "derper has not written a certificate for " + crIP + " yet", ""},
		{"a certificate behind a link", crCrt, link, "symbolic link", "has not written"},
		{"a certificate that is not a regular file", crCrt, func(name string) error {
			return fmt.Errorf("platform: %s is not a regular file", name)
		}, "not a regular file", "has not written"},
		{"a key behind a link", crKey, link, "symbolic link", "no readable key file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := crInstalled(t)
			d := v1r15Split(t, h.dpHost)
			if tc.refused == "" {
				delete(h.files, crCrt)
			}
			d.d.readCertFile = func(name string) ([]byte, error) {
				if name == tc.refused {
					return nil, tc.err(name)
				}
				return h.readFile(name)
			}
			_, err := d.certRenew().Handler.Plan(context.Background(), h.env(""), json.RawMessage(`{}`))
			coded := errcode.From(err)
			if err == nil || coded.Code != errcode.DERPNotReady || !core.IsDefinite(err) {
				t.Fatalf("err = %v, want a definite derp.not_ready", err)
			}
			if !strings.Contains(coded.Detail, tc.want) || (tc.not != "" && strings.Contains(coded.Detail, tc.not)) {
				t.Errorf("detail %q, want it to contain %q and not %q", coded.Detail, tc.want, tc.not)
			}
		})
	}
}

// TestV1r15InstallReadsTheCertificateThroughReadCertFile: the install task's wait for derper's certificate goes
// through the descriptor read.
func TestV1r15InstallReadsTheCertificateThroughReadCertFile(t *testing.T) {
	s := inNewSetup(t)
	s.active()
	der, _ := s.certFile(t)
	plain := s.d.d.readFile
	s.d.d.readFile = func(name string) ([]byte, error) {
		if strings.HasPrefix(name, server.CertDir+"/") {
			t.Errorf("readFile opened %s; the certificate directory is read through readCertFile", name)
		}
		return plain(name)
	}
	s.d.d.readCertFile = s.h.readFile
	var out json.RawMessage
	var err error
	inDrive(t, s.h, func() {
		out, err = s.d.serverInstall().Handler.Task(context.Background(), s.env, inInput("ip", inIP, nil))
	})
	if err != nil {
		t.Fatal(err)
	}
	var got ServerInstalled
	if err := json.Unmarshal(out, &got); err != nil || got.CertName != server.Pin(der) {
		t.Errorf("output %s (error %v), want the pin of the certificate that was read", out, err)
	}
}
