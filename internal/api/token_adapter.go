package api

import (
	"fmt"

	"github.com/Sesame-Disk/sesamefs/internal/db"
)

// CassandraTokenAdapter adapts db.TokenStore to api.TokenStore interface
type CassandraTokenAdapter struct {
	store *db.TokenStore
}

// NewCassandraTokenAdapter creates an adapter that wraps db.TokenStore
func NewCassandraTokenAdapter(store *db.TokenStore) *CassandraTokenAdapter {
	return &CassandraTokenAdapter{store: store}
}

// CreateUploadToken creates an upload token
func (a *CassandraTokenAdapter) CreateUploadToken(orgID, repoID, path, userID string) (string, error) {
	return a.store.CreateUploadToken(orgID, repoID, path, userID)
}

// CreateUpdateToken creates an upload token that overwrites by default.
func (a *CassandraTokenAdapter) CreateUpdateToken(orgID, repoID, path, userID string) (string, error) {
	return a.store.CreateUpdateToken(orgID, repoID, path, userID)
}

// CreateLinkUploadToken creates an upload token tagged as a share/upload link.
func (a *CassandraTokenAdapter) CreateLinkUploadToken(orgID, repoID, path, userID string) (string, error) {
	return a.store.CreateLinkUploadToken(orgID, repoID, path, userID)
}

// CreateDownloadToken creates a download token
func (a *CassandraTokenAdapter) CreateDownloadToken(orgID, repoID, path, userID string) (string, error) {
	return a.store.CreateDownloadToken(orgID, repoID, path, userID)
}

// CreateLinkDownloadToken creates a download token tagged as a share link.
func (a *CassandraTokenAdapter) CreateLinkDownloadToken(orgID, repoID, path, userID string) (string, error) {
	return a.store.CreateLinkDownloadToken(orgID, repoID, path, userID)
}

// GetToken retrieves and validates a token
func (a *CassandraTokenAdapter) GetToken(tokenStr string, expectedType TokenType) (*AccessToken, bool) {
	// Convert api.TokenType to db.TokenType
	dbTokenType := db.TokenType(expectedType)

	dbToken, ok := a.store.GetToken(tokenStr, dbTokenType)
	if !ok {
		return nil, false
	}

	// Convert db.AccessToken to api.AccessToken
	return &AccessToken{
		Token:     dbToken.Token,
		Type:      TokenType(dbToken.Type),
		OrgID:     dbToken.OrgID,
		RepoID:    dbToken.RepoID,
		Path:      dbToken.Path,
		Replace:   dbToken.Replace,
		UserID:    dbToken.UserID,
		Source:    dbToken.Source,
		CreatedAt: dbToken.CreatedAt,
	}, true
}

// DeleteToken removes a token
func (a *CassandraTokenAdapter) DeleteToken(tokenStr string) error {
	return a.store.DeleteToken(tokenStr)
}

// CreateOneTimeLoginToken - Not supported by Cassandra adapter (use in-memory TokenManager instead)
func (a *CassandraTokenAdapter) CreateOneTimeLoginToken(userID, orgID, authToken string) (string, error) {
	return "", fmt.Errorf("one-time login tokens not supported by Cassandra adapter")
}

// ConsumeOneTimeLoginToken - Not supported by Cassandra adapter (use in-memory TokenManager instead)
func (a *CassandraTokenAdapter) ConsumeOneTimeLoginToken(oneTimeToken string) (string, error) {
	return "", fmt.Errorf("one-time login tokens not supported by Cassandra adapter")
}

// Ensure CassandraTokenAdapter implements TokenStore
var _ TokenStore = (*CassandraTokenAdapter)(nil)
