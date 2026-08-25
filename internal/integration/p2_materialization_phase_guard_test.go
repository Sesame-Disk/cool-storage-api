package integration

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"
	"sort"
	"testing"
)

// P2/R24 rests on one structural invariant that lives OUTSIDE the helpers the
// existing guards pin: only the initial store phase may mint a physical
// incarnation, and that holds only while every upload funnel FORWARDS the phase
// its retry driver handed it. A funnel that passes the BlockMaterializationInitial
// constant instead still compiles, keeps every unit and integration test green,
// and silently restores the post-INSTALL remint this branch closed -- confirmed
// by mutating internal/api/sync.go and watching the whole suite stay green.
//
// TestP2MaterializationPhaseIsForwardedByFunnels closes that hole the way the
// FreshInstall authority guard closes its own: with go/types, so an alias, a
// package-level function variable, or a renamed constant cannot evade it.
//
// The rule: at every production call of a phase-carrying materialization helper,
// the phase argument must be an identifier bound to a BlockMaterializationPhase
// PARAMETER of an enclosing function or closure. A constant is a violation --
// with one deliberate exception, the phase-free compatibility wrapper of the same
// callee (ResolveNeedsPutBlockStore -> ResolveNeedsPutBlockStoreForPhase), whose
// entire job is to supply the initial phase.

// p2PhaseCarryingFuncs are the helpers whose last parameter is the phase.
var p2PhaseCarryingFuncs = map[string]bool{
	"ResolveNeedsPutBlockStoreForPhase":  true,
	"StoreUploadedBlockForProbeForPhase": true,
	"EnsureReusableBlockPresentForPhase": true,
}

// p2PhaseErasingFuncs discard the caller's phase and substitute the initial one.
// They remain for tests that exercise phase-agnostic retry behavior; production
// code must not reach them, or R24's "only Initial may mint" collapses back into
// "every observation may mint".
var p2PhaseErasingFuncs = map[string]bool{
	"ResolveNeedsPutBlockStore":                   true,
	"StoreUploadedBlockForProbe":                  true,
	"EnsureReusableBlockPresent":                  true,
	"RetryUploadedBlockMaterialization":           true,
	"RetryUploadedBlockMaterializationContext":    true,
	"retrySeafHTTPBlockMaterializationContext":    true,
	"retryCreateFileTemplateBlockMaterialization": true,
}

type p2PhaseSite struct {
	file     string
	function string
	callee   string
}

// p2WantPhaseForwarding pins the production sites that must forward a phase.
// Rejecting constants alone is not enough: a funnel could also DROP its
// phase-aware call and go back to the phase-free helper, which would leave
// nothing for the constant rule to inspect. Pinning the inventory makes both
// directions -- a constant argument and a vanished call site -- fail here, and
// keeps the guard from passing vacuously if the loader ever stops resolving
// these packages.
var p2WantPhaseForwarding = map[p2PhaseSite]int{
	{file: "blocks.go", function: "UploadBlock", callee: "StoreUploadedBlockForProbeForPhase"}: 1,

	{file: "files.go", function: "CreateFile", callee: "EnsureReusableBlockPresentForPhase"}: 1,
	{file: "files.go", function: "CreateFile", callee: "ResolveNeedsPutBlockStoreForPhase"}:  1,
	{file: "files.go", function: "UploadFile", callee: "EnsureReusableBlockPresentForPhase"}: 1,
	{file: "files.go", function: "UploadFile", callee: "ResolveNeedsPutBlockStoreForPhase"}:  1,

	{file: "onlyoffice.go", function: "saveEditedDocument", callee: "EnsureReusableBlockPresentForPhase"}: 1,
	{file: "onlyoffice.go", function: "saveEditedDocument", callee: "ResolveNeedsPutBlockStoreForPhase"}:  1,

	{file: "seafhttp.go", function: "HandleUpload", callee: "EnsureReusableBlockPresentForPhase"}:            1,
	{file: "seafhttp.go", function: "HandleUpload", callee: "ResolveNeedsPutBlockStoreForPhase"}:             1,
	{file: "seafhttp.go", function: "finalizeUploadStreaming", callee: "EnsureReusableBlockPresentForPhase"}: 1,
	{file: "seafhttp.go", function: "finalizeUploadStreaming", callee: "ResolveNeedsPutBlockStoreForPhase"}:  1,

	{file: "sync.go", function: "PutBlock", callee: "EnsureReusableBlockPresentForPhase"}: 1,
	{file: "sync.go", function: "PutBlock", callee: "ResolveNeedsPutBlockStoreForPhase"}:  1,

	{file: "upload_reuse.go", function: "EnsureReusableBlockPresentForPhase", callee: "StoreUploadedBlockForProbeForPhase"}: 1,
	{file: "upload_reuse.go", function: "StoreUploadedBlockForProbeForPhase", callee: "ResolveNeedsPutBlockStoreForPhase"}:  1,
}

type p2PhaseFinding struct {
	site   p2PhaseSite
	detail string
	pos    token.Position
}

func TestP2MaterializationPhaseIsForwardedByFunnels(t *testing.T) {
	program := p2LoadProductionProgram(t)

	forwarded := map[p2PhaseSite]int{}
	var violations, erasures []p2PhaseFinding

	paths := make([]string, 0, len(program.packages))
	for path := range program.packages {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	for _, path := range paths {
		source := program.packages[path]
		program.check(source)
		info := source.info
		if info == nil {
			continue
		}

		// Package-level "var f = v2.SomeFunc" aliases: a call through the variable
		// is a call to the function it was bound to.
		aliases := p2FunctionAliases(source, info)

		for _, file := range source.files {
			fileName := filepath.Base(source.paths[file])

			// Any production mention of a phase-erasing helper is a finding on its
			// own, whether it is called directly or bound to a function variable.
			ast.Inspect(file, func(node ast.Node) bool {
				identifier, ok := node.(*ast.Ident)
				if !ok {
					return true
				}
				function, ok := info.Uses[identifier].(*types.Func)
				if !ok || !p2PhaseErasingFuncs[function.Name()] {
					return true
				}
				erasures = append(erasures, p2PhaseFinding{
					site:   p2PhaseSite{file: fileName, callee: function.Name()},
					detail: "production code references a phase-erasing materialization helper",
					pos:    source.fset.Position(identifier.Pos()),
				})
				return true
			})

			for _, declaration := range file.Decls {
				function, ok := declaration.(*ast.FuncDecl)
				if !ok || function.Body == nil {
					continue
				}
				p2WalkPhaseCalls(source, info, aliases, fileName, function, forwarded, &violations)
			}
		}
	}

	for _, finding := range violations {
		t.Errorf("%s: %s -> %s: %s", finding.pos, finding.site.function, finding.site.callee, finding.detail)
	}
	for _, finding := range erasures {
		t.Errorf("%s: %s: %s", finding.pos, finding.site.callee, finding.detail)
	}

	sites := make([]p2PhaseSite, 0, len(forwarded)+len(p2WantPhaseForwarding))
	seen := map[p2PhaseSite]bool{}
	for site := range forwarded {
		sites, seen[site] = append(sites, site), true
	}
	for site := range p2WantPhaseForwarding {
		if !seen[site] {
			sites = append(sites, site)
		}
	}
	sort.Slice(sites, func(i, j int) bool {
		return fmt.Sprint(sites[i]) < fmt.Sprint(sites[j])
	})
	for _, site := range sites {
		got, want := forwarded[site], p2WantPhaseForwarding[site]
		if got != want {
			t.Errorf("%s %s -> %s: forwarded phase %d time(s), want %d; update p2WantPhaseForwarding only when the funnel inventory itself is meant to change",
				site.file, site.function, site.callee, got, want)
			continue
		}
		t.Logf("P2_PHASE_FORWARDED %s %s -> %s x%d", site.file, site.function, site.callee, got)
	}
}

// p2FunctionAliases maps package-level function variables to the function they
// were initialized with, so syncResolveNeedsPutBlockStoreFn(...) is recognized as
// a call to ResolveNeedsPutBlockStoreForPhase.
func p2FunctionAliases(source *p2PackageSource, info *types.Info) map[types.Object]*types.Func {
	aliases := map[types.Object]*types.Func{}
	for _, file := range source.files {
		for _, declaration := range file.Decls {
			generic, ok := declaration.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range generic.Specs {
				value, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, name := range value.Names {
					if i >= len(value.Values) {
						continue
					}
					target, ok := p2ResolveFuncExpr(value.Values[i], info)
					if !ok {
						continue
					}
					if declared := info.Defs[name]; declared != nil {
						aliases[declared] = target
					}
				}
			}
		}
	}
	return aliases
}

// p2ResolveFuncExpr resolves an expression that names a function (bare or
// package-qualified) to its type object.
func p2ResolveFuncExpr(expression ast.Expr, info *types.Info) (*types.Func, bool) {
	for {
		switch typed := expression.(type) {
		case *ast.ParenExpr:
			expression = typed.X
		case *ast.Ident:
			function, ok := info.Uses[typed].(*types.Func)
			return function, ok
		case *ast.SelectorExpr:
			function, ok := info.Uses[typed.Sel].(*types.Func)
			return function, ok
		default:
			return nil, false
		}
	}
}

// p2WalkPhaseCalls descends one top-level function, carrying the parameter sets
// of every enclosing function and closure so a phase argument can be proven to be
// a forwarded parameter rather than a constant.
func p2WalkPhaseCalls(source *p2PackageSource, info *types.Info, aliases map[types.Object]*types.Func, fileName string, declaration *ast.FuncDecl, forwarded map[p2PhaseSite]int, violations *[]p2PhaseFinding) {
	enclosing := declaration.Name.Name

	var walk func(node ast.Node, params []map[types.Object]bool)
	walk = func(node ast.Node, params []map[types.Object]bool) {
		ast.Inspect(node, func(current ast.Node) bool {
			switch typed := current.(type) {
			case *ast.FuncLit:
				walk(typed.Body, append(params[:len(params):len(params)], p2ParamObjects(typed.Type, info)))
				return false
			case *ast.CallExpr:
				p2CheckPhaseCall(source, info, aliases, fileName, enclosing, typed, params, forwarded, violations)
				return true
			}
			return true
		})
	}
	walk(declaration.Body, []map[types.Object]bool{p2ParamObjects(declaration.Type, info)})
}

func p2ParamObjects(signature *ast.FuncType, info *types.Info) map[types.Object]bool {
	objects := map[types.Object]bool{}
	if signature == nil || signature.Params == nil {
		return objects
	}
	for _, field := range signature.Params.List {
		for _, name := range field.Names {
			if declared := info.Defs[name]; declared != nil {
				objects[declared] = true
			}
		}
	}
	return objects
}

func p2CheckPhaseCall(source *p2PackageSource, info *types.Info, aliases map[types.Object]*types.Func, fileName, enclosing string, call *ast.CallExpr, params []map[types.Object]bool, forwarded map[p2PhaseSite]int, violations *[]p2PhaseFinding) {
	callee, ok := p2CalleeFunc(call.Fun, info, aliases)
	if !ok || !p2PhaseCarryingFuncs[callee.Name()] {
		return
	}
	site := p2PhaseSite{file: fileName, function: enclosing, callee: callee.Name()}
	position := source.fset.Position(call.Lparen)

	if len(call.Args) == 0 {
		*violations = append(*violations, p2PhaseFinding{site: site, detail: "call has no arguments", pos: position})
		return
	}

	// The phase-free compatibility wrapper of this exact callee is the one place
	// allowed to name the constant; supplying the initial phase is its purpose.
	if enclosing+"ForPhase" == callee.Name() {
		return
	}

	detail, ok := p2PhaseArgumentIsForwardedParam(call.Args[len(call.Args)-1], params, info)
	if !ok {
		*violations = append(*violations, p2PhaseFinding{site: site, detail: detail, pos: position})
		return
	}
	forwarded[site]++
}

func p2CalleeFunc(expression ast.Expr, info *types.Info, aliases map[types.Object]*types.Func) (*types.Func, bool) {
	for {
		switch typed := expression.(type) {
		case *ast.ParenExpr:
			expression = typed.X
		case *ast.Ident:
			return p2ObjectFunc(info.Uses[typed], aliases)
		case *ast.SelectorExpr:
			return p2ObjectFunc(info.Uses[typed.Sel], aliases)
		default:
			return nil, false
		}
	}
}

func p2ObjectFunc(object types.Object, aliases map[types.Object]*types.Func) (*types.Func, bool) {
	switch typed := object.(type) {
	case *types.Func:
		return typed, true
	case *types.Var:
		aliased, ok := aliases[typed]
		return aliased, ok
	}
	return nil, false
}

// p2PhaseArgumentIsForwardedParam proves the phase argument is the phase the
// caller was given: an identifier bound to a BlockMaterializationPhase parameter
// of an enclosing function or closure. A constant -- BlockMaterializationInitial,
// a rename of it, or any other -- resolves to *types.Const and is rejected.
func p2PhaseArgumentIsForwardedParam(argument ast.Expr, params []map[types.Object]bool, info *types.Info) (string, bool) {
	for {
		paren, ok := argument.(*ast.ParenExpr)
		if !ok {
			break
		}
		argument = paren.X
	}
	identifier, ok := argument.(*ast.Ident)
	if !ok {
		return fmt.Sprintf("phase argument is %T, want an identifier naming a forwarded phase parameter", argument), false
	}
	object := info.Uses[identifier]
	if object == nil {
		return fmt.Sprintf("phase argument %q could not be resolved", identifier.Name), false
	}
	variable, ok := object.(*types.Var)
	if !ok {
		return fmt.Sprintf("phase argument %q is a %T; a constant phase is exactly the regression this guard exists to catch, want a forwarded phase parameter", identifier.Name, object), false
	}
	named, ok := variable.Type().(*types.Named)
	if !ok || named.Obj().Name() != "BlockMaterializationPhase" {
		return fmt.Sprintf("phase argument %q has type %s, want BlockMaterializationPhase", identifier.Name, variable.Type()), false
	}
	for _, set := range params {
		if set[variable] {
			return "", true
		}
	}
	return fmt.Sprintf("phase argument %q is not a parameter of any enclosing function or closure", identifier.Name), false
}
