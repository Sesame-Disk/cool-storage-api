package v2

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"path"
	"strings"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

// FSHelper provides helper functions for file system operations
type FSHelper struct {
	db *db.DB
}

// NewFSHelper creates a new FSHelper instance
func NewFSHelper(database *db.DB) *FSHelper {
	return &FSHelper{db: database}
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

// GetRootFSID gets the root fs_id from a library's head commit
func (h *FSHelper) GetRootFSID(repoID string) (string, string, error) {
	// Get head_commit_id from library lookup table (no ALLOW FILTERING needed)
	var headCommitID string
	err := h.db.Session().Query(`
		SELECT head_commit_id FROM libraries_by_id WHERE library_id = ?
	`, repoID).Scan(&headCommitID)
	if err != nil {
		return "", "", fmt.Errorf("library not found: %w", err)
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
	if len(result.Ancestors) == 0 {
		// Parent was root, new parent FS ID is the new root
		return newParentFSID, nil
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

		// Get ancestor's entries
		entries, err := h.GetDirectoryEntries(repoID, ancestorFSID)
		if err != nil {
			return "", fmt.Errorf("failed to get ancestor %s: %w", ancestorPath, err)
		}

		// Update the child reference in ancestor
		for j := range entries {
			if entries[j].Name == currentName {
				entries[j].ID = currentFSID
				break
			}
		}

		// Create new fs_object for modified ancestor
		newAncestorFSID, err := h.CreateDirectoryFSObject(repoID, entries)
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

// UpdateLibraryHead updates the library's head_commit_id, size_bytes, and file_count
// Uses batched dual-write to maintain consistency with libraries_by_id
func (h *FSHelper) UpdateLibraryHead(orgID, repoID, commitID string) error {
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
	state, err := readAdminLibraryProjectionStateOptional(h.db, repoID)
	if err != nil {
		return fmt.Errorf("failed to read library projection state: %w", err)
	}
	projectionRow, err := db.ReadAdminLibraryProjectionRow(h.db.Session(), orgID, repoID)
	if err != nil {
		return fmt.Errorf("failed to read library projection row: %w", err)
	}
	projectionRow.SizeBytes = totalSize
	projectionRow.FileCount = fileCount
	projectionRow.UpdatedAt = now
	batch := h.db.Session().Batch(gocql.LoggedBatch)

	// Update main table with stats
	batch.Query(`
		UPDATE libraries SET head_commit_id = ?, size_bytes = ?, file_count = ?, updated_at = ?
		WHERE org_id = ? AND library_id = ?
	`, commitID, totalSize, fileCount, now, orgID, repoID)

	// Update lookup table
	batch.Query(`
		UPDATE libraries_by_id SET head_commit_id = ?
		WHERE library_id = ?
	`, commitID, repoID)
	addAdminLibraryReadModelRefreshQueries(batch, projectionRow, state)

	if err := batch.Exec(); err != nil {
		return fmt.Errorf("failed to update library head: %w", err)
	}

	log.Printf("[UpdateLibraryHead] Updated library %s: size=%d bytes, files=%d", repoID, totalSize, fileCount)
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
	var headCommitID string
	err := h.db.Session().Query(`
		SELECT head_commit_id FROM libraries_by_id WHERE library_id = ?
	`, repoID).Scan(&headCommitID)
	if err != nil {
		return "", fmt.Errorf("library not found: %w", err)
	}
	return headCommitID, nil
}

// CollectBlockIDsRecursive collects all block IDs from a directory tree recursively
func (h *FSHelper) CollectBlockIDsRecursive(repoID, fsID string) ([]string, error) {
	blockIDs, _, _, err := h.collectDirStats(repoID, fsID)
	return blockIDs, err
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

// DecrementBlockRefCounts decrements ref_count for blocks (for deletion).
// Returns the list of block IDs whose ref_count reached 0.
func (h *FSHelper) DecrementBlockRefCounts(orgID string, blockIDs []string) []string {
	var zeroRefBlocks []string
	for _, blockID := range blockIDs {
		err := h.db.Session().Query(`
			UPDATE blocks SET ref_count = ref_count - 1, last_accessed = ?
			WHERE org_id = ? AND block_id = ?
		`, time.Now(), orgID, blockID).Exec()
		if err != nil {
			continue
		}

		// Check if ref_count hit 0
		var refCount int
		if err := h.db.Session().Query(`
			SELECT ref_count FROM blocks WHERE org_id = ? AND block_id = ?
		`, orgID, blockID).Scan(&refCount); err != nil {
			continue
		}
		if refCount <= 0 {
			zeroRefBlocks = append(zeroRefBlocks, blockID)
		}
	}
	return zeroRefBlocks
}

// CopyFSObjectToLibrary recursively copies an fs_object (file or directory) from
// one library to another. Returns the new fs_id in the destination library.
// This is needed because fs_objects are keyed by (library_id, fs_id), so a cross-library
// copy must create new fs_object rows in the destination library.
func (h *FSHelper) CopyFSObjectToLibrary(srcRepoID, dstRepoID, fsID string) (string, error) {
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
		// File: create a new fs_object in the destination library with the same block_ids
		newFSID, err := h.CreateFileFSObject(dstRepoID, objName, sizeBytes, blockIDs)
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
		newChildFSID, err := h.CopyFSObjectToLibrary(srcRepoID, dstRepoID, entry.ID)
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

// IncrementBlockRefCounts increments ref_count for blocks (for copy)
func (h *FSHelper) IncrementBlockRefCounts(orgID string, blockIDs []string) error {
	for _, blockID := range blockIDs {
		err := h.db.Session().Query(`
			UPDATE blocks SET ref_count = ref_count + 1, last_accessed = ?
			WHERE org_id = ? AND block_id = ?
		`, time.Now(), orgID, blockID).Exec()
		if err != nil {
			continue
		}
	}
	return nil
}

// CreateFileFSObject creates a new fs_object for a file
func (h *FSHelper) CreateFileFSObject(repoID, name string, size int64, blockIDs []string) (string, error) {
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
