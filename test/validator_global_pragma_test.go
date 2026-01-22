package integration

import (
	"strings"
	"testing"

	"github.com/marte-dev/marte-dev-tools/internal/index"
	"github.com/marte-dev/marte-dev-tools/internal/parser"
	"github.com/marte-dev/marte-dev-tools/internal/validator"
)

func TestGlobalPragma(t *testing.T) {
	content := `
//!allow(unused): Suppress all unused
//!allow(implicit): Suppress all implicit

+Data = {
    Class = ReferenceContainer
    +MyDS = {
        Class = FileReader
        Filename = "test"
        Signals = {
            UnusedSig = { Type = uint32 }
        }
    }
}

+MyGAM = {
    Class = IOGAM
    InputSignals = {
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
	idx.AddFile("global_pragma.marte", config)
	idx.ResolveReferences()

	v := validator.NewValidator(idx, ".")
	v.ValidateProject()
	v.CheckUnused()

	foundUnusedWarning := false
	foundImplicitWarning := false

	for _, d := range v.Diagnostics {
		if strings.Contains(d.Message, "Unused Signal") {
			foundUnusedWarning = true
		}
		if strings.Contains(d.Message, "Implicitly Defined Signal") {
			foundImplicitWarning = true
		}
	}

	if foundUnusedWarning {
		t.Error("Expected warning for UnusedSig to be suppressed globally")
	}
	if foundImplicitWarning {
		t.Error("Expected warning for ImplicitSig to be suppressed globally")
	}
}
