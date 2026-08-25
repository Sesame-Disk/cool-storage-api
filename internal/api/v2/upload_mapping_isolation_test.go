package v2

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Sesame-Disk/sesamefs/internal/db"
)

// The external SHA-1 mapping is written AFTER the canonical install applied. If a
// transient mapping failure escaped as ErrBlockMaterializationTransient, the
// materialization retry driver would answer it by restarting the cycle at
// BlockMaterializationInitial -- the only phase with mint authority -- for a block
// that is already canonical. A probe that momentarily read rowless would then mint
// and PUT a second physical incarnation because a sidecar write timed out.
//
// These tests pin the isolation: mapping failures retry in place, and an exhausted
// mapping budget is NOT retryable, so the store phase is never re-entered.

func mappingIsolationTarget() BlockMaterializationTarget {
	return BlockMaterializationTarget{StorageClass: "hot", StorageKey: "blocks/org/ab/cd/hash.minted", FreshInstall: true}
}

func stubMaterializationRegister(t *testing.T, installs *int) {
	t.Helper()
	oldRegister := registerUploadedBlockTargetForMaterializationFn
	t.Cleanup(func() { registerUploadedBlockTargetForMaterializationFn = oldRegister })
	registerUploadedBlockTargetForMaterializationFn = func(context.Context, *db.DB, string, string, string, string, int, BlockMaterializationTarget, string) error {
		*installs++
		return nil
	}
}

// TestMappingTransientRetriesInPlaceAfterInstallApplied is the core regression:
// one mint, one PUT, one INSTALL, and the mapping recovers on its own retry.
func TestMappingTransientRetriesInPlaceAfterInstallApplied(t *testing.T) {
	fastBlockMaterializationRetries(t)

	installs, mappingCalls := 0, 0
	stubMaterializationRegister(t, &installs)

	oldMapping := writeBlockMappingForMaterializationFn
	t.Cleanup(func() { writeBlockMappingForMaterializationFn = oldMapping })
	writeBlockMappingForMaterializationFn = func(*db.DB, string, string, string, string) error {
		mappingCalls++
		if mappingCalls == 1 {
			return errors.New("cassandra write timeout")
		}
		return nil
	}

	storeCalls, phases := 0, []BlockMaterializationPhase{}
	err := RetryUploadedBlockMaterializationPhasedContext(context.Background(), "UploadFile", uploadReuseTestBlockID,
		func(phase BlockMaterializationPhase) error {
			storeCalls++
			phases = append(phases, phase)
			return nil
		},
		func() error {
			return RegisterUploadedBlockTargetAndMapping(context.Background(), nil, "org", "repo", uploadReuseTestBlockID, "op", 1, mappingIsolationTarget(), "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
		}, nil, nil)
	if err != nil {
		t.Fatalf("error = %v, want nil (the mapping retry should recover in place)", err)
	}

	if installs != 1 {
		t.Errorf("canonical installs = %d, want 1; a mapping failure must not resubmit the install", installs)
	}
	if mappingCalls != 2 {
		t.Errorf("mapping calls = %d, want 2 (one failure, one in-place retry)", mappingCalls)
	}
	want := []BlockMaterializationPhase{BlockMaterializationInitial, BlockMaterializationConfirmation}
	if len(phases) != len(want) {
		t.Fatalf("store phases = %v, want %v; the store phase must run exactly once per phase", phases, want)
	}
	for i, phase := range want {
		if phases[i] != phase {
			t.Fatalf("store phases = %v, want %v; a mapping failure must never re-enter the initial phase", phases, want)
		}
	}
}

// TestMappingExhaustionIsNotRetryable proves the escape hatch stays closed when
// the mapping never succeeds: the error must not carry the retryable sentinel, so
// the driver reports it instead of restarting the mint-capable store phase.
func TestMappingExhaustionIsNotRetryable(t *testing.T) {
	fastBlockMaterializationRetries(t)

	installs, mappingCalls := 0, 0
	stubMaterializationRegister(t, &installs)

	oldMapping := writeBlockMappingForMaterializationFn
	t.Cleanup(func() { writeBlockMappingForMaterializationFn = oldMapping })
	writeBlockMappingForMaterializationFn = func(*db.DB, string, string, string, string) error {
		mappingCalls++
		return errors.New("cassandra write timeout")
	}

	storeCalls := 0
	err := RetryUploadedBlockMaterializationPhasedContext(context.Background(), "UploadFile", uploadReuseTestBlockID,
		func(BlockMaterializationPhase) error {
			storeCalls++
			return nil
		},
		func() error {
			return RegisterUploadedBlockTargetAndMapping(context.Background(), nil, "org", "repo", uploadReuseTestBlockID, "op", 1, mappingIsolationTarget(), "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
		}, nil, nil)

	if !errors.Is(err, ErrBlockMappingWriteFailed) {
		t.Fatalf("error = %v, want ErrBlockMappingWriteFailed", err)
	}
	if IsRetryableBlockMaterializationError(err) {
		t.Fatalf("error = %v is retryable; an exhausted mapping budget must not restart the mint-capable store phase", err)
	}
	if installs != 1 {
		t.Errorf("canonical installs = %d, want 1", installs)
	}
	if storeCalls != 1 {
		t.Errorf("store calls = %d, want 1; the store phase must not be re-entered", storeCalls)
	}
	if mappingCalls < 2 {
		t.Errorf("mapping calls = %d, want the bounded in-place retry budget", mappingCalls)
	}
}

// TestMappingConflictIsNeverRetried keeps a verified external->different-internal
// conflict permanent. Retrying it would just re-run a write that cannot succeed.
func TestMappingConflictIsNeverRetried(t *testing.T) {
	fastBlockMaterializationRetries(t)

	installs, mappingCalls := 0, 0
	stubMaterializationRegister(t, &installs)

	oldMapping := writeBlockMappingForMaterializationFn
	t.Cleanup(func() { writeBlockMappingForMaterializationFn = oldMapping })
	writeBlockMappingForMaterializationFn = func(*db.DB, string, string, string, string) error {
		mappingCalls++
		return fmt.Errorf("mapping: %w", db.ErrBlockIDMappingConflict)
	}

	err := RegisterUploadedBlockTargetAndMapping(context.Background(), nil, "org", "repo", uploadReuseTestBlockID, "op", 1, mappingIsolationTarget(), "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if !errors.Is(err, db.ErrBlockIDMappingConflict) || !errors.Is(err, ErrBlockMappingWriteFailed) {
		t.Fatalf("error = %v, want a permanent mapping conflict", err)
	}
	if IsRetryableBlockMaterializationError(err) {
		t.Fatalf("error = %v is retryable, want permanent", err)
	}
	if mappingCalls != 1 {
		t.Errorf("mapping calls = %d, want 1; a conflict must not be retried", mappingCalls)
	}
}

// TestWebMappingTransientRetriesInPlaceAfterInstallApplied covers the web
// block-session variant, which uses the verified mapping writer and returns a
// bare conflict for its own 409 mapping.
func TestWebMappingTransientRetriesInPlaceAfterInstallApplied(t *testing.T) {
	fastBlockMaterializationRetries(t)

	installs, mappingCalls := 0, 0
	stubMaterializationRegister(t, &installs)

	oldMapping := writeVerifiedWebBlockMappingFn
	t.Cleanup(func() { writeVerifiedWebBlockMappingFn = oldMapping })
	writeVerifiedWebBlockMappingFn = func(*db.DB, string, string, string, string) error {
		mappingCalls++
		if mappingCalls == 1 {
			return errors.New("cassandra write timeout")
		}
		return nil
	}

	err := RegisterWebUploadedBlockTargetAndMapping(context.Background(), nil, "org", "repo", uploadReuseTestBlockID, "op", 1, mappingIsolationTarget(), "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err != nil {
		t.Fatalf("error = %v, want nil", err)
	}
	if installs != 1 || mappingCalls != 2 {
		t.Errorf("installs/mapping calls = %d/%d, want 1/2", installs, mappingCalls)
	}
}
