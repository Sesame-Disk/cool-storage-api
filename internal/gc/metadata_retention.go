package gc

import (
	"errors"
	"fmt"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/google/uuid"
)

// Read-only measurement of fs_object retention in live libraries
// (ISSUE-GC-LIVE-LIBRARY-METADATA-RECLAMATION-01, docs/GC-LIVE-LIBRARY-RECLAMATION.md).
// Phase 6 is suspended and Phase 5 retirement is metadata-only, so nothing
// reclaims fs_objects outside HEAD's chain in a live library. This reports how
// much that is; it never deletes, enqueues or writes anything.

// emptyDirFSID is Seafile's id for an empty directory. It is never stored as a
// row, so tree entries naming it are not dangling.
const emptyDirFSID = "0000000000000000000000000000000000000000"

// ErrLibraryNotLive reports a library without a canonical row or with deleted_at
// set. Deleted libraries are reclaimed by the library cascade, not measured here.
var ErrLibraryNotLive = errors.New("library is not live")

// FSObjectRetentionRow is one fs_object row with the columns the report needs.
type FSObjectRetentionRow struct {
	FSID       string
	ObjType    string
	BlockIDs   []string
	DirEntries []string
	SizeBytes  int64
}

// MetadataRetentionReader contains read methods only, so the measurement cannot
// mutate state by construction.
type MetadataRetentionReader interface {
	// GetLibraryHeadForRetention reads the canonical libraries row: its HEAD and
	// whether deleted_at is set. found is false when the row does not exist.
	GetLibraryHeadForRetention(orgID, libraryID uuid.UUID) (headCommitID string, deleted bool, found bool, err error)
	ListCommitsWithTimestamps(libraryID uuid.UUID) ([]CommitWithTimestamp, error)
	ListFSObjectRetentionRows(libraryID uuid.UUID) ([]FSObjectRetentionRow, error)
}

// MetadataRetentionLister adds the read-only library enumeration used by the
// report command.
type MetadataRetentionLister interface {
	MetadataRetentionReader
	ListDistinctFSObjectLibraries() ([]uuid.UUID, error)
	FindOrgForLibrary(libraryID uuid.UUID) (uuid.UUID, error)
}

var _ MetadataRetentionLister = (*CassandraStore)(nil)

// LibraryMetadataRetention is the measurement for one live library. The retained
// fields cover every fs_object outside HEAD's commit chain: the upper bound any
// reclamation could touch. Bytes are logical (size_bytes), not physical.
type LibraryMetadataRetention struct {
	OrgID                  uuid.UUID `json:"org_id"`
	LibraryID              uuid.UUID `json:"library_id"`
	HeadCommitID           string    `json:"head_commit_id"`
	Commits                int       `json:"commits"`
	HeadChainCommits       int       `json:"head_chain_commits"`
	OffChainCommits        int       `json:"off_chain_commits"`
	FSObjects              int       `json:"fs_objects"`
	ReachableFromHeadChain int       `json:"reachable_from_head_chain"`
	ReachableOnlyOffChain  int       `json:"reachable_only_off_chain"`
	Unreferenced           int       `json:"unreferenced"`
	RetainedFiles          int       `json:"retained_files"`
	RetainedLogicalBytes   int64     `json:"retained_logical_bytes"`
	RetainedFSRefs         int       `json:"retained_fs_refs"`
	DanglingReferences     int       `json:"dangling_references"`
	Complete               bool      `json:"complete"`
	Error                  string    `json:"error,omitempty"`
}

// MeasureLibraryMetadataRetention measures one live library from three reads:
// the canonical HEAD, the commit partition and the fs_object partition. It
// returns ErrLibraryNotLive for a missing or deleted library. Any other failure,
// including a HEAD commit with no row, yields Complete=false and the error; no
// partial numbers are reported as complete. The reads are not one consistent cut,
// so a concurrent publication can move objects between categories.
func MeasureLibraryMetadataRetention(r MetadataRetentionReader, orgID, libraryID uuid.UUID) (LibraryMetadataRetention, error) {
	report := LibraryMetadataRetention{OrgID: orgID, LibraryID: libraryID}
	fail := func(err error) (LibraryMetadataRetention, error) {
		report.Error = err.Error()
		return report, err
	}

	head, deleted, found, err := r.GetLibraryHeadForRetention(orgID, libraryID)
	if err != nil {
		return fail(fmt.Errorf("read library head: %w", err))
	}
	if !found || deleted {
		return report, ErrLibraryNotLive
	}
	report.HeadCommitID = head

	commits, err := r.ListCommitsWithTimestamps(libraryID)
	if err != nil {
		return fail(fmt.Errorf("list commits: %w", err))
	}
	rows, err := r.ListFSObjectRetentionRows(libraryID)
	if err != nil {
		return fail(fmt.Errorf("list fs_objects: %w", err))
	}

	commitByID := make(map[string]CommitWithTimestamp, len(commits))
	for _, c := range commits {
		commitByID[c.CommitID] = c
	}
	if _, ok := commitByID[head]; !ok {
		return fail(fmt.Errorf("HEAD commit %q has no commit row", head))
	}
	headChain := make(map[string]bool)
	for current := head; current != "" && !headChain[current]; {
		c, ok := commitByID[current]
		if !ok {
			break // retired ancestor: the chain ends here
		}
		headChain[current] = true
		current = c.ParentID
	}

	rowByID := make(map[string]FSObjectRetentionRow, len(rows))
	for _, row := range rows {
		rowByID[row.FSID] = row
	}
	dangling := make(map[string]bool)
	walk := func(root string, reached map[string]bool) {
		stack := []string{root}
		for len(stack) > 0 {
			id := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if id == "" || id == emptyDirFSID || reached[id] {
				continue
			}
			row, ok := rowByID[id]
			if !ok {
				dangling[id] = true
				continue
			}
			reached[id] = true
			stack = append(stack, row.DirEntries...)
		}
	}
	fromHead := make(map[string]bool)
	fromAny := make(map[string]bool)
	for _, c := range commits {
		if headChain[c.CommitID] {
			walk(c.RootFSID, fromHead)
		}
		walk(c.RootFSID, fromAny)
	}

	report.Commits = len(commits)
	report.HeadChainCommits = len(headChain)
	report.OffChainCommits = len(commits) - len(headChain)
	report.FSObjects = len(rows)
	report.DanglingReferences = len(dangling)
	for _, row := range rows {
		switch {
		case fromHead[row.FSID]:
			report.ReachableFromHeadChain++
			continue
		case fromAny[row.FSID]:
			report.ReachableOnlyOffChain++
		default:
			report.Unreferenced++
		}
		if row.ObjType == "file" {
			report.RetainedFiles++
			report.RetainedLogicalBytes += row.SizeBytes
			distinct := make(map[string]bool, len(row.BlockIDs))
			for _, blockID := range row.BlockIDs {
				distinct[blockID] = true
			}
			report.RetainedFSRefs += len(distinct)
		}
	}
	report.Complete = true
	return report, nil
}

// MetadataRetentionScope narrows the report. Zero UUIDs mean any; MaxLibraries 0
// means no limit on live libraries measured.
type MetadataRetentionScope struct {
	OrgID        uuid.UUID
	LibraryID    uuid.UUID
	MaxLibraries int
}

// MetadataRetentionSummary totals one report run over the complete libraries.
type MetadataRetentionSummary struct {
	Summary                bool  `json:"summary"`
	LibrariesListed        int   `json:"libraries_listed"`
	LibrariesMeasured      int   `json:"libraries_measured"`
	LibrariesNotLive       int   `json:"libraries_not_live"`
	LibrariesIncomplete    int   `json:"libraries_incomplete"`
	FSObjects              int   `json:"fs_objects"`
	ReachableFromHeadChain int   `json:"reachable_from_head_chain"`
	ReachableOnlyOffChain  int   `json:"reachable_only_off_chain"`
	Unreferenced           int   `json:"unreferenced"`
	RetainedFiles          int   `json:"retained_files"`
	RetainedLogicalBytes   int64 `json:"retained_logical_bytes"`
	RetainedFSRefs         int   `json:"retained_fs_refs"`
	DanglingReferences     int   `json:"dangling_references"`
}

// CollectMetadataRetention enumerates libraries the way scanner Phases 3/4 do and
// emits one measurement per live library in scope. Libraries that cannot be
// resolved to an org, or whose measurement fails, are emitted as incomplete and
// counted; the run continues. The returned error covers the enumeration only.
func CollectMetadataRetention(r MetadataRetentionLister, scope MetadataRetentionScope, emit func(LibraryMetadataRetention)) (MetadataRetentionSummary, error) {
	summary := MetadataRetentionSummary{Summary: true}
	libraries := []uuid.UUID{scope.LibraryID}
	if scope.LibraryID == uuid.Nil {
		listed, err := r.ListDistinctFSObjectLibraries()
		if err != nil {
			return summary, fmt.Errorf("list libraries: %w", err)
		}
		libraries = listed
	}
	for _, libraryID := range libraries {
		if scope.MaxLibraries > 0 && summary.LibrariesMeasured+summary.LibrariesIncomplete >= scope.MaxLibraries {
			break
		}
		summary.LibrariesListed++
		orgID, err := r.FindOrgForLibrary(libraryID)
		if errors.Is(err, gocql.ErrNotFound) {
			// Listed from the same tables a moment ago: hard-deleted since.
			summary.LibrariesNotLive++
			continue
		}
		if err != nil || orgID == uuid.Nil {
			summary.LibrariesIncomplete++
			emit(LibraryMetadataRetention{OrgID: orgID, LibraryID: libraryID, Error: fmt.Sprintf("resolve org: %v", err)})
			continue
		}
		if scope.OrgID != uuid.Nil && orgID != scope.OrgID {
			continue
		}
		report, err := MeasureLibraryMetadataRetention(r, orgID, libraryID)
		if errors.Is(err, ErrLibraryNotLive) {
			summary.LibrariesNotLive++
			continue
		}
		emit(report)
		if !report.Complete {
			summary.LibrariesIncomplete++
			continue
		}
		summary.LibrariesMeasured++
		summary.FSObjects += report.FSObjects
		summary.ReachableFromHeadChain += report.ReachableFromHeadChain
		summary.ReachableOnlyOffChain += report.ReachableOnlyOffChain
		summary.Unreferenced += report.Unreferenced
		summary.RetainedFiles += report.RetainedFiles
		summary.RetainedLogicalBytes += report.RetainedLogicalBytes
		summary.RetainedFSRefs += report.RetainedFSRefs
		summary.DanglingReferences += report.DanglingReferences
	}
	return summary, nil
}
