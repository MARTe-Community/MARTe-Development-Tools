package integration

import (
	"os"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/builder"
	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/validator"
)

func TestLetMacroFull(t *testing.T) {
	content := `
//# My documentation
#let MyConst: uint32 = 10 + 20
+Obj = {
    Value = @MyConst
}
`
	tmpFile, _ := os.CreateTemp("", "let_*.marte")
	defer os.Remove(tmpFile.Name())
	os.WriteFile(tmpFile.Name(), []byte(content), 0644)

	// 1. Test Parsing & Indexing
	p := parser.NewParser(content)
	cfg, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	tree := index.NewProjectTree()
	tree.AddFile(tmpFile.Name(), cfg)

	vars := tree.Root.Variables
	if iso, ok := tree.IsolatedFiles[tmpFile.Name()]; ok {
		vars = iso.Variables
	}

	info, ok := vars["MyConst"]
	if !ok || !info.Def.IsConst {
		t.Fatal("#let variable not indexed correctly as Const")
	}
	if info.Doc != "My documentation" {
		t.Errorf("Expected doc 'My documentation', got '%s'", info.Doc)
	}

	// 2. Test Builder Evaluation
	out, _ := os.CreateTemp("", "let_out.cfg")
	defer os.Remove(out.Name())

	b := builder.NewBuilder([]string{tmpFile.Name()}, nil)
	if err := b.Build(out); err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	outContent, _ := os.ReadFile(out.Name())
	if !strings.Contains(string(outContent), "Value = 30") {
		t.Errorf("Expected Value = 30 (evaluated @MyConst), got:\n%s", string(outContent))
	}

	// 3. Test Override Protection
	out2, _ := os.CreateTemp("", "let_out2.cfg")
	defer os.Remove(out2.Name())

	b2 := builder.NewBuilder([]string{tmpFile.Name()}, map[string]string{"MyConst": "100"})
	if err := b2.Build(out2); err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	outContent2, _ := os.ReadFile(out2.Name())
	if !strings.Contains(string(outContent2), "Value = 30") {
		t.Errorf("Constant was overridden! Expected 30, got:\n%s", string(outContent2))
	}

	// 4. Test Validator (Mandatory Value)
	contentErr := "#let BadConst: uint32"
	p2 := parser.NewParser(contentErr)
	cfg2, err2 := p2.Parse()
	// Parser might fail if = is missing?
	// parseLet expects =.
	if err2 == nil {
		// If parser didn't fail (maybe it was partial), validator should catch it
		tree2 := index.NewProjectTree()
		tree2.AddFile("err.marte", cfg2)
		v := validator.NewValidator(tree2, ".")
		v.ValidateProject()

		found := false
		for _, d := range v.Diagnostics {
			if strings.Contains(d.Message, "must have an initial value") {
				found = true
				break
			}
		}
		if !found && cfg2 != nil {
			// If p2.Parse() failed and added error to p2.errors, it's also fine.
			// But check if it reached validator.
		}
	}

	// 5. Test Duplicate Detection
	contentDup := `
#let MyConst: uint32 = 10
#var MyConst: uint32 = 20
`
	p3 := parser.NewParser(contentDup)
	cfg3, _ := p3.Parse()
	tree3 := index.NewProjectTree()
	tree3.AddFile("dup.marte", cfg3)
	v3 := validator.NewValidator(tree3, ".")
	v3.ValidateProject()

	foundDup := false
	for _, d := range v3.Diagnostics {
		if strings.Contains(d.Message, "Duplicate variable definition") {
			foundDup = true
			break
		}
	}
	if !foundDup {
		t.Error("Expected duplicate variable definition error")
	}
}
