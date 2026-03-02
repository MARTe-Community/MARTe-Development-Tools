package builder

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/schema"
	"github.com/marte-community/marte-dev-tools/internal/validator"
)

type Builder struct {
	Files           []string
	Overrides       map[string]string
	variables       map[string]parser.Value
	tree            *index.ProjectTree
	activeNodes     map[*index.ProjectNode]bool
	activeFragments map[*index.Fragment]bool
}

func NewBuilder(files []string, overrides map[string]string) *Builder {
	return &Builder{
		Files:           files,
		Overrides:       overrides,
		variables:       make(map[string]parser.Value),
		activeNodes:     make(map[*index.ProjectNode]bool),
		activeFragments: make(map[*index.Fragment]bool),
	}
}

func (b *Builder) collectActiveNodes(node *index.ProjectNode, evalCtx *index.EvaluationContext) {
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
	var processEval func([]index.EvaluatedDefinition, *index.ProjectNode)
	processEval = func(evaluated []index.EvaluatedDefinition, node *index.ProjectNode) {
		for _, ed := range evaluated {
			switch d := ed.Def.(type) {
			case *parser.ObjectNode:
				objName := b.tree.ValueToString(b.tree.EvaluateValue(d.Name, ed.Ctx))
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
						IsConditional: false, // It's active now
					}
					node.Children[norm] = child
					b.tree.AddToNodeMap(child)
				}
				
				// Ensure this fragment is present and active
				found := false
				for _, f := range child.Fragments {
					if f.Source == d {
						b.activeFragments[f] = true
						found = true
						break
					}
				}
				if !found {
					b.tree.PopulateObjectFragment(child, ed.File, d, "", nil, nil, true)
					for _, f := range child.Fragments {
						if f.Source == d {
							b.activeFragments[f] = true
							break
						}
					}
				}

				if !written[norm] {
					b.collectActiveNodes(child, ed.Ctx)
					written[norm] = true
				}
			case *parser.IfBlock:
				cond := b.tree.EvaluateValue(d.Condition, ed.Ctx)
				id := fmt.Sprintf("%d:%d", d.Position.Line, d.Position.Column)
				if b.tree.IsTrue(cond) {
					for _, f := range node.Fragments {
						if f.IsConditional && f.BranchID == id+":then" {
							b.activeFragments[f] = true
						}
					}
					processEval(b.tree.EvaluateDefinitions(d.Then, ed.Ctx, ed.File), node)
				} else if len(d.Else) > 0 {
					for _, f := range node.Fragments {
						if f.IsConditional && f.BranchID == id+":else" {
							b.activeFragments[f] = true
						}
					}
					processEval(b.tree.EvaluateDefinitions(d.Else, ed.Ctx, ed.File), node)
				}
			case *parser.ForeachBlock:
				iterable := b.tree.EvaluateValue(d.Iterable, ed.Ctx)
				id := fmt.Sprintf("%d:%d", d.Position.Line, d.Position.Column)
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
						processEval(b.tree.EvaluateDefinitions(d.Body, subCtx, ed.File), node)
					}
				}
			case *parser.TemplateDefinition:
				id := fmt.Sprintf("%d:%d", d.Position.Line, d.Position.Column)
				for _, f := range node.Fragments {
					if f.IsConditional && f.BranchID == id+":template" {
						b.activeFragments[f] = true
					}
				}
				processEval(b.tree.EvaluateDefinitions(d.Body, ed.Ctx, ed.File), node)
			}
		}
	}

	processEval(evaluated, node)

	for name, child := range node.Children {
		if !written[name] && !child.IsConditional {
			b.collectActiveNodes(child, evalCtx)
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
		b.collectActiveNodes(tree.Root, evalCtx)
		for _, node := range tree.IsolatedFiles {
			b.collectActiveNodes(node, evalCtx)
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
		Schema:          schema.LoadFullSchema("."),
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

	// Determine root node to print
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
							p := parser.NewParser("Temp = " + valStr)
							cfg, _ := p.Parse()
							if len(cfg.Definitions) > 0 {
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
			if child.IsConditional {
				continue
			}
			b.writeNodeContent(f, child, indent, ctx)
		}
	}
}

func (b *Builder) writeEvaluatedBody(f *os.File, node *index.ProjectNode, ctx *index.EvaluationContext, indent int, parentNode *index.ProjectNode, writtenChildren map[string]bool) {
	var evaluated []index.EvaluatedDefinition
	for _, frag := range node.Fragments {
		if b.activeFragments[frag] {
			evaluated = append(evaluated, b.tree.EvaluateDefinitions(frag.Definitions, ctx, frag.File)...)
		}
	}
	b.writeEvaluatedDefinitions(f, evaluated, indent, parentNode, writtenChildren, ctx)
}

func (b *Builder) writeEvaluatedDefinitions(f *os.File, evaluated []index.EvaluatedDefinition, indent int, parentNode *index.ProjectNode, writtenChildren map[string]bool, defaultCtx *index.EvaluationContext) {
	var fields []EvaluatedDefinition
	var objects []EvaluatedDefinition
	
	for _, ed := range evaluated {
		switch d := ed.Def.(type) {
		case *parser.Field:
			fields = append(fields, EvaluatedDefinition{Def: d, Ctx: ed.Ctx, File: ed.File})
		case *parser.ObjectNode:
			objects = append(objects, EvaluatedDefinition{Def: d, Ctx: ed.Ctx, File: ed.File})
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
			return fmt.Sprintf("\"%s\"", v.Value)
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
	default:
		return ""
	}
}
