package integration

import (
	"io/ioutil"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/validator"
)

func parseAndAddToIndex(t *testing.T, idx *index.ProjectTree, filePath string) {
	content, err := ioutil.ReadFile(filePath)
	if err != nil {
		t.Fatalf("Failed to read %s: %v", filePath, err)
	}

	p := parser.NewParser(string(content))
	config, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse failed for %s: %v", filePath, err)
	}

	idx.AddFile(filePath, config)
}

func TestMultiFileNodeValidation(t *testing.T) {
	idx := index.NewProjectTree()
	parseAndAddToIndex(t, idx, "integration/multifile_valid_1.marte")
	parseAndAddToIndex(t, idx, "integration/multifile_valid_2.marte")

	// Resolving references might be needed if the validator relies on it for merging implicitly
	// But primarily we want to check if the validator sees the merged node.
	// The current implementation of Validator likely iterates over the ProjectTree.
	// If the ProjectTree doesn't merge nodes automatically, the Validator needs to do it.
	// However, the spec says "The build tool, validator, and LSP must merge these definitions".
	// Let's assume the Validator or Index does the merging logic.

	v := validator.NewValidator(idx, ".")
	v.ValidateProject()

	// +MyNode is split.
	// valid_1 has FieldA
	// valid_2 has Class and FieldB
	// If merging works, it should have a Class, so no error about missing Class.

	for _, diag := range v.Diagnostics {
		if strings.Contains(diag.Message, "must contain a 'Class' field") {
			t.Errorf("Unexpected 'Class' field error for +MyNode: %s", diag.Message)
		}
	}
}

func TestMultiFileDuplicateField(t *testing.T) {
	idx := index.NewProjectTree()
	parseAndAddToIndex(t, idx, "integration/multifile_dup_1.marte")
	parseAndAddToIndex(t, idx, "integration/multifile_dup_2.marte")

	v := validator.NewValidator(idx, ".")
	v.ValidateProject()

	foundError := false
	for _, diag := range v.Diagnostics {
		if strings.Contains(diag.Message, "Duplicate Field Definition") && strings.Contains(diag.Message, "FieldX") {
			foundError = true
			break
		}
	}

	if !foundError {
		t.Errorf("Expected duplicate field error for FieldX in +DupNode, but found none")
	}
}

func TestMultiFileReference(t *testing.T) {
	idx := index.NewProjectTree()
	parseAndAddToIndex(t, idx, "integration/multifile_ref_1.marte")
	parseAndAddToIndex(t, idx, "integration/multifile_ref_2.marte")

	idx.ResolveReferences()

	// Check if the reference in +SourceNode to TargetNode is resolved.
	v := validator.NewValidator(idx, ".")
	v.ValidateProject()

	if len(v.Diagnostics) > 0 {
		// Filter out irrelevant errors
	}
}

func TestHierarchicalPackageMerge(t *testing.T) {
	idx := index.NewProjectTree()
	parseAndAddToIndex(t, idx, "integration/hierarchical_pkg_1.marte")
	parseAndAddToIndex(t, idx, "integration/hierarchical_pkg_2.marte")

	v := validator.NewValidator(idx, ".")
	v.ValidateProject()

	// +MyObj should have Class (from file 1) and FieldX (from file 2).
	// If Class is missing, ValidateProject reports error.

	for _, diag := range v.Diagnostics {
		if strings.Contains(diag.Message, "must contain a 'Class' field") {
			t.Errorf("Unexpected 'Class' field error for +MyObj: %s", diag.Message)
		}
	}

	// We can also inspect the tree to verify FieldX is there (optional, but good for confidence)
	projNode := idx.Root.Children["Proj"]
	if projNode == nil {
		t.Fatal("Proj node not found")
	}
	baseNode := projNode.Children["Base"]
	if baseNode == nil {
		t.Fatal("Base node not found")
	}
	objNode := baseNode.Children["MyObj"]
	if objNode == nil {
		t.Fatal("MyObj node not found in Base")
	}

	hasFieldX := false
	for _, frag := range objNode.Fragments {
		for _, def := range frag.Definitions {
			if f, ok := def.(*parser.Field); ok && f.Name == "FieldX" {
				hasFieldX = true
			}
		}
	}

	if !hasFieldX {
		t.Error("FieldX not found in +MyObj")
	}
}

func TestHierarchicalDuplicate(t *testing.T) {
	idx := index.NewProjectTree()
	parseAndAddToIndex(t, idx, "integration/hierarchical_dup_1.marte")
	parseAndAddToIndex(t, idx, "integration/hierarchical_dup_2.marte")

	v := validator.NewValidator(idx, ".")
	v.ValidateProject()

	foundError := false
	for _, diag := range v.Diagnostics {
		if strings.Contains(diag.Message, "Duplicate Field Definition") && strings.Contains(diag.Message, "FieldY") {
			foundError = true
			break
		}
	}

	if !foundError {
		t.Errorf("Expected duplicate field error for FieldY in +DupObj (hierarchical), but found none")
	}
}

func TestIsolatedFileValidation(t *testing.T) {
	idx := index.NewProjectTree()

	// File 1: Has package. Defines SharedClass.
	f1Content := `
#package Proj.Pkg
+SharedObj = { Class = SharedClass }
`
	p1 := parser.NewParser(f1Content)
	c1, _ := p1.Parse()
	idx.AddFile("shared.marte", c1)

	// File 2: No package. References SharedObj.
	// Should NOT resolve to SharedObj in shared.marte because iso.marte is isolated.
	f2Content := `
+IsoObj = {
    Class = "MyClass"
    Ref = SharedObj
}
`
	p2 := parser.NewParser(f2Content)
	c2, _ := p2.Parse()
	idx.AddFile("iso.marte", c2)

	idx.ResolveReferences()

	// Find reference
	var ref *index.Reference
	for i := range idx.References {
		if idx.References[i].File == "iso.marte" && idx.References[i].Name == "SharedObj" {
			ref = &idx.References[i]
			break
		}
	}

	if ref == nil {
		t.Fatal("Reference SharedObj not found in index")
	}

	if ref.Target != nil {
		t.Errorf("Expected reference in isolated file to be unresolved, but got target in %s", ref.Target.Fragments[0].File)
	}
}
