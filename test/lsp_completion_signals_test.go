package integration

import (
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/lsp"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/schema"
)

func TestSuggestSignalsInGAM(t *testing.T) {
	// Setup
	lsp.ResetTestServer()
	// Documents reset via ResetTestServer
	lsp.SetTestProjectRoot(".")
	lsp.GlobalSchema = schema.NewSchema()

	// Inject schema for directionality
	custom := []byte(`
package schema
#Classes: {
    FileReader: { direction: "IN" }
    FileWriter: { direction: "OUT" }
}
`)
	val := lsp.GlobalSchema.Context.CompileBytes(custom)
	lsp.GlobalSchema.Value = lsp.GlobalSchema.Value.Unify(val)

	content := `
+InDS = {
    Class = FileReader
    +Signals = {
        InSig = { Type = uint32 }
    }
}
+OutDS = {
    Class = FileWriter
    +Signals = {
        OutSig = { Type = uint32 }
    }
}
+GAM = {
    Class = IOGAM
    +InputSignals = {
        
    }
    +OutputSignals = {
        
    }
}
`
	uri := "file://signals.marte"
	lsp.GetTestDocuments()[uri] = content
	p := parser.NewParser(content)
	cfg, err := p.Parse()
	if err != nil {
		t.Fatal(err)
	}
	lsp.GetTestTree().AddFile("signals.marte", cfg)

	// 1. Suggest in InputSignals
	// Line 16 (empty line inside InputSignals)
	paramsIn := lsp.CompletionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     lsp.Position{Line: 16, Character: 8},
	}

	listIn := lsp.HandleCompletion(paramsIn)
	if listIn == nil {
		t.Fatal("Expected suggestions in InputSignals")
	}

	foundIn := false
	foundOut := false
	for _, item := range listIn.Items {
		if item.Label == "InDS:InSig" {
			foundIn = true
			// Normalize spaces for check
			insert := strings.ReplaceAll(item.InsertText, " ", "")
			expected := "InSig={DataSource=InDS}"
			if !strings.Contains(insert, expected) && !strings.Contains(item.InsertText, "InSig = {") {
				// Snippet might differ slightly, but should contain essentials
				t.Errorf("InsertText mismatch: %s", item.InsertText)
			}
		}
		if item.Label == "OutDS:OutSig" {
			foundOut = true
		}
	}

	if !foundIn {
		t.Error("Did not find InDS:InSig")
	}
	if foundOut {
		t.Error("Should not find OutDS:OutSig in InputSignals")
	}

	// 2. Suggest in OutputSignals
	// Line 19
	paramsOut := lsp.CompletionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     lsp.Position{Line: 19, Character: 8},
	}
	listOut := lsp.HandleCompletion(paramsOut)
	if listOut == nil {
		t.Fatal("Expected suggestions in OutputSignals")
	}

	foundIn = false
	foundOut = false
	for _, item := range listOut.Items {
		if item.Label == "InDS:InSig" {
			foundIn = true
		}
		if item.Label == "OutDS:OutSig" {
			foundOut = true
		}
	}

	if foundIn {
		t.Error("Should not find InDS:InSig in OutputSignals")
	}
	if !foundOut {
		t.Error("Did not find OutDS:OutSig in OutputSignals")
	}
}
