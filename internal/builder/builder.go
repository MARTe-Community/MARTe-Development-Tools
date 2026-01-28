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
}

func NewBuilder(files []string, overrides map[string]string) *Builder {
	return &Builder{Files: files, Overrides: overrides, variables: make(map[string]parser.Value)}
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

	if expectedProject == "" {
		for _, iso := range tree.IsolatedFiles {
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

func (b *Builder) writeNodeBody(f *os.File, node *index.ProjectNode, indent int) {
	// 1. Sort Fragments: Class first
	sort.SliceStable(node.Fragments, func(i, j int) bool {
		return hasClass(node.Fragments[i]) && !hasClass(node.Fragments[j])
	})

	writtenChildren := make(map[string]bool)

	// 2. Write definitions from fragments
	for _, frag := range node.Fragments {
		for _, def := range frag.Definitions {
			switch d := def.(type) {
			case *parser.Field:
				b.writeDefinition(f, d, indent)
			case *parser.VariableDefinition:
				continue
			case *parser.ObjectNode:
				norm := index.NormalizeName(d.Name)
				if child, ok := node.Children[norm]; ok {
					if !writtenChildren[norm] {
						b.writeNodeContent(f, child, indent)
						writtenChildren[norm] = true
					}
				}
			}
		}
	}

	// 3. Write Children (recursively)
	sortedChildren := make([]string, 0, len(node.Children))
	for k := range node.Children {
		if !writtenChildren[k] {
			sortedChildren = append(sortedChildren, k)
		}
	}
	sort.Strings(sortedChildren) // Alphabetical for determinism

	for _, k := range sortedChildren {
		child := node.Children[k]
		b.writeNodeContent(f, child, indent)
	}
}

func (b *Builder) writeDefinition(f *os.File, def parser.Definition, indent int) {
	indentStr := strings.Repeat("  ", indent)
	switch d := def.(type) {
	case *parser.Field:
		fmt.Fprintf(f, "%s%s = %s\n", indentStr, d.Name, b.formatValue(d.Value))
	}
}

func (b *Builder) formatValue(val parser.Value) string {
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
		name := strings.TrimPrefix(v.Name, "$")
		if val, ok := b.variables[name]; ok {
			return b.formatValue(val)
		}
		return v.Name
	case *parser.ReferenceValue:
		return v.Value
	case *parser.ArrayValue:
		elements := []string{}
		for _, e := range v.Elements {
			elements = append(elements, b.formatValue(e))
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
						p := parser.NewParser("Temp = " + valStr)
						cfg, _ := p.Parse()
						if len(cfg.Definitions) > 0 {
							if f, ok := cfg.Definitions[0].(*parser.Field); ok {
								b.variables[vdef.Name] = f.Value
								continue
							}
						}
					}
					if vdef.DefaultValue != nil {
						b.variables[vdef.Name] = vdef.DefaultValue
					}
				}
			}
		}
	}
	tree.Walk(processNode)
}
