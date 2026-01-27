package integration

import (
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/parser"
)

func TestASTCoverage(t *testing.T) {
	pos := parser.Position{Line: 1, Column: 1}

	var n parser.Node
	var d parser.Definition
	var v parser.Value

	// Field
	f := &parser.Field{Position: pos}
	n = f
	d = f
	if n.Pos() != pos {
		t.Error("Field.Pos failed")
	}
	_ = d

	// ObjectNode
	o := &parser.ObjectNode{Position: pos}
	n = o
	d = o
	if n.Pos() != pos {
		t.Error("ObjectNode.Pos failed")
	}

	// StringValue
	sv := &parser.StringValue{Position: pos}
	n = sv
	v = sv
	if n.Pos() != pos {
		t.Error("StringValue.Pos failed")
	}
	_ = v

	// IntValue
	iv := &parser.IntValue{Position: pos}
	n = iv
	v = iv
	if n.Pos() != pos {
		t.Error("IntValue.Pos failed")
	}

	// FloatValue
	fv := &parser.FloatValue{Position: pos}
	n = fv
	v = fv
	if n.Pos() != pos {
		t.Error("FloatValue.Pos failed")
	}

	// BoolValue
	bv := &parser.BoolValue{Position: pos}
	n = bv
	v = bv
	if n.Pos() != pos {
		t.Error("BoolValue.Pos failed")
	}

	// ReferenceValue
	rv := &parser.ReferenceValue{Position: pos}
	n = rv
	v = rv
	if n.Pos() != pos {
		t.Error("ReferenceValue.Pos failed")
	}

	// ArrayValue
	av := &parser.ArrayValue{Position: pos}
	n = av
	v = av
	if n.Pos() != pos {
		t.Error("ArrayValue.Pos failed")
	}

	// Package
	pkg := &parser.Package{Position: pos}
	n = pkg
	if n.Pos() != pos {
		t.Error("Package.Pos failed")
	}

	// Subnode
	sn := &parser.Subnode{Position: pos}
	n = sn
	if n.Pos() != pos {
		t.Error("Subnode.Pos failed")
	}

	// Comment
	cmt := &parser.Comment{Position: pos}
	n = cmt
	if n.Pos() != pos {
		t.Error("Comment.Pos failed")
	}

	// Pragma
	prg := &parser.Pragma{Position: pos}
	n = prg
	if n.Pos() != pos {
		t.Error("Pragma.Pos failed")
	}
}
