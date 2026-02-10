package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/validator"
)

func TestScanDirectory_SymlinkDuplication(t *testing.T) {
	// 1. Setup directories
	rootDir := t.TempDir()

	// 2. Create a file
	marteFile := filepath.Join(rootDir, "main.marte")
	err := os.WriteFile(marteFile, []byte(`+Node = { Class = MyClass }`), 0644)
	if err != nil {
		t.Fatalf("Failed to create file: %v", err)
	}

	// 3. Create Symlink in root pointing to root (recursive loop, but distinct path)
	// Or pointing to a subdir that contains the file?
	// The user said "linked folder project".
	// Case A: root/A/file.marte AND root/B -> root/A.
	// Scanner visits root/A/file.marte AND root/B/file.marte.
	
	dirA := filepath.Join(rootDir, "A")
	os.Mkdir(dirA, 0755)
	fileA := filepath.Join(dirA, "test.marte")
	os.WriteFile(fileA, []byte(`package MyPackage
	MyField = 10`), 0644)

    // Symlink to FILE
	linkFile := filepath.Join(rootDir, "link.marte")
	err = os.Symlink(fileA, linkFile)
	if err != nil {
		t.Skipf("Symlinks not supported: %v", err)
	}

	// 4. Scan root
	pt := index.NewProjectTree()
	err = pt.ScanDirectory(rootDir)
	if err != nil {
		t.Fatalf("ScanDirectory failed: %v", err)
	}

	// 5. Validate
	v := validator.NewValidator(pt, rootDir, nil)
	v.ValidateProject(context.Background())

	// 6. Check for duplicates (Expect NONE)
	for _, d := range v.Diagnostics {
		if strings.Contains(d.Message, "Duplicate Field") {
			t.Errorf("Unexpected duplicate error for symlinked file: %s", d.Message)
		}
	}
	
	// Verify we didn't index the same file twice
	
	if len(pt.IsolatedFiles) != 2 {
		t.Errorf("Expected 2 isolated files (deduplicated), got %d", len(pt.IsolatedFiles))
	}
}
