package integration

import (
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
)

func TestNodeMap(t *testing.T) {
	pt := index.NewProjectTree()
	root := pt.Root

	// Create structure: +A -> +B -> +C
	nodeA := &index.ProjectNode{Name: "A", RealName: "+A", Children: make(map[string]*index.ProjectNode), Parent: root}
	root.Children["A"] = nodeA

	nodeB := &index.ProjectNode{Name: "B", RealName: "+B", Children: make(map[string]*index.ProjectNode), Parent: nodeA}
	nodeA.Children["B"] = nodeB

	nodeC := &index.ProjectNode{Name: "C", RealName: "+C", Children: make(map[string]*index.ProjectNode), Parent: nodeB}
	nodeB.Children["C"] = nodeC

	// Rebuild Index
	pt.RebuildIndex()

	// Find by Name
	found := pt.FindNode(root, "C", nil, false)
	if found != nodeC {
		t.Errorf("FindNode(C) failed. Got %v, want %v", found, nodeC)
	}

	// Find by RealName
	found = pt.FindNode(root, "+C", nil, false)
	if found != nodeC {
		t.Errorf("FindNode(+C) failed. Got %v, want %v", found, nodeC)
	}

	// Find by Path
	found = pt.FindNode(root, "A.B.C", nil, false)
	if found != nodeC {
		t.Errorf("FindNode(A.B.C) failed. Got %v, want %v", found, nodeC)
	}

	// Find by Path with RealName
	found = pt.FindNode(root, "+A.+B.+C", nil, false)
	if found != nodeC {
		t.Errorf("FindNode(+A.+B.+C) failed. Got %v, want %v", found, nodeC)
	}
}

func TestResolveReferencesWithMap(t *testing.T) {
	pt := index.NewProjectTree()
	root := pt.Root

	nodeA := &index.ProjectNode{Name: "A", RealName: "+A", Children: make(map[string]*index.ProjectNode), Parent: root}
	root.Children["A"] = nodeA

	// Use IndexValue to populate FileReferences
	pt.IndexValue("test.marte", &parser.ReferenceValue{Value: "A"})

	pt.ResolveReferences()

	// Check resolution in FileReferences
	refs := pt.FileReferences["test.marte"]
	if len(refs) == 0 {
		t.Fatal("No references found")
	}
	if refs[0].Target != nodeA {
		t.Error("ResolveReferences failed to resolve A")
	}
}
