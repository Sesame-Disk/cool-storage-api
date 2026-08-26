package gc

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/metrics"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/google/uuid"
)

type libraryBlockRepresentationResolver interface {
	GetLibraryBlockRepresentationID(orgID, libraryID uuid.UUID) (string, error)
}

func itemTypeRequiresBlockRepresentation(itemType ItemType) bool {
	switch itemType {
	case ItemCommit, ItemFSObject, ItemLibraryCascade:
		return true
	default:
		return false
	}
}

func queueItemRepresentationLibraryID(item QueueItem) (uuid.UUID, error) {
	switch item.ItemType {
	case ItemCommit, ItemFSObject:
		if item.LibraryID == uuid.Nil {
			return uuid.Nil, fmt.Errorf("gc: item type %s (%s) requires library_id", item.ItemType, item.ItemID)
		}
		return item.LibraryID, nil
	case ItemLibraryCascade:
		libraryID, err := uuid.Parse(strings.TrimSpace(item.ItemID))
		if err != nil {
			return uuid.Nil, fmt.Errorf("gc: item type %s has invalid library id %q: %w", item.ItemType, item.ItemID, err)
		}
		return libraryID, nil
	default:
		return uuid.Nil, nil
	}
}

// countLibraryRepresentationDrift emits the drift metric that matches a
// representation which just failed validateQueueItemBlockRepresentation: a blank
// value is "missing", anything else is "invalid" (non-canonical or wrong
// library). Scanner phases call this when they skip a library so the classify
// logic lives in one place instead of being copied at every skip site.
func countLibraryRepresentationDrift(blockRepresentationID string) {
	if strings.TrimSpace(blockRepresentationID) == "" {
		metrics.GCAuditEventsTotal.WithLabelValues("gc_library_representation_missing").Inc()
	} else {
		metrics.GCAuditEventsTotal.WithLabelValues("gc_library_representation_invalid").Inc()
	}
}

// countLibraryRepresentationDefaulted records that a scanned library had no
// stored block_representation_id and was processed under the safe derived default
// (plain:v1 / library:<id>). Deriving the representation from the library's own
// identity is correct, but an empty stored value means a writer/migration did not
// stamp it, so scanners surface it as drift instead of hiding it.
func countLibraryRepresentationDefaulted() {
	metrics.GCAuditEventsTotal.WithLabelValues("gc_library_representation_defaulted").Inc()
}

func validateQueueItemBlockRepresentation(item QueueItem) error {
	if !itemTypeRequiresBlockRepresentation(item.ItemType) {
		return nil
	}

	representationID := strings.TrimSpace(item.BlockRepresentationID)
	if representationID == "" {
		return fmt.Errorf("gc: item type %s (%s) requires a block representation", item.ItemType, item.ItemID)
	}
	if !db.IsCanonicalBlockRepresentationID(representationID) {
		return fmt.Errorf("gc: item type %s (%s) requires a canonical block representation, got %q", item.ItemType, item.ItemID, representationID)
	}

	libraryID, err := queueItemRepresentationLibraryID(item)
	if err != nil {
		return err
	}
	if !db.IsCanonicalBlockRepresentationForLibrary(representationID, libraryID) {
		return fmt.Errorf("gc: item type %s (%s) carries block representation %q for different library %s", item.ItemType, item.ItemID, representationID, libraryID)
	}
	return nil
}

func validateQueueItemBlockCandidateIdentity(item QueueItem) error {
	candidate := item.BlockGCCandidateIdentity
	if item.ItemType != ItemBlock {
		if !candidate.Target.IsZero() || !candidate.CandidateAt.IsZero() {
			return fmt.Errorf("gc: non-block item type %s (%s) must not carry a block candidate identity", item.ItemType, item.ItemID)
		}
		return nil
	}
	if candidate.Target.IsZero() || candidate.CandidateAt.IsZero() {
		return fmt.Errorf("gc: block item %s requires an exact block GC candidate identity", item.ItemID)
	}
	if !item.IdentityAt.Equal(candidate.CandidateAt) {
		return fmt.Errorf("gc: block item %s identity_at must equal candidate_at", item.ItemID)
	}
	return nil
}

// resolveRequiredLibraryBlockRepresentation resolves the canonical block
// representation for a library at enqueue time. It uses providedRepresentationID
// (already persisted on the queue item) when present, otherwise reads it from the
// live/soft-deleted library row. The result MUST be a non-empty, canonical id — a
// missing or malformed representation is a hard error so we never enqueue a
// commit/fs_object/library-cascade the resolver could later fail to map. This is
// deliberately fail-closed: on a clean deploy the representation is always
// resolvable at enqueue time (the library row still exists), so an unresolved id
// signals drift/corruption that must surface, not be papered over with a guess.
func resolveRequiredLibraryBlockRepresentation(resolver libraryBlockRepresentationResolver, orgID, libraryID uuid.UUID, providedRepresentationID, source string) (string, error) {
	representationID := strings.TrimSpace(providedRepresentationID)
	if representationID == "" {
		resolved, err := resolver.GetLibraryBlockRepresentationID(orgID, libraryID)
		if err != nil {
			if errors.Is(err, gocql.ErrNotFound) {
				metrics.GCAuditEventsTotal.WithLabelValues("gc_library_representation_missing").Inc()
				return "", fmt.Errorf("missing block representation for library %s during %s", libraryID, source)
			}
			return "", fmt.Errorf("resolve block representation for library %s during %s: %w", libraryID, source, err)
		}
		representationID = strings.TrimSpace(resolved)
	}

	if representationID == "" {
		metrics.GCAuditEventsTotal.WithLabelValues("gc_library_representation_missing").Inc()
		return "", fmt.Errorf("empty block representation for library %s during %s", libraryID, source)
	}
	if !db.IsCanonicalBlockRepresentationForLibrary(representationID, libraryID) {
		metrics.GCAuditEventsTotal.WithLabelValues("gc_library_representation_invalid").Inc()
		if !db.IsCanonicalBlockRepresentationID(representationID) {
			return "", fmt.Errorf("non-canonical block representation %q for library %s during %s", representationID, libraryID, source)
		}
		return "", fmt.Errorf("block representation %q does not belong to library %s during %s", representationID, libraryID, source)
	}
	return representationID, nil
}
