package db

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBlockReferenceProducersPinWriteConsistency is a tripwire on the WRITE half of
// the destructive-GC liveness argument (ISSUE-GC-CROSS-DC-REFERENCE-VISIBILITY-01).
//
// BlockHasReferencesGlobal reads at EACH_QUORUM so that a FALSE answer may authorize
// destroying bytes. That answer is sound only because every reference write reaches a
// quorum in the datacenter that acknowledged it, which is what makes the write and the
// read intersect. Nothing about a single pinned INSERT keeps that true tomorrow: the
// property is "ALL producers write at a quorum", and a producer added later inherits
// the session by default, where `ONE` is an accepted `database.consistency`.
//
// The topology gate cannot cover this. It reads the consistency of the process running
// GC, while references are written by API nodes — other processes, other configuration,
// invisible to the worker. So the enforcement has to live with the writers, and this
// test is what notices when a new writer forgets.
//
// WHAT IT PROVES AND WHAT IT DOES NOT. It is a syntactic scan, not a semantic check:
// it asserts that any function issuing an `INSERT INTO block_references` also names
// BlockReferenceWriteConsistency at least as many times as it inserts. That catches
// the realistic mistake — a new producer written from the pattern of a neighbouring
// query, with no pin at all. It cannot prove the pin is attached to THAT statement, so
// treat a failure as "reason about the new producer", not "add the identifier".
//
// Both sides are counted over the AST rather than the file text, which is not a detail:
// counting text let a COMMENT mentioning the constant stand in for the pin, and the
// comment explaining why the batch is pinned did exactly that — removing the batch's
// real pin left this test green.
//
// Deletes are deliberately out of scope; RemoveBlockReference documents why an
// under-replicated DELETE fails toward keeping data.
func TestBlockReferenceProducersPinWriteConsistency(t *testing.T) {
	const (
		producerNeedle = "INSERT INTO block_references"
		pinNeedle      = "BlockReferenceWriteConsistency"
		// The three producers that exist today: two in AddBlockReference (TTL and
		// permanent) and one in AddProvisionalBlockReferenceWithExpiry's batch.
		knownProducerCount = 3
	)

	// Every CQL statement in the tree lives under internal/, and DB.Session() is
	// exported, so a producer can legitimately appear outside this package.
	root := filepath.Join("..", "..", "internal")

	totalProducers := 0
	fset := token.NewFileSet()

	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		text := string(src)
		if !strings.Contains(text, producerNeedle) {
			return nil
		}

		file, parseErr := parser.ParseFile(fset, path, src, 0)
		if parseErr != nil {
			t.Errorf("%s: parse: %v", path, parseErr)
			return nil
		}

		// countIn walks any node and returns the producers (string literals holding the
		// INSERT) and the pins (identifiers naming the constant, which also covers the
		// qualified db.BlockReferenceWriteConsistency form) beneath it. Identifiers and
		// literals only: a comment is not a pin.
		countIn := func(node ast.Node) (producers, pins int) {
			ast.Inspect(node, func(n ast.Node) bool {
				switch typed := n.(type) {
				case *ast.BasicLit:
					if typed.Kind == token.STRING && strings.Contains(typed.Value, producerNeedle) {
						producers++
					}
				case *ast.Ident:
					if typed.Name == pinNeedle {
						pins++
					}
				}
				return true
			})
			return producers, pins
		}

		fileProducers, _ := countIn(file)
		accountedFor := 0
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			producers, pins := countIn(fn)
			if producers == 0 {
				continue
			}
			accountedFor += producers
			totalProducers += producers

			if pins < producers {
				t.Errorf(
					"%s: %s writes block_references %d time(s) but names %s only %d time(s).\n"+
						"Every producer must pin the write consistency per statement instead of inheriting the session:\n"+
						"  .Consistency(db.BlockReferenceWriteConsistency)   // or, on a batch, .Batch(...).Consistency(...)\n"+
						"An under-replicated reference is invisible to the EACH_QUORUM liveness read, and GC deletes the\n"+
						"bytes it points at (ISSUE-GC-CROSS-DC-REFERENCE-VISIBILITY-01).",
					path, fn.Name.Name, producers, pinNeedle, pins,
				)
			}
		}

		if fileProducers != accountedFor {
			t.Errorf(
				"%s: %d of %d block_references producers are outside any function declaration, so this test cannot check them. Move the statement into a function or extend this scan.",
				path, fileProducers-accountedFor, fileProducers,
			)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan %s: %v", root, err)
	}

	// A scan that matches nothing passes for the wrong reason. If the statements are
	// ever reformatted so the needle stops matching inside the literal (a line break
	// after INSERT INTO, say), this is the assertion that says so instead of going
	// quietly green. A floor, not an equality: adding a PINNED producer is fine.
	if totalProducers < knownProducerCount {
		t.Fatalf(
			"found %d block_references producers, expected at least %d: either the scan has gone blind — a reformatted statement the needle no longer matches, and the write consistency is unguarded — or a producer was legitimately removed, in which case lower knownProducerCount deliberately",
			totalProducers, knownProducerCount,
		)
	}
}
