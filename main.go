package main

import (
	"github.com/palkan/mulint/mulint"
	"golang.org/x/tools/go/analysis/singlechecker"
)

func main() {
	singlechecker.Main(mulint.Mulint)
}
