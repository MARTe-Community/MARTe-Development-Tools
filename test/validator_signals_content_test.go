package integration

import (
	"strings"
	"testing"

	"github.com/marte-dev/marte-dev-tools/internal/index"
	"github.com/marte-dev/marte-dev-tools/internal/parser"
	"github.com/marte-dev/marte-dev-tools/internal/validator"
)

func TestSignalsContentValidation(t *testing.T) {
	content := `
+Data = {
    Class = ReferenceContainer
    +BadDS = {
        Class = DataSource
        Signals = {
            BadField = 1
            BadArray = { 1 2 }
            // Valid signal
            ValidSig = {
                Type = uint32
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
	idx.AddFile("signals_content.marte", config)

	v := validator.NewValidator(idx, ".")
	v.ValidateProject()

	foundBadField := false
	foundBadArray := false

	for _, d := range v.Diagnostics {
		if strings.Contains(d.Message, "Field 'BadField' is not allowed") {
			foundBadField = true
		}
		if strings.Contains(d.Message, "Field 'BadArray' is not allowed") {
			foundBadArray = true
		}
	}

	if !foundBadField {
		t.Error("Expected error for BadField in Signals")
	}
	if !foundBadArray {
		t.Error("Expected error for BadArray in Signals")
	}
}
