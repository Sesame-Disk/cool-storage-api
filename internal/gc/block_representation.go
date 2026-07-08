package gc

import (
	"errors"
	"fmt"
	"strings"

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

func resolveRequiredLibraryBlockRepresentation(resolver libraryBlockRepresentationResolver, orgID, libraryID uuid.UUID, providedRepresentationID, source string, allowLegacyLookup bool) (string, error) {
	providedRepresentationID = strings.TrimSpace(providedRepresentationID)
	if providedRepresentationID != "" {
		return providedRepresentationID, nil
	}
	if allowLegacyLookup {
		metrics.GCAuditEventsTotal.WithLabelValues("gc_library_representation_legacy_lookup").Inc()
	}

	resolvedRepresentationID, err := resolver.GetLibraryBlockRepresentationID(orgID, libraryID)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			if allowLegacyLookup {
				metrics.GCAuditEventsTotal.WithLabelValues("gc_library_representation_missing").Inc()
				return "", nil
			}
			metrics.GCAuditEventsTotal.WithLabelValues("gc_library_representation_missing").Inc()
			return "", fmt.Errorf("missing block representation for library %s during %s", libraryID, source)
		}
		return "", fmt.Errorf("resolve block representation for library %s during %s: %w", libraryID, source, err)
	}

	resolvedRepresentationID = strings.TrimSpace(resolvedRepresentationID)
	if resolvedRepresentationID == "" {
		if allowLegacyLookup {
			metrics.GCAuditEventsTotal.WithLabelValues("gc_library_representation_missing").Inc()
			return "", nil
		}
		metrics.GCAuditEventsTotal.WithLabelValues("gc_library_representation_missing").Inc()
		return "", fmt.Errorf("empty block representation for library %s during %s", libraryID, source)
	}
	return resolvedRepresentationID, nil
}
