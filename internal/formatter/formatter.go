package formatter

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/marte-dev/marte-dev-tools/internal/parser"
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
	if strings.HasPrefix(text, "//!") {
		if len(text) > 3 && text[3] != ' ' {
			return "//! " + text[3:]
		}
	} else if strings.HasPrefix(text, "//#") {
		if len(text) > 3 && text[3] != ' ' {
			return "//# " + text[3:]
		}
	} else if strings.HasPrefix(text, "//") {
		if len(text) > 2 && text[2] != ' ' && text[2] != '#' && text[2] != '!' {
			return "// " + text[2:]
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
	}
	return 0
}

func (f *Formatter) formatSubnode(sub parser.Subnode, indent int) {
	for _, def := range sub.Definitions {
		f.flushCommentsBefore(def.Pos(), indent, true) // Stick to definition
		lastLine := f.formatDefinition(def, indent)
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
	case *parser.ArrayValue:
		fmt.Fprint(f.writer, "{ ")
		for i, e := range v.Elements {
			if i > 0 {
				fmt.Fprint(f.writer, " ")
			}
			f.formatValue(e, indent)
		}
		fmt.Fprint(f.writer, " }")
		if v.EndPosition.Line > 0 {
			return v.EndPosition.Line
		}
		// Fallback if EndPosition not set (shouldn't happen with new parser)
		if len(v.Elements) > 0 {
			return v.Elements[len(v.Elements)-1].Pos().Line
		}
		return v.Position.Line
	default:
		return 0
	}
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
