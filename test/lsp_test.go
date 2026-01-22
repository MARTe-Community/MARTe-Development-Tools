package integration

import (
	"io/ioutil"
	"testing"

	"github.com/marte-dev/marte-dev-tools/internal/index"
	"github.com/marte-dev/marte-dev-tools/internal/parser"
	"github.com/marte-dev/marte-dev-tools/internal/validator"
)

// Helper to load and parse a file
func loadConfig(t *testing.T, filename string) *parser.Configuration {
	content, err := ioutil.ReadFile(filename)
	if err != nil {
		t.Fatalf("Failed to read %s: %v", filename, err)
	}
	p := parser.NewParser(string(content))
	config, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	return config
}

func TestLSPDiagnostics(t *testing.T) {
	inputFile := "integration/check_dup.marte"
	config := loadConfig(t, inputFile)
	
	// Simulate LSP logic: Build Index -> Validate
	idx := index.NewProjectTree()
	idx.AddFile(inputFile, config)
	
	v := validator.NewValidator(idx, ".")
	v.ValidateProject()
	
	// Check for expected diagnostics
	found := false
	for _, d := range v.Diagnostics {
		if d.Message == "Duplicate Field Definition: 'Field' is already defined in integration/check_dup.marte" {
			found = true
			if d.Position.Line != 5 {
				t.Errorf("Expected diagnostic at line 5, got %d", d.Position.Line)
			}
			break
		}
	}
	if !found {
		t.Error("LSP Diagnostic for duplicate field not found")
	}
}

// For GoToDefinition and References, we need to test the Indexer's ability to resolve symbols.
// Currently, my Indexer (ProjectTree) stores structure but doesn't explicitly track 
// "references" in a way that maps a source position to a target symbol yet.
// The ProjectTree is built for structure merging.
// To support LSP "Go To Definition", we need to map usage -> definition.

// Let's verify what we have implemented.
// `internal/index/index.go`:
//   ProjectTree has Fragments.
//   It does NOT have a "Lookup(position)" method or a reference map.
//   Previously (before rewrite), `index.go` had `References []Reference`.
//   I removed it during the rewrite to ProjectTree!

// I need to re-implement reference tracking in `ProjectTree` or a parallel structure 
// to support LSP features.
func TestLSPDefinition(t *testing.T) {
	// Create a virtual file content with a definition and a reference
	content := `
+MyObject = {
    Class = Type
}
+RefObject = {
    Class = Type
    RefField = MyObject
}
`
	p := parser.NewParser(content)
	config, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	idx := index.NewProjectTree()
	idx.AddFile("memory.marte", config)
	idx.ResolveReferences()

	// Find the reference to "MyObject"
	var foundRef *index.Reference
	for _, ref := range idx.References {
		if ref.Name == "MyObject" {
			foundRef = &ref
			break
		}
	}
	
	if foundRef == nil {
		t.Fatal("Reference to MyObject not found in index")
	}
	
	if foundRef.Target == nil {
		t.Fatal("Reference to MyObject was not resolved to a target")
	}
	
	if foundRef.Target.RealName != "+MyObject" {
		t.Errorf("Expected target to be +MyObject, got %s", foundRef.Target.RealName)
	}
}

func TestLSPHover(t *testing.T) {
	content := `
+MyObject = {
    Class = Type
}
`
	p := parser.NewParser(content)
	config, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	idx := index.NewProjectTree()
	file := "hover.marte"
	idx.AddFile(file, config)
	
	// +MyObject is at line 2.
	// Query at line 2, col 2 (on 'M' of MyObject)
	res := idx.Query(file, 2, 2)
	
	if res == nil {
		t.Fatal("Query returned nil")
	}
	
	if res.Node == nil {
		t.Fatal("Expected Node result")
	}
	
	if res.Node.RealName != "+MyObject" {
		t.Errorf("Expected +MyObject, got %s", res.Node.RealName)
	}
}

func TestParserError(t *testing.T) {
	invalidContent := `
A = {
  Field = 
}
`
	p := parser.NewParser(invalidContent)
	_, err := p.Parse()
	if err == nil {
		t.Fatal("Expected parser error, got nil")
	}
}
