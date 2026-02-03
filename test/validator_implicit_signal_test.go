package integration

import (
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/validator"
)

func TestImplicitSignal(t *testing.T) {
	content := `
+Data = {
    Class = ReferenceContainer
    +MyDS = {
        Class = FileReader
        Filename = "test"
        Signals = {
            ExplicitSig = { Type = uint32 }
        }
    }
}

+MyGAM = {
    Class = IOGAM
    InputSignals = {
        ExplicitSig = {
            DataSource = MyDS
            Type = uint32
        }
        ImplicitSig = {
            DataSource = MyDS
            Type = uint32
        }
    }
}
`
	p := parser.NewParser(content)
	config, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	idx := index.NewProjectTree()
	idx.AddFile("implicit_signal.marte", config)
	idx.ResolveReferences()

	v := validator.NewValidator(idx, ".", nil)
	v.ValidateProject()

	foundWarning := false
	foundError := false

	for _, d := range v.Diagnostics {
		if strings.Contains(d.Message, "Implicitly Defined Signal") {
			if strings.Contains(d.Message, "ImplicitSig") {
				foundWarning = true
			}
		}
		if strings.Contains(d.Message, "Signal 'ExplicitSig' not found") {
			foundError = true
		}
	}

	if !foundWarning || foundError {
		for _, d := range v.Diagnostics {
			t.Logf("Diagnostic: %s", d.Message)
		}
	}

	if !foundWarning {
		t.Error("Expected warning for ImplicitSig")
	}
	if foundError {
		t.Error("Unexpected error for ExplicitSig")
	}

	// Test missing Type for implicit
	contentMissingType := `
+Data = { Class = ReferenceContainer +DS={Class=FileReader Filename="" Signals={}} }
+GAM = { Class = IOGAM InputSignals = { Impl = { DataSource = DS } } }
`
	p2 := parser.NewParser(contentMissingType)
	config2, err2 := p2.Parse()
	if err2 != nil {
		t.Fatalf("Parse2 failed: %v", err2)
	}
	idx2 := index.NewProjectTree()
	idx2.AddFile("missing_type.marte", config2)
	idx2.ResolveReferences()
	v2 := validator.NewValidator(idx2, ".", nil)
	v2.ValidateProject()

	foundTypeErr := false
	for _, d := range v2.Diagnostics {
		if strings.Contains(d.Message, "Implicit signal 'Impl' must define Type") {
			foundTypeErr = true
		}
	}
	if !foundTypeErr {
		for _, d := range v2.Diagnostics {
			t.Logf("Diagnostic2: %s", d.Message)
		}
		t.Error("Expected error for missing Type in implicit signal")
	}
}
