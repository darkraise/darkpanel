//go:build harness

package harness

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestPinFile(t *testing.T) {
	p, err := LoadPins(PinsFile)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, o := range p.OS {
		ids = append(ids, o.ID)
	}
	if want := []string{"ubuntu-22.04", "ubuntu-24.04", "debian-12", "debian-13"}; !slices.Equal(ids, want) {
		t.Errorf("pinned OS ids = %v, want %v", ids, want)
	}
	if p.Scanner != "debian-13" {
		t.Errorf("scanner = %q, want debian-13", p.Scanner)
	}
}

// validOS is an entry every check accepts.
func validOS(id string) OS {
	pkg := func(name, version string) Package {
		return Package{
			Name: name, Version: version,
			URL:    "https://download.docker.com/linux/debian/dists/trixie/pool/stable/amd64/" + name + "_" + version + "_amd64.deb",
			SHA256: strings.Repeat("b", 64),
		}
	}
	return OS{
		ID: id, Family: "debian", VersionID: "13",
		Image: Image{
			URL:    "https://cloud.debian.org/images/cloud/trixie/20261017-2634/debian-13-genericcloud-amd64-20261017-2634.qcow2",
			SHA256: strings.Repeat("a", 64),
		},
		Packages: []Package{
			pkg("docker-ce", "5:29.9.1-1~debian.13~trixie"), pkg("docker-ce-cli", "5:29.9.1-1~debian.13~trixie"),
			pkg("containerd.io", "2.4.2-2~debian.13~trixie"), pkg("tailscale", "1.104.1"),
		},
	}
}

func TestPinChecks(t *testing.T) {
	cases := []struct {
		name string
		edit func(*Pins)
		want string // "" when the pins are valid
	}{
		{"valid", func(*Pins) {}, ""},
		{"current link", func(p *Pins) {
			p.OS[0].Image.URL = "https://cloud.debian.org/images/cloud/trixie/latest/debian-13-genericcloud-amd64.qcow2"
		}, "not a dated release"},
		{"plain http image", func(p *Pins) { p.OS[0].Image.URL = strings.Replace(p.OS[0].Image.URL, "https", "http", 1) }, "not https"},
		{"short image checksum", func(p *Pins) { p.OS[0].Image.SHA256 = "abc" }, "64 lowercase hex"},
		{"unknown family", func(p *Pins) { p.OS[0].Family = "fedora" }, "want ubuntu or debian"},
		{"missing id", func(p *Pins) { p.OS[0].ID = "" }, "id and versionId are required"},
		{"missing package version", func(p *Pins) { p.OS[0].Packages[0].Version = "" }, "not an apt version"},
		{"shell in a version", func(p *Pins) { p.OS[0].Packages[3].Version = "1.0'; reboot; '" }, "not an apt version"},
		{"plain http package", func(p *Pins) {
			p.OS[0].Packages[1].URL = strings.Replace(p.OS[0].Packages[1].URL, "https", "http", 1)
		}, "not an https .deb URL"},
		{"quote in a package URL", func(p *Pins) { p.OS[0].Packages[2].URL = "https://example.org/a'b.deb" }, "not an https .deb URL"},
		{"short package checksum", func(p *Pins) { p.OS[0].Packages[0].SHA256 = "abc" }, "64 lowercase hex"},
		{"missing package", func(p *Pins) { p.OS[0].Packages = p.OS[0].Packages[:3] }, "want [docker-ce docker-ce-cli containerd.io tailscale]"},
		{"packages out of order", func(p *Pins) {
			p.OS[0].Packages[0], p.OS[0].Packages[1] = p.OS[0].Packages[1], p.OS[0].Packages[0]
		}, "want [docker-ce docker-ce-cli containerd.io tailscale]"},
		{"duplicate OS", func(p *Pins) { p.OS = append(p.OS, validOS("debian-13")) }, "listed twice"},
		{"unknown scanner", func(p *Pins) { p.Scanner = "debian-14" }, "names no listed OS"},
	}
	for _, c := range cases {
		p := Pins{Scanner: "debian-13", OS: []OS{validOS("debian-13")}}
		c.edit(&p)
		err := p.check()
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: %v", c.name, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%s: error %v, want one containing %q", c.name, err, c.want)
		}
	}
}

func TestLoadPins(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, content []byte) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	encode := func(p Pins) []byte {
		b, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	want := Pins{Scanner: "debian-13", OS: []OS{validOS("debian-13")}}
	got, err := LoadPins(write("valid.json", encode(want)))
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("LoadPins of a valid file = %+v, %v; want %+v", got, err, want)
	}
	refusals := []struct {
		name    string
		content []byte
		want    string
	}{
		{"malformed.json", []byte("{"), "malformed.json"},
		{"unknown-field.json", []byte(`{"scanner":"debian-13","os":[],"extra":1}`), "unknown field"},
		{"invalid.json", encode(Pins{Scanner: "debian-14", OS: []OS{validOS("debian-13")}}), "names no listed OS"},
		{"trailing-object.json", append(encode(want), []byte(`{}`)...), "trailing content"},
		{"trailing-text.json", append(encode(want), []byte("x")...), "trailing content"},
	}
	for _, r := range refusals {
		if _, err := LoadPins(write(r.name, r.content)); err == nil || !strings.Contains(err.Error(), r.want) {
			t.Errorf("LoadPins(%s): %v, want an error containing %q", r.name, err, r.want)
		}
	}
	if _, err := LoadPins(filepath.Join(dir, "absent.json")); err == nil || !strings.Contains(err.Error(), "absent.json") {
		t.Errorf("LoadPins of a missing file: %v, want an error naming it", err)
	}
}

func TestFind(t *testing.T) {
	p := Pins{Scanner: "debian-13", OS: []OS{{ID: "debian-12"}, {ID: "debian-13"}}}
	if o, err := p.Find("debian-13"); err != nil || o.ID != "debian-13" {
		t.Errorf("Find(debian-13) = %+v, %v", o, err)
	}
	if _, err := p.Find("ubuntu-26.04"); err == nil || !strings.Contains(err.Error(), "debian-12, debian-13") {
		t.Errorf("Find of an unknown OS: %v, want an error listing the pinned ids", err)
	}
}
