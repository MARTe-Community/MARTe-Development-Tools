package integration

import (
	"os"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/builder"
)

// TestBuilderVariableOverrideValid verifies that a valid variable override
// is correctly applied in the build output.
func TestBuilderVariableOverrideValid(t *testing.T) {
	content := `
#var TIMEOUT: int = 100

+Config = {
    Class = "Test"
    Timeout = @TIMEOUT
}
`
	f, _ := os.CreateTemp("", "override_valid.marte")
	f.WriteString(content)
	f.Close()
	defer os.Remove(f.Name())

	overrides := map[string]string{"TIMEOUT": "200"}
	b := builder.NewBuilder([]string{f.Name()}, overrides)

	outF, _ := os.CreateTemp("", "out_override_valid.marte")
	defer os.Remove(outF.Name())

	err := b.Build(outF)
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}
	outF.Close()

	outContent, _ := os.ReadFile(outF.Name())
	outStr := string(outContent)

	if !strings.Contains(outStr, "Timeout = 200") {
		t.Errorf("Expected Timeout = 200 (overridden value), got:\n%s", outStr)
	}
	if strings.Contains(outStr, "Timeout = 100") {
		t.Errorf("Found default value 100, override was not applied:\n%s", outStr)
	}
}

// TestBuilderVariableOverrideParseError is a regression test for BUG-007:
// when a variable override value cannot be parsed (e.g., malformed syntax),
// the parse error is silently discarded and the malformed value may leak
// into the build output.
//
// Skipped until BUG-007 is fixed.
func TestBuilderVariableOverrideParseError(t *testing.T) {
	t.Skip("BUG-007: parse errors in variable overrides are silently discarded -- fix pending")

	content := `
#var MSG: string = "default"

+Config = {
    Class = "Test"
    Message = @MSG
}
`
	f, _ := os.CreateTemp("", "override_bad.marte")
	f.WriteString(content)
	f.Close()
	defer os.Remove(f.Name())

	// Unclosed quote — this cannot parse as a valid value
	overrides := map[string]string{"MSG": `broken"unclosed`}
	b := builder.NewBuilder([]string{f.Name()}, overrides)

	outF, _ := os.CreateTemp("", "out_override_bad.marte")
	defer os.Remove(outF.Name())

	err := b.Build(outF)
	if err != nil {
		t.Logf("Build returned error (BUG-007 may be fixed): %v", err)
	}
	outF.Close()

	outContent, _ := os.ReadFile(outF.Name())
	outStr := string(outContent)

	if strings.Contains(outStr, `"default"`) {
		t.Log("BUG-007: invalid override was silently ignored, default used")
	}
	if strings.Contains(outStr, `broken`) {
		t.Errorf("BUG-007: malformed override value appeared in output:\n%s", outStr)
	}
}
