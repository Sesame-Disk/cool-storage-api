package db

import (
	"go/ast"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// PC-D1B.4 lifecycle-mutation inventory.
//
// docs/PC-D1B-CERTIFICATION-WINDOW-FENCE.md decides which lifecycle mutations
// the certification-window fence must serialize with the witness CAS. That
// decision is only as good as the inventory it was made from, so this guard
// derives the inventory from the source and freezes it: a new statement that
// soft-deletes, restores or removes a canonical `libraries` row, writes the
// continuity witness, or a new call site that destroys witness-covered state,
// turns this test red until it is classified in the ADR and here.
//
// The companion guard TestIdentityWritersAreInventoried already confines every
// commits/fs_objects writer and deleter to the identity gateway. This file adds
// the lifecycle side and the gateway's destroyer call sites, which is where the
// PC-D1B.5 destruction intent has to be wired.

type pcd1b4LifecycleKind string

const (
	pcd1b4SoftDelete    pcd1b4LifecycleKind = "soft-delete"
	pcd1b4Restore       pcd1b4LifecycleKind = "restore"
	pcd1b4RowDelete     pcd1b4LifecycleKind = "row-delete"
	pcd1b4WitnessWrite  pcd1b4LifecycleKind = "witness-write"
	pcd1b4LibraryInsert pcd1b4LifecycleKind = "library-create"
	// A raw block_references delete outside RemoveBlockReference would bypass
	// the fs: reference destroyer inventory.
	pcd1b4ReferenceDelete pcd1b4LifecycleKind = "reference-delete"
)

// pcd1b4FenceRole records what PC-D1B.5 must do with each inventoried site.
type pcd1b4FenceRole string

const (
	// The fence needs no change: the mutation does not change the certified
	// dependency set (soft-delete/restore), or it removes the witness with the
	// canonical row it lives on (hard delete, unpublished rollback).
	pcd1b4NoChange pcd1b4FenceRole = "no-change"
	// A witness writer: its CAS must predicate the captured destruction epoch.
	pcd1b4PredicateEpoch pcd1b4FenceRole = "predicate-epoch"
	// A destroyer of witness-covered state while the canonical row can exist:
	// it must hold a destruction intent (fresh epoch + pending token + witness
	// cleared) before its first destructive write and release it only after
	// every destructive write is acknowledged.
	pcd1b4Participant pcd1b4FenceRole = "participant"
	// Destroys only state that no witness can cover: a library that never
	// published a HEAD, or provisional pub: references.
	pcd1b4NotCovered pcd1b4FenceRole = "not-covered"
	// Best-effort cleanup of a commit proven never to be the canonical HEAD
	// (never proposed, definitive HEAD-CAS loser, or unpromotable initial
	// loser). It needs a ProvenUncoveredCleanup capability, not a pending
	// intent: it has no durable owner that would re-drive a completion, so an
	// intent would turn a tolerated leak into a permanent certification block.
	pcd1b4ProvenUncovered pcd1b4FenceRole = "proven-uncovered"
)

type pcd1b4LifecycleSite struct {
	path string
	decl string
	kind pcd1b4LifecycleKind
	role pcd1b4FenceRole
}

var pcd1b4ExpectedLifecycleStatements = []pcd1b4LifecycleSite{
	// Soft-delete: plain LoggedBatch UPDATE of deleted_at, outside the HEAD
	// Paxos domain. It does not remove any certified dependency.
	{path: "internal/api/v2/write_helpers.go", decl: "softDeleteLibrary", kind: pcd1b4SoftDelete, role: pcd1b4NoChange},
	{path: "internal/gc/store_cassandra.go", decl: "CassandraStore.SoftDeleteLibrary", kind: pcd1b4SoftDelete, role: pcd1b4NoChange},
	// Restore: plain DELETE deleted_at under the hard-delete lease. HEAD and
	// the tree are untouched; any destroyer that ran during trash already
	// cleared the witness through its intent.
	{path: "internal/api/v2/write_helpers.go", decl: "restoreDeletedLibrary", kind: pcd1b4Restore, role: pcd1b4NoChange},
	// Canonical row removal: the witness columns go with the row.
	{path: "internal/api/v2/library_delete_helpers.go", decl: "hardDeleteLibraryRowsFn", kind: pcd1b4RowDelete, role: pcd1b4NoChange},
	{path: "internal/gc/store_cassandra.go", decl: "CassandraStore.PrepareLibraryHardDelete", kind: pcd1b4RowDelete, role: pcd1b4NoChange},
	{path: "internal/api/v2/write_helpers.go", decl: "deleteUnpublishedLibraryRow", kind: pcd1b4RowDelete, role: pcd1b4NoChange},
	// Witness writers: the only statements that set the continuity columns.
	{path: "internal/db/library_continuity.go", decl: "CommitLibraryContinuityWitness", kind: pcd1b4WitnessWrite, role: pcd1b4PredicateEpoch},
	{path: "internal/db/library_continuity.go", decl: "CommitLibraryContinuityWitnessContext", kind: pcd1b4WitnessWrite, role: pcd1b4PredicateEpoch},
	{path: "internal/db/library_continuity.go", decl: "AdvanceLibraryCertifiedFrontier", kind: pcd1b4WitnessWrite, role: pcd1b4PredicateEpoch},
	// The single raw block_references delete; every caller is inventoried below.
	{path: "internal/db/block_references.go", decl: "DB.RemoveBlockReference", kind: pcd1b4ReferenceDelete, role: pcd1b4Participant},
}

// Destroyer primitives: the identity gateway's source deletes and the
// block-reference delete. Every call site is classified.
var pcd1b4DestroyerPrimitives = map[string]bool{
	"DeleteCommitIdentity":                                 true,
	"DeleteFSObjectIdentity":                               true,
	"AddUnpublishedLibraryIdentityPartitionDeletesToBatch": true,
	"RemoveBlockReference":                                 true,
}

var pcd1b4ExpectedDestroyerCallSites = []struct {
	path, decl, callee string
	role               pcd1b4FenceRole
}{
	// GC: the only production path that can destroy an identity reachable from
	// the current HEAD (Phase 5 cascade, Phase 6 execute-time TOCTOU), and the
	// only remover of permanent fs: references. Dormant under GC_ENABLED=false.
	{"internal/gc/store_cassandra.go", "CassandraStore.DeleteCommit", "DeleteCommitIdentity", pcd1b4Participant},
	{"internal/gc/store_cassandra.go", "CassandraStore.DeleteFSObject", "DeleteFSObjectIdentity", pcd1b4Participant},
	{"internal/gc/store_cassandra.go", "CassandraStore.RemoveBlockReference", "RemoveBlockReference", pcd1b4Participant},
	{"internal/gc/worker.go", "Worker.removeFSObjectBlockReferences", "RemoveBlockReference", pcd1b4Participant},
	// D4/D5: best-effort deletes of commits proven never to be HEAD. They get
	// a ProvenUncoveredCleanup capability minted only from those proofs.
	// cleanupFailedPublishDeleteFSObjectFn has no production caller; adding one
	// needs a fence decision (fs_objects are shared with HEAD by content
	// addressing), so it is classified as a participant.
	{"internal/api/v2/publish_repair.go", "cleanupFailedPublishDeleteCommitFn", "DeleteCommitIdentity", pcd1b4ProvenUncovered},
	{"internal/api/v2/publish_repair.go", "cleanupFailedPublishDeleteFSObjectFn", "DeleteFSObjectIdentity", pcd1b4Participant},
	{"internal/api/v2/fs_helpers.go", "FSHelper.InitializeLibraryHeadIfUnset", "DeleteCommitIdentity", pcd1b4ProvenUncovered},
	{"internal/api/v2/fs_helpers.go", "DiscardLosingInitialCommit", "DeleteCommitIdentity", pcd1b4ProvenUncovered},
	// Unpublished-library rollback runs only after the IF head_commit_id = null
	// global-SERIAL authority deleted the canonical row: no witness can exist.
	{"internal/api/v2/library_rollback.go", "cleanupRolledBackLibraryDerivedState", "AddUnpublishedLibraryIdentityPartitionDeletesToBatch", pcd1b4NotCovered},
	// Provisional pub: attempt references are not part of a certified
	// dependency; the certifier relies only on permanent fs: references.
	{"internal/db/block_references.go", "removePublishAttemptReferenceFn", "RemoveBlockReference", pcd1b4NotCovered},
}

var (
	pcd1b4LibrariesTable         = `(?:[A-Za-z_][A-Za-z0-9_]*\s*\.\s*)?libraries\b`
	pcd1b4UpdateLibrariesPattern = regexp.MustCompile(`(?is)\bUPDATE\s+` + pcd1b4LibrariesTable)
	pcd1b4DeletedAtAssignPattern = regexp.MustCompile(`(?i)\bdeleted_at\s*=`)
	pcd1b4RestorePattern         = regexp.MustCompile(`(?is)\bDELETE\s+[^;]*?\bdeleted_at\b[^;]*?\bFROM\s+` + pcd1b4LibrariesTable)
	pcd1b4RowDeletePattern       = regexp.MustCompile(`(?is)\bDELETE\s+FROM\s+` + pcd1b4LibrariesTable)
	pcd1b4WitnessWritePattern    = regexp.MustCompile(`(?is)\b(?:UPDATE|INSERT\s+INTO)\s+` + pcd1b4LibrariesTable + `[^;]*?\bcontinuity_(?:certified_head_commit_id|contract_version)\b`)
	pcd1b4ReferenceDeletePattern = regexp.MustCompile(`(?is)\bDELETE\b[^;]*?\bFROM\s+(?:[A-Za-z_][A-Za-z0-9_]*\s*\.\s*)?block_references\b`)
	pcd1b4LibraryInsertPattern   = regexp.MustCompile(`(?is)\bINSERT\s+INTO\s+` + pcd1b4LibrariesTable)
	pcd1b4FenceColumnPattern     = regexp.MustCompile(`(?i)\bcontinuity_destruction_(?:epoch|pending|superseded)\b`)
)

// pcd1b4SetClauseAssignsDeletedAt looks only at the SET clause of an UPDATE on
// libraries: the witness CAS predicates `IF ... deleted_at = null`, which is a
// condition, not a soft-delete.
func pcd1b4SetClauseAssignsDeletedAt(prepared string) bool {
	for _, loc := range pcd1b4UpdateLibrariesPattern.FindAllStringIndex(prepared, -1) {
		rest := prepared[loc[1]:]
		set := pc0SETKeywordPattern.FindStringIndex(rest)
		if set == nil {
			continue
		}
		clause := rest[set[1]:]
		if where := pc0WHEREKeywordPattern.FindStringIndex(clause); where != nil {
			clause = clause[:where[0]]
		}
		if pcd1b4DeletedAtAssignPattern.MatchString(clause) {
			return true
		}
	}
	return false
}

func pcd1b4ClassifyStatement(statement string) []pcd1b4LifecycleKind {
	prepared := pc0PreparedCQL(statement)
	var kinds []pcd1b4LifecycleKind
	if pcd1b4SetClauseAssignsDeletedAt(prepared) {
		kinds = append(kinds, pcd1b4SoftDelete)
	}
	if pcd1b4RestorePattern.MatchString(prepared) {
		kinds = append(kinds, pcd1b4Restore)
	}
	if pcd1b4RowDeletePattern.MatchString(prepared) {
		kinds = append(kinds, pcd1b4RowDelete)
	}
	if pcd1b4WitnessWritePattern.MatchString(prepared) {
		kinds = append(kinds, pcd1b4WitnessWrite)
	}
	if pcd1b4ReferenceDeletePattern.MatchString(prepared) {
		kinds = append(kinds, pcd1b4ReferenceDelete)
	}
	if pcd1b4LibraryInsertPattern.MatchString(prepared) {
		kinds = append(kinds, pcd1b4LibraryInsert)
	}
	return kinds
}

type pcd1b4SourceScan struct {
	statements map[string]map[pcd1b4LifecycleKind]int
	destroyers map[string]map[string]int
	// references are uses of a destroyer primitive that are not a direct
	// call: a function value, alias or method value callable from anywhere
	// without appearing as a destroyer call site.
	references  map[string]map[string]int
	fenceWrites map[string]int
}

type pcd1b4DeclUnit struct {
	name string
	node ast.Node
}

// pcd1b4DeclUnits splits a file into named units. Unlike pc0HeadColumnDeclName,
// every ValueSpec of a grouped var block is its own unit, so a function value
// such as hardDeleteLibraryRowsFn is attributed to its own name.
func pcd1b4DeclUnits(file *ast.File) []pcd1b4DeclUnit {
	var units []pcd1b4DeclUnit
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok {
			units = append(units, pcd1b4DeclUnit{name: pc0HeadColumnDeclName(decl), node: decl})
			continue
		}
		for _, spec := range gen.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok || len(value.Names) == 0 {
				continue
			}
			units = append(units, pcd1b4DeclUnit{name: value.Names[0].Name, node: value})
		}
	}
	return units
}

func pcd1b4ScanSource(t *testing.T, roots ...string) pcd1b4SourceScan {
	t.Helper()
	scan := pcd1b4SourceScan{
		statements:  map[string]map[pcd1b4LifecycleKind]int{},
		destroyers:  map[string]map[string]int{},
		references:  map[string]map[string]int{},
		fenceWrites: map[string]int{},
	}
	repoRoot := r3RepositoryRoot(t)
	for _, root := range roots {
		walkErr := filepath.WalkDir(filepath.Join(repoRoot, root), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				return nil
			}
			relPath, relErr := filepath.Rel(repoRoot, path)
			if relErr != nil {
				return relErr
			}
			relPath = filepath.ToSlash(relPath)
			file := r3ParseProductionFile(t, path)
			bindings := pc0PackageConstStrings(file)
			for _, unit := range pcd1b4DeclUnits(file) {
				key := pc0CallerKey(relPath, unit.name)
				var statements []string
				calledOrDeclared := map[*ast.Ident]bool{}
				ast.Inspect(unit.node, func(node ast.Node) bool {
					switch typed := node.(type) {
					case *ast.CallExpr:
						switch fun := typed.Fun.(type) {
						case *ast.SelectorExpr:
							calledOrDeclared[fun.Sel] = true
						case *ast.Ident:
							calledOrDeclared[fun] = true
						}
					case *ast.FuncDecl:
						calledOrDeclared[typed.Name] = true
					case *ast.Field:
						for _, name := range typed.Names {
							calledOrDeclared[name] = true
						}
					}
					return true
				})
				ast.Inspect(unit.node, func(node ast.Node) bool {
					if ident, ok := node.(*ast.Ident); ok && pcd1b4DestroyerPrimitives[ident.Name] && !calledOrDeclared[ident] {
						if scan.references[key] == nil {
							scan.references[key] = map[string]int{}
						}
						scan.references[key][ident.Name]++
					}
					switch typed := node.(type) {
					case *ast.BinaryExpr:
						if typed.Op == token.ADD {
							if value, ok := pc0ResolveStringExpr(typed, bindings); ok {
								statements = append(statements, value)
								return false
							}
						}
					case *ast.BasicLit:
						if typed.Kind == token.STRING {
							if value, err := strconv.Unquote(typed.Value); err == nil {
								statements = append(statements, value)
							}
						}
					case *ast.CallExpr:
						callee := ""
						switch fun := typed.Fun.(type) {
						case *ast.SelectorExpr:
							callee = fun.Sel.Name
						case *ast.Ident:
							callee = fun.Name
						}
						if pcd1b4DestroyerPrimitives[callee] {
							if scan.destroyers[key] == nil {
								scan.destroyers[key] = map[string]int{}
							}
							scan.destroyers[key][callee]++
						}
					}
					return true
				})
				for _, statement := range statements {
					for _, kind := range pcd1b4ClassifyStatement(statement) {
						if scan.statements[key] == nil {
							scan.statements[key] = map[pcd1b4LifecycleKind]int{}
						}
						scan.statements[key][kind]++
					}
					if pcd1b4FenceColumnPattern.MatchString(pc0PreparedCQL(statement)) {
						scan.fenceWrites[key]++
					}
				}
			}
			return nil
		})
		if walkErr != nil {
			t.Fatalf("PC-D1B.4 LIFECYCLE: walk %s: %v", root, walkErr)
		}
	}
	return scan
}

func TestPCD1B4LifecycleStatementsAreInventoried(t *testing.T) {
	scan := pcd1b4ScanSource(t, "internal", "cmd")
	expected := map[string]map[pcd1b4LifecycleKind]bool{}
	for _, site := range pcd1b4ExpectedLifecycleStatements {
		key := pc0CallerKey(site.path, site.decl)
		if expected[key] == nil {
			expected[key] = map[pcd1b4LifecycleKind]bool{}
		}
		expected[key][site.kind] = true
	}

	var drift []string
	for key, kinds := range scan.statements {
		for kind := range kinds {
			if kind == pcd1b4LibraryInsert {
				continue
			}
			if !expected[key][kind] {
				drift = append(drift, "unlisted "+string(kind)+" at "+key)
			}
		}
	}
	for key, kinds := range expected {
		for kind := range kinds {
			if scan.statements[key][kind] == 0 {
				drift = append(drift, "inventoried "+string(kind)+" no longer found at "+key)
			}
		}
	}
	sort.Strings(drift)
	if len(drift) > 0 {
		t.Fatalf("PC-D1B.4 LIFECYCLE: the libraries lifecycle inventory drifted from the source:\n  %s\nClassify every soft-delete, restore, canonical row delete and witness write in docs/PC-D1B-CERTIFICATION-WINDOW-FENCE.md and pcd1b4ExpectedLifecycleStatements", strings.Join(drift, "\n  "))
	}
}

func TestPCD1B4DestroyerCallSitesAreInventoried(t *testing.T) {
	scan := pcd1b4ScanSource(t, "internal", "cmd")
	expected := map[string]map[string]bool{}
	for _, site := range pcd1b4ExpectedDestroyerCallSites {
		key := pc0CallerKey(site.path, site.decl)
		if expected[key] == nil {
			expected[key] = map[string]bool{}
		}
		expected[key][site.callee] = true
	}
	var drift []string
	for key, callees := range scan.destroyers {
		for callee := range callees {
			if !expected[key][callee] {
				drift = append(drift, "unlisted "+callee+" call at "+key)
			}
		}
	}
	for key, callees := range expected {
		for callee := range callees {
			if scan.destroyers[key][callee] == 0 {
				drift = append(drift, "inventoried "+callee+" call no longer found at "+key)
			}
		}
	}
	sort.Strings(drift)
	if len(drift) > 0 {
		t.Fatalf("PC-D1B.4 LIFECYCLE: destroyer call sites drifted from the source:\n  %s\nEvery destroyer of witness-covered state must be classified as a PC-D1B.5 destruction-intent participant or proven not covered", strings.Join(drift, "\n  "))
	}
}

// No production code writes the future fence columns yet. PC-D1B.5 introduces
// them; this guard makes that introduction a deliberate, reviewed change.
func TestPCD1B4FenceColumnsAreNotYetWritten(t *testing.T) {
	scan := pcd1b4ScanSource(t, "internal", "cmd")
	if len(scan.fenceWrites) != 0 {
		keys := make([]string, 0, len(scan.fenceWrites))
		for key := range scan.fenceWrites {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		t.Fatalf("PC-D1B.4 LIFECYCLE: fence columns appear in production code %v; update the PC-D1B.4 inventory and the ADR in the runtime PR", keys)
	}
}

// Every production INSERT INTO libraries binds library_id to a UUID minted by
// uuid.New / uuid.NewString in the same declaration. The ADR relies on library
// ids never being reused, so a hard delete cannot be followed by a new
// lifecycle under the same key.
func TestPCD1B4LibraryCreatorsMintFreshIDs(t *testing.T) {
	repoRoot := r3RepositoryRoot(t)
	creators := 0
	var violations []string
	for _, root := range []string{"internal", "cmd"} {
		walkErr := filepath.WalkDir(filepath.Join(repoRoot, root), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				return nil
			}
			relPath, relErr := filepath.Rel(repoRoot, path)
			if relErr != nil {
				return relErr
			}
			file := r3ParseProductionFile(t, path)
			bindings := pc0PackageConstStrings(file)
			for _, unit := range pcd1b4DeclUnits(file) {
				key := pc0CallerKey(filepath.ToSlash(relPath), unit.name)
				assignments := map[string]ast.Expr{}
				ast.Inspect(unit.node, func(node ast.Node) bool {
					if assign, ok := node.(*ast.AssignStmt); ok && len(assign.Lhs) == len(assign.Rhs) {
						for i, lhs := range assign.Lhs {
							if ident, ok := lhs.(*ast.Ident); ok {
								assignments[ident.Name] = assign.Rhs[i]
							}
						}
					}
					return true
				})
				ast.Inspect(unit.node, func(node ast.Node) bool {
					call, ok := node.(*ast.CallExpr)
					if !ok || len(call.Args) == 0 {
						return true
					}
					selector, ok := call.Fun.(*ast.SelectorExpr)
					if !ok || selector.Sel.Name != "Query" {
						return true
					}
					statement, ok := pc0ResolveStringExpr(call.Args[0], bindings)
					if !ok {
						return true
					}
					match := pc0InsertIntoPattern.FindStringSubmatch(pc0PreparedCQL(statement))
					if match == nil || pc0CQLMatchTable(match) != "libraries" {
						return true
					}
					creators++
					index := -1
					for i, column := range strings.Split(match[3], ",") {
						if pc0NormalizeCQLIdentifier(column) == "library_id" {
							index = i
						}
					}
					if index < 0 || len(call.Args) <= index+1 {
						violations = append(violations, key+" (library_id column not bound positionally)")
						return true
					}
					if !pcd1b4FreshUUIDExpr(call.Args[index+1], assignments, 0) {
						violations = append(violations, key)
					}
					return true
				})
			}
			return nil
		})
		if walkErr != nil {
			t.Fatalf("PC-D1B.4 LIFECYCLE: walk %s: %v", root, walkErr)
		}
	}
	sort.Strings(violations)
	// Six creators today: v2 CreateLibrary (plain and encrypted), admin
	// create, admin-extra, org-admin group library and group library.
	if creators != pcd1b4ExpectedLibraryCreators {
		t.Fatalf("PC-D1B.4 LIFECYCLE: found %d INSERT INTO libraries statements, want %d; classify the change in docs/PC-D1B-CERTIFICATION-WINDOW-FENCE.md", creators, pcd1b4ExpectedLibraryCreators)
	}
	if len(violations) > 0 {
		t.Fatalf("PC-D1B.4 LIFECYCLE: library creators whose library_id is not a freshly minted UUID: %v", violations)
	}
}

const pcd1b4ExpectedLibraryCreators = 6

// pcd1b4FreshUUIDExpr accepts uuid.New(), uuid.NewString(), x.String() of an
// accepted value, and identifiers assigned from an accepted value.
func pcd1b4FreshUUIDExpr(expr ast.Expr, assignments map[string]ast.Expr, depth int) bool {
	if depth > 8 {
		return false
	}
	switch typed := expr.(type) {
	case *ast.ParenExpr:
		return pcd1b4FreshUUIDExpr(typed.X, assignments, depth+1)
	case *ast.Ident:
		rhs, ok := assignments[typed.Name]
		return ok && pcd1b4FreshUUIDExpr(rhs, assignments, depth+1)
	case *ast.CallExpr:
		selector, ok := typed.Fun.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		if pkg, ok := selector.X.(*ast.Ident); ok && pkg.Name == "uuid" && (selector.Sel.Name == "New" || selector.Sel.Name == "NewString") {
			return true
		}
		if selector.Sel.Name == "String" && len(typed.Args) == 0 {
			return pcd1b4FreshUUIDExpr(selector.X, assignments, depth+1)
		}
	}
	return false
}

// A destroyer primitive may only be called directly. A function value or
// alias (var f = db.DeleteFSObjectIdentity) would let any caller destroy
// without appearing in pcd1b4ExpectedDestroyerCallSites, so none may exist.
// New callers of an already-inventoried wrapper (for example the GC store's
// DeleteFSObject) are not traced: the PC-D1B.5 capability parameter on the
// destructive primitives is the structural boundary, and this inventory is
// defense in depth that freezes the currently recognized production sites.
func TestPCD1B4DestroyerPrimitivesAreNotAliased(t *testing.T) {
	scan := pcd1b4ScanSource(t, "internal", "cmd")
	var aliases []string
	for key, names := range scan.references {
		for name := range names {
			aliases = append(aliases, name+" referenced without a call at "+key)
		}
	}
	sort.Strings(aliases)
	if len(aliases) > 0 {
		t.Fatalf("PC-D1B.4 LIFECYCLE: destroyer primitive aliased:\n  %s\nCall destroyer primitives directly so every destroyer site stays inventoried", strings.Join(aliases, "\n  "))
	}
}
