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

// buildTemp parses and builds src, returning the generated output.
func buildTemp(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	file := filepath.Join(dir, "in.marte")
	if err := os.WriteFile(file, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
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
	return string(content)
}

// validateTemp parses src and runs full validation, returning diagnostics.
func validateTemp(t *testing.T, src string) []validator.Diagnostic {
	t.Helper()
	pt := index.NewProjectTree()
	cfg, err := parser.NewParser(src).Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pt.AddFile("main.marte", cfg)
	pt.ResolveReferences(nil)
	v := validator.NewValidator(pt, ".", nil)
	v.ValidateProject(context.Background())
	return v.Diagnostics
}

func TestListConcatOperator(t *testing.T) {
	res := buildTemp(t, `#package T
var base: [int] = { 1, 2 }
+O = {
  Class = ReferenceContainer
  L = { 1, 2 } .. { 3, 4 }
  V = @base .. { 9 }
}`)
	for _, want := range []string{"L = { 1 2 3 4 }", "V = { 1 2 9 }"} {
		if !strings.Contains(res, want) {
			t.Errorf("expected %q in output, got:\n%s", want, res)
		}
	}
}

func TestDictConcatOperator(t *testing.T) {
	res := buildTemp(t, `#package T
+Dict = {
  Class = ReferenceContainer
  A = 1
} .. {
  B = 2
}

+Merged = {
  Class = ReferenceContainer
  Common = "base"
} .. {
  Class = ReferenceContainer
  Common = "override"
  Extra = "added"
}`)
	for _, want := range []string{"A = 1", "B = 2", "Common = \"override\"", "Extra = \"added\""} {
		if !strings.Contains(res, want) {
			t.Errorf("expected %q in output, got:\n%s", want, res)
		}
	}
	if strings.Contains(res, "Common = \"base\"") {
		t.Errorf("later operand must override earlier field, got:\n%s", res)
	}
	if !strings.Contains(res, "+Dict = {") || !strings.Contains(res, "+Merged = {") {
		t.Errorf("concatenated objects missing from output:\n%s", res)
	}
}

func TestElseIfOneWordForm(t *testing.T) {
	src := `#package T
var n: int = 2
+O = {
  Class = ReferenceContainer
  #if (@n > 2)
    P = "wide"
  #elseif (@n == 2)
    P = "narrow"
  #else
    P = "single"
  #end
}`
	res := buildTemp(t, src)
	if !strings.Contains(res, "P = \"narrow\"") {
		t.Errorf("expected one-word #elseif branch to win, got:\n%s", res)
	}
	diags := validateTemp(t, src)
	for _, d := range diags {
		if d.Level == validator.LevelError {
			t.Errorf("unexpected error: %s", d.Message)
		}
	}
}

func TestStringEscapes(t *testing.T) {
	src := `#package T
+O = {
  Class = ReferenceContainer
  Q = "a\"b"
  B = "x\\y"
  T = "n\tt"
}`
	res := buildTemp(t, src)
	// Output must be re-escaped so it stays valid configuration text.
	for _, want := range []string{`Q = "a\"b"`, `B = "x\\y"`, `T = "n\tt"`} {
		if !strings.Contains(res, want) {
			t.Errorf("expected %q in output, got:\n%s", want, res)
		}
	}
	// The emitted text must parse back to the same values.
	rt := validateTemp(t, res)
	for _, d := range rt {
		if d.Level == validator.LevelError {
			t.Errorf("re-parsing the built output failed: %s", d.Message)
		}
	}
	if got := buildTemp(t, res); !strings.Contains(got, `Q = "a\"b"`) {
		t.Errorf("escapes did not round-trip, got:\n%s", got)
	}
}

func TestConcatPrecedenceBelowArithmetic(t *testing.T) {
	res := buildTemp(t, `#package T
var n: int = 5
+O = {
  Class = ReferenceContainer
  P = "v" .. @n + 1
}`)
	if !strings.Contains(res, "P = \"v6\"") {
		t.Errorf("expected \"v\" .. (5+1) = v6, got:\n%s", res)
	}
}

func TestBitwiseOperatorsInVariableInitializers(t *testing.T) {
	src := `#package T
var f1: int = (0b1111 & 0x03)
var f2: int = (1 | 4)
var f3: int = (3 ^ 1)
var f4: bool = (2 <= 3)
+O = {
  Class = ReferenceContainer
  X = (0b1111 & 0x03)
}`
	diags := validateTemp(t, src)
	for _, d := range diags {
		if d.Level == validator.LevelError && strings.Contains(d.Message, "value mismatch") {
			t.Errorf("bitwise initializer rejected: %s", d.Message)
		}
	}
	res := buildTemp(t, src)
	if !strings.Contains(res, "X = 3") {
		t.Errorf("expected X = 3, got:\n%s", res)
	}
}

func TestTypeExpressionForms(t *testing.T) {
	src := `#package T
var a: uint32 = 8000
var b: [int] = { 1, 2 }
var c: [int] = { 5 }
var d: GAM = "SomeGAM"
var e: string =~ "^localhost" = "localhost:9"
var f: int|uint = 4
var g: float64 = 1.5
+O = {
  Class = ReferenceContainer
  R = @d
}
`
	diags := validateTemp(t, src)
	for _, d := range diags {
		if d.Level == validator.LevelError {
			t.Errorf("unexpected error: %s", d.Message)
		}
	}
}

func TestRegexTypeRejectsNonMatching(t *testing.T) {
	src := `#package T
var host: string =~ "^localhost" = "remote:1"
+O = {
  Class = ReferenceContainer
}`
	diags := validateTemp(t, src)
	found := false
	for _, d := range diags {
		if d.Level == validator.LevelError && strings.Contains(d.Message, "value mismatch") {
			found = true
		}
	}
	if !found {
		t.Error("expected value mismatch for regex-constrained variable with non-matching value")
	}
}

// One-word #elseif must also work inside array literals.
func TestElseIfInArrayLiteral(t *testing.T) {
	res := buildTemp(t, `#package T
var mode: int = 2
+O = {
  Class = ReferenceContainer
  List = {
    A,
    #if (@mode == 1)
      B,
    #elseif (@mode == 2)
      C,
    #else
      D,
    #end
    E,
  }
}`)
	if !strings.Contains(res, "List = { A C E }") {
		t.Errorf("expected the elseif branch to be active (A C E), got:\n%s", res)
	}
}
