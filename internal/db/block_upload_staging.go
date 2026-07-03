package db

import (
	"fmt"
	"hash/fnv"
	"time"
)

// BlockUploadStagedBlockBuckets is the number of ledger buckets a session's
// staged blocks are spread across. Bucketing keeps the per-block admission read
// O(1) rows (one small bucket) instead of an O(N^2) partition re-scan for large
// files: for a 12 GB file (~1536 8 MB blocks) each of 64 buckets holds ~24 rows.
// It is a fixed protocol constant (changing it re-buckets existing sessions), so
// it is not configurable. See docs/WEB-BLOCK-UPLOAD.md item 1.
const BlockUploadStagedBlockBuckets = 64

// StagedBlockBucket maps a block id to its ledger bucket. Deterministic, so the
// same block always lands in the same (session_id, bucket) partition and the
// reserve is idempotent under retries.
func StagedBlockBucket(blockID string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(blockID))
	return int(h.Sum32() % uint32(BlockUploadStagedBlockBuckets))
}

// CountSessionStagedBlocksInBucket returns how many distinct blocks the session
// has already staged in the given bucket, reading at most `limit` rows (the
// caller passes bucketCap+1 so it only needs to know whether the cap is reached).
func (db *DB) CountSessionStagedBlocksInBucket(sessionID string, bucket, limit int) (int, error) {
	iter := db.Session().Query(`
		SELECT block_id FROM block_upload_session_staged_blocks
		WHERE session_id = ? AND bucket = ? LIMIT ?
	`, sessionID, bucket, limit).Iter()
	count := 0
	var blockID string
	for iter.Scan(&blockID) {
		count++
	}
	if err := iter.Close(); err != nil {
		return 0, fmt.Errorf("count staged blocks for session=%s bucket=%d: %w", sessionID, bucket, err)
	}
	return count, nil
}

// ReserveSessionStagedBlock records a new staged block in the session's ledger
// BEFORE the block is stored (reserve-before-PUT, fail-closed). The insert is
// idempotent by ((session_id, bucket), block_id) — a retried block rewrites the
// same row and is never double-counted — and carries the session TTL so an
// abandoned session's ledger self-expires (no Cassandra COUNTER, no drift).
func (db *DB) ReserveSessionStagedBlock(sessionID string, bucket int, blockID string, sizeBytes int64) error {
	if err := db.Session().Query(`
		INSERT INTO block_upload_session_staged_blocks (session_id, bucket, block_id, size_bytes, created_at)
		VALUES (?, ?, ?, ?, ?) USING TTL ?
	`, sessionID, bucket, blockID, sizeBytes, time.Now().UTC(), BlockUploadSessionTTLSeconds).Exec(); err != nil {
		return fmt.Errorf("reserve staged block session=%s block=%s: %w", sessionID, blockID, err)
	}
	return nil
}

// CleanupCommittedBlockUploadSessionCaps releases the session's per-user slot so
// the user's concurrent-session budget recovers immediately at commit. It is
// BEST-EFFORT and MUST be called only after the critical idempotency write
// (MarkBlockUploadSessionCommitted) has succeeded — never inside it — so a
// cleanup failure can never make a committed file look uncommitted. A failed
// release only lets the slot linger until its TTL (≤48h), which fails safe
// (toward rejecting new sessions). The staged-block ledger rows are intentionally
// left to self-expire via TTL (keyed by the now-dead session id, never re-read),
// avoiding a burst of partition tombstones per commit.
func (db *DB) CleanupCommittedBlockUploadSessionCaps(s BlockUploadSession) error {
	if s.Slot < 0 {
		return nil // cap was disabled at creation; nothing to release
	}
	if err := db.releaseBlockUploadSessionSlot(s.OrgID, s.UserID, s.Slot); err != nil {
		return fmt.Errorf("release block upload session slot org=%s user=%s slot=%d: %w", s.OrgID, s.UserID, s.Slot, err)
	}
	return nil
}
