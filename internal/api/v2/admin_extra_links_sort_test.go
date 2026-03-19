package v2

import (
	"testing"

	"github.com/gin-gonic/gin"
)

func TestSortAdminLinks_ByViewCountAsc(t *testing.T) {
	links := []gin.H{
		{"token": "a", "view_cnt": 10},
		{"token": "b", "view_cnt": 2},
		{"token": "c", "view_cnt": 30},
	}

	sortAdminLinks(links, "view_cnt", "asc")

	if links[0]["token"] != "b" || links[1]["token"] != "a" || links[2]["token"] != "c" {
		t.Fatalf("unexpected order by view_cnt asc: %#v", links)
	}
}

func TestSortAdminLinks_ByViewCountDesc(t *testing.T) {
	links := []gin.H{
		{"token": "a", "view_cnt": 10},
		{"token": "b", "view_cnt": 2},
		{"token": "c", "view_cnt": 30},
	}

	sortAdminLinks(links, "view_cnt", "desc")

	if links[0]["token"] != "c" || links[1]["token"] != "a" || links[2]["token"] != "b" {
		t.Fatalf("unexpected order by view_cnt desc: %#v", links)
	}
}

func TestSortAdminLinks_ByCreatedTimeDesc(t *testing.T) {
	links := []gin.H{
		{"token": "a", "ctime": "2026-01-01T00:00:00Z"},
		{"token": "b", "ctime": "2026-03-01T00:00:00Z"},
		{"token": "c", "ctime": "2026-02-01T00:00:00Z"},
	}

	sortAdminLinks(links, "ctime", "desc")

	if links[0]["token"] != "b" || links[1]["token"] != "c" || links[2]["token"] != "a" {
		t.Fatalf("unexpected order by ctime desc: %#v", links)
	}
}

func TestSortAdminLinks_EmptySortByNoop(t *testing.T) {
	links := []gin.H{
		{"token": "a", "view_cnt": 1},
		{"token": "b", "view_cnt": 2},
	}

	sortAdminLinks(links, "", "asc")

	if links[0]["token"] != "a" || links[1]["token"] != "b" {
		t.Fatalf("unexpected order when sortBy empty: %#v", links)
	}
}
