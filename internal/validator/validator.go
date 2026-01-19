package validator

import (
	"fmt"
	"github.com/marte-dev/marte-dev-tools/internal/parser"
	"github.com/marte-dev/marte-dev-tools/internal/index"
)

type DiagnosticLevel int

const (
	LevelError DiagnosticLevel = iota
	LevelWarning
)

type Diagnostic struct {
	Level    DiagnosticLevel
	Message  string
	Position parser.Position
	File     string
}

type Validator struct {
	Diagnostics []Diagnostic
	Index       *index.Index
}

func NewValidator(idx *index.Index) *Validator {
	return &Validator{Index: idx}
}

func (v *Validator) Validate(file string, config *parser.Configuration) {
	for _, def := range config.Definitions {
		v.validateDefinition(file, "", config, def)
	}
}

func (v *Validator) validateDefinition(file string, path string, config *parser.Configuration, def parser.Definition) {
	switch d := def.(type) {
	case *parser.ObjectNode:
		name := d.Name
		fullPath := name
		if path != "" {
			fullPath = path + "." + name
		}

		// Check for mandatory 'Class' field for +/$ nodes
		if d.Name != "" && (d.Name[0] == '+' || d.Name[0] == '$') {
			hasClass := false
			for _, subDef := range d.Subnode.Definitions {
				if f, ok := subDef.(*parser.Field); ok && f.Name == "Class" {
					hasClass = true
					break
				}
			}
			if !hasClass {
				v.Diagnostics = append(v.Diagnostics, Diagnostic{
					Level:    LevelError,
					Message:  fmt.Sprintf("Node %s is an object and must contain a 'Class' field", d.Name),
					Position: d.Position,
					File:     file,
				})
			}
		}

		// GAM specific validation
		// (This is a placeholder, real logic would check if it's a GAM)

		for _, subDef := range d.Subnode.Definitions {
			v.validateDefinition(file, fullPath, config, subDef)
		}
	}
}

func (v *Validator) CheckUnused() {
	if v.Index == nil {
		return
	}

	referencedSymbols := make(map[*index.Symbol]bool)
	for _, ref := range v.Index.References {
		if ref.Target != nil {
			referencedSymbols[ref.Target] = true
		}
	}

	for _, sym := range v.Index.Symbols {
		// Heuristic: if it's a GAM or Signal, check if referenced
		// (Refining this later with proper class checks)
		if !referencedSymbols[sym] {
			// Logic to determine if it should be warned as unused
			// e.g. if sym.Class is a GAM or if it's a signal in a DataSource
		}
	}
}
