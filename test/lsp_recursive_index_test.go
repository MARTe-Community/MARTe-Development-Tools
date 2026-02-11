package integration

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/lsp"
)

func TestLSPRecursiveIndexing(t *testing.T) {
	// Setup directory structure
	rootDir, err := os.MkdirTemp("", "lsp_recursive")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(rootDir)

	// root/main.marte
	mainContent := `
#package App
+Main = {
    Ref = SubComp
}
`
	if err := os.WriteFile(filepath.Join(rootDir, "main.marte"), []byte(mainContent), 0644); err != nil {
		t.Fatal(err)
	}

	// root/subdir/sub.marte
	subDir := filepath.Join(rootDir, "subdir")
	if err := os.Mkdir(subDir, 0755); err != nil {
		t.Fatal(err)
	}
	subContent := `
#package App
+SubComp = { Class = Component }
`
	if err := os.WriteFile(filepath.Join(subDir, "sub.marte"), []byte(subContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Initialize LSP
	lsp.ResetTestServer()
	// Documents reset via ResetTestServer

	// Simulate ScanDirectory
	if err := lsp.GetTestTree().ScanDirectory(rootDir); err != nil {
		t.Fatalf("ScanDirectory failed: %v", err)
	}
	lsp.GetTestTree().ResolveReferences()

	// Check if SubComp is in the tree
	// Root -> App -> SubComp
	appNode := lsp.GetTestTree().Root.Children["App"]
	if appNode == nil {
		t.Fatal("App package not found")
	}

	subComp := appNode.Children["SubComp"]
	if subComp == nil {
		t.Fatal("SubComp not found in tree (recursive scan failed)")
	}

	mainURI := "file://" + filepath.Join(rootDir, "main.marte")

	// Definition Request
	params := lsp.DefinitionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: mainURI},
		Position:     lsp.Position{Line: 3, Character: 12},
	}

	res := lsp.HandleDefinition(params)
	if res == nil {
		t.Fatal("Definition not found for SubComp")
	}

	locs, ok := res.([]lsp.Location)
	if !ok || len(locs) == 0 {
		t.Fatal("Expected location list")
	}

	expectedFile := filepath.Join(subDir, "sub.marte")
	if locs[0].URI != "file://"+expectedFile {
		t.Errorf("Expected definition in %s, got %s", expectedFile, locs[0].URI)
	}
}
