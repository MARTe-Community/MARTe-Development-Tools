package parser

type Node interface {
	Pos() Position
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
func (f *Field) isDefinition() {}

type ObjectNode struct {
	Position Position
	Name     string // includes + or $
	Subnode  Subnode
}

func (o *ObjectNode) Pos() Position { return o.Position }
func (o *ObjectNode) isDefinition() {}

type Subnode struct {
	Position    Position
	EndPosition Position
	Definitions []Definition
}

func (s *Subnode) Pos() Position { return s.Position }

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
func (v *StringValue) isValue()      {}

type IntValue struct {
	Position Position
	Value    int64
	Raw      string
}

func (v *IntValue) Pos() Position { return v.Position }
func (v *IntValue) isValue()      {}

type FloatValue struct {
	Position Position
	Value    float64
	Raw      string
}

func (v *FloatValue) Pos() Position { return v.Position }
func (v *FloatValue) isValue()      {}

type BoolValue struct {
	Position Position
	Value    bool
}

func (v *BoolValue) Pos() Position { return v.Position }
func (v *BoolValue) isValue()      {}

type ReferenceValue struct {
	Position Position
	Value    string
}

func (v *ReferenceValue) Pos() Position { return v.Position }
func (v *ReferenceValue) isValue()      {}

type ArrayValue struct {
	Position    Position
	EndPosition Position
	Elements    []Value
}

func (v *ArrayValue) Pos() Position { return v.Position }
func (v *ArrayValue) isValue()      {}

type Package struct {
	Position Position
	URI      string
}

func (p *Package) Pos() Position { return p.Position }

type Comment struct {
	Position Position
	Text     string
	Doc      bool // true if starts with //#
}

func (c *Comment) Pos() Position { return c.Position }

type Pragma struct {
	Position Position
	Text     string
}

func (p *Pragma) Pos() Position { return p.Position }

type VariableDefinition struct {
	Position     Position
	Name         string
	TypeExpr     string
	DefaultValue Value
}

func (v *VariableDefinition) Pos() Position { return v.Position }
func (v *VariableDefinition) isDefinition() {}

type VariableReferenceValue struct {
	Position Position
	Name     string
}

func (v *VariableReferenceValue) Pos() Position { return v.Position }
func (v *VariableReferenceValue) isValue()      {}
