package integration

import (
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/validator"
)

func TestSchemaMetaValidation(t *testing.T) {
	// 1. Valid Usage
	validContent := `
+App = {
    Class = RealTimeApplication
    Functions = { Class = ReferenceContainer }
    Data = { Class = ReferenceContainer DefaultDataSource = "DS" }
    States = { Class = ReferenceContainer }
    Scheduler = { Class = GAMScheduler TimingDataSource = "DS" }
    #meta = {
        multithreaded = true
    }
}
`
	pt := index.NewProjectTree()
	p := parser.NewParser(validContent)
	cfg, err := p.Parse()
	if err != nil {
		t.Fatal(err)
	}
	pt.AddFile("valid.marte", cfg)
	
	v := validator.NewValidator(pt, "") 
	v.ValidateProject()
	
	if len(v.Diagnostics) > 0 {
		for _, d := range v.Diagnostics {
			t.Logf("Diag: %s", d.Message)
		}
		t.Errorf("Expected no errors for valid #meta")
	}

	// 2. Invalid Usage (Wrong Type)
	invalidContent := `
+App = {
    Class = RealTimeApplication
    Functions = { Class = ReferenceContainer }
    Data = { Class = ReferenceContainer DefaultDataSource = "DS" }
    States = { Class = ReferenceContainer }
    Scheduler = { Class = GAMScheduler TimingDataSource = "DS" }
    #meta = {
        multithreaded = "yes" // Should be bool
    }
}
`
	pt2 := index.NewProjectTree()
	p2 := parser.NewParser(invalidContent)
	cfg2, _ := p2.Parse()
	pt2.AddFile("invalid.marte", cfg2)
	
	v2 := validator.NewValidator(pt2, "")
	v2.ValidateProject()
	
	foundError := false
	for _, d := range v2.Diagnostics {
		// CUE validation error message
		if strings.Contains(d.Message, "mismatched types") || strings.Contains(d.Message, "conflicting values") {
			foundError = true
		}
	}
	
	if !foundError {
		t.Error("Expected error for invalid #meta type, got nothing")
		for _, d := range v2.Diagnostics {
			t.Logf("Diag: %s", d.Message)
		}
	}
}
