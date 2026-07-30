package api

import (
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
)

func TestAdaptDBAccessTokenSourceID(t *testing.T) {
	createdAt := time.Now()
	for _, test := range []struct {
		name     string
		sourceID string
	}{
		{name: "link identity", sourceID: "stable-link-fingerprint"},
		{name: "legacy token without identity"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := adaptDBAccessToken(&db.AccessToken{
				Token:     "token",
				Type:      db.TokenTypeUpload,
				OrgID:     "org",
				RepoID:    "repo",
				Path:      "/path",
				UserID:    "user",
				Source:    "link",
				SourceID:  test.sourceID,
				CreatedAt: createdAt,
			})
			if got.SourceID != test.sourceID {
				t.Fatalf("SourceID = %q, want %q", got.SourceID, test.sourceID)
			}
			if got.Token != "token" || got.Type != TokenTypeUpload || !got.CreatedAt.Equal(createdAt) {
				t.Fatalf("other token fields were not preserved: %#v", got)
			}
		})
	}
}
