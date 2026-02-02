package integration

import (
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/parser"
)

func TestLexerCoverage(t *testing.T) {
	// 1. Comments
	input := `
// Line comment
/* Block comment */
//# Docstring
//! Pragma
/* Unclosed block
`
	l := parser.NewLexer(input)
	for {
		tok := l.NextToken()
		if tok.Type == parser.TokenEOF {
			break
		}
	}

	// 2. Numbers
	inputNum := `123 12.34 1.2e3 1.2E-3 0xFF`
	lNum := parser.NewLexer(inputNum)
	for {
		tok := lNum.NextToken()
		if tok.Type == parser.TokenEOF {
			break
		}
	}

	// 3. Identifiers
	inputID := `Valid ID with-hyphen _under`
	lID := parser.NewLexer(inputID)
	for {
		tok := lID.NextToken()
		if tok.Type == parser.TokenEOF {
			break
		}
	}
}
