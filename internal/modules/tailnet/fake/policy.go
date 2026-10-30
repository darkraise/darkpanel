package fake

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/tailscale/hujson"
)

const (
	contentHuJSON = "application/hujson"
	contentJSON   = "application/json"
)

const defaultPolicy = `{
	// Allow every device to reach every other until a policy is set.
	"acls": [
		{"action": "accept", "src": ["*"], "dst": ["*:*"]},
	],
}
`

// etagOf is the quoted ETag of a policy text: the same text always has the same ETag.
func etagOf(text string) string {
	sum := sha256.Sum256([]byte(text))
	return `"` + hex.EncodeToString(sum[:])[:16] + `"`
}

// SetPolicy stores text without checking it and answers its ETag.
func (s *Server) SetPolicy(text string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.policy = text
	s.etag = etagOf(text)
	return s.etag
}

// Policy answers the stored policy text and its ETag.
func (s *Server) Policy() (text, etag string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.policy, s.etag
}

// checkPolicy is nil when text parses as HuJSON and its value is an object.
func checkPolicy(text string) error {
	v, err := hujson.Parse([]byte(text))
	if err != nil {
		return err
	}
	if v.Value.Kind() != '{' {
		return errors.New("the policy must be a JSON object")
	}
	return nil
}

// writePolicy answers 200 with text in the format the request's Accept header asks for.
func writePolicy(w http.ResponseWriter, r *http.Request, text, etag string) {
	body, contentType := []byte(text), contentHuJSON
	if !strings.Contains(r.Header.Get("Accept"), contentHuJSON) {
		std, err := hujson.Standardize([]byte(text))
		if err != nil {
			writeMessage(w, http.StatusInternalServerError, "stored policy is not valid HuJSON")
			return
		}
		body, contentType = std, contentJSON
	}
	w.Header().Set("ETag", etag)
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func writeInvalidPolicy(w http.ResponseWriter, err error) {
	writeJSON(w, http.StatusBadRequest, map[string]any{"message": "invalid policy: " + err.Error(), "data": []any{}})
}

func (s *Server) registerPolicy() {
	read := []string{"policy_file", "policy_file:read"}
	write := []string{"policy_file"}
	const path = "/api/v2/tailnet/{t}/acl"

	s.route(http.MethodGet, path, read, func(w http.ResponseWriter, r *http.Request) {
		text, etag := s.Policy()
		writePolicy(w, r, text, etag)
	})
	s.route(http.MethodPost, path, write, func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeMessage(w, http.StatusBadRequest, "unreadable request body")
			return
		}
		if err := checkPolicy(string(body)); err != nil {
			writeInvalidPolicy(w, err)
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if match := r.Header.Get("If-Match"); match != "" && strings.Trim(match, `"`) != strings.Trim(s.etag, `"`) {
			writeMessage(w, http.StatusPreconditionFailed, "precondition failed, invalid old hash")
			return
		}
		s.policy = string(body)
		s.etag = etagOf(s.policy)
		writePolicy(w, r, s.policy, s.etag)
	})
	s.route(http.MethodPost, path+"/validate", read, func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeMessage(w, http.StatusBadRequest, "unreadable request body")
			return
		}
		if err := checkPolicy(string(body)); err != nil {
			writeInvalidPolicy(w, err)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
}
