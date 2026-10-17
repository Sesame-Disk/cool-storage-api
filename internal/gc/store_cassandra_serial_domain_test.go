package gc

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

const hardDeleteLeasePinReason = "hard-delete lease no longer pins global SERIAL"

func TestHardDeleteLeasesPinGlobalSerial(t *testing.T) {
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal(hardDeleteLeasePinReason + ": runtime.Caller failed")
	}
	sourcePath := filepath.Join(filepath.Dir(testFile), "store_cassandra.go")
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, sourcePath, nil, 0)
	if err != nil {
		t.Fatalf("%s: parse %s: %v", hardDeleteLeasePinReason, sourcePath, err)
	}

	// Every session.Query in the lease helpers must be the receiver of
	// SerialConsistency(gocql.Serial); otherwise it inherits the session default.
	for _, helper := range []struct {
		name     string
		lwtCount int
	}{
		{name: "acquireHardDeleteLock", lwtCount: 2},
		{name: "renewHardDeleteLock", lwtCount: 1},
		{name: "releaseHardDeleteLock", lwtCount: 1},
	} {
		queries, pinned := 0, 0
		ast.Inspect(hardDeleteLeaseFuncDecl(t, file, helper.name).Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			method, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			switch method.Sel.Name {
			case "Query":
				queries++
			case "SerialConsistency":
				if isSessionQueryCall(method.X) && len(call.Args) == 1 && isGocqlSelector(call.Args[0], "Serial") {
					pinned++
				}
			}
			return true
		})
		if queries != helper.lwtCount || pinned != helper.lwtCount {
			t.Errorf("%s: %s issues %d session queries with %d pinned to gocql.Serial, want %d of each", hardDeleteLeasePinReason, helper.name, queries, pinned, helper.lwtCount)
		}
	}

	// Every acquire entry point must settle an unknown CAS outcome; otherwise an
	// applied-but-unacknowledged proposal strands the lease until stale takeover.
	for _, entry := range []string{"AcquireLibraryHardDeleteLockLease", "AcquireUserHardDeleteLock", "AcquireOrgHardDeleteLock"} {
		settles := false
		ast.Inspect(hardDeleteLeaseFuncDecl(t, file, entry).Body, func(node ast.Node) bool {
			if call, ok := node.(*ast.CallExpr); ok {
				if fun, ok := call.Fun.(*ast.Ident); ok && fun.Name == "settleHardDeleteLockAcquire" {
					settles = true
				}
			}
			return true
		})
		if !settles {
			t.Errorf("ambiguous hard-delete lease acquire is not settled: %s must call settleHardDeleteLockAcquire", entry)
		}
	}
}

func isSessionQueryCall(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	method, ok := call.Fun.(*ast.SelectorExpr)
	return ok && method.Sel.Name == "Query"
}

func isGocqlSelector(expr ast.Expr, name string) bool {
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != name {
		return false
	}
	pkg, ok := selector.X.(*ast.Ident)
	return ok && pkg.Name == "gocql"
}

func hardDeleteLeaseFuncDecl(t *testing.T, file *ast.File, name string) *ast.FuncDecl {
	t.Helper()
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == name {
			return fn
		}
	}
	t.Fatalf("%s: missing function %s", hardDeleteLeasePinReason, name)
	return nil
}

func TestIsAmbiguousHardDeleteLockCASError(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{name: "cas write unknown", err: &gocql.RequestErrCASWriteUnknown{}, want: true},
		{name: "v4 cas write timeout", err: &gocql.RequestErrWriteTimeout{WriteType: "CAS"}, want: true},
		{name: "v4 cas write failure", err: &gocql.RequestErrWriteFailure{WriteType: "CAS"}, want: true},
		{name: "no response", err: gocql.ErrTimeoutNoResponse, want: true},
		{name: "simple write timeout", err: &gocql.RequestErrWriteTimeout{WriteType: "SIMPLE"}, want: false},
		{name: "unavailable", err: &gocql.RequestErrUnavailable{}, want: false},
	} {
		if got := isAmbiguousHardDeleteLockCASError(tc.err); got != tc.want {
			t.Errorf("%s: ambiguous = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestSettleHardDeleteLockAcquire(t *testing.T) {
	casUnknown := &gocql.RequestErrCASWriteUnknown{}
	unavailable := &gocql.RequestErrUnavailable{}
	for _, tc := range []struct {
		name         string
		acquired     bool
		err          error
		releaseErr   error
		wantAcquired bool
		wantErr      error
		wantRelease  bool
		wantMessage  string
	}{
		{name: "acquired", acquired: true, wantAcquired: true},
		{name: "held by another owner", acquired: false},
		{name: "definite failure passes through", err: unavailable, wantErr: unavailable},
		{name: "unknown outcome attempts own-token release", err: casUnknown, wantErr: casUnknown, wantRelease: true,
			wantMessage: "own-token conditional release attempted"},
		// Settlement is best-effort: a failed release must be reported, never
		// described as a released lease.
		{name: "failed release is reported", err: casUnknown, releaseErr: errors.New("release timeout"), wantErr: casUnknown, wantRelease: true,
			wantMessage: "own-token release also failed (release timeout), the lease may stay held until stale takeover"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			released := false
			acquired, err := settleHardDeleteLockAcquire(tc.acquired, tc.err, func() error {
				released = true
				return tc.releaseErr
			})
			if acquired != tc.wantAcquired || released != tc.wantRelease || !errors.Is(err, tc.wantErr) || (err == nil) != (tc.wantErr == nil) {
				t.Fatalf("acquired=%v err=%v released=%v, want acquired=%v err=%v released=%v", acquired, err, released, tc.wantAcquired, tc.wantErr, tc.wantRelease)
			}
			if tc.wantMessage != "" && !strings.Contains(err.Error(), tc.wantMessage) {
				t.Fatalf("error %q does not contain %q", err, tc.wantMessage)
			}
			if err != nil && strings.Contains(err.Error(), "own token released") {
				t.Fatalf("error %q claims a guaranteed release", err)
			}
		})
	}
}

// Library finalization has its own lease helpers. Keep every one in the global
// SERIAL domain even when the configured session default is LOCAL_SERIAL.
func TestLibraryLifecycleFinalizationPinsGlobalSerial(t *testing.T) {
	_, testFile, _, _ := runtime.Caller(0)
	sourcePath := filepath.Join(filepath.Dir(testFile), "library_lifecycle.go")
	file, err := parser.ParseFile(token.NewFileSet(), sourcePath, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, helper := range []struct {
		name  string
		count int
	}{
		{"acquireLibraryLifecycleLease", 2}, {"renewLibraryLifecycleLease", 2},
		{"releaseLibraryLifecycleLease", 1}, {"WithLibraryLifecycleMutation", 2},
	} {
		queries, pinned := 0, 0
		ast.Inspect(hardDeleteLeaseFuncDecl(t, file, helper.name).Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			method, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if method.Sel.Name == "Query" {
				queries++
			}
			if method.Sel.Name == "SerialConsistency" && isSessionQueryCall(method.X) && len(call.Args) == 1 && isGocqlSelector(call.Args[0], "Serial") {
				pinned++
			}
			return true
		})
		if queries != helper.count || pinned != helper.count {
			t.Errorf("%s: queries=%d pinned=%d want=%d", helper.name, queries, pinned, helper.count)
		}
	}
}
