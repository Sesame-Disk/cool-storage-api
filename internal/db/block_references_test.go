package db

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestUpsertBlockMetadataWithSHA1_BackfillsEmptyExistingSHA1(t *testing.T) {
	database := &DB{}
	oldInsert := upsertBlockMetadataInsertFn
	oldRead := readBlockSHA1ForRepairFn
	oldBackfill := backfillBlockSHA1Fn
	t.Cleanup(func() {
		upsertBlockMetadataInsertFn = oldInsert
		readBlockSHA1ForRepairFn = oldRead
		backfillBlockSHA1Fn = oldBackfill
	})

	var calls []string
	upsertBlockMetadataInsertFn = func(database *DB, orgID, blockID, sha1 string, sizeBytes int, storageClass, storageKey string, now time.Time) (bool, error) {
		calls = append(calls, "insert")
		if orgID != "org-1" || blockID != "block-1" || sha1 != strings.Repeat("a", 40) {
			t.Fatalf("insert args = %s/%s/%s", orgID, blockID, sha1)
		}
		return false, nil // row already existed -> proceed to read + backfill
	}
	readBlockSHA1ForRepairFn = func(database *DB, orgID, blockID string) (string, bool, error) {
		calls = append(calls, "read")
		return "", true, nil
	}
	backfillBlockSHA1Fn = func(database *DB, orgID, blockID, sha1 string) (bool, error) {
		calls = append(calls, "backfill")
		if sha1 != strings.Repeat("a", 40) {
			t.Fatalf("backfill sha1 = %q, want %q", sha1, strings.Repeat("a", 40))
		}
		return true, nil
	}

	if err := database.UpsertBlockMetadataWithSHA1("org-1", "block-1", strings.Repeat("a", 40), 123, "hot", "key"); err != nil {
		t.Fatalf("UpsertBlockMetadataWithSHA1() error = %v, want nil", err)
	}
	want := []string{"insert", "read", "backfill"}
	if len(calls) != len(want) {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Fatalf("calls[%d] = %q, want %q (full=%#v)", i, calls[i], want[i], calls)
		}
	}
}

func TestUpsertBlockMetadataWithSHA1_FirstWriterSkipsReadAndBackfill(t *testing.T) {
	// Hot path: when the INSERT IF NOT EXISTS applies (a brand-new block), the row
	// already holds our sha1, so there must be NO extra read and NO backfill LWT.
	database := &DB{}
	oldInsert := upsertBlockMetadataInsertFn
	oldRead := readBlockSHA1ForRepairFn
	oldBackfill := backfillBlockSHA1Fn
	t.Cleanup(func() {
		upsertBlockMetadataInsertFn = oldInsert
		readBlockSHA1ForRepairFn = oldRead
		backfillBlockSHA1Fn = oldBackfill
	})

	upsertBlockMetadataInsertFn = func(database *DB, orgID, blockID, sha1 string, sizeBytes int, storageClass, storageKey string, now time.Time) (bool, error) {
		return true, nil // first writer created the row with this sha1
	}
	readBlockSHA1ForRepairFn = func(database *DB, orgID, blockID string) (string, bool, error) {
		t.Fatal("read should not run when the INSERT applied")
		return "", false, nil
	}
	backfillBlockSHA1Fn = func(database *DB, orgID, blockID, sha1 string) (bool, error) {
		t.Fatal("backfill should not run when the INSERT applied")
		return false, nil
	}

	if err := database.UpsertBlockMetadataWithSHA1("org-1", "block-1", strings.Repeat("a", 40), 123, "hot", "key"); err != nil {
		t.Fatalf("UpsertBlockMetadataWithSHA1() error = %v, want nil", err)
	}
}

func TestUpsertBlockMetadataWithSHA1_FailsWhenRowDisappearsBeforeBackfill(t *testing.T) {
	database := &DB{}
	oldInsert := upsertBlockMetadataInsertFn
	oldRead := readBlockSHA1ForRepairFn
	oldBackfill := backfillBlockSHA1Fn
	t.Cleanup(func() {
		upsertBlockMetadataInsertFn = oldInsert
		readBlockSHA1ForRepairFn = oldRead
		backfillBlockSHA1Fn = oldBackfill
	})

	upsertBlockMetadataInsertFn = func(database *DB, orgID, blockID, sha1 string, sizeBytes int, storageClass, storageKey string, now time.Time) (bool, error) {
		return false, nil // row already existed -> proceed to read/ensure
	}
	readBlockSHA1ForRepairFn = func(database *DB, orgID, blockID string) (string, bool, error) {
		return "", true, nil
	}
	// The conditional backfill does not apply: the row was GC'd between read and
	// write. The IF EXISTS guard reports applied=false instead of upserting a
	// partial row, and the caller must fail closed.
	backfillBlockSHA1Fn = func(database *DB, orgID, blockID, sha1 string) (bool, error) {
		return false, nil
	}

	err := database.UpsertBlockMetadataWithSHA1("org-1", "block-1", strings.Repeat("a", 40), 123, "hot", "key")
	if err == nil || !strings.Contains(err.Error(), "disappeared") {
		t.Fatalf("UpsertBlockMetadataWithSHA1() error = %v, want disappeared", err)
	}
}

func TestUpsertBlockMetadataWithSHA1_LeavesMatchingSHA1Untouched(t *testing.T) {
	database := &DB{}
	oldInsert := upsertBlockMetadataInsertFn
	oldRead := readBlockSHA1ForRepairFn
	oldBackfill := backfillBlockSHA1Fn
	t.Cleanup(func() {
		upsertBlockMetadataInsertFn = oldInsert
		readBlockSHA1ForRepairFn = oldRead
		backfillBlockSHA1Fn = oldBackfill
	})

	upsertBlockMetadataInsertFn = func(database *DB, orgID, blockID, sha1 string, sizeBytes int, storageClass, storageKey string, now time.Time) (bool, error) {
		return false, nil // row already existed -> proceed to read/ensure
	}
	readBlockSHA1ForRepairFn = func(database *DB, orgID, blockID string) (string, bool, error) {
		return strings.Repeat("b", 40), true, nil
	}
	backfillBlockSHA1Fn = func(database *DB, orgID, blockID, sha1 string) (bool, error) {
		t.Fatal("backfill should not run when the stored sha1 already matches")
		return false, nil
	}

	if err := database.UpsertBlockMetadataWithSHA1("org-1", "block-1", strings.Repeat("b", 40), 123, "hot", "key"); err != nil {
		t.Fatalf("UpsertBlockMetadataWithSHA1() error = %v, want nil", err)
	}
}

func TestUpsertBlockMetadataWithSHA1_RejectsConflictingExistingSHA1(t *testing.T) {
	database := &DB{}
	oldInsert := upsertBlockMetadataInsertFn
	oldRead := readBlockSHA1ForRepairFn
	oldBackfill := backfillBlockSHA1Fn
	t.Cleanup(func() {
		upsertBlockMetadataInsertFn = oldInsert
		readBlockSHA1ForRepairFn = oldRead
		backfillBlockSHA1Fn = oldBackfill
	})

	upsertBlockMetadataInsertFn = func(database *DB, orgID, blockID, sha1 string, sizeBytes int, storageClass, storageKey string, now time.Time) (bool, error) {
		return false, nil // row already existed -> proceed to read/ensure
	}
	readBlockSHA1ForRepairFn = func(database *DB, orgID, blockID string) (string, bool, error) {
		return strings.Repeat("c", 40), true, nil
	}
	backfillBlockSHA1Fn = func(database *DB, orgID, blockID, sha1 string) (bool, error) {
		t.Fatal("backfill should not run when the stored sha1 conflicts")
		return false, nil
	}

	err := database.UpsertBlockMetadataWithSHA1("org-1", "block-1", strings.Repeat("d", 40), 123, "hot", "key")
	if err == nil || !strings.Contains(err.Error(), "conflicting sha1") {
		t.Fatalf("UpsertBlockMetadataWithSHA1() error = %v, want conflicting sha1", err)
	}
}

func TestUpsertBlockMetadataWithSHA1_RejectsMalformedInputSHA1(t *testing.T) {
	database := &DB{}
	oldInsert := upsertBlockMetadataInsertFn
	oldRead := readBlockSHA1ForRepairFn
	t.Cleanup(func() {
		upsertBlockMetadataInsertFn = oldInsert
		readBlockSHA1ForRepairFn = oldRead
	})

	upsertBlockMetadataInsertFn = func(database *DB, orgID, blockID, sha1 string, sizeBytes int, storageClass, storageKey string, now time.Time) (bool, error) {
		t.Fatal("insert should not run for malformed sha1 input")
		return false, nil
	}
	readBlockSHA1ForRepairFn = func(database *DB, orgID, blockID string) (string, bool, error) {
		t.Fatal("read should not run for malformed sha1 input")
		return "", false, nil
	}

	err := database.UpsertBlockMetadataWithSHA1("org-1", "block-1", "not-a-sha1", 123, "hot", "key")
	if err == nil || !strings.Contains(err.Error(), "invalid block sha1") {
		t.Fatalf("UpsertBlockMetadataWithSHA1() error = %v, want invalid block sha1", err)
	}
}

func TestPromotePublishAttemptReferences_RetriesRegisterFailure(t *testing.T) {
	oldAttempts := publishAttemptPromotionRetryAttempts
	oldDelay := publishAttemptPromotionRetryDelay
	oldMaxDelay := publishAttemptPromotionRetryMaxDelay
	oldJitter := publishAttemptPromotionRetryJitter
	oldSleep := publishAttemptPromotionSleepFn
	oldRemove := removePublishAttemptReferencesForPromotionFn
	t.Cleanup(func() {
		publishAttemptPromotionRetryAttempts = oldAttempts
		publishAttemptPromotionRetryDelay = oldDelay
		publishAttemptPromotionRetryMaxDelay = oldMaxDelay
		publishAttemptPromotionRetryJitter = oldJitter
		publishAttemptPromotionSleepFn = oldSleep
		removePublishAttemptReferencesForPromotionFn = oldRemove
	})

	publishAttemptPromotionRetryAttempts = 2
	publishAttemptPromotionRetryDelay = time.Millisecond
	publishAttemptPromotionRetryMaxDelay = time.Millisecond
	publishAttemptPromotionRetryJitter = 0
	var slept []time.Duration
	publishAttemptPromotionSleepFn = func(delay time.Duration) {
		slept = append(slept, delay)
	}
	removeCalls := 0
	removePublishAttemptReferencesForPromotionFn = func(database *DB, orgID, attemptID string, blockIDs []string) error {
		removeCalls++
		if orgID != "org-1" || attemptID != "attempt-1" {
			t.Fatalf("remove args = %s/%s, want org-1/attempt-1", orgID, attemptID)
		}
		if len(blockIDs) != 1 || blockIDs[0] != "block-1" {
			t.Fatalf("remove blockIDs = %#v, want []string{\"block-1\"}", blockIDs)
		}
		return nil
	}

	registerCalls := 0
	wantErr := errors.New("register boom")
	err := PromotePublishAttemptReferences(nil, "org-1", "attempt-1", []string{"block-1"}, func() error {
		registerCalls++
		if registerCalls == 1 {
			return wantErr
		}
		return nil
	})
	if err != nil {
		t.Fatalf("PromotePublishAttemptReferences() error = %v, want nil", err)
	}
	if registerCalls != 2 {
		t.Fatalf("registerCalls = %d, want 2", registerCalls)
	}
	if removeCalls != 1 {
		t.Fatalf("removeCalls = %d, want 1", removeCalls)
	}
	if len(slept) != 1 || slept[0] != time.Millisecond {
		t.Fatalf("slept = %#v, want []time.Duration{time.Millisecond}", slept)
	}
}

func TestPromotePublishAttemptReferences_RetriesRemoveFailure(t *testing.T) {
	oldAttempts := publishAttemptPromotionRetryAttempts
	oldDelay := publishAttemptPromotionRetryDelay
	oldMaxDelay := publishAttemptPromotionRetryMaxDelay
	oldJitter := publishAttemptPromotionRetryJitter
	oldSleep := publishAttemptPromotionSleepFn
	oldRemove := removePublishAttemptReferencesForPromotionFn
	t.Cleanup(func() {
		publishAttemptPromotionRetryAttempts = oldAttempts
		publishAttemptPromotionRetryDelay = oldDelay
		publishAttemptPromotionRetryMaxDelay = oldMaxDelay
		publishAttemptPromotionRetryJitter = oldJitter
		publishAttemptPromotionSleepFn = oldSleep
		removePublishAttemptReferencesForPromotionFn = oldRemove
	})

	publishAttemptPromotionRetryAttempts = 2
	publishAttemptPromotionRetryDelay = 0
	publishAttemptPromotionRetryMaxDelay = 0
	publishAttemptPromotionRetryJitter = 0
	publishAttemptPromotionSleepFn = func(delay time.Duration) {
		t.Fatalf("sleep should not run when retry backoff is zero, got %s", delay)
	}

	registerCalls := 0
	removeCalls := 0
	wantErr := errors.New("remove boom")
	removePublishAttemptReferencesForPromotionFn = func(database *DB, orgID, attemptID string, blockIDs []string) error {
		removeCalls++
		if removeCalls == 1 {
			return wantErr
		}
		return nil
	}

	err := PromotePublishAttemptReferences(nil, "org-1", "attempt-1", []string{"block-1"}, func() error {
		registerCalls++
		return nil
	})
	if err != nil {
		t.Fatalf("PromotePublishAttemptReferences() error = %v, want nil", err)
	}
	if registerCalls != 2 {
		t.Fatalf("registerCalls = %d, want 2", registerCalls)
	}
	if removeCalls != 2 {
		t.Fatalf("removeCalls = %d, want 2", removeCalls)
	}
}

func TestPromotePublishAttemptReferences_ReturnsLastErrorAfterExhaustingRetries(t *testing.T) {
	oldAttempts := publishAttemptPromotionRetryAttempts
	oldDelay := publishAttemptPromotionRetryDelay
	oldMaxDelay := publishAttemptPromotionRetryMaxDelay
	oldJitter := publishAttemptPromotionRetryJitter
	oldSleep := publishAttemptPromotionSleepFn
	oldRemove := removePublishAttemptReferencesForPromotionFn
	t.Cleanup(func() {
		publishAttemptPromotionRetryAttempts = oldAttempts
		publishAttemptPromotionRetryDelay = oldDelay
		publishAttemptPromotionRetryMaxDelay = oldMaxDelay
		publishAttemptPromotionRetryJitter = oldJitter
		publishAttemptPromotionSleepFn = oldSleep
		removePublishAttemptReferencesForPromotionFn = oldRemove
	})

	publishAttemptPromotionRetryAttempts = 3
	publishAttemptPromotionRetryDelay = 0
	publishAttemptPromotionRetryMaxDelay = 0
	publishAttemptPromotionRetryJitter = 0
	publishAttemptPromotionSleepFn = func(delay time.Duration) {}

	removeCalls := 0
	removePublishAttemptReferencesForPromotionFn = func(database *DB, orgID, attemptID string, blockIDs []string) error {
		removeCalls++
		return nil
	}

	registerCalls := 0
	wantErr := errors.New("register boom")
	err := PromotePublishAttemptReferences(nil, "org-1", "attempt-1", []string{"block-1"}, func() error {
		registerCalls++
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("PromotePublishAttemptReferences() error = %v, want %v", err, wantErr)
	}
	if registerCalls != 3 {
		t.Fatalf("registerCalls = %d, want 3", registerCalls)
	}
	if removeCalls != 0 {
		t.Fatalf("removeCalls = %d, want 0", removeCalls)
	}
}

func TestStagePublishAttemptReferences_RollsBackPartialStage(t *testing.T) {
	oldAdd := addPublishAttemptReferenceFn
	oldRemove := removePublishAttemptReferenceFn
	t.Cleanup(func() {
		addPublishAttemptReferenceFn = oldAdd
		removePublishAttemptReferenceFn = oldRemove
	})

	var added []string
	var removed []string
	wantErr := errors.New("add boom")
	addPublishAttemptReferenceFn = func(database *DB, orgID, blockID, referrer, repoID string) error {
		if orgID != "org-1" || repoID != "repo-1" || referrer != BlockReferrerForPublishAttempt("attempt-1") {
			t.Fatalf("add args = %s/%s/%s, want org-1/repo-1/%s", orgID, repoID, referrer, BlockReferrerForPublishAttempt("attempt-1"))
		}
		if blockID == "block-2" {
			return wantErr
		}
		added = append(added, blockID)
		return nil
	}
	removePublishAttemptReferenceFn = func(database *DB, orgID, blockID, referrer string) error {
		if orgID != "org-1" || referrer != BlockReferrerForPublishAttempt("attempt-1") {
			t.Fatalf("remove args = %s/%s, want org-1/%s", orgID, referrer, BlockReferrerForPublishAttempt("attempt-1"))
		}
		removed = append(removed, blockID)
		return nil
	}

	resolved, err := StagePublishAttemptReferences(&DB{}, "org-1", "repo-1", "attempt-1", []string{"block-1", "block-2"}, nil)
	if !errors.Is(err, wantErr) {
		t.Fatalf("StagePublishAttemptReferences() error = %v, want %v", err, wantErr)
	}
	if resolved != nil {
		t.Fatalf("resolved = %#v, want nil on stage failure", resolved)
	}
	if len(added) != 1 || added[0] != "block-1" {
		t.Fatalf("added = %#v, want []string{\"block-1\"}", added)
	}
	if len(removed) != 1 || removed[0] != "block-1" {
		t.Fatalf("removed = %#v, want []string{\"block-1\"}", removed)
	}
}

func TestStagePublishAttemptReferences_JoinsRollbackFailure(t *testing.T) {
	oldAdd := addPublishAttemptReferenceFn
	oldRemove := removePublishAttemptReferenceFn
	t.Cleanup(func() {
		addPublishAttemptReferenceFn = oldAdd
		removePublishAttemptReferenceFn = oldRemove
	})

	wantStageErr := errors.New("add boom")
	wantRollbackErr := errors.New("remove boom")
	addPublishAttemptReferenceFn = func(database *DB, orgID, blockID, referrer, repoID string) error {
		if blockID == "block-2" {
			return wantStageErr
		}
		return nil
	}
	removePublishAttemptReferenceFn = func(database *DB, orgID, blockID, referrer string) error {
		return wantRollbackErr
	}

	_, err := StagePublishAttemptReferences(&DB{}, "org-1", "repo-1", "attempt-1", []string{"block-1", "block-2"}, nil)
	if !errors.Is(err, wantStageErr) {
		t.Fatalf("error = %v, want stage error %v", err, wantStageErr)
	}
	if !errors.Is(err, wantRollbackErr) {
		t.Fatalf("error = %v, want rollback error %v", err, wantRollbackErr)
	}
}

func TestProbeBlockReuseReusable(t *testing.T) {
	oldMetadata := probeBlockReuseMetadataFn
	oldRefs := probeBlockReuseHasReferencesFn
	oldOrphan := probeBlockReuseHasS3OrphanFn
	t.Cleanup(func() {
		probeBlockReuseMetadataFn = oldMetadata
		probeBlockReuseHasReferencesFn = oldRefs
		probeBlockReuseHasS3OrphanFn = oldOrphan
	})

	probeBlockReuseMetadataFn = func(database *DB, orgID, blockID string) (blockReuseMetadataRow, bool, error) {
		return blockReuseMetadataRow{Sha1: "sha1-abc", SizeBytes: 123, StorageClass: "hot-s3", StorageKey: "", GCState: ""}, true, nil
	}
	probeBlockReuseHasReferencesFn = func(database *DB, orgID, blockID string) (bool, error) {
		return true, nil
	}
	probeBlockReuseHasS3OrphanFn = func(database *DB, orgID, blockID string) (bool, error) {
		return false, nil
	}

	probe, err := (&DB{}).ProbeBlockReuse("org-1", "block-1")
	if err != nil {
		t.Fatalf("ProbeBlockReuse() error = %v, want nil", err)
	}
	if probe.Decision != BlockReuseReusable {
		t.Fatalf("decision = %v, want BlockReuseReusable", probe.Decision)
	}
	if probe.StorageClass != "hot-s3" {
		t.Fatalf("storage class = %q, want hot-s3", probe.StorageClass)
	}
	if probe.Sha1 != "sha1-abc" {
		t.Fatalf("sha1 = %q, want sha1-abc", probe.Sha1)
	}
}

func TestProbeBlockReuseNeedsPutWithoutMetadata(t *testing.T) {
	oldMetadata := probeBlockReuseMetadataFn
	oldOrphan := probeBlockReuseHasS3OrphanFn
	t.Cleanup(func() {
		probeBlockReuseMetadataFn = oldMetadata
		probeBlockReuseHasS3OrphanFn = oldOrphan
	})

	probeBlockReuseMetadataFn = func(database *DB, orgID, blockID string) (blockReuseMetadataRow, bool, error) {
		return blockReuseMetadataRow{}, false, nil
	}
	probeBlockReuseHasS3OrphanFn = func(database *DB, orgID, blockID string) (bool, error) {
		return false, nil
	}

	probe, err := (&DB{}).ProbeBlockReuse("org-1", "block-1")
	if err != nil {
		t.Fatalf("ProbeBlockReuse() error = %v, want nil", err)
	}
	if probe.Decision != BlockReuseNeedsPut {
		t.Fatalf("decision = %v, want BlockReuseNeedsPut", probe.Decision)
	}
}

func TestProbeBlockReuseBlockedByGC(t *testing.T) {
	oldMetadata := probeBlockReuseMetadataFn
	oldOrphan := probeBlockReuseHasS3OrphanFn
	t.Cleanup(func() {
		probeBlockReuseMetadataFn = oldMetadata
		probeBlockReuseHasS3OrphanFn = oldOrphan
	})

	probeBlockReuseMetadataFn = func(database *DB, orgID, blockID string) (blockReuseMetadataRow, bool, error) {
		return blockReuseMetadataRow{}, false, nil
	}
	probeBlockReuseHasS3OrphanFn = func(database *DB, orgID, blockID string) (bool, error) {
		return true, nil
	}

	probe, err := (&DB{}).ProbeBlockReuse("org-1", "block-1")
	if err != nil {
		t.Fatalf("ProbeBlockReuse() error = %v, want nil", err)
	}
	if probe.Decision != BlockReuseBlockedByGC {
		t.Fatalf("decision = %v, want BlockReuseBlockedByGC", probe.Decision)
	}
}

func TestProbeBlockReuseReturnsUnknownErrorForEmptyStorageClass(t *testing.T) {
	oldMetadata := probeBlockReuseMetadataFn
	t.Cleanup(func() { probeBlockReuseMetadataFn = oldMetadata })

	probeBlockReuseMetadataFn = func(database *DB, orgID, blockID string) (blockReuseMetadataRow, bool, error) {
		return blockReuseMetadataRow{SizeBytes: 123, StorageClass: "   ", StorageKey: "", GCState: ""}, true, nil
	}

	probe, err := (&DB{}).ProbeBlockReuse("org-1", "block-1")
	if err == nil {
		t.Fatal("ProbeBlockReuse() error = nil, want error")
	}
	if probe.Decision != BlockReuseUnknownError {
		t.Fatalf("decision = %v, want BlockReuseUnknownError", probe.Decision)
	}
}

// TestProbeBlockReuseNeedsPutWhenMetadataPresentButNoReferences covers the
// branch where a canonical block row exists (with a distinct, non-default
// storage class) but has no live references and is not GC-fenced. The block is
// an unreferenced GC candidate, so a fresh upload must re-materialize it with a
// direct PUT rather than trust a potentially-collectible object. The canonical
// storage class and size still flow through the probe for the materialize step.
func TestProbeBlockReuseNeedsPutWhenMetadataPresentButNoReferences(t *testing.T) {
	oldMetadata := probeBlockReuseMetadataFn
	oldRefs := probeBlockReuseHasReferencesFn
	oldOrphan := probeBlockReuseHasS3OrphanFn
	t.Cleanup(func() {
		probeBlockReuseMetadataFn = oldMetadata
		probeBlockReuseHasReferencesFn = oldRefs
		probeBlockReuseHasS3OrphanFn = oldOrphan
	})

	probeBlockReuseMetadataFn = func(database *DB, orgID, blockID string) (blockReuseMetadataRow, bool, error) {
		return blockReuseMetadataRow{Sha1: "sha1-cold", SizeBytes: 4096, StorageClass: "cold-archive", StorageKey: "blocks/ab/cd", GCState: ""}, true, nil
	}
	probeBlockReuseHasReferencesFn = func(database *DB, orgID, blockID string) (bool, error) {
		return false, nil
	}
	probeBlockReuseHasS3OrphanFn = func(database *DB, orgID, blockID string) (bool, error) {
		return false, nil
	}

	probe, err := (&DB{}).ProbeBlockReuse("org-1", "block-1")
	if err != nil {
		t.Fatalf("ProbeBlockReuse() error = %v, want nil", err)
	}
	if probe.Decision != BlockReuseNeedsPut {
		t.Fatalf("decision = %v, want BlockReuseNeedsPut", probe.Decision)
	}
	if probe.StorageClass != "cold-archive" {
		t.Fatalf("storage class = %q, want cold-archive", probe.StorageClass)
	}
	if probe.SizeBytes != 4096 {
		t.Fatalf("size = %d, want 4096", probe.SizeBytes)
	}
	if probe.Sha1 != "sha1-cold" {
		t.Fatalf("sha1 = %q, want sha1-cold", probe.Sha1)
	}
}

// TestProbeBlockReuseBlockedByGCWhenGCStateDeleting verifies the in-row claim
// (gc_state='deleting') is an immediate fence that short-circuits before the
// reference read, so a concurrent re-upload backs off even while references
// momentarily exist.
func TestProbeBlockReuseBlockedByGCWhenGCStateDeleting(t *testing.T) {
	oldMetadata := probeBlockReuseMetadataFn
	oldRefs := probeBlockReuseHasReferencesFn
	oldOrphan := probeBlockReuseHasS3OrphanFn
	t.Cleanup(func() {
		probeBlockReuseMetadataFn = oldMetadata
		probeBlockReuseHasReferencesFn = oldRefs
		probeBlockReuseHasS3OrphanFn = oldOrphan
	})

	probeBlockReuseMetadataFn = func(database *DB, orgID, blockID string) (blockReuseMetadataRow, bool, error) {
		return blockReuseMetadataRow{SizeBytes: 10, StorageClass: "hot", GCState: BlockGCStateDeleting}, true, nil
	}
	refsCalled := false
	probeBlockReuseHasReferencesFn = func(database *DB, orgID, blockID string) (bool, error) {
		refsCalled = true
		return true, nil
	}
	probeBlockReuseHasS3OrphanFn = func(database *DB, orgID, blockID string) (bool, error) {
		return false, nil
	}

	probe, err := (&DB{}).ProbeBlockReuse("org-1", "block-1")
	if err != nil {
		t.Fatalf("ProbeBlockReuse() error = %v, want nil", err)
	}
	if probe.Decision != BlockReuseBlockedByGC {
		t.Fatalf("decision = %v, want BlockReuseBlockedByGC", probe.Decision)
	}
	if refsCalled {
		t.Fatal("references were read even though gc_state=deleting must short-circuit")
	}
}

// TestProbeBlockReuseReturnsUnknownErrorWhenMetadataReadFails verifies a
// Cassandra read failure surfaces as UnknownError with the underlying error
// wrapped, so callers fall open to the legacy Exists+PUT path instead of
// silently skipping the PUT.
func TestProbeBlockReuseReturnsUnknownErrorWhenMetadataReadFails(t *testing.T) {
	oldMetadata := probeBlockReuseMetadataFn
	t.Cleanup(func() { probeBlockReuseMetadataFn = oldMetadata })

	wantErr := errors.New("cassandra unavailable")
	probeBlockReuseMetadataFn = func(database *DB, orgID, blockID string) (blockReuseMetadataRow, bool, error) {
		return blockReuseMetadataRow{}, false, wantErr
	}

	probe, err := (&DB{}).ProbeBlockReuse("org-1", "block-1")
	if !errors.Is(err, wantErr) {
		t.Fatalf("ProbeBlockReuse() error = %v, want wrapped %v", err, wantErr)
	}
	if probe.Decision != BlockReuseUnknownError {
		t.Fatalf("decision = %v, want BlockReuseUnknownError", probe.Decision)
	}
}
