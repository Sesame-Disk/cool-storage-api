package gc

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/google/uuid"
)

// parseUUID converts a string scanned from Cassandra to uuid.UUID.
// gocql v2 cannot unmarshal CQL UUID directly into google/uuid.UUID,
// so we scan as string and convert.
func parseUUID(s string) uuid.UUID {
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil
	}
	return id
}

// parseDirEntries extracts child fs_ids from a JSON dir_entries column.
// Each entry has an "id" field that is the child fs_id.
func parseDirEntries(jsonStr string) []string {
	if jsonStr == "" {
		return nil
	}
	var entries []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &entries); err != nil {
		return nil
	}
	if len(entries) == 0 {
		return nil
	}
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.ID != "" {
			ids = append(ids, e.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	return ids
}

// CassandraStore implements GCStore using a Cassandra database.
type CassandraStore struct {
	db *db.DB
}

// NewCassandraStore creates a new CassandraStore.
func NewCassandraStore(database *db.DB) *CassandraStore {
	return &CassandraStore{db: database}
}

// --- Queue operations ---

func (s *CassandraStore) EnqueueItem(orgID uuid.UUID, queuedAt time.Time, itemType ItemType, itemID string, libraryID uuid.UUID, storageClass string, retryCount int) error {
	return s.db.Session().Query(`
		INSERT INTO gc_queue (org_id, queued_at, item_type, item_id, library_id, storage_class, retry_count)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, orgID.String(), queuedAt, string(itemType), itemID, libraryID.String(), storageClass, retryCount).Exec()
}

func (s *CassandraStore) DequeueBatch(orgID uuid.UUID, batchSize int, cutoff time.Time) ([]QueueItem, error) {
	iter := s.db.Session().Query(`
		SELECT org_id, queued_at, item_type, item_id, library_id, storage_class, retry_count
		FROM gc_queue
		WHERE org_id = ? AND queued_at < ?
		LIMIT ?
	`, orgID.String(), cutoff, batchSize).Iter()

	var items []QueueItem
	var orgIDStr, itemTypeStr, itemID, libIDStr, storageClass string
	var queuedAt time.Time
	var retryCount int

	for iter.Scan(&orgIDStr, &queuedAt, &itemTypeStr, &itemID,
		&libIDStr, &storageClass, &retryCount) {
		items = append(items, QueueItem{
			OrgID:        parseUUID(orgIDStr),
			QueuedAt:     queuedAt,
			ItemType:     ItemType(itemTypeStr),
			ItemID:       itemID,
			LibraryID:    parseUUID(libIDStr),
			StorageClass: storageClass,
			RetryCount:   retryCount,
		})
	}

	if err := iter.Close(); err != nil {
		return nil, fmt.Errorf("failed to dequeue batch: %w", err)
	}
	return items, nil
}

func (s *CassandraStore) CompleteItem(orgID uuid.UUID, queuedAt time.Time, itemType ItemType, itemID string) error {
	return s.db.Session().Query(`
		DELETE FROM gc_queue
		WHERE org_id = ? AND queued_at = ? AND item_type = ? AND item_id = ?
	`, orgID.String(), queuedAt, string(itemType), itemID).Exec()
}

func (s *CassandraStore) UpdateRetryCount(orgID uuid.UUID, queuedAt time.Time, itemType ItemType, itemID string, retryCount int) error {
	return s.db.Session().Query(`
		UPDATE gc_queue SET retry_count = ?
		WHERE org_id = ? AND queued_at = ? AND item_type = ? AND item_id = ?
	`, retryCount, orgID.String(), queuedAt, string(itemType), itemID).Exec()
}

func (s *CassandraStore) GetQueueSize(orgID uuid.UUID) (int, error) {
	var count int
	err := s.db.Session().Query(`
		SELECT COUNT(*) FROM gc_queue WHERE org_id = ?
	`, orgID.String()).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to get queue size: %w", err)
	}
	return count, nil
}

func (s *CassandraStore) GetTotalQueueSize() (int, error) {
	var count int
	err := s.db.Session().Query(`SELECT COUNT(*) FROM gc_queue`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to get total queue size: %w", err)
	}
	return count, nil
}

func (s *CassandraStore) ListOrgsWithQueuedItems() ([]uuid.UUID, error) {
	iter := s.db.Session().Query(`SELECT DISTINCT org_id FROM gc_queue`).Iter()
	var orgs []uuid.UUID
	var orgIDStr string
	for iter.Scan(&orgIDStr) {
		orgs = append(orgs, parseUUID(orgIDStr))
	}
	if err := iter.Close(); err != nil {
		return nil, fmt.Errorf("failed to list orgs: %w", err)
	}
	return orgs, nil
}

// --- Block operations ---

func (s *CassandraStore) GetBlockRefCount(orgID uuid.UUID, blockID string) (int, error) {
	var refCount int
	err := s.db.Session().Query(`
		SELECT ref_count FROM blocks WHERE org_id = ? AND block_id = ?
	`, orgID.String(), blockID).Scan(&refCount)
	return refCount, err
}

func (s *CassandraStore) DeleteBlock(orgID uuid.UUID, blockID string) error {
	return s.db.Session().Query(`
		DELETE FROM blocks WHERE org_id = ? AND block_id = ?
	`, orgID.String(), blockID).Exec()
}

func (s *CassandraStore) DecrementBlockRefCount(orgID uuid.UUID, blockID string) error {
	return s.db.Session().Query(`
		UPDATE blocks SET ref_count = ref_count - 1, last_accessed = ?
		WHERE org_id = ? AND block_id = ?
	`, time.Now(), orgID.String(), blockID).Exec()
}

func (s *CassandraStore) ListBlockMappingsByInternalID(orgID uuid.UUID, internalID string) ([]BlockMapping, error) {
	iter := s.db.Session().Query(`
		SELECT internal_id, external_id FROM block_id_mappings_by_internal
		WHERE org_id = ? AND internal_id = ?
	`, orgID.String(), internalID).Iter()

	var mappings []BlockMapping
	var intID, extID string
	for iter.Scan(&intID, &extID) {
		mappings = append(mappings, BlockMapping{ExternalID: extID, InternalID: intID})
	}
	if err := iter.Close(); err != nil {
		// Fallback: scan the original table if reverse lookup table doesn't exist yet
		return s.listBlockMappingsByInternalIDFallback(orgID, internalID)
	}
	return mappings, nil
}

// listBlockMappingsByInternalIDFallback scans block_id_mappings for the given internalID.
// Used as fallback if the reverse lookup table hasn't been populated yet.
func (s *CassandraStore) listBlockMappingsByInternalIDFallback(orgID uuid.UUID, internalID string) ([]BlockMapping, error) {
	iter := s.db.Session().Query(`
		SELECT external_id, internal_id FROM block_id_mappings WHERE org_id = ?
	`, orgID.String()).Iter()

	var mappings []BlockMapping
	var extID, intID string
	for iter.Scan(&extID, &intID) {
		if intID == internalID {
			mappings = append(mappings, BlockMapping{ExternalID: extID, InternalID: intID})
		}
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return mappings, nil
}

func (s *CassandraStore) DeleteBlockMapping(orgID uuid.UUID, externalID string) error {
	// Read the internal_id first for reverse table cleanup
	var internalID string
	err := s.db.Session().Query(`
		SELECT internal_id FROM block_id_mappings WHERE org_id = ? AND external_id = ?
	`, orgID.String(), externalID).Scan(&internalID)

	batch := s.db.Session().Batch(gocql.UnloggedBatch)
	batch.Query(`DELETE FROM block_id_mappings WHERE org_id = ? AND external_id = ?`, orgID.String(), externalID)
	if err == nil && internalID != "" {
		batch.Query(`DELETE FROM block_id_mappings_by_internal WHERE org_id = ? AND internal_id = ? AND external_id = ?`,
			orgID.String(), internalID, externalID)
	}
	return batch.Exec()
}

// --- Commit operations ---

func (s *CassandraStore) GetCommit(libraryID uuid.UUID, commitID string) (CommitInfo, error) {
	var info CommitInfo
	err := s.db.Session().Query(`
		SELECT commit_id, root_fs_id FROM commits WHERE library_id = ? AND commit_id = ?
	`, libraryID.String(), commitID).Scan(&info.CommitID, &info.RootFSID)
	return info, err
}

func (s *CassandraStore) DeleteCommit(libraryID uuid.UUID, commitID string) error {
	return s.db.Session().Query(`
		DELETE FROM commits WHERE library_id = ? AND commit_id = ?
	`, libraryID.String(), commitID).Exec()
}

// --- FS object operations ---

func (s *CassandraStore) GetFSObject(libraryID uuid.UUID, fsID string) (FSObjectInfo, error) {
	var info FSObjectInfo
	var blockIDs []string
	var dirEntriesJSON string
	err := s.db.Session().Query(`
		SELECT fs_id, obj_type, block_ids, dir_entries FROM fs_objects WHERE library_id = ? AND fs_id = ?
	`, libraryID.String(), fsID).Scan(&info.FSID, &info.ObjType, &blockIDs, &dirEntriesJSON)
	if err != nil {
		return FSObjectInfo{}, err
	}
	info.BlockIDs = blockIDs
	info.DirEntries = parseDirEntries(dirEntriesJSON)
	return info, nil
}

func (s *CassandraStore) DeleteFSObject(libraryID uuid.UUID, fsID string) error {
	return s.db.Session().Query(`
		DELETE FROM fs_objects WHERE library_id = ? AND fs_id = ?
	`, libraryID.String(), fsID).Exec()
}

// --- Library operations ---

func (s *CassandraStore) GetLibraryStorageClass(orgID, libraryID uuid.UUID) (string, error) {
	var storageClass string
	err := s.db.Session().Query(`
		SELECT storage_class FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID.String(), libraryID.String()).Scan(&storageClass)
	return storageClass, err
}

func (s *CassandraStore) ListCommitsForLibrary(libraryID uuid.UUID) ([]CommitInfo, error) {
	iter := s.db.Session().Query(`
		SELECT commit_id, root_fs_id FROM commits WHERE library_id = ?
	`, libraryID.String()).Iter()

	var commits []CommitInfo
	var commitID, rootFSID string
	for iter.Scan(&commitID, &rootFSID) {
		commits = append(commits, CommitInfo{CommitID: commitID, RootFSID: rootFSID})
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return commits, nil
}

func (s *CassandraStore) ListFSObjectsForLibrary(libraryID uuid.UUID) ([]FSObjectInfo, error) {
	iter := s.db.Session().Query(`
		SELECT fs_id, obj_type, block_ids, dir_entries FROM fs_objects WHERE library_id = ?
	`, libraryID.String()).Iter()

	var objects []FSObjectInfo
	var fsID, objType, dirEntriesJSON string
	var blockIDs []string
	for iter.Scan(&fsID, &objType, &blockIDs, &dirEntriesJSON) {
		objects = append(objects, FSObjectInfo{
			FSID:       fsID,
			ObjType:    objType,
			BlockIDs:   blockIDs,
			DirEntries: parseDirEntries(dirEntriesJSON),
		})
		dirEntriesJSON = ""
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return objects, nil
}

// --- Scanner operations ---

func (s *CassandraStore) ListOrganizations() ([]uuid.UUID, error) {
	iter := s.db.Session().Query(`SELECT org_id FROM organizations`).Iter()
	var orgs []uuid.UUID
	var orgIDStr string
	for iter.Scan(&orgIDStr) {
		orgs = append(orgs, parseUUID(orgIDStr))
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return orgs, nil
}

func (s *CassandraStore) ListBlocksForOrg(orgID uuid.UUID) ([]BlockInfo, error) {
	iter := s.db.Session().Query(`
		SELECT block_id, storage_class, ref_count FROM blocks WHERE org_id = ?
	`, orgID.String()).Iter()

	var blocks []BlockInfo
	var blockID, storageClass string
	var refCount int
	for iter.Scan(&blockID, &storageClass, &refCount) {
		blocks = append(blocks, BlockInfo{BlockID: blockID, StorageClass: storageClass, RefCount: refCount})
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return blocks, nil
}

func (s *CassandraStore) ListShareLinks() ([]ShareLinkInfo, error) {
	iter := s.db.Session().Query(`
		SELECT link_token, org_id, expires_at FROM share_links
	`).Iter()

	var links []ShareLinkInfo
	var shareToken, orgIDStr string
	var expiresAt time.Time
	for iter.Scan(&shareToken, &orgIDStr, &expiresAt) {
		links = append(links, ShareLinkInfo{ShareToken: shareToken, OrgID: parseUUID(orgIDStr), ExpiresAt: expiresAt})
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return links, nil
}

func (s *CassandraStore) ListDistinctCommitLibraries() ([]uuid.UUID, error) {
	iter := s.db.Session().Query(`SELECT DISTINCT library_id FROM commits`).Iter()
	var ids []uuid.UUID
	var idStr string
	for iter.Scan(&idStr) {
		ids = append(ids, parseUUID(idStr))
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return ids, nil
}

func (s *CassandraStore) ListDistinctFSObjectLibraries() ([]uuid.UUID, error) {
	iter := s.db.Session().Query(`SELECT DISTINCT library_id FROM fs_objects`).Iter()
	var ids []uuid.UUID
	var idStr string
	for iter.Scan(&idStr) {
		ids = append(ids, parseUUID(idStr))
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return ids, nil
}

func (s *CassandraStore) LibraryExists(libraryID uuid.UUID) (bool, error) {
	var existingLibIDStr string
	err := s.db.Session().Query(`
		SELECT library_id FROM libraries_by_id WHERE library_id = ?
	`, libraryID.String()).Scan(&existingLibIDStr)
	if err != nil {
		return false, nil // Not found
	}
	return true, nil
}

func (s *CassandraStore) FindOrgForLibrary(libraryID uuid.UUID) (uuid.UUID, error) {
	var orgIDStr string
	err := s.db.Session().Query(`
		SELECT org_id FROM libraries_by_id WHERE library_id = ?
	`, libraryID.String()).Scan(&orgIDStr)
	if err != nil {
		return uuid.Nil, err
	}
	return parseUUID(orgIDStr), nil
}

func (s *CassandraStore) ListCommitIDsForLibrary(libraryID uuid.UUID) ([]string, error) {
	iter := s.db.Session().Query(`
		SELECT commit_id FROM commits WHERE library_id = ?
	`, libraryID.String()).Iter()
	var ids []string
	var id string
	for iter.Scan(&id) {
		ids = append(ids, id)
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return ids, nil
}

func (s *CassandraStore) ListFSObjectIDsForLibrary(libraryID uuid.UUID) ([]string, error) {
	iter := s.db.Session().Query(`
		SELECT fs_id FROM fs_objects WHERE library_id = ?
	`, libraryID.String()).Iter()
	var ids []string
	var id string
	for iter.Scan(&id) {
		ids = append(ids, id)
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return ids, nil
}

// --- Version TTL ---

func (s *CassandraStore) ListLibrariesWithVersionTTL() ([]LibraryTTLInfo, error) {
	iter := s.db.Session().Query(`
		SELECT org_id, library_id, head_commit_id, version_ttl_days FROM libraries
	`).Iter()

	var results []LibraryTTLInfo
	var orgIDStr, libIDStr, headCommitID string
	var versionTTLDays int
	for iter.Scan(&orgIDStr, &libIDStr, &headCommitID, &versionTTLDays) {
		if versionTTLDays > 0 {
			results = append(results, LibraryTTLInfo{
				OrgID:          parseUUID(orgIDStr),
				LibraryID:      parseUUID(libIDStr),
				HeadCommitID:   headCommitID,
				VersionTTLDays: versionTTLDays,
			})
		}
	}
	if err := iter.Close(); err != nil {
		return nil, fmt.Errorf("failed to list libraries with version TTL: %w", err)
	}
	return results, nil
}

func (s *CassandraStore) ListCommitsWithTimestamps(libraryID uuid.UUID) ([]CommitWithTimestamp, error) {
	iter := s.db.Session().Query(`
		SELECT commit_id, parent_id, root_fs_id, created_at FROM commits WHERE library_id = ?
	`, libraryID.String()).Iter()

	var commits []CommitWithTimestamp
	var commitID, parentID, rootFSID string
	var createdAt time.Time
	for iter.Scan(&commitID, &parentID, &rootFSID, &createdAt) {
		commits = append(commits, CommitWithTimestamp{
			CommitID:  commitID,
			ParentID:  parentID,
			RootFSID:  rootFSID,
			CreatedAt: createdAt,
		})
	}
	if err := iter.Close(); err != nil {
		return nil, fmt.Errorf("failed to list commits with timestamps: %w", err)
	}
	return commits, nil
}

// --- Auto-delete ---

func (s *CassandraStore) ListLibrariesWithAutoDelete() ([]LibraryAutoDeleteInfo, error) {
	iter := s.db.Session().Query(`
		SELECT org_id, library_id, head_commit_id, auto_delete_days FROM libraries
	`).Iter()

	var results []LibraryAutoDeleteInfo
	var orgIDStr, libIDStr, headCommitID string
	var autoDeleteDays int
	for iter.Scan(&orgIDStr, &libIDStr, &headCommitID, &autoDeleteDays) {
		if autoDeleteDays > 0 {
			results = append(results, LibraryAutoDeleteInfo{
				OrgID:          parseUUID(orgIDStr),
				LibraryID:      parseUUID(libIDStr),
				HeadCommitID:   headCommitID,
				AutoDeleteDays: autoDeleteDays,
			})
		}
	}
	if err := iter.Close(); err != nil {
		return nil, fmt.Errorf("failed to list libraries with auto delete: %w", err)
	}
	return results, nil
}

// --- Share link deletion ---

func (s *CassandraStore) DeleteShareLink(shareToken string) error {
	// Read clustering keys from primary table for quad-delete
	var orgID, createdBy, libraryID string
	var createdAt time.Time
	err := s.db.Session().Query(`
		SELECT org_id, created_by, library_id, created_at FROM share_links WHERE link_token = ?
	`, shareToken).Scan(&orgID, &createdBy, &libraryID, &createdAt)
	if err != nil {
		// If not found in primary table, nothing to delete
		return nil
	}

	// Quad-delete from all 4 tables
	batch := s.db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`DELETE FROM share_links WHERE link_token = ?`, shareToken)
	batch.Query(`DELETE FROM share_links_by_creator WHERE org_id = ? AND created_by = ? AND created_at = ? AND link_token = ?`,
		orgID, createdBy, createdAt, shareToken)
	batch.Query(`DELETE FROM share_links_by_org WHERE org_id = ? AND created_at = ? AND link_token = ?`,
		orgID, createdAt, shareToken)
	batch.Query(`DELETE FROM share_links_by_library WHERE org_id = ? AND library_id = ? AND link_token = ?`,
		orgID, libraryID, shareToken)
	return batch.Exec()
}

// --- Expired shares (user-to-user) ---

func (s *CassandraStore) ListExpiredShares() ([]ExpiredShareInfo, error) {
	now := time.Now()
	// shares table doesn't have a global secondary index on expires_at,
	// so we need to scan all shares. This is acceptable since it runs every 24h.
	iter := s.db.Session().Query(`
		SELECT library_id, share_id, shared_to, expires_at FROM shares
	`).Iter()

	var results []ExpiredShareInfo
	var libIDStr, shareIDStr, sharedToStr string
	var expiresAt time.Time
	for iter.Scan(&libIDStr, &shareIDStr, &sharedToStr, &expiresAt) {
		if !expiresAt.IsZero() && expiresAt.Before(now) {
			results = append(results, ExpiredShareInfo{
				LibraryID: parseUUID(libIDStr),
				ShareID:   parseUUID(shareIDStr),
				SharedTo:  parseUUID(sharedToStr),
				ExpiresAt: expiresAt,
			})
		}
	}
	if err := iter.Close(); err != nil {
		return nil, fmt.Errorf("failed to list expired shares: %w", err)
	}
	return results, nil
}

func (s *CassandraStore) DeleteShare(libraryID, shareID uuid.UUID) error {
	return s.db.Session().Query(`
		DELETE FROM shares WHERE library_id = ? AND share_id = ?
	`, libraryID.String(), shareID.String()).Exec()
}

func (s *CassandraStore) DeleteShareByUser(sharedTo, libraryID uuid.UUID) error {
	return s.db.Session().Query(`
		DELETE FROM shares_by_user WHERE shared_to = ? AND library_id = ?
	`, sharedTo.String(), libraryID.String()).Exec()
}

// --- Expired restore jobs ---

func (s *CassandraStore) ListExpiredRestoreJobs() ([]ExpiredRestoreJobInfo, error) {
	now := time.Now()
	iter := s.db.Session().Query(`
		SELECT org_id, library_id, job_id, status, expires_at FROM restore_jobs
	`).Iter()

	var results []ExpiredRestoreJobInfo
	var orgIDStr, libIDStr, jobIDStr, status string
	var expiresAt time.Time
	for iter.Scan(&orgIDStr, &libIDStr, &jobIDStr, &status, &expiresAt) {
		// Clean up completed jobs or jobs past their expiration
		if status == "completed" || status == "failed" || (!expiresAt.IsZero() && expiresAt.Before(now)) {
			results = append(results, ExpiredRestoreJobInfo{
				OrgID:     parseUUID(orgIDStr),
				LibraryID: parseUUID(libIDStr),
				JobID:     parseUUID(jobIDStr),
				Status:    status,
				ExpiresAt: expiresAt,
			})
		}
	}
	if err := iter.Close(); err != nil {
		return nil, fmt.Errorf("failed to list expired restore jobs: %w", err)
	}
	return results, nil
}

func (s *CassandraStore) DeleteRestoreJob(orgID, libraryID, jobID uuid.UUID) error {
	return s.db.Session().Query(`
		DELETE FROM restore_jobs WHERE org_id = ? AND library_id = ? AND job_id = ?
	`, orgID.String(), libraryID.String(), jobID.String()).Exec()
}

// --- Library artifact cleanup ---

func (s *CassandraStore) ListSharesByLibrary(libraryID uuid.UUID) ([]ShareInfo, error) {
	iter := s.db.Session().Query(`
		SELECT library_id, share_id, shared_to FROM shares WHERE library_id = ?
	`, libraryID.String()).Iter()

	var results []ShareInfo
	var libIDStr, shareIDStr, sharedToStr string
	for iter.Scan(&libIDStr, &shareIDStr, &sharedToStr) {
		results = append(results, ShareInfo{LibraryID: parseUUID(libIDStr), ShareID: parseUUID(shareIDStr), SharedTo: parseUUID(sharedToStr)})
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return results, nil
}

func (s *CassandraStore) ListRepoTagsByLibrary(libraryID uuid.UUID) ([]string, error) {
	iter := s.db.Session().Query(`
		SELECT tag_id FROM repo_tags WHERE repo_id = ?
	`, libraryID.String()).Iter()

	var results []string
	var tagID int
	for iter.Scan(&tagID) {
		results = append(results, fmt.Sprintf("%d", tagID))
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return results, nil
}

func (s *CassandraStore) DeleteRepoTag(libraryID uuid.UUID, tagID int) error {
	return s.db.Session().Query(`
		DELETE FROM repo_tags WHERE repo_id = ? AND tag_id = ?
	`, libraryID.String(), tagID).Exec()
}

func (s *CassandraStore) ListFileTagsByLibrary(libraryID uuid.UUID) ([]FileTagInfo, error) {
	iter := s.db.Session().Query(`
		SELECT repo_id, file_path, tag_id, file_tag_id FROM file_tags WHERE repo_id = ?
	`, libraryID.String()).Iter()

	var results []FileTagInfo
	var repoIDStr, filePath string
	var tagID, fileTagID int
	for iter.Scan(&repoIDStr, &filePath, &tagID, &fileTagID) {
		results = append(results, FileTagInfo{
			RepoID:    parseUUID(repoIDStr),
			FilePath:  filePath,
			TagID:     tagID,
			FileTagID: fileTagID,
		})
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return results, nil
}

func (s *CassandraStore) DeleteFileTag(libraryID uuid.UUID, filePath string, tagID int) error {
	return s.db.Session().Query(`
		DELETE FROM file_tags WHERE repo_id = ? AND file_path = ? AND tag_id = ?
	`, libraryID.String(), filePath, tagID).Exec()
}

func (s *CassandraStore) DeleteFileTagByID(libraryID uuid.UUID, fileTagID int) error {
	return s.db.Session().Query(`
		DELETE FROM file_tags_by_id WHERE repo_id = ? AND file_tag_id = ?
	`, libraryID.String(), fileTagID).Exec()
}

func (s *CassandraStore) ListRepoAPITokensByLibrary(libraryID uuid.UUID) ([]RepoAPITokenInfo, error) {
	iter := s.db.Session().Query(`
		SELECT repo_id, app_name, api_token FROM repo_api_tokens WHERE repo_id = ?
	`, libraryID.String()).Iter()

	var results []RepoAPITokenInfo
	var repoIDStr, appName, apiToken string
	for iter.Scan(&repoIDStr, &appName, &apiToken) {
		results = append(results, RepoAPITokenInfo{RepoID: parseUUID(repoIDStr), AppName: appName, APIToken: apiToken})
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return results, nil
}

func (s *CassandraStore) DeleteRepoAPIToken(libraryID uuid.UUID, appName string) error {
	return s.db.Session().Query(`
		DELETE FROM repo_api_tokens WHERE repo_id = ? AND app_name = ?
	`, libraryID.String(), appName).Exec()
}

func (s *CassandraStore) DeleteRepoAPITokenByToken(apiToken string) error {
	return s.db.Session().Query(`
		DELETE FROM repo_api_tokens_by_token WHERE api_token = ?
	`, apiToken).Exec()
}

func (s *CassandraStore) DeleteLockedFilesByLibrary(libraryID uuid.UUID) error {
	// First list all locked files for this library, then delete them
	iter := s.db.Session().Query(`
		SELECT path FROM locked_files WHERE repo_id = ?
	`, libraryID.String()).Iter()

	var paths []string
	var path string
	for iter.Scan(&path) {
		paths = append(paths, path)
	}
	iter.Close()

	for _, p := range paths {
		s.db.Session().Query(`
			DELETE FROM locked_files WHERE repo_id = ? AND path = ?
		`, libraryID.String(), p).Exec()
	}
	return nil
}

func (s *CassandraStore) DeleteShareLinksByLibrary(orgID, libraryID uuid.UUID) ([]string, error) {
	// Use the by_library index for efficient lookup
	iter := s.db.Session().Query(`
		SELECT link_token, created_by, created_at FROM share_links_by_library
		WHERE org_id = ? AND library_id = ?
	`, orgID.String(), libraryID.String()).Iter()

	type linkInfo struct {
		token     string
		createdBy string
		createdAt time.Time
	}

	var links []linkInfo
	var token, createdBy string
	var createdAt time.Time
	for iter.Scan(&token, &createdBy, &createdAt) {
		links = append(links, linkInfo{token: token, createdBy: createdBy, createdAt: createdAt})
	}
	iter.Close()

	var deletedTokens []string
	for _, link := range links {
		batch := s.db.Session().Batch(gocql.LoggedBatch)
		batch.Query(`DELETE FROM share_links WHERE link_token = ?`, link.token)
		batch.Query(`DELETE FROM share_links_by_creator WHERE org_id = ? AND created_by = ? AND created_at = ? AND link_token = ?`,
			orgID.String(), link.createdBy, link.createdAt, link.token)
		batch.Query(`DELETE FROM share_links_by_org WHERE org_id = ? AND created_at = ? AND link_token = ?`,
			orgID.String(), link.createdAt, link.token)
		batch.Query(`DELETE FROM share_links_by_library WHERE org_id = ? AND library_id = ? AND link_token = ?`,
			orgID.String(), libraryID.String(), link.token)
		if err := batch.Exec(); err == nil {
			deletedTokens = append(deletedTokens, link.token)
		}
	}

	return deletedTokens, nil
}

// --- GC stats persistence ---

func (s *CassandraStore) SaveGCStats(key, value string) error {
	return s.db.Session().Query(`
		INSERT INTO gc_stats (stat_key, stat_value, updated_at) VALUES (?, ?, ?)
	`, key, value, time.Now()).Exec()
}

func (s *CassandraStore) LoadGCStats(key string) (string, error) {
	var value string
	err := s.db.Session().Query(`
		SELECT stat_value FROM gc_stats WHERE stat_key = ?
	`, key).Scan(&value)
	if err != nil {
		return "", err
	}
	return value, nil
}

// --- Storage adapter ---

// StorageManagerAdapter wraps a *storage.Manager to implement StorageProvider.
type StorageManagerAdapter struct {
	manager *storage.Manager
}

// NewStorageManagerAdapter wraps a *storage.Manager as a StorageProvider.
func NewStorageManagerAdapter(manager *storage.Manager) *StorageManagerAdapter {
	return &StorageManagerAdapter{manager: manager}
}

func (a *StorageManagerAdapter) GetBlockStore(storageClass string) (BlockStoreDeleter, error) {
	bs, err := a.manager.GetBlockStore(storageClass)
	if err != nil {
		return nil, err
	}
	return bs, nil
}
