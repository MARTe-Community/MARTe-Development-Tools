package integration

import (
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/validator"
)

func TestPragmaSuppression(t *testing.T) {
	content := `
+Data = {
    Class = ReferenceContainer
    +MyDS = {
        Class = FileReader
        Filename = "test"
        Signals = {
            //!unused: Ignore this
            UnusedSig = { Type = uint32 }
            UsedSig = { Type = uint32 }
        }
    }
}

+MyGAM = {
    Class = IOGAM
    InputSignals = {
        UsedSig = { DataSource = MyDS Type = uint32 }
        
        //!implicit: Ignore this implicit
        ImplicitSig = { DataSource = MyDS Type = uint32 }
    }
}
`
	p := parser.NewParser(content)
	config, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	idx := index.NewProjectTree()
	idx.AddFile("pragma.marte", config)
	idx.ResolveReferences()

	v := validator.NewValidator(idx, ".")
	v.ValidateProject()
	v.CheckUnused()

	foundUnusedWarning := false
	foundImplicitWarning := false

	for _, d := range v.Diagnostics {
		if strings.Contains(d.Message, "Unused Signal") && strings.Contains(d.Message, "UnusedSig") {
			foundUnusedWarning = true
		}
		if strings.Contains(d.Message, "Implicitly Defined Signal") && strings.Contains(d.Message, "ImplicitSig") {
			foundImplicitWarning = true
		}
	}

	if foundUnusedWarning {
		t.Error("Expected warning for UnusedSig to be suppressed")
	}
	if foundImplicitWarning {
		t.Error("Expected warning for ImplicitSig to be suppressed")
	}
}
