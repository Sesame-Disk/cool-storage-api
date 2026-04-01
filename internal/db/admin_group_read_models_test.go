package db

import (
	"testing"
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

func TestOptionalGroupParentIDString(t *testing.T) {
	if got := optionalGroupParentIDString(gocql.UUID{}); got != "" {
		t.Fatalf("zero UUID parent should decode as empty string, got %q", got)
	}

	parentGroupID, err := gocql.RandomUUID()
	if err != nil {
		t.Fatalf("failed to generate UUID: %v", err)
	}
	if got := optionalGroupParentIDString(parentGroupID); got != parentGroupID.String() {
		t.Fatalf("decoded parent group id = %q, want %q", got, parentGroupID.String())
	}
}

func TestAddUpsertAdminGroupReadModelQueryAlwaysIncludesParentGroupID(t *testing.T) {
	batch := &gocql.Batch{}
	row := AdminGroupProjectionRow{
		OrgID:        "00000000-0000-0000-0000-000000000001",
		GroupID:      "00000000-0000-0000-0000-000000000002",
		Name:         "Projection Test Group",
		CreatorID:    "00000000-0000-0000-0000-000000000003",
		OwnerEmail:   "admin@sesamefs.local",
		OwnerName:    "Admin",
		IsDepartment: false,
		CreatedAt:    time.Unix(1711987200, 0).UTC(),
	}

	AddUpsertAdminGroupReadModelQuery(batch, row)

	if len(batch.Entries) != 2 {
		t.Fatalf("expected 2 batch entries, got %d", len(batch.Entries))
	}

	args := batch.Entries[1].Args
	if len(args) != 10 {
		t.Fatalf("expected 10 args for root-group projection insert, got %d", len(args))
	}
	if got := batch.Entries[1].Stmt; got == "" {
		t.Fatal("expected projection insert statement for root group")
	}
	if got := batch.Entries[1].Args[8]; got != nil {
		t.Fatalf("expected nil parent_group_id arg for root group, got %#v", got)
	}

	batchWithParent := &gocql.Batch{}
	row.ParentGroupID = "00000000-0000-0000-0000-000000000099"
	AddUpsertAdminGroupReadModelQuery(batchWithParent, row)
	if got := batchWithParent.Entries[1].Args[8]; got != row.ParentGroupID {
		t.Fatalf("expected parent_group_id arg %q, got %#v", row.ParentGroupID, got)
	}
}
