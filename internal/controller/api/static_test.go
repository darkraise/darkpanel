package api

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// testUI stands in for the built web UI.
var testUI = fstest.MapFS{
	"index.html":    {Data: []byte("<!doctype html><title>Darkpanel</title>")},
	"assets/app.js": {Data: []byte("console.log(1)")},
	"theme-init.js": {Data: []byte("document.documentElement.dataset.theme='dark'")},
}

func TestServeUI(t *testing.T) {
	for _, tc := range []struct {
		method, path, body, contentType string
	}{
		{"GET", "/", "<!doctype html><title>Darkpanel</title>", "text/html; charset=utf-8"},
		{"GET", "/index.html", "<!doctype html><title>Darkpanel</title>", "text/html; charset=utf-8"},
		{"GET", "/assets/app.js", "console.log(1)", "text/javascript; charset=utf-8"},
		{"GET", "/theme-init.js", "document.documentElement.dataset.theme='dark'", "text/javascript; charset=utf-8"},
		{"GET", "/hosts/01J/hostinfo", "<!doctype html><title>Darkpanel</title>", "text/html; charset=utf-8"},
		{"GET", "/assets", "<!doctype html><title>Darkpanel</title>", "text/html; charset=utf-8"},
		{"GET", "/assets/missing.js", "<!doctype html><title>Darkpanel</title>", "text/html; charset=utf-8"},
		{"GET", "/../index.html", "<!doctype html><title>Darkpanel</title>", "text/html; charset=utf-8"},
		{"HEAD", "/assets/app.js", "", "text/javascript; charset=utf-8"},
	} {
		r := httptest.NewRequest(tc.method, "/", nil)
		r.URL.Path = tc.path
		rec := httptest.NewRecorder()
		if !serveUI(rec, r, testUI) {
			t.Errorf("%s %s: not served", tc.method, tc.path)
			continue
		}
		if rec.Code != http.StatusOK || rec.Body.String() != tc.body || rec.Header().Get("Content-Type") != tc.contentType {
			t.Errorf("%s %s: got %d %q %q, want 200 %q %q", tc.method, tc.path, rec.Code,
				rec.Header().Get("Content-Type"), rec.Body, tc.contentType, tc.body)
		}
	}
}

func TestServeUIWithoutIndex(t *testing.T) {
	noIndex := fstest.MapFS{"assets/app.js": {Data: []byte("x")}}
	for _, ui := range []fs.FS{noIndex, nil} {
		r := httptest.NewRequest(http.MethodGet, "/hosts", nil)
		rec := httptest.NewRecorder()
		if serveUI(rec, r, ui) {
			t.Errorf("served %q without an index.html", rec.Body)
		}
		if rec.Body.Len() != 0 || rec.Code != http.StatusOK {
			t.Errorf("wrote a response: %d %q", rec.Code, rec.Body)
		}
	}
}

func TestEmbeddedUI(t *testing.T) {
	ui := EmbeddedUI()
	st, err := fs.Stat(ui, ".")
	if err != nil || !st.IsDir() {
		t.Fatalf("Stat(.) = %v, %v; want the dist directory", st, err)
	}
	entries, err := fs.ReadDir(ui, ".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "dist") {
			t.Errorf("EmbeddedUI is not rooted at dist: found %s", e.Name())
		}
	}
}
