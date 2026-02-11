package integration

import (
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/lsp"
	"github.com/marte-community/marte-dev-tools/internal/parser"
)

func TestHandleRename(t *testing.T) {
	// Setup
	lsp.ResetTestServer()
	// Documents reset via ResetTestServer

	content := `
#package Some
+MyNode = {
    Class = Type
}
+Consumer = {
    Link = MyNode
    PkgLink = Some.MyNode
}
`
	uri := "file://rename.marte"
	lsp.GetTestDocuments()[uri] = content
	p := parser.NewParser(content)
	cfg, err := p.Parse()
	if err != nil {
		t.Fatal(err)
	}
	lsp.GetTestTree().AddFile("rename.marte", cfg)
	lsp.GetTestTree().ResolveReferences()

	// Rename +MyNode to NewNode
	// +MyNode is at Line 2 (after #package)
	// Line 0: empty
	// Line 1: #package
	// Line 2: +MyNode
	params := lsp.RenameParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     lsp.Position{Line: 2, Character: 4}, // +MyNode
		NewName:      "NewNode",
	}

	edit := lsp.HandleRename(params)
	if edit == nil {
		t.Fatal("Expected edits")
	}

	edits := edit.Changes[uri]
	if len(edits) != 3 {
		t.Errorf("Expected 3 edits (Def, Link, PkgLink), got %d", len(edits))
	}

	// Verify Definition change (+MyNode -> +NewNode)
	foundDef := false
	for _, e := range edits {
		if e.NewText == "+NewNode" {
			foundDef = true
			if e.Range.Start.Line != 2 {
				t.Errorf("Definition edit line wrong: %d", e.Range.Start.Line)
			}
		}
	}
	if !foundDef {
		t.Error("Did not find definition edit +NewNode")
	}

	// Verify Link change (MyNode -> NewNode)
	foundLink := false
	for _, e := range edits {
		if e.NewText == "NewNode" && e.Range.Start.Line == 6 { // Link = MyNode
			foundLink = true
		}
	}
	if !foundLink {
		t.Error("Did not find Link edit")
	}

	// Verify PkgLink change (Some.MyNode -> Some.NewNode)
	foundPkg := false
	for _, e := range edits {
		if e.NewText == "NewNode" && e.Range.Start.Line == 7 { // PkgLink = Some.MyNode
			foundPkg = true
		}
	}
	if !foundPkg {
		t.Error("Did not find PkgLink edit")
	}
}
