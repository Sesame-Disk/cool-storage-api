package gc

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
)

// X1 / R19 + R28 — the partial-orphan failure class, in executable form.
//
// Both defects produced the same shape: a gc_s3_orphans row whose primary key is
// still live, whose identity columns (storage_class, first_seen_at,
// recovery_phase) are gone, and which has no gc_s3_orphans_by_day entry. Under
// A+ any orphan row is a writer fence — ProbeBlockReuse answers BlockedByGC on
// the mere existence of a row, and both fence reads select only block_id, which
// a partially expired row still returns. So the shape is a fence that no sweep
// can enumerate and no writer can get past.
//
// R19 reached it by upsert: UpdateS3OrphanAttempt was a plain UPDATE with no IF,
// so a recoverer could write it after another path had cleared the row, and the
// old key-only update could also land on a newer incarnation of that key.
// R28 reached it by ordinary expiry: Cassandra applies default_time_to_live per
// written VALUE, so writing three diagnostic columns on day 89 pushed those to
// day 179 while the identity columns still expired on day 90.
//
// WHY THE UNIT SUITE NEVER CAUGHT EITHER
// ---------------------------------------
// MockStore.UpdateS3OrphanAttempt only mutates when the key is already present,
// so it has always had the semantics the Cassandra implementation lacked. Every
// worker-level unit test therefore exercised correct behaviour against the mock
// while production could still resurrect a row. TestMockUpdateS3OrphanAttempt-
// NeverCreates and GuardsTokenAndExpiry below pin the mock's non-creating,
// different-token, and expired-schedule behavior. The Cassandra LWT remains
// gated for real in internal/integration/gc_s3_orphan_partial_row_test.go.

// TestS3OrphanRemainingTTLSecondsKeepsOneExpirySchedule is the R28 gate. A retry
// must not outlive the identity columns it describes: the diagnostic write is
// given the row's REMAINING life, not a fresh full term.
//
// The day-89 case is the one from the finding. Before the fix the diagnostic
// columns were written with the table default and expired on day 179, 89 days
// after the storage_class and first_seen_at they annotate.
func TestS3OrphanRemainingTTLSecondsKeepsOneExpirySchedule(t *testing.T) {
	const day = 24 * 60 * 60
	firstSeen := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)

	cases := []struct {
		name string
		now  time.Time
		want int
	}{
		{
			name: "fresh row gets the full term",
			now:  firstSeen,
			want: gcS3OrphanTTLSeconds,
		},
		{
			name: "day 89 retry expires with the row, not 89 days after it",
			now:  firstSeen.AddDate(0, 0, 89),
			want: gcS3OrphanTTLSeconds - 89*day,
		},
		{
			// Not clamped to 1: a one-second overhang is still a partial row.
			// Zero tells the caller to skip the write entirely.
			name: "exact expiry tells the caller not to write",
			now:  firstSeen.AddDate(0, 0, 90),
			want: 0,
		},
		{
			name: "fractional final second tells the caller not to write",
			now:  firstSeen.Add(gcS3OrphanTTLSeconds*time.Second - 500*time.Millisecond),
			want: 0,
		},
		{
			name: "remaining life is floored to whole seconds",
			now:  firstSeen.Add(gcS3OrphanTTLSeconds*time.Second - 1500*time.Millisecond),
			want: 1,
		},
		{
			name: "past expiry never resurrects a term",
			now:  firstSeen.AddDate(0, 0, 400),
			want: gcS3OrphanTTLSeconds - 400*day,
		},
		{
			name: "first seen in the future skips the diagnostic write",
			now:  firstSeen.AddDate(0, 0, -30),
			want: 0,
		},
		{
			name: "subsecond future first seen also skips the diagnostic write",
			now:  firstSeen.Add(-500 * time.Millisecond),
			want: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := s3OrphanRemainingTTLSeconds(firstSeen, tc.now)
			if got != tc.want {
				t.Fatalf("s3OrphanRemainingTTLSeconds() = %d, want %d", got, tc.want)
			}
			if got > gcS3OrphanTTLSeconds {
				t.Fatalf("s3OrphanRemainingTTLSeconds() = %d, above the schema default %d", got, gcS3OrphanTTLSeconds)
			}
		})
	}

	// The property the table above is really asserting: under the application
	// clock schedule, a diagnostic write is never scheduled after the calculated
	// identity expiry. The actual Cassandra-cell expiry remains a separate
	// coordinator-clock concern.
	identityExpiry := firstSeen.Add(gcS3OrphanTTLSeconds * time.Second)
	for elapsedDays := -30; elapsedDays <= 120; elapsedDays++ {
		now := firstSeen.AddDate(0, 0, elapsedDays)
		ttl := s3OrphanRemainingTTLSeconds(firstSeen, now)
		if ttl <= 0 {
			// Caller skips the write; nothing can outlive anything.
			continue
		}
		if diagnosticExpiry := now.Add(time.Duration(ttl) * time.Second); diagnosticExpiry.After(identityExpiry) {
			t.Fatalf("day %d: diagnostic columns would expire at %s, after identity columns at %s",
				elapsedDays, diagnosticExpiry, identityExpiry)
		}
	}
}

// TestS3OrphanTTLConstantMatchesSchema keeps gcS3OrphanTTLSeconds aligned with
// the greenfield/base schema. The test does not inspect later ALTER TABLE
// migrations; the integration suite checks the effective migrated schema.
func TestS3OrphanTTLConstantMatchesSchema(t *testing.T) {
	schemaPath := filepath.Join("..", "db", "migrations", "001_initial_schema.cql")
	schema, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("read %s: %v", schemaPath, err)
	}

	// Both gc_s3_orphans and its _by_day projection must carry the same value:
	// a projection that outlives or predeceases its canonical row is the very
	// partial state this pair of findings is about.
	for _, table := range []string{"gc_s3_orphans", "gc_s3_orphans_by_day"} {
		pattern := regexp.MustCompile(`(?s)CREATE TABLE IF NOT EXISTS ` + table + ` \((.*?);`)
		match := pattern.FindSubmatch(schema)
		if match == nil {
			t.Fatalf("could not find CREATE TABLE for %s in %s", table, schemaPath)
		}
		ttlPattern := regexp.MustCompile(`default_time_to_live\s*=\s*(\d+)`)
		ttlMatch := ttlPattern.FindSubmatch(match[1])
		if ttlMatch == nil {
			t.Fatalf("%s has no default_time_to_live in the base schema; if the TTL package removed it, "+
				"UpdateS3OrphanAttempt must stop computing a remaining TTL (R28)", table)
		}
		ttl, err := strconv.Atoi(string(ttlMatch[1]))
		if err != nil {
			t.Fatalf("parse default_time_to_live for %s: %v", table, err)
		}
		if ttl != gcS3OrphanTTLSeconds {
			t.Fatalf("%s default_time_to_live = %d, but gcS3OrphanTTLSeconds = %d",
				table, ttl, gcS3OrphanTTLSeconds)
		}
	}
}

// TestMockUpdateS3OrphanAttemptNeverCreates pins the semantics the mock already
// had and the Cassandra store did not. Without this, a future "simplification"
// of the mock to an unconditional or cross-incarnation update would make the
// whole worker unit suite agree with the defect instead of with the fix.
func TestMockUpdateS3OrphanAttemptNeverCreates(t *testing.T) {
	store := NewMockStore()
	orgID := uuid.New()

	if err := store.UpdateS3OrphanAttempt(orgID, "never-existed", time.Now().UTC(), "boom", time.Now().UTC()); err != nil {
		t.Fatalf("UpdateS3OrphanAttempt on an absent row returned error: %v", err)
	}
	if got := store.S3OrphanCount(); got != 0 {
		t.Fatalf("orphan rows = %d after updating an absent row, want 0: only "+
			"UpdateS3OrphanAttempt must remain non-creating (R19)", got)
	}
}

func TestMockUpdateS3OrphanAttemptGuardsTokenAndExpiry(t *testing.T) {
	firstSeen := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		expected   time.Time
		now        time.Time
		wantUpdate bool
	}{
		{
			name:       "different lifecycle token",
			expected:   firstSeen.Add(time.Second),
			now:        firstSeen.Add(time.Hour),
			wantUpdate: false,
		},
		{
			name:       "future application clock",
			expected:   firstSeen,
			now:        firstSeen.Add(-500 * time.Millisecond),
			wantUpdate: false,
		},
		{
			name:       "identity expiry",
			expected:   firstSeen,
			now:        firstSeen.Add(gcS3OrphanTTLSeconds * time.Second),
			wantUpdate: false,
		},
		{
			name:       "live matching lifecycle",
			expected:   firstSeen,
			now:        firstSeen.Add(time.Hour),
			wantUpdate: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := NewMockStore()
			orgID := uuid.New()
			const blockID = "mock-guarded-orphan"
			if _, err := store.StartBlockDeleteOrphan(orgID, blockID, "hot", blockID, "", firstSeen); err != nil {
				t.Fatalf("StartBlockDeleteOrphan: %v", err)
			}

			if err := store.UpdateS3OrphanAttempt(orgID, blockID, tc.expected, "boom", tc.now); err != nil {
				t.Fatalf("UpdateS3OrphanAttempt: %v", err)
			}
			orphans := store.AllS3Orphans()
			if len(orphans) != 1 {
				t.Fatalf("orphan rows = %d, want 1", len(orphans))
			}
			orphan := orphans[0]
			if tc.wantUpdate {
				if orphan.RetryCount != 1 || orphan.LastError != "boom" {
					t.Fatalf("updated orphan = retry_count %d, last_error %q; want 1, boom", orphan.RetryCount, orphan.LastError)
				}
				return
			}
			if orphan.RetryCount != 0 || orphan.LastError != "" {
				t.Fatalf("guarded orphan mutated = retry_count %d, last_error %q; want 0, empty", orphan.RetryCount, orphan.LastError)
			}
		})
	}
}
