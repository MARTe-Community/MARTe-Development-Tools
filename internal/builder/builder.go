package builder

import (
	"context"
	"fmt"
	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/loader"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/schema"
	"github.com/marte-community/marte-dev-tools/internal/validator"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Builder struct {
	Files           []string
	Overrides       map[string]string
	ProjectRoot     string // Directory containing .marte_schema.cue (derived from Files)
	variables       map[string]parser.Value
	tree            *index.ProjectTree
	activeNodes     map[*index.ProjectNode]bool
	activeFragments map[*index.Fragment]bool
}

func NewBuilder(files []string, overrides map[string]string) *Builder {
	root := "."
	if len(files) > 0 {
		if d := filepath.Dir(files[0]); d != "" {
			root = d
		}
	}
	return &Builder{
		Files:           files,
		Overrides:       overrides,
		ProjectRoot:     root,
		variables:       make(map[string]parser.Value),
		activeNodes:     make(map[*index.ProjectNode]bool),
		activeFragments: make(map[*index.Fragment]bool),
	}
}

func (b *Builder) collectActiveNodes(node *index.ProjectNode, evalCtx *index.EvaluationContext, forceUncond bool) {
	b.activeNodes[node] = true
	for _, frag := range node.Fragments {
		if !frag.IsConditional {
			b.activeFragments[frag] = true
		}
	}

	var evaluated []index.EvaluatedDefinition
	for _, frag := range node.Fragments {
		if b.activeFragments[frag] {
			evaluated = append(evaluated, b.tree.EvaluateDefinitions(frag.Definitions, evalCtx, frag.File)...)
		}
	}

	written := make(map[string]bool)
	// forceUncond: definitions in this expansion are unconditional
	// (true inside `with` blocks, which always load their document).
	var processEval func([]index.EvaluatedDefinition, *index.ProjectNode, bool)
	processEval = func(evaluated []index.EvaluatedDefinition, node *index.ProjectNode, forceUncond bool) {
		for _, ed := range evaluated {
			switch d := ed.Def.(type) {
			case *parser.SignalShorthand:
				nodeName := d.SignalName
				if d.AliasName != "" {
					nodeName = d.AliasName
				}
				norm := index.NormalizeName(nodeName)
				child, ok := node.Children[norm]
				if ok && !written[norm] {
					for _, f := range child.Fragments {
						if f.Source == parser.Definition(d) {
							b.activeFragments[f] = true
							if f.EvalCtx == nil {
								f.EvalCtx = ed.Ctx
							}
							break
						}
					}
					b.collectActiveNodes(child, ed.Ctx, forceUncond)
					written[norm] = true
				}
			case *parser.ObjectNode:
				objName := b.tree.ValueToString(b.tree.EvaluateValue(d.Name, ed.Ctx))
				if strings.Contains(objName, "@") || objName == "" {
					// Unresolvable at this stage: the bound expansion
					// (loop iteration / with binding) creates the real
					// object; skip the raw residual here.
					continue
				}
				norm := index.NormalizeName(objName)

				// Find or create the child node
				child, ok := node.Children[norm]
				if !ok {
					child = &index.ProjectNode{
						Name:          norm,
						RealName:      objName,
						Children:      make(map[string]*index.ProjectNode),
						Parent:        node,
						Metadata:      make(map[string]string),
						Variables:     make(map[string]index.VariableInfo),
						Fields:        make(map[string][]index.EvaluatedField),
						IsConditional: !forceUncond, // It's active now
					}
					node.Children[norm] = child
					b.tree.AddToNodeMap(child)
				}
				if forceUncond {
					// E.g. materialized inside a `with` block: the
					// block always loads, so the child is not
					// conditional even if an earlier pass marked it.
					child.IsConditional = false
				}

				// Ensure this fragment is present and active
				found := false
				for _, f := range child.Fragments {
					if f.Source == parser.Definition(d) {
						b.activeFragments[f] = true
						if f.EvalCtx == nil {
							f.EvalCtx = ed.Ctx
						}
						found = true
						break
					}
				}
				if !found {
					b.tree.PopulateObjectFragment(child, ed.File, d, "", nil, nil, !forceUncond)
					for _, f := range child.Fragments {
						if f.Source == d {
							b.activeFragments[f] = true
							if f.EvalCtx == nil {
								f.EvalCtx = ed.Ctx
							}
							break
						}
					}
				}

				if !written[norm] {
					b.collectActiveNodes(child, ed.Ctx, forceUncond)
					written[norm] = true
				}
			case *parser.IfBlock:
				cond := b.tree.EvaluateValue(d.Condition, ed.Ctx)
				id := fmt.Sprintf("%d:%d", d.Position.Line, d.Position.Column)
				if b.tree.IsTrue(cond) {
					for _, f := range node.Fragments {
						if f.IsConditional && f.BranchID == id+":then" {
							b.activeFragments[f] = true
						} else if f.IsConditional {
							b.activeFragments[f] = false
						}
					}
					processEval(b.tree.EvaluateDefinitions(d.Then, ed.Ctx, ed.File), node, false)
				} else {
					matched := false
					for i, ei := range d.ElseIf {
						elseifCond := b.tree.EvaluateValue(ei.Condition, ed.Ctx)
						if b.tree.IsTrue(elseifCond) {
							branchID := fmt.Sprintf("%s:elseif%d", id, i)
							for _, f := range node.Fragments {
								if f.IsConditional && f.BranchID == branchID {
									b.activeFragments[f] = true
								}
							}
							processEval(b.tree.EvaluateDefinitions(ei.Body, ed.Ctx, ed.File), node, false)
							matched = true
							break
						}
					}
					if !matched && len(d.Else) > 0 {
						for _, f := range node.Fragments {
							if f.IsConditional && f.BranchID == id+":else" {
								b.activeFragments[f] = true
							}
						}
						processEval(b.tree.EvaluateDefinitions(d.Else, ed.Ctx, ed.File), node, false)
					}
				}
			case *parser.ForeachBlock:
				iterable := b.tree.EvaluateValue(d.Iterable, ed.Ctx)
				id := fmt.Sprintf("%d:%d", d.Position.Line, d.Position.Column)
				if m, ok := iterable.(*parser.MapValue); ok {
					// Dict iteration: two-variable foreach binds
					// key and value of every member.
					for _, f := range node.Fragments {
						if f.IsConditional && f.BranchID == id+":body" {
							b.activeFragments[f] = true
						}
					}
					for _, key := range m.Keys {
						subCtx := &index.EvaluationContext{
							Variables: make(map[string]parser.Value),
							Parent:    ed.Ctx,
							Tree:      b.tree,
						}
						if d.KeyVar != "" {
							subCtx.Variables[d.KeyVar] = &parser.StringValue{Value: key, Quoted: true}
						}
						if d.ValueVar != "" {
							subCtx.Variables[d.ValueVar] = m.Values[key]
						}
						processEval(b.tree.EvaluateDefinitions(d.Body, subCtx, ed.File), node, forceUncond)
					}
				}
				if arr, ok := iterable.(*parser.ArrayValue); ok {
					for _, f := range node.Fragments {
						if f.IsConditional && f.BranchID == id+":body" {
							b.activeFragments[f] = true
						}
					}
					for i, val := range arr.Elements {
						subCtx := &index.EvaluationContext{
							Variables: make(map[string]parser.Value),
							Parent:    ed.Ctx,
							Tree:      b.tree,
						}
						if d.KeyVar != "" {
							subCtx.Variables[d.KeyVar] = &parser.IntValue{Value: int64(i), Raw: fmt.Sprintf("%d", i)}
						}
						if d.ValueVar != "" {
							subCtx.Variables[d.ValueVar] = val
						}
						processEval(b.tree.EvaluateDefinitions(d.Body, subCtx, ed.File), node, forceUncond)
					}
				}
			case *parser.WithBlock:
				resolved := loader.ResolvePath(ed.File, b.tree.ValueToString(b.tree.EvaluateValue(d.Path, ed.Ctx)))
				val, err := loader.Load(d.Format, resolved)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Warning: with %s(%q): %v\n", d.Format, resolved, err)
					continue
				}
				id := fmt.Sprintf("%d:%d", d.Position.Line, d.Position.Column)
				for _, f := range node.Fragments {
					if f.IsConditional && f.BranchID == id+":with" {
						b.activeFragments[f] = true
					}
				}
				subCtx := &index.EvaluationContext{
					Variables: map[string]parser.Value{d.BindName: val},
					Parent:    ed.Ctx,
					Tree:      b.tree,
				}
				// The with block always loads its document: the body's
				// definitions are unconditional.
				processEval(b.tree.EvaluateDefinitions(d.Body, subCtx, ed.File), node, true)
			case *parser.TemplateDefinition:
				id := fmt.Sprintf("%d:%d", d.Position.Line, d.Position.Column)
				for _, f := range node.Fragments {
					if f.IsConditional && f.BranchID == id+":template" {
						b.activeFragments[f] = true
					}
				}
				processEval(b.tree.EvaluateDefinitions(d.Body, ed.Ctx, ed.File), node, forceUncond)
			}
		}
	}

	processEval(evaluated, node, forceUncond)

	for name, child := range node.Children {
		if !written[name] && !child.IsConditional {
			b.collectActiveNodes(child, evalCtx, forceUncond)
		}
	}
}

func (b *Builder) Build(f *os.File) error {
	// Build the Project Tree
	tree := index.NewProjectTree()
	b.tree = tree

	var expectedProject string
	var projectSet bool

	for _, file := range b.Files {
		content, err := os.ReadFile(file)
		if err != nil {
			return err
		}

		p := parser.NewParser(string(content))
		config, err := p.Parse()
		if err != nil {
			return fmt.Errorf("error parsing %s: %v", file, err)
		}

		// Check Namespace/Project Consistency
		proj := ""
		if config.Package != nil {
			parts := strings.Split(config.Package.URI, ".")
			if len(parts) > 0 {
				proj = strings.TrimSpace(parts[0])
			}
		}

		if !projectSet {
			expectedProject = proj
			projectSet = true
		} else if proj != expectedProject {
			return fmt.Errorf("multiple namespaces defined in sources: found '%s' and '%s'", expectedProject, proj)
		}

		tree.AddFile(file, config)
	}

	b.collectVariables(tree)
	tree.ResolveFields(nil)
	tree.ResolveReferences(nil)

	// Multi-pass active node collection
	for pass := 0; pass < 5; pass++ {
		evalCtx := &index.EvaluationContext{Variables: make(map[string]parser.Value), Tree: b.tree}
		for k, v := range b.variables {
			evalCtx.Variables[k] = v
		}

		// Refresh variables from tree (might have new ones from newly activated fragments)
		tree.Walk(func(n *index.ProjectNode) {
			for k, varInfo := range n.Variables {
				if _, ok := b.variables[k]; !ok || varInfo.Def.IsConst {
					b.variables[k] = varInfo.Def.DefaultValue
				}
			}
		})
		// Re-apply overrides
		b.collectVariables(tree) // This re-parses overrides

		prevCount := len(b.activeFragments)
		b.collectActiveNodes(tree.Root, evalCtx, false)
		for _, node := range tree.IsolatedFiles {
			b.collectActiveNodes(node, evalCtx, false)
		}

		// Re-resolve only active things after activation pass
		tree.ResolveFields(b.activeFragments)
		tree.ResolveReferences(b.activeFragments)

		if len(b.activeFragments) == prevCount {
			break
		}
	}

	evalCtx := &index.EvaluationContext{Variables: make(map[string]parser.Value), Tree: b.tree}
	for k, v := range b.variables {
		evalCtx.Variables[k] = v
	}

	// Validate before building to ensure ActiveFragments are consistent and fields resolved
	v := &validator.Validator{
		Tree:            tree,
		ActiveFragments: b.activeFragments,
		ActiveNodes:     make(map[*index.ProjectNode]bool),
		Variables:       b.variables,
		Overrides:       make(map[string]parser.Value),
		Schema:          schema.LoadFullSchema(b.ProjectRoot),
	}
	v.ValidateProject(context.Background())
	if len(v.Diagnostics) > 0 {
		hasError := false
		for _, d := range v.Diagnostics {
			if d.Level == validator.LevelError {
				hasError = true
				break
			}
		}
		if hasError {
			// Print errors to stderr but we might want to continue if it's just warnings
			// Actually Build should probably fail on errors.
			// v.ValidateProject already prints to log if using logger?
			// No, it just populates Diagnostics.
		}
	}

	if expectedProject == "" {
		// Sort keys for deterministic order
		var isoPaths []string
		for path := range tree.IsolatedFiles {
			isoPaths = append(isoPaths, path)
		}
		sort.Strings(isoPaths)

		for _, path := range isoPaths {
			iso := tree.IsolatedFiles[path]
			tree.Root.Fragments = append(tree.Root.Fragments, iso.Fragments...)
			for name, child := range iso.Children {
				if existing, ok := tree.Root.Children[name]; ok {
					b.mergeNodes(existing, child)
				} else {
					tree.Root.Children[name] = child
					child.Parent = tree.Root
				}
			}
		}
	}

	rootNode := tree.Root
	if expectedProject != "" {
		if node, ok := tree.Root.Children[expectedProject]; ok {
			rootNode = node
		} else {
			return fmt.Errorf("project '%s' not found in indexed tree", expectedProject)
		}
	}

	b.writeNodeBody(f, rootNode, 0, nil)

	return nil
}

// childHasActiveFragment reports whether any fragment of the node is
// active. Such nodes are real runtime objects even when they were
// indexed as conditional.
func (b *Builder) childHasActiveFragment(node *index.ProjectNode) bool {
	for _, frag := range node.Fragments {
		if b.activeFragments[frag] {
			return true
		}
	}
	return false
}

func (b *Builder) writeNodeContent(f *os.File, node *index.ProjectNode, indent int, ctx *index.EvaluationContext) {
	indentStr := strings.Repeat("  ", indent)

	// If this node has a RealName (e.g. +App), we print it as an object definition
	if node.RealName != "" {
		fmt.Fprintf(f, "%s%s = {\n", indentStr, node.RealName)
		indent++
	}

	b.writeNodeBody(f, node, indent, ctx)

	if node.RealName != "" {
		indent--
		indentStr = strings.Repeat("  ", indent)
		fmt.Fprintf(f, "%s}\n", indentStr)
	}
}

func (b *Builder) mergeNodes(dest, src *index.ProjectNode) {
	dest.Fragments = append(dest.Fragments, src.Fragments...)
	for name, child := range src.Children {
		if existing, ok := dest.Children[name]; ok {
			b.mergeNodes(existing, child)
		} else {
			dest.Children[name] = child
			child.Parent = dest
		}
	}
}

func hasClass(frag *index.Fragment) bool {
	for _, def := range frag.Definitions {
		if f, ok := def.(*parser.Field); ok && f.Name == "Class" {
			return true
		}
	}
	return false
}

func (b *Builder) collectVariables(tree *index.ProjectTree) {
	processNode := func(n *index.ProjectNode) {
		for _, frag := range n.Fragments {
			for _, def := range frag.Definitions {
				if vdef, ok := def.(*parser.VariableDefinition); ok {
					if valStr, ok := b.Overrides[vdef.Name]; ok {
						if !vdef.IsConst {
							// fmt.Printf("[DEBUG-BUILDER] Checking %s, type=%s, val=%s\n", vdef.Name, vdef.TypeExpr, valStr)
							if shouldAutoQuoteWithDef(valStr, vdef) {
								// fmt.Printf("[DEBUG-BUILDER] Auto-quoting %s\n", vdef.Name)
								valStr = "\"" + valStr + "\""
							}
							p := parser.NewParser("Temp = " + valStr)
							cfg, err := p.Parse()
							if err != nil {
								fmt.Fprintf(os.Stderr, "Warning: failed to parse variable override for %s: %v\n", vdef.Name, err)
							} else if len(cfg.Definitions) > 0 {
								if f, ok := cfg.Definitions[0].(*parser.Field); ok {
									b.variables[vdef.Name] = f.Value
									continue
								}
							}
						}
					}
					if vdef.DefaultValue != nil {
						if _, ok := b.variables[vdef.Name]; !ok || vdef.IsConst {
							b.variables[vdef.Name] = vdef.DefaultValue
						}
					}
				}
			}
		}
	}
	tree.Walk(processNode)
}

func shouldAutoQuoteWithDef(valStr string, def *parser.VariableDefinition) bool {
	if strings.HasPrefix(valStr, "\"") && strings.HasSuffix(valStr, "\"") {
		return false
	}
	typeExpr := def.TypeExpr
	if strings.Contains(typeExpr, "string") || strings.Contains(typeExpr, "char8") {
		return true
	}
	if strings.Contains(typeExpr, "|") && strings.Contains(typeExpr, "\"") {
		return true
	}
	if strings.HasPrefix(typeExpr, "\"") && strings.HasSuffix(typeExpr, "\"") {
		return true
	}
	// Check default value
	if def.DefaultValue != nil {
		if _, ok := def.DefaultValue.(*parser.StringValue); ok {
			return true
		}
	}
	return false
}

type EvaluatedDefinition struct {
	Def  parser.Definition
	Ctx  *index.EvaluationContext
	File string
}

func (b *Builder) writeNodeBody(f *os.File, node *index.ProjectNode, indent int, ctx *index.EvaluationContext) {
	if ctx == nil {
		ctx = &index.EvaluationContext{Variables: make(map[string]parser.Value), Tree: b.tree}
		for k, v := range b.variables {
			ctx.Variables[k] = v
		}
	}

	written := make(map[string]bool)
	b.writeEvaluatedBody(f, node, ctx, indent, node, written)

	// Write remaining children (e.g. packages implicit nodes)
	var childNames []string
	for name := range node.Children {
		childNames = append(childNames, name)
	}
	sort.Strings(childNames)

	for _, name := range childNames {
		if !written[name] {
			child := node.Children[name]
			if child.IsConditional && !b.childHasActiveFragment(child) {
				continue
			}
			if strings.Contains(child.RealName, "@") {
				// Leftover from a definition whose dynamic name could
				// not be resolved during indexing; the resolved
				// iterations are written through the evaluated path.
				continue
			}
			b.writeNodeContent(f, child, indent, ctx)
		}
	}
}

func (b *Builder) writeEvaluatedBody(f *os.File, node *index.ProjectNode, ctx *index.EvaluationContext, indent int, parentNode *index.ProjectNode, writtenChildren map[string]bool) {
	var evaluated []index.EvaluatedDefinition
	for _, frag := range node.Fragments {
		if !b.activeFragments[frag] {
			continue
		}
		c := ctx
		if frag.EvalCtx != nil {
			// Fragment materialized under a loop iteration or template
			// expansion: evaluate with those bindings so field values
			// referencing loop/parameter variables resolve.
			c = frag.EvalCtx
		}
		evaluated = append(evaluated, b.tree.EvaluateDefinitions(frag.Definitions, c, frag.File)...)
	}
	b.writeEvaluatedDefinitions(f, evaluated, indent, parentNode, writtenChildren, ctx)
}

func (b *Builder) writeEvaluatedDefinitions(f *os.File, evaluated []index.EvaluatedDefinition, indent int, parentNode *index.ProjectNode, writtenChildren map[string]bool, defaultCtx *index.EvaluationContext) {
	var fields []EvaluatedDefinition
	var objects []EvaluatedDefinition

	var shorthands []EvaluatedDefinition
	for _, ed := range evaluated {
		switch d := ed.Def.(type) {
		case *parser.Field:
			fields = append(fields, EvaluatedDefinition{Def: d, Ctx: ed.Ctx, File: ed.File})
		case *parser.ObjectNode:
			objects = append(objects, EvaluatedDefinition{Def: d, Ctx: ed.Ctx, File: ed.File})
		case *parser.SignalShorthand:
			_ = d
			shorthands = append(shorthands, EvaluatedDefinition{Def: ed.Def, Ctx: ed.Ctx, File: ed.File})
		}
	}

	sort.SliceStable(fields, func(i, j int) bool {
		return fields[i].Def.(*parser.Field).Name == "Class" && fields[j].Def.(*parser.Field).Name != "Class"
	})

	for _, field := range fields {
		b.writeField(f, field.Def.(*parser.Field), field.Ctx, indent)
	}

	if writtenChildren == nil {
		writtenChildren = make(map[string]bool)
	}

	for _, obj := range objects {
		objectNode := obj.Def.(*parser.ObjectNode)
		objName := b.formatValueWithCtx(objectNode.Name, obj.Ctx)

		// If name still has variables, skip it. It will be rendered as a resolved child.
		if strings.Contains(objName, "@") {
			continue
		}

		// Attempt to resolve merged node if we have a parent context
		if parentNode != nil {
			cleanedName := objName
			if strings.HasPrefix(cleanedName, "\"") && strings.HasSuffix(cleanedName, "\"") && len(cleanedName) >= 2 {
				cleanedName = cleanedName[1 : len(cleanedName)-1]
			}

			norm := index.NormalizeName(cleanedName)
			if child, ok := parentNode.Children[norm]; ok {
				if !writtenChildren[norm] {
					b.writeNodeContent(f, child, indent, obj.Ctx)
					writtenChildren[norm] = true
				}
				continue
			}
		}

		b.writeEvaluatedObject(f, objectNode, obj.Ctx, indent, obj.File)
	}

	// Emit SignalShorthand entries as standard MARTe signal blocks.
	indentStr := strings.Repeat("  ", indent)
	for _, sh := range shorthands {
		d := sh.Def.(*parser.SignalShorthand)
		nodeName := d.SignalName
		if d.AliasName != "" {
			nodeName = d.AliasName
		}
		norm := index.NormalizeName(nodeName)

		// Use the indexed child node when available (handles merging).
		if parentNode != nil {
			if child, ok := parentNode.Children[norm]; ok {
				if !writtenChildren[norm] {
					b.writeNodeContent(f, child, indent, sh.Ctx)
					writtenChildren[norm] = true
				}
				continue
			}
		}

		// Fallback: write directly from shorthand fields.
		fmt.Fprintf(f, "%s%s = {\n", indentStr, nodeName)
		if d.DataSource != "" {
			fmt.Fprintf(f, "%s  DataSource = %s\n", indentStr, d.DataSource)
		}
		if d.AliasName != "" {
			fmt.Fprintf(f, "%s  Alias = %s\n", indentStr, d.SignalName)
		}
		if d.Type != "" {
			fmt.Fprintf(f, "%s  Type = %s\n", indentStr, d.Type)
		}
		if d.NumElements != nil {
			fmt.Fprintf(f, "%s  NumberOfElements = %s\n", indentStr, b.formatValueWithCtx(d.NumElements, sh.Ctx))
		}
		if d.HasExtraFields {
			for _, def := range d.ExtraFields.Definitions {
				if fld, ok := def.(*parser.Field); ok {
					b.writeField(f, fld, sh.Ctx, indent+1)
				}
			}
		}
		fmt.Fprintf(f, "%s}\n", indentStr)
	}
}

func (b *Builder) writeField(f *os.File, field *parser.Field, ctx *index.EvaluationContext, indent int) {
	indentStr := strings.Repeat("  ", indent)
	fmt.Fprintf(f, "%s%s = %s\n", indentStr, field.Name, b.formatValueWithCtx(field.Value, ctx))
}

func (b *Builder) writeEvaluatedObject(f *os.File, obj *parser.ObjectNode, ctx *index.EvaluationContext, indent int, file string) {
	indentStr := strings.Repeat("  ", indent)
	objName := b.formatValueWithCtx(obj.Name, ctx)
	fmt.Fprintf(f, "%s%s = {\n", indentStr, objName)

	evaluated := b.tree.EvaluateDefinitions(obj.Subnode.Definitions, ctx, file)
	b.writeEvaluatedDefinitions(f, evaluated, indent+1, nil, nil, ctx)

	fmt.Fprintf(f, "%s}\n", indentStr)
}

func (b *Builder) formatValueWithCtx(val parser.Value, ctx *index.EvaluationContext) string {
	val = b.tree.EvaluateValue(val, ctx)
	switch v := val.(type) {
	case *parser.StringValue:
		if v.Quoted {
			return fmt.Sprintf("\"%s\"", parser.EscapeString(v.Value))
		}
		return v.Value
	case *parser.IntValue:
		return v.Raw
	case *parser.FloatValue:
		return v.Raw
	case *parser.BoolValue:
		return fmt.Sprintf("%v", v.Value)
	case *parser.VariableReferenceValue:
		return v.Name
	case *parser.ReferenceValue:
		return v.Value
	case *parser.ArrayValue:
		elements := []string{}
		for _, e := range v.Elements {
			elements = append(elements, b.formatValueWithCtx(e, ctx))
		}
		return fmt.Sprintf("{ %s }", strings.Join(elements, " "))
	case *parser.MemberAccess:
		res := b.tree.EvaluateValue(v, ctx)
		return b.formatValueWithCtx(res, ctx)
	case *parser.MapValue:
		parts := []string{}
		for _, k := range v.Keys {
			parts = append(parts, fmt.Sprintf("%s = %s", k, b.formatValueWithCtx(v.Values[k], ctx)))
		}
		return fmt.Sprintf("{ %s }", strings.Join(parts, " "))
	case *parser.ConditionalArrayElements:
		cond := b.tree.EvaluateValue(v.Condition, ctx)
		if b.tree.IsTrue(cond) {
			parts := []string{}
			for _, e := range v.Then {
				parts = append(parts, b.formatValueWithCtx(e, ctx))
			}
			return strings.Join(parts, " ")
		}
		for _, ei := range v.ElseIf {
			elseifCond := b.tree.EvaluateValue(ei.Condition, ctx)
			if b.tree.IsTrue(elseifCond) {
				parts := []string{}
				for _, e := range ei.Body {
					parts = append(parts, b.formatValueWithCtx(e, ctx))
				}
				return strings.Join(parts, " ")
			}
		}
		parts := []string{}
		for _, e := range v.Else {
			parts = append(parts, b.formatValueWithCtx(e, ctx))
		}
		return strings.Join(parts, " ")
	default:
		return ""
	}
}
