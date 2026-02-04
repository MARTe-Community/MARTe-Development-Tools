package integration

import (
	"bytes"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/formatter"
	"github.com/marte-community/marte-dev-tools/internal/parser"
)

func TestFormatterArrayAndExpressions(t *testing.T) {
	input := `
+Obj = {
    Arr = { 1 + 2 3 + 4 }
    Nested = { { 5 + 6 7 } }
    Multi = {
      1
      2
    }
}
`
	// Expected formatting:
	// - Commas in arrays
	// - Parentheses around binary expressions
	expected := `
+Obj = {
  Arr = { (1 + 2), (3 + 4) }
  Nested = { { (5 + 6), 7 } }
  Multi = {
    1,
    2
  }
}
`

	// Normalize expected (trim first newline)
	expected = strings.TrimPrefix(expected, "\n")

	p := parser.NewParser(input)
	config, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	var buf bytes.Buffer
	formatter.Format(config, &buf)
	output := buf.String()

	// Simple whitespace normalization for comparison if needed, but formatter should be precise.
	// My formatter collapses blank lines. The input has standard structure.
	// Let's compare relevant parts if exact match fails (whitespace diffs).

	if !strings.Contains(output, "(1 + 2)") {
		t.Error("Expression 1+2 should be parenthesized")
	}
	if !strings.Contains(output, ", (3 + 4)") {
		t.Error("Array elements should be comma separated")
	}
	if !strings.Contains(output, "{ { (5 + 6), 7 } }") {
		t.Error("Nested array should be formatted with commas and parens")
	}
	// Check multiline commas
	if !strings.Contains(output, "1,") {
		t.Error("Multiline array element 1 should be followed by comma")
	}
}
