package integration

import (
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/formatter"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/validator"
)

// TestInOperator covers the membership operator in lists, dicts and strings,
// in plain values and in conditions.
func TestInOperator(t *testing.T) {
	const src = `#package T

var names: [string] = { "AI0", "AI1" }
var map: { k1: int = 1, k2: int = 2 }

+App = {
  Class = ReferenceContainer
  HasAI1 = "AI1" in @names
  HasMissing = "ZZ" in @names
  IsKey = "k2" in @map
  IsValue = "k3" in @map
  Substring = "AI" in "AI0 and AI1"
  NoSubstring = "ZZ" in "AI0"
  if "AI0" in @names
    InIf = true
  end
}
`
	out := buildTemp(t, src)
	for _, want := range []string{
		"HasAI1 = true", "HasMissing = false", "IsKey = true", "IsValue = false",
		"Substring = true", "NoSubstring = false", "InIf = true",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("build output missing %q:\n%s", want, out)
		}
	}
}

// TestInOperatorListOfNumbers pins numeric membership across int/float.
func TestInOperatorListOfNumbers(t *testing.T) {
	out := buildTemp(t, `#package T
+App = {
  Class = ReferenceContainer
  A = 2 in { 1, 2, 3 }
  B = 2.0 in { 1, 2, 3 }
  C = 9 in { 1, 2, 3 }
}
`)
	for _, want := range []string{"A = true", "B = true", "C = false"} {
		if !strings.Contains(out, want) {
			t.Errorf("build output missing %q:\n%s", want, out)
		}
	}
}

// TestAssertPasses: satisfied assertions leave no diagnostics and never
// appear in build output.
func TestAssertPasses(t *testing.T) {
	src := `#package T

var variant: string = "A"

assert(@variant == "A", "variant must be A")
assert("AI0" in { "AI0", "AI1" })
assert("k1" in { k1 = 1 })
assert("sub" in "substring")

+App = {
  Class = ReferenceContainer
}
`
	for _, d := range validateWithOverrides(t, src, nil) {
		if d.Level == validator.LevelError {
			t.Errorf("unexpected error: %s", d.Message)
		}
	}
	out := buildTemp(t, src)
	if strings.Contains(out, "assert") {
		t.Errorf("assert leaked into build output:\n%s", out)
	}
}

// TestAssertFailsBuild: a false condition is an error carrying the message,
// and the build refuses to produce output.
func TestAssertFailsBuild(t *testing.T) {
	src := `#package T

var variant: string = "B"

assert(@variant == "A", "variant must be A, got " .. @variant)

+App = {
  Class = ReferenceContainer
}
`
	diags := validateWithOverrides(t, src, nil)
	found := false
	for _, d := range diags {
		if d.Level == validator.LevelError && strings.Contains(d.Message, "variant must be A, got B") {
			found = true
		}
	}
	if !found {
		t.Fatalf("assert failure not reported, diagnostics: %+v", diags)
	}

	// The predicate itself must be reported at the assert position.
	for _, d := range diags {
		if strings.Contains(d.Message, "variant must be A") && d.Position.Line != 5 {
			t.Errorf("assert reported at line %d, want 5", d.Position.Line)
		}
	}
}

// TestAssertScope: assert in an inactive branch stays silent, assert in a
// foreach is evaluated per iteration, and -v overrides are visible.
func TestAssertScope(t *testing.T) {
	src := `#package T

var variant: string = "dev"
var names: [string] = { "AI0", "AI1" }

+App = {
  Class = ReferenceContainer
  if false
    assert(false, "inactive branch must not fire")
  end
  foreach n in { "AI0", "AI1" }
    assert(@n in @names, "unknown loop signal " .. @n)
  end
  assert(@variant == "prod", "release builds need profile=prod")
}
`
	// Without the override the variant assert fails, the others pass.
	var variantFailures int
	for _, d := range validateWithOverrides(t, src, nil) {
		if d.Level == validator.LevelError {
			if strings.Contains(d.Message, "inactive branch") || strings.Contains(d.Message, "unknown loop signal") {
				t.Errorf("unexpected error: %s", d.Message)
			}
			if strings.Contains(d.Message, "release builds need profile=prod") {
				variantFailures++
			}
		}
	}
	if variantFailures != 1 {
		t.Errorf("variant assert reported %d times, want 1", variantFailures)
	}

	// With the override it passes.
	for _, d := range validateWithOverrides(t, src, map[string]string{"variant": `"prod"`}) {
		if d.Level == validator.LevelError {
			t.Errorf("override did not satisfy the assert: %s", d.Message)
		}
	}
}

// TestAssertAndInFormatRoundTrip keeps fmt lossless for the new syntax.
func TestAssertAndInFormatRoundTrip(t *testing.T) {
	src := `#package T

assert(@variant == "A", "variant must be A")
assert("k" in { k = 1 })

+App = {
  Class = ReferenceContainer
  Has = "x" in { "x", "y" }
}
`
	cfg, err := parser.NewParser(src).Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var sb strings.Builder
	formatter.Format(cfg, &sb)
	out := sb.String()

	for _, want := range []string{
		`assert((@variant == "A"), "variant must be A")`,
		`assert(("k" in { k = 1 }))`,
		`Has = ("x" in { "x", "y" })`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("formatted output missing %q:\n%s", want, out)
		}
	}

	cfg2, err := parser.NewParser(out).Parse()
	if err != nil {
		t.Fatalf("re-parse formatted output: %v\n%s", err, out)
	}
	var sb2 strings.Builder
	formatter.Format(cfg2, &sb2)
	if sb2.String() != out {
		t.Errorf("formatting is not idempotent:\nfirst:\n%s\nsecond:\n%s", out, sb2.String())
	}
}
