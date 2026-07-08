package gc

import (
	"errors"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/google/uuid"
)

func TestMockStore_GetLibraryBlockRepresentationID_RejectsOrgMismatchOnDeletedLibrary(t *testing.T) {
	store := NewMockStore()
	orgID := uuid.New()
	otherOrgID := uuid.New()
	libID := uuid.New()

	store.AddDeletedLibrary(orgID, libID, "hot", time.Now().UTC())

	if _, err := store.GetLibraryBlockRepresentationID(otherOrgID, libID); !errors.Is(err, gocql.ErrNotFound) {
		t.Fatalf("GetLibraryBlockRepresentationID error = %v, want %v", err, gocql.ErrNotFound)
	}
}

func TestMockStore_EnqueueBatchRejectsForeignRepresentation(t *testing.T) {
	store := NewMockStore()
	orgID := uuid.New()
	libID := uuid.New()

	err := store.EnqueueBatch([]QueueItem{{
		OrgID:                 orgID,
		QueuedAt:              time.Now().UTC(),
		ItemType:              ItemCommit,
		ItemID:                "commit-1",
		LibraryID:             libID,
		BlockRepresentationID: db.EncryptedLibraryBlockRepresentationID(uuid.NewString()),
	}})
	if err == nil {
		t.Fatal("expected direct store enqueue to reject foreign representation")
	}
}
