package integration

import (
	"context"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/schema"
	"github.com/marte-community/marte-dev-tools/internal/validator"
)

func TestValidatorExpressionCoverage(t *testing.T) {
	content := `
#var A: int = 10
#var B: int = 5
#var S1: string = "Hello"
#var S2: string = "World"

// Valid cases (execution hits evaluateBinary)
#var Sum: int = @A + @B // 15
#var Sub: int = @A - @B // 5
#var Mul: int = @A * @B // 50
#var Div: int = @A / @B // 2
#var Mod: int = @A % 3  // 1
#var Concat: string = @S1 .. " " .. @S2 // "Hello World"
#var Unary: int = -@A // -10
#var BitAnd: int = 10 & 5
#var BitOr: int = 10 | 5
#var BitXor: int = 10 ^ 5

#var FA: float = 1.5
#var FB: float = 2.0
#var FSum: float = @FA + @FB // 3.5
#var FSub: float = @FB - @FA // 0.5
#var FMul: float = @FA * @FB // 3.0
#var FDiv: float = @FB / @FA // 1.333...

#var BT: bool = true
#var BF: bool = !@BT

// Invalid cases (should error)
#var BadSum: int & > 20 = @A + @B // 15, should fail
#var BadUnary: bool = !10 // Should fail type check (nil result from evaluateUnary)
#var StrVar: string = "DS"

+InvalidDS = {
    Class = IOGAM
    InputSignals = {
        S = { DataSource = 10 } // Int coverage
        S2 = { DataSource = 1.5 } // Float coverage
        S3 = { DataSource = true } // Bool coverage
        S4 = { DataSource = @StrVar } // VarRef coverage -> String
        S5 = { DataSource = { 1 } } // Array coverage (default case)
    }
    OutputSignals = {}
}
`
	pt := index.NewProjectTree()
	p := parser.NewParser(content)
	cfg, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	pt.AddFile("expr.marte", cfg)
	pt.ResolveReferences()

	v := validator.NewValidator(pt, ".", nil)
	// Use NewSchema to ensure basic types
	v.Schema = schema.NewSchema()

	v.CheckVariables(context.Background())

	// Check for expected errors
	foundBadSum := false
	for _, diag := range v.Diagnostics {
		if strings.Contains(diag.Message, "BadSum") && strings.Contains(diag.Message, "value mismatch") {
			foundBadSum = true
		}
	}
	if !foundBadSum {
		t.Error("Expected error for BadSum")
	}
}
