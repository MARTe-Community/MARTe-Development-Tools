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
	TokenComma
	TokenColon
	TokenPipe
	TokenLBracket
	TokenRBracket
	TokenSymbol
)

type Token struct {
	Type     TokenType
	Value    string
	Position Position
}

type Lexer struct {
	input       string
	start       int
	pos         int
	width       int
	line        int
	lineStart   int
	startLine   int
	startColumn int
}

func NewLexer(input string) *Lexer {
	return &Lexer{
		input:       input,
		line:        1,
		startLine:   1,
		startColumn: 1,
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
			// We don't perfectly restore lineStart here as it's complex,
			// but we mostly backup single characters within a line.
		}
	}
}

func (l *Lexer) peek() rune {
	r := l.next()
	l.backup()
	return r
}

func (l *Lexer) ignore() {
	l.start = l.pos
	l.startLine = l.line
	l.startColumn = l.pos - l.lineStart + 1
}

func (l *Lexer) emit(t TokenType) Token {
	tok := Token{
		Type:  t,
		Value: l.input[l.start:l.pos],
		Position: Position{
			Line:   l.startLine,
			Column: l.startColumn,
		},
	}
	l.ignore()
	return tok
}

func (l *Lexer) NextToken() Token {
	for {
		r := l.next()
		if r == -1 {
			return l.emit(TokenEOF)
		}

		if unicode.IsSpace(r) {
			l.ignore()
			continue
		}

		switch r {
		case '=':
			return l.emit(TokenEqual)
		case '{':
			return l.emit(TokenLBrace)
		case '}':
			return l.emit(TokenRBrace)
		case ',':
			return l.emit(TokenComma)
		case ':':
			return l.emit(TokenColon)
		case '|':
			return l.emit(TokenPipe)
		case '[':
			return l.emit(TokenLBracket)
		case ']':
			return l.emit(TokenRBracket)
		case '&', '?', '!', '<', '>', '*', '(', ')':
			return l.emit(TokenSymbol)
		case '"':
			return l.lexString()
		case '/':
			return l.lexComment()
		case '#':
			return l.lexHashIdentifier()
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
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' || r == '.' {
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
	for {
		r := l.next()
		if unicode.IsDigit(r) || unicode.IsLetter(r) || r == '.' || r == '-' || r == '+' {
			continue
		}
		l.backup()
		return l.emit(TokenNumber)
	}
}

func (l *Lexer) lexComment() Token {
	r := l.next()
	if r == '/' {
		r = l.next()
		if r == '#' {
			return l.lexUntilNewline(TokenDocstring)
		}
		if r == '!' {
			return l.lexUntilNewline(TokenPragma)
		}
		return l.lexUntilNewline(TokenComment)
	}
	if r == '*' {
		for {
			r := l.next()
			if r == -1 {
				return l.emit(TokenError)
			}
			if r == '*' {
				if l.peek() == '/' {
					l.next() // consume /
					return l.emit(TokenComment)
				}
			}
		}
	}
	l.backup()
	return l.emit(TokenError)
}

func (l *Lexer) lexUntilNewline(t TokenType) Token {
	for {
		r := l.next()
		if r == '\n' {
			l.backup()
			tok := l.emit(t)
			l.next() // consume \n
			l.ignore()
			return tok
		}
		if r == -1 {
			return l.emit(t)
		}
	}
}

func (l *Lexer) lexHashIdentifier() Token {
	// We are at '#', l.start is just before it
	for {
		r := l.next()
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' || r == '.' || r == '#' {
			continue
		}
		l.backup()
		break
	}
	val := l.input[l.start:l.pos]
	if val == "#package" {
		return l.lexUntilNewline(TokenPackage)
	}
	return l.emit(TokenIdentifier)
}
