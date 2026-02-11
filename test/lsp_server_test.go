package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/lsp"
	"github.com/marte-community/marte-dev-tools/internal/parser"
)

func TestInitProjectScan(t *testing.T) {
	// 1. Setup temp dir with files
	tmpDir, err := os.MkdirTemp("", "lsp_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// File 1: Definition
	if err := os.WriteFile(filepath.Join(tmpDir, "def.marte"), []byte("#package Test.Common\n+Target = { Class = C }"), 0644); err != nil {
		t.Fatal(err)
	}
	// File 2: Reference
	if err := os.WriteFile(filepath.Join(tmpDir, "ref.marte"), []byte("#package Test.Common\n+Source = { Class = C Link = Target }"), 0644); err != nil {
		t.Fatal(err)
	}

	// 2. Initialize
	lsp.ResetTestServer() // Reset global tree

	initParams := lsp.InitializeParams{RootPath: tmpDir}
	paramsBytes, _ := json.Marshal(initParams)

	msg := &lsp.JsonRpcMessage{
		Method: "initialize",
		Params: paramsBytes,
		ID:     1,
	}

	lsp.HandleMessage(msg)

	// Query the reference in ref.marte at "Target"
	defParams := lsp.DefinitionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: "file://" + filepath.Join(tmpDir, "ref.marte")},
		Position:     lsp.Position{Line: 1, Character: 29},
	}

	res := lsp.HandleDefinition(defParams)
	if res == nil {
		t.Fatal("Definition not found via LSP after initialization")
	}

	locs, ok := res.([]lsp.Location)
	if !ok {
		t.Fatalf("Expected []lsp.Location, got %T", res)
	}

	if len(locs) == 0 {
		t.Fatal("No locations found")
	}

	// Verify uri points to def.marte
	expectedURI := "file://" + filepath.Join(tmpDir, "def.marte")
	if locs[0].URI != expectedURI {
		t.Errorf("Expected URI %s, got %s", expectedURI, locs[0].URI)
	}
}

func TestHandleDefinition(t *testing.T) {
	// Reset tree for test
	lsp.ResetTestServer()

	content := `
+MyObject = {
    Class = Type
}
+RefObject = {
    Class = Type
    RefField = MyObject
}
`
	path := "/test.marte"
	p := parser.NewParser(content)
	config, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	lsp.GetTestTree().AddFile(path, config)
	lsp.GetTestTree().ResolveReferences()

	t.Logf("Refs: %d", len(lsp.GetTestTree().References))
	for _, r := range lsp.GetTestTree().References {
		t.Logf("  %s at %d:%d", r.Name, r.Position.Line, r.Position.Column)
	}

	// Test Go to Definition on MyObject reference
	params := lsp.DefinitionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: "file://" + path},
		Position:     lsp.Position{Line: 6, Character: 15}, // "MyObject" in RefField = MyObject
	}

	result := lsp.HandleDefinition(params)
	if result == nil {
		t.Fatal("HandleDefinition returned nil")
	}

	locations, ok := result.([]lsp.Location)
	if !ok {
		t.Fatalf("Expected []lsp.Location, got %T", result)
	}

	if len(locations) != 1 {
		t.Fatalf("Expected 1 location, got %d", len(locations))
	}

	if locations[0].Range.Start.Line != 1 { // +MyObject is on line 2 (0-indexed 1)
		t.Errorf("Expected definition on line 1, got %d", locations[0].Range.Start.Line)
	}
}

func TestHandleReferences(t *testing.T) {
	// Reset tree for test
	lsp.ResetTestServer()

	content := `
+MyObject = {
    Class = Type
}
+RefObject = {
    Class = Type
    RefField = MyObject
}
+AnotherRef = {
    Ref = MyObject
}
`
	path := "/test.marte"
	p := parser.NewParser(content)
	config, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	lsp.GetTestTree().AddFile(path, config)
	lsp.GetTestTree().ResolveReferences()

	// Test Find References for MyObject (triggered from its definition)
	params := lsp.ReferenceParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: "file://" + path},
		Position:     lsp.Position{Line: 1, Character: 1}, // "+MyObject"
		Context:      lsp.ReferenceContext{IncludeDeclaration: true},
	}

	locations := lsp.HandleReferences(params)
	if len(locations) != 3 { // 1 declaration + 2 references
		t.Fatalf("Expected 3 locations, got %d", len(locations))
	}
}

func TestLSPFormatting(t *testing.T) {
	// Setup
	content := `
#package Proj.Main
   +Object={
Field=1
  }
`
	uri := "file:///test.marte"

	// Open (populate Documents map)
	lsp.GetTestDocuments()[uri] = content

	// Format
	params := lsp.DocumentFormattingParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
	}

	edits := lsp.HandleFormatting(params)

	if len(edits) != 1 {
		t.Fatalf("Expected 1 edit, got %d", len(edits))
	}

	newText := edits[0].NewText

	expected := `#package Proj.Main

+Object = {
  Field = 1
}
`
	// Normalize newlines for comparison just in case
	if strings.TrimSpace(strings.ReplaceAll(newText, "\r\n", "\n")) != strings.TrimSpace(strings.ReplaceAll(expected, "\r\n", "\n")) {
		t.Errorf("Formatting mismatch.\nExpected:\n%s\nGot:\n%s", expected, newText)
	}
}
