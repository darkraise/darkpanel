package fake

import (
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"testing"
)

func fkRecordBody(name, content, comment string) string {
	b, _ := json.Marshal(map[string]any{"type": "CNAME", "name": name, "content": content, "comment": comment, "proxied": true, "ttl": 1})
	return string(b)
}

func TestFkZones(t *testing.T) {
	s := New(t)
	a, b := s.AddZone("example.com"), s.AddZone("example.org")
	if a == "" || a == b {
		t.Fatalf("zone ids %q %q", a, b)
	}
	all := fkList(t, fkDecode(t, fkReq(t, s, http.MethodGet, "/zones", Token, "")))
	if len(all) != 2 || all[0]["id"] != a || all[0]["name"] != "example.com" {
		t.Errorf("zones = %v", all)
	}
	one := fkList(t, fkDecode(t, fkReq(t, s, http.MethodGet, "/zones?name=example.org", Token, "")))
	if len(one) != 1 || one[0]["id"] != b {
		t.Errorf("zones by name = %v", one)
	}
}

func TestFkDNSRecords(t *testing.T) {
	s := New(t)
	zone := s.AddZone("example.com")
	base := "/zones/" + zone + "/dns_records"

	r := fkReq(t, s, http.MethodPost, base, Token, fkRecordBody("a.example.com", "t1.cfargotunnel.com", "darkpanel:01J0"))
	rec := fkObject(t, fkDecode(t, r).Result)
	id, _ := rec["id"].(string)
	if r.Status != http.StatusOK || id == "" || rec["proxied"] != true || rec["comment"] != "darkpanel:01J0" || rec["type"] != "CNAME" {
		t.Fatalf("create: %d %s", r.Status, r.Body)
	}
	want := []Record{{ID: id, Type: "CNAME", Name: "a.example.com", Content: "t1.cfargotunnel.com", Comment: "darkpanel:01J0", Proxied: true}}
	if got := s.Records(zone); !reflect.DeepEqual(got, want) {
		t.Errorf("Records = %+v, want %+v", got, want)
	}

	if dup := fkReq(t, s, http.MethodPost, base, Token, fkRecordBody("a.example.com", "t1.cfargotunnel.com", "other")); dup.Status != http.StatusBadRequest ||
		fkDecode(t, dup).Errors[0].Code != 81058 {
		t.Errorf("identical record: %d %s, want 400 with code 81058", dup.Status, dup.Body)
	}
	if bad := fkReq(t, s, http.MethodPost, base, Token, `{"type":"CNAME"}`); bad.Status != http.StatusBadRequest {
		t.Errorf("record without a name: status %d, want 400", bad.Status)
	}
	if r := fkReq(t, s, http.MethodPost, "/zones/nozone/dns_records", Token, fkRecordBody("a", "b", "")); r.Status != http.StatusNotFound {
		t.Errorf("unknown zone: status %d, want 404", r.Status)
	}

	own := s.AddRecord(zone, Record{Type: "A", Name: "b.example.com", Content: "192.0.2.1", Comment: "mine"})
	if own == "" || own == id {
		t.Fatalf("AddRecord answered %q", own)
	}
	for name, tc := range map[string]struct {
		query string
		ids   []string
	}{
		"nothing":       {"", []string{id, own}},
		"name":          {"name=b.example.com", []string{own}},
		"exact comment": {"comment.exact=darkpanel:01J0", []string{id}},
		"comment part":  {"comment.contains=panel", []string{id}},
		"type":          {"type=A", []string{own}},
		"no match":      {"name=" + url.QueryEscape("c.example.com"), nil},
	} {
		var got []string
		for _, m := range fkList(t, fkDecode(t, fkReq(t, s, http.MethodGet, base+"?"+tc.query, Token, ""))) {
			got = append(got, m["id"].(string))
		}
		if !reflect.DeepEqual(got, tc.ids) {
			t.Errorf("filter %s: ids %v, want %v", name, got, tc.ids)
		}
	}

	one := fkObject(t, fkDecode(t, fkReq(t, s, http.MethodGet, base+"/"+id, Token, "")).Result)
	if one["content"] != "t1.cfargotunnel.com" || one["zone_id"] != zone {
		t.Errorf("get = %v", one)
	}
	if r := fkReq(t, s, http.MethodDelete, base+"/"+id, Token, ""); r.Status != http.StatusOK || fkObject(t, fkDecode(t, r).Result)["id"] != id {
		t.Fatalf("delete: %d %s", r.Status, r.Body)
	}
	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		if r := fkReq(t, s, method, base+"/"+id, Token, ""); r.Status != http.StatusNotFound || fkDecode(t, r).Errors[0].Code != 81044 {
			t.Errorf("%s of a deleted record: %d %s, want 404 with code 81044", method, r.Status, r.Body)
		}
	}
	if got := s.Records(zone); len(got) != 1 || got[0].ID != own {
		t.Errorf("Records after delete = %+v, want only the added record", got)
	}
}
