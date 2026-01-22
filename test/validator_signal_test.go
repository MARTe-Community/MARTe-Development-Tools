package integration

import (
	"strings"
	"testing"

	"github.com/marte-dev/marte-dev-tools/internal/index"
	"github.com/marte-dev/marte-dev-tools/internal/parser"
	"github.com/marte-dev/marte-dev-tools/internal/validator"
)

func TestSignalValidation(t *testing.T) {
	content := `
+Data = {
    Class = ReferenceContainer
    +ValidDS = {
        Class = DataSource
        Signals = {
            ValidSig = {
                Type = uint32
            }
        }
    }
    +MissingTypeDS = {
        Class = DataSource
        Signals = {
            InvalidSig = {
                // Missing Type
                Dummy = 1
            }
        }
    }
    +InvalidTypeDS = {
        Class = DataSource
        Signals = {
            InvalidSig = {
                Type = invalid_type
            }
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
	idx.AddFile("signal_test.marte", config)

	v := validator.NewValidator(idx, ".")
	v.ValidateProject()

	foundMissing := false
	foundInvalid := false

	for _, d := range v.Diagnostics {
		if strings.Contains(d.Message, "missing mandatory field 'Type'") {
			foundMissing = true
		}
		if strings.Contains(d.Message, "Invalid Type 'invalid_type'") {
			foundInvalid = true
		}
	}

	if !foundMissing {
		t.Error("Expected error for missing Type field in Signal")
	}
	if !foundInvalid {
		t.Error("Expected error for invalid Type value in Signal")
	}
}
