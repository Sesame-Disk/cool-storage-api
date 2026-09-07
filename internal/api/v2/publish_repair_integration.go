//go:build integration

package v2

import (
	"fmt"
	"strings"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
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

// SchedulePublishedBlockReferenceRepairRetryForIntegration exercises the
// production retry LWT from a real-Cassandra integration test.
func SchedulePublishedBlockReferenceRepairRetryForIntegration(database *db.DB, bucket int, orgID, repoID, commitID, fsID string, nextRetryAt time.Time) error {
	return schedulePublishedBlockReferenceRepairRetryFn(database, publishedBlockReferenceRepair{
		Bucket:   bucket,
		OrgID:    orgID,
		RepoID:   repoID,
		CommitID: commitID,
		FSID:     fsID,
	}, nextRetryAt)
}

// DeletePublishedBlockReferenceRepairForIntegration exercises the production
// settlement LWT from a real-Cassandra integration test.
func DeletePublishedBlockReferenceRepairForIntegration(database *db.DB, bucket int, orgID, repoID, commitID, fsID string) error {
	return deletePublishedBlockReferenceRepairFn(database, publishedBlockReferenceRepair{
		Bucket:   bucket,
		OrgID:    orgID,
		RepoID:   repoID,
		CommitID: commitID,
		FSID:     fsID,
	})
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
