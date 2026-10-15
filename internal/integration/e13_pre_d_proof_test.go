//go:build integration

package integration

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	v2pkg "github.com/Sesame-Disk/sesamefs/internal/api/v2"
	"github.com/Sesame-Disk/sesamefs/internal/config"
	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
	gcpkg "github.com/Sesame-Disk/sesamefs/internal/gc"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

// Observe completed real SELECT responses; never alter rows or classify liveness.
// A dedicated worker session and exact fixture predicates isolate the barrier.
type e13ProofObserver struct {
	mu               sync.Mutex
	org, block, mode string
	refs             []int
	buckets          map[int]int
	repairWrites     int
	settlementWrites []string
	queryErrors      []string
	reached, release chan struct{}
	stopped          bool
	releaseOnce      sync.Once
}

func e13Observer(org, block, mode string) *e13ProofObserver {
	return &e13ProofObserver{org: org, block: block, mode: mode, buckets: map[int]int{}, reached: make(chan struct{}), release: make(chan struct{})}
}
func (o *e13ProofObserver) resume() { o.releaseOnce.Do(func() { close(o.release) }) }
func (o *e13ProofObserver) ObserveQuery(_ context.Context, q gocql.ObservedQuery) {
	statement := strings.ToLower(strings.Join(strings.Fields(q.Statement), " "))
	refRead := strings.HasPrefix(statement, "select referrer from block_references ") && len(q.Values) == 2 && fmt.Sprint(q.Values[0]) == o.org && fmt.Sprint(q.Values[1]) == o.block && q.Query.GetConsistency() == gocql.EachQuorum
	repairRead := strings.HasPrefix(statement, "select staged_block_ids from published_block_reference_repairs ") && len(q.Values) == 2 && fmt.Sprint(q.Values[1]) == o.org && q.Query.GetConsistency() == gocql.EachQuorum
	repairWrite := strings.HasPrefix(statement, "insert into published_block_reference_repairs ") && len(q.Values) >= 6 && fmt.Sprint(q.Values[1]) == o.org
	fsWrite := strings.HasPrefix(statement, "insert into block_references ") && len(q.Values) >= 3 && fmt.Sprint(q.Values[0]) == o.org && fmt.Sprint(q.Values[1]) == o.block && strings.HasPrefix(fmt.Sprint(q.Values[2]), "fs:")
	repairDelete := strings.HasPrefix(statement, "delete from published_block_reference_repairs ") && len(q.Values) == 5 && fmt.Sprint(q.Values[1]) == o.org
	if !refRead && !repairRead && !repairWrite && !fsWrite && !repairDelete {
		return
	}
	o.mu.Lock()
	pause := false
	if q.Err != nil {
		o.queryErrors = append(o.queryErrors, q.Err.Error())
	} else {
		if fsWrite {
			o.settlementWrites = append(o.settlementWrites, "fs:")
		}
		if repairDelete {
			o.settlementWrites = append(o.settlementWrites, "repair-delete")
		}
		if repairWrite {
			o.repairWrites++
		}
		if refRead {
			o.refs = append(o.refs, q.Rows)
			pause = o.mode == "settlement" && len(o.refs) == 1 && q.Rows == 0
		}
		if repairRead {
			bucket, ok := q.Values[0].(int)
			if ok {
				o.buckets[bucket] = q.Rows
			}
			pause = o.mode == "late" && ok && bucket == dbpkg.PublishedBlockReferenceRepairBuckets-1 && q.Rows == 0
		}
	}
	pause = pause && !o.stopped
	if pause {
		o.stopped = true
	}
	o.mu.Unlock()
	if pause {
		close(o.reached)
		select {
		case <-o.release:
		case <-time.After(20 * time.Second):
			o.mu.Lock()
			o.queryErrors = append(o.queryErrors, "proof barrier timed out")
			o.mu.Unlock()
		}
	}
}
func (o *e13ProofObserver) assertProof(t *testing.T, secondRows int) {
	t.Helper()
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.queryErrors) != 0 || len(o.refs) != 2 || o.refs[0] != 0 || o.refs[1] != secondRows || len(o.buckets) != dbpkg.PublishedBlockReferenceRepairBuckets {
		t.Fatalf("real proof trace: refs=%v buckets=%v errors=%v; want zero -> negative complete scan -> refs=%d", o.refs, o.buckets, o.queryErrors, secondRows)
	}
	for bucket, rows := range o.buckets {
		if rows != 0 {
			t.Fatalf("repair scan bucket=%d rows=%d; want negative", bucket, rows)
		}
	}
}
func e13Wait(t *testing.T, done <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatalf("timed out waiting for %s", label)
	}
}
func e13AssertClaim(t *testing.T, fx *w2CreateFileFixture) string {
	t.Helper()
	state, claim, handoff, class, key := x1ReadCommittedRow(t, fx.database, fx.orgUUID, fx.blockID)
	if state != dbpkg.BlockGCStateDeleting || claim == "" || handoff || class != fx.target.StorageClass || key != fx.target.StorageKey {
		t.Fatalf("proof must hold settled exact P1 claim before D: state=%s claim=%s handoff=%t P=(%s,%s)", state, claim, handoff, class, key)
	}
	return claim
}

type e13WorkerResult struct {
	processed int
	err       error
}

func e13StartWorker(t *testing.T, fx *w2CreateFileFixture, observer *e13ProofObserver) (<-chan e13WorkerResult, <-chan struct{}) {
	t.Helper()
	database := w2EvidenceSession(t, splitEnvOrDefault("CASSANDRA_HOSTS", "cassandra:9042")[0], observer)
	// Retain the observed native session; the destructive gate must receive the
	// fixture's complete validated topology, not the helper's local-DC default.
	// w2RepairFixture already requires config.Load through its database helper.
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	database = dbpkg.NewPublicationEvidenceDB(database.Session(), cfg.Database)
	store := gcpkg.NewCassandraStore(database)
	candidate := w2Candidate(t, gcpkg.NewCassandraStore(fx.database), fx.orgUUID, fx.blockID, fx.target.StorageClass)
	scope := &w2ClosureOwnedQueue{GCStore: store, org: fx.orgUUID, block: fx.blockID, identity: candidate.Identity()}
	worker := w2Worker(t, scope, fx.target.StorageClass)
	result, done := make(chan e13WorkerResult, 1), make(chan struct{})
	go func() {
		defer close(done)
		n, err := worker.ProcessOrgOnce(t.Context(), fx.orgUUID)
		result <- e13WorkerResult{n, err}
	}()
	// Registered after session.Close: release/join before sessions and fixture teardown.
	t.Cleanup(func() { observer.resume(); e13Wait(t, done, "worker teardown") })
	return result, done
}

func TestE13LatePublicationPreDProof(t *testing.T) {
	requireCassandra(t)
	t.Run("noGCWriterControl", func(t *testing.T) {
		fx := w2RepairFixture(t)
		if rec := fx.create(t); rec.Code != http.StatusCreated {
			t.Fatalf("writer control: %d %s", rec.Code, rec.Body.String())
		}
		fx.assertHeadAdvanced(t)
		if !fx.hasOwnFSReferrer(t) || len(w2Repairs(t, fx)) != 0 {
			t.Fatal("writer control failed permanent settlement")
		}
		w2AssertBytes(t, fx)
	})
	t.Run("claimWinsBeforeLateRepair", func(t *testing.T) {
		fx := w2RepairFixture(t)
		staged, resumeWriter, writerDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
		var resumeOnce sync.Once
		resume := func() { resumeOnce.Do(func() { close(resumeWriter) }) }
		restore := v2pkg.SetFileFromBlocksPublicationBarriersForTest(fx.repoID, nil, nil, func() { close(staged); <-resumeWriter }, nil)
		t.Cleanup(restore)
		writerTrace := e13Observer(fx.orgID, fx.blockID, "writer")
		writerDB := w2EvidenceSession(t, splitEnvOrDefault("CASSANDRA_HOSTS", "cassandra:9042")[0], writerTrace)
		fx.handler = newBorrowedFSHeadHandler(t, writerDB, x1StorageClass(t))
		var rec *httptest.ResponseRecorder
		go func() { defer close(writerDone); rec = fx.create(t) }()
		t.Cleanup(func() { resume(); e13Wait(t, writerDone, "writer teardown") })
		e13Wait(t, staged, "writer before durable repair")
		w2ExpireTemporaryRefs(t, fx) // Explicit expiry-state control, not TTL policy.
		if rows := w2Repairs(t, fx); len(rows) != 0 {
			t.Fatalf("repair acquired before GC: %+v", rows)
		}
		proof := e13Observer(fx.orgID, fx.blockID, "late")
		result, workerDone := e13StartWorker(t, fx, proof)
		e13Wait(t, proof.reached, "negative repair scan")
		claim := e13AssertClaim(t, fx)
		resume()
		e13Wait(t, writerDone, "late writer rejection")
		if head := borrowedFSReadHead(t, fx.database, fx.orgID, fx.repoID); head != fx.headBefore {
			t.Fatalf("late writer published HEAD under settled GC claim: before=%s after=%s", fx.headBefore, head)
		}
		if rec == nil || rec.Code != http.StatusConflict {
			t.Fatalf("late exact-P must reject: response=%+v", rec)
		}
		if e13AssertClaim(t, fx) != claim {
			t.Fatal("worker claim changed while paused")
		}
		writerTrace.mu.Lock()
		writes := writerTrace.repairWrites
		writerTrace.mu.Unlock()
		if writes == 0 {
			t.Fatal("writer did not acquire its durable repair late")
		}
		if fx.hasOwnFSReferrer(t) {
			t.Fatal("late rejected writer installed permanent fs:")
		}
		if rows := w2Repairs(t, fx); len(rows) != 0 {
			t.Fatalf("definite rejection did not clear owned repair: %+v", rows)
		}
		refs, err := fx.database.ListBlockReferrers(fx.orgID, fx.blockID)
		if err != nil || len(refs) != 0 {
			t.Fatalf("rejected writer left liveness: %v err=%v", refs, err)
		}
		proof.resume()
		e13Wait(t, workerDone, "GC after rejected publication")
		outcome := <-result
		if outcome.err != nil || outcome.processed != 1 {
			t.Fatalf("zero-ref continuation: processed=%d err=%v", outcome.processed, outcome.err)
		}
		proof.assertProof(t, 0)
		store := gcpkg.NewCassandraStore(fx.database)
		w2AssertCommittedContinuation(t, store, fx.orgUUID, fx.blockID, fx.target.StorageClass, fx.target.StorageKey, newVerificationBlockStore(t, fx.orgID))
		fx.assertHeadUnchanged(t)
		t.Logf("E1-3 late acquisition: exact claim=%s; real late repair insert observed; final exact-P=409; HEAD unchanged; second refs=0; D1 terminal; P1/K1 retired", claim)
	})
	t.Run("reachableSettlementWinsDuringProof", func(t *testing.T) {
		fx := w2RepairFixture(t)
		func() {
			restore := v2pkg.SetW2PublicationAfterHeadForTest(fx.repoID, func() { panic("w2-process-death") })
			defer restore()
			w2Crash(t, fx)
		}()
		fx.assertHeadAdvanced(t)
		head := borrowedFSReadHead(t, fx.database, fx.orgID, fx.repoID)
		rows := w2Repairs(t, fx)
		if len(rows) != 1 || rows[0].commitID != head {
			t.Fatalf("reachable repair missing: HEAD=%s rows=%+v", head, rows)
		}
		r := rows[0]
		w24AssertHeadReaches(t, fx, head, r.fsID)
		w2ExpireTemporaryRefs(t, fx)
		w2AssertGuardOnly(t, fx)
		proof := e13Observer(fx.orgID, fx.blockID, "settlement")
		result, workerDone := e13StartWorker(t, fx, proof)
		e13Wait(t, proof.reached, "first real refs=0 response")
		claim := e13AssertClaim(t, fx)
		recoveryTrace := e13Observer(fx.orgID, fx.blockID, "recovery")
		recovery := w2EvidenceSession(t, splitEnvOrDefault("CASSANDRA_HOSTS", "cassandra:9042")[0], recoveryTrace)
		classification, err := v2pkg.ClassifyPublishedBlockReferenceRepairResumableForIntegration(recovery, fx.orgID, fx.repoID, r.commitID, r.fsID)
		if err != nil || classification != "reachable" {
			t.Fatalf("productive repair classification=%q err=%v", classification, err)
		}
		if err := v2pkg.RepairPublishedFSObjectBlockReferenceRepair(recovery, fx.orgID, fx.repoID, r.commitID, r.fsID, r.blocks); err != nil {
			t.Fatal(err)
		}
		recoveryTrace.mu.Lock()
		writeOrder := append([]string(nil), recoveryTrace.settlementWrites...)
		recoveryTrace.mu.Unlock()
		if strings.Join(writeOrder, ",") != "fs:,repair-delete" {
			t.Fatalf("productive settlement order=%v; want acknowledged fs: then repair-delete", writeOrder)
		}
		if remaining := w2Repairs(t, fx); len(remaining) != 0 {
			t.Fatalf("settlement retained repair: %+v", remaining)
		}
		refs, err := fx.database.ListBlockReferrers(fx.orgID, fx.blockID)
		expected := dbpkg.BlockReferrerForFSObject(fx.repoID, r.fsID)
		if err != nil || len(refs) != 1 || refs[0] != expected {
			t.Fatalf("settlement must install exact fs: before negative scan: refs=%v err=%v", refs, err)
		}
		if e13AssertClaim(t, fx) != claim {
			t.Fatal("settlement revoked GC claim")
		}
		proof.resume()
		e13Wait(t, workerDone, "second real refs read")
		outcome := <-result
		if outcome.err != nil || outcome.processed != 1 {
			t.Fatalf("settlement queue result: processed=%d err=%v", outcome.processed, outcome.err)
		}
		// processed counts a live-item settlement too; destructive authority
		// is established by the actual canonical/lifecycle state, not this count.
		if exists, err := gcpkg.NewCassandraStore(fx.database).BlockExists(fx.orgUUID, fx.blockID); err != nil || !exists {
			t.Fatalf("E1-3 settlement destroyed canonical P1: exists=%v err=%v", exists, err)
		}
		proof.assertProof(t, 1)
		e12AssertNoDeleteLifecycle(t, fx)
		if borrowedFSReadHead(t, fx.database, fx.orgID, fx.repoID) != head {
			t.Fatal("settlement changed HEAD")
		}
		w24AssertHeadReaches(t, fx, head, r.fsID)
		if fx.readTarget(t) != fx.target {
			t.Fatal("settlement changed exact P1")
		}
		w2AssertBytes(t, fx)
		t.Logf("E1-3 settlement: exact claim=%s; first refs=0; productive REACHABLE promotion; repair gone; 32 empty repair buckets; second refs=1; claim released; no D; HEAD/P1/K1 intact", claim)
	})
}
