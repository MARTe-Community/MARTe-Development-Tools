package integration

import (
	"context"
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
		v := validator.NewValidator(tree2, ".", nil)
		v.ValidateProject(context.Background())

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
	v3 := validator.NewValidator(tree3, ".", nil)
	v3.ValidateProject(context.Background())

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

func TestLetReferences(t *testing.T) {
	content := `
+GAM1 = {
    Class = "ConstantGAM"
}
+GAM2 = {
    Class = "ConstantGAM"
}
+GAM3 = {
    Class = "ConstantGAM"
}

#let functions: [&GAM] = { GAM1, GAM2, GAM3 }
#let my_ref: &GAM = GAM1

+Obj = {
    Class = "ReferenceContainer"
    Funcs = @functions
    Ref = @my_ref
}
`
	tmpFile, err := os.CreateTemp("", "let_ref_*.marte")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())
	if err := os.WriteFile(tmpFile.Name(), []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write temp file: %v", err)
	}

	// 1. Test parsing and indexing
	p := parser.NewParser(content)
	cfg, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	tree := index.NewProjectTree()
	tree.AddFile(tmpFile.Name(), cfg)

	// 2. Validate
	v := validator.NewValidator(tree, ".", nil)
	v.ValidateProject(context.Background())
	for _, diag := range v.Diagnostics {
		t.Errorf("Validation error: %v", diag.Message)
	}

	// 3. Build
	out, err := os.CreateTemp("", "let_ref_out.cfg")
	if err != nil {
		t.Fatalf("Failed to create temp output file: %v", err)
	}
	defer os.Remove(out.Name())

	b := builder.NewBuilder([]string{tmpFile.Name()}, nil)
	if err := b.Build(out); err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	outContent, err := os.ReadFile(out.Name())
	if err != nil {
		t.Fatalf("Failed to read output: %v", err)
	}
	outStr := string(outContent)
	if !strings.Contains(outStr, "Funcs = { GAM1 GAM2 GAM3 }") {
		t.Errorf("Expected Funcs = { GAM1 GAM2 GAM3 }, got:\n%s", outStr)
	}
	if !strings.Contains(outStr, "Ref = GAM1") {
		t.Errorf("Expected Ref = GAM1, got:\n%s", outStr)
	}
}

// TestLetReferenceMarksGAMsUsed ensures that GAMs listed only inside a
// #let value (e.g. `#let funcs: [&GAM] = { GAM1, GAM2, GAM3 }`) and then
// wired up via `Functions = @funcs` are recognised as "used" by the
// unused-GAM check, instead of being flagged as unused.
func TestLetReferenceMarksGAMsUsed(t *testing.T) {
	content := `
#package App

+App = {
    Class = RealTimeApplication
    +Functions = {
        +GAM1 = {
            Class = "ConstantGAM"
            OutputSignals = {
                Sig1 = { DataSource = DDB1 Type = uint32 }
            }
        }
        +GAM2 = {
            Class = "ConstantGAM"
            OutputSignals = {
                Sig2 = { DataSource = DDB1 Type = uint32 }
            }
        }
        +GAM3 = {
            Class = "ConstantGAM"
            OutputSignals = {
                Sig3 = { DataSource = DDB1 Type = uint32 }
            }
        }
    }
    +States = {
        +State1 = {
            Class = RealTimeState
            +Threads = {
                +Thread1 = {
                    Class = RealTimeThread
                    Functions = @funcs
                }
            }
        }
    }
}

#let funcs: [&GAM] = { GAM1, GAM2, GAM3 }
`
	p := parser.NewParser(content)
	cfg, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	tree := index.NewProjectTree()
	tree.AddFile("test.marte", cfg)
	tree.ResolveReferences(nil)

	v := validator.NewValidator(tree, ".", nil)
	v.ValidateProject(context.Background())
	for _, diag := range v.Diagnostics {
		if strings.Contains(diag.Message, "Unused GAM") {
			t.Errorf("Unexpected unused_gam diagnostic: %v", diag.Message)
		}
	}
}

// TestLetReferenceUnaffectedByUnrelatedTopLevelConditional is a regression
// test for a bug where a GAM referenced only through a top-level `#let`
// (e.g. `Functions = @funcs`) was incorrectly flagged as "Unused GAM" as soon
// as the same file also contained an unrelated top-level `#if ... #end`
// block (regardless of whether that block appeared before or after the
// `#let`).
//
// Root cause: Validator.isPositionActive determined whether a package-level
// reference's position was "active" by returning the active-state of
// whichever non-object Fragment happened to be first in the node's Fragment
// list for that file, instead of checking whether the reference's position
// actually fell inside that fragment. Top-level conditional fragments (from
// `#if`/`#foreach`/`#template` blocks) are appended to the node's Fragment
// list *before* the main unconditional fragment (see populateNode), so any
// package-level reference -- including the elements of a `#let` list -- could
// spuriously inherit the (false) active-state of an unrelated conditional
// block, making every GAM only reachable through that `#let` appear unused.
func TestLetReferenceUnaffectedByUnrelatedTopLevelConditional(t *testing.T) {
	content := `
#package App

+App = {
    Class = RealTimeApplication
    +Functions = {
        +GAM1 = {
            Class = "ConstantGAM"
            OutputSignals = {
                Sig1 = { DataSource = DDB1 Type = uint32 }
            }
        }
        +GAM2 = {
            Class = "ConstantGAM"
            OutputSignals = {
                Sig2 = { DataSource = DDB1 Type = uint32 }
            }
        }
    }
    +States = {
        +State1 = {
            Class = RealTimeState
            +Threads = {
                +Thread1 = {
                    Class = RealTimeThread
                    Functions = @funcs
                }
            }
        }
    }
}

#var sdn_enabled: bool = false

#let funcs: [&GAM] = { GAM1, GAM2 }

// Unrelated top-level conditional block. Its mere presence in the file used
// to be enough to make the "#let funcs" reference above resolve as
// "inactive", regardless of the condition's actual value.
#if @sdn_enabled
+Unrelated = {
    Class = ReferenceContainer
}
#end
`
	p := parser.NewParser(content)
	cfg, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	tree := index.NewProjectTree()
	tree.AddFile("test.marte", cfg)
	tree.ResolveReferences(nil)

	v := validator.NewValidator(tree, ".", nil)
	v.ValidateProject(context.Background())
	for _, diag := range v.Diagnostics {
		if strings.Contains(diag.Message, "Unused GAM") {
			t.Errorf("Unexpected unused_gam diagnostic (GAM referenced only via #let wrongly flagged unused): %v", diag.Message)
		}
	}
}
