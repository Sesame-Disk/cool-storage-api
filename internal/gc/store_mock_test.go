package gc

import (
	"errors"
	"testing"
	"time"

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
