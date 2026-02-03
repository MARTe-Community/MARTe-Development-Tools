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
	Overrides   map[string]parser.Value
}

func NewValidator(tree *index.ProjectTree, projectRoot string, overrides map[string]string) *Validator {
	v := &Validator{
		Tree:      tree,
		Schema:    schema.LoadFullSchema(projectRoot),
		Overrides: make(map[string]parser.Value),
	}

	for name, valStr := range overrides {
		p := parser.NewParser("Temp = " + valStr)
		cfg, _ := p.Parse()
		if len(cfg.Definitions) > 0 {
			if f, ok := cfg.Definitions[0].(*parser.Field); ok {
				v.Overrides[name] = f.Value
			}
		}
	}

	return v
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
	v.CheckUnused()
	v.CheckDataSourceThreading()
	v.CheckINOUTOrdering()
	v.CheckSignalConsistency()
	v.CheckVariables()
	v.CheckUnresolvedVariables()
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

	// NEW: Strict Field Validation
	for name, defs := range fields {
		for _, f := range defs {
			if name == "Class" {
				v.validateClassField(f, node)
			} else if name == "Type" {
				v.validateTypeField(f, node)
			} else {
				v.validateGenericField(f, node)
			}
		}
	}

	// 2. Check for mandatory Class if it's an object node (+/$)
	className := ""
	if node.RealName != "" && (node.RealName[0] == '+' || node.RealName[0] == '$') {
		if classFields, ok := fields["Class"]; ok && len(classFields) > 0 {
			className = v.getFieldValue(classFields[0], node)
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

func (v *Validator) validateClassField(f *parser.Field, node *index.ProjectNode) {
	// Class field should always have a value Class (string quoted or not)
	var className string
	switch val := f.Value.(type) {
	case *parser.StringValue:
		className = val.Value
	case *parser.ReferenceValue:
		// Treated as string literal for Class field
		className = val.Value
	default:
		v.Diagnostics = append(v.Diagnostics, Diagnostic{
			Level:    LevelError,
			Message:  fmt.Sprintf("Class field must be a string (quoted or identifier), got %T", f.Value),
			Position: f.Position,
			File:     v.getFileForField(f, node),
		})
		return
	}

	// Check if class exists in schema
	if v.Schema != nil && className != "" {
		// Use cue LookupPath to check existence in #Classes
		path := cue.ParsePath(fmt.Sprintf("#Classes.%s", className))
		if v.Schema.Value.LookupPath(path).Err() != nil {
			// Unknown Class
			if !v.isSuppressed("unknown_class", node) {
				v.Diagnostics = append(v.Diagnostics, Diagnostic{
					Level:    LevelWarning,
					Message:  fmt.Sprintf("Unknown Class '%s'", className),
					Position: f.Position,
					File:     v.getFileForField(f, node),
				})
			}
		}
	}
}

func (v *Validator) validateTypeField(f *parser.Field, node *index.ProjectNode) {
	// Type field should always have a type as value (uint etc)
	var typeName string
	switch val := f.Value.(type) {
	case *parser.StringValue:
		typeName = val.Value
	case *parser.ReferenceValue:
		typeName = val.Value
	default:
		v.Diagnostics = append(v.Diagnostics, Diagnostic{
			Level:    LevelError,
			Message:  fmt.Sprintf("Type field must be a valid type string, got %T", f.Value),
			Position: f.Position,
			File:     v.getFileForField(f, node),
		})
		return
	}

	if !isValidType(typeName) {
		v.Diagnostics = append(v.Diagnostics, Diagnostic{
			Level:    LevelError,
			Message:  fmt.Sprintf("Invalid Type '%s'", typeName),
			Position: f.Position,
			File:     v.getFileForField(f, node),
		})
	}
}

func (v *Validator) validateGenericField(f *parser.Field, node *index.ProjectNode) {
	switch val := f.Value.(type) {
	case *parser.ReferenceValue:
		// Non-quoted string: a reference -> Must resolve
		// Unless it is a special field?
		// "if a non quoted string: a reference (an error should be produced for non valid references)"
		target := v.resolveReference(val.Value, node, nil)
		if target == nil {
			v.Diagnostics = append(v.Diagnostics, Diagnostic{
				Level:    LevelError,
				Message:  fmt.Sprintf("Unknown reference '%s'", val.Value),
				Position: val.Position,
				File:     v.getFileForField(f, node),
			})
		} else {
			// Link reference
			v.updateReferenceTarget(v.getFileForField(f, node), val.Position, target)
		}
	case *parser.StringValue:
		if !val.Quoted {
			// Should be handled as ReferenceValue by Parser for identifiers.
			// But if parser produces StringValue(Quoted=false), treat as ref?
			// Current parser produces ReferenceValue for identifiers.
			// So StringValue is likely Quoted=true.
		}
	case *parser.BinaryExpression:
		// Evaluate if possible
		// valueToInterface does evaluation.
		// If it relies on unresolved vars, it returns nil.
		res := v.valueToInterface(val, node)
		if res == nil {
			// Could warn if expression is malformed?
			// But nil is valid if vars are missing (already checked by checkVariables?)
		}
	}
}

func (v *Validator) isSuppressed(warningType string, node *index.ProjectNode) bool {
	file := v.getNodeFile(node)
	if v.isGloballyAllowed(warningType, file) {
		return true
	}
	// Check local pragmas on the node
	prefix1 := fmt.Sprintf("allow(%s)", warningType)
	prefix2 := fmt.Sprintf("ignore(%s)", warningType)
	for _, p := range node.Pragmas {
		normalized := strings.ReplaceAll(p, " ", "")
		if strings.HasPrefix(normalized, prefix1) || strings.HasPrefix(normalized, prefix2) {
			return true
		}
	}
	return false
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
			m[name] = v.valueToInterface(defs[len(defs)-1].Value, node)
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

func (v *Validator) valueToInterface(val parser.Value, ctx *index.ProjectNode) interface{} {
	switch t := val.(type) {
	case *parser.StringValue:
		return t.Value
	case *parser.IntValue:
		i, _ := strconv.ParseInt(t.Raw, 0, 64)
		return i
	case *parser.FloatValue:
		f, _ := strconv.ParseFloat(t.Raw, 64)
		return f
	case *parser.BoolValue:
		return t.Value
	case *parser.ReferenceValue:
		return t.Value
	case *parser.VariableReferenceValue:
		name := strings.TrimLeft(t.Name, "@$")
		if ov, ok := v.Overrides[name]; ok {
			return v.valueToInterface(ov, ctx)
		}
		if info := v.Tree.ResolveVariable(ctx, name); info != nil {
			if info.Def.DefaultValue != nil {
				return v.valueToInterface(info.Def.DefaultValue, ctx)
			}
		}
		return nil
	case *parser.ArrayValue:
		var arr []interface{}
		for _, e := range t.Elements {
			arr = append(arr, v.valueToInterface(e, ctx))
		}
		return arr
	case *parser.BinaryExpression:
		left := v.valueToInterface(t.Left, ctx)
		right := v.valueToInterface(t.Right, ctx)
		return v.evaluateBinary(left, t.Operator.Type, right)
	case *parser.UnaryExpression:
		val := v.valueToInterface(t.Right, ctx)
		return v.evaluateUnary(t.Operator.Type, val)
	}
	return nil
}

func (v *Validator) evaluateBinary(left interface{}, op parser.TokenType, right interface{}) interface{} {
	if left == nil || right == nil {
		return nil
	}

	if op == parser.TokenConcat {
		return fmt.Sprintf("%v%v", left, right)
	}

	toInt := func(val interface{}) (int64, bool) {
		switch v := val.(type) {
		case int64:
			return v, true
		case int:
			return int64(v), true
		}
		return 0, false
	}

	toFloat := func(val interface{}) (float64, bool) {
		switch v := val.(type) {
		case float64:
			return v, true
		case int64:
			return float64(v), true
		case int:
			return float64(v), true
		}
		return 0, false
	}

	if l, ok := toInt(left); ok {
		if r, ok := toInt(right); ok {
			switch op {
			case parser.TokenPlus:
				return l + r
			case parser.TokenMinus:
				return l - r
			case parser.TokenStar:
				return l * r
			case parser.TokenSlash:
				if r != 0 {
					return l / r
				}
			case parser.TokenPercent:
				if r != 0 {
					return l % r
				}
			}
		}
	}

	if l, ok := toFloat(left); ok {
		if r, ok := toFloat(right); ok {
			switch op {
			case parser.TokenPlus:
				return l + r
			case parser.TokenMinus:
				return l - r
			case parser.TokenStar:
				return l * r
			case parser.TokenSlash:
				if r != 0 {
					return l / r
				}
			}
		}
	}

	return nil
}

func (v *Validator) evaluateUnary(op parser.TokenType, val interface{}) interface{} {
	if val == nil {
		return nil
	}

	switch op {
	case parser.TokenMinus:
		switch v := val.(type) {
		case int64:
			return -v
		case float64:
			return -v
		}
	case parser.TokenSymbol: // ! is Symbol?
		// Parser uses TokenSymbol for ! ?
		// Lexer: '!' -> Symbol.
		if b, ok := val.(bool); ok {
			return !b
		}
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
		dsName = v.getFieldValue(dsFields[0], signalNode)
	}

	if dsName == "" {
		return // Ignore implicit signals or missing datasource (handled elsewhere if mandatory)
	}

	dsNode := v.resolveReference(dsName, signalNode, isDataSource)
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
		// path: #Classes.ClassName.#meta.direction
		path := cue.ParsePath(fmt.Sprintf("#Classes.%s.#meta.direction", dsClass))
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
		targetSignalName = v.getFieldValue(aliasFields[0], signalNode) // Alias is usually the name in DataSource
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
			typeVal := v.getFieldValue(typeFields[0], signalNode)
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
			typeVal := v.getFieldValue(typeFields[0], signalNode)
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

	// Validate Value initialization
	if valField, hasValue := fields["Value"]; hasValue && len(valField) > 0 {
		var typeStr string
		if typeFields, ok := fields["Type"]; ok && len(typeFields) > 0 {
			typeStr = v.getFieldValue(typeFields[0], signalNode)
		} else if signalNode.Target != nil {
			if t, ok := signalNode.Target.Metadata["Type"]; ok {
				typeStr = t
			}
		}

		if typeStr != "" && v.Schema != nil {
			ctx := v.Schema.Context
			typeVal := ctx.CompileString(typeStr)
			if typeVal.Err() == nil {
				valInterface := v.valueToInterface(valField[0].Value, signalNode)
				valVal := ctx.Encode(valInterface)
				res := typeVal.Unify(valVal)
				if err := res.Validate(cue.Concrete(true)); err != nil {
					v.Diagnostics = append(v.Diagnostics, Diagnostic{
						Level:    LevelError,
						Message:  fmt.Sprintf("Value initialization mismatch for signal '%s': %v", signalNode.RealName, err),
						Position: valField[0].Position,
						File:     v.getNodeFile(signalNode),
					})
				}
			}
		}
	}
}

func (v *Validator) getEvaluatedMetadata(node *index.ProjectNode, key string) string {
	for _, frag := range node.Fragments {
		for _, def := range frag.Definitions {
			if f, ok := def.(*parser.Field); ok && f.Name == key {
				return v.getFieldValue(f, node)
			}
		}
	}
	return node.Metadata[key]
}

func (v *Validator) checkSignalProperty(gamSig, dsSig *index.ProjectNode, prop string) {
	gamVal := v.getEvaluatedMetadata(gamSig, prop)
	dsVal := v.getEvaluatedMetadata(dsSig, prop)

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

func (v *Validator) getFieldValue(f *parser.Field, ctx *index.ProjectNode) string {
	res := v.valueToInterface(f.Value, ctx)
	if res == nil {
		return ""
	}
	return fmt.Sprintf("%v", res)
}

func (v *Validator) resolveReference(name string, ctx *index.ProjectNode, predicate func(*index.ProjectNode) bool) *index.ProjectNode {
	return v.Tree.ResolveName(ctx, name, predicate)
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
					target := v.resolveReference(ref.Value, node, isGAM)
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

func (v *Validator) CheckDataSourceThreading() {
	if v.Tree.Root == nil {
		return
	}

	var appNodes []*index.ProjectNode
	findApp := func(n *index.ProjectNode) {
		if cls, ok := n.Metadata["Class"]; ok && cls == "RealTimeApplication" {
			appNodes = append(appNodes, n)
		}
	}
	v.Tree.Walk(findApp)

	for _, appNode := range appNodes {
		v.checkAppDataSourceThreading(appNode)
	}
}

func (v *Validator) checkAppDataSourceThreading(appNode *index.ProjectNode) {
	// 2. Find States
	var statesNode *index.ProjectNode
	if s, ok := appNode.Children["States"]; ok {
		statesNode = s
	} else {
		for _, child := range appNode.Children {
			if cls, ok := child.Metadata["Class"]; ok && cls == "StateMachine" {
				statesNode = child
				break
			}
		}
	}

	if statesNode == nil {
		return
	}

	// 3. Iterate States
	for _, state := range statesNode.Children {
		dsUsage := make(map[*index.ProjectNode]string) // DS Node -> Thread Name
		var threads []*index.ProjectNode

		// Search for threads in the state (either direct children or inside "Threads" container)
		for _, child := range state.Children {
			if child.RealName == "Threads" {
				for _, t := range child.Children {
					if cls, ok := t.Metadata["Class"]; ok && cls == "RealTimeThread" {
						threads = append(threads, t)
					}
				}
			} else {
				if cls, ok := child.Metadata["Class"]; ok && cls == "RealTimeThread" {
					threads = append(threads, child)
				}
			}
		}

		for _, thread := range threads {
			gams := v.getThreadGAMs(thread)
			for _, gam := range gams {
				dss := v.getGAMDataSources(gam)
				for _, ds := range dss {
					if existingThread, ok := dsUsage[ds]; ok {
						if existingThread != thread.RealName {
							if !v.isMultithreaded(ds) {
								v.Diagnostics = append(v.Diagnostics, Diagnostic{
									Level:    LevelError,
									Message:  fmt.Sprintf("DataSource '%s' is not multithreaded but used in multiple threads (%s, %s) in state '%s'", ds.RealName, existingThread, thread.RealName, state.RealName),
									Position: v.getNodePosition(gam),
									File:     v.getNodeFile(gam),
								})
							}
						}
					} else {
						dsUsage[ds] = thread.RealName
					}
				}
			}
		}
	}
}

func (v *Validator) getThreadGAMs(thread *index.ProjectNode) []*index.ProjectNode {
	var gams []*index.ProjectNode
	fields := v.getFields(thread)
	if funcs, ok := fields["Functions"]; ok && len(funcs) > 0 {
		f := funcs[0]
		if arr, ok := f.Value.(*parser.ArrayValue); ok {
			for _, elem := range arr.Elements {
				if ref, ok := elem.(*parser.ReferenceValue); ok {
					target := v.resolveReference(ref.Value, thread, isGAM)
					if target != nil {
						gams = append(gams, target)
					}
				}
			}
		}
	}
	return gams
}

func (v *Validator) getGAMDataSources(gam *index.ProjectNode) []*index.ProjectNode {
	dsMap := make(map[*index.ProjectNode]bool)

	processSignals := func(container *index.ProjectNode) {
		if container == nil {
			return
		}
		for _, sig := range container.Children {
			fields := v.getFields(sig)
			if dsFields, ok := fields["DataSource"]; ok && len(dsFields) > 0 {
				dsName := v.getFieldValue(dsFields[0], sig)
				dsNode := v.resolveReference(dsName, sig, isDataSource)
				if dsNode != nil {
					dsMap[dsNode] = true
				}
			}
		}
	}

	processSignals(gam.Children["InputSignals"])
	processSignals(gam.Children["OutputSignals"])

	var dss []*index.ProjectNode
	for ds := range dsMap {
		dss = append(dss, ds)
	}
	return dss
}

func (v *Validator) isMultithreaded(ds *index.ProjectNode) bool {
	if meta, ok := ds.Children["#meta"]; ok {
		fields := v.getFields(meta)
		if mt, ok := fields["multithreaded"]; ok && len(mt) > 0 {
			val := v.getFieldValue(mt[0], meta)
			return val == "true"
		}
	}
	return false
}

func (v *Validator) CheckINOUTOrdering() {
	if v.Tree.Root == nil {
		return
	}

	var appNodes []*index.ProjectNode
	findApp := func(n *index.ProjectNode) {
		if cls, ok := n.Metadata["Class"]; ok && cls == "RealTimeApplication" {
			appNodes = append(appNodes, n)
		}
	}
	v.Tree.Walk(findApp)

	for _, appNode := range appNodes {
		v.checkAppINOUTOrdering(appNode)
	}
}

func (v *Validator) checkAppINOUTOrdering(appNode *index.ProjectNode) {
	var statesNode *index.ProjectNode
	if s, ok := appNode.Children["States"]; ok {
		statesNode = s
	} else {
		for _, child := range appNode.Children {
			if cls, ok := child.Metadata["Class"]; ok && cls == "StateMachine" {
				statesNode = child
				break
			}
		}
	}

	if statesNode == nil {
		return
	}

	suppress := v.isGloballyAllowed("not_consumed", v.getNodeFile(appNode))
	for _, state := range statesNode.Children {
		var threads []*index.ProjectNode
		for _, child := range state.Children {
			if child.RealName == "Threads" {
				for _, t := range child.Children {
					if cls, ok := t.Metadata["Class"]; ok && cls == "RealTimeThread" {
						threads = append(threads, t)
					}
				}
			} else {
				if cls, ok := child.Metadata["Class"]; ok && cls == "RealTimeThread" {
					threads = append(threads, child)
				}
			}
		}

		for _, thread := range threads {
			producedSignals := make(map[*index.ProjectNode]map[string][]*index.ProjectNode)
			consumedSignals := make(map[*index.ProjectNode]map[string]bool)

			gams := v.getThreadGAMs(thread)
			for _, gam := range gams {
				v.processGAMSignalsForOrdering(gam, "InputSignals", producedSignals, consumedSignals, true, thread, state)
				v.processGAMSignalsForOrdering(gam, "OutputSignals", producedSignals, consumedSignals, false, thread, state)
			}
			if !suppress {
				// Check for produced but not consumed
				for ds, signals := range producedSignals {
					for sigName, producers := range signals {
						consumed := false
						if cSet, ok := consumedSignals[ds]; ok {
							if cSet[sigName] {
								consumed = true
							}
						}
						if !consumed {
							for _, prod := range producers {
								locally_suppressed := false
								for _, p := range prod.Pragmas {
									if strings.HasPrefix(p, "not_consumed:") || strings.HasPrefix(p, "ignore(not_consumed)") {
										locally_suppressed = true
										break
									}
								}
								if !locally_suppressed {
									v.Diagnostics = append(v.Diagnostics, Diagnostic{
										Level:    LevelWarning,
										Message:  fmt.Sprintf("INOUT Signal '%s' (DS '%s') is produced in thread '%s' but never consumed in the same thread.", sigName, ds.RealName, thread.RealName),
										Position: v.getNodePosition(prod),
										File:     v.getNodeFile(prod),
									})
								}
							}
						}
					}
				}
			}
		}
	}
}

func (v *Validator) processGAMSignalsForOrdering(gam *index.ProjectNode, containerName string, produced map[*index.ProjectNode]map[string][]*index.ProjectNode, consumed map[*index.ProjectNode]map[string]bool, isInput bool, thread, state *index.ProjectNode) {
	container := gam.Children[containerName]
	if container == nil {
		return
	}
	not_produced_suppress := v.isGloballyAllowed("not_produced", v.getNodeFile(gam))
	for _, sig := range container.Children {
		fields := v.getFields(sig)
		var dsNode *index.ProjectNode
		var sigName string

		if sig.Target != nil {
			if sig.Target.Parent != nil && sig.Target.Parent.Parent != nil {
				dsNode = sig.Target.Parent.Parent
				sigName = sig.Target.RealName
			}
		}

		if dsNode == nil {
			if dsFields, ok := fields["DataSource"]; ok && len(dsFields) > 0 {
				dsName := v.getFieldValue(dsFields[0], sig)
				dsNode = v.resolveReference(dsName, sig, isDataSource)
			}
			if aliasFields, ok := fields["Alias"]; ok && len(aliasFields) > 0 {
				sigName = v.getFieldValue(aliasFields[0], sig)
			} else {
				sigName = sig.RealName
			}
		}

		if dsNode == nil || sigName == "" {
			continue
		}

		sigName = index.NormalizeName(sigName)

		if v.isMultithreaded(dsNode) {
			continue
		}

		dir := v.getDataSourceDirection(dsNode)
		if dir != "INOUT" {
			continue
		}

		if isInput {
			// Check if signal has 'Value' field - treat as produced/initialized
			if _, hasValue := fields["Value"]; hasValue {
				if produced[dsNode] == nil {
					produced[dsNode] = make(map[string][]*index.ProjectNode)
				}
				produced[dsNode][sigName] = append(produced[dsNode][sigName], sig)
			}

			if !not_produced_suppress {
				isProduced := false
				if set, ok := produced[dsNode]; ok {
					if len(set[sigName]) > 0 {
						isProduced = true
					}
				}
				locally_suppressed := false
				for _, p := range sig.Pragmas {
					if strings.HasPrefix(p, "not_produced:") || strings.HasPrefix(p, "ignore(not_produced)") {
						locally_suppressed = true
						break
					}
				}

				if !isProduced && !locally_suppressed {
					v.Diagnostics = append(v.Diagnostics, Diagnostic{
						Level:    LevelError,
						Message:  fmt.Sprintf("INOUT Signal '%s' (DS '%s') is consumed by GAM '%s' in thread '%s' (State '%s') before being produced by any previous GAM.", sigName, dsNode.RealName, gam.RealName, thread.RealName, state.RealName),
						Position: v.getNodePosition(sig),
						File:     v.getNodeFile(sig),
					})
				}

			}
			if consumed[dsNode] == nil {
				consumed[dsNode] = make(map[string]bool)
			}
			consumed[dsNode][sigName] = true
		} else {
			if produced[dsNode] == nil {
				produced[dsNode] = make(map[string][]*index.ProjectNode)
			}
			produced[dsNode][sigName] = append(produced[dsNode][sigName], sig)
		}
	}
}

func (v *Validator) getDataSourceDirection(ds *index.ProjectNode) string {
	cls := v.getNodeClass(ds)
	if cls == "" {
		return ""
	}
	if v.Schema == nil {
		return ""
	}
	path := cue.ParsePath(fmt.Sprintf("#Classes.%s.#meta.direction", cls))
	val := v.Schema.Value.LookupPath(path)
	if val.Err() == nil {
		s, _ := val.String()
		return s
	}
	return ""
}

func (v *Validator) CheckSignalConsistency() {
	// Map: DataSourceNode -> SignalName -> List of Signals
	signals := make(map[*index.ProjectNode]map[string][]*index.ProjectNode)

	// Helper to collect signals
	collect := func(node *index.ProjectNode) {
		if !isGAM(node) {
			return
		}
		// Check Input and Output
		for _, dir := range []string{"InputSignals", "OutputSignals"} {
			if container, ok := node.Children[dir]; ok {
				for _, sig := range container.Children {
					fields := v.getFields(sig)
					var dsNode *index.ProjectNode
					var sigName string

					// Resolve DS
					if dsFields, ok := fields["DataSource"]; ok && len(dsFields) > 0 {
						dsName := v.getFieldValue(dsFields[0], sig)
						if dsName != "" {
							dsNode = v.resolveReference(dsName, sig, isDataSource)
						}
					}

					// Resolve Name (Alias or RealName)
					if aliasFields, ok := fields["Alias"]; ok && len(aliasFields) > 0 {
						sigName = v.getFieldValue(aliasFields[0], sig)
					} else {
						sigName = sig.RealName
					}

					if dsNode != nil && sigName != "" {
						sigName = index.NormalizeName(sigName)
						if signals[dsNode] == nil {
							signals[dsNode] = make(map[string][]*index.ProjectNode)
						}
						signals[dsNode][sigName] = append(signals[dsNode][sigName], sig)
					}
				}
			}
		}
	}

	v.Tree.Walk(collect)

	// Check Consistency
	for ds, sigMap := range signals {
		for sigName, usages := range sigMap {
			if len(usages) <= 1 {
				continue
			}

			// Check Type consistency
			var firstType string
			var firstNode *index.ProjectNode

			for _, u := range usages {
				// Get Type
				typeVal := ""
				fields := v.getFields(u)
				if typeFields, ok := fields["Type"]; ok && len(typeFields) > 0 {
					typeVal = v.getFieldValue(typeFields[0], u)
				}

				if typeVal == "" {
					continue
				}

				if firstNode == nil {
					firstType = typeVal
					firstNode = u
				} else {
					if typeVal != firstType {
						v.Diagnostics = append(v.Diagnostics, Diagnostic{
							Level:   LevelError,
							Message: fmt.Sprintf("Signal Type Mismatch: Signal '%s' (in DS '%s') is defined as '%s' in '%s' but as '%s' in '%s'", sigName, ds.RealName, firstType, firstNode.Parent.Parent.RealName, typeVal, u.Parent.Parent.RealName),
							Position: v.getNodePosition(u),
							File:     v.getNodeFile(u),
						})
					}
				}
			}
		}
	}
}

func (v *Validator) CheckVariables() {
	if v.Schema == nil {
		return
	}
	ctx := v.Schema.Context

	checkNodeVars := func(node *index.ProjectNode) {
		seen := make(map[string]parser.Position)
		for _, frag := range node.Fragments {
			for _, def := range frag.Definitions {
				if vdef, ok := def.(*parser.VariableDefinition); ok {
					if prevPos, exists := seen[vdef.Name]; exists {
						v.Diagnostics = append(v.Diagnostics, Diagnostic{
							Level:    LevelError,
							Message:  fmt.Sprintf("Duplicate variable definition: '%s' was already defined at %d:%d", vdef.Name, prevPos.Line, prevPos.Column),
							Position: vdef.Position,
							File:     frag.File,
						})
					}
					seen[vdef.Name] = vdef.Position

					if vdef.IsConst && vdef.DefaultValue == nil {
						v.Diagnostics = append(v.Diagnostics, Diagnostic{
							Level:    LevelError,
							Message:  fmt.Sprintf("Constant variable '%s' must have an initial value", vdef.Name),
							Position: vdef.Position,
							File:     frag.File,
						})
						continue
					}

					// Compile Type
					typeVal := ctx.CompileString(vdef.TypeExpr)
					if typeVal.Err() != nil {
						v.Diagnostics = append(v.Diagnostics, Diagnostic{
							Level:    LevelError,
							Message:  fmt.Sprintf("Invalid type expression for variable '%s': %v", vdef.Name, typeVal.Err()),
							Position: vdef.Position,
							File:     frag.File,
						})
						continue
					}

					if vdef.DefaultValue != nil {
						valInterface := v.valueToInterface(vdef.DefaultValue, node)
						valVal := ctx.Encode(valInterface)

						// Unify
						res := typeVal.Unify(valVal)
						if err := res.Validate(cue.Concrete(true)); err != nil {
							v.Diagnostics = append(v.Diagnostics, Diagnostic{
								Level:    LevelError,
								Message:  fmt.Sprintf("Variable '%s' value mismatch: %v", vdef.Name, err),
								Position: vdef.Position,
								File:     frag.File,
							})
						}
					}
				}
			}
		}
	}

	v.Tree.Walk(checkNodeVars)
}
func (v *Validator) CheckUnresolvedVariables() {
	for _, ref := range v.Tree.References {
		if ref.IsVariable && ref.TargetVariable == nil {
			v.Diagnostics = append(v.Diagnostics, Diagnostic{
				Level:    LevelError,
				Message:  fmt.Sprintf("Unresolved variable reference: '@%s'", ref.Name),
				Position: ref.Position,
				File:     ref.File,
			})
		}
	}
}
