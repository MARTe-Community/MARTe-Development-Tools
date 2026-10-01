package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/validator"
)

const withVarSrc = `#package T

#var data_file: str = "data.json"

+App = {
  Class = ReferenceContainer
  with json(@data_file) as cfg begin
  #end
}
`

// newWithTree parses src into a tree rooted at dir (so relative with-paths
// resolve inside it) and returns the tree.
func newWithTree(t *testing.T, dir, src string) *index.ProjectTree {
	t.Helper()
	cfg, err := parser.NewParser(src).Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pt := index.NewProjectTree()
	pt.AddFile(filepath.Join(dir, "main.marte"), cfg)
	pt.ResolveReferences(nil)
	pt.ResolveFields(nil)
	return pt
}

// TestWithLoadFromVariable checks that the with-path may be a variable
// reference (`with json(@data_file)`), resolved at evaluation time.
func TestWithLoadFromVariable(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "data.json"), []byte(`{"signals":{"Stat":{"Type":"uint32"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	src := strings.Replace(withVarSrc, "with test: json(@data_file) as cfg begin\n  #end",
		"with test: json(@data_file) as cfg begin\n    +EpicsSignals = {\n      Class = ReferenceContainer\n      Stat = @cfg.signals.Stat.Type\n    }\n  #end", 1)
	pt := newWithTree(t, dir, src)

	v := validator.NewValidator(pt, dir, nil)
	v.ValidateProject(context.Background())
	for _, d := range v.Diagnostics {
		if d.Level == validator.LevelError {
			t.Errorf("unexpected error: %s", d.Message)
		}
	}
}

// TestMissingWithFileIsWarningInTolerantMode covers the editor/check contract:
// a missing document is a warning, the binding behaves like an empty document,
// and references to it do not cascade into errors.
func TestMissingWithFileIsWarningInTolerantMode(t *testing.T) {
	dir := t.TempDir()
	pt := newWithTree(t, dir, withVarSrc)

	v := validator.NewValidator(pt, dir, nil)
	v.MissingFilesAreWarnings = true
	v.ValidateProject(context.Background())

	sawWarning, sawError := false, false
	for _, d := range v.Diagnostics {
		if !strings.Contains(d.Message, "with json") {
			continue
		}
		if d.Level == validator.LevelWarning {
			sawWarning = true
		}
		if d.Level == validator.LevelError {
			sawError = true
		}
	}
	if !sawWarning {
		t.Errorf("expected a warning for the missing document, got %v", v.Diagnostics)
	}
	if sawError {
		t.Error("missing document reported as error in tolerant mode")
	}
}

// TestMissingWithFileIsErrorInStrictMode covers the build contract: a missing
// document must fail the build.
func TestMissingWithFileIsErrorInStrictMode(t *testing.T) {
	dir := t.TempDir()
	pt := newWithTree(t, dir, withVarSrc)

	v := validator.NewValidator(pt, dir, nil)
	v.ValidateProject(context.Background())

	found := false
	for _, d := range v.Diagnostics {
		if d.Level == validator.LevelError && strings.Contains(d.Message, "with json") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an error for the missing document, got %v", v.Diagnostics)
	}
}

// TestMissingWithFileFormatsDocumentPath checks that the diagnostic names the
// document that could not be opened (resolved path, not the expression).
func TestMissingWithFileFormatsDocumentPath(t *testing.T) {
	dir := t.TempDir()
	pt := newWithTree(t, dir, withVarSrc)

	v := validator.NewValidator(pt, dir, nil)
	v.ValidateProject(context.Background())

	found := false
	for _, d := range v.Diagnostics {
		if strings.Contains(d.Message, `with json("`+filepath.Join(dir, "data.json")+`")`) {
			found = true
		}
	}
	if !found {
		t.Errorf("diagnostic does not name the resolved path, got %v", v.Diagnostics)
	}
}
