package v2

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func newTestLimiter(t *testing.T) (*PasswordRateLimiter, *fakeClock) {
	t.Helper()
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	return &PasswordRateLimiter{
		store: newMemoryAttemptsStore(),
		now:   clock.Now,
	}, clock
}

type fakeClock struct {
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	return c.now
}

func (c *fakeClock) advance(d time.Duration) {
	c.now = c.now.Add(d)
}

// First few failures do not trigger a block.
func TestLimiter_SoftThresholdAllowsRetries(t *testing.T) {
	lim, _ := newTestLimiter(t)

	for i := 0; i < softFailureThreshold-1; i++ {
		retry, err := lim.Check("repo", "actor")
		if err != nil || retry != 0 {
			t.Fatalf("expected no block before threshold, got retry=%v err=%v", retry, err)
		}
		if err := lim.RecordFailure("repo", "actor"); err != nil {
			t.Fatalf("RecordFailure: %v", err)
		}
	}

	// Still allowed right before the threshold trip.
	if retry, err := lim.Check("repo", "actor"); err != nil || retry != 0 {
		t.Fatalf("pre-threshold Check should allow, got retry=%v err=%v", retry, err)
	}
}

// At the threshold, RecordFailure must install a block window.
func TestLimiter_BlocksAfterThreshold(t *testing.T) {
	lim, _ := newTestLimiter(t)

	for i := 0; i < softFailureThreshold; i++ {
		if err := lim.RecordFailure("repo", "actor"); err != nil {
			t.Fatalf("RecordFailure: %v", err)
		}
	}

	retry, err := lim.Check("repo", "actor")
	if !errors.Is(err, ErrPasswordRateLimited) {
		t.Fatalf("expected ErrPasswordRateLimited, got %v", err)
	}
	if retry < time.Second || retry > initialBlockWindow+time.Second {
		t.Fatalf("retryAfter %v not within first block window", retry)
	}
}

// Each additional threshold-count of failures doubles the block until the cap.
func TestLimiter_ExponentialBackoff(t *testing.T) {
	lim, clock := newTestLimiter(t)

	// 5 → 1min, 10 → 2min, 15 → 4min, 20 → 8min, 25 → 16min, 30+ → cap 30min.
	type step struct {
		failures int
		wantMin  time.Duration
	}
	steps := []step{
		{softFailureThreshold, initialBlockWindow},
		{softFailureThreshold * 2, initialBlockWindow << 1},
		{softFailureThreshold * 3, initialBlockWindow << 2},
		{softFailureThreshold * 6, maxBlockWindow},
	}

	count := 0
	for _, s := range steps {
		for count < s.failures {
			// Advance past any existing block so RecordFailure isn't itself gated;
			// RecordFailure never checks the block state, but Check does — we only
			// need a stable clock here.
			if err := lim.RecordFailure("repo", "actor"); err != nil {
				t.Fatalf("RecordFailure: %v", err)
			}
			count++
		}

		// Step time forward to a moment after the most recent block would have
		// started, so Check sees the freshly-set blockedUntil.
		clock.advance(time.Millisecond)

		retry, err := lim.Check("repo", "actor")
		if !errors.Is(err, ErrPasswordRateLimited) {
			t.Fatalf("failures=%d: expected limiter active, got err=%v", count, err)
		}
		if retry < s.wantMin-time.Second {
			t.Fatalf("failures=%d: retryAfter=%v want >= ~%v", count, retry, s.wantMin)
		}
		if retry > maxBlockWindow+time.Second {
			t.Fatalf("failures=%d: retryAfter=%v exceeds cap %v", count, retry, maxBlockWindow)
		}
	}
}

// A successful attempt wipes prior failures — the next wrong password starts
// from zero instead of inheriting the old counter.
func TestLimiter_SuccessResetsCounter(t *testing.T) {
	lim, _ := newTestLimiter(t)

	for i := 0; i < softFailureThreshold; i++ {
		_ = lim.RecordFailure("repo", "actor")
	}
	if err := lim.RecordSuccess("repo", "actor"); err != nil {
		t.Fatalf("RecordSuccess: %v", err)
	}

	if retry, err := lim.Check("repo", "actor"); err != nil || retry != 0 {
		t.Fatalf("after reset Check should allow, got retry=%v err=%v", retry, err)
	}
}

// Block expires cleanly when the clock advances past blockedUntil.
func TestLimiter_BlockExpires(t *testing.T) {
	lim, clock := newTestLimiter(t)

	for i := 0; i < softFailureThreshold; i++ {
		_ = lim.RecordFailure("repo", "actor")
	}

	clock.advance(initialBlockWindow + time.Second)

	if retry, err := lim.Check("repo", "actor"); err != nil || retry != 0 {
		t.Fatalf("block should have expired, got retry=%v err=%v", retry, err)
	}
}

// A nil receiver is safe (used during bootstrap when DB is not ready).
func TestLimiter_NilReceiverIsSafe(t *testing.T) {
	var lim *PasswordRateLimiter

	if retry, err := lim.Check("r", "a"); err != nil || retry != 0 {
		t.Fatalf("nil Check should no-op, got retry=%v err=%v", retry, err)
	}
	if err := lim.RecordFailure("r", "a"); err != nil {
		t.Fatalf("nil RecordFailure should no-op, got %v", err)
	}
	if err := lim.RecordSuccess("r", "a"); err != nil {
		t.Fatalf("nil RecordSuccess should no-op, got %v", err)
	}
}

// Different actors against the same repo have independent counters — one
// abusive IP must not lock out everyone else.
func TestLimiter_ActorsAreIndependent(t *testing.T) {
	lim, _ := newTestLimiter(t)

	for i := 0; i < softFailureThreshold; i++ {
		_ = lim.RecordFailure("repo", "attacker")
	}

	if _, err := lim.Check("repo", "attacker"); !errors.Is(err, ErrPasswordRateLimited) {
		t.Fatalf("attacker should be limited, got %v", err)
	}
	if retry, err := lim.Check("repo", "victim"); err != nil || retry != 0 {
		t.Fatalf("unrelated actor should not be limited, got retry=%v err=%v", retry, err)
	}
}

// Concurrent failures must not lose increments; otherwise a distributed
// attacker can undercount attempts by racing requests.
func TestLimiter_ConcurrentFailuresAreNotLost(t *testing.T) {
	lim, _ := newTestLimiter(t)

	const attempts = 20
	var wg sync.WaitGroup
	wg.Add(attempts)

	for i := 0; i < attempts; i++ {
		go func() {
			defer wg.Done()
			if err := lim.RecordFailure("repo", "actor"); err != nil {
				t.Errorf("RecordFailure: %v", err)
			}
		}()
	}
	wg.Wait()

	failures, blockedUntil, err := lim.store.Load("repo", "actor")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if failures != attempts {
		t.Fatalf("failures = %d, want %d", failures, attempts)
	}
	if blockedUntil.IsZero() {
		t.Fatal("expected limiter to be active after concurrent failures")
	}
}
