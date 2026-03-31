package v2

import (
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/gin-gonic/gin"
)

type departmentGroupRecord struct {
	ID            string
	Name          string
	ParentGroupID string
	CreatedAt     time.Time
}

func listDepartmentGroupRecords(session *gocql.Session, orgID string) []departmentGroupRecord {
	if session == nil {
		return []departmentGroupRecord{}
	}

	iter := session.Query(`
		SELECT group_id, name, parent_group_id, is_department, created_at
		FROM groups WHERE org_id = ?
	`, orgID).Iter()

	records := make([]departmentGroupRecord, 0)
	var groupID, name string
	var parentGroupID *string
	var isDepartment bool
	var createdAt time.Time

	for iter.Scan(&groupID, &name, &parentGroupID, &isDepartment, &createdAt) {
		if !isDepartment {
			continue
		}

		parent := ""
		if parentGroupID != nil {
			parent = *parentGroupID
		}

		records = append(records, departmentGroupRecord{
			ID:            groupID,
			Name:          name,
			ParentGroupID: parent,
			CreatedAt:     createdAt,
		})
	}
	_ = iter.Close()

	return records
}

func departmentGroupListData(records []departmentGroupRecord, quotaLookup func(string) int64) []gin.H {
	if len(records) == 0 {
		return []gin.H{}
	}

	results := make([]gin.H, 0, len(records))
	for _, record := range records {
		row := gin.H{
			"id":              record.ID,
			"name":            record.Name,
			"parent_group_id": record.ParentGroupID,
			"created_at":      record.CreatedAt.Format(time.RFC3339),
		}
		if quotaLookup != nil {
			row["quota"] = quotaLookup(record.ID)
		}
		results = append(results, row)
	}

	return results
}