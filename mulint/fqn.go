package mulint

import (
	"strings"
)

type FQN string

func FromCallInfo(pkg, fnName string) FQN {
	// fnName may already include "RecvType:MethodName" format
	// We need to produce: "pkg.RecvType:MethodName" to match fqn() output
	fnName = strings.Trim(fnName, "*")
	return FQN(pkg + "." + fnName)
}
