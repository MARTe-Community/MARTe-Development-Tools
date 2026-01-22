package validator

import (
	"fmt"
	"strings"

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
	// Ensure references are resolved (if not already done by builder/lsp)
	v.Tree.ResolveReferences()

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
	fields := v.getFields(node)
	fieldOrder := []string{}
	for _, frag := range node.Fragments {
		for _, def := range frag.Definitions {
			if f, ok := def.(*parser.Field); ok {
				if _, exists := fields[f.Name]; exists { // already collected
					// Maintain order logic if needed, but getFields collects all.
					// For strict order check we might need this loop.
					// Let's assume getFields is enough for validation logic,
					// but for "duplicate check" and "class validation" we iterate fields map.
					// We need to construct fieldOrder.
					// Just reuse loop for fieldOrder
				}
			}
		}
	}
	// Re-construct fieldOrder for order validation
	seen := make(map[string]bool)
	for _, frag := range node.Fragments {
		for _, def := range frag.Definitions {
			if f, ok := def.(*parser.Field); ok {
				if !seen[f.Name] {
					fieldOrder = append(fieldOrder, f.Name)
					seen[f.Name] = true
				}
			}
		}
	}

	// 1. Check for duplicate fields
	for name, defs := range fields {
		if len(defs) > 1 {
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
			className = v.getFieldValue(classFields[0])
		}

		hasType := false
		if _, ok := fields["Type"]; ok {
			hasType = true
		}

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

	// 5. GAM Validation (Signal references)
	if isGAM(node) {
		v.validateGAM(node)
	}

	// Recursively validate children
	for _, child := range node.Children {
		v.validateNode(child)
	}
}

func (v *Validator) validateClass(node *index.ProjectNode, classDef schema.ClassDefinition, fields map[string][]*parser.Field, fieldOrder []string) {
	// ... (same as before)
	for _, fieldDef := range classDef.Fields {
		if fieldDef.Mandatory {
			found := false
			if _, ok := fields[fieldDef.Name]; ok {
				found = true
			} else if fieldDef.Type == "node" {
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

	for _, fieldDef := range classDef.Fields {
		if fList, ok := fields[fieldDef.Name]; ok {
			f := fList[0]
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
			}
		}
	}
}

func (v *Validator) validateSignal(node *index.ProjectNode, fields map[string][]*parser.Field) {
	// ... (same as before)
	if typeFields, ok := fields["Type"]; !ok || len(typeFields) == 0 {
		v.Diagnostics = append(v.Diagnostics, Diagnostic{
			Level:    LevelError,
			Message:  fmt.Sprintf("Signal '%s' is missing mandatory field 'Type'", node.RealName),
			Position: v.getNodePosition(node),
			File:     v.getNodeFile(node),
		})
	} else {
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

func (v *Validator) validateGAM(node *index.ProjectNode) {
	if inputs, ok := node.Children["InputSignals"]; ok {
		v.validateGAMSignals(node, inputs, "Input")
	}
	if outputs, ok := node.Children["OutputSignals"]; ok {
		v.validateGAMSignals(node, outputs, "Output")
	}
}

func (v *Validator) validateGAMSignals(gamNode, signalsContainer *index.ProjectNode, direction string) {
	for _, signal := range signalsContainer.Children {
		v.validateGAMSignal(gamNode, signal, direction)
	}
}

func (v *Validator) validateGAMSignal(gamNode, signalNode *index.ProjectNode, direction string) {
	fields := v.getFields(signalNode)
	var dsName string
	if dsFields, ok := fields["DataSource"]; ok && len(dsFields) > 0 {
		dsName = v.getFieldValue(dsFields[0])
	}

	if dsName == "" {
		return // Ignore implicit signals or missing datasource (handled elsewhere if mandatory)
	}

	dsNode := v.resolveReference(dsName, v.getNodeFile(signalNode), isDataSource)
	if dsNode == nil {
		v.Diagnostics = append(v.Diagnostics, Diagnostic{
			Level:    LevelError,
			Message:  fmt.Sprintf("Unknown DataSource '%s' referenced in signal '%s'", dsName, signalNode.RealName),
			Position: v.getNodePosition(signalNode),
			File:     v.getNodeFile(signalNode),
		})
		return
	}

	// Link DataSource reference
	if dsFields, ok := fields["DataSource"]; ok && len(dsFields) > 0 {
		if val, ok := dsFields[0].Value.(*parser.ReferenceValue); ok {
			v.updateReferenceTarget(v.getNodeFile(signalNode), val.Position, dsNode)
		}
	}

	// Check Direction
	dsClass := v.getNodeClass(dsNode)
	if dsClass != "" {
		if classDef, ok := v.Schema.Classes[dsClass]; ok {
			dsDir := classDef.Direction
			if dsDir != "" {
				if direction == "Input" && dsDir == "OUT" {
					v.Diagnostics = append(v.Diagnostics, Diagnostic{
						Level:    LevelError,
						Message:  fmt.Sprintf("DataSource '%s' (Class %s) is Output-only but referenced in InputSignals of GAM '%s'", dsName, dsClass, gamNode.RealName),
						Position: v.getNodePosition(signalNode),
						File:     v.getNodeFile(signalNode),
					})
				}
				if direction == "Output" && dsDir == "IN" {
					v.Diagnostics = append(v.Diagnostics, Diagnostic{
						Level:    LevelError,
						Message:  fmt.Sprintf("DataSource '%s' (Class %s) is Input-only but referenced in OutputSignals of GAM '%s'", dsName, dsClass, gamNode.RealName),
						Position: v.getNodePosition(signalNode),
						File:     v.getNodeFile(signalNode),
					})
				}
			}
		}
	}

	// Check Signal Existence
	targetSignalName := index.NormalizeName(signalNode.RealName)
	if aliasFields, ok := fields["Alias"]; ok && len(aliasFields) > 0 {
		targetSignalName = v.getFieldValue(aliasFields[0]) // Alias is usually the name in DataSource
	}

	var targetNode *index.ProjectNode
	if signalsContainer, ok := dsNode.Children["Signals"]; ok {
		targetNorm := index.NormalizeName(targetSignalName)
		
		if child, ok := signalsContainer.Children[targetNorm]; ok {
			targetNode = child
		} else {
			// Fallback check
			for _, child := range signalsContainer.Children {
				if index.NormalizeName(child.RealName) == targetNorm {
					targetNode = child
					break
				}
			}
		}
	}

	if targetNode == nil {
		suppress := false
		for _, p := range signalNode.Pragmas {
			if strings.HasPrefix(p, "implicit:") {
				suppress = true
				break
			}
		}

		if !suppress {
			v.Diagnostics = append(v.Diagnostics, Diagnostic{
				Level:    LevelWarning,
				Message:  fmt.Sprintf("Implicitly Defined Signal: '%s' is defined in GAM '%s' but not in DataSource '%s'", targetSignalName, gamNode.RealName, dsName),
				Position: v.getNodePosition(signalNode),
				File:     v.getNodeFile(signalNode),
			})
		}

		if typeFields, ok := fields["Type"]; !ok || len(typeFields) == 0 {
			v.Diagnostics = append(v.Diagnostics, Diagnostic{
				Level:    LevelError,
				Message:  fmt.Sprintf("Implicit signal '%s' must define Type", targetSignalName),
				Position: v.getNodePosition(signalNode),
				File:     v.getNodeFile(signalNode),
			})
		} else {
			// Check Type validity even for implicit
			typeVal := v.getFieldValue(typeFields[0])
			if !isValidType(typeVal) {
				v.Diagnostics = append(v.Diagnostics, Diagnostic{
					Level:    LevelError,
					Message:  fmt.Sprintf("Invalid Type '%s' for Signal '%s'", typeVal, signalNode.RealName),
					Position: typeFields[0].Position,
					File:     v.getNodeFile(signalNode),
				})
			}
		}
	} else {
		signalNode.Target = targetNode
		// Link Alias reference
		if aliasFields, ok := fields["Alias"]; ok && len(aliasFields) > 0 {
			if val, ok := aliasFields[0].Value.(*parser.ReferenceValue); ok {
				v.updateReferenceTarget(v.getNodeFile(signalNode), val.Position, targetNode)
			}
		}
		
		// Property checks
		v.checkSignalProperty(signalNode, targetNode, "Type")
		v.checkSignalProperty(signalNode, targetNode, "NumberOfElements")
		v.checkSignalProperty(signalNode, targetNode, "NumberOfDimensions")
		
		// Check Type validity if present
		if typeFields, ok := fields["Type"]; ok && len(typeFields) > 0 {
			typeVal := v.getFieldValue(typeFields[0])
			if !isValidType(typeVal) {
				v.Diagnostics = append(v.Diagnostics, Diagnostic{
					Level:    LevelError,
					Message:  fmt.Sprintf("Invalid Type '%s' for Signal '%s'", typeVal, signalNode.RealName),
					Position: typeFields[0].Position,
					File:     v.getNodeFile(signalNode),
				})
			}
		}
	}
}

func (v *Validator) checkSignalProperty(gamSig, dsSig *index.ProjectNode, prop string) {
	gamVal := gamSig.Metadata[prop]
	dsVal := dsSig.Metadata[prop]

	if gamVal == "" {
		return
	}

	if dsVal != "" && gamVal != dsVal {
		if prop == "Type" {
			if v.checkCastPragma(gamSig, dsVal, gamVal) {
				return
			}
		}

		v.Diagnostics = append(v.Diagnostics, Diagnostic{
			Level:    LevelError,
			Message:  fmt.Sprintf("Signal '%s' property '%s' mismatch: defined '%s', referenced '%s'", gamSig.RealName, prop, dsVal, gamVal),
			Position: v.getNodePosition(gamSig),
			File:     v.getNodeFile(gamSig),
		})
	}
}

func (v *Validator) checkCastPragma(node *index.ProjectNode, defType, curType string) bool {
	for _, p := range node.Pragmas {
		if strings.HasPrefix(p, "cast(") {
			content := strings.TrimPrefix(p, "cast(")
			if idx := strings.Index(content, ")"); idx != -1 {
				content = content[:idx]
				parts := strings.Split(content, ",")
				if len(parts) == 2 {
					d := strings.TrimSpace(parts[0])
					c := strings.TrimSpace(parts[1])
					if d == defType && c == curType {
						return true
					}
				}
			}
		}
	}
	return false
}

func (v *Validator) updateReferenceTarget(file string, pos parser.Position, target *index.ProjectNode) {
	for i := range v.Tree.References {
		ref := &v.Tree.References[i]
		if ref.File == file && ref.Position == pos {
			ref.Target = target
			return
		}
	}
}

// Helpers

func (v *Validator) getFields(node *index.ProjectNode) map[string][]*parser.Field {
	fields := make(map[string][]*parser.Field)
	for _, frag := range node.Fragments {
		for _, def := range frag.Definitions {
			if f, ok := def.(*parser.Field); ok {
				fields[f.Name] = append(fields[f.Name], f)
			}
		}
	}
	return fields
}

func (v *Validator) getFieldValue(f *parser.Field) string {
	switch val := f.Value.(type) {
	case *parser.StringValue:
		return val.Value
	case *parser.ReferenceValue:
		return val.Value
	case *parser.IntValue:
		return val.Raw
	case *parser.FloatValue:
		return val.Raw
	}
	return ""
}

func (v *Validator) resolveReference(name string, file string, predicate func(*index.ProjectNode) bool) *index.ProjectNode {
	if isoNode, ok := v.Tree.IsolatedFiles[file]; ok {
		if found := v.findNodeRecursive(isoNode, name, predicate); found != nil {
			return found
		}
		return nil
	}
	if v.Tree.Root == nil {
		return nil
	}
	return v.findNodeRecursive(v.Tree.Root, name, predicate)
}

func (v *Validator) findNodeRecursive(root *index.ProjectNode, name string, predicate func(*index.ProjectNode) bool) *index.ProjectNode {
	// Simple recursive search matching name
	if root.RealName == name || root.Name == index.NormalizeName(name) {
		if predicate == nil || predicate(root) {
			return root
		}
	}
	
	// Recursive
	for _, child := range root.Children {
		if found := v.findNodeRecursive(child, name, predicate); found != nil {
			return found
		}
	}
	return nil
}

func (v *Validator) getNodeClass(node *index.ProjectNode) string {
	if cls, ok := node.Metadata["Class"]; ok {
		return cls
	}
	return ""
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
    // ... (same as before)
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
		v.collectTargetUsage(v.Tree.Root, referencedNodes)
	}
	for _, node := range v.Tree.IsolatedFiles {
		v.collectTargetUsage(node, referencedNodes)
	}

	if v.Tree.Root != nil {
		v.checkUnusedRecursive(v.Tree.Root, referencedNodes)
	}
	for _, node := range v.Tree.IsolatedFiles {
		v.checkUnusedRecursive(node, referencedNodes)
	}
}

func (v *Validator) collectTargetUsage(node *index.ProjectNode, referenced map[*index.ProjectNode]bool) {
	if node.Target != nil {
		referenced[node.Target] = true
	}
	for _, child := range node.Children {
		v.collectTargetUsage(child, referenced)
	}
}

func (v *Validator) checkUnusedRecursive(node *index.ProjectNode, referenced map[*index.ProjectNode]bool) {
	// Heuristic for GAM
	if isGAM(node) {
		if !referenced[node] {
			suppress := false
			for _, p := range node.Pragmas {
				if strings.HasPrefix(p, "unused:") {
					suppress = true
					break
				}
			}
			if !suppress {
				v.Diagnostics = append(v.Diagnostics, Diagnostic{
					Level:    LevelWarning,
					Message:  fmt.Sprintf("Unused GAM: %s is defined but not referenced in any thread or scheduler", node.RealName),
					Position: v.getNodePosition(node),
					File:     v.getNodeFile(node),
				})
			}
		}
	}

	// Heuristic for DataSource and its signals
	if isDataSource(node) {
		if signalsNode, ok := node.Children["Signals"]; ok {
			for _, signal := range signalsNode.Children {
				if !referenced[signal] {
					suppress := false
					for _, p := range signal.Pragmas {
						if strings.HasPrefix(p, "unused:") {
							suppress = true
							break
						}
					}
					if !suppress {
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