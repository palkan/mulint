// Package gclplugin registers mulint as a golangci-lint module plugin.
//
// Build a custom golangci-lint binary with mulint included by adding this
// module to your .custom-gcl.yml and running `golangci-lint custom`.
// See https://golangci-lint.run/docs/plugins/module-plugins/ and the README.
package gclplugin

import (
	"github.com/golangci/plugin-module-register/register"
	"golang.org/x/tools/go/analysis"

	"github.com/palkan/mulint/mulint"
)

func init() {
	register.Plugin("mulint", New)
}

// New returns the mulint plugin. It accepts no settings.
func New(_ any) (register.LinterPlugin, error) {
	return &plugin{}, nil
}

type plugin struct{}

func (*plugin) BuildAnalyzers() ([]*analysis.Analyzer, error) {
	return []*analysis.Analyzer{mulint.Mulint}, nil
}

func (*plugin) GetLoadMode() string {
	return register.LoadModeTypesInfo
}
