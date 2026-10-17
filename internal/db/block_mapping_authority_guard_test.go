package db

import (
	"context"
	"errors"
	"go/ast"
	"go/build/constraint"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

const blockMappingAuthorityClaimsTable = "block_mapping_authority_claims"

var (
	blockMappingAuthorityInsertPattern = regexp.MustCompile(`(?is)^INSERT\s+INTO\s+block_mapping_authority_claims\s*\([^)]*\)\s*VALUES\s*\([^)]*\)\s*IF\s+NOT\s+EXISTS$`)
	blockMappingAuthoritySelectPattern = regexp.MustCompile(`(?is)^SELECT\s+.+\s+FROM\s+block_mapping_authority_claims\s+WHERE\s+.+$`)
	blockMappingAuthorityCreatePattern = regexp.MustCompile(`(?is)^CREATE\s+TABLE\s+IF\s+NOT\s+EXISTS\s+block_mapping_authority_claims\b`)
	blockMappingTableMentionPattern    = regexp.MustCompile(`(?i)\bblock_id_mappings\b`)
	blockMappingSelectPattern          = regexp.MustCompile(`(?is)^SELECT\s+.+\s+FROM\s+block_id_mappings\b`)
	blockMappingInsertPattern          = regexp.MustCompile(`(?is)^INSERT\s+INTO\s+block_id_mappings\s*\(org_id,\s*representation_id,\s*external_id,\s*internal_id,\s*created_at\)\s*VALUES\s*\(\?,\s*\?,\s*\?,\s*\?,\s*\?\)$`)
	blockMappingFreezePattern          = regexp.MustCompile(`(?is)^UPDATE\s+block_id_mappings\s+USING\s+TIMESTAMP\s+\?\s+SET\s+internal_id\s*=\s*\?\s+WHERE\s+org_id\s*=\s*\?\s+AND\s+representation_id\s*=\s*\?\s+AND\s+external_id\s*=\s*\?$`)
	blockMappingCQLConditionalPattern  = regexp.MustCompile(`(?i)\bIF\b`)
)

// Symbols that acquire or write mapping authority with explicit source-file
// allowlists. The freeze primitive stays confined to its defining file;
// acquisition primitives and integration hooks are listed separately below.
var blockMappingAuthorityAcquisitionSymbols = map[string]map[string]bool{
	"freezeBlockMappingProjection": {
		"internal/db/block_mapping_authority.go": true,
	},
	"claimBlockMappingAuthority": {
		"internal/db/block_mapping_authority.go":             true,
		"internal/db/block_mapping_authority_integration.go": true,
	},
	"acquireBlockMappingClaim": {
		"internal/db/block_mapping_authority.go": true,
	},
	"promoteBlockMappingAuthority": {
		"internal/db/block_mapping_authority.go":             true,
		"internal/db/block_mapping_authority_integration.go": true,
	},
	"ReadBlockMappingAuthority": {
		"internal/db/block_mapping_authority.go": true,
	},
	"readBlockMappingAuthority": {
		"internal/db/block_mapping_authority.go":             true,
		"internal/db/block_mapping_authority_integration.go": true,
	},
	"blockMappingPromotionPorts": {
		"internal/db/block_mapping_authority.go": true,
	},
	"PromoteBlockMappingAuthority": {
		"internal/db/block_mapping_authority.go":      true,
		"internal/db/library_continuity_certifier.go": true,
	},
}

type blockMappingResolvedString struct {
	text      string
	constant  bool
	ambiguous bool
}

func blockMappingHasAmbiguousStringControlFlow(function *ast.FuncDecl) bool {
	if function == nil || function.Body == nil {
		return true
	}
	returns := 0
	ambiguous := false
	ast.Inspect(function.Body, func(node ast.Node) bool {
		if _, nestedFunction := node.(*ast.FuncLit); nestedFunction {
			return false
		}
		switch node.(type) {
		case *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt, *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt,
			*ast.BranchStmt, *ast.LabeledStmt, *ast.GoStmt, *ast.DeferStmt:
			ambiguous = true
		case *ast.ReturnStmt:
			returns++
		}
		return true
	})
	return ambiguous || returns != 1
}

// blockMappingNestedClosureWrites returns captured locals assigned by nested
// function literals. The CQL resolver cannot prove whether a closure runs, so
// it must not treat such a value as an immutable string.
func blockMappingNestedClosureWrites(body ast.Node, locals map[string]ast.Expr, before token.Pos) map[string]bool {
	writes := map[string]bool{}
	if body == nil || len(locals) == 0 {
		return writes
	}
	inspectAssignments := func(root ast.Node) {
		ast.Inspect(root, func(node ast.Node) bool {
			if node == nil || (before.IsValid() && node.Pos() >= before) {
				return false
			}
			switch value := node.(type) {
			case *ast.AssignStmt:
				if value.Tok == token.DEFINE {
					return true
				}
				for _, left := range value.Lhs {
					if name, ok := left.(*ast.Ident); ok {
						if _, tracked := locals[name.Name]; tracked {
							writes[name.Name] = true
						}
					}
				}
			case *ast.IncDecStmt:
				if name, ok := value.X.(*ast.Ident); ok {
					if _, tracked := locals[name.Name]; tracked {
						writes[name.Name] = true
					}
				}
			}
			return true
		})
	}
	ast.Inspect(body, func(node ast.Node) bool {
		literal, ok := node.(*ast.FuncLit)
		if !ok {
			return true
		}
		inspectAssignments(literal.Body)
		return false
	})
	return writes
}

// blockMappingSourceStrings follows string values used as Query arguments,
// including local bindings, constant concatenation, strings.Join and local
// helper returns. It deliberately does not try to execute Go code: unknown
// pieces keep the known fragments so a split table name still taints the
// statement and fails closed.
type blockMappingSourceStrings struct {
	functions               map[string]*ast.FuncDecl
	methods                 map[string][]*ast.FuncDecl
	functionPaths           map[string]string
	batchReturningFunctions map[string]bool
	globals                 map[string]ast.Expr
	mutableGlobals          map[string]bool
	dynamicValues           map[ast.Expr]bool
}

// These production CQL builders use dynamic fragments but have fixed table
// identity in their own source. Keep their exact call-site inventory explicit;
// any new unresolved Query must be reviewed and classified before the guard can
// pass. The hard-delete lock builders are additionally pinned to their three
// known table/partition-key pairs below.
type blockMappingDynamicQueryContract struct {
	count       int
	tableMarker string
}

var blockMappingDynamicQueryAllowlist = map[string]map[string]blockMappingDynamicQueryContract{
	"internal/api/v2/admin.go": {
		"UpdateOrganization": {count: 1, tableMarker: "UPDATE organizations SET"}, // validated update columns.
	},
	"internal/api/v2/admin_link_helpers.go": {
		"listAdminLinkProjectionCursorPage":          {count: 1, tableMarker: "FROM admin_links_by_created"},
		"listAdminLinkProjectionCursorPageByOrg":     {count: 1, tableMarker: "FROM admin_links_by_org_created"},
		"listAdminLinkProjectionCursorPageByCreator": {count: 1, tableMarker: "FROM admin_links_by_org_created"},
	},
	"internal/api/v2/libraries.go": {
		"UpdateLibrary": {count: 1, tableMarker: "UPDATE libraries SET"}, // dynamic SET fields come from a closed request mapping.
	},
	"internal/api/v2/org_admin.go": {
		"updateOrgSetting": {count: 1, tableMarker: "UPDATE organizations SET settings["}, // key is checked against allowedOrgSettingKeys.
	},
	"internal/gc/store_cassandra.go": {
		"ListFailedItems":       {count: 2, tableMarker: "FROM gc_failed_items"},  // optional LIMIT only.
		"PendingItemExists":     {count: 1, tableMarker: "FROM gc_pending_items"}, // optional clustering-key restriction.
		"acquireHardDeleteLock": {count: 2},                                       // fixed lock tables, enforced by the invocation inventory below.
		"renewHardDeleteLock":   {count: 1},
		"releaseHardDeleteLock": {count: 1},
	},
}

func blockMappingCallName(expr ast.Expr) string {
	switch value := expr.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.SelectorExpr:
		return value.Sel.Name
	}
	return ""
}

func blockMappingResolveAlias(expr ast.Expr, locals map[string]ast.Expr, seen map[string]bool) ast.Expr {
	for {
		switch value := expr.(type) {
		case *ast.ParenExpr:
			expr = value.X
		case *ast.Ident:
			if seen[value.Name] {
				return expr
			}
			next, ok := locals[value.Name]
			if !ok || next == nil {
				return expr
			}
			seen[value.Name] = true
			expr = next
		default:
			return expr
		}
	}
}

func blockMappingReceiverType(expression ast.Expr, locals map[string]ast.Expr, seen map[string]bool) string {
	switch value := expression.(type) {
	case *ast.ParenExpr:
		return blockMappingReceiverType(value.X, locals, seen)
	case *ast.StarExpr:
		return blockMappingReceiverType(value.X, locals, seen)
	case *ast.CompositeLit:
		return blockMappingTypeName(value.Type)
	case *ast.Ident:
		if seen[value.Name] {
			return ""
		}
		next, ok := locals[value.Name]
		if !ok || next == nil {
			return ""
		}
		seen[value.Name] = true
		return blockMappingReceiverType(next, locals, seen)
	}
	return ""
}

func blockMappingLocalStrings(body ast.Node, parameters *ast.FieldList, arguments []ast.Expr, outer map[string]ast.Expr, source *blockMappingSourceStrings, activeFunctions map[string]bool, depth int) map[string]ast.Expr {
	locals := make(map[string]ast.Expr, len(outer)+8)
	for name, expr := range outer {
		locals[name] = expr
	}
	if parameters != nil {
		argumentIndex := 0
		for _, field := range parameters.List {
			for _, name := range field.Names {
				if argumentIndex < len(arguments) {
					resolved := source.resolve(arguments[argumentIndex], outer, map[string]bool{}, activeFunctions, depth+1)
					if resolved.constant {
						locals[name.Name] = &ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(resolved.text)}
					} else if resolved.text != "" {
						dynamic := &ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(resolved.text)}
						locals[name.Name] = dynamic
						if source.dynamicValues == nil {
							source.dynamicValues = map[ast.Expr]bool{}
						}
						source.dynamicValues[dynamic] = true
					} else {
						locals[name.Name] = nil
					}
				} else {
					locals[name.Name] = nil
				}
				argumentIndex++
			}
		}
	}
	if body == nil {
		return locals
	}
	ast.Inspect(body, func(node ast.Node) bool {
		if _, nestedFunction := node.(*ast.FuncLit); nestedFunction {
			return false
		}
		switch value := node.(type) {
		case *ast.AssignStmt:
			for index, left := range value.Lhs {
				name, ok := left.(*ast.Ident)
				if !ok {
					continue
				}
				if index < len(value.Rhs) {
					locals[name.Name] = value.Rhs[index]
				} else {
					locals[name.Name] = nil
				}
			}
		case *ast.ValueSpec:
			for index, name := range value.Names {
				if index < len(value.Values) {
					locals[name.Name] = value.Values[index]
				}
			}
		}
		return true
	})
	return locals
}

func (source *blockMappingSourceStrings) resolve(expr ast.Expr, locals map[string]ast.Expr, seenLocals, activeFunctions map[string]bool, depth int) blockMappingResolvedString {
	if expr == nil || depth > 24 {
		return blockMappingResolvedString{}
	}
	switch value := expr.(type) {
	case *ast.BasicLit:
		if value.Kind != token.STRING {
			return blockMappingResolvedString{}
		}
		text, err := strconv.Unquote(value.Value)
		return blockMappingResolvedString{text: text, constant: err == nil && !source.dynamicValues[value]}
	case *ast.Ident:
		if seenLocals[value.Name] {
			return blockMappingResolvedString{}
		}
		if next, ok := locals[value.Name]; ok {
			if next == nil {
				return blockMappingResolvedString{}
			}
			seen := make(map[string]bool, len(seenLocals)+1)
			for name, active := range seenLocals {
				seen[name] = active
			}
			seen[value.Name] = true
			return source.resolve(next, locals, seen, activeFunctions, depth+1)
		}
		if source.mutableGlobals[value.Name] {
			return blockMappingResolvedString{ambiguous: true}
		}
		if next, ok := source.globals[value.Name]; ok {
			return source.resolve(next, source.globals, seenLocals, activeFunctions, depth+1)
		}
	case *ast.ParenExpr:
		return source.resolve(value.X, locals, seenLocals, activeFunctions, depth+1)
	case *ast.BinaryExpr:
		if value.Op != token.ADD {
			return blockMappingResolvedString{}
		}
		left := source.resolve(value.X, locals, seenLocals, activeFunctions, depth+1)
		right := source.resolve(value.Y, locals, seenLocals, activeFunctions, depth+1)
		return blockMappingResolvedString{text: left.text + right.text, constant: left.constant && right.constant, ambiguous: left.ambiguous || right.ambiguous}
	case *ast.CompositeLit:
		var combined blockMappingResolvedString
		combined.constant = true
		for _, element := range value.Elts {
			resolved := source.resolve(element, locals, seenLocals, activeFunctions, depth+1)
			combined.text += resolved.text
			combined.constant = combined.constant && resolved.constant
			combined.ambiguous = combined.ambiguous || resolved.ambiguous
		}
		return combined
	case *ast.CallExpr:
		name := blockMappingCallName(value.Fun)
		if name == "Join" && len(value.Args) == 2 {
			separator := source.resolve(value.Args[1], locals, seenLocals, activeFunctions, depth+1)
			itemsExpr := blockMappingResolveAlias(value.Args[0], locals, map[string]bool{})
			items, ok := itemsExpr.(*ast.CompositeLit)
			if !ok {
				return blockMappingResolvedString{text: separator.text, constant: false}
			}
			combined := blockMappingResolvedString{constant: separator.constant}
			for index, element := range items.Elts {
				if index > 0 {
					combined.text += separator.text
				}
				resolved := source.resolve(element, locals, seenLocals, activeFunctions, depth+1)
				combined.text += resolved.text
				combined.constant = combined.constant && resolved.constant
				combined.ambiguous = combined.ambiguous || resolved.ambiguous
			}
			return combined
		}
		if name == "Sprintf" && len(value.Args) > 0 {
			format := source.resolve(value.Args[0], locals, seenLocals, activeFunctions, depth+1)
			if len(value.Args) == 1 {
				return format
			}
			format.constant = false
			return format
		}
		function := source.functions[name]
		if selector, isMethod := value.Fun.(*ast.SelectorExpr); isMethod {
			candidates := source.methods[selector.Sel.Name]
			receiverType := blockMappingReceiverType(selector.X, locals, map[string]bool{})
			var matching *ast.FuncDecl
			for _, candidate := range candidates {
				if candidate.Recv == nil || len(candidate.Recv.List) == 0 || blockMappingTypeName(candidate.Recv.List[0].Type) != receiverType {
					continue
				}
				if matching != nil {
					return blockMappingResolvedString{ambiguous: true}
				}
				matching = candidate
			}
			if matching == nil {
				if len(candidates) > 0 {
					// A bare method name is not a declaration identity. If the
					// receiver cannot be resolved here, keep the CQL unresolved so
					// the execution-site inventory fails closed.
					return blockMappingResolvedString{ambiguous: true}
				}
				function = nil
			} else {
				function = matching
			}
		}
		if function == nil || function.Body == nil || activeFunctions[name] {
			return blockMappingResolvedString{}
		}
		active := make(map[string]bool, len(activeFunctions)+1)
		for current, running := range activeFunctions {
			active[current] = running
		}
		active[name] = true
		functionLocals := blockMappingLocalStrings(function.Body, function.Type.Params, value.Args, locals, source, active, depth)
		var result blockMappingResolvedString
		result.constant = true
		found := false
		returnCount := 0
		ast.Inspect(function.Body, func(node ast.Node) bool {
			if _, nestedFunction := node.(*ast.FuncLit); nestedFunction {
				return false
			}
			if returned, ok := node.(*ast.ReturnStmt); ok && len(returned.Results) > 0 {
				next := source.resolve(returned.Results[0], functionLocals, map[string]bool{}, active, depth+1)
				if returnCount == 0 {
					result = next
				} else {
					result.text += next.text
					result.constant = false
					result.ambiguous = true
				}
				result.constant = result.constant && next.constant
				result.ambiguous = result.ambiguous || next.ambiguous
				returnCount++
				found = true
			}
			return true
		})
		if found {
			if blockMappingHasAmbiguousStringControlFlow(function) || len(blockMappingNestedClosureWrites(function.Body, functionLocals, token.NoPos)) != 0 {
				result.constant = false
				result.ambiguous = true
			}
			return result
		}
	}
	return blockMappingResolvedString{}
}

func (source *blockMappingSourceStrings) queryLocals(function *ast.FuncDecl, functionLiteral *ast.FuncLit) map[string]ast.Expr {
	if function != nil {
		return blockMappingLocalStrings(function.Body, function.Type.Params, nil, source.globals, source, nil, 0)
	}
	if functionLiteral != nil {
		return blockMappingLocalStrings(functionLiteral.Body, functionLiteral.Type.Params, nil, source.globals, source, nil, 0)
	}
	return source.globals
}

// queryLocalsAt resolves assignments that precede a particular Query call.
// Assignments in a control-flow region are invalidated conservatively because
// walking both branches in source order cannot prove which value reaches the
// call site.
func (source *blockMappingSourceStrings) queryLocalsAt(function *ast.FuncDecl, functionLiteral *ast.FuncLit, arguments []ast.Expr, outer map[string]ast.Expr, at token.Pos) map[string]ast.Expr {
	var body ast.Node
	var parameters *ast.FieldList
	if function != nil {
		body, parameters = function.Body, function.Type.Params
	} else if functionLiteral != nil {
		body, parameters = functionLiteral.Body, functionLiteral.Type.Params
	}
	locals := blockMappingLocalStrings(nil, parameters, arguments, outer, source, nil, 0)
	if body == nil {
		return locals
	}
	ast.Inspect(body, func(node ast.Node) bool {
		if node == nil || node.Pos() >= at {
			return true
		}
		if _, nestedFunction := node.(*ast.FuncLit); nestedFunction {
			return false
		}
		switch value := node.(type) {
		case *ast.AssignStmt:
			for index, left := range value.Lhs {
				name, ok := left.(*ast.Ident)
				if !ok {
					continue
				}
				if index < len(value.Rhs) {
					locals[name.Name] = value.Rhs[index]
				} else {
					locals[name.Name] = nil
				}
			}
		case *ast.ValueSpec:
			for index, name := range value.Names {
				if index < len(value.Values) {
					locals[name.Name] = value.Values[index]
				}
			}
		}
		return true
	})
	// Once a tracked query string's address is exposed, an indirect write can
	// change the value without assigning its identifier. The resolver has no
	// pointer alias analysis, so poison that binding before classifying Query.
	addressTaken := map[string]bool{}
	ast.Inspect(body, func(node ast.Node) bool {
		if node == nil || node.Pos() >= at {
			return true
		}
		unary, ok := node.(*ast.UnaryExpr)
		if !ok || unary.Op != token.AND {
			return true
		}
		identifier, ok := unary.X.(*ast.Ident)
		if !ok {
			return true
		}
		if _, tracked := locals[identifier.Name]; tracked {
			addressTaken[identifier.Name] = true
		}
		return true
	})
	for name := range addressTaken {
		locals[name] = nil
	}
	ast.Inspect(body, func(node ast.Node) bool {
		if node == nil || node.Pos() >= at {
			return true
		}
		if _, nestedFunction := node.(*ast.FuncLit); nestedFunction {
			for name := range blockMappingNestedClosureWrites(node, locals, at) {
				locals[name] = nil
			}
			return false
		}
		switch value := node.(type) {
		case *ast.IfStmt:
			blockMappingInvalidateControlAssignments(value, at, locals)
		case *ast.ForStmt:
			blockMappingInvalidateControlAssignments(value, at, locals)
		case *ast.RangeStmt:
			blockMappingInvalidateControlAssignments(value, at, locals)
		case *ast.SwitchStmt:
			blockMappingInvalidateControlAssignments(value, at, locals)
		case *ast.TypeSwitchStmt:
			blockMappingInvalidateControlAssignments(value, at, locals)
		case *ast.SelectStmt:
			blockMappingInvalidateControlAssignments(value, at, locals)
		case *ast.BranchStmt, *ast.LabeledStmt, *ast.GoStmt, *ast.DeferStmt:
			for name := range locals {
				locals[name] = nil
			}
		}
		return true
	})
	return locals
}

// blockMappingQueryHasFixedTableMarkerAtCall ties an allowlisted table marker
// to the exact CQL argument reaching a Query/Batch.Bind call. For a local query
// string, it accepts a fixed-prefix initializer followed only by += or
// `query = query + suffix` updates before that call. Reassignment to an
// unrelated value, a missing initializer, or a different expression fails
// closed. This permits the repository's fixed-table pagination builders while
// preventing an unrelated literal elsewhere in the owner from authorizing it.
func blockMappingQueryHasFixedTableMarkerAtCall(source *blockMappingSourceStrings, function *ast.FuncDecl, functionLiteral *ast.FuncLit, arguments []ast.Expr, outer map[string]ast.Expr, call *ast.CallExpr, argument ast.Expr, marker string) bool {
	if marker == "" {
		return true
	}
	locals := source.queryLocalsAt(function, functionLiteral, arguments, outer, call.Pos())
	resolved := source.resolve(argument, locals, map[string]bool{}, map[string]bool{}, 0)
	if !resolved.ambiguous && strings.Contains(strings.ToLower(resolved.text), strings.ToLower(marker)) {
		return true
	}
	identifier, ok := blockMappingResolveAlias(argument, locals, map[string]bool{}).(*ast.Ident)
	if !ok {
		return false
	}
	var body ast.Node
	if function != nil {
		body = function.Body
	} else if functionLiteral != nil {
		body = functionLiteral.Body
	}
	if body == nil {
		return false
	}
	markerLocals := make(map[string]ast.Expr, len(locals))
	for name, expression := range locals {
		if name != identifier.Name {
			markerLocals[name] = expression
		}
	}
	initialized := false
	valid := true
	ast.Inspect(body, func(node ast.Node) bool {
		if node == nil {
			return true
		}
		if node.Pos() >= call.Pos() {
			return false
		}
		if _, nestedFunction := node.(*ast.FuncLit); nestedFunction {
			return false
		}
		initialize := func(expression ast.Expr) {
			if initialized {
				valid = false
				return
			}
			value := source.resolve(expression, markerLocals, map[string]bool{}, map[string]bool{}, 0)
			initialized = true
			if !strings.Contains(strings.ToLower(value.text), strings.ToLower(marker)) {
				valid = false
			}
		}
		switch value := node.(type) {
		case *ast.ValueSpec:
			for index, name := range value.Names {
				if name.Name != identifier.Name {
					continue
				}
				if index >= len(value.Values) {
					valid = false
					continue
				}
				initialize(value.Values[index])
			}
		case *ast.AssignStmt:
			for index, left := range value.Lhs {
				name, isIdentifier := left.(*ast.Ident)
				if !isIdentifier || name.Name != identifier.Name {
					continue
				}
				if index >= len(value.Rhs) {
					valid = false
					continue
				}
				if value.Tok == token.ADD_ASSIGN && initialized {
					continue
				}
				if value.Tok == token.ASSIGN && initialized {
					if concat, ok := value.Rhs[index].(*ast.BinaryExpr); ok && concat.Op == token.ADD {
						if leftName, ok := concat.X.(*ast.Ident); ok && leftName.Name == identifier.Name {
							continue
						}
					}
					valid = false
					continue
				}
				initialize(value.Rhs[index])
			}
		}
		return valid
	})
	return initialized && valid
}

func blockMappingInvalidateControlAssignments(control ast.Node, at token.Pos, locals map[string]ast.Expr) {
	ast.Inspect(control, func(node ast.Node) bool {
		if node == nil || node.Pos() >= at {
			return true
		}
		switch value := node.(type) {
		case *ast.AssignStmt:
			for _, left := range value.Lhs {
				if name, ok := left.(*ast.Ident); ok {
					locals[name.Name] = nil
				}
			}
		case *ast.ValueSpec:
			for _, name := range value.Names {
				locals[name.Name] = nil
			}
		}
		return true
	})
}

func blockMappingQueryChainContains(expr ast.Expr, query *ast.CallExpr) bool {
	switch value := expr.(type) {
	case *ast.CallExpr:
		if value == query {
			return true
		}
		if selector, ok := value.Fun.(*ast.SelectorExpr); ok {
			return blockMappingQueryChainContains(selector.X, query)
		}
	case *ast.SelectorExpr:
		return blockMappingQueryChainContains(value.X, query)
	case *ast.ParenExpr:
		return blockMappingQueryChainContains(value.X, query)
	}
	return false
}

func blockMappingQueryConsistencyNames(body ast.Node, query *ast.CallExpr) []string {
	var consistencies []string
	ast.Inspect(body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) != 1 {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || (selector.Sel.Name != "Consistency" && selector.Sel.Name != "SetConsistency") || !blockMappingQueryObjectContains(body, selector.X, query) {
			return true
		}
		prefix := ""
		if selector.Sel.Name == "SetConsistency" {
			prefix = "SetConsistency:"
		}
		switch value := call.Args[0].(type) {
		case *ast.Ident:
			consistencies = append(consistencies, prefix+value.Name)
		case *ast.SelectorExpr:
			if pkg, ok := value.X.(*ast.Ident); ok {
				consistencies = append(consistencies, prefix+pkg.Name+"."+value.Sel.Name)
			}
		}
		return true
	})
	return consistencies
}

// blockMappingQueryObjectContains ties fluent methods and local Query aliases
// back to the exact Query callsite. This catches q.SetConsistency(...) after
// q was initialized from Query(...).Consistency(...), as well as ordinary
// fluent chains.
func blockMappingQueryObjectContains(body ast.Node, receiver ast.Expr, query *ast.CallExpr) bool {
	if blockMappingQueryChainContains(receiver, query) {
		return true
	}
	root := func(expression ast.Expr) *ast.Ident {
		for {
			switch value := expression.(type) {
			case *ast.Ident:
				return value
			case *ast.SelectorExpr:
				expression = value.X
			case *ast.CallExpr:
				if selector, ok := value.Fun.(*ast.SelectorExpr); ok {
					expression = selector.X
				} else {
					return nil
				}
			case *ast.ParenExpr:
				expression = value.X
			default:
				return nil
			}
		}
	}
	target := root(receiver)
	if target == nil || body == nil {
		return false
	}
	aliases := map[string]bool{}
	for changed := true; changed; {
		changed = false
		ast.Inspect(body, func(node ast.Node) bool {
			var left []ast.Expr
			var right []ast.Expr
			switch value := node.(type) {
			case *ast.AssignStmt:
				left, right = value.Lhs, value.Rhs
			case *ast.ValueSpec:
				for _, name := range value.Names {
					left = append(left, name)
				}
				right = value.Values
			default:
				return true
			}
			for index, destination := range left {
				name, ok := destination.(*ast.Ident)
				if !ok || aliases[name.Name] || index >= len(right) {
					continue
				}
				expression := right[index]
				aliasSource := root(expression)
				if blockMappingQueryChainContains(expression, query) || (aliasSource != nil && aliases[aliasSource.Name]) {
					aliases[name.Name] = true
					changed = true
				}
			}
			return true
		})
	}
	return aliases[target.Name] || target == root(receiver) && blockMappingQueryChainContains(receiver, query)
}

func blockMappingBatchContainsMappingCQL(source *blockMappingSourceStrings, body ast.Node, function *ast.FuncDecl, functionLiteral *ast.FuncLit, arguments []ast.Expr, outer map[string]ast.Expr) bool {
	found := false
	if body == nil {
		return false
	}
	ast.Inspect(body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || (blockMappingCallName(call.Fun) != "Query" && blockMappingCallName(call.Fun) != "Bind") || len(call.Args) == 0 {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || !blockMappingIsCQLEntryPoint(blockMappingCallName(call.Fun), len(call.Args)) {
			return true
		}
		// A Batch entry point has a Batch receiver; the source resolver's
		// Query/Bind classification is also used for gocql Session.Query.
		if blockMappingCallName(selector.X) == "Session" || blockMappingCallName(selector.X) == "Query" {
			return true
		}
		locals := source.queryLocalsAt(function, functionLiteral, arguments, outer, call.Pos())
		resolved := source.resolve(call.Args[0], locals, map[string]bool{}, map[string]bool{}, 0)
		if strings.Contains(strings.ToLower(resolved.text), "block_id_mappings") {
			found = true
			return false
		}
		return true
	})
	return found
}

func blockMappingHasDirectQuery(body ast.Node) bool {
	found := false
	ast.Inspect(body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if ok && blockMappingIsCQLEntryPoint(blockMappingCallName(call.Fun), len(call.Args)) {
			found = true
			return false
		}
		return !found
	})
	return found
}

func blockMappingIsBatchEntryType(expression ast.Expr) bool {
	switch value := expression.(type) {
	case *ast.Ident:
		return value.Name == "BatchEntry"
	case *ast.SelectorExpr:
		return value.Sel.Name == "BatchEntry"
	}
	return false
}

func blockMappingMigrationQueryAllowlisted(relPath, owner string, argument ast.Expr) bool {
	// Migration statements come from the checked-in migration inventory and are
	// deliberately executed as parsed statements rather than inline CQL.
	identifier, ok := argument.(*ast.Ident)
	return ok && relPath == "internal/db/migrator.go" && owner == "apply" && identifier.Name == "stmt"
}

func blockMappingPotentialDynamicQuery(statement string, argument ast.Expr) bool {
	lower := strings.ToLower(statement)
	if strings.Contains(lower, "block_id_") || (strings.Contains(lower, "blockid") && strings.Contains(lower, "mapping")) {
		return true
	}
	trimmed := strings.TrimSpace(strings.ToUpper(statement))
	if strings.HasSuffix(trimmed, " FROM") || strings.HasSuffix(trimmed, " INTO") || strings.HasSuffix(trimmed, " UPDATE") || strings.Contains(trimmed, " FROM WHERE") || strings.Contains(trimmed, " FROM ORDER BY") || strings.Contains(trimmed, " FROM LIMIT") || strings.Contains(trimmed, " INTO VALUES") || strings.Contains(trimmed, " UPDATE WHERE") {
		return true
	}
	potential := false
	ast.Inspect(argument, func(node ast.Node) bool {
		if identifier, ok := node.(*ast.Ident); ok {
			name := strings.ToLower(identifier.Name)
			if strings.Contains(name, "blockmapping") || strings.Contains(name, "block_mapping") || strings.Contains(name, "blockidmapping") {
				potential = true
			}
		}
		return true
	})
	return potential
}

func blockMappingParentMap(root ast.Node) map[ast.Node]ast.Node {
	parents := make(map[ast.Node]ast.Node)
	var stack []ast.Node
	ast.Inspect(root, func(node ast.Node) bool {
		if node == nil {
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			return true
		}
		if len(stack) > 0 {
			parents[node] = stack[len(stack)-1]
		}
		stack = append(stack, node)
		return true
	})
	return parents
}

func blockMappingEnclosingFunctionName(parents map[ast.Node]ast.Node, node ast.Node) string {
	for parent := parents[node]; parent != nil; parent = parents[parent] {
		if function, ok := parent.(*ast.FuncDecl); ok {
			return function.Name.Name
		}
	}
	return ""
}

func blockMappingIsCQLEntryPoint(method string, arguments int) bool {
	switch method {
	case "Query":
		return arguments > 0
	case "Bind":
		// gocql Batch.Bind takes the CQL string and a binding callback.
		// The argument count avoids treating ordinary one-argument Bind
		// methods (for example, an HTTP request binder) as CQL entry points.
		return arguments >= 2
	default:
		return false
	}
}

func blockMappingIsDBType(expression ast.Expr) bool {
	switch value := expression.(type) {
	case *ast.StarExpr:
		return blockMappingIsDBType(value.X)
	case *ast.ParenExpr:
		return blockMappingIsDBType(value.X)
	case *ast.Ident:
		return value.Name == "DB"
	case *ast.SelectorExpr:
		return value.Sel.Name == "DB"
	default:
		return false
	}
}

func blockMappingTypeName(expression ast.Expr) string {
	switch value := expression.(type) {
	case *ast.StarExpr:
		return blockMappingTypeName(value.X)
	case *ast.ParenExpr:
		return blockMappingTypeName(value.X)
	case *ast.Ident:
		return value.Name
	case *ast.SelectorExpr:
		return blockMappingTypeName(value.X) + "." + value.Sel.Name
	}
	return ""
}

var blockMappingCASTerminals = map[string]bool{
	"ScanCAS": true, "MapScanCAS": true,
	"ScanCASContext": true, "MapScanCASContext": true,
	"ExecCAS": true, "MapExecCAS": true,
	"ExecCASContext": true, "MapExecCASContext": true,
	"ExecuteBatchCAS": true, "MapExecuteBatchCAS": true,
}

func blockMappingFunctionReturnsBatch(function *ast.FuncDecl) bool {
	if function == nil || function.Type == nil || function.Type.Results == nil {
		return false
	}
	for _, result := range function.Type.Results.List {
		if blockMappingTypeIsBatch(result.Type) {
			return true
		}
	}
	return false
}

// blockMappingSerialConsistency follows local and package const aliases and
// distinguishes known gocql consistency constants from unknown enum values.
// Unknown values are not safe to assume non-SERIAL on the upload path.
func blockMappingSerialConsistency(expression ast.Expr, locals, globals map[string]ast.Expr, seen map[string]bool, depth int) (serial, resolved bool) {
	if expression == nil || depth > 24 {
		return false, false
	}
	switch value := expression.(type) {
	case *ast.ParenExpr:
		return blockMappingSerialConsistency(value.X, locals, globals, seen, depth+1)
	case *ast.Ident:
		if seen[value.Name] {
			return false, false
		}
		if next, ok := locals[value.Name]; ok {
			if next == nil {
				return false, false
			}
			seen[value.Name] = true
			return blockMappingSerialConsistency(next, locals, globals, seen, depth+1)
		}
		if next, ok := globals[value.Name]; ok {
			seen[value.Name] = true
			return blockMappingSerialConsistency(next, globals, globals, seen, depth+1)
		}
		if strings.EqualFold(value.Name, "serial") || strings.EqualFold(value.Name, "localserial") ||
			strings.EqualFold(value.Name, "IdentityAuthorityReadConsistency") || strings.EqualFold(value.Name, "LibraryHeadSerialConsistency") {
			return true, true
		}
	case *ast.SelectorExpr:
		if receiver, ok := value.X.(*ast.Ident); ok && receiver.Name == "gocql" {
			switch value.Sel.Name {
			case "Serial", "LocalSerial":
				return true, true
			case "Any", "One", "Two", "Three", "Quorum", "All", "LocalQuorum", "EachQuorum", "LocalOne":
				return false, true
			}
		}
	}
	return false, false
}

// blockMappingHotPathNoPaxosViolations follows same-package functions and DB
// methods reachable from upload mapping writers. Queries in those helpers must
// be statically resolved and free of conditional CQL; global SERIAL reads and
// CAS terminals are prohibited anywhere on the reachable path.
func blockMappingHotPathNoPaxosViolations(t *testing.T, repoRoot string, roots map[string]bool) []string {
	t.Helper()
	type sourceFile struct {
		path string
		file *ast.File
	}
	type hotTarget struct {
		name string
		path string
		fn   *ast.FuncDecl
		lit  *ast.FuncLit
		body ast.Node
	}
	files := identityAuthorityProductionGoFiles(t, repoRoot)
	source := &blockMappingSourceStrings{
		functions:               map[string]*ast.FuncDecl{},
		methods:                 map[string][]*ast.FuncDecl{},
		functionPaths:           map[string]string{},
		batchReturningFunctions: map[string]bool{},
		globals:                 map[string]ast.Expr{},
		mutableGlobals:          map[string]bool{},
		dynamicValues:           map[ast.Expr]bool{},
	}
	var dbFiles []sourceFile
	freeFunctions := map[string]*ast.FuncDecl{}
	methodsByType := map[string]map[string][]*ast.FuncDecl{}
	methodsByName := map[string][]*ast.FuncDecl{}
	methodPaths := map[*ast.FuncDecl]string{}
	typeFields := map[string]map[string]ast.Expr{}
	functionLiterals := map[string]*ast.FuncLit{}
	rootTargets := map[string]hotTarget{}
	for _, path := range files {
		relPath, err := filepath.Rel(repoRoot, path)
		if err != nil {
			t.Fatalf("relative path %s: %v", path, err)
		}
		relPath = filepath.ToSlash(relPath)
		if filepath.ToSlash(filepath.Dir(relPath)) != "internal/db" {
			continue
		}
		parsed := blockMappingAuthorityParse(t, path)
		dbFiles = append(dbFiles, sourceFile{path: relPath, file: parsed})
		for _, declaration := range parsed.Decls {
			switch value := declaration.(type) {
			case *ast.FuncDecl:
				if value.Recv == nil {
					source.functions[value.Name.Name] = value
					source.functionPaths[value.Name.Name] = relPath
				} else {
					source.methods[value.Name.Name] = append(source.methods[value.Name.Name], value)
					if source.functions[value.Name.Name] == nil {
						source.functions[value.Name.Name] = value
					}
					if len(source.methods[value.Name.Name]) > 1 {
						delete(source.functions, value.Name.Name)
					}
					receiverType := blockMappingTypeName(value.Recv.List[0].Type)
					methodsByName[value.Name.Name] = append(methodsByName[value.Name.Name], value)
					methodPaths[value] = relPath
					if methodsByType[receiverType] == nil {
						methodsByType[receiverType] = map[string][]*ast.FuncDecl{}
					}
					methodsByType[receiverType][value.Name.Name] = append(methodsByType[receiverType][value.Name.Name], value)
				}
				if blockMappingFunctionReturnsBatch(value) {
					source.batchReturningFunctions[value.Name.Name] = true
				}
				if value.Recv == nil {
					freeFunctions[value.Name.Name] = value
				}
				if roots[value.Name.Name] {
					rootTargets[value.Name.Name] = hotTarget{name: value.Name.Name, path: relPath, fn: value, body: value.Body}
				}
			case *ast.GenDecl:
				for _, spec := range value.Specs {
					if typeSpec, ok := spec.(*ast.TypeSpec); ok {
						if structure, ok := typeSpec.Type.(*ast.StructType); ok {
							fields := map[string]ast.Expr{}
							for _, field := range structure.Fields.List {
								for _, name := range field.Names {
									fields[name.Name] = field.Type
								}
							}
							typeFields[typeSpec.Name.Name] = fields
						}
						continue
					}
					valueSpec, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for index, name := range valueSpec.Names {
						if value.Tok == token.VAR {
							source.mutableGlobals[name.Name] = true
						}
						if value.Tok == token.CONST && index < len(valueSpec.Values) {
							source.globals[name.Name] = valueSpec.Values[index]
						}
						if index < len(valueSpec.Values) {
							if literal, ok := valueSpec.Values[index].(*ast.FuncLit); ok {
								functionLiterals[name.Name] = literal
								if roots[name.Name] {
									rootTargets[name.Name] = hotTarget{name: name.Name, path: relPath, lit: literal, body: literal.Body}
								}
							}
						}
					}
				}
			}
		}
	}
	if len(dbFiles) == 0 {
		t.Fatalf("no internal/db production sources found for the upload hot-path audit")
	}
	var inferType func(ast.Expr, hotTarget, map[string]bool) string
	inferType = func(expression ast.Expr, target hotTarget, seen map[string]bool) string {
		if expression == nil {
			return ""
		}
		switch value := expression.(type) {
		case *ast.ParenExpr:
			return inferType(value.X, target, seen)
		case *ast.StarExpr:
			return inferType(value.X, target, seen)
		case *ast.UnaryExpr:
			return inferType(value.X, target, seen)
		case *ast.CompositeLit:
			return blockMappingTypeName(value.Type)
		case *ast.Ident:
			if seen[value.Name] {
				return ""
			}
			seen[value.Name] = true
			for _, fields := range []*ast.FieldList{func() *ast.FieldList {
				if target.fn != nil {
					return target.fn.Recv
				}
				return nil
			}(), func() *ast.FieldList {
				if target.fn != nil {
					return target.fn.Type.Params
				}
				if target.lit != nil {
					return target.lit.Type.Params
				}
				return nil
			}()} {
				if fields == nil {
					continue
				}
				for _, field := range fields.List {
					for _, name := range field.Names {
						if name.Name == value.Name {
							return blockMappingTypeName(field.Type)
						}
					}
				}
			}
			var inferred string
			ast.Inspect(target.body, func(node ast.Node) bool {
				if inferred != "" {
					return false
				}
				switch declaration := node.(type) {
				case *ast.ValueSpec:
					for index, name := range declaration.Names {
						if name.Name != value.Name {
							continue
						}
						if declaration.Type != nil {
							inferred = blockMappingTypeName(declaration.Type)
						} else if index < len(declaration.Values) {
							inferred = inferType(declaration.Values[index], target, seen)
						}
						return false
					}
				case *ast.AssignStmt:
					for index, left := range declaration.Lhs {
						name, ok := left.(*ast.Ident)
						if !ok || name.Name != value.Name || index >= len(declaration.Rhs) {
							continue
						}
						inferred = inferType(declaration.Rhs[index], target, seen)
						return false
					}
				}
				return true
			})
			return inferred
		case *ast.SelectorExpr:
			receiverType := inferType(value.X, target, seen)
			if fields := typeFields[receiverType]; fields != nil {
				return blockMappingTypeName(fields[value.Sel.Name])
			}
		case *ast.CallExpr:
			if identifier, ok := value.Fun.(*ast.Ident); ok {
				if function := source.functions[identifier.Name]; function != nil && function.Type.Results != nil && len(function.Type.Results.List) > 0 {
					return blockMappingTypeName(function.Type.Results.List[0].Type)
				}
			}
		}
		return ""
	}
	var violations []string
	for name := range roots {
		if _, found := rootTargets[name]; !found {
			violations = append(violations, "hot-path mapping writer "+name+" not found; transitive guard would be vacuous")
		}
	}
	var visit func(rootName string, target hotTarget, arguments []ast.Expr, outer map[string]ast.Expr, active map[string]bool, depth int)
	visit = func(rootName string, target hotTarget, arguments []ast.Expr, outer map[string]ast.Expr, active map[string]bool, depth int) {
		if target.body == nil {
			return
		}
		key := target.path + ":" + target.name
		if active[key] {
			return
		}
		if depth > 32 {
			violations = append(violations, "upload mapping writer "+rootName+" reaches a call chain too deep to classify at "+target.path+":"+target.name)
			return
		}
		nestedActive := make(map[string]bool, len(active)+1)
		for current, inUse := range active {
			nestedActive[current] = inUse
		}
		nestedActive[key] = true
		dbIdentifiers := map[string]bool{}
		addDBFields := func(fields *ast.FieldList) {
			if fields == nil {
				return
			}
			for _, field := range fields.List {
				if !blockMappingIsDBType(field.Type) {
					continue
				}
				for _, name := range field.Names {
					dbIdentifiers[name.Name] = true
				}
			}
		}
		if target.fn != nil {
			addDBFields(target.fn.Recv)
			addDBFields(target.fn.Type.Params)
		} else if target.lit != nil {
			addDBFields(target.lit.Type.Params)
		}
		for changed := true; changed; {
			changed = false
			ast.Inspect(target.body, func(node ast.Node) bool {
				switch value := node.(type) {
				case *ast.ValueSpec:
					if blockMappingIsDBType(value.Type) {
						for _, name := range value.Names {
							if !dbIdentifiers[name.Name] {
								dbIdentifiers[name.Name] = true
								changed = true
							}
						}
					}
				case *ast.AssignStmt:
					for index, left := range value.Lhs {
						if index >= len(value.Rhs) {
							continue
						}
						right, rightOK := value.Rhs[index].(*ast.Ident)
						name, leftOK := left.(*ast.Ident)
						if rightOK && leftOK && dbIdentifiers[right.Name] && !dbIdentifiers[name.Name] {
							dbIdentifiers[name.Name] = true
							changed = true
						}
					}
				}
				return true
			})
		}
		ast.Inspect(target.body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			callName := blockMappingCallName(call.Fun)
			callLocals := source.queryLocalsAt(target.fn, target.lit, arguments, outer, call.Pos())
			if callName == "ReadBlockMappingAuthority" || callName == "readBlockMappingAuthority" {
				violations = append(violations, "upload mapping writer "+rootName+" reaches cold-path "+callName+" through "+target.path+":"+target.name)
			}
			if blockMappingIsCQLEntryPoint(callName, len(call.Args)) {
				resolved := source.resolve(call.Args[0], callLocals, map[string]bool{}, map[string]bool{}, 0)
				if !resolved.constant || resolved.ambiguous {
					violations = append(violations, "upload mapping writer "+rootName+" reaches unresolved/dynamic CQL through "+target.path+":"+target.name)
				} else if blockMappingCQLConditionalPattern.MatchString(resolved.text) {
					violations = append(violations, "upload mapping writer "+rootName+" issues conditional CQL through "+target.path+":"+target.name)
				}
			}
			if selector, isSelector := call.Fun.(*ast.SelectorExpr); isSelector {
				method := selector.Sel.Name
				if method == "SerialConsistency" || blockMappingCASTerminals[method] {
					violations = append(violations, "upload mapping writer "+rootName+" reaches an LWT ("+method+") through "+target.path+":"+target.name)
				}
				if method == "SetConsistency" {
					violations = append(violations, "upload mapping writer "+rootName+" uses forbidden SetConsistency through "+target.path+":"+target.name)
				}
				if method == "Consistency" || method == "SetConsistency" {
					if len(call.Args) != 1 {
						violations = append(violations, "upload mapping writer "+rootName+" reaches unresolved consistency assignment through "+target.path+":"+target.name)
						return true
					}
					serial, resolved := blockMappingSerialConsistency(call.Args[0], callLocals, source.globals, map[string]bool{}, 0)
					if serial {
						violations = append(violations, "upload mapping writer "+rootName+" reaches a global SERIAL read through "+target.path+":"+target.name)
					} else if !resolved {
						violations = append(violations, "upload mapping writer "+rootName+" reaches unresolved Consistency argument through "+target.path+":"+target.name)
					}
				}
			}
			var next hotTarget
			found := false
			switch value := call.Fun.(type) {
			case *ast.Ident:
				if function := freeFunctions[value.Name]; function != nil {
					rel := source.functionPaths[value.Name]
					next = hotTarget{name: value.Name, path: rel, fn: function, body: function.Body}
					found = true
				} else if literal := functionLiterals[value.Name]; literal != nil {
					for _, sourceFile := range dbFiles {
						for _, declaration := range sourceFile.file.Decls {
							gen, ok := declaration.(*ast.GenDecl)
							if !ok {
								continue
							}
							for _, spec := range gen.Specs {
								values, ok := spec.(*ast.ValueSpec)
								if !ok {
									continue
								}
								for index, name := range values.Names {
									if name.Name == value.Name && index < len(values.Values) && values.Values[index] == literal {
										next = hotTarget{name: value.Name, path: sourceFile.path, lit: literal, body: literal.Body}
										found = true
									}
								}
							}
						}
					}
				} else if alias, isLocal := callLocals[value.Name]; isLocal {
					seenAliases := map[string]bool{value.Name: true}
					for {
						identifier, isIdentifier := alias.(*ast.Ident)
						if !isIdentifier || seenAliases[identifier.Name] {
							break
						}
						seenAliases[identifier.Name] = true
						if function := freeFunctions[identifier.Name]; function != nil {
							rel := source.functionPaths[identifier.Name]
							next = hotTarget{name: identifier.Name, path: rel, fn: function, body: function.Body}
							found = true
							break
						}
						if literal := functionLiterals[identifier.Name]; literal != nil {
							for _, sourceFile := range dbFiles {
								for _, declaration := range sourceFile.file.Decls {
									gen, ok := declaration.(*ast.GenDecl)
									if !ok {
										continue
									}
									for _, spec := range gen.Specs {
										values, ok := spec.(*ast.ValueSpec)
										if !ok {
											continue
										}
										for index, name := range values.Names {
											if name.Name == identifier.Name && index < len(values.Values) && values.Values[index] == literal {
												next = hotTarget{name: identifier.Name, path: sourceFile.path, lit: literal, body: literal.Body}
												found = true
											}
										}
									}
								}
							}
							break
						}
						nextAlias, ok := callLocals[identifier.Name]
						if !ok || nextAlias == nil {
							break
						}
						alias = nextAlias
					}
					if !found && alias != nil {
						if literal, ok := alias.(*ast.FuncLit); ok {
							next = hotTarget{name: "local-function-value", path: target.path, lit: literal, body: literal.Body}
							found = true
						}
					}
					if !found {
						violations = append(violations, "upload mapping writer "+rootName+" reaches an unresolved function-value call through "+target.path+":"+target.name)
					}
				}
			case *ast.SelectorExpr:
				receiverType := inferType(value.X, target, map[string]bool{})
				candidates := methodsByType[receiverType][value.Sel.Name]
				if len(candidates) == 1 {
					method := candidates[0]
					next = hotTarget{name: method.Name.Name, path: methodPaths[method], fn: method, body: method.Body}
					found = true
				} else if len(candidates) > 1 || (receiverType == "" && len(methodsByName[value.Sel.Name]) > 0) {
					violations = append(violations, "upload mapping writer "+rootName+" reaches unresolved same-package method dispatch "+value.Sel.Name+" through "+target.path+":"+target.name)
				}
			case *ast.FuncLit:
				next = hotTarget{name: "inline-function-value", path: target.path, lit: value, body: value.Body}
				found = true
			}
			if found {
				visit(rootName, next, call.Args, callLocals, nestedActive, depth+1)
			}
			return true
		})
	}
	for name, target := range rootTargets {
		visit(name, target, nil, source.globals, map[string]bool{}, 0)
	}
	sort.Strings(violations)
	return violations
}

func blockMappingExprCreatesBatch(expression ast.Expr, batchReturningFunctions map[string]bool) bool {
	if expression == nil {
		return false
	}
	created := false
	ast.Inspect(expression, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if selector, ok := call.Fun.(*ast.SelectorExpr); ok && selector.Sel.Name == "Batch" {
			created = true
			return false
		}
		if identifier, ok := call.Fun.(*ast.Ident); ok && batchReturningFunctions[identifier.Name] {
			created = true
			return false
		}
		return true
	})
	return created
}

func blockMappingTypeIsBatch(expression ast.Expr) bool {
	if expression == nil {
		return false
	}
	isBatch := false
	ast.Inspect(expression, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.Ident:
			if value.Name == "Batch" {
				isBatch = true
				return false
			}
		case *ast.SelectorExpr:
			if value.Sel.Name == "Batch" {
				isBatch = true
				return false
			}
		}
		return true
	})
	return isBatch
}

func blockMappingBatchIdentifiers(body ast.Node, parameters *ast.FieldList, batchReturningFunctions map[string]bool) map[string]bool {
	identifiers := map[string]bool{}
	if parameters != nil {
		for _, field := range parameters.List {
			if !blockMappingTypeIsBatch(field.Type) {
				continue
			}
			for _, name := range field.Names {
				identifiers[name.Name] = true
			}
		}
	}
	if body == nil {
		return identifiers
	}
	changed := true
	for changed {
		changed = false
		ast.Inspect(body, func(node ast.Node) bool {
			if literal, ok := node.(*ast.FuncLit); ok && literal.Body != body {
				return false
			}
			add := func(name *ast.Ident, expression ast.Expr) {
				if name == nil || identifiers[name.Name] {
					return
				}
				alias, isAlias := expression.(*ast.Ident)
				if blockMappingExprCreatesBatch(expression, batchReturningFunctions) || (isAlias && identifiers[alias.Name]) {
					identifiers[name.Name] = true
					changed = true
				}
			}
			switch value := node.(type) {
			case *ast.AssignStmt:
				for index, left := range value.Lhs {
					if index >= len(value.Rhs) {
						continue
					}
					name, _ := left.(*ast.Ident)
					add(name, value.Rhs[index])
				}
			case *ast.ValueSpec:
				for index, name := range value.Names {
					if blockMappingTypeIsBatch(value.Type) {
						if !identifiers[name.Name] {
							identifiers[name.Name] = true
							changed = true
						}
						continue
					}
					var expression ast.Expr
					if index < len(value.Values) {
						expression = value.Values[index]
					}
					add(name, expression)
				}
			}
			return true
		})
	}
	return identifiers
}

func blockMappingTypeMayBeBatch(expression ast.Expr) bool {
	if blockMappingTypeIsBatch(expression) {
		return true
	}
	mayBeBatch := false
	ast.Inspect(expression, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.Ident:
			if strings.Contains(strings.ToLower(value.Name), "batch") || value.Name == "any" {
				mayBeBatch = true
			}
		case *ast.SelectorExpr:
			if strings.Contains(strings.ToLower(value.Sel.Name), "batch") {
				mayBeBatch = true
			}
		case *ast.InterfaceType:
			mayBeBatch = true
		}
		return !mayBeBatch
	})
	return mayBeBatch
}

func blockMappingReceiverIsProvenNonBatch(name string, body ast.Node, parameters *ast.FieldList, source *blockMappingSourceStrings, seen map[string]bool, depth int) bool {
	if name == "" || seen[name] || depth > 16 {
		return false
	}
	seen[name] = true
	var binding ast.Expr
	if parameters != nil {
		for _, field := range parameters.List {
			for _, parameter := range field.Names {
				if parameter.Name == name {
					binding = field.Type
					break
				}
			}
		}
	}
	if body != nil {
		ast.Inspect(body, func(node ast.Node) bool {
			if _, nested := node.(*ast.FuncLit); nested {
				return false
			}
			switch value := node.(type) {
			case *ast.ValueSpec:
				for index, declared := range value.Names {
					if declared.Name != name {
						continue
					}
					if value.Type != nil {
						binding = value.Type
					} else if index < len(value.Values) {
						binding = value.Values[index]
					}
				}
			case *ast.AssignStmt:
				for index, left := range value.Lhs {
					identifier, ok := left.(*ast.Ident)
					if !ok || identifier.Name != name {
						continue
					}
					if index < len(value.Rhs) {
						binding = value.Rhs[index]
					} else {
						binding = nil
					}
				}
			}
			return true
		})
	}
	if binding == nil {
		return false
	}
	if blockMappingTypeMayBeBatch(binding) {
		return false
	}
	switch value := binding.(type) {
	case *ast.Ident:
		return blockMappingReceiverIsProvenNonBatch(value.Name, body, parameters, source, seen, depth+1)
	case *ast.ParenExpr:
		return blockMappingReceiverExprIsProvenNonBatch(value.X, body, parameters, source, seen, depth+1)
	case *ast.CompositeLit:
		return !blockMappingTypeMayBeBatch(value.Type)
	case *ast.CallExpr:
		name := blockMappingCallName(value.Fun)
		if name == "Batch" || name == "NewBatch" || source.batchReturningFunctions[name] {
			return false
		}
		if function := source.functions[name]; function != nil && function.Type.Results != nil && len(function.Type.Results.List) > 0 {
			resultType := function.Type.Results.List[0].Type
			return !blockMappingTypeMayBeBatch(resultType)
		}
	case *ast.InterfaceType, *ast.FuncType:
		return false
	default:
		// A concrete, explicitly declared non-batch type is sufficient to rule
		// out gocql.Batch. Unknown/interface and batch-named aliases fail closed.
		return !blockMappingTypeMayBeBatch(binding)
	}
	return false
}

func blockMappingReceiverExprIsProvenNonBatch(expression ast.Expr, body ast.Node, parameters *ast.FieldList, source *blockMappingSourceStrings, seen map[string]bool, depth int) bool {
	if identifier, ok := expression.(*ast.Ident); ok {
		return blockMappingReceiverIsProvenNonBatch(identifier.Name, body, parameters, source, seen, depth+1)
	}
	if composite, ok := expression.(*ast.CompositeLit); ok {
		return !blockMappingTypeMayBeBatch(composite.Type)
	}
	if call, ok := expression.(*ast.CallExpr); ok {
		name := blockMappingCallName(call.Fun)
		if function := source.functions[name]; function != nil && function.Type.Results != nil && len(function.Type.Results.List) > 0 {
			resultType := function.Type.Results.List[0].Type
			return !blockMappingTypeMayBeBatch(resultType)
		}
	}
	return false
}

func blockMappingUnclassifiedBatchEntries(body ast.Node, parameters *ast.FieldList, source *blockMappingSourceStrings) int {
	if body == nil {
		return 0
	}
	batches := blockMappingBatchIdentifiers(body, parameters, source.batchReturningFunctions)
	parents := blockMappingParentMap(body)
	violations := 0
	ast.Inspect(body, func(node ast.Node) bool {
		if literal, ok := node.(*ast.FuncLit); ok && literal.Body != body {
			return false
		}
		selector, ok := node.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "Entries" {
			return true
		}
		batch, ok := selector.X.(*ast.Ident)
		if (!ok || !batches[batch.Name]) && !blockMappingExprCreatesBatch(selector.X, source.batchReturningFunctions) {
			if ok && blockMappingReceiverIsProvenNonBatch(batch.Name, body, parameters, source, map[string]bool{}, 0) {
				return true
			}
			if !ok && blockMappingReceiverExprIsProvenNonBatch(selector.X, body, parameters, source, map[string]bool{}, 0) {
				return true
			}
			// Unknown .Entries receivers have no proven gocql.Batch lineage.
			// Production code currently has no non-gocql .Entries access, so fail
			// closed instead of allowing a helper-returned batch to disappear.
			violations++
			return true
		}
		call, ok := parents[selector].(*ast.CallExpr)
		if ok && len(call.Args) == 1 && call.Args[0] == selector {
			if name, ok := call.Fun.(*ast.Ident); ok && name.Name == "len" {
				return true
			}
		}
		violations++
		return true
	})
	return violations
}

func blockMappingAuthorityParse(t *testing.T, path string) *ast.File {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("PCD1B3 MAPPING AUTHORITY: parse %s: %v", path, err)
	}
	return parsed
}

// TestBlockMappingAuthorityClaimsAreImmutableRepositoryWide freezes every
// production/schema reference to the claim table: one INSERT IF NOT EXISTS and
// one SELECT in the primitive, one CREATE TABLE without TTL, nothing else. In
// particular no mapping delete or GC path can retire or rewrite a claim.
func TestBlockMappingAuthorityClaimsAreImmutableRepositoryWide(t *testing.T) {
	repoRoot := r3RepositoryRoot(t)
	relPrimitive := "internal/db/block_mapping_authority.go"
	var violations []string
	insertCount, selectCount := 0, 0
	for _, path := range identityAuthorityProductionGoFiles(t, repoRoot) {
		relPath, err := filepath.Rel(repoRoot, path)
		if err != nil {
			t.Fatalf("relative path %s: %v", path, err)
		}
		relPath = filepath.ToSlash(relPath)
		var joined strings.Builder
		var references []string
		for _, value := range identityAuthorityGoStrings(t, path) {
			joined.WriteString(value)
			if strings.Contains(strings.ToLower(value), blockMappingAuthorityClaimsTable) {
				references = append(references, value)
			}
		}
		if relPath != relPrimitive {
			if strings.Contains(strings.ToLower(joined.String()), blockMappingAuthorityClaimsTable) {
				violations = append(violations, relPath+": unauthorized production reference")
			}
			continue
		}
		for _, statement := range references {
			normalized := strings.TrimSpace(statement)
			switch {
			case blockMappingAuthorityInsertPattern.MatchString(normalized) && !strings.Contains(strings.ToUpper(normalized), "USING TTL"):
				insertCount++
			case blockMappingAuthoritySelectPattern.MatchString(normalized):
				selectCount++
			default:
				violations = append(violations, relPath+": unauthorized operation on "+blockMappingAuthorityClaimsTable)
			}
		}
	}
	if insertCount != 1 || selectCount != 1 {
		violations = append(violations, "runtime INSERT IF NOT EXISTS="+strconv.Itoa(insertCount)+" SELECT="+strconv.Itoa(selectCount)+", want 1/1")
	}

	createCount := 0
	walkErr := filepath.WalkDir(filepath.Join(repoRoot, "internal", "db", "migrations"), func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".cql") {
			return err
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		content = identityAuthorityCQLComments.ReplaceAll(content, nil)
		for _, statement := range strings.Split(string(content), ";") {
			normalized := strings.TrimSpace(statement)
			if !strings.Contains(strings.ToLower(normalized), blockMappingAuthorityClaimsTable) {
				continue
			}
			if !strings.HasSuffix(filepath.ToSlash(path), "/027_block_mapping_authority_claims.cql") ||
				!blockMappingAuthorityCreatePattern.MatchString(normalized) ||
				strings.Contains(strings.ToLower(normalized), "default_time_to_live") {
				violations = append(violations, filepath.ToSlash(path)+": unauthorized schema operation on "+blockMappingAuthorityClaimsTable)
				continue
			}
			createCount++
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk migrations: %v", walkErr)
	}
	if createCount != 1 {
		violations = append(violations, "schema CREATE TABLE count="+strconv.Itoa(createCount)+", want 1")
	}
	sort.Strings(violations)
	if len(violations) > 0 {
		t.Fatalf("PCD1B3 MAPPING AUTHORITY IMMUTABILITY: %v", violations)
	}
}

// TestBlockMappingAuthorityAcquisitionIsColdPathOnly is the performance gate:
// the upload hot path keeps its plain read-before-write mapping and gains no
// Paxos. Acquisition symbols may appear only in the primitive and the
// certifier, never in API, sync, GC or upload code.
func TestBlockMappingAuthorityAcquisitionIsColdPathOnly(t *testing.T) {
	repoRoot := r3RepositoryRoot(t)
	var violations []string
	integrationPath := filepath.Join(repoRoot, "internal", "db", "block_mapping_authority_integration.go")
	integrationSource, integrationReadErr := os.ReadFile(integrationPath)
	if integrationReadErr != nil {
		t.Fatalf("read integration claim capability: %v", integrationReadErr)
	}
	firstLine := strings.SplitN(string(integrationSource), "\n", 2)[0]
	buildConstraint, constraintErr := constraint.Parse(firstLine)
	if constraintErr != nil {
		violations = append(violations, "integration claim capability has no pinned //go:build integration constraint")
	} else if tag, ok := buildConstraint.(*constraint.TagExpr); !ok || tag.Tag != "integration" {
		violations = append(violations, "integration claim capability build constraint must be exactly //go:build integration")
	}
	freezeDefinitions := 0
	freezeDirectCalls := 0
	claimDefinitions := 0
	claimProductionCalls := 0
	claimIntegrationCalls := 0
	acquireDefinitions := 0
	acquireCalls := 0
	promotionPortsDefinitions := 0
	promotionPortsCalls := 0
	promotionHelperCalls := 0
	for _, path := range identityAuthorityProductionGoFiles(t, repoRoot) {
		relPath, err := filepath.Rel(repoRoot, path)
		if err != nil {
			t.Fatalf("relative path %s: %v", path, err)
		}
		relPath = filepath.ToSlash(relPath)
		parsed := blockMappingAuthorityParse(t, path)
		parents := blockMappingParentMap(parsed)
		ast.Inspect(parsed, func(node ast.Node) bool {
			if function, ok := node.(*ast.FuncDecl); ok {
				switch function.Name.Name {
				case "claimBlockMappingAuthority":
					claimDefinitions++
					if relPath != "internal/db/block_mapping_authority.go" {
						violations = append(violations, relPath+": unauthorized claimBlockMappingAuthority definition")
					}
				case "acquireBlockMappingClaim":
					acquireDefinitions++
					if relPath != "internal/db/block_mapping_authority.go" {
						violations = append(violations, relPath+": unauthorized acquireBlockMappingClaim definition")
					}
				}
			}
			if function, ok := node.(*ast.FuncDecl); ok && function.Name.Name == "freezeBlockMappingProjection" {
				freezeDefinitions++
				if relPath != "internal/db/block_mapping_authority.go" {
					violations = append(violations, relPath+": unauthorized freezeBlockMappingProjection definition")
				}
			}
			if function, ok := node.(*ast.FuncDecl); ok && function.Name.Name == "blockMappingPromotionPorts" {
				promotionPortsDefinitions++
				if relPath != "internal/db/block_mapping_authority.go" {
					violations = append(violations, relPath+": unauthorized blockMappingPromotionPorts definition")
				}
			}
			if call, ok := node.(*ast.CallExpr); ok && blockMappingCallName(call.Fun) == "freezeBlockMappingProjection" {
				freezeDirectCalls++
				owner := blockMappingEnclosingFunctionName(parents, call)
				if relPath != "internal/db/block_mapping_authority.go" || owner != "blockMappingPromotionPorts" {
					violations = append(violations, relPath+": freezeBlockMappingProjection direct caller must be blockMappingPromotionPorts, found "+owner)
				}
			}
			if call, ok := node.(*ast.CallExpr); ok {
				owner := blockMappingEnclosingFunctionName(parents, call)
				switch blockMappingCallName(call.Fun) {
				case "claimBlockMappingAuthority":
					if relPath == "internal/db/block_mapping_authority_integration.go" {
						claimIntegrationCalls++
						if owner != "ClaimBlockMappingAuthorityForIntegration" {
							violations = append(violations, relPath+": integration claim caller must be ClaimBlockMappingAuthorityForIntegration, found "+owner)
						}
					} else {
						claimProductionCalls++
						if relPath != "internal/db/block_mapping_authority.go" || owner != "blockMappingPromotionPorts" {
							violations = append(violations, relPath+": claimBlockMappingAuthority direct caller must be blockMappingPromotionPorts, found "+owner)
						}
					}
				case "acquireBlockMappingClaim":
					acquireCalls++
					if relPath != "internal/db/block_mapping_authority.go" || owner != "promoteBlockMappingAuthority" {
						violations = append(violations, relPath+": acquireBlockMappingClaim direct caller must be promoteBlockMappingAuthority, found "+owner)
					}
				case "blockMappingPromotionPorts":
					promotionPortsCalls++
					if relPath != "internal/db/block_mapping_authority.go" || owner != "PromoteBlockMappingAuthority" {
						violations = append(violations, relPath+": blockMappingPromotionPorts may only be called by PromoteBlockMappingAuthority, found "+owner)
					}
					parent, directArgument := parents[call].(*ast.CallExpr)
					if !directArgument || blockMappingCallName(parent.Fun) != "promoteBlockMappingAuthority" || len(parent.Args) < 2 || parent.Args[1] != call {
						violations = append(violations, relPath+": blockMappingPromotionPorts result must feed directly into promoteBlockMappingAuthority")
					}
				case "promoteBlockMappingAuthority":
					promotionHelperCalls++
					if relPath != "internal/db/block_mapping_authority.go" || owner != "PromoteBlockMappingAuthority" {
						violations = append(violations, relPath+": promoteBlockMappingAuthority production caller must be PromoteBlockMappingAuthority, found "+owner)
					}
				}
			}
			var name string
			switch value := node.(type) {
			case *ast.Ident:
				if selector, selectorName := parents[value].(*ast.SelectorExpr); selectorName && selector.Sel == value {
					return true
				}
				name = value.Name
			case *ast.SelectorExpr:
				name = value.Sel.Name
			default:
				return true
			}
			if allowed, tracked := blockMappingAuthorityAcquisitionSymbols[name]; tracked && !allowed[relPath] {
				violations = append(violations, relPath+": references "+name)
			}
			if name == "freezeBlockMappingProjection" {
				if _, declarationName := parents[node].(*ast.FuncDecl); declarationName {
					return true
				}
				if call, directCall := parents[node].(*ast.CallExpr); directCall && call.Fun == node {
					return true
				}
				violations = append(violations, relPath+": freezeBlockMappingProjection may not be taken as a function value or aliased")
			}
			if name == "claimBlockMappingAuthority" || name == "acquireBlockMappingClaim" {
				if _, declarationName := parents[node].(*ast.FuncDecl); declarationName {
					return true
				}
				if call, directCall := parents[node].(*ast.CallExpr); directCall && call.Fun == node {
					return true
				}
				violations = append(violations, relPath+": "+name+" may not be taken as a function value or aliased")
			}
			if name == "blockMappingPromotionPorts" || name == "promoteBlockMappingAuthority" {
				if _, isIdentifier := node.(*ast.Ident); isIdentifier && name == "blockMappingPromotionPorts" {
					// The method shares its name with the returned capability type;
					// bare identifiers here are type references, not function values.
					return true
				}
				if _, declarationName := parents[node].(*ast.FuncDecl); declarationName {
					return true
				}
				if call, directCall := parents[node].(*ast.CallExpr); directCall && call.Fun == node {
					return true
				}
				violations = append(violations, relPath+": "+name+" may only be used as its authorized direct call, not as a function value or alias")
			}
			if selector, ok := node.(*ast.SelectorExpr); ok && (selector.Sel.Name == "freeze" || selector.Sel.Name == "claim") && strings.HasPrefix(relPath, "internal/db/") {
				allowedOwner := "promoteBlockMappingAuthority"
				if selector.Sel.Name == "claim" {
					allowedOwner = "acquireBlockMappingClaim"
				}
				call, directCall := parents[node].(*ast.CallExpr)
				directCall = directCall && call.Fun == node
				owner := blockMappingEnclosingFunctionName(parents, node)
				if !directCall || relPath != "internal/db/block_mapping_authority.go" || owner != allowedOwner {
					violations = append(violations, relPath+": blockMappingPromotionPorts ."+selector.Sel.Name+" capability may only be used as a direct call within "+allowedOwner)
				}
			}
			return true
		})
	}
	if freezeDefinitions != 1 {
		violations = append(violations, "freezeBlockMappingProjection definition count="+strconv.Itoa(freezeDefinitions)+", want 1")
	}
	if freezeDirectCalls != 1 {
		violations = append(violations, "freezeBlockMappingProjection direct caller count="+strconv.Itoa(freezeDirectCalls)+", want 1 authorized call")
	}
	if claimDefinitions != 1 {
		violations = append(violations, "claimBlockMappingAuthority production definition count="+strconv.Itoa(claimDefinitions)+", want 1")
	}
	if claimProductionCalls != 1 {
		violations = append(violations, "claimBlockMappingAuthority production direct caller count="+strconv.Itoa(claimProductionCalls)+", want 1 authorized call")
	}
	if claimIntegrationCalls != 1 {
		violations = append(violations, "claimBlockMappingAuthority integration-only direct caller count="+strconv.Itoa(claimIntegrationCalls)+", want 1")
	}
	if acquireDefinitions != 1 {
		violations = append(violations, "acquireBlockMappingClaim production definition count="+strconv.Itoa(acquireDefinitions)+", want 1")
	}
	if acquireCalls != 1 {
		violations = append(violations, "acquireBlockMappingClaim production direct caller count="+strconv.Itoa(acquireCalls)+", want 1 authorized call")
	}
	if promotionPortsDefinitions != 1 {
		violations = append(violations, "blockMappingPromotionPorts definition count="+strconv.Itoa(promotionPortsDefinitions)+", want 1")
	}
	if promotionPortsCalls != 1 {
		violations = append(violations, "blockMappingPromotionPorts productive caller count="+strconv.Itoa(promotionPortsCalls)+", want 1")
	}
	if promotionHelperCalls != 1 {
		violations = append(violations, "promoteBlockMappingAuthority production caller count="+strconv.Itoa(promotionHelperCalls)+", want 1")
	}

	source := filepath.Join(repoRoot, "internal", "db", "block_references.go")
	parsed := blockMappingAuthorityParse(t, source)
	hotPath := map[string]bool{
		"WriteBlockIDMapping":                 true,
		"WriteVerifiedWebBlockMapping":        true,
		"writeCheckedBlockIDMapping":          true,
		"insertBlockIDMappingForWriteCheckFn": true,
		"getBlockIDMappingForWriteCheckFn":    true,
		"getBlockIDMappingForWriteCheck":      true,
	}
	seen := map[string]bool{}
	checkHotPath := func(name string, body ast.Node) {
		seen[name] = true
		ast.Inspect(body, func(node ast.Node) bool {
			switch value := node.(type) {
			case *ast.SelectorExpr:
				if value.Sel.Name == "SerialConsistency" || blockMappingCASTerminals[value.Sel.Name] {
					violations = append(violations, "upload mapping writer "+name+" runs an LWT ("+value.Sel.Name+")")
				}
			case *ast.BasicLit:
				if text, ok := constantIdentityAuthorityString(value); ok {
					lower := strings.ToLower(text)
					if blockMappingCQLConditionalPattern.MatchString(text) || strings.Contains(lower, blockMappingAuthorityClaimsTable) {
						violations = append(violations, "upload mapping writer "+name+" issues conditional/authority CQL")
					}
				}
			}
			return true
		})
	}
	for _, decl := range parsed.Decls {
		switch value := decl.(type) {
		case *ast.FuncDecl:
			if hotPath[value.Name.Name] && value.Body != nil {
				checkHotPath(value.Name.Name, value.Body)
			}
		case *ast.GenDecl:
			for _, spec := range value.Specs {
				valueSpec, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for index, name := range valueSpec.Names {
					if hotPath[name.Name] && index < len(valueSpec.Values) {
						checkHotPath(name.Name, valueSpec.Values[index])
					}
				}
			}
		}
	}
	for name := range hotPath {
		if !seen[name] {
			violations = append(violations, "hot-path mapping writer "+name+" not found; this guard is vacuous")
		}
	}
	violations = append(violations, blockMappingHotPathNoPaxosViolations(t, repoRoot, hotPath)...)
	sort.Strings(violations)
	if len(violations) > 0 {
		t.Fatalf("PCD1B3 MAPPING AUTHORITY HOT PATH: %v", violations)
	}
}

// TestBlockMappingAuthorityPinsGlobalSerial requires the claim and its read to
// use the explicit global SERIAL constants, never the configurable session
// default that a multi-DC deployment may set to LOCAL_SERIAL.
func TestBlockMappingAuthorityPinsGlobalSerial(t *testing.T) {
	sourceBytes, err := os.ReadFile(filepath.Join(r3RepositoryRoot(t), "internal", "db", "block_mapping_authority.go"))
	if err != nil {
		t.Fatalf("read primitive: %v", err)
	}
	source := string(sourceBytes)
	claim := blockMappingAuthorityFuncBody(t, source, "func claimBlockMappingAuthority(")
	if !strings.Contains(claim, "SerialConsistency(LibraryHeadSerialConsistency)") || strings.Contains(claim, "LocalSerial") {
		t.Fatal("mapping authority claim must pin global SERIAL explicitly")
	}
	read := blockMappingAuthorityFuncBody(t, source, "func readBlockMappingAuthority(")
	if !strings.Contains(read, "\tConsistency(IdentityAuthorityReadConsistency)") || strings.Contains(read, "LocalSerial") {
		t.Fatal("mapping authority read must use the global SERIAL read consistency")
	}
	if LibraryHeadSerialConsistency.String() != "SERIAL" || IdentityAuthorityReadConsistency.String() != "SERIAL" {
		t.Fatalf("mapping authority serial domain = %s/%s, want SERIAL/SERIAL", LibraryHeadSerialConsistency, IdentityAuthorityReadConsistency)
	}
	freeze := blockMappingAuthorityFuncBody(t, source, "func freezeBlockMappingProjection(")
	if !strings.Contains(freeze, "UPDATE block_id_mappings USING TIMESTAMP ? SET internal_id = ?") ||
		!strings.Contains(freeze, "BlockMappingProjectionFrozenTimestamp, authority,") ||
		!strings.Contains(freeze, "Consistency(gocql.EachQuorum)") {
		t.Fatal("projection freeze must rewrite the ordinary row at the dominant frozen timestamp and EACH_QUORUM")
	}
	projection := blockMappingAuthorityFuncBody(t, source, "func readBlockMappingProjection(")
	if !strings.Contains(projection, "WRITETIME(internal_id)") || !strings.Contains(projection, "Consistency(gocql.EachQuorum)") {
		t.Fatal("projection reads must observe the write timestamp at EACH_QUORUM")
	}
	promoteBody := blockMappingAuthorityFuncBody(t, source, "func promoteBlockMappingAuthority(")
	if !strings.Contains(promoteBody, "ports.freeze(ctx, identity, authority)") {
		t.Fatal("promotion must freeze the ordinary projection to the durable authority")
	}
	promote := blockMappingAuthorityFuncBody(t, source, "func acquireBlockMappingClaim(")
	proveAt := strings.Index(promote, "ports.prove(")
	claimAt := strings.Index(promote, "ports.claim(")
	if proveAt < 0 || claimAt < 0 || proveAt > claimAt {
		t.Fatal("promotion must obtain provenance before it claims")
	}
}

// TestBlockMappingProjectionReadsPinLocalQuorum protects the reader side of
// the temporal projection. EACH_QUORUM freezes a quorum in each DC, which
// cannot prevent a ONE read from selecting a stale replica in an RF>1 DC.
func TestBlockMappingProjectionReadsPinLocalQuorum(t *testing.T) {
	repoRoot := r3RepositoryRoot(t)
	source, err := os.ReadFile(filepath.Join(repoRoot, "internal", "db", "block_references.go"))
	if err != nil {
		t.Fatalf("read mapping reader: %v", err)
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), "internal/db/block_references.go", source, 0)
	if err != nil {
		t.Fatalf("parse mapping reader: %v", err)
	}
	foundReader, pinsConsistency := false, false
	for _, declaration := range parsed.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != "GetBlockIDMappingContext" || function.Body == nil {
			continue
		}
		foundReader = true
		ast.Inspect(function.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || blockMappingCallName(call.Fun) != "Query" || len(call.Args) == 0 {
				return true
			}
			literal, ok := call.Args[0].(*ast.BasicLit)
			if !ok {
				return true
			}
			query, ok := constantIdentityAuthorityString(literal)
			if !ok || !blockMappingSelectPattern.MatchString(strings.Join(strings.Fields(query), " ")) {
				return true
			}
			consistencies := blockMappingQueryConsistencyNames(function.Body, call)
			pinsConsistency = len(consistencies) == 1 && consistencies[0] == "BlockMappingProjectionReadConsistency"
			return true
		})
	}
	if !foundReader {
		t.Fatal("GetBlockIDMappingContext was not found; projection reader contract would be vacuous")
	}
	if !pinsConsistency {
		t.Fatal("productive block mapping resolution must pin BlockMappingProjectionReadConsistency")
	}
	if BlockMappingProjectionReadConsistency.String() != "LOCAL_QUORUM" {
		t.Fatalf("productive block mapping consistency = %s, want LOCAL_QUORUM", BlockMappingProjectionReadConsistency)
	}
}

func TestBlockMappingAuthoritySerialReadRetriesOnlyAmbiguousCAS(t *testing.T) {
	t.Run("ambiguous result is retried", func(t *testing.T) {
		calls := 0
		err := retryAmbiguousGlobalSerialRead(context.Background(), func() error {
			calls++
			if calls == 1 {
				return &gocql.RequestErrCASWriteUnknown{}
			}
			return nil
		})
		if err != nil || calls != 2 {
			t.Fatalf("ambiguous SERIAL read = %v after %d attempts, want success after 2", err, calls)
		}
	})

	t.Run("other errors fail without retry", func(t *testing.T) {
		wantErr := errors.New("unavailable")
		calls := 0
		err := retryAmbiguousGlobalSerialRead(context.Background(), func() error {
			calls++
			return wantErr
		})
		if !errors.Is(err, wantErr) || calls != 1 {
			t.Fatalf("non-CAS read error = %v after %d attempts, want immediate original error", err, calls)
		}
	})

	t.Run("retry count is bounded", func(t *testing.T) {
		calls := 0
		err := retryAmbiguousGlobalSerialRead(context.Background(), func() error {
			calls++
			return &gocql.RequestErrCASWriteUnknown{}
		})
		if err == nil || calls != blockMappingSerialReadRetryLimit+1 {
			t.Fatalf("persistent ambiguous SERIAL read = %v after %d attempts, want bounded failure after %d", err, calls, blockMappingSerialReadRetryLimit+1)
		}
	})
}

// TestBlockMappingMutationsAreRepositoryWideInventoried resolves production
// Query arguments through constant concatenation, strings.Join, local
// bindings, and same-package helper returns. It inventories every mapping
// mutation and every SELECT reader, including the explicitly pre-GC exception.
func TestBlockMappingMutationsAreRepositoryWideInventoried(t *testing.T) {
	if violations := authorityExecutionInventory(t); len(violations) > 0 {
		t.Fatalf("PCD1B3 BLOCK MAPPING MUTATION INVENTORY: %v", violations)
	}
}

// Share the existing closed-world Query/Batch analysis with identity claims.
// Dynamic classifications and resolver semantics have one source of record.
func authorityExecutionInventory(t *testing.T) []string {
	t.Helper()
	repoRoot := r3RepositoryRoot(t)
	const ordinaryWriter = "insertBlockIDMappingForWriteCheckFn"
	const freezeWriter = "freezeBlockMappingProjection"
	const primitivePath = "internal/db/block_mapping_authority.go"
	const gcReaderMarker = "PCD1B3-PRE-GC-SESSION-CONSISTENCY-EXCEPTION"
	type readerContract struct {
		path               string
		consistency        string
		sessionConsistency bool
		preGC              bool
	}
	readers := map[string]readerContract{
		"GetBlockIDMappingContext": {
			path: "internal/db/block_references.go", consistency: "BlockMappingProjectionReadConsistency",
		},
		"getBlockIDMappingForWriteCheck": {
			path: "internal/db/block_references.go", sessionConsistency: true,
		},
		// Scoped resurrection reader; its consistency is the callback parameter
		// of readHistoricalAdmissionRow (LQ hit / EQ only on clean absence).
		// API source contracts bind the actual query to that callback and reject
		// all direct/overridden pins. This admits one SELECT, never a writer.
		"resolveHistoricalBlockIDs": {
			path: "internal/api/v2/resurrection_admission.go", consistency: "consistency",
		},
		"readBlockMappingProjection": {
			path: primitivePath, consistency: "gocql.EachQuorum",
		},
		"lookupBlockMapping": {
			path: "internal/gc/store_cassandra.go", preGC: true,
		},
	}
	var violations []string
	insertCount, freezeCount := 0, 0
	productionFiles := identityAuthorityProductionGoFiles(t, repoRoot)
	if len(productionFiles) == 0 {
		t.Fatal("scanned no production Go sources; mapping mutation inventory would pass vacuously")
	}
	type parsedProductionFile struct {
		path string
		file *ast.File
	}
	parsedFiles := make([]parsedProductionFile, 0, len(productionFiles))
	packages := map[string]*blockMappingSourceStrings{}
	for _, path := range productionFiles {
		parsed := blockMappingAuthorityParse(t, path)
		parsedFiles = append(parsedFiles, parsedProductionFile{path: path, file: parsed})
		packageRoot := filepath.Dir(path)
		source := packages[packageRoot]
		if source == nil {
			source = &blockMappingSourceStrings{
				functions:               map[string]*ast.FuncDecl{},
				methods:                 map[string][]*ast.FuncDecl{},
				functionPaths:           map[string]string{},
				batchReturningFunctions: map[string]bool{},
				globals:                 map[string]ast.Expr{},
				mutableGlobals:          map[string]bool{},
				dynamicValues:           map[ast.Expr]bool{},
			}
			packages[packageRoot] = source
		}
		relFunctionPath, err := filepath.Rel(repoRoot, path)
		if err != nil {
			t.Fatalf("relative path %s: %v", path, err)
		}
		for _, declaration := range parsed.Decls {
			switch value := declaration.(type) {
			case *ast.FuncDecl:
				if value.Recv == nil {
					source.functions[value.Name.Name] = value
					source.functionPaths[value.Name.Name] = filepath.ToSlash(relFunctionPath)
				} else {
					source.methods[value.Name.Name] = append(source.methods[value.Name.Name], value)
					if source.functions[value.Name.Name] == nil {
						source.functions[value.Name.Name] = value
					}
					if len(source.methods[value.Name.Name]) > 1 {
						delete(source.functions, value.Name.Name)
					}
				}
				if filepath.ToSlash(filepath.Dir(filepath.ToSlash(relFunctionPath))) == "internal/db" && blockMappingFunctionReturnsBatch(value) {
					source.batchReturningFunctions[value.Name.Name] = true
				}
			case *ast.GenDecl:
				if value.Tok == token.VAR {
					for _, spec := range value.Specs {
						if valueSpec, ok := spec.(*ast.ValueSpec); ok {
							for _, name := range valueSpec.Names {
								source.mutableGlobals[name.Name] = true
								delete(source.globals, name.Name)
							}
						}
					}
					continue
				}
				for _, spec := range value.Specs {
					valueSpec, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for index, name := range valueSpec.Names {
						if index < len(valueSpec.Values) {
							source.globals[name.Name] = valueSpec.Values[index]
						}
					}
				}
			}
		}
	}
	readerCounts := map[string]int{}
	readerCallsites := map[string]map[string]bool{}
	claimTableCallsites := map[string]bool{}
	claimInsertExecutionCount, claimSelectExecutionCount := 0, 0
	identitySites := map[string]bool{}
	identityInserts, identitySelects := 0, 0
	insertCallsites := map[string]bool{}
	freezeCallsites := map[string]bool{}
	dynamicQueryCallsites := map[string]bool{}
	dynamicQueryCounts := map[string]int{}
	checkStandaloneCQL := func(relPath, owner, statement string) {
		normalized := strings.Join(strings.Fields(statement), " ")
		if !blockMappingTableMentionPattern.MatchString(normalized) {
			return
		}
		upper := strings.ToUpper(normalized)
		switch {
		case blockMappingSelectPattern.MatchString(normalized):
			violations = append(violations, relPath+": unclassified production block_id_mappings SELECT in "+owner)
		case strings.HasPrefix(upper, "DELETE FROM BLOCK_ID_MAPPINGS"):
			violations = append(violations, relPath+": production DELETE from block_id_mappings is prohibited by R11a")
		case strings.HasPrefix(upper, "INSERT INTO BLOCK_ID_MAPPINGS"):
			if strings.Contains(upper, "USING TIMESTAMP") {
				violations = append(violations, relPath+": ordinary block_id_mappings INSERT must not specify USING TIMESTAMP")
			}
			if relPath != "internal/db/block_references.go" || owner != ordinaryWriter || !blockMappingInsertPattern.MatchString(normalized) {
				violations = append(violations, relPath+": unauthorized or malformed ordinary block_id_mappings INSERT in "+owner)
			} else {
				insertCount++
			}
		case strings.HasPrefix(upper, "UPDATE BLOCK_ID_MAPPINGS"):
			if relPath != primitivePath || owner != freezeWriter || !blockMappingFreezePattern.MatchString(normalized) {
				violations = append(violations, relPath+": only freezeBlockMappingProjection may use explicit-timestamp block_id_mappings UPDATE")
			} else {
				freezeCount++
			}
		default:
			violations = append(violations, relPath+": unrecognized/dynamic block_id_mappings CQL mutation in "+owner)
		}
	}
	checkReader := func(relPath, owner string, query *ast.CallExpr, body ast.Node, statement string) {
		contract, allowed := readers[owner]
		if !allowed || contract.path != relPath {
			violations = append(violations, relPath+": unclassified production block_id_mappings SELECT in "+owner)
			return
		}
		querySite := relPath + ":" + strconv.Itoa(int(query.Pos()))
		if readerCallsites[owner] == nil {
			readerCallsites[owner] = map[string]bool{}
		}
		if !readerCallsites[owner][querySite] {
			readerCallsites[owner][querySite] = true
			readerCounts[owner]++
		}
		consistencies := blockMappingQueryConsistencyNames(body, query)
		if contract.preGC {
			if len(consistencies) != 0 {
				violations = append(violations, relPath+":"+owner+" PRE-GC exception unexpectedly changed its consistency contract")
			}
			if !strings.Contains(statement, "SELECT internal_id FROM block_id_mappings") {
				violations = append(violations, relPath+":"+owner+" PRE-GC query shape changed without reclassification")
			}
			return
		}
		if contract.sessionConsistency {
			if len(consistencies) != 0 {
				violations = append(violations, relPath+":"+owner+" writer pre-check must inherit session consistency, found "+strings.Join(consistencies, ","))
			}
			return
		}
		if len(consistencies) != 1 || consistencies[0] != contract.consistency {
			violations = append(violations, relPath+":"+owner+" block mapping SELECT consistency="+strings.Join(consistencies, ",")+", want "+contract.consistency)
		}
	}

	var checkQuery func(source *blockMappingSourceStrings, node ast.Node, owner, relPath string, body ast.Node, function *ast.FuncDecl, functionLiteral *ast.FuncLit, arguments []ast.Expr, outer map[string]ast.Expr, active map[string]bool)
	checkQuery = func(source *blockMappingSourceStrings, node ast.Node, owner, relPath string, body ast.Node, function *ast.FuncDecl, functionLiteral *ast.FuncLit, arguments []ast.Expr, outer map[string]ast.Expr, active map[string]bool) {
		var parameters *ast.FieldList
		if function != nil {
			parameters = function.Type.Params
		} else if functionLiteral != nil {
			parameters = functionLiteral.Type.Params
		}
		if entries := blockMappingUnclassifiedBatchEntries(body, parameters, source); entries > 0 {
			violations = append(violations, relPath+":"+owner+" directly accesses gocql.Batch.Entries or an unproven .Entries receiver ("+strconv.Itoa(entries)+" sites); classify the batch statements")
		}
		ast.Inspect(node, func(child ast.Node) bool {
			if composite, ok := child.(*ast.CompositeLit); ok && blockMappingIsBatchEntryType(composite.Type) {
				foundStatement := false
				for _, element := range composite.Elts {
					field, ok := element.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					name, ok := field.Key.(*ast.Ident)
					if !ok || name.Name != "Stmt" {
						continue
					}
					foundStatement = true
					entryLocals := source.queryLocalsAt(function, functionLiteral, arguments, outer, field.Value.Pos())
					resolved := source.resolve(field.Value, entryLocals, map[string]bool{}, map[string]bool{}, 0)
					if !resolved.constant {
						violations = append(violations, relPath+":"+owner+" has an unresolved/dynamic gocql.BatchEntry statement")
						continue
					}
					if strings.Contains(strings.ToLower(resolved.text), identityAuthorityClaimsTable) {
						violations = append(violations, relPath+":"+owner+" unauthorized identity BatchEntry execution")
					}
					if blockMappingTableMentionPattern.MatchString(resolved.text) {
						violations = append(violations, relPath+":"+owner+" submits block_id_mappings CQL through gocql.BatchEntry")
					}
				}
				if !foundStatement {
					violations = append(violations, relPath+":"+owner+" builds gocql.BatchEntry without a classifiable Stmt")
				}
			}
			call, ok := child.(*ast.CallExpr)
			if !ok {
				return true
			}
			callName := blockMappingCallName(call.Fun)
			locals := source.queryLocalsAt(function, functionLiteral, arguments, outer, call.Pos())
			if !blockMappingIsCQLEntryPoint(callName, len(call.Args)) {
				var helper *ast.FuncDecl
				if identifier, ok := call.Fun.(*ast.Ident); ok {
					helper = source.functions[identifier.Name]
				}
				if helper != nil && helper.Body != nil && blockMappingHasDirectQuery(helper.Body) && !active[callName] {
					nestedActive := make(map[string]bool, len(active)+1)
					for name, inUse := range active {
						nestedActive[name] = inUse
					}
					nestedActive[callName] = true
					helperPath := source.functionPaths[callName]
					if helperPath == "" {
						helperPath = relPath
					}
					checkQuery(source, helper.Body, callName, helperPath, helper.Body, helper, nil, call.Args, locals, nestedActive)
				}
				return true
			}
			if len(call.Args) == 0 {
				return true
			}
			resolved := source.resolve(call.Args[0], locals, map[string]bool{}, map[string]bool{}, 0)
			normalized := strings.Join(strings.Fields(resolved.text), " ")
			mappingStatement := blockMappingTableMentionPattern.MatchString(normalized)
			if mappingStatement {
				batchMapping := blockMappingBatchContainsMappingCQL(source, body, function, functionLiteral, arguments, outer)
				ast.Inspect(node, func(candidate ast.Node) bool {
					selector, ok := candidate.(*ast.SelectorExpr)
					if !ok || selector.Sel.Name != "WithTimestamp" {
						return true
					}
					if blockMappingQueryObjectContains(body, selector.X, call) || batchMapping {
						violations = append(violations, relPath+":"+owner+" block_id_mappings Query/Batch must not use WithTimestamp")
					}
					return true
				})
			}
			if !resolved.constant {
				if strings.Contains(strings.ToLower(resolved.text), identityAuthorityClaimsTable) {
					violations = append(violations, relPath+":"+owner+" unresolved/dynamic Query may touch "+identityAuthorityClaimsTable)
					return true
				}
				if strings.Contains(strings.ToLower(resolved.text), blockMappingAuthorityClaimsTable) {
					violations = append(violations, relPath+":"+owner+" unresolved/dynamic Query may touch "+blockMappingAuthorityClaimsTable)
					return true
				}
				if blockMappingMigrationQueryAllowlisted(relPath, owner, call.Args[0]) {
					return true
				}
				if blockMappingPotentialDynamicQuery(normalized, call.Args[0]) {
					violations = append(violations, relPath+": unresolved/dynamic block_id_mappings Query argument in "+owner)
					return true
				}
				if contract, allowed := blockMappingDynamicQueryAllowlist[relPath][owner]; allowed {
					if contract.tableMarker != "" && !blockMappingQueryHasFixedTableMarkerAtCall(source, function, functionLiteral, arguments, outer, call, call.Args[0], contract.tableMarker) {
						violations = append(violations, relPath+":"+owner+" dynamic Query lost fixed table marker "+contract.tableMarker+" at this call site")
						return true
					}
					querySite := relPath + ":" + strconv.Itoa(int(call.Pos()))
					if !dynamicQueryCallsites[querySite] {
						dynamicQueryCallsites[querySite] = true
						dynamicQueryCounts[relPath+"::"+owner]++
					}
					return true
				}
				violations = append(violations, relPath+": unresolved/dynamic Query argument in "+owner+"; classify or allowlist this call site")
				return true
			}
			if strings.Contains(strings.ToLower(normalized), identityAuthorityClaimsTable) {
				site := relPath + ":" + strconv.Itoa(int(call.Pos()))
				if !identitySites[site] {
					identitySites[site] = true
					selector, direct := call.Fun.(*ast.SelectorExpr)
					sessionQuery := direct && callName == "Query" && blockMappingReceiverExprIsProvenNonBatch(selector.X, body, parameters, source, map[string]bool{}, 0)
					switch {
					case sessionQuery && relPath == "internal/db/identity_authority.go" && owner == "ClaimIdentityAuthorityAt" && normalized == identityAuthorityExactInsert:
						identityInserts++
					case sessionQuery && relPath == "internal/db/identity_authority.go" && owner == "ReadIdentityAuthority" && normalized == identityAuthorityExactSelect:
						identitySelects++
					default:
						violations = append(violations, relPath+":"+owner+" unauthorized identity execution of "+identityAuthorityClaimsTable)
					}
				}
			}
			claimTableMention := strings.Contains(strings.ToLower(normalized), blockMappingAuthorityClaimsTable)
			if claimTableMention {
				querySite := relPath + ":" + strconv.Itoa(int(call.Pos()))
				if !claimTableCallsites[querySite] {
					claimTableCallsites[querySite] = true
					switch {
					case relPath == primitivePath && owner == "claimBlockMappingAuthority" && blockMappingAuthorityInsertPattern.MatchString(normalized) && !strings.Contains(strings.ToUpper(normalized), "USING TTL"):
						claimInsertExecutionCount++
					case relPath == primitivePath && owner == "readBlockMappingAuthority" && blockMappingAuthoritySelectPattern.MatchString(normalized):
						claimSelectExecutionCount++
					default:
						violations = append(violations, relPath+":"+owner+" unauthorized execution of "+blockMappingAuthorityClaimsTable)
					}
				}
			}
			if !blockMappingTableMentionPattern.MatchString(normalized) {
				return true
			}
			upper := strings.ToUpper(normalized)
			switch {
			case blockMappingSelectPattern.MatchString(normalized):
				checkReader(relPath, owner, call, body, normalized)
			case strings.HasPrefix(upper, "DELETE FROM BLOCK_ID_MAPPINGS"):
				violations = append(violations, relPath+": production DELETE from block_id_mappings is prohibited by R11a")
			case strings.HasPrefix(upper, "INSERT INTO BLOCK_ID_MAPPINGS"):
				if strings.Contains(upper, "USING TIMESTAMP") {
					violations = append(violations, relPath+": ordinary block_id_mappings INSERT must not specify USING TIMESTAMP")
				}
				if relPath != "internal/db/block_references.go" || owner != ordinaryWriter || !blockMappingInsertPattern.MatchString(normalized) {
					violations = append(violations, relPath+": unauthorized or malformed ordinary block_id_mappings INSERT in "+owner)
				} else {
					querySite := relPath + ":" + strconv.Itoa(int(call.Pos()))
					if !insertCallsites[querySite] {
						insertCallsites[querySite] = true
						insertCount++
					}
				}
			case strings.HasPrefix(upper, "UPDATE BLOCK_ID_MAPPINGS"):
				if relPath != primitivePath || owner != freezeWriter || !blockMappingFreezePattern.MatchString(normalized) {
					violations = append(violations, relPath+": only freezeBlockMappingProjection may use explicit-timestamp block_id_mappings UPDATE")
				} else {
					querySite := relPath + ":" + strconv.Itoa(int(call.Pos()))
					if !freezeCallsites[querySite] {
						freezeCallsites[querySite] = true
						freezeCount++
					}
				}
			default:
				violations = append(violations, relPath+": unrecognized/dynamic block_id_mappings CQL mutation in "+owner)
			}
			return true
		})
	}

	for _, parsedSource := range parsedFiles {
		path := parsedSource.path
		relPath, err := filepath.Rel(repoRoot, path)
		if err != nil {
			t.Fatalf("relative path %s: %v", path, err)
		}
		relPath = filepath.ToSlash(relPath)
		parsed := parsedSource.file
		source := packages[filepath.Dir(path)]
		parents := blockMappingParentMap(parsed)
		ast.Inspect(parsed, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if !ok || (selector.Sel.Name != "Query" && selector.Sel.Name != "Bind") {
				return true
			}
			if selector.Sel.Name == "Query" {
				if pointer, isType := parents[selector].(*ast.StarExpr); isType && pointer.X == selector {
					return true
				}
			}
			call, directCall := parents[selector].(*ast.CallExpr)
			if directCall && call.Fun == selector {
				if selector.Sel.Name == "Bind" && !blockMappingIsCQLEntryPoint("Bind", len(call.Args)) {
					return true
				}
				return true
			}
			if selector.Sel.Name == "Bind" {
				violations = append(violations, relPath+": Batch.Bind method values/aliases are forbidden because their CQL call site cannot be inventoried")
			} else {
				violations = append(violations, relPath+": Session.Query method values/aliases are forbidden because their CQL call site cannot be inventoried at AST position "+strconv.Itoa(int(selector.Pos())))
			}
			return true
		})
		for _, declaration := range parsed.Decls {
			switch value := declaration.(type) {
			case *ast.FuncDecl:
				checkQuery(source, value.Body, value.Name.Name, relPath, value.Body, value, nil, nil, source.globals, map[string]bool{value.Name.Name: true})
			case *ast.GenDecl:
				for _, spec := range value.Specs {
					if valueSpec, ok := spec.(*ast.ValueSpec); ok {
						for index, expression := range valueSpec.Values {
							owner := ""
							if index < len(valueSpec.Names) {
								owner = valueSpec.Names[index].Name
							}
							if functionLiteral, ok := expression.(*ast.FuncLit); ok {
								checkQuery(source, functionLiteral.Body, owner, relPath, functionLiteral.Body, nil, functionLiteral, nil, source.globals, map[string]bool{})
							} else {
								resolved := source.resolve(expression, source.globals, map[string]bool{}, map[string]bool{}, 0)
								checkStandaloneCQL(relPath, owner, resolved.text)
								checkQuery(source, expression, owner, relPath, expression, nil, nil, nil, source.globals, map[string]bool{})
							}
						}
					}
				}
			}
		}
	}
	for owner, contract := range readers {
		if readerCounts[owner] != 1 {
			violations = append(violations, contract.path+":"+owner+" SELECT inventory count="+strconv.Itoa(readerCounts[owner])+", want 1")
		}
	}
	if identityInserts != 1 || identitySelects != 1 {
		violations = append(violations, "identity_authority_claims execution inventory INSERT="+strconv.Itoa(identityInserts)+" SELECT="+strconv.Itoa(identitySelects)+", want 1/1")
	}
	if claimInsertExecutionCount != 1 || claimSelectExecutionCount != 1 {
		violations = append(violations, "block_mapping_authority_claims execution inventory INSERT="+strconv.Itoa(claimInsertExecutionCount)+" SELECT="+strconv.Itoa(claimSelectExecutionCount)+", want 1/1")
	}
	for relPath, owners := range blockMappingDynamicQueryAllowlist {
		for owner, contract := range owners {
			key := relPath + "::" + owner
			if actual := dynamicQueryCounts[key]; actual != contract.count {
				violations = append(violations, relPath+":"+owner+" dynamic Query inventory count="+strconv.Itoa(actual)+", want "+strconv.Itoa(contract.count))
			}
			delete(dynamicQueryCounts, key)
		}
	}
	for key, count := range dynamicQueryCounts {
		violations = append(violations, key+" dynamic Query inventory count="+strconv.Itoa(count)+" is not classified")
	}
	lockSourcePath := filepath.Join(repoRoot, "internal", "gc", "store_cassandra.go")
	lockSource := blockMappingAuthorityParse(t, lockSourcePath)
	lockCalls := map[string]map[string]int{
		"acquireHardDeleteLock": {},
		"renewHardDeleteLock":   {},
		"releaseHardDeleteLock": {},
	}
	ast.Inspect(lockSource, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		name, tracked := lockCalls[blockMappingCallName(call.Fun)]
		if !tracked {
			return true
		}
		if len(call.Args) < 3 {
			violations = append(violations, "internal/gc/store_cassandra.go: hard-delete lock helper has no literal table identity")
			return true
		}
		table, tableOK := call.Args[1].(*ast.BasicLit)
		column, columnOK := call.Args[2].(*ast.BasicLit)
		if !tableOK || !columnOK {
			violations = append(violations, "internal/gc/store_cassandra.go: hard-delete lock table and key column must be literals")
			return true
		}
		tableName, tableErr := strconv.Unquote(table.Value)
		columnName, columnErr := strconv.Unquote(column.Value)
		if tableErr != nil || columnErr != nil {
			violations = append(violations, "internal/gc/store_cassandra.go: invalid hard-delete lock table identity literal")
			return true
		}
		identity := tableName + "/" + columnName
		allowed := map[string]bool{
			"gc_user_hard_delete_locks/user_id": true,
			"gc_org_hard_delete_locks/org_id":   true,
		}
		if !allowed[identity] {
			violations = append(violations, "internal/gc/store_cassandra.go: unclassified hard-delete lock table identity "+identity)
		}
		name[identity]++
		return true
	})
	for name, identities := range lockCalls {
		for _, identity := range []string{
			"gc_user_hard_delete_locks/user_id",
			"gc_org_hard_delete_locks/org_id",
		} {
			if actual := identities[identity]; actual != 1 {
				violations = append(violations, "internal/gc/store_cassandra.go:"+name+" "+identity+" call count="+strconv.Itoa(actual)+", want 1")
			}
		}
	}
	gcSource, err := os.ReadFile(filepath.Join(repoRoot, "internal", "gc", "store_cassandra.go"))
	if err != nil {
		t.Fatalf("read pre-GC reader exception: %v", err)
	}
	if !strings.Contains(string(gcSource), gcReaderMarker) {
		violations = append(violations, "GC mapping reader is missing its explicit PRE-GC session-consistency exception marker")
	}
	gcContract, err := os.ReadFile(filepath.Join(repoRoot, "docs", "PC-D1B-METADATA-IDENTITY-AUTHORITY.md"))
	if err != nil {
		t.Fatalf("read GC activation contract: %v", err)
	}
	if !strings.Contains(string(gcContract), "GC_ENABLED=false") {
		violations = append(violations, "PRE-GC mapping reader exception no longer documents GC_ENABLED=false")
	}
	if insertCount != 1 {
		violations = append(violations, "ordinary block_id_mappings INSERT inventory count="+strconv.Itoa(insertCount)+", want 1")
	}
	if freezeCount != 1 {
		violations = append(violations, "dominant-timestamp block_id_mappings freeze inventory count="+strconv.Itoa(freezeCount)+", want 1")
	}
	sort.Strings(violations)
	return violations
}

func blockMappingAuthorityFuncBody(t *testing.T, source, signature string) string {
	t.Helper()
	start := strings.Index(source, signature)
	if start < 0 {
		t.Fatalf("%s not found", signature)
	}
	body := source[start:]
	if next := strings.Index(body[len(signature):], "\nfunc "); next >= 0 {
		body = body[:len(signature)+next]
	}
	return body
}
