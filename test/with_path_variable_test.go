package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/builder"
	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/validator"
)

// writeWithProject writes the given files into a temp dir and returns it.
func writeWithProject(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// validateDir parses every .marte file in dir, lowers the sugar and runs
// validation — the same pipeline as `mdt check`.
func validateDir(t *testing.T, dir string) []validator.Diagnostic {
	t.Helper()
	pt := index.NewProjectTree()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".marte") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		cfg, err := parser.NewParser(string(content)).Parse()
		if err != nil {
			t.Fatalf("parse %s: %v", e.Name(), err)
		}
		pt.AddFile(path, cfg)
	}
	pt.ResolveReferences(nil)
	pt.ResolveFields(nil)
	if _, err := builder.Lower(pt); err != nil {
		t.Fatalf("lower: %v", err)
	}
	v := validator.NewValidator(pt, dir, nil)
	v.ValidateProject(context.Background())
	return v.Diagnostics
}

// TestWithPathVariableFromAnotherFile: the path may live in a variable
// declared by a different file; it must resolve before the document loads.
func TestWithPathVariableFromAnotherFile(t *testing.T) {
	dir := writeWithProject(t, map[string]string{
		"signals.json": `{"signals":[{"name":"AI0"},{"name":"AI1"}]}`,
		"vars.marte":   "#package T\nvar signal_config_file: string = \"signals.json\"\n",
		"app.marte": `#package T
+App = {
  Class = ReferenceContainer
  with json(@signal_config_file) as cfg begin
    N = @cfg.signals.length()
  end
}
`,
	})
	for _, d := range validateDir(t, dir) {
		if strings.Contains(d.Message, "with json") || strings.Contains(d.Message, "signal_config_file") {
			t.Errorf("path variable not resolved: %s", d.Message)
		}
	}
}

// TestWithPathVariableInsideForeach: a with-block inside a loop is visited
// once per iteration and once at node level; only the iteration has the loop
// variable bound and must not produce a "taken as string" failure.
func TestWithPathVariableInsideForeach(t *testing.T) {
	dir := writeWithProject(t, map[string]string{
		"signals.json": `{"signals":[{"name":"AI0"},{"name":"AI1"}]}`,
		"app.marte": `#package T
var files: [string] = { "signals.json" }
+App = {
  Class = ReferenceContainer
  foreach f in @files
    with json(@f) as cfg begin
      N = @cfg.signals.length()
    end
  end
}
`,
	})
	diags := validateDir(t, dir)
	for _, d := range diags {
		if strings.Contains(d.Message, "with json") || strings.Contains(d.Message, "no such file") {
			t.Errorf("loop-scoped path not resolved: %s", d.Message)
		}
	}
	if len(diags) != 0 {
		t.Errorf("unexpected diagnostics: %+v", diags)
	}
}

// TestWithPathVariableUnbound: an unbound path variable still fails, with a
// message that points at the variable instead of a literal file name.
func TestWithPathVariableUnbound(t *testing.T) {
	dir := writeWithProject(t, map[string]string{
		"app.marte": `#package T
+App = {
  Class = ReferenceContainer
  with json(@missing_config) as cfg begin
    N = 1
  end
}
`,
	})
	found := false
	for _, d := range validateDir(t, dir) {
		if d.Level == validator.LevelError && strings.Contains(d.Message, "never bound") {
			found = true
		}
	}
	if !found {
		t.Fatal("unbound with path not reported")
	}
}

// TestWithPathLiteralMissingFile keeps the existing missing-file diagnostic
// for literal paths.
func TestWithPathLiteralMissingFile(t *testing.T) {
	dir := writeWithProject(t, map[string]string{
		"app.marte": `#package T
+App = {
  Class = ReferenceContainer
  with json("nope.json") as cfg begin
    N = 1
  end
}
`,
	})
	found := false
	for _, d := range validateDir(t, dir) {
		if strings.Contains(d.Message, "no such file") {
			found = true
		}
	}
	if !found {
		t.Fatal("missing literal document not reported")
	}
}
