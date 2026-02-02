package integration

import (
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
)

func TestIsolatedFileIsolation(t *testing.T) {
	pt := index.NewProjectTree()

	// File 1: Project file
	f1 := "#package P\n+A = { Class = C }"
	p1 := parser.NewParser(f1)
	c1, _ := p1.Parse()
	pt.AddFile("f1.marte", c1)

	// File 2: Isolated file
	f2 := "+B = { Class = C }"
	p2 := parser.NewParser(f2)
	c2, _ := p2.Parse()
	pt.AddFile("f2.marte", c2)

	pt.ResolveReferences()

	// Try finding A from f2
	isoNode := pt.IsolatedFiles["f2.marte"]
	if pt.ResolveName(isoNode, "A", nil) != nil {
		t.Error("Isolated file f2 should not see global A")
	}

	// Try finding B from f1
	pNode := pt.Root.Children["P"]
	if pt.ResolveName(pNode, "B", nil) != nil {
		t.Error("Project file f1 should not see isolated B")
	}
}