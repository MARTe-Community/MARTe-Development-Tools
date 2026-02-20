package validator

import (
	"context"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"sync"

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
	Variables   map[string]parser.Value
	mu          sync.Mutex
}

func NewValidator(tree *index.ProjectTree, projectRoot string, overrides map[string]string) *Validator {
	v := &Validator{
		Tree:      tree,
		Schema:    schema.LoadFullSchema(projectRoot),
		Overrides: make(map[string]parser.Value),
		Variables: make(map[string]parser.Value),
	}

	for name, valStr := range overrides {
		p := parser.NewParser("Temp = " + valStr)
		cfg, _ := p.Parse()
		if cfg != nil && len(cfg.Definitions) > 0 {
			if f, ok := cfg.Definitions[0].(*parser.Field); ok {
				v.Overrides[name] = f.Value
				v.Variables[name] = f.Value
			}
		}
	}

	// Also collect variables from Tree
	tree.Walk(func(n *index.ProjectNode) {
		for k, varInfo := range n.Variables {
			if _, ok := v.Variables[k]; !ok || varInfo.Def.IsConst {
				v.Variables[k] = varInfo.Def.DefaultValue
			}
		}
	})

	return v
}

func (v *Validator) ValidateProject(ctx context.Context) {
	if v.Tree == nil {
		return
	}
	// Ensure references and fields are resolved
	v.Tree.ResolveFields()
	v.Tree.ResolveReferences()

	numWorkers := runtime.NumCPU()
	if numWorkers < 4 {
		numWorkers = 4
	}
	tasks := make(chan *index.ProjectNode, 100)
	var wg sync.WaitGroup

	evalCtx := &index.EvaluationContext{Variables: v.Variables, Tree: v.Tree}

	for i := 0; i < numWorkers; i++ {
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case node, ok := <-tasks:
					if !ok {
						return
					}
					v.validateNode(ctx, node, evalCtx)
					wg.Done()
				}
			}
		}()
	}

	queueTask := func(n *index.ProjectNode) {
		// We need to count tasks to wait for them
		// But validateNode itself recurses and we don't want to deadlock
		// if we queue from inside validateNode.
		// Actually, let's just use the workers for TOP-LEVEL packages/files
		// and let validateNode recurse synchronously within each worker.
		wg.Add(1)
		select {
		case <-ctx.Done():
			wg.Done()
		case tasks <- n:
		}
	}

	if v.Tree.Root != nil {
		for _, child := range v.Tree.Root.Children {
			if ctx.Err() != nil {
				break
			}
			queueTask(child)
		}
	}
	for _, node := range v.Tree.IsolatedFiles {
		if ctx.Err() != nil {
			break
		}
		queueTask(node)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-ctx.Done():
		// Canceled
	case <-done:
		// Finished workers
	}

	close(tasks)

	if ctx.Err() != nil {
		return
	}

	v.CheckUnused(ctx)
	v.CheckDataSourceThreading(ctx)
	v.CheckINOUTOrdering(ctx)
	v.CheckSignalConsistency(ctx)
	v.CheckVariables(ctx)
	v.CheckUnresolvedVariables(ctx)
}

func (v *Validator) validateNode(ctx context.Context, node *index.ProjectNode, evalCtx *index.EvaluationContext) {
	if ctx.Err() != nil {
		return
	}

	// Check for invalid content in Signals container of DataSource
	if node.RealName == "Signals" && node.Parent != nil && isDataSource(node.Parent) {
		for _, frag := range node.Fragments {
			for _, def := range frag.Definitions {
				if f, ok := def.(*parser.Field); ok {
					v.report(node, "invalid_container_content", LevelError,
						fmt.Sprintf("Invalid content in Signals container: Field '%s' is not allowed. Only Signal objects are allowed.", f.Name),
						f.Position, frag.File)
				}
			}
		}
	}

	var evaluated []index.EvaluatedDefinition
	for _, frag := range node.Fragments {
		evaluated = append(evaluated, v.Tree.EvaluateDefinitions(frag.Definitions, evalCtx, frag.File)...)
	}

	fields := v.extractFields(evaluated)
	var objects []index.EvaluatedDefinition
	for _, ed := range evaluated {
		if _, ok := ed.Def.(*parser.ObjectNode); ok {
			objects = append(objects, ed)
		}
	}

	// 1. Check for duplicate fields (evaluated only)
	for name, defs := range fields {
		if len(defs) > 1 {
			v.report(node, "duplicate_field", LevelError,
				fmt.Sprintf("Duplicate Field Definition: '%s' is already defined in %s", name, defs[0].File),
				defs[1].Raw.Position, defs[1].File)
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
			v.report(node, "missing_class", LevelError,
				fmt.Sprintf("Node %s is an object and must contain a 'Class' field (or be a Signal with 'Type')", node.RealName),
				pos, file)
		}

		if className == "RealTimeThread" {
			v.checkFunctionsArray(node, fields)
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

	// 6. DataSource Validation
	if isDataSource(node) {
		v.validateDataSource(node)
	}

	// 3. CUE Validation
	if className != "" && v.Schema != nil {
		v.validateWithCUE(node, className)
	}

	written := make(map[string]bool)
	for _, obj := range objects {
		objectNode := obj.Def.(*parser.ObjectNode)
		objName := v.ValueToString(objectNode.Name, obj.Ctx)
		norm := index.NormalizeName(objName)

		if child, ok := node.Children[norm]; ok {
			if !written[norm] {
				v.validateNode(ctx, child, obj.Ctx)
				written[norm] = true
			}
		} else {
			// Dynamic object
			v.validateDynamicObject(ctx, objectNode, obj.Ctx, obj.File)
		}
	}

	// Recursively validate remaining static children (e.g. packages)
	for name, child := range node.Children {
		if !written[name] {
			v.validateNode(ctx, child, evalCtx)
		}
	}
}

func (v *Validator) validateDynamicObject(ctx context.Context, obj *parser.ObjectNode, evalCtx *index.EvaluationContext, file string) {
	evaluated := v.Tree.EvaluateDefinitions(obj.Subnode.Definitions, evalCtx, file)

	fields := v.extractFields(evaluated)
	var objects []index.EvaluatedDefinition
	for _, ed := range evaluated {
		if _, ok := ed.Def.(*parser.ObjectNode); ok {
			objects = append(objects, ed)
		}
	}

	// Perform basic validation on dynamic object (Class existence, duplicates)
	for name, defs := range fields {
		if len(defs) > 1 {
			v.report(nil, "duplicate_field", LevelError,
				fmt.Sprintf("Duplicate Field Definition in dynamic object: '%s'", name),
				defs[1].Raw.Position, defs[1].File)
		}
	}

	// Recurse into sub-objects
	for _, sub := range objects {
		v.validateDynamicObject(ctx, sub.Def.(*parser.ObjectNode), sub.Ctx, sub.File)
	}
}

func (v *Validator) extractFields(evaluated []index.EvaluatedDefinition) map[string][]index.EvaluatedField {
	fields := make(map[string][]index.EvaluatedField)
	for _, ed := range evaluated {
		if d, ok := ed.Def.(*parser.Field); ok {
			fields[d.Name] = append(fields[d.Name], index.EvaluatedField{
				Raw:   d,
				Value: v.Tree.EvaluateValue(d.Value, ed.Ctx),
				File:  ed.File,
			})
		}
	}
	return fields
}


func (v *Validator) ValueToString(val parser.Value, ctx *index.EvaluationContext) string {
	res := v.Tree.EvaluateValue(val, ctx)
	return v.Tree.ValueToString(res)
}


func (v *Validator) validateClassField(f index.EvaluatedField, node *index.ProjectNode) {
	// Class field should always have a value Class (string quoted or not)
	var className string
	switch val := f.Value.(type) {
	case *parser.StringValue:
		className = val.Value
	case *parser.ReferenceValue:
		// Treated as string literal for Class field
		className = val.Value
	default:
		v.report(node, "invalid_class_field", LevelError,
			fmt.Sprintf("Class field must be a string (quoted or identifier), got %T", f.Value),
			f.Raw.Position, f.File)
		return
	}

	// Check if class exists in schema
	if v.Schema != nil && className != "" {
		// Strip namespace if present (e.g. SDN::SDNSubscriber -> SDNSubscriber)
		lookupName := className
		if idx := strings.LastIndex(className, "::"); idx != -1 {
			lookupName = className[idx+2:]
		}

		// Use cue LookupPath to check existence in #Classes
		path := cue.ParsePath(fmt.Sprintf("#Classes.%s", lookupName))
		if v.Schema.Value.LookupPath(path).Err() != nil {
			// Unknown Class
			v.report(node, "unknown_class", LevelWarning,
				fmt.Sprintf("Unknown Class '%s'", className),
				f.Raw.Position, f.File)
		}
	}
}

func (v *Validator) validateTypeField(f index.EvaluatedField, node *index.ProjectNode) {
	// Type field should always have a type as value (uint etc)
	var typeName string
	switch val := f.Value.(type) {
	case *parser.StringValue:
		typeName = val.Value
	case *parser.ReferenceValue:
		typeName = val.Value
	default:
		v.report(node, "invalid_type_field", LevelError,
			fmt.Sprintf("Type field must be a valid type string, got %T", f.Value),
			f.Raw.Position, f.File)
		return
	}

	if !isValidType(typeName) {
		v.report(node, "invalid_type", LevelError,
			fmt.Sprintf("Invalid Type '%s'", typeName),
			f.Raw.Position, f.File)
	}
}

func (v *Validator) validateGenericField(f index.EvaluatedField, node *index.ProjectNode) {
	v.validateValue(f.Value, node, f.File)
}

func (v *Validator) validateValue(val parser.Value, node *index.ProjectNode, file string) {
	switch t := val.(type) {
	case *parser.ReferenceValue:
		// Non-quoted string: a reference -> Must resolve
		target := v.resolveReference(t.Value, node, nil)
		if target == nil {
			v.report(node, "unknown_reference", LevelError,
				fmt.Sprintf("Unknown reference '%s'", t.Value),
				t.Position, file)
		} else {
			// Link reference
			v.updateReferenceTarget(file, t.Position, target)
		}
	case *parser.StringValue:
		if !t.Quoted {
			// Should be handled as ReferenceValue by Parser for identifiers.
			// But if parser produces StringValue(Quoted=false), treat as ref?
			// Current parser produces ReferenceValue for identifiers.
			// So StringValue is likely Quoted=true.
		}
	case *parser.ArrayValue:
		for _, elem := range t.Elements {
			v.validateValue(elem, node, file)
		}
	case *parser.BinaryExpression:
		v.validateValue(t.Left, node, file)
		v.validateValue(t.Right, node, file)
	case *parser.UnaryExpression:
		v.validateValue(t.Right, node, file)
	}
}

func (v *Validator) isSuppressed(warningType string, node *index.ProjectNode) bool {
	// Global suppression check
	// Use an empty string if node is nil for file context, 
	// but isGloballyAllowed should ideally have a file context.
	file := ""
	if node != nil {
		file = v.getNodeFile(node)
	}
	
	if v.isGloballyAllowed(warningType, file) {
		return true
	}

	// Legacy tag support
	if warningType == "unused_gam" || warningType == "unused_signal" {
		if v.isGloballyAllowed("unused", file) {
			return true
		}
	}
	if warningType == "implicit_signal" {
		if v.isGloballyAllowed("implicit", file) {
			return true
		}
	}
	
	if node == nil {
		return false
	}

	// Check local pragmas on the node
	checkTags := []string{warningType}
	if warningType == "unused_gam" || warningType == "unused_signal" {
		checkTags = append(checkTags, "unused")
	}
	if warningType == "implicit_signal" {
		checkTags = append(checkTags, "implicit")
	}

	for _, tag := range checkTags {
		prefix1 := fmt.Sprintf("allow(%s)", tag)
		prefix2 := fmt.Sprintf("ignore(%s)", tag)
		for _, p := range node.Pragmas {
			normalized := strings.ReplaceAll(p, " ", "")
			if strings.HasPrefix(normalized, prefix1) || strings.HasPrefix(normalized, prefix2) {
				return true
			}
			// Special case for colon-separated pragmas //! unused: ...
			if strings.HasPrefix(p, tag+":") {
				return true
			}
		}
	}
	return false
}

func (v *Validator) report(node *index.ProjectNode, tag string, level DiagnosticLevel, msg string, pos parser.Position, file string) {
	if v.isSuppressed(tag, node) {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.Diagnostics = append(v.Diagnostics, Diagnostic{
		Level:    level,
		Message:  msg,
		Position: pos,
		File:     file,
	})
}

func (v *Validator) validateWithCUE(node *index.ProjectNode, className string) {
	// Strip namespace if present
	lookupName := className
	if idx := strings.LastIndex(className, "::"); idx != -1 {
		lookupName = className[idx+2:]
	}

	// Check if class exists in schema
	classPath := cue.ParsePath(fmt.Sprintf("#Classes.%s", lookupName))
	if v.Schema.Value.LookupPath(classPath).Err() != nil {
		return // Unknown class, skip validation
	}

	// Convert node to map (shallow for validation of this specific object)
	// We use depth 3 to reach Signals and their elements for GAM/DataSource validation.
	data := v.nodeToMapWithDepth(node, 3)

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

	// Check Parent constraints from #meta
	meta := res.LookupPath(cue.ParsePath("#meta"))
	if meta.Exists() {
		v.validateParent(node, meta)
	}
}

func (v *Validator) validateParent(node *index.ProjectNode, meta cue.Value) {
	parentConstraint := meta.LookupPath(cue.ParsePath("Parent"))
	if !parentConstraint.Exists() || parentConstraint.Err() != nil {
		return
	}

	parent := node.Parent
	if parent == nil {
		// Root node has no parent. If there is a constraint, it might be an error.
		// Usually only RealTimeApplication is root.
		return
	}

	// Check Name
	if nameVal := parentConstraint.LookupPath(cue.ParsePath("Name")); nameVal.Exists() {
		expectedName, _ := nameVal.String()
		if parent.RealName != expectedName && parent.Name != expectedName {
			v.report(node, "parent_mismatch", LevelError,
				fmt.Sprintf("Parent Name Mismatch: Node '%s' must have parent named '%s', but has '%s'", node.RealName, expectedName, parent.RealName),
				v.getNodePosition(node), v.getNodeFile(node))
		}
	}

	// Check Class
	if classVal := parentConstraint.LookupPath(cue.ParsePath("Class")); classVal.Exists() {
		expectedClass, _ := classVal.String()
		parentClass := v.getNodeClass(parent)
		if parentClass != expectedClass {
			v.report(node, "parent_mismatch", LevelError,
				fmt.Sprintf("Parent Class Mismatch: Node '%s' must have parent of class '%s', but has '%s'", node.RealName, expectedClass, parentClass),
				v.getNodePosition(node), v.getNodeFile(node))
		}
	}

	// Check MetaType
	if typeVal := parentConstraint.LookupPath(cue.ParsePath("MetaType")); typeVal.Exists() {
		expectedType, _ := typeVal.String()
		parentMeta := v.getMetaType(parent)
		if parentMeta != expectedType {
			v.report(node, "parent_mismatch", LevelError,
				fmt.Sprintf("Parent MetaType Mismatch: Node '%s' must have parent of type '%s', but has '%s'", node.RealName, expectedType, parentMeta),
				v.getNodePosition(node), v.getNodeFile(node))
		}
	}
}

func (v *Validator) getMetaType(node *index.ProjectNode) string {
	className := v.getNodeClass(node)
	if className == "" {
		return ""
	}
	if v.Schema == nil {
		return ""
	}

	path := cue.ParsePath(fmt.Sprintf("#Classes.%s.#meta.MetaType", className))
	val := v.Schema.Value.LookupPath(path)
	if val.Exists() {
		s, _ := val.String()
		return s
	}
	return ""
}


func (v *Validator) reportCUEError(err error, node *index.ProjectNode) {
	list := errors.Errors(err)
	for _, e := range list {
		msg := e.Error()
		v.report(node, "schema_validation", LevelError,
			fmt.Sprintf("Schema Validation Error: %v", msg),
			v.getNodePosition(node), v.getNodeFile(node))
	}
}

func (v *Validator) nodeToMapWithDepth(node *index.ProjectNode, depth int) map[string]interface{} {
	m := make(map[string]interface{})
	fields := v.getFields(node)

	for name, defs := range fields {
		if len(defs) > 0 {
			// Use the last definition (duplicates checked elsewhere)
			val := v.ValueToInterface(defs[len(defs)-1].Value, node)
			// Strip namespace from Class field value for schema validation
			if name == "Class" {
				if s, ok := val.(string); ok {
					if idx := strings.LastIndex(s, "::"); idx != -1 {
						val = s[idx+2:]
					}
				}
			}
			m[name] = val
		}
	}

	if size := v.getSignalByteSize(node); size > 0 {
		m["ByteSize"] = int(size)
	}

	// Children as nested maps?
	if depth > 0 {
		for name, child := range node.Children {
			m[name] = v.nodeToMapWithDepth(child, depth-1)
		}
	}

	return m
}


func (v *Validator) ValueToInterface(val parser.Value, ctx *index.ProjectNode) interface{} {
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
			return v.ValueToInterface(ov, ctx)
		}
		if info := v.Tree.ResolveVariable(ctx, name); info != nil {
			if info.Def.DefaultValue != nil {
				return v.ValueToInterface(info.Def.DefaultValue, ctx)
			}
		}
		return nil
	case *parser.ArrayValue:
		var arr []interface{}
		for _, e := range t.Elements {
			arr = append(arr, v.ValueToInterface(e, ctx))
		}
		return arr
	case *parser.BinaryExpression:
		left := v.ValueToInterface(t.Left, ctx)
		right := v.ValueToInterface(t.Right, ctx)
		return v.evaluateBinary(left, t.Operator.Type, right)
	case *parser.UnaryExpression:
		val := v.ValueToInterface(t.Right, ctx)
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

func (v *Validator) validateSignal(node *index.ProjectNode, fields map[string][]index.EvaluatedField) {
	// ... (same as before)
	if typeFields, ok := fields["Type"]; !ok || len(typeFields) == 0 {
		v.report(node, "missing_signal_type", LevelError,
			fmt.Sprintf("Signal '%s' is missing mandatory field 'Type'", node.RealName),
			v.getNodePosition(node), v.getNodeFile(node))
	} else {
		typeVal := typeFields[0].Value
		var typeStr string
		switch t := typeVal.(type) {
		case *parser.StringValue:
			typeStr = t.Value
		case *parser.ReferenceValue:
			typeStr = t.Value
		default:
			v.report(node, "invalid_signal_type", LevelError,
				fmt.Sprintf("Field 'Type' in Signal '%s' must be a type name", node.RealName),
				typeFields[0].Raw.Position, typeFields[0].File)
			return
		}

		if !isValidType(typeStr) {
			v.report(node, "invalid_signal_type", LevelError,
				fmt.Sprintf("Invalid Type '%s' for Signal '%s'", typeStr, node.RealName),
				typeFields[0].Raw.Position, typeFields[0].File)
		}
	}
	
	v.validateByteSize(node, fields)
}

func (v *Validator) validateGAM(node *index.ProjectNode) {
	if inputs, ok := node.Children["InputSignals"]; ok {
		v.validateGAMSignals(node, inputs, "Input")
	}
	if outputs, ok := node.Children["OutputSignals"]; ok {
		v.validateGAMSignals(node, outputs, "Output")
	}
}



func (v *Validator) validateDataSource(node *index.ProjectNode) {
	// Hooks for DataSource specialized validation
	// switch v.getNodeClass(node) { ... }
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
		v.report(signalNode, "unknown_datasource", LevelError,
			fmt.Sprintf("Unknown DataSource '%s' referenced in signal '%s'", dsName, signalNode.RealName),
			v.getNodePosition(signalNode), v.getNodeFile(signalNode))
		return
	}

	// Link DataSource reference
	if dsFields, ok := fields["DataSource"]; ok && len(dsFields) > 0 {
		if val, ok := dsFields[0].Raw.Value.(*parser.ReferenceValue); ok {
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
					v.report(signalNode, "datasource_direction", LevelError,
						fmt.Sprintf("DataSource '%s' (Class %s) is Output-only but referenced in InputSignals of GAM '%s'", dsName, dsClass, gamNode.RealName),
						v.getNodePosition(signalNode), v.getNodeFile(signalNode))
				}
				if direction == "Output" && dsDir == "IN" {
					v.report(signalNode, "datasource_direction", LevelError,
						fmt.Sprintf("DataSource '%s' (Class %s) is Input-only but referenced in OutputSignals of GAM '%s'", dsName, dsClass, gamNode.RealName),
						v.getNodePosition(signalNode), v.getNodeFile(signalNode))
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
		v.report(signalNode, "implicit_signal", LevelWarning,
			fmt.Sprintf("Implicitly Defined Signal: '%s' is defined in GAM '%s' but not in DataSource '%s'", targetSignalName, gamNode.RealName, dsName),
			v.getNodePosition(signalNode), v.getNodeFile(signalNode))

		if typeFields, ok := fields["Type"]; !ok || len(typeFields) == 0 {
			v.report(signalNode, "implicit_signal_missing_type", LevelError,
				fmt.Sprintf("Implicit signal '%s' must define Type", targetSignalName),
				v.getNodePosition(signalNode), v.getNodeFile(signalNode))
		} else {
			// Check Type validity even for implicit
			typeVal := v.getFieldValue(typeFields[0], signalNode)
			if !isValidType(typeVal) {
				v.report(signalNode, "invalid_signal_type", LevelError,
					fmt.Sprintf("Invalid Type '%s' for Signal '%s'", typeVal, signalNode.RealName),
					typeFields[0].Raw.Position, typeFields[0].File)
			}
		}
	} else {
		signalNode.Target = targetNode
		// Link Alias reference
		if aliasFields, ok := fields["Alias"]; ok && len(aliasFields) > 0 {
			if val, ok := aliasFields[0].Raw.Value.(*parser.ReferenceValue); ok {
				v.updateReferenceTarget(v.getNodeFile(signalNode), val.Position, targetNode)
			}
		}

		// Property checks
		v.checkSignalProperty(signalNode, targetNode, "Type")
		
		// If Ranges or Samples are present, NumberOfElements/Dimensions might differ legitimately (local override/modification)
		hasModifiers := false
		if r, ok := fields["Ranges"]; ok && len(r) > 0 { hasModifiers = true }
		if s, ok := fields["Samples"]; ok && len(s) > 0 { hasModifiers = true }

		if !hasModifiers {
			v.checkSignalProperty(signalNode, targetNode, "NumberOfElements")
			v.checkSignalProperty(signalNode, targetNode, "NumberOfDimensions")
		}

		// Check Type validity if present
		if typeFields, ok := fields["Type"]; ok && len(typeFields) > 0 {
			typeVal := v.getFieldValue(typeFields[0], signalNode)
			if !isValidType(typeVal) {
				v.report(signalNode, "invalid_signal_type", LevelError,
					fmt.Sprintf("Invalid Type '%s' for Signal '%s'", typeVal, signalNode.RealName),
					typeFields[0].Raw.Position, typeFields[0].File)
			}
		}
	}

	// Validate ByteSize
	v.validateByteSize(signalNode, fields)

	// Validate Value initialization
	if valField, hasValue := fields["Value"]; hasValue && len(valField) > 0 {
		var typeStr string
		if typeFields, ok := fields["Type"]; ok && len(typeFields) > 0 {
			typeStr = v.getFieldValue(typeFields[0], signalNode)
		} else if signalNode.Target != nil {
			if tFields, ok := signalNode.Target.Fields["Type"]; ok && len(tFields) > 0 {
				typeStr = v.getFieldValue(tFields[0], signalNode.Target)
			} else if t, ok := signalNode.Target.Metadata["Type"]; ok {
				typeStr = t
			}
		}

		if typeStr != "" && v.Schema != nil {
			ctx := v.Schema.Context
			typeVal := ctx.CompileString(typeStr)
			if typeVal.Err() == nil {
				valInterface := v.ValueToInterface(valField[0].Value, signalNode)
				valVal := ctx.Encode(valInterface)
				res := typeVal.Unify(valVal)
				if err := res.Validate(cue.Concrete(true)); err != nil {
					v.report(signalNode, "signal_value_mismatch", LevelError,
						fmt.Sprintf("Value initialization mismatch for signal '%s': %v", signalNode.RealName, err),
						valField[0].Raw.Position, v.getNodeFile(signalNode))
				}
			}
		}
	}
}

func (v *Validator) getEvaluatedMetadata(node *index.ProjectNode, key string) string {
	if fields, ok := node.Fields[key]; ok && len(fields) > 0 {
		return v.getFieldValue(fields[len(fields)-1], node)
	}
	return node.Metadata[key]
}

func (v *Validator) getSignalByteSize(node *index.ProjectNode) int64 {
	fields := v.getFields(node)

	// Get Type
	typeStr := ""
	if typeFields, ok := fields["Type"]; ok && len(typeFields) > 0 {
		typeStr = v.getFieldValue(typeFields[0], node)
	} else if node.Target != nil {
		if tFields, ok := node.Target.Fields["Type"]; ok && len(tFields) > 0 {
			typeStr = v.getFieldValue(tFields[0], node.Target)
		} else if t, ok := node.Target.Metadata["Type"]; ok {
			typeStr = t
		}
	}

	if typeStr == "" {
		return 0
	}

	typeSize := v.getTypeSize(typeStr)
	if typeSize <= 0 {
		return 0 // Variable size or unknown type
	}

	totalElements := int64(1)

	// 1. Check for Ranges (Overrides base dimensions)
	if rangeFields, ok := fields["Ranges"]; ok && len(rangeFields) > 0 {
		if arr, ok := rangeFields[0].Value.(*parser.ArrayValue); ok {
			totalElements = 1
			for _, elem := range arr.Elements {
				if inner, ok := elem.(*parser.ArrayValue); ok && len(inner.Elements) == 2 {
					start := v.ValueToInterface(inner.Elements[0], node)
					stop := v.ValueToInterface(inner.Elements[1], node)

					var iStart, iStop int64
					if s, ok := start.(int64); ok {
						iStart = s
					} else if s, ok := start.(int); ok {
						iStart = int64(s)
					}
					if s, ok := stop.(int64); ok {
						iStop = s
					} else if s, ok := stop.(int); ok {
						iStop = int64(s)
					}

					if iStop >= iStart {
						totalElements *= (iStop - iStart + 1)
					}
				}
			}
		}
	} else {
		// Base elements calculation
		numElements := int64(1)
		if neFields, ok := fields["NumberOfElements"]; ok && len(neFields) > 0 {
			val := v.ValueToInterface(neFields[0].Value, node)
			if i, ok := val.(int64); ok {
				numElements = i
			} else if i, ok := val.(int); ok {
				numElements = int64(i)
			}
		} else if node.Target != nil {
			if neFields, ok := node.Target.Fields["NumberOfElements"]; ok && len(neFields) > 0 {
				val := v.ValueToInterface(neFields[0].Value, node.Target)
				if i, ok := val.(int64); ok {
					numElements = i
				} else if i, ok := val.(int); ok {
					numElements = int64(i)
				}
			} else if ne, ok := node.Target.Metadata["NumberOfElements"]; ok {
				if i, err := strconv.ParseInt(ne, 0, 64); err == nil {
					numElements = i
				}
			}
		}

		numDimensions := int64(1)
		if ndFields, ok := fields["NumberOfDimensions"]; ok && len(ndFields) > 0 {
			val := v.ValueToInterface(ndFields[0].Value, node)
			if i, ok := val.(int64); ok {
				numDimensions = i
			} else if i, ok := val.(int); ok {
				numDimensions = int64(i)
			}
		} else if node.Target != nil {
			if ndFields, ok := node.Target.Fields["NumberOfDimensions"]; ok && len(ndFields) > 0 {
				val := v.ValueToInterface(ndFields[0].Value, node.Target)
				if i, ok := val.(int64); ok {
					numDimensions = i
				} else if i, ok := val.(int); ok {
					numDimensions = int64(i)
				}
			} else if nd, ok := node.Target.Metadata["NumberOfDimensions"]; ok {
				if i, err := strconv.ParseInt(nd, 0, 64); err == nil {
					numDimensions = i
				}
			}
		}
		totalElements = numElements * numDimensions
	}

	// 2. Check for Samples multiplier
	if sampleFields, ok := fields["Samples"]; ok && len(sampleFields) > 0 {
		val := v.ValueToInterface(sampleFields[0].Value, node)
		var samples int64
		if i, ok := val.(int64); ok {
			samples = i
		} else if i, ok := val.(int); ok {
			samples = int64(i)
		}
		if samples > 0 {
			totalElements *= samples
		}
	}

	return int64(typeSize) * totalElements
}

func (v *Validator) validateByteSize(node *index.ProjectNode, fields map[string][]index.EvaluatedField) {
	expectedSize := v.getSignalByteSize(node)
	if expectedSize == 0 {
		return
	}

	// Check ByteSize or ByteDimension
	sizeFields := [][]index.EvaluatedField{}
	if f, ok := fields["ByteSize"]; ok {
		sizeFields = append(sizeFields, f)
	}
	if f, ok := fields["ByteDimension"]; ok {
		sizeFields = append(sizeFields, f)
	}

	for _, fs := range sizeFields {
		if len(fs) > 0 {
			val := v.ValueToInterface(fs[0].Value, node)
			var definedSize int64
			if i, ok := val.(int64); ok {
				definedSize = i
			} else if i, ok := val.(int); ok {
				definedSize = int64(i)
			} else {
				continue
			}

			if definedSize != expectedSize {
				v.report(node, "signal_size_mismatch", LevelError,
					fmt.Sprintf("Size mismatch for signal '%s': defined %d, expected %d", node.RealName, definedSize, expectedSize),
					fs[0].Raw.Position, v.getNodeFile(node))
			}
		}
	}
}

func (v *Validator) getTypeSize(typeName string) int {
	switch typeName {
	case "uint8", "int8", "char8", "bool":
		return 1
	case "uint16", "int16":
		return 2
	case "uint32", "int32", "float32":
		return 4
	case "uint64", "int64", "float64":
		return 8
	case "string":
		return -1 // Variable size, usually pointer or handled differently
	}
	return 0 // Unknown
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

		v.report(gamSig, "signal_property_mismatch", LevelError,
			fmt.Sprintf("Signal '%s' property '%s' mismatch: defined '%s', referenced '%s'", gamSig.RealName, prop, dsVal, gamVal),
			v.getNodePosition(gamSig), v.getNodeFile(gamSig))
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
	v.mu.Lock()
	defer v.mu.Unlock()
	if refs, ok := v.Tree.FileReferences[file]; ok {
		for i := range refs {
			if refs[i].Position == pos {
				refs[i].Target = target
				// Also update legacy slice if necessary, but FileReferences is primary now
				// pt.References will be rebuilt by ResolveReferences anyway?
				// But we are in middle of validation.
				// For now, update map entry.
				return
			}
		}
	}
}

// Helpers

func (v *Validator) getFields(node *index.ProjectNode) map[string][]index.EvaluatedField {
	return node.Fields
}

func (v *Validator) getFieldValue(f index.EvaluatedField, ctx *index.ProjectNode) string {
	res := v.ValueToInterface(f.Value, ctx)
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
		// Strip namespace if present
		if idx := strings.LastIndex(cls, "::"); idx != -1 {
			return cls[idx+2:]
		}
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

func (v *Validator) CheckUnused(ctx context.Context) {
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
		v.checkUnusedRecursive(ctx, v.Tree.Root, referencedNodes)
	}
	for _, node := range v.Tree.IsolatedFiles {
		v.checkUnusedRecursive(ctx, node, referencedNodes)
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

func (v *Validator) checkUnusedRecursive(ctx context.Context, node *index.ProjectNode, referenced map[*index.ProjectNode]bool) {
	if ctx.Err() != nil {
		return
	}
	// Heuristic for GAM
	if isGAM(node) {
		if !referenced[node] {
			v.report(node, "unused_gam", LevelWarning,
				fmt.Sprintf("Unused GAM: %s is defined but not referenced in any thread or scheduler", node.RealName),
				v.getNodePosition(node), v.getNodeFile(node))
		}
	}

	// Heuristic for DataSource and its signals
	if isDataSource(node) {
		if signalsNode, ok := node.Children["Signals"]; ok {
			for _, signal := range signalsNode.Children {
				if !referenced[signal] {
					v.report(signal, "unused_signal", LevelWarning,
						fmt.Sprintf("Unused Signal: %s is defined in DataSource %s but never referenced", signal.RealName, node.RealName),
						v.getNodePosition(signal), v.getNodeFile(signal))
				}
			}
		}
	}

	for _, child := range node.Children {
		v.checkUnusedRecursive(ctx, child, referenced)
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

func (v *Validator) checkFunctionsArray(node *index.ProjectNode, fields map[string][]index.EvaluatedField) {
	if funcs, ok := fields["Functions"]; ok && len(funcs) > 0 {
		f := funcs[0]
		if arr, ok := f.Value.(*parser.ArrayValue); ok {
			for _, elem := range arr.Elements {
				if ref, ok := elem.(*parser.ReferenceValue); ok {
					target := v.resolveReference(ref.Value, node, isGAM)
					if target == nil {
						v.report(node, "invalid_function", LevelError,
							fmt.Sprintf("Function '%s' not found or is not a valid GAM", ref.Value),
							ref.Position, v.getNodeFile(node))
					}
				} else {
					v.report(node, "invalid_function", LevelError,
						"Functions array must contain references",
						f.Raw.Position, v.getNodeFile(node))
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

func (v *Validator) CheckDataSourceThreading(ctx context.Context) {
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
		if ctx.Err() != nil {
			return
		}
		v.checkAppDataSourceThreading(ctx, appNode)
	}
}

func (v *Validator) checkAppDataSourceThreading(ctx context.Context, appNode *index.ProjectNode) {
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
								v.report(gam, "datasource_threading", LevelError,
									fmt.Sprintf("DataSource '%s' is not multithreaded but used in multiple threads (%s, %s) in state '%s'", ds.RealName, existingThread, thread.RealName, state.RealName),
									v.getNodePosition(gam), v.getNodeFile(gam))
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

func (v *Validator) CheckINOUTOrdering(ctx context.Context) {
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
		if ctx.Err() != nil {
			return
		}
		v.checkAppINOUTOrdering(ctx, appNode)
	}
}

func (v *Validator) checkAppINOUTOrdering(ctx context.Context, appNode *index.ProjectNode) {
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
							v.report(prod, "not_consumed", LevelWarning,
								fmt.Sprintf("INOUT Signal '%s' (DS '%s') is produced in thread '%s' but never consumed in the same thread.", sigName, ds.RealName, thread.RealName),
								v.getNodePosition(prod), v.getNodeFile(prod))
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

			isProduced := false
			if set, ok := produced[dsNode]; ok {
				if len(set[sigName]) > 0 {
					isProduced = true
				}
			}

			if !isProduced {
				v.report(sig, "not_produced", LevelError,
					fmt.Sprintf("INOUT Signal '%s' (DS '%s') is consumed by GAM '%s' in thread '%s' (State '%s') before being produced by any previous GAM.", sigName, dsNode.RealName, gam.RealName, thread.RealName, state.RealName),
					v.getNodePosition(sig), v.getNodeFile(sig))
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

func (v *Validator) CheckSignalConsistency(ctx context.Context) {
	// Map: DataSourceNode -> SignalName -> List of Signals
	signals := make(map[*index.ProjectNode]map[string][]*index.ProjectNode)

	// Helper to collect signals
	collect := func(node *index.ProjectNode) {
		if ctx.Err() != nil {
			return
		}
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
		if ctx.Err() != nil {
			return
		}
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
						v.report(u, "signal_type_mismatch", LevelError,
							fmt.Sprintf("Signal Type Mismatch: Signal '%s' (in DS '%s') is defined as '%s' in '%s' but as '%s' in '%s'", sigName, ds.RealName, firstType, firstNode.Parent.Parent.RealName, typeVal, u.Parent.Parent.RealName),
							v.getNodePosition(u), v.getNodeFile(u))
					}
				}
			}
		}
	}
}

func (v *Validator) CheckVariables(ctx context.Context) {
	if v.Schema == nil {
		return
	}
	ctx_cue := v.Schema.Context

	checkNodeVars := func(node *index.ProjectNode) {
		if ctx.Err() != nil {
			return
		}
		seen := make(map[string]parser.Position)
		for _, frag := range node.Fragments {
			for _, def := range frag.Definitions {
				if vdef, ok := def.(*parser.VariableDefinition); ok {
					if prevPos, exists := seen[vdef.Name]; exists {
						v.report(node, "duplicate_variable", LevelError,
							fmt.Sprintf("Duplicate variable definition: '%s' was already defined at %d:%d", vdef.Name, prevPos.Line, prevPos.Column),
							vdef.Position, frag.File)
					}
					seen[vdef.Name] = vdef.Position

					if vdef.IsConst && vdef.DefaultValue == nil {
						v.report(node, "missing_variable_value", LevelError,
							fmt.Sprintf("Constant variable '%s' must have an initial value", vdef.Name),
							vdef.Position, frag.File)
						continue
					}

					// Compile Type
					typeVal := ctx_cue.CompileString(vdef.TypeExpr)
					if typeVal.Err() != nil {
						v.report(node, "invalid_variable_type", LevelError,
							fmt.Sprintf("Invalid type expression for variable '%s': %v", vdef.Name, typeVal.Err()),
							vdef.Position, frag.File)
						continue
					}

					if vdef.DefaultValue != nil {
						valInterface := v.ValueToInterface(vdef.DefaultValue, node)
						valVal := ctx_cue.Encode(valInterface)

						// Unify
						res := typeVal.Unify(valVal)
						if err := res.Validate(cue.Concrete(true)); err != nil {
							v.report(node, "variable_value_mismatch", LevelError,
								fmt.Sprintf("Variable '%s' value mismatch: %v", vdef.Name, err),
								vdef.Position, frag.File)
						}
					}
				}
			}
		}
	}

	v.Tree.Walk(checkNodeVars)
}
func (v *Validator) CheckUnresolvedVariables(ctx context.Context) {
	for _, ref := range v.Tree.References {
		if ctx.Err() != nil {
			return
		}
		if ref.IsVariable && ref.TargetVariable == nil {
			v.report(nil, "unresolved_variable", LevelError,
				fmt.Sprintf("Unresolved variable reference: '@%s'", ref.Name),
				ref.Position, ref.File)
		}
	}
}
