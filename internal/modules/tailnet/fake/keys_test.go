package fake

import (
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

const fkKeysPath = "/api/v2/tailnet/example.com/keys"

func fkKeyBody(description string, expiry int, tags ...string) string {
	quoted := make([]string, len(tags))
	for i, tag := range tags {
		quoted[i] = `"` + tag + `"`
	}
	exp := ""
	if expiry != 0 {
		exp = `,"expirySeconds":` + strconv.Itoa(expiry)
	}
	return `{"capabilities":{"devices":{"create":{"reusable":true,"ephemeral":false,"preauthorized":true,"tags":[` +
		strings.Join(quoted, ",") + `]}}},"description":"` + description + `"` + exp + `}`
}

func TestFkKeyCreateRefusals(t *testing.T) {
	s := New(t)
	tok := fkToken(t, s)
	for name, body := range map[string]string{
		"no tags":             fkKeyBody("darkpanel-1", 0),
		"tag without prefix":  fkKeyBody("darkpanel-1", 0, "srv"),
		"description over 50": fkKeyBody(strings.Repeat("d", 51), 0, "tag:srv"),
		"expiry over 90 days": fkKeyBody("darkpanel-1", 7776001, "tag:srv"),
		"not json":            `{`,
	} {
		if r := fkReq(t, s, http.MethodPost, fkKeysPath, tok, nil, body); r.Status != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", name, r.Status)
		}
	}
	if len(s.Keys()) != 0 {
		t.Errorf("a refused create stored %d keys", len(s.Keys()))
	}
}

func TestFkKeyLifecycle(t *testing.T) {
	s := New(t)
	tok := fkToken(t, s)
	before := time.Now().UTC().Add(-time.Second)
	r := fkReq(t, s, http.MethodPost, fkKeysPath, tok, nil, fkKeyBody("darkpanel-01J0", 3600, "tag:srv", "tag:db"))
	if r.Status != http.StatusOK {
		t.Fatalf("create: %d %s", r.Status, r.Body)
	}
	created := fkDecode(t, r.Body)
	id, _ := created["id"].(string)
	secret, _ := created["key"].(string)
	if id == "" || !strings.HasPrefix(secret, "tskey-auth-") || created["description"] != "darkpanel-01J0" {
		t.Fatalf("create answer %s", r.Body)
	}
	create := created["capabilities"].(map[string]any)["devices"].(map[string]any)["create"].(map[string]any)
	if create["reusable"] != true || create["preauthorized"] != true || create["ephemeral"] != false ||
		!reflect.DeepEqual(create["tags"], []any{"tag:srv", "tag:db"}) {
		t.Errorf("capabilities %v", create)
	}
	exp, err := time.Parse(time.RFC3339, created["expires"].(string))
	if err != nil || exp.Before(before.Add(time.Hour)) || exp.After(time.Now().Add(time.Hour+time.Minute)) {
		t.Errorf("expires %v (%v), want about an hour from now", created["expires"], err)
	}
	keys := s.Keys()
	if len(keys) != 1 || keys[0].ID != id || keys[0].Key != secret || keys[0].Description != "darkpanel-01J0" ||
		!reflect.DeepEqual(keys[0].Tags, []string{"tag:srv", "tag:db"}) || !keys[0].Reusable || !keys[0].Preauthorized || keys[0].Ephemeral {
		t.Errorf("stored keys %+v", keys)
	}

	s.AddKey(Key{Description: "darkpanel-enroll-h1", Tags: []string{"tag:srv"}, Expires: time.Now().Add(time.Hour)})
	s.AddKey(Key{ID: "old", Revoked: true})
	if none := fkDecode(t, fkReq(t, s, http.MethodGet, fkKeysPath, tok, nil, "").Body)["keys"].([]any); len(none) != 0 {
		t.Errorf("list without all=true answered %v, want no keys", none)
	}
	list := fkDecode(t, fkReq(t, s, http.MethodGet, fkKeysPath+"?all=true", tok, nil, "").Body)["keys"].([]any)
	var ids []string
	for _, e := range list {
		ids = append(ids, e.(map[string]any)["id"].(string))
	}
	if len(ids) != 2 || ids[0] != id || ids[1] == "old" {
		t.Errorf("list ids %v, want the created key then the added one and not the revoked one", ids)
	}

	one := fkDecode(t, fkReq(t, s, http.MethodGet, fkKeysPath+"/"+id, tok, nil, "").Body)
	if one["id"] != id || one["description"] != "darkpanel-01J0" {
		t.Errorf("get %v", one)
	}
	if _, leaked := one["key"]; leaked {
		t.Errorf("get returns the key secret")
	}
	if old := fkDecode(t, fkReq(t, s, http.MethodGet, fkKeysPath+"/old", tok, nil, "").Body); old["revoked"] == nil || old["invalid"] != true {
		t.Errorf("a revoked key shows no revoked member or no invalid true: %v", old)
	}

	if r := fkReq(t, s, http.MethodDelete, fkKeysPath+"/"+id, tok, nil, ""); r.Status != http.StatusOK {
		t.Fatalf("delete: %d %s", r.Status, r.Body)
	}
	if r := fkReq(t, s, http.MethodGet, fkKeysPath+"/"+id, tok, nil, ""); r.Status != http.StatusNotFound {
		t.Errorf("get after delete: status %d, want 404", r.Status)
	}
	if r := fkReq(t, s, http.MethodDelete, fkKeysPath+"/"+id, tok, nil, ""); r.Status != http.StatusNotFound {
		t.Errorf("second delete: status %d, want 404", r.Status)
	}
	if len(s.Keys()) != 2 {
		t.Errorf("Keys has %d entries after deleting one of three, want 2", len(s.Keys()))
	}
}

func TestFkKeyDefaultExpiry(t *testing.T) {
	s := New(t)
	tok := fkToken(t, s)
	fkReq(t, s, http.MethodPost, fkKeysPath, tok, nil, fkKeyBody("darkpanel-2", 0, "tag:srv"))
	k := s.Keys()[0]
	if got := k.Expires.Sub(k.Created); got != 90*24*time.Hour {
		t.Errorf("default expiry %v, want 90 days", got)
	}
}
