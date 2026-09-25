package integration

import (
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
)

// markTree indexes src under file.
func markTree(t *testing.T, pt *index.ProjectTree, file, src string) {
	t.Helper()
	cfg, err := parser.NewParser(src).Parse()
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}
	pt.AddFile(file, cfg)
}

func findRealName(pt *index.ProjectTree, name string) *index.ProjectNode {
	var found *index.ProjectNode
	pt.Walk(func(n *index.ProjectNode) {
		if n.RealName == name || n.Name == name {
			found = n
		}
	})
	return found
}

// TestSharedPackageChainSurvivesFileReplacement pins the ownership rule: files
// sharing a #package share the package node chain, so replacing one file must
// only drop that file's fragments -- never the subtree other files contribute
// to. Replacing a file used to delete child nodes "owned" by it, which took
// the other file's objects down with it.
func TestSharedPackageChainSurvivesFileReplacement(t *testing.T) {
	pt := index.NewProjectTree()
	markTree(t, pt, "a.marte", "#package my.project\n+A = { Class = ReferenceContainer }\n")
	markTree(t, pt, "b.marte", "#package my.project\n+B = { Class = ReferenceContainer }\n")

	if findRealName(pt, "+B") == nil {
		t.Fatal("B missing after both files were indexed")
	}

	// Replace a.marte: A goes away, B has to survive.
	markTree(t, pt, "a.marte", "#package my.project\n+A2 = { Class = ReferenceContainer }\n")
	if findRealName(pt, "+B") == nil {
		t.Error("replacing a.marte dropped +B from the shared package tree")
	}
	if findRealName(pt, "+A") != nil {
		t.Error("replacing a.marte left the old +A node behind")
	}
	if findRealName(pt, "+A2") == nil {
		t.Error("replacing a.marte did not add +A2")
	}

	// Removing a.marte entirely must keep B as well.
	pt.RemoveFile("a.marte")
	if findRealName(pt, "+B") == nil {
		t.Error("removing a.marte dropped +B")
	}
	if findRealName(pt, "+A2") != nil {
		t.Error("removing a.marte left +A2 behind")
	}
}

// TestIndexingManyFilesKeepsContent indexes many files into one tree. The
// timing consequence is asserted at the LSP level
// (e2e TestLSPInitializeScalesWithWorkspaceSize); here the point is that the
// fast path in AddFile (files that are not in the tree yet skip the removal
// walk) leaves the tree populated.
func TestIndexingManyFilesKeepsContent(t *testing.T) {
	const files = 1200
	configs := make([]*parser.Configuration, files)
	for i := range configs {
		cfg, err := parser.NewParser("#package p\n+App = { Class = ReferenceContainer }\n").Parse()
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		configs[i] = cfg
	}

	pt := index.NewProjectTree()
	for i, cfg := range configs {
		pt.AddFile(filePathForIndex(i), cfg)
	}
	// The point is the timing below; reaching here with the tree intact is the
	// functional check.
	if findRealName(pt, "App") == nil {
		t.Error("indexed content missing")
	}
}

func filePathForIndex(i int) string {
	const digits = "0123456789"
	s := "p/f"
	if i == 0 {
		return s + "0.marte"
	}
	var buf []byte
	for i > 0 {
		buf = append([]byte{digits[i%10]}, buf...)
		i /= 10
	}
	return s + string(buf) + ".marte"
}
