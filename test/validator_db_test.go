package integration

import (
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/validator"
)

func TestRealTimeApplicationValidation(t *testing.T) {
	// RealTimeApplication requires Functions, Data, States
	content := `
+App = {
    Class = RealTimeApplication
    +Functions = {}
    // Missing Data
    // Missing States
}
`
	p := parser.NewParser(content)
	config, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	idx := index.NewProjectTree()
	idx.AddFile("app.marte", config)

	v := validator.NewValidator(idx, ".")
	v.ValidateProject()

	missingData := false
	missingStates := false

	for _, d := range v.Diagnostics {
		if strings.Contains(d.Message, "Data: field is required") {
			missingData = true
		}
		if strings.Contains(d.Message, "States: field is required") {
			missingStates = true
		}
	}

	if !missingData || !missingStates {
		for _, d := range v.Diagnostics {
			t.Logf("Diagnostic: %s", d.Message)
		}
	}

	if !missingData {
		t.Error("Expected error for missing 'Data' field in RealTimeApplication")
	}
	if !missingStates {
		t.Error("Expected error for missing 'States' field in RealTimeApplication")
	}
}

func TestGAMSchedulerValidation(t *testing.T) {
	// GAMScheduler requires TimingDataSource (reference)
	content := `
+Scheduler = {
    Class = GAMScheduler
    // Missing TimingDataSource
}
`
	p := parser.NewParser(content)
	config, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	idx := index.NewProjectTree()
	idx.AddFile("scheduler.marte", config)

	v := validator.NewValidator(idx, ".")
	v.ValidateProject()

	found := false
	for _, d := range v.Diagnostics {
		if strings.Contains(d.Message, "TimingDataSource: incomplete value") {
			found = true
			break
		}
	}

	if !found {
		t.Error("Expected error for missing 'TimingDataSource' in GAMScheduler")
	}
}
