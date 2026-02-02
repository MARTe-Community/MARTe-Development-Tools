package integration

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/index"
)

func TestRecursiveIndexing(t *testing.T) {
	// Setup: root/level1/level2/deep.marte
	rootDir, _ := os.MkdirTemp("", "rec_index")
	defer os.RemoveAll(rootDir)

	l1 := filepath.Join(rootDir, "level1")
	l2 := filepath.Join(l1, "level2")
	if err := os.MkdirAll(l2, 0755); err != nil {
		t.Fatal(err)
	}

	content := "#package Deep\n+DeepObj = { Class = A }"
	if err := os.WriteFile(filepath.Join(l2, "deep.marte"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	// Also add a file in root to ensure mixed levels work
	os.WriteFile(filepath.Join(rootDir, "root.marte"), []byte("#package Root\n+RootObj = { Class = A }"), 0644)

	// Scan
	tree := index.NewProjectTree()
	err := tree.ScanDirectory(rootDir)
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}

	// Verify Deep
	deepPkg := tree.Root.Children["Deep"]
	if deepPkg == nil {
		t.Fatal("Package Deep not found")
	}
	if deepPkg.Children["DeepObj"] == nil {
		t.Fatal("DeepObj not found in Deep package")
	}

	// Verify Root
	rootPkg := tree.Root.Children["Root"]
	if rootPkg == nil {
		t.Fatal("Package Root not found")
	}
	if rootPkg.Children["RootObj"] == nil {
		t.Fatal("RootObj not found in Root package")
	}
}
