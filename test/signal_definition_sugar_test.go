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

// DataSource signal definition sugar: `Name: Type[Dim] [= { … }]` inside
// a Signals block, as an alternative to `Name = { Type = … }`.
func TestSignalDefinitionSugarParsing(t *testing.T) {
	src := `+DS = {
  Class = GAMDataSource
  Signals = {
    Plain: uint32
    Vector: float32[4]
    Extra: uint32 = {
      Frequency = 100
    }
  }
}`
	p := parser.NewParser(src)
	cfg, err := p.Parse()
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	ds := cfg.Definitions[0].(*parser.ObjectNode)
	var signals *parser.ObjectNode
	for _, d := range ds.Subnode.Definitions {
		if obj, ok := d.(*parser.ObjectNode); ok {
			if ref, ok := obj.Name.(*parser.ReferenceValue); ok && ref.Value == "Signals" {
				signals = obj
				break
			}
		}
	}
	if signals == nil {
		t.Fatal("Signals object not found")
	}
	defs := signals.Subnode.Definitions
	if len(defs) != 3 {
		t.Fatalf("expected 3 signal definitions, got %d", len(defs))
	}

	plain, ok := defs[0].(*parser.SignalShorthand)
	if !ok {
		t.Fatalf("expected SignalShorthand, got %T", defs[0])
	}
	if plain.DataSource != "" || plain.SignalName != "Plain" || plain.Type != "uint32" {
		t.Errorf("plain: ds=%q name=%q type=%q", plain.DataSource, plain.SignalName, plain.Type)
	}

	vector := defs[1].(*parser.SignalShorthand)
	if vector.NumElements == nil {
		t.Error("vector: expected NumElements")
	}

	extra := defs[2].(*parser.SignalShorthand)
	if !extra.HasExtraFields || len(extra.ExtraFields.Definitions) != 1 {
		t.Errorf("extra: HasExtraFields=%v defs=%d", extra.HasExtraFields, len(extra.ExtraFields.Definitions))
	}
}

// The sugar is only valid inside Signals blocks: elsewhere it is rejected.
func TestSignalDefinitionSugarRejectedOutsideSignals(t *testing.T) {
	src := `+O = {
  Class = ReferenceContainer
  Field: uint32
}`
	p := parser.NewParser(src)
	if _, err := p.Parse(); err == nil {
		t.Error("expected a parse error for signal definition sugar outside a Signals block")
	}
}

// Build output must use the standard `Name = { Type = … }` form, with
// dimensions mapped to NumberOfElements and extra fields merged.
func TestSignalDefinitionSugarBuild(t *testing.T) {
	dir := t.TempDir()
	src := `#package T
+DDB = {
  Class = GAMDataSource
  Signals = {
    Counter: uint32
    Waveform: float32[4] = {
      Frequency = 50
    }
  }
}
+Ref = {
  Class = ReferenceContainer
  DefaultDataSource = DDB
}
`
	file := filepath.Join(dir, "main.marte")
	os.WriteFile(file, []byte(src), 0644)

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
	res := string(content)

	for _, want := range []string{
		"Counter = {",
		"Type = uint32",
		"Waveform = {",
		"Type = float32",
		"NumberOfElements = 4",
		"Frequency = 50",
	} {
		if !strings.Contains(res, want) {
			t.Errorf("expected %q in build output, got:\n%s", want, res)
		}
	}
	// The sugar itself must not leak into MARTe output.
	if strings.Contains(res, "Counter: uint32") {
		t.Errorf("build output should use the expanded form, got:\n%s", res)
	}
}

// The sugar must validate like the expanded form: declared signals are
// known to the datasource (no implicit-signal warnings) and carry types.
func TestSignalDefinitionSugarValidation(t *testing.T) {
	dir := t.TempDir()
	src := `#package T
+DDB = {
  Class = GAMDataSource
  Signals = {
    In: uint32
    Out: uint32
  }
}
+Ref = {
  Class = ReferenceContainer
  DefaultDataSource = DDB
}
+GAM = {
  Class = IOGAM
  InputSignals = {
    DDB::In
  }
  OutputSignals = {
    DDB::Out
  }
}
`
	file := filepath.Join(dir, "main.marte")
	os.WriteFile(file, []byte(src), 0644)

	pt := index.NewProjectTree()
	cfg, err := parser.NewParser(src).Parse()
	if err != nil {
		t.Fatal(err)
	}
	pt.AddFile(file, cfg)
	pt.ResolveReferences(nil)
	v := validator.NewValidator(pt, dir, nil)
	v.ValidateProject(context.Background())

	for _, d := range v.Diagnostics {
		if strings.Contains(d.Message, "Implicitly Defined Signal") {
			t.Errorf("declared signal reported as implicit: %s", d.Message)
		}
		if d.Level == validator.LevelError {
			t.Errorf("unexpected error: %s", d.Message)
		}
	}
}

// `mdt fmt` must round-trip the sugar.
func TestSignalDefinitionSugarFormatting(t *testing.T) {
	src := `+DS = {
  Class = GAMDataSource
  Signals = {
    Vector: float32[4]
    Extra: uint32 = {
      Frequency = 100
    }
  }
}`
	cfg, err := parser.NewParser(src).Parse()
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	formatter.Format(cfg, &sb)
	got := sb.String()

	for _, want := range []string{"Vector: float32[4]", "Extra: uint32 = {"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in formatted output, got:\n%s", want, got)
		}
	}
	if _, err := parser.NewParser(got).Parse(); err != nil {
		t.Errorf("formatted output does not re-parse: %v\n%s", err, got)
	}
}
