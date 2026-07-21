package db

import (
	"context"
	"errors"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

// BlockStorageLocation is the canonical physical location recorded for a block.
type BlockStorageLocation struct {
	SizeBytes    int64
	StorageClass string
	StorageKey   string
}

// GetBlockStorageLocation reads one block's canonical physical location. The
// found result distinguishes a genuinely absent legacy row from a Cassandra
// failure, which is returned as an error.
func (db *DB) GetBlockStorageLocation(ctx context.Context, orgID, blockID string) (BlockStorageLocation, bool, error) {
	return scanBlockStorageLocation(func(dest ...interface{}) error {
		return db.Session().Query(`
			SELECT size_bytes, storage_class, storage_key
			FROM blocks
			WHERE org_id = ? AND block_id = ?
		`, orgID, blockID).WithContext(ctx).Scan(dest...)
	})
}

func scanBlockStorageLocation(scan func(dest ...interface{}) error) (BlockStorageLocation, bool, error) {
	var location BlockStorageLocation
	err := scan(&location.SizeBytes, &location.StorageClass, &location.StorageKey)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return BlockStorageLocation{}, false, nil
		}
		return BlockStorageLocation{}, false, err
	}
	return location, true, nil
}
