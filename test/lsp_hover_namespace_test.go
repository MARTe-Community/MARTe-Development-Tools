package integration

import (
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/lsp"
	"github.com/marte-community/marte-dev-tools/internal/parser"
)

func TestHoverNamespaceStripping(t *testing.T) {
	content := `
+Data = {
	Class = "ReferenceContainer"
	+DS1 = {
		Class = "SDN::SDNSubscriber"
	}
}
`
	// Reset global state
	lsp.ResetTestServer()
	// Documents reset via ResetTestServer

	p := parser.NewParser(content)
	config, err := p.Parse()
	if err != nil {
		t.Fatal(err)
	}
	lsp.GetTestTree().AddFile("hover.marte", config)
	
    // Resolve references to populate NodeMap/Metadata? 
    // AddFile does populate NodeMap.
    
    // Hover on DS1 definition
    // Line 4, Col 5 (approx)
    
	params := lsp.HoverParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: "file://hover.marte"},
		Position:     lsp.Position{Line: 3, Character: 4}, // +DS1
	}

	lsp.GetTestDocuments()["file://hover.marte"] = content

	res := lsp.HandleHover(params)
    
    if res == nil {
        t.Fatal("Hover returned nil")
    }
    
    markup, ok := res.Contents.(lsp.MarkupContent)
    if !ok {
        t.Fatal("Hover contents not MarkupContent")
    }
    
    // Expect: `SDNSubscriber:DS1`
    // NOT: `SDN::SDNSubscriber:DS1`
    
    if strings.Contains(markup.Value, "SDN::SDNSubscriber") {
        t.Errorf("Hover contains namespace: %s", markup.Value)
    }
    if !strings.Contains(markup.Value, "SDNSubscriber:DS1") {
        t.Errorf("Hover missing bare class: %s", markup.Value)
    }
}
