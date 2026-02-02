package integration

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/lsp"
	"github.com/marte-community/marte-dev-tools/internal/parser"
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

func TestLSPDispatch(t *testing.T) {
	var buf bytes.Buffer
	lsp.Output = &buf

	// Initialize
	msgInit := &lsp.JsonRpcMessage{Method: "initialize", ID: 1, Params: json.RawMessage(`{}`)}
	lsp.HandleMessage(msgInit)

	// DidOpen
	msgOpen := &lsp.JsonRpcMessage{Method: "textDocument/didOpen", Params: json.RawMessage(`{"textDocument":{"uri":"file://d.marte","text":""}}`)}
	lsp.HandleMessage(msgOpen)

	// DidChange
	msgChange := &lsp.JsonRpcMessage{Method: "textDocument/didChange", Params: json.RawMessage(`{"textDocument":{"uri":"file://d.marte","version":2},"contentChanges":[{"text":"A"}]}`)}
	lsp.HandleMessage(msgChange)

	// Hover
	msgHover := &lsp.JsonRpcMessage{Method: "textDocument/hover", ID: 2, Params: json.RawMessage(`{"textDocument":{"uri":"file://d.marte"},"position":{"line":0,"character":0}}`)}
	lsp.HandleMessage(msgHover)

	// Definition
	msgDef := &lsp.JsonRpcMessage{Method: "textDocument/definition", ID: 3, Params: json.RawMessage(`{"textDocument":{"uri":"file://d.marte"},"position":{"line":0,"character":0}}`)}
	lsp.HandleMessage(msgDef)

	// References
	msgRef := &lsp.JsonRpcMessage{Method: "textDocument/references", ID: 4, Params: json.RawMessage(`{"textDocument":{"uri":"file://d.marte"},"position":{"line":0,"character":0},"context":{"includeDeclaration":true}}`)}
	lsp.HandleMessage(msgRef)

	// Completion
	msgComp := &lsp.JsonRpcMessage{Method: "textDocument/completion", ID: 5, Params: json.RawMessage(`{"textDocument":{"uri":"file://d.marte"},"position":{"line":0,"character":0}}`)}
	lsp.HandleMessage(msgComp)

	// Formatting
	msgFmt := &lsp.JsonRpcMessage{Method: "textDocument/formatting", ID: 6, Params: json.RawMessage(`{"textDocument":{"uri":"file://d.marte"},"options":{"tabSize":4,"insertSpaces":true}}`)}
	lsp.HandleMessage(msgFmt)

	// Rename
	msgRename := &lsp.JsonRpcMessage{Method: "textDocument/rename", ID: 7, Params: json.RawMessage(`{"textDocument":{"uri":"file://d.marte"},"position":{"line":0,"character":0},"newName":"B"}`)}
	lsp.HandleMessage(msgRename)
}

func TestLSPVariableDefinition(t *testing.T) {
	lsp.Tree = index.NewProjectTree()
	lsp.Documents = make(map[string]string)
	
	content := `
#var MyVar: int = 10
+Obj = {
    Field = @MyVar
}
`
	uri := "file://var_def.marte"
	lsp.Documents[uri] = content
	
	p := parser.NewParser(content)
	cfg, _ := p.Parse()
	lsp.Tree.AddFile("var_def.marte", cfg)
	lsp.Tree.ResolveReferences()
	
	params := lsp.DefinitionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     lsp.Position{Line: 3, Character: 13},
	}
	
	res := lsp.HandleDefinition(params)
	if res == nil {
		t.Fatal("Definition not found for variable")
	}
	
	locs, ok := res.([]lsp.Location)
	if !ok || len(locs) == 0 {
		t.Fatal("Expected location list")
	}
	
	if locs[0].Range.Start.Line != 1 {
		t.Errorf("Expected line 1, got %d", locs[0].Range.Start.Line)
	}
}
