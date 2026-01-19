package parser

import (
	"fmt"
	"strconv"
	"strings"
)

type Parser struct {
	lexer    *Lexer
	tok      Token
	peeked   bool
	comments []Comment
	pragmas  []Pragma
}

func NewParser(input string) *Parser {
	return &Parser{
		lexer: NewLexer(input),
	}
}

func (p *Parser) next() Token {
	if p.peeked {
		p.peeked = false
		return p.tok
	}
	p.tok = p.fetchToken()
	return p.tok
}

func (p *Parser) peek() Token {
	if p.peeked {
		return p.tok
	}
	p.tok = p.fetchToken()
	p.peeked = true
	return p.tok
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
		// field = value
		name := tok.Value
		if p.next().Type != TokenEqual {
			return nil, fmt.Errorf("%d:%d: expected =", p.tok.Position.Line, p.tok.Position.Column)
		}
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
			return nil, fmt.Errorf("%d:%d: expected =", p.tok.Position.Line, p.tok.Position.Column)
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
