package gc

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func x1HandoffPlanPath() string {
	if root := strings.TrimSpace(os.Getenv(x1SourceRootEnv)); root != "" {
		return filepath.Join(root, "docs", "GC-X1-PHYSICAL-LIFE-HANDOFF-PLAN.md")
	}
	return filepath.Join("..", "..", "docs", "GC-X1-PHYSICAL-LIFE-HANDOFF-PLAN.md")
}

func TestX1PhysicalLifeHandoffPlanIsDocumented(t *testing.T) {
	raw, err := os.ReadFile(x1HandoffPlanPath())
	if err != nil {
		t.Fatalf("read physical-life handoff plan: %v", err)
	}
	text := string(raw)

	required := []string{
		"X1 remains OPEN",
		"GC_ENABLED=false",
		"activation is explicitly outside X1 closure",
		"P = (storage_class, storage_key)",
		"`P` is never a synonym of `storage_key`",
		"`PREPARED` is **not** current production protocol",
		"CURRENT / TRANSITIONAL",
		"DECIDED",
		"PROVEN",
		"PENDING RE-EVALUATION",
		"SUPERSEDED as the closure architecture",
		"P4c-orphan",
		"PRIMARY KEY is still",
		"`((org_id, block_id))`",
		"writer fence",
		"ProbeBlockReuse",
		"BlockDeleteFenceActive",
		"ValidateBlockRepairAuthority",
		"FinalizeBlockDelete",
		"DeleteBlockByStorageKey",
		"BlockHasReferencesGlobal",
		"CommittedOwner",
		"RecoverS3Orphans",
		"default_time_to_live = 7776000",
		"must not disappear by TTL while still pending",
		"R31 remains a blocker",
		"D0 does **not** reclassify H as X3",
		"R18 / R27 — `PENDING RE-EVALUATION`",
		"Lifecycle 020 — keep",
		"Only E1 may declare `X1 CLOSED`",
		"A1 — GC activation",
		"#181",
		"#185",
		"#187",
		"#189",
		"#190",
		"#194",
		"#199",
		"#200",
		"orphan PREPARED",
		"orphan COMMITTED owns retirement of P1",
		"P1 cleanup may overlap P2 life",
	}
	for _, needle := range required {
		if !strings.Contains(text, needle) {
			t.Fatalf("physical-life handoff plan is missing %q", needle)
		}
	}

	if strings.Count(text, "**Current verdict:") > 0 {
		t.Fatal("D0 is an architecture freeze, not a characterization; it must not declare a characterization Current verdict")
	}
	if !strings.Contains(text, "this PR has no runtime code, schema, or config") {
		t.Fatal("D0 merge criteria must keep the no-runtime rule")
	}

	// Future protocol must not be described as what processBlock does today.
	currentSection := sectionBetween(t, text, "## 9. CURRENT production protocol (transitional)", "## 10. DECIDED handoff protocol")
	for _, forbidden := range []string{"orphan PREPARED", "PREPARED → COMMITTED", "gc_s3_orphans(P1,D1, PREPARED)"} {
		if strings.Contains(currentSection, forbidden) {
			t.Fatalf("CURRENT protocol section must not describe DECIDED prepare/promote as production: %q", forbidden)
		}
	}
	for _, requiredCurrent := range []string{
		"CommitBlockDeleteOrphanHandoff",
		"StartBlockDeleteOrphan",
		"FinalizeBlockDelete",
		"DeleteBlockByStorageKey",
		"BlockHasReferencesGlobal",
	} {
		if !strings.Contains(currentSection, requiredCurrent) {
			t.Fatalf("CURRENT protocol section must name %q", requiredCurrent)
		}
	}

	decidedSection := sectionBetween(t, text, "## 10. DECIDED handoff protocol", "## 11. P2 may be born immediately after Finalize")
	for _, requiredDecided := range []string{"PREPARED", "COMMITTED", "CommitBlockDeleteHandoff"} {
		if !strings.Contains(decidedSection, requiredDecided) {
			t.Fatalf("DECIDED protocol section must name %q", requiredDecided)
		}
	}
	if !strings.Contains(decidedSection, "Not current `processBlock`") {
		t.Fatal("DECIDED protocol must say it is not current processBlock")
	}
}

func TestX1PhysicalLifeHandoffCurrentProcessBlockOrder(t *testing.T) {
	source, err := os.ReadFile("worker.go")
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), "worker.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	fn := findGCFunction(file, "processBlock")
	if fn == nil {
		t.Fatal("processBlock not found; D0 current-order pin is vacuous")
	}

	handoff := x1FirstCallIn(t, fn.Body, "processBlock", "CommitBlockDeleteOrphanHandoff")
	orphan := x1FirstCallIn(t, fn.Body, "processBlock", "StartBlockDeleteOrphan")
	finalize := x1FirstCallIn(t, fn.Body, "processBlock", "FinalizeBlockDelete")
	del := x1FirstCallIn(t, fn.Body, "processBlock", "deleteS3WithRetry")
	if !(handoff < orphan && orphan < finalize && finalize < del) {
		t.Fatal("CURRENT processBlock must be handoff → StartBlockDeleteOrphan → FinalizeBlockDelete → deleteS3WithRetry; D0 must not describe Delete-before-Finalize or PREPARED-before-commit as production")
	}

	recovery := findGCFunction(file, "RecoverS3Orphans")
	if recovery == nil {
		t.Fatal("RecoverS3Orphans not found; D0 recovery-ref pin is vacuous")
	}
	_ = x1FirstCallIn(t, recovery.Body, "RecoverS3Orphans", "BlockHasReferencesGlobal")
}

func TestX1PhysicalLifeHandoffCurrentOrphanIdentityAndTTL(t *testing.T) {
	schema, err := os.ReadFile(filepath.Join("..", "db", "migrations", "001_initial_schema.cql"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(schema)
	orphans := regexp.MustCompile(`(?s)CREATE TABLE IF NOT EXISTS gc_s3_orphans \((.*?);`).FindStringSubmatch(text)
	if len(orphans) < 2 {
		t.Fatal("could not find CREATE TABLE gc_s3_orphans")
	}
	if !strings.Contains(orphans[1], "PRIMARY KEY ((org_id, block_id))") {
		t.Fatal("P4c-orphan is still open: gc_s3_orphans PRIMARY KEY must remain ((org_id, block_id)) until G1")
	}
	if !strings.Contains(orphans[1], "default_time_to_live = 7776000") {
		t.Fatal("current pending-orphan TTL is still 7776000; D0 must not present that as acceptable post-handoff authority")
	}

	byDay := regexp.MustCompile(`(?s)CREATE TABLE IF NOT EXISTS gc_s3_orphans_by_day \((.*?);`).FindStringSubmatch(text)
	if len(byDay) < 2 {
		t.Fatal("could not find CREATE TABLE gc_s3_orphans_by_day")
	}
	if !strings.Contains(byDay[1], "default_time_to_live = 7776000") {
		t.Fatal("gc_s3_orphans_by_day still carries the 90-day TTL in the base schema")
	}

	m019, err := os.ReadFile(filepath.Join("..", "db", "migrations", "019_gc_block_delete_authority_handoff.cql"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(m019), "PRIMARY KEY of gc_s3_orphans / gc_s3_orphans_by_day is unchanged (P4c-orphan)") {
		t.Fatal("migration 019 must still record that the orphan PK was left for P4c-orphan")
	}

	m020, err := os.ReadFile(filepath.Join("..", "db", "migrations", "020_gc_block_delete_lifecycle.cql"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(m020), "CREATE TABLE IF NOT EXISTS gc_block_delete_lifecycles") {
		t.Fatal("D0 keeps lifecycle 020; the migration must still create gc_block_delete_lifecycles")
	}
}

func TestX1PhysicalLifeHandoffCurrentWriterStillFencesOnOrphan(t *testing.T) {
	path := filepath.Join("..", "db", "block_references.go")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, fn := range []string{"ProbeBlockReuse", "BlockDeleteFenceActive", "ValidateBlockRepairAuthority"} {
		if !strings.Contains(text, "func (db *DB) "+fn+"(") {
			t.Fatalf("%s not found in block_references.go", fn)
		}
	}
	if !strings.Contains(text, "SELECT block_id FROM gc_s3_orphans WHERE org_id = ? AND block_id = ?") {
		t.Fatal("CURRENT writer fence still keys gc_s3_orphans by (org_id, block_id); D0 must not claim G4 already landed")
	}
	if !strings.Contains(text, "pending gc_s3_orphans row as an active fence") {
		t.Fatal("BlockDeleteFenceActive must still document the transitional orphan-as-fence role")
	}
}

func sectionBetween(t *testing.T, text, start, end string) string {
	t.Helper()
	i := strings.Index(text, start)
	if i < 0 {
		t.Fatalf("missing section start %q", start)
	}
	j := strings.Index(text[i:], end)
	if j < 0 {
		t.Fatalf("missing section end %q after %q", end, start)
	}
	return text[i : i+j]
}
