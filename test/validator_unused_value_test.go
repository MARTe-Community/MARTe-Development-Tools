package integration

import (
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/validator"
)

func TestUnusedGAMValueValidation(t *testing.T) {
	content := `
+Data = {
    Class = ReferenceContainer
    +DS = { Class = GAMDataSource Signals = { S = { Type = uint8 } } }
}
+UnusedGAM = {
    Class = IOGAM
    InputSignals = {
        S = { DataSource = DS Type = uint8 Value = 1024 }
    }
}
+App = { Class = RealTimeApplication }
`
	pt := index.NewProjectTree()
	p := parser.NewParser(content)
	cfg, err := p.Parse()
	if err != nil {
		t.Fatal(err)
	}
	pt.AddFile("unused.marte", cfg)

	v := validator.NewValidator(pt, ".")
	v.ValidateProject()

	found := false
	for _, d := range v.Diagnostics {
		if strings.Contains(d.Message, "Value initialization mismatch") {
			found = true
		}
	}
	if !found {
		t.Error("Expected Value initialization mismatch error for unused GAM")
	}
}
