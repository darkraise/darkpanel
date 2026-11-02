package derp

import (
	"reflect"
	"strings"
	"testing"
)

const sdPolicyWithRegions = `{
	// custom regions
	"acls": [],
	"derpMap": {
		"OmitDefaultRegions": true,
		"Regions": {
			"3": {"RegionID": 3, "RegionCode": "def", "RegionName": "Default", "Nodes": [
				{"Name": "3a", "RegionID": 3, "HostName": "default.example.net"},
			]},
			"900": {"RegionID": 900, "RegionCode": "fra-x", "RegionName": "Frankfurt X", "Nodes": [
				{"Name": "900a", "RegionID": 900, "HostName": "derp.Example.com", "DERPPort": 443, "STUNPort": 3478},
			]},
			"901": {"RegionID": 901, "RegionCode": "ams-x", "RegionName": "Amsterdam X", "Nodes": [
				{"Name": "901a", "RegionID": 901, "HostName": "203.0.113.10", "CertName": "sha256-raw:ab", "IPv4": "203.0.113.10", "DERPPort": 443, "STUNPort": 3478},
			]},
		},
	},
}`

func TestSdParseDERPMap(t *testing.T) {
	m, err := parseDERPMap(sdPolicyWithRegions)
	if err != nil {
		t.Fatal(err)
	}
	if !m.Present || !m.OmitDefaultRegions || len(m.Regions) != 3 {
		t.Fatalf("parsed %+v", m)
	}
	fra := m.Regions[900]
	if fra.RegionID != 900 || fra.RegionCode != "fra-x" || fra.RegionName != "Frankfurt X" {
		t.Errorf("region 900 = %+v", fra)
	}
	wantFra := []derpNode{{Name: "900a", HostName: "derp.Example.com", RegionID: 900, STUNPort: 3478, DERPPort: 443}}
	if !reflect.DeepEqual(fra.Nodes, wantFra) {
		t.Errorf("region 900 nodes %+v, want %+v", fra.Nodes, wantFra)
	}
	wantAms := derpNode{Name: "901a", HostName: "203.0.113.10", CertName: "sha256-raw:ab", IPv4: "203.0.113.10", RegionID: 901, STUNPort: 3478, DERPPort: 443}
	if got := m.Regions[901].Nodes; !reflect.DeepEqual(got, []derpNode{wantAms}) {
		t.Errorf("region 901 nodes %+v, want %+v", got, wantAms)
	}
}

func TestSdParseDERPMapAbsentAndEmpty(t *testing.T) {
	for _, tt := range []struct {
		name, text string
		present    bool
	}{
		{"no derpMap member", `{"acls": []}`, false},
		{"a null derpMap", `{"derpMap": null}`, false},
		{"an empty derpMap", `{"derpMap": {}}`, true},
		{"a derpMap with only the omit flag", `{"derpMap": {"OmitDefaultRegions": true}}`, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m, err := parseDERPMap(tt.text)
			if err != nil {
				t.Fatal(err)
			}
			if m.Present != tt.present || len(m.Regions) != 0 || len(m.customRegions()) != 0 {
				t.Errorf("parsed %+v", m)
			}
			if _, ok := m.regionOf("anything"); ok {
				t.Error("regionOf found a region in a map without any")
			}
		})
	}
	if m, _ := parseDERPMap(`{"derpMap": {"OmitDefaultRegions": true}}`); !m.OmitDefaultRegions {
		t.Error("the omit flag was lost")
	}
}

func TestSdDERPMapCustomRegions(t *testing.T) {
	m, err := parseDERPMap(sdPolicyWithRegions)
	if err != nil {
		t.Fatal(err)
	}
	if got := m.customRegions(); !reflect.DeepEqual(got, []int{900, 901}) {
		t.Errorf("customRegions = %v, want [900 901] (region 3 is a default region)", got)
	}
}

func TestSdDERPMapRegionOf(t *testing.T) {
	m, err := parseDERPMap(sdPolicyWithRegions)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		host string
		want int
	}{
		{"derp.example.com", 900},
		{"DERP.EXAMPLE.COM", 900},
		{"203.0.113.10", 901},
		{"default.example.net", 0}, // a node of a default region never counts
		{"other.example.com", 0},
		{"", 0},
	} {
		r, ok := m.regionOf(tt.host)
		if (tt.want != 0) != ok || r.RegionID != tt.want {
			t.Errorf("regionOf(%q) = region %d, %v; want region %d", tt.host, r.RegionID, ok, tt.want)
		}
	}
}

func TestSdParseDERPMapRefuses(t *testing.T) {
	for _, tt := range []struct{ name, text, mention string }{
		{"a key that is not a number", `{"derpMap": {"Regions": {"abc": {"RegionID": 900}}}}`, "abc"},
		{"a key that differs from its region id", `{"derpMap": {"Regions": {"900": {"RegionID": 901}}}}`, "900"},
		{"a key with a sign", `{"derpMap": {"Regions": {"+900": {"RegionID": 900}}}}`, "+900"},
		{"text that is not a policy", `{"derpMap": `, ""},
		{"a derpMap that is not an object", `{"derpMap": 5}`, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseDERPMap(tt.text)
			if err == nil {
				t.Fatal("parseDERPMap accepted the policy")
			}
			if !strings.Contains(err.Error(), tt.mention) {
				t.Errorf("error %q does not name %q", err, tt.mention)
			}
		})
	}
}
