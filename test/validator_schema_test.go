package integration

import (
	"strings"
	"testing"

	"github.com/marte-dev/marte-dev-tools/internal/index"
	"github.com/marte-dev/marte-dev-tools/internal/parser"
	"github.com/marte-dev/marte-dev-tools/internal/validator"
)

func TestSchemaValidationType(t *testing.T) {
	// OrderedClass: First (int), Second (string)
	content := `
+Obj = {
    Class = OrderedClass
    First = "WrongType" 
    Second = "Correct"
}
`
	p := parser.NewParser(content)
	config, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	idx := index.NewProjectTree()
	idx.AddFile("test.marte", config)

	v := validator.NewValidator(idx, ".")
	v.ValidateProject()

	found := false
	for _, d := range v.Diagnostics {
		if strings.Contains(d.Message, "mismatched types") {
			found = true
			break
		}
	}

	if !found {
		t.Error("Expected error for wrong type in field 'First', but found none")
	}
}

func TestSchemaValidationOrder(t *testing.T) {
	// OrderedClass: First, Second (ordered=true)
	content := `
+Obj = {
    Class = OrderedClass
    Second = "Correct"
    First = 1
}
`
	p := parser.NewParser(content)
	config, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	idx := index.NewProjectTree()
	idx.AddFile("test.marte", config)

	v := validator.NewValidator(idx, ".")
	v.ValidateProject()

	found := false
	for _, d := range v.Diagnostics {
		if strings.Contains(d.Message, "Field 'First' is out of order") {
			found = true
			break
		}
	}

	if found {
		t.Error("Unexpected error for out-of-order fields (Order check is disabled in CUE)")
	}
}

func TestSchemaValidationMandatoryNode(t *testing.T) {
	// StateMachine requires "States" which is usually a node (+States or $States)
	content := `
+MySM = {
    Class = StateMachine
    +States = {}
}
`
	p := parser.NewParser(content)
	config, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	idx := index.NewProjectTree()
	idx.AddFile("test.marte", config)

	v := validator.NewValidator(idx, ".")
	v.ValidateProject()

	for _, d := range v.Diagnostics {
		if strings.Contains(d.Message, "Missing mandatory field 'States'") {
			t.Error("Reported missing mandatory field 'States' despite +States being present as a child node")
		}
	}
}
