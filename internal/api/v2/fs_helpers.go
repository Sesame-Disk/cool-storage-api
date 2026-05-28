package v2

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"path"
	"strings"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/streaming"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

// FSHelper provides helper functions for file system operations
type FSHelper struct {
	db *db.DB
}

// ErrLibraryHeadConflict indicates that the library HEAD changed between the
// caller's read and publish steps.
var ErrLibraryHeadConflict = errors.New("library HEAD was modified concurrently")

// ErrLibraryHeadPublicationUnknown indicates that a conditional HEAD publish may
// already be visible, but we could not confirm the outcome after an ambiguous
// CAS error. Callers must not roll back promoted blocks on this error.
var ErrLibraryHeadPublicationUnknown = errors.New("library HEAD publication outcome is unknown")

// ErrStorageQuotaExceeded indicates the caller's storage quota would be exceeded.
var ErrStorageQuotaExceeded = errors.New("storage quota exceeded")

// ErrBlockMutationOutcomeUnknown indicates a conditional block ref-count
// mutation may have applied, but the post-error confirmation read could not
// attribute the visible state to this operation.
var ErrBlockMutationOutcomeUnknown = errors.New("block ref-count mutation outcome is unknown")

// ErrBlockDeleteInProgress indicates the GC worker has claimed the block for
// deletion. Callers should retry from the full upload/store path so S3 PUT and
// provisional-reference registration happen after the claim clears.
var ErrBlockDeleteInProgress = errors.New("block delete is in progress")

// NewFSHelper creates a new FSHelper instance
func NewFSHelper(database *db.DB) *FSHelper {
	return &FSHelper{db: database}
}

var resolveStoredBlockIDsFn = func(h *FSHelper, orgID string, blockIDs []string) ([]string, error) {
	return h.resolveStoredBlockIDs(orgID, blockIDs)
}

// PathTraverseResult contains the result of traversing to a path
type PathTraverseResult struct {
	TargetFSID   string    // FS ID of the target (file or dir)
	TargetEntry  *FSEntry  // Entry for the target
	ParentFSID   string    // FS ID of the parent directory
	ParentPath   string    // Path of the parent directory
	Ancestors    []string  // FS IDs from root to parent (for rebuilding)
	AncestorPath []string  // Path components from root to parent
	Entries      []FSEntry // Entries in parent directory
}

// LibraryHeadSnapshot captures the canonical HEAD and root tree that a caller
// used to build a mutation. expectedHead at publish time must come from this
// same snapshot, not from a later fresh read.
type LibraryHeadSnapshot struct {
	OrgID        string
	HeadCommitID string
	RootFSID     string
}

// ValidateExpectedHead rejects attempts to publish a tree built from one HEAD
// while comparing against another.
func (s *LibraryHeadSnapshot) ValidateExpectedHead(expectedHead string) error {
	if s == nil {
		return fmt.Errorf("library head snapshot is required")
	}
	if expectedHead != s.HeadCommitID {
		return fmt.Errorf("expectedHead %s does not match snapshot head %s", expectedHead, s.HeadCommitID)
	}
	return nil
}

// GetRootFSID gets the root fs_id from a library's head commit
func (h *FSHelper) GetRootFSID(repoID string) (string, string, error) {
	_, headCommitID, err := h.getCanonicalHeadCommit(repoID)
	if err != nil {
		return "", "", err
	}

	if headCommitID == "" {
		return "", "", fmt.Errorf("library has no head commit")
	}

	// Get root_fs_id from commit
	var rootFSID string
	err = h.db.Session().Query(`
		SELECT root_fs_id FROM commits WHERE library_id = ? AND commit_id = ?
	`, repoID, headCommitID).Scan(&rootFSID)
	if err != nil {
		return "", "", fmt.Errorf("commit not found: %w", err)
	}

	return rootFSID, headCommitID, nil
}

func (h *FSHelper) getCanonicalHeadCommit(repoID string) (string, string, error) {
	var orgID string
	if err := h.db.Session().Query(`
		SELECT org_id FROM libraries_by_id WHERE library_id = ?
	`, repoID).Scan(&orgID); err != nil {
		return "", "", fmt.Errorf("library not found: %w", err)
	}

	var headCommitID string
	if err := h.db.Session().Query(`
		SELECT head_commit_id FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, repoID).Scan(&headCommitID); err != nil {
		return "", "", fmt.Errorf("library not found: %w", err)
	}

	return orgID, headCommitID, nil
}

// GetLibraryHeadSnapshot resolves the canonical HEAD once and returns the root
// tree that callers should use for stale-sensitive metadata mutations.
func (h *FSHelper) GetLibraryHeadSnapshot(repoID string) (*LibraryHeadSnapshot, error) {
	orgID, headCommitID, err := h.getCanonicalHeadCommit(repoID)
	if err != nil {
		return nil, err
	}
	if headCommitID == "" {
		return nil, fmt.Errorf("library has no head commit")
	}

	var rootFSID string
	err = h.db.Session().Query(`
		SELECT root_fs_id FROM commits WHERE library_id = ? AND commit_id = ?
	`, repoID, headCommitID).Scan(&rootFSID)
	if err != nil {
		return nil, fmt.Errorf("commit not found: %w", err)
	}

	return &LibraryHeadSnapshot{
		OrgID:        orgID,
		HeadCommitID: headCommitID,
		RootFSID:     rootFSID,
	}, nil
}

// TraverseToPathAtHead resolves the current canonical HEAD once and traverses
// the requested path from that fixed root tree.
func (h *FSHelper) TraverseToPathAtHead(repoID, targetPath string) (*PathTraverseResult, *LibraryHeadSnapshot, error) {
	snapshot, err := h.GetLibraryHeadSnapshot(repoID)
	if err != nil {
		return nil, nil, err
	}

	result, err := h.TraverseToPathFromRoot(repoID, snapshot.RootFSID, targetPath)
	if err != nil {
		return nil, nil, err
	}

	return result, snapshot, nil
}

// TraverseToPathFromSnapshot traverses a path from an already-fixed HEAD
// snapshot so callers can resolve multiple paths against the same tree.
func (h *FSHelper) TraverseToPathFromSnapshot(repoID string, snapshot *LibraryHeadSnapshot, targetPath string) (*PathTraverseResult, error) {
	if snapshot == nil {
		return nil, fmt.Errorf("library head snapshot is required")
	}
	return h.TraverseToPathFromRoot(repoID, snapshot.RootFSID, targetPath)
}

// GetDirectoryEntries gets the entries from a directory fs_object
func (h *FSHelper) GetDirectoryEntries(repoID, fsID string) ([]FSEntry, error) {
	var entriesJSON string
	err := h.db.Session().Query(`
		SELECT dir_entries FROM fs_objects WHERE library_id = ? AND fs_id = ?
	`, repoID, fsID).Scan(&entriesJSON)
	if err != nil {
		if err == gocql.ErrNotFound {
			// Self-heal: missing fs_object is treated as an empty directory.
			// This can happen when the root fs_object was never persisted at library
			// creation time (e.g. a silent Cassandra write failure). Returning an
			// empty slice lets write operations (create file, mkdir…) proceed and
			// correct the state on the next commit.
			log.Printf("[GetDirectoryEntries] WARNING: fs_object not found for library=%s fs_id=%s, treating as empty directory", repoID, fsID)
			return []FSEntry{}, nil
		}
		return nil, fmt.Errorf("fs_object not found: %w", err)
	}

	var entries []FSEntry
	if entriesJSON != "" && entriesJSON != "[]" {
		if err := json.Unmarshal([]byte(entriesJSON), &entries); err != nil {
			return nil, fmt.Errorf("invalid directory data: %w", err)
		}
	}

	return entries, nil
}

// TraverseToPath traverses from root to the specified path, collecting ancestors
func (h *FSHelper) TraverseToPath(repoID, targetPath string) (*PathTraverseResult, error) {
	rootFSID, _, err := h.GetRootFSID(repoID)
	if err != nil {
		return nil, err
	}

	return h.TraverseToPathFromRoot(repoID, rootFSID, targetPath)
}

// TraverseToPathFromRoot traverses from a specific root FS ID to the specified path
// This is useful for traversing historical commits where the root differs from HEAD
func (h *FSHelper) TraverseToPathFromRoot(repoID, rootFSID, targetPath string) (*PathTraverseResult, error) {
	targetPath = normalizePath(targetPath)

	// Handle root path
	if targetPath == "/" {
		entries, err := h.GetDirectoryEntries(repoID, rootFSID)
		if err != nil {
			return nil, err
		}
		return &PathTraverseResult{
			TargetFSID:   rootFSID,
			ParentFSID:   "",
			ParentPath:   "",
			Ancestors:    []string{},
			AncestorPath: []string{},
			Entries:      entries,
		}, nil
	}

	// Split path into components
	parts := strings.Split(strings.Trim(targetPath, "/"), "/")

	// Track ancestors as we traverse
	ancestors := []string{rootFSID}
	ancestorPath := []string{"/"}
	currentFSID := rootFSID
	currentPath := "/"

	// Traverse to parent (all but last component)
	for i := 0; i < len(parts)-1; i++ {
		part := parts[i]
		if part == "" {
			continue
		}

		entries, err := h.GetDirectoryEntries(repoID, currentFSID)
		if err != nil {
			return nil, fmt.Errorf("failed to get directory %s: %w", currentPath, err)
		}

		found := false
		for _, entry := range entries {
			if entry.Name == part {
				if entry.Mode&0170000 != 040000 && entry.Mode != ModeDir {
					return nil, fmt.Errorf("path component %s is not a directory", part)
				}
				currentFSID = entry.ID
				if currentPath == "/" {
					currentPath = "/" + part
				} else {
					currentPath = currentPath + "/" + part
				}
				ancestors = append(ancestors, currentFSID)
				ancestorPath = append(ancestorPath, currentPath)
				found = true
				break
			}
		}

		if !found {
			return nil, fmt.Errorf("directory not found: %s", part)
		}
	}

	// Get parent directory entries
	entries, err := h.GetDirectoryEntries(repoID, currentFSID)
	if err != nil {
		return nil, fmt.Errorf("failed to get parent directory: %w", err)
	}

	// Find the target entry
	targetName := parts[len(parts)-1]
	var targetEntry *FSEntry
	var targetFSID string
	for _, entry := range entries {
		if entry.Name == targetName {
			entryCopy := entry
			targetEntry = &entryCopy
			targetFSID = entry.ID
			break
		}
	}

	return &PathTraverseResult{
		TargetFSID:   targetFSID,
		TargetEntry:  targetEntry,
		ParentFSID:   currentFSID,
		ParentPath:   currentPath,
		Ancestors:    ancestors,
		AncestorPath: ancestorPath,
		Entries:      entries,
	}, nil
}

// TraverseToParent traverses to the parent of the given path
func (h *FSHelper) TraverseToParent(repoID, targetPath string) (*PathTraverseResult, error) {
	parentPath := path.Dir(normalizePath(targetPath))
	if parentPath == "." {
		parentPath = "/"
	}
	return h.TraverseToPath(repoID, parentPath)
}

// CreateDirectoryFSObject creates a new fs_object for a directory and returns its ID
func (h *FSHelper) CreateDirectoryFSObject(repoID string, entries []FSEntry) (string, error) {
	entriesJSON, err := json.Marshal(entries)
	if err != nil {
		return "", fmt.Errorf("failed to marshal entries: %w", err)
	}

	// Calculate fs_id as SHA-1 of the EXACT JSON that will be returned by pack-fs
	// Seafile format: {"dirents":[...],"type":3,"version":1} (alphabetical key order)
	// CRITICAL: The hash MUST match what the client receives, or it can't store the object
	// CRITICAL: Must use map[string]interface{} which serializes keys alphabetically.
	// Using a struct would change field order and break hash matching.
	fsContent := map[string]interface{}{
		"version": 1,
		"type":    3, // SEAF_METADATA_TYPE_DIR
		"dirents": json.RawMessage(entriesJSON),
	}
	fsContentJSON, err := json.Marshal(fsContent)
	if err != nil {
		return "", fmt.Errorf("failed to marshal fs content: %w", err)
	}
	hash := sha1.Sum(fsContentJSON)
	fsID := hex.EncodeToString(hash[:])

	// Store in database
	err = h.db.Session().Query(`
		INSERT INTO fs_objects (library_id, fs_id, obj_type, dir_entries, mtime)
		VALUES (?, ?, ?, ?, ?)
	`, repoID, fsID, "dir", string(entriesJSON), time.Now().Unix()).Exec()
	if err != nil {
		return "", fmt.Errorf("failed to create fs_object: %w", err)
	}

	return fsID, nil
}

// RebuildPathToRoot rebuilds the path from a modified directory back to root
// Returns the new root fs_id
func (h *FSHelper) RebuildPathToRoot(repoID string, result *PathTraverseResult, newParentFSID string) (string, error) {
	return rebuildPathToRootWithHooks(repoID, result, newParentFSID, h.GetDirectoryEntries, h.CreateDirectoryFSObject)
}

func rebuildPathToRootWithHooks(repoID string, result *PathTraverseResult, newParentFSID string, getDirectoryEntries func(string, string) ([]FSEntry, error), createDirectoryFSObject func(string, []FSEntry) (string, error)) (string, error) {
	if result == nil {
		return "", fmt.Errorf("path traverse result is required")
	}
	if getDirectoryEntries == nil || createDirectoryFSObject == nil {
		return "", fmt.Errorf("rebuild path helpers are required")
	}
	if len(result.Ancestors) != len(result.AncestorPath) {
		return "", fmt.Errorf("path traverse result has %d ancestors but %d ancestor paths", len(result.Ancestors), len(result.AncestorPath))
	}
	if len(result.Ancestors) == 0 {
		// Parent was root, new parent FS ID is the new root
		return newParentFSID, nil
	}
	if strings.TrimSpace(result.AncestorPath[len(result.AncestorPath)-1]) == "" {
		return "", fmt.Errorf("path traverse result has empty ancestor path for rebuild")
	}

	currentFSID := newParentFSID

	// CRITICAL FIX: currentName should be the name of the directory we're updating,
	// which is the LAST entry in AncestorPath, not path.Base(ParentPath).
	// For path "/folder/subfolder/file.docx":
	//   - AncestorPath = ["/", "/folder", "/folder/subfolder"]
	//   - ParentPath = "/folder" (parent of the TARGET, not the modified directory)
	//   - We need "subfolder" (base of last AncestorPath), not "folder" (base of ParentPath)
	currentName := path.Base(result.AncestorPath[len(result.AncestorPath)-1])

	// Walk back through ancestors from parent to root
	for i := len(result.Ancestors) - 2; i >= 0; i-- {
		ancestorFSID := result.Ancestors[i]
		ancestorPath := result.AncestorPath[i]
		if strings.TrimSpace(ancestorPath) == "" {
			return "", fmt.Errorf("path traverse result has empty ancestor path at index %d", i)
		}

		// Get ancestor's entries
		entries, err := getDirectoryEntries(repoID, ancestorFSID)
		if err != nil {
			return "", fmt.Errorf("failed to get ancestor %s: %w", ancestorPath, err)
		}

		// Update the child reference in ancestor
		found := false
		for j := range entries {
			if entries[j].Name == currentName {
				entries[j].ID = currentFSID
				found = true
				break
			}
		}
		if !found {
			return "", fmt.Errorf("failed to rebuild path at %s: child %q not found", ancestorPath, currentName)
		}

		// Create new fs_object for modified ancestor
		newAncestorFSID, err := createDirectoryFSObject(repoID, entries)
		if err != nil {
			return "", fmt.Errorf("failed to create ancestor fs_object: %w", err)
		}

		// Update for next iteration
		currentFSID = newAncestorFSID
		if i > 0 {
			currentName = path.Base(ancestorPath)
		}
	}

	return currentFSID, nil
}

// CreateCommit creates a new commit with the given root fs_id
func (h *FSHelper) CreateCommit(repoID, userID, rootFSID, parentCommitID, description string) (string, error) {
	// Generate commit ID as SHA-1 hash
	commitData := fmt.Sprintf("%s:%s:%s:%d", repoID, rootFSID, description, time.Now().UnixNano())
	hash := sha1.Sum([]byte(commitData))
	commitID := hex.EncodeToString(hash[:])

	// Insert commit
	err := h.db.Session().Query(`
		INSERT INTO commits (library_id, commit_id, parent_id, root_fs_id, creator_id, description, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, repoID, commitID, parentCommitID, rootFSID, userID, description, time.Now()).Exec()
	if err != nil {
		return "", fmt.Errorf("failed to create commit: %w", err)
	}

	return commitID, nil
}

// CalculateLibraryStats recursively calculates total size and file count for a library
func (h *FSHelper) CalculateLibraryStats(repoID, rootFSID string) (totalSize int64, fileCount int64, err error) {
	return h.calculateDirStats(repoID, rootFSID)
}

// calculateDirStats recursively calculates size and file count for a directory
func (h *FSHelper) calculateDirStats(repoID, dirFSID string) (totalSize int64, fileCount int64, err error) {
	var dirEntriesJSON string
	err = h.db.Session().Query(`
		SELECT dir_entries FROM fs_objects WHERE library_id = ? AND fs_id = ?
	`, repoID, dirFSID).Scan(&dirEntriesJSON)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to get directory entries: %w", err)
	}

	if dirEntriesJSON == "" || dirEntriesJSON == "[]" {
		return 0, 0, nil
	}

	var entries []map[string]interface{}
	if err := json.Unmarshal([]byte(dirEntriesJSON), &entries); err != nil {
		return 0, 0, fmt.Errorf("failed to parse directory entries: %w", err)
	}

	for _, entry := range entries {
		mode, ok := entry["mode"].(float64)
		if !ok {
			continue
		}

		if mode == 16384 { // Directory
			childID, ok := entry["id"].(string)
			if !ok {
				continue
			}
			childSize, childCount, err := h.calculateDirStats(repoID, childID)
			if err != nil {
				log.Printf("[calculateDirStats] Warning: failed to calculate stats for dir %s: %v", childID, err)
				continue
			}
			totalSize += childSize
			fileCount += childCount
		} else if mode == 33188 { // Regular file
			size, ok := entry["size"].(float64)
			if !ok {
				sizeInt, ok := entry["size"].(int64)
				if ok {
					totalSize += sizeInt
				}
			} else {
				totalSize += int64(size)
			}
			fileCount++
		}
	}

	return totalSize, fileCount, nil
}

// UpdateLibraryHead updates the library's head_commit_id, size_bytes, and file_count.
// The canonical libraries row advances via CAS; derived lookup/admin rows are
// immediately resynced from the canonical state so lagging projections cannot
// drive future mutations off a stale HEAD. expectedHead must be the same HEAD
// commit that the caller used to build the new tree and commit parent; reading
// a fresher HEAD immediately before publish is invalid because it can let a tree
// built from an older snapshot overwrite newer metadata.
func isAmbiguousLibraryHeadUpdateError(err error) bool {
	var casUnknown gocql.RequestErrCASWriteUnknown
	return errors.As(err, &casUnknown) || errors.Is(err, gocql.ErrTimeoutNoResponse) || errors.Is(err, gocql.ErrConnectionClosed)
}

func (h *FSHelper) confirmLibraryHeadCommitVisible(orgID, repoID, commitID string) (string, bool, error) {
	var currentHead string
	err := h.db.Session().Query(`
		SELECT head_commit_id FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, repoID).Consistency(gocql.Serial).Scan(&currentHead)
	if err != nil {
		return "", false, err
	}
	return currentHead, currentHead == commitID, nil
}

func resolveLibraryHeadUpdateError(repoID, commitID string, updateErr error, confirmVisible func() (string, bool, error)) error {
	wrapped := fmt.Errorf("conditional library head update failed: %w", updateErr)
	if !isAmbiguousLibraryHeadUpdateError(updateErr) {
		return wrapped
	}

	currentHead, visible, confirmErr := confirmVisible()
	if confirmErr == nil {
		if visible {
			log.Printf("[UpdateLibraryHead] WARNING: ambiguous CAS error for library %s commit %s but confirmation read shows the canonical head is already published", repoID, commitID)
			return nil
		}
		log.Printf("[UpdateLibraryHead] INFO: ambiguous CAS error for library %s commit %s confirmed canonical head remains at %s", repoID, commitID, currentHead)
		return wrapped
	}

	log.Printf("[UpdateLibraryHead] WARNING: ambiguous CAS error for library %s commit %s could not be confirmed: %v", repoID, commitID, confirmErr)
	return errors.Join(
		ErrLibraryHeadPublicationUnknown,
		wrapped,
		fmt.Errorf("confirmation read failed: %w", confirmErr),
	)
}

func (h *FSHelper) UpdateLibraryHead(orgID, repoID, commitID, expectedHead string) error {
	// Get root_fs_id from the new commit to recalculate stats
	var rootFSID string
	err := h.db.Session().Query(`
		SELECT root_fs_id FROM commits WHERE library_id = ? AND commit_id = ?
	`, repoID, commitID).Scan(&rootFSID)
	if err != nil {
		return fmt.Errorf("failed to get root_fs_id from commit: %w", err)
	}

	totalSize, fileCount, err := h.CalculateLibraryStats(repoID, rootFSID)
	if err != nil {
		log.Printf("[UpdateLibraryHead] Warning: failed to calculate library stats: %v", err)
		totalSize = 0
		fileCount = 0
	}

	now := time.Now()
	casState := map[string]interface{}{}
	applied, err := h.db.Session().Query(`
		UPDATE libraries SET head_commit_id = ?, size_bytes = ?, file_count = ?, updated_at = ?
		WHERE org_id = ? AND library_id = ?
		IF head_commit_id = ?
	`, commitID, totalSize, fileCount, now, orgID, repoID, expectedHead).MapScanCAS(casState)
	if err != nil {
		return resolveLibraryHeadUpdateError(repoID, commitID, err, func() (string, bool, error) {
			return h.confirmLibraryHeadCommitVisible(orgID, repoID, commitID)
		})
	}
	if !applied {
		currentHead, _ := casState["head_commit_id"].(string)
		return fmt.Errorf("%w: expected %s but found %s", ErrLibraryHeadConflict, expectedHead, currentHead)
	}
	if err := h.syncLibraryHeadDerivedState(orgID, repoID); err != nil {
		// The canonical HEAD is already published at this point. Returning an
		// error would make callers treat the mutation as failed and can trigger
		// unsafe rollback of block refs that are now reachable from HEAD.
		log.Printf("[UpdateLibraryHead] WARNING: canonical head updated for library %s but derived state sync failed: %v", repoID, err)
	}

	log.Printf("[UpdateLibraryHead] Updated library %s: size=%d bytes, files=%d", repoID, totalSize, fileCount)
	return nil
}

// UpdateLibraryHeadFromSnapshot is the safe publish entrypoint for mutations
// that were built from a fixed HEAD snapshot.
func (h *FSHelper) UpdateLibraryHeadFromSnapshot(snapshot *LibraryHeadSnapshot, repoID, commitID, expectedHead string) error {
	if err := snapshot.ValidateExpectedHead(expectedHead); err != nil {
		return err
	}
	return h.UpdateLibraryHead(snapshot.OrgID, repoID, commitID, snapshot.HeadCommitID)
}

// Independent SesameFS nodes can transiently stack more CAS conflicts than the
// single-process retry budget originally assumed.
const libraryHeadMutationRetryAttempts = 8

var libraryHeadMutationRetryDelay = 50 * time.Millisecond

var libraryHeadMutationRetryMaxDelay = 400 * time.Millisecond

var libraryHeadMutationRetryJitter = 25 * time.Millisecond

var libraryHeadMutationRetryJitterInt63n = rand.Int63n

// RetryBackoff returns the exponential-with-jitter delay between
// upload-metadata-publish or library-head-mutation retry attempts. Both retry
// paths share the same CAS conflict semantics, so they share one schedule.
func RetryBackoff(attempt int) time.Duration {
	return libraryHeadMutationRetryBackoff(attempt)
}

// RetryAttempts returns the bounded retry budget shared by every CAS-based
// HEAD-mutation path (uploads, v2 mutations, and sync HEAD publish). Keeping a
// single source of truth prevents the per-caller budgets from drifting again.
func RetryAttempts() int {
	return libraryHeadMutationRetryAttempts
}

func libraryHeadMutationRetryBackoff(attempt int) time.Duration {
	if attempt < 1 || libraryHeadMutationRetryDelay <= 0 {
		return 0
	}

	delay := libraryHeadMutationRetryDelay
	for step := 1; step < attempt; step++ {
		delay *= 2
		if libraryHeadMutationRetryMaxDelay > 0 && delay >= libraryHeadMutationRetryMaxDelay {
			delay = libraryHeadMutationRetryMaxDelay
			break
		}
	}

	if libraryHeadMutationRetryMaxDelay > 0 && delay > libraryHeadMutationRetryMaxDelay {
		delay = libraryHeadMutationRetryMaxDelay
	}
	if libraryHeadMutationRetryJitter > 0 && libraryHeadMutationRetryJitterInt63n != nil {
		delay += time.Duration(libraryHeadMutationRetryJitterInt63n(int64(libraryHeadMutationRetryJitter)))
	}
	return delay
}

func retryLibraryHeadMutation(label string, mutate func() error) error {
	var lastConflict error

	for attempt := 1; attempt <= libraryHeadMutationRetryAttempts; attempt++ {
		err := mutate()
		if err == nil {
			return nil
		}
		if !errors.Is(err, ErrLibraryHeadConflict) {
			return err
		}
		lastConflict = err
		if attempt == libraryHeadMutationRetryAttempts {
			break
		}
		sleepFor := libraryHeadMutationRetryBackoff(attempt)
		log.Printf("[%s] Retrying metadata publish after head conflict (%d/%d), sleeping %s", label, attempt, libraryHeadMutationRetryAttempts, sleepFor)
		if sleepFor > 0 {
			time.Sleep(sleepFor)
		}
	}

	if lastConflict != nil {
		return lastConflict
	}
	return fmt.Errorf("%s mutation failed", label)
}

func (h *FSHelper) syncLibraryHeadDerivedState(orgID, repoID string) error {
	var headCommitID string
	if err := h.db.Session().Query(`
		SELECT head_commit_id FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, repoID).Scan(&headCommitID); err != nil {
		return fmt.Errorf("failed to read canonical head after library update: %w", err)
	}

	projectionRow, err := db.ReadAdminLibraryProjectionRow(h.db.Session(), orgID, repoID)
	if err != nil {
		return fmt.Errorf("failed to read library projection row: %w", err)
	}

	batch := h.db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		UPDATE libraries_by_id SET head_commit_id = ?
		WHERE library_id = ?
	`, headCommitID, repoID)
	db.AddUpsertAdminLibraryReadModelQuery(batch, projectionRow)
	if err := batch.Exec(); err != nil {
		return fmt.Errorf("failed to sync library head derived state: %w", err)
	}

	return nil
}

// InitializeLibraryFS creates the empty root directory, initial commit, and sets
// head_commit_id on a newly created library. This MUST be called after inserting
// the library rows (libraries + libraries_by_id) so that file uploads work.
func (h *FSHelper) InitializeLibraryFS(orgID, repoID, userID, repoName string) error {
	now := time.Now()

	// 1. Create empty root directory fs_object
	emptyDirEntries := "[]"
	emptyDirData := fmt.Sprintf("%d\n%s", 1, emptyDirEntries)
	emptyDirHash := sha1.Sum([]byte(emptyDirData))
	rootFSID := hex.EncodeToString(emptyDirHash[:])

	// 2. Generate initial commit ID
	commitData := fmt.Sprintf("%s:%s:%d", repoID, repoName, now.UnixNano())
	commitHash := sha1.Sum([]byte(commitData))
	headCommitID := hex.EncodeToString(commitHash[:])
	projectionRow, err := db.ReadAdminLibraryProjectionRow(h.db.Session(), orgID, repoID)
	if err != nil {
		return fmt.Errorf("failed to read library projection row: %w", err)
	}

	// 3. Persist root object, initial commit, and head_commit atomically.
	batch := h.db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		INSERT INTO fs_objects (library_id, fs_id, obj_type, obj_name, dir_entries, mtime)
		VALUES (?, ?, ?, ?, ?, ?)
	`, repoID, rootFSID, "dir", "", emptyDirEntries, now.Unix())
	batch.Query(`
		INSERT INTO commits (library_id, commit_id, root_fs_id, creator_id, description, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, repoID, headCommitID, rootFSID, userID, "Initial commit", now)
	batch.Query(`
		UPDATE libraries SET head_commit_id = ? WHERE org_id = ? AND library_id = ?
	`, headCommitID, orgID, repoID)
	batch.Query(`
		UPDATE libraries_by_id SET head_commit_id = ? WHERE library_id = ?
	`, headCommitID, repoID)
	addAdminLibraryReadModelRefreshQueries(batch, projectionRow, nil)
	if err := batch.Exec(); err != nil {
		return fmt.Errorf("failed to initialize library fs state: %w", err)
	}

	return nil
}

// RemoveEntryFromList removes an entry by name from a list of entries
func RemoveEntryFromList(entries []FSEntry, name string) []FSEntry {
	result := make([]FSEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.Name != name {
			result = append(result, entry)
		}
	}
	return result
}

// FindEntryInList finds an entry by name in a list of entries
func FindEntryInList(entries []FSEntry, name string) *FSEntry {
	for i := range entries {
		if entries[i].Name == name {
			return &entries[i]
		}
	}
	return nil
}

// UpdateEntryInList updates an entry's name in a list of entries
func UpdateEntryInList(entries []FSEntry, oldName, newName string) []FSEntry {
	result := make([]FSEntry, len(entries))
	for i, entry := range entries {
		if entry.Name == oldName {
			entry.Name = newName
		}
		result[i] = entry
	}
	return result
}

// AddEntryToList adds a new entry to a list of entries
func AddEntryToList(entries []FSEntry, entry FSEntry) []FSEntry {
	return append(entries, entry)
}

// GenerateUniqueName generates a unique name by appending " (1)", " (2)", etc.
// Pattern: "report.pdf" → "report (1).pdf" → "report (2).pdf"
func GenerateUniqueName(entries []FSEntry, baseName string) string {
	// Build set of existing names for fast lookup
	existing := make(map[string]bool, len(entries))
	for _, e := range entries {
		existing[e.Name] = true
	}

	if !existing[baseName] {
		return baseName
	}

	ext := path.Ext(baseName)
	nameWithoutExt := strings.TrimSuffix(baseName, ext)

	for i := 1; ; i++ {
		candidate := fmt.Sprintf("%s (%d)%s", nameWithoutExt, i, ext)
		if !existing[candidate] {
			return candidate
		}
	}
}

// GetHeadCommitID gets the current head commit ID for a library
func (h *FSHelper) GetHeadCommitID(repoID string) (string, error) {
	_, headCommitID, err := h.getCanonicalHeadCommit(repoID)
	if err != nil {
		return "", err
	}
	return headCommitID, nil
}

// CollectBlockIDsRecursive collects all block IDs from a directory tree recursively
func (h *FSHelper) CollectBlockIDsRecursive(repoID, fsID string) ([]string, error) {
	blockIDs, _, _, err := h.collectDirStats(repoID, fsID)
	return blockIDs, err
}

// resolveStoredBlockIDs maps external SHA-1 block IDs to the internal SHA-256 IDs
// stored in the blocks table. Resolution is strict (see BatchResolveBlockIDs):
// ref-count mutations MUST run on resolved IDs. Incrementing an unresolved SHA-1
// would INSERT a phantom SHA-1 row and leave the real SHA-256 block
// un-incremented; decrementing one is skipped harmlessly but leaks the real
// block. Interim fail-closed handling only — see
// ISSUE-REFCOUNT-RESOLVE-FAILCLOSED-01; the root fix lands with the ref_count
// counter→per-block redesign, not here.
func (h *FSHelper) resolveStoredBlockIDs(orgID string, blockIDs []string) ([]string, error) {
	if len(blockIDs) == 0 {
		return nil, nil
	}
	return streaming.BatchResolveBlockIDs(h.db, orgID, blockIDs)
}

// RegisterUploadedBlock records freshly-uploaded block metadata plus a
// provisional reference that keeps the block alive until its fs_object is
// committed. The provisional reference carries a TTL, so an abandoned upload
// cannot leak the block forever. Liveness is row-based (no mutable counter), so
// a client retry that re-uploads the same block is naturally idempotent.
//
// blockID must already be the internal (SHA-256) ID. operationID must identify
// the specific upload/session/rollback flow that owns the provisional ref so a
// rollback only removes its own pin. The reference is written BEFORE the
// metadata read so that a concurrent GC claim-then-verify observes it. If the
// GC worker has already claimed the block for deletion (gc_state='deleting'),
// the provisional reference is removed again and the caller gets a retryable
// error so it can restart from the full upload/store path.
func (h *FSHelper) RegisterUploadedBlock(orgID, libraryID, blockID, operationID string, sizeBytes int, storageClass, storageKey string) error {
	referrer := db.BlockReferrerForUpload(operationID)

	if err := h.db.AddBlockReference(orgID, blockID, referrer, libraryID, db.ProvisionalBlockReferenceTTLSeconds); err != nil {
		return fmt.Errorf("add provisional block reference for %s: %w", blockID, err)
	}

	gcState, err := h.db.BlockGCState(orgID, blockID)
	if err != nil {
		return fmt.Errorf("read block gc_state for %s: %w", blockID, err)
	}
	if gcState == db.BlockGCStateDeleting {
		if err := h.db.RemoveBlockReference(orgID, blockID, referrer); err != nil {
			log.Printf("[RegisterUploadedBlock] WARNING: failed to remove provisional reference for block %s after observing gc_state=%q: %v", blockID, gcState, err)
		}
		return fmt.Errorf("%w: block %s is currently claimed by GC", ErrBlockDeleteInProgress, blockID)
	}

	if err := h.db.UpsertBlockMetadata(orgID, blockID, sizeBytes, storageClass, storageKey); err != nil {
		return fmt.Errorf("upsert block metadata for %s: %w", blockID, err)
	}
	return nil
}

// RegisterFSObjectBlockReferences creates the permanent reference rows for every
// block held by an fs_object. A reference row exists iff the fs_object exists in
// fs_objects, so block liveness == "some live fs_object contains this block".
//
// Block IDs are resolved to internal SHA-256 IDs first and the resolution is
// strict (fail-closed): if any ID cannot be resolved, no reference is written and
// the caller must abort the commit. References are written here, BEFORE the
// fs_object row is inserted, so a partial failure leaves orphan reference rows
// (a harmless leak self-healed by retrying the same content-addressed upload)
// rather than an fs_object whose blocks have no reference (potential data loss).
// Re-registering the same (block, fs_object) is idempotent.
func (h *FSHelper) RegisterFSObjectBlockReferences(orgID, libraryID, fsID string, blockIDs []string) error {
	if len(blockIDs) == 0 {
		return nil
	}
	resolved, err := resolveStoredBlockIDsFn(h, orgID, blockIDs)
	if err != nil {
		return fmt.Errorf("resolve block IDs before referencing fs_object %s/%s: %w", libraryID, fsID, err)
	}
	referrer := db.BlockReferrerForFSObject(libraryID, fsID)
	for _, blockID := range resolved {
		if err := h.db.AddBlockReference(orgID, blockID, referrer, libraryID, 0); err != nil {
			return fmt.Errorf("add fs_object block reference (block %s, fs_object %s/%s): %w", blockID, libraryID, fsID, err)
		}
	}
	return nil
}

// ReleaseUploadReferences removes the provisional upload references for blocks of
// an aborted upload and returns the block IDs that are now unreferenced, so the
// caller can enqueue them for GC. blockIDs must be internal (SHA-256) IDs (the
// same IDs used when the provisional reference was created). operationID must be
// the same upload/session identity used at registration time. Idempotent:
// removing a missing reference is a no-op, so a retried rollback is safe.
func (h *FSHelper) ReleaseUploadReferences(orgID, libraryID, operationID string, blockIDs []string) []string {
	referrer := db.BlockReferrerForUpload(operationID)
	var zeroRefBlocks []string
	for _, blockID := range blockIDs {
		if err := h.db.RemoveBlockReference(orgID, blockID, referrer); err != nil {
			log.Printf("[ReleaseUploadReferences] WARNING: failed to remove provisional reference for block %s: %v", blockID, err)
			continue
		}
		hasRefs, err := h.db.BlockHasReferences(orgID, blockID)
		if err != nil {
			log.Printf("[ReleaseUploadReferences] WARNING: failed to check references for block %s: %v", blockID, err)
			continue
		}
		if !hasRefs {
			zeroRefBlocks = append(zeroRefBlocks, blockID)
		}
	}
	return zeroRefBlocks
}

// collectDirStats recursively collects block IDs, total size in bytes, and file count
// for a directory tree rooted at the given fs_object.
func (h *FSHelper) collectDirStats(repoID, fsID string) (blockIDs []string, totalSize int64, fileCount int64, err error) {
	var objType string
	var dirEntries string
	var blockIDsList []string
	var sizeBytes int64

	err = h.db.Session().Query(`
		SELECT obj_type, dir_entries, block_ids, size_bytes FROM fs_objects WHERE library_id = ? AND fs_id = ?
	`, repoID, fsID).Scan(&objType, &dirEntries, &blockIDsList, &sizeBytes)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("fs_object not found: %w", err)
	}

	if objType == "file" || objType == "" {
		return blockIDsList, sizeBytes, 1, nil
	}

	// Directory: recurse into children
	var entries []FSEntry
	if dirEntries != "" && dirEntries != "[]" {
		if err := json.Unmarshal([]byte(dirEntries), &entries); err != nil {
			return nil, 0, 0, fmt.Errorf("invalid directory data: %w", err)
		}
	}

	for _, entry := range entries {
		childBlocks, childSize, childCount, err := h.collectDirStats(repoID, entry.ID)
		if err != nil {
			continue
		}
		blockIDs = append(blockIDs, childBlocks...)
		totalSize += childSize
		fileCount += childCount
	}
	return blockIDs, totalSize, fileCount, nil
}

// CopyFSObjectToLibrary recursively copies an fs_object (file or directory) from
// one library to another. Returns the new fs_id in the destination library.
// This is needed because fs_objects are keyed by (library_id, fs_id), so a cross-library
// copy must create new fs_object rows in the destination library.
func (h *FSHelper) CopyFSObjectToLibrary(orgID, srcRepoID, dstRepoID, fsID string) (string, error) {
	// Read the source fs_object
	var objType, objName, dirEntries string
	var blockIDs []string
	var sizeBytes int64
	var mtime int64

	err := h.db.Session().Query(`
		SELECT obj_type, obj_name, dir_entries, block_ids, size_bytes, mtime
		FROM fs_objects WHERE library_id = ? AND fs_id = ?
	`, srcRepoID, fsID).Scan(&objType, &objName, &dirEntries, &blockIDs, &sizeBytes, &mtime)
	if err != nil {
		return "", fmt.Errorf("source fs_object not found (library=%s, fs_id=%s): %w", srcRepoID, fsID, err)
	}

	if objType == "file" || objType == "" {
		// File: create a new fs_object in the destination library with the same
		// block_ids. CreateFileFSObject registers the permanent block references
		// for the new fs_object, so the copy keeps the blocks alive — no separate
		// ref-count increment is needed.
		newFSID, err := h.CreateFileFSObject(orgID, dstRepoID, objName, sizeBytes, blockIDs)
		if err != nil {
			return "", fmt.Errorf("failed to copy file fs_object: %w", err)
		}
		return newFSID, nil
	}

	// Directory: recursively copy all children, then create the directory in destination
	var entries []FSEntry
	if dirEntries != "" && dirEntries != "[]" {
		if err := json.Unmarshal([]byte(dirEntries), &entries); err != nil {
			return "", fmt.Errorf("invalid directory data: %w", err)
		}
	}

	// Copy each child entry, updating the fs_id to the new one in the destination library
	newEntries := make([]FSEntry, len(entries))
	for i, entry := range entries {
		newChildFSID, err := h.CopyFSObjectToLibrary(orgID, srcRepoID, dstRepoID, entry.ID)
		if err != nil {
			return "", fmt.Errorf("failed to copy child %q: %w", entry.Name, err)
		}
		newEntries[i] = FSEntry{
			Name:  entry.Name,
			ID:    newChildFSID,
			Mode:  entry.Mode,
			MTime: entry.MTime,
			Size:  entry.Size,
		}
	}

	// Create the directory fs_object in the destination library
	newDirFSID, err := h.CreateDirectoryFSObject(dstRepoID, newEntries)
	if err != nil {
		return "", fmt.Errorf("failed to copy directory fs_object: %w", err)
	}

	return newDirFSID, nil
}

// CreateFileFSObject creates a new fs_object for a file and registers a permanent
// block reference for each of its blocks (row-per-reference liveness). References
// are written BEFORE the fs_object row so a partial failure leaves harmless orphan
// references rather than an fs_object whose blocks are unreferenced. orgID is
// required to key the block reference rows (blocks are partitioned per org).
func (h *FSHelper) CreateFileFSObject(orgID, repoID, name string, size int64, blockIDs []string) (string, error) {
	// Calculate fs_id as SHA-1 of the EXACT JSON that will be returned by pack-fs
	// Seafile format: {"block_ids":[...],"size":N,"type":1,"version":1} (alphabetical key order)
	// CRITICAL: The hash MUST match what the client receives, or it can't store the object
	// CRITICAL: Must use map[string]interface{} which serializes keys alphabetically.
	// Using a struct would change field order and break hash matching.
	fsContent := map[string]interface{}{
		"version":   1,
		"type":      1, // SEAF_METADATA_TYPE_FILE
		"block_ids": blockIDs,
		"size":      size,
	}
	fsContentJSON, err := json.Marshal(fsContent)
	if err != nil {
		return "", fmt.Errorf("failed to marshal fs content: %w", err)
	}
	hash := sha1.Sum(fsContentJSON)
	fsID := hex.EncodeToString(hash[:])

	// Register permanent block references before inserting the fs_object row.
	// Fail-closed: if any block ID cannot be resolved, no fs_object is created.
	if err := h.RegisterFSObjectBlockReferences(orgID, repoID, fsID, blockIDs); err != nil {
		return "", fmt.Errorf("failed to register block references for fs_object %s: %w", fsID, err)
	}

	// Store in database
	err = h.db.Session().Query(`
		INSERT INTO fs_objects (library_id, fs_id, obj_type, obj_name, block_ids, size_bytes, mtime)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, repoID, fsID, "file", name, blockIDs, size, time.Now().Unix()).Exec()
	if err != nil {
		return "", fmt.Errorf("failed to create fs_object: %w", err)
	}

	return fsID, nil
}
