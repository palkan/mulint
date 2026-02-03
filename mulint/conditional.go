package mulint

import (
	"go/ast"
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
func (r *ConditionalLockRegistry) AnalyzeFunc(fqn FQN, fn *ast.FuncDecl) {
	if fn.Type.Params == nil {
		return
	}

	// Build map of bool parameter names to their indices
	boolParams := make(map[string]int)
	paramIndex := 0
	for _, field := range fn.Type.Params.List {
		// Check if this is a bool type
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

	if len(boolParams) == 0 {
		return
	}

	// Look for if statements that check a bool parameter and contain a lock
	for _, stmt := range fn.Body.List {
		ifStmt, ok := stmt.(*ast.IfStmt)
		if !ok {
			continue
		}

		paramName, negated := extractBoolParamCondition(ifStmt.Cond, boolParams)
		if paramName == "" {
			continue
		}

		// Check if the if body contains a lock
		selector := findLockInBlock(ifStmt.Body)
		if selector == "" {
			continue
		}

		r.locks[fqn] = append(r.locks[fqn], ConditionalLock{
			ParamIndex: boolParams[paramName],
			ParamName:  paramName,
			Selector:   selector,
			Negated:    negated,
		})
	}
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
			boolParams := make(map[string]int)
			paramIndex := 0
			for _, field := range fn.Type.Params.List {
				if ident, ok := field.Type.(*ast.Ident); ok && ident.Name == "bool" {
					for _, name := range field.Names {
						boolParams[name.Name] = paramIndex
						paramIndex++
					}
				} else {
					paramIndex += len(field.Names)
					if len(field.Names) == 0 {
						paramIndex++
					}
				}
			}

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

// findLockInBlock searches for a Lock() call in a block and returns its selector.
func findLockInBlock(block *ast.BlockStmt) string {
	for _, stmt := range block.List {
		if subject := SubjectForCall(stmt, lockMethods); subject != nil {
			return StrExpr(subject)
		}
		// Also check deferred locks
		if deferStmt, ok := stmt.(*ast.DeferStmt); ok {
			if subject := SubjectForCall(deferStmt.Call, lockMethods); subject != nil {
				return StrExpr(subject)
			}
		}
	}
	return ""
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
