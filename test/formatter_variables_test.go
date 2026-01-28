package integration

import (
	"bytes"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/formatter"
	"github.com/marte-community/marte-dev-tools/internal/parser"
)

func TestFormatterVariables(t *testing.T) {
	content := `
#var MyInt: int = 10
#var MyStr: string | "A" = "default"

+Obj = {
    Field1 = $MyInt
    Field2 = $MyStr
}
`
	p := parser.NewParser(content)
	cfg, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	var buf bytes.Buffer
	formatter.Format(cfg, &buf)

	output := buf.String()

	// Parser reconstructs type expression with spaces
	if !strings.Contains(output, "#var MyInt: int = 10") {
		t.Errorf("Variable MyInt formatted incorrectly. Got:\n%s", output)
	}
	// Note: parser adds space after each token in TypeExpr
	// string | "A" -> "string | \"A\"" 
	if !strings.Contains(output, "#var MyStr: string | \"A\" = \"default\"") {
		t.Errorf("Variable MyStr formatted incorrectly. Got:\n%s", output)
	}
	if !strings.Contains(output, "Field1 = $MyInt") {
		t.Errorf("Variable reference $MyInt formatted incorrectly. Got:\n%s", output)
	}
}
