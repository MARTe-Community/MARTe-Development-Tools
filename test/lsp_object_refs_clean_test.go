package integration

import (
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/lsp"
	"github.com/marte-community/marte-dev-tools/internal/parser"
)

func TestLSPObjectReferencesInArray(t *testing.T) {
	lsp.ResetTestServer()

	content := `
+MyGAM = {
    Class = IOGAM
    InputSignals = {
        A = {
            Type = uint32
        }
    }
}

+App = {
    Class = RealTimeApplication
    +States = {
        Class = ReferenceContainer
        +State = {
            Class = RealTimeState
            Threads = {
                +Thread = {
                    Class = RealTimeThread
                    Functions = { MyGAM }
                }
            }
        }
    }
}
`
	uri := "file://test.marte"
	lsp.GetTestDocuments()[uri] = content
	p := parser.NewParser(content)
	cfg, err := p.Parse()
	if err != nil {
		t.Fatal(err)
	}
	tree := lsp.GetTestTree()
	tree.AddFile("test.marte", cfg)
	tree.ResolveReferences(nil)

	// Test 1: Find definition of MyGAM when clicking on the reference in Functions array
	// Line 20: "                    Functions = { MyGAM }"
	// MyGAM is at column 35 (1-indexed), so Character = 34 (0-indexed)
	paramsDef := lsp.DefinitionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     lsp.Position{Line: 19, Character: 34},
	}
	resDef := lsp.HandleDefinition(paramsDef)
	locs, ok := resDef.([]lsp.Location)
	if !ok {
		t.Fatalf("Expected []Location, got %T", resDef)
	}
	if len(locs) == 0 {
		t.Error("Expected at least 1 definition location, got 0")
	}

	// Test 2: Find all references when clicking on the MyGAM definition
	// Line 2: "+MyGAM = {"
	paramsRef := lsp.ReferenceParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     lsp.Position{Line: 1, Character: 1},
		Context:      lsp.ReferenceContext{IncludeDeclaration: true},
	}
	resRef := lsp.HandleReferences(paramsRef)
	if len(resRef) < 2 {
		t.Errorf("Expected at least 2 references (definition + usage), got %d", len(resRef))
	}
}
