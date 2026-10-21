package integration

import (
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/parser"
)

// End() must cover the whole expression: a member access ends past the
// member name, not after the first characters of the base.
func TestMemberAccessEndPosition(t *testing.T) {
	cases := []struct {
		src        string
		wantColumn int
	}{
		{`@prefixes.fast`, 15}, // "@prefixes" (9) + "." + "fast" (4) => past col 14
		{`@rfid`, 6},           // plain reference unchanged
		{`@a.b.c`, 7},          // nested access: @a + .b + .c
		{`@cfg.program.name`, 18},
	}
	for _, tc := range cases {
		t.Run(tc.src, func(t *testing.T) {
			v, err := parser.ParseValue(tc.src)
			if err != nil {
				t.Fatalf("ParseValue(%q): %v", tc.src, err)
			}
			// ParseValue starts at column 1.
			if got := v.End().Column; got != tc.wantColumn {
				t.Errorf("End(%q).Column = %d, want %d", tc.src, got, tc.wantColumn)
			}
		})
	}
}

// A method call ends past its closing parenthesis, not at the receiver.
func TestMethodCallEndPosition(t *testing.T) {
	v, err := parser.ParseValue("@names.replace(\":\", \"\")")
	if err != nil {
		t.Fatalf("ParseValue: %v", err)
	}
	if got, want := v.End().Column, len("@names.replace(\":\", \"\")")+1; got != want {
		t.Errorf("End().Column = %d, want %d (past the closing paren)", got, want)
	}
}

// The expression result hint must sit after the expression, never inside
// a reference or member name.
func TestExpressionEndAfterMemberAccess(t *testing.T) {
	src := `#let fast_id: string = (@rfid .. @prefixes.fast)`
	v, err := parser.ParseValue("(@rfid .. @prefixes.fast)")
	if err != nil {
		t.Fatalf("ParseValue: %v", err)
	}
	// Parentheses are not part of the AST: the value ends right after
	// "fast", i.e. at the column of the closing paren.
	want := len("(@rfid .. @prefixes.fast") // 0-based index of ')'
	if got := v.End().Column - 1; got != want {
		t.Errorf("expression end = %d, want %d (index of the closing paren in %q)", got, want, src)
	}
}
