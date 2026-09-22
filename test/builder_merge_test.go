package integration

import (
	"os"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/builder"
)

func TestBuilderMergeNodes(t *testing.T) {
	// Two files without package, defining SAME root node +App.
	// This triggers merging logic in Builder.

	content1 := `
+App = {
    Field1 = 10
    +Sub = { Val = 1 }
}
`
	content2 := `
+App = {
    Field2 = 20
    +Sub = { Val2 = 2 }
}
`
	f1, _ := os.CreateTemp("", "merge1.marte")
	f1.WriteString(content1)
	f1.Close()
	defer os.Remove(f1.Name())

	f2, _ := os.CreateTemp("", "merge2.marte")
	f2.WriteString(content2)
	f2.Close()
	defer os.Remove(f2.Name())

	b := builder.NewBuilder([]string{f1.Name(), f2.Name()}, nil)

	outF, _ := os.CreateTemp("", "out_merge.marte")
	defer os.Remove(outF.Name())

	err := b.Build(outF)
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}
	outF.Close()

	outContent, _ := os.ReadFile(outF.Name())
	outStr := string(outContent)

	if !strings.Contains(outStr, "Field1 = 10") {
		t.Error("Missing Field1")
	}
	if !strings.Contains(outStr, "Field2 = 20") {
		t.Error("Missing Field2")
	}
	if !strings.Contains(outStr, "+Sub = {") {
		t.Error("Missing Sub")
	}
	if !strings.Contains(outStr, "Val = 1") {
		t.Error("Missing Sub.Val")
	}
	if !strings.Contains(outStr, "Val2 = 2") {
		t.Error("Missing Sub.Val2")
	}
}

func TestBuilderIfBlockPreservesNonConditionalFields(t *testing.T) {
	// Regression test: when an #if condition is true, non-conditional
	// fragments (like Class, other fields) in the same node MUST remain
	// active. The original #if handler set ALL non-matching fragments
	// to false instead of only touching conditional branches.

	content := `
//! allow(unknown_class)
#var ENABLE: bool = true

+Config = {
    Class = "MyClass"
    BeforeIf = "before"
    #if @ENABLE
        ThenBranch = "then"
    #else
        ElseBranch = "else"
    #end
    AfterIf = "after"
}
`
	f, _ := os.CreateTemp("", "if_bug.marte")
	f.WriteString(content)
	f.Close()
	defer os.Remove(f.Name())

	b := builder.NewBuilder([]string{f.Name()}, nil)

	outF, _ := os.CreateTemp("", "out_if_bug.marte")
	defer os.Remove(outF.Name())

	err := b.Build(outF)
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}
	outF.Close()

	outContent, _ := os.ReadFile(outF.Name())
	outStr := string(outContent)

	// Non-conditional fields MUST be present
	if !strings.Contains(outStr, `Class = "MyClass"`) {
		t.Error("Missing Class (non-conditional field deactivated by #if handler)")
	}
	if !strings.Contains(outStr, `BeforeIf = "before"`) {
		t.Error("Missing BeforeIf (non-conditional field before #if deactivated)")
	}
	if !strings.Contains(outStr, `AfterIf = "after"`) {
		t.Error("Missing AfterIf (non-conditional field after #if deactivated)")
	}

	// #if then-branch MUST be present (condition is true)
	if !strings.Contains(outStr, `ThenBranch = "then"`) {
		t.Error("Missing ThenBranch (then-branch should be active)")
	}

	// #else branch MUST NOT be present
	if strings.Contains(outStr, "ElseBranch") {
		t.Error("ElseBranch present (else-branch should be inactive)")
	}
}
