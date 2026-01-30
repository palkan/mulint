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

// ShortName returns just the type:method part without the package path.
// For example, "github.com/foo/bar.MyType:Method" returns "MyType:Method".
func (f FQN) ShortName() string {
	s := string(f)
	if idx := strings.LastIndex(s, "."); idx >= 0 {
		return s[idx+1:]
	}
	return s
}
