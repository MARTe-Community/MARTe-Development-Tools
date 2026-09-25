package integration

import (
	"context"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/validator"
)

// Regression: re-aggregating node state when a later file is added
// (AddFile -> removeFileFromNode) used to drop #foreach loop variable and
// #template parameter registrations, flooding multi-file projects with
// false "Unresolved variable reference" errors for every pass.
func TestLoopVariablesSurviveMultiFileAddOrder(t *testing.T) {
	loopSrc := `#package Reg
var Count: int = 2

template Dev(N: int)
  ("+Dev" .. @N) = {
    Class = ReferenceContainer
    Index = @N
  }
end

+Rack = {
  Class = ReferenceContainer
  #foreach i in { 1, 2 }
    #if (@i <= @Count)
      use Dev D(N = @i)
    #end
  #end
}`
	lateSrc := `#package Reg
var Late: int = 7

+LateObj = {
  Class = ReferenceContainer
}`

	pt := index.NewProjectTree()
	for name, src := range map[string]string{"a.marte": loopSrc, "b.marte": lateSrc} {
		cfg, err := parser.NewParser(src).Parse()
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		pt.AddFile(name, cfg)
	}

	root, ok := pt.Root.Children["Reg"]
	if !ok {
		t.Fatal("package node Reg not found")
	}
	for _, name := range []string{"Count", "N"} {
		if _, ok := root.Variables[name]; !ok {
			t.Errorf("variable %q missing from package scope after later AddFile (re-aggregation dropped it)", name)
		}
	}
	rack, ok := root.Children["Rack"]
	if !ok {
		t.Fatal("Rack node not found")
	}
	if _, ok := rack.Variables["i"]; !ok {
		t.Error("loop variable 'i' missing from Rack scope after later AddFile (re-aggregation dropped it)")
	}

	v := validator.NewValidator(pt, ".", nil)
	v.ValidateProject(context.Background())
	for _, d := range v.Diagnostics {
		if d.Level == validator.LevelError && strings.Contains(d.Message, "Unresolved variable reference") {
			t.Errorf("unexpected unresolved-variable diagnostic: %s", d.Message)
		}
	}
}
