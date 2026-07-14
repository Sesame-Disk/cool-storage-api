package v2

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/metrics"
	"github.com/Sesame-Disk/sesamefs/internal/traffic"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func newDeleteTestContext(method, path string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, path, nil)
	return c, w
}

func decodeDeleteTestJSON(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var payload map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("response is not valid JSON: %v (body=%s)", err, w.Body.String())
	}
	return payload
}

func installDeleteHelperStubs(t *testing.T) {
	t.Helper()
	oldResolve := resolveDeleteBlockRepresentationFn
	oldCleanupLinks := cleanupLibraryLinksForDeleteFn
	oldHardDelete := hardDeleteLibraryRowsFn
	oldCleanupTags := cleanupAllLibraryTagsForDeleteFn
	oldDeleteStorageCounter := deleteLibraryStorageCounterForDeleteFn
	oldRunAsync := runAsyncLibraryDeleteSideEffectFn
	t.Cleanup(func() {
		resolveDeleteBlockRepresentationFn = oldResolve
		cleanupLibraryLinksForDeleteFn = oldCleanupLinks
		hardDeleteLibraryRowsFn = oldHardDelete
		cleanupAllLibraryTagsForDeleteFn = oldCleanupTags
		deleteLibraryStorageCounterForDeleteFn = oldDeleteStorageCounter
		runAsyncLibraryDeleteSideEffectFn = oldRunAsync
	})
	runAsyncLibraryDeleteSideEffectFn = func(fn func()) { fn() }
}

func TestPermanentDeleteResolvedRepo_FailClosedOnRepresentationError(t *testing.T) {
	installDeleteHelperStubs(t)

	var cleanupLinksCalled, hardDeleteCalled, cleanupTagsCalled, deleteCounterCalled int
	resolveDeleteBlockRepresentationFn = func(_ *db.DB, _, _ string) (string, error) {
		return "", errors.New("corrupt representation")
	}
	cleanupLibraryLinksForDeleteFn = func(_ *db.DB, _, _ string) error {
		cleanupLinksCalled++
		return nil
	}
	hardDeleteLibraryRowsFn = func(_ *db.DB, _, _, _, _ string, _ time.Time) error {
		hardDeleteCalled++
		return nil
	}
	cleanupAllLibraryTagsForDeleteFn = func(_ *db.DB, _ string) error {
		cleanupTagsCalled++
		return nil
	}
	deleteLibraryStorageCounterForDeleteFn = func(_ traffic.DBSession, _, _ string) error {
		deleteCounterCalled++
		return nil
	}

	before := testutil.ToFloat64(metrics.LibraryDeleteRepresentationResolutionFailures.WithLabelValues("permanent_delete"))
	h := &DeletedLibraryHandler{db: &db.DB{}}
	c, w := newDeleteTestContext(http.MethodDelete, "/api/v2.1/repos/deleted/repo-1/")

	h.permanentDeleteResolvedRepo(c, "org-1", "repo-1", "hot", time.Now())

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d, body=%s", w.Code, http.StatusInternalServerError, w.Body.String())
	}
	payload := decodeDeleteTestJSON(t, w)
	if payload["error"] != "failed to prepare library for permanent deletion" {
		t.Fatalf("error = %v, want %q", payload["error"], "failed to prepare library for permanent deletion")
	}
	if cleanupLinksCalled != 0 || hardDeleteCalled != 0 || cleanupTagsCalled != 0 || deleteCounterCalled != 0 {
		t.Fatalf("side effects ran despite fail-closed refusal: cleanupLinks=%d hardDelete=%d cleanupTags=%d deleteCounter=%d",
			cleanupLinksCalled, hardDeleteCalled, cleanupTagsCalled, deleteCounterCalled)
	}
	after := testutil.ToFloat64(metrics.LibraryDeleteRepresentationResolutionFailures.WithLabelValues("permanent_delete"))
	if after != before+1 {
		t.Fatalf("resolution failure metric = %v, want %v", after, before+1)
	}
}

func TestDeleteResolvedTrashLibrary_FailClosedOnRepresentationError(t *testing.T) {
	installDeleteHelperStubs(t)

	var cleanupLinksCalled, hardDeleteCalled int
	resolveDeleteBlockRepresentationFn = func(_ *db.DB, _, _ string) (string, error) {
		return "", errors.New("corrupt representation")
	}
	cleanupLibraryLinksForDeleteFn = func(_ *db.DB, _, _ string) error {
		cleanupLinksCalled++
		return nil
	}
	hardDeleteLibraryRowsFn = func(_ *db.DB, _, _, _, _ string, _ time.Time) error {
		hardDeleteCalled++
		return nil
	}

	before := testutil.ToFloat64(metrics.LibraryDeleteRepresentationResolutionFailures.WithLabelValues("org_delete_trash_library"))
	h := &OrgAdminHandler{db: &db.DB{}}
	c, w := newDeleteTestContext(http.MethodDelete, "/org/org-1/admin/trash-libraries/repo-1/")

	h.deleteResolvedTrashLibrary(c, "org-1", "repo-1", "hot", time.Now())

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d, body=%s", w.Code, http.StatusInternalServerError, w.Body.String())
	}
	payload := decodeDeleteTestJSON(t, w)
	if payload["error"] != "failed to prepare library for permanent deletion" {
		t.Fatalf("error = %v, want %q", payload["error"], "failed to prepare library for permanent deletion")
	}
	if cleanupLinksCalled != 0 || hardDeleteCalled != 0 {
		t.Fatalf("side effects ran despite fail-closed refusal: cleanupLinks=%d hardDelete=%d", cleanupLinksCalled, hardDeleteCalled)
	}
	after := testutil.ToFloat64(metrics.LibraryDeleteRepresentationResolutionFailures.WithLabelValues("org_delete_trash_library"))
	if after != before+1 {
		t.Fatalf("resolution failure metric = %v, want %v", after, before+1)
	}
}

func TestPermanentDeleteResolvedRepo_StampsResolvedRepresentationOnSuccess(t *testing.T) {
	installDeleteHelperStubs(t)

	tests := []struct {
		name      string
		resolved  string
		libraryID string
	}{
		{
			name:      "plaintext marker uses plain:v1",
			resolved:  db.PlainBlockRepresentationID,
			libraryID: uuid.NewString(),
		},
		{
			name:      "encrypted marker uses library:id",
			libraryID: uuid.NewString(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolved := tt.resolved
			if resolved == "" {
				resolved = db.EncryptedLibraryBlockRepresentationID(tt.libraryID)
			}

			var gotRepresentation string
			var gotWriterDeletedAt time.Time
			var cleanupLinksCalled, cleanupTagsCalled, deleteCounterCalled int
			resolveDeleteBlockRepresentationFn = func(_ *db.DB, _, _ string) (string, error) {
				return resolved, nil
			}
			cleanupLibraryLinksForDeleteFn = func(_ *db.DB, _, _ string) error {
				cleanupLinksCalled++
				return nil
			}
			hardDeleteLibraryRowsFn = func(_ *db.DB, _, _, _, blockRepresentationID string, deletedAt time.Time) error {
				gotRepresentation = blockRepresentationID
				gotWriterDeletedAt = deletedAt
				return nil
			}
			cleanupAllLibraryTagsForDeleteFn = func(_ *db.DB, _ string) error {
				cleanupTagsCalled++
				return nil
			}
			deleteLibraryStorageCounterForDeleteFn = func(_ traffic.DBSession, _, _ string) error {
				deleteCounterCalled++
				return nil
			}
			libEnq := &mockLibraryGCEnqueuer{}
			h := &DeletedLibraryHandler{
				db: &db.DB{},
				libHandler: &LibraryHandler{
					gcEnqueuer: libEnq,
				},
			}
			c, w := newDeleteTestContext(http.MethodDelete, "/api/v2.1/repos/deleted/"+tt.libraryID+"/")

			// A fixed, non-now deleted_at (the original trash time) so we can assert it is
			// threaded verbatim to the marker writer and the cascade — never reset to now().
			wantDeletedAt := time.Now().Add(-72 * time.Hour).UTC().Truncate(time.Millisecond)
			h.permanentDeleteResolvedRepo(c, "org-1", tt.libraryID, "hot", wantDeletedAt)

			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d, body=%s", w.Code, http.StatusOK, w.Body.String())
			}
			if gotRepresentation != resolved {
				t.Fatalf("hard-delete marker representation = %q, want %q", gotRepresentation, resolved)
			}
			if !gotWriterDeletedAt.Equal(wantDeletedAt) {
				t.Fatalf("writer deleted_at = %s, want the original trash time %s (must not be reset)", gotWriterDeletedAt, wantDeletedAt)
			}
			if cleanupLinksCalled != 1 || cleanupTagsCalled != 1 || deleteCounterCalled != 1 {
				t.Fatalf("expected one successful destructive pass, got cleanupLinks=%d cleanupTags=%d deleteCounter=%d",
					cleanupLinksCalled, cleanupTagsCalled, deleteCounterCalled)
			}
			// The permanent-delete path immediately queues the durable, Phase-13-deduplicated
			// library cascade (not a content-only accelerator) with the resolved representation
			// and the SAME original deleted_at identity Phase 13 would use.
			if len(libEnq.calls) != 1 {
				t.Fatalf("expected exactly one immediate library-cascade enqueue, got %#v", libEnq.calls)
			}
			if libEnq.calls[0].blockRepresentationID != resolved {
				t.Fatalf("cascade enqueue representation = %q, want %q", libEnq.calls[0].blockRepresentationID, resolved)
			}
			if !libEnq.calls[0].deletedAt.Equal(wantDeletedAt) {
				t.Fatalf("cascade enqueue deleted_at = %s, want %s (dedup identity must match Phase 13)", libEnq.calls[0].deletedAt, wantDeletedAt)
			}
		})
	}
}

func TestDeleteResolvedTrashLibrary_ImmediatelyEnqueuesLibraryCascadeOnSuccess(t *testing.T) {
	installDeleteHelperStubs(t)

	resolved := db.PlainBlockRepresentationID
	wantDeletedAt := time.Now().Add(-48 * time.Hour).UTC().Truncate(time.Millisecond)
	var gotRepresentation string
	var gotWriterDeletedAt time.Time
	var cleanupLinksCalled int
	resolveDeleteBlockRepresentationFn = func(_ *db.DB, _, _ string) (string, error) {
		return resolved, nil
	}
	cleanupLibraryLinksForDeleteFn = func(_ *db.DB, _, _ string) error {
		cleanupLinksCalled++
		return nil
	}
	hardDeleteLibraryRowsFn = func(_ *db.DB, _, _, _, blockRepresentationID string, deletedAt time.Time) error {
		gotRepresentation = blockRepresentationID
		gotWriterDeletedAt = deletedAt
		return nil
	}
	libEnq := &mockLibraryGCEnqueuer{}
	h := &OrgAdminHandler{db: &db.DB{}, gcEnqueuer: libEnq}
	c, w := newDeleteTestContext(http.MethodDelete, "/org/org-1/admin/trash-libraries/repo-1/")

	h.deleteResolvedTrashLibrary(c, "org-1", "repo-1", "hot", wantDeletedAt)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if cleanupLinksCalled != 1 {
		t.Fatalf("cleanup links called %d times, want 1", cleanupLinksCalled)
	}
	if gotRepresentation != resolved {
		t.Fatalf("hard-delete marker representation = %q, want %q", gotRepresentation, resolved)
	}
	if !gotWriterDeletedAt.Equal(wantDeletedAt) {
		t.Fatalf("writer deleted_at = %s, want %s", gotWriterDeletedAt, wantDeletedAt)
	}
	if len(libEnq.calls) != 1 {
		t.Fatalf("expected exactly one immediate library-cascade enqueue, got %#v", libEnq.calls)
	}
	if libEnq.calls[0].blockRepresentationID != resolved {
		t.Fatalf("cascade enqueue representation = %q, want %q", libEnq.calls[0].blockRepresentationID, resolved)
	}
	if !libEnq.calls[0].deletedAt.Equal(wantDeletedAt) {
		t.Fatalf("cascade enqueue deleted_at = %s, want %s", libEnq.calls[0].deletedAt, wantDeletedAt)
	}
}

func TestAdminCleanTrashLibraries_SkipsRepresentationFailuresWithoutSideEffects(t *testing.T) {
	installDeleteHelperStubs(t)

	var hardDeleteCalled, cleanupTagsCalled int
	resolveDeleteBlockRepresentationFn = func(_ *db.DB, _, libraryID string) (string, error) {
		if libraryID == "repo-bad" {
			return "", errors.New("corrupt representation")
		}
		return db.PlainBlockRepresentationID, nil
	}
	hardDeleteLibraryRowsFn = func(_ *db.DB, _, _, _, _ string, _ time.Time) error {
		hardDeleteCalled++
		return nil
	}
	cleanupAllLibraryTagsForDeleteFn = func(_ *db.DB, _ string) error {
		cleanupTagsCalled++
		return nil
	}
	libEnq := &mockLibraryGCEnqueuer{}
	h := &AdminHandler{db: &db.DB{}}

	cleaned, failed := h.processAdminTrashCandidates([]trashLibraryCandidate{{
		OrgID:        "org-1",
		LibraryID:    "repo-bad",
		StorageClass: "hot",
	}}, libEnq)

	if cleaned != 0 || failed != 1 {
		t.Fatalf("processAdminTrashCandidates() = (cleaned=%d, failed=%d), want (0, 1)", cleaned, failed)
	}
	if hardDeleteCalled != 0 || cleanupTagsCalled != 0 || len(libEnq.calls) != 0 {
		t.Fatalf("side effects ran for skipped library: hardDelete=%d cleanupTags=%d enqueues=%#v",
			hardDeleteCalled, cleanupTagsCalled, libEnq.calls)
	}
}

func TestAdminCleanTrashLibraries_BatchFailureDoesNotRunPostCommitSideEffects(t *testing.T) {
	installDeleteHelperStubs(t)

	var cleanupTagsCalled int
	resolveDeleteBlockRepresentationFn = func(_ *db.DB, _, _ string) (string, error) {
		return db.PlainBlockRepresentationID, nil
	}
	hardDeleteLibraryRowsFn = func(_ *db.DB, _, _, _, _ string, _ time.Time) error {
		return errors.New("batch exec failed")
	}
	cleanupAllLibraryTagsForDeleteFn = func(_ *db.DB, _ string) error {
		cleanupTagsCalled++
		return nil
	}
	libEnq := &mockLibraryGCEnqueuer{}
	h := &AdminHandler{db: &db.DB{}}

	cleaned, failed := h.processAdminTrashCandidates([]trashLibraryCandidate{{
		OrgID:        "org-1",
		LibraryID:    "repo-batch-fail",
		StorageClass: "hot",
	}}, libEnq)

	if cleaned != 0 || failed != 1 {
		t.Fatalf("processAdminTrashCandidates() = (cleaned=%d, failed=%d), want (0, 1)", cleaned, failed)
	}
	if cleanupTagsCalled != 0 || len(libEnq.calls) != 0 {
		t.Fatalf("post-commit side effects ran after failed hard delete: cleanupTags=%d enqueues=%#v",
			cleanupTagsCalled, libEnq.calls)
	}
}

func TestAdminCleanTrashLibraries_PartialSuccessLeavesBadLibraryUntouched(t *testing.T) {
	installDeleteHelperStubs(t)

	var hardDeleteCalls []string
	var cleanupTagCalls []string
	resolveDeleteBlockRepresentationFn = func(_ *db.DB, _, libraryID string) (string, error) {
		if libraryID == "repo-bad" {
			return "", errors.New("corrupt representation")
		}
		return db.PlainBlockRepresentationID, nil
	}
	hardDeleteLibraryRowsFn = func(_ *db.DB, _, libraryID, _, _ string, _ time.Time) error {
		hardDeleteCalls = append(hardDeleteCalls, libraryID)
		return nil
	}
	cleanupAllLibraryTagsForDeleteFn = func(_ *db.DB, libraryID string) error {
		cleanupTagCalls = append(cleanupTagCalls, libraryID)
		return nil
	}
	libEnq := &mockLibraryGCEnqueuer{}
	h := &AdminHandler{db: &db.DB{}}

	cleaned, failed := h.processAdminTrashCandidates([]trashLibraryCandidate{
		{OrgID: "org-1", LibraryID: "repo-good", StorageClass: "hot"},
		{OrgID: "org-1", LibraryID: "repo-bad", StorageClass: "hot"},
	}, libEnq)

	if cleaned != 1 || failed != 1 {
		t.Fatalf("processAdminTrashCandidates() = (cleaned=%d, failed=%d), want (1, 1)", cleaned, failed)
	}
	if strings.Join(hardDeleteCalls, ",") != "repo-good" {
		t.Fatalf("hard-delete calls = %#v, want only repo-good", hardDeleteCalls)
	}
	if strings.Join(cleanupTagCalls, ",") != "repo-good" {
		t.Fatalf("tag cleanup calls = %#v, want only repo-good", cleanupTagCalls)
	}
	if len(libEnq.calls) != 1 || libEnq.calls[0].libraryID != "repo-good" {
		t.Fatalf("library-cascade enqueue calls = %#v, want only repo-good", libEnq.calls)
	}
}

// The following tests cover the org-admin BULK clean-trash loop (processOrgTrashCandidates)
// with explicit candidates — the same coverage the flawed org-wide E2E attempted, but with
// no org-wide SELECT and no shared-org side effects, mirroring the admin-path tests above.

func TestProcessOrgTrashCandidates_SkipsRepresentationFailuresWithoutSideEffects(t *testing.T) {
	installDeleteHelperStubs(t)

	var cleanupLinksCalled, hardDeleteCalled int
	resolveDeleteBlockRepresentationFn = func(_ *db.DB, _, libraryID string) (string, error) {
		if libraryID == "repo-bad" {
			return "", errors.New("corrupt representation")
		}
		return db.PlainBlockRepresentationID, nil
	}
	cleanupLibraryLinksForDeleteFn = func(_ *db.DB, _, _ string) error {
		cleanupLinksCalled++
		return nil
	}
	hardDeleteLibraryRowsFn = func(_ *db.DB, _, _, _, _ string, _ time.Time) error {
		hardDeleteCalled++
		return nil
	}
	libEnq := &mockLibraryGCEnqueuer{}
	h := &OrgAdminHandler{db: &db.DB{}, gcEnqueuer: libEnq}

	cleaned, failed := h.processOrgTrashCandidates([]trashLibraryCandidate{{
		OrgID:        "org-1",
		LibraryID:    "repo-bad",
		StorageClass: "hot",
	}}, libEnq)

	if cleaned != 0 || failed != 1 {
		t.Fatalf("processOrgTrashCandidates() = (cleaned=%d, failed=%d), want (0, 1)", cleaned, failed)
	}
	if cleanupLinksCalled != 0 || hardDeleteCalled != 0 || len(libEnq.calls) != 0 {
		t.Fatalf("side effects ran for library skipped before hard-delete: cleanupLinks=%d hardDelete=%d enqueues=%#v",
			cleanupLinksCalled, hardDeleteCalled, libEnq.calls)
	}
}

func TestProcessOrgTrashCandidates_LinkCleanupFailureSkipsWithoutHardDelete(t *testing.T) {
	installDeleteHelperStubs(t)

	var hardDeleteCalled int
	resolveDeleteBlockRepresentationFn = func(_ *db.DB, _, _ string) (string, error) {
		return db.PlainBlockRepresentationID, nil
	}
	cleanupLibraryLinksForDeleteFn = func(_ *db.DB, _, _ string) error {
		return errors.New("link cleanup failed")
	}
	hardDeleteLibraryRowsFn = func(_ *db.DB, _, _, _, _ string, _ time.Time) error {
		hardDeleteCalled++
		return nil
	}
	libEnq := &mockLibraryGCEnqueuer{}
	h := &OrgAdminHandler{db: &db.DB{}, gcEnqueuer: libEnq}

	cleaned, failed := h.processOrgTrashCandidates([]trashLibraryCandidate{{
		OrgID:        "org-1",
		LibraryID:    "repo-links-fail",
		StorageClass: "hot",
	}}, libEnq)

	if cleaned != 0 || failed != 1 {
		t.Fatalf("processOrgTrashCandidates() = (cleaned=%d, failed=%d), want (0, 1)", cleaned, failed)
	}
	// A link-cleanup failure must skip THIS library (leaving its canonical row live and
	// restorable) without hard-deleting or enqueuing — never abort the whole batch.
	if hardDeleteCalled != 0 || len(libEnq.calls) != 0 {
		t.Fatalf("hard-delete/enqueue ran after link cleanup failed: hardDelete=%d enqueues=%#v",
			hardDeleteCalled, libEnq.calls)
	}
}

func TestProcessOrgTrashCandidates_HardDeleteFailureSkipsEnqueue(t *testing.T) {
	installDeleteHelperStubs(t)

	resolveDeleteBlockRepresentationFn = func(_ *db.DB, _, _ string) (string, error) {
		return db.PlainBlockRepresentationID, nil
	}
	cleanupLibraryLinksForDeleteFn = func(_ *db.DB, _, _ string) error {
		return nil
	}
	hardDeleteLibraryRowsFn = func(_ *db.DB, _, _, _, _ string, _ time.Time) error {
		return errors.New("batch exec failed")
	}
	libEnq := &mockLibraryGCEnqueuer{}
	h := &OrgAdminHandler{db: &db.DB{}, gcEnqueuer: libEnq}

	cleaned, failed := h.processOrgTrashCandidates([]trashLibraryCandidate{{
		OrgID:        "org-1",
		LibraryID:    "repo-batch-fail",
		StorageClass: "hot",
	}}, libEnq)

	if cleaned != 0 || failed != 1 {
		t.Fatalf("processOrgTrashCandidates() = (cleaned=%d, failed=%d), want (0, 1)", cleaned, failed)
	}
	if len(libEnq.calls) != 0 {
		t.Fatalf("cascade enqueue ran after failed hard delete: %#v", libEnq.calls)
	}
}

func TestProcessOrgTrashCandidates_PartialSuccessLeavesBadLibraryUntouched(t *testing.T) {
	installDeleteHelperStubs(t)

	var hardDeleteCalls []string
	resolveDeleteBlockRepresentationFn = func(_ *db.DB, _, libraryID string) (string, error) {
		if libraryID == "repo-bad" {
			return "", errors.New("corrupt representation")
		}
		return db.PlainBlockRepresentationID, nil
	}
	cleanupLibraryLinksForDeleteFn = func(_ *db.DB, _, _ string) error {
		return nil
	}
	hardDeleteLibraryRowsFn = func(_ *db.DB, _, libraryID, _, _ string, _ time.Time) error {
		hardDeleteCalls = append(hardDeleteCalls, libraryID)
		return nil
	}
	libEnq := &mockLibraryGCEnqueuer{}
	h := &OrgAdminHandler{db: &db.DB{}, gcEnqueuer: libEnq}

	cleaned, failed := h.processOrgTrashCandidates([]trashLibraryCandidate{
		{OrgID: "org-1", LibraryID: "repo-good", StorageClass: "hot"},
		{OrgID: "org-1", LibraryID: "repo-bad", StorageClass: "hot"},
	}, libEnq)

	if cleaned != 1 || failed != 1 {
		t.Fatalf("processOrgTrashCandidates() = (cleaned=%d, failed=%d), want (1, 1)", cleaned, failed)
	}
	if strings.Join(hardDeleteCalls, ",") != "repo-good" {
		t.Fatalf("hard-delete calls = %#v, want only repo-good", hardDeleteCalls)
	}
	if len(libEnq.calls) != 1 || libEnq.calls[0].libraryID != "repo-good" {
		t.Fatalf("library-cascade enqueue calls = %#v, want only repo-good", libEnq.calls)
	}
}

func TestProcessOrgTrashCandidates_SuccessEnqueuesDedupCascadePreservingDeletedAt(t *testing.T) {
	installDeleteHelperStubs(t)

	resolved := db.PlainBlockRepresentationID
	wantDeletedAt := time.Now().Add(-96 * time.Hour).UTC().Truncate(time.Millisecond)
	var gotRepresentation string
	var gotWriterDeletedAt time.Time
	var cleanupLinksCalled int
	resolveDeleteBlockRepresentationFn = func(_ *db.DB, _, _ string) (string, error) {
		return resolved, nil
	}
	cleanupLibraryLinksForDeleteFn = func(_ *db.DB, _, _ string) error {
		cleanupLinksCalled++
		return nil
	}
	hardDeleteLibraryRowsFn = func(_ *db.DB, _, _, _, blockRepresentationID string, deletedAt time.Time) error {
		gotRepresentation = blockRepresentationID
		gotWriterDeletedAt = deletedAt
		return nil
	}
	libEnq := &mockLibraryGCEnqueuer{}
	h := &OrgAdminHandler{db: &db.DB{}, gcEnqueuer: libEnq}

	cleaned, failed := h.processOrgTrashCandidates([]trashLibraryCandidate{{
		OrgID:        "org-1",
		LibraryID:    "repo-1",
		StorageClass: "hot",
		DeletedAt:    wantDeletedAt,
	}}, libEnq)

	if cleaned != 1 || failed != 0 {
		t.Fatalf("processOrgTrashCandidates() = (cleaned=%d, failed=%d), want (1, 0)", cleaned, failed)
	}
	if cleanupLinksCalled != 1 {
		t.Fatalf("cleanup links called %d times, want 1", cleanupLinksCalled)
	}
	if gotRepresentation != resolved {
		t.Fatalf("hard-delete marker representation = %q, want %q", gotRepresentation, resolved)
	}
	// The bulk writer must preserve the ORIGINAL trash time so the immediate cascade shares
	// Phase 13's dedup identity — never reset to now().
	if !gotWriterDeletedAt.Equal(wantDeletedAt) {
		t.Fatalf("writer deleted_at = %s, want the original trash time %s (must not be reset)", gotWriterDeletedAt, wantDeletedAt)
	}
	if len(libEnq.calls) != 1 {
		t.Fatalf("expected exactly one immediate library-cascade enqueue, got %#v", libEnq.calls)
	}
	got := libEnq.calls[0]
	if got.orgID != "org-1" || got.libraryID != "repo-1" || got.storageClass != "hot" {
		t.Fatalf("cascade enqueue identity = %#v, want org-1/repo-1/hot", got)
	}
	if got.blockRepresentationID != resolved {
		t.Fatalf("cascade enqueue representation = %q, want %q", got.blockRepresentationID, resolved)
	}
	if !got.deletedAt.Equal(wantDeletedAt) {
		t.Fatalf("cascade enqueue deleted_at = %s, want %s (dedup identity must match Phase 13)", got.deletedAt, wantDeletedAt)
	}
}
