package v2

import (
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/crypto"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/httputil"
	"github.com/Sesame-Disk/sesamefs/internal/metrics"
	"github.com/Sesame-Disk/sesamefs/internal/middleware"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
	"github.com/Sesame-Disk/sesamefs/internal/templates"
	"github.com/Sesame-Disk/sesamefs/internal/traffic"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// TokenCreator is an interface for creating access tokens
type TokenCreator interface {
	CreateUploadToken(orgID, repoID, path, userID string) (string, error)
	CreateUpdateToken(orgID, repoID, path, userID string) (string, error)
	CreateDownloadToken(orgID, repoID, path, userID string) (string, error)
	CreateSyncToken(orgID, repoID, userID string) (string, error)
	CreateLinkUploadToken(orgID, repoID, path, userID, sourceID string) (string, error)
	CreateLinkDownloadToken(orgID, repoID, path, userID, sourceID string) (string, error)
}

// formatSizeSeafile delegates to httputil.FormatSizeSeafile.
var formatSizeSeafile = httputil.FormatSizeSeafile

// formatRelativeTimeHTML delegates to httputil.FormatRelativeTimeHTML.
var formatRelativeTimeHTML = httputil.FormatRelativeTimeHTML

var checkFileLockedByOther = func(h *FileHandler, repoID, filePath, userID string) (bool, string, error) {
	if h == nil || h.db == nil {
		return false, "", nil
	}
	return db.FileLockedByOther(h.db.Session(), repoID, normalizePath(filePath), userID)
}

// checkSubtreeLockedByOther reports whether the target path OR any descendant under it
// is locked by another user. Used by directory-capable operations (rename/move/delete)
// so a folder action cannot bypass a lock on a file inside it.
var checkSubtreeLockedByOther = func(h *FileHandler, repoID, targetPath, userID string) (bool, string, error) {
	if h == nil || h.db == nil {
		return false, "", nil
	}
	return db.SubtreeLockedByOther(h.db.Session(), repoID, normalizePath(targetPath), userID)
}

// checkCopyReplaceDestLocked reports whether a "replace" copy's destination path or
// any descendant beneath it is locked by another user. This blocks folder-replace
// copies from clobbering a locked file inside the destination subtree. Injectable for tests.
var checkCopyReplaceDestLocked = func(fsHelper *FSHelper, repoID, dstPath, userID string) (bool, string, error) {
	if fsHelper == nil || fsHelper.db == nil {
		return false, "", nil
	}
	return db.SubtreeLockedByOther(fsHelper.db.Session(), repoID, normalizePath(dstPath), userID)
}

// lockTargetState reports whether filePath resolves to an existing entry in repoID's
// HEAD tree and, if so, whether that entry is a directory. LockFile uses it to refuse a
// manual lock on a path the tree never contained: a "phantom" lock row (e.g.
// /empty-dir/ghost.txt or /foo.txt/ghost) would otherwise make SubtreeLockedByOther
// block legitimate rename/move/delete on the real parent. Fail-closed: a traversal that
// cannot resolve the path (intermediate component missing or not a directory) surfaces
// the error so the caller refuses the lock with 503 rather than creating a phantom.
// Injectable for tests; a nil db skips the check (returns exists=true).
var lockTargetState = func(h *FileHandler, repoID, filePath string) (exists, isDir bool, err error) {
	if h == nil || h.db == nil {
		return true, false, nil
	}
	res, terr := NewFSHelper(h.db).TraverseToPath(repoID, normalizePath(filePath))
	if terr != nil {
		return false, false, terr
	}
	if res == nil || res.TargetEntry == nil {
		return false, false, nil
	}
	isDir = res.TargetEntry.Mode == ModeDir || res.TargetEntry.Mode&0170000 == 040000
	return true, isDir, nil
}

// acquireFileLock atomically takes/refreshes a manual lock (LWT). Injectable for tests.
var acquireFileLock = func(h *FileHandler, repoID, filePath, userID string, lockedAt time.Time) (db.LockAcquireResult, string, error) {
	if h == nil || h.db == nil {
		return db.LockAcquired, userID, nil
	}
	return db.AcquireFileLock(h.db.Session(), repoID, normalizePath(filePath), userID, lockedAt)
}

// releaseFileLock atomically releases a lock only if the requester holds it (LWT).
// Injectable for tests.
var releaseFileLock = func(h *FileHandler, repoID, filePath, userID string) (bool, string, error) {
	if h == nil || h.db == nil {
		return true, "", nil
	}
	return db.ReleaseFileLock(h.db.Session(), repoID, normalizePath(filePath), userID)
}

// clearLocksAfterDelete drops the operator's own locks left under a just-deleted path.
// Best-effort: it runs after the delete already committed and only touches the separate
// locked_files table, so a failure is logged but never fails the delete.
var clearLocksAfterDelete = func(h *FileHandler, repoID, deletedPath, userID string) {
	if h == nil || h.db == nil {
		return
	}
	if err := db.ClearLocksUnder(h.db.Session(), repoID, normalizePath(deletedPath), userID); err != nil {
		log.Printf("[locks] failed to clear locks under deleted path %s: %v", deletedPath, err)
	}
}

// relocateLocksAfterMove rewrites the operator's own locks from oldPath to newPath after
// a rename/move. Best-effort for the same reason as clearLocksAfterDelete.
var relocateLocksAfterMove = func(h *FileHandler, repoID, oldPath, newPath, userID string) {
	if h == nil || h.db == nil {
		return
	}
	if err := db.RelocateLocksUnder(h.db.Session(), repoID, normalizePath(oldPath), normalizePath(newPath), userID); err != nil {
		log.Printf("[locks] failed to relocate locks %s -> %s: %v", oldPath, newPath, err)
	}
}

var errUploadStorageQuotaExceeded = errors.New("storage quota exceeded")
var errLibraryNotFound = errors.New("library not found")
var errParentDirectoryNotFound = errors.New("parent directory not found")
var errFileExists = errors.New("file already exists")
var errDirectoryExists = errors.New("directory already exists")
var errBlockStorageUnavailable = errors.New("block storage not available")

func errorMessageOrFallback(err error, fallback string) string {
	if err == nil || err.Error() == "" {
		return fallback
	}
	if err.Error() == errLibraryNotFound.Error() || err.Error() == errParentDirectoryNotFound.Error() {
		return fallback
	}
	return err.Error()
}

func rebuildTraversedDirectoryToRoot(fsHelper *FSHelper, repoID string, result *PathTraverseResult, dirPath, newDirFSID string) (string, error) {
	if dirPath == "/" {
		return newDirFSID, nil
	}

	dirName := path.Base(dirPath)
	updatedParentEntries := make([]FSEntry, len(result.Entries))
	found := false
	for i, entry := range result.Entries {
		if entry.Name == dirName {
			entry.ID = newDirFSID
			found = true
		}
		updatedParentEntries[i] = entry
	}
	if !found {
		return "", fmt.Errorf("directory %q not found in parent", dirName)
	}

	newParentFSID, err := fsHelper.CreateDirectoryFSObject(repoID, updatedParentEntries)
	if err != nil {
		return "", fmt.Errorf("failed to update parent directory: %w", err)
	}
	return fsHelper.RebuildPathToRoot(repoID, result, newParentFSID)
}

// Upload-finalize retry shares libraryHeadMutationRetryBackoff for per-attempt
// delay (exponential with jitter, capped). Only the attempt count is local.
const uploadMetadataRetryAttempts = 20

// Office template creates can race with GC over the shared content-addressed
// template block. When that happens, re-store the template and retry the block
// registration instead of surfacing a transient 500 to the caller.
const createFileTemplateBlockRetryAttempts = 3

var createFileTemplateBlockRetryBackoffFn = RetryBackoff

var createFileTemplateBlockSleepFn = time.Sleep

func retryCreateFileTemplateBlockMaterialization(store func() error, register func() error, resetStored func()) error {
	return retryCreateFileTemplateBlockMaterializationPhased(func(BlockMaterializationPhase) error {
		return store()
	}, register, resetStored)
}

func retryCreateFileTemplateBlockMaterializationPhased(store func(BlockMaterializationPhase) error, register func() error, resetStored func()) error {
	// Checked up front, before any side effect. resetStored is what re-probes after
	// registration and rebuilds the caller's target without the fresh-install
	// authority the initial phase minted; without it the confirmation store
	// short-circuits on the cached state and a later HEAD-conflict retry would
	// resubmit a single-use install identity. A nil callback is a caller bug, so it
	// should not first mint, PUT and register a block and only then be reported.
	if resetStored == nil {
		return fmt.Errorf("template block materialization requires a reset callback to re-probe after registration")
	}

	attempts := createFileTemplateBlockRetryAttempts
	if attempts < 1 {
		attempts = 1
	}

	// reason is derived from the failing PHASE (never the sentinel), matching the
	// generic wrapper so a register-phase write failure is not mislabeled as a
	// probe read (finding F14).
	retryBlocked := func(attempt int, phaseReason string, retryErr error) {
		if resetStored != nil {
			resetStored()
		}
		reason := phaseReason
		if errors.Is(retryErr, ErrBlockDeleteInProgress) {
			reason = blockMaterializationReasonFence
		}
		metrics.BlockUploadMaterializationRetriesTotal.WithLabelValues("CreateFile", reason).Inc()
		sleepFor := createFileTemplateBlockRetryBackoffFn(attempt)
		log.Printf("[CreateFile] template block materialization retry reason=%s (%d/%d) after %s", reason, attempt, attempts, sleepFor)
		if sleepFor > 0 {
			createFileTemplateBlockSleepFn(sleepFor)
		}
	}

	for attempt := 1; attempt <= attempts; attempt++ {
		if err := store(BlockMaterializationInitial); err != nil {
			if !IsRetryableBlockMaterializationError(err) || attempt == attempts {
				return err
			}
			retryBlocked(attempt, blockMaterializationReasonProbe, err)
			continue
		}
		if err := register(); err != nil {
			if !IsRetryableBlockMaterializationError(err) || attempt == attempts {
				return err
			}
			retryBlocked(attempt, blockMaterializationReasonMaterial, err)
			continue
		}
		// Re-probe after the provisional reference is durable. Clearing the cached
		// store state makes this a real canonical HEAD/repair, not a no-op -- and it
		// is what rebuilds the caller's target from the now-canonical row, dropping
		// the fresh-install authority the initial phase minted.
		resetStored()
		for confirmationAttempt := 1; confirmationAttempt <= attempts; confirmationAttempt++ {
			if err := store(BlockMaterializationConfirmation); err == nil {
				return nil
			} else {
				if !IsRetryableBlockMaterializationError(err) || confirmationAttempt == attempts {
					return err
				}
				retryBlocked(confirmationAttempt, blockMaterializationReasonProbe, err)
				// Only a GC delete fence restarts the full cycle: the fence invalidates
				// the canonical state this request just installed, so probe->prepare has
				// to re-run from the initial phase. Every other retryable confirmation
				// failure -- a transient canonical HEAD/repair error, or a canonical row
				// that is not visible yet -- is convergence of state this request already
				// installed, so it retries the confirmation probe instead. Staying here is
				// what keeps a transient HEAD failure from handing the initial phase
				// another chance to mint a second incarnation (finding F5).
				if !errors.Is(err, ErrBlockDeleteInProgress) {
					continue
				}
			}
			break
		}
	}

	return fmt.Errorf("unreachable template block materialization state")
}

// Dirent represents a directory entry in Seafile API format
// This matches the exact format expected by Seafile clients
type Dirent struct {
	ID                    string `json:"id"`
	Name                  string `json:"name"`
	Type                  string `json:"type"` // "file" or "dir"
	Size                  int64  `json:"size"`
	MTime                 int64  `json:"mtime"`      // Unix timestamp
	Permission            string `json:"permission"` // "rw" or "r"
	ParentDir             string `json:"parent_dir,omitempty"`
	Starred               bool   `json:"starred,omitempty"`
	ModifierEmail         string `json:"modifier_email,omitempty"`
	ModifierName          string `json:"modifier_name,omitempty"`
	ModifierContactEmail  string `json:"modifier_contact_email,omitempty"`
	IsLocked              bool   `json:"is_locked"`
	LockTime              int64  `json:"lock_time"`
	IsFreezed             bool   `json:"is_freezed"`
	LockOwner             string `json:"lock_owner"`
	LockOwnerName         string `json:"lock_owner_name"`
	LockOwnerContactEmail string `json:"lock_owner_contact_email"`
	LockedByMe            bool   `json:"locked_by_me"`
	ExpiresAt             int64  `json:"expires_at,omitempty"` // Unix timestamp when file expires (auto_delete_days)
}

// FSEntry represents a directory entry stored in fs_objects.dir_entries
// This matches the Seafile format for directory entries
// CRITICAL: Field order MUST be alphabetical to match Seafile JSON format.
// Seafile uses alphabetical key ordering in JSON which affects fs_id hash computation.
type FSEntry struct {
	ID       string `json:"id"`   // FS object ID (40 char hex)
	Mode     int    `json:"mode"` // Unix file mode (33188 = regular file, 16384 = directory)
	Modifier string `json:"modifier,omitempty"`
	MTime    int64  `json:"mtime"` // Unix timestamp
	Name     string `json:"name"`
	Size     int64  `json:"size,omitempty"`
}

// ModeFile is the Unix mode for a regular file (0100644)
const ModeFile = 33188

// ModeDir is the Unix mode for a directory (040000)
const ModeDir = 16384

// GCEnqueuer is the interface for enqueuing blocks for garbage collection.
// This keeps the gc package dependency out of the v2 package.
type GCEnqueuer interface {
	// EnqueueBlocks enqueues blocks with ref_count=0 for garbage collection.
	// orgID and storageClass identify where the blocks live.
	EnqueueBlocks(orgID string, blockIDs []string, storageClass string)
}

// FileHandler handles file-related API requests
type FileHandler struct {
	db              *db.DB
	config          *config.Config
	storage         *storage.S3Store
	storageManager  *storage.Manager
	tokenCreator    TokenCreator
	zipTokenCreator LibraryTokenCreator // For zip-task endpoint (only needs CreateDownloadToken)
	serverURL       string              // Base URL of the server for generating seafhttp URLs
	permMiddleware  *middleware.PermissionMiddleware
	gcEnqueuer      GCEnqueuer
}

// NewFileHandler creates a new FileHandler instance
func NewFileHandler(database *db.DB, cfg *config.Config, s3Store *storage.S3Store, storageManager *storage.Manager, tokenCreator TokenCreator, serverURL string, permMiddleware *middleware.PermissionMiddleware) *FileHandler {
	return &FileHandler{
		db:             database,
		config:         cfg,
		storage:        s3Store,
		storageManager: storageManager,
		tokenCreator:   tokenCreator,
		serverURL:      serverURL,
		permMiddleware: permMiddleware,
	}
}

// SetGCEnqueuer sets the GC enqueuer for inline block enqueue on deletion.
func (h *FileHandler) SetGCEnqueuer(enqueuer GCEnqueuer) {
	h.gcEnqueuer = enqueuer
}

func (h *FileHandler) lookupLibraryStorageClass(orgID, repoID string) (string, error) {
	if h == nil || h.db == nil || orgID == "" || repoID == "" {
		return "", nil
	}
	return lookupLibraryStorageClass(h.db, orgID, repoID)
}

// resolveLibraryBlockStore returns the library-preferred store for new writes
// and fallback plumbing. Canonical readers resolve each block's persisted
// storage_class independently.
func (h *FileHandler) resolveLibraryBlockStore(c *gin.Context, orgID, repoID string) (*storage.BlockStore, string, error) {
	libraryClass, err := h.lookupLibraryStorageClass(orgID, repoID)
	if err != nil {
		return nil, "", err
	}
	if h.storageManager != nil {
		preferredClass, err := h.storageManager.ResolveStorageClass(routingHostname(c, h.config), libraryClass, "hot")
		if err != nil {
			return nil, libraryClass, err
		}
		return h.storageManager.GetHealthyBlockStoreForOrg(orgID, preferredClass)
	}

	if libraryClass == "" && h.config != nil {
		libraryClass = h.config.Storage.DefaultClass
	}
	// Fallback (no storage manager): build an org-scoped store from the raw S3
	// store. The legacy org-less singleton is never served — that would recreate
	// the cross-org block-delete hazard (P10).
	if h.storage != nil {
		bs, err := storage.NewOrgBlockStore(h.storage, "blocks/", orgID)
		if err != nil {
			return nil, libraryClass, err
		}
		return bs, libraryClass, nil
	}

	return nil, libraryClass, fmt.Errorf("block storage not available")
}

// requireDecryptSession checks if a library is encrypted and requires an active decrypt session.
// Returns true if access is allowed (library not encrypted OR user has active decrypt session).
// Returns false and sends 403 response if library is encrypted and user hasn't unlocked it.
// This enforces the "vault" security model - encrypted libraries are completely inaccessible
// without first providing the password via the set-password endpoint.
func (h *FileHandler) requireDecryptSession(c *gin.Context, orgID, userID, repoID string) bool {
	if h.db == nil {
		return true // No database, allow access (for testing)
	}

	// Check if library is encrypted. A probe failure must NOT open this gate:
	// err covers a Cassandra timeout as well as a missing row, and returning
	// true there would let a transient fault grant access to an encrypted
	// library with no decrypt session.
	encrypted, err := libraryIsEncrypted(h.db, orgID, repoID)
	if err != nil {
		respondEncryptionProbeUnavailable(c)
		return false
	}

	if !encrypted {
		return true // Not encrypted, no session required
	}

	// Library is encrypted - require active decrypt session
	if !GetDecryptSessions().IsUnlocked(userID, repoID) {
		log.Printf("[SECURITY] Blocked access to encrypted library %s by user %s - no decrypt session", repoID, userID)
		c.JSON(http.StatusForbidden, gin.H{
			"error":     "Library is encrypted",
			"error_msg": "This library is encrypted. Please provide the password to unlock it.",
			// Stable, machine-readable flag (the app-wide convention used by copy/move
			// and batch_operations) so clients detect "needs decrypt" without matching
			// the human-readable error string. See isLibraryEncryptedError (frontend).
			"lib_need_decrypt": true,
		})
		return false
	}

	return true
}

// requireWritePermission checks if the user has write permission based on their organization role.
// Returns true if access is allowed (user is admin or user role).
// Returns false and sends 403 response if user is readonly or guest.
func (h *FileHandler) requireWritePermission(c *gin.Context, orgID, userID string) bool {
	// Check repo API token permission first
	if isRepoToken, _ := c.Get("repo_api_token"); isRepoToken == true {
		tokenPerm := c.GetString("repo_api_token_permission")
		if tokenPerm != "rw" {
			log.Printf("[PERMISSION] Write access denied for repo API token (permission: %s)", tokenPerm)
			c.JSON(http.StatusForbidden, gin.H{
				"error": "insufficient permissions: write operations require 'rw' token permission",
			})
			return false
		}
		return true
	}

	if h.permMiddleware == nil {
		return true // No middleware, allow access
	}

	userRole, err := h.permMiddleware.GetUserOrgRole(orgID, userID)
	if err != nil {
		log.Printf("[PERMISSION] Failed to get user role for %s in org %s: %v", userID, orgID, err)
		return false // On error, deny access (fail-closed)
	}

	if !middleware.HasRequiredOrgRole(userRole, middleware.RoleUser) {
		log.Printf("[PERMISSION] Write access denied for user %s with role %s", userID, userRole)
		c.JSON(http.StatusForbidden, gin.H{
			"error": "insufficient permissions: write operations require 'user' role or higher",
		})
		return false
	}

	return true
}

// ListDirectory returns the contents of a directory
// Implements Seafile API: GET /api2/repos/:repo_id/dir/?p=/path
// Reads from fs_objects for proper Seafile compatibility
func (h *FileHandler) ListDirectory(c *gin.Context) {
	repoID := c.Param("repo_id")
	dirPath := c.DefaultQuery("p", "/")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	// Normalize path
	dirPath = normalizePath(dirPath)

	// ========================================================================
	// PERMISSION CHECK: User must have at least read access to library
	// ========================================================================
	if h.permMiddleware != nil {
		hasAccess, err := h.permMiddleware.HasLibraryAccessCtx(c, orgID, userID, repoID, middleware.PermissionR)
		if err != nil {
			log.Printf("[ListDirectory] Failed to check permissions: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check permissions"})
			return
		}

		if !hasAccess {
			if respondIfLibraryMissing(c, h.db.Session(), orgID, repoID) {
				return
			}
			log.Printf("[ListDirectory] Permission denied: user %q does not have access to library %q", userID, repoID)
			c.JSON(http.StatusForbidden, gin.H{"error": "you do not have access to this library"})
			return
		}
	}

	// Resolve actual permission for the user (rw/r based on share or ownership)
	perm := "rw"
	if h.permMiddleware != nil {
		rawPerm, err := h.permMiddleware.GetLibraryPermissionRaw(orgID, userID, repoID)
		if err == nil && rawPerm != "" {
			perm = rawPerm
		}
	}

	// ========================================================================
	// ENCRYPTION CHECK: Encrypted libraries require active decrypt session
	// ========================================================================
	if !h.requireDecryptSession(c, orgID, userID, repoID) {
		return
	}

	// Check if database is available
	if h.db == nil {
		c.Header("oid", "")
		c.Header("dir_perm", perm)
		c.JSON(http.StatusOK, []Dirent{})
		return
	}

	// Get library's head_commit_id
	var libID, headCommitID string
	err := h.db.Session().Query(`
		SELECT library_id, head_commit_id FROM libraries
		WHERE org_id = ? AND library_id = ?
	`, orgID, repoID).Scan(&libID, &headCommitID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
		return
	}

	// If no head commit, return empty directory
	if headCommitID == "" {
		c.Header("oid", "")
		c.Header("dir_perm", perm)
		c.JSON(http.StatusOK, []Dirent{})
		return
	}

	// Get root_fs_id from the head commit
	var rootFSID string
	err = h.db.Session().Query(`
		SELECT root_fs_id FROM commits
		WHERE library_id = ? AND commit_id = ?
	`, repoID, headCommitID).Scan(&rootFSID)
	if err != nil {
		log.Printf("ListDirectory: failed to get commit %s: %v", headCommitID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load library data"})
		return
	}

	// All-zeros root means an empty library (no files).
	// This is a valid state after the desktop client syncs a deletion of all files.
	if rootFSID == "" || rootFSID == strings.Repeat("0", 40) {
		if dirPath == "/" {
			c.Header("oid", rootFSID)
			c.Header("dir_perm", perm)
			c.JSON(http.StatusOK, []Dirent{})
			return
		}
		c.JSON(http.StatusNotFound, gin.H{"error": "directory not found"})
		return
	}

	// Traverse from root to requested path
	currentFSID := rootFSID
	if dirPath != "/" {
		// Split path into components and traverse
		parts := strings.Split(strings.Trim(dirPath, "/"), "/")
		for _, part := range parts {
			if part == "" {
				continue
			}

			// Get current directory's entries
			var entriesJSON string
			err = h.db.Session().Query(`
				SELECT dir_entries FROM fs_objects
				WHERE library_id = ? AND fs_id = ?
			`, repoID, currentFSID).Scan(&entriesJSON)
			if err != nil {
				log.Printf("ListDirectory: failed to get fs_object %s: %v", currentFSID, err)
				c.JSON(http.StatusNotFound, gin.H{"error": "directory not found"})
				return
			}

			// Parse entries and find the next component
			var entries []FSEntry
			if entriesJSON != "" && entriesJSON != "[]" {
				if err := json.Unmarshal([]byte(entriesJSON), &entries); err != nil {
					log.Printf("ListDirectory: failed to parse entries for %s: %v", currentFSID, err)
					c.JSON(http.StatusInternalServerError, gin.H{"error": "invalid directory data"})
					return
				}
			}

			// Find the child directory
			found := false
			for _, entry := range entries {
				if entry.Name == part {
					// Check if it's a directory (mode & 0170000 == 040000 for dirs)
					if entry.Mode&0170000 == 040000 || entry.Mode == ModeDir {
						currentFSID = entry.ID
						found = true
						break
					} else {
						// Path component is not a directory
						c.JSON(http.StatusBadRequest, gin.H{"error": "path is not a directory"})
						return
					}
				}
			}

			if !found {
				c.JSON(http.StatusNotFound, gin.H{"error": "directory not found"})
				return
			}
		}
	}

	// Get the target directory's entries
	var entriesJSON string
	err = h.db.Session().Query(`
		SELECT dir_entries FROM fs_objects
		WHERE library_id = ? AND fs_id = ?
	`, repoID, currentFSID).Scan(&entriesJSON)
	if err != nil {
		log.Printf("ListDirectory: failed to get target fs_object %s: %v", currentFSID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "library data is unavailable"})
		return
	}

	// Parse entries
	var entries []FSEntry
	if entriesJSON != "" && entriesJSON != "[]" {
		if err := json.Unmarshal([]byte(entriesJSON), &entries); err != nil {
			log.Printf("ListDirectory: failed to parse target entries for %s: %v", currentFSID, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "corrupted directory data"})
			return
		}
	}

	// Get starred files for this user and repo to check starred status
	// userID already declared above in permission check
	starredPaths := make(map[string]bool)
	if userID != "" {
		iter := h.db.Session().Query(`
			SELECT path FROM starred_files WHERE user_id = ? AND repo_id = ?
		`, userID, repoID).Iter()
		var starredPath string
		for iter.Scan(&starredPath) {
			starredPaths[starredPath] = true
		}
		iter.Close()
	}

	// Convert FSEntry to Dirent for API response
	resolvePersistedModifier := h.newPersistedModifierResolver(orgID)
	direntList := make([]Dirent, 0, len(entries))
	for _, entry := range entries {
		// Determine type from mode
		fileType := "file"
		if entry.Mode&0170000 == 040000 || entry.Mode == ModeDir {
			fileType = "dir"
		}

		// Build full path for starred check
		var fullPath string
		if dirPath == "/" {
			fullPath = "/" + entry.Name
		} else {
			fullPath = dirPath + "/" + entry.Name
		}

		dirent := Dirent{
			ID:         entry.ID,
			Name:       entry.Name,
			Type:       fileType,
			Size:       entry.Size,
			MTime:      entry.MTime,
			Permission: perm,
			ParentDir:  dirPath,
			Starred:    starredPaths[fullPath],
		}

		// Normalize the persisted modifier so API clients see the real identity when it
		// can be resolved, regardless of whether the entry stored an email or a user id.
		modifierEmail, modifierName := resolvePersistedModifier(entry.Modifier)
		applyResolvedModifierToDirent(&dirent, modifierEmail, modifierName)

		direntList = append(direntList, dirent)
	}

	// Seafile API /api2/repos/:id/dir/ always returns flat array
	// Set oid header (directory's FS ID) - required by Seafile desktop client file browser
	c.Header("oid", currentFSID)
	c.Header("dir_perm", perm)
	c.JSON(http.StatusOK, direntList)
}

// generatePathID creates a deterministic ID for a file/dir path
// This is a placeholder - in a full implementation, IDs come from fs_objects
func generatePathID(orgID, repoID, filePath string) string {
	hash := sha256.Sum256([]byte(orgID + "/" + repoID + filePath))
	return hex.EncodeToString(hash[:20]) // 40 character hex string like Seafile
}

// DirectoryOperation handles directory operations (mkdir, rename, revert)
// Seafile API: POST /api2/repos/:repo_id/dir/?p=/path&operation=mkdir|rename|revert
func (h *FileHandler) DirectoryOperation(c *gin.Context) {
	operation := c.Query("operation")
	if operation == "" {
		// Default to mkdir for backward compatibility
		operation = "mkdir"
	}

	switch operation {
	case "mkdir":
		h.CreateDirectory(c)
	case "rename":
		h.RenameDirectory(c)
	case "revert":
		h.RevertDirectory(c)
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid operation"})
	}
}

// CreateDirectory creates a new directory
func (h *FileHandler) CreateDirectory(c *gin.Context) {
	repoID := c.Param("repo_id")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")
	dirPath := c.Query("p")

	if dirPath == "" {
		dirPath = c.PostForm("p")
	}

	dirPath = normalizePath(dirPath)
	if dirPath == "/" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot create root directory"})
		return
	}

	// PERMISSION CHECK: Readonly and guest users cannot create directories
	if !h.requireWritePermission(c, orgID, userID) {
		return
	}

	// CUSTOM PERMISSION CHECK: create flag
	if h.permMiddleware != nil && !h.permMiddleware.RequirePermFlag(c, "create") {
		c.JSON(http.StatusForbidden, gin.H{"error": "create is not allowed by your permission"})
		return
	}

	// ENCRYPTION CHECK: Encrypted libraries require active decrypt session
	if !h.requireDecryptSession(c, orgID, userID, repoID) {
		return
	}

	fsHelper := NewFSHelper(h.db)

	// Get parent path and new directory name
	parentPath := path.Dir(dirPath)
	if parentPath == "." {
		parentPath = "/"
	}
	dirName := path.Base(dirPath)
	var newCommitID string

	err := retryLibraryHeadMutation("CreateDirectory", func() error {
		snapshot, err := fsHelper.GetLibraryHeadSnapshot(repoID)
		if err != nil {
			return fmt.Errorf("%w: %v", errLibraryNotFound, err)
		}

		result, err := fsHelper.TraverseToPathFromSnapshot(repoID, snapshot, parentPath)
		if err != nil {
			return fmt.Errorf("%w: %v", errParentDirectoryNotFound, err)
		}

		var parentEntries []FSEntry
		if parentPath == "/" {
			parentEntries = result.Entries
		} else {
			if result.TargetFSID == "" {
				return errParentDirectoryNotFound
			}
			parentEntries, err = fsHelper.GetDirectoryEntries(repoID, result.TargetFSID)
			if err != nil {
				return fmt.Errorf("failed to read parent directory: %w", err)
			}
		}

		for _, entry := range parentEntries {
			if entry.Name == dirName {
				return errDirectoryExists
			}
		}

		newDirFSID, err := fsHelper.CreateDirectoryFSObject(repoID, []FSEntry{})
		if err != nil {
			return fmt.Errorf("failed to create directory: %w", err)
		}

		newEntry := FSEntry{
			Name:  dirName,
			ID:    newDirFSID,
			Mode:  ModeDir,
			MTime: time.Now().Unix(),
		}
		newEntries := AddEntryToList(parentEntries, newEntry)

		newParentFSID, err := fsHelper.CreateDirectoryFSObject(repoID, newEntries)
		if err != nil {
			return fmt.Errorf("failed to update parent directory: %w", err)
		}

		var newRootFSID string
		if parentPath == "/" {
			newRootFSID = newParentFSID
		} else {
			parentDirName := path.Base(parentPath)
			updatedGrandparentEntries := make([]FSEntry, len(result.Entries))
			for i, entry := range result.Entries {
				if entry.Name == parentDirName {
					entry.ID = newParentFSID
				}
				updatedGrandparentEntries[i] = entry
			}

			newGrandparentFSID, err := fsHelper.CreateDirectoryFSObject(repoID, updatedGrandparentEntries)
			if err != nil {
				return fmt.Errorf("failed to update grandparent directory: %w", err)
			}

			newRootFSID, err = fsHelper.RebuildPathToRoot(repoID, result, newGrandparentFSID)
			if err != nil {
				return fmt.Errorf("failed to rebuild path: %w", err)
			}
		}

		description := fmt.Sprintf("Added directory \"%s\"", dirName)
		commitID, err := fsHelper.CreateCommit(repoID, userID, newRootFSID, snapshot.HeadCommitID, description)
		if err != nil {
			return fmt.Errorf("failed to create commit: %w", err)
		}

		if err := fsHelper.UpdateLibraryHeadFromSnapshot(snapshot, repoID, commitID, snapshot.HeadCommitID); err != nil {
			return err
		}

		newCommitID = commitID
		return nil
	})
	if err != nil {
		switch {
		case errors.Is(err, errLibraryNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
		case errors.Is(err, errParentDirectoryNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": errorMessageOrFallback(err, "parent directory not found")})
		case errors.Is(err, errDirectoryExists):
			c.JSON(http.StatusConflict, gin.H{"error": "directory already exists"})
		case errors.Is(err, ErrLibraryHeadConflict):
			c.JSON(http.StatusConflict, gin.H{"error": "library was modified concurrently; retry the create"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create directory"})
		}
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"success":   true,
		"repo_id":   repoID,
		"path":      dirPath,
		"commit_id": newCommitID,
	})
}

// RenameDirectory renames a directory
func (h *FileHandler) RenameDirectory(c *gin.Context) {
	repoID := c.Param("repo_id")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")
	dirPath := c.Query("p")

	var renameReq struct {
		Newname string `json:"newname"`
	}
	c.ShouldBindJSON(&renameReq) //nolint:errcheck
	newName := renameReq.Newname

	if dirPath == "" || dirPath == "/" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid path"})
		return
	}

	if newName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "newname is required"})
		return
	}

	dirPath = normalizePath(dirPath)

	// PERMISSION CHECK: Readonly and guest users cannot rename directories
	if !h.requireWritePermission(c, orgID, userID) {
		return
	}

	// CUSTOM PERMISSION CHECK: modify flag
	if h.permMiddleware != nil && !h.permMiddleware.RequirePermFlag(c, "modify") {
		c.JSON(http.StatusForbidden, gin.H{"error": "modify is not allowed by your permission"})
		return
	}

	// ENCRYPTION CHECK: Encrypted libraries require active decrypt session. This runs
	// BEFORE lock enforcement so an encrypted library stays completely inaccessible
	// without an active decrypt session — otherwise a 403 "locked" with lock_owner would
	// leak lock metadata of an encrypted library before the decrypt gate.
	if !h.requireDecryptSession(c, orgID, userID, repoID) {
		return
	}

	// LOCK ENFORCEMENT: a folder cannot be renamed if it contains a file locked by
	// another user (renaming the folder relocates every descendant path).
	if !h.requireSubtreeNotLockedByOther(c, repoID, dirPath, userID) {
		return
	}

	fsHelper := NewFSHelper(h.db)
	oldName := path.Base(dirPath)
	errDirectoryNotFound := errors.New("directory not found")
	errNameExists := errors.New("name already exists")
	var result *PathTraverseResult
	var mtime int64

	err := retryLibraryHeadMutation("RenameDirectory", func() error {
		currentResult, snapshot, err := fsHelper.TraverseToPathAtHead(repoID, dirPath)
		if err != nil {
			return err
		}
		if currentResult.TargetEntry == nil {
			return errDirectoryNotFound
		}

		for _, entry := range currentResult.Entries {
			if entry.Name == newName {
				return errNameExists
			}
		}

		newEntries := UpdateEntryInList(currentResult.Entries, oldName, newName)
		newParentFSID, err := fsHelper.CreateDirectoryFSObject(repoID, newEntries)
		if err != nil {
			return fmt.Errorf("failed to update directory: %w", err)
		}

		newRootFSID, err := fsHelper.RebuildPathToRoot(repoID, currentResult, newParentFSID)
		if err != nil {
			return fmt.Errorf("failed to rebuild path: %w", err)
		}

		description := fmt.Sprintf("Renamed \"%s\" to \"%s\"", oldName, newName)
		commitID, err := fsHelper.CreateCommit(repoID, userID, newRootFSID, snapshot.HeadCommitID, description)
		if err != nil {
			return fmt.Errorf("failed to create commit: %w", err)
		}

		if err := fsHelper.UpdateLibraryHeadFromSnapshot(snapshot, repoID, commitID, snapshot.HeadCommitID); err != nil {
			return err
		}

		for _, entry := range currentResult.Entries {
			if entry.Name == oldName {
				mtime = entry.MTime
				break
			}
		}
		result = currentResult
		return nil
	})
	if err != nil {
		switch {
		case errors.Is(err, errDirectoryNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "directory not found"})
		case errors.Is(err, errNameExists):
			c.JSON(http.StatusConflict, gin.H{"error": "name already exists"})
		case errors.Is(err, ErrLibraryHeadConflict):
			c.JSON(http.StatusConflict, gin.H{"error": "library was modified concurrently; retry the rename"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to rename directory"})
		}
		return
	}

	// Move directory tags from old path to new path (async, preserves tags on rename)
	newDirPath := path.Join(path.Dir(dirPath), newName)
	go MoveFileTagsByPrefix(h.db, repoID, dirPath, newDirPath)

	// Carry the operator's own locks under the renamed folder to the new prefix so they
	// don't dangle at the old path (any other user's lock here would have blocked the
	// rename upstream).
	relocateLocksAfterMove(h, repoID, dirPath, newDirPath, userID)

	// Get directory info for response
	parentDir := path.Dir(dirPath)
	if parentDir == "" || parentDir == "." {
		parentDir = "/"
	}

	// Return Seafile-compatible response
	c.JSON(http.StatusOK, gin.H{
		"type":       "dir",
		"repo_id":    repoID,
		"parent_dir": parentDir,
		"obj_name":   newName,
		"obj_id":     result.TargetEntry.ID,
		"mtime":      time.Unix(mtime, 0).UTC().Format("2006-01-02T15:04:05+00:00"),
	})
}

// FileOperation handles file operations (rename, create)
// Seafile API: POST /api2/repos/:repo_id/file/?p=/path&operation=rename|create
// Note: operation can be in query string OR in form body (frontend sends it in body)
func (h *FileHandler) FileOperation(c *gin.Context) {
	repoID := c.Param("repo_id")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	operation := c.Query("operation")
	if operation == "" {
		// Also check form body - frontend sends operation in POST body
		operation = c.PostForm("operation")
	}
	if operation == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "operation parameter is required"})
		return
	}

	// ========================================================================
	// PERMISSION CHECK: All file operations require write permission
	// ========================================================================
	if h.permMiddleware != nil {
		hasWrite, err := h.permMiddleware.HasLibraryAccessCtx(c, orgID, userID, repoID, middleware.PermissionRW)
		if err != nil {
			log.Printf("[FileOperation] Failed to check permissions: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check permissions"})
			return
		}

		if !hasWrite {
			log.Printf("[FileOperation] Permission denied: user %q does not have write permission to library %q", userID, repoID)
			c.JSON(http.StatusForbidden, gin.H{"error": "you do not have write permission to this library"})
			return
		}
	}

	// ========================================================================
	// ENCRYPTION CHECK: Encrypted libraries require active decrypt session
	// ========================================================================
	if !h.requireDecryptSession(c, orgID, userID, repoID) {
		return
	}

	switch operation {
	case "rename":
		h.RenameFile(c)
	case "create":
		h.CreateFile(c)
	case "move":
		h.MoveFile(c)
	case "copy":
		h.CopyFile(c)
	case "revert":
		h.RevertFile(c)
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid operation"})
	}
}

// RenameFile renames a file
func (h *FileHandler) RenameFile(c *gin.Context) {
	repoID := c.Param("repo_id")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")
	filePath := c.Query("p")

	var renameFileReq struct {
		Newname string `json:"newname"`
	}
	c.ShouldBindJSON(&renameFileReq) //nolint:errcheck
	newName := renameFileReq.Newname

	if filePath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path is required"})
		return
	}

	if newName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "newname is required"})
		return
	}

	filePath = normalizePath(filePath)
	if filePath == "/" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot rename root"})
		return
	}

	// PERMISSION CHECK: Readonly and guest users cannot rename files
	if !h.requireWritePermission(c, orgID, userID) {
		return
	}

	// CUSTOM PERMISSION CHECK: modify flag
	if h.permMiddleware != nil && !h.permMiddleware.RequirePermFlag(c, "modify") {
		c.JSON(http.StatusForbidden, gin.H{"error": "modify is not allowed by your permission"})
		return
	}

	// LOCK ENFORCEMENT: a file locked by another user cannot be renamed — and a
	// folder cannot be renamed if it contains a file locked by another user.
	if !h.requireSubtreeNotLockedByOther(c, repoID, filePath, userID) {
		return
	}

	fsHelper := NewFSHelper(h.db)
	oldName := path.Base(filePath)
	errFileNotFound := errors.New("file not found")
	errNameExists := errors.New("name already exists")
	var result *PathTraverseResult
	var fileSize int64
	var mtime int64

	err := retryLibraryHeadMutation("RenameFile", func() error {
		currentResult, snapshot, err := fsHelper.TraverseToPathAtHead(repoID, filePath)
		if err != nil {
			return err
		}
		if currentResult.TargetEntry == nil {
			return errFileNotFound
		}

		for _, entry := range currentResult.Entries {
			if entry.Name == newName {
				return errNameExists
			}
		}

		newEntries := UpdateEntryInList(currentResult.Entries, oldName, newName)
		newParentFSID, err := fsHelper.CreateDirectoryFSObject(repoID, newEntries)
		if err != nil {
			return fmt.Errorf("failed to update directory: %w", err)
		}

		newRootFSID, err := fsHelper.RebuildPathToRoot(repoID, currentResult, newParentFSID)
		if err != nil {
			return fmt.Errorf("failed to rebuild path: %w", err)
		}

		description := fmt.Sprintf("Renamed \"%s\" to \"%s\"", oldName, newName)
		commitID, err := fsHelper.CreateCommit(repoID, userID, newRootFSID, snapshot.HeadCommitID, description)
		if err != nil {
			return fmt.Errorf("failed to create commit: %w", err)
		}

		if err := fsHelper.UpdateLibraryHeadFromSnapshot(snapshot, repoID, commitID, snapshot.HeadCommitID); err != nil {
			return err
		}

		for _, entry := range currentResult.Entries {
			if entry.Name == oldName {
				fileSize = entry.Size
				mtime = entry.MTime
				break
			}
		}
		result = currentResult
		return nil
	})
	if err != nil {
		switch {
		case errors.Is(err, errFileNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
		case errors.Is(err, errNameExists):
			c.JSON(http.StatusConflict, gin.H{"error": "name already exists"})
		case errors.Is(err, ErrLibraryHeadConflict):
			c.JSON(http.StatusConflict, gin.H{"error": "library was modified concurrently; retry the rename"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to rename file"})
		}
		return
	}

	// Move file tags from old path to new path (async, preserves tags on rename)
	newFilePath := path.Join(path.Dir(filePath), newName)
	go MoveFileTagsByPath(h.db, repoID, filePath, newFilePath)

	// Carry the operator's own locks to the new path so they don't dangle at the old
	// one (any other user's lock here would have blocked the rename upstream).
	relocateLocksAfterMove(h, repoID, filePath, newFilePath, userID)

	// Get file info for response
	parentDir := path.Dir(filePath)
	if parentDir == "" || parentDir == "." {
		parentDir = "/"
	}

	// Return Seafile-compatible response
	c.JSON(http.StatusOK, gin.H{
		"type":        "file",
		"repo_id":     repoID,
		"parent_dir":  parentDir,
		"obj_name":    newName,
		"obj_id":      result.TargetEntry.ID,
		"size":        fileSize,
		"mtime":       time.Unix(mtime, 0).UTC().Format("2006-01-02T15:04:05+00:00"),
		"is_locked":   false,
		"can_preview": false,
		"can_edit":    false,
	})
}

// CreateFile creates a new empty file
// For Office files (.docx, .xlsx, .pptx), creates a minimal valid document so OnlyOffice can edit it
func (h *FileHandler) CreateFile(c *gin.Context) {
	repoID := c.Param("repo_id")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")
	filePath := c.Query("p")

	if filePath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path is required"})
		return
	}

	filePath = normalizePath(filePath)

	// PERMISSION CHECK: Readonly and guest users cannot create files
	if !h.requireWritePermission(c, orgID, userID) {
		return
	}

	// CUSTOM PERMISSION CHECK: create flag
	if h.permMiddleware != nil && !h.permMiddleware.RequirePermFlag(c, "create") {
		c.JSON(http.StatusForbidden, gin.H{"error": "create is not allowed by your permission"})
		return
	}

	fsHelper := NewFSHelper(h.db)

	// Get parent path and file name
	parentPath := path.Dir(filePath)
	if parentPath == "." {
		parentPath = "/"
	}
	fileName := path.Base(filePath)

	// Check if this file type needs a template (Office files)
	ext := strings.ToLower(filepath.Ext(fileName))
	templateContent, err := templates.GetTemplateForExtension(ext)
	if err != nil {
		log.Printf("[CreateFile] Error getting template for %s: %v", ext, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create file template"})
		return
	}

	var fileSize int64
	var blockIDs []string
	var externalBlockID string
	var templateBlockData *storage.BlockData
	// These survive across retry attempts. Office templates are content-addressed
	// by SHA-256, so reusing the first successful upload avoids rewriting the
	// same bytes on later CAS retries.
	var templateBlockStore *storage.BlockStore
	var templateStorageClass string
	var templateMaterializedStorageClass string
	var templateStorageKey string
	var templateBlockStored bool
	var templateTarget BlockMaterializationTarget

	if len(templateContent) > 0 {
		fileSize = int64(len(templateContent))

		// Internal storage identity is SHA-256; the Seafile-facing block id in the
		// fs_object (and therefore the derived fs_id) MUST be SHA-1, like every other
		// upload path (see UploadFile). Compute both: SHA-256 addresses the block in
		// storage, SHA-1 is the external id written into fs_objects + mapped below.
		hash := sha256.Sum256(templateContent)
		blockID := hex.EncodeToString(hash[:])
		sha1Hash := sha1.Sum(templateContent)
		externalBlockID = hex.EncodeToString(sha1Hash[:])
		blockIDs = []string{externalBlockID}
		templateBlockData = &storage.BlockData{
			Hash: blockID,
			Data: templateContent,
			Size: fileSize,
		}
	} else {
		// Empty file for non-Office types
		fileSize = 0
		blockIDs = []string{}
	}
	var newFileFSID string
	var newCommitID string

	err = retryLibraryHeadMutation("CreateFile", func() error {
		uploadOperationID := uuid.NewString()

		snapshot, err := fsHelper.GetLibraryHeadSnapshot(repoID)
		if err != nil {
			return fmt.Errorf("%w: %v", errLibraryNotFound, err)
		}

		result, err := fsHelper.TraverseToPathFromSnapshot(repoID, snapshot, parentPath)
		if err != nil {
			return fmt.Errorf("%w: %v", errParentDirectoryNotFound, err)
		}

		var parentEntries []FSEntry
		if parentPath == "/" {
			parentEntries = result.Entries
		} else {
			if result.TargetFSID == "" {
				return errParentDirectoryNotFound
			}
			parentEntries, err = fsHelper.GetDirectoryEntries(repoID, result.TargetFSID)
			if err != nil {
				return fmt.Errorf("failed to read parent directory: %w", err)
			}
		}

		for _, entry := range parentEntries {
			if entry.Name == fileName {
				return errFileExists
			}
		}

		if templateBlockData != nil {
			if templateBlockStore == nil {
				blockStore, storageClass, err := h.resolveLibraryBlockStore(c, orgID, repoID)
				if err != nil {
					return fmt.Errorf("%w: %v", errBlockStorageUnavailable, err)
				}
				if blockStore == nil {
					return errBlockStorageUnavailable
				}
				templateBlockStore = blockStore
				templateStorageClass = storageClass
				templateMaterializedStorageClass = storageClass
			}
			if err := retryCreateFileTemplateBlockMaterializationPhased(func(phase BlockMaterializationPhase) error {
				if templateBlockStored {
					return nil
				}
				templateMaterializedStorageClass = templateStorageClass
				templateStorageKey = ""
				probe, probeErr := probeUploadedBlockReuseFn(h.db, orgID, templateBlockData.Hash)
				if probeErr != nil {
					return fmt.Errorf("probe template block reuse for %s: %w", templateBlockData.Hash, probeErr)
				}
				probe, probeErr = prepareUploadedBlockProbeFn(h.db, orgID, templateBlockData.Hash, probe)
				if probeErr != nil {
					return probeErr
				}
				switch probe.Decision {
				case db.BlockReuseReusable:
					templateMaterializedStorageClass = probe.StorageClass
					var ensureErr error
					templateStorageKey, ensureErr = EnsureReusableBlockPresentForPhase(c.Request.Context(), h.db, templateBlockData.Hash, probe, templateBlockData.Data, h.storageManager, templateBlockStore, templateStorageClass, orgID, phase)
					if ensureErr != nil {
						return fmt.Errorf("failed to verify reusable template block: %w", ensureErr)
					}
					templateTarget = BlockMaterializationTarget{StorageClass: templateMaterializedStorageClass, StorageKey: templateStorageKey}
					templateBlockStored = true
					return nil
				case db.BlockReuseNeedsPut:
					target, resolveErr := ResolveNeedsPutBlockStoreForPhase(h.storageManager, templateBlockStore, templateStorageClass, probe, orgID, templateBlockData.Hash, phase)
					if resolveErr != nil {
						return resolveErr
					}
					templateTarget = target
					templateMaterializedStorageClass = target.StorageClass
					var err error
					templateStorageKey, err = PutBlockMaterializationTarget(c.Request.Context(), h.db, orgID, templateBlockData.Hash, target, templateBlockData.Data, putUploadedBlockAutoDirectFn, nil)
					if err != nil {
						return fmt.Errorf("failed to store file content: %w", err)
					}
					templateBlockStored = true
					log.Printf("[CreateFile] Created Office file %s with template size %d bytes", fileName, fileSize)
					return nil
				case db.BlockReuseBlockedByGC:
					return ErrBlockDeleteInProgress
				}
				return fmt.Errorf("unexpected template block reuse decision %d for %s", probe.Decision, templateBlockData.Hash)
			}, func() error {
				// Keep the freshly stored template block alive and respect the GC
				// delete fence. The up: ref remains TTL-bound after publish-attempt
				// and permanent refs are added below. Use the mapping variant so the
				// SHA-1 (external) -> SHA-256 (internal) row and blocks.sha1 are
				// written from the real bytes — required for desktop downloads (which
				// fetch by SHA-1) and for staging to resolve the fs_object's SHA-1
				// block id back to its storage identity.
				if err := RegisterUploadedBlockTargetAndMapping(c.Request.Context(), h.db, orgID, repoID, templateBlockData.Hash, uploadOperationID, int(fileSize), templateTarget, externalBlockID); err != nil {
					return fmt.Errorf("failed to register template block metadata: %w", err)
				}
				return nil
			}, func() {
				templateBlockStored = false
				templateTarget = BlockMaterializationTarget{}
			}); err != nil {
				return err
			}
		}

		pendingFile, err := fsHelper.prepareFileFSObjectForPublish(repoID, fileName, fileSize, blockIDs)
		if err != nil {
			return fmt.Errorf("failed to create file: %w", err)
		}
		cleanupPendingFilePublish := func() {
			if cleanupErr := CleanupFailedPublishAttempt(h.db, orgID, repoID, "", "", []*pendingPublishedFile{pendingFile}); cleanupErr != nil {
				log.Printf("[CreateFile] WARNING: failed to clean up pending fs_object %s before commit publish: %v", pendingFile.fsID, cleanupErr)
			}
		}
		pendingFiles := []*pendingPublishedFile{pendingFile}

		newEntry := FSEntry{
			Name:     fileName,
			ID:       pendingFile.fsID,
			Mode:     ModeFile,
			MTime:    time.Now().Unix(),
			Size:     fileSize,
			Modifier: modifierIdentityForUser(userID),
		}
		newEntries := AddEntryToList(parentEntries, newEntry)

		newParentFSID, err := fsHelper.CreateDirectoryFSObject(repoID, newEntries)
		if err != nil {
			cleanupPendingFilePublish()
			return fmt.Errorf("failed to update parent directory: %w", err)
		}

		var newRootFSID string
		if parentPath == "/" {
			newRootFSID = newParentFSID
		} else {
			parentDirName := path.Base(parentPath)
			updatedGrandparentEntries := make([]FSEntry, len(result.Entries))
			for i, entry := range result.Entries {
				if entry.Name == parentDirName {
					entry.ID = newParentFSID
				}
				updatedGrandparentEntries[i] = entry
			}

			newGrandparentFSID, err := fsHelper.CreateDirectoryFSObject(repoID, updatedGrandparentEntries)
			if err != nil {
				cleanupPendingFilePublish()
				return fmt.Errorf("failed to update grandparent directory: %w", err)
			}

			newRootFSID, err = fsHelper.RebuildPathToRoot(repoID, result, newGrandparentFSID)
			if err != nil {
				cleanupPendingFilePublish()
				return fmt.Errorf("failed to rebuild path: %w", err)
			}
		}

		description := fmt.Sprintf("Added \"%s\"", fileName)
		commitCreatedAt := time.Now().UTC()
		commitID := buildCommitID(repoID, newRootFSID, description, commitCreatedAt)
		if err := fsHelper.stagePendingPublishedFiles(orgID, repoID, commitID, pendingFiles); err != nil {
			if cleanupErr := CleanupFailedPublishAttempt(h.db, orgID, repoID, commitID, commitID, pendingFiles); cleanupErr != nil {
				return errors.Join(
					fmt.Errorf("failed to stage publish-attempt block references for commit %s: %w", commitID, err),
					fmt.Errorf("cleanup failed publish commit %s: %w", commitID, cleanupErr),
				)
			}
			return fmt.Errorf("failed to stage publish-attempt block references for commit %s: %w", commitID, err)
		}
		if err := queuePendingPublishedFileRepairs(h.db, orgID, repoID, commitID, pendingFiles); err != nil {
			cleanupErr := CleanupFailedPublishAttempt(h.db, orgID, repoID, commitID, commitID, pendingFiles)
			clearErr := clearPendingPublishedFileRepairs(h.db, orgID, repoID, commitID, pendingFiles)
			return errors.Join(
				fmt.Errorf("failed to queue durable publish repair for commit %s: %w", commitID, err),
				cleanupErr,
				clearErr,
			)
		}
		if err := fsHelper.insertCommit(repoID, commitID, userID, newRootFSID, snapshot.HeadCommitID, description, commitCreatedAt); err != nil {
			cleanupErr := CleanupFailedPublishAttempt(h.db, orgID, repoID, commitID, commitID, pendingFiles)
			clearErr := clearPendingPublishedFileRepairs(h.db, orgID, repoID, commitID, pendingFiles)
			return errors.Join(
				fmt.Errorf("failed to create commit: %w", err),
				cleanupErr,
				clearErr,
			)
		}

		if err := fsHelper.UpdateLibraryHeadFromSnapshot(snapshot, repoID, commitID, snapshot.HeadCommitID); err != nil {
			if errors.Is(err, ErrLibraryHeadConflict) {
				if cleanupErr := CleanupFailedPublishAttempt(h.db, orgID, repoID, commitID, commitID, pendingFiles); cleanupErr != nil {
					return fmt.Errorf("failed to clean up conflict publish attempt %s: %w", commitID, cleanupErr)
				}
				if clearErr := clearPendingPublishedFileRepairs(h.db, orgID, repoID, commitID, pendingFiles); clearErr != nil {
					log.Printf("[CreateFile] WARNING: failed to clear queued publish repair for repo=%s commit=%s fs_object=%s after head conflict: %v", repoID, commitID, pendingFile.fsID, clearErr)
				}
			}
			return err
		}
		if ownerErr := clearPendingPublishedFileOwnersFn(h.db, repoID, pendingFiles); ownerErr != nil {
			log.Printf("[CreateFile] WARNING: published repo=%s commit=%s but failed to clear pending fs_object owners: %v", repoID, commitID, ownerErr)
		}
		if err := fsHelper.promotePendingPublishedFiles(orgID, repoID, commitID, pendingFiles); err != nil {
			log.Printf("[CreateFile] WARNING: head updated for repo=%s commit=%s but failed to promote block references for fs_object %s: %v", repoID, commitID, pendingFile.fsID, err)
			schedulePendingPublishedFileRepairs(h.db, orgID, repoID, commitID, pendingFiles, "CreateFile")
		} else if clearErr := clearPendingPublishedFileRepairs(h.db, orgID, repoID, commitID, pendingFiles); clearErr != nil {
			log.Printf("[CreateFile] WARNING: published repo=%s commit=%s but failed to clear queued publish repair for fs_object %s: %v", repoID, commitID, pendingFile.fsID, clearErr)
		}

		newFileFSID = pendingFile.fsID
		newCommitID = commitID
		return nil
	})
	if err != nil {
		writeCreateFileError(c, err)
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"success":   true,
		"id":        newFileFSID,
		"name":      fileName,
		"size":      fileSize,
		"commit_id": newCommitID,
	})
}

// DeleteDirectory deletes a directory
func (h *FileHandler) DeleteDirectory(c *gin.Context) {
	repoID := c.Param("repo_id")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")
	dirPath := c.Query("p")

	log.Printf("[DeleteDirectory] repoID=%s, orgID=%s, userID=%s, dirPath=%s", repoID, orgID, userID, dirPath)

	if dirPath == "" || dirPath == "/" {
		log.Printf("[DeleteDirectory] Invalid path: %s", dirPath)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid path"})
		return
	}

	dirPath = normalizePath(dirPath)

	// PERMISSION CHECK: Readonly and guest users cannot delete directories
	if !h.requireWritePermission(c, orgID, userID) {
		return
	}

	// CUSTOM PERMISSION CHECK: delete flag
	if h.permMiddleware != nil && !h.permMiddleware.RequirePermFlag(c, "delete") {
		c.JSON(http.StatusForbidden, gin.H{"error": "delete is not allowed by your permission"})
		return
	}

	// ENCRYPTION CHECK: Encrypted libraries require active decrypt session. This runs
	// BEFORE lock enforcement so an encrypted library stays completely inaccessible
	// without an active decrypt session — otherwise a 403 "locked" with lock_owner would
	// leak lock metadata of an encrypted library before the decrypt gate.
	if !h.requireDecryptSession(c, orgID, userID, repoID) {
		return
	}

	// LOCK ENFORCEMENT: a folder cannot be deleted if it contains a file locked by
	// another user (deleting the folder would strip that lock's file).
	if !h.requireSubtreeNotLockedByOther(c, repoID, dirPath, userID) {
		return
	}

	fsHelper := NewFSHelper(h.db)
	dirName := path.Base(dirPath)
	errDirectoryNotFound := errors.New("directory not found")
	errPathNotDirectory := errors.New("path is not a directory")
	var totalSize int64
	var fileCount int64
	var newCommitID string

	err := retryLibraryHeadMutation("DeleteDirectory", func() error {
		result, snapshot, err := fsHelper.TraverseToPathAtHead(repoID, dirPath)
		if err != nil {
			return err
		}
		if result.TargetEntry == nil {
			return errDirectoryNotFound
		}
		if result.TargetEntry.Mode != ModeDir && result.TargetEntry.Mode&0170000 != 040000 {
			return errPathNotDirectory
		}

		_, currentTotalSize, currentFileCount, _ := fsHelper.collectDirStats(repoID, result.TargetFSID)
		newEntries := RemoveEntryFromList(result.Entries, dirName)
		newParentFSID, err := fsHelper.CreateDirectoryFSObject(repoID, newEntries)
		if err != nil {
			return fmt.Errorf("failed to update directory: %w", err)
		}

		newRootFSID, err := fsHelper.RebuildPathToRoot(repoID, result, newParentFSID)
		if err != nil {
			return fmt.Errorf("failed to rebuild path: %w", err)
		}

		description := fmt.Sprintf("Removed directory \"%s\"", dirName)
		commitID, err := fsHelper.CreateCommit(repoID, userID, newRootFSID, snapshot.HeadCommitID, description)
		if err != nil {
			return fmt.Errorf("failed to create commit: %w", err)
		}

		if err := fsHelper.UpdateLibraryHeadFromSnapshot(snapshot, repoID, commitID, snapshot.HeadCommitID); err != nil {
			return err
		}

		totalSize = currentTotalSize
		fileCount = currentFileCount
		newCommitID = commitID
		return nil
	})
	if err != nil {
		switch {
		case errors.Is(err, errDirectoryNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "directory not found"})
		case errors.Is(err, errPathNotDirectory):
			c.JSON(http.StatusBadRequest, gin.H{"error": "path is not a directory"})
		case errors.Is(err, ErrLibraryHeadConflict):
			c.JSON(http.StatusConflict, gin.H{"error": "library was modified concurrently; retry the delete"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete directory"})
		}
		return
	}

	// Block references are released when GC sweeps the now-unreachable fs_objects
	// (the deleted tree stays in fs_objects until version retention expires), so
	// there is no inline ref decrement here. Storage-quota counters still update
	// immediately to reflect the user-visible deletion.
	if totalSize > 0 {
		go traffic.DecrementStorageCounters(h.db, orgID, userID, repoID, totalSize, fileCount)
	}

	// Clean up file tags for the deleted directory and its contents (async, non-blocking)
	go h.cleanupFileTagsForPrefix(repoID, dirPath)

	// Drop the operator's own locks under the just-deleted directory so they don't linger
	// as orphans (any other user's lock here would have blocked the delete upstream).
	clearLocksAfterDelete(h, repoID, dirPath, userID)

	c.JSON(http.StatusOK, gin.H{
		"success":   true,
		"commit_id": newCommitID,
	})
}

// GetFileInfo returns information about a file
// Implements: GET /api2/repos/:repo_id/file/?p=/path
func (h *FileHandler) GetFileInfo(c *gin.Context) {
	repoID := c.Param("repo_id")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")
	filePath := c.Query("p")

	if filePath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path is required"})
		return
	}

	filePath = normalizePath(filePath)

	// ENCRYPTION CHECK: Encrypted libraries require active decrypt session
	if !h.requireDecryptSession(c, orgID, userID, repoID) {
		return
	}

	// PERMISSION CHECK: User must have at least read access to the library
	if h.permMiddleware != nil {
		hasAccess, err := h.permMiddleware.HasLibraryAccessCtx(c, orgID, userID, repoID, middleware.PermissionR)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check permissions"})
			return
		}
		if !hasAccess {
			if respondIfLibraryMissing(c, h.db.Session(), orgID, repoID) {
				return
			}
			c.JSON(http.StatusForbidden, gin.H{"error": "you do not have access to this library"})
			return
		}
	}

	// CUSTOM PERMISSION CHECK: download flag
	if h.permMiddleware != nil && !h.permMiddleware.RequirePermFlag(c, "download") {
		c.JSON(http.StatusForbidden, gin.H{"error": "download is not allowed by your permission"})
		return
	}

	// Seafile API compatibility: GET /api2/repos/{id}/file/?p={path}&reuse=1
	// returns a download URL string (not JSON). The seafile-js library expects this.
	// Detect api2 requests by checking the URL path prefix or the "reuse" parameter.
	if c.Query("reuse") != "" || strings.HasPrefix(c.Request.URL.Path, "/api2/") {
		h.getFileDownloadURL(c, orgID, userID, repoID, filePath)
		return
	}

	fsHelper := NewFSHelper(h.db)

	// Traverse to the file
	result, err := fsHelper.TraverseToPath(repoID, filePath)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	if result.TargetEntry == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
		return
	}

	entry := result.TargetEntry
	isDir := entry.Mode == ModeDir || entry.Mode&0170000 == 040000
	fileType := "file"
	if isDir {
		fileType = "dir"
	}

	// Get library info for repo name
	var repoName string
	h.db.Session().Query(`
		SELECT name FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, repoID).Scan(&repoName)

	// Check if file is starred
	starred := false
	starredHandler := NewStarredHandler(h.db)
	starred = starredHandler.IsFileStarred(userID, repoID, filePath)

	// Construct view_url using the request origin for browser accessibility
	viewURL := fmt.Sprintf("%s/lib/%s/file%s", httputil.GetBrowserURL(c, h.serverURL), repoID, filePath)

	// Resolve actual permission for the user
	perm := "rw"
	if h.permMiddleware != nil {
		rawPerm, err := h.permMiddleware.GetLibraryPermissionRaw(orgID, userID, repoID)
		if err == nil && rawPerm != "" {
			perm = rawPerm
		}
	}

	response := gin.H{
		"id":         entry.ID,
		"type":       fileType,
		"name":       entry.Name,
		"size":       entry.Size,
		"mtime":      entry.MTime,
		"permission": perm,
		"starred":    starred,
		"repo_id":    repoID,
		"repo_name":  repoName,
		"parent_dir": result.ParentPath,
		"view_url":   viewURL,
	}

	c.JSON(http.StatusOK, response)
}

// getFileDownloadURL returns a plain download URL string (Seafile api2 compatible).
// This is what seafile-js expects from GET /api2/repos/{id}/file/?p={path}&reuse=1.
func (h *FileHandler) getFileDownloadURL(c *gin.Context, orgID, userID, repoID, filePath string) {
	if h.tokenCreator == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "service not available"})
		return
	}

	token, err := h.tokenCreator.CreateDownloadToken(orgID, repoID, filePath, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate download link"})
		return
	}

	filename := filepath.Base(filePath)
	downloadURL := fmt.Sprintf("%s/seafhttp/files/%s/%s", httputil.GetBrowserURL(c, h.serverURL), token, filename)
	// Return as JSON-encoded string (with double quotes).
	// Seafile clients strip the first and last character (the quotes) to extract the URL.
	c.JSON(http.StatusOK, downloadURL)
}

// fileLastModifierWalkCap bounds the commit-blame walk in resolveFileLastModifier so
// a single file/detail request can never traverse an unbounded history. If the commit
// that introduced the current fs_id is not reached within this many commits, the
// modifier is reported as unresolved ("") rather than misattributed to whoever happened
// to author the oldest commit examined -- a wrong-but-subtle answer is worse than none.
//
// This blame walk is the fallback path only. Files written through upload, OnlyOffice,
// copy/move and the desktop client carry a Modifier on the fs entry, so file/detail
// reads it directly and never walks. The walk (bounded at cap * path-depth fs reads)
// is reached mainly for older entries created before modifier stamping, where its cost
// is capped and its result acceptable-or-empty.
const fileLastModifierWalkCap = 64

// resolveFileLastModifier returns the user id of the author who last changed the file
// at filePath -- the creator of the commit that introduced its current fs_id -- by
// walking the commit parent chain from HEAD. It returns "" when it cannot be
// resolved, in which case the caller must NOT fall back to the requesting user.
func (h *FileHandler) resolveFileLastModifier(repoID, filePath, currentFSID string) string {
	if h.db == nil || currentFSID == "" {
		return ""
	}
	fsHelper := NewFSHelper(h.db)

	headCommitID, err := fsHelper.GetHeadCommitID(repoID)
	if err != nil || headCommitID == "" {
		return ""
	}

	fileFSIDAt := func(rootFSID string) string {
		res, err := fsHelper.TraverseToPathFromRoot(repoID, rootFSID, filePath)
		if err != nil || res == nil || res.TargetEntry == nil {
			return ""
		}
		return res.TargetEntry.ID
	}

	loadCommit := func(commitID string) (commitWalkNode, bool) {
		var node commitWalkNode
		var creatorUUID gocql.UUID
		if err := h.db.Session().Query(
			`SELECT root_fs_id, parent_id, creator_id FROM commits WHERE library_id = ? AND commit_id = ?`,
			repoID, commitID,
		).Scan(&node.rootFSID, &node.parentID, &creatorUUID); err != nil {
			return commitWalkNode{}, false
		}
		node.creator = creatorUUID.String()
		return node, true
	}

	return resolveLastModifierFromWalk(headCommitID, currentFSID, fileLastModifierWalkCap, loadCommit, fileFSIDAt)
}

// commitWalkNode is the slice of a commit row the blame walk needs.
type commitWalkNode struct {
	rootFSID string
	parentID string
	creator  string
}

// resolveLastModifierFromWalk implements the blame decision over injectable loaders so
// the cap/contiguity logic is testable without a database. It walks newest->oldest from
// headCommitID. The oldest contiguous commit whose tree still has currentFSID at the
// path is where the current version was introduced; its creator is the last modifier.
// The answer is only trusted if the walk actually reaches that boundary (the commit
// where the fs_id changes, or the creating root commit) within walkCap -- otherwise the
// accumulated introducer is a partial, wrong result and "" is returned so the caller
// leaves the field empty instead of misattributing it to an unrelated old commit.
//
// The walk matches by path, so a pure rename (a move preserves the content fs_id) is
// counted as introducing the version and credits the renamer rather than the content
// author. This only affects entries with no stamped Modifier -- the rare fallback path,
// since copy/move/upload/OnlyOffice all preserve or set a Modifier. On the common path
// the renamed entry carries the original Modifier, so file/detail still credits the
// content author; the divergence is confined to pre-stamping entries (none in prod).
func resolveLastModifierFromWalk(
	headCommitID, currentFSID string,
	walkCap int,
	loadCommit func(commitID string) (commitWalkNode, bool),
	fileFSIDAt func(rootFSID string) string,
) string {
	commitID := headCommitID
	introducer := ""
	resolved := false
	for i := 0; i < walkCap && commitID != ""; i++ {
		node, ok := loadCommit(commitID)
		if !ok {
			break
		}
		if fileFSIDAt(node.rootFSID) != currentFSID {
			// The file did not yet have its current version here; the previously
			// examined (newer) commit is the introducer -- if there was one.
			resolved = introducer != ""
			break
		}
		introducer = node.creator
		if node.parentID == "" {
			resolved = true // reached the creating commit; introducer is definitive
			break
		}
		commitID = node.parentID
	}
	if !resolved {
		return ""
	}
	return introducer
}

// sesamefsLocalDomain is the synthetic email domain we attach to a bare user id when an
// account's real address is unavailable. Its presence marks a modifier value as a
// user-id reference (web/OnlyOffice) rather than a real address (Seafile desktop client).
const sesamefsLocalDomain = "@sesamefs.local"

func modifierIdentityForUser(userID string) string {
	return userID + sesamefsLocalDomain
}

// contentModifiedFileEntry stamps a real content mutation on an existing file entry.
// Operations like revert reintroduce historical content, but they are still a new edit
// performed by the current user, so the operation refreshes mtime and last-modifier
// while preserving the file's fs_id/size/mode for the chosen content version.
func contentModifiedFileEntry(src FSEntry, newName, userID string, modifiedAt int64) FSEntry {
	src.Name = newName
	src.MTime = modifiedAt
	src.Modifier = modifierIdentityForUser(userID)
	return src
}

func syntheticModifierUserID(modifier string) (string, bool) {
	if !strings.HasSuffix(modifier, sesamefsLocalDomain) {
		return "", false
	}
	uid := strings.TrimSuffix(modifier, sesamefsLocalDomain)
	if uid == "" {
		return "", false
	}
	if _, err := gocql.ParseUUID(uid); err != nil {
		return "", false
	}
	return uid, true
}

// composeLastModifierIdentity resolves the file's real last-modifier identity for
// file/detail. It NEVER falls back to the requesting user: an unresolved modifier stays
// empty, which the API surfaces as blank rather than wrong.
//
// Sources, in order of preference:
//   - entryModifier holding a real address (Seafile desktop client) -> used as the email;
//     its display name is resolved via nameForEmail so the same user shows the same name
//     whether the file was last touched from the desktop client or the web. Falls back to
//     the address local-part when the account name is unavailable.
//   - a user id, taken from the local-part of a synthetic <uid>@sesamefs.local
//     (web/OnlyOffice) or from blameUID (commit history). The id is resolved to the
//     account's real email/name via lookup; if the account is unknown, the synthetic
//     <uid>@sesamefs.local address is used so the field is still populated.
func composeLastModifierIdentity(
	entryModifier, blameUID string,
	lookup func(uid string) (email, name string),
	nameForEmail func(email string) string,
) (email, name string) {
	// A persisted real address is already the email; resolve its display name so it
	// matches the id-based paths instead of showing the local-part. We first let
	// nameForEmail prove that the address is a real account, even if it happens to end
	// with @sesamefs.local. Only an unresolved reserved <uuid>@sesamefs.local pattern is
	// treated as an embedded user id.
	uid := blameUID
	if entryModifier != "" {
		resolvedName := ""
		if nameForEmail != nil {
			resolvedName = nameForEmail(entryModifier)
		}
		if resolvedName != "" {
			return entryModifier, resolvedName
		}
		if syntheticUID, ok := syntheticModifierUserID(entryModifier); ok {
			uid = syntheticUID
		} else {
			return entryModifier, strings.Split(entryModifier, "@")[0]
		}
	}
	if uid == "" {
		return "", ""
	}

	if lookup != nil {
		if realEmail, realName := lookup(uid); realEmail != "" {
			if realName == "" {
				realName = strings.Split(realEmail, "@")[0]
			}
			return realEmail, realName
		}
	}
	return modifierIdentityForUser(uid), uid
}

// lookupUserIdentity resolves a user id to its real email and name within an org. It
// returns empty strings when the id is not a known account (e.g. a deleted user), so
// callers can fall back to a synthetic address.
func (h *FileHandler) lookupUserIdentity(orgID, userID string) (email, name string) {
	if h.db == nil || orgID == "" || userID == "" {
		return "", ""
	}
	_ = h.db.Session().Query(
		`SELECT email, name FROM users WHERE org_id = ? AND user_id = ?`,
		orgID, userID,
	).Scan(&email, &name)
	return email, name
}

// lookupUserNameByEmail resolves a real email address to its account display name within
// an org, so file/detail reports the same name regardless of whether the modifier was
// stored as an address (Seafile desktop client) or a user id (web/OnlyOffice/blame). It
// returns "" when the address is not a known account; if the account exists but has no
// explicit display name, it returns the email local-part so callers can still tell the
// address is real rather than a synthetic uid marker.
func (h *FileHandler) lookupUserNameByEmail(orgID, email string) string {
	if h.db == nil || orgID == "" || email == "" {
		return ""
	}
	// A synthetic <uuid>@sesamefs.local marker is never a real users_by_email row, so this
	// lookup would always miss. Skip it: composeLastModifierIdentity resolves these by user
	// id instead. This drops a wasted query on every web/OnlyOffice-stamped modifier -- the
	// common case -- which compounds on directory listings that resolve one modifier per
	// entry (see newPersistedModifierResolver).
	if _, ok := syntheticModifierUserID(email); ok {
		return ""
	}
	var userID gocql.UUID
	if err := h.db.Session().Query(
		`SELECT user_id FROM users_by_email WHERE email = ?`, email,
	).Scan(&userID); err != nil {
		return ""
	}
	var name string
	_ = h.db.Session().Query(
		`SELECT name FROM users WHERE org_id = ? AND user_id = ?`,
		orgID, userID,
	).Scan(&name)
	if name == "" {
		return strings.Split(email, "@")[0]
	}
	return name
}

func publicModifierIdentity(email, name string) (string, string) {
	if email == "" {
		return "", ""
	}
	if uid, ok := syntheticModifierUserID(email); ok && name == uid {
		return "", ""
	}
	return email, name
}

// resolveModifierIdentity turns the persisted modifier/blame inputs into the public
// identity the API should expose. File/detail can pass a blame uid for legacy unstamped
// entries; directory listings pass an empty blame uid and resolve only persisted data.
// Any synthetic uid@sesamefs.local fallback stays internal and is blanked before the
// response is serialized.
func (h *FileHandler) resolveModifierIdentity(orgID, entryModifier, blameUID string) (email, name string) {
	email, name = composeLastModifierIdentity(
		entryModifier,
		blameUID,
		func(uid string) (string, string) { return h.lookupUserIdentity(orgID, uid) },
		func(email string) string { return h.lookupUserNameByEmail(orgID, email) },
	)
	return publicModifierIdentity(email, name)
}

// newPersistedModifierResolver returns a request-scoped resolver that maps a persisted
// fs-entry modifier to its public (email, name), memoizing by raw modifier string so each
// distinct modifier is resolved at most once per request.
//
// PERFORMANCE NOTE (registered risk): directory listings (ListDirectory / ListDirectoryV21)
// are NOT paginated -- they resolve a modifier for every entry returned. The per-request
// cache bounds the cost to the number of DISTINCT modifiers in the directory, and each
// distinct modifier costs one users lookup (the synthetic-marker case is short-circuited
// in lookupUserNameByEmail to avoid a wasted users_by_email query). So:
//   - typical directories (few distinct uploaders): negligible, a handful of queries.
//   - pathological directory (thousands of files each by a different user): up to one
//     query per distinct user, issued serially on a hot listing path -> latency regression.
//
// If large multi-uploader directories become common, batch-resolve the distinct modifiers
// (single IN/multi-key fetch) or paginate the listing before optimizing elsewhere.
func (h *FileHandler) newPersistedModifierResolver(orgID string) func(entryModifier string) (email, name string) {
	cache := make(map[string][2]string)
	return func(entryModifier string) (email, name string) {
		if entryModifier == "" {
			return "", ""
		}
		if cached, ok := cache[entryModifier]; ok {
			return cached[0], cached[1]
		}
		email, name = h.resolveModifierIdentity(orgID, entryModifier, "")
		cache[entryModifier] = [2]string{email, name}
		return email, name
	}
}

func applyResolvedModifierToDirent(dirent *Dirent, email, name string) {
	if dirent == nil || email == "" {
		return
	}
	dirent.ModifierEmail = email
	dirent.ModifierContactEmail = email
	dirent.ModifierName = name
}

// GetFileDetail returns detailed information about a file
// Implements: GET /api2/repos/:repo_id/file/detail/?p=/path
func (h *FileHandler) GetFileDetail(c *gin.Context) {
	repoID := c.Param("repo_id")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")
	filePath := c.Query("p")

	if filePath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path is required"})
		return
	}

	filePath = normalizePath(filePath)

	// PERMISSION CHECK: User must have at least read access to the library
	if h.permMiddleware != nil {
		hasAccess, err := h.permMiddleware.HasLibraryAccessCtx(c, orgID, userID, repoID, middleware.PermissionR)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check permissions"})
			return
		}
		if !hasAccess {
			if respondIfLibraryMissing(c, h.db.Session(), orgID, repoID) {
				return
			}
			c.JSON(http.StatusForbidden, gin.H{"error": "you do not have access to this library"})
			return
		}
	}

	// ENCRYPTION CHECK: Encrypted libraries require active decrypt session
	if !h.requireDecryptSession(c, orgID, userID, repoID) {
		return
	}

	fsHelper := NewFSHelper(h.db)

	// Traverse to the file
	result, err := fsHelper.TraverseToPath(repoID, filePath)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	if result.TargetEntry == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
		return
	}

	entry := result.TargetEntry
	isDir := entry.Mode == ModeDir || entry.Mode&0170000 == 040000
	fileType := "file"
	if isDir {
		fileType = "dir"
	}

	// Get library info
	var repoName, ownerID string
	var encrypted bool
	h.db.Session().Query(`
		SELECT name, owner_id, encrypted FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, repoID).Scan(&repoName, &ownerID, &encrypted)

	// Check if file is starred
	starred := false
	starredHandler := NewStarredHandler(h.db)
	starred = starredHandler.IsFileStarred(userID, repoID, filePath)

	// Resolve the file's REAL last modifier -- never the requesting user. Prefer the
	// modifier persisted on the fs entry (set by uploads and OnlyOffice saves); otherwise
	// blame the commit history (only walked when the entry carries no modifier). If it
	// cannot be resolved, the field is left empty rather than misattributed to the caller.
	//
	// Note: files written through a public upload/update link are attributed to the link
	// creator, not the anonymous visitor -- the upload token carries the creator's user
	// id (seafhttp sets user_id = token.UserID), so that id is what gets stamped/blamed.
	// An anonymous visitor has no account identity to attribute, so this is intentional.
	//
	// The blame walk is skipped for directories: a folder carries no modifier and its
	// fs_id changes on every descendant edit, so "last modifier" is not meaningful and
	// the walk would run on every dir detail. Directories report an empty modifier.
	blameUID := ""
	if entry.Modifier == "" && !isDir {
		blameUID = h.resolveFileLastModifier(repoID, filePath, entry.ID)
	}
	modifier, modifierName := h.resolveModifierIdentity(orgID, entry.Modifier, blameUID)

	// Resolve actual permission for the user
	perm := ""
	if h.permMiddleware != nil {
		rawPerm, err := h.permMiddleware.GetLibraryPermissionRaw(orgID, userID, repoID)
		if err == nil && rawPerm != "" {
			perm = rawPerm
		}
	}
	if perm == "" {
		perm = "r" // safe default
	}

	canEdit := perm == "rw"

	c.JSON(http.StatusOK, gin.H{
		"id":                          entry.ID,
		"type":                        fileType,
		"name":                        entry.Name,
		"size":                        entry.Size,
		"mtime":                       entry.MTime,
		"permission":                  perm,
		"starred":                     starred,
		"repo_id":                     repoID,
		"repo_name":                   repoName,
		"parent_dir":                  result.ParentPath,
		"last_modifier_email":         modifier,
		"last_modifier_name":          modifierName,
		"last_modifier_contact_email": modifier,
		"can_preview":                 true,
		"can_edit":                    canEdit,
		"encoded_thumbnail_src":       "",
	})
}

// GetDirDetail returns metadata for a directory.
// GET /api/v2.1/repos/:repo_id/dir/detail/?path=/dir_name
func (h *FileHandler) GetDirDetail(c *gin.Context) {
	repoID := c.Param("repo_id")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")
	dirPath := c.Query("path")

	if dirPath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error_msg": "path is required"})
		return
	}

	dirPath = normalizePath(dirPath)

	// PERMISSION CHECK: User must have at least read access to the library
	if h.permMiddleware != nil {
		hasAccess, err := h.permMiddleware.HasLibraryAccessCtx(c, orgID, userID, repoID, middleware.PermissionR)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error_msg": "failed to check permissions"})
			return
		}
		if !hasAccess {
			if respondIfLibraryMissing(c, h.db.Session(), orgID, repoID) {
				return
			}
			c.JSON(http.StatusForbidden, gin.H{"error_msg": "you do not have access to this library"})
			return
		}
	}

	// ENCRYPTION CHECK: Encrypted libraries require active decrypt session
	if !h.requireDecryptSession(c, orgID, userID, repoID) {
		return
	}

	fsHelper := NewFSHelper(h.db)

	// Traverse to the directory
	result, err := fsHelper.TraverseToPath(repoID, dirPath)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error_msg": err.Error()})
		return
	}

	if result.TargetEntry == nil {
		c.JSON(http.StatusNotFound, gin.H{"error_msg": "directory not found"})
		return
	}

	entry := result.TargetEntry

	// Get library info
	var repoName string
	h.db.Session().Query(`
		SELECT name FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, repoID).Scan(&repoName)

	// Resolve actual permission for the user
	perm := ""
	if h.permMiddleware != nil {
		rawPerm, err := h.permMiddleware.GetLibraryPermissionRaw(orgID, userID, repoID)
		if err == nil && rawPerm != "" {
			perm = rawPerm
		}
	}
	if perm == "" {
		perm = "r" // safe default
	}

	c.JSON(http.StatusOK, gin.H{
		"repo_id":    repoID,
		"repo_name":  repoName,
		"path":       dirPath,
		"name":       entry.Name,
		"mtime":      entry.MTime,
		"permission": perm,
	})
}

// GetSmartLink generates a token-based internal permalink for a file or folder.
// Internal links are stored in the share_links table with link_type = 'internal'.
// If a link already exists for this repo+path+user, return the existing one.
// GET /api/v2.1/smart-link/?repo_id=xxx&path=/path&is_dir=true
func (h *FileHandler) GetSmartLink(c *gin.Context) {
	repoID := c.Query("repo_id")
	itemPath := c.Query("path")
	isDir := c.Query("is_dir") == "true" || c.Query("is_dir") == "1"

	if repoID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error_msg": "repo_id is required"})
		return
	}
	if itemPath == "" {
		itemPath = "/"
	}
	itemPath = normalizeSmartLinkPath(itemPath, isDir)

	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	// Check if an internal link already exists for this repo+path by scanning user's links
	orgUUID, _ := gocql.ParseUUID(orgID)
	userUUID, _ := gocql.ParseUUID(userID)

	iter := h.db.Session().Query(`
		SELECT link_token, link_type, library_id, file_path
		FROM share_links_by_creator
		WHERE org_id = ? AND created_by = ?
	`, orgUUID, userUUID).Iter()

	var existingToken, lt, lid, fp string
	for iter.Scan(&existingToken, &lt, &lid, &fp) {
		if lt == "internal" && lid == repoID && fp == itemPath {
			iter.Close()
			baseURL := httputil.GetBrowserURL(c, h.serverURL)
			c.JSON(http.StatusOK, gin.H{
				"smart_link": fmt.Sprintf("%s/smart-link/%s", baseURL, existingToken),
			})
			return
		}
	}
	iter.Close()

	// No existing link — create a new one
	token, err := generateSecureShareToken(16)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error_msg": "failed to generate token"})
		return
	}

	now := time.Now()
	sh := &ShareLinkHandler{db: h.db, serverURL: h.serverURL}
	if err := sh.insertShareLink(
		token, "internal", orgID, repoID, itemPath, userID,
		"", "", nil, false, now,
		0, 0, 0,
	); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error_msg": "failed to create internal link"})
		return
	}

	baseURL := httputil.GetBrowserURL(c, h.serverURL)
	c.JSON(http.StatusOK, gin.H{
		"smart_link": fmt.Sprintf("%s/smart-link/%s", baseURL, token),
	})
}

// ResolveSmartLink resolves an internal (smart) link token and redirects to the frontend file/folder view.
// Internal links always require authentication — the user must belong to the same org as the link.
// GET /api/v2.1/smart-link/:token
func (h *FileHandler) ResolveSmartLink(c *gin.Context) {
	token := c.Param("token")
	userOrgID := c.GetString("org_id")

	var linkType, orgID, libraryID, filePath string
	var active bool
	err := h.db.Session().Query(`
		SELECT link_type, org_id, library_id, file_path, active
		FROM share_links WHERE link_token = ?
	`, token).Scan(&linkType, &orgID, &libraryID, &filePath, &active)
	if err != nil || linkType != "internal" {
		c.JSON(http.StatusNotFound, gin.H{"error_msg": "link not found"})
		return
	}
	if orgID != userOrgID {
		c.JSON(http.StatusForbidden, gin.H{"error_msg": "access denied"})
		return
	}
	if !active {
		c.JSON(http.StatusGone, gin.H{"error_msg": "link is no longer active"})
		return
	}

	// Increment view_count in background
	go func() {
		now := time.Now()
		if err := incrementShareLinkCounterDualWrite(h.db, token, "view_count", now); err != nil {
			log.Printf("[ViewInternalLink] failed to update view_count for token %s: %v", token, err)
		}
	}()

	// Determine redirect URL based on path
	baseURL := httputil.GetBrowserURL(c, h.serverURL)
	isDir, dirErr := resolveLibraryPathIsDir(h.db, libraryID, filePath)
	if dirErr != nil {
		isDir = filePath == "/" || strings.HasSuffix(filePath, "/")
	}
	var repoName string
	if queryErr := h.db.Session().Query(`SELECT name FROM libraries_by_id WHERE library_id = ?`, libraryID).Scan(&repoName); queryErr != nil {
		repoName = libraryID
	}
	redirectURL := buildSmartLinkRedirectURL(baseURL, libraryID, repoName, filePath, h.config.FileView.PreviewExtensions, isDir)

	c.Redirect(http.StatusFound, redirectURL)
}

func buildSmartLinkRedirectURL(baseURL, libraryID, repoName, filePath string, previewExtensions []string, isDir bool) string {
	if isDir {
		normalizedPath := normalizePath(filePath)
		repoSegment := url.PathEscape(repoName)
		if repoSegment == "" {
			repoSegment = libraryID
		}
		if normalizedPath == "/" {
			return fmt.Sprintf("%s/library/%s/%s/", baseURL, libraryID, repoSegment)
		}
		return fmt.Sprintf("%s/library/%s/%s%s", baseURL, libraryID, repoSegment, normalizedPath)
	}

	filename := filepath.Base(filePath)
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(filename), "."))
	if isInlinePreviewable(ext, previewExtensions) {
		return baseURL + buildFrontendFilePreviewURL(libraryID, filePath, "")
	}

	return fmt.Sprintf("%s/lib/%s/file%s", baseURL, libraryID, filePath)
}

func normalizeSmartLinkPath(p string, isDir bool) string {
	normalized := normalizePath(p)
	if isDir && normalized != "/" {
		return normalized + "/"
	}
	return normalized
}

func resolveLibraryPathIsDir(database *db.DB, libraryID, filePath string) (bool, error) {
	if database == nil {
		return false, fmt.Errorf("database not available")
	}

	normalizedPath := normalizePath(filePath)
	if normalizedPath == "/" {
		return true, nil
	}

	fsHelper := NewFSHelper(database)
	rootFSID, _, err := fsHelper.GetRootFSID(libraryID)
	if err != nil {
		return false, err
	}

	result, err := fsHelper.TraverseToPathFromRoot(libraryID, rootFSID, normalizedPath)
	if err != nil {
		return false, err
	}
	if result == nil || result.TargetEntry == nil {
		return false, fmt.Errorf("path not found")
	}

	return result.TargetEntry.Mode == ModeDir || result.TargetEntry.Mode&0170000 == 040000, nil
}

// DeleteFile deletes a file
func (h *FileHandler) DeleteFile(c *gin.Context) {
	repoID := c.Param("repo_id")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")
	filePath := c.Query("p")

	if filePath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path is required"})
		return
	}

	filePath = normalizePath(filePath)
	if filePath == "/" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot delete root"})
		return
	}

	// ========================================================================
	// PERMISSION CHECK: Readonly and guest users cannot delete files
	// ========================================================================
	if !h.requireWritePermission(c, orgID, userID) {
		return
	}

	// ========================================================================
	// PERMISSION CHECK: User must have write permission to delete files
	// ========================================================================
	if h.permMiddleware != nil {
		hasWrite, err := h.permMiddleware.HasLibraryAccessCtx(c, orgID, userID, repoID, middleware.PermissionRW)
		if err != nil {
			log.Printf("[DeleteFile] Failed to check permissions: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check permissions"})
			return
		}

		if !hasWrite {
			log.Printf("[DeleteFile] Permission denied: user %q does not have write permission to library %q", userID, repoID)
			c.JSON(http.StatusForbidden, gin.H{"error": "you do not have write permission to this library"})
			return
		}
	}

	// CUSTOM PERMISSION CHECK: delete flag
	if h.permMiddleware != nil && !h.permMiddleware.RequirePermFlag(c, "delete") {
		c.JSON(http.StatusForbidden, gin.H{"error": "delete is not allowed by your permission"})
		return
	}

	// ========================================================================
	// ENCRYPTION CHECK: Encrypted libraries require active decrypt session. This runs
	// BEFORE lock enforcement so an encrypted library stays completely inaccessible
	// without an active decrypt session — otherwise a 403 "locked" with lock_owner would
	// leak lock metadata of an encrypted library before the decrypt gate.
	// ========================================================================
	if !h.requireDecryptSession(c, orgID, userID, repoID) {
		return
	}

	// LOCK ENFORCEMENT: a file locked by another user cannot be deleted — and a
	// folder cannot be deleted if it contains a file locked by another user.
	if !h.requireSubtreeNotLockedByOther(c, repoID, filePath, userID) {
		return
	}

	fsHelper := NewFSHelper(h.db)
	fileName := path.Base(filePath)
	errFileNotFound := errors.New("file not found")
	errPathIsDirectory := errors.New("path is a directory")
	var result *PathTraverseResult
	var newCommitID string

	err := retryLibraryHeadMutation("DeleteFile", func() error {
		currentResult, snapshot, err := fsHelper.TraverseToPathAtHead(repoID, filePath)
		if err != nil {
			return err
		}
		if currentResult.TargetEntry == nil {
			return errFileNotFound
		}
		if currentResult.TargetEntry.Mode == ModeDir || currentResult.TargetEntry.Mode&0170000 == 040000 {
			return errPathIsDirectory
		}

		newEntries := RemoveEntryFromList(currentResult.Entries, fileName)
		newParentFSID, err := fsHelper.CreateDirectoryFSObject(repoID, newEntries)
		if err != nil {
			return fmt.Errorf("failed to update directory: %w", err)
		}

		newRootFSID, err := fsHelper.RebuildPathToRoot(repoID, currentResult, newParentFSID)
		if err != nil {
			return fmt.Errorf("failed to rebuild path: %w", err)
		}

		description := fmt.Sprintf("Deleted \"%s\"", fileName)
		commitID, err := fsHelper.CreateCommit(repoID, userID, newRootFSID, snapshot.HeadCommitID, description)
		if err != nil {
			return fmt.Errorf("failed to create commit: %w", err)
		}

		if err := fsHelper.UpdateLibraryHeadFromSnapshot(snapshot, repoID, commitID, snapshot.HeadCommitID); err != nil {
			return err
		}

		result = currentResult
		newCommitID = commitID
		return nil
	})
	if err != nil {
		switch {
		case errors.Is(err, errFileNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
		case errors.Is(err, errPathIsDirectory):
			c.JSON(http.StatusBadRequest, gin.H{"error": "path is a directory, use DELETE /dir/ instead"})
		case errors.Is(err, ErrLibraryHeadConflict):
			c.JSON(http.StatusConflict, gin.H{"error": "library was modified concurrently; retry the delete"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete file"})
		}
		return
	}

	// Block references are released when GC sweeps the now-unreachable fs_object
	// (the deleted file stays in fs_objects until version retention expires), so
	// there is no inline ref decrement here.

	// Clean up file tags for the deleted file (async, non-blocking)
	go h.cleanupFileTagsForPath(repoID, filePath)

	// Drop the operator's own locks under the just-deleted path so they don't linger
	// as orphans (any other user's lock here would have blocked the delete upstream).
	clearLocksAfterDelete(h, repoID, filePath, userID)

	// Decrement storage counters for the deleted file — fire-and-forget.
	if fileSize := result.TargetEntry.Size; fileSize > 0 {
		traffic.DecrementStorageCounters(h.db, orgID, userID, repoID, fileSize, 1)
	}

	c.JSON(http.StatusOK, gin.H{
		"success":   true,
		"commit_id": newCommitID,
	})
}

// MoveFileRequest represents the request for moving a file
type MoveFileRequest struct {
	SrcRepoID      string `json:"src_repo_id" form:"src_repo_id"`
	SrcPath        string `json:"src_path" form:"src_path"`
	DstRepoID      string `json:"dst_repo_id" form:"dst_repo_id"`
	DstDir         string `json:"dst_dir" form:"dst_dir"`                 // Destination directory
	ConflictPolicy string `json:"conflict_policy" form:"conflict_policy"` // "replace", "autorename", "skip", or empty
	// Legacy format fields
	SrcDir   string      `json:"src_dir" form:"src_dir"`   // Source directory (legacy)
	Filename interface{} `json:"filename" form:"filename"` // Can be string or []string for batch operations
}

// MoveFile moves a file to a new location
// Supports both same-repo and cross-repo moves, single and batch operations
func (h *FileHandler) MoveFile(c *gin.Context) {
	repoID := c.Param("repo_id")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	// ========================================================================
	// PERMISSION CHECK: Readonly and guest users cannot move files
	// ========================================================================
	if !h.requireWritePermission(c, orgID, userID) {
		return
	}

	// ========================================================================
	// PERMISSION CHECK: User must have write permission to move files
	// ========================================================================
	if h.permMiddleware != nil {
		hasWrite, err := h.permMiddleware.HasLibraryAccessCtx(c, orgID, userID, repoID, middleware.PermissionRW)
		if err != nil {
			log.Printf("[MoveFile] Failed to check permissions: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check permissions"})
			return
		}

		if !hasWrite {
			log.Printf("[MoveFile] Permission denied: user %q does not have write permission to library %q", userID, repoID)
			c.JSON(http.StatusForbidden, gin.H{"error": "you do not have write permission to this library"})
			return
		}
	}

	// CUSTOM PERMISSION CHECK: modify flag
	if h.permMiddleware != nil && !h.permMiddleware.RequirePermFlag(c, "modify") {
		c.JSON(http.StatusForbidden, gin.H{"error": "modify is not allowed by your permission"})
		return
	}

	// ========================================================================
	// ENCRYPTION CHECK: Encrypted libraries require active decrypt session
	// ========================================================================
	if !h.requireDecryptSession(c, orgID, userID, repoID) {
		return
	}

	var req MoveFileRequest
	if err := c.ShouldBind(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Extract filenames from interface{} (can be string or []interface{} for batch)
	var filenames []string
	if req.Filename != nil {
		switch v := req.Filename.(type) {
		case string:
			filenames = []string{v}
		case []interface{}:
			for _, item := range v {
				if str, ok := item.(string); ok {
					filenames = append(filenames, str)
				}
			}
		case []string:
			filenames = v
		}
	}

	// Handle different request formats
	srcRepoID := req.SrcRepoID
	if srcRepoID == "" {
		srcRepoID = repoID
	}
	dstRepoID := req.DstRepoID
	if dstRepoID == "" {
		dstRepoID = repoID
	}

	// Build source and destination paths
	var srcPaths []string
	if req.SrcPath != "" {
		// Single file move with full path
		srcPaths = []string{req.SrcPath}
	} else if req.SrcDir != "" && len(filenames) > 0 {
		// Batch move or legacy single file format
		for _, filename := range filenames {
			srcPaths = append(srcPaths, path.Join(req.SrcDir, filename))
		}
	}

	dstDir := req.DstDir

	if len(srcPaths) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "source path is required"})
		return
	}
	if dstDir == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "destination directory is required"})
		return
	}

	for i := range srcPaths {
		srcPaths[i] = normalizePath(srcPaths[i])
	}
	dstDir = normalizePath(dstDir)

	// Cross-repo move not yet implemented
	if srcRepoID != dstRepoID {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "cross-repo move not yet implemented"})
		return
	}

	for _, srcPath := range srcPaths {
		// Subtree-aware: also blocks moving a folder that contains a file locked
		// by another user. The destination "replace" lock is enforced downstream
		// in processSingleItem.
		if !h.requireSubtreeNotLockedByOther(c, srcRepoID, srcPath, userID) {
			return
		}
	}

	// For batch operations (multiple files), handle differently
	if len(srcPaths) > 1 {
		h.moveBatchFiles(c, srcPaths, srcRepoID, dstRepoID, dstDir, orgID, userID)
		return
	}

	// Single file move continues with existing logic
	srcPath := srcPaths[0]

	// Check if database is available
	if h.db == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database not available"})
		return
	}

	fsHelper := NewFSHelper(h.db)
	fileName := path.Base(srcPath)
	sourceResult, err := fsHelper.TraverseToPath(repoID, srcPath)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "source not found: " + err.Error()})
		return
	}
	if sourceResult == nil || sourceResult.TargetEntry == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "source file not found"})
		return
	}
	sourceEntryID := sourceResult.TargetEntry.ID

	batchHandler := NewBatchOperationHandler(h.db, h.config)
	if err := batchHandler.processSingleItem(orgID, userID, srcRepoID, dstRepoID, srcPath, dstDir, "move", fsHelper, req.ConflictPolicy); err != nil {
		writeMoveFileError(c, err, srcPath)
		return
	}

	if snapshot, err := fsHelper.GetLibraryHeadSnapshot(repoID); err == nil {
		if dstResult, err := fsHelper.TraverseToPathFromSnapshot(repoID, snapshot, dstDir); err == nil {
			var dstEntries []FSEntry
			if dstDir == "/" {
				dstEntries = dstResult.Entries
			} else if dstResult.TargetFSID != "" {
				dstEntries, err = fsHelper.GetDirectoryEntries(repoID, dstResult.TargetFSID)
			}
			if err == nil {
				for _, entry := range dstEntries {
					if entry.ID == sourceEntryID {
						fileName = entry.Name
						break
					}
				}
			}
		}
	}

	// Return Seafile-compatible response
	// Seafile returns HTTP 301 for moves but we use 200 for API compatibility
	c.JSON(http.StatusOK, gin.H{
		"repo_id":    dstRepoID,
		"parent_dir": dstDir,
		"obj_name":   fileName,
	})
}

func writeMoveFileError(c *gin.Context, err error, srcPath string) {
	switch {
	case errors.Is(err, ErrLibraryHeadConflict):
		c.JSON(http.StatusConflict, gin.H{"error": "library was modified concurrently; retry the move"})
	case errors.Is(err, ErrBatchSourceNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "source file not found"})
	case errors.Is(err, ErrBatchDestinationNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "destination directory not found"})
	case errors.Is(err, ErrBatchItemLocked):
		c.JSON(http.StatusForbidden, gin.H{"error": "file is locked by another user"})
	case errors.Is(err, ErrBatchLockStatusUnavailable):
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "failed to verify file lock"})
	case errors.Is(err, ErrStorageQuotaExceeded):
		c.JSON(http.StatusForbidden, gin.H{"error": "storage quota exceeded"})
	default:
		var conflictErr *ConflictError
		if errors.As(err, &conflictErr) {
			c.JSON(http.StatusConflict, gin.H{
				"error":             "conflict",
				"conflicting_items": []string{path.Base(srcPath)},
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to move file"})
	}
}

// moveBatchFiles handles moving multiple files in a single operation
func (h *FileHandler) moveBatchFiles(c *gin.Context, srcPaths []string, srcRepoID, dstRepoID, dstDir, orgID, userID string) {
	// Cross-repo batch move not yet implemented
	if srcRepoID != dstRepoID {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "cross-repo batch move not yet implemented"})
		return
	}

	// Same-repo batch move via this legacy endpoint is not implemented: it used to
	// report {"success":true,"moved":N} without touching the FS tree at all
	// (ISSUE-BATCH-MOVE-FALSE-SUCCESS-01). Real batch moves go through
	// seafileAPI.moveDirWithPolicy -> POST /api/v2.1/repos/sync-batch-move-item/
	// (SyncBatchMove/AsyncBatchMove -> processSingleItem), which is
	// integration-tested and correct.
	c.JSON(http.StatusNotImplemented, gin.H{"error": "batch move via this legacy endpoint is not implemented; use POST /api/v2.1/repos/sync-batch-move-item/"})
}

// CopyFileRequest represents the request for copying a file
type CopyFileRequest struct {
	SrcRepoID      string `json:"src_repo_id" form:"src_repo_id"`
	SrcPath        string `json:"src_path" form:"src_path"`
	DstRepoID      string `json:"dst_repo_id" form:"dst_repo_id"`
	DstDir         string `json:"dst_dir" form:"dst_dir"`                 // Destination directory
	ConflictPolicy string `json:"conflict_policy" form:"conflict_policy"` // "replace", "autorename", "skip", or empty
	// Legacy format fields
	SrcDir   string      `json:"src_dir" form:"src_dir"`   // Source directory (legacy)
	Filename interface{} `json:"filename" form:"filename"` // Can be string or []string for batch operations
}

type copyItemResult struct {
	itemName   string
	commitID   string
	deltaBytes int64
	deltaFiles int64
	skipped    bool
}

func copyItemWithinRepoWithRetry(label string, fsHelper *FSHelper, orgID, userID, repoID, srcPath, dstDir, conflictPolicy string) (*copyItemResult, error) {
	result := &copyItemResult{}
	srcPath = normalizePath(srcPath)
	dstDir = normalizePath(dstDir)
	originalItemName := path.Base(srcPath)

	// LOCK ENFORCEMENT: a "replace" copy overwrites the destination, so that target
	// must not be locked by another user. autorename/skip never clobber it, so they
	// are exempt. The copy path does not funnel through processSingleItem, hence the
	// dedicated check here.
	if conflictPolicy == "replace" {
		dstPath := path.Join(dstDir, originalItemName)
		blocked, _, lerr := checkCopyReplaceDestLocked(fsHelper, repoID, dstPath, userID)
		if lerr != nil {
			return nil, fmt.Errorf("%w: %v", ErrBatchLockStatusUnavailable, lerr)
		}
		if blocked {
			return nil, fmt.Errorf("%w: destination %s", ErrBatchItemLocked, dstPath)
		}
	}

	err := retryLibraryHeadMutation(label, func() error {
		snapshot, err := fsHelper.GetLibraryHeadSnapshot(repoID)
		if err != nil {
			return fmt.Errorf("%w: %v", errLibraryNotFound, err)
		}

		srcResult, err := fsHelper.TraverseToPathFromSnapshot(repoID, snapshot, srcPath)
		if err != nil || srcResult.TargetEntry == nil {
			return ErrBatchSourceNotFound
		}

		dstResult, err := fsHelper.TraverseToPathFromSnapshot(repoID, snapshot, dstDir)
		if err != nil {
			return ErrBatchDestinationNotFound
		}

		var dstDirEntries []FSEntry
		if dstDir == "/" {
			dstDirEntries = dstResult.Entries
		} else {
			if dstResult.TargetFSID == "" {
				return ErrBatchDestinationNotFound
			}
			dstDirEntries, err = fsHelper.GetDirectoryEntries(repoID, dstResult.TargetFSID)
			if err != nil {
				return fmt.Errorf("failed to read destination directory: %w", err)
			}
		}

		currentItemName := originalItemName
		var replacedEntry *FSEntry
		if existing := FindEntryInList(dstDirEntries, currentItemName); existing != nil {
			switch conflictPolicy {
			case "replace":
				ent := *existing
				replacedEntry = &ent
				dstDirEntries = RemoveEntryFromList(dstDirEntries, currentItemName)
			case "autorename":
				currentItemName = GenerateUniqueName(dstDirEntries, currentItemName)
			case "skip":
				result.itemName = currentItemName
				result.deltaBytes = 0
				result.deltaFiles = 0
				result.skipped = true
				return nil
			default:
				return &ConflictError{ItemName: currentItemName}
			}
		}

		// Same-repo copy keeps the same content fs_id, so the copied dirent should
		// preserve content mtime/modifier exactly like the batch-operation path.
		copiedEntry := copiedFileEntry(*srcResult.TargetEntry, srcResult.TargetEntry.ID, currentItemName)

		deltaBytes, deltaFiles, err := fsEntryDelta(fsHelper, repoID, copiedEntry, replacedEntry)
		if err != nil {
			return fmt.Errorf("failed to compute storage delta: %w", err)
		}
		if deltaBytes > 0 {
			if !storageQuotaAllowsDelta(orgID, userID, deltaBytes) {
				return ErrStorageQuotaExceeded
			}
		}

		dstNewEntries := AddEntryToList(dstDirEntries, copiedEntry)
		newDstFSID, err := fsHelper.CreateDirectoryFSObject(repoID, dstNewEntries)
		if err != nil {
			return fmt.Errorf("failed to update destination directory: %w", err)
		}

		var newRootFSID string
		if dstDir == "/" {
			newRootFSID = newDstFSID
		} else {
			dstDirName := path.Base(dstDir)
			parentEntries := make([]FSEntry, len(dstResult.Entries))
			copy(parentEntries, dstResult.Entries)
			for i := range parentEntries {
				if parentEntries[i].Name == dstDirName {
					parentEntries[i].ID = newDstFSID
					break
				}
			}
			newParentFSID, err := fsHelper.CreateDirectoryFSObject(repoID, parentEntries)
			if err != nil {
				return fmt.Errorf("failed to update parent directory: %w", err)
			}
			newRootFSID, err = fsHelper.RebuildPathToRoot(repoID, dstResult, newParentFSID)
			if err != nil {
				return fmt.Errorf("failed to rebuild path: %w", err)
			}
		}

		description := fmt.Sprintf("Copied \"%s\" to \"%s\"", currentItemName, dstDir)
		newCommitID, err := fsHelper.CreateCommit(repoID, userID, newRootFSID, snapshot.HeadCommitID, description)
		if err != nil {
			return fmt.Errorf("failed to create commit: %w", err)
		}

		// Block references need no adjustment on copy: a same-repo copy shares the
		// source's content-addressed fs_id (its reference already exists), and the
		// replaced entry's old fs_object stays referenced from older commits until
		// GC sweeps it. The new directory fs_objects carry no blocks.
		if err := fsHelper.UpdateLibraryHeadFromSnapshot(snapshot, repoID, newCommitID, snapshot.HeadCommitID); err != nil {
			return err
		}

		result.itemName = currentItemName
		result.commitID = newCommitID
		result.deltaBytes = deltaBytes
		result.deltaFiles = deltaFiles
		result.skipped = false
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// CopyFile copies a file to a new location
// Supports both same-repo and cross-repo copies, single and batch operations
func (h *FileHandler) CopyFile(c *gin.Context) {
	repoID := c.Param("repo_id")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	// ========================================================================
	// PERMISSION CHECK: Readonly and guest users cannot copy files
	// ========================================================================
	if !h.requireWritePermission(c, orgID, userID) {
		return
	}

	// ========================================================================
	// PERMISSION CHECK: User must have write permission to copy files
	// ========================================================================
	if h.permMiddleware != nil {
		hasWrite, err := h.permMiddleware.HasLibraryAccessCtx(c, orgID, userID, repoID, middleware.PermissionRW)
		if err != nil {
			log.Printf("[CopyFile] Failed to check permissions: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check permissions"})
			return
		}

		if !hasWrite {
			log.Printf("[CopyFile] Permission denied: user %q does not have write permission to library %q", userID, repoID)
			c.JSON(http.StatusForbidden, gin.H{"error": "you do not have write permission to this library"})
			return
		}
	}

	// CUSTOM PERMISSION CHECK: copy flag
	if h.permMiddleware != nil && !h.permMiddleware.RequirePermFlag(c, "copy") {
		c.JSON(http.StatusForbidden, gin.H{"error": "copy is not allowed by your permission"})
		return
	}

	// ========================================================================
	// ENCRYPTION CHECK: Encrypted libraries require active decrypt session
	// ========================================================================
	if !h.requireDecryptSession(c, orgID, userID, repoID) {
		return
	}

	var req CopyFileRequest
	if err := c.ShouldBind(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Extract filenames from interface{} (can be string or []interface{} for batch)
	var filenames []string
	if req.Filename != nil {
		switch v := req.Filename.(type) {
		case string:
			filenames = []string{v}
		case []interface{}:
			for _, item := range v {
				if str, ok := item.(string); ok {
					filenames = append(filenames, str)
				}
			}
		case []string:
			filenames = v
		}
	}

	// Handle different request formats
	srcRepoID := req.SrcRepoID
	if srcRepoID == "" {
		srcRepoID = repoID
	}
	dstRepoID := req.DstRepoID
	if dstRepoID == "" {
		dstRepoID = repoID
	}

	// Build source and destination paths
	var srcPaths []string
	if req.SrcPath != "" {
		// Single file copy with full path
		srcPaths = []string{req.SrcPath}
	} else if req.SrcDir != "" && len(filenames) > 0 {
		// Batch copy or legacy single file format
		for _, filename := range filenames {
			srcPaths = append(srcPaths, path.Join(req.SrcDir, filename))
		}
	}

	dstDir := req.DstDir

	if len(srcPaths) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "source path is required"})
		return
	}
	if dstDir == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "destination directory is required"})
		return
	}

	// For batch operations (multiple files), handle differently
	if len(srcPaths) > 1 {
		h.copyBatchFiles(c, srcPaths, srcRepoID, dstRepoID, dstDir, orgID, userID, req.ConflictPolicy)
		return
	}

	// Single file copy continues with existing logic
	srcPath := srcPaths[0]

	srcPath = normalizePath(srcPath)
	dstDir = normalizePath(dstDir)

	// Cross-repo copy not yet implemented
	if srcRepoID != dstRepoID {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "cross-repo copy not yet implemented"})
		return
	}

	// Check if database is available
	if h.db == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database not available"})
		return
	}

	fsHelper := NewFSHelper(h.db)

	copyResult, err := copyItemWithinRepoWithRetry("CopyFile", fsHelper, orgID, userID, repoID, srcPath, dstDir, req.ConflictPolicy)
	if err != nil {
		switch {
		case errors.Is(err, errLibraryNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
		case errors.Is(err, ErrBatchSourceNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "source file not found"})
		case errors.Is(err, ErrBatchDestinationNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "destination directory not found"})
		case errors.Is(err, ErrBatchItemLocked):
			c.JSON(http.StatusForbidden, gin.H{"error": "file is locked by another user"})
		case errors.Is(err, ErrBatchLockStatusUnavailable):
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "failed to verify file lock"})
		case errors.Is(err, ErrStorageQuotaExceeded):
			c.JSON(http.StatusForbidden, gin.H{"error": "storage quota exceeded"})
		case errors.Is(err, ErrLibraryHeadConflict):
			c.JSON(http.StatusConflict, gin.H{"error": "library was modified concurrently; retry the copy"})
		default:
			var conflictErr *ConflictError
			if errors.As(err, &conflictErr) {
				c.JSON(http.StatusConflict, gin.H{
					"error":             "conflict",
					"conflicting_items": []string{conflictErr.ItemName},
				})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to copy file"})
		}
		return
	}

	if !copyResult.skipped {
		if !applyStorageCounterDelta(c, h.db, orgID, userID, repoID, copyResult.deltaBytes, copyResult.deltaFiles) {
			return
		}
	}

	// Return Seafile-compatible response
	c.JSON(http.StatusOK, gin.H{
		"repo_id":    dstRepoID,
		"parent_dir": dstDir,
		"obj_name":   copyResult.itemName,
	})
}

// GetDownloadLink returns a URL for downloading a file (Seafile compatible)
// The URL points to the server's seafhttp endpoint, not directly to S3
func (h *FileHandler) GetDownloadLink(c *gin.Context) {
	repoID := c.Param("repo_id")
	filePath := c.Query("p")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	if filePath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path is required"})
		return
	}

	// ENCRYPTION CHECK: Encrypted libraries require active decrypt session
	if !h.requireDecryptSession(c, orgID, userID, repoID) {
		return
	}

	// CUSTOM PERMISSION CHECK: download flag
	if h.permMiddleware != nil && !h.permMiddleware.RequirePermFlag(c, "download") {
		c.JSON(http.StatusForbidden, gin.H{"error": "download is not allowed by your permission"})
		return
	}

	// Check if token creator is available
	if h.tokenCreator == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "service not available"})
		return
	}

	// Normalize path
	filePath = normalizePath(filePath)

	// Create a download token
	token, err := h.tokenCreator.CreateDownloadToken(orgID, repoID, filePath, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate download link"})
		return
	}

	// Get the filename from the path
	filename := filepath.Base(filePath)

	// Build the Seafile-compatible download URL using the browser-facing URL
	// Format: {server}/seafhttp/files/{token}/{filename}
	downloadURL := fmt.Sprintf("%s/seafhttp/files/%s/%s", httputil.GetBrowserURL(c, h.serverURL), token, filename)

	// Return as JSON-encoded string (with double quotes).
	// Seafile clients strip the first and last character (the quotes) to extract the URL.
	c.JSON(http.StatusOK, downloadURL)
}

// GetUploadLink returns a URL for uploading a file (Seafile compatible)
// The URL points to the server's seafhttp endpoint, not directly to S3
func (h *FileHandler) GetUploadLink(c *gin.Context) {
	if h.tokenCreator == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "service not available"})
		return
	}
	h.getUploadLinkWithCreator(c, h.tokenCreator.CreateUploadToken)
}

// GetUpdateLink returns an upload URL whose token overwrites the target path by default.
func (h *FileHandler) GetUpdateLink(c *gin.Context) {
	if h.tokenCreator == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "service not available"})
		return
	}
	h.getUploadLinkWithCreator(c, h.tokenCreator.CreateUpdateToken)
}

func (h *FileHandler) getUploadLinkWithCreator(c *gin.Context, createToken func(orgID, repoID, path, userID string) (string, error)) {
	repoID := c.Param("repo_id")
	parentDir := c.DefaultQuery("p", "/")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	// ENCRYPTION CHECK: Encrypted libraries require active decrypt session
	if !h.requireDecryptSession(c, orgID, userID, repoID) {
		return
	}

	// CUSTOM PERMISSION CHECK: upload flag
	if h.permMiddleware != nil && !h.permMiddleware.RequirePermFlag(c, "upload") {
		c.JSON(http.StatusForbidden, gin.H{"error": "upload is not allowed by your permission"})
		return
	}

	// Normalize path
	parentDir = normalizePath(parentDir)

	// Create an upload token
	token, err := createToken(orgID, repoID, parentDir, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate upload link"})
		return
	}

	// Build the Seafile-compatible upload URL using the browser-facing URL
	// Format: {server}/seafhttp/upload-api/{token}
	uploadURL := fmt.Sprintf("%s/seafhttp/upload-api/%s", httputil.GetBrowserURL(c, h.serverURL), token)

	// Return as JSON-encoded string (with double quotes).
	// Seafile clients strip the first and last character (the quotes) to extract the URL.
	// Without quotes, the client strips 'h' from 'https' → "ttps://" → "Protocol ttps is unknown".
	c.JSON(http.StatusOK, uploadURL)
}

// UploadFile handles direct file uploads (for smaller files)
func (h *FileHandler) UploadFile(c *gin.Context) {
	repoID := c.Param("repo_id")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	parentDir := c.DefaultPostForm("parent_dir", "/")
	if parentDir == "" {
		parentDir = "/"
	}
	parentDir = normalizePath(parentDir)

	replace := c.DefaultPostForm("replace", "0") == "1"

	// CUSTOM PERMISSION CHECK: upload flag
	if h.permMiddleware != nil && !h.permMiddleware.RequirePermFlag(c, "upload") {
		c.JSON(http.StatusForbidden, gin.H{"error": "upload is not allowed by your permission"})
		return
	}

	// ENCRYPTION CHECK: Encrypted libraries require active decrypt session
	if !h.requireDecryptSession(c, orgID, userID, repoID) {
		return
	}

	if h.db == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database not available"})
		return
	}

	// Traffic quota pre-check — before reading the body to fail fast. Storage
	// quota is checked below using the visible tree delta for this path.
	uploadTrafficStatus := traffic.QuotaStatus{Allowed: true}
	if checker := traffic.GetChecker(); checker != nil {
		contentLength := c.Request.ContentLength
		if contentLength < 0 {
			contentLength = 0
		}
		uploadTrafficStatus, _ = traffic.CheckTrafficQuotaWithChecker(checker, orgID, userID, "upload", contentLength)
		if !uploadTrafficStatus.Allowed {
			c.JSON(http.StatusForbidden, traffic.TrafficQuotaExceededResponse(uploadTrafficStatus, "traffic quota exceeded", true))
			return
		}
		if warning, ok := traffic.TrafficQuotaWarningHeader(uploadTrafficStatus); ok {
			c.Header("X-Quota-Warning", warning)
		}
	}

	// Read file from multipart form
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file is required"})
		return
	}
	defer file.Close()

	content, err := httputil.ReadAllWithLimit(file, header.Size, httputil.SingleShotUploadReadLimitBytes)
	if err != nil {
		if errors.Is(err, httputil.ErrReadLimitExceeded) {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "single-shot upload exceeds 1 GiB limit; use chunked upload"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read file"})
		return
	}
	fileSize := int64(len(content))
	filename := header.Filename

	// LOCK ENFORCEMENT: overwriting a file locked by another user is forbidden.
	// Only the overwrite path can clobber the locked file; an autorename upload
	// creates a new name and leaves the locked file untouched.
	if replace && !h.requireFileNotLockedByOther(c, repoID, parentDir+"/"+filename, userID) {
		return
	}

	fsHelper := NewFSHelper(h.db)
	uploadOperationID := uuid.NewString()

	// Get the current visible storage delta before storing the block so quota is
	// enforced against the live directory state (replace vs autorename).
	storageDeltaBytes, storageDeltaFiles, err := currentUploadStorageDelta(fsHelper, repoID, parentDir, filename, fileSize, replace)
	if err != nil {
		if errors.Is(err, errLibraryNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
		} else if errors.Is(err, errParentDirectoryNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "parent directory not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to inspect upload target"})
		}
		return
	}

	if storageDeltaBytes > 0 {
		if checker := traffic.GetChecker(); checker != nil {
			if st, _ := checker.CheckStorageQuota(orgID, userID, storageDeltaBytes); !st.Allowed {
				c.JSON(http.StatusForbidden, gin.H{"error": "storage quota exceeded"})
				return
			}
		}
	}

	// SHA-1 of plaintext → fileID (Seafile protocol: block ID used in fs_object's block_ids)
	sha1Hash := sha1.Sum(content)
	fileID := hex.EncodeToString(sha1Hash[:])

	// Check encryption and encrypt content if needed. Failing open here would
	// persist PLAINTEXT into a library the user believes is encrypted, and the
	// stored object gives no way to detect it afterwards.
	storedContent := content
	encrypted, err := libraryIsEncrypted(h.db, orgID, repoID)
	if err != nil {
		respondEncryptionProbeUnavailable(c)
		return
	}
	if encrypted {
		fileKey, fileIV := GetDecryptSessions().GetFileKeyAndIV(userID, repoID)
		if fileKey == nil {
			c.JSON(http.StatusForbidden, gin.H{"error": "library is encrypted and not unlocked"})
			return
		}
		encryptedContent, err := crypto.EncryptBlockSeafile(content, fileKey, fileIV)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to encrypt content"})
			return
		}
		storedContent = encryptedContent
	}

	// SHA-256 of stored content (used as key in block store and blocks table)
	sha256Hash := sha256.Sum256(storedContent)
	sha256ID := hex.EncodeToString(sha256Hash[:])

	// Store block
	blockStore, storageClass, err := h.resolveLibraryBlockStore(c, orgID, repoID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "storage not available"})
		return
	}
	var materializationTarget BlockMaterializationTarget
	if err := RetryUploadedBlockMaterializationPhasedContext(c.Request.Context(), "UploadFile", sha256ID, func(phase BlockMaterializationPhase) error {
		materializationTarget = BlockMaterializationTarget{}
		probe, probeErr := probeUploadedBlockReuseFn(h.db, orgID, sha256ID)
		if probeErr != nil {
			return fmt.Errorf("probe block reuse for %s: %w", sha256ID, probeErr)
		}
		probe, probeErr = prepareUploadedBlockProbeFn(h.db, orgID, sha256ID, probe)
		if probeErr != nil {
			return probeErr
		}
		switch probe.Decision {
		case db.BlockReuseReusable:
			var ensureErr error
			storageKey, ensureErr := EnsureReusableBlockPresentForPhase(c.Request.Context(), h.db, sha256ID, probe, storedContent, h.storageManager, blockStore, storageClass, orgID, phase)
			materializationTarget = BlockMaterializationTarget{StorageClass: probe.StorageClass, StorageKey: storageKey}
			return ensureErr
		case db.BlockReuseNeedsPut:
			target, resolveErr := ResolveNeedsPutBlockStoreForPhase(h.storageManager, blockStore, storageClass, probe, orgID, sha256ID, phase)
			if resolveErr != nil {
				return resolveErr
			}
			materializationTarget = target
			_, putErr := PutBlockMaterializationTarget(c.Request.Context(), h.db, orgID, sha256ID, target, storedContent, putUploadedBlockAutoDirectFn, nil)
			if putErr != nil {
				return fmt.Errorf("failed to store block: %w", putErr)
			}
			return nil
		case db.BlockReuseBlockedByGC:
			return ErrBlockDeleteInProgress
		}
		return fmt.Errorf("unexpected block reuse decision %d for %s", probe.Decision, sha256ID)
	}, func() error {
		// Register block metadata + a provisional reference (kept alive by TTL until
		// the fs_object commit below creates the permanent reference), then write the
		// external SHA-1 mapping only after the block is durable in Cassandra.
		return RegisterUploadedBlockTargetAndMapping(c.Request.Context(), h.db, orgID, repoID, sha256ID, uploadOperationID, len(storedContent), materializationTarget, fileID)
	}, nil, nil); err != nil {
		log.Printf("[UploadFile] CRITICAL: failed to materialize block org=%s block=%s ext=%s: %v", orgID, sha256ID[:16], fileID[:16], err)
		if errors.Is(err, ErrBlockDeleteInProgress) || errors.Is(err, ErrBlockCanonicalStateNotVisible) {
			writeUploadFileError(c, err)
		} else if errors.Is(err, ErrBlockMappingWriteFailed) {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create block mapping"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to store block metadata"})
		}
		return
	}

	actualFilename, storageDeltaBytes, storageDeltaFiles, err := h.finalizeStoredUploadMetadata(orgID, userID, repoID, parentDir, filename, []string{fileID}, fileSize, replace, nil)
	if err != nil {
		// Failed publication leaves the provisional up: reference to Cassandra
		// TTL and Phase 0 instead of deleting from block_references.
		writeUploadFileError(c, err)
		return
	}

	// Record traffic (fire-and-forget)
	if rec := traffic.Get(); rec != nil {
		traffic.RecordCheckedTransfer(rec, uploadTrafficStatus, orgID, userID, traffic.WebUpload, fileSize)
	}
	if err := traffic.AdjustStorageCountersByDeltaSync(h.db, orgID, userID, repoID, storageDeltaBytes, storageDeltaFiles); err != nil {
		log.Printf("[UploadFile] failed to update storage counters: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update storage counters"})
		return
	}

	// Return Seafile-compatible response
	c.JSON(http.StatusOK, []gin.H{{"name": actualFilename, "id": fileID, "size": strconv.FormatInt(fileSize, 10)}})
}

// writeCreateFileError maps CreateFile failures to their HTTP contract. It is a
// named function rather than an inline switch so the mapping is reachable from a
// test: the block-materialization sentinels it shares with writeUploadFileError
// are exactly the ones that regress silently, since removing a case only changes a
// status code and never breaks a build.
func writeCreateFileError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, errLibraryNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
	case errors.Is(err, errParentDirectoryNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": errorMessageOrFallback(err, "parent directory not found")})
	case errors.Is(err, errFileExists):
		c.JSON(http.StatusConflict, gin.H{"error": "file already exists"})
	case errors.Is(err, errBlockStorageUnavailable):
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "block storage not available"})
	case errors.Is(err, ErrBlockDeleteInProgress):
		c.JSON(http.StatusConflict, gin.H{"error": "block is being deleted; retry the create"})
	// Same transient class as the fence above; see writeUploadFileError.
	case errors.Is(err, ErrBlockCanonicalStateNotVisible):
		c.Header("Retry-After", "1")
		c.JSON(http.StatusConflict, gin.H{"error": "block state is still converging; retry the create"})
	case errors.Is(err, ErrLibraryHeadConflict):
		c.JSON(http.StatusConflict, gin.H{"error": "library was modified concurrently; retry the create"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create file"})
	}
}

func writeUploadFileError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrLibraryHeadConflict):
		// CLIENT_CONTRACT: the 409 status is the authoritative signal, but
		// frontend uploaders also match this exact string as a fallback when
		// status code is not observable (see RETRYABLE_UPLOAD_CONFLICT_ERROR
		// in frontend/src/utils/upload-finalization.js). Keep the wording in
		// sync across both places.
		c.JSON(http.StatusConflict, gin.H{"error": "library was modified concurrently; retry the upload"})
	case errors.Is(err, ErrBlockDeleteInProgress):
		c.JSON(http.StatusConflict, gin.H{"error": "block is being deleted; retry the upload"})
	// Same transient class as the fence above: the block is installed and durable,
	// only its canonical row stayed invisible for this request's confirmation
	// budget. The block funnels (web /blocks/upload, seafhttp, sync PutBlock)
	// already answer 409 here; a 500 on this surface would report a healthy block
	// as a server fault and split one state across two status codes.
	case errors.Is(err, ErrBlockCanonicalStateNotVisible):
		c.Header("Retry-After", "1")
		c.JSON(http.StatusConflict, gin.H{"error": "block state is still converging; retry the upload"})
	case errors.Is(err, errUploadStorageQuotaExceeded):
		c.JSON(http.StatusForbidden, gin.H{"error": "storage quota exceeded"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update library"})
	}
}

func currentUploadStorageDelta(fsHelper *FSHelper, repoID, parentDir, filename string, fileSize int64, replace bool) (int64, int64, error) {
	if _, err := fsHelper.GetHeadCommitID(repoID); err != nil {
		return 0, 0, fmt.Errorf("%w: %v", errLibraryNotFound, err)
	}

	parentResult, err := fsHelper.TraverseToPath(repoID, parentDir)
	if err != nil {
		return 0, 0, fmt.Errorf("%w: %v", errParentDirectoryNotFound, err)
	}

	var dirEntries []FSEntry
	if parentDir == "/" {
		dirEntries = parentResult.Entries
	} else {
		if parentResult.TargetEntry == nil {
			return 0, 0, errParentDirectoryNotFound
		}
		dirEntries, err = fsHelper.GetDirectoryEntries(repoID, parentResult.TargetFSID)
		if err != nil {
			return 0, 0, fmt.Errorf("failed to read parent directory: %w", err)
		}
	}

	storageDeltaBytes := fileSize
	storageDeltaFiles := int64(1)
	if existing := FindEntryInList(dirEntries, filename); existing != nil && replace {
		storageDeltaBytes = fileSize - existing.Size
		storageDeltaFiles = 0
	}

	return storageDeltaBytes, storageDeltaFiles, nil
}

func (h *FileHandler) finalizeStoredUploadMetadata(orgID, userID, repoID, parentDir, filename string, blockIDs []string, fileSize int64, replace bool, commitBlocks []commitBlockPlacement) (string, int64, int64, error) {
	fsHelper := NewFSHelper(h.db)
	startedAt := time.Now()
	attemptsUsed := 0
	result := "error"
	defer func() {
		metrics.UploadFinalizeAttempts.WithLabelValues("v2_direct", result).Observe(float64(attemptsUsed))
		metrics.UploadFinalizeDuration.WithLabelValues("v2_direct", result).Observe(time.Since(startedAt).Seconds())
	}()

	var lastConflict error

	for attempt := 1; attempt <= uploadMetadataRetryAttempts; attempt++ {
		attemptsUsed = attempt
		actualFilename, storageDeltaBytes, storageDeltaFiles, err := h.finalizeStoredUploadMetadataOnce(fsHelper, orgID, userID, repoID, parentDir, filename, blockIDs, fileSize, replace, commitBlocks)
		if err == nil {
			result = "success"
			return actualFilename, storageDeltaBytes, storageDeltaFiles, nil
		}
		if !errors.Is(err, ErrLibraryHeadConflict) {
			if errors.Is(err, errUploadStorageQuotaExceeded) {
				result = "quota_exceeded"
			}
			return "", 0, 0, err
		}
		lastConflict = err
		metrics.UploadFinalizeHeadConflictsTotal.WithLabelValues("v2_direct").Inc()
		if attempt == uploadMetadataRetryAttempts {
			break
		}
		sleepFor := libraryHeadMutationRetryBackoff(attempt)
		log.Printf("[UploadFile] Retrying metadata publish for repo=%s after head conflict (%d/%d), sleeping %s", repoID, attempt, uploadMetadataRetryAttempts, sleepFor)
		time.Sleep(sleepFor)
	}

	log.Printf("[UploadFile] Exhausted metadata retries for repo=%s: %v", repoID, lastConflict)
	result = "retry_exhausted"
	metrics.UploadFinalizeRetryExhaustedTotal.WithLabelValues("v2_direct").Inc()
	return "", 0, 0, fmt.Errorf("%w: failed to finalize upload metadata after %d attempts", ErrLibraryHeadConflict, uploadMetadataRetryAttempts)
}

func (h *FileHandler) finalizeStoredUploadMetadataOnce(fsHelper *FSHelper, orgID, userID, repoID, parentDir, filename string, blockIDs []string, fileSize int64, replace bool, commitBlocks []commitBlockPlacement) (string, int64, int64, error) {
	// Capture HEAD and root FS ID in one consistent snapshot so the CAS
	// compare at publish time uses the exact same HEAD the tree was built from.
	parentResult, snapshot, err := fsHelper.TraverseToPathAtHead(repoID, parentDir)
	if err != nil {
		return "", 0, 0, fmt.Errorf("%w: %v", errParentDirectoryNotFound, err)
	}

	var dirEntries []FSEntry
	if parentDir == "/" {
		dirEntries = parentResult.Entries
	} else {
		if parentResult.TargetEntry == nil {
			return "", 0, 0, errParentDirectoryNotFound
		}
		dirEntries, err = fsHelper.GetDirectoryEntries(repoID, parentResult.TargetFSID)
		if err != nil {
			return "", 0, 0, fmt.Errorf("failed to read parent directory: %w", err)
		}
	}

	actualFilename := filename
	storageDeltaBytes := fileSize
	storageDeltaFiles := int64(1)
	if existing := FindEntryInList(dirEntries, filename); existing != nil {
		if replace {
			storageDeltaBytes = fileSize - existing.Size
			storageDeltaFiles = 0
			dirEntries = RemoveEntryFromList(dirEntries, filename)
		} else {
			actualFilename = GenerateUniqueName(dirEntries, filename)
		}
	}

	if storageDeltaBytes > 0 {
		if checker := traffic.GetChecker(); checker != nil {
			if st, _ := checker.CheckStorageQuota(orgID, userID, storageDeltaBytes); !st.Allowed {
				return "", 0, 0, errUploadStorageQuotaExceeded
			}
		}
	}

	pendingFile, err := newPendingPublishedFile(actualFilename, blockIDs, fileSize)
	if err != nil {
		return "", 0, 0, fmt.Errorf("failed to create file metadata: %w", err)
	}
	fileFSID := pendingFile.fsID

	newEntry := FSEntry{
		ID:       fileFSID,
		Name:     actualFilename,
		Mode:     ModeFile,
		MTime:    time.Now().Unix(),
		Size:     fileSize,
		Modifier: modifierIdentityForUser(userID),
	}
	newDirEntries := AddEntryToList(dirEntries, newEntry)
	newDirFSID, err := fsHelper.CreateDirectoryFSObject(repoID, newDirEntries)
	if err != nil {
		return "", 0, 0, fmt.Errorf("failed to update directory: %w", err)
	}

	var newRootFSID string
	if parentDir == "/" {
		newRootFSID = newDirFSID
	} else {
		parentDirName := path.Base(parentDir)
		grandParentEntries := make([]FSEntry, len(parentResult.Entries))
		copy(grandParentEntries, parentResult.Entries)
		for i := range grandParentEntries {
			if grandParentEntries[i].Name == parentDirName {
				grandParentEntries[i].ID = newDirFSID
				break
			}
		}
		newGrandParentFSID, err := fsHelper.CreateDirectoryFSObject(repoID, grandParentEntries)
		if err != nil {
			return "", 0, 0, fmt.Errorf("failed to update parent directory: %w", err)
		}
		newRootFSID, err = fsHelper.RebuildPathToRoot(repoID, parentResult, newGrandParentFSID)
		if err != nil {
			return "", 0, 0, fmt.Errorf("failed to rebuild path: %w", err)
		}
	}

	description := fmt.Sprintf("Added or modified \"%s\".\n", actualFilename)
	pendingFiles := []*pendingPublishedFile{pendingFile}
	commitCreatedAt := time.Now().UTC()
	newCommitID := buildCommitID(repoID, newRootFSID, description, commitCreatedAt)
	if err := fsHelper.stagePendingPublishedFiles(orgID, repoID, newCommitID, pendingFiles); err != nil {
		if cleanupErr := CleanupFailedPublishAttempt(h.db, orgID, repoID, newCommitID, newCommitID, pendingFiles); cleanupErr != nil {
			return "", 0, 0, errors.Join(
				fmt.Errorf("failed to stage publish-attempt block references for commit %s: %w", newCommitID, err),
				fmt.Errorf("cleanup failed publish commit %s: %w", newCommitID, cleanupErr),
			)
		}
		return "", 0, 0, fmt.Errorf("failed to stage publish-attempt block references for commit %s: %w", newCommitID, err)
	}
	fileFromBlocksAfterStagedBarrier(repoID)
	if err := queuePendingPublishedFileRepairs(h.db, orgID, repoID, newCommitID, pendingFiles); err != nil {
		cleanupErr := CleanupFailedPublishAttempt(h.db, orgID, repoID, newCommitID, newCommitID, pendingFiles)
		clearErr := clearPendingPublishedFileRepairs(h.db, orgID, repoID, newCommitID, pendingFiles)
		return "", 0, 0, errors.Join(
			fmt.Errorf("failed to queue durable publish repair for commit %s: %w", newCommitID, err),
			cleanupErr,
			clearErr,
		)
	}
	if err := fsHelper.insertCommit(repoID, newCommitID, userID, newRootFSID, snapshot.HeadCommitID, description, commitCreatedAt); err != nil {
		cleanupErr := CleanupFailedPublishAttempt(h.db, orgID, repoID, newCommitID, newCommitID, pendingFiles)
		clearErr := clearPendingPublishedFileRepairs(h.db, orgID, repoID, newCommitID, pendingFiles)
		return "", 0, 0, errors.Join(
			fmt.Errorf("failed to create commit: %w", err),
			cleanupErr,
			clearErr,
		)
	}

	if err := fileFromBlocksBeforeHeadBarrier(repoID); err != nil {
		cleanupErr := CleanupFailedPublishAttempt(h.db, orgID, repoID, newCommitID, newCommitID, pendingFiles)
		clearErr := clearPendingPublishedFileRepairs(h.db, orgID, repoID, newCommitID, pendingFiles)
		return "", 0, 0, errors.Join(err, cleanupErr, clearErr)
	}
	if err := h.validateCommitBlockPublicationFences(orgID, commitBlocks); err != nil {
		cleanupErr := CleanupFailedPublishAttempt(h.db, orgID, repoID, newCommitID, newCommitID, pendingFiles)
		clearErr := clearPendingPublishedFileRepairs(h.db, orgID, repoID, newCommitID, pendingFiles)
		return "", 0, 0, errors.Join(err, cleanupErr, clearErr)
	}
	if err := fsHelper.UpdateLibraryHeadFromSnapshot(snapshot, repoID, newCommitID, snapshot.HeadCommitID); err != nil {
		if errors.Is(err, ErrLibraryHeadConflict) {
			if cleanupErr := CleanupFailedPublishAttempt(h.db, orgID, repoID, newCommitID, newCommitID, pendingFiles); cleanupErr != nil {
				return "", 0, 0, fmt.Errorf("failed to clean up conflict publish attempt %s: %w", newCommitID, cleanupErr)
			}
			if clearErr := clearPendingPublishedFileRepairs(h.db, orgID, repoID, newCommitID, pendingFiles); clearErr != nil {
				log.Printf("[UploadFile] WARNING: failed to clear queued publish repair for repo=%s commit=%s fs_object=%s after head conflict: %v", repoID, newCommitID, pendingFile.fsID, clearErr)
			}
		}
		return "", 0, 0, fmt.Errorf("failed to update library: %w", err)
	}
	if ownerErr := clearPendingPublishedFileOwnersFn(h.db, repoID, pendingFiles); ownerErr != nil {
		log.Printf("[UploadFile] WARNING: published repo=%s commit=%s but failed to clear pending fs_object owners: %v", repoID, newCommitID, ownerErr)
	}
	if err := fsHelper.promotePendingPublishedFiles(orgID, repoID, newCommitID, pendingFiles); err != nil {
		log.Printf("[UploadFile] WARNING: head updated for repo=%s commit=%s but failed to promote block references for fs_object %s: %v", repoID, newCommitID, fileFSID, err)
		schedulePendingPublishedFileRepairs(h.db, orgID, repoID, newCommitID, pendingFiles, "UploadFile")
	} else if clearErr := clearPendingPublishedFileRepairs(h.db, orgID, repoID, newCommitID, pendingFiles); clearErr != nil {
		log.Printf("[UploadFile] WARNING: published repo=%s commit=%s but failed to clear queued publish repair for fs_object %s: %v", repoID, newCommitID, pendingFile.fsID, clearErr)
	}

	return actualFilename, storageDeltaBytes, storageDeltaFiles, nil
}

// copyBatchFiles handles copying multiple files in a single operation.
// Each file gets its own commit so that the head advances correctly between copies.
func (h *FileHandler) copyBatchFiles(c *gin.Context, srcPaths []string, srcRepoID, dstRepoID, dstDir, orgID, userID, conflictPolicy string) {
	if srcRepoID != dstRepoID {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "cross-repo batch copy not yet implemented"})
		return
	}

	repoID := srcRepoID
	dstDir = normalizePath(dstDir)

	if h.db == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database not available"})
		return
	}

	fsHelper := NewFSHelper(h.db)

	type copyResult struct {
		Name string `json:"name"`
		Path string `json:"path"`
	}
	var results []copyResult

	for _, rawSrcPath := range srcPaths {
		srcPath := normalizePath(rawSrcPath)

		copyOutcome, err := copyItemWithinRepoWithRetry("CopyFileBatch", fsHelper, orgID, userID, repoID, srcPath, dstDir, conflictPolicy)
		if err != nil {
			switch {
			case errors.Is(err, ErrBatchSourceNotFound):
				log.Printf("[copyBatchFiles] source not found, skipping: %s", srcPath)
				continue
			case errors.Is(err, errLibraryNotFound):
				c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
				return
			case errors.Is(err, ErrBatchDestinationNotFound):
				c.JSON(http.StatusNotFound, gin.H{"error": "destination directory not found"})
				return
			case errors.Is(err, ErrBatchItemLocked):
				c.JSON(http.StatusForbidden, gin.H{"error": "file is locked by another user"})
				return
			case errors.Is(err, ErrBatchLockStatusUnavailable):
				c.JSON(http.StatusServiceUnavailable, gin.H{"error": "failed to verify file lock"})
				return
			case errors.Is(err, ErrStorageQuotaExceeded):
				c.JSON(http.StatusForbidden, gin.H{"error": "storage quota exceeded"})
				return
			case errors.Is(err, ErrLibraryHeadConflict):
				c.JSON(http.StatusConflict, gin.H{"error": "library was modified concurrently; retry the copy"})
				return
			default:
				var conflictErr *ConflictError
				if errors.As(err, &conflictErr) {
					log.Printf("[copyBatchFiles] skipping %s: conflict and no policy", srcPath)
					continue
				}
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to copy file"})
				return
			}
		}

		if !copyOutcome.skipped {
			if !applyStorageCounterDelta(c, h.db, orgID, userID, repoID, copyOutcome.deltaBytes, copyOutcome.deltaFiles) {
				return
			}
		}

		results = append(results, copyResult{Name: copyOutcome.itemName, Path: path.Join(dstDir, copyOutcome.itemName)})
	}

	c.JSON(http.StatusOK, gin.H{
		"repo_id":    dstRepoID,
		"parent_dir": dstDir,
		"dst_items":  results,
	})
}

// normalizePath ensures path starts with / and removes trailing /
func normalizePath(p string) string {
	if p == "" {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	if p != "/" && strings.HasSuffix(p, "/") {
		p = strings.TrimSuffix(p, "/")
	}
	return path.Clean(p)
}

// GetDownloadInfo returns repository sync information for desktop client
// Implements Seafile API: GET /api2/repos/:repo_id/download-info/
func (h *FileHandler) GetDownloadInfo(c *gin.Context) {
	repoID := c.Param("repo_id")
	if repoID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "repo_id is required"})
		return
	}
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	// CUSTOM PERMISSION CHECK: download flag
	if h.permMiddleware != nil && !h.permMiddleware.RequirePermFlag(c, "download") {
		c.JSON(http.StatusForbidden, gin.H{"error": "download is not allowed by your permission"})
		return
	}

	// Get library info from database
	var libID, ownerID, name, description, headCommitID string
	var encrypted bool
	var encVersion int
	var magic, randomKey string
	var sizeBytes int64
	var updatedAt time.Time

	err := h.db.Session().Query(`
		SELECT library_id, owner_id, name, description, encrypted, enc_version,
		       magic, random_key, head_commit_id, size_bytes, updated_at
		FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, repoID).Scan(
		&libID, &ownerID, &name, &description, &encrypted, &encVersion,
		&magic, &randomKey, &headCommitID, &sizeBytes, &updatedAt,
	)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
		return
	}

	// Generate a sync token for this repo
	token, err := h.tokenCreator.CreateSyncToken(orgID, repoID, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate sync token"})
		return
	}

	// Format repo size in Seafile's human-readable format
	repoSizeFormatted := formatSizeSeafile(sizeBytes)

	// Format mtime as relative time HTML (Seafile format)
	mtimeRelative := formatRelativeTimeHTML(updatedAt)

	// Build response in Seafile format
	// Convert encrypted bool to int (Seafile uses 1/0, not true/false)
	encryptedInt := 0
	if encrypted {
		encryptedInt = 1
	}

	// Resolve actual permission for the user
	perm := "rw"
	if h.permMiddleware != nil {
		rawPerm, err := h.permMiddleware.GetLibraryPermissionRaw(orgID, userID, repoID)
		if err == nil && rawPerm != "" {
			perm = rawPerm
		}
	}

	relayHost := effectiveHostname(c, h.config)
	response := gin.H{
		"relay_id":            relayHost,
		"relay_addr":          relayHost,
		"relay_port":          relayPortFromRequest(c, h.config),
		"email":               userID + "@sesamefs.local",
		"token":               token,
		"repo_id":             repoID,
		"repo_name":           name,
		"repo_desc":           "",
		"repo_size":           sizeBytes,
		"repo_size_formatted": repoSizeFormatted,
		"repo_version":        1,
		"mtime":               updatedAt.Unix(),
		"mtime_relative":      mtimeRelative,
		"encrypted":           encryptedInt,
		"permission":          perm,
		"head_commit_id":      headCommitID,
	}

	// Add encryption fields if encrypted
	// Translate enc_version for Seafile desktop client compatibility:
	// Our enc_version 12 (dual-mode) uses PBKDF2-compatible magic/random_key
	// that the Seafile client can decrypt with enc_version 2
	if encrypted {
		clientEncVersion := encVersion
		if encVersion == 12 || encVersion == 10 {
			// Translate SesameFS dual-mode (12) or native (10) to Seafile v2
			clientEncVersion = 2
		}
		response["enc_version"] = clientEncVersion
		// CRITICAL: For Seafile v2, salt must be empty string (not null)
		response["salt"] = ""
		response["magic"] = magic
		response["random_key"] = randomKey
	}

	c.JSON(http.StatusOK, response)
}

// V21DirectoryResponse represents the v2.1 API response format for directory listing
type V21DirectoryResponse struct {
	UserPerm   string   `json:"user_perm"`
	DirID      string   `json:"dir_id"`
	DirentList []Dirent `json:"dirent_list"`
}

// ListDirectoryV21 returns directory contents in v2.1 API format
// Implements Seafile API: GET /api/v2.1/repos/:repo_id/dir/?p=/path
func (h *FileHandler) ListDirectoryV21(c *gin.Context) {
	repoID := c.Param("repo_id")
	dirPath := c.DefaultQuery("p", "/")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	// Normalize path
	dirPath = normalizePath(dirPath)

	// ========================================================================
	// PERMISSION CHECK: User must have at least read access to library
	// ========================================================================
	if h.permMiddleware != nil {
		hasAccess, err := h.permMiddleware.HasLibraryAccessCtx(c, orgID, userID, repoID, middleware.PermissionR)
		if err != nil {
			log.Printf("[ListDirectoryV21] Failed to check permissions: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check permissions"})
			return
		}

		if !hasAccess {
			if respondIfLibraryMissing(c, h.db.Session(), orgID, repoID) {
				return
			}
			log.Printf("[ListDirectoryV21] Permission denied: user %q does not have access to library %q", userID, repoID)
			c.JSON(http.StatusForbidden, gin.H{"error": "you do not have access to this library"})
			return
		}
	}

	// Resolve actual permission for the user (rw/r based on share or ownership)
	perm := "rw"
	if h.permMiddleware != nil {
		rawPerm, err := h.permMiddleware.GetLibraryPermissionRaw(orgID, userID, repoID)
		if err == nil && rawPerm != "" {
			perm = rawPerm
		}
	}

	// ========================================================================
	// ENCRYPTION CHECK: Encrypted libraries require active decrypt session
	// ========================================================================
	if !h.requireDecryptSession(c, orgID, userID, repoID) {
		return
	}

	// Check if database is available
	if h.db == nil {
		c.JSON(http.StatusOK, V21DirectoryResponse{
			UserPerm:   perm,
			DirID:      "",
			DirentList: []Dirent{},
		})
		return
	}

	// Get library's head_commit_id and auto_delete_days
	var libID, headCommitID string
	var autoDeleteDays int
	err := h.db.Session().Query(`
		SELECT library_id, head_commit_id, auto_delete_days FROM libraries
		WHERE org_id = ? AND library_id = ?
	`, orgID, repoID).Scan(&libID, &headCommitID, &autoDeleteDays)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
		return
	}

	// If no head commit, return empty directory
	if headCommitID == "" {
		c.JSON(http.StatusOK, V21DirectoryResponse{
			UserPerm:   perm,
			DirID:      "",
			DirentList: []Dirent{},
		})
		return
	}

	// Get root_fs_id from the head commit
	var rootFSID string
	err = h.db.Session().Query(`
		SELECT root_fs_id FROM commits
		WHERE library_id = ? AND commit_id = ?
	`, repoID, headCommitID).Scan(&rootFSID)
	if err != nil {
		log.Printf("ListDirectoryV21: failed to get commit %s: %v", headCommitID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load library data"})
		return
	}

	// All-zeros root means an empty library (no files).
	// This is a valid state after the desktop client syncs a deletion of all files.
	if rootFSID == "" || rootFSID == strings.Repeat("0", 40) {
		if dirPath == "/" {
			c.JSON(http.StatusOK, V21DirectoryResponse{
				UserPerm:   perm,
				DirID:      rootFSID,
				DirentList: []Dirent{},
			})
			return
		}
		c.JSON(http.StatusNotFound, gin.H{"error": "directory not found"})
		return
	}

	// Check for with_parents parameter (used by file-chooser tree in move/copy dialogs)
	withParents := c.Query("with_parents") == "true"

	// Traverse from root to requested path, collecting parent entries if with_parents=true
	currentFSID := rootFSID
	var parentDirents []Dirent // collected parent directory entries when with_parents=true
	if dirPath != "/" {
		// Split path into components and traverse
		parts := strings.Split(strings.Trim(dirPath, "/"), "/")
		currentParentPath := "/"
		for i, part := range parts {
			if part == "" {
				continue
			}

			// Get current directory's entries
			var entriesJSON string
			err = h.db.Session().Query(`
				SELECT dir_entries FROM fs_objects
				WHERE library_id = ? AND fs_id = ?
			`, repoID, currentFSID).Scan(&entriesJSON)
			if err != nil {
				log.Printf("ListDirectoryV21: failed to get fs_object %s: %v", currentFSID, err)
				c.JSON(http.StatusNotFound, gin.H{"error": "Folder does not exist."})
				return
			}

			// Parse entries and find the next component
			var entries []FSEntry
			if entriesJSON != "" && entriesJSON != "[]" {
				if err := json.Unmarshal([]byte(entriesJSON), &entries); err != nil {
					log.Printf("ListDirectoryV21: failed to parse entries for %s: %v", currentFSID, err)
					c.JSON(http.StatusInternalServerError, gin.H{"error": "invalid directory data"})
					return
				}
			}

			// If with_parents, collect directory entries at this level
			if withParents {
				parentDir := currentParentPath
				if parentDir != "/" {
					parentDir = parentDir + "/"
				}
				for _, entry := range entries {
					if entry.Mode&0170000 == 040000 || entry.Mode == ModeDir {
						parentDirents = append(parentDirents, Dirent{
							ID:        entry.ID,
							Name:      entry.Name,
							Type:      "dir",
							Size:      entry.Size,
							MTime:     entry.MTime,
							ParentDir: parentDir,
						})
					}
				}
			}

			// Find the child directory
			found := false
			for _, entry := range entries {
				if entry.Name == part {
					if entry.Mode&0170000 == 040000 || entry.Mode == ModeDir {
						currentFSID = entry.ID
						found = true
						break
					} else {
						c.JSON(http.StatusBadRequest, gin.H{"error": "path is not a directory"})
						return
					}
				}
			}

			if !found {
				c.JSON(http.StatusNotFound, gin.H{"error": "Folder does not exist."})
				return
			}

			// Update current parent path for next iteration
			if i == 0 {
				currentParentPath = "/" + part
			} else {
				currentParentPath = currentParentPath + "/" + part
			}
		}
	}

	// Get the target directory's entries
	var entriesJSON string
	err = h.db.Session().Query(`
		SELECT dir_entries FROM fs_objects
		WHERE library_id = ? AND fs_id = ?
	`, repoID, currentFSID).Scan(&entriesJSON)
	if err != nil {
		log.Printf("ListDirectoryV21: failed to get target fs_object %s: %v", currentFSID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "library data is unavailable"})
		return
	}

	// Parse entries
	var entries []FSEntry
	if entriesJSON != "" && entriesJSON != "[]" {
		if err := json.Unmarshal([]byte(entriesJSON), &entries); err != nil {
			log.Printf("ListDirectoryV21: failed to parse target entries for %s: %v", currentFSID, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "corrupted directory data"})
			return
		}
	}

	// Get starred files for this user and repo to check starred status
	// userID already declared above in permission check
	starredPaths := make(map[string]bool)
	if userID != "" {
		iter := h.db.Session().Query(`
			SELECT path FROM starred_files WHERE user_id = ? AND repo_id = ?
		`, userID, repoID).Iter()
		var starredPath string
		for iter.Scan(&starredPath) {
			starredPaths[starredPath] = true
		}
		iter.Close()
	}

	// Get locked files for this repo
	type lockInfo struct {
		LockedBy string
		LockedAt time.Time
	}
	lockedFiles := make(map[string]lockInfo)

	// Parse repo UUID for locked files query
	repoUUID, err := gocql.ParseUUID(repoID)
	if err != nil {
		log.Printf("ListDirectoryV21: failed to parse repo UUID for locked files: %v", err)
	} else {
		lockIter := h.db.Session().Query(`
			SELECT path, locked_by, locked_at FROM locked_files WHERE repo_id = ?
		`, repoUUID).Iter()
		var lockPath string
		var lockedByUUID gocql.UUID
		var lockedAt time.Time
		for lockIter.Scan(&lockPath, &lockedByUUID, &lockedAt) {
			log.Printf("ListDirectoryV21: found locked file: path=%s, locked_by=%s", lockPath, lockedByUUID.String())
			lockedFiles[lockPath] = lockInfo{LockedBy: lockedByUUID.String(), LockedAt: lockedAt}
		}
		if err := lockIter.Close(); err != nil {
			log.Printf("ListDirectoryV21: failed to get locked files: %v", err)
		}
	}
	log.Printf("ListDirectoryV21: repoID=%s, dirPath=%s, lockedFiles count=%d", repoID, dirPath, len(lockedFiles))

	// Convert FSEntry to Dirent for API response (v2.1 format)
	resolvePersistedModifier := h.newPersistedModifierResolver(orgID)
	direntList := make([]Dirent, 0, len(entries))
	for _, entry := range entries {
		// Determine type from mode
		fileType := "file"
		if entry.Mode&0170000 == 040000 || entry.Mode == ModeDir {
			fileType = "dir"
		}

		// Build full path for starred check
		var fullPath string
		if dirPath == "/" {
			fullPath = "/" + entry.Name
		} else {
			fullPath = dirPath + "/" + entry.Name
		}

		// Check if this file is starred
		isStarred := starredPaths[fullPath]

		// parent_dir format: with trailing slash (e.g., "/foo/bar/") except root is "/"
		// This matches Seafile's format which the frontend expects
		entryParentDir := dirPath
		if withParents {
			if dirPath == "/" {
				entryParentDir = "/"
			} else {
				entryParentDir = dirPath + "/"
			}
		}

		dirent := Dirent{
			ID:         entry.ID,
			Name:       entry.Name,
			Type:       fileType,
			Size:       entry.Size,
			MTime:      entry.MTime,
			Permission: perm,
			ParentDir:  entryParentDir,
			Starred:    isStarred,
		}

		// Normalize the persisted modifier so v2.1 surfaces the same real identity as
		// file/detail instead of leaking the internal uid@sesamefs.local marker.
		modifierEmail, modifierName := resolvePersistedModifier(entry.Modifier)
		applyResolvedModifierToDirent(&dirent, modifierEmail, modifierName)

		// Add file expiry countdown if library has auto_delete_days set
		if fileType == "file" && autoDeleteDays > 0 && entry.MTime > 0 {
			expiresAt := entry.MTime + int64(autoDeleteDays)*86400
			dirent.ExpiresAt = expiresAt
		}

		// Add file-specific fields
		if fileType == "file" {
			// Check if file is locked
			if lock, isLocked := lockedFiles[fullPath]; isLocked {
				log.Printf("ListDirectoryV21: file %s is LOCKED by %s", fullPath, lock.LockedBy)
				dirent.IsLocked = true
				dirent.LockTime = lock.LockedAt.Unix()
				dirent.LockOwner = lock.LockedBy
				dirent.LockOwnerName = strings.Split(lock.LockedBy, "@")[0]
				dirent.LockOwnerContactEmail = lock.LockedBy
				dirent.LockedByMe = (lock.LockedBy == userID)
			} else {
				dirent.IsLocked = false
				dirent.LockTime = 0
				dirent.LockOwner = ""
				dirent.LockOwnerName = ""
				dirent.LockOwnerContactEmail = ""
				dirent.LockedByMe = false
			}
			dirent.IsFreezed = false
		}

		direntList = append(direntList, dirent)
	}

	// If with_parents, prepend parent directory entries to the result
	if withParents && len(parentDirents) > 0 {
		direntList = append(parentDirents, direntList...)
	}

	// Return v2.1 format response
	c.JSON(http.StatusOK, V21DirectoryResponse{
		UserPerm:   perm,
		DirID:      currentFSID,
		DirentList: direntList,
	})
}

// FileLockRequest represents the request for locking/unlocking a file
type FileLockRequest struct {
	Operation string `json:"operation" form:"operation"` // lock or unlock
}

func (h *FileHandler) requireReplaceRevertFileNotLockedByOther(c *gin.Context, repoID, filePath, userID, conflictPolicy string) bool {
	if conflictPolicy != "replace" {
		return true
	}
	return h.requireFileNotLockedByOther(c, repoID, filePath, userID)
}

func (h *FileHandler) requireReplaceRevertDirectoryNotLockedByOther(c *gin.Context, repoID, dirPath, userID, conflictPolicy string) bool {
	if conflictPolicy != "replace" {
		return true
	}
	return h.requireSubtreeNotLockedByOther(c, repoID, dirPath, userID)
}

// RevertFile restores a file to a previous version from commit history
// POST /api/v2.1/repos/:repo_id/file/?p=/path with operation=revert&commit_id=xxx
// Optional: conflict_policy=replace|skip to handle existing files
func (h *FileHandler) RevertFile(c *gin.Context) {
	repoID := c.Param("repo_id")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")
	filePath := c.Query("p")

	var restoreFileReq struct {
		CommitID       string `json:"commit_id" form:"commit_id"`
		ConflictPolicy string `json:"conflict_policy" form:"conflict_policy"`
	}
	c.ShouldBind(&restoreFileReq) //nolint:errcheck
	commitID := restoreFileReq.CommitID
	conflictPolicy := restoreFileReq.ConflictPolicy

	if filePath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path is required"})
		return
	}
	if commitID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "commit_id is required"})
		return
	}

	filePath = normalizePath(filePath)

	// CUSTOM PERMISSION CHECK: modify flag
	if h.permMiddleware != nil && !h.permMiddleware.RequirePermFlag(c, "modify") {
		c.JSON(http.StatusForbidden, gin.H{"error": "modify is not allowed by your permission"})
		return
	}

	// LOCK ENFORCEMENT: a replace-revert overwrites the file at this path, so reject it
	// when the file is locked by another user. Non-replace policies never overwrite the
	// existing file (autorename/keep_both restore under a new name, skip/conflict do
	// nothing), so they are exempt — matching the copy path's replace-only enforcement.
	if !h.requireReplaceRevertFileNotLockedByOther(c, repoID, filePath, userID, conflictPolicy) {
		return
	}

	fsHelper := NewFSHelper(h.db)

	// Get the root_fs_id from the target commit
	var oldRootFSID string
	err := h.db.Session().Query(`
		SELECT root_fs_id FROM commits WHERE library_id = ? AND commit_id = ?
	`, repoID, commitID).Scan(&oldRootFSID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "commit not found"})
		return
	}

	// Traverse the old commit to find the file
	oldResult, err := fsHelper.TraverseToPathFromRoot(repoID, oldRootFSID, filePath)
	if err != nil || oldResult.TargetEntry == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "file not found in specified commit"})
		return
	}

	oldEntry := *oldResult.TargetEntry

	fileName := path.Base(filePath)
	parentDir := path.Dir(filePath)
	if parentDir == "." {
		parentDir = "/"
	}
	var deltaBytes int64
	var deltaFiles int64
	var alreadySameContent bool
	var skipped bool

	err = retryLibraryHeadMutation("RevertFile", func() error {
		snapshot, err := fsHelper.GetLibraryHeadSnapshot(repoID)
		if err != nil {
			return fmt.Errorf("%w: %v", errLibraryNotFound, err)
		}

		result, err := fsHelper.TraverseToPathFromSnapshot(repoID, snapshot, parentDir)
		if err != nil {
			return fmt.Errorf("%w: %v", errParentDirectoryNotFound, err)
		}

		var parentEntries []FSEntry
		if parentDir == "/" {
			parentEntries = result.Entries
		} else {
			if result.TargetFSID == "" || result.TargetEntry == nil {
				return errParentDirectoryNotFound
			}
			parentEntries, err = fsHelper.GetDirectoryEntries(repoID, result.TargetFSID)
			if err != nil {
				return fmt.Errorf("failed to read parent directory: %w", err)
			}
		}

		currentFileName := fileName
		existingEntry := FindEntryInList(parentEntries, currentFileName)
		if existingEntry != nil {
			if existingEntry.ID == oldEntry.ID {
				alreadySameContent = true
				return nil
			}

			switch conflictPolicy {
			case "replace":
			case "keep_both", "autorename":
				currentFileName = GenerateUniqueName(parentEntries, currentFileName)
			case "skip":
				skipped = true
				return nil
			default:
				return &ConflictError{ItemName: currentFileName}
			}
		}

		var replacedEntry *FSEntry
		if conflictPolicy == "replace" && existingEntry != nil {
			ent := *existingEntry
			replacedEntry = &ent
		}

		// Reverting to historical content is still a new content mutation authored by
		// the current user, so the restored entry must be re-stamped instead of
		// carrying the historical modifier forward.
		revertedEntry := contentModifiedFileEntry(oldEntry, currentFileName, userID, time.Now().Unix())

		currentDeltaBytes, currentDeltaFiles, err := fsEntryDelta(fsHelper, repoID, revertedEntry, replacedEntry)
		if err != nil {
			return fmt.Errorf("failed to compute storage delta: %w", err)
		}
		if currentDeltaBytes > 0 {
			if !storageQuotaAllowsDelta(orgID, userID, currentDeltaBytes) {
				return ErrStorageQuotaExceeded
			}
		}

		newEntries := RemoveEntryFromList(parentEntries, currentFileName)
		newEntries = AddEntryToList(newEntries, revertedEntry)

		newParentFSID, err := fsHelper.CreateDirectoryFSObject(repoID, newEntries)
		if err != nil {
			return fmt.Errorf("failed to update directory: %w", err)
		}

		newRootFSID, err := rebuildTraversedDirectoryToRoot(fsHelper, repoID, result, parentDir, newParentFSID)
		if err != nil {
			return fmt.Errorf("failed to rebuild path: %w", err)
		}

		description := fmt.Sprintf("Reverted file \"%s\"", currentFileName)
		newCommitID, err := fsHelper.CreateCommit(repoID, userID, newRootFSID, snapshot.HeadCommitID, description)
		if err != nil {
			return fmt.Errorf("failed to create commit: %w", err)
		}

		if err := fsHelper.UpdateLibraryHeadFromSnapshot(snapshot, repoID, newCommitID, snapshot.HeadCommitID); err != nil {
			return err
		}

		deltaBytes = currentDeltaBytes
		deltaFiles = currentDeltaFiles
		return nil
	})
	if err != nil {
		switch {
		case errors.Is(err, errLibraryNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
		case errors.Is(err, errParentDirectoryNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": errorMessageOrFallback(err, "parent directory does not exist, restore the folder first")})
		case errors.Is(err, ErrStorageQuotaExceeded):
			c.JSON(http.StatusForbidden, gin.H{"error": "storage quota exceeded"})
		case errors.Is(err, ErrLibraryHeadConflict):
			c.JSON(http.StatusConflict, gin.H{"error": "library was modified concurrently; retry the revert"})
		default:
			var conflictErr *ConflictError
			if errors.As(err, &conflictErr) {
				c.JSON(http.StatusConflict, gin.H{
					"error":             "conflict",
					"conflicting_items": []string{conflictErr.ItemName},
					"message":           "file already exists with different content",
				})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to revert file"})
		}
		return
	}

	if alreadySameContent {
		c.JSON(http.StatusOK, gin.H{"success": true, "message": "file already has the same content"})
		return
	}
	if skipped {
		c.JSON(http.StatusOK, gin.H{"success": true, "skipped": true, "message": "file already exists, skipped"})
		return
	}

	if !applyStorageCounterDelta(c, h.db, orgID, userID, repoID, deltaBytes, deltaFiles) {
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// RevertDirectory restores a directory to a previous version from commit history
// POST /api/v2.1/repos/:repo_id/dir/?p=/path with operation=revert&commit_id=xxx
// Optional: conflict_policy=replace|skip to handle existing directories
func (h *FileHandler) RevertDirectory(c *gin.Context) {
	repoID := c.Param("repo_id")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")
	dirPath := c.Query("p")

	var revertDirReq struct {
		CommitID       string `json:"commit_id" form:"commit_id"`
		ConflictPolicy string `json:"conflict_policy" form:"conflict_policy"`
	}
	c.ShouldBind(&revertDirReq) //nolint:errcheck
	commitID := revertDirReq.CommitID
	conflictPolicy := revertDirReq.ConflictPolicy

	if dirPath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path is required"})
		return
	}
	if commitID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "commit_id is required"})
		return
	}

	dirPath = normalizePath(dirPath)

	// CUSTOM PERMISSION CHECK: modify flag
	if h.permMiddleware != nil && !h.permMiddleware.RequirePermFlag(c, "modify") {
		c.JSON(http.StatusForbidden, gin.H{"error": "modify is not allowed by your permission"})
		return
	}

	// LOCK ENFORCEMENT: a replace-revert overwrites the directory subtree at this path,
	// which would clobber a file locked by another user inside it. Reject when the path
	// or any descendant is locked by someone else. Non-replace policies restore under a
	// new name (or skip), so they never touch the locked file and are exempt.
	if !h.requireReplaceRevertDirectoryNotLockedByOther(c, repoID, dirPath, userID, conflictPolicy) {
		return
	}

	fsHelper := NewFSHelper(h.db)

	// Get the root_fs_id from the target commit
	var oldRootFSID string
	err := h.db.Session().Query(`
		SELECT root_fs_id FROM commits WHERE library_id = ? AND commit_id = ?
	`, repoID, commitID).Scan(&oldRootFSID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "commit not found"})
		return
	}

	// Traverse the old commit to find the directory
	oldResult, err := fsHelper.TraverseToPathFromRoot(repoID, oldRootFSID, dirPath)
	if err != nil || oldResult.TargetEntry == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "directory not found in specified commit"})
		return
	}

	oldEntry := *oldResult.TargetEntry
	if oldEntry.Mode != 16384 { // Not a directory
		c.JSON(http.StatusBadRequest, gin.H{"error": "path is not a directory in specified commit"})
		return
	}

	dirName := path.Base(dirPath)
	parentDir := path.Dir(dirPath)
	if parentDir == "." {
		parentDir = "/"
	}
	var deltaBytes int64
	var deltaFiles int64
	var alreadySameContent bool
	var skipped bool

	err = retryLibraryHeadMutation("RevertDirectory", func() error {
		snapshot, err := fsHelper.GetLibraryHeadSnapshot(repoID)
		if err != nil {
			return fmt.Errorf("%w: %v", errLibraryNotFound, err)
		}

		result, err := fsHelper.TraverseToPathFromSnapshot(repoID, snapshot, parentDir)
		if err != nil {
			return fmt.Errorf("%w: %v", errParentDirectoryNotFound, err)
		}

		var parentEntries []FSEntry
		if parentDir == "/" {
			parentEntries = result.Entries
		} else {
			if result.TargetFSID == "" || result.TargetEntry == nil {
				return errParentDirectoryNotFound
			}
			parentEntries, err = fsHelper.GetDirectoryEntries(repoID, result.TargetFSID)
			if err != nil {
				return fmt.Errorf("failed to read parent directory: %w", err)
			}
		}

		currentDirName := dirName
		existingEntry := FindEntryInList(parentEntries, currentDirName)
		if existingEntry != nil {
			if existingEntry.ID == oldEntry.ID {
				alreadySameContent = true
				return nil
			}

			switch conflictPolicy {
			case "replace":
			case "keep_both", "autorename":
				currentDirName = GenerateUniqueName(parentEntries, currentDirName)
			case "skip":
				skipped = true
				return nil
			default:
				return &ConflictError{ItemName: currentDirName}
			}
		}

		var replacedDir *FSEntry
		if conflictPolicy == "replace" && existingEntry != nil {
			ent := *existingEntry
			replacedDir = &ent
		}

		revertedDir := oldEntry
		revertedDir.Name = currentDirName
		revertedDir.MTime = time.Now().Unix()

		currentDeltaBytes, currentDeltaFiles, err := fsEntryDelta(fsHelper, repoID, revertedDir, replacedDir)
		if err != nil {
			return fmt.Errorf("failed to compute storage delta: %w", err)
		}
		if currentDeltaBytes > 0 {
			if !storageQuotaAllowsDelta(orgID, userID, currentDeltaBytes) {
				return ErrStorageQuotaExceeded
			}
		}

		newEntries := RemoveEntryFromList(parentEntries, currentDirName)
		newEntries = AddEntryToList(newEntries, revertedDir)

		newParentFSID, err := fsHelper.CreateDirectoryFSObject(repoID, newEntries)
		if err != nil {
			return fmt.Errorf("failed to update directory: %w", err)
		}

		newRootFSID, err := rebuildTraversedDirectoryToRoot(fsHelper, repoID, result, parentDir, newParentFSID)
		if err != nil {
			return fmt.Errorf("failed to rebuild path: %w", err)
		}

		description := fmt.Sprintf("Reverted folder \"%s\"", currentDirName)
		newCommitID, err := fsHelper.CreateCommit(repoID, userID, newRootFSID, snapshot.HeadCommitID, description)
		if err != nil {
			return fmt.Errorf("failed to create commit: %w", err)
		}

		if err := fsHelper.UpdateLibraryHeadFromSnapshot(snapshot, repoID, newCommitID, snapshot.HeadCommitID); err != nil {
			return err
		}

		deltaBytes = currentDeltaBytes
		deltaFiles = currentDeltaFiles
		return nil
	})
	if err != nil {
		switch {
		case errors.Is(err, errLibraryNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
		case errors.Is(err, errParentDirectoryNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": errorMessageOrFallback(err, "parent directory does not exist, restore the parent folder first")})
		case errors.Is(err, ErrStorageQuotaExceeded):
			c.JSON(http.StatusForbidden, gin.H{"error": "storage quota exceeded"})
		case errors.Is(err, ErrLibraryHeadConflict):
			c.JSON(http.StatusConflict, gin.H{"error": "library was modified concurrently; retry the revert"})
		default:
			var conflictErr *ConflictError
			if errors.As(err, &conflictErr) {
				c.JSON(http.StatusConflict, gin.H{
					"error":             "conflict",
					"conflicting_items": []string{conflictErr.ItemName},
					"message":           "directory already exists with different content",
				})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to revert directory"})
		}
		return
	}

	if alreadySameContent {
		c.JSON(http.StatusOK, gin.H{"success": true, "message": "directory already has the same content"})
		return
	}
	if skipped {
		c.JSON(http.StatusOK, gin.H{"success": true, "skipped": true, "message": "directory already exists, skipped"})
		return
	}

	if !applyStorageCounterDelta(c, h.db, orgID, userID, repoID, deltaBytes, deltaFiles) {
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// LockFile handles file lock/unlock operations
// Implements: PUT /api/v2.1/repos/:repo_id/file/?p=/path
func (h *FileHandler) LockFile(c *gin.Context) {
	repoID := c.Param("repo_id")
	filePath := c.Query("p")
	userID := c.GetString("user_id")
	orgID := c.GetString("org_id")

	log.Printf("LockFile: repoID=%s, filePath=%s, userID=%s", repoID, filePath, userID)

	if filePath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path is required"})
		return
	}

	// Normalize path
	filePath = normalizePath(filePath)

	// CUSTOM PERMISSION CHECK: modify flag
	if h.permMiddleware != nil && !h.permMiddleware.RequirePermFlag(c, "modify") {
		c.JSON(http.StatusForbidden, gin.H{"error": "modify is not allowed by your permission"})
		return
	}

	// ENCRYPTION CHECK: Encrypted libraries require active decrypt session
	if !h.requireDecryptSession(c, orgID, userID, repoID) {
		return
	}

	var req FileLockRequest
	if err := c.ShouldBind(&req); err != nil {
		log.Printf("LockFile: failed to bind request: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	log.Printf("LockFile: operation=%s", req.Operation)

	// Validate repo id up front so an obviously malformed id is a 400, not a 503.
	if _, err := gocql.ParseUUID(repoID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid repo_id"})
		return
	}

	switch req.Operation {
	case "lock":
		// Guard against phantom locks: only an existing file may be locked. Locking a
		// nonexistent path or a directory would plant a lock row the FS tree never backs,
		// which SubtreeLockedByOther would then honour to block legitimate operations on
		// the real parent. Verified for "lock" only — "unlock" stays lenient so a stale
		// lock on an already-deleted path can still be cleared.
		exists, isDir, terr := lockTargetState(h, repoID, filePath)
		if terr != nil {
			log.Printf("LockFile: failed to verify lock target path=%s: %v", filePath, terr)
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "failed to verify lock target"})
			return
		}
		if !exists {
			c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
			return
		}
		if isDir {
			c.JSON(http.StatusBadRequest, gin.H{"error": "cannot lock a directory"})
			return
		}

		// Atomic acquire/refresh via LWT (INSERT ... IF NOT EXISTS). A file already
		// locked by another user cannot be stolen; the owner may re-issue "lock" to
		// refresh. CAS makes this race-safe across regions (no check-then-write).
		lockTime := time.Now()
		res, ownerID, err := acquireFileLock(h, repoID, filePath, userID, lockTime)
		if err != nil {
			log.Printf("LockFile: failed to acquire lock on path=%s: %v", filePath, err)
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "failed to acquire file lock"})
			return
		}
		if res == db.LockConflict {
			log.Printf("LockFile: lock conflict on path=%s held by %s, requested by %s", filePath, ownerID, userID)
			c.JSON(http.StatusConflict, gin.H{"error": "file is locked by another user", "lock_owner": ownerID})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"success":    true,
			"repo_id":    repoID,
			"path":       filePath,
			"is_locked":  true,
			"lock_time":  lockTime.Unix(),
			"lock_owner": userID,
		})
	case "unlock":
		// Atomic owner-only release via LWT (DELETE ... IF locked_by = ?): a non-owner
		// cannot strip the lock, and the delete cannot race an ownership change that
		// slipped in between a separate check and write.
		released, ownerID, err := releaseFileLock(h, repoID, filePath, userID)
		if err != nil {
			log.Printf("LockFile: failed to release lock on path=%s: %v", filePath, err)
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "failed to release file lock"})
			return
		}
		if !released {
			log.Printf("LockFile: unlock refused on path=%s held by %s, requested by %s", filePath, ownerID, userID)
			c.JSON(http.StatusForbidden, gin.H{"error": "file is locked by another user", "lock_owner": ownerID})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"success":   true,
			"repo_id":   repoID,
			"path":      filePath,
			"is_locked": false,
		})
	default:
		log.Printf("LockFile: unknown operation: %s", req.Operation)
		c.JSON(http.StatusBadRequest, gin.H{"error": "operation must be 'lock' or 'unlock'"})
	}
}

// requireFileNotLockedByOther rejects the request with 403 when the target file is
// locked by a user other than userID. It returns true when the caller may proceed
// (unlocked, or locked by the caller themselves). If the lock state cannot be
// verified, the request fails closed with 503.
func (h *FileHandler) requireFileNotLockedByOther(c *gin.Context, repoID, filePath, userID string) bool {
	blocked, ownerID, err := checkFileLockedByOther(h, repoID, filePath, userID)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "failed to verify file lock"})
		return false
	}
	if blocked {
		c.JSON(http.StatusForbidden, gin.H{"error": "file is locked by another user", "lock_owner": ownerID})
		return false
	}
	return true
}

// requireSubtreeNotLockedByOther is the directory-aware counterpart of
// requireFileNotLockedByOther: it rejects the request with 403 when the target path
// OR any descendant under it is locked by another user, and 503 when the lock state
// cannot be verified. Use it for operations that can act on a folder (rename, move,
// delete) so a folder action cannot strip or relocate a file locked inside it.
func (h *FileHandler) requireSubtreeNotLockedByOther(c *gin.Context, repoID, targetPath, userID string) bool {
	blocked, ownerID, err := checkSubtreeLockedByOther(h, repoID, targetPath, userID)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "failed to verify file lock"})
		return false
	}
	if blocked {
		c.JSON(http.StatusForbidden, gin.H{"error": "a file in this path is locked by another user", "lock_owner": ownerID})
		return false
	}
	return true
}

// FileRevision represents a file revision in API response
type FileRevision struct {
	CommitID      string `json:"commit_id"`
	RevFileID     string `json:"rev_file_id"`
	CTime         int64  `json:"ctime"`
	Description   string `json:"description"`
	Size          int64  `json:"size"`
	RevRenamedOld string `json:"rev_renamed_old_path,omitempty"`
	CreatorName   string `json:"creator_name"`
	CreatorEmail  string `json:"creator_email"`
}

// GetFileRevisions returns the revision history of a file
// Implements: GET /api2/repo/file_revisions/:repo_id/
func (h *FileHandler) GetFileRevisions(c *gin.Context) {
	repoID := c.Param("repo_id")
	filePath := c.Query("p")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	if filePath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path is required"})
		return
	}

	// ENCRYPTION CHECK: Encrypted libraries require active decrypt session
	if !h.requireDecryptSession(c, orgID, userID, repoID) {
		return
	}

	if h.db == nil {
		c.JSON(http.StatusOK, gin.H{"data": []FileRevision{}})
		return
	}

	// Query commits for this library
	iter := h.db.Session().Query(`
		SELECT commit_id, root_fs_id, creator_id, description, created_at
		FROM commits WHERE library_id = ?
		LIMIT 50
	`, repoID).Iter()

	var revisions []FileRevision
	var commitID, rootFSID, creatorID, description string
	var createdAt time.Time

	fsHelper := NewFSHelper(h.db)

	// Cache for user lookups to avoid repeated queries
	type userInfo struct {
		Name  string
		Email string
	}
	userCache := make(map[string]userInfo)

	resolveUser := func(cid string) userInfo {
		if cached, ok := userCache[cid]; ok {
			return cached
		}
		var uName, uEmail string
		h.db.Session().Query(`SELECT name, email FROM users WHERE org_id = ? AND user_id = ?`,
			orgID, cid).Scan(&uName, &uEmail)
		if uName == "" {
			uName = uEmail
		}
		if uName == "" {
			uName = cid
		}
		if uEmail == "" {
			uEmail = cid + "@sesamefs.local"
		}
		info := userInfo{Name: uName, Email: uEmail}
		userCache[cid] = info
		return info
	}

	for iter.Scan(&commitID, &rootFSID, &creatorID, &description, &createdAt) {
		// Check if file exists in this commit by traversing from the commit's root
		result, err := fsHelper.TraverseToPathFromRoot(repoID, rootFSID, filePath)
		if err != nil || result.TargetEntry == nil {
			continue
		}

		user := resolveUser(creatorID)

		revisions = append(revisions, FileRevision{
			CommitID:     commitID,
			RevFileID:    result.TargetEntry.ID,
			CTime:        createdAt.Unix(),
			Description:  description,
			Size:         result.TargetEntry.Size,
			CreatorName:  user.Name,
			CreatorEmail: user.Email,
		})
	}
	iter.Close()

	// Get library info for response
	var libName string
	h.db.Session().Query(`
		SELECT name FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, repoID).Scan(&libName)

	c.JSON(http.StatusOK, gin.H{
		"data":        revisions,
		"repo_name":   libName,
		"repo_id":     repoID,
		"file_path":   filePath,
		"next_start":  0,
		"total_count": len(revisions),
	})
}

// FileHistoryRecord represents a single history record in v2.1 format
type FileHistoryRecord struct {
	CommitID      string `json:"commit_id"`
	RevFileID     string `json:"rev_file_id"`
	RevFileSize   int64  `json:"rev_file_size"`
	Size          int64  `json:"size"` // Duplicate of RevFileSize for frontend compatibility
	CTime         int64  `json:"ctime"`
	CreatorEmail  string `json:"creator_email"`
	CreatorName   string `json:"creator_name"`
	CreatorAvatar string `json:"creator_avatar_url"`
	Path          string `json:"path"`
	Description   string `json:"description"`
}

// GetFileHistoryV21 returns file history in v2.1 API format
// Implements: GET /api/v2.1/repos/:repo_id/file/new_history/?path=/xxx&page=1&per_page=25
func (h *FileHandler) GetFileHistoryV21(c *gin.Context) {
	repoID := c.Param("repo_id")
	filePath := c.Query("path")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	if filePath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error_msg": "path is required"})
		return
	}

	// Normalize path
	filePath = normalizePath(filePath)

	// ENCRYPTION CHECK: Encrypted libraries require active decrypt session
	if !h.requireDecryptSession(c, orgID, userID, repoID) {
		return
	}

	// Parse pagination
	page := 1
	perPage := 25
	if p := c.Query("page"); p != "" {
		if parsed, err := strconv.Atoi(p); err == nil && parsed > 0 {
			page = parsed
		}
	}
	if pp := c.Query("per_page"); pp != "" {
		if parsed, err := strconv.Atoi(pp); err == nil && parsed > 0 {
			perPage = parsed
		}
	}

	if h.db == nil {
		c.JSON(http.StatusOK, gin.H{
			"data":        []FileHistoryRecord{},
			"page":        page,
			"total_count": 0,
		})
		return
	}

	// Query commits for this library, ordered by created_at desc
	iter := h.db.Session().Query(`
		SELECT commit_id, root_fs_id, creator_id, description, created_at
		FROM commits WHERE library_id = ?
		LIMIT 100
	`, repoID).Iter()

	var allRecords []FileHistoryRecord
	var commitID, rootFSID, creatorID, description string
	var createdAt time.Time

	fsHelper := NewFSHelper(h.db)

	// Collect all commits that contain this file
	type commitEntry struct {
		CommitID    string
		RevFileID   string
		RevFileSize int64
		CreatorID   string
		Description string
		CreatedAt   time.Time
	}
	var entries []commitEntry

	for iter.Scan(&commitID, &rootFSID, &creatorID, &description, &createdAt) {
		// Check if file exists in this commit by traversing from the commit's root
		result, err := fsHelper.TraverseToPathFromRoot(repoID, rootFSID, filePath)
		if err != nil || result.TargetEntry == nil {
			continue
		}

		entries = append(entries, commitEntry{
			CommitID:    commitID,
			RevFileID:   result.TargetEntry.ID,
			RevFileSize: result.TargetEntry.Size,
			CreatorID:   creatorID,
			Description: description,
			CreatedAt:   createdAt,
		})
	}
	iter.Close()

	// Sort by time descending so we can deduplicate chronologically
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].CreatedAt.After(entries[j].CreatedAt)
	})

	// Cache for user lookups to avoid repeated queries
	type userInfo struct {
		Name  string
		Email string
	}
	userCache := make(map[string]userInfo)

	resolveUser := func(creatorID string) userInfo {
		if cached, ok := userCache[creatorID]; ok {
			return cached
		}
		var userName, userEmail string
		h.db.Session().Query(`SELECT name, email FROM users WHERE org_id = ? AND user_id = ?`,
			orgID, creatorID).Scan(&userName, &userEmail)
		if userName == "" {
			userName = userEmail
		}
		if userName == "" {
			userName = creatorID
		}
		if userEmail == "" {
			userEmail = creatorID + "@sesamefs.local"
		}
		info := userInfo{Name: userName, Email: userEmail}
		userCache[creatorID] = info
		return info
	}

	// Deduplicate: only include a record when the file's fs_id changes
	// (i.e., the file was actually modified in that commit)
	lastSeenFSID := ""
	for _, e := range entries {
		if e.RevFileID == lastSeenFSID {
			continue // same file content as the more recent commit, skip
		}
		lastSeenFSID = e.RevFileID

		user := resolveUser(e.CreatorID)
		allRecords = append(allRecords, FileHistoryRecord{
			CommitID:      e.CommitID,
			RevFileID:     e.RevFileID,
			RevFileSize:   e.RevFileSize,
			Size:          e.RevFileSize,
			CTime:         e.CreatedAt.Unix(),
			CreatorEmail:  user.Email,
			CreatorName:   user.Name,
			CreatorAvatar: "",
			Path:          filePath,
			Description:   e.Description,
		})
	}

	// Sort by ctime descending (most recent first)
	sort.Slice(allRecords, func(i, j int) bool {
		return allRecords[i].CTime > allRecords[j].CTime
	})

	// Apply pagination
	totalCount := len(allRecords)
	start := (page - 1) * perPage
	end := start + perPage

	var records []FileHistoryRecord
	if start < totalCount {
		if end > totalCount {
			end = totalCount
		}
		records = allRecords[start:end]
	} else {
		records = []FileHistoryRecord{}
	}

	// Get library info
	var libName string
	h.db.Session().Query(`
		SELECT name FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, repoID).Scan(&libName)

	c.JSON(http.StatusOK, gin.H{
		"data":        records,
		"page":        page,
		"total_count": totalCount,
	})
}

// GetRepoHistory returns the commit history for a repository
// Implements: GET /api/v2.1/repos/:repo_id/history/?page=1&per_page=25
func (h *FileHandler) GetRepoHistory(c *gin.Context) {
	repoID := c.Param("repo_id")
	orgID := c.GetString("org_id")

	// Parse pagination
	page := 1
	perPage := 25
	if p := c.Query("page"); p != "" {
		if parsed, err := strconv.Atoi(p); err == nil && parsed > 0 {
			page = parsed
		}
	}
	if pp := c.Query("per_page"); pp != "" {
		if parsed, err := strconv.Atoi(pp); err == nil && parsed > 0 {
			perPage = parsed
		}
	}

	if h.db == nil {
		c.JSON(http.StatusOK, gin.H{"data": []interface{}{}, "more": false})
		return
	}

	// Query all commits for this library
	iter := h.db.Session().Query(`
		SELECT commit_id, parent_id, creator_id, description, created_at
		FROM commits WHERE library_id = ?
	`, repoID).Iter()

	type commitRecord struct {
		CommitID    string
		ParentID    string
		CreatorID   string
		Description string
		CreatedAt   time.Time
	}

	var records []commitRecord
	var commitID, parentID, creatorID, description string
	var createdAt time.Time
	for iter.Scan(&commitID, &parentID, &creatorID, &description, &createdAt) {
		records = append(records, commitRecord{
			CommitID:    commitID,
			ParentID:    parentID,
			CreatorID:   creatorID,
			Description: description,
			CreatedAt:   createdAt,
		})
	}
	iter.Close()

	// Sort by time descending
	sort.Slice(records, func(i, j int) bool {
		return records[i].CreatedAt.After(records[j].CreatedAt)
	})

	// Apply pagination: fetch one extra to determine "more"
	totalCount := len(records)
	start := (page - 1) * perPage
	end := start + perPage
	hasMore := false

	if start >= totalCount {
		c.JSON(http.StatusOK, gin.H{"data": []interface{}{}, "more": false})
		return
	}
	if end < totalCount {
		hasMore = true
	}
	if end > totalCount {
		end = totalCount
	}

	pageRecords := records[start:end]

	// Build response: look up creator names
	type historyEntry struct {
		CommitID       string   `json:"commit_id"`
		Description    string   `json:"description"`
		Time           string   `json:"time"`
		Name           string   `json:"name"`
		Email          string   `json:"email"`
		SecondParentID string   `json:"second_parent_id,omitempty"`
		ClientVersion  string   `json:"client_version"`
		DeviceName     string   `json:"device_name"`
		Tags           []string `json:"tags"`
	}

	data := make([]historyEntry, 0, len(pageRecords))
	for _, rec := range pageRecords {
		// Look up user name and email
		var userName, userEmail string
		h.db.Session().Query(`SELECT name, email FROM users WHERE org_id = ? AND user_id = ?`,
			orgID, rec.CreatorID).Scan(&userName, &userEmail)
		if userName == "" {
			userName = userEmail
		}
		if userName == "" {
			userName = rec.CreatorID
		}
		if userEmail == "" {
			userEmail = rec.CreatorID + "@sesamefs.local"
		}

		data = append(data, historyEntry{
			CommitID:      rec.CommitID,
			Description:   rec.Description,
			Time:          rec.CreatedAt.Format(time.RFC3339),
			Name:          userName,
			Email:         userEmail,
			ClientVersion: "",
			DeviceName:    "",
			Tags:          []string{},
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"data": data,
		"more": hasMore,
	})
}

// GetFileUploadedBytes returns the number of bytes already uploaded for resumable uploads
// Implements: GET /api/v2.1/repos/:repo_id/file-uploaded-bytes/?parent_dir=/&file_name=xxx
func (h *FileHandler) GetFileUploadedBytes(c *gin.Context) {
	repoID := c.Param("repo_id")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	// ENCRYPTION CHECK: Encrypted libraries require active decrypt session
	if !h.requireDecryptSession(c, orgID, userID, repoID) {
		return
	}

	// For resumable uploads, this endpoint returns how many bytes have been uploaded
	// For now, return 0 to indicate no bytes uploaded (start fresh)
	// A full implementation would track partial uploads in the database

	c.JSON(http.StatusOK, gin.H{
		"uploadedBytes": 0,
	})
}

// BatchDeleteRequest represents the request body for batch delete operations
type BatchDeleteRequest struct {
	RepoID    string   `json:"repo_id"`
	ParentDir string   `json:"parent_dir"`
	Dirents   []string `json:"dirents"`
}

// BatchDeleteItems deletes multiple files/folders in a single operation
// Implements: DELETE /api/v2.1/repos/batch-delete-item/
func (h *FileHandler) BatchDeleteItems(c *gin.Context) {
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	// PERMISSION CHECK: Readonly and guest users cannot delete items
	if !h.requireWritePermission(c, orgID, userID) {
		return
	}

	var req BatchDeleteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	if req.RepoID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "repo_id is required"})
		return
	}

	if len(req.Dirents) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "dirents is required"})
		return
	}

	// CUSTOM PERMISSION CHECK: delete flag
	if h.permMiddleware != nil && !h.permMiddleware.RequirePermFlagForRepo(c, req.RepoID, "delete") {
		c.JSON(http.StatusForbidden, gin.H{"error": "delete is not allowed by your permission"})
		return
	}

	// ENCRYPTION CHECK: Encrypted libraries require active decrypt session
	if !h.requireDecryptSession(c, orgID, userID, req.RepoID) {
		return
	}

	parentDir := normalizePath(req.ParentDir)
	if parentDir == "" {
		parentDir = "/"
	}
	for _, name := range req.Dirents {
		// Subtree-aware: deleting a folder is blocked when it contains a file
		// locked by another user, not just when a top-level item is locked.
		if !h.requireSubtreeNotLockedByOther(c, req.RepoID, path.Join(parentDir, name), userID) {
			return
		}
	}

	fsHelper := NewFSHelper(h.db)

	var deletedEntries []FSEntry
	var responseCommitID string

	err := retryLibraryHeadMutation("BatchDeleteItems", func() error {
		snapshot, err := fsHelper.GetLibraryHeadSnapshot(req.RepoID)
		if err != nil {
			return fmt.Errorf("%w: %v", errLibraryNotFound, err)
		}

		result, err := fsHelper.TraverseToPathFromSnapshot(req.RepoID, snapshot, parentDir)
		if err != nil {
			return fmt.Errorf("%w: %v", errParentDirectoryNotFound, err)
		}

		var currentEntries []FSEntry
		if parentDir == "/" {
			currentEntries = append([]FSEntry(nil), result.Entries...)
		} else {
			if result.TargetFSID == "" {
				return errParentDirectoryNotFound
			}
			currentEntries, err = fsHelper.GetDirectoryEntries(req.RepoID, result.TargetFSID)
			if err != nil {
				return fmt.Errorf("failed to read parent directory: %w", err)
			}
		}

		currentDeletedNames := []string{}
		currentDeletedEntries := make([]FSEntry, 0, len(req.Dirents))
		for _, name := range req.Dirents {
			for _, entry := range currentEntries {
				if entry.Name == name {
					currentDeletedNames = append(currentDeletedNames, name)
					currentDeletedEntries = append(currentDeletedEntries, entry)
					break
				}
			}
		}
		for _, name := range currentDeletedNames {
			currentEntries = RemoveEntryFromList(currentEntries, name)
		}

		if len(currentDeletedNames) == 0 {
			responseCommitID = snapshot.HeadCommitID
			deletedEntries = nil
			return nil
		}

		newParentFSID, err := fsHelper.CreateDirectoryFSObject(req.RepoID, currentEntries)
		if err != nil {
			return fmt.Errorf("failed to update directory: %w", err)
		}

		newRootFSID, err := rebuildTraversedDirectoryToRoot(fsHelper, req.RepoID, result, parentDir, newParentFSID)
		if err != nil {
			return fmt.Errorf("failed to rebuild path: %w", err)
		}

		var description string
		if len(currentDeletedNames) == 1 {
			description = fmt.Sprintf("Deleted \"%s\"", currentDeletedNames[0])
		} else {
			description = fmt.Sprintf("Deleted \"%s\" and %d other items", currentDeletedNames[0], len(currentDeletedNames)-1)
		}

		commitID, err := fsHelper.CreateCommit(req.RepoID, userID, newRootFSID, snapshot.HeadCommitID, description)
		if err != nil {
			return fmt.Errorf("failed to create commit: %w", err)
		}

		if err := fsHelper.UpdateLibraryHeadFromSnapshot(snapshot, req.RepoID, commitID, snapshot.HeadCommitID); err != nil {
			return err
		}

		deletedEntries = append([]FSEntry(nil), currentDeletedEntries...)
		responseCommitID = commitID
		return nil
	})
	if err != nil {
		switch {
		case errors.Is(err, errLibraryNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
		case errors.Is(err, errParentDirectoryNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": errorMessageOrFallback(err, "parent directory not found")})
		case errors.Is(err, ErrLibraryHeadConflict):
			c.JSON(http.StatusConflict, gin.H{"error": "library was modified concurrently; retry the delete"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete items"})
		}
		return
	}

	if len(deletedEntries) == 0 {
		c.JSON(http.StatusOK, gin.H{
			"success":   true,
			"commit_id": responseCommitID,
		})
		return
	}

	// Clean up file tags and decrement storage counters for all deleted items (async, non-blocking).
	// Block references are released when GC sweeps the now-unreachable fs_objects
	// (they stay in fs_objects until version retention expires), so there is no
	// inline ref decrement here.
	go func() {
		for _, entry := range deletedEntries {
			deletedPath := path.Join(parentDir, entry.Name)

			// Drop the operator's own locks under each deleted item (other users'
			// locks here would have blocked the delete upstream).
			clearLocksAfterDelete(h, req.RepoID, deletedPath, userID)

			// Clean up tags
			if entry.Mode == ModeDir || entry.Mode&0170000 == 040000 {
				h.cleanupFileTagsForPrefix(req.RepoID, deletedPath)
			} else {
				h.cleanupFileTagsForPath(req.RepoID, deletedPath)
			}

			// Update storage-quota counters for the deletion.
			_, totalSize, fileCount, err := fsHelper.collectDirStats(req.RepoID, entry.ID)
			if err == nil && (totalSize > 0 || fileCount > 0) {
				traffic.DecrementStorageCounters(h.db, orgID, userID, req.RepoID, totalSize, fileCount)
			}
		}
	}()

	c.JSON(http.StatusOK, gin.H{
		"success":   true,
		"commit_id": responseCommitID,
	})
}

// cleanupFileTagsForPath removes all tag associations for a specific file path.
// Called asynchronously after file deletion to keep tag data consistent.
func (h *FileHandler) cleanupFileTagsForPath(repoID, filePath string) {
	CleanupFileTagsByPath(h.db, repoID, filePath)
}

// cleanupFileTagsForPrefix removes all tag associations for files under a directory path.
// Called asynchronously after directory deletion.
func (h *FileHandler) cleanupFileTagsForPrefix(repoID, dirPath string) {
	if h.db == nil {
		return
	}

	repoUUID, err := gocql.ParseUUID(repoID)
	if err != nil {
		return
	}

	// Clean up tags for the directory path itself
	h.cleanupFileTagsForPath(repoID, dirPath)

	// Find all file_tags for this repo and filter by prefix
	// Note: Cassandra doesn't support LIKE on clustering columns, so we scan the partition
	prefix := dirPath + "/"
	iter := h.db.Session().Query(`
		SELECT file_path, tag_id, file_tag_id FROM file_tags WHERE repo_id = ?
	`, repoUUID).Iter()

	var fp string
	var tagID2, fileTagID2 int
	for iter.Scan(&fp, &tagID2, &fileTagID2) {
		if len(fp) >= len(prefix) && fp[:len(prefix)] == prefix {
			batch := h.db.Session().Batch(gocql.LoggedBatch)
			batch.Query(`DELETE FROM file_tags WHERE repo_id = ? AND file_path = ? AND tag_id = ?`,
				repoUUID, fp, tagID2)
			batch.Query(`DELETE FROM file_tags_by_id WHERE repo_id = ? AND file_tag_id = ?`,
				repoUUID, fileTagID2)
			addFileTagsByTagDeleteQuery(batch, repoUUID, fp, tagID2)
			if err := batch.Exec(); err != nil {
				log.Printf("[cleanupFileTagsForPrefix] failed to delete tag rows for repo %s path %q tag %d: %v", repoID, fp, tagID2, err)
				continue
			}

			if err := h.db.Session().Query(`
				UPDATE repo_tag_file_counts SET file_count = file_count - 1
				WHERE repo_id = ? AND tag_id = ?
			`, repoUUID, tagID2).Exec(); err != nil {
				log.Printf("[cleanupFileTagsForPrefix] failed to decrement repo_tag_file_counts for repo %s tag %d: %v", repoID, tagID2, err)
			}
		}
	}
	iter.Close()
}

// CreateZipTask handles POST /api/v2.1/repos/:repo_id/zip-task/
// Creates a zip download task for a directory and returns a zip token.
// This is the authenticated counterpart to share-link-zip-task.
func (h *FileHandler) CreateZipTask(c *gin.Context) {
	repoID := c.Param("repo_id")
	path := c.DefaultQuery("p", "/")

	// CUSTOM PERMISSION CHECK: download flag
	if h.permMiddleware != nil && !h.permMiddleware.RequirePermFlag(c, "download") {
		c.JSON(http.StatusForbidden, gin.H{"error": "download is not allowed by your permission"})
		return
	}

	// Get user info from middleware
	orgID, exists := c.Get("org_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	// Check library exists and user has read permission
	var libraryID string
	err := h.db.Session().Query(`
		SELECT library_id FROM libraries
		WHERE org_id = ? AND library_id = ?
	`, orgID.(string), repoID).Scan(&libraryID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
		return
	}

	// Check read permission
	if h.permMiddleware != nil {
		hasRead, err := h.permMiddleware.HasLibraryAccess(orgID.(string), userID.(string), repoID, middleware.PermissionR)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check permissions"})
			return
		}
		if !hasRead {
			c.JSON(http.StatusForbidden, gin.H{"error": "you do not have read access to this library"})
			return
		}
	}

	// Generate a download token for the zip
	// The zip will be created on-the-fly when the token is used
	// Use zipTokenCreator (from RegisterV21LibraryRoutes) or fall back to tokenCreator
	var tc LibraryTokenCreator
	if h.zipTokenCreator != nil {
		tc = h.zipTokenCreator
	} else if h.tokenCreator != nil {
		tc = h.tokenCreator
	} else {
		log.Printf("[CreateZipTask] No token creator available")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "server misconfigured"})
		return
	}
	zipToken, err := tc.CreateDownloadToken(orgID.(string), repoID, path, userID.(string))
	if err != nil {
		log.Printf("[CreateZipTask] Failed to create download token: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create zip download token"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"zip_token": zipToken,
	})
}
