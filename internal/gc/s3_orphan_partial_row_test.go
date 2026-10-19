package gc

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestS3OrphanMigrationHasNoExpirySchedule(t *testing.T) {
	schemaPath := filepath.Join("..", "db", "migrations", "001_initial_schema.cql")
	schema, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("read %s: %v", schemaPath, err)
	}
	for _, table := range []string{
		"gc_s3_orphans",
		"gc_s3_orphans_by_day",
		"gc_s3_orphan_recovery_roots",
	} {
		pattern := regexp.MustCompile(`(?s)CREATE TABLE IF NOT EXISTS ` + table + ` \((.*?);`)
		match := pattern.FindSubmatch(schema)
		if match == nil {
			t.Fatalf("could not find CREATE TABLE for %s", table)
		}
		if !regexp.MustCompile(`default_time_to_live\s*=\s*0`).Match(match[1]) {
			t.Fatalf("%s must have no expiry schedule", table)
		}
	}
}

func TestMockUpdateS3OrphanAttemptRequiresExactAuthority(t *testing.T) {
	store := NewMockStore()
	orgID := uuid.New()
	blockID := "mock-guarded-orphan"
	claimedAt := time.Date(2026, 5, 1, 0, 0, 0, 123456789, time.UTC)
	authority := testCommittedOrphanAuthorityForOrg(orgID, blockID, "hot").Authority()
	created := store.StartBlockDeleteOrphan(orgID, blockID, testCommittedOrphanAuthorityForOrg(orgID, blockID, "hot"), "", claimedAt)
	if created.Outcome != StartBlockDeleteOrphanCreated {
		t.Fatalf("StartBlockDeleteOrphan: outcome=%s cause=%v", created.Outcome, created.Cause)
	}

	wrong := authority
	wrong.ClaimID = "different-claim"
	if err := store.UpdateS3OrphanAttempt(orgID, blockID, wrong, "wrong", claimedAt.Add(time.Hour)); err != nil {
		t.Fatalf("UpdateS3OrphanAttempt with wrong authority: %v", err)
	}
	if orphan := store.AllS3Orphans()[0]; orphan.RetryCount != 0 || orphan.LastError != "" {
		t.Fatalf("wrong authority mutated orphan: retry_count=%d last_error=%q", orphan.RetryCount, orphan.LastError)
	}

	if err := store.UpdateS3OrphanAttempt(orgID, blockID, authority, "matching", claimedAt.Add(365*24*time.Hour)); err != nil {
		t.Fatalf("UpdateS3OrphanAttempt with matching authority: %v", err)
	}
	if orphan := store.AllS3Orphans()[0]; orphan.RetryCount != 1 || orphan.LastError != "matching" {
		t.Fatalf("matching authority did not update orphan: retry_count=%d last_error=%q", orphan.RetryCount, orphan.LastError)
	}
}

func TestMockUpdateS3OrphanAttemptNeverCreates(t *testing.T) {
	store := NewMockStore()
	orgID := uuid.New()
	authority := testCommittedOrphanAuthorityForOrg(orgID, "never-existed", "hot").Authority()
	if err := store.UpdateS3OrphanAttempt(orgID, "never-existed", authority, "boom", time.Now().UTC()); err != nil {
		t.Fatalf("UpdateS3OrphanAttempt on an absent row returned error: %v", err)
	}
	if got := store.S3OrphanCount(); got != 0 {
		t.Fatalf("orphan rows = %d after updating an absent row, want 0", got)
	}
}
