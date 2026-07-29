package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/metrics"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
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
//   - The bound keys on (IP, stable link source), not IP alone, so two people behind one NAT
//     using two different links do not share a bucket.
//
// A test asserting only "link tokens get 429" would have missed both. These go
// through HandleUpload rather than the decision helper, so they also pin that the
// handler actually consults it, after the token resolves.

func throttleTestConfig(perMinute, burst, sourcePerMinute, sourceBurst int) *config.Config {
	cfg := &config.Config{}
	cfg.SeafHTTP.UploadLinkWritesPerMinute = perMinute
	cfg.SeafHTTP.UploadLinkWriteBurst = burst
	cfg.SeafHTTP.UploadLinkSourceWritesPerMinute = sourcePerMinute
	cfg.SeafHTTP.UploadLinkSourceWriteBurst = sourceBurst
	return cfg
}

func inflightTestConfig(perSource, perNode int) *config.Config {
	cfg := throttleTestConfig(0, 0, 0, 0)
	cfg.SeafHTTP.UploadLinkMaxInflightPerSource = perSource
	cfg.SeafHTTP.UploadLinkMaxInflightPerNode = perNode
	return cfg
}

// newThrottleTestHandler builds a handler with a token store holding the given
// tokens, and stops its limiter goroutines when the test ends.
func newThrottleTestHandler(t *testing.T, cfg *config.Config, tokens map[string]string) (*SeafHTTPHandler, *gin.Engine) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	store := NewMockTokenStore()
	for tokenStr, source := range tokens {
		sourceID := ""
		if source == "link" {
			sourceID = tokenStr
		}
		store.tokens[tokenStr] = &AccessToken{
			Token:     tokenStr,
			Type:      TokenTypeUpload,
			OrgID:     "00000000-0000-0000-0000-000000000001",
			RepoID:    "repo-1",
			Path:      "/",
			UserID:    "user-1",
			Source:    source,
			SourceID:  sourceID,
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

func setThrottleTestSourceID(h *SeafHTTPHandler, tokenStr, sourceID string) {
	store := h.tokenStore.(*MockTokenStore)
	store.tokens[tokenStr].SourceID = sourceID
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
	h, r := newThrottleTestHandler(t, throttleTestConfig(1, 1, 0, 0), map[string]string{
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
	_, r := newThrottleTestHandler(t, throttleTestConfig(1, 1, 0, 0), map[string]string{
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
		t.Fatal("a second link from the same address was throttled; the key must be (IP, source), not IP alone")
	}
}

// TestUploadLinkWriteThrottlePerSourceBucket covers the case the per-client
// bucket structurally cannot see: one leaked upload URL hit from many addresses.
func TestUploadLinkWriteThrottlePerSourceBucket(t *testing.T) {
	// Per-client generous, per-source tight, so only the per-link bound can fire.
	_, r := newThrottleTestHandler(t, throttleTestConfig(6000, 100, 1, 2), map[string]string{
		"leaked-link": "link",
	})

	before := testutil.ToFloat64(metrics.UploadLinkWriteThrottledTotal.WithLabelValues("source"))

	// Two different addresses spend the per-link burst, a third finds it empty.
	upload(t, r, "leaked-link", "203.0.113.1")
	upload(t, r, "leaked-link", "203.0.113.2")
	w := upload(t, r, "leaked-link", "203.0.113.3")

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("third address = %d, want 429; a leaked link is unbounded across IPs without this bucket", w.Code)
	}
	if after := testutil.ToFloat64(metrics.UploadLinkWriteThrottledTotal.WithLabelValues("source")); after <= before {
		t.Errorf("source-bucket rejections did not move (%v -> %v); the two buckets must be distinguishable, they call for opposite responses", before, after)
	}
}

// The two bounds are independently configurable. Disabling the per-client
// bucket must not accidentally remove the per-link defence against one leaked
// link being used from many addresses.
func TestUploadLinkWriteThrottlePerSourceOnly(t *testing.T) {
	h, r := newThrottleTestHandler(t, throttleTestConfig(0, 0, 1, 1), map[string]string{
		"leaked-link": "link",
	})
	if h.uploadLinkWriteLimits == nil || h.uploadLinkWriteLimits.perSource == nil {
		t.Fatal("per-source limiter was not constructed when the per-client limiter was disabled")
	}
	if h.uploadLinkWriteLimits.perClient != nil {
		t.Fatal("per-client limiter was constructed while disabled")
	}

	if w := upload(t, r, "leaked-link", "203.0.113.1"); w.Code == http.StatusTooManyRequests {
		t.Fatal("first write was refused; the per-source burst must admit it")
	}
	if w := upload(t, r, "leaked-link", "203.0.113.2"); w.Code != http.StatusTooManyRequests {
		t.Fatalf("second address = %d, want 429 from the independently enabled per-source bucket", w.Code)
	}
}

func TestUploadLinkWriteThrottleSourceRejectionRestoresClientAdmission(t *testing.T) {
	h, r := newThrottleTestHandler(t, throttleTestConfig(1, 1, 1, 1), map[string]string{
		"shared-link": "link",
	})

	upload(t, r, "shared-link", "203.0.113.1") // Spend the source-wide burst.
	if w := upload(t, r, "shared-link", "203.0.113.2"); w.Code != http.StatusTooManyRequests {
		t.Fatalf("second address = %d, want source-wide 429", w.Code)
	}

	key := "203.0.113.2|shared-link"
	now := time.Now()
	admission := h.uploadLinkWriteLimits.perClient.TryReserve(key, now)
	if admission == nil {
		t.Fatal("source-wide rejection consumed the independent per-client admission")
	}
	admission.CancelAt(now)
}

func TestUploadLinkWriteThrottleRemintsSharePerClientBucket(t *testing.T) {
	h, r := newThrottleTestHandler(t, throttleTestConfig(1, 1, 0, 0), map[string]string{
		"mint-a": "link",
		"mint-b": "link",
	})
	setThrottleTestSourceID(h, "mint-a", "same-public-link")
	setThrottleTestSourceID(h, "mint-b", "same-public-link")

	upload(t, r, "mint-a", "203.0.113.10")
	if w := upload(t, r, "mint-b", "203.0.113.10"); w.Code != http.StatusTooManyRequests {
		t.Fatalf("reminted token write = %d, want 429 from the shared (client, source) bucket", w.Code)
	}
}

func TestUploadLinkWriteThrottleRemintsShareAggregateBucket(t *testing.T) {
	h, r := newThrottleTestHandler(t, throttleTestConfig(6000, 100, 1, 1), map[string]string{
		"mint-a": "link",
		"mint-b": "link",
	})
	setThrottleTestSourceID(h, "mint-a", "same-public-link")
	setThrottleTestSourceID(h, "mint-b", "same-public-link")

	upload(t, r, "mint-a", "203.0.113.11")
	if w := upload(t, r, "mint-b", "203.0.113.12"); w.Code != http.StatusTooManyRequests {
		t.Fatalf("reminted token from another client = %d, want 429 from the shared source bucket", w.Code)
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
	h, _ := newThrottleTestHandler(t, throttleTestConfig(1, 1, 0, 0), nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/seafhttp/upload-api/tok", nil)

	for i := 0; i < 5; i++ {
		if admitted := h.allowUploadLinkWrite(c, nil); !admitted {
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

type gatedEOFReader struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (r *gatedEOFReader) Read([]byte) (int, error) {
	r.once.Do(func() { close(r.started) })
	<-r.release
	return 0, io.EOF
}

type observedEOFReader struct {
	read chan struct{}
	once sync.Once
}

func (r *observedEOFReader) Read([]byte) (int, error) {
	r.once.Do(func() { close(r.read) })
	return 0, io.EOF
}

func uploadWithReader(r *gin.Engine, tokenStr string, body io.Reader) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/seafhttp/upload-api/"+tokenStr, body)
	req.Header.Set("Content-Type", "multipart/form-data; boundary=inflight-test")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func startBlockedUpload(t *testing.T, r *gin.Engine, tokenStr string) (func(), <-chan *httptest.ResponseRecorder) {
	t.Helper()
	body := &gatedEOFReader{started: make(chan struct{}), release: make(chan struct{})}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- uploadWithReader(r, tokenStr, body) }()
	select {
	case <-body.started:
	case <-time.After(2 * time.Second):
		t.Fatal("upload did not reach its body read")
	}
	return func() { close(body.release) }, done
}

func requireBodyUnread(t *testing.T, read <-chan struct{}) {
	t.Helper()
	select {
	case <-read:
		t.Fatal("rejected upload reached its body read")
	default:
	}
}

func waitUpload(t *testing.T, done <-chan *httptest.ResponseRecorder) *httptest.ResponseRecorder {
	t.Helper()
	select {
	case w := <-done:
		return w
	case <-time.After(2 * time.Second):
		t.Fatal("upload did not finish")
		return nil
	}
}

func TestUploadLinkInflightRejectionConsumesRateAdmission(t *testing.T) {
	tests := []struct {
		name      string
		perClient int
		perSource int
	}{
		{name: "both buckets enabled", perClient: 1, perSource: 1},
		{name: "per-client bucket only", perClient: 1},
		{name: "per-source bucket only", perSource: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := throttleTestConfig(tt.perClient, tt.perClient, tt.perSource, tt.perSource)
			cfg.SeafHTTP.UploadLinkMaxInflightPerSource = 1
			h, r := newThrottleTestHandler(t, cfg, map[string]string{"link": "link"})
			setThrottleTestSourceID(h, "link", "stable-source")

			release, reason := h.uploadLinkInflight.tryAcquire("stable-source")
			if release == nil {
				t.Fatalf("failed to fill in-flight guard: %s", reason)
			}
			defer release()

			body := &observedEOFReader{read: make(chan struct{})}
			req := httptest.NewRequest(http.MethodPost, "/seafhttp/upload-api/link", body)
			req.Header.Set("Content-Type", "multipart/form-data; boundary=inflight-test")
			req.RemoteAddr = "198.51.100.77:4321"
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != http.StatusTooManyRequests {
				t.Fatalf("upload with full in-flight guard = %d, want 429", w.Code)
			}
			requireBodyUnread(t, body.read)

			now := time.Now()
			if tt.perClient > 0 {
				if admission := h.uploadLinkWriteLimits.perClient.TryReserve("198.51.100.77|stable-source", now); admission != nil {
					t.Fatal("A2 rejection did not consume the A1 per-client budget keyed by RemoteAddr and SourceID")
				}
				wrongKey := h.uploadLinkWriteLimits.perClient.TryReserve("198.51.100.77|link", now)
				if wrongKey == nil {
					t.Fatal("token bearer was used instead of SourceID in the per-client key")
				}
				wrongKey.CancelAt(now)
			}

			if tt.perSource > 0 {
				if admission := h.uploadLinkWriteLimits.perSource.TryReserve("stable-source", now); admission != nil {
					t.Fatal("A2 rejection did not consume the A1 per-source budget")
				}
				wrongKey := h.uploadLinkWriteLimits.perSource.TryReserve("link", now)
				if wrongKey == nil {
					t.Fatal("token bearer was used instead of SourceID in the per-source key")
				}
				wrongKey.CancelAt(now)
			}
		})
	}
}

func TestUploadLinkWithBlankSourceIDFailsClosed(t *testing.T) {
	h, r := newThrottleTestHandler(t, throttleTestConfig(0, 0, 0, 0), map[string]string{"link": "link"})
	setThrottleTestSourceID(h, "link", " \t")
	body := &observedEOFReader{read: make(chan struct{})}
	w := uploadWithReader(r, "link", body)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("blank-SourceID link upload = %d, want 401", w.Code)
	}
	requireBodyUnread(t, body.read)
}

func TestUploadLinkInflightPerSourceRejectsBeforeBodyAndReleases(t *testing.T) {
	h, r := newThrottleTestHandler(t, inflightTestConfig(1, 4), map[string]string{
		"link-a": "link",
		"link-b": "link",
	})
	setThrottleTestSourceID(h, "link-a", "same-public-link")
	setThrottleTestSourceID(h, "link-b", "same-public-link")
	h.storageManager = storage.NewManager()
	release, done := startBlockedUpload(t, r, "link-a")

	before := testutil.ToFloat64(metrics.UploadLinkInflightRejectedTotal.WithLabelValues("source"))
	rejectedBody := &observedEOFReader{read: make(chan struct{})}
	w := uploadWithReader(r, "link-b", rejectedBody)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("concurrent write = %d, want 429", w.Code)
	}
	if got := w.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After = %q, want 1", got)
	}
	requireBodyUnread(t, rejectedBody.read)
	if after := testutil.ToFloat64(metrics.UploadLinkInflightRejectedTotal.WithLabelValues("source")); after != before+1 {
		t.Fatalf("source rejection metric = %v, want %v", after, before+1)
	}

	release()
	if first := waitUpload(t, done); first.Code != http.StatusBadRequest {
		t.Fatalf("released malformed upload = %d, want 400", first.Code)
	}
	laterBody := &observedEOFReader{read: make(chan struct{})}
	if later := uploadWithReader(r, "link-b", laterBody); later.Code != http.StatusBadRequest {
		t.Fatalf("later upload = %d, want admission followed by 400", later.Code)
	}
	select {
	case <-laterBody.read:
	default:
		t.Fatal("later upload was not admitted after release")
	}
}

func TestUploadLinkInflightNodeCapSpansLinks(t *testing.T) {
	h, r := newThrottleTestHandler(t, inflightTestConfig(2, 1), map[string]string{
		"link-a": "link",
		"link-b": "link",
	})
	h.storageManager = storage.NewManager()
	release, done := startBlockedUpload(t, r, "link-a")

	before := testutil.ToFloat64(metrics.UploadLinkInflightRejectedTotal.WithLabelValues("node"))
	rejectedBody := &observedEOFReader{read: make(chan struct{})}
	w := uploadWithReader(r, "link-b", rejectedBody)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("second link while node full = %d, want 429", w.Code)
	}
	requireBodyUnread(t, rejectedBody.read)
	if after := testutil.ToFloat64(metrics.UploadLinkInflightRejectedTotal.WithLabelValues("node")); after != before+1 {
		t.Fatalf("node rejection metric = %v, want %v", after, before+1)
	}

	release()
	waitUpload(t, done)
	laterBody := &observedEOFReader{read: make(chan struct{})}
	if later := uploadWithReader(r, "link-b", laterBody); later.Code != http.StatusBadRequest {
		t.Fatalf("second link after release = %d, want admission followed by 400", later.Code)
	}
}

func TestUploadLinkInflightReleasesAfterEarlyStorageError(t *testing.T) {
	_, r := newThrottleTestHandler(t, inflightTestConfig(1, 1), map[string]string{"link": "link"})
	for i := 0; i < 2; i++ {
		w := uploadWithReader(r, "link", &observedEOFReader{read: make(chan struct{})})
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("request %d = %d, want 503; a 429 means the prior early return leaked its slot", i+1, w.Code)
		}
	}
}

func TestUploadLinkInflightWebTokenBypassesFullCap(t *testing.T) {
	h, r := newThrottleTestHandler(t, inflightTestConfig(1, 1), map[string]string{
		"link": "link",
		"web":  "web",
	})
	h.storageManager = storage.NewManager()
	release, done := startBlockedUpload(t, r, "link")
	webBody := &observedEOFReader{read: make(chan struct{})}
	if w := uploadWithReader(r, "web", webBody); w.Code != http.StatusBadRequest {
		t.Fatalf("web upload while link cap full = %d, want bypass followed by 400", w.Code)
	}
	select {
	case <-webBody.read:
	default:
		t.Fatal("web upload did not bypass the anonymous cap")
	}
	release()
	waitUpload(t, done)
}

func TestUploadLinkInflightDisabled(t *testing.T) {
	h, r := newThrottleTestHandler(t, inflightTestConfig(0, 0), map[string]string{"link": "link"})
	if h.uploadLinkInflight != nil {
		t.Fatal("in-flight limiter constructed while both caps are disabled")
	}
	h.storageManager = storage.NewManager()
	body := &observedEOFReader{read: make(chan struct{})}
	if w := uploadWithReader(r, "link", body); w.Code != http.StatusBadRequest {
		t.Fatalf("disabled-cap upload = %d, want body parsing 400", w.Code)
	}
}

func TestUploadLinkInflightCapsDisableIndependently(t *testing.T) {
	t.Run("source cap disabled", func(t *testing.T) {
		l := newUploadLinkInflightLimiter(inflightTestConfig(0, 1))
		release, reason := l.tryAcquire("source-a")
		if release == nil {
			t.Fatalf("first admission rejected: %s", reason)
		}
		defer release()
		if second, reason := l.tryAcquire("source-b"); second != nil || reason != "node" {
			t.Fatalf("second admission = (%v, %q), want node rejection", second != nil, reason)
		}
	})

	t.Run("node cap disabled", func(t *testing.T) {
		l := newUploadLinkInflightLimiter(inflightTestConfig(1, 0))
		releaseA, reason := l.tryAcquire("source-a")
		if releaseA == nil {
			t.Fatalf("first admission rejected: %s", reason)
		}
		defer releaseA()
		if second, reason := l.tryAcquire("source-a"); second != nil || reason != "source" {
			t.Fatalf("same-source admission = (%v, %q), want source rejection", second != nil, reason)
		}
		releaseB, reason := l.tryAcquire("source-b")
		if releaseB == nil {
			t.Fatalf("different source rejected with node cap disabled: %s", reason)
		}
		releaseB()
	})
}

func TestUploadLinkInflightLimiterConcurrentRelease(t *testing.T) {
	l := &uploadLinkInflightLimiter{maxPerSource: 4, maxPerNode: 16, perSource: make(map[string]int)}
	var wg sync.WaitGroup
	for worker := 0; worker < 32; worker++ {
		wg.Add(1)
		go func(source string) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				if release, _ := l.tryAcquire(source); release != nil {
					release()
					release()
				}
			}
		}(string(rune('a' + worker%8)))
	}
	wg.Wait()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.inflight != 0 || len(l.perSource) != 0 {
		t.Fatalf("after concurrent release: inflight=%d perSource=%v", l.inflight, l.perSource)
	}
}

func TestUploadLinkInflightMetricsTrackAdmissions(t *testing.T) {
	l := newUploadLinkInflightLimiter(inflightTestConfig(2, 4))
	beforeGauge := testutil.ToFloat64(metrics.UploadLinkInflightCurrent)
	beforeHistogram := &dto.Metric{}
	if err := metrics.UploadLinkSourceInflightOccupancy.Write(beforeHistogram); err != nil {
		t.Fatalf("write histogram before admission: %v", err)
	}

	releaseA, _ := l.tryAcquire("source")
	releaseB, _ := l.tryAcquire("source")
	if releaseA == nil || releaseB == nil {
		t.Fatal("expected two metric test admissions")
	}
	if got := testutil.ToFloat64(metrics.UploadLinkInflightCurrent); got != beforeGauge+2 {
		t.Fatalf("current in-flight gauge = %v, want %v", got, beforeGauge+2)
	}
	afterHistogram := &dto.Metric{}
	if err := metrics.UploadLinkSourceInflightOccupancy.Write(afterHistogram); err != nil {
		t.Fatalf("write histogram after admission: %v", err)
	}
	if got, want := afterHistogram.GetHistogram().GetSampleCount(), beforeHistogram.GetHistogram().GetSampleCount()+2; got != want {
		t.Fatalf("occupancy histogram sample count = %d, want %d", got, want)
	}
	if got, want := afterHistogram.GetHistogram().GetSampleSum(), beforeHistogram.GetHistogram().GetSampleSum()+3; got != want {
		t.Fatalf("occupancy histogram sample sum = %v, want %v", got, want)
	}

	releaseA()
	releaseB()
	if got := testutil.ToFloat64(metrics.UploadLinkInflightCurrent); got != beforeGauge {
		t.Fatalf("current in-flight gauge after release = %v, want %v", got, beforeGauge)
	}
}
