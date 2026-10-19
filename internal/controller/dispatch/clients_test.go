package dispatch

import (
	"context"
	"sync"
	"testing"

	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

func TestStaticClientsUnknownHostIsOffline(t *testing.T) {
	_, _, err := NewStaticClients().Client(context.Background(), "h9")
	e := wantCode(t, err, errcode.HostOffline)
	if e.Detail != "host h9 is offline" {
		t.Errorf("detail = %q", e.Detail)
	}
}

func TestStaticClientsGenerations(t *testing.T) {
	ctx := context.Background()
	s := NewStaticClients()
	a, b := &coretest.FakeAgent{}, &coretest.FakeAgent{}
	s.Add("h1", a)
	c, gen1, err := s.Client(ctx, "h1")
	if err != nil || c != a || gen1 == 0 {
		t.Fatalf("Client(h1) = %v, %d, %v; want a with a positive generation", c, gen1, err)
	}
	if _, again, _ := s.Client(ctx, "h1"); again != gen1 {
		t.Errorf("generation changed without Add: %d, then %d", gen1, again)
	}
	s.Add("h1", b)
	c, gen2, err := s.Client(ctx, "h1")
	if err != nil || c != b || gen2 == gen1 {
		t.Errorf("after Add: %v, %d, %v; want b with a new generation (old %d)", c, gen2, err, gen1)
	}
	s.Add("h2", a)
	if _, gen3, _ := s.Client(ctx, "h2"); gen3 == gen1 || gen3 == gen2 {
		t.Errorf("h2 generation %d repeats %d or %d", gen3, gen1, gen2)
	}
	// Generations never repeat across sources either.
	other := NewStaticClients()
	other.Add("h1", a)
	if _, gen4, _ := other.Client(ctx, "h1"); gen4 == gen1 || gen4 == gen2 {
		t.Errorf("a second source repeated generation %d", gen4)
	}
}

func TestStaticClientsConcurrentUse(t *testing.T) {
	ctx := context.Background()
	s := NewStaticClients()
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 100 {
				s.Add("h1", &coretest.FakeAgent{})
				if _, _, err := s.Client(ctx, "h1"); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	wg.Wait()
}
