package builder

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
)

type Builder struct {
	Files     []string
	Overrides map[string]string
	variables map[string]parser.Value
	tree      *index.ProjectTree
}

func NewBuilder(files []string, overrides map[string]string) *Builder {
	return &Builder{
		Files:     files,
		Overrides: overrides,
		variables: make(map[string]parser.Value),
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

	b.writeNodeBody(f, rootNode, 0)

	return nil
}

func (b *Builder) writeNodeContent(f *os.File, node *index.ProjectNode, indent int) {
	indentStr := strings.Repeat("  ", indent)

	// If this node has a RealName (e.g. +App), we print it as an object definition
	if node.RealName != "" {
		fmt.Fprintf(f, "%s%s = {\n", indentStr, node.RealName)
		indent++
	}

	b.writeNodeBody(f, node, indent)

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

func (b *Builder) writeNodeBody(f *os.File, node *index.ProjectNode, indent int) {
	ctx := &index.EvaluationContext{Variables: make(map[string]parser.Value), Tree: b.tree}
	for k, v := range b.variables {
		ctx.Variables[k] = v
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
			b.writeNodeContent(f, child, indent)
		}
	}
}

func (b *Builder) writeEvaluatedBody(f *os.File, node *index.ProjectNode, ctx *index.EvaluationContext, indent int, parentNode *index.ProjectNode, writtenChildren map[string]bool) {
	var evaluated []index.EvaluatedDefinition
	for _, frag := range node.Fragments {
		evaluated = append(evaluated, b.tree.EvaluateDefinitions(frag.Definitions, ctx, frag.File)...)
	}
	b.writeEvaluatedDefinitions(f, evaluated, indent, parentNode, writtenChildren)
}

func (b *Builder) writeEvaluatedDefinitions(f *os.File, evaluated []index.EvaluatedDefinition, indent int, parentNode *index.ProjectNode, writtenChildren map[string]bool) {
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

		// Attempt to resolve merged node if we have a parent context
		if parentNode != nil {
			objName := b.formatValueWithCtx(objectNode.Name, obj.Ctx)
			if strings.HasPrefix(objName, "\"") && strings.HasSuffix(objName, "\"") && len(objName) >= 2 {
				objName = objName[1 : len(objName)-1]
			}

			norm := index.NormalizeName(objName)
			if child, ok := parentNode.Children[norm]; ok {
				if !writtenChildren[norm] {
					b.writeNodeContent(f, child, indent)
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
	b.writeEvaluatedDefinitions(f, evaluated, indent+1, nil, nil)

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
