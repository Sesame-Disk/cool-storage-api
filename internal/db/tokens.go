package db

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
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
	Replace   bool   // Default overwrite behavior for upload tokens
	UserID    string
	Source    string // "" or "web" = regular user; "link" = share/upload link
	SourceID  string // Stable non-secret identity for the originating public link
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
func (ts *TokenStore) CreateToken(tokenType TokenType, orgID, repoID, path, userID, source string) (*AccessToken, error) {
	return ts.createToken(tokenType, orgID, repoID, path, userID, source, "", false)
}

func (ts *TokenStore) createToken(tokenType TokenType, orgID, repoID, path, userID, source, sourceID string, replace bool) (*AccessToken, error) {
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
		Replace:   replace,
		UserID:    userID,
		Source:    source,
		SourceID:  sourceID,
		CreatedAt: time.Now(),
	}

	// Insert with TTL for automatic expiration
	// Note: "token" is quoted because it's a reserved keyword in CQL
	ttlSeconds := int(ts.ttl.Seconds())
	query := `INSERT INTO access_tokens ("token", token_type, org_id, repo_id, file_path, user_id, source, source_id, created_at, replace_existing)
	          VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?) USING TTL ?`

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
		source,
		sourceID,
		token.CreatedAt,
		replace,
		ttlSeconds,
	).Exec()

	if err != nil {
		return nil, fmt.Errorf("failed to store token: %w", err)
	}

	return token, nil
}

// CreateUploadToken creates an upload token for a regular (web) user.
func (ts *TokenStore) CreateUploadToken(orgID, repoID, path, userID string) (string, error) {
	token, err := ts.createToken(TokenTypeUpload, orgID, repoID, path, userID, "", "", false)
	if err != nil {
		return "", err
	}
	return token.Token, nil
}

// CreateUpdateToken creates an upload token that overwrites the target path by default.
func (ts *TokenStore) CreateUpdateToken(orgID, repoID, path, userID string) (string, error) {
	token, err := ts.createToken(TokenTypeUpload, orgID, repoID, path, userID, "", "", true)
	if err != nil {
		return "", err
	}
	return token.Token, nil
}

// CreateLinkUploadToken creates an upload token for a share/upload link — tagged as source="link".
func (ts *TokenStore) CreateLinkUploadToken(orgID, repoID, path, userID, sourceID string) (string, error) {
	token, err := ts.createToken(TokenTypeUpload, orgID, repoID, path, userID, "link", sourceID, false)
	if err != nil {
		return "", err
	}
	return token.Token, nil
}

// CreateDownloadToken creates a download token for a regular (web) user.
func (ts *TokenStore) CreateDownloadToken(orgID, repoID, path, userID string) (string, error) {
	token, err := ts.CreateToken(TokenTypeDownload, orgID, repoID, path, userID, "")
	if err != nil {
		return "", err
	}
	return token.Token, nil
}

// CreateLinkDownloadToken creates a download token for a share link — tagged as source="link".
func (ts *TokenStore) CreateLinkDownloadToken(orgID, repoID, path, userID string) (string, error) {
	token, err := ts.CreateToken(TokenTypeDownload, orgID, repoID, path, userID, "link")
	if err != nil {
		return "", err
	}
	return token.Token, nil
}

func resolveReplaceDefault(tokenType TokenType, replaceExisting *bool) bool {
	if replaceExisting != nil {
		return *replaceExisting
	}

	// Legacy upload tokens created before migration 004 had no persisted replace
	// bit and always behaved as overwrite-by-default because HandleUpload used
	// replace=1 when the multipart form omitted the field.
	return tokenType == TokenTypeUpload
}

func resolveSourceID(sourceID *string) string {
	if sourceID == nil {
		return ""
	}
	return *sourceID
}

// GetToken retrieves and validates a token
func (ts *TokenStore) GetToken(tokenStr string, expectedType TokenType) (*AccessToken, bool) {
	query := `SELECT "token", token_type, org_id, repo_id, file_path, user_id, source, source_id, created_at, replace_existing
	          FROM access_tokens WHERE "token" = ?`

	var token AccessToken
	var tokenType string
	var orgUUID, repoUUID, userUUID gocql.UUID
	var replaceExisting *bool
	var sourceID *string

	err := ts.session.Query(query, tokenStr).Scan(
		&token.Token,
		&tokenType,
		&orgUUID,
		&repoUUID,
		&token.Path,
		&userUUID,
		&token.Source,
		&sourceID,
		&token.CreatedAt,
		&replaceExisting,
	)

	if err != nil {
		// Token not found or expired (TTL)
		return nil, false
	}

	token.Type = TokenType(tokenType)
	token.OrgID = orgUUID.String()
	token.RepoID = repoUUID.String()
	token.UserID = userUUID.String()
	token.Replace = resolveReplaceDefault(token.Type, replaceExisting)
	token.SourceID = resolveSourceID(sourceID)

	// Check type
	if token.Type != expectedType {
		return nil, false
	}

	return &token, true
}

// DeleteToken removes a token (for single-use tokens like upload)
func (ts *TokenStore) DeleteToken(tokenStr string) error {
	query := `DELETE FROM access_tokens WHERE "token" = ?`
	return ts.session.Query(query, tokenStr).Exec()
}

// TokenCreator interface for compatibility with existing code
type TokenCreator interface {
	CreateUploadToken(orgID, repoID, path, userID string) (string, error)
	CreateUpdateToken(orgID, repoID, path, userID string) (string, error)
	CreateDownloadToken(orgID, repoID, path, userID string) (string, error)
	CreateLinkUploadToken(orgID, repoID, path, userID, sourceID string) (string, error)
	CreateLinkDownloadToken(orgID, repoID, path, userID string) (string, error)
}

// Ensure TokenStore implements TokenCreator
var _ TokenCreator = (*TokenStore)(nil)
