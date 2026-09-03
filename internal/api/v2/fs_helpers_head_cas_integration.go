//go:build integration

package v2

import "time"

// SetLibraryHeadAmbiguousCASForTest forces the next library HEAD CAS write to
// report ambErr (a shape isAmbiguousLibraryHeadUpdateError recognizes --
// gocql.RequestErrCASWriteUnknown, gocql.ErrTimeoutNoResponse, or
// gocql.ErrConnectionClosed) instead of its real outcome, so
// resolveLibraryHeadUpdateError's ambiguous branch can be exercised
// end-to-end from a real CreateFileFromBlocks call. When applyReal is true
// the genuine CAS write still executes first (models "applied, response
// lost"); when false, no write happens at all (models "uncertain, did not
// apply"). The returned restore function must run from t.Cleanup.
func SetLibraryHeadAmbiguousCASForTest(applyReal bool, ambErr error) func() {
	previous := libraryHeadCASExecuteFn
	libraryHeadCASExecuteFn = func(h *FSHelper, orgID, repoID, commitID string, totalSize, fileCount int64, now time.Time, expectedHead string) (bool, map[string]interface{}, error) {
		if applyReal {
			if _, _, err := previous(h, orgID, repoID, commitID, totalSize, fileCount, now, expectedHead); err != nil {
				return false, nil, err
			}
		}
		return false, nil, ambErr
	}
	return func() { libraryHeadCASExecuteFn = previous }
}

// SetLibraryHeadConfirmVisibleForTest forces the post-ambiguous-CAS SERIAL
// confirmation read's outcome instead of running the real read.
// visible=true reports commitID itself as the confirmed head; visible=false
// reports otherHead as the confirmed (different) head. confirmErr, when
// non-nil, models outcome (c): the confirmation read itself failed/was
// inconclusive, which resolveLibraryHeadUpdateError turns into
// ErrLibraryHeadPublicationUnknown. The returned restore function must run
// from t.Cleanup.
func SetLibraryHeadConfirmVisibleForTest(visible bool, otherHead string, confirmErr error) func() {
	previous := libraryHeadConfirmVisibleFn
	libraryHeadConfirmVisibleFn = func(h *FSHelper, orgID, repoID, commitID string) (string, bool, error) {
		if confirmErr != nil {
			return "", false, confirmErr
		}
		if visible {
			return commitID, true, nil
		}
		return otherHead, false, nil
	}
	return func() { libraryHeadConfirmVisibleFn = previous }
}
