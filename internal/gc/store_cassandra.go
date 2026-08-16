package gc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"log"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/metrics"
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

const hardDeleteLockTTLSeconds = 21600

func acquireHardDeleteLock(session *gocql.Session, tableName, keyColumn string, keyValue, leaseToken uuid.UUID) (bool, error) {
	now := time.Now().UTC()
	existing := map[string]interface{}{}
	// CQL requires IF NOT EXISTS before USING on an LWT insert; the reverse order is a
	// syntax error. The explicit TTL bounds a lock leaked by a crash between acquire and
	// release, while the stale-aware takeover below reclaims it sooner than full expiry.
	applied, err := session.Query(fmt.Sprintf(`
		INSERT INTO %s (%s, started_at, heartbeat, lease_token)
		VALUES (?, ?, ?, ?) IF NOT EXISTS USING TTL %d
	`, tableName, keyColumn, hardDeleteLockTTLSeconds), keyValue.String(), now, now, leaseToken.String()).MapScanCAS(existing)
	if err != nil || applied {
		return applied, err
	}

	heartbeat := parseCASTime(existing["heartbeat"])
	existingToken := parseCASUUID(existing["lease_token"])
	if heartbeat.IsZero() || existingToken == uuid.Nil || now.Sub(heartbeat) < hardDeleteLockStaleAfter {
		return false, nil
	}

	applied, err = session.Query(fmt.Sprintf(`
		UPDATE %s USING TTL %d
		SET started_at = ?, heartbeat = ?, lease_token = ?
		WHERE %s = ? IF lease_token = ?
	`, tableName, hardDeleteLockTTLSeconds, keyColumn), now, now, leaseToken.String(), keyValue.String(), existingToken.String()).MapScanCAS(map[string]interface{}{})
	if err != nil {
		return false, err
	}
	return applied, nil
}

func renewHardDeleteLock(session *gocql.Session, tableName, keyColumn string, keyValue, leaseToken uuid.UUID) (bool, error) {
	applied, err := session.Query(fmt.Sprintf(`
		UPDATE %s USING TTL %d
		SET heartbeat = ?, lease_token = ?
		WHERE %s = ? IF lease_token = ?
	`, tableName, hardDeleteLockTTLSeconds, keyColumn), time.Now().UTC(), leaseToken.String(), keyValue.String(), leaseToken.String()).MapScanCAS(map[string]interface{}{})
	if err != nil {
		return false, err
	}
	return applied, nil
}

func releaseHardDeleteLock(session *gocql.Session, tableName, keyColumn string, keyValue, leaseToken uuid.UUID) error {
	applied, err := session.Query(fmt.Sprintf(`
		DELETE FROM %s WHERE %s = ? IF lease_token = ?
	`, tableName, keyColumn), keyValue.String(), leaseToken.String()).MapScanCAS(map[string]interface{}{})
	if err != nil {
		return err
	}
	if !applied {
		// The lease was already stolen (stale takeover) or expired by TTL before we
		// released it. Deleting is safely skipped — we no longer own the row — but this
		// signals we lost ownership mid-operation, which is worth surfacing.
		log.Printf("[gc] release %s lock %s: lease token is no longer the owner (stolen or expired)", tableName, keyValue)
	}
	return nil
}

// AcquireLibraryHardDeleteLockLease acquires the library hard-delete lock using
// the same stale-aware CAS semantics as the GC worker.
func AcquireLibraryHardDeleteLockLease(session *gocql.Session, libraryID, leaseToken uuid.UUID) (bool, error) {
	return acquireHardDeleteLock(session, "gc_library_hard_delete_locks", "library_id", libraryID, leaseToken)
}

// RenewLibraryHardDeleteLockLease fences ownership of the library hard-delete
// lock and refreshes its TTL.
func RenewLibraryHardDeleteLockLease(session *gocql.Session, libraryID, leaseToken uuid.UUID) (bool, error) {
	return renewHardDeleteLock(session, "gc_library_hard_delete_locks", "library_id", libraryID, leaseToken)
}

// ReleaseLibraryHardDeleteLockLease releases the library hard-delete lock only
// when the same lease token still owns it.
func ReleaseLibraryHardDeleteLockLease(session *gocql.Session, libraryID, leaseToken uuid.UUID) error {
	return releaseHardDeleteLock(session, "gc_library_hard_delete_locks", "library_id", libraryID, leaseToken)
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

var _ GCStore = (*CassandraStore)(nil)

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
	// EnqueueItem is the raw single-row path and cannot carry a block
	// representation, so it must never accept a type that requires one. Enforce
	// the invariant here (not just in Queue.Enqueue/EnqueueCascade) so writing
	// straight to the store can't smuggle an incomplete commit/fs_object/
	// library_cascade row past the representation contract.
	if itemTypeRequiresBlockRepresentation(itemType) {
		return fmt.Errorf("item type %s requires explicit block representation; use EnqueueBatch", itemType)
	}
	now := time.Now().UTC()
	queueBucket := gcQueueBucket(orgID, itemType, itemID)
	batch := s.db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		INSERT INTO gc_queue (org_id, bucket, queued_at, identity_at, requires_library_deleted_check, library_guard_mode, item_type, item_id, library_id, block_representation_id, storage_class, retry_count)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, orgID.String(), queueBucket, queuedAt, queuedAt, false, string(LibraryGuardNone), string(itemType), itemID, libraryID.String(), "", storageClass, retryCount)
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
	for _, item := range items {
		if err := validateQueueItemBlockRepresentation(item); err != nil {
			return err
		}
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
			guardMode := effectiveLibraryGuardMode(item.LibraryGuardMode, item.RequiresLibraryDeletedCheck)
			requiresLibraryDeletedCheck := guardMode != LibraryGuardNone
			queueBucket := gcQueueBucket(item.OrgID, item.ItemType, item.ItemID)
			batch.Query(`
				INSERT INTO gc_queue (org_id, bucket, queued_at, identity_at, requires_library_deleted_check, library_guard_mode, item_type, item_id, library_id, block_representation_id, storage_class, retry_count)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			`, item.OrgID.String(), queueBucket, item.QueuedAt, identityAt, requiresLibraryDeletedCheck, string(guardMode), string(item.ItemType), item.ItemID, item.LibraryID.String(), strings.TrimSpace(item.BlockRepresentationID), item.StorageClass, item.RetryCount)
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
	// Read under the same coerced key the write/delete helpers use, so a block dedup
	// probe always inspects the canonical uuid.Nil partition regardless of the caller.
	libraryID = pendingItemLibraryID(itemType, libraryID)
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

// pendingItemLibraryID is the single choke point that enforces the block/library
// invariant for gc_pending_items. Blocks are content-addressed and library-independent
// (processBlock never reads library_id), but the gc_pending_items key is library-scoped
// (the bucket hashes library_id and library_id is a clustering column) while gc_queue is
// not. If two producers enqueue the same block under different library_ids (worker
// cascade vs scanner, or two libraries sharing a block), the single gc_queue row keeps
// only the last writer's library_id column, but each producer wrote its OWN pending row;
// CompleteItem then deletes only the pending row matching the surviving queue row and
// orphans the other. Coercing every block pending write AND delete to uuid.Nil here makes
// all block pending rows collapse to one key regardless of the caller, so a new producer
// can never re-introduce the leak (ISSUE-GC-PENDING-ITEM-BLOCK-LIBRARY-SCOPE-01). Non-block
// items keep their real library_id (they are always enqueued with a consistent one).
func pendingItemLibraryID(itemType ItemType, libraryID uuid.UUID) uuid.UUID {
	if itemType == ItemBlock {
		return uuid.Nil
	}
	return libraryID
}

func addPendingItemBatchQuery(batch *gocql.Batch, orgID, libraryID uuid.UUID, itemType ItemType, itemID string, identityAt time.Time) {
	libraryID = pendingItemLibraryID(itemType, libraryID)
	batch.Query(`
		INSERT INTO gc_pending_items (org_id, bucket, item_type, library_id, item_id, identity_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, orgID.String(), gcPendingItemBucket(orgID, libraryID, itemType, itemID), string(itemType), libraryID.String(), itemID, identityAt)
}

func addPendingItemDeleteBatchQuery(batch *gocql.Batch, orgID, libraryID uuid.UUID, itemType ItemType, itemID string, identityAt time.Time) {
	libraryID = pendingItemLibraryID(itemType, libraryID)
	batch.Query(`
		DELETE FROM gc_pending_items
		WHERE org_id = ? AND bucket = ? AND item_type = ? AND library_id = ? AND item_id = ? AND identity_at = ?
	`, orgID.String(), gcPendingItemBucket(orgID, libraryID, itemType, itemID), string(itemType), libraryID.String(), itemID, identityAt)
}

func (s *CassandraStore) queueItemPendingInfo(orgID uuid.UUID, queuedAt time.Time, itemType ItemType, itemID string) (time.Time, uuid.UUID, string, error) {
	var identityAt time.Time
	var libraryIDStr string
	var blockRepresentationID string
	err := s.db.Session().Query(`
		SELECT identity_at, library_id, block_representation_id FROM gc_queue
		WHERE org_id = ? AND bucket = ? AND queued_at = ? AND item_type = ? AND item_id = ?
	`, orgID.String(), gcQueueBucket(orgID, itemType, itemID), queuedAt, string(itemType), itemID).Scan(&identityAt, &libraryIDStr, &blockRepresentationID)
	if err != nil {
		return time.Time{}, uuid.Nil, "", err
	}
	return identityAt, parseUUID(libraryIDStr), strings.TrimSpace(blockRepresentationID), nil
}

type failedItemRow struct {
	QueuedAt                    time.Time
	IdentityAt                  time.Time
	ExpiresAt                   time.Time
	RequiresLibraryDeletedCheck bool
	LibraryGuardMode            LibraryGuardMode
	LibraryID                   uuid.UUID
	BlockRepresentationID       string
	StorageClass                string
}

func (s *CassandraStore) failedItemInfo(orgID uuid.UUID, failedAt time.Time, itemType ItemType, itemID string) (failedItemRow, error) {
	var row failedItemRow
	var libraryIDStr string
	var blockRepresentationID string
	err := s.db.Session().Query(`
		SELECT queued_at, identity_at, expires_at, requires_library_deleted_check, library_guard_mode, library_id, block_representation_id, storage_class FROM gc_failed_items
		WHERE org_id = ? AND failed_at = ? AND item_type = ? AND item_id = ?
	`, orgID.String(), failedAt, string(itemType), itemID).Scan(&row.QueuedAt, &row.IdentityAt, &row.ExpiresAt, &row.RequiresLibraryDeletedCheck, &row.LibraryGuardMode, &libraryIDStr, &blockRepresentationID, &row.StorageClass)
	if err != nil {
		return failedItemRow{}, err
	}
	row.LibraryID = parseUUID(libraryIDStr)
	row.BlockRepresentationID = strings.TrimSpace(blockRepresentationID)
	return row, nil
}

func (s *CassandraStore) DequeueBatch(orgID uuid.UUID, batchSize int, cutoff time.Time) ([]QueueItem, error) {
	if batchSize <= 0 {
		return nil, nil
	}

	var items []QueueItem
	for bucket := 0; bucket < gcDefaultQueueBucketCount; bucket++ {
		iter := s.db.Session().Query(`
			SELECT org_id, queued_at, identity_at, requires_library_deleted_check, library_guard_mode, item_type, item_id, library_id, block_representation_id, storage_class, retry_count
			FROM gc_queue
			WHERE org_id = ? AND bucket = ? AND queued_at < ?
			LIMIT ?
		`, orgID.String(), bucket, cutoff, batchSize).Iter()

		var orgIDStr, itemTypeStr, itemID, libIDStr, blockRepresentationID, storageClass string
		var queuedAt, identityAt time.Time
		var requiresLibraryDeletedCheck bool
		var libraryGuardMode LibraryGuardMode
		var retryCount int

		for iter.Scan(&orgIDStr, &queuedAt, &identityAt, &requiresLibraryDeletedCheck, &libraryGuardMode, &itemTypeStr, &itemID,
			&libIDStr, &blockRepresentationID, &storageClass, &retryCount) {
			items = append(items, QueueItem{
				OrgID:                       parseUUID(orgIDStr),
				QueuedAt:                    queuedAt,
				IdentityAt:                  effectiveIdentityAt(queuedAt, identityAt),
				RequiresLibraryDeletedCheck: requiresLibraryDeletedCheck,
				LibraryGuardMode:            effectiveLibraryGuardMode(libraryGuardMode, requiresLibraryDeletedCheck),
				ItemType:                    ItemType(itemTypeStr),
				ItemID:                      itemID,
				LibraryID:                   parseUUID(libIDStr),
				BlockRepresentationID:       strings.TrimSpace(blockRepresentationID),
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
	identityAt, libraryID, _, err := s.queueItemPendingInfo(orgID, queuedAt, itemType, itemID)
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
func (s *CassandraStore) RequeueItem(orgID uuid.UUID, oldQueuedAt, newQueuedAt time.Time, itemType ItemType, itemID string, libraryID uuid.UUID, blockRepresentationID, storageClass string, newRetryCount int, identityAt time.Time, requiresLibraryDeletedCheck bool, libraryGuardMode LibraryGuardMode) error {
	now := time.Now().UTC()
	guardMode := effectiveLibraryGuardMode(libraryGuardMode, requiresLibraryDeletedCheck)
	batch := s.db.Session().Batch(gocql.LoggedBatch)

	// Delete old item
	batch.Query(`
		DELETE FROM gc_queue
		WHERE org_id = ? AND bucket = ? AND queued_at = ? AND item_type = ? AND item_id = ?
	`, orgID.String(), gcQueueBucket(orgID, itemType, itemID), oldQueuedAt, string(itemType), itemID)

	// Insert new item at the end of the queue
	batch.Query(`
		INSERT INTO gc_queue (org_id, bucket, queued_at, identity_at, requires_library_deleted_check, library_guard_mode, item_type, item_id, library_id, block_representation_id, storage_class, retry_count)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, orgID.String(), gcQueueBucket(orgID, itemType, itemID), newQueuedAt, effectiveIdentityAt(oldQueuedAt, identityAt), guardMode != LibraryGuardNone, string(guardMode), string(itemType), itemID, libraryID.String(), strings.TrimSpace(blockRepresentationID), storageClass, newRetryCount)
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
	identityAt, libraryID, blockRepresentationID, err := s.queueItemPendingInfo(item.OrgID, item.QueuedAt, item.ItemType, item.ItemID)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			log.Printf("[GC] Skipping DLQ move for missing queue row org=%s item_type=%s item_id=%s queued_at=%s", item.OrgID, item.ItemType, item.ItemID, item.QueuedAt.Format(time.RFC3339Nano))
			return nil
		}
		return fmt.Errorf("load queue identity for fail %s/%s: %w", item.OrgID, item.ItemID, err)
	}
	expiresAt := failedAt.UTC().Add(gcFailedItemRetention)
	effectiveIdentity := effectiveIdentityAt(item.QueuedAt, identityAt)
	guardMode := effectiveLibraryGuardMode(item.LibraryGuardMode, item.RequiresLibraryDeletedCheck)
	queueBucket := gcQueueBucket(item.OrgID, item.ItemType, item.ItemID)
	now := time.Now().UTC()

	batch := s.db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		INSERT INTO gc_failed_items (
			org_id, failed_at, expires_at, queued_at, identity_at, requires_library_deleted_check, library_guard_mode, item_type, item_id, library_id, block_representation_id, storage_class, retry_count, last_error, failure_code, resolution_status
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, item.OrgID.String(), failedAt, expiresAt, item.QueuedAt, effectiveIdentity, guardMode != LibraryGuardNone, string(guardMode), string(item.ItemType), item.ItemID, libraryID.String(), blockRepresentationID, item.StorageClass, item.RetryCount, lastError, failureCode, "open")
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
		SELECT failed_at, expires_at, queued_at, identity_at, requires_library_deleted_check, library_guard_mode, item_type, item_id, library_id, block_representation_id, storage_class, retry_count, last_error, failure_code, resolution_status, resolved_at
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
		libraryGuardMode            LibraryGuardMode
		itemType                    string
		itemID                      string
		libraryIDStr                string
		blockRepresentationID       string
		storageClass                string
		retryCount                  int
		lastError                   string
		failureCode                 string
		resolutionStatus            string
		resolvedAt                  *time.Time
	)
	for iter.Scan(&failedAt, &expiresAt, &queuedAt, &identityAt, &requiresLibraryDeletedCheck, &libraryGuardMode, &itemType, &itemID, &libraryIDStr, &blockRepresentationID, &storageClass, &retryCount, &lastError, &failureCode, &resolutionStatus, &resolvedAt) {
		items = append(items, GCFailedItemInfo{
			OrgID:                       orgID,
			FailedAt:                    failedAt,
			ExpiresAt:                   expiresAt,
			QueuedAt:                    queuedAt,
			IdentityAt:                  effectiveIdentityAt(queuedAt, identityAt),
			RequiresLibraryDeletedCheck: requiresLibraryDeletedCheck,
			LibraryGuardMode:            effectiveLibraryGuardMode(libraryGuardMode, requiresLibraryDeletedCheck),
			ItemType:                    ItemType(itemType),
			ItemID:                      itemID,
			LibraryID:                   parseUUID(libraryIDStr),
			BlockRepresentationID:       strings.TrimSpace(blockRepresentationID),
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

// parseStoredQueueLibraryID interprets a library_id string read back from
// gc_failed_items / gc_queue. It returns the parsed UUID (uuid.Nil for a
// library-less item such as an org/user cascade), the value to persist back into
// gc_queue, and an error when a non-empty value is not a valid UUID. An empty
// value is preserved as empty; a valid UUID — including the nil UUID cascades
// legitimately carry — is re-emitted in canonical form so a stray non-canonical
// spelling is normalized on the round-trip rather than propagated.
func parseStoredQueueLibraryID(raw string) (uuid.UUID, string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return uuid.Nil, "", nil
	}
	parsed, err := uuid.Parse(trimmed)
	if err != nil {
		return uuid.Nil, "", err
	}
	return parsed, parsed.String(), nil
}

func (s *CassandraStore) RequeueFailedItem(orgID uuid.UUID, failedAt time.Time, itemType ItemType, itemID string, queuedAt time.Time) error {
	var (
		failedQueuedAt              time.Time
		identityAt                  time.Time
		expiresAt                   time.Time
		requiresLibraryDeletedCheck bool
		libraryGuardMode            LibraryGuardMode
		libraryIDStr                string
		blockRepresentationID       string
		storageClass                string
	)
	err := s.db.Session().Query(`
		SELECT queued_at, identity_at, expires_at, requires_library_deleted_check, library_guard_mode, library_id, block_representation_id, storage_class
		FROM gc_failed_items WHERE org_id = ? AND failed_at = ? AND item_type = ? AND item_id = ?
	`, orgID.String(), failedAt, string(itemType), itemID).Scan(&failedQueuedAt, &identityAt, &expiresAt, &requiresLibraryDeletedCheck, &libraryGuardMode, &libraryIDStr, &blockRepresentationID, &storageClass)
	if err != nil {
		return fmt.Errorf("load failed item for requeue %s/%s: %w", orgID, itemID, err)
	}
	// Interpret the stored library_id via parseStoredQueueLibraryID instead of
	// parseUUID, which would silently coerce a corrupted value to uuid.Nil —
	// masking the corruption as a generic "requires library_id" validation error
	// and writing pending state under the nil library. A non-empty value that does
	// not parse is refused outright. The nil UUID is NOT corruption: org/user
	// cascades carry no library and legitimately persist library_id as the nil
	// UUID string, so rejecting it would break their DLQ requeue. queueLibraryID is
	// the normalized value round-tripped back into gc_queue; libraryID drives
	// validation and the pending-item row.
	libraryID, queueLibraryID, perr := parseStoredQueueLibraryID(libraryIDStr)
	if perr != nil {
		return fmt.Errorf("refusing to requeue failed item org=%s item_type=%s item_id=%s failed_at=%s: corrupted stored library_id %q: %w",
			orgID, itemType, itemID, failedAt.UTC().Format(time.RFC3339Nano), strings.TrimSpace(libraryIDStr), perr)
	}

	// RequeueFailedItem writes straight into gc_queue (it does not go through
	// EnqueueBatch), so re-assert the block-representation invariant here too. A
	// representation-required item whose stored representation is blank or
	// non-canonical would be re-processed and fail forever, so refuse the manual
	// DLQ requeue with a clear error instead of silently re-queuing a doomed row.
	if verr := validateQueueItemBlockRepresentation(QueueItem{
		ItemType:              itemType,
		ItemID:                itemID,
		LibraryID:             libraryID,
		BlockRepresentationID: blockRepresentationID,
	}); verr != nil {
		return fmt.Errorf("refusing to requeue failed item org=%s item_type=%s item_id=%s failed_at=%s: %w",
			orgID, itemType, itemID, failedAt.UTC().Format(time.RFC3339Nano), verr)
	}
	requeueAt := failedQueuedAt
	guardMode := effectiveLibraryGuardMode(libraryGuardMode, requiresLibraryDeletedCheck)
	batch := s.db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		INSERT INTO gc_queue (org_id, bucket, queued_at, identity_at, requires_library_deleted_check, library_guard_mode, item_type, item_id, library_id, block_representation_id, storage_class, retry_count)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, orgID.String(), gcQueueBucket(orgID, itemType, itemID), requeueAt, effectiveIdentityAt(failedQueuedAt, identityAt), guardMode != LibraryGuardNone, string(guardMode), string(itemType), itemID, queueLibraryID, strings.TrimSpace(blockRepresentationID), storageClass, 0)
	addPendingItemBatchQuery(batch, orgID, libraryID, itemType, itemID, effectiveIdentityAt(failedQueuedAt, identityAt))
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

func (s *CassandraStore) GetLibraryBlockRepresentationID(orgID, libraryID uuid.UUID) (string, error) {
	var encrypted bool
	var storedRepresentationID string
	err := s.db.Session().Query(`
		SELECT encrypted, block_representation_id FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID.String(), libraryID.String()).Scan(&encrypted, &storedRepresentationID)
	if err == nil {
		return db.CanonicalBlockRepresentationIDForLibrary(libraryID.String(), encrypted, storedRepresentationID)
	}
	if !errors.Is(err, gocql.ErrNotFound) {
		return "", err
	}

	var deletedOrgID string
	var deletedRepresentationID string
	err = s.db.Session().Query(`
		SELECT org_id, block_representation_id FROM deleted_libraries WHERE library_id = ?
	`, libraryID.String()).Scan(&deletedOrgID, &deletedRepresentationID)
	if err != nil {
		return "", err
	}
	if parseUUID(deletedOrgID) != orgID {
		return "", gocql.ErrNotFound
	}
	deletedRepresentationID = strings.TrimSpace(deletedRepresentationID)
	if deletedRepresentationID == "" {
		return "", gocql.ErrNotFound
	}
	if !db.IsCanonicalBlockRepresentationForLibrary(deletedRepresentationID, libraryID) {
		if !db.IsCanonicalBlockRepresentationID(deletedRepresentationID) {
			return "", fmt.Errorf("deleted library %s carries non-canonical block representation %q", libraryID, deletedRepresentationID)
		}
		return "", fmt.Errorf("deleted library %s carries block representation %q for a different library", libraryID, deletedRepresentationID)
	}
	return deletedRepresentationID, nil
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

// --- S3 orphan recovery ---

func (s *CassandraStore) upsertS3OrphanProjection(orgID uuid.UUID, blockID, storageClass, representationID, externalSHA1, recoveryPhase string, firstSeenAt time.Time) error {
	return s.db.Session().Query(`
		INSERT INTO gc_s3_orphans_by_day (first_seen_day, bucket, first_seen_at, org_id, block_id, storage_class, representation_id, external_sha1, recovery_phase)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, db.GCProjectionUTCDate(firstSeenAt), db.GCDiscoveryBucket(orgID.String(), blockID), firstSeenAt.UTC(), orgID.String(), blockID, storageClass, representationID, externalSHA1, recoveryPhase).Exec()
}

// StartBlockDeleteOrphan records the durable recovery row for a NEW block
// delete lifecycle. It always resets the phase to pending_s3, even when a
// stale row from an older delete already exists for the same block_id.
func (s *CassandraStore) StartBlockDeleteOrphan(orgID uuid.UUID, blockID, storageClass, representationID, externalSHA1 string, now time.Time) (time.Time, error) {
	externalSHA1 = strings.TrimSpace(externalSHA1)
	representationID = strings.TrimSpace(representationID)
	existing := map[string]interface{}{}
	applied, err := s.db.Session().Query(`
		INSERT INTO gc_s3_orphans (org_id, block_id, storage_class, representation_id, external_sha1, recovery_phase, first_seen_at, last_attempt_at, retry_count, last_error)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?) IF NOT EXISTS
	`, orgID.String(), blockID, storageClass, representationID, externalSHA1, S3OrphanPhasePendingS3, now, now, 0, "").MapScanCAS(existing)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to record block delete orphan: %w", err)
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
		updateState := map[string]interface{}{}
		updated, err := s.db.Session().Query(`
			UPDATE gc_s3_orphans
			SET storage_class = ?, representation_id = ?, external_sha1 = ?, recovery_phase = ?, last_attempt_at = ?, retry_count = ?, last_error = ?
			WHERE org_id = ? AND block_id = ?
			IF EXISTS
		`, effectiveStorageClass, representationID, externalSHA1, S3OrphanPhasePendingS3, now, 0, "", orgID.String(), blockID).MapScanCAS(updateState)
		if err != nil {
			return effectiveFirstSeenAt, fmt.Errorf("reset S3 orphan recovery state for org=%s block=%s: %w", orgID, blockID, err)
		}
		if !updated {
			return effectiveFirstSeenAt, fmt.Errorf("reset S3 orphan recovery state for org=%s block=%s: row disappeared before update", orgID, blockID)
		}
	}
	if err := s.upsertS3OrphanProjection(orgID, blockID, effectiveStorageClass, representationID, externalSHA1, S3OrphanPhasePendingS3, effectiveFirstSeenAt); err != nil {
		return effectiveFirstSeenAt, fmt.Errorf("ensure gc_s3_orphans_by_day discovery row for org=%s block=%s: %w", orgID, blockID, err)
	}
	return effectiveFirstSeenAt, nil
}

func (s *CassandraStore) MarkS3OrphanMappingCleanupPending(orgID uuid.UUID, blockID, representationID, externalSHA1 string, now time.Time) error {
	externalSHA1 = strings.TrimSpace(externalSHA1)
	representationID = strings.TrimSpace(representationID)
	// IF EXISTS: a plain UPDATE is an upsert in Cassandra, so if a concurrent
	// DeleteS3Orphan (multi-worker recovery race) already removed the row, an
	// unconditional UPDATE would resurrect a partial phantom row (PK + these cols,
	// null first_seen_at) plus a stranded projection. IF EXISTS refuses that;
	// applied=false means another worker finished the recovery — nothing to advance.
	applied, err := s.db.Session().Query(`
		UPDATE gc_s3_orphans
		SET representation_id = ?, external_sha1 = ?, recovery_phase = ?, last_attempt_at = ?, last_error = ?
		WHERE org_id = ? AND block_id = ?
		IF EXISTS
	`, representationID, externalSHA1, S3OrphanPhasePendingMappingCleanup, now, "", orgID.String(), blockID).MapScanCAS(map[string]interface{}{})
	if err != nil {
		return fmt.Errorf("mark S3 orphan mapping cleanup pending org=%s block=%s: %w", orgID, blockID, err)
	}
	if !applied {
		return nil
	}
	var firstSeenAt time.Time
	var storageClass string
	var effectiveRepresentationID string
	err = s.db.Session().Query(`
		SELECT first_seen_at, storage_class, representation_id FROM gc_s3_orphans WHERE org_id = ? AND block_id = ?
	`, orgID.String(), blockID).Scan(&firstSeenAt, &storageClass, &effectiveRepresentationID)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("read S3 orphan after phase advance org=%s block=%s: %w", orgID, blockID, err)
	}
	if err := s.upsertS3OrphanProjection(orgID, blockID, storageClass, effectiveRepresentationID, externalSHA1, S3OrphanPhasePendingMappingCleanup, firstSeenAt); err != nil {
		return fmt.Errorf("update S3 orphan discovery phase org=%s block=%s: %w", orgID, blockID, err)
	}
	return nil
}

// gcS3OrphanTTLSeconds mirrors `default_time_to_live` on gc_s3_orphans and
// gc_s3_orphans_by_day (001_initial_schema.cql). It is duplicated here because
// UpdateS3OrphanAttempt has to write its diagnostic columns with an explicit TTL
// anchored on the row's own first_seen_at rather than on the retry's wall clock
// — see the comment there. TestS3OrphanTTLConstantMatchesSchema fails if the two
// ever drift.
const gcS3OrphanTTLSeconds = 7776000

// UpdateS3OrphanAttempt records a failed recovery attempt on an EXISTING orphan
// row. It never creates one.
//
// Two defects this closes, both of which produced the same shape — a row whose
// primary key is still live, whose identity columns are gone, and which has no
// gc_s3_orphans_by_day entry. Under A+ any orphan row is a writer fence
// (ProbeBlockReuse answers BlockedByGC on mere existence, and both fence reads
// select only block_id, which such a row still returns), so that shape blocks
// every upload of the content while no sweep can enumerate it.
//
//	R19: the statement was a plain UPDATE with no IF, and in Cassandra that is an
//	upsert. A recoverer whose S3 delete failed could write it after another path
//	had already cleared the row, recreating it from the three diagnostic columns
//	alone. The expected first_seen_at makes the statement non-creating and
//	stale-token-safe when the stored token differs. This mutation is non-creating;
//	making StartBlockDeleteOrphan the sole creator is the R21 authority boundary.
//	Reusing a token when resetting an existing lifecycle remains a separate open issue.
//
//	R28: Cassandra applies default_time_to_live per written VALUE and counts it
//	from the WRITE, so an UPDATE that rewrites only the diagnostic columns hands
//	them a fresh full term while storage_class, first_seen_at and recovery_phase
//	keep the term they were inserted with. A retry late in the row's life pushed
//	the diagnostics months past the identity columns, and the projection — never
//	rewritten — expired with the identity. No upsert was needed to produce a
//	partial orphan; ordinary expiry did it. Anchoring the diagnostic TTL on
//	first_seen_at keeps this writer on the same application-derived schedule;
//	coordinator-clock alignment remains a separate open requirement.
//
// Rewriting the identity columns to realign them was the other candidate and is
// deliberately NOT what happens here: representation_id, external_sha1 and
// recovery_phase all have other conditional writers (StartBlockDeleteOrphan's
// reset and the pending_mapping_cleanup transition), so
// echoing back values read a moment earlier would trade a TTL race for a
// lost-update race — including a recovery_phase regression.
//
// Note what this does NOT do: the row still expires, and expiry still destroys
// the durable record that an object needs deleting. Removing the TTL outright is
// the documented package (R28 in docs/GC-X1-CLOSURE-OPTIONS.md) and needs the
// cold-start horizon and cursor semantics redefined with it, since
// gcS3OrphanInitialScanLookbackDays is pinned to this same 90 days.
func (s *CassandraStore) UpdateS3OrphanAttempt(orgID uuid.UUID, blockID string, expectedFirstSeenAt time.Time, errMsg string, now time.Time) error {
	// A missing identity is never a wildcard. The caller must carry the
	// first_seen_at it observed for this lifecycle so a delayed P1 attempt cannot
	// update a newly-created P2 row with the same primary key.
	if expectedFirstSeenAt.IsZero() {
		return nil
	}
	// Cassandra TIMESTAMP values have millisecond precision. Normalize the
	// caller's token before comparing it with the value read from Cassandra;
	// StartBlockDeleteOrphan may have returned a time.Now() value with nanos.
	expectedFirstSeenAt = expectedFirstSeenAt.UTC().Truncate(time.Millisecond)

	// The read supplies retry_count; the identity predicate on the LWT closes
	// the read-to-write race where this lifecycle can be cleared and recreated.
	// retry_count comes along because a counter-like increment needs a
	// read-modify-write; a lost update there is acceptable, since the field is a
	// diagnostic and being off by one changes no decision.
	var prev int
	var storedFirstSeenAt time.Time
	err := s.db.Session().Query(`
		SELECT retry_count, first_seen_at FROM gc_s3_orphans WHERE org_id = ? AND block_id = ?
	`, orgID.String(), blockID).Scan(&prev, &storedFirstSeenAt)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("failed to read prior S3 orphan attempt state: %w", err)
	}
	storedFirstSeenAt = storedFirstSeenAt.UTC().Truncate(time.Millisecond)
	if storedFirstSeenAt.IsZero() || !storedFirstSeenAt.Equal(expectedFirstSeenAt) {
		// A row whose identity columns already expired, one written by an older
		// build, or a newer incarnation for the same key. Refuse to extend or
		// cross lifecycles.
		return nil
	}

	ttl := s3OrphanRemainingTTLSeconds(expectedFirstSeenAt, now)
	if ttl <= 0 {
		// At or past the row's original expiry. Writing a diagnostic now could
		// only outlive the identity columns it annotates, and a partial row with
		// a short life is still a partial row.
		return nil
	}
	applied, err := s.db.Session().Query(`
		UPDATE gc_s3_orphans USING TTL ?
		SET last_attempt_at = ?, retry_count = ?, last_error = ?
		WHERE org_id = ? AND block_id = ?
		IF first_seen_at = ?
	`, ttl, now, prev+1, errMsg, orgID.String(), blockID, expectedFirstSeenAt).MapScanCAS(map[string]interface{}{})
	if err != nil {
		return fmt.Errorf("failed to record S3 orphan attempt: %w", err)
	}
	if !applied {
		// Cleared or replaced between the read and the write. Not an error: the
		// lifecycle this attempt belonged to is over, and crossing it is the defect.
		return nil
	}
	return nil
}

// s3OrphanRemainingTTLSeconds returns the seconds left until the orphan created
// at firstSeenAt reaches its original expiry. It is deliberately derived from
// first_seen_at rather than from "now + full TTL": the whole point is that a
// retry must not outlive the identity columns it describes.
//
// A result of zero or less means the row is at or past that expiry and the
// caller must not write. Clamping to one second instead was the first attempt
// and is wrong: it puts the diagnostic cell one second beyond the identity
// cells, which is a partial row with a very short life rather than no partial
// row at all.
//
// This is an application-clock schedule, not a read of Cassandra's actual
// remaining TTL. Cassandra counts a cell's TTL from its write, so the identity
// columns really expire at insert_time + TTL, while this computes first_seen_at
// + TTL. If first_seen_at is in the future, the chronology is uncertain and the
// diagnostic is skipped rather than risking an extension beyond the identity.
// Exact protection against coordinator-clock skew and read-to-write latency is
// a separate open requirement documented with R28.
func s3OrphanRemainingTTLSeconds(firstSeenAt, now time.Time) int {
	firstSeenAt = firstSeenAt.UTC()
	now = now.UTC()
	if now.Before(firstSeenAt) {
		// Even a subsecond future value is an uncertain chronology. Do not let
		// integer division turn TTL+fraction into a fresh full TTL.
		return 0
	}
	expiresAt := firstSeenAt.Add(gcS3OrphanTTLSeconds * time.Second)
	remaining := int(expiresAt.Sub(now) / time.Second)
	return remaining
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
		SELECT first_seen_at, org_id, block_id, storage_class, representation_id, external_sha1, recovery_phase
		FROM gc_s3_orphans_by_day
		WHERE first_seen_day = ? AND bucket = ?
		LIMIT ?
	`, db.GCProjectionUTCDate(day), bucket, limit).Iter()
	var out []S3OrphanInfo
	var firstSeen time.Time
	var orgIDStr, blockID, storageClass, representationID, externalSHA1, recoveryPhase string
	for iter.Scan(&firstSeen, &orgIDStr, &blockID, &storageClass, &representationID, &externalSHA1, &recoveryPhase) {
		out = append(out, S3OrphanInfo{
			OrgID:            parseUUID(orgIDStr),
			BlockID:          blockID,
			StorageClass:     storageClass,
			RepresentationID: strings.TrimSpace(representationID),
			ExternalSHA1:     strings.TrimSpace(externalSHA1),
			RecoveryPhase:    strings.TrimSpace(recoveryPhase),
			FirstSeenAt:      firstSeen,
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

// BlockHasReferences reports whether any block_references row still exists, at the
// session consistency. Discovery and abort-early only — see the interface contract.
func (s *CassandraStore) BlockHasReferences(orgID uuid.UUID, blockID string) (bool, error) {
	return s.db.BlockHasReferences(orgID.String(), blockID)
}

// BlockHasReferencesGlobal is the EACH_QUORUM liveness read that authorizes physical
// deletion. Errors (including an unreachable DC) propagate so the caller fails closed.
func (s *CassandraStore) BlockHasReferencesGlobal(orgID uuid.UUID, blockID string) (bool, error) {
	return s.db.BlockHasReferencesGlobal(orgID.String(), blockID)
}

// ReleaseStaleBlockClaim hands back a delete claim left behind by an attempt that
// died between claiming and releasing. It reads the claim first so the common case —
// no claim at all — costs one point read and reports "nothing to do" instead of a
// failed conditional update, and so a claim young enough to belong to a concurrent
// in-flight attempt is left strictly alone.
//
// Age is the whole test; the owning claim id is read but never compared against the
// caller's. See the interface contract for why an owner-only release strands blocks
// behind a permanent fence.
//
// A claim with no gc_claimed_at is treated as too fresh rather than as releasable.
// That is the fail-safe direction: the timestamp is written in the same statement as
// the claim, so its absence means an unexpected row shape, and guessing "old enough"
// there would drop a fence on no evidence.
//
// The conditional update pins gc_claimed_at as well as the claim id it observed, so a
// claim that gets released and re-taken between the read and the write is not the one
// this call hands back.
//
// KNOWN RESIDUAL — THIS READ IS AT SESSION CONSISTENCY, AND A FALSE "ABSENT" COSTS THE
// CANDIDATE. Every other read in this file was audited for the X2 asymmetry ("a local
// positive is proof, a local zero authorizes nothing"), and this one does not fit that
// shape: its zero DOES authorize something. BlockClaimAbsent makes processBlock fall
// through to DeleteBlockGCCandidate, consuming the only work item that could ever lift
// the fence — so a read that misses an existing claim strands the block behind
// gc_state='deleting' exactly as consuming the item on an error would.
//
// Two ways it can miss one, neither of which is data loss (nothing here authorizes a
// delete; the cost is a permanent upload refusal on that content):
//
//   - CROSS-DATACENTER. ClaimBlockDelete's LWT commits at the regular consistency of
//     the writing process, so a claim taken by a worker in another DC is acknowledged
//     by a quorum THERE. With RF 1 per DC those replica sets do not intersect, and this
//     LOCAL_QUORUM read can legitimately see no claim. Same geometry as X2 itself,
//     which is why it is worth naming rather than assuming away.
//   - THE PAXOS WINDOW, same DC. A LWT accepted but not yet committed when its proposer
//     died is materialized by a SERIAL read and may be missed by an ordinary one.
//
// WHY IT IS NOT FIXED HERE, rather than fixed badly. Both candidate fixes cost more
// than the residual:
//
//   - EACH_QUORUM on this read closes the cross-DC case, but this is the DISCARD path —
//     it runs for every candidate that turns out to be still referenced — so it would
//     couple ordinary queue drain to every datacenter being reachable. A single DC
//     outage would stop referenced-block candidates settling at all. It also does
//     nothing for the Paxos window.
//   - A SERIAL read is the linearizable read for LWT-written state, but SERIAL takes a
//     GLOBAL quorum (2 of 3 at RF 1 in three DCs), which need not intersect a claim
//     committed under LOCAL_SERIAL in one DC — and mixing the two levels on the blocks
//     partition is precisely the one-serial-domain violation R12 tracks.
//
// So the clean fix depends on the serial-domain decision X1 has to make anyway, and is
// recorded with it (ISSUE-GC-STALE-CLAIM-READ-CONSISTENCY-01) rather than half-made
// here. Until then: the exposure is a stale claim taken by a GC worker in a DIFFERENT
// datacenter and then abandoned, and destructive GC runs nowhere.
func (s *CassandraStore) ReleaseStaleBlockClaim(orgID uuid.UUID, blockID string, staleBefore time.Time) (BlockClaimReleaseOutcome, error) {
	var gcState, gcClaimID string
	var gcClaimedAt time.Time
	err := s.db.Session().Query(`
		SELECT gc_state, gc_claim_id, gc_claimed_at FROM blocks WHERE org_id = ? AND block_id = ?
	`, orgID.String(), blockID).Scan(&gcState, &gcClaimID, &gcClaimedAt)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return BlockClaimAbsent, nil
		}
		return BlockClaimAbsent, err
	}
	if gcState != db.BlockGCStateDeleting {
		return BlockClaimAbsent, nil
	}
	if gcClaimedAt.IsZero() || gcClaimedAt.After(staleBefore) {
		return BlockClaimTooFresh, nil
	}

	applied, err := s.db.Session().Query(`
		UPDATE blocks SET gc_state = null, gc_claim_id = null, gc_claimed_at = null
		WHERE org_id = ? AND block_id = ?
		IF gc_state = ? AND gc_claim_id = ? AND gc_claimed_at = ?
	`, orgID.String(), blockID, db.BlockGCStateDeleting, gcClaimID, gcClaimedAt).MapScanCAS(map[string]interface{}{})
	if err != nil {
		return BlockClaimAbsent, err
	}
	if !applied {
		// The row changed between the read and the conditional write — someone
		// re-claimed it. Treat that as a live claim rather than as nothing to do.
		return BlockClaimTooFresh, nil
	}
	return BlockClaimReleased, nil
}

// ValidateDestructiveGCTopology gates every physical delete on the live keyspace
// replication still supporting EACH_QUORUM's per-datacenter semantics.
func (s *CassandraStore) ValidateDestructiveGCTopology() error {
	return s.db.ValidateDestructiveGCTopology()
}

func (s *CassandraStore) BlockReferenceExists(orgID uuid.UUID, blockID, referrer string) (bool, error) {
	return s.db.BlockReferenceExists(orgID.String(), blockID, referrer)
}

func (s *CassandraStore) GetBlockInfo(orgID uuid.UUID, blockID string) (BlockInfo, error) {
	info := BlockInfo{BlockID: blockID}
	var createdAt *time.Time
	var representationID string
	var sha1 string
	// Single-partition point read by the full ((org_id), block_id) key. sha1 is
	// the same row, so reading it adds no extra query and no tombstone scan.
	err := s.db.Session().Query(`
		SELECT storage_class, created_at, representation_id, sha1 FROM blocks WHERE org_id = ? AND block_id = ?
	`, orgID.String(), blockID).Scan(&info.StorageClass, &createdAt, &representationID, &sha1)
	if err != nil {
		return BlockInfo{}, err
	}
	info.CreatedAt = createdAt
	info.RepresentationID = strings.TrimSpace(representationID)
	info.Sha1 = strings.TrimSpace(sha1)
	return info, nil
}

// RemoveBlockReference deletes one (block, referrer) reference row (idempotent).
func (s *CassandraStore) RemoveBlockReference(orgID uuid.UUID, blockID, referrer string) error {
	return s.db.RemoveBlockReference(orgID.String(), blockID, referrer)
}

// mappingResolveConcurrency bounds the number of in-flight single-row lookups
// against block_id_mappings so a large fs_object's block list cannot flood the
// driver. block_id_mappings is partitioned by
// ((org_id, representation_id, external_id)), so each lookup is a single-
// partition point read.
const mappingResolveConcurrency = 32

func (s *CassandraStore) lookupBlockMapping(orgID uuid.UUID, representationID, externalID string) (string, error) {
	var internalID string
	err := s.db.Session().Query(`
		SELECT internal_id FROM block_id_mappings
		WHERE org_id = ? AND representation_id = ? AND external_id = ?
	`, orgID.String(), representationID, externalID).Scan(&internalID)
	return internalID, err
}

func (s *CassandraStore) lookupBlockMappingWithFallback(orgID, libraryID uuid.UUID, representationID, externalID string) (string, error) {
	if strings.TrimSpace(representationID) != "" {
		return s.lookupBlockMapping(orgID, representationID, externalID)
	}

	plainInternalID, plainErr := s.lookupBlockMapping(orgID, db.PlainBlockRepresentationID, externalID)
	if plainErr != nil && !errors.Is(plainErr, gocql.ErrNotFound) {
		return "", plainErr
	}

	encryptedInternalID, encryptedErr := s.lookupBlockMapping(orgID, db.EncryptedLibraryBlockRepresentationID(libraryID.String()), externalID)
	if encryptedErr != nil && !errors.Is(encryptedErr, gocql.ErrNotFound) {
		return "", encryptedErr
	}

	plainCanonical := db.NormalizeBlockID(plainInternalID)
	plainValid := plainErr == nil && db.IsSHA256BlockID(plainCanonical)
	encryptedCanonical := db.NormalizeBlockID(encryptedInternalID)
	encryptedValid := encryptedErr == nil && db.IsSHA256BlockID(encryptedCanonical)

	switch {
	case plainValid && encryptedValid && plainCanonical == encryptedCanonical:
		return plainCanonical, nil
	case plainValid && encryptedValid:
		metrics.GCAuditEventsTotal.WithLabelValues("gc_block_mapping_unresolved_ambiguous_representation").Inc()
		return "", nil
	case plainValid:
		return plainCanonical, nil
	case encryptedValid:
		return encryptedCanonical, nil
	case plainErr == nil:
		return plainInternalID, nil
	case encryptedErr == nil:
		return encryptedInternalID, nil
	case errors.Is(plainErr, gocql.ErrNotFound) && errors.Is(encryptedErr, gocql.ErrNotFound):
		return "", gocql.ErrNotFound
	default:
		return "", nil
	}
}

func (s *CassandraStore) ResolveBlockIDs(orgID, libraryID uuid.UUID, blockRepresentationID string, blockIDs []string) ([]string, error) {
	// Fast path: an all-SHA-256 block list never consults block_id_mappings, so
	// skip representation resolution entirely. For SHA-1 lists we prefer the
	// representation persisted on the queue item; when absent we resolve from the
	// canonical/deleted library rows and only fall back to a safe dual-probe path
	// for legacy queue rows created before representation persistence existed.
	representationID := strings.TrimSpace(blockRepresentationID)
	if representationID == "" {
		for _, id := range blockIDs {
			if db.IsSHA1BlockID(db.NormalizeBlockID(id)) {
				resolvedRepresentationID, err := s.GetLibraryBlockRepresentationID(orgID, libraryID)
				if err == nil {
					representationID = resolvedRepresentationID
				} else if !errors.Is(err, gocql.ErrNotFound) {
					return nil, err
				}
				break
			}
		}
	}
	return resolveBlockIDsConcurrent(orgID, blockIDs, mappingResolveConcurrency, func(idx int) (string, error) {
		return s.lookupBlockMappingWithFallback(orgID, libraryID, representationID, db.NormalizeBlockID(blockIDs[idx]))
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

	// Canonicalize (trim + lowercase) before classifying by hex content, mirroring
	// the streaming resolver and the Cassandra mapping query, so a padded/uppercase
	// id is still recognized as a SHA-1 and a passthrough SHA-256 lands canonicalized.
	//
	// Unlike streaming this resolver is deliberately LENIENT: a non-hex or
	// wrong-length id is not a fatal error and a missing/garbage mapping keeps the
	// original id. GC mapping cleanup legitimately runs after the forward mapping
	// was already deleted, so failing closed here would wedge fs_object GC on a
	// row that can never resolve. Worst case is a skipped reference removal (a
	// leak + a harmless GC candidate), never a live-data delete. Each lenient skip
	// is counted (gc_block_id_invalid / gc_block_mapping_unresolved_*) so silent
	// leaks stay visible for drift/corruption alerting instead of vanishing.
	var toResolve []int
	for i, blockID := range blockIDs {
		normalized := db.NormalizeBlockID(blockID)
		resolved[i] = normalized
		switch {
		case db.IsSHA1BlockID(normalized):
			toResolve = append(toResolve, i)
		case db.IsSHA256BlockID(normalized):
			// already-internal id, nothing to resolve
		default:
			metrics.GCAuditEventsTotal.WithLabelValues("gc_block_id_invalid").Inc()
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
			if errors.Is(result.err, gocql.ErrNotFound) {
				metrics.GCAuditEventsTotal.WithLabelValues("gc_block_mapping_unresolved_not_found").Inc()
			} else {
				resolveErr = errors.Join(resolveErr, fmt.Errorf("resolve block mapping org=%s external=%s: %w", orgID, blockIDs[result.idx], result.err))
			}
			continue
		}
		// Only accept a hex 64-char SHA-256; a garbage internal_id is left as the
		// original SHA-1 (lenient, see above) instead of poisoning the reference key.
		internalID := db.NormalizeBlockID(result.internalID)
		switch {
		case internalID == "":
			metrics.GCAuditEventsTotal.WithLabelValues("gc_block_mapping_unresolved_empty_internal").Inc()
		case !db.IsSHA256BlockID(internalID):
			metrics.GCAuditEventsTotal.WithLabelValues("gc_block_mapping_unresolved_invalid_internal").Inc()
		default:
			resolved[result.idx] = internalID
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

func (s *CassandraStore) DeleteClaimedBlockStub(orgID uuid.UUID, blockID, claimID string) (bool, error) {
	return s.db.DeleteClaimedBlockStub(orgID.String(), blockID, claimID)
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

func (s *CassandraStore) DeleteBlockMappingExact(orgID uuid.UUID, representationID, externalID string) error {
	externalID = db.NormalizeBlockID(externalID)
	if err := s.db.Session().Query(`
		DELETE FROM block_id_mappings WHERE org_id = ? AND representation_id = ? AND external_id = ?
	`, orgID.String(), representationID, externalID).Exec(); err != nil {
		return fmt.Errorf("delete block mapping org=%s representation_id=%s external_id=%s: %w", orgID, representationID, externalID, err)
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
		if errors.Is(err, gocql.ErrNotFound) {
			return false, nil // genuinely absent
		}
		// Fail closed: a transient read error must NOT be reported as "missing".
		// Callers (scanner Phases 3/4/9, the worker delete guard) treat "missing"
		// as license to enqueue/delete a library's content, so swallowing the error
		// here could destroy a live library on a Cassandra blip.
		return false, fmt.Errorf("check library existence for %s: %w", libraryID, err)
	}
	return true, nil
}

// CanonicalLibraryExists reads the authoritative `libraries` table by (org_id,
// library_id). A present row (even soft-deleted) means the library is live or
// recoverable, so its content must not be orphan-deleted. Fails closed on read
// errors so a Cassandra blip never masquerades as "library gone".
func (s *CassandraStore) CanonicalLibraryExists(orgID, libraryID uuid.UUID) (bool, error) {
	var existingLibIDStr string
	err := s.db.Session().Query(`
		SELECT library_id FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID.String(), libraryID.String()).Scan(&existingLibIDStr)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return false, nil // genuinely absent
		}
		return false, fmt.Errorf("check canonical library existence for %s/%s: %w", orgID, libraryID, err)
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
	expectedPlatformByShard := make(map[int]traffic.StorageSnapshot, traffic.CounterShardCount)
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
			shard := traffic.CounterShard(libOrgIDStr)
			snap := expectedPlatformByShard[shard]
			snap.BytesUsed += libSnapshot.BytesUsed
			snap.FileCount += libSnapshot.FileCount
			expectedPlatformByShard[shard] = snap
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
		var err error
		if request.Scope == traffic.PlatformStorageScope() {
			err = traffic.ReconcileStorageScopeSharded(s.db, request.Scope, expectedPlatformByShard)
		} else {
			err = traffic.ReconcileStorageScope(s.db, request.Scope, expected[request.Scope])
		}
		if err != nil {
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
			OrgID:                   row.OrgID,
			LibraryID:               row.LibraryID,
			HeadCommitID:            row.HeadCommitID,
			BlockRepresentationID:   row.BlockRepresentationID,
			RepresentationDefaulted: row.RepresentationDefaulted,
			RepresentationInvalid:   row.RepresentationInvalid,
			AutoDeleteDays:          row.VersionTTLDays,
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
			var encrypted bool
			var storedRepresentationID string
			var deletedAt *time.Time
			err := s.db.Session().Query(`
				SELECT head_commit_id, version_ttl_days, auto_delete_days, encrypted, block_representation_id, deleted_at FROM libraries WHERE org_id = ? AND library_id = ?
			`, orgIDStr, libraryIDStr).Scan(&headCommitID, &versionTTLDays, &autoDeleteDays, &encrypted, &storedRepresentationID, &deletedAt)
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

			// Validate the stored representation against the library's own
			// identity+encrypted flag (not just canonical shape) so a cross-domain
			// value — e.g. an encrypted library stamped plain:v1 — is surfaced as
			// drift and skipped by the scanner rather than enqueued under the wrong
			// SHA-1 mapping domain. Mirrors the delete/GetLibraryBlockRepresentationID
			// paths so every live resolver fails closed on the same rule.
			resolvedRepresentationID, repErr := db.CanonicalBlockRepresentationIDForLibrary(libraryIDStr, encrypted, storedRepresentationID)
			if repErr != nil {
				results = append(results, LibraryTTLInfo{
					OrgID:                 parseUUID(orgIDStr),
					LibraryID:             parseUUID(libraryIDStr),
					HeadCommitID:          headCommitID,
					BlockRepresentationID: strings.TrimSpace(storedRepresentationID),
					RepresentationInvalid: true,
					VersionTTLDays:        days,
				})
				continue
			}
			results = append(results, LibraryTTLInfo{
				OrgID:                   parseUUID(orgIDStr),
				LibraryID:               parseUUID(libraryIDStr),
				HeadCommitID:            headCommitID,
				BlockRepresentationID:   resolvedRepresentationID,
				RepresentationDefaulted: strings.TrimSpace(storedRepresentationID) == "",
				VersionTTLDays:          days,
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
	// Tear down the canonical row and the file_tags_by_tag reverse-lookup
	// projection together so library-cascade cleanup leaves no orphan rows.
	batch := s.db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		DELETE FROM file_tags WHERE repo_id = ? AND file_path = ? AND tag_id = ?
	`, libraryID.String(), filePath, tagID)
	batch.Query(`
		DELETE FROM file_tags_by_tag WHERE repo_id = ? AND tag_id = ? AND file_path = ?
	`, libraryID.String(), tagID, filePath)
	return batch.Exec()
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
	var userID, path string
	batch := s.db.Session().Batch(gocql.UnloggedBatch)
	pendingDeletes := 0
	flushDeletes := func() error {
		if pendingDeletes == 0 {
			return nil
		}
		if err := batch.Exec(); err != nil {
			return err
		}
		batch = s.db.Session().Batch(gocql.UnloggedBatch)
		pendingDeletes = 0
		return nil
	}
	for iter.Scan(&userID, &path) {
		batch.Query(`DELETE FROM starred_files WHERE user_id = ? AND repo_id = ? AND path = ?`,
			userID, libraryIDStr, path)
		pendingDeletes++
		if pendingDeletes >= maxBatchSize {
			if err := flushDeletes(); err != nil {
				_ = iter.Close()
				return fmt.Errorf("delete starred_files canonicals for library %s: %w", libraryID, err)
			}
		}
	}
	if err := iter.Close(); err != nil {
		return fmt.Errorf("scan starred_files_by_repo for library %s: %w", libraryID, err)
	}
	if err := flushDeletes(); err != nil {
		return fmt.Errorf("delete starred_files canonicals for library %s: %w", libraryID, err)
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

// ScanAllGroupShares streams all group-share projection rows to the scanner.
// Scan the projection directly: enumerating groups first cannot discover a
// shares_by_group partition after its group row has already been deleted.
func (s *CassandraStore) ScanAllGroupShares(ctx context.Context, visit func(GroupShareInfo) error) error {
	iter := s.db.Session().Query(`
		SELECT org_id, group_id, library_id, share_id FROM shares_by_group
	`).WithContext(ctx).PageSize(256).Iter()
	var orgIDStr, groupIDStr, libIDStr, shareIDStr string
	for iter.Scan(&orgIDStr, &groupIDStr, &libIDStr, &shareIDStr) {
		if err := visit(GroupShareInfo{
			LibraryID:    parseUUID(libIDStr),
			ShareID:      parseUUID(shareIDStr),
			SharedTo:     parseUUID(groupIDStr),
			SharedToType: "group",
			OrgID:        parseUUID(orgIDStr),
		}); err != nil {
			// Preserve a concurrent Cassandra/iteration failure alongside the
			// visitor error instead of discarding it.
			return errors.Join(err, iter.Close())
		}
	}
	if err := iter.Close(); err != nil {
		return fmt.Errorf("failed to scan group shares: %w", err)
	}
	return nil
}

func (s *CassandraStore) GroupExists(orgID, groupID uuid.UUID) (bool, error) {
	var name string
	err := s.db.Session().Query(`
		SELECT name FROM groups WHERE org_id = ? AND group_id = ?
	`, orgID.String(), groupID.String()).Scan(&name)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return false, nil // genuinely absent
		}
		// Fail closed: Phase 9 deletes a group share when the group is reported
		// absent, so a transient read error must surface as an error rather than
		// masquerading as "group deleted" and dropping a valid share.
		return false, fmt.Errorf("check group existence for %s/%s: %w", orgID, groupID, err)
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
	var (
		encrypted              bool
		storedRepresentationID string
	)
	// A missing canonical libraries row means there is nothing left to soft-delete.
	// Return nil (idempotent success) rather than an error: SoftDeleteLibrary runs
	// inside user/org GC cascades that enumerate libraries and then delete each one,
	// so a row that vanished between listing and here is already-done work, not a
	// failure. Surfacing ErrNotFound would fail the whole cascade and eventually
	// push it to the DLQ. This also matches MockStore, which no-ops on a missing
	// library. A genuine read error (non-NotFound) still propagates.
	if errors.Is(err, gocql.ErrNotFound) {
		if baseErr := s.db.Session().Query(
			`SELECT owner_id, storage_class, encrypted, block_representation_id FROM libraries WHERE org_id = ? AND library_id = ?`,
			orgID.String(), libraryID.String(),
		).Scan(&ownerID, &storageClass, &encrypted, &storedRepresentationID); baseErr != nil {
			if errors.Is(baseErr, gocql.ErrNotFound) {
				return nil
			}
			return baseErr
		}
	} else if baseErr := s.db.Session().Query(
		`SELECT encrypted, block_representation_id FROM libraries WHERE org_id = ? AND library_id = ?`,
		orgID.String(), libraryID.String(),
	).Scan(&encrypted, &storedRepresentationID); baseErr != nil {
		if errors.Is(baseErr, gocql.ErrNotFound) {
			return nil
		}
		return baseErr
	}
	blockRepresentationID, repErr := db.CanonicalBlockRepresentationIDForLibrary(libraryID.String(), encrypted, storedRepresentationID)
	if repErr != nil {
		metrics.LibraryDeleteRepresentationResolutionFailures.WithLabelValues("gc_soft_delete").Inc()
		return fmt.Errorf("resolve block representation for soft delete %s/%s: %w", orgID, libraryID, repErr)
	}

	now := time.Now().UTC()
	batch := s.db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		UPDATE libraries SET deleted_at = ?, deleted_by = ?, updated_at = ? WHERE org_id = ? AND library_id = ?
	`, now, deletedBy.String(), now, orgID.String(), libraryID.String())
	batch.Query(`
		INSERT INTO deleted_libraries (library_id, org_id, deleted_at, storage_class, block_representation_id) VALUES (?, ?, ?, ?, ?)
	`, libraryID.String(), orgID.String(), now, storageClass, blockRepresentationID)
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
	var repoID, path string
	batch := s.db.Session().Batch(gocql.UnloggedBatch)
	pendingDeletes := 0
	flushDeletes := func() error {
		if pendingDeletes == 0 {
			return nil
		}
		if err := batch.Exec(); err != nil {
			return err
		}
		batch = s.db.Session().Batch(gocql.UnloggedBatch)
		pendingDeletes = 0
		return nil
	}
	for iter.Scan(&repoID, &path) {
		batch.Query(`DELETE FROM starred_files_by_repo WHERE repo_id = ? AND user_id = ? AND path = ?`,
			repoID, userIDStr, path)
		pendingDeletes++
		if pendingDeletes >= maxBatchSize {
			if err := flushDeletes(); err != nil {
				_ = iter.Close()
				return fmt.Errorf("delete starred_files_by_repo rows for user %s: %w", userID, err)
			}
		}
	}
	if err := iter.Close(); err != nil {
		return fmt.Errorf("scan starred_files for user %s: %w", userID, err)
	}
	if err := flushDeletes(); err != nil {
		return fmt.Errorf("delete starred_files_by_repo rows for user %s: %w", userID, err)
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
	return acquireHardDeleteLock(s.db.Session(), "gc_user_hard_delete_locks", "user_id", userID, leaseToken)
}

func (s *CassandraStore) RenewUserHardDeleteLock(userID, leaseToken uuid.UUID) (bool, error) {
	return renewHardDeleteLock(s.db.Session(), "gc_user_hard_delete_locks", "user_id", userID, leaseToken)
}

func (s *CassandraStore) ReleaseUserHardDeleteLock(userID, leaseToken uuid.UUID) error {
	return releaseHardDeleteLock(s.db.Session(), "gc_user_hard_delete_locks", "user_id", userID, leaseToken)
}

func (s *CassandraStore) AcquireLibraryHardDeleteLock(libraryID, leaseToken uuid.UUID) (bool, error) {
	return AcquireLibraryHardDeleteLockLease(s.db.Session(), libraryID, leaseToken)
}

func (s *CassandraStore) RenewLibraryHardDeleteLock(libraryID, leaseToken uuid.UUID) (bool, error) {
	return RenewLibraryHardDeleteLockLease(s.db.Session(), libraryID, leaseToken)
}

func (s *CassandraStore) ReleaseLibraryHardDeleteLock(libraryID, leaseToken uuid.UUID) error {
	return ReleaseLibraryHardDeleteLockLease(s.db.Session(), libraryID, leaseToken)
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
		SELECT library_id, org_id, deleted_at, storage_class, block_representation_id, purge_requested_at FROM deleted_libraries
	`).Iter()

	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	var libIDStr, orgIDStr, storageClass, blockRepresentationID string
	var deletedAt, purgeRequestedAt time.Time
	var result []DeletedLibraryInfo
	for iter.Scan(&libIDStr, &orgIDStr, &deletedAt, &storageClass, &blockRepresentationID, &purgeRequestedAt) {
		// A permanent-delete stamps purge_requested_at so the library becomes eligible on
		// this scan instead of after TrashRetentionDays (it is still grace-gated before the
		// worker processes it); a normal soft-delete leaves it null and waits out the
		// retention window (deleted_at < cutoff). See migration 012 / P1b.
		if !purgeRequestedAt.IsZero() || deletedAt.Before(cutoff) {
			orgID := parseUUID(orgIDStr)
			libID := parseUUID(libIDStr)
			if storageClass == "" {
				storageClass, _ = s.GetLibraryStorageClass(orgID, libID)
			}
			result = append(result, DeletedLibraryInfo{
				OrgID:                 orgID,
				LibraryID:             libID,
				BlockRepresentationID: strings.TrimSpace(blockRepresentationID),
				StorageClass:          storageClass,
				DeletedAt:             deletedAt,
				PurgeRequestedAt:      purgeRequestedAt,
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
	return acquireHardDeleteLock(s.db.Session(), "gc_org_hard_delete_locks", "org_id", orgID, leaseToken)
}

func (s *CassandraStore) RenewOrgHardDeleteLock(orgID, leaseToken uuid.UUID) (bool, error) {
	return renewHardDeleteLock(s.db.Session(), "gc_org_hard_delete_locks", "org_id", orgID, leaseToken)
}

func (s *CassandraStore) ReleaseOrgHardDeleteLock(orgID, leaseToken uuid.UUID) error {
	return releaseHardDeleteLock(s.db.Session(), "gc_org_hard_delete_locks", "org_id", orgID, leaseToken)
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

func (a *StorageManagerAdapter) GetBlockStoreForOrg(orgID, storageClass string) (BlockStoreDeleter, error) {
	bs, err := a.manager.GetBlockStoreForOrg(orgID, storageClass)
	if err != nil {
		return nil, err
	}
	return bs, nil
}
