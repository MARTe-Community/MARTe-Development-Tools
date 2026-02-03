package integration

import (
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/validator"
)

func TestGAMSignalDirectionality(t *testing.T) {
	content := `
$App = {
    $Data = {
        +InDS = { Class = FileReader Filename="f" +Signals = { S1 = { Type = uint32 } } }
        +OutDS = { Class = FileWriter Filename="f" +Signals = { S1 = { Type = uint32 } } }
        +InOutDS = { Class = FileDataSource Filename="f" +Signals = { S1 = { Type = uint32 } } }
    }
    +ValidGAM = {
        Class = IOGAM
        InputSignals = {
            S1 = { DataSource = InDS }
            S2 = { DataSource = InOutDS Alias = S1 }
        }
        OutputSignals = {
            S3 = { DataSource = OutDS Alias = S1 }
            S4 = { DataSource = InOutDS Alias = S1 }
        }
    }
    +InvalidGAM = {
        Class = IOGAM
        InputSignals = {
            BadIn = { DataSource = OutDS Alias = S1 }
        }
        OutputSignals = {
            BadOut = { DataSource = InDS Alias = S1 }
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
	idx.AddFile("dir.marte", config)
	idx.ResolveReferences()

	v := validator.NewValidator(idx, ".", nil)
	v.ValidateProject()

	// Check ValidGAM has NO directionality errors
	for _, d := range v.Diagnostics {
		if strings.Contains(d.Message, "is Output-only but referenced in InputSignals") ||
			strings.Contains(d.Message, "is Input-only but referenced in OutputSignals") {
			if strings.Contains(d.Message, "ValidGAM") {
				t.Errorf("Unexpected direction error for ValidGAM: %s", d.Message)
			}
		}
	}

	// Check InvalidGAM HAS errors
	foundBadIn := false
	foundBadOut := false
	for _, d := range v.Diagnostics {
		if strings.Contains(d.Message, "InvalidGAM") {
			if strings.Contains(d.Message, "is Output-only but referenced in InputSignals") {
				foundBadIn = true
			}
			if strings.Contains(d.Message, "is Input-only but referenced in OutputSignals") {
				foundBadOut = true
			}
		}
	}

	if !foundBadIn {
		t.Error("Expected error for OutDS in InputSignals of InvalidGAM")
	}
	if !foundBadOut {
		t.Error("Expected error for InDS in OutputSignals of InvalidGAM")
	}
}
