package integration

import (
	"bytes"
	"io/ioutil"
	"os"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/builder"
	"github.com/marte-community/marte-dev-tools/internal/formatter"
	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/validator"
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

	idx := index.NewProjectTree()
	idx.AddFile(inputFile, config)

	v := validator.NewValidator(idx, ".", nil)
	v.ValidateProject()
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

func TestCheckDuplicate(t *testing.T) {
	inputFile := "integration/check_dup.marte"
	content, err := ioutil.ReadFile(inputFile)
	if err != nil {
		t.Fatalf("Failed to read %s: %v", inputFile, err)
	}

	p := parser.NewParser(string(content))
	config, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	idx := index.NewProjectTree()
	idx.AddFile(inputFile, config)

	v := validator.NewValidator(idx, ".", nil)
	v.ValidateProject()

	foundError := false
	for _, diag := range v.Diagnostics {
		if strings.Contains(diag.Message, "Duplicate Field Definition") {
			foundError = true
			break
		}
	}

	if !foundError {
		t.Errorf("Expected duplicate field error in %s, but found none", inputFile)
	}
}

func TestSignalNoClassValidation(t *testing.T) {
	inputFile := "integration/signal_no_class.marte"
	content, err := ioutil.ReadFile(inputFile)
	if err != nil {
		t.Fatalf("Failed to read %s: %v", inputFile, err)
	}

	p := parser.NewParser(string(content))
	config, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	idx := index.NewProjectTree()
	idx.AddFile(inputFile, config)

	v := validator.NewValidator(idx, ".", nil)
	v.ValidateProject()

	if len(v.Diagnostics) > 0 {
		t.Errorf("Expected no errors for signal without Class, but got: %v", v.Diagnostics)
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
	if !strings.Contains(output, "  // Sticky comment\n  Field = 123") {
		t.Error("Expected sticky comment to be immediately followed by field")
	}

	if !strings.Contains(output, "Array = { 1, 2, 3 }") {
		t.Errorf("Expected formatted array '{ 1, 2, 3 }', got: %s", output)
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

func TestBuildCommand(t *testing.T) {
	// Clean previous build
	os.RemoveAll("build_test")
	os.MkdirAll("build_test", 0755)
	defer os.RemoveAll("build_test")

	// Test Merge
	files := []string{"integration/build_merge_1.marte", "integration/build_merge_2.marte"}
	b := builder.NewBuilder(files, nil)

	outputFile, err := os.Create("build_test/TEST.marte")
	if err != nil {
		t.Fatalf("Failed to create output file: %v", err)
	}
	defer outputFile.Close()

	err = b.Build(outputFile)
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	// Check output existence
	if _, err := os.Stat("build_test/TEST.marte"); os.IsNotExist(err) {
		t.Fatalf("Expected output file build_test/TEST.marte not found")
	}

	content, _ := ioutil.ReadFile("build_test/TEST.marte")
	output := string(content)

	if !strings.Contains(output, "FieldA = 1") || !strings.Contains(output, "FieldB = 2") {
		t.Error("Merged output missing fields")
	}

	// Test Order (Class First)
	filesOrder := []string{"integration/build_order_1.marte", "integration/build_order_2.marte"}
	bOrder := builder.NewBuilder(filesOrder, nil)

	outputFileOrder, err := os.Create("build_test/ORDER.marte")
	if err != nil {
		t.Fatalf("Failed to create output file: %v", err)
	}
	defer outputFileOrder.Close()

	err = bOrder.Build(outputFileOrder)
	if err != nil {
		t.Fatalf("Build order test failed: %v", err)
	}

	contentOrder, _ := ioutil.ReadFile("build_test/ORDER.marte")
	outputOrder := string(contentOrder)

	// Check for Class before Field
	classIdx := strings.Index(outputOrder, "Class = \"Ordered\"")
	fieldIdx := strings.Index(outputOrder, "Field = 1")

	if classIdx == -1 || fieldIdx == -1 {
		t.Fatal("Missing Class or Field in ordered output")
	}
	if classIdx > fieldIdx {
		t.Error("Expected Class to appear before Field in merged output")
	}
}
