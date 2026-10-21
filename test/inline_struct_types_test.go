package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/builder"
	"github.com/marte-community/marte-dev-tools/internal/formatter"
	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/validator"
)

const inlineStructSrc = `#package T

var hardware: {
  address: uint16
  port: uint16
  sample_rate: uint8
}

+App = {
  Class = ReferenceContainer
  Signals = {
    A = {
      SampleRate = @hardware.sample_rate
      Port = @hardware.port
    }
  }
}
`

// TestInlineStructTypeParsing covers `var v: { field: type, ... }` — an
// anonymous structured type on a variable.
func TestInlineStructTypeParsing(t *testing.T) {
	cfg, err := parser.NewParser(inlineStructSrc).Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var vdef *parser.VariableDefinition
	for _, d := range cfg.Definitions {
		if vd, ok := d.(*parser.VariableDefinition); ok && vd.Name == "hardware" {
			vdef = vd
		}
	}
	if vdef == nil {
		t.Fatal("hardware variable not found")
	}
	if vdef.InlineType == nil {
		t.Fatal("InlineType not captured")
	}
	if len(vdef.InlineType.Fields) != 3 {
		t.Fatalf("expected 3 fields, got %d", len(vdef.InlineType.Fields))
	}
	wantTypes := []string{"uint16", "uint16", "uint8"}
	for i, want := range wantTypes {
		if vdef.InlineType.Fields[i].TypeExpr != want {
			t.Errorf("field %d type = %q, want %q", i, vdef.InlineType.Fields[i].TypeExpr, want)
		}
	}
	if !strings.HasPrefix(vdef.TypeExpr, "{") || !strings.Contains(vdef.TypeExpr, "sample_rate: uint8") {
		t.Errorf("canonical TypeExpr = %q", vdef.TypeExpr)
	}
}

func TestInlineStructTypeErrors(t *testing.T) {
	cases := []struct{ name, src, wantErr string }{
		{"duplicate field", "var v: {\n  a: uint8,\n  a: uint8\n}\n", "duplicate field"},
		{"missing type", "var v: {\n  a:\n}\n", "expected a type"},
		{"unclosed", "var v: {\n  a: uint8\n", "unexpected EOF"},
		{"no fields", "var v: {\n}\n", "has no fields"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parser.NewParser(tc.src).Parse()
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("expected error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestInlineStructValidation(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		wantErr []string
	}{
		{"valid", inlineStructSrc, nil},
		{"uint16 range", strings.Replace(inlineStructSrc, "port: uint16", "port: uint16", 1), /* value set below via struct literal */
			nil},
		{"missing field", strings.Replace(inlineStructSrc, "      Port = @hardware.port\n", "", 1), nil},
	}
	_ = cases

	// Range violation through an override.
	diags := validateWithOverrides(t, inlineStructSrc, map[string]string{"hardware": `{ address = 70000 }`})
	found := false
	for _, d := range diags {
		if strings.Contains(d.Message, "out of bound") || strings.Contains(d.Message, "invalid value") {
			found = true
		}
	}
	if !found {
		t.Errorf("uint16 range violation not reported, got %v", diags)
	}

	// Unknown field through an override.
	diags = validateWithOverrides(t, inlineStructSrc, map[string]string{"hardware": `{ nope = 1 }`})
	found = false
	for _, d := range diags {
		if strings.Contains(d.Message, "field not allowed") {
			found = true
		}
	}
	if !found {
		t.Errorf("unknown override field not reported, got %v", diags)
	}

	// Struct variable with no value anywhere: a warning, not an error.
	src := `#package T
var hardware: {
  address: uint16
}
+App = {
  Class = ReferenceContainer
}
`
	pt := newIndexFromSource(t, src)
	v := validator.NewValidator(pt, ".", nil)
	v.MissingFilesAreWarnings = true
	v.Activate(context.Background())
	v.ValidateProject(context.Background())
	sawWarn := false
	sawErr := false
	for _, d := range v.Diagnostics {
		if strings.Contains(d.Message, "has no default value and no override") && d.Level == validator.LevelWarning {
			sawWarn = true
		}
		if d.Level == validator.LevelError {
			sawErr = true
		}
	}
	if !sawWarn {
		t.Errorf("expected the unset-struct-variable warning, got %v", v.Diagnostics)
	}
	if sawErr {
		t.Error("unset struct variable reported as error")
	}
}

// TestInlineStructBuildAndOverride is the reported use case: no default, the
// value arrives entirely through a partial override, and member access
// resolves in the output.
func TestInlineStructBuildAndOverride(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "main.marte")
	if err := os.WriteFile(file, []byte(inlineStructSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	b := builder.NewBuilder([]string{file}, map[string]string{
		"hardware": `{ address = 4660, port = 1234, sample_rate = 10 }`,
	})
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
		"SampleRate = 10",
		"Port = 1234",
	} {
		if !strings.Contains(res, want) {
			t.Errorf("expected %q in output, got:\n%s", want, res)
		}
	}
}

// TestInlineStructNestedInTypeDeclaration covers nested anonymous structs as
// field types of a named type.
func TestInlineStructNestedInTypeDeclaration(t *testing.T) {
	src := `#package T

type Config_T {
  name: str,
  hardware: {
    address: uint16,
    port: uint16
  }
}

#var cfg: Config_T = {
  name = "board",
  hardware = {
    address = 4660,
    port = 1234
  }
}

+App = {
  Class = ReferenceContainer
  Addr = @cfg.hardware.address
}
`
	diags := validateTemp(t, src)
	for _, d := range diags {
		if d.Level == validator.LevelError {
			t.Errorf("unexpected error: %s", d.Message)
		}
	}

	// The nested field's range is enforced.
	bad := strings.Replace(src, "address = 4660,", "address = 70000,", 1)
	diags = validateTemp(t, bad)
	found := false
	for _, d := range diags {
		if strings.Contains(d.Message, "out of bound") || strings.Contains(d.Message, "invalid value") {
			found = true
		}
	}
	if !found {
		t.Errorf("nested uint16 violation not reported, got %v", diags)
	}
}

// TestInlineStructFormatRoundTrip checks the formatter prints the anonymous
// type and the result re-parses.
func TestInlineStructFormatRoundTrip(t *testing.T) {
	cfg, err := parser.NewParser(inlineStructSrc).Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var out strings.Builder
	formatter.Format(cfg, &out)
	formatted := out.String()
	if !strings.Contains(formatted, "var hardware: {") {
		t.Fatalf("inline type missing from formatted output:\n%s", formatted)
	}
	if _, err := parser.NewParser(formatted).Parse(); err != nil {
		t.Errorf("formatted output does not re-parse: %v\n%s", err, formatted)
	}
}

// TestLetWithInlineStruct covers constants.
func TestLetWithInlineStruct(t *testing.T) {
	src := `#package T
let hardware: {
  address: uint16
} = {
  address = 4660
}
+App = {
  Class = ReferenceContainer
  Addr = @hardware.address
}
`
	diags := validateTemp(t, src)
	for _, d := range diags {
		if d.Level == validator.LevelError {
			t.Errorf("unexpected error: %s", d.Message)
		}
	}
}

func newIndexFromSource(t *testing.T, src string) *index.ProjectTree {
	t.Helper()
	cfg, err := parser.NewParser(src).Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pt := index.NewProjectTree()
	pt.AddFile("main.marte", cfg)
	pt.ResolveReferences(nil)
	pt.ResolveFields(nil)
	return pt
}

func validateWithOverrides(t *testing.T, src string, overrides map[string]string) []validator.Diagnostic {
	t.Helper()
	pt := newIndexFromSource(t, src)
	v := validator.NewValidator(pt, ".", overrides)
	v.Activate(context.Background())
	v.ValidateProject(context.Background())
	return v.Diagnostics
}

// TestInlineStructFieldDefaults covers `field: type = default`: a variable
// with no initializer takes the field defaults, and a partial override
// replaces only the given fields.
func TestInlineStructFieldDefaults(t *testing.T) {
	src := `#package T

var hardware: {
  address: uint16 = 4660
  port: uint16 = 1234
  sample_rate: uint8
}

+App = {
  Class = ReferenceContainer
  Address = @hardware.address
  Port = @hardware.port
}
`
	build := func(t *testing.T, overrides map[string]string) string {
		t.Helper()
		dir := t.TempDir()
		file := filepath.Join(dir, "main.marte")
		if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
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

	res := build(t, nil)
	for _, want := range []string{"Address = 4660", "Port = 1234"} {
		if !strings.Contains(res, want) {
			t.Errorf("expected %q with pure defaults, got:\n%s", want, res)
		}
	}

	res = build(t, map[string]string{"hardware": `{ address = 1 }`})
	if !strings.Contains(res, "Address = 1") {
		t.Errorf("override not applied, got:\n%s", res)
	}
	if !strings.Contains(res, "Port = 1234") {
		t.Errorf("partial override dropped the other field default, got:\n%s", res)
	}
}
