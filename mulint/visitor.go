package mulint

import (
	"fmt"
	"go/ast"
	"go/types"
)

// Visitor collects information about mutex operations from AST traversal.
type Visitor struct {
	scopes   map[FQN]*LockTracker
	calls    map[FQN][]FQN
	wrappers *WrapperRegistry
	pkg      *types.Package
	info     *types.Info
	funcs    []*ast.FuncDecl
}

func NewVisitor(pkg *types.Package, info *types.Info) *Visitor {
	return &Visitor{
		scopes:   make(map[FQN]*LockTracker),
		calls:    make(map[FQN][]FQN),
		wrappers: NewWrapperRegistry(),
		pkg:      pkg,
		info:     info,
		funcs:    make([]*ast.FuncDecl, 0),
	}
}

// Visit collects function declarations for later analysis.
func (v *Visitor) Visit(node ast.Node) ast.Visitor {
	if fn, ok := node.(*ast.FuncDecl); ok && fn.Body != nil {
		v.funcs = append(v.funcs, fn)
	}
	return v
}

// AnalyzeAll performs all analysis passes after AST traversal.
func (v *Visitor) AnalyzeAll() {
	// Pass 1: Analyze bodies for direct locks and collect calls
	for _, fn := range v.funcs {
		fqn := v.funcFQN(fn)
		v.analyzeDirectLocks(fqn, fn.Body)
		v.recordCalls(fqn, fn.Body)
	}

	// Pass 2: Identify wrapper methods from collected scopes
	v.wrappers.IdentifyWrappers(v.scopes, v.funcs, v.funcFQN)

	// Pass 3: Re-analyze bodies without scopes using wrapper awareness
	for _, fn := range v.funcs {
		fqn := v.funcFQN(fn)
		if _, exists := v.scopes[fqn]; exists {
			continue // Already has direct lock scopes
		}

		tracker := v.analyzeWithWrappers(fn.Body)
		if tracker.HasScopes() {
			v.scopes[fqn] = tracker.LockTracker
		}
	}
}

// analyzeDirectLocks analyzes a function body for direct lock/unlock calls.
func (v *Visitor) analyzeDirectLocks(fqn FQN, body *ast.BlockStmt) {
	tracker := NewLockTracker()

	for _, stmt := range body.List {
		tracker.Track(stmt, true)
	}

	tracker.EndBlock()

	if tracker.HasScopes() {
		v.scopes[fqn] = tracker
	}
}

// analyzeWithWrappers analyzes a function body recognizing wrapper method calls.
func (v *Visitor) analyzeWithWrappers(body *ast.BlockStmt) *WrapperAwareTracker {
	tracker := NewWrapperAwareTracker(v.wrappers, v.info)
	tracker.AnalyzeStatements(body.List)
	tracker.EndBlock()
	return tracker
}

// recordCalls records function calls made within a function body.
func (v *Visitor) recordCalls(fqn FQN, body *ast.BlockStmt) {
	for _, stmt := range body.List {
		if call := CallExpr(stmt); call != nil {
			if pkg, name, ok := GetCallInfo(call, v.info); ok {
				calledFQN := FromCallInfo(pkg, name)
				v.addCall(fqn, calledFQN)
			}
		}
	}
}

func (v *Visitor) addCall(from, to FQN) {
	v.calls[from] = append(v.calls[from], to)
}

// funcFQN returns the fully qualified name for a function declaration.
func (v *Visitor) funcFQN(fn *ast.FuncDecl) FQN {
	name := fn.Name.String()
	if fn.Recv != nil {
		typeName := extractTypeName(fn.Recv.List[0].Type)
		name = fmt.Sprintf("%s:%s", typeName, name)
	}
	return FQN(v.pkg.Path() + "." + name)
}

// extractTypeName extracts the type name from a receiver type expression.
func extractTypeName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.StarExpr:
		return extractTypeName(t.X)
	case *ast.SelectorExpr:
		return t.Sel.Name
	case *ast.Ident:
		return t.Name
	}
	return ""
}

// Scopes returns the collected lock scopes.
func (v *Visitor) Scopes() map[FQN]*LockTracker {
	return v.scopes
}

// Calls returns the collected function call graph.
func (v *Visitor) Calls() map[FQN][]FQN {
	return v.calls
}

// Funcs returns the collected function declarations.
func (v *Visitor) Funcs() []*ast.FuncDecl {
	return v.funcs
}

// Wrappers returns the wrapper registry.
func (v *Visitor) Wrappers() *WrapperRegistry {
	return v.wrappers
}
