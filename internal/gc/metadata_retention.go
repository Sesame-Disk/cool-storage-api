package gc

import (
	"errors"
	"fmt"

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

// ErrLibraryWithoutHead reports a canonical row with no HEAD (for example the
// ghost row of ISSUE-PCD1B-CONTINUITY-LWT-GHOST-ROW-01). GC treats such a row as
// canonically absent, so it is counted separately and not measured.
var ErrLibraryWithoutHead = errors.New("library has no HEAD")

// FSObjectRetentionRow is one fs_object row with the columns the report needs.
type FSObjectRetentionRow struct {
	FSID       string
	ObjType    string
	BlockIDs   []string
	DirEntries []string
	SizeBytes  int64
}

// LibraryRef names a library and the org recorded for it in libraries_by_id.
type LibraryRef struct {
	OrgID     uuid.UUID
	LibraryID uuid.UUID
}

// MetadataRetentionReader contains read methods only, so the measurement cannot
// mutate state by construction.
type MetadataRetentionReader interface {
	// GetLibraryHeadForRetention reads the canonical libraries row: its HEAD and
	// whether deleted_at is set. found is false when the row does not exist.
	GetLibraryHeadForRetention(orgID, libraryID uuid.UUID) (headCommitID string, deleted bool, found bool, err error)
	ListCommitsWithTimestamps(libraryID uuid.UUID) ([]CommitWithTimestamp, error)
	// ListFSObjectRetentionRows fails on a row it cannot decode instead of
	// dropping its entries, so a corrupt tree is never measured as complete.
	ListFSObjectRetentionRows(libraryID uuid.UUID) ([]FSObjectRetentionRow, error)
}

// MetadataRetentionLister adds the read-only library enumeration used by the
// report command.
type MetadataRetentionLister interface {
	MetadataRetentionReader
	// ListLibrariesForRetention lists libraries with their org: the one row for
	// libraryID when it is set (none if absent), else the org's libraries when
	// orgID is set, else all of them. Hard-deleted libraries are not listed;
	// they are not live. Callers still filter by org.
	ListLibrariesForRetention(orgID, libraryID uuid.UUID) ([]LibraryRef, error)
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
	HeadDanglingReferences int       `json:"head_dangling_references"`
	Complete               bool      `json:"complete"`
	Error                  string    `json:"error,omitempty"`
}

// MeasureLibraryMetadataRetention measures one live library from three reads:
// the canonical HEAD, the commit partition and the fs_object partition. It
// returns ErrLibraryNotLive for a missing or deleted library and
// ErrLibraryWithoutHead for a HEAD-less row. Any other failure, including a HEAD
// commit with no row or an undecodable fs_object, yields Complete=false and the
// error; no partial numbers are reported as complete. The reads are not one
// consistent cut, so a concurrent publication can move objects between categories.
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
	if head == "" {
		return report, ErrLibraryWithoutHead
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
	headDangling := make(map[string]bool)
	dangling := make(map[string]bool)
	walk := func(root string, reached, dangling map[string]bool) {
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
	for _, c := range commits {
		if headChain[c.CommitID] {
			walk(c.RootFSID, fromHead, headDangling)
		}
	}
	fromAny := make(map[string]bool, len(fromHead))
	for id := range fromHead {
		fromAny[id] = true
	}
	for _, c := range commits {
		if !headChain[c.CommitID] {
			walk(c.RootFSID, fromAny, dangling)
		}
	}
	for id := range headDangling {
		dangling[id] = true
	}

	report.Commits = len(commits)
	report.HeadChainCommits = len(headChain)
	report.OffChainCommits = len(commits) - len(headChain)
	report.FSObjects = len(rows)
	report.DanglingReferences = len(dangling)
	report.HeadDanglingReferences = len(headDangling)
	distinct := make(map[string]bool)
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
		// An empty obj_type is read as a file elsewhere (api/v2 fs helpers).
		if row.ObjType == "file" || row.ObjType == "" {
			report.RetainedFiles++
			report.RetainedLogicalBytes += row.SizeBytes
			clear(distinct)
			for _, blockID := range row.BlockIDs {
				distinct[blockID] = true
			}
			report.RetainedFSRefs += len(distinct)
		}
	}
	if len(headDangling) > 0 {
		// A missing directory under HEAD hides its subtree, whose live rows would be
		// counted as retained: the classification is unreliable, so not complete.
		report.Error = fmt.Sprintf("HEAD's tree names %d fs_object(s) with no row; retained counts are unreliable", len(headDangling))
		return report, errors.New(report.Error)
	}
	report.Complete = true
	return report, nil
}

// MetadataRetentionScope narrows the report. Zero UUIDs mean any; MaxLibraries 0
// means no limit on the libraries measured or reported incomplete.
type MetadataRetentionScope struct {
	OrgID        uuid.UUID
	LibraryID    uuid.UUID
	MaxLibraries int
}

// MetadataRetentionSummary totals one report run over the complete libraries.
// LibrariesListed counts in-scope libraries visited; it equals measured +
// incomplete + not live + without HEAD.
type MetadataRetentionSummary struct {
	Summary                bool  `json:"summary"`
	LibrariesListed        int   `json:"libraries_listed"`
	LibrariesMeasured      int   `json:"libraries_measured"`
	LibrariesIncomplete    int   `json:"libraries_incomplete"`
	LibrariesNotLive       int   `json:"libraries_not_live"`
	LibrariesWithoutHead   int   `json:"libraries_without_head"`
	FSObjects              int   `json:"fs_objects"`
	ReachableFromHeadChain int   `json:"reachable_from_head_chain"`
	ReachableOnlyOffChain  int   `json:"reachable_only_off_chain"`
	Unreferenced           int   `json:"unreferenced"`
	RetainedFiles          int   `json:"retained_files"`
	RetainedLogicalBytes   int64 `json:"retained_logical_bytes"`
	RetainedFSRefs         int   `json:"retained_fs_refs"`
	DanglingReferences     int   `json:"dangling_references"`
}

// CollectMetadataRetention lists libraries in scope and emits one measurement per
// live library. A row with no org, or whose measurement fails, is emitted as
// incomplete and counted; the run continues. It stops at the first error emit
// returns (for example a failed output write) and returns it; otherwise the
// returned error covers the enumeration only.
func CollectMetadataRetention(r MetadataRetentionLister, scope MetadataRetentionScope, emit func(LibraryMetadataRetention) error) (MetadataRetentionSummary, error) {
	summary := MetadataRetentionSummary{Summary: true}
	refs, err := r.ListLibrariesForRetention(scope.OrgID, scope.LibraryID)
	if err != nil {
		return summary, fmt.Errorf("list libraries: %w", err)
	}
	for _, ref := range refs {
		if scope.OrgID != uuid.Nil && ref.OrgID != scope.OrgID {
			continue
		}
		if scope.MaxLibraries > 0 && summary.LibrariesMeasured+summary.LibrariesIncomplete >= scope.MaxLibraries {
			break
		}
		summary.LibrariesListed++
		if ref.OrgID == uuid.Nil {
			summary.LibrariesIncomplete++
			if err := emit(LibraryMetadataRetention{LibraryID: ref.LibraryID, Error: "libraries_by_id row has no org_id"}); err != nil {
				return summary, err
			}
			continue
		}
		report, err := MeasureLibraryMetadataRetention(r, ref.OrgID, ref.LibraryID)
		switch {
		case errors.Is(err, ErrLibraryNotLive):
			summary.LibrariesNotLive++
			continue
		case errors.Is(err, ErrLibraryWithoutHead):
			summary.LibrariesWithoutHead++
			continue
		}
		if err := emit(report); err != nil {
			return summary, err
		}
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
