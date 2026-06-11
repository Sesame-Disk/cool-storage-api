package v2

import (
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/traffic"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/gin-gonic/gin"
)

// trafficTypeOrder defines the fixed output column order for traffic statistics responses.
var trafficTypeOrder = []string{
	"sync-file-upload", "sync-file-download",
	"web-file-upload", "web-file-download",
	"link-file-upload", "link-file-download",
}

type userTrafficMeta struct {
	ID    string
	Email string
	Name  string
}

func forEachTrafficCounterShard(orgID gocql.UUID, fn func(int)) {
	if orgID == (gocql.UUID{}) {
		traffic.ForEachCounterShard(fn)
		return
	}
	fn(0)
}

func readPlatformTrafficUsage(session *gocql.Session, months []string) traffic.MonthlyTransferUsage {
	usage := traffic.MonthlyTransferUsage{}
	for _, month := range months {
		forEachTrafficCounterShard(gocql.UUID{}, func(shard int) {
			iter := session.Query(
				`SELECT user_id, traffic_type, bytes_transferred FROM traffic_counters WHERE org_id = ? AND month = ? AND shard = ?`,
				gocql.UUID{}, month, shard,
			).Iter()
			var userUUID gocql.UUID
			var trafficType string
			var bytes int64
			for iter.Scan(&userUUID, &trafficType, &bytes) {
				if userUUID != (gocql.UUID{}) {
					continue
				}
				usage.Combined += bytes
				if strings.HasSuffix(trafficType, "-upload") {
					usage.Upload += bytes
				} else if strings.HasSuffix(trafficType, "-download") {
					usage.Download += bytes
				}
			}
			if err := iter.Close(); err != nil {
				log.Printf("[traffic] readPlatformTrafficUsage shard=%d month=%s iter error: %v", shard, month, err)
			}
		})
	}
	return usage
}

func yearToDateMonthKeys(now time.Time) []string {
	months := make([]string, 0, int(now.Month()))
	for month := time.January; month <= now.Month(); month++ {
		months = append(months, time.Date(now.Year(), month, 1, 0, 0, 0, 0, time.UTC).Format("200601"))
	}
	return months
}

func newUserTrafficRow(meta userTrafficMeta) gin.H {
	return gin.H{
		"email":              meta.Email,
		"name":               meta.Name,
		"sync_file_upload":   int64(0),
		"sync_file_download": int64(0),
		"web_file_upload":    int64(0),
		"web_file_download":  int64(0),
		"link_file_upload":   int64(0),
		"link_file_download": int64(0),
	}
}

func addTrafficBytes(row gin.H, trafficType string, bytes int64) {
	switch trafficType {
	case "sync-file-upload":
		row["sync_file_upload"] = row["sync_file_upload"].(int64) + bytes
	case "sync-file-download":
		row["sync_file_download"] = row["sync_file_download"].(int64) + bytes
	case "web-file-upload":
		row["web_file_upload"] = row["web_file_upload"].(int64) + bytes
	case "web-file-download":
		row["web_file_download"] = row["web_file_download"].(int64) + bytes
	case "link-file-upload":
		row["link_file_upload"] = row["link_file_upload"].(int64) + bytes
	case "link-file-download":
		row["link_file_download"] = row["link_file_download"].(int64) + bytes
	}
}

func int64FromTrafficRow(row gin.H, key string) int64 {
	if value, ok := row[key]; ok {
		switch typed := value.(type) {
		case int64:
			return typed
		case int:
			return int64(typed)
		case int32:
			return int64(typed)
		case float64:
			return int64(typed)
		}
	}
	return 0
}

func stringFromTrafficRow(row gin.H, key string) string {
	if value, ok := row[key]; ok {
		if typed, ok := value.(string); ok {
			return typed
		}
	}
	return ""
}

func sortTrafficRows(rows []gin.H, orderBy, defaultField string) {
	field := orderBy
	ascending := true
	if field == "" {
		field = defaultField + "_desc"
	}
	if strings.HasSuffix(field, "_desc") {
		ascending = false
		field = strings.TrimSuffix(field, "_desc")
	} else if strings.HasSuffix(field, "_asc") {
		field = strings.TrimSuffix(field, "_asc")
	}

	sort.SliceStable(rows, func(i, j int) bool {
		if field == "name" || field == "email" || field == "org_name" {
			left := stringFromTrafficRow(rows[i], field)
			right := stringFromTrafficRow(rows[j], field)
			if !ascending {
				return left > right
			}
			return left < right
		}

		left := int64FromTrafficRow(rows[i], field)
		right := int64FromTrafficRow(rows[j], field)
		if left == right {
			return stringFromTrafficRow(rows[i], "name") < stringFromTrafficRow(rows[j], "name")
		}
		if ascending {
			return left < right
		}
		return left > right
	})
}

func loadUserTrafficMetaForOrg(session *gocql.Session, orgID gocql.UUID, userMap map[string]userTrafficMeta) {
	iter := session.Query(`SELECT user_id, email, name FROM users WHERE org_id = ?`, orgID).Iter()
	var uid gocql.UUID
	var email, name string
	for iter.Scan(&uid, &email, &name) {
		userMap[uid.String()] = userTrafficMeta{ID: uid.String(), Email: email, Name: name}
	}
	if err := iter.Close(); err != nil {
		log.Printf("[traffic] loadUserTrafficMetaForOrg org=%s iter error: %v", orgID, err)
	}
}

func accumulateTrafficPartition(session *gocql.Session, orgID gocql.UUID, month string, userMap map[string]userTrafficMeta, entries map[string]gin.H, perUserTotals map[string]int64, aggregateTotals map[string]int64) {
	forEachTrafficCounterShard(orgID, func(shard int) {
		iter := session.Query(
			`SELECT user_id, traffic_type, bytes_transferred FROM traffic_counters WHERE org_id = ? AND month = ? AND shard = ?`,
			orgID, month, shard,
		).Iter()
		var userUUID gocql.UUID
		var trafficType string
		var bytes int64
		for iter.Scan(&userUUID, &trafficType, &bytes) {
			if userUUID == (gocql.UUID{}) {
				if aggregateTotals != nil {
					aggregateTotals[trafficType] += bytes
				}
				continue
			}
			if perUserTotals != nil {
				perUserTotals[trafficType] += bytes
			}
			meta, ok := userMap[userUUID.String()]
			if !ok || meta.Email == "" {
				continue
			}
			entry, exists := entries[meta.Email]
			if !exists {
				entry = newUserTrafficRow(meta)
			}
			addTrafficBytes(entry, trafficType, bytes)
			entries[meta.Email] = entry
		}
		if err := iter.Close(); err != nil {
			log.Printf("[traffic] accumulateTrafficPartition org=%s shard=%d month=%s iter error: %v", orgID, shard, month, err)
		}
	})
}

func platformUserTrafficComplete(platformTotals map[string]int64, perUserTotals map[string]int64) bool {
	for _, trafficType := range trafficTypeOrder {
		if platformTotals[trafficType] != perUserTotals[trafficType] {
			return false
		}
	}
	return true
}

// queryTrafficStats reads traffic_counters for the given orgID and the date range
// specified in the gin.Context query params (start, end, group_by).
// Pass orgID=gocql.UUID{} (all-zeros) to query the platform-wide aggregate
// written by the Recorder for sysadmin cross-org statistics. That partition now
// also contains per-user detail rows, so only the zero-user aggregate rows are
// counted for the system-wide chart.
func queryTrafficStats(session *gocql.Session, orgID gocql.UUID, c *gin.Context) []gin.H {
	startStr := c.Query("start")
	endStr := c.Query("end")
	groupBy := c.DefaultQuery("group_by", "day")

	today := time.Now().UTC().Truncate(24 * time.Hour)
	start := today.AddDate(0, 0, -7)
	end := today
	if startStr != "" {
		start = parseDateParam(startStr, start)
	}
	if endStr != "" {
		end = parseDateParam(endStr, end)
	}

	var dates []time.Time
	for d := start; !d.After(end); {
		dates = append(dates, d)
		switch groupBy {
		case "month":
			d = d.AddDate(0, 1, 0)
		case "week":
			d = d.AddDate(0, 0, 7)
		default:
			d = d.AddDate(0, 0, 1)
		}
	}
	if len(dates) == 0 {
		dates = []time.Time{today}
	}

	monthsNeeded := map[string]bool{}
	for _, d := range dates {
		monthsNeeded[d.Format("200601")] = true
	}

	agg := map[string]map[string]int64{}
	for month := range monthsNeeded {
		forEachTrafficCounterShard(orgID, func(shard int) {
			iter := session.Query(
				`SELECT day, user_id, traffic_type, bytes_transferred FROM traffic_counters
				 WHERE org_id = ? AND month = ? AND shard = ?`,
				orgID, month, shard,
			).Iter()
			var day time.Time
			var userID gocql.UUID
			var trafficType string
			var bytes int64
			for iter.Scan(&day, &userID, &trafficType, &bytes) {
				if orgID == (gocql.UUID{}) && userID != (gocql.UUID{}) {
					continue
				}
				dk := day.UTC().Format("2006-01-02")
				if agg[dk] == nil {
					agg[dk] = map[string]int64{}
				}
				agg[dk][trafficType] += bytes
			}
			if err := iter.Close(); err != nil {
				log.Printf("[traffic] queryTrafficStats iter error: %v", err)
			}
		})
	}

	result := make([]gin.H, 0, len(dates))
	for i, d := range dates {
		row := gin.H{"datetime": d.Format("2006-01-02T00:00:00+00:00")}
		var periodEnd time.Time
		if i+1 < len(dates) {
			periodEnd = dates[i+1]
		} else {
			periodEnd = end.AddDate(0, 0, 1)
		}
		sums := map[string]int64{}
		for dk, vals := range agg {
			dt, err := time.Parse("2006-01-02", dk)
			if err != nil {
				continue
			}
			if !dt.Before(d) && dt.Before(periodEnd) {
				for k, v := range vals {
					sums[k] += v
				}
			}
		}
		for _, k := range trafficTypeOrder {
			row[k] = sums[k]
		}
		result = append(result, row)
	}
	return result
}

func (h *AdminHandler) AdminStatisticFiles(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	stats := h.generateDateRange(c)
	result := make([]gin.H, len(stats))
	for i, dt := range stats {
		result[i] = gin.H{
			"datetime": dt,
			"added":    0,
			"deleted":  0,
			"modified": 0,
			"visited":  0,
		}
	}
	c.JSON(http.StatusOK, result)
}

func (h *AdminHandler) AdminStatisticStorage(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	start, end := parseDateRangeParams(c)
	history := traffic.ReconstructStorageHistory(h.db, traffic.PlatformStorageScope(), start, end)

	stats := h.generateDateRange(c)
	result := make([]gin.H, len(stats))
	for i, dt := range stats {
		dayKey := dt[:10]
		result[i] = gin.H{
			"datetime":      dt,
			"total_storage": history[dayKey],
		}
	}
	c.JSON(http.StatusOK, result)
}

func (h *AdminHandler) AdminStatisticActiveUsers(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	c.JSON(http.StatusOK, queryActiveUserStats(h.db.Session(), gocql.UUID{}, c))
}

func (h *AdminHandler) AdminStatisticTraffic(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	c.JSON(http.StatusOK, queryTrafficStats(h.db.Session(), gocql.UUID{}, c))
}

func (h *AdminHandler) generateDateRange(c *gin.Context) []string {
	return dateRangeStrings(c)
}

func parseDateParam(s string, fallback time.Time) time.Time {
	for _, layout := range []string{"2006-01-02", "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC().Truncate(24 * time.Hour)
		}
	}
	return fallback
}

func parseDateRangeParams(c *gin.Context) (time.Time, time.Time) {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	start := today.AddDate(0, 0, -7)
	end := today
	if s := c.Query("start"); s != "" {
		start = parseDateParam(s, start)
	}
	if s := c.Query("end"); s != "" {
		end = parseDateParam(s, end)
	}
	return start, end
}

// dateRangeStrings is the package-level equivalent of generateDateRange.
// Both AdminHandler and OrgAdminHandler use this helper.
func dateRangeStrings(c *gin.Context) []string {
	startStr := c.Query("start")
	endStr := c.Query("end")
	groupBy := c.DefaultQuery("group_by", "day")

	today := time.Now().UTC().Truncate(24 * time.Hour)
	start := today.AddDate(0, 0, -7)
	end := today

	if startStr != "" {
		start = parseDateParam(startStr, start)
	}
	if endStr != "" {
		end = parseDateParam(endStr, end)
	}

	var dates []string
	for d := start; !d.After(end); {
		dates = append(dates, d.Format("2006-01-02T00:00:00+00:00"))
		switch groupBy {
		case "month":
			d = d.AddDate(0, 1, 0)
		case "week":
			d = d.AddDate(0, 0, 7)
		default:
			d = d.AddDate(0, 0, 1)
		}
	}
	if len(dates) == 0 {
		dates = []string{today.Format("2006-01-02T00:00:00+00:00")}
	}
	return dates
}

// queryActiveUserStats returns a time series where each bucket counts distinct
// users that generated traffic during the requested period, regardless of their
// current status at query time.
func queryActiveUserStats(session *gocql.Session, orgID gocql.UUID, c *gin.Context) []gin.H {
	startStr := c.Query("start")
	endStr := c.Query("end")
	groupBy := c.DefaultQuery("group_by", "day")

	today := time.Now().UTC().Truncate(24 * time.Hour)
	start := today.AddDate(0, 0, -7)
	end := today
	if startStr != "" {
		start = parseDateParam(startStr, start)
	}
	if endStr != "" {
		end = parseDateParam(endStr, end)
	}

	var dates []time.Time
	for d := start; !d.After(end); {
		dates = append(dates, d)
		switch groupBy {
		case "month":
			d = d.AddDate(0, 1, 0)
		case "week":
			d = d.AddDate(0, 0, 7)
		default:
			d = d.AddDate(0, 0, 1)
		}
	}
	if len(dates) == 0 {
		dates = []time.Time{today}
	}

	monthsNeeded := map[string]bool{}
	for _, d := range dates {
		monthsNeeded[d.Format("200601")] = true
	}

	dailyUsers := map[string]map[gocql.UUID]struct{}{}
	for month := range monthsNeeded {
		forEachTrafficCounterShard(orgID, func(shard int) {
			iter := session.Query(
				`SELECT day, user_id FROM traffic_counters WHERE org_id = ? AND month = ? AND shard = ?`,
				orgID, month, shard,
			).Iter()
			var day time.Time
			var userID gocql.UUID
			for iter.Scan(&day, &userID) {
				if userID == (gocql.UUID{}) {
					continue
				}
				dayKey := day.UTC().Format("2006-01-02")
				if dailyUsers[dayKey] == nil {
					dailyUsers[dayKey] = map[gocql.UUID]struct{}{}
				}
				dailyUsers[dayKey][userID] = struct{}{}
			}
			if err := iter.Close(); err != nil {
				log.Printf("[active-users] queryActiveUserStats iter error: %v", err)
			}
		})
	}

	result := make([]gin.H, 0, len(dates))
	for i, d := range dates {
		var periodEnd time.Time
		if i+1 < len(dates) {
			periodEnd = dates[i+1]
		} else {
			periodEnd = end.AddDate(0, 0, 1)
		}

		bucketUsers := map[gocql.UUID]struct{}{}
		for dayKey, users := range dailyUsers {
			dt, err := time.Parse("2006-01-02", dayKey)
			if err != nil {
				continue
			}
			if !dt.Before(d) && dt.Before(periodEnd) {
				for userID := range users {
					bucketUsers[userID] = struct{}{}
				}
			}
		}

		result = append(result, gin.H{
			"datetime": d.Format("2006-01-02T00:00:00+00:00"),
			"count":    len(bucketUsers),
		})
	}

	return result
}

// AdminListOrgTraffic returns per-org traffic totals for a given month.
// GET /admin/statistics/org-traffic/?month=202603&page=1&per_page=25
func (h *AdminHandler) AdminListOrgTraffic(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
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

	type orgMeta struct {
		ID   gocql.UUID
		Name string
	}
	var allOrgs []orgMeta
	iter := h.db.Session().Query(`SELECT org_id, name FROM organizations`).Iter()
	var oid gocql.UUID
	var oname string
	for iter.Scan(&oid, &oname) {
		allOrgs = append(allOrgs, orgMeta{ID: oid, Name: oname})
	}
	if err := iter.Close(); err != nil {
		log.Printf("[traffic] admin traffic organizations iter error: %v", err)
	}

	list := make([]gin.H, 0, len(allOrgs))
	for _, org := range allOrgs {
		usage := traffic.ReadOrgMonthlyUsage(h.db, org.ID.String(), month)
		list = append(list, gin.H{
			"org_id":         org.ID.String(),
			"name":           org.Name,
			"upload_bytes":   usage.Upload,
			"download_bytes": usage.Download,
			"total_bytes":    usage.Combined,
		})
	}
	sortTrafficRows(list, orderBy, "total_bytes")

	offset := (page - 1) * perPage
	hasNext := false
	if offset >= len(list) {
		c.JSON(http.StatusOK, gin.H{"org_traffic_list": []gin.H{}, "has_next_page": false})
		return
	}
	end := offset + perPage
	if end >= len(list) {
		end = len(list)
	} else {
		hasNext = true
	}
	list = list[offset:end]

	c.JSON(http.StatusOK, gin.H{"org_traffic_list": list, "has_next_page": hasNext})
}

// AdminListUserTraffic returns per-user traffic totals for a given month.
// When org_id is omitted, it aggregates traffic across all organizations.
// GET /admin/statistics/user-traffic/?month=202603&page=1&per_page=25
func (h *AdminHandler) AdminListUserTraffic(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	targetOrgStr := c.Query("org_id")
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

	entries := map[string]gin.H{}
	userMap := map[string]userTrafficMeta{}
	var orgIDs []gocql.UUID
	if targetOrgStr != "" {
		targetOrgUUID, err := gocql.ParseUUID(targetOrgStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid org_id"})
			return
		}
		orgIDs = []gocql.UUID{targetOrgUUID}
	} else {
		iter := h.db.Session().Query(`SELECT org_id FROM organizations`).Iter()
		var orgID gocql.UUID
		for iter.Scan(&orgID) {
			orgIDs = append(orgIDs, orgID)
		}
		if err := iter.Close(); err != nil {
			log.Printf("[traffic] admin active-user org list iter error: %v", err)
		}
	}

	for _, orgID := range orgIDs {
		loadUserTrafficMetaForOrg(h.db.Session(), orgID, userMap)
	}

	if targetOrgStr != "" {
		accumulateTrafficPartition(h.db.Session(), orgIDs[0], month, userMap, entries, nil, nil)
	} else {
		fastEntries := map[string]gin.H{}
		platformTotals := map[string]int64{}
		perUserTotals := map[string]int64{}
		accumulateTrafficPartition(h.db.Session(), gocql.UUID{}, month, userMap, fastEntries, perUserTotals, platformTotals)
		if platformUserTrafficComplete(platformTotals, perUserTotals) {
			entries = fastEntries
		} else {
			for _, orgID := range orgIDs {
				accumulateTrafficPartition(h.db.Session(), orgID, month, userMap, entries, nil, nil)
			}
		}
	}

	list := make([]gin.H, 0, len(entries))
	for _, entry := range entries {
		list = append(list, entry)
	}
	sortTrafficRows(list, orderBy, "link_file_download")

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
