package v2

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/middleware"
	"github.com/Sesame-Disk/sesamefs/internal/traffic"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// BatchOperationHandler handles batch move/copy operations
type BatchOperationHandler struct {
	db             *db.DB
	config         *config.Config
	permMiddleware *middleware.PermissionMiddleware
	tasks          *TaskStore
}

// TaskStore stores async task progress
type TaskStore struct {
	mu    sync.RWMutex
	tasks map[string]*AsyncTask
}

// AsyncTask represents an async copy/move task
type AsyncTask struct {
	ID           string    `json:"task_id"`
	Type         string    `json:"type"`   // "move" or "copy"
	Status       string    `json:"status"` // "processing", "done", "failed"
	Total        int       `json:"total"`
	Done         int       `json:"done"`
	Failed       int       `json:"failed"`
	FailedReason string    `json:"failed_reason,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

// ConflictError indicates a name conflict at the destination
type ConflictError struct {
	ItemName string
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("item with name '%s' already exists in destination", e.ItemName)
}

var ErrBatchSourceNotFound = errors.New("batch source item not found")

var ErrBatchDestinationNotFound = errors.New("batch destination not found")

func isSameLocationMove(opType, srcRepoID, dstRepoID, srcParentPath, dstDir string) bool {
	return opType == "move" && srcRepoID == dstRepoID && normalizePath(srcParentPath) == normalizePath(dstDir)
}

func shouldSkipSourceRemovalAfterMove(result *PathTraverseResult, err error) bool {
	return err != nil || result == nil || result.TargetEntry == nil
}

func isPathWithin(candidatePath, parentPath string) bool {
	candidatePath = normalizePath(candidatePath)
	parentPath = normalizePath(parentPath)
	if parentPath == "/" {
		return candidatePath != "/"
	}
	return candidatePath == parentPath || strings.HasPrefix(candidatePath, parentPath+"/")
}

func isDirectoryEntry(entry *FSEntry) bool {
	if entry == nil {
		return false
	}
	return entry.Mode == ModeDir || entry.Mode&0170000 == 040000
}

func replacedDestinationTagCleanup(dstDir, itemName string, replacedEntry *FSEntry) (string, bool) {
	if replacedEntry == nil || itemName == "" {
		return "", false
	}
	return normalizePath(path.Join(dstDir, itemName)), isDirectoryEntry(replacedEntry)
}

func movedItemTagMutation(srcPath, dstDir, itemName string, movedEntry *FSEntry) (string, string, bool) {
	if movedEntry == nil || itemName == "" {
		return "", "", false
	}
	return normalizePath(srcPath), normalizePath(path.Join(dstDir, itemName)), isDirectoryEntry(movedEntry)
}

var loadZeroRefBlockStorageClassesFn = loadZeroRefBlockStorageClasses

func loadZeroRefBlockStorageClasses(database *db.DB, orgID string, blockIDs []string) (map[string][]string, error) {
	grouped := make(map[string][]string)
	seen := make(map[string]struct{}, len(blockIDs))
	var loadErr error
	for _, blockID := range blockIDs {
		blockID = strings.TrimSpace(blockID)
		if blockID == "" {
			continue
		}
		if _, dup := seen[blockID]; dup {
			continue
		}
		seen[blockID] = struct{}{}

		var storageClass string
		if err := database.Session().Query(`
			SELECT storage_class FROM blocks WHERE org_id = ? AND block_id = ?
		`, orgID, blockID).Scan(&storageClass); err != nil {
			loadErr = errors.Join(loadErr, fmt.Errorf("load storage class for block %s: %w", blockID, err))
			continue
		}
		storageClass = strings.TrimSpace(storageClass)
		if storageClass == "" {
			loadErr = errors.Join(loadErr, fmt.Errorf("load storage class for block %s: empty canonical storage class", blockID))
			continue
		}
		grouped[storageClass] = append(grouped[storageClass], blockID)
	}
	return grouped, loadErr
}

func runSameRepoMoveTagMutation(database *db.DB, repoID, oldPath, newPath string, moveByPrefix bool, replacedCleanupPath string, replacedCleanupByPrefix bool) {
	if replacedCleanupPath != "" {
		if replacedCleanupByPrefix {
			CleanupFileTagsByPrefix(database, repoID, replacedCleanupPath)
		} else {
			CleanupFileTagsByPath(database, repoID, replacedCleanupPath)
		}
	}
	if oldPath == "" || newPath == "" {
		return
	}
	if moveByPrefix {
		MoveFileTagsByPrefix(database, repoID, oldPath, newPath)
	} else {
		MoveFileTagsByPath(database, repoID, oldPath, newPath)
	}
}

func enqueueZeroRefBlocks(database *db.DB, orgID, repoID string, blockIDs []string) {
	if database == nil || len(blockIDs) == 0 {
		return
	}
	blockEnqueuer := getBlockEnqueuer()
	if blockEnqueuer == nil {
		return
	}

	grouped, err := loadZeroRefBlockStorageClassesFn(database, orgID, blockIDs)
	if err != nil {
		log.Printf("[enqueueZeroRefBlocks] failed to load canonical block storage classes for repo %s: %v", repoID, err)
	}
	if len(grouped) == 0 {
		return
	}
	classes := make([]string, 0, len(grouped))
	for storageClass := range grouped {
		classes = append(classes, storageClass)
	}
	sort.Strings(classes)
	for _, storageClass := range classes {
		blockEnqueuer.EnqueueBlocks(orgID, grouped[storageClass], storageClass)
	}
}

func batchOperationErrorResponse(err error, opType, itemName string) (int, gin.H, string) {
	switch {
	case errors.Is(err, ErrLibraryHeadConflict):
		msg := fmt.Sprintf("library was modified concurrently; retry the %s", opType)
		return http.StatusConflict, gin.H{"error": msg}, msg
	case errors.Is(err, ErrBatchSourceNotFound):
		msg := "source item not found"
		return http.StatusNotFound, gin.H{"error": msg}, msg
	case errors.Is(err, ErrBatchDestinationNotFound):
		msg := "destination directory not found"
		return http.StatusNotFound, gin.H{"error": msg}, msg
	case errors.Is(err, ErrStorageQuotaExceeded):
		msg := "storage quota exceeded"
		return http.StatusForbidden, gin.H{"error": msg}, msg
	default:
		var conflictErr *ConflictError
		if errors.As(err, &conflictErr) {
			return http.StatusConflict, gin.H{
				"error":             "conflict",
				"conflicting_items": []string{conflictErr.ItemName},
			}, "conflict"
		}
		msg := fmt.Sprintf("failed to %s %s", opType, itemName)
		return http.StatusInternalServerError, gin.H{"error": msg}, msg
	}
}

// Global task store
var globalTaskStore = &TaskStore{
	tasks: make(map[string]*AsyncTask),
}

// NewBatchOperationHandler creates a new batch operation handler
func NewBatchOperationHandler(database *db.DB, cfg *config.Config) *BatchOperationHandler {
	return &BatchOperationHandler{
		db:             database,
		config:         cfg,
		permMiddleware: middleware.NewPermissionMiddleware(database),
		tasks:          globalTaskStore,
	}
}

// RegisterBatchOperationRoutes registers the batch operation routes
func RegisterBatchOperationRoutes(rg *gin.RouterGroup, database *db.DB, cfg *config.Config) {
	h := NewBatchOperationHandler(database, cfg)

	// Sync operations (same-repo)
	rg.POST("/repos/sync-batch-move-item/", h.SyncBatchMove)
	rg.POST("/repos/sync-batch-move-item", h.SyncBatchMove)
	rg.POST("/repos/sync-batch-copy-item/", h.SyncBatchCopy)
	rg.POST("/repos/sync-batch-copy-item", h.SyncBatchCopy)

	// Async operations (cross-repo)
	rg.POST("/repos/async-batch-move-item/", h.AsyncBatchMove)
	rg.POST("/repos/async-batch-move-item", h.AsyncBatchMove)
	rg.POST("/repos/async-batch-copy-item/", h.AsyncBatchCopy)
	rg.POST("/repos/async-batch-copy-item", h.AsyncBatchCopy)

	// Task progress query (both URLs used by different frontend versions)
	rg.GET("/copy-move-task/", h.GetTaskProgress)
	rg.GET("/copy-move-task", h.GetTaskProgress)
	rg.GET("/query-copy-move-progress/", h.GetTaskProgress)
	rg.GET("/query-copy-move-progress", h.GetTaskProgress)
}

// BatchRequest represents a batch move/copy request
type BatchRequest struct {
	SrcRepoID      string   `json:"src_repo_id"`
	SrcParentDir   string   `json:"src_parent_dir"`
	DstRepoID      string   `json:"dst_repo_id"`
	DstParentDir   string   `json:"dst_parent_dir"`
	SrcDirents     []string `json:"src_dirents"`
	ConflictPolicy string   `json:"conflict_policy"` // "replace", "autorename", "skip", or empty
}

// SyncBatchMove handles synchronous batch move (same repo)
func (h *BatchOperationHandler) SyncBatchMove(c *gin.Context) {
	h.handleBatchOperation(c, "move", false)
}

// SyncBatchCopy handles synchronous batch copy (same repo)
func (h *BatchOperationHandler) SyncBatchCopy(c *gin.Context) {
	h.handleBatchOperation(c, "copy", false)
}

// AsyncBatchMove handles asynchronous batch move (cross-repo)
func (h *BatchOperationHandler) AsyncBatchMove(c *gin.Context) {
	h.handleBatchOperation(c, "move", true)
}

// AsyncBatchCopy handles asynchronous batch copy (cross-repo)
func (h *BatchOperationHandler) AsyncBatchCopy(c *gin.Context) {
	h.handleBatchOperation(c, "copy", true)
}

// handleBatchOperation processes batch move/copy operations
func (h *BatchOperationHandler) handleBatchOperation(c *gin.Context, opType string, async bool) {
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	var req BatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	log.Printf("[BatchOperation] %s async=%v: src_repo=%s src_dir=%s dst_repo=%s dst_dir=%s dirents=%v",
		opType, async, req.SrcRepoID, req.SrcParentDir, req.DstRepoID, req.DstParentDir, req.SrcDirents)

	// Validate required fields
	if req.SrcRepoID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "src_repo_id is required"})
		return
	}
	if req.DstRepoID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "dst_repo_id is required"})
		return
	}
	if len(req.SrcDirents) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "src_dirents is required"})
		return
	}

	if h.db == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database not available"})
		return
	}

	// CUSTOM PERMISSION CHECK: modify (move) or copy flag
	if h.permMiddleware != nil {
		flag := "modify"
		if opType == "copy" {
			flag = "copy"
		}
		if !h.permMiddleware.RequirePermFlagForRepo(c, req.SrcRepoID, flag) {
			c.JSON(http.StatusForbidden, gin.H{"error": flag + " is not allowed by your permission"})
			return
		}
	}

	// Permission check for source repo
	if !h.checkWritePermission(c, orgID, userID) {
		return
	}

	// Permission check for destination repo (if different)
	if req.SrcRepoID != req.DstRepoID {
		if h.permMiddleware != nil {
			hasWrite, err := h.permMiddleware.HasLibraryAccessCtx(c, orgID, userID, req.DstRepoID, middleware.PermissionRW)
			if err != nil {
				log.Printf("[BatchOperation] Failed to check dst permissions: %v", err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check permissions"})
				return
			}
			if !hasWrite {
				c.JSON(http.StatusForbidden, gin.H{"error": "you do not have write permission to destination library"})
				return
			}
		}
	}

	// Check encryption for source library
	if !h.checkDecryptSession(c, orgID, userID, req.SrcRepoID) {
		return
	}

	// Check encryption for destination library (if different)
	if req.SrcRepoID != req.DstRepoID {
		if !h.checkDecryptSession(c, orgID, userID, req.DstRepoID) {
			return
		}
	}

	fsHelper := NewFSHelper(h.db)

	// Pre-flight conflict check: if no conflict_policy set, scan for conflicts first
	// This applies to BOTH sync and async paths — return 409 before starting any work
	if req.ConflictPolicy == "" {
		conflicting := h.checkConflicts(req, fsHelper, opType)
		if len(conflicting) > 0 {
			c.JSON(http.StatusConflict, gin.H{
				"error":             "conflict",
				"conflicting_items": conflicting,
			})
			return
		}
	}

	if async {
		// Create async task and return task ID
		taskID := uuid.New().String()
		task := &AsyncTask{
			ID:        taskID,
			Type:      opType,
			Status:    "processing",
			Total:     len(req.SrcDirents),
			Done:      0,
			Failed:    0,
			CreatedAt: time.Now(),
		}
		h.tasks.mu.Lock()
		h.tasks.tasks[taskID] = task
		h.tasks.mu.Unlock()

		// Process in background
		go h.processAsyncBatch(orgID, userID, req, opType, task, fsHelper)

		c.JSON(http.StatusOK, gin.H{"task_id": taskID})
		return
	}

	// Synchronous operation (no conflicts, or explicit conflict policy)
	for _, direntName := range req.SrcDirents {
		srcPath := path.Join(req.SrcParentDir, direntName)
		err := h.processSingleItem(orgID, userID, req.SrcRepoID, req.DstRepoID, srcPath, req.DstParentDir, opType, fsHelper, req.ConflictPolicy)
		if err != nil {
			log.Printf("[BatchOperation] Failed to %s %s: %v", opType, srcPath, err)
			status, payload, _ := batchOperationErrorResponse(err, opType, direntName)
			c.JSON(status, payload)
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// checkConflicts checks for name conflicts without modifying any data
func (h *BatchOperationHandler) checkConflicts(req BatchRequest, fsHelper *FSHelper, opType string) []string {
	if isSameLocationMove(opType, req.SrcRepoID, req.DstRepoID, req.SrcParentDir, req.DstParentDir) {
		return nil
	}

	// Get destination directory entries
	var dstEntries []FSEntry
	if req.DstParentDir == "/" {
		rootFSID, _, err := fsHelper.GetRootFSID(req.DstRepoID)
		if err != nil {
			return nil
		}
		dstEntries, err = fsHelper.GetDirectoryEntries(req.DstRepoID, rootFSID)
		if err != nil {
			return nil
		}
	} else {
		dstResult, err := fsHelper.TraverseToPath(req.DstRepoID, req.DstParentDir)
		if err != nil || dstResult.TargetFSID == "" {
			return nil
		}
		dstEntries, err = fsHelper.GetDirectoryEntries(req.DstRepoID, dstResult.TargetFSID)
		if err != nil {
			return nil
		}
	}

	existingNames := make(map[string]bool, len(dstEntries))
	for _, entry := range dstEntries {
		existingNames[entry.Name] = true
	}

	var conflicting []string
	for _, direntName := range req.SrcDirents {
		if existingNames[direntName] {
			conflicting = append(conflicting, direntName)
		}
	}
	return conflicting
}

// processAsyncBatch processes items in background
func (h *BatchOperationHandler) processAsyncBatch(orgID, userID string, req BatchRequest, opType string, task *AsyncTask, fsHelper *FSHelper) {
	for _, direntName := range req.SrcDirents {
		srcPath := path.Join(req.SrcParentDir, direntName)
		err := h.processSingleItem(orgID, userID, req.SrcRepoID, req.DstRepoID, srcPath, req.DstParentDir, opType, fsHelper, req.ConflictPolicy)

		h.tasks.mu.Lock()
		if err != nil {
			task.Failed++
			_, _, task.FailedReason = batchOperationErrorResponse(err, opType, direntName)
			log.Printf("[AsyncBatch] Failed to %s %s: %v", opType, srcPath, err)
		} else {
			task.Done++
		}
		h.tasks.mu.Unlock()
	}

	h.tasks.mu.Lock()
	task.Status = "done"
	if task.Failed > 0 && task.Done == 0 {
		task.Status = "failed"
	}
	h.tasks.mu.Unlock()

	log.Printf("[AsyncBatch] Task %s completed: done=%d failed=%d", task.ID, task.Done, task.Failed)
}

// processSingleItem handles moving/copying a single item
func (h *BatchOperationHandler) processSingleItem(orgID, userID, srcRepoID, dstRepoID, srcPath, dstDir, opType string, fsHelper *FSHelper, conflictPolicy ...string) error {
	policy := ""
	if len(conflictPolicy) > 0 {
		policy = conflictPolicy[0]
	}
	srcPath = normalizePath(srcPath)
	dstDir = normalizePath(dstDir)
	log.Printf("[processSingleItem] %s: src_repo=%s src_path=%s dst_repo=%s dst_dir=%s",
		opType, srcRepoID, srcPath, dstRepoID, dstDir)

	itemName := path.Base(srcPath)
	originalItemName := itemName // Preserve for source removal in move+autorename
	srcParentPath := path.Dir(srcPath)
	if srcParentPath == "." {
		srcParentPath = "/"
	}
	if isSameLocationMove(opType, srcRepoID, dstRepoID, srcParentPath, dstDir) {
		srcSnapshot, err := fsHelper.GetLibraryHeadSnapshot(srcRepoID)
		if err != nil {
			return fmt.Errorf("source library not found: %w", err)
		}

		srcResult, err := fsHelper.TraverseToPathFromSnapshot(srcRepoID, srcSnapshot, srcPath)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrBatchSourceNotFound, err)
		}
		if srcResult.TargetEntry == nil {
			return ErrBatchSourceNotFound
		}

		log.Printf("[processSingleItem] no-op same-location move: %s", srcPath)
		return nil
	}
	if opType == "move" && srcRepoID == dstRepoID {
		return h.processSameRepoMove(orgID, userID, srcRepoID, srcPath, dstDir, fsHelper, policy)
	}
	var srcSize int64
	var srcFiles int64
	skipped := false
	sourceRemovalPublished := false
	replacedTagCleanupPath := ""
	replacedTagCleanupByPrefix := false

	err := retryLibraryHeadMutation("BatchOperationDestination", func() error {
		currentItemName := path.Base(srcPath)
		replacedTagCleanupPath = ""
		replacedTagCleanupByPrefix = false

		srcSnapshot, err := fsHelper.GetLibraryHeadSnapshot(srcRepoID)
		if err != nil {
			return fmt.Errorf("source library not found: %w", err)
		}

		srcResult, err := fsHelper.TraverseToPathFromSnapshot(srcRepoID, srcSnapshot, srcPath)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrBatchSourceNotFound, err)
		}
		if srcResult.TargetEntry == nil {
			return ErrBatchSourceNotFound
		}

		dstSnapshot, err := fsHelper.GetLibraryHeadSnapshot(dstRepoID)
		if err != nil {
			return fmt.Errorf("destination library not found: %w", err)
		}

		dstResult, err := fsHelper.TraverseToPathFromSnapshot(dstRepoID, dstSnapshot, dstDir)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrBatchDestinationNotFound, err)
		}

		var dstEntries []FSEntry
		if dstDir == "/" {
			dstEntries = dstResult.Entries
		} else {
			if dstResult.TargetFSID == "" {
				return ErrBatchDestinationNotFound
			}
			dstEntries, err = fsHelper.GetDirectoryEntries(dstRepoID, dstResult.TargetFSID)
			if err != nil {
				return fmt.Errorf("failed to read destination directory: %w", err)
			}
		}

		var replacedEntry *FSEntry
		if existing := FindEntryInList(dstEntries, currentItemName); existing != nil {
			switch policy {
			case "replace":
				ent := *existing
				replacedEntry = &ent
				dstEntries = RemoveEntryFromList(dstEntries, currentItemName)
			case "autorename":
				currentItemName = GenerateUniqueName(dstEntries, currentItemName)
			case "skip":
				skipped = true
				return nil
			default:
				return &ConflictError{ItemName: currentItemName}
			}
		}

		currentSrcSize, currentSrcFiles, err := fsEntryStats(fsHelper, srcRepoID, *srcResult.TargetEntry)
		if err != nil {
			return fmt.Errorf("failed to compute source size: %w", err)
		}

		var oldDstSize, oldDstFiles int64
		if replacedEntry != nil {
			oldDstSize, oldDstFiles, err = fsEntryStats(fsHelper, dstRepoID, *replacedEntry)
			if err != nil {
				return fmt.Errorf("failed to compute replaced entry size: %w", err)
			}
			// The replaced entry's old fs_object stays in fs_objects (reachable from
			// older commits) and its block references are released when GC sweeps it.
			// No explicit block-reference removal here.
		}

		dstDeltaBytes := currentSrcSize - oldDstSize
		dstDeltaFiles := currentSrcFiles - oldDstFiles
		quotaDeltaBytes := dstDeltaBytes
		if opType == "move" {
			quotaDeltaBytes -= currentSrcSize
		}
		if quotaDeltaBytes > 0 {
			if checker := traffic.GetChecker(); checker != nil {
				if st, _ := checker.CheckStorageQuota(orgID, userID, quotaDeltaBytes); !st.Allowed {
					return ErrStorageQuotaExceeded
				}
			}
		}

		entryFSID := srcResult.TargetEntry.ID
		var pendingCopiedFiles []*pendingPublishedFile
		cleanupPendingCopyPublish := func() {
			if cleanupErr := CleanupFailedPublishAttempt(h.db, orgID, dstRepoID, "", "", pendingCopiedFiles); cleanupErr != nil {
				log.Printf("[processSingleItem] WARNING: failed to clean up pending copied fs_objects before commit publish: %v", cleanupErr)
			}
		}
		if srcRepoID != dstRepoID {
			newFSID, copiedFiles, err := fsHelper.copyFSObjectToLibraryForPublish(srcRepoID, dstRepoID, srcResult.TargetEntry.ID)
			pendingCopiedFiles = copiedFiles
			if err != nil {
				cleanupPendingCopyPublish()
				return fmt.Errorf("failed to copy fs_objects to destination library: %w", err)
			}
			entryFSID = newFSID
		}

		newEntry := FSEntry{
			Name:  currentItemName,
			ID:    entryFSID,
			Mode:  srcResult.TargetEntry.Mode,
			MTime: time.Now().Unix(),
			Size:  srcResult.TargetEntry.Size,
		}
		newDstEntries := AddEntryToList(dstEntries, newEntry)

		newDstDirFSID, err := fsHelper.CreateDirectoryFSObject(dstRepoID, newDstEntries)
		if err != nil {
			cleanupPendingCopyPublish()
			return fmt.Errorf("failed to update destination directory: %w", err)
		}

		var newDstRootFSID string
		if dstDir == "/" {
			newDstRootFSID = newDstDirFSID
		} else {
			dstDirName := path.Base(dstDir)
			updatedParentEntries := make([]FSEntry, len(dstResult.Entries))
			for i, entry := range dstResult.Entries {
				if entry.Name == dstDirName {
					entry.ID = newDstDirFSID
				}
				updatedParentEntries[i] = entry
			}

			newParentFSID, err := fsHelper.CreateDirectoryFSObject(dstRepoID, updatedParentEntries)
			if err != nil {
				cleanupPendingCopyPublish()
				return fmt.Errorf("failed to update destination parent: %w", err)
			}

			newDstRootFSID, err = fsHelper.RebuildPathToRoot(dstRepoID, dstResult, newParentFSID)
			if err != nil {
				cleanupPendingCopyPublish()
				return fmt.Errorf("failed to rebuild destination path: %w", err)
			}
		}

		var dstDescription string
		if opType == "copy" {
			dstDescription = fmt.Sprintf("Copied \"%s\"", currentItemName)
		} else {
			dstDescription = fmt.Sprintf("Moved \"%s\"", currentItemName)
		}

		newDstCommitID, err := fsHelper.CreateCommit(dstRepoID, userID, newDstRootFSID, dstSnapshot.HeadCommitID, dstDescription)
		if err != nil {
			cleanupPendingCopyPublish()
			return fmt.Errorf("failed to create destination commit: %w", err)
		}
		if len(pendingCopiedFiles) > 0 {
			if err := fsHelper.stagePendingPublishedFiles(orgID, dstRepoID, newDstCommitID, pendingCopiedFiles); err != nil {
				if cleanupErr := CleanupFailedPublishAttempt(h.db, orgID, dstRepoID, newDstCommitID, newDstCommitID, pendingCopiedFiles); cleanupErr != nil {
					return errors.Join(
						fmt.Errorf("failed to stage destination publish-attempt refs for commit %s: %w", newDstCommitID, err),
						fmt.Errorf("cleanup failed publish commit %s: %w", newDstCommitID, cleanupErr),
					)
				}
				return fmt.Errorf("failed to stage destination publish-attempt refs for commit %s: %w", newDstCommitID, err)
			}
			if err := queuePendingPublishedFileRepairs(h.db, orgID, dstRepoID, newDstCommitID, pendingCopiedFiles); err != nil {
				cleanupErr := CleanupFailedPublishAttempt(h.db, orgID, dstRepoID, newDstCommitID, newDstCommitID, pendingCopiedFiles)
				clearErr := clearPendingPublishedFileRepairs(h.db, orgID, dstRepoID, newDstCommitID, pendingCopiedFiles)
				return errors.Join(
					fmt.Errorf("failed to queue durable publish repair for destination commit %s: %w", newDstCommitID, err),
					cleanupErr,
					clearErr,
				)
			}
		}

		if err := fsHelper.UpdateLibraryHeadFromSnapshot(dstSnapshot, dstRepoID, newDstCommitID, dstSnapshot.HeadCommitID); err != nil {
			if len(pendingCopiedFiles) > 0 && errors.Is(err, ErrLibraryHeadConflict) {
				if cleanupErr := CleanupFailedPublishAttempt(h.db, orgID, dstRepoID, newDstCommitID, newDstCommitID, pendingCopiedFiles); cleanupErr != nil {
					return fmt.Errorf("failed to clean up conflict copy publish attempt %s: %w", newDstCommitID, cleanupErr)
				}
				if clearErr := clearPendingPublishedFileRepairs(h.db, orgID, dstRepoID, newDstCommitID, pendingCopiedFiles); clearErr != nil {
					log.Printf("[processSingleItem] WARNING: failed to clear queued publish repairs for repo=%s commit=%s after head conflict: %v", dstRepoID, newDstCommitID, clearErr)
				}
			}
			return fmt.Errorf("failed to update destination library: %w", err)
		}
		if len(pendingCopiedFiles) > 0 {
			if ownerErr := clearPendingPublishedFileOwnersFn(h.db, dstRepoID, pendingCopiedFiles); ownerErr != nil {
				log.Printf("[processSingleItem] WARNING: published repo=%s commit=%s but failed to clear pending copied fs_object owners: %v", dstRepoID, newDstCommitID, ownerErr)
			}
			if err := fsHelper.promotePendingPublishedFiles(orgID, dstRepoID, newDstCommitID, pendingCopiedFiles); err != nil {
				log.Printf("[processSingleItem] WARNING: head updated for repo=%s commit=%s but failed to promote copied block references: %v", dstRepoID, newDstCommitID, err)
				schedulePendingPublishedFileRepairs(h.db, orgID, dstRepoID, newDstCommitID, pendingCopiedFiles, "BatchOperationDestination")
			} else if clearErr := clearPendingPublishedFileRepairs(h.db, orgID, dstRepoID, newDstCommitID, pendingCopiedFiles); clearErr != nil {
				log.Printf("[processSingleItem] WARNING: published repo=%s commit=%s but failed to clear queued publish repairs: %v", dstRepoID, newDstCommitID, clearErr)
			}
		}

		replacedTagCleanupPath, replacedTagCleanupByPrefix = replacedDestinationTagCleanup(dstDir, currentItemName, replacedEntry)

		if err := traffic.AdjustStorageCountersByDeltaSync(h.db, orgID, userID, dstRepoID, dstDeltaBytes, dstDeltaFiles); err != nil {
			log.Printf("[processSingleItem] failed to apply destination storage counter delta for %s -> %s/%s: %v", srcPath, dstRepoID, dstDir, err)
		}

		itemName = currentItemName
		srcSize = currentSrcSize
		srcFiles = currentSrcFiles
		return nil
	})
	if err != nil {
		return err
	}
	if skipped {
		return nil
	}
	if replacedTagCleanupPath != "" {
		if replacedTagCleanupByPrefix {
			go CleanupFileTagsByPrefix(h.db, dstRepoID, replacedTagCleanupPath)
		} else {
			go CleanupFileTagsByPath(h.db, dstRepoID, replacedTagCleanupPath)
		}
	}

	// For move operation, remove from source
	if opType == "move" {
		err = retryLibraryHeadMutation("BatchOperationSourceMove", func() error {
			srcSnapshot, err := fsHelper.GetLibraryHeadSnapshot(srcRepoID)
			if err != nil {
				return fmt.Errorf("failed to get source head: %w", err)
			}

			srcResult, err := fsHelper.TraverseToPathFromSnapshot(srcRepoID, srcSnapshot, srcPath)
			if shouldSkipSourceRemovalAfterMove(srcResult, err) {
				return nil
			}

			srcParentResult, err := fsHelper.TraverseToPathFromSnapshot(srcRepoID, srcSnapshot, srcParentPath)
			if err != nil {
				return fmt.Errorf("failed to traverse source parent: %w", err)
			}

			var srcParentEntries []FSEntry
			if srcParentPath == "/" {
				srcParentEntries = srcParentResult.Entries
			} else {
				if srcParentResult.TargetFSID == "" {
					return fmt.Errorf("source parent directory not found")
				}
				srcParentEntries, err = fsHelper.GetDirectoryEntries(srcRepoID, srcParentResult.TargetFSID)
				if err != nil {
					return fmt.Errorf("failed to read source parent directory: %w", err)
				}
			}

			newSrcEntries := RemoveEntryFromList(srcParentEntries, originalItemName)
			newSrcParentFSID, err := fsHelper.CreateDirectoryFSObject(srcRepoID, newSrcEntries)
			if err != nil {
				return fmt.Errorf("failed to update source directory: %w", err)
			}

			var newSrcRootFSID string
			if srcParentPath == "/" {
				newSrcRootFSID = newSrcParentFSID
			} else {
				srcParentDirName := path.Base(srcParentPath)
				updatedGrandparentEntries := make([]FSEntry, len(srcParentResult.Entries))
				for i, entry := range srcParentResult.Entries {
					if entry.Name == srcParentDirName {
						entry.ID = newSrcParentFSID
					}
					updatedGrandparentEntries[i] = entry
				}

				newGrandparentFSID, err := fsHelper.CreateDirectoryFSObject(srcRepoID, updatedGrandparentEntries)
				if err != nil {
					return fmt.Errorf("failed to update source grandparent: %w", err)
				}

				newSrcRootFSID, err = fsHelper.RebuildPathToRoot(srcRepoID, srcParentResult, newGrandparentFSID)
				if err != nil {
					return fmt.Errorf("failed to rebuild source path: %w", err)
				}
			}

			srcDescription := fmt.Sprintf("Removed \"%s\"", originalItemName)
			newSrcCommitID, err := fsHelper.CreateCommit(srcRepoID, userID, newSrcRootFSID, srcSnapshot.HeadCommitID, srcDescription)
			if err != nil {
				return fmt.Errorf("failed to create source commit: %w", err)
			}

			if err := fsHelper.UpdateLibraryHeadFromSnapshot(srcSnapshot, srcRepoID, newSrcCommitID, srcSnapshot.HeadCommitID); err != nil {
				return fmt.Errorf("failed to update source library: %w", err)
			}
			sourceRemovalPublished = true
			return nil
		})
		if err != nil {
			return err
		}
		if !sourceRemovalPublished {
			return nil
		}

		// Decrement the source library's storage counter by the moved tree.
		// For move cross-repo same-owner this nets out against the destination
		// increment at the org/user/platform scopes; per-library counters still
		// need both halves so per-lib views stay consistent.
		if err := traffic.AdjustStorageCountersByDeltaSync(h.db, orgID, userID, srcRepoID, -srcSize, -srcFiles); err != nil {
			log.Printf("[processSingleItem] failed to apply source storage counter delta for %s: %v", srcPath, err)
		}

		// Clean up file tags for the moved item (async, non-blocking)
		// After a move, the file no longer exists at the source path
		go CleanupFileTagsByPath(h.db, srcRepoID, srcPath)
	}

	return nil
}

func (h *BatchOperationHandler) processSameRepoMove(orgID, userID, repoID, srcPath, dstDir string, fsHelper *FSHelper, policy string) error {
	originalItemName := path.Base(srcPath)
	srcParentPath := path.Dir(srcPath)
	if srcParentPath == "." {
		srcParentPath = "/"
	}

	var itemName string
	var replacedTagCleanupPath string
	var replacedTagCleanupByPrefix bool
	var movedTagOldPath string
	var movedTagNewPath string
	var movedTagByPrefix bool
	var replacedSize int64
	var replacedFiles int64
	skipped := false

	err := retryLibraryHeadMutation("BatchOperationSameRepoMove", func() error {
		currentItemName := originalItemName
		replacedTagCleanupPath = ""
		replacedTagCleanupByPrefix = false
		movedTagOldPath = ""
		movedTagNewPath = ""
		movedTagByPrefix = false
		replacedSize = 0
		replacedFiles = 0
		skipped = false

		snapshot, err := fsHelper.GetLibraryHeadSnapshot(repoID)
		if err != nil {
			return fmt.Errorf("source library not found: %w", err)
		}

		srcResult, err := fsHelper.TraverseToPathFromSnapshot(repoID, snapshot, srcPath)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrBatchSourceNotFound, err)
		}
		if srcResult.TargetEntry == nil {
			return ErrBatchSourceNotFound
		}
		if srcResult.TargetEntry.Mode == ModeDir || srcResult.TargetEntry.Mode&0170000 == 040000 {
			if isPathWithin(dstDir, srcPath) {
				return fmt.Errorf("cannot move directory into itself")
			}
		}

		movedEntry := *srcResult.TargetEntry
		movedEntry.MTime = time.Now().Unix()

		rootAfterRemoval, err := updateDirectoryAtPathFromRoot(fsHelper, repoID, snapshot.RootFSID, srcParentPath, func(entries []FSEntry) ([]FSEntry, error) {
			if FindEntryInList(entries, originalItemName) == nil {
				return nil, ErrBatchSourceNotFound
			}
			return RemoveEntryFromList(entries, originalItemName), nil
		})
		if err != nil {
			return err
		}

		newRootFSID, err := updateDirectoryAtPathFromRoot(fsHelper, repoID, rootAfterRemoval, dstDir, func(entries []FSEntry) ([]FSEntry, error) {
			var replacedEntry *FSEntry
			if existing := FindEntryInList(entries, currentItemName); existing != nil {
				switch policy {
				case "replace":
					ent := *existing
					replacedEntry = &ent
					entries = RemoveEntryFromList(entries, currentItemName)
				case "autorename":
					currentItemName = GenerateUniqueName(entries, currentItemName)
				case "skip":
					skipped = true
					return entries, nil
				default:
					return nil, &ConflictError{ItemName: currentItemName}
				}
			}

			if replacedEntry != nil {
				var err error
				replacedSize, replacedFiles, err = fsEntryStats(fsHelper, repoID, *replacedEntry)
				if err != nil {
					return nil, fmt.Errorf("failed to compute replaced entry size: %w", err)
				}
				// The replaced entry's old fs_object stays referenced from older
				// commits until GC sweeps it; no block-reference removal here.
				replacedTagCleanupPath, replacedTagCleanupByPrefix = replacedDestinationTagCleanup(dstDir, currentItemName, replacedEntry)
			}

			movedEntry.Name = currentItemName
			movedTagOldPath, movedTagNewPath, movedTagByPrefix = movedItemTagMutation(srcPath, dstDir, currentItemName, &movedEntry)
			return AddEntryToList(entries, movedEntry), nil
		})
		if err != nil {
			return err
		}
		if skipped {
			return nil
		}

		description := fmt.Sprintf("Moved \"%s\"", currentItemName)
		newCommitID, err := fsHelper.CreateCommit(repoID, userID, newRootFSID, snapshot.HeadCommitID, description)
		if err != nil {
			return fmt.Errorf("failed to create move commit: %w", err)
		}

		if err := fsHelper.UpdateLibraryHeadFromSnapshot(snapshot, repoID, newCommitID, snapshot.HeadCommitID); err != nil {
			return fmt.Errorf("failed to update library: %w", err)
		}

		itemName = currentItemName
		return nil
	})
	if err != nil {
		return err
	}
	if skipped {
		return nil
	}
	go runSameRepoMoveTagMutation(h.db, repoID, movedTagOldPath, movedTagNewPath, movedTagByPrefix, replacedTagCleanupPath, replacedTagCleanupByPrefix)
	if replacedSize != 0 || replacedFiles != 0 {
		if err := traffic.AdjustStorageCountersByDeltaSync(h.db, orgID, userID, repoID, -replacedSize, -replacedFiles); err != nil {
			log.Printf("[processSameRepoMove] failed to apply replaced destination storage counter delta for %s -> %s/%s: %v", srcPath, repoID, dstDir, err)
		}
	}
	log.Printf("[processSameRepoMove] moved %s to %s as %s", srcPath, dstDir, itemName)
	return nil
}

func updateDirectoryAtPathFromRoot(fsHelper *FSHelper, repoID, rootFSID, dirPath string, update func([]FSEntry) ([]FSEntry, error)) (string, error) {
	dirPath = normalizePath(dirPath)
	if dirPath == "/" {
		entries, err := fsHelper.GetDirectoryEntries(repoID, rootFSID)
		if err != nil {
			return "", err
		}
		updatedEntries, err := update(entries)
		if err != nil {
			return "", err
		}
		return fsHelper.CreateDirectoryFSObject(repoID, updatedEntries)
	}

	parts := strings.Split(strings.Trim(dirPath, "/"), "/")
	var rebuild func(string, int) (string, error)
	rebuild = func(currentFSID string, depth int) (string, error) {
		entries, err := fsHelper.GetDirectoryEntries(repoID, currentFSID)
		if err != nil {
			return "", err
		}
		if depth == len(parts) {
			updatedEntries, err := update(entries)
			if err != nil {
				return "", err
			}
			return fsHelper.CreateDirectoryFSObject(repoID, updatedEntries)
		}

		part := parts[depth]
		for i, entry := range entries {
			if entry.Name != part {
				continue
			}
			if entry.Mode != ModeDir && entry.Mode&0170000 != 040000 {
				return "", ErrBatchDestinationNotFound
			}
			newChildFSID, err := rebuild(entry.ID, depth+1)
			if err != nil {
				return "", err
			}
			entries[i].ID = newChildFSID
			return fsHelper.CreateDirectoryFSObject(repoID, entries)
		}
		return "", ErrBatchDestinationNotFound
	}

	return rebuild(rootFSID, 0)
}

// GetTaskProgress returns the progress of an async task
func (h *BatchOperationHandler) GetTaskProgress(c *gin.Context) {
	taskID := c.Query("task_id")
	if taskID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "task_id is required"})
		return
	}

	h.tasks.mu.RLock()
	task, exists := h.tasks.tasks[taskID]
	h.tasks.mu.RUnlock()

	if !exists {
		c.JSON(http.StatusNotFound, gin.H{"error": "task not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"task_id":       task.ID,
		"done":          task.Status == "done",
		"successful":    task.Done,
		"failed":        task.Failed,
		"total":         task.Total,
		"failed_reason": task.FailedReason,
	})
}

// checkWritePermission checks if user has write permission based on role
func (h *BatchOperationHandler) checkWritePermission(c *gin.Context, orgID, userID string) bool {
	if h.permMiddleware == nil {
		return true
	}

	userRole, err := h.permMiddleware.GetUserOrgRole(orgID, userID)
	if err != nil {
		log.Printf("[BatchOperation] Failed to get user role: %v", err)
		return false // On error, deny access (fail-closed)
	}

	if !middleware.HasRequiredOrgRole(userRole, middleware.RoleUser) {
		log.Printf("[BatchOperation] Write access denied for user %s with role %s", userID, userRole)
		c.JSON(http.StatusForbidden, gin.H{
			"error": "insufficient permissions: write operations require 'user' role or higher",
		})
		return false
	}

	return true
}

// checkDecryptSession checks if library is encrypted and user has decrypt session
func (h *BatchOperationHandler) checkDecryptSession(c *gin.Context, orgID, userID, repoID string) bool {
	if h.db == nil {
		return true
	}

	// Check if library is encrypted
	var encrypted bool
	err := h.db.Session().Query(`
		SELECT encrypted FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, repoID).Scan(&encrypted)
	if err != nil {
		return true // Library not found, let the caller handle it
	}

	if !encrypted {
		return true
	}

	// Library is encrypted - require active decrypt session
	if !GetDecryptSessions().IsUnlocked(userID, repoID) {
		log.Printf("[BatchOperation] Blocked access to encrypted library %s by user %s", repoID, userID)
		c.JSON(http.StatusForbidden, gin.H{
			"error":            "Library is encrypted",
			"error_msg":        "This library is encrypted. Please provide the password to unlock it.",
			"lib_need_decrypt": true,
		})
		return false
	}

	return true
}

// Note: RemoveEntryFromList is defined in fs_helpers.go
