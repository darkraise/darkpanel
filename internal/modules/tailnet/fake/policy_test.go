package fake

import (
	"net/http"
	"strings"
	"testing"
)

const (
	fkACL      = "/api/v2/tailnet/example.com/acl"
	fkHuJSON   = "application/hujson"
	fkNewStyle = "{\n\t// keep this note\n\t\"acls\": [],\n}\n"
)

func TestFkPolicyRead(t *testing.T) {
	s := New(t)
	tok := fkToken(t, s)
	text, etag := s.Policy()
	if !strings.Contains(text, "//") || etag == "" {
		t.Fatalf("default policy %q etag %q: want a commented policy and an etag", text, etag)
	}

	r := fkReq(t, s, http.MethodGet, fkACL, tok, map[string]string{"Accept": fkHuJSON}, "")
	if r.Status != http.StatusOK || r.Body != text || r.Header.Get("ETag") != etag || !strings.HasPrefix(r.Header.Get("Content-Type"), fkHuJSON) {
		t.Errorf("hujson read: %d etag %q type %q body %q", r.Status, r.Header.Get("ETag"), r.Header.Get("Content-Type"), r.Body)
	}
	j := fkReq(t, s, http.MethodGet, fkACL, tok, map[string]string{"Accept": "application/json"}, "")
	if strings.Contains(j.Body, "//") || fkDecode(t, j.Body)["acls"] == nil || j.Header.Get("ETag") != etag {
		t.Errorf("json read: etag %q body %q", j.Header.Get("ETag"), j.Body)
	}

	next := s.SetPolicy(fkNewStyle)
	if got, gotTag := s.Policy(); got != fkNewStyle || gotTag != next || next == etag {
		t.Errorf("after SetPolicy: text %q etag %q (returned %q, was %q)", got, gotTag, next, etag)
	}
	if again := s.SetPolicy(fkNewStyle); again != next {
		t.Errorf("same text gave etag %q, want %q", again, next)
	}
}

func TestFkPolicyWriteIsConditional(t *testing.T) {
	s := New(t)
	tok := fkToken(t, s)
	_, etag := s.Policy()
	hdr := func(match string) map[string]string {
		return map[string]string{"Content-Type": fkHuJSON, "Accept": fkHuJSON, "If-Match": match}
	}

	stale := fkReq(t, s, http.MethodPost, fkACL, tok, hdr(`"stale"`), fkNewStyle)
	if stale.Status != http.StatusPreconditionFailed {
		t.Fatalf("stale If-Match: status %d, want 412", stale.Status)
	}
	if text, _ := s.Policy(); text == fkNewStyle {
		t.Fatalf("a refused write changed the policy")
	}

	ok := fkReq(t, s, http.MethodPost, fkACL, tok, hdr(etag), fkNewStyle)
	_, now := s.Policy()
	if ok.Status != http.StatusOK || ok.Header.Get("ETag") != now || now == etag {
		t.Fatalf("matching If-Match: status %d, answered etag %q, stored %q, old %q", ok.Status, ok.Header.Get("ETag"), now, etag)
	}
	if text, _ := s.Policy(); text != fkNewStyle {
		t.Errorf("stored policy %q, want the posted text with its comment", text)
	}

	unquoted := fkReq(t, s, http.MethodPost, fkACL, tok, hdr(strings.Trim(now, `"`)), "{}")
	if unquoted.Status != http.StatusOK {
		t.Errorf("If-Match without quotes: status %d, want 200", unquoted.Status)
	}
	bad := fkReq(t, s, http.MethodPost, fkACL, tok, map[string]string{"Content-Type": fkHuJSON}, `{"acls": [`)
	if bad.Status != http.StatusBadRequest || fkDecode(t, bad.Body)["message"] == "" {
		t.Errorf("unparseable policy: %d %s, want 400 with a message", bad.Status, bad.Body)
	}
}

func TestFkPolicyValidate(t *testing.T) {
	s := New(t)
	tok := fkToken(t, s)
	_, etag := s.Policy()
	valid := fkReq(t, s, http.MethodPost, fkACL+"/validate", tok, map[string]string{"Content-Type": fkHuJSON}, fkNewStyle)
	if valid.Status != http.StatusOK || strings.TrimSpace(valid.Body) != "" {
		t.Errorf("valid policy: %d %q, want 200 with an empty body", valid.Status, valid.Body)
	}
	for name, body := range map[string]string{"truncated": `{"acls": [`, "not an object": `[1]`} {
		r := fkReq(t, s, http.MethodPost, fkACL+"/validate", tok, map[string]string{"Content-Type": fkHuJSON}, body)
		doc := fkDecode(t, r.Body)
		if r.Status != http.StatusBadRequest || doc["message"] == "" || doc["data"] == nil {
			t.Errorf("%s: %d %s, want 400 with message and data", name, r.Status, r.Body)
		}
	}
	if _, now := s.Policy(); now != etag {
		t.Errorf("validate changed the policy")
	}
}
