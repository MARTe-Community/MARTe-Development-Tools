package integration

import (
	"testing"

	"github.com/marte-dev/marte-dev-tools/internal/index"
	"github.com/marte-dev/marte-dev-tools/internal/parser"
	"github.com/marte-dev/marte-dev-tools/internal/validator"
)

func TestUnusedGAM(t *testing.T) {
	content := `
+MyGAM = {
    Class = GAMClass
    +InputSignals = {}
}
+UsedGAM = {
    Class = GAMClass
    +InputSignals = {}
}
$App = {
    $Data = {}
    $States = {
        $State = {
            $Threads = {
                $Thread = {
                    Functions = { UsedGAM }
                }
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
	idx.AddFile("test.marte", config)
	idx.ResolveReferences()

	v := validator.NewValidator(idx)
	v.CheckUnused()

	foundUnused := false
	for _, d := range v.Diagnostics {
		if d.Message == "Unused GAM: +MyGAM is defined but not referenced in any thread or scheduler" {
			foundUnused = true
			break
		}
	}

	if !foundUnused {
		t.Error("Expected warning for unused GAM +MyGAM, but found none")
	}
}

func TestUnusedSignal(t *testing.T) {
	content := `
$App = {
    $Data = {
        +MyDS = {
            Class = DataSourceClass
            Sig1 = { Type = uint32 }
            Sig2 = { Type = uint32 }
        }
    }
}
+MyGAM = {
    Class = GAMClass
    +InputSignals = {
        S1 = { DataSource = MyDS Alias = Sig1 }
    }
}
`
	p := parser.NewParser(content)
	config, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	idx := index.NewProjectTree()
	idx.AddFile("test.marte", config)
	idx.ResolveReferences()

	v := validator.NewValidator(idx)
	v.CheckUnused()

	foundUnusedSig2 := false
	for _, d := range v.Diagnostics {
		if d.Message == "Unused Signal: Sig2 is defined in DataSource +MyDS but never referenced" {
			foundUnusedSig2 = true
			break
		}
	}

	if !foundUnusedSig2 {
		t.Error("Expected warning for unused signal Sig2, but found none")
	}
}
