package dockerapi

import (
	"io"
	"net/http"
	"net/netip"
	"reflect"
	"strings"
	"testing"
)

const nwInspectJSON = `{
  "Name": "site_default", "Id": "net1", "Created": "2026-10-01T00:00:00.5Z", "Scope": "local", "Driver": "bridge",
  "EnableIPv4": true, "EnableIPv6": true,
  "IPAM": {"Driver": "default", "Options": null, "Config": [
    {"Subnet": "172.20.0.0/16", "Gateway": "172.20.0.1"},
    {"Subnet": "fd00:20::/64", "Gateway": "fd00:20::1"}]},
  "Internal": false, "Attachable": false, "Ingress": false, "ConfigFrom": {"Network": ""}, "ConfigOnly": false,
  "Containers": {
    "c1": {"Name": "web", "EndpointID": "ep1", "MacAddress": "02:42:ac:14:00:02", "IPv4Address": "172.20.0.2/16", "IPv6Address": "fd00:20::2/64"},
    "c2": {"Name": "db", "EndpointID": "ep2", "MacAddress": "02:42:ac:14:00:03", "IPv4Address": "172.20.0.3/16", "IPv6Address": ""}},
  "Options": {"com.docker.network.bridge.name": "br-site"},
  "Labels": {"com.docker.compose.project": "site"}
}`

func TestInspectAndListNetworks(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1.49/networks/{id}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") != "site_default" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"message":"network `+r.PathValue("id")+` not found"}`) //nolint:gosec // G705: a test fake answering JSON
			return
		}
		_, _ = io.WriteString(w, nwInspectJSON)
	})
	mux.HandleFunc("GET /v1.49/networks", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("filters") != `{"driver":["bridge"]}` {
			t.Errorf("filters = %s", r.URL.Query().Get("filters"))
		}
		_, _ = io.WriteString(w, `[`+nwInspectJSON+`]`)
	})
	c := newFakeDaemon(t, "1.49", mux)
	ctx := t.Context()
	n, err := c.InspectNetwork(ctx, "site_default")
	if err != nil {
		t.Fatal(err)
	}
	if n.ID != "net1" || n.EnableIPv4 == nil || !*n.EnableIPv4 || !n.EnableIPv6 || n.Options["com.docker.network.bridge.name"] != "br-site" ||
		n.IPAM.Config[0].Subnet != netip.MustParsePrefix("172.20.0.0/16") ||
		n.IPAM.Config[1].Gateway != netip.MustParseAddr("fd00:20::1") {
		t.Fatalf("network = %+v", n)
	}
	web, db := n.Containers["c1"], n.Containers["c2"]
	if web.IPv4Address != netip.MustParsePrefix("172.20.0.2/16") || web.IPv6Address != netip.MustParsePrefix("fd00:20::2/64") ||
		db.IPv6Address.IsValid() || db.Name != "db" {
		t.Fatalf("containers = %+v", n.Containers)
	}
	list, err := c.ListNetworks(ctx, Filters{"driver": {"bridge"}})
	if err != nil || len(list) != 1 || list[0].Name != "site_default" {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if _, err := c.InspectNetwork(ctx, "gone"); !IsNotFound(err) {
		t.Fatalf("missing network err = %v", err)
	}
}

func TestNetworkMutations(t *testing.T) {
	mux := http.NewServeMux()
	var seen []string
	record := func(name string, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		seen = append(seen, name+" "+string(b))
	}
	mux.HandleFunc("POST /v1.49/networks/create", func(w http.ResponseWriter, r *http.Request) {
		record("create", r)
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"Id":"net9","Warning":""}`)
	})
	mux.HandleFunc("POST /v1.49/networks/{id}/{op}", func(w http.ResponseWriter, r *http.Request) {
		record(r.PathValue("op")+" "+r.PathValue("id"), r)
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("DELETE /v1.49/networks/{id}", func(w http.ResponseWriter, r *http.Request) {
		record("delete "+r.PathValue("id"), r)
		w.WriteHeader(http.StatusNoContent)
	})
	c := newFakeDaemon(t, "1.49", mux)
	ctx := t.Context()
	v6 := false
	id, err := c.CreateNetwork(ctx, NetworkCreate{
		Name:       "darkpanel-panel",
		Driver:     "bridge",
		EnableIPv6: &v6,
		IPAM:       &IPAM{Config: []IPAMConfig{{Subnet: netip.MustParsePrefix("10.200.0.0/29")}}},
		Options:    map[string]string{"com.docker.network.bridge.name": "dp-panel"},
		Labels:     map[string]string{"darkpanel.module": "panel"},
	})
	if err != nil || id != "net9" {
		t.Fatalf("create = %q, %v", id, err)
	}
	if err := c.ConnectNetwork(ctx, "net9", "npm", &EndpointSettings{
		Aliases:    []string{"proxy"},
		IPAMConfig: &EndpointIPAMConfig{IPv4Address: netip.MustParseAddr("10.200.0.2")},
	}); err != nil {
		t.Fatal(err)
	}
	if err := c.ConnectNetwork(ctx, "net9", "plain", nil); err != nil {
		t.Fatal(err)
	}
	if err := c.DisconnectNetwork(ctx, "net9", "npm", true); err != nil {
		t.Fatal(err)
	}
	if err := c.RemoveNetwork(ctx, "net9"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		`create {"Name":"darkpanel-panel","Driver":"bridge","EnableIPv6":false,"IPAM":{"Config":[{"Subnet":"10.200.0.0/29"}]},` +
			`"Options":{"com.docker.network.bridge.name":"dp-panel"},"Labels":{"darkpanel.module":"panel"}}`,
		`connect net9 {"Container":"npm","EndpointConfig":{"IPAMConfig":{"IPv4Address":"10.200.0.2"},"Aliases":["proxy"]}}`,
		`connect net9 {"Container":"plain"}`,
		`disconnect net9 {"Container":"npm","Force":true}`,
		`delete net9 `,
	}
	if !reflect.DeepEqual(seen, want) {
		t.Fatalf("requests =\n%s", strings.Join(seen, "\n"))
	}
}
