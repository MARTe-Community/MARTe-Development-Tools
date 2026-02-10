package integration

import (
	"context"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/lsp"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/validator"
)

func TestRenameImplicitToDefinition(t *testing.T) {
	// Setup
	lsp.Tree = index.NewProjectTree()
	lsp.Documents = make(map[string]string)

	content := `
+DS = {
    Class = FileReader
    +Signals = {
        Sig1 = { Type = uint32 }
    }
}
+GAM = {
    Class = IOGAM
    +InputSignals = {
        // Implicit usage
        Sig1 = { DataSource = DS }
    }
}
`
	uri := "file://rename_imp.marte"
	lsp.Documents[uri] = content
	p := parser.NewParser(content)
	cfg, err := p.Parse()
	if err != nil {
		t.Fatal(err)
	}
	lsp.Tree.AddFile("rename_imp.marte", cfg)
	lsp.Tree.ResolveReferences()

	// Run validator to link targets
	v := validator.NewValidator(lsp.Tree, ".", nil)
	v.ValidateProject(context.Background())

	// Rename Implicit Sig1 (Line 11, 0-based 11)
	// Line 11: "        Sig1 = { DataSource = DS }"
	params := lsp.RenameParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     lsp.Position{Line: 11, Character: 9},
		NewName:      "NewSig",
	}

	edit := lsp.HandleRename(params)
	if edit == nil {
		t.Fatal("Expected edits")
	}

	edits := edit.Changes[uri]

	// Expect:
	// 1. Rename Implicit Sig1 (Line 9) -> NewSig
	// 2. Rename Definition Sig1 (Line 4) -> NewSig

	if len(edits) != 2 {
		t.Errorf("Expected 2 edits, got %d", len(edits))
		for _, e := range edits {
			t.Logf("Edit at line %d", e.Range.Start.Line)
		}
	}

	foundDef := false
	foundImp := false
	for _, e := range edits {
		if e.Range.Start.Line == 4 {
			foundDef = true
		}
		if e.Range.Start.Line == 11 {
			foundImp = true
		}
	}

	if !foundDef {
		t.Error("Definition not renamed")
	}
	if !foundImp {
		t.Error("Implicit usage not renamed")
	}
}
