package integration

import (
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/validator"
)

func TestEvaluatedSignalProperties(t *testing.T) {
	content := `
#let N: uint32 = 10
+DS = {
    Class = FileReader
    Filename = "test.bin"
    Signals = {
        Sig1 = { Type = uint32 NumberOfElements = @N }
    }
}
+GAM = {
    Class = IOGAM
    InputSignals = {
        Sig1 = { DataSource = DS Type = uint32 NumberOfElements = 10 }
    }
}
`
	p := parser.NewParser(content)
	cfg, err := p.Parse()
	if err != nil {
		t.Fatal(err)
	}

	tree := index.NewProjectTree()
	tree.AddFile("test.marte", cfg)
	tree.ResolveReferences()

	v := validator.NewValidator(tree, ".")
	v.ValidateProject()

	// There should be no errors because @N evaluates to 10
	for _, d := range v.Diagnostics {
		if d.Level == validator.LevelError {
			t.Errorf("Unexpected error: %s", d.Message)
		}
	}

	// Test mismatch with expression
	contentErr := `
#let N: uint32 = 10
+DS = {
    Class = FileReader
    Filename = "test.bin"
    Signals = {
        Sig1 = { Type = uint32 NumberOfElements = @N + 5 }
    }
}
+GAM = {
    Class = IOGAM
    InputSignals = {
        Sig1 = { DataSource = DS Type = uint32 NumberOfElements = 10 }
    }
}
`
	p2 := parser.NewParser(contentErr)
	cfg2, _ := p2.Parse()
	tree2 := index.NewProjectTree()
	tree2.AddFile("test_err.marte", cfg2)
	tree2.ResolveReferences()

	v2 := validator.NewValidator(tree2, ".")
	v2.ValidateProject()

	found := false
	for _, d := range v2.Diagnostics {
		if strings.Contains(d.Message, "property 'NumberOfElements' mismatch") {
			found = true
			if !strings.Contains(d.Message, "defined '15'") {
				t.Errorf("Expected defined '15', got message: %s", d.Message)
			}
			break
		}
	}
	if !found {
		t.Error("Expected property mismatch error for @N + 5")
	}
}