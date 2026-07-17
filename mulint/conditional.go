package mulint

import (
	"go/ast"
	"go/token"
	"go/types"
)

// ConditionalLock represents a lock that is guarded by a boolean parameter.
// Example:
//
//	func (a *Some) helper(lock bool) {
//	    if lock {
//	        a.mu.Lock()
//	        defer a.mu.Unlock()
//	    }
//	}
type ConditionalLock struct {
	ParamIndex int    // Index of the bool parameter that controls the lock
	ParamName  string // Name of the parameter
	Selector   string // The mutex selector (e.g., "a.mu")
	Negated    bool   // True if condition is negated (if !lock)
	Balanced   bool   // True if guarded unlocks balance the guarded locks:
	// the function is self-contained for any argument value and must not be
	// classified as a lock-acquiring wrapper.
}

// ConditionalLockRegistry tracks functions with conditional locks.
type ConditionalLockRegistry struct {
	locks map[FQN][]ConditionalLock
	info  *types.Info
}

func NewConditionalLockRegistry(info *types.Info) *ConditionalLockRegistry {
	return &ConditionalLockRegistry{
		locks: make(map[FQN][]ConditionalLock),
		info:  info,
	}
}

// Get returns conditional locks for a function, if any.
func (r *ConditionalLockRegistry) Get(fqn FQN) []ConditionalLock {
	return r.locks[fqn]
}

// AnalyzeFunc analyzes a function for conditional lock patterns.
// Guards are recognized at any nesting depth (e.g. nats-server's
// pubAllowedFullCheck buries `if !hasLock { c.mu.Lock() }` inside another
// condition). Registration is conservative — skipping a call site based on a
// literal argument is only sound when:
//   - the parameter is never reassigned in the body (its value at guard time
//     is the call-site value), and
//   - every acquisition of that mutex in the function is covered by the same
//     param+negation guard (no unguarded Lock that would run regardless).
func (r *ConditionalLockRegistry) AnalyzeFunc(fqn FQN, fn *ast.FuncDecl) {
	if fn.Type.Params == nil || fn.Body == nil {
		return
	}

	boolParams := collectBoolParams(fn.Type.Params)
	if len(boolParams) == 0 {
		return
	}

	removeReassignedParams(fn.Body, boolParams)
	if len(boolParams) == 0 {
		return
	}

	// Find guards at any depth and record which lock/unlock sites they cover.
	type guardKey struct {
		param    string
		negated  bool
		selector string
	}
	guarded := make(map[guardKey]map[token.Pos]bool)
	guardedUnlocks := make(map[guardKey]int)

	ast.Inspect(fn.Body, func(n ast.Node) bool {
		ifStmt, ok := n.(*ast.IfStmt)
		if !ok {
			return true
		}

		paramName, negated := extractBoolParamCondition(ifStmt.Cond, boolParams)
		if paramName == "" {
			return true
		}

		for selector, positions := range findLockSites(ifStmt.Body) {
			key := guardKey{paramName, negated, selector}
			if guarded[key] == nil {
				guarded[key] = make(map[token.Pos]bool)
			}
			for _, pos := range positions {
				guarded[key][pos] = true
			}
		}
		for selector, positions := range findMutexCallSites(ifStmt.Body, unlockMethods) {
			guardedUnlocks[guardKey{paramName, negated, selector}] += len(positions)
		}
		return true
	})

	if len(guarded) == 0 {
		return
	}

	allSites := findLockSites(fn.Body)

	for key, positions := range guarded {
		exclusive := true
		for _, pos := range allSites[key.selector] {
			if !positions[pos] {
				exclusive = false
				break
			}
		}
		if !exclusive {
			continue
		}

		r.locks[fqn] = append(r.locks[fqn], ConditionalLock{
			ParamIndex: boolParams[key.param],
			ParamName:  key.param,
			Selector:   key.selector,
			Negated:    key.negated,
			Balanced:   guardedUnlocks[key] >= len(positions),
		})
	}
}

// HasBalancedPair reports whether the function has a balanced conditional
// lock/unlock pair for the given selector — i.e. it is self-contained for
// any argument value and must not be treated as a lock-acquiring wrapper.
func (r *ConditionalLockRegistry) HasBalancedPair(fqn FQN, selector string) bool {
	for _, cl := range r.locks[fqn] {
		if cl.Selector == selector && cl.Balanced {
			return true
		}
	}
	return false
}

// collectBoolParams maps bool parameter names to their positional indices.
func collectBoolParams(params *ast.FieldList) map[string]int {
	boolParams := make(map[string]int)
	paramIndex := 0
	for _, field := range params.List {
		if ident, ok := field.Type.(*ast.Ident); ok && ident.Name == "bool" {
			for _, name := range field.Names {
				boolParams[name.Name] = paramIndex
				paramIndex++
			}
		} else {
			paramIndex += len(field.Names)
			if len(field.Names) == 0 {
				paramIndex++ // unnamed parameter
			}
		}
	}
	return boolParams
}

// removeReassignedParams drops parameters whose value may change inside the
// body: assigned to, or having their address taken.
func removeReassignedParams(body *ast.BlockStmt, boolParams map[string]int) {
	ast.Inspect(body, func(n ast.Node) bool {
		switch s := n.(type) {
		case *ast.AssignStmt:
			for _, lhs := range s.Lhs {
				if id, ok := lhs.(*ast.Ident); ok {
					delete(boolParams, id.Name)
				}
			}
		case *ast.UnaryExpr:
			if s.Op == token.AND {
				if id, ok := s.X.(*ast.Ident); ok {
					delete(boolParams, id.Name)
				}
			}
		}
		return true
	})
}

// findLockSites returns the position of every Lock/RLock call per selector
// within the node, at any depth.
func findLockSites(root ast.Node) map[string][]token.Pos {
	return findMutexCallSites(root, lockMethods)
}

// findMutexCallSites returns the position of every call to one of the given
// mutex methods per selector within the node, at any depth.
func findMutexCallSites(root ast.Node, methods []string) map[string][]token.Pos {
	sites := make(map[string][]token.Pos)
	ast.Inspect(root, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if subject := SubjectForCall(call, methods); subject != nil {
			selector := StrExpr(subject)
			sites[selector] = append(sites[selector], call.Pos())
		}
		return true
	})
	return sites
}

// PropagateConditionalLocks propagates conditional locks through intermediate functions.
// If function A calls function B with a conditional lock, and passes its own bool param
// to B's conditional param, then A also has a conditional lock.
func (r *ConditionalLockRegistry) PropagateConditionalLocks(funcs []*ast.FuncDecl, funcFQN func(*ast.FuncDecl) FQN) {
	// Build a map from FQN to function declaration for quick lookup
	fqnToFunc := make(map[FQN]*ast.FuncDecl)
	for _, fn := range funcs {
		fqnToFunc[funcFQN(fn)] = fn
	}

	// Keep propagating until no new conditional locks are found
	changed := true
	for changed {
		changed = false
		for _, fn := range funcs {
			if fn.Type.Params == nil || fn.Body == nil {
				continue
			}

			fqn := funcFQN(fn)

			// Build map of bool parameter names to their indices for this function
			boolParams := collectBoolParams(fn.Type.Params)
			if len(boolParams) == 0 {
				continue
			}

			// Look for calls to functions with conditional locks
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}

				// Get the called function's FQN
				calleePkg, calleeName, ok := GetCallInfo(call, r.info)
				if !ok {
					return true
				}
				calleeFQN := FromCallInfo(calleePkg, calleeName)

				// Check if callee has conditional locks
				calleeLocks := r.locks[calleeFQN]
				if len(calleeLocks) == 0 {
					return true
				}

				// Check if any of our bool params are passed to callee's conditional params
				for _, calleeLock := range calleeLocks {
					if calleeLock.ParamIndex >= len(call.Args) {
						continue
					}

					arg := call.Args[calleeLock.ParamIndex]
					argIdent, ok := arg.(*ast.Ident)
					if !ok {
						continue
					}

					// Check if this argument is one of our bool parameters
					ourParamIndex, isBoolParam := boolParams[argIdent.Name]
					if !isBoolParam {
						continue
					}

					// Check if we already have this conditional lock
					alreadyHave := false
					for _, existing := range r.locks[fqn] {
						if existing.ParamIndex == ourParamIndex &&
							existing.Selector == calleeLock.Selector &&
							existing.Negated == calleeLock.Negated {
							alreadyHave = true
							break
						}
					}

					if !alreadyHave {
						r.locks[fqn] = append(r.locks[fqn], ConditionalLock{
							ParamIndex: ourParamIndex,
							ParamName:  argIdent.Name,
							Selector:   calleeLock.Selector,
							Negated:    calleeLock.Negated,
						})
						changed = true
					}
				}

				return true
			})
		}
	}
}

// extractBoolParamCondition checks if the condition is a simple bool parameter check.
// Returns the parameter name and whether it's negated.
func extractBoolParamCondition(cond ast.Expr, boolParams map[string]int) (string, bool) {
	switch c := cond.(type) {
	case *ast.Ident:
		// if lock { ... }
		if _, ok := boolParams[c.Name]; ok {
			return c.Name, false
		}
	case *ast.UnaryExpr:
		// if !lock { ... }
		if c.Op.String() == "!" {
			if ident, ok := c.X.(*ast.Ident); ok {
				if _, ok := boolParams[ident.Name]; ok {
					return ident.Name, true
				}
			}
		}
	}
	return "", false
}

// ShouldSkipLock checks if a transitive lock should be skipped based on the call arguments.
func (r *ConditionalLockRegistry) ShouldSkipLock(fqn FQN, call *ast.CallExpr, lockSelector string) bool {
	condLocks := r.locks[fqn]
	if len(condLocks) == 0 {
		return false
	}

	for _, cl := range condLocks {
		if cl.Selector != lockSelector {
			continue
		}

		// Check if we have enough arguments
		if cl.ParamIndex >= len(call.Args) {
			continue
		}

		arg := call.Args[cl.ParamIndex]
		boolValue, ok := extractBoolLiteral(arg)
		if !ok {
			continue // Can't determine value statically
		}

		// If negated: lock happens when param is false, so skip when param is true
		// If not negated: lock happens when param is true, so skip when param is false
		if cl.Negated {
			if boolValue { // param is true, !param is false, lock doesn't happen
				return true
			}
		} else {
			if !boolValue { // param is false, lock doesn't happen
				return true
			}
		}
	}

	return false
}

// extractBoolLiteral extracts a boolean literal value from an expression.
func extractBoolLiteral(expr ast.Expr) (bool, bool) {
	switch e := expr.(type) {
	case *ast.Ident:
		if e.Name == "true" {
			return true, true
		}
		if e.Name == "false" {
			return false, true
		}
	}
	return false, false
}
