package db

import (
	"errors"
	"testing"
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

func TestReadFileLock_NilSessionReturnsUnavailable(t *testing.T) {
	repoUUID, err := gocql.RandomUUID()
	if err != nil {
		t.Fatalf("RandomUUID() error = %v", err)
	}

	_, _, err = ReadFileLock(nil, repoUUID, "/file.txt")
	if !errors.Is(err, ErrFileLockStatusUnavailable) {
		t.Fatalf("err = %v, want %v", err, ErrFileLockStatusUnavailable)
	}
}

func TestFileLockedByOther_InvalidRepoIDDoesNotBlock(t *testing.T) {
	blocked, ownerID, err := FileLockedByOther(nil, "not-a-uuid", "/file.txt", "user-1")
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if blocked {
		t.Fatal("blocked = true, want false")
	}
	if ownerID != "" {
		t.Fatalf("ownerID = %q, want empty string", ownerID)
	}
}

func TestRewriteLockedPath(t *testing.T) {
	cases := []struct {
		name             string
		oldRoot, newRoot string
		p                string
		want             string
	}{
		{"file renamed in place", "/docs/a.docx", "/docs/b.docx", "/docs/a.docx", "/docs/b.docx"},
		{"descendant follows folder rename", "/docs", "/archive", "/docs/a.docx", "/archive/a.docx"},
		{"nested descendant", "/docs", "/archive", "/docs/sub/a.docx", "/archive/sub/a.docx"},
		{"folder root itself", "/docs", "/archive", "/docs", "/archive"},
		{"move into another folder", "/docs", "/team/docs", "/docs/a.docx", "/team/docs/a.docx"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := rewriteLockedPath(tc.oldRoot, tc.newRoot, tc.p); got != tc.want {
				t.Fatalf("rewriteLockedPath(%q,%q,%q) = %q, want %q", tc.oldRoot, tc.newRoot, tc.p, got, tc.want)
			}
		})
	}
}

func TestClearLocksUnder_NilSessionReturnsUnavailable(t *testing.T) {
	err := ClearLocksUnder(nil, "repo-1", "/file.txt", "user-1")
	if !errors.Is(err, ErrFileLockStatusUnavailable) {
		t.Fatalf("err = %v, want %v", err, ErrFileLockStatusUnavailable)
	}
}

func TestRelocateLocksUnder_NilSessionReturnsUnavailable(t *testing.T) {
	err := RelocateLocksUnder(nil, "repo-1", "/old.txt", "/new.txt", "user-1")
	if !errors.Is(err, ErrFileLockStatusUnavailable) {
		t.Fatalf("err = %v, want %v", err, ErrFileLockStatusUnavailable)
	}
}

func TestAcquireFileLock_NilSessionReturnsUnavailable(t *testing.T) {
	result, ownerID, err := AcquireFileLock(nil, "repo-1", "/file.txt", "user-1", time.Now().UTC())
	if !errors.Is(err, ErrFileLockStatusUnavailable) {
		t.Fatalf("err = %v, want %v", err, ErrFileLockStatusUnavailable)
	}
	if result != LockConflict {
		t.Fatalf("result = %v, want %v", result, LockConflict)
	}
	if ownerID != "" {
		t.Fatalf("ownerID = %q, want empty string", ownerID)
	}
}

func TestReleaseFileLock_NilSessionReturnsUnavailable(t *testing.T) {
	released, ownerID, err := ReleaseFileLock(nil, "repo-1", "/file.txt", "user-1")
	if !errors.Is(err, ErrFileLockStatusUnavailable) {
		t.Fatalf("err = %v, want %v", err, ErrFileLockStatusUnavailable)
	}
	if released {
		t.Fatal("released = true, want false")
	}
	if ownerID != "" {
		t.Fatalf("ownerID = %q, want empty string", ownerID)
	}
}
