package api

import (
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

// CreateDownloadToken creates a download token
func (a *CassandraTokenAdapter) CreateDownloadToken(orgID, repoID, path, userID string) (string, error) {
	return a.store.CreateDownloadToken(orgID, repoID, path, userID)
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
		UserID:    dbToken.UserID,
		CreatedAt: dbToken.CreatedAt,
	}, true
}

// DeleteToken removes a token
func (a *CassandraTokenAdapter) DeleteToken(tokenStr string) error {
	return a.store.DeleteToken(tokenStr)
}

// Ensure CassandraTokenAdapter implements TokenStore
var _ TokenStore = (*CassandraTokenAdapter)(nil)
