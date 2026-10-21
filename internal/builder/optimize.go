package builder

import (
	"fmt"
	"sort"
	"strings"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
)

// OptimizeReport summarizes the transformations applied by the tree
// optimizer. Zero values mean nothing was changed.
type OptimizeReport struct {
	MergedIOGAMs         int // IOGAMs folded into a preceding one
	RemovedIdentityGAMs  int // no-op copy GAMs removed
	ReorderedDataSources int // DataSources whose signal order changed
	ReorderedSignals     int // DataSource signals moved
	ReorderedGAMSignals  int // GAM signal lists reordered
	FixedOrderSources    int // DataSources left alone (external layout)
}

// Empty reports whether the optimizer changed anything.
func (r OptimizeReport) Empty() bool {
	return r.MergedIOGAMs == 0 && r.RemovedIdentityGAMs == 0 &&
		r.ReorderedDataSources == 0 && r.ReorderedSignals == 0 &&
		r.ReorderedGAMSignals == 0 && r.FixedOrderSources == 0
}

func (r OptimizeReport) String() string {
	if r.Empty() {
		return "no optimisations applied"
	}
	parts := []string{}
	if r.MergedIOGAMs > 0 {
		parts = append(parts, fmt.Sprintf("merged %d IOGAM(s)", r.MergedIOGAMs))
	}
	if r.RemovedIdentityGAMs > 0 {
		parts = append(parts, fmt.Sprintf("removed %d identity copy GAM(s)", r.RemovedIdentityGAMs))
	}
	if r.ReorderedDataSources > 0 {
		parts = append(parts, fmt.Sprintf("reordered %d signal(s) in %d DataSource(s)",
			r.ReorderedSignals, r.ReorderedDataSources))
	}
	if r.ReorderedGAMSignals > 0 {
		parts = append(parts, fmt.Sprintf("grouped %d GAM signal list(s)", r.ReorderedGAMSignals))
	}
	if r.FixedOrderSources > 0 {
		parts = append(parts, fmt.Sprintf("kept the signal order of %d data source(s) with an external layout", r.FixedOrderSources))
	}
	return strings.Join(parts, ", ")
}

// optimize applies MARTe-oriented optimisations to the tree before it is
// written. The tree is private to the build, so mutating definitions and
// child maps here is safe.
//
// Transformations, in order:
//
//  1. Merge adjacent IOGAMs in the same Functions list. IOGAM copies its
//     InputSignals to OutputSignals positionally, so concatenating two
//     adjacent lists preserves the copy order while removing one function
//     invocation (and its setup) per merged GAM.
//  2. Remove IOGAMs whose every copy is an identity (same DataSource and
//     signal on both sides): they perform no useful work.
//  3. Order DataSource Signals by first consumer in the execution order,
//     so signals read by the same GAM are adjacent in the DataSource's
//     memory layout (fewer, larger contiguous copies).
//  4. Group IOGAM copy pairs by source DataSource: copies that read the
//     same DataSource run consecutively, improving locality.
//  5. Group non-IOGAM GAM signal lists by DataSource (order is irrelevant
//     for named signal entries).
func (b *Builder) optimize(root *index.ProjectNode) OptimizeReport {
	var rep OptimizeReport
	if root == nil || b.tree == nil {
		return rep
	}

	o := &optimizer{
		tree:   b.tree,
		ctx:    &index.EvaluationContext{Variables: b.variables, Tree: b.tree},
		byName: map[string]*index.ProjectNode{},
	}
	o.indexNames(root)
	o.executionOrder(root)

	o.removeIdentityIOGAMs(root, &rep)
	o.mergeIOGAMs(root, &rep)
	o.reorderDataSources(root, &rep)
	o.groupIOGAMCopies(root, &rep)
	o.groupGAMSignals(root, &rep)
	return rep
}

type optimizer struct {
	tree   *index.ProjectTree
	ctx    *index.EvaluationContext
	byName map[string]*index.ProjectNode
	// position maps a GAM node to its first index in the execution order.
	position map[*index.ProjectNode]int
	// usage maps "datasource|signal" to the earliest consumer position.
	usage map[string]int
}

const unreferenced = 1 << 30

func (o *optimizer) indexNames(root *index.ProjectNode) {
	var walk func(n *index.ProjectNode)
	walk = func(n *index.ProjectNode) {
		if n.RealName != "" {
			o.byName[index.NormalizeName(n.RealName)] = n
		}
		if n.Parent == nil {
			// Root and package nodes have no runtime name; register by
			// their tree name as a fallback.
			o.byName[n.Name] = n
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(root)
}

// executionOrder walks Functions arrays in declaration order and records
// where each function runs. Signals read or written by a function inherit
// that position, giving the DataSource sorter a deterministic grouping.
func (o *optimizer) executionOrder(root *index.ProjectNode) {
	o.position = map[*index.ProjectNode]int{}
	o.usage = map[string]int{}
	pos := 0
	for _, fa := range o.functionsArrays() {
		for _, el := range fa.arr.Elements {
			name := referenceName(el)
			if name == "" {
				continue
			}
			gam := o.byName[name]
			if gam == nil {
				pos++
				continue
			}
			if _, seen := o.position[gam]; !seen {
				o.position[gam] = pos
			}
			pos++
			o.recordUsage(gam)
		}
	}
}

func (o *optimizer) recordUsage(gam *index.ProjectNode) {
	pos, ok := o.position[gam]
	if !ok {
		return
	}
	for _, container := range []string{"InputSignals", "OutputSignals"} {
		sigs, ok := gam.Children[container]
		if !ok {
			continue
		}
		for _, sig := range sigs.Children {
			ds := signalDataSource(sig)
			if ds == "" {
				continue
			}
			key := index.NormalizeName(ds) + "|" + sig.Name
			if cur, seen := o.usage[key]; !seen || pos < cur {
				o.usage[key] = pos
			}
		}
	}
}

// --- helpers -------------------------------------------------------------

func referenceName(v parser.Value) string {
	switch t := v.(type) {
	case *parser.ReferenceValue:
		return index.NormalizeName(t.Value)
	case *parser.StringValue:
		return index.NormalizeName(t.Value)
	}
	return ""
}

func objectName(def parser.Definition) string {
	switch t := def.(type) {
	case *parser.ObjectNode:
		return referenceName(t.Name)
	case *parser.SignalShorthand:
		return index.NormalizeName(t.SignalName)
	}
	return ""
}

func nodeClass(n *index.ProjectNode) string {
	for _, frag := range n.Fragments {
		for _, def := range frag.Definitions {
			f, ok := def.(*parser.Field)
			if !ok || f.Name != "Class" {
				continue
			}
			switch v := f.Value.(type) {
			case *parser.ReferenceValue:
				return v.Value
			case *parser.StringValue:
				return v.Value
			}
		}
	}
	return ""
}

func signalDataSource(sig *index.ProjectNode) string {
	for _, frag := range sig.Fragments {
		for _, def := range frag.Definitions {
			f, ok := def.(*parser.Field)
			if !ok || f.Name != "DataSource" {
				continue
			}
			if name := referenceName(f.Value); name != "" {
				return name
			}
		}
	}
	return ""
}

func signalFieldValue(sig *index.ProjectNode, field string) string {
	for _, frag := range sig.Fragments {
		for _, def := range frag.Definitions {
			f, ok := def.(*parser.Field)
			if !ok || f.Name != field {
				continue
			}
			return valueText(f.Value)
		}
	}
	return ""
}

func valueText(v parser.Value) string {
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

// objectDefs collects the object-definition indices of a fragment.
func objectDefs(defs []parser.Definition) ([]int, []string) {
	idxs := []int{}
	names := []string{}
	for i, d := range defs {
		switch d.(type) {
		case *parser.ObjectNode, *parser.SignalShorthand:
			idxs = append(idxs, i)
			names = append(names, objectName(d))
		}
	}
	return idxs, names
}

// reorderContainer rewrites the object definitions of container in the
// order given by less. Returns how many entries moved.
func reorderContainer(container *index.ProjectNode, less func(a, b string) bool) int {
	moved := 0
	for _, frag := range container.Fragments {
		idxs, names := objectDefs(frag.Definitions)
		if len(idxs) < 2 {
			continue
		}
		order := make([]int, len(idxs))
		for i := range order {
			order[i] = i
		}
		sort.SliceStable(order, func(i, j int) bool {
			return less(names[order[i]], names[order[j]])
		})
		newDefs := append([]parser.Definition(nil), frag.Definitions...)
		for slot, src := range order {
			newDefs[idxs[slot]] = frag.Definitions[idxs[src]]
			if slot != src {
				moved++
			}
		}
		frag.Definitions = newDefs
	}
	return moved
}

// --- transform 3: DataSource signal ordering -----------------------------

// reorderableDataSourceClasses lists DataSource classes whose signal storage
// is private to the implementation and addressed by name, so declaration
// order carries no semantics. Everything else — FileReader/FileWriter (fixed
// binary layout on disk), SDN subscriber/publisher (external payload
// layout), hardware data sources (channel map defined by the hardware
// configuration) and unknown classes — keeps its signal order.
var reorderableDataSourceClasses = map[string]bool{
	"GAMDataSource":  true,
	"EPICSCAInput":   true,
	"EPICSCAOutput":  true,
	"EPICSCAClient":  true,
	"EPICSPVAInput":  true,
	"EPICSPVAOutput": true,
	"EPICSPVA":       true,
}

// isReorderableDataSource reports whether the DataSource's Signals block may
// be reordered. Namespaced classes (`DAN::Foo`) are matched on the last
// segment.
func isReorderableDataSource(n *index.ProjectNode) bool {
	cls := nodeClass(n)
	if idx := strings.LastIndex(cls, "::"); idx != -1 {
		cls = cls[idx+2:]
	}
	return reorderableDataSourceClasses[cls]
}

func (o *optimizer) reorderDataSources(root *index.ProjectNode, rep *OptimizeReport) {
	o.tree.Walk(func(n *index.ProjectNode) {
		if !o.tree.IsDataSource(n) {
			return
		}
		if !isReorderableDataSource(n) {
			if signals, ok := n.Children["Signals"]; ok && len(signals.Children) > 1 {
				rep.FixedOrderSources++
			}
			return
		}
		signals, ok := n.Children["Signals"]
		if !ok || len(signals.Children) < 2 {
			return
		}
		dsName := n.Name
		key := func(sig string) int {
			if pos, ok := o.usage[index.NormalizeName(dsName)+"|"+sig]; ok {
				return pos
			}
			return unreferenced
		}
		moved := reorderContainer(signals, func(a, b string) bool {
			ka, kb := key(a), key(b)
			if ka != kb {
				return ka < kb
			}
			return a < b
		})
		if moved > 0 {
			rep.ReorderedDataSources++
			rep.ReorderedSignals += moved
		}
	})
}

// --- transform 4: IOGAM copy grouping ------------------------------------

func (o *optimizer) groupIOGAMCopies(root *index.ProjectNode, rep *OptimizeReport) {
	o.tree.Walk(func(n *index.ProjectNode) {
		if nodeClass(n) != "IOGAM" {
			return
		}
		inputs, okIn := n.Children["InputSignals"]
		outputs, okOut := n.Children["OutputSignals"]
		if !okIn || !okOut {
			return
		}
		if len(inputs.Children) != len(outputs.Children) || len(inputs.Children) < 2 {
			// Pairing is positional; only reorder when both sides match.
			return
		}
		if !iogamCopiesIndependent(inputs, outputs) {
			// A pair writes a signal another pair reads (A->B, B->C):
			// reordering the copies would change what is read.
			return
		}
		key := func(sig string) (string, string) {
			child, ok := inputs.Children[sig]
			if !ok {
				return "", sig
			}
			return signalDataSource(child), sig
		}
		// Compute the permutation from the input list, then apply it by
		// name to both containers so the copies stay paired.
		for _, frag := range inputs.Fragments {
			idxs, names := objectDefs(frag.Definitions)
			if len(idxs) < 2 {
				continue
			}
			order := make([]int, len(idxs))
			for i := range order {
				order[i] = i
			}
			sort.SliceStable(order, func(i, j int) bool {
				da, sa := key(names[order[i]])
				db, sb := key(names[order[j]])
				if da != db {
					return da < db
				}
				return sa < sb
			})
			moved := false
			for slot, src := range order {
				if slot != src {
					moved = true
				}
			}
			if !moved {
				continue
			}
			applyIndexOrder(frag, order)
			for _, outFrag := range outputs.Fragments {
				applyIndexOrder(outFrag, order)
			}
			rep.ReorderedGAMSignals++
			return
		}
	})
}

// iogamCopiesIndependent reports whether reordering the copy pairs is safe:
// no pair may write a signal that a different pair reads.
func iogamCopiesIndependent(inputs, outputs *index.ProjectNode) bool {
	readBy := map[string]int{}
	for i, name := range orderedNames(inputs) {
		sig, ok := inputs.Children[name]
		if !ok {
			return false
		}
		readBy[signalRefKey(sig)] = i
	}
	written := orderedNames(outputs)
	if len(written) != len(readBy) {
		return false
	}
	for i, name := range written {
		sig, ok := outputs.Children[name]
		if !ok {
			return false
		}
		if from, ok := readBy[signalRefKey(sig)]; ok && from != i {
			return false
		}
	}
	return true
}

// signalRefKey identifies a signal by DataSource and name (the actual
// storage location), ignoring declaration details.
func signalRefKey(sig *index.ProjectNode) string {
	return index.NormalizeName(signalDataSource(sig)) + "|" + sig.Name
}

// applyIndexOrder rewrites frag's object definitions so that the new slot
// i holds the definition that was at old index order[i]. Used to permute
// IOGAM input and output lists with the same permutation, preserving the
// positional copy pairing.
func applyIndexOrder(frag *index.Fragment, order []int) {
	idxs, _ := objectDefs(frag.Definitions)
	if len(idxs) != len(order) {
		return
	}
	newDefs := append([]parser.Definition(nil), frag.Definitions...)
	for slot, src := range order {
		newDefs[idxs[slot]] = frag.Definitions[idxs[src]]
	}
	frag.Definitions = newDefs
}

// applyNameOrder rewrites frag's object definitions to the given name order.
func applyNameOrder(frag *index.Fragment, order []string) {
	idxs, _ := objectDefs(frag.Definitions)
	if len(idxs) != len(order) {
		return
	}
	byName := make(map[string]parser.Definition, len(idxs))
	for _, i := range idxs {
		byName[objectName(frag.Definitions[i])] = frag.Definitions[i]
	}
	newDefs := append([]parser.Definition(nil), frag.Definitions...)
	for slot, name := range order {
		if def, ok := byName[name]; ok {
			newDefs[idxs[slot]] = def
		}
	}
	frag.Definitions = newDefs
}

// applyNameOrderTo rewrites every fragment of container to the given order,
// keeping entries not mentioned in order at their positions.
func applyNameOrderTo(container *index.ProjectNode, order []string) {
	for _, frag := range container.Fragments {
		applyNameOrder(frag, order)
	}
}

// --- transform 5: non-IOGAM signal grouping ------------------------------

func (o *optimizer) groupGAMSignals(root *index.ProjectNode, rep *OptimizeReport) {
	o.tree.Walk(func(n *index.ProjectNode) {
		if !o.tree.IsGAM(n) || nodeClass(n) == "IOGAM" {
			return
		}
		changed := false
		for _, container := range []string{"InputSignals", "OutputSignals"} {
			sigs, ok := n.Children[container]
			if !ok || len(sigs.Children) < 2 {
				continue
			}
			keyOf := func(sig string) (string, string) {
				child, ok := sigs.Children[sig]
				if !ok {
					return "", sig
				}
				return signalDataSource(child), sig
			}
			moved := reorderContainer(sigs, func(a, b string) bool {
				da, sa := keyOf(a)
				db, sb := keyOf(b)
				if da != db {
					return da < db
				}
				return sa < sb
			})
			if moved > 0 {
				changed = true
			}
		}
		if changed {
			rep.ReorderedGAMSignals++
		}
	})
}

// --- transform 2: identity IOGAM removal ---------------------------------

func (o *optimizer) removeIdentityIOGAMs(root *index.ProjectNode, rep *OptimizeReport) {
	o.tree.Walk(func(n *index.ProjectNode) {
		if nodeClass(n) != "IOGAM" {
			return
		}
		inputs, okIn := n.Children["InputSignals"]
		outputs, okOut := n.Children["OutputSignals"]
		if !okIn || !okOut || len(inputs.Children) == 0 {
			return
		}
		if len(inputs.Children) != len(outputs.Children) {
			return
		}
		if !singleFragment(n) || n.Doc != "" {
			return
		}
		inOrder := orderedNames(inputs)
		outOrder := orderedNames(outputs)
		if len(inOrder) == 0 || len(inOrder) != len(outOrder) {
			return
		}
		if len(inOrder) != len(inputs.Children) || len(outOrder) != len(outputs.Children) {
			return // shorthand-only or unparsed entries: do not guess
		}
		for i := range inOrder {
			inSig, ok1 := inputs.Children[inOrder[i]]
			outSig, ok2 := outputs.Children[outOrder[i]]
			if !ok1 || !ok2 {
				return
			}
			if normalizeSignalKey(inSig) != normalizeSignalKey(outSig) {
				return
			}
		}
		if !o.removeFunction(n) {
			return
		}
		rep.RemovedIdentityGAMs++
	})
}

// normalizeSignalKey identifies a signal pair for identity checks: the
// DataSource, name, Type and NumberOfElements must all match.
func normalizeSignalKey(sig *index.ProjectNode) string {
	return strings.Join([]string{
		signalDataSource(sig),
		sig.Name,
		signalFieldValue(sig, "Type"),
		signalFieldValue(sig, "NumberOfElements"),
	}, "|")
}

// --- transform 1: adjacent IOGAM merging ---------------------------------

type functionsArray struct {
	owner *index.ProjectNode
	arr   *parser.ArrayValue
}

func (o *optimizer) mergeIOGAMs(root *index.ProjectNode, rep *OptimizeReport) {
	arrays := o.functionsArrays()
	if len(arrays) == 0 {
		return
	}

	// runsOf returns the maximal adjacent IOGAM runs of an array.
	runsOf := func(fa *functionsArray) [][]*index.ProjectNode {
		var runs [][]*index.ProjectNode
		var cur []*index.ProjectNode
		flush := func() {
			if len(cur) > 1 {
				runs = append(runs, cur)
			}
			cur = nil
		}
		for _, el := range fa.arr.Elements {
			gam := o.mergeableGAM(referenceName(el))
			if gam != nil {
				cur = append(cur, gam)
				continue
			}
			flush()
		}
		flush()
		return runs
	}

	merged := map[*index.ProjectNode]bool{}
	for _, fa := range arrays {
		for _, run := range runsOf(fa) {
			if merged[run[0]] {
				continue // already folded in a previous array
			}
			involved := map[*index.ProjectNode]bool{}
			for _, g := range run {
				involved[g] = true
			}
			// Every array mentioning any of the run's GAMs must contain the
			// same run (same order, adjacency); otherwise merging would
			// change another state's copies.
			ok := true
			var targets []*functionsArray
			for _, other := range arrays {
				mentions := false
				for _, el := range other.arr.Elements {
					if involved[o.mergeableGAM(referenceName(el))] || involved[o.byName[referenceName(el)]] {
						mentions = true
						break
					}
				}
				if !mentions {
					continue
				}
				hasRun := false
				for _, r := range runsOf(other) {
					if sameRun(r, run) {
						hasRun = true
						break
					}
				}
				if !hasRun {
					ok = false
					break
				}
				targets = append(targets, other)
			}
			if !ok || len(targets) == 0 {
				continue
			}

			first := run[0]
			for _, g := range run[1:] {
				if !o.mergeInto(first, g) {
					ok = false
					break
				}
				merged[g] = true
				rep.MergedIOGAMs++
			}
			if !ok {
				continue
			}
			for _, t := range targets {
				replaceRun(t.arr, run, first)
			}
			merged[first] = true
		}
	}
}

// mergeableGAM resolves a Functions element to a GAM that can be merged.
func (o *optimizer) mergeableGAM(name string) *index.ProjectNode {
	if name == "" {
		return nil
	}
	gam := o.byName[name]
	if gam == nil || nodeClass(gam) != "IOGAM" || !o.mergeable(gam) {
		return nil
	}
	return gam
}

// sameRun reports whether two runs reference the same GAMs in the same order.
func sameRun(a, b []*index.ProjectNode) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// replaceRun rewrites a Functions array so the run becomes a single
// reference to first, keeping every other element in place.
func replaceRun(arr *parser.ArrayValue, run []*index.ProjectNode, first *index.ProjectNode) {
	out := make([]parser.Value, 0, len(arr.Elements))
	i := 0
	for i < len(arr.Elements) {
		if i+len(run) <= len(arr.Elements) && elementRunMatches(arr.Elements[i:i+len(run)], run) {
			out = append(out, &parser.ReferenceValue{Position: arr.Elements[i].Pos(), Value: index.NormalizeName(first.RealName)})
			i += len(run)
			continue
		}
		out = append(out, arr.Elements[i])
		i++
	}
	arr.Elements = out
}

func elementRunMatches(elems []parser.Value, run []*index.ProjectNode) bool {
	if len(elems) != len(run) {
		return false
	}
	for i := range elems {
		if referenceName(elems[i]) != index.NormalizeName(run[i].RealName) {
			return false
		}
	}
	return true
}

// functionsArrays collects every distinct Functions array in the tree.
// `Functions = @list` refers to a shared `let` array; the evaluated array
// is used so merging rewrites the list every thread sees.
func (o *optimizer) functionsArrays() []*functionsArray {
	var out []*functionsArray
	seen := map[*parser.ArrayValue]bool{}
	o.tree.Walk(func(n *index.ProjectNode) {
		for _, frag := range n.Fragments {
			for _, def := range frag.Definitions {
				f, ok := def.(*parser.Field)
				if !ok || f.Name != "Functions" {
					continue
				}
				arr := o.evaluatedArray(f.Value)
				if arr == nil || seen[arr] {
					continue
				}
				seen[arr] = true
				out = append(out, &functionsArray{owner: n, arr: arr})
			}
		}
	})
	return out
}

// evaluatedArray resolves a Functions field value to its array, following
// variable references. The array returned is the one stored in the source
// (`let f: [...] = { ... }`), not a fresh evaluation, so rewriting it is
// visible in the output for every thread that uses the list.
func (o *optimizer) evaluatedArray(v parser.Value) *parser.ArrayValue {
	switch t := v.(type) {
	case *parser.ArrayValue:
		return t
	case *parser.VariableReferenceValue:
		return o.rawArrayFor(variableName(t.Name), 0)
	}
	return nil
}

func variableName(name string) string {
	return strings.TrimPrefix(strings.TrimPrefix(name, "@"), "$")
}

// rawArrayFor finds the variable definition and returns its raw default
// array, following chained references.
func (o *optimizer) rawArrayFor(name string, depth int) *parser.ArrayValue {
	if name == "" || depth > 8 {
		return nil
	}
	var def *parser.VariableDefinition
	o.tree.Walk(func(n *index.ProjectNode) {
		if def != nil {
			return
		}
		if info, ok := n.Variables[name]; ok {
			def = info.Def
		}
	})
	if def == nil || def.DefaultValue == nil {
		return nil
	}
	switch t := def.DefaultValue.(type) {
	case *parser.ArrayValue:
		return t
	case *parser.VariableReferenceValue:
		return o.rawArrayFor(variableName(t.Name), depth+1)
	}
	return nil
}

// mergeable reports whether a GAM can participate in merging.
func (o *optimizer) mergeable(n *index.ProjectNode) bool {
	if !singleFragment(n) || n.Doc != "" {
		return false
	}
	// Extra top-level fields would be silently dropped by the merge.
	allowed := map[string]bool{"Class": true, "InputSignals": true, "OutputSignals": true}
	for _, frag := range n.Fragments {
		for _, def := range frag.Definitions {
			if f, ok := def.(*parser.Field); ok && !allowed[f.Name] {
				return false
			}
		}
	}
	return true
}

// mergeInto appends src's input/output signals to dst and removes src.
func (o *optimizer) mergeInto(dst, src *index.ProjectNode) bool {
	if dst == src {
		return true
	}
	for _, container := range []string{"InputSignals", "OutputSignals"} {
		dstSigs, ok1 := dst.Children[container]
		srcSigs, ok2 := src.Children[container]
		if !ok1 || !ok2 {
			continue
		}
		if len(srcSigs.Children) == 0 {
			continue
		}
		for name := range srcSigs.Children {
			if _, clash := dstSigs.Children[name]; clash {
				return false // duplicate signal names: leave the GAM alone
			}
		}
		// Move definitions (order preserved) into dst's fragment.
		for _, srcFrag := range srcSigs.Fragments {
			var moved []parser.Definition
			kept := srcFrag.Definitions[:0]
			for _, def := range srcFrag.Definitions {
				switch def.(type) {
				case *parser.ObjectNode, *parser.SignalShorthand:
					moved = append(moved, def)
					continue
				}
				kept = append(kept, def)
			}
			srcFrag.Definitions = kept
			if len(moved) == 0 {
				continue
			}
			dstFrag := dstSigs.Fragments[0]
			dstFrag.Definitions = append(dstFrag.Definitions, moved...)
		}
		for name, child := range srcSigs.Children {
			dstSigs.Children[name] = child
			child.Parent = dstSigs
		}
		srcSigs.Children = map[string]*index.ProjectNode{}
	}
	// Drop the merged GAM's object definition and its tree entry, otherwise
	// the writer would emit it again from the Functions fragment.
	o.removeDefinition(src)
	return true
}

// references counts how many Functions arrays mention n.
func (o *optimizer) references(n *index.ProjectNode) int {
	name := index.NormalizeName(n.RealName)
	count := 0
	for _, fa := range o.functionsArrays() {
		for _, el := range fa.arr.Elements {
			if referenceName(el) == name {
				count++
			}
		}
	}
	return count
}

// removeReferences drops every mention of n from Functions arrays.
func (o *optimizer) removeReferences(n *index.ProjectNode) {
	name := index.NormalizeName(n.RealName)
	for _, fa := range o.functionsArrays() {
		kept := fa.arr.Elements[:0]
		for _, el := range fa.arr.Elements {
			if referenceName(el) == name {
				continue
			}
			kept = append(kept, el)
		}
		fa.arr.Elements = kept
	}
}

// removeDefinition deletes n's object definition and node entry.
func (o *optimizer) removeDefinition(n *index.ProjectNode) {
	parent := n.Parent
	if parent == nil {
		return
	}
	name := index.NormalizeName(n.RealName)
	for _, frag := range parent.Fragments {
		kept := frag.Definitions[:0]
		for _, def := range frag.Definitions {
			if _, ok := def.(*parser.ObjectNode); ok && objectName(def) == name {
				continue
			}
			kept = append(kept, def)
		}
		frag.Definitions = kept
	}
	delete(parent.Children, n.Name)
}

// removeFunction removes n from the configuration when it is referenced at
// most once. Returns false when it is shared between Functions lists.
func (o *optimizer) removeFunction(n *index.ProjectNode) bool {
	if o.references(n) > 1 {
		return false
	}
	o.removeReferences(n)
	o.removeDefinition(n)
	return true
}

func singleFragment(n *index.ProjectNode) bool {
	return len(n.Fragments) == 1
}

func orderedNames(container *index.ProjectNode) []string {
	names := []string{}
	for _, frag := range container.Fragments {
		for _, def := range frag.Definitions {
			switch def.(type) {
			case *parser.ObjectNode, *parser.SignalShorthand:
				names = append(names, objectName(def))
			}
		}
	}
	return names
}
