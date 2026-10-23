package parser

import "sort"

type Node interface {
	Pos() Position
	End() Position
}

type Position struct {
	Line   int
	Column int
}

type Configuration struct {
	Definitions []Definition
	Package     *Package
	Comments    []Comment
	Pragmas     []Pragma
}

type Definition interface {
	Node
	isDefinition()
}

type Field struct {
	Position Position
	Name     string
	Value    Value
}

func (f *Field) Pos() Position { return f.Position }
func (f *Field) End() Position { return f.Value.End() }
func (f *Field) isDefinition() {}

type ObjectNode struct {
	Position Position
	Name     Value // can be StringValue, ReferenceValue or BinaryExpression (concat)
	Subnode  Subnode
}

func (o *ObjectNode) Pos() Position { return o.Position }
func (o *ObjectNode) End() Position { return o.Subnode.End() }
func (o *ObjectNode) isDefinition() {}

type Subnode struct {
	Position    Position
	EndPosition Position
	Definitions []Definition
}

func (s *Subnode) Pos() Position { return s.Position }
func (s *Subnode) End() Position { return s.EndPosition }

type Value interface {
	Node
	isValue()
}

type StringValue struct {
	Position Position
	Value    string
	Quoted   bool
}

func (v *StringValue) Pos() Position { return v.Position }
func (v *StringValue) End() Position {
	col := v.Position.Column + len(v.Value)
	if v.Quoted {
		col += 2
	}
	return Position{Line: v.Position.Line, Column: col}
}
func (v *StringValue) isValue() {}

type IntValue struct {
	Position Position
	Value    int64
	Raw      string
}

func (v *IntValue) Pos() Position { return v.Position }
func (v *IntValue) End() Position {
	return Position{Line: v.Position.Line, Column: v.Position.Column + len(v.Raw)}
}
func (v *IntValue) isValue() {}

type FloatValue struct {
	Position Position
	Value    float64
	Raw      string
}

func (v *FloatValue) Pos() Position { return v.Position }
func (v *FloatValue) End() Position {
	return Position{Line: v.Position.Line, Column: v.Position.Column + len(v.Raw)}
}
func (v *FloatValue) isValue() {}

type BoolValue struct {
	Position Position
	Value    bool
}

func (v *BoolValue) Pos() Position { return v.Position }
func (v *BoolValue) End() Position {
	length := 4 // true
	if !v.Value {
		length = 5 // false
	}
	return Position{Line: v.Position.Line, Column: v.Position.Column + length}
}
func (v *BoolValue) isValue() {}

type ReferenceValue struct {
	Position Position
	Value    string
}

func (v *ReferenceValue) Pos() Position { return v.Position }
func (v *ReferenceValue) End() Position {
	return Position{Line: v.Position.Line, Column: v.Position.Column + len(v.Value)}
}
func (v *ReferenceValue) isValue() {}

type ArrayValue struct {
	Position    Position
	EndPosition Position
	Elements    []Value
}

func (v *ArrayValue) Pos() Position { return v.Position }
func (v *ArrayValue) End() Position { return v.EndPosition }
func (v *ArrayValue) isValue()      {}

// ConditionalArrayElements represents a #if/#else/#end block inside an array.
// It implements Value so it can appear as an element of ArrayValue.Elements.
// When the containing array is evaluated the active branch is flattened inline.
type ConditionalArrayElements struct {
	Position    Position
	EndPosition Position
	Condition   Value
	Then        []Value
	ElseIf      []ConditionalElseIfBranch
	Else        []Value
}

type ConditionalElseIfBranch struct {
	Condition Value
	Body      []Value
}

func (c *ConditionalArrayElements) Pos() Position { return c.Position }
func (c *ConditionalArrayElements) End() Position { return c.EndPosition }
func (c *ConditionalArrayElements) isValue()      {}

// MapValue is a string-keyed map of values, produced by the structured
// loaders (`with json("file") as name`). Keys are kept sorted so that
// iteration and rendering are deterministic.
type MapValue struct {
	Position Position
	Keys     []string
	Values   map[string]Value
}

func (v *MapValue) Pos() Position { return v.Position }
func (v *MapValue) End() Position { return v.Position }
func (v *MapValue) isValue()      {}

// Lookup returns the value stored under key.
func (v *MapValue) Lookup(key string) (Value, bool) {
	val, ok := v.Values[key]
	return val, ok
}

// MemberAccess is a structured member lookup on a value, written
// `@document.member` (chains are allowed: `@a.b.c`).
type MemberAccess struct {
	Position Position
	Base     Value
	Member   string
}

func (v *MemberAccess) Pos() Position { return v.Position }

// End returns the position just past the member name. The base expression
// is part of the access, so the member does not start at the access'
// position: `@prefixes.fast` ends past `fast`, not past `pref`.
func (v *MemberAccess) End() Position {
	base := v.Base.End()
	return Position{Line: v.Position.Line, Column: base.Column + len(v.Member) + 1}
}
func (v *MemberAccess) isValue() {}

// MethodCall is a builtin method invocation on a value, written
// `receiver.method(arg, …)`:
//
//	@variable.name.replace("EC-GN-P01:", "")
//	@list.append(4).length()
//
// The receiver is any expression; the method set depends on the receiver's
// kind (string, list or dict).
type MethodCall struct {
	Position Position
	// EndPosition is just past the closing parenthesis. Zero for values
	// built programmatically rather than parsed.
	EndPosition Position
	Base        Value
	Method      string
	Args        []Value
}

func (v *MethodCall) Pos() Position { return v.Position }
func (v *MethodCall) End() Position {
	if v.EndPosition.Line > 0 {
		return v.EndPosition
	}
	return v.Base.End()
}
func (v *MethodCall) isValue() {}

// FunctionCall is a builtin iterator function invocation, written
// `name(arg, …)`:
//
//	enumerate(@devices)
//	zip(@names, @channels)
//	range(0, 8, 2)
//
// The argument list is evaluated in the caller's context; the builtin set
// lives in internal/loader (FunctionCall).
type FunctionCall struct {
	Position Position
	// EndPosition is just past the closing parenthesis. Zero for values
	// built programmatically rather than parsed.
	EndPosition Position
	Name        string
	Args        []Value
}

func (v *FunctionCall) Pos() Position { return v.Position }
func (v *FunctionCall) End() Position {
	if v.EndPosition.Line > 0 {
		return v.EndPosition
	}
	return v.Position
}
func (v *FunctionCall) isValue() {}

// NewMapValue builds a MapValue from a Go map, sorting the keys.
func NewMapValue(pos Position, values map[string]Value) *MapValue {
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return &MapValue{Position: pos, Keys: keys, Values: values}
}

type Package struct {
	Position Position
	URI      string
}

func (p *Package) Pos() Position { return p.Position }
func (p *Package) End() Position {
	return Position{Line: p.Position.Line, Column: p.Position.Column + 8 + 1 + len(p.URI)}
}

type Comment struct {
	Position Position
	Text     string
	Doc      bool // true if starts with //#
}

func (c *Comment) Pos() Position { return c.Position }
func (c *Comment) End() Position {
	return Position{Line: c.Position.Line, Column: c.Position.Column + len(c.Text)}
}

type Pragma struct {
	Position Position
	Text     string
}

func (p *Pragma) Pos() Position { return p.Position }
func (p *Pragma) End() Position {
	return Position{Line: p.Position.Line, Column: p.Position.Column + len(p.Text)}
}

type VariableDefinition struct {
	Position Position
	Name     string
	TypeExpr string
	// InlineType holds the parsed structured type when TypeExpr is an
	// anonymous struct type (`var v: { field: type, ... }`). nil otherwise.
	InlineType   *TypeDefinition
	DefaultValue Value
	IsConst      bool
}

func (v *VariableDefinition) Pos() Position { return v.Position }
func (v *VariableDefinition) End() Position {
	if v.DefaultValue != nil {
		return v.DefaultValue.End()
	}
	return Position{Line: v.Position.Line, Column: v.Position.Column + 4 + 1 + len(v.Name) + 2 + len(v.TypeExpr)}
}
func (v *VariableDefinition) isDefinition() {}

// TypeDefinition declares a named structured type:
//
//	type ADCConf_T {
//	    device_name: str,
//	    board_id: uint8,
//	}
//
// Fields are name/type pairs. Type expressions use the same syntax as
// variable types (scalars, lists, node references, regex constraints) plus
// references to other declared types. Values of such variables are struct
// literals (`{ field = value, ... }`) and are validated against the fields.
type TypeDefinition struct {
	Position Position
	Name     string
	Fields   []*TypeField
}

func (t *TypeDefinition) Pos() Position { return t.Position }
func (t *TypeDefinition) End() Position {
	if len(t.Fields) > 0 {
		return t.Fields[len(t.Fields)-1].End()
	}
	return Position{Line: t.Position.Line, Column: t.Position.Column + len("type ") + len(t.Name)}
}
func (t *TypeDefinition) isDefinition() {}

// Field returns the field declaration with the given name, or nil.
func (t *TypeDefinition) Field(name string) *TypeField {
	for _, f := range t.Fields {
		if f.Name == name {
			return f
		}
	}
	return nil
}

// TypeField is one `name: type` entry of a structured type.
type TypeField struct {
	Position Position
	Name     string
	TypeExpr string
	// InlineType holds the parsed structured type when the field's type is
	// an anonymous struct (`field: { ... }`). nil otherwise.
	InlineType *TypeDefinition
	// DefaultValue is the field's default value (`field: type = value`).
	// Fields without one must be provided by the variable's initializer or
	// an override.
	DefaultValue Value
}

func (f *TypeField) Pos() Position { return f.Position }
func (f *TypeField) End() Position {
	return Position{Line: f.Position.Line, Column: f.Position.Column + len(f.Name) + 2 + len(f.TypeExpr)}
}

type VariableReferenceValue struct {
	Position Position
	Name     string
}

func (v *VariableReferenceValue) Pos() Position { return v.Position }
func (v *VariableReferenceValue) End() Position {
	return Position{Line: v.Position.Line, Column: v.Position.Column + len(v.Name)}
}
func (v *VariableReferenceValue) isValue() {}

type BinaryExpression struct {
	Position Position
	Left     Value
	Operator Token
	Right    Value
}

func (b *BinaryExpression) Pos() Position { return b.Position }
func (b *BinaryExpression) End() Position { return b.Right.End() }
func (b *BinaryExpression) isValue()      {}

type UnaryExpression struct {
	Position Position
	Operator Token
	Right    Value
}

func (u *UnaryExpression) Pos() Position { return u.Position }
func (u *UnaryExpression) End() Position { return u.Right.End() }
func (u *UnaryExpression) isValue()      {}

type IfBlock struct {
	Position    Position
	EndPosition Position
	Condition   Value
	Then        []Definition
	ElseIf      []ElseIfBranch
	Else        []Definition
}

type ElseIfBranch struct {
	Condition Value
	Body      []Definition
}

func (i *IfBlock) Pos() Position { return i.Position }
func (i *IfBlock) End() Position { return i.EndPosition }
func (i *IfBlock) isDefinition() {}

type ForeachBlock struct {
	Position    Position
	EndPosition Position
	KeyVar      string // optional
	ValueVar    string
	Iterable    Value
	Body        []Definition
}

func (f *ForeachBlock) Pos() Position { return f.Position }
func (f *ForeachBlock) End() Position { return f.EndPosition }
func (f *ForeachBlock) isDefinition() {}

type TemplateDefinition struct {
	Position    Position
	EndPosition Position
	Name        string
	Parameters  []TemplateParameter
	Body        []Definition
}

type TemplateParameter struct {
	Name         string
	TypeExpr     string
	DefaultValue Value
}

func (t *TemplateDefinition) Pos() Position { return t.Position }
func (t *TemplateDefinition) End() Position { return t.EndPosition }
func (t *TemplateDefinition) isDefinition() {}

type TemplateInstantiation struct {
	Position    Position
	EndPosition Position
	Name        string // Name of the instance
	Template    string // Name of the template
	Arguments   []TemplateArgument
}

type TemplateArgument struct {
	Name  string
	Value Value
}

func (t *TemplateInstantiation) Pos() Position { return t.Position }
func (t *TemplateInstantiation) End() Position { return t.EndPosition }
func (t *TemplateInstantiation) isDefinition() {}

// SignalShorthand is syntactic sugar for a signal entry inside
// InputSignals or OutputSignals blocks:
//
//	DataSource::SignalName
//	DataSource::SignalName: Type
//	DataSource::SignalName: Type[NumElements]
//	DataSource::SignalName: Type[NumElements] = { Field = Val … }
//
// DataSource and SignalName are mandatory. Type, NumElements and ExtraFields
// are all optional.
type SignalShorthand struct {
	Position           Position
	EndPosition        Position
	SignalNamePosition Position // start position of SignalName alone, i.e. Position shifted past "DataSource::"
	DataSource         string   // left of "::"
	SignalName         string   // right of "::"
	Type               string   // empty when absent
	NumElements        Value    // nil when absent
	AliasName          string   // non-empty when "as <NAME>" was specified; becomes the node name; SignalName becomes the Alias field
	ExtraFields        Subnode  // body of the optional "= { … }" block
	HasExtraFields     bool     // true when "= { … }" was present
}

func (s *SignalShorthand) Pos() Position { return s.Position }
func (s *SignalShorthand) End() Position { return s.EndPosition }
func (s *SignalShorthand) isDefinition() {}

// WithBlock loads an external structured document (JSON, CSV, …) and
// binds it to a name for the duration of the block body:
//
//	with json("epics.json") as epics_cfg begin
//	  …
//	end
type WithBlock struct {
	Position    Position
	EndPosition Position
	Format      string // "json", "csv", …
	Path        Value  // expression evaluating to the file path
	BindName    string
	Body        []Definition
}

func (w *WithBlock) Pos() Position { return w.Position }
func (w *WithBlock) End() Position { return w.EndPosition }
func (w *WithBlock) isDefinition() {}

// --- Copy / wiring sugar ------------------------------------------------

// CopyEndpoint is one side of a copy: `GAM.Signal`, `GAM[n]` or `DS::Signal`.
type CopyEndpoint struct {
	Position Position
	Object   string // GAM or DataSource name
	Signal   string // signal name ("" when ByIndex)
	IsDS     bool   // DataSource::Signal form
	ByIndex  bool   // selected by 1-based index in the GAM's signal list
	Index    int    // 1-based start (or single) index
	IndexEnd int    // 1-based end for ranges; equals Index for single
	EndPos   Position
}

func (e CopyEndpoint) Pos() Position { return e.Position }

// CopyOption is a per-signal field emitted verbatim into the generated
// IOGAM signal entry (Type, NumberOfElements, Samples, Frequency, Ranges,
// Value, Trigger, …).
type CopyOption struct {
	Position Position
	Name     string
	Value    Value
}

// CopyMember is one signal inside a grouped endpoint, with its options.
type CopyMember struct {
	Position Position
	Endpoint CopyEndpoint
	Alias    string
	Options  []CopyOption
}

// CopyItem is one copy line inside a copy group.
type CopyItem struct {
	Position Position
	Src      CopyEndpoint
	Dst      CopyEndpoint
	// SrcAlias/DstAlias rename the local signal slot (`as NAME`).
	SrcAlias string
	DstAlias string
	// DstAt places a synthesized destination declaration at a 1-based
	// position in the GAM's signal list.
	DstAt int
	// Optional explicit type for signals that declare none.
	Type string
	Dim  int
	// Per-signal options copied into the generated entries.
	SrcOptions []CopyOption
	DstOptions []CopyOption
	// Pragmas attached to the item (`//! implicit: …`), emitted above it.
	Pragmas []string
	// Grouped endpoints: `(a, b) -> c`. When set, the members are copied as
	// one contiguous memory block (the IOGAM copies whole memory, not
	// pairwise), and Src/Dst hold the first member for position reporting.
	SrcGroup []CopyMember
	DstGroup []CopyMember
}

func (c *CopyItem) Pos() Position { return c.Position }

// CopyGroup is a copy group. With an empty Name it is an inline group that
// expands to a generated IOGAM at its position in a Functions list. With a
// Name it defines a named IOGAM object that Functions lists can reference.
type CopyGroup struct {
	Position    Position
	EndPosition Position
	Name        string
	Items       []CopyItem
}

func (c *CopyGroup) Pos() Position { return c.Position }
func (c *CopyGroup) End() Position { return c.EndPosition }
func (c *CopyGroup) isDefinition() {}
func (c *CopyGroup) isValue()      {} // valid as a Functions list element

// FromValue is the value of `Slot = from GAM.Signal` inside a signal list.
// Lowering turns it into a regular signal declaration (DataSource / Alias /
// Type) wired to the producer.
type FromValue struct {
	Position Position
	Src      CopyEndpoint
	Alias    string
	Type     string
	Dim      int
	EndPos   Position
}

func (f *FromValue) Pos() Position { return f.Position }
func (f *FromValue) End() Position { return f.EndPos }
func (f *FromValue) isValue()      {}

// FuseStatement requests GAM fusion (`fuse A with B`).
type FuseStatement struct {
	Position    Position
	EndPosition Position
	Left        string
	Right       string
}

func (f *FuseStatement) Pos() Position { return f.Position }
func (f *FuseStatement) End() Position { return f.EndPosition }
func (f *FuseStatement) isDefinition() {}
func (f *FuseStatement) isValue()      {}

// GamPort is an input/output port of a gam pipeline block.
type GamPort struct {
	Position Position
	Name     string
	Type     string
	Dim      int
	IsOutput bool
}

// GamDefinition is a `gam Name { … }` pipeline macro. It expands at build
// time into its body GAMs plus the wiring declared with `from` statements.
type GamDefinition struct {
	Position    Position
	EndPosition Position
	Name        string
	Inputs      []GamPort
	Outputs     []GamPort
	Body        []Definition
}

func (g *GamDefinition) Pos() Position { return g.Position }
func (g *GamDefinition) End() Position { return g.EndPosition }
func (g *GamDefinition) isDefinition() {}
