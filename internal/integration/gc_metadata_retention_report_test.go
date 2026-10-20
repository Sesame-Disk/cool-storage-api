//go:build integration

package integration

import (
	"net/http"
	"reflect"
	"testing"
	"time"

	apipkg "github.com/Sesame-Disk/sesamefs/internal/api"
	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
	gcpkg "github.com/Sesame-Disk/sesamefs/internal/gc"
	"github.com/google/uuid"
)

// The read-only metadata retention report (docs/GC-LIVE-LIBRARY-RECLAMATION.md)
// on real Cassandra after productive Sync writes. Counts are asserted as deltas
// against a measurement taken before the writes, so the library's creation
// commit does not matter. The report never writes, so this needs no GC isolation.
func TestGCMetadataRetentionReportMeasuresProductiveSyncState(t *testing.T) {
	requireCassandra(t)
	database := shareProjectionDBForTest(t)
	class := x1StorageClass(t)
	fx := &w2CreateFileFixture{w2UploadFileFixture: newW2UploadFileFixture(t, database, newBorrowedFSHeadHandler(t, database, class))}
	library := uuid.MustParse(fx.repoID)
	store := gcpkg.NewCassandraStore(database)
	handler := w24Handler(t, database, class)
	syncRef := dbpkg.BlockReferrerForUpload("sync:" + fx.repoID + ":" + fx.blockID)

	measure := func() gcpkg.LibraryMetadataRetention {
		t.Helper()
		r, err := gcpkg.MeasureLibraryMetadataRetention(store, fx.orgUUID, library)
		if err != nil || !r.Complete {
			t.Fatalf("measure: %+v err=%v", r, err)
		}
		return r
	}
	before := measure()

	sizedFile := func(blockID string, size int) ([]byte, string) {
		raw := mustMarshalSyncObjectForTest(t, map[string]interface{}{"block_ids": []string{blockID}, "size": size, "type": 1, "version": 1})
		return raw, syncSHA1HexForTest(raw)
	}
	dir := func(entries ...apipkg.FSEntry) ([]byte, string) {
		raw := mustMarshalSyncObjectForTest(t, map[string]interface{}{"dirents": entries, "type": 3, "version": 1})
		return raw, syncSHA1HexForTest(raw)
	}
	entry := func(id, name string, size int) apipkg.FSEntry {
		return apipkg.FSEntry{ID: id, Mode: 33188, Mtime: time.Now().Unix(), Name: name, Size: int64(size)}
	}
	commitPayload := func(commit, root, parent string) []byte {
		return mustMarshalSyncObjectForTest(t, map[string]interface{}{"commit_id": commit, "repo_id": fx.repoID, "root_id": root, "parent_id": parent, "description": "retention report fixture", "ctime": time.Now().Unix(), "version": 1})
	}
	recv := func(objects ...syncPackedFSObject) {
		e17OK(t, e17Request(fx, http.MethodPost, "/recv-fs", "", "", packSyncFSObjectsForTest(t, objects...), handler.RecvFS))
	}

	// C1 published as HEAD: R1 -> F1 (the fixture block).
	f1JSON, f1 := sizedFile(fx.sha1ID, len(fx.content))
	r1JSON, r1 := dir(entry(f1, fx.filename, len(fx.content)))
	c1 := syncSHA1HexForTest([]byte("retention-c1-" + uuid.NewString()))
	cleanupUploadedBlockArtifactsForTest(t, fx.orgID, fx.repoID, fx.blockID, fx.sha1ID, syncRef, dbpkg.BlockReferrerForFSObject(fx.repoID, f1))
	t.Cleanup(func() {
		for bucket := 0; bucket < dbpkg.PublishedBlockReferenceRepairBuckets; bucket++ {
			if err := database.Session().Query(`DELETE FROM published_block_reference_repairs WHERE bucket = ? AND org_id = ? AND repo_id = ?`, bucket, fx.orgID, fx.repoID).Exec(); err != nil {
				t.Error(err)
			}
		}
	})
	e17OK(t, e17Request(fx, http.MethodPut, "/commit/"+c1, "commit_id", c1, commitPayload(c1, r1, fx.headBefore), handler.PutCommit))
	recv(syncPackedFSObject{fsID: f1, jsonData: f1JSON}, syncPackedFSObject{fsID: r1, jsonData: r1JSON})
	e17OK(t, e17Request(fx, http.MethodPut, "/block/"+fx.sha1ID, "block_id", fx.sha1ID, fx.content, handler.PutBlock))
	e17OK(t, e17Request(fx, http.MethodPut, "/commit/HEAD?head="+c1, "commit_id", "HEAD", nil, handler.PutCommit))
	if head := borrowedFSReadHead(t, database, fx.orgID, fx.repoID); head != c1 {
		t.Fatalf("HEAD=%s want=%s", head, c1)
	}

	// C2 stored but never published: R2 -> {F1, F2}. F2 is only off-chain.
	const f2Size, orphanSize = 1234, 5678
	f2JSON, f2 := sizedFile(sha1hex([]byte("retention-f2-"+uuid.NewString())), f2Size)
	r2JSON, r2 := dir(entry(f1, fx.filename, len(fx.content)), entry(f2, "off-chain.bin", f2Size))
	c2 := syncSHA1HexForTest([]byte("retention-c2-" + uuid.NewString()))
	e17OK(t, e17Request(fx, http.MethodPut, "/commit/"+c2, "commit_id", c2, commitPayload(c2, r2, c1), handler.PutCommit))
	recv(syncPackedFSObject{fsID: f2, jsonData: f2JSON}, syncPackedFSObject{fsID: r2, jsonData: r2JSON})

	// A received file that no commit references.
	orphanJSON, orphan := sizedFile(sha1hex([]byte("retention-orphan-"+uuid.NewString())), orphanSize)
	recv(syncPackedFSObject{fsID: orphan, jsonData: orphanJSON})

	refsBefore, err := database.ListBlockReferrers(fx.orgID, fx.blockID)
	if err != nil {
		t.Fatal(err)
	}
	queueBefore := len(phase6OwnedItems(t, store, fx.orgUUID, library, fx.blockID))

	after := measure()
	delta := gcpkg.LibraryMetadataRetention{
		Commits:                after.Commits - before.Commits,
		HeadChainCommits:       after.HeadChainCommits - before.HeadChainCommits,
		OffChainCommits:        after.OffChainCommits - before.OffChainCommits,
		FSObjects:              after.FSObjects - before.FSObjects,
		ReachableFromHeadChain: after.ReachableFromHeadChain - before.ReachableFromHeadChain,
		ReachableOnlyOffChain:  after.ReachableOnlyOffChain - before.ReachableOnlyOffChain,
		Unreferenced:           after.Unreferenced - before.Unreferenced,
		RetainedFiles:          after.RetainedFiles - before.RetainedFiles,
		RetainedLogicalBytes:   after.RetainedLogicalBytes - before.RetainedLogicalBytes,
		RetainedFSRefs:         after.RetainedFSRefs - before.RetainedFSRefs,
		DanglingReferences:     after.DanglingReferences - before.DanglingReferences,
	}
	want := gcpkg.LibraryMetadataRetention{
		Commits: 2, HeadChainCommits: 1, OffChainCommits: 1,
		FSObjects:              5, // R1, F1, R2, F2, orphan
		ReachableFromHeadChain: 2, // R1, F1
		ReachableOnlyOffChain:  2, // R2, F2
		Unreferenced:           1, // orphan
		RetainedFiles:          2, // F2, orphan
		RetainedLogicalBytes:   f2Size + orphanSize,
		RetainedFSRefs:         2,
	}
	if !reflect.DeepEqual(delta, want) {
		t.Fatalf("retention delta:\n got %+v\nwant %+v\nbefore %+v\nafter %+v", delta, want, before, after)
	}
	if after.HeadCommitID != c1 {
		t.Fatalf("report HEAD=%s want=%s", after.HeadCommitID, c1)
	}

	// The command's enumeration path, scoped to this library, reports the same.
	var emitted []gcpkg.LibraryMetadataRetention
	summary, err := gcpkg.CollectMetadataRetention(store, gcpkg.MetadataRetentionScope{LibraryID: library}, func(r gcpkg.LibraryMetadataRetention) error {
		emitted = append(emitted, r)
		return nil
	})
	if err != nil || summary.LibrariesMeasured != 1 || len(emitted) != 1 || !reflect.DeepEqual(emitted[0], after) {
		t.Fatalf("collect: summary=%+v emitted=%+v err=%v", summary, emitted, err)
	}
	// The org scope reads the org's canonical libraries partition. The org is
	// shared with other fixtures, so only require this library's exact report.
	var orgReport *gcpkg.LibraryMetadataRetention
	summary, err = gcpkg.CollectMetadataRetention(store, gcpkg.MetadataRetentionScope{OrgID: fx.orgUUID}, func(r gcpkg.LibraryMetadataRetention) error {
		if r.LibraryID == library {
			r := r
			orgReport = &r
		}
		return nil
	})
	if err != nil || orgReport == nil || !reflect.DeepEqual(*orgReport, after) || summary.LibrariesListed < 1 {
		t.Fatalf("org-scoped collect: summary=%+v report=%+v err=%v", summary, orgReport, err)
	}

	// Read-only: repeated measurements agree and nothing GC-visible changed.
	if again := measure(); !reflect.DeepEqual(again, after) {
		t.Fatalf("repeated measurement differs:\n%+v\n%+v", again, after)
	}
	refsAfter, err := database.ListBlockReferrers(fx.orgID, fx.blockID)
	if err != nil || !reflect.DeepEqual(refsAfter, refsBefore) {
		t.Fatalf("references changed: %v -> %v err=%v", refsBefore, refsAfter, err)
	}
	if got := len(phase6OwnedItems(t, store, fx.orgUUID, library, fx.blockID)); got != queueBefore {
		t.Fatalf("queue rows changed: %d -> %d", queueBefore, got)
	}
	t.Logf("EVIDENCE retention report: before=%+v after=%+v", before, after)
}
