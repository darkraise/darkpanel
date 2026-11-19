package system

import "testing"

func TestDebVersionCompare(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"1.0", "1.0", 0}, {"1.0", "1.1", -1}, {"1:0.9", "2.0", 1}, {"1.0~rc1", "1.0", -1},
		{"1.0-1", "1.0-2", -1}, {"6.1.76-1", "6.1.90-1", -1}, {"5.15.0-1ubuntu1", "5.15.0-1", 1}, {"1.0a", "1.0", 1},
	} {
		if got := debVersionCompare(c.a, c.b); got != c.want {
			t.Errorf("%s vs %s = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}
