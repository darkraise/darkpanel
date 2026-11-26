package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"strconv"
	"time"

	"github.com/darkraise/darkpanel/internal/agent/wire"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// The file endpoints (M11 spec section 5): files/<op>.
const (
	FileList     = "list"
	FileStat     = "stat"
	FileRead     = "read"
	FileDownload = "download"
	FileWrite    = "write"
	FileUpload   = "upload"
	FileMkdir    = "mkdir"
	FileRename   = "rename"
	FileDelete   = "delete"
	FileChmod    = "chmod"
	FileChown    = "chown"
)

// The body limits of the raw file endpoints.
const (
	maxWriteBody  = 1 << 20
	maxUploadBody = 512 << 20
	fileChunk     = 32 << 10
)

// FileOpener serves the console's file endpoints; the agent runtime
// implements it. OpenFiles returns the operations p may run, the context to
// run them under and done, called once the request ended; a transfer counts
// against the host's limit until done.
type FileOpener interface {
	OpenFiles(ctx context.Context, p core.Principal, transfer bool) (context.Context, core.ConsoleFiles, func(), error)
}

type releaseKey struct{}

// WithRelease returns ctx carrying release, which a transfer's handler
// calls once the runtime counted the transfer: from then on the runtime's
// Close ends it, so the daemon's guard need not be held.
func WithRelease(ctx context.Context, release func()) context.Context {
	return context.WithValue(ctx, releaseKey{}, release)
}

func releaseGuard(ctx context.Context) {
	if release, ok := ctx.Value(releaseKey{}).(func()); ok {
		release()
	}
}

// OpensTransfer reports whether r is an upload or a download, which last as
// long as their bytes take.
func OpensTransfer(r *http.Request) bool {
	switch r.URL.Path {
	case prefix + "files/" + FileUpload:
		return r.Method == http.MethodPost
	case prefix + "files/" + FileDownload:
		return r.Method == http.MethodGet
	}
	return false
}

func (h *handler) fileRoutes(mux *http.ServeMux) {
	for _, op := range []string{FileList, FileStat, FileRead, FileDownload} {
		mux.HandleFunc("GET "+prefix+"files/"+op, h.handleFile(op))
	}
	for _, op := range []string{FileWrite, FileUpload, FileMkdir, FileRename, FileDelete, FileChmod, FileChown} {
		mux.HandleFunc("POST "+prefix+"files/"+op, h.handleFile(op))
	}
}

// rawBody checks the body of a write or an upload: application/octet-stream
// with a Content-Length of at most limit.
func rawBody(r *http.Request, limit int64) (int64, error) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	switch {
	case err != nil || mediaType != "application/octet-stream":
		return 0, errcode.New(errcode.UnsupportedMediaType, "send the file with Content-Type: application/octet-stream")
	case r.ContentLength < 0:
		return 0, errcode.New(errcode.InvalidInput, "a file body needs its Content-Length")
	case r.ContentLength > limit:
		return 0, errcode.New(errcode.TooLarge, fmt.Sprintf("the file body exceeds %d MiB", limit>>20))
	}
	return r.ContentLength, nil
}

// handleFile serves files/<op>. A refusal before the runtime opened the
// files is an ordinary problem answer, and so is every operation's error
// before its answer starts.
func (h *handler) handleFile(op string) http.HandlerFunc {
	transfer := op == FileUpload || op == FileDownload
	return func(w http.ResponseWriter, r *http.Request) {
		// ServeMux routes HEAD to the GET routes; a HEAD download would
		// read the whole file for nothing.
		if r.Method == http.MethodHead {
			w.Header().Set("Allow", http.MethodGet)
			h.writeProblem(w, errcode.New(errcode.MethodNotAllowed, "a file read uses GET"))
			return
		}
		p, err := h.actor(r, opFiles, op)
		if err != nil {
			h.writeProblem(w, err)
			return
		}
		opener, ok := h.cfg.Agent.(FileOpener)
		if !ok {
			h.writeProblem(w, errcode.New(errcode.NotFound, "this agent serves no file manager"))
			return
		}
		var size int64
		switch op {
		case FileWrite:
			size, err = rawBody(r, maxWriteBody)
		case FileUpload:
			size, err = rawBody(r, maxUploadBody)
		}
		if err != nil {
			h.writeProblem(w, err)
			return
		}
		ctx, files, done, err := opener.OpenFiles(r.Context(), p, transfer)
		if err != nil {
			h.writeProblem(w, err)
			return
		}
		defer done()
		releaseGuard(r.Context())
		if transfer {
			// A read or write blocked on the peer ends with the transfer.
			rc := http.NewResponseController(w)
			stop := context.AfterFunc(ctx, func() {
				_ = rc.SetReadDeadline(time.Now())
				_ = rc.SetWriteDeadline(time.Now())
			})
			defer stop()
		}
		if err := h.serveFile(ctx, w, r, op, files, size); err != nil {
			h.writeProblem(w, err)
		}
	}
}

// serveFile runs op. It returns an error only before the answer started.
func (h *handler) serveFile(ctx context.Context, w http.ResponseWriter, r *http.Request, op string, files core.ConsoleFiles, size int64) error {
	q := r.URL.Query()
	path := q.Get("path")
	switch op {
	case FileList:
		page, err := files.List(ctx, path, q.Get("cursor"))
		if err != nil {
			return err
		}
		h.writeJSON(w, http.StatusOK, page)
	case FileStat:
		info, err := files.Stat(ctx, path)
		if err != nil {
			return err
		}
		h.writeJSON(w, http.StatusOK, info)
	case FileRead:
		data, sum, err := files.ReadText(ctx, path)
		if err != nil {
			return err
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.Header().Set(wire.HeaderSHA256, sum)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data) //nolint:gosec // G705: file text sent as text/plain, never rendered as HTML
	case FileDownload:
		return h.download(ctx, w, files, path)
	case FileWrite:
		data, err := io.ReadAll(io.LimitReader(r.Body, size))
		if err != nil {
			return errcode.Wrap(errcode.InvalidInput, err, "the file body could not be read")
		}
		if int64(len(data)) != size {
			return errcode.New(errcode.InvalidInput, fmt.Sprintf("the file body ended after %d of %d bytes", len(data), size))
		}
		sum, err := files.WriteText(ctx, path, data, q.Get("sha256"))
		if err != nil {
			return err
		}
		h.writeJSON(w, http.StatusOK, wire.FileWritten{SHA256: sum, Size: size})
	case FileUpload:
		sum, err := files.Upload(ctx, path, r.Body, size)
		if err != nil {
			return err
		}
		h.writeJSON(w, http.StatusOK, wire.FileWritten{SHA256: sum, Size: size})
	default:
		if err := h.fileChange(ctx, w, r, op, files); err != nil {
			return err
		}
		w.WriteHeader(http.StatusNoContent)
	}
	return nil
}

// download answers the regular file path's bytes, fileChunk at a time.
func (h *handler) download(ctx context.Context, w http.ResponseWriter, files core.ConsoleFiles, path string) error {
	body, size, err := files.Open(ctx, path)
	if err != nil {
		return err
	}
	defer func() { _ = body.Close() }()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.WriteHeader(http.StatusOK)
	buf := make([]byte, fileChunk)
	// A file that grows while it is sent is cut at its size; one that
	// shrinks ends the answer short, which the peer sees as an error.
	src := io.LimitReader(body, size)
	for ctx.Err() == nil {
		n, rerr := src.Read(buf)
		if n > 0 {
			if _, err := w.Write(buf[:n]); err != nil {
				return nil
			}
		}
		if rerr != nil {
			if !errors.Is(rerr, io.EOF) {
				h.cfg.Log.Warn("api: download", "path", path, "error", rerr)
			}
			return nil
		}
	}
	return nil
}

// fileChange runs one of the JSON operations: mkdir, rename, delete, chmod
// and chown.
func (h *handler) fileChange(ctx context.Context, w http.ResponseWriter, r *http.Request, op string, files core.ConsoleFiles) error {
	var body wire.FileChange
	if err := decodeJSON(w, r, &body); err != nil {
		return err
	}
	switch op {
	case FileMkdir:
		return files.Mkdir(ctx, body.Path)
	case FileRename:
		return files.Rename(ctx, body.Path, body.To)
	case FileDelete:
		return files.Delete(ctx, body.Path, body.Confirm)
	case FileChmod:
		mode, err := strconv.ParseUint(body.Mode, 8, 32)
		if err != nil {
			return errcode.New(errcode.InvalidInput, fmt.Sprintf("the mode %q is not an octal number", body.Mode))
		}
		return files.Chmod(ctx, body.Path, fs.FileMode(mode))
	case FileChown:
		uid, gid := -1, -1
		if body.UID != nil {
			uid = *body.UID
		}
		if body.GID != nil {
			gid = *body.GID
		}
		return files.Chown(ctx, body.Path, uid, gid)
	}
	return errcode.New(errcode.NotFound, "no file operation "+op)
}
