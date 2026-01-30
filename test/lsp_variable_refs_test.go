package integration

import (
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/lsp"
	"github.com/marte-community/marte-dev-tools/internal/parser"
)

func TestLSPVariableRefs(t *testing.T) {
	lsp.Tree = index.NewProjectTree()
	lsp.Documents = make(map[string]string)

	content := `
#var MyVar: int = 1
+Obj = {
    Field = @MyVar
}
`
	uri := "file://vars.marte"
	lsp.Documents[uri] = content
	p := parser.NewParser(content)
	cfg, err := p.Parse()
	if err != nil {
		t.Fatal(err)
	}
	lsp.Tree.AddFile("vars.marte", cfg)
	lsp.Tree.ResolveReferences()

	// 1. Definition from Usage
	// Line 4: "    Field = @MyVar"
	// @ is at col 12 (0-based) ?
	// "    Field = " is 4 + 6 + 3 = 13 chars?
	// 4 spaces. Field (5). " = " (3). 4+5+3 = 12.
	// So @ is at 12.
	paramsDef := lsp.DefinitionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     lsp.Position{Line: 3, Character: 12},
	}
	resDef := lsp.HandleDefinition(paramsDef)
	locs, ok := resDef.([]lsp.Location)
	if !ok || len(locs) != 1 {
		t.Fatalf("Expected 1 definition location, got %v", resDef)
	}
	// Line 2 in file is index 1.
	if locs[0].Range.Start.Line != 1 {
		t.Errorf("Expected definition at line 1, got %d", locs[0].Range.Start.Line)
	}

	// 2. References from Definition
	// #var at line 2 (index 1). Col 0.
	paramsRef := lsp.ReferenceParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     lsp.Position{Line: 1, Character: 1},
		Context:      lsp.ReferenceContext{IncludeDeclaration: true},
	}
	resRef := lsp.HandleReferences(paramsRef)
	if len(resRef) != 2 { // Decl + Usage
		t.Errorf("Expected 2 references, got %d", len(resRef))
	}
}
