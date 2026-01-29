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

	a := NewAnalyzer(pass, v.Scopes(), v.Calls())
	a.Analyze()

	for _, e := range a.Errors() {
		e.Report(pass)
	}

	return nil, nil
}

type Analyzer struct {
	errors []LintError
	pass   *analysis.Pass
	scopes map[FQN]*Scopes
	calls  map[FQN][]FQN
}

func NewAnalyzer(pass *analysis.Pass, scopes map[FQN]*Scopes, calls map[FQN][]FQN) *Analyzer {
	return &Analyzer{
		pass:   pass,
		scopes: scopes,
		calls:  calls,
	}
}

func (a *Analyzer) Errors() []LintError {
	return a.errors
}

func (a *Analyzer) Analyze() {
	for _, s := range a.scopes {
		for _, seq := range s.Scopes() {
			for _, n := range seq.Nodes() {
				a.ContainsLock(n, seq)
			}
		}
	}
}

func (a *Analyzer) ContainsLock(n ast.Node, seq *MutexScope) {
	switch sty := n.(type) {
	case *ast.ExprStmt:
		a.ContainsLock(sty.X, seq)
	case *ast.CallExpr:
		a.checkLockToSequenceMutex(seq, sty)
		a.checkCallToFuncWhichLocksSameMutex(seq, sty)
	}
}

func (a *Analyzer) checkCallToFuncWhichLocksSameMutex(seq *MutexScope, callExpr *ast.CallExpr) {
	pkg, name, ok := GetCallInfo(callExpr, a.pass.TypesInfo)

	if ok {
		fqn := FromCallInfo(pkg, name)

		if a.hasTransitiveCall(fqn, seq, make(map[FQN]bool)) {
			a.recordError(seq.Pos(), callExpr.Pos())
		}
	}
}

func (a *Analyzer) hasAnyMutexScopeWithSameSelector(fqn FQN, seq *MutexScope) bool {
	mutexScopes, ok := a.scopes[fqn]

	if !ok {
		return false
	}

	for _, currentMutexScope := range mutexScopes.Scopes() {
		if currentMutexScope.IsEqual(seq) {
			return true
		}
	}

	return false
}

func (a *Analyzer) hasTransitiveCall(fqn FQN, seq *MutexScope, checked map[FQN]bool) bool {
	if checked, ok := checked[fqn]; ok {
		return checked
	}

	if hasLock := a.hasAnyMutexScopeWithSameSelector(fqn, seq); hasLock {
		checked[fqn] = hasLock

		return hasLock
	}

	calls, ok := a.calls[fqn]
	if !ok {
		return false
	}

	any := false
	for _, c := range calls {
		any = any || a.hasTransitiveCall(c, seq, checked)
	}

	return any
}

func (a *Analyzer) checkLockToSequenceMutex(seq *MutexScope, callExpr *ast.CallExpr) {
	selector := StrExpr(SubjectForCall(callExpr, []string{"RLock", "Lock"}))

	if selector == seq.Selector() {
		a.recordError(seq.Pos(), callExpr.Pos())
	}
}

func (a *Analyzer) recordError(origin, secondLock token.Pos) {
	originLoc := NewLocation(origin)
	secondLockLoc := NewLocation(secondLock)

	err := NewLintError(originLoc, secondLockLoc)
	a.errors = append(a.errors, err)
}

// GetCallInfo extracts the package path and function name from a call expression.
// Returns (package path, function name, ok).
func GetCallInfo(callExpr *ast.CallExpr, info *types.Info) (string, string, bool) {
	switch fun := callExpr.Fun.(type) {
	case *ast.SelectorExpr:
		// Method call: x.Method() or pkg.Function()
		sel, ok := info.Selections[fun]
		if ok {
			// It's a method call
			recv := sel.Recv()
			if recv == nil {
				return "", "", false
			}
			// Get the package path from the method's receiver type
			obj := sel.Obj()
			if obj == nil {
				return "", "", false
			}
			pkg := obj.Pkg()
			pkgPath := ""
			if pkg != nil {
				pkgPath = pkg.Path()
			}
			// For method calls, include the receiver type name (without package) in the name
			// to match the format from fqn(): "pkg.RecvType:MethodName"
			recvTypeName := getTypeName(recv)
			return pkgPath, recvTypeName + ":" + fun.Sel.Name, true
		}
		// It might be a package-qualified function call
		if ident, ok := fun.X.(*ast.Ident); ok {
			if pkgName, ok := info.Uses[ident].(*types.PkgName); ok {
				return pkgName.Imported().Path(), fun.Sel.Name, true
			}
		}
	case *ast.Ident:
		// Direct function call: Function()
		if obj := info.Uses[fun]; obj != nil {
			if fn, ok := obj.(*types.Func); ok {
				pkg := fn.Pkg()
				pkgPath := ""
				if pkg != nil {
					pkgPath = pkg.Path()
				}
				return pkgPath, fun.Name, true
			}
		}
	}
	return "", "", false
}

// getTypeName extracts just the type name from a types.Type, without the package path.
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
