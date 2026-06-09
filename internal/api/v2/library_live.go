package v2

import (
	"errors"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

var readLiveLibraryStateFn = db.ReadLiveLibraryState

var resolveLiveLibraryStateByIDFn = db.ResolveLiveLibraryStateByID

func isLibraryUnavailableErr(err error) bool {
	return err != nil && (errors.Is(err, gocql.ErrNotFound) || errors.Is(err, db.ErrLibraryDeleted))
}
