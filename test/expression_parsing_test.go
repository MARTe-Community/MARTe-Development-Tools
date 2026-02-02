package integration

import (
	"os"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/builder"
)

func TestExpressionParsing(t *testing.T) {
	content := `
#var A: int = 10
#var B: int = 2

+Obj = {
    // 1. Multiple variables
    Expr1 = @A + @B + @A
    
    // 2. Brackets
    Expr2 = (@A + 2) * @B
    
    // 3. No space operator (variable name strictness)
    Expr3 = @A-2
}
`
	f, _ := os.CreateTemp("", "expr_test.marte")
	f.WriteString(content)
	f.Close()
	defer os.Remove(f.Name())

	b := builder.NewBuilder([]string{f.Name()}, nil)

	outF, _ := os.CreateTemp("", "out.marte")
	defer os.Remove(outF.Name())
	
	err := b.Build(outF)
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}
	outF.Close()

	outContent, _ := os.ReadFile(outF.Name())
	outStr := string(outContent)

	// Expr1: 10 + 2 + 10 = 22
	if !strings.Contains(outStr, "Expr1 = 22") {
		t.Errorf("Expr1 failed. Got:\n%s", outStr)
	}
	
	// Expr2: (10 + 2) * 2 = 24
	if !strings.Contains(outStr, "Expr2 = 24") {
		t.Errorf("Expr2 failed. Got:\n%s", outStr)
	}
	
	// Expr3: 10 - 2 = 8
	if !strings.Contains(outStr, "Expr3 = 8") {
		t.Errorf("Expr3 failed. Got:\n%s", outStr)
	}
}
