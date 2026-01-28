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
	Files []string
}

func NewBuilder(files []string) *Builder {
	return &Builder{Files: files}
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

func hasClass(frag *index.Fragment) bool {
	for _, def := range frag.Definitions {
		if f, ok := def.(*parser.Field); ok && f.Name == "Class" {
			return true
		}
	}
	return false
}
