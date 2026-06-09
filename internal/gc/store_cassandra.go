package gc

import (
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"log"
	"sort"
	"strconv"
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

func parseCASUUID(value interface{}) uuid.UUID {
	switch v := value.(type) {
	case uuid.UUID:
		return v
	case string:
		return parseUUID(v)
	case []byte:
		id, err := uuid.FromBytes(v)
		if err != nil {
			return uuid.Nil
		}
		return id
	case fmt.Stringer:
		return parseUUID(v.String())
	default:
		return parseUUID(fmt.Sprint(v))
	}
}

func parseCASTime(value interface{}) time.Time {
	switch v := value.(type) {
	case time.Time:
		return v
	case string:
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			return t
		}
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			return t
		}
	}
	return time.Time{}
}

func parseCASString(value interface{}) string {
	switch v := value.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	case fmt.Stringer:
		return v.String()
	case nil:
		return ""
	default:
		return fmt.Sprint(v)
	}
}

func (s *CassandraStore) acquireHardDeleteLock(tableName, keyColumn string, keyValue, leaseToken uuid.UUID) (bool, error) {
	now := time.Now().UTC()
	existing := map[string]interface{}{}
	applied, err := s.db.Session().Query(fmt.Sprintf(`
		INSERT INTO %s (%s, started_at, heartbeat, lease_token)
		VALUES (?, ?, ?, ?) IF NOT EXISTS
	`, tableName, keyColumn), keyValue.String(), now, now, leaseToken.String()).MapScanCAS(existing)
	if err != nil || applied {
		return applied, err
	}

	heartbeat := parseCASTime(existing["heartbeat"])
	existingToken := parseCASUUID(existing["lease_token"])
	if heartbeat.IsZero() || existingToken == uuid.Nil || now.Sub(heartbeat) < hardDeleteLockStaleAfter {
		return false, nil
	}

	applied, err = s.db.Session().Query(fmt.Sprintf(`
		UPDATE %s USING TTL 21600
		SET started_at = ?, heartbeat = ?, lease_token = ?
		WHERE %s = ? IF lease_token = ?
	`, tableName, keyColumn), now, now, leaseToken.String(), keyValue.String(), existingToken.String()).MapScanCAS(map[string]interface{}{})
	if err != nil {
		return false, err
	}
	return applied, nil
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

const gcDeletedUsersCursorKey = "gc.scan.expired_deleted_users.last_deleted_day"
const gcExpiredShareLinksCursorKey = "gc.scan.expired_share_links.last_expiry_day"
const gcExpiredSharesCursorKey = "gc.scan.expired_shares.last_expiry_day"
const gcBlockCandidatesCursorKey = "gc.scan.block_candidates.last_candidate_day"
const gcProvisionalBlockRefsCursorKey = "gc.scan.provisional_block_refs.last_expiry_day"
const gcS3OrphansCursorKey = "gc.scan.s3_orphans.last_first_seen_day"
const gcFailedItemsExpiryCursorKey = "gc.scan.failed_items.last_expiry_day"

const (
	gcInitialScanLookbackDays             = 7
	gcFailedItemExpiryInitialLookbackDays = 45
	gcScanOverlapDays                     = 2
)

// NewCassandraStore creates a new CassandraStore.
func NewCassandraStore(database *db.DB) *CassandraStore {
	return &CassandraStore{db: database}
}

// maxBatchSize is the maximum number of statements per Cassandra batch.
// Keeps batches within Cassandra's recommended size limits.
const maxBatchSize = 50

const gcDefaultOrgBucketCount = 32
const gcDefaultQueueBucketCount = 32

const (
	gcStatsKeyTotalQueue  = "total_queue_depth"
	gcStatsKeyTotalFailed = "total_failed_items"
)

func gcOrgBucket(orgID uuid.UUID) int {
	h := fnv.New32a()
	_, _ = h.Write(orgID[:])
	return int(h.Sum32() % gcDefaultOrgBucketCount)
}

// OrgBucket returns the Cassandra bucket used by org-level GC projections.
func OrgBucket(orgID uuid.UUID) int {
	return gcOrgBucket(orgID)
}

func gcQueueBucket(orgID uuid.UUID, itemType ItemType, itemID string) int {
	h := fnv.New32a()
	_, _ = h.Write(orgID[:])
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(itemType))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(itemID))
	return int(h.Sum32() % gcDefaultQueueBucketCount)
}

// QueueBucket returns the Cassandra bucket used by the live GC queue.
func QueueBucket(orgID uuid.UUID, itemType ItemType, itemID string) int {
	return gcQueueBucket(orgID, itemType, itemID)
}

func gcPendingItemBucket(orgID, libraryID uuid.UUID, itemType ItemType, itemID string) int {
	h := fnv.New32a()
	_, _ = h.Write(orgID[:])
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(libraryID[:])
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(itemType))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(itemID))
	return int(h.Sum32() % gcDefaultQueueBucketCount)
}

// PendingItemBucket returns the Cassandra bucket used by gc_pending_items.
func PendingItemBucket(orgID, libraryID uuid.UUID, itemType ItemType, itemID string) int {
	return gcPendingItemBucket(orgID, libraryID, itemType, itemID)
}

func (s *CassandraStore) loadGCStatInt(key string) (int, bool, error) {
	value, err := s.LoadGCStats(key)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return 0, false, nil
		}
		return 0, false, err
	}
	if value == "" {
		return 0, true, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, true, fmt.Errorf("parse gc_stats[%s]=%q: %w", key, value, err)
	}
	if parsed < 0 {
		parsed = 0
	}
	return parsed, true, nil
}

func (s *CassandraStore) scanOrgQueueStats(orgID uuid.UUID) (GCOrgStats, error) {
	stats := GCOrgStats{OrgID: orgID}
	var oldestQueuedAt *time.Time

	for bucket := 0; bucket < gcDefaultQueueBucketCount; bucket++ {
		iter := s.db.Session().Query(`
			SELECT queued_at
			FROM gc_queue
			WHERE org_id = ? AND bucket = ?
		`, orgID.String(), bucket).Iter()
		var queuedAt time.Time
		for iter.Scan(&queuedAt) {
			stats.QueueDepth++
			queuedAtCopy := queuedAt.UTC()
			if oldestQueuedAt == nil || queuedAtCopy.Before(*oldestQueuedAt) {
				oldestQueuedAt = &queuedAtCopy
			}
		}
		if err := iter.Close(); err != nil {
			return GCOrgStats{}, fmt.Errorf("scan queue depth org=%s bucket=%d: %w", orgID, bucket, err)
		}
	}

	iter := s.db.Session().Query(`
		SELECT failed_at
		FROM gc_failed_items
		WHERE org_id = ?
	`, orgID.String()).Iter()
	var failedAt time.Time
	for iter.Scan(&failedAt) {
		stats.FailedDepth++
	}
	if err := iter.Close(); err != nil {
		return GCOrgStats{}, fmt.Errorf("scan failed depth org=%s: %w", orgID, err)
	}

	stats.OldestQueuedAt = oldestQueuedAt
	return stats, nil
}

// --- Queue operations ---

func (s *CassandraStore) EnqueueItem(orgID uuid.UUID, queuedAt time.Time, itemType ItemType, itemID string, libraryID uuid.UUID, storageClass string, retryCount int) error {
	now := time.Now().UTC()
	queueBucket := gcQueueBucket(orgID, itemType, itemID)
	batch := s.db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		INSERT INTO gc_queue (org_id, bucket, queued_at, identity_at, requires_library_deleted_check, item_type, item_id, library_id, storage_class, retry_count)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, orgID.String(), queueBucket, queuedAt, queuedAt, false, string(itemType), itemID, libraryID.String(), storageClass, retryCount)
	addPendingItemBatchQuery(batch, orgID, libraryID, itemType, itemID, queuedAt)
	batch.Query(`
		INSERT INTO gc_active_orgs (bucket, org_id, last_enqueued_at)
		VALUES (?, ?, ?)
	`, gcOrgBucket(orgID), orgID.String(), now)
	batch.Query(`
		INSERT INTO gc_dirty_orgs (bucket, org_id, marked_at)
		VALUES (?, ?, ?)
	`, gcOrgBucket(orgID), orgID.String(), now)
	return batch.Exec()
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
		activeAtByOrg := make(map[string]time.Time)
		for _, item := range chunk {
			identityAt := effectiveIdentityAt(item.QueuedAt, item.IdentityAt)
			queueBucket := gcQueueBucket(item.OrgID, item.ItemType, item.ItemID)
			batch.Query(`
				INSERT INTO gc_queue (org_id, bucket, queued_at, identity_at, requires_library_deleted_check, item_type, item_id, library_id, storage_class, retry_count)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			`, item.OrgID.String(), queueBucket, item.QueuedAt, identityAt, item.RequiresLibraryDeletedCheck, string(item.ItemType), item.ItemID, item.LibraryID.String(), item.StorageClass, item.RetryCount)
			addPendingItemBatchQuery(batch, item.OrgID, item.LibraryID, item.ItemType, item.ItemID, identityAt)
			activeAtByOrg[item.OrgID.String()] = time.Now().UTC()
		}
		for orgIDStr, activeAt := range activeAtByOrg {
			batch.Query(`
				INSERT INTO gc_active_orgs (bucket, org_id, last_enqueued_at)
				VALUES (?, ?, ?)
			`, gcOrgBucket(parseUUID(orgIDStr)), orgIDStr, activeAt)
			batch.Query(`
				INSERT INTO gc_dirty_orgs (bucket, org_id, marked_at)
				VALUES (?, ?, ?)
			`, gcOrgBucket(parseUUID(orgIDStr)), orgIDStr, activeAt)
		}
		if err := batch.Exec(); err != nil {
			return fmt.Errorf("failed to enqueue batch chunk at offset %d: %w", i, err)
		}
	}
	return nil
}

func (s *CassandraStore) QueueItemExists(orgID uuid.UUID, queuedAt time.Time, itemType ItemType, itemID string) (bool, error) {
	var existingItemID string
	err := s.db.Session().Query(`
		SELECT item_id FROM gc_queue
		WHERE org_id = ? AND bucket = ? AND queued_at = ? AND item_type = ? AND item_id = ?
	`, orgID.String(), gcQueueBucket(orgID, itemType, itemID), queuedAt, string(itemType), itemID).Scan(&existingItemID)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (s *CassandraStore) PendingItemExists(orgID, libraryID uuid.UUID, identityAt time.Time, itemType ItemType, itemID string) (bool, error) {
	var existingItemID string
	query := `
		SELECT item_id FROM gc_pending_items
		WHERE org_id = ? AND bucket = ? AND item_type = ? AND library_id = ? AND item_id = ?
	`
	args := []interface{}{orgID.String(), gcPendingItemBucket(orgID, libraryID, itemType, itemID), string(itemType), libraryID.String(), itemID}
	if !identityAt.IsZero() {
		query += ` AND identity_at = ?`
		args = append(args, identityAt)
	}
	query += ` LIMIT 1`
	err := s.db.Session().Query(query, args...).Scan(&existingItemID)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func addPendingItemBatchQuery(batch *gocql.Batch, orgID, libraryID uuid.UUID, itemType ItemType, itemID string, identityAt time.Time) {
	batch.Query(`
		INSERT INTO gc_pending_items (org_id, bucket, item_type, library_id, item_id, identity_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, orgID.String(), gcPendingItemBucket(orgID, libraryID, itemType, itemID), string(itemType), libraryID.String(), itemID, identityAt)
}

func addPendingItemDeleteBatchQuery(batch *gocql.Batch, orgID, libraryID uuid.UUID, itemType ItemType, itemID string, identityAt time.Time) {
	batch.Query(`
		DELETE FROM gc_pending_items
		WHERE org_id = ? AND bucket = ? AND item_type = ? AND library_id = ? AND item_id = ? AND identity_at = ?
	`, orgID.String(), gcPendingItemBucket(orgID, libraryID, itemType, itemID), string(itemType), libraryID.String(), itemID, identityAt)
}

func (s *CassandraStore) queueItemPendingInfo(orgID uuid.UUID, queuedAt time.Time, itemType ItemType, itemID string) (time.Time, uuid.UUID, error) {
	var identityAt time.Time
	var libraryIDStr string
	err := s.db.Session().Query(`
		SELECT identity_at, library_id FROM gc_queue
		WHERE org_id = ? AND bucket = ? AND queued_at = ? AND item_type = ? AND item_id = ?
	`, orgID.String(), gcQueueBucket(orgID, itemType, itemID), queuedAt, string(itemType), itemID).Scan(&identityAt, &libraryIDStr)
	if err != nil {
		return time.Time{}, uuid.Nil, err
	}
	return identityAt, parseUUID(libraryIDStr), nil
}

type failedItemRow struct {
	QueuedAt                    time.Time
	IdentityAt                  time.Time
	ExpiresAt                   time.Time
	RequiresLibraryDeletedCheck bool
	LibraryID                   uuid.UUID
	StorageClass                string
}

func (s *CassandraStore) failedItemInfo(orgID uuid.UUID, failedAt time.Time, itemType ItemType, itemID string) (failedItemRow, error) {
	var row failedItemRow
	var libraryIDStr string
	err := s.db.Session().Query(`
		SELECT queued_at, identity_at, expires_at, requires_library_deleted_check, library_id, storage_class FROM gc_failed_items
		WHERE org_id = ? AND failed_at = ? AND item_type = ? AND item_id = ?
	`, orgID.String(), failedAt, string(itemType), itemID).Scan(&row.QueuedAt, &row.IdentityAt, &row.ExpiresAt, &row.RequiresLibraryDeletedCheck, &libraryIDStr, &row.StorageClass)
	if err != nil {
		return failedItemRow{}, err
	}
	row.LibraryID = parseUUID(libraryIDStr)
	return row, nil
}

func (s *CassandraStore) DequeueBatch(orgID uuid.UUID, batchSize int, cutoff time.Time) ([]QueueItem, error) {
	if batchSize <= 0 {
		return nil, nil
	}

	var items []QueueItem
	for bucket := 0; bucket < gcDefaultQueueBucketCount; bucket++ {
		iter := s.db.Session().Query(`
			SELECT org_id, queued_at, identity_at, requires_library_deleted_check, item_type, item_id, library_id, storage_class, retry_count
			FROM gc_queue
			WHERE org_id = ? AND bucket = ? AND queued_at < ?
			LIMIT ?
		`, orgID.String(), bucket, cutoff, batchSize).Iter()

		var orgIDStr, itemTypeStr, itemID, libIDStr, storageClass string
		var queuedAt, identityAt time.Time
		var requiresLibraryDeletedCheck bool
		var retryCount int

		for iter.Scan(&orgIDStr, &queuedAt, &identityAt, &requiresLibraryDeletedCheck, &itemTypeStr, &itemID,
			&libIDStr, &storageClass, &retryCount) {
			items = append(items, QueueItem{
				OrgID:                       parseUUID(orgIDStr),
				QueuedAt:                    queuedAt,
				IdentityAt:                  effectiveIdentityAt(queuedAt, identityAt),
				RequiresLibraryDeletedCheck: requiresLibraryDeletedCheck,
				ItemType:                    ItemType(itemTypeStr),
				ItemID:                      itemID,
				LibraryID:                   parseUUID(libIDStr),
				StorageClass:                storageClass,
				RetryCount:                  retryCount,
			})
		}

		if err := iter.Close(); err != nil {
			return nil, fmt.Errorf("failed to dequeue batch from bucket %d: %w", bucket, err)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].QueuedAt.Equal(items[j].QueuedAt) {
			if items[i].ItemType == items[j].ItemType {
				return items[i].ItemID < items[j].ItemID
			}
			return items[i].ItemType < items[j].ItemType
		}
		return items[i].QueuedAt.Before(items[j].QueuedAt)
	})
	if batchSize > 0 && len(items) > batchSize {
		items = items[:batchSize]
	}
	return items, nil
}

func (s *CassandraStore) CompleteItem(orgID uuid.UUID, queuedAt time.Time, itemType ItemType, itemID string) error {
	identityAt, libraryID, err := s.queueItemPendingInfo(orgID, queuedAt, itemType, itemID)
	hadQueueRow := err == nil
	if err != nil && !errors.Is(err, gocql.ErrNotFound) {
		return fmt.Errorf("load queue identity for complete %s/%s: %w", orgID, itemID, err)
	}
	now := time.Now().UTC()
	batch := s.db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		DELETE FROM gc_queue
		WHERE org_id = ? AND bucket = ? AND queued_at = ? AND item_type = ? AND item_id = ?
	`, orgID.String(), gcQueueBucket(orgID, itemType, itemID), queuedAt, string(itemType), itemID)
	addPendingItemDeleteBatchQuery(batch, orgID, libraryID, itemType, itemID, effectiveIdentityAt(queuedAt, identityAt))
	batch.Query(`
		INSERT INTO gc_dirty_orgs (bucket, org_id, marked_at)
		VALUES (?, ?, ?)
	`, gcOrgBucket(orgID), orgID.String(), now)
	if err := batch.Exec(); err != nil {
		return err
	}
	if !hadQueueRow {
		return nil
	}
	return nil
}

// RequeueItem moves a failed item to the back of the queue to prevent head-of-line blocking.
// It deletes the old queue record and inserts a new one with a new queued_at timestamp and incremented retry count.
func (s *CassandraStore) RequeueItem(orgID uuid.UUID, oldQueuedAt, newQueuedAt time.Time, itemType ItemType, itemID string, libraryID uuid.UUID, storageClass string, newRetryCount int, identityAt time.Time, requiresLibraryDeletedCheck bool) error {
	now := time.Now().UTC()
	batch := s.db.Session().Batch(gocql.LoggedBatch)

	// Delete old item
	batch.Query(`
		DELETE FROM gc_queue
		WHERE org_id = ? AND bucket = ? AND queued_at = ? AND item_type = ? AND item_id = ?
	`, orgID.String(), gcQueueBucket(orgID, itemType, itemID), oldQueuedAt, string(itemType), itemID)

	// Insert new item at the end of the queue
	batch.Query(`
		INSERT INTO gc_queue (org_id, bucket, queued_at, identity_at, requires_library_deleted_check, item_type, item_id, library_id, storage_class, retry_count)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, orgID.String(), gcQueueBucket(orgID, itemType, itemID), newQueuedAt, effectiveIdentityAt(oldQueuedAt, identityAt), requiresLibraryDeletedCheck, string(itemType), itemID, libraryID.String(), storageClass, newRetryCount)
	addPendingItemBatchQuery(batch, orgID, libraryID, itemType, itemID, effectiveIdentityAt(oldQueuedAt, identityAt))
	batch.Query(`
		INSERT INTO gc_active_orgs (bucket, org_id, last_enqueued_at)
		VALUES (?, ?, ?)
	`, gcOrgBucket(orgID), orgID.String(), now)
	batch.Query(`
		INSERT INTO gc_dirty_orgs (bucket, org_id, marked_at)
		VALUES (?, ?, ?)
	`, gcOrgBucket(orgID), orgID.String(), now)

	return batch.Exec()
}

func (s *CassandraStore) FailItem(item QueueItem, failedAt time.Time, lastError, failureCode string) error {
	identityAt, libraryID, err := s.queueItemPendingInfo(item.OrgID, item.QueuedAt, item.ItemType, item.ItemID)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			log.Printf("[GC] Skipping DLQ move for missing queue row org=%s item_type=%s item_id=%s queued_at=%s", item.OrgID, item.ItemType, item.ItemID, item.QueuedAt.Format(time.RFC3339Nano))
			return nil
		}
		return fmt.Errorf("load queue identity for fail %s/%s: %w", item.OrgID, item.ItemID, err)
	}
	expiresAt := failedAt.UTC().Add(gcFailedItemRetention)
	effectiveIdentity := effectiveIdentityAt(item.QueuedAt, identityAt)
	queueBucket := gcQueueBucket(item.OrgID, item.ItemType, item.ItemID)
	now := time.Now().UTC()

	batch := s.db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		INSERT INTO gc_failed_items (
			org_id, failed_at, expires_at, queued_at, identity_at, requires_library_deleted_check, item_type, item_id, library_id, storage_class, retry_count, last_error, failure_code, resolution_status
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, item.OrgID.String(), failedAt, expiresAt, item.QueuedAt, effectiveIdentity, item.RequiresLibraryDeletedCheck, string(item.ItemType), item.ItemID, libraryID.String(), item.StorageClass, item.RetryCount, lastError, failureCode, "open")
	db.AddUpsertFailedItemExpiryQuery(batch, item.OrgID.String(), failedAt, string(item.ItemType), item.ItemID, expiresAt)
	addPendingItemBatchQuery(batch, item.OrgID, libraryID, item.ItemType, item.ItemID, effectiveIdentity)
	batch.Query(`
		DELETE FROM gc_queue
		WHERE org_id = ? AND bucket = ? AND queued_at = ? AND item_type = ? AND item_id = ?
	`, item.OrgID.String(), queueBucket, item.QueuedAt, string(item.ItemType), item.ItemID)
	batch.Query(`
		INSERT INTO gc_dirty_orgs (bucket, org_id, marked_at)
		VALUES (?, ?, ?)
	`, gcOrgBucket(item.OrgID), item.OrgID.String(), now)
	return batch.Exec()
}

func (s *CassandraStore) GetQueueSize(orgID uuid.UUID) (int, error) {
	stats, err := s.GetOrgQueueStats(orgID)
	if err != nil {
		return 0, fmt.Errorf("failed to get queue size for %s: %w", orgID, err)
	}
	return stats.QueueDepth, nil
}

func (s *CassandraStore) GetTotalQueueSize() (int, error) {
	count, found, err := s.loadGCStatInt(gcStatsKeyTotalQueue)
	if err != nil {
		return 0, fmt.Errorf("failed to get total queue size: %w", err)
	}
	if found {
		return count, nil
	}
	return 0, fmt.Errorf("gc queue snapshot missing")
}

func (s *CassandraStore) GetTotalFailedItems() (int, error) {
	count, found, err := s.loadGCStatInt(gcStatsKeyTotalFailed)
	if err != nil {
		return 0, fmt.Errorf("failed to get total failed items: %w", err)
	}
	if found {
		return count, nil
	}
	return 0, fmt.Errorf("gc failed-item snapshot missing")
}

func (s *CassandraStore) ListFailedItems(orgID uuid.UUID, limit int) ([]GCFailedItemInfo, error) {
	query := `
		SELECT failed_at, expires_at, queued_at, identity_at, requires_library_deleted_check, item_type, item_id, library_id, storage_class, retry_count, last_error, failure_code, resolution_status, resolved_at
		FROM gc_failed_items WHERE org_id = ?
	`
	if limit > 0 {
		query += ` LIMIT ?`
	}
	var iter *gocql.Iter
	if limit > 0 {
		iter = s.db.Session().Query(query, orgID.String(), limit).Iter()
	} else {
		iter = s.db.Session().Query(query, orgID.String()).Iter()
	}
	var items []GCFailedItemInfo
	var (
		failedAt                    time.Time
		expiresAt                   time.Time
		queuedAt                    time.Time
		identityAt                  time.Time
		requiresLibraryDeletedCheck bool
		itemType                    string
		itemID                      string
		libraryIDStr                string
		storageClass                string
		retryCount                  int
		lastError                   string
		failureCode                 string
		resolutionStatus            string
		resolvedAt                  *time.Time
	)
	for iter.Scan(&failedAt, &expiresAt, &queuedAt, &identityAt, &requiresLibraryDeletedCheck, &itemType, &itemID, &libraryIDStr, &storageClass, &retryCount, &lastError, &failureCode, &resolutionStatus, &resolvedAt) {
		items = append(items, GCFailedItemInfo{
			OrgID:                       orgID,
			FailedAt:                    failedAt,
			ExpiresAt:                   expiresAt,
			QueuedAt:                    queuedAt,
			IdentityAt:                  effectiveIdentityAt(queuedAt, identityAt),
			RequiresLibraryDeletedCheck: requiresLibraryDeletedCheck,
			ItemType:                    ItemType(itemType),
			ItemID:                      itemID,
			LibraryID:                   parseUUID(libraryIDStr),
			StorageClass:                storageClass,
			RetryCount:                  retryCount,
			LastError:                   lastError,
			FailureCode:                 failureCode,
			ResolvedState:               resolutionStatus,
			ResolvedAt:                  resolvedAt,
		})
	}
	if err := iter.Close(); err != nil {
		return nil, fmt.Errorf("list failed items for %s: %w", orgID, err)
	}
	return items, nil
}

func (s *CassandraStore) ListFailedItemExpiriesByDay(day time.Time, bucket int) ([]GCFailedItemExpiryInfo, error) {
	iter := s.db.Session().Query(`
		SELECT expires_at, org_id, failed_at, item_type, item_id
		FROM gc_failed_items_by_expiry
		WHERE expiry_day = ? AND bucket = ?
	`, db.GCProjectionUTCDate(day), bucket).Iter()
	var expiries []GCFailedItemExpiryInfo
	var (
		expiresAt time.Time
		orgIDStr  string
		failedAt  time.Time
		itemType  string
		itemID    string
	)
	for iter.Scan(&expiresAt, &orgIDStr, &failedAt, &itemType, &itemID) {
		expiries = append(expiries, GCFailedItemExpiryInfo{
			OrgID:     parseUUID(orgIDStr),
			FailedAt:  failedAt,
			ExpiresAt: expiresAt,
			ItemType:  ItemType(itemType),
			ItemID:    itemID,
		})
	}
	if err := iter.Close(); err != nil {
		return nil, fmt.Errorf("list failed item expiries day=%s bucket=%d: %w", db.GCProjectionDateString(day), bucket, err)
	}
	return expiries, nil
}

// latestFailedAt returns the most recent failure time for an org. gc_failed_items
// is clustered by failed_at DESC, so LIMIT 1 is a cheap single-row read of the
// newest DLQ entry. Returns the zero time when the org has no failed items.
func (s *CassandraStore) latestFailedAt(orgID uuid.UUID) (time.Time, error) {
	var failedAt time.Time
	err := s.db.Session().Query(`
		SELECT failed_at FROM gc_failed_items WHERE org_id = ? LIMIT 1
	`, orgID.String()).Scan(&failedAt)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return time.Time{}, nil
		}
		return time.Time{}, err
	}
	return failedAt, nil
}

func (s *CassandraStore) ListOrgsWithFailedItems(limit int) ([]GCFailedItemOrgInfo, error) {
	iter := s.db.Session().Query(`SELECT org_id, failed_depth FROM gc_org_stats`).Iter()
	var (
		orgIDStr    string
		failedDepth int
	)
	type failedOrgCandidate struct {
		orgID uuid.UUID
		depth int
	}
	candidates := make([]failedOrgCandidate, 0)
	for iter.Scan(&orgIDStr, &failedDepth) {
		if failedDepth <= 0 {
			continue
		}
		candidates = append(candidates, failedOrgCandidate{orgID: parseUUID(orgIDStr), depth: failedDepth})
	}
	if err := iter.Close(); err != nil {
		return nil, fmt.Errorf("list orgs with failed items: %w", err)
	}
	// Hydrate every candidate before ordering: the top-N is by (failed_depth,
	// most-recent failure), so last-failure must be known for all candidates
	// before the limit is applied — truncating earlier would bias depth ties by
	// org_id and could starve a recently-failing org from the auto-retry page.
	// The per-org reads are bounded by the number of orgs that actually have DLQ
	// items, which is small in practice.
	orgs := make([]GCFailedItemOrgInfo, 0, len(candidates))
	for _, candidate := range candidates {
		orgName, err := s.GetOrgName(candidate.orgID)
		if err != nil {
			orgName = ""
		}
		// UpdatedAt reflects the most recent real failure for this org, not the
		// snapshot refresh time (which a forced refresh pins to ~now for every
		// org and so conveys nothing in the admin list).
		lastFailedAt, err := s.latestFailedAt(candidate.orgID)
		if err != nil {
			log.Printf("[GC] Failed to read latest failed_at for %s: %v", candidate.orgID, err)
		}
		orgs = append(orgs, GCFailedItemOrgInfo{
			OrgID:            candidate.orgID,
			OrgName:          orgName,
			FailedItemsTotal: candidate.depth,
			UpdatedAt:        lastFailedAt,
		})
	}
	sort.Slice(orgs, func(i, j int) bool {
		if orgs[i].FailedItemsTotal != orgs[j].FailedItemsTotal {
			return orgs[i].FailedItemsTotal > orgs[j].FailedItemsTotal
		}
		if !orgs[i].UpdatedAt.Equal(orgs[j].UpdatedAt) {
			return orgs[i].UpdatedAt.After(orgs[j].UpdatedAt)
		}
		return orgs[i].OrgID.String() < orgs[j].OrgID.String()
	})
	if limit > 0 && len(orgs) > limit {
		orgs = orgs[:limit]
	}
	return orgs, nil
}

func (s *CassandraStore) DeleteFailedItem(orgID uuid.UUID, failedAt time.Time, itemType ItemType, itemID string) error {
	row, err := s.failedItemInfo(orgID, failedAt, itemType, itemID)
	if errors.Is(err, gocql.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load failed identity for delete %s/%s: %w", orgID, itemID, err)
	}
	now := time.Now().UTC()
	batch := s.db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		DELETE FROM gc_failed_items
		WHERE org_id = ? AND failed_at = ? AND item_type = ? AND item_id = ?
	`, orgID.String(), failedAt, string(itemType), itemID)
	db.AddDeleteFailedItemExpiryQuery(batch, orgID.String(), failedAt, string(itemType), itemID, row.ExpiresAt)
	addPendingItemDeleteBatchQuery(batch, orgID, row.LibraryID, itemType, itemID, effectiveIdentityAt(row.QueuedAt, row.IdentityAt))
	batch.Query(`
		INSERT INTO gc_dirty_orgs (bucket, org_id, marked_at)
		VALUES (?, ?, ?)
	`, gcOrgBucket(orgID), orgID.String(), now)
	return batch.Exec()
}

func (s *CassandraStore) DeleteExpiredFailedItem(expiry GCFailedItemExpiryInfo, now time.Time) (bool, error) {
	row, err := s.failedItemInfo(expiry.OrgID, expiry.FailedAt, expiry.ItemType, expiry.ItemID)
	if errors.Is(err, gocql.ErrNotFound) {
		if err := s.db.Session().Query(`
			DELETE FROM gc_failed_items_by_expiry
			WHERE expiry_day = ? AND bucket = ? AND expires_at = ? AND org_id = ? AND failed_at = ? AND item_type = ? AND item_id = ?
		`, db.GCProjectionUTCDate(expiry.ExpiresAt), db.GCDiscoveryBucket(expiry.OrgID.String(), string(expiry.ItemType), expiry.ItemID, expiry.FailedAt.UTC().Format(time.RFC3339Nano)), expiry.ExpiresAt.UTC(), expiry.OrgID.String(), expiry.FailedAt.UTC(), string(expiry.ItemType), expiry.ItemID).Exec(); err != nil {
			return false, fmt.Errorf("delete orphaned failed-item expiry projection org=%s item=%s: %w", expiry.OrgID, expiry.ItemID, err)
		}
		if markErr := s.MarkOrgDirty(expiry.OrgID, time.Now().UTC()); markErr != nil {
			log.Printf("[GC] WARNING: failed to mark org dirty for orphaned failed-item expiry org=%s item=%s: %v", expiry.OrgID, expiry.ItemID, markErr)
		}
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("load failed item for expiry org=%s item=%s: %w", expiry.OrgID, expiry.ItemID, err)
	}
	if row.ExpiresAt.IsZero() {
		row.ExpiresAt = expiry.FailedAt.Add(gcFailedItemRetention)
	}
	if !row.ExpiresAt.Equal(expiry.ExpiresAt.UTC()) {
		if err := s.db.Session().Query(`
			DELETE FROM gc_failed_items_by_expiry
			WHERE expiry_day = ? AND bucket = ? AND expires_at = ? AND org_id = ? AND failed_at = ? AND item_type = ? AND item_id = ?
		`, db.GCProjectionUTCDate(expiry.ExpiresAt), db.GCDiscoveryBucket(expiry.OrgID.String(), string(expiry.ItemType), expiry.ItemID, expiry.FailedAt.UTC().Format(time.RFC3339Nano)), expiry.ExpiresAt.UTC(), expiry.OrgID.String(), expiry.FailedAt.UTC(), string(expiry.ItemType), expiry.ItemID).Exec(); err != nil {
			return false, fmt.Errorf("delete stale failed-item expiry projection org=%s item=%s: %w", expiry.OrgID, expiry.ItemID, err)
		}
		if markErr := s.MarkOrgDirty(expiry.OrgID, time.Now().UTC()); markErr != nil {
			log.Printf("[GC] WARNING: failed to mark org dirty for stale failed-item expiry org=%s item=%s: %v", expiry.OrgID, expiry.ItemID, markErr)
		}
		return false, nil
	}
	if row.ExpiresAt.After(now.UTC()) {
		return false, nil
	}

	mutationAt := time.Now().UTC()
	batch := s.db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		DELETE FROM gc_failed_items
		WHERE org_id = ? AND failed_at = ? AND item_type = ? AND item_id = ?
	`, expiry.OrgID.String(), expiry.FailedAt, string(expiry.ItemType), expiry.ItemID)
	db.AddDeleteFailedItemExpiryQuery(batch, expiry.OrgID.String(), expiry.FailedAt, string(expiry.ItemType), expiry.ItemID, row.ExpiresAt)
	addPendingItemDeleteBatchQuery(batch, expiry.OrgID, row.LibraryID, expiry.ItemType, expiry.ItemID, effectiveIdentityAt(row.QueuedAt, row.IdentityAt))
	batch.Query(`
		INSERT INTO gc_dirty_orgs (bucket, org_id, marked_at)
		VALUES (?, ?, ?)
	`, gcOrgBucket(expiry.OrgID), expiry.OrgID.String(), mutationAt)
	if err := batch.Exec(); err != nil {
		return false, err
	}
	return true, nil
}

func (s *CassandraStore) RequeueFailedItem(orgID uuid.UUID, failedAt time.Time, itemType ItemType, itemID string, queuedAt time.Time) error {
	var (
		failedQueuedAt              time.Time
		identityAt                  time.Time
		expiresAt                   time.Time
		requiresLibraryDeletedCheck bool
		libraryIDStr                string
		storageClass                string
	)
	err := s.db.Session().Query(`
		SELECT queued_at, identity_at, expires_at, requires_library_deleted_check, library_id, storage_class
		FROM gc_failed_items WHERE org_id = ? AND failed_at = ? AND item_type = ? AND item_id = ?
	`, orgID.String(), failedAt, string(itemType), itemID).Scan(&failedQueuedAt, &identityAt, &expiresAt, &requiresLibraryDeletedCheck, &libraryIDStr, &storageClass)
	if err != nil {
		return fmt.Errorf("load failed item for requeue %s/%s: %w", orgID, itemID, err)
	}
	requeueAt := failedQueuedAt
	batch := s.db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		INSERT INTO gc_queue (org_id, bucket, queued_at, identity_at, requires_library_deleted_check, item_type, item_id, library_id, storage_class, retry_count)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, orgID.String(), gcQueueBucket(orgID, itemType, itemID), requeueAt, effectiveIdentityAt(failedQueuedAt, identityAt), requiresLibraryDeletedCheck, string(itemType), itemID, libraryIDStr, storageClass, 0)
	addPendingItemBatchQuery(batch, orgID, parseUUID(libraryIDStr), itemType, itemID, effectiveIdentityAt(failedQueuedAt, identityAt))
	batch.Query(`
		DELETE FROM gc_failed_items
		WHERE org_id = ? AND failed_at = ? AND item_type = ? AND item_id = ?
	`, orgID.String(), failedAt, string(itemType), itemID)
	db.AddDeleteFailedItemExpiryQuery(batch, orgID.String(), failedAt, string(itemType), itemID, expiresAt)
	batch.Query(`
		INSERT INTO gc_active_orgs (bucket, org_id, last_enqueued_at)
		VALUES (?, ?, ?)
	`, gcOrgBucket(orgID), orgID.String(), queuedAt)
	batch.Query(`
		INSERT INTO gc_dirty_orgs (bucket, org_id, marked_at)
		VALUES (?, ?, ?)
	`, gcOrgBucket(orgID), orgID.String(), queuedAt)
	return batch.Exec()
}

func (s *CassandraStore) ListOrgsWithQueuedItems() ([]uuid.UUID, error) {
	var orgs []uuid.UUID
	seen := make(map[uuid.UUID]struct{})
	for bucket := 0; bucket < gcDefaultOrgBucketCount; bucket++ {
		iter := s.db.Session().Query(`SELECT org_id FROM gc_active_orgs WHERE bucket = ?`, bucket).Iter()
		var orgIDStr string
		for iter.Scan(&orgIDStr) {
			orgID := parseUUID(orgIDStr)
			if _, ok := seen[orgID]; ok {
				continue
			}
			seen[orgID] = struct{}{}
			orgs = append(orgs, orgID)
		}
		if err := iter.Close(); err != nil {
			return nil, fmt.Errorf("failed to list active orgs from bucket %d: %w", bucket, err)
		}
	}
	return orgs, nil
}

func (s *CassandraStore) ListOrgsWithQueuedSnapshots(limit int) ([]uuid.UUID, error) {
	iter := s.db.Session().Query(`SELECT org_id, queue_depth FROM gc_org_stats`).Iter()
	orgs := make([]uuid.UUID, 0)
	var orgIDStr string
	var queueDepth int
	for iter.Scan(&orgIDStr, &queueDepth) {
		if queueDepth <= 0 {
			continue
		}
		orgs = append(orgs, parseUUID(orgIDStr))
		if limit > 0 && len(orgs) >= limit {
			break
		}
	}
	if err := iter.Close(); err != nil {
		return nil, fmt.Errorf("list orgs with queued snapshots: %w", err)
	}
	return orgs, nil
}

func (s *CassandraStore) MarkOrgActive(orgID uuid.UUID, activeAt time.Time) error {
	return s.db.Session().Query(`
		INSERT INTO gc_active_orgs (bucket, org_id, last_enqueued_at)
		VALUES (?, ?, ?)
	`, gcOrgBucket(orgID), orgID.String(), activeAt).Exec()
}

func (s *CassandraStore) RemoveOrgFromActiveSet(orgID uuid.UUID, activeBefore time.Time) error {
	applied, err := s.db.Session().Query(`
		DELETE FROM gc_active_orgs
		WHERE bucket = ? AND org_id = ?
		IF last_enqueued_at < ?
	`, gcOrgBucket(orgID), orgID.String(), activeBefore).MapScanCAS(map[string]interface{}{})
	if err != nil {
		return fmt.Errorf("remove active org %s: %w", orgID, err)
	}
	if !applied {
		return nil
	}
	return nil
}

func (s *CassandraStore) MarkOrgDirty(orgID uuid.UUID, dirtyAt time.Time) error {
	return s.db.Session().Query(`
		INSERT INTO gc_dirty_orgs (bucket, org_id, marked_at)
		VALUES (?, ?, ?)
	`, gcOrgBucket(orgID), orgID.String(), dirtyAt).Exec()
}

func (s *CassandraStore) ListDirtyOrgs(limit int) ([]GCDirtyOrg, error) {
	orgs := make([]GCDirtyOrg, 0)
	seen := make(map[uuid.UUID]struct{})
	for bucket := 0; bucket < gcDefaultOrgBucketCount; bucket++ {
		iter := s.db.Session().Query(`SELECT org_id, marked_at FROM gc_dirty_orgs WHERE bucket = ?`, bucket).Iter()
		var orgIDStr string
		var markedAt time.Time
		for iter.Scan(&orgIDStr, &markedAt) {
			orgID := parseUUID(orgIDStr)
			if _, ok := seen[orgID]; ok {
				continue
			}
			seen[orgID] = struct{}{}
			orgs = append(orgs, GCDirtyOrg{OrgID: orgID, MarkedAt: markedAt})
			if limit > 0 && len(orgs) >= limit {
				break
			}
		}
		if err := iter.Close(); err != nil {
			return nil, fmt.Errorf("failed to list dirty orgs from bucket %d: %w", bucket, err)
		}
		if limit > 0 && len(orgs) >= limit {
			break
		}
	}
	return orgs, nil
}

func (s *CassandraStore) ClearDirtyOrg(orgID uuid.UUID, dirtyBefore time.Time) error {
	applied, err := s.db.Session().Query(`
		DELETE FROM gc_dirty_orgs
		WHERE bucket = ? AND org_id = ?
		IF marked_at <= ?
	`, gcOrgBucket(orgID), orgID.String(), dirtyBefore).MapScanCAS(map[string]interface{}{})
	if err != nil {
		return fmt.Errorf("clear dirty org %s: %w", orgID, err)
	}
	if !applied {
		return nil
	}
	return nil
}

func (s *CassandraStore) GetOrgQueueStats(orgID uuid.UUID) (GCOrgStats, error) {
	stats := GCOrgStats{OrgID: orgID}
	var oldestQueuedAt *time.Time
	err := s.db.Session().Query(`
		SELECT queue_depth, failed_depth, oldest_queued_at, updated_at, recalculated_at
		FROM gc_org_stats WHERE org_id = ?
	`, orgID.String()).Scan(&stats.QueueDepth, &stats.FailedDepth, &oldestQueuedAt, &stats.UpdatedAt, &stats.RecalculatedAt)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return stats, nil
		}
		return GCOrgStats{}, fmt.Errorf("get org queue stats for %s: %w", orgID, err)
	}
	stats.OldestQueuedAt = oldestQueuedAt
	return stats, nil
}

func (s *CassandraStore) SaveOrgQueueStats(stats GCOrgStats) error {
	return s.db.Session().Query(`
		INSERT INTO gc_org_stats (org_id, queue_depth, failed_depth, oldest_queued_at, updated_at, recalculated_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, stats.OrgID.String(), stats.QueueDepth, stats.FailedDepth, stats.OldestQueuedAt, stats.UpdatedAt, stats.RecalculatedAt).Exec()
}

func (s *CassandraStore) RecalculateOrgQueueStats(orgID uuid.UUID) (GCOrgStats, error) {
	stats, err := s.scanOrgQueueStats(orgID)
	if err != nil {
		return GCOrgStats{}, err
	}
	now := time.Now().UTC()
	stats.UpdatedAt = now
	stats.RecalculatedAt = now
	if err := s.SaveOrgQueueStats(stats); err != nil {
		return GCOrgStats{}, fmt.Errorf("save recalculated org queue stats for %s: %w", orgID, err)
	}
	return stats, nil
}

func (s *CassandraStore) GetOldestQueuedAt(orgID uuid.UUID) (*time.Time, error) {
	var oldest *time.Time
	for bucket := 0; bucket < gcDefaultQueueBucketCount; bucket++ {
		var queuedAt time.Time
		err := s.db.Session().Query(`
			SELECT queued_at FROM gc_queue WHERE org_id = ? AND bucket = ? LIMIT 1
		`, orgID.String(), bucket).Scan(&queuedAt)
		if err != nil {
			if err == gocql.ErrNotFound {
				continue
			}
			return nil, fmt.Errorf("get oldest queued_at for %s bucket %d: %w", orgID, bucket, err)
		}
		queuedAtCopy := queuedAt
		if oldest == nil || queuedAtCopy.Before(*oldest) {
			oldest = &queuedAtCopy
		}
	}
	return oldest, nil
}

func (s *CassandraStore) SumOrgQueueStats() (int, int, error) {
	iter := s.db.Session().Query(`SELECT queue_depth, failed_depth FROM gc_org_stats`).Iter()
	var (
		queueDepth  int
		failedDepth int
		totalQueue  int
		totalFailed int
	)
	for iter.Scan(&queueDepth, &failedDepth) {
		totalQueue += queueDepth
		totalFailed += failedDepth
	}
	if err := iter.Close(); err != nil {
		return 0, 0, fmt.Errorf("sum org queue stats: %w", err)
	}
	if totalQueue < 0 {
		totalQueue = 0
	}
	if totalFailed < 0 {
		totalFailed = 0
	}
	return totalQueue, totalFailed, nil
}
func (s *CassandraStore) GetUserDeletedAt(orgID, userID uuid.UUID) (*time.Time, error) {
	var status string
	var deletedAt *time.Time
	err := s.db.Session().Query(`
		SELECT status, deleted_at FROM users WHERE org_id = ? AND user_id = ?
	`, orgID.String(), userID.String()).Scan(&status, &deletedAt)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	if status != "deleted" || deletedAt == nil {
		return nil, nil
	}
	deletedAtCopy := *deletedAt
	return &deletedAtCopy, nil
}

func (s *CassandraStore) GetLibraryDeletedAt(libraryID uuid.UUID) (*time.Time, error) {
	var deletedAt time.Time
	err := s.db.Session().Query(`
		SELECT deleted_at FROM deleted_libraries WHERE library_id = ?
	`, libraryID.String()).Scan(&deletedAt)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	deletedAtCopy := deletedAt
	return &deletedAtCopy, nil
}

func (s *CassandraStore) GetOrgDeletedAt(orgID uuid.UUID) (*time.Time, error) {
	var status string
	var deletedAt *time.Time
	err := s.db.Session().Query(`
		SELECT status, deleted_at FROM organizations WHERE org_id = ?
	`, orgID.String()).Scan(&status, &deletedAt)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	if (status != "deleted" && status != "purging") || deletedAt == nil {
		return nil, nil
	}
	deletedAtCopy := *deletedAt
	return &deletedAtCopy, nil
}

func (s *CassandraStore) upsertBlockGCCandidateProjection(orgID uuid.UUID, blockID, storageClass string, candidateAt time.Time) error {
	return s.db.Session().Query(`
		INSERT INTO gc_block_candidates_by_day (candidate_day, bucket, candidate_at, org_id, block_id, storage_class)
		VALUES (?, ?, ?, ?, ?, ?)
	`, db.GCProjectionUTCDate(candidateAt), db.GCDiscoveryBucket(orgID.String(), blockID), candidateAt.UTC(), orgID.String(), blockID, storageClass).Exec()
}

func (s *CassandraStore) moveBlockGCCandidateProjection(orgID uuid.UUID, blockID, storageClass string, fromCandidateAt, toCandidateAt time.Time) error {
	if fromCandidateAt.Equal(toCandidateAt) {
		return s.upsertBlockGCCandidateProjection(orgID, blockID, storageClass, toCandidateAt)
	}
	batch := s.db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		DELETE FROM gc_block_candidates_by_day
		WHERE candidate_day = ? AND bucket = ? AND candidate_at = ? AND org_id = ? AND block_id = ?
	`, db.GCProjectionUTCDate(fromCandidateAt), db.GCDiscoveryBucket(orgID.String(), blockID), fromCandidateAt.UTC(), orgID.String(), blockID)
	batch.Query(`
		INSERT INTO gc_block_candidates_by_day (candidate_day, bucket, candidate_at, org_id, block_id, storage_class)
		VALUES (?, ?, ?, ?, ?, ?)
	`, db.GCProjectionUTCDate(toCandidateAt), db.GCDiscoveryBucket(orgID.String(), blockID), toCandidateAt.UTC(), orgID.String(), blockID, storageClass)
	return batch.Exec()
}

func casStringValue(row map[string]interface{}, key string) (string, error) {
	value, ok := row[key]
	if !ok || value == nil {
		return "", nil
	}
	switch typed := value.(type) {
	case string:
		return typed, nil
	case []byte:
		return string(typed), nil
	default:
		return "", fmt.Errorf("unexpected CAS %s type %T", key, value)
	}
}

func casTimeValue(row map[string]interface{}, key string) (time.Time, error) {
	value, ok := row[key]
	if !ok || value == nil {
		return time.Time{}, nil
	}
	switch typed := value.(type) {
	case time.Time:
		return typed, nil
	case *time.Time:
		if typed == nil {
			return time.Time{}, nil
		}
		return *typed, nil
	default:
		return time.Time{}, fmt.Errorf("unexpected CAS %s type %T", key, value)
	}
}

// EnsureBlockGCCandidate inserts a (org_id, block_id) row into the canonical
// gc_block_candidates table if one does not already exist, and guarantees the
// matching gc_block_candidates_by_day discovery row exists for the effective
// candidate_at timestamp. If a row already exists with a later candidate_at,
// the earlier requested timestamp wins so explicit zero-ref enqueue paths are
// not delayed behind a provisional upload's future TTL-based candidate.
func (s *CassandraStore) EnsureBlockGCCandidate(orgID uuid.UUID, blockID, storageClass string, candidateAt time.Time) (time.Time, error) {
	effectiveCandidateAt := candidateAt.UTC()
	if effectiveCandidateAt.IsZero() {
		effectiveCandidateAt = time.Now().UTC()
	}
	for {
		existing := map[string]interface{}{}
		applied, err := s.db.Session().Query(`
			INSERT INTO gc_block_candidates (org_id, block_id, storage_class, candidate_at)
			VALUES (?, ?, ?, ?) IF NOT EXISTS
		`, orgID.String(), blockID, storageClass, effectiveCandidateAt).MapScanCAS(existing)
		if err != nil {
			return time.Time{}, err
		}
		if applied {
			if err := s.upsertBlockGCCandidateProjection(orgID, blockID, storageClass, effectiveCandidateAt); err != nil {
				return effectiveCandidateAt, fmt.Errorf("ensure gc_block_candidates_by_day discovery row for org=%s block=%s: %w", orgID, blockID, err)
			}
			return effectiveCandidateAt, nil
		}

		existingCandidateAt, err := casTimeValue(existing, "candidate_at")
		if err != nil {
			return time.Time{}, err
		}
		if existingCandidateAt.IsZero() {
			return time.Time{}, fmt.Errorf("gc_block_candidates row for org=%s block=%s is missing candidate_at", orgID, blockID)
		}
		existingStorageClass, err := casStringValue(existing, "storage_class")
		if err != nil {
			return time.Time{}, err
		}
		if existingStorageClass == "" {
			existingStorageClass = storageClass
		}
		if !effectiveCandidateAt.Before(existingCandidateAt) {
			if err := s.upsertBlockGCCandidateProjection(orgID, blockID, existingStorageClass, existingCandidateAt); err != nil {
				return existingCandidateAt, fmt.Errorf("ensure gc_block_candidates_by_day discovery row for org=%s block=%s: %w", orgID, blockID, err)
			}
			return existingCandidateAt, nil
		}

		updateState := map[string]interface{}{}
		updated, err := s.db.Session().Query(`
			UPDATE gc_block_candidates SET candidate_at = ?
			WHERE org_id = ? AND block_id = ?
			IF candidate_at = ?
		`, effectiveCandidateAt, orgID.String(), blockID, existingCandidateAt).MapScanCAS(updateState)
		if err != nil {
			return time.Time{}, err
		}
		if !updated {
			continue
		}
		if err := s.moveBlockGCCandidateProjection(orgID, blockID, existingStorageClass, existingCandidateAt, effectiveCandidateAt); err != nil {
			return effectiveCandidateAt, fmt.Errorf("move gc_block_candidates_by_day discovery row for org=%s block=%s: %w", orgID, blockID, err)
		}
		return effectiveCandidateAt, nil
	}
}

// DeleteBlockGCCandidate removes both the canonical row and the matching
// discovery row. Callers should pass candidateAt when they already know it
// (for example from QueueItem.QueuedAt or BlockGCCandidateInfo.CandidateAt) so
// the discovery row can still be removed even if the canonical row is already
// gone. A zero candidateAt falls back to reading the canonical row first. If
// that row is already gone too, the discovery-row primary key is unknown and
// the cleanup degrades to a best-effort canonical delete plus a warning.
func (s *CassandraStore) DeleteBlockGCCandidate(orgID uuid.UUID, blockID string, candidateAt time.Time) error {
	if candidateAt.IsZero() {
		err := s.db.Session().Query(`
			SELECT candidate_at FROM gc_block_candidates WHERE org_id = ? AND block_id = ?
		`, orgID.String(), blockID).Scan(&candidateAt)
		if err != nil && !errors.Is(err, gocql.ErrNotFound) {
			return fmt.Errorf("failed to read gc_block_candidates row for delete: %w", err)
		}
	}

	if err := s.db.Session().Query(`
		DELETE FROM gc_block_candidates WHERE org_id = ? AND block_id = ?
	`, orgID.String(), blockID).Exec(); err != nil {
		return err
	}

	if candidateAt.IsZero() {
		log.Printf("[GC] WARNING: DeleteBlockGCCandidate called without candidate_at for org=%s block=%s and canonical row is already gone; gc_block_candidates_by_day cleanup skipped because the discovery key is unknown", orgID, blockID)
		return nil
	}
	if err := s.db.Session().Query(`
		DELETE FROM gc_block_candidates_by_day
		WHERE candidate_day = ? AND bucket = ? AND candidate_at = ? AND org_id = ? AND block_id = ?
	`, db.GCProjectionUTCDate(candidateAt), db.GCDiscoveryBucket(orgID.String(), blockID), candidateAt.UTC(), orgID.String(), blockID).Exec(); err != nil {
		log.Printf("[GC] WARNING: failed to delete gc_block_candidates_by_day discovery row for org=%s block=%s: %v", orgID, blockID, err)
	}
	return nil
}

// ListBlockGCCandidatesByDay enumerates candidates for one (UTC day, discovery
// bucket) partition. The scanner walks buckets [0, GCDiscoveryBucketCount)
// for each day from its persisted cursor up to today.
func (s *CassandraStore) ListBlockGCCandidatesByDay(day time.Time, bucket int) ([]BlockGCCandidateInfo, error) {
	iter := s.db.Session().Query(`
		SELECT candidate_at, org_id, block_id, storage_class
		FROM gc_block_candidates_by_day
		WHERE candidate_day = ? AND bucket = ?
	`, db.GCProjectionUTCDate(day), bucket).Iter()
	var candidates []BlockGCCandidateInfo
	var candidateAt time.Time
	var orgIDStr, blockID, storageClass string
	for iter.Scan(&candidateAt, &orgIDStr, &blockID, &storageClass) {
		candidates = append(candidates, BlockGCCandidateInfo{
			OrgID:        parseUUID(orgIDStr),
			BlockID:      blockID,
			StorageClass: storageClass,
			CandidateAt:  candidateAt,
		})
	}
	if err := iter.Close(); err != nil {
		return nil, fmt.Errorf("failed to list block GC candidates for day=%s bucket=%d: %w", db.GCProjectionDateString(day), bucket, err)
	}
	return candidates, nil
}

func (s *CassandraStore) ListProvisionalBlockRefExpiriesByDay(day time.Time, bucket int) ([]ProvisionalBlockRefExpiryInfo, error) {
	iter := s.db.Session().Query(`
		SELECT expires_at, org_id, block_id, referrer, storage_class
		FROM gc_provisional_block_refs_by_day
		WHERE expiry_day = ? AND bucket = ?
	`, db.GCProjectionUTCDate(day), bucket).Iter()
	var out []ProvisionalBlockRefExpiryInfo
	var expiresAt time.Time
	var orgIDStr, blockID, referrer, storageClass string
	for iter.Scan(&expiresAt, &orgIDStr, &blockID, &referrer, &storageClass) {
		out = append(out, ProvisionalBlockRefExpiryInfo{
			OrgID:        parseUUID(orgIDStr),
			BlockID:      blockID,
			Referrer:     referrer,
			StorageClass: storageClass,
			ExpiresAt:    expiresAt,
		})
	}
	if err := iter.Close(); err != nil {
		return nil, fmt.Errorf("failed to list provisional block ref expiries for day=%s bucket=%d: %w", db.GCProjectionDateString(day), bucket, err)
	}
	return out, nil
}

func (s *CassandraStore) GetProvisionalBlockRefExpiry(orgID uuid.UUID, blockID, referrer string) (ProvisionalBlockRefExpiryInfo, bool, error) {
	var storageClass string
	var expiresAt time.Time
	if err := s.db.Session().Query(`
		SELECT storage_class, expires_at
		FROM gc_provisional_block_refs
		WHERE org_id = ? AND block_id = ? AND referrer = ?
	`, orgID.String(), blockID, referrer).Scan(&storageClass, &expiresAt); err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return ProvisionalBlockRefExpiryInfo{}, false, nil
		}
		return ProvisionalBlockRefExpiryInfo{}, false, fmt.Errorf("failed to load provisional block ref expiry org=%s block=%s referrer=%s: %w", orgID, blockID, referrer, err)
	}
	return ProvisionalBlockRefExpiryInfo{
		OrgID:        orgID,
		BlockID:      blockID,
		Referrer:     referrer,
		StorageClass: storageClass,
		ExpiresAt:    expiresAt.UTC(),
	}, true, nil
}

func (s *CassandraStore) DeleteProvisionalBlockRefExpiryProjection(orgID uuid.UUID, blockID, referrer string, expiresAt time.Time) error {
	expiresAt = expiresAt.UTC()
	return s.db.Session().Query(`
		DELETE FROM gc_provisional_block_refs_by_day
		WHERE expiry_day = ? AND bucket = ? AND expires_at = ? AND org_id = ? AND block_id = ? AND referrer = ?
	`, db.GCProjectionUTCDate(expiresAt), db.GCDiscoveryBucket(orgID.String(), blockID, referrer), expiresAt, orgID.String(), blockID, referrer).Exec()
}

func (s *CassandraStore) DeleteProvisionalBlockRefExpiry(orgID uuid.UUID, blockID, referrer string, expiresAt time.Time) error {
	return s.db.DeleteProvisionalBlockReferenceExpiry(orgID.String(), blockID, referrer, expiresAt)
}

// --- S3 orphan recovery ---

func (s *CassandraStore) upsertS3OrphanProjection(orgID uuid.UUID, blockID, storageClass string, firstSeenAt time.Time) error {
	return s.db.Session().Query(`
		INSERT INTO gc_s3_orphans_by_day (first_seen_day, bucket, first_seen_at, org_id, block_id, storage_class)
		VALUES (?, ?, ?, ?, ?, ?)
	`, db.GCProjectionUTCDate(firstSeenAt), db.GCDiscoveryBucket(orgID.String(), blockID), firstSeenAt.UTC(), orgID.String(), blockID, storageClass).Exec()
}

// RecordS3Orphan upserts a gc_s3_orphans row preserving and returning the
// effective first_seen_at when the row already exists. Called both for the
// initial "S3 pending" record and for actual S3 delete failures.
//
// It also guarantees the matching gc_s3_orphans_by_day discovery row exists so
// recovery can enumerate every orphan without scanning canonical partitions.
func (s *CassandraStore) RecordS3Orphan(orgID uuid.UUID, blockID, storageClass, errMsg string, now time.Time) (time.Time, error) {
	initialRetryCount := 0
	if errMsg != "" {
		initialRetryCount = 1
	}
	existing := map[string]interface{}{}
	// INSERT IF NOT EXISTS preserves the original first_seen_at on conflict;
	// if the row exists we fall through to UpdateS3OrphanAttempt-style update.
	applied, err := s.db.Session().Query(`
		INSERT INTO gc_s3_orphans (org_id, block_id, storage_class, first_seen_at, last_attempt_at, retry_count, last_error)
		VALUES (?, ?, ?, ?, ?, ?, ?) IF NOT EXISTS
	`, orgID.String(), blockID, storageClass, now, now, initialRetryCount, errMsg).MapScanCAS(existing)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to record S3 orphan: %w", err)
	}
	effectiveFirstSeenAt := now.UTC()
	effectiveStorageClass := storageClass
	if !applied {
		effectiveFirstSeenAt, err = casTimeValue(existing, "first_seen_at")
		if err != nil {
			return time.Time{}, err
		}
		if effectiveFirstSeenAt.IsZero() {
			return time.Time{}, fmt.Errorf("gc_s3_orphans row for org=%s block=%s is missing first_seen_at", orgID, blockID)
		}
		effectiveStorageClass, err = casStringValue(existing, "storage_class")
		if err != nil {
			return time.Time{}, err
		}
		if effectiveStorageClass == "" {
			effectiveStorageClass = storageClass
		}
	}
	if err := s.upsertS3OrphanProjection(orgID, blockID, effectiveStorageClass, effectiveFirstSeenAt); err != nil {
		return effectiveFirstSeenAt, fmt.Errorf("ensure gc_s3_orphans_by_day discovery row for org=%s block=%s: %w", orgID, blockID, err)
	}
	if !applied && errMsg != "" {
		if err := s.UpdateS3OrphanAttempt(orgID, blockID, errMsg, now); err != nil {
			return effectiveFirstSeenAt, err
		}
	}
	return effectiveFirstSeenAt, nil
}

func (s *CassandraStore) UpdateS3OrphanAttempt(orgID uuid.UUID, blockID, errMsg string, now time.Time) error {
	// Cassandra UPDATE with counter-like increment requires a read-modify-write;
	// do it with a simple UPDATE + read of the prior retry_count. Conflict is
	// acceptable: worst case retry_count is off-by-one, which has no correctness
	// impact — the field is a diagnostic.
	var prev int
	err := s.db.Session().Query(`
		SELECT retry_count FROM gc_s3_orphans WHERE org_id = ? AND block_id = ?
	`, orgID.String(), blockID).Scan(&prev)
	if err != nil && !errors.Is(err, gocql.ErrNotFound) {
		return fmt.Errorf("failed to read prior retry count: %w", err)
	}
	return s.db.Session().Query(`
		UPDATE gc_s3_orphans
		SET last_attempt_at = ?, retry_count = ?, last_error = ?
		WHERE org_id = ? AND block_id = ?
	`, now, prev+1, errMsg, orgID.String(), blockID).Exec()
}

// DeleteS3Orphan removes both the canonical row and the matching discovery
// projection row. Callers should pass firstSeenAt when they already know it so
// the discovery row can still be removed if the canonical row has already been
// deleted. A zero firstSeenAt falls back to reading the canonical row first.
func (s *CassandraStore) DeleteS3Orphan(orgID uuid.UUID, blockID string, firstSeenAt time.Time) error {
	if firstSeenAt.IsZero() {
		err := s.db.Session().Query(`
			SELECT first_seen_at FROM gc_s3_orphans WHERE org_id = ? AND block_id = ?
		`, orgID.String(), blockID).Scan(&firstSeenAt)
		if err != nil && !errors.Is(err, gocql.ErrNotFound) {
			return fmt.Errorf("failed to read gc_s3_orphans row for delete: %w", err)
		}
	}

	if err := s.db.Session().Query(`
		DELETE FROM gc_s3_orphans WHERE org_id = ? AND block_id = ?
	`, orgID.String(), blockID).Exec(); err != nil {
		return err
	}

	if firstSeenAt.IsZero() {
		return nil
	}
	if err := s.db.Session().Query(`
		DELETE FROM gc_s3_orphans_by_day
		WHERE first_seen_day = ? AND bucket = ? AND first_seen_at = ? AND org_id = ? AND block_id = ?
	`, db.GCProjectionUTCDate(firstSeenAt), db.GCDiscoveryBucket(orgID.String(), blockID), firstSeenAt.UTC(), orgID.String(), blockID).Exec(); err != nil {
		log.Printf("[GC] WARNING: failed to delete gc_s3_orphans_by_day discovery row for org=%s block=%s: %v", orgID, blockID, err)
	}
	return nil
}

// ListS3OrphansByDay enumerates orphans for one (UTC day, discovery bucket)
// partition. `limit` caps the rows returned for one (day, bucket); the worker
// walks buckets [0, GCDiscoveryBucketCount) for each day from the persisted
// recovery cursor.
func (s *CassandraStore) ListS3OrphansByDay(day time.Time, bucket int, limit int) ([]S3OrphanInfo, error) {
	if limit <= 0 {
		limit = 100
	}
	iter := s.db.Session().Query(`
		SELECT first_seen_at, org_id, block_id, storage_class
		FROM gc_s3_orphans_by_day
		WHERE first_seen_day = ? AND bucket = ?
		LIMIT ?
	`, db.GCProjectionUTCDate(day), bucket, limit).Iter()
	var out []S3OrphanInfo
	var firstSeen time.Time
	var orgIDStr, blockID, storageClass string
	for iter.Scan(&firstSeen, &orgIDStr, &blockID, &storageClass) {
		out = append(out, S3OrphanInfo{
			OrgID:        parseUUID(orgIDStr),
			BlockID:      blockID,
			StorageClass: storageClass,
			FirstSeenAt:  firstSeen,
		})
	}
	if err := iter.Close(); err != nil {
		return nil, fmt.Errorf("failed to list S3 orphans for day=%s bucket=%d: %w", db.GCProjectionDateString(day), bucket, err)
	}
	return out, nil
}

// --- Block operations ---

// BlockExists reports whether the canonical blocks row still exists.
func (s *CassandraStore) BlockExists(orgID uuid.UUID, blockID string) (bool, error) {
	var existing string
	err := s.db.Session().Query(`
		SELECT block_id FROM blocks WHERE org_id = ? AND block_id = ?
	`, orgID.String(), blockID).Scan(&existing)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// BlockHasReferences reports whether any block_references row still exists.
func (s *CassandraStore) BlockHasReferences(orgID uuid.UUID, blockID string) (bool, error) {
	return s.db.BlockHasReferences(orgID.String(), blockID)
}

func (s *CassandraStore) GetBlockInfo(orgID uuid.UUID, blockID string) (BlockInfo, error) {
	info := BlockInfo{BlockID: blockID}
	var createdAt *time.Time
	err := s.db.Session().Query(`
		SELECT storage_class, created_at FROM blocks WHERE org_id = ? AND block_id = ?
	`, orgID.String(), blockID).Scan(&info.StorageClass, &createdAt)
	if err != nil {
		return BlockInfo{}, err
	}
	info.CreatedAt = createdAt
	return info, nil
}

// RemoveBlockReference deletes one (block, referrer) reference row (idempotent).
func (s *CassandraStore) RemoveBlockReference(orgID uuid.UUID, blockID, referrer string) error {
	return s.db.RemoveBlockReference(orgID.String(), blockID, referrer)
}

// mappingResolveConcurrency bounds the number of in-flight single-row lookups
// against block_id_mappings so a large fs_object's block list cannot flood the
// driver. block_id_mappings is partitioned by ((org_id, external_id)), so each
// lookup is a single-partition point read.
const mappingResolveConcurrency = 32

func (s *CassandraStore) ResolveBlockIDs(orgID uuid.UUID, blockIDs []string) ([]string, error) {
	return resolveBlockIDsConcurrent(orgID, blockIDs, mappingResolveConcurrency, func(idx int) (string, error) {
		var internalID string
		err := s.db.Session().Query(`
			SELECT internal_id FROM block_id_mappings
			WHERE org_id = ? AND external_id = ?
		`, orgID.String(), blockIDs[idx]).Scan(&internalID)
		return internalID, err
	})
}

// resolveBlockIDsConcurrent maps every 40-char SHA-1 entry of blockIDs to its
// internal SHA-256 by calling lookup with bounded concurrency, preserving slice
// order. 64-char SHA-256 IDs are left untouched and lookup is never called for
// them. lookup must return gocql.ErrNotFound when no mapping row exists; that
// (and an empty internal_id) leaves the original ID in place. Any other lookup
// error is fatal: the function still drains every in-flight lookup, then returns
// (nil, joinedErr) so callers never act on a partially-resolved slice.
//
// orgID is used only for error context. The DB-backed lookup is injected so the
// concurrency/ordering/error semantics stay unit-testable without a live
// Cassandra (block_id_mappings has no in-process fake).
func resolveBlockIDsConcurrent(orgID uuid.UUID, blockIDs []string, maxConcurrency int, lookup func(idx int) (string, error)) ([]string, error) {
	resolved := make([]string, len(blockIDs))
	copy(resolved, blockIDs)

	var toResolve []int
	for i, blockID := range blockIDs {
		if len(blockID) == 40 {
			toResolve = append(toResolve, i)
		}
	}
	if len(toResolve) == 0 {
		return resolved, nil
	}

	type lookupResult struct {
		idx        int
		internalID string
		err        error
	}

	concurrency := maxConcurrency
	if concurrency > len(toResolve) {
		concurrency = len(toResolve)
	}
	if concurrency < 1 {
		concurrency = 1
	}
	sem := make(chan struct{}, concurrency)
	results := make(chan lookupResult, len(toResolve))

	for _, idx := range toResolve {
		sem <- struct{}{}
		go func(idx int) {
			defer func() { <-sem }()
			internalID, err := lookup(idx)
			results <- lookupResult{idx: idx, internalID: internalID, err: err}
		}(idx)
	}

	var resolveErr error
	for range toResolve {
		result := <-results
		if result.err != nil {
			if !errors.Is(result.err, gocql.ErrNotFound) {
				resolveErr = errors.Join(resolveErr, fmt.Errorf("resolve block mapping org=%s external=%s: %w", orgID, blockIDs[result.idx], result.err))
			}
			continue
		}
		if result.internalID != "" {
			resolved[result.idx] = result.internalID
		}
	}
	if resolveErr != nil {
		return nil, resolveErr
	}

	return resolved, nil
}

// ClaimBlockDelete marks the block row gc_state='deleting' via LWT so writers
// back off, deferring the physical DELETE until S3-recovery state is persisted.
// This is the single expensive Paxos operation in the block lifecycle. claimID
// is stable for one logical candidate so retries of the same item remain the
// owner, but a different attempt cannot steal or release the claim.
func (s *CassandraStore) readBlockDeleteClaimState(orgID uuid.UUID, blockID string) (string, string, error) {
	var gcState string
	var gcClaimID string
	err := s.db.Session().Query(`
		SELECT gc_state, gc_claim_id FROM blocks WHERE org_id = ? AND block_id = ?
	`, orgID.String(), blockID).Scan(&gcState, &gcClaimID)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return "", "", nil
		}
		return "", "", err
	}
	return gcState, gcClaimID, nil
}

func (s *CassandraStore) ClaimBlockDelete(orgID uuid.UUID, blockID, claimID string) (bool, error) {
	existing := map[string]interface{}{}
	applied, err := s.db.Session().Query(`
		UPDATE blocks SET gc_state = ?, gc_claim_id = ?, gc_claimed_at = ?
		WHERE org_id = ? AND block_id = ?
		IF gc_state != ?
	`, db.BlockGCStateDeleting, claimID, time.Now().UTC(), orgID.String(), blockID, db.BlockGCStateDeleting).MapScanCAS(existing)
	if err != nil {
		return false, err
	}
	if applied {
		return true, nil
	}
	gcState, gcClaimID, err := s.readBlockDeleteClaimState(orgID, blockID)
	if err != nil {
		return false, err
	}
	if gcState == "" && gcClaimID == "" {
		return false, nil
	}
	return gcState == db.BlockGCStateDeleting && gcClaimID == claimID, nil
}

// ReleaseBlockClaim clears the gc_state claim when a concurrent reference appeared
// between the claim and the verify step, so writers stop backing off.
func (s *CassandraStore) ReleaseBlockClaim(orgID uuid.UUID, blockID, claimID string) error {
	applied, err := s.db.Session().Query(`
		UPDATE blocks SET gc_state = null, gc_claim_id = null, gc_claimed_at = null
		WHERE org_id = ? AND block_id = ?
		IF gc_state = ? AND gc_claim_id = ?
	`, orgID.String(), blockID, db.BlockGCStateDeleting, claimID).MapScanCAS(map[string]interface{}{})
	if err != nil {
		return err
	}
	if !applied {
		return fmt.Errorf("block delete claim release not applied for %s", blockID)
	}
	return nil
}

// FinalizeBlockDelete removes a block row that was previously claimed by GC.
func (s *CassandraStore) FinalizeBlockDelete(orgID uuid.UUID, blockID, claimID string) error {
	applied, err := s.db.Session().Query(`
		DELETE FROM blocks WHERE org_id = ? AND block_id = ?
		IF gc_state = ? AND gc_claim_id = ?
	`, orgID.String(), blockID, db.BlockGCStateDeleting, claimID).MapScanCAS(map[string]interface{}{})
	if err != nil {
		log.Printf("[GC Store] Warning: claim succeeded but conditional DELETE failed for block %s: %v", blockID, err)
		return err
	}
	if !applied {
		return fmt.Errorf("block delete finalize not applied for %s", blockID)
	}
	return nil
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
		return nil, fmt.Errorf("list block mappings for org=%s internal_id=%s: %w", orgID, internalID, err)
	}
	return mappings, nil
}

func (s *CassandraStore) DeleteBlockMapping(orgID uuid.UUID, externalID string) error {
	// Read the internal_id first for reverse table cleanup
	var internalID string
	err := s.db.Session().Query(`
		SELECT internal_id FROM block_id_mappings WHERE org_id = ? AND external_id = ?
	`, orgID.String(), externalID).Scan(&internalID)
	if err != nil && !errors.Is(err, gocql.ErrNotFound) {
		return fmt.Errorf("read block mapping org=%s external_id=%s: %w", orgID, externalID, err)
	}
	if err == nil && internalID != "" {
		return s.DeleteBlockMappingResolved(orgID, externalID, internalID)
	}

	batch := s.db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`DELETE FROM block_id_mappings WHERE org_id = ? AND external_id = ?`, orgID.String(), externalID)
	return batch.Exec()
}

func (s *CassandraStore) DeleteBlockMappingResolved(orgID uuid.UUID, externalID, internalID string) error {
	batch := s.db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`DELETE FROM block_id_mappings WHERE org_id = ? AND external_id = ?`, orgID.String(), externalID)
	batch.Query(`DELETE FROM block_id_mappings_by_internal WHERE org_id = ? AND internal_id = ? AND external_id = ?`,
		orgID.String(), internalID, externalID)
	if err := batch.Exec(); err != nil {
		return fmt.Errorf("delete resolved block mapping org=%s external_id=%s internal_id=%s: %w", orgID, externalID, internalID, err)
	}
	return nil
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

func (s *CassandraStore) ListExpiredShareLinks() ([]ExpiredShareLinkInfo, error) {
	now := time.Now()
	cutoffDay := db.GCProjectionUTCDate(now)
	startDay, err := s.loadExpiredShareLinksStartDay(cutoffDay)
	if err != nil {
		return nil, err
	}
	if startDay.After(cutoffDay) {
		return nil, nil
	}

	var links []ExpiredShareLinkInfo
	for day := startDay; !day.After(cutoffDay); day = day.AddDate(0, 0, 1) {
		for bucket := 0; bucket < db.GCDiscoveryBucketCount; bucket++ {
			iter := s.db.Session().Query(`
				SELECT expires_at, link_token, org_id, library_id, created_by, created_at, link_type
				FROM gc_share_links_by_expiry
				WHERE expiry_day = ? AND bucket = ?
			`, day, bucket).Iter()

			var expiresAt time.Time
			var shareToken, orgIDStr, libraryIDStr, createdByStr, linkType string
			var createdAt time.Time
			for iter.Scan(&expiresAt, &shareToken, &orgIDStr, &libraryIDStr, &createdByStr, &createdAt, &linkType) {
				if expiresAt.IsZero() || expiresAt.After(now) {
					continue
				}
				links = append(links, ExpiredShareLinkInfo{
					ShareToken: shareToken,
					OrgID:      parseUUID(orgIDStr),
					LibraryID:  parseUUID(libraryIDStr),
					CreatedBy:  parseUUID(createdByStr),
					CreatedAt:  createdAt,
					LinkType:   linkType,
					ExpiresAt:  expiresAt,
				})
			}
			if err := iter.Close(); err != nil {
				return nil, err
			}
		}
	}

	return links, nil
}

func (s *CassandraStore) loadExpiredShareLinksStartDay(cutoffDay time.Time) (time.Time, error) {
	value, err := s.LoadGCStats(gcExpiredShareLinksCursorKey)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return expiredShareLinksScanStartDay(time.Time{}, cutoffDay), nil
		}
		return time.Time{}, err
	}
	lastDay, err := db.ParseGCProjectionDate(value)
	if err != nil {
		return time.Time{}, err
	}
	return expiredShareLinksScanStartDay(lastDay, cutoffDay), nil
}

func expiredShareLinksScanStartDay(lastProcessedDay, cutoffDay time.Time) time.Time {
	if lastProcessedDay.IsZero() {
		return cutoffDay.AddDate(0, 0, -gcInitialScanLookbackDays)
	}
	return lastProcessedDay.AddDate(0, 0, -gcScanOverlapDays)
}

func (s *CassandraStore) ListDistinctCommitLibraries() ([]uuid.UUID, error) {
	return s.listGCArtifactLibraries()
}

func (s *CassandraStore) ListDistinctFSObjectLibraries() ([]uuid.UUID, error) {
	return s.listGCArtifactLibraries()
}

func (s *CassandraStore) listGCArtifactLibraries() ([]uuid.UUID, error) {
	seen := make(map[uuid.UUID]struct{})
	ids := make([]uuid.UUID, 0)

	liveIter := s.db.Session().Query(`SELECT library_id FROM libraries_by_id`).Iter()
	var liveLibID string
	for liveIter.Scan(&liveLibID) {
		libraryID := parseUUID(liveLibID)
		if _, ok := seen[libraryID]; ok {
			continue
		}
		seen[libraryID] = struct{}{}
		ids = append(ids, libraryID)
	}
	if err := liveIter.Close(); err != nil {
		return nil, err
	}

	deletedIter := s.db.Session().Query(`SELECT library_id FROM deleted_libraries`).Iter()
	var deletedLibID string
	for deletedIter.Scan(&deletedLibID) {
		libraryID := parseUUID(deletedLibID)
		if _, ok := seen[libraryID]; ok {
			continue
		}
		seen[libraryID] = struct{}{}
		ids = append(ids, libraryID)
	}
	if err := deletedIter.Close(); err != nil {
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

func (s *CassandraStore) ReconcilePendingStorageCounters() (int, error) {
	type reconcileRequest struct {
		Scope   string
		OrgID   uuid.UUID
		OwnerID uuid.UUID
	}

	iter := s.db.Session().Query(`
		SELECT scope, org_id, owner_id FROM gc_storage_counter_reconciliation
	`).Iter()

	requests := make(map[string]reconcileRequest)
	var scope, orgIDStr, ownerIDStr string
	for iter.Scan(&scope, &orgIDStr, &ownerIDStr) {
		requests[scope] = reconcileRequest{
			Scope:   scope,
			OrgID:   parseUUID(orgIDStr),
			OwnerID: parseUUID(ownerIDStr),
		}
	}
	if err := iter.Close(); err != nil {
		return 0, fmt.Errorf("failed to list pending storage counter reconciliation scopes: %w", err)
	}
	if len(requests) == 0 {
		return 0, nil
	}

	expected := make(map[string]traffic.StorageSnapshot, len(requests))
	libIter := s.db.Session().Query(`
		SELECT org_id, owner_id, size_bytes, file_count, deleted_at FROM libraries
	`).Iter()

	var libOrgIDStr, libraryOwnerIDStr string
	var sizeBytes, fileCount int64
	var deletedAt time.Time
	for libIter.Scan(&libOrgIDStr, &libraryOwnerIDStr, &sizeBytes, &fileCount, &deletedAt) {
		if !deletedAt.IsZero() {
			continue
		}

		libSnapshot := traffic.StorageSnapshot{BytesUsed: sizeBytes, FileCount: fileCount}
		if libSnapshot.BytesUsed == 0 && libSnapshot.FileCount == 0 {
			continue
		}

		if _, ok := requests[traffic.PlatformStorageScope()]; ok {
			snap := expected[traffic.PlatformStorageScope()]
			snap.BytesUsed += libSnapshot.BytesUsed
			snap.FileCount += libSnapshot.FileCount
			expected[traffic.PlatformStorageScope()] = snap
		}

		orgScope := traffic.OrganizationStorageScope(libOrgIDStr)
		if _, ok := requests[orgScope]; ok {
			snap := expected[orgScope]
			snap.BytesUsed += libSnapshot.BytesUsed
			snap.FileCount += libSnapshot.FileCount
			expected[orgScope] = snap
		}

		userScope := traffic.UserStorageScope(libOrgIDStr, libraryOwnerIDStr)
		if _, ok := requests[userScope]; ok {
			snap := expected[userScope]
			snap.BytesUsed += libSnapshot.BytesUsed
			snap.FileCount += libSnapshot.FileCount
			expected[userScope] = snap
		}
	}
	if err := libIter.Close(); err != nil {
		return 0, fmt.Errorf("failed to scan libraries for storage reconciliation: %w", err)
	}

	reconciled := 0
	var firstErr error
	for _, request := range requests {
		if err := traffic.ReconcileStorageScope(s.db, request.Scope, expected[request.Scope]); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("failed to reconcile storage scope %s: %w", request.Scope, err)
			}
			continue
		}
		if err := s.db.Session().Query(`DELETE FROM gc_storage_counter_reconciliation WHERE scope = ?`, request.Scope).Exec(); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("failed to delete reconciled storage scope %s: %w", request.Scope, err)
			}
			continue
		}
		reconciled++
	}

	return reconciled, firstErr
}

// --- Version TTL ---

func (s *CassandraStore) ListLibrariesWithVersionTTL() ([]LibraryTTLInfo, error) {
	return s.listLibrariesByPolicy(db.GCLibraryPolicyVersionTTL)
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
	rows, err := s.listLibrariesByPolicy(db.GCLibraryPolicyAutoDelete)
	if err != nil {
		return nil, err
	}
	results := make([]LibraryAutoDeleteInfo, 0, len(rows))
	for _, row := range rows {
		results = append(results, LibraryAutoDeleteInfo{
			OrgID:          row.OrgID,
			LibraryID:      row.LibraryID,
			HeadCommitID:   row.HeadCommitID,
			AutoDeleteDays: row.VersionTTLDays,
		})
	}
	return results, nil
}

func (s *CassandraStore) listLibrariesByPolicy(policyType string) ([]LibraryTTLInfo, error) {
	results := make([]LibraryTTLInfo, 0)
	for bucket := 0; bucket < db.GCDiscoveryBucketCount; bucket++ {
		iter := s.db.Session().Query(`
			SELECT org_id, library_id FROM gc_libraries_by_policy WHERE policy_type = ? AND bucket = ?
		`, policyType, bucket).Iter()

		var orgIDStr, libraryIDStr string
		for iter.Scan(&orgIDStr, &libraryIDStr) {
			var headCommitID string
			var versionTTLDays, autoDeleteDays int
			var deletedAt *time.Time
			err := s.db.Session().Query(`
				SELECT head_commit_id, version_ttl_days, auto_delete_days, deleted_at FROM libraries WHERE org_id = ? AND library_id = ?
			`, orgIDStr, libraryIDStr).Scan(&headCommitID, &versionTTLDays, &autoDeleteDays, &deletedAt)
			if errors.Is(err, gocql.ErrNotFound) {
				continue
			}
			if err != nil {
				iter.Close()
				return nil, fmt.Errorf("failed to revalidate library policy row for %s/%s: %w", orgIDStr, libraryIDStr, err)
			}
			if deletedAt != nil && !deletedAt.IsZero() {
				continue
			}

			days := versionTTLDays
			if policyType == db.GCLibraryPolicyAutoDelete {
				days = autoDeleteDays
			}
			if days <= 0 {
				continue
			}

			results = append(results, LibraryTTLInfo{
				OrgID:          parseUUID(orgIDStr),
				LibraryID:      parseUUID(libraryIDStr),
				HeadCommitID:   headCommitID,
				VersionTTLDays: days,
			})
		}
		if err := iter.Close(); err != nil {
			return nil, fmt.Errorf("failed to list libraries for policy %s: %w", policyType, err)
		}
	}
	return results, nil
}

// --- Share link deletion ---

func (s *CassandraStore) DeleteShareLink(shareToken string, fallbackOrgID uuid.UUID, fallbackLibraryID uuid.UUID) error {
	// Read clustering keys from primary table for canonical delete + projection cleanup
	var orgID, createdBy, libraryID, linkType string
	var createdAt time.Time
	var expiresAt *time.Time
	err := s.db.Session().Query(`
		SELECT org_id, created_by, library_id, created_at, link_type, expires_at FROM share_links WHERE link_token = ?
	`, shareToken).Scan(&orgID, &createdBy, &libraryID, &createdAt, &linkType, &expiresAt)
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
	if expiresAt != nil && !expiresAt.IsZero() {
		db.AddDeleteShareLinkExpiryQuery(batch, shareToken, *expiresAt)
	}
	db.AddDeleteAdminLinkReadModelQuery(batch, linkType, createdAt, orgID, shareToken)
	if err := batch.Exec(); err != nil {
		return err
	}
	db.BestEffortAdjustAdminOrgLinkCount(s.db.Session(), orgID, linkType, db.AdminOrgLinkCountDelta(-1))
	return nil
}

func (s *CassandraStore) DeleteExpiredShareLink(link ExpiredShareLinkInfo) error {
	orgID := link.OrgID.String()
	libraryID := link.LibraryID.String()
	createdBy := link.CreatedBy.String()
	createdAt := link.CreatedAt
	linkType := link.LinkType
	expiresAt := link.ExpiresAt.UTC()

	var canonicalOrgID, canonicalCreatedBy, canonicalLibraryID, canonicalLinkType string
	var canonicalCreatedAt time.Time
	var canonicalExpiresAt *time.Time
	err := s.db.Session().Query(`
		SELECT org_id, created_by, library_id, created_at, link_type, expires_at FROM share_links WHERE link_token = ?
	`, link.ShareToken).Scan(&canonicalOrgID, &canonicalCreatedBy, &canonicalLibraryID, &canonicalCreatedAt, &canonicalLinkType, &canonicalExpiresAt)
	if err != nil && !errors.Is(err, gocql.ErrNotFound) {
		return err
	}
	if err == nil {
		if canonicalExpiresAt == nil || canonicalExpiresAt.IsZero() || canonicalExpiresAt.After(time.Now()) {
			batch := s.db.Session().Batch(gocql.LoggedBatch)
			db.AddDeleteShareLinkExpiryQuery(batch, link.ShareToken, expiresAt)
			return batch.Exec()
		}
		orgID = canonicalOrgID
		libraryID = canonicalLibraryID
		createdBy = canonicalCreatedBy
		createdAt = canonicalCreatedAt
		linkType = canonicalLinkType
		expiresAt = canonicalExpiresAt.UTC()
	}

	batch := s.db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`DELETE FROM share_links WHERE link_token = ?`, link.ShareToken)
	batch.Query(`DELETE FROM share_links_by_creator WHERE org_id = ? AND created_by = ? AND created_at = ? AND link_token = ?`,
		orgID, createdBy, createdAt, link.ShareToken)
	batch.Query(`DELETE FROM share_links_by_library WHERE org_id = ? AND library_id = ? AND link_token = ?`,
		orgID, libraryID, link.ShareToken)
	db.AddDeleteShareLinkExpiryQuery(batch, link.ShareToken, expiresAt)
	if linkType != "" {
		db.AddDeleteAdminLinkReadModelQuery(batch, linkType, createdAt, orgID, link.ShareToken)
	}
	if err := batch.Exec(); err != nil {
		return err
	}
	if linkType != "" {
		db.BestEffortAdjustAdminOrgLinkCount(s.db.Session(), orgID, linkType, db.AdminOrgLinkCountDelta(-1))
	}
	return nil
}

// --- Expired shares (user-to-user) ---

func (s *CassandraStore) ListExpiredShares() ([]ExpiredShareInfo, error) {
	now := time.Now()
	cutoffDay := db.GCProjectionUTCDate(now)
	startDay, err := s.loadExpiredSharesStartDay(cutoffDay)
	if err != nil {
		return nil, err
	}
	if startDay.After(cutoffDay) {
		return nil, nil
	}

	var results []ExpiredShareInfo
	for day := startDay; !day.After(cutoffDay); day = day.AddDate(0, 0, 1) {
		for bucket := 0; bucket < db.GCDiscoveryBucketCount; bucket++ {
			iter := s.db.Session().Query(`
				SELECT expires_at, org_id, library_id, share_id, shared_to, shared_to_type, shared_by, created_at
				FROM gc_shares_by_expiry
				WHERE expiry_day = ? AND bucket = ?
			`, day, bucket).Iter()

			var expiresAt, createdAt time.Time
			var orgIDStr, libIDStr, shareIDStr, sharedToStr, sharedToType, sharedByStr string
			for iter.Scan(&expiresAt, &orgIDStr, &libIDStr, &shareIDStr, &sharedToStr, &sharedToType, &sharedByStr, &createdAt) {
				if expiresAt.IsZero() || expiresAt.After(now) {
					continue
				}
				results = append(results, ExpiredShareInfo{
					OrgID:        parseUUID(orgIDStr),
					LibraryID:    parseUUID(libIDStr),
					ShareID:      parseUUID(shareIDStr),
					SharedBy:     parseUUID(sharedByStr),
					SharedTo:     parseUUID(sharedToStr),
					SharedToType: sharedToType,
					CreatedAt:    createdAt,
					ExpiresAt:    expiresAt,
				})
			}
			if err := iter.Close(); err != nil {
				return nil, fmt.Errorf("failed to list expired shares: %w", err)
			}
		}
	}

	return results, nil
}

func (s *CassandraStore) loadExpiredSharesStartDay(cutoffDay time.Time) (time.Time, error) {
	value, err := s.LoadGCStats(gcExpiredSharesCursorKey)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return expiredSharesScanStartDay(time.Time{}, cutoffDay), nil
		}
		return time.Time{}, err
	}
	lastDay, err := db.ParseGCProjectionDate(value)
	if err != nil {
		return time.Time{}, err
	}
	return expiredSharesScanStartDay(lastDay, cutoffDay), nil
}

func expiredSharesScanStartDay(lastProcessedDay, cutoffDay time.Time) time.Time {
	if lastProcessedDay.IsZero() {
		return cutoffDay.AddDate(0, 0, -gcInitialScanLookbackDays)
	}
	return lastProcessedDay.AddDate(0, 0, -gcScanOverlapDays)
}

func (s *CassandraStore) DeleteShare(libraryID, shareID uuid.UUID) error {
	row, err := db.ReadShareReadModelRow(s.db.Session(), libraryID.String(), shareID.String())
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return nil
		}
		return err
	}

	batch := s.db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		DELETE FROM shares WHERE library_id = ? AND share_id = ?
	`, libraryID.String(), shareID.String())
	if row.ExpiresAt != nil && !row.ExpiresAt.IsZero() {
		db.AddDeleteShareExpiryQuery(batch, shareID.String(), *row.ExpiresAt, row.OrgID, libraryID.String())
	}
	db.AddDeleteShareReadModelQuery(batch, row)
	return batch.Exec()
}

func (s *CassandraStore) DeleteExpiredShare(share ExpiredShareInfo) error {
	row := db.ShareReadModelRow{
		OrgID:        share.OrgID.String(),
		LibraryID:    share.LibraryID.String(),
		ShareID:      share.ShareID.String(),
		SharedBy:     share.SharedBy.String(),
		SharedTo:     share.SharedTo.String(),
		SharedToType: share.SharedToType,
		CreatedAt:    share.CreatedAt,
		ExpiresAt:    &share.ExpiresAt,
	}

	var canonicalOrgID, canonicalSharedBy, canonicalSharedTo, canonicalSharedToType string
	var canonicalCreatedAt time.Time
	var canonicalExpiresAt *time.Time
	err := s.db.Session().Query(`
		SELECT org_id, shared_by, shared_to, shared_to_type, created_at, expires_at FROM shares WHERE library_id = ? AND share_id = ?
	`, share.LibraryID.String(), share.ShareID.String()).Scan(&canonicalOrgID, &canonicalSharedBy, &canonicalSharedTo, &canonicalSharedToType, &canonicalCreatedAt, &canonicalExpiresAt)
	if err != nil && !errors.Is(err, gocql.ErrNotFound) {
		return err
	}
	if err == nil {
		if canonicalExpiresAt == nil || canonicalExpiresAt.IsZero() || canonicalExpiresAt.After(time.Now()) {
			batch := s.db.Session().Batch(gocql.LoggedBatch)
			db.AddDeleteShareExpiryQuery(batch, share.ShareID.String(), share.ExpiresAt, share.OrgID.String(), share.LibraryID.String())
			return batch.Exec()
		}
		row.OrgID = canonicalOrgID
		row.SharedBy = canonicalSharedBy
		row.SharedTo = canonicalSharedTo
		row.SharedToType = canonicalSharedToType
		row.CreatedAt = canonicalCreatedAt
		row.ExpiresAt = canonicalExpiresAt
	}

	batch := s.db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`DELETE FROM shares WHERE library_id = ? AND share_id = ?`, share.LibraryID.String(), share.ShareID.String())
	db.AddDeleteShareExpiryQuery(batch, share.ShareID.String(), *row.ExpiresAt, row.OrgID, row.LibraryID)
	db.AddDeleteShareReadModelQuery(batch, row)
	return batch.Exec()
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
		var expiresAt *time.Time
		if err := s.db.Session().Query(`SELECT expires_at FROM share_links WHERE link_token = ?`, link.token).Scan(&expiresAt); err != nil && !errors.Is(err, gocql.ErrNotFound) {
			continue
		}
		batch := s.db.Session().Batch(gocql.LoggedBatch)
		batch.Query(`DELETE FROM share_links WHERE link_token = ?`, link.token)
		batch.Query(`DELETE FROM share_links_by_creator WHERE org_id = ? AND created_by = ? AND created_at = ? AND link_token = ?`,
			orgID.String(), link.createdBy, link.createdAt, link.token)
		batch.Query(`DELETE FROM share_links_by_library WHERE org_id = ? AND library_id = ? AND link_token = ?`,
			orgID.String(), libraryID.String(), link.token)
		if expiresAt != nil && !expiresAt.IsZero() {
			db.AddDeleteShareLinkExpiryQuery(batch, link.token, *expiresAt)
		}
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
	// Read the per-repo projection (single-partition read) instead of a
	// secondary-index scan over starred_files.
	libraryIDStr := libraryID.String()
	iter := s.db.Session().Query(`
		SELECT user_id, path FROM starred_files_by_repo WHERE repo_id = ?
	`, libraryIDStr).Iter()

	type starEntry struct {
		userID string
		path   string
	}
	var entries []starEntry
	var userID, path string
	for iter.Scan(&userID, &path) {
		entries = append(entries, starEntry{userID: userID, path: path})
	}
	if err := iter.Close(); err != nil {
		return fmt.Errorf("scan starred_files_by_repo for library %s: %w", libraryID, err)
	}

	// Delete the canonical user-partitioned rows per (user, path). Do not drop
	// the repo-partitioned projection until every canonical delete batch
	// succeeds; otherwise a partial failure would strand canonical rows with no
	// reverse lookup left for retry-based cleanup.
	for i := 0; i < len(entries); i += maxBatchSize {
		end := i + maxBatchSize
		if end > len(entries) {
			end = len(entries)
		}
		batch := s.db.Session().Batch(gocql.UnloggedBatch)
		for _, e := range entries[i:end] {
			batch.Query(`DELETE FROM starred_files WHERE user_id = ? AND repo_id = ? AND path = ?`,
				e.userID, libraryIDStr, e.path)
		}
		if err := batch.Exec(); err != nil {
			return fmt.Errorf("delete starred_files canonicals for library %s: %w", libraryID, err)
		}
	}

	// Drop the whole projection partition for this repo in one shot only after
	// the canonical rows are gone.
	if err := s.db.Session().Query(`
		DELETE FROM starred_files_by_repo WHERE repo_id = ?
	`, libraryIDStr).Exec(); err != nil {
		return fmt.Errorf("delete starred_files_by_repo partition for library %s: %w", libraryID, err)
	}
	return nil
}

func (s *CassandraStore) DeleteMonitoredReposByLibrary(libraryID uuid.UUID) error {
	iter := s.db.Session().Query(`
		SELECT user_id FROM monitored_repos_by_repo WHERE repo_id = ?
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
			batch.Query(`DELETE FROM monitored_repos_by_repo WHERE repo_id = ? AND user_id = ?`,
				libraryID.String(), uid)
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
	cutoffDay := db.GCProjectionUTCDate(cutoff)
	startDay, err := s.loadDeletedUsersStartDay(cutoffDay)
	if err != nil {
		return nil, err
	}
	if startDay.After(cutoffDay) {
		return nil, nil
	}

	var result []DeletedUserInfo
	for day := startDay; !day.After(cutoffDay); day = day.AddDate(0, 0, 1) {
		for bucket := 0; bucket < db.GCDiscoveryBucketCount; bucket++ {
			iter := s.db.Session().Query(`
				SELECT deleted_at, org_id, user_id
				FROM gc_deleted_users_by_deleted_day
				WHERE deleted_day = ? AND bucket = ?
			`, day, bucket).Iter()

			var deletedAt time.Time
			var orgIDStr, userIDStr string
			for iter.Scan(&deletedAt, &orgIDStr, &userIDStr) {
				orgID, err := uuid.Parse(orgIDStr)
				if err != nil {
					continue
				}
				userID, err := uuid.Parse(userIDStr)
				if err != nil {
					continue
				}

				var email, status string
				var canonicalDeletedAt *time.Time
				err = s.db.Session().Query(`
					SELECT email, status, deleted_at FROM users WHERE org_id = ? AND user_id = ?
				`, orgIDStr, userIDStr).Scan(&email, &status, &canonicalDeletedAt)
				if errors.Is(err, gocql.ErrNotFound) {
					continue
				}
				if err != nil {
					iter.Close()
					return nil, err
				}
				if status != "deleted" || canonicalDeletedAt == nil || canonicalDeletedAt.IsZero() {
					continue
				}
				if !canonicalDeletedAt.UTC().Equal(deletedAt.UTC()) || canonicalDeletedAt.After(cutoff) {
					continue
				}

				result = append(result, DeletedUserInfo{
					OrgID:     orgID,
					UserID:    userID,
					Email:     email,
					DeletedAt: canonicalDeletedAt.UTC(),
				})
			}
			if err := iter.Close(); err != nil {
				return nil, err
			}
		}
	}

	return result, nil
}

func (s *CassandraStore) loadDeletedUsersStartDay(cutoffDay time.Time) (time.Time, error) {
	value, err := s.LoadGCStats(gcDeletedUsersCursorKey)
	return deletedUsersStartDayFromCursor(value, err, cutoffDay)
}

func deletedUsersStartDayFromCursor(value string, loadErr error, cutoffDay time.Time) (time.Time, error) {
	if loadErr != nil {
		if errors.Is(loadErr, gocql.ErrNotFound) {
			return deletedUsersScanStartDay(time.Time{}, cutoffDay), nil
		}
		return time.Time{}, loadErr
	}
	lastDay, err := db.ParseGCProjectionDate(value)
	if err != nil {
		return time.Time{}, err
	}
	return deletedUsersScanStartDay(lastDay, cutoffDay), nil
}

func deletedUsersScanStartDay(lastProcessedDay, cutoffDay time.Time) time.Time {
	if lastProcessedDay.IsZero() {
		return cutoffDay.AddDate(0, 0, -gcInitialScanLookbackDays)
	}
	return lastProcessedDay.AddDate(0, 0, -gcScanOverlapDays)
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
	previousRow, err := db.ReadAdminLibraryProjectionRow(s.db.Session(), orgID.String(), libraryID.String())
	if err != nil && !errors.Is(err, gocql.ErrNotFound) {
		return err
	}
	ownerID := previousRow.OwnerID
	storageClass := previousRow.StorageClass
	if errors.Is(err, gocql.ErrNotFound) {
		if baseErr := s.db.Session().Query(
			`SELECT owner_id, storage_class FROM libraries WHERE org_id = ? AND library_id = ?`,
			orgID.String(), libraryID.String(),
		).Scan(&ownerID, &storageClass); baseErr != nil && !errors.Is(baseErr, gocql.ErrNotFound) {
			return baseErr
		}
	}

	now := time.Now().UTC()
	batch := s.db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		UPDATE libraries SET deleted_at = ?, deleted_by = ?, updated_at = ? WHERE org_id = ? AND library_id = ?
	`, now, deletedBy.String(), now, orgID.String(), libraryID.String())
	batch.Query(`
		INSERT INTO deleted_libraries (library_id, org_id, deleted_at, storage_class) VALUES (?, ?, ?, ?)
	`, libraryID.String(), orgID.String(), now, storageClass)
	traffic.AddAggregateStorageReconciliationQueries(batch, orgID.String(), ownerID, now)
	if err == nil {
		nextRow := previousRow
		nextRow.UpdatedAt = now
		nextRow.DeletedAt = &now
		db.AddRefreshAdminLibraryReadModelQueries(batch, nextRow, &previousRow)
	}
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

func (s *CassandraStore) ListSharesByUser(orgID, userID uuid.UUID) ([]ShareByUserInfo, error) {
	iter := s.db.Session().Query(`
		SELECT user_id, library_id, share_id FROM shares_by_user_org WHERE org_id = ? AND user_id = ?
	`, orgID.String(), userID.String()).Iter()

	var sharedToStr, libIDStr, shareIDStr string
	var result []ShareByUserInfo
	for iter.Scan(&sharedToStr, &libIDStr, &shareIDStr) {
		result = append(result, ShareByUserInfo{
			SharedTo:  parseUUID(sharedToStr),
			LibraryID: parseUUID(libIDStr),
			ShareID:   parseUUID(shareIDStr),
		})
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *CassandraStore) ListSharesCreatedByUser(orgID, userID uuid.UUID) ([]ShareByCreatorInfo, error) {
	iter := s.db.Session().Query(`
		SELECT library_id, share_id FROM shares_by_creator WHERE org_id = ? AND shared_by = ?
	`, orgID.String(), userID.String()).Iter()

	var libIDStr, shareIDStr string
	var result []ShareByCreatorInfo
	for iter.Scan(&libIDStr, &shareIDStr) {
		result = append(result, ShareByCreatorInfo{
			LibraryID: parseUUID(libIDStr),
			ShareID:   parseUUID(shareIDStr),
		})
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *CassandraStore) DeleteStarredFilesByUser(userID uuid.UUID) error {
	// The starred_files_by_repo projection is partitioned by repo_id, so the
	// user's rows are scattered across repo partitions and cannot be removed by
	// the single canonical partition delete. Enumerate the user's stars first
	// and tear down each projection row before dropping the canonical partition;
	// if any projection delete fails, keep the canonical partition intact so a
	// retry can still enumerate and finish cleanup.
	userIDStr := userID.String()
	iter := s.db.Session().Query(`
		SELECT repo_id, path FROM starred_files WHERE user_id = ?
	`, userIDStr).Iter()

	type starEntry struct {
		repoID string
		path   string
	}
	var entries []starEntry
	var repoID, path string
	for iter.Scan(&repoID, &path) {
		entries = append(entries, starEntry{repoID: repoID, path: path})
	}
	if err := iter.Close(); err != nil {
		return fmt.Errorf("scan starred_files for user %s: %w", userID, err)
	}

	for i := 0; i < len(entries); i += maxBatchSize {
		end := i + maxBatchSize
		if end > len(entries) {
			end = len(entries)
		}
		batch := s.db.Session().Batch(gocql.UnloggedBatch)
		for _, e := range entries[i:end] {
			batch.Query(`DELETE FROM starred_files_by_repo WHERE repo_id = ? AND user_id = ? AND path = ?`,
				e.repoID, userIDStr, e.path)
		}
		if err := batch.Exec(); err != nil {
			return fmt.Errorf("delete starred_files_by_repo rows for user %s: %w", userID, err)
		}
	}

	return s.db.Session().Query(`
		DELETE FROM starred_files WHERE user_id = ?
	`, userIDStr).Exec()
}

func (s *CassandraStore) DeleteMonitoredReposByUser(userID uuid.UUID) error {
	iter := s.db.Session().Query(`
		SELECT repo_id FROM monitored_repos WHERE user_id = ?
	`, userID.String()).Iter()

	var repoIDs []string
	var repoID string
	for iter.Scan(&repoID) {
		repoIDs = append(repoIDs, repoID)
	}
	if err := iter.Close(); err != nil {
		return err
	}
	if len(repoIDs) == 0 {
		return nil
	}

	for i := 0; i < len(repoIDs); i += maxBatchSize {
		end := i + maxBatchSize
		if end > len(repoIDs) {
			end = len(repoIDs)
		}
		batch := s.db.Session().Batch(gocql.UnloggedBatch)
		for _, rid := range repoIDs[i:end] {
			batch.Query(`DELETE FROM monitored_repos WHERE user_id = ? AND repo_id = ?`, userID.String(), rid)
			batch.Query(`DELETE FROM monitored_repos_by_repo WHERE repo_id = ? AND user_id = ?`, rid, userID.String())
		}
		if err := batch.Exec(); err != nil {
			return err
		}
	}
	return nil
}

func (s *CassandraStore) DeleteAPIKeysByUser(orgID, userID uuid.UUID) error {
	iter := s.db.Session().Query(
		`SELECT key_hash, created_at FROM api_keys_by_user WHERE org_id = ? AND user_id = ?`,
		orgID.String(), userID.String(),
	).Iter()

	type keyRef struct {
		hash      string
		createdAt time.Time
	}
	var refs []keyRef
	var ref keyRef
	for iter.Scan(&ref.hash, &ref.createdAt) {
		refs = append(refs, ref)
	}
	if err := iter.Close(); err != nil {
		return err
	}
	if len(refs) == 0 {
		return nil
	}

	for i := 0; i < len(refs); i += 25 {
		end := i + 25
		if end > len(refs) {
			end = len(refs)
		}
		batch := s.db.Session().Batch(gocql.UnloggedBatch)
		for _, r := range refs[i:end] {
			batch.Query(`DELETE FROM api_keys WHERE key_hash = ?`, r.hash)
			batch.Query(`DELETE FROM api_keys_by_user WHERE org_id = ? AND user_id = ? AND created_at = ?`, orgID.String(), userID.String(), r.createdAt)
		}
		if err := batch.Exec(); err != nil {
			return err
		}
	}

	return nil
}

func (s *CassandraStore) buildAdminOrganizationProjectionRowAfterUserDelete(orgID, deletedUserID string) (db.AdminOrganizationProjectionRow, error) {
	row := db.AdminOrganizationProjectionRow{OrgID: orgID}
	var deletedAt *time.Time
	if err := s.db.Session().Query(`
		SELECT name, status, plan, storage_quota, deleted_at, created_at
		FROM organizations WHERE org_id = ?
	`, orgID).Scan(&row.Name, &row.Status, &row.Plan, &row.StorageQuota, &deletedAt, &row.CreatedAt); err != nil {
		return db.AdminOrganizationProjectionRow{}, err
	}
	if row.Status == "" {
		row.Status = "active"
	}
	row.DeletedAt = deletedAt

	iter := s.db.Session().Query(`
		SELECT user_id, email, name, role FROM users WHERE org_id = ?
	`, orgID).Iter()

	var userID, email, name, role string
	var firstEmail, firstName string
	firstRemaining := true
	for iter.Scan(&userID, &email, &name, &role) {
		if userID == deletedUserID {
			continue
		}
		row.UsersCount++
		if firstRemaining {
			firstEmail, firstName = email, name
			firstRemaining = false
		}
		if row.OwnerEmail == "" && (role == "superadmin" || role == "owner" || role == "admin") {
			row.OwnerEmail, row.OwnerName = email, name
		}
	}
	if err := iter.Close(); err != nil {
		return db.AdminOrganizationProjectionRow{}, err
	}
	if row.OwnerEmail == "" {
		row.OwnerEmail, row.OwnerName = firstEmail, firstName
	}
	return row, nil
}

func (s *CassandraStore) HardDeleteUser(orgID, userID uuid.UUID, email string) error {
	session := s.db.Session()
	var deletedAt *time.Time
	deletedAtErr := session.Query(`
		SELECT deleted_at FROM users WHERE org_id = ? AND user_id = ?
	`, orgID.String(), userID.String()).Scan(&deletedAt)
	if deletedAtErr != nil && !errors.Is(deletedAtErr, gocql.ErrNotFound) {
		return deletedAtErr
	}

	userState, err := db.ReadAdminUserProjectionState(session, userID.String())
	hasUserState := err == nil
	if err != nil && !errors.Is(err, gocql.ErrNotFound) {
		return err
	}

	orgState, err := db.ReadAdminOrganizationProjectionState(session, orgID.String())
	hasOrgState := err == nil
	if err != nil && !errors.Is(err, gocql.ErrNotFound) {
		return err
	}

	nextOrgRow, err := s.buildAdminOrganizationProjectionRowAfterUserDelete(orgID.String(), userID.String())
	hasNextOrgRow := err == nil
	if err != nil && !errors.Is(err, gocql.ErrNotFound) {
		return err
	}

	batch := session.Batch(gocql.LoggedBatch)
	if hasUserState {
		db.AddDeleteAdminUserReadModelQuery(batch, userState)
	}
	batch.Query(`
		DELETE FROM users WHERE org_id = ? AND user_id = ?
	`, orgID.String(), userID.String())
	if deletedAtErr == nil && deletedAt != nil && !deletedAt.IsZero() {
		db.AddDeleteDeletedUserDiscoveryQuery(batch, orgID.String(), userID.String(), *deletedAt)
	}
	if email != "" {
		batch.Query(`
			DELETE FROM users_by_email WHERE email = ?
		`, email)
	}
	if hasOrgState && hasNextOrgRow && (orgState.Status != nextOrgRow.Status || !orgState.CreatedAt.Equal(nextOrgRow.CreatedAt)) {
		db.AddDeleteAdminOrganizationReadModelQuery(batch, orgState)
	}
	if hasNextOrgRow {
		db.AddUpsertAdminOrganizationReadModelQuery(batch, nextOrgRow)
	} else if hasOrgState {
		db.AddDeleteAdminOrganizationReadModelQuery(batch, orgState)
	}
	if err := batch.Exec(); err != nil {
		return err
	}
	return nil
}

func (s *CassandraStore) AcquireUserHardDeleteLock(userID, leaseToken uuid.UUID) (bool, error) {
	return s.acquireHardDeleteLock("gc_user_hard_delete_locks", "user_id", userID, leaseToken)
}

func (s *CassandraStore) RenewUserHardDeleteLock(userID, leaseToken uuid.UUID) (bool, error) {
	applied, err := s.db.Session().Query(`
		UPDATE gc_user_hard_delete_locks USING TTL 21600
		SET heartbeat = ?, lease_token = ?
		WHERE user_id = ? IF lease_token = ?
	`, time.Now().UTC(), leaseToken.String(), userID.String(), leaseToken.String()).MapScanCAS(map[string]interface{}{})
	if err != nil {
		return false, err
	}
	return applied, nil
}

func (s *CassandraStore) ReleaseUserHardDeleteLock(userID, leaseToken uuid.UUID) error {
	_, err := s.db.Session().Query(`
		DELETE FROM gc_user_hard_delete_locks WHERE user_id = ? IF lease_token = ?
	`, userID.String(), leaseToken.String()).MapScanCAS(map[string]interface{}{})
	return err
}

func (s *CassandraStore) AcquireLibraryHardDeleteLock(libraryID, leaseToken uuid.UUID) (bool, error) {
	return s.acquireHardDeleteLock("gc_library_hard_delete_locks", "library_id", libraryID, leaseToken)
}

func (s *CassandraStore) RenewLibraryHardDeleteLock(libraryID, leaseToken uuid.UUID) (bool, error) {
	applied, err := s.db.Session().Query(`
		UPDATE gc_library_hard_delete_locks USING TTL 21600
		SET heartbeat = ?, lease_token = ?
		WHERE library_id = ? IF lease_token = ?
	`, time.Now().UTC(), leaseToken.String(), libraryID.String(), leaseToken.String()).MapScanCAS(map[string]interface{}{})
	if err != nil {
		return false, err
	}
	return applied, nil
}

func (s *CassandraStore) ReleaseLibraryHardDeleteLock(libraryID, leaseToken uuid.UUID) error {
	_, err := s.db.Session().Query(`
		DELETE FROM gc_library_hard_delete_locks WHERE library_id = ? IF lease_token = ?
	`, libraryID.String(), leaseToken.String()).MapScanCAS(map[string]interface{}{})
	return err
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
		SELECT library_id, org_id, deleted_at, storage_class FROM deleted_libraries
	`).Iter()

	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	var libIDStr, orgIDStr, storageClass string
	var deletedAt time.Time
	var result []DeletedLibraryInfo
	for iter.Scan(&libIDStr, &orgIDStr, &deletedAt, &storageClass) {
		if deletedAt.Before(cutoff) {
			orgID := parseUUID(orgIDStr)
			libID := parseUUID(libIDStr)
			if storageClass == "" {
				storageClass, _ = s.GetLibraryStorageClass(orgID, libID)
			}
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
	session := s.db.Session()
	batch := session.Batch(gocql.LoggedBatch)
	if err := db.AddDeleteAdminLibraryReadModelQueries(session, batch, orgID.String(), libraryID.String()); err != nil {
		return err
	}

	db.AddDeleteLibraryPolicyQuery(batch, db.GCLibraryPolicyVersionTTL, orgID.String(), libraryID.String())
	db.AddDeleteLibraryPolicyQuery(batch, db.GCLibraryPolicyAutoDelete, orgID.String(), libraryID.String())
	batch.Query(`DELETE FROM libraries WHERE org_id = ? AND library_id = ?`,
		orgID.String(), libraryID.String())
	batch.Query(`DELETE FROM libraries_by_id WHERE library_id = ?`,
		libraryID.String())
	batch.Query(`DELETE FROM deleted_libraries WHERE library_id = ?`,
		libraryID.String())
	return batch.Exec()
}

// --- Org cascade (Fase 4) ---

func (s *CassandraStore) ListExpiredDeletedOrgs(graceDays int) ([]DeletedOrgInfo, error) {
	iter := s.db.Session().Query(`
		SELECT org_id, name, deleted_at FROM deleted_organizations
	`).Iter()

	cutoff := time.Now().AddDate(0, 0, -graceDays)
	var orgIDStr, name string
	var deletedAt time.Time
	var result []DeletedOrgInfo
	for iter.Scan(&orgIDStr, &name, &deletedAt) {
		if !deletedAt.IsZero() && deletedAt.Before(cutoff) {
			result = append(result, DeletedOrgInfo{
				OrgID:     parseUUID(orgIDStr),
				Name:      name,
				DeletedAt: deletedAt,
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
	return traffic.DeleteLibraryStorageCounter(s.db, orgID.String(), libraryID.String())
}

func (s *CassandraStore) DeleteGroupFull(orgID, groupID uuid.UUID) error {
	session := s.db.Session()
	projectionRow, err := db.ReadAdminGroupProjectionRow(session, orgID.String(), groupID.String())
	hasProjectionRow := err == nil
	if err != nil && !errors.Is(err, gocql.ErrNotFound) {
		return err
	}

	// Clean up groups_by_member for each member
	iter := session.Query(`
		SELECT user_id FROM group_members WHERE group_id = ?
	`, groupID.String()).Iter()

	memberIDs := make([]string, 0)
	var userIDStr string
	for iter.Scan(&userIDStr) {
		memberIDs = append(memberIDs, userIDStr)
	}
	if err := iter.Close(); err != nil {
		return err
	}

	shareIter := session.Query(`
		SELECT created_at, library_id, share_id, permission,
		       shared_by, shared_by_email, shared_by_name, repo_name, encrypted, size_bytes
		FROM shares_by_group WHERE org_id = ? AND group_id = ?
	`, orgID.String(), groupID.String()).Iter()

	shareRows := make([]db.ShareReadModelRow, 0)
	var shareRow db.ShareReadModelRow
	for shareIter.Scan(
		&shareRow.CreatedAt,
		&shareRow.LibraryID,
		&shareRow.ShareID,
		&shareRow.Permission,
		&shareRow.SharedBy,
		&shareRow.SharedByEmail,
		&shareRow.SharedByName,
		&shareRow.RepoName,
		&shareRow.Encrypted,
		&shareRow.SizeBytes,
	) {
		shareRow.OrgID = orgID.String()
		shareRow.SharedTo = groupID.String()
		shareRow.SharedToType = "group"
		shareRows = append(shareRows, shareRow)
		shareRow = db.ShareReadModelRow{}
	}
	if err := shareIter.Close(); err != nil {
		return err
	}
	shareRows, err = db.HydrateShareReadModelRows(session, shareRows)
	if err != nil {
		return err
	}

	// Delete members, shares, group record, and by_id lookup
	batch := session.Batch(gocql.LoggedBatch)
	for _, memberID := range memberIDs {
		batch.Query(`DELETE FROM groups_by_member WHERE org_id = ? AND user_id = ? AND group_id = ?`,
			orgID.String(), memberID, groupID.String())
	}
	for _, share := range shareRows {
		batch.Query(`DELETE FROM shares WHERE library_id = ? AND share_id = ?`, share.LibraryID, share.ShareID)
		if share.ExpiresAt != nil {
			db.AddDeleteShareExpiryQuery(batch, share.ShareID, *share.ExpiresAt, share.OrgID, share.LibraryID)
		}
		db.AddDeleteShareReadModelQuery(batch, share)
	}
	batch.Query(`DELETE FROM group_members WHERE group_id = ?`, groupID.String())
	batch.Query(`DELETE FROM groups WHERE org_id = ? AND group_id = ?`, orgID.String(), groupID.String())
	batch.Query(`DELETE FROM groups_by_id WHERE group_id = ?`, groupID.String())
	if hasProjectionRow {
		db.AddDeleteAdminGroupReadModelQuery(batch, projectionRow)
	}
	return batch.Exec()
}

func (s *CassandraStore) AcquireOrgHardDeleteLock(orgID, leaseToken uuid.UUID) (bool, error) {
	return s.acquireHardDeleteLock("gc_org_hard_delete_locks", "org_id", orgID, leaseToken)
}

func (s *CassandraStore) RenewOrgHardDeleteLock(orgID, leaseToken uuid.UUID) (bool, error) {
	applied, err := s.db.Session().Query(`
		UPDATE gc_org_hard_delete_locks USING TTL 21600
		SET heartbeat = ?, lease_token = ?
		WHERE org_id = ? IF lease_token = ?
	`, time.Now().UTC(), leaseToken.String(), orgID.String(), leaseToken.String()).MapScanCAS(map[string]interface{}{})
	if err != nil {
		return false, err
	}
	return applied, nil
}

func (s *CassandraStore) ReleaseOrgHardDeleteLock(orgID, leaseToken uuid.UUID) error {
	_, err := s.db.Session().Query(`
		DELETE FROM gc_org_hard_delete_locks WHERE org_id = ? IF lease_token = ?
	`, orgID.String(), leaseToken.String()).MapScanCAS(map[string]interface{}{})
	return err
}

func (s *CassandraStore) BeginOrgPurge(orgID uuid.UUID, identityAt time.Time) (bool, error) {
	var status string
	var deletedAt *time.Time
	err := s.db.Session().Query(`
		SELECT status, deleted_at FROM organizations WHERE org_id = ?
	`, orgID.String()).Scan(&status, &deletedAt)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	if deletedAt == nil || !deletedAt.Equal(identityAt) {
		return false, nil
	}
	if status == "purging" {
		return true, nil
	}
	if status != "deleted" {
		return false, nil
	}
	applied, err := s.db.Session().Query(`
		UPDATE organizations SET status = ? WHERE org_id = ?
		IF status = ? AND deleted_at = ?
	`, "purging", orgID.String(), "deleted", identityAt).MapScanCAS(map[string]interface{}{})
	if err != nil {
		return false, err
	}
	return applied, nil
}

func (s *CassandraStore) HardDeleteOrg(orgID uuid.UUID) error {
	leaseToken := uuid.New()
	applied, err := s.AcquireOrgHardDeleteLock(orgID, leaseToken)
	if err != nil {
		return err
	}
	if !applied {
		return fmt.Errorf("org %s hard delete already in progress", orgID)
	}
	defer s.ReleaseOrgHardDeleteLock(orgID, leaseToken) //nolint:errcheck

	deletedAt, err := s.GetOrgDeletedAt(orgID)
	if err != nil {
		return err
	}
	if deletedAt == nil {
		return fmt.Errorf("org %s is not in deleted state", orgID)
	}
	if err := s.ensureOrgHasNoLiveChildren(orgID); err != nil {
		return err
	}
	purging, err := s.BeginOrgPurge(orgID, *deletedAt)
	if err != nil {
		return err
	}
	if !purging {
		return fmt.Errorf("org %s is not in deleted state", orgID)
	}
	return s.HardDeleteOrgLocked(orgID)
}

func (s *CassandraStore) ensureOrgHasNoLiveChildren(orgID uuid.UUID) error {
	session := s.db.Session()
	var childID string
	if err := session.Query(`SELECT library_id FROM libraries WHERE org_id = ? LIMIT 1`, orgID.String()).Scan(&childID); err == nil {
		return fmt.Errorf("org %s still has live libraries", orgID)
	} else if !errors.Is(err, gocql.ErrNotFound) {
		return err
	}
	if err := session.Query(`SELECT user_id FROM users WHERE org_id = ? LIMIT 1`, orgID.String()).Scan(&childID); err == nil {
		return fmt.Errorf("org %s still has live users", orgID)
	} else if !errors.Is(err, gocql.ErrNotFound) {
		return err
	}
	if err := session.Query(`SELECT group_id FROM groups WHERE org_id = ? LIMIT 1`, orgID.String()).Scan(&childID); err == nil {
		return fmt.Errorf("org %s still has live groups", orgID)
	} else if !errors.Is(err, gocql.ErrNotFound) {
		return err
	}
	return nil
}

func (s *CassandraStore) HardDeleteOrgLocked(orgID uuid.UUID) error {
	session := s.db.Session()

	var orgStatus string
	if err := session.Query(`SELECT status FROM organizations WHERE org_id = ?`, orgID.String()).Scan(&orgStatus); err != nil {
		return err
	}
	if orgStatus != "purging" {
		return fmt.Errorf("org %s is not in purge state", orgID)
	}

	if err := s.ensureOrgHasNoLiveChildren(orgID); err != nil {
		return err
	}

	orgState, err := db.ReadAdminOrganizationProjectionState(session, orgID.String())
	hasOrgState := err == nil
	if err != nil && !errors.Is(err, gocql.ErrNotFound) {
		return err
	}
	batch := session.Batch(gocql.LoggedBatch)
	if hasOrgState {
		db.AddDeleteAdminOrganizationReadModelQuery(batch, orgState)
	}
	batch.Query(`DELETE FROM organizations WHERE org_id = ?`, orgID.String())
	batch.Query(`DELETE FROM deleted_organizations WHERE org_id = ?`, orgID.String())
	if err := batch.Exec(); err != nil {
		return err
	}
	return nil
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
