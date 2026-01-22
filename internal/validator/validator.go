package validator

import (
	"fmt"
	"github.com/marte-dev/marte-dev-tools/internal/index"
	"github.com/marte-dev/marte-dev-tools/internal/parser"
	"github.com/marte-dev/marte-dev-tools/internal/schema"
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
	Schema      *schema.Schema
}

func NewValidator(tree *index.ProjectTree, projectRoot string) *Validator {
	return &Validator{
		Tree:   tree,
		Schema: schema.LoadFullSchema(projectRoot),
	}
}

func (v *Validator) ValidateProject() {
	if v.Tree == nil {
		return
	}
	if v.Tree.Root != nil {
		v.validateNode(v.Tree.Root)
	}
	for _, node := range v.Tree.IsolatedFiles {
		v.validateNode(node)
	}
}

func (v *Validator) validateNode(node *index.ProjectNode) {
	// Check for invalid content in Signals container of DataSource
	if node.RealName == "Signals" && node.Parent != nil && isDataSource(node.Parent) {
		for _, frag := range node.Fragments {
			for _, def := range frag.Definitions {
				if f, ok := def.(*parser.Field); ok {
					v.Diagnostics = append(v.Diagnostics, Diagnostic{
						Level:    LevelError,
						Message:  fmt.Sprintf("Invalid content in Signals container: Field '%s' is not allowed. Only Signal objects are allowed.", f.Name),
						Position: f.Position,
						File:     frag.File,
					})
				}
			}
		}
	}

	// Collect fields and their definitions
	fields := make(map[string][]*parser.Field)
	fieldOrder := []string{} // Keep track of order of appearance (approximate across fragments)

	for _, frag := range node.Fragments {
		for _, def := range frag.Definitions {
			if f, ok := def.(*parser.Field); ok {
				if _, exists := fields[f.Name]; !exists {
					fieldOrder = append(fieldOrder, f.Name)
				}
				fields[f.Name] = append(fields[f.Name], f)
			}
		}
	}

	// 1. Check for duplicate fields
	for name, defs := range fields {
		if len(defs) > 1 {
			// Report error on the second definition
			firstFile := v.getFileForField(defs[0], node)
			v.Diagnostics = append(v.Diagnostics, Diagnostic{
				Level:    LevelError,
				Message:  fmt.Sprintf("Duplicate Field Definition: '%s' is already defined in %s", name, firstFile),
				Position: defs[1].Position,
				File:     v.getFileForField(defs[1], node),
			})
		}
	}

	// 2. Check for mandatory Class if it's an object node (+/$)
	className := ""
	if node.RealName != "" && (node.RealName[0] == '+' || node.RealName[0] == '$') {
		if classFields, ok := fields["Class"]; ok && len(classFields) > 0 {
			// Extract class name from value
			switch val := classFields[0].Value.(type) {
			case *parser.StringValue:
				className = val.Value
			case *parser.ReferenceValue:
				className = val.Value
			}
		}

		hasType := false
		if _, ok := fields["Type"]; ok {
			hasType = true
		}

		// Exception for Signals: Signals don't need Class if they have Type.
		// But general nodes need Class.
		// Logic handles it: if className=="" and !hasType -> Error.
		if className == "" && !hasType {
			pos := v.getNodePosition(node)
			file := v.getNodeFile(node)
			v.Diagnostics = append(v.Diagnostics, Diagnostic{
				Level:    LevelError,
				Message:  fmt.Sprintf("Node %s is an object and must contain a 'Class' field (or be a Signal with 'Type')", node.RealName),
				Position: pos,
				File:     file,
			})
		}
	}

	// 3. Schema Validation
	if className != "" && v.Schema != nil {
		if classDef, ok := v.Schema.Classes[className]; ok {
			v.validateClass(node, classDef, fields, fieldOrder)
		}
	}

	// 4. Signal Validation (for DataSource signals)
	if isSignal(node) {
		v.validateSignal(node, fields)
	}

	// Recursively validate children
	for _, child := range node.Children {
		v.validateNode(child)
	}
}

func (v *Validator) validateClass(node *index.ProjectNode, classDef schema.ClassDefinition, fields map[string][]*parser.Field, fieldOrder []string) {
	// Check Mandatory Fields
	for _, fieldDef := range classDef.Fields {
		if fieldDef.Mandatory {
			found := false
			if _, ok := fields[fieldDef.Name]; ok {
				found = true
			} else if fieldDef.Type == "node" {
				// Check children for nodes
				if _, ok := node.Children[fieldDef.Name]; ok {
					found = true
				}
			}

			if !found {
				v.Diagnostics = append(v.Diagnostics, Diagnostic{
					Level:    LevelError,
					Message:  fmt.Sprintf("Missing mandatory field '%s' for class '%s'", fieldDef.Name, node.Metadata["Class"]),
					Position: v.getNodePosition(node),
					File:     v.getNodeFile(node),
				})
			}
		}
	}

	// Check Field Types
	for _, fieldDef := range classDef.Fields {
		if fList, ok := fields[fieldDef.Name]; ok {
			f := fList[0] // Check the first definition (duplicates handled elsewhere)
			if !v.checkType(f.Value, fieldDef.Type) {
				v.Diagnostics = append(v.Diagnostics, Diagnostic{
					Level:    LevelError,
					Message:  fmt.Sprintf("Field '%s' expects type '%s'", fieldDef.Name, fieldDef.Type),
					Position: f.Position,
					File:     v.getFileForField(f, node),
				})
			}
		}
	}

	// Check Field Order
	if classDef.Ordered {
		schemaIdx := 0
		for _, nodeFieldName := range fieldOrder {
			foundInSchema := false
			for i, fd := range classDef.Fields {
				if fd.Name == nodeFieldName {
					foundInSchema = true
					if i < schemaIdx {
						v.Diagnostics = append(v.Diagnostics, Diagnostic{
							Level:    LevelError,
							Message:  fmt.Sprintf("Field '%s' is out of order", nodeFieldName),
							Position: fields[nodeFieldName][0].Position,
							File:     v.getFileForField(fields[nodeFieldName][0], node),
						})
					} else {
						schemaIdx = i
					}
					break
				}
			}
			if !foundInSchema {
				// Ignore extra fields
			}
		}
	}
}

func (v *Validator) validateSignal(node *index.ProjectNode, fields map[string][]*parser.Field) {
	// Check mandatory Type
	if typeFields, ok := fields["Type"]; !ok || len(typeFields) == 0 {
		v.Diagnostics = append(v.Diagnostics, Diagnostic{
			Level:    LevelError,
			Message:  fmt.Sprintf("Signal '%s' is missing mandatory field 'Type'", node.RealName),
			Position: v.getNodePosition(node),
			File:     v.getNodeFile(node),
		})
	} else {
		// Check valid Type value
		typeVal := typeFields[0].Value
		var typeStr string
		switch t := typeVal.(type) {
		case *parser.StringValue:
			typeStr = t.Value
		case *parser.ReferenceValue:
			typeStr = t.Value
		default:
			v.Diagnostics = append(v.Diagnostics, Diagnostic{
				Level:    LevelError,
				Message:  fmt.Sprintf("Field 'Type' in Signal '%s' must be a type name", node.RealName),
				Position: typeFields[0].Position,
				File:     v.getFileForField(typeFields[0], node),
			})
			return
		}

		if !isValidType(typeStr) {
			v.Diagnostics = append(v.Diagnostics, Diagnostic{
				Level:    LevelError,
				Message:  fmt.Sprintf("Invalid Type '%s' for Signal '%s'", typeStr, node.RealName),
				Position: typeFields[0].Position,
				File:     v.getFileForField(typeFields[0], node),
			})
		}
	}
}

func isValidType(t string) bool {
	switch t {
	case "uint8", "int8", "uint16", "int16", "uint32", "int32", "uint64", "int64",
		"float32", "float64", "string", "bool", "char8":
		return true
	}
	return false
}

func (v *Validator) checkType(val parser.Value, expectedType string) bool {
	switch expectedType {
	case "int":
		_, ok := val.(*parser.IntValue)
		return ok
	case "float":
		_, ok := val.(*parser.FloatValue)
		return ok
	case "string":
		_, okStr := val.(*parser.StringValue)
		_, okRef := val.(*parser.ReferenceValue)
		return okStr || okRef
	case "bool":
		_, ok := val.(*parser.BoolValue)
		return ok
	case "array":
		_, ok := val.(*parser.ArrayValue)
		return ok
	case "reference":
		_, ok := val.(*parser.ReferenceValue)
		return ok
	case "node":
		return true
	case "any":
		return true
	}
	return true
}

func (v *Validator) getFileForField(f *parser.Field, node *index.ProjectNode) string {
	for _, frag := range node.Fragments {
		for _, def := range frag.Definitions {
			if def == f {
				return frag.File
			}
		}
	}
	return ""
}

func (v *Validator) CheckUnused() {
	referencedNodes := make(map[*index.ProjectNode]bool)
	for _, ref := range v.Tree.References {
		if ref.Target != nil {
			referencedNodes[ref.Target] = true
		}
	}

	if v.Tree.Root != nil {
		v.checkUnusedRecursive(v.Tree.Root, referencedNodes)
	}
	for _, node := range v.Tree.IsolatedFiles {
		v.checkUnusedRecursive(node, referencedNodes)
	}
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
		if signalsNode, ok := node.Children["Signals"]; ok {
			for _, signal := range signalsNode.Children {
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

func isSignal(node *index.ProjectNode) bool {
	if node.Parent != nil && node.Parent.Name == "Signals" {
		if isDataSource(node.Parent.Parent) {
			return true
		}
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