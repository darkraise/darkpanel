//go:build harness

// Package harness holds the M1 integration tests (spec section 18): for each
// pinned operating system, a target VM with UFW, Docker and tailscaled, and a
// scanner VM on a shared "internet" link outside every private range.
package harness

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"
)

// PinsFile is the pin file, relative to this package's directory, which is
// the working directory of its tests.
const PinsFile = "images.json"

// RequiredPackages are the packages every OS pins, in the order the pin file
// lists them: Docker Engine and its Compose plugin from Docker's apt
// repository, and tailscaled from Tailscale's stable repository.
var RequiredPackages = []string{"docker-ce", "docker-ce-cli", "containerd.io", "docker-compose-plugin", "tailscale"}

// RequiredContainers are the container images the pin file lists, in its
// order: the ACME server Pebble and its DNS and challenge server, which
// stand in for Let's Encrypt in the certificate tests.
var RequiredContainers = []string{"pebble", "pebble-challtestsrv"}

// Pins are the pinned images and packages of every operating system the
// harness tests, and the container images its tests run on a target.
type Pins struct {
	Scanner    string      `json:"scanner"` // the OS id whose image the scanner VM boots
	OS         []OS        `json:"os"`
	Containers []Container `json:"containers"` // RequiredContainers, in that order
}

// Container is one container image, pinned by the digest of its image
// index; the tag only tells a reader which release it is.
type Container struct {
	Name string `json:"name"` // what a test asks Pins.Container for
	Ref  string `json:"ref"`  // <registry>/<repository>:<tag>@sha256:<digest>
}

// OS is one pinned operating system.
type OS struct {
	ID        string    `json:"id"`        // the -os value, such as ubuntu-22.04
	Family    string    `json:"family"`    // ubuntu or debian: the os-release ID
	VersionID string    `json:"versionId"` // the os-release VERSION_ID
	Image     Image     `json:"image"`     // the cloud image
	Packages  []Package `json:"packages"`  // RequiredPackages, in that order
}

// Image is a cloud image by dated release URL and SHA-256.
type Image struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

// Package is one .deb by exact version, repository pool URL and SHA-256.
type Package struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	URL     string `json:"url"`
	SHA256  string `json:"sha256"`
}

var (
	sha256Hex    = regexp.MustCompile(`^[0-9a-f]{64}$`)
	datedRelease = regexp.MustCompile(`/(release-)?[0-9]{8}(-[0-9]+)?/`)
	aptPackage   = regexp.MustCompile(`^[a-z0-9][a-z0-9.+-]*$`)
	aptVersion   = regexp.MustCompile(`^[0-9A-Za-z.:~+-]+$`)
	debURL       = regexp.MustCompile(`^https://[0-9A-Za-z./_~:+-]+\.deb$`)
	containerRef = regexp.MustCompile(`^[a-z0-9.-]+(/[a-z0-9._-]+)+:[0-9A-Za-z][0-9A-Za-z._-]{0,127}@sha256:[0-9a-f]{64}$`)
)

// LoadPins reads and checks the pin file at path.
func LoadPins(path string) (Pins, error) {
	b, err := os.ReadFile(path) //nolint:gosec // G304: the harness's own pin file
	if err != nil {
		return Pins{}, fmt.Errorf("harness: %w", err)
	}
	var p Pins
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return Pins{}, fmt.Errorf("harness: %s: %w", path, err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Pins{}, fmt.Errorf("harness: %s: trailing content after the pins object", path)
	}
	if err := p.check(); err != nil {
		return Pins{}, fmt.Errorf("harness: %s: %w", path, err)
	}
	return p, nil
}

// Find returns the pinned OS with id.
func (p Pins) Find(id string) (OS, error) {
	ids := make([]string, 0, len(p.OS))
	for _, o := range p.OS {
		if o.ID == id {
			return o, nil
		}
		ids = append(ids, o.ID)
	}
	return OS{}, fmt.Errorf("harness: unknown OS %q; the pin file lists %s", id, strings.Join(ids, ", "))
}

// Container returns the pinned container image with name.
func (p Pins) Container(name string) (Container, error) {
	for _, c := range p.Containers {
		if c.Name == name {
			return c, nil
		}
	}
	return Container{}, fmt.Errorf("harness: no pinned container image %q", name)
}

// check refuses duplicate ids, a scanner that names no listed OS, any entry
// OS.check refuses, and container images other than RequiredContainers or
// not pinned by digest: the provisioning script pastes the references into
// shell commands.
func (p Pins) check() error {
	seen := make(map[string]bool)
	for _, o := range p.OS {
		if err := o.check(); err != nil {
			return err
		}
		if seen[o.ID] {
			return fmt.Errorf("OS %s is listed twice", o.ID)
		}
		seen[o.ID] = true
	}
	if !seen[p.Scanner] {
		return fmt.Errorf("scanner %q names no listed OS", p.Scanner)
	}
	names := make([]string, 0, len(p.Containers))
	for _, c := range p.Containers {
		if !containerRef.MatchString(c.Ref) {
			return fmt.Errorf("container %s: ref %q is not <registry>/<repository>:<tag>@sha256:<64 hex digits>", c.Name, c.Ref)
		}
		names = append(names, c.Name)
	}
	if !slices.Equal(names, RequiredContainers) {
		return fmt.Errorf("containers %v, want %v", names, RequiredContainers)
	}
	return nil
}

// check refuses an undated or unverifiable image or package, and names,
// versions or URLs with characters apt does not use: the provisioning script
// pastes them into shell commands.
func (o OS) check() error {
	switch {
	case o.ID == "" || o.VersionID == "":
		return fmt.Errorf("OS %q: id and versionId are required", o.ID)
	case o.Family != "ubuntu" && o.Family != "debian":
		return fmt.Errorf("OS %s: family %q, want ubuntu or debian", o.ID, o.Family)
	case !strings.HasPrefix(o.Image.URL, "https://"):
		return fmt.Errorf("OS %s: image URL %q is not https", o.ID, o.Image.URL)
	case !datedRelease.MatchString(o.Image.URL):
		return fmt.Errorf("OS %s: image URL %q is not a dated release", o.ID, o.Image.URL)
	case !sha256Hex.MatchString(o.Image.SHA256):
		return fmt.Errorf("OS %s: image SHA-256 %q is not 64 lowercase hex digits", o.ID, o.Image.SHA256)
	}
	names := make([]string, 0, len(o.Packages))
	for _, p := range o.Packages {
		switch {
		case !aptPackage.MatchString(p.Name):
			return fmt.Errorf("OS %s: package name %q is not an apt package name", o.ID, p.Name)
		case !aptVersion.MatchString(p.Version):
			return fmt.Errorf("OS %s: %s version %q is not an apt version", o.ID, p.Name, p.Version)
		case !debURL.MatchString(p.URL):
			return fmt.Errorf("OS %s: %s URL %q is not an https .deb URL", o.ID, p.Name, p.URL)
		case !sha256Hex.MatchString(p.SHA256):
			return fmt.Errorf("OS %s: %s SHA-256 %q is not 64 lowercase hex digits", o.ID, p.Name, p.SHA256)
		}
		names = append(names, p.Name)
	}
	if !slices.Equal(names, RequiredPackages) {
		return fmt.Errorf("OS %s: packages %v, want %v", o.ID, names, RequiredPackages)
	}
	return nil
}
