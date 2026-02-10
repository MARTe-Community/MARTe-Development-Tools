package integration

import (
	"context"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/validator"
)

func TestMultiFileVariableResolution(t *testing.T) {
	// File 1: Defines a variable in the root scope (no package)
	file1Content := `#package Test
#var GlobalVal: int = 42`

	// File 2: Uses the variable (no package)
	file2Content := `
	#package Test
+App = {
	Class = RealTimeApplication
	Field = @GlobalVal
}
`

	pt := index.NewProjectTree()

	// Parse and add File 1
	p1 := parser.NewParser(file1Content)
	cfg1, err := p1.Parse()
	if err != nil {
		t.Fatalf("Parse file1 error: %v", err)
	}
	pt.AddFile("vars.marte", cfg1)

	// Parse and add File 2
	p2 := parser.NewParser(file2Content)
	cfg2, err := p2.Parse()
	if err != nil {
		t.Fatalf("Parse file2 error: %v", err)
	}
	pt.AddFile("main.marte", cfg2)

	pt.ResolveReferences()

	// Validate
	// We need a dummy schema for CheckVariables to work, or we check References directly.
	// CheckVariables validates types. CheckUnresolvedVariables validates existence.
	// We want to check if $GlobalVal is resolved.

	t.Logf("Root Variables keys: %v", getKeys(pt.Root.Variables))

	v := validator.NewValidator(pt, ".", nil)
	v.CheckUnresolvedVariables(context.Background())

	for _, d := range v.Diagnostics {
		if strings.Contains(d.Message, "Unresolved variable") {
			t.Errorf("Unexpected unresolved variable error: %s", d.Message)
		}
	}

	// Verify reference target directly
	found := false
	for _, ref := range pt.References {
		if ref.Name == "GlobalVal" {
			found = true
			if ref.TargetVariable == nil {
				t.Error("Reference 'GlobalVal' TargetVariable is nil (not resolved)")
			} else {
				if ref.TargetVariable.Name != "GlobalVal" {
					t.Errorf("Reference resolved to wrong variable: %s", ref.TargetVariable.Name)
				}
			}
		}
	}
	if !found {
		t.Error("Reference 'GlobalVal' not found in index")
	}
}

func getKeys(m map[string]index.VariableInfo) []string {
	keys := []string{}
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
