package integration

import (
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/validator"
)

func TestValidatorArrayRecursion(t *testing.T) {
	content := `
+Obj = {
    Class = "MyClass"
    // Array containing references and expressions with references
    Arr = { ValidRef, UnresolvedRef, ValidRef + 1, UnresolvedRef2 + 1 }
}
+ValidRef = { Class = "C" }
`
	p := parser.NewParser(content)
	config, _ := p.Parse()

	idx := index.NewProjectTree()
	idx.AddFile("test.marte", config)

	v := validator.NewValidator(idx, ".", nil)
	v.ValidateProject()

	foundUnresolvedRef := false
	foundUnresolvedRef2 := false

	for _, d := range v.Diagnostics {
		if strings.Contains(d.Message, "Unknown reference 'UnresolvedRef'") {
			foundUnresolvedRef = true
		}
		if strings.Contains(d.Message, "Unknown reference 'UnresolvedRef2'") {
			foundUnresolvedRef2 = true
		}
	}

	if !foundUnresolvedRef {
		t.Error("Did not find diagnostic for 'UnresolvedRef' in array")
	}
	if !foundUnresolvedRef2 {
		t.Error("Did not find diagnostic for 'UnresolvedRef2' in expression inside array")
	}
}
