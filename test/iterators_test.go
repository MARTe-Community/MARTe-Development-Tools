package integration

import (
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/formatter"
	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/validator"
)

// evalValue evaluates src as an expression against a tree built from decls
// (variable declarations evaluated in the same context).
func evalValue(t *testing.T, decls, src string) parser.Value {
	t.Helper()
	pt := index.NewProjectTree()
	cfg, err := parser.NewParser(decls).Parse()
	if err != nil {
		t.Fatalf("parse decls: %v", err)
	}
	pt.AddFile("decls.marte", cfg)
	pt.ResolveReferences(nil)
	pt.ResolveFields(nil)

	val, err := parser.ParseValue(src)
	if err != nil {
		t.Fatalf("parse value %q: %v", src, err)
	}
	ctx := &index.EvaluationContext{Variables: map[string]parser.Value{}, Tree: pt}
	// Seed the tree's variables so @name resolves.
	pt.Walk(func(n *index.ProjectNode) {
		for name, info := range n.Variables {
			if info.Def != nil && info.Def.DefaultValue != nil {
				ctx.Variables[name] = info.Def.DefaultValue
			}
		}
	})
	return pt.EvaluateValue(val, ctx)
}

func renderValue(t *testing.T, v parser.Value) string {
	t.Helper()
	if v == nil {
		t.Fatal("value is nil")
	}
	switch val := v.(type) {
	case *parser.ArrayValue:
		parts := make([]string, 0, len(val.Elements))
		for _, e := range val.Elements {
			parts = append(parts, renderValue(t, e))
		}
		return "[" + strings.Join(parts, " ") + "]"
	case *parser.StringValue:
		return val.Value
	case *parser.IntValue:
		return val.Raw
	case *parser.BoolValue:
		if val.Value {
			return "true"
		}
		return "false"
	case *parser.FunctionCall, *parser.MethodCall:
		return "unresolved"
	}
	return "?"
}

func TestIteratorFunctions(t *testing.T) {
	const decls = `#package T
var names: [string] = { "a", "b" }
var chans: [int] = { 10, 20, 30 }
`

	cases := []struct {
		name string
		src  string
		want string
	}{
		{"enumerate list", "enumerate(@names)", "[[0 a] [1 b]]"},
		{"at-form", "@enumerate(@names)", "[[0 a] [1 b]]"},
		{"zip stops at shortest", "zip(@names, @chans)", "[[a 10] [b 20]]"},
		{"range stop", "range(3)", "[0 1 2]"},
		{"range start stop", "range(1, 4)", "[1 2 3]"},
		{"range step", "range(0, 6, 2)", "[0 2 4]"},
		{"range negative step", "range(3, 0, -1)", "[3 2 1]"},
		{"keys sorted", "keys({ x = 1, y = 2 })", "[x y]"},
		{"values in key order", "values({ x = 1, y = 2 })", "[1 2]"},
		{"len list", "len(@names)", "2"},
		{"len string", "len(\"héllo\")", "5"},
		{"reversed", "reversed(@names)", "[b a]"},
		{"concat", "concat(@names, @chans)", "[a b 10 20 30]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := renderValue(t, evalValue(t, decls, tc.src))
			if got != tc.want {
				t.Errorf("%s = %s, want %s", tc.src, got, tc.want)
			}
		})
	}
}

func TestIteratorFunctionsUnknownStaySymbolic(t *testing.T) {
	for _, src := range []string{"nosuchfn(@names)", "zip(1, 2)", "range(@names)"} {
		got := renderValue(t, evalValue(t, "#package T\nvar names: [string] = { \"a\" }\n", src))
		if got != "unresolved" {
			t.Errorf("%s should stay symbolic, got %q", src, got)
		}
	}
}

// TestZipDestructuringInForEach covers `foreach a, b in @zip(...)`: rows that
// are two-element lists bind both loop variables, while plain elements keep
// the index/element binding.
func TestZipDestructuringInForEach(t *testing.T) {
	out := buildTemp(t, `#package T

var names: [string] = { "a", "b" }
var chans: [int] = { 10, 20 }

+App = {
  Class = ReferenceContainer
  foreach name, chan in zip(@names, @chans)
    ("+Chan_" .. @name) = {
      Id = @chan
    }
  end
  foreach i, v in { 7, 8 }
    ("+Idx_" .. @i) = {
      V = @v
    }
  end
}
`)
	for _, want := range []string{"+Chan_a = {", "Id = 10", "+Chan_b = {", "Id = 20", "+Idx_0 = {", "V = 7", "+Idx_1 = {", "V = 8"} {
		if !strings.Contains(out, want) {
			t.Errorf("build output missing %q:\n%s", want, out)
		}
	}
}

// TestIteratorCallsInFormatterRoundTrip pins that calls survive `mdt fmt`
// (method calls used to be dropped entirely).
func TestIteratorCallsInFormatterRoundTrip(t *testing.T) {
	src := `#package T

var names: [string] = { "a" }

+App = {
  Class = ReferenceContainer
  N = @len(@names)
  Z = @zip(@names, @names)
  Clean = @device.replace("EC-", "")
  foreach a, b in @zip(@names, @names)
    ("+X_" .. @a) = { V = @b }
  end
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
		"N = len(@names)",
		"Z = zip(@names, @names)",
		`Clean = @device.replace("EC-", "")`,
		"foreach a, b in zip(@names, @names)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("formatted output missing %q:\n%s", want, out)
		}
	}

	// The formatter's output must parse again and format identically.
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

// TestIteratorFunctionsValidate keeps `mdt check`/build validation quiet for
// valid iterator usage.
func TestIteratorFunctionsValidate(t *testing.T) {
	src := `#package T

var names: [string] = { "a", "b" }

+App = {
  Class = RealTimeApplication
  +Data = {
    Class = ReferenceContainer
    DefaultDataSource = DS
    +DS = {
      Class = GAMDataSource
      Signals = { S = { Type = float32 } }
    }
  }
  +Functions = { Class = ReferenceContainer }
  Scheduler = { Class = GAMScheduler  TimingDataSource = DS }
  +States = { Class = ReferenceContainer }
  Count = @len(@names)
  Pairs = @zip(@names, @names)
}
`
	for _, d := range validateWithOverrides(t, src, nil) {
		if d.Level == validator.LevelError {
			t.Errorf("unexpected error: %s", d.Message)
		}
	}

}
