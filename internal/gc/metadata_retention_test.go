package gc

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
)

var (
	_ MetadataRetentionReader = (*MockStore)(nil)
	_ MetadataRetentionLister = (*MockStore)(nil)
)

// retentionFixture builds one live library:
//
//	HEAD chain  h1 <- h2 (HEAD); off-chain commit "off" (parent h1)
//	r1: [d-shared]            d-shared: [f-shared]      f-shared: b1, 10B
//	r2: [d-shared, f-new, missing-head-child, <empty dir>]  f-new: b2, 20B
//	roff: [d-shared, f-off]   f-off: b3, b3, b4, 30B (two distinct refs)
//	unreferenced: o-dir [o-file, missing-orphan-child], o-file b5 40B,
//	              o-lone b6 b7 50B
func retentionFixture(t *testing.T) (*MockStore, uuid.UUID, uuid.UUID) {
	t.Helper()
	store := NewMockStore()
	orgID, libID := uuid.New(), uuid.New()
	store.AddOrganization(orgID)
	store.AddLibraryWithAutoDelete(orgID, libID, "hot", "h2", 0)
	now := time.Now()
	store.AddCommitWithDetails(libID, "h1", "r1", "", now.Add(-72*time.Hour))
	store.AddCommitWithDetails(libID, "h2", "r2", "h1", now)
	store.AddCommitWithDetails(libID, "off", "roff", "h1", now.Add(-48*time.Hour))
	store.AddFSObjectWithEntries(libID, "r1", "dir", nil, []string{"d-shared"})
	store.AddFSObjectWithEntries(libID, "r2", "dir", nil, []string{"d-shared", "f-new", "missing-head-child", emptyDirFSID})
	store.AddFSObjectWithEntries(libID, "d-shared", "dir", nil, []string{"f-shared"})
	store.AddFSObject(libID, "f-shared", "file", []string{"b1"})
	store.AddFSObject(libID, "f-new", "file", []string{"b2"})
	store.AddFSObjectWithEntries(libID, "roff", "dir", nil, []string{"d-shared", "f-off"})
	store.AddFSObject(libID, "f-off", "file", []string{"b3", "b3", "b4"})
	store.AddFSObjectWithEntries(libID, "o-dir", "dir", nil, []string{"o-file", "missing-orphan-child"})
	store.AddFSObject(libID, "o-file", "file", []string{"b5"})
	store.AddFSObject(libID, "o-lone", "file", []string{"b6", "b7"})
	for id, size := range map[string]int64{"f-shared": 10, "f-new": 20, "f-off": 30, "o-file": 40, "o-lone": 50} {
		store.SetFSObjectSizeForTest(libID, id, size)
	}
	return store, orgID, libID
}

func TestMeasureLibraryMetadataRetention_ClassifiesEveryObject(t *testing.T) {
	store, orgID, libID := retentionFixture(t)
	queueBefore := len(store.QueueItems(orgID))

	got, err := MeasureLibraryMetadataRetention(store, orgID, libID)
	if err != nil {
		t.Fatalf("measure: %v", err)
	}
	want := LibraryMetadataRetention{
		OrgID: orgID, LibraryID: libID, HeadCommitID: "h2",
		Commits: 3, HeadChainCommits: 2, OffChainCommits: 1,
		FSObjects:              10,
		ReachableFromHeadChain: 5, // r1, r2, d-shared, f-shared, f-new
		ReachableOnlyOffChain:  2, // roff, f-off
		Unreferenced:           3, // o-dir, o-file, o-lone
		RetainedFiles:          3, // f-off, o-file, o-lone
		RetainedLogicalBytes:   120,
		RetainedFSRefs:         5, // f-off {b3,b4}, o-file {b5}, o-lone {b6,b7}
		DanglingReferences:     1, // missing-head-child; the empty-dir id and unwalked orphans do not count
		Complete:               true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("retention:\n got %+v\nwant %+v", got, want)
	}
	if len(store.QueueItems(orgID)) != queueBefore {
		t.Fatal("the measurement must not enqueue anything")
	}
	if store.GetFSObj(libID, "o-lone") == nil || store.GetFSObj(libID, "f-off") == nil {
		t.Fatal("the measurement must not delete anything")
	}
}

// A retired ancestor ends the chain; its descendants on the chain stay counted.
func TestMeasureLibraryMetadataRetention_ChainEndsAtRetiredAncestor(t *testing.T) {
	store := NewMockStore()
	orgID, libID := uuid.New(), uuid.New()
	store.AddLibraryWithAutoDelete(orgID, libID, "hot", "h3", 0)
	store.AddCommitWithDetails(libID, "h3", "r3", "h2-retired", time.Now())
	store.AddCommitWithDetails(libID, "orphan-commit", "rx", "", time.Now())
	store.AddFSObjectWithEntries(libID, "r3", "dir", nil, nil)
	store.AddFSObjectWithEntries(libID, "rx", "dir", nil, nil)

	got, err := MeasureLibraryMetadataRetention(store, orgID, libID)
	if err != nil || !got.Complete || got.HeadChainCommits != 1 || got.OffChainCommits != 1 || got.ReachableFromHeadChain != 1 || got.ReachableOnlyOffChain != 1 {
		t.Fatalf("retired ancestor: %+v err=%v", got, err)
	}
}

func TestMeasureLibraryMetadataRetention_NotLive(t *testing.T) {
	store, orgID, libID := retentionFixture(t)
	if _, err := MeasureLibraryMetadataRetention(store, orgID, uuid.New()); !errors.Is(err, ErrLibraryNotLive) {
		t.Fatalf("missing library: err=%v, want ErrLibraryNotLive", err)
	}
	if _, err := MeasureLibraryMetadataRetention(store, uuid.New(), libID); !errors.Is(err, ErrLibraryNotLive) {
		t.Fatalf("wrong org: err=%v, want ErrLibraryNotLive", err)
	}
	if err := store.SoftDeleteLibrary(orgID, libID, uuid.Nil); err != nil {
		t.Fatal(err)
	}
	if _, err := MeasureLibraryMetadataRetention(store, orgID, libID); !errors.Is(err, ErrLibraryNotLive) {
		t.Fatalf("soft-deleted library: err=%v, want ErrLibraryNotLive", err)
	}
}

type failingRetentionRows struct {
	*MockStore
}

func (failingRetentionRows) ListFSObjectRetentionRows(uuid.UUID) ([]FSObjectRetentionRow, error) {
	return nil, errors.New("injected fs_object read failure")
}

// No partial numbers are ever reported as complete.
func TestMeasureLibraryMetadataRetention_IncompleteOnReadFailureOrMissingHead(t *testing.T) {
	store, orgID, libID := retentionFixture(t)
	got, err := MeasureLibraryMetadataRetention(failingRetentionRows{store}, orgID, libID)
	if err == nil || got.Complete || got.Error == "" || got.FSObjects != 0 {
		t.Fatalf("read failure must be incomplete with no counts: %+v err=%v", got, err)
	}

	missingHead, orgID2, libID2 := retentionFixture(t)
	missingHead.mu.Lock()
	missingHead.libraries[libID2].HeadCommitID = "h-without-row"
	missingHead.mu.Unlock()
	got, err = MeasureLibraryMetadataRetention(missingHead, orgID2, libID2)
	if err == nil || got.Complete || got.FSObjects != 0 {
		t.Fatalf("HEAD without a commit row must be incomplete: %+v err=%v", got, err)
	}
}

func TestCollectMetadataRetention_ScopesAndTotals(t *testing.T) {
	store, orgA, libA := retentionFixture(t)
	libA2 := uuid.New()
	store.AddLibraryWithAutoDelete(orgA, libA2, "hot", "c1", 0)
	store.AddCommitWithDetails(libA2, "c1", "root", "", time.Now())
	store.AddFSObjectWithEntries(libA2, "root", "dir", nil, nil)
	store.AddFSObject(libA2, "stray", "file", []string{"b9"})
	store.SetFSObjectSizeForTest(libA2, "stray", 7)
	orgB, libB := uuid.New(), uuid.New()
	store.AddLibraryWithAutoDelete(orgB, libB, "hot", "cb", 0)
	store.AddCommitWithDetails(libB, "cb", "rb", "", time.Now())
	store.AddFSObjectWithEntries(libB, "rb", "dir", nil, nil)
	libDeleted := uuid.New()
	store.AddLibraryWithAutoDelete(orgA, libDeleted, "hot", "cd", 0)
	if err := store.SoftDeleteLibrary(orgA, libDeleted, uuid.Nil); err != nil {
		t.Fatal(err)
	}

	collect := func(scope MetadataRetentionScope) (MetadataRetentionSummary, map[uuid.UUID]LibraryMetadataRetention) {
		t.Helper()
		seen := map[uuid.UUID]LibraryMetadataRetention{}
		summary, err := CollectMetadataRetention(store, scope, func(r LibraryMetadataRetention) { seen[r.LibraryID] = r })
		if err != nil {
			t.Fatalf("collect %+v: %v", scope, err)
		}
		return summary, seen
	}

	summary, seen := collect(MetadataRetentionScope{OrgID: orgA})
	if summary.LibrariesListed != 3 || summary.LibrariesMeasured != 2 || summary.LibrariesNotLive != 1 || summary.LibrariesIncomplete != 0 || len(seen) != 2 {
		t.Fatalf("org scope (listed counts in-scope libraries only): %+v seen=%d", summary, len(seen))
	}
	if _, ok := seen[libB]; ok {
		t.Fatal("org scope leaked another org's library")
	}
	if r := seen[libA]; !r.Complete || r.Unreferenced != 3 || r.RetainedLogicalBytes != 120 {
		t.Fatalf("org scope must measure the fixture library: %+v", r)
	}
	if summary.Unreferenced != 4 || summary.RetainedFiles != 4 || summary.RetainedLogicalBytes != 127 || summary.RetainedFSRefs != 6 {
		t.Fatalf("org totals: %+v", summary)
	}

	summary, seen = collect(MetadataRetentionScope{LibraryID: libB})
	if summary.LibrariesMeasured != 1 || len(seen) != 1 || seen[libB].FSObjects != 1 {
		t.Fatalf("library scope: %+v %+v", summary, seen)
	}

	summary, _ = collect(MetadataRetentionScope{MaxLibraries: 1})
	if summary.LibrariesMeasured+summary.LibrariesIncomplete != 1 {
		t.Fatalf("max-libraries: %+v", summary)
	}

	summary, seen = collect(MetadataRetentionScope{})
	if summary.LibrariesMeasured != 3 || summary.LibrariesNotLive != 1 || len(seen) != 3 {
		t.Fatalf("unscoped: %+v seen=%d", summary, len(seen))
	}

	// A HEAD-less canonical row (ghost row) is counted apart, not as incomplete;
	// a libraries_by_id row without an org is incomplete; a hard-deleted library
	// is not listed at all.
	libHeadless, libNoOrg := uuid.New(), uuid.New()
	store.AddLibraryWithAutoDelete(orgA, libHeadless, "hot", "", 0)
	store.AddLibraryWithAutoDelete(uuid.Nil, libNoOrg, "hot", "x", 0)
	if err := store.HardDeleteLibrary(orgA, libDeleted); err != nil {
		t.Fatal(err)
	}
	summary, seen = collect(MetadataRetentionScope{})
	if summary.LibrariesListed != 5 || summary.LibrariesMeasured != 3 || summary.LibrariesWithoutHead != 1 || summary.LibrariesIncomplete != 1 || summary.LibrariesNotLive != 0 {
		t.Fatalf("headless/org-less/hard-deleted: %+v", summary)
	}
	if r, ok := seen[libNoOrg]; !ok || r.Complete || r.Error == "" {
		t.Fatalf("org-less row must be emitted incomplete: %+v", r)
	}
	if _, ok := seen[libHeadless]; ok {
		t.Fatal("headless library must not be emitted as a measurement")
	}
}

func TestMeasureLibraryMetadataRetention_EmptyObjTypeIsAFile(t *testing.T) {
	store := NewMockStore()
	orgID, libID := uuid.New(), uuid.New()
	store.AddLibraryWithAutoDelete(orgID, libID, "hot", "c1", 0)
	store.AddCommitWithDetails(libID, "c1", "root", "", time.Now())
	store.AddFSObjectWithEntries(libID, "root", "dir", nil, nil)
	store.AddFSObject(libID, "untyped", "", []string{"b1"})
	store.SetFSObjectSizeForTest(libID, "untyped", 9)
	got, err := MeasureLibraryMetadataRetention(store, orgID, libID)
	if err != nil || got.RetainedFiles != 1 || got.RetainedLogicalBytes != 9 || got.RetainedFSRefs != 1 {
		t.Fatalf("empty obj_type must count as a retained file: %+v err=%v", got, err)
	}
}

func TestParseDirEntriesStrict(t *testing.T) {
	if ids, err := parseDirEntriesStrict(""); err != nil || ids != nil {
		t.Fatalf("empty: %v %v", ids, err)
	}
	if ids, err := parseDirEntriesStrict(`[{"id":"a"},{"id":""},{"id":"b"}]`); err != nil || !reflect.DeepEqual(ids, []string{"a", "b"}) {
		t.Fatalf("valid: %v %v", ids, err)
	}
	if _, err := parseDirEntriesStrict(`[{"id":"a"`); err == nil {
		t.Fatal("malformed dir_entries must be an error, not an empty directory")
	}
}
