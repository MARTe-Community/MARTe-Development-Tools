package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/builder"
	"github.com/marte-community/marte-dev-tools/internal/formatter"
	"github.com/marte-community/marte-dev-tools/internal/lsp"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/validator"
)

const structTypesSrc = `#package T

type ADCConf_T {
  device_name: str,
  board_id: uint8,
  frequency: uint32
}

#var sample_freq: uint32 = 1000
#var ADC_A: ADCConf_T = {
  device_name = "/dev/tty0",
  board_id = 3,
  frequency = @sample_freq
}

+App = {
  Class = ReferenceContainer
  DeviceName = @ADC_A.device_name
  Board = @ADC_A.board_id
  Freq = @ADC_A.frequency
}
`

// --- parser ---

func TestTypeStatementParsing(t *testing.T) {
	cfg, err := parser.NewParser(structTypesSrc).Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	var typedef *parser.TypeDefinition
	for _, d := range cfg.Definitions {
		if td, ok := d.(*parser.TypeDefinition); ok && td.Name == "ADCConf_T" {
			typedef = td
		}
	}
	if typedef == nil {
		t.Fatal("type definition not found in declarations")
	}
	if len(typedef.Fields) != 3 {
		t.Fatalf("expected 3 fields, got %d", len(typedef.Fields))
	}
	want := []struct{ name, expr string }{
		{"device_name", "str"},
		{"board_id", "uint8"},
		{"frequency", "uint32"},
	}
	for i, w := range want {
		f := typedef.Fields[i]
		if f.Name != w.name || f.TypeExpr != w.expr {
			t.Errorf("field %d = %s: %s, want %s: %s", i, f.Name, f.TypeExpr, w.name, w.expr)
		}
	}

	// The struct literal parses as a MapValue with all fields.
	var vdef *parser.VariableDefinition
	for _, d := range cfg.Definitions {
		if vd, ok := d.(*parser.VariableDefinition); ok && vd.Name == "ADC_A" {
			vdef = vd
		}
	}
	if vdef == nil {
		t.Fatal("variable not found")
	}
	if vdef.TypeExpr != "ADCConf_T" {
		t.Errorf("TypeExpr = %q", vdef.TypeExpr)
	}
	mv, ok := vdef.DefaultValue.(*parser.MapValue)
	if !ok {
		t.Fatalf("default value is %T, want MapValue", vdef.DefaultValue)
	}
	if len(mv.Keys) != 3 {
		t.Errorf("struct literal has %d fields, want 3", len(mv.Keys))
	}
}

func TestStructLiteralParseErrors(t *testing.T) {
	cases := []struct {
		name, src, wantErr string
	}{
		{"field without type", "type T {\n  a:\n}\n", "expected a type"},
		{"duplicate field", "type T {\n  a: uint8,\n  a: uint8\n}\n", "duplicate field"},
		{"unclosed brace", "type T {\n  a: uint8\n", "expected }"},
		{"empty body", "type T {\n}\n", "has no fields"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parser.NewParser(tc.src).Parse()
			if err == nil {
				t.Fatalf("expected an error containing %q, got none", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestBracesStayListsAndStructs(t *testing.T) {
	// Plain braces are still lists; `name = value` switches to a struct.
	cfg, err := parser.NewParser("#package T\nvar xs: [int] = { 1, 2, 3 }\nvar s: str = \"x\"\n").Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, d := range cfg.Definitions {
		if vd, ok := d.(*parser.VariableDefinition); ok && vd.Name == "xs" {
			if _, isList := vd.DefaultValue.(*parser.ArrayValue); !isList {
				t.Errorf("list literal became %T", vd.DefaultValue)
			}
		}
	}
}

// --- validation ---

func TestStructVarValidation(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		wantErr []string
	}{
		{"valid", structTypesSrc, nil},
		{"missing field", strings.Replace(structTypesSrc, "  board_id = 3,\n", "", 1),
			[]string{"board_id"}},
		{"unknown field", strings.Replace(structTypesSrc, "  board_id = 3,", "  board_id = 3,\n  nope = 1,", 1),
			[]string{"field not allowed"}},
		{"uint8 range", strings.Replace(structTypesSrc, "board_id = 3,", "board_id = 300,", 1),
			[]string{"out of bound"}},
		{"wrong field type", strings.Replace(structTypesSrc, `device_name = "/dev/tty0",`, `device_name = 5,`, 1),
			[]string{"device_name"}},
		{"undeclared struct type", strings.Replace(structTypesSrc, "var ADC_A: ADCConf_T", "var ADC_A: ADCCon_Typo", 1),
			[]string{"Unknown type 'ADCCon_Typo'"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			diags := validateTemp(t, tc.src)
			for _, want := range tc.wantErr {
				found := false
				for _, d := range diags {
					if strings.Contains(d.Message, want) {
						found = true
					}
				}
				if !found {
					t.Errorf("expected a diagnostic containing %q, got %v", want, diags)
				}
			}
			if tc.wantErr == nil && len(diags) > 0 {
				for _, d := range diags {
					if d.Level == validator.LevelError {
						t.Errorf("unexpected error: %s", d.Message)
					}
				}
			}
		})
	}
}

func TestNestedStructTypes(t *testing.T) {
	src := `#package T

type Inner_T {
  pin: uint8
}
type Outer_T {
  name: str,
  inner: Inner_T
}

#var cfg: Outer_T = {
  name = "board",
  inner = {
    pin = 5
  }
}
`
	diags := validateTemp(t, src)
	for _, d := range diags {
		if d.Level == validator.LevelError {
			t.Errorf("unexpected error: %s", d.Message)
		}
	}

	// A bad nested field is caught through the referenced type.
	bad := strings.Replace(src, "pin = 5", "pin = 999", 1)
	diags = validateTemp(t, bad)
	found := false
	for _, d := range diags {
		if strings.Contains(d.Message, "out of bound") {
			found = true
		}
	}
	if !found {
		t.Errorf("nested uint8 violation not reported, got %v", diags)
	}
}

func TestBareTypeNameRemainsClassReference(t *testing.T) {
	// Established behaviour: an undeclared bare type denotes a node/class
	// reference and must keep working.
	src := `#package T
var d: GAM = "MyGAM"
+App = {
  Class = ReferenceContainer
  Ref = @d
}
`
	diags := validateTemp(t, src)
	for _, d := range diags {
		if d.Level == validator.LevelError {
			t.Errorf("unexpected error: %s", d.Message)
		}
	}
}

// --- builder ---

func TestStructVarBuildAndPartialOverride(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "main.marte")
	if err := os.WriteFile(file, []byte(structTypesSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	build := func(overrides map[string]string) string {
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

	res := build(nil)
	for _, want := range []string{
		`DeviceName = "/dev/tty0"`,
		"Board = 3",
		"Freq = 1000", // @ADC_A.frequency -> @sample_freq -> 1000
	} {
		if !strings.Contains(res, want) {
			t.Errorf("expected %q in build output, got:\n%s", want, res)
		}
	}

	// Partial override: only board_id changes.
	res = build(map[string]string{"ADC_A": "{ board_id = 7 }"})
	if !strings.Contains(res, "Board = 7") {
		t.Errorf("override not applied, got:\n%s", res)
	}
	if !strings.Contains(res, `DeviceName = "/dev/tty0"`) || !strings.Contains(res, "Freq = 1000") {
		t.Errorf("partial override dropped untouched fields, got:\n%s", res)
	}
}

// --- formatter ---

func TestStructTypesFormatRoundTrip(t *testing.T) {
	cfg, err := parser.NewParser(structTypesSrc).Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	var out strings.Builder
	formatter.Format(cfg, &out)
	formatted := out.String()

	// The type statement survives formatting and re-parses cleanly.
	if !strings.Contains(formatted, "type ADCConf_T {") {
		t.Fatalf("type statement missing from formatted output:\n%s", formatted)
	}
	cfg2, err := parser.NewParser(formatted).Parse()
	if err != nil {
		t.Fatalf("re-parse formatted output: %v\n%s", err, formatted)
	}
	var typedef2 *parser.TypeDefinition
	for _, d := range cfg2.Definitions {
		if td, ok := d.(*parser.TypeDefinition); ok && td.Name == "ADCConf_T" {
			typedef2 = td
		}
	}
	if typedef2 == nil || len(typedef2.Fields) != 3 {
		t.Error("type definition lost or truncated by the formatter")
	}
}

// --- LSP ---

func TestLSPStructTypeDiagnostics(t *testing.T) {
	lsp.ResetTestServer()
	lsp.GetTestDocuments()["file:///struct.marte"] = structTypesSrc

	// A missing required field must surface as a diagnostic.
	broken := strings.Replace(structTypesSrc, "  board_id = 3,\n", "", 1)
	lsp.HandleDidChange(lsp.DidChangeTextDocumentParams{
		TextDocument: lsp.VersionedTextDocumentIdentifier{URI: "file:///struct.marte"},
		ContentChanges: []lsp.TextDocumentContentChangeEvent{{
			Text: broken,
		}},
	})

	tree := lsp.GetTestTree()
	v := validator.NewValidator(tree, ".", nil)
	v.ValidateProject(context.Background())
	found := false
	for _, d := range v.Diagnostics {
		if strings.Contains(d.Message, "board_id") {
			found = true
		}
	}
	if !found {
		t.Error("missing struct field not reported through the LSP validation path")
	}
}
