package builder

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
)

// LowerReport summarizes build-time lowering of copy/wiring sugar.
type LowerReport struct {
	Copies    int // copy pairs emitted as IOGAM copies
	Wired     int // `from` entries wired without a copy
	Buffers   int // synthesized GAMDataSource buffers
	Fused     int // GAM pairs fused into one expression GAM
	Redundant int // copy pairs that were already wired
}

// Empty reports whether nothing was lowered.
func (r LowerReport) Empty() bool {
	return r.Copies == 0 && r.Wired == 0 && r.Buffers == 0 && r.Fused == 0 && r.Redundant == 0
}

func (r LowerReport) String() string {
	if r.Copies == 0 && r.Wired == 0 && r.Buffers == 0 && r.Redundant == 0 {
		return "no copy sugar"
	}
	parts := []string{}
	if r.Copies > 0 {
		parts = append(parts, fmt.Sprintf("%d copy pair(s)", r.Copies))
	}
	if r.Wired > 0 {
		parts = append(parts, fmt.Sprintf("%d direct wire(s)", r.Wired))
	}
	if r.Buffers > 0 {
		parts = append(parts, fmt.Sprintf("%d copy buffer(s)", r.Buffers))
	}
	if r.Fused > 0 {
		parts = append(parts, fmt.Sprintf("%d GAM pair(s) fused", r.Fused))
	}
	if r.Redundant > 0 {
		parts = append(parts, fmt.Sprintf("%d redundant copy pair(s) skipped", r.Redundant))
	}
	return strings.Join(parts, ", ")
}

// copyPair is one resolved copy edge.
type copyPair struct {
	srcDS     string
	srcSignal string
	srcAlias  string
	dstDS     string
	dstSignal string
	dstAlias  string
	typ       string
	dim       int
	position  parser.Position
}

// lowerAll lowers every root of the tree (the root node plus isolated files).
func (b *Builder) lowerAll() (LowerReport, error) {
	var total LowerReport
	add := func(r LowerReport) {
		total.Copies += r.Copies
		total.Wired += r.Wired
		total.Buffers += r.Buffers
		total.Fused += r.Fused
		total.Redundant += r.Redundant
	}
	if b.tree == nil {
		return total, nil
	}
	if b.tree.Root != nil {
		r, err := b.lowerSugar(b.tree.Root)
		if err != nil {
			return total, err
		}
		add(r)
	}
	for _, iso := range b.tree.IsolatedFiles {
		r, err := b.lowerSugar(iso)
		if err != nil {
			return total, err
		}
		add(r)
	}
	return total, nil
}

// Lower expands copy/wiring sugar on a tree without writing it. Used by
// `mdt check` so sugar-backed configurations validate like lowered ones.
func Lower(tree *index.ProjectTree) (LowerReport, error) {
	b := &Builder{tree: tree}
	return b.lowerAll()
}

// lowerSugar rewrites copy groups and `from` wiring into ordinary MARTe
// configuration (signal declarations, IOGAMs and, when needed,
// GAMDataSource buffers). It runs after validation and before writing.
func (b *Builder) lowerSugar(root *index.ProjectNode) (LowerReport, error) {
	var rep LowerReport
	if root == nil || b.tree == nil {
		return rep, nil
	}
	l := &lowerer{
		b:      b,
		tree:   b.tree,
		byName: map[string]*index.ProjectNode{},
		bufSeq: 0,
		seq:    0,
	}
	l.indexNames(root)

	// 1. Direct `from` wiring in signal lists (no copy needed when the
	// producer's DataSource is used directly).
	if err := l.lowerFromFields(root, &rep); err != nil {
		return rep, err
	}

	// 2. Copy groups (named definitions and inline elements).
	if err := l.lowerCopyGroups(root, &rep); err != nil {
		return rep, err
	}
	l.fuseGAMs(root, &rep)
	rep.Buffers += l.buffers
	return rep, nil
}

type lowerer struct {
	b       *Builder
	tree    *index.ProjectTree
	byName  map[string]*index.ProjectNode
	bufSeq  int
	seq     int
	buffers int
}

func (l *lowerer) indexNames(root *index.ProjectNode) {
	var walk func(n *index.ProjectNode)
	walk = func(n *index.ProjectNode) {
		if n.RealName != "" {
			l.byName[index.NormalizeName(n.RealName)] = n
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(root)
}

// --- from wiring ---------------------------------------------------------

func (l *lowerer) lowerFromFields(root *index.ProjectNode, rep *LowerReport) error {
	var walk func(n *index.ProjectNode) error
	walk = func(n *index.ProjectNode) error {
		for _, frag := range n.Fragments {
			if !l.active(frag) {
				continue
			}
			for i, def := range frag.Definitions {
				field, ok := def.(*parser.Field)
				if !ok {
					continue
				}
				fv, ok := field.Value.(*parser.FromValue)
				if !ok {
					continue
				}
				// The signal entry lives under the container (InputSignals /
				// OutputSignals) named by the parent node.
				if n.Parent == nil {
					return fmt.Errorf("%d:%d: `from` used outside a signal list",
						field.Position.Line, field.Position.Column)
				}
				srcs, err := l.resolveSourceList(fv.Src)
				if err == nil && len(srcs) != 1 {
					err = fmt.Errorf("expected a single source signal")
				}
				var src signalRef
				if err == nil {
					src = srcs[0]
				}
				if err != nil {
					return fmt.Errorf("%d:%d: %v", fv.Position.Line, fv.Position.Column, err)
				}
				oldName := field.Name
				sigName := oldName
				if fv.Alias != "" {
					sigName = fv.Alias
				}
				// Replace the FromValue with a plain signal body.
				sub := parser.Subnode{Position: fv.Position, EndPosition: fv.End()}
				dsVal := &parser.ReferenceValue{Position: fv.Position, Value: src.ds}
				sub.Definitions = append(sub.Definitions, &parser.Field{Position: fv.Position, Name: "DataSource", Value: dsVal})
				if src.signal != sigName {
					aliasVal := &parser.StringValue{Position: fv.Position, Value: src.signal, Quoted: true}
					sub.Definitions = append(sub.Definitions, &parser.Field{Position: fv.Position, Name: "Alias", Value: aliasVal})
				}
				typ, dim := src.typ, src.dim
				if fv.Type != "" {
					typ, dim = fv.Type, fv.Dim
				}
				if typ != "" {
					sub.Definitions = append(sub.Definitions, &parser.Field{Position: fv.Position, Name: "Type", Value: &parser.ReferenceValue{Position: fv.Position, Value: typ}})
				}
				if dim > 0 {
					sub.Definitions = append(sub.Definitions, &parser.Field{Position: fv.Position, Name: "NumberOfElements", Value: &parser.IntValue{Position: fv.Position, Value: int64(dim), Raw: strconv.Itoa(dim)}})
				}
				frag.Definitions[i] = &parser.ObjectNode{
					Position: field.Position,
					Name:     &parser.ReferenceValue{Position: field.Position, Value: sigName},
					Subnode:  sub,
				}
				// Keep the indexed child in sync: re-key and replace its
				// body with the resolved signal declaration.
				if sigNode, ok := n.Children[index.NormalizeName(oldName)]; ok {
					if sigName != oldName {
						delete(n.Children, index.NormalizeName(oldName))
						sigNode.Name = index.NormalizeName(sigName)
						sigNode.RealName = sigName
						n.Children[index.NormalizeName(sigName)] = sigNode
					}
					newObj := frag.Definitions[i].(*parser.ObjectNode)
					replaceSignalBody(sigNode, newObj, frag.File)
				}
				rep.Wired++
			}
		}
		for _, c := range n.Children {
			if err := walk(c); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(root)
}

// --- copy groups ---------------------------------------------------------

func (l *lowerer) lowerCopyGroups(root *index.ProjectNode, rep *LowerReport) error {
	type pending struct {
		owner *index.ProjectNode // Functions container owning the group
		frag  *index.Fragment
		index int // definition index inside frag (for body definitions)
		group *parser.CopyGroup
		// set for groups that are elements of a Functions array
		arr      *parser.ArrayValue
		arrIndex int
	}
	var groups []pending

	var walk func(n *index.ProjectNode, inheritedOwner *index.ProjectNode) error
	walk = func(n *index.ProjectNode, inheritedOwner *index.ProjectNode) error {
		owner := inheritedOwner
		if owner == nil && isFunctionsContainer(n) {
			owner = n
		}
		for _, frag := range n.Fragments {
			if !l.active(frag) {
				continue
			}
			for i, def := range frag.Definitions {
				if g, ok := def.(*parser.CopyGroup); ok {
					if g.Name == "" {
						// An anonymous group in an object body has no place
						// in any Functions list, so it could never run.
						return fmt.Errorf("%d:%d: an anonymous copy group must be written inside a Functions list; give it a name and reference it, or move it into the list",
							g.Position.Line, g.Position.Column)
					}
					groups = append(groups, pending{owner: owner, frag: frag, index: i, group: g})
				}
				// Copy groups inside Functions arrays.
				field, ok := def.(*parser.Field)
				if !ok || field.Name != "Functions" {
					continue
				}
				arr, ok := field.Value.(*parser.ArrayValue)
				if !ok {
					continue
				}
				for j, el := range arr.Elements {
					if g, ok := el.(*parser.CopyGroup); ok {
						groups = append(groups, pending{owner: l.functionsOwner(n, arr), arr: arr, arrIndex: j, group: g})
					}
				}
			}
		}
		for _, c := range n.Children {
			if err := walk(c, owner); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(root, nil); err != nil {
		return err
	}

	for _, p := range groups {
		owner := p.owner
		if owner == nil {
			return fmt.Errorf("%d:%d: copy group is not inside a Functions container",
				p.group.Position.Line, p.group.Position.Column)
		}
		pairs, err := l.resolveGroup(p.group, &rep.Redundant)
		if err != nil {
			return fmt.Errorf("%d:%d: %v", p.group.Position.Line, p.group.Position.Column, err)
		}
		if len(pairs) == 0 {
			// Fully redundant: drop the group (and its array entry).
			if p.arr != nil {
				p.arr.Elements = append(p.arr.Elements[:p.arrIndex], p.arr.Elements[p.arrIndex+1:]...)
			} else {
				p.frag.Definitions = append(p.frag.Definitions[:p.index], p.frag.Definitions[p.index+1:]...)
			}
			continue
		}

		name := p.group.Name
		if name == "" {
			l.seq++
			name = fmt.Sprintf("MdtCopy%d", l.seq)
		}
		if err := l.emitCopyGAM(owner, name, pairs, p.group.Position, rep); err != nil {
			return err
		}

		// Replace the group with a reference to the generated GAM.
		ref := &parser.ReferenceValue{Position: p.group.Position, Value: name}
		if p.arr != nil {
			p.arr.Elements[p.arrIndex] = ref
		} else {
			// The group definition is replaced by the synthesized object
			// definition; drop the sugar node itself.
			p.frag.Definitions[p.index] = nil
		}
	}
	// Drop the sugar definitions removed above.
	var walkFilter func(n *index.ProjectNode)
	walkFilter = func(n *index.ProjectNode) {
		for _, frag := range n.Fragments {
			kept := frag.Definitions[:0]
			for _, def := range frag.Definitions {
				if def == nil {
					continue
				}
				kept = append(kept, def)
			}
			frag.Definitions = kept
		}
		for _, c := range n.Children {
			walkFilter(c)
		}
	}
	walkFilter(root)
	return nil
}

// resolveGroup expands a copy group into resolved pairs.
func (l *lowerer) resolveGroup(group *parser.CopyGroup, redundant *int) ([]copyPair, error) {
	var pairs []copyPair
	for _, item := range group.Items {
		srcs, err := l.resolveSourceList(item.Src)
		if err != nil {
			return nil, fmt.Errorf("%d:%d: %v", item.Position.Line, item.Position.Column, err)
		}
		isRange := (item.Src.ByIndex && item.Src.IndexEnd > item.Src.Index) ||
			(item.Dst.ByIndex && item.Dst.IndexEnd > item.Dst.Index)
		if isRange {
			if !item.Dst.ByIndex || !item.Src.ByIndex {
				return nil, fmt.Errorf("%d:%d: a signal range needs index ranges on both sides",
					item.Position.Line, item.Position.Column)
			}
			dsts, err := l.resolveDestinationRange(item.Dst, srcs, item.Type, item.Dim)
			if err != nil {
				return nil, fmt.Errorf("%d:%d: %v", item.Position.Line, item.Position.Column, err)
			}
			if len(srcs) != len(dsts) {
				return nil, fmt.Errorf("%d:%d: copy ranges differ in length (%d vs %d)",
					item.Position.Line, item.Position.Column, len(srcs), len(dsts))
			}
			for i := range srcs {
				if p, ok := l.pair(item, srcs[i], dsts[i]); ok {
					pairs = append(pairs, p)
				} else {
					*redundant++
				}
			}
			continue
		}

		typ, dim := srcs[0].typ, srcs[0].dim
		if item.Type != "" {
			typ, dim = item.Type, item.Dim
		}
		dst, err := l.resolveDestination(item.Dst, item.DstAlias, typ, dim, item.DstAt)
		if err != nil {
			return nil, fmt.Errorf("%d:%d: %v", item.Position.Line, item.Position.Column, err)
		}
		if len(srcs) != 1 {
			return nil, fmt.Errorf("%d:%d: source range against a single destination",
				item.Position.Line, item.Position.Column)
		}
		if p, ok := l.pair(item, srcs[0], dst); ok {
			pairs = append(pairs, p)
		} else {
			*redundant++
		}
	}
	return pairs, nil
}

// pair builds a copyPair, reporting ok=false for a redundant copy.
func (l *lowerer) pair(item parser.CopyItem, src, dst signalRef) (copyPair, bool) {
	p := copyPair{
		srcDS:     src.ds,
		srcSignal: src.signal,
		srcAlias:  item.SrcAlias,
		dstDS:     dst.ds,
		dstSignal: dst.signal,
		dstAlias:  item.DstAlias,
		typ:       src.typ,
		dim:       src.dim,
		position:  item.Position,
	}
	if p.typ == "" {
		p.typ, p.dim = dst.typ, dst.dim
	}
	if item.Type != "" {
		p.typ, p.dim = item.Type, item.Dim
	}
	if p.srcDS == p.dstDS && p.srcSignal == p.dstSignal && p.srcAlias == "" && p.dstAlias == "" {
		return p, false
	}
	return p, true
}

// resolveDestinationRange resolves an index range to existing destination
// slots (ranges never synthesize signals).
func (l *lowerer) resolveDestinationRange(ep parser.CopyEndpoint, srcs []signalRef, typ string, dim int) ([]signalRef, error) {
	if ep.IsDS {
		return nil, fmt.Errorf("a DataSource endpoint cannot be a range")
	}
	gam := l.byName[index.NormalizeName(ep.Object)]
	if gam == nil {
		return nil, fmt.Errorf("unknown GAM %q", ep.Object)
	}
	inputs, ok := gam.Children["InputSignals"]
	if !ok {
		return nil, fmt.Errorf("GAM %q has no InputSignals", ep.Object)
	}
	names := l.orderedSignals(inputs)
	if ep.Index < 1 || ep.IndexEnd > len(names) {
		return nil, fmt.Errorf("signal index range [%d..%d] out of bounds for %q (1..%d)",
			ep.Index, ep.IndexEnd, ep.Object, len(names))
	}
	var refs []signalRef
	for i, name := range names[ep.Index-1 : ep.IndexEnd] {
		ref, err := l.signalRefOf(inputs.Children[name])
		if err != nil {
			return nil, err
		}
		if i < len(srcs) && ref.typ == "" {
			ref.typ, ref.dim = srcs[i].typ, srcs[i].dim
		}
		refs = append(refs, ref)
	}
	return refs, nil
}

// signalRef is a resolved signal location.
type signalRef struct {
	ds     string
	signal string
	typ    string
	dim    int
}

func (l *lowerer) resolveSourceList(ep parser.CopyEndpoint) ([]signalRef, error) {
	if ep.IsDS {
		return []signalRef{{ds: ep.Object, signal: ep.Signal}}, nil
	}
	gam := l.byName[index.NormalizeName(ep.Object)]
	if gam == nil {
		return nil, fmt.Errorf("unknown GAM %q", ep.Object)
	}
	outputs, ok := gam.Children["OutputSignals"]
	if !ok {
		return nil, fmt.Errorf("GAM %q has no OutputSignals", ep.Object)
	}
	if !ep.ByIndex {
		sig, ok := outputs.Children[index.NormalizeName(ep.Signal)]
		if !ok {
			return nil, fmt.Errorf("GAM %q has no output signal %q", ep.Object, ep.Signal)
		}
		ref, err := l.signalRefOf(sig)
		if err != nil {
			return nil, err
		}
		return []signalRef{ref}, nil
	}
	names := l.orderedSignals(outputs)
	start, end := ep.Index, ep.IndexEnd
	if start < 1 || end > len(names) {
		return nil, fmt.Errorf("signal index range [%d..%d] out of bounds for %q (1..%d)",
			start, end, ep.Object, len(names))
	}
	refs := make([]signalRef, 0, end-start+1)
	for _, name := range names[start-1 : end] {
		ref, err := l.signalRefOf(outputs.Children[name])
		if err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, nil
}

func (l *lowerer) resolveDestination(ep parser.CopyEndpoint, alias, typ string, dim, at int) (signalRef, error) {
	if ep.IsDS {
		return signalRef{ds: ep.Object, signal: ep.Signal}, nil
	}
	gam := l.byName[index.NormalizeName(ep.Object)]
	if gam == nil {
		return signalRef{}, fmt.Errorf("unknown GAM %q", ep.Object)
	}
	inputs, ok := gam.Children["InputSignals"]
	if !ok {
		return signalRef{}, fmt.Errorf("GAM %q has no InputSignals", ep.Object)
	}
	if !ep.ByIndex {
		name := index.NormalizeName(ep.Signal)
		if sig, ok := inputs.Children[name]; ok {
			ref, err := l.signalRefOf(sig)
			if err != nil {
				return signalRef{}, err
			}
			if alias != "" && alias != ref.signal {
				if err := l.renameSignalSlot(inputs, ref.signal, alias); err != nil {
					return signalRef{}, err
				}
			}
			return ref, nil
		}
		// Slot not declared: create it in a dedicated buffer DataSource.
		return l.createBufferSignal(gam, ep.Signal, alias, typ, dim, at)
	}
	names := l.orderedSignals(inputs)
	if ep.Index < 1 || ep.Index > len(names) {
		return signalRef{}, fmt.Errorf("signal index %d out of bounds for %q (1..%d)",
			ep.Index, ep.Object, len(names))
	}
	return l.signalRefOf(inputs.Children[names[ep.Index-1]])
}

func (l *lowerer) signalRefOf(sig *index.ProjectNode) (signalRef, error) {
	ds := ""
	typ := ""
	alias := ""
	dim := 0
	for _, frag := range sig.Fragments {
		for _, def := range frag.Definitions {
			f, ok := def.(*parser.Field)
			if !ok {
				continue
			}
			switch f.Name {
			case "DataSource":
				ds = literalText(f.Value)
			case "Alias":
				alias = literalText(f.Value)
			case "Type":
				typ = literalText(f.Value)
			case "NumberOfElements":
				if v, err := strconv.Atoi(literalText(f.Value)); err == nil {
					dim = v
				}
			}
		}
	}
	if ds == "" {
		if def := l.defaultDataSource(); def != "" {
			ds = def
		} else {
			return signalRef{}, fmt.Errorf("signal %q has no DataSource", sig.RealName)
		}
	}
	name := index.NormalizeName(sig.RealName)
	if name == "" {
		name = sig.Name
	}
	if alias != "" {
		name = index.NormalizeName(alias)
	}
	return signalRef{ds: ds, signal: name, typ: typ, dim: dim}, nil
}

func (l *lowerer) orderedSignals(container *index.ProjectNode) []string {
	var names []string
	seen := map[string]bool{}
	for _, frag := range container.Fragments {
		for _, def := range frag.Definitions {
			switch d := def.(type) {
			case *parser.ObjectNode:
				n := index.NormalizeName(literalText(d.Name))
				if n != "" && !seen[n] {
					names = append(names, n)
					seen[n] = true
				}
			case *parser.SignalShorthand:
				n := index.NormalizeName(d.SignalName)
				if d.AliasName != "" {
					n = index.NormalizeName(d.AliasName)
				}
				if n != "" && !seen[n] {
					names = append(names, n)
					seen[n] = true
				}
			}
		}
	}
	return names
}

// renameSignalSlot renames a declared signal slot to alias, keeping the
// original signal name in the Alias field (the standard alias mechanism).
func (l *lowerer) renameSignalSlot(container *index.ProjectNode, oldName, alias string) error {
	sig, ok := container.Children[index.NormalizeName(oldName)]
	if !ok {
		return fmt.Errorf("signal %q not found", oldName)
	}
	delete(container.Children, index.NormalizeName(oldName))
	sig.Name = index.NormalizeName(alias)
	sig.RealName = alias
	container.Children[index.NormalizeName(alias)] = sig

	aliasField := &parser.Field{
		Position: sig.Fragments[0].ObjectPos,
		Name:     "Alias",
		Value:    &parser.StringValue{Position: sig.Fragments[0].ObjectPos, Value: oldName, Quoted: true},
	}
	sig.Fragments[0].Definitions = append(sig.Fragments[0].Definitions, aliasField)
	if sig.Fields == nil {
		sig.Fields = map[string][]index.EvaluatedField{}
	}
	sig.Fields["Alias"] = append(sig.Fields["Alias"], index.EvaluatedField{Raw: aliasField, Value: aliasField.Value, File: sig.Fragments[0].File})

	// Rename the definition so the writer emits the new slot name.
	for _, frag := range container.Fragments {
		for _, def := range frag.Definitions {
			if obj, ok := def.(*parser.ObjectNode); ok && index.NormalizeName(literalText(obj.Name)) == index.NormalizeName(oldName) {
				obj.Name = &parser.ReferenceValue{Position: obj.Position, Value: alias}
			}
		}
	}
	return nil
}

// createBufferSignal ensures a destination GAM has a signal slot backed by
// a synthesized GAMDataSource buffer.
func (l *lowerer) createBufferSignal(gam *index.ProjectNode, slot, alias, typ string, dim, at int) (signalRef, error) {
	inputs, ok := gam.Children["InputSignals"]
	if !ok {
		inputs = l.ensureSignalContainer(gam, "InputSignals")
	}
	bufName := l.bufferFor(gam)
	buf := l.byName[index.NormalizeName(bufName)]
	if buf == nil {
		return signalRef{}, fmt.Errorf("internal: buffer %q not indexed", bufName)
	}
	pos := gam.Fragments[0].ObjectPos
	// Declare the buffer signal with the source's type.
	sigDef := &parser.ObjectNode{
		Position: pos,
		Name:     &parser.ReferenceValue{Position: pos, Value: slot},
		Subnode:  parser.Subnode{},
	}
	if typ != "" {
		sigDef.Subnode.Definitions = append(sigDef.Subnode.Definitions, &parser.Field{
			Position: pos, Name: "Type", Value: &parser.ReferenceValue{Position: pos, Value: typ},
		})
	}
	if dim > 0 {
		sigDef.Subnode.Definitions = append(sigDef.Subnode.Definitions, &parser.Field{
			Position: pos, Name: "NumberOfElements", Value: &parser.IntValue{Position: pos, Value: int64(dim), Raw: strconv.Itoa(dim)},
		})
	}
	l.appendDefinition(l.bufContainer(buf), sigDef, "")

	// Declare the GAM slot pointing at the buffer signal.
	entryName := slot
	if alias != "" {
		entryName = alias
	}
	entry := &parser.ObjectNode{
		Position: pos,
		Name:     &parser.ReferenceValue{Position: pos, Value: entryName},
		Subnode:  parser.Subnode{},
	}
	entry.Subnode.Definitions = append(entry.Subnode.Definitions, &parser.Field{
		Position: pos, Name: "DataSource", Value: &parser.ReferenceValue{Position: pos, Value: bufName},
	})
	if entryName != slot {
		entry.Subnode.Definitions = append(entry.Subnode.Definitions, &parser.Field{
			Position: pos, Name: "Alias", Value: &parser.StringValue{Position: pos, Value: slot, Quoted: true},
		})
	}
	if typ != "" {
		entry.Subnode.Definitions = append(entry.Subnode.Definitions, &parser.Field{
			Position: pos, Name: "Type", Value: &parser.ReferenceValue{Position: pos, Value: typ},
		})
	}
	if dim > 0 {
		entry.Subnode.Definitions = append(entry.Subnode.Definitions, &parser.Field{
			Position: pos, Name: "NumberOfElements", Value: &parser.IntValue{Position: pos, Value: int64(dim), Raw: strconv.Itoa(dim)},
		})
	}
	if at > 0 {
		if err := l.insertSignalDefinition(inputs, entry, at); err != nil {
			return signalRef{}, err
		}
	} else {
		l.appendDefinition(inputs, entry, "")
	}
	return signalRef{ds: bufName, signal: slot, typ: typ, dim: dim}, nil
}

func (l *lowerer) bufferFor(gam *index.ProjectNode) string {
	l.bufSeq++
	l.buffers++
	name := fmt.Sprintf("MdtCopyBuf%d", l.bufSeq)
	data := l.dataContainer(gam)
	obj := &parser.ObjectNode{
		Position: gam.Fragments[0].ObjectPos,
		Name:     &parser.ReferenceValue{Position: gam.Fragments[0].ObjectPos, Value: "+" + name},
	}
	cls := &parser.Field{Position: obj.Position, Name: "Class", Value: &parser.ReferenceValue{Position: obj.Position, Value: "GAMDataSource"}}
	allow := &parser.Field{Position: obj.Position, Name: "AllowNoProducers", Value: &parser.IntValue{Position: obj.Position, Value: 1, Raw: "1"}}
	obj.Subnode.Definitions = append(obj.Subnode.Definitions, cls, allow)
	sigs := &parser.ObjectNode{
		Position: obj.Position,
		Name:     &parser.ReferenceValue{Position: obj.Position, Value: "Signals"},
	}
	obj.Subnode.Definitions = append(obj.Subnode.Definitions, sigs)
	l.appendDefinition(data, obj, "")
	return name
}

func (l *lowerer) ensureSignalContainer(gam *index.ProjectNode, name string) *index.ProjectNode {
	obj := &parser.ObjectNode{
		Position: gam.Fragments[0].ObjectPos,
		Name:     &parser.ReferenceValue{Position: gam.Fragments[0].ObjectPos, Value: name},
	}
	l.appendDefinition(gam, obj, "")
	if child, ok := gam.Children[index.NormalizeName(name)]; ok {
		return child
	}
	return gam
}

// appendDefinition adds def to the (first active) fragment of node and
// rebuilds the index subtree for it.
func (l *lowerer) appendDefinition(node *index.ProjectNode, def parser.Definition, file string) {
	frag := l.activeFragmentOf(node, file)
	if frag == nil {
		return
	}
	frag.Definitions = append(frag.Definitions, def)
	l.indexDefinition(node, def, frag.File)
}

// insertSignalDefinition inserts def so it becomes the at-th (1-based)
// signal declaration of container.
func (l *lowerer) insertSignalDefinition(container *index.ProjectNode, def parser.Definition, at int) error {
	frag := l.activeFragmentOf(container, "")
	var slots []int
	for i, d := range frag.Definitions {
		switch d.(type) {
		case *parser.ObjectNode, *parser.SignalShorthand:
			slots = append(slots, i)
		}
	}
	if at < 1 || at > len(slots)+1 {
		return fmt.Errorf("signal index %d out of range for %q (1..%d)", at, container.RealName, len(slots)+1)
	}
	pos := len(frag.Definitions)
	if at-1 < len(slots) {
		pos = slots[at-1]
	}
	frag.Definitions = append(frag.Definitions, nil)
	copy(frag.Definitions[pos+1:], frag.Definitions[pos:])
	frag.Definitions[pos] = def
	l.indexDefinition(container, def, frag.File)
	return nil
}

func (l *lowerer) activeFragmentOf(node *index.ProjectNode, file string) *index.Fragment {
	for _, frag := range node.Fragments {
		if l.active(frag) && (file == "" || frag.File == file) {
			return frag
		}
	}
	if len(node.Fragments) > 0 {
		return node.Fragments[0]
	}
	frag := &index.Fragment{File: file, IsObject: node.RealName != "", ObjectPos: parser.Position{}}
	node.Fragments = append(node.Fragments, frag)
	if l.b.activeFragments != nil {
		l.b.activeFragments[frag] = true
	}
	return frag
}

// indexDefinition builds the child nodes for a synthesized definition, the
// same shape the index produces for parsed files.
func (l *lowerer) indexDefinition(parent *index.ProjectNode, def parser.Definition, file string) {
	switch d := def.(type) {
	case *parser.ObjectNode:
		name := index.NormalizeName(literalText(d.Name))
		if name == "" {
			return
		}
		child, ok := parent.Children[name]
		if !ok {
			child = &index.ProjectNode{
				Name:      name,
				RealName:  literalText(d.Name),
				Parent:    parent,
				Children:  map[string]*index.ProjectNode{},
				Variables: map[string]index.VariableInfo{},
				Fields:    map[string][]index.EvaluatedField{},
				Metadata:  map[string]string{},
				File:      file,
			}
			if parent.Children == nil {
				parent.Children = map[string]*index.ProjectNode{}
			}
			parent.Children[name] = child
			l.tree.AddToNodeMap(child)
			if _, exists := l.byName[name]; !exists {
				l.byName[name] = child
			}
		}
		frag := &index.Fragment{
			File:        file,
			Definitions: d.Subnode.Definitions,
			IsObject:    true,
			ObjectPos:   d.Position,
			EndPos:      d.Subnode.EndPosition,
			Source:      d,
		}
		child.Fragments = append(child.Fragments, frag)
		if l.b.activeFragments != nil {
			l.b.activeFragments[frag] = true
		}
		for _, sub := range d.Subnode.Definitions {
			l.indexDefinition(child, sub, file)
		}
	case *parser.Field:
		if parent.Fields == nil {
			parent.Fields = map[string][]index.EvaluatedField{}
		}
		parent.Fields[d.Name] = append(parent.Fields[d.Name], index.EvaluatedField{Raw: d, Value: d.Value, File: file})
	}
}

// defaultDataSource returns the Data.DefaultDataSource of the enclosing
// Data container, if declared.
func (l *lowerer) defaultDataSource() string {
	var found string
	l.tree.Walk(func(n *index.ProjectNode) {
		if found != "" || n.Name != "Data" {
			return
		}
		for _, frag := range n.Fragments {
			for _, def := range frag.Definitions {
				f, ok := def.(*parser.Field)
				if ok && f.Name == "DefaultDataSource" {
					found = literalText(f.Value)
				}
			}
		}
	})
	return found
}

func (l *lowerer) dataContainer(from *index.ProjectNode) *index.ProjectNode {
	for n := from; n != nil; n = n.Parent {
		if data, ok := n.Children["Data"]; ok {
			return data
		}
	}
	root := from
	for root.Parent != nil {
		root = root.Parent
	}
	return root
}

func (l *lowerer) bufContainer(buf *index.ProjectNode) *index.ProjectNode {
	if sigs, ok := buf.Children["Signals"]; ok {
		return sigs
	}
	return buf
}

// emitCopyGAM synthesizes the IOGAM object for a resolved group.
func (l *lowerer) emitCopyGAM(owner *index.ProjectNode, name string, pairs []copyPair, pos parser.Position, rep *LowerReport) error {
	obj := &parser.ObjectNode{
		Position: pos,
		Name:     &parser.ReferenceValue{Position: pos, Value: "+" + name},
	}
	obj.Subnode.Definitions = append(obj.Subnode.Definitions, &parser.Field{
		Position: pos, Name: "Class", Value: &parser.ReferenceValue{Position: pos, Value: "IOGAM"},
	})
	inputs := &parser.ObjectNode{Position: pos, Name: &parser.ReferenceValue{Position: pos, Value: "InputSignals"}}
	outputs := &parser.ObjectNode{Position: pos, Name: &parser.ReferenceValue{Position: pos, Value: "OutputSignals"}}

	usedIn := map[string]bool{}
	usedOut := map[string]bool{}
	for _, p := range pairs {
		inName := p.srcAlias
		if inName == "" {
			inName = p.srcSignal
		}
		// A copy group may read the same signal for several destinations;
		// IOGAM signal lists are keyed by name, so give the extra reads an
		// alias instead of colliding.
		baseIn := inName
		for i := 2; usedIn[inName]; i++ {
			inName = fmt.Sprintf("%s%d", baseIn, i)
		}
		usedIn[inName] = true
		in := &parser.ObjectNode{Position: p.position, Name: &parser.ReferenceValue{Position: p.position, Value: inName}}
		in.Subnode.Definitions = append(in.Subnode.Definitions, &parser.Field{
			Position: p.position, Name: "DataSource", Value: &parser.ReferenceValue{Position: p.position, Value: p.srcDS},
		})
		if inName != p.srcSignal {
			in.Subnode.Definitions = append(in.Subnode.Definitions, &parser.Field{
				Position: p.position, Name: "Alias", Value: &parser.StringValue{Position: p.position, Value: p.srcSignal, Quoted: true},
			})
		}
		inputs.Subnode.Definitions = append(inputs.Subnode.Definitions, in)

		outName := p.dstAlias
		if outName == "" {
			outName = p.dstSignal
		}
		baseOut := outName
		for i := 2; usedOut[outName]; i++ {
			outName = fmt.Sprintf("%s%d", baseOut, i)
		}
		usedOut[outName] = true
		out := &parser.ObjectNode{Position: p.position, Name: &parser.ReferenceValue{Position: p.position, Value: outName}}
		out.Subnode.Definitions = append(out.Subnode.Definitions, &parser.Field{
			Position: p.position, Name: "DataSource", Value: &parser.ReferenceValue{Position: p.position, Value: p.dstDS},
		})
		if outName != p.dstSignal {
			out.Subnode.Definitions = append(out.Subnode.Definitions, &parser.Field{
				Position: p.position, Name: "Alias", Value: &parser.StringValue{Position: p.position, Value: p.dstSignal, Quoted: true},
			})
		}
		outputs.Subnode.Definitions = append(outputs.Subnode.Definitions, out)
		rep.Copies++
	}
	obj.Subnode.Definitions = append(obj.Subnode.Definitions, inputs, outputs)
	l.appendDefinition(owner, obj, owner.File)
	return nil
}

// --- helpers -------------------------------------------------------------

func (l *lowerer) active(frag *index.Fragment) bool {
	if l.b.activeFragments == nil {
		return true
	}
	if l.b.activeFragments[frag] {
		return true
	}
	// Fragments synthesized by lowering are marked active explicitly; a
	// build always has at least one active fragment per written node.
	return false
}

// functionsOwner finds the container that holds the GAMs referenced by a
// Functions list: the parent of a referenced GAM, or the nearest ancestor
// with a Functions child.
func (l *lowerer) functionsOwner(n *index.ProjectNode, arr *parser.ArrayValue) *index.ProjectNode {
	for _, el := range arr.Elements {
		name := ""
		switch v := el.(type) {
		case *parser.ReferenceValue:
			name = v.Value
		case *parser.StringValue:
			name = v.Value
		}
		if name == "" {
			continue
		}
		if gam := l.byName[index.NormalizeName(name)]; gam != nil && gam.Parent != nil {
			return gam.Parent
		}
	}
	for p := n; p != nil; p = p.Parent {
		if f, ok := p.Children["Functions"]; ok && f != n {
			return f
		}
	}
	return n
}

func isFunctionsContainer(n *index.ProjectNode) bool {
	if n.Name != "Functions" {
		return false
	}
	for _, frag := range n.Fragments {
		for _, def := range frag.Definitions {
			if f, ok := def.(*parser.Field); ok && f.Name == "Class" {
				return true
			}
		}
	}
	return false
}

func literalText(v parser.Value) string {
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

// replaceSignalBody points a signal node at its resolved declaration so
// the validator and writer see the wiring without re-indexing.
func replaceSignalBody(sig *index.ProjectNode, obj *parser.ObjectNode, file string) {
	sig.Fields = map[string][]index.EvaluatedField{}
	for _, def := range obj.Subnode.Definitions {
		f, ok := def.(*parser.Field)
		if !ok {
			continue
		}
		sig.Fields[f.Name] = append(sig.Fields[f.Name], index.EvaluatedField{Raw: f, Value: f.Value, File: file})
	}
	if len(sig.Fragments) == 0 {
		sig.Fragments = append(sig.Fragments, &index.Fragment{File: file})
	}
	frag := sig.Fragments[0]
	frag.File = file
	frag.Definitions = obj.Subnode.Definitions
	frag.Source = obj
	frag.ObjectPos = obj.Position
	frag.EndPos = obj.Subnode.EndPosition
}

var _ = sort.Strings
