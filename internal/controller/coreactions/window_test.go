package coreactions

import "testing"

func TestSupported(t *testing.T) {
	cases := []struct {
		controller, agent string
		protocol          int
		want              bool
	}{
		{"1.3.0", "1.3.0", 1, true},
		{"1.3.0", "1.3.7", 1, true},
		{"1.3.2", "1.3.0", 1, true},
		{"1.3.0", "1.2.9", 1, true},
		{"1.3.0", "1.3.0-beta.1", 1, true},
		{"1.3.0-beta.2", "1.2.0", 1, true},
		{"1.3.0", "1.1.0", 1, false},
		{"1.3.0", "1.4.0", 1, false},
		{"1.3.0", "2.3.0", 1, false},
		{"2.0.0", "1.9.0", 1, false},
		{"1.0.0", "0.9.0", 1, false},
		{"1.3.0", "1.3.0", 2, false},
		{"1.3.0", "1.3.0", 0, false},
		{"dev", "dev", 1, true},
		{"dev", "1.3.0", 1, false},
		{"1.3.0", "dev", 1, false},
		{"1.3.0", "", 1, false},
		{"dev", "dev", 2, false},
	}
	for _, c := range cases {
		if got := Supported(c.controller, c.agent, c.protocol); got != c.want {
			t.Errorf("Supported(%q, %q, %d) = %t, want %t", c.controller, c.agent, c.protocol, got, c.want)
		}
	}
}
