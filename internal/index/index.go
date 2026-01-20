package index

import (
	"fmt"
	"os"
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
	Doc       string // Aggregated documentation
	Fragments []*Fragment
	Children  map[string]*ProjectNode
	Parent    *ProjectNode
	Metadata  map[string]string // Store extra info like Class, Type, Size
}

type Fragment struct {
	File        string
	Definitions []parser.Definition
	IsObject    bool
	ObjectPos   parser.Position
	Doc         string // Documentation for this fragment (if object)
}

func NewProjectTree() *ProjectTree {
	return &ProjectTree{
		Root: &ProjectNode{
			Children: make(map[string]*ProjectNode),
			Metadata: make(map[string]string),
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
	newRefs := []Reference{}
	for _, ref := range pt.References {
		if ref.File != file {
			newRefs = append(newRefs, ref)
		}
	}
	pt.References = newRefs

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

	// Re-aggregate documentation
	node.Doc = ""
	for _, frag := range node.Fragments {
		if frag.Doc != "" {
			if node.Doc != "" {
				node.Doc += "\n\n"
			}
			node.Doc += frag.Doc
		}
	}

	// Re-aggregate metadata
	node.Metadata = make(map[string]string)
	pt.rebuildMetadata(node)

	for _, child := range node.Children {
		pt.removeFileFromNode(child, file)
	}
}

func (pt *ProjectTree) rebuildMetadata(node *ProjectNode) {
	for _, frag := range node.Fragments {
		for _, def := range frag.Definitions {
			if f, ok := def.(*parser.Field); ok {
				pt.extractFieldMetadata(node, f)
			}
		}
	}
}

func (pt *ProjectTree) extractFieldMetadata(node *ProjectNode, f *parser.Field) {
	key := f.Name
	val := ""
	switch v := f.Value.(type) {
	case *parser.StringValue:
		val = v.Value
	case *parser.ReferenceValue:
		val = v.Value
	case *parser.IntValue:
		val = v.Raw
	}

	if val == "" {
		return
	}

	// Capture relevant fields
	if key == "Class" || key == "Type" || key == "NumberOfElements" || key == "NumberOfDimensions" || key == "DataSource" {
		node.Metadata[key] = val
	}
}

func (pt *ProjectTree) AddFile(file string, config *parser.Configuration) {
	pt.RemoveFile(file)

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
					Metadata: make(map[string]string),
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
		doc := pt.findDoc(config.Comments, def.Pos())

		switch d := def.(type) {
		case *parser.Field:
			fileFragment.Definitions = append(fileFragment.Definitions, d)
			pt.indexValue(file, d.Value)
			// Metadata update not really relevant for package node usually, but consistency
		case *parser.ObjectNode:
			norm := NormalizeName(d.Name)
			if _, ok := node.Children[norm]; !ok {
				node.Children[norm] = &ProjectNode{
					Name:     norm,
					RealName: d.Name,
					Children: make(map[string]*ProjectNode),
					Parent:   node,
					Metadata: make(map[string]string),
				}
			}
			child := node.Children[norm]
			if child.RealName == norm && d.Name != norm {
				child.RealName = d.Name
			}

			if doc != "" {
				if child.Doc != "" {
					child.Doc += "\n\n"
				}
				child.Doc += doc
			}

			pt.addObjectFragment(child, file, d, doc, config.Comments)
		}
	}

	if len(fileFragment.Definitions) > 0 {
		node.Fragments = append(node.Fragments, fileFragment)
	}
}

func (pt *ProjectTree) addObjectFragment(node *ProjectNode, file string, obj *parser.ObjectNode, doc string, comments []parser.Comment) {
	frag := &Fragment{
		File:      file,
		IsObject:  true,
		ObjectPos: obj.Position,
		Doc:       doc,
	}

	for _, def := range obj.Subnode.Definitions {
		subDoc := pt.findDoc(comments, def.Pos())

		switch d := def.(type) {
		case *parser.Field:
			frag.Definitions = append(frag.Definitions, d)
			pt.indexValue(file, d.Value)
			pt.extractFieldMetadata(node, d)
		case *parser.ObjectNode:
			norm := NormalizeName(d.Name)
			if _, ok := node.Children[norm]; !ok {
				node.Children[norm] = &ProjectNode{
					Name:     norm,
					RealName: d.Name,
					Children: make(map[string]*ProjectNode),
					Parent:   node,
					Metadata: make(map[string]string),
				}
			}
			child := node.Children[norm]
			if child.RealName == norm && d.Name != norm {
				child.RealName = d.Name
			}

			if subDoc != "" {
				if child.Doc != "" {
					child.Doc += "\n\n"
				}
				child.Doc += subDoc
			}

			pt.addObjectFragment(child, file, d, subDoc, comments)
		}
	}

	node.Fragments = append(node.Fragments, frag)
}

func (pt *ProjectTree) findDoc(comments []parser.Comment, pos parser.Position) string {
	var docBuilder strings.Builder
	targetLine := pos.Line - 1
	var docIndices []int

	for i := len(comments) - 1; i >= 0; i-- {
		c := comments[i]
		if c.Position.Line > pos.Line {
			continue
		}
		if c.Position.Line == pos.Line {
			continue
		}

		if c.Position.Line == targetLine {
			if c.Doc {
				docIndices = append(docIndices, i)
				targetLine--
			} else {
				break
			}
		} else if c.Position.Line < targetLine {
			break
		}
	}

	for i := len(docIndices) - 1; i >= 0; i-- {
		txt := strings.TrimPrefix(comments[docIndices[i]].Text, "//#")
		txt = strings.TrimSpace(txt)
		if docBuilder.Len() > 0 {
			docBuilder.WriteString("\n")
		}
		docBuilder.WriteString(txt)
	}

	return docBuilder.String()
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

type QueryResult struct {
	Node      *ProjectNode
	Field     *parser.Field
	Reference *Reference
}

func (pt *ProjectTree) Query(file string, line, col int) *QueryResult {
	fmt.Fprintf(os.Stderr, "File: %s:%d:%d\n", file, line, col)
	for i := range pt.References {
		fmt.Fprintf(os.Stderr, "%s\n", pt.Root.Name)
		ref := &pt.References[i]
		if ref.File == file {
			if line == ref.Position.Line && col >= ref.Position.Column && col < ref.Position.Column+len(ref.Name) {
				return &QueryResult{Reference: ref}
			}
		}
	}

	return pt.queryNode(pt.Root, file, line, col)
}

func (pt *ProjectTree) queryNode(node *ProjectNode, file string, line, col int) *QueryResult {
	for _, frag := range node.Fragments {
		if frag.File == file {
			if frag.IsObject {
				if line == frag.ObjectPos.Line && col >= frag.ObjectPos.Column && col < frag.ObjectPos.Column+len(node.RealName) {
					return &QueryResult{Node: node}
				}
			}

			for _, def := range frag.Definitions {
				if f, ok := def.(*parser.Field); ok {
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
