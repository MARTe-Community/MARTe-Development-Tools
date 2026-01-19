package index

import (
	"github.com/marte-dev/marte-dev-tools/internal/parser"
)

type SymbolType int

const (
	SymbolObject SymbolType = iota
	SymbolSignal
	SymbolDataSource
	SymbolGAM
)

type Symbol struct {
	Name     string
	Type     SymbolType
	Position parser.Position
	File     string
	Doc      string
	Class    string
	Parent   *Symbol
}

type Reference struct {
	Name     string
	Position parser.Position
	File     string
	Target   *Symbol
}

type Index struct {
	Symbols    map[string]*Symbol
	References []Reference
	Packages   map[string][]string // pkgURI -> list of files
}

func NewIndex() *Index {
	return &Index{
		Symbols:  make(map[string]*Symbol),
		Packages: make(map[string][]string),
	}
}

func (idx *Index) IndexConfig(file string, config *parser.Configuration) {
	pkgURI := ""
	if config.Package != nil {
		pkgURI = config.Package.URI
	}
	idx.Packages[pkgURI] = append(idx.Packages[pkgURI], file)

	for _, def := range config.Definitions {
		idx.indexDefinition(file, "", nil, def)
	}
}

func (idx *Index) indexDefinition(file string, path string, parent *Symbol, def parser.Definition) {
	switch d := def.(type) {
	case *parser.ObjectNode:
		name := d.Name
		fullPath := name
		if path != "" {
			fullPath = path + "." + name
		}
		
		class := ""
		for _, subDef := range d.Subnode.Definitions {
			if f, ok := subDef.(*parser.Field); ok && f.Name == "Class" {
				if s, ok := f.Value.(*parser.StringValue); ok {
					class = s.Value
				} else if r, ok := f.Value.(*parser.ReferenceValue); ok {
					class = r.Value
				}
			}
		}

		symType := SymbolObject
		// Simple heuristic for GAM or DataSource if class name matches or node name starts with +/$
		// In a real implementation we would check the class against known MARTe classes

		sym := &Symbol{
			Name:     fullPath,
			Type:     symType,
			Position: d.Position,
			File:     file,
			Class:    class,
			Parent:   parent,
		}
		idx.Symbols[fullPath] = sym

		for _, subDef := range d.Subnode.Definitions {
			idx.indexDefinition(file, fullPath, sym, subDef)
		}

	case *parser.Field:
		idx.indexValue(file, d.Value)
	}
}

func (idx *Index) indexValue(file string, val parser.Value) {
	switch v := val.(type) {
	case *parser.ReferenceValue:
		idx.References = append(idx.References, Reference{
			Name:     v.Value,
			Position: v.Position,
			File:     file,
		})
	case *parser.ArrayValue:
		for _, elem := range v.Elements {
			idx.indexValue(file, elem)
		}
	}
}

func (idx *Index) ResolveReferences() {
	for i := range idx.References {
		ref := &idx.References[i]
		if sym, ok := idx.Symbols[ref.Name]; ok {
			ref.Target = sym
		} else {
			// Try relative resolution?
		}
	}
}