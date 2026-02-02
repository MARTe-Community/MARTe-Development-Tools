package integration

import (
	"os"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/builder"
	"github.com/marte-community/marte-dev-tools/internal/parser"
)

func TestOperators(t *testing.T) {
	content := `
#var A: int = 10
#var B: int = 20
#var S1: string = "Hello"
#var S2: string = "World"
#var FA: float = 1.5
#var FB: float = 2.0

+Obj = {
    Math = @A + @B
    Precedence = @A + @B * 2
    Concat = @S1 .. " " .. @S2
    FloatMath = @FA + @FB
    Mix = @A + @FA
    ConcatNum = "Num: " .. @A
    ConcatFloat = "F: " .. @FA
    ConcatArr = "A: " .. { 1 }
    BoolVal = true
    RefVal = Obj
    ArrVal = { 1 2 }
    Unres = @Unknown
    InvalidMath = "A" + 1
}
`
	// Check Parser
	p := parser.NewParser(content)
	_, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	// Check Builder Output
	f, _ := os.CreateTemp("", "ops.marte")
	f.WriteString(content)
	f.Close()
	defer os.Remove(f.Name())

	b := builder.NewBuilder([]string{f.Name()}, nil)

	outF, _ := os.CreateTemp("", "out.marte")
	defer os.Remove(outF.Name())
	b.Build(outF)
	outF.Close()

	outContent, _ := os.ReadFile(outF.Name())
	outStr := string(outContent)

	if !strings.Contains(outStr, "Math = 30") {
		t.Errorf("Math failed. Got:\n%s", outStr)
	}
	// 10 + 20 * 2 = 50
	if !strings.Contains(outStr, "Precedence = 50") {
		t.Errorf("Precedence failed. Got:\n%s", outStr)
	}
	if !strings.Contains(outStr, "Concat = \"Hello World\"") {
		t.Errorf("Concat failed. Got:\n%s", outStr)
	}
	if !strings.Contains(outStr, "FloatMath = 3.5") {
		t.Errorf("FloatMath failed. Got:\n%s", outStr)
	}
	// 10 + 1.5 = 11.5
	if !strings.Contains(outStr, "Mix = 11.5") {
		t.Errorf("Mix failed. Got:\n%s", outStr)
	}
	if !strings.Contains(outStr, "ConcatNum = \"Num: 10\"") {
		t.Errorf("ConcatNum failed. Got:\n%s", outStr)
	}
	if !strings.Contains(outStr, "BoolVal = true") {
		t.Errorf("BoolVal failed. Got:\n%s", outStr)
	}
	if !strings.Contains(outStr, "RefVal = Obj") {
		t.Errorf("RefVal failed. Got:\n%s", outStr)
	}
	if !strings.Contains(outStr, "ArrVal = { 1 2 }") {
		t.Errorf("ArrVal failed. Got:\n%s", outStr)
	}
	if !strings.Contains(outStr, "Unres = @Unknown") {
		t.Errorf("Unres failed. Got:\n%s", outStr)
	}
}