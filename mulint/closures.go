package mulint

import (
	"go/ast"
	"go/token"
	"go/types"
)

// LocalUnlockClosure describes a function-local closure whose call releases
// one or more mutexes, e.g. nats-server's error-path helpers:
//
//	clearAndUnlock := func() {
//		batch.clearBatchState()
//		mset.mu.Unlock()
//	}
type LocalUnlockClosure struct {
	Unlocks []string // selectors released by calling the closure
}

// AnalyzeLocalClosures finds function-local closures that unconditionally
// release mutexes when called. The analysis is deliberately conservative —
// treating a call as a release can suppress a real leak report, so a closure
// qualifies only if:
//   - it is bound exactly once via `name := func(){...}` (or `var name = ...`)
//     and never reassigned;
//   - the variable never escapes: every use is a direct call `name()`
//     (including `defer name()`);
//   - the claimed unlocks are top-level statements of the closure body
//     (unconditional);
//   - the closure does not also lock the same mutex (a balanced closure
//     manages its own critical section and releases nothing for the caller).
func AnalyzeLocalClosures(body *ast.BlockStmt, info *types.Info) map[types.Object]*LocalUnlockClosure {
	if body == nil || info == nil {
		return nil
	}

	candidates := make(map[types.Object]*ast.FuncLit)
	disqualified := make(map[types.Object]bool)
	allowedIdents := make(map[*ast.Ident]bool)

	// Pass 1: candidate definitions and reassignment/escape bookkeeping.
	ast.Inspect(body, func(n ast.Node) bool {
		switch s := n.(type) {
		case *ast.AssignStmt:
			isDefinition := s.Tok == token.DEFINE &&
				len(s.Lhs) == 1 && len(s.Rhs) == 1
			if isDefinition {
				ident, okL := s.Lhs[0].(*ast.Ident)
				funcLit, okR := s.Rhs[0].(*ast.FuncLit)
				if okL && okR {
					if obj := info.Defs[ident]; obj != nil {
						candidates[obj] = funcLit
						allowedIdents[ident] = true
						return true
					}
				}
			}
			// Any other assignment touching a candidate disqualifies it.
			for _, lhs := range s.Lhs {
				if ident, ok := lhs.(*ast.Ident); ok {
					if obj := info.Uses[ident]; obj != nil {
						disqualified[obj] = true
					}
				}
			}
		case *ast.ValueSpec:
			if len(s.Names) == 1 && len(s.Values) == 1 {
				if funcLit, ok := s.Values[0].(*ast.FuncLit); ok {
					if obj := info.Defs[s.Names[0]]; obj != nil {
						candidates[obj] = funcLit
						allowedIdents[s.Names[0]] = true
					}
				}
			}
		case *ast.CallExpr:
			if ident, ok := s.Fun.(*ast.Ident); ok {
				allowedIdents[ident] = true
			}
		}
		return true
	})

	// Pass 2: escape check — every remaining use must be an allowed ident
	// (the definition site or a direct call).
	ast.Inspect(body, func(n ast.Node) bool {
		ident, ok := n.(*ast.Ident)
		if !ok || allowedIdents[ident] {
			return true
		}
		if obj := info.Uses[ident]; obj != nil {
			if _, isCandidate := candidates[obj]; isCandidate {
				disqualified[obj] = true
			}
		}
		return true
	})

	// Pass 3: compute the release effect of each surviving closure.
	result := make(map[types.Object]*LocalUnlockClosure)
	for obj, funcLit := range candidates {
		if disqualified[obj] || funcLit.Body == nil {
			continue
		}

		lockSites := findLockSites(funcLit.Body)

		var unlocks []string
		for _, stmt := range funcLit.Body.List {
			subject := SubjectForCall(stmt, unlockMethods)
			if subject == nil || !IsMutexType(subject, info) {
				continue
			}
			selector := StrExpr(subject)
			if len(lockSites[selector]) > 0 {
				continue // the closure manages its own critical section
			}
			unlocks = append(unlocks, selector)
		}

		if len(unlocks) > 0 {
			result[obj] = &LocalUnlockClosure{Unlocks: unlocks}
		}
	}

	return result
}
