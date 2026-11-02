package derp

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/darkraise/darkpanel/internal/modules/tailnet/policy"
)

// derpMap is the policy file's "derpMap" member as the module reads it (Tailscale's JSON names: "OmitDefaultRegions",
// "Regions" keyed by the decimal region id, each region "RegionID", "RegionCode", "RegionName", "Nodes", each node
// "Name", "RegionID", "HostName", "CertName", "IPv4", "IPv6", "STUNPort", "DERPPort").
type derpMap struct {
	Present            bool // the policy has a derpMap member
	OmitDefaultRegions bool
	Regions            map[int]derpRegion
}

// derpRegion is one region of the derpMap.
type derpRegion struct {
	RegionID               int
	RegionCode, RegionName string
	Nodes                  []derpNode
}

// derpNode is one node of a region.
type derpNode struct {
	Name, HostName, CertName, IPv4, IPv6 string
	RegionID, STUNPort, DERPPort         int
}

// policyDERPMap is the policy file's derpMap member in Tailscale's names.
type policyDERPMap struct {
	OmitDefaultRegions bool                        `json:"OmitDefaultRegions"`
	Regions            map[string]policyDERPRegion `json:"Regions"`
}

type policyDERPRegion struct {
	RegionID   int              `json:"RegionID"`
	RegionCode string           `json:"RegionCode"`
	RegionName string           `json:"RegionName"`
	Nodes      []policyDERPNode `json:"Nodes"`
}

type policyDERPNode struct {
	Name     string `json:"Name"`
	RegionID int    `json:"RegionID"`
	HostName string `json:"HostName"`
	CertName string `json:"CertName"`
	IPv4     string `json:"IPv4"`
	IPv6     string `json:"IPv6"`
	STUNPort int    `json:"STUNPort"`
	DERPPort int    `json:"DERPPort"`
}

// parseDERPMap reads text (HuJSON) through policy.Canonical; a Regions key that is not a decimal integer equal to
// its region's RegionID is an error naming it.
func parseDERPMap(text string) (derpMap, error) {
	canonical, err := policy.Canonical([]byte(text))
	if err != nil {
		return derpMap{}, fmt.Errorf("derp: read the policy: %w", err)
	}
	var doc struct {
		DERPMap json.RawMessage `json:"derpMap"`
	}
	if err := json.Unmarshal(canonical, &doc); err != nil {
		return derpMap{}, fmt.Errorf("derp: read the policy: %w", err)
	}
	if len(doc.DERPMap) == 0 || string(doc.DERPMap) == "null" {
		return derpMap{}, nil
	}
	if doc.DERPMap[0] != '{' {
		return derpMap{}, errors.New("derp: the policy's derpMap is not an object")
	}
	var raw policyDERPMap
	if err := json.Unmarshal(doc.DERPMap, &raw); err != nil {
		return derpMap{}, fmt.Errorf("derp: read the policy's derpMap: %w", err)
	}
	m := derpMap{Present: true, OmitDefaultRegions: raw.OmitDefaultRegions, Regions: make(map[int]derpRegion, len(raw.Regions))}
	for key, r := range raw.Regions {
		id, err := strconv.Atoi(key)
		if err != nil || strconv.Itoa(id) != key || id != r.RegionID {
			return derpMap{}, fmt.Errorf("derp: the policy's derpMap region key %q is not the decimal RegionID %d", key, r.RegionID)
		}
		region := derpRegion{RegionID: r.RegionID, RegionCode: r.RegionCode, RegionName: r.RegionName}
		for _, n := range r.Nodes {
			region.Nodes = append(region.Nodes, derpNode{
				Name: n.Name, HostName: n.HostName, CertName: n.CertName, IPv4: n.IPv4, IPv6: n.IPv6,
				RegionID: n.RegionID, STUNPort: n.STUNPort, DERPPort: n.DERPPort,
			})
		}
		m.Regions[id] = region
	}
	return m, nil
}

// regionOf returns the custom region (id 900-999) one of whose nodes has HostName equal to hostName, ignoring case.
func (m derpMap) regionOf(hostName string) (derpRegion, bool) {
	if hostName == "" {
		return derpRegion{}, false
	}
	for _, id := range m.customRegions() {
		r := m.Regions[id]
		for _, n := range r.Nodes {
			if strings.EqualFold(n.HostName, hostName) {
				return r, true
			}
		}
	}
	return derpRegion{}, false
}

// customRegions returns the ids 900-999 present, ascending.
func (m derpMap) customRegions() []int {
	var ids []int
	for id := range m.Regions {
		if id >= 900 && id <= 999 {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids
}
