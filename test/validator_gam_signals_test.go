package integration

import (
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/validator"
)

func TestGAMSignalValidation(t *testing.T) {
	content := `
+Data = {
    Class = ReferenceContainer
    +InDS = {
        Class = FileReader
        Signals = {
            SigIn = { Type = uint32 }
        }
    }
    +OutDS = {
        Class = FileWriter
        Signals = {
            SigOut = { Type = uint32 }
        }
    }
}

+MyGAM = {
    Class = IOGAM
    InputSignals = {
        SigIn = {
            DataSource = InDS
            Type = uint32
        }
        // Error: OutDS is OUT only
        BadInput = {
            DataSource = OutDS
            Alias = SigOut
            Type = uint32
        }
        // Error: MissingSig not in InDS
        Missing = {
            DataSource = InDS
            Alias = MissingSig
            Type = uint32
        }
    }
    OutputSignals = {
        SigOut = {
            DataSource = OutDS
            Type = uint32
        }
        // Error: InDS is IN only
        BadOutput = {
            DataSource = InDS
            Alias = SigIn
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
	idx.AddFile("gam_signals.marte", config)
	idx.ResolveReferences()

	v := validator.NewValidator(idx, ".", nil)
	v.ValidateProject()

	foundBadInput := false
	foundMissing := false
	foundBadOutput := false

	for _, d := range v.Diagnostics {
		if strings.Contains(d.Message, "DataSource 'OutDS' (Class FileWriter) is Output-only but referenced in InputSignals") {
			foundBadInput = true
		}
		if strings.Contains(d.Message, "Implicitly Defined Signal: 'MissingSig'") {
			foundMissing = true
		}
		if strings.Contains(d.Message, "DataSource 'InDS' (Class FileReader) is Input-only but referenced in OutputSignals") {
			foundBadOutput = true
		}
	}

	if !foundBadInput || !foundMissing || !foundBadOutput {
		for _, d := range v.Diagnostics {
			t.Logf("Diagnostic: %s", d.Message)
		}
	}

	if !foundBadInput {
		t.Error("Expected error for OutDS in InputSignals")
	}
	if !foundMissing {
		t.Error("Expected error for missing signal reference")
	}
	if !foundBadOutput {
		t.Error("Expected error for InDS in OutputSignals")
	}
}
