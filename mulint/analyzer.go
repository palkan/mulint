package mulint

import (
	"go/ast"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/analysis"
)

var Mulint = &analysis.Analyzer{
	Name: "mulint",
	Doc:  "reports reentrant mutex locks",
	Run:  run,
}

func run(pass *analysis.Pass) (interface{}, error) {
	v := NewVisitor(pass.Pkg, pass.TypesInfo)
	for _, file := range pass.Files {
		ast.Inspect(file, func(n ast.Node) bool {
			v.Visit(n)
			return true
		})
	}

	v.AnalyzeAll()

	a := NewAnalyzer(pass, v.Scopes(), v.Calls())
	a.Analyze()

	for _, e := range a.Errors() {
		e.Report(pass)
	}

	return nil, nil
}

// Analyzer checks for mutex-related issues in collected scopes.
type Analyzer struct {
	errors   []LintError
	pass     *analysis.Pass
	scopes   map[FQN]*LockTracker
	calls    map[FQN][]FQN
	reported map[token.Pos]bool // tracks secondLock positions to avoid duplicates
}

func NewAnalyzer(pass *analysis.Pass, scopes map[FQN]*LockTracker, calls map[FQN][]FQN) *Analyzer {
	return &Analyzer{
		pass:     pass,
		scopes:   scopes,
		calls:    calls,
		reported: make(map[token.Pos]bool),
	}
}

func (a *Analyzer) Errors() []LintError {
	return a.errors
}

// Analyze runs all checks on collected scopes.
func (a *Analyzer) Analyze() {
	a.checkReentrantLocks()
	// Future: a.checkMissingUnlocks()
	// Future: a.checkDoubleUnlocks()
	// Future: a.checkUnlockWithoutLock()
}

// checkReentrantLocks detects attempts to acquire a lock that's already held.
func (a *Analyzer) checkReentrantLocks() {
	for fqn, tracker := range a.scopes {
		for _, scope := range tracker.Scopes() {
			for _, node := range scope.Nodes() {
				a.checkNodeForReentrantLock(node, scope, fqn)
			}
		}
	}
}

func (a *Analyzer) checkNodeForReentrantLock(n ast.Node, scope *MutexScope, currentFQN FQN) {
	// Walk the AST to find all CallExpr nodes within this statement
	ast.Inspect(n, func(node ast.Node) bool {
		if call, ok := node.(*ast.CallExpr); ok {
			a.checkDirectReentrantLock(scope, call)
			a.checkTransitiveReentrantLock(scope, call)
		}
		return true
	})
}

// checkDirectReentrantLock checks if a call is a direct lock on the same mutex.
func (a *Analyzer) checkDirectReentrantLock(scope *MutexScope, call *ast.CallExpr) {
	subject := SubjectForCall(call, lockMethods)
	if subject == nil {
		return
	}

	selector := StrExpr(subject)
	if selector == scope.Selector() {
		a.recordError(scope.Pos(), call.Pos(), scope.Wrapper())
	}
}

// checkTransitiveReentrantLock checks if a call leads to a lock on the same mutex.
func (a *Analyzer) checkTransitiveReentrantLock(scope *MutexScope, call *ast.CallExpr) {
	pkg, name, ok := GetCallInfo(call, a.pass.TypesInfo)
	if !ok {
		return
	}

	// Skip if call is on a different receiver instance
	if a.isCallOnDifferentReceiver(call, scope) {
		return
	}

	fqn := FromCallInfo(pkg, name)
	if a.hasTransitiveLock(fqn, scope, make(map[FQN]bool)) {
		a.recordError(scope.Pos(), call.Pos(), scope.Wrapper())
	}
}

// isCallOnDifferentReceiver checks if a method call is on a different receiver
// than the one used in the mutex scope.
func (a *Analyzer) isCallOnDifferentReceiver(call *ast.CallExpr, scope *MutexScope) bool {
	selector := SelectorExpr(call)
	if selector == nil {
		return false
	}

	callReceiver := RootSelector(selector)
	if callReceiver == nil {
		return false
	}

	scopeRoot, _ := SplitSelector(scope.Selector())
	if scopeRoot == "" {
		return false
	}

	return callReceiver.Name != scopeRoot
}

// hasTransitiveLock checks if a function (or its callees) locks the same mutex.
func (a *Analyzer) hasTransitiveLock(fqn FQN, scope *MutexScope, checked map[FQN]bool) bool {
	if result, ok := checked[fqn]; ok {
		return result
	}

	// Check if this function directly locks the same mutex
	if tracker, ok := a.scopes[fqn]; ok {
		for _, s := range tracker.Scopes() {
			if s.HasSameSelector(scope) {
				checked[fqn] = true
				return true
			}
		}
	}

	// Check callees recursively
	calls, ok := a.calls[fqn]
	if !ok {
		checked[fqn] = false
		return false
	}

	for _, callee := range calls {
		if a.hasTransitiveLock(callee, scope, checked) {
			checked[fqn] = true
			return true
		}
	}

	checked[fqn] = false
	return false
}

func (a *Analyzer) recordError(origin, secondLock token.Pos, wrapper *WrapperInfo) {
	// Deduplicate errors by secondLock position
	if a.reported[secondLock] {
		return
	}
	a.reported[secondLock] = true

	var err LintError
	if wrapper != nil {
		err = NewLintErrorWithWrapper(NewLocation(origin), NewLocation(secondLock), wrapper)
	} else {
		err = NewLintError(NewLocation(origin), NewLocation(secondLock))
	}
	a.errors = append(a.errors, err)
}

// GetCallInfo extracts the package path and function name from a call expression.
func GetCallInfo(call *ast.CallExpr, info *types.Info) (string, string, bool) {
	switch fun := call.Fun.(type) {
	case *ast.SelectorExpr:
		// Method call: x.Method() or pkg.Function()
		if sel, ok := info.Selections[fun]; ok {
			recv := sel.Recv()
			obj := sel.Obj()
			if recv == nil || obj == nil {
				return "", "", false
			}
			pkgPath := ""
			if pkg := obj.Pkg(); pkg != nil {
				pkgPath = pkg.Path()
			}
			recvTypeName := getTypeName(recv)
			return pkgPath, recvTypeName + ":" + fun.Sel.Name, true
		}
		// Package-qualified function call
		if ident, ok := fun.X.(*ast.Ident); ok {
			if pkgName, ok := info.Uses[ident].(*types.PkgName); ok {
				return pkgName.Imported().Path(), fun.Sel.Name, true
			}
		}
	case *ast.Ident:
		// Direct function call
		if obj := info.Uses[fun]; obj != nil {
			if fn, ok := obj.(*types.Func); ok {
				pkgPath := ""
				if pkg := fn.Pkg(); pkg != nil {
					pkgPath = pkg.Path()
				}
				return pkgPath, fun.Name, true
			}
		}
	}
	return "", "", false
}

// getTypeName extracts just the type name from a types.Type.
func getTypeName(t types.Type) string {
	switch ty := t.(type) {
	case *types.Pointer:
		return getTypeName(ty.Elem())
	case *types.Named:
		return ty.Obj().Name()
	default:
		return t.String()
	}
}
