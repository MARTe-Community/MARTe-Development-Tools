package integration

import (
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
)

// GAM/datasource classification is structural: nodes are recognised by
// their signal children, whether or not they carry a '+'/'$' prefix
// (unprefixed object definitions are common in real configurations).
func TestGAMAndDataSourceStructuralDetection(t *testing.T) {
	src := `#package T
+PrefixedGAM = {
  Class = IOGAM
  InputSignals = {
    S = {
      Class = ReferenceContainer
    }
  }
}
UnprefixedGAM = {
  Class = IOGAM
  OutputSignals = {
    S = {
      Class = ReferenceContainer
    }
  }
}
+Plain = {
  Class = ReferenceContainer
}
DS = {
  Class = GAMDataSource
  Signals = {
    Sig = {
      Type = uint32
    }
  }
}
`
	pt := index.NewProjectTree()
	cfg, err := parser.NewParser(src).Parse()
	if err != nil {
		t.Fatal(err)
	}
	pt.AddFile("main.marte", cfg)

	root := pt.Root.Children["T"]
	if root == nil {
		t.Fatal("package node not found")
	}
	cases := []struct {
		name  string
		isGAM bool
		isDS  bool
	}{
		{"PrefixedGAM", true, false},
		{"UnprefixedGAM", true, false},
		{"Plain", false, false},
		{"DS", false, true},
	}
	for _, c := range cases {
		node, ok := root.Children[c.name]
		if !ok {
			t.Fatalf("node %s not found", c.name)
		}
		if got := pt.IsGAM(node); got != c.isGAM {
			t.Errorf("IsGAM(%s) = %v, want %v", c.name, got, c.isGAM)
		}
		if got := pt.IsDataSource(node); got != c.isDS {
			t.Errorf("IsDataSource(%s) = %v, want %v", c.name, got, c.isDS)
		}
	}
}
