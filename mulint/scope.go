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

// Track processes a statement for lock/unlock operations.
// If addToOngoing is true, the statement is added to all currently held lock scopes.
func (t *LockTracker) Track(stmt ast.Stmt, addToOngoing bool) {
	if addToOngoing {
		t.AddToOngoing(stmt)
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
	if deferStmt, ok := node.(*ast.DeferStmt); ok {
		return SubjectForCall(deferStmt.Call, unlockMethods)
	}
	return nil
}
