package v2

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/gin-gonic/gin"
)

var readLiveLibraryStateFn = db.ReadLiveLibraryState

var resolveLiveLibraryStateByIDFn = db.ResolveLiveLibraryStateByID

func isLibraryUnavailableErr(err error) bool {
	return err != nil && (errors.Is(err, gocql.ErrNotFound) || errors.Is(err, db.ErrLibraryDeleted))
}

func writeLiveLibraryStateError(c *gin.Context, err error) {
	if isLibraryUnavailableErr(err) {
		c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
		return
	}

	c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check library state"})
}

func wrapLiveLibraryStateError(err error) error {
	if isLibraryUnavailableErr(err) {
		return fmt.Errorf("library not found: %w", err)
	}

	return fmt.Errorf("failed to check library state: %w", err)
}

// respondIfLibraryMissing disambiguates a denied library access. Permission
// lookups collapse "library does not exist" and "caller lacks access" into the
// same negative result; calling this in the access-denied branch lets a missing
// or soft-deleted library surface as 404 (or 500 on lookup error) instead of a
// misleading 403. It returns true when it has written a response and the caller
// should return; false when the library is live and the caller should emit its
// own 403. Cost is paid only on the (rare) denied path, never on the happy path.
func respondIfLibraryMissing(c *gin.Context, session *gocql.Session, orgID, repoID string) bool {
	if _, err := readLiveLibraryStateFn(session, orgID, repoID); err != nil {
		writeLiveLibraryStateError(c, err)
		return true
	}
	return false
}
