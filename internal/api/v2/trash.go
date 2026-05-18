package v2

import (
	"fmt"
	"log"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/middleware"
	"github.com/Sesame-Disk/sesamefs/internal/traffic"
	"github.com/gin-gonic/gin"
)

// CommitGCEnqueuer enqueues specific commits for GC deletion.
type CommitGCEnqueuer interface {
	EnqueueCommits(orgID, libraryID string, commitIDs []string)
}

// TrashHandler handles file/folder trash (recycle bin) endpoints
type TrashHandler struct {
	db             *db.DB
	permMiddleware *middleware.PermissionMiddleware
}

// NewTrashHandler creates a new TrashHandler
func NewTrashHandler(database *db.DB) *TrashHandler {
	return &TrashHandler{
		db:             database,
		permMiddleware: middleware.NewPermissionMiddleware(database),
	}
}

// RegisterTrashRoutes registers trash-related routes
func RegisterTrashRoutes(rg *gin.RouterGroup, database *db.DB) {
	h := NewTrashHandler(database)

	repos := rg.Group("/repos/:repo_id")
	{
		// File/folder trash (recycle bin)
		repos.GET("/trash", h.GetRepoFolderTrash)
		repos.GET("/trash/", h.GetRepoFolderTrash)
		repos.DELETE("/trash", h.CleanRepoTrash)
		repos.DELETE("/trash/", h.CleanRepoTrash)

		// List directory from a specific commit (for browsing deleted folders)
		repos.GET("/commit/:commit_id/dir", h.ListCommitDir)
		repos.GET("/commit/:commit_id/dir/", h.ListCommitDir)

		// Restore from trash
		repos.POST("/file/restore", h.RestoreTrashItem)
		repos.POST("/file/restore/", h.RestoreTrashItem)
		repos.POST("/dir/restore", h.RestoreTrashItem)
		repos.POST("/dir/restore/", h.RestoreTrashItem)

		// Batch restore from trash (used by frontend to restore multiple items at once)
		repos.POST("/trash/revert-dirents", h.RevertDirents)
		repos.POST("/trash/revert-dirents/", h.RevertDirents)
	}
}

// TrashItem represents a deleted file or folder
type TrashItem struct {
	ObjName     string `json:"obj_name"`
	ObjID       string `json:"obj_id"`
	IsDir       bool   `json:"is_dir"`
	ParentDir   string `json:"parent_dir"`
	DeletedTime string `json:"deleted_time"`
	CommitID    string `json:"commit_id"`
	Size        int64  `json:"size"`
}

// pathEntry represents an entry with its parent directory path for recursive scanning
type pathEntry struct {
	ParentDir string // The directory containing this entry (e.g., "/testUp001012/")
	Entry     FSEntry
}

// collectAllEntries recursively collects all entries from a directory tree
func (h *TrashHandler) collectAllEntries(fsHelper *FSHelper, repoID, fsID, currentDir string, maxDepth int) []pathEntry {
	if maxDepth <= 0 {
		return nil
	}

	entries, err := fsHelper.GetDirectoryEntries(repoID, fsID)
	if err != nil {
		return nil
	}

	// Ensure currentDir ends with /
	if currentDir == "" {
		currentDir = "/"
	}
	if !strings.HasSuffix(currentDir, "/") {
		currentDir += "/"
	}

	var result []pathEntry
	for _, entry := range entries {
		// Store the parent directory (currentDir), not the full path
		result = append(result, pathEntry{ParentDir: currentDir, Entry: entry})

		// Recursively collect from subdirectories
		if entry.Mode == ModeDir || entry.Mode&0170000 == 040000 {
			subDir := currentDir + entry.Name
			subEntries := h.collectAllEntries(fsHelper, repoID, entry.ID, subDir, maxDepth-1)
			result = append(result, subEntries...)
		}
	}

	return result
}

// GetRepoFolderTrash returns deleted files/folders in a library or subdirectory
// GET /api/v2.1/repos/:repo_id/trash/?parent_dir=/path&scan_stat=cursor
// Also handles: GET /api2/repos/:repo_id/trash/?path=/path&scan_stat=cursor
func (h *TrashHandler) GetRepoFolderTrash(c *gin.Context) {
	repoID := c.Param("repo_id")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	// Support both parameter names (v2.1 uses parent_dir, api2 uses path)
	parentDir := c.DefaultQuery("parent_dir", c.DefaultQuery("path", "/"))
	scanStat := c.Query("scan_stat")

	if parentDir == "" {
		parentDir = "/"
	}
	parentDir = normalizePath(parentDir)
	// Ensure parent_dir ends with /
	if parentDir != "/" && parentDir[len(parentDir)-1] != '/' {
		parentDir += "/"
	}

	// Permission check
	if h.permMiddleware != nil {
		hasAccess, err := h.permMiddleware.HasLibraryAccessCtx(c, orgID, userID, repoID, middleware.PermissionR)
		if err != nil || !hasAccess {
			c.JSON(http.StatusForbidden, gin.H{"error_msg": "permission denied"})
			return
		}
	}

	if h.db == nil {
		c.JSON(http.StatusOK, gin.H{"data": []TrashItem{}, "more": false, "scan_stat": ""})
		return
	}

	fsHelper := NewFSHelper(h.db)

	// Query all commits for this library (most recent first)
	iter := h.db.Session().Query(`
		SELECT commit_id, root_fs_id, created_at
		FROM commits WHERE library_id = ?
		LIMIT 200
	`, repoID).Iter()

	type commitInfo struct {
		CommitID  string
		RootFSID  string
		CreatedAt time.Time
	}

	var commits []commitInfo
	var commitID, rootFSID string
	var createdAt time.Time

	for iter.Scan(&commitID, &rootFSID, &createdAt) {
		commits = append(commits, commitInfo{CommitID: commitID, RootFSID: rootFSID, CreatedAt: createdAt})
	}
	iter.Close()

	// Sort commits by time descending (newest first)
	sort.Slice(commits, func(i, j int) bool {
		return commits[i].CreatedAt.After(commits[j].CreatedAt)
	})

	// Handle scan_stat pagination — skip commits before cursor
	startIdx := 0
	if scanStat != "" {
		if idx, err := strconv.Atoi(scanStat); err == nil && idx > 0 && idx < len(commits) {
			startIdx = idx
		}
	}

	// For root path, scan recursively to find all deleted items
	// For subdirectories, only scan that specific directory
	scanRecursively := parentDir == "/"

	// Track which items exist in HEAD (newest commit)
	// Key format: "path:name" for recursive, or just "name" for single dir
	headEntries := make(map[string]bool)
	deletedItems := []TrashItem{}
	seenDeleted := make(map[string]bool) // avoid duplicates: key: "path:obj_id"

	maxItems := 100

	for i, commit := range commits {
		if i < startIdx {
			continue
		}

		var entries []pathEntry

		if scanRecursively {
			// Recursively collect all entries from the entire tree
			entries = h.collectAllEntries(fsHelper, repoID, commit.RootFSID, "", 10)
		} else {
			// Only scan the specific directory
			result, err := fsHelper.TraverseToPathFromRoot(repoID, commit.RootFSID, parentDir)
			if err != nil {
				continue // parent_dir doesn't exist in this commit
			}

			var dirEntries []FSEntry
			if result.TargetFSID != "" {
				dirEntries, err = fsHelper.GetDirectoryEntries(repoID, result.TargetFSID)
				if err != nil {
					continue
				}
			} else {
				dirEntries = result.Entries
			}

			for _, entry := range dirEntries {
				entries = append(entries, pathEntry{ParentDir: parentDir, Entry: entry})
			}
		}

		if i == startIdx {
			// First commit after startIdx = HEAD — collect existing items
			for _, pe := range entries {
				key := pe.ParentDir + ":" + pe.Entry.Name
				headEntries[key] = true
			}
			continue
		}

		// For older commits, find items that existed then but not in HEAD
		for _, pe := range entries {
			key := pe.ParentDir + ":" + pe.Entry.Name
			if headEntries[key] {
				continue // still exists in HEAD, not deleted
			}

			dedupeKey := pe.ParentDir + ":" + pe.Entry.Name
			if seenDeleted[dedupeKey] {
				continue // already reported this item (keep most recent version)
			}
			seenDeleted[dedupeKey] = true

			item := TrashItem{
				ObjName:     pe.Entry.Name,
				ObjID:       pe.Entry.ID,
				IsDir:       pe.Entry.Mode == ModeDir || pe.Entry.Mode&0170000 == 040000,
				ParentDir:   pe.ParentDir,
				DeletedTime: commit.CreatedAt.Format(time.RFC3339),
				CommitID:    commit.CommitID,
				Size:        pe.Entry.Size,
			}
			deletedItems = append(deletedItems, item)
		}

		if len(deletedItems) >= maxItems {
			break
		}
	}

	// When scanning recursively, filter out items whose parent directory
	// is itself a deleted directory. If a folder was deleted, we only want
	// to show the folder, not each of its children individually.
	if scanRecursively && len(deletedItems) > 0 {
		// Build a set of deleted directory paths (e.g., "/test 25/")
		deletedDirs := make(map[string]bool)
		for _, item := range deletedItems {
			if item.IsDir {
				dirPath := item.ParentDir + item.ObjName + "/"
				deletedDirs[dirPath] = true
			}
		}

		// Filter: keep only items whose parent is NOT inside a deleted directory
		if len(deletedDirs) > 0 {
			filtered := make([]TrashItem, 0, len(deletedItems))
			for _, item := range deletedItems {
				isInsideDeletedDir := false
				// Check if this item's parent_dir starts with any deleted dir path
				for ddir := range deletedDirs {
					if strings.HasPrefix(item.ParentDir, ddir) {
						isInsideDeletedDir = true
						break
					}
				}
				if !isInsideDeletedDir {
					filtered = append(filtered, item)
				}
			}
			deletedItems = filtered
		}
	}

	// Determine pagination
	more := len(deletedItems) > maxItems
	nextScanStat := ""
	if more {
		deletedItems = deletedItems[:maxItems]
		// Find index of last processed commit for cursor
		for i, commit := range commits {
			if commit.CommitID == deletedItems[len(deletedItems)-1].CommitID {
				nextScanStat = strconv.Itoa(i + 1)
				break
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"data":      deletedItems,
		"more":      more,
		"scan_stat": nextScanStat,
	})
}

// RestoreTrashItem restores a deleted file or folder from trash
// POST /api/v2.1/repos/:repo_id/file/restore/ or /dir/restore/
// JSON body: {"commit_id":"xxx","p":"/path/to/file"}
func (h *TrashHandler) RestoreTrashItem(c *gin.Context) {
	repoID := c.Param("repo_id")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")
	var restoreReq struct {
		CommitID string `json:"commit_id"`
		Path     string `json:"p"`
	}
	_ = c.ShouldBindJSON(&restoreReq)
	commitID := restoreReq.CommitID
	filePath := restoreReq.Path

	if commitID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error_msg": "commit_id is required"})
		return
	}
	if filePath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error_msg": "path is required"})
		return
	}

	filePath = normalizePath(filePath)

	// Permission check — need write access
	if h.permMiddleware != nil {
		hasAccess, err := h.permMiddleware.HasLibraryAccessCtx(c, orgID, userID, repoID, middleware.PermissionRW)
		if err != nil || !hasAccess {
			c.JSON(http.StatusForbidden, gin.H{"error_msg": "permission denied"})
			return
		}
	}

	if h.db == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error_msg": "database not available"})
		return
	}

	fsHelper := NewFSHelper(h.db)

	// Get the root_fs_id from the target commit
	var oldRootFSID string
	err := h.db.Session().Query(`
		SELECT root_fs_id FROM commits WHERE library_id = ? AND commit_id = ?
	`, repoID, commitID).Scan(&oldRootFSID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error_msg": "commit not found"})
		return
	}

	// Traverse the old commit to find the deleted item
	oldResult, err := fsHelper.TraverseToPathFromRoot(repoID, oldRootFSID, filePath)
	if err != nil || oldResult.TargetEntry == nil {
		c.JSON(http.StatusNotFound, gin.H{"error_msg": "item not found in specified commit"})
		return
	}

	oldEntry := *oldResult.TargetEntry

	// Get current HEAD commit
	headCommitID, err := fsHelper.GetHeadCommitID(repoID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error_msg": "library not found"})
		return
	}

	// Determine parent directory path
	parentPath := path.Dir(filePath)
	if parentPath == "." {
		parentPath = "/"
	}

	// Traverse current HEAD to the parent directory
	result, err := fsHelper.TraverseToPath(repoID, parentPath)
	if err != nil {
		// Parent directory doesn't exist in HEAD — we need to recreate the path
		// For simplicity, restore to root if parent doesn't exist
		result, err = fsHelper.TraverseToPath(repoID, "/")
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error_msg": "failed to traverse library"})
			return
		}
	}

	// Add the restored item to the parent directory
	fileName := path.Base(filePath)
	var replacedEntry *FSEntry
	if existing := FindEntryInList(result.Entries, fileName); existing != nil {
		ent := *existing
		replacedEntry = &ent
	}
	newEntries := RemoveEntryFromList(result.Entries, fileName) // remove if somehow exists
	oldEntry.Name = fileName
	oldEntry.MTime = time.Now().Unix()
	newEntries = AddEntryToList(newEntries, oldEntry)

	deltaBytes, deltaFiles, err := fsEntryDelta(fsHelper, repoID, oldEntry, replacedEntry)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error_msg": "failed to compute storage delta: " + err.Error()})
		return
	}
	if !preCheckStorageQuotaForDeltaWithKey(c, orgID, userID, deltaBytes, "error_msg") {
		return
	}

	// Create new fs_object for modified parent
	newParentFSID, err := fsHelper.CreateDirectoryFSObject(repoID, newEntries)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error_msg": "failed to update directory"})
		return
	}

	// Rebuild path to root
	newRootFSID, err := fsHelper.RebuildPathToRoot(repoID, result, newParentFSID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error_msg": "failed to rebuild path"})
		return
	}

	// Create new commit
	description := fmt.Sprintf("Restored \"%s\" from trash", fileName)
	newCommitID, err := fsHelper.CreateCommit(repoID, userID, newRootFSID, headCommitID, description)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error_msg": "failed to create commit"})
		return
	}

	// Update library head
	if err := fsHelper.UpdateLibraryHead(orgID, repoID, newCommitID, headCommitID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error_msg": "failed to update library"})
		return
	}

	if !applyStorageCounterDeltaWithKey(c, h.db, orgID, userID, repoID, deltaBytes, deltaFiles, "error_msg") {
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// CleanRepoTrash permanently cleans deleted items from trash by enqueuing
// expired commits for GC deletion.
//
// The handler lists all commits for the library, walks the HEAD parent chain
// to identify commits that must be kept, and enqueues the rest to the GC queue.
// The GC worker handles cascading deletion (commit → root_fs_id → blocks).
//
// DELETE /api/v2.1/repos/:repo_id/trash/?keep_days=3
func (h *TrashHandler) CleanRepoTrash(c *gin.Context) {
	repoID := c.Param("repo_id")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	// Permission check — need write access
	if h.permMiddleware != nil {
		hasAccess, err := h.permMiddleware.HasLibraryAccessCtx(c, orgID, userID, repoID, middleware.PermissionRW)
		if err != nil || !hasAccess {
			c.JSON(http.StatusForbidden, gin.H{"error_msg": "permission denied"})
			return
		}
	}

	if h.db == nil {
		c.JSON(http.StatusOK, gin.H{"success": true})
		return
	}

	// Parse keep_days parameter (0 = delete all non-HEAD, 3 = keep last 3 days, etc.)
	keepDays := 0
	if d := c.Query("keep_days"); d != "" {
		if parsed, err := strconv.Atoi(d); err == nil && parsed >= 0 {
			keepDays = parsed
		}
	}

	// Resolve the org partition through libraries_by_id, then read the canonical
	// head_commit_id from libraries so transient lookup-table lag does not affect
	// commit retention decisions.
	var resolvedOrgID string
	if err := h.db.Session().Query(`
		SELECT org_id FROM libraries_by_id WHERE library_id = ?
	`, repoID).Scan(&resolvedOrgID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error_msg": "library not found"})
		return
	}

	var headCommitID string
	if err := h.db.Session().Query(`
		SELECT head_commit_id FROM libraries WHERE org_id = ? AND library_id = ?
	`, resolvedOrgID, repoID).Scan(&headCommitID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error_msg": "library not found"})
		return
	}

	// List all commits with timestamps and parent IDs
	type commitRow struct {
		CommitID  string
		ParentID  string
		CreatedAt time.Time
	}

	iter := h.db.Session().Query(`
		SELECT commit_id, parent_id, created_at FROM commits WHERE library_id = ?
	`, repoID).Iter()

	commitMap := make(map[string]commitRow)
	var cID, pID string
	var createdAt time.Time
	for iter.Scan(&cID, &pID, &createdAt) {
		commitMap[cID] = commitRow{CommitID: cID, ParentID: pID, CreatedAt: createdAt}
	}
	iter.Close()

	if len(commitMap) == 0 {
		c.JSON(http.StatusOK, gin.H{"success": true})
		return
	}

	// Walk HEAD chain → keepSet (these commits form the linear history)
	keepSet := make(map[string]bool)
	current := headCommitID
	for current != "" {
		if keepSet[current] {
			break // cycle protection
		}
		keepSet[current] = true
		if row, ok := commitMap[current]; ok {
			current = row.ParentID
		} else {
			break
		}
	}

	// Also keep any commit created within keep_days window
	if keepDays > 0 {
		cutoff := time.Now().AddDate(0, 0, -keepDays)
		for _, row := range commitMap {
			if !row.CreatedAt.Before(cutoff) {
				keepSet[row.CommitID] = true
			}
		}
	}

	// Collect expired commit IDs (not in keepSet)
	var expiredIDs []string
	for id := range commitMap {
		if !keepSet[id] {
			expiredIDs = append(expiredIDs, id)
		}
	}

	if len(expiredIDs) == 0 {
		c.JSON(http.StatusOK, gin.H{"success": true})
		return
	}

	// Enqueue expired commits for GC deletion
	enqueuer := getCommitEnqueuer()
	if enqueuer != nil {
		enqueuer.EnqueueCommits(orgID, repoID, expiredIDs)
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// CommitDirEntry represents an entry in a commit's directory listing
type CommitDirEntry struct {
	Name      string `json:"name"`
	ObjID     string `json:"obj_id"`
	Type      string `json:"type"` // "file" or "dir"
	Size      int64  `json:"size"`
	ParentDir string `json:"parent_dir"`
}

// ListCommitDir lists the contents of a directory at a specific commit
// GET /api/v2.1/repos/:repo_id/commit/:commit_id/dir/?p=/path
func (h *TrashHandler) ListCommitDir(c *gin.Context) {
	repoID := c.Param("repo_id")
	commitID := c.Param("commit_id")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")
	dirPath := c.DefaultQuery("p", "/")

	dirPath = normalizePath(dirPath)

	// Permission check
	if h.permMiddleware != nil {
		hasAccess, err := h.permMiddleware.HasLibraryAccessCtx(c, orgID, userID, repoID, middleware.PermissionR)
		if err != nil || !hasAccess {
			c.JSON(http.StatusForbidden, gin.H{"error_msg": "permission denied"})
			return
		}
	}

	if h.db == nil {
		c.JSON(http.StatusOK, gin.H{"dirent_list": []CommitDirEntry{}})
		return
	}

	// Get the root_fs_id from the specified commit
	var rootFSID string
	err := h.db.Session().Query(`
		SELECT root_fs_id FROM commits WHERE library_id = ? AND commit_id = ?
	`, repoID, commitID).Scan(&rootFSID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error_msg": "commit not found"})
		return
	}

	fsHelper := NewFSHelper(h.db)

	// Traverse from the commit's root to the target directory
	result, err := fsHelper.TraverseToPathFromRoot(repoID, rootFSID, dirPath)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error_msg": "directory not found in commit"})
		return
	}

	// Convert entries to response format
	var direntList []CommitDirEntry
	for _, entry := range result.Entries {
		entryType := "file"
		if entry.Mode == ModeDir {
			entryType = "dir"
		}
		direntList = append(direntList, CommitDirEntry{
			Name:      entry.Name,
			ObjID:     entry.ID,
			Type:      entryType,
			Size:      entry.Size,
			ParentDir: dirPath,
		})
	}

	if direntList == nil {
		direntList = []CommitDirEntry{}
	}

	c.JSON(http.StatusOK, gin.H{"dirent_list": direntList})
}

// RevertDirents restores multiple deleted items from trash in a single request.
// POST /api/v2.1/repos/:repo_id/trash/revert-dirents/
// Body (form-data): commit_id=xxx&path=/file1&path=/dir1/
func (h *TrashHandler) RevertDirents(c *gin.Context) {
	repoID := c.Param("repo_id")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")
	commitID := c.PostForm("commit_id")
	paths := c.PostFormArray("path")

	if commitID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error_msg": "commit_id is required"})
		return
	}
	if len(paths) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error_msg": "at least one path is required"})
		return
	}

	// Permission check — need write access
	if h.permMiddleware != nil {
		hasAccess, err := h.permMiddleware.HasLibraryAccessCtx(c, orgID, userID, repoID, middleware.PermissionRW)
		if err != nil || !hasAccess {
			c.JSON(http.StatusForbidden, gin.H{"error_msg": "permission denied"})
			return
		}
	}

	if h.db == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error_msg": "database not available"})
		return
	}

	fsHelper := NewFSHelper(h.db)

	// Get the root_fs_id from the target commit (where items existed)
	var oldRootFSID string
	err := h.db.Session().Query(`
		SELECT root_fs_id FROM commits WHERE library_id = ? AND commit_id = ?
	`, repoID, commitID).Scan(&oldRootFSID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error_msg": "commit not found"})
		return
	}

	// Get current HEAD commit
	headCommitID, err := fsHelper.GetHeadCommitID(repoID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error_msg": "library not found"})
		return
	}

	// Restore each path one by one, updating HEAD each time
	currentHeadCommitID := headCommitID

	type revertResult struct {
		Path  string `json:"path"`
		IsDir bool   `json:"is_dir"`
	}
	var successItems []revertResult
	var failedItems []revertResult

	for _, filePath := range paths {
		filePath = normalizePath(filePath)

		// Traverse the old commit to find the deleted item
		oldResult, err := fsHelper.TraverseToPathFromRoot(repoID, oldRootFSID, filePath)
		if err != nil || oldResult.TargetEntry == nil {
			failedItems = append(failedItems, revertResult{Path: filePath})
			continue
		}

		oldEntry := *oldResult.TargetEntry
		isDir := oldEntry.Mode == ModeDir || oldEntry.Mode&0170000 == 040000

		// Determine parent directory path
		parentPath := path.Dir(filePath)
		if parentPath == "." {
			parentPath = "/"
		}

		// Traverse current HEAD to the parent directory
		result, err := fsHelper.TraverseToPath(repoID, parentPath)
		if err != nil {
			result, err = fsHelper.TraverseToPath(repoID, "/")
			if err != nil {
				failedItems = append(failedItems, revertResult{Path: filePath, IsDir: isDir})
				continue
			}
		}

		// Add the restored item to the parent directory
		fileName := path.Base(filePath)
		var replacedEntry *FSEntry
		if existing := FindEntryInList(result.Entries, fileName); existing != nil {
			ent := *existing
			replacedEntry = &ent
		}
		newEntries := RemoveEntryFromList(result.Entries, fileName)
		oldEntry.Name = fileName
		oldEntry.MTime = time.Now().Unix()
		newEntries = AddEntryToList(newEntries, oldEntry)

		// Storage quota pre-check using the visible delta the restore introduces.
		// Failures fall into failedItems so partial batches succeed; the user
		// can re-issue with a smaller selection if some items exceed the cap.
		deltaBytes, deltaFiles, err := fsEntryDelta(fsHelper, repoID, oldEntry, replacedEntry)
		if err != nil {
			failedItems = append(failedItems, revertResult{Path: filePath, IsDir: isDir})
			continue
		}
		if deltaBytes > 0 {
			if checker := traffic.GetChecker(); checker != nil {
				if st, _ := checker.CheckStorageQuota(orgID, userID, deltaBytes); !st.Allowed {
					failedItems = append(failedItems, revertResult{Path: filePath, IsDir: isDir})
					continue
				}
			}
		}

		// Create new fs_object for modified parent
		newParentFSID, err := fsHelper.CreateDirectoryFSObject(repoID, newEntries)
		if err != nil {
			failedItems = append(failedItems, revertResult{Path: filePath, IsDir: isDir})
			continue
		}

		// Rebuild path to root
		newRootFSID, err := fsHelper.RebuildPathToRoot(repoID, result, newParentFSID)
		if err != nil {
			failedItems = append(failedItems, revertResult{Path: filePath, IsDir: isDir})
			continue
		}

		// Create new commit
		description := fmt.Sprintf("Restored \"%s\" from trash", fileName)
		newCommitID, err := fsHelper.CreateCommit(repoID, userID, newRootFSID, currentHeadCommitID, description)
		if err != nil {
			failedItems = append(failedItems, revertResult{Path: filePath, IsDir: isDir})
			continue
		}

		// Update library head
		if err := fsHelper.UpdateLibraryHead(orgID, repoID, newCommitID, currentHeadCommitID); err != nil {
			failedItems = append(failedItems, revertResult{Path: filePath, IsDir: isDir})
			continue
		}

		currentHeadCommitID = newCommitID

		if err := traffic.AdjustStorageCountersByDeltaSync(h.db, orgID, userID, repoID, deltaBytes, deltaFiles); err != nil {
			log.Printf("[RevertDirents] failed to apply storage counter delta for %s: %v", filePath, err)
		}

		successItems = append(successItems, revertResult{Path: filePath, IsDir: isDir})
	}

	if successItems == nil {
		successItems = []revertResult{}
	}
	if failedItems == nil {
		failedItems = []revertResult{}
	}

	c.JSON(http.StatusOK, gin.H{"success": successItems, "failed": failedItems})
}
