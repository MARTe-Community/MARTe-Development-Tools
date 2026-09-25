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
	// signalsDepth counts how many "Signals = { … }" bodies we are
	// inside. Signal definition sugar (`Name: Type`) is only valid there.
	signalsDepth int
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

		// `with json("file") as name begin … end`
		if name == "with" && p.atWithLoader() {
			return p.parseWith(tok)
		}

		// Signal shorthand: DS::Signal [: Type[Dim]] [= { … }]
		if strings.Contains(name, "::") {
			return p.parseSignalShorthand(tok, name)
		}

		// Signal definition sugar inside a Signals block: `Name: Type[Dim] [= { … }]`
		if p.peek().Type == TokenColon && p.signalsDepth > 0 {
			return p.parseSignalDefinition(tok, name)
		}

		// If followed by =, it's a definition
		if p.peek().Type == TokenEqual {
			p.next() // consume =

			if p.peek().Type == TokenLBrace && p.isSubnodeLookahead() {
				if name == "Signals" {
					p.signalsDepth++
				}
				sub, ok := p.parseSubnodeConcat()
				if name == "Signals" {
					p.signalsDepth--
				}
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

		sub, ok := p.parseSubnodeConcat()
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
			sub, ok := p.parseSubnodeConcat()
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

// parseSignalShorthand parses:
//
//	DS::Signal [: Type[Dim]] [= { … }]
//
// The caller has already consumed the identifier token (name = "DS::Signal").
func (p *Parser) parseSignalShorthand(startTok Token, name string) (Definition, bool) {
	parts := strings.SplitN(name, "::", 2)
	// The whole "DataSource::SignalName" run is lexed as a single identifier
	// token starting at startTok.Position, and (per lexIdentifier) can never
	// span multiple lines, so SignalName's own start position is simply
	// startTok.Position shifted right past "DataSource::". Use the untrimmed
	// parts[0] length since the lexer never includes whitespace in an
	// identifier token (TrimSpace below is a defensive no-op).
	signalNamePos := startTok.Position
	signalNamePos.Column += len(parts[0]) + len("::")
	sh := &SignalShorthand{
		Position:           startTok.Position,
		EndPosition:        startTok.Position,
		SignalNamePosition: signalNamePos,
		DataSource:         strings.TrimSpace(parts[0]),
		SignalName:         strings.TrimSpace(parts[1]),
	}

	// Optional ": Type [Dim]"
	if p.peek().Type == TokenColon {
		p.next() // consume ':'
		typeTok := p.peek()
		if typeTok.Type != TokenIdentifier {
			p.addError(typeTok.Position, "expected type name after ':'")
			return nil, false
		}
		p.next()
		sh.Type = typeTok.Value
		sh.EndPosition = typeTok.Position

		// Optional "[NumElements]"
		if p.peek().Type == TokenLBracket {
			p.next() // consume '['
			dim, ok := p.parseValue()
			if !ok {
				return nil, false
			}
			sh.NumElements = dim
			sh.EndPosition = dim.End()
			if p.peek().Type != TokenRBracket {
				p.addError(p.peek().Position, "expected ']'")
				return nil, false
			}
			p.next() // consume ']'
		}
	}

	// Optional "as <NAME>"
	if p.peek().Type == TokenAs {
		p.next() // consume 'as'
		nameTok := p.peek()
		if nameTok.Type != TokenIdentifier {
			p.addError(nameTok.Position, "expected name after 'as'")
			return nil, false
		}
		p.next()
		sh.AliasName = nameTok.Value
		sh.EndPosition = nameTok.Position
	}

	// Optional "= { … }"
	if p.peek().Type == TokenEqual {
		p.next() // consume '='
		if p.peek().Type != TokenLBrace {
			p.addError(p.peek().Position, "expected '{' after '='")
			return nil, false
		}
		sub, ok := p.parseSubnode()
		if !ok {
			return nil, false
		}
		sh.ExtraFields = sub
		sh.HasExtraFields = true
		sh.EndPosition = sub.EndPosition
	}

	return sh, true
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

	var elseIfBranches []ElseIfBranch
	var elseBody []Definition

	for endTok.Type == TokenElse || endTok.Type == TokenElseIf {
		isPlainElse := false
		if endTok.Type == TokenElse {
			switch {
			case p.peek().Type == TokenIf:
				p.next() // two-word `else if`
			case p.peek().Type == TokenElseIf:
				// one-word `elseif`, consumed below
			default:
				isPlainElse = true
			}
		}
		if isPlainElse {
			if p.peek().Type == TokenLBrace {
				p.next()
			}
			elseBody, endTok, ok = p.parseBlock()
			if !ok {
				return nil, false
			}
			break
		}
		if p.peek().Type == TokenElseIf {
			p.next() // one-word `elseif`
		}
		elseifCond, ok2 := p.parseValue()
		if !ok2 {
			return nil, false
		}
		if p.peek().Type == TokenLBrace {
			p.next()
		}
		elseifBody, endTok2, ok3 := p.parseBlock()
		if !ok3 {
			return nil, false
		}
		elseIfBranches = append(elseIfBranches, ElseIfBranch{
			Condition: elseifCond,
			Body:      elseifBody,
		})
		endTok = endTok2
	}

	if endTok.Type != TokenEnd {
		p.addError(endTok.Position, "expected end")
	}

	return &IfBlock{
		Position:    startTok.Position,
		EndPosition: endTok.Position,
		Condition:   cond,
		Then:        thenBody,
		ElseIf:      elseIfBranches,
		Else:        elseBody,
	}, true
}

// parseSignalDefinition parses the DataSource signal definition sugar:
//
//	Name: Type
//	Name: Type[Dim]
//	Name: Type = { Extra = 1 … }
//
// The caller has consumed the name token and verified the ':' and that
// we are inside a Signals block.
func (p *Parser) parseSignalDefinition(nameTok Token, name string) (Definition, bool) {
	p.next() // consume ':'
	typeTok := p.next()
	if typeTok.Type != TokenIdentifier {
		p.addError(typeTok.Position, "expected signal type after ':'")
		return nil, false
	}
	sh := &SignalShorthand{
		Position:           nameTok.Position,
		EndPosition:        typeTok.Position,
		SignalNamePosition: nameTok.Position,
		SignalName:         name,
		Type:               typeTok.Value,
	}

	if p.peek().Type == TokenLBracket {
		p.next() // consume '['
		dim, ok := p.parseValue()
		if !ok {
			return nil, false
		}
		sh.NumElements = dim
		sh.EndPosition = dim.End()
		if p.next().Type != TokenRBracket {
			p.addError(p.peek().Position, "expected ']'")
			return nil, false
		}
	}

	if p.peek().Type == TokenEqual {
		p.next() // consume '='
		if p.peek().Type != TokenLBrace {
			p.addError(p.peek().Position, "expected '{' after '='")
			return nil, false
		}
		sub, ok := p.parseSubnodeConcat()
		if !ok {
			return nil, false
		}
		sh.ExtraFields = sub
		sh.HasExtraFields = true
		sh.EndPosition = sub.EndPosition
	}

	return sh, true
}

// parseConditionalArrayElements parses a #if block that appears inside an array:
//
//	{ X  #if cond  Y  Z  #else  W  #end  V }
//
// The returned ConditionalArrayElements holds the two value-lists (Then / Else).
func (p *Parser) parseConditionalArrayElements(startTok Token) (Value, bool) {
	cond, ok := p.parseValue()
	if !ok {
		return nil, false
	}
	if p.peek().Type == TokenLBrace {
		p.next()
	}
	thenElems, endTok, ok := p.parseArrayValueBlock()
	if !ok {
		return nil, false
	}
	var elseIfBranches []ConditionalElseIfBranch
	var elseElems []Value
	for endTok.Type == TokenElse || endTok.Type == TokenElseIf {
		isPlainElse := false
		if endTok.Type == TokenElse {
			switch {
			case p.peek().Type == TokenIf:
				p.next() // two-word `else if`
			case p.peek().Type == TokenElseIf:
				// one-word `elseif`, consumed below
			default:
				isPlainElse = true
			}
		}
		if isPlainElse {
			if p.peek().Type == TokenLBrace {
				p.next()
			}
			elseElems, endTok, ok = p.parseArrayValueBlock()
			if !ok {
				return nil, false
			}
			break
		}
		if p.peek().Type == TokenElseIf {
			p.next() // one-word `elseif`
		}
		elseifCond, ok2 := p.parseValue()
		if !ok2 {
			return nil, false
		}
		if p.peek().Type == TokenLBrace {
			p.next()
		}
		elseifElems, endTok2, ok3 := p.parseArrayValueBlock()
		if !ok3 {
			return nil, false
		}
		elseIfBranches = append(elseIfBranches, ConditionalElseIfBranch{
			Condition: elseifCond,
			Body:      elseifElems,
		})
		endTok = endTok2
	}
	if endTok.Type != TokenEnd {
		p.addError(endTok.Position, "expected end")
	}
	return &ConditionalArrayElements{
		Position:    startTok.Position,
		EndPosition: endTok.Position,
		Condition:   cond,
		Then:        thenElems,
		ElseIf:      elseIfBranches,
		Else:        elseElems,
	}, true
}

// parseArrayValueBlock parses a sequence of Values until a #else, #end, or EOF
// token is consumed.  It supports nested #if blocks.
func (p *Parser) parseArrayValueBlock() ([]Value, Token, bool) {
	var elems []Value
	for {
		t := p.peek()
		switch t.Type {
		case TokenElse, TokenElseIf, TokenEnd, TokenEOF:
			return elems, p.next(), true
		case TokenComma:
			p.next()
		case TokenIf:
			ifTok := p.next()
			elem, ok := p.parseConditionalArrayElements(ifTok)
			if !ok {
				return nil, Token{}, false
			}
			elems = append(elems, elem)
		default:
			val, ok := p.parseValue()
			if !ok {
				return nil, Token{}, false
			}
			elems = append(elems, val)
		}
	}
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
	} else if next.Type == TokenComma {
		// `foreach key, value in …` — comma-separated form.
		p.next()
		nameTok := p.next()
		if nameTok.Type != TokenIdentifier {
			p.addError(nameTok.Position, "expected variable name after ','")
			return nil, false
		}
		keyVar = v1Tok.Value
		valueVar = nameTok.Value
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

	// Optional `do` before the body: `foreach x in xs do … end`.
	if p.peek().Type == TokenIdentifier && p.peek().Value == "do" {
		p.next()
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
			var typeTokens []Token
			for {
				t := p.peek()
				if t.Type == TokenEOF || t.Type == TokenEqual || t.Type == TokenComma || (t.Type == TokenSymbol && t.Value == ")") {
					break
				}
				typeTokens = append(typeTokens, p.next())
			}
			typeExpr := joinTypeTokens(typeTokens)
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
			p.addError(t.Position, "unexpected EOF, expected #end or #else")
			return defs, t, false
		}
		if t.Type == TokenEnd || t.Type == TokenElse || t.Type == TokenElseIf {
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
		// Signal shorthand (DS::Signal ...) is always a definition.
		if strings.Contains(t1.Value, "::") {
			return true
		}
		// Identifier inside.
		// If followed by '=', it's a definition -> Subnode.
		t2 := p.peekN(2)
		if t2.Type == TokenEqual {
			return true
		}
		// "Name: Type" — signal definition sugar -> Subnode.
		if t2.Type == TokenColon {
			return true
		}
		// Identifier alone or followed by something else -> Reference/Value -> Array
		return false
	}

	if t1.Type == TokenObjectIdentifier {
		// +Node = ... -> Definition -> Subnode
		return true
	}

	// Directives opening a block (`#foreach`, `#if`, `#template`, …)
	// make the braces a subnode containing definitions.
	switch t1.Type {
	case TokenForeach, TokenIf, TokenLet, TokenVar, TokenTemplate, TokenUse, TokenElseIf:
		return true
	}

	// Literals -> Array
	return false
}

// parseSubnodeConcat parses an object body, optionally merged with
// further bodies via the concat operator:
//
//	{ A = 1 } .. { B = 2 }
//
// The definitions of all operands are concatenated; on key conflicts
// the later operand wins at evaluation time.
func (p *Parser) parseSubnodeConcat() (Subnode, bool) {
	sub, ok := p.parseSubnode()
	if !ok {
		return sub, false
	}
	for p.peek().Type == TokenConcat {
		p.next() // consume '..'
		next, ok := p.parseSubnode()
		if !ok {
			return sub, false
		}
		// Dict-merge semantics: fields redefined by the later operand
		// override the earlier ones instead of duplicating them.
		overrides := map[string]bool{}
		for _, d := range next.Definitions {
			if f, ok := d.(*Field); ok {
				overrides[f.Name] = true
			}
		}
		if len(overrides) > 0 {
			merged := make([]Definition, 0, len(sub.Definitions)+len(next.Definitions))
			for _, d := range sub.Definitions {
				if f, ok := d.(*Field); ok && overrides[f.Name] {
					continue
				}
				merged = append(merged, d)
			}
			sub.Definitions = merged
		}
		sub.Definitions = append(sub.Definitions, next.Definitions...)
		sub.EndPosition = next.EndPosition
	}
	return sub, true
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
	case TokenSymbol:
		switch t.Value {
		case "||":
			return 1
		case "&&":
			return 2
		case "==", "!=":
			return 3
		case "<", ">", "<=", ">=":
			return 4
		}
	case TokenConcat:
		return 5
	case TokenPipe, TokenCaret:
		return 6 // Bitwise OR/XOR
	case TokenAmpersand:
		return 7 // Bitwise AND
	case TokenPlus, TokenMinus:
		return 8
	case TokenStar, TokenSlash, TokenPercent:
		return 9
	}
	return 0
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
			Value:    unescapeString(strings.Trim(tok.Value, "\"")),
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
		var val Value = &VariableReferenceValue{Position: tok.Position, Name: tok.Value}
		// Structured member access: @doc.member (chains allowed).
		for p.peek().Type == TokenSymbol && p.peek().Value == "." && p.peekN(1).Type == TokenIdentifier {
			p.next() // consume '.'
			member := p.next()
			val = &MemberAccess{Position: tok.Position, Base: val, Member: member.Value}
		}
		return val, true
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
		if tok.Value != "{" {
			p.addError(tok.Position, fmt.Sprintf("unexpected symbol %q", tok.Value))
			return nil, false
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
			if t.Type == TokenIf {
				ifTok := p.next()
				elem, ok := p.parseConditionalArrayElements(ifTok)
				if !ok {
					return nil, false
				}
				arr.Elements = append(arr.Elements, elem)
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

	typeExpr := joinTypeTokens(typeTokens)

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

// joinTypeTokens renders a sequence of type-expression tokens into a compact
// string, e.g. "[&GAM]" rather than "[ & GAM ]". Tokens are space-separated by
// default, except around bracket/paren/comma delimiters and after a leading
// "&" reference marker, which are kept tight against their neighbour.
func joinTypeTokens(tokens []Token) string {
	var b strings.Builder
	for i, t := range tokens {
		if i > 0 && !noSpaceBetween(tokens[i-1], t) {
			b.WriteByte(' ')
		}
		b.WriteString(t.Value)
	}
	return b.String()
}

func noSpaceBetween(prev, next Token) bool {
	switch {
	case prev.Type == TokenLBracket:
		return true
	case next.Type == TokenRBracket:
		return true
	case next.Type == TokenComma:
		return true
	case prev.Type == TokenAmpersand:
		return true
	case prev.Type == TokenSymbol && prev.Value == "(":
		return true
	case next.Type == TokenSymbol && next.Value == ")":
		return true
	default:
		return false
	}
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

	typeExpr := joinTypeTokens(typeTokens)

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

// atWithLoader reports whether a `with <format>(` loader call follows.
// Only then is `with` treated as a directive; `with = 3` remains a
// plain field.
func (p *Parser) atWithLoader() bool {
	t1 := p.peek()
	if t1.Type != TokenIdentifier || (t1.Value != "json" && t1.Value != "csv") {
		return false
	}
	t2 := p.peekN(1)
	return t2.Type == TokenSymbol && t2.Value == "("
}

// parseWith parses:
//
//	with json("file.json") as name begin … end
//	with csv("file.csv") as name { … }
//
// `begin`/`{` are both accepted as body openers; the body is closed by
// `end` (or the closing brace).
func (p *Parser) parseWith(startTok Token) (Definition, bool) {
	formatTok := p.next()
	if formatTok.Type != TokenIdentifier {
		p.addError(formatTok.Position, "expected format name (json, csv, …) after 'with'")
		return nil, false
	}
	if t := p.next(); t.Type != TokenSymbol || t.Value != "(" {
		p.addError(t.Position, "expected '(' after format name")
		return nil, false
	}
	path, ok := p.parseValue()
	if !ok {
		return nil, false
	}
	if t := p.next(); t.Type != TokenSymbol || t.Value != ")" {
		p.addError(t.Position, "expected ')' after path")
		return nil, false
	}
	nameTok := p.next()
	if nameTok.Type != TokenAs {
		p.addError(nameTok.Position, "expected 'as' before binding name")
		return nil, false
	}
	bindTok := p.next()
	if bindTok.Type != TokenIdentifier {
		p.addError(bindTok.Position, "expected binding name after 'as'")
		return nil, false
	}
	switch bindTok.Value {
	case "begin", "end", "do", "in", "as":
		p.addError(bindTok.Position, fmt.Sprintf("invalid binding name %q after 'as'", bindTok.Value))
		return nil, false
	}
	// Optional `begin` or `{` opens the body.
	if p.peek().Type == TokenLBrace {
		p.next()
	} else if p.peek().Type == TokenIdentifier && p.peek().Value == "begin" {
		p.next()
	}
	body, endTok, ok := p.parseBlock()
	if !ok {
		return nil, false
	}
	if endTok.Type != TokenEnd {
		p.addError(endTok.Position, "expected end")
	}
	return &WithBlock{
		Position:    startTok.Position,
		EndPosition: endTok.Position,
		Format:      formatTok.Value,
		Path:        path,
		BindName:    bindTok.Value,
		Body:        body,
	}, true
}

func (p *Parser) Errors() []error {
	return p.errors
}

// EscapeString renders s as the body of a string literal, escaping the
// characters the lexer understands (\, ", \n, \t, \r). It is the
// inverse of unescapeString.
func EscapeString(s string) string {
	if !strings.ContainsAny(s, "\\\"\n\t\r") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 4)
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString("\\\\")
		case '"':
			b.WriteString("\\\"")
		case '\n':
			b.WriteString("\\n")
		case '\t':
			b.WriteString("\\t")
		case '\r':
			b.WriteString("\\r")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// unescapeString resolves backslash escapes in a string literal body.
// Recognised: \\ \" \n \t \r. Unknown escapes keep the escaped
// character verbatim.
func unescapeString(s string) string {
	if !strings.Contains(s, "\\") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	// Classic loop: the body reassigns i to skip escaped characters.
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 >= len(s) {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch s[i] {
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case 'r':
			b.WriteByte('\r')
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}
