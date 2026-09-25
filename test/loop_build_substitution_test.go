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

const loopSubstitutionSrc = `#package T
var Count: int = 2

template Ch(ID: int, Gain: float = 1.0)
  ("+Channel" .. @ID) = {
    Class = ReferenceContainer
    Scale = @Gain
  }
end

+Rack = {
  Class = ReferenceContainer
  #foreach i in { 1, 2 }
    #if (@i <= @Count)
      use Ch C(ID = @i, Gain = (0.5 * @i))
    #end
  #end

  #foreach idx name in { "Alpha", "Beta" }
    ("+Pair" .. @idx) = {
      Class = ReferenceContainer
      Partner = @name
    }
  #end
}
`

// Loop and template variables must be substituted in build output for
// object names *and* field values, without duplicated objects.
func TestLoopAndTemplateBuildSubstitution(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "main.marte")
	os.WriteFile(file, []byte(loopSubstitutionSrc), 0644)

	b := builder.NewBuilder([]string{file}, nil)
	out, err := os.CreateTemp("", "out*.marte")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(out.Name())
	if err := b.Build(out); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(out.Name())
	if err != nil {
		t.Fatal(err)
	}
	res := string(content)

	for _, want := range []string{
		"+Channel1 = {",
		"Scale = 0.5",
		"+Channel2 = {",
		"Scale = 1",
		`Partner = "Alpha"`,
		`Partner = "Beta"`,
	} {
		if !strings.Contains(res, want) {
			t.Errorf("expected %q in build output, got:\n%s", want, res)
		}
	}

	// No duplicates and no leftover unsubstituted names.
	for _, name := range []string{"+Channel1", "+Channel2", "+Pair0", "+Pair1"} {
		if n := strings.Count(res, name+" = {"); n != 1 {
			t.Errorf("expected exactly one %s, got %d\n%s", name, n, res)
		}
	}
	for _, leak := range []string{"@ID", "@Gain", "@i", "@name", "@idx"} {
		if strings.Contains(res, leak) {
			t.Errorf("unsubstituted %s leaked into output:\n%s", leak, res)
		}
	}
}

// The same config must also be clean when the loop file is part of a
// multi-file project with conditional fragments in sibling files (this
// used to flood "Unresolved variable reference" errors).
func TestLoopVariablesCleanInMultiFileProject(t *testing.T) {
	dir := t.TempDir()
	loopFile := filepath.Join(dir, "loops.marte")
	otherFile := filepath.Join(dir, "other.marte")
	os.WriteFile(loopFile, []byte(loopSubstitutionSrc), 0644)
	os.WriteFile(otherFile, []byte(`#package T
var Streaming: bool = true

+App = {
  Class = ReferenceContainer
  #if @Streaming
    +Live = {
      Class = ReferenceContainer
    }
  #else
    +Local = {
      Class = ReferenceContainer
    }
  #end
}
`), 0644)

	pt := index.NewProjectTree()
	for _, f := range []string{otherFile, loopFile} {
		cfg, err := parser.NewParser(readFileString(t, f)).Parse()
		if err != nil {
			t.Fatalf("parse %s: %v", f, err)
		}
		pt.AddFile(f, cfg)
	}
	pt.ResolveReferences(nil)
	v := validator.NewValidator(pt, dir, nil)
	v.ValidateProject(context.Background())

	for _, d := range v.Diagnostics {
		if strings.Contains(d.Message, "Unresolved variable reference") {
			t.Errorf("unexpected unresolved variable: %s", d.Message)
		}
		if d.Level == validator.LevelError {
			t.Errorf("unexpected error: %s", d.Message)
		}
	}

	// Build across both files: values substituted, nothing duplicated.
	b := builder.NewBuilder([]string{otherFile, loopFile}, nil)
	out, err := os.CreateTemp("", "out*.marte")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(out.Name())
	if err := b.Build(out); err != nil {
		t.Fatal(err)
	}
	content, _ := os.ReadFile(out.Name())
	res := string(content)
	for _, want := range []string{"+Channel1 = {", "Scale = 0.5", "+Channel2 = {", "Scale = 1"} {
		if !strings.Contains(res, want) {
			t.Errorf("expected %q in multi-file build output, got:\n%s", want, res)
		}
	}
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
