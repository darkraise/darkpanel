//go:build harness

package harness

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestW10HxProbeURLUnit(t *testing.T) {
	tests := []struct{ host, want string }{
		{"203.0.113.10", "https://203.0.113.10/derp/probe"},
		{"derp.example.com", "https://derp.example.com/derp/probe"},
		{"2001:db8::10", "https://[2001:db8::10]/derp/probe"},
	}
	for _, tc := range tests {
		got := w10hxProbeURL(tc.host)
		if got != tc.want {
			t.Errorf("w10hxProbeURL(%q) = %s, want %s", tc.host, got, tc.want)
		}
		u, err := url.Parse(got)
		if err != nil {
			t.Errorf("%s does not parse: %v", got, err)
			continue
		}
		if u.Hostname() != tc.host {
			t.Errorf("%s parses to host %q, want %q", got, u.Hostname(), tc.host)
		}
	}
}

func TestW10HxUploadRegexpUnit(t *testing.T) {
	step := "        with:\n          name: harness\n\n\n          path: bin/harness\n"
	loc := m5dwUploadRE.FindStringIndex(step)
	if loc == nil {
		t.Fatalf("m5dwUploadRE found no upload line in %q", step)
	}
	if want := strings.Index(step, "          path:"); loc[0] != want {
		t.Errorf("m5dwUploadRE starts at %d, want %d, the start of the path line: its whitespace class crossed a line break", loc[0], want)
	}
	for _, other := range []string{"          path: bin/harness/derper-*\n", "          path: bin/harness-extra\n"} {
		if m5dwUploadRE.MatchString(other) {
			t.Errorf("m5dwUploadRE matches %q, which is not the upload line", other)
		}
	}
}

func TestW10HxGoneUnit(t *testing.T) {
	app := m6nResponse{Status: http.StatusNotFound, Body: []byte(m6nAppBody + "\n")}
	get := func(err error) error {
		return fmt.Errorf("harness: %w", &url.Error{Op: "Get", URL: "https://" + m6nAppHost + "/", Err: err})
	}
	tests := []struct {
		name string
		resp m6nResponse
		err  error
		want string
	}{
		{"the target still answers", app, nil, "still reaches the removed target"},
		{"NPM answers with a page of its own", m6nResponse{Status: http.StatusOK, Body: []byte("Congratulations!")}, nil, ""},
		{"NPM answers with its own 404", m6nResponse{Status: http.StatusNotFound, Body: []byte("<html>not found</html>")}, nil, ""},
		{"the request failed", m6nResponse{}, errors.New("connection refused"), "connection refused"},
		{"NPM refuses the handshake for the removed name", m6nResponse{}, get(&net.OpError{Op: "remote error", Err: errors.New("tls: unrecognized name")}), ""},
		{"the request timed out", m6nResponse{}, get(context.DeadlineExceeded), "deadline exceeded"},
		{"the connection was refused", m6nResponse{}, get(&net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}), "connection refused"},
		{"NPM sends another TLS alert", m6nResponse{}, get(&net.OpError{Op: "remote error", Err: errors.New("tls: handshake failure")}), "handshake failure"},
	}
	for _, tc := range tests {
		err := w10hxGone(tc.resp, tc.err)
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: %v, want it accepted", tc.name, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%s: error %v, want one containing %q", tc.name, err, tc.want)
		}
	}
}

func TestW10HxRecreatedUnit(t *testing.T) {
	tests := []struct {
		name          string
		before, after string
		want          string
	}{
		{"a new container", "aa", "bb", ""},
		{"the same container", "aa", "aa", "did not change"},
		{"no id after the upgrade", "aa", "", "no container id"},
		{"no id before the upgrade", "", "bb", "no container id"},
	}
	for _, tc := range tests {
		err := w10hxRecreated(tc.before, tc.after)
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: %v, want it accepted", tc.name, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%s: error %v, want one containing %q", tc.name, err, tc.want)
		}
	}
}

func TestW10HxSetContainerUnit(t *testing.T) {
	id := strings.Repeat("ab", 32)
	cmd := w10hxSetContainerCmd("/var/lib/darkpanel/agent.db", id)
	for _, want := range []string{
		"sudo sqlite3 -cmd '.timeout 10000' /var/lib/darkpanel/agent.db",
		"UPDATE npm_instances SET container_id = '" + id + "' WHERE id = 1; SELECT changes();",
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("the command lacks %q:\n%s", want, cmd)
		}
	}
	for out, wantErr := range map[string]bool{"1\n": false, "1": false, "0\n": true, "2\n": true, "": true, "Error: database is locked\n": true} {
		if err := w10hxChanged(out); (err != nil) != wantErr {
			t.Errorf("w10hxChanged(%q) = %v, want an error: %t", out, err, wantErr)
		}
	}
}

func TestW10HxDiagnosticContextUnit(t *testing.T) {
	type key struct{}
	parent, cancelParent := context.WithCancel(context.WithValue(context.Background(), key{}, "kept"))
	cancelParent()
	ctx, cancel := w10hxDiagnosticContext(parent)
	defer cancel()
	if err := ctx.Err(); err != nil {
		t.Errorf("the diagnostic context is done after its parent was cancelled: %v", err)
	}
	deadline, ok := ctx.Deadline()
	if left := time.Until(deadline); !ok || left <= 0 || left > m6nDiagnosticTimeout {
		t.Errorf("deadline %v (set %t), want one within %s from now", deadline, ok, m6nDiagnosticTimeout)
	}
	if got := ctx.Value(key{}); got != "kept" {
		t.Errorf("the context value = %v, want the parent's", got)
	}
}
