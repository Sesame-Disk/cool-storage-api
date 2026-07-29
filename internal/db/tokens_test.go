package db

import "testing"

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
