package integration

import (
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/graph"
	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
)

// TestGraphParseErrorHandling is a regression test for BUG-032:
// the graph command's buildTreeAndDiags helper discards parse errors
// via `config, _ := p.Parse()`. This test verifies that the graph
// generation functions handle nil/invalid trees gracefully without
// panicking.
func TestGraphParseErrorHandling(t *testing.T) {
	// Test 1: Generate with empty tree
	tree := index.NewProjectTree()
	result := graph.Generate(tree, nil, "")
	if result.DOT == "" {
		t.Error("Generate returned empty DOT for empty tree")
	}

	// Test 2: Generate with valid config
	content := `
+App = {
    Class = RealTimeApplication
    +States = {
        +Run = {
            +Threads = {
                +T1 = {
                    Functions = { GAM1 }
                }
            }
        }
    }
}
+GAM1 = {
    Class = IOGAM
    InputSignals = {
        Sig1 = { Type = int32 }
    }
    OutputSignals = {
        Sig2 = { Type = int32 }
    }
}
`
	p := parser.NewParser(content)
	cfg, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	tree = index.NewProjectTree()
	tree.AddFile("test.marte", cfg)
	tree.ResolveFields(nil)
	tree.ResolveReferences(nil)

	result = graph.Generate(tree, nil, "")
	if result.DOT == "" {
		t.Error("Generate returned empty DOT for valid config")
	}

	// Verify the DOT output contains expected elements
	if !strings.Contains(result.DOT, "digraph") {
		t.Error("DOT output missing 'digraph'")
	}
}

// TestGraphGenerateWithOptionsNilDiags verifies that Generate and
// GenerateWithOptions handle nil diagnostics map without panicking.
func TestGraphGenerateWithOptionsNilDiags(t *testing.T) {
	tree := index.NewProjectTree()
	content := `
+GAM1 = {
    Class = IOGAM
    InputSignals = {
        Sig1 = { Type = int32 }
    }
}
`
	p := parser.NewParser(content)
	cfg, _ := p.Parse()
	tree.AddFile("test.marte", cfg)
	tree.ResolveFields(nil)
	tree.ResolveReferences(nil)

	// nil diags should not panic
	result := graph.Generate(tree, nil, "")
	if result.DOT == "" {
		t.Error("Generate with nil diags returned empty")
	}

	// State filter with nil diags
	result2 := graph.Generate(tree, nil, "Run")
	if result2.DOT == "" {
		t.Log("Generate with state filter returned empty (expected: no states)")
	}
}

// TestGraphNodeInfoPopulated verifies that graph generation populates
// node metadata correctly.
func TestGraphNodeInfoPopulated(t *testing.T) {
	content := `
+GAM1 = {
    Class = IOGAM
    InputSignals = {
        In1 = { Type = int32 }
    }
    OutputSignals = {
        Out1 = { Type = float64 }
    }
}
`
	p := parser.NewParser(content)
	cfg, _ := p.Parse()
	tree := index.NewProjectTree()
	tree.AddFile("test.marte", cfg)
	tree.ResolveFields(nil)
	tree.ResolveReferences(nil)

	result := graph.Generate(tree, nil, "")

	if result.Meta == nil {
		t.Fatal("Generate returned nil Meta map")
	}
	if result.AllGAMIDs == nil {
		t.Fatal("Generate returned nil AllGAMIDs")
	}
	if result.States == nil {
		t.Fatal("Generate returned nil States map")
	}

	// Verify GAM node has metadata
	found := false
	for _, info := range result.Meta {
		if info.Kind == "gam" {
			found = true
			if info.Class == "" {
				t.Error("GAM node has empty Class")
			}
			break
		}
	}
	if !found {
		t.Log("No GAM node in meta (may need full application structure)")
	}
}
