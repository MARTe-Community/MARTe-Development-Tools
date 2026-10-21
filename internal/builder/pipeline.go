package builder

import (
	"fmt"
	"strings"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
)

// expandPipelines instantiates `gam` pipeline blocks. An instance is an
// object whose Class names a pipeline; its fields bind the ports.
//
//	+Pre = {
//	  Class = Preprocess
//	  Raw   = DDB::RawIn
//	  Clean = SINK::Clean
//	}
//
// The instance is replaced by the pipeline body: its GAMs (renamed with the
// instance prefix) plus the wiring, in body order. References to the
// instance in Functions lists expand to the same order.
func (l *lowerer) expandPipelines(root *index.ProjectNode, rep *LowerReport) error {
	defs := map[string]*parser.GamDefinition{}
	l.tree.Walk(func(n *index.ProjectNode) {
		for _, frag := range n.Fragments {
			for _, def := range frag.Definitions {
				if g, ok := def.(*parser.GamDefinition); ok {
					defs[g.Name] = g
				}
			}
		}
	})
	if len(defs) == 0 {
		return nil
	}

	type expansion struct {
		node  *index.ProjectNode
		frag  *index.Fragment
		index int
		name  string
		elems []parser.Value
		defs  []parser.Definition
		err   error
	}
	var expansions []expansion

	l.tree.Walk(func(n *index.ProjectNode) {
		for _, frag := range n.Fragments {
			for i, def := range frag.Definitions {
				obj, ok := def.(*parser.ObjectNode)
				if !ok {
					continue
				}
				pipeline := defs[l.classOfObject(obj)]
				if pipeline == nil {
					continue
				}
				instanceName := index.NormalizeName(literalText(obj.Name))
				elems, generated, err := l.expandInstance(pipeline, instanceName, obj)
				expansions = append(expansions, expansion{
					node: n, frag: frag, index: i, name: instanceName,
					elems: elems, defs: generated, err: err,
				})
			}
		}
	})

	for _, e := range expansions {
		if e.err != nil {
			return fmt.Errorf("%d:%d: pipeline instance %q: %v",
				e.frag.ObjectPos.Line, e.frag.ObjectPos.Column, e.name, e.err)
		}
		// Replace the instance definition with the generated definitions.
		newDefs := make([]parser.Definition, 0, len(e.frag.Definitions)+len(e.defs))
		for j, def := range e.frag.Definitions {
			if j == e.index {
				newDefs = append(newDefs, e.defs...)
				continue
			}
			newDefs = append(newDefs, def)
		}
		e.frag.Definitions = newDefs
		for _, def := range e.defs {
			l.indexDefinition(e.node, def, e.frag.File)
		}
		delete(e.node.Children, e.name)
		l.replaceFunctionsElement(e.name, e.elems)
		rep.Pipelines++
	}
	return nil
}

// classOfObject returns the Class value of an object definition.
func (l *lowerer) classOfObject(obj *parser.ObjectNode) string {
	for _, def := range obj.Subnode.Definitions {
		if f, ok := def.(*parser.Field); ok && f.Name == "Class" {
			return literalText(f.Value)
		}
	}
	return ""
}

// expandInstance builds the generated definitions and Functions elements.
func (l *lowerer) expandInstance(pipeline *parser.GamDefinition, instance string, obj *parser.ObjectNode) ([]parser.Value, []parser.Definition, error) {
	bindings := map[string]parser.CopyEndpoint{}
	for _, def := range obj.Subnode.Definitions {
		f, ok := def.(*parser.Field)
		if !ok || f.Name == "Class" {
			continue
		}
		ep, err := endpointFromBinding(f.Value)
		if err != nil {
			return nil, nil, fmt.Errorf("%d:%d: port %q: %v", f.Position.Line, f.Position.Column, f.Name, err)
		}
		bindings[f.Name] = ep
	}

	isInput := func(name string) bool {
		for _, p := range pipeline.Inputs {
			if p.Name == name {
				return true
			}
		}
		return false
	}
	isOutput := func(name string) bool {
		for _, p := range pipeline.Outputs {
			if p.Name == name {
				return true
			}
		}
		return false
	}
	for _, p := range pipeline.Inputs {
		if _, ok := bindings[p.Name]; !ok {
			return nil, nil, fmt.Errorf("pipeline %q: input port %q is not bound", pipeline.Name, p.Name)
		}
	}
	for _, p := range pipeline.Outputs {
		if _, ok := bindings[p.Name]; !ok {
			return nil, nil, fmt.Errorf("pipeline %q: output port %q is not bound", pipeline.Name, p.Name)
		}
	}

	// Body items in declaration order; wiring is scheduled before the
	// GAM it feeds (or after its producer when it only feeds a port).
	type bodyItem struct {
		gam   *parser.ObjectNode
		group *parser.CopyGroup
		name  string
	}
	var items []bodyItem
	for _, body := range pipeline.Body {
		switch d := body.(type) {
		case *parser.ObjectNode:
			items = append(items, bodyItem{gam: d, name: index.NormalizeName(literalText(d.Name))})
		case *parser.CopyGroup:
			group := &parser.CopyGroup{Position: d.Position, EndPosition: d.EndPosition, Name: d.Name}
			for _, item := range d.Items {
				item.Src = l.rewriteEndpoint(item.Src, bindings, instance, isInput, isOutput, true)
				item.Dst = l.rewriteEndpoint(item.Dst, bindings, instance, isInput, isOutput, false)
				group.Items = append(group.Items, item)
			}
			items = append(items, bodyItem{group: group})
		}
	}

	// Index of a GAM by (unprefixed) name.
	gamIndex := map[string]int{}
	gamOrder := []string{}
	for _, it := range items {
		if it.gam != nil {
			gamIndex[it.name] = len(gamOrder)
			gamOrder = append(gamOrder, it.name)
		}
	}
	slotOf := func(name string) (int, bool) {
		name = index.NormalizeName(name)
		if idx, ok := gamIndex[name]; ok {
			return idx, true
		}
		// Instance-prefixed names appear after rewriting.
		if strings.HasPrefix(name, instance+"_") {
			if idx, ok := gamIndex[strings.TrimPrefix(name, instance+"_")]; ok {
				return idx, true
			}
		}
		return 0, false
	}

	// scheduled[i] holds the wiring that runs before GAM i; tail runs last.
	scheduled := map[int][]*parser.CopyGroup{}
	var tail []*parser.CopyGroup
	for _, it := range items {
		if it.group == nil {
			continue
		}
		pos := -1
		for _, item := range it.group.Items {
			if idx, ok := slotOf(item.Dst.Object); ok {
				if pos < 0 || idx < pos {
					pos = idx
				}
			}
		}
		if pos < 0 {
			// Output-only wiring: run after the last producing GAM.
			last := -1
			for _, item := range it.group.Items {
				if idx, ok := slotOf(item.Src.Object); ok && idx > last {
					last = idx
				}
			}
			if last+1 < len(gamOrder) {
				pos = last + 1
			}
		}
		if pos < 0 || pos >= len(gamOrder) {
			tail = append(tail, it.group)
		} else {
			scheduled[pos] = append(scheduled[pos], it.group)
		}
	}

	var elems []parser.Value
	var generated []parser.Definition
	for gi := range gamOrder {
		// Wiring feeding this GAM runs first.
		for _, g := range scheduled[gi] {
			elems = append(elems, g)
		}
		for _, it := range items {
			if it.gam != nil && it.name == gamOrder[gi] {
				clone := cloneObjectWithPrefix(it.gam, instance+"_")
				generated = append(generated, clone)
				elems = append(elems, &parser.ReferenceValue{Position: it.gam.Position, Value: instance + "_" + it.name})
				break
			}
		}
	}
	for _, g := range tail {
		elems = append(elems, g)
	}
	return elems, generated, nil
}

// rewriteEndpoint substitutes ports with their bindings and prefixes
// internal GAM names with the instance name.
func (l *lowerer) rewriteEndpoint(ep parser.CopyEndpoint, bindings map[string]parser.CopyEndpoint,
	instance string, isInput, isOutput func(string) bool, asSource bool) parser.CopyEndpoint {
	name := index.NormalizeName(ep.Object)
	if bound, ok := bindings[name]; ok {
		if asSource && isOutput(name) {
			// An output port as a source: use the bound signal (fan-out
			// from the pipeline output back into the body).
			return bound
		}
		if !asSource && isInput(name) {
			// An input port as a destination: write into the bound signal.
			return bound
		}
		return bound
	}
	if isInput(name) && !asSource {
		return ep
	}
	if isOutput(name) && asSource {
		return ep
	}
	ep.Object = instance + "_" + name
	return ep
}

// endpointFromBinding parses `DS::Sig`, `GAM.Sig` or `GAM` into an endpoint.
func endpointFromBinding(v parser.Value) (parser.CopyEndpoint, error) {
	pos := v.Pos()
	switch t := v.(type) {
	case *parser.ReferenceValue:
		return endpointFromText(t.Value, pos)
	case *parser.StringValue:
		return endpointFromText(t.Value, pos)
	case *parser.FromValue:
		return t.Src, nil
	}
	return parser.CopyEndpoint{}, fmt.Errorf("expected a signal reference")
}

func endpointFromText(text string, pos parser.Position) (parser.CopyEndpoint, error) {
	text = strings.TrimSpace(text)
	if idx := strings.Index(text, "::"); idx >= 0 {
		if idx == 0 || idx+2 >= len(text) {
			return parser.CopyEndpoint{}, fmt.Errorf("invalid signal reference %q", text)
		}
		return parser.CopyEndpoint{Position: pos, IsDS: true, Object: text[:idx], Signal: text[idx+2:]}, nil
	}
	if dot := strings.LastIndex(text, "."); dot >= 0 {
		if dot == 0 || dot+1 >= len(text) {
			return parser.CopyEndpoint{}, fmt.Errorf("invalid signal reference %q", text)
		}
		return parser.CopyEndpoint{Position: pos, Object: text[:dot], Signal: text[dot+1:]}, nil
	}
	if text == "" {
		return parser.CopyEndpoint{}, fmt.Errorf("empty signal reference")
	}
	return parser.CopyEndpoint{Position: pos, Object: text}, nil
}

// cloneObjectWithPrefix deep-copies an object definition and prefixes the
// names of nested object definitions (the pipeline's GAMs).
func cloneObjectWithPrefix(obj *parser.ObjectNode, prefix string) *parser.ObjectNode {
	clone := &parser.ObjectNode{
		Position: obj.Position,
		Name:     &parser.ReferenceValue{Position: obj.Position, Value: prefixedName(literalText(obj.Name), prefix)},
	}
	clone.Subnode.Position = obj.Subnode.Position
	clone.Subnode.EndPosition = obj.Subnode.EndPosition
	for _, def := range obj.Subnode.Definitions {
		clone.Subnode.Definitions = append(clone.Subnode.Definitions, cloneDefinition(def, prefix))
	}
	return clone
}

func cloneDefinition(def parser.Definition, prefix string) parser.Definition {
	switch d := def.(type) {
	case *parser.ObjectNode:
		clone := &parser.ObjectNode{
			Position: d.Position,
			Name:     &parser.ReferenceValue{Position: d.Position, Value: cloneName(literalText(d.Name), prefix)},
		}
		clone.Subnode.Position = d.Subnode.Position
		clone.Subnode.EndPosition = d.Subnode.EndPosition
		for _, sub := range d.Subnode.Definitions {
			clone.Subnode.Definitions = append(clone.Subnode.Definitions, cloneDefinition(sub, prefix))
		}
		return clone
	case *parser.Field:
		return &parser.Field{Position: d.Position, Name: d.Name, Value: cloneValue(d.Value, prefix)}
	case *parser.SignalShorthand:
		c := *d
		return &c
	}
	return def
}

// cloneName prefixes top-level pipeline GAM names only; signal and
// container names stay as written.
func cloneName(name, prefix string) string {
	if strings.HasPrefix(name, "+") || strings.HasPrefix(name, "$") {
		return name[:1] + prefix + name[1:]
	}
	return name
}

// prefixedName inserts prefix after a leading '+'/'$', if any.
func prefixedName(name, prefix string) string {
	if len(name) > 1 && (name[0] == '+' || name[0] == '$') {
		return name[:1] + prefix + name[1:]
	}
	return prefix + name
}

func cloneValue(v parser.Value, prefix string) parser.Value {
	// Values inside pipeline GAMs refer to DataSources and signals, not to
	// other GAMs, so they are copied as-is.
	return v
}

// replaceFunctionsElement expands a pipeline instance reference in every
// Functions list into the generated elements.
func (l *lowerer) replaceFunctionsElement(instance string, elems []parser.Value) {
	if len(elems) == 0 {
		return
	}
	l.tree.Walk(func(n *index.ProjectNode) {
		for _, frag := range n.Fragments {
			for _, def := range frag.Definitions {
				f, ok := def.(*parser.Field)
				if !ok || f.Name != "Functions" {
					continue
				}
				arr, ok := f.Value.(*parser.ArrayValue)
				if !ok {
					continue
				}
				out := make([]parser.Value, 0, len(arr.Elements))
				changed := false
				for _, el := range arr.Elements {
					if referencesName(el, index.NormalizeName(instance)) {
						for _, gen := range elems {
							if ref, ok := gen.(*parser.ReferenceValue); ok {
								l.tree.IndexValue(frag.File, ref)
							}
						}
						out = append(out, elems...)
						changed = true
						continue
					}
					out = append(out, el)
				}
				if changed {
					arr.Elements = out
				}
			}
		}
	})
}
