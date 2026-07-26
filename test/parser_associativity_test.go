package integration

import (
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/parser"
)

// TestParserLeftAssociativeSubtraction is a regression test for BUG-004:
// binary operators that should be left-associative (e.g., `-`, `/`) may
// be parsed with wrong associativity in the Pratt expression parser,
// producing incorrect AST trees for chained operations.
//
// `a - b - c` MUST parse as ((a - b) - c), NOT (a - (b - c)).
func TestParserLeftAssociativeSubtraction(t *testing.T) {
	content := "Field = a - b - c"
	p := parser.NewParser(content)
	cfg, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(cfg.Definitions) != 1 {
		t.Fatalf("Expected 1 definition, got %d", len(cfg.Definitions))
	}

	fld, ok := cfg.Definitions[0].(*parser.Field)
	if !ok {
		t.Fatalf("Expected *parser.Field, got %T", cfg.Definitions[0])
	}

	// The top-level expression should be a BinaryExpression with operator `-`
	outer, ok := fld.Value.(*parser.BinaryExpression)
	if !ok {
		t.Fatalf("Expected top-level *parser.BinaryExpression, got %T", fld.Value)
	}
	if outer.Operator.Value != "-" {
		t.Fatalf("Expected outer operator '-', got %q", outer.Operator.Value)
	}

	// LEFT child should be another subtraction: (a - b)
	left, ok := outer.Left.(*parser.BinaryExpression)
	if !ok {
		t.Fatalf("Expected left child to be *parser.BinaryExpression, got %T -- expression is right-associative (BUG: should be left-associative)", outer.Left)
	}
	if left.Operator.Value != "-" {
		t.Fatalf("Expected left operator '-', got %q", left.Operator.Value)
	}

	// LEFT.LEFT should be the reference 'a'
	leftLeft, ok := left.Left.(*parser.ReferenceValue)
	if !ok {
		t.Fatalf("Expected left.Left to be *parser.ReferenceValue (a), got %T", left.Left)
	}
	if leftLeft.Value != "a" {
		t.Errorf("Expected 'a', got %q", leftLeft.Value)
	}

	// LEFT.RIGHT should be the reference 'b'
	leftRight, ok := left.Right.(*parser.ReferenceValue)
	if !ok {
		t.Fatalf("Expected left.Right to be *parser.ReferenceValue (b), got %T", left.Right)
	}
	if leftRight.Value != "b" {
		t.Errorf("Expected 'b', got %q", leftRight.Value)
	}

	// RIGHT of outer should be 'c' (not another expression)
	right, ok := outer.Right.(*parser.ReferenceValue)
	if !ok {
		t.Fatalf("Expected outer.Right to be *parser.ReferenceValue (c), got %T -- expression may be right-associative", outer.Right)
	}
	if right.Value != "c" {
		t.Errorf("Expected 'c', got %q", right.Value)
	}
}

// TestParserLeftAssociativeDivision checks `/` operator associativity.
func TestParserLeftAssociativeDivision(t *testing.T) {
	content := "Field = x / y / z"
	p := parser.NewParser(content)
	cfg, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	fld := cfg.Definitions[0].(*parser.Field)
	outer := fld.Value.(*parser.BinaryExpression)

	if outer.Operator.Value != "/" {
		t.Fatalf("Expected '/', got %q", outer.Operator.Value)
	}

	// LEFT must be (x / y), not reference x
	left, ok := outer.Left.(*parser.BinaryExpression)
	if !ok {
		t.Fatalf("Expected left child to be *parser.BinaryExpression, got %T -- division is right-associative (BUG)", outer.Left)
	}
	if left.Operator.Value != "/" {
		t.Fatalf("Expected left operator '/', got %q", left.Operator.Value)
	}

	// RIGHT must be 'z'
	if right, ok := outer.Right.(*parser.ReferenceValue); !ok || right.Value != "z" {
		t.Fatalf("Expected outer.Right to be reference 'z', got %T", outer.Right)
	}
}

// TestParserLeftAssociativeAddition checks that `+` remains left-associative.
func TestParserLeftAssociativeAddition(t *testing.T) {
	content := "Field = 1 + 2 + 3"
	p := parser.NewParser(content)
	cfg, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	fld := cfg.Definitions[0].(*parser.Field)
	outer := fld.Value.(*parser.BinaryExpression)

	if outer.Operator.Value != "+" {
		t.Fatalf("Expected '+', got %q", outer.Operator.Value)
	}

	left, ok := outer.Left.(*parser.BinaryExpression)
	if !ok {
		t.Fatalf("Expected left child to be *parser.BinaryExpression, got %T -- addition is right-associative (BUG)", outer.Left)
	}
	if left.Operator.Value != "+" {
		t.Fatalf("Expected left operator '+', got %q", left.Operator.Value)
	}
}

// TestParserMixedPrecedence checks that `*` binds tighter than `+` regardless
// of associativity.
func TestParserMixedPrecedence(t *testing.T) {
	// a + b * c  MUST parse as  a + (b * c), NOT (a + b) * c
	content := "Field = a + b * c"
	p := parser.NewParser(content)
	cfg, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	fld := cfg.Definitions[0].(*parser.Field)
	outer := fld.Value.(*parser.BinaryExpression)

	if outer.Operator.Value != "+" {
		t.Fatalf("Expected top-level operator '+', got %q", outer.Operator.Value)
	}

	// Left must be 'a' (reference), not another expression
	if left, ok := outer.Left.(*parser.ReferenceValue); !ok || left.Value != "a" {
		t.Fatalf("Expected outer.Left to be reference 'a', got %T (precedence bug: + binding tighter than *)", outer.Left)
	}

	// Right must be (b * c)
	right, ok := outer.Right.(*parser.BinaryExpression)
	if !ok {
		t.Fatalf("Expected outer.Right to be *parser.BinaryExpression (b * c), got %T", outer.Right)
	}
	if right.Operator.Value != "*" {
		t.Fatalf("Expected right operator '*', got %q", right.Operator.Value)
	}
}
