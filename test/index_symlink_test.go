package integration

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/index"
)

func TestScanDirectory_Symlinks(t *testing.T) {
	// 1. Setup directories
	rootDir := t.TempDir()
	externalDir := t.TempDir()

	// 2. Create external file
	extFile := filepath.Join(externalDir, "external.marte")
	err := os.WriteFile(extFile, []byte(`+Node = { Class = MyClass }`), 0644)
	if err != nil {
		t.Fatalf("Failed to create external file: %v", err)
	}

	// 3. Create Symlink in root pointing to externalDir
	linkPath := filepath.Join(rootDir, "linked_folder")
	err = os.Symlink(externalDir, linkPath)
	if err != nil {
		t.Skipf("Symlinks not supported or failed: %v", err) // Skip on Windows if privileges missing
	}

	// 4. Scan root
	pt := index.NewProjectTree()
	err = pt.ScanDirectory(rootDir)
	if err != nil {
		t.Fatalf("ScanDirectory failed: %v", err)
	}

	// 5. Verify Node exists
	// The file path in index will likely be the resolved path or the linked path depending on traversal?
	// filepath.Walk usually ignores symlinks to directories. So it might find nothing.
	
	// Check if we found "+Node"
	found := false
	pt.Walk(func(n *index.ProjectNode) {
		if n.RealName == "+Node" {
			found = true
		}
	})

	if !found {
		t.Errorf("ScanDirectory did not follow symlink to directory. Node '+Node' not found.")
	}
}
