// Package dstate renders Docker objects as the normalized state documents the
// docker module's effects journal and compare.
package dstate

import (
	"fmt"
	"time"
)

// Phase says which comparison a document serves.
type Phase int

const (
	// Recorded is the post-state, recorded after the action and compared
	// before an undo.
	Recorded Phase = iota
	// Restored is the state an undo restores, journaled as Before and
	// compared after the inverse.
	Restored
)

func (p Phase) check() error {
	if p != Recorded && p != Restored {
		return fmt.Errorf("dstate: unknown phase %d", int(p))
	}
	return nil
}

// Instant normalizes one Docker timestamp to UTC RFC 3339 with nanoseconds, so
// two spellings of one instant compare equal. It returns "" for "" and for
// Docker's zero time.
func Instant(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return "", fmt.Errorf("dstate: instant %q: %w", s, err)
	}
	if t.IsZero() {
		return "", nil
	}
	return t.UTC().Format(time.RFC3339Nano), nil
}
