//go:build harness

package harness

import (
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

func a20Fetch(t *testing.T, url string) (string, error) {
	t.Helper()
	script := "pkgdir='" + t.TempDir() + "'\n" + fetchFunc + "fetch docker-ce 1.0-1 '" + url + "' '" + strings.Repeat("0", 64) + "'\n"
	out, err := exec.Command("bash", "-c", script).CombinedOutput() //nolint:gosec // G204: the test's own script
	return strings.TrimSpace(string(out)), err
}

func TestA20_APinnedPackageThatIsGoneIsAStalePin(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusGone} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }))
		out, err := a20Fetch(t, srv.URL+"/docker-ce.deb")
		srv.Close()
		const want = "pinned package docker-ce=1.0-1 missing; update the harness pins"
		if err == nil || a20Last(out) != want {
			t.Errorf("HTTP %d: %v, %q; want a failure ending %q", status, err, out, want)
		}
		if strings.Contains(out, "could not download") {
			t.Errorf("HTTP %d: %q reports a failed download for a stale pin", status, out)
		}
	}
}

func TestA20_AnyOtherFailureIsAFailedDownloadWithCurlsExitCodeAndTheHTTPStatus(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }))
		out, err := a20Fetch(t, srv.URL+"/docker-ce.deb")
		srv.Close()
		want := "could not download pinned package docker-ce=1.0-1 (curl exit 22, HTTP " + strconv.Itoa(status) + ")"
		if err == nil || a20Last(out) != want {
			t.Errorf("HTTP %d: %v, %q; want a failure ending %q", status, err, out, want)
		}
		if !strings.Contains(out, "curl: (22)") {
			t.Errorf("HTTP %d: %q does not carry curl's own error line", status, out)
		}
		if strings.Contains(out, "missing; update the harness pins") {
			t.Errorf("HTTP %d: %q reports a stale pin for an outage", status, out)
		}
	}
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	out, err := a20Fetch(t, url+"/docker-ce.deb")
	const want = "could not download pinned package docker-ce=1.0-1 (curl exit 7, HTTP 000)"
	if err == nil || a20Last(out) != want {
		t.Errorf("a server that is down: %v, %q; want a failure ending %q", err, out, want)
	}
	if !strings.Contains(out, "curl: (7)") {
		t.Errorf("a server that is down: %q does not carry curl's own error line", out)
	}
}

func a20Last(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}
