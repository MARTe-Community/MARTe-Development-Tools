package integration

import (
	"testing"

	"github.com/marte-dev/marte-dev-tools/internal/index"
	"github.com/marte-dev/marte-dev-tools/internal/parser"
)

func TestLSPHoverDoc(t *testing.T) {
	content := `
//# Object Documentation
//# Second line
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
	file := "doc.marte"
	idx.AddFile(file, config)
	idx.ResolveReferences()
	
	// Test 1: Hover over +MyObject definition
	res := idx.Query(file, 4, 2) // Line 4: +MyObject
	if res == nil || res.Node == nil {
		t.Fatal("Query failed for definition")
	}
	
	expectedDoc := "Object Documentation\nSecond line"
	if res.Node.Doc != expectedDoc {
		t.Errorf("Expected definition doc:\n%q\nGot:\n%q", expectedDoc, res.Node.Doc)
	}
	
	// Test 2: Hover over MyObject reference
	resRef := idx.Query(file, 10, 16) // Line 10: RefField = MyObject
	if resRef == nil || resRef.Reference == nil {
		t.Fatal("Query failed for reference")
	}
	
	if resRef.Reference.Target == nil {
		t.Fatal("Reference target not resolved")
	}
	
	if resRef.Reference.Target.Doc != expectedDoc {
		t.Errorf("Expected reference target definition doc:\n%q\nGot:\n%q", expectedDoc, resRef.Reference.Target.Doc)
	}
}
