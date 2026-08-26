package gc

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/metrics"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/google/uuid"
)

type gcFailureCoder interface {
	error
	FailureCode() string
}

var (
	errS3OrphanCanonicalMissing = errors.New("canonical S3 orphan disappeared")
	errS3OrphanCanonicalChanged = errors.New("canonical S3 orphan state changed")
)

type libraryHardDeleteInProgressError struct {
	LibraryID uuid.UUID
	ItemID    string
}

type hardDeleteInProgressError struct {
	Kind   string
	Target string
	ItemID string
}

func (e libraryHardDeleteInProgressError) Error() string {
	return fmt.Sprintf("library %s hard delete already in progress for child %s", e.LibraryID, e.ItemID)
}

func (e libraryHardDeleteInProgressError) FailureCode() string {
	return GCFailureCodeLibraryHardDeleteInProgress
}

func (e hardDeleteInProgressError) Error() string {
	return fmt.Sprintf("%s %s hard delete already in progress for item %s", e.Kind, e.Target, e.ItemID)
}

func (e hardDeleteInProgressError) FailureCode() string {
	return GCFailureCodeLibraryHardDeleteInProgress
}

func failureCodeForError(err error) string {
	var coded gcFailureCoder
	if errors.As(err, &coded) {
		return coded.FailureCode()
	}
	return GCFailureCodeNone
}

func isHardDeleteInProgressError(err error) bool {
	return failureCodeForError(err) == GCFailureCodeLibraryHardDeleteInProgress
}

// failedClosedError marks a refusal to delete that says nothing about the item and
// everything about the environment: an unreachable datacenter, or a replication map
// that no longer supports the per-DC EACH_QUORUM argument
// (ISSUE-GC-CROSS-DC-REFERENCE-VISIBILITY-01).
//
// The distinction matters for the retry budget. Making the destructive verify global
// turned "a DC is unreachable" from a rare local error into a systematic, fleet-wide
// one: every block in flight fails on the same tick, for the same reason, and would
// burn all five retries within a few passes of a single outage. From the DLQ,
// ItemBlock is not auto-recoverable — the classifier only rescues commit/fs_object
// rows blocked on a library hard delete — and the scanner's day cursor has already
// advanced past those candidates, so nothing would ever rediscover them. A short
// outage would quietly convert a stall into permanently uncollectable storage.
//
// So these failures postpone instead: same treatment as lock contention, no retry
// increment, no DLQ. Failing closed must cost latency, not the work item.
type failedClosedError struct {
	Reason string
	ItemID string
	Err    error
}

func (e failedClosedError) Error() string {
	return fmt.Sprintf("%s for %s: %v", e.Reason, e.ItemID, e.Err)
}

func (e failedClosedError) Unwrap() error { return e.Err }

func (e failedClosedError) FailureCode() string {
	return GCFailureCodeDestructiveFailClosed
}

// blockCandidateAuthorityInvalidError says a destructive step was refused because the
// physical identity that would have authorized it is unusable — a candidate row or a
// canonical row present but carrying no storage class or no storage key.
//
// It postpones rather than failing the item, and that is deliberate in BOTH directions.
// Nothing destructive may run on an identity that cannot be named, and nothing may
// consume the candidate either: dropping it would remove the only work item able to
// revisit the block, so a fence standing on it could never be lifted. With a clean
// deploy this is unreachable — EnsureBlockGCCandidate refuses to write such a row — so
// seeing it means something wrote the table behind that helper's back.
type blockCandidateAuthorityInvalidError struct {
	ItemID string
}

func (e blockCandidateAuthorityInvalidError) Error() string {
	return fmt.Sprintf("block %s: physical identity is unusable as destructive authority", e.ItemID)
}

func (e blockCandidateAuthorityInvalidError) FailureCode() string {
	return GCFailureCodeBlockAuthorityInvalid
}

// blockClaimNotYetStaleError says the candidate cannot be settled yet because the
// block still carries a delete claim too young to hand back. Nothing is wrong; the
// item simply must not be consumed until the fence can be lifted. See
// BlockClaimTooFresh.
type blockClaimNotYetStaleError struct {
	ItemID string
}

func (e blockClaimNotYetStaleError) Error() string {
	return fmt.Sprintf("block %s carries a delete claim that is not yet stale enough to release", e.ItemID)
}

func (e blockClaimNotYetStaleError) FailureCode() string {
	return GCFailureCodeBlockClaimNotYetStale
}

// blockClaimReleaseUnconfirmedError says a stale delete claim was found on a live
// block and could not be handed back. The candidate must survive: it is the only work
// item that will ever lift that fence.
//
// Deliberately NOT routed through failClosedIfUnavailable. That helper postpones only
// what isClusterUnavailableError recognises and lets everything else spend the retry
// budget, which is the correct default everywhere in the walk EXCEPT here. Everywhere
// else, exhausting the budget parks an item in the DLQ where a human can see it, and
// the block is left untouched. Here it also leaves gc_state='deleting' standing on a
// block that is provably still referenced, with no work item left to clear it — a
// permanent upload fence on live content, produced by the very branch that exists to
// remove such fences. See GCFailureCodeBlockClaimReleaseUnconfirmed.
type blockClaimReleaseUnconfirmedError struct {
	ItemID string
	Err    error
}

func (e blockClaimReleaseUnconfirmedError) Error() string {
	return fmt.Sprintf("failed to release a stale delete claim for %s: %v", e.ItemID, e.Err)
}

func (e blockClaimReleaseUnconfirmedError) Unwrap() error { return e.Err }

func (e blockClaimReleaseUnconfirmedError) FailureCode() string {
	return GCFailureCodeBlockClaimReleaseUnconfirmed
}

// isClusterUnavailableError reports whether an error means "the cluster could not
// serve this right now" rather than anything about the item.
//
// It exists because the fail-closed reasoning is not specific to the EACH_QUORUM
// verify. That read is merely the FIRST call in the destructive walk that a
// datacenter outage breaks — but ClaimBlockDelete, BlockExists, GetBlockInfo and
// StartBlockDeleteOrphan break on the same outage for the same reason, and every one
// of them used to surface as a plain error: retry incremented, five passes, DLQ. And
// ItemBlock never leaves the DLQ (isAutoRecoverableFailedItem rescues only
// commit/fs_object rows blocked on a library hard delete) while the scanner's day
// cursor has already moved past the candidate. Protecting only the verify would have
// left the exact loss the protection exists to prevent reachable through the
// statement immediately before it.
//
// WHICH calls an outage actually breaks depends on the consistency each one uses,
// and that is a property of deployment configuration rather than something the worker
// can reason about. Do NOT read this as "a remote DC is down, therefore the claim
// fails": the serial consistency levels do not work that way. LOCAL_SERIAL takes a
// quorum among the local DC's replicas; SERIAL takes a GLOBAL quorum over all
// replicas of the token range — with RF 1 in each of three DCs that is 2 of 3, which
// one unreachable DC does not defeat. EACH_QUORUM is the level that requires a quorum
// in *every* DC, which is exactly why the destructive liveness read is the call that
// reliably fails first. This function's job is only to recognise the failure when it
// arrives, from whichever statement it arrives at.
//
// Scope is deliberately narrow: availability failures only. A malformed statement, an
// unknown column or a serialization bug must still consume its retry budget and reach
// the DLQ, because those DO say something about the item and a human needs to see
// them.
//
// KNOWN LIMITATION — the timeout codes are ambiguous. Unavailable and the
// connection-level sentinels genuinely mean "the cluster cannot serve this"; a read or
// write timeout can also mean a hot partition, LWT contention on one row, or a
// deadline that is simply too tight. Those are per-item conditions, and treating them
// as environmental means such an item postpones indefinitely instead of surfacing in
// the DLQ. They are included anyway because a partial outage does produce timeouts and
// losing work there is the worse failure.
//
// SUCH AN ITEM IS NOT INDIVIDUALLY VISIBLE, and an earlier version of this comment
// claimed otherwise. gc_destructive_last_blocked_timestamp_seconds is per PATH: one
// block timing out on a hot partition advances it, and the very next block's successful
// verify advances the liveness-success half past it again, so the alert reads healthy
// while that one item postpones forever. The pair reports whether a PATH can authorize
// deletes, which is what it was built for; it is not per-item observability and cannot
// substitute for it.
//
// Bounding the environmental postpones per item (after N, fall back to the ordinary
// retry path) needs a counter distinct from retry_count, which is a queue-protocol
// change and belongs to X1 rather than here. Until then the honest statement is that a
// persistently timing-out item stalls silently.
func isClusterUnavailableError(err error) bool {
	if err == nil {
		return false
	}
	// Server answered, saying it could not serve the request.
	var reqErr gocql.RequestError
	if errors.As(err, &reqErr) {
		switch reqErr.Code() {
		case gocql.ErrCodeUnavailable, gocql.ErrCodeOverloaded,
			gocql.ErrCodeReadTimeout, gocql.ErrCodeWriteTimeout:
			return true
		}
	}
	// Driver never got an answer at all. (gocql.ErrHostQueryFailed is deliberately
	// absent: the driver documents it as deprecated and never returned, so listing it
	// would only suggest a case that cannot occur.)
	for _, sentinel := range []error{
		gocql.ErrTimeoutNoResponse,
		gocql.ErrConnectionClosed,
		gocql.ErrNoConnections,
		gocql.ErrNoStreams,
		gocql.ErrNoHosts,
		gocql.ErrCannotFindHost,
	} {
		if errors.Is(err, sentinel) {
			return true
		}
	}
	return errors.Is(err, context.DeadlineExceeded)
}

// failClosedIfUnavailable converts a cluster-availability failure anywhere in the
// destructive walk into the same postpone-without-retry treatment the EACH_QUORUM
// verify gets, and leaves every other error alone. reason names the step so the log
// still says which statement failed.
func (w *Worker) failClosedIfUnavailable(reason, itemID string, err error) error {
	if err == nil {
		return nil
	}
	if !isClusterUnavailableError(err) {
		return fmt.Errorf("%s for %s: %w", reason, itemID, err)
	}
	metrics.GCErrorsTotal.WithLabelValues("cluster_unavailable").Inc()
	w.recordDestructiveBlocked(destructivePathBlock)
	log.Printf("[GC Worker] Block %s: %s failed because the cluster was unavailable; postponing without burning a retry: %v", itemID, reason, err)
	return failedClosedError{Reason: reason, ItemID: itemID, Err: err}
}

// releaseBlockClaim hands a delete claim back and, when it cannot confirm the claim is
// gone, refuses to let the caller spend the item's retry budget.
//
// THE RULE, which applies to every release in this walk and not just the stale one:
//
//	if a branch needs to leave the block usable and cannot confirm the fence came
//	off, the queue item must not be allowed to reach the DLQ.
//
// The reason is the same one that produced GCFailureCodeBlockClaimReleaseUnconfirmed
// for the pre-check branch, and it was a mistake to apply it only there. A release
// that fails for a non-availability reason used to surface as an ordinary error:
// retry, five passes, DLQ — which ItemBlock never leaves, past a scanner day cursor
// that has already moved on. What is left behind is gc_state='deleting' on a block
// this very walk may have just PROVEN to be still referenced, and
// BlockDeleteFenceActive then refuses every future upload of that content, forever.
//
// The site that made this reachable in practice is the re-referenced branch: the
// EACH_QUORUM verify says the block is alive, so the fence is standing on live data,
// and the local pre-check on the next pass can keep returning false — which is exactly
// the cross-datacenter divergence X2 is about — so the walk comes back here and burns
// the budget again rather than settling through the pre-check's safe path.
//
// THE RELEASE ERROR DOMINATES THE ORIGINAL ONE. Callers that were going to return some
// other failure (a ReadFailure from the verify, a malformed canonical row) must return
// this instead while it applies. Nothing is lost: the item is postponed rather than
// consumed, and once a later pass confirms the fence is off, the original error is
// reached again and spends its retries normally. Deciding queue policy from the
// original error while the fence is still up is what strands the block.
func (w *Worker) releaseBlockClaim(orgID uuid.UUID, blockID string, authority BlockDeleteAuthority) error {
	outcome, relErr := w.store.ReleaseBlockClaim(orgID, blockID, authority)
	if relErr == nil {
		// NOT-OWNER IS NOT A FAILURE, and treating it as one would be actively harmful.
		// Under per-attempt identity an attempt whose claim was taken over while it
		// worked is SUPPOSED to fail here: it holds no fence, so there is nothing left to
		// hand back and nothing to repair. Reporting it as an error would spend the
		// item's retry budget and — because this helper's error dominates the caller's
		// original one — bury the reason the walk was unwinding in the first place.
		metrics.GCBlockDeleteClaimTotal.WithLabelValues("release_" + outcome.String()).Inc()
		if outcome == BlockReleaseNotOwner {
			log.Printf("[GC Worker] Block %s: delete claim %s was already gone at release time (taken over, released or finalized elsewhere); nothing to hand back", blockID, authority.ClaimID)
		}
		return nil
	}
	if isClusterUnavailableError(relErr) {
		metrics.GCErrorsTotal.WithLabelValues("cluster_unavailable").Inc()
		w.recordDestructiveBlocked(destructivePathBlock)
		log.Printf("[GC Worker] Block %s: releasing the delete claim failed because the cluster was unavailable; postponing with the fence still up: %v", blockID, relErr)
	} else {
		metrics.GCErrorsTotal.WithLabelValues("block_claim_release_failed").Inc()
		log.Printf("[GC Worker] Block %s: releasing the delete claim failed for a non-availability reason; postponing rather than spending the retry that would strand this block behind the fence — this will NOT self-heal and needs a human: %v", blockID, relErr)
	}
	return blockClaimReleaseUnconfirmedError{ItemID: blockID, Err: relErr}
}

// recordDestructiveBlocked marks that a destructive path refused a delete because the
// environment could not authorize it.
//
// It only ever moves this path's "last refused" mark forward. Nothing here clears
// anything, and no state is scoped to a pass: the recovery half of the signal is
// recordDestructiveLivenessSuccess, and the two are compared by timestamp. See
// metrics.GCDestructiveLastBlockedTimestamp for why a single boolean cannot carry
// this and why a pass-scoped clear made an ongoing outage unalertable.
//
// path must be one of the destructivePath* constants.
func (w *Worker) recordDestructiveBlocked(path string) {
	metrics.GCAuditEventsTotal.WithLabelValues("gc_block_delete_failed_closed").Inc()
	metrics.GCDestructiveLastBlockedTimestamp.WithLabelValues(path).Set(prometheusTimestamp(w.clock()))
}

// recordDestructiveLivenessSuccess marks that this path completed the global
// EACH_QUORUM liveness read — the only statement whose success proves the environment
// can still authorize a destructive delete.
//
// Call it whenever that read RETURNS, before looking at what it found: a block that
// turns out to be still referenced is not a delete, but the read that established
// that is exactly the evidence this records. Gating it on a completed delete would
// leave a fleet whose candidates all turn out to be live permanently reading as
// blocked.
func (w *Worker) recordDestructiveLivenessSuccess(path string) {
	metrics.GCDestructiveLastLivenessSuccessTimestamp.WithLabelValues(path).Set(prometheusTimestamp(w.clock()))
}

// recordS3OrphanCanonicalReloadFailure records why the defense-in-depth canonical
// reload refused to continue. Only an error classified as unavailable by
// isClusterUnavailableError says the orphan path's environment could not authorize
// destructive work; missing, changed, and other read errors are item/projection
// conditions and must not move the blocked timestamp.
func (w *Worker) recordS3OrphanCanonicalReloadFailure(err error) {
	switch {
	case isClusterUnavailableError(err):
		metrics.GCErrorsTotal.WithLabelValues("s3_orphan_canonical_reload_unavailable").Inc()
		w.recordDestructiveBlocked(destructivePathOrphan)
	case errors.Is(err, errS3OrphanCanonicalMissing):
		metrics.GCErrorsTotal.WithLabelValues("s3_orphan_canonical_reload_missing").Inc()
	case errors.Is(err, errS3OrphanCanonicalChanged):
		metrics.GCErrorsTotal.WithLabelValues("s3_orphan_canonical_changed").Inc()
	default:
		metrics.GCErrorsTotal.WithLabelValues("s3_orphan_canonical_reload_failed").Inc()
	}
}

// prometheusTimestamp renders an instant as a Prometheus timestamp gauge value.
//
// Fractional on purpose, and load-bearing. The block path can record a liveness
// success and then a topology refusal inside the SAME processBlock walk — the
// commit-point gate runs a few statements after the global read — and the alert
// compares the two by value. At whole-second resolution those two events tie,
// `blocked > liveness_success` is false, and the alert misses precisely the failure
// mode where the global read works but the gate refuses. Worse, a systematic gate
// rejection ties on every single walk, so it would never alert at all.
//
// float64 holds about microsecond resolution at present epoch values, which orders
// events milliseconds apart comfortably. Tests asserting on this ordering must drive
// the worker with a clock that ADVANCES; the frozen `func() time.Time { return now }`
// idiom used elsewhere in this package makes every event in a walk tie regardless.
func prometheusTimestamp(t time.Time) float64 {
	return float64(t.UnixNano()) / 1e9
}

// shouldPostponeWithoutRetry covers every refusal that says nothing about the item
// and everything about timing or the environment. All of them requeue the work
// untouched rather than spending a retry on it: burning the budget on a condition the
// item cannot influence is how transient trouble turns into lost work.
//
// Postponed items are not re-read immediately: RequeueItem stamps queued_at=now and
// DequeueBatch only sees rows older than the grace period, so a postpone costs a full
// grace period of latency rather than spinning each tick.
func shouldPostponeWithoutRetry(err error) bool {
	switch failureCodeForError(err) {
	case GCFailureCodeLibraryHardDeleteInProgress,
		GCFailureCodeDestructiveFailClosed,
		GCFailureCodeBlockClaimNotYetStale,
		GCFailureCodeBlockClaimReleaseUnconfirmed:
		return true
	default:
		return false
	}
}

func isBlockNotFound(err error) bool {
	return errors.Is(err, gocql.ErrNotFound)
}

// s3DeleteRetryDelays is the backoff schedule used when S3 DeleteBlock fails.
// Total in-worker wait budget: 100 + 500 + 2000 = 2.6s across 3 retries.
// Exposed as a var so tests can shorten it.
var s3DeleteRetryDelays = []time.Duration{
	100 * time.Millisecond,
	500 * time.Millisecond,
	2 * time.Second,
}

var hardDeleteLockHeartbeatInterval = 30 * time.Minute
var hardDeleteLockStaleAfter = 3 * hardDeleteLockHeartbeatInterval
var fsObjectReferenceFenceInterval = 5 * time.Minute

type hardDeleteLease struct {
	stopCh  chan struct{}
	release func() error

	mu         sync.Mutex
	err        error
	closeOnce  sync.Once
	closedChan chan struct{}
}

func newHardDeleteLease(ctx context.Context, kind, target string, renew func() (bool, error), release func() error) *hardDeleteLease {
	lease := &hardDeleteLease{
		stopCh:     make(chan struct{}),
		release:    release,
		closedChan: make(chan struct{}),
	}
	go func() {
		defer close(lease.closedChan)
		ticker := time.NewTicker(hardDeleteLockHeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-lease.stopCh:
				return
			case <-ticker.C:
				applied, err := renew()
				if err != nil {
					lease.setErr(fmt.Errorf("renew %s hard-delete lock for %s: %w", kind, target, err))
					return
				}
				if !applied {
					lease.setErr(fmt.Errorf("%s hard-delete lock for %s lost during cascade", kind, target))
					return
				}
			}
		}
	}()
	return lease
}

func (l *hardDeleteLease) setErr(err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err == nil {
		l.err = err
	}
}

func (l *hardDeleteLease) Check() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.err
}

func (l *hardDeleteLease) Close() {
	l.closeOnce.Do(func() {
		close(l.stopCh)
		<-l.closedChan
		if err := l.release(); err != nil {
			l.setErr(err)
		}
	})
}

func (w *Worker) newTimedFence(fence func() error, interval time.Duration) func() error {
	lastFenceAt := time.Time{}
	return func() error {
		now := w.clock().UTC()
		if interval > 0 && !lastFenceAt.IsZero() && !now.Before(lastFenceAt) && now.Sub(lastFenceAt) < interval {
			return nil
		}
		if err := fence(); err != nil {
			return err
		}
		lastFenceAt = now
		return nil
	}
}

// Worker drains the gc_queue and deletes items from S3 and the database.
type Worker struct {
	store       GCStore
	storage     StorageProvider
	queue       *Queue
	batchSize   int
	gracePeriod time.Duration
	dryRun      atomic.Bool
	stats       *Stats
	clock       func() time.Time

	// destructiveTopologyGate guards every physical delete. Armed from the store in
	// NewWorker so it can never be absent by accident; see SetDestructiveTopologyGate.
	destructiveTopologyGate func() error

	// topologyGateMu protects the short-lived cache of a PASSING gate result.
	topologyGateMu      sync.Mutex
	topologyGateOKUntil time.Time
}

// destructiveTopologyGateTTL is how long a PASSING topology gate result may be
// reused before the keyspace metadata is read again.
//
// The gate must stay a runtime check — replication can be altered while the process
// runs, and a topology that stops supporting the per-DC EACH_QUORUM argument has to
// stop deletes then, not at the next restart. But "runtime" does not require "once
// per candidate": without a cache, a batch of N blocks issues N identical
// system_schema.keyspaces reads, and schema metadata does not change N times per
// batch.
//
// Only successes are cached. A failing gate is re-evaluated every single time, so
// recovery is immediate and the cache can never extend a refusal — the direction that
// would cost availability. A change to a bad topology is caught within one TTL, which
// is the same tick in practice.
const destructiveTopologyGateTTL = 30 * time.Second

// The two destructive paths, as reported by the gc_destructive_last_*_timestamp_seconds
// pair. They fail independently — the worker drains gc_queue, the scanner sweeps
// gc_s3_orphans — so each reports its own state rather than sharing one series where a
// clean pass on one would speak for the other.
//
// Aliased from the metrics package, which seeds both series for exactly these values
// at registration: defining them here independently would let the two drift into a
// path that is written but never seeded, and an unseeded series drops out of the
// alert's comparison silently.
const (
	destructivePathBlock  = metrics.GCDestructivePathBlock
	destructivePathOrphan = metrics.GCDestructivePathOrphan
)

// blockDeleteClaimStaleAfter is how long a gc_state='deleting' claim must have been
// held before another attempt may hand it back on the owner's behalf.
//
// Age is the only thing that distinguishes an abandoned claim from a live one here.
// claimID is derived from the candidate timestamp, so it identifies a candidate, not
// an attempt: it is shared by every attempt on the same candidate (concurrent ones
// included) and differs across candidates on the same block. Releasing by identity
// therefore fails in both directions — it can drop the fence under a live attempt
// that shares the id, and it cannot lift a claim from a candidate that no longer
// exists. The threshold only has to exceed the longest possible single processBlock
// walk — a handful of statements, each bounded by the driver timeout — so a wide
// margin costs nothing: it delays unwedging a genuinely abandoned claim, and never
// races a live one.
//
// TWO PRECONDITIONS, stated because the safety of releasing another attempt's claim
// rests on them rather than on anything this file can check:
//
//  1. No legitimate processBlock attempt can still be running under a claim this old.
//     The walk is a handful of statements, each bounded by the driver timeout, so the
//     margin here is three orders of magnitude — but a future change that adds a long
//     or unbounded operation between the claim and its release would invalidate it.
//  2. Application clocks are reasonably synchronised. gc_claimed_at is written from
//     the CLAIMING process's clock (ClaimBlockDelete) and compared against the
//     RELEASING process's clock, so a node running far ahead could judge a live claim
//     stale. NTP across application nodes is already an operational requirement for
//     Cassandra's own timestamps; this simply inherits it, with 15 minutes of slack.
//
// Both belong to the claim protocol, which X1 owns. They are recorded here so a
// redesign inherits the constraints instead of rediscovering them.
const blockDeleteClaimStaleAfter = 15 * time.Minute

// errBlockClaimUnsettled marks a claim whose outcome could not be established even by a
// serial-domain settling read. It is a sentinel rather than a message so the fail-closed
// path is greppable and cannot be confused with a driver error.
var errBlockClaimUnsettled = errors.New("block delete claim outcome could not be settled in the serial domain")

// WHY THE POST-CLAIM RELEASES ARE OWNER-EXACT, AND WHY THE PRE-CHECK ONE IS AGE-BASED
//
// processBlock releases a claim in five places, and they deliberately do not use the
// same rule. The pre-check path (before this attempt has claimed anything) releases
// only a claim old enough to be provably abandoned, because there it would be handing
// back a claim it never took. The four post-claim paths — failed global verify,
// re-referenced after claim, malformed canonical metadata, and a topology gate that
// rejects at the commit point — release THIS attempt's own claim, by exact authority.
//
// That used to be an unconditional release, and it had to be: claimID derived from the
// candidate timestamp, so it was shared by every attempt on one candidate, and worker A
// releasing after ITS verify failed could drop the fence while worker B was still
// deleting under the same id. The exposure was bounded by B's gc_s3_orphans row also
// being a fence, but the window was real and was recorded against X1.
//
// Per-attempt identity closes it structurally: A's release names A's own claim id and
// claimed_at, so it can only ever clear a fence A itself is holding. B's claim is a
// different id and simply does not match. Nothing here has to weigh "drop someone
// else's fence" against "fence the fleet during an outage" any more, because the two
// cases are now distinguishable at the CAS.
//
// What remains deliberate is that these releases still run on EVERY unwind path,
// including a systematic one. A failed global verify is not incidental: when a
// datacenter is unreachable every block in flight fails at once, and holding those
// claims would fence all of that content for the full staleness threshold. Releasing
// our own claim there is both safe and necessary.

// NewWorker creates a new GC worker.
func NewWorker(store GCStore, storage StorageProvider, queue *Queue, batchSize int, gracePeriod time.Duration, dryRun bool, stats *Stats) *Worker {
	worker := &Worker{
		store:       store,
		storage:     storage,
		queue:       queue,
		batchSize:   batchSize,
		gracePeriod: gracePeriod,
		stats:       stats,
		clock:       time.Now,
		// Armed unconditionally: ValidateDestructiveGCTopology is part of GCStore, so
		// every store answers it and no wiring mistake can leave the destructive path
		// ungated. Stores with no keyspace behind them (the mock) answer nil.
		destructiveTopologyGate: store.ValidateDestructiveGCTopology,
	}
	worker.dryRun.Store(dryRun)
	return worker
}

// SetDestructiveTopologyGate overrides the check that must pass before this worker
// may delete physical bytes. It stays a runtime check — keyspace replication can be
// altered while the process runs, and a topology that stops supporting the per-DC
// EACH_QUORUM argument must stop deletes then, not at the next restart. A passing
// result is reused for at most destructiveTopologyGateTTL; a failing one never is.
//
// The gate is already armed from the store by NewWorker; this exists so a test can
// substitute a specific rejection. Passing nil restores the store's own gate rather
// than removing the constraint — an unguarded destructive path is not a state this
// type offers. Swapping the gate drops any cached pass, so a test that installs a
// rejection sees it on the very next call.
func (w *Worker) SetDestructiveTopologyGate(gate func() error) {
	if gate == nil {
		gate = w.store.ValidateDestructiveGCTopology
	}
	w.topologyGateMu.Lock()
	defer w.topologyGateMu.Unlock()
	w.destructiveTopologyGate = gate
	w.topologyGateOKUntil = time.Time{}
}

// checkDestructiveTopology is the CHEAP form, used to filter candidates: it may reuse
// a passing result for up to destructiveTopologyGateTTL. Callers about to destroy
// bytes must use checkDestructiveTopologyFresh instead.
func (w *Worker) checkDestructiveTopology(path string) error {
	return w.evaluateDestructiveTopology(path, false)
}

// checkDestructiveTopologyFresh is the AUTHORITATIVE form: it ignores the cache and
// re-reads the live replication map.
//
// The distinction is the whole value of the second check. A processBlock walk takes
// milliseconds, so a commit-point check sharing the cheap form's 30s cache would
// almost always return the result the walk's OWN first check just stored — asserting
// nothing while looking like defence in depth. The only honest way to narrow the
// window between "topology approved" and "bytes destroyed" is to actually look again.
//
// The cost lands where it belongs. The cheap form runs per candidate, including the
// many that turn out to be still referenced and never reach a delete; this one runs
// only for blocks that are truly about to be destroyed, which is a far smaller set.
func (w *Worker) checkDestructiveTopologyFresh(path string) error {
	return w.evaluateDestructiveTopology(path, true)
}

// evaluateDestructiveTopology fails closed: any error, including an unreachable
// Cassandra, prevents the delete rather than being treated as a passing gate.
func (w *Worker) evaluateDestructiveTopology(path string, fresh bool) error {
	w.topologyGateMu.Lock()
	defer w.topologyGateMu.Unlock()

	if w.destructiveTopologyGate == nil {
		// Unreachable through NewWorker, which always arms it. Refusing rather than
		// passing keeps "no gate" from ever meaning "no constraint" — the shape this
		// whole guard exists to rule out.
		w.recordTopologyGateRejectionLocked(path)
		return fmt.Errorf("destructive topology gate is not armed on this worker")
	}
	now := w.clock()
	if !fresh && !w.topologyGateOKUntil.IsZero() && now.Before(w.topologyGateOKUntil) {
		return nil
	}
	if err := w.destructiveTopologyGate(); err != nil {
		// Not cached: a refusal is re-evaluated every time so recovery is immediate.
		w.topologyGateOKUntil = time.Time{}
		w.recordTopologyGateRejectionLocked(path)
		return err
	}
	w.topologyGateOKUntil = now.Add(destructiveTopologyGateTTL)
	// Deliberately not touching the liveness-success series here: a passing gate means
	// the topology still gives EACH_QUORUM its per-datacenter meaning, not that a
	// quorum is currently reachable in every datacenter. With a DC down the gate passes
	// and the read still fails, so only the read itself is evidence of recovery.
	return nil
}

func (w *Worker) recordTopologyGateRejectionLocked(path string) {
	metrics.GCErrorsTotal.WithLabelValues("destructive_topology_gate").Inc()
	w.recordDestructiveBlocked(path)
}

// ProcessOnce runs a single pass of the worker: find orgs with queued items,
// dequeue a batch for each, and process them.
func (w *Worker) ProcessOnce(ctx context.Context) (int, error) {
	orgs, err := w.queue.ListOrgsWithQueuedItems()
	if err != nil {
		return 0, fmt.Errorf("failed to list orgs: %w", err)
	}

	totalProcessed := 0
	for _, orgID := range orgs {
		select {
		case <-ctx.Done():
			return totalProcessed, ctx.Err()
		default:
		}

		n, err := w.processOrg(ctx, orgID)
		if err != nil {
			log.Printf("[GC Worker] Error processing org %s: %v", orgID, err)
			continue
		}
		totalProcessed += n
	}

	// No end-of-pass verdict on whether this path can delete. A pass that refused
	// nothing is not evidence of health — the common case during an outage is a pass
	// that attempted nothing at all, because the postponed candidates are still
	// waiting out their grace period. Only recordDestructiveLivenessSuccess, from the
	// read itself, says the environment can authorize again.
	return totalProcessed, nil
}

// ProcessOrgOnce processes a single org's queued items in one pass. It is the
// scoped counterpart to ProcessOnce (which fans out across every active org) so
// callers — notably integration tests that enqueue work under a synthetic org —
// can drive GC for exactly that org without dequeuing unrelated orgs' items. A
// worker wired with a nil or partial storage provider must never touch another
// org's real blocks (it would route their S3 deletes down the slow recovery
// path), and this is the entry point that guarantees that scoping.
func (w *Worker) ProcessOrgOnce(ctx context.Context, orgID uuid.UUID) (int, error) {
	return w.processOrg(ctx, orgID)
}

func (w *Worker) processOrg(ctx context.Context, orgID uuid.UUID) (int, error) {
	activeBefore := w.clock()
	items, err := w.queue.DequeueBatch(orgID, w.batchSize, w.gracePeriod)
	if err != nil {
		return 0, err
	}

	processed := 0
	for _, item := range items {
		select {
		case <-ctx.Done():
			return processed, ctx.Err()
		default:
		}

		if err := w.processItem(ctx, item); err != nil {
			log.Printf("[GC Worker] Failed to process item %s/%s (type=%s): %v",
				item.OrgID, item.ItemID, item.ItemType, err)
			metrics.GCErrorsTotal.WithLabelValues(string(item.ItemType)).Inc()

			// These postpone without burning a retry: the item is fine, the timing or
			// the environment is not. See shouldPostponeWithoutRetry.
			if shouldPostponeWithoutRetry(err) {
				if postponeErr := w.postponeItem(item); postponeErr != nil {
					log.Printf("[GC Worker] Failed to postpone item %s/%s without retry increment: %v",
						item.OrgID, item.ItemID, postponeErr)
				}
				continue
			}

			// Requeue transient failures, but move retry-capped items into the DLQ
			// so they stop polluting the live queue forever. If the requeue itself
			// reports an error, do NOT blindly escalate to the DLQ: RequeueItem is
			// a LoggedBatch (DELETE old + INSERT new) and Cassandra timeout /
			// unavailable responses are ambiguous — the batch may have applied
			// even though the client saw an error. Re-checking the original row
			// tells us which side of the ambiguity we landed on:
			//
			//   - row gone  → batch applied, new requeued row is live; do nothing.
			//   - row still → batch did not apply; safe to escalate to the DLQ.
			//   - check err → unknown; leave the item in place rather than risk
			//                 a duplicated processing path. Next tick will retry.
			if item.RetryCount < 5 {
				if incErr := w.queue.IncrementRetry(item); incErr != nil {
					log.Printf("[GC Worker] Failed to requeue item %s/%s after error %v: %v",
						item.OrgID, item.ItemID, err, incErr)
					stillExists, checkErr := w.store.QueueItemExists(item.OrgID, item.QueuedAt, item.ItemType, item.ItemID)
					if checkErr != nil {
						log.Printf("[GC Worker] Cannot verify requeue state for %s/%s (%v); leaving item untouched to avoid double-processing",
							item.OrgID, item.ItemID, checkErr)
						continue
					}
					if !stillExists {
						log.Printf("[GC Worker] IncrementRetry returned %v but old row is already gone for %s/%s; treating as successful requeue",
							incErr, item.OrgID, item.ItemID)
						continue
					}
					escalation := fmt.Sprintf("requeue failed (%v) after processing error: %v", incErr, err)
					if failErr := w.store.FailItem(item, w.clock(), escalation, failureCodeForError(err)); failErr != nil {
						log.Printf("[GC Worker] Failed to escalate item %s/%s to DLQ after requeue failure: %v", item.OrgID, item.ItemID, failErr)
					}
				}
			} else {
				if failErr := w.store.FailItem(item, w.clock(), err.Error(), failureCodeForError(err)); failErr != nil {
					log.Printf("[GC Worker] Failed to move retry-capped item %s/%s to DLQ: %v", item.OrgID, item.ItemID, failErr)
				}
			}
			continue
		}

		if w.dryRun.Load() {
			continue
		}

		// Remove from queue
		if err := w.queue.Complete(item.OrgID, item.QueuedAt, item.ItemType, item.ItemID); err != nil {
			log.Printf("[GC Worker] Failed to complete item %s/%s: %v",
				item.OrgID, item.ItemID, err)
		}

		metrics.GCItemsProcessedTotal.WithLabelValues(string(item.ItemType)).Inc()
		processed++
	}

	if len(items) < w.batchSize {
		if len(items) > 0 {
			stats, statsErr := w.store.GetOrgQueueStats(orgID)
			if statsErr != nil {
				log.Printf("[GC Worker] Failed to read queue snapshot for org %s: %v", orgID, statsErr)
			} else if stats.QueueDepth > 0 {
				return processed, nil
			}
		}

		oldestQueuedAt, oldestErr := w.store.GetOldestQueuedAt(orgID)
		if oldestErr != nil {
			log.Printf("[GC Worker] Failed to inspect remaining queue state for org %s: %v", orgID, oldestErr)
			return processed, nil
		}
		if oldestQueuedAt == nil {
			if activeErr := w.store.RemoveOrgFromActiveSet(orgID, activeBefore); activeErr != nil {
				log.Printf("[GC Worker] Failed to remove org %s from active set: %v", orgID, activeErr)
			}
		}
	}

	return processed, nil
}

func (w *Worker) postponeItem(item QueueItem) error {
	return w.store.RequeueItem(
		item.OrgID,
		item.QueuedAt,
		w.clock(),
		item.ItemType,
		item.ItemID,
		item.LibraryID,
		item.BlockRepresentationID,
		item.StorageClass,
		item.RetryCount,
		effectiveIdentityAt(item.QueuedAt, item.IdentityAt),
		item.RequiresLibraryDeletedCheck,
		item.LibraryGuardMode,
	)
}

func (w *Worker) processItem(ctx context.Context, item QueueItem) error {
	switch item.ItemType {
	case ItemBlock:
		return w.processBlock(ctx, item)
	case ItemCommit:
		return w.processCommit(item)
	case ItemFSObject:
		return w.processFSObject(ctx, item)
	case ItemShareLink:
		return w.processShareLink(ctx, item)
	case ItemShare:
		return w.processShare(ctx, item)
	case ItemRestoreJob:
		return w.processRestoreJob(ctx, item)
	case ItemUserCascade:
		return w.processUserCascade(ctx, item)
	case ItemLibraryCascade:
		return w.processLibraryCascade(ctx, item)
	case ItemOrgCascade:
		return w.processOrgCascade(ctx, item)
	default:
		return fmt.Errorf("unknown item type: %s", item.ItemType)
	}
}

func (w *Worker) processBlock(ctx context.Context, item QueueItem) error {
	if w.dryRun.Load() {
		log.Printf("[GC Worker] DRY RUN: Would conditionally delete block %s from DB and S3", item.ItemID)
		return nil
	}

	// THE CANDIDATE IS THE AUTHORITY FOR WHICH PHYSICAL INCARNATION MAY BE DESTROYED,
	// and it is deliberately not re-derived from `blocks` here. Re-reading the canonical
	// row at this point would simply observe whatever incarnation is installed NOW and
	// authorize deleting that — which is exactly the ABA defect R14 names: the candidate
	// was enqueued for P1, P1 died, P2 was minted onto the same logical block, and
	// nothing ever decided that P2 was garbage.
	//
	// candidate_at comes from the same row for the same reason: it is what the candidate
	// cleanup CAS is bound to, so a value carried on the queue item (which can outlive
	// the candidate it was built from) must not stand in for it.
	candidate, candidateFound, err := w.store.GetBlockGCCandidate(item.OrgID, item.ItemID)
	if err != nil {
		return w.failClosedIfUnavailable("failed to load block GC candidate authority", item.ItemID, err)
	}

	// A FRESH UUID PER ATTEMPT, never a value derived from candidate_at. The old
	// candidate-derived id was shared by every attempt on one candidate — concurrent
	// ones included — so the claim CAS answered "applied" to both workers and either
	// could release or finalize under the other. candidate_at still orders discovery and
	// measures the grace period; it is simply no longer an ownership token.
	attempt := BlockDeleteAuthority{
		Target:    candidate.Target,
		ClaimID:   uuid.NewString(),
		ClaimedAt: w.clock().UTC(),
	}

	// Pre-check: a block is alive iff it still has reference rows. This single-
	// partition point read replaces the old per-org full scan of live fs_objects.
	//
	// Deliberately the LOCAL_QUORUM form. The zero-check is asymmetric: a locally
	// visible row is proof that the block is alive, so aborting here is always
	// correct, while a local zero proves nothing and authorizes nothing — it only
	// lets the walk continue to the claim and the global verify below. Raising this
	// read would be correct too, but it would pay WAN on every candidate to learn
	// something the verify has to re-establish anyway.
	//
	// This runs BEFORE the topology gate on purpose: a locally visible reference
	// settles the item without any destructive step, so it should not need schema
	// metadata, and the worker keeps draining discardable candidates even while the
	// gate would fail.
	hasRefs, err := w.store.BlockHasReferences(item.OrgID, item.ItemID)
	if err != nil {
		return w.failClosedIfUnavailable("failed to check block references", item.ItemID, err)
	}
	if hasRefs {
		log.Printf("[GC Worker] Block %s still referenced, skipping deletion", item.ItemID)
		// An earlier attempt on this same candidate may have claimed the row and then
		// died before releasing it (a crash between the claim and the verify does
		// exactly that). This is the last pass that will ever look at this candidate,
		// so if such a claim is left behind it has to come off here: gc_state would
		// otherwise stay 'deleting' forever and BlockDeleteFenceActive would refuse
		// every future upload of this content.
		//
		// Only a STALE claim, though — but any owner's. An unconditional release could
		// hand back a claim a concurrent worker is still deleting under, dropping the
		// fence in exactly the window it exists to cover; an owner-only release would
		// leave a claim from an abandoned candidate up forever. Age separates the two
		// cleanly, because nothing live survives blockDeleteClaimStaleAfter.
		//
		// OWNER-AGNOSTIC IS NOT THE SAME AS INCARNATION-AGNOSTIC, and the difference is
		// load-bearing. This path may lift a fence whose owner will never return, but only
		// on the incarnation THIS candidate names. `blocks` can perfectly ordinarily hold
		// a different life by now — P1 died, P2 was installed, a lifecycle for P2 claimed
		// it and was abandoned — and releasing that fence would be a worker authorized for
		// P1 acting on P2. P2's own candidate survives any standing claim (nothing here
		// consumes a candidate while a fence is up), so P2's fence gets lifted by P2's
		// work item, not by ours.
		//
		// With no candidate there is no incarnation to name and therefore no authority to
		// act on: skip the release entirely rather than falling back to a looser one.
		var outcome BlockClaimReleaseOutcome
		var relErr error
		if candidateFound {
			outcome, relErr = w.store.ReleaseStaleBlockClaim(item.OrgID, item.ItemID, candidate.Target, w.clock().Add(-blockDeleteClaimStaleAfter))
		} else {
			log.Printf("[GC Worker] Block %s is referenced and has no GC candidate; no incarnation is named, so any standing fence is left for the candidate that owns it", item.ItemID)
		}
		if relErr != nil {
			// Postpone for EVERY failure reason, not just the ones
			// isClusterUnavailableError recognises. "Do not clear the candidate" is
			// necessary but nowhere near sufficient: the candidate ROW survives an
			// ordinary error too, and it still ends up unreachable, because the queue
			// item burns its five retries into the DLQ, ItemBlock never auto-recovers
			// from there, and the scanner's day cursor has already stepped past this
			// candidate's bucket. The fence would then stand on a live block with
			// nothing left in the system able to lift it.
			//
			// So the classifier is used for the SIGNAL, not for the queue policy —
			// those come apart here, uniquely in this walk. An availability failure
			// keeps the existing cluster_unavailable accounting and moves this path's
			// blocked mark; a permanent one gets its own reason label and leaves the
			// blocked mark alone, because one broken row does not answer "can this path
			// still authorize deletes" (the same rule RecoverS3Orphans applies).
			if isClusterUnavailableError(relErr) {
				metrics.GCErrorsTotal.WithLabelValues("cluster_unavailable").Inc()
				w.recordDestructiveBlocked(destructivePathBlock)
				log.Printf("[GC Worker] Block %s: releasing a stale delete claim failed because the cluster was unavailable; postponing with the fence still up: %v", item.ItemID, relErr)
			} else {
				metrics.GCErrorsTotal.WithLabelValues("stale_claim_release_failed").Inc()
				log.Printf("[GC Worker] Block %s: releasing a stale delete claim failed for a non-availability reason; postponing rather than spending the retry that would strand this live block behind the fence — this will NOT self-heal and needs a human: %v", item.ItemID, relErr)
			}
			return blockClaimReleaseUnconfirmedError{ItemID: item.ItemID, Err: relErr}
		}
		switch outcome {
		case BlockClaimReleased:
			metrics.GCAuditEventsTotal.WithLabelValues("gc_block_stale_claim_released").Inc()
			log.Printf("[GC Worker] Block %s: released a stale delete claim left by an earlier attempt", item.ItemID)
		case BlockClaimTooFresh:
			// Same reasoning as the error above, for the case that looks like success.
			// The claim is too young to hand back — it may belong to a worker still
			// deleting — but it may equally belong to one that just died, and this
			// candidate is the only thing that will ever revisit the block. Settling
			// now would leave gc_state='deleting' with nothing left to clear it.
			// Postpone: no retry burned, no DLQ, and the next pass finds the claim
			// old enough to release.
			metrics.GCAuditEventsTotal.WithLabelValues("gc_block_claim_not_yet_stale").Inc()
			log.Printf("[GC Worker] Block %s is referenced but still carries a recent delete claim; postponing until it ages out", item.ItemID)
			return blockClaimNotYetStaleError{ItemID: item.ItemID}
		}
		if err := w.settleBlockCandidate(item, candidate, candidateFound); err != nil {
			return err
		}
		metrics.GCItemsSkippedTotal.Inc()
		return nil
	}

	// From here the walk can reach a destructive claim, and that needs an authority. A
	// queue item with no candidate row behind it has nothing that ever decided this
	// block was garbage — the candidate was already settled by another lifecycle, or the
	// item outlived it. Settle without touching `blocks`.
	if !candidateFound {
		metrics.GCBlockDeleteClaimTotal.WithLabelValues("no_candidate").Inc()
		log.Printf("[GC Worker] Block %s has no GC candidate row; nothing authorized its deletion, skipping", item.ItemID)
		metrics.GCItemsSkippedTotal.Inc()
		return nil
	}
	if candidate.Target.IsZero() {
		// A candidate that cannot name its incarnation can never authorize anything, and
		// it must not be consumed either: deleting it would drop the only work item that
		// could ever revisit this block. With a clean deploy this is unreachable —
		// EnsureBlockGCCandidate refuses to write such a row — so reaching it means the
		// table was written by something that bypassed that helper.
		metrics.GCBlockDeleteClaimTotal.WithLabelValues("invalid").Inc()
		log.Printf("[GC Worker] Block %s: GC candidate carries no exact physical incarnation; refusing every destructive step and postponing — this will NOT self-heal and needs a human", item.ItemID)
		return blockCandidateAuthorityInvalidError{ItemID: item.ItemID}
	}

	// THE GRACE PERIOD BELONGS TO THE CANDIDATE, NOT TO THE QUEUE ROW.
	//
	// DequeueBatch applies it to the queue item's own timestamp, which is right for an
	// item whose candidate has not changed. But a candidate that was REPLACED gets a new
	// candidate_at precisely so the new incarnation serves its own grace — and an old
	// queue row that already cleared grace would otherwise pick that fresh candidate up
	// and process it immediately, handing the new life exactly the head start replacement
	// exists to deny it. Re-checking here closes that, and costs nothing when the two
	// timestamps agree.
	if w.gracePeriod > 0 && candidate.CandidateAt.After(w.clock().Add(-w.gracePeriod)) {
		log.Printf("[GC Worker] Block %s: the GC candidate is younger than the grace period; postponing so the incarnation it names serves its own grace", item.ItemID)
		return blockClaimNotYetStaleError{ItemID: item.ItemID}
	}

	// Topology gate: from here the walk can reach a physical delete. The
	// EACH_QUORUM verify below only closes X2 if the keyspace actually gives
	// EACH_QUORUM a per-datacenter meaning; under an unsupported replication class
	// the argument is vacuous, so refuse rather than delete under a proof that does
	// not apply.
	if err := w.checkDestructiveTopology(destructivePathBlock); err != nil {
		log.Printf("[GC Worker] Block %s: destructive topology gate rejected the delete; failing closed: %v", item.ItemID, err)
		return failedClosedError{Reason: "destructive topology gate rejected block", ItemID: item.ItemID, Err: err}
	}

	exists, err := w.store.BlockExists(item.OrgID, item.ItemID)
	if err != nil {
		return w.failClosedIfUnavailable("failed to check canonical block row", item.ItemID, err)
	}
	if !exists {
		// The canonical row is already gone. The forward SHA-1 mapping belongs to
		// the logical block, not this physical GC candidate, so leave it alone.
		if err := w.settleBlockCandidate(item, candidate, candidateFound); err != nil {
			return err
		}
		log.Printf("[GC Worker] Block %s missing canonical row, skipping deletion", item.ItemID)
		metrics.GCItemsSkippedTotal.Inc()
		return nil
	}

	// 1. Claim the block (gc_state='deleting') via LWT, bound to the exact incarnation
	// this candidate was created for and to this attempt's own ownership token.
	claim, err := w.store.ClaimBlockDelete(item.OrgID, item.ItemID, attempt)
	outcome := claim.Outcome
	if err != nil {
		metrics.GCBlockDeleteClaimTotal.WithLabelValues(outcome.String()).Inc()
		// AN UNSETTLED CLAIM POSTPONES, WHATEVER KIND OF ERROR PRODUCED IT. This branch
		// used to hand everything to failClosedIfUnavailable, which spends a retry on
		// anything it does not recognise as an outage — so the MOST uncertain state in the
		// walk (the LWT may have committed, and the serial settling read could not tell
		// us) was the one that could reach the DLQ with a fence possibly standing. The
		// same ambiguity arriving through a non-applied CAS already postpones; these are
		// the same state and must get the same answer (R20).
		if outcome == BlockClaimAmbiguous {
			// Loud and alertable, but never the DLQ. The DLQ is where an item goes to be
			// seen by a human and never processed again — and ItemBlock does not come back
			// from it — so parking an item there while its fence may be standing makes the
			// fence permanent. A metric and a log get the same human attention without
			// throwing away the only thing that can still lift it, and the moment the
			// underlying cause is fixed the next pass takes the stale claim over.
			metrics.GCErrorsTotal.WithLabelValues("block_claim_unsettled").Inc()
			w.recordDestructiveBlocked(destructivePathBlock)
			log.Printf("[GC Worker] Block %s: the delete claim could not be settled even in the serial domain; retaining claim and candidate and postponing: %v", item.ItemID, err)
			return blockClaimReleaseUnconfirmedError{ItemID: item.ItemID, Err: err}
		}
		// An LWT is more exposed than a plain read — Paxos needs its serial quorum on
		// top of the ordinary one, and contention or a degraded cluster shows up here
		// first. Whether a given outage defeats it depends on the serial consistency
		// and the replica count, not on any simple "a DC is down" rule (see
		// isClusterUnavailableError). Either way an availability failure here says
		// nothing about the item, so it must not spend the item's retry budget.
		return w.failClosedIfUnavailable("failed to claim block record for deletion", item.ItemID, err)
	}
	metrics.GCBlockDeleteClaimTotal.WithLabelValues(outcome.String()).Inc()

	// A NON-APPLIED CLAIM IS NOT COMPLETION (R16). Each of these outcomes demands a
	// different response, and collapsing them into "row gone, drop the candidate" is the
	// defect this classifier exists to remove: it consumed the work item whenever
	// ANOTHER live attempt happened to own the row.
	switch outcome {
	case BlockClaimAcquired:
		// Fall through to claim-then-verify below.
	case BlockClaimTargetChanged:
		// The row is a different physical incarnation than this candidate authorized.
		// This candidate's life is over and its work is irrelevant; the incarnation
		// installed now was never decided to be garbage by anything, so it must not be
		// touched. Settle the stale candidate — by its OWN exact identity, so this
		// cannot consume a candidate that already belongs to the new incarnation.
		if err := w.settleBlockCandidate(item, candidate, candidateFound); err != nil {
			return err
		}
		log.Printf("[GC Worker] Block %s: candidate authorized %s but the canonical row now holds a different incarnation; settling the stale candidate without touching it", item.ItemID, candidate.Target)
		metrics.GCItemsSkippedTotal.Inc()
		return nil
	case BlockClaimFreshOwner:
		// Another attempt owns the exact incarnation and is too young to presume dead.
		// Do NOT settle: if that owner turns out to be dead, this candidate is what will
		// eventually take the claim over, and consuming it now would leave the fence
		// standing with nothing left able to lift it. Postpone without burning a retry.
		log.Printf("[GC Worker] Block %s: the exact incarnation is claimed by a live attempt; postponing and preserving the candidate", item.ItemID)
		return blockClaimNotYetStaleError{ItemID: item.ItemID}
	case BlockClaimStaleOwner:
		// The owner is old enough that no live walk can still be running under it. Take
		// over — but only through a CAS against that exact previous authority, never an
		// unconditional clear, and re-classify rather than assume the takeover holds.
		return w.takeOverStaleBlockClaim(item, claim.Owner)
	case BlockClaimMissing:
		if err := w.settleBlockCandidate(item, candidate, candidateFound); err != nil {
			return err
		}
		log.Printf("[GC Worker] Block %s claim found no canonical row, skipping S3 deletion", item.ItemID)
		metrics.GCItemsSkippedTotal.Inc()
		return nil
	case BlockClaimInvalid:
		log.Printf("[GC Worker] Block %s: canonical row carries no usable physical identity; refusing every destructive step and postponing — this will NOT self-heal and needs a human", item.ItemID)
		return blockCandidateAuthorityInvalidError{ItemID: item.ItemID}
	case BlockClaimAmbiguous:
		// The claim's outcome could not be established even in the serial domain.
		// Retain the claim, retain the candidate, finalize nothing, release nothing (R20).
		w.recordDestructiveBlocked(destructivePathBlock)
		log.Printf("[GC Worker] Block %s: the delete claim's outcome is unsettled; retaining claim and candidate and postponing", item.ItemID)
		return blockClaimReleaseUnconfirmedError{ItemID: item.ItemID, Err: errBlockClaimUnsettled}
	default:
		return fmt.Errorf("block %s: unhandled claim outcome %s", item.ItemID, outcome)
	}

	// 2. Claim-then-verify: re-check references AFTER claiming. If a concurrent
	// upload registered a reference, abandon the claim so the block stays alive.
	//
	// THIS IS THE READ THAT AUTHORIZES DESTRUCTION, and it is the only one allowed
	// to. It must be the EACH_QUORUM form: a reference acknowledged at LOCAL_QUORUM
	// in any DC intersects this read's quorum in that same DC, so a zero here means
	// zero fleet-wide rather than zero locally
	// (ISSUE-GC-CROSS-DC-REFERENCE-VISIBILITY-01). Everything this attempt goes on to
	// do — the orphan row and the S3 delete — takes its authority from this single
	// call, so downgrading it to the local form silently reopens X2 for the whole
	// path. (RecoverS3Orphans could inherit that authority through the orphan row,
	// but deliberately does not: it re-establishes the global zero itself.)
	//
	// An unreachable DC makes this read fail rather than return zero, and the error
	// aborts the delete: fail closed, never delete on an uncertain read. The claim is
	// handed back on the way out (see below) so failing closed does not also fence
	// the block.
	hasRefs, err = w.store.BlockHasReferencesGlobal(item.OrgID, item.ItemID)
	if err != nil {
		// Hand the claim back before giving up. Holding it would leave
		// gc_state='deleting' behind an error that an unavailable DC makes
		// systematic rather than rare, and every writer of this content would see
		// BlockDeleteFenceActive until some later attempt happened to clear it.
		// Failing closed must not mean fencing the block indefinitely.
		// Unconditional on purpose — see "WHY THE POST-CLAIM RELEASES ARE
		// UNCONDITIONAL" above; this is the site whose systematic failure mode
		// decides that trade.
		//
		// A failed release RETURNS here rather than warning and falling through to the
		// classifier below. It used to only log, which meant the queue policy was
		// decided from the verify's error while the fence was still up: a ReadFailure
		// verify plus a failing release spent five retries and reached the DLQ with
		// gc_state='deleting' left standing. See releaseBlockClaim — the release error
		// dominates until the fence is confirmed gone, and the verify's own error is
		// reached again on a later pass.
		if relErr := w.releaseBlockClaim(item.OrgID, item.ItemID, attempt); relErr != nil {
			log.Printf("[GC Worker] Block %s: global liveness verify failed (%v) AND the claim could not be handed back; postponing on the release, the verify error will be re-reached once the fence is off", item.ItemID, err)
			return relErr
		}

		// The delete is abandoned either way — an error here never authorizes
		// destruction. What is decided below is only the QUEUE policy, and the same
		// classifier rule applies here as at every other statement in the walk:
		// postpone what the environment caused, spend retries on what it did not.
		//
		// This branch is easy to get wrong in the safe-looking direction. Treating
		// every failure here as environmental keeps the fail-closed guarantee intact,
		// which is why it survived a while — but it makes an item-specific, permanent
		// error postpone for eternity: no retry spent, no DLQ entry, nobody told. A
		// ReadFailure from a tombstone-heavy block_references partition is exactly
		// that shape, and it would be invisible, because the blocked/liveness pair is
		// per PATH: any other block's successful verify moves the recovery half
		// forward and clears the alert while this item stays stuck.
		if !isClusterUnavailableError(err) {
			metrics.GCErrorsTotal.WithLabelValues("liveness_verify_failed").Inc()
			log.Printf("[GC Worker] Block %s: global liveness verify failed for a non-availability reason; not deleting, and spending a retry so it can reach the DLQ: %v", item.ItemID, err)
			return fmt.Errorf("failed to re-check block references for %s: %w", item.ItemID, err)
		}

		metrics.GCErrorsTotal.WithLabelValues("liveness_verify_unavailable").Inc()
		w.recordDestructiveBlocked(destructivePathBlock)
		log.Printf("[GC Worker] Block %s: global liveness verify failed; failing closed without deleting: %v", item.ItemID, err)
		return failedClosedError{Reason: "failed to re-check block references", ItemID: item.ItemID, Err: err}
	}
	// The read returned, which is this path's only proof that the environment can still
	// authorize a delete. Recorded here, BEFORE looking at what it found: a still
	// referenced block is not a delete, but the read that established it is evidence
	// all the same, and waiting for a completed delete would leave a fleet of live
	// blocks reading as permanently blocked.
	w.recordDestructiveLivenessSuccess(destructivePathBlock)
	if hasRefs {
		blockInfo, infoErr := w.store.GetBlockInfo(item.OrgID, item.ItemID)
		if infoErr != nil {
			return w.failClosedIfUnavailable("failed to load re-referenced block info", item.ItemID, infoErr)
		}
		if blockInfo.CreatedAt == nil {
			if blockInfo.StorageClass != "" {
				return fmt.Errorf("stub block %s has storage class without creation timestamp", item.ItemID)
			}
			// The owned claim fences writers. Remove only the metadata-free stub;
			// logical SHA-1 mappings are independent of this physical row.
			deleted, deleteErr := w.store.DeleteClaimedBlockStub(item.OrgID, item.ItemID, attempt.ClaimID)
			if deleteErr != nil {
				return w.failClosedIfUnavailable("failed to delete re-referenced claimed stub", item.ItemID, deleteErr)
			}
			if !deleted {
				return fmt.Errorf("claimed stub %s changed before conditional delete", item.ItemID)
			}
			if err := w.store.DeleteBlockGCCandidate(item.OrgID, item.ItemID, candidate.Identity()); err != nil {
				return w.failClosedIfUnavailable("failed to clear block GC candidate after re-referenced stub cleanup", item.ItemID, err)
			}
			log.Printf("[GC Worker] Block %s re-referenced after a stub claim; removed the owned stub", item.ItemID)
			metrics.GCItemsSkippedTotal.Inc()
			return nil
		}
		// Unconditional: see "WHY THE POST-CLAIM RELEASES ARE UNCONDITIONAL" above.
		//
		// THE most load-bearing release in the walk: the EACH_QUORUM verify just proved
		// this block is still referenced, so a fence left standing here is standing on
		// live data. Routed through releaseBlockClaim, which postpones on ANY failure —
		// failClosedIfUnavailable would let a non-availability error spend the budget,
		// and the next pass cannot be relied on to settle it through the safe pre-check
		// path, because that pre-check is the LOCAL read and may keep answering false
		// while the reference lives in another datacenter.
		if relErr := w.releaseBlockClaim(item.OrgID, item.ItemID, attempt); relErr != nil {
			return relErr
		}
		if err := w.store.DeleteBlockGCCandidate(item.OrgID, item.ItemID, candidate.Identity()); err != nil {
			return w.failClosedIfUnavailable("failed to clear block GC candidate after re-reference", item.ItemID, err)
		}
		log.Printf("[GC Worker] Block %s re-referenced after claim, skipping deletion", item.ItemID)
		metrics.GCItemsSkippedTotal.Inc()
		return nil
	}

	// 3. Persist the S3-pending record BEFORE removing the DB row. This closes the
	// crash window where the process dies after deleting the canonical row but
	// before recording recovery metadata for the later S3 delete.
	blockInfo, err := w.store.GetBlockInfo(item.OrgID, item.ItemID)
	if err != nil {
		return w.failClosedIfUnavailable("failed to load canonical block info", item.ItemID, err)
	}
	// THE DESTRUCTIVE LOCATOR IS THE ONE THE CLAIM AUTHORIZED, NOT THE ONE READ BACK.
	//
	// GetBlockInfo is an ordinary read — `database.consistency` accepts ONE — while the
	// claim commits at EACH_QUORUM in the serial domain, so this read can legitimately
	// land on a replica that never saw what the claim serialized. Taking the orphan and
	// the S3 delete from it would mean publishing and destroying whatever incarnation
	// happens to be visible here, which is the same "re-read blocks and destroy what is
	// there now" that the candidate authority at the top of this walk exists to forbid.
	// FinalizeBlockDelete was already bound to `attempt`; the two steps that actually
	// touch bytes were not.
	//
	// So blockInfo is used only for what the claim cannot carry — the stub discriminator
	// and sha1 — and any disagreement about the incarnation aborts before anything is
	// published or deleted.
	storageClass := attempt.Target.StorageClass
	storageKey := attempt.Target.StorageKey
	if observed := (BlockDeleteTarget{StorageClass: blockInfo.StorageClass, StorageKey: blockInfo.StorageKey}); !observed.IsZero() && observed != attempt.Target {
		metrics.GCErrorsTotal.WithLabelValues("block_incarnation_divergence").Inc()
		if relErr := w.releaseBlockClaim(item.OrgID, item.ItemID, attempt); relErr != nil {
			return relErr
		}
		return fmt.Errorf("block %s: the canonical row reads back as %s but the claim authorized %s; refusing to publish or delete either", item.ItemID, observed, attempt.Target)
	}
	if blockInfo.StorageClass == "" {
		if blockInfo.CreatedAt == nil {
			deleted, deleteErr := w.store.DeleteClaimedBlockStub(item.OrgID, item.ItemID, attempt.ClaimID)
			if deleteErr != nil {
				return w.failClosedIfUnavailable("failed to remove stub block row", item.ItemID, deleteErr)
			}
			if !deleted {
				return fmt.Errorf("claimed stub %s changed before conditional delete", item.ItemID)
			}
			if err := w.store.DeleteBlockGCCandidate(item.OrgID, item.ItemID, candidate.Identity()); err != nil {
				return w.failClosedIfUnavailable("failed to clear block GC candidate after stub cleanup", item.ItemID, err)
			}
			log.Printf("[GC Worker] Block %s missing canonical metadata after claim; removed stub row and skipped deletion", item.ItemID)
			metrics.GCItemsSkippedTotal.Inc()
			return nil
		}
		// Unconditional: see "WHY THE POST-CLAIM RELEASES ARE UNCONDITIONAL" above.
		//
		// The malformed-row error below is deliberately DLQ-bound — a canonical row
		// with a creation timestamp and no storage class needs a human. But it must not
		// travel while the fence is unconfirmed, or the human inherits a fenced block
		// as well as a malformed one. releaseBlockClaim's error dominates; the
		// malformed-row error is re-reached on the pass that confirms the release.
		if relErr := w.releaseBlockClaim(item.OrgID, item.ItemID, attempt); relErr != nil {
			return relErr
		}
		return fmt.Errorf("block %s has empty canonical storage class", item.ItemID)
	}
	if !config.IsCanonicalStorageClassName(storageClass) {
		if relErr := w.releaseBlockClaim(item.OrgID, item.ItemID, attempt); relErr != nil {
			return relErr
		}
		return fmt.Errorf("block %s has non-canonical storage class %q", item.ItemID, storageClass)
	}
	if storageKey == "" || strings.TrimSpace(storageKey) != storageKey {
		if relErr := w.releaseBlockClaim(item.OrgID, item.ItemID, attempt); relErr != nil {
			return relErr
		}
		return fmt.Errorf("block %s has empty canonical storage key", item.ItemID)
	}
	// Resolve the destination store HERE, in the authorization phase, rather than
	// after the row is gone. Two reasons, and the second is the one that matters:
	//
	//   - the persisted locator can only be validated by the org-scoped store that
	//     owns its physical naming rules. A mismatch must abort BEFORE
	//     StartBlockDeleteOrphan and FinalizeBlockDelete, or a suspicious row is
	//     already half-destroyed by the time anyone refuses to touch its bytes.
	//   - a store that will not resolve now hands the claim back instead of stranding
	//     a deleted row whose object nothing is left to remove.
	var blockStore BlockStoreDeleter
	if w.storage != nil {
		resolved, resolveErr := w.storage.GetBlockStoreForOrg(item.OrgID.String(), storageClass)
		if resolveErr != nil {
			if relErr := w.releaseBlockClaim(item.OrgID, item.ItemID, attempt); relErr != nil {
				return relErr
			}
			return fmt.Errorf("failed to get block store for org %s class %s: %w", item.OrgID, storageClass, resolveErr)
		}
		if validateErr := resolved.ValidatePhysicalLocator(item.ItemID, storageKey); validateErr != nil {
			metrics.GCErrorsTotal.WithLabelValues("block_storage_key_mismatch").Inc()
			if relErr := w.releaseBlockClaim(item.OrgID, item.ItemID, attempt); relErr != nil {
				return relErr
			}
			return fmt.Errorf("block %s persisted physical locator %q failed validation: %w", item.ItemID, storageKey, validateErr)
		}
		blockStore = resolved
	}
	if item.StorageClass != "" && item.StorageClass != storageClass {
		log.Printf("[GC Worker] WARNING: block %s queued with storage_class=%s but canonical storage_class=%s; using canonical value", item.ItemID, item.StorageClass, storageClass)
	}

	// Re-check the gate on the way out of the authorization phase and into the
	// destructive one, ignoring the cache. The first check ran several statements
	// ago, and a keyspace can be ALTERed at any moment, so passing it once does not
	// mean the EACH_QUORUM argument still holds now — the gate detects drift, it does
	// not prevent a concurrent ALTER. This does not close that window (nothing here
	// can; topology changes under enabled destructive GC are outside the supported
	// procedure), it narrows it from "the whole walk" to "these two statements".
	//
	// It must be the FRESH form. A walk takes milliseconds, so a cached check here
	// would return the pass this same walk stored moments ago and assert nothing at
	// all — defence in depth in appearance only. The read is paid once per block that
	// is actually about to be destroyed, not once per candidate.
	if err := w.checkDestructiveTopologyFresh(destructivePathBlock); err != nil {
		log.Printf("[GC Worker] Block %s: destructive topology gate rejected the delete at the commit point; failing closed: %v", item.ItemID, err)
		// Hand the claim back: the block is provably unreferenced, so the fence buys
		// nothing here, and holding it under a systematic rejection would fence this
		// content for as long as the topology stays wrong.
		//
		// Both outcomes postpone, so unlike the sites above this one was never able to
		// strand the item — it is routed through releaseBlockClaim for the accounting
		// and the log, not to change the queue policy.
		if relErr := w.releaseBlockClaim(item.OrgID, item.ItemID, attempt); relErr != nil {
			return relErr
		}
		return failedClosedError{Reason: "destructive topology gate rejected block at the commit point", ItemID: item.ItemID, Err: err}
	}

	orphanFirstSeenAt, err := w.store.StartBlockDeleteOrphan(item.OrgID, item.ItemID, storageClass, storageKey, blockInfo.Sha1, w.clock().UTC())
	if err != nil {
		return w.failClosedIfUnavailable("failed to record pending S3 delete", item.ItemID, err)
	}

	// 4. Now remove the claimed DB row. If this fails, the row stays claimed and
	// the queue item will retry; the pending S3 row already preserves recovery state.
	if err := w.store.FinalizeBlockDelete(item.OrgID, item.ItemID, attempt); err != nil {
		return w.failClosedIfUnavailable("failed to finalize claimed block delete", item.ItemID, err)
	}

	// With no storage provider (degenerate/no-storage-manager config) there is no S3
	// step and RecoverS3Orphans is a no-op, so the recovery row has nothing left to
	// drive: clear it instead of leaving it to TTL. With
	// storage, the row is only cleared once the S3 delete has succeeded (or it stays
	// for RecoverS3Orphans to retry).
	clearRecoveryRow := blockStore == nil
	if blockStore != nil {
		if delErr := w.deleteS3WithRetry(ctx, blockStore, storageKey); delErr != nil {
			log.Printf("[GC Worker] WARNING: Failed to delete block %s from S3 after DB deletion: %v (recording for scanner recovery)", item.ItemID, delErr)
			if recErr := w.store.UpdateS3OrphanAttempt(item.OrgID, item.ItemID, orphanFirstSeenAt, delErr.Error(), w.clock()); recErr != nil {
				log.Printf("[GC Worker] ERROR: Failed to update S3 orphan %s: %v", item.ItemID, recErr)
				metrics.GCErrorsTotal.WithLabelValues("s3_orphan_record").Inc()
			}
			metrics.GCAuditEventsTotal.WithLabelValues("gc_block_s3_orphaned").Inc()
			// Do NOT return error — the block is recorded for recovery.
			// Continue to post-delete cleanup so the queue item completes.
		} else if err := w.store.MarkS3OrphanMappingCleanupPending(item.OrgID, item.ItemID, blockInfo.Sha1, w.clock()); err != nil {
			log.Printf("[GC Worker] WARNING: S3 delete for block %s succeeded but failed to advance recovery row: %v", item.ItemID, err)
			clearRecoveryRow = true
		} else {
			clearRecoveryRow = true
		}
	}

	// 5. Finalize the recovery row after the physical delete. The forward mapping
	// is logical metadata and intentionally survives this physical GC lifecycle.
	if clearRecoveryRow {
		if err := w.store.DeleteS3Orphan(item.OrgID, item.ItemID, orphanFirstSeenAt); err != nil {
			log.Printf("[GC Worker] WARNING: block %s physical cleanup succeeded but failed to clear recovery row: %v", item.ItemID, err)
		}
	}

	if err := w.store.DeleteBlockGCCandidate(item.OrgID, item.ItemID, candidate.Identity()); err != nil {
		return w.failClosedIfUnavailable("failed to clear block GC candidate", item.ItemID, err)
	}

	w.stats.IncrBlocksDeleted()
	metrics.GCAuditEventsTotal.WithLabelValues("gc_block_deleted").Inc()
	log.Printf("[GC Worker] Deleted block %s", item.ItemID)
	return nil
}

// settleBlockCandidate consumes the candidate this walk was authorized by, and only
// that one.
//
// The identity it passes down is the candidate's OWN (storage_class, storage_key,
// candidate_at), read from the candidate row at the top of the walk — never a timestamp
// carried on the queue item. A queue item can outlive the candidate it was built from,
// so using its timestamp would let a late lifecycle for P1 erase a candidate that by
// then belongs to P2, destroying the only work item authorized to reclaim P2.
func (w *Worker) settleBlockCandidate(item QueueItem, candidate BlockGCCandidateInfo, candidateFound bool) error {
	if !candidateFound {
		return nil
	}
	if err := w.store.DeleteBlockGCCandidate(item.OrgID, item.ItemID, candidate.Identity()); err != nil {
		return w.failClosedIfUnavailable("failed to clear block GC candidate", item.ItemID, err)
	}
	return nil
}

// takeOverStaleBlockClaim hands back an abandoned claim and re-attempts the walk under
// this attempt's own identity.
//
// Two rules make this safe. The release is a CAS against the EXACT previous authority —
// incarnation, claim id and claimed_at — so an owner that woke up and re-claimed between
// the observation and the write keeps its row. And a failed takeover NEVER settles the
// candidate: the whole point of the fresh/stale split is that a claim which cannot be
// lifted yet still has to be lifted eventually, and this candidate is what will do it.
func (w *Worker) takeOverStaleBlockClaim(item QueueItem, observed BlockDeleteAuthority) error {
	// THE CAS NAMES THE AUTHORITY THE CLAIM ACTUALLY OBSERVED, and that is the whole
	// point of this function.
	//
	// The tempting shape — re-read the row, see whether whatever is there now is old,
	// release that — is a different operation wearing the same name. Between the claim's
	// observation and this call the row can have become a different incarnation with its
	// own stale owner, and releasing THAT is a worker authorized for `P1` dropping `P2`'s
	// fence: the exact authority violation R14a exists to prevent, with nothing but the
	// staleness window standing between it and a delete under a lifted fence.
	//
	// So this is ReleaseBlockClaim, not ReleaseStaleBlockClaim. Staleness was already
	// decided when the claim classified this owner; what remains is one question the CAS
	// answers on its own: is that exact owner still there?
	outcome, err := w.store.ReleaseBlockClaim(item.OrgID, item.ItemID, observed)
	if err != nil {
		metrics.GCBlockDeleteTakeoverTotal.WithLabelValues("failed").Inc()
		if isClusterUnavailableError(err) {
			metrics.GCErrorsTotal.WithLabelValues("cluster_unavailable").Inc()
			w.recordDestructiveBlocked(destructivePathBlock)
		} else {
			metrics.GCErrorsTotal.WithLabelValues("stale_claim_release_failed").Inc()
		}
		log.Printf("[GC Worker] Block %s: taking over a stale delete claim failed; postponing with the fence still up: %v", item.ItemID, err)
		return blockClaimReleaseUnconfirmedError{ItemID: item.ItemID, Err: err}
	}
	if outcome != BlockReleaseReleased {
		// Ownership changed between the claim's classification and this release: the old
		// owner came back, a third attempt took it, or the row became a different
		// incarnation. Re-classify on a later pass; never conclude the candidate is
		// finished from a failed takeover.
		metrics.GCBlockDeleteTakeoverTotal.WithLabelValues("lost").Inc()
		log.Printf("[GC Worker] Block %s: the stale delete claim %s was no longer the owner at takeover time; postponing and preserving the candidate", item.ItemID, observed.ClaimID)
		return blockClaimNotYetStaleError{ItemID: item.ItemID}
	}
	metrics.GCBlockDeleteTakeoverTotal.WithLabelValues("released").Inc()
	metrics.GCAuditEventsTotal.WithLabelValues("gc_block_stale_claim_released").Inc()
	log.Printf("[GC Worker] Block %s: took over a stale delete claim left by an abandoned attempt", item.ItemID)

	// The fence is off and this attempt still holds no claim. Postpone rather than
	// re-entering the walk inline: a fresh pass re-establishes every precondition —
	// references, canonical row, topology — under a new attempt identity, instead of
	// reusing observations made before another worker's claim was standing.
	return blockClaimNotYetStaleError{ItemID: item.ItemID}
}

// deleteS3WithRetry attempts to delete a block from S3 with exponential backoff.
// It is cancellable via the context. Returns nil on success; the last error
// otherwise. Retries are NOT applied to context cancellation.
func (w *Worker) deleteS3WithRetry(ctx context.Context, blockStore BlockStoreDeleter, storageKey string) error {
	var lastErr error
	attempts := len(s3DeleteRetryDelays) + 1 // 1 initial try + N retries
	for i := 0; i < attempts; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := blockStore.DeleteBlockByStorageKey(ctx, storageKey); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if i >= len(s3DeleteRetryDelays) {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(s3DeleteRetryDelays[i]):
		}
	}
	return lastErr
}

// RecoverS3Orphans retries S3 deletes for orphan rows in gc_s3_orphans.
// Called by the scanner; exposed on the worker because it needs access to
// w.storage. Returns the number of orphans successfully recovered.
//
// Walks the gc_s3_orphans_by_day discovery projection from a persisted UTC-day
// cursor up to today. On cold start (no cursor) it scans the full 90-day TTL
// horizon so old orphan rows cannot get stranded forever. `perBucketLimit`
// caps the rows pulled per (day, bucket) so a single misbehaving bucket cannot
// starve the worker.
// RecoverS3Orphans finishes physical deletes that processBlock started but could not
// complete (S3 error, crash, restart).
//
// AUTHORIZATION INVARIANT: every physical delete in this codebase must trace back to
// an EACH_QUORUM liveness read (ISSUE-GC-CROSS-DC-REFERENCE-VISIBILITY-01). A new
// destructive path that does not is a silent reopening with no failing test.
//
// This path is authorized twice over. Transitively, a gc_s3_orphans row cannot exist
// unless processBlock already passed its claim-then-verify, because
// StartBlockDeleteOrphan runs strictly after it. But that implication only holds
// forward in time: a row written by a pre-X2 binary was authorized by a LOCAL_QUORUM
// verify, and recovery would happily finish that delete after an upgrade. Rather than
// leave the guarantee resting on the greenfield deployment precondition — true today,
// unenforceable in code, and invisible when it stops being true — recovery re-reads
// BlockHasReferencesGlobal for itself before destroying bytes. It is the cold path;
// the extra WAN read costs nothing that matters.
func (w *Worker) RecoverS3Orphans(ctx context.Context, perBucketLimit int) (int, error) {
	if w.storage == nil {
		return 0, nil
	}
	if w.dryRun.Load() {
		log.Println("[GC Worker] DRY RUN: skipping S3 orphan recovery")
		return 0, nil
	}
	// Same gate as processBlock: this path deletes bytes too. Authorization comes from
	// the BlockHasReferencesGlobal below, not from the orphan row; the gate is what
	// makes that read mean anything. Checked once here — cached form — to refuse the
	// whole sweep cheaply, and again immediately before each delete in the FRESH form,
	// which deliberately re-reads the live replication map. A sweep can run long, so a
	// cached pass taken at its start would say nothing about the topology in effect by
	// the time a given orphan is destroyed. That costs one metadata read per orphan
	// actually deleted; this is the cold path, and the read is cheap next to destroying
	// bytes irreversibly.
	if err := w.checkDestructiveTopology(destructivePathOrphan); err != nil {
		log.Printf("[GC Worker] S3 orphan recovery: destructive topology gate rejected the sweep; failing closed: %v", err)
		return 0, fmt.Errorf("destructive topology gate rejected S3 orphan recovery: %w", err)
	}
	if perBucketLimit <= 0 {
		perBucketLimit = 100
	}

	cutoffDay := db.GCProjectionUTCDate(w.clock())
	startDay, err := w.loadS3OrphansStartDay(cutoffDay)
	if err != nil {
		return 0, err
	}
	if startDay.After(cutoffDay) {
		return 0, nil
	}

	recovered := 0
	var phaseErr error
	for day := startDay; !day.After(cutoffDay); day = day.AddDate(0, 0, 1) {
		for bucket := 0; bucket < db.GCDiscoveryBucketCount; bucket++ {
			select {
			case <-ctx.Done():
				return recovered, ctx.Err()
			default:
			}

			orphans, err := w.store.ListS3OrphansByDay(day, bucket, perBucketLimit+1)
			if err != nil {
				log.Printf("[GC Worker] S3 orphan recovery: list failed for day=%s bucket=%d: %v", db.GCProjectionDateString(day), bucket, err)
				if phaseErr == nil {
					phaseErr = fmt.Errorf("list S3 orphan recovery partition day=%s bucket=%d: %w", db.GCProjectionDateString(day), bucket, err)
				}
				continue
			}
			if len(orphans) > perBucketLimit {
				log.Printf("[GC Worker] S3 orphan recovery: partition day=%s bucket=%d hit limit=%d; deferring cursor advance", db.GCProjectionDateString(day), bucket, perBucketLimit)
				if phaseErr == nil {
					phaseErr = fmt.Errorf("S3 orphan recovery partition day=%s bucket=%d incomplete after reaching limit=%d", db.GCProjectionDateString(day), bucket, perBucketLimit)
				}
				orphans = orphans[:perBucketLimit]
			}
			for _, discovery := range orphans {
				select {
				case <-ctx.Done():
					return recovered, ctx.Err()
				default:
				}

				canonical, found, err := w.store.GetS3OrphanGlobal(discovery.OrgID, discovery.BlockID)
				if err != nil {
					if isClusterUnavailableError(err) {
						metrics.GCErrorsTotal.WithLabelValues("s3_orphan_canonical_read_unavailable").Inc()
						w.recordDestructiveBlocked(destructivePathOrphan)
					} else {
						metrics.GCErrorsTotal.WithLabelValues("s3_orphan_canonical_read_failed").Inc()
					}
					log.Printf("[GC Worker] S3 orphan recovery: canonical read failed for org=%s block=%s: %v", discovery.OrgID, discovery.BlockID, err)
					if phaseErr == nil {
						phaseErr = fmt.Errorf("read canonical S3 orphan org=%s block=%s: %w", discovery.OrgID, discovery.BlockID, err)
					}
					continue
				}
				if !found {
					metrics.GCErrorsTotal.WithLabelValues("s3_orphan_canonical_missing").Inc()
					log.Printf("[GC Worker] S3 orphan recovery: discovery row has no canonical orphan for org=%s block=%s; retaining cursor", discovery.OrgID, discovery.BlockID)
					if phaseErr == nil {
						phaseErr = fmt.Errorf("canonical S3 orphan missing for org=%s block=%s", discovery.OrgID, discovery.BlockID)
					}
					continue
				}
				if !s3OrphanDiscoveryMatchesCanonical(discovery, canonical) {
					metrics.GCErrorsTotal.WithLabelValues("s3_orphan_discovery_token_mismatch").Inc()
					log.Printf("[GC Worker] S3 orphan recovery: discovery token does not match canonical orphan for org=%s block=%s; retaining cursor", discovery.OrgID, discovery.BlockID)
					if phaseErr == nil {
						phaseErr = fmt.Errorf("discovery token mismatch for canonical S3 orphan org=%s block=%s", discovery.OrgID, discovery.BlockID)
					}
					continue
				}
				if strings.TrimSpace(canonical.StorageKey) == "" {
					metrics.GCErrorsTotal.WithLabelValues("s3_orphan_empty_storage_key").Inc()
					log.Printf("[GC Worker] S3 orphan recovery: canonical row has empty storage key for org=%s block=%s; retaining cursor", canonical.OrgID, canonical.BlockID)
					if phaseErr == nil {
						phaseErr = fmt.Errorf("canonical S3 orphan has empty storage key for org=%s block=%s", canonical.OrgID, canonical.BlockID)
					}
					continue
				}

				// This is a defense-in-depth reload, not a lifecycle lock. It narrows
				// the stale-read window immediately before an irreversible action;
				// R23/R26 must still bind the operation to an immutable P.
				reloadCanonical := func(previous S3OrphanInfo) (S3OrphanInfo, error) {
					next, exists, reloadErr := w.store.GetS3OrphanGlobal(discovery.OrgID, discovery.BlockID)
					if reloadErr != nil {
						return S3OrphanInfo{}, fmt.Errorf("reload canonical S3 orphan org=%s block=%s: %w", discovery.OrgID, discovery.BlockID, reloadErr)
					}
					if !exists {
						return S3OrphanInfo{}, fmt.Errorf("canonical S3 orphan disappeared for org=%s block=%s: %w", discovery.OrgID, discovery.BlockID, errS3OrphanCanonicalMissing)
					}
					if !s3OrphanRecoveryStateEqual(previous, next) {
						return S3OrphanInfo{}, fmt.Errorf("canonical S3 orphan state changed for org=%s block=%s: %w", discovery.OrgID, discovery.BlockID, errS3OrphanCanonicalChanged)
					}
					return next, nil
				}

				phase := strings.TrimSpace(canonical.RecoveryPhase)
				if phase == S3OrphanPhasePendingMappingCleanup {
					// Historical phase name. S3 has already succeeded; only finalize the
					// orphan lifecycle. The old BlockExists guard and mapping delete were
					// coupled to the physical lifecycle and are intentionally gone.
					canonicalCommit, reloadErr := reloadCanonical(canonical)
					if reloadErr != nil {
						w.recordS3OrphanCanonicalReloadFailure(reloadErr)
						log.Printf("[GC Worker] S3 orphan recovery: refusing post-S3 orphan finalization for %s after canonical reload: %v", canonical.BlockID, reloadErr)
						if phaseErr == nil {
							phaseErr = reloadErr
						}
						continue
					}
					if err := w.store.DeleteS3Orphan(canonicalCommit.OrgID, canonicalCommit.BlockID, canonicalCommit.FirstSeenAt); err != nil {
						log.Printf("[GC Worker] S3 orphan recovery: failed to finalize post-S3 orphan row %s: %v", canonicalCommit.BlockID, err)
						if phaseErr == nil {
							phaseErr = fmt.Errorf("finalize post-S3 orphan row for block %s: %w", canonicalCommit.BlockID, err)
						}
						continue
					}
					recovered++
					metrics.GCAuditEventsTotal.WithLabelValues("gc_s3_orphan_recovered").Inc()
					log.Printf("[GC Worker] Finalized post-S3 orphan for block %s (org=%s, retries=%d)", canonicalCommit.BlockID, canonicalCommit.OrgID, canonicalCommit.RetryCount)
					continue
				}
				if phase != S3OrphanPhasePendingS3 {
					metrics.GCErrorsTotal.WithLabelValues("s3_orphan_invalid_recovery_phase").Inc()
					log.Printf("[GC Worker] S3 orphan recovery: canonical row has unsupported recovery phase %q for org=%s block=%s; retaining cursor", phase, canonical.OrgID, canonical.BlockID)
					if phaseErr == nil {
						phaseErr = fmt.Errorf("unsupported canonical S3 orphan recovery phase %q for org=%s block=%s", phase, canonical.OrgID, canonical.BlockID)
					}
					continue
				}
				if exists, err := w.store.BlockExists(canonical.OrgID, canonical.BlockID); err != nil {
					log.Printf("[GC Worker] S3 orphan recovery: block existence lookup failed for org=%s block=%s: %v", canonical.OrgID, canonical.BlockID, err)
					if phaseErr == nil {
						phaseErr = fmt.Errorf("check block existence for S3 orphan org=%s block=%s: %w", canonical.OrgID, canonical.BlockID, err)
					}
					continue
				} else if exists {
					// The canonical block row still exists (likely claimed but not yet finalized).
					// Skip recovery for now; a later worker retry or startup scan will finish it.
					if phaseErr == nil {
						phaseErr = fmt.Errorf("S3 orphan recovery deferred for org=%s block=%s because canonical block row still exists", canonical.OrgID, canonical.BlockID)
					}
					continue
				}

				// Independent authorization for this delete. See the invariant on this
				// function: the orphan row alone would make recovery inherit whatever
				// consistency the writing binary used, so it establishes the global
				// zero itself. An error here defers the sweep rather than deleting.
				hasRefs, livenessErr := w.store.BlockHasReferencesGlobal(canonical.OrgID, canonical.BlockID)
				if livenessErr != nil {
					// Classified the same way processBlock's verify is, for a different
					// reason. There is no queue policy to decide here — this sweep has
					// no retry budget and no DLQ, and the deferral below is identical
					// either way — so what the split buys is an honest signal. Calling a
					// permanent ReadFailure from a tombstone-heavy block_references
					// partition an availability failure pages whoever is on call to go
					// look at datacenter health for a condition that will still be there
					// when every DC is up.
					//
					// The blocked mark is availability-only for the same reason. It is
					// half of the pair that answers "can this path still authorize
					// deletes at all", and one poisoned partition does not answer it;
					// moving the mark would report an environment failure that did not
					// happen. Left unmoved, the row's own error metric and the frozen
					// scan-success timestamp are what surface it.
					if isClusterUnavailableError(livenessErr) {
						metrics.GCErrorsTotal.WithLabelValues("liveness_verify_unavailable").Inc()
						w.recordDestructiveBlocked(destructivePathOrphan)
						log.Printf("[GC Worker] S3 orphan recovery: global liveness verify failed for org=%s block=%s because the cluster was unavailable; failing closed: %v", canonical.OrgID, canonical.BlockID, livenessErr)
					} else {
						metrics.GCErrorsTotal.WithLabelValues("liveness_verify_failed").Inc()
						log.Printf("[GC Worker] S3 orphan recovery: global liveness verify failed for org=%s block=%s for a non-availability reason (this row will not recover on its own); failing closed: %v", canonical.OrgID, canonical.BlockID, livenessErr)
					}
					// Unchanged by the classification: the sweep defers either way, which
					// holds the day cursor and keeps the row in the working set.
					if phaseErr == nil {
						phaseErr = fmt.Errorf("global liveness verify for S3 orphan org=%s block=%s: %w", canonical.OrgID, canonical.BlockID, livenessErr)
					}
					continue
				}
				// Same rule as processBlock: the read RETURNING is this path's proof that
				// the environment can authorize a delete, whatever the read found. Recorded
				// before the hasRefs branch, never after a completed delete.
				w.recordDestructiveLivenessSuccess(destructivePathOrphan)
				if hasRefs {
					// Something references this block even though its canonical row is
					// gone. Recovery must not destroy the bytes those references point
					// at; leave the row for an operator rather than guessing.
					//
					// Reported through the metric and the log ONLY — deliberately not
					// through phaseErr. A phase error suppresses SetLastScanSuccess, so
					// one anomalous row would freeze the scanner's success timestamp
					// forever and make a healthy fleet indistinguishable from a broken
					// one — losing a signal that matters far more than restating an
					// anomaly the counter already exposes. There is also no way to
					// acknowledge it: unlike the DLQ, gc_s3_orphans has no resolved
					// state. Alert on gc_audit_events_total{event=
					// "gc_s3_orphan_referenced_deferred"}.
					//
					// KNOWN GAP — this row now falls out of the working set, and an
					// earlier version of this comment wrongly claimed the opposite ("the
					// row stays, so every subsequent sweep rediscovers it"). It does not:
					// a sweep that ends without a phase error advances the day cursor, and
					// the next one starts only gcScanOverlapDays back, so once the cursor
					// passes this row's bucket nothing revisits it. If the anomalous
					// reference later goes away the bytes are never collected, and the row
					// itself TTLs out at 90 days, taking the recovery metadata with it.
					// The counter goes quiet at the same moment, so the alert above stops
					// firing while the condition persists.
					//
					// Storage leak, not a delete of live data — recovery refuses, it does
					// not guess. Fixing it needs a lifecycle of its own (a durable
					// deferred/quarantine state, or re-projection into a future bucket)
					// rather than a phaseErr, which is the thing that froze the scanner.
					// Tracked as ISSUE-GC-REFERENCED-ORPHAN-LIFECYCLE-01.
					metrics.GCAuditEventsTotal.WithLabelValues("gc_s3_orphan_referenced_deferred").Inc()
					log.Printf("[GC Worker] S3 orphan recovery: block %s (org=%s) still has references; refusing to delete its bytes (operator action required)", canonical.BlockID, canonical.OrgID)
					continue
				}

				// Re-check the gate immediately before destroying bytes, ignoring the
				// cache: a sweep can run long, and a cached pass from the top of it
				// would assert nothing about the topology in effect right now.
				if err := w.checkDestructiveTopologyFresh(destructivePathOrphan); err != nil {
					log.Printf("[GC Worker] S3 orphan recovery: destructive topology gate rejected block %s mid-sweep; failing closed: %v", canonical.BlockID, err)
					if phaseErr == nil {
						phaseErr = fmt.Errorf("destructive topology gate rejected S3 orphan org=%s block=%s: %w", canonical.OrgID, canonical.BlockID, err)
					}
					continue
				}

				canonicalCommit, reloadErr := reloadCanonical(canonical)
				if reloadErr != nil {
					w.recordS3OrphanCanonicalReloadFailure(reloadErr)
					log.Printf("[GC Worker] S3 orphan recovery: refusing physical delete for %s after canonical reload: %v", canonical.BlockID, reloadErr)
					if phaseErr == nil {
						phaseErr = reloadErr
					}
					continue
				}

				storageClass := canonicalCommit.StorageClass
				if storageClass == "" {
					if phaseErr == nil {
						phaseErr = fmt.Errorf("S3 orphan recovery row has empty storage class for org=%s block=%s", canonicalCommit.OrgID, canonicalCommit.BlockID)
					}
					continue
				}
				if !config.IsCanonicalStorageClassName(storageClass) {
					if phaseErr == nil {
						phaseErr = fmt.Errorf("S3 orphan recovery row has non-canonical storage class %q for org=%s block=%s", storageClass, canonicalCommit.OrgID, canonicalCommit.BlockID)
					}
					continue
				}
				blockStore, err := w.storage.GetBlockStoreForOrg(canonicalCommit.OrgID.String(), storageClass)
				if err != nil {
					log.Printf("[GC Worker] S3 orphan recovery: get block store for org=%s class=%s failed: %v", canonicalCommit.OrgID, storageClass, err)
					if phaseErr == nil {
						phaseErr = fmt.Errorf("get block store for S3 orphan org=%s class=%s: %w", canonicalCommit.OrgID, storageClass, err)
					}
					continue
				}
				// Same refusal as the normal delete path: the reloaded row names the
				// object, but only this org's store can validate that physical locator.
				// Refuse rather than hand an unverified key to S3.
				if validateErr := blockStore.ValidatePhysicalLocator(canonicalCommit.BlockID, canonicalCommit.StorageKey); validateErr != nil {
					metrics.GCErrorsTotal.WithLabelValues("s3_orphan_storage_key_mismatch").Inc()
					log.Printf("[GC Worker] S3 orphan recovery: persisted physical locator %q for org=%s block=%s failed validation: %v; retaining cursor", canonicalCommit.StorageKey, canonicalCommit.OrgID, canonicalCommit.BlockID, validateErr)
					if phaseErr == nil {
						phaseErr = fmt.Errorf("canonical S3 orphan physical locator %q for org=%s block=%s failed validation: %w", canonicalCommit.StorageKey, canonicalCommit.OrgID, canonicalCommit.BlockID, validateErr)
					}
					continue
				}
				if err := blockStore.DeleteBlockByStorageKey(ctx, canonicalCommit.StorageKey); err != nil {
					if updErr := w.store.UpdateS3OrphanAttempt(canonicalCommit.OrgID, canonicalCommit.BlockID, canonicalCommit.FirstSeenAt, err.Error(), w.clock()); updErr != nil {
						log.Printf("[GC Worker] S3 orphan recovery: update attempt for %s failed: %v", canonicalCommit.BlockID, updErr)
						if phaseErr == nil {
							phaseErr = fmt.Errorf("update S3 orphan attempt for block %s: %w", canonicalCommit.BlockID, updErr)
						}
					}
					if phaseErr == nil {
						phaseErr = fmt.Errorf("delete S3 orphan block %s from backing store: %w", canonicalCommit.BlockID, err)
					}
					continue
				}
				if err := w.store.MarkS3OrphanMappingCleanupPending(canonicalCommit.OrgID, canonicalCommit.BlockID, canonicalCommit.ExternalSHA1, w.clock()); err != nil {
					log.Printf("[GC Worker] S3 orphan recovery: failed to advance %s to mapping cleanup: %v", canonicalCommit.BlockID, err)
					if phaseErr == nil {
						phaseErr = fmt.Errorf("advance recovered block %s to mapping cleanup: %w", canonicalCommit.BlockID, err)
					}
					continue
				}
				if err := w.store.DeleteS3Orphan(canonicalCommit.OrgID, canonicalCommit.BlockID, canonicalCommit.FirstSeenAt); err != nil {
					log.Printf("[GC Worker] S3 orphan recovery: failed to clear orphan row %s: %v", canonicalCommit.BlockID, err)
					if phaseErr == nil {
						phaseErr = fmt.Errorf("clear S3 orphan row for block %s: %w", canonicalCommit.BlockID, err)
					}
					continue
				}
				recovered++
				metrics.GCAuditEventsTotal.WithLabelValues("gc_s3_orphan_recovered").Inc()
				log.Printf("[GC Worker] Recovered S3 orphan %s (org=%s, retries=%d)", canonicalCommit.BlockID, canonicalCommit.OrgID, canonicalCommit.RetryCount)
			}
		}
	}

	// No end-of-sweep verdict here either. A sweep that refused nothing is not
	// evidence this path can delete — the usual shape is a sweep with no orphan rows at
	// all, which attempts nothing and proves nothing. The sweep's own liveness reads
	// carry the signal; see the pair on metrics.GCDestructiveLastBlockedTimestamp.

	if phaseErr == nil {
		newCursor := cutoffDay.AddDate(0, 0, -1)
		if !newCursor.Before(startDay) {
			if err := w.store.SaveGCStats(gcS3OrphansCursorKey, db.GCProjectionDateString(newCursor)); err != nil {
				phaseErr = fmt.Errorf("persist S3 orphan recovery cursor: %w", err)
			}
		}
	}

	return recovered, phaseErr
}

func normalizeS3OrphanRecoveryTime(value time.Time) time.Time {
	if value.IsZero() {
		return time.Time{}
	}
	return value.UTC().Truncate(time.Millisecond)
}

func s3OrphanDiscoveryMatchesCanonical(discovery S3OrphanDiscoveryInfo, canonical S3OrphanInfo) bool {
	if discovery.OrgID != canonical.OrgID || discovery.BlockID != canonical.BlockID {
		return false
	}
	discoveryFirstSeenAt := normalizeS3OrphanRecoveryTime(discovery.FirstSeenAt)
	canonicalFirstSeenAt := normalizeS3OrphanRecoveryTime(canonical.FirstSeenAt)
	return !discoveryFirstSeenAt.IsZero() && discoveryFirstSeenAt.Equal(canonicalFirstSeenAt)
}

// s3OrphanRecoveryStateEqual compares only canonical fields that can change
// which destructive or mapping action recovery is allowed to take. Diagnostic
// retry fields are intentionally excluded because a failed attempt may update
// them without changing the lifecycle state being recovered.
func s3OrphanRecoveryStateEqual(left, right S3OrphanInfo) bool {
	return left.OrgID == right.OrgID &&
		left.BlockID == right.BlockID &&
		normalizeS3OrphanRecoveryTime(left.FirstSeenAt).Equal(normalizeS3OrphanRecoveryTime(right.FirstSeenAt)) &&
		left.StorageClass == right.StorageClass &&
		left.StorageKey == right.StorageKey &&
		left.ExternalSHA1 == right.ExternalSHA1 &&
		left.RecoveryPhase == right.RecoveryPhase
}

func (w *Worker) loadS3OrphansStartDay(cutoffDay time.Time) (time.Time, error) {
	value, err := w.store.LoadGCStats(gcS3OrphansCursorKey)
	return s3OrphansRecoveryStartDayFromCursor(value, err, cutoffDay)
}

func s3OrphansRecoveryStartDayFromCursor(value string, loadErr error, cutoffDay time.Time) (time.Time, error) {
	if loadErr != nil {
		if errors.Is(loadErr, gocql.ErrNotFound) {
			return s3OrphansRecoveryScanStartDay(time.Time{}, cutoffDay), nil
		}
		return time.Time{}, loadErr
	}
	lastDay, err := db.ParseGCProjectionDate(value)
	if err != nil {
		return time.Time{}, err
	}
	return s3OrphansRecoveryScanStartDay(lastDay, cutoffDay), nil
}

func s3OrphansRecoveryScanStartDay(lastProcessedDay, cutoffDay time.Time) time.Time {
	if lastProcessedDay.IsZero() {
		return cutoffDay.AddDate(0, 0, -gcS3OrphanInitialScanLookbackDays)
	}
	return lastProcessedDay.AddDate(0, 0, -gcScanOverlapDays)
}

// gcS3OrphanInitialScanLookbackDays bounds the cold-start recovery sweep when
// no cursor exists yet. Match the gc_s3_orphans / gc_s3_orphans_by_day TTL so
// the first pass can still see every live orphan row.
const gcS3OrphanInitialScanLookbackDays = 90

func (w *Worker) processCommit(item QueueItem) error {
	// Get the commit to find its root_fs_id for cascading deletion
	commit, err := w.store.GetCommit(item.LibraryID, item.ItemID)
	if err != nil {
		// Commit may already be deleted
		log.Printf("[GC Worker] Commit %s not found (may already be deleted)", item.ItemID)
		return nil
	}

	if w.dryRun.Load() {
		log.Printf("[GC Worker] DRY RUN: Would delete commit %s from library %s", item.ItemID, item.LibraryID)
		return nil
	}

	identityAt := effectiveIdentityAt(item.QueuedAt, item.IdentityAt)
	releaseGuard, fenceGuard, stale, err := w.acquireLibraryDeleteGuard(item)
	if err != nil {
		return err
	}
	if stale {
		return nil
	}
	defer releaseGuard()

	// Enqueue the root fs_object for cascading deletion (fs_object → blocks).
	// Use parent's QueuedAt so cascade children skip the grace period.
	// CRITICAL: if enqueue fails, we must NOT delete the commit — otherwise
	// the root fs_object becomes an orphan with no GC entry. The next scanner
	// sweep will re-discover and re-enqueue this commit.
	if commit.RootFSID != "" {
		exists, err := w.store.PendingItemExists(item.OrgID, item.LibraryID, time.Time{}, ItemFSObject, commit.RootFSID)
		if err != nil {
			return fmt.Errorf("failed to inspect root fs_object %s for commit %s: %w", commit.RootFSID, item.ItemID, err)
		}
		if !exists {
			child := QueueItem{
				OrgID:                       item.OrgID,
				QueuedAt:                    item.QueuedAt,
				IdentityAt:                  identityAt,
				RequiresLibraryDeletedCheck: item.RequiresLibraryDeletedCheck,
				LibraryGuardMode:            item.LibraryGuardMode,
				ItemType:                    ItemFSObject,
				ItemID:                      commit.RootFSID,
				LibraryID:                   item.LibraryID,
				BlockRepresentationID:       item.BlockRepresentationID,
			}
			if err := w.queue.EnqueueBatch([]QueueItem{child}); err != nil {
				return fmt.Errorf("failed to enqueue root fs_object %s for commit %s: %w", commit.RootFSID, item.ItemID, err)
			}
		}
	}

	// Fence immediately before the destructive delete: re-confirm we still own the
	// library hard-delete lock so a lease lost to expiry/restore cannot let us drop a
	// live library's commit. Fail closed (item stays queued and re-validates on retry).
	if err := fenceGuard(); err != nil {
		return err
	}
	if err := w.store.DeleteCommit(item.LibraryID, item.ItemID); err != nil {
		return fmt.Errorf("failed to delete commit: %w", err)
	}

	log.Printf("[GC Worker] Deleted commit %s", item.ItemID)
	return nil
}

func (w *Worker) processFSObject(ctx context.Context, item QueueItem) error {
	// Get the fs_object to find its block_ids
	fsObj, err := w.store.GetFSObject(item.LibraryID, item.ItemID)
	if err != nil {
		// Already deleted
		log.Printf("[GC Worker] FS object %s not found (may already be deleted)", item.ItemID)
		return nil
	}

	if w.dryRun.Load() {
		log.Printf("[GC Worker] DRY RUN: Would delete fs_object %s from library %s", item.ItemID, item.LibraryID)
		return nil
	}

	identityAt := effectiveIdentityAt(item.QueuedAt, item.IdentityAt)
	releaseGuard, fenceGuard, stale, err := w.acquireLibraryDeleteGuard(item)
	if err != nil {
		return err
	}
	if stale {
		return nil
	}
	defer releaseGuard()

	// If it's a directory, enqueue child fs_objects for recursive deletion.
	// Use parent's QueuedAt so cascade children skip the grace period.
	if len(fsObj.DirEntries) > 0 {
		var batch []QueueItem
		for _, childID := range fsObj.DirEntries {
			exists, err := w.store.PendingItemExists(item.OrgID, item.LibraryID, time.Time{}, ItemFSObject, childID)
			if err != nil {
				return fmt.Errorf("failed to inspect child fs_object %s for parent %s: %w", childID, item.ItemID, err)
			}
			if exists {
				continue
			}
			batch = append(batch, QueueItem{
				OrgID:                       item.OrgID,
				QueuedAt:                    item.QueuedAt,
				IdentityAt:                  identityAt,
				RequiresLibraryDeletedCheck: item.RequiresLibraryDeletedCheck,
				LibraryGuardMode:            item.LibraryGuardMode,
				ItemType:                    ItemFSObject,
				ItemID:                      childID,
				LibraryID:                   item.LibraryID,
				BlockRepresentationID:       item.BlockRepresentationID,
				StorageClass:                "",
				RetryCount:                  0,
			})
		}
		if err := w.queue.EnqueueBatch(batch); err != nil {
			log.Printf("[GC Worker] Failed to batch enqueue children for %s: %v", item.ItemID, err)
			return err
		}
	}

	// If it's a file with blocks, remove its permanent block references. Any block
	// left with no references becomes a GC candidate. Re-fence periodically during
	// long loops so a suspended worker cannot keep mutating with a stale lease.
	if len(fsObj.BlockIDs) > 0 {
		referenceFence := w.newTimedFence(fenceGuard, fsObjectReferenceFenceInterval)
		zeroRefBlocks, err := w.removeFSObjectBlockReferences(item.OrgID, item.LibraryID, item.BlockRepresentationID, item.ItemID, fsObj.BlockIDs, referenceFence)
		if err != nil {
			return err
		}
		storageClass, _ := w.store.GetLibraryStorageClass(item.OrgID, item.LibraryID)
		if len(zeroRefBlocks) > 0 {
			if err := w.enqueueZeroRefBlocks(item.OrgID, item.LibraryID, zeroRefBlocks, storageClass); err != nil {
				return fmt.Errorf("failed to enqueue zero-ref blocks for fs_object %s: %w", item.ItemID, err)
			}
		}
	}

	// Delete the fs_object. Fence immediately before this destructive delete so a lease
	// lost to expiry/restore cannot let us drop a node from a live/restored library
	// (the directory branch above releases no references, so this is its only fence).
	if err := fenceGuard(); err != nil {
		return err
	}
	if err := w.store.DeleteFSObject(item.LibraryID, item.ItemID); err != nil {
		return fmt.Errorf("failed to delete fs_object: %w", err)
	}

	log.Printf("[GC Worker] Deleted fs_object %s", item.ItemID)
	return nil
}

func (w *Worker) processShareLink(ctx context.Context, item QueueItem) error {
	if w.dryRun.Load() {
		log.Printf("[GC Worker] DRY RUN: Would delete share link %s", item.ItemID)
		return nil
	}

	if err := w.store.DeleteShareLink(item.ItemID, item.OrgID, item.LibraryID); err != nil {
		return fmt.Errorf("failed to delete share link: %w", err)
	}

	log.Printf("[GC Worker] Deleted share link %s", item.ItemID)
	return nil
}

func (w *Worker) processShare(ctx context.Context, item QueueItem) error {
	if w.dryRun.Load() {
		log.Printf("[GC Worker] DRY RUN: Would delete share %s", item.ItemID)
		return nil
	}

	shareID, err := uuid.Parse(item.ItemID)
	if err != nil {
		return fmt.Errorf("invalid share ID: %w", err)
	}

	if err := w.store.DeleteShare(item.LibraryID, shareID); err != nil {
		return fmt.Errorf("failed to delete share: %w", err)
	}

	log.Printf("[GC Worker] Deleted share %s", item.ItemID)
	return nil
}

func (w *Worker) processRestoreJob(ctx context.Context, item QueueItem) error {
	if w.dryRun.Load() {
		log.Printf("[GC Worker] DRY RUN: Would delete restore job %s", item.ItemID)
		return nil
	}

	jobID, err := uuid.Parse(item.ItemID)
	if err != nil {
		return fmt.Errorf("invalid restore job ID: %w", err)
	}

	if err := w.store.DeleteRestoreJob(item.OrgID, item.LibraryID, jobID); err != nil {
		return fmt.Errorf("failed to delete restore job: %w", err)
	}

	log.Printf("[GC Worker] Deleted restore job %s", item.ItemID)
	return nil
}

// processUserCascade performs the full cascade deletion of a soft-deleted user:
// 1. Soft-delete all owned libraries (move to trash)
// 2. Remove from all groups
// 3. Clean up shares received by and created by this user
// 4. Delete starred files and monitored repos
// 5. Hard-delete user record + email lookup
// 6. Audit log
func (w *Worker) processUserCascade(ctx context.Context, item QueueItem) error {
	if w.dryRun.Load() {
		log.Printf("[GC Worker] DRY RUN: Would cascade-delete user %s in org %s", item.ItemID, item.OrgID)
		return nil
	}

	userID, err := uuid.Parse(item.ItemID)
	if err != nil {
		return fmt.Errorf("invalid user ID: %w", err)
	}

	deletedAt, err := w.store.GetUserDeletedAt(item.OrgID, userID)
	if err != nil {
		return fmt.Errorf("failed to read deleted user marker for %s: %w", item.ItemID, err)
	}
	identityAt := effectiveIdentityAt(item.QueuedAt, item.IdentityAt)
	if deletedAt == nil || !deletedAt.Equal(identityAt) {
		log.Printf("[GC Worker] Skipping stale user cascade for %s (current deleted_at=%v identity_at=%v queued_at=%v)", item.ItemID, deletedAt, identityAt, item.QueuedAt)
		return nil
	}

	// Acquire a short-lived lock so a concurrent activateUser (restore) cannot
	// race between the stale-check above and the final HardDeleteUser write.
	leaseToken := uuid.New()
	acquired, err := w.store.AcquireUserHardDeleteLock(userID, leaseToken)
	if err != nil {
		return fmt.Errorf("failed to acquire user hard-delete lock for %s: %w", item.ItemID, err)
	}
	if !acquired {
		return hardDeleteInProgressError{Kind: "user", Target: userID.String(), ItemID: item.ItemID}
	}
	lease := newHardDeleteLease(ctx, "user", userID.String(), func() (bool, error) {
		return w.store.RenewUserHardDeleteLock(userID, leaseToken)
	}, func() error {
		return w.store.ReleaseUserHardDeleteLock(userID, leaseToken)
	})
	defer lease.Close()

	// Secondary stale-check after the lock: if the user was restored in the
	// window between the first stale-check and the lock acquisition, skip.
	deletedAt2, err := w.store.GetUserDeletedAt(item.OrgID, userID)
	if err != nil {
		return fmt.Errorf("failed to re-read deleted user marker for %s: %w", item.ItemID, err)
	}
	if deletedAt2 == nil || !deletedAt2.Equal(identityAt) {
		log.Printf("[GC Worker] Skipping user cascade for %s after lock: restored between checks (deleted_at=%v identity_at=%v)", item.ItemID, deletedAt2, identityAt)
		return nil
	}
	if err := lease.Check(); err != nil {
		return err
	}

	// Get user email before deletion (needed for users_by_email cleanup)
	email, err := w.store.GetUserEmail(item.OrgID, userID)
	if err != nil {
		return fmt.Errorf("failed to read user email for %s: %w", item.ItemID, err)
	}

	libCount, err := w.softDeleteUserLibraries(item.OrgID, userID)
	if err != nil {
		return fmt.Errorf("failed to soft-delete libraries owned by user %s: %w", item.ItemID, err)
	}
	if err := lease.Check(); err != nil {
		return err
	}

	groupCount, shareCount, err := w.cleanupUserArtifacts(item.OrgID, userID)
	if err != nil {
		return fmt.Errorf("failed to clean up artifacts for user %s: %w", item.ItemID, err)
	}
	if err := lease.Check(); err != nil {
		return err
	}

	// 5. Hard-delete user record + email lookup
	if err := w.store.HardDeleteUser(item.OrgID, userID, email); err != nil {
		return fmt.Errorf("failed to hard-delete user %s: %w", item.ItemID, err)
	}

	// 6. Audit log
	w.store.WriteAuditLog(AuditLogEntry{
		OrgID:      item.OrgID,
		Action:     "gc_user_cascade_deleted",
		TargetType: "user",
		TargetID:   item.ItemID,
		ActorID:    "gc_worker",
		Details:    fmt.Sprintf("email=%s libraries=%d groups=%d shares=%d", email, libCount, groupCount, shareCount),
		Timestamp:  time.Now(),
	})

	log.Printf("[GC Worker] Cascade-deleted user %s (%s): %d libraries, %d groups, %d shares",
		item.ItemID, email, libCount, groupCount, shareCount)
	return nil
}

func (w *Worker) softDeleteUserLibraries(orgID, userID uuid.UUID) (int, error) {
	libIDs, err := w.store.ListLibrariesByOwner(orgID, userID)
	if err != nil {
		return 0, fmt.Errorf("list owned libraries: %w", err)
	}

	var cleanupErr error
	for _, libID := range libIDs {
		if err := w.store.SoftDeleteLibrary(orgID, libID, userID); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("soft-delete library %s: %w", libID, err))
		}
	}
	return len(libIDs), cleanupErr
}

func (w *Worker) cleanupUserArtifacts(orgID, userID uuid.UUID) (int, int, error) {
	var cleanupErr error

	groupIDs, err := w.store.ListGroupMembershipsByUser(orgID, userID)
	if err != nil {
		cleanupErr = errors.Join(cleanupErr, fmt.Errorf("list group memberships: %w", err))
	} else {
		for _, groupID := range groupIDs {
			if err := w.store.DeleteGroupMember(groupID, userID); err != nil {
				cleanupErr = errors.Join(cleanupErr, fmt.Errorf("delete group member %s/%s: %w", groupID, userID, err))
			}
			if err := w.store.DeleteGroupByMember(orgID, userID, groupID); err != nil {
				cleanupErr = errors.Join(cleanupErr, fmt.Errorf("delete group by member %s/%s/%s: %w", orgID, userID, groupID, err))
			}
		}
	}

	shareCount, err := w.deleteUserShares(orgID, userID)
	if err != nil {
		cleanupErr = errors.Join(cleanupErr, err)
	}
	if err := w.store.DeleteStarredFilesByUser(userID); err != nil {
		cleanupErr = errors.Join(cleanupErr, fmt.Errorf("delete starred files: %w", err))
	}
	if err := w.store.DeleteMonitoredReposByUser(userID); err != nil {
		cleanupErr = errors.Join(cleanupErr, fmt.Errorf("delete monitored repos: %w", err))
	}
	if err := w.store.DeleteAPIKeysByUser(orgID, userID); err != nil {
		cleanupErr = errors.Join(cleanupErr, fmt.Errorf("delete api keys: %w", err))
	}

	return len(groupIDs), shareCount, cleanupErr
}

func (w *Worker) deleteUserShares(orgID, userID uuid.UUID) (int, error) {
	shareRefs := make(map[string]ShareInfo)
	var cleanupErr error

	receivedShares, err := w.store.ListSharesByUser(orgID, userID)
	if err != nil {
		cleanupErr = errors.Join(cleanupErr, fmt.Errorf("list received shares: %w", err))
	} else {
		for _, share := range receivedShares {
			key := share.LibraryID.String() + ":" + share.ShareID.String()
			shareRefs[key] = ShareInfo{LibraryID: share.LibraryID, ShareID: share.ShareID, SharedTo: share.SharedTo}
		}
	}

	createdShares, err := w.store.ListSharesCreatedByUser(orgID, userID)
	if err != nil {
		cleanupErr = errors.Join(cleanupErr, fmt.Errorf("list created shares: %w", err))
	} else {
		for _, share := range createdShares {
			key := share.LibraryID.String() + ":" + share.ShareID.String()
			shareRefs[key] = ShareInfo{LibraryID: share.LibraryID, ShareID: share.ShareID}
		}
	}

	for _, share := range shareRefs {
		if err := w.store.DeleteShare(share.LibraryID, share.ShareID); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("delete share %s/%s: %w", share.LibraryID, share.ShareID, err))
		}
	}

	return len(shareRefs), cleanupErr
}

// processLibraryCascade performs the full cascade deletion of a soft-deleted library:
// 1. Enqueue all commits, fs_objects, and clean up artifacts
// 2. Hard-delete the library record
// 3. Audit log
func (w *Worker) processLibraryCascade(ctx context.Context, item QueueItem) error {
	if w.dryRun.Load() {
		log.Printf("[GC Worker] DRY RUN: Would cascade-delete library %s in org %s", item.ItemID, item.OrgID)
		return nil
	}

	libraryID, err := uuid.Parse(item.ItemID)
	if err != nil {
		return fmt.Errorf("invalid library ID: %w", err)
	}

	deletedAt, err := w.store.GetLibraryDeletedAt(libraryID)
	if err != nil {
		return fmt.Errorf("failed to read deleted library marker for %s: %w", item.ItemID, err)
	}
	identityAt := effectiveIdentityAt(item.QueuedAt, item.IdentityAt)
	if deletedAt == nil || !deletedAt.Equal(identityAt) {
		if deletedAt == nil {
			// The delete marker is gone: either the library was restored (canonical
			// row present again) or a prior cascade pass already hard-deleted it. In
			// the latter case a crash between HardDeleteLibrary and
			// DeleteLibraryStorageCounter may have left the per-library storage
			// counter behind — reclaim it, but only after confirming the canonical
			// row is absent so we never disturb a restored library's live counter.
			if err := w.reclaimHardDeletedLibraryStorageCounter(item.OrgID, libraryID); err != nil {
				return err
			}
		}
		log.Printf("[GC Worker] Skipping stale library cascade for %s (current deleted_at=%v identity_at=%v queued_at=%v)", item.ItemID, deletedAt, identityAt, item.QueuedAt)
		return nil
	}

	lease, fenceLibrary, err := w.acquireLibraryCascadeLease(ctx, libraryID, item.ItemID)
	if err != nil {
		return err
	}
	defer lease.Close()

	// Second stale-check after acquiring the lock.
	deletedAt2, err := w.store.GetLibraryDeletedAt(libraryID)
	if err != nil {
		return fmt.Errorf("failed to re-read deleted library marker for %s: %w", item.ItemID, err)
	}
	if deletedAt2 == nil || !deletedAt2.Equal(identityAt) {
		log.Printf("[GC Worker] Skipping stale library cascade for %s after lock (current deleted_at=%v identity_at=%v queued_at=%v)", item.ItemID, deletedAt2, identityAt, item.QueuedAt)
		return nil
	}
	if err := lease.Check(); err != nil {
		return err
	}

	if err := w.cascadeDeleteLibrary(item.OrgID, libraryID, item.BlockRepresentationID, item.StorageClass, identityAt, fenceLibrary); err != nil {
		return err
	}
	return lease.Check()
}

// reclaimHardDeletedLibraryStorageCounter idempotently deletes the per-library
// storage counter of a library whose canonical row is already gone. It exists to
// recover from a crash between HardDeleteLibrary and DeleteLibraryStorageCounter
// (see cascadeDeleteLibrary): the hard delete succeeded but the counter cleanup
// did not, leaving an orphaned counter that would otherwise inflate future
// reconciliation. It refuses to act while the canonical row still exists, so a
// restored library keeps its live counter. Fails closed on read errors.
func (w *Worker) reclaimHardDeletedLibraryStorageCounter(orgID, libraryID uuid.UUID) error {
	exists, err := w.store.CanonicalLibraryExists(orgID, libraryID)
	if err != nil {
		return fmt.Errorf("failed to confirm canonical library absence before storage-counter reclaim for %s: %w", libraryID, err)
	}
	if exists {
		return nil
	}
	if err := w.store.DeleteLibraryStorageCounter(orgID, libraryID); err != nil {
		return fmt.Errorf("failed to reclaim storage counter for hard-deleted library %s: %w", libraryID, err)
	}
	return nil
}

func (w *Worker) acquireLibraryCascadeLease(ctx context.Context, libraryID uuid.UUID, itemID string) (*hardDeleteLease, func() error, error) {
	leaseToken := uuid.New()
	acquired, err := w.store.AcquireLibraryHardDeleteLock(libraryID, leaseToken)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to acquire library hard-delete lock for %s: %w", itemID, err)
	}
	if !acquired {
		return nil, nil, hardDeleteInProgressError{Kind: "library", Target: libraryID.String(), ItemID: itemID}
	}
	lease := newHardDeleteLease(ctx, "library", libraryID.String(), func() (bool, error) {
		return w.store.RenewLibraryHardDeleteLock(libraryID, leaseToken)
	}, func() error {
		return w.store.ReleaseLibraryHardDeleteLock(libraryID, leaseToken)
	})
	fence := func() error {
		owned, err := w.store.RenewLibraryHardDeleteLock(libraryID, leaseToken)
		if err != nil {
			return fmt.Errorf("failed to fence library cascade for %s: %w", libraryID, err)
		}
		if !owned {
			return fmt.Errorf("lost library hard-delete lock for %s", libraryID)
		}
		return nil
	}
	return lease, fence, nil
}

func (w *Worker) cascadeDeleteLibrary(orgID, libraryID uuid.UUID, blockRepresentationID, storageClass string, libraryDeletedAt time.Time, fenceLibrary func() error) error {
	if storageClass == "" {
		storageClass, _ = w.store.GetLibraryStorageClass(orgID, libraryID)
	}

	if err := w.enqueueLibraryContentsAt(orgID, libraryID, blockRepresentationID, storageClass, libraryDeletedAt, LibraryGuardDeletedAtIdentity); err != nil {
		return fmt.Errorf("failed to enqueue library contents: %w", err)
	}

	// Fence before the point of no return, then hard-delete the canonical row +
	// marker FIRST. Ordering matters: once the canonical `libraries` row is gone,
	// restoreDeletedLibrary can no longer resurrect the library, so a concurrent
	// restore cannot observe a deleted storage counter and reactivate an
	// under-counted library. Deleting the counter before the hard delete (the old
	// order) left exactly that window. See DEBT-GC-COUNTER-ORDERING history.
	if err := fenceLibrary(); err != nil {
		return err
	}
	if err := w.store.HardDeleteLibrary(orgID, libraryID); err != nil {
		return fmt.Errorf("failed to hard-delete library %s: %w", libraryID, err)
	}

	// The library is now definitively gone — record the audit here (not after the
	// counter cleanup) so the event is captured even if the reclamation below has to
	// be retried on a later pass.
	w.store.WriteAuditLog(AuditLogEntry{
		OrgID:      orgID,
		Action:     "gc_library_cascade_deleted",
		TargetType: "library",
		TargetID:   libraryID.String(),
		ActorID:    "gc_worker",
		Details:    fmt.Sprintf("storage_class=%s", storageClass),
		Timestamp:  time.Now(),
	})
	log.Printf("[GC Worker] Cascade-deleted library %s (storage_class=%s)", libraryID, storageClass)

	// Reclaim the per-library storage counter after the library is gone. No fence
	// here: the canonical row no longer exists, so losing the lease past this point
	// cannot corrupt a live/restored library. A failure still returns an error so the
	// item is retried; the retry lands on the canonical-absent reclamation path in
	// processLibraryCascade, which cleans the orphaned counter idempotently.
	if err := w.store.DeleteLibraryStorageCounter(orgID, libraryID); err != nil {
		return fmt.Errorf("failed to delete library storage counter for %s: %w", libraryID, err)
	}

	return nil
}

// processOrgCascade performs the full cascade deletion of a soft-deleted organization:
// 1. Cascade-delete all libraries synchronously
// 2. Clean up all users (shares, starred, monitored, hard-delete)
// 3. Delete all groups (members, by_member, by_id, group record)
// 4. Hard-delete org record
// 5. Audit log
func (w *Worker) processOrgCascade(ctx context.Context, item QueueItem) error {
	if w.dryRun.Load() {
		log.Printf("[GC Worker] DRY RUN: Would cascade-delete org %s", item.ItemID)
		return nil
	}

	orgID := item.OrgID
	deletedAt, err := w.store.GetOrgDeletedAt(orgID)
	if err != nil {
		return fmt.Errorf("failed to read deleted org marker for %s: %w", item.ItemID, err)
	}
	identityAt := effectiveIdentityAt(item.QueuedAt, item.IdentityAt)
	if deletedAt == nil || !deletedAt.Equal(identityAt) {
		log.Printf("[GC Worker] Skipping stale org cascade for %s (current deleted_at=%v identity_at=%v queued_at=%v)", item.ItemID, deletedAt, identityAt, item.QueuedAt)
		return nil
	}

	leaseToken := uuid.New()
	acquired, err := w.store.AcquireOrgHardDeleteLock(orgID, leaseToken)
	if err != nil {
		return fmt.Errorf("failed to acquire org hard-delete lock for %s: %w", item.ItemID, err)
	}
	if !acquired {
		return hardDeleteInProgressError{Kind: "org", Target: orgID.String(), ItemID: item.ItemID}
	}
	lease := newHardDeleteLease(ctx, "org", orgID.String(), func() (bool, error) {
		return w.store.RenewOrgHardDeleteLock(orgID, leaseToken)
	}, func() error {
		return w.store.ReleaseOrgHardDeleteLock(orgID, leaseToken)
	})
	defer lease.Close()

	// Secondary stale-check after the lock: if the org was restored in the
	// window between the first stale-check and the lock acquisition, skip.
	deletedAt2, err := w.store.GetOrgDeletedAt(orgID)
	if err != nil {
		return fmt.Errorf("failed to re-read deleted org marker for %s: %w", item.ItemID, err)
	}
	if deletedAt2 == nil || !deletedAt2.Equal(identityAt) {
		log.Printf("[GC Worker] Skipping org cascade for %s after lock: restored between checks (deleted_at=%v identity_at=%v)", item.ItemID, deletedAt2, identityAt)
		return nil
	}
	if err := lease.Check(); err != nil {
		return err
	}

	purging, err := w.store.BeginOrgPurge(orgID, identityAt)
	if err != nil {
		return fmt.Errorf("failed to transition org %s into purge state: %w", item.ItemID, err)
	}
	if !purging {
		log.Printf("[GC Worker] Skipping org cascade for %s after purge transition race (identity_at=%v)", item.ItemID, identityAt)
		return nil
	}

	orgName, err := w.store.GetOrgName(orgID)
	if err != nil {
		return fmt.Errorf("failed to read org name for %s: %w", item.ItemID, err)
	}

	libs, err := w.store.ListLibrariesForOrg(orgID)
	if err != nil {
		return fmt.Errorf("failed to list libraries for org %s: %w", item.ItemID, err)
	}
	for _, lib := range libs {
		if err := lease.Check(); err != nil {
			return err
		}
		libraryLease, fenceLibrary, err := w.acquireLibraryCascadeLease(ctx, lib.LibraryID, item.ItemID)
		if err != nil {
			return err
		}
		funcErr := func() error {
			defer libraryLease.Close()
			if err := lease.Check(); err != nil {
				return err
			}
			deletedLibraryAt, err := w.store.GetLibraryDeletedAt(lib.LibraryID)
			if err != nil {
				return fmt.Errorf("failed to read deleted library marker for %s during org cascade: %w", lib.LibraryID, err)
			}
			if deletedLibraryAt == nil {
				exists, err := w.store.CanonicalLibraryExists(orgID, lib.LibraryID)
				if err != nil {
					return fmt.Errorf("failed to read canonical library row for %s during org cascade: %w", lib.LibraryID, err)
				}
				if !exists {
					return nil
				}
				if err := w.store.SoftDeleteLibrary(orgID, lib.LibraryID, uuid.Nil); err != nil {
					return fmt.Errorf("failed to soft-delete library %s during org cascade: %w", lib.LibraryID, err)
				}
				deletedLibraryAt, err = w.store.GetLibraryDeletedAt(lib.LibraryID)
				if err != nil {
					return fmt.Errorf("failed to read deleted library marker for %s during org cascade: %w", lib.LibraryID, err)
				}
				if deletedLibraryAt == nil {
					return fmt.Errorf("missing deleted library marker for %s during org cascade", lib.LibraryID)
				}
			}
			blockRepresentationID, err := resolveRequiredLibraryBlockRepresentation(w.store, orgID, lib.LibraryID, "", "org cascade")
			if err != nil {
				return err
			}
			if err := libraryLease.Check(); err != nil {
				return err
			}
			if err := w.cascadeDeleteLibrary(orgID, lib.LibraryID, blockRepresentationID, lib.StorageClass, *deletedLibraryAt, fenceLibrary); err != nil {
				return fmt.Errorf("failed to cascade-delete library %s during org delete: %w", lib.LibraryID, err)
			}
			return libraryLease.Check()
		}()
		if funcErr != nil {
			return funcErr
		}
		if err := lease.Check(); err != nil {
			return err
		}
	}

	users, err := w.store.ListUsersByOrg(orgID)
	if err != nil {
		return fmt.Errorf("failed to list users for org %s: %w", item.ItemID, err)
	}
	for _, u := range users {
		if err := lease.Check(); err != nil {
			return err
		}
		if _, _, err := w.cleanupUserArtifacts(orgID, u.UserID); err != nil {
			return fmt.Errorf("failed to clean up user %s during org cascade: %w", u.UserID, err)
		}
		if err := w.store.HardDeleteUser(orgID, u.UserID, u.Email); err != nil {
			return fmt.Errorf("failed to hard-delete user %s during org cascade: %w", u.UserID, err)
		}
		if err := lease.Check(); err != nil {
			return err
		}
	}

	groupIDs, err := w.store.ListGroupsByOrg(orgID)
	if err != nil {
		return fmt.Errorf("failed to list groups for org %s: %w", item.ItemID, err)
	}
	for _, gid := range groupIDs {
		if err := lease.Check(); err != nil {
			return err
		}
		if err := w.store.DeleteGroupFull(orgID, gid); err != nil {
			return fmt.Errorf("failed to delete group %s during org cascade: %w", gid, err)
		}
		if err := lease.Check(); err != nil {
			return err
		}
	}
	if err := lease.Check(); err != nil {
		return err
	}

	if err := w.store.HardDeleteOrgLocked(orgID); err != nil {
		return fmt.Errorf("failed to hard-delete org %s: %w", item.ItemID, err)
	}

	w.store.WriteAuditLog(AuditLogEntry{
		OrgID:      orgID,
		Action:     "gc_org_cascade_deleted",
		TargetType: "organization",
		TargetID:   item.ItemID,
		ActorID:    "gc_worker",
		Details:    fmt.Sprintf("name=%s libraries=%d users=%d groups=%d", orgName, len(libs), len(users), len(groupIDs)),
		Timestamp:  time.Now(),
	})

	log.Printf("[GC Worker] Cascade-deleted org %s (%s): %d libraries, %d users, %d groups",
		item.ItemID, orgName, len(libs), len(users), len(groupIDs))
	return nil
}

func fsObjectBlockDecrementTaskID(libraryID uuid.UUID, fsID string, identityAt time.Time, blockIndex int, blockID string) uuid.UUID {
	taskIDStr := fmt.Sprintf("fs_object_block_decrement:%s:%s:%d:%d:%s", libraryID, fsID, identityAt.UnixNano(), blockIndex, blockID)
	return uuid.NewMD5(uuid.NameSpaceOID, []byte(taskIDStr))
}

// removeFSObjectBlockReferences deletes the permanent reference rows held by an
// fs_object (one "fs:<library>:<fs_id>" referrer per block) and returns the blocks
// that are now unreferenced, so the caller can enqueue them for GC. Block IDs are
// resolved to internal SHA-256 IDs first. Idempotent: deleting a missing reference
// is a no-op, so a retried fs_object GC pass is safe (no double-decrement risk —
// the whole class of decrement idempotency bugs disappears with the counter).
func (w *Worker) removeFSObjectBlockReferences(orgID, libraryID uuid.UUID, blockRepresentationID, fsID string, blockIDs []string, beforeMutation func() error) ([]string, error) {
	resolvedBlockIDs, err := w.store.ResolveBlockIDs(orgID, libraryID, blockRepresentationID, blockIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve block IDs for fs_object %s/%s: %w", libraryID, fsID, err)
	}

	referrer := db.BlockReferrerForFSObject(libraryID.String(), fsID)
	seen := make(map[string]struct{}, len(resolvedBlockIDs))
	var zeroRef []string
	for _, blockID := range resolvedBlockIDs {
		if _, dup := seen[blockID]; dup {
			continue
		}
		seen[blockID] = struct{}{}

		if err := beforeMutation(); err != nil {
			return nil, fmt.Errorf("failed to fence block reference cleanup for fs_object %s/%s block %s: %w", libraryID, fsID, blockID, err)
		}
		if err := w.store.RemoveBlockReference(orgID, blockID, referrer); err != nil {
			return nil, fmt.Errorf("failed to remove block reference for fs_object %s/%s block %s: %w", libraryID, fsID, blockID, err)
		}
		hasRefs, err := w.store.BlockHasReferences(orgID, blockID)
		if err != nil {
			return nil, fmt.Errorf("failed to check references for fs_object %s/%s block %s: %w", libraryID, fsID, blockID, err)
		}
		if !hasRefs {
			zeroRef = append(zeroRef, blockID)
		}
	}
	return zeroRef, nil
}

func (w *Worker) enqueueZeroRefBlocks(orgID, libraryID uuid.UUID, blockIDs []string, storageClass string) error {
	var blockBatch []QueueItem
	var candidateProjectionErr error
	for _, blockID := range blockIDs {
		candidateAt, candidateErr := w.store.EnsureBlockGCCandidate(orgID, blockID, storageClass, time.Now())
		if errors.Is(candidateErr, ErrBlockCandidateTargetUnavailable) {
			// Nothing reclaimable: no canonical row, or one with no usable locator. That is
			// a normal observation — processBlock treats the same state as routine further
			// down the walk — and it must not fail the batch.
			//
			// Failing here was self-poisoning on this path specifically. The caller aborts
			// the whole fs_object delete, the item retries, removeFSObjectBlockReferences
			// is idempotent so the same block comes back zero-ref, the canonical row is
			// still gone, and the fs_object never gets deleted at all.
			metrics.GCBlockCandidateDiscoveryDegradedTotal.WithLabelValues("worker").Inc()
			log.Printf("[GC Worker] block %s in org=%s has nothing reclaimable (%v); skipping it and continuing with its siblings", blockID, orgID, candidateErr)
			continue
		}
		if candidateErr != nil && candidateAt.IsZero() {
			return candidateErr
		}
		exists, err := w.store.PendingItemExists(orgID, uuid.Nil, candidateAt, ItemBlock, blockID)
		if err != nil {
			return errors.Join(candidateErr, candidateProjectionErr, err)
		}
		if exists {
			continue
		}
		if candidateErr != nil {
			metrics.GCBlockCandidateDiscoveryDegradedTotal.WithLabelValues("worker").Inc()
			log.Printf("[GC Worker] WARNING: block candidate discovery degraded for org=%s block=%s: %v", orgID, blockID, candidateErr)
			candidateProjectionErr = errors.Join(candidateProjectionErr, fmt.Errorf("ensure block GC candidate projection for block %s: %w", blockID, candidateErr))
		}
		blockBatch = append(blockBatch, QueueItem{
			OrgID:    orgID,
			QueuedAt: candidateAt,
			ItemType: ItemBlock,
			ItemID:   blockID,
			// Blocks are content-addressed and library-independent: processBlock only
			// uses OrgID+ItemID. Enqueue every block under uuid.Nil, matching the
			// uuid.Nil dedup check above and the scanner's orphan-block path. A single
			// producer is self-consistent even with a real libraryID (CompleteItem
			// re-reads it from the same queue row), but gc_pending_items is
			// library-scoped in its key while gc_queue is not: if a second producer
			// (the scanner, or another library sharing this block) enqueues the same
			// block/candidate under a different libraryID, the single gc_queue row keeps
			// only the last writer's library_id column while BOTH producers' pending
			// rows survive — and CompleteItem then deletes only the one matching the
			// surviving queue row, orphaning the other forever. Keying every producer
			// under uuid.Nil collapses them to one pending row. The store-level pending
			// helpers coerce ItemBlock to uuid.Nil as the backstop.
			// See ISSUE-GC-PENDING-ITEM-BLOCK-LIBRARY-SCOPE-01.
			LibraryID:    uuid.Nil,
			StorageClass: storageClass,
			RetryCount:   0,
		})
	}
	if len(blockBatch) == 0 {
		return nil
	}
	if err := w.queue.EnqueueBatch(blockBatch); err != nil {
		return errors.Join(candidateProjectionErr, err)
	}
	return nil
}

// EnqueueLibraryContents enqueues all contents of a deleted library for GC.
// Only enqueues commits and fs_objects — blocks are handled in cascade
// when fs_objects are processed (via decrementFSObjectBlocks).
func (w *Worker) EnqueueLibraryContents(orgID, libraryID uuid.UUID, storageClass string) error {
	return w.enqueueLibraryContentsAt(orgID, libraryID, "", storageClass, w.clock(), LibraryGuardNone)
}

func (w *Worker) enqueueLibraryContentsAt(orgID, libraryID uuid.UUID, blockRepresentationID, storageClass string, identityAt time.Time, libraryGuardMode LibraryGuardMode) error {
	if identityAt.IsZero() {
		identityAt = w.clock()
	}
	resolvedBlockRepresentationID, err := resolveRequiredLibraryBlockRepresentation(w.store, orgID, libraryID, blockRepresentationID, "library contents enqueue")
	if err != nil {
		return err
	}
	blockRepresentationID = resolvedBlockRepresentationID
	requiresLibraryDeletedCheck := libraryGuardMode != LibraryGuardNone

	// Enqueue all commits for this library (batched)
	commits, err := w.store.ListCommitsForLibrary(libraryID)
	if err != nil {
		return fmt.Errorf("failed to list commits for library %s: %w", libraryID, err)
	}
	if len(commits) > 0 {
		batch := make([]QueueItem, 0, len(commits))
		for _, c := range commits {
			exists, err := w.store.PendingItemExists(orgID, libraryID, identityAt, ItemCommit, c.CommitID)
			if err != nil {
				return fmt.Errorf("failed to check commit queue state for library %s: %w", libraryID, err)
			}
			if exists {
				continue
			}
			batch = append(batch, QueueItem{
				OrgID: orgID, QueuedAt: identityAt, IdentityAt: identityAt, RequiresLibraryDeletedCheck: requiresLibraryDeletedCheck, LibraryGuardMode: libraryGuardMode, ItemType: ItemCommit,
				ItemID: c.CommitID, LibraryID: libraryID, BlockRepresentationID: blockRepresentationID,
			})
		}
		if len(batch) > 0 {
			if err := w.queue.EnqueueBatch(batch); err != nil {
				return fmt.Errorf("failed to batch enqueue commits for library %s: %w", libraryID, err)
			}
		}
	}

	// Enqueue all fs_objects (batched; blocks will cascade via processFSObject)
	fsObjects, err := w.store.ListFSObjectsForLibrary(libraryID)
	if err != nil {
		return fmt.Errorf("failed to list fs_objects for library %s: %w", libraryID, err)
	}
	if len(fsObjects) > 0 {
		batch := make([]QueueItem, 0, len(fsObjects))
		for _, obj := range fsObjects {
			exists, err := w.store.PendingItemExists(orgID, libraryID, identityAt, ItemFSObject, obj.FSID)
			if err != nil {
				return fmt.Errorf("failed to check fs_object queue state for library %s: %w", libraryID, err)
			}
			if exists {
				continue
			}
			batch = append(batch, QueueItem{
				OrgID: orgID, QueuedAt: identityAt, IdentityAt: identityAt, RequiresLibraryDeletedCheck: requiresLibraryDeletedCheck, LibraryGuardMode: libraryGuardMode, ItemType: ItemFSObject,
				ItemID: obj.FSID, LibraryID: libraryID, BlockRepresentationID: blockRepresentationID,
			})
		}
		if len(batch) > 0 {
			if err := w.queue.EnqueueBatch(batch); err != nil {
				return fmt.Errorf("failed to batch enqueue fs_objects for library %s: %w", libraryID, err)
			}
		}
	}

	// Clean up library-specific artifacts that don't cascade through fs_objects
	shareCount, linkCount, err := w.enqueueLibraryArtifacts(orgID, libraryID)
	if err != nil {
		return err
	}

	log.Printf("[GC Worker] Enqueued library %s contents for deletion (%d commits, %d fs_objects, %d shares, %d share links)", libraryID, len(commits), len(fsObjects), shareCount, linkCount)
	return nil
}

func (w *Worker) acquireLibraryDeleteGuard(item QueueItem) (func(), func() error, bool, error) {
	guardMode := effectiveLibraryGuardMode(item.LibraryGuardMode, item.RequiresLibraryDeletedCheck)
	if guardMode == LibraryGuardNone {
		return func() {}, func() error { return nil }, false, nil
	}
	identityAt := effectiveIdentityAt(item.QueuedAt, item.IdentityAt)
	if guardMode == LibraryGuardDeletedAtIdentity {
		deletedAt, err := w.store.GetLibraryDeletedAt(item.LibraryID)
		if err != nil {
			return nil, nil, false, fmt.Errorf("failed to read deleted library marker for %s/%s: %w", item.LibraryID, item.ItemID, err)
		}
		if deletedAt == nil {
			exists, err := w.store.CanonicalLibraryExists(item.OrgID, item.LibraryID)
			if err != nil {
				return nil, nil, false, fmt.Errorf("failed to confirm canonical library absence for completed cascade %s/%s: %w", item.LibraryID, item.ItemID, err)
			}
			if exists {
				log.Printf("[GC Worker] Skipping stale guarded item %s/%s: delete marker is gone but canonical library exists", item.LibraryID, item.ItemID)
				return func() {}, func() error { return nil }, true, nil
			}
			// The parent cascade already hard-deleted both the canonical row and its
			// marker. Its children must keep draining; the row can no longer be
			// restored through the guarded restore path.
			return func() {}, func() error { return nil }, false, nil
		}
		if !deletedAt.Equal(identityAt) {
			log.Printf("[GC Worker] Skipping stale guarded item %s/%s (current deleted_at=%v identity_at=%v)", item.LibraryID, item.ItemID, deletedAt, identityAt)
			return func() {}, func() error { return nil }, true, nil
		}
	} else if guardMode != LibraryGuardCanonicalMustBeAbsent {
		return nil, nil, false, fmt.Errorf("unknown library guard mode %q for %s/%s", guardMode, item.LibraryID, item.ItemID)
	}

	leaseToken := uuid.New()
	acquired, err := w.store.AcquireLibraryHardDeleteLock(item.LibraryID, leaseToken)
	if err != nil {
		return nil, nil, false, fmt.Errorf("failed to acquire library hard-delete lock for child %s/%s: %w", item.LibraryID, item.ItemID, err)
	}
	if !acquired {
		return nil, nil, false, libraryHardDeleteInProgressError{LibraryID: item.LibraryID, ItemID: item.ItemID}
	}

	release := func() {
		_ = w.store.ReleaseLibraryHardDeleteLock(item.LibraryID, leaseToken)
	}

	if guardMode == LibraryGuardCanonicalMustBeAbsent {
		exists, err := w.store.CanonicalLibraryExists(item.OrgID, item.LibraryID)
		if err != nil {
			release()
			return nil, nil, false, fmt.Errorf("failed to confirm canonical library absence for %s/%s: %w", item.LibraryID, item.ItemID, err)
		}
		if exists {
			release()
			log.Printf("[GC Worker] Skipping orphan item %s/%s: canonical library exists", item.LibraryID, item.ItemID)
			return func() {}, func() error { return nil }, true, nil
		}
	} else {
		deletedAt, err := w.store.GetLibraryDeletedAt(item.LibraryID)
		if err != nil {
			release()
			return nil, nil, false, fmt.Errorf("failed to re-read deleted library marker for child %s/%s: %w", item.LibraryID, item.ItemID, err)
		}
		if deletedAt == nil || !deletedAt.Equal(identityAt) {
			release()
			log.Printf("[GC Worker] Skipping stale guarded item %s/%s after lock (current deleted_at=%v identity_at=%v)", item.LibraryID, item.ItemID, deletedAt, identityAt)
			return func() {}, func() error { return nil }, true, nil
		}
		// The delete marker still matches, but a matching marker does NOT prove the parent
		// cascade finished HardDeleteLibrary. HardDeleteLibrary removes the canonical
		// `libraries` row and the marker together; while the canonical row still exists the
		// library is soft-deleted and RESTORABLE. If a child reaches here with the canonical
		// row present, the parent crashed after enqueuing children but before the canonical
		// delete (and this worker stole its stale lease). Purging content now would let a
		// later restore revive a partially-purged library. Require the canonical row to be
		// gone; otherwise postpone (no retry burn, no DLQ) until the cascade is re-driven and
		// completes the hard delete. In the normal flow children run only after
		// HardDeleteLibrary, so the marker is already gone and this branch is not reached.
		exists, err := w.store.CanonicalLibraryExists(item.OrgID, item.LibraryID)
		if err != nil {
			release()
			return nil, nil, false, fmt.Errorf("failed to confirm canonical library absence for guarded child %s/%s: %w", item.LibraryID, item.ItemID, err)
		}
		if exists {
			release()
			log.Printf("[GC Worker] Postponing guarded item %s/%s: canonical library still present (cascade not yet hard-deleted)", item.LibraryID, item.ItemID)
			return nil, nil, false, libraryHardDeleteInProgressError{LibraryID: item.LibraryID, ItemID: item.ItemID}
		}
	}

	fence := func() error {
		owned, err := w.store.RenewLibraryHardDeleteLock(item.LibraryID, leaseToken)
		if err != nil {
			return fmt.Errorf("failed to fence library delete for %s/%s: %w", item.LibraryID, item.ItemID, err)
		}
		if !owned {
			return fmt.Errorf("lost library delete lock for %s/%s", item.LibraryID, item.ItemID)
		}
		return nil
	}
	return release, fence, false, nil
}

// enqueueLibraryArtifacts cleans up ALL auxiliary data tied to a deleted library:
// share links, shares, tags, tag counters, api tokens, locked files,
// starred files, monitored repos, and restore jobs.
func (w *Worker) enqueueLibraryArtifacts(orgID, libraryID uuid.UUID) (int, int, error) {
	var cleanupErr error
	joinErr := func(label string, err error) {
		if err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("%s: %w", label, err))
		}
	}

	// Delete share links via the by_library index (efficient)
	tokens, err := w.store.DeleteShareLinksByLibrary(orgID, libraryID)
	joinErr("delete share links", err)
	if err == nil && len(tokens) > 0 {
		log.Printf("[GC Worker] Cleaned up %d share links for deleted library %s", len(tokens), libraryID)
	}

	// Delete shares (user-to-user and group shares)
	shares, err := w.store.ListSharesByLibrary(libraryID)
	joinErr("list shares", err)
	if err == nil {
		for _, share := range shares {
			joinErr("delete share", w.store.DeleteShare(libraryID, share.ShareID))
		}
		if len(shares) > 0 {
			log.Printf("[GC Worker] Cleaned up %d shares for deleted library %s", len(shares), libraryID)
		}
	}

	// Delete repo tags and file tags
	joinErr("cleanup tags", w.cleanupLibraryTags(libraryID))

	// Delete tag counter tables (repo_tag_counters, file_tag_counters, repo_tag_file_counts)
	joinErr("delete repo tag counters", w.store.DeleteRepoTagCounters(libraryID))
	joinErr("delete file tag counters", w.store.DeleteFileTagCounters(libraryID))
	joinErr("delete repo tag file counts", w.store.DeleteRepoTagFileCounts(libraryID))

	// Delete API tokens
	tokens2, err := w.store.ListRepoAPITokensByLibrary(libraryID)
	joinErr("list repo api tokens", err)
	if err == nil {
		for _, t := range tokens2 {
			joinErr("delete repo api token", w.store.DeleteRepoAPIToken(libraryID, t.AppName))
			joinErr("delete repo api token by token", w.store.DeleteRepoAPITokenByToken(t.APIToken))
		}
	}

	// Delete locked files
	joinErr("delete locked files", w.store.DeleteLockedFilesByLibrary(libraryID))

	// Delete starred files referencing this library
	joinErr("delete starred files", w.store.DeleteStarredFilesByLibrary(libraryID))

	// Delete monitored repos referencing this library
	joinErr("delete monitored repos", w.store.DeleteMonitoredReposByLibrary(libraryID))

	// Delete restore jobs for this library
	joinErr("delete restore jobs", w.store.DeleteRestoreJobsByLibrary(orgID, libraryID))

	if cleanupErr != nil {
		return len(shares), len(tokens), fmt.Errorf("failed to clean auxiliary artifacts for library %s: %w", libraryID, cleanupErr)
	}

	// Audit log
	w.store.WriteAuditLog(AuditLogEntry{
		OrgID:      orgID,
		Action:     "gc_library_artifacts_cleaned",
		TargetType: "library",
		TargetID:   libraryID.String(),
		ActorID:    "gc_worker",
		Details:    fmt.Sprintf("shares=%d links=%d", len(shares), len(tokens)),
		Timestamp:  time.Now(),
	})

	return len(shares), len(tokens), nil
}

func (w *Worker) cleanupLibraryTags(libraryID uuid.UUID) error {
	var cleanupErr error
	joinErr := func(label string, err error) {
		if err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("%s: %w", label, err))
		}
	}

	// Delete file tags first (they reference repo tags)
	fileTags, err := w.store.ListFileTagsByLibrary(libraryID)
	if err != nil {
		return err
	}
	for _, ft := range fileTags {
		joinErr("delete file tag", w.store.DeleteFileTag(libraryID, ft.FilePath, ft.TagID))
		joinErr("delete file tag by id", w.store.DeleteFileTagByID(libraryID, ft.FileTagID))
	}

	// Delete repo tag definitions
	tagIDs, err := w.store.ListRepoTagsByLibrary(libraryID)
	if err != nil {
		return err
	}
	for _, tagIDStr := range tagIDs {
		var tagID int
		if _, err := fmt.Sscanf(tagIDStr, "%d", &tagID); err == nil {
			joinErr("delete repo tag", w.store.DeleteRepoTag(libraryID, tagID))
		}
	}

	return cleanupErr
}
