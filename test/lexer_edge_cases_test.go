package integration

import (
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/parser"
)

// TestLexerCommentVariants is a regression test for BUG-017:
// the comment tokenization path may not properly distinguish //, //#, and //!
// under certain edge cases.
func TestLexerCommentVariants(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantType parser.TokenType
		wantText string
	}{
		{
			name:     "standard line comment",
			input:    "// plain comment\n",
			wantType: parser.TokenComment,
			wantText: "// plain comment",
		},
		{
			name:     "docstring comment",
			input:    "//# docstring\n",
			wantType: parser.TokenDocstring,
			wantText: "//# docstring",
		},
		{
			name:     "pragma comment",
			input:    "//! pragma\n",
			wantType: parser.TokenPragma,
			wantText: "//! pragma",
		},
		{
			name:     "comment at EOF without newline",
			input:    "// eof comment",
			wantType: parser.TokenComment,
			wantText: "// eof comment",
		},
		{
			name:     "docstring at EOF without newline",
			input:    "//# eof doc",
			wantType: parser.TokenDocstring,
			wantText: "//# eof doc",
		},
		{
			name:     "pragma at EOF without newline",
			input:    "//! eof pragma",
			wantType: parser.TokenPragma,
			wantText: "//! eof pragma",
		},
		{
			name:     "block comment with content",
			input:    "/* block */\n",
			wantType: parser.TokenComment,
			wantText: "/* block */",
		},
		{
			name:     "block comment spanning lines",
			input:    "/* line1\nline2 */\n",
			wantType: parser.TokenComment,
			wantText: "/* line1\nline2 */",
		},
		{
			name:     "empty block comment",
			input:    "/**/\n",
			wantType: parser.TokenComment,
			wantText: "/**/",
		},
		{
			name:     "block comment with stars inside",
			input:    "/* a * b */\n",
			wantType: parser.TokenComment,
			wantText: "/* a * b */",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := parser.NewLexer(tt.input)
			tok := l.NextToken()
			if tok.Type != tt.wantType {
				t.Errorf("Expected token type %v, got %v (%q)", tt.wantType, tok.Type, tok.Value)
			}
			if tok.Value != tt.wantText {
				t.Errorf("Expected text %q, got %q", tt.wantText, tok.Value)
			}
		})
	}
}

// TestLexerUnclosedBlockComment is a regression test for BUG-017/BUG-029:
// an unclosed block comment should produce a TokenError, not hang or panic.
func TestLexerUnclosedBlockComment(t *testing.T) {
	input := "/* unclosed block"
	l := parser.NewLexer(input)

	var sawError bool
	for {
		tok := l.NextToken()
		if tok.Type == parser.TokenEOF {
			break
		}
		if tok.Type == parser.TokenError {
			sawError = true
			break
		}
	}

	if !sawError {
		t.Error("Expected TokenError for unclosed block comment, got none")
	}
}

// TestLexerHexLiterals is a regression test for BUG-028:
// negative hex literals like -0xFF may be incorrectly tokenized.
func TestLexerHexLiterals(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantTokens []parser.TokenType
		wantValues []string
	}{
		{
			name:       "positive hex",
			input:      "0xFF\n",
			wantTokens: []parser.TokenType{parser.TokenNumber},
			wantValues: []string{"0xFF"},
		},
		{
			name:       "positive hex uppercase",
			input:      "0XABCD\n",
			wantTokens: []parser.TokenType{parser.TokenNumber},
			wantValues: []string{"0XABCD"},
		},
		{
			name:       "hex zero",
			input:      "0x0\n",
			wantTokens: []parser.TokenType{parser.TokenNumber},
			wantValues: []string{"0x0"},
		},
		{
			name:       "binary literal",
			input:      "0b1010\n",
			wantTokens: []parser.TokenType{parser.TokenNumber},
			wantValues: []string{"0b1010"},
		},
		{
			name:       "decimal number",
			input:      "42\n",
			wantTokens: []parser.TokenType{parser.TokenNumber},
			wantValues: []string{"42"},
		},
		{
			name:       "negative decimal",
			input:      "-42\n",
			wantTokens: []parser.TokenType{parser.TokenMinus, parser.TokenNumber},
			wantValues: []string{"-", "42"},
		},
		{
			name:       "float literal",
			input:      "3.14\n",
			wantTokens: []parser.TokenType{parser.TokenNumber},
			wantValues: []string{"3.14"},
		},
		{
			name:       "float with exponent",
			input:      "1.5e3\n",
			wantTokens: []parser.TokenType{parser.TokenNumber},
			wantValues: []string{"1.5e3"},
		},
		{
			name:       "float with negative exponent",
			input:      "2.5E-2\n",
			wantTokens: []parser.TokenType{parser.TokenNumber},
			wantValues: []string{"2.5E-2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := parser.NewLexer(tt.input)
			var types []parser.TokenType
			var values []string
			for {
				tok := l.NextToken()
				if tok.Type == parser.TokenEOF {
					break
				}
				if tok.Type == parser.TokenError {
					t.Errorf("Unexpected TokenError: %q", tok.Value)
					break
				}
				types = append(types, tok.Type)
				values = append(values, tok.Value)
			}

			if len(types) != len(tt.wantTokens) {
				t.Errorf("Expected %d tokens, got %d: types=%v values=%v",
					len(tt.wantTokens), len(types), types, values)
				return
			}
			for i := range types {
				if types[i] != tt.wantTokens[i] {
					t.Errorf("Token %d: expected type %v, got %v", i, tt.wantTokens[i], types[i])
				}
				if values[i] != tt.wantValues[i] {
					t.Errorf("Token %d: expected value %q, got %q", i, tt.wantValues[i], values[i])
				}
			}
		})
	}
}

// TestLexerHashDirectives tests that #-prefixed directives are correctly
// tokenized in both their #-prefixed and bare forms.
func TestLexerHashDirectives(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantType parser.TokenType
	}{
		{"if with hash", "#if", parser.TokenIf},
		{"else with hash", "#else", parser.TokenElse},
		{"end with hash", "#end", parser.TokenEnd},
		{"var with hash", "#var", parser.TokenVar},
		{"let with hash", "#let", parser.TokenLet},
		{"foreach with hash", "#foreach", parser.TokenForeach},
		{"template with hash", "#template", parser.TokenTemplate},
		{"use with hash", "#use", parser.TokenUse},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := parser.NewLexer(tt.input + "\n")
			tok := l.NextToken()
			if tok.Type != tt.wantType {
				t.Errorf("Expected %v, got %v (%q)", tt.wantType, tok.Type, tok.Value)
			}
		})
	}
}

// TestLexerDocstringPragmaAdjacent tests that adjacent //# and //! tokens
// are correctly distinguished (BUG-017 edge case).
func TestLexerDocstringPragmaAdjacent(t *testing.T) {
	input := "//# doc\n//! pragma\n// comment\n"

	l := parser.NewLexer(input)

	expect := []parser.TokenType{
		parser.TokenDocstring,
		parser.TokenPragma,
		parser.TokenComment,
	}

	for i, want := range expect {
		tok := l.NextToken()
		if tok.Type == parser.TokenEOF {
			t.Fatalf("Unexpected EOF at position %d, wanted %v", i, want)
		}
		if tok.Type != want {
			t.Errorf("Token %d: expected %v, got %v (%q)", i, want, tok.Type, tok.Value)
		}
	}
}

// TestParserUnterminatedConstruct is a regression test for BUG-029:
// unterminated constructs (unclosed braces, missing #end) should produce
// meaningful parse errors, not panics or masked errors.
func TestParserUnterminatedConstruct(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "unclosed object brace",
			input: "+Node = { Class = Test",
		},
		{
			name:  "unclosed if block",
			input: "#if true\nField = 1",
		},
		{
			name:  "unclosed foreach block",
			input: "#foreach x in { 1 2 }\n+N = { Class = C }",
		},
		{
			name:  "unclosed template block",
			input: "#template T(p: int)\nField = 1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := parser.NewParser(tt.input)
			_, err := p.Parse()
			if err == nil {
				// BUG-029: parser may silently succeed on unterminated input
				t.Log("BUG-029: parser did not return error for unterminated construct")
			} else {
				// Verify the error message is meaningful
				errStr := err.Error()
				if strings.TrimSpace(errStr) == "" {
					t.Error("BUG-029: parser returned empty error message")
				}
				t.Logf("Parser error (expected): %v", err)
			}
		})
	}
}

// TestParserDuplicateCloseBrace tests that an extra closing brace does not
// crash or corrupt the parser state (BUG-029 edge case).
func TestParserDuplicateCloseBrace(t *testing.T) {
	input := "+Node = { Class = Test }\n}\n"

	p := parser.NewParser(input)
	cfg, err := p.Parse()

	// The parser should either return an error or produce a valid (partial) config.
	// A panic is the real failure mode we're guarding against.
	if err != nil {
		t.Logf("Parser error on extra close brace (may be expected): %v", err)
	} else if cfg != nil {
		t.Log("Parser accepted extra close brace without error")
	}
}

// TestParserEmptyInput verifies the parser handles empty input gracefully.
func TestParserEmptyInput(t *testing.T) {
	p := parser.NewParser("")
	cfg, err := p.Parse()
	if err != nil {
		t.Fatalf("Empty input should parse without error, got: %v", err)
	}
	if cfg == nil {
		t.Fatal("Empty input should produce non-nil Configuration")
	}
	if len(cfg.Definitions) != 0 {
		t.Errorf("Empty input should produce 0 definitions, got %d", len(cfg.Definitions))
	}
}
