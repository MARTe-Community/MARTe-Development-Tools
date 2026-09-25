package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/builder"
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
