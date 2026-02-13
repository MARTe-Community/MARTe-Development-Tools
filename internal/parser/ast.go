package parser

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
	Position     Position
	Name         string
	TypeExpr     string
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
	Else        []Definition
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
