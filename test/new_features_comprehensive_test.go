package integration

import (
	"context"
	"testing"
	"strings"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/validator"
)

func TestNewFeaturesComprehensive(t *testing.T) {
	schemaContent := `
package schema

#Classes: {
	RootClass: {
		#meta: MetaType: "root"
		Children?: {...}
	}
	ChildClass: {
		#meta: {
			MetaType: "child"
			Parent: {
				Class: "RootClass"
			}
		}
		Value: int
	}
	NamedChild: {
		#meta: {
			Parent: {
				Name: "SpecialRoot"
			}
		}
	}
}
`
	content := `
	#package test
	+ValidRoot = {
		Class = RootClass
		+ValidChild = {
			Class = ChildClass
			Value = -100 + 50 // Should be -50
		}
	}

	+InvalidRoot = {
		Class = "Configuration"
		+OrphanChild = {
			Class = ChildClass
			Value = -35
		}
	}

	+SpecialRoot = {
		Class = RootClass
		+NamedChildInstance = {
			Class = NamedChild
		}
	}

	+NormalRoot = {
		Class = RootClass
		+WronglyNamedChild = {
			Class = NamedChild
		}
	}
	`

	p := parser.NewParser(content)
	cfg, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	tree := index.NewProjectTree()
	tree.AddFile("test.marte", cfg)
	tree.ResolveFields()

	v := validator.NewValidator(tree, "", nil)
	// Inject custom schema
	ctx := v.Schema.Context
	v.Schema.Value = v.Schema.Value.Unify(ctx.CompileString(schemaContent))

	v.ValidateProject(context.Background())

	// We expect 2 errors: 
	// 1. OrphanChild parent class mismatch (Parent should be RootClass, is Configuration)
	// 2. WronglyNamedChild parent name mismatch (Parent should be SpecialRoot, is NormalRoot)

	errorMsgs := []string{}
	for _, diag := range v.Diagnostics {
		if diag.Level == validator.LevelError {
			errorMsgs = append(errorMsgs, diag.Message)
			t.Logf("Found Error: %s", diag.Message)
		}
	}

	foundOrphanErr := false
	foundNameErr := false
	for _, msg := range errorMsgs {
		if strings.Contains(msg, "OrphanChild") && strings.Contains(msg, "Parent Class Mismatch") {
			foundOrphanErr = true
		}
		if strings.Contains(msg, "WronglyNamedChild") && strings.Contains(msg, "Parent Name Mismatch") {
			foundNameErr = true
		}
	}

	if !foundOrphanErr {
		t.Errorf("Expected Parent Class Mismatch error for OrphanChild")
	}
	if !foundNameErr {
		t.Errorf("Expected Parent Name Mismatch error for WronglyNamedChild")
	}

	// Verify negative number evaluation
	testNode := tree.Root.Children["test"]
	validRoot := testNode.Children["ValidRoot"]
	validChild := validRoot.Children["ValidChild"]
	val := v.ValueToInterface(validChild.Fields["Value"][0].Value, validChild)
	if i, ok := val.(int64); ok {
		if i != -50 {
			t.Errorf("Expected Value = -50, got %d", i)
		}
	} else {
		t.Errorf("Expected int64 for Value, got %T", val)
	}

	// --- Test Suppression ---
	t.Run("Suppression", func(t *testing.T) {
		contentSuppressed := `
		#package test.suppressed
		+InvalidRoot = {
			Class = "Configuration"
			//! ignore(parent_mismatch)
			+OrphanChild = {
				Class = ChildClass
				Value = -35
			}
		}
		`
		p2 := parser.NewParser(contentSuppressed)
		cfg2, _ := p2.Parse()
		tree.AddFile("suppressed.marte", cfg2)
		tree.ResolveFields()
		
		v.Diagnostics = nil
		v.ValidateProject(context.Background())

		for _, diag := range v.Diagnostics {
			if diag.Level == validator.LevelError && strings.Contains(diag.Message, "OrphanChild") && strings.Contains(diag.Message, "suppressed") {
				t.Errorf("Error was not suppressed by pragma: %s", diag.Message)
			}
		}
	})
}
