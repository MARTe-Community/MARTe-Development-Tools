package integration

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/lsp"
)

func TestLSPIncrementalSync(t *testing.T) {
	lsp.Documents = make(map[string]string)
	var buf bytes.Buffer
	lsp.Output = &buf
	
	content := "Line1\nLine2\nLine3"
	uri := "file://inc.marte"
	lsp.Documents[uri] = content 

	// Replace "Line2" (Line 1, 0-5) with "Modified"
	change := lsp.TextDocumentContentChangeEvent{
		Range: &lsp.Range{
			Start: lsp.Position{Line: 1, Character: 0},
			End:   lsp.Position{Line: 1, Character: 5},
		},
		Text: "Modified",
	}
	
	params := lsp.DidChangeTextDocumentParams{
		TextDocument: lsp.VersionedTextDocumentIdentifier{URI: uri, Version: 2},
		ContentChanges: []lsp.TextDocumentContentChangeEvent{change},
	}
	
lsp.HandleDidChange(params)
	
	expected := "Line1\nModified\nLine3"
	if lsp.Documents[uri] != expected {
		t.Errorf("Incremental update failed. Got:\n%q\nWant:\n%q", lsp.Documents[uri], expected)
	}
	
	// Insert at end
	change2 := lsp.TextDocumentContentChangeEvent{
		Range: &lsp.Range{
			Start: lsp.Position{Line: 2, Character: 5},
			End:   lsp.Position{Line: 2, Character: 5},
		},
		Text: "\nLine4",
	}
	params2 := lsp.DidChangeTextDocumentParams{
		TextDocument: lsp.VersionedTextDocumentIdentifier{URI: uri, Version: 3},
		ContentChanges: []lsp.TextDocumentContentChangeEvent{change2},
	}
	lsp.HandleDidChange(params2)
	
	expected2 := "Line1\nModified\nLine3\nLine4"
	if lsp.Documents[uri] != expected2 {
		t.Errorf("Incremental insert failed. Got:\n%q\nWant:\n%q", lsp.Documents[uri], expected2)
	}
}

func TestLSPLifecycle(t *testing.T) {
	var buf bytes.Buffer
	lsp.Output = &buf
	
	// Shutdown
	msgShutdown := &lsp.JsonRpcMessage{
		Method: "shutdown",
		ID: 1,
	}
	lsp.HandleMessage(msgShutdown)
	
	if !strings.Contains(buf.String(), `"result":null`) {
		t.Error("Shutdown response incorrect")
	}
	
	// Exit
	if os.Getenv("TEST_LSP_EXIT") == "1" {
		msgExit := &lsp.JsonRpcMessage{Method: "exit"}
		lsp.HandleMessage(msgExit)
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestLSPLifecycle")
	cmd.Env = append(os.Environ(), "TEST_LSP_EXIT=1")
	err := cmd.Run()
	if err != nil {
		t.Errorf("Exit failed: %v", err)
	}
}

func TestLSPMalformedParams(t *testing.T) {
	var buf bytes.Buffer
	lsp.Output = &buf

	// Malformed Hover
	msg := &lsp.JsonRpcMessage{
		Method: "textDocument/hover",
		ID: 2,
		Params: json.RawMessage(`{invalid`),
	}
	lsp.HandleMessage(msg)
	
	output := buf.String()
	// Should respond with nil result
	if !strings.Contains(output, `"result":null`) {
		t.Errorf("Expected nil result for malformed params, got: %s", output)
	}
}
