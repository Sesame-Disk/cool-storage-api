package gc

import (
	"go/ast"
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
		"deliberately conservative strategy",
		"independent physical lives",
		"writers stay fenced until G4",
		"G4 owns `blocks=P2` + `orphan=P1`",
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

	decidedSection := sectionBetween(t, text, "## 10. DECIDED handoff protocol", "## 11. Finalize frees `blocks(L)`; writers stay fenced until G4")
	for _, requiredDecided := range []string{"PREPARED", "COMMITTED", "CommitBlockDeleteHandoff"} {
		if !strings.Contains(decidedSection, requiredDecided) {
			t.Fatalf("DECIDED protocol section must name %q", requiredDecided)
		}
	}
	if !strings.Contains(decidedSection, "Not current `processBlock`") {
		t.Fatal("DECIDED protocol must say it is not current processBlock")
	}

	if strings.Contains(text, "while physical identity was still reusable") {
		t.Fatal("#199 already used independent lives after #185; D0 must not explain strict non-overlap as leftover reusable physical identity")
	}
	if strings.Contains(text, "## 11. P2 may be born immediately after Finalize") {
		t.Fatal("section 11 must not treat Finalize as writer permission to install P2")
	}

	g3 := sectionBetween(t, text, "### G3 — Canonical retirement after committed handoff", "### G4 — Remove orphan from writer fencing; detach post-D refs")
	for _, forbidden := range []string{
		"After Finalize, P2 may install",
		"This PR must demonstrate `blocks=P2`",
	} {
		if strings.Contains(g3, forbidden) {
			t.Fatalf("G3 must not claim writer permission or the coexistence demonstration: %q", forbidden)
		}
	}
	for _, requiredG3 := range []string{
		"blocks(L)` is architecturally free",
		"Writers are still blocked",
		"not productive until G4",
	} {
		if !strings.Contains(g3, requiredG3) {
			t.Fatalf("G3 must name %q", requiredG3)
		}
	}

	g4 := sectionBetween(t, text, "### G4 — Remove orphan from writer fencing; detach post-D refs", "### G5 — Recovery scheduling hardening")
	for _, requiredG4 := range []string{
		"This PR must demonstrate `blocks=P2` + `orphan=P1` is a valid state",
		"ProbeBlockReuse",
		"BlockDeleteFenceActive",
		"ValidateBlockRepairAuthority",
		"Only after W1+W2+G3",
	} {
		if !strings.Contains(g4, requiredG4) {
			t.Fatalf("G4 must name %q", requiredG4)
		}
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
	_, file := x1ParseFile(t, "internal", "db", "block_references.go")
	const orphanSelect = "SELECT block_id FROM gc_s3_orphans WHERE org_id = ? AND block_id = ?"
	const probeHelper = "probeBlockReuseHasS3OrphanFn"
	const fenceHelper = "blockDeleteFenceHasS3OrphanFn"
	const repairHelper = "blockRepairHasS3OrphanFn"

	x1RequireOrphanFenceOnProbeBlockReuse(t, x1Func(t, file, "ProbeBlockReuse"), probeHelper)

	fence := x1Func(t, file, "BlockDeleteFenceActive")
	orphanCall := x1FirstCallIn(t, fence.Body, "BlockDeleteFenceActive", fenceHelper)
	ast.Inspect(fence.Body, func(n ast.Node) bool {
		ret, ok := n.(*ast.ReturnStmt)
		if !ok || ret.Pos() >= orphanCall {
			return true
		}
		if x1ReturnFalseNil(ret) {
			t.Fatal("BlockDeleteFenceActive must not return false,nil before consulting the orphan fence; a rowless read is not an early no-fence")
		}
		return true
	})

	_ = x1FirstCallIn(t, x1Func(t, file, "ValidateBlockRepairAuthority").Body, "ValidateBlockRepairAuthority", "validateBlockRepairAuthority")
	_ = x1FirstCallIn(t, x1Func(t, file, "validateBlockRepairAuthority").Body, "validateBlockRepairAuthority", repairHelper)
	_ = x1FirstCallIn(t, x1Func(t, file, "RepairBlockMetadataIfCurrent").Body, "RepairBlockMetadataIfCurrent", "validateBlockRepairAuthority")
	_ = x1FirstCallIn(t, x1Func(t, file, "RepairReleasedBlockStub").Body, "RepairReleasedBlockStub", probeHelper)

	for _, name := range []string{probeHelper, fenceHelper, repairHelper} {
		lit := x1AssignedFuncLit(t, file, name)
		if !x1NodeContainsString(lit, orphanSelect) {
			t.Fatalf("%s must SELECT gc_s3_orphans by (org_id, block_id); D0 must not claim G4 already landed", name)
		}
	}

	raw, err := os.ReadFile(x1SourcePath("internal", "db", "block_references.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "pending gc_s3_orphans row as an active fence") {
		t.Fatal("BlockDeleteFenceActive must still document the transitional orphan-as-fence role")
	}
}

func x1RequireOrphanFenceOnProbeBlockReuse(t *testing.T, fn *ast.FuncDecl, helper string) {
	t.Helper()
	var sawNotFound, sawStub bool
	pastStub := false
	liveCalls := 0
	for _, stmt := range fn.Body.List {
		ifstmt, ok := stmt.(*ast.IfStmt)
		switch {
		case ok && x1UnaryNotIdent(ifstmt.Cond, "found"):
			if x1CountCalls(ifstmt.Body, helper) < 1 {
				t.Fatal("rowless ProbeBlockReuse must call probeBlockReuseHasS3OrphanFn")
			}
			sawNotFound = true
		case ok && x1IsCreatedAtNil(ifstmt.Cond):
			if x1CountCalls(ifstmt.Body, helper) < 1 {
				t.Fatal("stub ProbeBlockReuse must call probeBlockReuseHasS3OrphanFn")
			}
			sawStub = true
			pastStub = true
		case pastStub:
			liveCalls += x1CountCalls(stmt, helper)
		}
	}
	if !sawNotFound || !sawStub {
		t.Fatal("ProbeBlockReuse must keep distinct rowless and stub branches, each consulting the orphan fence")
	}
	if liveCalls < 1 {
		t.Fatal("canonical ProbeBlockReuse path must call probeBlockReuseHasS3OrphanFn; D0 must not claim G4 already landed")
	}
}

func x1AssignedFuncLit(t *testing.T, file *ast.File, name string) *ast.FuncLit {
	t.Helper()
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, ident := range vs.Names {
				if ident.Name != name {
					continue
				}
				if i >= len(vs.Values) {
					t.Fatalf("%s has no initializer", name)
				}
				lit, ok := vs.Values[i].(*ast.FuncLit)
				if !ok {
					t.Fatalf("%s is not a function literal", name)
				}
				return lit
			}
		}
	}
	t.Fatalf("%s not found", name)
	return nil
}

func x1CountCalls(node ast.Node, name string) int {
	if node == nil {
		return 0
	}
	n := 0
	ast.Inspect(node, func(x ast.Node) bool {
		call, ok := x.(*ast.CallExpr)
		if ok && x1CallName(call) == name {
			n++
		}
		return true
	})
	return n
}

func x1NodeContainsString(node ast.Node, needle string) bool {
	found := false
	ast.Inspect(node, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		if strings.Contains(lit.Value, needle) {
			found = true
		}
		return true
	})
	return found
}

func x1ReturnFalseNil(ret *ast.ReturnStmt) bool {
	if len(ret.Results) != 2 {
		return false
	}
	a, okA := ret.Results[0].(*ast.Ident)
	b, okB := ret.Results[1].(*ast.Ident)
	return okA && okB && a.Name == "false" && b.Name == "nil"
}

func x1UnaryNotIdent(cond ast.Expr, name string) bool {
	u, ok := cond.(*ast.UnaryExpr)
	if !ok || u.Op != token.NOT {
		return false
	}
	id, ok := u.X.(*ast.Ident)
	return ok && id.Name == name
}

func x1IsCreatedAtNil(cond ast.Expr) bool {
	bin, ok := cond.(*ast.BinaryExpr)
	if !ok || bin.Op != token.EQL {
		return false
	}
	return (x1SelectorNamed(bin.X, "CreatedAt") && x1IdentNamed(bin.Y, "nil")) ||
		(x1SelectorNamed(bin.Y, "CreatedAt") && x1IdentNamed(bin.X, "nil"))
}

func x1SelectorNamed(expr ast.Expr, name string) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == name
}

func x1IdentNamed(expr ast.Expr, name string) bool {
	id, ok := expr.(*ast.Ident)
	return ok && id.Name == name
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
