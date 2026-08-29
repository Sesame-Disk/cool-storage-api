package db

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/metrics"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

// ErrBlockPublishAuthorityDenied means a staged pub: attempt must not proceed
// to HEAD or Promote. Callers must not treat it as publication success. A
// rollback failure is joined onto this error; the joined result is still a
// denial, never a successful stage.
var ErrBlockPublishAuthorityDenied = errors.New("block publish authority denied")

// BlockPublishAuthorityOutcome is the R3 post-stage verdict for one logical
// block (or a batch, taking the first non-Active). Only Active may continue
// toward publication or fs: promotion.
type BlockPublishAuthorityOutcome int

const (
	BlockPublishAuthorityActive BlockPublishAuthorityOutcome = iota
	BlockPublishAuthorityMissing
	BlockPublishAuthorityDeleting
	BlockPublishAuthorityRepairing
	BlockPublishAuthorityOrphaned
	BlockPublishAuthorityInvalid
	BlockPublishAuthorityUnavailable
)

func (o BlockPublishAuthorityOutcome) String() string {
	switch o {
	case BlockPublishAuthorityActive:
		return "active"
	case BlockPublishAuthorityMissing:
		return "missing"
	case BlockPublishAuthorityDeleting:
		return "deleting"
	case BlockPublishAuthorityRepairing:
		return "repairing"
	case BlockPublishAuthorityOrphaned:
		return "orphaned"
	case BlockPublishAuthorityInvalid:
		return "invalid"
	case BlockPublishAuthorityUnavailable:
		return "unavailable"
	default:
		return "unavailable"
	}
}

type publishAttemptAuthorityRow struct {
	StorageClass    string
	StorageKey      string
	GCState         string
	GCClaimID       string
	GCClaimedAt     *time.Time
	GCOrphanHandoff *bool
	CreatedAt       *time.Time
}

// ValidatePublishAttemptAuthority is the R3 post-stage question: can these
// blocks, whose pub: rows have just been written, still back a publication?
//
// It is deliberately not ProbeBlockReuse (materialization) and not
// ValidateBlockRepairAuthority (exact-P, SERIAL pre-PUT). Under A+, any
// gc_s3_orphans row for L is a writer fence. gc_orphan_handoff=true is never
// Active even if gc_state happens to look clear.
//
// Reads are ordinary LOCAL_QUORUM (BlockFenceReadConsistency), pinned per
// statement. Canonical row first, orphan LAST. An empty batch is vacuously
// Active and issues no reads.
func ValidatePublishAttemptAuthority(database *DB, orgID string, blockIDs []string) (BlockPublishAuthorityOutcome, error) {
	return validatePublishAttemptAuthorityFn(database, orgID, blockIDs)
}

var validatePublishAttemptAuthorityFn = validatePublishAttemptAuthority

func validatePublishAttemptAuthority(database *DB, orgID string, blockIDs []string) (BlockPublishAuthorityOutcome, error) {
	ids := NormalizeBlockIDs(blockIDs)
	if len(ids) == 0 {
		return BlockPublishAuthorityActive, nil
	}
	if database == nil {
		return BlockPublishAuthorityUnavailable, fmt.Errorf("%w: publish authority database is unavailable", ErrBlockPublishAuthorityDenied)
	}
	for _, blockID := range ids {
		outcome, err := classifyOnePublishAttemptAuthority(database, orgID, blockID)
		if outcome != BlockPublishAuthorityActive {
			return outcome, err
		}
	}
	return BlockPublishAuthorityActive, nil
}

func classifyOnePublishAttemptAuthority(database *DB, orgID, blockID string) (BlockPublishAuthorityOutcome, error) {
	if blockID != NormalizeBlockID(blockID) || !IsSHA256BlockID(blockID) {
		return BlockPublishAuthorityInvalid, fmt.Errorf("%w: block id %q is not a canonical lower-case SHA-256", ErrBlockPublishAuthorityDenied, blockID)
	}

	// Read order is load-bearing and copies the P3 writer-fence argument without
	// sharing ValidateBlockRepairAuthority: GC stamps gc_state, then the orphan,
	// then removes the canonical row. Orphan LAST means an absent row cannot be
	// mistaken for "no fence".
	row, found, err := readPublishAttemptAuthorityFn(database, orgID, blockID)
	if err != nil {
		return BlockPublishAuthorityUnavailable, fmt.Errorf("%w: read canonical publish authority for %s: %w", ErrBlockPublishAuthorityDenied, blockID, err)
	}
	hasOrphan, err := publishAttemptHasS3OrphanFn(database, orgID, blockID)
	if err != nil {
		return BlockPublishAuthorityUnavailable, fmt.Errorf("%w: read S3 orphan publish fence for %s: %w", ErrBlockPublishAuthorityDenied, blockID, err)
	}
	if !found {
		if hasOrphan {
			return BlockPublishAuthorityOrphaned, fmt.Errorf("%w: block %s has an orphan fence without a canonical row", ErrBlockPublishAuthorityDenied, blockID)
		}
		return BlockPublishAuthorityMissing, fmt.Errorf("%w: canonical row for block %s is absent", ErrBlockPublishAuthorityDenied, blockID)
	}
	if row.CreatedAt == nil || !config.IsCanonicalStorageClassName(row.StorageClass) || row.StorageKey == "" || strings.TrimSpace(row.StorageKey) != row.StorageKey {
		return BlockPublishAuthorityInvalid, fmt.Errorf("%w: block %s has incomplete or malformed canonical locator", ErrBlockPublishAuthorityDenied, blockID)
	}
	if row.GCOrphanHandoff != nil && *row.GCOrphanHandoff {
		return BlockPublishAuthorityDeleting, fmt.Errorf("%w: block %s has a committed orphan handoff", ErrBlockPublishAuthorityDenied, blockID)
	}
	activeClaim, repairClaim, ownershipErr := classifyBlockClaimOwnership(row.GCState, row.GCClaimID, row.GCClaimedAt)
	if ownershipErr != nil {
		return BlockPublishAuthorityInvalid, fmt.Errorf("%w: block %s has malformed GC ownership: %v", ErrBlockPublishAuthorityDenied, blockID, ownershipErr)
	}
	if activeClaim {
		return BlockPublishAuthorityDeleting, fmt.Errorf("%w: block %s has an active deleting claim", ErrBlockPublishAuthorityDenied, blockID)
	}
	if repairClaim {
		return BlockPublishAuthorityRepairing, fmt.Errorf("%w: block %s has a repairing_stub claim", ErrBlockPublishAuthorityDenied, blockID)
	}
	if hasOrphan {
		return BlockPublishAuthorityOrphaned, fmt.Errorf("%w: block %s has an S3 orphan fence", ErrBlockPublishAuthorityDenied, blockID)
	}
	return BlockPublishAuthorityActive, nil
}

var readPublishAttemptAuthorityFn = func(database *DB, orgID, blockID string) (publishAttemptAuthorityRow, bool, error) {
	if database == nil || database.Session() == nil {
		return publishAttemptAuthorityRow{}, false, errors.New("publish authority database is unavailable")
	}
	var row publishAttemptAuthorityRow
	var storageClass *string
	var storageKey *string
	err := database.Session().Query(`
		SELECT storage_class, storage_key, gc_state, gc_claim_id, gc_claimed_at, gc_orphan_handoff, created_at
		FROM blocks
		WHERE org_id = ? AND block_id = ?
	`, orgID, blockID).Consistency(BlockFenceReadConsistency).Scan(
		&storageClass,
		&storageKey,
		&row.GCState,
		&row.GCClaimID,
		&row.GCClaimedAt,
		&row.GCOrphanHandoff,
		&row.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return publishAttemptAuthorityRow{}, false, nil
		}
		return publishAttemptAuthorityRow{}, false, err
	}
	if storageClass != nil {
		row.StorageClass = *storageClass
	}
	if storageKey != nil {
		row.StorageKey = *storageKey
	}
	return row, true, nil
}

var publishAttemptHasS3OrphanFn = func(database *DB, orgID, blockID string) (bool, error) {
	if database == nil || database.Session() == nil {
		return false, errors.New("publish authority database is unavailable")
	}
	var existingBlockID string
	err := database.Session().Query(`
		SELECT block_id FROM gc_s3_orphans WHERE org_id = ? AND block_id = ? LIMIT 1
	`, orgID, blockID).Consistency(BlockFenceReadConsistency).Scan(&existingBlockID)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return existingBlockID != "", nil
}

// FinishCheckedPublishAttempt is the checked half of the pub: handshake.
// Callers must invoke it AFTER the attempt's pub: rows are fully staged and
// BEFORE HEAD/promote. Only Active continues. Any other outcome rolls back
// exactly this attempt's pub: rows; a rollback error is joined and is still
// not publication success.
func FinishCheckedPublishAttempt(database *DB, orgID, repoID, attemptID string, staged []string) error {
	return finishCheckedPublishAttemptFn(database, orgID, repoID, attemptID, staged)
}

var finishCheckedPublishAttemptFn = finishCheckedPublishAttempt

func finishCheckedPublishAttempt(database *DB, orgID, repoID, attemptID string, staged []string) error {
	outcome, checkErr := validatePublishAttemptAuthorityFn(database, orgID, staged)
	if outcome == BlockPublishAuthorityActive && checkErr == nil {
		metrics.PublishAttemptAuthorityCheckTotal.WithLabelValues(BlockPublishAuthorityActive.String()).Inc()
		return nil
	}
	result := outcome.String()
	metrics.PublishAttemptAuthorityCheckTotal.WithLabelValues(result).Inc()
	log.Printf("[R3] publish attempt authority denied org=%s library=%s attempt=%s reason=%s: %v",
		orgID, repoID, attemptID, result, checkErr)

	cleanupErr := RemovePublishAttemptReferences(database, orgID, attemptID, staged)
	if cleanupErr != nil {
		metrics.PublishAttemptRollbackTotal.WithLabelValues("error").Inc()
		denied := publishAuthorityDeniedError(outcome, checkErr)
		return errors.Join(denied, fmt.Errorf("rollback staged publish-attempt refs for %s: %w", attemptID, cleanupErr))
	}
	metrics.PublishAttemptRollbackTotal.WithLabelValues("success").Inc()
	return publishAuthorityDeniedError(outcome, checkErr)
}

func publishAuthorityDeniedError(outcome BlockPublishAuthorityOutcome, checkErr error) error {
	if checkErr != nil {
		if errors.Is(checkErr, ErrBlockPublishAuthorityDenied) {
			return checkErr
		}
		return fmt.Errorf("%w: %s: %w", ErrBlockPublishAuthorityDenied, outcome.String(), checkErr)
	}
	return fmt.Errorf("%w: %s", ErrBlockPublishAuthorityDenied, outcome.String())
}
