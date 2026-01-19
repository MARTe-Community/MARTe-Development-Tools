package integration

import (
	"bytes"
	"io/ioutil"
	"strings"
	"testing"

	"github.com/marte-dev/marte-dev-tools/internal/formatter"
	"github.com/marte-dev/marte-dev-tools/internal/index"
	"github.com/marte-dev/marte-dev-tools/internal/parser"
	"github.com/marte-dev/marte-dev-tools/internal/validator"
)

func TestCheckCommand(t *testing.T) {
	inputFile := "integration/error.marte"
	content, err := ioutil.ReadFile(inputFile)
	if err != nil {
		t.Fatalf("Failed to read %s: %v", inputFile, err)
	}

	p := parser.NewParser(string(content))
	config, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	idx := index.NewIndex()
	idx.IndexConfig(inputFile, config)
	idx.ResolveReferences()

	v := validator.NewValidator(idx)
	v.Validate(inputFile, config)
	v.CheckUnused()

	foundError := false
	for _, diag := range v.Diagnostics {
		if strings.Contains(diag.Message, "must contain a 'Class' field") {
			foundError = true
			break
		}
	}

	if !foundError {
		t.Errorf("Expected 'Class' field error in %s, but found none", inputFile)
	}
}

func TestFmtCommand(t *testing.T) {
	inputFile := "integration/fmt.marte"
	content, err := ioutil.ReadFile(inputFile)
	if err != nil {
		t.Fatalf("Failed to read %s: %v", inputFile, err)
	}

	p := parser.NewParser(string(content))
	config, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	var buf bytes.Buffer
	formatter.Format(config, &buf)

	output := buf.String()
	
	// Check for indentation
	if !strings.Contains(output, "  Class = \"MyClass\"") {
		t.Error("Expected 2-space indentation for Class field")
	}

	// Check for sticky comments (no blank line between comment and field)
	// We expect:
	//   // Sticky comment
	//   Field = 123
	if !strings.Contains(output, "  // Sticky comment\n  Field = 123") {
		t.Errorf("Expected sticky comment to be immediately followed by field, got:\n%s", output)
	}

	if !strings.Contains(output, "Array = { 1 2 3 }") {
		t.Errorf("Expected formatted array '{ 1 2 3 }', got: %s", output)
	}

	// Check for inline comments
	inputFile2 := "integration/fmt_inline.marte"
	content2, err := ioutil.ReadFile(inputFile2)
	if err != nil {
		t.Fatalf("Failed to read %s: %v", inputFile2, err)
	}

	p2 := parser.NewParser(string(content2))
	config2, err := p2.Parse()
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	var buf2 bytes.Buffer
	formatter.Format(config2, &buf2)
	output2 := buf2.String()

	if !strings.Contains(output2, "+Node = { // Comment after open brace") {
		t.Error("Expected inline comment after open brace")
	}
	if !strings.Contains(output2, "Field1 = \"Value\" // Comment after value") {
		t.Error("Expected inline comment after field value")
	}
}
