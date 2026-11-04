package cli

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/spf13/cobra"

	"github.com/darkraise/darkpanel/internal/controller/backup"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/paths"
)

const (
	ckPass      = "correct horse battery"
	ckPrompts   = "Passphrase: \nRepeat passphrase: \n"
	ckLongError = "darkpanel: the passphrase is longer than 1024 bytes\n"
)

// ckUID is the uid a passphrase file of these tests belongs to: the test
// process's own, where the command requires uid 0.
func ckUID() uint32 {
	return uint32(os.Getuid()) //nolint:gosec // G115: a uid fits in uint32
}

// ckServe serves h on a Unix socket in a short directory and returns the
// socket path.
func ckServe(t *testing.T, h http.Handler) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "ck")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "c.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close(); _ = os.RemoveAll(dir) })
	return path
}

// ckSeen is the backup requests a fake controller received.
type ckSeen struct {
	mu   sync.Mutex
	reqs []backup.BackupRequest
}

func (s *ckSeen) all() []backup.BackupRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.reqs)
}

// ckBackupServer serves POST /api/v1/controller/backup on a socket: it
// records the decoded request, refusing a body with an unknown field, and
// lets reply answer.
func ckBackupServer(t *testing.T, reply http.HandlerFunc) (string, *ckSeen) {
	t.Helper()
	seen := &ckSeen{}
	sock := ckServe(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/controller/backup" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type %q, want application/json", got)
		}
		dec := json.NewDecoder(io.LimitReader(r.Body, 64<<10))
		dec.DisallowUnknownFields()
		var req backup.BackupRequest
		if err := dec.Decode(&req); err != nil {
			t.Errorf("the request body: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		seen.mu.Lock()
		seen.reqs = append(seen.reqs, req)
		seen.mu.Unlock()
		reply(w, r)
	}))
	return sock, seen
}

// ckOctets answers 200 with body in 4 KiB writes, flushing each.
func ckOctets(body []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		for off := 0; off < len(body); off += 4096 {
			_, _ = w.Write(body[off:min(off+4096, len(body))])
			_ = http.NewResponseController(w).Flush()
		}
	}
}

// ckProblem answers with RFC 9457 problem details.
func ckProblem(status int, code, detail string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(status)
		_, _ = fmt.Fprintf(w, `{"type":"about:blank","title":"refused","status":%d,"detail":%q,"code":%q}`, status, detail, code)
	}
}

// ckEnv runs the backup and restore commands under a controller group, with
// the terminal, the secret prompts, the passphrase file owner and the
// restore host of the test.
type ckEnv struct {
	terminal bool
	secrets  []string
	owner    uint32 // the uid a passphrase file must belong to
	newHost  func(*cobra.Command) (backup.Host, func() error, error)
}

func (e ckEnv) app(out, errb io.Writer) *app {
	secrets := slices.Clone(e.secrets)
	opts := withDefaults(Options{
		Stdin: strings.NewReader(""), Stdout: out, Stderr: errb,
		IsTerminal: func() bool { return e.terminal },
		ReadSecret: func() (string, error) {
			if len(secrets) == 0 {
				return "", io.EOF
			}
			s := secrets[0]
			secrets = secrets[1:]
			return s, nil
		},
	})
	return &app{opts: opts, term: &terminal{in: bufio.NewReader(opts.Stdin), out: opts.Stderr, isTerm: opts.IsTerminal(), readSecret: opts.ReadSecret}}
}

func (e ckEnv) run(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	var out, errb bytes.Buffer
	flow := newBackupFlow(e.app(&out, &errb))
	flow.fileOwner = e.owner
	flow.newHost = e.newHost
	if flow.newHost == nil {
		flow.newHost = func(*cobra.Command) (backup.Host, func() error, error) {
			t.Error("the command built the restore host")
			return backup.Host{}, nil, errors.New("no host in this test")
		}
	}
	root := &cobra.Command{Use: "darkpanel", SilenceUsage: true, SilenceErrors: true}
	root.SetIn(strings.NewReader(""))
	root.SetOut(&out)
	root.SetErr(&errb)
	ctl := &cobra.Command{Use: "controller"}
	ctl.AddCommand(flow.backupCommand(), flow.restoreCommand())
	root.AddCommand(ctl)
	markRuns(root)
	root.SetArgs(append([]string{"controller"}, args...))
	err := root.Execute()
	PrintError(&errb, err)
	return out.String(), errb.String(), ExitCode(err)
}

// ckPassFile writes a passphrase file with the given content and mode.
func ckPassFile(t *testing.T, content string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "pass")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
	return p
}

// ckBackupFile writes a backup under pass whose members authenticate but are
// not a database and a key, so a restore passes phase 1 and stops at phase 2.
func ckBackupFile(t *testing.T, dir, pass string) string {
	t.Helper()
	path := filepath.Join(dir, "in.dpbk")
	f, err := os.Create(path) //nolint:gosec // G304: a path under the test's own temporary directory
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	db, key := []byte("this is not an SQLite database"), []byte("this is not a key file")
	err = backup.Write(f, rand.Reader, []byte(pass), backup.TestParams, []backup.Member{
		{Name: backup.MemberDB, Size: int64(len(db)), Body: bytes.NewReader(db)},
		{Name: backup.MemberKey, Size: int64(len(key)), Body: bytes.NewReader(key)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return path
}

// ckUnits is a core.Units whose services are all stopped; any other method
// panics, because a restore must not call it.
type ckUnits struct{ core.Units }

func (ckUnits) State(context.Context, string) (core.UnitState, error) {
	return core.UnitState{LoadState: "loaded", ActiveState: "inactive"}, nil
}

// ckHost is a restore host in a temporary directory: a controller that is
// installed, its services stopped, and euid as the effective user.
func ckHost(t *testing.T, euid int) backup.Host {
	t.Helper()
	root := t.TempDir()
	p := paths.Under(root)
	unit := filepath.Join(root, "darkpanel-controller.service")
	for dir, file := range map[string]string{p.ControllerDir: "", filepath.Dir(p.ControllerKey): p.ControllerKey, root: unit} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if file != "" {
			if err := os.WriteFile(file, []byte("x\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	return backup.Host{
		Paths: p, UnitFile: unit, LockFile: filepath.Join(root, "restore.lock"),
		Units: ckUnits{}, Euid: func() int { return euid },
		Owner: func() (int, int, error) { return os.Getuid(), os.Getgid(), nil },
		Ops:   backup.OSOps(), Clock: coretest.NewFakeClock(time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)),
		NewTx: func() string { return ulid.Make().String() },
		Log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

// ckHostFor returns a newHost hook that hands out h and records that its
// close function ran.
func ckHostFor(h backup.Host) (hook func(*cobra.Command) (backup.Host, func() error, error), closed *bool) {
	closed = new(bool)
	return func(*cobra.Command) (backup.Host, func() error, error) {
		return h, func() error { *closed = true; return nil }, nil
	}, closed
}

func TestCkBackupWritesTheFile(t *testing.T) {
	body := bytes.Repeat([]byte("darkpanel backup frame "), 20000)
	sock, seen := ckBackupServer(t, ckOctets(body))
	out := filepath.Join(t.TempDir(), "controller.dpbk")
	stdout, stderr, code := ckEnv{terminal: true, secrets: []string{ckPass, ckPass}}.run(t, "backup", "--out", out, "--socket", sock)
	if code != 0 {
		t.Fatalf("backup: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	want := fmt.Sprintf("Backup written to %s (%d bytes); keep the passphrase, the file cannot be opened without it\n", out, len(body))
	if stdout != want {
		t.Errorf("stdout %q, want %q", stdout, want)
	}
	if stderr != ckPrompts {
		t.Errorf("stderr %q, want only the two prompts %q", stderr, ckPrompts)
	}
	if got, err := os.ReadFile(out); err != nil || !bytes.Equal(got, body) { //nolint:gosec // G304: a path under the test's own temporary directory
		t.Errorf("the file holds %d bytes (error %v), want the %d bytes the controller streamed", len(got), err, len(body))
	}
	info, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("the file has mode %v, want 0600", info.Mode().Perm())
	}
	if entries, err := os.ReadDir(filepath.Dir(out)); err != nil || len(entries) != 1 {
		t.Errorf("the directory holds %d entries (error %v), want only the backup", len(entries), err)
	}
	reqs := seen.all()
	if len(reqs) != 1 || reqs[0].Passphrase != ckPass || reqs[0].Argon2 != nil {
		t.Errorf("the controller received %+v, want the passphrase and no argon2 member", reqs)
	}
	for _, s := range []string{stdout, stderr} {
		if strings.Contains(s, ckPass) {
			t.Errorf("the output shows the passphrase: %q", s)
		}
	}
}

func TestCkBackupArgon2Parameters(t *testing.T) {
	sock, seen := ckBackupServer(t, ckOctets([]byte("x")))
	for i, tc := range []struct {
		args []string
		want *backup.Params
	}{
		{nil, nil},
		{[]string{"--argon2-t", "1", "--argon2-m", "65536", "--argon2-p", "1"}, &backup.Params{T: 1, M: 65536, P: 1}},
		{[]string{"--argon2-p", "2"}, &backup.Params{T: 3, M: 65536, P: 2}},
		{[]string{"--argon2-m", "131072"}, &backup.Params{T: 3, M: 131072, P: 4}},
	} {
		out := filepath.Join(t.TempDir(), "b.dpbk")
		args := append([]string{"backup", "--out", out, "--socket", sock}, tc.args...)
		if _, stderr, code := (ckEnv{terminal: true, secrets: []string{ckPass, ckPass}}).run(t, args...); code != 0 {
			t.Fatalf("%v: exit %d, stderr %q", tc.args, code, stderr)
		}
		reqs := seen.all()
		if len(reqs) != i+1 {
			t.Fatalf("%v: the controller has received %d requests, want %d", tc.args, len(reqs), i+1)
		}
		got := reqs[i].Argon2
		if tc.want == nil && got != nil || tc.want != nil && (got == nil || *got != *tc.want) {
			t.Errorf("%v: the request carried argon2 %+v, want %+v", tc.args, got, tc.want)
		}
	}
}

func TestCkBackupArgon2Bounds(t *testing.T) {
	sock, seen := ckBackupServer(t, ckOctets([]byte("x")))
	for _, tc := range []struct{ flag, value, msg string }{
		{"--argon2-t", "0", "--argon2-t must be 1 to 10, not 0"},
		{"--argon2-t", "11", "--argon2-t must be 1 to 10, not 11"},
		{"--argon2-m", "65535", "--argon2-m must be 65536 to 1048576 KiB, not 65535"},
		{"--argon2-m", "1048577", "--argon2-m must be 65536 to 1048576 KiB, not 1048577"},
		{"--argon2-p", "0", "--argon2-p must be 1 to 16, not 0"},
		{"--argon2-p", "17", "--argon2-p must be 1 to 16, not 17"},
	} {
		out := filepath.Join(t.TempDir(), "b.dpbk")
		stdout, stderr, code := (ckEnv{terminal: true}).run(t, "backup", "--out", out, "--socket", sock, tc.flag, tc.value)
		if want := "darkpanel: " + tc.msg + "\n"; code != ExitUsage || stdout != "" || stderr != want {
			t.Errorf("%s %s: exit %d, stdout %q, stderr %q; want exit %d and stderr %q before any prompt", tc.flag, tc.value, code, stdout, stderr, ExitUsage, want)
		}
	}
	if n := len(seen.all()); n != 0 {
		t.Errorf("the controller received %d requests, want none", n)
	}
}

func TestCkBackupRefusesBeforeAsking(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "old.dpbk")
	if err := os.WriteFile(existing, []byte("old backup"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.dpbk")
	if err := os.Symlink(filepath.Join(dir, "nowhere"), link); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "no")
	sock, seen := ckBackupServer(t, ckOctets([]byte("x")))
	for _, tc := range []struct {
		name   string
		args   []string
		code   int
		stderr string
	}{
		{"no --out", nil, ExitUsage, "darkpanel: --out is required\n"},
		{"an existing file", []string{"--out", existing}, ExitError,
			"darkpanel: refusing to overwrite " + existing + "; remove it or choose another --out\n"},
		{"a dangling link", []string{"--out", link}, ExitError,
			"darkpanel: refusing to overwrite " + link + "; remove it or choose another --out\n"},
		{"a missing directory", []string{"--out", filepath.Join(missing, "b.dpbk")}, ExitError,
			"darkpanel: --out directory: stat " + missing + ": no such file or directory\n"},
	} {
		stdout, stderr, code := (ckEnv{terminal: true}).run(t, append([]string{"backup", "--socket", sock}, tc.args...)...)
		if code != tc.code || stdout != "" || stderr != tc.stderr {
			t.Errorf("%s: exit %d, stdout %q, stderr %q; want exit %d and stderr %q, with no prompt", tc.name, code, stdout, stderr, tc.code, tc.stderr)
		}
	}
	if got, err := os.ReadFile(existing); err != nil || string(got) != "old backup" { //nolint:gosec // G304: a path under the test's own temporary directory
		t.Errorf("the existing file now holds %q (error %v), want it untouched", got, err)
	}
	if n := len(seen.all()); n != 0 {
		t.Errorf("the controller received %d requests, want none", n)
	}
}

func TestCkBackupFailuresLeaveNothing(t *testing.T) {
	for _, tc := range []struct {
		name, stderr, contains string
		reply                  http.HandlerFunc
	}{
		{name: "a refusal", reply: ckProblem(http.StatusConflict, "core.change_busy", "another backup is running"),
			stderr: "darkpanel: another backup is running (core.change_busy)\n"},
		{name: "an answer that is not problem details", contains: "unexpected answer 500 Internal Server Error",
			reply: func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "boom", http.StatusInternalServerError) }},
		{name: "a stream cut after the first bytes", contains: "receive the backup",
			reply: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/octet-stream")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(bytes.Repeat([]byte("x"), 5000))
				_ = http.NewResponseController(w).Flush()
				panic(http.ErrAbortHandler)
			}},
	} {
		sock, _ := ckBackupServer(t, tc.reply)
		dir := t.TempDir()
		out := filepath.Join(dir, "b.dpbk")
		stdout, stderr, code := (ckEnv{terminal: true, secrets: []string{ckPass, ckPass}}).run(t, "backup", "--out", out, "--socket", sock)
		if code != ExitError || stdout != "" {
			t.Errorf("%s: exit %d, stdout %q, want exit %d and no stdout", tc.name, code, stdout, ExitError)
		}
		if tc.stderr != "" && stderr != ckPrompts+tc.stderr || !strings.Contains(stderr, tc.contains) {
			t.Errorf("%s: stderr %q, want %q%s", tc.name, stderr, ckPrompts+tc.stderr, tc.contains)
		}
		if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
			t.Errorf("%s: the directory holds %d entries (error %v), want none: no backup and no temporary file", tc.name, len(entries), err)
		}
	}
}

// ckRoundTripper is an http.RoundTripper that is not an *http.Transport.
type ckRoundTripper func(*http.Request) (*http.Response, error)

func (f ckRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCkBackupClientHasNoTotalTimeout(t *testing.T) {
	sock := ckServe(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") }))
	c := newSocketClient(sock)
	hc, err := backupHTTPClient(c)
	if err != nil {
		t.Fatal(err)
	}
	if hc.Timeout != 0 {
		t.Errorf("the backup client has a total timeout of %v, want none", hc.Timeout)
	}
	tr, ok := hc.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("the backup client's transport is %T, want an *http.Transport", hc.Transport)
	}
	if http.RoundTripper(tr) == c.http.Transport || tr.ResponseHeaderTimeout != 0 {
		t.Errorf("the backup client shares the socket client's transport (%t) or has a header timeout of %v, want its own clone without one",
			http.RoundTripper(tr) == c.http.Transport, tr.ResponseHeaderTimeout)
	}
	if c.http.Timeout != requestTimeout {
		t.Errorf("the socket client's timeout is now %v, want %v unchanged", c.http.Timeout, requestTimeout)
	}
	resp, err := hc.Get("http://controller/x")
	if err != nil {
		t.Fatalf("a request through the backup client: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status %d through the backup client, want it to reach the socket", resp.StatusCode)
	}
	other := &client{base: "http://x", http: &http.Client{Transport: ckRoundTripper(nil)}}
	if _, err := backupHTTPClient(other); err == nil {
		t.Error("backupHTTPClient accepted a transport that is not an *http.Transport")
	}
}

func TestCkPassphraseSources(t *testing.T) {
	good := ckPassFile(t, "from file\n", 0o600)
	loose := ckPassFile(t, "from file\n", 0o644)
	long := strings.Repeat("a", 1025)
	longFile := ckPassFile(t, long, 0o600)
	emptyFile := ckPassFile(t, "\n", 0o600)
	missing := filepath.Join(t.TempDir(), "nowhere")
	tests := []struct {
		name     string
		env      ckEnv
		args     []string
		code     int
		stderr   string // exact; "" compares contains instead
		contains string
		want     string // the passphrase the controller receives; "" when no request is made
	}{
		{name: "typed twice", env: ckEnv{terminal: true, secrets: []string{ckPass, ckPass}}, stderr: ckPrompts, want: ckPass},
		{name: "typed twice differently", env: ckEnv{terminal: true, secrets: []string{"one", "two"}}, code: ExitUsage,
			stderr: ckPrompts + "darkpanel: the passphrases do not match\n"},
		{name: "typed empty", env: ckEnv{terminal: true, secrets: []string{"", ""}}, code: ExitUsage,
			stderr: ckPrompts + "darkpanel: the passphrase is empty\n"},
		{name: "typed too long", env: ckEnv{terminal: true, secrets: []string{long, long}}, code: ExitUsage, stderr: ckPrompts + ckLongError},
		{name: "no terminal and no file", env: ckEnv{}, code: ExitUsage,
			stderr: "darkpanel: no terminal to read the passphrase from; pass --passphrase-file\n"},
		{name: "the terminal fails", env: ckEnv{terminal: true}, code: ExitError, stderr: "Passphrase: \ndarkpanel: read the passphrase: EOF\n"},
		{name: "a file", env: ckEnv{owner: ckUID()}, args: []string{"--passphrase-file", good}, want: "from file"},
		{name: "a file beats the terminal", env: ckEnv{owner: ckUID(), terminal: true, secrets: []string{"typed", "typed"}},
			args: []string{"--passphrase-file", good}, want: "from file"},
		{name: "a file with a loose mode", env: ckEnv{owner: ckUID()}, args: []string{"--passphrase-file", loose}, code: ExitError,
			contains: "permission bits outside 0600"},
		{name: "a missing file", env: ckEnv{owner: ckUID()}, args: []string{"--passphrase-file", missing}, code: ExitError, contains: "no such file"},
		{name: "a file of another owner", env: ckEnv{owner: ckUID() + 1}, args: []string{"--passphrase-file", good}, code: ExitError,
			contains: "must be owned by uid"},
		{name: "a file too long", env: ckEnv{owner: ckUID()}, args: []string{"--passphrase-file", longFile}, code: ExitUsage, stderr: ckLongError},
		{name: "an empty file", env: ckEnv{owner: ckUID()}, args: []string{"--passphrase-file", emptyFile}, code: ExitUsage,
			stderr: "darkpanel: the passphrase is empty\n"},
	}
	for _, tc := range tests {
		sock, seen := ckBackupServer(t, ckOctets([]byte("x")))
		out := filepath.Join(t.TempDir(), "b.dpbk")
		args := append([]string{"backup", "--out", out, "--socket", sock}, tc.args...)
		stdout, stderr, code := tc.env.run(t, args...)
		if code != tc.code {
			t.Errorf("%s: exit %d, stdout %q, stderr %q; want exit %d", tc.name, code, stdout, stderr, tc.code)
		}
		switch {
		case tc.stderr != "" && stderr != tc.stderr, tc.stderr == "" && !strings.Contains(stderr, tc.contains),
			tc.stderr == "" && tc.contains == "" && stderr != "":
			t.Errorf("%s: stderr %q, want %q%s", tc.name, stderr, tc.stderr, tc.contains)
		}
		reqs := seen.all()
		switch {
		case tc.want == "" && len(reqs) != 0:
			t.Errorf("%s: the controller received %d requests, want none", tc.name, len(reqs))
		case tc.want != "" && (len(reqs) != 1 || reqs[0].Passphrase != tc.want):
			t.Errorf("%s: the controller received %+v, want one request with the passphrase %q", tc.name, reqs, tc.want)
		}
		for _, s := range []string{stdout, stderr} {
			if tc.want != "" && strings.Contains(s, tc.want) {
				t.Errorf("%s: the output shows the passphrase: %q", tc.name, s)
			}
		}
	}
}

func TestCkReadPassphraseFile(t *testing.T) {
	uid := ckUID()
	dir := t.TempDir()
	write := func(name, content string, mode os.FileMode) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
		return p
	}
	target := write("target", "secret\n", 0o600)
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(dir, "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, path string
		owner      uint32
		want, err  string
	}{
		{"plain", write("a", "secret", 0o600), uid, "secret", ""},
		{"one newline is stripped", write("b", "secret\n", 0o600), uid, "secret", ""},
		{"only one", write("c", "secret\n\n", 0o600), uid, "secret\n", ""},
		{"a carriage return stays", write("d", "secret\r\n", 0o600), uid, "secret\r", ""},
		{"mode 0400", write("e", "secret\n", 0o400), uid, "secret", ""},
		{"empty", write("f", "", 0o600), uid, "", ""},
		{"readable by the group", write("g", "secret\n", 0o640), uid, "", "permission bits outside 0600"},
		{"writable by others", write("h", "secret\n", 0o602), uid, "", "permission bits outside 0600"},
		{"another owner", write("i", "secret\n", 0o600), uid + 1, "", "must be owned by uid"},
		{"a directory", sub, uid, "", "not a regular file"},
		{"a named pipe", fifo, uid, "", "not a regular file"},
		{"a symbolic link", link, uid, "", "symbolic link"},
		{"missing", filepath.Join(dir, "nowhere"), uid, "", "no such file"},
		{"1024 bytes and a newline", write("j", strings.Repeat("a", 1024)+"\n", 0o600), uid, strings.Repeat("a", 1024), ""},
		{"past 1025 bytes", write("k", strings.Repeat("a", 1026), 0o600), uid, "", "longer than 1024 bytes"},
	}
	for _, tc := range tests {
		got, err := readPassphraseFile(tc.path, tc.owner)
		switch {
		case tc.err == "" && err != nil:
			t.Errorf("%s: %v, want it read", tc.name, err)
		case tc.err != "" && (err == nil || !strings.Contains(err.Error(), tc.err)):
			t.Errorf("%s: error %v, want one containing %q", tc.name, err, tc.err)
		case tc.err == "" && string(got) != tc.want:
			t.Errorf("%s: read %q, want %q", tc.name, got, tc.want)
		}
		if err != nil && strings.Contains(err.Error(), "secret") {
			t.Errorf("%s: the error shows the file's content: %v", tc.name, err)
		}
	}
}

func TestCkRestoreFlags(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nowhere.dpbk")
	for _, tc := range []struct {
		name     string
		args     []string
		code     int
		stderr   string
		contains string
	}{
		{"no flag", []string{"restore"}, ExitUsage,
			"darkpanel: give --in <file> to restore, or --recover to finish an interrupted restore\n", ""},
		{"--recover with --in", []string{"restore", "--recover", "--in", missing}, ExitUsage, "darkpanel: --recover takes no other flag\n", ""},
		{"--recover with a passphrase file", []string{"restore", "--recover", "--passphrase-file", missing}, ExitUsage,
			"darkpanel: --recover takes no other flag\n", ""},
		{"an input that is missing", []string{"restore", "--in", missing}, ExitError, "", missing + ": no such file or directory"},
	} {
		stdout, stderr, code := (ckEnv{terminal: true}).run(t, tc.args...)
		if code != tc.code || stdout != "" || tc.stderr != "" && stderr != tc.stderr || !strings.Contains(stderr, tc.contains) {
			t.Errorf("%s: exit %d, stdout %q, stderr %q; want exit %d and stderr %q%s, with no host built and no prompt",
				tc.name, code, stdout, stderr, tc.code, tc.stderr, tc.contains)
		}
	}
}

func TestCkRestoreUsesTheHostAndThePassphrase(t *testing.T) {
	const pass = "restore passphrase"
	in := ckBackupFile(t, t.TempDir(), pass)
	right := ckPassFile(t, pass+"\n", 0o600)
	wrong := ckPassFile(t, "not "+pass+"\n", 0o600)
	const authFailure = "wrong passphrase or damaged backup file"
	for _, tc := range []struct {
		name          string
		euid          int
		env           ckEnv
		file          string
		contains      string
		notContaining string
	}{
		{name: "not root", euid: 1000, env: ckEnv{owner: ckUID()}, file: right, contains: "backup: a restore must run as root"},
		{name: "a wrong passphrase from a file", env: ckEnv{owner: ckUID()}, file: wrong, contains: authFailure},
		{name: "the right passphrase from a file", env: ckEnv{owner: ckUID()}, file: right, notContaining: authFailure},
		{name: "a wrong passphrase typed twice", env: ckEnv{terminal: true, secrets: []string{"bad", "bad"}}, contains: authFailure},
		{name: "the right passphrase typed twice", env: ckEnv{terminal: true, secrets: []string{pass, pass}}, notContaining: authFailure},
	} {
		hook, closed := ckHostFor(ckHost(t, tc.euid))
		tc.env.newHost = hook
		args := []string{"restore", "--in", in}
		if tc.file != "" {
			args = append(args, "--passphrase-file", tc.file)
		}
		stdout, stderr, code := tc.env.run(t, args...)
		// The members of the test archive are not a database and a key, so a
		// restore that gets past the passphrase still fails, in phase 2.
		if code != ExitError || stdout != "" || !strings.HasPrefix(stderr, "darkpanel: ") && !strings.Contains(stderr, "\ndarkpanel: ") {
			t.Errorf("%s: exit %d, stdout %q, stderr %q; want exit %d and an error", tc.name, code, stdout, stderr, ExitError)
		}
		if tc.contains != "" && !strings.Contains(stderr, tc.contains) {
			t.Errorf("%s: stderr %q, want it to contain %q", tc.name, stderr, tc.contains)
		}
		if tc.notContaining != "" && strings.Contains(stderr, tc.notContaining) {
			t.Errorf("%s: stderr %q contains %q, so the passphrase did not reach the restore", tc.name, stderr, tc.notContaining)
		}
		if !*closed {
			t.Errorf("%s: the host's close function did not run", tc.name)
		}
		if strings.Contains(stderr, pass) || strings.Contains(stdout, pass) {
			t.Errorf("%s: the output shows the passphrase", tc.name)
		}
	}
}

func TestCkRecover(t *testing.T) {
	clean := ckHost(t, 0)
	hook, closed := ckHostFor(clean)
	stdout, stderr, code := (ckEnv{newHost: hook}).run(t, "restore", "--recover")
	if code != 0 || stdout != "" || stderr != "" || !*closed {
		t.Errorf("--recover with no journal: exit %d, stdout %q, stderr %q, closed %t; want a silent success", code, stdout, stderr, *closed)
	}

	broken := ckHost(t, 0)
	journal := filepath.Join(broken.Paths.ControllerDir, "restore-"+ulid.Make().String()+".journal")
	if err := os.WriteFile(journal, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	hook, closed = ckHostFor(broken)
	stdout, stderr, code = (ckEnv{newHost: hook}).run(t, "restore", "--recover")
	if code != ExitError || stdout != "" || !strings.HasPrefix(stderr, "darkpanel: ") || !*closed {
		t.Errorf("--recover over an unreadable journal: exit %d, stdout %q, stderr %q, closed %t; want exit %d with an error, so the unit fails",
			code, stdout, stderr, *closed, ExitError)
	}
}

func TestCkRestoreSummary(t *testing.T) {
	want := "Restored the backup taken 2026-11-03T10:00:00Z (restore 01JTESTTX).\n" +
		"The controller database starts in restore quarantine. Start darkpanel-controller.service and darkpanel-agent.service, then run:\n" +
		"  darkpanel controller quarantine list\n"
	utc := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	if got := restoreSummary(backup.RestoreResult{Tx: "01JTESTTX", BackupCreatedAt: utc}); got != want {
		t.Errorf("restoreSummary = %q, want %q", got, want)
	}
	zoned := utc.In(time.FixedZone("plus2", 2*3600))
	if got := restoreSummary(backup.RestoreResult{Tx: "01JTESTTX", BackupCreatedAt: zoned}); got != want {
		t.Errorf("restoreSummary of a time in another zone = %q, want it in UTC: %q", got, want)
	}
}

func TestCkBackupCommandsMount(t *testing.T) {
	var out, errb bytes.Buffer
	cmds := backupCommands((ckEnv{}).app(&out, &errb))
	var names []string
	for _, c := range cmds {
		names = append(names, c.Name())
	}
	if want := []string{"backup", "restore", "quarantine"}; !slices.Equal(names, want) {
		t.Fatalf("backupCommands returned %v, want %v in that order", names, want)
	}
	root, err := NewRoot(Options{Registry: testRegistry(t)})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name         string
		flags, never []string
	}{
		{"backup", []string{"out", "passphrase-file", "argon2-t", "argon2-m", "argon2-p", "socket", "dev"}, []string{"url", "recover", "in"}},
		{"restore", []string{"in", "passphrase-file", "recover"}, []string{"socket", "dev", "url", "out"}},
		{"quarantine", nil, nil},
	} {
		cmd, rest, err := root.Find([]string{"controller", tc.name})
		if err != nil || len(rest) != 0 || cmd.CommandPath() != "darkpanel controller "+tc.name {
			t.Errorf("controller %s: not found under controller (error %v, %d arguments left over)", tc.name, err, len(rest))
			continue
		}
		for _, f := range tc.flags {
			if cmd.Flags().Lookup(f) == nil {
				t.Errorf("controller %s has no --%s", tc.name, f)
			}
		}
		for _, f := range tc.never {
			if cmd.Flags().Lookup(f) != nil {
				t.Errorf("controller %s has a --%s", tc.name, f)
			}
		}
	}
}
