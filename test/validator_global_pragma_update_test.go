package integration

import (
	"strings"
	"testing"

	"github.com/marte-dev/marte-dev-tools/internal/index"
	"github.com/marte-dev/marte-dev-tools/internal/parser"
	"github.com/marte-dev/marte-dev-tools/internal/validator"
)

func TestGlobalPragmaUpdate(t *testing.T) {
	// Scenario: Project scope. File A has pragma. File B has warning.

	fileA := "fileA.marte"
	contentA_WithPragma := `
#package my.project
//!allow(unused): Suppress
`
	contentA_NoPragma := `
#package my.project
// No pragma
`

	fileB := "fileB.marte"
	contentB := `
#package my.project
+Data={Class=ReferenceContainer +DS={Class=FileReader Filename="t" Signals={Unused={Type=uint32}}}}
`

	idx := index.NewProjectTree()

	// Helper to validate
	check := func() bool {
		idx.ResolveReferences()
		v := validator.NewValidator(idx, ".")
		v.ValidateProject()
		v.CheckUnused()
		for _, d := range v.Diagnostics {
			if strings.Contains(d.Message, "Unused Signal") {
				return true // Found warning
			}
		}
		return false
	}

	// 1. Add A (with pragma) and B
	pA := parser.NewParser(contentA_WithPragma)
	cA, _ := pA.Parse()
	idx.AddFile(fileA, cA)

	pB := parser.NewParser(contentB)
	cB, _ := pB.Parse()
	idx.AddFile(fileB, cB)

	if check() {
		t.Error("Step 1: Expected warning to be suppressed")
	}

	// 2. Update A (remove pragma)
	pA2 := parser.NewParser(contentA_NoPragma)
	cA2, _ := pA2.Parse()
	idx.AddFile(fileA, cA2)

	if !check() {
		t.Error("Step 2: Expected warning to appear")
	}

	// 3. Update A (add pragma back)
	idx.AddFile(fileA, cA) // Re-use config A

	if check() {
		t.Error("Step 3: Expected warning to be suppressed again")
	}
}
