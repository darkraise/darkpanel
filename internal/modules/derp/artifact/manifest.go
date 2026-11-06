// Package artifact is the derper release artifact: its version, its names and its derper.json manifest.
package artifact

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
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
	// semverPattern is versionPattern of internal/update, copied so that the release tool, which reads manifests,
	// does not link the updater.
	semverPattern = regexp.MustCompile(
		`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$`)
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
	if err := checkNoRepeats(b); err != nil {
		return Manifest{}, err
	}
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

// checkNoRepeats refuses a member named twice in the document, or an architecture named twice in its artifacts,
// which decoding into a map would silently collapse to the last one. Names are compared as decoded. A document that
// is not an object, or that the walk cannot read, is left to the decoding that follows.
func checkNoRepeats(b []byte) error {
	members, ok := objectMembers(b)
	if !ok {
		return nil
	}
	seen := make(map[string]bool, len(members))
	for _, mb := range members {
		if seen[mb.name] {
			return fmt.Errorf("artifact: %s has the member %q twice", ManifestName, mb.name)
		}
		seen[mb.name] = true
		if mb.name != "artifacts" {
			continue
		}
		arches, ok := objectMembers(mb.value)
		if !ok {
			continue
		}
		seenArch := make(map[string]bool, len(arches))
		for _, a := range arches {
			if seenArch[a.name] {
				return fmt.Errorf("artifact: %s artifacts has the architecture %q twice", ManifestName, a.name)
			}
			seenArch[a.name] = true
		}
	}
	return nil
}

type jsonMember struct {
	name  string
	value json.RawMessage
}

// objectMembers returns the members of the JSON object b in document order, repeats included; ok is false when b
// does not start with an object or the object cannot be read.
func objectMembers(b []byte) (members []jsonMember, ok bool) {
	dec := json.NewDecoder(bytes.NewReader(b))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, false
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, false
		}
		name, isName := tok.(string)
		if !isName {
			return nil, false
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, false
		}
		members = append(members, jsonMember{name, value})
	}
	return members, true
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
	if !isSemver(m.DarkpanelVersion) {
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
		if _, ok := m.Artifacts[arch]; !ok {
			return fmt.Errorf("artifact: artifacts lacks the architecture %q", arch)
		}
	}
	for _, arch := range Arches {
		if !hashPattern.MatchString(m.Artifacts[arch]) {
			return fmt.Errorf("artifact: artifacts[%q] is not 64 lower-case hex digits", arch)
		}
	}
	return nil
}

// isSemver reports whether v is a version update.ParseVersion accepts: it matches semverPattern and each of its
// three numbers fits in a uint64.
func isSemver(v string) bool {
	m := semverPattern.FindStringSubmatch(v)
	if m == nil {
		return false
	}
	for _, n := range m[1:4] {
		if _, err := strconv.ParseUint(n, 10, 64); err != nil {
			return false
		}
	}
	return true
}
