package formatter

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/marte-community/marte-dev-tools/internal/parser"
)

type Insertable struct {
	Position parser.Position
	Text     string
	IsDoc    bool
}

type Formatter struct {
	insertables []Insertable
	cursor      int
	writer      io.Writer
}

func Format(config *parser.Configuration, w io.Writer) {
	ins := []Insertable{}
	for _, c := range config.Comments {
		ins = append(ins, Insertable{Position: c.Position, Text: fixComment(c.Text), IsDoc: c.Doc})
	}
	for _, p := range config.Pragmas {
		ins = append(ins, Insertable{Position: p.Position, Text: fixComment(p.Text)})
	}
	// Sort
	sort.Slice(ins, func(i, j int) bool {
		if ins[i].Position.Line != ins[j].Position.Line {
			return ins[i].Position.Line < ins[j].Position.Line
		}
		return ins[i].Position.Column < ins[j].Position.Column
	})

	f := &Formatter{
		insertables: ins,
		writer:      w,
	}
	f.formatConfig(config)
}

func fixComment(text string) string {
	if !strings.HasPrefix(text, "//!") {
		if strings.HasPrefix(text, "//#") {
			if len(text) > 3 && text[3] != ' ' {
				return "//# " + text[3:]
			}
		} else if strings.HasPrefix(text, "//") {
			if len(text) > 2 && text[2] != ' ' && text[2] != '#' && text[2] != '!' {
				return "// " + text[2:]
			}
		}
	}
	return text
}

func (f *Formatter) formatConfig(config *parser.Configuration) {
	lastLine := 0
	if config.Package != nil {
		f.flushCommentsBefore(config.Package.Position, 0, false) // Package comments usually detached unless specifically doc
		fmt.Fprintf(f.writer, "#package %s", config.Package.URI)
		lastLine = config.Package.Position.Line
		if f.hasTrailingComment(lastLine) {
			fmt.Fprintf(f.writer, " %s", f.popComment())
		}
		fmt.Fprintln(f.writer)
		fmt.Fprintln(f.writer)
	}

	for _, def := range config.Definitions {
		pos := def.Pos()
		peek := f.peekPosition()
		if peek.Line > 0 && peek.Line < pos.Line {
			pos = peek
		}

		if lastLine > 0 && pos.Line > lastLine+1 {
			fmt.Fprintln(f.writer)
		}

		f.flushCommentsBefore(def.Pos(), 0, true) // Stick to definition
		lastLine = f.formatDefinition(def, 0)
		if f.hasTrailingComment(lastLine) {
			fmt.Fprintf(f.writer, " %s", f.popComment())
		}
		fmt.Fprintln(f.writer)
	}

	f.flushRemainingComments(0)
}

func (f *Formatter) formatDefinition(def parser.Definition, indent int) int {
	indentStr := strings.Repeat("  ", indent)
	switch d := def.(type) {
	case *parser.Field:
		fmt.Fprintf(f.writer, "%s%s = ", indentStr, d.Name)
		endLine := f.formatValue(d.Value, indent)
		return endLine
	case *parser.ObjectNode:
		fmt.Fprintf(f.writer, "%s%s = {", indentStr, d.Name)
		if f.hasTrailingComment(d.Position.Line) {
			fmt.Fprintf(f.writer, " %s", f.popComment())
		}
		fmt.Fprintln(f.writer)

		f.formatSubnode(d.Subnode, indent+1)

		fmt.Fprintf(f.writer, "%s}", indentStr)
		return d.Subnode.EndPosition.Line
	case *parser.VariableDefinition:
		macro := "#var"
		if d.IsConst {
			macro = "#let"
		}
		fmt.Fprintf(f.writer, "%s%s %s: %s", indentStr, macro, d.Name, d.TypeExpr)
		if d.DefaultValue != nil {
			fmt.Fprint(f.writer, " = ")
			endLine := f.formatValue(d.DefaultValue, indent)
			return endLine
		}
		return d.Position.Line
	}
	return 0
}

func (f *Formatter) formatSubnode(sub parser.Subnode, indent int) {
	lastLine := sub.Position.Line
	for _, def := range sub.Definitions {
		pos := def.Pos()
		peek := f.peekPosition()
		if peek.Line > 0 && peek.Line < pos.Line && peek.Line > lastLine {
			pos = peek
		}

		if lastLine > 0 && pos.Line > lastLine+1 {
			fmt.Fprintln(f.writer)
		}

		f.flushCommentsBefore(def.Pos(), indent, true) // Stick to definition
		lastLine = f.formatDefinition(def, indent)
		if f.hasTrailingComment(lastLine) {
			fmt.Fprintf(f.writer, " %s", f.popComment())
		}
		fmt.Fprintln(f.writer)
	}
	f.flushCommentsBefore(sub.EndPosition, indent, false)
}

func (f *Formatter) formatValue(val parser.Value, indent int) int {
	switch v := val.(type) {
	case *parser.StringValue:
		if v.Quoted {
			fmt.Fprintf(f.writer, "\"%s\"", v.Value)
		} else {
			// Should strictly parse unquoted as ReferenceValue or identifiers, but fallback here
			fmt.Fprint(f.writer, v.Value)
		}
		return v.Position.Line
	case *parser.IntValue:
		fmt.Fprint(f.writer, v.Raw)
		return v.Position.Line
	case *parser.FloatValue:
		fmt.Fprint(f.writer, v.Raw)
		return v.Position.Line
	case *parser.BoolValue:
		fmt.Fprintf(f.writer, "%v", v.Value)
		return v.Position.Line
	case *parser.ReferenceValue:
		fmt.Fprint(f.writer, v.Value)
		return v.Position.Line
	case *parser.VariableReferenceValue:
		fmt.Fprint(f.writer, v.Name)
		return v.Position.Line
	case *parser.BinaryExpression:
		f.formatValue(v.Left, indent)
		fmt.Fprintf(f.writer, " %s ", v.Operator.Value)
		f.formatValue(v.Right, indent)
		return v.Position.Line
	case *parser.UnaryExpression:
		fmt.Fprint(f.writer, v.Operator.Value)
		f.formatValue(v.Right, indent)
		return v.Position.Line
	case *parser.ArrayValue:
		return f.formatArray(v, indent)
	default:
		return 0
	}
}

func (f *Formatter) formatArray(v *parser.ArrayValue, indent int) int {
	// Heuristic: if array spans multiple lines in source, preserve multiline structure
	// Or if formatted inline length > 120 chars
	multiline := false
	if v.EndPosition.Line > v.Position.Line {
		multiline = true
	}

	if !multiline {
		// Try formatting inline to check length
		// We need a dummy writer to measure length
		// But recursive formatValue writes to f.writer.
		// We can use a temporary buffer.
		// But f.writer is io.Writer. We can swap it.
		originalWriter := f.writer
		var buf strings.Builder
		f.writer = &buf
		f.formatArrayInline(v, indent)
		f.writer = originalWriter

		if buf.Len() > 120 { // Simplified check, assumes start of line is handled elsewhere or is negligible for long arrays
			multiline = true
		} else {
			fmt.Fprint(f.writer, buf.String())
			return v.Position.Line
		}
	}

	if multiline {
		fmt.Fprintln(f.writer, "{")
		indentStr := strings.Repeat("  ", indent+1)
		for _, e := range v.Elements {
			fmt.Fprint(f.writer, indentStr)
			f.formatValue(e, indent+1)
			fmt.Fprintln(f.writer)
		}
		fmt.Fprintf(f.writer, "%s}", strings.Repeat("  ", indent))
		if v.EndPosition.Line > 0 {
			return v.EndPosition.Line
		}
		if len(v.Elements) > 0 {
			return v.Elements[len(v.Elements)-1].Pos().Line
		}
		return v.Position.Line
	}

	return v.Position.Line
}

func (f *Formatter) formatArrayInline(v *parser.ArrayValue, indent int) {
	fmt.Fprint(f.writer, "{ ")
	for i, e := range v.Elements {
		if i > 0 {
			fmt.Fprint(f.writer, " ")
		}
		f.formatValue(e, indent)
	}
	fmt.Fprint(f.writer, " }")
}

func (f *Formatter) flushCommentsBefore(pos parser.Position, indent int, stick bool) {
	indentStr := strings.Repeat("  ", indent)
	for f.cursor < len(f.insertables) {
		c := f.insertables[f.cursor]
		if c.Position.Line < pos.Line || (c.Position.Line == pos.Line && c.Position.Column < pos.Column) {
			fmt.Fprintf(f.writer, "%s%s\n", indentStr, c.Text)
			f.cursor++
		} else {
			break
		}
	}
	// If stick is true, we don't print extra newline.
	// The caller will print the definition immediately after this function returns.
	// If stick is false (e.g. end of block comments), we act normally.
	// But actually, the previous implementation didn't print extra newlines between comments and code
	// explicitly, it relied on the loop in formatConfig/formatSubnode to print newline AFTER definition.
	// So comments naturally sat on top.
	// The issue is if there WAS a blank line in source, we ignore it and squash. This implements "stick".
}

func (f *Formatter) flushRemainingComments(indent int) {
	indentStr := strings.Repeat("  ", indent)
	for f.cursor < len(f.insertables) {
		c := f.insertables[f.cursor]
		fmt.Fprintf(f.writer, "%s%s\n", indentStr, c.Text)
		f.cursor++
	}
}

func (f *Formatter) hasTrailingComment(line int) bool {
	if f.cursor >= len(f.insertables) {
		return false
	}
	c := f.insertables[f.cursor]
	return c.Position.Line == line
}

func (f *Formatter) popComment() string {
	if f.cursor >= len(f.insertables) {
		return ""
	}
	c := f.insertables[f.cursor]
	f.cursor++
	return c.Text
}

func (f *Formatter) peekPosition() parser.Position {
	if f.cursor < len(f.insertables) {
		return f.insertables[f.cursor].Position
	}
	return parser.Position{}
}
