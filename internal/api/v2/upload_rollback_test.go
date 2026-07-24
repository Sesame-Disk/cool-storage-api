package v2

import (
	"errors"
	"strings"
	"testing"

	"github.com/Sesame-Disk/sesamefs/internal/db"
)

func TestRegisterUploadedBlockAndMapping_WritesMappingAfterMetadata(t *testing.T) {
	oldRegister := registerUploadedBlockForMaterializationFn
	oldWriteMapping := writeBlockMappingForMaterializationFn
	t.Cleanup(func() {
		registerUploadedBlockForMaterializationFn = oldRegister
		writeBlockMappingForMaterializationFn = oldWriteMapping
	})

	var calls []string
	registerUploadedBlockForMaterializationFn = func(_ *db.DB, _, _, _, _ string, _ int, _, _, sha1ID string) error {
		calls = append(calls, "register")
		if sha1ID != "ext-1" {
			t.Fatalf("sha1ID = %q, want ext-1", sha1ID)
		}
		return nil
	}
	writeBlockMappingForMaterializationFn = func(_ *db.DB, _, _, _, _ string) error {
		calls = append(calls, "mapping")
		return nil
	}

	err := RegisterUploadedBlockAndMapping(nil, "org-1", "repo-1", "int-1", "op-1", 123, "hot", "", "ext-1")
	if err != nil {
		t.Fatalf("RegisterUploadedBlockAndMapping returned error: %v", err)
	}
	want := []string{"register", "mapping"}
	if len(calls) != len(want) {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Fatalf("calls[%d] = %q, want %q (full=%#v)", i, calls[i], want[i], calls)
		}
	}
}

// A mapping failure is reported without invoking any eager provisional-reference
// cleanup. The up: reference written by the register step remains TTL-bound.
func TestRegisterUploadedBlockAndMapping_ReportsMappingFailure(t *testing.T) {
	oldRegister := registerUploadedBlockForMaterializationFn
	oldWriteMapping := writeBlockMappingForMaterializationFn
	t.Cleanup(func() {
		registerUploadedBlockForMaterializationFn = oldRegister
		writeBlockMappingForMaterializationFn = oldWriteMapping
	})

	registerUploadedBlockForMaterializationFn = func(_ *db.DB, _, _, _, _ string, _ int, _, _, _ string) error {
		return nil
	}
	wantErr := errors.New("mapping boom")
	writeBlockMappingForMaterializationFn = func(_ *db.DB, _, _, _, _ string) error {
		return wantErr
	}

	err := RegisterUploadedBlockAndMapping(nil, "org-1", "repo-1", "int-1", "op-1", 123, "hot", "", "ext-1")
	if !errors.Is(err, ErrBlockMappingWriteFailed) {
		t.Fatalf("error = %v, want ErrBlockMappingWriteFailed", err)
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want underlying cause %v", err, wantErr)
	}
}

func TestRegisterUploadedBlockAndMapping_TransientMappingFailureIsRetryable(t *testing.T) {
	oldRegister := registerUploadedBlockForMaterializationFn
	oldWriteMapping := writeBlockMappingForMaterializationFn
	t.Cleanup(func() {
		registerUploadedBlockForMaterializationFn = oldRegister
		writeBlockMappingForMaterializationFn = oldWriteMapping
	})

	registerUploadedBlockForMaterializationFn = func(*db.DB, string, string, string, string, int, string, string, string) error {
		return nil
	}
	wantCause := errors.New("cassandra timeout")
	writeBlockMappingForMaterializationFn = func(*db.DB, string, string, string, string) error { return wantCause }

	err := RegisterUploadedBlockAndMapping(nil, "org-1", "repo-1", "int-1", "op-1", 123, "hot", "", "ext-1")
	if !IsRetryableBlockMaterializationError(err) {
		t.Fatalf("error = %v, want retryable (ErrBlockMaterializationTransient)", err)
	}
	if !errors.Is(err, ErrBlockMappingWriteFailed) {
		t.Fatalf("error = %v, want ErrBlockMappingWriteFailed preserved", err)
	}
	if !errors.Is(err, wantCause) {
		t.Fatalf("error = %v, want underlying cause preserved via %%w", err)
	}
}

func TestRegisterWebUploadedBlockAndMapping_ConflictIsNotRetryable(t *testing.T) {
	oldRegister := registerUploadedBlockForMaterializationFn
	oldWriteMapping := writeVerifiedWebBlockMappingFn
	t.Cleanup(func() {
		registerUploadedBlockForMaterializationFn = oldRegister
		writeVerifiedWebBlockMappingFn = oldWriteMapping
	})

	registerUploadedBlockForMaterializationFn = func(*db.DB, string, string, string, string, int, string, string, string) error {
		return nil
	}
	writeVerifiedWebBlockMappingFn = func(*db.DB, string, string, string, string) error {
		return db.ErrBlockIDMappingConflict
	}

	err := RegisterWebUploadedBlockAndMapping(nil, "org-1", "repo-1", "int-1", "op-1", 123, "hot", "", "ext-1")
	if !errors.Is(err, db.ErrBlockIDMappingConflict) {
		t.Fatalf("error = %v, want db.ErrBlockIDMappingConflict", err)
	}
	if IsRetryableBlockMaterializationError(err) {
		t.Fatalf("a permanent mapping conflict must not be retryable: %v", err)
	}
}

func TestRegisterUploadedBlockAndMapping_SkipsMappingWithoutExternalID(t *testing.T) {
	oldRegister := registerUploadedBlockForMaterializationFn
	oldWriteMapping := writeBlockMappingForMaterializationFn
	t.Cleanup(func() {
		registerUploadedBlockForMaterializationFn = oldRegister
		writeBlockMappingForMaterializationFn = oldWriteMapping
	})

	registerUploadedBlockForMaterializationFn = func(_ *db.DB, _, _, _, _ string, _ int, _, _, sha1ID string) error {
		if strings.TrimSpace(sha1ID) != "" {
			t.Fatalf("sha1ID = %q, want empty", sha1ID)
		}
		return nil
	}
	writeCalled := false
	writeBlockMappingForMaterializationFn = func(_ *db.DB, _, _, _, _ string) error {
		writeCalled = true
		return nil
	}

	err := RegisterUploadedBlockAndMapping(nil, "org-1", "repo-1", "int-1", "op-1", 123, "hot", "", "  ")
	if err != nil {
		t.Fatalf("RegisterUploadedBlockAndMapping returned error: %v", err)
	}
	if writeCalled {
		t.Fatal("mapping write should be skipped when external block ID is empty")
	}
}

func TestRegisterUploadedBlockAndMapping_StopsOnRegisterFailure(t *testing.T) {
	oldRegister := registerUploadedBlockForMaterializationFn
	oldWriteMapping := writeBlockMappingForMaterializationFn
	t.Cleanup(func() {
		registerUploadedBlockForMaterializationFn = oldRegister
		writeBlockMappingForMaterializationFn = oldWriteMapping
	})

	wantErr := errors.New("register boom")
	registerUploadedBlockForMaterializationFn = func(*db.DB, string, string, string, string, int, string, string, string) error {
		return wantErr
	}
	writeCalled := false
	writeBlockMappingForMaterializationFn = func(*db.DB, string, string, string, string) error {
		writeCalled = true
		return nil
	}

	err := RegisterUploadedBlockAndMapping(nil, "org-1", "repo-1", "int-1", "op-1", 123, "hot", "", "ext-1")
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
	if writeCalled {
		t.Fatal("mapping write should not run after register failure")
	}
}
