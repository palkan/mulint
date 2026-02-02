package mulint

import (
	"go/ast"
	"go/token"
	"go/types"
)

// BranchLockInfo tracks a lock's state at a point in code.
type BranchLockInfo struct {
	selector string
	pos      token.Pos
	wrapper  *WrapperInfo
}

// MissingUnlock records a return statement that occurs while a lock is held.
type MissingUnlock struct {
	lockInfo  BranchLockInfo
	returnPos token.Pos
}

// BranchTracker tracks lock state through branching control flow.
// It detects return statements that occur while locks are held.
type BranchTracker struct {
	ongoing map[string]BranchLockInfo
	defers  map[string]bool
	errors  *[]MissingUnlock // Pointer to shared slice for collecting errors

	// For wrapper support
	registry *WrapperRegistry
	typeInfo *types.Info
}

func NewBranchTracker() *BranchTracker {
	errors := make([]MissingUnlock, 0)
	return &BranchTracker{
		ongoing:  make(map[string]BranchLockInfo),
		defers:   make(map[string]bool),
		errors:   &errors,
		registry: nil,
		typeInfo: nil,
	}
}

func NewBranchTrackerWithWrappers(registry *WrapperRegistry, typeInfo *types.Info) *BranchTracker {
	errors := make([]MissingUnlock, 0)
	return &BranchTracker{
		ongoing:  make(map[string]BranchLockInfo),
		defers:   make(map[string]bool),
		errors:   &errors,
		registry: registry,
		typeInfo: typeInfo,
	}
}

// Clone creates a copy of the tracker for branch analysis.
func (t *BranchTracker) Clone() *BranchTracker {
	clone := &BranchTracker{
		ongoing:  make(map[string]BranchLockInfo, len(t.ongoing)),
		defers:   make(map[string]bool, len(t.defers)),
		errors:   t.errors, // Share pointer to collect all errors
		registry: t.registry,
		typeInfo: t.typeInfo,
	}
	for k, v := range t.ongoing {
		clone.ongoing[k] = v
	}
	for k, v := range t.defers {
		clone.defers[k] = v
	}
	return clone
}

// Errors returns all collected missing unlock errors.
func (t *BranchTracker) Errors() []MissingUnlock {
	return *t.errors
}

// AnalyzeStatements analyzes a sequence of statements for missing unlocks.
func (t *BranchTracker) AnalyzeStatements(stmts []ast.Stmt) {
	for _, stmt := range stmts {
		t.analyzeStmt(stmt)
	}
}

func (t *BranchTracker) analyzeStmt(stmt ast.Stmt) {
	// Check for lock acquisition (direct)
	if e := subjectForLockCall(stmt); e != nil {
		selector := StrExpr(e)
		if _, exists := t.ongoing[selector]; !exists {
			t.ongoing[selector] = BranchLockInfo{
				selector: selector,
				pos:      stmt.Pos(),
				wrapper:  nil,
			}
		}
	}

	// Check for wrapper lock call
	t.checkWrapperLockCall(stmt)

	// Check for deferred unlock (direct)
	if e := subjectForDeferUnlockCall(stmt); e != nil {
		selector := StrExpr(e)
		t.defers[selector] = true
	}

	// Check for deferred wrapper unlock
	t.checkDeferredWrapperUnlock(stmt)

	// Check for direct unlock
	if e := subjectForUnlockCall(stmt); e != nil {
		selector := StrExpr(e)
		delete(t.ongoing, selector)
	}

	// Check for wrapper unlock call
	t.checkWrapperUnlockCall(stmt)

	// Check for return statement
	if ret, ok := stmt.(*ast.ReturnStmt); ok {
		t.checkReturnWithLocks(ret)
		return // Don't recurse into return
	}

	// Recurse into nested structures
	t.analyzeNestedStmt(stmt)
}

func (t *BranchTracker) analyzeNestedStmt(stmt ast.Stmt) {
	switch s := stmt.(type) {
	case *ast.IfStmt:
		// Process init statement in current scope
		if s.Init != nil {
			t.analyzeStmt(s.Init)
		}

		// Fork for if body
		ifTracker := t.Clone()
		ifTracker.AnalyzeStatements(s.Body.List)

		// Fork for else body if exists
		if s.Else != nil {
			elseTracker := t.Clone()
			switch e := s.Else.(type) {
			case *ast.BlockStmt:
				elseTracker.AnalyzeStatements(e.List)
			case *ast.IfStmt:
				elseTracker.analyzeStmt(e)
			}
		}

		// After if/else, the lock state is uncertain (could be either branch)
		// We keep the original state since we can't merge branches
		// The errors are already collected in each branch

	case *ast.ForStmt:
		if s.Init != nil {
			t.analyzeStmt(s.Init)
		}
		// Fork for loop body
		loopTracker := t.Clone()
		loopTracker.AnalyzeStatements(s.Body.List)

	case *ast.RangeStmt:
		// Fork for loop body
		loopTracker := t.Clone()
		loopTracker.AnalyzeStatements(s.Body.List)

	case *ast.SwitchStmt:
		if s.Init != nil {
			t.analyzeStmt(s.Init)
		}
		if s.Body != nil {
			for _, clause := range s.Body.List {
				if cc, ok := clause.(*ast.CaseClause); ok {
					caseTracker := t.Clone()
					caseTracker.AnalyzeStatements(cc.Body)
				}
			}
		}

	case *ast.TypeSwitchStmt:
		if s.Init != nil {
			t.analyzeStmt(s.Init)
		}
		if s.Body != nil {
			for _, clause := range s.Body.List {
				if cc, ok := clause.(*ast.CaseClause); ok {
					caseTracker := t.Clone()
					caseTracker.AnalyzeStatements(cc.Body)
				}
			}
		}

	case *ast.SelectStmt:
		if s.Body != nil {
			for _, clause := range s.Body.List {
				if cc, ok := clause.(*ast.CommClause); ok {
					caseTracker := t.Clone()
					caseTracker.AnalyzeStatements(cc.Body)
				}
			}
		}

	case *ast.BlockStmt:
		t.AnalyzeStatements(s.List)
	}
}

// checkReturnWithLocks checks if there are held locks when returning.
func (t *BranchTracker) checkReturnWithLocks(ret *ast.ReturnStmt) {
	for selector, lockInfo := range t.ongoing {
		// Skip if there's a deferred unlock for this lock
		if t.defers[selector] {
			continue
		}
		*t.errors = append(*t.errors, MissingUnlock{
			lockInfo:  lockInfo,
			returnPos: ret.Pos(),
		})
	}
}

// checkWrapperLockCall checks if a statement is a call to a lock wrapper method.
func (t *BranchTracker) checkWrapperLockCall(stmt ast.Stmt) {
	if t.registry == nil || t.typeInfo == nil {
		return
	}

	call := CallExpr(stmt)
	if call == nil {
		return
	}

	pkg, name, ok := GetCallInfo(call, t.typeInfo)
	if !ok {
		return
	}

	fqn := FromCallInfo(pkg, name)
	wrapper, isWrapper := t.registry.Get(fqn)
	if !isWrapper || wrapper.Kind != WrapperLock {
		return
	}

	// Get the receiver
	selector := SelectorExpr(call)
	if selector == nil {
		return
	}
	receiver := RootSelector(selector)
	if receiver == nil {
		return
	}

	effectiveSelector := receiver.Name + "." + wrapper.MutexField
	if _, exists := t.ongoing[effectiveSelector]; !exists {
		t.ongoing[effectiveSelector] = BranchLockInfo{
			selector: effectiveSelector,
			pos:      stmt.Pos(),
			wrapper: &WrapperInfo{
				FQN:     wrapper.FQN,
				LockPos: wrapper.LockPos,
			},
		}
	}
}

// checkWrapperUnlockCall checks if a statement is a call to an unlock wrapper method.
func (t *BranchTracker) checkWrapperUnlockCall(stmt ast.Stmt) {
	if t.registry == nil || t.typeInfo == nil {
		return
	}

	call := CallExpr(stmt)
	if call == nil {
		return
	}

	pkg, name, ok := GetCallInfo(call, t.typeInfo)
	if !ok {
		return
	}

	fqn := FromCallInfo(pkg, name)
	wrapper, isWrapper := t.registry.Get(fqn)
	if !isWrapper || wrapper.Kind != WrapperUnlock {
		return
	}

	// Get the receiver
	selector := SelectorExpr(call)
	if selector == nil {
		return
	}
	receiver := RootSelector(selector)
	if receiver == nil {
		return
	}

	effectiveSelector := receiver.Name + "." + wrapper.MutexField
	delete(t.ongoing, effectiveSelector)
}

// checkDeferredWrapperUnlock checks if a statement is a deferred call to an unlock wrapper.
func (t *BranchTracker) checkDeferredWrapperUnlock(stmt ast.Stmt) {
	if t.registry == nil || t.typeInfo == nil {
		return
	}

	deferStmt, ok := stmt.(*ast.DeferStmt)
	if !ok {
		return
	}

	call := deferStmt.Call
	pkg, name, ok := GetCallInfo(call, t.typeInfo)
	if !ok {
		return
	}

	fqn := FromCallInfo(pkg, name)
	wrapper, isWrapper := t.registry.Get(fqn)
	if !isWrapper || wrapper.Kind != WrapperUnlock {
		return
	}

	selector := SelectorExpr(call)
	if selector == nil {
		return
	}
	receiver := RootSelector(selector)
	if receiver == nil {
		return
	}

	effectiveSelector := receiver.Name + "." + wrapper.MutexField
	t.defers[effectiveSelector] = true
}
