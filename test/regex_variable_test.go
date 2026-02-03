package integration

import (
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/validator"
)

func TestRegexVariable(t *testing.T) {
	content := `
#var IP: string & =~"^[0-9.]+$" = "127.0.0.1"
#var BadIP: string & =~"^[0-9.]+$" = "abc"

+Obj = {
    IP = @IP
}
`
	// Test Validator
	pt := index.NewProjectTree()
	p := parser.NewParser(content)
	cfg, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	pt.AddFile("regex.marte", cfg)

	v := validator.NewValidator(pt, ".", nil)
	v.CheckVariables()

	foundError := false
	for _, d := range v.Diagnostics {
		if strings.Contains(d.Message, "Variable 'BadIP' value mismatch") {
			foundError = true
		}
	}

	if !foundError {
		t.Error("Expected error for BadIP")
		for _, d := range v.Diagnostics {
			t.Logf("Diag: %s", d.Message)
		}
	}

	// Test valid variable
	for _, d := range v.Diagnostics {
		if strings.Contains(d.Message, "Variable 'IP' value mismatch") {
			t.Error("Unexpected error for IP")
		}
	}
}
