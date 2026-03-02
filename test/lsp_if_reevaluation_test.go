package integration

import (
	"bytes"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/lsp"
	"github.com/marte-community/marte-dev-tools/internal/schema"
)

func TestLSPIfReevaluation(t *testing.T) {
	// Setup LSP environment
	lsp.ResetTestServer()
	lsp.GlobalSchema = schema.LoadFullSchema(".")

	// Capture output
	var buf bytes.Buffer
	lsp.Output = &buf

	uri := "file://test_if.marte"
	content := `
#package Test
#var Active: bool = false

#if @Active
	+ErrorNode = {
		// Missing Class field
		Type = uint32
	}
#else
	+ValidNode = {
		Class = ReferenceContainer
	}
#end
`
	// 1. Open document with Active=false
	lsp.HandleDidOpen(lsp.DidOpenTextDocumentParams{
		TextDocument: lsp.TextDocumentItem{
			URI:  uri,
			Text: content,
		},
	})

	output := buf.String()
	if strings.Contains(output, "must contain a 'Class' field") {
		t.Error("Did not expect 'Missing Class' error when Active=false")
	}
	buf.Reset()

	// 2. Change Active to true
	newContent := strings.Replace(content, "#var Active: bool = false", "#var Active: bool = true", 1)
	lsp.HandleDidChange(lsp.DidChangeTextDocumentParams{
		TextDocument: lsp.VersionedTextDocumentIdentifier{
			URI: uri,
		},
		ContentChanges: []lsp.TextDocumentContentChangeEvent{
			{Text: newContent},
		},
	})

	output = buf.String()
	if !strings.Contains(output, "must contain a 'Class' field") {
		t.Logf("Output: %s", output)
		t.Error("Expected 'Missing Class' error when Active=true")
	}
	buf.Reset()

	// 3. Change Active back to false
	lsp.HandleDidChange(lsp.DidChangeTextDocumentParams{
		TextDocument: lsp.VersionedTextDocumentIdentifier{
			URI: uri,
		},
		ContentChanges: []lsp.TextDocumentContentChangeEvent{
			{Text: content},
		},
	})

	output = buf.String()
	if strings.Contains(output, "must contain a 'Class' field") {
		t.Error("Expected error to disappear when Active=false")
	}
}
