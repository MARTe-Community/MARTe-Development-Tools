package integration

import (
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
	v.ValidateProject()

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
