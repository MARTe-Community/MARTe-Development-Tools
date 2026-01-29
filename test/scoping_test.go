package integration

import (
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
)

func TestNameScoping(t *testing.T) {
	// App1 = { A = { Data = 10 } B = { Ref = A } }
	// App2 = { C = { Data = 10 } A = { Data = 12 } D = { Ref = A } }
	
	content := `
+App1 = {
    Class = App
    +A = { Class = Node Data = 10 }
    +B = { Class = Node Ref = A }
}
+App2 = {
    Class = App
    +C = { Class = Node Data = 10 }
    +A = { Class = Node Data = 12 }
    +D = { Class = Node Ref = A }
}
`
	pt := index.NewProjectTree()
	p := parser.NewParser(content)
	cfg, err := p.Parse()
	if err != nil { t.Fatal(err) }
	pt.AddFile("main.marte", cfg)
	
	pt.ResolveReferences()
	
	// Helper to find ref target
	findRefTarget := func(refName string, containerName string) *index.ProjectNode {
		for _, ref := range pt.References {
			if ref.Name == refName {
				container := pt.GetNodeContaining(ref.File, ref.Position)
				if container != nil && container.RealName == containerName {
					return ref.Target
				}
			}
		}
		return nil
	}
	
	targetB := findRefTarget("A", "+B")
	if targetB == nil {
		t.Fatal("Could not find reference A in +B")
	}
	// Check if targetB is App1.A
	if targetB.Parent == nil || targetB.Parent.RealName != "+App1" {
		t.Errorf("App1.B.Ref resolved to wrong target: %v (Parent %v)", targetB.RealName, targetB.Parent.RealName)
	}
	
	targetD := findRefTarget("A", "+D")
	if targetD == nil {
		t.Fatal("Could not find reference A in +D")
	}
	// Check if targetD is App2.A
	if targetD.Parent == nil || targetD.Parent.RealName != "+App2" {
		t.Errorf("App2.D.Ref resolved to wrong target: %v (Parent %v)", targetD.RealName, targetD.Parent.RealName)
	}
}
