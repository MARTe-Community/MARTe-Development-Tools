package integration

import (
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/lsp"
	"github.com/marte-community/marte-dev-tools/internal/parser"
)

func TestLSPHoverVariable(t *testing.T) {
	lsp.Tree = index.NewProjectTree()
	lsp.Documents = make(map[string]string)

	content := `
#var MyInt: int = 123
+Obj = {
    Field = $MyInt
}
`
	uri := "file://hover_var.marte"
	lsp.Documents[uri] = content
	p := parser.NewParser(content)
	cfg, err := p.Parse()
	if err != nil {
		t.Fatal(err)
	}
	lsp.Tree.AddFile("hover_var.marte", cfg)
	lsp.Tree.ResolveReferences()

	// 1. Hover on Definition (#var MyInt)
	// Line 2 (index 1). # is at 0. Name "MyInt" is at 5.
	paramsDef := lsp.HoverParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     lsp.Position{Line: 1, Character: 5},
	}
	resDef := lsp.HandleHover(paramsDef)
	if resDef == nil {
		t.Fatal("Expected hover for definition")
	}
	contentDef := resDef.Contents.(lsp.MarkupContent).Value
	if !strings.Contains(contentDef, "Type: `int`") {
		t.Errorf("Hover def missing type. Got: %s", contentDef)
	}
	if !strings.Contains(contentDef, "Default: `123`") {
		t.Errorf("Hover def missing default value. Got: %s", contentDef)
	}

	// 2. Hover on Reference ($MyInt)
	// Line 4 (index 3). $MyInt is at col 12.
	paramsRef := lsp.HoverParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     lsp.Position{Line: 3, Character: 12},
	}
	resRef := lsp.HandleHover(paramsRef)
	if resRef == nil {
		t.Fatal("Expected hover for reference")
	}
	contentRef := resRef.Contents.(lsp.MarkupContent).Value
	if !strings.Contains(contentRef, "Type: `int`") {
		t.Errorf("Hover ref missing type. Got: %s", contentRef)
	}
	if !strings.Contains(contentRef, "Default: `123`") {
		t.Errorf("Hover ref missing default value. Got: %s", contentRef)
	}
}
