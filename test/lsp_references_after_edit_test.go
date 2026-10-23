package integration

import (
	"fmt"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/lsp"
)

// Cross-file references must follow the edited text: after an edit the
// reference index is rebuilt for that file, so old positions disappear and
// new ones appear (a stale flat reference slice used to keep both).
func TestReferencesFollowEdits(t *testing.T) {
	lsp.ResetTestServer()

	aURI := "file:///a.marte"
	bURI := "file:///b.marte"
	lsp.HandleDidOpen(lsp.DidOpenTextDocumentParams{TextDocument: lsp.TextDocumentItem{
		URI:  aURI,
		Text: "#package Test.Common\n+Target = {\n  Class = C\n}\n",
	}})
	lsp.HandleDidOpen(lsp.DidOpenTextDocumentParams{TextDocument: lsp.TextDocumentItem{
		URI:  bURI,
		Text: "#package Test.Common\n+Source = {\n  Class = C\n  Link = Target\n}\n",
	}})

	locations := func() []lsp.Location {
		return lsp.HandleReferences(lsp.ReferenceParams{
			TextDocument: lsp.TextDocumentIdentifier{URI: aURI},
			Position:     lsp.Position{Line: 1, Character: 2}, // +Target
			Context:      lsp.ReferenceContext{IncludeDeclaration: true},
		})
	}

	before := locations()
	var beforeUsage *lsp.Location
	for i := range before {
		if before[i].URI == bURI {
			beforeUsage = &before[i]
		}
	}
	if beforeUsage == nil {
		t.Fatalf("no cross-file usage before edit: %+v", before)
	}
	if beforeUsage.Range.Start.Line != 3 {
		t.Fatalf("usage at line %d, want 3", beforeUsage.Range.Start.Line+1)
	}

	// Push the usage one line down.
	lsp.HandleDidChange(lsp.DidChangeTextDocumentParams{
		TextDocument: lsp.VersionedTextDocumentIdentifier{URI: bURI, Version: 2},
		ContentChanges: []lsp.TextDocumentContentChangeEvent{{
			Text: "#package Test.Common\n+Source = {\n  Class = C\n\n  Link = Target\n}\n",
		}},
	})

	after := locations()
	var usages []lsp.Location
	for _, l := range after {
		if l.URI == bURI {
			usages = append(usages, l)
		}
	}
	if len(usages) != 1 {
		t.Fatalf("expected exactly one usage after edit, got %d: %s", len(usages), fmt.Sprint(usages))
	}
	if got := usages[0].Range.Start.Line; got != 4 {
		t.Errorf("usage at line %d, want 4 (stale position not removed)", got+1)
	}
}
