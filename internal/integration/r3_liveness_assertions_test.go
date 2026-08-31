//go:build integration

package integration

import (
	"testing"

	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
	gcpkg "github.com/Sesame-Disk/sesamefs/internal/gc"
	"github.com/google/uuid"
)

func assertR3ProductiveUploadPinVisible(t *testing.T, database *dbpkg.DB, store *gcpkg.CassandraStore, orgID uuid.UUID, blockID, referrer string) {
	t.Helper()
	exists, err := database.BlockReferenceExists(orgID.String(), blockID, referrer)
	if err != nil || !exists {
		t.Fatalf("R3 GC-WINS EVIDENCE: productive RegisterUploadedBlockTarget pin visible=%v err=%v; want true, nil", exists, err)
	}
	hasRefs, err := store.BlockHasReferencesGlobal(orgID, blockID)
	if err != nil || !hasRefs {
		t.Fatalf("R3 GC-WINS EVIDENCE: EACH_QUORUM visibility after productive writer pin=%v err=%v; want true, nil", hasRefs, err)
	}
}
