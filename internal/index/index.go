package index

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/marte-dev/marte-dev-tools/internal/logger"
	"github.com/marte-dev/marte-dev-tools/internal/parser"
)

type ProjectTree struct {
	Root          *ProjectNode
	References    []Reference
	IsolatedFiles map[string]*ProjectNode
}

func (pt *ProjectTree) ScanDirectory(rootPath string) error {
	return filepath.Walk(rootPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(info.Name(), ".marte") {
			content, err := os.ReadFile(path)
			if err != nil {
				return err // Or log and continue
			}
			p := parser.NewParser(string(content))
			config, err := p.Parse()
			if err == nil {
				pt.AddFile(path, config)
			}
		}
		return nil
	})
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
	Target    *ProjectNode      // Points to referenced node (for Direct References/Links)
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
		IsolatedFiles: make(map[string]*ProjectNode),
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

	delete(pt.IsolatedFiles, file)
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

	if config.Package == nil {
		node := &ProjectNode{
			Children: make(map[string]*ProjectNode),
			Metadata: make(map[string]string),
		}
		pt.IsolatedFiles[file] = node
		pt.populateNode(node, file, config)
		return
	}

	node := pt.Root
	parts := strings.Split(config.Package.URI, ".")
	// Skip first part as per spec (Project Name is namespace only)
	startIdx := 0
	if len(parts) > 0 {
		startIdx = 1
	}

	for i := startIdx; i < len(parts); i++ {
		part := strings.TrimSpace(parts[i])
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

	pt.populateNode(node, file, config)
}

func (pt *ProjectTree) populateNode(node *ProjectNode, file string, config *parser.Configuration) {
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
		if isoNode, ok := pt.IsolatedFiles[ref.File]; ok {
			ref.Target = pt.findNode(isoNode, ref.Name)
		} else {
			ref.Target = pt.findNode(pt.Root, ref.Name)
		}
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
	logger.Printf("File: %s:%d:%d", file, line, col)
	for i := range pt.References {
		logger.Printf("%s", pt.Root.Name)
		ref := &pt.References[i]
		if ref.File == file {
			if line == ref.Position.Line && col >= ref.Position.Column && col < ref.Position.Column+len(ref.Name) {
				return &QueryResult{Reference: ref}
			}
		}
	}

	if isoNode, ok := pt.IsolatedFiles[file]; ok {
		return pt.queryNode(isoNode, file, line, col)
	}

	return pt.queryNode(pt.Root, file, line, col)
}

func (pt *ProjectTree) Walk(visitor func(*ProjectNode)) {
	if pt.Root != nil {
		pt.walkRecursive(pt.Root, visitor)
	}
	for _, node := range pt.IsolatedFiles {
		pt.walkRecursive(node, visitor)
	}
}

func (pt *ProjectTree) walkRecursive(node *ProjectNode, visitor func(*ProjectNode)) {
	visitor(node)
	for _, child := range node.Children {
		pt.walkRecursive(child, visitor)
	}
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
