package integration

import (
	"strings"
	"testing"

	"github.com/marte-dev/marte-dev-tools/internal/index"
	"github.com/marte-dev/marte-dev-tools/internal/parser"
	"github.com/marte-dev/marte-dev-tools/internal/validator"
)

func TestFunctionsArrayValidation(t *testing.T) {
	content := `
+App = {
    Class = RealTimeApplication
    +State = {
        Class = RealTimeState
        +Thread = {
            Class = RealTimeThread
            Functions = {
                ValidGAM,
                InvalidGAM, // Not a GAM (DataSource)
                MissingGAM, // Not found
                "String",   // Not reference
            }
        }
    }
}

+ValidGAM = { Class = IOGAM InputSignals = {} }
+InvalidGAM = { Class = FileReader }
`
	p := parser.NewParser(content)
	config, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	idx := index.NewProjectTree()
	idx.AddFile("funcs.marte", config)
	idx.ResolveReferences()

	v := validator.NewValidator(idx, ".")
	v.ValidateProject()

	foundInvalid := false
	foundMissing := false
	foundNotRef := false

	for _, d := range v.Diagnostics {
		if strings.Contains(d.Message, "not found or is not a valid GAM") {
			// This covers both InvalidGAM and MissingGAM cases
			if strings.Contains(d.Message, "InvalidGAM") {
				foundInvalid = true
			}
			if strings.Contains(d.Message, "MissingGAM") {
				foundMissing = true
			}
		}
		if strings.Contains(d.Message, "must contain references") {
			foundNotRef = true
		}
	}

	if !foundInvalid {
		t.Error("Expected error for InvalidGAM")
	}
	if !foundMissing {
		t.Error("Expected error for MissingGAM")
	}
	if !foundNotRef {
		t.Error("Expected error for non-reference element")
	}
}
