package integration

import (
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/lsp"
	"github.com/marte-community/marte-dev-tools/internal/parser"
)

func TestLSPCrashOnUndefinedReference(t *testing.T) {
	// Setup
	lsp.Tree = index.NewProjectTree()
	lsp.Documents = make(map[string]string)

	content := `
+App = {
    Class = RealTimeApplication
    +State = {
        Class = RealTimeState
        +Thread = {
            Class = RealTimeThread
            Functions = { UndefinedGAM }
        }
    }
}
`
	uri := "file://crash.marte"
	lsp.Documents[uri] = content
	p := parser.NewParser(content)
	cfg, err := p.Parse()
	if err != nil {
		t.Fatal(err)
	}
	lsp.Tree.AddFile("crash.marte", cfg)
	lsp.Tree.ResolveReferences()

	// Line 7: "            Functions = { UndefinedGAM }"
	// 12 spaces + "Functions" (9) + " = { " (5) = 26 chars prefix.
	// UndefinedGAM starts at 26.
	params := lsp.DefinitionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     lsp.Position{Line: 7, Character: 27},
	}

	// This should NOT panic
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Recovered from panic: %v", r)
		}
	}()

	res := lsp.HandleDefinition(params)

	if res != nil {
		t.Error("Expected nil for undefined reference definition")
	}

	// 2. Hover
	hParams := lsp.HoverParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     lsp.Position{Line: 7, Character: 27},
	}
	hover := lsp.HandleHover(hParams)
	if hover == nil {
		t.Error("Expected hover for unresolved reference")
	} else {
		content := hover.Contents.(lsp.MarkupContent).Value
		if !strings.Contains(content, "Unresolved") {
			t.Errorf("Expected 'Unresolved' in hover, got: %s", content)
		}
	}
}
