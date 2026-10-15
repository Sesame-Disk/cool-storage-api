//go:build integration

package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

var (
	baseURL          string
	adminClient      *testClient
	userClient       *testClient
	readonlyClient   *testClient
	guestClient      *testClient
	superadminClient *testClient
)

// liveRepoIDs tracks repos created during the current test run that must not
// be deleted by the stale-library cleanup path.
var liveRepoIDs sync.Map

// Include interrupted API cross-library fixtures: they share the integration
// owners and quota, while liveRepoIDs still protects this run's active repos.
var ephemeralLibraryPrefixes = []string{
	"cross-lib-src-",
	"cross-lib-dst-",
	"inttest-",
	"smoke-",
}

var ephemeralLibraryExactNames = []string{
	"sesamefs-public-smoke",
}

func TestMain(m *testing.M) {
	if os.Getenv("SESAMEFS_W2_PROCESS_CHILD") == "1" {
		os.Exit(m.Run())
	}
	// Every evidence gate must be listed here. A gate that is not in this chain still
	// fails a SKIP inside its own test, but it cannot stop TestMain from exiting 0 when
	// the stack never came up at all — the run would print "ok" having executed nothing.
	// TestEveryEvidenceGateIsWiredIntoTestMain enforces the rule above, because a
	// comment could not: R26 was added to docker-compose and missed here, and the
	// omission was invisible in the standard run only because P4A happens to be set
	// alongside it.
	requireEvidence := os.Getenv("SESAMEFS_REQUIRE_E101_PUBLISHED_PREPARED_CRASH_EVIDENCE") == "1" ||
		os.Getenv("SESAMEFS_REQUIRE_E101_DISCOVERY_RETAINED_CRASH_EVIDENCE") == "1" ||
		os.Getenv("SESAMEFS_REQUIRE_E101_ROOT_ONLY_CRASH_EVIDENCE") == "1" ||
		os.Getenv("SESAMEFS_REQUIRE_E117_REHEAD_RECHECK_EVIDENCE") == "1" ||
		os.Getenv("SESAMEFS_REQUIRE_E101_TERMINAL_CRASH_EVIDENCE") == "1" ||
		os.Getenv("SESAMEFS_REQUIRE_E101_PHYSICAL_CRASH_EVIDENCE") == "1" ||
		os.Getenv("SESAMEFS_REQUIRE_E101_G3_CRASH_EVIDENCE") == "1" ||
		os.Getenv("SESAMEFS_REQUIRE_E101_G2_CRASH_EVIDENCE") == "1" ||
		os.Getenv("SESAMEFS_REQUIRE_E115E_REPAIR_SETTLEMENT_EVIDENCE") == "1" ||
		os.Getenv("SESAMEFS_REQUIRE_E115D_ABANDONED_COMMIT_EVIDENCE") == "1" ||
		os.Getenv("SESAMEFS_REQUIRE_E115C_POST_D_PHYSICAL_EVIDENCE") == "1" ||
		os.Getenv("SESAMEFS_REQUIRE_E115B_WRITER_POST_D_EVIDENCE") == "1" ||
		os.Getenv("SESAMEFS_REQUIRE_E115A_STALE_VISITOR_EVIDENCE") == "1" ||
		os.Getenv("SESAMEFS_REQUIRE_E114_PUB_ZERO_REF_EVIDENCE") == "1" ||
		os.Getenv("SESAMEFS_REQUIRE_E113_DISCOVERY_DELAY_EVIDENCE") == "1" ||
		os.Getenv("SESAMEFS_REQUIRE_E112_KNOWN_LOSER_EVIDENCE") == "1" ||
		os.Getenv("SESAMEFS_REQUIRE_G5_COEXISTENCE_EVIDENCE") == "1" ||
		os.Getenv("SESAMEFS_REQUIRE_E111_REPAIR_LIVENESS_EVIDENCE") == "1" ||
		os.Getenv("SESAMEFS_REQUIRE_G5_EVIDENCE") == "1" ||
		os.Getenv("SESAMEFS_REQUIRE_G4_EVIDENCE") == "1" ||
		os.Getenv("SESAMEFS_REQUIRE_W24_CHARACTERIZATION") == "1" ||
		os.Getenv("SESAMEFS_REQUIRE_W2_REPAIR_3DC_EVIDENCE") == "1" ||
		os.Getenv("SESAMEFS_REQUIRE_W2_ROLLOUT_EVIDENCE") == "1" ||
		os.Getenv("SESAMEFS_REQUIRE_W2_CLOSURE_EVIDENCE") == "1" ||
		os.Getenv("SESAMEFS_REQUIRE_W2_PUBLICATION_CONTINUITY_EVIDENCE") == "1" ||
		os.Getenv("SESAMEFS_REQUIRE_W2_CREATEFILE_EXACT_P_EVIDENCE") == "1" ||
		os.Getenv("SESAMEFS_REQUIRE_E14_ONLYOFFICE_EVIDENCE") == "1" ||
		os.Getenv("SESAMEFS_REQUIRE_E15A_SEAFHTTP_SINGLE_EVIDENCE") == "1" ||
		os.Getenv("SESAMEFS_REQUIRE_E15B_SEAFHTTP_STREAMING_EVIDENCE") == "1" ||
		os.Getenv("SESAMEFS_REQUIRE_E16_REVERTFILE_CHARACTERIZATION") == "1" ||
		os.Getenv("SESAMEFS_REQUIRE_E110_RESTORETRASH_CHARACTERIZATION") == "1" ||
		os.Getenv("SESAMEFS_REQUIRE_E110B_RESTORETRASH_DIR_CHARACTERIZATION") == "1" ||
		os.Getenv("SESAMEFS_REQUIRE_E110C_REVERTDIR_CHARACTERIZATION") == "1" ||
		os.Getenv("SESAMEFS_REQUIRE_E110D_REVERTDIRENTS_CHARACTERIZATION") == "1" ||
		os.Getenv("SESAMEFS_REQUIRE_E110E_UNPUBLISHED_RESURRECTION_EVIDENCE") == "1" ||
		os.Getenv("SESAMEFS_REQUIRE_E117_RESIDUAL_RESURRECTION_EVIDENCE") == "1" ||
		os.Getenv("SESAMEFS_REQUIRE_E17_SYNC_RECVFS_CHARACTERIZATION") == "1" ||
		os.Getenv("SESAMEFS_REQUIRE_E19_CROSS_REPO_EVIDENCE") == "1" ||
		os.Getenv("SESAMEFS_REQUIRE_P2_EVIDENCE") == "1" ||
		os.Getenv("SESAMEFS_REQUIRE_P3_EVIDENCE") == "1" ||
		os.Getenv("SESAMEFS_REQUIRE_P4A_EVIDENCE") == "1" ||
		os.Getenv("SESAMEFS_REQUIRE_P4B_EVIDENCE") == "1" ||
		os.Getenv("SESAMEFS_REQUIRE_G1_ORPHAN_EVIDENCE") == "1" ||
		os.Getenv("SESAMEFS_REQUIRE_R3_CHARACTERIZATION") == "1" ||
		os.Getenv("SESAMEFS_REQUIRE_X1_NONOVERLAP_CHARACTERIZATION") == "1" ||
		os.Getenv("SESAMEFS_REQUIRE_BORROWEDFS_OWN_LIVENESS_EVIDENCE") == "1" ||
		os.Getenv("SESAMEFS_REQUIRE_SESSIONUPLOAD_OWN_LIVENESS_EVIDENCE") == "1" ||
		os.Getenv("SESAMEFS_REQUIRE_W2_UPLOADFILE_EXACT_P_EVIDENCE") == "1" ||
		os.Getenv("SESAMEFS_REQUIRE_R26_EVIDENCE") == "1" ||
		os.Getenv("SESAMEFS_REQUIRE_W2_POST_HEAD_EVIDENCE") == "1" ||
		os.Getenv("SESAMEFS_REQUIRE_W2_POST_HEAD_MULTIDC_EVIDENCE") == "1" ||
		os.Getenv("SESAMEFS_REQUIRE_W2_SYNC_PUTBLOCK_HEAD_EVIDENCE") == "1" ||
		os.Getenv("SESAMEFS_REQUIRE_W2_SYNC_PUTBLOCK_HEAD_CRASH_EVIDENCE") == "1" ||
		os.Getenv("SESAMEFS_REQUIRE_W2_SYNC_PUTBLOCK_XDC_EVIDENCE") == "1" ||
		os.Getenv("SESAMEFS_REQUIRE_PC0_PUBLICATION_CHARACTERIZATION") == "1" ||
		os.Getenv("SESAMEFS_REQUIRE_H1_INITIAL_HEAD_MULTIDC_EVIDENCE") == "1" ||
		os.Getenv("SESAMEFS_REQUIRE_LIBRARY_HEAD_SERIAL_DOMAIN_EVIDENCE") == "1" ||
		os.Getenv("SESAMEFS_REQUIRE_LIBRARY_HARD_DELETE_LEASE_SERIAL_DOMAIN_EVIDENCE") == "1" ||
		os.Getenv("SESAMEFS_REQUIRE_LIBRARY_CONTINUITY_EVIDENCE") == "1" ||
		os.Getenv("SESAMEFS_REQUIRE_IDENTITY_AUTHORITY_EVIDENCE") == "1" ||
		os.Getenv("SESAMEFS_REQUIRE_LIBRARY_BASELINE_CERTIFIER_EVIDENCE") == "1" ||
		os.Getenv("SESAMEFS_REQUIRE_LIBRARY_BASELINE_CERTIFIER_UNAVAILABLE_DC_EVIDENCE") == "1" ||
		os.Getenv("SESAMEFS_REQUIRE_LIBRARY_BASELINE_CERTIFIER_PARTIAL_FS_EVIDENCE") == "1" ||
		os.Getenv("SESAMEFS_REQUIRE_LIBRARY_BASELINE_CERTIFIER_IDENTITY_DIVERGENCE_EVIDENCE") == "1" ||
		os.Getenv("SESAMEFS_REQUIRE_LIBRARY_BASELINE_CERTIFIER_IDENTITY_UNAVAILABLE_EVIDENCE") == "1" ||
		os.Getenv("SESAMEFS_REQUIRE_BLOCK_MAPPING_AUTHORITY_EVIDENCE") == "1" ||
		os.Getenv("SESAMEFS_REQUIRE_BLOCK_MAPPING_AUTHORITY_UNAVAILABLE_EVIDENCE") == "1"
	baseURL = os.Getenv("SESAMEFS_URL")
	if baseURL == "" {
		baseURL = "http://localhost:3000"
	}

	resolvedBaseURL, err := resolveIntegrationBaseURL(baseURL)
	if err != nil {
		fmt.Printf("Backend not available at %s: %v\n", baseURL, err)
		fmt.Println("")
		fmt.Println("Start the backend with:")
		fmt.Println("  docker compose up -d")
		if requireEvidence {
			os.Exit(1)
		}
		os.Exit(0)
	}
	baseURL = resolvedBaseURL

	if err := verifyIntegrationAuth(baseURL, "dev-token-admin"); err != nil {
		fmt.Printf("Integration auth not available at %s: %v\n", baseURL, err)
		fmt.Println("")
		fmt.Println("The running backend is reachable, but the standard dev tokens are not enabled.")
		fmt.Println("Set SESAMEFS_URL to an environment with dev tokens, or run the backend with AUTH_DEV_MODE and seeded dev tokens.")
		if requireEvidence {
			os.Exit(1)
		}
		os.Exit(0)
	}

	// Set up clients for each role
	superadminClient = newTestClient(baseURL, "dev-token-superadmin")
	adminClient = newTestClient(baseURL, "dev-token-admin")
	userClient = newTestClient(baseURL, "dev-token-user")
	readonlyClient = newTestClient(baseURL, "dev-token-readonly")
	guestClient = newTestClient(baseURL, "dev-token-guest")

	cleanupIntegrationEphemeralLibraries("before")
	afterCleanupDone := false
	defer func() {
		if !afterCleanupDone {
			cleanupIntegrationEphemeralLibraries("after")
		}
	}()

	code := m.Run()
	if os.Getenv(e101gEvidenceEnv) == "1" {
		if missing := e101gMissing(); len(missing) > 0 {
			fmt.Printf("%s=1 requires both published-prepared crash/control legs; missing=%s\n", e101gEvidenceEnv, strings.Join(missing, ","))
			code = 1
		}
	}
	if os.Getenv(e101fEvidenceEnv) == "1" {
		if missing := e101fMissing(); len(missing) > 0 {
			fmt.Printf("%s=1 requires both discovery-retained terminal crash/control legs; missing=%s\n", e101fEvidenceEnv, strings.Join(missing, ","))
			code = 1
		}
	}
	if os.Getenv(e101eEvidenceEnv) == "1" {
		if missing := e101eMissing(); len(missing) > 0 {
			fmt.Printf("%s=1 requires both root-only terminal crash/control legs; missing=%s\n", e101eEvidenceEnv, strings.Join(missing, ","))
			code = 1
		}
	}
	if os.Getenv(e117bEvidenceEnv) == "1" {
		if missing := e117bMissing(e117bEvidence); len(missing) > 0 {
			fmt.Printf("%s=1 requires both pre-HEAD recheck legs; missing=%s\n", e117bEvidenceEnv, strings.Join(missing, ","))
			code = 1
		}
	}
	if os.Getenv(e101dEvidenceEnv) == "1" {
		if missing := e101dMissing(); len(missing) > 0 {
			fmt.Printf("%s=1 requires both terminal-before-cleanup crash/control legs; missing=%s\n", e101dEvidenceEnv, strings.Join(missing, ","))
			code = 1
		}
	}
	if os.Getenv(e101cEvidenceEnv) == "1" {
		if missing := e101cMissing(); len(missing) > 0 {
			fmt.Printf("%s=1 requires all physical recovery process-crash/control legs; missing=%s\n", e101cEvidenceEnv, strings.Join(missing, ","))
			code = 1
		}
	}
	if os.Getenv(e101bEvidenceEnv) == "1" {
		if missing := e101bMissing(); len(missing) > 0 {
			fmt.Printf("%s=1 requires all G3 process-crash/control legs; missing=%s\n", e101bEvidenceEnv, strings.Join(missing, ","))
			code = 1
		}
	}
	if os.Getenv(e101EvidenceEnv) == "1" {
		if missing := e101Missing(); len(missing) > 0 {
			fmt.Printf("%s=1 requires all G2 process-crash/control legs; missing=%s\n", e101EvidenceEnv, strings.Join(missing, ","))
			code = 1
		}
	}
	if os.Getenv(g5CoexistenceEvidenceEnv) == "1" && !g5CoexistenceObserved {
		fmt.Println("required G5 isolated old-life continuation evidence was not observed")
		code = 1
	}
	if os.Getenv("SESAMEFS_REQUIRE_G5_EVIDENCE") == "1" && (!g5CoexistenceObserved || !g5PaginationObserved || !g5GraceObserved) {
		fmt.Println("required G5 Cassandra/MinIO old-life, bounded seek and fresh zero-epoch grace evidence was not observed")
		code = 1
	}
	if os.Getenv("SESAMEFS_REQUIRE_G4_EVIDENCE") == "1" && !g4CoexistenceObserved {
		fmt.Println("required G4 Cassandra/MinIO coexistence evidence was not observed")
		code = 1
	}
	if os.Getenv(w24EvidenceEnv) == "1" {
		if missing := w24Missing(w24Observed); len(missing) > 0 {
			fmt.Printf("%s=1 requires all named W2-4 characterization legs; missing=%s\n", w24EvidenceEnv, strings.Join(missing, ","))
			code = 1
		}
	}
	if os.Getenv(w2Repair3DCEvidenceEnv) == "1" && !w2Repair3DCEvidence {
		fmt.Println("required real W2 repair 3-DC phase was not observed")
		code = 1
	}
	if os.Getenv(w2RolloutEvidenceEnv) == "1" {
		if missing := w2RolloutMissing(); len(missing) > 0 {
			fmt.Printf("%s=1 requires all named W2 rollout legs; missing=%s\n", w2RolloutEvidenceEnv, strings.Join(missing, ","))
			code = 1
		}
	}
	if os.Getenv(w2ClosureEvidenceEnv) == "1" {
		if missing := w2ClosureMissing(); len(missing) > 0 {
			fmt.Printf("%s=1 requires all named W2-0 closure legs; missing=%s\n", w2ClosureEvidenceEnv, strings.Join(missing, ","))
			code = 1
		}
	}
	if os.Getenv(w2PublicationContinuityEnv) == "1" {
		if missing := w2PublicationContinuityMissing(w2PublicationContinuityObserved); len(missing) > 0 {
			fmt.Printf("%s=1 requires all named W2-0 legs; missing=%s (check -run filters)\n", w2PublicationContinuityEnv, strings.Join(missing, ","))
			code = 1
		}
	}
	if os.Getenv(e19EvidenceEnv) == "1" {
		if missing := e19Missing(e19Evidence); len(missing) > 0 {
			fmt.Printf("%s=1 requires every named cross-repo copy/move leg; missing=%s\n", e19EvidenceEnv, strings.Join(missing, ","))
			code = 1
		}
	}
	if os.Getenv(e17EvidenceEnv) == "1" {
		if missing := e17Missing(e17Evidence); len(missing) > 0 {
			fmt.Printf("%s=1 requires all named RecvFS-before-PutBlock legs; missing=%s (check -run filters)\n", e17EvidenceEnv, strings.Join(missing, ","))
			code = 1
		}
	}
	if os.Getenv(e115eEvidenceEnv) == "1" {
		if missing := e115eMissing(e115eObserved); len(missing) > 0 {
			fmt.Printf("%s=1 requires all named E1-15E repair settlement legs; missing=%s (check -run filters)\n", e115eEvidenceEnv, strings.Join(missing, ","))
			code = 1
		}
	}
	if os.Getenv(e115dEvidenceEnv) == "1" {
		if missing := e115dMissing(e115dObserved); len(missing) > 0 {
			fmt.Printf("%s=1 requires all named E1-15D abandoned commit legs; missing=%s (check -run filters)\n", e115dEvidenceEnv, strings.Join(missing, ","))
			code = 1
		}
	}
	if os.Getenv(e115cEvidenceEnv) == "1" {
		if missing := e115cMissing(e115cObserved); len(missing) > 0 {
			fmt.Printf("%s=1 requires all named E1-15C post-D physical legs; missing=%s (check -run filters)\n", e115cEvidenceEnv, strings.Join(missing, ","))
			code = 1
		}
	}
	if os.Getenv(e115bEvidenceEnv) == "1" {
		if missing := e115bMissing(e115bObserved); len(missing) > 0 {
			fmt.Printf("%s=1 requires all named E1-15B writer post-D legs; missing=%s (check -run filters)\n", e115bEvidenceEnv, strings.Join(missing, ","))
			code = 1
		}
	}
	if os.Getenv(e115aEvidenceEnv) == "1" {
		if missing := e115aMissing(e115aObserved); len(missing) > 0 {
			fmt.Printf("%s=1 requires all named E1-15A stale visitor legs; missing=%s (check -run filters)\n", e115aEvidenceEnv, strings.Join(missing, ","))
			code = 1
		}
	}
	if os.Getenv(e114EvidenceEnv) == "1" {
		if missing := e114Missing(e114Observed); len(missing) > 0 {
			fmt.Printf("%s=1 requires all named E1-14 pub zero-ref legs; missing=%s (check -run filters)\n", e114EvidenceEnv, strings.Join(missing, ","))
			code = 1
		}
	}
	if os.Getenv(e113EvidenceEnv) == "1" {
		if missing := e113Missing(e113Observed); len(missing) > 0 {
			fmt.Printf("%s=1 requires all named E1-13 discovery delay legs; missing=%s (check -run filters)\n", e113EvidenceEnv, strings.Join(missing, ","))
			code = 1
		}
	}
	if os.Getenv(e112EvidenceEnv) == "1" {
		if missing := e112Missing(e112Observed); len(missing) > 0 {
			fmt.Printf("%s=1 requires all named E1-12 loser crash legs; missing=%s (check -run filters)\n", e112EvidenceEnv, strings.Join(missing, ","))
			code = 1
		}
	}
	if os.Getenv(e111EvidenceEnv) == "1" {
		if missing := e111Missing(e111Observed); len(missing) > 0 {
			fmt.Printf("%s=1 requires all named E1-11 repair liveness legs; missing=%s (check -run filters)\n", e111EvidenceEnv, strings.Join(missing, ","))
			code = 1
		}
	}
	if os.Getenv(e117EvidenceEnv) == "1" {
		if missing := e117Missing(e117Evidence); len(missing) > 0 {
			fmt.Printf("%s=1 requires all named E1-17 residual resurrection legs; missing=%s (check -run filters)\n", e117EvidenceEnv, strings.Join(missing, ","))
			code = 1
		}
	}
	if os.Getenv(e110eEvidenceEnv) == "1" {
		if missing := e110eMissing(e110eEvidence); len(missing) > 0 {
			fmt.Printf("%s=1 requires all named unpublished-source legs; missing=%s (check -run filters)\n", e110eEvidenceEnv, strings.Join(missing, ","))
			code = 1
		}
	}
	if os.Getenv(e110dEvidenceEnv) == "1" {
		if missing := e110dMissing(e110dEvidence); len(missing) > 0 {
			fmt.Printf("%s=1 requires all named RevertDirents legs; missing=%s (check -run filters)\n", e110dEvidenceEnv, strings.Join(missing, ","))
			code = 1
		}
	}
	if os.Getenv(e110cEvidenceEnv) == "1" {
		if missing := e110cMissing(e110cEvidence); len(missing) > 0 {
			fmt.Printf("%s=1 requires all named RevertDirectory legs; missing=%s (check -run filters)\n", e110cEvidenceEnv, strings.Join(missing, ","))
			code = 1
		}
	}
	if os.Getenv(e110bEvidenceEnv) == "1" {
		if missing := e110bMissing(e110bEvidence); len(missing) > 0 {
			fmt.Printf("%s=1 requires all named directory RestoreTrashItem legs; missing=%s (check -run filters)\n", e110bEvidenceEnv, strings.Join(missing, ","))
			code = 1
		}
	}
	if os.Getenv(e110EvidenceEnv) == "1" {
		if missing := e110Missing(e110Evidence); len(missing) > 0 {
			fmt.Printf("%s=1 requires all named RestoreTrashItem retained-history legs; missing=%s (check -run filters)\n", e110EvidenceEnv, strings.Join(missing, ","))
			code = 1
		}
	}
	if os.Getenv(e16EvidenceEnv) == "1" {
		if missing := e16Missing(e16Evidence); len(missing) > 0 {
			fmt.Printf("%s=1 requires all named RevertFile retained-history legs; missing=%s (check -run filters)\n", e16EvidenceEnv, strings.Join(missing, ","))
			code = 1
		}
	}
	if os.Getenv(e15bEvidenceEnv) == "1" {
		if missing := e15bMissing(e15bEvidence); len(missing) > 0 {
			fmt.Printf("%s=1 requires all named SeafHTTP streaming legs; missing=%s (check -run filters)\n", e15bEvidenceEnv, strings.Join(missing, ","))
			code = 1
		}
	}

	if os.Getenv(e15aEvidenceEnv) == "1" {
		if missing := e15aMissing(e15aEvidence); len(missing) > 0 {
			fmt.Printf("%s=1 requires all named SeafHTTP single-shot legs; missing=%s (check -run filters)\n", e15aEvidenceEnv, strings.Join(missing, ","))
			code = 1
		}
	}
	if os.Getenv(e14EvidenceEnv) == "1" {
		if missing := e14Missing(e14Evidence); len(missing) > 0 {

			fmt.Printf("%s=1 requires all named OnlyOffice legs; missing=%s (check -run filters)\n", e14EvidenceEnv, strings.Join(missing, ","))
			code = 1
		}
	}
	if os.Getenv(w2CreateFileExactPEnv) == "1" {
		if missing := w2CreateFileExactPMissing(w2CreateFileExactPEvidence); len(missing) > 0 {
			fmt.Printf("%s=1 requires all named W2-6a legs; missing=%s (check -run filters)\n", w2CreateFileExactPEnv, strings.Join(missing, ","))
			if code == 0 {
				code = 1
			}
		}
	}
	if os.Getenv(r3CharacterizationEvidenceEnv) == "1" && !r3CharacterizationEvidence.complete() {
		fmt.Printf("%s=1 requires all R3 Cassandra legs; missing=%s (check -run filters)\n", r3CharacterizationEvidenceEnv, strings.Join(r3CharacterizationEvidence.missing(), ","))
		if code == 0 {
			code = 1
		}
	}
	if os.Getenv(x1NonoverlapCharacterizationEnv) == "1" && !x1NonoverlapEvidence.complete() {
		fmt.Printf("%s=1 requires all named X1 non-overlap legs; missing=%s (check -run filters)\n", x1NonoverlapCharacterizationEnv, strings.Join(x1NonoverlapEvidence.missing(), ","))
		if code == 0 {
			code = 1
		}
	}
	if os.Getenv(borrowedFSOwnLivenessEnv) == "1" && !borrowedFSOwnLivenessEvidence.complete() {
		fmt.Printf("%s=1 requires all named BorrowedFS own-liveness legs; missing=%s (check -run filters)\n", borrowedFSOwnLivenessEnv, strings.Join(borrowedFSOwnLivenessEvidence.missing(), ","))
		if code == 0 {
			code = 1
		}
	}
	if os.Getenv(sessionUploadOwnLivenessEnv) == "1" && !sessionUploadOwnLivenessEvidence.complete() {
		fmt.Printf("%s=1 requires all named SessionUpload own-liveness legs; missing=%s (check -run filters)\n", sessionUploadOwnLivenessEnv, strings.Join(sessionUploadOwnLivenessEvidence.missing(), ","))
		if code == 0 {
			code = 1
		}
	}
	if os.Getenv(w2PostHeadEvidenceEnv) == "1" && !w2PostHeadEvidence.complete() {
		fmt.Printf("%s=1 requires all named W2 CreateFileFromBlocks post-HEAD legs; missing=%s (check -run filters)\n", w2PostHeadEvidenceEnv, strings.Join(w2PostHeadEvidence.missing(), ","))
		if code == 0 {
			code = 1
		}
	}
	if os.Getenv("SESAMEFS_REQUIRE_W2_POST_HEAD_MULTIDC_EVIDENCE") == "1" && !w2PostHeadMultidcEvidence {
		fmt.Printf("SESAMEFS_REQUIRE_W2_POST_HEAD_MULTIDC_EVIDENCE=1 requires the real 3-DC W2 post-HEAD reachability leg\n")
		if code == 0 {
			code = 1
		}
	}
	if os.Getenv(w2SyncXDCEvidenceEnv) == "1" && !w2SyncXDCEvidence {
		fmt.Printf("%s=1 requires the real 3-DC Sync PutBlock cross-DC provenance recovery leg\n", w2SyncXDCEvidenceEnv)
		if code == 0 {
			code = 1
		}
	}
	if os.Getenv(w2SyncPutBlockHeadEvidenceEnv) == "1" && !w2SyncPutBlockHeadEvidence.complete() {
		fmt.Printf("%s=1 requires all named W2 Sync PutBlock->HEAD legs; missing=%s (check -run filters)\n", w2SyncPutBlockHeadEvidenceEnv, strings.Join(w2SyncPutBlockHeadEvidence.missing(), ","))
		if code == 0 {
			code = 1
		}
	}
	if os.Getenv(w2SyncPutBlockHeadCrashEvidenceEnv) == "1" && !w2SyncPutBlockHeadCrashEvidenceObserved {
		fmt.Printf("%s=1 requires the real post-CAS crash/replay leg to have run; it did not (check -run filters)\n", w2SyncPutBlockHeadCrashEvidenceEnv)
		if code == 0 {
			code = 1
		}
	}
	if os.Getenv(h1InitialHeadMultiDCEvidenceEnv) == "1" && !h1InitialHeadMultiDCEvidence {
		fmt.Printf("%s=1 requires the real 3-DC blind-datacenter initializer leg (TestH1InitialHeadBlindDCDoesNotRevert3DC)\n", h1InitialHeadMultiDCEvidenceEnv)
		if code == 0 {
			code = 1
		}
	}
	if os.Getenv(libraryHeadSerialDomainEvidenceEnv) == "1" && !libraryHeadSerialDomainEvidence.complete() {
		fmt.Printf("%s=1 requires the real 3-DC library HEAD SERIAL-domain legs (TestLibraryHeadSerialDomainConcurrentAdvance3DC and TestLibraryHeadSerialDomainInitialHead3DC)\n", libraryHeadSerialDomainEvidenceEnv)
		if code == 0 {
			code = 1
		}
	}
	if os.Getenv(libraryHardDeleteLeaseSerialDomainEvidenceEnv) == "1" && !libraryHardDeleteLeaseSerialDomainEvidence {
		fmt.Printf("%s=1 requires the real 3-DC library hard-delete lease SERIAL-domain cycle (TestLibraryHardDeleteLeaseSerialDomain3DC)\n", libraryHardDeleteLeaseSerialDomainEvidenceEnv)
		if code == 0 {
			code = 1
		}
	}
	if os.Getenv(libraryContinuityEvidenceEnv) == "1" && !libraryContinuityEvidence {
		fmt.Println(libraryContinuityEvidenceEnv + "=1 requires the real 3-DC certified-frontier evidence (TestLibraryContinuityCertifiedFrontier3DC)")
		if code == 0 {
			code = 1
		}
	}
	if os.Getenv(libraryBaselineCertifierEvidenceEnv) == "1" && !libraryBaselineCertifierEvidence {
		fmt.Println(libraryBaselineCertifierEvidenceEnv + "=1 requires the real 3-DC certified-baseline certifier evidence (TestLibraryBaselineCertifier3DC)")
		if code == 0 {
			code = 1
		}
	}
	if os.Getenv(libraryBaselineCertifierUnavailableDCEvidenceEnv) == "1" && !libraryBaselineCertifierUnavailableDCEvidence {
		fmt.Println(libraryBaselineCertifierUnavailableDCEvidenceEnv + "=1 requires real dc-asia-unavailable EACH_QUORUM evidence (TestLibraryBaselineCertifierUnavailableDCEachQuorum)")
		if code == 0 {
			code = 1
		}
	}
	if os.Getenv(libraryBaselineCertifierPartialFSEvidenceEnv) == "1" && !libraryBaselineCertifierPartialFSEvidence {
		fmt.Println(libraryBaselineCertifierPartialFSEvidenceEnv + "=1 requires the real 3-DC partial reachable fs_object fail-closed leg (TestLibraryBaselineCertifierPartialFSObject3DC)")
		if code == 0 {
			code = 1
		}
	}
	if os.Getenv(blockMappingAuthorityEvidenceEnv) == "1" && !blockMappingAuthorityEvidence {
		fmt.Println(blockMappingAuthorityEvidenceEnv + "=1 requires the real 3-DC mapping-authority matrix (TestBlockMappingAuthority3DC)")
		if code == 0 {
			code = 1
		}
	}
	if os.Getenv(blockMappingAuthorityUnavailableEvidenceEnv) == "1" && !blockMappingAuthorityUnavailableEvidence {
		fmt.Println(blockMappingAuthorityUnavailableEvidenceEnv + "=1 requires the real 3-DC mapping-authority outage leg (TestBlockMappingAuthorityUnavailable3DC)")
		if code == 0 {
			code = 1
		}
	}
	if os.Getenv(pc0PublicationCharacterizationEnv) == "1" && !pc0PublicationMatrix.matrixRecorded() {
		fmt.Printf("%s=1 requires all named PC-0 3-DC topology/matrix rows; missing=%s (check -run filters)\n", pc0PublicationCharacterizationEnv, strings.Join(pc0PublicationMatrix.missing(), ","))
		if code == 0 {
			code = 1
		}
	}
	afterCleanupDone = true
	cleanupIntegrationEphemeralLibraries("after")
	if verifyErr := verifyNoOrphanAdminLibraryProjectionsWithRetry(5*time.Second, 250*time.Millisecond); verifyErr != nil {
		fmt.Printf("integration cleanup verification failed: %v\n", verifyErr)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

func verifyNoOrphanAdminLibraryProjectionsWithRetry(timeout, interval time.Duration) error {
	return settleAdminProjectionCleanup(timeout, interval, verifyNoOrphanAdminLibraryProjections, cleanupOrphanAdminLibraryProjectionsWithoutTesting)
}

// A GC pass can retire a canonical library after the cleanup snapshot. Retry
// the existing orphan-only cleanup, then require the same zero-orphan verifier.
// Persistent corruption or cleanup/read errors still fail the entire profile.
func settleAdminProjectionCleanup(timeout, interval time.Duration, verify func() error, cleanup func() (int, error)) error {
	deadline := time.Now().Add(timeout)
	for {
		if err := verify(); err == nil {
			return nil
		}
		if _, err := cleanup(); err != nil {
			return fmt.Errorf("settle admin projection cleanup: %w", err)
		}
		lastErr := verify()
		if lastErr == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return lastErr
		}
		time.Sleep(interval)
	}
}

func verifyNoOrphanAdminLibraryProjections() error {
	database, err := openIntegrationProjectionDB()
	if err != nil {
		return fmt.Errorf("connect Cassandra for cleanup verification: %w", err)
	}
	defer database.Close()

	return verifyNoOrphanAdminLibraryProjectionsInDB(database)
}

func verifyNoOrphanAdminLibraryProjectionsInDB(database *dbpkg.DB) error {
	type projectionIssue struct {
		scope  string
		orgID  string
		repoID string
		reason string
	}

	issues := make([]projectionIssue, 0)
	appendIssues := func(scope string, rows []dbpkg.AdminLibraryProjectionRow, err error) error {
		if err != nil {
			return fmt.Errorf("list %s admin library rows: %w", scope, err)
		}
		for _, row := range rows {
			reason, err := classifyProjectionIntegrityIssue(database.Session(), row)
			if err != nil {
				return fmt.Errorf("verify %s projection %s/%s: %w", scope, row.OrgID, row.LibraryID, err)
			}
			if reason == "" {
				continue
			}
			issues = append(issues, projectionIssue{scope: scope, orgID: row.OrgID, repoID: row.LibraryID, reason: reason})
		}
		return nil
	}

	globalRows, err := dbpkg.ListAdminGlobalLibraryRows(database.Session())
	if err := appendIssues("global", globalRows, err); err != nil {
		return err
	}
	orgRows, err := dbpkg.ListAdminOrgLibraryRows(database.Session(), defaultOrgID)
	if err := appendIssues("org", orgRows, err); err != nil {
		return err
	}
	for _, ownerEmail := range []string{defaultAdminEmail, defaultUserEmail, "superadmin@sesamefs.local"} {
		ownerID, ok, err := lookupUserIDByEmailForCleanup(database.Session(), ownerEmail)
		if err != nil {
			return fmt.Errorf("lookup cleanup owner %s: %w", ownerEmail, err)
		}
		if !ok {
			continue
		}
		ownerRows, err := dbpkg.ListAdminOwnerLibraryRows(database.Session(), defaultOrgID, ownerID)
		if err := appendIssues("owner", ownerRows, err); err != nil {
			return err
		}
	}

	const maxExamples = 8
	examples := make([]string, 0, maxExamples)
	for _, issue := range issues {
		if len(examples) < maxExamples {
			examples = append(examples, fmt.Sprintf("%s:%s/%s (%s)", issue.scope, issue.orgID, issue.repoID, issue.reason))
		}
	}

	if len(issues) == 0 {
		return nil
	}
	return fmt.Errorf("found %d orphan or partial admin library projection rows after cleanup: %s", len(issues), strings.Join(examples, ", "))
}

func classifyProjectionIntegrityIssue(session *gocql.Session, row dbpkg.AdminLibraryProjectionRow) (string, error) {
	// Scope to libraries this suite owns. A populated, non-ephemeral name means
	// the row belongs to data outside this test run (or a shared keyspace); an
	// empty name is itself a corruption signature and must still be checked.
	if row.Name != "" && !isEphemeralLibraryName(row.Name) {
		return "", nil
	}
	switch {
	case strings.TrimSpace(row.Name) == "":
		return "empty name", nil
	case strings.TrimSpace(row.OwnerID) == "":
		return "empty owner_id", nil
	case strings.TrimSpace(row.OwnerEmail) == "":
		return "empty owner_email", nil
	default:
		if _, err := dbpkg.ReadAdminLibraryProjectionRow(session, row.OrgID, row.LibraryID); err != nil {
			if err == gocql.ErrNotFound {
				return "missing canonical libraries row", nil
			}
			return "", err
		}
	}
	return "", nil
}

func lookupUserIDByEmailForCleanup(session *gocql.Session, email string) (string, bool, error) {
	var userID string
	if err := session.Query(`SELECT user_id FROM users_by_email WHERE email = ?`, email).Scan(&userID); err != nil {
		if err == gocql.ErrNotFound {
			return "", false, nil
		}
		return "", false, err
	}
	return userID, userID != "", nil
}

// openIntegrationProjectionDB opens the projection database against the fixture's
// declared topology, the same way shareProjectionDBForTest does: the server config
// carries the configured DC map. The destructive GC gate compares live keyspace
// replication against that declaration exactly. This aligns the two shared
// projection DB helpers; it does not identify the cause of the E1 fixture-progress
// failures recorded in docs/TECHNICAL-DEBT.md.
func openIntegrationProjectionDB() (*dbpkg.DB, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	return dbpkg.New(cfg.Database)
}

func resolveIntegrationBaseURL(baseURL string) (string, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	candidates := []string{baseURL}

	if strings.Contains(baseURL, "localhost") {
		candidates = append(candidates, strings.Replace(baseURL, "localhost", "127.0.0.1", 1))
	}

	if os.Getenv("SESAMEFS_URL") == "" {
		candidates = append(candidates,
			"http://127.0.0.1:3000",
			"http://localhost:3000",
			"http://127.0.0.1:8082",
			"http://localhost:8082",
		)
	}

	seen := make(map[string]struct{}, len(candidates))
	filtered := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if _, exists := seen[candidate]; exists {
			continue
		}
		seen[candidate] = struct{}{}
		filtered = append(filtered, candidate)
	}

	var lastErr error
	for _, candidate := range filtered {
		resp, err := client.Get(candidate + "/health")
		if err != nil {
			lastErr = err
			continue
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return candidate, nil
		}
		lastErr = fmt.Errorf("health check returned %d", resp.StatusCode)
	}

	return "", lastErr
}

func verifyIntegrationAuth(baseURL, token string) error {
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest(http.MethodGet, baseURL+"/api2/account/info/", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Token "+token)

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("account info returned %d", resp.StatusCode)
	}

	return nil
}

// createTestLibrary creates a library and registers cleanup via t.Cleanup.
// Returns the repo_id.
func createTestLibrary(t *testing.T, c *testClient, name string) string {
	return createLibraryForTest(t, c, name, map[string]string{"repo_name": name}, true)
}

func createTestLibraryWithCleanup(t *testing.T, c, cleanupClient *testClient, name string) string {
	return createLibraryForTestWithCleanup(t, c, cleanupClient, name, map[string]string{"repo_name": name}, true)
}

func createDisposableTestLibrary(t *testing.T, c *testClient, name string) string {
	// "Disposable" describes the fixture lifetime, not an exemption from cleanup.
	// Register the owner-scoped delete with t.Cleanup so a failed test does not
	// depend on TestMain's stale-library sweep.
	return createLibraryForTest(t, c, name, map[string]string{"repo_name": name}, true)
}

func createLibraryWithBody(t *testing.T, c *testClient, name string, body interface{}, cleanup bool) string {
	return createLibraryForTest(t, c, name, body, cleanup)
}

func createLibraryForTest(t *testing.T, c *testClient, name string, body interface{}, cleanup bool) string {
	return createLibraryForTestWithCleanup(t, c, c, name, body, cleanup)
}

func createLibraryForTestWithCleanup(t *testing.T, c, cleanupClient *testClient, name string, body interface{}, cleanup bool, preserveOnFailure ...bool) string {
	t.Helper()

	const maxAttempts = 5
	for attempt := 0; attempt < maxAttempts; attempt++ {
		repoID, limitReached := tryCreateLibrary(t, c, name, body)
		if repoID != "" {
			liveRepoIDs.Store(repoID, struct{}{})
			if cleanup {
				t.Cleanup(func() {
					if len(preserveOnFailure) > 0 && preserveOnFailure[0] && (t.Failed() || t.Skipped()) {
						t.Logf("preserving failed fixture library %s", repoID)
						return // Keep liveRepoIDs protection through TestMain's sweep.
					}
					liveRepoIDs.Delete(repoID)
					resp := cleanupClient.Delete(t, fmt.Sprintf("/api/v2.1/repos/%s/", repoID))
					if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNotFound {
						resp.Body.Close()
						return
					}
					body := responseBody(t, resp)
					t.Errorf("cleanup delete library %s failed: status=%d body=%s", repoID, resp.StatusCode, body)
				})
			} else {
				// Remove from live set when test ends so stale-cleanup can find it
				// if it was never deleted by the test itself.
				t.Cleanup(func() { liveRepoIDs.Delete(repoID) })
			}
			return repoID
		}

		if !limitReached {
			break
		}

		deleted := cleanupTestLibrariesAcrossKnownUsers(t)
		if deleted > 0 {
			t.Logf("cleaned up %d stale test libraries after hitting the library limit while creating %q", deleted, name)
		} else if attempt == maxAttempts-1 {
			t.Fatalf("failed to create library %q: library limit reached and no stale test libraries were available for cleanup", name)
		} else {
			t.Logf("library limit reached while creating %q but no stale test libraries were available; waiting for active-library projections to converge before retrying", name)
		}

		// Library creation enforcement reads the projection-backed active-library
		// count. After a previous test cleanup, that projection can lag briefly
		// behind the delete that already made the library disappear from the owned
		// repo list, so an immediate retry can still see the stale count. Give the
		// shared integration environment a short convergence window before retrying.
		time.Sleep(time.Duration(attempt+1) * 250 * time.Millisecond)
	}

	t.Fatalf("failed to create library %q after retrying", name)
	return ""
}

func tryCreateLibrary(t *testing.T, c *testClient, name string, body interface{}) (string, bool) {
	t.Helper()

	resp := c.PostJSON(t, "/api/v2.1/repos/", body)
	var result map[string]interface{}
	decodeJSON(t, resp, &result)

	if resp.StatusCode != http.StatusOK {
		if isLibraryLimitResponse(resp.StatusCode, result) {
			return "", true
		}
		t.Fatalf("create library %q failed: status=%d body=%v", name, resp.StatusCode, result)
	}

	repoID, ok := result["repo_id"].(string)
	if !ok || repoID == "" {
		t.Fatalf("failed to get repo_id from create library response for %q: %v", name, result)
	}

	return repoID, false
}

func isLibraryLimitResponse(statusCode int, body map[string]interface{}) bool {
	if statusCode != http.StatusForbidden {
		return false
	}
	errMsg, _ := body["error"].(string)
	return errMsg == "Library limit reached"
}

func cleanupTestLibrariesAcrossKnownUsers(t *testing.T) int {
	t.Helper()

	clients := []*testClient{adminClient, userClient, superadminClient}
	seen := map[*testClient]struct{}{}
	total := 0
	for _, client := range clients {
		if client == nil {
			continue
		}
		if _, ok := seen[client]; ok {
			continue
		}
		seen[client] = struct{}{}
		total += cleanupOwnedEphemeralLibraries(t, client)
	}
	return total
}

func cleanupIntegrationEphemeralLibraries(phase string) {
	clients := []*testClient{adminClient, userClient, superadminClient}
	seen := map[string]struct{}{}
	total := 0
	for _, client := range clients {
		if client == nil {
			continue
		}
		if _, ok := seen[client.token]; ok {
			continue
		}
		seen[client.token] = struct{}{}
		total += cleanupOwnedEphemeralLibrariesWithoutTesting(client)
	}
	orphaned, err := cleanupOrphanAdminLibraryProjectionsWithoutTesting()
	if err != nil {
		fmt.Printf("skipping orphan admin library projection cleanup %s test run: %v\n", phase, err)
	} else {
		total += orphaned
	}
	if total > 0 {
		fmt.Printf("cleaned up %d stale integration test libraries %s test run\n", total, phase)
	}
}

func cleanupOrphanAdminLibraryProjectionsWithoutTesting() (int, error) {
	database, err := openIntegrationProjectionDB()
	if err != nil {
		return 0, err
	}
	defer database.Close()

	return cleanupOrphanAdminLibraryProjectionsInDB(database)
}

func cleanupOrphanAdminLibraryProjectionsInDB(database *dbpkg.DB) (int, error) {
	type cleanupTarget struct {
		orgID  string
		repoID string
		row    dbpkg.AdminLibraryProjectionRow
	}

	targets := map[string]cleanupTarget{}
	appendTargets := func(rows []dbpkg.AdminLibraryProjectionRow, err error) error {
		if err != nil {
			return err
		}
		for _, row := range rows {
			reason, classifyErr := classifyProjectionIntegrityIssue(database.Session(), row)
			if classifyErr != nil {
				return classifyErr
			}
			if reason == "" {
				continue
			}
			key := row.OrgID + "/" + row.LibraryID
			if _, exists := targets[key]; !exists {
				targets[key] = cleanupTarget{orgID: row.OrgID, repoID: row.LibraryID, row: row}
			}
		}
		return nil
	}

	globalRows, err := dbpkg.ListAdminGlobalLibraryRows(database.Session())
	if err != nil {
		return 0, fmt.Errorf("list global admin library rows: %w", err)
	}
	if err := appendTargets(globalRows, nil); err != nil {
		return 0, fmt.Errorf("collect global orphan admin library rows: %w", err)
	}
	orgRows, err := dbpkg.ListAdminOrgLibraryRows(database.Session(), defaultOrgID)
	if err != nil {
		return 0, fmt.Errorf("list org admin library rows: %w", err)
	}
	if err := appendTargets(orgRows, nil); err != nil {
		return 0, fmt.Errorf("collect org orphan admin library rows: %w", err)
	}
	for _, ownerEmail := range []string{defaultAdminEmail, defaultUserEmail, "superadmin@sesamefs.local"} {
		ownerID, ok, err := lookupUserIDByEmailForCleanup(database.Session(), ownerEmail)
		if err != nil {
			return 0, fmt.Errorf("lookup cleanup owner %s: %w", ownerEmail, err)
		}
		if !ok {
			continue
		}
		ownerRows, err := dbpkg.ListAdminOwnerLibraryRows(database.Session(), defaultOrgID, ownerID)
		if err != nil {
			return 0, fmt.Errorf("list owner admin library rows for %s: %w", ownerEmail, err)
		}
		if err := appendTargets(ownerRows, nil); err != nil {
			return 0, fmt.Errorf("collect owner orphan admin library rows for %s: %w", ownerEmail, err)
		}
	}

	if len(targets) == 0 {
		return 0, nil
	}

	buckets, err := dbpkg.ListAdminLibraryBucketDays(database.Session())
	if err != nil {
		return 0, fmt.Errorf("list admin library bucket days: %w", err)
	}

	batch := database.Session().Batch(gocql.LoggedBatch)
	cleaned := 0
	for _, target := range targets {
		batch.Query(`DELETE FROM libraries_by_org_updated WHERE org_id = ? AND library_id = ?`, target.orgID, target.repoID)
		if strings.TrimSpace(target.row.OwnerID) != "" {
			batch.Query(`DELETE FROM libraries_by_owner WHERE org_id = ? AND owner_id = ? AND library_id = ?`, target.orgID, target.row.OwnerID, target.repoID)
		}
		for _, bucketDay := range buckets {
			batch.Query(`DELETE FROM libraries_admin_global_by_updated WHERE bucket_day = ? AND org_id = ? AND library_id = ?`, bucketDay, target.orgID, target.repoID)
		}
		if target.row.DeletedAt != nil && !target.row.DeletedAt.IsZero() {
			batch.Query(`DELETE FROM libraries_deleted_by_org WHERE org_id = ? AND deleted_at = ? AND library_id = ?`, target.orgID, *target.row.DeletedAt, target.repoID)
		}
		cleaned++
	}
	if err := batch.Exec(); err != nil {
		return 0, fmt.Errorf("delete orphan admin library projections: %w", err)
	}
	return cleaned, nil
}

func cleanupOwnedEphemeralLibrariesWithoutTesting(c *testClient) int {
	req, err := http.NewRequest(http.MethodGet, c.baseURL+"/api/v2.1/repos/?type=mine", nil)
	if err != nil {
		fmt.Printf("skipping stale library cleanup for %s: create list request: %v\n", c.token, err)
		return 0
	}
	req.Header.Set("Authorization", "Token "+c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		fmt.Printf("skipping stale library cleanup for %s: list request failed: %v\n", c.token, err)
		return 0
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Printf("skipping stale library cleanup for %s: list status=%d\n", c.token, resp.StatusCode)
		return 0
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		fmt.Printf("skipping stale library cleanup for %s: decode list response: %v\n", c.token, err)
		return 0
	}

	repos, ok := result["repos"].([]interface{})
	if !ok {
		return 0
	}

	deleted := 0
	for _, entry := range repos {
		repo, ok := entry.(map[string]interface{})
		if !ok {
			continue
		}

		repoID, _ := repo["repo_id"].(string)
		if repoID == "" {
			repoID, _ = repo["id"].(string)
		}
		repoName, _ := repo["repo_name"].(string)
		if repoName == "" {
			repoName, _ = repo["name"].(string)
		}
		if repoID == "" || !isEphemeralLibraryName(repoName) {
			continue
		}
		if _, live := liveRepoIDs.Load(repoID); live {
			continue
		}

		if deleteEphemeralLibrary(c, repoID, repoName) {
			deleted++
		}
	}

	return deleted
}

func deleteEphemeralLibrary(c *testClient, repoID, repoName string) bool {
	req, err := http.NewRequest(http.MethodDelete, c.baseURL+"/api/v2.1/repos/"+repoID+"/", nil)
	if err != nil {
		fmt.Printf("failed to create delete request for stale test library %q (%s): %v\n", repoName, repoID, err)
		return false
	}
	req.Header.Set("Authorization", "Token "+c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		fmt.Printf("failed to delete stale test library %q (%s): %v\n", repoName, repoID, err)
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNotFound {
		return true
	}
	fmt.Printf("failed to delete stale test library %q (%s): status=%d\n", repoName, repoID, resp.StatusCode)
	return false
}

func cleanupOwnedEphemeralLibraries(t *testing.T, c *testClient) int {
	t.Helper()

	resp := c.Get(t, "/api/v2.1/repos/?type=mine")
	if resp.StatusCode != http.StatusOK {
		body := responseBody(t, resp)
		t.Logf("skipping stale library cleanup for %s: list status=%d body=%s", c.token, resp.StatusCode, body)
		return 0
	}

	var result map[string]interface{}
	decodeJSON(t, resp, &result)

	repos, ok := result["repos"].([]interface{})
	if !ok {
		return 0
	}

	deleted := 0
	for _, entry := range repos {
		repo, ok := entry.(map[string]interface{})
		if !ok {
			continue
		}

		repoID, _ := repo["repo_id"].(string)
		if repoID == "" {
			repoID, _ = repo["id"].(string)
		}
		repoName, _ := repo["repo_name"].(string)
		if repoName == "" {
			repoName, _ = repo["name"].(string)
		}
		if repoID == "" || !isEphemeralLibraryName(repoName) {
			continue
		}
		if _, live := liveRepoIDs.Load(repoID); live {
			continue
		}

		delResp := c.Delete(t, fmt.Sprintf("/api/v2.1/repos/%s/", repoID))
		if delResp.StatusCode == http.StatusOK || delResp.StatusCode == http.StatusNotFound {
			deleted++
		} else {
			body := responseBody(t, delResp)
			t.Logf("failed to delete stale test library %q (%s): status=%d body=%s", repoName, repoID, delResp.StatusCode, body)
			continue
		}
		delResp.Body.Close()
	}

	return deleted
}

func isEphemeralLibraryName(name string) bool {
	for _, exactName := range ephemeralLibraryExactNames {
		if name == exactName {
			return true
		}
	}
	for _, prefix := range ephemeralLibraryPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// decodeJSON decodes a response body into v and closes the body.
func decodeJSON(t *testing.T, resp *http.Response, v interface{}) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("failed to decode JSON response: %v", err)
	}
}

// expectStatus asserts the response has the expected status code.
func expectStatus(t *testing.T, resp *http.Response, expected int) {
	t.Helper()
	if resp.StatusCode != expected {
		t.Errorf("expected status %d, got %d", expected, resp.StatusCode)
	}
}

func TestInterruptedCrossLibraryFixtureCleanupNames(t *testing.T) {
	for name, want := range map[string]bool{
		"cross-lib-src-1791297781":    true,
		"cross-lib-dst-1791297781":    true,
		"cross-lib-src":               false,
		"cross-lib-dst":               false,
		"my-cross-lib-src-1791297781": false,
		"customer-library":            false,
	} {
		if got := isEphemeralLibraryName(name); got != want {
			t.Errorf("cleanup eligibility for %q = %v, want %v", name, got, want)
		}
	}
}
