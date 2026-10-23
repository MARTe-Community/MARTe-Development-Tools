package parser

import (
	"fmt"
	"strconv"
	"strings"
)

// parseCopyGroup parses `copy [Name] { item... }`. The caller has consumed
// the `copy` identifier; name may be empty for an inline group.
func (p *Parser) parseCopyGroup(startTok Token, name string) (Definition, bool) {
	if p.peek().Type != TokenLBrace {
		p.addError(p.peek().Position, "expected '{' after copy")
		return nil, false
	}
	p.next() // consume '{'

	group := &CopyGroup{Position: startTok.Position, Name: name}
	// Pragmas are collected by the parser before being swallowed; those
	// written above an item belong to it.
	pi := 0
	for pi < len(p.pragmas) && p.pragmas[pi].Position.Line <= startTok.Position.Line {
		pi++
	}
	var pendingPragmas []string
	for {
		t := p.peek()
		if t.Type == TokenEOF {
			p.addError(t.Position, "unexpected EOF, expected }")
			return nil, false
		}
		for pi < len(p.pragmas) && p.pragmas[pi].Position.Line < t.Position.Line {
			pendingPragmas = append(pendingPragmas, strings.TrimSpace(strings.TrimPrefix(p.pragmas[pi].Text, "//!")))
			if p.consumedPragmas == nil {
				p.consumedPragmas = map[int]bool{}
			}
			p.consumedPragmas[pi] = true
			pi++
		}
		if t.Type == TokenRBrace {
			group.EndPosition = p.next().Position
			return group, true
		}
		if t.Type == TokenComment {
			p.next()
			continue
		}
		if t.Type == TokenComma {
			p.next()
			continue
		}
		item, ok := p.parseCopyItem()
		if !ok {
			return nil, false
		}
		item.Pragmas = pendingPragmas
		pendingPragmas = nil
		group.Items = append(group.Items, item)
	}
}

// parseCopyItem parses `src [as alias] -> dst [as alias] [at n] [: type[dim]]`.
func (p *Parser) parseCopyItem() (CopyItem, bool) {
	item := CopyItem{}
	if p.peek().Type == TokenSymbol && p.peek().Value == "(" {
		members, ok := p.parseCopyGroupMembers()
		if !ok {
			return item, false
		}
		item.SrcGroup = members
		item.Src = members[0].Endpoint
		item.Position = members[0].Position
	} else {
		src, ok := p.parseCopyEndpoint()
		if !ok {
			return item, false
		}
		item.Src = src
		item.Position = src.Position
	}

	if item.SrcGroup == nil && p.peek().Type == TokenAs {
		p.next()
		nameTok := p.next()
		if nameTok.Type != TokenIdentifier {
			p.addError(nameTok.Position, "expected alias name after 'as'")
			return item, false
		}
		item.SrcAlias = nameTok.Value
	}

	if item.SrcGroup == nil && p.peek().Type == TokenLBrace {
		opts, ok := p.parseCopyOptions()
		if !ok {
			return item, false
		}
		item.SrcOptions = opts
	}

	if p.peek().Type != TokenArrow {
		p.addError(p.peek().Position, "expected '->' in copy item")
		return item, false
	}
	p.next()

	if p.peek().Type == TokenSymbol && p.peek().Value == "(" {
		members, ok := p.parseCopyGroupMembers()
		if !ok {
			return item, false
		}
		item.DstGroup = members
		item.Dst = members[0].Endpoint
	} else {
		dst, ok := p.parseCopyEndpoint()
		if !ok {
			return item, false
		}
		item.Dst = dst
	}

	if item.DstGroup == nil && p.peek().Type == TokenAs {
		p.next()
		nameTok := p.next()
		if nameTok.Type != TokenIdentifier {
			p.addError(nameTok.Position, "expected alias name after 'as'")
			return item, false
		}
		item.DstAlias = nameTok.Value
	}

	if item.DstGroup == nil && p.peek().Type == TokenLBrace {
		opts, ok := p.parseCopyOptions()
		if !ok {
			return item, false
		}
		item.DstOptions = opts
	}

	if item.DstGroup == nil && p.peek().Type == TokenIdentifier && p.peek().Value == "at" {
		p.next()
		numTok := p.next()
		if numTok.Type != TokenNumber {
			p.addError(numTok.Position, "expected a number after 'at'")
			return item, false
		}
		n, err := strconv.Atoi(numTok.Value)
		if err != nil || n < 1 {
			p.addError(numTok.Position, "signal index must be a positive number")
			return item, false
		}
		item.DstAt = n
	}

	if p.peek().Type == TokenColon {
		p.next()
		typeTokens := []Token{}
		for {
			t := p.peek()
			if t.Type == TokenEOF || t.Type == TokenRBrace || t.Type == TokenArrow {
				break
			}
			if t.Position.Line > item.Position.Line {
				break
			}
			typeTokens = append(typeTokens, p.next())
		}
		if len(typeTokens) == 0 {
			p.addError(p.peek().Position, "expected a type after ':'")
			return item, false
		}
		item.Type, item.Dim = splitTypeAndDim(joinTypeTokens(typeTokens))
	}
	return item, true
}

// parseCopyGroupMembers parses `(endpoint [as name] [{opts}], …)`.
func (p *Parser) parseCopyGroupMembers() ([]CopyMember, bool) {
	p.next() // consume '('
	var members []CopyMember
	for {
		t := p.peek()
		if t.Type == TokenSymbol && t.Value == ")" {
			p.next()
			if len(members) == 0 {
				p.addError(t.Position, "empty signal group")
				return nil, false
			}
			return members, true
		}
		if t.Type == TokenEOF {
			p.addError(t.Position, "unexpected EOF, expected )")
			return nil, false
		}
		if t.Type == TokenComma {
			p.next()
			continue
		}
		ep, ok := p.parseCopyEndpoint()
		if !ok {
			return nil, false
		}
		member := CopyMember{Position: ep.Position, Endpoint: ep}
		if p.peek().Type == TokenAs {
			p.next()
			nameTok := p.next()
			if nameTok.Type != TokenIdentifier {
				p.addError(nameTok.Position, "expected alias name after 'as'")
				return nil, false
			}
			member.Alias = nameTok.Value
		}
		if p.peek().Type == TokenLBrace {
			opts, ok := p.parseCopyOptions()
			if !ok {
				return nil, false
			}
			member.Options = opts
		}
		members = append(members, member)
	}
}

// parseCopyOptions parses `{ Name = value, … }` per-signal options.
func (p *Parser) parseCopyOptions() ([]CopyOption, bool) {
	if p.peek().Type != TokenLBrace {
		p.addError(p.peek().Position, "expected '{'")
		return nil, false
	}
	p.next() // consume '{'
	var opts []CopyOption
	for {
		t := p.peek()
		if t.Type == TokenRBrace {
			p.next()
			return opts, true
		}
		if t.Type == TokenComma {
			p.next()
			continue
		}
		if t.Type == TokenComment {
			p.next()
			continue
		}
		if t.Type == TokenEOF {
			p.addError(t.Position, "unexpected EOF in signal options")
			return nil, false
		}
		nameTok := p.next()
		if nameTok.Type != TokenIdentifier {
			p.addError(nameTok.Position, "expected an option name")
			return nil, false
		}
		if p.next().Type != TokenEqual {
			p.addError(nameTok.Position, "expected '=' after option name")
			return nil, false
		}
		val, ok := p.parseValue()
		if !ok {
			return nil, false
		}
		opts = append(opts, CopyOption{Position: nameTok.Position, Name: nameTok.Value, Value: val})
	}
}

// parseCopyEndpoint parses `GAM.Signal`, `GAM[n]`, `GAM[a..b]` or `DS::Signal`.
func (p *Parser) parseCopyEndpoint() (CopyEndpoint, bool) {
	tok := p.next()
	if tok.Type != TokenIdentifier {
		p.addError(tok.Position, "expected a signal reference")
		return CopyEndpoint{}, false
	}
	ep := CopyEndpoint{Position: tok.Position, EndPos: tok.Position}
	name := tok.Value

	// Dynamic signal name: `DS::@expr`. The lexer stops the identifier at
	// the '@', leaving the trailing '::' attached to the DataSource name.
	if strings.HasSuffix(name, "::") {
		ep.IsDS = true
		ep.Object = strings.TrimSuffix(name, "::")
		if ep.Object == "" {
			p.addError(tok.Position, "expected a DataSource before '::'")
			return ep, false
		}
		sigExpr, ok := p.parseValue()
		if !ok {
			return ep, false
		}
		ep.SignalExpr = sigExpr
		ep.EndPos = sigExpr.End()
		return ep, true
	}

	if idx := strings.Index(name, "::"); idx >= 0 {
		ep.IsDS = true
		ep.Object = name[:idx]
		ep.Signal = name[idx+2:]
		if ep.Object == "" || ep.Signal == "" {
			p.addError(tok.Position, fmt.Sprintf("invalid DataSource signal reference %q", name))
			return ep, false
		}
		return ep, true
	}

	if dot := strings.LastIndex(name, "."); dot >= 0 {
		ep.Object = name[:dot]
		ep.Signal = name[dot+1:]
		if ep.Object == "" || ep.Signal == "" {
			p.addError(tok.Position, fmt.Sprintf("invalid signal reference %q", name))
			return ep, false
		}
	} else {
		ep.Object = name
	}

	// Optional index or index range: GAM[3] or GAM[1..4].
	if p.peek().Type == TokenLBracket {
		p.next()
		startTok := p.next()
		if startTok.Type != TokenNumber {
			p.addError(startTok.Position, "expected a signal index inside '[]'")
			return ep, false
		}
		start, err := strconv.Atoi(startTok.Value)
		if err != nil || start < 1 {
			p.addError(startTok.Position, "signal index must be a positive number")
			return ep, false
		}
		ep.ByIndex = true
		ep.Index = start
		ep.IndexEnd = start
		if p.peek().Type == TokenConcat {
			p.next()
			endTok := p.next()
			if endTok.Type != TokenNumber {
				p.addError(endTok.Position, "expected a signal index after '..'")
				return ep, false
			}
			end, err := strconv.Atoi(endTok.Value)
			if err != nil || end < start {
				p.addError(endTok.Position, "invalid signal index range")
				return ep, false
			}
			ep.IndexEnd = end
		}
		if p.next().Type != TokenRBracket {
			p.addError(p.peek().Position, "expected ']'")
			return ep, false
		}
		if ep.Signal != "" {
			// `GAM.Sig[3]` is ambiguous; index selection applies to lists.
			p.addError(tok.Position, fmt.Sprintf("cannot combine a signal name with an index in %q", name))
			return ep, false
		}
	}
	return ep, true
}

// parseFuse parses `fuse A with B`.
func (p *Parser) parseFuse(startTok Token) (Definition, bool) {
	left := p.next()
	if left.Type != TokenIdentifier {
		p.addError(left.Position, "expected a GAM name after 'fuse'")
		return nil, false
	}
	withTok := p.next()
	if withTok.Type != TokenIdentifier || withTok.Value != "with" {
		p.addError(withTok.Position, "expected 'with' in fuse statement")
		return nil, false
	}
	right := p.next()
	if right.Type != TokenIdentifier {
		p.addError(right.Position, "expected a GAM name after 'with'")
		return nil, false
	}
	return &FuseStatement{
		Position:    startTok.Position,
		EndPosition: right.Position,
		Left:        left.Value,
		Right:       right.Value,
	}, true
}

// parseFromValue parses `from GAM.Signal [as Alias] [: type[dim]]`. The
// caller consumed the `from` identifier.
func (p *Parser) parseFromValue(fromTok Token) (Value, bool) {
	src, ok := p.parseCopyEndpoint()
	if !ok {
		return nil, false
	}
	fv := &FromValue{Position: fromTok.Position, Src: src, EndPos: src.EndPos}
	if p.peek().Type == TokenAs {
		p.next()
		nameTok := p.next()
		if nameTok.Type != TokenIdentifier {
			p.addError(nameTok.Position, "expected alias name after 'as'")
			return nil, false
		}
		fv.Alias = nameTok.Value
	}
	if p.peek().Type == TokenColon {
		p.next()
		typeTokens := []Token{}
		for {
			t := p.peek()
			if t.Type == TokenEOF || t.Type == TokenRBrace || t.Type == TokenComma {
				break
			}
			if t.Position.Line > fromTok.Position.Line {
				break
			}
			typeTokens = append(typeTokens, p.next())
		}
		if len(typeTokens) == 0 {
			p.addError(p.peek().Position, "expected a type after ':'")
			return nil, false
		}
		fv.Type, fv.Dim = splitTypeAndDim(joinTypeTokens(typeTokens))
	}
	return fv, true
}

// splitTypeAndDim splits `float32[4]` into ("float32", 4). Types without a
// trailing dimension return dim 0.
func splitTypeAndDim(expr string) (string, int) {
	expr = strings.TrimSpace(expr)
	open := strings.LastIndex(expr, "[")
	if open < 0 || !strings.HasSuffix(expr, "]") {
		return expr, 0
	}
	dim, err := strconv.Atoi(strings.TrimSpace(expr[open+1 : len(expr)-1]))
	if err != nil {
		return expr, 0
	}
	return strings.TrimSpace(expr[:open]), dim
}

// looksLikeCopyGroup reports whether the tokens after the `copy` keyword
// start a copy body: `copy {` or `copy Name {`.
func (p *Parser) looksLikeCopyGroup() bool {
	return p.peek().Type == TokenLBrace ||
		(p.peek().Type == TokenIdentifier && p.peekN(1).Type == TokenLBrace)
}

var _ = fmt.Sprintf
