//go:build harness

package harness

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestVMLimiter(t *testing.T) {
	ctx := context.Background()
	l := newVMLimiter(3)
	if err := l.acquire(ctx, 4); err == nil || !strings.Contains(err.Error(), "-vms allows 3") {
		t.Errorf("acquire past the limit: %v, want a refusal naming -vms", err)
	}
	if err := l.acquire(ctx, 2); err != nil {
		t.Fatal(err)
	}
	got := make(chan error, 1)
	go func() { got <- l.acquire(ctx, 2) }()
	select {
	case err := <-got:
		t.Fatalf("a boot past the free slots did not wait: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	l.release(2)
	select {
	case err := <-got:
		if err != nil {
			t.Fatalf("the waiting boot failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the waiting boot did not take the released slots")
	}
	short, cancel := context.WithCancel(ctx)
	cancel()
	if err := l.acquire(short, 2); !errors.Is(err, context.Canceled) {
		t.Errorf("acquire with an ended context: %v, want context.Canceled", err)
	}
	if err := l.acquire(ctx, 1); err != nil {
		t.Errorf("the last free slot: %v", err)
	}
	l.release(3)
	defer func() {
		if recover() == nil {
			t.Error("releasing more than acquired did not panic")
		}
	}()
	l.release(1)
}
