package db

import (
	"errors"
	"testing"
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

func TestScanBlockStorageLocation(t *testing.T) {
	t.Run("found", func(t *testing.T) {
		createdAt := time.Now().UTC()
		location, found, err := scanBlockStorageLocation(func(dest ...interface{}) error {
			*(dest[0].(*int64)) = 123
			*(dest[1].(*string)) = "hot"
			*(dest[2].(*string)) = "canonical/key"
			*(dest[3].(*string)) = BlockGCStateDeleting
			*(dest[4].(**time.Time)) = &createdAt
			return nil
		})
		if err != nil || !found {
			t.Fatalf("scanBlockStorageLocation() = (%+v, %t, %v), want found", location, found, err)
		}
		want := BlockStorageLocation{SizeBytes: 123, StorageClass: "hot", StorageKey: "canonical/key", GCState: BlockGCStateDeleting, CreatedAt: &createdAt}
		if location != want {
			t.Fatalf("location = %+v, want %+v", location, want)
		}
	})

	t.Run("not found", func(t *testing.T) {
		location, found, err := scanBlockStorageLocation(func(...interface{}) error { return gocql.ErrNotFound })
		if err != nil || found || location != (BlockStorageLocation{}) {
			t.Fatalf("scanBlockStorageLocation() = (%+v, %t, %v), want zero, false, nil", location, found, err)
		}
	})

	t.Run("database error", func(t *testing.T) {
		databaseErr := errors.New("cassandra unavailable")
		_, found, err := scanBlockStorageLocation(func(...interface{}) error { return databaseErr })
		if found || !errors.Is(err, databaseErr) {
			t.Fatalf("scanBlockStorageLocation() = (_, %t, %v), want false and database error", found, err)
		}
	})
}
