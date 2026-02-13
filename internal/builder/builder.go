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
	templates map[string]*parser.TemplateDefinition
}

func NewBuilder(files []string, overrides map[string]string) *Builder {
	return &Builder{
		Files:     files,
		Overrides: overrides,
		variables: make(map[string]parser.Value),
		templates: make(map[string]*parser.TemplateDefinition),
	}
}

type evaluationContext struct {
	variables map[string]parser.Value
	parent    *evaluationContext
}

func (c *evaluationContext) resolve(name string) parser.Value {
	if v, ok := c.variables[name]; ok {
		return v
	}
	if c.parent != nil {
		return c.parent.resolve(name)
	}
	return nil
}

func (b *Builder) Build(f *os.File) error {
	// Build the Project Tree
	tree := index.NewProjectTree()

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
	b.collectTemplates(tree)

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
		if child, ok := tree.Root.Children[expectedProject]; ok {
			rootNode = child
		}
	}

	// Write entire root content (definitions and children) to the single output file
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

func (b *Builder) collectTemplates(tree *index.ProjectTree) {
	processNode := func(n *index.ProjectNode) {
		for _, frag := range n.Fragments {
			for _, def := range frag.Definitions {
				if tdef, ok := def.(*parser.TemplateDefinition); ok {
					b.templates[tdef.Name] = tdef
				}
			}
		}
	}
	tree.Walk(processNode)
}

type EvaluatedDefinition struct {
	Def parser.Definition
	Ctx *evaluationContext
}

func (b *Builder) writeNodeBody(f *os.File, node *index.ProjectNode, indent int) {
	var allDefs []parser.Definition
	for _, frag := range node.Fragments {
		allDefs = append(allDefs, frag.Definitions...)
	}

	ctx := &evaluationContext{variables: b.variables}
	written := make(map[string]bool)
	b.writeEvaluatedBody(f, allDefs, ctx, indent, node, written)

	// Write remaining children (e.g. packages implicit nodes)
	var childNames []string
	for name := range node.Children {
		childNames = append(childNames, name)
	}
	sort.Strings(childNames)

	for _, name := range childNames {
		if !written[name] {
			child := node.Children[name]
			b.writeNodeContent(f, child, indent)
		}
	}
}

func (b *Builder) writeEvaluatedBody(f *os.File, defs []parser.Definition, ctx *evaluationContext, indent int, parentNode *index.ProjectNode, writtenChildren map[string]bool) {
	evaluated := b.evaluateDefinitions(defs, ctx)

	var fields []EvaluatedDefinition
	var objects []EvaluatedDefinition
	for _, ed := range evaluated {
		switch ed.Def.(type) {
		case *parser.Field:
			fields = append(fields, ed)
		case *parser.ObjectNode:
			objects = append(objects, ed)
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
			// Evaluate name to string to check against Children
			objName := b.formatValueWithCtx(objectNode.Name, obj.Ctx)
			// Unquote if needed
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
		
		b.writeEvaluatedObject(f, objectNode, obj.Ctx, indent)
	}
}

func (b *Builder) writeField(f *os.File, field *parser.Field, ctx *evaluationContext, indent int) {
	indentStr := strings.Repeat("  ", indent)
	fmt.Fprintf(f, "%s%s = %s\n", indentStr, field.Name, b.formatValueWithCtx(field.Value, ctx))
}

func (b *Builder) writeEvaluatedObject(f *os.File, obj *parser.ObjectNode, ctx *evaluationContext, indent int) {
	indentStr := strings.Repeat("  ", indent)
	objName := b.formatValueWithCtx(obj.Name, ctx)
	fmt.Fprintf(f, "%s%s = {\n", indentStr, objName)
	b.writeEvaluatedBody(f, obj.Subnode.Definitions, ctx, indent+1, nil, nil)
	fmt.Fprintf(f, "%s}\n", indentStr)
}

func (b *Builder) evaluateDefinitions(defs []parser.Definition, ctx *evaluationContext) []EvaluatedDefinition {
	var result []EvaluatedDefinition
	for _, def := range defs {
		switch d := def.(type) {
		case *parser.IfBlock:
			cond := b.evaluateValue(d.Condition, ctx)
			if b.isTrue(cond) {
				result = append(result, b.evaluateDefinitions(d.Then, ctx)...)
			} else {
				result = append(result, b.evaluateDefinitions(d.Else, ctx)...)
			}
		case *parser.ForeachBlock:
			iterable := b.evaluateValue(d.Iterable, ctx)
			if arr, ok := iterable.(*parser.ArrayValue); ok {
				for i, elem := range arr.Elements {
					loopCtx := &evaluationContext{
						variables: make(map[string]parser.Value),
						parent:    ctx,
					}
					if d.KeyVar != "" {
						loopCtx.variables[d.KeyVar] = &parser.IntValue{Value: int64(i), Raw: fmt.Sprintf("%d", i)}
					}
					loopCtx.variables[d.ValueVar] = elem
					result = append(result, b.evaluateDefinitions(d.Body, loopCtx)...)
				}
			}
		case *parser.TemplateInstantiation:
			if tdef, ok := b.templates[d.Template]; ok {
				templateCtx := &evaluationContext{
					variables: make(map[string]parser.Value),
					parent:    ctx,
				}
				// Bind arguments
				argMap := make(map[string]parser.Value)
				for _, arg := range d.Arguments {
					argMap[arg.Name] = b.evaluateValue(arg.Value, ctx)
				}

				for _, param := range tdef.Parameters {
					if val, ok := argMap[param.Name]; ok {
						templateCtx.variables[param.Name] = val
					} else {
						templateCtx.variables[param.Name] = param.DefaultValue
					}
				}
				// The template generates an object with Name d.Name
				obj := &parser.ObjectNode{
					Name: &parser.StringValue{Value: d.Name, Quoted: false},
					Subnode: parser.Subnode{
						Definitions: tdef.Body,
					},
				}
				result = append(result, EvaluatedDefinition{Def: obj, Ctx: templateCtx})
			}
		case *parser.VariableDefinition:
			// If overridden, do not overwrite the value
			if _, overridden := b.Overrides[d.Name]; overridden {
				continue
			}
			if d.DefaultValue != nil {
				ctx.variables[d.Name] = b.evaluateValue(d.DefaultValue, ctx)
			}
		default:
			result = append(result, EvaluatedDefinition{Def: d, Ctx: ctx})
		}
	}
	return result
}

func (b *Builder) evaluateValue(val parser.Value, ctx *evaluationContext) parser.Value {
	switch v := val.(type) {
	case *parser.VariableReferenceValue:
		name := strings.TrimPrefix(v.Name, "@")
		name = strings.TrimPrefix(name, "$")
		if res := ctx.resolve(name); res != nil {
			return b.evaluateValue(res, ctx)
		}
		return v
	case *parser.BinaryExpression:
		left := b.evaluateValue(v.Left, ctx)
		right := b.evaluateValue(v.Right, ctx)
		return b.compute(left, v.Operator, right)
	case *parser.UnaryExpression:
		right := b.evaluateValue(v.Right, ctx)
		return b.computeUnary(v.Operator, right)
	case *parser.ArrayValue:
		newElems := make([]parser.Value, len(v.Elements))
		for i, e := range v.Elements {
			newElems[i] = b.evaluateValue(e, ctx)
		}
		return &parser.ArrayValue{
			Position:    v.Position,
			EndPosition: v.EndPosition,
			Elements:    newElems,
		}
	}
	return val
}

func (b *Builder) computeUnary(op parser.Token, val parser.Value) parser.Value {
	switch op.Type {
	case parser.TokenMinus:
		if i, ok := val.(*parser.IntValue); ok {
			return &parser.IntValue{Value: -i.Value, Raw: fmt.Sprintf("%d", -i.Value)}
		}
		if f, ok := val.(*parser.FloatValue); ok {
			return &parser.FloatValue{Value: -f.Value, Raw: fmt.Sprintf("%g", -f.Value)}
		}
	case parser.TokenSymbol:
		if op.Value == "!" {
			if b, ok := val.(*parser.BoolValue); ok {
				return &parser.BoolValue{Value: !b.Value}
			}
		}
	}
	return nil
}

func (b *Builder) isTrue(v parser.Value) bool {
	switch val := v.(type) {
	case *parser.BoolValue:
		return val.Value
	case *parser.IntValue:
		return val.Value != 0
	}
	return false
}

func (b *Builder) formatValueWithCtx(val parser.Value, ctx *evaluationContext) string {
	val = b.evaluateValue(val, ctx)
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


func (b *Builder) compute(left parser.Value, op parser.Token, right parser.Value) parser.Value {
	if op.Type == parser.TokenConcat {
		s1 := b.valToString(left)
		s2 := b.valToString(right)
		return &parser.StringValue{Value: s1 + s2, Quoted: true}
	}

	// Try Integer arithmetic first
	lI, lIsI := b.valToInt(left)
	rI, rIsI := b.valToInt(right)

	if lIsI && rIsI {
		res := int64(0)
		switch op.Type {
		case parser.TokenPlus:
			res = lI + rI
		case parser.TokenMinus:
			res = lI - rI
		case parser.TokenStar:
			res = lI * rI
		case parser.TokenSlash:
			if rI != 0 {
				res = lI / rI
			}
		case parser.TokenPercent:
			if rI != 0 {
				res = lI % rI
			}
		case parser.TokenAmpersand:
			res = lI & rI
		case parser.TokenPipe:
			res = lI | rI
		case parser.TokenCaret:
			res = lI ^ rI
		case parser.TokenSymbol:
			switch op.Value {
			case "<":
				return &parser.BoolValue{Value: lI < rI}
			case ">":
				return &parser.BoolValue{Value: lI > rI}
			case "<=":
				return &parser.BoolValue{Value: lI <= rI}
			case ">=":
				return &parser.BoolValue{Value: lI >= rI}
			case "==":
				return &parser.BoolValue{Value: lI == rI}
			case "!=":
				return &parser.BoolValue{Value: lI != rI}
			}
		}
		return &parser.IntValue{Value: res, Raw: fmt.Sprintf("%d", res)}
	}

	// Fallback to Float arithmetic
	lF, lIsF := b.valToFloat(left)
	rF, rIsF := b.valToFloat(right)

	if lIsF || rIsF {
		res := 0.0
		switch op.Type {
		case parser.TokenPlus:
			res = lF + rF
		case parser.TokenMinus:
			res = lF - rF
		case parser.TokenStar:
			res = lF * rF
		case parser.TokenSlash:
			res = lF / rF
		case parser.TokenSymbol:
			switch op.Value {
			case "<":
				return &parser.BoolValue{Value: lF < rF}
			case ">":
				return &parser.BoolValue{Value: lF > rF}
			case "<=":
				return &parser.BoolValue{Value: lF <= rF}
			case ">=":
				return &parser.BoolValue{Value: lF >= rF}
			case "==":
				return &parser.BoolValue{Value: lF == rF}
			case "!=":
				return &parser.BoolValue{Value: lF != rF}
			}
		}
		return &parser.FloatValue{Value: res, Raw: fmt.Sprintf("%g", res)}
	}

	return left
}

func (b *Builder) valToString(v parser.Value) string {
	switch val := v.(type) {
	case *parser.StringValue:
		return val.Value
	case *parser.IntValue:
		return val.Raw
	case *parser.FloatValue:
		return val.Raw
	default:
		return ""
	}
}

func (b *Builder) valToFloat(v parser.Value) (float64, bool) {
	switch val := v.(type) {
	case *parser.FloatValue:
		return val.Value, true
	case *parser.IntValue:
		return float64(val.Value), true
	}
	return 0, false
}

func (b *Builder) valToInt(v parser.Value) (int64, bool) {
	switch val := v.(type) {
	case *parser.IntValue:
		return val.Value, true
	}
	return 0, false
}
