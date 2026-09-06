//go:build integration

package v2

import (
	"fmt"
	"strings"

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
	case "definitely_not_published":
		outcomeValue = publishedBlockReferenceRepairCommitDefinitelyNotPublished
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
	case publishedBlockReferenceRepairCommitDefinitelyNotPublished:
		return "definitely_not_published", err
	default:
		return "unknown", err
	}
}
