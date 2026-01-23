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
}

func NewParser(input string) *Parser {
	return &Parser{
		lexer: NewLexer(input),
	}
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

		def, err := p.parseDefinition()
		if err != nil {
			return nil, err
		}
		config.Definitions = append(config.Definitions, def)
	}
	config.Comments = p.comments
	config.Pragmas = p.pragmas
	return config, nil
}

func (p *Parser) parseDefinition() (Definition, error) {
	tok := p.next()
	switch tok.Type {
	case TokenIdentifier:
		// Could be Field = Value OR Node = { ... }
		name := tok.Value
		if p.next().Type != TokenEqual {
			return nil, fmt.Errorf("%d:%d: expected =", tok.Position.Line, tok.Position.Column)
		}

		// Disambiguate based on RHS
		nextTok := p.peek()
		if nextTok.Type == TokenLBrace {
			// Check if it looks like a Subnode (contains definitions) or Array (contains values)
			if p.isSubnodeLookahead() {
				sub, err := p.parseSubnode()
				if err != nil {
					return nil, err
				}
				return &ObjectNode{
					Position: tok.Position,
					Name:     name,
					Subnode:  sub,
				}, nil
			}
		}

		// Default to Field
		val, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		return &Field{
			Position: tok.Position,
			Name:     name,
			Value:    val,
		}, nil

	case TokenObjectIdentifier:
		// node = subnode
		name := tok.Value
		if p.next().Type != TokenEqual {
			return nil, fmt.Errorf("%d:%d: expected =", tok.Position.Line, tok.Position.Column)
		}
		sub, err := p.parseSubnode()
		if err != nil {
			return nil, err
		}
		return &ObjectNode{
			Position: tok.Position,
			Name:     name,
			Subnode:  sub,
		}, nil
	default:
		return nil, fmt.Errorf("%d:%d: unexpected token %v", tok.Position.Line, tok.Position.Column, tok.Value)
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

func (p *Parser) parseSubnode() (Subnode, error) {
	tok := p.next()
	if tok.Type != TokenLBrace {
		return Subnode{}, fmt.Errorf("%d:%d: expected {", tok.Position.Line, tok.Position.Column)
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
			return sub, fmt.Errorf("%d:%d: unexpected EOF, expected }", t.Position.Line, t.Position.Column)
		}
		def, err := p.parseDefinition()
		if err != nil {
			return sub, err
		}
		sub.Definitions = append(sub.Definitions, def)
	}
	return sub, nil
}

func (p *Parser) parseValue() (Value, error) {
	tok := p.next()
	switch tok.Type {
	case TokenString:
		return &StringValue{
			Position: tok.Position,
			Value:    strings.Trim(tok.Value, "\""),
			Quoted:   true,
		}, nil

	case TokenNumber:
		// Simplistic handling
		if strings.Contains(tok.Value, ".") || strings.Contains(tok.Value, "e") {
			f, _ := strconv.ParseFloat(tok.Value, 64)
			return &FloatValue{Position: tok.Position, Value: f, Raw: tok.Value}, nil
		}
		i, _ := strconv.ParseInt(tok.Value, 0, 64)
		return &IntValue{Position: tok.Position, Value: i, Raw: tok.Value}, nil
	case TokenBool:
		return &BoolValue{Position: tok.Position, Value: tok.Value == "true"},
			nil
	case TokenIdentifier:
		// reference?
		return &ReferenceValue{Position: tok.Position, Value: tok.Value}, nil
	case TokenLBrace:
		// array
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
			val, err := p.parseValue()
			if err != nil {
				return nil, err
			}
			arr.Elements = append(arr.Elements, val)
		}
		return arr, nil
	default:
		return nil, fmt.Errorf("%d:%d: unexpected value token %v", tok.Position.Line, tok.Position.Column, tok.Value)
	}
}
