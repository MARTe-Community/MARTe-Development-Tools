package integration

import (
	"strings"
	"testing"

	"github.com/marte-dev/marte-dev-tools/internal/index"
	"github.com/marte-dev/marte-dev-tools/internal/parser"
	"github.com/marte-dev/marte-dev-tools/internal/validator"
)

func TestMDSWriterValidation(t *testing.T) {
	// MDSWriter requires TreeName, NumberOfBuffers, etc.
	content := `
+MyMDSWriter = {
    Class = MDSWriter
    NumberOfBuffers = 10
    CPUMask = 1
    StackSize = 1000000
    // Missing TreeName
    StoreOnTrigger = 0
    EventName = "Update"
    TimeRefresh = 1.0
    +Signals = {}
}
`
	p := parser.NewParser(content)
	config, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	idx := index.NewProjectTree()
	idx.AddFile("mdswriter.marte", config)

	v := validator.NewValidator(idx, ".")
	v.ValidateProject()

	found := false
	for _, d := range v.Diagnostics {
		if strings.Contains(d.Message, "Missing mandatory field 'TreeName'") {
			found = true
			break
		}
	}

	if !found {
		t.Error("Expected error for missing 'TreeName' in MDSWriter")
	}
}

func TestMathExpressionGAMValidation(t *testing.T) {
	// MathExpressionGAM requires Expression
	content := `
+MyMath = {
    Class = MathExpressionGAM
    // Missing Expression
}
`
	p := parser.NewParser(content)
	config, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	idx := index.NewProjectTree()
	idx.AddFile("math.marte", config)

	v := validator.NewValidator(idx, ".")
	v.ValidateProject()

	found := false
	for _, d := range v.Diagnostics {
		if strings.Contains(d.Message, "Missing mandatory field 'Expression'") {
			found = true
			break
		}
	}

	if !found {
		t.Error("Expected error for missing 'Expression' in MathExpressionGAM")
	}
}
