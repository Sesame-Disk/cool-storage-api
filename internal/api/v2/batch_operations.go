package v2

import (
	"fmt"
	"log"
	"net/http"
	"path"
	"sync"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/middleware"
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
		conflicting := h.checkConflicts(req, fsHelper)
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
			if _, ok := err.(*ConflictError); ok {
				c.JSON(http.StatusConflict, gin.H{
					"error":             "conflict",
					"conflicting_items": []string{direntName},
				})
				return
			}
			log.Printf("[BatchOperation] Failed to %s %s: %v", opType, srcPath, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to %s %s: %v", opType, direntName, err)})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// checkConflicts checks for name conflicts without modifying any data
func (h *BatchOperationHandler) checkConflicts(req BatchRequest, fsHelper *FSHelper) []string {
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
			task.FailedReason = err.Error()
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
	log.Printf("[processSingleItem] %s: src_repo=%s src_path=%s dst_repo=%s dst_dir=%s",
		opType, srcRepoID, srcPath, dstRepoID, dstDir)

	// Get source head commit
	srcHeadCommitID, err := fsHelper.GetHeadCommitID(srcRepoID)
	if err != nil {
		return fmt.Errorf("source library not found: %w", err)
	}

	// Traverse to source item
	srcResult, err := fsHelper.TraverseToPath(srcRepoID, srcPath)
	if err != nil {
		return fmt.Errorf("source path not found: %w", err)
	}

	if srcResult.TargetEntry == nil {
		return fmt.Errorf("source item not found")
	}

	itemName := path.Base(srcPath)
	originalItemName := itemName // Preserve for source removal in move+autorename
	srcParentPath := path.Dir(srcPath)
	if srcParentPath == "." {
		srcParentPath = "/"
	}

	// Get destination head commit
	dstHeadCommitID, err := fsHelper.GetHeadCommitID(dstRepoID)
	if err != nil {
		return fmt.Errorf("destination library not found: %w", err)
	}

	// Traverse to destination directory
	dstResult, err := fsHelper.TraverseToPath(dstRepoID, dstDir)
	if err != nil {
		return fmt.Errorf("destination path not found: %w", err)
	}

	// Get the actual entries inside the destination directory
	// Note: dstResult.Entries contains the PARENT's entries, not the destination folder's contents
	var dstEntries []FSEntry
	if dstDir == "/" {
		// If destination is root, dstResult.Entries is already the root's contents
		dstEntries = dstResult.Entries
	} else {
		// Otherwise, get the contents of the destination directory
		if dstResult.TargetFSID == "" {
			return fmt.Errorf("destination directory not found")
		}
		dstEntries, err = fsHelper.GetDirectoryEntries(dstRepoID, dstResult.TargetFSID)
		if err != nil {
			return fmt.Errorf("failed to read destination directory: %w", err)
		}
	}

	// Check if item with same name exists in destination
	hasConflict := false
	for _, entry := range dstEntries {
		if entry.Name == itemName {
			hasConflict = true
			break
		}
	}

	if hasConflict {
		switch policy {
		case "replace":
			// Remove existing entry before adding
			dstEntries = RemoveEntryFromList(dstEntries, itemName)
		case "autorename":
			// Generate unique name
			itemName = GenerateUniqueName(dstEntries, itemName)
		case "skip":
			// Silently skip this item
			return nil
		default:
			return &ConflictError{ItemName: itemName}
		}
	}

	// For cross-library operations, we need to copy fs_objects to the destination library
	// because fs_objects are keyed by (library_id, fs_id)
	entryFSID := srcResult.TargetEntry.ID
	if srcRepoID != dstRepoID {
		newFSID, err := fsHelper.CopyFSObjectToLibrary(srcRepoID, dstRepoID, srcResult.TargetEntry.ID)
		if err != nil {
			return fmt.Errorf("failed to copy fs_objects to destination library: %w", err)
		}
		entryFSID = newFSID
	}

	// Add source entry to destination
	newEntry := FSEntry{
		Name:  itemName,
		ID:    entryFSID,
		Mode:  srcResult.TargetEntry.Mode,
		MTime: time.Now().Unix(),
		Size:  srcResult.TargetEntry.Size,
	}
	newDstEntries := AddEntryToList(dstEntries, newEntry)

	// Create new fs_object for the destination directory with new contents
	newDstDirFSID, err := fsHelper.CreateDirectoryFSObject(dstRepoID, newDstEntries)
	if err != nil {
		return fmt.Errorf("failed to update destination directory: %w", err)
	}

	// Rebuild path to root for destination
	var newDstRootFSID string
	if dstDir == "/" {
		// Destination is root - the new fs_id IS the new root
		newDstRootFSID = newDstDirFSID
	} else {
		// Destination is a subdirectory - need to update parent to point to new destination fs_id
		// dstResult.Entries contains the parent's entries (which includes the dest folder)
		dstDirName := path.Base(dstDir)
		updatedParentEntries := make([]FSEntry, len(dstResult.Entries))
		for i, entry := range dstResult.Entries {
			if entry.Name == dstDirName {
				entry.ID = newDstDirFSID // Update to point to new destination directory
			}
			updatedParentEntries[i] = entry
		}

		// Create new fs_object for the parent directory
		newParentFSID, err := fsHelper.CreateDirectoryFSObject(dstRepoID, updatedParentEntries)
		if err != nil {
			return fmt.Errorf("failed to update destination parent: %w", err)
		}

		// Rebuild from parent to root using the original traversal result
		newDstRootFSID, err = fsHelper.RebuildPathToRoot(dstRepoID, dstResult, newParentFSID)
		if err != nil {
			return fmt.Errorf("failed to rebuild destination path: %w", err)
		}
	}

	// Create commit for destination
	var dstDescription string
	if opType == "copy" {
		dstDescription = fmt.Sprintf("Copied \"%s\"", itemName)
	} else {
		dstDescription = fmt.Sprintf("Moved \"%s\"", itemName)
	}

	newDstCommitID, err := fsHelper.CreateCommit(dstRepoID, userID, newDstRootFSID, dstHeadCommitID, dstDescription)
	if err != nil {
		return fmt.Errorf("failed to create destination commit: %w", err)
	}

	// Update destination library head
	if err := fsHelper.UpdateLibraryHead(orgID, dstRepoID, newDstCommitID); err != nil {
		return fmt.Errorf("failed to update destination library: %w", err)
	}

	// For move operation, remove from source
	if opType == "move" {
		// Don't remove from source if same location
		if srcRepoID == dstRepoID && srcParentPath == dstDir {
			return nil
		}

		// Get fresh source state (might have changed)
		srcResult, err = fsHelper.TraverseToPath(srcRepoID, srcPath)
		if err != nil {
			// Already moved, that's fine
			return nil
		}

		// Re-traverse to get parent path result
		srcParentResult, err := fsHelper.TraverseToPath(srcRepoID, srcParentPath)
		if err != nil {
			return fmt.Errorf("failed to traverse source parent: %w", err)
		}

		// Get the actual entries inside the source parent directory
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

		// Remove the entry from parent (use original name, not the potentially-renamed one)
		newSrcEntries := RemoveEntryFromList(srcParentEntries, originalItemName)

		// Create new fs_object for source parent
		newSrcParentFSID, err := fsHelper.CreateDirectoryFSObject(srcRepoID, newSrcEntries)
		if err != nil {
			return fmt.Errorf("failed to update source directory: %w", err)
		}

		// Get fresh head commit for source (destination update might have changed it if same repo)
		srcHeadCommitID, err = fsHelper.GetHeadCommitID(srcRepoID)
		if err != nil {
			return fmt.Errorf("failed to get source head: %w", err)
		}

		// Rebuild path to root for source
		var newSrcRootFSID string
		if srcParentPath == "/" {
			newSrcRootFSID = newSrcParentFSID
		} else {
			// Need to update grandparent to point to new parent
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

		// Create commit for source
		srcDescription := fmt.Sprintf("Removed \"%s\"", originalItemName)
		newSrcCommitID, err := fsHelper.CreateCommit(srcRepoID, userID, newSrcRootFSID, srcHeadCommitID, srcDescription)
		if err != nil {
			return fmt.Errorf("failed to create source commit: %w", err)
		}

		// Update source library head
		if err := fsHelper.UpdateLibraryHead(orgID, srcRepoID, newSrcCommitID); err != nil {
			return fmt.Errorf("failed to update source library: %w", err)
		}

		// Clean up file tags for the moved item (async, non-blocking)
		// After a move, the file no longer exists at the source path
		go CleanupFileTagsByPath(h.db, srcRepoID, srcPath)
	}

	return nil
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
