package db

import (
	"errors"
	"fmt"
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

type PendingPublishedFSObjectOwner struct {
	RepoID    string
	FSID      string
	OwnerID   string
	CreatedAt time.Time
	OrgID     string
	AttemptID string
	BlockIDs  []string
}

const PendingPublishedFSObjectOwnerTTLSeconds = 35 * 24 * 60 * 60 // 35d

func AddUpsertPendingPublishedFSObjectOwnerQueries(batch *gocql.Batch, repoID, fsID, ownerID string, createdAt time.Time, orgID, attemptID string, blockIDs []string) {
	if batch == nil || repoID == "" || fsID == "" || ownerID == "" || createdAt.IsZero() {
		return
	}
	createdAt = createdAt.UTC()
	// Keep block_ids out of this logged batch. Large uploads can have hundreds
	// of staged IDs and exceed Cassandra's batch fail threshold.
	if orgID == "" && attemptID == "" {
		batch.Query(`
			INSERT INTO pending_published_fs_objects (repo_id, fs_id, owner_id, created_at)
			VALUES (?, ?, ?, ?) USING TTL ?
		`, repoID, fsID, ownerID, createdAt, PendingPublishedFSObjectOwnerTTLSeconds)
	} else {
		batch.Query(`
			INSERT INTO pending_published_fs_objects (repo_id, fs_id, owner_id, created_at, org_id, attempt_id)
			VALUES (?, ?, ?, ?, ?, ?) USING TTL ?
		`, repoID, fsID, ownerID, createdAt, orgID, attemptID, PendingPublishedFSObjectOwnerTTLSeconds)
	}
	batch.Query(`
		INSERT INTO pending_published_fs_objects_by_day (created_day, bucket, created_at, repo_id, fs_id, owner_id)
		VALUES (?, ?, ?, ?, ?, ?) USING TTL ?
	`, GCProjectionUTCDate(createdAt), GCDiscoveryBucket(repoID, fsID, ownerID), createdAt, repoID, fsID, ownerID, PendingPublishedFSObjectOwnerTTLSeconds)
}

func AddDeletePendingPublishedFSObjectOwnerQueries(batch *gocql.Batch, repoID, fsID, ownerID string, createdAt time.Time) {
	if batch == nil || repoID == "" || fsID == "" || ownerID == "" || createdAt.IsZero() {
		return
	}
	createdAt = createdAt.UTC()
	batch.Query(`
		DELETE FROM pending_published_fs_objects
		WHERE repo_id = ? AND fs_id = ? AND owner_id = ?
	`, repoID, fsID, ownerID)
	batch.Query(`
		DELETE FROM pending_published_fs_objects_by_day
		WHERE created_day = ? AND bucket = ? AND created_at = ? AND repo_id = ? AND fs_id = ? AND owner_id = ?
	`, GCProjectionUTCDate(createdAt), GCDiscoveryBucket(repoID, fsID, ownerID), createdAt, repoID, fsID, ownerID)
}

func (db *DB) UpsertPendingPublishedFSObjectOwner(repoID, fsID, ownerID string, createdAt time.Time, orgID, attemptID string, blockIDs []string) error {
	if db == nil || repoID == "" || fsID == "" || ownerID == "" {
		return nil
	}
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	createdAt = createdAt.UTC()
	blockIDs = NormalizeBlockIDs(blockIDs)
	batch := db.Session().Batch(gocql.LoggedBatch)
	AddUpsertPendingPublishedFSObjectOwnerQueries(batch, repoID, fsID, ownerID, createdAt, orgID, attemptID, blockIDs)
	if err := batch.Exec(); err != nil {
		return fmt.Errorf("upsert pending published fs_object owner repo=%s fs=%s owner=%s: %w", repoID, fsID, ownerID, err)
	}
	if len(blockIDs) == 0 {
		return nil
	}
	if err := db.Session().Query(`
		UPDATE pending_published_fs_objects USING TTL ?
		SET block_ids = ?
		WHERE repo_id = ? AND fs_id = ? AND owner_id = ?
	`, PendingPublishedFSObjectOwnerTTLSeconds, blockIDs, repoID, fsID, ownerID).Exec(); err != nil {
		rollbackErr := db.DeletePendingPublishedFSObjectOwner(repoID, fsID, ownerID, createdAt)
		if rollbackErr != nil {
			return errors.Join(
				fmt.Errorf("upsert pending published fs_object owner repo=%s fs=%s owner=%s block ids: %w", repoID, fsID, ownerID, err),
				fmt.Errorf("rollback pending published fs_object owner repo=%s fs=%s owner=%s: %w", repoID, fsID, ownerID, rollbackErr),
			)
		}
		return fmt.Errorf("upsert pending published fs_object owner repo=%s fs=%s owner=%s block ids: %w", repoID, fsID, ownerID, err)
	}
	return nil
}

func (db *DB) DeletePendingPublishedFSObjectOwner(repoID, fsID, ownerID string, createdAt time.Time) error {
	if db == nil || repoID == "" || fsID == "" || ownerID == "" {
		return nil
	}
	if createdAt.IsZero() {
		err := db.Session().Query(`
			SELECT created_at FROM pending_published_fs_objects
			WHERE repo_id = ? AND fs_id = ? AND owner_id = ?
		`, repoID, fsID, ownerID).Scan(&createdAt)
		if err != nil {
			if errors.Is(err, gocql.ErrNotFound) {
				return nil
			}
			return fmt.Errorf("read pending published fs_object owner repo=%s fs=%s owner=%s: %w", repoID, fsID, ownerID, err)
		}
	}
	batch := db.Session().Batch(gocql.LoggedBatch)
	AddDeletePendingPublishedFSObjectOwnerQueries(batch, repoID, fsID, ownerID, createdAt)
	if err := batch.Exec(); err != nil {
		return fmt.Errorf("delete pending published fs_object owner repo=%s fs=%s owner=%s: %w", repoID, fsID, ownerID, err)
	}
	return nil
}

func (db *DB) PendingPublishedFSObjectOwnerExists(repoID, fsID string) (bool, error) {
	if db == nil || repoID == "" || fsID == "" {
		return false, nil
	}
	var ownerID string
	err := db.Session().Query(`
		SELECT owner_id FROM pending_published_fs_objects
		WHERE repo_id = ? AND fs_id = ?
		LIMIT 1
	`, repoID, fsID).Scan(&ownerID)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("check pending published fs_object owners repo=%s fs=%s: %w", repoID, fsID, err)
	}
	return true, nil
}

func (db *DB) LoadPendingPublishedFSObjectOwner(repoID, fsID, ownerID string) (PendingPublishedFSObjectOwner, error) {
	if db == nil || repoID == "" || fsID == "" || ownerID == "" {
		return PendingPublishedFSObjectOwner{}, nil
	}
	owner := PendingPublishedFSObjectOwner{}
	err := db.Session().Query(`
		SELECT created_at, org_id, attempt_id, block_ids
		FROM pending_published_fs_objects
		WHERE repo_id = ? AND fs_id = ? AND owner_id = ?
	`, repoID, fsID, ownerID).Scan(&owner.CreatedAt, &owner.OrgID, &owner.AttemptID, &owner.BlockIDs)
	if err != nil {
		return PendingPublishedFSObjectOwner{}, err
	}
	owner.RepoID = repoID
	owner.FSID = fsID
	owner.OwnerID = ownerID
	return owner, nil
}

func (db *DB) ListPendingPublishedFSObjectOwnersByDay(day time.Time, bucket int) ([]PendingPublishedFSObjectOwner, error) {
	if db == nil {
		return nil, nil
	}
	iter := db.Session().Query(`
		SELECT created_at, repo_id, fs_id, owner_id
		FROM pending_published_fs_objects_by_day
		WHERE created_day = ? AND bucket = ?
	`, GCProjectionUTCDate(day), bucket).Iter()

	owners := make([]PendingPublishedFSObjectOwner, 0)
	var owner PendingPublishedFSObjectOwner
	for iter.Scan(&owner.CreatedAt, &owner.RepoID, &owner.FSID, &owner.OwnerID) {
		owners = append(owners, owner)
		owner = PendingPublishedFSObjectOwner{}
	}
	if err := iter.Close(); err != nil {
		return nil, fmt.Errorf("list pending published fs_object owners day=%s bucket=%d: %w", GCProjectionDateString(day), bucket, err)
	}
	return owners, nil
}
