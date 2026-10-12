package v2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

// Read local settled observations first; only a clean absence consults the
// cross-DC domain. Transport errors and contradictory data fail closed. This
// scoped policy neither acquires own liveness nor grants mapping authority.
func readHistoricalAdmissionRow(read func(gocql.Consistency) error) error {
	err := read(gocql.LocalQuorum)
	if !errors.Is(err, gocql.ErrNotFound) {
		return err
	}
	return read(gocql.EachQuorum)
}

// The five new admission reads share this policy. The existing final exact-P
// authority check remains required before every HEAD, against captured P.
func (h *FSHelper) captureRetainedHistoricalFile(orgID, repoID string, entry FSEntry) ([]commitBlockPlacement, error) {
	var kind *string
	var size *int64
	var ids []string
	// Pointer destinations distinguish NULL from explicit zero, as the shared
	// immutable-source reader does, without changing its global read policy.
	if err := readHistoricalAdmissionRow(func(consistency gocql.Consistency) error {
		kind, size, ids = nil, nil, nil
		if err := h.db.Session().Query(`SELECT obj_type, block_ids, size_bytes FROM fs_objects WHERE library_id = ? AND fs_id = ?`, repoID, entry.ID).Consistency(consistency).Scan(&kind, &ids, &size); err != nil {
			return err
		}
		// Missing immutable fields are absence, not zero/default metadata. Verify in
		// the cross-DC domain before rejecting a locally partial source row.
		if kind == nil || size == nil || (*size > 0 && len(ids) == 0) {
			return gocql.ErrNotFound
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("read historical file: %w", err)
	}
	if kind == nil || *kind != "file" || size == nil || *size < 0 || *size != entry.Size || (*size > 0 && len(ids) == 0) {
		return nil, fmt.Errorf("historical file layout is incomplete")
	}
	resolved, err := h.resolveHistoricalBlockIDs(orgID, repoID, ids)
	if err != nil {
		return nil, fmt.Errorf("resolve historical blocks: %w", err)
	}
	placements := make([]commitBlockPlacement, 0, len(resolved))
	for _, id := range db.NormalizeBlockIDs(resolved) {
		placements = append(placements, commitBlockPlacement{blockID: id})
	}
	if err := h.requireHistoricalFileReferences(orgID, repoID, entry.ID, placements); err != nil {
		return nil, err
	}
	// Capture the already resolved set once. Calling the copy adapter here would
	// re-resolve representation/mappings with its unrelated local read policy.
	err = runBoundedPublicationChecks(len(placements), func(ctx context.Context, i int) error {
		block := &placements[i]
		if err := readHistoricalAdmissionRow(func(consistency gocql.Consistency) error {
			var class, key *string
			if err := h.db.Session().Query(`SELECT storage_class, storage_key FROM blocks WHERE org_id = ? AND block_id = ?`, orgID, block.blockID).WithContext(ctx).Consistency(consistency).Scan(&class, &key); err != nil {
				return err
			}
			if class == nil || key == nil {
				return gocql.ErrNotFound
			}
			block.storageClass, block.storageKey = *class, *key
			return nil
		}); err != nil {
			return fmt.Errorf("read historical physical life: %w", err)
		}
		if !db.IsSHA256BlockID(block.blockID) || !config.IsCanonicalStorageClassName(block.storageClass) || block.storageKey == "" || strings.TrimSpace(block.storageKey) != block.storageKey {
			return fmt.Errorf("%w: incomplete historical physical life", db.ErrBlockMetadataPermanent)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return placements, nil
}

// Preserve representation validation and strict all-or-error ID resolution;
// only this resurrection adapter changes the reads' consistency domain.
func (h *FSHelper) resolveHistoricalBlockIDs(orgID, repoID string, ids []string) ([]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var encrypted *bool
	var stored *string
	var deletedAt *time.Time
	if err := readHistoricalAdmissionRow(func(consistency gocql.Consistency) error {
		encrypted, stored, deletedAt = nil, nil, nil
		if err := h.db.Session().Query(`SELECT encrypted, block_representation_id, deleted_at FROM libraries WHERE org_id = ? AND library_id = ?`, orgID, repoID).Consistency(consistency).Scan(&encrypted, &stored, &deletedAt); err != nil {
			return err
		}
		if encrypted == nil {
			return gocql.ErrNotFound
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if deletedAt != nil && !deletedAt.IsZero() {
		return nil, db.ErrLibraryDeleted
	}
	// Preserve the existing legacy representation default only when encryption
	// state is present; a missing encrypted flag must not default to plaintext.
	storedValue := ""
	if stored != nil {
		storedValue = *stored
	}
	representation, err := db.CanonicalBlockRepresentationIDForLibrary(repoID, *encrypted, storedValue)
	if err != nil {
		return nil, err
	}
	resolved := make([]string, len(ids))
	err = runBoundedPublicationChecks(len(ids), func(ctx context.Context, i int) error {
		id := db.NormalizeBlockID(ids[i])
		if db.IsSHA256BlockID(id) {
			resolved[i] = id
			return nil
		}
		if !db.IsSHA1BlockID(id) {
			return fmt.Errorf("invalid historical block id %q", ids[i])
		}
		var mapped string
		if err := readHistoricalAdmissionRow(func(consistency gocql.Consistency) error {
			var value *string
			if err := h.db.Session().Query(`SELECT internal_id FROM block_id_mappings WHERE org_id = ? AND representation_id = ? AND external_id = ?`, orgID, representation, id).WithContext(ctx).Consistency(consistency).Scan(&value); err != nil {
				return err
			}
			if value == nil {
				return gocql.ErrNotFound
			}
			mapped = *value
			return nil
		}); err != nil {
			return err
		}
		mapped = db.NormalizeBlockID(mapped)
		if !db.IsSHA256BlockID(mapped) {
			return fmt.Errorf("historical block mapping is incomplete")
		}
		resolved[i] = mapped
		return nil
	})
	if err != nil {
		return nil, err
	}
	return resolved, nil
}

func (h *FSHelper) requireHistoricalFileReferences(orgID, repoID, fsID string, placements []commitBlockPlacement) error {
	ref := db.BlockReferrerForFSObject(repoID, fsID)
	return runBoundedPublicationChecks(len(placements), func(ctx context.Context, i int) error {
		var observed string
		if err := readHistoricalAdmissionRow(func(consistency gocql.Consistency) error {
			return h.db.Session().Query(`SELECT referrer FROM block_references WHERE org_id = ? AND block_id = ? AND referrer = ?`, orgID, placements[i].blockID, ref).WithContext(ctx).Consistency(consistency).Scan(&observed)
		}); err != nil {
			return fmt.Errorf("historical file lacks settled permanent liveness: %w", err)
		}
		if observed != ref {
			return fmt.Errorf("historical reference observation is contradictory")
		}
		return nil
	})
}

// retainedHistoricalSource is the admission evidence for one restored entry:
// every file in it, with the exact placements captured for that file.
type retainedHistoricalSource struct {
	files []retainedHistoricalFile
}

type retainedHistoricalFile struct {
	fsID       string
	placements []commitBlockPlacement
}

// fs_ids are content-addressed, so a well-formed tree cannot nest this deep;
// the bound stops a corrupt (cyclic) tree from recursing forever.
const maxRetainedHistoricalDepth = 256

// captureRetainedHistoricalEntry applies the file admission to a whole restored
// entry. A directory is admitted only if every file in its historical subtree
// is admitted.
func (h *FSHelper) captureRetainedHistoricalEntry(orgID, repoID string, entry FSEntry) (retainedHistoricalSource, error) {
	var source retainedHistoricalSource
	err := h.collectRetainedHistoricalEntry(orgID, repoID, entry, 0, &source)
	return source, err
}

func (h *FSHelper) collectRetainedHistoricalEntry(orgID, repoID string, entry FSEntry, depth int, source *retainedHistoricalSource) error {
	if !isDirectoryEntry(&entry) {
		placements, err := h.captureRetainedHistoricalFile(orgID, repoID, entry)
		if err != nil {
			return err
		}
		source.files = append(source.files, retainedHistoricalFile{fsID: entry.ID, placements: placements})
		return nil
	}
	if depth >= maxRetainedHistoricalDepth {
		return fmt.Errorf("historical directory nesting exceeds %d", maxRetainedHistoricalDepth)
	}
	children, err := h.readRetainedHistoricalDirectory(repoID, entry.ID)
	if err != nil {
		return err
	}
	for _, child := range children {
		if err := h.collectRetainedHistoricalEntry(orgID, repoID, child, depth+1, source); err != nil {
			return err
		}
	}
	return nil
}

// Strict counterpart of GetDirectoryEntries for admission. That reader treats a
// missing object as an empty directory, which here would admit an unread tree.
func (h *FSHelper) readRetainedHistoricalDirectory(repoID, fsID string) ([]FSEntry, error) {
	var kind, entriesJSON *string
	if err := readHistoricalAdmissionRow(func(consistency gocql.Consistency) error {
		kind, entriesJSON = nil, nil
		if err := h.db.Session().Query(`SELECT obj_type, dir_entries FROM fs_objects WHERE library_id = ? AND fs_id = ?`, repoID, fsID).Consistency(consistency).Scan(&kind, &entriesJSON); err != nil {
			return err
		}
		if kind == nil {
			return gocql.ErrNotFound
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("read historical directory: %w", err)
	}
	return parseRetainedHistoricalDirectory(fsID, *kind, entriesJSON)
}

func parseRetainedHistoricalDirectory(fsID, kind string, entriesJSON *string) ([]FSEntry, error) {
	if kind != "dir" {
		return nil, fmt.Errorf("historical directory %s has object type %q", fsID, kind)
	}
	raw := ""
	if entriesJSON != nil {
		raw = strings.TrimSpace(*entriesJSON)
	}
	// An empty directory is stored as "", "[]" or "null" (a marshalled nil slice).
	if raw == "" || raw == "[]" || raw == "null" {
		return nil, nil
	}
	var entries []FSEntry
	if err := json.Unmarshal([]byte(raw), &entries); err != nil {
		return nil, fmt.Errorf("historical directory %s is malformed: %w", fsID, err)
	}
	for _, entry := range entries {
		if strings.TrimSpace(entry.ID) == "" {
			return nil, fmt.Errorf("historical directory %s has an entry without fs_id", fsID)
		}
	}
	return entries, nil
}

// validateRetainedHistoricalSource rechecks every captured file immediately
// before each HEAD: its own permanent references, then one exact-P authority
// pass over the union of placements. Unknown, changed and claimed lives fail
// closed via the existing authority primitive. The fallback does not close
// observation-to-HEAD continuity.
func (h *FSHelper) validateRetainedHistoricalSource(orgID, repoID string, source retainedHistoricalSource) error {
	var placements []commitBlockPlacement
	seen := make(map[commitBlockPlacement]bool)
	for _, file := range source.files {
		if err := h.requireHistoricalFileReferences(orgID, repoID, file.fsID, file.placements); err != nil {
			return err
		}
		for _, placement := range file.placements {
			if !seen[placement] {
				seen[placement] = true
				placements = append(placements, placement)
			}
		}
	}
	return (&FileHandler{db: h.db}).validateCommitBlockPublicationFences(orgID, placements)
}
