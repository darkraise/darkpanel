package api

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/darkraise/darkpanel/web"
)

// EmbeddedUI returns the built web UI embedded in the binary (web.Dist),
// rooted at its dist directory: the value production passes as Config.UI.
func EmbeddedUI() fs.FS {
	ui, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		panic(fmt.Sprintf("api: embedded UI: %v", err)) // "dist" is a valid path; Sub cannot fail
	}
	return ui
}

// serveUI answers a GET or HEAD outside the API with the UI file at the
// request path, or with index.html when no file is there, so client routes
// work on reload (spec section 12). It writes nothing and returns false when
// neither exists.
func serveUI(w http.ResponseWriter, r *http.Request, ui fs.FS) bool {
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "" {
		name = "index.html"
	}
	return serveFile(w, r, ui, name) || serveFile(w, r, ui, "index.html")
}

func serveFile(w http.ResponseWriter, r *http.Request, ui fs.FS, name string) bool {
	if ui == nil || !fs.ValidPath(name) {
		return false
	}
	f, err := ui.Open(name)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		return false
	}
	content, ok := f.(io.ReadSeeker)
	if !ok {
		b, err := io.ReadAll(f)
		if err != nil {
			return false
		}
		content = bytes.NewReader(b)
	}
	http.ServeContent(w, r, name, st.ModTime(), content)
	return true
}
