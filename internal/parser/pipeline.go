package parser

import (
	"strconv"
)

// parseGamDefinition parses `gam Name { in p: type  out q: type  body… }`.
// The caller consumed `gam` and the pipeline name.
func (p *Parser) parseGamDefinition(startTok Token, name string) (Definition, bool) {
	if p.peek().Type != TokenLBrace {
		p.addError(p.peek().Position, "expected '{' after gam")
		return nil, false
	}
	p.next() // consume '{'

	def := &GamDefinition{Position: startTok.Position, Name: name}
	for {
		t := p.peek()
		if t.Type == TokenEOF {
			p.addError(t.Position, "unexpected EOF, expected }")
			return nil, false
		}
		if t.Type == TokenRBrace {
			def.EndPosition = p.next().Position
			return def, true
		}
		if t.Type == TokenComment || t.Type == TokenComma {
			p.next()
			continue
		}
		// Ports: `in Name: type` / `out Name: type`.
		if t.Type == TokenIn {
			p.next()
			port, ok := p.parseGamPort(false)
			if !ok {
				return nil, false
			}
			def.Inputs = append(def.Inputs, port)
			continue
		}
		if t.Type == TokenIdentifier && t.Value == "out" {
			p.next()
			port, ok := p.parseGamPort(true)
			if !ok {
				return nil, false
			}
			def.Outputs = append(def.Outputs, port)
			continue
		}
		// Internal wiring: `from A -> B` (same grammar as a copy item).
		if t.Type == TokenIdentifier && t.Value == "from" {
			fromTok := p.next()
			item, ok := p.parseCopyItem()
			if !ok {
				return nil, false
			}
			def.Body = append(def.Body, &CopyGroup{Position: fromTok.Position, Items: []CopyItem{item}})
			continue
		}
		sub, ok := p.parseDefinition()
		if !ok {
			return nil, false
		}
		def.Body = append(def.Body, sub)
	}
}

func (p *Parser) parseGamPort(isOutput bool) (GamPort, bool) {
	nameTok := p.next()
	if nameTok.Type != TokenIdentifier {
		p.addError(nameTok.Position, "expected a port name")
		return GamPort{}, false
	}
	port := GamPort{Position: nameTok.Position, Name: nameTok.Value, IsOutput: isOutput}
	if p.peek().Type == TokenColon {
		p.next()
		typeTokens := []Token{}
		for {
			t := p.peek()
			if t.Type == TokenEOF || t.Type == TokenRBrace || t.Type == TokenIn ||
				(t.Type == TokenIdentifier && (t.Value == "out" || t.Value == "from")) {
				break
			}
			if t.Type == TokenObjectIdentifier {
				break
			}
			if t.Position.Line > nameTok.Position.Line {
				break
			}
			typeTokens = append(typeTokens, p.next())
		}
		if len(typeTokens) == 0 {
			p.addError(p.peek().Position, "expected a type for the port")
			return GamPort{}, false
		}
		port.Type, port.Dim = splitTypeAndDim(joinTypeTokens(typeTokens))
	}
	return port, true
}

var _ = strconv.Itoa
