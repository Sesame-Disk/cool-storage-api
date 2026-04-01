package gc

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
	"github.com/Sesame-Disk/sesamefs/internal/traffic"
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

// maxBatchSize is the maximum number of statements per Cassandra batch.
// Keeps batches within Cassandra's recommended size limits.
const maxBatchSize = 50

// --- Queue operations ---

func (s *CassandraStore) EnqueueItem(orgID uuid.UUID, queuedAt time.Time, itemType ItemType, itemID string, libraryID uuid.UUID, storageClass string, retryCount int) error {
	if err := s.db.Session().Query(`
		INSERT INTO gc_queue (org_id, queued_at, item_type, item_id, library_id, storage_class, retry_count)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, orgID.String(), queuedAt, string(itemType), itemID, libraryID.String(), storageClass, retryCount).Exec(); err != nil {
		return err
	}

	// Counter updates must be in a separate COUNTER batch (cannot mix with regular mutations)
	counterBatch := s.db.Session().Batch(gocql.CounterBatch)
	counterBatch.Query(`UPDATE gc_queue_stats SET queue_size = queue_size + 1 WHERE stat_key = ?`, orgID.String())
	counterBatch.Query(`UPDATE gc_queue_stats SET queue_size = queue_size + 1 WHERE stat_key = 'total'`)
	return counterBatch.Exec()
}

func (s *CassandraStore) EnqueueBatch(items []QueueItem) error {
	if len(items) == 0 {
		return nil
	}

	// Insert in chunks of maxBatchSize to stay within Cassandra batch limits
	for i := 0; i < len(items); i += maxBatchSize {
		end := i + maxBatchSize
		if end > len(items) {
			end = len(items)
		}
		chunk := items[i:end]

		batch := s.db.Session().Batch(gocql.LoggedBatch)
		for _, item := range chunk {
			batch.Query(`
				INSERT INTO gc_queue (org_id, queued_at, item_type, item_id, library_id, storage_class, retry_count)
				VALUES (?, ?, ?, ?, ?, ?, ?)
			`, item.OrgID.String(), item.QueuedAt, string(item.ItemType), item.ItemID, item.LibraryID.String(), item.StorageClass, item.RetryCount)
		}
		if err := batch.Exec(); err != nil {
			return fmt.Errorf("failed to enqueue batch chunk at offset %d: %w", i, err)
		}
	}

	// Counter updates in a separate COUNTER batch
	orgCounts := make(map[string]int)
	for _, item := range items {
		orgCounts[item.OrgID.String()]++
	}
	counterBatch := s.db.Session().Batch(gocql.CounterBatch)
	for orgIDStr, count := range orgCounts {
		counterBatch.Query(`UPDATE gc_queue_stats SET queue_size = queue_size + ? WHERE stat_key = ?`, int64(count), orgIDStr)
	}
	counterBatch.Query(`UPDATE gc_queue_stats SET queue_size = queue_size + ? WHERE stat_key = 'total'`, int64(len(items)))
	return counterBatch.Exec()
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
	// Delete the queue item. gc_queue already has a table-level TTL (7 days) which
	// limits tombstone lifespan. The DELETE is necessary because UPDATE USING TTL
	// only applies to the updated columns, leaving the rest of the row alive.
	if err := s.db.Session().Query(`
		DELETE FROM gc_queue
		WHERE org_id = ? AND queued_at = ? AND item_type = ? AND item_id = ?
	`, orgID.String(), queuedAt, string(itemType), itemID).Exec(); err != nil {
		return err
	}

	// Decrement counters in a separate COUNTER batch
	counterBatch := s.db.Session().Batch(gocql.CounterBatch)
	counterBatch.Query(`UPDATE gc_queue_stats SET queue_size = queue_size - 1 WHERE stat_key = ?`, orgID.String())
	counterBatch.Query(`UPDATE gc_queue_stats SET queue_size = queue_size - 1 WHERE stat_key = 'total'`)
	if err := counterBatch.Exec(); err != nil {
		log.Printf("[GC Store] Warning: failed to decrement queue counters for org %s: %v", orgID, err)
	}
	return nil
}

func (s *CassandraStore) UpdateRetryCount(orgID uuid.UUID, queuedAt time.Time, itemType ItemType, itemID string, retryCount int) error {
	return s.db.Session().Query(`
		UPDATE gc_queue SET retry_count = ?
		WHERE org_id = ? AND queued_at = ? AND item_type = ? AND item_id = ?
	`, retryCount, orgID.String(), queuedAt, string(itemType), itemID).Exec()
}

func (s *CassandraStore) GetQueueSize(orgID uuid.UUID) (int, error) {
	var count int64
	err := s.db.Session().Query(`
		SELECT queue_size FROM gc_queue_stats WHERE stat_key = ?
	`, orgID.String()).Scan(&count)
	if err != nil {
		if err == gocql.ErrNotFound {
			return 0, nil
		}
		return 0, fmt.Errorf("failed to get queue size: %w", err)
	}
	if count < 0 {
		count = 0
	}
	return int(count), nil
}

func (s *CassandraStore) GetTotalQueueSize() (int, error) {
	var count int64
	err := s.db.Session().Query(`
		SELECT queue_size FROM gc_queue_stats WHERE stat_key = 'total'
	`).Scan(&count)
	if err != nil {
		if err == gocql.ErrNotFound {
			return 0, nil
		}
		return 0, fmt.Errorf("failed to get total queue size: %w", err)
	}
	if count < 0 {
		count = 0
	}
	return int(count), nil
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

// MarkItemProcessed attempts to insert the taskID into the gc_processed_items table.
// The table has a default TTL of 48 hours so entries auto-expire.
// Returns applied=true if this is the first time (safe to proceed), false if already processed.
func (s *CassandraStore) MarkItemProcessed(taskID uuid.UUID) (bool, error) {
	// USING TTL must come before IF NOT EXISTS in CQL syntax.
	// We omit USING TTL here because the table already has default_time_to_live = 172800.
	applied, err := s.db.Session().Query(`
		INSERT INTO gc_processed_items (task_id) VALUES (?) IF NOT EXISTS
	`, taskID.String()).ScanCAS()
	return applied, err
}

// --- Block operations ---

func (s *CassandraStore) GetBlockRefCount(orgID uuid.UUID, blockID string) (int, error) {
	var refCount int
	err := s.db.Session().Query(`
		SELECT ref_count FROM blocks WHERE org_id = ? AND block_id = ?
	`, orgID.String(), blockID).Scan(&refCount)
	return refCount, err
}

// DeleteBlockIfUnreferenced atomically checks ref_count <= 0 and, if so, deletes the block.
// Uses a two-phase approach because CQL does not support DELETE ... IF <non-key condition>.
// Phase 1: UPDATE ... SET ref_count = -999 ... IF ref_count <= 0 (LWT marks for deletion)
// Phase 2: DELETE the row (unconditional, since we own it after the LWT)
// If ref_count > 0, the LWT fails and we skip deletion entirely.
func (s *CassandraStore) DeleteBlock(orgID uuid.UUID, blockID string) (bool, error) {
	// Phase 1: Atomically claim the block for deletion via LWT.
	// Setting ref_count to a sentinel value (-999) ensures that even if another
	// process reads the row between phase 1 and 2, it won't treat it as valid.
	var prevRefCount int
	applied, err := s.db.Session().Query(`
		UPDATE blocks SET ref_count = -999
		WHERE org_id = ? AND block_id = ?
		IF ref_count <= 0
	`, orgID.String(), blockID).ScanCAS(&prevRefCount)
	if err != nil {
		return false, err
	}
	if !applied {
		return false, nil
	}

	// Phase 2: The LWT succeeded — we own this row. Delete it unconditionally.
	if err := s.db.Session().Query(`
		DELETE FROM blocks WHERE org_id = ? AND block_id = ?
	`, orgID.String(), blockID).Exec(); err != nil {
		// If the DELETE fails, the row is left with ref_count = -999.
		// The scanner will find it (ref_count <= 0) and re-enqueue for cleanup.
		log.Printf("[GC Store] Warning: LWT succeeded but DELETE failed for block %s: %v", blockID, err)
		return true, err
	}
	return true, nil
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
	`, orgID, internalID).Iter()

	var mappings []BlockMapping
	var intID, extID string
	for iter.Scan(&intID, &extID) {
		mappings = append(mappings, BlockMapping{ExternalID: extID, InternalID: intID})
	}
	if err := iter.Close(); err != nil {
		// Log error but do NOT fallback to full org scan. Scanner will eventually clean this up.
		return nil, nil
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
		// Fallback to deleted_libraries table for permanently deleted libraries
		// that the garbage collector is trying to process.
		err = s.db.Session().Query(`
			SELECT org_id FROM deleted_libraries WHERE library_id = ?
		`, libraryID.String()).Scan(&orgIDStr)
		if err != nil {
			return uuid.Nil, err
		}
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

func (s *CassandraStore) DeleteShareLink(shareToken string, fallbackOrgID uuid.UUID, fallbackLibraryID uuid.UUID) error {
	// Read clustering keys from primary table for canonical delete + projection cleanup
	var orgID, createdBy, libraryID, linkType string
	var createdAt time.Time
	err := s.db.Session().Query(`
		SELECT org_id, created_by, library_id, created_at, link_type FROM share_links WHERE link_token = ?
	`, shareToken).Scan(&orgID, &createdBy, &libraryID, &createdAt, &linkType)
	if err != nil {
		// Primary record is gone. Attempt defensive cleanup of index tables
		// using the fallback org/library IDs from the queue item. This prevents
		// permanent orphans in the secondary index tables.
		if fallbackOrgID != uuid.Nil && fallbackLibraryID != uuid.Nil {
			log.Printf("[GC DeleteShareLink] Primary record missing for token %s, defensive cleanup of share_links_by_library", shareToken)
			s.db.Session().Query(`DELETE FROM share_links_by_library WHERE org_id = ? AND library_id = ? AND link_token = ?`,
				fallbackOrgID.String(), fallbackLibraryID.String(), shareToken).Exec()
		}
		return nil
	}

	// Delete canonical rows and admin projections.
	batch := s.db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`DELETE FROM share_links WHERE link_token = ?`, shareToken)
	batch.Query(`DELETE FROM share_links_by_creator WHERE org_id = ? AND created_by = ? AND created_at = ? AND link_token = ?`,
		orgID, createdBy, createdAt, shareToken)
	batch.Query(`DELETE FROM share_links_by_library WHERE org_id = ? AND library_id = ? AND link_token = ?`,
		orgID, libraryID, shareToken)
	db.AddDeleteAdminLinkReadModelQuery(batch, linkType, createdAt, orgID, shareToken)
	if err := batch.Exec(); err != nil {
		return err
	}
	db.BestEffortAdjustAdminOrgLinkCount(s.db.Session(), orgID, linkType, db.AdminOrgLinkCountDelta(-1))
	return nil
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
	iter := s.db.Session().Query(`
		SELECT path FROM locked_files WHERE repo_id = ?
	`, libraryID.String()).Iter()

	var paths []string
	var path string
	for iter.Scan(&path) {
		paths = append(paths, path)
	}
	iter.Close()

	// Batch deletes in chunks
	for i := 0; i < len(paths); i += maxBatchSize {
		end := i + maxBatchSize
		if end > len(paths) {
			end = len(paths)
		}
		batch := s.db.Session().Batch(gocql.UnloggedBatch)
		for _, p := range paths[i:end] {
			batch.Query(`DELETE FROM locked_files WHERE repo_id = ? AND path = ?`, libraryID.String(), p)
		}
		if err := batch.Exec(); err != nil {
			log.Printf("[GC Store] Warning: failed to batch delete locked files for library %s: %v", libraryID, err)
		}
	}
	return nil
}

func (s *CassandraStore) DeleteShareLinksByLibrary(orgID, libraryID uuid.UUID) ([]string, error) {
	// Use the by_library index for efficient lookup
	iter := s.db.Session().Query(`
		SELECT link_token, link_type, created_by, created_at FROM share_links_by_library
		WHERE org_id = ? AND library_id = ?
	`, orgID.String(), libraryID.String()).Iter()

	type linkInfo struct {
		token     string
		linkType  string
		createdBy string
		createdAt time.Time
	}

	var links []linkInfo
	var token, linkType, createdBy string
	var createdAt time.Time
	for iter.Scan(&token, &linkType, &createdBy, &createdAt) {
		links = append(links, linkInfo{token: token, linkType: linkType, createdBy: createdBy, createdAt: createdAt})
	}
	iter.Close()

	var deletedTokens []string
	for _, link := range links {
		batch := s.db.Session().Batch(gocql.LoggedBatch)
		batch.Query(`DELETE FROM share_links WHERE link_token = ?`, link.token)
		batch.Query(`DELETE FROM share_links_by_creator WHERE org_id = ? AND created_by = ? AND created_at = ? AND link_token = ?`,
			orgID.String(), link.createdBy, link.createdAt, link.token)
		batch.Query(`DELETE FROM share_links_by_library WHERE org_id = ? AND library_id = ? AND link_token = ?`,
			orgID.String(), libraryID.String(), link.token)
		db.AddDeleteAdminLinkReadModelQuery(batch, link.linkType, link.createdAt, orgID.String(), link.token)
		if err := batch.Exec(); err == nil {
			db.BestEffortAdjustAdminOrgLinkCount(s.db.Session(), orgID.String(), link.linkType, db.AdminOrgLinkCountDelta(-1))
			deletedTokens = append(deletedTokens, link.token)
		}
	}

	return deletedTokens, nil
}

// --- Starred files and monitored repos cleanup ---

func (s *CassandraStore) DeleteStarredFilesByLibrary(libraryID uuid.UUID) error {
	// starred_files has a secondary index on repo_id, so we can query by it
	iter := s.db.Session().Query(`
		SELECT user_id, path FROM starred_files WHERE repo_id = ?
	`, libraryID.String()).Iter()

	type starEntry struct {
		userID string
		path   string
	}
	var entries []starEntry
	var userID, path string
	for iter.Scan(&userID, &path) {
		entries = append(entries, starEntry{userID: userID, path: path})
	}
	iter.Close()

	for i := 0; i < len(entries); i += maxBatchSize {
		end := i + maxBatchSize
		if end > len(entries) {
			end = len(entries)
		}
		batch := s.db.Session().Batch(gocql.UnloggedBatch)
		for _, e := range entries[i:end] {
			batch.Query(`DELETE FROM starred_files WHERE user_id = ? AND repo_id = ? AND path = ?`,
				e.userID, libraryID.String(), e.path)
		}
		if err := batch.Exec(); err != nil {
			log.Printf("[GC Store] Warning: failed to batch delete starred files for library %s: %v", libraryID, err)
		}
	}
	return nil
}

func (s *CassandraStore) DeleteMonitoredReposByLibrary(libraryID uuid.UUID) error {
	// monitored_repos is partitioned by user_id — need to scan or use secondary index
	// We'll do a full scan filtered by repo_id (acceptable in GC context)
	iter := s.db.Session().Query(`
		SELECT user_id FROM monitored_repos WHERE repo_id = ? ALLOW FILTERING
	`, libraryID.String()).Iter()

	var userIDs []string
	var userID string
	for iter.Scan(&userID) {
		userIDs = append(userIDs, userID)
	}
	iter.Close()

	for i := 0; i < len(userIDs); i += maxBatchSize {
		end := i + maxBatchSize
		if end > len(userIDs) {
			end = len(userIDs)
		}
		batch := s.db.Session().Batch(gocql.UnloggedBatch)
		for _, uid := range userIDs[i:end] {
			batch.Query(`DELETE FROM monitored_repos WHERE user_id = ? AND repo_id = ?`,
				uid, libraryID.String())
		}
		if err := batch.Exec(); err != nil {
			log.Printf("[GC Store] Warning: failed to batch delete monitored repos for library %s: %v", libraryID, err)
		}
	}
	return nil
}

// --- Restore jobs cleanup by library ---

func (s *CassandraStore) DeleteRestoreJobsByLibrary(orgID, libraryID uuid.UUID) error {
	iter := s.db.Session().Query(`
		SELECT job_id FROM restore_jobs WHERE org_id = ? AND library_id = ?
	`, orgID.String(), libraryID.String()).Iter()

	var jobIDs []string
	var jobID string
	for iter.Scan(&jobID) {
		jobIDs = append(jobIDs, jobID)
	}
	iter.Close()

	for i := 0; i < len(jobIDs); i += maxBatchSize {
		end := i + maxBatchSize
		if end > len(jobIDs) {
			end = len(jobIDs)
		}
		batch := s.db.Session().Batch(gocql.UnloggedBatch)
		for _, jid := range jobIDs[i:end] {
			batch.Query(`DELETE FROM restore_jobs WHERE org_id = ? AND library_id = ? AND job_id = ?`,
				orgID.String(), libraryID.String(), jid)
		}
		if err := batch.Exec(); err != nil {
			log.Printf("[GC Store] Warning: failed to batch delete restore jobs for library %s: %v", libraryID, err)
		}
	}
	return nil
}

// --- Tag counter cleanup ---

func (s *CassandraStore) DeleteRepoTagCounters(libraryID uuid.UUID) error {
	return s.db.Session().Query(`
		DELETE FROM repo_tag_counters WHERE repo_id = ?
	`, libraryID.String()).Exec()
}

func (s *CassandraStore) DeleteFileTagCounters(libraryID uuid.UUID) error {
	return s.db.Session().Query(`
		DELETE FROM file_tag_counters WHERE repo_id = ?
	`, libraryID.String()).Exec()
}

func (s *CassandraStore) DeleteRepoTagFileCounts(libraryID uuid.UUID) error {
	// repo_tag_file_counts is a counter table partitioned by (repo_id), tag_id
	// We need to list tag_ids first, then delete each row
	iter := s.db.Session().Query(`
		SELECT tag_id FROM repo_tag_file_counts WHERE repo_id = ?
	`, libraryID.String()).Iter()

	var tagIDs []int
	var tagID int
	for iter.Scan(&tagID) {
		tagIDs = append(tagIDs, tagID)
	}
	iter.Close()

	for _, tid := range tagIDs {
		s.db.Session().Query(`DELETE FROM repo_tag_file_counts WHERE repo_id = ? AND tag_id = ?`,
			libraryID.String(), tid).Exec()
	}
	return nil
}

// --- Group shares cleanup ---

func (s *CassandraStore) ListSharesByGroup(groupID uuid.UUID) ([]GroupShareInfo, error) {
	var orgIDStr string
	if err := s.db.Session().Query(`
		SELECT org_id FROM groups_by_id WHERE group_id = ?
	`, groupID.String()).Scan(&orgIDStr); err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return []GroupShareInfo{}, nil
		}
		return nil, err
	}

	iter := s.db.Session().Query(`
		SELECT library_id, share_id FROM shares_by_group WHERE org_id = ? AND group_id = ?
	`, orgIDStr, groupID.String()).Iter()

	var results []GroupShareInfo
	var libIDStr, shareIDStr string
	for iter.Scan(&libIDStr, &shareIDStr) {
		results = append(results, GroupShareInfo{
			LibraryID:    parseUUID(libIDStr),
			ShareID:      parseUUID(shareIDStr),
			SharedTo:     groupID,
			OrgID:        parseUUID(orgIDStr),
			SharedToType: "group",
		})
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}

	return results, nil
}

// ListAllGroupShares returns all group shares for the scanner.
// This scans groups, then reads each group's partition from shares_by_group.
func (s *CassandraStore) ListAllGroupShares() ([]GroupShareInfo, error) {
	groupIter := s.db.Session().Query(`
		SELECT org_id, group_id FROM groups
	`).Iter()

	var results []GroupShareInfo
	var orgIDStr, groupIDStr string
	for groupIter.Scan(&orgIDStr, &groupIDStr) {
		shareIter := s.db.Session().Query(`
			SELECT library_id, share_id FROM shares_by_group WHERE org_id = ? AND group_id = ?
		`, orgIDStr, groupIDStr).Iter()
		var libIDStr, shareIDStr string
		for shareIter.Scan(&libIDStr, &shareIDStr) {
			results = append(results, GroupShareInfo{
				LibraryID:    parseUUID(libIDStr),
				ShareID:      parseUUID(shareIDStr),
				SharedTo:     parseUUID(groupIDStr),
				SharedToType: "group",
				OrgID:        parseUUID(orgIDStr),
			})
		}
		if err := shareIter.Close(); err != nil {
			return nil, fmt.Errorf("failed to list shares for group %s: %w", groupIDStr, err)
		}
	}
	if err := groupIter.Close(); err != nil {
		return nil, fmt.Errorf("failed to list group shares: %w", err)
	}
	return results, nil
}

func (s *CassandraStore) GroupExists(orgID, groupID uuid.UUID) (bool, error) {
	var name string
	err := s.db.Session().Query(`
		SELECT name FROM groups WHERE org_id = ? AND group_id = ?
	`, orgID.String(), groupID.String()).Scan(&name)
	if err != nil {
		return false, nil
	}
	return true, nil
}

// --- Audit log ---

func (s *CassandraStore) WriteAuditLog(entry AuditLogEntry) error {
	return s.db.Session().Query(`
		INSERT INTO audit_log (org_id, timestamp, action, target_type, target_id, actor_id, details)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, entry.OrgID.String(), entry.Timestamp, entry.Action, entry.TargetType,
		entry.TargetID, entry.ActorID, entry.Details).Exec()
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

// --- User cascade methods ---

func (s *CassandraStore) ListDeletedUsersExpired(graceDays int) ([]DeletedUserInfo, error) {
	cutoff := time.Now().AddDate(0, 0, -graceDays)

	// Scan all orgs for users with status='deleted' and deleted_at before cutoff
	orgIter := s.db.Session().Query(`SELECT org_id FROM organizations`).Iter()
	var orgIDStr string
	var result []DeletedUserInfo

	for orgIter.Scan(&orgIDStr) {
		orgID := parseUUID(orgIDStr)
		if orgID == uuid.Nil {
			continue
		}

		iter := s.db.Session().Query(`
			SELECT user_id, email, status, deleted_at FROM users WHERE org_id = ?
		`, orgIDStr).Iter()

		var userIDStr, email, status string
		var deletedAt *time.Time
		for iter.Scan(&userIDStr, &email, &status, &deletedAt) {
			if status != "deleted" || deletedAt == nil || deletedAt.IsZero() {
				continue
			}
			if deletedAt.Before(cutoff) {
				result = append(result, DeletedUserInfo{
					OrgID:     orgID,
					UserID:    parseUUID(userIDStr),
					Email:     email,
					DeletedAt: *deletedAt,
				})
			}
		}
		iter.Close()
	}
	orgIter.Close()

	return result, nil
}

func (s *CassandraStore) ListLibrariesByOwner(orgID, ownerID uuid.UUID) ([]uuid.UUID, error) {
	iter := s.db.Session().Query(`
		SELECT library_id, owner_id FROM libraries WHERE org_id = ?
	`, orgID.String()).Iter()

	var libIDStr, ownerIDStr string
	var result []uuid.UUID
	for iter.Scan(&libIDStr, &ownerIDStr) {
		if ownerIDStr == ownerID.String() {
			result = append(result, parseUUID(libIDStr))
		}
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *CassandraStore) SoftDeleteLibrary(orgID, libraryID, deletedBy uuid.UUID) error {
	// Look up the library owner for storage counter adjustment.
	var ownerID string
	_ = s.db.Session().Query(
		`SELECT owner_id FROM libraries WHERE org_id = ? AND library_id = ?`,
		orgID.String(), libraryID.String(),
	).Scan(&ownerID)

	now := time.Now()
	batch := s.db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		UPDATE libraries SET deleted_at = ?, deleted_by = ? WHERE org_id = ? AND library_id = ?
	`, now, deletedBy.String(), orgID.String(), libraryID.String())
	batch.Query(`
		INSERT INTO deleted_libraries (library_id, org_id, deleted_at) VALUES (?, ?, ?)
	`, libraryID.String(), orgID.String(), now)
	if err := batch.Exec(); err != nil {
		return err
	}

	// Adjust storage counters: subtract library's usage from aggregate scopes.
	if ownerID != "" {
		traffic.AdjustAggregateStorageCounters(s.db, orgID.String(), ownerID, libraryID.String(), false)
	}
	return nil
}

func (s *CassandraStore) ListGroupMembershipsByUser(orgID, userID uuid.UUID) ([]uuid.UUID, error) {
	iter := s.db.Session().Query(`
		SELECT group_id FROM groups_by_member WHERE org_id = ? AND user_id = ?
	`, orgID.String(), userID.String()).Iter()

	var groupIDStr string
	var result []uuid.UUID
	for iter.Scan(&groupIDStr) {
		result = append(result, parseUUID(groupIDStr))
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *CassandraStore) DeleteGroupMember(groupID, userID uuid.UUID) error {
	return s.db.Session().Query(`
		DELETE FROM group_members WHERE group_id = ? AND user_id = ?
	`, groupID.String(), userID.String()).Exec()
}

func (s *CassandraStore) DeleteGroupByMember(orgID, userID, groupID uuid.UUID) error {
	return s.db.Session().Query(`
		DELETE FROM groups_by_member WHERE org_id = ? AND user_id = ? AND group_id = ?
	`, orgID.String(), userID.String(), groupID.String()).Exec()
}

func (s *CassandraStore) ListSharesByUser(userID uuid.UUID) ([]ShareByUserInfo, error) {
	iter := s.db.Session().Query(`
		SELECT shared_to, library_id FROM shares_by_user WHERE shared_to = ?
	`, userID.String()).Iter()

	var sharedToStr, libIDStr string
	var result []ShareByUserInfo
	for iter.Scan(&sharedToStr, &libIDStr) {
		result = append(result, ShareByUserInfo{
			SharedTo:  parseUUID(sharedToStr),
			LibraryID: parseUUID(libIDStr),
		})
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *CassandraStore) DeleteStarredFilesByUser(userID uuid.UUID) error {
	return s.db.Session().Query(`
		DELETE FROM starred_files WHERE user_id = ?
	`, userID.String()).Exec()
}

func (s *CassandraStore) DeleteMonitoredReposByUser(userID uuid.UUID) error {
	return s.db.Session().Query(`
		DELETE FROM monitored_repos WHERE user_id = ?
	`, userID.String()).Exec()
}

func (s *CassandraStore) HardDeleteUser(orgID, userID uuid.UUID, email string) error {
	batch := s.db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		DELETE FROM users WHERE org_id = ? AND user_id = ?
	`, orgID.String(), userID.String())
	if email != "" {
		batch.Query(`
			DELETE FROM users_by_email WHERE email = ?
		`, email)
	}
	return batch.Exec()
}

func (s *CassandraStore) GetUserEmail(orgID, userID uuid.UUID) (string, error) {
	var email string
	err := s.db.Session().Query(`
		SELECT email FROM users WHERE org_id = ? AND user_id = ?
	`, orgID.String(), userID.String()).Scan(&email)
	if err != nil {
		return "", err
	}
	return email, nil
}

// --- Library trash auto-purge (Fase 3) ---

func (s *CassandraStore) ListExpiredDeletedLibraries(retentionDays int) ([]DeletedLibraryInfo, error) {
	iter := s.db.Session().Query(`
		SELECT library_id, org_id, deleted_at FROM deleted_libraries
	`).Iter()

	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	var libIDStr, orgIDStr string
	var deletedAt time.Time
	var result []DeletedLibraryInfo
	for iter.Scan(&libIDStr, &orgIDStr, &deletedAt) {
		if deletedAt.Before(cutoff) {
			orgID := parseUUID(orgIDStr)
			libID := parseUUID(libIDStr)
			storageClass, _ := s.GetLibraryStorageClass(orgID, libID)
			result = append(result, DeletedLibraryInfo{
				OrgID:        orgID,
				LibraryID:    libID,
				StorageClass: storageClass,
				DeletedAt:    deletedAt,
			})
		}
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *CassandraStore) HardDeleteLibrary(orgID, libraryID uuid.UUID) error {
	batch := s.db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`DELETE FROM libraries WHERE org_id = ? AND library_id = ?`,
		orgID.String(), libraryID.String())
	batch.Query(`DELETE FROM deleted_libraries WHERE library_id = ?`,
		libraryID.String())
	return batch.Exec()
}

// --- Org cascade (Fase 4) ---

func (s *CassandraStore) ListExpiredDeletedOrgs(graceDays int) ([]DeletedOrgInfo, error) {
	iter := s.db.Session().Query(`
		SELECT org_id, name, status, deleted_at FROM organizations
	`).Iter()

	cutoff := time.Now().AddDate(0, 0, -graceDays)
	var orgIDStr, name, status string
	var deletedAt *time.Time
	var result []DeletedOrgInfo
	for iter.Scan(&orgIDStr, &name, &status, &deletedAt) {
		if status != "deleted" || deletedAt == nil || deletedAt.IsZero() {
			continue
		}
		if deletedAt.Before(cutoff) {
			result = append(result, DeletedOrgInfo{
				OrgID:     parseUUID(orgIDStr),
				Name:      name,
				DeletedAt: *deletedAt,
			})
		}
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *CassandraStore) ListUsersByOrg(orgID uuid.UUID) ([]OrgUserInfo, error) {
	iter := s.db.Session().Query(`
		SELECT user_id, email FROM users WHERE org_id = ?
	`, orgID.String()).Iter()

	var userIDStr, email string
	var result []OrgUserInfo
	for iter.Scan(&userIDStr, &email) {
		result = append(result, OrgUserInfo{
			UserID: parseUUID(userIDStr),
			Email:  email,
		})
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *CassandraStore) ListGroupsByOrg(orgID uuid.UUID) ([]uuid.UUID, error) {
	iter := s.db.Session().Query(`
		SELECT group_id FROM groups WHERE org_id = ?
	`, orgID.String()).Iter()

	var groupIDStr string
	var result []uuid.UUID
	for iter.Scan(&groupIDStr) {
		result = append(result, parseUUID(groupIDStr))
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *CassandraStore) ListLibrariesForOrg(orgID uuid.UUID) ([]OrgLibraryInfo, error) {
	iter := s.db.Session().Query(`
		SELECT library_id, storage_class, owner_id, deleted_at FROM libraries WHERE org_id = ?
	`, orgID.String()).Iter()

	var libIDStr, storageClass, ownerIDStr string
	var deletedAt time.Time
	var result []OrgLibraryInfo
	for iter.Scan(&libIDStr, &storageClass, &ownerIDStr, &deletedAt) {
		result = append(result, OrgLibraryInfo{
			LibraryID:    parseUUID(libIDStr),
			StorageClass: storageClass,
			OwnerID:      parseUUID(ownerIDStr),
			DeletedAt:    deletedAt,
		})
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *CassandraStore) DeleteLibraryStorageCounter(orgID, libraryID uuid.UUID) error {
	traffic.DeleteLibraryStorageCounter(s.db, orgID.String(), libraryID.String())
	return nil
}

func (s *CassandraStore) DeleteGroupFull(orgID, groupID uuid.UUID) error {
	// Clean up groups_by_member for each member
	iter := s.db.Session().Query(`
		SELECT user_id FROM group_members WHERE group_id = ?
	`, groupID.String()).Iter()

	var userIDStr string
	for iter.Scan(&userIDStr) {
		s.db.Session().Query(`
			DELETE FROM groups_by_member WHERE org_id = ? AND user_id = ? AND group_id = ?
		`, orgID.String(), userIDStr, groupID.String()).Exec()
	}
	iter.Close()

	// Delete members, group record, and by_id lookup
	batch := s.db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`DELETE FROM group_members WHERE group_id = ?`, groupID.String())
	batch.Query(`DELETE FROM groups WHERE org_id = ? AND group_id = ?`, orgID.String(), groupID.String())
	batch.Query(`DELETE FROM groups_by_id WHERE group_id = ?`, groupID.String())
	return batch.Exec()
}

func (s *CassandraStore) HardDeleteOrg(orgID uuid.UUID) error {
	return s.db.Session().Query(`
		DELETE FROM organizations WHERE org_id = ?
	`, orgID.String()).Exec()
}

func (s *CassandraStore) GetOrgName(orgID uuid.UUID) (string, error) {
	var name string
	err := s.db.Session().Query(`
		SELECT name FROM organizations WHERE org_id = ?
	`, orgID.String()).Scan(&name)
	return name, err
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
