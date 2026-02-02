package integration

import (
	"bytes"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/formatter"
	"github.com/marte-community/marte-dev-tools/internal/parser"
)

func TestFormatterCoverage(t *testing.T) {
	content := `
// Head comment
#package Pkg

//# Doc for A
+A = {
    Field = 10 // Trailing
    Bool = true
    Float = 1.23
    Ref = SomeObj
    Array = { 1 2 3 }
    Expr = 1 + 2
    
    // Inner
    +B = {
        Val = "Str"
    }
}

// Final
`
	p := parser.NewParser(content)
	cfg, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	var buf bytes.Buffer
	formatter.Format(cfg, &buf)

	out := buf.String()
	if !strings.Contains(out, "Field = 10") {
		t.Error("Formatting failed")
	}

	// Check comments
	if !strings.Contains(out, "// Head comment") {
		t.Error("Head comment missing")
	}
	if !strings.Contains(out, "//# Doc for A") {
		t.Error("Doc missing")
	}
}
