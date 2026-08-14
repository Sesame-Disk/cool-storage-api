package db

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// producerStatementPattern matches an INSERT into block_references across any
// formatting a Go string literal can carry.
//
// Whitespace-tolerant on purpose, and that is the whole difference between a tripwire
// and a decoration. The earlier version matched the fixed substring
// "INSERT INTO block_references", so a producer written as
//
//	`INSERT
//	 INTO block_references (...)`
//
// was invisible to it — and because knownProducerCount below is a FLOOR, the three
// existing producers kept satisfying it, so a fourth unpinned producer in that shape
// changed nothing and the suite stayed green. \s+ closes that: any run of spaces,
// tabs or newlines between the keywords still matches.
var producerStatementPattern = regexp.MustCompile(`(?i)\bINSERT\s+INTO\s+block_references\b`)

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
// MUTATION-VERIFIED against three shapes of unpinned producer, because the first two
// rounds of this test each passed against a shape they claimed to cover:
//
//	raw backtick literal, newline after INSERT   (broke the fixed-substring needle)
//	interpreted literal, "INSERT\nINTO ..."      (broke the raw-source pre-filter)
//	ordinary single-line literal                 (the baseline)
//
// The second one is the instructive failure: the pattern was whitespace-tolerant and
// would have matched, but the file was skipped before parsing because the raw bytes
// hold `\` and `n` rather than a newline. A guard is only as good as its narrowest
// stage.
//
// STILL OUT OF REACH, stated so nobody mistakes this for a proof. The scan reads
// string literals, so a statement assembled at runtime — fmt.Sprintf, a const
// concatenation, a table name substituted from a variable — is invisible to it no
// matter how the whitespace is normalised. Catching that needs data-flow analysis this
// test does not attempt. A producer built that way must be reviewed by hand; the
// convention is to write reference INSERTs as plain literals so this scan can see them.
//
// Deletes are deliberately out of scope; RemoveBlockReference documents why the
// destructive read's own level, not the delete's, is what keeps that safe.
func TestBlockReferenceProducersPinWriteConsistency(t *testing.T) {
	const (
		pinNeedle = "BlockReferenceWriteConsistency"
		// The three producers that exist today: two in AddBlockReference (TTL and
		// permanent) and one in AddProvisionalBlockReferenceWithExpiry's batch.
		knownProducerCount = 3
	)

	// Scanned from the repository root, not internal/. DB.Session() is exported, so a
	// producer can be written anywhere in the module — cmd/, a future package — and a
	// scan rooted at internal/ would simply not look there. Vendored and non-Go trees
	// are skipped for speed, not for correctness.
	root := filepath.Join("..", "..")
	skipDirs := map[string]bool{
		".git": true, "node_modules": true, "vendor": true, "frontend": true,
	}

	totalProducers := 0
	fset := token.NewFileSet()

	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			if skipDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		// EVERY file is parsed. There used to be a cheap pre-filter here that ran the
		// producer pattern over the raw source and skipped the file on no match,
		// claiming it "can never skip a file the real scan would have flagged". That
		// claim was FALSE, and the counterexample is the most ordinary Go string there
		// is:
		//
		//	"INSERT\nINTO block_references (...)"
		//
		// In the SOURCE BYTES the separator is the two characters `\` and `n`, which
		// `\s+` does not match — so the pre-filter skipped the file, while the AST walk
		// unquotes the literal first, turning it into a real newline the same pattern
		// does match. The guard's one job is catching a producer nobody noticed, and it
		// was blind to one written the most ordinary way possible. (The mutation that
		// "verified" the hardened pattern used a raw backtick literal, whose newlines
		// are real, so it never exercised this path — see the interpreted-string case
		// in the mutation list below.)
		//
		// No pre-filter at all is a guarantee instead of another almost-true one: any
		// narrower filter has to reason about escaping again, and reasoning about
		// escaping is precisely what went wrong. Parsing the module's non-test .go
		// files costs well under a second.
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
					if typed.Kind != token.STRING {
						return true
					}
					// Unquote so the pattern sees the STATEMENT rather than its Go
					// source form. A raw literal spans real newlines, an interpreted
					// one carries them as the two characters `\` and `n`; unquoting
					// turns both into the same text, so a producer written either way
					// is counted the same.
					value, unquoteErr := strconv.Unquote(typed.Value)
					if unquoteErr != nil {
						value = typed.Value
					}
					if producerStatementPattern.MatchString(value) {
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

	// A scan that matches nothing passes for the wrong reason. With the pattern now
	// whitespace-tolerant this is a much weaker signal than it was — reformatting can
	// no longer blind the scan — but it still catches the case where the table is
	// renamed or the statements move behind a builder the AST walk cannot see. A
	// floor, not an equality: adding a PINNED producer is fine.
	if totalProducers < knownProducerCount {
		t.Fatalf(
			"found %d block_references producers, expected at least %d: either the scan has gone blind — the table was renamed, or the statements are no longer plain string literals, and the write consistency is unguarded — or a producer was legitimately removed, in which case lower knownProducerCount deliberately",
			totalProducers, knownProducerCount,
		)
	}
}
