package parser

import (
	"testing"
)

func TestParseBasic(t *testing.T) {
	input := `
#package PROJECT.SUB
// comment
+Node1 = {
    Class = MyClass
    Field1 = "value"
    Field2 = 123
    Field3 = true
    +SubNode = {
        Class = OtherClass
    }
}
$Node2 = {
    Class = AppClass
    Array = {1 2 3}
}
`
	p := NewParser(input)
	config, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	if config.Package == nil || config.Package.URI != "PROJECT.SUB" {
		t.Errorf("Expected package PROJECT.SUB, got %v", config.Package)
	}

	if len(config.Definitions) != 2 {
		t.Errorf("Expected 2 definitions, got %d", len(config.Definitions))
	}
}
