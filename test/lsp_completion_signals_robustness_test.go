package integration

import (
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/lsp"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/schema"
)

func TestSuggestSignalsRobustness(t *testing.T) {
	// Setup
	lsp.Tree = index.NewProjectTree()
	lsp.Documents = make(map[string]string)
	lsp.ProjectRoot = "."
	lsp.GlobalSchema = schema.NewSchema()

	// Inject schema with INOUT
	custom := []byte(`
package schema
#Classes: {
    InOutReader: { #direction: "INOUT" }
}
`)
	val := lsp.GlobalSchema.Context.CompileBytes(custom)
	lsp.GlobalSchema.Value = lsp.GlobalSchema.Value.Unify(val)

	content := `
+DS = {
    Class = InOutReader
    +Signals = {
        Sig = { Type = uint32 }
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
	uri := "file://robust.marte"
	lsp.Documents[uri] = content
	p := parser.NewParser(content)
	cfg, err := p.Parse()
	if err != nil {
		t.Fatal(err)
	}
	lsp.Tree.AddFile("robust.marte", cfg)

	// Check Input (Line 10)
	paramsIn := lsp.CompletionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     lsp.Position{Line: 10, Character: 8},
	}
	listIn := lsp.HandleCompletion(paramsIn)
	found := false
	if listIn != nil {
		for _, item := range listIn.Items {
			if item.Label == "DS:Sig" {
				found = true
			}
		}
	}
	if !found {
		t.Error("INOUT signal not found in InputSignals")
	}

	// Check Output (Line 13)
	paramsOut := lsp.CompletionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     lsp.Position{Line: 13, Character: 8},
	}
	listOut := lsp.HandleCompletion(paramsOut)
	found = false
	if listOut != nil {
		for _, item := range listOut.Items {
			if item.Label == "DS:Sig" {
				found = true
			}
		}
	}
	if !found {
		t.Error("INOUT signal not found in OutputSignals")
	}
}
