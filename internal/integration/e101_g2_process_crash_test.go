//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
	gcpkg "github.com/Sesame-Disk/sesamefs/internal/gc"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/google/uuid"
)

const e101EvidenceEnv = "SESAMEFS_REQUIRE_E101_G2_CRASH_EVIDENCE"

var e101Observed = map[string]bool{}

func e101Missing() []string {
	var missing []string
	for _, cut := range []string{"prepared", "committed"} {
		for _, action := range []string{"kill", "resume"} {
			if leg := cut + "/" + action; !e101Observed[leg] {
				missing = append(missing, leg)
			}
		}
	}
	return missing
}

type e101ProcessData struct {
	Org                                     uuid.UUID
	Block, Class, Marker, Cut, Mode, Expect string
	Candidate                               gcpkg.BlockGCCandidateInfo
	Authority                               gcpkg.BlockDeleteAuthority
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
	// Cassandra timestamps persist millisecond precision, as normalizeBlockDeleteAuthority does.
	a.ClaimedAt = a.ClaimedAt.UTC().Truncate(time.Millisecond)
	encoded, err := json.Marshal(a)
	if err != nil {
		s.t.Fatal(err)
	}
	if err := os.WriteFile(s.data.Marker+".tmp", encoded, 0600); err != nil {
		s.t.Fatal(err)
	}
	if err := os.Rename(s.data.Marker+".tmp", s.data.Marker); err != nil {
		s.t.Fatal(err)
	}
	e112AwaitFile(s.t, s.data.Marker+".resume")
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
	if data.Mode == "worker" {
		scope := &w2ClosureOwnedQueue{GCStore: store, org: data.Org, block: data.Block, identity: data.Candidate.Identity()}
		paused := &e101PausedStore{w2ClosureOwnedQueue: scope, t: t, data: data}
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
	database := shareProjectionDBForTest(t)
	for _, cut := range []string{"prepared", "committed"} {
		t.Run(cut, func(t *testing.T) {
			for _, action := range []string{"kill", "resume"} {
				t.Run(action, func(t *testing.T) {
					// Account for evidence only after parent-owned fixture teardown has succeeded.
					t.Cleanup(func() {
						if !t.Failed() && !t.Skipped() {
							e101Observed[cut+"/"+action] = true
						}
					})
					class := x1StorageClass(t)
					web := newSessionUploadHeadFixture(t, database, newBorrowedFSHeadHandler(t, database, class))
					fx := &w2CreateFileFixture{w2UploadFileFixture: &w2UploadFileFixture{borrowedFSHeadFixture: web.borrowedFSHeadFixture}}
					web.dropOwnSessionRef(t)
					store := gcpkg.NewCassandraStore(database)
					candidate := w2Candidate(t, store, fx.orgUUID, fx.blockID, class)
					data := e101ProcessData{Org: fx.orgUUID, Block: fx.blockID, Class: class, Marker: filepath.Join(t.TempDir(), "cut"), Cut: cut, Mode: "worker", Candidate: candidate}
					// Never erase lifecycle authority before productive recovery has completed/aborted it.
					t.Cleanup(func() {
						if data.Authority.IsZero() {
							t.Errorf("no exact D captured; preserving GC fixture authority")
							return
						}
						e101Recover(t, data)
						cleanupGCBlockFixturesForTest(t, fx.orgUUID, fx.blockID)
						e115aVerifyNoRoot(t, database, fx)
					})
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
					e101AssertBoundary(t, fx, data)
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
						if err != nil {
							t.Fatalf("no-kill worker: %v\n%s", err, output.String())
						}
					}
					data.Expect = "terminal"
					if cut == "prepared" && action == "kill" {
						data.Expect = "aborted"
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
	t.Logf("independently observed %s P=%+v D=%+v", data.Expect, data.Authority.Target, data.Authority)
}

func e101Isolated(t *testing.T, endpoint string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	run := "^TestE101G2ProcessCrash$"
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
	cmd.Env = append(cmd.Env, e101EvidenceEnv+"=1", "SESAMEFS_E101_CHILD=1", "CASSANDRA_KEYSPACE=sesamefs_e19", "SESAMEFS_URL="+endpoint, "SESAMEFS_URL_2="+endpoint, "SESAMEFS_URL_3="+endpoint)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("isolated E1-01A evidence: %v", err)
	}
	for _, leg := range e101Missing() {
		e101Observed[leg] = true
	}
}
