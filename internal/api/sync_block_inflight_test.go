package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/metrics"
)

// Subcontract B (= registry X10) of ISSUE-RATE-LIMIT-UPLOAD-DOWNLOAD-01.
//
// The property under test is not "requests get refused" — it is that a request
// without an admission never reaches io.ReadAll, that the refusal speaks the
// dialect the desktop sync client actually retries (503, never 429), and that
// admissions are not leaked. A test that only checked status codes would pass
// against an implementation that buffered the body first and refused afterwards,
// which is the exact defect this closes.

func syncInflightConfig(perUser, perNode int, wait time.Duration) *config.Config {
	cfg := &config.Config{}
	cfg.SeafHTTP.SyncBlockMaxBytes = config.DefaultSyncBlockMaxBytes
	cfg.SeafHTTP.SyncBlockMaxInflightPerUser = perUser
	cfg.SeafHTTP.SyncBlockMaxInflightPerNode = perNode
	cfg.SeafHTTP.SyncBlockAdmissionWait = wait
	cfg.SeafHTTP.SyncBlockMaxWaitersPerUser = config.DefaultSyncBlockMaxWaitersPerUser
	cfg.SeafHTTP.SyncBlockMaxWaitersPerNode = config.DefaultSyncBlockMaxWaitersPerNode
	cfg.SeafHTTP.SyncBlockAdmittedLifetime = config.DefaultSyncBlockAdmittedLifetime
	return cfg
}

// newInflightTestHandler builds the handler through the real constructor, so a
// wiring regression that stops constructing the limiter fails here rather than
// passing vacuously.
func newInflightTestHandler(t *testing.T, cfg *config.Config) *SyncHandler {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h := NewSyncHandler(nil, nil, nil, cfg, nil)
	if h.blockInflight == nil {
		t.Fatal("in-flight limiter was not constructed; the rest of this test would pass vacuously")
	}
	return h
}

// putBlockRouterFor routes with a caller-supplied identity, since the per-user
// gate is keyed on (org, user) and isolation cannot be tested with the shared
// fixed-identity router.
func putBlockRouterFor(h *SyncHandler, orgID, userID string) *gin.Engine {
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("org_id", orgID)
		c.Set("user_id", userID)
		c.Next()
	})
	r.PUT("/seafhttp/repo/:repo_id/block/:block_id", h.PutBlock)
	return r
}

const inflightTestBlockID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" // 64 hex

func putBlockWithBody(r *gin.Engine, body *gatedEOFReader) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPut, "/seafhttp/repo/repo/block/"+inflightTestBlockID, body)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// startHeldBlockUpload starts an upload that is admitted and then parks inside
// its body read, so a second request meets a genuinely occupied slot rather than
// a simulated one. The returned func frees it.
func startHeldBlockUpload(t *testing.T, r *gin.Engine) (func(), <-chan *httptest.ResponseRecorder) {
	t.Helper()
	body := &gatedEOFReader{started: make(chan struct{}), release: make(chan struct{})}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- putBlockWithBody(r, body) }()
	select {
	case <-body.started:
	case <-time.After(3 * time.Second):
		t.Fatal("upload never reached its body read, so it never held an admission")
	}
	return func() { close(body.release) }, done
}

// putBlockObservingBody drives a request whose body reports when it is first
// read. A refused request must leave that channel closed.
func putBlockObservingBody(r *gin.Engine) (*httptest.ResponseRecorder, <-chan struct{}) {
	body := &observedEOFReader{read: make(chan struct{})}
	req := httptest.NewRequest(http.MethodPut, "/seafhttp/repo/repo/block/"+inflightTestBlockID, body)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w, body.read
}

// TestPutBlockRefusedWithoutReadingBody is the core claim of subcontract B: the
// aggregate memory bound only holds if a request that cannot be admitted never
// buffers anything.
func TestPutBlockRefusedWithoutReadingBody(t *testing.T) {
	h := newInflightTestHandler(t, syncInflightConfig(1, 1, 0))
	r := putBlockRouterFor(h, "org", "user")

	free, done := startHeldBlockUpload(t, r)
	defer func() {
		free()
		<-done
	}()

	w, bodyRead := putBlockObservingBody(r)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("refused upload = %d, want 503", w.Code)
	}
	requireBodyUnread(t, bodyRead)
}

// TestPutBlockRefusalIsNever429 pins the client contract. The official sync
// client retries 502/503/504 as transient network errors but has no 429
// handling, so answering 429 here would surface as a hard sync failure. This is
// deliberately the opposite of the anonymous upload-link path, which is browser
// traffic and does use 429.
func TestPutBlockRefusalIsNever429(t *testing.T) {
	h := newInflightTestHandler(t, syncInflightConfig(1, 1, 0))
	r := putBlockRouterFor(h, "org", "user")

	free, done := startHeldBlockUpload(t, r)
	defer func() {
		free()
		<-done
	}()

	w, _ := putBlockObservingBody(r)

	if w.Code == http.StatusTooManyRequests {
		t.Fatal("refused block upload answered 429; the sync client does not treat it as retryable, it must be 503")
	}
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("refused upload = %d, want 503", w.Code)
	}
}

// TestPutBlockRefusalAdvertisesRetryAfter checks the header a retrying client
// reads, and that it tracks the admission wait rather than being an unrelated
// constant.
func TestPutBlockRefusalAdvertisesRetryAfter(t *testing.T) {
	const wait = 3 * time.Second
	h := newInflightTestHandler(t, syncInflightConfig(1, 1, wait))
	r := putBlockRouterFor(h, "org", "user")

	free, done := startHeldBlockUpload(t, r)
	defer func() {
		free()
		<-done
	}()

	start := time.Now()
	w, bodyRead := putBlockObservingBody(r)
	elapsed := time.Since(start)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("refused upload = %d, want 503", w.Code)
	}
	if got := w.Header().Get("Retry-After"); got != strconv.Itoa(int(wait.Seconds())) {
		t.Fatalf("Retry-After = %q, want %q", got, strconv.Itoa(int(wait.Seconds())))
	}
	// The refusal must come after a real wait, not immediately: a bounded wait is
	// what keeps a cap near the honest-client concurrency from failing bursts.
	if elapsed < wait {
		t.Fatalf("refused after %s, want at least the %s admission wait", elapsed, wait)
	}
	requireBodyUnread(t, bodyRead)
}

// TestPutBlockAdmittedAfterSlotFrees proves the wait is useful and not merely a
// delayed rejection: a queued request proceeds as soon as capacity appears.
func TestPutBlockAdmittedAfterSlotFrees(t *testing.T) {
	h := newInflightTestHandler(t, syncInflightConfig(1, 1, 5*time.Second))
	r := putBlockRouterFor(h, "org", "user")

	free, firstDone := startHeldBlockUpload(t, r)

	queued := make(chan *httptest.ResponseRecorder, 1)
	queuedBody := &observedEOFReader{read: make(chan struct{})}
	go func() {
		req := httptest.NewRequest(http.MethodPut, "/seafhttp/repo/repo/block/"+inflightTestBlockID, queuedBody)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		queued <- w
	}()

	// While the slot is held the queued request must still be waiting, and in
	// particular must not have touched its body.
	time.Sleep(150 * time.Millisecond)
	requireBodyUnread(t, queuedBody.read)

	free()
	<-firstDone

	select {
	case w := <-queued:
		if w.Code == http.StatusServiceUnavailable {
			t.Fatal("queued upload was refused 503 even though a slot freed within the wait")
		}
		select {
		case <-queuedBody.read:
		default:
			t.Fatal("admitted upload never read its body")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("queued upload never completed after the slot freed")
	}
}

// TestPutBlockPerUserGateDoesNotBlockOtherUsers is the fairness property, and the
// reason the per-user gate is acquired before the node gate: a user parked on
// their own cap must not be holding node capacity while they wait.
func TestPutBlockPerUserGateDoesNotBlockOtherUsers(t *testing.T) {
	// Node capacity is deliberately larger than one user's cap, so anything the
	// noisy user cannot use stays available to everyone else.
	h := newInflightTestHandler(t, syncInflightConfig(1, 4, time.Second))
	noisy := putBlockRouterFor(h, "org", "noisy-user")
	quiet := putBlockRouterFor(h, "org", "quiet-user")

	free, done := startHeldBlockUpload(t, noisy)
	defer func() {
		free()
		<-done
	}()

	// A second request from the noisy user piles onto their own gate, holding
	// nothing global while it waits.
	extra := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		w, _ := putBlockObservingBody(noisy)
		extra <- w
	}()
	time.Sleep(100 * time.Millisecond)

	start := time.Now()
	w, bodyRead := putBlockObservingBody(quiet)
	if w.Code == http.StatusServiceUnavailable {
		t.Fatalf("unrelated user refused 503 while another user sat on their own per-user cap")
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("unrelated user waited %s for admission; the noisy user's queue is not supposed to be in the way", elapsed)
	}
	select {
	case <-bodyRead:
	default:
		t.Fatal("admitted upload never read its body")
	}

	<-extra
}

// TestPutBlockNodeGateSpansUsers checks the bound that actually caps memory:
// distinct users share one process-wide budget.
func TestPutBlockNodeGateSpansUsers(t *testing.T) {
	h := newInflightTestHandler(t, syncInflightConfig(1, 1, 0))
	first := putBlockRouterFor(h, "org", "user-a")
	second := putBlockRouterFor(h, "org", "user-b")

	free, done := startHeldBlockUpload(t, first)
	defer func() {
		free()
		<-done
	}()

	w, bodyRead := putBlockObservingBody(second)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("second user = %d, want 503 from the node gate", w.Code)
	}
	requireBodyUnread(t, bodyRead)
}

func TestPutBlockPerUserWaiterQueueFullRejectsBeforeBodyAndTimer(t *testing.T) {
	cfg := syncInflightConfig(1, 4, 5*time.Second)
	cfg.SeafHTTP.SyncBlockMaxWaitersPerUser = 1
	h := newInflightTestHandler(t, cfg)
	r := putBlockRouterFor(h, "org", "user")

	free, heldDone := startHeldBlockUpload(t, r)
	queuedDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		w, _ := putBlockObservingBody(r)
		queuedDone <- w
	}()
	requireWaiterCount(t, h.blockInflight, "user", 1)

	start := time.Now()
	w, bodyRead := putBlockObservingBody(r)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("queue-full upload = %d, want 503", w.Code)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("queue-full rejection took %s; it parked or allocated the admission wait", elapsed)
	}
	requireBodyUnread(t, bodyRead)

	free()
	<-heldDone
	<-queuedDone
	if err := requireDrainedLimiter(h.blockInflight); err != nil {
		t.Fatal(err)
	}
}

func TestPutBlockNodeWaiterQueueFullRejectsBeforeBody(t *testing.T) {
	cfg := syncInflightConfig(1, 1, 5*time.Second)
	cfg.SeafHTTP.SyncBlockMaxWaitersPerNode = 1
	h := newInflightTestHandler(t, cfg)
	holder := putBlockRouterFor(h, "org", "holder")
	waiter := putBlockRouterFor(h, "org", "waiter")
	rejected := putBlockRouterFor(h, "org", "rejected")

	free, heldDone := startHeldBlockUpload(t, holder)
	queuedDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		w, _ := putBlockObservingBody(waiter)
		queuedDone <- w
	}()
	requireWaiterCount(t, h.blockInflight, "node", 1)

	w, bodyRead := putBlockObservingBody(rejected)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("node queue-full upload = %d, want 503", w.Code)
	}
	requireBodyUnread(t, bodyRead)

	free()
	<-heldDone
	<-queuedDone
	if err := requireDrainedLimiter(h.blockInflight); err != nil {
		t.Fatal(err)
	}
}

func TestPutBlockGlobalWaiterCapIncludesPerUserQueues(t *testing.T) {
	cfg := syncInflightConfig(1, 10, 5*time.Second)
	cfg.SeafHTTP.SyncBlockMaxWaitersPerUser = 2
	cfg.SeafHTTP.SyncBlockMaxWaitersPerNode = 2
	h := newInflightTestHandler(t, cfg)
	routers := []*gin.Engine{
		putBlockRouterFor(h, "org", "user-a"),
		putBlockRouterFor(h, "org", "user-b"),
		putBlockRouterFor(h, "org", "user-c"),
	}

	var frees []func()
	var holders []<-chan *httptest.ResponseRecorder
	for _, router := range routers {
		free, done := startHeldBlockUpload(t, router)
		frees = append(frees, free)
		holders = append(holders, done)
	}

	waiters := make(chan *httptest.ResponseRecorder, 2)
	for _, router := range routers[:2] {
		go func(router *gin.Engine) {
			w, _ := putBlockObservingBody(router)
			waiters <- w
		}(router)
	}
	requireWaiterCount(t, h.blockInflight, "node", 2)

	w, bodyRead := putBlockObservingBody(routers[2])
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("global queue overflow = %d, want 503", w.Code)
	}
	requireBodyUnread(t, bodyRead)

	for _, free := range frees {
		free()
	}
	for _, done := range holders {
		<-done
	}
	<-waiters
	<-waiters
	if err := requireDrainedLimiter(h.blockInflight); err != nil {
		t.Fatal(err)
	}
}

func TestBlockAdmissionEntryCapBoundsDistinctUserCardinality(t *testing.T) {
	cfg := syncInflightConfig(1, 1, 30*time.Second)
	cfg.SeafHTTP.SyncBlockMaxWaitersPerUser = 1
	cfg.SeafHTTP.SyncBlockMaxWaitersPerNode = 4
	l := newSyncBlockInflightLimiter(cfg)

	releaseHolder, reason := l.acquire(context.Background(), "holder")
	if reason != "" {
		t.Fatalf("holder rejected: %s", reason)
	}

	ctx, cancel := context.WithCancel(context.Background())
	const contenders = 1000
	results := make(chan string, contenders)
	for i := 0; i < contenders; i++ {
		go func(i int) {
			release, reason := l.acquire(ctx, fmt.Sprintf("user-%d", i))
			if release != nil {
				release()
			}
			results <- reason
		}(i)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(l.entries) == cap(l.entries) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if got, want := len(l.entries), cap(l.entries); got != want {
		cancel()
		releaseHolder()
		t.Fatalf("entry occupancy = %d, want bounded capacity %d", got, want)
	}
	l.mu.Lock()
	users := len(l.perUser)
	l.mu.Unlock()
	if users > cap(l.entries) {
		cancel()
		releaseHolder()
		t.Fatalf("per-user gate cardinality = %d, exceeds global entry cap %d", users, cap(l.entries))
	}

	cancel()
	releaseHolder()
	// The entry ring reports entry_queue_full, not node_queue_full: this burst is
	// high cardinality exhausting the pre-gate ring, which is a different
	// operational signal from a saturated node waiter queue.
	entryFull := 0
	for i := 0; i < contenders; i++ {
		switch reason := <-results; reason {
		case syncBlockRejectEntryQueueFull:
			entryFull++
		case syncBlockRejectNodeQueueFull:
			t.Errorf("entry-ring rejection reported itself as %q; the two must stay distinguishable", reason)
		}
	}
	if entryFull == 0 {
		t.Fatal("high-cardinality burst never reached the global entry cap")
	}
	if err := requireDrainedLimiter(l); err != nil {
		t.Fatal(err)
	}
	if got := len(l.entries); got != 0 {
		t.Fatalf("global entries after drain = %d, want 0", got)
	}
}

// TestPutBlockDisconnectDuringWaitReleasesEverything covers the path where a
// client gives up while queued. Nothing may be read, no slot may be stranded,
// and the outcome must not be counted as a capacity rejection — that would make
// the metric read as overload during ordinary client churn.
func TestPutBlockDisconnectDuringWaitReleasesEverything(t *testing.T) {
	h := newInflightTestHandler(t, syncInflightConfig(1, 1, 30*time.Second))
	r := putBlockRouterFor(h, "org", "user")

	free, done := startHeldBlockUpload(t, r)

	before := testutil.ToFloat64(metrics.SyncPutBlockAdmissionRejectedTotal.WithLabelValues(syncBlockRejectClientGone))

	ctx, cancel := context.WithCancel(context.Background())
	abandoned := make(chan struct{})
	abandonedBody := &observedEOFReader{read: make(chan struct{})}
	go func() {
		defer close(abandoned)
		req := httptest.NewRequest(http.MethodPut, "/seafhttp/repo/repo/block/"+inflightTestBlockID, abandonedBody).WithContext(ctx)
		r.ServeHTTP(httptest.NewRecorder(), req)
	}()

	time.Sleep(150 * time.Millisecond)
	cancel()

	select {
	case <-abandoned:
	case <-time.After(3 * time.Second):
		t.Fatal("abandoned upload did not unwind when its client disconnected; the wait is not cancellable")
	}
	requireBodyUnread(t, abandonedBody.read)

	if after := testutil.ToFloat64(metrics.SyncPutBlockAdmissionRejectedTotal.WithLabelValues(syncBlockRejectClientGone)); after != before+1 {
		t.Fatalf("client_gone rejections = %v, want %v", after, before+1)
	}

	free()
	<-done

	// The abandoned request must not have stranded the slot it was queued for.
	if err := requireDrainedLimiter(h.blockInflight); err != nil {
		t.Fatal(err)
	}
}

// TestSyncBlockInflightLimiterConcurrentReleaseDoesNotLeak hammers the limiter
// directly under -race with repeated releases, which is where a semaphore
// implementation strands slots or corrupts its per-user bookkeeping. Every
// counter must return to zero.
func TestSyncBlockInflightLimiterConcurrentReleaseDoesNotLeak(t *testing.T) {
	l := newSyncBlockInflightLimiter(syncInflightConfig(4, 16, 2*time.Second))
	if l == nil {
		t.Fatal("limiter not constructed")
	}

	const goroutines = 32
	const iterations = 200
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			key := "org|user-" + strconv.Itoa(g%6)
			for i := 0; i < iterations; i++ {
				release, reason := l.acquire(context.Background(), key)
				if release == nil {
					t.Errorf("acquire refused with a live wait budget: %s", reason)
					return
				}
				// Double release must be idempotent, not a second returned slot.
				release()
				release()
			}
		}(g)
	}
	wg.Wait()

	if err := requireDrainedLimiter(l); err != nil {
		t.Fatal(err)
	}
}

func TestSyncBlockInflightGatesShareOneWaitDeadline(t *testing.T) {
	const waitBudget = 200 * time.Millisecond
	l := newSyncBlockInflightLimiter(syncInflightConfig(1, 1, waitBudget))

	setupWait := newSyncBlockAdmissionWait(context.Background(), time.Second)
	releaseUser, reason := l.acquireUser(setupWait, "org|user")
	if releaseUser == nil {
		t.Fatalf("occupy user gate: %s", reason)
	}
	releaseNode, reason := l.acquireNode(setupWait)
	if releaseNode == nil {
		t.Fatalf("occupy node gate: %s", reason)
	}
	setupWait.close()
	defer releaseNode()

	wait := newSyncBlockAdmissionWait(context.Background(), waitBudget)
	defer wait.close()
	time.AfterFunc(120*time.Millisecond, releaseUser)
	start := time.Now()
	targetUserRelease, reason := l.acquireUser(wait, "org|user")
	if targetUserRelease == nil {
		t.Fatalf("user gate did not free within budget: %s", reason)
	}
	defer targetUserRelease()
	if targetNodeRelease, reason := l.acquireNode(wait); targetNodeRelease != nil {
		targetNodeRelease()
		t.Fatal("node gate unexpectedly admitted while its slot was held")
	} else if reason != syncBlockRejectNode {
		t.Fatalf("node rejection reason = %q, want %q", reason, syncBlockRejectNode)
	}
	elapsed := time.Since(start)
	if elapsed < waitBudget {
		t.Fatalf("both gates returned after %s, before shared budget %s", elapsed, waitBudget)
	}
	if elapsed > waitBudget+100*time.Millisecond {
		t.Fatalf("both gates took %s; deadline appears to restart at the node gate", elapsed)
	}
}

// TestSyncBlockInflightUserGatesAreEvicted pins the bound on the limiter's own
// memory: the per-user map must not grow with every user ever seen.
func TestSyncBlockInflightUserGatesAreEvicted(t *testing.T) {
	l := newSyncBlockInflightLimiter(syncInflightConfig(2, 8, time.Second))
	if l == nil {
		t.Fatal("limiter not constructed")
	}
	for i := 0; i < 500; i++ {
		release, reason := l.acquire(context.Background(), "org|user-"+strconv.Itoa(i))
		if release == nil {
			t.Fatalf("acquire refused: %s", reason)
		}
		release()
	}
	if err := requireDrainedLimiter(l); err != nil {
		t.Fatal(err)
	}
}

// TestSyncBlockInflightDisabledIsNoOp confirms turning the caps off restores the
// previous behaviour rather than refusing everything.
func TestSyncBlockInflightDisabledIsNoOp(t *testing.T) {
	if l := newSyncBlockInflightLimiter(syncInflightConfig(0, 0, time.Second)); l != nil {
		t.Fatal("both caps disabled should yield no limiter")
	}
	if l := newSyncBlockInflightLimiter(nil); l != nil {
		t.Fatal("nil config should yield no limiter")
	}

	// A nil limiter must admit through the same call path the handler uses.
	var nilLimiter *syncBlockInflightLimiter
	release, reason := nilLimiter.acquire(context.Background(), "org|user")
	if release == nil {
		t.Fatalf("nil limiter refused admission: %s", reason)
	}
	release()
}

// TestPutBlockAdmissionMetrics checks the counters an operator would actually
// page on, including that a refusal is attributed to the gate that fired.
func TestPutBlockAdmissionMetrics(t *testing.T) {
	h := newInflightTestHandler(t, syncInflightConfig(1, 1, 0))
	r := putBlockRouterFor(h, "org", "metrics-user")

	beforeUser := testutil.ToFloat64(metrics.SyncPutBlockAdmissionRejectedTotal.WithLabelValues(syncBlockRejectUser))

	free, done := startHeldBlockUpload(t, r)
	if got := testutil.ToFloat64(metrics.SyncPutBlockInflightCurrent); got < 1 {
		t.Fatalf("in-flight gauge = %v while an upload holds an admission, want at least 1", got)
	}

	if w, _ := putBlockObservingBody(r); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("refused upload = %d, want 503", w.Code)
	}

	free()
	<-done

	if after := testutil.ToFloat64(metrics.SyncPutBlockAdmissionRejectedTotal.WithLabelValues(syncBlockRejectUser)); after != beforeUser+1 {
		t.Fatalf("per-user rejections = %v, want %v", after, beforeUser+1)
	}
	if err := requireDrainedLimiter(h.blockInflight); err != nil {
		t.Fatal(err)
	}
}

// TestPutBlockCheapRejectionsSkipAdmission verifies the ordering inside the
// handler: an obviously oversized or malformed request must be refused without
// spending or waiting for a slot, so junk cannot queue behind real work.
func TestPutBlockCheapRejectionsSkipAdmission(t *testing.T) {
	h := newInflightTestHandler(t, syncInflightConfig(1, 1, 30*time.Second))
	r := putBlockRouterFor(h, "org", "user")

	// Occupy the only slot for the whole subtest.
	free, done := startHeldBlockUpload(t, r)
	defer func() {
		free()
		<-done
	}()

	cases := []struct {
		name     string
		blockID  string
		declared int64
		want     int
	}{
		{"oversized declared length", inflightTestBlockID, config.DefaultSyncBlockMaxBytes + 1, http.StatusRequestEntityTooLarge},
		{"malformed block id", "not-a-hash", 5, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPut, "/seafhttp/repo/repo/block/"+tc.blockID, strings.NewReader("small"))
			req.ContentLength = tc.declared
			w := httptest.NewRecorder()

			start := time.Now()
			r.ServeHTTP(w, req)
			elapsed := time.Since(start)

			if w.Code != tc.want {
				t.Fatalf("code = %d, want %d", w.Code, tc.want)
			}
			// If this had queued for the occupied slot it would have taken the
			// full 30s wait before answering.
			if elapsed > 2*time.Second {
				t.Fatalf("cheap rejection took %s; it queued for an admission it should never have asked for", elapsed)
			}
		})
	}
}

// requireDrainedLimiter asserts no admission survived its request: the node
// semaphore and global entry bound are empty and no per-user gate is registered.
func requireDrainedLimiter(l *syncBlockInflightLimiter) error {
	if l.node != nil && len(l.node) != 0 {
		return fmt.Errorf("in-flight admission leak: node gate still holds %d admissions", len(l.node))
	}
	if l.entries != nil && len(l.entries) != 0 {
		return fmt.Errorf("in-flight admission leak: global entry gate still holds %d requests", len(l.entries))
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.nodeWaiters != 0 {
		return fmt.Errorf("in-flight admission leak: node gate still has %d waiters", l.nodeWaiters)
	}
	for key, gate := range l.perUser {
		return fmt.Errorf("in-flight admission leak: per-user gate for %q survived with refs=%d, held=%d, waiters=%d", key, gate.refs, len(gate.sem), gate.waiters)
	}
	return nil
}

func requireWaiterCount(t *testing.T, l *syncBlockInflightLimiter, gateName string, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		l.mu.Lock()
		got := l.nodeWaiters
		if gateName == "user" {
			got = 0
			for _, gate := range l.perUser {
				got += gate.waiters
			}
		}
		l.mu.Unlock()
		if got == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("%s waiter count did not reach %d", gateName, want)
}

// TestEntryRingExhaustionHasItsOwnReason keeps the pre-gate entry ring
// distinguishable from the node waiter queue. Both mean "no room", but they ask
// the operator for opposite responses: a full waiter queue is a saturated node
// that wants capacity, while a full entry ring means the global admission
// envelope is full before further per-user state can be created. Folding them
// into one label would make the only signal an operator has ambiguous.
func TestEntryRingExhaustionHasItsOwnReason(t *testing.T) {
	cfg := syncInflightConfig(1, 1, time.Second)
	cfg.SeafHTTP.SyncBlockMaxWaitersPerNode = 1
	cfg.SeafHTTP.SyncBlockMaxWaitersPerUser = 1
	l := newSyncBlockInflightLimiter(cfg)
	if l == nil {
		t.Fatal("limiter not constructed")
	}

	// Entry ring capacity is node cap + node waiters = 2. Fill it with holders
	// that never release, then confirm the gauge sees them.
	var releases []func()
	for i := 0; i < 2; i++ {
		release, reason := l.acquireEntry(context.Background())
		if release == nil {
			t.Fatalf("entry %d refused with %q while the ring still had room", i, reason)
		}
		releases = append(releases, release)
	}
	if got := testutil.ToFloat64(metrics.SyncPutBlockEntriesCurrent); got < 2 {
		t.Fatalf("entries gauge = %v while 2 tickets are held, want at least 2", got)
	}

	release, reason := l.acquireEntry(context.Background())
	if release != nil {
		t.Fatal("entry ring admitted past its capacity")
	}
	if reason != syncBlockRejectEntryQueueFull {
		t.Fatalf("reason = %q, want %q; the entry ring must not report itself as the node waiter queue",
			reason, syncBlockRejectEntryQueueFull)
	}

	for _, r := range releases {
		r()
		r() // idempotent
	}
	if len(l.entries) != 0 {
		t.Fatalf("entry ring still holds %d tickets after release", len(l.entries))
	}
}
