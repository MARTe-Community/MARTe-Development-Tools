package integration

import (
	"strings"
	"testing"

	"github.com/marte-dev/marte-dev-tools/internal/index"
	"github.com/marte-dev/marte-dev-tools/internal/parser"
	"github.com/marte-dev/marte-dev-tools/internal/validator"
)

func TestGlobalPragmaDebug(t *testing.T) {
	content := `//! allow(implicit): Debugging
//! allow(unused): Debugging
+Data={Class=ReferenceContainer}
+GAM={Class=IOGAM InputSignals={Impl={DataSource=Data Type=uint32}}}
+UnusedGAM={Class=IOGAM}
`
	p := parser.NewParser(content)
	config, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
    
    // Check if pragma parsed
    if len(config.Pragmas) == 0 {
        t.Fatal("Pragma not parsed")
    }
    t.Logf("Parsed Pragma 0: %s", config.Pragmas[0].Text)

	idx := index.NewProjectTree()
	idx.AddFile("debug.marte", config)
    idx.ResolveReferences()
    
    // Check if added to GlobalPragmas
    pragmas, ok := idx.GlobalPragmas["debug.marte"]
    if !ok || len(pragmas) == 0 {
        t.Fatal("GlobalPragmas not populated")
    }
    t.Logf("Global Pragma stored: %s", pragmas[0])

	v := validator.NewValidator(idx, ".")
	v.ValidateProject()
	v.CheckUnused() // Must call this for unused check!

	foundImplicitWarning := false
	foundUnusedWarning := false
	for _, d := range v.Diagnostics {
		if strings.Contains(d.Message, "Implicitly Defined Signal") {
			foundImplicitWarning = true
            t.Logf("Found warning: %s", d.Message)
		}
		if strings.Contains(d.Message, "Unused GAM") {
			foundUnusedWarning = true
            t.Logf("Found warning: %s", d.Message)
		}
	}

	if foundImplicitWarning {
		t.Error("Expected implicit warning to be suppressed")
	}
	if foundUnusedWarning {
		t.Error("Expected unused warning to be suppressed")
	}
}
