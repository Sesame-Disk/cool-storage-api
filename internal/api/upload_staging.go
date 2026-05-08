package api

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
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

type UploadStagingStore interface {
	UpsertSession(record UploadSessionRecord) error
	UpsertBlock(record UploadSessionBlockRecord) error
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
	return s.db.Session().Query(`
		INSERT INTO upload_sessions (
			org_id, upload_id, repo_id, user_id, token_id, parent_dir, filename,
			actual_filename, commit_id, total_size, state, created_at, updated_at,
			expires_at, last_error
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, record.OrgID, record.UploadID, record.RepoID, record.UserID, record.TokenID,
		record.ParentDir, record.Filename, record.ActualFilename, record.CommitID,
		record.TotalSize, string(record.State), record.CreatedAt, record.UpdatedAt,
		record.ExpiresAt, record.LastError).Exec()
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
