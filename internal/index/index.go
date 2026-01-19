package index

import (
	"strings"

	"github.com/marte-dev/marte-dev-tools/internal/parser"
)

type ProjectTree struct {
	Root *ProjectNode
}

type ProjectNode struct {
	Name      string // Normalized name
	RealName  string // The actual name used in definition (e.g. +Node)
	Fragments []*Fragment
	Children  map[string]*ProjectNode
}

type Fragment struct {
	File        string
	Definitions []parser.Definition
	IsObject    bool            // True if this fragment comes from an ObjectNode, False if from File/Package body
	ObjectPos   parser.Position // Position of the object node if IsObject is true
}

func NewProjectTree() *ProjectTree {
	return &ProjectTree{
		Root: &ProjectNode{
			Children: make(map[string]*ProjectNode),
		},
	}
}

func NormalizeName(name string) string {
	if len(name) > 0 && (name[0] == '+' || name[0] == '$') {
		return name[1:]
	}
	return name
}

func (pt *ProjectTree) AddFile(file string, config *parser.Configuration) {
	// Determine root node for this file based on package
	node := pt.Root
	if config.Package != nil {
		parts := strings.Split(config.Package.URI, ".")
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			// Navigate or Create
			if _, ok := node.Children[part]; !ok {
				node.Children[part] = &ProjectNode{
					Name:     part,
					RealName: part, // Default, might be updated if we find a +Part later? 
					// Actually, package segments are just names. 
					// If they refer to an object defined elsewhere as +Part, we hope to match it.
					Children: make(map[string]*ProjectNode),
				}
			}
			node = node.Children[part]
		}
	}

	// Now 'node' is the container for the file's definitions.
	// We add a Fragment to this node containing the top-level definitions.
	// But wait, definitions can be ObjectNodes (which start NEW nodes) or Fields (which belong to 'node').
	
	// We need to split definitions:
	// Fields -> go into a Fragment for 'node'.
	// ObjectNodes -> create/find Child node and add Fragment there.
	
	// Actually, the Build Process says: "#package ... implies all definitions ... are children".
	// So if I have "Field = 1", it is a child of the package node.
	// If I have "+Sub = {}", it is a child of the package node.
	
	// So we can just iterate definitions.
	
	// But for merging, we need to treat "+Sub" as a Node, not just a field.
	
	fileFragment := &Fragment{
		File: file,
		IsObject: false,
	}
	
	for _, def := range config.Definitions {
		switch d := def.(type) {
		case *parser.Field:
			// Fields belong to the current package node
			fileFragment.Definitions = append(fileFragment.Definitions, d)
		case *parser.ObjectNode:
			// Object starts a new child node
			norm := NormalizeName(d.Name)
			if _, ok := node.Children[norm]; !ok {
				node.Children[norm] = &ProjectNode{
					Name:     norm,
					RealName: d.Name,
					Children: make(map[string]*ProjectNode),
				}
			}
			child := node.Children[norm]
			if child.RealName == norm && d.Name != norm {
				child.RealName = d.Name // Update to specific name if we had generic
			}
			
			// Recursively add definitions of the object
			pt.addObjectFragment(child, file, d)
		}
	}
	
	if len(fileFragment.Definitions) > 0 {
		node.Fragments = append(node.Fragments, fileFragment)
	}
}

func (pt *ProjectTree) addObjectFragment(node *ProjectNode, file string, obj *parser.ObjectNode) {
	frag := &Fragment{
		File:        file,
		IsObject:    true,
		ObjectPos:   obj.Position,
	}
	
	for _, def := range obj.Subnode.Definitions {
		switch d := def.(type) {
		case *parser.Field:
			frag.Definitions = append(frag.Definitions, d)
		case *parser.ObjectNode:
			norm := NormalizeName(d.Name)
			if _, ok := node.Children[norm]; !ok {
				node.Children[norm] = &ProjectNode{
					Name:     norm,
					RealName: d.Name,
					Children: make(map[string]*ProjectNode),
				}
			}
			child := node.Children[norm]
			if child.RealName == norm && d.Name != norm {
				child.RealName = d.Name
			}
			pt.addObjectFragment(child, file, d)
		}
	}
	
	node.Fragments = append(node.Fragments, frag)
}
