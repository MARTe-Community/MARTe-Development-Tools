package integration

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/loader"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/validator"
)

// writeXLSX creates a minimal (Office Open XML) workbook with one worksheet
// and the given rows. Cells are written with explicit references so gaps are
// exercised.
func writeXLSX(t *testing.T, path string, shared []string, rows [][]string) {
	t.Helper()
	sheet := strings.Builder{}
	sheet.WriteString(`<?xml version="1.0"?>
<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">`)
	for r, row := range rows {
		sheet.WriteString(`<row>`)
		for c, cell := range row {
			if cell == "" {
				continue // gap: the column index comes from the reference
			}
			ref := columnLetters(c) + itoa(r+1)
			switch {
			case strings.HasPrefix(cell, "#S#"): // shared string
				idx := strings.TrimPrefix(cell, "#S#")
				sheet.WriteString(`<c r="` + ref + `" t="s"><v>` + idx + `</v></c>`)
			default:
				sheet.WriteString(`<c r="` + ref + `"><v>` + cell + `</v></c>`)
			}
		}
		sheet.WriteString(`</row>`)
	}
	sheet.WriteString(`</worksheet>`)

	sharedXML := strings.Builder{}
	sharedXML.WriteString(`<?xml version="1.0"?><sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">`)
	for _, s := range shared {
		sharedXML.WriteString(`<si><t>` + s + `</t></si>`)
	}
	sharedXML.WriteString(`</sst>`)

	files := map[string]string{
		"xl/workbook.xml": `<?xml version="1.0"?>
<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
<sheets><sheet name="Data" sheetId="1" r:id="rId1"/></sheets>
</workbook>`,
		"xl/_rels/workbook.xml.rels": `<?xml version="1.0"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/>
</Relationships>`,
		"xl/worksheets/sheet1.xml": `<?xml version="1.0"?>` + sheet.String(),
		"xl/sharedStrings.xml":     sharedXML.String(),
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
}

func columnLetters(c int) string {
	s := ""
	for {
		s = string(rune('A'+c%26)) + s
		c = c/26 - 1
		if c < 0 {
			return s
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// TestLoaderXLSX checks the Excel reader: first worksheet, header row, shared
// strings, skipped cells and number cells rendered as strings.
func TestLoaderXLSX(t *testing.T) {
	dir := t.TempDir()
	xlsx := filepath.Join(dir, "book.xlsx")
	// Row 1 (header): A1 name (shared), C1 pin (gap at B1).
	// Row 2: A2 "#S#0", B2 missing, C2 42.
	writeXLSX(t, xlsx,
		[]string{"name", "Sensor A"},
		[][]string{
			{"#S#0", "", "pin"},
			{"#S#1", "", "42"},
		})

	val, err := loader.Load("xlsx", xlsx)
	if err != nil {
		t.Fatalf("load xlsx: %v", err)
	}
	arr, ok := val.(*parser.ArrayValue)
	if !ok {
		t.Fatalf("expected ArrayValue, got %T", val)
	}
	if len(arr.Elements) != 1 {
		t.Fatalf("expected 1 data row, got %d", len(arr.Elements))
	}
	row, ok := arr.Elements[0].(*parser.MapValue)
	if !ok {
		t.Fatalf("row is %T, want MapValue", arr.Elements[0])
	}
	get := func(key string) string {
		v, found := row.Lookup(key)
		if !found {
			t.Errorf("key %q missing", key)
			return ""
		}
		s, isStr := v.(*parser.StringValue)
		if !isStr {
			t.Errorf("value for %q is %T, want StringValue", key, v)
			return ""
		}
		return s.Value
	}
	if got := get("name"); got != "Sensor A" {
		t.Errorf("shared string cell = %q, want %q", got, "Sensor A")
	}
	if got := get("pin"); got != "42" {
		t.Errorf("number cell = %q, want %q", got, "42")
	}
}

func TestLoaderXLSXAliases(t *testing.T) {
	dir := t.TempDir()
	xlsx := filepath.Join(dir, "book.xlsx")
	writeXLSX(t, xlsx, []string{"label"}, [][]string{{"label"}, {"#S#0"}})

	for _, format := range []string{"xlsx", "xls", "excel"} {
		if _, err := loader.Load(format, xlsx); err != nil {
			t.Errorf("Load(%q): %v", format, err)
		}
	}

	// A file that is not a zip is not a workbook: say so clearly.
	txt := filepath.Join(dir, "book.xls")
	if err := os.WriteFile(txt, []byte("not a workbook"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := loader.Load("xls", txt)
	if err == nil || !strings.Contains(err.Error(), "not an xlsx") {
		t.Errorf("expected a clear legacy-.xls error, got %v", err)
	}
}

// TestWithXLSXBlock loads a workbook through a with-block and iterates it.
func TestWithXLSXBlock(t *testing.T) {
	dir := t.TempDir()
	xlsx := filepath.Join(dir, "sensors.xlsx")
	writeXLSX(t, xlsx,
		[]string{"label", "gain"},
		[][]string{
			{"label", "gain"},
			{"#S#0", "0.5"},
			{"#S#1", "1.5"},
		})

	src := `#package T
+App = {
  Class = ReferenceContainer
  with xlsx("sensors.xlsx") as sensors begin
    #foreach row in @sensors
      ("+Ch" .. @row.label) = {
        Class = ReferenceContainer
        Gain = @row.gain
      }
    #end
  end
}
`
	pt := index.NewProjectTree()
	cfg, err := parser.NewParser(src).Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pt.AddFile(filepath.Join(dir, "main.marte"), cfg)
	pt.ResolveReferences(nil)
	pt.ResolveFields(nil)

	v := validator.NewValidator(pt, dir, nil)
	v.ValidateProject(context.Background())
	for _, d := range v.Diagnostics {
		if d.Level == validator.LevelError {
			t.Errorf("unexpected error: %s", d.Message)
		}
	}
}
