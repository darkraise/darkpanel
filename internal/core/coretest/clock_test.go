package coretest

import (
	"testing"
	"time"
)

var start = time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)

func TestFakeClockFiresInOrder(t *testing.T) {
	c := NewFakeClock(start)
	var got []string
	var seen []time.Time
	record := func(name string) func() {
		return func() { got = append(got, name); seen = append(seen, c.Now()) }
	}
	c.AfterFunc(30*time.Second, record("b"))
	c.AfterFunc(10*time.Second, record("a"))
	c.AfterFunc(30*time.Second, record("c"))
	c.AfterFunc(2*time.Minute, record("late"))

	c.Advance(time.Minute)
	if want := []string{"a", "b", "c"}; len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("fired %v, want %v", got, want)
	}
	if !seen[0].Equal(start.Add(10*time.Second)) || !seen[1].Equal(start.Add(30*time.Second)) {
		t.Errorf("Now inside timers = %v, want each timer's deadline", seen)
	}
	if !c.Now().Equal(start.Add(time.Minute)) {
		t.Errorf("Now = %v, want start+1m", c.Now())
	}
	if c.Pending() != 1 {
		t.Errorf("Pending = %d, want 1", c.Pending())
	}
}

func TestFakeClockStop(t *testing.T) {
	c := NewFakeClock(start)
	timer := c.AfterFunc(time.Second, func() { t.Error("stopped timer fired") })
	if !timer.Stop() {
		t.Error("first Stop() = false, want true")
	}
	if timer.Stop() {
		t.Error("second Stop() = true, want false")
	}
	c.Advance(time.Minute)

	fired := c.AfterFunc(time.Second, func() {})
	c.Advance(time.Second)
	if fired.Stop() {
		t.Error("Stop() after firing = true, want false")
	}
}

func TestFakeClockTimerArmedByTimer(t *testing.T) {
	c := NewFakeClock(start)
	n := 0
	c.AfterFunc(time.Second, func() {
		n++
		c.AfterFunc(time.Second, func() { n++ })
	})
	c.Advance(5 * time.Second)
	if n != 2 {
		t.Errorf("fired %d timers, want 2 (the second was armed inside the first)", n)
	}
}

func TestFakeClockZeroAdvanceFiresDue(t *testing.T) {
	c := NewFakeClock(start)
	fired := false
	c.AfterFunc(0, func() { fired = true })
	if fired {
		t.Fatal("timer fired before Advance")
	}
	c.Advance(0)
	if !fired {
		t.Error("Advance(0) did not fire a timer due now")
	}
}
