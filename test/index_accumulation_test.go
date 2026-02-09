package integration

import (
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
)

func TestIndex_FieldAccumulation_SharedPackage(t *testing.T) {
	pt := index.NewProjectTree()
	f1 := "file1.marte"
	f2 := "file2.marte"
	content1 := `#package P
	Field1 = 1`
	content2 := `#package P
	Field2 = 2`

	// 1. Add both files
	p1 := parser.NewParser(content1)
	c1, _ := p1.Parse()
	pt.AddFile(f1, c1)

	p2 := parser.NewParser(content2)
	c2, _ := p2.Parse()
	pt.AddFile(f2, c2)

	// Verify initial state
	count := 0
	pt.Walk(func(n *index.ProjectNode) {
		if n.Name == "P" {
			count = len(n.Fields["Field1"])
		}
	})
	if count != 1 {
		t.Fatalf("Expected 1, got %d", count)
	}

	// 2. Update file1 (re-add same content)
	p1 = parser.NewParser(content1)
	c1, _ = p1.Parse()
	pt.AddFile(f1, c1)

	// Verify count of Field1
	count = 0
	pt.Walk(func(n *index.ProjectNode) {
		if n.Name == "P" {
			count = len(n.Fields["Field1"])
		}
	})
	if count != 1 {
		t.Errorf("Expected 1 after update, got %d. BUG: Field accumulation in shared package!", count)
	}
}

func TestIndex_VariableAccumulation_SharedPackage(t *testing.T) {
	pt := index.NewProjectTree()
	f1 := "file1.marte"
	f2 := "file2.marte"
	content1 := `#package P
	#let Var1 : int = 1`
	content2 := `#package P
	#let Var2 : int = 2`

	// 1. Add both files
	p1 := parser.NewParser(content1)
	c1, _ := p1.Parse()
	pt.AddFile(f1, c1)

	p2 := parser.NewParser(content2)
	c2, _ := p2.Parse()
	pt.AddFile(f2, c2)

	// 2. Update file1 (change value)
	content1_v2 := `#package P
	#let Var1 : int = 100`
	p1 = parser.NewParser(content1_v2)
	c1, _ = p1.Parse()
	pt.AddFile(f1, c1)

	// Verify Var1 exists
	found := false
	pt.Walk(func(n *index.ProjectNode) {
		if n.Name == "P" {
			if _, ok := n.Variables["Var1"]; !ok {
				t.Errorf("Var1 not found!")
				return
			}
			found = true
		}
	})
	if !found {
		t.Errorf("Node P not found")
	}
}

func TestIndex_VariableRemoval(t *testing.T) {
	pt := index.NewProjectTree()
	f1 := "file1.marte"
	content1 := `#package P
	#let Var1 : int = 1`

	p1 := parser.NewParser(content1)
	c1, _ := p1.Parse()
	pt.AddFile(f1, c1)

	// Verify exists
	exists := false
	pt.Walk(func(n *index.ProjectNode) {
		if n.Name == "P" {
			if _, ok := n.Variables["Var1"]; ok {
				exists = true
			}
		}
	})
	if !exists {
		t.Fatalf("Variable should exist initially")
	}

	// Update to remove variable
	content2 := `#package P`
	p2 := parser.NewParser(content2)
	c2, _ := p2.Parse()
	pt.AddFile(f1, c2)

	// Verify GONE
	exists = false
	pt.Walk(func(n *index.ProjectNode) {
		if n.Name == "P" {
			if _, ok := n.Variables["Var1"]; ok {
				exists = true
			}
		}
	})
	if exists {
		t.Errorf("Variable should have been removed from index!")
	}
}


