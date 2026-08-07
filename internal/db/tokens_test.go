package db

import (
	"fmt"
	"strings"
	"testing"
)

func TestResolveReplaceDefault(t *testing.T) {
	t.Run("legacy upload token defaults to overwrite", func(t *testing.T) {
		if !resolveReplaceDefault(TokenTypeUpload, nil) {
			t.Fatal("legacy upload token should preserve overwrite-by-default behavior")
		}
	})

	t.Run("legacy download token does not opt into overwrite", func(t *testing.T) {
		if resolveReplaceDefault(TokenTypeDownload, nil) {
			t.Fatal("legacy download token should not default Replace to true")
		}
	})

	t.Run("persisted false is respected", func(t *testing.T) {
		replaceExisting := false
		if resolveReplaceDefault(TokenTypeUpload, &replaceExisting) {
			t.Fatal("explicit persisted false should be preserved")
		}
	})

	t.Run("persisted true is respected", func(t *testing.T) {
		replaceExisting := true
		if !resolveReplaceDefault(TokenTypeUpload, &replaceExisting) {
			t.Fatal("explicit persisted true should be preserved")
		}
	})
}

func TestResolveSourceID(t *testing.T) {
	t.Run("legacy token without source ID", func(t *testing.T) {
		if got := resolveSourceID(nil); got != "" {
			t.Fatalf("resolveSourceID(nil) = %q, want empty", got)
		}
	})

	t.Run("persisted source ID", func(t *testing.T) {
		sourceID := "sha256:stable-link-id"
		if got := resolveSourceID(&sourceID); got != sourceID {
			t.Fatalf("resolveSourceID() = %q, want %q", got, sourceID)
		}
	})
}

// requireSourceIDRejection asserts the call failed *because of the source-ID
// guard, not for any other reason.
//
// A bare &TokenStore{} has no session, and these fixtures pass a non-UUID org,
// so createToken fails at gocql.ParseUUID whatever the guard does. Asserting
// only err != nil therefore passes with the guard deleted outright — verified
// by removing it — which makes the test describe nothing. The guard runs before
// the UUID parse and is the only path that names the source ID, so matching the
// message is what distinguishes it.
func requireSourceIDRejection(t *testing.T, context string, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s succeeded, want a source-ID rejection", context)
	}
	if !strings.Contains(err.Error(), "source ID is required") {
		t.Fatalf("%s failed with %v, want the source-ID guard to reject it", context, err)
	}
}

func TestTokenStoreCreateLinkUploadTokenRejectsBlankSourceID(t *testing.T) {
	store := &TokenStore{}
	for _, sourceID := range []string{"", " ", "\t\r\n"} {
		_, err := store.CreateLinkUploadToken("org", "repo", "/", "user", sourceID)
		requireSourceIDRejection(t, fmt.Sprintf("CreateLinkUploadToken(%q)", sourceID), err)
	}
}

func TestTokenStoreCreateLinkDownloadTokenRejectsBlankSourceID(t *testing.T) {
	store := &TokenStore{}
	for _, sourceID := range []string{"", " ", "\t\r\n"} {
		_, err := store.CreateLinkDownloadToken("org", "repo", "/file.txt", "user", sourceID)
		requireSourceIDRejection(t, fmt.Sprintf("CreateLinkDownloadToken(%q)", sourceID), err)
	}
}

func TestTokenStoreCreateTokenRejectsGenericLinkWithoutSourceID(t *testing.T) {
	store := &TokenStore{}
	_, err := store.CreateToken(TokenTypeDownload, "org", "repo", "/file.txt", "user", "link")
	requireSourceIDRejection(t, "generic CreateToken with source=link", err)
}

// Mirror of TestGenericCreateTokenRefusesSyncType in internal/api. Both stores
// carry the same guard, so both need the regression: "only CreateSyncToken
// mints a sync credential" is an architectural invariant, and an invariant with
// one of its two enforcement points untested is one accidental deletion away
// from being false again.
//
// The guard runs before the session is touched, so a zero-value store is enough
// and no Cassandra is required.
//
// Matching the message is what makes this test mean anything, for the same
// reason requireSourceIDRejection does it: these placeholder ids fail the UUID
// parse further down, so "returned an error" would hold with the guard deleted.
// Asserting only that would be a test that can never fail.
func TestTokenStoreCreateTokenRefusesSyncType(t *testing.T) {
	store := &TokenStore{}

	// A non-root path is the case that matters most: it would mint a sync
	// credential that isRepositorySyncToken then rejects — a token that exists
	// but can never be used.
	for _, path := range []string{"/", "/some/file.txt"} {
		_, err := store.CreateToken(TokenTypeSync, "org", "repo", path, "user", "")
		if err == nil {
			t.Errorf("CreateToken(TokenTypeSync, path=%q) succeeded; the generic constructor must refuse it", path)
			continue
		}
		if !strings.Contains(err.Error(), "must be created through CreateSyncToken") {
			t.Errorf("CreateToken(TokenTypeSync, path=%q) failed with %v, want the sync-type guard to reject it", path, err)
		}
	}
}

// A non-canonical source would be a link token to the one EqualFold reader and
// a regular web token to every exact-comparison reader, so it would slip past
// the source-ID requirement below and past the link-only guards downstream.
func TestTokenStoreCreateTokenNormalisesSourceBeforeTheLinkCheck(t *testing.T) {
	store := &TokenStore{}
	for _, source := range []string{"LINK", "Link", " link ", "\tLINK\n"} {
		_, err := store.CreateToken(TokenTypeDownload, "org", "repo", "/file.txt", "user", source)
		requireSourceIDRejection(t, fmt.Sprintf("CreateToken(source=%q)", source), err)
	}
}
