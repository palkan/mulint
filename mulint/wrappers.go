package mulint

import (
	"go/ast"
	"go/token"
	"go/types"
)

// WrapperKind indicates whether a wrapper method locks or unlocks.
type WrapperKind int

const (
	WrapperLock WrapperKind = iota
	WrapperUnlock
)

// WrapperMethod represents a method that wraps a mutex lock or unlock operation.
type WrapperMethod struct {
	MutexField string      // The mutex field name (e.g., "m" from "w.m.Lock()")
	Kind       WrapperKind // Whether this wrapper locks or unlocks
	FQN        FQN         // The fully qualified name of the wrapper method
	LockPos    token.Pos   // Position of the actual Lock() call inside the wrapper
}

// WrapperRegistry tracks methods that are lock/unlock wrappers.
type WrapperRegistry struct {
	wrappers map[FQN]WrapperMethod
}

func NewWrapperRegistry() *WrapperRegistry {
	return &WrapperRegistry{
		wrappers: make(map[FQN]WrapperMethod),
	}
}

// Register adds a wrapper method to the registry.
func (r *WrapperRegistry) Register(fqn FQN, mutexField string, kind WrapperKind, lockPos token.Pos) {
	r.wrappers[fqn] = WrapperMethod{
		MutexField: mutexField,
		Kind:       kind,
		FQN:        fqn,
		LockPos:    lockPos,
	}
}

// Get returns the wrapper info for a method, if it exists.
func (r *WrapperRegistry) Get(fqn FQN) (WrapperMethod, bool) {
	w, ok := r.wrappers[fqn]
	return w, ok
}

// IsLockWrapper returns true if the FQN is a locking wrapper.
func (r *WrapperRegistry) IsLockWrapper(fqn FQN) bool {
	w, ok := r.wrappers[fqn]
	return ok && w.Kind == WrapperLock
}

// IsUnlockWrapper returns true if the FQN is an unlocking wrapper.
func (r *WrapperRegistry) IsUnlockWrapper(fqn FQN) bool {
	w, ok := r.wrappers[fqn]
	return ok && w.Kind == WrapperUnlock
}

// IdentifyWrappers scans collected scopes and function bodies to identify wrapper methods.
func (r *WrapperRegistry) IdentifyWrappers(scopes map[FQN]*LockTracker, funcs []*ast.FuncDecl, fqnFunc func(*ast.FuncDecl) FQN) {
	// A locking wrapper is a function that locks a mutex but does NOT unlock it.
	// Functions that lock AND unlock (like doSomeWork with defer unlock) are self-contained
	// and should not be treated as locking wrappers.
	for fqn, tracker := range scopes {
		for _, scope := range tracker.Scopes() {
			// Only consider scopes that were NOT properly unlocked
			if scope.IsUnlocked() {
				continue
			}
			_, mutexField := SplitSelector(scope.Selector())
			if mutexField != "" {
				r.Register(fqn, mutexField, WrapperLock, scope.Pos())
				break // One mutex field per function is enough
			}
		}
	}

	// Identify unlock-only methods (methods that unlock without locking)
	for _, fn := range funcs {
		fqn := fqnFunc(fn)
		if _, isLocking := r.wrappers[fqn]; isLocking {
			continue // Already registered as locking
		}

		if mutexField, pos := getUnlockOnlyField(fn.Body); mutexField != "" {
			r.Register(fqn, mutexField, WrapperUnlock, pos)
		}
	}
}

// getUnlockOnlyField checks if a function body only contains an unlock call
// and returns the mutex field name and position if so.
func getUnlockOnlyField(body *ast.BlockStmt) (string, token.Pos) {
	if body == nil {
		return "", token.NoPos
	}

	var unlockField string
	var unlockPos token.Pos
	hasLock := false

	for _, stmt := range body.List {
		if e := subjectForLockCall(stmt); e != nil {
			hasLock = true
		}
		if e := subjectForUnlockCall(stmt); e != nil {
			selector := StrExpr(e)
			_, unlockField = SplitSelector(selector)
			unlockPos = stmt.Pos()
		}
	}

	if hasLock || unlockField == "" {
		return "", token.NoPos
	}
	return unlockField, unlockPos
}

// WrapperAwareTracker extends LockTracker with wrapper method awareness.
type WrapperAwareTracker struct {
	*LockTracker
	registry *WrapperRegistry
	typeInfo *types.Info
}

func NewWrapperAwareTracker(registry *WrapperRegistry, typeInfo *types.Info) *WrapperAwareTracker {
	return &WrapperAwareTracker{
		LockTracker: NewLockTracker(),
		registry:    registry,
		typeInfo:    typeInfo,
	}
}

// TrackWithWrappers processes a statement, recognizing both direct and wrapper lock/unlock calls.
func (t *WrapperAwareTracker) TrackWithWrappers(stmt ast.Stmt) {
	// Add to ongoing scopes first
	t.AddToOngoing(stmt)

	// Check for wrapper calls (creates new scopes)
	t.trackWrapperCall(stmt)

	// Track direct lock/unlock calls (don't re-add to scopes)
	t.Track(stmt, false)
}

// trackWrapperCall checks if a statement is a call to a wrapper method.
func (t *WrapperAwareTracker) trackWrapperCall(stmt ast.Stmt) {
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
	if !isWrapper {
		return
	}

	// Get the receiver of the call (e.g., "w" from "w.Acquire()")
	selector := SelectorExpr(call)
	if selector == nil {
		return
	}
	receiver := RootSelector(selector)
	if receiver == nil {
		return
	}

	// Build the effective mutex selector (e.g., "w" + "." + "m" = "w.m")
	effectiveSelector := receiver.Name + "." + wrapper.MutexField

	switch wrapper.Kind {
	case WrapperLock:
		wrapperInfo := &WrapperInfo{
			FQN:     wrapper.FQN,
			LockPos: wrapper.LockPos,
		}
		t.StartLockWithWrapper(effectiveSelector, stmt.Pos(), wrapperInfo)
	case WrapperUnlock:
		t.EndLock(effectiveSelector)
	}

	// Handle deferred wrapper calls
	if deferStmt, ok := stmt.(*ast.DeferStmt); ok {
		t.trackDeferredWrapperCall(deferStmt)
	}
}

// trackDeferredWrapperCall handles deferred wrapper unlock calls.
func (t *WrapperAwareTracker) trackDeferredWrapperCall(deferStmt *ast.DeferStmt) {
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
	t.AddDeferredUnlock(effectiveSelector)
}

// AnalyzeStatements recursively analyzes statements including nested blocks.
func (t *WrapperAwareTracker) AnalyzeStatements(stmts []ast.Stmt) {
	for _, stmt := range stmts {
		t.TrackWithWrappers(stmt)
		t.analyzeNestedStatements(stmt)
	}
}

// analyzeNestedStatements handles statements that contain nested blocks.
func (t *WrapperAwareTracker) analyzeNestedStatements(stmt ast.Stmt) {
	switch s := stmt.(type) {
	case *ast.IfStmt:
		if s.Body != nil {
			t.AnalyzeStatements(s.Body.List)
		}
		if s.Else != nil {
			switch e := s.Else.(type) {
			case *ast.BlockStmt:
				t.AnalyzeStatements(e.List)
			case *ast.IfStmt:
				t.analyzeNestedStatements(e)
			}
		}
	case *ast.ForStmt:
		if s.Body != nil {
			t.AnalyzeStatements(s.Body.List)
		}
	case *ast.RangeStmt:
		if s.Body != nil {
			t.AnalyzeStatements(s.Body.List)
		}
	case *ast.SwitchStmt:
		// Switch cases are mutually exclusive - analyze each independently
		t.analyzeMutuallyExclusiveCases(s.Body)
	case *ast.SelectStmt:
		// Select cases are mutually exclusive - analyze each independently
		t.analyzeMutuallyExclusiveCommCases(s.Body)
	case *ast.BlockStmt:
		t.AnalyzeStatements(s.List)
	}
}

// analyzeMutuallyExclusiveCases analyzes switch cases independently.
// Each case starts with the current ongoing lock state but doesn't affect other cases.
func (t *WrapperAwareTracker) analyzeMutuallyExclusiveCases(body *ast.BlockStmt) {
	if body == nil {
		return
	}

	// Save current state
	savedOngoing := t.snapshotOngoing()

	for _, clause := range body.List {
		cc, ok := clause.(*ast.CaseClause)
		if !ok {
			continue
		}

		// Restore to state before switch for each case
		t.restoreOngoing(savedOngoing)

		// Analyze this case
		t.AnalyzeStatements(cc.Body)
	}

	// After switch, restore to pre-switch state
	// (conservative: we don't know which case ran)
	t.restoreOngoing(savedOngoing)
}

// analyzeMutuallyExclusiveCommCases analyzes select cases independently.
func (t *WrapperAwareTracker) analyzeMutuallyExclusiveCommCases(body *ast.BlockStmt) {
	if body == nil {
		return
	}

	savedOngoing := t.snapshotOngoing()

	for _, clause := range body.List {
		cc, ok := clause.(*ast.CommClause)
		if !ok {
			continue
		}

		t.restoreOngoing(savedOngoing)
		t.AnalyzeStatements(cc.Body)
	}

	t.restoreOngoing(savedOngoing)
}

// snapshotOngoing creates a copy of the current ongoing locks state.
func (t *WrapperAwareTracker) snapshotOngoing() map[string]*MutexScope {
	snapshot := make(map[string]*MutexScope, len(t.LockTracker.onGoing))
	for k, v := range t.LockTracker.onGoing {
		snapshot[k] = v
	}
	return snapshot
}

// restoreOngoing restores the ongoing locks state from a snapshot.
func (t *WrapperAwareTracker) restoreOngoing(snapshot map[string]*MutexScope) {
	t.LockTracker.onGoing = make(map[string]*MutexScope, len(snapshot))
	for k, v := range snapshot {
		t.LockTracker.onGoing[k] = v
	}
}
