package v2

import (
	"errors"
	"testing"
	"time"
)

func TestRetryUploadedBlockMaterializationRetriesStoreFence(t *testing.T) {
	oldDelay := libraryHeadMutationRetryDelay
	oldMaxDelay := libraryHeadMutationRetryMaxDelay
	oldJitter := libraryHeadMutationRetryJitter
	oldSleep := registerUploadedBlockSleepFn
	t.Cleanup(func() {
		libraryHeadMutationRetryDelay = oldDelay
		libraryHeadMutationRetryMaxDelay = oldMaxDelay
		libraryHeadMutationRetryJitter = oldJitter
		registerUploadedBlockSleepFn = oldSleep
	})

	libraryHeadMutationRetryDelay = time.Millisecond
	libraryHeadMutationRetryMaxDelay = time.Millisecond
	libraryHeadMutationRetryJitter = 0
	var slept []time.Duration
	registerUploadedBlockSleepFn = func(delay time.Duration) {
		slept = append(slept, delay)
	}

	storeCalls := 0
	materializeCalls := 0
	err := RetryUploadedBlockMaterialization("UploadFile", "block-1", func() error {
		storeCalls++
		if storeCalls == 1 {
			return ErrBlockDeleteInProgress
		}
		return nil
	}, func() error {
		materializeCalls++
		return nil
	}, nil, nil)
	if err != nil {
		t.Fatalf("RetryUploadedBlockMaterialization() error = %v, want nil", err)
	}
	if storeCalls != 2 {
		t.Fatalf("storeCalls = %d, want 2", storeCalls)
	}
	if materializeCalls != 1 {
		t.Fatalf("materializeCalls = %d, want 1", materializeCalls)
	}
	if len(slept) != 1 || slept[0] != time.Millisecond {
		t.Fatalf("slept = %#v, want []time.Duration{time.Millisecond}", slept)
	}
}

func TestProbeUploadedBlockReuseReturnsUnknownErrorWithoutSession(t *testing.T) {
	probe, err := ProbeUploadedBlockReuse(nil, "org-1", "block-1")
	if err == nil {
		t.Fatal("ProbeUploadedBlockReuse() error = nil, want error")
	}
	if probe.Decision != 0 {
		t.Fatalf("decision = %v, want BlockReuseUnknownError", probe.Decision)
	}
}

func TestRetryUploadedBlockMaterializationReturnsNonRetryableStoreError(t *testing.T) {
	wantErr := errors.New("boom")
	err := RetryUploadedBlockMaterialization("UploadFile", "block-1", func() error {
		return wantErr
	}, func() error {
		t.Fatal("materialize should not run after non-retryable store error")
		return nil
	}, nil, nil)
	if !errors.Is(err, wantErr) {
		t.Fatalf("RetryUploadedBlockMaterialization() error = %v, want %v", err, wantErr)
	}
}
