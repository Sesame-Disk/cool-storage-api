package downloadadmission

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/metrics"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func enabledTestConfig() config.DownloadAdmissionConfig {
	return config.DownloadAdmissionConfig{
		Enabled:                true,
		MaxActivePerNode:       2,
		MaxActivePerAuthUser:   1,
		MaxActivePerLinkSource: 1,
		MaxActivePerClientLink: 1,
		MaxWaitersPerIdentity:  2,
		MaxWaitersPerNode:      2,
		AdmissionWait:          250 * time.Millisecond,
		PreparationDeadline:    time.Minute,
		IdleWriteTimeout:       time.Minute,
		RetryAfter:             2 * time.Second,
	}
}

func authenticatedRequest(t *testing.T, user string) AdmissionRequest {
	t.Helper()
	request, err := NewAuthenticatedRequest(ProfileFile, "org-1", user)
	if err != nil {
		t.Fatalf("NewAuthenticatedRequest(%q): %v", user, err)
	}
	return request
}

func publicLinkRequest(t *testing.T, source, ip string) AdmissionRequest {
	t.Helper()
	request, err := NewPublicLinkRequest(ProfileLinkRaw, source, ip)
	if err != nil {
		t.Fatalf("NewPublicLinkRequest(%q, %q): %v", source, ip, err)
	}
	return request
}

func acquireOrFatal(t *testing.T, coordinator *Coordinator, request AdmissionRequest) *Lease {
	t.Helper()
	lease, reason := coordinator.Acquire(context.Background(), request)
	if lease == nil {
		t.Fatalf("Acquire rejected with %q", reason)
	}
	return lease
}

type acquireOutcome struct {
	lease  *Lease
	reason RejectReason
}

func awaitCancelledWaiter(t *testing.T, cancel context.CancelFunc, result <-chan acquireOutcome) {
	t.Helper()
	cancel()
	select {
	case outcome := <-result:
		if outcome.lease != nil {
			outcome.lease.Release(ReleaseClientDisconnect)
			t.Fatalf("cancelled waiter was admitted with reason %q", outcome.reason)
		}
		if outcome.reason != RejectClientGone {
			t.Fatalf("cancelled waiter reason = %q, want %q", outcome.reason, RejectClientGone)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled waiter did not return")
	}
}

func waitForWaiters(t *testing.T, coordinator *Coordinator, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		coordinator.mu.Lock()
		got := len(coordinator.waiters)
		coordinator.mu.Unlock()
		if got == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	coordinator.mu.Lock()
	got := len(coordinator.waiters)
	coordinator.mu.Unlock()
	t.Fatalf("waiter count = %d, want %d", got, want)
}

func assertAdmissionMetricInvariants(t *testing.T) {
	t.Helper()
	active := testutil.ToFloat64(metrics.DownloadAdmissionActiveCurrent)
	entries := testutil.ToFloat64(metrics.DownloadAdmissionEntriesCurrent)
	waiters := testutil.ToFloat64(metrics.DownloadAdmissionWaitersCurrent)
	profileSum := float64(0)
	for _, profile := range allProfiles() {
		profileSum += testutil.ToFloat64(metrics.DownloadAdmissionActiveByProfile.WithLabelValues(string(profile)))
	}
	if active != profileSum {
		t.Fatalf("active metric %.0f != profile sum %.0f", active, profileSum)
	}
	if entries != active+waiters {
		t.Fatalf("entries metric %.0f != active %.0f + waiters %.0f", entries, active, waiters)
	}
}

func rejectedMetricTotal() float64 {
	reasons := []RejectReason{
		RejectNodeFull, RejectProfileFull, RejectAuthUserFull, RejectLinkSourceFull, RejectClientLinkFull,
		RejectNodeQueueFull, RejectAuthUserQueueFull, RejectLinkSourceQueueFull, RejectClientLinkQueueFull,
		RejectAdmissionTimeout, RejectClientGone, RejectInvalidRequest,
	}
	total := float64(0)
	for _, reason := range reasons {
		total += testutil.ToFloat64(metrics.DownloadAdmissionRejectedTotal.WithLabelValues(string(reason)))
	}
	return total
}

func TestAcquireIsAtomicAcrossNodeAndIdentity(t *testing.T) {
	cfg := enabledTestConfig()
	cfg.MaxActivePerNode = 2
	cfg.AdmissionWait = 0
	c, err := New(&cfg)
	if err != nil {
		t.Fatal(err)
	}

	first := acquireOrFatal(t, c, authenticatedRequest(t, "user-1"))
	defer first.Release(ReleaseCompleted)

	_, reason := c.Acquire(context.Background(), authenticatedRequest(t, "user-1"))
	if reason != RejectAuthUserFull {
		t.Fatalf("same-user rejection = %q, want %q", reason, RejectAuthUserFull)
	}

	second := acquireOrFatal(t, c, authenticatedRequest(t, "user-2"))
	second.Release(ReleaseCompleted)

	c.mu.Lock()
	active, profileActive := c.active, c.activeByProfile[ProfileFile]
	c.mu.Unlock()
	if active != 1 || profileActive != 1 {
		t.Fatalf("active state after release = (%d, %d), want (1, 1)", active, profileActive)
	}
}

func TestProfileCapIsASeparateAtomicGate(t *testing.T) {
	cfg := enabledTestConfig()
	cfg.MaxActivePerNode = 2
	cfg.MaxActiveFile = 1
	cfg.AdmissionWait = 0
	c, err := New(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	first := acquireOrFatal(t, c, authenticatedRequest(t, "user-1"))
	defer first.Release(ReleaseCompleted)

	_, reason := c.Acquire(context.Background(), authenticatedRequest(t, "user-2"))
	if reason != RejectProfileFull {
		t.Fatalf("profile rejection = %q, want %q", reason, RejectProfileFull)
	}
	c.mu.Lock()
	active, profileActive := c.active, c.activeByProfile[ProfileFile]
	c.mu.Unlock()
	if active != 1 || profileActive != 1 {
		t.Fatalf("state after profile rejection = active %d, profile %d; want 1, 1", active, profileActive)
	}
}

func TestPublicLinkDoesNotConsumeOwnerUserAdmission(t *testing.T) {
	cfg := enabledTestConfig()
	cfg.MaxActivePerNode = 3
	cfg.AdmissionWait = 0
	c, err := New(&cfg)
	if err != nil {
		t.Fatal(err)
	}

	link := acquireOrFatal(t, c, publicLinkRequest(t, "source-1", "198.51.100.10"))
	defer link.Release(ReleaseCompleted)

	owner := acquireOrFatal(t, c, authenticatedRequest(t, "source-1"))
	defer owner.Release(ReleaseCompleted)
	assertAdmissionMetricInvariants(t)

	_, reason := c.Acquire(context.Background(), publicLinkRequest(t, "source-1", "198.51.100.11"))
	if reason != RejectLinkSourceFull {
		t.Fatalf("same-link rejection = %q, want %q", reason, RejectLinkSourceFull)
	}

	if len(link.request.dimension) != 2 || link.request.dimension[0].Kind != DimensionLinkSource || link.request.dimension[1].Kind != DimensionClientLink {
		t.Fatalf("public-link dimensions = %#v, want source and client-link dimensions", link.request.dimension)
	}
}

func TestWaiterIsAdmittedAfterAllGatesFree(t *testing.T) {
	cfg := enabledTestConfig()
	cfg.MaxActivePerNode = 1
	c, err := New(&cfg)
	if err != nil {
		t.Fatal(err)
	}

	holder := acquireOrFatal(t, c, authenticatedRequest(t, "user-1"))
	request := authenticatedRequest(t, "user-2")
	result := make(chan acquireOutcome, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		lease, reason := c.Acquire(ctx, request)
		result <- acquireOutcome{lease: lease, reason: reason}
	}()
	waitForWaiters(t, c, 1)
	assertAdmissionMetricInvariants(t)

	holder.Release(ReleaseCompleted)
	select {
	case outcome := <-result:
		if outcome.lease == nil || outcome.reason != "" {
			t.Fatalf("waiter outcome = (%v, %q), want lease", outcome.lease, outcome.reason)
		}
		outcome.lease.Release(ReleaseCompleted)
	case <-time.After(time.Second):
		t.Fatal("waiter was not admitted after release")
	}

	c.mu.Lock()
	active, waiters, identities := c.active, len(c.waiters), len(c.identities)
	c.mu.Unlock()
	if active != 0 || waiters != 0 || identities != 0 {
		t.Fatalf("coordinator state = active %d, waiters %d, identities %d; want all zero", active, waiters, identities)
	}
}

func TestSimultaneousIdentityCardinalityDrains(t *testing.T) {
	const activeIdentities = 1024
	cfg := enabledTestConfig()
	cfg.MaxActivePerNode = activeIdentities
	cfg.MaxActivePerAuthUser = 1
	cfg.MaxWaitersPerIdentity = 0
	cfg.MaxWaitersPerNode = 0
	c, err := New(&cfg)
	if err != nil {
		t.Fatal(err)
	}

	leases := make([]*Lease, 0, activeIdentities)
	for i := 0; i < activeIdentities; i++ {
		request, err := NewAuthenticatedRequest(ProfileFile, "org-1", fmt.Sprintf("simultaneous-%05d", i))
		if err != nil {
			t.Fatal(err)
		}
		leases = append(leases, acquireOrFatal(t, c, request))
	}
	c.mu.Lock()
	active, identities, tracked := c.active, len(c.identities), c.trackedIdentities[DimensionAuthUser]
	c.mu.Unlock()
	if active != activeIdentities || identities != activeIdentities || tracked != activeIdentities {
		t.Fatalf("simultaneous state = active %d, identities %d, tracked %d; want %d", active, identities, tracked, activeIdentities)
	}
	assertAdmissionMetricInvariants(t)

	for _, lease := range leases {
		lease.Release(ReleaseCompleted)
	}
	c.mu.Lock()
	active, identities, tracked = c.active, len(c.identities), c.trackedIdentities[DimensionAuthUser]
	c.mu.Unlock()
	if active != 0 || identities != 0 || tracked != 0 {
		t.Fatalf("state after simultaneous drain = active %d, identities %d, tracked %d; want zero", active, identities, tracked)
	}
	assertAdmissionMetricInvariants(t)
}

func TestSimultaneousWaiterCardinalityDrains(t *testing.T) {
	const waiterCount = 1024
	cfg := enabledTestConfig()
	cfg.MaxActivePerNode = 1
	cfg.MaxWaitersPerIdentity = 1
	cfg.MaxWaitersPerNode = waiterCount
	cfg.AdmissionWait = 10 * time.Second
	c, err := New(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	holder := acquireOrFatal(t, c, authenticatedRequest(t, "waiter-holder"))

	requests := make([]AdmissionRequest, waiterCount)
	for i := range requests {
		requests[i], err = NewAuthenticatedRequest(ProfileFile, "org-1", fmt.Sprintf("waiter-%05d", i))
		if err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	results := make(chan RejectReason, waiterCount)
	for _, request := range requests {
		go func(request AdmissionRequest) {
			_, reason := c.Acquire(ctx, request)
			results <- reason
		}(request)
	}
	waitForWaiters(t, c, waiterCount)
	c.mu.Lock()
	active, waiters, identities, tracked := c.active, len(c.waiters), len(c.identities), c.trackedIdentities[DimensionAuthUser]
	c.mu.Unlock()
	if active != 1 || waiters != waiterCount || identities != waiterCount+1 || tracked != waiterCount+1 {
		t.Fatalf("simultaneous waiter state = active %d, waiters %d, identities %d, tracked %d; want 1, %d, %d, %d", active, waiters, identities, tracked, waiterCount, waiterCount+1, waiterCount+1)
	}
	assertAdmissionMetricInvariants(t)

	cancel()
	for i := 0; i < waiterCount; i++ {
		select {
		case reason := <-results:
			if reason != RejectClientGone {
				t.Fatalf("cancelled waiter reason = %q, want %q", reason, RejectClientGone)
			}
		case <-time.After(time.Second):
			t.Fatal("waiter did not drain after cancellation")
		}
	}
	holder.Release(ReleaseCompleted)
	c.mu.Lock()
	active, waiters, identities, tracked = c.active, len(c.waiters), len(c.identities), c.trackedIdentities[DimensionAuthUser]
	c.mu.Unlock()
	if active != 0 || waiters != 0 || identities != 0 || tracked != 0 {
		t.Fatalf("state after waiter drain = active %d, waiters %d, identities %d, tracked %d; want zero", active, waiters, identities, tracked)
	}
}

func TestCancelledWaiterIsRemoved(t *testing.T) {
	cfg := enabledTestConfig()
	cfg.MaxActivePerNode = 1
	c, err := New(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	holder := acquireOrFatal(t, c, authenticatedRequest(t, "user-1"))
	request := authenticatedRequest(t, "user-2")

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan acquireOutcome, 1)
	go func() {
		lease, reason := c.Acquire(ctx, request)
		result <- acquireOutcome{lease: lease, reason: reason}
	}()
	waitForWaiters(t, c, 1)
	cancel()
	holder.Release(ReleaseCompleted)
	select {
	case outcome := <-result:
		if outcome.lease != nil {
			outcome.lease.Release(ReleaseClientDisconnect)
			t.Fatal("cancelled waiter was admitted after holder release")
		}
		if outcome.reason != RejectClientGone {
			t.Fatalf("cancelled waiter reason = %q, want %q", outcome.reason, RejectClientGone)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled waiter did not return")
	}
	waitForWaiters(t, c, 0)

	c.mu.Lock()
	if len(c.identities) != 0 {
		t.Fatalf("identity entries after cancellation and release = %d, want zero", len(c.identities))
	}
	c.mu.Unlock()
}

func TestQueueCapsRejectBeforeCreatingAnotherWaiter(t *testing.T) {
	cfg := enabledTestConfig()
	cfg.MaxActivePerNode = 1
	cfg.MaxWaitersPerNode = 1
	cfg.MaxWaitersPerIdentity = 1
	c, err := New(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	holder := acquireOrFatal(t, c, authenticatedRequest(t, "user-1"))
	request := authenticatedRequest(t, "user-2")

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan acquireOutcome, 1)
	go func() {
		lease, reason := c.Acquire(ctx, request)
		result <- acquireOutcome{lease: lease, reason: reason}
	}()
	waitForWaiters(t, c, 1)

	_, reason := c.Acquire(context.Background(), authenticatedRequest(t, "user-3"))
	if reason != RejectNodeQueueFull {
		t.Fatalf("node queue rejection = %q, want %q", reason, RejectNodeQueueFull)
	}
	awaitCancelledWaiter(t, cancel, result)
	holder.Release(ReleaseCompleted)
}

func TestIdentityQueueCapRejectsWithoutPartialAdmission(t *testing.T) {
	cfg := enabledTestConfig()
	cfg.MaxActivePerNode = 3
	cfg.MaxWaitersPerNode = 3
	cfg.MaxWaitersPerIdentity = 1
	c, err := New(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	holder := acquireOrFatal(t, c, authenticatedRequest(t, "user-1"))
	request := authenticatedRequest(t, "user-1")

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan acquireOutcome, 1)
	go func() {
		lease, reason := c.Acquire(ctx, request)
		result <- acquireOutcome{lease: lease, reason: reason}
	}()
	waitForWaiters(t, c, 1)

	_, reason := c.Acquire(context.Background(), authenticatedRequest(t, "user-1"))
	if reason != RejectAuthUserQueueFull {
		t.Fatalf("identity queue rejection = %q, want %q", reason, RejectAuthUserQueueFull)
	}
	c.mu.Lock()
	active, waiters := c.active, len(c.waiters)
	c.mu.Unlock()
	if active != 1 || waiters != 1 {
		t.Fatalf("state after queue rejection = active %d, waiters %d; want 1, 1", active, waiters)
	}
	awaitCancelledWaiter(t, cancel, result)
	holder.Release(ReleaseCompleted)
}

func TestExpiredWaiterIsNotAdmittedAfterHolderRelease(t *testing.T) {
	cfg := enabledTestConfig()
	cfg.MaxActivePerNode = 1
	cfg.AdmissionWait = 20 * time.Millisecond
	c, err := New(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	holder := acquireOrFatal(t, c, authenticatedRequest(t, "timeout-holder"))
	request := authenticatedRequest(t, "timeout-waiter")
	ctx := context.Background()
	result := make(chan acquireOutcome, 1)
	go func() {
		lease, reason := c.Acquire(ctx, request)
		result <- acquireOutcome{lease: lease, reason: reason}
	}()
	waitForWaiters(t, c, 1)
	time.Sleep(cfg.AdmissionWait + 20*time.Millisecond)
	holder.Release(ReleaseCompleted)
	select {
	case outcome := <-result:
		if outcome.lease != nil {
			outcome.lease.Release(ReleaseClientDisconnect)
			t.Fatal("expired waiter was admitted after holder release")
		}
		if outcome.reason != RejectAdmissionTimeout {
			t.Fatalf("expired waiter reason = %q, want %q", outcome.reason, RejectAdmissionTimeout)
		}
	case <-time.After(time.Second):
		t.Fatal("expired waiter did not return")
	}
}

func TestIdentityEntriesDrainAfterSequentialChurn(t *testing.T) {
	cfg := enabledTestConfig()
	cfg.MaxActivePerNode = 1
	cfg.MaxActivePerAuthUser = 1
	cfg.MaxWaitersPerIdentity = 0
	cfg.MaxWaitersPerNode = 0
	c, err := New(&cfg)
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 20000; i++ {
		request := authenticatedRequest(t, fmt.Sprintf("churn-%05d", i))
		lease := acquireOrFatal(t, c, request)
		lease.Release(ReleaseCompleted)
	}

	c.mu.Lock()
	active, waiters, identities := c.active, len(c.waiters), len(c.identities)
	c.mu.Unlock()
	if active != 0 || waiters != 0 || identities != 0 {
		t.Fatalf("state after churn = active %d, waiters %d, identities %d; want all zero", active, waiters, identities)
	}
}

func TestConcurrentAcquireReleaseContention(t *testing.T) {
	const workers = 32
	const iterations = 50
	cfg := enabledTestConfig()
	cfg.MaxActivePerNode = 8
	cfg.MaxActivePerAuthUser = 1
	cfg.MaxWaitersPerIdentity = 2
	cfg.MaxWaitersPerNode = 64
	cfg.AdmissionWait = 2 * time.Second
	c, err := New(&cfg)
	if err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	requests := make([]AdmissionRequest, workers)
	for worker := range requests {
		requests[worker] = authenticatedRequest(t, fmt.Sprintf("contended-%d", worker))
	}
	for worker := 0; worker < workers; worker++ {
		worker := worker
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			request := requests[worker]
			for iteration := 0; iteration < iterations; iteration++ {
				lease, reason := c.Acquire(context.Background(), request)
				if lease == nil {
					errs <- fmt.Errorf("worker %d iteration %d rejected with %q", worker, iteration, reason)
					return
				}
				lease.Release(ReleaseCompleted)
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	c.mu.Lock()
	active, waiters, identities := c.active, len(c.waiters), len(c.identities)
	c.mu.Unlock()
	if active != 0 || waiters != 0 || identities != 0 {
		t.Fatalf("state after contention = active %d, waiters %d, identities %d; want zero", active, waiters, identities)
	}
}

func TestLeaseReleaseIsIdempotent(t *testing.T) {
	cfg := enabledTestConfig()
	c, err := New(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	lease := acquireOrFatal(t, c, authenticatedRequest(t, "user-1"))
	lease.Release(ReleaseCompleted)
	lease.Release(ReleaseStorageError)

	c.mu.Lock()
	active, identities := c.active, len(c.identities)
	c.mu.Unlock()
	if active != 0 || identities != 0 {
		t.Fatalf("state after repeated release = active %d, identities %d; want zero", active, identities)
	}
}

func TestDisabledCoordinatorIsNoOp(t *testing.T) {
	cfg := config.DownloadAdmissionConfig{}
	c, err := New(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	lease, reason := c.Acquire(ctx, AdmissionRequest{})
	if lease == nil || reason != "" {
		t.Fatalf("disabled acquire = (%v, %q), want lease", lease, reason)
	}
	beforeDeadline := testutil.ToFloat64(metrics.DownloadAdmissionDeadlineExpiredTotal.WithLabelValues(string(DeadlinePreparation)))
	beforeWriter := testutil.ToFloat64(metrics.DownloadAdmissionWriterUnreachableTotal)
	lease.RecordDeadlineExpired(DeadlinePreparation)
	lease.RecordWriterUnreachable()
	lease.Release(ReleaseCompleted)
	if got := testutil.ToFloat64(metrics.DownloadAdmissionDeadlineExpiredTotal.WithLabelValues(string(DeadlinePreparation))); got != beforeDeadline {
		t.Fatalf("disabled coordinator changed deadline metric from %.0f to %.0f", beforeDeadline, got)
	}
	if got := testutil.ToFloat64(metrics.DownloadAdmissionWriterUnreachableTotal); got != beforeWriter {
		t.Fatalf("disabled coordinator changed writer metric from %.0f to %.0f", beforeWriter, got)
	}
	c.mu.Lock()
	active, identities := c.active, len(c.identities)
	c.mu.Unlock()
	if active != 0 || identities != 0 {
		t.Fatalf("disabled state = active %d, identities %d; want zero", active, identities)
	}
}

func TestInvalidRequestDoesNotCountAsCapacityRejection(t *testing.T) {
	cfg := enabledTestConfig()
	cfg.AdmissionWait = 0
	c, err := New(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	before := rejectedMetricTotal()
	lease, reason := c.Acquire(context.Background(), AdmissionRequest{})
	if lease != nil || reason != RejectInvalidRequest {
		t.Fatalf("invalid request outcome = (%v, %q), want nil, %q", lease, reason, RejectInvalidRequest)
	}
	after := rejectedMetricTotal()
	if after != before {
		t.Fatalf("invalid request changed rejection metrics from %.0f to %.0f", before, after)
	}
}

func TestRetryAfterSecondsRoundsUp(t *testing.T) {
	cfg := config.DownloadAdmissionConfig{RetryAfter: 1500 * time.Millisecond}
	c, err := New(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.RetryAfterSeconds(); got != 2 {
		t.Fatalf("RetryAfterSeconds() = %d, want 2", got)
	}
}

func TestRequestConstructorsRejectMissingIdentity(t *testing.T) {
	if _, err := NewAuthenticatedRequest(ProfileFile, "", "user"); err == nil {
		t.Fatal("empty organization was accepted")
	}
	if _, err := NewPublicLinkRequest(ProfileLinkRaw, "source", " "); err == nil {
		t.Fatal("empty normalized client IP was accepted")
	}
	if _, err := NewAuthenticatedRequest(Profile("unknown"), "org", "user"); err == nil {
		t.Fatal("unknown profile was accepted")
	}
	if err := validateRequest(AdmissionRequest{
		profile:   ProfileFile,
		dimension: []DimensionKey{{Kind: DimensionLinkSource, Scope: "public-link", ID: "source"}},
	}); err == nil {
		t.Fatal("link_source-only request was accepted")
	}
	if err := validateRequest(AdmissionRequest{
		profile: ProfileFile,
		dimension: []DimensionKey{
			{Kind: DimensionAuthUser, Scope: "org", ID: "user"},
			{Kind: DimensionClientLink, Scope: "198.51.100.1", ID: "source"},
		},
	}); err == nil {
		t.Fatal("auth-user plus client-link request was accepted")
	}
}

// lateCancelContext reports a live context to the first Err() caller and a
// cancelled one to every caller after that.
//
// Acquire checks Err() once before taking the coordinator mutex and once after
// holding it, so this reproduces a client that disconnects while its request is
// queued for the lock. A plain cancelled context cannot reach that window: the
// pre-lock check would refuse first and the test would pass even without the
// second check. Driving it off the call itself keeps the regression
// deterministic, with no sleep and no test hook in production code.
type lateCancelContext struct {
	context.Context
	mu    sync.Mutex
	calls int
	done  chan struct{}
}

func (l *lateCancelContext) Err() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls++
	if l.calls <= 1 {
		return nil
	}
	select {
	case <-l.done:
	default:
		close(l.done)
	}
	return context.Canceled
}

func (l *lateCancelContext) Done() <-chan struct{} { return l.done }

func TestRequestCancelledBeforeGrantDecisionIsNotAdmitted(t *testing.T) {
	cfg := enabledTestConfig()
	cfg.MaxActivePerNode = 4
	cfg.AdmissionWait = 0
	c, err := New(&cfg)
	if err != nil {
		t.Fatal(err)
	}

	// Capacity is entirely free, so the only thing that can refuse this request
	// is the cancellation observed at decision time.
	ctx := &lateCancelContext{Context: context.Background(), done: make(chan struct{})}
	lease, reason := c.Acquire(ctx, authenticatedRequest(t, "late-cancel"))
	if lease != nil {
		lease.Release(ReleaseClientDisconnect)
		t.Fatalf("a request cancelled before the grant decision was admitted (reason %q)", reason)
	}
	if reason != RejectClientGone {
		t.Fatalf("late-cancel reason = %q, want %q", reason, RejectClientGone)
	}

	c.mu.Lock()
	active, waiters, identities := c.active, len(c.waiters), len(c.identities)
	c.mu.Unlock()
	if active != 0 || waiters != 0 || identities != 0 {
		t.Fatalf("state after late cancellation = active %d, waiters %d, identities %d; want zero", active, waiters, identities)
	}
}

func TestRequestCancelledBeforeAcquireIsRefused(t *testing.T) {
	cfg := enabledTestConfig()
	cfg.MaxActivePerNode = 4
	cfg.AdmissionWait = 0
	c, err := New(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	lease, reason := c.Acquire(ctx, authenticatedRequest(t, "pre-cancelled"))
	if lease != nil {
		lease.Release(ReleaseClientDisconnect)
		t.Fatal("an already-cancelled request was admitted")
	}
	if reason != RejectClientGone {
		t.Fatalf("pre-cancelled reason = %q, want %q", reason, RejectClientGone)
	}
	c.mu.Lock()
	identities := len(c.identities)
	c.mu.Unlock()
	if identities != 0 {
		t.Fatalf("identity entries after pre-cancelled request = %d, want zero", identities)
	}
}
