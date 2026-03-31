package v2

import (
	"testing"

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
