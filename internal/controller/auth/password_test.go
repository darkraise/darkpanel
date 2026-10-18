package auth

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// testParams keep argon2id cheap in tests; production uses DefaultParams.
var testParams = Params{Memory: 8, Iterations: 1, Parallelism: 1, SaltLen: 16, KeyLen: 32}

func TestHashAndVerify(t *testing.T) {
	h := newHasher(testParams)
	ctx := context.Background()
	phc, err := h.hash(ctx, "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(phc, "$argon2id$v=19$m=8,t=1,p=1$") {
		t.Fatalf("hash = %q, want a PHC argon2id string with the parameters", phc)
	}
	again, err := h.hash(ctx, "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if again == phc {
		t.Error("two hashes of one password are equal; the salt is not random")
	}
	for _, tt := range []struct {
		password string
		want     bool
	}{
		{"correct horse battery", true},
		{"correct horse batterz", false},
		{"", false},
	} {
		got, err := h.verify(ctx, phc, tt.password)
		if err != nil {
			t.Fatalf("verify(%q): %v", tt.password, err)
		}
		if got != tt.want {
			t.Errorf("verify(%q) = %v, want %v", tt.password, got, tt.want)
		}
	}
}

func TestVerifyReadsParametersFromTheHash(t *testing.T) {
	old := newHasher(Params{Memory: 16, Iterations: 2, Parallelism: 1, SaltLen: 16, KeyLen: 32})
	ctx := context.Background()
	phc, err := old.hash(ctx, "a long password")
	if err != nil {
		t.Fatal(err)
	}
	ok, err := newHasher(testParams).verify(ctx, phc, "a long password")
	if err != nil || !ok {
		t.Fatalf("verify with other default parameters = %v, %v; want true", ok, err)
	}
}

func TestVerifyRefusesMalformedHashes(t *testing.T) {
	h := newHasher(testParams)
	for _, phc := range []string{
		"",
		"$argon2i$v=19$m=8,t=1,p=1$c2FsdHNhbHRzYWx0c2FsdA$a2V5",
		"$argon2id$v=18$m=8,t=1,p=1$c2FsdHNhbHRzYWx0c2FsdA$a2V5",
		"$argon2id$v=19$m=8,t=1$c2FsdHNhbHRzYWx0c2FsdA$a2V5",
		"$argon2id$v=19$m=8,t=0,p=1$c2FsdHNhbHRzYWx0c2FsdA$a2V5",
		"$argon2id$v=19$m=8,t=1,p=1x$c2FsdHNhbHRzYWx0c2FsdA$a2V5",
		"$argon2id$v=19$m=8,t=1,p=1$!!$a2V5",
		"$argon2id$v=19$m=8,t=1,p=1$c2FsdHNhbHRzYWx0c2FsdA$",
	} {
		if ok, err := h.verify(context.Background(), phc, "x"); err == nil || ok {
			t.Errorf("verify(%q) = %v, %v; want false and an error", phc, ok, err)
		}
	}
}

func TestDummyHashRuns(t *testing.T) {
	h := newHasher(testParams)
	var calls atomic.Int32
	inner := h.compute
	h.compute = func(password, salt []byte, p Params) []byte {
		calls.Add(1)
		return inner(password, salt, p)
	}
	if err := h.verifyDummy(context.Background(), "anything"); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Errorf("verifyDummy ran %d hashes, want 1", calls.Load())
	}
}

func TestAtMostTwoHashesAtOnce(t *testing.T) {
	h := newHasher(testParams)
	release := make(chan struct{})
	var running, peak atomic.Int32
	h.compute = func([]byte, []byte, Params) []byte {
		n := running.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		<-release
		running.Add(-1)
		return make([]byte, 32)
	}
	var wg sync.WaitGroup
	for range MaxConcurrentHashes {
		wg.Go(func() { _, _ = h.hash(context.Background(), "a long password") })
	}
	deadline := time.Now().Add(5 * time.Second)
	for running.Load() < MaxConcurrentHashes {
		if time.Now().After(deadline) {
			t.Fatal("the first two hashes did not start")
		}
		time.Sleep(time.Millisecond)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := h.hash(ctx, "a third password")
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("a third hash returned %v while two were running; want it to wait", err)
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("third hash after cancel = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the waiting hash ignored its context")
	}
	close(release)
	wg.Wait()
	if peak.Load() != MaxConcurrentHashes {
		t.Errorf("peak concurrent hashes = %d, want %d", peak.Load(), MaxConcurrentHashes)
	}
}

func TestHeldVariantsTakeNoSlot(t *testing.T) {
	h := newHasher(testParams)
	ctx := context.Background()
	phc, err := h.hash(ctx, "a long password")
	if err != nil {
		t.Fatal(err)
	}
	for range MaxConcurrentHashes {
		if err := h.acquire(ctx); err != nil {
			t.Fatal(err)
		}
	}
	defer func() {
		for range MaxConcurrentHashes {
			h.release()
		}
	}()
	var calls atomic.Int32
	inner := h.compute
	h.compute = func(password, salt []byte, p Params) []byte {
		calls.Add(1)
		return inner(password, salt, p)
	}
	if ok, err := h.verifyHeld(phc, "a long password"); err != nil || !ok {
		t.Errorf("verifyHeld = %v, %v; want true", ok, err)
	}
	if err := h.verifyDummyHeld("a long password"); err != nil {
		t.Errorf("verifyDummyHeld = %v", err)
	}
	if calls.Load() != 2 {
		t.Errorf("held verifications ran %d hashes, want 2", calls.Load())
	}
}

func TestCheckUsername(t *testing.T) {
	for _, tt := range []struct {
		name string
		ok   bool
	}{
		{"alice", true},
		{"0ps.team_a-1", true},
		{strings.Repeat("a", 64), true},
		{strings.Repeat("a", 65), false},
		{"", false},
		{"Alice", false},
		{".alice", false},
		{"-alice", false},
		{"al ice", false},
		{"alice\n", false},
	} {
		err := checkUsername(tt.name)
		if (err == nil) != tt.ok {
			t.Errorf("checkUsername(%q) = %v, want ok=%v", tt.name, err, tt.ok)
		}
		if err != nil {
			wantField(t, err, "/username")
		}
	}
}

func TestCheckPassword(t *testing.T) {
	for _, tt := range []struct {
		password string
		ok       bool
	}{
		{strings.Repeat("a", 11), false},
		{strings.Repeat("a", 12), true},
		{strings.Repeat("é", 12), true},
		{strings.Repeat("a", 128), true},
		{strings.Repeat("a", 129), false},
		{"", false},
	} {
		err := checkPassword(tt.password)
		if (err == nil) != tt.ok {
			t.Errorf("checkPassword(len %d) = %v, want ok=%v", len(tt.password), err, tt.ok)
		}
		if err != nil {
			wantField(t, err, "/password")
			if strings.Contains(err.Error(), tt.password) && tt.password != "" {
				t.Errorf("error %q quotes the password", err)
			}
		}
	}
}

// wantField fails unless err is request.invalid_input with one field error
// at path.
func wantField(t *testing.T, err error, path string) {
	t.Helper()
	var e *errcode.Error
	if !errors.As(err, &e) || e.Code != errcode.InvalidInput {
		t.Fatalf("error = %v, want %s", err, errcode.InvalidInput)
	}
	if len(e.Fields) != 1 || e.Fields[0].Path != path {
		t.Fatalf("fields = %+v, want one at %s", e.Fields, path)
	}
}
