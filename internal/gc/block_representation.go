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
	if !db.IsCanonicalBlockRepresentationID(representationID) {
		metrics.GCAuditEventsTotal.WithLabelValues("gc_library_representation_invalid").Inc()
		return "", fmt.Errorf("non-canonical block representation %q for library %s during %s", representationID, libraryID, source)
	}
	return representationID, nil
}
