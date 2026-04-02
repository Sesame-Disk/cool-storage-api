package v2

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestParseAdminLinkListFilters_MapsLegacyStatus(t *testing.T) {
	filters, err := parseAdminLinkListFilters("active", "", "false", "  Repo Alpha ")
	if err != nil {
		t.Fatalf("parseAdminLinkListFilters returned error: %v", err)
	}

	if !filters.HasActiveFilter || !filters.ActiveFilter {
		t.Fatalf("expected active filter to be enabled and true: %#v", filters)
	}
	if !filters.HasExpiredFilter || filters.ExpiredFilter {
		t.Fatalf("expected expired filter to be enabled and false: %#v", filters)
	}
	if filters.Search != "repo alpha" {
		t.Fatalf("search = %q, want %q", filters.Search, "repo alpha")
	}
}

func TestParseAdminLinkListFilters_RejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name       string
		status     string
		active     string
		expired    string
		search     string
		wantErrMsg string
	}{
		{name: "status", status: "paused", wantErrMsg: "invalid status filter"},
		{name: "active", active: "truthy", wantErrMsg: "invalid active filter"},
		{name: "expired", expired: "later", wantErrMsg: "invalid expired filter"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := parseAdminLinkListFilters(test.status, test.active, test.expired, test.search)
			if err == nil {
				t.Fatalf("expected error %q, got nil", test.wantErrMsg)
			}
			if err.Error() != test.wantErrMsg {
				t.Fatalf("error = %q, want %q", err.Error(), test.wantErrMsg)
			}
		})
	}
}

func TestAdminLinkListFilters_MatchesStateAndSearch(t *testing.T) {
	filters := adminLinkListFilters{
		HasActiveFilter:  true,
		ActiveFilter:     true,
		HasExpiredFilter: true,
		ExpiredFilter:    false,
		Search:           "alice",
	}

	if !filters.MatchesState(true, false) {
		t.Fatal("expected state to match active/non-expired link")
	}
	if filters.MatchesState(false, false) {
		t.Fatal("expected inactive link to be filtered out")
	}
	if filters.MatchesState(true, true) {
		t.Fatal("expected expired link to be filtered out")
	}
	if !filters.MatchesSearch("Quarterly Plan.pdf", "ALICE@example.com") {
		t.Fatal("expected search to match creator email case-insensitively")
	}
	if filters.MatchesSearch("Quarterly Plan.pdf", "bob@example.com") {
		t.Fatal("expected unrelated values not to match search")
	}
}

func TestParseAdminLinkListFiltersFromContext_IncludeActive(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	req := httptest.NewRequest("GET", "/?active=false&expired=false&search=Alpha", nil)
	c.Request = req

	filters, err := parseAdminLinkListFiltersFromContext(c, true)
	if err != nil {
		t.Fatalf("parseAdminLinkListFiltersFromContext returned error: %v", err)
	}
	if !filters.HasActiveFilter || filters.ActiveFilter {
		t.Fatalf("expected active filter false to be applied: %#v", filters)
	}
	if !filters.HasExpiredFilter || filters.ExpiredFilter {
		t.Fatalf("expected expired filter false to be applied: %#v", filters)
	}
	if filters.Search != "alpha" {
		t.Fatalf("search = %q, want %q", filters.Search, "alpha")
	}

	filters, err = parseAdminLinkListFiltersFromContext(c, false)
	if err != nil {
		t.Fatalf("parseAdminLinkListFiltersFromContext returned error: %v", err)
	}
	if filters.HasActiveFilter {
		t.Fatalf("expected active filter to be ignored when includeActive=false: %#v", filters)
	}
}

func TestMatchesAdminLinkSearch_MatchesAcrossFields(t *testing.T) {
	if !matchesAdminLinkSearch("folder", "repo alpha", "/Shared/Folder/report.pdf") {
		t.Fatal("expected search to match path")
	}
	if !matchesAdminLinkSearch("tok-123", "Quarterly Report", "tok-123") {
		t.Fatal("expected search to match token")
	}
	if matchesAdminLinkSearch("missing", "Quarterly Report", "tok-123") {
		t.Fatal("did not expect search to match unrelated fields")
	}
	if !matchesAdminLinkSearch("", "anything") {
		t.Fatal("empty search should match all values")
	}
}

func TestParseAdminLinkPageParams_NormalizesAndCaps(t *testing.T) {
	page, perPage := parseAdminLinkPageParams("0", "999", 25, 100)
	if page != 1 || perPage != 100 {
		t.Fatalf("page/perPage = %d/%d, want 1/100", page, perPage)
	}

	page, perPage = parseAdminLinkPageParams("bad", "bad", 25, 0)
	if page != 1 || perPage != 25 {
		t.Fatalf("page/perPage = %d/%d, want 1/25", page, perPage)
	}
}

func TestPaginateAdminLinks_ReturnsWindowAndNextFlag(t *testing.T) {
	links := []gin.H{
		{"token": "a"},
		{"token": "b"},
		{"token": "c"},
	}

	paged, total, pageNext := paginateAdminLinks(links, 2, 2)
	if total != 3 {
		t.Fatalf("total = %d, want 3", total)
	}
	if pageNext {
		t.Fatal("pageNext = true, want false")
	}
	if len(paged) != 1 || paged[0]["token"] != "c" {
		t.Fatalf("unexpected paged result: %#v", paged)
	}

	paged, total, pageNext = paginateAdminLinks(links, 1, 2)
	if total != 3 || !pageNext {
		t.Fatalf("expected first page to have next flag, got total=%d pageNext=%v", total, pageNext)
	}
	if len(paged) != 2 || paged[0]["token"] != "a" || paged[1]["token"] != "b" {
		t.Fatalf("unexpected first page result: %#v", paged)
	}
}

func TestAdminLinkCursor_RoundTrip(t *testing.T) {
	cursorValue, err := buildAdminLinkCursor(adminLinkPageCursor{
		BucketDay: "2026-04-02",
		CreatedAt: time.Date(2026, 4, 2, 8, 30, 0, 0, time.UTC),
		OrgID:     "org-1",
		Token:     "tok-1",
	})
	if err != nil {
		t.Fatalf("buildAdminLinkCursor returned error: %v", err)
	}

	parsed, ok, err := parseAdminLinkCursor(cursorValue)
	if err != nil {
		t.Fatalf("parseAdminLinkCursor returned error: %v", err)
	}
	if !ok {
		t.Fatal("expected cursor to be present")
	}
	if parsed.BucketDay != "2026-04-02" || parsed.OrgID != "org-1" || parsed.Token != "tok-1" {
		t.Fatalf("unexpected parsed cursor: %#v", parsed)
	}
	if !parsed.CreatedAt.Equal(time.Date(2026, 4, 2, 8, 30, 0, 0, time.UTC)) {
		t.Fatalf("created_at = %v, want exact roundtrip", parsed.CreatedAt)
	}
}

func TestParseAdminLinkCursor_RejectsInvalidPayload(t *testing.T) {
	if _, ok, err := parseAdminLinkCursor(""); err != nil || ok {
		t.Fatalf("empty cursor should be ignored, got ok=%v err=%v", ok, err)
	}
	if _, _, err := parseAdminLinkCursor("not-base64"); err == nil {
		t.Fatal("expected invalid base64 cursor to fail")
	}
}

func TestAdminLinkCursorComparisons(t *testing.T) {
	cursor := adminLinkPageCursor{
		BucketDay: "2026-04-02",
		CreatedAt: time.Date(2026, 4, 2, 8, 30, 0, 0, time.UTC),
		OrgID:     "80000000-0000-0000-0000-000000000000",
		Token:     "tok-2",
	}

	if !adminLinkBucketBeforeCursor("2026-04-03", cursor, true) {
		t.Fatal("expected newer bucket to be skipped")
	}
	if adminLinkBucketBeforeCursor("2026-04-01", cursor, true) {
		t.Fatal("did not expect older bucket to be skipped")
	}

	newerRow := adminLinkProjectionRow{OrgID: "70000000-0000-0000-0000-000000000000", Token: "tok-1", CreatedAt: cursor.CreatedAt.Add(time.Minute)}
	if !adminLinkGlobalRowAtOrBeforeCursor(newerRow, cursor) {
		t.Fatal("expected newer global row to be treated as already seen")
	}
	olderRow := adminLinkProjectionRow{OrgID: "90000000-0000-0000-0000-000000000000", Token: "tok-9", CreatedAt: cursor.CreatedAt.Add(-time.Minute)}
	if adminLinkGlobalRowAtOrBeforeCursor(olderRow, cursor) {
		t.Fatal("did not expect older global row to be treated as already seen")
	}
	sameGlobalRow := adminLinkProjectionRow{OrgID: "80000000-0000-0000-0000-000000000000", Token: "tok-2", CreatedAt: cursor.CreatedAt}
	if !adminLinkGlobalRowAtOrBeforeCursor(sameGlobalRow, cursor) {
		t.Fatal("expected identical global row to be skipped on resume")
	}
	afterCursorByCassandraOrder := adminLinkProjectionRow{OrgID: "7fffffff-ffff-ffff-ffff-ffffffffffff", Token: "tok-1", CreatedAt: cursor.CreatedAt}
	if adminLinkGlobalRowAtOrBeforeCursor(afterCursorByCassandraOrder, cursor) {
		t.Fatal("did not expect row with later Cassandra UUID order to be skipped")
	}
	sameScopedRow := adminLinkProjectionRow{Token: "tok-2", CreatedAt: cursor.CreatedAt}
	if !adminLinkScopedRowAtOrBeforeCursor(sameScopedRow, cursor) {
		t.Fatal("expected identical scoped row to be skipped on resume")
	}
	afterScopedRow := adminLinkProjectionRow{Token: "tok-3", CreatedAt: cursor.CreatedAt}
	if adminLinkScopedRowAtOrBeforeCursor(afterScopedRow, cursor) {
		t.Fatal("did not expect later scoped row to be skipped")
	}
}

func TestValidateAdminLinkScope(t *testing.T) {
	tests := []struct {
		name          string
		actualOrgID   string
		expectedOrgID string
		actualType    string
		expectedType  string
		wantErr       error
	}{
		{name: "matching org and type", actualOrgID: "org-a", expectedOrgID: "org-a", actualType: "share", expectedType: "share"},
		{name: "wrong org", actualOrgID: "org-b", expectedOrgID: "org-a", actualType: "share", expectedType: "share", wantErr: errAdminLinkWrongOrg},
		{name: "wrong type", actualOrgID: "org-a", expectedOrgID: "org-a", actualType: "upload", expectedType: "share", wantErr: errAdminLinkWrongType},
		{name: "type only check", actualType: "upload", expectedType: "upload"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateAdminLinkScope(test.actualOrgID, test.expectedOrgID, test.actualType, test.expectedType)
			if err != test.wantErr {
				t.Fatalf("error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestResolveAdminLinkObjName_FallsBackToPathRules(t *testing.T) {
	if got := resolveAdminLinkObjName("", "/folder/report.pdf", "Repo"); got != "report.pdf" {
		t.Fatalf("obj_name = %q, want %q", got, "report.pdf")
	}
	if got := resolveAdminLinkObjName("Projected Name", "/folder/report.pdf", "Repo"); got != "Projected Name" {
		t.Fatalf("obj_name = %q, want projected value", got)
	}
	if got := resolveAdminLinkObjName("", "/", "Repo"); got != "Repo" {
		t.Fatalf("root obj_name = %q, want %q", got, "Repo")
	}
}

func TestAdminLinkProjectionRowsSortByCreatedTimeDesc(t *testing.T) {
	now := time.Now().UTC()
	links := []gin.H{
		{"ctime": now.Add(-time.Hour).Format(time.RFC3339), "token": "older"},
		{"ctime": now.Format(time.RFC3339), "token": "newer"},
	}
	sortAdminLinks(links, "", "")
	if links[0]["token"] != "newer" {
		t.Fatalf("expected newest link first, got %#v", links)
	}
}

func TestNormalizeAdminLinkSort_DefaultsToCreatedDesc(t *testing.T) {
	sortBy, direction := normalizeAdminLinkSort("", "")
	if sortBy != "ctime" || direction != "desc" {
		t.Fatalf("sort = %s/%s, want ctime/desc", sortBy, direction)
	}
	if !isDefaultAdminLinkSort("", "") {
		t.Fatal("expected empty sort params to use default optimized order")
	}
	if isDefaultAdminLinkSort("name", "asc") {
		t.Fatal("did not expect name/asc to use default optimized order")
	}
}

func TestAdminLinkProjectionDisplay_UsesProjectedFields(t *testing.T) {
	row := adminLinkProjectionRow{
		CreatedBy:    "user-id",
		RepoName:     "Projected Repo",
		FilePath:     "/folder/report.pdf",
		ObjName:      "Projected File",
		CreatorEmail: "user@example.com",
		CreatorName:  "User Name",
	}

	repoName, objName, creatorEmail, creatorName := adminLinkProjectionDisplay(row)
	if repoName != "Projected Repo" || objName != "Projected File" {
		t.Fatalf("unexpected projected names: repo=%q obj=%q", repoName, objName)
	}
	if creatorEmail != "user@example.com" || creatorName != "User Name" {
		t.Fatalf("unexpected projected creator info: %q / %q", creatorEmail, creatorName)
	}
}
