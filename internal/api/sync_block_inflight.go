package api

import (
	"context"
	"log"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/metrics"
	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

// Subcontract B (= registry X10) of ISSUE-RATE-LIMIT-UPLOAD-DOWNLOAD-01: the
// aggregate bound on concurrent in-flight block readers.
//
// seafhttp.sync_block_max_bytes bounds ONE PutBlock body. It is not an aggregate
// bound: N concurrent uploads still cost N x the cap, so a few hundred
// authenticated PUTs could drive the process to gigabytes of buffered bodies.
// This limiter is the missing term. Admission is acquired *before*
// readLimitedRequestBody, so a request that never gets a slot never reaches
// io.ReadAll and costs nothing but a parked goroutine.
//
// Three properties this design is built around, and which the tests assert:
//
//  1. Bounded wait, then 503 — never 429. The official desktop client runs up to
//     5 sync tasks x 3 block threads, so ONE honest desktop can have ~15
//     concurrent PutBlock requests in flight. A limiter that rejects immediately
//     would punish that client. The client also classifies 502/503/504 as
//     network errors and retries them, but has no special handling for 429, so
//     429 on this route reads as a permanent failure. The anonymous upload-link
//     limiter (uploadLinkInflightLimiter) is deliberately the opposite on both
//     counts — non-blocking, and 429 — because it serves browser traffic.
//
//  2. Per-user gate first, node gate second. Both are held before the body is
//     read, so the memory ceiling (maxPerNode x sync_block_max_bytes, plus the
//     usual read/hash/HTTP overhead) does not depend on the order. Fairness
//     does: acquiring the node slot first would let one user park every node
//     slot while their own requests wait on the per-user gate — capacity
//     consumed without memory consumed, starving everyone else. Waiting on your
//     own gate holds nothing global, so the blast radius of one noisy user stays
//     inside their own budget.
//
//  3. One deadline for both gates, derived from the request context. Total wait
//     is bounded by waitTimeout rather than 2x it, and a client that disconnects
//     mid-wait cancels immediately: no timer and no goroutine outlives the
//     request.
//
// Like the upload-link caps, this is a process-local capacity guard, not a
// cluster-global quota: each node holds its own gates, so fleet-wide admission
// scales with the number of nodes.
type syncBlockInflightLimiter struct {
	// mu guards perUser and both waiter counters. The semaphore channels remain
	// the source of truth for active admissions.
	mu      sync.Mutex
	perUser map[string]*syncBlockUserGate

	// node is a counting semaphore: capacity is maxPerNode, a send takes a slot
	// and a receive returns one. Nil when the node cap is disabled.
	node chan struct{}

	maxPerUser        int
	maxPerNode        int
	maxWaitersPerUser int
	maxWaitersPerNode int
	nodeWaiters       int
	waitTimeout       time.Duration

	// logSample keeps at most one rejection log per interval. A rejected request
	// is cheap by design; logging every one would turn the defence into a
	// log-volume amplifier. The counters still move per rejection.
	logSample rate.Sometimes
}

// syncBlockUserGate is one user's slice of the per-user cap.
//
// refs counts holders *and* waiters, and is incremented before a caller blocks
// on sem. That is what makes eviction safe without a background sweeper: the
// entry can only be removed when nobody holds a slot and nobody is queued for
// one, so a waiter can never have its gate deleted out from under it.
type syncBlockUserGate struct {
	sem     chan struct{}
	refs    int
	waiters int
}

// Rejection reasons. These are metric label values, so the set is fixed and
// deliberately excludes user, org, repo and block identifiers.
const (
	syncBlockRejectUser          = "user"
	syncBlockRejectNode          = "node"
	syncBlockRejectClientGone    = "client_gone"
	syncBlockRejectUserQueueFull = "user_queue_full"
	syncBlockRejectNodeQueueFull = "node_queue_full"
)

// newSyncBlockInflightLimiter returns nil when every bound is disabled, which
// makes the whole limiter a no-op through the nil receiver rather than through a
// branch at each call site.
//
// A nil config yields a nil limiter rather than a default one: unlike
// syncBlockMaxBytes, failing open here does not restore an unbounded read (the
// per-request cap still applies), and handlers built without config are test
// fixtures that should not silently acquire admission.
func newSyncBlockInflightLimiter(cfg *config.Config) *syncBlockInflightLimiter {
	if cfg == nil {
		return nil
	}
	maxPerUser := cfg.SeafHTTP.SyncBlockMaxInflightPerUser
	maxPerNode := cfg.SeafHTTP.SyncBlockMaxInflightPerNode
	if maxPerUser <= 0 && maxPerNode <= 0 {
		return nil
	}
	l := &syncBlockInflightLimiter{
		perUser:           make(map[string]*syncBlockUserGate),
		maxPerUser:        maxPerUser,
		maxPerNode:        maxPerNode,
		maxWaitersPerUser: cfg.SeafHTTP.SyncBlockMaxWaitersPerUser,
		maxWaitersPerNode: cfg.SeafHTTP.SyncBlockMaxWaitersPerNode,
		waitTimeout:       cfg.SeafHTTP.SyncBlockAdmissionWait,
		logSample:         rate.Sometimes{Interval: time.Minute},
	}
	if maxPerNode > 0 {
		l.node = make(chan struct{}, maxPerNode)
	}
	return l
}

// retryAfterSeconds is what a refused client is told to wait. It tracks the
// admission wait rather than being a separate knob: a caller that waited the
// full budget and still found no slot should come back on roughly the timescale
// a slot plausibly frees up on. Floored at 1 because Retry-After has
// second granularity and 0 would mean "retry immediately", which is the churn
// this is meant to avoid.
func (l *syncBlockInflightLimiter) retryAfterSeconds() int {
	if l == nil || l.waitTimeout <= 0 {
		return 1
	}
	seconds := int(math.Ceil(l.waitTimeout.Seconds()))
	if seconds < 1 {
		return 1
	}
	return seconds
}

// acquire takes one admission for key, waiting up to waitTimeout for capacity.
//
// It returns either a non-nil release closure (safe to call more than once) or a
// non-empty reason. The caller must hold the admission until the buffered body
// is no longer referenced — through hashing and the storage write, not just the
// read — because that whole span is what the memory bound covers.
func (l *syncBlockInflightLimiter) acquire(ctx context.Context, key string) (func(), string) {
	if l == nil {
		return func() {}, ""
	}
	if ctx != nil && ctx.Err() != nil {
		return nil, syncBlockRejectClientGone
	}

	start := time.Now()
	// One deadline for both gates: the caller waits waitTimeout in total, not
	// once per gate. Deriving from the request context means a client
	// disconnect aborts the wait at once, and cancel() releases the timer on
	// every path.
	//
	// A zero wait is not a special case: WithTimeout(ctx, 0) is already expired,
	// so the non-blocking attempt still admits when capacity is free and a full
	// gate refuses immediately. That is exactly what waitTimeout: 0 means.
	wait := newSyncBlockAdmissionWait(ctx, l.waitTimeout)
	defer wait.close()

	releaseUser, reason := l.acquireUser(wait, key)
	if reason != "" {
		l.observeWait(start, false)
		return nil, reason
	}
	if wait.parent.Err() != nil {
		releaseUser()
		l.observeWait(start, false)
		return nil, syncBlockRejectClientGone
	}

	releaseNode, reason := l.acquireNode(wait)
	if reason != "" {
		// Hand back the gate we already hold before answering. Leaving it taken
		// would leak one slot of this user's budget per refused request.
		releaseUser()
		l.observeWait(start, false)
		return nil, reason
	}
	if wait.parent.Err() != nil {
		releaseNode()
		releaseUser()
		l.observeWait(start, false)
		return nil, syncBlockRejectClientGone
	}

	l.observeWait(start, true)
	metrics.SyncPutBlockInflightCurrent.Inc()

	var once sync.Once
	return func() {
		once.Do(func() {
			metrics.SyncPutBlockInflightCurrent.Dec()
			releaseNode()
			releaseUser()
		})
	}, ""
}

// syncBlockAdmissionWait creates its deadline lazily. Queue-full requests are
// therefore rejected before allocating a timer, while both gates still share
// one deadline measured from the start of admission.
type syncBlockAdmissionWait struct {
	parent   context.Context
	deadline time.Time
	ctx      context.Context
	cancel   context.CancelFunc
}

func newSyncBlockAdmissionWait(parent context.Context, timeout time.Duration) *syncBlockAdmissionWait {
	if parent == nil {
		parent = context.Background()
	}
	return &syncBlockAdmissionWait{parent: parent, deadline: time.Now().Add(timeout)}
}

func (w *syncBlockAdmissionWait) context() context.Context {
	if w.ctx == nil {
		w.ctx, w.cancel = context.WithDeadline(w.parent, w.deadline)
	}
	return w.ctx
}

func (w *syncBlockAdmissionWait) close() {
	if w.cancel != nil {
		w.cancel()
	}
}

// acquireUser blocks on the calling user's own gate. Because refs is bumped
// before the wait, the gate cannot be evicted while somebody is queued on it.
func (l *syncBlockInflightLimiter) acquireUser(wait *syncBlockAdmissionWait, key string) (func(), string) {
	if l.maxPerUser <= 0 {
		return func() {}, ""
	}

	l.mu.Lock()
	gate := l.perUser[key]
	if gate == nil {
		gate = &syncBlockUserGate{sem: make(chan struct{}, l.maxPerUser)}
		l.perUser[key] = gate
	}
	dropRefLocked := func() {
		gate.refs--
		if gate.refs == 0 && l.perUser[key] == gate {
			delete(l.perUser, key)
		}
	}
	dropRef := func() {
		l.mu.Lock()
		dropRefLocked()
		l.mu.Unlock()
	}

	select {
	case gate.sem <- struct{}{}:
		gate.refs++
		l.mu.Unlock()
	default:
		if gate.waiters >= l.maxWaitersPerUser {
			if gate.refs == 0 {
				delete(l.perUser, key)
			}
			l.mu.Unlock()
			return nil, syncBlockRejectUserQueueFull
		}
		if l.nodeWaiters >= l.maxWaitersPerNode {
			if gate.refs == 0 {
				delete(l.perUser, key)
			}
			l.mu.Unlock()
			return nil, syncBlockRejectNodeQueueFull
		}
		gate.refs++
		gate.waiters++
		l.nodeWaiters++
		metrics.SyncPutBlockWaitersCurrent.WithLabelValues("user").Inc()
		metrics.SyncPutBlockWaitersCurrent.WithLabelValues("node").Inc()
		l.mu.Unlock()
		ctx := wait.context()
		select {
		case gate.sem <- struct{}{}:
		case <-ctx.Done():
			l.mu.Lock()
			gate.waiters--
			l.nodeWaiters--
			metrics.SyncPutBlockWaitersCurrent.WithLabelValues("user").Dec()
			metrics.SyncPutBlockWaitersCurrent.WithLabelValues("node").Dec()
			dropRefLocked()
			l.mu.Unlock()
			return nil, wait.rejectReason(syncBlockRejectUser)
		}
		l.mu.Lock()
		gate.waiters--
		l.nodeWaiters--
		metrics.SyncPutBlockWaitersCurrent.WithLabelValues("user").Dec()
		metrics.SyncPutBlockWaitersCurrent.WithLabelValues("node").Dec()
		l.mu.Unlock()
	}

	metrics.SyncPutBlockUserInflightOccupancy.Observe(float64(len(gate.sem)))

	return func() {
		<-gate.sem
		dropRef()
	}, ""
}

// acquireNode blocks on the process-wide gate. This is the one that fixes the
// memory ceiling, and it is taken last so a user queued on their own cap never
// occupies node capacity while waiting.
func (l *syncBlockInflightLimiter) acquireNode(wait *syncBlockAdmissionWait) (func(), string) {
	if l.node == nil {
		return func() {}, ""
	}

	l.mu.Lock()
	select {
	case l.node <- struct{}{}:
		l.mu.Unlock()
	default:
		if l.nodeWaiters >= l.maxWaitersPerNode {
			l.mu.Unlock()
			return nil, syncBlockRejectNodeQueueFull
		}
		l.nodeWaiters++
		metrics.SyncPutBlockWaitersCurrent.WithLabelValues("node").Inc()
		l.mu.Unlock()
		ctx := wait.context()
		select {
		case l.node <- struct{}{}:
		case <-ctx.Done():
			l.mu.Lock()
			l.nodeWaiters--
			metrics.SyncPutBlockWaitersCurrent.WithLabelValues("node").Dec()
			l.mu.Unlock()
			return nil, wait.rejectReason(syncBlockRejectNode)
		}
		l.mu.Lock()
		l.nodeWaiters--
		metrics.SyncPutBlockWaitersCurrent.WithLabelValues("node").Dec()
		l.mu.Unlock()
	}

	return func() {
		l.mu.Lock()
		<-l.node
		l.mu.Unlock()
	}, ""
}

// ctxRejectReason distinguishes "we ran out of admission budget" from "the
// client went away while queued". Only the former is a capacity signal; counting
// abandoned requests as capacity rejections would make the metric read as
// overload during ordinary client churn.
func (w *syncBlockAdmissionWait) rejectReason(capacityReason string) string {
	if w.parent.Err() != nil {
		return syncBlockRejectClientGone
	}
	return capacityReason
}

func (l *syncBlockInflightLimiter) observeWait(start time.Time, admitted bool) {
	outcome := "rejected"
	if admitted {
		outcome = "admitted"
	}
	metrics.SyncPutBlockAdmissionWaitSeconds.WithLabelValues(outcome).Observe(time.Since(start).Seconds())
}

// reject answers a request that could not be admitted.
//
// 503 rather than 429 is a client-contract decision, not a taste one: the
// desktop sync client retries 502/503/504 as transient network errors and has no
// 429 handling, so a 429 here would surface as a hard sync failure. See the type
// comment.
//
// A client that disconnected while queued gets no response at all — there is
// nobody left to read it, and writing one would only be a way to log a status
// code that never travelled.
func (l *syncBlockInflightLimiter) reject(c *gin.Context, reason string) {
	metrics.SyncPutBlockAdmissionRejectedTotal.WithLabelValues(reason).Inc()

	if reason == syncBlockRejectClientGone {
		c.Abort()
		return
	}

	retryAfter := l.retryAfterSeconds()
	l.logSample.Do(func() {
		log.Printf("[PutBlock] refusing block upload at process-local in-flight cap (reason=%s); sampled, see sync_put_block_admission_rejected_total", reason)
	})

	c.Header("Retry-After", strconv.Itoa(retryAfter))
	c.JSON(http.StatusServiceUnavailable, gin.H{
		"error":       "too many block uploads in progress, please try again shortly",
		"retry_after": retryAfter,
	})
}

// acquireSyncBlockInflight adapts the limiter to the handler. It returns a
// release closure and true when the request may proceed; when it returns false
// the response has already been written (or deliberately not written, for a
// vanished client) and the handler must return without touching the body.
func (h *SyncHandler) acquireSyncBlockInflight(c *gin.Context, orgID, userID string) (func(), bool) {
	if h == nil || h.blockInflight == nil {
		return func() {}, true
	}
	release, reason := h.blockInflight.acquire(c.Request.Context(), orgID+"|"+userID)
	if release == nil {
		h.blockInflight.reject(c, reason)
		return nil, false
	}
	return release, true
}
