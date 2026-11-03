package dstate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/netip"
	"slices"

	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

type endpointDoc struct {
	Container  string   `json:"container"`
	Network    string   `json:"network"`
	Present    bool     `json:"present"`
	EndpointID string   `json:"endpointId,omitempty"`
	Aliases    []string `json:"aliases,omitempty"`
	IPAMConfig *ipamDoc `json:"ipamConfig,omitempty"`
}

type ipamDoc struct {
	IPv4 string `json:"ipv4,omitempty"`
	IPv6 string `json:"ipv6,omitempty"`
}

// Endpoint renders a container's attachment to a network; a nil ep means the
// container is not attached. Only the Recorded form holds the endpoint id,
// which a reconnect does not reproduce.
func Endpoint(containerID, networkID string, ep *dockerapi.EndpointSettings, p Phase) (json.RawMessage, error) {
	if err := p.check(); err != nil {
		return nil, err
	}
	doc := endpointDoc{Container: containerID, Network: networkID, Present: ep != nil}
	if ep != nil {
		if p == Recorded {
			doc.EndpointID = ep.EndpointID
		}
		doc.Aliases = slices.Compact(slices.Sorted(slices.Values(ep.Aliases)))
		if ep.IPAMConfig != nil {
			v4, v6 := addrText(ep.IPAMConfig.IPv4Address), addrText(ep.IPAMConfig.IPv6Address)
			if v4 != "" || v6 != "" {
				doc.IPAMConfig = &ipamDoc{IPv4: v4, IPv6: v6}
			}
		}
	}
	return json.Marshal(doc)
}

func addrText(a netip.Addr) string {
	if !a.IsValid() {
		return ""
	}
	return a.String()
}

// labelDigests keeps the keys of labels and replaces each value by
// "sha256:" and the hex SHA-256 of the value. A state document is only ever
// compared, never used to recreate an object, so a digest still tells a
// changed value apart while a secret placed in a label never reaches the
// effects journal.
func labelDigests(labels map[string]string) map[string]string {
	if len(labels) == 0 {
		return nil
	}
	out := make(map[string]string, len(labels))
	for k, v := range labels {
		sum := sha256.Sum256([]byte(v))
		out[k] = "sha256:" + hex.EncodeToString(sum[:])
	}
	return out
}

// networkDoc is the state of a created network; its label values are digests.
type networkDoc struct {
	ID      string            `json:"id"`
	Created string            `json:"created,omitempty"`
	Labels  map[string]string `json:"labels,omitempty"`
	Unused  bool              `json:"unused"`
}

// NetworkCreated renders a network that an action created, as InspectNetwork
// reports it; nil renders null. Label values are recorded as their SHA-256
// digests, never as given. Unused is true when no container is attached.
func NetworkCreated(n *dockerapi.Network) (json.RawMessage, error) {
	if n == nil {
		return json.RawMessage("null"), nil
	}
	created, err := Instant(n.Created)
	if err != nil {
		return nil, err
	}
	return json.Marshal(networkDoc{ID: n.ID, Created: created, Labels: labelDigests(n.Labels), Unused: len(n.Containers) == 0})
}

// volumeDoc is the state of a created volume; its label values are digests.
type volumeDoc struct {
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels,omitempty"`
	Unused bool              `json:"unused"`
}

// VolumeCreated renders a volume that an action created; nil renders null.
// Label values are recorded as their SHA-256 digests, never as given. The
// Engine does not report which containers use a volume, so the caller says
// whether it is unused.
func VolumeCreated(v *dockerapi.Volume, unused bool) (json.RawMessage, error) {
	if v == nil {
		return json.RawMessage("null"), nil
	}
	return json.Marshal(volumeDoc{Name: v.Name, Labels: labelDigests(v.Labels), Unused: unused})
}
