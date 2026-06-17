package db

import (
	"testing"
	"time"
)

func TestAdminUserProjectionStateMatchesEntry(t *testing.T) {
	createdAt := time.Unix(1711987200, 0).UTC()
	state := AdminUserProjectionState{
		UserID:    "user-1",
		OrgID:     "org-1",
		Status:    "active",
		CreatedAt: createdAt,
	}

	tests := []struct {
		name      string
		orgID     string
		createdAt time.Time
		status    string
		want      bool
	}{
		{name: "exact match", orgID: "org-1", createdAt: createdAt, status: "active", want: true},
		{name: "blank status normalizes to active", orgID: "org-1", createdAt: createdAt, status: "", want: true},
		{name: "different status", orgID: "org-1", createdAt: createdAt, status: "deleted", want: false},
		{name: "different org", orgID: "org-2", createdAt: createdAt, status: "active", want: false},
		{name: "different created at", orgID: "org-1", createdAt: createdAt.Add(time.Second), status: "active", want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := adminUserProjectionStateMatchesEntry(state, test.orgID, test.createdAt, test.status); got != test.want {
				t.Fatalf("adminUserProjectionStateMatchesEntry() = %v, want %v", got, test.want)
			}
		})
	}
}
