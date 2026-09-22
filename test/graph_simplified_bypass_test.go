package integration

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/graph"
	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
)

// buildBypassTree parses a MARTe config and returns a resolved project tree.
func buildBypassTree(t *testing.T, content string) *index.ProjectTree {
	t.Helper()
	p := parser.NewParser(content)
	cfg, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	tree := index.NewProjectTree()
	tree.AddFile("test.marte", cfg)
	tree.ResolveFields(nil)
	tree.ResolveReferences(nil)
	return tree
}

// dotDanglingEndpoints parses DOT output and returns the node IDs that are
// referenced by edges but never defined as nodes (dangling arrows).
func dotDanglingEndpoints(dot string) []string {
	edgeRe := regexp.MustCompile(`(?m)^\s*([A-Za-z0-9_]+)(?::[A-Za-z0-9_]+)?\s*->\s*([A-Za-z0-9_]+)(?::[A-Za-z0-9_]+)?\s*\[`)
	nodeRe := regexp.MustCompile(`(?m)^\s*([A-Za-z0-9_]+)\s*\[`)

	defined := make(map[string]bool)
	for _, m := range nodeRe.FindAllStringSubmatch(dot, -1) {
		id := m[1]
		if id == "node" || id == "edge" {
			continue // graphviz default attribute statements
		}
		defined[id] = true
	}
	dangling := make(map[string]bool)
	for _, m := range edgeRe.FindAllStringSubmatch(dot, -1) {
		for _, ep := range []string{m[1], m[2]} {
			if !defined[ep] {
				dangling[ep] = true
			}
		}
	}
	out := make([]string, 0, len(dangling))
	for id := range dangling {
		out = append(out, id)
	}
	return out
}

// dotHasEdge reports whether the DOT output contains an edge between the
// two node IDs (ports ignored).
func dotHasEdge(dot, fromID, toID string) bool {
	re := regexp.MustCompile(`(?m)^\s*` + fromID + `(?::[A-Za-z0-9_]+){0,2}\s*->\s*` + toID + `(?::[A-Za-z0-9_]+){0,2}\s*\[`)
	return re.MatchString(dot)
}

// TestGraphSimplifiedNoDanglingEdges verifies that simplified graph
// generation (levels 1 and 2) never emits an edge whose endpoint is a
// hidden node. The fixture chains two plain GAMs through pass-through
// GAMDataSources and IOGAMs:
//
//	Producer → DDB1 → Bridge(IOGAM) → DDB2 → Bridge2(IOGAM) → DDB4 → Consumer
//	Producer2 → DDB3 → Consumer2   (plain pass-through, no IOGAM)
//
// Every intermediate hop is hidden by simplification, so the bypass logic
// must resolve edges transitively to displayed endpoints (Producer →
// Consumer) instead of pointing at removed nodes.
func TestGraphSimplifiedNoDanglingEdges(t *testing.T) {
	content := `
+App = {
    Class = RealTimeApplication
    +States = {
        +Run = {
            +Threads = {
                +Thread1 = {
                    Functions = { Producer Bridge Bridge2 Consumer Producer2 Consumer2 }
                }
            }
        }
    }
}
+DDB1 = {
    Class = GAMDataSource
    Signals = {
        Ping = { Type = int32 }
    }
}
+DDB2 = {
    Class = GAMDataSource
    Signals = {
        Mid = { Type = int32 }
    }
}
+DDB3 = {
    Class = GAMDataSource
    Signals = {
        Plain = { Type = int32 }
    }
}
+DDB4 = {
    Class = GAMDataSource
    Signals = {
        Pong = { Type = int32 }
    }
}
+Producer = {
    Class = ProducerGAM
    OutputSignals = {
        Ping = {
            Type = int32
            DataSource = DDB1
}
    }
}
+Bridge = {
    Class = IOGAM
    InputSignals = {
        Ping = {
            Type = int32
            DataSource = DDB1
}
    }
    OutputSignals = {
        Mid = {
            Type = int32
            DataSource = DDB2
}
    }
}
+Bridge2 = {
    Class = IOGAM
    InputSignals = {
        Mid = {
            Type = int32
            DataSource = DDB2
}
    }
    OutputSignals = {
        Pong = {
            Type = int32
            DataSource = DDB4
}
    }
}
+Consumer = {
    Class = ConsumerGAM
    InputSignals = {
        Pong = {
            Type = int32
            DataSource = DDB4
}
    }
}
+Producer2 = {
    Class = ProducerGAM
    OutputSignals = {
        Plain = {
            Type = int32
            DataSource = DDB3
}
    }
}
+Consumer2 = {
    Class = ConsumerGAM
    InputSignals = {
        Plain = {
            Type = int32
            DataSource = DDB3
}
    }
}
`
	tree := buildBypassTree(t, content)

	for _, level := range []int{1, 2} {
		t.Run(fmt.Sprintf("level%d", level), func(t *testing.T) {
			res := graph.GenerateWithOptions(tree, nil, graph.GenerateOptions{Simplified: level})

			if dangling := dotDanglingEndpoints(res.DOT); len(dangling) > 0 {
				t.Errorf("edges reference undefined nodes: %v\nDOT:\n%s", dangling, res.DOT)
			}
			if !dotHasEdge(res.DOT, "fnProducer", "fnConsumer") {
				t.Errorf("missing bypass edge Producer → Consumer through hidden IOGAM/DDB chain\nDOT:\n%s", res.DOT)
			}
			if !dotHasEdge(res.DOT, "fnProducer2", "fnConsumer2") {
				t.Errorf("missing bypass edge Producer2 → Consumer2 through plain pass-through DS\nDOT:\n%s", res.DOT)
			}
		})
	}
}
