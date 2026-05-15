package v2

import (
	"fmt"
	"net/http"

	"github.com/Sesame-Disk/sesamefs/internal/traffic"
	"github.com/gin-gonic/gin"
)

func quotaErrorPayloadKey(errorKey string) string {
	if errorKey == "" {
		return "error"
	}
	return errorKey
}

// fsEntryStats returns (sizeBytes, fileCount) for an FS entry.
// For files, returns entry.Size and 1. For directories, recursively sums the
// tree rooted at the entry's fs_id.
func fsEntryStats(fsHelper *FSHelper, repoID string, entry FSEntry) (int64, int64, error) {
	if entry.Mode == ModeDir || entry.Mode&0170000 == 040000 {
		_, totalSize, fileCount, err := fsHelper.collectDirStats(repoID, entry.ID)
		if err != nil {
			return 0, 0, fmt.Errorf("collect dir stats for %s: %w", entry.ID, err)
		}
		return totalSize, fileCount, nil
	}
	return entry.Size, 1, nil
}

// fsEntryDelta returns the (bytes, files) delta when newEntry is added to a
// directory, optionally replacing an existing entry.
func fsEntryDelta(fsHelper *FSHelper, repoID string, newEntry FSEntry, replacing *FSEntry) (int64, int64, error) {
	newSize, newCount, err := fsEntryStats(fsHelper, repoID, newEntry)
	if err != nil {
		return 0, 0, err
	}
	if replacing == nil {
		return newSize, newCount, nil
	}
	oldSize, oldCount, err := fsEntryStats(fsHelper, repoID, *replacing)
	if err != nil {
		return 0, 0, err
	}
	return newSize - oldSize, newCount - oldCount, nil
}

// preCheckStorageQuotaForDelta evaluates the per-user/org storage quota for a
// positive byte delta. Returns true when the operation may proceed. On quota
// failure, writes a 403 response and returns false. Negative or zero deltas
// always pass — the operation does not grow storage.
func preCheckStorageQuotaForDelta(c *gin.Context, orgID, userID string, deltaBytes int64) bool {
	return preCheckStorageQuotaForDeltaWithKey(c, orgID, userID, deltaBytes, "error")
}

func preCheckStorageQuotaForDeltaWithKey(c *gin.Context, orgID, userID string, deltaBytes int64, errorKey string) bool {
	if deltaBytes <= 0 {
		return true
	}
	checker := traffic.GetChecker()
	if checker == nil {
		return true
	}
	st, _ := checker.CheckStorageQuota(orgID, userID, deltaBytes)
	if !st.Allowed {
		c.JSON(http.StatusForbidden, gin.H{quotaErrorPayloadKey(errorKey): "storage quota exceeded"})
		return false
	}
	return true
}

// applyStorageCounterDelta calls AdjustStorageCountersByDeltaSync and, on error,
// writes a 500 response and returns false. Callers should only call this after
// the underlying commit has been published; the function is meant to keep the
// publish/counter pair together at the handler level.
func applyStorageCounterDelta(c *gin.Context, db traffic.DBSession, orgID, userID, repoID string, deltaBytes, deltaFiles int64) bool {
	return applyStorageCounterDeltaWithKey(c, db, orgID, userID, repoID, deltaBytes, deltaFiles, "error")
}

func applyStorageCounterDeltaWithKey(c *gin.Context, db traffic.DBSession, orgID, userID, repoID string, deltaBytes, deltaFiles int64, errorKey string) bool {
	if deltaBytes == 0 && deltaFiles == 0 {
		return true
	}
	if err := traffic.AdjustStorageCountersByDeltaSync(db, orgID, userID, repoID, deltaBytes, deltaFiles); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{quotaErrorPayloadKey(errorKey): "failed to update storage counters"})
		return false
	}
	return true
}
