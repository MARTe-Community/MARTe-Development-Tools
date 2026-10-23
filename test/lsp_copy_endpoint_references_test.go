package integration

import (
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/lsp"
)

// Copy-group endpoints (`DS::Signal`) are sugar: they have no tree node
// until lowering, but they are real usages of the signal. Go-to references
// from the signal declaration must include them (and must not wander into
// unrelated files that happen to share the line/column).
func TestCopyEndpointReferences(t *testing.T) {
	lsp.ResetTestServer()

	dataURI := "file:///data.marte"
	funcURI := "file:///functions.marte"
	lsp.HandleDidOpen(lsp.DidOpenTextDocumentParams{TextDocument: lsp.TextDocumentItem{
		URI: dataURI,
		Text: `#package T

+DDB = {
  Class = GAMDataSource
  AllowNoProducers = 1
  Signals = {
    AI0 = { Type = float32 }
  }
}
`,
	}})
	lsp.HandleDidOpen(lsp.DidOpenTextDocumentParams{TextDocument: lsp.TextDocumentItem{
		URI: funcURI,
		Text: `#package T

+App = {
  Class = RealTimeApplication
  +Functions = {
    Class = ReferenceContainer
    copy C {
      DDB::AI0 -> DDB::AI0
    }
  }
}
`,
	}})

	// Cursor on AI0 in the DataSource signal list (line 8, column 5).
	locs := lsp.HandleReferences(lsp.ReferenceParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: dataURI},
		Position:     lsp.Position{Line: 6, Character: 4},
		Context:      lsp.ReferenceContext{IncludeDeclaration: true},
	})

	var inFunc int
	for _, l := range locs {
		if l.URI == funcURI {
			inFunc++
		}
	}
	if inFunc != 2 {
		t.Fatalf("expected both copy endpoints in %s, got %d: %+v", funcURI, inFunc, locs)
	}
	for _, l := range locs {
		if strings.Contains(l.URI, "apsbps") {
			t.Fatalf("unrelated location leaked: %+v", l)
		}
	}
}
