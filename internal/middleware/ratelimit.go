package middleware

import (
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

// visitor tracks a rate limiter and last-seen time for a single IP.
type visitor struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// RateLimiter provides per-IP token-bucket rate limiting for Gin handlers.
type RateLimiter struct {
	mu       sync.Mutex
	visitors map[string]*visitor
	rate     rate.Limit
	burst    int
	done     chan struct{}
	stopOnce sync.Once
}

// NewRateLimiter creates a per-IP rate limiter.
// r controls how often tokens are replenished; burst is the max burst size.
// Example: rate.Every(6*time.Second), 10 → ~10 requests per minute per IP.
func NewRateLimiter(r rate.Limit, burst int) *RateLimiter {
	rl := &RateLimiter{
		visitors: make(map[string]*visitor),
		rate:     r,
		burst:    burst,
		done:     make(chan struct{}),
	}
	go rl.cleanupLoop()
	return rl
}

// Limit returns a Gin middleware that rejects requests over the rate limit with 429.
func (rl *RateLimiter) Limit() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !rl.Allow(c.ClientIP()) {
			c.Header("Retry-After", strconv.Itoa(rl.RetryAfterSeconds()))
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error": "too many requests, please try again later",
			})
			c.Abort()
			return
		}
		c.Next()
	}
}

// Allow consumes a token for key and reports whether the request may proceed.
//
// It exists for callers that cannot use Limit() as route middleware because the
// decision depends on something only the handler knows — the upload endpoint,
// for instance, must throttle anonymous link tokens while leaving authenticated
// web uploads on the same route untouched. Those callers own the response, and
// choose their own key: Limit() keys on the client IP, but a caller that must
// isolate two clients sharing one NAT address can key on more than that.
//
// A nil receiver allows the request, so a disabled limiter is expressed by
// holding no limiter at all rather than by branching at every call site.
func (rl *RateLimiter) Allow(key string) bool {
	if rl == nil {
		return true
	}
	return rl.getVisitor(key).Allow()
}

// RetryAfterSeconds is the whole-second Retry-After value implied by the
// configured refill rate, floored at 1 — a `Retry-After: 0` invites the
// immediate retry the header exists to prevent.
//
// It is exported because a caller using Allow() writes its own 429 and must be
// able to answer with the same delay Limit() would have, without duplicating
// the arithmetic or reaching for the unexported rate.
func (rl *RateLimiter) RetryAfterSeconds() int {
	retryAfter := time.Second
	if rl != nil && rl.rate > 0 {
		retryAfter = time.Duration(float64(time.Second) / float64(rl.rate))
		if retryAfter < time.Second {
			retryAfter = time.Second
		}
	}
	return int(math.Ceil(retryAfter.Seconds()))
}

func (rl *RateLimiter) getVisitor(ip string) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	v, exists := rl.visitors[ip]
	if !exists {
		limiter := rate.NewLimiter(rl.rate, rl.burst)
		rl.visitors[ip] = &visitor{limiter: limiter, lastSeen: time.Now()}
		return limiter
	}
	v.lastSeen = time.Now()
	return v.limiter
}

// cleanupLoop removes visitors idle for more than 5 minutes, every 3 minutes.
func (rl *RateLimiter) cleanupLoop() {
	ticker := time.NewTicker(3 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			rl.mu.Lock()
			for ip, v := range rl.visitors {
				if time.Since(v.lastSeen) > 5*time.Minute {
					delete(rl.visitors, ip)
				}
			}
			rl.mu.Unlock()
		case <-rl.done:
			return
		}
	}
}

// Stop shuts down the background cleanup goroutine. Nil-safe, matching Allow, so
// a caller holding an optional limiter can stop it unconditionally.
func (rl *RateLimiter) Stop() {
	if rl == nil {
		return
	}
	rl.stopOnce.Do(func() {
		close(rl.done)
	})
}
