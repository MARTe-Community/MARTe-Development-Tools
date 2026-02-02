package integration

import (
	"testing"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/formatter"
	"bytes"
)

func TestAdvancedNumbers(t *testing.T) {
	content := `
Hex = 0xFF
HexLower = 0xee
Binary = 0b1011
Decimal = 123
Scientific = 1e-3
`
	p := parser.NewParser(content)
	cfg, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	// Verify values
	foundHex := false
	foundHexLower := false
	foundBinary := false
	for _, def := range cfg.Definitions {
		if f, ok := def.(*parser.Field); ok {
			if f.Name == "Hex" {
				if v, ok := f.Value.(*parser.IntValue); ok {
					if v.Value != 255 {
						t.Errorf("Expected 255 for Hex, got %d", v.Value)
					}
					foundHex = true
				}
			}
			if f.Name == "HexLower" {
				if v, ok := f.Value.(*parser.IntValue); ok {
					if v.Value != 238 {
						t.Errorf("Expected 238 for HexLower, got %d", v.Value)
					}
					foundHexLower = true
				} else {
					t.Errorf("HexLower was parsed as %T, expected *parser.IntValue", f.Value)
				}
			}
			if f.Name == "Binary" {
				if v, ok := f.Value.(*parser.IntValue); ok {
					if v.Value == 11 {
						foundBinary = true
					}
				}
			}
		}
	}
	if !foundHex { t.Error("Hex field not found") }
	if !foundHexLower { t.Error("HexLower field not found") }
	if !foundBinary { t.Error("Binary field not found") }

	// Verify formatting
	var buf bytes.Buffer
	formatter.Format(cfg, &buf)
	formatted := buf.String()
	if !contains(formatted, "Hex = 0xFF") {
		t.Errorf("Formatted content missing Hex = 0xFF:\n%s", formatted)
	}
	if !contains(formatted, "HexLower = 0xee") {
		t.Errorf("Formatted content missing HexLower = 0xee:\n%s", formatted)
	}
	if !contains(formatted, "Binary = 0b1011") {
		t.Errorf("Formatted content missing Binary = 0b1011:\n%s", formatted)
	}
}

func contains(s, substr string) bool {
	return bytes.Contains([]byte(s), []byte(substr))
}