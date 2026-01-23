package validator

import (
	"fmt"
	"strconv"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/errors"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/schema"
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

	fields := v.getFields(node)

	// 1. Check for duplicate fields (Go logic)
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

		if className == "RealTimeThread" {
			v.checkFunctionsArray(node, fields)
		}
	}

	// 3. CUE Validation
	if className != "" && v.Schema != nil {
		v.validateWithCUE(node, className)
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

func (v *Validator) validateWithCUE(node *index.ProjectNode, className string) {
	// Check if class exists in schema
	classPath := cue.ParsePath(fmt.Sprintf("#Classes.%s", className))
	if v.Schema.Value.LookupPath(classPath).Err() != nil {
		return // Unknown class, skip validation
	}

	// Convert node to map
	data := v.nodeToMap(node)

	// Encode data to CUE
	dataVal := v.Schema.Context.Encode(data)

	// Unify with #Object
	// #Object requires "Class" field, which is present in data.
	objDef := v.Schema.Value.LookupPath(cue.ParsePath("#Object"))

	// Unify
	res := objDef.Unify(dataVal)

	if err := res.Validate(cue.Concrete(true)); err != nil {
		// Report errors

		// Parse CUE error to diagnostic
		v.reportCUEError(err, node)
	}
}

func (v *Validator) reportCUEError(err error, node *index.ProjectNode) {
	list := errors.Errors(err)
	for _, e := range list {
		msg := e.Error()
		v.Diagnostics = append(v.Diagnostics, Diagnostic{
			Level:    LevelError,
			Message:  fmt.Sprintf("Schema Validation Error: %v", msg),
			Position: v.getNodePosition(node),
			File:     v.getNodeFile(node),
		})
	}
}

func (v *Validator) nodeToMap(node *index.ProjectNode) map[string]interface{} {
	m := make(map[string]interface{})
	fields := v.getFields(node)

	for name, defs := range fields {
		if len(defs) > 0 {
			// Use the last definition (duplicates checked elsewhere)
			m[name] = v.valueToInterface(defs[len(defs)-1].Value)
		}
	}

	// Children as nested maps?
	// CUE schema expects nested structs for "node" type fields.
	// But `node.Children` contains ALL children (even those defined as +Child).
	// If schema expects `States: { ... }`, we map children.

	for name, child := range node.Children {
		// normalize name? CUE keys are strings.
		// If child real name is "+States", key in Children is "States".
		// We use "States" as key in map.
		m[name] = v.nodeToMap(child)
	}

	return m
}

func (v *Validator) valueToInterface(val parser.Value) interface{} {
	switch t := val.(type) {
	case *parser.StringValue:
		return t.Value
	case *parser.IntValue:
		i, _ := strconv.ParseInt(t.Raw, 0, 64)
		return i // CUE handles int64
	case *parser.FloatValue:
		f, _ := strconv.ParseFloat(t.Raw, 64)
		return f
	case *parser.BoolValue:
		return t.Value
	case *parser.ReferenceValue:
		return t.Value
	case *parser.ArrayValue:
		var arr []interface{}
		for _, e := range t.Elements {
			arr = append(arr, v.valueToInterface(e))
		}
		return arr
	}
	return nil
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

	// Check Direction using CUE Schema
	dsClass := v.getNodeClass(dsNode)
	if dsClass != "" {
		// Lookup class definition in Schema
		// path: #Classes.ClassName.direction
		path := cue.ParsePath(fmt.Sprintf("#Classes.%s.direction", dsClass))
		val := v.Schema.Value.LookupPath(path)

		if val.Err() == nil {
			dsDir, err := val.String()
			if err == nil && dsDir != "" {
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
		suppressed := v.isGloballyAllowed("implicit", v.getNodeFile(signalNode))
		if !suppressed {
			for _, p := range signalNode.Pragmas {
				if strings.HasPrefix(p, "implicit:") || strings.HasPrefix(p, "ignore(implicit)") {
					suppressed = true
					break
				}
			}
		}

		if !suppressed {
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
		if found := v.Tree.FindNode(isoNode, name, predicate); found != nil {
			return found
		}
		return nil
	}
	if v.Tree.Root == nil {
		return nil
	}
	return v.Tree.FindNode(v.Tree.Root, name, predicate)
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
	// Legacy function, replaced by CUE.
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
			suppress := v.isGloballyAllowed("unused", v.getNodeFile(node))
			if !suppress {
				for _, p := range node.Pragmas {
					if strings.HasPrefix(p, "unused:") || strings.HasPrefix(p, "ignore(unused)") {
						suppress = true
						break
					}
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
					if v.isGloballyAllowed("unused", v.getNodeFile(signal)) {
						continue
					}
					suppress := false
					for _, p := range signal.Pragmas {
						if strings.HasPrefix(p, "unused:") || strings.HasPrefix(p, "ignore(unused)") {
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
	_, hasSignals := node.Children["Signals"]
	return hasSignals
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

func (v *Validator) checkFunctionsArray(node *index.ProjectNode, fields map[string][]*parser.Field) {
	if funcs, ok := fields["Functions"]; ok && len(funcs) > 0 {
		f := funcs[0]
		if arr, ok := f.Value.(*parser.ArrayValue); ok {
			for _, elem := range arr.Elements {
				if ref, ok := elem.(*parser.ReferenceValue); ok {
					target := v.resolveReference(ref.Value, v.getNodeFile(node), isGAM)
					if target == nil {
						v.Diagnostics = append(v.Diagnostics, Diagnostic{
							Level:    LevelError,
							Message:  fmt.Sprintf("Function '%s' not found or is not a valid GAM", ref.Value),
							Position: ref.Position,
							File:     v.getNodeFile(node),
						})
					}
				} else {
					v.Diagnostics = append(v.Diagnostics, Diagnostic{
						Level:    LevelError,
						Message:  "Functions array must contain references",
						Position: f.Position,
						File:     v.getNodeFile(node),
					})
				}
			}
		}
	}
}

func (v *Validator) isGloballyAllowed(warningType string, contextFile string) bool {
	prefix1 := fmt.Sprintf("allow(%s)", warningType)
	prefix2 := fmt.Sprintf("ignore(%s)", warningType)

	// If context file is isolated, only check its own pragmas
	if _, isIsolated := v.Tree.IsolatedFiles[contextFile]; isIsolated {
		if pragmas, ok := v.Tree.GlobalPragmas[contextFile]; ok {
			for _, p := range pragmas {
				normalized := strings.ReplaceAll(p, " ", "")
				if strings.HasPrefix(normalized, prefix1) || strings.HasPrefix(normalized, prefix2) {
					return true
				}
			}
		}
		return false
	}

	// If project file, check all non-isolated files
	for file, pragmas := range v.Tree.GlobalPragmas {
		if _, isIsolated := v.Tree.IsolatedFiles[file]; isIsolated {
			continue
		}
		for _, p := range pragmas {
			normalized := strings.ReplaceAll(p, " ", "")
			if strings.HasPrefix(normalized, prefix1) || strings.HasPrefix(normalized, prefix2) {
				return true
			}
		}
	}
	return false
}
