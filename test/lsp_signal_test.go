package integration

import (
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/validator"
)

func TestLSPSignalReferences(t *testing.T) {
	content := `
+Data = {
    Class = ReferenceContainer
    +MyDS = {
        Class = FileReader
        Filename = "test"
        Signals = {
            MySig = { Type = uint32 }
        }
    }
}

+MyGAM = {
    Class = IOGAM
    InputSignals = {
        MySig = {
            DataSource = MyDS
            Type = uint32
        }
    }
}
`
	p := parser.NewParser(content)
	config, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	idx := index.NewProjectTree()
	idx.AddFile("signal_refs.marte", config)
	idx.ResolveReferences()

	v := validator.NewValidator(idx, ".", nil)
	v.ValidateProject()

	// Find definition of MySig in MyDS
	root := idx.IsolatedFiles["signal_refs.marte"]
	if root == nil {
		t.Fatal("Root node not found (isolated)")
	}

	// Traverse to MySig
	dataNode := root.Children["Data"]
	if dataNode == nil {
		t.Fatal("Data node not found")
	}

	myDS := dataNode.Children["MyDS"]
	if myDS == nil {
		t.Fatal("MyDS node not found")
	}

	signals := myDS.Children["Signals"]
	if signals == nil {
		t.Fatal("Signals node not found")
	}

	mySigDef := signals.Children["MySig"]
	if mySigDef == nil {
		t.Fatal("Definition of MySig not found in tree")
	}

	// Now simulate "Find References" on mySigDef
	foundRefs := 0
	idx.Walk(func(node *index.ProjectNode) {
		if node.Target == mySigDef {
			foundRefs++
			// Check if node is the GAM signal
			if node.RealName != "MySig" { // In GAM it is MySig
				t.Errorf("Unexpected reference node name: %s", node.RealName)
			}
			// Check parent is InputSignals -> MyGAM
			if node.Parent == nil || node.Parent.Parent == nil || node.Parent.Parent.RealName != "+MyGAM" {
				t.Errorf("Reference node not in MyGAM")
			}
		}
	})

	if foundRefs != 1 {
		t.Errorf("Expected 1 reference (Direct), found %d", foundRefs)
	}
}
