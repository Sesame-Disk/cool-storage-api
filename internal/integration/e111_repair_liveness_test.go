//go:build integration

package integration

import (
	"context"
	"errors"
	"flag"
	"os"
	"os/exec"
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

const e111EvidenceEnv = "SESAMEFS_REQUIRE_E111_REPAIR_LIVENESS_EVIDENCE"

var e111Observed = map[string]bool{}
var e111Legs = []string{"normal", "during-classify", "reachable-post-classify", "unknown-retained", "classifier-wire-error", "guard-wire-error"}

func e111Missing(observed map[string]bool) []string {
	var missing []string
	for _, leg := range e111Legs {
		if !observed[leg] {
			missing = append(missing, leg)
		}
	}
	return missing
}

type e111Trace struct {
	mu                                                sync.Mutex
	org, repo, block                                  string
	pauseHEAD                                         bool
	reached, release                                  chan struct{}
	once, releaseOnce                                 sync.Once
	refReads, pendingReads, pendingErrors, headErrors int
	writes                                            []string
}

func e111NewTrace(fx *w2CreateFileFixture) *e111Trace {
	return &e111Trace{org: fx.orgID, repo: fx.repoID, block: fx.blockID, reached: make(chan struct{}), release: make(chan struct{})}
}
func (o *e111Trace) resume() { o.releaseOnce.Do(func() { close(o.release) }) }
func (o *e111Trace) ObserveQuery(_ context.Context, q gocql.ObservedQuery) {
	statement := strings.ToLower(strings.Join(strings.Fields(q.Statement), " "))
	head := strings.HasPrefix(statement, "select head_commit_id from libraries ") && len(q.Values) == 2 && q.Values[0] == o.org && q.Values[1] == o.repo && q.Query.GetConsistency() == gocql.Serial
	refs := strings.HasPrefix(statement, "select referrer from block_references ") && len(q.Values) == 2 && q.Values[0] == o.org && q.Values[1] == o.block && q.Query.GetConsistency() == gocql.EachQuorum
	pending := strings.HasPrefix(statement, "select staged_block_ids from published_block_reference_repairs ") && len(q.Values) == 2 && q.Values[1] == o.org && q.Query.GetConsistency() == gocql.EachQuorum
	fs := strings.HasPrefix(statement, "insert into block_references ") && len(q.Values) >= 3 && q.Values[0] == o.org && q.Values[1] == o.block && strings.HasPrefix(q.Values[2].(string), "fs:")
	del := strings.HasPrefix(statement, "delete from published_block_reference_repairs ") && len(q.Values) == 5 && q.Values[1] == o.org && q.Values[2] == o.repo
	o.mu.Lock()
	if head && q.Err != nil {
		o.headErrors++
	}
	if pending {
		if q.Err != nil {
			o.pendingErrors++
		} else {
			o.pendingReads++
		}
	}
	if refs && q.Err == nil {
		o.refReads++
	}
	if fs && q.Err == nil {
		o.writes = append(o.writes, "fs:")
	}
	if del && q.Err == nil {
		o.writes = append(o.writes, "repair-delete")
	}
	pause := head && q.Err == nil && o.pauseHEAD
	o.mu.Unlock()
	if pause {
		o.once.Do(func() { close(o.reached); <-o.release })
	}
}

// This is a productive worker, with only discovery narrowed to the owned
// candidate. The mutation must reach an exact COMMITTED D before it is RED.
func e111GC(t *testing.T, fx *w2CreateFileFixture, database *dbpkg.DB, trace *e111Trace, failScan bool) {
	t.Helper()
	// Keep the observed/faulted native session, but declare the fixture's full
	// topology to the real destructive gate. The fixture already requires Load;
	// w2EvidenceSession's minimal config alone only declares its local DC.
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	database = dbpkg.NewPublicationEvidenceDB(database.Session(), cfg.Database)
	store := gcpkg.NewCassandraStore(database)
	candidate := w2Candidate(t, gcpkg.NewCassandraStore(fx.database), fx.orgUUID, fx.blockID, fx.target.StorageClass)
	scope := &w2ClosureOwnedQueue{GCStore: store, org: fx.orgUUID, block: fx.blockID, identity: candidate.Identity()}
	n, err := w2Worker(t, scope, fx.target.StorageClass).ProcessOrgOnce(t.Context(), fx.orgUUID)
	if n != 0 {
		var phase, class, key, claim string
		var claimedAt time.Time
		scanErr := fx.database.Session().Query(`SELECT phase,storage_class,storage_key,claim_id,claimed_at FROM gc_block_delete_lifecycles WHERE org_id = ? AND block_id = ?`, fx.orgID, fx.blockID).Consistency(gocql.EachQuorum).Scan(&phase, &class, &key, &claim, &claimedAt)
		authority := gcpkg.BlockDeleteAuthority{Target: gcpkg.BlockDeleteTarget{StorageClass: class, StorageKey: key}, ClaimID: claim, ClaimedAt: claimedAt}
		orphan, orphanFound, orphanErr := store.GetS3OrphanExact(fx.orgUUID, fx.blockID, authority)
		_, rootFound, rootErr := store.GetS3OrphanRecoveryRootExact(fx.orgUUID, fx.blockID, authority)
		if scanErr == nil && phase == gcpkg.BlockDeleteLifecyclePhasePublished && class == fx.target.StorageClass && key == fx.target.StorageKey && orphanErr == nil && orphanFound && orphan.RecoveryState == gcpkg.S3OrphanRecoveryStateCommitted && rootErr == nil && rootFound {
			// Finish only this owned COMMITTED root before reporting RED; pending
			// repair is deliberately not post-D revocation authority.
			w2AssertCommittedContinuation(t, gcpkg.NewCassandraStore(fx.database), fx.orgUUID, fx.blockID, fx.target.StorageClass, fx.target.StorageKey, newVerificationBlockStore(t, fx.orgID))
			t.Fatalf("E1-11 RED: pending repair allowed exact COMMITTED D(P1): n=%d err=%v P=(%s,%s)", n, err, class, key)
		}
		t.Fatalf("unexpected worker progress without exact COMMITTED certificate: n=%d err=%v phase=%s scan=%v", n, err, phase, scanErr)
	}
	if !scope.visited {
		t.Fatal("worker did not visit owned real candidate")
	}
	// ProcessOrgOnce handles retryable/fail-closed items internally; the concrete
	// failed query below establishes the outage rather than an aggregate count.
	if err != nil {
		t.Fatalf("productive worker: %v", err)
	}
	trace.mu.Lock()
	reads, pending, failed := trace.refReads, trace.pendingReads, trace.pendingErrors
	trace.mu.Unlock()
	if reads != 1 || (!failScan && (pending == 0 || failed != 0)) || (failScan && failed == 0) {
		t.Fatalf("destructive proof was not observed: refs=%d pending=%d errors=%d failScan=%t", reads, pending, failed, failScan)
	}
	if _, found, err := store.GetBlockGCCandidateExact(fx.orgUUID, fx.blockID, candidate.Identity()); err != nil || !found {
		t.Fatalf("candidate lost: %t %v", found, err)
	}
	e12AssertNoDeleteLifecycle(t, fx)
	if fx.readTarget(t) != fx.target {
		t.Fatal("worker changed exact P1")
	}
	w2AssertBytes(t, fx)
	t.Logf("E1-11 worker: first EQ refs=0; pending scans=%d errors=%d; claim released; candidate retained; no D/root; exact P1/K1 intact", pending, failed)
}

func TestCurrentRuntimeRepairLiveness(t *testing.T) {
	if endpoint := os.Getenv("SESAMEFS_E111_ISOLATED_URL"); endpoint != "" && os.Getenv("SESAMEFS_E111_CHILD") != "1" {
		e111RunIsolated(t, endpoint)
		return
	}
	requireCassandra(t)
	if os.Getenv("SESAMEFS_TEST_IN_CONTAINER") != "1" {
		t.Fatal("E1-11 requires Docker")
	}
	for _, endpoint := range []string{superadminClient.baseURL, envOrDefault("SESAMEFS_URL_2", "http://sesamefs-node-2:8080"), envOrDefault("SESAMEFS_URL_3", "http://sesamefs-node-3:8080")} {
		if err := e19CheckGCDisabled(newTestClient(endpoint, superadminClient.token)); err != nil {
			t.Fatalf("E1-11 owned-worker isolation: %v", err)
		}
	}
	for _, leg := range e111Legs {
		t.Run(leg, func(t *testing.T) {
			t.Cleanup(func() {
				if !t.Failed() && !t.Skipped() {
					e111Observed[leg] = true
				}
			})
			var fx *w2CreateFileFixture
			verificationDB := shareProjectionDBForTest(t)
			t.Cleanup(func() {
				if fx != nil {
					e111VerifyCleanup(t, verificationDB, fx)
				}
			})
			fx = w2RepairFixture(t)
			unknown := leg == "unknown-retained"
			stop := v2pkg.SetW2PublicationAfterHeadForTest
			if unknown {
				stop = v2pkg.SetW2PublicationAfterAuthorityForTest
			}
			func() {
				restore := stop(fx.repoID, func() { panic("w2-process-death") })
				defer restore()
				w2Crash(t, fx)
			}()
			head := borrowedFSReadHead(t, fx.database, fx.orgID, fx.repoID)
			rows := w2Repairs(t, fx)
			if len(rows) != 1 || len(rows[0].blocks) != 1 || rows[0].blocks[0] != fx.blockID {
				t.Fatalf("real writer repair missing: %+v", rows)
			}
			r := rows[0]
			var repairTTL *int
			if err := fx.database.Session().Query(`SELECT TTL(created_at) FROM published_block_reference_repairs WHERE bucket = ? AND org_id = ? AND repo_id = ? AND commit_id = ? AND fs_id = ?`, r.bucket, fx.orgID, fx.repoID, r.commitID, r.fsID).Consistency(gocql.EachQuorum).Scan(&repairTTL); err != nil || repairTTL != nil {
				t.Fatalf("repair must be non-expiring: TTL=%v err=%v", repairTTL, err)
			}
			if unknown {
				// HEAD stays at the interrupted attempt's parent, so the attempt
				// could still be fast-forwarded: its actual ancestry yields UNKNOWN,
				// without CQL. (A competitor moving HEAD would make it SUPERSEDED
				// and settle it since E1-15E.)
				fx.assertHeadUnchanged(t)
				if head == r.commitID || e115dParent(t, fx, r.commitID) != head || len(w2Repairs(t, fx)) != 1 {
					t.Fatal("UNKNOWN control needs HEAD = the attempt's parent and its repair")
				}
			} else {
				if head != r.commitID {
					t.Fatal("writer did not win HEAD")
				}
				w24AssertHeadReaches(t, fx, head, r.fsID)
			}
			original := fx.target
			trace := e111NewTrace(fx)
			trace.pauseHEAD = leg == "during-classify"
			endpoint := splitEnvOrDefault("CASSANDRA_HOSTS", "cassandra:9042")[0]
			var classifierProxy *w2WireProxy
			if leg == "classifier-wire-error" {
				classifierProxy = newW2WireProxy(t)
				endpoint = classifierProxy.listener.Addr().String()
			}
			visitor := w2EvidenceSession(t, endpoint, trace)
			if classifierProxy != nil {
				classifierProxy.mu.Lock()
				classifierProxy.dropQuery = "select head_commit_id from libraries"
				classifierProxy.armed = true
				classifierProxy.beforeServer = true
				classifierProxy.mu.Unlock()
			}
			type classification struct {
				outcome string
				err     error
			}
			classified := make(chan classification, 1)
			release := make(chan struct{})
			var releaseOnce sync.Once
			resume := func() { releaseOnce.Do(func() { close(release) }) }
			t.Cleanup(v2pkg.SetRepairAfterClassifyForIntegration(visitor, fx.repoID, func(outcome string, err error) { classified <- classification{outcome, err}; <-release }))
			result, done := make(chan error, 1), make(chan struct{})
			go func() {
				defer close(done)
				result <- v2pkg.RepairPublishedFSObjectBlockReferenceRepair(visitor, fx.orgID, fx.repoID, r.commitID, r.fsID, r.blocks)
			}()
			t.Cleanup(func() { trace.resume(); resume(); e13Wait(t, done, "repair teardown") })
			if leg == "during-classify" {
				e13Wait(t, trace.reached, "real classifier SERIAL HEAD response")
			}
			var outcome classification
			if leg != "during-classify" {
				select {
				case outcome = <-classified:
				case <-time.After(20 * time.Second):
					t.Fatal("classifier did not finish")
				}
			}
			if leg != "normal" {
				refs, err := fx.database.ListBlockReferrers(fx.orgID, fx.blockID)
				if err != nil || len(refs) == 0 {
					t.Fatalf("original TTL refs missing: %v %v", refs, err)
				}
				for _, ref := range refs {
					if !strings.HasPrefix(ref, "up:") && !strings.HasPrefix(ref, "pub:") {
						t.Fatalf("unexpected saving ref %s", ref)
					}
				}
				e12ExpireRealTemporaryTTL(t, fx, refs)
				if rows := w2Repairs(t, fx); len(rows) != 1 || rows[0].commitID != r.commitID || rows[0].fsID != r.fsID {
					t.Fatalf("repair changed at zero-ref boundary: %+v", rows)
				}
				workerTrace := e111NewTrace(fx)
				workerEndpoint := splitEnvOrDefault("CASSANDRA_HOSTS", "cassandra:9042")[0]
				var guardProxy *w2WireProxy
				if leg == "guard-wire-error" {
					guardProxy = newW2WireProxy(t)
					workerEndpoint = guardProxy.listener.Addr().String()
				}
				workerDB := w2EvidenceSession(t, workerEndpoint, workerTrace)
				if guardProxy != nil {
					guardProxy.mu.Lock()
					guardProxy.dropQuery = "select staged_block_ids from published_block_reference_repairs"
					guardProxy.armed = true
					guardProxy.beforeServer = true
					guardProxy.mu.Unlock()
				}
				e111GC(t, fx, workerDB, workerTrace, guardProxy != nil)
				if guardProxy != nil {
					guardProxy.restore()
				}
			}
			trace.resume()
			if leg == "during-classify" {
				select {
				case outcome = <-classified:
				case <-time.After(20 * time.Second):
					t.Fatal("classifier did not finish after resuming HEAD read")
				}
			}
			want := "reachable"
			if unknown || classifierProxy != nil {
				want = "unknown"
			}
			if outcome.outcome != want || (outcome.err != nil) != (classifierProxy != nil) {
				t.Fatalf("real classifier=%s err=%v want=%s", outcome.outcome, outcome.err, want)
			}
			if classifierProxy != nil {
				if !errors.Is(outcome.err, gocql.ErrTimeoutNoResponse) && !errors.Is(outcome.err, gocql.ErrConnectionClosed) {
					t.Fatalf("expected native driver loss: %T %v", outcome.err, outcome.err)
				}
				trace.mu.Lock()
				failures := trace.headErrors
				trace.mu.Unlock()
				if failures == 0 {
					t.Fatal("no actual classifier query failed")
				}
				classifierProxy.restore() // let the real visitor renew after classification
			}
			resume()
			e13Wait(t, done, "real repair visit")
			visitErr := <-result
			if unknown || classifierProxy != nil {
				wantVisit := "retained"
				if classifierProxy != nil {
					wantVisit = "failed"
				}
				if v2pkg.PublishedBlockReferenceRepairVisitOutcomeForIntegration(visitErr) != wantVisit {
					t.Fatalf("visit=%v want=%s", visitErr, wantVisit)
				}
				rows := w2Repairs(t, fx)
				if len(rows) != 1 || rows[0].commitID != r.commitID || rows[0].fsID != r.fsID {
					t.Fatalf("unresolved repair lost: %+v", rows)
				}
				refs, err := fx.database.ListBlockReferrers(fx.orgID, fx.blockID)
				pub := v2pkg.PublishedBlockReferenceRepairLivenessReferrerForIntegration(fx.repoID, r.commitID, r.fsID)
				if err != nil || len(refs) != 1 || refs[0] != pub {
					t.Fatalf("renewal must leave only exact repair-owned pub: refs=%v err=%v", refs, err)
				}
			} else {
				if visitErr != nil {
					t.Fatal(visitErr)
				}
				refs, err := fx.database.ListBlockReferrers(fx.orgID, fx.blockID)
				expected := dbpkg.BlockReferrerForFSObject(fx.repoID, r.fsID)
				found := false
				for _, ref := range refs {
					found = found || ref == expected
					if ref != expected && !(leg == "normal" && strings.HasPrefix(ref, "up:")) {
						t.Fatalf("unexpected settlement ref %s", ref)
					}
				}
				if err != nil || !found || len(w2Repairs(t, fx)) != 0 {
					t.Fatalf("settlement refs=%v err=%v", refs, err)
				}
				trace.mu.Lock()
				order := strings.Join(trace.writes, ",")
				trace.mu.Unlock()
				if order != "fs:,repair-delete" {
					t.Fatalf("settlement write order %s", order)
				}
			}
			// Failed classification can now
			// recover against the same real reachable HEAD; clean UNKNOWN stays pending.
			// Use an independent session so the barrier cannot be re-entered.
			retryTrace := e111NewTrace(fx)
			recovery := w2EvidenceSession(t, splitEnvOrDefault("CASSANDRA_HOSTS", "cassandra:9042")[0], retryTrace)
			if err := v2pkg.RepairPublishedFSObjectBlockReferenceRepair(recovery, fx.orgID, fx.repoID, r.commitID, r.fsID, r.blocks); !unknown && err != nil {
				t.Fatalf("retry/recovery: %v", err)
			}
			if unknown && (len(w2Repairs(t, fx)) != 1 || fx.hasOwnFSReferrer(t)) {
				t.Fatal("UNKNOWN retry lost guard or installed fs:")
			}
			if !unknown {
				retryTrace.mu.Lock()
				before := len(retryTrace.writes)
				retryTrace.mu.Unlock()
				if (classifierProxy == nil && before != 0) || (classifierProxy != nil && before != 2) {
					t.Fatalf("retry/recovery writes=%d classifierError=%t", before, classifierProxy != nil)
				}
				if err := v2pkg.RepairPublishedFSObjectBlockReferenceRepair(recovery, fx.orgID, fx.repoID, r.commitID, r.fsID, r.blocks); err != nil {
					t.Fatalf("settled retry: %v", err)
				}
				retryTrace.mu.Lock()
				after := len(retryTrace.writes)
				retryTrace.mu.Unlock()
				if after != before {
					t.Fatal("settled retry performed fs:/repair writes")
				}
			}
			if !unknown && (len(w2Repairs(t, fx)) != 0 || !fx.hasOwnFSReferrer(t)) {
				t.Fatal("reachable retry failed settlement")
			}
			if borrowedFSReadHead(t, fx.database, fx.orgID, fx.repoID) != head || fx.readTarget(t) != original {
				t.Fatal("repair changed HEAD or exact P1")
			}
			if !unknown {
				w24AssertHeadReaches(t, fx, head, r.fsID)
			}
			e12AssertNoDeleteLifecycle(t, fx)
			w2AssertBytes(t, fx)
			t.Logf("E1-11 %s: classifier=%s err=%v visit=%s HEAD=%s fs=%s P1=(%s,%s); TTLExpired=%t, productive guard and settlement/retry verified", leg, outcome.outcome, outcome.err, v2pkg.PublishedBlockReferenceRepairVisitOutcomeForIntegration(visitErr), head, r.fsID, original.StorageClass, original.StorageKey, leg != "normal")
		})
	}
}

func e111RunIsolated(t *testing.T, endpoint string) {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	run := "^TestCurrentRuntimeRepairLiveness$"
	if _, sub, ok := strings.Cut(flag.Lookup("test.run").Value.String(), "/"); ok {
		run += "/" + sub
	}
	cmd := exec.CommandContext(ctx, binary, "-test.run="+run, "-test.v", "-test.count=1", "-test.timeout=2m")
	for _, entry := range os.Environ() {
		name := strings.SplitN(entry, "=", 2)[0]
		if strings.HasPrefix(name, "SESAMEFS_REQUIRE_") || strings.HasSuffix(name, "_CHILD") || name == "SESAMEFS_URL" || name == "SESAMEFS_URL_2" || name == "SESAMEFS_URL_3" || name == "CASSANDRA_KEYSPACE" {
			continue
		}
		cmd.Env = append(cmd.Env, entry)
	}
	cmd.Env = append(cmd.Env, e111EvidenceEnv+"=1", "SESAMEFS_E111_CHILD=1", "CASSANDRA_KEYSPACE=sesamefs_e19", "SESAMEFS_URL="+endpoint, "SESAMEFS_URL_2="+endpoint, "SESAMEFS_URL_3="+endpoint)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("isolated E1-11 failed: %v", err)
	}
	for _, leg := range e111Legs {
		e111Observed[leg] = true
	}
}

func TestCurrentRuntimeRepairLivenessCompleteness(t *testing.T) {
	if len(e111Missing(nil)) != 6 {
		t.Fatal("must require six legs")
	}
	all := map[string]bool{}
	for _, leg := range e111Legs {
		all[leg] = true
	}
	if len(e111Missing(all)) != 0 {
		t.Fatal("complete evidence rejected")
	}
	for _, leg := range e111Legs {
		delete(all, leg)
		if missing := e111Missing(all); len(missing) != 1 || missing[0] != leg {
			t.Fatalf("omission %s hidden by another leg: %v", leg, missing)
		}
		all[leg] = true
	}
}

// Registered before fixture cleanup: runs after its metadata/object teardown,
// including a failed mutation. Isolated orgs do not consume shared user quota.
func e111VerifyCleanup(t *testing.T, database *dbpkg.DB, fx *w2CreateFileFixture) {
	t.Helper()
	clean := true
	for _, table := range []string{"blocks", "block_references", "gc_block_delete_lifecycles", "gc_s3_orphans"} {
		var count int
		if err := database.Session().Query("SELECT count(*) FROM "+table+" WHERE org_id = ? AND block_id = ?", fx.orgID, fx.blockID).Consistency(gocql.EachQuorum).Scan(&count); err != nil || count != 0 {
			clean = false
			t.Errorf("E1-11 teardown %s: rows=%d err=%v", table, count, err)
		}
	}
	for _, table := range []string{"commits", "fs_objects", "libraries_by_id"} {
		var count int
		if err := database.Session().Query("SELECT count(*) FROM "+table+" WHERE library_id = ?", fx.repoID).Consistency(gocql.EachQuorum).Scan(&count); err != nil || count != 0 {
			clean = false
			t.Errorf("E1-11 teardown %s: rows=%d err=%v", table, count, err)
		}
	}
	if rows := w2Repairs(t, fx); len(rows) != 0 {
		clean = false
		t.Errorf("E1-11 teardown repairs: %+v", rows)
	}
	if fx.target.StorageKey != "" {
		if exists, err := newVerificationBlockStore(t, fx.orgID).ObjectExists(context.Background(), fx.target.StorageKey); err != nil || exists {
			clean = false
			t.Errorf("E1-11 teardown K1 exists=%t err=%v", exists, err)
		}
	}
	if clean {
		t.Logf("E1-11 teardown verified org=%s repo=%s P1=%s: owned metadata/repairs/K1 absent", fx.orgID, fx.repoID, fx.target.StorageKey)
	}
}
