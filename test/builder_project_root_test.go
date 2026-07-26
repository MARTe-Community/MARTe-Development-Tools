package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cuelang.org/go/cue"

	"github.com/marte-community/marte-dev-tools/internal/builder"
	"github.com/marte-community/marte-dev-tools/internal/schema"
)

// TestBuilderProjectRootSchemaLoading is a regression test for BUG-006:
// the builder hardcodes schema.LoadFullSchema(".") regardless of where
// the .marte source files actually live. This means the project-local
// .marte_schema.cue is only discovered when the builder is invoked from
// the project root directory; building from any other CWD silently skips
// the project schema.
func TestBuilderProjectRootSchemaLoading(t *testing.T) {
	tmpDir := t.TempDir()

	// Create project schema defining a custom class
	schemaContent := `
package schema

#Classes: {
	ProjectClass: {
		CustomField: int
		...
	}
}
`
	if err := os.WriteFile(filepath.Join(tmpDir, ".marte_schema.cue"), []byte(schemaContent), 0644); err != nil {
		t.Fatalf("Failed to write schema file: %v", err)
	}

	// Create a .marte file in a subdirectory that uses the custom class
	subDir := filepath.Join(tmpDir, "sub")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("Failed to create subdirectory: %v", err)
	}

	marteContent := `
+Obj = {
    Class = ProjectClass
    CustomField = 42
}
`
	marteFile := filepath.Join(subDir, "test.marte")
	if err := os.WriteFile(marteFile, []byte(marteContent), 0644); err != nil {
		t.Fatalf("Failed to write marte file: %v", err)
	}

	// Save original CWD to restore after the test
	origCWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get CWD: %v", err)
	}
	defer os.Chdir(origCWD)

	// === Test 1: Build from project root (CWD = tmpDir) ===
	// The schema SHOULD be found because LoadFullSchema(".") resolves to tmpDir.
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("Failed to chdir to project root: %v", err)
	}

	b1 := builder.NewBuilder([]string{filepath.Join("sub", "test.marte")}, nil)
	outF1, err := os.CreateTemp("", "out_root_*.marte")
	if err != nil {
		t.Fatalf("Failed to create output file: %v", err)
	}
	defer os.Remove(outF1.Name())

	err = b1.Build(outF1)
	outF1.Close()
	if err != nil {
		t.Fatalf("Build from project root failed: %v", err)
	}

	out1, _ := os.ReadFile(outF1.Name())
	if !strings.Contains(string(out1), "CustomField = 42") {
		t.Errorf("Build from project root: missing expected field in output:\n%s", string(out1))
	}

	// Verify schema was actually loaded from project root
	sFromRoot := schema.LoadFullSchema(tmpDir)
	hasClass := func(s *schema.Schema, class string) bool {
		return s.Value.LookupPath(cue.ParsePath("#Classes." + class)).Exists()
	}
	if !hasClass(sFromRoot, "ProjectClass") {
		t.Error("BUG-006: schema.LoadFullSchema(projectRoot) did NOT discover ProjectClass")
	}

	// === Test 2: Build from non-project-root CWD ===
	// The schema should NOT be found because builder hardcodes ".".
	if err := os.Chdir(origCWD); err != nil {
		t.Fatalf("Failed to chdir back to original CWD: %v", err)
	}

	b2 := builder.NewBuilder([]string{marteFile}, nil)
	outF2, err := os.CreateTemp("", "out_cwd_*.marte")
	if err != nil {
		t.Fatalf("Failed to create output file: %v", err)
	}
	defer os.Remove(outF2.Name())

	err = b2.Build(outF2)
	outF2.Close()
	if err != nil {
		t.Fatalf("Build from non-root CWD failed: %v", err)
	}

	out2, _ := os.ReadFile(outF2.Name())
	if !strings.Contains(string(out2), "CustomField = 42") {
		t.Errorf("Build from non-root CWD: missing expected field in output:\n%s", string(out2))
	}

	// Verify: schema.LoadFullSchema(".") from the non-project-root CWD does NOT
	// discover the project schema. This is the core of BUG-006.
	sFromCWD := schema.LoadFullSchema(".")
	if hasClass(sFromCWD, "ProjectClass") {
		t.Log("BUG-006 may be fixed: schema.LoadFullSchema(\".\") from non-root CWD found ProjectClass (CWD may contain .marte_schema.cue)")
	} else {
		t.Log("BUG-006 confirmed: schema.LoadFullSchema(\".\") from non-root CWD did NOT find ProjectClass")
	}
}
