package index

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/marte-community/marte-dev-tools/internal/logger"
	"github.com/marte-community/marte-dev-tools/internal/parser"
)

type VariableInfo struct {
	Def  *parser.VariableDefinition
	File string
	Doc  string
}

type ProjectTree struct {
	Root          *ProjectNode
	References    []Reference
	IsolatedFiles map[string]*ProjectNode
	GlobalPragmas map[string][]string
	NodeMap       map[string][]*ProjectNode
}

func (pt *ProjectTree) ScanDirectory(rootPath string) error {
	return filepath.Walk(rootPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(info.Name(), ".marte") {
			logger.Printf("indexing: %s [%s]\n", info.Name(), path)
			content, err := os.ReadFile(path)
			if err != nil {
				return err // Or log and continue
			}
			p := parser.NewParser(string(content))
			config, _ := p.Parse()
			if config != nil {
				pt.AddFile(path, config)
			}
		}
		return nil
	})
}

type Reference struct {
	Name           string
	Position       parser.Position
	File           string
	Target         *ProjectNode
	TargetVariable *parser.VariableDefinition
	IsVariable     bool
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
	Pragmas   []string
	Variables map[string]VariableInfo
}

type Fragment struct {
	File        string
	Definitions []parser.Definition
	IsObject    bool
	ObjectPos   parser.Position
	EndPos      parser.Position
	Doc         string // Documentation for this fragment (if object)
}

func NewProjectTree() *ProjectTree {
	return &ProjectTree{
		Root: &ProjectNode{
			Children:  make(map[string]*ProjectNode),
			Metadata:  make(map[string]string),
			Variables: make(map[string]VariableInfo),
		},
		IsolatedFiles: make(map[string]*ProjectNode),
		GlobalPragmas: make(map[string][]string),
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
	delete(pt.GlobalPragmas, file)
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

	for name, child := range node.Children {
		pt.removeFileFromNode(child, file)
		if len(child.Fragments) == 0 && len(child.Children) == 0 {
			delete(node.Children, name)
		}
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

	// Collect global pragmas
	for _, p := range config.Pragmas {
		txt := strings.TrimSpace(strings.TrimPrefix(p.Text, "//!"))
		normalized := strings.ReplaceAll(txt, " ", "")
		if strings.HasPrefix(normalized, "allow(") || strings.HasPrefix(normalized, "ignore(") {
			pt.GlobalPragmas[file] = append(pt.GlobalPragmas[file], txt)
		}
	}

	if config.Package == nil {
		node := &ProjectNode{
			Children:  make(map[string]*ProjectNode),
			Metadata:  make(map[string]string),
			Variables: make(map[string]VariableInfo),
		}
		pt.IsolatedFiles[file] = node
		pt.populateNode(node, file, config)
		return
	}

	node := pt.Root
	parts := strings.Split(config.Package.URI, ".")

	for i := 0; i < len(parts); i++ {
		part := strings.TrimSpace(parts[i])
		if part == "" {
			continue
		}
		if _, ok := node.Children[part]; !ok {
			node.Children[part] = &ProjectNode{
				Name:      part,
				RealName:  part,
				Children:  make(map[string]*ProjectNode),
				Parent:    node,
				Metadata:  make(map[string]string),
				Variables: make(map[string]VariableInfo),
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
		pragmas := pt.findPragmas(config.Pragmas, def.Pos())

		switch d := def.(type) {
		case *parser.Field:
			fileFragment.Definitions = append(fileFragment.Definitions, d)
			pt.indexValue(file, d.Value)
		case *parser.VariableDefinition:
			fileFragment.Definitions = append(fileFragment.Definitions, d)
			node.Variables[d.Name] = VariableInfo{Def: d, File: file, Doc: doc}
		case *parser.ObjectNode:
			fileFragment.Definitions = append(fileFragment.Definitions, d)
			norm := NormalizeName(d.Name)
			if _, ok := node.Children[norm]; !ok {
				node.Children[norm] = &ProjectNode{
					Name:      norm,
					RealName:  d.Name,
					Children:  make(map[string]*ProjectNode),
					Parent:    node,
					Metadata:  make(map[string]string),
					Variables: make(map[string]VariableInfo),
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

			if len(pragmas) > 0 {
				child.Pragmas = append(child.Pragmas, pragmas...)
			}

			pt.addObjectFragment(child, file, d, doc, config.Comments, config.Pragmas)
		}
	}

	if len(fileFragment.Definitions) > 0 {
		node.Fragments = append(node.Fragments, fileFragment)
	}
}

func (pt *ProjectTree) addObjectFragment(node *ProjectNode, file string, obj *parser.ObjectNode, doc string, comments []parser.Comment, pragmas []parser.Pragma) {
	frag := &Fragment{
		File:      file,
		IsObject:  true,
		ObjectPos: obj.Position,
		EndPos:    obj.Subnode.EndPosition,
		Doc:       doc,
	}

	for _, def := range obj.Subnode.Definitions {
		subDoc := pt.findDoc(comments, def.Pos())
		subPragmas := pt.findPragmas(pragmas, def.Pos())

		switch d := def.(type) {
		case *parser.Field:
			frag.Definitions = append(frag.Definitions, d)
			pt.indexValue(file, d.Value)
			pt.extractFieldMetadata(node, d)
		case *parser.VariableDefinition:
			frag.Definitions = append(frag.Definitions, d)
			node.Variables[d.Name] = VariableInfo{Def: d, File: file, Doc: subDoc}
		case *parser.ObjectNode:
			frag.Definitions = append(frag.Definitions, d)
			norm := NormalizeName(d.Name)
			if _, ok := node.Children[norm]; !ok {
				node.Children[norm] = &ProjectNode{
					Name:      norm,
					RealName:  d.Name,
					Children:  make(map[string]*ProjectNode),
					Parent:    node,
					Metadata:  make(map[string]string),
					Variables: make(map[string]VariableInfo),
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

			if len(subPragmas) > 0 {
				child.Pragmas = append(child.Pragmas, subPragmas...)
			}

			pt.addObjectFragment(child, file, d, subDoc, comments, pragmas)
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

func (pt *ProjectTree) findPragmas(pragmas []parser.Pragma, pos parser.Position) []string {
	var found []string
	targetLine := pos.Line - 1

	for i := len(pragmas) - 1; i >= 0; i-- {
		p := pragmas[i]
		if p.Position.Line > pos.Line {
			continue
		}
		if p.Position.Line == pos.Line {
			continue
		}

		if p.Position.Line == targetLine {
			txt := strings.TrimSpace(strings.TrimPrefix(p.Text, "//!"))
			found = append(found, txt)
			targetLine--
		} else if p.Position.Line < targetLine {
			break
		}
	}
	return found
}

func (pt *ProjectTree) indexValue(file string, val parser.Value) {
	switch v := val.(type) {
	case *parser.ReferenceValue:
		pt.References = append(pt.References, Reference{
			Name:     v.Value,
			Position: v.Position,
			File:     file,
		})
	case *parser.VariableReferenceValue:
		name := strings.TrimPrefix(v.Name, "@")
		pt.References = append(pt.References, Reference{
			Name:       name,
			Position:   v.Position,
			File:       file,
			IsVariable: true,
		})
	case *parser.ArrayValue:
		for _, elem := range v.Elements {
			pt.indexValue(file, elem)
		}
	}
}

func (pt *ProjectTree) RebuildIndex() {
	pt.NodeMap = make(map[string][]*ProjectNode)
	visitor := func(n *ProjectNode) {
		pt.NodeMap[n.Name] = append(pt.NodeMap[n.Name], n)
		if n.RealName != n.Name {
			pt.NodeMap[n.RealName] = append(pt.NodeMap[n.RealName], n)
		}
	}
	pt.Walk(visitor)
}

func (pt *ProjectTree) ResolveReferences() {
	pt.RebuildIndex()
	for i := range pt.References {
		ref := &pt.References[i]

		container := pt.GetNodeContaining(ref.File, ref.Position)

		if v := pt.ResolveVariable(container, ref.Name); v != nil {
			ref.TargetVariable = v.Def
			continue
		}

		ref.Target = pt.ResolveName(container, ref.Name, nil)
	}
}

func (pt *ProjectTree) FindNode(root *ProjectNode, name string, predicate func(*ProjectNode) bool) *ProjectNode {
	if pt.NodeMap == nil {
		pt.RebuildIndex()
	}

	if strings.Contains(name, ".") {
		parts := strings.Split(name, ".")
		rootName := parts[0]

		candidates := pt.NodeMap[rootName]

		for _, cand := range candidates {
			if !pt.isDescendant(cand, root) {
				continue
			}

			curr := cand
			valid := true
			for i := 1; i < len(parts); i++ {
				nextName := parts[i]
				normNext := NormalizeName(nextName)
				if child, ok := curr.Children[normNext]; ok {
					curr = child
				} else {
					valid = false
					break
				}
			}
			if valid {
				if predicate == nil || predicate(curr) {
					return curr
				}
			}
		}
		return nil
	}

	candidates := pt.NodeMap[name]
	for _, cand := range candidates {
		if !pt.isDescendant(cand, root) {
			continue
		}
		if predicate == nil || predicate(cand) {
			return cand
		}
	}
	return nil
}

func (pt *ProjectTree) isDescendant(node, root *ProjectNode) bool {
	if node == root {
		return true
	}
	if root == nil {
		return true
	}
	curr := node
	for curr != nil {
		if curr == root {
			return true
		}
		curr = curr.Parent
	}
	return false
}

type QueryResult struct {
	Node      *ProjectNode
	Field     *parser.Field
	Reference *Reference
	Variable  *parser.VariableDefinition
}

func (pt *ProjectTree) Query(file string, line, col int) *QueryResult {
	for i := range pt.References {
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
				} else if v, ok := def.(*parser.VariableDefinition); ok {
					if line == v.Position.Line {
						return &QueryResult{Variable: v}
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

func (pt *ProjectTree) GetNodeContaining(file string, pos parser.Position) *ProjectNode {
	if isoNode, ok := pt.IsolatedFiles[file]; ok {
		if found := pt.findNodeContaining(isoNode, file, pos); found != nil {
			return found
		}
		return isoNode
	}
	if pt.Root != nil {
		if found := pt.findNodeContaining(pt.Root, file, pos); found != nil {
			return found
		}
		for _, frag := range pt.Root.Fragments {
			if frag.File == file && !frag.IsObject {
				return pt.Root
			}
		}
	}
	return nil
}

func (pt *ProjectTree) findNodeContaining(node *ProjectNode, file string, pos parser.Position) *ProjectNode {
	for _, child := range node.Children {
		if res := pt.findNodeContaining(child, file, pos); res != nil {
			return res
		}
	}

	for _, frag := range node.Fragments {
		if frag.File == file && frag.IsObject {
			start := frag.ObjectPos
			end := frag.EndPos

			if (pos.Line > start.Line || (pos.Line == start.Line && pos.Column >= start.Column)) &&
				(pos.Line < end.Line || (pos.Line == end.Line && pos.Column <= end.Column)) {
				return node
			}
		}
	}
	return nil
}

func (pt *ProjectTree) ResolveName(ctx *ProjectNode, name string, predicate func(*ProjectNode) bool) *ProjectNode {
	if ctx == nil {
		return pt.FindNode(pt.Root, name, predicate)
	}

	curr := ctx
	for curr != nil {
		if found := pt.FindNode(curr, name, predicate); found != nil {
			return found
		}
		curr = curr.Parent
	}
	return nil
}

func (pt *ProjectTree) ResolveVariable(ctx *ProjectNode, name string) *VariableInfo {
	curr := ctx
	for curr != nil {
		if v, ok := curr.Variables[name]; ok {
			return &v
		}
		curr = curr.Parent
	}
	if ctx == nil {
		if v, ok := pt.Root.Variables[name]; ok {
			return &v
		}
	}
	return nil
}
