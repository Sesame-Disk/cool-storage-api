//go:build integration

package v2

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

// SetPublishedBlockReferenceRepairOutcomeForIntegration overrides only the
// cold-path publication outcome classifier for a real-Cassandra integration
// leg. It lets the evidence suite model an ambiguous CAS confirmation result
// while still proving that the durable repair row and block references obey
// the same settlement rules. The returned function restores the production
// classifier and must be deferred by the caller.
func SetPublishedBlockReferenceRepairOutcomeForIntegration(outcome string, injectedErr error) (func(), error) {
	var outcomeValue publishedBlockReferenceRepairCommitOutcome
	switch strings.ToLower(strings.TrimSpace(outcome)) {
	case "reachable":
		outcomeValue = publishedBlockReferenceRepairCommitReachable
	case "unknown":
		outcomeValue = publishedBlockReferenceRepairCommitUnknown
	default:
		return nil, fmt.Errorf("unknown integration repair outcome %q", outcome)
	}

	previous := publishedBlockReferenceRepairCommitReachableFn
	publishedBlockReferenceRepairCommitReachableFn = func(_ *db.DB, _, _, _ string) (publishedBlockReferenceRepairCommitOutcome, error) {
		return outcomeValue, injectedErr
	}
	return func() { publishedBlockReferenceRepairCommitReachableFn = previous }, nil
}

// PublishedBlockReferenceRepairCommitOutcomeForIntegration runs the production
// cold-path classifier without exposing its internal outcome type to integration
// packages. It is used by the standalone 3-DC evidence leg.
func PublishedBlockReferenceRepairCommitOutcomeForIntegration(database *db.DB, orgID, repoID, commitID string) (string, error) {
	outcome, err := publishedBlockReferenceRepairCommitReachableFn(database, orgID, repoID, commitID)
	switch outcome {
	case publishedBlockReferenceRepairCommitReachable:
		return "reachable", err
	default:
		return "unknown", err
	}
}

// ReschedulePublishedBlockReferenceRepairForIntegration keeps a real-Cassandra
// fixture's due projection aligned when the test deliberately backdates the
// durable repair row. It uses the production scheduler path and is available
// only to integration builds.
func ReschedulePublishedBlockReferenceRepairForIntegration(database *db.DB, orgID, repoID, commitID, fsID string, stagedBlockIDs []string, nextRetryAt time.Time) error {
	repair := newPublishedBlockReferenceRepair(orgID, repoID, commitID, fsID, stagedBlockIDs)
	currentRetryAt, err := loadPublishedBlockReferenceRepairScheduleStateFn(database, repair)
	if err == nil {
		repair.LeaseExpiresAt = currentRetryAt
	} else if !errors.Is(err, gocql.ErrNotFound) {
		return err
	}
	return schedulePublishedBlockReferenceRepairRetryFn(database, repair, nextRetryAt.UTC())
}
