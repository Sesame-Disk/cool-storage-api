package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/metrics"
)

// Subcontract A1 of ISSUE-RATE-LIMIT-UPLOAD-DOWNLOAD-01: the anonymous
// public-upload-link write path had no bound. The /api/v2.1 upload-link routes
// already carried a per-IP limiter, but the seafhttp endpoint they hand off to
// carried none, so the write itself was unlimited.
//
// Two properties here are NEGATIVE, and they are the ones worth having:
//
//   - /seafhttp/upload-api/:token also serves authenticated web uploads, so the
//     bound must key on the token's origin, not on the route. A limiter installed
//     as route middleware would have throttled ordinary users.
//   - The bound keys on (IP, token), not IP alone, so two people behind one NAT
//     using two different links do not share a bucket.
//
// A test asserting only "link tokens get 429" would have missed both. These go
// through HandleUpload rather than the decision helper, so they also pin that the
// handler actually consults it, after the token resolves.

func throttleTestConfig(perMinute, burst, tokenPerMinute, tokenBurst int) *config.Config {
	cfg := &config.Config{}
	cfg.SeafHTTP.UploadLinkWritesPerMinute = perMinute
	cfg.SeafHTTP.UploadLinkWriteBurst = burst
	cfg.SeafHTTP.UploadLinkTokenWritesPerMinute = tokenPerMinute
	cfg.SeafHTTP.UploadLinkTokenWriteBurst = tokenBurst
	return cfg
}

// newThrottleTestHandler builds a handler with a token store holding the given
// tokens, and stops its limiter goroutines when the test ends.
func newThrottleTestHandler(t *testing.T, cfg *config.Config, tokens map[string]string) (*SeafHTTPHandler, *gin.Engine) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	store := NewMockTokenStore()
	for tokenStr, source := range tokens {
		store.tokens[tokenStr] = &AccessToken{
			Token:     tokenStr,
			Type:      TokenTypeUpload,
			OrgID:     "00000000-0000-0000-0000-000000000001",
			RepoID:    "repo-1",
			Path:      "/",
			UserID:    "user-1",
			Source:    source,
			ExpiresAt: time.Now().Add(time.Hour),
			CreatedAt: time.Now(),
		}
	}

	h := NewSeafHTTPHandler(nil, nil, nil, store, cfg, nil)
	t.Cleanup(h.Close)

	r := gin.New()
	r.POST("/seafhttp/upload-api/:token", h.HandleUpload)
	return h, r
}

// upload drives one request from a given client address. Everything past the
// throttle needs storage and permissions that these tests do not configure, so
// the assertions are about 429-or-not, never about success.
func upload(t *testing.T, r *gin.Engine, tokenStr, clientIP string) *httptest.ResponseRecorder {
	t.Helper()
	req := newMultipartUploadRequest(t, "/seafhttp/upload-api/"+tokenStr, "photo.jpg", []byte("hello"))
	req.RemoteAddr = clientIP + ":54321"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestUploadLinkWriteThrottleAppliesOnlyToLinkTokens(t *testing.T) {
	// A burst of 1 makes the second request in a row the one that trips.
	h, r := newThrottleTestHandler(t, throttleTestConfig(60, 1, 0, 0), map[string]string{
		"link-token": "link",
		"web-token":  "web",
		"bare-token": "",
	})
	if h.uploadLinkWriteLimits == nil {
		t.Fatal("limiter not constructed; the rest of this test would pass vacuously")
	}

	const clientIP = "203.0.113.7"

	t.Run("anonymous link token is throttled past the burst", func(t *testing.T) {
		if w := upload(t, r, "link-token", clientIP); w.Code == http.StatusTooManyRequests {
			t.Fatal("first link write refused; the burst must admit it")
		}
		w := upload(t, r, "link-token", clientIP)
		if w.Code != http.StatusTooManyRequests {
			t.Fatalf("second link write = %d, want 429; the limiter is not bounding this surface", w.Code)
		}
		if w.Header().Get("Retry-After") == "" {
			t.Error("a throttled write must carry Retry-After; the browser uploader waits on it")
		}
	})

	// The negative half. The same handler, the same client address, the same
	// route — only the token origin differs.
	t.Run("authenticated web uploads on the same route are never throttled", func(t *testing.T) {
		for _, tokenStr := range []string{"web-token", "bare-token"} {
			for i := 0; i < 10; i++ {
				if w := upload(t, r, tokenStr, clientIP); w.Code == http.StatusTooManyRequests {
					t.Fatalf("%s was throttled (attempt %d); the bound must key on token origin, not on the route", tokenStr, i+1)
				}
			}
		}
	})
}

// TestUploadLinkWriteThrottleIsolatesLinksBehindOneAddress is the NAT property.
// One address is routinely a whole office, school or mobile carrier. Keyed on IP
// alone, one person uploading through one link would throttle every colleague
// using a different one — a limiter that produces exactly the outage it exists to
// prevent.
func TestUploadLinkWriteThrottleIsolatesLinksBehindOneAddress(t *testing.T) {
	_, r := newThrottleTestHandler(t, throttleTestConfig(60, 1, 0, 0), map[string]string{
		"link-a": "link",
		"link-b": "link",
	})
	const sharedIP = "198.51.100.20"

	// Exhaust the bucket for link A.
	upload(t, r, "link-a", sharedIP)
	if w := upload(t, r, "link-a", sharedIP); w.Code != http.StatusTooManyRequests {
		t.Fatalf("link A second write = %d, want 429; the bucket was not exhausted, so the next assertion proves nothing", w.Code)
	}

	// Link B from the same address must be untouched by that.
	if w := upload(t, r, "link-b", sharedIP); w.Code == http.StatusTooManyRequests {
		t.Fatal("a second link from the same address was throttled; the key must be (IP, token), not IP alone")
	}
}

// TestUploadLinkWriteThrottlePerTokenBucket covers the case the (IP, token)
// bucket structurally cannot see: one leaked upload URL hit from many addresses.
func TestUploadLinkWriteThrottlePerTokenBucket(t *testing.T) {
	// Per-client generous, per-token tight, so only the per-token bound can fire.
	_, r := newThrottleTestHandler(t, throttleTestConfig(6000, 100, 60, 2), map[string]string{
		"leaked-link": "link",
	})

	before := testutil.ToFloat64(metrics.UploadLinkWriteThrottledTotal.WithLabelValues("token"))

	// Two different addresses spend the per-token burst, a third finds it empty.
	upload(t, r, "leaked-link", "203.0.113.1")
	upload(t, r, "leaked-link", "203.0.113.2")
	w := upload(t, r, "leaked-link", "203.0.113.3")

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("third address = %d, want 429; a leaked link is unbounded across IPs without this bucket", w.Code)
	}
	if after := testutil.ToFloat64(metrics.UploadLinkWriteThrottledTotal.WithLabelValues("token")); after <= before {
		t.Errorf("token-bucket rejections did not move (%v -> %v); the two buckets must be distinguishable, they call for opposite responses", before, after)
	}
}

// The two bounds are independently configurable. Disabling the per-client
// bucket must not accidentally remove the per-token defence against one leaked
// link being used from many addresses.
func TestUploadLinkWriteThrottlePerTokenOnly(t *testing.T) {
	h, r := newThrottleTestHandler(t, throttleTestConfig(0, 0, 60, 1), map[string]string{
		"leaked-link": "link",
	})
	if h.uploadLinkWriteLimits == nil || h.uploadLinkWriteLimits.perToken == nil {
		t.Fatal("per-token limiter was not constructed when the per-client limiter was disabled")
	}
	if h.uploadLinkWriteLimits.perClient != nil {
		t.Fatal("per-client limiter was constructed while disabled")
	}

	if w := upload(t, r, "leaked-link", "203.0.113.1"); w.Code == http.StatusTooManyRequests {
		t.Fatal("first write was refused; the per-token burst must admit it")
	}
	if w := upload(t, r, "leaked-link", "203.0.113.2"); w.Code != http.StatusTooManyRequests {
		t.Fatalf("second address = %d, want 429 from the independently enabled per-token bucket", w.Code)
	}
}

func TestUploadLinkWriteThrottleDisabledByConfig(t *testing.T) {
	h, r := newThrottleTestHandler(t, throttleTestConfig(0, 0, 0, 0), map[string]string{"link-token": "link"})
	if h.uploadLinkWriteLimits != nil {
		t.Fatal("limiter constructed while disabled by configuration")
	}
	for i := 0; i < 50; i++ {
		if w := upload(t, r, "link-token", "203.0.113.7"); w.Code == http.StatusTooManyRequests {
			t.Fatalf("link write refused at attempt %d while the limiter is disabled", i+1)
		}
	}
}

// A handler built without configuration must not silently enable a limiter, and
// must not panic either. Nil config is a wiring bug, not a supported mode, so the
// safe behaviour is to let traffic through rather than to throttle blindly.
func TestUploadLinkWriteThrottleNilConfig(t *testing.T) {
	h, r := newThrottleTestHandler(t, nil, map[string]string{"link-token": "link"})
	if h.uploadLinkWriteLimits != nil {
		t.Fatal("limiter constructed from a nil config")
	}
	if w := upload(t, r, "link-token", "203.0.113.7"); w.Code == http.StatusTooManyRequests {
		t.Fatal("link write refused with no configuration present")
	}
}

// A zero burst reaching the constructor means the config never went through
// Validate(), which rejects it. Building a bucket with no capacity would refuse
// every anonymous upload, so the constructor declines to build one at all.
func TestUploadLinkWriteThrottleZeroBurstDoesNotBuildAnEmptyBucket(t *testing.T) {
	h, r := newThrottleTestHandler(t, throttleTestConfig(600, 0, 0, 0), map[string]string{"link-token": "link"})
	if h.uploadLinkWriteLimits != nil && h.uploadLinkWriteLimits.perClient != nil {
		t.Fatal("a bucket was built with zero capacity; it would refuse every request")
	}
	for i := 0; i < 5; i++ {
		if w := upload(t, r, "link-token", "203.0.113.7"); w.Code == http.StatusTooManyRequests {
			t.Fatalf("link write refused at attempt %d by a zero-capacity bucket", i+1)
		}
	}
}

// A nil token must not be treated as a link token. HandleUpload never reaches the
// helper with one today, but the helper is the thing that decides, so it owns the
// answer rather than relying on its caller's ordering staying as it is.
func TestUploadLinkWriteThrottleNilTokenIsAllowed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _ := newThrottleTestHandler(t, throttleTestConfig(60, 1, 0, 0), nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/seafhttp/upload-api/tok", nil)

	for i := 0; i < 5; i++ {
		if !h.allowUploadLinkWrite(c, "tok", nil) {
			t.Fatalf("nil token refused at attempt %d", i+1)
		}
	}
}

// Close owns background goroutines, so it must tolerate being called on a
// handler with no limiters and more than once — Shutdown is not the only path
// that reaches it.
func TestSeafHTTPHandlerCloseIsSafeToRepeat(t *testing.T) {
	h := NewSeafHTTPHandler(nil, nil, nil, nil, throttleTestConfig(600, 1200, 12000, 24000), nil)
	h.Close()
	h.Close()

	disabled := NewSeafHTTPHandler(nil, nil, nil, nil, throttleTestConfig(0, 0, 0, 0), nil)
	disabled.Close()

	var nilHandler *SeafHTTPHandler
	nilHandler.Close()
}
