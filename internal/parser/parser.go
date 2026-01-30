package parser

import (
	"fmt"
	"strconv"
	"strings"
)

type Parser struct {
	lexer    *Lexer
	buf      []Token
	comments []Comment
	pragmas  []Pragma
	errors   []error
}

func NewParser(input string) *Parser {
	return &Parser{
		lexer: NewLexer(input),
	}
}

func (p *Parser) addError(pos Position, msg string) {
	p.errors = append(p.errors, fmt.Errorf("%d:%d: %s", pos.Line, pos.Column, msg))
}

func (p *Parser) next() Token {
	if len(p.buf) > 0 {
		t := p.buf[0]
		p.buf = p.buf[1:]
		return t
	}
	return p.fetchToken()
}

func (p *Parser) peek() Token {
	return p.peekN(0)
}

func (p *Parser) peekN(n int) Token {
	for len(p.buf) <= n {
		p.buf = append(p.buf, p.fetchToken())
	}
	return p.buf[n]
}

func (p *Parser) fetchToken() Token {
	for {
		tok := p.lexer.NextToken()
		switch tok.Type {
		case TokenComment:
			p.comments = append(p.comments, Comment{Position: tok.Position, Text: tok.Value})
		case TokenDocstring:
			p.comments = append(p.comments, Comment{Position: tok.Position, Text: tok.Value, Doc: true})
		case TokenPragma:
			p.pragmas = append(p.pragmas, Pragma{Position: tok.Position, Text: tok.Value})
		default:
			return tok
		}
	}
}

func (p *Parser) Parse() (*Configuration, error) {
	config := &Configuration{}
	for {
		tok := p.peek()
		if tok.Type == TokenEOF {
			break
		}
		if tok.Type == TokenPackage {
			p.next()
			config.Package = &Package{
				Position: tok.Position,
				URI:      strings.TrimSpace(strings.TrimPrefix(tok.Value, "#package")),
			}
			continue
		}

		def, ok := p.parseDefinition()
		if ok {
			config.Definitions = append(config.Definitions, def)
		} else {
			// Synchronization: skip token if not consumed to make progress
			if p.peek() == tok {
				p.next()
			}
		}
	}
	config.Comments = p.comments
	config.Pragmas = p.pragmas

	var err error
	if len(p.errors) > 0 {
		err = p.errors[0]
	}
	return config, err
}

func (p *Parser) parseDefinition() (Definition, bool) {
	tok := p.next()
	switch tok.Type {
	case TokenIdentifier:
		name := tok.Value
		if name == "#var" {
			return p.parseVariableDefinition(tok)
		}
		if p.peek().Type != TokenEqual {
			p.addError(tok.Position, "expected =")
			return nil, false
		}
		p.next() // Consume =

		nextTok := p.peek()
		if nextTok.Type == TokenLBrace {
			if p.isSubnodeLookahead() {
				sub, ok := p.parseSubnode()
				if !ok {
					return nil, false
				}
				return &ObjectNode{
					Position: tok.Position,
					Name:     name,
					Subnode:  sub,
				}, true
			}
		}

		val, ok := p.parseValue()
		if !ok {
			return nil, false
		}
		return &Field{
			Position: tok.Position,
			Name:     name,
			Value:    val,
		}, true

	case TokenObjectIdentifier:
		name := tok.Value
		if p.peek().Type != TokenEqual {
			p.addError(tok.Position, "expected =")
			return nil, false
		}
		p.next() // Consume =

		sub, ok := p.parseSubnode()
		if !ok {
			return nil, false
		}
		return &ObjectNode{
			Position: tok.Position,
			Name:     name,
			Subnode:  sub,
		}, true
	default:
		p.addError(tok.Position, fmt.Sprintf("unexpected token %v", tok.Value))
		return nil, false
	}
}

func (p *Parser) isSubnodeLookahead() bool {
	// We are before '{'.
	// Look inside:
	// peek(0) is '{'
	// peek(1) is first token inside

	t1 := p.peekN(1)
	if t1.Type == TokenRBrace {
		// {} -> Empty. Assume Array (Value) by default, unless forced?
		// If we return false, it parses as ArrayValue.
		// If user writes "Sig = {}", is it an empty signal?
		// Empty array is more common for value.
		// If "Sig" is a node, it should probably have content or use +Sig.
		return false
	}

	if t1.Type == TokenIdentifier {
		// Identifier inside.
		// If followed by '=', it's a definition -> Subnode.
		t2 := p.peekN(2)
		if t2.Type == TokenEqual {
			return true
		}
		// Identifier alone or followed by something else -> Reference/Value -> Array
		return false
	}

	if t1.Type == TokenObjectIdentifier {
		// +Node = ... -> Definition -> Subnode
		return true
	}

	// Literals -> Array
	return false
}

func (p *Parser) parseSubnode() (Subnode, bool) {
	tok := p.next()
	if tok.Type != TokenLBrace {
		p.addError(tok.Position, "expected {")
		return Subnode{}, false
	}
	sub := Subnode{Position: tok.Position}
	for {
		t := p.peek()
		if t.Type == TokenRBrace {
			endTok := p.next()
			sub.EndPosition = endTok.Position
			break
		}
		if t.Type == TokenEOF {
			p.addError(t.Position, "unexpected EOF, expected }")
			sub.EndPosition = t.Position
			return sub, true
		}
		def, ok := p.parseDefinition()
		if ok {
			sub.Definitions = append(sub.Definitions, def)
		} else {
			if p.peek() == t {
				p.next()
			}
		}
	}
	return sub, true
}

func (p *Parser) parseValue() (Value, bool) {
	return p.parseExpression(0)
}

func getPrecedence(t TokenType) int {
	switch t {
	case TokenStar, TokenSlash, TokenPercent:
		return 5
	case TokenPlus, TokenMinus:
		return 4
	case TokenConcat:
		return 3
	case TokenAmpersand:
		return 2
	case TokenPipe, TokenCaret:
		return 1
	default:
		return 0
	}
}

func (p *Parser) parseExpression(minPrecedence int) (Value, bool) {
	left, ok := p.parseAtom()
	if !ok {
		return nil, false
	}

	for {
		t := p.peek()
		prec := getPrecedence(t.Type)
		if prec == 0 || prec <= minPrecedence {
			break
		}
		p.next()

		right, ok := p.parseExpression(prec)
		if !ok {
			return nil, false
		}

		left = &BinaryExpression{
			Position: left.Pos(),
			Left:     left,
			Operator: t,
			Right:    right,
		}
	}
	return left, true
}

func (p *Parser) parseAtom() (Value, bool) {
	tok := p.next()
	switch tok.Type {
	case TokenString:
		return &StringValue{
			Position: tok.Position,
			Value:    strings.Trim(tok.Value, "\""),
			Quoted:   true,
		}, true

	case TokenNumber:
		if strings.Contains(tok.Value, ".") || strings.Contains(tok.Value, "e") {
			f, _ := strconv.ParseFloat(tok.Value, 64)
			return &FloatValue{Position: tok.Position, Value: f, Raw: tok.Value}, true
		}
		i, _ := strconv.ParseInt(tok.Value, 0, 64)
		return &IntValue{Position: tok.Position, Value: i, Raw: tok.Value}, true
	case TokenBool:
		return &BoolValue{Position: tok.Position, Value: tok.Value == "true"},
			true
	case TokenIdentifier:
		return &ReferenceValue{Position: tok.Position, Value: tok.Value}, true
	case TokenVariableReference:
		return &VariableReferenceValue{Position: tok.Position, Name: tok.Value}, true
	case TokenLBrace:
		arr := &ArrayValue{Position: tok.Position}
		for {
			t := p.peek()
			if t.Type == TokenRBrace {
				endTok := p.next()
				arr.EndPosition = endTok.Position
				break
			}
			if t.Type == TokenComma {
				p.next()
				continue
			}
			val, ok := p.parseValue()
			if !ok {
				return nil, false
			}
			arr.Elements = append(arr.Elements, val)
		}
		return arr, true
	default:
		p.addError(tok.Position, fmt.Sprintf("unexpected value token %v", tok.Value))
		return nil, false
	}
}

func (p *Parser) parseVariableDefinition(startTok Token) (Definition, bool) {
	nameTok := p.next()
	if nameTok.Type != TokenIdentifier {
		p.addError(nameTok.Position, "expected variable name")
		return nil, false
	}

	if p.next().Type != TokenColon {
		p.addError(nameTok.Position, "expected :")
		return nil, false
	}

	var typeTokens []Token
	startLine := nameTok.Position.Line

	for {
		t := p.peek()
		if t.Position.Line > startLine || t.Type == TokenEOF {
			break
		}
		if t.Type == TokenEqual {
			if p.peekN(1).Type == TokenSymbol && p.peekN(1).Value == "~" {
				p.next()
				p.next()
				typeTokens = append(typeTokens, Token{Type: TokenSymbol, Value: "=~", Position: t.Position})
				continue
			}
			break
		}
		typeTokens = append(typeTokens, p.next())
	}

	typeExpr := ""
	for _, t := range typeTokens {
		typeExpr += t.Value + " "
	}

	var defVal Value
	if p.peek().Type == TokenEqual {
		p.next()
		val, ok := p.parseValue()
		if ok {
			defVal = val
		} else {
			return nil, false
		}
	}

	return &VariableDefinition{
		Position:     startTok.Position,
		Name:         nameTok.Value,
		TypeExpr:     strings.TrimSpace(typeExpr),
		DefaultValue: defVal,
	}, true
}
