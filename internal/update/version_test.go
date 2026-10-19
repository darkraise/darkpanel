package update

import (
	"errors"
	"reflect"
	"testing"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

func TestParseVersion(t *testing.T) {
	tests := []struct {
		in   string
		want Version
		ok   bool
	}{
		{"1.2.3", Version{Major: 1, Minor: 2, Patch: 3}, true},
		{"0.0.0", Version{}, true},
		{"1.3.0-beta.1", Version{Major: 1, Minor: 3, Pre: "beta.1"}, true},
		{"10.20.30-rc-1.x.07", Version{Major: 10, Minor: 20, Patch: 30, Pre: "rc-1.x.07"}, true},
		{"v1.2.3", Version{}, false},
		{"1.2", Version{}, false},
		{"01.2.3", Version{}, false},
		{"1.2.3+build", Version{}, false},
		{"1.2.3-", Version{}, false},
		{"1.2.3-beta..1", Version{}, false},
		{"dev", Version{}, false},
		{"99999999999999999999.0.0", Version{}, false},
	}
	for _, tt := range tests {
		got, err := ParseVersion(tt.in)
		if (err == nil) != tt.ok {
			t.Errorf("ParseVersion(%q) error = %v, want ok %v", tt.in, err, tt.ok)
			continue
		}
		if tt.ok && got != tt.want {
			t.Errorf("ParseVersion(%q) = %+v, want %+v", tt.in, got, tt.want)
		}
		if tt.ok && got.String() != tt.in {
			t.Errorf("ParseVersion(%q).String() = %q", tt.in, got.String())
		}
	}
}

func TestVersionCompare(t *testing.T) {
	// Each version has lower precedence than the next (semver 2.0.0 section 11).
	ordered := []string{
		"1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta",
		"1.0.0-beta.2", "1.0.0-beta.11", "1.0.0-rc.1", "1.0.0",
		"1.0.1", "1.2.0-beta.1", "1.2.0", "1.10.0", "2.0.0",
	}
	for i, x := range ordered {
		for j, y := range ordered {
			a, b := mustVersion(t, x), mustVersion(t, y)
			want := 0
			switch {
			case i < j:
				want = -1
			case i > j:
				want = 1
			}
			if got := a.Compare(b); got != want {
				t.Errorf("%s.Compare(%s) = %d, want %d", a, b, got, want)
			}
		}
	}
	if got := mustVersion(t, "1.0.0-beta.007").Compare(mustVersion(t, "1.0.0-beta.7")); got != 0 {
		t.Errorf("numeric identifiers with leading zeros compare %d, want 0", got)
	}
	long := mustVersion(t, "1.0.0-123456789012345678901234567890")
	if got := long.Compare(mustVersion(t, "1.0.0-99")); got != 1 {
		t.Errorf("a numeric identifier above uint64 compares %d, want 1", got)
	}
}

func mustVersion(t *testing.T, s string) Version {
	t.Helper()
	v, err := ParseVersion(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

var stepReleases = []Release{
	{Version: "1.5.0", Channel: "stable"},
	{Version: "1.2.0", Channel: "stable"},
	{Version: "1.2.4", Channel: "stable"},
	{Version: "1.3.0", Channel: "stable"},
	{Version: "1.3.2", Channel: "stable"},
	{Version: "1.3.3-beta.1", Channel: "beta"},
	{Version: "1.4.1", Channel: "stable"},
	{Version: "1.4.0", Channel: "stable"},
	{Version: "1.6.0-beta.1", Channel: "beta"},
	{Version: "1.8.0", Channel: "stable"},
	{Version: "2.0.0", Channel: "stable"},
	{Version: "not-a-version", Channel: "stable"},
	{Version: "1.9.0", Channel: "nightly"},
}

func TestInChannel(t *testing.T) {
	versions := func(rs []Release) []string {
		var out []string
		for _, r := range rs {
			out = append(out, r.Version)
		}
		return out
	}
	stable := []string{"1.2.0", "1.2.4", "1.3.0", "1.3.2", "1.4.0", "1.4.1", "1.5.0", "1.8.0", "2.0.0"}
	beta := []string{"1.2.0", "1.2.4", "1.3.0", "1.3.2", "1.3.3-beta.1", "1.4.0", "1.4.1", "1.5.0", "1.6.0-beta.1", "1.8.0", "2.0.0"}
	if got := versions(InChannel(stepReleases, "stable")); !reflect.DeepEqual(got, stable) {
		t.Errorf("stable = %v, want %v", got, stable)
	}
	if got := versions(InChannel(stepReleases, "beta")); !reflect.DeepEqual(got, beta) {
		t.Errorf("beta = %v, want %v", got, beta)
	}
	if got := InChannel(stepReleases, "nightly"); len(got) != 0 {
		t.Errorf("unknown channel = %v, want none", got)
	}
}

func TestSteps(t *testing.T) {
	tests := []struct {
		name            string
		current, target string
		channel         string
		force           bool
		want            []string
		code            errcode.Code
	}{
		{"one minor at a time", "1.2.0", "1.5.0", "stable", false, []string{"1.3.2", "1.4.1", "1.5.0"}, ""},
		{"beta steps through the newest beta", "1.2.0", "1.4.1", "beta", false, []string{"1.3.3-beta.1", "1.4.1"}, ""},
		{"patch of the same minor", "1.2.0", "1.2.4", "stable", false, []string{"1.2.4"}, ""},
		{"next minor", "1.4.0", "1.5.0", "stable", false, []string{"1.5.0"}, ""},
		{"beta release on a beta host", "1.5.0", "1.6.0-beta.1", "beta", false, []string{"1.6.0-beta.1"}, ""},
		{"prerelease to its release", "1.3.3-beta.1", "1.4.0", "beta", false, []string{"1.4.0"}, ""},
		{"forced downgrade of one minor", "1.5.0", "1.4.0", "stable", true, []string{"1.4.0"}, ""},
		{"forced downgrade of a patch", "1.2.4", "1.2.0", "stable", true, []string{"1.2.0"}, ""},
		{"forced downgrade of two minors", "1.5.0", "1.3.2", "stable", true, nil, errcode.UpdateDowngradeRefused},
		{"unforced downgrade", "1.5.0", "1.4.1", "stable", false, nil, errcode.UpdateDowngradeRefused},
		{"missing intermediate minor", "1.5.0", "1.8.0", "stable", false, nil, errcode.UpdateNotFound},
		{"target not in the channel", "1.5.0", "1.6.0-beta.1", "stable", false, nil, errcode.UpdateNotFound},
		{"target not published", "1.5.0", "1.5.9", "stable", false, nil, errcode.UpdateNotFound},
		{"another major", "1.8.0", "2.0.0", "stable", false, nil, errcode.InvalidInput},
		{"the running version", "1.5.0", "1.5.0", "stable", false, nil, errcode.InvalidInput},
		{"a development build", "dev", "1.5.0", "stable", false, nil, errcode.InvalidInput},
		{"an unknown channel", "1.2.0", "1.2.4", "nightly", false, nil, errcode.InvalidInput},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Steps(tt.current, tt.target, tt.channel, stepReleases, tt.force)
			if tt.code != "" {
				var ce *errcode.Error
				if !errors.As(err, &ce) || ce.Code != tt.code {
					t.Fatalf("Steps = %v, %v; want %s", got, err, tt.code)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var versions []string
			for _, r := range got {
				versions = append(versions, r.Version)
			}
			if !reflect.DeepEqual(versions, tt.want) {
				t.Errorf("Steps = %v, want %v", versions, tt.want)
			}
		})
	}
}

func TestStepsKeepTheEntryChannel(t *testing.T) {
	got, err := Steps("1.2.0", "1.4.1", "beta", stepReleases, false)
	if err != nil {
		t.Fatal(err)
	}
	want := []Release{{Version: "1.3.3-beta.1", Channel: "beta"}, {Version: "1.4.1", Channel: "stable"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Steps = %+v, want %+v", got, want)
	}
}
