package dstate

import (
	"encoding/json"
	"net/netip"
	"testing"

	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

func dsText(t *testing.T, raw json.RawMessage, err error) string {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return string(raw)
}

func dsContainer(running bool, startedAt, finishedAt string) dockerapi.ContainerJSON {
	return dockerapi.ContainerJSON{
		ID:      "c1",
		Created: "2026-10-01T02:00:00.123456789+02:00",
		State:   dockerapi.ContainerState{Running: running, StartedAt: startedAt, FinishedAt: finishedAt},
	}
}

func TestDsInstant(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"0001-01-01T00:00:00Z", ""},
		{"2026-10-23T09:15:03Z", "2026-10-23T09:15:03Z"},
		{"2026-10-23T09:15:03.123456789Z", "2026-10-23T09:15:03.123456789Z"},
		{"2026-10-23T11:15:03.500+02:00", "2026-10-23T09:15:03.5Z"},
	}
	for _, tc := range cases {
		got, err := Instant(tc.in)
		if err != nil || got != tc.want {
			t.Errorf("Instant(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
		}
	}
	if got, err := Instant("yesterday"); err == nil {
		t.Errorf("Instant(%q) = %q, want an error", "yesterday", got)
	}
}

func TestDsContainerRun(t *testing.T) {
	cases := []struct {
		name  string
		c     dockerapi.ContainerJSON
		phase Phase
		want  string
	}{
		{
			"recorded running", dsContainer(true, "2026-10-23T11:15:03.500+02:00", "2026-10-22T08:00:00Z"), Recorded,
			`{"id":"c1","created":"2026-10-01T00:00:00.123456789Z","running":true,"startedAt":"2026-10-23T09:15:03.5Z","finishedAt":"2026-10-22T08:00:00Z"}`,
		},
		{
			"recorded never ran", dsContainer(false, "0001-01-01T00:00:00Z", ""), Recorded,
			`{"id":"c1","created":"2026-10-01T00:00:00.123456789Z","running":false}`,
		},
		{
			"restored leaves out the start and finish", dsContainer(true, "2026-10-23T09:15:03.5Z", "2026-10-22T08:00:00Z"), Restored,
			`{"id":"c1","created":"2026-10-01T00:00:00.123456789Z","running":true}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := ContainerRun(tc.c, tc.phase)
			if got := dsText(t, raw, err); got != tc.want {
				t.Errorf("got  %s\nwant %s", got, tc.want)
			}
		})
	}
}

func TestDsContainerRunRejects(t *testing.T) {
	if raw, err := ContainerRun(dsContainer(true, "", ""), Phase(9)); err == nil {
		t.Errorf("an unknown phase gave %s, want an error", raw)
	}
	bad := dsContainer(true, "not a time", "")
	if raw, err := ContainerRun(bad, Recorded); err == nil {
		t.Errorf("an unparseable startedAt gave %s, want an error", raw)
	}
}

func TestDsContainerRunningMatchesRestored(t *testing.T) {
	c := dsContainer(true, "2026-10-23T09:15:03.5Z", "")
	want, err := ContainerRun(c, Restored)
	got, err2 := ContainerRunning("c1", "2026-10-01T02:00:00.123456789+02:00", true)
	if err != nil || err2 != nil {
		t.Fatalf("errors: %v, %v", err, err2)
	}
	if string(got) != string(want) {
		t.Errorf("ContainerRunning = %s, want %s", got, want)
	}
	stopped, err := ContainerRunning("c1", "2026-10-01T02:00:00.123456789+02:00", false)
	if err != nil || string(stopped) == string(got) {
		t.Errorf("a stopped container renders %s, %v; it must differ from %s", stopped, err, got)
	}
	if raw, err := ContainerRunning("c1", "not a time", true); err == nil {
		t.Errorf("an unparseable created gave %s, want an error", raw)
	}
}

func TestDsEndpoint(t *testing.T) {
	full := &dockerapi.EndpointSettings{
		EndpointID: "e1",
		Aliases:    []string{"web", "abc", "web"},
		IPAMConfig: &dockerapi.EndpointIPAMConfig{
			IPv4Address: netip.MustParseAddr("172.20.0.5"),
			IPv6Address: netip.MustParseAddr("FD00:0:0:0:0:0:0:5"),
		},
	}
	bare := &dockerapi.EndpointSettings{
		EndpointID: "e1",
		Aliases:    []string{},
		IPAMConfig: &dockerapi.EndpointIPAMConfig{},
	}
	v4Only := &dockerapi.EndpointSettings{
		IPAMConfig: &dockerapi.EndpointIPAMConfig{IPv4Address: netip.MustParseAddr("172.20.0.5")},
	}
	cases := []struct {
		name  string
		ep    *dockerapi.EndpointSettings
		phase Phase
		want  string
	}{
		{
			"recorded", full, Recorded,
			`{"container":"c1","network":"n1","present":true,"endpointId":"e1","aliases":["abc","web"],"ipamConfig":{"ipv4":"172.20.0.5","ipv6":"fd00::5"}}`,
		},
		{
			"restored leaves out the endpoint id", full, Restored,
			`{"container":"c1","network":"n1","present":true,"aliases":["abc","web"],"ipamConfig":{"ipv4":"172.20.0.5","ipv6":"fd00::5"}}`,
		},
		{"empty aliases and addresses are absent", bare, Recorded, `{"container":"c1","network":"n1","present":true,"endpointId":"e1"}`},
		{"one address", v4Only, Restored, `{"container":"c1","network":"n1","present":true,"ipamConfig":{"ipv4":"172.20.0.5"}}`},
		{"absent, recorded", nil, Recorded, `{"container":"c1","network":"n1","present":false}`},
		{"absent, restored", nil, Restored, `{"container":"c1","network":"n1","present":false}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := Endpoint("c1", "n1", tc.ep, tc.phase)
			if got := dsText(t, raw, err); got != tc.want {
				t.Errorf("got  %s\nwant %s", got, tc.want)
			}
		})
	}
	if raw, err := Endpoint("c1", "n1", full, Phase(9)); err == nil {
		t.Errorf("an unknown phase gave %s, want an error", raw)
	}
}

func TestDsNetworkCreated(t *testing.T) {
	raw, err := NetworkCreated(nil)
	if got := dsText(t, raw, err); got != "null" {
		t.Errorf("NetworkCreated(nil) = %s, want null", got)
	}
	used := &dockerapi.Network{
		ID:         "n1",
		Created:    "2026-10-23T11:15:00+02:00",
		Labels:     map[string]string{"b": "2", "a": "1"},
		Containers: map[string]dockerapi.NetworkContainer{"c1": {Name: "web"}},
	}
	raw, err = NetworkCreated(used)
	want := `{"id":"n1","created":"2026-10-23T09:15:00Z","labels":{"a":"1","b":"2"},"unused":false}`
	if got := dsText(t, raw, err); got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	for _, n := range []*dockerapi.Network{
		{ID: "n1", Created: "2026-10-23T09:15:00Z"},
		{ID: "n1", Created: "2026-10-23T09:15:00Z", Labels: map[string]string{}, Containers: map[string]dockerapi.NetworkContainer{}},
	} {
		raw, err = NetworkCreated(n)
		want = `{"id":"n1","created":"2026-10-23T09:15:00Z","unused":true}`
		if got := dsText(t, raw, err); got != want {
			t.Errorf("got  %s\nwant %s", got, want)
		}
	}
	if raw, err := NetworkCreated(&dockerapi.Network{ID: "n1", Created: "soon"}); err == nil {
		t.Errorf("an unparseable created gave %s, want an error", raw)
	}
}

func TestDsVolumeCreated(t *testing.T) {
	raw, err := VolumeCreated(nil, true)
	if got := dsText(t, raw, err); got != "null" {
		t.Errorf("VolumeCreated(nil) = %s, want null", got)
	}
	raw, err = VolumeCreated(&dockerapi.Volume{Name: "data", Labels: map[string]string{"x": "y"}}, true)
	want := `{"name":"data","labels":{"x":"y"},"unused":true}`
	if got := dsText(t, raw, err); got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	for _, labels := range []map[string]string{nil, {}} {
		raw, err = VolumeCreated(&dockerapi.Volume{Name: "data", Labels: labels}, false)
		want = `{"name":"data","unused":false}`
		if got := dsText(t, raw, err); got != want {
			t.Errorf("got  %s\nwant %s", got, want)
		}
	}
}
