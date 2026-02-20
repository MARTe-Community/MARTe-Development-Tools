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
	tok := p.peek()
	switch tok.Type {
	case TokenLet:
		p.next()
		return p.parseLet(tok)
	case TokenVar:
		p.next()
		return p.parseVariableDefinition(tok)
	case TokenIf:
		p.next()
		return p.parseIf(tok)
	case TokenForeach:
		p.next()
		return p.parseForeach(tok)
	case TokenTemplate:
		p.next()
		return p.parseTemplate(tok)
	case TokenUse:
		p.next()
		return p.parseUse(tok)
	case TokenIdentifier:
		p.next()
		name := tok.Value
		
		// If followed by =, it's a definition
		if p.peek().Type == TokenEqual {
			p.next() // consume =
			
			if p.peek().Type == TokenLBrace && p.isSubnodeLookahead() {
				sub, ok := p.parseSubnode()
				if !ok {
					return nil, false
				}
				return &ObjectNode{
					Position: tok.Position,
					Name:     &ReferenceValue{Position: tok.Position, Value: name},
					Subnode:  sub,
				}, true
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
		}
		
		// If not followed by =, it might be an expression start?
		// But parseDefinition expects a definition.
		// Fallback to default if we want to support "A + B = C"
		// But for now, let's stick to simple identifiers or fail.
		p.addError(p.peek().Position, "expected =")
		return nil, false

	case TokenObjectIdentifier:
		p.next()
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
			Name:     &ReferenceValue{Position: tok.Position, Value: name},
			Subnode:  sub,
		}, true
	default:
		// Attempt to parse name (could be expression)
		nameVal, ok := p.parseValue()
		if !ok {
			return nil, false
		}

		if p.peek().Type != TokenEqual {
			// If not followed by =, it might be a naked expression (invalid as definition)
			// or part of a template use if we messed up.
			p.addError(p.peek().Position, fmt.Sprintf("expected =, got %v", p.peek().Value))
			return nil, false
		}
		p.next() // consume =

		if p.peek().Type == TokenLBrace && p.isSubnodeLookahead() {
			sub, ok := p.parseSubnode()
			if !ok {
				return nil, false
			}
			return &ObjectNode{
				Position: nameVal.Pos(),
				Name:     nameVal,
				Subnode:  sub,
			}, true
		}

		val, ok := p.parseValue()
		if !ok {
			return nil, false
		}

		// If it's a simple Field, Name must be string
		fieldName := ""
		if ref, ok := nameVal.(*ReferenceValue); ok {
			fieldName = ref.Value
		} else if str, ok := nameVal.(*StringValue); ok {
			fieldName = str.Value
		} else {
			// It might be a complex expression name for an object, but if no { follows, it's weird.
			// However, MARTe allows "Name" .. "Suffix" = Value for fields too?
			// Let's assume field names can also be expressions if we want to be powerful.
			// But for now, let's just use the string value if it's a constant.
			// Actually, let's just use a placeholder or handle it in builder.
			fieldName = "EXPR_FIELD" 
		}

		return &Field{
			Position: nameVal.Pos(),
			Name:     fieldName,
			Value:    val,
		}, true
	}
}

func (p *Parser) parseIf(startTok Token) (Definition, bool) {
	cond, ok := p.parseValue()
	if !ok {
		return nil, false
	}

	if p.peek().Type == TokenLBrace {
		p.next() // consume {
	}

	thenBody, endTok, ok := p.parseBlock()
	if !ok {
		return nil, false
	}

	var elseBody []Definition
	if endTok.Type == TokenElse {
		if p.peek().Type == TokenLBrace {
			p.next() // consume {
		}
		elseBody, endTok, ok = p.parseBlock()
		if !ok {
			return nil, false
		}
	}

	if endTok.Type != TokenEnd {
		p.addError(endTok.Position, "expected #end")
	}

	return &IfBlock{
		Position:    startTok.Position,
		EndPosition: endTok.Position,
		Condition:   cond,
		Then:        thenBody,
		Else:        elseBody,
	}, true
}

func (p *Parser) parseForeach(startTok Token) (Definition, bool) {
	// #foreach Value in Array
	// #foreach Key Value in Map
	v1Tok := p.next()
	if v1Tok.Type != TokenIdentifier {
		p.addError(v1Tok.Position, "expected identifier in #foreach")
		return nil, false
	}

	var keyVar, valueVar string
	next := p.peek()
	if next.Type == TokenIdentifier {
		p.next()
		keyVar = v1Tok.Value
		valueVar = next.Value
	} else {
		valueVar = v1Tok.Value
	}

	if p.next().Type != TokenIn {
		p.addError(p.peek().Position, "expected 'in' in #foreach")
		return nil, false
	}

	iterable, ok := p.parseValue()
	if !ok {
		return nil, false
	}

	if p.peek().Type == TokenLBrace {
		p.next() // consume {
	}

	body, endTok, ok := p.parseBlock()
	if !ok {
		return nil, false
	}

	if endTok.Type != TokenEnd {
		p.addError(endTok.Position, "expected #end")
	}

	return &ForeachBlock{
		Position:    startTok.Position,
		EndPosition: endTok.Position,
		KeyVar:      keyVar,
		ValueVar:    valueVar,
		Iterable:    iterable,
		Body:        body,
	}, true
}

func (p *Parser) parseTemplate(startTok Token) (Definition, bool) {
	nameTok := p.next()
	if nameTok.Type != TokenIdentifier {
		p.addError(nameTok.Position, "expected template name")
		return nil, false
	}

	var params []TemplateParameter
	if p.peek().Type == TokenSymbol && p.peek().Value == "(" {
		p.next() // consume (
		for {
			if p.peek().Type == TokenSymbol && p.peek().Value == ")" {
				p.next()
				break
			}
			paramName := p.next()
			if paramName.Type != TokenIdentifier {
				p.addError(paramName.Position, "expected parameter name")
				return nil, false
			}
			if p.next().Type != TokenColon {
				p.addError(p.peek().Position, "expected :")
				return nil, false
			}
			// Parse type expression (simplified until =)
			typeExpr := ""
			for {
				t := p.peek()
				if t.Type == TokenEOF || t.Type == TokenEqual || t.Type == TokenComma || (t.Type == TokenSymbol && t.Value == ")") {
					break
				}
				tok := p.next()
				typeExpr += tok.Value + " "
			}
			var defVal Value
			if p.peek().Type == TokenEqual {
				p.next() // consume =
				val, ok := p.parseValue()
				if ok {
					defVal = val
				}
			}
			params = append(params, TemplateParameter{
				Name:         paramName.Value,
				TypeExpr:     strings.TrimSpace(typeExpr),
				DefaultValue: defVal,
			})
			if p.peek().Type == TokenComma {
				p.next()
			}
		}
	}

	if p.peek().Type == TokenLBrace {
		p.next() // consume {
	}

	body, endTok, ok := p.parseBlock()
	if !ok {
		return nil, false
	}

	if endTok.Type != TokenEnd {
		p.addError(endTok.Position, "expected #end")
	}

	return &TemplateDefinition{
		Position:    startTok.Position,
		EndPosition: endTok.Position,
		Name:        nameTok.Value,
		Parameters:  params,
		Body:        body,
	}, true
}

func (p *Parser) parseUse(startTok Token) (Definition, bool) {
	templateTok := p.next()
	if templateTok.Type != TokenIdentifier {
		p.addError(templateTok.Position, "expected template name")
		return nil, false
	}

	// Let's assume #use Template Name (args)
	instanceNameTok := p.next()
	if instanceNameTok.Type != TokenIdentifier {
		p.addError(instanceNameTok.Position, "expected instance name")
		return nil, false
	}

	var args []TemplateArgument
	if p.peek().Type == TokenSymbol && p.peek().Value == "(" {
		p.next()
		for {
			if p.peek().Type == TokenSymbol && p.peek().Value == ")" {
				p.next()
				break
			}
			argName := p.next()
			if argName.Type != TokenIdentifier {
				p.addError(argName.Position, "expected argument name")
				return nil, false
			}
			if p.next().Type != TokenEqual {
				p.addError(p.peek().Position, "expected =")
				return nil, false
			}
			val, _ := p.parseValue()
			args = append(args, TemplateArgument{Name: argName.Value, Value: val})
			if p.peek().Type == TokenComma {
				p.next()
			}
		}
	}

	return &TemplateInstantiation{
		Position:    startTok.Position,
		EndPosition: p.peek().Position, // Rough
		Name:        instanceNameTok.Value,
		Template:    templateTok.Value,
		Arguments:   args,
	}, true
}

func (p *Parser) parseBlock() ([]Definition, Token, bool) {
	var defs []Definition
	for {
		t := p.peek()
		if t.Type == TokenEOF {
			return defs, t, false
		}
		if t.Type == TokenEnd || t.Type == TokenElse {
			return defs, p.next(), true
		}
		if t.Type == TokenRBrace {
			// If we are in a brace block, #end might be inside or after.
			// Usually we expect #end to close the block.
			p.next()
			continue
		}
		def, ok := p.parseDefinition()
		if ok {
			defs = append(defs, def)
		} else {
			p.next()
		}
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

func getPrecedence(t Token) int {
	switch t.Type {
	case TokenStar, TokenSlash, TokenPercent:
		return 5
	case TokenPlus, TokenMinus:
		return 4
	case TokenConcat:
		return 3
	case TokenSymbol:
		if t.Value == "<" || t.Value == ">" || t.Value == "<=" || t.Value == ">=" || t.Value == "==" || t.Value == "!=" {
			return 2
		}
		return 0
	case TokenAmpersand:
		return 1 // Bitwise AND
	case TokenPipe, TokenCaret:
		return 1 // Bitwise OR/XOR
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
		prec := getPrecedence(t)
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
		isFloat := (strings.Contains(tok.Value, ".") || strings.Contains(tok.Value, "e") || strings.Contains(tok.Value, "E")) &&
			!strings.HasPrefix(tok.Value, "0x") && !strings.HasPrefix(tok.Value, "0X") &&
			!strings.HasPrefix(tok.Value, "0b") && !strings.HasPrefix(tok.Value, "0B")

		if isFloat {
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
	case TokenMinus:
		val, ok := p.parseAtom()
		if !ok {
			return nil, false
		}
		return &UnaryExpression{Position: tok.Position, Operator: tok, Right: val}, true
	case TokenSymbol:
		if tok.Value == "(" {
			val, ok := p.parseExpression(0)
			if !ok {
				return nil, false
			}
			if next := p.next(); next.Type != TokenSymbol || next.Value != ")" {
				p.addError(next.Position, "expected )")
				return nil, false
			}
			return val, true
		}
		if tok.Value == "!" {
			val, ok := p.parseAtom()
			if !ok {
				return nil, false
			}
			return &UnaryExpression{Position: tok.Position, Operator: tok, Right: val}, true
		}
		fallthrough
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

func (p *Parser) parseLet(startTok Token) (Definition, bool) {
	nameTok := p.next()
	if nameTok.Type != TokenIdentifier {
		p.addError(nameTok.Position, "expected constant name")
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
			break
		}
		typeTokens = append(typeTokens, p.next())
	}

	typeExpr := ""
	for _, t := range typeTokens {
		typeExpr += t.Value + " "
	}

	var defVal Value
	if p.next().Type != TokenEqual {
		p.addError(nameTok.Position, "expected =")
		return nil, false
	}
	val, ok := p.parseValue()
	if ok {
		defVal = val
	} else {
		return nil, false
	}

	return &VariableDefinition{
		Position:     startTok.Position,
		Name:         nameTok.Value,
		TypeExpr:     strings.TrimSpace(typeExpr),
		DefaultValue: defVal,
		IsConst:      true,
	}, true
}

func (p *Parser) Errors() []error {
	return p.errors
}
