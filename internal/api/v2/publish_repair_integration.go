//go:build integration

package v2

import "github.com/Sesame-Disk/sesamefs/internal/db"

// RunPublishedBlockReferenceRepairSweepForTest executes one complete repair
// sweep synchronously. It exists only for integration evidence so retention
// assertions prove that the queued row was actually inspected, rather than
// inferring processing from a wall-clock wait on the background ticker.
func RunPublishedBlockReferenceRepairSweepForTest(database *db.DB) error {
	return runPublishedBlockReferenceRepairSweep(database)
}
