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
	if node.RealName != "" && (node.RealName[0] == '+' || node.RealName[0] == '$') {
		hasClass := false
		hasType := false
		for _, frag := range node.Fragments {
			for _, def := range frag.Definitions {
				if f, ok := def.(*parser.Field); ok {
					if f.Name == "Class" {
						hasClass = true
					}
					if f.Name == "Type" {
						hasType = true
					}
				}
			}
			if hasClass {
				break
			}
		}
		
		if !hasClass && !hasType {
			// Report error on the first fragment's position
			pos := parser.Position{Line: 1, Column: 1}
			file := ""
			if len(node.Fragments) > 0 {
				pos = node.Fragments[0].ObjectPos
				file = node.Fragments[0].File
			}
			v.Diagnostics = append(v.Diagnostics, Diagnostic{
				Level:    LevelError,
				Message:  fmt.Sprintf("Node %s is an object and must contain a 'Class' field (or be a Signal with 'Type')", node.RealName),
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

func (v *Validator) CheckUnused() {
	referencedNodes := make(map[*index.ProjectNode]bool)
	for _, ref := range v.Tree.References {
		if ref.Target != nil {
			referencedNodes[ref.Target] = true
		}
	}

	v.checkUnusedRecursive(v.Tree.Root, referencedNodes)
}

func (v *Validator) checkUnusedRecursive(node *index.ProjectNode, referenced map[*index.ProjectNode]bool) {
	// Heuristic for GAM
	if isGAM(node) {
		if !referenced[node] {
			v.Diagnostics = append(v.Diagnostics, Diagnostic{
				Level:    LevelWarning,
				Message:  fmt.Sprintf("Unused GAM: %s is defined but not referenced in any thread or scheduler", node.RealName),
				Position: v.getNodePosition(node),
				File:     v.getNodeFile(node),
			})
		}
	}

	// Heuristic for DataSource and its signals
	if isDataSource(node) {
		for _, signal := range node.Children {
			if !referenced[signal] {
				v.Diagnostics = append(v.Diagnostics, Diagnostic{
					Level:    LevelWarning,
					Message:  fmt.Sprintf("Unused Signal: %s is defined in DataSource %s but never referenced", signal.RealName, node.RealName),
					Position: v.getNodePosition(signal),
					File:     v.getNodeFile(signal),
				})
			}
		}
	}

	for _, child := range node.Children {
		v.checkUnusedRecursive(child, referenced)
	}
}

func isGAM(node *index.ProjectNode) bool {
	if node.RealName == "" || (node.RealName[0] != '+' && node.RealName[0] != '$') {
		return false
	}
	_, hasInput := node.Children["InputSignals"]
	_, hasOutput := node.Children["OutputSignals"]
	return hasInput || hasOutput
}

func isDataSource(node *index.ProjectNode) bool {
	if node.Parent != nil && node.Parent.Name == "Data" {
		return true
	}
	return false
}

func (v *Validator) getNodePosition(node *index.ProjectNode) parser.Position {
	if len(node.Fragments) > 0 {
		return node.Fragments[0].ObjectPos
	}
	return parser.Position{Line: 1, Column: 1}
}

func (v *Validator) getNodeFile(node *index.ProjectNode) string {
	if len(node.Fragments) > 0 {
		return node.Fragments[0].File
	}
	return ""
}