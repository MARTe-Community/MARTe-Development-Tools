package integration

import (
	"os"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/builder"
)

func TestMultiFileBuildMergeAndOrder(t *testing.T) {
	// Setup
	os.RemoveAll("build_multi_test")
	os.MkdirAll("build_multi_test", 0755)
	defer os.RemoveAll("build_multi_test")

	// Create source files
	// File 1: Has FieldA, no Class.
	// File 2: Has Class, FieldB.
	// Both in package +MyObj

	f1Content := `
#package Proj.+MyObj
FieldA = 10
`
	f2Content := `
#package Proj.+MyObj
Class = "MyClass"
FieldB = 20
`
	os.WriteFile("build_multi_test/f1.marte", []byte(f1Content), 0644)
	os.WriteFile("build_multi_test/f2.marte", []byte(f2Content), 0644)

	// Execute Build
	b := builder.NewBuilder([]string{"build_multi_test/f1.marte", "build_multi_test/f2.marte"})

	// Prepare output file
	// Should be +MyObj.marte (normalized MyObj.marte) - Actually checking content
	outputFile := "build_multi_test/MyObj.marte"
	f, err := os.Create(outputFile)
	if err != nil {
		t.Fatalf("Failed to create output file: %v", err)
	}
	defer f.Close()

	err = b.Build(f)
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}
	f.Close() // Close to flush

	// Check Output
	if _, err := os.Stat(outputFile); os.IsNotExist(err) {
		t.Fatalf("Expected output file not found")
	}

	content, err := os.ReadFile(outputFile)
	if err != nil {
		t.Fatalf("Failed to read output: %v", err)
	}

	output := string(content)

	// Check presence
	if !strings.Contains(output, "Class = \"MyClass\"") {
		t.Error("Output missing Class")
	}
	if !strings.Contains(output, "FieldA = 10") {
		t.Error("Output missing FieldA")
	}
	if !strings.Contains(output, "FieldB = 20") {
		t.Error("Output missing FieldB")
	}

	// Check Order: Class/FieldB (from f2) should come BEFORE FieldA (from f1)
	// because f2 has the Class definition.

	idxClass := strings.Index(output, "Class")
	idxFieldB := strings.Index(output, "FieldB")
	idxFieldA := strings.Index(output, "FieldA")

	if idxClass == -1 || idxFieldB == -1 || idxFieldA == -1 {
		t.Fatal("Missing fields in output")
	}

	// Class should be first
	if idxClass > idxFieldA {
		t.Errorf("Expected Class (from f2) to be before FieldA (from f1). Output:\n%s", output)
	}

	// FieldB should be near Class (same fragment)
	// FieldA should be after
	if idxFieldB > idxFieldA {
		t.Errorf("Expected FieldB (from f2) to be before FieldA (from f1). Output:\n%s", output)
	}
}
