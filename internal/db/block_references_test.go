package db

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
)

// installTestBlockID is a real SHA-256 block id. InstallBlockMetadata validates
// its own block id now, so these tests must use an identity production could
// actually produce rather than a readable placeholder.
var installTestBlockID = strings.Repeat("b", 64)

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
				if ctx == nil || database == nil || orgID != "org-1" || blockID != installTestBlockID || representationID != PlainBlockRepresentationID || sha1 != strings.Repeat("a", 40) || sizeBytes != 123 || location != proposed || now.IsZero() {
					t.Fatalf("install args were not preserved: db=%p org=%q block=%q representation=%q sha1=%q size=%d location=%+v now=%v", database, orgID, blockID, representationID, sha1, sizeBytes, location, now)
				}
				return test.applied, test.current, nil
			}
			settleInstalledBlockMetadataFn = func(context.Context, *DB, string, string) (installedBlockMetadataRow, bool, error) {
				t.Fatal("settlement must not run after a definite CAS result")
				return installedBlockMetadataRow{}, false, nil
			}

			got := (&DB{}).InstallBlockMetadata(context.Background(), "org-1", PlainBlockRepresentationID, installTestBlockID, strings.Repeat("a", 40), 123, proposed)
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
		got := (&DB{}).InstallBlockMetadata(context.Background(), "org-1", PlainBlockRepresentationID, installTestBlockID, "", 1, proposed)
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

			got := (&DB{}).InstallBlockMetadata(context.Background(), "org-1", PlainBlockRepresentationID, installTestBlockID, "", 1, proposed)
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

	got := (&DB{config: config.DatabaseConfig{Timeout: time.Second}}).InstallBlockMetadata(requestCtx, "org-1", PlainBlockRepresentationID, installTestBlockID, "", 1, proposed)
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
		got := (&DB{}).InstallBlockMetadata(context.Background(), "org-1", PlainBlockRepresentationID, installTestBlockID, "", 1, proposed)
		if got.Outcome != InstallBlockMetadataAmbiguous || !errors.Is(got.Cause, ErrBlockMetadataPermanent) {
			t.Fatalf("InstallBlockMetadata(%+v) = %+v, want non-authorizing permanent rejection", proposed, got)
		}
		if got.Submitted {
			t.Fatalf("InstallBlockMetadata(%+v) Submitted = true before LWT", proposed)
		}
	}
}

// TestInstallBlockMetadataRejectsNonSHA256BlockID keeps this seam self-sufficient.
// InstallBlockMetadata is the single-use canonical install boundary, so it validates
// its own block id instead of trusting the caller. Production only reaches it behind
// ValidateMintedPhysicalLocator, which already checks the SHA-256 -- but that is the
// caller guarantee, not this boundary guarantee, and a future internal caller must
// not be able to install a canonical row under a block id that is not a SHA-256.
//
// The rejection must happen before the LWT seam so it is conclusively unsubmitted:
// Submitted stays false, which is what tells the cleanup path the object is safe to
// remove and that no install identity was consumed.
// TestInstallBlockMetadataAcceptsCanonicalBlockID is the companion to the rejection
// table: the gate must accept the exact lower-case digest production actually mints,
// or it would be rejecting everything and the table above would prove nothing.
func TestInstallBlockMetadataAcceptsCanonicalBlockID(t *testing.T) {
	withInstallBlockMetadataSeams(t)
	installed := ""
	installBlockMetadataLWTFn = func(_ context.Context, _ *DB, _, blockID, _, _ string, _ int, _ BlockPhysicalLocation, _ time.Time) (bool, map[string]interface{}, error) {
		installed = blockID
		return true, nil, nil
	}

	proposed := BlockPhysicalLocation{StorageClass: "hot", StorageKey: "blocks/org-1/minted"}
	got := (&DB{}).InstallBlockMetadata(context.Background(), "org-1", PlainBlockRepresentationID, installTestBlockID, "", 1, proposed)
	if got.Outcome != InstallBlockMetadataApplied {
		t.Fatalf("InstallBlockMetadata() = %+v, want Applied", got)
	}
	if installed != installTestBlockID {
		t.Fatalf("installed block id = %q, want the exact validated id %q", installed, installTestBlockID)
	}
}

func TestInstallBlockMetadataRejectsNonSHA256BlockID(t *testing.T) {
	withInstallBlockMetadataSeams(t)
	installBlockMetadataLWTFn = func(context.Context, *DB, string, string, string, string, int, BlockPhysicalLocation, time.Time) (bool, map[string]interface{}, error) {
		t.Fatal("a non-SHA-256 block id must not issue INSTALL")
		return false, nil, nil
	}
	settleInstalledBlockMetadataFn = func(context.Context, *DB, string, string) (installedBlockMetadataRow, bool, error) {
		t.Fatal("a non-SHA-256 block id must not settle an INSTALL that was never issued")
		return installedBlockMetadataRow{}, false, nil
	}

	proposed := BlockPhysicalLocation{StorageClass: "hot", StorageKey: "blocks/org-1/minted"}
	for _, blockID := range []string{
		"block-1",
		"",
		strings.Repeat("b", 63),
		strings.Repeat("b", 65),
		strings.Repeat("z", 64),
		strings.Repeat("b", 40),
		// Non-canonical SPELLINGS of an otherwise valid digest. These are the cases
		// a normalize-then-validate gate would wave through while the LWT and the
		// settlement SELECT still used the original string as the partition key --
		// validating one identity and installing the canonical row under another.
		strings.ToUpper(installTestBlockID),
		" " + installTestBlockID,
		installTestBlockID + " ",
		" " + installTestBlockID + " ",
	} {
		got := (&DB{}).InstallBlockMetadata(context.Background(), "org-1", PlainBlockRepresentationID, blockID, "", 1, proposed)
		if got.Outcome != InstallBlockMetadataAmbiguous || !errors.Is(got.Cause, ErrBlockMetadataPermanent) {
			t.Fatalf("InstallBlockMetadata(blockID=%q) = %+v, want permanent rejection", blockID, got)
		}
		if got.Submitted {
			t.Fatalf("InstallBlockMetadata(blockID=%q) Submitted = true; a pre-seam rejection must stay conclusively unsubmitted", blockID)
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

func withBlockRepairSeams(t *testing.T) {
	t.Helper()
	oldRead := readBlockRepairAuthorityFn
	oldOrphan := blockRepairHasS3OrphanFn
	oldRepresentation := backfillCurrentBlockRepresentationIDFn
	oldSHA1 := backfillCurrentBlockSHA1Fn
	t.Cleanup(func() {
		readBlockRepairAuthorityFn = oldRead
		blockRepairHasS3OrphanFn = oldOrphan
		backfillCurrentBlockRepresentationIDFn = oldRepresentation
		backfillCurrentBlockSHA1Fn = oldSHA1
	})
}

func completeBlockRepairAuthorityRow(location BlockPhysicalLocation) blockRepairAuthorityRow {
	createdAt := time.Date(2026, time.August, 25, 12, 0, 0, 0, time.UTC)
	return blockRepairAuthorityRow{
		blockIdentityRepairRow: blockIdentityRepairRow{
			RepresentationID:    PlainBlockRepresentationID,
			Sha1:                strings.Repeat("a", 40),
			SizeBytes:           123,
			StorageClass:        location.StorageClass,
			StorageClassPresent: true,
			StorageKey:          location.StorageKey,
			CreatedAt:           &createdAt,
		},
		StorageKeyPresent: true,
	}
}

func TestValidateBlockRepairAuthorityClassifiesExactIncarnation(t *testing.T) {
	withBlockRepairSeams(t)
	expected := BlockPhysicalLocation{StorageClass: "hot", StorageKey: "blocks/org-1/bb/bb/" + installTestBlockID + ".8f14e45f-ea4d-4f73-9f7c-63f4e7a5bc21"}
	claimedAt := time.Date(2026, time.August, 25, 12, 1, 0, 0, time.UTC)

	tests := []struct {
		name        string
		row         blockRepairAuthorityRow
		found       bool
		hasOrphan   bool
		wantOutcome BlockRepairAuthorityOutcome
		wantErr     error
	}{
		{name: "absent row", wantOutcome: BlockRepairAuthorityChanged, wantErr: ErrBlockRepairAuthorityChanged},
		{name: "tuple changed", row: completeBlockRepairAuthorityRow(BlockPhysicalLocation{StorageClass: "hot", StorageKey: expected.StorageKey + ".other"}), found: true, wantOutcome: BlockRepairAuthorityChanged, wantErr: ErrBlockRepairAuthorityChanged},
		{name: "deleting claim", row: func() blockRepairAuthorityRow {
			row := completeBlockRepairAuthorityRow(expected)
			row.GCState, row.GCClaimID, row.GCClaimedAt = BlockGCStateDeleting, "delete-1", &claimedAt
			return row
		}(), found: true, wantOutcome: BlockRepairAuthorityBlocked, wantErr: ErrBlockRepairBlocked},
		{name: "repair claim", row: func() blockRepairAuthorityRow {
			row := completeBlockRepairAuthorityRow(expected)
			row.GCState, row.GCClaimID, row.GCClaimedAt = BlockGCStateRepairingStub, "repair-1", &claimedAt
			return row
		}(), found: true, wantOutcome: BlockRepairAuthorityBlocked, wantErr: ErrBlockRepairBlocked},
		{name: "orphan fence", row: completeBlockRepairAuthorityRow(expected), found: true, hasOrphan: true, wantOutcome: BlockRepairAuthorityBlocked, wantErr: ErrBlockRepairBlocked},
		{name: "complete exact row", row: completeBlockRepairAuthorityRow(expected), found: true, wantOutcome: BlockRepairAuthorityAuthorized},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			readBlockRepairAuthorityFn = func(*DB, string, string, BlockAuthorityRead) (blockRepairAuthorityRow, bool, error) {
				return test.row, test.found, nil
			}
			blockRepairHasS3OrphanFn = func(*DB, string, string, BlockAuthorityRead) (bool, error) {
				return test.hasOrphan, nil
			}
			outcome, err := (&DB{}).ValidateBlockRepairAuthority("org-1", installTestBlockID, expected)
			if outcome != test.wantOutcome || !errors.Is(err, test.wantErr) {
				t.Fatalf("ValidateBlockRepairAuthority() = %v, %v, want %v, %v", outcome, err, test.wantOutcome, test.wantErr)
			}
		})
	}
}

func TestRepairBlockMetadataIfCurrentAbsentRowNeverInserts(t *testing.T) {
	withBlockRepairSeams(t)
	blockRepairHasS3OrphanFn = func(*DB, string, string, BlockAuthorityRead) (bool, error) { return false, nil }
	readBlockRepairAuthorityFn = func(*DB, string, string, BlockAuthorityRead) (blockRepairAuthorityRow, bool, error) {
		return blockRepairAuthorityRow{}, false, nil
	}

	err := (&DB{}).RepairBlockMetadataIfCurrent("org-1", PlainBlockRepresentationID, installTestBlockID, strings.Repeat("a", 40), 123, BlockPhysicalLocation{StorageClass: "hot", StorageKey: "legacy-key"})
	if !errors.Is(err, ErrBlockRepairAuthorityChanged) {
		t.Fatalf("RepairBlockMetadataIfCurrent() error = %v, want authority changed", err)
	}
}

func TestRepairBlockMetadataIfCurrentBackfillsWithTupleBoundCAS(t *testing.T) {
	expected := BlockPhysicalLocation{StorageClass: "hot", StorageKey: "canonical-key"}

	t.Run("representation contention", func(t *testing.T) {
		withBlockRepairSeams(t)
		row := completeBlockRepairAuthorityRow(expected)
		row.RepresentationID = ""
		readBlockRepairAuthorityFn = func(*DB, string, string, BlockAuthorityRead) (blockRepairAuthorityRow, bool, error) {
			return row, true, nil
		}
		blockRepairHasS3OrphanFn = func(*DB, string, string, BlockAuthorityRead) (bool, error) { return false, nil }
		backfillCurrentBlockRepresentationIDFn = func(_ *DB, orgID, blockID, representationID, current string, location BlockPhysicalLocation, createdAt time.Time, size int) (bool, error) {
			if orgID != "org-1" || blockID != installTestBlockID || representationID != PlainBlockRepresentationID || current != "" || location != expected || !createdAt.Equal(*row.CreatedAt) || size != 123 {
				t.Fatalf("representation CAS was not bound to the observed row: %s/%s/%s/%q/%+v/%v/%d", orgID, blockID, representationID, current, location, createdAt, size)
			}
			return false, nil
		}
		err := (&DB{}).RepairBlockMetadataIfCurrent("org-1", PlainBlockRepresentationID, installTestBlockID, row.Sha1, 123, expected)
		if !errors.Is(err, ErrBlockRepairAuthorityChanged) {
			t.Fatalf("RepairBlockMetadataIfCurrent() error = %v, want authority changed", err)
		}
	})

	t.Run("sha1 contention", func(t *testing.T) {
		withBlockRepairSeams(t)
		row := completeBlockRepairAuthorityRow(expected)
		row.Sha1 = ""
		readBlockRepairAuthorityFn = func(*DB, string, string, BlockAuthorityRead) (blockRepairAuthorityRow, bool, error) {
			return row, true, nil
		}
		blockRepairHasS3OrphanFn = func(*DB, string, string, BlockAuthorityRead) (bool, error) { return false, nil }
		backfillCurrentBlockSHA1Fn = func(_ *DB, orgID, blockID, sha1, current, representationID string, location BlockPhysicalLocation, createdAt time.Time, size int) (bool, error) {
			if orgID != "org-1" || blockID != installTestBlockID || sha1 != strings.Repeat("b", 40) || current != "" || representationID != PlainBlockRepresentationID || location != expected || !createdAt.Equal(*row.CreatedAt) || size != 123 {
				t.Fatalf("sha1 CAS was not bound to the observed row: %s/%s/%s/%q/%s/%+v/%v/%d", orgID, blockID, sha1, current, representationID, location, createdAt, size)
			}
			return false, nil
		}
		err := (&DB{}).RepairBlockMetadataIfCurrent("org-1", PlainBlockRepresentationID, installTestBlockID, strings.Repeat("b", 40), 123, expected)
		if !errors.Is(err, ErrBlockRepairAuthorityChanged) {
			t.Fatalf("RepairBlockMetadataIfCurrent() error = %v, want authority changed", err)
		}
	})
}

func TestRepairBlockMetadataIfCurrentAcceptsCompleteAndLegacyRows(t *testing.T) {
	withBlockRepairSeams(t)
	legacy := BlockPhysicalLocation{StorageClass: "hot", StorageKey: "blocks/org-1/bb/bb/" + installTestBlockID}
	row := completeBlockRepairAuthorityRow(legacy)
	readBlockRepairAuthorityFn = func(*DB, string, string, BlockAuthorityRead) (blockRepairAuthorityRow, bool, error) {
		return row, true, nil
	}
	blockRepairHasS3OrphanFn = func(*DB, string, string, BlockAuthorityRead) (bool, error) { return false, nil }
	backfillCurrentBlockRepresentationIDFn = func(*DB, string, string, string, string, BlockPhysicalLocation, time.Time, int) (bool, error) {
		t.Fatal("complete row must not backfill representation")
		return false, nil
	}
	backfillCurrentBlockSHA1Fn = func(*DB, string, string, string, string, string, BlockPhysicalLocation, time.Time, int) (bool, error) {
		t.Fatal("complete row must not backfill sha1")
		return false, nil
	}
	if err := (&DB{}).RepairBlockMetadataIfCurrent("org-1", PlainBlockRepresentationID, installTestBlockID, row.Sha1, row.SizeBytes, legacy); err != nil {
		t.Fatalf("RepairBlockMetadataIfCurrent(legacy deterministic key) error = %v", err)
	}
}

func TestBlockRepairAuthorityRejectsMalformedStatePermanently(t *testing.T) {
	withBlockRepairSeams(t)
	expected := BlockPhysicalLocation{StorageClass: "hot", StorageKey: "canonical-key"}
	row := completeBlockRepairAuthorityRow(expected)
	row.GCClaimID = "claim-without-state"
	readBlockRepairAuthorityFn = func(*DB, string, string, BlockAuthorityRead) (blockRepairAuthorityRow, bool, error) {
		return row, true, nil
	}
	orphanRead := false
	blockRepairHasS3OrphanFn = func(*DB, string, string, BlockAuthorityRead) (bool, error) {
		orphanRead = true
		return false, nil
	}
	outcome, err := (&DB{}).ValidateBlockRepairAuthority("org-1", installTestBlockID, expected)
	if outcome != BlockRepairAuthorityPermanent || !errors.Is(err, ErrBlockRepairAuthorityPermanent) || !errors.Is(err, ErrBlockMetadataPermanent) {
		t.Fatalf("ValidateBlockRepairAuthority() = %v, %v, want permanent malformed-state rejection", outcome, err)
	}
	if !orphanRead {
		t.Fatal("authority validation must read the orphan fence before validating the canonical row")
	}

	readBlockRepairAuthorityFn = func(*DB, string, string, BlockAuthorityRead) (blockRepairAuthorityRow, bool, error) {
		t.Fatal("malformed locator input must fail before reading")
		return blockRepairAuthorityRow{}, false, nil
	}
	outcome, err = (&DB{}).ValidateBlockRepairAuthority("org-1", installTestBlockID, BlockPhysicalLocation{StorageClass: "Hot", StorageKey: " key "})
	if outcome != BlockRepairAuthorityPermanent || !errors.Is(err, ErrBlockRepairAuthorityPermanent) {
		t.Fatalf("ValidateBlockRepairAuthority(malformed locator) = %v, %v, want permanent", outcome, err)
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
		if orgID != "org-1" || blockID != installTestBlockID || repairID != "repair-1" || claimedAt.IsZero() {
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

	repaired, err := (&DB{}).RepairReleasedBlockStub("org-1", installTestBlockID)
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

	repaired, err := (&DB{}).RepairReleasedBlockStub("org-1", installTestBlockID)
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
	repairID := blockStubRepairIDFn("org-1", installTestBlockID)
	claimReleasedBlockStubForRepairFn = func(*DB, string, string, string, time.Time) (bool, error) {
		return false, errors.New("ambiguous timeout")
	}
	readBlockIdentityForRepairFn = func(*DB, string, string) (blockIdentityRepairRow, bool, error) {
		return blockIdentityRepairRow{GCState: BlockGCStateRepairingStub, GCClaimID: repairID, GCClaimedAt: &claimedAt}, true, nil
	}
	probeBlockReuseHasS3OrphanFn = func(*DB, string, string) (bool, error) { return false, nil }
	deleteRepairClaimedBlockStubFn = func(*DB, string, string, string) (bool, error) { return true, nil }

	repaired, err := (&DB{}).RepairReleasedBlockStub("org-1", installTestBlockID)
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
	repairID := blockStubRepairIDFn("org-1", installTestBlockID)
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

	repaired, err := (&DB{}).RepairReleasedBlockStub("org-1", installTestBlockID)
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
	repaired, err := (&DB{}).RepairReleasedBlockStub("org-1", installTestBlockID)
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
		if len(blockIDs) != 1 || blockIDs[0] != installTestBlockID {
			t.Fatalf("remove blockIDs = %#v, want []string{%q}", blockIDs, installTestBlockID)
		}
		return nil
	}

	registerCalls := 0
	wantErr := errors.New("register boom")
	err := PromotePublishAttemptReferences(nil, "org-1", "attempt-1", []string{installTestBlockID}, func() error {
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

	err := PromotePublishAttemptReferences(nil, "org-1", "attempt-1", []string{installTestBlockID}, func() error {
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
	err := PromotePublishAttemptReferences(nil, "org-1", "attempt-1", []string{installTestBlockID}, func() error {
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

	resolved, err := StagePublishAttemptReferences(&DB{}, "org-1", "repo-1", "attempt-1", []string{installTestBlockID, "block-2"}, nil)
	if !errors.Is(err, wantErr) {
		t.Fatalf("StagePublishAttemptReferences() error = %v, want %v", err, wantErr)
	}
	if resolved != nil {
		t.Fatalf("resolved = %#v, want nil on stage failure", resolved)
	}
	if len(added) != 1 || added[0] != installTestBlockID {
		t.Fatalf("added = %#v, want []string{%q}", added, installTestBlockID)
	}
	if len(removed) != 1 || removed[0] != installTestBlockID {
		t.Fatalf("removed = %#v, want []string{%q}", removed, installTestBlockID)
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

	_, err := StagePublishAttemptReferences(&DB{}, "org-1", "repo-1", "attempt-1", []string{installTestBlockID, "block-2"}, nil)
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

	probe, err := (&DB{}).ProbeBlockReuse("org-1", installTestBlockID)
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

	probe, err := (&DB{}).ProbeBlockReuse("org-1", installTestBlockID)
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

	probe, err := (&DB{}).ProbeBlockReuse("org-1", installTestBlockID)
	if err != nil {
		t.Fatalf("ProbeBlockReuse() error = %v, want nil", err)
	}
	if probe.Decision != BlockReuseBlockedByGC {
		t.Fatalf("decision = %v, want BlockReuseBlockedByGC", probe.Decision)
	}
}

func TestP3ProbeBlockReuseOrphanOutranksReferences(t *testing.T) {
	oldMetadata := probeBlockReuseMetadataFn
	oldReferences := probeBlockReuseHasReferencesFn
	oldOrphan := probeBlockReuseHasS3OrphanFn
	t.Cleanup(func() {
		probeBlockReuseMetadataFn = oldMetadata
		probeBlockReuseHasReferencesFn = oldReferences
		probeBlockReuseHasS3OrphanFn = oldOrphan
	})

	probeBlockReuseMetadataFn = func(*DB, string, string) (blockReuseMetadataRow, bool, error) {
		row := completeProbeMetadataRow("hot")
		row.StorageKey = "legacy-deterministic-key"
		return row, true, nil
	}
	probeBlockReuseHasReferencesFn = func(*DB, string, string) (bool, error) { return true, nil }
	probeBlockReuseHasS3OrphanFn = func(*DB, string, string) (bool, error) { return true, nil }

	probe, err := (&DB{}).ProbeBlockReuse("org-1", installTestBlockID)
	if err != nil || probe.Decision != BlockReuseBlockedByGC {
		t.Fatalf("ProbeBlockReuse() = %v, %v, want BlockReuseBlockedByGC", probe.Decision, err)
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

	probe, err := (&DB{}).ProbeBlockReuse("org-1", installTestBlockID)
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

		probe, err := (&DB{}).ProbeBlockReuse("org-1", installTestBlockID)
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

		probe, err := (&DB{}).ProbeBlockReuse("org-1", installTestBlockID)
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

	probe, err := (&DB{}).ProbeBlockReuse("org-1", installTestBlockID)
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

	probe, err := (&DB{}).ProbeBlockReuse("org-1", installTestBlockID)
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

	probe, err := (&DB{}).ProbeBlockReuse("org-1", installTestBlockID)
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
			GCClaimID:   blockStubRepairIDFn("org-1", installTestBlockID),
			GCClaimedAt: &claimedAt,
		}, true, nil
	}
	probeBlockReuseHasS3OrphanFn = func(*DB, string, string) (bool, error) { return false, nil }

	probe, err := (&DB{}).ProbeBlockReuse("org-1", installTestBlockID)
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

	probe, err := (&DB{}).ProbeBlockReuse("org-1", installTestBlockID)
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

	probe, err := (&DB{}).ProbeBlockReuse("org-1", installTestBlockID)
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

	probe, err := (&DB{}).ProbeBlockReuse("org-1", installTestBlockID)
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

	probe, err := (&DB{}).ProbeBlockReuse("org-1", installTestBlockID)
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

	probe, err := (&DB{}).ProbeBlockReuse("org-1", installTestBlockID)
	if !errors.Is(err, wantErr) {
		t.Fatalf("ProbeBlockReuse() error = %v, want wrapped %v", err, wantErr)
	}
	if probe.Decision != BlockReuseUnknownError {
		t.Fatalf("decision = %v, want BlockReuseUnknownError", probe.Decision)
	}
}

// TestP3BlockDeleteFenceSurvivesOrphanHandoff pins the read order that closes the
// A+ handoff race (R13). GC writes the orphan and only then removes the canonical
// row, so a writer that reads the orphan FIRST can observe "no orphan", have GC
// complete both steps underneath it, then read an absent row and conclude there is
// no fence at all -- leaving orphan(P1) live while it installs P2.
//
// The seam below reproduces exactly that interleaving: the canonical read is the
// moment GC finishes. With the canonical row read first the orphan read that
// follows must observe the fence; swap the two reads back and this test fails.
func TestP3BlockDeleteFenceSurvivesOrphanHandoff(t *testing.T) {
	oldState := blockDeleteFenceGCStateFn
	oldOrphan := blockDeleteFenceHasS3OrphanFn
	t.Cleanup(func() {
		blockDeleteFenceGCStateFn = oldState
		blockDeleteFenceHasS3OrphanFn = oldOrphan
	})

	orphanPublished := false
	canonicalReads := 0
	blockDeleteFenceGCStateFn = func(*DB, string, string) (string, bool, error) {
		canonicalReads++
		// GC's StartBlockDeleteOrphan then FinalizeBlockDelete land here.
		orphanPublished = true
		return "", false, nil
	}
	blockDeleteFenceHasS3OrphanFn = func(*DB, string, string) (bool, error) {
		return orphanPublished, nil
	}

	fenced, err := (&DB{}).BlockDeleteFenceActive("org-1", installTestBlockID)
	if err != nil {
		t.Fatalf("BlockDeleteFenceActive() error = %v, want nil", err)
	}
	if !fenced {
		t.Fatal("BlockDeleteFenceActive() = false; a rowless read must not be reported as unfenced while the lifecycle's orphan is live")
	}
	if canonicalReads != 1 {
		t.Fatalf("canonical reads = %d, want 1", canonicalReads)
	}
}

// TestP3BlockDeleteFenceReadsCanonicalRowBeforeOrphan states the ordering as a
// property rather than as a consequence, so a refactor cannot satisfy the handoff
// test by accident.
func TestP3BlockDeleteFenceReadsCanonicalRowBeforeOrphan(t *testing.T) {
	oldState := blockDeleteFenceGCStateFn
	oldOrphan := blockDeleteFenceHasS3OrphanFn
	t.Cleanup(func() {
		blockDeleteFenceGCStateFn = oldState
		blockDeleteFenceHasS3OrphanFn = oldOrphan
	})

	var order []string
	blockDeleteFenceGCStateFn = func(*DB, string, string) (string, bool, error) {
		order = append(order, "blocks")
		return "", true, nil
	}
	blockDeleteFenceHasS3OrphanFn = func(*DB, string, string) (bool, error) {
		order = append(order, "orphan")
		return false, nil
	}

	if _, err := (&DB{}).BlockDeleteFenceActive("org-1", installTestBlockID); err != nil {
		t.Fatalf("BlockDeleteFenceActive() error = %v, want nil", err)
	}
	if len(order) != 2 || order[0] != "blocks" || order[1] != "orphan" {
		t.Fatalf("fence read order = %v, want [blocks orphan]: the orphan must be the last fence read", order)
	}
}

// TestP3BlockDeleteFenceStillCatchesAnActiveClaim keeps the short-circuit honest:
// an in-row claim fences without needing the orphan read at all.
func TestP3BlockDeleteFenceStillCatchesAnActiveClaim(t *testing.T) {
	oldState := blockDeleteFenceGCStateFn
	oldOrphan := blockDeleteFenceHasS3OrphanFn
	t.Cleanup(func() {
		blockDeleteFenceGCStateFn = oldState
		blockDeleteFenceHasS3OrphanFn = oldOrphan
	})

	orphanReads := 0
	blockDeleteFenceGCStateFn = func(*DB, string, string) (string, bool, error) {
		return BlockGCStateDeleting, true, nil
	}
	blockDeleteFenceHasS3OrphanFn = func(*DB, string, string) (bool, error) {
		orphanReads++
		return false, nil
	}

	fenced, err := (&DB{}).BlockDeleteFenceActive("org-1", installTestBlockID)
	if err != nil || !fenced {
		t.Fatalf("BlockDeleteFenceActive() = %v, %v; want true, nil", fenced, err)
	}
	if orphanReads != 0 {
		t.Fatalf("orphan reads = %d, want 0 for an already-claimed row", orphanReads)
	}
}

// TestP3RepairAuthorityReadsCanonicalRowBeforeOrphan applies the same ordering
// proof to the pre-PUT authority boundary.
func TestP3RepairAuthorityReadsCanonicalRowBeforeOrphan(t *testing.T) {
	oldRead := readBlockRepairAuthorityFn
	oldOrphan := blockRepairHasS3OrphanFn
	t.Cleanup(func() {
		readBlockRepairAuthorityFn = oldRead
		blockRepairHasS3OrphanFn = oldOrphan
	})

	var order []string
	var observedMode BlockAuthorityRead
	readBlockRepairAuthorityFn = func(_ *DB, _, _ string, mode BlockAuthorityRead) (blockRepairAuthorityRow, bool, error) {
		order = append(order, "blocks")
		observedMode = mode
		return blockRepairAuthorityRow{}, false, nil
	}
	blockRepairHasS3OrphanFn = func(_ *DB, _, _ string, _ BlockAuthorityRead) (bool, error) {
		order = append(order, "orphan")
		return true, nil
	}

	outcome, err := (&DB{}).ValidateBlockRepairAuthority("org-1", installTestBlockID, BlockPhysicalLocation{
		StorageClass: "hot",
		StorageKey:   "blocks/org-1/minted",
	})
	if outcome != BlockRepairAuthorityBlocked || !errors.Is(err, ErrBlockRepairBlocked) {
		t.Fatalf("ValidateBlockRepairAuthority() = %v, %v; want Blocked with a fence error", outcome, err)
	}
	if len(order) != 2 || order[0] != "blocks" || order[1] != "orphan" {
		t.Fatalf("authority read order = %v, want [blocks orphan]", order)
	}
	if observedMode != BlockAuthorityStrong {
		t.Fatalf("pre-PUT authority read mode = %v, want BlockAuthorityStrong", observedMode)
	}
}

// TestP3MetadataRepairUsesAdvisoryReads keeps the hot dedup path off global Paxos.
// Its safety comes from the non-creating tuple-bound CAS, not from read freshness.
func TestP3MetadataRepairUsesAdvisoryReads(t *testing.T) {
	oldRead := readBlockRepairAuthorityFn
	oldOrphan := blockRepairHasS3OrphanFn
	t.Cleanup(func() {
		readBlockRepairAuthorityFn = oldRead
		blockRepairHasS3OrphanFn = oldOrphan
	})

	modes := map[BlockAuthorityRead]int{}
	readBlockRepairAuthorityFn = func(_ *DB, _, _ string, mode BlockAuthorityRead) (blockRepairAuthorityRow, bool, error) {
		modes[mode]++
		return blockRepairAuthorityRow{}, false, nil
	}
	blockRepairHasS3OrphanFn = func(_ *DB, _, _ string, mode BlockAuthorityRead) (bool, error) {
		modes[mode]++
		return false, nil
	}

	err := (&DB{}).RepairBlockMetadataIfCurrent("org-1", PlainBlockRepresentationID, installTestBlockID, "", 7, BlockPhysicalLocation{
		StorageClass: "hot",
		StorageKey:   "blocks/org-1/minted",
	})
	if !errors.Is(err, ErrBlockRepairAuthorityChanged) {
		t.Fatalf("RepairBlockMetadataIfCurrent() = %v, want authority changed for an absent row", err)
	}
	if modes[BlockAuthorityStrong] != 0 {
		t.Fatalf("metadata repair issued %d SERIAL reads, want 0 on the deduplicated upload path", modes[BlockAuthorityStrong])
	}
	if modes[BlockAuthorityAdvisory] != 2 {
		t.Fatalf("metadata repair advisory reads = %d, want 2", modes[BlockAuthorityAdvisory])
	}
}
