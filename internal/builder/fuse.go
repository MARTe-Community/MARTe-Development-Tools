package builder

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
)

// fuseGAMs implements `fuse A with B` for expression GAMs: when an
// intermediate signal is produced by A and consumed only by B, B's
// expression is inlined into A and B disappears.
func (l *lowerer) fuseGAMs(root *index.ProjectNode, rep *LowerReport) {
	var walk func(n *index.ProjectNode)
	walk = func(n *index.ProjectNode) {
		for _, frag := range n.Fragments {
			if !l.active(frag) {
				continue
			}
			for _, def := range frag.Definitions {
				f, ok := def.(*parser.Field)
				if !ok || f.Name != "Functions" {
					continue
				}
				arr, ok := f.Value.(*parser.ArrayValue)
				if !ok {
					continue
				}
				l.fuseInArray(arr, rep)
			}
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(root)
}

func (l *lowerer) fuseInArray(arr *parser.ArrayValue, rep *LowerReport) {
	for i := 0; i < len(arr.Elements); i++ {
		fs, ok := arr.Elements[i].(*parser.FuseStatement)
		if !ok {
			continue
		}
		leftName := index.NormalizeName(fs.Left)
		rightName := index.NormalizeName(fs.Right)
		li, ri := l.arrayIndex(arr, leftName), l.arrayIndex(arr, rightName)
		// The pair must be adjacent in order; the directive may sit
		// between the two references.
		adjacent := ri == li+1
		if !adjacent && ri == li+2 {
			if _, ok := arr.Elements[li+1].(*parser.FuseStatement); ok {
				adjacent = true
			}
		}
		if li < 0 || ri < 0 || !adjacent {
			continue
		}
		left := l.byName[leftName]
		right := l.byName[rightName]
		if left == nil || right == nil || !l.fusable(left) || !l.fusable(right) {
			continue
		}
		if l.fusePair(left, right) {
			// Replace the fuse statement and the right GAM with the fused
			// left GAM; the left element keeps its position.
			out := make([]parser.Value, 0, len(arr.Elements))
			for j, el := range arr.Elements {
				switch {
				case j == li:
					out = append(out, &parser.ReferenceValue{Position: el.Pos(), Value: leftName})
				case j == ri, j == i:
					// right reference and the fuse directive disappear
				default:
					out = append(out, el)
				}
			}
			arr.Elements = out
			l.removeGAMDefinition(right)
			rep.Fused++
			i--
		}
	}
}

func (l *lowerer) arrayIndex(arr *parser.ArrayValue, name string) int {
	for i, el := range arr.Elements {
		if referencesName(el, name) {
			return i
		}
	}
	return -1
}

func referencesName(v parser.Value, name string) bool {
	switch t := v.(type) {
	case *parser.ReferenceValue:
		return index.NormalizeName(t.Value) == name
	case *parser.StringValue:
		return index.NormalizeName(t.Value) == name
	}
	return false
}

// fusable reports whether a GAM can participate in expression fusion.
func (l *lowerer) fusable(n *index.ProjectNode) bool {
	cls := nodeClass(n)
	if !strings.Contains(cls, "MathExpression") {
		return false
	}
	if expressionOf(n) == "" {
		return false
	}
	return singleFragment(n)
}

func expressionOf(n *index.ProjectNode) string {
	for _, frag := range n.Fragments {
		for _, def := range frag.Definitions {
			if f, ok := def.(*parser.Field); ok && f.Name == "Expression" {
				if s, ok := f.Value.(*parser.StringValue); ok {
					return s.Value
				}
			}
		}
	}
	return ""
}

type assignment struct {
	name string
	expr string
}

// parseAssignments splits `a := x; b := y` into assignments.
func parseAssignments(expr string) ([]assignment, bool) {
	var out []assignment
	for _, part := range strings.Split(expr, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		idx := strings.Index(part, ":=")
		if idx < 0 {
			idx = strings.Index(part, "=")
			if idx < 0 || (idx > 0 && part[idx-1] == '=') {
				return nil, false
			}
		}
		name := strings.TrimSpace(part[:idx])
		rhs := strings.TrimSpace(part[idx+2:])
		if idx+1 < len(part) && part[idx:idx+2] != ":=" {
			rhs = strings.TrimSpace(part[idx+1:])
		}
		if name == "" || rhs == "" || strings.ContainsAny(name, " ()/*+-<>=&|") {
			return nil, false
		}
		out = append(out, assignment{name: name, expr: rhs})
	}
	return out, true
}

// fusePair merges right into left. Returns false when the pair is not
// safely fusable; the configuration is then left unchanged.
func (l *lowerer) fusePair(left, right *index.ProjectNode) bool {
	leftInputs, ok := left.Children["InputSignals"]
	if !ok {
		return false
	}
	leftOutputs, ok := left.Children["OutputSignals"]
	if !ok {
		return false
	}
	rightInputs, ok := right.Children["InputSignals"]
	if !ok {
		return false
	}
	rightOutputs, ok := right.Children["OutputSignals"]
	if !ok {
		return false
	}

	leftAssignments, ok := parseAssignments(expressionOf(left))
	if !ok {
		return false
	}
	rightExpr := expressionOf(right)

	// Intermediate signals: right inputs produced by left.
	intermediates := map[string]bool{}
	var fused []string
	for _, name := range l.orderedSignals(rightInputs) {
		ref, err := l.signalRefOf(rightInputs.Children[name])
		if err != nil {
			return false
		}
		for _, outName := range l.orderedSignals(leftOutputs) {
			outRef, err := l.signalRefOf(leftOutputs.Children[outName])
			if err != nil {
				return false
			}
			if outRef.ds == ref.ds && outRef.signal == ref.signal {
				// Only fuse signals consumed exactly once (by right) and
				// assigned by left.
				if l.consumerCount(outRef.ds, outRef.signal) != 1 {
					return false
				}
				assigned := false
				for _, a := range leftAssignments {
					if a.name == ref.signal {
						assigned = true
					}
				}
				if !assigned {
					return false
				}
				intermediates[name] = true
				fused = append(fused, ref.signal)
				break
			}
		}
	}
	if len(fused) == 0 {
		return false
	}

	// Rewrite right's expression with the intermediate assignments inlined.
	newRight := rightExpr
	for _, name := range fused {
		for _, a := range leftAssignments {
			if a.name != name {
				continue
			}
			re := regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\b`)
			newRight = re.ReplaceAllString(newRight, "("+a.expr+")")
		}
	}
	if regexp.MustCompile(`\b(` + strings.Join(quoteAll(fused), "|") + `)\b`).MatchString(newRight) {
		return false // substitution left a reference we cannot resolve
	}

	// Keep left assignments for signals that are not fused.
	var kept []string
	for _, a := range leftAssignments {
		skip := false
		for _, name := range fused {
			if a.name == name {
				skip = true
			}
		}
		if !skip {
			kept = append(kept, a.name+" := "+a.expr)
		}
	}
	kept = append(kept, strings.TrimSpace(newRight))
	newExpression := strings.Join(kept, "; ")

	// New signal lists.
	var inDefs []parser.Definition
	seen := map[string]bool{}
	appendSignals := func(container *index.ProjectNode, skip map[string]bool) {
		for _, name := range l.orderedSignals(container) {
			if skip[name] || seen[name] {
				continue
			}
			if sig, ok := container.Children[name]; ok {
				if def := signalDefinitionAST(sig); def != nil {
					inDefs = append(inDefs, def)
					seen[name] = true
				}
			}
		}
	}
	appendSignals(leftInputs, nil)
	appendSignals(rightInputs, intermediates)

	var outDefs []parser.Definition
	seenOut := map[string]bool{}
	fusedSet := map[string]bool{}
	for _, name := range fused {
		fusedSet[name] = true
	}
	appendOutputs := func(container *index.ProjectNode, skip map[string]bool) bool {
		for _, name := range l.orderedSignals(container) {
			if skip[name] || seenOut[name] {
				continue
			}
			sig, ok := container.Children[name]
			if !ok {
				return false
			}
			if def := signalDefinitionAST(sig); def != nil {
				outDefs = append(outDefs, def)
				seenOut[name] = true
			}
		}
		return true
	}
	if !appendOutputs(leftOutputs, fusedSet) || !appendOutputs(rightOutputs, nil) {
		return false
	}

	// Rebuild the left GAM with the fused inputs, outputs and expression.
	pos := left.Fragments[0].ObjectPos
	defs := []parser.Definition{
		&parser.Field{Position: pos, Name: "Class", Value: &parser.ReferenceValue{Position: pos, Value: nodeClass(left)}},
		&parser.ObjectNode{Position: pos, Name: &parser.ReferenceValue{Position: pos, Value: "InputSignals"}, Subnode: parser.Subnode{Definitions: inDefs}},
		&parser.ObjectNode{Position: pos, Name: &parser.ReferenceValue{Position: pos, Value: "OutputSignals"}, Subnode: parser.Subnode{Definitions: outDefs}},
		&parser.Field{Position: pos, Name: "Expression", Value: &parser.StringValue{Position: pos, Value: newExpression, Quoted: true}},
	}
	// Rewrite the definition in the owning fragment as well: the writer and
	// the validator read the AST definitions, not just the node.
	var fusedObj *parser.ObjectNode
	if left.Parent != nil {
		for _, frag := range left.Parent.Fragments {
			for i, def := range frag.Definitions {
				if obj, ok := def.(*parser.ObjectNode); ok &&
					index.NormalizeName(literalText(obj.Name)) == index.NormalizeName(left.RealName) {
					fusedObj = &parser.ObjectNode{
						Position: obj.Position,
						Name:     obj.Name,
						Subnode:  parser.Subnode{Definitions: defs},
					}
					frag.Definitions[i] = fusedObj
				}
			}
		}
	}
	l.rebuildNode(left, fusedObj, defs)
	return true
}

// consumerCount counts how many GAM input signals reference (ds, signal).
func (l *lowerer) consumerCount(ds, signal string) int {
	count := 0
	l.tree.Walk(func(n *index.ProjectNode) {
		if !l.tree.IsGAM(n) {
			return
		}
		inputs, ok := n.Children["InputSignals"]
		if !ok {
			return
		}
		for _, sig := range inputs.Children {
			ref, err := l.signalRefOf(sig)
			if err != nil {
				continue
			}
			if ref.ds == ds && ref.signal == signal {
				count++
			}
		}
	})
	return count
}

// signalDefinitionAST rebuilds the AST of a signal declaration.
func signalDefinitionAST(sig *index.ProjectNode) *parser.ObjectNode {
	name := sig.RealName
	if name == "" {
		name = sig.Name
	}
	if strings.HasPrefix(name, "+") {
		return nil
	}
	obj := &parser.ObjectNode{Position: sig.Fragments[0].ObjectPos, Name: &parser.ReferenceValue{Position: sig.Fragments[0].ObjectPos, Value: name}}
	for _, def := range sig.Fragments[0].Definitions {
		if f, ok := def.(*parser.Field); ok {
			obj.Subnode.Definitions = append(obj.Subnode.Definitions, f)
		}
	}
	return obj
}

// removeGAMDefinition drops a GAM's definition and tree entry.
func (l *lowerer) removeGAMDefinition(n *index.ProjectNode) {
	parent := n.Parent
	if parent == nil {
		return
	}
	name := index.NormalizeName(n.RealName)
	for _, frag := range parent.Fragments {
		kept := frag.Definitions[:0]
		for _, def := range frag.Definitions {
			if obj, ok := def.(*parser.ObjectNode); ok && index.NormalizeName(literalText(obj.Name)) == name {
				continue
			}
			kept = append(kept, def)
		}
		frag.Definitions = kept
	}
	delete(parent.Children, n.Name)
}

// rebuildNode replaces a node's body with defs. source is the definition
// the fragment belongs to (the validator matches fragments by Source).
func (l *lowerer) rebuildNode(node *index.ProjectNode, source *parser.ObjectNode, defs []parser.Definition) {
	pos := parser.Position{}
	if len(node.Fragments) > 0 {
		pos = node.Fragments[0].ObjectPos
	}
	node.Fragments = nil
	node.Children = map[string]*index.ProjectNode{}
	node.Fields = map[string][]index.EvaluatedField{}
	frag := &index.Fragment{File: node.File, Definitions: defs, IsObject: true, ObjectPos: pos}
	if source != nil {
		frag.Source = source
	}
	node.Fragments = append(node.Fragments, frag)
	if l.b.activeFragments != nil {
		l.b.activeFragments[frag] = true
	}
	for _, def := range defs {
		l.indexDefinition(node, def, frag.File)
	}
}

func quoteAll(names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = regexp.QuoteMeta(n)
	}
	return out
}

var _ = fmt.Sprintf
