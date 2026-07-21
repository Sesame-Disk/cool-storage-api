package db

import (
	"errors"
	"testing"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

func TestScanBlockStorageLocation(t *testing.T) {
	t.Run("found", func(t *testing.T) {
		location, found, err := scanBlockStorageLocation(func(dest ...interface{}) error {
			*(dest[0].(*int64)) = 123
			*(dest[1].(*string)) = "hot"
			*(dest[2].(*string)) = "canonical/key"
			*(dest[3].(*string)) = BlockGCStateDeleting
			return nil
		})
		if err != nil || !found {
			t.Fatalf("scanBlockStorageLocation() = (%+v, %t, %v), want found", location, found, err)
		}
		if location.SizeBytes != 123 || location.StorageClass != "hot" || location.StorageKey != "canonical/key" || location.GCState != BlockGCStateDeleting {
			t.Fatalf("location = %+v, want size=123 class=hot key=canonical/key gc_state=deleting", location)
		}
	})

	t.Run("not found", func(t *testing.T) {
		location, found, err := scanBlockStorageLocation(func(...interface{}) error {
			return gocql.ErrNotFound
		})
		if err != nil || found || location != (BlockStorageLocation{}) {
			t.Fatalf("scanBlockStorageLocation() = (%+v, %t, %v), want zero, false, nil", location, found, err)
		}
	})

	t.Run("infrastructure error", func(t *testing.T) {
		infrastructureErr := errors.New("cassandra unavailable")
		_, found, err := scanBlockStorageLocation(func(...interface{}) error {
			return infrastructureErr
		})
		if found || !errors.Is(err, infrastructureErr) {
			t.Fatalf("scanBlockStorageLocation() = (_, %t, %v), want false and infrastructure error", found, err)
		}
	})
}
