package db

import (
	"testing"
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

func TestAddUpsertPendingPublishedFSObjectOwnerQueries_KeepsBlockIDsOutOfLoggedBatch(t *testing.T) {
	batch := &gocql.Batch{}
	createdAt := time.Date(2026, time.June, 1, 12, 0, 0, 0, time.UTC)
	blockIDs := []string{"block-a", "block-b"}

	AddUpsertPendingPublishedFSObjectOwnerQueries(batch, "repo-1", "fs-1", "owner-1", createdAt, "org-1", "attempt-1", blockIDs)

	if len(batch.Entries) != 2 {
		t.Fatalf("len(batch.Entries) = %d, want 2", len(batch.Entries))
	}
	primaryArgs := batch.Entries[0].Args
	if len(primaryArgs) != 7 {
		t.Fatalf("len(primaryArgs) = %d, want 7 for lean owner metadata + ttl", len(primaryArgs))
	}
	if got := primaryArgs[4]; got != "org-1" {
		t.Fatalf("primary org_id = %v, want org-1", got)
	}
	if got := primaryArgs[5]; got != "attempt-1" {
		t.Fatalf("primary attempt_id = %v, want attempt-1", got)
	}
	if got := primaryArgs[6]; got != PendingPublishedFSObjectOwnerTTLSeconds {
		t.Fatalf("primary ttl = %v, want %d", got, PendingPublishedFSObjectOwnerTTLSeconds)
	}
	byDayArgs := batch.Entries[1].Args
	if len(byDayArgs) != 7 {
		t.Fatalf("len(byDayArgs) = %d, want 7 for lean discovery projection + ttl", len(byDayArgs))
	}
	if got := byDayArgs[3]; got != "repo-1" {
		t.Fatalf("byDay repo_id = %v, want repo-1", got)
	}
	if got := byDayArgs[4]; got != "fs-1" {
		t.Fatalf("byDay fs_id = %v, want fs-1", got)
	}
	if got := byDayArgs[5]; got != "owner-1" {
		t.Fatalf("byDay owner_id = %v, want owner-1", got)
	}
	if got := byDayArgs[6]; got != PendingPublishedFSObjectOwnerTTLSeconds {
		t.Fatalf("byDay ttl = %v, want %d", got, PendingPublishedFSObjectOwnerTTLSeconds)
	}
}
