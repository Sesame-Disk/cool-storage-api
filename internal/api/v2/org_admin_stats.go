package v2

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/traffic"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/gin-gonic/gin"
)

// Statistics
// ============================================================================

func (h *OrgAdminHandler) OrgStatisticFiles(c *gin.Context) {
	h.notImplemented(c, "org statistics file-operations")
}
func (h *OrgAdminHandler) OrgStatisticStorage(c *gin.Context) {
	targetOrgID := c.Param("org_id")
	if err := h.requireOrgAccess(c, targetOrgID); err != nil {
		return
	}

	scope := fmt.Sprintf("org:%s", targetOrgID)
	start, end := parseDateRangeParams(c)
	history := traffic.ReconstructStorageHistory(h.db, scope, start, end)

	stats := dateRangeStrings(c)
	result := make([]gin.H, len(stats))
	for i, dt := range stats {
		dayKey := dt[:10]
		result[i] = gin.H{"datetime": dt, "total_storage": history[dayKey]}
	}
	c.JSON(http.StatusOK, result)
}
func (h *OrgAdminHandler) OrgStatisticActiveUsers(c *gin.Context) {
	targetOrgID := c.Param("org_id")
	if err := h.requireOrgAccess(c, targetOrgID); err != nil {
		return
	}

	orgUUID, err := gocql.ParseUUID(targetOrgID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid org_id"})
		return
	}

	c.JSON(http.StatusOK, queryActiveUserStats(h.db.Session(), orgUUID, c))
}
func (h *OrgAdminHandler) OrgStatisticTraffic(c *gin.Context) {
	targetOrgID := c.Param("org_id")
	if err := h.requireOrgAccess(c, targetOrgID); err != nil {
		return
	}

	orgUUID, err := gocql.ParseUUID(targetOrgID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid org_id"})
		return
	}
	c.JSON(http.StatusOK, queryTrafficStats(h.db.Session(), orgUUID, c))
}
func (h *OrgAdminHandler) OrgStatisticUserTraffic(c *gin.Context) {
	targetOrgID := c.Param("org_id")
	if err := h.requireOrgAccess(c, targetOrgID); err != nil {
		return
	}

	month := c.DefaultQuery("month", time.Now().UTC().Format("200601"))
	orderBy := c.Query("order_by")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	perPage, _ := strconv.Atoi(c.DefaultQuery("per_page", "25"))
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 100 {
		perPage = 25
	}

	orgUUID, err := gocql.ParseUUID(targetOrgID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid org_id"})
		return
	}

	// Build user info map and accumulate per-type traffic from traffic_counters,
	// matching the sysadmin AdminListUserTraffic response format so the frontend
	// can display per-type columns (sync/web/link × upload/download).
	userMap := map[string]userTrafficMeta{}
	loadUserTrafficMetaForOrg(h.db.Session(), orgUUID, userMap)

	entries := map[string]gin.H{}
	accumulateTrafficPartition(h.db.Session(), orgUUID, month, userMap, entries, nil, nil)

	list := make([]gin.H, 0, len(entries))
	for _, entry := range entries {
		list = append(list, entry)
	}
	sortTrafficRows(list, orderBy, "link_file_download")

	// Paginate after sorting.
	offset := (page - 1) * perPage
	hasNext := false
	if offset >= len(list) {
		c.JSON(http.StatusOK, gin.H{"user_monthly_traffic_list": []gin.H{}, "has_next_page": false})
		return
	}
	endIdx := offset + perPage
	if endIdx >= len(list) {
		endIdx = len(list)
	} else {
		hasNext = true
	}
	c.JSON(http.StatusOK, gin.H{"user_monthly_traffic_list": list[offset:endIdx], "has_next_page": hasNext})
}

// ============================================================================
// Devices
// ============================================================================

// ListOrgDevices returns an empty device list (no device tracking table yet).
// GET /org/:org_id/admin/devices/
func (h *OrgAdminHandler) ListOrgDevices(c *gin.Context) {
	targetOrgID := c.Param("org_id")
	if err := h.requireOrgAccess(c, targetOrgID); err != nil {
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"devices": []gin.H{},
		"page_info": gin.H{
			"current_page":  1,
			"has_next_page": false,
		},
	})
}

// UnlinkOrgDevice is a no-op (no device tracking table yet).
// DELETE /org/:org_id/admin/devices/
func (h *OrgAdminHandler) UnlinkOrgDevice(c *gin.Context) {
	targetOrgID := c.Param("org_id")
	if err := h.requireOrgAccess(c, targetOrgID); err != nil {
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ListOrgDeviceErrors returns an empty error list (no device tracking table yet).
// GET /org/:org_id/admin/devices-errors/
func (h *OrgAdminHandler) ListOrgDeviceErrors(c *gin.Context) {
	targetOrgID := c.Param("org_id")
	if err := h.requireOrgAccess(c, targetOrgID); err != nil {
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"devices": []gin.H{},
		"page_info": gin.H{
			"current_page":  1,
			"has_next_page": false,
		},
	})
}

// ============================================================================
