package mulint

import (
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
)

var Mulint = &analysis.Analyzer{
	Name: "mulint",
	Doc: "reports mutex misuses: reentrant locks and missing unlocks\n\n" +
		"Detects self-deadlocks: acquiring a sync.Mutex or sync.RWMutex that is " +
		"already held, either directly or transitively through function calls, " +
		"and return statements that leave a mutex locked.",
	Run: run,
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

	a := NewAnalyzer(pass, v.Scopes(), v.Calls(), v.Funcs(), v.Wrappers(), v.Conditionals(), pass.TypesInfo)
	a.Analyze()

	for _, e := range a.Errors() {
		e.Report(pass)
	}

	for _, e := range a.MissingUnlockErrors() {
		e.Report(pass)
	}

	return nil, nil
}

// Analyzer checks for mutex-related issues in collected scopes.
type Analyzer struct {
	errors         []LintError
	missingUnlocks []MissingUnlockError
	pass           *analysis.Pass
	scopes         map[FQN]*LockTracker
	calls          map[FQN][]FQN
	reported       map[token.Pos]bool // tracks secondLock positions to avoid duplicates
	funcs          []*ast.FuncDecl
	wrappers       *WrapperRegistry
	conditionals   *ConditionalLockRegistry
	info           *types.Info
}

func NewAnalyzer(pass *analysis.Pass, scopes map[FQN]*LockTracker, calls map[FQN][]FQN, funcs []*ast.FuncDecl, wrappers *WrapperRegistry, conditionals *ConditionalLockRegistry, info *types.Info) *Analyzer {
	return &Analyzer{
		pass:           pass,
		scopes:         scopes,
		calls:          calls,
		reported:       make(map[token.Pos]bool),
		funcs:          funcs,
		wrappers:       wrappers,
		conditionals:   conditionals,
		info:           info,
		missingUnlocks: make([]MissingUnlockError, 0),
	}
}

func (a *Analyzer) Errors() []LintError {
	return a.errors
}

func (a *Analyzer) MissingUnlockErrors() []MissingUnlockError {
	return a.missingUnlocks
}

// Analyze runs all checks on collected scopes.
func (a *Analyzer) Analyze() {
	a.checkReentrantLocks()
	a.checkMissingUnlocks()
	// Future: a.checkDoubleUnlocks()
	// Future: a.checkUnlockWithoutLock()
}

// checkMissingUnlocks detects return statements that occur while a lock is held.
func (a *Analyzer) checkMissingUnlocks() {
	for _, fn := range a.funcs {
		if fn.Body == nil {
			continue
		}

		tracker := NewBranchTrackerWithWrappers(a.wrappers, a.info)
		tracker.SetLocalClosures(AnalyzeLocalClosures(fn.Body, a.info))
		tracker.AnalyzeStatements(fn.Body.List)

		for _, err := range tracker.Errors() {
			// Deduplicate by return position
			if a.reported[err.returnPos] {
				continue
			}
			a.reported[err.returnPos] = true

			var unlockErr MissingUnlockError
			if err.lockInfo.wrapper != nil {
				unlockErr = NewMissingUnlockErrorWithWrapper(
					NewLocation(err.lockInfo.pos),
					NewLocation(err.returnPos),
					err.lockInfo.wrapper,
				)
			} else {
				unlockErr = NewMissingUnlockError(
					NewLocation(err.lockInfo.pos),
					NewLocation(err.returnPos),
				)
			}
			a.missingUnlocks = append(a.missingUnlocks, unlockErr)
		}
	}
}

// checkReentrantLocks detects attempts to acquire a lock that's already held.
func (a *Analyzer) checkReentrantLocks() {
	for fqn, tracker := range a.scopes {
		for _, scope := range tracker.Scopes() {
			safeDefers := a.safeDeferredStmts(scope)
			for _, node := range scope.Nodes() {
				a.checkNodeForReentrantLock(node, scope, fqn, safeDefers)
			}
		}
	}
}

// safeDeferredStmts simulates the defer stack for the scope's mutex: a defer
// registered inside the critical section runs at function exit, not at its
// lexical position, so a Lock inside it is only a self-deadlock if the mutex
// can still be held when the defer fires. A defer is safe when:
//   - the scope is released by a plain Unlock() (the mutex is free before any
//     defers run), or
//   - a deferred unlock of the same mutex is registered AFTER this defer —
//     defers run LIFO, so the unlock fires first.
//
// A deferred unlock registered BEFORE the defer (it would run after — real
// LIFO deadlock) or a scope that is never released keeps the report.
func (a *Analyzer) safeDeferredStmts(scope *MutexScope) map[*ast.DeferStmt]bool {
	var deferStmts []*ast.DeferStmt
	var unlockPositions []token.Pos

	for _, node := range scope.Nodes() {
		ds, ok := node.(*ast.DeferStmt)
		if !ok {
			continue
		}
		deferStmts = append(deferStmts, ds)
		for _, e := range subjectsForDeferUnlockCall(ds) {
			if StrExpr(e) == scope.Selector() {
				unlockPositions = append(unlockPositions, ds.Pos())
			}
		}
	}
	if len(deferStmts) == 0 {
		return nil
	}

	safe := make(map[*ast.DeferStmt]bool)
	for _, ds := range deferStmts {
		if scope.ReleasedDirectly() {
			safe[ds] = true
			continue
		}
		for _, pos := range unlockPositions {
			if pos > ds.Pos() {
				safe[ds] = true
				break
			}
		}
	}
	return safe
}

func (a *Analyzer) checkNodeForReentrantLock(n ast.Node, scope *MutexScope, currentFQN FQN, safeDefers map[*ast.DeferStmt]bool) {
	// Collect func literals that should be skipped from analysis:
	// 1. Func literals passed as arguments to calls - may run asynchronously
	// 2. Func literals that are returned - will be executed by caller after lock is released
	// 3. Func literals assigned to variables - likely returned or called later
	// Note: func literals that are called directly (e.g., defer func(){}()) are NOT skipped.
	skipFuncLits := make(map[*ast.FuncLit]bool)
	ast.Inspect(n, func(node ast.Node) bool {
		if call, ok := node.(*ast.CallExpr); ok {
			for _, arg := range call.Args {
				if funcLit, ok := arg.(*ast.FuncLit); ok {
					skipFuncLits[funcLit] = true
				}
			}
		}
		if ret, ok := node.(*ast.ReturnStmt); ok {
			for _, result := range ret.Results {
				if funcLit, ok := result.(*ast.FuncLit); ok {
					skipFuncLits[funcLit] = true
				}
			}
		}
		if assign, ok := node.(*ast.AssignStmt); ok {
			for _, rhs := range assign.Rhs {
				if funcLit, ok := rhs.(*ast.FuncLit); ok {
					skipFuncLits[funcLit] = true
				}
			}
		}
		return true
	})

	// Walk the AST to find all CallExpr nodes within this statement
	ast.Inspect(n, func(node ast.Node) bool {
		// Skip goroutines - they run asynchronously, lock may be released
		if _, ok := node.(*ast.GoStmt); ok {
			return false
		}
		// Skip defers that provably run after the mutex is released
		if ds, ok := node.(*ast.DeferStmt); ok && safeDefers[ds] {
			return false
		}
		// Skip func literals that are passed as arguments or returned
		if funcLit, ok := node.(*ast.FuncLit); ok {
			if skipFuncLits[funcLit] {
				return false
			}
		}
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

	// Only flag if the receiver is actually a sync.Mutex or sync.RWMutex
	if !IsMutexType(subject, a.info) {
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

	// Check if this is a conditional lock that won't be taken based on arguments
	if a.conditionals.ShouldSkipLock(fqn, call, scope.Selector()) {
		return
	}

	if a.hasTransitiveLock(fqn, scope, make(map[FQN]bool)) {
		a.recordError(scope.Pos(), call.Pos(), scope.Wrapper())
	}
}

// isCallOnDifferentReceiver checks if a method call is on a different receiver
// than the one that owns the mutex in the scope.
//
// The mutex owner is the scope selector minus its final field ("c.mu" -> "c",
// "c.pconn.mu" -> "c.pconn"). A callee can only re-acquire the caller's mutex
// through its own receiver when that receiver is the owner or contains it
// ("c" may reach "c.b.mu"; "c.pconn" cannot reach "c.mu" — its "c.mu" is a
// different object that merely shares receiver and field names).
func (a *Analyzer) isCallOnDifferentReceiver(call *ast.CallExpr, scope *MutexScope) bool {
	selector := SelectorExpr(call)
	if selector == nil {
		return false
	}

	sel := scope.Selector()
	idx := strings.LastIndex(sel, ".")
	if idx < 0 {
		// Package-level mutex: compare against the receiver's root identifier.
		callReceiver := RootSelector(selector)
		if callReceiver == nil {
			return false
		}
		return callReceiver.Name != sel
	}

	ownerPath := sel[:idx]
	recvPath := StrExpr(selector.X)
	return recvPath != ownerPath && !strings.HasPrefix(ownerPath, recvPath+".")
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
