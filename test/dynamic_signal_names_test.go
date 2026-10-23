package integration

import (
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/formatter"
	"github.com/marte-community/marte-dev-tools/internal/parser"
)

// TestDynamicSignalNameUnparenthesized pins `"DI_" .. @v = { … }` — a
// dynamic signal name as the first entry of a Signals block, where the
// brace used to be taken for an array value.
func TestDynamicSignalNameUnparenthesized(t *testing.T) {
	out := buildTemp(t, `#package T
var v: string = "AI0"
+App = {
  Class = ReferenceContainer
  +DDB = {
    Class = GAMDataSource
    Signals = {
      "DI_" .. @v = {
        Type = float32
      }
    }
  }
}
`)
	if !strings.Contains(out, "DI_AI0 = {") {
		t.Errorf("dynamic signal name not expanded:\n%s", out)
	}
	if strings.Contains(out, "@v") {
		t.Errorf("unexpanded variable in build output:\n%s", out)
	}
}

// TestDynamicSignalNameInForeach: one signal per iteration, each named from
// the loop variable.
func TestDynamicSignalNameInForeach(t *testing.T) {
	out := buildTemp(t, `#package T
var names: [string] = { "AI0", "AI1" }
+App = {
  Class = ReferenceContainer
  +DDB = {
    Class = GAMDataSource
    Signals = {
      foreach n in @names
        "DI_" .. @n = {
          Type = float32
        }
      end
    }
  }
}
`)
	for _, want := range []string{"DI_AI0 = {", "DI_AI1 = {"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

// TestDynamicObjectNameForms: parenthesised, unparenthesised and prefixed
// concatenations all name objects.
func TestDynamicObjectNameForms(t *testing.T) {
	out := buildTemp(t, `#package T
var v: string = "X"
+App = {
  Class = ReferenceContainer
  ("+A_" .. @v) = { Class = ReferenceContainer }
  "+B_" .. @v = { Class = ReferenceContainer }
  +C = { Class = ReferenceContainer }
}
`)
	for _, want := range []string{"+A_X = {", "+B_X = {", "+C = {"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

// TestDynamicSignalNameFormat: fmt may add parentheses but must stay valid
// and keep the name expression.
func TestDynamicSignalNameFormat(t *testing.T) {
	src := `#package T
+App = {
  Class = ReferenceContainer
  +DDB = {
    Class = GAMDataSource
    Signals = {
      "DI_" .. @v = { Type = float32 }
    }
  }
}
`
	cfg, err := parser.NewParser(src).Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var sb strings.Builder
	formatter.Format(cfg, &sb)
	out := sb.String()
	if !strings.Contains(out, `("DI_" .. @v) = {`) {
		t.Errorf("dynamic name lost by the formatter:\n%s", out)
	}
	if _, err := parser.NewParser(out).Parse(); err != nil {
		t.Fatalf("re-parse formatted output: %v\n%s", err, out)
	}
}

// TestArrayValueStillParsedAsArray: the lookahead must not turn ordinary
// array values into subnodes.
func TestArrayValueStillParsedAsArray(t *testing.T) {
	out := buildTemp(t, `#package T
var v: string = "X"
+App = {
  Class = ReferenceContainer
  L = { "a" .. @v, "b" }
  M = { (1), 2 }
}
`)
	if !strings.Contains(out, `L = { "aX" "b" }`) {
		t.Errorf("array with concatenation broken:\n%s", out)
	}
	if !strings.Contains(out, "M = { 1 2 }") {
		t.Errorf("array with grouped element broken:\n%s", out)
	}
}
