package api

import (
	"fmt"

	"github.com/Sesame-Disk/sesamefs/internal/db"
)

type classifiedClientBlockID struct {
	normalized     string
	isLegacySHA1   bool
	isDirectSHA256 bool
}

// classifyClientReadableBlockID enforces the client-facing block-id contract:
// only a hex SHA-1 or hex SHA-256 is accepted on read/lookup surfaces. GC keeps
// its own lenient resolver for damaged metadata.
func classifyClientReadableBlockID(blockID string) (classifiedClientBlockID, error) {
	normalized := db.NormalizeBlockID(blockID)
	switch {
	case db.IsSHA1BlockID(normalized):
		return classifiedClientBlockID{normalized: normalized, isLegacySHA1: true}, nil
	case db.IsSHA256BlockID(normalized):
		return classifiedClientBlockID{normalized: normalized, isDirectSHA256: true}, nil
	default:
		return classifiedClientBlockID{}, fmt.Errorf("invalid block id")
	}
}

// classifySyncUploadBlockID enforces the upload contract before reading the body:
// the declared external id must be a valid hex SHA-1 or SHA-256. If hash_type is
// omitted we infer from the id; if it is provided it must be exactly "sha256" and
// must carry a SHA-256 id.
func classifySyncUploadBlockID(blockID, hashType string) (classifiedClientBlockID, error) {
	normalized := db.NormalizeBlockID(blockID)
	switch {
	case hashType != "" && hashType != "sha256":
		return classifiedClientBlockID{}, fmt.Errorf("invalid hash type")
	case hashType == "sha256":
		if !db.IsSHA256BlockID(normalized) {
			return classifiedClientBlockID{}, fmt.Errorf("invalid block id")
		}
		return classifiedClientBlockID{normalized: normalized, isDirectSHA256: true}, nil
	case db.IsSHA1BlockID(normalized):
		return classifiedClientBlockID{normalized: normalized, isLegacySHA1: true}, nil
	case db.IsSHA256BlockID(normalized):
		return classifiedClientBlockID{normalized: normalized, isDirectSHA256: true}, nil
	default:
		return classifiedClientBlockID{}, fmt.Errorf("invalid block id")
	}
}

// normalizeResolvedInternalBlockID validates DB-returned mapping targets before
// they are handed to storage, so corrupted/manual rows fail closed.
func normalizeResolvedInternalBlockID(blockID string) (string, error) {
	normalized := db.NormalizeBlockID(blockID)
	if !db.IsSHA256BlockID(normalized) {
		return "", fmt.Errorf("invalid internal block id")
	}
	return normalized, nil
}
