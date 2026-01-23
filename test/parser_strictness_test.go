package integration

import (
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/parser"
)

func TestParserStrictness(t *testing.T) {
	// Case 1: content not a definition (missing =)
	invalidDef := `
A = {
 Field = 10
 XXX
}
`
	p := parser.NewParser(invalidDef)
	_, err := p.Parse()
	if err == nil {
		t.Error("Expected error for invalid definition XXX, got nil")
	}

	// Case 2: Missing closing bracket
	missingBrace := `
A = {
 SUBNODE = {
   FIELD = 10
}
`
	p2 := parser.NewParser(missingBrace)
	_, err2 := p2.Parse()
	if err2 == nil {
		t.Error("Expected error for missing closing bracket, got nil")
	}
}
