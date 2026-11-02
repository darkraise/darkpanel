package artifact

import (
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

const (
	mfHashAMD = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	mfHashARM = "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"

	mfDarkpanel = `"darkpanelVersion":"1.4.0"`
	mfDerper    = `"derperVersion":"v1.104.1"`
	mfArtifacts = `"artifacts":{"amd64":"` + mfHashAMD + `","arm64":"` + mfHashARM + `"}`
)

func mfDoc(members ...string) string { return "{" + strings.Join(members, ",") + "}" }

func mfManifest() Manifest {
	return Manifest{
		DarkpanelVersion: "1.4.0",
		DerperVersion:    "v1.104.1",
		Artifacts:        map[string]string{"amd64": mfHashAMD, "arm64": mfHashARM},
	}
}

func TestMfDerperVersionFileHoldsTheConstant(t *testing.T) {
	b, err := os.ReadFile("../../../../release/derper.version")
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != DerperVersion+"\n" {
		t.Errorf("release/derper.version = %q, want %q followed by one newline", b, DerperVersion)
	}
	if !regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(DerperVersion) {
		t.Errorf("DerperVersion %q is not v<major>.<minor>.<patch>", DerperVersion)
	}
}

func TestMfNames(t *testing.T) {
	if got := BinaryName("v1.104.1", "amd64"); got != "derper-v1.104.1-linux-amd64" {
		t.Errorf("BinaryName amd64 = %q", got)
	}
	if got := BinaryName("v2.0.3", "arm64"); got != "derper-v2.0.3-linux-arm64" {
		t.Errorf("BinaryName arm64 = %q", got)
	}
	if got := PinVersion(); strings.HasPrefix(got, "v") || "v"+got != DerperVersion {
		t.Errorf("PinVersion = %q, want DerperVersion %q without its leading v", got, DerperVersion)
	}
	if !reflect.DeepEqual(Arches, []string{"amd64", "arm64"}) {
		t.Errorf("Arches = %v, want amd64 then arm64", Arches)
	}
	if ManifestName != "derper.json" {
		t.Errorf("ManifestName = %q", ManifestName)
	}
}

func TestMfEncodeIsSortedIndentedJSON(t *testing.T) {
	got, err := mfManifest().Encode()
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n" +
		"  \"darkpanelVersion\": \"1.4.0\",\n" +
		"  \"derperVersion\": \"v1.104.1\",\n" +
		"  \"artifacts\": {\n" +
		"    \"amd64\": \"" + mfHashAMD + "\",\n" +
		"    \"arm64\": \"" + mfHashARM + "\"\n" +
		"  }\n" +
		"}\n"
	if string(got) != want {
		t.Errorf("Encode =\n%s\nwant\n%s", got, want)
	}
	back, err := ParseManifest(got)
	if err != nil || !reflect.DeepEqual(back, mfManifest()) {
		t.Errorf("ParseManifest(Encode) = %+v, %v, want the manifest back", back, err)
	}
}

func TestMfParseManifestAccepts(t *testing.T) {
	tests := []struct {
		name string
		doc  string
		want Manifest
	}{
		{"members in any order, with whitespace", " {\n " + mfArtifacts + ",\n" + mfDerper + ", " + mfDarkpanel + "}\n\n", mfManifest()},
		{"the harness build's version", mfDoc(`"darkpanelVersion":"0.0.0"`, mfDerper, mfArtifacts),
			Manifest{DarkpanelVersion: "0.0.0", DerperVersion: "v1.104.1", Artifacts: mfManifest().Artifacts}},
		{"a prerelease version", mfDoc(`"darkpanelVersion":"1.5.0-beta.1"`, mfDerper, mfArtifacts),
			Manifest{DarkpanelVersion: "1.5.0-beta.1", DerperVersion: "v1.104.1", Artifacts: mfManifest().Artifacts}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseManifest([]byte(tt.doc))
			if err != nil || !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseManifest = %+v, %v, want %+v", got, err, tt.want)
			}
		})
	}
}

func TestMfParseManifestRefuses(t *testing.T) {
	arts := func(members string) string { return `"artifacts":{` + members + `}` }
	amd := `"amd64":"` + mfHashAMD + `"`
	arm := `"arm64":"` + mfHashARM + `"`
	tests := []struct {
		name string
		doc  string
		want string // a substring of the error, which also starts with "artifact:"
	}{
		{"not JSON", `nope`, "artifact:"},
		{"JSON null", `null`, "artifact:"},
		{"an array", `[]`, "artifact:"},
		{"trailing data", mfDoc(mfDarkpanel, mfDerper, mfArtifacts) + ` {}`, "trailing"},
		{"unknown member", mfDoc(mfDarkpanel, mfDerper, mfArtifacts, `"extra":1`), "extra"},
		{"missing darkpanelVersion", mfDoc(mfDerper, mfArtifacts), "darkpanelVersion"},
		{"missing derperVersion", mfDoc(mfDarkpanel, mfArtifacts), "derperVersion"},
		{"missing artifacts", mfDoc(mfDarkpanel, mfDerper), "artifacts"},
		{"null artifacts", mfDoc(mfDarkpanel, mfDerper, `"artifacts":null`), "artifacts"},
		{"darkpanelVersion with a v", mfDoc(`"darkpanelVersion":"v1.4.0"`, mfDerper, mfArtifacts), "darkpanelVersion"},
		{"darkpanelVersion that is not a version", mfDoc(`"darkpanelVersion":"dev"`, mfDerper, mfArtifacts), "darkpanelVersion"},
		{"derperVersion without a v", mfDoc(mfDarkpanel, `"derperVersion":"1.104.1"`, mfArtifacts), "derperVersion"},
		{"derperVersion with two numbers", mfDoc(mfDarkpanel, `"derperVersion":"v1.104"`, mfArtifacts), "derperVersion"},
		{"derperVersion of the wrong type", mfDoc(mfDarkpanel, `"derperVersion":5`, mfArtifacts), "derperVersion"},
		{"an architecture outside Arches", mfDoc(mfDarkpanel, mfDerper, arts(amd+`,`+arm+`,"riscv64":"`+mfHashAMD+`"`)), "riscv64"},
		{"arm64 missing", mfDoc(mfDarkpanel, mfDerper, arts(amd)), "arm64"},
		{"amd64 missing", mfDoc(mfDarkpanel, mfDerper, arts(arm)), "amd64"},
		{"no artifacts at all", mfDoc(mfDarkpanel, mfDerper, arts("")), "amd64"},
		{"an upper-case hash", mfDoc(mfDarkpanel, mfDerper, arts(`"amd64":"`+strings.ToUpper(mfHashAMD)+`",`+arm)), "amd64"},
		{"a short hash", mfDoc(mfDarkpanel, mfDerper, arts(amd+`,"arm64":"`+mfHashARM[:63]+`"`)), "arm64"},
		{"a hash that is not a string", mfDoc(mfDarkpanel, mfDerper, arts(amd+`,"arm64":7`)), "artifacts"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseManifest([]byte(tt.doc))
			if err == nil {
				t.Fatalf("ParseManifest accepted %s as %+v", tt.doc, got)
			}
			if !strings.HasPrefix(err.Error(), "artifact:") || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("ParseManifest error %q, want the prefix \"artifact:\" and a mention of %q", err, tt.want)
			}
		})
	}
}

func TestMfEncodeValidatesTheManifest(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Manifest)
		want   string
	}{
		{"empty darkpanel version", func(m *Manifest) { m.DarkpanelVersion = "" }, "darkpanelVersion"},
		{"darkpanel version with a v", func(m *Manifest) { m.DarkpanelVersion = "v1.4.0" }, "darkpanelVersion"},
		{"derper version without a v", func(m *Manifest) { m.DerperVersion = "1.104.1" }, "derperVersion"},
		{"missing arch", func(m *Manifest) { delete(m.Artifacts, "arm64") }, "arm64"},
		{"extra arch", func(m *Manifest) { m.Artifacts["s390x"] = mfHashAMD }, "s390x"},
		{"bad hash", func(m *Manifest) { m.Artifacts["amd64"] = "xyz" }, "amd64"},
		{"nil artifacts", func(m *Manifest) { m.Artifacts = nil }, "amd64"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := mfManifest()
			tt.mutate(&m)
			b, err := m.Encode()
			if err == nil {
				t.Fatalf("Encode accepted %+v and wrote %s", m, b)
			}
			if b != nil || !strings.HasPrefix(err.Error(), "artifact:") || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Encode = %q, %v, want no bytes and an \"artifact:\" error mentioning %q", b, err, tt.want)
			}
		})
	}
}
