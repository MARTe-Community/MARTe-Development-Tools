package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/validator"
)

func TestProjectSpecificSchema(t *testing.T) {
	// Create temp dir
	tmpDir, err := os.MkdirTemp("", "mdt_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Define project schema
	schemaContent := `
package schema

#Classes: {
	ProjectClass: {
		CustomField: int
		...
	}
}
`
	err = os.WriteFile(filepath.Join(tmpDir, ".marte_schema.cue"), []byte(schemaContent), 0644)
	if err != nil {
		t.Fatal(err)
	}

	// Define MARTe file using ProjectClass
	marteContent := `
+Obj = {
    Class = ProjectClass
    // Missing CustomField
}
`
	// We parse the content in memory, but we need the validator to look in tmpDir
	p := parser.NewParser(marteContent)
	config, err := p.Parse()
	if err != nil {
		t.Fatal(err)
	}

	idx := index.NewProjectTree()
	idx.AddFile("project.marte", config)

	// Pass tmpDir as projectRoot
	v := validator.NewValidator(idx, tmpDir, nil)
	v.ValidateProject(context.Background())

	found := false
	for _, d := range v.Diagnostics {
		if strings.Contains(d.Message, "CustomField: incomplete value") {
			found = true
			break
		}
	}

	if !found {
		t.Error("Expected error for missing 'CustomField' defined in project schema")
	}
}

// A project schema that fails to compile contributes nothing; the validator
// must say so instead of only flooding "Unknown Class" warnings for the
// classes the file declares.
func TestProjectSchemaCompileErrorReported(t *testing.T) {
	tmpDir := t.TempDir()
	schemaContent := `package schema

#Classes: {
	ProjectClass: {
		InputSize: list.Sum([1, 2])
		...
	}
}
`
	if err := os.WriteFile(filepath.Join(tmpDir, ".marte_schema.cue"), []byte(schemaContent), 0644); err != nil {
		t.Fatal(err)
	}

	config, err := parser.NewParser("+Obj = {\n    Class = ProjectClass\n}\n").Parse()
	if err != nil {
		t.Fatal(err)
	}
	idx := index.NewProjectTree()
	idx.AddFile(filepath.Join(tmpDir, "project.marte"), config)

	v := validator.NewValidator(idx, tmpDir, nil)
	v.ValidateProject(context.Background())

	var schemaErr, unknown bool
	for _, d := range v.Diagnostics {
		if d.Level == validator.LevelWarning && strings.Contains(d.Message, "Ignoring schema file") {
			schemaErr = true
			if !strings.Contains(d.Message, ".marte_schema.cue") || !strings.Contains(d.Message, "list") {
				t.Errorf("schema warning lacks the file or cause: %s", d.Message)
			}
			if d.File != filepath.Join(tmpDir, ".marte_schema.cue") {
				t.Errorf("schema warning anchored to %q, want the schema path", d.File)
			}
		}
		if strings.Contains(d.Message, "Unknown Class 'ProjectClass'") {
			unknown = true
		}
	}
	if !schemaErr {
		t.Errorf("schema compile error not reported, diagnostics: %+v", v.Diagnostics)
	}
	if !unknown {
		t.Errorf("expected the class to be unknown when its schema is ignored: %+v", v.Diagnostics)
	}
}
