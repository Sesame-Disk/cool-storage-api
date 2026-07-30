//go:build integration

package integration

import (
	"testing"
	"time"

	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/google/uuid"
)

func TestAccessTokenSourceIDPersistsAcrossLinkUploadTokenRemints(t *testing.T) {
	requireCassandra(t)
	database := shareProjectionDBForTest(t)

	var columnName string
	if err := database.Session().Query(`
		SELECT column_name
		FROM system_schema.columns
		WHERE keyspace_name = ? AND table_name = ? AND column_name = ?
	`, envOrDefault("CASSANDRA_KEYSPACE", "sesamefs"), "access_tokens", "source_id").Scan(&columnName); err != nil {
		t.Fatalf("migration 013 source_id column not found on access_tokens: %v", err)
	}
	if columnName != "source_id" {
		t.Fatalf("access_tokens column = %q, want source_id", columnName)
	}

	store := dbpkg.NewTokenStore(database, 5*time.Minute)
	orgID := uuid.NewString()
	repoID := uuid.NewString()
	userID := uuid.NewString()
	sourceID := uuid.NewString()

	var createdTokens []string
	t.Cleanup(func() {
		for _, token := range createdTokens {
			if err := store.DeleteToken(token); err != nil {
				t.Errorf("clean up access token %q: %v", token, err)
			}
		}
	})

	firstToken, err := store.CreateLinkUploadToken(orgID, repoID, "/uploads", userID, sourceID)
	if err != nil {
		t.Fatalf("create first link upload token: %v", err)
	}
	createdTokens = append(createdTokens, firstToken)

	secondToken, err := store.CreateLinkUploadToken(orgID, repoID, "/uploads", userID, sourceID)
	if err != nil {
		t.Fatalf("create second link upload token: %v", err)
	}
	createdTokens = append(createdTokens, secondToken)

	if firstToken == secondToken {
		t.Fatalf("reminted link upload token strings are equal: %q", firstToken)
	}
	for label, tokenString := range map[string]string{"first": firstToken, "second": secondToken} {
		token, ok := store.GetToken(tokenString, dbpkg.TokenTypeUpload)
		if !ok {
			t.Fatalf("get %s link upload token %q: not found", label, tokenString)
		}
		if token.SourceID != sourceID {
			t.Errorf("%s token SourceID = %q, want exact %q", label, token.SourceID, sourceID)
		}
	}

	emptySourceToken, err := store.CreateLinkUploadToken(orgID, repoID, "/uploads", userID, "")
	if emptySourceToken != "" {
		createdTokens = append(createdTokens, emptySourceToken)
	}
	if err == nil {
		t.Fatal("CreateLinkUploadToken with empty SourceID succeeded, want rejection")
	}
}
