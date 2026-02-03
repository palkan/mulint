package mulint

import (
	"go/ast"
	"go/token"
)

// WrapperInfo contains information about a wrapper method that was used to acquire a lock.
type WrapperInfo struct {
	FQN     FQN       // Fully qualified name of the wrapper method
	LockPos token.Pos // Position of the actual Lock() call inside the wrapper
}

// MutexScope represents a region of code where a mutex is held.
// It tracks the lock position and all statements executed while holding the lock.
type MutexScope struct {
	selector string
	pos      token.Pos
	nodes    []ast.Node
	unlocked bool        // true if the scope was properly unlocked (deferred or direct)
	wrapper  *WrapperInfo // non-nil if the lock was acquired via a wrapper method
}

func NewMutexScope(selector string, pos token.Pos) *MutexScope {
	return &MutexScope{
		selector: selector,
		nodes:    make([]ast.Node, 0),
		pos:      pos,
		unlocked: false,
		wrapper:  nil,
	}
}

// NewMutexScopeWithWrapper creates a scope that was acquired via a wrapper method.
func NewMutexScopeWithWrapper(selector string, pos token.Pos, wrapper *WrapperInfo) *MutexScope {
	return &MutexScope{
		selector: selector,
		nodes:    make([]ast.Node, 0),
		pos:      pos,
		unlocked: false,
		wrapper:  wrapper,
	}
}

func (s *MutexScope) Pos() token.Pos {
	return s.pos
}

func (s *MutexScope) Add(node ast.Node) {
	s.nodes = append(s.nodes, node)
}

func (s *MutexScope) Nodes() []ast.Node {
	return s.nodes
}

func (s *MutexScope) Selector() string {
	return s.selector
}

func (s *MutexScope) HasSameSelector(other *MutexScope) bool {
	return s.selector == other.selector
}

// IsUnlocked returns true if the scope was properly unlocked.
func (s *MutexScope) IsUnlocked() bool {
	return s.unlocked
}

func (s *MutexScope) markUnlocked() {
	s.unlocked = true
}

// Wrapper returns the wrapper info if the lock was acquired via a wrapper, nil otherwise.
func (s *MutexScope) Wrapper() *WrapperInfo {
	return s.wrapper
}

// LockTracker tracks mutex lock/unlock operations within a function body.
// It maintains state about ongoing locks, deferred unlocks, and completed scopes.
type LockTracker struct {
	onGoing  map[string]*MutexScope
	defers   map[string]bool
	finished []*MutexScope

	// For future checks: track unlocks without matching locks
	// unmatchedUnlocks []UnlockInfo
}

func NewLockTracker() *LockTracker {
	return &LockTracker{
		onGoing:  make(map[string]*MutexScope),
		defers:   make(map[string]bool),
		finished: make([]*MutexScope, 0),
	}
}

// Clone creates a copy of the tracker for independent branch analysis.
func (t *LockTracker) Clone() *LockTracker {
	clone := &LockTracker{
		onGoing:  make(map[string]*MutexScope, len(t.onGoing)),
		defers:   make(map[string]bool, len(t.defers)),
		finished: make([]*MutexScope, 0),
	}
	for k, v := range t.onGoing {
		clone.onGoing[k] = v
	}
	for k, v := range t.defers {
		clone.defers[k] = v
	}
	return clone
}

// Track processes a statement for lock/unlock operations.
// If addToOngoing is true, the statement is added to all currently held lock scopes.
func (t *LockTracker) Track(stmt ast.Stmt, addToOngoing bool) {
	// For compound statements, add only the "prefix" parts (init, condition)
	// that execute before any body code, not the entire statement.
	if addToOngoing {
		t.addStatementToOngoing(stmt)
	}

	// Check for lock acquisition
	if e := subjectForLockCall(stmt); e != nil {
		selector := StrExpr(e)
		if _, exists := t.onGoing[selector]; !exists {
			t.onGoing[selector] = NewMutexScope(selector, stmt.Pos())
		}
	}

	// Check for deferred unlock
	if e := subjectForDeferUnlockCall(stmt); e != nil {
		selector := StrExpr(e)
		t.defers[selector] = true
	}

	// Check for unlock
	if e := subjectForUnlockCall(stmt); e != nil {
		selector := StrExpr(e)
		if scope, ok := t.onGoing[selector]; ok {
			scope.markUnlocked()
			t.finished = append(t.finished, scope)
			delete(t.onGoing, selector)
		}
		// Future: else track as unmatched unlock
	}

	// Recurse into nested blocks
	t.trackNestedStatements(stmt, addToOngoing)
}

// addStatementToOngoing adds the appropriate parts of a statement to ongoing scopes.
// For compound statements, only add prefix parts (init, condition) that execute
// before body code, so that unlocks in the body don't affect them.
func (t *LockTracker) addStatementToOngoing(stmt ast.Stmt) {
	switch s := stmt.(type) {
	case *ast.IfStmt:
		// Add init and condition - they execute before the body
		if s.Init != nil {
			t.AddToOngoing(s.Init)
		}
		if s.Cond != nil {
			t.addExprToOngoing(s.Cond)
		}
	case *ast.ForStmt:
		if s.Init != nil {
			t.AddToOngoing(s.Init)
		}
		if s.Cond != nil {
			t.addExprToOngoing(s.Cond)
		}
		// Note: Post executes after body, so we don't add it here
	case *ast.RangeStmt:
		if s.X != nil {
			t.addExprToOngoing(s.X)
		}
	case *ast.SwitchStmt:
		if s.Init != nil {
			t.AddToOngoing(s.Init)
		}
		if s.Tag != nil {
			t.addExprToOngoing(s.Tag)
		}
	case *ast.TypeSwitchStmt:
		if s.Init != nil {
			t.AddToOngoing(s.Init)
		}
		if s.Assign != nil {
			t.AddToOngoing(s.Assign)
		}
	case *ast.SelectStmt:
		// Select has no prefix expressions
	case *ast.BlockStmt:
		// Block has no prefix expressions
	default:
		// Non-compound statements: add the whole thing
		t.AddToOngoing(stmt)
	}
}

// addExprToOngoing wraps an expression and adds it to ongoing scopes.
func (t *LockTracker) addExprToOngoing(expr ast.Expr) {
	for _, scope := range t.onGoing {
		scope.Add(expr)
	}
}

// trackNestedStatements processes statements inside compound statements.
func (t *LockTracker) trackNestedStatements(stmt ast.Stmt, addToOngoing bool) {
	switch s := stmt.(type) {
	case *ast.IfStmt:
		// Track each branch independently to avoid cross-branch contamination
		if s.Body != nil {
			ifTracker := t.Clone()
			for _, inner := range s.Body.List {
				ifTracker.Track(inner, addToOngoing)
			}
			ifTracker.EndBlock()
			t.finished = append(t.finished, ifTracker.finished...)
		}
		if s.Else != nil {
			elseTracker := t.Clone()
			switch e := s.Else.(type) {
			case *ast.BlockStmt:
				for _, inner := range e.List {
					elseTracker.Track(inner, addToOngoing)
				}
			case *ast.IfStmt:
				elseTracker.Track(e, addToOngoing)
			}
			elseTracker.EndBlock()
			t.finished = append(t.finished, elseTracker.finished...)
		}
	case *ast.ForStmt:
		if s.Body != nil {
			for _, inner := range s.Body.List {
				t.Track(inner, addToOngoing)
			}
		}
	case *ast.RangeStmt:
		if s.Body != nil {
			for _, inner := range s.Body.List {
				t.Track(inner, addToOngoing)
			}
		}
	case *ast.SwitchStmt:
		if s.Body != nil {
			for _, clause := range s.Body.List {
				if cc, ok := clause.(*ast.CaseClause); ok {
					// Track each case with a clone to avoid cross-case contamination
					caseTracker := t.Clone()
					for _, inner := range cc.Body {
						caseTracker.Track(inner, addToOngoing)
					}
					// Finalize and merge scopes back
					caseTracker.EndBlock()
					t.finished = append(t.finished, caseTracker.finished...)
				}
			}
		}
	case *ast.TypeSwitchStmt:
		if s.Body != nil {
			for _, clause := range s.Body.List {
				if cc, ok := clause.(*ast.CaseClause); ok {
					caseTracker := t.Clone()
					for _, inner := range cc.Body {
						caseTracker.Track(inner, addToOngoing)
					}
					caseTracker.EndBlock()
					t.finished = append(t.finished, caseTracker.finished...)
				}
			}
		}
	case *ast.SelectStmt:
		if s.Body != nil {
			for _, clause := range s.Body.List {
				if cc, ok := clause.(*ast.CommClause); ok {
					caseTracker := t.Clone()
					for _, inner := range cc.Body {
						caseTracker.Track(inner, addToOngoing)
					}
					caseTracker.EndBlock()
					t.finished = append(t.finished, caseTracker.finished...)
				}
			}
		}
	case *ast.BlockStmt:
		for _, inner := range s.List {
			t.Track(inner, addToOngoing)
		}
	}
}

// isCompoundStatement returns true if the statement contains nested blocks.
func isCompoundStatement(stmt ast.Stmt) bool {
	switch stmt.(type) {
	case *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt,
		*ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt,
		*ast.BlockStmt:
		return true
	}
	return false
}

// AddToOngoing adds a node to all currently held lock scopes.
func (t *LockTracker) AddToOngoing(node ast.Node) {
	for _, scope := range t.onGoing {
		scope.Add(node)
	}
}

// StartLock begins tracking a new lock scope with the given selector.
func (t *LockTracker) StartLock(selector string, pos token.Pos) {
	if _, exists := t.onGoing[selector]; !exists {
		t.onGoing[selector] = NewMutexScope(selector, pos)
	}
}

// StartLockWithWrapper begins tracking a new lock scope acquired via a wrapper method.
func (t *LockTracker) StartLockWithWrapper(selector string, pos token.Pos, wrapper *WrapperInfo) {
	if _, exists := t.onGoing[selector]; !exists {
		t.onGoing[selector] = NewMutexScopeWithWrapper(selector, pos, wrapper)
	}
}

// EndLock finishes a lock scope, moving it to finished.
func (t *LockTracker) EndLock(selector string) {
	if scope, ok := t.onGoing[selector]; ok {
		scope.markUnlocked()
		t.finished = append(t.finished, scope)
		delete(t.onGoing, selector)
	}
}

// AddDeferredUnlock marks a selector as having a deferred unlock.
func (t *LockTracker) AddDeferredUnlock(selector string) {
	t.defers[selector] = true
}

// EndBlock finalizes tracking at block end.
// Processes deferred unlocks and moves remaining locks to finished.
func (t *LockTracker) EndBlock() {
	// Process deferred unlocks - these are properly unlocked
	for selector := range t.defers {
		if scope, ok := t.onGoing[selector]; ok {
			scope.markUnlocked()
			t.finished = append(t.finished, scope)
			delete(t.onGoing, selector)
		}
	}

	// Locks without unlocks still represent lock acquisition
	// (important for transitive deadlock detection)
	// These remain marked as unlocked=false
	for _, scope := range t.onGoing {
		t.finished = append(t.finished, scope)
	}

	// Future: remaining onGoing without defers could be "missing unlock" warnings

	t.onGoing = make(map[string]*MutexScope)
	t.defers = make(map[string]bool)
}

// HasScopes returns true if any lock scopes were tracked.
func (t *LockTracker) HasScopes() bool {
	return len(t.finished) > 0
}

// Scopes returns all completed lock scopes.
func (t *LockTracker) Scopes() []*MutexScope {
	return t.finished
}

// HasOngoingLock returns true if the given selector has an active lock.
func (t *LockTracker) HasOngoingLock(selector string) bool {
	_, exists := t.onGoing[selector]
	return exists
}

// Lock call detection helpers

var lockMethods = []string{"RLock", "Lock"}
var unlockMethods = []string{"RUnlock", "Unlock"}

func subjectForLockCall(node ast.Node) ast.Expr {
	return SubjectForCall(node, lockMethods)
}

func subjectForUnlockCall(node ast.Node) ast.Expr {
	return SubjectForCall(node, unlockMethods)
}

func subjectForDeferUnlockCall(node ast.Node) ast.Expr {
	deferStmt, ok := node.(*ast.DeferStmt)
	if !ok {
		return nil
	}

	// Check for direct defer m.Unlock()
	if subject := SubjectForCall(deferStmt.Call, unlockMethods); subject != nil {
		return subject
	}

	// Check for defer func() { ... m.Unlock() ... }()
	funcLit, ok := deferStmt.Call.Fun.(*ast.FuncLit)
	if !ok || funcLit.Body == nil {
		return nil
	}

	// Search for Unlock call inside the closure body
	for _, stmt := range funcLit.Body.List {
		if subject := SubjectForCall(stmt, unlockMethods); subject != nil {
			return subject
		}
	}

	return nil
}
