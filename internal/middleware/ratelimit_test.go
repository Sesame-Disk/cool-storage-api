package middleware

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

func TestRateLimiterReservationCancellationRestoresAdmission(t *testing.T) {
	limiter := NewRateLimiter(rate.Every(time.Minute), 1)
	t.Cleanup(limiter.Stop)
	now := time.Now()

	reservation := limiter.TryReserve("client", now)
	if reservation == nil {
		t.Fatal("first reservation was unexpectedly refused")
	}
	reservation.CancelAt(now)

	if second := limiter.TryReserve("client", now); second == nil {
		t.Fatal("cancelled reservation did not restore the token")
	}
}

func TestRateLimiterTryReserveRequiresImmediateToken(t *testing.T) {
	limiter := NewRateLimiter(rate.Every(time.Minute), 1)
	t.Cleanup(limiter.Stop)
	now := time.Now()

	if first := limiter.TryReserve("client", now); first == nil {
		t.Fatal("first reservation was unexpectedly refused")
	}
	if second := limiter.TryReserve("client", now); second != nil {
		t.Fatal("reservation with a future delay was treated as immediate admission")
	}
}

func TestNilRateLimiterReservationIsNoOp(t *testing.T) {
	var limiter *RateLimiter
	reservation := limiter.TryReserve("client", time.Now())
	if reservation == nil {
		t.Fatal("disabled limiter refused admission")
	}
	reservation.CancelAt(time.Now())
}

func TestRateLimiterTrackedKeyCount(t *testing.T) {
	limiter := NewRateLimiter(rate.Every(time.Minute), 1)
	t.Cleanup(limiter.Stop)
	now := time.Now()

	if got := limiter.TrackedKeyCount(); got != 0 {
		t.Fatalf("initial tracked key count = %d, want 0", got)
	}
	limiter.TryReserve("client-a", now)
	limiter.TryReserve("client-a", now)
	limiter.TryReserve("client-b", now)
	if got := limiter.TrackedKeyCount(); got != 2 {
		t.Fatalf("tracked key count = %d, want 2 distinct keys", got)
	}
}

func TestRateLimiterTrackedKeyCountIsNilSafe(t *testing.T) {
	var limiter *RateLimiter
	if got := limiter.TrackedKeyCount(); got != 0 {
		t.Fatalf("nil limiter tracked key count = %d, want 0", got)
	}
}

func TestRateLimiterTrackedKeyCountConcurrentWithAdmissions(t *testing.T) {
	limiter := NewRateLimiter(rate.Every(time.Minute), 1)
	t.Cleanup(limiter.Stop)

	const keys = 100
	var wg sync.WaitGroup
	for i := 0; i < keys; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			limiter.Allow(fmt.Sprintf("client-%d", i))
			_ = limiter.TrackedKeyCount()
		}(i)
	}
	wg.Wait()

	if got := limiter.TrackedKeyCount(); got != keys {
		t.Fatalf("tracked key count after concurrent admissions = %d, want %d", got, keys)
	}
}
