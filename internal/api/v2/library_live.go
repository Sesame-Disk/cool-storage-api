package v2

import (
	"errors"
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
