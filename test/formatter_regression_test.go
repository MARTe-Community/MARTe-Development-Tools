package integration

import (
	"bytes"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/formatter"
	"github.com/marte-community/marte-dev-tools/internal/parser"
)

// TestFormatterPeekPositionEdgeCase is a regression test for BUG-037:
// peekPosition returns parser.Position{} (Line:0, Column:0) when the
// cursor is at the end of insertables. Callers must know to check
// peek.Line > 0 — a fragile, undocumented contract.
//
// This test verifies that formatting with comments and pragmas at
// various positions does not crash and produces expected output.
func TestFormatterPeekPositionEdgeCase(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantSubs []string // substrings that must appear in output
	}{
		{
			name: "comment at end of file no newline",
			input: `+Node = { Class = Test }
// trailing comment`,
			wantSubs: []string{"+Node", "Class", "Test", "// trailing comment"},
		},
		{
			name: "pragma at end of file no newline",
			input: `+Node = { Class = Test }
//! ignore(unused)`,
			wantSubs: []string{"+Node", "Class", "Test", "//! ignore(unused)"},
		},
		{
			name: "docstring before object",
			input: `//# MyNode docs
+Node = { Class = Test }`,
			wantSubs: []string{"//# MyNode docs", "+Node", "Class", "Test"},
		},
		{
			name: "comment between fields",
			input: `+Node = {
    // comment between
    Field1 = 1
    Field2 = 2
}`,
			wantSubs: []string{"// comment between", "Field1", "Field2"},
		},
		{
			name: "no comments or pragmas",
			input: `+Node = { Class = Test }
`,
			wantSubs: []string{"+Node", "Class", "Test"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := parser.NewParser(tt.input)
			cfg, err := p.Parse()
			if err != nil {
				t.Fatalf("Parse failed: %v", err)
			}

			var buf bytes.Buffer
			formatter.Format(cfg, &buf)
			output := buf.String()

			for _, want := range tt.wantSubs {
				if !strings.Contains(output, want) {
					t.Errorf("Expected output to contain %q, got:\n%s", want, output)
				}
			}
		})
	}
}

// TestFormatterCommentSticking verifies that comments "stick" to the
// following definition (no blank line between comment and code).
func TestFormatterCommentSticking(t *testing.T) {
	input := `//# Doc
+Node = {
    // inline
    Field = 1
}
`
	p := parser.NewParser(input)
	cfg, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	var buf bytes.Buffer
	formatter.Format(cfg, &buf)
	output := buf.String()

	// Docstring should be immediately followed by the object (no blank line)
	if !strings.Contains(output, "//# Doc\n+Node") {
		t.Errorf("Expected docstring to stick to object, got:\n%s", output)
	}

	// Inline comment should be immediately followed by Field
	if !strings.Contains(output, "// inline\n  Field") {
		t.Errorf("Expected inline comment to stick to field, got:\n%s", output)
	}
}
