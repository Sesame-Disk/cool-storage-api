//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
	gcpkg "github.com/Sesame-Disk/sesamefs/internal/gc"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/google/uuid"
)

const e101EvidenceEnv = "SESAMEFS_REQUIRE_E101_G2_CRASH_EVIDENCE"

var e101Observed = map[string]bool{}

func e101Missing() []string {
	return e101MissingLegs(e101Observed, []string{"prepared", "committed"})
}
func e101MissingLegs(observed map[string]bool, cuts []string) []string {
	var missing []string
	for _, cut := range cuts {
		for _, action := range []string{"kill", "resume"} {
			if leg := cut + "/" + action; !observed[leg] {
				missing = append(missing, leg)
			}
		}
	}
	return missing
}

type e101ProcessData struct {
	Org                                                          uuid.UUID
	Block, Class, Marker, Cut, Mode, Expect, Repo, Session, SHA1 string
	Candidate                                                    gcpkg.BlockGCCandidateInfo
	Authority                                                    gcpkg.BlockDeleteAuthority
	FirstSeenAt                                                  time.Time
}

// Pause only delegated successful current-protocol calls; no productive hook or fabricated outcome.
type e101PausedStore struct {
	*w2ClosureOwnedQueue
	t    *testing.T
	data e101ProcessData
}

func (s *e101PausedStore) hold(a gcpkg.BlockDeleteAuthority) {
	if a.Target != s.data.Candidate.Target || !s.visited || len(s.liveness) != 1 || s.liveness[0] != dbpkg.BlockPublicationZero || s.errs[0] != nil {
		s.t.Fatal("boundary lacked the owned physical identity and real zero-liveness proof")
	}
	e101Pause(s.t, s.data, a)
}
func e101Pause(t *testing.T, data e101ProcessData, a gcpkg.BlockDeleteAuthority) {
	// Cassandra timestamps persist millisecond precision, as normalizeBlockDeleteAuthority does.
	a.ClaimedAt = a.ClaimedAt.UTC().Truncate(time.Millisecond)
	encoded, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(data.Marker+".tmp", encoded, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(data.Marker+".tmp", data.Marker); err != nil {
		t.Fatal(err)
	}
	e112AwaitFile(t, data.Marker+".resume")
}
func (s *e101PausedStore) PrepareBlockDeleteOrphan(org uuid.UUID, block string, a gcpkg.BlockDeleteAuthority, sha1 string, now time.Time) gcpkg.StartBlockDeleteOrphanResult {
	r := s.GCStore.PrepareBlockDeleteOrphan(org, block, a, sha1, now)
	if org == s.org && block == s.block && s.data.Cut == "prepared" && (r.Outcome == gcpkg.StartBlockDeleteOrphanCreated || r.Outcome == gcpkg.StartBlockDeleteOrphanSameAuthority) {
		s.hold(a)
	}
	return r
}
func (s *e101PausedStore) CommitBlockDeleteOrphanHandoff(org uuid.UUID, block string, a gcpkg.BlockDeleteAuthority) (gcpkg.BlockDeleteHandoffResult, error) {
	r, err := s.GCStore.CommitBlockDeleteOrphanHandoff(org, block, a)
	if org == s.org && block == s.block && s.data.Cut == "committed" && err == nil && (r.Outcome == gcpkg.BlockDeleteHandoffCommitted || r.Outcome == gcpkg.BlockDeleteHandoffAlreadyCommitted) {
		s.hold(a)
	}
	return r, err
}

func TestE101GCProcessChild(t *testing.T) {
	raw := os.Getenv("SESAMEFS_E101_PROCESS_DATA")
	if raw == "" {
		t.Skip("parent-only subprocess helper")
	}
	var data e101ProcessData
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		t.Fatal(err)
	}
	t.Logf("fresh GC process pid=%d mode=%s", os.Getpid(), data.Mode)
	database := shareProjectionDBForTest(t)
	store := gcpkg.NewCassandraStore(database)
	if data.Mode == "prepare-physical" || data.Mode == "physical" {
		e101cChild(t, database, data)
		return
	}
	if data.Mode == "verify_failure" {
		e101VerifyFailure(t, database, data)
		return
	}
	if data.Mode == "settle" {
		e101bReplay(t, database, data)
		return
	}
	if data.Mode == "worker" {
		scope := &w2ClosureOwnedQueue{GCStore: store, org: data.Org, block: data.Block, identity: data.Candidate.Identity()}
		base := &e101PausedStore{w2ClosureOwnedQueue: scope, t: t, data: data}
		var paused gcpkg.GCStore = base
		if e101bCut(data.Cut) {
			paused = &e101bPausedStore{e101PausedStore: base}
		}
		if n, err := w2Worker(t, paused, data.Class).ProcessOrgOnce(t.Context(), data.Org); err != nil || n != 1 || !scope.visited {
			t.Fatalf("worker n=%d visited=%v err=%v", n, scope.visited, err)
		}
		return
	}
	if data.Mode != "recover" {
		t.Fatalf("unknown process mode %q", data.Mode)
	}
	// Only enumeration is filtered. Every authority/abort/promotion/retirement/storage mutation is real.
	worker := w2Worker(t, &w2OwnedRecoveryStore{GCStore: store, org: data.Org, block: data.Block}, data.Class)
	for tick := 0; tick < 4; tick++ {
		if _, err := worker.RecoverS3Orphans(t.Context(), 1000); err != nil {
			t.Fatal(err)
		}
		_, orphan, err := store.GetS3OrphanExact(data.Org, data.Block, data.Authority)
		if err != nil {
			t.Fatal(err)
		}
		_, root, err := store.GetS3OrphanRecoveryRootExact(data.Org, data.Block, data.Authority)
		if err != nil {
			t.Fatal(err)
		}
		if !orphan && !root {
			t.Log("E101_RECOVERY_COMPLETED")
			return
		}
	}
	t.Fatal("owned root/orphan did not settle after four healthy recovery ticks")
}

func e101Command(t *testing.T, data e101ProcessData) (*exec.Cmd, context.CancelFunc) {
	t.Helper()
	encoded, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestE101GCProcessChild$", "-test.v", "-test.count=1", "-test.timeout=80s")
	for _, entry := range os.Environ() {
		name := strings.SplitN(entry, "=", 2)[0]
		if strings.HasPrefix(name, "SESAMEFS_REQUIRE_") || name == "SESAMEFS_W2_PROCESS_CHILD" || name == "SESAMEFS_E101_PROCESS_DATA" {
			continue
		}
		cmd.Env = append(cmd.Env, entry)
	}
	cmd.Env = append(cmd.Env, "SESAMEFS_W2_PROCESS_CHILD=1", "SESAMEFS_E101_PROCESS_DATA="+string(encoded))
	return cmd, cancel
}
func e101Recover(t *testing.T, data e101ProcessData) {
	t.Helper()
	data.Mode = "recover"
	cmd, cancel := e101Command(t, data)
	defer cancel()
	out, err := cmd.CombinedOutput()
	t.Logf("independent recovery: %s", out)
	if err != nil || !bytes.Contains(out, []byte("E101_RECOVERY_COMPLETED")) {
		t.Fatalf("recovery completion not proved: %v", err)
	}
}

func TestE101G2ProcessCrash(t *testing.T) {
	e101Observed = map[string]bool{}
	if endpoint := os.Getenv("SESAMEFS_E19_ISOLATED_URL"); endpoint != "" && os.Getenv("SESAMEFS_E101_CHILD") != "1" {
		e101Isolated(t, endpoint)
		return
	}
	if runtime.GOOS != "linux" || os.Getenv("SESAMEFS_TEST_IN_CONTAINER") != "1" {
		t.Fatal("E1-01A requires Linux Docker")
	}
	e19RequireNoBackgroundGC(t)
	e101RunMatrix(t, []string{"prepared", "committed"}, e101Observed, e101AssertBoundary, nil)
}

func e101RunMatrix(t *testing.T, cuts []string, observed map[string]bool, boundary func(*testing.T, *w2CreateFileFixture, e101ProcessData), beforeRecovery func(*testing.T, *w2CreateFileFixture, e101ProcessData, e101Snapshot)) {
	if runtime.GOOS != "linux" || os.Getenv("SESAMEFS_TEST_IN_CONTAINER") != "1" {
		t.Fatal("E1-01 process evidence requires Linux Docker")
	}
	database := shareProjectionDBForTest(t)
	for _, cut := range cuts {
		t.Run(cut, func(t *testing.T) {
			for _, action := range []string{"kill", "resume"} {
				t.Run(action, func(t *testing.T) {
					// Account for evidence only after parent-owned fixture teardown has succeeded.
					t.Cleanup(func() {
						if !t.Failed() && !t.Skipped() {
							observed[cut+"/"+action] = true
						}
					})
					class := x1StorageClass(t)
					web := newSessionUploadHeadFixture(t, database, newBorrowedFSHeadHandler(t, database, class), true)
					fx := &w2CreateFileFixture{w2UploadFileFixture: &w2UploadFileFixture{borrowedFSHeadFixture: web.borrowedFSHeadFixture}}
					web.dropOwnSessionRef(t)
					store := gcpkg.NewCassandraStore(database)
					candidate := w2Candidate(t, store, fx.orgUUID, fx.blockID, class)
					data := e101ProcessData{Org: fx.orgUUID, Block: fx.blockID, Class: class, Marker: filepath.Join(t.TempDir(), "cut"), Cut: cut, Mode: "worker", Candidate: candidate, Repo: fx.repoID, Session: fx.sessionID, SHA1: fx.sha1ID}
					var before *e101Snapshot
					t.Cleanup(func() {
						if t.Failed() || t.Skipped() {
							e101PreserveFailure(t, database, data, before)
						}
					})
					// A failed leg must not retry recovery or allow inherited destructive cleanup.
					t.Cleanup(func() {
						if t.Failed() || t.Skipped() {
							return
						}
						if data.Authority.IsZero() {
							t.Errorf("no exact D captured; preserving GC fixture authority")
							return
						}
						e101Recover(t, data)
						cleanupGCBlockFixturesForTest(t, fx.orgUUID, fx.blockID)
						e115aVerifyNoRoot(t, database, fx)
					})
					if e101RecoveryCut(cut) {
						w2AssertBytes(t, fx)
						data = e101cPrepare(t, data)
					}
					cmd, cancel := e101Command(t, data)
					defer cancel()
					var output bytes.Buffer
					cmd.Stdout, cmd.Stderr = &output, &output
					if err := cmd.Start(); err != nil {
						t.Fatal(err)
					}
					exited := make(chan error, 1)
					go func() { exited <- cmd.Wait() }()
					finished := false
					t.Cleanup(func() {
						if !finished {
							_ = cmd.Process.Kill()
							<-exited
						}
						if t.Failed() {
							t.Logf("worker output: %s", output.String())
						}
					})
					e112AwaitFile(t, data.Marker)
					encoded, err := os.ReadFile(data.Marker)
					if err != nil || json.Unmarshal(encoded, &data.Authority) != nil {
						t.Fatalf("read exact boundary authority: %v", err)
					}
					boundary(t, fx, data)
					snapshot := e101Capture(database, data)
					before = &snapshot
					if len(snapshot.Errors) != 0 {
						t.Fatalf("boundary snapshot unavailable: %v", snapshot.Errors)
					}
					if action == "kill" {
						if err := cmd.Process.Kill(); err != nil {
							t.Fatal(err)
						}
						err := <-exited
						finished = true
						var killed *exec.ExitError
						if !errors.As(err, &killed) || !w2WasSIGKILL(killed) {
							t.Fatalf("worker did not die by SIGKILL: %v", err)
						}
						t.Logf("verified worker OS SIGKILL: pid=%d cut=%s P=%+v D=%+v", cmd.Process.Pid, cut, data.Authority.Target, data.Authority)
					} else {
						if err := os.WriteFile(data.Marker+".resume", []byte("resume"), 0600); err != nil {
							t.Fatal(err)
						}
						err := <-exited
						finished = true
						if err != nil || (e101RecoveryCut(cut) && !bytes.Contains(output.Bytes(), []byte("E101C_PHYSICAL_COMPLETED"))) {
							t.Fatalf("no-kill worker: %v\n%s", err, output.String())
						}
					}
					if os.Getenv("SESAMEFS_E101_FAIL_AFTER_KILL") == "1" {
						t.Fatal("E101_INJECTED_FAILURE: harness preservation control")
					}
					data.Expect = "terminal"
					if cut == "prepared" && action == "kill" {
						data.Expect = "aborted"
					}
					if beforeRecovery != nil {
						beforeRecovery(t, fx, data, *before)
					}
					e101Recover(t, data)
					e101AssertRecovered(t, fx, data)
					e101Recover(t, data) // Another OS process/session must replay the already settled identity safely.
					e101AssertRecovered(t, fx, data)
				})
			}
		})
	}
}

func e101AssertBoundary(t *testing.T, fx *w2CreateFileFixture, data e101ProcessData) {
	t.Helper()
	a := data.Authority
	if a.IsZero() || a.Target != fx.target {
		t.Fatalf("wrong boundary identity: %+v", a)
	}
	var claim, state, class, key string
	var at time.Time
	var committed *bool
	if err := fx.database.Session().Query(`SELECT gc_claim_id, gc_claimed_at, gc_state, storage_class, storage_key, gc_orphan_handoff FROM blocks WHERE org_id=? AND block_id=?`, fx.orgID, fx.blockID).Consistency(gocql.Serial).Scan(&claim, &at, &state, &class, &key, &committed); err != nil || claim != a.ClaimID || !at.Equal(a.ClaimedAt) || state != dbpkg.BlockGCStateDeleting || class != a.Target.StorageClass || key != a.Target.StorageKey || (data.Cut == "prepared" && committed != nil) || (data.Cut == "committed" && (committed == nil || !*committed)) {
		t.Fatalf("durable blocks cut=%s claim=%s at=%v state=%s P=(%s,%s) committed=%v err=%v", data.Cut, claim, at, state, class, key, committed, err)
	}
	store := gcpkg.NewCassandraStore(fx.database)
	row, found, err := store.GetS3OrphanExact(fx.orgUUID, fx.blockID, a)
	if err != nil || !found || row.RecoveryState != gcpkg.S3OrphanRecoveryStatePrepared || row.Authority != a {
		t.Fatalf("exact PREPARED: %+v found=%v err=%v", row, found, err)
	}
	root, found, err := store.GetS3OrphanRecoveryRootExact(fx.orgUUID, fx.blockID, a)
	if err != nil || !found || root.Authority != a || !root.FirstSeenAt.Equal(row.FirstSeenAt) {
		t.Fatalf("exact root: %+v found=%v err=%v", root, found, err)
	}
	if observation := store.ObserveBlockDeleteLifecycle(fx.orgUUID, fx.blockID, gcpkg.CommittedBlockDeleteAuthorityForTest(a)); observation.Outcome != gcpkg.StartBlockDeleteOrphanNotPublished {
		t.Fatalf("promotion occurred before held boundary: %+v", observation)
	}
	e17AssertRefs(t, fx)
	fx.assertHeadUnchanged(t)
	w2AssertBytes(t, fx)
	t.Logf("durable pre-kill cut=%s org=%s L=%s P=%+v D=%+v root=%+v", data.Cut, fx.orgID, fx.blockID, a.Target, a, root)
}
func e101AssertRecovered(t *testing.T, fx *w2CreateFileFixture, data e101ProcessData) {
	t.Helper()
	store := gcpkg.NewCassandraStore(fx.database)
	_, orphan, err := store.GetS3OrphanExact(fx.orgUUID, fx.blockID, data.Authority)
	if err != nil || orphan {
		t.Fatalf("orphan remains: %v %v", orphan, err)
	}
	_, root, err := store.GetS3OrphanRecoveryRootExact(fx.orgUUID, fx.blockID, data.Authority)
	if err != nil || root {
		t.Fatalf("root remains: %v %v", root, err)
	}
	observation := store.ObserveBlockDeleteLifecycle(fx.orgUUID, fx.blockID, gcpkg.CommittedBlockDeleteAuthorityForTest(data.Authority))
	if data.Expect == "aborted" {
		if observation.Outcome != gcpkg.StartBlockDeleteOrphanNotPublished {
			t.Fatalf("uncommitted D was published: %+v", observation)
		}
		if outcome, err := fx.database.ValidateBlockRepairAuthority(fx.orgID, fx.blockID, dbpkg.BlockPhysicalLocation{StorageClass: fx.target.StorageClass, StorageKey: fx.target.StorageKey}); err != nil || outcome != dbpkg.BlockRepairAuthorityAuthorized {
			t.Fatalf("aborted D still fences P1: %v %v", outcome, err)
		}
		w2AssertBytes(t, fx)
	} else {
		if observation.Outcome != gcpkg.StartBlockDeleteOrphanLifecycleAdvanced {
			t.Fatalf("exact D not terminal: %+v", observation)
		}
		x1AssertCanonicalAbsent(t, store, fx.orgUUID, fx.blockID)
		exists, err := newVerificationBlockStore(t, fx.orgID).ObjectExists(t.Context(), fx.target.StorageKey)
		if err != nil || exists {
			t.Fatalf("K1 remains: %v %v", exists, err)
		}
	}
	e17AssertRefs(t, fx)
	fx.assertHeadUnchanged(t)
	if rows := w2Repairs(t, fx); len(rows) != 0 {
		t.Fatalf("GC invented publication repair: %+v", rows)
	}
	if e101bCut(data.Cut) || e101RecoveryCut(data.Cut) {
		e101bBookkeeping(t, fx.database, data, false)
	}
	if e101dCut(data.Cut) || e101eCut(data.Cut) {
		e101dAssertSettled(t, fx.database, data)
	}
	t.Logf("independently observed %s P=%+v D=%+v", data.Expect, data.Authority.Target, data.Authority)
}

func e101Isolated(t *testing.T, endpoint string) {
	e101IsolatedMatrix(t, endpoint, "TestE101G2ProcessCrash", e101EvidenceEnv, "SESAMEFS_E101_CHILD", e101Observed, []string{"prepared", "committed"})
}
func e101IsolatedMatrix(t *testing.T, endpoint, testName, gate, child string, observed map[string]bool, cuts []string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	run := "^" + testName + "$"
	if _, sub, ok := strings.Cut(flag.Lookup("test.run").Value.String(), "/"); ok {
		run += "/" + sub
	}
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run="+run, "-test.v", "-test.count=1", "-test.timeout=5m")
	for _, entry := range os.Environ() {
		name := strings.SplitN(entry, "=", 2)[0]
		if strings.HasPrefix(name, "SESAMEFS_REQUIRE_") || name == "SESAMEFS_URL" || name == "SESAMEFS_URL_2" || name == "SESAMEFS_URL_3" || name == "CASSANDRA_KEYSPACE" || strings.HasSuffix(name, "_CHILD") {
			continue
		}
		cmd.Env = append(cmd.Env, entry)
	}
	cmd.Env = append(cmd.Env, gate+"=1", child+"=1", "CASSANDRA_KEYSPACE=sesamefs_e19", "SESAMEFS_URL="+endpoint, "SESAMEFS_URL_2="+endpoint, "SESAMEFS_URL_3="+endpoint)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("isolated %s evidence: %v", testName, err)
	}
	for _, leg := range e101MissingLegs(observed, cuts) {
		observed[leg] = true
	}
}

// Snapshot reads never fail/skip the test halfway: UNKNOWN is recorded and the fixture is retained.
type e101Snapshot struct {
	Rows                            map[string][]map[string]interface{}
	Root                            gcpkg.S3OrphanRecoveryRootInfo
	RootFound, K1Exists, QueueFound bool
	K1Bytes                         []byte
	Errors                          map[string]string
}
type e101Failure struct {
	Data           e101ProcessData
	Keyspace, Test string
	CapturedAt     time.Time
	Before         *e101Snapshot
	After          e101Snapshot
}

func e101Capture(database *dbpkg.DB, data e101ProcessData) e101Snapshot {
	s := e101Snapshot{Rows: map[string][]map[string]interface{}{}, Errors: map[string]string{}}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	read := func(name, query string, cl gocql.Consistency, args ...interface{}) {
		iter := database.Session().Query(query, args...).Consistency(cl).WithContext(ctx).Iter()
		s.Rows[name] = nil
		row := map[string]interface{}{}
		for iter.MapScan(row) {
			s.Rows[name] = append(s.Rows[name], row)
			row = map[string]interface{}{}
		}
		if err := iter.Close(); err != nil {
			s.Errors[name] = err.Error()
		}
	}
	for _, table := range []string{"blocks", "gc_s3_orphans", "gc_block_delete_lifecycles", "gc_block_candidates", "block_references"} {
		cl := gocql.EachQuorum
		if table == "blocks" || table == "gc_block_delete_lifecycles" {
			cl = gocql.Serial
		}
		read(table, "SELECT * FROM "+table+" WHERE org_id=? AND block_id=?", cl, data.Org.String(), data.Block)
	}
	if e101dCut(data.Cut) || e101eCut(data.Cut) {
		if data.FirstSeenAt.IsZero() {
			s.Errors["discovery"] = "missing exact first_seen_at"
		} else {
			read("gc_s3_orphans_by_day", `SELECT * FROM gc_s3_orphans_by_day WHERE first_seen_day=? AND bucket=? AND first_seen_at=? AND org_id=? AND block_id=? AND storage_class=? AND storage_key=? AND gc_claim_id=? AND gc_claimed_at=?`, gocql.EachQuorum, dbpkg.GCProjectionUTCDate(data.FirstSeenAt), dbpkg.GCDiscoveryBucket(data.Org.String(), data.Block), data.FirstSeenAt, data.Org.String(), data.Block, data.Authority.Target.StorageClass, data.Authority.Target.StorageKey, data.Authority.ClaimID, data.Authority.ClaimedAt)
		}
	}
	read("head", "SELECT head_commit_id FROM libraries WHERE org_id=? AND library_id=?", gocql.Serial, data.Org.String(), data.Repo)
	for b := 0; b < dbpkg.PublishedBlockReferenceRepairBuckets; b++ {
		read(fmt.Sprintf("repairs/%d", b), "SELECT * FROM published_block_reference_repairs WHERE bucket=? AND org_id=? AND repo_id=?", gocql.EachQuorum, b, data.Org.String(), data.Repo)
	}
	var err error
	store := gcpkg.NewCassandraStore(database)
	s.QueueFound, err = store.QueueItemExists(data.Org, data.Candidate.CandidateAt, gcpkg.ItemBlock, data.Block, data.Candidate.ItemIdentity())
	if err != nil {
		s.Errors["queue"] = err.Error()
	}
	s.Root, s.RootFound, err = store.GetS3OrphanRecoveryRootExact(data.Org, data.Block, data.Authority)
	if err != nil {
		s.Errors["root"] = err.Error()
	}
	s3, err := storage.NewS3Store(ctx, defaultClassS3Config())
	if err == nil {
		var bs *storage.BlockStore
		bs, err = storage.NewOrgBlockStore(s3, "blocks/", data.Org.String())
		if err == nil {
			s.K1Exists, err = bs.ObjectExists(ctx, data.Candidate.Target.StorageKey)
			if err == nil && s.K1Exists {
				s.K1Bytes, err = bs.GetBlockByStorageKey(ctx, data.Candidate.Target.StorageKey)
			}
		}
	}
	if err != nil {
		s.Errors["K1"] = err.Error()
	}
	return s
}
func e101PreserveFailure(t *testing.T, database *dbpkg.DB, data e101ProcessData, before *e101Snapshot) {
	t.Helper()
	failure := e101Failure{Data: data, Keyspace: os.Getenv("CASSANDRA_KEYSPACE"), Test: t.Name(), CapturedAt: time.Now().UTC(), Before: before, After: e101Capture(database, data)}
	encoded, err := json.Marshal(failure)
	if err != nil {
		t.Errorf("encode retained failure: %v", err)
		return
	}
	// The full JSON in the test log survives --rm and all fixture/TempDir cleanups.
	t.Logf("E101_FAILURE_SNAPSHOT %s", encoded)
	dir := envOrDefault("SESAMEFS_E101_EVIDENCE_DIR", "tmp/e101-failures")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Errorf("create evidence directory: %v", err)
		return
	}
	path := filepath.Join(dir, uuid.NewString()+".json")
	if err := os.WriteFile(path+".tmp", encoded, 0600); err != nil {
		t.Errorf("write retained failure: %v", err)
		return
	}
	if err := os.Rename(path+".tmp", path); err != nil {
		t.Errorf("publish retained failure: %v", err)
		return
	}
	t.Logf("retained failure artifact=%s; automatic recovery/teardown disabled", path)
}

func TestE101FailureSnapshotSurvivesTeardown(t *testing.T) {
	e101FailureControl(t, "TestE101G2ProcessCrash", "committed", e101EvidenceEnv, 1, 0)
}
func e101FailureControl(t *testing.T, testName, cut, gate string, blocks, lifecycles int) {
	endpoint := os.Getenv("SESAMEFS_E19_ISOLATED_URL")
	if runtime.GOOS != "linux" || os.Getenv("SESAMEFS_TEST_IN_CONTAINER") != "1" || endpoint == "" {
		t.Fatal("failure preservation control requires the isolated Docker fleet")
	}
	dir := t.TempDir() // Owned by this verifier, outside the deliberately failed child's TempDir.
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^"+testName+"$/"+cut+"/kill", "-test.v", "-test.count=1", "-test.timeout=5m")
	for _, entry := range os.Environ() {
		name := strings.SplitN(entry, "=", 2)[0]
		if strings.HasPrefix(name, "SESAMEFS_REQUIRE_") || name == "SESAMEFS_E101_FAIL_AFTER_KILL" || name == "SESAMEFS_E101_EVIDENCE_DIR" || strings.HasSuffix(name, "_CHILD") {
			continue
		}
		cmd.Env = append(cmd.Env, entry)
	}
	cmd.Env = append(cmd.Env, gate+"=1", "SESAMEFS_E101_FAIL_AFTER_KILL=1", "SESAMEFS_E101_EVIDENCE_DIR="+dir)
	// Keep E's fixture-owning process on e19: a second forwarding TestMain
	// cannot inherit its liveRepoIDs and could sweep a root-only library.
	if e101eCut(cut) {
		cmd.Env = append(cmd.Env, "SESAMEFS_E101E_CHILD=1", "CASSANDRA_KEYSPACE=sesamefs_e19", "SESAMEFS_URL="+endpoint, "SESAMEFS_URL_2="+endpoint, "SESAMEFS_URL_3="+endpoint)
	}
	out, err := cmd.CombinedOutput()
	t.Logf("expected failing preservation child: %s", out)
	var failed *exec.ExitError
	if !errors.As(err, &failed) || failed.ExitCode() != 1 || !bytes.Contains(out, []byte("E101_INJECTED_FAILURE")) || !bytes.Contains(out, []byte("verified worker OS SIGKILL")) || bytes.Contains(out, []byte("independent recovery:")) {
		t.Fatalf("negative did not prove the intended failure without automatic recovery: %v", err)
	}
	paths, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("failure artifact did not survive child teardown: %v %v", paths, err)
	}
	encoded, err := os.ReadFile(paths[0])
	var failure e101Failure
	if err != nil || json.Unmarshal(encoded, &failure) != nil {
		t.Fatalf("invalid retained snapshot: %v", err)
	}
	retainedWork := !e101RecoveryCut(cut)
	expectedOrphans := 1
	if e101eCut(cut) {
		expectedOrphans = 0
	}
	expectedCandidates := 1
	if !retainedWork {
		expectedCandidates = 0
	}
	if (e101dCut(cut) && len(failure.After.Rows["gc_s3_orphans_by_day"]) != 1) || (e101eCut(cut) && len(failure.After.Rows["gc_s3_orphans_by_day"]) != 0) || failure.Before == nil || len(failure.Before.Errors) != 0 || len(failure.After.Errors) != 0 || !reflect.DeepEqual(*failure.Before, failure.After) || !failure.After.RootFound || failure.After.QueueFound != retainedWork || failure.After.K1Exists != retainedWork || (retainedWork && len(failure.After.K1Bytes) == 0) || len(failure.After.Rows["blocks"]) != blocks || len(failure.After.Rows["gc_s3_orphans"]) != expectedOrphans || len(failure.After.Rows["gc_block_candidates"]) != expectedCandidates || len(failure.After.Rows["head"]) != 1 || len(failure.After.Rows["gc_block_delete_lifecycles"]) != lifecycles {
		t.Fatal("failure snapshot lost or changed the exact durable boundary state")
	}
	// A new session must observe the same live state AFTER the failed subprocess and TestMain sweeps.
	data := failure.Data
	data.Mode, data.Marker = "verify_failure", paths[0]
	verify, stop := e101Command(t, data)
	defer stop()
	for i, entry := range verify.Env {
		if strings.HasPrefix(entry, "CASSANDRA_KEYSPACE=") {
			verify.Env[i] = "CASSANDRA_KEYSPACE=" + failure.Keyspace
		}
		if strings.HasPrefix(entry, "SESAMEFS_URL=") {
			verify.Env[i] = "SESAMEFS_URL=" + endpoint
		}
	}
	out, err = verify.CombinedOutput()
	t.Logf("post-teardown verification and explicit disposal: %s", out)
	if err != nil || !bytes.Contains(out, []byte("E101_FAILURE_SNAPSHOT_VERIFIED")) {
		t.Fatalf("live failure state did not survive: %v", err)
	}
	// Disposal is authorized only after artifact and live-state proof; it is not the failed leg's cleanup.
	resp := newTestClient(endpoint, "dev-token-admin").Delete(t, "/api/v2.1/repos/"+data.Repo+"/")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("dispose verified negative library: HTTP %d", resp.StatusCode)
	}
	if after, err := os.ReadFile(paths[0]); err != nil || !bytes.Equal(encoded, after) {
		t.Fatal("explicit disposal changed the retained artifact")
	}
}
func e101VerifyFailure(t *testing.T, database *dbpkg.DB, data e101ProcessData) {
	encoded, err := os.ReadFile(data.Marker)
	var failure e101Failure
	if err != nil || json.Unmarshal(encoded, &failure) != nil {
		t.Fatalf("read failure artifact: %v", err)
	}
	actual := e101Capture(database, data)
	// Decode both through JSON so Cassandra map value representations do not affect equality.
	live, err := json.Marshal(actual)
	var normalized e101Snapshot
	if err != nil || json.Unmarshal(live, &normalized) != nil || !reflect.DeepEqual(normalized, failure.After) {
		t.Fatalf("retained live state differs after failed child's cleanups: live=%s expected=%s err=%v", live, encoded, err)
	}
	t.Log("E101_FAILURE_SNAPSHOT_VERIFIED")
	t.Cleanup(func() { cleanupBlockUploadSessionForTest(t, data.Session) })
	cleanupUploadedBlockArtifactsForTest(t, data.Org.String(), data.Repo, data.Block, data.SHA1, dbpkg.BlockReferrerForUpload(data.Session))

	e101Recover(t, data)
	cleanupGCBlockFixturesForTest(t, data.Org, data.Block)
	fx := &w2CreateFileFixture{w2UploadFileFixture: &w2UploadFileFixture{borrowedFSHeadFixture: &borrowedFSHeadFixture{orgUUID: data.Org, orgID: data.Org.String(), blockID: data.Block}}}
	e115aVerifyNoRoot(t, database, fx)
}
