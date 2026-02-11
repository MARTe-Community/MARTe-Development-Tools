package integration

import (
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/lsp"
)

func TestLSPTypeDefinition(t *testing.T) {
	lsp.ResetTestServer()
	// Documents reset via ResetTestServer
	content := `#package P
$T1 = { Class = C }
+I1 = { Class = T1 }
`
	uri := "file:///type.marte"
	lsp.HandleDidOpen(lsp.DidOpenTextDocumentParams{
		TextDocument: lsp.TextDocumentItem{
			URI: uri,
			Text: content,
		},
	})

	// Type Definition on +I1 (Line 3, Col 2) -> Position{Line: 2, Character: 1}
	params := lsp.TypeDefinitionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     lsp.Position{Line: 2, Character: 1},
	}

	res := lsp.HandleTypeDefinition(params)
	locs, ok := res.([]lsp.Location)
	if !ok || len(locs) == 0 {
		t.Fatalf("Expected location for type definition, got %v", res)
	}

	if locs[0].Range.Start.Line != 1 {
		t.Errorf("Expected jump to line 1 ($T1), got line %d", locs[0].Range.Start.Line)
	}
}

func TestLSPCodeActions(t *testing.T) {
	uri := "file:///action.marte"
	// Simulate diagnostic for missing class
	params := lsp.CodeActionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Range: lsp.Range{
			Start: lsp.Position{Line: 5, Character: 0},
			End:   lsp.Position{Line: 5, Character: 10},
		},
		Context: lsp.CodeActionContext{
			Diagnostics: []lsp.LSPDiagnostic{
				{
					Range: lsp.Range{
						Start: lsp.Position{Line: 5, Character: 0},
						End:   lsp.Position{Line: 5, Character: 10},
					},
					Message: "Node +Obj is an object and must contain a 'Class' field",
				},
			},
		},
	}

	actions := lsp.HandleCodeAction(params)
	found := false
	for _, a := range actions {
		if a.Title == "Add Class = ReferenceContainer" {
			found = true
			if a.Edit == nil || len(a.Edit.Changes[uri]) == 0 {
				t.Fatal("Action missing edit")
			}
			break
		}
	}
	if !found {
		t.Error("Expected 'Add Class' code action not found")
	}
}

func TestLSPCallHierarchy(t *testing.T) {
	lsp.ResetTestServer()
	// Documents reset via ResetTestServer
	content := `#package P
+DS = {
    Class = GAMDataSource
    Signals = { S1 = { Type = uint32 } }
}
+GAM1 = {
    Class = IOGAM
    OutputSignals = { S1 = { DataSource = DS Type = uint32 } }
}
+GAM2 = {
    Class = IOGAM
    InputSignals = { S1 = { DataSource = DS Type = uint32 } }
}
`
	uri := "file:///flow.marte"
	lsp.HandleDidOpen(lsp.DidOpenTextDocumentParams{
		TextDocument: lsp.TextDocumentItem{
			URI: uri,
			Text: content,
		},
	})
	
	// Prepare on +GAM2 (Line 10, Col 2) -> Position{Line: 9, Character: 1}
	prepareParams := lsp.CallHierarchyPrepareParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     lsp.Position{Line: 9, Character: 1},
	}
	
	items := lsp.HandlePrepareCallHierarchy(prepareParams)
	if len(items) == 0 {
		t.Fatal("PrepareCallHierarchy failed")
	}
	
	item := items[0]
	if item.Name != "+GAM2" {
		t.Errorf("Expected +GAM2, got %s", item.Name)
	}
	
	// Incoming calls to GAM2 (should be GAM1 via S1)
	inParams := lsp.CallHierarchyIncomingCallsParams{Item: item}
	inCalls := lsp.HandleIncomingCalls(inParams)
	
	found := false
	for _, c := range inCalls {
		if c.From.Name == "+GAM1" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Incoming call from GAM1 to GAM2 not found. Calls: %v", inCalls)
	}
}