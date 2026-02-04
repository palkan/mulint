package mulint

import (
	"bytes"
	"go/ast"
	"go/printer"
	"go/token"
	"go/types"
)

// StrExpr converts an AST expression to its string representation.
func StrExpr(e ast.Expr) string {
	var buf bytes.Buffer
	printer.Fprint(&buf, token.NewFileSet(), e)
	return buf.String()
}

// SplitSelector splits a selector string into root and field parts.
// For example, "w.m" returns ("w", "m"), "s.mu" returns ("s", "mu").
func SplitSelector(selector string) (root, field string) {
	for i, c := range selector {
		if c == '.' {
			return selector[:i], selector[i+1:]
		}
	}
	return selector, ""
}

// CallExpr extracts a CallExpr from a node if present.
func CallExpr(node ast.Node) *ast.CallExpr {
	switch n := node.(type) {
	case *ast.CallExpr:
		return n
	case *ast.ExprStmt:
		if call, ok := n.X.(*ast.CallExpr); ok {
			return call
		}
	case *ast.AssignStmt:
		// Handle: v := foo() or v = foo()
		for _, rhs := range n.Rhs {
			if call, ok := rhs.(*ast.CallExpr); ok {
				return call
			}
		}
	}
	return nil
}

// SubjectForCall returns the receiver expression if the node is a call
// to one of the named methods. For example, for "m.Lock()" with names=["Lock"],
// it returns the expression "m".
func SubjectForCall(node ast.Node, names []string) ast.Expr {
	var call *ast.CallExpr

	switch n := node.(type) {
	case *ast.CallExpr:
		call = n
	case *ast.ExprStmt:
		var ok bool
		call, ok = n.X.(*ast.CallExpr)
		if !ok {
			return nil
		}
	default:
		return nil
	}

	selector := SelectorExpr(call)
	if selector == nil {
		return nil
	}

	fnName := selector.Sel.Name
	for _, name := range names {
		if name == fnName {
			return selector.X
		}
	}
	return nil
}

// RootSelector extracts the root identifier from a selector expression.
// For "a.b.c", it returns "a".
func RootSelector(sel *ast.SelectorExpr) *ast.Ident {
	switch x := sel.X.(type) {
	case *ast.SelectorExpr:
		return RootSelector(x)
	case *ast.Ident:
		return x
	}
	return nil
}

// SelectorExpr extracts the SelectorExpr from a call expression's function.
func SelectorExpr(call *ast.CallExpr) *ast.SelectorExpr {
	if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
		return sel
	}
	return nil
}

// IsMutexType checks if the given expression's type is sync.Mutex or sync.RWMutex.
func IsMutexType(expr ast.Expr, info *types.Info) bool {
	if info == nil {
		return true // If no type info, assume it could be a mutex
	}

	t := info.TypeOf(expr)
	if t == nil {
		return true // If type unknown, assume it could be a mutex
	}

	return isMutexTypeName(t)
}

// isMutexTypeName checks if a type is sync.Mutex or sync.RWMutex.
func isMutexTypeName(t types.Type) bool {
	// Handle pointer types
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}

	// Get the named type
	named, ok := t.(*types.Named)
	if !ok {
		return false
	}

	obj := named.Obj()
	if obj == nil || obj.Pkg() == nil {
		return false
	}

	pkgPath := obj.Pkg().Path()
	typeName := obj.Name()

	return pkgPath == "sync" && (typeName == "Mutex" || typeName == "RWMutex")
}
