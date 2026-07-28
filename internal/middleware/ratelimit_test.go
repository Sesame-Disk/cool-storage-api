package middleware

import (
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
