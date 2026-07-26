package v2

import (
	"encoding/json"
	"errors"
	goast "go/ast"
	goparser "go/parser"
	gotoken "go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Sesame-Disk/sesamefs/internal/config"
)

// The share-link password gate is an authorization control, so these tests assert
// on the response *body*, not the status code. The defect this file pins
// (ISSUE-SHARELINK-PASSWORD-BYPASS-01) shipped a 200 whose `needPassword` was
// true and whose `fileContent` held the protected bytes anyway: the frontend
// short-circuits to the password dialog, so nothing looked wrong in a browser
// while `curl` walked away with the file. A status assertion cannot see that.

const gateHMACKey = "gate-test-hmac-key"

// countingTokenCreator records whether a download credential was ever minted.
// The OnlyOffice half of the bypass is not a content leak — it is a real
// `CreateLinkDownloadToken` handed to an anonymous caller — so "was the token
// created" is the only assertion that catches it.
type countingTokenCreator struct{ linkDownloadCalls int }

func (m *countingTokenCreator) CreateUploadToken(string, string, string, string) (string, error) {
	return "upload-token", nil
}
func (m *countingTokenCreator) CreateUpdateToken(string, string, string, string) (string, error) {
	return "update-token", nil
}
func (m *countingTokenCreator) CreateDownloadToken(string, string, string, string) (string, error) {
	return "download-token", nil
}
func (m *countingTokenCreator) CreateLinkUploadToken(string, string, string, string) (string, error) {
	return "link-upload-token", nil
}
func (m *countingTokenCreator) CreateLinkDownloadToken(string, string, string, string) (string, error) {
	m.linkDownloadCalls++
	return "link-download-token", nil
}

func newGateHandler(t *testing.T, onlyOfficeEnabled bool) (*ShareLinkViewHandler, *countingTokenCreator) {
	t.Helper()
	tc := &countingTokenCreator{}
	cfg := &config.Config{}
	cfg.Auth.ShareLinkHMACKey = gateHMACKey
	cfg.OnlyOffice.Enabled = onlyOfficeEnabled
	cfg.OnlyOffice.JWTSecret = "onlyoffice-jwt-secret"
	cfg.OnlyOffice.APIJSURL = "https://oo.example/web-apps/apps/api/documents/api.js"
	return &ShareLinkViewHandler{config: cfg, tokenCreator: tc, serverURL: "https://files.example"}, tc
}

func gateShareLink(path, passwordHash string) *shareLinkData {
	return &shareLinkData{
		orgID:        "org-1",
		libraryID:    "repo-1",
		token:        "sharetoken1234",
		filePath:     path,
		passwordHash: passwordHash,
		canDownload:  true,
		targetEntry: &FSEntry{
			ID:   "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Name: "secret",
			Size: 42,
			Mode: ModeFile,
		},
	}
}

// emitGate drives the production chain (builder -> emitter -> HTTP) and returns
// the decoded page options. withCookie mints the real HMAC cookie the same way
// CheckPublicLinkPassword does, so the "verified" cases exercise the true
// comparison rather than a stubbed bypass.
func emitGate(t *testing.T, h *ShareLinkViewHandler, sl *shareLinkData, withCookie bool) (int, map[string]any, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v2.1/share-links/"+sl.token+"/bootstrap", nil)
	if withCookie {
		name, value := buildPublicLinkPasswordCookie("share", sl.token, sl.passwordHash, gateHMACKey)
		c.Request.AddCookie(&http.Cookie{Name: name, Value: value})
	}

	h.emitShareFileBootstrap(c, sl)

	var decoded struct {
		Bundle      string         `json:"bundle"`
		PageOptions map[string]any `json:"page_options"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode bootstrap body: %v (body=%s)", err, w.Body.String())
	}
	return w.Code, decoded.PageOptions, w.Body.String()
}

func TestShareFileBootstrapWithholdsInlineContentWithoutPassword(t *testing.T) {
	const secret = "TOP-SECRET-MARKDOWN-BODY"

	original := shareInlineTextFn
	t.Cleanup(func() { shareInlineTextFn = original })
	readCalls := 0
	shareInlineTextFn = func(*ShareLinkViewHandler, *shareLinkData) (string, error) {
		readCalls++
		return secret, nil
	}

	h, _ := newGateHandler(t, false)
	status, opts, raw := emitGate(t, h, gateShareLink("/notes.md", "bcrypt-hash"), false)

	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (the password page is a 200 with no content, not an error)", status)
	}
	if got, _ := opts["fileContent"].(string); got != "" {
		t.Fatalf("fileContent = %q, want empty for an unverified password link", got)
	}
	if strings.Contains(raw, secret) {
		t.Fatalf("response body leaked the protected content: %s", raw)
	}
	if needPassword, _ := opts["needPassword"].(bool); !needPassword {
		t.Fatal("needPassword must stay true so the SPA renders the password dialog")
	}
	if noPassword, _ := opts["noPassword"].(bool); noPassword {
		t.Fatal("noPassword must be false while the password is unverified")
	}
	// Reading the file costs a Cassandra lookup, an S3 fetch and a decrypt. An
	// anonymous caller must not be able to drive that work on every request.
	if readCalls != 0 {
		t.Fatalf("inline text reader called %d time(s) for an unverified link; the read must be skipped, not just discarded", readCalls)
	}
}

func TestShareFileBootstrapWithholdsOnlyOfficeTokenWithoutPassword(t *testing.T) {
	h, tc := newGateHandler(t, true)
	status, opts, raw := emitGate(t, h, gateShareLink("/quarterly.docx", "bcrypt-hash"), false)

	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if tc.linkDownloadCalls != 0 {
		t.Fatalf("CreateLinkDownloadToken called %d time(s) for an unverified link; an anonymous caller must never be handed a download credential", tc.linkDownloadCalls)
	}
	if _, present := opts["onlyOfficeConfig"]; present {
		t.Fatal("onlyOfficeConfig must be absent for an unverified password link")
	}
	if strings.Contains(raw, "link-download-token") {
		t.Fatalf("response body leaked a download token: %s", raw)
	}
	if needPassword, _ := opts["needPassword"].(bool); !needPassword {
		t.Fatal("needPassword must stay true for the OnlyOffice branch too")
	}
}

func TestShareFileBootstrapServesContentOncePasswordIsVerified(t *testing.T) {
	const secret = "TOP-SECRET-MARKDOWN-BODY"

	original := shareInlineTextFn
	t.Cleanup(func() { shareInlineTextFn = original })
	shareInlineTextFn = func(*ShareLinkViewHandler, *shareLinkData) (string, error) {
		return secret, nil
	}

	h, _ := newGateHandler(t, false)
	status, opts, _ := emitGate(t, h, gateShareLink("/notes.md", "bcrypt-hash"), true)

	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if got, _ := opts["fileContent"].(string); got != secret {
		t.Fatalf("fileContent = %q, want the real content once the password cookie is valid", got)
	}
	if needPassword, _ := opts["needPassword"].(bool); needPassword {
		t.Fatal("needPassword must be false once the cookie verifies")
	}
}

func TestShareFileBootstrapServesOnlyOfficeOncePasswordIsVerified(t *testing.T) {
	h, tc := newGateHandler(t, true)
	status, opts, _ := emitGate(t, h, gateShareLink("/quarterly.docx", "bcrypt-hash"), true)

	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if tc.linkDownloadCalls != 1 {
		t.Fatalf("CreateLinkDownloadToken calls = %d, want 1 once the password cookie verifies", tc.linkDownloadCalls)
	}
	if _, present := opts["onlyOfficeConfig"]; !present {
		t.Fatal("onlyOfficeConfig must be produced once the password cookie verifies")
	}
	if needPassword, _ := opts["needPassword"].(bool); needPassword {
		// OnlyOffice bootstrap does not set needPassword; if it ever appears, a
		// verified cookie must not leave it true.
		t.Fatal("needPassword must not be true after a verified OnlyOffice bootstrap")
	}
}

func TestBuildOnlyOfficeShareBootstrapFailsClosedWithoutPassword(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h, tc := newGateHandler(t, true)
	sl := gateShareLink("/quarterly.docx", "bcrypt-hash")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/d/"+sl.token, nil)

	_, err := h.buildOnlyOfficeShareBootstrap(c, sl, "quarterly.docx", "docx", 42)
	if !errors.Is(err, errShareLinkPasswordRequired) {
		t.Fatalf("err = %v, want errShareLinkPasswordRequired", err)
	}
	if tc.linkDownloadCalls != 0 {
		t.Fatalf("CreateLinkDownloadToken called %d time(s); the helper must fail before minting", tc.linkDownloadCalls)
	}
}

func TestShareFileBootstrapServesContentWhenLinkHasNoPassword(t *testing.T) {
	const secret = "PUBLIC-MARKDOWN-BODY"

	original := shareInlineTextFn
	t.Cleanup(func() { shareInlineTextFn = original })
	shareInlineTextFn = func(*ShareLinkViewHandler, *shareLinkData) (string, error) {
		return secret, nil
	}

	h, tc := newGateHandler(t, true)

	// Plain text link: content flows as before.
	_, opts, _ := emitGate(t, h, gateShareLink("/readme.md", ""), false)
	if got, _ := opts["fileContent"].(string); got != secret {
		t.Fatalf("fileContent = %q, want the content for an unprotected link", got)
	}

	// OnlyOffice link: the token is still minted when no password guards the link.
	_, opts, _ = emitGate(t, h, gateShareLink("/plan.docx", ""), false)
	if tc.linkDownloadCalls != 1 {
		t.Fatalf("CreateLinkDownloadToken calls = %d, want 1 for an unprotected OnlyOffice link", tc.linkDownloadCalls)
	}
	if _, present := opts["onlyOfficeConfig"]; !present {
		t.Fatal("onlyOfficeConfig must still be produced for an unprotected link")
	}
}

// The gate protects both public endpoints only because both reach it through the
// one emitter. The sibling AST test in sharelink_bootstrap_error_test.go pins the
// *error* path; this pins the success path, which is the one that carries the
// content. Without it, a refactor could give one endpoint its own builder call and
// every test above would stay green while half the surface reopened.
//
// Scope, stated honestly so the guarantee is not read wider than it is: this walks
// every non-test .go file in package v2, not just sharelink_view.go — an earlier
// revision parsed the one file, which would have missed a new minting call site
// added anywhere else in the package. It is still a *syntactic* check. It does not
// execute the handlers, and it cannot see a call reached through a function value,
// an interface, or another package. It is a cheap tripwire on the call graph, not
// a proof; the behavioural guarantees come from the unit tests above and, at
// runtime, from internal/integration —
// TestShareLinkBootstrapPasswordGateOnBothEndpoints for the inline-content half
// across both endpoints, and
// TestShareLinkBootstrapWithholdsOnlyOfficeCredentialWithoutPassword for the
// credential half, which needs a .docx fixture because .md never reaches that
// branch.
func TestBothShareBootstrapEndpointsGoThroughTheGatedEmitter(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to resolve current file path")
	}
	pkgDir := filepath.Dir(thisFile)

	fset := gotoken.NewFileSet()
	pkgs, err := goparser.ParseDir(fset, pkgDir, func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse package dir %s: %v", pkgDir, err)
	}
	if len(pkgs) == 0 {
		t.Fatalf("no non-test packages parsed from %s", pkgDir)
	}

	wantEmitter := map[string]bool{
		"GetShareLinkBootstrap":     false,
		"GetShareLinkFileBootstrap": false,
	}
	// Nothing outside the emitter may call the builder directly, or it would skip
	// nothing today but could skip the gate after any future edit to the emitter.
	directBuilderCalls := map[string]int{}
	// The OnlyOffice helper mints credentials; only the gated builder may call it.
	onlyOfficeCallers := map[string]int{}
	filesWalked := 0

	for _, pkg := range pkgs {
		for _, fileNode := range pkg.Files {
			filesWalked++
			goast.Inspect(fileNode, func(node goast.Node) bool {
				fn, ok := node.(*goast.FuncDecl)
				if !ok {
					return true
				}
				goast.Inspect(fn, func(inner goast.Node) bool {
					call, ok := inner.(*goast.CallExpr)
					if !ok {
						return true
					}
					sel, ok := call.Fun.(*goast.SelectorExpr)
					if !ok {
						return true
					}
					switch sel.Sel.Name {
					case "emitShareFileBootstrap":
						if _, tracked := wantEmitter[fn.Name.Name]; tracked {
							wantEmitter[fn.Name.Name] = true
						}
					case "buildShareFileBootstrapResponse":
						// shareFileBootstrapFn is the single indirection the emitter
						// uses and the seam tests substitute; anything else bypasses.
						if fn.Name.Name != "emitShareFileBootstrap" {
							directBuilderCalls[fn.Name.Name]++
						}
					case "buildOnlyOfficeShareBootstrap":
						onlyOfficeCallers[fn.Name.Name]++
					}
					return true
				})
				return true
			})
		}
	}

	// Guard the guard: if the walk silently stopped finding files, every assertion
	// below would pass vacuously.
	if filesWalked < 2 {
		t.Fatalf("walked %d file(s); the package scan is not running", filesWalked)
	}
	for handler, found := range wantEmitter {
		if !found {
			t.Fatalf("%s does not call emitShareFileBootstrap; it would bypass the password gate", handler)
		}
	}
	for caller, n := range directBuilderCalls {
		t.Fatalf("%s calls buildShareFileBootstrapResponse directly %d time(s), outside the emitter", caller, n)
	}
	if n := onlyOfficeCallers["buildShareFileBootstrapResponse"]; n != 1 {
		t.Fatalf("buildShareFileBootstrapResponse calls buildOnlyOfficeShareBootstrap %d time(s), want 1", n)
	}
	for caller, n := range onlyOfficeCallers {
		if caller != "buildShareFileBootstrapResponse" {
			t.Fatalf("%s calls buildOnlyOfficeShareBootstrap %d time(s); only the gated builder may mint the download token", caller, n)
		}
	}
}

// The gate lives at the point of emission, not only in the caller that happens to
// exist today. A future caller that assembles content itself and hands it to the
// bundle builder must still not be able to publish it past an unverified password.
func TestBuildSharedFileBundleBootstrapNeverEmitsContentWithoutPassword(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h, _ := newGateHandler(t, false)
	sl := gateShareLink("/notes.md", "bcrypt-hash")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/d/"+sl.token, nil)

	bootstrap := h.buildSharedFileBundleBootstrap(
		c, sl, "sharedFileViewMarkdown", "/raw", "notes.md", "md", 42,
		"CONTENT-A-FUTURE-CALLER-PASSED-IN",
		map[string]sharedMarkdownSmartLinkTarget{"x": {}},
	)

	opts, ok := bootstrap.PageOptions.(map[string]any)
	if !ok {
		t.Fatalf("page options type = %T, want map", bootstrap.PageOptions)
	}
	if got, _ := opts["fileContent"].(string); got != "" {
		t.Fatalf("fileContent = %q; the bundle builder must drop content it was given when the password is unverified", got)
	}
	// Compared by length, not against nil: the value is a typed map inside an
	// `any`, so a nil map is not `== nil` here even though it serializes to JSON
	// null. Length is what actually distinguishes "dropped" from "leaked".
	if got, _ := opts["smartLinkMap"].(map[string]sharedMarkdownSmartLinkTarget); len(got) != 0 {
		t.Fatalf("smartLinkMap has %d entr(ies); it is derived from the protected content and must be dropped with it", len(got))
	}
}
