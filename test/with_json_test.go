package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/builder"
	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/loader"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/validator"
	"github.com/marte-community/marte-dev-tools/internal/varsfile"
)

func TestVarsFileLoadJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vars.json")
	os.WriteFile(path, []byte(`{
		"udp_streamer": false,
		"NumChannels": 8,
		"SamplingFrequency": 1.0e6,
		"HostName": "localhost",
		"Masks": [1, 2]
	}`), 0644)

	vars, err := varsfile.LoadJSON(path)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"udp_streamer":      "false",
		"NumChannels":       "8",
		"SamplingFrequency": "1000000",
		"HostName":          `"localhost"`,
		"Masks":             "{ 1, 2 }",
	} {
		if got := vars[name]; got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

func TestVarsFileRejectsNestedObjects(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vars.json")
	os.WriteFile(path, []byte(`{"good": 1, "nested": {"a": 2}}`), 0644)
	if _, err := varsfile.LoadJSON(path); err == nil {
		t.Error("expected error for nested object value")
	}
}

// End-to-end: `with json(...) as name` + foreach over a JSON dict.
func TestWithJSONBlock(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "epics.json"), []byte(`{
		"signals": {
			"Stat":  { "Type": "uint32", "PVName": "TEST:STAT" },
			"Temp":  { "Type": "float64", "PVName": "TEST:TEMP" }
		}
	}`), 0644)

	src := `#package T
with json("epics.json") as epics_cfg begin
  +Object = {
    Class = ReferenceContainer
    Signals = {
      foreach name, object in @epics_cfg.signals do
        @name = {
          Class = ReferenceContainer
          Type = @object.Type
          PVName = @object.PVName
        }
      end
    }
  }
end`
	os.WriteFile(filepath.Join(dir, "main.marte"), []byte(src), 0644)

	res := buildTempIn(t, dir)
	for _, want := range []string{
		`Stat = {`,
		`Type = "uint32"`,
		`PVName = "TEST:STAT"`,
		`Temp = {`,
		`Type = "float64"`,
		`PVName = "TEST:TEMP"`,
	} {
		if !strings.Contains(res, want) {
			t.Errorf("expected %q in output, got:\n%s", want, res)
		}
	}
}

// buildTempIn builds src with the working directory set to dir, so that
// relative `with json(...)` paths resolve like real projects.
func buildTempIn(t *testing.T, dir string) string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.marte"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no marte files in %s", dir)
	}
	b := builder.NewBuilder(files, nil)
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
	return string(content)
}

func TestWithMissingFileReportsError(t *testing.T) {
	src := `#package T
with json("missing.json") as cfg begin
  +O = {
    Class = ReferenceContainer
    X = @cfg.any
  }
end`
	diags := validateTemp(t, src)
	found := false
	for _, d := range diags {
		if d.Level == validator.LevelError && strings.Contains(d.Message, "with json") {
			found = true
		}
	}
	if !found {
		t.Error("expected with-load error diagnostic for missing file")
	}
}

func TestLoaderJSONShapes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.json")
	os.WriteFile(path, []byte(`{
		"name": "demo",
		"count": 42,
		"rate": 1.5,
		"on": true,
		"tags": [1, 2],
		"nested": { "host": "h1" }
	}`), 0644)

	v, err := loader.Load("json", path)
	if err != nil {
		t.Fatal(err)
	}
	m, ok := v.(*parser.MapValue)
	if !ok {
		t.Fatalf("expected MapValue, got %T", v)
	}
	if got := len(m.Keys); got != 6 {
		t.Errorf("expected 6 keys (null omitted), got %d", got)
	}
	if n, _ := m.Lookup("count"); n.(*parser.IntValue).Value != 42 {
		t.Error("count should be IntValue 42")
	}
	nested, ok := m.Lookup("nested")
	if !ok {
		t.Fatal("nested missing")
	}
	nm := nested.(*parser.MapValue)
	if h, _ := nm.Lookup("host"); h.(*parser.StringValue).Value != "h1" {
		t.Error("nested.host should be h1")
	}
}

func TestLoaderCSV(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.csv")
	os.WriteFile(path, []byte("Name,Type\nA,uint32\nB,float64\n"), 0644)

	v, err := loader.Load("csv", path)
	if err != nil {
		t.Fatal(err)
	}
	arr, ok := v.(*parser.ArrayValue)
	if !ok {
		t.Fatalf("expected ArrayValue, got %T", v)
	}
	if len(arr.Elements) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(arr.Elements))
	}
	row := arr.Elements[0].(*parser.MapValue)
	if namev, _ := row.Lookup("Name"); namev.(*parser.StringValue).Value != "A" {
		t.Errorf("row Name = %v", namev)
	}
}

func TestLoaderUnknownFormat(t *testing.T) {
	if _, err := loader.Load("yaml", "x.yaml"); err == nil {
		t.Error("expected error for unknown format")
	}
}

// Guards the shipped example: the JSON-driven signals must materialize
// with their type and PVName values in build output.
// An unprefixed GAM (`GAM = { Class = IOGAM … }`) must be schema-validated
// like a prefixed one: mismatched input/output signal sizes are an error.
func TestIOGAMSizeMismatchReportedForUnprefixedGAM(t *testing.T) {
	dir := t.TempDir()
	src := `#package T
+DDB = {
  Class = GAMDataSource
  Signals = {
    A = { Type = uint32 }
    B = { Type = float64 }
  }
}
+Ref = {
  Class = ReferenceContainer
  DefaultDataSource = DDB
}
GAM = {
  Class = IOGAM
  InputSignals = {
    DDB::A
    DDB::B
  }
  OutputSignals = {
    DDB::A
  }
}
`
	file := filepath.Join(dir, "main.marte")
	os.WriteFile(file, []byte(src), 0644)

	tree := index.NewProjectTree()
	cfg, err := parser.NewParser(src).Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	tree.AddFile(file, cfg)
	tree.ResolveReferences(nil)
	v := validator.NewValidator(tree, dir, nil)
	v.ValidateProject(context.Background())

	found := false
	for _, d := range v.Diagnostics {
		if d.Level == validator.LevelError && strings.Contains(d.Message, "InputSize") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an InputSize mismatch error, got %d diagnostics", len(v.Diagnostics))
	}
}

// The JSON-driven signals must appear in *build output* when the config
// is valid; built in a temp project so the check passes.
func TestJSONSignalsInBuildOutput(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "epics.json"), []byte(`{
		"signals": { "Stat": { "Type": "uint32", "PVName": "DEMO:STAT" } }
	}`), 0644)
	src := `#package T
+DDB = {
  Class = GAMDataSource
  Signals = {
    StatOut = {
      Type = uint32
    }
  }
}
+Epics = {
  Class = ReferenceContainer
  DefaultDataSource = DDB
}
with json("epics.json") as cfg begin
  +EpicsSignals = {
    Class = EPICSCAInput
    Signals = {
      foreach name, object in @cfg.signals
        @name = {
          Type = @object.Type
          PVName = @object.PVName
        }
      #end
    }
  }
end
`
	os.WriteFile(filepath.Join(dir, "main.marte"), []byte(src), 0644)
	res := buildTempIn(t, dir)
	for _, want := range []string{
		"+EpicsSignals = {",
		`Type = "uint32"`,
		`PVName = "DEMO:STAT"`,
	} {
		if !strings.Contains(res, want) {
			t.Errorf("expected %q in build output, got:\n%s", want, res)
		}
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// `with csv(...)` yields a list of row dicts; iterating it with a
// single-variable foreach binds each row, and member access reads cells.
func TestWithCSVBlock(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "signals.csv"),
		[]byte("Name,Type\nA,uint32\nB,float64\n"), 0644)

	src := `#package T
with csv("signals.csv") as rows begin
  +DDB = {
    Class = GAMDataSource
    Signals = {
      foreach row in @rows
        @row.Name = {
          Type = @row.Type
        }
      #end
    }
  }
end
`
	os.WriteFile(filepath.Join(dir, "main.marte"), []byte(src), 0644)

	res := buildTempInCWD(t, dir)
	for _, want := range []string{
		"A = {",
		`Type = "uint32"`,
		"B = {",
		`Type = "float64"`,
	} {
		if !strings.Contains(res, want) {
			t.Errorf("expected %q in CSV build output, got:\n%s", want, res)
		}
	}
}

// buildTempInCWD is buildTempIn but the config is written by the caller.
func buildTempInCWD(t *testing.T, dir string) string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.marte"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no marte files in %s", dir)
	}
	b := builder.NewBuilder(files, nil)
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
	return string(content)
}

// Malformed `with` blocks must be reported as parse errors.
func TestWithBlockParseErrors(t *testing.T) {
	cases := map[string]string{
		"missing paren": `with json "x.json" as c begin end`,
		"missing as":    `with json("x.json") c begin end`,
		"missing name":  `with json("x.json") as begin end`,
		"missing end":   `with json("x.json") as c begin`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parser.NewParser("#package T\n" + src).Parse(); err == nil {
				t.Errorf("expected parse error for: %s", src)
			}
		})
	}
}

// JSON variable files: null values and bad shapes are rejected.
func TestVarsFileRejectsNullAndShapes(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"null.json":     `{"x": null}`,
		"arrayobj.json": `{"x": [{"a": 1}]}`,
		"bad.json":      `{`,
	} {
		path := filepath.Join(dir, name)
		os.WriteFile(path, []byte(body), 0644)
		if _, err := varsfile.LoadJSON(path); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

// Arrays keep element order and literals; absolute loader paths are used
// unchanged.
func TestVarsFileArrayAndLoaderPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vars.json")
	os.WriteFile(path, []byte(`{"Masks": [1, 2, "three", true]}`), 0644)
	vars, err := varsfile.LoadJSON(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := vars["Masks"]; got != `{ 1, 2, "three", true }` {
		t.Errorf("Masks = %q", got)
	}

	abs := filepath.Join(dir, "data.json")
	if got := loader.ResolvePath(filepath.Join(dir, "other.marte"), abs); got != abs {
		t.Errorf("absolute path should be unchanged, got %q", got)
	}
	if got := loader.ResolvePath(filepath.Join(dir, "other.marte"), "data.json"); got != abs {
		t.Errorf("relative path should resolve against the file dir, got %q", got)
	}
}

// Typed constants with conditional elements resolve then/elseif/else
// branches through the schema type check.
func TestTypedLetConditionalBranches(t *testing.T) {
	for _, tc := range []struct {
		name string
		vars string
		want string
	}{
		{"then", "var c1: bool = true\nvar c2: bool = false", "Refs = { GA GB }"},
		{"elseif", "var c1: bool = false\nvar c2: bool = true", "Refs = { GA GC }"},
		{"else", "var c1: bool = false\nvar c2: bool = false", "Refs = { GA GA }"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := "#package T\n" + tc.vars + `
+GA = { Class = ReferenceContainer }
+GB = { Class = ReferenceContainer }
+GC = { Class = ReferenceContainer }
#let List: [&GAM] = {
  GA,
  #if @c1
    GB,
  #elseif @c2
    GC,
  #else
    GA,
  #end
}
+O = {
  Class = ReferenceContainer
  Refs = @List
}`
			diags := validateTemp(t, src)
			for _, d := range diags {
				if d.Level == validator.LevelError {
					t.Errorf("unexpected error: %s", d.Message)
				}
			}
			res := buildTemp(t, src)
			if !strings.Contains(res, tc.want) {
				t.Errorf("expected %q in output, got:\n%s", tc.want, res)
			}
		})
	}
}
