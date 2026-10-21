package integration

import (
	"fmt"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
)

func dump(n *index.ProjectNode, depth int) {
	pad := ""
	for i := 0; i < depth; i++ {
		pad += "  "
	}
	cls := ""
	for _, f := range n.Fragments {
		for _, d := range f.Definitions {
			if fld, ok := d.(*parser.Field); ok && fld.Name == "Class" {
				cls = fmt.Sprintf("%#v", fld.Value)
			}
			fmt.Printf("%s  def %T\n", pad, d)
		}
	}
	fmt.Printf("%s(n=%q real=%q cls=%s) children=%d\n", pad, n.Name, n.RealName, cls, len(n.Children))
	for _, c := range n.Children {
		dump(c, depth+1)
	}
}

func TestZZ5(t *testing.T) {
	src := `#package Demo

+App = {
  Class = RealTimeApplication
  +Functions = {
    Class = ReferenceContainer
    +G1 = {
      Class = IOGAM
      InputSignals = {
        A = { DataSource = DDB  Type = uint32 }
      }
      OutputSignals = {
        B = { DataSource = DDB  Type = uint32 }
      }
    }
  }
  +Data = {
    Class = ReferenceContainer
    +DDB = {
      Class = GAMDataSource
      Signals = {
        A = { Type = uint32 }
        B = { Type = uint32 }
      }
    }
  }
  +States = {
    Class = ReferenceContainer
    +S = {
      Class = RealTimeState
      +Th = {
        Class = RealTimeThread
        Functions = { G1 }
      }
    }
  }
}
`
	cfg, err := parser.NewParser(src).Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	tree := index.NewProjectTree()
	tree.AddFile("x.marte", cfg)
	dump(tree.Root, 0)
}
