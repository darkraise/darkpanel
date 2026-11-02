// Package artifact is the derper release artifact: its version, its derper.json manifest and its verified fetch.
package artifact

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/darkraise/darkpanel/internal/update"
)

// DerperVersion is the derper release this Darkpanel builds, publishes and installs; release/derper.version holds
// the same value (a test reads that file and compares).
const DerperVersion = "v1.104.1"

// ManifestName is the release asset listing the derper builds.
const ManifestName = "derper.json"

// Arches are the architectures a release builds derper for, in order.
var Arches = []string{"amd64", "arm64"}

var (
	derperVersionPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)
	hashPattern          = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// BinaryName is the release asset of derper version for arch: "derper-" + version + "-linux-" + arch
// (BinaryName("v1.104.1", "amd64") == "derper-v1.104.1-linux-amd64").
func BinaryName(version, arch string) string {
	return "derper-" + version + "-linux-" + arch
}

// PinVersion is the tailscaled version a DERP host is pinned to: DerperVersion without its leading "v" ("1.104.1").
func PinVersion() string {
	return strings.TrimPrefix(DerperVersion, "v")
}

// Manifest is derper.json.
type Manifest struct {
	DarkpanelVersion string            `json:"darkpanelVersion"` // a version update.ParseVersion accepts, without "v"
	DerperVersion    string            `json:"derperVersion"`    // ^v[0-9]+\.[0-9]+\.[0-9]+$
	Artifacts        map[string]string `json:"artifacts"`        // arch → 64 lower-case hex digits; exactly the keys of Arches
}

// ParseManifest decodes b strictly: unknown members, a missing member, a version that does not match its pattern,
// an arch outside Arches, a missing arch or a hash that is not 64 lower-case hex digits is an error naming it.
func ParseManifest(b []byte) (Manifest, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	var members map[string]json.RawMessage
	if err := dec.Decode(&members); err != nil {
		return Manifest{}, fmt.Errorf("artifact: read %s: %w", ManifestName, err)
	}
	if members == nil {
		return Manifest{}, fmt.Errorf("artifact: read %s: not a JSON object", ManifestName)
	}
	if _, err := dec.Token(); err != io.EOF {
		return Manifest{}, fmt.Errorf("artifact: read %s: trailing data after the JSON object", ManifestName)
	}

	var m Manifest
	targets := []struct {
		name string
		dst  any
	}{
		{"darkpanelVersion", &m.DarkpanelVersion},
		{"derperVersion", &m.DerperVersion},
		{"artifacts", &m.Artifacts},
	}
	known := make(map[string]bool, len(targets))
	for _, t := range targets {
		known[t.name] = true
	}
	names := make([]string, 0, len(members))
	for name := range members {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if !known[name] {
			return Manifest{}, fmt.Errorf("artifact: %s has an unknown member %q", ManifestName, name)
		}
	}
	for _, t := range targets {
		raw, ok := members[t.name]
		if !ok || string(bytes.TrimSpace(raw)) == "null" {
			return Manifest{}, fmt.Errorf("artifact: %s lacks the member %q", ManifestName, t.name)
		}
		if err := json.Unmarshal(raw, t.dst); err != nil {
			return Manifest{}, fmt.Errorf("artifact: %s member %q: %w", ManifestName, t.name, err)
		}
	}
	if err := m.validate(); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

// Encode is m as two-space indented JSON with a trailing newline (map keys sorted, as encoding/json sorts them);
// it validates m as ParseManifest does first.
func (m Manifest) Encode() ([]byte, error) {
	if err := m.validate(); err != nil {
		return nil, err
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("artifact: encode %s: %w", ManifestName, err)
	}
	return append(b, '\n'), nil
}

func (m Manifest) validate() error {
	if _, err := update.ParseVersion(m.DarkpanelVersion); err != nil {
		return fmt.Errorf("artifact: darkpanelVersion %q is not a semantic version without a leading v", m.DarkpanelVersion)
	}
	if !derperVersionPattern.MatchString(m.DerperVersion) {
		return fmt.Errorf("artifact: derperVersion %q is not v<major>.<minor>.<patch>", m.DerperVersion)
	}
	keys := make([]string, 0, len(m.Artifacts))
	for arch := range m.Artifacts {
		keys = append(keys, arch)
	}
	sort.Strings(keys)
	for _, arch := range keys {
		if !slices.Contains(Arches, arch) {
			return fmt.Errorf("artifact: artifacts names the architecture %q, which is not one of %s",
				arch, strings.Join(Arches, ", "))
		}
	}
	for _, arch := range Arches {
		hash, ok := m.Artifacts[arch]
		if !ok {
			return fmt.Errorf("artifact: artifacts lacks the architecture %q", arch)
		}
		if !hashPattern.MatchString(hash) {
			return fmt.Errorf("artifact: artifacts[%q] is not 64 lower-case hex digits", arch)
		}
	}
	return nil
}
