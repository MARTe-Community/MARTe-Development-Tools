package integration

import (
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/lsp"
	"github.com/marte-community/marte-dev-tools/internal/parser"
)

func TestInlayHintNamespaceStripping(t *testing.T) {
	content := `
+Data = {
	Class = "ReferenceContainer"
	+DS1 = {
		Class = "SDN::SDNSubscriber"
	}
}
$GAM = {
    Class = "IOGAM"
    InputSignals = {
        Sig1 = {
            DataSource = "DS1"
            Type = "uint32"
        }
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
	lsp.GetTestTree().AddFile("test.marte", config)
	lsp.GetTestTree().ResolveReferences()

	params := lsp.InlayHintParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: "file://test.marte"},
		Range:        lsp.Range{Start: lsp.Position{0, 0}, End: lsp.Position{20, 0}},
	}

	lsp.GetTestDocuments()["file://test.marte"] = content

	hints := lsp.HandleInlayHint(params)

	found := false
	for _, h := range hints {
		if strings.Contains(h.Label, "SDNSubscriber::") {
			found = true
			if strings.Contains(h.Label, "SDN::") {
				t.Errorf("Inlay hint contains namespace: %s", h.Label)
			}
		}
	}

	if !found {
		t.Error("Expected 'SDNSubscriber::' hint, but not found")
		for _, h := range hints {
			t.Logf("Hint: %s at %v", h.Label, h.Position)
		}
	}
}