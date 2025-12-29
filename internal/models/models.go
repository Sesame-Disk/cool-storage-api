package models

import (
	"time"

	"github.com/google/uuid"
)

// Organization represents a tenant in the system
type Organization struct {
	OrgID              uuid.UUID         `json:"org_id"`
	Name               string            `json:"name"`
	Settings           map[string]string `json:"settings,omitempty"`
	StorageQuota       int64             `json:"storage_quota"`
	StorageUsed        int64             `json:"storage_used"`
	ChunkingPolynomial int64             `json:"-"` // Per-tenant security
	StorageConfig      map[string]string `json:"storage_config,omitempty"`
	CreatedAt          time.Time         `json:"created_at"`
}

// User represents a user in the system
type User struct {
	UserID     uuid.UUID `json:"user_id"`
	OrgID      uuid.UUID `json:"org_id"`
	Email      string    `json:"email"`
	Name       string    `json:"name"`
	Role       string    `json:"role"` // admin, user
	OIDCSub    string    `json:"-"`    // OIDC subject identifier
	QuotaBytes int64     `json:"quota_bytes"`
	UsedBytes  int64     `json:"used_bytes"`
	CreatedAt  time.Time `json:"created_at"`
}

// Library represents a file library (repository)
// JSON field names match Seafile/Seahub frontend expectations
type Library struct {
	LibraryID      uuid.UUID `json:"repo_id"`                // Seahub frontend expects repo_id
	OrgID          uuid.UUID `json:"org_id,omitempty"`
	OwnerID        uuid.UUID `json:"owner_id,omitempty"`
	Owner          string    `json:"owner_email,omitempty"`  // Seahub frontend expects owner_email
	OwnerName      string    `json:"owner_name,omitempty"`   // Display name for owner
	Name           string    `json:"repo_name"`              // Seahub frontend expects repo_name
	Description    string    `json:"description,omitempty"`
	Encrypted      bool      `json:"encrypted"`
	EncVersion     int       `json:"enc_version,omitempty"`
	Magic          string    `json:"-"` // For client-side encryption
	RandomKey      string    `json:"-"` // For client-side encryption
	RootCommitID   string    `json:"-"`
	HeadCommitID   string    `json:"head_commit_id,omitempty"`
	StorageClass   string    `json:"storage_name,omitempty"` // Seahub uses storage_name
	SizeBytes      int64     `json:"size"`
	FileCount      int64     `json:"file_count,omitempty"`
	VersionTTLDays int       `json:"version_ttl_days,omitempty"`
	MTime          int64     `json:"last_modified"`          // Seahub frontend expects last_modified
	Type           string    `json:"type,omitempty"`         // "repo" for Seafile compatibility
	Permission     string    `json:"permission,omitempty"`   // "rw" or "r" for Seafile
	Starred        bool      `json:"starred,omitempty"`      // User has starred this repo
	Monitored      bool      `json:"monitored,omitempty"`    // User monitors this repo
	Status         string    `json:"status,omitempty"`       // Repo status
	CreatedAt      time.Time `json:"created_at,omitempty"`
	UpdatedAt      time.Time `json:"updated_at,omitempty"`
}

// Commit represents a version snapshot of a library
type Commit struct {
	LibraryID   uuid.UUID `json:"library_id"`
	CommitID    string    `json:"commit_id"`
	ParentID    string    `json:"parent_id,omitempty"`
	RootFSID    string    `json:"root_fs_id"`
	CreatorID   uuid.UUID `json:"creator_id"`
	Description string    `json:"description,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// FSObject represents a file system object (file or directory)
type FSObject struct {
	LibraryID uuid.UUID `json:"library_id"`
	FSID      string    `json:"fs_id"` // SHA-256 of content
	Type      string    `json:"type"`  // "file" or "dir"
	Name      string    `json:"name"`
	Entries   []DirEntry `json:"entries,omitempty"` // For directories
	BlockIDs  []string   `json:"block_ids,omitempty"` // For files
	SizeBytes int64      `json:"size"`
	MTime     int64      `json:"mtime"` // Unix timestamp
}

// DirEntry represents an entry in a directory
type DirEntry struct {
	Name  string `json:"name"`
	ID    string `json:"id"`    // FSID for the entry
	Mode  int    `json:"mode"`  // File mode/permissions
	MTime int64  `json:"mtime"` // Unix timestamp
	Size  int64  `json:"size,omitempty"`
}

// Block represents a content-addressable block
type Block struct {
	OrgID        uuid.UUID `json:"org_id"`
	BlockID      string    `json:"block_id"` // SHA-256 hash
	SizeBytes    int       `json:"size"`
	StorageClass string    `json:"storage_class"`
	StorageKey   string    `json:"storage_key"` // S3 key or Glacier archive ID
	RefCount     int       `json:"ref_count"`
	CreatedAt    time.Time `json:"created_at"`
	LastAccessed time.Time `json:"last_accessed"`
}

// ShareLink represents a public share link
type ShareLink struct {
	Token         string     `json:"token"`
	OrgID         uuid.UUID  `json:"org_id"`
	LibraryID     uuid.UUID  `json:"library_id"`
	Path          string     `json:"path"`
	CreatedBy     uuid.UUID  `json:"created_by"`
	Permission    string     `json:"permission"` // "view", "download", "upload"
	PasswordHash  string     `json:"-"`
	ExpiresAt     *time.Time `json:"expires_at,omitempty"`
	DownloadCount int        `json:"download_count"`
	MaxDownloads  *int       `json:"max_downloads,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

// Share represents a library share to a user or group
type Share struct {
	ShareID    uuid.UUID  `json:"share_id"`
	LibraryID  uuid.UUID  `json:"library_id"`
	SharedBy   uuid.UUID  `json:"shared_by"`
	SharedTo   uuid.UUID  `json:"shared_to"`      // User or group ID
	SharedType string     `json:"shared_to_type"` // "user" or "group"
	Permission string     `json:"permission"`     // "r", "rw"
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
}

// RestoreJob represents a Glacier restore job
type RestoreJob struct {
	JobID        uuid.UUID `json:"job_id"`
	OrgID        uuid.UUID `json:"org_id"`
	LibraryID    uuid.UUID `json:"library_id"`
	BlockIDs     []string  `json:"block_ids"`
	GlacierJobID string    `json:"glacier_job_id,omitempty"`
	Status       string    `json:"status"` // pending, in_progress, completed, failed
	RequestedAt  time.Time `json:"requested_at"`
	CompletedAt  *time.Time `json:"completed_at,omitempty"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
}

// FileInfo represents file information for API responses
type FileInfo struct {
	Name         string    `json:"name"`
	Path         string    `json:"path"`
	Type         string    `json:"type"` // "file" or "dir"
	Size         int64     `json:"size"`
	MTime        time.Time `json:"mtime"`
	Permission   string    `json:"permission,omitempty"`
	Starred      bool      `json:"starred,omitempty"`
	StorageClass string    `json:"storage_class,omitempty"`
}

// UploadLink represents a presigned upload URL
type UploadLink struct {
	URL       string    `json:"url"`
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// DownloadLink represents a presigned download URL
type DownloadLink struct {
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
}
