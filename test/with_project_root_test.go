package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/builder"
	"github.com/marte-community/marte-dev-tools/internal/loader"
	"github.com/marte-community/marte-dev-tools/internal/parser"
)

// buildFile builds a single configuration file and returns the output.
func buildFile(t *testing.T, file string) string {
	t.Helper()
	b := builder.NewBuilder([]string{file}, nil)
	out, err := os.CreateTemp("", "out*.marte")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(out.Name())
	if err := b.Build(out); err != nil {
		t.Fatalf("build: %v", err)
	}
	content, err := os.ReadFile(out.Name())
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
}

const homeApp = `#package T
+App = {
  Class = ReferenceContainer
  with json("data/signals.json") as cfg begin
    N = @cfg.signals.length()
    First = @cfg.signals.first().name
  end
}
`

// TestWithPathRelativeToProjectMarker: marte_project.json marks the project
// home, so a document at the root resolves from a nested configuration.
func TestWithPathRelativeToProjectMarker(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"marte_project.json":    `{}`,
		"data/signals.json":     `{"signals":[{"name":"ROOT"}]}`,
		"sub/app.marte":         homeApp,
		"sub/data/signals.json": `{"signals":[{"name":"SIBLING"},{"name":"X"}]}`,
	})
	out := buildFile(t, filepath.Join(dir, "sub", "app.marte"))
	for _, want := range []string{`First = "ROOT"`, "N = 1"} {
		if !strings.Contains(out, want) {
			t.Errorf("project-home document not used, missing %q:\n%s", want, out)
		}
	}
}

// TestWithPathRelativeToGitRoot: without a marker, the git root anchors the
// paths.
func TestWithPathRelativeToGitRoot(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	writeTree(t, dir, map[string]string{
		"data/signals.json": `{"signals":[{"name":"ROOT"}]}`,
		"sub/app.marte":     homeApp,
	})
	out := buildFile(t, filepath.Join(dir, "sub", "app.marte"))
	if !strings.Contains(out, `First = "ROOT"`) {
		t.Errorf("git-root document not used:\n%s", out)
	}
}

// TestWithPathFallsBackToFileDirectory: with no marker and no git root, the
// document next to the configuration keeps working.
func TestWithPathFallsBackToFileDirectory(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"sub/data/signals.json": `{"signals":[{"name":"LOCAL"}]}`,
		"sub/app.marte":         homeApp,
	})
	out := buildFile(t, filepath.Join(dir, "sub", "app.marte"))
	if !strings.Contains(out, `First = "LOCAL"`) {
		t.Errorf("file-relative document not used:\n%s", out)
	}
}

// TestWithPathAbsolute stays as written.
func TestWithPathAbsolute(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"abs/signals.json": `{"signals":[{"name":"ABS"}]}`,
	})
	app := "#package T\n+App = {\n  Class = ReferenceContainer\n  with json(\"" +
		filepath.Join(dir, "abs", "signals.json") + "\") as cfg begin\n    First = @cfg.signals.first().name\n  end\n}\n"
	writeTree(t, dir, map[string]string{"app.marte": app})
	out := buildFile(t, filepath.Join(dir, "app.marte"))
	if !strings.Contains(out, `First = "ABS"`) {
		t.Errorf("absolute document not used:\n%s", out)
	}
}

// TestWithPathAbsoluteBeatsProjectHome: an absolute path is used as written
// even when the project home is set.
func TestWithPathAbsoluteBeatsProjectHome(t *testing.T) {
	dir := t.TempDir()
	other := t.TempDir()
	writeTree(t, dir, map[string]string{"marte_project.json": `{}`})
	writeTree(t, other, map[string]string{"sig.json": `{"signals":[{"name":"ABS"}]}`})
	app := "#package T\n+App = {\n  Class = ReferenceContainer\n  with json(\"" +
		filepath.Join(other, "sig.json") + "\") as cfg begin\n    First = @cfg.signals.first().name\n  end\n}\n"
	writeTree(t, dir, map[string]string{"app.marte": app})
	out := buildFile(t, filepath.Join(dir, "app.marte"))
	if !strings.Contains(out, `First = "ABS"`) {
		t.Errorf("absolute path did not win over the project home:\n%s", out)
	}
}

// TestWithPathDotRelativeAnchorsToFile: `./` and `../` mean "from this file"
// and never resolve against the project home.
func TestWithPathDotRelativeAnchorsToFile(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	writeTree(t, dir, map[string]string{
		"marte_project.json": `{}`,
		"data/up.json":       `{"signals":[{"name":"INSIDE"}]}`,
		"sub/app.marte": `#package T
+App = {
  Class = ReferenceContainer
  with json("../data/up.json") as cfg begin
    First = @cfg.signals.first().name
  end
}
`,
	})
	// A document reachable by naively joining the home with "../data/up.json".
	writeTree(t, outside, map[string]string{"data/up.json": `{"signals":[{"name":"OUTSIDE"}]}`})

	out := buildFile(t, filepath.Join(dir, "sub", "app.marte"))
	if !strings.Contains(out, `First = "INSIDE"`) {
		t.Errorf("../ path not anchored to the file:\n%s", out)
	}
}

// TestWithPathAbsoluteForms: absolute paths work as literal, variable and
// concatenated expressions.
func TestWithPathAbsoluteForms(t *testing.T) {
	dir := t.TempDir()
	sig := filepath.Join(dir, "sig.json")
	writeTree(t, dir, map[string]string{"sig.json": `{"signals":[{"name":"ABS"}]}`})
	app := "#package T\nvar p: string = \"" + sig + "\"\n+App = {\n  Class = ReferenceContainer\n" +
		"  with json(\"" + sig + "\") as a begin\n    ViaLiteral = @a.signals.first().name\n  end\n" +
		"  with json(@p) as b begin\n    ViaVar = @b.signals.first().name\n  end\n" +
		"  with json(\"" + filepath.Dir(sig) + "/\" .. \"sig.json\") as c begin\n    ViaConcat = @c.signals.first().name\n  end\n}\n"
	writeTree(t, dir, map[string]string{"app.marte": app})
	out := buildFile(t, filepath.Join(dir, "app.marte"))
	for _, want := range []string{`ViaLiteral = "ABS"`, `ViaVar = "ABS"`, `ViaConcat = "ABS"`} {
		if !strings.Contains(out, want) {
			t.Errorf("absolute form missing %q:\n%s", want, out)
		}
	}
}

// TestResolvePathFromContract pins the loader helper's rules.
func TestResolvePathFromContract(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"marte_project.json": `{}`,
		"data/root.json":     `{}`,
		"sub/local.json":     `{}`,
	})
	base := filepath.Join(dir, "sub", "app.marte")
	abs := filepath.Join(dir, "data", "root.json")

	if got := loader.ResolvePathFrom(base, dir, abs); got != abs {
		t.Errorf("absolute path altered: %q", got)
	}
	if got, want := loader.ResolvePathFrom(base, dir, "data/root.json"), abs; got != want {
		t.Errorf("project-home path = %q, want %q", got, want)
	}
	if got, want := loader.ResolvePathFrom(base, dir, "./local.json"), filepath.Join(dir, "sub", "local.json"); got != want {
		t.Errorf("dot-relative path = %q, want %q", got, want)
	}
	// Missing file, no dot prefix: the project-home form is reported.
	if got, want := loader.ResolvePathFrom(base, dir, "missing.json"), filepath.Join(dir, "missing.json"); got != want {
		t.Errorf("missing path = %q, want %q", got, want)
	}
}

// TestWithPathProjectHomeDiscovery covers the loader helper directly.
func TestWithPathProjectHomeDiscovery(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sub", "deep"), 0755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "marte_project.json")
	if err := os.WriteFile(marker, []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "sub", "deep", "app.marte")
	if err := os.WriteFile(file, []byte("#package T\n"), 0644); err != nil {
		t.Fatal(err)
	}

	if got := loader.ProjectHome(file, ""); got != dir {
		t.Errorf("project home = %q, want %q", got, dir)
	}
	// The marker wins over an explicit fallback.
	if got := loader.ProjectHome(file, filepath.Join(dir, "other")); got != dir {
		t.Errorf("project home = %q, want marker dir %q", got, dir)
	}
}

// TestWithPathParser sanity: the fixture parses (keeps the test independent
// of build failures elsewhere).
func TestWithPathParser(t *testing.T) {
	if _, err := parser.NewParser(homeApp).Parse(); err != nil {
		t.Fatalf("parse: %v", err)
	}
}
