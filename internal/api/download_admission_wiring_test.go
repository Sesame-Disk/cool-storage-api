package api

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
)

func TestServerInitializesAndWiresDownloadAdmissionCoordinator(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.DownloadAdmission = config.DownloadAdmissionConfig{
		Enabled:                true,
		MaxActivePerNode:       8,
		MaxActivePerAuthUser:   2,
		MaxActivePerLinkSource: 4,
		MaxActivePerClientLink: 2,
		MaxWaitersPerIdentity:  4,
		MaxWaitersPerNode:      8,
		AdmissionWait:          time.Second,
		PreparationDeadline:    time.Minute,
		IdleWriteTimeout:       time.Minute,
		RetryAfter:             2 * time.Second,
	}
	s := &Server{config: cfg}
	s.initializeDownloadAdmissionCoordinator()

	if s.downloadAdmission == nil {
		t.Fatal("server download admission coordinator = nil")
	}
	if got := s.downloadAdmission.RetryAfterSeconds(); got != 2 {
		t.Fatalf("coordinator retry-after = %d, want 2", got)
	}

	seafHTTPHandler := NewSeafHTTPHandler(nil, nil, nil, nil, cfg, nil)
	seafHTTPHandler.SetDownloadAdmissionCoordinator(s.downloadAdmission)
	if seafHTTPHandler.downloadAdmission != s.downloadAdmission {
		t.Fatal("SeafHTTP handler received a different download admission coordinator")
	}

	syncHandler := NewSyncHandler(nil, nil, nil, cfg, nil)
	syncHandler.SetDownloadAdmissionCoordinator(s.downloadAdmission)
	if syncHandler.downloadAdmission != s.downloadAdmission {
		t.Fatal("Sync handler received a different download admission coordinator")
	}
}

// TestRegisterCompatibilityRoutesWiresSyncDownloadAdmission pins the production
// call site, not the setter. The test above only proves that assigning the
// pointer works: deleting the call from registerCompatibilityRoutes would leave
// it green while every block GET silently ran with a nil coordinator — which,
// with download_admission disabled, is also silently harmless today and would
// only surface when D6 turns it on.
func TestRegisterCompatibilityRoutesWiresSyncDownloadAdmission(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to resolve current file path")
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filepath.Join(filepath.Dir(thisFile), "server_routes.go"), nil, 0)
	if err != nil {
		t.Fatalf("parse server_routes.go: %v", err)
	}

	fn := syncFindFunction(t, file, "registerCompatibilityRoutes")

	// Each producer is asserted by receiver. Checking only that the setter is
	// called somewhere in this function is not enough — three handlers are wired
	// here, so dropping any one of them would still leave the other two matching.
	for _, handler := range []string{"fileViewHandler", "seafHTTPHandler", "syncHandler"} {
		// The argument matters as much as the call: handing a producer a freshly
		// constructed coordinator would multiply the process-local node cap, which
		// the D0 contract calls out as making the global metrics meaningless.
		if !sharesServerDownloadAdmission(fn, handler) {
			t.Fatalf("%s must receive s.downloadAdmission in registerCompatibilityRoutes", handler)
		}
	}
}

// sharesServerDownloadAdmission reports whether the named receiver is handed the
// server's own coordinator via SetDownloadAdmissionCoordinator.
func sharesServerDownloadAdmission(fn *ast.FuncDecl, receiver string) bool {
	found := false
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		fun, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || fun.Sel.Name != "SetDownloadAdmissionCoordinator" || len(call.Args) != 1 {
			return true
		}
		if ident, ok := fun.X.(*ast.Ident); !ok || ident.Name != receiver {
			return true
		}
		arg, ok := call.Args[0].(*ast.SelectorExpr)
		if !ok || arg.Sel.Name != "downloadAdmission" {
			return true
		}
		if ident, ok := arg.X.(*ast.Ident); ok && ident.Name == "s" {
			found = true
		}
		return true
	})
	return found
}
