package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/test/e2e/framework"
)

// withVarSrc uses a variable as the with-document path, for both formats.
const withVarSrcTemplate = `#package T

#var data_file: str = "@@DATA@@"

+App = {
  Class = ReferenceContainer
  with json(@data_file) as cfg begin
  #end
}
`

func writeWithVarProject(t *testing.T, ctx *framework.TestContext, dataFile string) {
	t.Helper()
	ctx.CreateFile("config.marte", strings.Replace(withVarSrcTemplate, "@@DATA@@", dataFile, 1))
}

// TestWithVariablePathJSON checks `with json(@var)`: the document is loaded
// from the path the variable holds.
func TestWithVariablePathJSON(t *testing.T) {
	ctx := framework.NewTestContext(t)
	defer ctx.Cleanup()

	ctx.CreateSubdir("data")
	if err := os.WriteFile(filepath.Join(ctx.RootDir(), "data", "cfg.json"),
		[]byte(`{"signals":{"Stat":{"Type":"uint32"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	writeWithVarProject(t, ctx, "data/cfg.json")

	result := ctx.RunCheck("-P", ".")
	for _, d := range result.Diagnostics {
		if strings.Contains(d.Message, "with json") {
			t.Errorf("unexpected with diagnostic: %s", d.Message)
		}
	}
}

// TestWithVariablePathCSV checks `with csv(@var)`.
func TestWithVariablePathCSV(t *testing.T) {
	ctx := framework.NewTestContext(t)
	defer ctx.Cleanup()

	ctx.CreateSubdir("data")
	if err := os.WriteFile(filepath.Join(ctx.RootDir(), "data", "rows.csv"), []byte("idx,label\n1,alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx.CreateFile("config.marte", `#package T

#var csv_file: str = "data/rows.csv"

+App = {
  Class = ReferenceContainer
  #with csv(@csv_file) as rows begin
  #end
}
`)

	result := ctx.RunCheck("-P", ".")
	for _, d := range result.Diagnostics {
		if strings.Contains(d.Message, "with csv") {
			t.Errorf("unexpected with diagnostic: %s", d.Message)
		}
	}
}

// TestMissingWithFileCheckWarns pins the check contract: a missing document is
// a warning (exit code stays clean), not an error.
func TestMissingWithFileCheckWarns(t *testing.T) {
	ctx := framework.NewTestContext(t)
	defer ctx.Cleanup()
	writeWithVarProject(t, ctx, "data/missing.json")

	result := ctx.RunCheck("-P", ".")
	sawWarning, sawError := false, false
	for _, d := range result.Diagnostics {
		if !strings.Contains(d.Message, "with json") {
			continue
		}
		if d.Severity == "warning" {
			sawWarning = true
		}
		if d.Severity == "error" {
			sawError = true
		}
	}
	if !sawWarning {
		t.Errorf("expected a warning for the missing document, got %v", result.Diagnostics)
	}
	if sawError {
		t.Error("missing document reported as error in check")
	}
}

// TestMissingWithFileBuildFails pins the build contract: a missing document
// fails the build.
func TestMissingWithFileBuildFails(t *testing.T) {
	ctx := framework.NewTestContext(t)
	defer ctx.Cleanup()
	writeWithVarProject(t, ctx, "data/missing.json")

	result := ctx.RunBuild("-o", filepath.Join(ctx.RootDir(), "out.marte"), "config.marte")
	if result.ExitCode == 0 {
		t.Error("build succeeded despite a missing with-document")
	}
	failed := result.ExitCode != 0 &&
		(strings.Contains(result.Stderr, "failed due to validation") ||
			strings.Contains(result.Output, "failed due to validation"))
	if !failed {
		t.Fatalf("expected a build failure, got exit=%d output=%q stderr=%q", result.ExitCode, result.Output, result.Stderr)
	}
	combined := result.Output + result.Stderr
	if !strings.Contains(combined, "with json(") || !strings.Contains(combined, "no such file or directory") {
		t.Errorf("build failure does not name the missing document, got %q", combined)
	}
}
