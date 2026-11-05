package derp

import (
	"strings"
	"testing"
)

// FuzzParseDERPMap feeds arbitrary policy text to parseDERPMap: it never
// panics, an error starts with the package name and returns an empty map, a
// map without a derpMap member has no regions, and every region of an
// accepted map is filed under its own RegionID, which the policy writes in
// decimal as the key (R74).
func FuzzParseDERPMap(f *testing.F) {
	f.Add(sdPolicyWithRegions)
	f.Add(`{"derpMap": {"Regions": {"abc": {"RegionID": 900}}}}`)
	f.Add(`{"derpMap": {"Regions": {"900": {"RegionID": 901}}}}`)
	f.Add(`{"derpMap": {"Regions": {"+900": {"RegionID": 900}}}}`)
	f.Add(`{"derpMap": {"Regions": {"-0": {"RegionID": 0}, "0": null}}}`)
	f.Add(`{"derpMap": 5}`)
	f.Add(`{"derpMap": null}`)
	f.Add(`{}`)
	f.Add(`{"derpMap": `)
	f.Fuzz(func(t *testing.T, text string) {
		m, err := parseDERPMap(text)
		if err != nil {
			if !strings.HasPrefix(err.Error(), "derp:") || m.Present || m.OmitDefaultRegions || len(m.Regions) != 0 {
				t.Fatalf("parseDERPMap(%q) = %+v, %v", text[:min(len(text), 64)], m, err)
			}
			return
		}
		if !m.Present && (m.OmitDefaultRegions || len(m.Regions) != 0) {
			t.Fatalf("parseDERPMap(%q) holds regions without a derpMap: %+v", text[:min(len(text), 64)], m)
		}
		for id, r := range m.Regions {
			if r.RegionID != id {
				t.Fatalf("parseDERPMap(%q) files region %d under %d", text[:min(len(text), 64)], r.RegionID, id)
			}
		}
	})
}
