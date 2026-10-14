//go:build integration

package integration

import (
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	v2pkg "github.com/Sesame-Disk/sesamefs/internal/api/v2"
	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
)

// e117b covers the half of E1-17 the 15 residual legs cannot reach.
//
// Those legs reject at capture and assert headVisits == 0, so the pre-HEAD
// barrier and validateRetainedHistoricalSource are never reached in them. Here
// capture passes on settled history (the supported restore path) and the
// permanent fs: reference the recheck reads is removed at the existing pre-HEAD
// barrier, immediately before the recheck runs - the capture-to-HEAD window the
// recheck exists to close. The control leg runs the same fixture and request
// with the mutation withheld.
const e117bEvidenceEnv = "SESAMEFS_REQUIRE_E117_REHEAD_RECHECK_EVIDENCE"

var (
	e117bLegs     = []string{"settled-authority-control", "recheck-rejects-lost-authority"}
	e117bEvidence = map[string]bool{}
)

func e117bMissing(observed map[string]bool) []string {
	var missing []string
	for _, leg := range e117bLegs {
		if !observed[leg] {
			missing = append(missing, leg)
		}
	}
	return missing
}

func TestE117PreHEADRecheckRejectsLostAuthority(t *testing.T) {
	requireCassandra(t)

	for _, leg := range e117bLegs {
		t.Run(leg, func(t *testing.T) {
			database := shareProjectionDBForTest(t)
			fx := &w2CreateFileFixture{w2UploadFileFixture: newW2UploadFileFixture(t, database, newBorrowedFSHeadHandler(t, database, x1StorageClass(t)))}

			e16UploadHistory(t, fx, fx.content, false)
			historyHead := borrowedFSReadHead(t, database, fx.orgID, fx.repoID)

			helper := v2pkg.NewFSHelper(database)
			old, err := helper.TraverseToPath(fx.repoID, "/"+fx.filename)
			if err != nil || old.TargetEntry == nil {
				t.Fatalf("historical entry: %v", err)
			}
			fsID := old.TargetEntry.ID
			permanent := dbpkg.BlockReferrerForFSObject(fx.repoID, fsID)

			// Lapse only this isolated upload's own temporary pin; the historical
			// permanent fs: is exactly what the recheck later requires.
			refs, err := database.ListBlockReferrers(fx.orgID, fx.blockID)
			if err != nil {
				t.Fatal(err)
			}
			own := 0
			for _, ref := range refs {
				if strings.HasPrefix(ref, "up:") {
					own++
					if err := database.RemoveBlockReference(fx.orgID, fx.blockID, ref); err != nil {
						t.Fatal(err)
					}
				}
			}
			if own != 1 {
				t.Fatalf("upload must own exactly one temporary pin: %v", refs)
			}

			// A real delete: HEAD advances while the version's fs_object, blocks
			// and permanent references stay for retention.
			if rec := e16FileRequest(fx, http.MethodDelete, "", nil, fx.handler.DeleteFile); rec.Code != http.StatusOK {
				t.Fatalf("productive delete: %d %s", rec.Code, rec.Body.String())
			}
			fx.headBefore = borrowedFSReadHead(t, database, fx.orgID, fx.repoID)
			if fx.headBefore == historyHead {
				t.Fatal("delete did not advance HEAD")
			}

			historicalVisits, headVisits := 0, 0
			t.Cleanup(v2pkg.SetRevertFilePublicationBarriersForTest(fx.repoID, func(observed string) {
				historicalVisits++
				if observed != fsID {
					t.Fatalf("historical entry %s, want %s", observed, fsID)
				}
				fx.assertHeadUnchanged(t)
			}, func() {
				headVisits++
				if leg == "recheck-rejects-lost-authority" && headVisits == 1 {
					// Same request, same fixture, one difference: the row the
					// recheck reads is gone before it runs.
					if err := database.RemoveBlockReference(fx.orgID, fx.blockID, permanent); err != nil {
						t.Fatal(err)
					}
				}
			}))

			rec := e16FileRequest(fx, http.MethodPost, "", url.Values{"commit_id": {historyHead}, "conflict_policy": {"replace"}}, fx.handler.RevertFile)

			if historicalVisits != 1 {
				t.Fatalf("historical capture visits = %d, want 1", historicalVisits)
			}
			// Reachability is the claim: the 15 residual legs require headVisits == 0,
			// this leg requires the pre-HEAD barrier to be reached, which is only
			// possible when capture passed and the recheck is about to run.
			if headVisits < 1 {
				t.Fatalf("pre-HEAD barrier never reached (visits=%d): capture must pass so the recheck runs", headVisits)
			}
			t.Logf("pre-HEAD visits=%d (recheck reached)", headVisits)

			switch leg {
			case "settled-authority-control":
				if rec.Code != http.StatusOK {
					t.Fatalf("control revert: %d %s", rec.Code, rec.Body.String())
				}
				fx.assertHeadAdvanced(t)
			case "recheck-rejects-lost-authority":
				if rec.Code == http.StatusOK {
					t.Fatalf("recheck admitted a source whose permanent fs: was removed: %d %s", rec.Code, rec.Body.String())
				}
				fx.assertHeadUnchanged(t)
				if head := borrowedFSReadHead(t, database, fx.orgID, fx.repoID); head != fx.headBefore {
					t.Fatalf("rejected recheck advanced HEAD %s -> %s", fx.headBefore, head)
				}
				if rows := w2Repairs(t, fx); len(rows) != 0 {
					t.Fatalf("rejected recheck installed repair %+v", rows)
				}
			}

			e117bEvidence[leg] = true
		})
	}

	if os.Getenv(e117bEvidenceEnv) == "1" {
		if missing := e117bMissing(e117bEvidence); len(missing) > 0 {
			t.Fatalf("required legs missing: %s", strings.Join(missing, ", "))
		}
	}
}
