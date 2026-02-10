package integration

import (
	"context"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/lsp"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/validator"
)

func TestRenameSignalInGAM(t *testing.T) {
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
        // Implicit match
        Sig1 = { DataSource = DS }
        // Explicit Alias
        S2 = { DataSource = DS Alias = Sig1 }
    }
}
`
	uri := "file://rename_sig.marte"
	lsp.Documents[uri] = content
	p := parser.NewParser(content)
	cfg, err := p.Parse()
	if err != nil {
		t.Fatal(err)
	}
	lsp.Tree.AddFile("rename_sig.marte", cfg)
	lsp.Tree.ResolveReferences()

	// Run validator to populate Targets
	v := validator.NewValidator(lsp.Tree, ".", nil)
	v.ValidateProject(context.Background())

	// Rename DS.Sig1 to NewSig
	// Sig1 is at Line 5.
	// Line 0: empty
	// Line 1: +DS
	// Line 2: Class
	// Line 3: +Signals
	// Line 4: Sig1
	params := lsp.RenameParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     lsp.Position{Line: 4, Character: 9}, // Sig1
		NewName:      "NewSig",
	}

	edit := lsp.HandleRename(params)
	if edit == nil {
		t.Fatal("Expected edits")
	}

	edits := edit.Changes[uri]

	// Expect:
	// 1. Definition of Sig1 in DS (Line 5) -> NewSig
	// 2. Definition of Sig1 in GAM (Line 10) -> NewSig (Implicit match)
	// 3. Alias reference in S2 (Line 12) -> NewSig

	// Line 10: Sig1 = ... (0-based 9)
	// Line 12: S2 = ... Alias = Sig1 (0-based 11)

	expectedCount := 3
	if len(edits) != expectedCount {
		t.Errorf("Expected %d edits, got %d", expectedCount, len(edits))
		for _, e := range edits {
			t.Logf("Edit: %s at %d", e.NewText, e.Range.Start.Line)
		}
	}

	// Check Implicit Signal Rename
	foundImplicit := false
	for _, e := range edits {
		if e.Range.Start.Line == 11 {
			if e.NewText == "NewSig" {
				foundImplicit = true
			}
		}
	}
	if !foundImplicit {
		t.Error("Did not find implicit signal rename")
	}

	// Check Alias Rename
	foundAlias := false
	for _, e := range edits {
		if e.Range.Start.Line == 13 {
			// Alias = Sig1. Range should cover Sig1.
			if e.NewText == "NewSig" {
				foundAlias = true
			}
		}
	}
	if !foundAlias {
		t.Error("Did not find alias reference rename")
	}
}
