package db

import (
	"errors"
	"strings"
	"testing"
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

func withListLocksUnderStub(t *testing.T, stub func(*gocql.Session, gocql.UUID, string) ([]fileLockRow, error)) {
	t.Helper()
	old := listLocksUnderFn
	listLocksUnderFn = stub
	t.Cleanup(func() {
		listLocksUnderFn = old
	})
}

func withRelocateLockRowCASStub(t *testing.T, stub func(*gocql.Session, gocql.UUID, string, string, gocql.UUID, time.Time, gocql.UUID) (bool, error)) {
	t.Helper()
	old := relocateLockRowCASFn
	relocateLockRowCASFn = stub
	t.Cleanup(func() {
		relocateLockRowCASFn = old
	})
}

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

func TestRelocateLocksUnder_NotAppliedRowsReturnUnavailable(t *testing.T) {
	repoUUID, err := gocql.RandomUUID()
	if err != nil {
		t.Fatalf("RandomUUID() repo error = %v", err)
	}
	userUUID, err := gocql.RandomUUID()
	if err != nil {
		t.Fatalf("RandomUUID() user error = %v", err)
	}
	otherUUID, err := gocql.RandomUUID()
	if err != nil {
		t.Fatalf("RandomUUID() other error = %v", err)
	}

	lockedAt := time.Now().UTC()
	var calls []struct {
		oldPath string
		newPath string
	}

	withListLocksUnderStub(t, func(_ *gocql.Session, repo gocql.UUID, root string) ([]fileLockRow, error) {
		if repo != repoUUID {
			t.Fatalf("repoUUID = %s, want %s", repo, repoUUID)
		}
		if root != "/docs" {
			t.Fatalf("root = %q, want %q", root, "/docs")
		}
		return []fileLockRow{
			{path: "/docs", lockedBy: userUUID, lockedAt: lockedAt},
			{path: "/docs/sub/a.docx", lockedBy: userUUID, lockedAt: lockedAt},
			{path: "/docs/teammate.docx", lockedBy: otherUUID, lockedAt: lockedAt},
		}, nil
	})
	withRelocateLockRowCASStub(t, func(_ *gocql.Session, repo gocql.UUID, oldPath, newPath string, lockedBy gocql.UUID, gotLockedAt time.Time, requester gocql.UUID) (bool, error) {
		if repo != repoUUID {
			t.Fatalf("repoUUID = %s, want %s", repo, repoUUID)
		}
		if requester != userUUID {
			t.Fatalf("requester = %s, want %s", requester, userUUID)
		}
		if lockedBy != userUUID {
			t.Fatalf("lockedBy = %s, want %s", lockedBy, userUUID)
		}
		if !gotLockedAt.Equal(lockedAt) {
			t.Fatalf("lockedAt = %v, want %v", gotLockedAt, lockedAt)
		}
		calls = append(calls, struct {
			oldPath string
			newPath string
		}{oldPath: oldPath, newPath: newPath})
		switch oldPath {
		case "/docs":
			if newPath != "/archive" {
				t.Fatalf("newPath = %q, want %q", newPath, "/archive")
			}
			return false, nil
		case "/docs/sub/a.docx":
			if newPath != "/archive/sub/a.docx" {
				t.Fatalf("newPath = %q, want %q", newPath, "/archive/sub/a.docx")
			}
			return true, nil
		default:
			t.Fatalf("unexpected relocated path %q", oldPath)
			return false, nil
		}
	})

	err = RelocateLocksUnder(&gocql.Session{}, repoUUID.String(), "/docs", "/archive", userUUID.String())
	if !errors.Is(err, ErrFileLockStatusUnavailable) {
		t.Fatalf("err = %v, want %v", err, ErrFileLockStatusUnavailable)
	}
	if err == nil {
		t.Fatal("err = nil, want relocation failure")
	}
	if !strings.Contains(err.Error(), "conditional lock relocation not applied for 1 path(s)") {
		t.Fatalf("err = %q, want not-applied count", err.Error())
	}
	if !strings.Contains(err.Error(), "[/docs]") {
		t.Fatalf("err = %q, want stale source path", err.Error())
	}
	if strings.Contains(err.Error(), "/docs/sub/a.docx") {
		t.Fatalf("err = %q, want only not-applied paths listed", err.Error())
	}
	if len(calls) != 2 {
		t.Fatalf("relocate calls = %d, want 2", len(calls))
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
