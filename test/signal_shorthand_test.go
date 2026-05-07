package integration

import (
	"os"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/builder"
	"github.com/marte-community/marte-dev-tools/internal/formatter"
	"github.com/marte-community/marte-dev-tools/internal/parser"
)

func TestSignalShorthandParser(t *testing.T) {
	src := `
+InputSignals = {
  MyDS::Signal1: float32
  MyDS::Signal2: uint32[4]
  MyDS::Signal3: float64[1] = {
    Alias = RealSignal
  }
  MyDS::Signal4
}
`
	p := parser.NewParser(src)
	cfg, err := p.Parse()
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	// Should have one object node (+InputSignals)
	if len(cfg.Definitions) != 1 {
		t.Fatalf("expected 1 definition, got %d", len(cfg.Definitions))
	}
	obj, ok := cfg.Definitions[0].(*parser.ObjectNode)
	if !ok {
		t.Fatal("expected ObjectNode")
	}
	defs := obj.Subnode.Definitions
	if len(defs) != 4 {
		t.Fatalf("expected 4 signal shorthands, got %d", len(defs))
	}

	// Signal1: type only
	s1, ok := defs[0].(*parser.SignalShorthand)
	if !ok {
		t.Fatalf("def[0] is %T, want *parser.SignalShorthand", defs[0])
	}
	if s1.DataSource != "MyDS" || s1.SignalName != "Signal1" || s1.Type != "float32" || s1.NumElements != nil || s1.HasExtraFields {
		t.Errorf("Signal1 mismatch: %+v", s1)
	}

	// Signal2: type + dim
	s2, ok := defs[1].(*parser.SignalShorthand)
	if !ok {
		t.Fatalf("def[1] is %T, want *parser.SignalShorthand", defs[1])
	}
	if s2.DataSource != "MyDS" || s2.SignalName != "Signal2" || s2.Type != "uint32" || s2.NumElements == nil {
		t.Errorf("Signal2 mismatch: %+v", s2)
	}

	// Signal3: type + dim + extra fields
	s3, ok := defs[2].(*parser.SignalShorthand)
	if !ok {
		t.Fatalf("def[2] is %T, want *parser.SignalShorthand", defs[2])
	}
	if s3.DataSource != "MyDS" || s3.SignalName != "Signal3" || !s3.HasExtraFields {
		t.Errorf("Signal3 mismatch: %+v", s3)
	}
	if len(s3.ExtraFields.Definitions) != 1 {
		t.Errorf("Signal3 extra fields: expected 1, got %d", len(s3.ExtraFields.Definitions))
	}

	// Signal4: bare (no type, no dim, no extra)
	s4, ok := defs[3].(*parser.SignalShorthand)
	if !ok {
		t.Fatalf("def[3] is %T, want *parser.SignalShorthand", defs[3])
	}
	if s4.DataSource != "MyDS" || s4.SignalName != "Signal4" || s4.Type != "" || s4.NumElements != nil || s4.HasExtraFields {
		t.Errorf("Signal4 mismatch: %+v", s4)
	}
}

func TestSignalShorthandFormatter(t *testing.T) {
	src := `+InputSignals = {
  MyDS::Signal1: float32
  MyDS::Signal2: uint32[4]
  MyDS::Signal3: float64[1] = {
    Alias = RealSignal
  }
  MyDS::Signal4
}
`
	p := parser.NewParser(src)
	cfg, err := p.Parse()
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	var sb strings.Builder
	formatter.Format(cfg, &sb)
	result := sb.String()

	t.Logf("Formatter output:\n%s", result)

	if !strings.Contains(result, "MyDS::Signal1: float32") {
		t.Error("expected Signal1 shorthand in output")
	}
	if !strings.Contains(result, "MyDS::Signal2: uint32[4]") {
		t.Error("expected Signal2 shorthand in output")
	}
	if !strings.Contains(result, "MyDS::Signal3: float64[1] = {") {
		t.Error("expected Signal3 shorthand in output")
	}
	if !strings.Contains(result, "Alias = RealSignal") {
		t.Error("expected Alias field in Signal3")
	}
	if !strings.Contains(result, "MyDS::Signal4") {
		t.Error("expected Signal4 shorthand in output")
	}
}

func TestSignalShorthandAliasParser(t *testing.T) {
	src := `
+InputSignals = {
  MyDS::RawSignal: float32 as LocalName
  MyDS::RawSignal2: uint32[4] as LocalName2 = {
    Gain = 1.0
  }
}
`
	p := parser.NewParser(src)
	cfg, err := p.Parse()
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	obj := cfg.Definitions[0].(*parser.ObjectNode)
	defs := obj.Subnode.Definitions

	s1 := defs[0].(*parser.SignalShorthand)
	if s1.DataSource != "MyDS" || s1.SignalName != "RawSignal" || s1.Type != "float32" || s1.AliasName != "LocalName" {
		t.Errorf("s1 mismatch: %+v", s1)
	}

	s2 := defs[1].(*parser.SignalShorthand)
	if s2.DataSource != "MyDS" || s2.SignalName != "RawSignal2" || s2.AliasName != "LocalName2" || !s2.HasExtraFields {
		t.Errorf("s2 mismatch: %+v", s2)
	}
}

func TestSignalShorthandAliasFormatter(t *testing.T) {
	src := `+InputSignals = {
  MyDS::RawSignal: float32 as LocalName
  MyDS::RawSignal2: uint32[4] as LocalName2 = {
    Gain = 1.0
  }
}
`
	p := parser.NewParser(src)
	cfg, err := p.Parse()
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	var sb strings.Builder
	formatter.Format(cfg, &sb)
	result := sb.String()
	t.Logf("Formatter output:\n%s", result)

	if !strings.Contains(result, "MyDS::RawSignal: float32 as LocalName") {
		t.Error("expected 'as LocalName' in formatter output")
	}
	if !strings.Contains(result, "MyDS::RawSignal2: uint32[4] as LocalName2 = {") {
		t.Error("expected 'as LocalName2 = {' in formatter output")
	}
}

func TestSignalShorthandAliasBuilder(t *testing.T) {
	content := `
+InputSignals = {
  DS1::RawSig: float32 as MySig
  DS1::RawSig2: uint32[4] as MySig2 = {
    Gain = 1.0
  }
}
`
	tmpFile, err := os.CreateTemp("", "shorthand_alias*.marte")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.WriteString(content)
	tmpFile.Close()

	b := builder.NewBuilder([]string{tmpFile.Name()}, nil)
	out, err := os.CreateTemp("", "out*.cfg")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(out.Name())
	if err := b.Build(out); err != nil {
		t.Fatalf("build error: %v", err)
	}
	out.Close()

	result, _ := os.ReadFile(out.Name())
	res := string(result)
	t.Logf("Builder output:\n%s", res)

	// Node name should be the alias name
	if !strings.Contains(res, "MySig = {") {
		t.Error("expected '+MySig = {' in output")
	}
	if !strings.Contains(res, "MySig2 = {") {
		t.Error("expected '+MySig2 = {' in output")
	}
	// DataSource and Alias fields
	if !strings.Contains(res, "DataSource = DS1") {
		t.Error("expected 'DataSource = DS1'")
	}
	if !strings.Contains(res, "Alias = RawSig") {
		t.Error("expected 'Alias = RawSig'")
	}
	if !strings.Contains(res, "Alias = RawSig2") {
		t.Error("expected 'Alias = RawSig2'")
	}
	if !strings.Contains(res, "Type = float32") {
		t.Error("expected 'Type = float32'")
	}
	if !strings.Contains(res, "Gain = 1.0") {
		t.Error("expected 'Gain = 1.0'")
	}
}

func TestSignalShorthandBuilder(t *testing.T) {
	// This test verifies that shorthand syntax is expanded correctly to
	// standard MARTe2 signal blocks by the builder.
	content := `
+InputSignals = {
  DS1::Sig1: float32
  DS1::Sig2: uint32[4]
  DS1::Sig3: float64[1] = {
    Alias = RealSignal
  }
}
`
	tmpFile, err := os.CreateTemp("", "shorthand*.marte")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	if _, err := tmpFile.WriteString(content); err != nil {
		t.Fatal(err)
	}
	tmpFile.Close()

	b := builder.NewBuilder([]string{tmpFile.Name()}, nil)
	out, err := os.CreateTemp("", "out*.cfg")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(out.Name())

	// Build may have validation warnings for incomplete file; that's OK.
	// We call b.Build directly, which skips the CLI's os.Exit(1).
	if err := b.Build(out); err != nil {
		t.Fatalf("build error: %v", err)
	}
	out.Close()

	result, err := os.ReadFile(out.Name())
	if err != nil {
		t.Fatal(err)
	}
	res := string(result)
	t.Logf("Builder output:\n%s", res)

	// Verify standard MARTe2 signal blocks were emitted
	if !strings.Contains(res, "DataSource = DS1") {
		t.Error("expected DataSource = DS1 in output")
	}
	if !strings.Contains(res, "Type = float32") {
		t.Error("expected Type = float32 in output")
	}
	if !strings.Contains(res, "Type = uint32") {
		t.Error("expected Type = uint32 in output")
	}
	if !strings.Contains(res, "NumberOfElements = 4") {
		t.Error("expected NumberOfElements = 4 in output")
	}
	if !strings.Contains(res, "Alias = RealSignal") {
		t.Error("expected Alias = RealSignal in output")
	}
}
