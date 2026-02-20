package index

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/marte-community/marte-dev-tools/internal/logger"
	"github.com/marte-community/marte-dev-tools/internal/parser"
)

type VariableInfo struct {
	Def  *parser.VariableDefinition
	File string
	Doc  string
}

type ProjectTree struct {
	Root           *ProjectNode
	References     []Reference // Deprecated: Use FileReferences for lookup
	FileReferences map[string][]Reference
	IsolatedFiles  map[string]*ProjectNode
	GlobalPragmas  map[string][]string
	NodeMap        map[string][]*ProjectNode
	Templates      map[string]*parser.TemplateDefinition
	mu             sync.RWMutex
}

func (pt *ProjectTree) ScanDirectory(rootPath string) error {
	var files []string
	visited := make(map[string]struct{})

	var walk func(string) error
	walk = func(path string) error {
		info, err := os.Lstat(path)
		if err != nil {
			return nil
		}

		// Handle symlinks
		if info.Mode()&os.ModeSymlink != 0 {
			resolved, err := filepath.EvalSymlinks(path)
			if err != nil {
				return nil
			}
			absResolved, _ := filepath.Abs(resolved)
			if _, ok := visited[absResolved]; ok {
				return nil
			}
			// For symlinked folders, we check the target info
			info, err = os.Stat(path)
			if err != nil {
				return nil
			}
			// Note: we don't mark visited yet, the recursive call or file logic will do it
		}

		absPath, _ := filepath.Abs(path)
		if _, ok := visited[absPath]; ok {
			return nil
		}
		visited[absPath] = struct{}{}

		if info.IsDir() {
			entries, err := os.ReadDir(path)
			if err != nil {
				return nil
			}
			for _, e := range entries {
				walk(filepath.Join(path, e.Name()))
			}
		} else {
			if strings.HasSuffix(info.Name(), ".marte") {
				files = append(files, path)
			}
		}
		return nil
	}

	err := walk(rootPath)
	if err != nil {
		return err
	}

	type result struct {
		path   string
		config *parser.Configuration
	}
	results := make(chan result, len(files))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8) // Limit concurrency

	for _, f := range files {
		wg.Add(1)
		go func(path string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			logger.Printf("indexing: %s [%s]\n", filepath.Base(path), path)
			content, err := os.ReadFile(path)
			if err == nil {
				p := parser.NewParser(string(content))
				config, _ := p.Parse()
				if config != nil {
					results <- result{path, config}
				}
			}
		}(f)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	for res := range results {
		pt.AddFile(res.path, res.config)
	}
	return nil
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
	Pragmas       []string
	Variables     map[string]VariableInfo
	Fields        map[string][]EvaluatedField
	IsConditional bool
}

type EvaluatedField struct {
	Raw   *parser.Field
	Value parser.Value
	File  string
}

type EvaluationContext struct {
	Variables map[string]parser.Value
	Parent    *EvaluationContext
	Tree      *ProjectTree
}

func (ctx *EvaluationContext) Resolve(name string) parser.Value {
	if v, ok := ctx.Variables[name]; ok {
		return v
	}
	if ctx.Parent != nil {
		return ctx.Parent.Resolve(name)
	}
	// Fallback to global variables in tree?
	// But which node? The evaluation usually starts at a node.
	return nil
}

type EvaluatedDefinition struct {
	Def  parser.Definition
	Ctx  *EvaluationContext
	File string
}

type Fragment struct {
	File           string
	Definitions    []parser.Definition
	IsObject       bool
	ObjectPos      parser.Position
	EndPos         parser.Position
	Doc            string // Documentation for this fragment (if object)
	Pragmas        []string
	DefinitionDocs map[parser.Definition]string
}

func NewProjectTree() *ProjectTree {
	return &ProjectTree{
		Root: &ProjectNode{
			Children:  make(map[string]*ProjectNode),
			Metadata:  make(map[string]string),
			Variables: make(map[string]VariableInfo),
			Fields:    make(map[string][]EvaluatedField),
		},
		IsolatedFiles:  make(map[string]*ProjectNode),
		GlobalPragmas:  make(map[string][]string),
		NodeMap:        make(map[string][]*ProjectNode),
		FileReferences: make(map[string][]Reference),
		Templates:      make(map[string]*parser.TemplateDefinition),
	}
}

func NormalizeName(name string) string {
	if len(name) > 0 && (name[0] == '+' || name[0] == '$') {
		return name[1:]
	}
	return name
}

func (pt *ProjectTree) RemoveFile(file string) {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	// Remove references for this file
	delete(pt.FileReferences, file)

	// Rebuild legacy References slice (if needed, or deprecate usage)
	pt.rebuildLegacyReferences()

	if iso, ok := pt.IsolatedFiles[file]; ok {
		pt.removeNodeTreeFromMap(iso)
		delete(pt.IsolatedFiles, file)
	}
	delete(pt.GlobalPragmas, file)
	pt.removeFileFromNode(pt.Root, file)
}

func (pt *ProjectTree) removeNodeTreeFromMap(node *ProjectNode) {
	pt.removeFromNodeMap(node)
	for _, child := range node.Children {
		pt.removeNodeTreeFromMap(child)
	}
}

func (pt *ProjectTree) removeFromNodeMap(node *ProjectNode) {
	removeFromList := func(name string) {
		if list, ok := pt.NodeMap[name]; ok {
			newList := []*ProjectNode{}
			for _, n := range list {
				if n != node {
					newList = append(newList, n)
				}
			}
			if len(newList) == 0 {
				delete(pt.NodeMap, name)
			} else {
				pt.NodeMap[name] = newList
			}
		}
	}
	removeFromList(node.Name)
	if node.RealName != node.Name {
		removeFromList(node.RealName)
	}
}

func (pt *ProjectTree) rebuildLegacyReferences() {
	pt.References = nil
	for _, refs := range pt.FileReferences {
		pt.References = append(pt.References, refs...)
	}
}

func (pt *ProjectTree) removeFileFromNode(node *ProjectNode, file string) {
	newFragments := []*Fragment{}
	for _, frag := range node.Fragments {
		if frag.File != file {
			newFragments = append(newFragments, frag)
		}
	}
	node.Fragments = newFragments

	// Re-aggregate EVERYTHING from fragments
	node.Doc = ""
	node.Metadata = make(map[string]string)
	node.Fields = make(map[string][]EvaluatedField)
	node.Variables = make(map[string]VariableInfo)
	node.Pragmas = nil

	for _, frag := range node.Fragments {
		if frag.Doc != "" {
			if node.Doc != "" {
				node.Doc += "\n\n"
			}
			node.Doc += frag.Doc
		}
		node.Pragmas = append(node.Pragmas, frag.Pragmas...)
		for _, def := range frag.Definitions {
			switch d := def.(type) {
			case *parser.Field:
				pt.extractFieldMetadata(node, d)
				node.Fields[d.Name] = append(node.Fields[d.Name], EvaluatedField{Raw: d, Value: d.Value, File: frag.File})
			case *parser.VariableDefinition:
				node.Variables[d.Name] = VariableInfo{
					Def:  d,
					File: frag.File,
					Doc:  frag.DefinitionDocs[d],
				}
			}
		}
	}

	for name, child := range node.Children {
		pt.removeFileFromNode(child, file)
		if len(child.Fragments) == 0 && len(child.Children) == 0 {
			delete(node.Children, name)
			pt.removeFromNodeMap(child)
		}
	}
}

func (pt *ProjectTree) rebuildMetadata(node *ProjectNode) {
	// Logic integrated into removeFileFromNode, but keeping for compatibility if used elsewhere
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
		if key == "Class" {
			if idx := strings.LastIndex(val, "::"); idx != -1 {
				val = val[idx+2:]
			}
		}
		node.Metadata[key] = val
	}
}

func (pt *ProjectTree) valToString(val parser.Value) string {
	if val == nil {
		return ""
	}
	switch v := val.(type) {
	case *parser.StringValue:
		return v.Value
	case *parser.ReferenceValue:
		return v.Value
	case *parser.IntValue:
		return v.Raw
	case *parser.BinaryExpression:
		if v.Operator.Type == parser.TokenConcat {
			return pt.valToString(v.Left) + pt.valToString(v.Right)
		}
	case *parser.VariableReferenceValue:
		return v.Name
	}
	return ""
}

func (pt *ProjectTree) AddFile(file string, config *parser.Configuration) {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	
	// We call internal removeFile (without lock, as we hold it)
	// But RemoveFile is public and locks.
	// We should split RemoveFile into internal/external.
	// Refactoring to avoid double lock or code dup.
	// For now, let's copy body of RemoveFile logic or use a helper.
	
	// RE-IMPLEMENTATION of RemoveFile logic inline to avoid deadlock
	delete(pt.FileReferences, file)
	if iso, ok := pt.IsolatedFiles[file]; ok {
		pt.removeNodeTreeFromMap(iso)
		delete(pt.IsolatedFiles, file)
	}
	delete(pt.GlobalPragmas, file)
	pt.removeFileFromNode(pt.Root, file)

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
			Fields:    make(map[string][]EvaluatedField),
		}
		pt.IsolatedFiles[file] = node
		pt.populateNode(node, file, config)
		pt.addToNodeMap(node)
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
				Fields:    make(map[string][]EvaluatedField),
			}
			pt.addToNodeMap(node.Children[part])
		}
		node = node.Children[part]
	}

	pt.populateNode(node, file, config)
}

func (pt *ProjectTree) addToNodeMap(n *ProjectNode) {
	add := func(name string) {
		if name == "" {
			return
		}
		list := pt.NodeMap[name]
		for _, existing := range list {
			if existing == n {
				return
			}
		}
		pt.NodeMap[name] = append(list, n)
	}
	add(n.Name)
	add(n.RealName)
}

func (pt *ProjectTree) populateNode(node *ProjectNode, file string, config *parser.Configuration) {
	fileFragment := &Fragment{
		File:           file,
		IsObject:       false,
		DefinitionDocs: make(map[parser.Definition]string),
	}

	for _, def := range config.Definitions {
		doc := pt.findDoc(config.Comments, def.Pos())
		pragmas := pt.findPragmas(config.Pragmas, def.Pos())

		switch d := def.(type) {
		case *parser.Field:
			fileFragment.Definitions = append(fileFragment.Definitions, d)
			fileFragment.DefinitionDocs[d] = doc
			pt.IndexValue(file, d.Value)
			node.Fields[d.Name] = append(node.Fields[d.Name], EvaluatedField{Raw: d, Value: d.Value, File: file})
		case *parser.VariableDefinition:
			fileFragment.Definitions = append(fileFragment.Definitions, d)
			fileFragment.DefinitionDocs[d] = doc
			node.Variables[d.Name] = VariableInfo{Def: d, File: file, Doc: doc}
		case *parser.ObjectNode:
			fileFragment.Definitions = append(fileFragment.Definitions, d)
			fileFragment.DefinitionDocs[d] = doc
			pt.IndexExpressionVariables(file, d.Name)
			objName := pt.valToString(d.Name)
			norm := NormalizeName(objName)
			if _, ok := node.Children[norm]; !ok {
				node.Children[norm] = &ProjectNode{
					Name:      norm,
					RealName:  objName,
					Children:  make(map[string]*ProjectNode),
					Parent:    node,
					Metadata:  make(map[string]string),
					Variables: make(map[string]VariableInfo),
					Fields:    make(map[string][]EvaluatedField),
				}
			}
			child := node.Children[norm]
			if child.RealName == norm && objName != norm {
				child.RealName = objName
			}
			pt.addToNodeMap(child)

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
		case *parser.IfBlock:
			fileFragment.Definitions = append(fileFragment.Definitions, d)
			pt.IndexValue(file, d.Condition)
			pt.indexNestedDefinitions(node, file, d.Then, config.Comments, config.Pragmas)
			pt.indexNestedDefinitions(node, file, d.Else, config.Comments, config.Pragmas)
		case *parser.ForeachBlock:
			fileFragment.Definitions = append(fileFragment.Definitions, d)
			pt.IndexValue(file, d.Iterable)
			// Add loop variables to index
			if d.KeyVar != "" {
				node.Variables[d.KeyVar] = VariableInfo{
					Def:  &parser.VariableDefinition{Name: d.KeyVar, Position: d.Position, TypeExpr: "loop key"},
					File: file,
				}
			}
			if d.ValueVar != "" {
				node.Variables[d.ValueVar] = VariableInfo{
					Def:  &parser.VariableDefinition{Name: d.ValueVar, Position: d.Position, TypeExpr: "loop value"},
					File: file,
				}
			}
			pt.indexNestedDefinitions(node, file, d.Body, config.Comments, config.Pragmas)
		case *parser.TemplateDefinition:
			fileFragment.Definitions = append(fileFragment.Definitions, d)
			pt.Templates[d.Name] = d
			// Template params
			for _, p := range d.Parameters {
				node.Variables[p.Name] = VariableInfo{
					Def:  &parser.VariableDefinition{Name: p.Name, Position: d.Position, TypeExpr: "template parameter: " + p.TypeExpr},
					File: file,
				}
				if p.DefaultValue != nil {
					pt.IndexValue(file, p.DefaultValue)
				}
			}
			pt.indexNestedDefinitions(node, file, d.Body, config.Comments, config.Pragmas)
		case *parser.TemplateInstantiation:
			fileFragment.Definitions = append(fileFragment.Definitions, d)
			pt.FileReferences[file] = append(pt.FileReferences[file], Reference{
				Name:     d.Template,
				Position: d.Position,
				File:     file,
			})
			for _, arg := range d.Arguments {
				pt.IndexValue(file, arg.Value)
			}
		default:
			fileFragment.Definitions = append(fileFragment.Definitions, d)
		}
	}

	if len(fileFragment.Definitions) > 0 {
		node.Fragments = append(node.Fragments, fileFragment)
	}
}

func (pt *ProjectTree) addObjectFragment(node *ProjectNode, file string, obj *parser.ObjectNode, doc string, comments []parser.Comment, pragmas []parser.Pragma) {
	selfPragmas := pt.findPragmas(pragmas, obj.Position)
	frag := &Fragment{
		File:           file,
		IsObject:       true,
		ObjectPos:      obj.Position,
		EndPos:         obj.Subnode.EndPosition,
		Doc:            doc,
		Pragmas:        selfPragmas,
		DefinitionDocs: make(map[parser.Definition]string),
	}

	for _, def := range obj.Subnode.Definitions {
		subDoc := pt.findDoc(comments, def.Pos())
		subPragmas := pt.findPragmas(pragmas, def.Pos())

		switch d := def.(type) {
		case *parser.Field:
			frag.Definitions = append(frag.Definitions, d)
			frag.DefinitionDocs[d] = subDoc
			pt.IndexValue(file, d.Value)
			pt.extractFieldMetadata(node, d)
			node.Fields[d.Name] = append(node.Fields[d.Name], EvaluatedField{Raw: d, Value: d.Value, File: file})
		case *parser.VariableDefinition:
			frag.Definitions = append(frag.Definitions, d)
			frag.DefinitionDocs[d] = subDoc
			node.Variables[d.Name] = VariableInfo{Def: d, File: file, Doc: subDoc}
		case *parser.ObjectNode:
			frag.Definitions = append(frag.Definitions, d)
			frag.DefinitionDocs[d] = subDoc
			pt.IndexExpressionVariables(file, d.Name)
			objName := pt.valToString(d.Name)
			norm := NormalizeName(objName)
			if _, ok := node.Children[norm]; !ok {
				node.Children[norm] = &ProjectNode{
					Name:      norm,
					RealName:  objName,
					Children:  make(map[string]*ProjectNode),
					Parent:    node,
					Metadata:  make(map[string]string),
					Variables: make(map[string]VariableInfo),
					Fields:    make(map[string][]EvaluatedField),
				}
			}
			child := node.Children[norm]
			if child.RealName == norm && objName != norm {
				child.RealName = objName
			}
			pt.addToNodeMap(child)

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
		case *parser.IfBlock:
			frag.Definitions = append(frag.Definitions, d)
			pt.IndexValue(file, d.Condition)
			pt.indexNestedDefinitions(node, file, d.Then, comments, pragmas)
			pt.indexNestedDefinitions(node, file, d.Else, comments, pragmas)
		case *parser.ForeachBlock:
			frag.Definitions = append(frag.Definitions, d)
			pt.IndexValue(file, d.Iterable)
			if d.KeyVar != "" {
				node.Variables[d.KeyVar] = VariableInfo{
					Def:  &parser.VariableDefinition{Name: d.KeyVar, Position: d.Position, TypeExpr: "loop key"},
					File: file,
				}
			}
			if d.ValueVar != "" {
				node.Variables[d.ValueVar] = VariableInfo{
					Def:  &parser.VariableDefinition{Name: d.ValueVar, Position: d.Position, TypeExpr: "loop value"},
					File: file,
				}
			}
			pt.indexNestedDefinitions(node, file, d.Body, comments, pragmas)
		case *parser.TemplateDefinition:
			frag.Definitions = append(frag.Definitions, d)
			pt.Templates[d.Name] = d
			for _, p := range d.Parameters {
				node.Variables[p.Name] = VariableInfo{
					Def:  &parser.VariableDefinition{Name: p.Name, Position: d.Position, TypeExpr: "template parameter: " + p.TypeExpr},
					File: file,
				}
				if p.DefaultValue != nil {
					pt.IndexValue(file, p.DefaultValue)
				}
			}
			pt.indexNestedDefinitions(node, file, d.Body, comments, pragmas)
		case *parser.TemplateInstantiation:
			frag.Definitions = append(frag.Definitions, d)
			pt.FileReferences[file] = append(pt.FileReferences[file], Reference{
				Name:     d.Template,
				Position: d.Position,
				File:     file,
			})
			for _, arg := range d.Arguments {
				pt.IndexValue(file, arg.Value)
			}
		default:
			frag.Definitions = append(frag.Definitions, d)
		}
	}

	node.Fragments = append(node.Fragments, frag)
}

func (pt *ProjectTree) indexNestedDefinitions(node *ProjectNode, file string, defs []parser.Definition, comments []parser.Comment, pragmas []parser.Pragma) {
	for _, def := range defs {
		doc := pt.findDoc(comments, def.Pos())
		subPragmas := pt.findPragmas(pragmas, def.Pos())

		switch d := def.(type) {
		case *parser.Field:
			pt.IndexValue(file, d.Value)
			pt.extractFieldMetadata(node, d)
			node.Fields[d.Name] = append(node.Fields[d.Name], EvaluatedField{Raw: d, Value: d.Value, File: file})
		case *parser.VariableDefinition:
			node.Variables[d.Name] = VariableInfo{Def: d, File: file, Doc: doc}
		case *parser.ObjectNode:
			pt.IndexExpressionVariables(file, d.Name)
			objName := pt.valToString(d.Name)
			norm := NormalizeName(objName)
			if _, ok := node.Children[norm]; !ok {
				node.Children[norm] = &ProjectNode{
					Name:          norm,
					RealName:      objName,
					Children:      make(map[string]*ProjectNode),
					Parent:        node,
					Metadata:      make(map[string]string),
					Variables:     make(map[string]VariableInfo),
					Fields:        make(map[string][]EvaluatedField),
					IsConditional: true,
				}
			}
			child := node.Children[norm]
			child.IsConditional = true
			if child.RealName == norm && objName != norm {
				child.RealName = objName
			}
			pt.addToNodeMap(child)

			if doc != "" {
				if child.Doc != "" {
					child.Doc += "\n\n"
				}
				child.Doc += doc
			}

			if len(subPragmas) > 0 {
				child.Pragmas = append(child.Pragmas, subPragmas...)
			}

			pt.addObjectFragment(child, file, d, doc, comments, pragmas)
		case *parser.IfBlock:
			pt.IndexValue(file, d.Condition)
			pt.indexNestedDefinitions(node, file, d.Then, comments, pragmas)
			pt.indexNestedDefinitions(node, file, d.Else, comments, pragmas)
		case *parser.ForeachBlock:
			pt.IndexValue(file, d.Iterable)
			if d.KeyVar != "" {
				node.Variables[d.KeyVar] = VariableInfo{
					Def:  &parser.VariableDefinition{Name: d.KeyVar, Position: d.Position, TypeExpr: "loop key"},
					File: file,
				}
			}
			if d.ValueVar != "" {
				node.Variables[d.ValueVar] = VariableInfo{
					Def:  &parser.VariableDefinition{Name: d.ValueVar, Position: d.Position, TypeExpr: "loop value"},
					File: file,
				}
			}
			pt.indexNestedDefinitions(node, file, d.Body, comments, pragmas)
		case *parser.TemplateDefinition:
			pt.Templates[d.Name] = d
			for _, p := range d.Parameters {
				node.Variables[p.Name] = VariableInfo{
					Def:  &parser.VariableDefinition{Name: p.Name, Position: d.Position, TypeExpr: "template parameter: " + p.TypeExpr},
					File: file,
				}
				if p.DefaultValue != nil {
					pt.IndexValue(file, p.DefaultValue)
				}
			}
			pt.indexNestedDefinitions(node, file, d.Body, comments, pragmas)
		case *parser.TemplateInstantiation:
			pt.FileReferences[file] = append(pt.FileReferences[file], Reference{
				Name:     d.Template,
				Position: d.Position,
				File:     file,
			})
			for _, arg := range d.Arguments {
				pt.IndexValue(file, arg.Value)
			}
		}
	}
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

func (pt *ProjectTree) IndexValue(file string, val parser.Value) {
	switch v := val.(type) {
	case *parser.ReferenceValue:
		ref := Reference{
			Name:     v.Value,
			Position: v.Position,
			File:     file,
		}
		pt.FileReferences[file] = append(pt.FileReferences[file], ref)
		// Maintain legacy slice for now
		pt.References = append(pt.References, ref)
	case *parser.VariableReferenceValue:
		name := strings.TrimPrefix(v.Name, "@")
		ref := Reference{
			Name:       name,
			Position:   v.Position,
			File:       file,
			IsVariable: true,
		}
		pt.FileReferences[file] = append(pt.FileReferences[file], ref)
		pt.References = append(pt.References, ref)
	case *parser.BinaryExpression:
		pt.IndexValue(file, v.Left)
		pt.IndexValue(file, v.Right)
	case *parser.UnaryExpression:
		pt.IndexValue(file, v.Right)
	case *parser.ArrayValue:
		for _, elem := range v.Elements {
			pt.IndexValue(file, elem)
		}
	}
}

func (pt *ProjectTree) IndexExpressionVariables(file string, val parser.Value) {
	switch v := val.(type) {
	case *parser.VariableReferenceValue:
		name := strings.TrimPrefix(v.Name, "@")
		ref := Reference{
			Name:       name,
			Position:   v.Position,
			File:       file,
			IsVariable: true,
		}
		pt.FileReferences[file] = append(pt.FileReferences[file], ref)
		pt.References = append(pt.References, ref)
	case *parser.BinaryExpression:
		pt.IndexExpressionVariables(file, v.Left)
		pt.IndexExpressionVariables(file, v.Right)
	case *parser.UnaryExpression:
		pt.IndexExpressionVariables(file, v.Right)
	case *parser.ArrayValue:
		for _, e := range v.Elements {
			pt.IndexExpressionVariables(file, e)
		}
	}
}

func (pt *ProjectTree) RebuildIndex() {
	if pt.NodeMap == nil {
		pt.NodeMap = make(map[string][]*ProjectNode)
	}
	if len(pt.NodeMap) > 0 {
		return
	}

	visitor := func(n *ProjectNode) {
		pt.NodeMap[n.Name] = append(pt.NodeMap[n.Name], n)
		if n.RealName != n.Name {
			pt.NodeMap[n.RealName] = append(pt.NodeMap[n.RealName], n)
		}
	}
	pt.walk(visitor)
}

func (pt *ProjectTree) ResolveReferences() {
	pt.mu.Lock()
	defer pt.mu.Unlock()

	if len(pt.NodeMap) == 0 {
		pt.RebuildIndex()
	}
	
	// We need to resolve ALL references?
	// Or only those that are unresolved or might have changed?
	// For simplicity, resolve all, but avoid tree walking if possible.
	
	// Iterate map
	pt.References = nil // Clear legacy slice to rebuild it consistent with map
	
	for _, refs := range pt.FileReferences {
		for i := range refs {
			ref := &refs[i]
			container := pt.getNodeContaining(ref.File, ref.Position)

			if v := pt.resolveVariable(container, ref.Name); v != nil {
				ref.TargetVariable = v.Def
			} else {
				ref.Target = pt.resolveName(container, ref.Name, nil)
			}
			pt.References = append(pt.References, *ref) // Keep legacy slice updated
		}
		// Update map? No, ref is pointer to slice elem?
		// No, &refs[i] points to slice backing array.
		// So map is updated.
	}
}

func (pt *ProjectTree) EvaluateDefinitions(defs []parser.Definition, ctx *EvaluationContext, file string) []EvaluatedDefinition {
	var result []EvaluatedDefinition
	for _, def := range defs {
		switch d := def.(type) {
		case *parser.IfBlock:
			cond := pt.EvaluateValue(d.Condition, ctx)
			if pt.IsTrue(cond) {
				result = append(result, pt.EvaluateDefinitions(d.Then, ctx, file)...)
			} else {
				result = append(result, pt.EvaluateDefinitions(d.Else, ctx, file)...)
			}
		case *parser.ForeachBlock:
			iterable := pt.EvaluateValue(d.Iterable, ctx)
			if arr, ok := iterable.(*parser.ArrayValue); ok {
				for i, elem := range arr.Elements {
					loopCtx := &EvaluationContext{
						Variables: make(map[string]parser.Value),
						Parent:    ctx,
						Tree:      pt,
					}
					if d.KeyVar != "" {
						loopCtx.Variables[d.KeyVar] = &parser.IntValue{Value: int64(i), Raw: fmt.Sprintf("%d", i)}
					}
					loopCtx.Variables[d.ValueVar] = elem
					result = append(result, pt.EvaluateDefinitions(d.Body, loopCtx, file)...)
				}
			}
		case *parser.TemplateInstantiation:
			var tdef *parser.TemplateDefinition
			if pt.Templates != nil {
				tdef = pt.Templates[d.Template]
			}

			if tdef != nil {
				templateCtx := &EvaluationContext{
					Variables: make(map[string]parser.Value),
					Parent:    ctx,
					Tree:      pt,
				}
				// Bind arguments
				argMap := make(map[string]parser.Value)
				for _, arg := range d.Arguments {
					argMap[arg.Name] = pt.EvaluateValue(arg.Value, ctx)
				}

				for _, param := range tdef.Parameters {
					if val, ok := argMap[param.Name]; ok {
						templateCtx.Variables[param.Name] = val
					} else {
						templateCtx.Variables[param.Name] = param.DefaultValue
					}
				}
				// The template generates an object with Name d.Name
				obj := &parser.ObjectNode{
					Position: d.Position,
					Name:     &parser.StringValue{Value: d.Name, Quoted: false},
					Subnode: parser.Subnode{
						Definitions: tdef.Body,
					},
				}
				result = append(result, EvaluatedDefinition{Def: obj, Ctx: templateCtx, File: file})
			}
		case *parser.TemplateDefinition:
			// Skip template definitions during evaluation
		case *parser.VariableDefinition:
			if d.DefaultValue != nil {
				ctx.Variables[d.Name] = pt.EvaluateValue(d.DefaultValue, ctx)
			}
		default:
			result = append(result, EvaluatedDefinition{Def: d, Ctx: ctx, File: file})
		}
	}
	return result
}

func (pt *ProjectTree) EvaluateValue(val parser.Value, ctx *EvaluationContext) parser.Value {
	switch v := val.(type) {
	case *parser.VariableReferenceValue:
		name := strings.TrimPrefix(v.Name, "@")
		if res := ctx.Resolve(name); res != nil {
			return pt.EvaluateValue(res, ctx)
		}
		// Fallback to tree variables if ctx resolution failed
		// We need a ProjectNode for resolveVariable.
		// Evaluation context might not have one directly.
		return v
	case *parser.BinaryExpression:
		left := pt.EvaluateValue(v.Left, ctx)
		right := pt.EvaluateValue(v.Right, ctx)
		if res := pt.compute(left, v.Operator, right); res != nil {
			return res
		}
		return &parser.BinaryExpression{
			Position: v.Position,
			Left:     left,
			Operator: v.Operator,
			Right:    right,
		}
	case *parser.UnaryExpression:
		right := pt.EvaluateValue(v.Right, ctx)
		if res := pt.computeUnary(v.Operator, right); res != nil {
			return res
		}
		return &parser.UnaryExpression{
			Position: v.Position,
			Operator: v.Operator,
			Right:    right,
		}
	case *parser.ArrayValue:
		newElems := make([]parser.Value, len(v.Elements))
		for i, e := range v.Elements {
			newElems[i] = pt.EvaluateValue(e, ctx)
		}
		return &parser.ArrayValue{
			Position:    v.Position,
			EndPosition: v.EndPosition,
			Elements:    newElems,
		}
	}
	return val
}

func (pt *ProjectTree) IsTrue(val parser.Value) bool {
	switch v := val.(type) {
	case *parser.BoolValue:
		return v.Value
	case *parser.IntValue:
		return v.Value != 0
	case *parser.FloatValue:
		return v.Value != 0
	case *parser.StringValue:
		return v.Value != ""
	}
	return false
}
	// Internal usage might already hold lock.
	// But FindNode is public.
	// We need to be careful about recursive locking.
	// ResolveName calls FindNode.
	// ResolveName is public.
	// If ResolveReferences (Lock) calls ResolveName (Lock) -> Deadlock.
	
	// STRATEGY: Public methods Lock. Private methods don't.
	// Rename internal implementation to findNodeInternal.
	// Public FindNode calls Lock + findNodeInternal.
	
	// But FindNode is used by ResolveName.
	// ResolveName is used by ResolveReferences.
	// ResolveReferences holds Lock.
	
	// I should make FindNode assume NO lock? No, that's unsafe for public usage.
	// I should make FindNode acquire RLock.
	// But if called from ResolveReferences (holding Lock), RLock might block?
	// RWMutex: If Lock is held, RLock blocks.
	
	// So I MUST separate internal/external.
	// Or pass a context?
	
	// Quick fix: Since I control the code, I will rename `FindNode` to `findNode` (private)
	// and make `FindNode` (public) wrap it with RLock.
	// And update internal callers (`ResolveName`) to use `findNode`?
	// `ResolveName` is public too.
	
	// Let's defer adding lock to FindNode inside index.go for a moment and check callers.
	// `ResolveName` calls `FindNode`.
	// `ResolveReferences` calls `ResolveName`.
	
	// If I modify `ResolveReferences` to call `resolveNameInternal`?
	// It seems deep refactoring is needed to do proper locking.
	
func (pt *ProjectTree) FindNode(root *ProjectNode, name string, predicate func(*ProjectNode) bool, strict bool) *ProjectNode {
	pt.mu.RLock()
	defer pt.mu.RUnlock()
	return pt.findNode(root, name, predicate, strict)
}

func (pt *ProjectTree) findNode(root *ProjectNode, name string, predicate func(*ProjectNode) bool, strict bool) *ProjectNode {
	if pt.NodeMap == nil {
		pt.RebuildIndex()
	}

	if strings.Contains(name, ".") {
		parts := strings.Split(name, ".")
		rootName := parts[0]

		candidates := pt.NodeMap[rootName]
		var bestMatch *ProjectNode

		for _, cand := range candidates {
			if strict {
				if cand.Parent != root {
					continue
				}
			} else {
				if !pt.isDescendant(cand, root) {
					continue
				}
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
					bestMatch = curr
					return curr
				}
			}
		}
		return bestMatch
	}

	candidates := pt.NodeMap[name]
	for _, cand := range candidates {
		if strict {
			if cand.Parent != root {
				continue
			}
		} else {
			if !pt.isDescendant(cand, root) {
				continue
			}
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
	pt.mu.RLock()
	defer pt.mu.RUnlock()
	// Search in FileReferences instead of global slice
	if refs, ok := pt.FileReferences[file]; ok {
		for i := range refs {
			ref := &refs[i]
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
	pt.mu.RLock()
	defer pt.mu.RUnlock()
	pt.walk(visitor)
}

func (pt *ProjectTree) walk(visitor func(*ProjectNode)) {
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
						if col >= v.Position.Column && col < v.Position.Column+len(v.Name)+5 {
							return &QueryResult{Variable: v}
						}
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
	pt.mu.RLock()
	defer pt.mu.RUnlock()
	return pt.getNodeContaining(file, pos)
}

func (pt *ProjectTree) getNodeContaining(file string, pos parser.Position) *ProjectNode {
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
		if frag.File == file {
			if frag.IsObject {
				start := frag.ObjectPos
				end := frag.EndPos

				if (pos.Line > start.Line || (pos.Line == start.Line && pos.Column >= start.Column)) &&
					(pos.Line < end.Line || (pos.Line == end.Line && pos.Column <= end.Column)) {
					return node
				}
			} else {
				// Package-level fragment matches if not inside any child object
				return node
			}
		}
	}
	return nil
}

func (pt *ProjectTree) ResolveName(ctx *ProjectNode, name string, predicate func(*ProjectNode) bool) *ProjectNode {
	pt.mu.RLock()
	defer pt.mu.RUnlock()
	return pt.resolveName(ctx, name, predicate)
}

func (pt *ProjectTree) resolveName(ctx *ProjectNode, name string, predicate func(*ProjectNode) bool) *ProjectNode {
	if ctx == nil {
		return pt.findNode(pt.Root, name, predicate, true)
	}

	curr := ctx
	for curr != nil {
		if found := pt.findNode(curr, name, predicate, false); found != nil {
			return found
		}
		curr = curr.Parent
	}

	// Fallback to global root if not found in local scope chain (Strict search)
	if pt.Root != nil {
		if found := pt.findNode(pt.Root, name, predicate, true); found != nil {
			return found
		}
	}

	return nil
}

func (pt *ProjectTree) ResolveVariable(ctx *ProjectNode, name string) *VariableInfo {
	pt.mu.RLock()
	defer pt.mu.RUnlock()
	return pt.resolveVariable(ctx, name)
}

func (pt *ProjectTree) resolveVariable(ctx *ProjectNode, name string) *VariableInfo {
	curr := ctx
	for curr != nil {
		if v, ok := curr.Variables[name]; ok {
			return &v
		}
		curr = curr.Parent
	}
	if pt.Root != nil {
		if v, ok := pt.Root.Variables[name]; ok {
			return &v
		}
	}
	return nil
}

func (pt *ProjectTree) Evaluate(val parser.Value, ctx *ProjectNode) parser.Value {
	pt.mu.RLock()
	defer pt.mu.RUnlock()
	return pt.evaluate(val, ctx)
}

func (pt *ProjectTree) evaluate(val parser.Value, ctx *ProjectNode) parser.Value {
	switch v := val.(type) {
	case *parser.VariableReferenceValue:
		name := strings.TrimPrefix(v.Name, "@")
		if info := pt.resolveVariable(ctx, name); info != nil {
			if info.Def.DefaultValue != nil {
				return pt.evaluate(info.Def.DefaultValue, ctx)
			}
		}
		return v
	case *parser.BinaryExpression:
		left := pt.evaluate(v.Left, ctx)
		right := pt.evaluate(v.Right, ctx)
		if res := pt.compute(left, v.Operator, right); res != nil {
			return res
		}
		return &parser.BinaryExpression{
			Position: v.Position,
			Left:     left,
			Operator: v.Operator,
			Right:    right,
		}
	case *parser.UnaryExpression:
		right := pt.evaluate(v.Right, ctx)
		if res := pt.computeUnary(v.Operator, right); res != nil {
			return res
		}
		return &parser.UnaryExpression{
			Position: v.Position,
			Operator: v.Operator,
			Right:    right,
		}
	case *parser.ArrayValue:
		newElems := make([]parser.Value, len(v.Elements))
		for i, e := range v.Elements {
			newElems[i] = pt.evaluate(e, ctx)
		}
		return &parser.ArrayValue{
			Position:    v.Position,
			EndPosition: v.EndPosition,
			Elements:    newElems,
		}
	}
	return val
}

func (pt *ProjectTree) Clone() *ProjectTree {
	pt.mu.RLock()
	defer pt.mu.RUnlock()

	newPT := NewProjectTree()
	
	// Clone isolated files
	for k, v := range pt.IsolatedFiles {
		newPT.IsolatedFiles[k] = v.Clone(nil)
	}

	// Clone Root
	if pt.Root != nil {
		newPT.Root = pt.Root.Clone(nil)
	}

	// Clone GlobalPragmas
	for k, v := range pt.GlobalPragmas {
		newPragmas := make([]string, len(v))
		copy(newPragmas, v)
		newPT.GlobalPragmas[k] = newPragmas
	}

	// Clone FileReferences
	for k, v := range pt.FileReferences {
		newRefs := make([]Reference, len(v))
		// We can shallow copy references because they mostly point to data we aren't mutating deeply in place
		// BUT Reference.Target points to *ProjectNode. 
		// If we clone the nodes, we need to update the references to point to the NEW nodes.
		// This is tricky. A simple Clone() breaks the graph links (Reference -> Target).
		
		// For now, we will perform a simple copy and rely on RebuildIndex/ResolveReferences 
		// being called or handling it. 
		// Actually, in the Snapshot model, we typically 'Update' a snapshot by cloning it and applying a delta.
		// If we clone the whole tree, all pointers in 'Reference.Target' will point to the OLD tree's nodes.
		// This is BAD.
		
		// To fix this correctly, we would need to re-resolve references after cloning.
		// Since 'ResolveReferences' is fast enough (lookup in map), we can just clear Targets and re-resolve?
		// Or we can traverse and update.
		
		// Let's copy them, and then nil out the Targets so they get re-resolved lazily or explicitly.
		for i, r := range v {
			newRef := r
			newRef.Target = nil // Invalidate pointers to old tree
			newRef.TargetVariable = nil
			newRefs[i] = newRef
		}
		newPT.FileReferences[k] = newRefs
	}

	// Rebuild NodeMap
	newPT.RebuildIndex()

	// Re-resolve references (connect to new nodes)
	// Note: This makes Clone() expensive. But necessary for correctness.
	newPT.ResolveReferences()

	return newPT
}

func (pn *ProjectNode) Clone(parent *ProjectNode) *ProjectNode {
	newNode := &ProjectNode{
		Name:      pn.Name,
		RealName:  pn.RealName,
		Doc:       pn.Doc,
		Parent:    parent,
		Children:  make(map[string]*ProjectNode),
		Metadata:  make(map[string]string),
		Variables: make(map[string]VariableInfo),
		Fields:    make(map[string][]EvaluatedField),
	}

	// Clone Metadata
	for k, v := range pn.Metadata {
		newNode.Metadata[k] = v
	}

	// Clone Variables
	for k, v := range pn.Variables {
		newNode.Variables[k] = v // VariableInfo is value type, safe shallow copy
	}

	// Clone Fields
	for k, v := range pn.Fields {
		newFields := make([]EvaluatedField, len(v))
		copy(newFields, v)
		newNode.Fields[k] = newFields
	}

	// Clone Pragmas
	if len(pn.Pragmas) > 0 {
		newNode.Pragmas = make([]string, len(pn.Pragmas))
		copy(newNode.Pragmas, pn.Pragmas)
	}

	// Clone Fragments
	// Fragment pointers can be shared? 
	// Fragments contain AST nodes (*parser.Definition).
	// If we don't mutate AST nodes, sharing fragments is fine.
	// But Fragments also contain IsObject, Doc...
	// Ideally we clone Fragments too.
	for _, frag := range pn.Fragments {
		newFrag := *frag // Shallow copy of struct
		// Deep copy slices in fragment if necessary
		if len(frag.Definitions) > 0 {
			newDefs := make([]parser.Definition, len(frag.Definitions))
			copy(newDefs, frag.Definitions)
			newFrag.Definitions = newDefs
		}
		if len(frag.Pragmas) > 0 {
			newPrags := make([]string, len(frag.Pragmas))
			copy(newPrags, frag.Pragmas)
			newFrag.Pragmas = newPrags
		}
		// DefinitionDocs map
		if len(frag.DefinitionDocs) > 0 {
			newDocs := make(map[parser.Definition]string)
			for k, v := range frag.DefinitionDocs {
				newDocs[k] = v
			}
			newFrag.DefinitionDocs = newDocs
		}
		newNode.Fragments = append(newNode.Fragments, &newFrag)
	}

	// Recursively clone children
	for k, child := range pn.Children {
		newNode.Children[k] = child.Clone(newNode)
	}
	
	// Note: newNode.Target is NOT cloned here because it points to an arbitrary node in the tree.
	// It must be resolved after the full tree is built.
	// We leave it nil or deal with it in RebuildIndex/Resolve?
	// The `Target` field is set during `ResolveReferences` usually (or ResolveName).
	// So we leave it nil.

	return newNode
}

func (pt *ProjectTree) compute(left parser.Value, op parser.Token, right parser.Value) parser.Value {
	if op.Type == parser.TokenConcat {
		s1 := pt.valueToString(left)
		s2 := pt.valueToString(right)
		return &parser.StringValue{Value: s1 + s2, Quoted: true}
	}

	toInt := func(v parser.Value) (int64, bool) {
		if idx, ok := v.(*parser.IntValue); ok {
			return idx.Value, true
		}
		return 0, false
	}
	toFloat := func(v parser.Value) (float64, bool) {
		if f, ok := v.(*parser.FloatValue); ok {
			return f.Value, true
		}
		if idx, ok := v.(*parser.IntValue); ok {
			return float64(idx.Value), true
		}
		return 0, false
	}

	lI, lIsI := toInt(left)
	rI, rIsI := toInt(right)

	if lIsI && rIsI {
		var res int64
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

	lF, lIsF := toFloat(left)
	rF, rIsF := toFloat(right)

	if lIsF && rIsF {
		var res float64
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

	// Boolean comparisons
	lB, lIsB := left.(*parser.BoolValue)
	rB, rIsB := right.(*parser.BoolValue)
	if lIsB && rIsB {
		if op.Type == parser.TokenSymbol {
			switch op.Value {
			case "==":
				return &parser.BoolValue{Value: lB.Value == rB.Value}
			case "!=":
				return &parser.BoolValue{Value: lB.Value != rB.Value}
			case "&&":
				return &parser.BoolValue{Value: lB.Value && rB.Value}
			case "||":
				return &parser.BoolValue{Value: lB.Value || rB.Value}
			}
		}
	}

	// String comparison
	lS, lIsS := left.(*parser.StringValue)
	rS, rIsS := right.(*parser.StringValue)
	if lIsS && rIsS {
		if op.Type == parser.TokenSymbol {
			switch op.Value {
			case "==":
				return &parser.BoolValue{Value: lS.Value == rS.Value}
			case "!=":
				return &parser.BoolValue{Value: lS.Value != rS.Value}
			}
		}
	}

	return nil
}

func (pt *ProjectTree) computeUnary(op parser.Token, val parser.Value) parser.Value {
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

func (pt *ProjectTree) ValueToString(val parser.Value) string {
	return pt.valueToString(val)
}

func (pt *ProjectTree) valueToString(val parser.Value) string {
	switch v := val.(type) {
	case *parser.StringValue:
		return v.Value
	case *parser.IntValue:
		return v.Raw
	case *parser.FloatValue:
		return v.Raw
	case *parser.BoolValue:
		return fmt.Sprintf("%v", v.Value)
	case *parser.ReferenceValue:
		return v.Value
	case *parser.VariableReferenceValue:
		return v.Name
	case *parser.ArrayValue:
		elements := []string{}
		for _, e := range v.Elements {
			elements = append(elements, pt.valueToString(e))
		}
		return fmt.Sprintf("{ %s }", strings.Join(elements, " "))
	default:
		return ""
	}
}

func (pt *ProjectTree) ResolveFields() {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	pt.walk(func(node *ProjectNode) {
		for _, fields := range node.Fields {
			for i := range fields {
				fields[i].Value = pt.evaluate(fields[i].Raw.Value, node)
			}
		}
	})
}