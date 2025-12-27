package db

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/apache/cassandra-gocql-driver/v2"
)

// TokenType represents the type of access token
type TokenType string

const (
	TokenTypeUpload   TokenType = "upload"
	TokenTypeDownload TokenType = "download"
)

// AccessToken represents a temporary access token for file operations
type AccessToken struct {
	Token     string
	Type      TokenType
	OrgID     string
	RepoID    string
	Path      string // File path for downloads, parent dir for uploads
	UserID    string
	CreatedAt time.Time
}

// TokenStore provides distributed token management using Cassandra
// Tokens are stored with TTL for automatic expiration
type TokenStore struct {
	session *gocql.Session
	ttl     time.Duration
}

// NewTokenStore creates a new distributed token store
func NewTokenStore(db *DB, ttl time.Duration) *TokenStore {
	if ttl <= 0 {
		ttl = 1 * time.Hour
	}
	return &TokenStore{
		session: db.session,
		ttl:     ttl,
	}
}

// CreateToken creates a new access token and stores it in Cassandra
func (ts *TokenStore) CreateToken(tokenType TokenType, orgID, repoID, path, userID string) (*AccessToken, error) {
	// Generate random token
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return nil, fmt.Errorf("failed to generate token: %w", err)
	}
	tokenStr := hex.EncodeToString(bytes)

	token := &AccessToken{
		Token:     tokenStr,
		Type:      tokenType,
		OrgID:     orgID,
		RepoID:    repoID,
		Path:      path,
		UserID:    userID,
		CreatedAt: time.Now(),
	}

	// Insert with TTL for automatic expiration
	ttlSeconds := int(ts.ttl.Seconds())
	query := `INSERT INTO access_tokens (token, token_type, org_id, repo_id, file_path, user_id, created_at)
	          VALUES (?, ?, ?, ?, ?, ?, ?) USING TTL ?`

	orgUUID, err := gocql.ParseUUID(orgID)
	if err != nil {
		return nil, fmt.Errorf("invalid org_id: %w", err)
	}

	repoUUID, err := gocql.ParseUUID(repoID)
	if err != nil {
		return nil, fmt.Errorf("invalid repo_id: %w", err)
	}

	userUUID, err := gocql.ParseUUID(userID)
	if err != nil {
		return nil, fmt.Errorf("invalid user_id: %w", err)
	}

	err = ts.session.Query(query,
		tokenStr,
		string(tokenType),
		orgUUID,
		repoUUID,
		path,
		userUUID,
		token.CreatedAt,
		ttlSeconds,
	).Exec()

	if err != nil {
		return nil, fmt.Errorf("failed to store token: %w", err)
	}

	return token, nil
}

// CreateUploadToken creates an upload token
func (ts *TokenStore) CreateUploadToken(orgID, repoID, path, userID string) (string, error) {
	token, err := ts.CreateToken(TokenTypeUpload, orgID, repoID, path, userID)
	if err != nil {
		return "", err
	}
	return token.Token, nil
}

// CreateDownloadToken creates a download token
func (ts *TokenStore) CreateDownloadToken(orgID, repoID, path, userID string) (string, error) {
	token, err := ts.CreateToken(TokenTypeDownload, orgID, repoID, path, userID)
	if err != nil {
		return "", err
	}
	return token.Token, nil
}

// GetToken retrieves and validates a token
func (ts *TokenStore) GetToken(tokenStr string, expectedType TokenType) (*AccessToken, bool) {
	query := `SELECT token, token_type, org_id, repo_id, file_path, user_id, created_at
	          FROM access_tokens WHERE token = ?`

	var token AccessToken
	var tokenType string
	var orgUUID, repoUUID, userUUID gocql.UUID

	err := ts.session.Query(query, tokenStr).Scan(
		&token.Token,
		&tokenType,
		&orgUUID,
		&repoUUID,
		&token.Path,
		&userUUID,
		&token.CreatedAt,
	)

	if err != nil {
		// Token not found or expired (TTL)
		return nil, false
	}

	token.Type = TokenType(tokenType)
	token.OrgID = orgUUID.String()
	token.RepoID = repoUUID.String()
	token.UserID = userUUID.String()

	// Check type
	if token.Type != expectedType {
		return nil, false
	}

	return &token, true
}

// DeleteToken removes a token (for single-use tokens like upload)
func (ts *TokenStore) DeleteToken(tokenStr string) error {
	query := `DELETE FROM access_tokens WHERE token = ?`
	return ts.session.Query(query, tokenStr).Exec()
}

// TokenCreator interface for compatibility with existing code
type TokenCreator interface {
	CreateUploadToken(orgID, repoID, path, userID string) (string, error)
	CreateDownloadToken(orgID, repoID, path, userID string) (string, error)
}

// Ensure TokenStore implements TokenCreator
var _ TokenCreator = (*TokenStore)(nil)
