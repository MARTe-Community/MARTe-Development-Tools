package integration

import (
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/lsp"
	"github.com/marte-community/marte-dev-tools/internal/parser"
)

func TestHoverGAMUsage(t *testing.T) {
	// Setup
	lsp.ResetTestServer()
	// Documents reset via ResetTestServer

	content := `
+DS1 = {
    Class = FileReader
    +Signals = {
        Sig1 = { Type = uint32 }
    }
}
+GAM1 = {
    Class = IOGAM
    +InputSignals = {
        S1 = {
            DataSource = DS1
            Alias = Sig1
        }
    }
}
+GAM2 = {
    Class = IOGAM
    +OutputSignals = {
        S2 = {
             DataSource = DS1
             Alias = Sig1
        }
    }
}
`
	uri := "file://test_gam_usage.marte"
	lsp.GetTestDocuments()[uri] = content
	p := parser.NewParser(content)
	cfg, err := p.Parse()
	if err != nil {
		t.Fatal(err)
	}
	lsp.GetTestTree().AddFile("test_gam_usage.marte", cfg)
	lsp.GetTestTree().ResolveReferences()

	// Query hover for Sig1 (Line 5)
	// Line 4: Sig1... (0-based)
	params := lsp.HoverParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     lsp.Position{Line: 4, Character: 9},
	}

	hover := lsp.HandleHover(params)
	if hover == nil {
		t.Fatal("Expected hover")
	}

	contentHover := hover.Contents.(lsp.MarkupContent).Value
	if !strings.Contains(contentHover, "**Used in GAMs**") {
		t.Errorf("Expected 'Used in GAMs' section, got:\n%s", contentHover)
	}
	if !strings.Contains(contentHover, "- +GAM1") {
		t.Error("Expected +GAM1 in usage list")
	}
	if !strings.Contains(contentHover, "- +GAM2") {
		t.Error("Expected +GAM2 in usage list")
	}
}
