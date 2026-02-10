package integration

import (
	"context"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/validator"
)

func TestVariableValidation(t *testing.T) {
	// Need a schema that enforces strict types to test usage validation.
	// We can use built-in types or rely on Variable Definition validation.
	
	// Test Case 1: Variable Definition Mismatch
	contentDef := `
#var Positive: uint = -5
`
	pt := index.NewProjectTree()
	p := parser.NewParser(contentDef)
	cfg, err := p.Parse()
	if err != nil { t.Fatal(err) }
	pt.AddFile("def.marte", cfg)
	
	v := validator.NewValidator(pt, ".", nil)
	v.CheckVariables(context.Background())
	
	foundError := false
	for _, d := range v.Diagnostics {
		if strings.Contains(d.Message, "Variable 'Positive' value mismatch") {
			foundError = true
		}
	}
	if !foundError {
		t.Error("Expected error for invalid variable definition")
	}

	// Test Case 2: Variable Usage Mismatch
	// We need a class with specific field type.
	// PIDGAM.Kp is float | int.
	// Let's use string variable.
	contentUsage := `
#var MyStr: string = "hello"
+MyPID = {
    Class = PIDGAM
    Kp = @MyStr
    Ki = 0.0
    Kd = 0.0
}
`
	pt2 := index.NewProjectTree()
	p2 := parser.NewParser(contentUsage)
	cfg2, err := p2.Parse()
	if err != nil { t.Fatal(err) }
	pt2.AddFile("usage.marte", cfg2)
	
	v2 := validator.NewValidator(pt2, ".", nil)
	v2.ValidateProject(context.Background()) // Should run CUE validation on nodes
	
	foundUsageError := false
	for _, d := range v2.Diagnostics {
		// Schema validation error
		if strings.Contains(d.Message, "Schema Validation Error") && 
		   (strings.Contains(d.Message, "conflicting values") || strings.Contains(d.Message, "mismatched types")) {
			foundUsageError = true
		}
	}
	
	if !foundUsageError {
		t.Error("Expected error for invalid variable usage in PIDGAM.Kp")
		for _, d := range v2.Diagnostics {
			t.Logf("Diag: %s", d.Message)
		}
	}
	
	// Test Case 3: Valid Usage
	contentValid := `
#var MyGain: float = 1.5
+MyPID = {
    Class = PIDGAM
    Kp = @MyGain
    Ki = 0.0
    Kd = 0.0
}
`
	pt3 := index.NewProjectTree()
	p3 := parser.NewParser(contentValid)
	cfg3, err := p3.Parse()
	if err != nil { t.Fatal(err) }
	pt3.AddFile("valid.marte", cfg3)
	
	v3 := validator.NewValidator(pt3, ".", nil)
	v3.ValidateProject(context.Background())
	
	for _, d := range v3.Diagnostics {
		if strings.Contains(d.Message, "Schema Validation Error") {
			t.Errorf("Unexpected schema error: %s", d.Message)
		}
	}
}
