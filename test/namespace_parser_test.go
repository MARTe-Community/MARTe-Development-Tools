package integration

import (
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/parser"
)

func TestNamespaceParsing(t *testing.T) {
	input := `Class = SDN::SDNSubscriber`
	p := parser.NewParser(input)
	config, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	if len(config.Definitions) != 1 {
		t.Fatalf("Expected 1 definition, got %d", len(config.Definitions))
	}

	f, ok := config.Definitions[0].(*parser.Field)
	if !ok {
		t.Fatal("Expected Field")
	}

	if f.Name != "Class" {
		t.Errorf("Expected name Class, got %s", f.Name)
	}

	// Should be ReferenceValue "SDN::SDNSubscriber"
	ref, ok := f.Value.(*parser.ReferenceValue)
	if !ok {
		t.Fatalf("Expected ReferenceValue, got %T", f.Value)
	}

	if ref.Value != "SDN::SDNSubscriber" {
		t.Errorf("Expected 'SDN::SDNSubscriber', got '%s'", ref.Value)
	}
}