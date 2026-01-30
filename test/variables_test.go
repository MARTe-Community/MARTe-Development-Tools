package integration

import (
	"os"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/builder"
	"github.com/marte-community/marte-dev-tools/internal/parser"
)

func TestVariables(t *testing.T) {
	content := `
#var MyInt: int = 10
#var MyStr: string = "default"

+Obj = {
    Class = Test
    Field1 = @MyInt
    Field2 = @MyStr
}
`
	// Test Parsing
	p := parser.NewParser(content)
	cfg, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	// Check definitions: #var, #var, +Obj
	if len(cfg.Definitions) != 3 {
		t.Errorf("Expected 3 definitions, got %d", len(cfg.Definitions))
	}

	// Test Builder resolution
	f, _ := os.CreateTemp("", "vars.marte")
	f.WriteString(content)
	f.Close()
	defer os.Remove(f.Name())

	// Build with override
	overrides := map[string]string{
		"MyInt": "999",
	}

	b := builder.NewBuilder([]string{f.Name()}, overrides)

	outF, _ := os.CreateTemp("", "out.marte")
	outName := outF.Name()
	defer os.Remove(outName)

	err = b.Build(outF)
	outF.Close()

	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	outContent, _ := os.ReadFile(outName)
	outStr := string(outContent)

	if !strings.Contains(outStr, "Field1 = 999") {
		t.Errorf("Variable override failed for MyInt. Got:\n%s", outStr)
	}
	if !strings.Contains(outStr, "Field2 = \"default\"") {
		t.Errorf("Default value failed for MyStr. Got:\n%s", outStr)
	}
	// Check #var is removed
	if strings.Contains(outStr, "#var") {
		t.Error("#var definition present in output")
	}
}
