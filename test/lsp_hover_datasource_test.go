package integration

import (
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/lsp"
	"github.com/marte-community/marte-dev-tools/internal/parser"
)

func TestHoverDataSourceName(t *testing.T) {
	// Setup
	lsp.Tree = index.NewProjectTree()
	lsp.Documents = make(map[string]string)

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
`
	uri := "file://test_ds.marte"
	lsp.Documents[uri] = content
	p := parser.NewParser(content)
	cfg, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	lsp.Tree.AddFile("test_ds.marte", cfg)
	lsp.Tree.ResolveReferences()

	// Test 1: Explicit Signal (Sig1)
	// Position: "Sig1" at line 5 (0-based 4)
	// Line 4: "        Sig1 = { Type = uint32 }"
	// Col: 8
	params1 := lsp.HoverParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     lsp.Position{Line: 4, Character: 9},
	}

	hover1 := lsp.HandleHover(params1)
	if hover1 == nil {
		t.Fatal("Expected hover for Sig1")
	}

	content1 := hover1.Contents.(lsp.MarkupContent).Value
	// Expectation: explicit signal shows owner datasource
	if !strings.Contains(content1, "**DataSource**: `+DS1`") && !strings.Contains(content1, "**DataSource**: `DS1`") {
		t.Errorf("Expected DataSource: +DS1 in hover for Sig1, got: %s", content1)
	}

	// Test 2: Implicit Signal (S1)
	// Position: "S1" at line 11 (0-based 10)
	params2 := lsp.HoverParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     lsp.Position{Line: 10, Character: 9},
	}

	hover2 := lsp.HandleHover(params2)
	if hover2 == nil {
		t.Fatal("Expected hover for S1")
	}

	content2 := hover2.Contents.(lsp.MarkupContent).Value
	// Expectation: implicit signal shows referenced datasource
	if !strings.Contains(content2, "**DataSource**: `DS1`") {
		t.Errorf("Expected DataSource: DS1 in hover for S1, got: %s", content2)
	}
}
