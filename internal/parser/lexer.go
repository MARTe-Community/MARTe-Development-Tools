package parser

import (
	"unicode"
	"unicode/utf8"
)

type TokenType int

const (
	TokenError TokenType = iota
	TokenEOF
	TokenIdentifier
	TokenObjectIdentifier // +$
	TokenEqual
	TokenLBrace
	TokenRBrace
	TokenString
	TokenNumber
	TokenBool
	TokenPackage
	TokenPragma
	TokenComment
	TokenDocstring
)

type Token struct {
	Type     TokenType
	Value    string
	Position Position
}

type Lexer struct {
	input    string
	start    int
	pos      int
	width    int
	line     int
	lineStart int
}

func NewLexer(input string) *Lexer {
	return &Lexer{
		input: input,
		line:  1,
	}
}

func (l *Lexer) next() rune {
	if l.pos >= len(l.input) {
		l.width = 0
		return -1
	}
	r, w := utf8.DecodeRuneInString(l.input[l.pos:])
	l.width = w
	l.pos += l.width
	if r == '\n' {
		l.line++
		l.lineStart = l.pos
	}
	return r
}

func (l *Lexer) backup() {
	l.pos -= l.width
	if l.width > 0 {
		r, _ := utf8.DecodeRuneInString(l.input[l.pos:])
		if r == '\n' {
			l.line--
			// This is tricky, we'd need to find the previous line start
			// For simplicity, let's just not backup over newlines or handle it better
		}
	}
}

func (l *Lexer) peek() rune {
	r := l.next()
	l.backup()
	return r
}

func (l *Lexer) emit(t TokenType) Token {
	tok := Token{
		Type: t,
		Value: l.input[l.start:l.pos],
		Position: Position{
			Line:   l.line,
			Column: l.start - l.lineStart + 1,
		},
	}
	l.start = l.pos
	return tok
}

func (l *Lexer) NextToken() Token {
	for {
		r := l.next()
		if r == -1 {
			return l.emit(TokenEOF)
		}

		if unicode.IsSpace(r) {
			l.start = l.pos
			continue
		}

		switch r {
		case '=':
			return l.emit(TokenEqual)
		case '{':
			return l.emit(TokenLBrace)
		case '}':
			return l.emit(TokenRBrace)
		case '"':
			return l.lexString()
		case '/':
			return l.lexComment()
		case '#':
			return l.lexPackage()
		case '!':
			// Might be part of pragma //! 
			// But grammar says pragma is //!
			// So it should start with //
		case '+':
			fallthrough
		case '$':
			return l.lexObjectIdentifier()
		}

		if unicode.IsLetter(r) {
			return l.lexIdentifier()
		}

		if unicode.IsDigit(r) || r == '-' {
			return l.lexNumber()
		}

		return l.emit(TokenError)
	}
}

func (l *Lexer) lexIdentifier() Token {
	for {
		r := l.next()
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' {
			continue
		}
		l.backup()
		val := l.input[l.start:l.pos]
		if val == "true" || val == "false" {
			return l.emit(TokenBool)
		}
		return l.emit(TokenIdentifier)
	}
}

func (l *Lexer) lexObjectIdentifier() Token {
	for {
		r := l.next()
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' {
			continue
		}
		l.backup()
		return l.emit(TokenObjectIdentifier)
	}
}

func (l *Lexer) lexString() Token {
	for {
		r := l.next()
		if r == '"' {
			return l.emit(TokenString)
		}
		if r == -1 {
			return l.emit(TokenError)
		}
	}
}

func (l *Lexer) lexNumber() Token {
	// Simple number lexing, could be improved for hex, binary, float
	for {
		r := l.next()
		if unicode.IsDigit(r) || r == '.' || r == 'x' || r == 'b' || r == 'e' || r == '-' {
			continue
		}
		l.backup()
		return l.emit(TokenNumber)
	}
}

func (l *Lexer) lexComment() Token {
	r := l.next()
	if r == '/' {
		// It's a comment, docstring or pragma
		r = l.next()
		if r == '#' {
			return l.lexUntilNewline(TokenDocstring)
		}
		if r == '!' {
			return l.lexUntilNewline(TokenPragma)
		}
		return l.lexUntilNewline(TokenComment)
	}
	l.backup()
	return l.emit(TokenError)
}

func (l *Lexer) lexUntilNewline(t TokenType) Token {
	for {
		r := l.next()
		if r == '\n' || r == -1 {
			return l.emit(t)
		}
	}
}

func (l *Lexer) lexPackage() Token {
	// #package
	l.start = l.pos - 1 // Include '#'
	for {
		r := l.next()
		if unicode.IsLetter(r) {
			continue
		}
		l.backup()
		break
	}
	if l.input[l.start:l.pos] == "#package" {
		return l.lexUntilNewline(TokenPackage)
	}
	return l.emit(TokenError)
}
