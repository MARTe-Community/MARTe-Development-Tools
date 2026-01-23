package integration

import (
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/validator"
)

func TestIgnorePragma(t *testing.T) {
	content := `
//!ignore(unused): Suppress global unused
+Data = {
    Class = ReferenceContainer
    +MyDS = {
        Class = FileReader
        Filename = "test"
        Signals = {
            Unused1 = { Type = uint32 }
            
            //!ignore(unused): Suppress local unused
            Unused2 = { Type = uint32 }
        }
    }
}

+MyGAM = {
    Class = IOGAM
    InputSignals = {
        //!ignore(implicit): Suppress local implicit
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
	idx.AddFile("ignore.marte", config)
	idx.ResolveReferences()

	v := validator.NewValidator(idx, ".")
	v.ValidateProject()
	v.CheckUnused()

	for _, d := range v.Diagnostics {
		if strings.Contains(d.Message, "Unused Signal") {
			t.Errorf("Unexpected warning: %s", d.Message)
		}
		if strings.Contains(d.Message, "Implicitly Defined Signal") {
			t.Errorf("Unexpected warning: %s", d.Message)
		}
	}
}
