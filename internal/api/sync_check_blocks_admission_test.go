package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
	"github.com/Sesame-Disk/sesamefs/internal/streaming"
)

// Subcontract C (= registry X11) of ISSUE-RATE-LIMIT-UPLOAD-DOWNLOAD-01.
//
// The parser cap was never the bound that mattered. What these tests pin is the
// work an *accepted* request can trigger: that admission happens before the body
// is buffered, that repeated ids cost one lookup rather than one each, that the
// lookup fan-out is bounded, that a vanished client actually stops the Cassandra
// reads, and that this route's budget is its own — a check-blocks storm must not
// be able to spend the block-upload admissions subcontract B established.

func checkBlocksTestConfig(perUser, perNode int, wait time.Duration) *config.Config {
	cfg := &config.Config{}
	cfg.SeafHTTP.CheckBlocksMaxIDs = config.DefaultCheckBlocksMaxIDs
	cfg.SeafHTTP.CheckBlocksMaxInflightPerUser = perUser
	cfg.SeafHTTP.CheckBlocksMaxInflightPerNode = perNode
	cfg.SeafHTTP.CheckBlocksMaxWaitersPerUser = config.DefaultCheckBlocksMaxWaitersPerUser
	cfg.SeafHTTP.CheckBlocksMaxWaitersPerNode = config.DefaultCheckBlocksMaxWaitersPerNode
	cfg.SeafHTTP.CheckBlocksAdmissionWait = wait
	cfg.SeafHTTP.CheckBlocksAdmittedLifetime = config.DefaultCheckBlocksAdmittedLifetime
	cfg.SeafHTTP.CheckBlocksLookupFanout = config.DefaultCheckBlocksLookupFanout
	// The block route is configured too, at its shipped defaults: the isolation
	// test needs both limiters to exist, and a config that quietly disabled the
	// block one would make that test pass for the wrong reason.
	cfg.SeafHTTP.SyncBlockMaxBytes = config.DefaultSyncBlockMaxBytes
	cfg.SeafHTTP.SyncBlockMaxInflightPerUser = config.DefaultSyncBlockMaxInflightPerUser
	cfg.SeafHTTP.SyncBlockMaxInflightPerNode = config.DefaultSyncBlockMaxInflightPerNode
	cfg.SeafHTTP.SyncBlockMaxWaitersPerUser = config.DefaultSyncBlockMaxWaitersPerUser
	cfg.SeafHTTP.SyncBlockMaxWaitersPerNode = config.DefaultSyncBlockMaxWaitersPerNode
	cfg.SeafHTTP.SyncBlockAdmissionWait = 0
	cfg.SeafHTTP.SyncBlockAdmittedLifetime = config.DefaultSyncBlockAdmittedLifetime
	return cfg
}

// newCheckBlocksTestHandler builds the handler through the real constructor, so
// a wiring regression that stops constructing the limiter fails here rather than
// passing vacuously.
func newCheckBlocksTestHandler(t *testing.T, cfg *config.Config) *SyncHandler {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h := NewSyncHandler(nil, nil, nil, cfg, nil)
	if h.checkBlocksInflight == nil {
		t.Fatal("check-blocks limiter was not constructed; the rest of this test would pass vacuously")
	}
	return h
}

func checkBlocksRouterFor(h *SyncHandler, orgID, userID string) *gin.Engine {
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("org_id", orgID)
		c.Set("user_id", userID)
		c.Next()
	})
	r.POST("/seafhttp/repo/:repo_id/check-blocks", h.CheckBlocks)
	r.PUT("/seafhttp/repo/:repo_id/block/:block_id", h.PutBlock)
	return r
}

// isAdmissionRefusal distinguishes "the limiter turned this away" from the other
// 503 this route can answer ("block storage not available", which a handler built
// without storage returns for any accepted request). Asserting on the status code
// alone would let these tests pass against a limiter that never ran.
func isAdmissionRefusal(w *httptest.ResponseRecorder) bool {
	return w.Code == http.StatusServiceUnavailable &&
		w.Header().Get("Retry-After") != "" &&
		strings.Contains(w.Body.String(), "in progress")
}

func postCheckBlocks(r *gin.Engine, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/seafhttp/repo/repo/check-blocks", strings.NewReader(body)))
	return w
}

func postCheckBlocksBody(r *gin.Engine, body *gatedEOFReader) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/seafhttp/repo/repo/check-blocks", body)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// startHeldCheckBlocks starts a request that is admitted and then parks inside
// its body read, so a second request meets a genuinely occupied slot rather than
// a simulated one.
func startHeldCheckBlocks(t *testing.T, r *gin.Engine) (func(), <-chan *httptest.ResponseRecorder) {
	t.Helper()
	body := &gatedEOFReader{started: make(chan struct{}), release: make(chan struct{})}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- postCheckBlocksBody(r, body) }()
	select {
	case <-body.started:
	case <-time.After(3 * time.Second):
		t.Fatal("check-blocks never reached its body read, so it never held an admission")
	}
	return func() { close(body.release) }, done
}

// TestCheckBlocksRefusedWithoutReadingBody is the core admission claim: a request
// that cannot be admitted never buffers its body, so a refusal costs nothing.
func TestCheckBlocksRefusedWithoutReadingBody(t *testing.T) {
	h := newCheckBlocksTestHandler(t, checkBlocksTestConfig(1, 1, 0))
	r := checkBlocksRouterFor(h, "org", "user")

	release, done := startHeldCheckBlocks(t, r)

	body := &observedEOFReader{read: make(chan struct{})}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/seafhttp/repo/repo/check-blocks", body))

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("refused check-blocks status = %d, want 503", w.Code)
	}
	select {
	case <-body.read:
		t.Fatal("refused check-blocks read its body; the work bound only holds if it does not")
	default:
	}
	if got := w.Header().Get("Retry-After"); got == "" {
		t.Fatal("refused check-blocks carried no Retry-After; the sync client needs one to back off")
	}

	release()
	<-done
	if err := requireDrainedLimiter(h.checkBlocksInflight); err != nil {
		t.Fatal(err)
	}
}

// TestCheckBlocksRefusalIsNever429 pins the client contract. The desktop sync
// client retries 502/503/504 and has no 429 handling, so a 429 here would
// surface as a hard sync failure rather than as backpressure.
func TestCheckBlocksRefusalIsNever429(t *testing.T) {
	h := newCheckBlocksTestHandler(t, checkBlocksTestConfig(1, 1, 0))
	r := checkBlocksRouterFor(h, "org", "user")

	release, done := startHeldCheckBlocks(t, r)
	defer func() { release(); <-done }()

	w := postCheckBlocks(r, "")
	if w.Code == http.StatusTooManyRequests {
		t.Fatal("check-blocks answered 429; the sync client treats that as permanent")
	}
	if !isAdmissionRefusal(w) {
		t.Fatalf("status = %d body = %s, want an admission refusal", w.Code, w.Body.String())
	}
	if _, err := strconv.Atoi(w.Header().Get("Retry-After")); err != nil {
		t.Fatalf("Retry-After = %q, want an integer number of seconds", w.Header().Get("Retry-After"))
	}
}

// TestCheckBlocksAdmissionIsSeparateFromBlockUploads is the reason these are two
// limiter instances rather than one. With the check-blocks node budget fully
// occupied, a block upload must still be admitted: the two routes exhaust
// different resources, and sharing capacity would let a check-blocks storm deny
// the uploads subcontract B was built to keep flowing.
func TestCheckBlocksAdmissionIsSeparateFromBlockUploads(t *testing.T) {
	h := newCheckBlocksTestHandler(t, checkBlocksTestConfig(1, 1, 0))
	r := checkBlocksRouterFor(h, "org", "user")

	release, done := startHeldCheckBlocks(t, r)
	defer func() { release(); <-done }()

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/seafhttp/repo/repo/block/"+inflightTestBlockID, strings.NewReader("")))
	if w.Code == http.StatusServiceUnavailable {
		t.Fatal("a saturated check-blocks budget refused a block upload; the two routes must hold separate capacity")
	}
}

// TestBlockUploadAdmissionIsSeparateFromCheckBlocks is the same claim in the
// other direction: block uploads at their cap must not refuse check-blocks.
func TestBlockUploadAdmissionIsSeparateFromCheckBlocks(t *testing.T) {
	cfg := checkBlocksTestConfig(2, 2, 0)
	cfg.SeafHTTP.SyncBlockMaxInflightPerUser = 1
	cfg.SeafHTTP.SyncBlockMaxInflightPerNode = 1
	h := newCheckBlocksTestHandler(t, cfg)
	r := checkBlocksRouterFor(h, "org", "user")

	release, done := startHeldBlockUpload(t, r)
	defer func() { release(); <-done }()

	if w := postCheckBlocks(r, ""); isAdmissionRefusal(w) {
		t.Fatal("a saturated block-upload budget refused check-blocks; the two routes must hold separate capacity")
	}
}

// TestCheckBlocksPerUserBudgetIsIsolated keeps one identity's saturation local.
// Keyed wrongly — or not keyed at all — one busy desktop would deny the route to
// every other user on the node, which is the outage the limiter exists to avoid.
func TestCheckBlocksPerUserBudgetIsIsolated(t *testing.T) {
	h := newCheckBlocksTestHandler(t, checkBlocksTestConfig(1, 4, 0))

	busy := checkBlocksRouterFor(h, "org", "busy-user")
	release, done := startHeldCheckBlocks(t, busy)
	defer func() { release(); <-done }()

	other := checkBlocksRouterFor(h, "org", "other-user")
	if w := postCheckBlocks(other, ""); isAdmissionRefusal(w) {
		t.Fatal("one user at their per-user cap refused a different user; the gate is not keyed per identity")
	}
}

// stubCheckBlocksMapping replaces the legacy SHA-1 mapping read with a counting
// stub and returns a snapshot function. The handler must be reachable without a
// live Cassandra session for these properties to be testable at all.
func stubCheckBlocksMapping(t *testing.T, resolve func(ctx context.Context, externalID string) (string, bool, error)) {
	t.Helper()
	old := syncGetBlockIDMappingFn
	t.Cleanup(func() { syncGetBlockIDMappingFn = old })
	syncGetBlockIDMappingFn = func(ctx context.Context, _ *db.DB, _, _, externalID string) (string, bool, error) {
		return resolve(ctx, externalID)
	}
}

// stubCheckBlocksCanonicalReader keeps the existence phase out of the way so the
// mapping phase is the only thing a test observes.
func stubCheckBlocksCanonicalReader(t *testing.T, exists map[string]bool) {
	t.Helper()
	old := syncNewCanonicalBlockCheckReaderFn
	t.Cleanup(func() { syncNewCanonicalBlockCheckReaderFn = old })
	syncNewCanonicalBlockCheckReaderFn = func(context.Context, *db.DB, *storage.Manager, string, []string, *storage.BlockStore, string, int) (streaming.CanonicalBlockReader, error) {
		return &syncCanonicalReaderStub{exists: exists}, nil
	}
}

// newMappingTestHandler builds a handler whose legacy-SHA-1 path is reachable:
// a non-nil db selects it, and the representation id is primed so resolution
// never touches a session.
func newMappingTestHandler(t *testing.T, cfg *config.Config) *SyncHandler {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h := NewSyncHandler(&db.DB{}, nil, storage.NewManager(), cfg, nil)
	h.blockRepresentationIDs.put("org", "repo", db.PlainBlockRepresentationID)
	return h
}

func sha1TestID(i int) string {
	return strings.Repeat("a", 36) + strings.ToLower(strconv.FormatInt(int64(0x1000+i), 16))
}

func internalForSHA1(external string) string {
	return strings.Repeat("b", 24) + external
}

// TestCheckBlocksResolvesEachUniqueIDOnce is the deduplication regression. Before
// it, a body of one id repeated N times cost N sequential Cassandra reads — the
// cheapest possible way to turn a 16 MiB request into six figures of database
// work.
func TestCheckBlocksResolvesEachUniqueIDOnce(t *testing.T) {
	const repeats = 500
	external := sha1TestID(1)

	var calls atomic.Int64
	stubCheckBlocksMapping(t, func(context.Context, string) (string, bool, error) {
		calls.Add(1)
		return internalForSHA1(external), true, nil
	})
	stubCheckBlocksCanonicalReader(t, map[string]bool{internalForSHA1(external): true})

	h := newMappingTestHandler(t, checkBlocksTestConfig(4, 8, 0))
	r := checkBlocksRouterFor(h, "org", "user")

	ids := make([]string, repeats)
	for i := range ids {
		ids[i] = external
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/seafhttp/repo/repo/check-blocks", strings.NewReader(strings.Join(ids, "\n"))))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("mapping reads = %d for %d copies of one id, want 1", got, repeats)
	}
}

// TestCheckBlocksMappingFanoutIsBounded pins the other half of the work bound.
// Resolving sequentially held one admission for the whole list; resolving with
// unbounded concurrency would just move the amplification from latency to
// instantaneous load on Cassandra. The configured fan-out is what makes the
// node's aggregate budget (node cap x fan-out) a real number.
func TestCheckBlocksMappingFanoutIsBounded(t *testing.T) {
	const (
		ids    = 64
		fanout = 4
	)

	// Every read parks until the test releases it. That is what makes the
	// observation sound: with the fan-out held open for the whole dispatch, an
	// unbounded implementation reveals itself immediately by putting a
	// (fanout+1)th read in flight, while a bounded one can never get there no
	// matter how the scheduler orders the goroutines.
	var (
		inflight     atomic.Int64
		maxSeen      atomic.Int64
		saturateOnce sync.Once
		overflowOnce sync.Once
	)
	saturated := make(chan struct{})
	overflowed := make(chan struct{})
	release := make(chan struct{})
	stubCheckBlocksMapping(t, func(_ context.Context, externalID string) (string, bool, error) {
		n := inflight.Add(1)
		for {
			seen := maxSeen.Load()
			if n <= seen || maxSeen.CompareAndSwap(seen, n) {
				break
			}
		}
		if n >= fanout {
			saturateOnce.Do(func() { close(saturated) })
		}
		if n > fanout {
			overflowOnce.Do(func() { close(overflowed) })
		}
		<-release
		inflight.Add(-1)
		return internalForSHA1(externalID), true, nil
	})
	stubCheckBlocksCanonicalReader(t, map[string]bool{})

	cfg := checkBlocksTestConfig(4, 8, 0)
	cfg.SeafHTTP.CheckBlocksLookupFanout = fanout
	h := newMappingTestHandler(t, cfg)
	r := checkBlocksRouterFor(h, "org", "user")

	body := make([]string, ids)
	for i := range body {
		body[i] = sha1TestID(i)
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- postCheckBlocks(r, strings.Join(body, "\n")) }()

	// The resolution must actually be concurrent: a serial implementation never
	// reaches the configured fan-out and fails here.
	select {
	case <-saturated:
	case <-time.After(3 * time.Second):
		close(release)
		<-done
		t.Fatalf("mapping reads never reached the configured fan-out of %d; the resolution is not concurrent", fanout)
	}
	// With the whole fan-out parked, a limiter that is not doing its job will have
	// dispatched the rest of the list by now.
	select {
	case <-overflowed:
		close(release)
		<-done
		t.Fatalf("peak concurrent mapping reads = %d, above the configured fan-out of %d", maxSeen.Load(), fanout)
	case <-time.After(300 * time.Millisecond):
	}

	close(release)
	w := <-done
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if got := maxSeen.Load(); got != fanout {
		t.Fatalf("peak concurrent mapping reads = %d, want exactly the configured fan-out of %d", got, fanout)
	}
}

// TestCheckBlocksCancellationStopsMappingReads is the property the contextless
// db.GetBlockIDMapping could not have: a client that goes away must stop the
// Cassandra work, not merely stop receiving its result. Without it, disconnecting
// mid-request was free for the attacker and fully paid for by the server.
func TestCheckBlocksCancellationStopsMappingReads(t *testing.T) {
	const (
		ids    = 64
		fanout = 4
	)

	var (
		started  atomic.Int64
		saturate = make(chan struct{})
		once     sync.Once
	)
	release := make(chan struct{})
	stubCheckBlocksMapping(t, func(ctx context.Context, externalID string) (string, bool, error) {
		if n := started.Add(1); n >= fanout {
			once.Do(func() { close(saturate) })
		}
		select {
		case <-release:
		case <-ctx.Done():
			return "", false, ctx.Err()
		}
		return internalForSHA1(externalID), true, nil
	})
	stubCheckBlocksCanonicalReader(t, map[string]bool{})

	cfg := checkBlocksTestConfig(4, 8, 0)
	cfg.SeafHTTP.CheckBlocksLookupFanout = fanout
	h := newMappingTestHandler(t, cfg)
	r := checkBlocksRouterFor(h, "org", "user")

	body := make([]string, ids)
	for i := range body {
		body[i] = sha1TestID(i)
	}
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/seafhttp/repo/repo/check-blocks", strings.NewReader(strings.Join(body, "\n"))).WithContext(ctx)

	done := make(chan struct{})
	go func() {
		defer close(done)
		r.ServeHTTP(httptest.NewRecorder(), req)
	}()

	select {
	case <-saturate:
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("mapping resolution never reached the configured fan-out")
	}

	// The client goes away with the fan-out saturated and the rest of the list
	// unresolved. Everything queued behind those in-flight reads must be
	// abandoned rather than run to completion.
	cancel()
	close(release)

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not return after the client disconnected")
	}

	if got := started.Load(); got > fanout {
		t.Fatalf("mapping reads issued = %d after the client disconnected at the fan-out of %d; queued lookups kept running", got, fanout)
	}
	if err := requireDrainedLimiter(h.checkBlocksInflight); err != nil {
		t.Fatal(err)
	}
}

// TestCheckBlocksIDCapIsConfigurable proves the cap is a knob rather than a
// constant, without changing what the shipped default accepts. Lowering it is
// how an operator responds to the traffic sync_check_blocks_ids_per_request
// reveals.
func TestCheckBlocksIDCapIsConfigurable(t *testing.T) {
	cfg := checkBlocksTestConfig(4, 8, 0)
	cfg.SeafHTTP.CheckBlocksMaxIDs = 10
	h := newCheckBlocksTestHandler(t, cfg)
	r := checkBlocksRouterFor(h, "org", "user")

	post := func(n int) int {
		ids := make([]string, n)
		for i := range ids {
			ids[i] = "a"
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/seafhttp/repo/repo/check-blocks", strings.NewReader(strings.Join(ids, "\n"))))
		return w.Code
	}

	if code := post(11); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("list one over the configured cap = %d, want 413", code)
	}
	if code := post(10); code == http.StatusRequestEntityTooLarge {
		t.Fatal("list exactly at the configured cap was rejected 413")
	}
}

// TestCheckBlocksAdmissionSurvivesRepeatedRequests catches the leak that turns a
// working limiter into an outage after N requests: an admission released on some
// paths but not others eventually pins the node at its cap with nothing running.
func TestCheckBlocksAdmissionSurvivesRepeatedRequests(t *testing.T) {
	h := newCheckBlocksTestHandler(t, checkBlocksTestConfig(1, 1, 0))
	r := checkBlocksRouterFor(h, "org", "user")

	for _, body := range []string{
		"",
		"not-a-block-id",
		strings.Repeat("a", 40),
		strings.Repeat("z", 64),
		`["` + strings.Repeat("b", 64) + `"]`,
		`[`,
	} {
		if w := postCheckBlocks(r, body); isAdmissionRefusal(w) {
			t.Fatalf("body %q was refused on a free node; an admission was leaked by an earlier request", body)
		}
		if err := requireDrainedLimiter(h.checkBlocksInflight); err != nil {
			t.Fatalf("after body %q: %v", body, err)
		}
	}
}
