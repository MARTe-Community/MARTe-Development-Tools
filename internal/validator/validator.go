package validator

import (
	"fmt"
	"github.com/marte-dev/marte-dev-tools/internal/index"
	"github.com/marte-dev/marte-dev-tools/internal/parser"
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
	Tree        *index.ProjectTree
}

func NewValidator(tree *index.ProjectTree) *Validator {
	return &Validator{Tree: tree}
}

func (v *Validator) ValidateProject() {
	if v.Tree == nil || v.Tree.Root == nil {
		return
	}
	v.validateNode(v.Tree.Root)
}

func (v *Validator) validateNode(node *index.ProjectNode) {
	// Check for duplicate fields in this node
	fields := make(map[string]string) // FieldName -> File
	
	for _, frag := range node.Fragments {
		for _, def := range frag.Definitions {
			if f, ok := def.(*parser.Field); ok {
				if existingFile, exists := fields[f.Name]; exists {
					// Duplicate field
					v.Diagnostics = append(v.Diagnostics, Diagnostic{
						Level:    LevelError,
						Message:  fmt.Sprintf("Duplicate Field Definition: '%s' is already defined in %s", f.Name, existingFile),
						Position: f.Position,
						File:     frag.File,
					})
				} else {
					fields[f.Name] = frag.File
				}
			}
		}
	}

	// Check for mandatory Class if it's an object node (+/$)
	// Root node usually doesn't have a name or is implicit
	if node.RealName != "" && (node.RealName[0] == '+' || node.RealName[0] == '$') {
		hasClass := false
		for _, frag := range node.Fragments {
			for _, def := range frag.Definitions {
				if f, ok := def.(*parser.Field); ok && f.Name == "Class" {
					hasClass = true
					break
				}
			}
			if hasClass {
				break
			}
		}
		
		if !hasClass {
			// Report error on the first fragment's position
			pos := parser.Position{Line: 1, Column: 1}
			file := ""
			if len(node.Fragments) > 0 {
				pos = node.Fragments[0].ObjectPos
				file = node.Fragments[0].File
			}
			v.Diagnostics = append(v.Diagnostics, Diagnostic{
				Level:    LevelError,
				Message:  fmt.Sprintf("Node %s is an object and must contain a 'Class' field", node.RealName),
				Position: pos,
				File:     file,
			})
		}
	}

	// Recursively validate children
	for _, child := range node.Children {
		v.validateNode(child)
	}
}

// Legacy/Compatibility method if needed, but we prefer ValidateProject
func (v *Validator) Validate(file string, config *parser.Configuration) {
	// No-op or local checks if any
}

func (v *Validator) CheckUnused() {
	// To implement unused check, we'd need reference tracking in Index
	// For now, focusing on duplicate fields and class validation
}