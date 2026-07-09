package db

import (
	"fmt"
	"strings"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/google/uuid"
)

const PlainBlockRepresentationID = "plain:v1"

// NormalizeBlockID canonicalizes a hex block identifier (external SHA-1 or internal
// SHA-256) to trimmed lowercase. Hex is case-insensitive, so without this the same
// content-address could land in two different partition keys or miss a lookup purely
// on letter case. Server-derived IDs are already lowercase; applying this at every
// mapping read/write/delete and on blocks.sha1 keeps the canonicalization consistent
// (and defensive if a non-server-derived uppercase id ever reaches these paths).
func NormalizeBlockID(id string) string {
	return strings.ToLower(strings.TrimSpace(id))
}

// IsSHA1BlockID reports whether id is a 40-char hex external SHA-1 block id, and
// IsSHA256BlockID whether it is a 64-char hex internal content address. Both
// validate hex CONTENT, not just length, so a 40/64-char non-hex string is
// rejected. Callers should normalize with NormalizeBlockID first.
func IsSHA1BlockID(id string) bool { return isHexN(id, 40) }

func IsSHA256BlockID(id string) bool { return isHexN(id, 64) }

func EncryptedLibraryBlockRepresentationID(libraryID string) string {
	libraryID = strings.TrimSpace(libraryID)
	if libraryID == "" {
		return ""
	}
	if parsed, err := uuid.Parse(libraryID); err == nil {
		libraryID = parsed.String()
	}
	return "library:" + libraryID
}

func EffectiveBlockRepresentationID(libraryID string, encrypted bool, stored string) string {
	stored = strings.TrimSpace(stored)
	if stored != "" {
		return stored
	}
	if encrypted {
		return EncryptedLibraryBlockRepresentationID(libraryID)
	}
	return PlainBlockRepresentationID
}

func ValidateBlockRepresentationID(representationID string) error {
	if strings.TrimSpace(representationID) == "" {
		return fmt.Errorf("missing block representation id")
	}
	return nil
}

// IsCanonicalBlockRepresentationID reports whether representationID is one of the
// exact forms this system mints: the plaintext default "plain:v1", or a per-library
// encrypted id "library:<uuid>". A non-empty value that matches neither indicates a
// corrupt/foreign id that would resolve mappings in a nonexistent namespace, so
// callers that require a *usable* representation validate format, not just presence.
func IsCanonicalBlockRepresentationID(representationID string) bool {
	representationID = strings.TrimSpace(representationID)
	if representationID == PlainBlockRepresentationID {
		return true
	}
	_, ok := parseCanonicalLibraryBlockRepresentationID(representationID)
	return ok
}

func IsCanonicalBlockRepresentationForLibrary(representationID string, libraryID uuid.UUID) bool {
	representationID = strings.TrimSpace(representationID)
	if representationID == PlainBlockRepresentationID {
		return true
	}
	parsedLibraryID, ok := parseCanonicalLibraryBlockRepresentationID(representationID)
	return ok && parsedLibraryID == libraryID
}

func parseCanonicalLibraryBlockRepresentationID(representationID string) (uuid.UUID, bool) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(representationID), "library:")
	if !ok {
		return uuid.Nil, false
	}
	parsed, err := uuid.Parse(rest)
	if err != nil || parsed.String() != rest {
		return uuid.Nil, false
	}
	return parsed, true
}

func ResolveBlockRepresentationID(session *gocql.Session, orgID, libraryID string) (string, error) {
	state, err := ReadLiveLibraryState(session, orgID, libraryID)
	if err != nil {
		return "", err
	}
	return state.BlockRepresentationIDOrDefault(), nil
}

func ResolveBlockRepresentationIDByLibraryID(session *gocql.Session, libraryID string) (string, error) {
	state, err := ResolveLiveLibraryStateByID(session, libraryID)
	if err != nil {
		return "", err
	}
	return state.BlockRepresentationIDOrDefault(), nil
}

// ResolveBlockRepresentationIDForDelete resolves the effective block
// representation for a library that is being soft- or permanently deleted.
// Unlike ResolveBlockRepresentationID it reads the row even when deleted_at is
// already set (a permanent delete acts on a library that is already in trash),
// so callers can stamp block_representation_id onto the deleted_libraries GC
// marker before the libraries row disappears. GC relies on that stamp to purge
// the library later; without it the cascade cannot resolve the SHA-1 mapping
// domain once the live row is gone.
func ResolveBlockRepresentationIDForDelete(session *gocql.Session, orgID, libraryID string) (string, error) {
	state, err := ReadLibraryState(session, orgID, libraryID)
	if err != nil {
		return "", err
	}
	return state.BlockRepresentationIDOrDefault(), nil
}
