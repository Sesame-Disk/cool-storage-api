package db

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
)

func withInstallBlockMetadataSeams(t *testing.T) {
	t.Helper()
	oldInstall := installBlockMetadataLWTFn
	oldSettle := settleInstalledBlockMetadataFn
	t.Cleanup(func() {
		installBlockMetadataLWTFn = oldInstall
		settleInstalledBlockMetadataFn = oldSettle
	})
}

func TestInstallBlockMetadataDirectOutcomesCompareCompleteTuple(t *testing.T) {
	withInstallBlockMetadataSeams(t)
	proposed := BlockPhysicalLocation{StorageClass: "hot", StorageKey: "blocks/org-1/minted"}

	tests := []struct {
		name        string
		applied     bool
		current     map[string]interface{}
		wantOutcome InstallBlockMetadataOutcome
		want        BlockPhysicalLocation
	}{
		{name: "insert applied", applied: true, wantOutcome: InstallBlockMetadataApplied, want: proposed},
		{name: "CAS returns exact tuple contradiction", current: map[string]interface{}{"storage_class": proposed.StorageClass, "storage_key": proposed.StorageKey}, wantOutcome: InstallBlockMetadataIdentityContradiction},
		{name: "different key loses", current: map[string]interface{}{"storage_class": proposed.StorageClass, "storage_key": "blocks/org-1/winner"}, wantOutcome: InstallBlockMetadataKnownLost, want: BlockPhysicalLocation{StorageClass: "hot", StorageKey: "blocks/org-1/winner"}},
		{name: "same key in different class loses", current: map[string]interface{}{"storage_class": "cold", "storage_key": proposed.StorageKey}, wantOutcome: InstallBlockMetadataKnownLost, want: BlockPhysicalLocation{StorageClass: "cold", StorageKey: proposed.StorageKey}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			installCalls := 0
			installBlockMetadataLWTFn = func(ctx context.Context, database *DB, orgID, blockID, representationID, sha1 string, sizeBytes int, location BlockPhysicalLocation, now time.Time) (bool, map[string]interface{}, error) {
				installCalls++
				if ctx == nil || database == nil || orgID != "org-1" || blockID != "block-1" || representationID != PlainBlockRepresentationID || sha1 != strings.Repeat("a", 40) || sizeBytes != 123 || location != proposed || now.IsZero() {
					t.Fatalf("install args were not preserved: db=%p org=%q block=%q representation=%q sha1=%q size=%d location=%+v now=%v", database, orgID, blockID, representationID, sha1, sizeBytes, location, now)
				}
				return test.applied, test.current, nil
			}
			settleInstalledBlockMetadataFn = func(context.Context, *DB, string, string) (installedBlockMetadataRow, bool, error) {
				t.Fatal("settlement must not run after a definite CAS result")
				return installedBlockMetadataRow{}, false, nil
			}

			got := (&DB{}).InstallBlockMetadata(context.Background(), "org-1", PlainBlockRepresentationID, "block-1", strings.Repeat("a", 40), 123, proposed)
			if got.Outcome != test.wantOutcome || got.Canonical != test.want {
				t.Fatalf("InstallBlockMetadata() = %+v, want outcome %v canonical %+v", got, test.wantOutcome, test.want)
			}
			if !got.Submitted {
				t.Fatal("InstallBlockMetadata() Submitted = false, want true after LWT seam entry")
			}
			if test.wantOutcome == InstallBlockMetadataIdentityContradiction && !errors.Is(got.Cause, ErrInstallBlockMetadataIdentityContradiction) {
				t.Fatalf("InstallBlockMetadata() cause = %v, want identity contradiction", got.Cause)
			}
			if installCalls != 1 {
				t.Fatalf("install calls = %d, want exactly 1", installCalls)
			}
		})
	}
}

func TestInstallBlockMetadataMalformedCASRowsFailClosed(t *testing.T) {
	withInstallBlockMetadataSeams(t)
	proposed := BlockPhysicalLocation{StorageClass: "hot", StorageKey: "blocks/org-1/minted"}
	tests := []map[string]interface{}{
		{"storage_key": proposed.StorageKey},
		{"storage_class": proposed.StorageClass},
		{"storage_class": "Hot", "storage_key": proposed.StorageKey},
		{"storage_class": proposed.StorageClass, "storage_key": " "},
		{"storage_class": 42, "storage_key": proposed.StorageKey},
	}
	for _, current := range tests {
		installBlockMetadataLWTFn = func(context.Context, *DB, string, string, string, string, int, BlockPhysicalLocation, time.Time) (bool, map[string]interface{}, error) {
			return false, current, nil
		}
		got := (&DB{}).InstallBlockMetadata(context.Background(), "org-1", PlainBlockRepresentationID, "block-1", "", 1, proposed)
		if got.Outcome != InstallBlockMetadataAmbiguous || got.Canonical != (BlockPhysicalLocation{}) || got.Cause == nil {
			t.Fatalf("InstallBlockMetadata() = %+v, want non-authorizing malformed result", got)
		}
	}
}

func TestInstallBlockMetadataSettlesMutationErrorsWithoutRepeatingInstall(t *testing.T) {
	withInstallBlockMetadataSeams(t)
	proposed := BlockPhysicalLocation{StorageClass: "hot", StorageKey: "blocks/org-1/minted"}
	installErr := errors.New("LWT timeout")

	tests := []struct {
		name        string
		row         installedBlockMetadataRow
		found       bool
		settleErr   error
		wantOutcome InstallBlockMetadataOutcome
		want        BlockPhysicalLocation
	}{
		{name: "exact tuple applied", row: completeInstalledBlockMetadataRow(proposed), found: true, wantOutcome: InstallBlockMetadataApplied, want: proposed},
		{name: "other tuple lost", row: completeInstalledBlockMetadataRow(BlockPhysicalLocation{StorageClass: "cold", StorageKey: proposed.StorageKey}), found: true, wantOutcome: InstallBlockMetadataKnownLost, want: BlockPhysicalLocation{StorageClass: "cold", StorageKey: proposed.StorageKey}},
		{name: "no row lost", wantOutcome: InstallBlockMetadataKnownLost},
		{name: "read failure ambiguous", settleErr: errors.New("SERIAL unavailable"), wantOutcome: InstallBlockMetadataAmbiguous},
		{name: "malformed row ambiguous", row: installedBlockMetadataRow{Location: proposed, StorageKeyPresent: true}, found: true, wantOutcome: InstallBlockMetadataAmbiguous},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			installCalls := 0
			installBlockMetadataLWTFn = func(context.Context, *DB, string, string, string, string, int, BlockPhysicalLocation, time.Time) (bool, map[string]interface{}, error) {
				installCalls++
				return false, nil, installErr
			}
			settleCalls := 0
			settleInstalledBlockMetadataFn = func(context.Context, *DB, string, string) (installedBlockMetadataRow, bool, error) {
				settleCalls++
				return test.row, test.found, test.settleErr
			}

			got := (&DB{}).InstallBlockMetadata(context.Background(), "org-1", PlainBlockRepresentationID, "block-1", "", 1, proposed)
			if got.Outcome != test.wantOutcome || got.Canonical != test.want {
				t.Fatalf("InstallBlockMetadata() = %+v, want outcome %v canonical %+v", got, test.wantOutcome, test.want)
			}
			if !got.Submitted {
				t.Fatal("settled InstallBlockMetadata() Submitted = false, want true")
			}
			if installCalls != 1 || settleCalls != 1 {
				t.Fatalf("install/settle calls = %d/%d, want 1/1", installCalls, settleCalls)
			}
		})
	}
}

func TestInstallBlockMetadataSettlementSurvivesRequestCancellationAndIsBounded(t *testing.T) {
	withInstallBlockMetadataSeams(t)
	requestCtx, cancelRequest := context.WithCancel(context.Background())
	proposed := BlockPhysicalLocation{StorageClass: "hot", StorageKey: "blocks/org-1/minted"}
	installBlockMetadataLWTFn = func(context.Context, *DB, string, string, string, string, int, BlockPhysicalLocation, time.Time) (bool, map[string]interface{}, error) {
		cancelRequest()
		return false, nil, errors.New("request canceled after submission")
	}
	settleInstalledBlockMetadataFn = func(ctx context.Context, _ *DB, _, _ string) (installedBlockMetadataRow, bool, error) {
		if err := ctx.Err(); err != nil {
			t.Fatalf("settlement context inherited request cancellation: %v", err)
		}
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > time.Second {
			t.Fatalf("settlement deadline = %v, want active bound no greater than 1s", deadline)
		}
		return completeInstalledBlockMetadataRow(proposed), true, nil
	}

	got := (&DB{config: config.DatabaseConfig{Timeout: time.Second}}).InstallBlockMetadata(requestCtx, "org-1", PlainBlockRepresentationID, "block-1", "", 1, proposed)
	if got.Outcome != InstallBlockMetadataApplied || got.Canonical != proposed {
		t.Fatalf("InstallBlockMetadata() = %+v, want settled Applied", got)
	}
	if !got.Submitted {
		t.Fatal("settled InstallBlockMetadata() Submitted = false, want true")
	}
}

func TestInstallBlockMetadataRejectsInvalidInputBeforeInstall(t *testing.T) {
	withInstallBlockMetadataSeams(t)
	installBlockMetadataLWTFn = func(context.Context, *DB, string, string, string, string, int, BlockPhysicalLocation, time.Time) (bool, map[string]interface{}, error) {
		t.Fatal("invalid input must not issue INSTALL")
		return false, nil, nil
	}
	settleInstalledBlockMetadataFn = func(context.Context, *DB, string, string) (installedBlockMetadataRow, bool, error) {
		t.Fatal("invalid input must not settle an INSTALL that was never issued")
		return installedBlockMetadataRow{}, false, nil
	}

	for _, proposed := range []BlockPhysicalLocation{{StorageClass: "Hot", StorageKey: "key"}, {StorageClass: "hot", StorageKey: " key "}, {StorageClass: "hot"}} {
		got := (&DB{}).InstallBlockMetadata(context.Background(), "org-1", PlainBlockRepresentationID, "block-1", "", 1, proposed)
		if got.Outcome != InstallBlockMetadataAmbiguous || !errors.Is(got.Cause, ErrBlockMetadataPermanent) {
			t.Fatalf("InstallBlockMetadata(%+v) = %+v, want non-authorizing permanent rejection", proposed, got)
		}
		if got.Submitted {
			t.Fatalf("InstallBlockMetadata(%+v) Submitted = true before LWT", proposed)
		}
	}
}

func completeInstalledBlockMetadataRow(location BlockPhysicalLocation) installedBlockMetadataRow {
	return installedBlockMetadataRow{Location: location, StorageClassPresent: true, StorageKeyPresent: true}
}

func completeIdentityRepairRow(representationID, sha1 string) blockIdentityRepairRow {
	createdAt := time.Now().UTC()
	return blockIdentityRepairRow{
		RepresentationID:    representationID,
		Sha1:                sha1,
		StorageClass:        "hot",
		StorageClassPresent: true,
		StorageKey:          "key",
		CreatedAt:           &createdAt,
	}
}

func completeProbeMetadataRow(storageClass string) blockReuseMetadataRow {
	createdAt := time.Now().UTC()
	return blockReuseMetadataRow{
		StorageClass:        storageClass,
		StorageClassPresent: true,
		StorageKey:          "canonical-key",
		CreatedAt:           &createdAt,
	}
}

func TestUpsertBlockMetadataWithSHA1_BackfillsEmptyExistingSHA1(t *testing.T) {
	database := &DB{}
	oldInsert := upsertBlockMetadataInsertFn
	oldRead := readBlockIdentityForRepairFn
	oldBackfill := backfillBlockSHA1Fn
	t.Cleanup(func() {
		upsertBlockMetadataInsertFn = oldInsert
		readBlockIdentityForRepairFn = oldRead
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
	readBlockIdentityForRepairFn = func(database *DB, orgID, blockID string) (blockIdentityRepairRow, bool, error) {
		calls = append(calls, "read")
		return completeIdentityRepairRow(PlainBlockRepresentationID, ""), true, nil
	}
	backfillBlockSHA1Fn = func(database *DB, orgID, blockID, sha1, expectedCurrent string) (bool, error) {
		calls = append(calls, "backfill")
		if sha1 != strings.Repeat("a", 40) {
			t.Fatalf("backfill sha1 = %q, want %q", sha1, strings.Repeat("a", 40))
		}
		if expectedCurrent != "" {
			t.Fatalf("expectedCurrent = %q, want empty", expectedCurrent)
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
	oldRead := readBlockIdentityForRepairFn
	oldBackfill := backfillBlockSHA1Fn
	t.Cleanup(func() {
		upsertBlockMetadataInsertFn = oldInsert
		readBlockIdentityForRepairFn = oldRead
		backfillBlockSHA1Fn = oldBackfill
	})

	upsertBlockMetadataInsertFn = func(database *DB, orgID, blockID, sha1 string, sizeBytes int, storageClass, storageKey string, now time.Time) (bool, error) {
		return true, nil // first writer created the row with this sha1
	}
	readBlockIdentityForRepairFn = func(database *DB, orgID, blockID string) (blockIdentityRepairRow, bool, error) {
		t.Fatal("read should not run when the INSERT applied")
		return blockIdentityRepairRow{}, false, nil
	}
	backfillBlockSHA1Fn = func(database *DB, orgID, blockID, sha1, expectedCurrent string) (bool, error) {
		t.Fatal("backfill should not run when the INSERT applied")
		return false, nil
	}

	if err := database.UpsertBlockMetadataWithSHA1("org-1", "block-1", strings.Repeat("a", 40), 123, "hot", "key"); err != nil {
		t.Fatalf("UpsertBlockMetadataWithSHA1() error = %v, want nil", err)
	}
}

func TestUpsertBlockMetadataWithSHA1_FailsWhenRowChangesBeforeBackfill(t *testing.T) {
	database := &DB{}
	oldInsert := upsertBlockMetadataInsertFn
	oldRead := readBlockIdentityForRepairFn
	oldBackfill := backfillBlockSHA1Fn
	t.Cleanup(func() {
		upsertBlockMetadataInsertFn = oldInsert
		readBlockIdentityForRepairFn = oldRead
		backfillBlockSHA1Fn = oldBackfill
	})

	upsertBlockMetadataInsertFn = func(database *DB, orgID, blockID, sha1 string, sizeBytes int, storageClass, storageKey string, now time.Time) (bool, error) {
		return false, nil // row already existed -> proceed to read/ensure
	}
	readBlockIdentityForRepairFn = func(database *DB, orgID, blockID string) (blockIdentityRepairRow, bool, error) {
		return completeIdentityRepairRow(PlainBlockRepresentationID, ""), true, nil
	}
	// The conditional backfill does not apply: another writer (or GC) changed the
	// row between read and write. The CAS reports applied=false and the caller must
	// fail closed instead of overwriting or creating a phantom row.
	backfillBlockSHA1Fn = func(database *DB, orgID, blockID, sha1, expectedCurrent string) (bool, error) {
		if expectedCurrent != "" {
			t.Fatalf("expectedCurrent = %q, want empty", expectedCurrent)
		}
		return false, nil
	}

	err := database.UpsertBlockMetadataWithSHA1("org-1", "block-1", strings.Repeat("a", 40), 123, "hot", "key")
	if err == nil || !strings.Contains(err.Error(), "changed before sha1 repair") {
		t.Fatalf("UpsertBlockMetadataWithSHA1() error = %v, want changed-before-sha1-repair", err)
	}
}

func TestUpsertBlockMetadataWithSHA1_LeavesMatchingSHA1Untouched(t *testing.T) {
	database := &DB{}
	oldInsert := upsertBlockMetadataInsertFn
	oldRead := readBlockIdentityForRepairFn
	oldBackfill := backfillBlockSHA1Fn
	t.Cleanup(func() {
		upsertBlockMetadataInsertFn = oldInsert
		readBlockIdentityForRepairFn = oldRead
		backfillBlockSHA1Fn = oldBackfill
	})

	upsertBlockMetadataInsertFn = func(database *DB, orgID, blockID, sha1 string, sizeBytes int, storageClass, storageKey string, now time.Time) (bool, error) {
		return false, nil // row already existed -> proceed to read/ensure
	}
	readBlockIdentityForRepairFn = func(database *DB, orgID, blockID string) (blockIdentityRepairRow, bool, error) {
		return completeIdentityRepairRow(PlainBlockRepresentationID, strings.Repeat("b", 40)), true, nil
	}
	backfillBlockSHA1Fn = func(database *DB, orgID, blockID, sha1, expectedCurrent string) (bool, error) {
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
	oldRead := readBlockIdentityForRepairFn
	oldBackfill := backfillBlockSHA1Fn
	t.Cleanup(func() {
		upsertBlockMetadataInsertFn = oldInsert
		readBlockIdentityForRepairFn = oldRead
		backfillBlockSHA1Fn = oldBackfill
	})

	upsertBlockMetadataInsertFn = func(database *DB, orgID, blockID, sha1 string, sizeBytes int, storageClass, storageKey string, now time.Time) (bool, error) {
		return false, nil // row already existed -> proceed to read/ensure
	}
	readBlockIdentityForRepairFn = func(database *DB, orgID, blockID string) (blockIdentityRepairRow, bool, error) {
		return completeIdentityRepairRow(PlainBlockRepresentationID, strings.Repeat("c", 40)), true, nil
	}
	backfillBlockSHA1Fn = func(database *DB, orgID, blockID, sha1, expectedCurrent string) (bool, error) {
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
	oldRead := readBlockIdentityForRepairFn
	t.Cleanup(func() {
		upsertBlockMetadataInsertFn = oldInsert
		readBlockIdentityForRepairFn = oldRead
	})

	upsertBlockMetadataInsertFn = func(database *DB, orgID, blockID, sha1 string, sizeBytes int, storageClass, storageKey string, now time.Time) (bool, error) {
		t.Fatal("insert should not run for malformed sha1 input")
		return false, nil
	}
	readBlockIdentityForRepairFn = func(database *DB, orgID, blockID string) (blockIdentityRepairRow, bool, error) {
		t.Fatal("read should not run for malformed sha1 input")
		return blockIdentityRepairRow{}, false, nil
	}

	err := database.UpsertBlockMetadataWithSHA1("org-1", "block-1", "not-a-sha1", 123, "hot", "key")
	if err == nil || !strings.Contains(err.Error(), "invalid block sha1") {
		t.Fatalf("UpsertBlockMetadataWithSHA1() error = %v, want invalid block sha1", err)
	}
}

func TestUpsertBlockMetadataRejectsEmptyStorageClassBeforeInsert(t *testing.T) {
	oldPlainInsert := upsertBlockMetadataInsertFn
	oldRepresentationInsert := upsertBlockMetadataInsertWithRepresentationFn
	t.Cleanup(func() {
		upsertBlockMetadataInsertFn = oldPlainInsert
		upsertBlockMetadataInsertWithRepresentationFn = oldRepresentationInsert
	})
	upsertBlockMetadataInsertFn = func(*DB, string, string, string, int, string, string, time.Time) (bool, error) {
		t.Fatal("plain insert must not run with an empty storage class")
		return false, nil
	}
	upsertBlockMetadataInsertWithRepresentationFn = func(*DB, string, string, string, string, int, string, string, time.Time) (bool, error) {
		t.Fatal("representation insert must not run with an empty storage class")
		return false, nil
	}

	if err := (&DB{}).UpsertBlockMetadata("org-1", "block-1", 1, "   ", "key"); err == nil {
		t.Fatal("UpsertBlockMetadata() error = nil, want empty storage class error")
	}
}

func TestUpsertBlockMetadataRejectsEmptyRepresentationBeforeInsert(t *testing.T) {
	oldInsert := upsertBlockMetadataInsertWithRepresentationFn
	t.Cleanup(func() {
		upsertBlockMetadataInsertWithRepresentationFn = oldInsert
	})
	upsertBlockMetadataInsertWithRepresentationFn = func(*DB, string, string, string, string, int, string, string, time.Time) (bool, error) {
		t.Fatal("representation insert must not run with an empty representation id")
		return false, nil
	}

	err := (&DB{}).UpsertBlockMetadataWithRepresentationAndSHA1("org-1", "", "block-1", "", 1, "hot", "key")
	if err == nil || !strings.Contains(err.Error(), "missing block representation id") {
		t.Fatalf("UpsertBlockMetadataWithRepresentationAndSHA1() error = %v, want missing representation error", err)
	}
}

// R23a: the persisted storage_class is the physical namespace identity, so the
// write funnel is where a non-canonical value has to be stopped. Normalizing it
// would store an identity the writer never named; every reader resolves the raw
// stored value and would fail on it later, far from the cause.
func TestUpsertBlockMetadataRejectsNonCanonicalStorageClass(t *testing.T) {
	oldInsert := upsertBlockMetadataInsertWithRepresentationFn
	t.Cleanup(func() {
		upsertBlockMetadataInsertWithRepresentationFn = oldInsert
	})
	upsertBlockMetadataInsertWithRepresentationFn = func(*DB, string, string, string, string, int, string, string, time.Time) (bool, error) {
		t.Fatal("insert must not run with a non-canonical storage class")
		return false, nil
	}

	for _, storageClass := range []string{" hot", "hot ", "Hot", "hot_tier", "hot--tier", "   "} {
		err := (&DB{}).UpsertBlockMetadataWithRepresentationAndSHA1("org-1", PlainBlockRepresentationID, "block-1", "", 1, storageClass, "key")
		if err == nil || !strings.Contains(err.Error(), "non-canonical storage class") {
			t.Fatalf("UpsertBlockMetadataWithRepresentationAndSHA1(%q) error = %v, want non-canonical rejection", storageClass, err)
		}
		if !errors.Is(err, ErrBlockMetadataPermanent) {
			t.Fatalf("UpsertBlockMetadataWithRepresentationAndSHA1(%q) error is not permanent: %v", storageClass, err)
		}
	}
}

// Validating only the incoming argument leaves the case that actually matters
// uncovered: when the INSERT loses, the caller inherits whatever identity the
// existing row already carries. Reporting success there would finish an upload
// whose metadata ProbeBlockReuse and GC both refuse to resolve.
func TestUpsertBlockMetadataRejectsNonCanonicalStorageClassOnTheExistingRow(t *testing.T) {
	oldInsert := upsertBlockMetadataInsertFn
	oldRead := readBlockIdentityForRepairFn
	oldBackfill := backfillBlockSHA1Fn
	t.Cleanup(func() {
		upsertBlockMetadataInsertFn = oldInsert
		readBlockIdentityForRepairFn = oldRead
		backfillBlockSHA1Fn = oldBackfill
	})

	backfillBlockSHA1Fn = func(*DB, string, string, string, string) (bool, error) {
		t.Fatal("must not repair identity on a row whose storage class cannot name a namespace")
		return false, nil
	}

	for _, storedClass := range []string{" hot", "hot ", "Hot", "hot_tier", "   "} {
		upsertBlockMetadataInsertFn = func(*DB, string, string, string, int, string, string, time.Time) (bool, error) {
			return false, nil // the row already exists; converge on it
		}
		readBlockIdentityForRepairFn = func(*DB, string, string) (blockIdentityRepairRow, bool, error) {
			row := completeIdentityRepairRow(PlainBlockRepresentationID, "")
			row.StorageClass = storedClass
			return row, true, nil
		}

		// The ARGUMENT is canonical here; only the stored row is not.
		err := (&DB{}).UpsertBlockMetadataWithSHA1("org-1", "block-1", strings.Repeat("a", 40), 123, "hot", "key")
		if err == nil {
			t.Fatalf("stored class %q: error = nil, want rejection", storedClass)
		}
		if !strings.Contains(err.Error(), "non-canonical storage class") {
			t.Fatalf("stored class %q: error = %v, want non-canonical rejection", storedClass, err)
		}
		if !errors.Is(err, ErrBlockMetadataPermanent) {
			t.Fatalf("stored class %q: error is not permanent (a corrupt label never converges): %v", storedClass, err)
		}
	}
}

// The persisted storage_key is what GC hands to S3 and what reads resolve, so a
// row must never be created without one. Empty is refused BEFORE the insert, not
// normalized: a row minted here with no locator would have to be repaired by
// deriving a key later, which is exactly the derivation authority P1 removed.
func TestUpsertBlockMetadataRejectsEmptyStorageKeyBeforeInsert(t *testing.T) {
	oldPlainInsert := upsertBlockMetadataInsertFn
	oldRepresentationInsert := upsertBlockMetadataInsertWithRepresentationFn
	t.Cleanup(func() {
		upsertBlockMetadataInsertFn = oldPlainInsert
		upsertBlockMetadataInsertWithRepresentationFn = oldRepresentationInsert
	})
	upsertBlockMetadataInsertFn = func(*DB, string, string, string, int, string, string, time.Time) (bool, error) {
		t.Fatal("plain insert must not run without a canonical storage key")
		return false, nil
	}
	upsertBlockMetadataInsertWithRepresentationFn = func(*DB, string, string, string, string, int, string, string, time.Time) (bool, error) {
		t.Fatal("representation insert must not run without a canonical storage key")
		return false, nil
	}

	for _, storageKey := range []string{"", "   ", " key", "key "} {
		err := (&DB{}).UpsertBlockMetadata("org-1", "block-1", 1, "hot", storageKey)
		if err == nil || !strings.Contains(err.Error(), "missing canonical storage key") {
			t.Fatalf("UpsertBlockMetadata(%q) error = %v, want missing storage key error", storageKey, err)
		}
		if !errors.Is(err, ErrBlockMetadataPermanent) {
			t.Fatalf("UpsertBlockMetadata(%q) error is not permanent: %v", storageKey, err)
		}
	}
}

// Losing the INSERT means inheriting the existing row's locator. A row with no
// locator cannot be inherited into a successful upload: reads and GC would both
// refuse it later, far from the cause.
func TestUpsertBlockMetadataRejectsEmptyStorageKeyOnTheExistingRow(t *testing.T) {
	oldInsert := upsertBlockMetadataInsertFn
	oldRead := readBlockIdentityForRepairFn
	oldBackfill := backfillBlockSHA1Fn
	t.Cleanup(func() {
		upsertBlockMetadataInsertFn = oldInsert
		readBlockIdentityForRepairFn = oldRead
		backfillBlockSHA1Fn = oldBackfill
	})
	backfillBlockSHA1Fn = func(*DB, string, string, string, string) (bool, error) {
		t.Fatal("must not repair identity on a row with no canonical locator")
		return false, nil
	}
	upsertBlockMetadataInsertFn = func(*DB, string, string, string, int, string, string, time.Time) (bool, error) {
		return false, nil // the row already exists; converge on it
	}
	readBlockIdentityForRepairFn = func(*DB, string, string) (blockIdentityRepairRow, bool, error) {
		row := completeIdentityRepairRow(PlainBlockRepresentationID, "")
		row.StorageKey = " key "
		return row, true, nil
	}

	err := (&DB{}).UpsertBlockMetadataWithSHA1("org-1", "block-1", strings.Repeat("a", 40), 123, "hot", "key")
	if err == nil || !strings.Contains(err.Error(), "empty canonical storage key") {
		t.Fatalf("UpsertBlockMetadataWithSHA1() error = %v, want empty storage key rejection", err)
	}
	if !errors.Is(err, ErrBlockMetadataPermanent) {
		t.Fatalf("error is not permanent: %v", err)
	}
}

// Two writers proposing different locators for the same content is a placement
// conflict, not a race that converges: one of them PUT bytes somewhere the
// canonical row does not name. Fail permanently rather than report success for a
// locator the row never adopted.
func TestUpsertBlockMetadataRejectsConflictingStorageKeyOnTheExistingRow(t *testing.T) {
	oldInsert := upsertBlockMetadataInsertFn
	oldRead := readBlockIdentityForRepairFn
	oldBackfill := backfillBlockSHA1Fn
	t.Cleanup(func() {
		upsertBlockMetadataInsertFn = oldInsert
		readBlockIdentityForRepairFn = oldRead
		backfillBlockSHA1Fn = oldBackfill
	})
	backfillBlockSHA1Fn = func(*DB, string, string, string, string) (bool, error) {
		t.Fatal("must not repair identity on a row whose locator disagrees with the writer")
		return false, nil
	}
	upsertBlockMetadataInsertFn = func(*DB, string, string, string, int, string, string, time.Time) (bool, error) {
		return false, nil
	}
	readBlockIdentityForRepairFn = func(*DB, string, string) (blockIdentityRepairRow, bool, error) {
		row := completeIdentityRepairRow(PlainBlockRepresentationID, "")
		row.StorageKey = "blocks/org-1/ab/cd/abcd1234"
		return row, true, nil
	}

	err := (&DB{}).UpsertBlockMetadataWithSHA1("org-1", "block-1", strings.Repeat("a", 40), 123, "hot", "blocks/org-2/ab/cd/abcd1234")
	if err == nil || !strings.Contains(err.Error(), "conflicting storage key") {
		t.Fatalf("UpsertBlockMetadataWithSHA1() error = %v, want conflicting storage key rejection", err)
	}
	if !errors.Is(err, ErrBlockMetadataPermanent) {
		t.Fatalf("error is not permanent: %v", err)
	}
}

func TestUpsertBlockMetadataRepairsReleasedStubAndRetriesInsert(t *testing.T) {
	database := &DB{}
	oldInsert := upsertBlockMetadataInsertFn
	oldRead := readBlockIdentityForRepairFn
	oldRepair := repairReleasedBlockStubForUpsertFn
	t.Cleanup(func() {
		upsertBlockMetadataInsertFn = oldInsert
		readBlockIdentityForRepairFn = oldRead
		repairReleasedBlockStubForUpsertFn = oldRepair
	})

	insertCalls := 0
	upsertBlockMetadataInsertFn = func(*DB, string, string, string, int, string, string, time.Time) (bool, error) {
		insertCalls++
		return insertCalls == 2, nil
	}
	readBlockIdentityForRepairFn = func(*DB, string, string) (blockIdentityRepairRow, bool, error) {
		return blockIdentityRepairRow{RepresentationID: PlainBlockRepresentationID, Sha1: strings.Repeat("a", 40)}, true, nil
	}
	repairCalls := 0
	repairReleasedBlockStubForUpsertFn = func(*DB, string, string) (bool, error) {
		repairCalls++
		return true, nil
	}

	if err := database.UpsertBlockMetadataWithSHA1("org-1", "block-1", strings.Repeat("a", 40), 1, "hot", "key"); err != nil {
		t.Fatalf("UpsertBlockMetadataWithSHA1() error = %v, want nil", err)
	}
	if insertCalls != 2 || repairCalls != 1 {
		t.Fatalf("insert/repair calls = %d/%d, want 2/1", insertCalls, repairCalls)
	}
}

func TestUpsertRepresentationMetadataRepairsReleasedStubAndRetriesInsert(t *testing.T) {
	database := &DB{}
	oldInsert := upsertBlockMetadataInsertWithRepresentationFn
	oldRead := readBlockIdentityForRepairFn
	oldRepair := repairReleasedBlockStubForUpsertFn
	t.Cleanup(func() {
		upsertBlockMetadataInsertWithRepresentationFn = oldInsert
		readBlockIdentityForRepairFn = oldRead
		repairReleasedBlockStubForUpsertFn = oldRepair
	})
	representationID := EncryptedLibraryBlockRepresentationID("library-1")
	insertCalls := 0
	upsertBlockMetadataInsertWithRepresentationFn = func(*DB, string, string, string, string, int, string, string, time.Time) (bool, error) {
		insertCalls++
		return insertCalls == 2, nil
	}
	readBlockIdentityForRepairFn = func(*DB, string, string) (blockIdentityRepairRow, bool, error) {
		return blockIdentityRepairRow{RepresentationID: representationID}, true, nil
	}
	repairCalls := 0
	repairReleasedBlockStubForUpsertFn = func(*DB, string, string) (bool, error) {
		repairCalls++
		return true, nil
	}

	if err := database.UpsertBlockMetadataWithRepresentationAndSHA1("org-1", representationID, "block-1", "", 1, "hot", "key"); err != nil {
		t.Fatalf("UpsertBlockMetadataWithRepresentationAndSHA1() error = %v", err)
	}
	if insertCalls != 2 || repairCalls != 1 {
		t.Fatalf("insert/repair calls = %d/%d, want 2/1", insertCalls, repairCalls)
	}
}

func TestUpsertBlockMetadataStopsWhenReleasedStubDeleteLosesRace(t *testing.T) {
	database := &DB{}
	oldInsert := upsertBlockMetadataInsertFn
	oldRead := readBlockIdentityForRepairFn
	oldRepair := repairReleasedBlockStubForUpsertFn
	t.Cleanup(func() {
		upsertBlockMetadataInsertFn = oldInsert
		readBlockIdentityForRepairFn = oldRead
		repairReleasedBlockStubForUpsertFn = oldRepair
	})

	insertCalls := 0
	upsertBlockMetadataInsertFn = func(*DB, string, string, string, int, string, string, time.Time) (bool, error) {
		insertCalls++
		return false, nil
	}
	readBlockIdentityForRepairFn = func(*DB, string, string) (blockIdentityRepairRow, bool, error) {
		return blockIdentityRepairRow{}, true, nil
	}
	repairReleasedBlockStubForUpsertFn = func(*DB, string, string) (bool, error) { return false, nil }

	err := database.UpsertBlockMetadata("org-1", "block-1", 1, "hot", "key")
	if !errors.Is(err, ErrBlockStubRepairContended) {
		t.Fatalf("UpsertBlockMetadata() error = %v, want ErrBlockStubRepairContended (retryable)", err)
	}
	if insertCalls != 1 {
		t.Fatalf("insertCalls = %d, want 1", insertCalls)
	}
}

func TestRepairReleasedBlockStubClaimsRechecksOrphanAndDeletes(t *testing.T) {
	oldClaim := claimReleasedBlockStubForRepairFn
	oldDelete := deleteRepairClaimedBlockStubFn
	oldOrphan := probeBlockReuseHasS3OrphanFn
	oldID := blockStubRepairIDFn
	t.Cleanup(func() {
		claimReleasedBlockStubForRepairFn = oldClaim
		deleteRepairClaimedBlockStubFn = oldDelete
		probeBlockReuseHasS3OrphanFn = oldOrphan
		blockStubRepairIDFn = oldID
	})
	blockStubRepairIDFn = func(string, string) string { return "repair-1" }
	claimReleasedBlockStubForRepairFn = func(_ *DB, orgID, blockID, repairID string, claimedAt time.Time) (bool, error) {
		if orgID != "org-1" || blockID != "block-1" || repairID != "repair-1" || claimedAt.IsZero() {
			t.Fatalf("claim args = %s/%s/%s/%v", orgID, blockID, repairID, claimedAt)
		}
		return true, nil
	}
	probeBlockReuseHasS3OrphanFn = func(*DB, string, string) (bool, error) { return false, nil }
	deleteRepairClaimedBlockStubFn = func(_ *DB, orgID, blockID, repairID string) (bool, error) {
		if repairID != "repair-1" {
			t.Fatalf("repairID = %q", repairID)
		}
		return true, nil
	}

	repaired, err := (&DB{}).RepairReleasedBlockStub("org-1", "block-1")
	if err != nil || !repaired {
		t.Fatalf("RepairReleasedBlockStub() = %v, %v, want true/nil", repaired, err)
	}
}

func TestRepairReleasedBlockStubStopsWhenOrphanFenceAppears(t *testing.T) {
	oldClaim := claimReleasedBlockStubForRepairFn
	oldDelete := deleteRepairClaimedBlockStubFn
	oldOrphan := probeBlockReuseHasS3OrphanFn
	t.Cleanup(func() {
		claimReleasedBlockStubForRepairFn = oldClaim
		deleteRepairClaimedBlockStubFn = oldDelete
		probeBlockReuseHasS3OrphanFn = oldOrphan
	})
	claimReleasedBlockStubForRepairFn = func(*DB, string, string, string, time.Time) (bool, error) { return true, nil }
	probeBlockReuseHasS3OrphanFn = func(*DB, string, string) (bool, error) { return true, nil }
	deleteCalls := 0
	deleteRepairClaimedBlockStubFn = func(*DB, string, string, string) (bool, error) {
		deleteCalls++
		return true, nil
	}

	repaired, err := (&DB{}).RepairReleasedBlockStub("org-1", "block-1")
	if err != nil || repaired {
		t.Fatalf("RepairReleasedBlockStub() = %v, %v, want false/nil", repaired, err)
	}
	if deleteCalls != 1 {
		t.Fatalf("deleteCalls = %d, want repair claim cleanup", deleteCalls)
	}
}

func TestRepairReleasedBlockStubResumesAmbiguousClaim(t *testing.T) {
	oldClaim := claimReleasedBlockStubForRepairFn
	oldDelete := deleteRepairClaimedBlockStubFn
	oldOrphan := probeBlockReuseHasS3OrphanFn
	oldRead := readBlockIdentityForRepairFn
	t.Cleanup(func() {
		claimReleasedBlockStubForRepairFn = oldClaim
		deleteRepairClaimedBlockStubFn = oldDelete
		probeBlockReuseHasS3OrphanFn = oldOrphan
		readBlockIdentityForRepairFn = oldRead
	})
	claimedAt := time.Now().UTC()
	repairID := blockStubRepairIDFn("org-1", "block-1")
	claimReleasedBlockStubForRepairFn = func(*DB, string, string, string, time.Time) (bool, error) {
		return false, errors.New("ambiguous timeout")
	}
	readBlockIdentityForRepairFn = func(*DB, string, string) (blockIdentityRepairRow, bool, error) {
		return blockIdentityRepairRow{GCState: BlockGCStateRepairingStub, GCClaimID: repairID, GCClaimedAt: &claimedAt}, true, nil
	}
	probeBlockReuseHasS3OrphanFn = func(*DB, string, string) (bool, error) { return false, nil }
	deleteRepairClaimedBlockStubFn = func(*DB, string, string, string) (bool, error) { return true, nil }

	repaired, err := (&DB{}).RepairReleasedBlockStub("org-1", "block-1")
	if err != nil || !repaired {
		t.Fatalf("RepairReleasedBlockStub() = %v, %v, want true/nil", repaired, err)
	}
}

func TestRepairReleasedBlockStubRetriesAmbiguousDelete(t *testing.T) {
	oldClaim := claimReleasedBlockStubForRepairFn
	oldDelete := deleteRepairClaimedBlockStubFn
	oldOrphan := probeBlockReuseHasS3OrphanFn
	oldRead := readBlockIdentityForRepairFn
	t.Cleanup(func() {
		claimReleasedBlockStubForRepairFn = oldClaim
		deleteRepairClaimedBlockStubFn = oldDelete
		probeBlockReuseHasS3OrphanFn = oldOrphan
		readBlockIdentityForRepairFn = oldRead
	})
	claimedAt := time.Now().UTC()
	repairID := blockStubRepairIDFn("org-1", "block-1")
	claimReleasedBlockStubForRepairFn = func(*DB, string, string, string, time.Time) (bool, error) { return true, nil }
	readBlockIdentityForRepairFn = func(*DB, string, string) (blockIdentityRepairRow, bool, error) {
		return blockIdentityRepairRow{GCState: BlockGCStateRepairingStub, GCClaimID: repairID, GCClaimedAt: &claimedAt}, true, nil
	}
	probeBlockReuseHasS3OrphanFn = func(*DB, string, string) (bool, error) { return false, nil }
	deleteCalls := 0
	deleteRepairClaimedBlockStubFn = func(*DB, string, string, string) (bool, error) {
		deleteCalls++
		if deleteCalls == 1 {
			return false, errors.New("ambiguous timeout")
		}
		return true, nil
	}

	repaired, err := (&DB{}).RepairReleasedBlockStub("org-1", "block-1")
	if err != nil || !repaired || deleteCalls != 2 {
		t.Fatalf("RepairReleasedBlockStub() = %v, %v with %d deletes, want true/nil with 2", repaired, err, deleteCalls)
	}
}

func TestRepairReleasedBlockStubDoesNotSucceedWhenClaimedRowChanges(t *testing.T) {
	oldClaim := claimReleasedBlockStubForRepairFn
	oldDelete := deleteRepairClaimedBlockStubFn
	oldOrphan := probeBlockReuseHasS3OrphanFn
	oldRead := readBlockIdentityForRepairFn
	t.Cleanup(func() {
		claimReleasedBlockStubForRepairFn = oldClaim
		deleteRepairClaimedBlockStubFn = oldDelete
		probeBlockReuseHasS3OrphanFn = oldOrphan
		readBlockIdentityForRepairFn = oldRead
	})
	claimReleasedBlockStubForRepairFn = func(*DB, string, string, string, time.Time) (bool, error) { return true, nil }
	probeBlockReuseHasS3OrphanFn = func(*DB, string, string) (bool, error) { return false, nil }
	deleteRepairClaimedBlockStubFn = func(*DB, string, string, string) (bool, error) { return false, nil }
	readBlockIdentityForRepairFn = func(*DB, string, string) (blockIdentityRepairRow, bool, error) {
		return completeIdentityRepairRow(PlainBlockRepresentationID, strings.Repeat("a", 40)), true, nil
	}

	// The row was completed by another actor before our conditional delete. That is
	// a benign lost race, so the repair reports (false, nil) — retryable, not a hard
	// error — and the caller re-probes to converge on Reusable/BlockedByGC.
	repaired, err := (&DB{}).RepairReleasedBlockStub("org-1", "block-1")
	if err != nil || repaired {
		t.Fatalf("RepairReleasedBlockStub() = %v, %v, want false/nil (retryable lost race)", repaired, err)
	}
}

func TestWriteBlockIDMapping_RejectsMalformedIDs(t *testing.T) {
	database := &DB{}
	oldGet := getBlockIDMappingForWriteCheckFn
	oldInsert := insertBlockIDMappingForWriteCheckFn
	t.Cleanup(func() {
		getBlockIDMappingForWriteCheckFn = oldGet
		insertBlockIDMappingForWriteCheckFn = oldInsert
	})

	getBlockIDMappingForWriteCheckFn = func(database *DB, orgID, representationID, externalID string) (string, bool, error) {
		t.Fatal("existing mapping lookup should not run for malformed IDs")
		return "", false, nil
	}
	insertBlockIDMappingForWriteCheckFn = func(database *DB, orgID, representationID, externalID, internalID string, createdAt time.Time) error {
		t.Fatal("insert should not run for malformed IDs")
		return nil
	}

	err := database.WriteBlockIDMapping("org-1", PlainBlockRepresentationID, "not-a-sha1", strings.Repeat("a", 64), time.Time{})
	if err == nil || !strings.Contains(err.Error(), "invalid external block id") {
		t.Fatalf("WriteBlockIDMapping() external error = %v, want invalid external block id", err)
	}

	err = database.WriteBlockIDMapping("org-1", PlainBlockRepresentationID, strings.Repeat("b", 40), "not-a-sha256", time.Time{})
	if err == nil || !strings.Contains(err.Error(), "invalid internal block id") {
		t.Fatalf("WriteBlockIDMapping() internal error = %v, want invalid internal block id", err)
	}
}

func TestWriteBlockIDMapping_RejectsSameDomainRemap(t *testing.T) {
	database := &DB{}
	oldGet := getBlockIDMappingForWriteCheckFn
	oldInsert := insertBlockIDMappingForWriteCheckFn
	t.Cleanup(func() {
		getBlockIDMappingForWriteCheckFn = oldGet
		insertBlockIDMappingForWriteCheckFn = oldInsert
	})

	getBlockIDMappingForWriteCheckFn = func(database *DB, orgID, representationID, externalID string) (string, bool, error) {
		if orgID != "org-1" || representationID != PlainBlockRepresentationID || externalID != strings.Repeat("b", 40) {
			t.Fatalf("lookup args = %s/%s/%s", orgID, representationID, externalID)
		}
		return strings.Repeat("a", 64), true, nil
	}
	insertBlockIDMappingForWriteCheckFn = func(database *DB, orgID, representationID, externalID, internalID string, createdAt time.Time) error {
		t.Fatal("insert should not run when an existing conflicting mapping is present")
		return nil
	}

	err := database.WriteBlockIDMapping("org-1", PlainBlockRepresentationID, strings.Repeat("b", 40), strings.Repeat("c", 64), time.Time{})
	if !errors.Is(err, ErrBlockIDMappingConflict) {
		t.Fatalf("WriteBlockIDMapping() error = %v, want ErrBlockIDMappingConflict", err)
	}
}

func TestNormalizeBlockID(t *testing.T) {
	cases := map[string]string{
		"":                      "",
		"  ":                    "",
		"  ABCDEF  ":            "abcdef",
		"DeadBeef":              "deadbeef",
		strings.Repeat("A", 40): strings.Repeat("a", 40),
		strings.Repeat("f", 64): strings.Repeat("f", 64),
	}
	for in, want := range cases {
		if got := NormalizeBlockID(in); got != want {
			t.Fatalf("NormalizeBlockID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWriteBlockIDMapping_CanonicalizesHashesToLowercase(t *testing.T) {
	database := &DB{}
	oldGet := getBlockIDMappingForWriteCheckFn
	oldInsert := insertBlockIDMappingForWriteCheckFn
	t.Cleanup(func() {
		getBlockIDMappingForWriteCheckFn = oldGet
		insertBlockIDMappingForWriteCheckFn = oldInsert
	})

	getCalled := false
	insertCalled := false
	getBlockIDMappingForWriteCheckFn = func(database *DB, orgID, representationID, externalID string) (string, bool, error) {
		getCalled = true
		if externalID != strings.Repeat("a", 40) {
			t.Fatalf("externalID = %q, want lowercase", externalID)
		}
		return "", false, nil
	}
	insertBlockIDMappingForWriteCheckFn = func(database *DB, orgID, representationID, externalID, internalID string, createdAt time.Time) error {
		insertCalled = true
		if externalID != strings.Repeat("a", 40) {
			t.Fatalf("insert externalID = %q, want lowercase", externalID)
		}
		if internalID != strings.Repeat("b", 64) {
			t.Fatalf("insert internalID = %q, want lowercase", internalID)
		}
		return nil
	}

	err := database.WriteBlockIDMapping("org-1", PlainBlockRepresentationID, strings.Repeat("A", 40), strings.Repeat("B", 64), time.Time{})
	if err != nil {
		t.Fatalf("WriteBlockIDMapping() error = %v, want nil", err)
	}
	if !getCalled || !insertCalled {
		t.Fatalf("expected canonicalized lookup+insert path, got lookup=%v insert=%v", getCalled, insertCalled)
	}
}

func TestEnsureBlockIdentity_PlaintextBackfillsMissingRepresentationID(t *testing.T) {
	database := &DB{}
	oldRead := readBlockIdentityForRepairFn
	oldBackfillRepresentation := backfillBlockRepresentationIDFn
	oldBackfillSHA1 := backfillBlockSHA1Fn
	t.Cleanup(func() {
		readBlockIdentityForRepairFn = oldRead
		backfillBlockRepresentationIDFn = oldBackfillRepresentation
		backfillBlockSHA1Fn = oldBackfillSHA1
	})

	readBlockIdentityForRepairFn = func(database *DB, orgID, blockID string) (blockIdentityRepairRow, bool, error) {
		return completeIdentityRepairRow("", strings.Repeat("a", 40)), true, nil
	}
	backfillBlockRepresentationIDFn = func(database *DB, orgID, blockID, representationID, expectedCurrent string) (bool, error) {
		if representationID != PlainBlockRepresentationID {
			t.Fatalf("representationID = %q, want %q", representationID, PlainBlockRepresentationID)
		}
		if expectedCurrent != "" {
			t.Fatalf("expectedCurrent = %q, want empty", expectedCurrent)
		}
		return true, nil
	}
	backfillBlockSHA1Fn = func(database *DB, orgID, blockID, sha1, expectedCurrent string) (bool, error) {
		t.Fatal("sha1 backfill should not run when the stored sha1 already matches")
		return false, nil
	}

	if err := database.ensureBlockIdentity("org-1", "block-1", PlainBlockRepresentationID, strings.Repeat("a", 40)); err != nil {
		t.Fatalf("ensureBlockIdentity() error = %v, want nil", err)
	}
}

func TestEnsureBlockIdentity_PlaintextRejectsStoredRepresentationConflict(t *testing.T) {
	database := &DB{}
	oldRead := readBlockIdentityForRepairFn
	oldBackfillRepresentation := backfillBlockRepresentationIDFn
	oldBackfillSHA1 := backfillBlockSHA1Fn
	t.Cleanup(func() {
		readBlockIdentityForRepairFn = oldRead
		backfillBlockRepresentationIDFn = oldBackfillRepresentation
		backfillBlockSHA1Fn = oldBackfillSHA1
	})

	readBlockIdentityForRepairFn = func(database *DB, orgID, blockID string) (blockIdentityRepairRow, bool, error) {
		return completeIdentityRepairRow(EncryptedLibraryBlockRepresentationID("library-1"), strings.Repeat("a", 40)), true, nil
	}
	backfillBlockRepresentationIDFn = func(database *DB, orgID, blockID, representationID, expectedCurrent string) (bool, error) {
		t.Fatal("representation backfill should not run on a stored conflict")
		return false, nil
	}
	backfillBlockSHA1Fn = func(database *DB, orgID, blockID, sha1, expectedCurrent string) (bool, error) {
		t.Fatal("sha1 backfill should not run on a stored conflict")
		return false, nil
	}

	err := database.ensureBlockIdentity("org-1", "block-1", PlainBlockRepresentationID, strings.Repeat("a", 40))
	if err == nil || !strings.Contains(err.Error(), "conflicting representation id") {
		t.Fatalf("ensureBlockIdentity() error = %v, want conflicting representation id", err)
	}
}

func TestEnsureBlockIdentity_DoesNotBackfillRepresentationWhenSHA1Conflicts(t *testing.T) {
	database := &DB{}
	oldRead := readBlockIdentityForRepairFn
	oldBackfillRepresentation := backfillBlockRepresentationIDFn
	oldBackfillSHA1 := backfillBlockSHA1Fn
	t.Cleanup(func() {
		readBlockIdentityForRepairFn = oldRead
		backfillBlockRepresentationIDFn = oldBackfillRepresentation
		backfillBlockSHA1Fn = oldBackfillSHA1
	})

	readBlockIdentityForRepairFn = func(database *DB, orgID, blockID string) (blockIdentityRepairRow, bool, error) {
		return completeIdentityRepairRow("", strings.Repeat("d", 40)), true, nil
	}
	backfillBlockRepresentationIDFn = func(database *DB, orgID, blockID, representationID, expectedCurrent string) (bool, error) {
		t.Fatal("representation backfill should not run when the stored sha1 already conflicts")
		return false, nil
	}
	backfillBlockSHA1Fn = func(database *DB, orgID, blockID, sha1, expectedCurrent string) (bool, error) {
		t.Fatal("sha1 backfill should not run when the stored sha1 already conflicts")
		return false, nil
	}

	err := database.ensureBlockIdentity("org-1", "block-1", PlainBlockRepresentationID, strings.Repeat("e", 40))
	if err == nil || !strings.Contains(err.Error(), "conflicting sha1") {
		t.Fatalf("ensureBlockIdentity() error = %v, want conflicting sha1", err)
	}
}

func TestUpsertBlockMetadataClassifiesPermanentFailures(t *testing.T) {
	// Entry-level invariant violations never reach the driver: they are
	// deterministically irrecoverable and must carry ErrBlockMetadataPermanent so
	// the upload materialization wrapper refuses to retry them.
	t.Run("empty storage class is permanent", func(t *testing.T) {
		err := (&DB{}).UpsertBlockMetadata("org-1", "block-1", 1, "   ", "key")
		if !errors.Is(err, ErrBlockMetadataPermanent) {
			t.Fatalf("error = %v, want ErrBlockMetadataPermanent", err)
		}
	})
	t.Run("invalid sha1 is permanent", func(t *testing.T) {
		err := (&DB{}).UpsertBlockMetadataWithSHA1("org-1", "block-1", "not-a-sha1", 1, "hot", "key")
		if !errors.Is(err, ErrBlockMetadataPermanent) {
			t.Fatalf("error = %v, want ErrBlockMetadataPermanent", err)
		}
	})

	oldRead := readBlockIdentityForRepairFn
	t.Cleanup(func() { readBlockIdentityForRepairFn = oldRead })

	t.Run("conflicting representation id is permanent", func(t *testing.T) {
		readBlockIdentityForRepairFn = func(*DB, string, string) (blockIdentityRepairRow, bool, error) {
			return completeIdentityRepairRow(EncryptedLibraryBlockRepresentationID("library-1"), strings.Repeat("a", 40)), true, nil
		}
		err := (&DB{}).ensureBlockIdentity("org-1", "block-1", PlainBlockRepresentationID, strings.Repeat("a", 40))
		if !errors.Is(err, ErrBlockMetadataPermanent) {
			t.Fatalf("error = %v, want ErrBlockMetadataPermanent", err)
		}
	})
	t.Run("conflicting sha1 is permanent", func(t *testing.T) {
		readBlockIdentityForRepairFn = func(*DB, string, string) (blockIdentityRepairRow, bool, error) {
			return completeIdentityRepairRow("", strings.Repeat("d", 40)), true, nil
		}
		err := (&DB{}).ensureBlockIdentity("org-1", "block-1", PlainBlockRepresentationID, strings.Repeat("e", 40))
		if !errors.Is(err, ErrBlockMetadataPermanent) {
			t.Fatalf("error = %v, want ErrBlockMetadataPermanent", err)
		}
	})
	t.Run("malformed GC ownership is permanent", func(t *testing.T) {
		createdAt := time.Now().UTC()
		readBlockIdentityForRepairFn = func(*DB, string, string) (blockIdentityRepairRow, bool, error) {
			// Claim identity present without a lifecycle state == row corruption.
			return blockIdentityRepairRow{
				RepresentationID:    PlainBlockRepresentationID,
				StorageClass:        "hot",
				StorageClassPresent: true,
				CreatedAt:           &createdAt,
				GCClaimID:           "claim-1",
			}, true, nil
		}
		err := (&DB{}).ensureBlockIdentity("org-1", "block-1", PlainBlockRepresentationID, "")
		if !errors.Is(err, ErrBlockMetadataPermanent) {
			t.Fatalf("error = %v, want ErrBlockMetadataPermanent", err)
		}
	})
}

func TestUpsertBlockMetadataLeavesTransientFailuresUnmarked(t *testing.T) {
	oldInsert := upsertBlockMetadataInsertFn
	oldRead := readBlockIdentityForRepairFn
	t.Cleanup(func() {
		upsertBlockMetadataInsertFn = oldInsert
		readBlockIdentityForRepairFn = oldRead
	})

	t.Run("raw driver insert error is transient", func(t *testing.T) {
		wantErr := errors.New("cassandra timeout")
		upsertBlockMetadataInsertFn = func(*DB, string, string, string, int, string, string, time.Time) (bool, error) {
			return false, wantErr
		}
		err := (&DB{}).UpsertBlockMetadata("org-1", "block-1", 1, "hot", "key")
		if !errors.Is(err, wantErr) {
			t.Fatalf("error = %v, want wrapped %v", err, wantErr)
		}
		if errors.Is(err, ErrBlockMetadataPermanent) {
			t.Fatalf("driver I/O error must not be classified permanent: %v", err)
		}
	})

	t.Run("incomplete metadata from an active claim is transient", func(t *testing.T) {
		claimedAt := time.Now().UTC()
		readBlockIdentityForRepairFn = func(*DB, string, string) (blockIdentityRepairRow, bool, error) {
			// An active GC delete claim (deleting) can converge on a re-probe, so
			// the caller must retry rather than fail closed.
			return blockIdentityRepairRow{
				GCState:     BlockGCStateDeleting,
				GCClaimID:   "claim-1",
				GCClaimedAt: &claimedAt,
			}, true, nil
		}
		err := (&DB{}).ensureBlockIdentity("org-1", "block-1", PlainBlockRepresentationID, "")
		if err == nil {
			t.Fatal("ensureBlockIdentity() error = nil, want incomplete metadata error")
		}
		if errors.Is(err, ErrBlockMetadataPermanent) {
			t.Fatalf("incomplete-metadata (active claim) must not be permanent: %v", err)
		}
	})
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
		row := completeProbeMetadataRow("hot-s3")
		row.Sha1 = "sha1-abc"
		row.SizeBytes = 123
		return row, true, nil
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
		row := completeProbeMetadataRow("")
		row.SizeBytes = 123
		return row, true, nil
	}

	probe, err := (&DB{}).ProbeBlockReuse("org-1", "block-1")
	if err == nil {
		t.Fatal("ProbeBlockReuse() error = nil, want error")
	}
	if probe.Decision != BlockReuseUnknownError {
		t.Fatalf("decision = %v, want BlockReuseUnknownError", probe.Decision)
	}
}

// A stored class that only looks empty after normalization is corruption, not an
// absent class: the probe must refuse it instead of resolving a trimmed copy.
func TestProbeBlockReuseReturnsUnknownErrorForNonCanonicalStorageClass(t *testing.T) {
	oldMetadata := probeBlockReuseMetadataFn
	t.Cleanup(func() { probeBlockReuseMetadataFn = oldMetadata })

	for _, storageClass := range []string{"   ", " hot-s3-eu", "Hot-S3-EU", "hot_s3_eu"} {
		probeBlockReuseMetadataFn = func(database *DB, orgID, blockID string) (blockReuseMetadataRow, bool, error) {
			row := completeProbeMetadataRow(storageClass)
			row.SizeBytes = 123
			return row, true, nil
		}

		probe, err := (&DB{}).ProbeBlockReuse("org-1", "block-1")
		if err == nil {
			t.Fatalf("ProbeBlockReuse(%q) error = nil, want error", storageClass)
		}
		if probe.Decision != BlockReuseUnknownError {
			t.Fatalf("ProbeBlockReuse(%q) decision = %v, want BlockReuseUnknownError", storageClass, probe.Decision)
		}
	}
}

// The probe is what every reuse/repair path resolves through, so it is where a
// row with no locator has to stop. Returning Reusable here would send a caller to
// verify an object it cannot name, and NeedsPut would let it invent one.
func TestProbeBlockReuseReturnsUnknownErrorForEmptyStorageKey(t *testing.T) {
	oldMetadata := probeBlockReuseMetadataFn
	oldReferences := probeBlockReuseHasReferencesFn
	oldOrphan := probeBlockReuseHasS3OrphanFn
	t.Cleanup(func() {
		probeBlockReuseMetadataFn = oldMetadata
		probeBlockReuseHasReferencesFn = oldReferences
		probeBlockReuseHasS3OrphanFn = oldOrphan
	})
	probeBlockReuseHasReferencesFn = func(*DB, string, string) (bool, error) {
		t.Fatal("must not classify reuse for a row with no canonical locator")
		return false, nil
	}
	probeBlockReuseHasS3OrphanFn = func(*DB, string, string) (bool, error) {
		t.Fatal("must not read the orphan fence for a row with no canonical locator")
		return false, nil
	}

	for _, storageKey := range []string{"", "   ", "canonical-key ", " canonical-key"} {
		probeBlockReuseMetadataFn = func(database *DB, orgID, blockID string) (blockReuseMetadataRow, bool, error) {
			row := completeProbeMetadataRow("hot")
			row.SizeBytes = 123
			row.StorageKey = storageKey
			return row, true, nil
		}

		probe, err := (&DB{}).ProbeBlockReuse("org-1", "block-1")
		if err == nil || !strings.Contains(err.Error(), "empty canonical storage key") {
			t.Fatalf("ProbeBlockReuse(%q) error = %v, want empty storage key error", storageKey, err)
		}
		if probe.Decision != BlockReuseUnknownError {
			t.Fatalf("ProbeBlockReuse(%q) decision = %v, want BlockReuseUnknownError", storageKey, probe.Decision)
		}
	}
}

func TestProbeBlockReuseReturnsRepairableStubForReleasedClaimRow(t *testing.T) {
	oldMetadata := probeBlockReuseMetadataFn
	oldOrphan := probeBlockReuseHasS3OrphanFn
	t.Cleanup(func() {
		probeBlockReuseMetadataFn = oldMetadata
		probeBlockReuseHasS3OrphanFn = oldOrphan
	})
	probeBlockReuseMetadataFn = func(*DB, string, string) (blockReuseMetadataRow, bool, error) {
		return blockReuseMetadataRow{Sha1: strings.Repeat("a", 40)}, true, nil
	}
	probeBlockReuseHasS3OrphanFn = func(*DB, string, string) (bool, error) { return false, nil }

	probe, err := (&DB{}).ProbeBlockReuse("org-1", "block-1")
	if err != nil {
		t.Fatalf("ProbeBlockReuse() error = %v, want nil", err)
	}
	if probe.Decision != BlockReuseRepairableStub {
		t.Fatalf("decision = %v, want BlockReuseRepairableStub", probe.Decision)
	}
}

func TestProbeBlockReuseBlocksReleasedStubWithS3Orphan(t *testing.T) {
	oldMetadata := probeBlockReuseMetadataFn
	oldOrphan := probeBlockReuseHasS3OrphanFn
	t.Cleanup(func() {
		probeBlockReuseMetadataFn = oldMetadata
		probeBlockReuseHasS3OrphanFn = oldOrphan
	})
	probeBlockReuseMetadataFn = func(*DB, string, string) (blockReuseMetadataRow, bool, error) {
		return blockReuseMetadataRow{}, true, nil
	}
	probeBlockReuseHasS3OrphanFn = func(*DB, string, string) (bool, error) { return true, nil }

	probe, err := (&DB{}).ProbeBlockReuse("org-1", "block-1")
	if err != nil {
		t.Fatalf("ProbeBlockReuse() error = %v, want nil", err)
	}
	if probe.Decision != BlockReuseBlockedByGC {
		t.Fatalf("decision = %v, want BlockReuseBlockedByGC", probe.Decision)
	}
}

func TestProbeBlockReuseBlocksActivelyClaimedStub(t *testing.T) {
	oldMetadata := probeBlockReuseMetadataFn
	oldOrphan := probeBlockReuseHasS3OrphanFn
	t.Cleanup(func() {
		probeBlockReuseMetadataFn = oldMetadata
		probeBlockReuseHasS3OrphanFn = oldOrphan
	})
	claimedAt := time.Now().UTC()
	probeBlockReuseMetadataFn = func(*DB, string, string) (blockReuseMetadataRow, bool, error) {
		return blockReuseMetadataRow{GCState: BlockGCStateDeleting, GCClaimID: "claim-1", GCClaimedAt: &claimedAt}, true, nil
	}
	probeBlockReuseHasS3OrphanFn = func(*DB, string, string) (bool, error) {
		t.Fatal("orphan read must not run for an active claim")
		return false, nil
	}

	probe, err := (&DB{}).ProbeBlockReuse("org-1", "block-1")
	if err != nil {
		t.Fatalf("ProbeBlockReuse() error = %v, want nil", err)
	}
	if probe.Decision != BlockReuseBlockedByGC {
		t.Fatalf("decision = %v, want BlockReuseBlockedByGC", probe.Decision)
	}
}

func TestProbeBlockReuseResumesOwnedRepairingStub(t *testing.T) {
	oldMetadata := probeBlockReuseMetadataFn
	oldOrphan := probeBlockReuseHasS3OrphanFn
	t.Cleanup(func() {
		probeBlockReuseMetadataFn = oldMetadata
		probeBlockReuseHasS3OrphanFn = oldOrphan
	})
	claimedAt := time.Now().UTC()
	probeBlockReuseMetadataFn = func(*DB, string, string) (blockReuseMetadataRow, bool, error) {
		return blockReuseMetadataRow{
			GCState:     BlockGCStateRepairingStub,
			GCClaimID:   blockStubRepairIDFn("org-1", "block-1"),
			GCClaimedAt: &claimedAt,
		}, true, nil
	}
	probeBlockReuseHasS3OrphanFn = func(*DB, string, string) (bool, error) { return false, nil }

	probe, err := (&DB{}).ProbeBlockReuse("org-1", "block-1")
	if err != nil || probe.Decision != BlockReuseRepairableStub {
		t.Fatalf("ProbeBlockReuse() = %v, %v, want repairable/nil", probe.Decision, err)
	}
}

func TestProbeBlockReuseBlocksForeignRepairingStub(t *testing.T) {
	oldMetadata := probeBlockReuseMetadataFn
	oldOrphan := probeBlockReuseHasS3OrphanFn
	t.Cleanup(func() {
		probeBlockReuseMetadataFn = oldMetadata
		probeBlockReuseHasS3OrphanFn = oldOrphan
	})
	claimedAt := time.Now().UTC()
	probeBlockReuseMetadataFn = func(*DB, string, string) (blockReuseMetadataRow, bool, error) {
		return blockReuseMetadataRow{GCState: BlockGCStateRepairingStub, GCClaimID: "foreign", GCClaimedAt: &claimedAt}, true, nil
	}
	probeBlockReuseHasS3OrphanFn = func(*DB, string, string) (bool, error) {
		t.Fatal("orphan read must not run for a foreign repair claim")
		return false, nil
	}

	probe, err := (&DB{}).ProbeBlockReuse("org-1", "block-1")
	if err != nil || probe.Decision != BlockReuseBlockedByGC {
		t.Fatalf("ProbeBlockReuse() = %v, %v, want blocked/nil", probe.Decision, err)
	}
}

func TestProbeBlockReuseRejectsIncompleteClaimOwnership(t *testing.T) {
	oldMetadata := probeBlockReuseMetadataFn
	t.Cleanup(func() { probeBlockReuseMetadataFn = oldMetadata })
	probeBlockReuseMetadataFn = func(*DB, string, string) (blockReuseMetadataRow, bool, error) {
		return blockReuseMetadataRow{GCState: BlockGCStateDeleting, GCClaimID: "claim-1"}, true, nil
	}

	probe, err := (&DB{}).ProbeBlockReuse("org-1", "block-1")
	if err == nil {
		t.Fatal("ProbeBlockReuse() error = nil, want malformed ownership error")
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
		row := completeProbeMetadataRow("cold-archive")
		row.Sha1 = "sha1-cold"
		row.SizeBytes = 4096
		row.StorageKey = "blocks/ab/cd"
		return row, true, nil
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
		row := completeProbeMetadataRow("hot")
		claimedAt := time.Now().UTC()
		row.SizeBytes = 10
		row.GCState = BlockGCStateDeleting
		row.GCClaimID = "claim-1"
		row.GCClaimedAt = &claimedAt
		return row, true, nil
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
