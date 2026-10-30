package fake

import (
	"net/http"
	"reflect"
	"testing"
)

func TestFkDevices(t *testing.T) {
	s := New(t)
	tok := fkToken(t, s)
	id := s.AddDevice(Device{Name: "web.example.ts.net", Hostname: "web", OS: "linux", User: "ann@example.com",
		Addresses: []string{"100.64.0.1"}, AdvertisedRoutes: []string{"10.0.0.0/24", "0.0.0.0/0"}, Tags: []string{"tag:srv"}})
	if id != "n1" {
		t.Fatalf("AddDevice assigned %q, want n1", id)
	}
	if got := s.AddDevice(Device{ID: "nodeX"}); got != "nodeX" {
		t.Fatalf("AddDevice with an id answered %q, want nodeX", got)
	}

	list := fkDecode(t, fkReq(t, s, http.MethodGet, "/api/v2/tailnet/example.com/devices?fields=all", tok, nil, "").Body)
	devs := list["devices"].([]any)
	if len(devs) != 2 {
		t.Fatalf("list has %d devices, want 2", len(devs))
	}
	first := devs[0].(map[string]any)
	if first["id"] != "n1" || first["nodeId"] != "n1" || first["hostname"] != "web" || first["authorized"] != false {
		t.Errorf("first device %v", first)
	}
	if !reflect.DeepEqual(first["advertisedRoutes"], []any{"10.0.0.0/24", "0.0.0.0/0"}) || !reflect.DeepEqual(first["enabledRoutes"], []any{}) {
		t.Errorf("fields=all routes %v / %v", first["advertisedRoutes"], first["enabledRoutes"])
	}
	plain := fkDecode(t, fkReq(t, s, http.MethodGet, "/api/v2/tailnet/example.com/devices", tok, nil, "").Body)["devices"].([]any)[0].(map[string]any)
	if _, ok := plain["advertisedRoutes"]; ok {
		t.Errorf("a list without fields=all carries routes")
	}

	one := fkDecode(t, fkReq(t, s, http.MethodGet, "/api/v2/device/n1?fields=all", tok, nil, "").Body)
	if one["name"] != "web.example.ts.net" || one["user"] != "ann@example.com" {
		t.Errorf("device %v", one)
	}
	if r := fkReq(t, s, http.MethodGet, "/api/v2/device/none", tok, nil, ""); r.Status != http.StatusNotFound {
		t.Errorf("unknown device: status %d, want 404", r.Status)
	}

	if r := fkReq(t, s, http.MethodPost, "/api/v2/device/n1/authorized", tok, nil, `{"authorized":true}`); r.Status != http.StatusOK {
		t.Fatalf("authorize: %d %s", r.Status, r.Body)
	}
	if d, _ := s.Device("n1"); !d.Authorized {
		t.Errorf("device not authorized after the call")
	}

	routes := fkDecode(t, fkReq(t, s, http.MethodPost, "/api/v2/device/n1/routes", tok, nil, `{"routes":["10.0.0.0/24"]}`).Body)
	if !reflect.DeepEqual(routes["enabledRoutes"], []any{"10.0.0.0/24"}) || !reflect.DeepEqual(routes["advertisedRoutes"], []any{"10.0.0.0/24", "0.0.0.0/0"}) {
		t.Errorf("set routes answered %v", routes)
	}
	if d, _ := s.Device("n1"); !reflect.DeepEqual(d.EnabledRoutes, []string{"10.0.0.0/24"}) {
		t.Errorf("stored enabled routes %v", d.EnabledRoutes)
	}
	if got := fkDecode(t, fkReq(t, s, http.MethodGet, "/api/v2/device/n1/routes", tok, nil, "").Body); !reflect.DeepEqual(got["enabledRoutes"], []any{"10.0.0.0/24"}) {
		t.Errorf("get routes answered %v", got)
	}
	if r := fkReq(t, s, http.MethodPost, "/api/v2/device/n1/routes", tok, nil, `{}`); r.Status != http.StatusBadRequest {
		t.Errorf("routes body without a routes member: status %d, want 400", r.Status)
	}

	if r := fkReq(t, s, http.MethodPost, "/api/v2/device/n1/expire", tok, nil, ""); r.Status != http.StatusOK {
		t.Fatalf("expire: %d %s", r.Status, r.Body)
	}
	if d, _ := s.Device("n1"); d.Expires == "" {
		t.Errorf("device has no expiry after expire")
	}

	if r := fkReq(t, s, http.MethodDelete, "/api/v2/device/n1", tok, nil, ""); r.Status != http.StatusOK {
		t.Fatalf("delete: %d %s", r.Status, r.Body)
	}
	if _, ok := s.Device("n1"); ok {
		t.Errorf("device still stored after delete")
	}
	if r := fkReq(t, s, http.MethodDelete, "/api/v2/device/n1", tok, nil, ""); r.Status != http.StatusNotFound {
		t.Errorf("second delete: status %d, want 404", r.Status)
	}
}
