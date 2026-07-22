package db

import (
	"context"
	"errors"
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

// BlockStorageLocation is the canonical physical location recorded for a block.
type BlockStorageLocation struct {
	SizeBytes    int64
	StorageClass string
	StorageKey   string
	GCState      string
	CreatedAt    *time.Time
}

// GetBlockStorageLocation reads one block's canonical physical location.
func (db *DB) GetBlockStorageLocation(ctx context.Context, orgID, blockID string) (BlockStorageLocation, bool, error) {
	return scanBlockStorageLocation(func(dest ...interface{}) error {
		return db.Session().Query(`
			SELECT size_bytes, storage_class, storage_key, gc_state, created_at
			FROM blocks
			WHERE org_id = ? AND block_id = ?
		`, orgID, blockID).WithContext(ctx).Scan(dest...)
	})
}

func scanBlockStorageLocation(scan func(dest ...interface{}) error) (BlockStorageLocation, bool, error) {
	var location BlockStorageLocation
	err := scan(&location.SizeBytes, &location.StorageClass, &location.StorageKey, &location.GCState, &location.CreatedAt)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return BlockStorageLocation{}, false, nil
		}
		return BlockStorageLocation{}, false, err
	}
	return location, true, nil
}
