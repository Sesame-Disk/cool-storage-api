package db

import (
	"errors"
	"fmt"
	"strings"
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

// ErrFileLockStatusUnavailable indicates the lock table could not be queried, so
// callers must fail closed instead of assuming the file is unlocked.
var ErrFileLockStatusUnavailable = errors.New("file lock status unavailable")

// FileLock is the current exclusive lock on a path inside a library.
//
// The schema (locked_files) carries no lock_type or expiry yet, so every lock is
// treated as a manual exclusive lock: a single writer who must be the only one
// allowed to mutate the file until they release it. See
// docs/FILE-LOCKING-DESIGN.md for the planned OnlyOffice (online_office) lock type.
type FileLock struct {
	LockedBy string // canonical UUID string of the lock holder
	LockedAt time.Time
}

// ReadFileLock returns the lock on (repoUUID, path), or ok=false when the path is
// not locked. Query failures are reported to the caller so write paths can fail
// closed instead of silently bypassing lock enforcement.
func ReadFileLock(session *gocql.Session, repoUUID gocql.UUID, path string) (FileLock, bool, error) {
	if session == nil {
		return FileLock{}, false, ErrFileLockStatusUnavailable
	}

	var lockedBy gocql.UUID
	var lockedAt time.Time
	if err := session.Query(
		`SELECT locked_by, locked_at FROM locked_files WHERE repo_id = ? AND path = ?`,
		repoUUID, path,
	).Scan(&lockedBy, &lockedAt); err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return FileLock{}, false, nil
		}
		return FileLock{}, false, fmt.Errorf("%w: %v", ErrFileLockStatusUnavailable, err)
	}
	return FileLock{LockedBy: lockedBy.String(), LockedAt: lockedAt}, true, nil
}

// FileLockedByOther reports whether (repoID, path) is locked by a user other than
// userID. ownerID is the lock holder's UUID (empty when the path is unlocked). The
// lock holder themselves and any unlocked path are never reported as blocked, so a
// user can always overwrite or refresh their own lock. A malformed repoID is
// reported as "not blocked" so the caller's own id validation produces the right
// error.
func FileLockedByOther(session *gocql.Session, repoID, path, userID string) (blocked bool, ownerID string, err error) {
	repoUUID, err := gocql.ParseUUID(repoID)
	if err != nil {
		return false, "", nil
	}
	lock, ok, err := ReadFileLock(session, repoUUID, path)
	if err != nil {
		return false, "", err
	}
	if !ok {
		return false, "", nil
	}
	if reqUUID, err := gocql.ParseUUID(userID); err == nil {
		if owner, oerr := gocql.ParseUUID(lock.LockedBy); oerr == nil && owner == reqUUID {
			return false, lock.LockedBy, nil
		}
	}
	return true, lock.LockedBy, nil
}

// fileLockRow is a lock row plus its path, used for subtree maintenance.
type fileLockRow struct {
	path     string
	lockedBy gocql.UUID
	lockedAt time.Time
}

// RepoLockedFile is one active lock, as returned by the desktop/SeaDrive client's
// locked-files polling endpoint.
type RepoLockedFile struct {
	Path     string
	LockedBy string // canonical UUID string of the lock holder
}

// ListRepoLocks returns every active lock in repoID. Unlike ReadFileLock this is
// not scoped to a single path: locked_files is partitioned by repo_id, so a full
// per-repo scan is cheap and this is what the desktop client's locked-files
// endpoint needs (it wants the whole repo's lock state in one call). An
// unparseable repoID is reported as "no locks" rather than an error, matching
// how the sync protocol's other unauthenticated per-repo endpoints degrade.
func ListRepoLocks(session *gocql.Session, repoID string) ([]RepoLockedFile, error) {
	if session == nil {
		return nil, ErrFileLockStatusUnavailable
	}
	repoUUID, err := gocql.ParseUUID(repoID)
	if err != nil {
		return nil, nil
	}
	iter := session.Query(
		`SELECT path, locked_by FROM locked_files WHERE repo_id = ?`, repoUUID,
	).Iter()
	var locks []RepoLockedFile
	var path string
	var lockedBy gocql.UUID
	for iter.Scan(&path, &lockedBy) {
		locks = append(locks, RepoLockedFile{Path: path, LockedBy: lockedBy.String()})
	}
	if err := iter.Close(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrFileLockStatusUnavailable, err)
	}
	return locks, nil
}

var listLocksUnderFn = listLocksUnder

var relocateLockRowCASFn = func(session *gocql.Session, repoUUID gocql.UUID, oldPath, newPath string, lockedBy gocql.UUID, lockedAt time.Time, userUUID gocql.UUID) (bool, error) {
	batch := session.Batch(gocql.LoggedBatch)
	batch.Query(
		`INSERT INTO locked_files (repo_id, path, locked_by, locked_at) VALUES (?, ?, ?, ?) IF NOT EXISTS`,
		repoUUID, newPath, lockedBy, lockedAt,
	)
	batch.Query(
		`DELETE FROM locked_files WHERE repo_id = ? AND path = ? IF locked_by = ?`,
		repoUUID, oldPath, userUUID,
	)
	applied, iter, err := batch.MapExecCAS(map[string]interface{}{})
	if iter != nil {
		_ = iter.Close()
	}
	return applied, err
}

// listLocksUnder returns every lock at exactly root or beneath root+"/". Paths are
// expected pre-normalized by the caller (package db cannot reuse the v2 normalizer).
func listLocksUnder(session *gocql.Session, repoUUID gocql.UUID, root string) ([]fileLockRow, error) {
	prefix := strings.TrimSuffix(root, "/") + "/"
	iter := session.Query(
		`SELECT path, locked_by, locked_at FROM locked_files WHERE repo_id = ?`, repoUUID,
	).Iter()
	var rows []fileLockRow
	var p string
	var by gocql.UUID
	var at time.Time
	for iter.Scan(&p, &by, &at) {
		if p == root || strings.HasPrefix(p, prefix) {
			rows = append(rows, fileLockRow{path: p, lockedBy: by, lockedAt: at})
		}
	}
	if err := iter.Close(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrFileLockStatusUnavailable, err)
	}
	return rows, nil
}

// ClearLocksUnder removes every lock at root or beneath it. Intended to run AFTER a
// successful delete of root: the subtree pre-check guarantees any remaining locks are
// the operator's own, so this only drops the operator's own (now-orphaned) locks.
// Best-effort: a maintenance failure is returned but must not undo the delete itself.
func ClearLocksUnder(session *gocql.Session, repoID, root, userID string) error {
	if session == nil {
		return ErrFileLockStatusUnavailable
	}
	repoUUID, err := gocql.ParseUUID(repoID)
	if err != nil {
		return nil
	}
	userUUID, err := gocql.ParseUUID(userID)
	if err != nil {
		return nil
	}
	rows, err := listLocksUnderFn(session, repoUUID, root)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if r.lockedBy != userUUID {
			continue
		}
		prior := map[string]interface{}{}
		applied, derr := session.Query(
			`DELETE FROM locked_files WHERE repo_id = ? AND path = ? IF locked_by = ?`,
			repoUUID, r.path, userUUID,
		).MapScanCAS(prior)
		if derr != nil {
			return fmt.Errorf("%w: %v", ErrFileLockStatusUnavailable, derr)
		}
		if !applied {
			if owner, ok := prior["locked_by"].(gocql.UUID); ok && owner != (gocql.UUID{}) && owner != userUUID {
				continue
			}
		}
	}
	return nil
}

// RelocateLocksUnder rewrites every lock path at oldRoot or beneath it so its prefix
// becomes newRoot, preserving the holder and timestamp. Intended to run AFTER a
// successful rename/move oldRoot→newRoot so the operator's own locks follow the file
// instead of being left dangling at the old path. Paths are expected pre-normalized.
func RelocateLocksUnder(session *gocql.Session, repoID, oldRoot, newRoot, userID string) error {
	if session == nil {
		return ErrFileLockStatusUnavailable
	}
	repoUUID, err := gocql.ParseUUID(repoID)
	if err != nil {
		return nil
	}
	userUUID, err := gocql.ParseUUID(userID)
	if err != nil {
		return nil
	}
	rows, err := listLocksUnderFn(session, repoUUID, oldRoot)
	if err != nil {
		return err
	}
	var notApplied []string
	for _, r := range rows {
		if r.lockedBy != userUUID {
			continue
		}
		newPath := rewriteLockedPath(oldRoot, newRoot, r.path)
		if newPath == r.path {
			continue
		}
		// Move the lock row atomically: insert at the new path and delete the old in a
		// single conditional batch on the same partition (repo_id). Either both apply
		// or neither does, so a failure can never leave the lock at BOTH paths. The
		// conditions keep it safe: insert only if the new path is free, delete only if
		// we still hold the old one.
		applied, berr := relocateLockRowCASFn(session, repoUUID, r.path, newPath, r.lockedBy, r.lockedAt, userUUID)
		if berr != nil {
			return fmt.Errorf("%w: %v", ErrFileLockStatusUnavailable, berr)
		}
		if !applied {
			// The conditional batch did not apply: the destination path was already
			// locked, or we no longer held the source lock. Because the batch is atomic,
			// nothing changed — so the source row may now be a stale lock on a path that
			// the rename/move already vacated, with the destination left unlocked. Do
			// NOT report success for this row; surface it so the caller logs it. Keep
			// relocating the remaining rows rather than abandoning them mid-subtree.
			notApplied = append(notApplied, r.path)
		}
	}
	if len(notApplied) > 0 {
		return fmt.Errorf("%w: conditional lock relocation not applied for %d path(s): %v",
			ErrFileLockStatusUnavailable, len(notApplied), notApplied)
	}
	return nil
}

// rewriteLockedPath maps a locked path under oldRoot onto newRoot, preserving the
// suffix. The root itself (p == oldRoot) maps to newRoot. Inputs are pre-normalized
// (leading slash, no trailing slash except "/").
func rewriteLockedPath(oldRoot, newRoot, p string) string {
	if p == oldRoot {
		return newRoot
	}
	return newRoot + strings.TrimPrefix(p, oldRoot)
}

// LockAcquireResult is the outcome of an attempt to acquire or refresh a lock.
type LockAcquireResult int

const (
	// LockAcquired means the lock was newly taken by the requester.
	LockAcquired LockAcquireResult = iota
	// LockRefreshed means the requester already held the lock; its timestamp was bumped.
	LockRefreshed
	// LockConflict means another user holds the lock; ownerID identifies them.
	LockConflict
)

const acquireFileLockMaxAttempts = 4

// AcquireFileLock atomically takes the lock on (repoID, path) for userID, or refreshes
// it when userID already holds it. It uses a compare-and-set (INSERT ... IF NOT EXISTS)
// so two concurrent acquirers cannot both win — the loser is reported as LockConflict
// with the current owner. This replaces the previous check-then-INSERT, which raced in
// multi-region clusters (both requests could pass a read check, then upsert).
func AcquireFileLock(session *gocql.Session, repoID, path, userID string, lockedAt time.Time) (LockAcquireResult, string, error) {
	if session == nil {
		return LockConflict, "", ErrFileLockStatusUnavailable
	}
	repoUUID, err := gocql.ParseUUID(repoID)
	if err != nil {
		return LockConflict, "", fmt.Errorf("invalid repo id: %w", err)
	}
	userUUID, err := gocql.ParseUUID(userID)
	if err != nil {
		return LockConflict, "", fmt.Errorf("invalid user id: %w", err)
	}

	for attempt := 0; attempt < acquireFileLockMaxAttempts; attempt++ {
		prior := map[string]interface{}{}
		applied, err := session.Query(
			`INSERT INTO locked_files (repo_id, path, locked_by, locked_at) VALUES (?, ?, ?, ?) IF NOT EXISTS`,
			repoUUID, path, userUUID, lockedAt,
		).MapScanCAS(prior)
		if err != nil {
			return LockConflict, "", fmt.Errorf("%w: %v", ErrFileLockStatusUnavailable, err)
		}
		if applied {
			return LockAcquired, userID, nil
		}

		owner, _ := prior["locked_by"].(gocql.UUID)
		if owner != userUUID {
			return LockConflict, owner.String(), nil
		}

		refreshPrior := map[string]interface{}{}
		refreshed, err := session.Query(
			`UPDATE locked_files SET locked_at = ? WHERE repo_id = ? AND path = ? IF locked_by = ?`,
			lockedAt, repoUUID, path, userUUID,
		).MapScanCAS(refreshPrior)
		if err != nil {
			return LockConflict, "", fmt.Errorf("%w: %v", ErrFileLockStatusUnavailable, err)
		}
		if refreshed {
			return LockRefreshed, owner.String(), nil
		}
		if refreshOwner, ok := refreshPrior["locked_by"].(gocql.UUID); ok && refreshOwner != (gocql.UUID{}) && refreshOwner != userUUID {
			return LockConflict, refreshOwner.String(), nil
		}
	}

	return LockConflict, "", ErrFileLockStatusUnavailable
}

// ReleaseFileLock atomically deletes the lock on (repoID, path) only if userID holds it
// (DELETE ... IF locked_by = ?). It returns released=true when the lock was removed or
// was already absent (idempotent), and released=false with the current owner when
// another user holds it. This replaces the previous check-then-DELETE race.
func ReleaseFileLock(session *gocql.Session, repoID, path, userID string) (released bool, ownerID string, err error) {
	if session == nil {
		return false, "", ErrFileLockStatusUnavailable
	}
	repoUUID, perr := gocql.ParseUUID(repoID)
	if perr != nil {
		return false, "", fmt.Errorf("invalid repo id: %w", perr)
	}
	userUUID, perr := gocql.ParseUUID(userID)
	if perr != nil {
		return false, "", fmt.Errorf("invalid user id: %w", perr)
	}

	prior := map[string]interface{}{}
	applied, qerr := session.Query(
		`DELETE FROM locked_files WHERE repo_id = ? AND path = ? IF locked_by = ?`,
		repoUUID, path, userUUID,
	).MapScanCAS(prior)
	if qerr != nil {
		return false, "", fmt.Errorf("%w: %v", ErrFileLockStatusUnavailable, qerr)
	}
	if applied {
		return true, "", nil
	}
	owner, ok := prior["locked_by"].(gocql.UUID)
	if !ok || owner == (gocql.UUID{}) {
		// No row existed → already unlocked; treat as idempotent success.
		return true, "", nil
	}
	return false, owner.String(), nil
}

// SubtreeLockedByOther reports whether targetPath itself OR any descendant beneath it
// is locked by a user other than userID. Directory-capable operations (rename, move,
// delete) use this so a folder action cannot bypass a lock on a file inside it. When
// targetPath is a plain file this degenerates to the same answer as FileLockedByOther.
//
// It scans the (small) per-repo lock partition once. A query failure is surfaced as
// ErrFileLockStatusUnavailable so callers fail closed rather than silently allowing a
// write past an unverifiable lock.
func SubtreeLockedByOther(session *gocql.Session, repoID, targetPath, userID string) (blocked bool, ownerID string, err error) {
	if session == nil {
		return false, "", ErrFileLockStatusUnavailable
	}
	repoUUID, perr := gocql.ParseUUID(repoID)
	if perr != nil {
		return false, "", nil
	}

	// Descendants live under targetPath + "/"; the path itself is matched exactly.
	prefix := strings.TrimSuffix(targetPath, "/") + "/"
	reqUUID, reqErr := gocql.ParseUUID(userID)

	iter := session.Query(
		`SELECT path, locked_by FROM locked_files WHERE repo_id = ?`, repoUUID,
	).Iter()
	var lockedPath string
	var lockedBy gocql.UUID
	for iter.Scan(&lockedPath, &lockedBy) {
		if lockedPath != targetPath && !strings.HasPrefix(lockedPath, prefix) {
			continue
		}
		// A lock the requester themselves holds never blocks their own operation.
		if reqErr == nil && lockedBy == reqUUID {
			continue
		}
		_ = iter.Close()
		return true, lockedBy.String(), nil
	}
	if cerr := iter.Close(); cerr != nil {
		return false, "", fmt.Errorf("%w: %v", ErrFileLockStatusUnavailable, cerr)
	}
	return false, "", nil
}
