package integration

import (
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/validator"
)

func TestGAMSignalLinking(t *testing.T) {
	content := `
+Data = {
    Class = ReferenceContainer
    +MyDS = {
        Class = FileReader
        Filename = "test.txt"
        Signals = {
            MySig = { Type = uint32 }
        }
    }
}

+MyGAM = {
    Class = ConversionGAM
    //! ignore(unused)
    InputSignals = {
        MySig = {
            DataSource = MyDS
            Type = uint32
        }
        AliasedSig = {
            Alias = MySig
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
	idx.AddFile("gam_signals_linking.marte", config)
	idx.ResolveReferences()

	v := validator.NewValidator(idx, ".", nil)
	v.ValidateProject()

	if len(v.Diagnostics) > 0 {
		for _, d := range v.Diagnostics {
			t.Logf("Diagnostic: %s", d.Message)
		}
		t.Fatalf("Validation failed with %d issues", len(v.Diagnostics))
	}

	foundMyDSRef := 0
	foundAliasRef := 0

	for _, ref := range idx.References {
		if ref.Name == "MyDS" {
			if ref.Target != nil && ref.Target.RealName == "+MyDS" {
				foundMyDSRef++
			}
		}
		if ref.Name == "MySig" {
			if ref.Target != nil && ref.Target.RealName == "MySig" {
				foundAliasRef++
			}
		}
	}

	if foundMyDSRef < 2 {
		t.Errorf("Expected at least 2 resolved MyDS references, found %d", foundMyDSRef)
	}
	if foundAliasRef < 1 {
		t.Errorf("Expected at least 1 resolved Alias MySig reference, found %d", foundAliasRef)
	}
}
