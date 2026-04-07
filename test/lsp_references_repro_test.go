package integration

import (
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/lsp"
	"github.com/marte-community/marte-dev-tools/internal/schema"
)

func TestLSPReferencesRepro(t *testing.T) {
	lsp.ResetTestServer()
	lsp.GlobalSchema = schema.LoadFullSchema(".")

	uri := "file://repro.marte"
	content := `
#package Repro
+MyDS = {
    Class = "GAMDataSource"
    Signals = {
        Sig1 = { Type = uint32 }
    }
}

+MyGAM = {
    Class = "IOGAM"
    InputSignals = {
        Sig1 = {
            DataSource = MyDS
        }
    }
}

+MyGAM2 = {
    Class = "IOGAM"
    InputSignals = {
        SigAlias = {
            Alias = Sig1
            DataSource = "MyDS"
        }
    }
}
`
	lsp.HandleDidOpen(lsp.DidOpenTextDocumentParams{
		TextDocument: lsp.TextDocumentItem{
			URI:  uri,
			Text: content,
		},
	})

	tree := lsp.GetTestTree()
	tree.ResolveReferences(nil)

	// Test 1: References for MyDS (DataSource) from its definition
	// MyDS definition is at line 3, col 2 (+MyDS)
	params := lsp.ReferenceParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     lsp.Position{Line: 2, Character: 2}, // on 'MyDS'
		Context:      lsp.ReferenceContext{IncludeDeclaration: true},
	}
	refs := lsp.HandleReferences(params)
	t.Logf("Found %d refs for MyDS", len(refs))
	for _, r := range refs {
		t.Logf("  Ref: %d:%d", r.Range.Start.Line, r.Range.Start.Character)
	}
	
	foundUsage1 := false // in MyGAM
	foundUsage2 := false // in MyGAM2 (string literal)
	
	for _, r := range refs {
		// usage in MyGAM is at line 13 (0-indexed), col 25
		if r.Range.Start.Line == 13 {
			foundUsage1 = true
		}
		// usage in MyGAM2 is at line 23 (0-indexed), col 25
		if r.Range.Start.Line == 23 {
			foundUsage2 = true
		}
	}

	if !foundUsage1 {
		t.Error("Expected to find reference to MyDS in MyGAM (ReferenceValue)")
	}
	if !foundUsage2 {
		t.Error("Expected to find reference to MyDS in MyGAM2 (StringValue)")
	}

	// Test 2: References for Sig1 (Signal) from its definition
	// Sig1 definition is at line 6, col 9
	paramsSig := lsp.ReferenceParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     lsp.Position{Line: 5, Character: 9},
		Context:      lsp.ReferenceContext{IncludeDeclaration: true},
	}
	refsSig := lsp.HandleReferences(paramsSig)
	t.Logf("Found %d refs for Sig1", len(refsSig))
	for _, r := range refsSig {
		t.Logf("  Ref: %d:%d", r.Range.Start.Line, r.Range.Start.Character)
	}
	
	foundSigUsage1 := false // in MyGAM
	foundSigUsage2 := false // in MyGAM2 (Alias)
	
	for _, r := range refsSig {
		if r.Range.Start.Line == 12 { // Sig1 = { ... } in MyGAM
			foundSigUsage1 = true
		}
		if r.Range.Start.Line == 21 { // Alias = Sig1 in MyGAM2
			foundSigUsage2 = true
		}
	}

	if !foundSigUsage1 {
		t.Error("Expected to find reference to Sig1 in MyGAM (Implicit name match)")
	}
	if !foundSigUsage2 {
		t.Error("Expected to find reference to Sig1 in MyGAM2 (Alias)")
	}

	// Test 3: References for Sig1 from its usage in MyGAM
	// Sig1 usage in MyGAM is at line 12 (0-indexed), col 9
	paramsSigUsage := lsp.ReferenceParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     lsp.Position{Line: 12, Character: 9},
		Context:      lsp.ReferenceContext{IncludeDeclaration: true},
	}
	refsSigUsage := lsp.HandleReferences(paramsSigUsage)
	t.Logf("Found %d refs for Sig1 starting from usage", len(refsSigUsage))
	for _, r := range refsSigUsage {
		t.Logf("  Ref: %d:%d", r.Range.Start.Line, r.Range.Start.Character)
	}

	if len(refsSigUsage) < 2 {
		t.Errorf("Expected at least 2 references starting from usage, got %d", len(refsSigUsage))
	}
}
