package v2

import (
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestYearToDateMonthKeys(t *testing.T) {
	now := time.Date(2026, time.March, 31, 9, 45, 0, 0, time.UTC)
	assert.Equal(t, []string{"202601", "202602", "202603"}, yearToDateMonthKeys(now))
}

func TestParseDateParam(t *testing.T) {
	fallback := time.Date(2026, time.March, 1, 12, 34, 56, 0, time.UTC)

	parsed := parseDateParam("2026-03-31 14:15:16", fallback)
	assert.Equal(t, time.Date(2026, time.March, 31, 0, 0, 0, 0, time.UTC), parsed)

	assert.Equal(t, fallback, parseDateParam("not-a-date", fallback))
}

func TestSortTrafficRows_DefaultsDescendingAndBreaksTiesByName(t *testing.T) {
	rows := []gin.H{
		{"name": "zeta", "total_bytes": int64(10)},
		{"name": "alpha", "total_bytes": int64(20)},
		{"name": "beta", "total_bytes": int64(20)},
	}

	sortTrafficRows(rows, "", "total_bytes")

	assert.Equal(t, "alpha", rows[0]["name"])
	assert.Equal(t, "beta", rows[1]["name"])
	assert.Equal(t, "zeta", rows[2]["name"])
}

func TestDepartmentGroupListData(t *testing.T) {
	createdAt := time.Date(2026, time.March, 31, 10, 0, 0, 0, time.UTC)
	records := []departmentGroupRecord{{
		ID:            "group-1",
		Name:          "Engineering",
		ParentGroupID: "root",
		CreatedAt:     createdAt,
	}}

	rows := departmentGroupListData(records, func(groupID string) int64 {
		assert.Equal(t, "group-1", groupID)
		return 42
	})

	assert.Len(t, rows, 1)
	assert.Equal(t, "group-1", rows[0]["id"])
	assert.Equal(t, "Engineering", rows[0]["name"])
	assert.Equal(t, "root", rows[0]["parent_group_id"])
	assert.Equal(t, createdAt.Format(time.RFC3339), rows[0]["created_at"])
	assert.Equal(t, int64(42), rows[0]["quota"])

	assert.Equal(t, []gin.H{}, departmentGroupListData(nil, nil))
}
