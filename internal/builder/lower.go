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
	Pipelines int // pipeline instances expanded
	Redundant int // copy pairs that were already wired
}

// Empty reports whether nothing was lowered.
func (r LowerReport) Empty() bool {
	return r.Copies == 0 && r.Wired == 0 && r.Buffers == 0 && r.Fused == 0 && r.Pipelines == 0 && r.Redundant == 0
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
	if r.Pipelines > 0 {
		parts = append(parts, fmt.Sprintf("%d pipeline(s) expanded", r.Pipelines))
	}
	if r.Redundant > 0 {
		parts = append(parts, fmt.Sprintf("%d redundant copy pair(s) skipped", r.Redundant))
	}
	return strings.Join(parts, ", ")
}

// copyPair is one resolved copy edge.
// copyEntry is one signal entry generated for an IOGAM.
type copyEntry struct {
	ds     string
	signal string
	alias  string
	typ    string
	dim    int
	opts   []parser.CopyOption
	pos    parser.Position
}

// copyOp is one copy item resolved: a contiguous source block mapped onto a
// contiguous destination block. IOGAM copies whole memory, so entries inside
// an op keep their order.
type copyOp struct {
	srcs    []copyEntry
	dsts    []copyEntry
	pos     parser.Position
	pragmas []string
}

func (o copyOp) grouped() bool { return len(o.srcs) != 1 || len(o.dsts) != 1 }

// typeSize returns the byte size of a scalar MARTe type, or 0 if unknown.
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

// entrySize returns the byte size of an entry, or 0 when it cannot be
// determined (unknown type).
func entrySize(e copyEntry) int {
	base := typeSize(e.typ)
	if base == 0 {
		return 0
	}
	dim := e.dim
	if dim <= 0 {
		dim = 1
	}
	return base * dim
}

// lowerSugar rewrites copy groups and `from` wiring into ordinary MARTe
// configuration (signal declarations, IOGAMs and, when needed,
// GAMDataSource buffers). It runs after validation and before writing.
// lowerAll lowers every root of the tree (the root node plus isolated files).
func (b *Builder) lowerAll() (LowerReport, error) {
	var total LowerReport
	add := func(r LowerReport) {
		total.Copies += r.Copies
		total.Wired += r.Wired
		total.Buffers += r.Buffers
		total.Fused += r.Fused
		total.Pipelines += r.Pipelines
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
	b := NewBuilder(nil, nil)
	b.tree = tree
	// Expansion needs the same scope evaluation as a build: dynamic copy
	// endpoints are materialized per foreach/with expansion during
	// activation, and only active fragments may be lowered.
	b.activate()
	rep, err := b.lowerAll()
	if err != nil {
		return rep, err
	}
	tree.ResolveReferences(nil)
	return rep, nil
}

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

	// 0. Pipeline instances expand into their body GAMs and wiring.
	if err := l.expandPipelines(root, &rep); err != nil {
		return rep, err
	}

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
		file     string
		refPos   parser.Position
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
					if parser.CopyGroupHasDynamicEndpoints(g) {
						// Materialized per expansion during activation; the
						// concrete group is lowered instead.
						continue
					}
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
						groups = append(groups, pending{
							owner: l.functionsOwner(n, arr), arr: arr, arrIndex: j, group: g,
							file: frag.File, refPos: field.Position,
						})
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
		// Drop the sugar definition first so the generated GAM can take its
		// slot without shifting it. Locate it by identity: earlier
		// insertions in the same fragment shift the recorded indices.
		var insertFrag *index.Fragment
		insertAt := -1
		if p.frag != nil {
			if idx := indexOfDefinition(p.frag.Definitions, p.group); idx >= 0 {
				insertAt = idx
				p.frag.Definitions[idx] = nil
			} else {
				insertAt = p.index
			}
			insertFrag = p.frag
		}
		if err := l.emitCopyGAM(owner, name, pairs, p.group.Position, rep, insertAt, insertFrag); err != nil {
			return err
		}

		// Replace the group with a reference to the generated GAM. The
		// reference lives at the list position, in the list's file, so it
		// resolves like a parsed reference.
		refPos := p.group.Position
		if p.refPos.Line > 0 {
			refPos = p.refPos
		}
		ref := &parser.ReferenceValue{Position: refPos, Value: name}
		if p.arr != nil {
			p.arr.Elements[p.arrIndex] = ref
			l.tree.IndexValue(p.file, ref)
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
func (l *lowerer) resolveGroup(group *parser.CopyGroup, redundant *int) ([]copyOp, error) {
	var ops []copyOp
	anyGroup := false
	for _, item := range group.Items {
		srcGroup, dstGroup := item.SrcGroup, item.DstGroup
		if len(srcGroup) > 0 || len(dstGroup) > 0 {
			anyGroup = true
		}

		isRange := (item.Src.ByIndex && item.Src.IndexEnd > item.Src.Index) ||
			(item.Dst.ByIndex && item.Dst.IndexEnd > item.Dst.Index)
		if isRange {
			if len(srcGroup) > 0 || len(dstGroup) > 0 {
				return nil, fmt.Errorf("%d:%d: a signal range cannot be used inside a group",
					item.Position.Line, item.Position.Column)
			}
			if !item.Dst.ByIndex || !item.Src.ByIndex {
				return nil, fmt.Errorf("%d:%d: a signal range needs index ranges on both sides",
					item.Position.Line, item.Position.Column)
			}
			srcs, err := l.resolveSourceList(item.Src)
			if err != nil {
				return nil, fmt.Errorf("%d:%d: %v", item.Position.Line, item.Position.Column, err)
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
				if p, ok := l.singleOp(item, srcs[i], dsts[i]); ok {
					ops = append(ops, p)
				} else {
					*redundant++
				}
			}
			continue
		}

		if len(srcGroup) > 0 || len(dstGroup) > 0 {
			op, ok, err := l.groupOp(item)
			if err != nil {
				return nil, fmt.Errorf("%d:%d: %v", item.Position.Line, item.Position.Column, err)
			}
			if ok {
				ops = append(ops, op)
			} else {
				*redundant++
			}
			continue
		}

		srcs, err := l.resolveSourceList(item.Src)
		if err != nil {
			return nil, fmt.Errorf("%d:%d: %v", item.Position.Line, item.Position.Column, err)
		}
		if len(srcs) != 1 {
			return nil, fmt.Errorf("%d:%d: source range against a single destination",
				item.Position.Line, item.Position.Column)
		}
		typ, dim := srcs[0].typ, srcs[0].dim
		if t, d, ok := optionType(item.SrcOptions); ok {
			typ, dim = t, d
		} else if t, d, ok := optionType(item.DstOptions); ok {
			typ, dim = t, d
		}
		if item.Type != "" {
			typ, dim = item.Type, item.Dim
		}
		dst, err := l.resolveDestination(item.Dst, item.DstAlias, typ, dim, item.DstAt)
		if err != nil {
			return nil, fmt.Errorf("%d:%d: %v", item.Position.Line, item.Position.Column, err)
		}
		if p, ok := l.singleOp(item, srcs[0], dst); ok {
			ops = append(ops, p)
		} else {
			*redundant++
		}
	}
	if anyGroup {
		if err := validateBlockLayout(ops); err != nil {
			return nil, err
		}
	}
	return ops, nil
}

// singleOp builds a one-to-one op, reporting ok=false for a redundant copy.
func (l *lowerer) singleOp(item parser.CopyItem, src, dst signalRef) (copyOp, bool) {
	srcEntry := entryFromSignal(src, item.SrcAlias, item.SrcOptions, item.Position)
	dstEntry := entryFromSignal(dst, item.DstAlias, item.DstOptions, item.Position)
	if item.Type != "" {
		srcEntry.typ, srcEntry.dim = item.Type, item.Dim
	}
	if src.aliasEmpty(item) && dst.aliasEmpty(item) &&
		src.ds == dst.ds && src.signal == dst.signal {
		return copyOp{}, false
	}
	return copyOp{srcs: []copyEntry{srcEntry}, dsts: []copyEntry{dstEntry}, pos: item.Position, pragmas: item.Pragmas}, true
}

func (r signalRef) aliasEmpty(item parser.CopyItem) bool {
	return item.SrcAlias == "" && item.DstAlias == ""
}

// groupOp resolves a grouped item `(a, b, …) -> (c, …)`.
func (l *lowerer) groupOp(item parser.CopyItem) (copyOp, bool, error) {
	op := copyOp{pos: item.Position, pragmas: item.Pragmas}
	for _, m := range item.SrcGroup {
		refs, err := l.resolveSourceList(m.Endpoint)
		if err != nil {
			return op, false, err
		}
		if len(refs) != 1 {
			return op, false, fmt.Errorf("a group member cannot be a range")
		}
		op.srcs = append(op.srcs, entryFromSignal(refs[0], m.Alias, m.Options, m.Position))
	}
	if len(op.srcs) == 0 {
		// Single source used inside a destination group.
		refs, err := l.resolveSourceList(item.Src)
		if err != nil {
			return op, false, err
		}
		if len(refs) != 1 {
			return op, false, fmt.Errorf("a group needs a single source signal")
		}
		op.srcs = append(op.srcs, entryFromSignal(refs[0], item.SrcAlias, item.SrcOptions, item.Position))
	}

	for _, m := range item.DstGroup {
		typ, dim := "", 0
		if t, d, ok := optionType(m.Options); ok {
			typ, dim = t, d
		} else if t, d, ok := optionType(item.SrcOptions); ok {
			typ, dim = t, d
		}
		ref, err := l.resolveDestination(m.Endpoint, m.Alias, typ, dim, 0)
		if err != nil {
			return op, false, err
		}
		op.dsts = append(op.dsts, entryFromSignal(ref, m.Alias, m.Options, m.Position))
	}
	if len(op.dsts) == 0 {
		typ, dim := "", 0
		if t, d, ok := optionType(item.SrcOptions); ok {
			typ, dim = t, d
		}
		ref, err := l.resolveDestination(item.Dst, item.DstAlias, typ, dim, item.DstAt)
		if err != nil {
			return op, false, err
		}
		op.dsts = append(op.dsts, entryFromSignal(ref, item.DstAlias, item.DstOptions, item.Position))
	}
	return op, true, nil
}

// entryFromSignal builds an entry, applying Type/NumberOfElements options.
func entryFromSignal(ref signalRef, alias string, opts []parser.CopyOption, pos parser.Position) copyEntry {
	e := copyEntry{ds: ref.ds, signal: ref.signal, alias: alias, typ: ref.typ, dim: ref.dim, opts: opts, pos: pos}
	if t, d, ok := optionType(opts); ok {
		e.typ, e.dim = t, d
	}
	return e
}

// validateBlockLayout checks that every op maps the same byte range on both
// sides: an IOGAM copies its whole input memory to its output memory, so the
// cumulative offsets must match and each op must move an equal number of
// bytes.
func validateBlockLayout(ops []copyOp) error {
	inOff, outOff := 0, 0
	for _, op := range ops {
		inSize, outSize := 0, 0
		for _, e := range op.srcs {
			sz := entrySize(e)
			if sz == 0 {
				return fmt.Errorf("%d:%d: cannot determine the byte size of '%s' (declare Type)",
					e.pos.Line, e.pos.Column, e.signal)
			}
			inSize += sz
		}
		for _, e := range op.dsts {
			sz := entrySize(e)
			if sz == 0 {
				return fmt.Errorf("%d:%d: cannot determine the byte size of '%s' (declare Type)",
					e.pos.Line, e.pos.Column, e.signal)
			}
			outSize += sz
		}
		if inOff != outOff {
			return fmt.Errorf("%d:%d: copy block starts at byte %d on the input side and %d on the output side",
				op.pos.Line, op.pos.Column, inOff, outOff)
		}
		if inSize != outSize {
			if op.grouped() {
				return fmt.Errorf("%d:%d: copy group moves %d input bytes into %d output bytes",
					op.pos.Line, op.pos.Column, inSize, outSize)
			}
			return fmt.Errorf("%d:%d: signal copy moves %d input bytes into %d output bytes",
				op.pos.Line, op.pos.Column, inSize, outSize)
		}
		inOff += inSize
		outOff += outSize
	}
	if inOff != outOff {
		return fmt.Errorf("copy block totals differ: %d input bytes vs %d output bytes", inOff, outOff)
	}
	return nil
}

// optionType extracts Type/NumberOfElements from a signal's options.
func optionType(opts []parser.CopyOption) (string, int, bool) {
	typ, dim := "", 0
	found := false
	for _, o := range opts {
		switch o.Name {
		case "Type":
			typ = literalText(o.Value)
			found = true
		case "NumberOfElements":
			if v, err := strconv.Atoi(literalText(o.Value)); err == nil {
				dim = v
			}
		}
	}
	return typ, dim, found
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
		return []signalRef{l.dsSignalRef(ep.Object, ep.Signal)}, nil
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
		return l.dsSignalRef(ep.Object, ep.Signal), nil
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

// dsSignalRef resolves `DS::Signal` to its declared type when the
// DataSource and signal are known; unknown ones keep empty type metadata.
func (l *lowerer) dsSignalRef(ds, signal string) signalRef {
	ref := signalRef{ds: ds, signal: signal}
	node := l.byName[index.NormalizeName(ds)]
	if node == nil {
		return ref
	}
	signals, ok := node.Children["Signals"]
	if !ok {
		return ref
	}
	sig, ok := signals.Children[index.NormalizeName(signal)]
	if !ok {
		return ref
	}
	ref.typ, ref.dim = signalDeclaredType(sig)
	return ref
}

// signalDeclaredType reads Type/NumberOfElements from a DataSource signal
// declaration (which has no DataSource field of its own).
func signalDeclaredType(sig *index.ProjectNode) (string, int) {
	typ, dim := "", 0
	for _, frag := range sig.Fragments {
		for _, def := range frag.Definitions {
			f, ok := def.(*parser.Field)
			if !ok {
				continue
			}
			switch f.Name {
			case "Type":
				typ = literalText(f.Value)
			case "NumberOfElements":
				if v, err := strconv.Atoi(literalText(f.Value)); err == nil {
					dim = v
				}
			}
		}
	}
	return typ, dim
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

// indexOfDefinition returns the position of def in defs, or -1.
func indexOfDefinition(defs []parser.Definition, def parser.Definition) int {
	for i, d := range defs {
		if d == def {
			return i
		}
	}
	return -1
}

// insertDefinitionInFrag inserts def at index of a specific fragment.
func (l *lowerer) insertDefinitionInFrag(owner *index.ProjectNode, frag *index.Fragment, index int, def parser.Definition) {
	if index < 0 || index > len(frag.Definitions) {
		index = len(frag.Definitions)
	}
	frag.Definitions = append(frag.Definitions, nil)
	copy(frag.Definitions[index+1:], frag.Definitions[index:])
	frag.Definitions[index] = def
	l.indexDefinitionCond(owner, def, frag.File, frag.IsConditional)
}

// insertDefinition places def at index of the owner's active fragment,
// keeping the generated GAM where the sugar was written.
func (l *lowerer) insertDefinition(owner *index.ProjectNode, index int, def parser.Definition) {
	frag := l.activeFragmentOf(owner, owner.File)
	if frag == nil {
		return
	}
	if index < 0 || index > len(frag.Definitions) {
		index = len(frag.Definitions)
	}
	frag.Definitions = append(frag.Definitions, nil)
	copy(frag.Definitions[index+1:], frag.Definitions[index:])
	frag.Definitions[index] = def
	l.indexDefinitionCond(owner, def, frag.File, frag.IsConditional)
}

// appendDefinition adds def to the (first active) fragment of node and
// rebuilds the index subtree for it.
func (l *lowerer) appendDefinition(node *index.ProjectNode, def parser.Definition, file string) {
	frag := l.activeFragmentOf(node, file)
	if frag == nil {
		return
	}
	frag.Definitions = append(frag.Definitions, def)
	l.indexDefinitionCond(node, def, frag.File, frag.IsConditional)
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
	l.indexDefinitionCond(parent, def, file, false)
}

func (l *lowerer) indexDefinitionCond(parent *index.ProjectNode, def parser.Definition, file string, conditional bool) {
	switch d := def.(type) {
	case *parser.ObjectNode:
		name := index.NormalizeName(literalText(d.Name))
		if name == "" {
			return
		}
		child, ok := parent.Children[name]
		if !ok {
			child = &index.ProjectNode{
				Name:          name,
				RealName:      literalText(d.Name),
				Parent:        parent,
				Children:      map[string]*index.ProjectNode{},
				Variables:     map[string]index.VariableInfo{},
				Fields:        map[string][]index.EvaluatedField{},
				Metadata:      map[string]string{},
				IsConditional: conditional,
				File:          file,
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
			l.indexDefinitionCond(child, sub, file, conditional)
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
func (l *lowerer) emitCopyGAM(owner *index.ProjectNode, name string, ops []copyOp, pos parser.Position, rep *LowerReport, insertAt int, insertFrag *index.Fragment) error {
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
	type attached struct {
		side    string
		entry   string
		pragmas []string
	}
	var attaches []attached
	appendEntry := func(container *parser.ObjectNode, e copyEntry, side string, used map[string]bool, pragmas []string) {
		entryName := e.alias
		if entryName == "" {
			entryName = e.signal
		}
		base := entryName
		for i := 2; used[entryName]; i++ {
			entryName = fmt.Sprintf("%s%d", base, i)
		}
		used[entryName] = true
		entry := &parser.ObjectNode{Position: e.pos, Name: &parser.ReferenceValue{Position: e.pos, Value: entryName}}
		entry.Subnode.Definitions = append(entry.Subnode.Definitions, &parser.Field{
			Position: e.pos, Name: "DataSource", Value: &parser.ReferenceValue{Position: e.pos, Value: e.ds},
		})
		if entryName != e.signal {
			entry.Subnode.Definitions = append(entry.Subnode.Definitions, &parser.Field{
				Position: e.pos, Name: "Alias", Value: &parser.StringValue{Position: e.pos, Value: e.signal, Quoted: true},
			})
		}
		for _, o := range e.opts {
			entry.Subnode.Definitions = append(entry.Subnode.Definitions, &parser.Field{Position: o.Position, Name: o.Name, Value: o.Value})
		}
		container.Subnode.Definitions = append(container.Subnode.Definitions, entry)
		if len(pragmas) > 0 {
			attaches = append(attaches, attached{side: side, entry: entryName, pragmas: pragmas})
		}
	}

	for _, op := range ops {
		for _, e := range op.srcs {
			appendEntry(inputs, e, "in", usedIn, op.pragmas)
		}
		for _, e := range op.dsts {
			appendEntry(outputs, e, "out", usedOut, op.pragmas)
		}
		rep.Copies += len(op.srcs)
	}
	obj.Subnode.Definitions = append(obj.Subnode.Definitions, inputs, outputs)
	if insertAt >= 0 && insertFrag != nil {
		l.insertDefinitionInFrag(owner, insertFrag, insertAt, obj)
	} else if insertAt >= 0 {
		l.insertDefinition(owner, insertAt, obj)
	} else {
		l.appendDefinition(owner, obj, owner.File)
	}
	// Carry per-item pragmas (e.g. `//! implicit: …`) onto the generated
	// signal nodes so diagnostics behave like the hand-written entries.
	if len(attaches) > 0 {
		gam := owner.Children[index.NormalizeName(name)]
		for _, a := range attaches {
			container := "InputSignals"
			if a.side == "out" {
				container = "OutputSignals"
			}
			if gam == nil {
				continue
			}
			cont, ok := gam.Children[container]
			if !ok {
				continue
			}
			if sig, ok := cont.Children[index.NormalizeName(a.entry)]; ok {
				sig.Pragmas = append(sig.Pragmas, a.pragmas...)
			}
		}
	}
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
