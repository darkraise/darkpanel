package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/backup"
)

func TestW10BrBackupRequestBody(t *testing.T) {
	cases := []struct {
		name   string
		pass   []byte
		params *backup.Params
	}{
		{"plain", []byte("correct horse battery"), nil},
		{"quotes and backslashes", []byte(`a"b\c`), nil},
		{"control characters", []byte("tab\there\nnew\r\x00\x1f\x7f"), nil},
		{"markup characters and non-ASCII text", []byte("<&>ü漢字😀 "), nil},
		{"argon2 parameters", []byte("x"), &backup.Params{T: 1, M: 65536, P: 2}},
		{"the longest passphrase", bytes.Repeat([]byte("p"), maxPassphrase), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pass := bytes.Clone(tc.pass)
			body, err := encodeBackupRequest(pass, tc.params)
			if err != nil {
				t.Fatalf("encodeBackupRequest: %v", err)
			}
			dec := json.NewDecoder(bytes.NewReader(body))
			dec.DisallowUnknownFields()
			var got backup.BackupRequest
			if err := dec.Decode(&got); err != nil {
				t.Fatalf("the body is not a backup request: %v", err)
			}
			if dec.InputOffset() != int64(len(bytes.TrimSpace(body))) {
				t.Errorf("the body holds more than one JSON value")
			}
			if got.Passphrase != string(tc.pass) {
				t.Errorf("the decoded passphrase has %d bytes and differs from the one given (%d bytes)", len(got.Passphrase), len(tc.pass))
			}
			if (got.Argon2 == nil) != (tc.params == nil) || got.Argon2 != nil && *got.Argon2 != *tc.params {
				t.Errorf("argon2 = %+v, want %+v", got.Argon2, tc.params)
			}
		})
	}

	t.Run("a passphrase that is not UTF-8 is refused", func(t *testing.T) {
		body, err := encodeBackupRequest([]byte{'a', 0xff, 0xfe}, nil)
		if err == nil || body != nil || !strings.Contains(err.Error(), "UTF-8") {
			t.Errorf("encodeBackupRequest = %q, %v; want no body and an error naming UTF-8: JSON would replace the bytes and the restore could never type the same passphrase", body, err)
		}
	})
}

func TestW10BrBackupRefusesAnAnswerThatIsNotABackupStream(t *testing.T) {
	body := bytes.Repeat([]byte("x"), 5000)
	for _, tc := range []struct {
		name, contentType string
		ok                bool
	}{
		{"an HTML page", "text/html; charset=utf-8", false},
		{"JSON", "application/json", false},
		{"no content type", "", false},
		{"a look-alike type", "application/octet-streamer", false},
		{"octet-stream", "application/octet-stream", true},
		{"octet-stream with a parameter", "application/octet-stream; charset=binary", true},
		{"octet-stream in capitals", "Application/Octet-Stream", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sock, _ := ckBackupServer(t, func(w http.ResponseWriter, _ *http.Request) {
				if tc.contentType == "" {
					w.Header()["Content-Type"] = nil
				} else {
					w.Header().Set("Content-Type", tc.contentType)
				}
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(body)
			})
			dir := t.TempDir()
			out := filepath.Join(dir, "b.dpbk")
			stdout, stderr, code := (ckEnv{terminal: true, secrets: []string{ckPass, ckPass}}).run(t, "backup", "--out", out, "--socket", sock)
			if tc.ok {
				if got, err := os.ReadFile(out); code != 0 || err != nil || !bytes.Equal(got, body) { //nolint:gosec // G304: a path under the test's own temporary directory
					t.Errorf("exit %d, file of %d bytes (error %v), stderr %q; want the stream saved", code, len(got), err, stderr)
				}
				return
			}
			if code != ExitError || stdout != "" || !strings.Contains(stderr, "not application/octet-stream") {
				t.Errorf("exit %d, stdout %q, stderr %q; want exit %d, no stdout and an error saying the answer is not application/octet-stream", code, stdout, stderr, ExitError)
			}
			if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
				t.Errorf("the directory holds %d entries (error %v), want none: nothing may be saved from such an answer", len(entries), err)
			}
		})
	}
}

func TestW10BrReportRestore(t *testing.T) {
	done := backup.RestoreResult{Tx: "01JTESTTX", BackupCreatedAt: time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)}
	cleanup := errors.New("backup: restore 01JTESTTX finished and its database is installed, but removing earlier restores' set-aside files failed")
	for _, tc := range []struct {
		name    string
		res     backup.RestoreResult
		err     error
		wantOut string
	}{
		{"a finished restore", done, nil, restoreSummary(done)},
		{"a finished restore whose cleanup failed", done, cleanup, restoreSummary(done)},
		{"a restore that did not finish", backup.RestoreResult{}, errors.New("backup: wrong passphrase or damaged backup file"), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			err := reportRestore(&out, tc.res, tc.err)
			if !errors.Is(err, tc.err) {
				t.Errorf("reportRestore returned %v, want %v: a failure is never hidden", err, tc.err)
			}
			if out.String() != tc.wantOut {
				t.Errorf("reportRestore printed %q, want %q", out.String(), tc.wantOut)
			}
		})
	}
}
