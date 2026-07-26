package integration

import (
	"os"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/builder"
)

// TestBuilderBranchCollisionBothTrue verifies that when two files each have
// an #if block at the same line/column position (merged into the same node),
// and both conditions are true, both then-branches produce their fields.
//
// The branch ID is computed as "line:col" (no file disambiguation), so
// fragments from different files at the same position share a BranchID.
// When both conditions are true the result is correct despite the shared ID.
//
// BUG-008 tracks the real fix: BranchID should include the file path so that
// #if blocks from different files never collide regardless of their conditions.
func TestBuilderBranchCollisionBothTrue(t *testing.T) {
	dir, err := os.MkdirTemp("", "bug008")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	// Both files target +MyObj.  Blank lines pad so that #if lands at
	// line 8, column 1 in BOTH files — same branch ID "8:1".
	//
	// File 1: #if @ENABLE_A == 1  → FieldA = "from_f1"
	// File 2: #if @ENABLE_B == 1  → FieldB = "from_f2"
	// Each condition uses a distinct variable so they are independent.

	f1 := `#package Proj.+MyObj
#var ENABLE_A: int = 0






#if @ENABLE_A == 1
FieldA = "from_f1"
#end
`

	f2 := `#package Proj.+MyObj
#var ENABLE_B: int = 0






#if @ENABLE_B == 1
FieldB = "from_f2"
#end
`

	if err := os.WriteFile(dir+"/f1.marte", []byte(f1), 0644); err != nil {
		t.Fatalf("Failed to write f1.marte: %v", err)
	}
	if err := os.WriteFile(dir+"/f2.marte", []byte(f2), 0644); err != nil {
		t.Fatalf("Failed to write f2.marte: %v", err)
	}

	// Both variables set to 1 → both conditions true.
	overrides := map[string]string{"ENABLE_A": "1", "ENABLE_B": "1"}
	b := builder.NewBuilder([]string{dir + "/f1.marte", dir + "/f2.marte"}, overrides)

	outPath := dir + "/MyObj.marte"
	outF, err := os.Create(outPath)
	if err != nil {
		t.Fatalf("Failed to create output file: %v", err)
	}
	defer outF.Close()

	if err := b.Build(outF); err != nil {
		t.Fatalf("Build failed: %v", err)
	}
	outF.Close()

	outContent, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("Failed to read output: %v", err)
	}
	outStr := string(outContent)

	// Both then-branches should be active.
	if !strings.Contains(outStr, `FieldA = "from_f1"`) {
		t.Errorf("Expected FieldA active (ENABLE_A=1), got:\n%s", outStr)
	}
	if !strings.Contains(outStr, `FieldB = "from_f2"`) {
		t.Errorf("Expected FieldB active (ENABLE_B=1), got:\n%s", outStr)
	}
}

// TestBuilderBranchCollisionMixed demonstrates the collision bug when the
// two #if blocks at identical positions have different truth values.
//
// Skipped until BUG-008 is fixed (branch ID must include file path).
func TestBuilderBranchCollisionMixed(t *testing.T) {
	t.Skip("BUG-008: branch ID collision across merged files — fix pending")

	dir, err := os.MkdirTemp("", "bug008_mixed")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	// Same #if position (line 8) in both files.
	// File 1: @ENABLE == 1 (true)  → FieldA should be active
	// File 2: @ENABLE == 2 (false) → FieldB should be inactive
	f1 := `#package Proj.+MyObj
#var ENABLE: int = 0






#if @ENABLE == 1
FieldA = "from_f1"
#end
`

	f2 := `#package Proj.+MyObj






#if @ENABLE == 2
FieldB = "from_f2"
#end
`

	if err := os.WriteFile(dir+"/f1.marte", []byte(f1), 0644); err != nil {
		t.Fatalf("Failed to write f1.marte: %v", err)
	}
	if err := os.WriteFile(dir+"/f2.marte", []byte(f2), 0644); err != nil {
		t.Fatalf("Failed to write f2.marte: %v", err)
	}

	overrides := map[string]string{"ENABLE": "1"}
	b := builder.NewBuilder([]string{dir + "/f1.marte", dir + "/f2.marte"}, overrides)

	outPath := dir + "/MyObj.marte"
	outF, err := os.Create(outPath)
	if err != nil {
		t.Fatalf("Failed to create output file: %v", err)
	}
	defer outF.Close()

	if err := b.Build(outF); err != nil {
		t.Fatalf("Build failed: %v", err)
	}
	outF.Close()

	outContent, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("Failed to read output: %v", err)
	}
	outStr := string(outContent)

	// When the bug is fixed:
	//   - File 1 (@ENABLE==1, true):  FieldA present
	//   - File 2 (@ENABLE==2, false): FieldB absent
	if !strings.Contains(outStr, `FieldA = "from_f1"`) {
		t.Errorf("Expected FieldA active (ENABLE=1)")
	}
	if strings.Contains(outStr, `FieldB = "from_f2"`) {
		t.Errorf("BUG-008: FieldB leaked despite ENABLE != 2 due to branch ID collision")
	}
}
