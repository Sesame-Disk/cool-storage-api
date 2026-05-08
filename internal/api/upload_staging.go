package api

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

type UploadSessionState string

const (
	UploadSessionStateReceiving      UploadSessionState = "receiving"
	UploadSessionStatePromoting      UploadSessionState = "promoting"
	UploadSessionStatePublished      UploadSessionState = "published"
	UploadSessionStateClosed         UploadSessionState = "closed"
	UploadSessionStateCleanupPending UploadSessionState = "cleanup_pending"
	UploadSessionStateAborted        UploadSessionState = "aborted"
)

type UploadBlockState string

const (
	UploadBlockStateRegistered     UploadBlockState = "registered"
	UploadBlockStateUploaded       UploadBlockState = "uploaded"
	UploadBlockStatePromoted       UploadBlockState = "promoted"
	UploadBlockStateCleanupPending UploadBlockState = "cleanup_pending"
	UploadBlockStateCleaned        UploadBlockState = "cleaned"
)

type UploadBlockSource string

const (
	UploadBlockSourceNewObject    UploadBlockSource = "new_object"
	UploadBlockSourceExistingLive UploadBlockSource = "existing_live"
)

type UploadSessionRecord struct {
	UploadID       string
	OrgID          string
	RepoID         string
	UserID         string
	TokenID        string
	ParentDir      string
	Filename       string
	ActualFilename string
	CommitID       string
	TotalSize      int64
	State          UploadSessionState
	CreatedAt      time.Time
	UpdatedAt      time.Time
	ExpiresAt      time.Time
	LastError      string
}

type UploadSessionBlockRecord struct {
	UploadID     string
	BlockIndex   int
	OrgID        string
	BlockSHA1    string
	BlockSHA256  string
	SizeBytes    int
	StorageClass string
	StorageKey   string
	Source       UploadBlockSource
	State        UploadBlockState
	CreatedAt    time.Time
	UpdatedAt    time.Time
	UploadedAt   *time.Time
	PromotedAt   *time.Time
}

type UploadBlockPromotionRecord struct {
	OrgID       string
	UploadID    string
	BlockIndex  int
	BlockSHA256 string
	CommitID    string
	ClaimedAt   time.Time
	AppliedAt   *time.Time
}

type UploadBlockPromotionAttempt struct {
	Inserted    bool
	BlockSHA256 string
	CommitID    string
	AppliedAt   *time.Time
}

type UploadStagingStore interface {
	UpsertSession(record UploadSessionRecord) error
	UpsertBlock(record UploadSessionBlockRecord) error
	GetSession(orgID, uploadID string) (*UploadSessionRecord, error)
	ListSessionsByState(orgID string, state UploadSessionState, limit int) ([]UploadSessionRecord, error)
	ListBlockPromotions(orgID, uploadID string) ([]UploadBlockPromotionRecord, error)
	TryStartBlockPromotion(record UploadBlockPromotionRecord) (UploadBlockPromotionAttempt, error)
	MarkBlockPromotionApplied(orgID, uploadID string, blockIndex int, appliedAt time.Time) error
	DeleteBlockPromotion(orgID, uploadID string, blockIndex int) error
}

type CassandraUploadStagingStore struct {
	db *db.DB
}

func NewCassandraUploadStagingStore(database *db.DB) UploadStagingStore {
	if database == nil {
		return nil
	}
	return &CassandraUploadStagingStore{db: database}
}

func NormalizeUploadParentDir(parentDir string) string {
	normalizedParent := strings.TrimSpace(parentDir)
	normalizedParent = strings.ReplaceAll(normalizedParent, "\\", "/")
	if normalizedParent == "" {
		return "/"
	}
	if !strings.HasPrefix(normalizedParent, "/") {
		normalizedParent = "/" + normalizedParent
	}
	normalizedParent = path.Clean(normalizedParent)
	if normalizedParent == "." {
		return "/"
	}
	return normalizedParent
}

func BuildUploadSessionID(orgID, repoID, userID, tokenID, parentDir, filename string) string {
	normalizedParent := NormalizeUploadParentDir(parentDir)
	sum := sha256.Sum256([]byte(strings.Join([]string{orgID, repoID, userID, tokenID, normalizedParent, filename}, "\x00")))
	return hex.EncodeToString(sum[:])
}

func (s *CassandraUploadStagingStore) UpsertSession(record UploadSessionRecord) error {
	if strings.TrimSpace(record.UploadID) == "" {
		return fmt.Errorf("upload id is required")
	}
	if s == nil || s.db == nil {
		return nil
	}
	if err := s.db.Session().Query(`
		INSERT INTO upload_sessions (
			org_id, upload_id, repo_id, user_id, token_id, parent_dir, filename,
			actual_filename, commit_id, total_size, state, created_at, updated_at,
			expires_at, last_error
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, record.OrgID, record.UploadID, record.RepoID, record.UserID, record.TokenID,
		record.ParentDir, record.Filename, record.ActualFilename, record.CommitID,
		record.TotalSize, string(record.State), record.CreatedAt, record.UpdatedAt,
		record.ExpiresAt, record.LastError).Exec(); err != nil {
		return err
	}
	return s.db.Session().Query(`
		INSERT INTO upload_sessions_by_state (
			state, org_id, updated_at, upload_id, repo_id, commit_id, expires_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)
	`, string(record.State), record.OrgID, record.UpdatedAt, record.UploadID, record.RepoID, record.CommitID, record.ExpiresAt).Exec()
}

func (s *CassandraUploadStagingStore) UpsertBlock(record UploadSessionBlockRecord) error {
	if strings.TrimSpace(record.UploadID) == "" {
		return fmt.Errorf("upload id is required")
	}
	if strings.TrimSpace(record.BlockSHA256) == "" {
		return fmt.Errorf("block sha256 is required")
	}
	if s == nil || s.db == nil {
		return nil
	}
	if err := s.db.Session().Query(`
		INSERT INTO upload_session_blocks (
			upload_id, block_index, org_id, block_sha1, block_sha256, size_bytes,
			storage_class, storage_key, source, state, created_at, updated_at,
			uploaded_at, promoted_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, record.UploadID, record.BlockIndex, record.OrgID, record.BlockSHA1,
		record.BlockSHA256, record.SizeBytes, record.StorageClass, record.StorageKey,
		string(record.Source), string(record.State), record.CreatedAt, record.UpdatedAt,
		record.UploadedAt, record.PromotedAt).Exec(); err != nil {
		return err
	}
	return s.db.Session().Query(`
		INSERT INTO upload_session_blocks_by_sha256 (
			org_id, block_sha256, upload_id, block_index, state, updated_at
		) VALUES (?, ?, ?, ?, ?, ?)
	`, record.OrgID, record.BlockSHA256, record.UploadID, record.BlockIndex,
		string(record.State), record.UpdatedAt).Exec()
}

func (s *CassandraUploadStagingStore) GetSession(orgID, uploadID string) (*UploadSessionRecord, error) {
	if strings.TrimSpace(orgID) == "" {
		return nil, fmt.Errorf("org id is required")
	}
	if strings.TrimSpace(uploadID) == "" {
		return nil, fmt.Errorf("upload id is required")
	}
	if s == nil || s.db == nil {
		return nil, nil
	}

	var record UploadSessionRecord
	err := s.db.Session().Query(`
		SELECT repo_id, user_id, token_id, parent_dir, filename, actual_filename,
		       commit_id, total_size, state, created_at, updated_at, expires_at, last_error
		FROM upload_sessions WHERE org_id = ? AND upload_id = ?
	`, orgID, uploadID).Scan(
		&record.RepoID,
		&record.UserID,
		&record.TokenID,
		&record.ParentDir,
		&record.Filename,
		&record.ActualFilename,
		&record.CommitID,
		&record.TotalSize,
		&record.State,
		&record.CreatedAt,
		&record.UpdatedAt,
		&record.ExpiresAt,
		&record.LastError,
	)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	record.OrgID = orgID
	record.UploadID = uploadID
	return &record, nil
}

func (s *CassandraUploadStagingStore) ListSessionsByState(orgID string, state UploadSessionState, limit int) ([]UploadSessionRecord, error) {
	if strings.TrimSpace(orgID) == "" {
		return nil, fmt.Errorf("org id is required")
	}
	if strings.TrimSpace(string(state)) == "" {
		return nil, fmt.Errorf("state is required")
	}
	if limit <= 0 {
		limit = 16
	}
	if s == nil || s.db == nil {
		return nil, nil
	}

	iter := s.db.Session().Query(`
		SELECT updated_at, upload_id, repo_id, commit_id, expires_at
		FROM upload_sessions_by_state WHERE state = ? AND org_id = ? LIMIT ?
	`, string(state), orgID, limit).Iter()

	var records []UploadSessionRecord
	var (
		updatedAt time.Time
		uploadID  string
		repoID    string
		commitID  string
		expiresAt time.Time
	)
	for iter.Scan(&updatedAt, &uploadID, &repoID, &commitID, &expiresAt) {
		records = append(records, UploadSessionRecord{
			OrgID:     orgID,
			UploadID:  uploadID,
			RepoID:    repoID,
			CommitID:  commitID,
			State:     state,
			UpdatedAt: updatedAt,
			ExpiresAt: expiresAt,
		})
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return records, nil
}

func (s *CassandraUploadStagingStore) ListBlockPromotions(orgID, uploadID string) ([]UploadBlockPromotionRecord, error) {
	if strings.TrimSpace(orgID) == "" {
		return nil, fmt.Errorf("org id is required")
	}
	if strings.TrimSpace(uploadID) == "" {
		return nil, fmt.Errorf("upload id is required")
	}
	if s == nil || s.db == nil {
		return nil, nil
	}

	iter := s.db.Session().Query(`
		SELECT block_index, block_sha256, commit_id, claimed_at, applied_at
		FROM upload_block_promotions WHERE org_id = ? AND upload_id = ?
	`, orgID, uploadID).Iter()

	var records []UploadBlockPromotionRecord
	var (
		blockIndex  int
		blockSHA256 string
		commitID    string
		claimedAt   time.Time
		appliedAt   *time.Time
	)
	for iter.Scan(&blockIndex, &blockSHA256, &commitID, &claimedAt, &appliedAt) {
		records = append(records, UploadBlockPromotionRecord{
			OrgID:       orgID,
			UploadID:    uploadID,
			BlockIndex:  blockIndex,
			BlockSHA256: blockSHA256,
			CommitID:    commitID,
			ClaimedAt:   claimedAt,
			AppliedAt:   appliedAt,
		})
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return records, nil
}

func (s *CassandraUploadStagingStore) TryStartBlockPromotion(record UploadBlockPromotionRecord) (UploadBlockPromotionAttempt, error) {
	if strings.TrimSpace(record.OrgID) == "" {
		return UploadBlockPromotionAttempt{}, fmt.Errorf("org id is required")
	}
	if strings.TrimSpace(record.UploadID) == "" {
		return UploadBlockPromotionAttempt{}, fmt.Errorf("upload id is required")
	}
	if strings.TrimSpace(record.BlockSHA256) == "" {
		return UploadBlockPromotionAttempt{}, fmt.Errorf("block sha256 is required")
	}
	if strings.TrimSpace(record.CommitID) == "" {
		return UploadBlockPromotionAttempt{}, fmt.Errorf("commit id is required")
	}
	if s == nil || s.db == nil {
		return UploadBlockPromotionAttempt{
			Inserted:    true,
			BlockSHA256: record.BlockSHA256,
			CommitID:    record.CommitID,
		}, nil
	}

	existing := map[string]interface{}{}
	applied, err := s.db.Session().Query(`
		INSERT INTO upload_block_promotions (
			org_id, upload_id, block_index, block_sha256, commit_id, claimed_at, applied_at
		) VALUES (?, ?, ?, ?, ?, ?, ?) IF NOT EXISTS
	`, record.OrgID, record.UploadID, record.BlockIndex, record.BlockSHA256, record.CommitID, record.ClaimedAt, record.AppliedAt).MapScanCAS(existing)
	if err != nil {
		return UploadBlockPromotionAttempt{}, err
	}
	if applied {
		return UploadBlockPromotionAttempt{
			Inserted:    true,
			BlockSHA256: record.BlockSHA256,
			CommitID:    record.CommitID,
		}, nil
	}

	return UploadBlockPromotionAttempt{
		Inserted:    false,
		BlockSHA256: uploadPromotionStringValue(existing, "block_sha256"),
		CommitID:    uploadPromotionStringValue(existing, "commit_id"),
		AppliedAt:   uploadPromotionTimeValue(existing, "applied_at"),
	}, nil
}

func (s *CassandraUploadStagingStore) MarkBlockPromotionApplied(orgID, uploadID string, blockIndex int, appliedAt time.Time) error {
	if strings.TrimSpace(orgID) == "" {
		return fmt.Errorf("org id is required")
	}
	if strings.TrimSpace(uploadID) == "" {
		return fmt.Errorf("upload id is required")
	}
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Session().Query(`
		UPDATE upload_block_promotions SET applied_at = ?
		WHERE org_id = ? AND upload_id = ? AND block_index = ?
	`, appliedAt, orgID, uploadID, blockIndex).Exec()
}

func (s *CassandraUploadStagingStore) DeleteBlockPromotion(orgID, uploadID string, blockIndex int) error {
	if strings.TrimSpace(orgID) == "" {
		return fmt.Errorf("org id is required")
	}
	if strings.TrimSpace(uploadID) == "" {
		return fmt.Errorf("upload id is required")
	}
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Session().Query(`
		DELETE FROM upload_block_promotions WHERE org_id = ? AND upload_id = ? AND block_index = ?
	`, orgID, uploadID, blockIndex).Exec()
}

func uploadPromotionStringValue(row map[string]interface{}, key string) string {
	value, ok := row[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	case []byte:
		return string(typed)
	default:
		return fmt.Sprint(typed)
	}
}

func uploadPromotionTimeValue(row map[string]interface{}, key string) *time.Time {
	value, ok := row[key]
	if !ok || value == nil {
		return nil
	}
	switch typed := value.(type) {
	case time.Time:
		promotedAt := typed.UTC()
		return &promotedAt
	case *time.Time:
		if typed == nil {
			return nil
		}
		promotedAt := typed.UTC()
		return &promotedAt
	default:
		return nil
	}
}
