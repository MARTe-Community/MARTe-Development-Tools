package integration

import (
	"testing"

	"github.com/marte-dev/marte-dev-tools/internal/index"
	"github.com/marte-dev/marte-dev-tools/internal/parser"
)

func TestGetNodeContaining(t *testing.T) {
	content := `
+App = {
    Class = RealTimeApplication
    +State1 = {
        Class = RealTimeState
        +Thread1 = {
            Class = RealTimeThread
            Functions = { GAM1 }
        }
    }
}
+GAM1 = { Class = IOGAM }
`
	p := parser.NewParser(content)
	config, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	idx := index.NewProjectTree()
	file := "hover_context.marte"
	idx.AddFile(file, config)
	idx.ResolveReferences()

	// Find reference to GAM1
	var gamRef *index.Reference
	for i := range idx.References {
		ref := &idx.References[i]
		if ref.Name == "GAM1" {
			gamRef = ref
			break
		}
	}

	if gamRef == nil {
		t.Fatal("Reference to GAM1 not found")
	}

	// Check containing node
	container := idx.GetNodeContaining(file, gamRef.Position)
	if container == nil {
		t.Fatal("Container not found")
	}

	if container.RealName != "+Thread1" {
		t.Errorf("Expected container +Thread1, got %s", container.RealName)
	}

	// Check traversal up to State
	curr := container
	foundState := false
	for curr != nil {
		if curr.RealName == "+State1" {
			foundState = true
			break
		}
		curr = curr.Parent
	}

	if !foundState {
		t.Error("State parent not found")
	}
}
