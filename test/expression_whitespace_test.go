package integration

import (
	"os"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/builder"
)

func TestExpressionWhitespace(t *testing.T) {
	content := `
+Obj = {
    NoSpace = 2+2
    WithSpace = 2 + 2
}
`
	f, _ := os.CreateTemp("", "expr_ws.marte")
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

	if !strings.Contains(outStr, "NoSpace = 4") {
		t.Errorf("NoSpace failed. Got:\n%s", outStr)
	}
	if !strings.Contains(outStr, "WithSpace = 4") {
		t.Errorf("WithSpace failed. Got:\n%s", outStr)
	}
}
