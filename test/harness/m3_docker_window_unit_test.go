//go:build harness

package harness

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// v1r24At is the time of the attempt that starts sec seconds into a fixed minute.
func v1r24At(sec int) time.Time {
	return time.Date(2026, time.November, 6, 14, 42, sec, 500, time.UTC)
}

// v1r24Line is one probe log line of port with a start time.
func v1r24Line(port int, result string, sec int) string {
	return strconv.Itoa(port) + " " + result + " " + strconv.FormatInt(v1r24At(sec).UnixNano(), 10)
}

func TestV1r24UnitProbeWindows(t *testing.T) {
	timed := strings.Join([]string{
		v1r24Line(18209, "fail", 1), v1r24Line(18210, "fail", 1),
		v1r24Line(18209, "ok", 2), v1r24Line(18210, "fail", 2),
		v1r24Line(18209, "ok", 3), v1r24Line(18210, "ok", 3),
		v1r24Line(18209, "fail", 4), v1r24Line(18210, "fail", 4),
		v1r24Line(18209, "ok", 5), v1r24Line(18210, "fail", 5),
		"",
	}, "\n")
	for _, tc := range []struct {
		name string
		log  string
		want []v1r24Window
		bad  string // "" accepts
	}{
		{"two ports interleaved", timed, []v1r24Window{
			{Port: 18209, Attempts: 2, First: v1r24At(2), Last: v1r24At(3)},
			{Port: 18209, Attempts: 1, First: v1r24At(5), Last: v1r24At(5)},
			{Port: 18210, Attempts: 1, First: v1r24At(3), Last: v1r24At(3)},
		}, ""},
		{"a log without times", "18209 ok\n18209 ok\n18209 fail\n18209 ok\n", []v1r24Window{
			{Port: 18209, Attempts: 2},
			{Port: 18209, Attempts: 1},
		}, ""},
		{"nothing connected", "18209 fail\n18209 fail\n", nil, ""},
		{"a time that is not a number", "18209 ok soon\n", nil, "malformed"},
		{"a fourth field", "18209 ok 12 extra\n", nil, "malformed"},
		{"an unknown result", "18209 maybe 12\n", nil, "malformed"},
	} {
		got, err := v1r24Windows(tc.log)
		if tc.bad != "" {
			if err == nil || !strings.Contains(err.Error(), tc.bad) {
				t.Errorf("%s: %v, want an error naming %q", tc.name, err, tc.bad)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if len(got) != len(tc.want) {
			t.Errorf("%s: windows %+v, want %+v", tc.name, got, tc.want)
			continue
		}
		for i, w := range tc.want {
			g := got[i]
			if g.Port != w.Port || g.Attempts != w.Attempts || !g.First.Equal(w.First) || !g.Last.Equal(w.Last) {
				t.Errorf("%s: window %d is %+v, want %+v", tc.name, i, g, w)
			}
		}
	}
}

// TestV1r24UnitLeakNamesTheWindows: a leak reported by m3dxTrafficLeaks names
// each window of the leaking port with its attempts and, when the log carries
// times, its start and end, so the failure can be placed against the agent's
// journal. A log of two-field lines gives the same error without times.
func TestV1r24UnitLeakNamesTheWindows(t *testing.T) {
	var timed, plain strings.Builder
	for i := range 23 {
		result := "fail"
		if i >= 15 && i <= 17 {
			result = "ok"
		}
		timed.WriteString(v1r24Line(18209, result, i) + "\n")
		plain.WriteString("18209 " + result + "\n")
	}

	err := m3dxTrafficLeaks(timed.String(), []uint16{18209}, 20)
	if err == nil {
		t.Fatal("m3dxTrafficLeaks accepted a log with a leak")
	}
	first, last := v1r24At(15).Format(time.RFC3339Nano), v1r24At(17).Format(time.RFC3339Nano)
	for _, want := range []string{"port 18209 connected in 3 of 23 attempts", "3 attempts from " + first + " to " + last} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}

	err = m3dxTrafficLeaks(plain.String(), []uint16{18209}, 20)
	if err == nil || !strings.Contains(err.Error(), "port 18209 connected in 3 of 23 attempts") || !strings.Contains(err.Error(), "3 attempts") || strings.Contains(err.Error(), " from ") {
		t.Errorf("plain log: %v, want the count and a window of 3 attempts without times", err)
	}
	if err := m3dxTrafficProbed(timed.String(), []uint16{18209}, 20); err != nil {
		t.Errorf("a timed log with 23 attempts: %v, want nil", err)
	}
}
