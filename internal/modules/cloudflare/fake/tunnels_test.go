package fake

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
)

func TestFkTunnelLifecycle(t *testing.T) {
	s := New(t)
	base := fkAccount("/cfd_tunnel")
	for name, body := range map[string]string{
		"no name":         `{"config_src":"cloudflare"}`,
		"locally managed": `{"name":"x","config_src":"local"}`,
		"not json":        `{`,
	} {
		if r := fkReq(t, s, http.MethodPost, base, Token, body); r.Status != http.StatusBadRequest {
			t.Errorf("create %s: status %d, want 400", name, r.Status)
		}
	}
	r := fkReq(t, s, http.MethodPost, base, Token, `{"name":"darkpanel-01J0","config_src":"cloudflare"}`)
	created := fkObject(t, fkDecode(t, r).Result)
	id, _ := created["id"].(string)
	if r.Status != http.StatusOK || id == "" || created["name"] != "darkpanel-01J0" || created["status"] != "inactive" || created["deleted_at"] != nil {
		t.Fatalf("create: %d %s", r.Status, r.Body)
	}
	if dup := fkReq(t, s, http.MethodPost, base, Token, `{"name":"darkpanel-01J0","config_src":"cloudflare"}`); dup.Status != http.StatusConflict {
		t.Errorf("duplicate name: status %d, want 409", dup.Status)
	}
	other := s.AddTunnel("legacy")

	named := fkList(t, fkDecode(t, fkReq(t, s, http.MethodGet, base+"?is_deleted=false&name=legacy", Token, "")))
	if len(named) != 1 || named[0]["id"] != other {
		t.Errorf("list by name = %v, want only the legacy tunnel", named)
	}
	if all := fkList(t, fkDecode(t, fkReq(t, s, http.MethodGet, base+"?is_deleted=false", Token, ""))); len(all) != 2 {
		t.Errorf("list = %d tunnels, want 2", len(all))
	}

	s.SetConnections(id, 2)
	one := fkObject(t, fkDecode(t, fkReq(t, s, http.MethodGet, base+"/"+id, Token, "")).Result)
	conns, _ := one["connections"].([]any)
	if one["status"] != "healthy" || len(conns) != 2 {
		t.Errorf("tunnel with two connections: status %v, %d connection entries", one["status"], len(conns))
	}
	if c := conns[0].(map[string]any); c["colo_name"] == "" || c["origin_ip"] == "" || c["opened_at"] == "" || c["client_version"] == "" {
		t.Errorf("connection entry %v lacks members", c)
	}

	connectors := fkList(t, fkDecode(t, fkReq(t, s, http.MethodGet, base+"/"+id+"/connections", Token, "")))
	if len(connectors) != 1 || len(connectors[0]["conns"].([]any)) != 2 || connectors[0]["version"] == "" {
		t.Errorf("connections endpoint = %v, want one connector with two conns", connectors)
	}
	s.SetConnections(other, 0)
	if none := fkList(t, fkDecode(t, fkReq(t, s, http.MethodGet, base+"/"+other+"/connections", Token, ""))); len(none) != 0 {
		t.Errorf("connections of an idle tunnel = %v, want none", none)
	}

	tok := fkDecode(t, fkReq(t, s, http.MethodGet, base+"/"+id+"/token", Token, ""))
	var encoded string
	if err := json.Unmarshal(tok.Result, &encoded); err != nil {
		t.Fatalf("token result %s is not a string", tok.Result)
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || !json.Valid(raw) || !reflect.DeepEqual(fkObject(t, raw)["t"], id) {
		t.Errorf("token %q does not decode to a document naming the tunnel (%v)", encoded, err)
	}

	if r := fkReq(t, s, http.MethodDelete, base+"/"+id, Token, ""); r.Status != http.StatusBadRequest {
		t.Errorf("delete with connections: status %d, want 400", r.Status)
	}
	s.SetConnections(id, 0)
	if r := fkReq(t, s, http.MethodDelete, base+"/"+id, Token, ""); r.Status != http.StatusOK || fkObject(t, fkDecode(t, r).Result)["id"] != id {
		t.Fatalf("delete: %d %s", r.Status, r.Body)
	}
	if got := s.Tunnels(); len(got) != 1 || got[0].ID != other {
		t.Errorf("Tunnels after delete = %+v, want only the legacy tunnel", got)
	}
	for _, path := range []string{base + "/" + id, base + "/" + id + "/token", base + "/" + id + "/configurations"} {
		if r := fkReq(t, s, http.MethodGet, path, Token, ""); r.Status != http.StatusNotFound {
			t.Errorf("GET %s after delete: status %d, want 404", path, r.Status)
		}
	}
}

func TestFkTunnelConfiguration(t *testing.T) {
	s := New(t)
	id := s.AddTunnel("darkpanel-1")
	path := fkAccount("/cfd_tunnel/" + id + "/configurations")

	fresh := fkObject(t, fkDecode(t, fkReq(t, s, http.MethodGet, path, Token, "")).Result)
	if fresh["config"] != nil || fresh["tunnel_id"] != id || s.Configuration(id) != nil {
		t.Errorf("a new tunnel has configuration %v / %s", fresh["config"], s.Configuration(id))
	}

	good := `{"ingress":[{"hostname":"a.example.com","service":"http://web:80"},{"service":"http_status:404"}]}`
	r := fkReq(t, s, http.MethodPut, path, Token, `{"config":`+good+`}`)
	put := fkObject(t, fkDecode(t, r).Result)
	if r.Status != http.StatusOK || put["version"] != float64(1) {
		t.Fatalf("put: %d %s", r.Status, r.Body)
	}
	if string(s.Configuration(id)) != good {
		t.Errorf("stored configuration %s, want the posted config member byte for byte", s.Configuration(id))
	}
	got := fkObject(t, fkDecode(t, fkReq(t, s, http.MethodGet, path, Token, "")).Result)
	cfg, _ := json.Marshal(got["config"])
	if !json.Valid(cfg) || got["version"] != float64(1) || string(cfg) != good {
		t.Errorf("get after put: version %v config %s", got["version"], cfg)
	}

	for name, tc := range map[string]struct {
		body string
		code int
	}{
		"no catch-all":          {`{"config":{"ingress":[{"hostname":"a.example.com","service":"http://web:80"}]}}`, 1015},
		"empty ingress":         {`{"config":{"ingress":[]}}`, 1015},
		"catch-all with a path": {`{"config":{"ingress":[{"path":"/x","service":"http_status:404"}]}}`, 1015},
		"no config member":      {`{}`, 1001},
	} {
		r := fkReq(t, s, http.MethodPut, path, Token, tc.body)
		if e := fkDecode(t, r); r.Status != http.StatusBadRequest || len(e.Errors) != 1 || e.Errors[0].Code != tc.code {
			t.Errorf("%s: %d %s, want 400 with code %d", name, r.Status, r.Body, tc.code)
		}
	}
	if string(s.Configuration(id)) != good {
		t.Errorf("a refused put changed the configuration to %s", s.Configuration(id))
	}

	replaced := `{"ingress":[{"service":"http_status:404"}],"warp-routing":{"enabled":true}}`
	s.SetConfiguration(id, json.RawMessage(replaced))
	if string(s.Configuration(id)) != replaced {
		t.Errorf("SetConfiguration then Configuration = %s", s.Configuration(id))
	}
	again := fkObject(t, fkDecode(t, fkReq(t, s, http.MethodGet, path, Token, "")).Result)
	if again["version"] != float64(2) {
		t.Errorf("version after SetConfiguration = %v, want 2", again["version"])
	}
	if r := fkReq(t, s, http.MethodPut, fkAccount("/cfd_tunnel/none/configurations"), Token, `{"config":{}}`); r.Status != http.StatusNotFound {
		t.Errorf("put for an unknown tunnel: status %d, want 404", r.Status)
	}
}
