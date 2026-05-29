package db

import (
	"errors"
	"testing"
	"time"
)

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