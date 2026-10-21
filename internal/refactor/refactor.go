// Package refactor rewrites configurations to the modern syntax:
// IOGAM copy GAMs become `copy` groups, and signal declarations are
// compacted into the shorthand forms.
package refactor

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/marte-community/marte-dev-tools/internal/parser"
)

// SignalType describes a DataSource signal declaration.
type SignalType struct {
	Type string
	Dim  int
}

// Options selects which rewrites run (both default to true).
type Options struct {
	CompactSignals bool
	CopyGroups     bool
	// Pragmas maps a 1-based line number to the `//!` pragmas written above
	// that line, so converted copy items keep their suppressions.
	Pragmas map[int][]string
}

// Result reports what was rewritten.
type Result struct {
	Signals    int // signal declarations compacted
	CopyGroups int // IOGAMs replaced by copy groups
	Warnings   []string
}

// SourcePragmas maps each 1-based line to the `//!` pragmas written directly
// above it, so converted items keep their diagnostic suppressions.
func SourcePragmas(src string) map[int][]string {
	out := map[int][]string{}
	var pending []string
	for i, line := range strings.Split(src, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "//!") {
			txt := strings.TrimSpace(strings.TrimPrefix(t, "//!"))
			if strings.HasPrefix(txt, "allow(") || strings.HasPrefix(txt, "ignore(") {
				pending = nil
				continue
			}
			if txt != "" {
				pending = append(pending, txt)
			}
			continue
		}
		if t == "" || strings.HasPrefix(t, "//") {
			continue
		}
		if len(pending) > 0 {
			out[i+1] = pending
			pending = nil
		}
	}
	return out
}

// CollectTypes records the declared type of every DataSource signal across
// the given configurations. Keys are "DataSource|Signal".
func CollectTypes(configs []*parser.Configuration) map[string]SignalType {
	out := map[string]SignalType{}
	for _, cfg := range configs {
		walk(defsOf(cfg), func(def parser.Definition) {
			obj, ok := def.(*parser.ObjectNode)
			if !ok {
				return
			}
			ds := defName(obj)
			for _, sub := range obj.Subnode.Definitions {
				sigs, ok := sub.(*parser.ObjectNode)
				if !ok || defName(sigs) != "Signals" {
					continue
				}
				for _, sd := range sigs.Subnode.Definitions {
					switch e := sd.(type) {
					case *parser.ObjectNode:
						var typ string
						var dim int
						for _, fd := range e.Subnode.Definitions {
							f, ok := fd.(*parser.Field)
							if !ok {
								continue
							}
							switch f.Name {
							case "Type":
								typ = fieldText(f.Value)
							case "NumberOfElements":
								if v, err := strconv.Atoi(fieldText(f.Value)); err == nil {
									dim = v
								}
							}
						}
						if typ != "" {
							out[ds+"|"+defName(e)] = SignalType{Type: typ, Dim: dim}
						}
					case *parser.SignalShorthand:
						if e.Type != "" {
							dim := 0
							if e.NumElements != nil {
								if v, err := strconv.Atoi(fieldText(e.NumElements)); err == nil {
									dim = v
								}
							}
							out[ds+"|"+e.SignalName] = SignalType{Type: e.Type, Dim: dim}
						}
					}
				}
			}
		})
	}
	return out
}

// Config rewrites one configuration in place.
func Config(cfg *parser.Configuration, types map[string]SignalType, opts Options) Result {
	var res Result
	if opts.CopyGroups {
		res.CopyGroups = convertIOGAMs(defsOf(cfg), types, opts.Pragmas, &res.Warnings)
	}
	if opts.CompactSignals {
		res.Signals = compactDefinitions(defsOf(cfg))
	}
	return res
}

func defsOf(cfg *parser.Configuration) []parser.Definition {
	if cfg == nil {
		return nil
	}
	return cfg.Definitions
}

// --- signal compaction ---------------------------------------------------

// compactDefinitions rewrites signal entries inside Signals / InputSignals /
// OutputSignals containers and recurses everywhere else.
func compactDefinitions(defs []parser.Definition) int {
	n := 0
	for i, def := range defs {
		switch d := def.(type) {
		case *parser.ObjectNode:
			name := defName(d)
			switch name {
			case "Signals":
				n += compactContainer(d, false)
			case "InputSignals", "OutputSignals":
				n += compactContainer(d, true)
			}
			n += compactDefinitions(d.Subnode.Definitions)
		case *parser.IfBlock:
			n += compactDefinitions(d.Then)
			for _, ei := range d.ElseIf {
				n += compactDefinitions(ei.Body)
			}
			n += compactDefinitions(d.Else)
		case *parser.ForeachBlock:
			n += compactDefinitions(d.Body)
		case *parser.TemplateDefinition:
			n += compactDefinitions(d.Body)
		case *parser.GamDefinition:
			n += compactDefinitions(d.Body)
			_ = i
		case *parser.WithBlock:
			n += compactDefinitions(d.Body)
		}
	}
	return n
}

// compactContainer turns object-form signal entries into shorthand nodes.
func compactContainer(container *parser.ObjectNode, gamStyle bool) int {
	n := 0
	for i, def := range container.Subnode.Definitions {
		obj, ok := def.(*parser.ObjectNode)
		if !ok {
			continue
		}
		sh, ok := compactEntry(obj, gamStyle)
		if !ok {
			continue
		}
		container.Subnode.Definitions[i] = sh
		n++
	}
	return n
}

// compactEntry converts one signal object into a SignalShorthand.
func compactEntry(obj *parser.ObjectNode, gamStyle bool) (*parser.SignalShorthand, bool) {
	name := defName(obj)
	if name == "" {
		return nil, false
	}
	sh := &parser.SignalShorthand{Position: obj.Position, EndPosition: obj.Subnode.EndPosition, SignalName: name}
	var extra []parser.Definition
	ds := ""
	alias := ""
	for _, def := range obj.Subnode.Definitions {
		f, ok := def.(*parser.Field)
		if !ok {
			extra = append(extra, def)
			continue
		}
		switch f.Name {
		case "DataSource":
			ds = fieldText(f.Value)
			if !gamStyle {
				extra = append(extra, def)
			}
		case "Type":
			sh.Type = fieldText(f.Value)
		case "NumberOfElements":
			sh.NumElements = f.Value
		case "Alias":
			alias = fieldText(f.Value)
			if !gamStyle {
				extra = append(extra, def)
			}
		default:
			extra = append(extra, def)
		}
	}
	if gamStyle {
		if ds == "" {
			return nil, false
		}
		sh.DataSource = ds
		if alias != "" {
			sh.SignalName = alias
			if alias != name {
				sh.AliasName = name
			}
		}
	} else if sh.Type == "" {
		return nil, false // a DataSource signal needs a type to use the shorthand
	}
	if len(extra) > 0 {
		sh.HasExtraFields = true
		sh.ExtraFields = parser.Subnode{Position: obj.Position, EndPosition: obj.Subnode.EndPosition, Definitions: extra}
	}
	return sh, true
}

// --- IOGAM -> copy group -------------------------------------------------

type entry struct {
	ds     string
	signal string
	alias  string // local entry name ("" = signal name)
	typ    string
	dim    int
	opts   []parser.CopyOption
	size   int // bytes, 0 = unknown
	sizeOK bool
	pos    parser.Position
}

// convertIOGAMs replaces IOGAM objects with named copy groups, recursively.
func convertIOGAMs(defs []parser.Definition, types map[string]SignalType, pragmas map[int][]string, warnings *[]string) int {
	n := 0
	for i, def := range defs {
		switch d := def.(type) {
		case *parser.ObjectNode:
			if defName(d) == "Class" {
				continue
			}
			if classOf(d) == "IOGAM" {
				group, ok, warn := iogamToCopyGroup(d, types, pragmas)
				if warn != "" {
					*warnings = append(*warnings, warn)
				}
				if ok {
					defs[i] = group
					n++
					continue
				}
			}
			n += convertIOGAMs(d.Subnode.Definitions, types, pragmas, warnings)
		case *parser.IfBlock:
			n += convertIOGAMs(d.Then, types, pragmas, warnings)
			for _, ei := range d.ElseIf {
				n += convertIOGAMs(ei.Body, types, pragmas, warnings)
			}
			n += convertIOGAMs(d.Else, types, pragmas, warnings)
		case *parser.ForeachBlock:
			n += convertIOGAMs(d.Body, types, pragmas, warnings)
		case *parser.TemplateDefinition:
			n += convertIOGAMs(d.Body, types, pragmas, warnings)
		case *parser.GamDefinition:
			n += convertIOGAMs(d.Body, types, pragmas, warnings)
		case *parser.WithBlock:
			n += convertIOGAMs(d.Body, types, pragmas, warnings)
		}
	}
	return n
}

// iogamToCopyGroup builds the copy group for one IOGAM object.
func iogamToCopyGroup(obj *parser.ObjectNode, types map[string]SignalType, pragmas map[int][]string) (*parser.CopyGroup, bool, string) {
	name := defName(obj)
	// Only class + signal lists may be present, otherwise converting would
	// drop configuration.
	var ins, outs *parser.ObjectNode
	for _, def := range obj.Subnode.Definitions {
		switch f := def.(type) {
		case *parser.Field:
			if f.Name != "Class" {
				return nil, false, fmt.Sprintf("%s: IOGAM has extra field %q; left unchanged", name, f.Name)
			}
		case *parser.ObjectNode:
			switch defName(f) {
			case "InputSignals":
				ins = f
			case "OutputSignals":
				outs = f
			}
		}
	}
	if ins == nil || outs == nil {
		return nil, false, fmt.Sprintf("%s: IOGAM without both signal lists; left unchanged", name)
	}

	srcs := collectEntries(ins, types)
	dsts := collectEntries(outs, types)
	if srcs == nil || dsts == nil {
		return nil, false, fmt.Sprintf("%s: unsupported signal entry form; left unchanged", name)
	}
	group := &parser.CopyGroup{Position: obj.Position, EndPosition: obj.Subnode.EndPosition, Name: name}

	if len(srcs) == len(dsts) {
		// Same counts: keeping the declaration order preserves the byte
		// mapping exactly, no size knowledge required.
		for i := range srcs {
			group.Items = append(group.Items, parser.CopyItem{
				Position:   srcs[i].pos,
				Src:        endpointOf(srcs[i]),
				Dst:        endpointOf(dsts[i]),
				SrcAlias:   srcs[i].alias,
				DstAlias:   dsts[i].alias,
				SrcOptions: entryOptions(srcs[i]),
				DstOptions: entryOptions(dsts[i]),
			})
		}
		return group, true, ""
	}

	// Different counts (packing): require byte sizes.
	inSize, outSize := 0, 0
	for _, e := range srcs {
		if !e.sizeOK {
			return nil, false, fmt.Sprintf("%s: cannot determine byte size of %q (grouped conversion needs Type); left unchanged", name, e.signal)
		}
		inSize += e.size
	}
	for _, e := range dsts {
		if !e.sizeOK {
			return nil, false, fmt.Sprintf("%s: cannot determine byte size of %q (grouped conversion needs Type); left unchanged", name, e.signal)
		}
		outSize += e.size
	}
	if inSize != outSize {
		return nil, false, fmt.Sprintf("%s: input (%d bytes) and output (%d bytes) sizes differ; left unchanged", name, inSize, outSize)
	}
	i, j := 0, 0
	for i < len(srcs) && j < len(dsts) {
		if srcs[i].size == dsts[j].size {
			item := pairItem(srcs[i], dsts[j])
			item.Pragmas = mergePragmas(pragmas, srcs[i], dsts[j])
			group.Items = append(group.Items, item)
			i++
			j++
			continue
		}
		if srcs[i].size > dsts[j].size {
			acc := 0
			start := j
			for j < len(dsts) && acc < srcs[i].size {
				acc += dsts[j].size
				j++
			}
			if acc != srcs[i].size {
				return nil, false, fmt.Sprintf("%s: signal boundaries do not align; left unchanged", name)
			}
			item := groupItem([]entry{srcs[i]}, dsts[start:j])
			item.Pragmas = mergePragmas(pragmas, srcs[i])
			for _, d := range dsts[start:j] {
				item.Pragmas = append(item.Pragmas, pragmas[d.pos.Line]...)
			}
			group.Items = append(group.Items, item)
			i++
			continue
		}
		acc := 0
		start := i
		for i < len(srcs) && acc < dsts[j].size {
			acc += srcs[i].size
			i++
		}
		if acc != dsts[j].size {
			return nil, false, fmt.Sprintf("%s: signal boundaries do not align; left unchanged", name)
		}
		item := groupItem(srcs[start:i], []entry{dsts[j]})
		for _, s := range srcs[start:i] {
			item.Pragmas = append(item.Pragmas, pragmas[s.pos.Line]...)
		}
		item.Pragmas = append(item.Pragmas, mergePragmas(pragmas, dsts[j])...)
		group.Items = append(group.Items, item)
		j++
	}
	if i != len(srcs) || j != len(dsts) {
		return nil, false, fmt.Sprintf("%s: signal boundaries do not align; left unchanged", name)
	}
	return group, true, ""
}

// mergePragmas returns the pragmas attached to the given entry lines.
func mergePragmas(pragmas map[int][]string, entries ...entry) []string {
	var out []string
	for _, e := range entries {
		out = append(out, pragmas[e.pos.Line]...)
	}
	return out
}

func pairItem(src, dst entry) parser.CopyItem {
	return parser.CopyItem{
		Position:   src.pos,
		Src:        endpointOf(src),
		Dst:        endpointOf(dst),
		SrcAlias:   src.alias,
		DstAlias:   dst.alias,
		SrcOptions: entryOptions(src),
		DstOptions: entryOptions(dst),
	}
}

func groupItem(srcs, dsts []entry) parser.CopyItem {
	item := parser.CopyItem{Position: srcs[0].pos}
	for _, e := range srcs {
		item.SrcGroup = append(item.SrcGroup, parser.CopyMember{Position: e.pos, Endpoint: endpointOf(e), Alias: e.alias, Options: entryOptions(e)})
	}
	for _, e := range dsts {
		item.DstGroup = append(item.DstGroup, parser.CopyMember{Position: e.pos, Endpoint: endpointOf(e), Alias: e.alias, Options: entryOptions(e)})
	}
	return item
}

func endpointOf(e entry) parser.CopyEndpoint {
	return parser.CopyEndpoint{Position: e.pos, IsDS: true, Object: e.ds, Signal: e.signal}
}

// entryOptions returns the options needed to reproduce the entry: Type,
// NumberOfElements and all extra fields.
func entryOptions(e entry) []parser.CopyOption {
	var opts []parser.CopyOption
	if e.typ != "" {
		opts = append(opts, parser.CopyOption{Position: e.pos, Name: "Type", Value: &parser.ReferenceValue{Position: e.pos, Value: e.typ}})
	}
	if e.dim > 0 {
		opts = append(opts, parser.CopyOption{Position: e.pos, Name: "NumberOfElements", Value: &parser.IntValue{Position: e.pos, Value: int64(e.dim), Raw: strconv.Itoa(e.dim)}})
	}
	return append(opts, e.opts...)
}

// collectEntries reads the signal entries of one IOGAM list in the order the
// builder emits them: object-form entries first, then shorthand entries (the
// writer prints those last). Reproducing that order keeps the runtime memory
// mapping identical.
func collectEntries(container *parser.ObjectNode, types map[string]SignalType) []entry {
	var objects, shorthands []entry
	for _, def := range container.Subnode.Definitions {
		switch d := def.(type) {
		case *parser.ObjectNode:
			e, ok := entryFromObject(d, types)
			if !ok {
				return nil
			}
			objects = append(objects, e)
		case *parser.SignalShorthand:
			shorthands = append(shorthands, entryFromShorthand(d, types))
		default:
			return nil
		}
	}
	return append(objects, shorthands...)
}

func entryFromObject(obj *parser.ObjectNode, types map[string]SignalType) (entry, bool) {
	e := entry{pos: obj.Position}
	e.alias = defName(obj)
	for _, def := range obj.Subnode.Definitions {
		f, ok := def.(*parser.Field)
		if !ok {
			return entry{}, false
		}
		switch f.Name {
		case "DataSource":
			e.ds = fieldText(f.Value)
		case "Alias":
			e.signal = fieldText(f.Value)
		case "Type":
			e.typ = fieldText(f.Value)
		case "NumberOfElements":
			if v, err := strconv.Atoi(fieldText(f.Value)); err == nil {
				e.dim = v
			}
		default:
			e.opts = append(e.opts, parser.CopyOption{Position: f.Position, Name: f.Name, Value: f.Value})
		}
	}
	if e.ds == "" {
		return entry{}, false
	}
	if e.signal == "" {
		e.signal = e.alias
	}
	if e.alias == e.signal {
		e.alias = ""
	}
	finishEntry(&e, types)
	return e, true
}

func entryFromShorthand(sh *parser.SignalShorthand, types map[string]SignalType) entry {
	e := entry{pos: sh.Position, ds: sh.DataSource, signal: sh.SignalName, typ: sh.Type}
	e.alias = sh.AliasName
	if e.alias == "" {
		e.alias = sh.SignalName
	}
	if e.alias == e.signal {
		e.alias = ""
	}
	if sh.NumElements != nil {
		if v, err := strconv.Atoi(fieldText(sh.NumElements)); err == nil {
			e.dim = v
		}
	}
	if sh.HasExtraFields {
		for _, def := range sh.ExtraFields.Definitions {
			if f, ok := def.(*parser.Field); ok {
				e.opts = append(e.opts, parser.CopyOption{Position: f.Position, Name: f.Name, Value: f.Value})
			}
		}
	}
	finishEntry(&e, types)
	return e
}

func finishEntry(e *entry, types map[string]SignalType) {
	// Sizes may come from the DataSource declaration, but only types and
	// element counts declared on the entry itself are emitted again.
	typ, dim := e.typ, e.dim
	if typ == "" {
		if t, ok := types[e.ds+"|"+e.signal]; ok {
			typ, dim = t.Type, t.Dim
		}
	}
	if base := typeSize(typ); base > 0 {
		if dim == 0 {
			dim = 1
		}
		e.size = base * dim
		e.sizeOK = true
	}
}

func typeSize(typ string) int {
	switch strings.TrimSpace(typ) {
	case "uint8", "int8", "char8", "bool":
		return 1
	case "uint16", "int16":
		return 2
	case "uint32", "int32", "float32":
		return 4
	case "uint64", "int64", "float64":
		return 8
	}
	return 0
}

// --- helpers -------------------------------------------------------------

func defName(obj *parser.ObjectNode) string {
	s := fieldText(obj.Name)
	return strings.TrimPrefix(strings.TrimPrefix(s, "+"), "$")
}

func classOf(obj *parser.ObjectNode) string {
	for _, def := range obj.Subnode.Definitions {
		if f, ok := def.(*parser.Field); ok && f.Name == "Class" {
			return fieldText(f.Value)
		}
	}
	return ""
}

func fieldText(v parser.Value) string {
	switch t := v.(type) {
	case *parser.ReferenceValue:
		return t.Value
	case *parser.StringValue:
		return t.Value
	case *parser.IntValue:
		return t.Raw
	case *parser.FloatValue:
		return t.Raw
	case *parser.BoolValue:
		return fmt.Sprintf("%v", t.Value)
	}
	return ""
}

// walk visits every definition recursively (including block bodies).
func walk(defs []parser.Definition, fn func(parser.Definition)) {
	for _, def := range defs {
		fn(def)
		switch d := def.(type) {
		case *parser.ObjectNode:
			walk(d.Subnode.Definitions, fn)
		case *parser.IfBlock:
			walk(d.Then, fn)
			for _, ei := range d.ElseIf {
				walk(ei.Body, fn)
			}
			walk(d.Else, fn)
		case *parser.ForeachBlock:
			walk(d.Body, fn)
		case *parser.TemplateDefinition:
			walk(d.Body, fn)
		case *parser.GamDefinition:
			walk(d.Body, fn)
		case *parser.WithBlock:
			walk(d.Body, fn)
		}
	}
}
