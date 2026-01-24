package integration

import (
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
)

func TestIndexCleanup(t *testing.T) {
	idx := index.NewProjectTree()
	file := "cleanup.marte"
	content := `
#package Pkg
+Node = { Class = Type }
`
	p := parser.NewParser(content)
	cfg, err := p.Parse()
	if err != nil {
		t.Fatal(err)
	}
	idx.AddFile(file, cfg)

	// Check node exists
	// Root -> Pkg -> Node
	pkgNode := idx.Root.Children["Pkg"]
	if pkgNode == nil {
		t.Fatal("Pkg node should exist")
	}
	if pkgNode.Children["Node"] == nil {
		t.Fatal("Node should exist")
	}

	// Update file: remove +Node
	content2 := `
#package Pkg
// Removed node
`
	p2 := parser.NewParser(content2)
	cfg2, _ := p2.Parse()
	idx.AddFile(file, cfg2)

	// Check Node is gone
	pkgNode = idx.Root.Children["Pkg"]
	if pkgNode == nil {
		// Pkg should exist because of #package Pkg
		t.Fatal("Pkg node should exist after update")
	}
	if pkgNode.Children["Node"] != nil {
		t.Error("Node should be gone")
	}

	// Test removing file completely
	idx.RemoveFile(file)
	if len(idx.Root.Children) != 0 {
		t.Errorf("Root should be empty after removing file, got %d children", len(idx.Root.Children))
	}
}
