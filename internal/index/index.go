package index

import (
	"strings"

	"github.com/marte-dev/marte-dev-tools/internal/parser"
)

type ProjectTree struct {
	Root       *ProjectNode
	References []Reference
}

type Reference struct {
	Name     string
	Position parser.Position
	File     string
	Target   *ProjectNode // Resolved target
}

type ProjectNode struct {
	Name      string // Normalized name
	RealName  string // The actual name used in definition (e.g. +Node)
	Fragments []*Fragment
	Children  map[string]*ProjectNode
	Parent    *ProjectNode
}

type Fragment struct {
	File        string
	Definitions []parser.Definition
	IsObject    bool            
	ObjectPos   parser.Position 
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

func (pt *ProjectTree) RemoveFile(file string) {
	// Remove references from this file
	newRefs := []Reference{}
	for _, ref := range pt.References {
		if ref.File != file {
			newRefs = append(newRefs, ref)
		}
	}
	pt.References = newRefs

	// Remove fragments from tree
	pt.removeFileFromNode(pt.Root, file)
}

func (pt *ProjectTree) removeFileFromNode(node *ProjectNode, file string) {
	newFragments := []*Fragment{}
	for _, frag := range node.Fragments {
		if frag.File != file {
			newFragments = append(newFragments, frag)
		}
	}
	node.Fragments = newFragments

	for _, child := range node.Children {
		pt.removeFileFromNode(child, file)
	}
}

func (pt *ProjectTree) AddFile(file string, config *parser.Configuration) {
	pt.RemoveFile(file) // Ensure clean state for this file

	node := pt.Root
	if config.Package != nil {
		parts := strings.Split(config.Package.URI, ".")
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if _, ok := node.Children[part]; !ok {
				node.Children[part] = &ProjectNode{
					Name:     part,
					RealName: part, 
					Children: make(map[string]*ProjectNode),
					Parent:   node,
				}
			}
			node = node.Children[part]
		}
	}

	fileFragment := &Fragment{
		File:     file,
		IsObject: false,
	}
	
	for _, def := range config.Definitions {
		switch d := def.(type) {
		case *parser.Field:
			fileFragment.Definitions = append(fileFragment.Definitions, d)
			pt.indexValue(file, d.Value)
		case *parser.ObjectNode:
			norm := NormalizeName(d.Name)
			if _, ok := node.Children[norm]; !ok {
				node.Children[norm] = &ProjectNode{
					Name:     norm,
					RealName: d.Name,
					Children: make(map[string]*ProjectNode),
					Parent:   node,
				}
			}
			child := node.Children[norm]
			if child.RealName == norm && d.Name != norm {
				child.RealName = d.Name
			}
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
			pt.indexValue(file, d.Value)
		case *parser.ObjectNode:
			norm := NormalizeName(d.Name)
			if _, ok := node.Children[norm]; !ok {
				node.Children[norm] = &ProjectNode{
					Name:     norm,
					RealName: d.Name,
					Children: make(map[string]*ProjectNode),
					Parent:   node,
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

func (pt *ProjectTree) indexValue(file string, val parser.Value) {
	switch v := val.(type) {
	case *parser.ReferenceValue:
		pt.References = append(pt.References, Reference{
			Name:     v.Value,
			Position: v.Position,
			File:     file,
		})
	case *parser.ArrayValue:
		for _, elem := range v.Elements {
			pt.indexValue(file, elem)
		}
	}
}

func (pt *ProjectTree) ResolveReferences() {
	for i := range pt.References {
		ref := &pt.References[i]
		ref.Target = pt.findNode(pt.Root, ref.Name)
	}
}

func (pt *ProjectTree) findNode(root *ProjectNode, name string) *ProjectNode {
	if root.RealName == name || root.Name == name {
		return root
	}
	for _, child := range root.Children {
		if res := pt.findNode(child, name); res != nil {
			return res
		}
	}
	return nil
}

// QueryResult holds the result of a query at a position
type QueryResult struct {
	Node      *ProjectNode
	Field     *parser.Field
	Reference *Reference
}

func (pt *ProjectTree) Query(file string, line, col int) *QueryResult {
	// 1. Check References
	for i := range pt.References {
		ref := &pt.References[i]
		if ref.File == file {
			// Check if pos is within reference range
			// Approx length
			if line == ref.Position.Line && col >= ref.Position.Column && col < ref.Position.Column+len(ref.Name) {
				return &QueryResult{Reference: ref}
			}
		}
	}

	// 2. Check Definitions (traverse tree)
	return pt.queryNode(pt.Root, file, line, col)
}

func (pt *ProjectTree) queryNode(node *ProjectNode, file string, line, col int) *QueryResult {
	for _, frag := range node.Fragments {
		if frag.File == file {
			// Check Object definition itself
			if frag.IsObject {
				// Object definition usually starts at 'Name'.
				// Position is start of Name.
				if line == frag.ObjectPos.Line && col >= frag.ObjectPos.Column && col < frag.ObjectPos.Column+len(node.RealName) {
					return &QueryResult{Node: node}
				}
			}
			
			// Check definitions in fragment
			for _, def := range frag.Definitions {
				if f, ok := def.(*parser.Field); ok {
					// Check field name range
					if line == f.Position.Line && col >= f.Position.Column && col < f.Position.Column+len(f.Name) {
						return &QueryResult{Field: f}
					}
				}
			}
		}
	}
	
	for _, child := range node.Children {
		if res := pt.queryNode(child, file, line, col); res != nil {
			return res
		}
	}
	return nil
}
