package v2

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	db "github.com/Sesame-Disk/sesamefs/internal/db"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/gin-gonic/gin"
)

var (
	errInvalidStatusFilter  = errors.New("invalid status filter")
	errInvalidActiveFilter  = errors.New("invalid active filter")
	errInvalidExpiredFilter = errors.New("invalid expired filter")
	errAdminLinkNotFound    = errors.New("admin link not found")
	errAdminLinkWrongType   = errors.New("wrong type")
	errAdminLinkWrongOrg    = errors.New("wrong org")
)

type adminLinkListFilters struct {
	HasActiveFilter  bool
	ActiveFilter     bool
	HasExpiredFilter bool
	ExpiredFilter    bool
	Search           string
}

func parseAdminLinkListFiltersFromContext(c *gin.Context, includeActive bool) (adminLinkListFilters, error) {
	statusParam := ""
	activeParam := ""
	if includeActive {
		statusParam = c.DefaultQuery("status", "")
		activeParam = c.DefaultQuery("active", "")
	}

	return parseAdminLinkListFilters(
		statusParam,
		activeParam,
		c.DefaultQuery("expired", ""),
		c.DefaultQuery("search", ""),
	)
}

func parseAdminLinkListFilters(statusParam, activeParam, expiredParam, searchParam string) (adminLinkListFilters, error) {
	filters := adminLinkListFilters{
		Search: strings.ToLower(strings.TrimSpace(searchParam)),
	}

	statusFilter := strings.TrimSpace(strings.ToLower(statusParam))
	activeValue := strings.TrimSpace(strings.ToLower(activeParam))
	expiredValue := strings.TrimSpace(strings.ToLower(expiredParam))

	if statusFilter == "active" {
		activeValue = "true"
	} else if statusFilter == "inactive" {
		activeValue = "false"
	} else if statusFilter != "" && statusFilter != "all" {
		return filters, errInvalidStatusFilter
	}

	if activeValue != "" && activeValue != "all" {
		parsed, err := strconv.ParseBool(activeValue)
		if err != nil {
			return filters, errInvalidActiveFilter
		}
		filters.HasActiveFilter = true
		filters.ActiveFilter = parsed
	}

	if expiredValue != "" && expiredValue != "all" {
		parsed, err := strconv.ParseBool(expiredValue)
		if err != nil {
			return filters, errInvalidExpiredFilter
		}
		filters.HasExpiredFilter = true
		filters.ExpiredFilter = parsed
	}

	return filters, nil
}

func (filters adminLinkListFilters) MatchesState(active, expired bool) bool {
	if filters.HasActiveFilter && active != filters.ActiveFilter {
		return false
	}
	if filters.HasExpiredFilter && expired != filters.ExpiredFilter {
		return false
	}
	return true
}

func (filters adminLinkListFilters) MatchesSearch(values ...string) bool {
	return matchesAdminLinkSearch(filters.Search, values...)
}

func matchesAdminLinkSearch(search string, values ...string) bool {
	needle := strings.ToLower(strings.TrimSpace(search))
	if needle == "" {
		return true
	}

	for _, value := range values {
		if strings.Contains(strings.ToLower(value), needle) {
			return true
		}
	}

	return false
}

func validateAdminLinkScope(actualOrgID, expectedOrgID, actualType, expectedType string) error {
	if expectedOrgID != "" && actualOrgID != expectedOrgID {
		return errAdminLinkWrongOrg
	}
	if expectedType != "" && actualType != expectedType {
		return errAdminLinkWrongType
	}
	return nil
}

func parseAdminLinkPageParams(pageParam, perPageParam string, defaultPerPage, maxPerPage int) (int, int) {
	page, err := strconv.Atoi(pageParam)
	if err != nil || page < 1 {
		page = 1
	}

	perPage, err := strconv.Atoi(perPageParam)
	if err != nil || perPage < 1 {
		perPage = defaultPerPage
	}
	if maxPerPage > 0 && perPage > maxPerPage {
		perPage = maxPerPage
	}

	return page, perPage
}

func paginateAdminLinks(links []gin.H, page, perPage int) ([]gin.H, int, bool) {
	total := len(links)
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = total
		if perPage < 1 {
			perPage = 1
		}
	}

	start := (page - 1) * perPage
	if start > total {
		start = total
	}
	end := start + perPage
	if end > total {
		end = total
	}

	return links[start:end], total, end < total
}

type adminLinkPageCursor struct {
	BucketDay string    `json:"bucket_day"`
	CreatedAt time.Time `json:"created_at"`
	OrgID     string    `json:"org_id,omitempty"`
	Token     string    `json:"token"`
}

func parseAdminLinkCursor(cursorParam string) (adminLinkPageCursor, bool, error) {
	cursorParam = strings.TrimSpace(cursorParam)
	if cursorParam == "" {
		return adminLinkPageCursor{}, false, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(cursorParam)
	if err != nil {
		return adminLinkPageCursor{}, false, errors.New("invalid cursor")
	}
	var cursor adminLinkPageCursor
	if err := json.Unmarshal(decoded, &cursor); err != nil {
		return adminLinkPageCursor{}, false, errors.New("invalid cursor")
	}
	if cursor.BucketDay == "" || cursor.CreatedAt.IsZero() || cursor.Token == "" {
		return adminLinkPageCursor{}, false, errors.New("invalid cursor")
	}
	return cursor, true, nil
}

func buildAdminLinkCursor(cursor adminLinkPageCursor) (string, error) {
	payload, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func adminLinkCursorFromRow(bucketDay string, row adminLinkProjectionRow) (string, error) {
	return buildAdminLinkCursor(adminLinkPageCursor{
		BucketDay: bucketDay,
		CreatedAt: row.CreatedAt,
		OrgID:     row.OrgID,
		Token:     row.Token,
	})
}

func adminLinkBucketBeforeCursor(bucketDay string, cursor adminLinkPageCursor, hasCursor bool) bool {
	return hasCursor && bucketDay > cursor.BucketDay
}

func compareCassandraUUIDStrings(left, right string) int {
	leftUUID, leftErr := gocql.ParseUUID(left)
	rightUUID, rightErr := gocql.ParseUUID(right)
	if leftErr != nil || rightErr != nil {
		return strings.Compare(left, right)
	}

	leftMSB := int64(binary.BigEndian.Uint64(leftUUID[:8]))
	rightMSB := int64(binary.BigEndian.Uint64(rightUUID[:8]))
	if leftMSB < rightMSB {
		return -1
	}
	if leftMSB > rightMSB {
		return 1
	}

	leftLSB := int64(binary.BigEndian.Uint64(leftUUID[8:]))
	rightLSB := int64(binary.BigEndian.Uint64(rightUUID[8:]))
	if leftLSB < rightLSB {
		return -1
	}
	if leftLSB > rightLSB {
		return 1
	}
	return 0
}

func adminLinkGlobalRowAtOrBeforeCursor(row adminLinkProjectionRow, cursor adminLinkPageCursor) bool {
	if row.CreatedAt.After(cursor.CreatedAt) {
		return true
	}
	if row.CreatedAt.Before(cursor.CreatedAt) {
		return false
	}
	orgCmp := compareCassandraUUIDStrings(row.OrgID, cursor.OrgID)
	if orgCmp < 0 {
		return true
	}
	if orgCmp > 0 {
		return false
	}
	return row.Token <= cursor.Token
}

func adminLinkScopedRowAtOrBeforeCursor(row adminLinkProjectionRow, cursor adminLinkPageCursor) bool {
	if row.CreatedAt.After(cursor.CreatedAt) {
		return true
	}
	if row.CreatedAt.Before(cursor.CreatedAt) {
		return false
	}
	return row.Token <= cursor.Token
}

func normalizeAdminLinkSort(sortBy, direction string) (string, string) {
	if sortBy == "" {
		sortBy = "ctime"
		if direction == "" {
			direction = "desc"
		}
	} else if direction == "" {
		direction = "asc"
	}
	return sortBy, direction
}

func isDefaultAdminLinkSort(sortBy, direction string) bool {
	sortBy, direction = normalizeAdminLinkSort(sortBy, direction)
	return sortBy == "ctime" && direction == "desc"
}

func sortAdminLinks(links []gin.H, sortBy, direction string) {
	sortBy, direction = normalizeAdminLinkSort(sortBy, direction)

	sort.Slice(links, func(i, j int) bool {
		if sortBy == "view_cnt" {
			vi, _ := links[i]["view_cnt"].(int)
			vj, _ := links[j]["view_cnt"].(int)
			if direction == "desc" {
				return vi > vj
			}
			return vi < vj
		}

		var vi, vj string
		switch sortBy {
		case "ctime":
			vi, _ = links[i]["ctime"].(string)
			vj, _ = links[j]["ctime"].(string)
		case "creator":
			vi, _ = links[i]["creator_email"].(string)
			vj, _ = links[j]["creator_email"].(string)
		case "name":
			vi, _ = links[i]["obj_name"].(string)
			vj, _ = links[j]["obj_name"].(string)
		default:
			vi, _ = links[i]["ctime"].(string)
			vj, _ = links[j]["ctime"].(string)
		}
		if direction == "desc" {
			return vi > vj
		}
		return vi < vj
	})
}

type adminLinkProjectionRow struct {
	Token        string
	OrgID        string
	LibraryID    string
	RepoName     string
	FilePath     string
	ObjName      string
	CreatedBy    string
	CreatorEmail string
	CreatorName  string
	Permission   string
	ExpiresAt    *time.Time
	HasPassword  bool
	Active       bool
	ViewCount    int
	UploadCount  int
	CreatedAt    time.Time
}

func listAdminLinkBuckets(session *gocql.Session, linkType string) ([]string, error) {
	iter := session.Query(`SELECT bucket_day FROM admin_link_buckets WHERE link_type = ?`, linkType).Iter()
	var buckets []string
	var bucketDay string
	for iter.Scan(&bucketDay) {
		buckets = append(buckets, bucketDay)
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return buckets, nil
}

func listAdminLinkBucketsByOrg(session *gocql.Session, orgID, linkType string) ([]string, error) {
	iter := session.Query(`SELECT bucket_day FROM admin_link_buckets_by_org WHERE org_id = ? AND link_type = ?`, orgID, linkType).Iter()
	var buckets []string
	var bucketDay string
	for iter.Scan(&bucketDay) {
		buckets = append(buckets, bucketDay)
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return buckets, nil
}

func listAdminLinkProjectionRows(session *gocql.Session, linkType string) ([]adminLinkProjectionRow, error) {
	buckets, err := listAdminLinkBuckets(session, linkType)
	if err != nil {
		return nil, err
	}
	var rows []adminLinkProjectionRow
	for _, bucketDay := range buckets {
		iter := session.Query(`
			SELECT org_id, link_token, library_id, repo_name, file_path, obj_name,
			       created_by, creator_email, creator_name, permission, expires_at,
			       has_password, active, view_count, upload_count, created_at
			FROM admin_links_by_created
			WHERE link_type = ? AND bucket_day = ?
		`, linkType, bucketDay).Iter()

		var row adminLinkProjectionRow
		for iter.Scan(
			&row.OrgID,
			&row.Token,
			&row.LibraryID,
			&row.RepoName,
			&row.FilePath,
			&row.ObjName,
			&row.CreatedBy,
			&row.CreatorEmail,
			&row.CreatorName,
			&row.Permission,
			&row.ExpiresAt,
			&row.HasPassword,
			&row.Active,
			&row.ViewCount,
			&row.UploadCount,
			&row.CreatedAt,
		) {
			rows = append(rows, row)
			row = adminLinkProjectionRow{}
		}
		if err := iter.Close(); err != nil {
			return nil, err
		}
	}
	return rows, nil
}

func listAdminLinkProjectionRowsByOrg(session *gocql.Session, orgID, linkType string) ([]adminLinkProjectionRow, error) {
	buckets, err := listAdminLinkBucketsByOrg(session, orgID, linkType)
	if err != nil {
		return nil, err
	}
	var rows []adminLinkProjectionRow
	for _, bucketDay := range buckets {
		iter := session.Query(`
			SELECT link_token, library_id, repo_name, file_path, obj_name,
			       created_by, creator_email, creator_name, permission, expires_at,
			       has_password, active, view_count, upload_count, created_at
			FROM admin_links_by_org_created
			WHERE org_id = ? AND link_type = ? AND bucket_day = ?
		`, orgID, linkType, bucketDay).Iter()

		var row adminLinkProjectionRow
		for iter.Scan(
			&row.Token,
			&row.LibraryID,
			&row.RepoName,
			&row.FilePath,
			&row.ObjName,
			&row.CreatedBy,
			&row.CreatorEmail,
			&row.CreatorName,
			&row.Permission,
			&row.ExpiresAt,
			&row.HasPassword,
			&row.Active,
			&row.ViewCount,
			&row.UploadCount,
			&row.CreatedAt,
		) {
			row.OrgID = orgID
			rows = append(rows, row)
			row = adminLinkProjectionRow{}
		}
		if err := iter.Close(); err != nil {
			return nil, err
		}
	}
	return rows, nil
}

func listAdminLinkProjectionRowsByCreator(session *gocql.Session, orgID, createdBy, linkType string) ([]adminLinkProjectionRow, error) {
	buckets, err := listAdminLinkBucketsByOrg(session, orgID, linkType)
	if err != nil {
		return nil, err
	}
	var rows []adminLinkProjectionRow
	for _, bucketDay := range buckets {
		iter := session.Query(`
			SELECT link_token, library_id, repo_name, file_path, obj_name,
			       created_by, creator_email, creator_name, permission, expires_at,
			       has_password, active, view_count, upload_count, created_at
			FROM admin_links_by_org_created
			WHERE org_id = ? AND link_type = ? AND bucket_day = ?
		`, orgID, linkType, bucketDay).Iter()

		var row adminLinkProjectionRow
		for iter.Scan(
			&row.Token,
			&row.LibraryID,
			&row.RepoName,
			&row.FilePath,
			&row.ObjName,
			&row.CreatedBy,
			&row.CreatorEmail,
			&row.CreatorName,
			&row.Permission,
			&row.ExpiresAt,
			&row.HasPassword,
			&row.Active,
			&row.ViewCount,
			&row.UploadCount,
			&row.CreatedAt,
		) {
			if row.CreatedBy == createdBy {
				row.OrgID = orgID
				rows = append(rows, row)
			}
			row = adminLinkProjectionRow{}
		}
		if err := iter.Close(); err != nil {
			return nil, err
		}
	}
	return rows, nil
}

func normalizedAdminLinkRepoName(projected string) string {
	repoName := strings.TrimSpace(projected)
	if repoName == "" {
		return "Unknown Library"
	}
	return repoName
}

func normalizedAdminLinkCreatorInfo(createdBy, projectedEmail, projectedName string) (string, string) {
	creatorEmail := strings.TrimSpace(projectedEmail)
	creatorName := strings.TrimSpace(projectedName)
	if creatorEmail == "" {
		creatorEmail = createdBy
	}
	if creatorName == "" {
		creatorName = creatorEmail
	}
	return creatorEmail, creatorName
}

func adminLinkProjectionDisplay(row adminLinkProjectionRow) (string, string, string, string) {
	repoName := normalizedAdminLinkRepoName(row.RepoName)
	creatorEmail, creatorName := normalizedAdminLinkCreatorInfo(row.CreatedBy, row.CreatorEmail, row.CreatorName)
	objName := resolveAdminLinkObjName(row.ObjName, row.FilePath, repoName)
	return repoName, objName, creatorEmail, creatorName
}

func adminLinkProjectionState(row adminLinkProjectionRow) (bool, string) {
	if row.ExpiresAt == nil || row.ExpiresAt.IsZero() {
		return false, ""
	}
	return row.ExpiresAt.Before(time.Now()), row.ExpiresAt.Format(time.RFC3339)
}

func adminLinkProjectionMatches(row adminLinkProjectionRow, filters adminLinkListFilters, extraSearch ...string) bool {
	repoName, objName, creatorEmail, creatorName := adminLinkProjectionDisplay(row)
	isExpired, _ := adminLinkProjectionState(row)
	if !filters.MatchesState(row.Active, isExpired) {
		return false
	}
	searchValues := []string{objName, row.Token, row.FilePath, repoName, creatorEmail, creatorName}
	searchValues = append(searchValues, extraSearch...)
	return filters.MatchesSearch(searchValues...)
}

func listAdminLinkProjectionCursorPage(session *gocql.Session, linkType string, filters adminLinkListFilters, cursorParam string, perPage int) ([]adminLinkProjectionRow, string, bool, error) {
	buckets, err := listAdminLinkBuckets(session, linkType)
	if err != nil {
		return nil, "", false, err
	}
	sort.Sort(sort.Reverse(sort.StringSlice(buckets)))
	cursor, hasCursor, err := parseAdminLinkCursor(cursorParam)
	if err != nil {
		return nil, "", false, err
	}
	rows := make([]adminLinkProjectionRow, 0, perPage)
	var lastRow adminLinkProjectionRow
	var lastBucketDay string
	hasNext := false

	for _, bucketDay := range buckets {
		if adminLinkBucketBeforeCursor(bucketDay, cursor, hasCursor) {
			continue
		}
		query := `
			SELECT org_id, link_token, library_id, repo_name, file_path, obj_name,
			       created_by, creator_email, creator_name, permission, expires_at,
			       has_password, active, view_count, upload_count, created_at
			FROM admin_links_by_created
			WHERE link_type = ? AND bucket_day = ?
		`
		args := []interface{}{linkType, bucketDay}
		if hasCursor && bucketDay == cursor.BucketDay {
			query += ` AND created_at <= ?`
			args = append(args, cursor.CreatedAt)
		}
		iter := session.Query(query, args...).Iter()

		var row adminLinkProjectionRow
		for iter.Scan(
			&row.OrgID,
			&row.Token,
			&row.LibraryID,
			&row.RepoName,
			&row.FilePath,
			&row.ObjName,
			&row.CreatedBy,
			&row.CreatorEmail,
			&row.CreatorName,
			&row.Permission,
			&row.ExpiresAt,
			&row.HasPassword,
			&row.Active,
			&row.ViewCount,
			&row.UploadCount,
			&row.CreatedAt,
		) {
			if hasCursor && bucketDay == cursor.BucketDay && adminLinkGlobalRowAtOrBeforeCursor(row, cursor) {
				row = adminLinkProjectionRow{}
				continue
			}
			if !adminLinkProjectionMatches(row, filters) {
				row = adminLinkProjectionRow{}
				continue
			}
			if len(rows) < perPage {
				rows = append(rows, row)
				lastRow = row
				lastBucketDay = bucketDay
				row = adminLinkProjectionRow{}
				continue
			}
			hasNext = true
			break
		}
		if err := iter.Close(); err != nil {
			return nil, "", false, err
		}
		if hasNext {
			break
		}
	}

	nextCursor := ""
	if hasNext && len(rows) > 0 {
		nextCursor, err = adminLinkCursorFromRow(lastBucketDay, lastRow)
		if err != nil {
			return nil, "", false, err
		}
	}
	return rows, nextCursor, hasNext, nil
}

func listAdminLinkProjectionCursorPageByOrg(session *gocql.Session, orgID, linkType string, filters adminLinkListFilters, cursorParam string, perPage int) ([]adminLinkProjectionRow, string, bool, error) {
	buckets, err := listAdminLinkBucketsByOrg(session, orgID, linkType)
	if err != nil {
		return nil, "", false, err
	}
	sort.Sort(sort.Reverse(sort.StringSlice(buckets)))
	cursor, hasCursor, err := parseAdminLinkCursor(cursorParam)
	if err != nil {
		return nil, "", false, err
	}
	rows := make([]adminLinkProjectionRow, 0, perPage)
	var lastRow adminLinkProjectionRow
	var lastBucketDay string
	hasNext := false

	for _, bucketDay := range buckets {
		if adminLinkBucketBeforeCursor(bucketDay, cursor, hasCursor) {
			continue
		}
		query := `
			SELECT link_token, library_id, repo_name, file_path, obj_name,
			       created_by, creator_email, creator_name, permission, expires_at,
			       has_password, active, view_count, upload_count, created_at
			FROM admin_links_by_org_created
			WHERE org_id = ? AND link_type = ? AND bucket_day = ?
		`
		args := []interface{}{orgID, linkType, bucketDay}
		if hasCursor && bucketDay == cursor.BucketDay {
			query += ` AND created_at <= ?`
			args = append(args, cursor.CreatedAt)
		}
		iter := session.Query(query, args...).Iter()

		var row adminLinkProjectionRow
		for iter.Scan(
			&row.Token,
			&row.LibraryID,
			&row.RepoName,
			&row.FilePath,
			&row.ObjName,
			&row.CreatedBy,
			&row.CreatorEmail,
			&row.CreatorName,
			&row.Permission,
			&row.ExpiresAt,
			&row.HasPassword,
			&row.Active,
			&row.ViewCount,
			&row.UploadCount,
			&row.CreatedAt,
		) {
			row.OrgID = orgID
			if hasCursor && bucketDay == cursor.BucketDay && adminLinkScopedRowAtOrBeforeCursor(row, cursor) {
				row = adminLinkProjectionRow{}
				continue
			}
			if !adminLinkProjectionMatches(row, filters) {
				row = adminLinkProjectionRow{}
				continue
			}
			if len(rows) < perPage {
				rows = append(rows, row)
				lastRow = row
				lastBucketDay = bucketDay
				row = adminLinkProjectionRow{}
				continue
			}
			hasNext = true
			break
		}
		if err := iter.Close(); err != nil {
			return nil, "", false, err
		}
		if hasNext {
			break
		}
	}

	nextCursor := ""
	if hasNext && len(rows) > 0 {
		nextCursor, err = adminLinkCursorFromRow(lastBucketDay, lastRow)
		if err != nil {
			return nil, "", false, err
		}
	}
	return rows, nextCursor, hasNext, nil
}

func listAdminLinkProjectionCursorPageByCreator(session *gocql.Session, orgID, createdBy, linkType string, filters adminLinkListFilters, cursorParam string, perPage int) ([]adminLinkProjectionRow, string, bool, error) {
	buckets, err := listAdminLinkBucketsByOrg(session, orgID, linkType)
	if err != nil {
		return nil, "", false, err
	}
	sort.Sort(sort.Reverse(sort.StringSlice(buckets)))
	cursor, hasCursor, err := parseAdminLinkCursor(cursorParam)
	if err != nil {
		return nil, "", false, err
	}
	rows := make([]adminLinkProjectionRow, 0, perPage)
	var lastRow adminLinkProjectionRow
	var lastBucketDay string
	hasNext := false

	for _, bucketDay := range buckets {
		if adminLinkBucketBeforeCursor(bucketDay, cursor, hasCursor) {
			continue
		}
		query := `
			SELECT link_token, library_id, repo_name, file_path, obj_name,
			       created_by, creator_email, creator_name, permission, expires_at,
			       has_password, active, view_count, upload_count, created_at
			FROM admin_links_by_org_created
			WHERE org_id = ? AND link_type = ? AND bucket_day = ?
		`
		args := []interface{}{orgID, linkType, bucketDay}
		if hasCursor && bucketDay == cursor.BucketDay {
			query += ` AND created_at <= ?`
			args = append(args, cursor.CreatedAt)
		}
		iter := session.Query(query, args...).Iter()

		var row adminLinkProjectionRow
		for iter.Scan(
			&row.Token,
			&row.LibraryID,
			&row.RepoName,
			&row.FilePath,
			&row.ObjName,
			&row.CreatedBy,
			&row.CreatorEmail,
			&row.CreatorName,
			&row.Permission,
			&row.ExpiresAt,
			&row.HasPassword,
			&row.Active,
			&row.ViewCount,
			&row.UploadCount,
			&row.CreatedAt,
		) {
			row.OrgID = orgID
			if row.CreatedBy != createdBy {
				row = adminLinkProjectionRow{}
				continue
			}
			if hasCursor && bucketDay == cursor.BucketDay && adminLinkScopedRowAtOrBeforeCursor(row, cursor) {
				row = adminLinkProjectionRow{}
				continue
			}
			if !adminLinkProjectionMatches(row, filters) {
				row = adminLinkProjectionRow{}
				continue
			}
			if len(rows) < perPage {
				rows = append(rows, row)
				lastRow = row
				lastBucketDay = bucketDay
				row = adminLinkProjectionRow{}
				continue
			}
			hasNext = true
			break
		}
		if err := iter.Close(); err != nil {
			return nil, "", false, err
		}
		if hasNext {
			break
		}
	}

	nextCursor := ""
	if hasNext && len(rows) > 0 {
		nextCursor, err = adminLinkCursorFromRow(lastBucketDay, lastRow)
		if err != nil {
			return nil, "", false, err
		}
	}
	return rows, nextCursor, hasNext, nil
}

func listAdminLinkProjectionPage(session *gocql.Session, linkType string, filters adminLinkListFilters, page, perPage int) ([]adminLinkProjectionRow, int, bool, error) {
	buckets, err := listAdminLinkBuckets(session, linkType)
	if err != nil {
		return nil, 0, false, err
	}
	sort.Sort(sort.Reverse(sort.StringSlice(buckets)))
	offset := (page - 1) * perPage
	if offset < 0 {
		offset = 0
	}
	rows := make([]adminLinkProjectionRow, 0, perPage)
	total := 0
	for _, bucketDay := range buckets {
		iter := session.Query(`
			SELECT org_id, link_token, library_id, repo_name, file_path, obj_name,
			       created_by, creator_email, creator_name, permission, expires_at,
			       has_password, active, view_count, upload_count, created_at
			FROM admin_links_by_created
			WHERE link_type = ? AND bucket_day = ?
		`, linkType, bucketDay).Iter()

		var row adminLinkProjectionRow
		for iter.Scan(
			&row.OrgID,
			&row.Token,
			&row.LibraryID,
			&row.RepoName,
			&row.FilePath,
			&row.ObjName,
			&row.CreatedBy,
			&row.CreatorEmail,
			&row.CreatorName,
			&row.Permission,
			&row.ExpiresAt,
			&row.HasPassword,
			&row.Active,
			&row.ViewCount,
			&row.UploadCount,
			&row.CreatedAt,
		) {
			if adminLinkProjectionMatches(row, filters) {
				if total >= offset && len(rows) < perPage {
					rows = append(rows, row)
				}
				total++
			}
			row = adminLinkProjectionRow{}
		}
		if err := iter.Close(); err != nil {
			return nil, 0, false, err
		}
	}
	return rows, total, offset+len(rows) < total, nil
}

func listAdminLinkProjectionPageByOrg(session *gocql.Session, orgID, linkType string, filters adminLinkListFilters, page, perPage int) ([]adminLinkProjectionRow, int, bool, error) {
	buckets, err := listAdminLinkBucketsByOrg(session, orgID, linkType)
	if err != nil {
		return nil, 0, false, err
	}
	sort.Sort(sort.Reverse(sort.StringSlice(buckets)))
	offset := (page - 1) * perPage
	if offset < 0 {
		offset = 0
	}
	rows := make([]adminLinkProjectionRow, 0, perPage)
	total := 0
	for _, bucketDay := range buckets {
		iter := session.Query(`
			SELECT link_token, library_id, repo_name, file_path, obj_name,
			       created_by, creator_email, creator_name, permission, expires_at,
			       has_password, active, view_count, upload_count, created_at
			FROM admin_links_by_org_created
			WHERE org_id = ? AND link_type = ? AND bucket_day = ?
		`, orgID, linkType, bucketDay).Iter()

		var row adminLinkProjectionRow
		for iter.Scan(
			&row.Token,
			&row.LibraryID,
			&row.RepoName,
			&row.FilePath,
			&row.ObjName,
			&row.CreatedBy,
			&row.CreatorEmail,
			&row.CreatorName,
			&row.Permission,
			&row.ExpiresAt,
			&row.HasPassword,
			&row.Active,
			&row.ViewCount,
			&row.UploadCount,
			&row.CreatedAt,
		) {
			row.OrgID = orgID
			if adminLinkProjectionMatches(row, filters) {
				if total >= offset && len(rows) < perPage {
					rows = append(rows, row)
				}
				total++
			}
			row = adminLinkProjectionRow{}
		}
		if err := iter.Close(); err != nil {
			return nil, 0, false, err
		}
	}
	return rows, total, offset+len(rows) < total, nil
}

func listAdminLinkProjectionPageByCreator(session *gocql.Session, orgID, createdBy, linkType string, filters adminLinkListFilters, page, perPage int) ([]adminLinkProjectionRow, int, bool, error) {
	buckets, err := listAdminLinkBucketsByOrg(session, orgID, linkType)
	if err != nil {
		return nil, 0, false, err
	}
	sort.Sort(sort.Reverse(sort.StringSlice(buckets)))
	offset := (page - 1) * perPage
	if offset < 0 {
		offset = 0
	}
	rows := make([]adminLinkProjectionRow, 0, perPage)
	total := 0
	for _, bucketDay := range buckets {
		iter := session.Query(`
			SELECT link_token, library_id, repo_name, file_path, obj_name,
			       created_by, creator_email, creator_name, permission, expires_at,
			       has_password, active, view_count, upload_count, created_at
			FROM admin_links_by_org_created
			WHERE org_id = ? AND link_type = ? AND bucket_day = ?
		`, orgID, linkType, bucketDay).Iter()

		var row adminLinkProjectionRow
		for iter.Scan(
			&row.Token,
			&row.LibraryID,
			&row.RepoName,
			&row.FilePath,
			&row.ObjName,
			&row.CreatedBy,
			&row.CreatorEmail,
			&row.CreatorName,
			&row.Permission,
			&row.ExpiresAt,
			&row.HasPassword,
			&row.Active,
			&row.ViewCount,
			&row.UploadCount,
			&row.CreatedAt,
		) {
			row.OrgID = orgID
			if row.CreatedBy == createdBy && adminLinkProjectionMatches(row, filters) {
				if total >= offset && len(rows) < perPage {
					rows = append(rows, row)
				}
				total++
			}
			row = adminLinkProjectionRow{}
		}
		if err := iter.Close(); err != nil {
			return nil, 0, false, err
		}
	}
	return rows, total, offset+len(rows) < total, nil
}

func resolveAdminLinkRepoName(session *gocql.Session, cache map[string]string, orgID, libraryID, projected string) string {
	cacheKey := orgID + ":" + libraryID
	if repoName, ok := cache[cacheKey]; ok {
		return repoName
	}
	repoName := normalizedAdminLinkRepoName(projected)
	cache[cacheKey] = repoName
	return repoName
}

func resolveAdminLinkCreatorInfo(session *gocql.Session, cache map[string][2]string, orgID, createdBy, projectedEmail, projectedName string) (string, string) {
	cacheKey := orgID + ":" + createdBy
	if info, ok := cache[cacheKey]; ok {
		return info[0], info[1]
	}
	creatorEmail, creatorName := normalizedAdminLinkCreatorInfo(createdBy, projectedEmail, projectedName)
	info := [2]string{creatorEmail, creatorName}
	cache[cacheKey] = info
	return info[0], info[1]
}

func resolveAdminLinkObjName(projectedObjName, filePath, repoName string) string {
	if strings.TrimSpace(projectedObjName) != "" {
		return projectedObjName
	}
	return db.AdminLinkObjName(filePath, repoName)
}
