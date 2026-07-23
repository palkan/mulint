// Command mulint-vet is a go vet tool (unitchecker) variant of mulint.
//
// Unlike the standalone mulint binary, it is driven by the go build system
// and caches per-package analysis results in GOCACHE, so repeated and
// incremental runs are fast:
//
//	go vet -vettool=$(which mulint-vet) ./...
package main

import (
	"github.com/palkan/mulint/mulint"
	"golang.org/x/tools/go/analysis/unitchecker"
)

func main() {
	unitchecker.Main(mulint.Mulint)
}
