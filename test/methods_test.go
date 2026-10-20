package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"context"

	"github.com/marte-community/marte-dev-tools/internal/builder"
	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/validator"
)

const methodsSrc = `#package T

#var device_raw: str = "EC-GN-P01:AI_CH01"
#var devices: [str] = { "beta", "alpha" }
#var mapping: any = { b = 2, a = 1 }

+App = {
  Class = ReferenceContainer
  Clean = @device_raw.replace("EC-GN-P01:", "")
  Upper = @device_raw.upper()
  Count = @devices.length()
  Sorted = @devices.sort().join(",")
  First = @devices.first()
  MapKeys = @mapping.keys().join(",")
  Chained = @device_raw.trim().lower().replace("ec-gn-p01:", "")
}
`

func buildMethods(t *testing.T, overrides map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	file := filepath.Join(dir, "main.marte")
	if err := os.WriteFile(file, []byte(methodsSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	b := builder.NewBuilder([]string{file}, overrides)
	out := filepath.Join(dir, "out.marte")
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := b.Build(f); err != nil {
		t.Fatalf("build: %v", err)
	}
	content, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func TestStringMethodsInBuild(t *testing.T) {
	res := buildMethods(t, nil)
	for _, want := range []string{
		`Clean = "AI_CH01"`,
		`Upper = "EC-GN-P01:AI_CH01"`,
		`Sorted = "alpha,beta"`,
		`First = "beta"`,
		`MapKeys = "a,b"`,
		`Chained = "ai_ch01"`,
	} {
		if !strings.Contains(res, want) {
			t.Errorf("expected %q in output, got:\n%s", want, res)
		}
	}
}

func TestArrayMethodsInBuild(t *testing.T) {
	src := `#package T
#var xs: [int] = { 3, 1, 2 }
+App = {
  Class = ReferenceContainer
  Len = @xs.length()
  Sorted = @xs.sort().join(",")
  Rev = @xs.reverse().join(",")
  First = @xs.first()
  Slice = @xs.slice(1, 3).join(",")
  Contains = @xs.contains(2)
  Appended = @xs.append(9).join(",")
}
`
	dir := t.TempDir()
	file := filepath.Join(dir, "main.marte")
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	b := builder.NewBuilder([]string{file}, nil)
	out := filepath.Join(dir, "out.marte")
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := b.Build(f); err != nil {
		t.Fatalf("build: %v", err)
	}
	content, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	res := string(content)
	for _, want := range []string{
		"Len = 3",
		`Sorted = "1,2,3"`,
		`Rev = "2,1,3"`,
		`Slice = "1,2"`,
		`Appended = "3,1,2,9"`,
		"First = 3",
		"Contains = true",
	} {
		if !strings.Contains(res, want) {
			t.Errorf("expected %q in output, got:\n%s", want, res)
		}
	}
}

func TestDictMethodsInBuild(t *testing.T) {
	src := `#package T
#var m: any = { b = 2, a = 1 }
+App = {
  Class = ReferenceContainer
  Len = @m.length()
  Keys = @m.keys().join(",")
  Has = @m.contains("a")
  Not = @m.contains("zz")
}
`
	dir := t.TempDir()
	file := filepath.Join(dir, "main.marte")
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	b := builder.NewBuilder([]string{file}, nil)
	out := filepath.Join(dir, "out.marte")
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := b.Build(f); err != nil {
		t.Fatalf("build: %v", err)
	}
	content, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	res := string(content)
	for _, want := range []string{
		"Len = 2",
		`Keys = "a,b"`,
		"Has = true",
		"Not = false",
	} {
		if !strings.Contains(res, want) {
			t.Errorf("expected %q in output, got:\n%s", want, res)
		}
	}
}

// TestMethodWithWithBinding combines the motivating case: methods on members
// of a with-block document.
func TestMethodOnWithBinding(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "cfg.json"), []byte(`{"signals":[{"name":"EC-GN-P01:AI_CH01"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	src := `#package T
+App = {
  Class = ReferenceContainer
  with json("cfg.json") as cfg begin
    #foreach s in @cfg.signals
      ("+" .. @s.name.replace("EC-GN-P01:", "")) = {
        Class = ReferenceContainer
        Origin = @s.name
      }
    #end
  end
}
`
	pt_ := dir
	_ = pt_
	b := builder.NewBuilder([]string{filepath.Join(dir, "main.marte")}, nil)
	// main.marte does not exist yet — write it now that cfg.json exists.
	if err := os.WriteFile(filepath.Join(dir, "main.marte"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.marte")
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := b.Build(f); err != nil {
		t.Fatalf("build: %v", err)
	}
	content, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	res := string(content)
	if !strings.Contains(res, `+AI_CH01 = {`) {
		t.Errorf("stripped name not used for the object, got:\n%s", res)
	}
	if !strings.Contains(res, `Origin = "EC-GN-P01:AI_CH01"`) {
		t.Errorf("original name not preserved, got:\n%s", res)
	}
}

// TestMethodValidatesClean checks the validation path: methods must not
// produce diagnostics and must resolve for schema checks.
func TestMethodValidatesClean(t *testing.T) {
	cfg, err := parser.NewParser(methodsSrc).Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pt := index.NewProjectTree()
	pt.AddFile("main.marte", cfg)
	pt.ResolveReferences(nil)
	pt.ResolveFields(nil)
	v := validator.NewValidator(pt, ".", nil)
	v.ValidateProject(context.Background())
	for _, d := range v.Diagnostics {
		if d.Level == validator.LevelError {
			t.Errorf("unexpected error: %s", d.Message)
		}
	}
}

// TestUnknownMethodRendersEmpty pins the failure mode: an unknown method
// renders as an empty value instead of aborting the build.
func TestUnknownMethodRendersEmpty(t *testing.T) {
	src := `#package T
#var v: str = "x"
+App = {
  Class = ReferenceContainer
  Out = @v.frobnicate()
}
`
	dir := t.TempDir()
	file := filepath.Join(dir, "main.marte")
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	b := builder.NewBuilder([]string{file}, nil)
	out := filepath.Join(dir, "out.marte")
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := b.Build(f); err != nil {
		t.Fatalf("build: %v", err)
	}
	content, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "Out =") {
		t.Errorf("field vanished, got:\n%s", string(content))
	}
}
