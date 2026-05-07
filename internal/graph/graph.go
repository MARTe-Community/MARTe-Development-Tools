package graph

import (
	"fmt"
	"sort"
	"strings"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
)

const maxSigDisplay = 128

// DiagSeverity represents error (0) or warning (1).
type DiagSeverity int

const (
	DiagError   DiagSeverity = 0
	DiagWarning DiagSeverity = 1
)

// NodeDiag is a single diagnostic attached to a node.
type NodeDiag struct {
	Severity DiagSeverity
	Message  string
}

// SigInfo holds metadata for a signal shown in a node.
type SigInfo struct {
	LocalName string
	DSName    string // canonical DS signal name
	Type      string
	NumElems  string
	Doc       string
	Dir       string // "in", "out", "ds-out", "ds-in", "ds-both"
	Implicit  bool
	PortID    string
	Diags     []NodeDiag
}

// NodeInfo is metadata for the /api/meta endpoint.
type NodeInfo struct {
	Name        string            `json:"name"`
	Kind        string            `json:"kind"` // "gam" or "ds"
	Class       string            `json:"class"`
	Doc         string            `json:"doc"`
	Conditional bool              `json:"conditional"`
	IOGAM       bool              `json:"iogam"`
	Fields      map[string]string `json:"fields"`
	InSigs      []SigInfo         `json:"-"`
	OutSigs     []SigInfo         `json:"-"`
	DSSigs      []SigInfo         `json:"-"`
	Diags       []NodeDiag        `json:"-"`
	// For split DS nodes (mixed source+sink):
	SplitSide  string   // "r" = source/read clone, "w" = sink/write clone, "" = single
	CloneGroup []string // all graphviz IDs for the same logical DS (including self)
}

// StateInfo describes GAMs in each thread of a real-time state.
type StateInfo struct {
	Threads map[string][]string
}

// Result is returned by Generate.
type Result struct {
	DOT       string
	Meta      map[string]NodeInfo
	States    map[string]*StateInfo
	AllGAMIDs map[string]bool
	// Node lookup maps (graphviz ID → ProjectNode) for subset generation.
	GAMNodes map[string]*index.ProjectNode
	DSNodes  map[string]*index.ProjectNode
	// GenOpts stores the options that produced this Result so that
	// GenerateSubset can regenerate a subset at the same simplification level.
	GenOpts genOpts
}

// edge describes one signal connection.
type edge struct {
	fromID, toID     string
	fromPort, toPort string
	color            string
	isWrite          bool // true = GAM→DS
}

// dsSplitEntry holds the graphviz node IDs for a DS that may be split.
type dsSplitEntry struct {
	readID         string          // ID for the read clone (early-read signals only)
	writeID        string          // ID for the main node  (write + late-read signals)
	isMixed        bool            // true when a read clone is needed
	earlyReadPorts map[string]bool // DS port IDs (s_+canon) that must go to the read clone
}

// GenerateOptions controls public Generate entry points for static output.
type GenerateOptions struct {
	StateFilter  string   // restrict to GAMs in this state
	ThreadFilter string   // restrict further to one thread within StateFilter
	FollowNodes  []string // node names to focus on (subset); empty = all
	// Simplified controls the simplification level:
	//   0 = full graph (HTML-table nodes, all signals, all nodes)
	//   1 = bypass IOGAM and pass-through DS nodes; keep HTML-table signal display
	//   2 = bypass IOGAM and pass-through DS nodes; collapse to plain box nodes
	Simplified int
}

// genOpts controls behaviour of the internal generate function.
type genOpts struct {
	stateFilter  string
	threadFilter string // single thread within stateFilter; only used when stateFilter != ""
	bypassLevel  int    // 0=off, 1=bypass+HTML signals, 2=bypass+plain nodes (→generateSimplified)
	// subsetNodes, if non-nil, restricts the graph to this set of ProjectNodes.
	// IDs remain consistent because the full tree.Walk is still done for ID assignment.
	subsetNodes map[*index.ProjectNode]bool
}

// activeThreadsMap returns the threads map to use for state-based filtering.
// When threadFilter is set, only that thread is included.
func activeThreadsMap(si *StateInfo, threadFilter string) map[string][]string {
	if si == nil {
		return nil
	}
	if threadFilter == "" {
		return si.Threads
	}
	if ids, ok := si.Threads[threadFilter]; ok {
		return map[string][]string{threadFilter: ids}
	}
	return nil
}

// Generate produces a Graphviz DOT graph.
// stateFilter (if non-empty) restricts to GAMs in that state.
func Generate(tree *index.ProjectTree, diags map[*index.ProjectNode][]NodeDiag, stateFilter string) Result {
	return generate(tree, diags, genOpts{stateFilter: stateFilter})
}

// GenerateSubset produces a focused Graphviz DOT graph containing only the
// specified nodes (identified by their graphviz IDs from a prior Generate call)
// plus their connecting edges, with a re-optimised layout for that subset.
// Graphviz node IDs are preserved so they match /api/meta keys.
// The simplification level from the original Result is preserved.
func GenerateSubset(tree *index.ProjectTree, diags map[*index.ProjectNode][]NodeDiag, nodeIDs []string, existing Result) Result {
	subset := make(map[*index.ProjectNode]bool)
	var subsetGAMs []*index.ProjectNode
	for _, id := range nodeIDs {
		if n, ok := existing.GAMNodes[id]; ok {
			subset[n] = true
			subsetGAMs = append(subsetGAMs, n)
		}
		if n, ok := existing.DSNodes[id]; ok {
			subset[n] = true
		}
	}
	// Expand the subset to include every DS that any subset GAM connects to.
	// The caller's adjacency map may be stale (built from a previous focused
	// SVG), so some DS nodes reachable from the selected GAMs may have been
	// missing from nodeIDs.  Without this expansion those DS nodes would not
	// be declared in the generated DOT, causing Graphviz to auto-create plain
	// rectangles ("ghost nodes") wherever the edges reference them.
	for _, gam := range subsetGAMs {
		for _, dir := range []string{"InputSignals", "OutputSignals"} {
			c, ok := gam.Children[dir]
			if !ok {
				continue
			}
			for _, sig := range c.Children {
				ds, _ := resolveSignal(tree, sig, gam)
				if ds != nil {
					subset[ds] = true
				}
			}
		}
	}
	// Preserve the simplification level from the original result.
	opts := existing.GenOpts
	opts.subsetNodes = subset
	if opts.bypassLevel >= 2 {
		return generateSimplified(tree, diags, opts)
	}
	return generate(tree, diags, opts)
}

func generate(tree *index.ProjectTree, diags map[*index.ProjectNode][]NodeDiag, opts genOpts) Result {
	if diags == nil {
		diags = make(map[*index.ProjectNode][]NodeDiag)
	}

	// ── Collect GAMs and DSes ─────────────────────────────────────────────
	dsIDMap := make(map[*index.ProjectNode]string)
	gamIDMap := make(map[*index.ProjectNode]string)
	usedDS := make(map[string]bool)
	usedGAM := make(map[string]bool)
	var allDSS, allGAMs []*index.ProjectNode

	tree.Walk(func(n *index.ProjectNode) {
		if tree.IsDataSource(n) {
			dsIDMap[n] = makeID("ds", n.Name, usedDS)
			allDSS = append(allDSS, n)
		} else if tree.IsGAM(n) {
			gamIDMap[n] = makeID("fn", n.Name, usedGAM)
			allGAMs = append(allGAMs, n)
		}
	})
	sort.Slice(allDSS, func(i, j int) bool { return dsIDMap[allDSS[i]] < dsIDMap[allDSS[j]] })
	sort.Slice(allGAMs, func(i, j int) bool { return gamIDMap[allGAMs[i]] < gamIDMap[allGAMs[j]] })

	// ── Apply subset filter (GenerateSubset) ──────────────────────────────
	// IDs are already assigned for all nodes; filtering here keeps IDs consistent
	// with the full graph so meta-data lookups remain valid.
	if opts.subsetNodes != nil {
		var fg []*index.ProjectNode
		for _, g := range allGAMs {
			if opts.subsetNodes[g] {
				fg = append(fg, g)
			}
		}
		allGAMs = fg
		var fd []*index.ProjectNode
		for _, d := range allDSS {
			if opts.subsetNodes[d] {
				fd = append(fd, d)
			}
		}
		allDSS = fd
	}

	// ── Extract state info ────────────────────────────────────────────────
	states := extractStates(tree, gamIDMap)

	// ── Apply state filter to GAMs ────────────────────────────────────────
	gams := allGAMs
	if opts.stateFilter != "" {
		if si, ok := states[opts.stateFilter]; ok {
			threads := activeThreadsMap(si, opts.threadFilter)
			gamByID := make(map[string]*index.ProjectNode)
			for _, g := range allGAMs {
				gamByID[gamIDMap[g]] = g
			}
			activeSet := make(map[*index.ProjectNode]bool)
			for _, ids := range threads {
				for _, id := range ids {
					if g, ok := gamByID[id]; ok {
						activeSet[g] = true
					}
				}
			}
			var filtered []*index.ProjectNode
			for _, g := range allGAMs {
				if activeSet[g] {
					filtered = append(filtered, g)
				}
			}
			gams = filtered
		}
	}

	// ── Pre-compute all edges ─────────────────────────────────────────────
	// Use base DS IDs at this stage; we remap to split IDs below.
	var edges []edge
	dsReadSigs := make(map[*index.ProjectNode]map[string]bool)
	dsWriteSigs := make(map[*index.ProjectNode]map[string]bool)

	seenEdge := make(map[[4]string]bool)
	for _, n := range gams {
		gid := gamIDMap[n]
		if c, ok := n.Children["InputSignals"]; ok {
			for _, sig := range c.Children {
				ds, canon := resolveSignal(tree, sig, n)
				if ds == nil {
					continue
				}
				did, ok := dsIDMap[ds]
				if !ok {
					continue
				}
				sigReal := sig.RealName
				if sigReal == "" {
					sigReal = sig.Name
				}
				key := [4]string{did, "s_" + sanitize(canon), gid, "i_" + sanitize(sigReal)}
				if !seenEdge[key] {
					seenEdge[key] = true
					edges = append(edges, edge{did, gid, "s_" + sanitize(canon), "i_" + sanitize(sigReal), "#3d6fd6", false})
				}
				if dsReadSigs[ds] == nil {
					dsReadSigs[ds] = make(map[string]bool)
				}
				dsReadSigs[ds][canon] = true
			}
		}
		if c, ok := n.Children["OutputSignals"]; ok {
			for _, sig := range c.Children {
				ds, canon := resolveSignal(tree, sig, n)
				if ds == nil {
					continue
				}
				did, ok := dsIDMap[ds]
				if !ok {
					continue
				}
				sigReal := sig.RealName
				if sigReal == "" {
					sigReal = sig.Name
				}
				key := [4]string{gid, "o_" + sanitize(sigReal), did, "s_" + sanitize(canon)}
				if !seenEdge[key] {
					seenEdge[key] = true
					edges = append(edges, edge{gid, did, "o_" + sanitize(sigReal), "s_" + sanitize(canon), "#c87941", true})
				}
				if dsWriteSigs[ds] == nil {
					dsWriteSigs[ds] = make(map[string]bool)
				}
				dsWriteSigs[ds][canon] = true
			}
		}
	}

	// ── Apply state filter to DS ──────────────────────────────────────────
	dss := allDSS
	if opts.stateFilter != "" {
		connectedDS := make(map[*index.ProjectNode]bool)
		for _, e := range edges {
			for _, ds := range allDSS {
				id := dsIDMap[ds]
				if e.fromID == id || e.toID == id {
					connectedDS[ds] = true
				}
			}
		}
		var filtered []*index.ProjectNode
		for _, ds := range allDSS {
			if connectedDS[ds] {
				filtered = append(filtered, ds)
			}
		}
		dss = filtered
	}

	// ── Level-1 bypass: remove IOGAM + pass-through DS, add bypass edges ─
	// bypassEntry: a bypass edge that replaces one or more IOGAM/DS hops.
	// When fromPort/toPort are non-empty the edge uses port-based routing
	// (e.g. writerGAM:outPort → readerGAM:inPort for pass-through DS bypass).
	// When they are empty the edge connects at node level (IOGAM bypass).
	type bypassEntry struct {
		fromID, fromPort string
		toID, toPort     string
		color, style     string
	}
	var bypassEntries []bypassEntry

	if opts.bypassLevel >= 1 {
		// Identify IOGAM GAMs.
		ioGAMIDSet := make(map[string]bool)
		for _, g := range gams {
			if isIOGAM(g.Metadata["Class"]) {
				ioGAMIDSet[gamIDMap[g]] = true
			}
		}
		// Identify pass-through DS (GAMDataSource / AsynchThreadDataSource with writers).
		passThroughDSIDSet := make(map[string]bool)
		for _, ds := range dss {
			if !passThroughClasses[ds.Metadata["Class"]] {
				continue
			}
			for _, e := range edges {
				if e.isWrite && e.toID == dsIDMap[ds] {
					passThroughDSIDSet[dsIDMap[ds]] = true
					break
				}
			}
		}

		// Pass-through DS bypass: writerGAM:outPort → readerGAM:inPort
		// Both GAMs already have the signal in their HTML-table; we draw a direct
		// dashed edge between the matching output port and input port.
		type sigPortKey struct{ dsID, dsPort string }
		writerPortEdge := make(map[sigPortKey]edge)
		readerPortEdges := make(map[sigPortKey][]edge)
		for _, e := range edges {
			if passThroughDSIDSet[e.toID] && e.isWrite {
				k := sigPortKey{e.toID, e.toPort}
				if _, exists := writerPortEdge[k]; !exists {
					writerPortEdge[k] = e
				}
			}
			if passThroughDSIDSet[e.fromID] && !e.isWrite {
				k := sigPortKey{e.fromID, e.fromPort}
				readerPortEdges[k] = append(readerPortEdges[k], e)
			}
		}
		seen3 := make(map[[4]string]bool)
		for k, we := range writerPortEdge {
			for _, re := range readerPortEdges[k] {
				if we.fromID == re.toID {
					continue // skip self-loop
				}
				bk := [4]string{we.fromID, we.fromPort, re.toID, re.toPort}
				if seen3[bk] {
					continue
				}
				seen3[bk] = true
				bypassEntries = append(bypassEntries, bypassEntry{
					fromID: we.fromID, fromPort: we.fromPort,
					toID:   re.toID, toPort: re.toPort,
					color: "#40a060", style: "dashed",
				})
			}
		}

		// IOGAM bypass: srcDS → dstDS (node-level dashed edge).
		// If srcDS is itself a pass-through DS, chain one level to its writers.
		seen2 := make(map[[2]string]bool)
		for iogamID := range ioGAMIDSet {
			srcIDs2 := make(map[string]bool)
			dstIDs2 := make(map[string]bool)
			for _, e := range edges {
				if !e.isWrite && e.toID == iogamID { // IOGAM reads from DS
					if passThroughDSIDSet[e.fromID] {
						// One-level chain: use writers of that pass-through DS.
						for _, we := range edges {
							if we.isWrite && we.toID == e.fromID && !ioGAMIDSet[we.fromID] {
								srcIDs2[we.fromID] = true
							}
						}
					} else {
						srcIDs2[e.fromID] = true
					}
				}
				if e.isWrite && e.fromID == iogamID { // IOGAM writes to DS
					dstIDs2[e.toID] = true
				}
			}
			for srcID := range srcIDs2 {
				for dstID := range dstIDs2 {
					bk := [2]string{srcID, dstID}
					if seen2[bk] {
						continue
					}
					seen2[bk] = true
					bypassEntries = append(bypassEntries, bypassEntry{
						fromID: srcID, toID: dstID,
						color: "#9060c0", style: "dashed",
					})
				}
			}
		}

		// Filter edges, gams, dss to remove bypassed nodes.
		var filteredEdges []edge
		for _, e := range edges {
			if ioGAMIDSet[e.fromID] || ioGAMIDSet[e.toID] {
				continue
			}
			if passThroughDSIDSet[e.fromID] || passThroughDSIDSet[e.toID] {
				continue
			}
			filteredEdges = append(filteredEdges, e)
		}
		edges = filteredEdges

		var filteredGAMs []*index.ProjectNode
		for _, g := range gams {
			if !ioGAMIDSet[gamIDMap[g]] {
				filteredGAMs = append(filteredGAMs, g)
			}
		}
		gams = filteredGAMs

		var filteredDSS []*index.ProjectNode
		for _, ds := range dss {
			if !passThroughDSIDSet[dsIDMap[ds]] {
				filteredDSS = append(filteredDSS, ds)
			}
		}
		dss = filteredDSS

		// Also remove pass-through DS entries from dsReadSigs/dsWriteSigs so
		// the DS signal tables are not built for bypassed nodes.
		for id := range passThroughDSIDSet {
			for ds, mapID := range dsIDMap {
				if mapID == id {
					delete(dsReadSigs, ds)
					delete(dsWriteSigs, ds)
				}
			}
		}
	}

	// ── Minimal-split algorithm ──────────────────────────────────────────
	// Goal: only split a DS into read-clone (_r) + main when a GAM reads
	// from it *before* any GAM has written to it in the same cycle.
	//
	// Key insight: in MARTe2 the execution order is defined by the thread
	// configuration, NOT by signal dependency (which is cyclic for shared
	// GAMDataSource buffers where every GAM both reads and writes).
	//
	// Phase 1: assign each GAM its execution position within its thread
	//   (0-based).  Concurrent threads share the same position space.
	//   If stateFilter is set, only that state's threads are used.
	//
	// Phase 2: for each DS, find the *earliest* writer position in the
	//   thread execution sequence.
	//
	// Phase 3: flag read edges where the reader executes before the first
	//   writer as "early".  Only those signals need a read clone.

	// Phase 1: GAM execution ranks derived from thread Functions order.
	//
	// With a state filter: positions come from that state's threads only (0-based
	// index in the Functions list). GAMs in a later thread that share names with
	// an earlier thread take the max position, mirroring MARTe2 scheduling.
	//
	// Without a filter: compute a topological (longest-path) rank across the
	// union of all thread orderings. For every pair of consecutive GAMs A, B in
	// any Functions list the constraint A < B is recorded; the longest path to
	// each GAM gives its column rank so the merged graph reflects the combined
	// execution order, e.g.:
	//   State1.ThreadA: [G1, G2, G3]  →  G1→G2, G2→G3
	//   State2.ThreadB: [G2, G4, G3]  →  G2→G4, G4→G3
	//   Result: G1(0) G2(1) G4(2) G3(3)
	gamThreadPos := make(map[string]int) // graphviz GAM ID → execution rank

	// Initialise every known GAM to rank 0.
	for _, g := range allGAMs {
		gamThreadPos[gamIDMap[g]] = 0
	}

	if opts.stateFilter != "" {
		// Filtered view: use positions from this state's threads only.
		if si, ok := states[opts.stateFilter]; ok {
			for _, ids := range activeThreadsMap(si, opts.threadFilter) {
				for pos, id := range ids {
					if cur := gamThreadPos[id]; pos > cur {
						gamThreadPos[id] = pos
					}
				}
			}
		}
	} else {
		// Unfiltered: build ordering constraints from all states/threads and
		// compute the topological longest-path rank for each GAM.
		type orderEdge struct{ from, to string }
		var orderEdges []orderEdge
		for _, si := range states {
			for _, ids := range si.Threads {
				for i := 1; i < len(ids); i++ {
					orderEdges = append(orderEdges, orderEdge{ids[i-1], ids[i]})
				}
			}
		}
		// Bellman-Ford longest path: rank[to] = max(rank[to], rank[from]+1).
		for range orderEdges {
			changed := false
			for _, e := range orderEdges {
				r := gamThreadPos[e.from]
				if cur := gamThreadPos[e.to]; cur < r+1 {
					gamThreadPos[e.to] = r + 1
					changed = true
				}
			}
			if !changed {
				break
			}
		}
	}

	// Phase 2: DS last-write position = max thread position among writers.
	//
	// Using MAX (not min) is critical for shared GAMDataSource buffers (DDB).
	// In MARTe2 a DDB accumulates writes throughout the cycle: each GAM reads
	// the most-recently-written values and then writes its own outputs.  A GAM
	// that executes *before* the last writer reads the *previous* cycle's value
	// of that writer's signals — an early read.  Only GAMs that execute *after*
	// the last writer can read all signals from the current cycle.
	//
	// Using min instead would declare only readers before the *first* write as
	// "early", leaving mid-thread readers (e.g. GoWaitHVONGAM at pos 6 when
	// the last writer ChoiceGAM is at pos 11) as late readers of a high-ranked
	// DS node — causing backward edges and rank inflation when relaxation fires.
	dsLastWritePos := make(map[string]int) // DS base ID → last-write pos
	for _, e := range edges {
		if !e.isWrite {
			continue
		}
		pos := gamThreadPos[e.fromID] // 0 if not in any thread
		if p, ok := dsLastWritePos[e.toID]; !ok || pos > p {
			dsLastWritePos[e.toID] = pos
		}
	}

	// Phase 3: identify early-read ports per DS.
	// A read is "early" (reads previous-cycle value) when:
	//   readerPos <= lastWritePos
	// This covers two cases:
	//   • same-thread readers that execute before the last writer (previous-cycle data)
	//   • cross-thread readers at the same or lower position (concurrent threads,
	//     always buffered via bridge DSes — previous-cycle data by design)
	dsEarlyReadPorts := make(map[string]map[string]bool) // DS base ID → port IDs
	for _, e := range edges {
		if e.isWrite {
			continue
		}
		lastWrite, hasWriters := dsLastWritePos[e.fromID]
		if !hasWriters {
			continue // pure source DS, no split needed
		}
		readerPos := gamThreadPos[e.toID] // 0 if not in any thread
		if readerPos <= lastWrite {
			if dsEarlyReadPorts[e.fromID] == nil {
				dsEarlyReadPorts[e.fromID] = make(map[string]bool)
			}
			dsEarlyReadPorts[e.fromID][e.fromPort] = true
		}
	}

	// Build split map: only split when early-read signals exist.
	// Mixed DS: read clone (_r) for early-read signals only; main node (base)
	// for write signals + late-read signals.  No "_w" suffix on the main node.
	dsSplitMap := make(map[*index.ProjectNode]*dsSplitEntry)
	for _, ds := range dss {
		base := dsIDMap[ds]
		earlyPorts := dsEarlyReadPorts[base]
		if len(earlyPorts) > 0 {
			dsSplitMap[ds] = &dsSplitEntry{
				readID:         base + "_r",
				writeID:        base,
				isMixed:        true,
				earlyReadPorts: earlyPorts,
			}
		} else {
			dsSplitMap[ds] = &dsSplitEntry{
				readID:  base,
				writeID: base,
				isMixed: false,
			}
		}
	}

	// ── Remap edge endpoints to split IDs ─────────────────────────────────
	baseIDToDS := make(map[string]*index.ProjectNode)
	for _, ds := range dss {
		baseIDToDS[dsIDMap[ds]] = ds
	}

	for i := range edges {
		e := &edges[i]
		if e.isWrite {
			// GAM → DS: always goes to the main node (writeID = base, no suffix).
			if ds, ok := baseIDToDS[e.toID]; ok {
				e.toID = dsSplitMap[ds].writeID
			}
		} else {
			// DS → GAM: early-read ports go to the clone (_r); all others stay at main.
			if ds, ok := baseIDToDS[e.fromID]; ok {
				sp := dsSplitMap[ds]
				if sp.isMixed && sp.earlyReadPorts[e.fromPort] {
					e.fromID = sp.readID // clone node
				}
				// else: stays at base = main node (sp.writeID)
			}
		}
	}

	// ── Build implicit signal registry for DS labels ───────────────────────
	implicitSigs := make(map[*index.ProjectNode]map[string]bool)
	for _, n := range gams {
		for _, dir := range []string{"InputSignals", "OutputSignals"} {
			c, ok := n.Children[dir]
			if !ok {
				continue
			}
			for _, sig := range c.Children {
				ds, canon := resolveSignal(tree, sig, n)
				if ds == nil || canon == "" {
					continue
				}
				norm := index.NormalizeName(canon)
				if sigsCont, ok := ds.Children["Signals"]; ok {
					if _, exists := sigsCont.Children[norm]; exists {
						continue
					}
				}
				if implicitSigs[ds] == nil {
					implicitSigs[ds] = make(map[string]bool)
				}
				implicitSigs[ds][canon] = true
			}
		}
	}

	meta := make(map[string]NodeInfo)

	// ── dot rank assignment ───────────────────────────────────────────────
	// srcIDs: nodes that should be at the left edge of the graph (rank 0).
	//   • Pure source DS (no writers) — single node.
	//   • Read clones (_r) of mixed DS — provides previous-cycle values.
	// snkIDs: nodes that should be at the right edge (rank max).
	//   • Pure sink DS (no readers) — single node.
	//   • Main node of mixed DS — accumulates written values.
	// "Middle" DS (single node, both readers and writers but no early reads)
	//   appear in neither list; BFS places them between their writers and readers.
	// Mixed DS main nodes are NOT added to snkIDs — they have a computed rank
	// (right after their writers) and must not be forced to rank=max.
	var srcIDs, snkIDs []string
	var mixedMainIDs []string // main nodes of split DS — placed by computed rank, not rank=max
	for _, ds := range dss {
		split := dsSplitMap[ds]
		if split.isMixed {
			srcIDs = append(srcIDs, split.readID)              // clone always leftmost (rank=min)
			mixedMainIDs = append(mixedMainIDs, split.writeID) // rank from nodeRank
		} else if len(dsWriteSigs[ds]) == 0 {
			srcIDs = append(srcIDs, split.readID) // pure source
		} else if len(dsReadSigs[ds]) == 0 {
			snkIDs = append(snkIDs, split.writeID) // pure sink
		}
		// else: single-node mixed DS with no early reads → let rank assignment place it
	}
	// ── Thread-position rank assignment ──────────────────────────────────
	// Assign dot ranks directly from thread execution positions so the graph
	// reflects the real-time execution order instead of collapsing everything
	// into 2-3 levels (which happens when BFS is used on a cyclic DDB graph).
	//
	// Rank layout:
	//   0          — source DS / read clones (rank=min anchor)
	//   pos + 1    — GAM at thread position pos
	//   max(writers' GAM ranks) + 1 — DS written after its writers (incl. mixed DS main nodes)
	//   maxRank    — pure-sink DS (rank=max anchor; no readers so naturally rightmost)

	nodeRank := make(map[string]int)

	// Source DS and read clones → rank 0.
	for _, id := range srcIDs {
		nodeRank[id] = 0
	}

	// GAMs → rank = thread_position + 1.
	// GAMs not found in any thread default to position 0 (rank 1).
	for _, n := range gams {
		gid := gamIDMap[n]
		pos := gamThreadPos[gid] // 0 if absent
		nodeRank[gid] = pos + 1
	}

	// DS nodes that receive write edges → rank = max(writer_rank) + 1.
	// Iterate edges (already remapped) to find write targets.
	for _, e := range edges {
		if !e.isWrite {
			continue
		}
		writerRank := nodeRank[e.fromID]
		dsRank := writerRank + 1
		if existing, ok := nodeRank[e.toID]; !ok || dsRank > existing {
			nodeRank[e.toID] = dsRank
		}
	}

	// Relaxation: enforce rank(consumer_GAM) > rank(DS_it_reads).
	//
	// Thread positions give the correct intra-thread order, but don't account
	// for GAMs that execute *after* a DS has been written: e.g. EPICSThSyncGAM
	// at pos 7 (rank 8) reading DDB1 which was last written at rank 7 → DDB1
	// rank = 8 = EPICSThSyncGAM rank (same column, confusing).
	//
	// Algorithm: Bellman-Ford over read edges, skipping read-modify-write pairs
	// (GAM reads AND writes the same DS) to prevent infinite cycles from shared
	// DDB buffers. Thread positions act as rank floors, so ranks only increase.
	gamWriteTargets := make(map[string]map[string]bool) // gamID → DS IDs it writes to
	for _, e := range edges {
		if !e.isWrite {
			continue
		}
		if gamWriteTargets[e.fromID] == nil {
			gamWriteTargets[e.fromID] = make(map[string]bool)
		}
		gamWriteTargets[e.fromID][e.toID] = true
	}
	// Relaxation: enforce rank(consumer_GAM) > rank(DS_it_reads).
	//
	// With dsLastWritePos-based early-read detection, most inter-thread and
	// mid-thread readers are now clone readers (rank=min), eliminating the
	// backward edges that previously caused rank inflation.  Only the small
	// set of GAMs that execute *after* the last writer read the main DS node.
	// Bellman-Ford is safe here because those remaining readers have a nearly
	// acyclic dependency structure.
	//
	// Read-modify-write pairs (GAM reads AND writes the same DS) are skipped
	// to break direct cycles in the remaining graph.
	for range edges { // Bellman-Ford: at most |edges| iterations
		changed := false
		for _, e := range edges {
			if e.isWrite {
				continue
			}
			// Skip read-modify-write: GAM reads and writes the same DS (cycle risk).
			if gamWriteTargets[e.toID][e.fromID] {
				continue
			}
			dsRank, ok := nodeRank[e.fromID]
			if !ok {
				continue
			}
			needed := dsRank + 1
			if cur, ok2 := nodeRank[e.toID]; !ok2 || cur < needed {
				nodeRank[e.toID] = needed
				changed = true
			}
		}
		// Recompute DS ranks from updated GAM ranks.
		for _, e := range edges {
			if !e.isWrite {
				continue
			}
			writerRank := nodeRank[e.fromID]
			dsRank := writerRank + 1
			if existing, ok := nodeRank[e.toID]; !ok || dsRank > existing {
				nodeRank[e.toID] = dsRank
				changed = true
			}
		}
		if !changed {
			break
		}
	}

	// Ensure all DS nodes that didn't receive write edges still have a rank.
	maxRank := 1
	for _, r := range nodeRank {
		if r > maxRank {
			maxRank = r
		}
	}
	for _, id := range append(snkIDs, mixedMainIDs...) {
		if _, ok := nodeRank[id]; !ok {
			nodeRank[id] = maxRank
		}
	}
	// Recompute maxRank after fallback assignment.
	for _, r := range nodeRank {
		if r > maxRank {
			maxRank = r
		}
	}

	// Collect all node IDs grouped by rank (used later for {rank=same}).
	// mixedMainIDs are explicitly included so they get {rank=same} at their computed rank.
	// Middle DS (single-node mixed with no early reads) must also be included.
	inOrderedSet := make(map[string]bool)
	for _, id := range srcIDs {
		inOrderedSet[id] = true
	}
	for _, id := range snkIDs {
		inOrderedSet[id] = true
	}
	for _, id := range mixedMainIDs {
		inOrderedSet[id] = true
	}
	allOrderedIDs := append([]string{}, srcIDs...)
	for _, n := range gams {
		allOrderedIDs = append(allOrderedIDs, gamIDMap[n])
	}
	allOrderedIDs = append(allOrderedIDs, mixedMainIDs...)
	allOrderedIDs = append(allOrderedIDs, snkIDs...)
	for _, ds := range dss {
		id := dsSplitMap[ds].readID // == writeID for non-mixed
		if !inOrderedSet[id] {
			allOrderedIDs = append(allOrderedIDs, id)
		}
	}

	// Group nodes by BFS rank for {rank=same} dot constraints.
	rankNodes := make(map[int][]string)
	for _, id := range allOrderedIDs {
		r, ok := nodeRank[id]
		if !ok {
			r = maxRank / 2
		}
		rankNodes[r] = append(rankNodes[r], id)
	}

	var sb strings.Builder
	sb.WriteString("digraph MARTe {\n")
	sb.WriteString("  bgcolor=\"transparent\";\n")
	// dot: hierarchical left-to-right layout — the right engine for DAG signal flow.
	// Our minimal-split algorithm ensures the graph is acyclic so dot works well.
	// ranksep controls horizontal distance between rank columns;
	// nodesep controls vertical gap between nodes in the same column.
	sb.WriteString("  layout=dot;\n")
	sb.WriteString("  rankdir=LR;\n")
	sb.WriteString("  ranksep=0.7;\n")
	sb.WriteString("  nodesep=0.35;\n")
	sb.WriteString("  splines=spline;\n")
	sb.WriteString("  node [shape=none, margin=0, fontname=\"Helvetica\"];\n")
	sb.WriteString("  edge [fontname=\"Helvetica\", fontsize=7, arrowsize=0.5, arrowhead=open];\n\n")

	for _, ds := range dss {
		split := dsSplitMap[ds]
		nd := diags[ds]
		dispName := ds.RealName
		if dispName == "" {
			dispName = ds.Name
		}

		allSigs := buildDSSigs(ds, dsReadSigs[ds], dsWriteSigs[ds], implicitSigs[ds], diags)

		if split.isMixed {
			cloneGroup := []string{split.readID, split.writeID}

			// Read clone — only early-read signals, always shown as sources.
			cloneSigs := filterDSSigsByPorts(allSigs, split.earlyReadPorts)
			for i := range cloneSigs {
				cloneSigs[i].Dir = "ds-out" // clone always provides, never receives
			}
			fmt.Fprintf(&sb, "  %s [label=%s];\n", split.readID,
				dsLabel(dispName, ds.Metadata["Class"], cloneSigs, nd, "r"))
			meta[split.readID] = NodeInfo{
				Name: dispName, Kind: "ds", Class: ds.Metadata["Class"],
				Doc: ds.Doc, Conditional: ds.IsConditional,
				Fields: collectFields(ds), DSSigs: cloneSigs, Diags: nd,
				SplitSide: "r", CloneGroup: cloneGroup,
			}

			// Main node — write signals + late-read signals.
			// For signals that are early-read AND written (feedback), also show
			// the write side here (so write arrows land on the main node).
			mainSigs := filterDSSigsForMain(allSigs, split.earlyReadPorts)
			splitSide := "m"
			if len(dsReadSigs[ds]) == len(split.earlyReadPorts) {
				splitSide = "w" // all reads are early → pure sink main node
			}
			fmt.Fprintf(&sb, "  %s [label=%s];\n", split.writeID,
				dsLabel(dispName, ds.Metadata["Class"], mainSigs, nd, splitSide))
			meta[split.writeID] = NodeInfo{
				Name: dispName, Kind: "ds", Class: ds.Metadata["Class"],
				Doc: ds.Doc, Conditional: ds.IsConditional,
				Fields: collectFields(ds), DSSigs: mainSigs, Diags: nd,
				SplitSide: splitSide, CloneGroup: cloneGroup,
			}
		} else {
			id := dsIDMap[ds]
			fmt.Fprintf(&sb, "  %s [label=%s];\n", id,
				dsLabel(dispName, ds.Metadata["Class"], allSigs, nd, ""))
			meta[id] = NodeInfo{
				Name: dispName, Kind: "ds", Class: ds.Metadata["Class"],
				Doc: ds.Doc, Conditional: ds.IsConditional,
				Fields: collectFields(ds), DSSigs: allSigs, Diags: nd,
			}
		}
	}
	sb.WriteString("\n")

	// ── GAM nodes ─────────────────────────────────────────────────────────
	writeGAMNode := func(n *index.ProjectNode, indent string) {
		gid := gamIDMap[n]
		inS, outS := buildGAMSigs(tree, n, diags)
		isIO := isIOGAM(n.Metadata["Class"])
		nd := diags[n]
		gamName := n.RealName
		if gamName == "" {
			gamName = n.Name
		}
		var lbl string
		if isIO {
			lbl = iogamLabel(gamName, n.Metadata["Class"], inS, outS, nd, n.IsConditional)
		} else {
			lbl = gamLabel(gamName, n.Metadata["Class"], inS, outS, nd, n.IsConditional)
		}
		fmt.Fprintf(&sb, "%s%s [label=%s];\n", indent, gid, lbl)
		meta[gid] = NodeInfo{
			Name: gamName, Kind: "gam", Class: n.Metadata["Class"],
			Doc: n.Doc, Conditional: n.IsConditional, IOGAM: isIO,
			Fields: collectFields(n), InSigs: inS, OutSigs: outS, Diags: nd,
		}
	}

	if opts.stateFilter != "" {
		si := states[opts.stateFilter]
		threads := activeThreadsMap(si, opts.threadFilter)
		var threadNames []string
		for t := range threads {
			threadNames = append(threadNames, t)
		}
		sort.Strings(threadNames)
		gamByID := make(map[string]*index.ProjectNode)
		for _, g := range gams {
			gamByID[gamIDMap[g]] = g
		}

		for _, threadName := range threadNames {
			ids := threads[threadName]
			fmt.Fprintf(&sb, "  subgraph cluster_%s {\n", sanitize(threadName))
			fmt.Fprintf(&sb, "    label=<%s>;\n", he(threadName))
			sb.WriteString("    color=\"#30363d\"; penwidth=1.5;\n")
			sb.WriteString("    fontname=\"Helvetica\"; fontsize=10; fontcolor=\"#7a8899\";\n")
			for _, id := range ids {
				n, ok := gamByID[id]
				if !ok {
					continue
				}
				writeGAMNode(n, "    ")
			}
			sb.WriteString("  }\n\n")
		}
	} else {
		for _, n := range gams {
			writeGAMNode(n, "  ")
		}
	}
	sb.WriteString("\n")

	// ── Edges ─────────────────────────────────────────────────────────────
	// Use compass points :e (east) on the source port and :w (west) on the
	// destination port so every arrow exits/enters at the correct horizontal
	// edge of its node, vertically aligned with its signal row.
	for _, e := range edges {
		attrs := fmt.Sprintf("color=%q, penwidth=1.2", e.color)
		fmt.Fprintf(&sb, "  %s:%s:e -> %s:%s:w [%s];\n",
			e.fromID, e.fromPort, e.toID, e.toPort, attrs)
	}

	// Level-1 bypass edges (dashed, drawn after regular edges).
	for _, be := range bypassEntries {
		if be.fromPort != "" {
			fmt.Fprintf(&sb, "  %s:%s:e -> %s:%s:w [color=%q, style=%s, penwidth=1.0];\n",
				be.fromID, be.fromPort, be.toID, be.toPort, be.color, be.style)
		} else {
			fmt.Fprintf(&sb, "  %s -> %s [color=%q, style=%s, penwidth=1.0];\n",
				be.fromID, be.toID, be.color, be.style)
		}
	}

	// ── Rank constraints ──────────────────────────────────────────────────
	// {rank=min}  — source DS and read clones pinned to the leftmost column.
	// {rank=same} — all other multi-node ranks aligned in the same column,
	//               including mixed DS main nodes at their computed rank so
	//               they appear strictly after their writers, not at rank=max.
	// {rank=max}  — pure-sink DS (only written, never read) pinned rightmost.
	//               Mixed DS main nodes are NOT in this group — they have a
	//               meaningful computed rank and must not be overridden.
	sb.WriteString("\n")
	if len(srcIDs) > 0 {
		sb.WriteString("  { rank=min;")
		for _, id := range srcIDs {
			fmt.Fprintf(&sb, " %s;", id)
		}
		sb.WriteString(" }\n")
	}
	if len(snkIDs) > 0 {
		sb.WriteString("  { rank=max;")
		for _, id := range snkIDs {
			fmt.Fprintf(&sb, " %s;", id)
		}
		sb.WriteString(" }\n")
	}
	// Intermediate ranks — group same-rank nodes in the same column.
	var sortedRanks []int
	for r := range rankNodes {
		sortedRanks = append(sortedRanks, r)
	}
	sort.Ints(sortedRanks)
	// Build a set of IDs that are already covered by rank=min / rank=max so we
	// can skip them in {rank=same} (conflicting constraints confuse dot).
	coveredByAnchor := make(map[string]bool)
	for _, id := range srcIDs {
		coveredByAnchor[id] = true
	}
	for _, id := range snkIDs {
		coveredByAnchor[id] = true
	}
	for _, r := range sortedRanks {
		ids := rankNodes[r]
		if len(ids) <= 1 {
			continue // single-node rank needs no constraint
		}
		// Filter out nodes already covered by rank=min / rank=max anchors.
		var freeIDs []string
		for _, id := range ids {
			if !coveredByAnchor[id] {
				freeIDs = append(freeIDs, id)
			}
		}
		if len(freeIDs) <= 1 {
			continue
		}
		sb.WriteString("  { rank=same;")
		for _, id := range freeIDs {
			fmt.Fprintf(&sb, " %s;", id)
		}
		sb.WriteString(" }\n")
	}

	sb.WriteString("}\n")

	allGAMIDs := make(map[string]bool)
	for _, g := range gams {
		allGAMIDs[gamIDMap[g]] = true
	}

	// Build node-lookup maps: graphviz ID → ProjectNode.
	// These are used by GenerateSubset to map user-selected IDs back to tree nodes.
	// We include all nodes in gamIDMap/dsIDMap so that dim nodes (hidden by
	// state filter but still present in the SVG) are also resolvable.
	gamNodes := make(map[string]*index.ProjectNode)
	for n, id := range gamIDMap {
		gamNodes[id] = n
	}
	dsNodes := make(map[string]*index.ProjectNode)
	for n, id := range dsIDMap {
		dsNodes[id] = n
	}
	// For split DS, also map the _r clone ID to the same ProjectNode.
	for _, ds := range dss {
		if sp := dsSplitMap[ds]; sp.isMixed {
			dsNodes[sp.readID] = ds
		}
	}

	return Result{DOT: sb.String(), Meta: meta, States: states, AllGAMIDs: allGAMIDs, GAMNodes: gamNodes, DSNodes: dsNodes, GenOpts: opts}
}

// buildDSSigs builds the full signal list for a DS (explicit + implicit).
func buildDSSigs(
	ds *index.ProjectNode,
	readMap, writeMap, implicitMap map[string]bool,
	diags map[*index.ProjectNode][]NodeDiag,
) []SigInfo {
	var sigs []SigInfo
	if cont, ok := ds.Children["Signals"]; ok {
		for _, sig := range cont.Children {
			realName := sig.RealName
			if realName == "" {
				realName = sig.Name
			}
			isRead := readMap != nil && readMap[realName]
			isWrite := writeMap != nil && writeMap[realName]
			dir := "ds-both"
			if isRead && !isWrite {
				dir = "ds-out"
			}
			if isWrite && !isRead {
				dir = "ds-in"
			}
			sigs = append(sigs, SigInfo{
				LocalName: realName, DSName: realName,
				Type: sig.Metadata["Type"], NumElems: sig.Metadata["NumberOfElements"],
				Doc: sig.Doc, Dir: dir, Implicit: false,
				PortID: "s_" + sanitize(realName), Diags: diags[sig],
			})
		}
	}
	sort.Slice(sigs, func(i, j int) bool { return sigs[i].LocalName < sigs[j].LocalName })

	if implicitMap != nil {
		var names []string
		for name := range implicitMap {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			isRead := readMap != nil && readMap[name]
			isWrite := writeMap != nil && writeMap[name]
			dir := "ds-both"
			if isRead && !isWrite {
				dir = "ds-out"
			}
			if isWrite && !isRead {
				dir = "ds-in"
			}
			sigs = append(sigs, SigInfo{
				LocalName: name, DSName: name,
				Dir: dir, Implicit: true,
				PortID: "s_" + sanitize(name),
			})
		}
	}
	return sigs
}

// filterDSSigsByPorts returns signals whose PortID is in the given set.
// Used to select early-read signals for the read clone node.
func filterDSSigsByPorts(sigs []SigInfo, ports map[string]bool) []SigInfo {
	var out []SigInfo
	for _, s := range sigs {
		if ports[s.PortID] {
			out = append(out, s)
		}
	}
	return out
}

// filterDSSigsForMain returns signals for the main (write-side) node of a split DS.
// It includes:
//   - All signals NOT in earlyPorts (late-read and write signals).
//   - For signals in earlyPorts that are also written (Dir "ds-in" or "ds-both"):
//     include them as "ds-in" only so the write arrow can land on the main node.
func filterDSSigsForMain(sigs []SigInfo, earlyPorts map[string]bool) []SigInfo {
	var out []SigInfo
	for _, s := range sigs {
		if !earlyPorts[s.PortID] {
			out = append(out, s)
		} else if s.Dir == "ds-in" || s.Dir == "ds-both" {
			// Feedback signal: early-read AND written — show write side in main.
			sCopy := s
			sCopy.Dir = "ds-in"
			out = append(out, sCopy)
		}
		// early-read-only signals: appear in clone only, not in main.
	}
	return out
}

// buildGAMSigs extracts sorted InputSignals and OutputSignals for a GAM.
func buildGAMSigs(tree *index.ProjectTree, n *index.ProjectNode, diags map[*index.ProjectNode][]NodeDiag) (inSigs, outSigs []SigInfo) {
	if c, ok := n.Children["InputSignals"]; ok {
		for _, sig := range c.Children {
			ds, canon := resolveSignal(tree, sig, n)
			dsLbl := ""
			if ds != nil {
				dsLbl = realName(ds) + "." + canon
			}
			rn := sig.RealName
			if rn == "" {
				rn = sig.Name
			}
			inSigs = append(inSigs, SigInfo{
				LocalName: rn, DSName: dsLbl,
				Type: sig.Metadata["Type"], NumElems: sig.Metadata["NumberOfElements"],
				Doc: sig.Doc, Dir: "in",
				PortID: "i_" + sanitize(rn), Diags: diags[sig],
			})
		}
		sort.Slice(inSigs, func(i, j int) bool { return inSigs[i].LocalName < inSigs[j].LocalName })
	}
	if c, ok := n.Children["OutputSignals"]; ok {
		for _, sig := range c.Children {
			ds, canon := resolveSignal(tree, sig, n)
			dsLbl := ""
			if ds != nil {
				dsLbl = realName(ds) + "." + canon
			}
			rn := sig.RealName
			if rn == "" {
				rn = sig.Name
			}
			outSigs = append(outSigs, SigInfo{
				LocalName: rn, DSName: dsLbl,
				Type: sig.Metadata["Type"], NumElems: sig.Metadata["NumberOfElements"],
				Doc: sig.Doc, Dir: "out",
				PortID: "o_" + sanitize(rn), Diags: diags[sig],
			})
		}
		sort.Slice(outSigs, func(i, j int) bool { return outSigs[i].LocalName < outSigs[j].LocalName })
	}
	return
}

func realName(n *index.ProjectNode) string {
	if n.RealName != "" {
		return n.RealName
	}
	return n.Name
}

func isIOGAM(class string) bool {
	return class == "IOGAM" || strings.HasSuffix(class, "IOGAM")
}

// ── State extraction ──────────────────────────────────────────────────────────

func extractStates(tree *index.ProjectTree, gamIDMap map[*index.ProjectNode]string) map[string]*StateInfo {
	states := make(map[string]*StateInfo)
	tree.Walk(func(n *index.ProjectNode) {
		if n.Metadata["Class"] != "RealTimeApplication" {
			return
		}
		statesNode := n.Children["States"]
		if statesNode == nil {
			return
		}
		for _, state := range statesNode.Children {
			if state.Metadata["Class"] != "RealTimeState" {
				continue
			}
			si := &StateInfo{Threads: make(map[string][]string)}
			findThreads := func(container *index.ProjectNode) {
				for _, child := range container.Children {
					if child.Metadata["Class"] != "RealTimeThread" {
						continue
					}
					ids := getThreadGAMIDs(tree, child, gamIDMap)
					if len(ids) > 0 {
						si.Threads[child.Name] = ids
					}
				}
			}
			if tc, ok := state.Children["Threads"]; ok {
				findThreads(tc)
			}
			findThreads(state)
			if len(si.Threads) > 0 {
				states[state.Name] = si
			}
		}
	})
	return states
}

func getThreadGAMIDs(tree *index.ProjectTree, thread *index.ProjectNode, gamIDMap map[*index.ProjectNode]string) []string {
	var ids []string
	for _, frag := range thread.Fragments {
		for _, def := range frag.Definitions {
			f, ok := def.(*parser.Field)
			if !ok || f.Name != "Functions" {
				continue
			}
			arr, ok := f.Value.(*parser.ArrayValue)
			if !ok {
				continue
			}
			for _, elem := range arr.Elements {
				var name string
				switch v := elem.(type) {
				case *parser.ReferenceValue:
					name = v.Value
				case *parser.StringValue:
					name = v.Value
				}
				if name == "" {
					continue
				}
				gam := tree.ResolveName(thread, name, tree.IsGAM)
				if gam != nil {
					if id, ok := gamIDMap[gam]; ok {
						ids = append(ids, id)
					}
				}
			}
		}
	}
	return ids
}

// ── Signal resolution ─────────────────────────────────────────────────────────

func resolveSignal(tree *index.ProjectTree, sig *index.ProjectNode, gam *index.ProjectNode) (*index.ProjectNode, string) {
	ds, canon := tree.GetSignalInfo(sig)
	if ds != nil {
		return ds, canon
	}

	sigName := sig.RealName
	if sigName == "" {
		sigName = sig.Name
	}
	if alias := getFieldValue(sig, "Alias"); alias != "" {
		sigName = alias
	}

	for cur := gam.Parent; cur != nil; cur = cur.Parent {
		if v := getFieldValue(cur, "DefaultDataSource"); v != "" {
			dsNode := tree.ResolveName(gam, v, tree.IsDataSource)
			return dsNode, sigName
		}
	}
	return nil, ""
}

func getFieldValue(n *index.ProjectNode, key string) string {
	for _, frag := range n.Fragments {
		for _, def := range frag.Definitions {
			if f, ok := def.(*parser.Field); ok && f.Name == key {
				switch v := f.Value.(type) {
				case *parser.StringValue:
					return v.Value
				case *parser.ReferenceValue:
					return v.Value
				}
			}
		}
	}
	return ""
}

func collectFields(n *index.ProjectNode) map[string]string {
	skip := map[string]bool{
		"Class": true, "Type": true, "NumberOfElements": true,
		"DataSource": true, "Alias": true,
	}
	result := make(map[string]string)
	for _, frag := range n.Fragments {
		for _, def := range frag.Definitions {
			if f, ok := def.(*parser.Field); ok && !skip[f.Name] {
				switch v := f.Value.(type) {
				case *parser.StringValue:
					result[f.Name] = v.Value
				case *parser.IntValue:
					result[f.Name] = v.Raw
				case *parser.FloatValue:
					result[f.Name] = v.Raw
				case *parser.ReferenceValue:
					result[f.Name] = v.Value
				case *parser.BoolValue:
					if v.Value {
						result[f.Name] = "true"
					} else {
						result[f.Name] = "false"
					}
				}
			}
		}
	}
	return result
}

// ── Diagnostic helpers ────────────────────────────────────────────────────────

func worstDiag(nd []NodeDiag) DiagSeverity {
	w := DiagSeverity(-1)
	for _, d := range nd {
		if w == -1 || d.Severity < w {
			w = d.Severity
		}
	}
	return w
}

func diagBorder(nd []NodeDiag, defBorder, defWidth string) (string, string) {
	if len(nd) == 0 {
		return defBorder, defWidth
	}
	if worstDiag(nd) == DiagError {
		return "#d73a49", "3"
	}
	return "#e3b341", "2"
}

func diagMark(nd []NodeDiag) string {
	if len(nd) == 0 {
		return ""
	}
	if worstDiag(nd) == DiagError {
		return ` <FONT COLOR="#d73a49">⚠</FONT>`
	}
	return ` <FONT COLOR="#e3b341">⚠</FONT>`
}

// ── Label builders ────────────────────────────────────────────────────────────

// dsLabel builds the HTML label for a DataSource node.
// splitSide: "" = single node, "r" = read clone (early signals only),
//
//	"w" = pure sink main node, "m" = mixed main node (writes + late reads).
//
// Cell alignment is determined per-signal from Dir, so a mixed main node
// correctly shows "← name" (LEFT) for incoming signals and "name →" (RIGHT)
// for outgoing signals.
func dsLabel(name, class string, signals []SigInfo, nd []NodeDiag, splitSide string) string {
	border, bw := diagBorder(nd, "#1a6a9a", "2")

	var sb strings.Builder
	fmt.Fprintf(&sb,
		`<<TABLE BORDER="%s" CELLBORDER="0" CELLSPACING="0" CELLPADDING="0" COLOR="%s" BGCOLOR="#0b1e30">`,
		bw, border)

	// Header — tiny split indicator for cloned nodes.
	subtitle := class
	switch splitSide {
	case "r":
		subtitle = class + `<FONT COLOR="#1a3a5a" POINT-SIZE="7"> ·src</FONT>`
	case "w":
		subtitle = class + `<FONT COLOR="#1a3a5a" POINT-SIZE="7"> ·snk</FONT>`
	case "m":
		subtitle = class + `<FONT COLOR="#1a3a5a" POINT-SIZE="7"> ·buf</FONT>`
	}
	fmt.Fprintf(&sb,
		`<TR><TD ALIGN="CENTER" CELLPADDING="5"><B><FONT COLOR="#7ec8e3" POINT-SIZE="11">%s</FONT></B>%s<BR/>`+
			`<FONT COLOR="#5a90b0" POINT-SIZE="8">%s</FONT></TD></TR>`,
		he(name), diagMark(nd), subtitle)

	if len(signals) > 0 {
		sb.WriteString(`<HR/>`)
		// DS signals are never truncated — every port must be reachable for edges.
		// Alignment is per-signal so a mixed main node renders correctly.
		for _, s := range signals {
			var label, color, cellAlign string
			switch s.Dir {
			case "ds-out": // exits east toward GAMs
				color = "#3d8fdd"
				label = he(s.LocalName) + " &#8594;" // "name →"
				cellAlign = "RIGHT"
			case "ds-in": // enters west from GAMs
				color = "#c07030"
				label = "&#8592; " + he(s.LocalName) // "← name"
				cellAlign = "LEFT"
			default: // ds-both
				color = "#7878a0"
				label = "&#8644; " + he(s.LocalName) // "↔ name"
				cellAlign = "CENTER"
			}
			if s.Implicit {
				color = "#4a6a90"
				label = "~ " + label
			}

			if s.Type != "" {
				label += `<BR/><FONT POINT-SIZE="7" COLOR="#3a6080">` + he(s.Type)
				if s.NumElems != "" && s.NumElems != "1" {
					label += "[" + he(s.NumElems) + "]"
				}
				label += `</FONT>`
			}
			fmt.Fprintf(&sb,
				`<TR><TD PORT="%s" ALIGN="%s" CELLPADDING="2"><FONT COLOR="%s" POINT-SIZE="8">%s</FONT>%s</TD></TR>`,
				s.PortID, cellAlign, color, label, diagMark(s.Diags))
		}
	}

	sb.WriteString(`</TABLE>>`)
	return sb.String()
}

// iogamLabel builds a horizontal paired layout for IOGAM nodes.
func iogamLabel(name, class string, inSigs, outSigs []SigInfo, nd []NodeDiag, conditional bool) string {
	border, bw := diagBorder(nd, "#404468", "1")
	displayName := name
	if conditional {
		displayName = "◇ " + name
	}

	var sb strings.Builder
	fmt.Fprintf(&sb,
		`<<TABLE BORDER="%s" CELLBORDER="0" CELLSPACING="0" CELLPADDING="0" COLOR="%s" BGCOLOR="#1c1e30" STYLE="ROUNDED">`,
		bw, border)

	fmt.Fprintf(&sb,
		`<TR><TD COLSPAN="3" ALIGN="CENTER" CELLPADDING="5"><B><FONT COLOR="#d8d8e8" POINT-SIZE="11">%s</FONT></B>%s<BR/>`+
			`<FONT COLOR="#8080b0" POINT-SIZE="8">%s</FONT></TD></TR>`,
		he(displayName), diagMark(nd), he(class))

	n := len(inSigs)
	if len(outSigs) > n {
		n = len(outSigs)
	}

	if n > 0 {
		sb.WriteString(`<HR/>`)
		for i := 0; i < n; i++ {
			sb.WriteString(`<TR>`)
			if i < len(inSigs) {
				s := inSigs[i]
				text := he(s.LocalName)
				if s.Type != "" {
					text += `<BR/><FONT POINT-SIZE="7" COLOR="#4a7ab0">` + he(s.Type)
					if s.NumElems != "" && s.NumElems != "1" {
						text += "[" + he(s.NumElems) + "]"
					}
					text += `</FONT>`
				}
				fmt.Fprintf(&sb,
					`<TD PORT="%s" ALIGN="RIGHT" CELLPADDING="4"><FONT COLOR="#6aaff0" POINT-SIZE="8">%s &#8592;</FONT>%s</TD>`,
					s.PortID, text, diagMark(s.Diags))
			} else {
				sb.WriteString(`<TD CELLPADDING="4"></TD>`)
			}
			sb.WriteString(`<VR/>`)
			if i < len(outSigs) {
				s := outSigs[i]
				text := he(s.LocalName)
				if s.Type != "" {
					text += `<BR/><FONT POINT-SIZE="7" COLOR="#9a6848">` + he(s.Type)
					if s.NumElems != "" && s.NumElems != "1" {
						text += "[" + he(s.NumElems) + "]"
					}
					text += `</FONT>`
				}
				fmt.Fprintf(&sb,
					`<TD PORT="%s" ALIGN="LEFT" CELLPADDING="4"><FONT COLOR="#e08848" POINT-SIZE="8">&#8594; %s</FONT>%s</TD>`,
					s.PortID, text, diagMark(s.Diags))
			} else {
				sb.WriteString(`<TD CELLPADDING="4"></TD>`)
			}
			sb.WriteString(`</TR>`)
			if i < n-1 {
				sb.WriteString(`<HR/>`)
			}
		}
	}

	sb.WriteString(`</TABLE>>`)
	return sb.String()
}

// gamLabel builds the label for a regular (non-IOGAM) GAM.
//
// Layout uses a flat outer table so PORT cells project directly to the node
// boundary; edges with :e/:w compass points then attach at the exact
// y-position of each signal row.
//
//   - Both in and out → two columns separated by VR:
//     [ ← in-signal ]  │  [ out-signal → ]
//   - Only in  → single left column:  [ ← in-signal ]
//   - Only out → single right column: [ out-signal → ]
func gamLabel(name, class string, inSigs, outSigs []SigInfo, nd []NodeDiag, conditional bool) string {
	nameColor, classColor := "#c8c8d8", "#9090b0"
	bgColor, defBorder, inBG, outBG := "#181824", "#383850", "#141428", "#141428"

	switch class {
	case "MessageGAM":
		nameColor, classColor = "#f0c040", "#b09030"
		bgColor, defBorder, inBG, outBG = "#241c08", "#604400", "#1c1400", "#1c1400"
	}

	border, bw := diagBorder(nd, defBorder, "1")
	displayName := name
	if conditional {
		displayName = "◇ " + name
	}

	hasIn := len(inSigs) > 0
	hasOut := len(outSigs) > 0
	dual := hasIn && hasOut

	// colspan for header: 3 when dual (left TD + VR + right TD), 1 when single.
	colspan := "1"
	if dual {
		colspan = "3"
	}

	dispIn, extraIn := truncateSlice(inSigs, maxSigDisplay)
	dispOut, extraOut := truncateSlice(outSigs, maxSigDisplay)

	var sb strings.Builder
	fmt.Fprintf(&sb,
		`<<TABLE BORDER="%s" CELLBORDER="0" CELLSPACING="0" CELLPADDING="0" COLOR="%s" BGCOLOR="%s" STYLE="ROUNDED">`,
		bw, border, bgColor)

	// ── Header ────────────────────────────────────────────────────────────
	fmt.Fprintf(&sb,
		`<TR><TD COLSPAN="%s" ALIGN="CENTER" CELLPADDING="5">`+
			`<B><FONT COLOR="%s" POINT-SIZE="11">%s</FONT></B>%s<BR/>`+
			`<FONT COLOR="%s" POINT-SIZE="8">%s</FONT></TD></TR>`,
		colspan, nameColor, he(displayName), diagMark(nd), classColor, he(class))

	// ── Signal section ────────────────────────────────────────────────────
	if hasIn || hasOut {
		sb.WriteString(`<HR/>`)

		sigRow := func(s SigInfo, isIn bool) string {
			bg, textColor, align := outBG, "#e08848", "RIGHT"
			arrow := "&#8594; " + he(s.LocalName) // "→ name"
			if isIn {
				bg, textColor, align = inBG, "#6aaff0", "LEFT"
				arrow = he(s.LocalName) + " &#8592;" // "name ←"
			}
			text := arrow
			if s.Type != "" {
				typeColor := "#9a6848"
				if isIn {
					typeColor = "#3a6090"
				}
				text += `<BR/><FONT POINT-SIZE="7" COLOR="` + typeColor + `">` + he(s.Type)
				if s.NumElems != "" && s.NumElems != "1" {
					text += "[" + he(s.NumElems) + "]"
				}
				text += `</FONT>`
			}
			return fmt.Sprintf(
				`<TD PORT="%s" ALIGN="%s" CELLPADDING="2" BGCOLOR="%s">`+
					`<FONT COLOR="%s" POINT-SIZE="8">%s</FONT>%s</TD>`,
				s.PortID, align, bg, textColor, text, diagMark(s.Diags))
		}

		switch {
		case dual:
			// Section label row
			fmt.Fprintf(&sb,
				`<TR><TD ALIGN="LEFT" CELLPADDING="2" BGCOLOR="%s">`+
					`<FONT COLOR="#3a5a8a" POINT-SIZE="7">in</FONT></TD><VR/>`+
					`<TD ALIGN="RIGHT" CELLPADDING="2" BGCOLOR="%s">`+
					`<FONT COLOR="#7a5020" POINT-SIZE="7">out</FONT></TD></TR>`,
				inBG, outBG)

			rows := len(dispIn)
			if len(dispOut) > rows {
				rows = len(dispOut)
			}
			hasExtra := extraIn > 0 || extraOut > 0
			for i := 0; i < rows; i++ {
				sb.WriteString(`<TR>`)
				if i < len(dispIn) {
					sb.WriteString(sigRow(dispIn[i], true))
				} else {
					fmt.Fprintf(&sb, `<TD BGCOLOR="%s"></TD>`, inBG)
				}
				sb.WriteString(`<VR/>`)
				if i < len(dispOut) {
					sb.WriteString(sigRow(dispOut[i], false))
				} else {
					fmt.Fprintf(&sb, `<TD BGCOLOR="%s"></TD>`, outBG)
				}
				sb.WriteString(`</TR>`)
				if i < rows-1 {
					sb.WriteString(`<HR/>`)
				}
			}
			if hasExtra {
				sb.WriteString(`<TR>`)
				if extraIn > 0 {
					fmt.Fprintf(&sb, `<TD ALIGN="LEFT" CELLPADDING="2" BGCOLOR="%s"><FONT COLOR="#3a6090" POINT-SIZE="7">+%d</FONT></TD>`, inBG, extraIn)
				} else {
					fmt.Fprintf(&sb, `<TD BGCOLOR="%s"></TD>`, inBG)
				}
				sb.WriteString(`<VR/>`)
				if extraOut > 0 {
					fmt.Fprintf(&sb, `<TD ALIGN="RIGHT" CELLPADDING="2" BGCOLOR="%s"><FONT COLOR="#8a5828" POINT-SIZE="7">+%d</FONT></TD>`, outBG, extraOut)
				} else {
					fmt.Fprintf(&sb, `<TD BGCOLOR="%s"></TD>`, outBG)
				}
				sb.WriteString(`</TR>`)
			}

		case hasIn:
			// Input-only: single left column
			fmt.Fprintf(&sb,
				`<TR><TD ALIGN="LEFT" CELLPADDING="2" BGCOLOR="%s">`+
					`<FONT COLOR="#3a5a8a" POINT-SIZE="7">in</FONT></TD></TR>`,
				inBG)
			for i, s := range dispIn {
				sb.WriteString(`<TR>`)
				sb.WriteString(sigRow(s, true))
				sb.WriteString(`</TR>`)
				if i < len(dispIn)-1 {
					sb.WriteString(`<HR/>`)
				}
			}
			if extraIn > 0 {
				fmt.Fprintf(&sb,
					`<TR><TD ALIGN="LEFT" CELLPADDING="2" BGCOLOR="%s"><FONT COLOR="#3a6090" POINT-SIZE="7">+%d</FONT></TD></TR>`,
					inBG, extraIn)
			}

		case hasOut:
			// Output-only: single right column
			fmt.Fprintf(&sb,
				`<TR><TD ALIGN="RIGHT" CELLPADDING="2" BGCOLOR="%s">`+
					`<FONT COLOR="#7a5020" POINT-SIZE="7">out</FONT></TD></TR>`,
				outBG)
			for i, s := range dispOut {
				sb.WriteString(`<TR>`)
				sb.WriteString(sigRow(s, false))
				sb.WriteString(`</TR>`)
				if i < len(dispOut)-1 {
					sb.WriteString(`<HR/>`)
				}
			}
			if extraOut > 0 {
				fmt.Fprintf(&sb,
					`<TR><TD ALIGN="RIGHT" CELLPADDING="2" BGCOLOR="%s"><FONT COLOR="#8a5828" POINT-SIZE="7">+%d</FONT></TD></TR>`,
					outBG, extraOut)
			}
		}
	}

	sb.WriteString(`</TABLE>>`)
	return sb.String()
}

// ── Utilities ─────────────────────────────────────────────────────────────────

func truncateSlice[T any](ss []T, max int) ([]T, int) {
	if len(ss) <= max {
		return ss, 0
	}
	return ss[:max], len(ss) - max
}

func makeID(prefix, name string, used map[string]bool) string {
	base := prefix + sanitize(name)
	id := base
	for i := 2; used[id]; i++ {
		id = fmt.Sprintf("%s_%d", base, i)
	}
	used[id] = true
	return id
}

func sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	return b.String()
}

func he(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

// ── Public static-output entry point ──────────────────────────────────────────

// GenerateWithOptions generates a graph applying state/thread filtering,
// optional simplification, and optional node-follow subsetting.
// Use this for static output (mdt graph -o ...) and live mode.
func GenerateWithOptions(tree *index.ProjectTree, diags map[*index.ProjectNode][]NodeDiag, opts GenerateOptions) Result {
	gopts := genOpts{stateFilter: opts.StateFilter, threadFilter: opts.ThreadFilter, bypassLevel: opts.Simplified}
	var res Result
	if opts.Simplified >= 2 {
		res = generateSimplified(tree, diags, gopts)
	} else {
		// Level 0 (full) and level 1 (bypass+signals) both go through generate;
		// bypassLevel in gopts controls whether bypass logic is applied.
		res = generate(tree, diags, gopts)
	}
	if len(opts.FollowNodes) > 0 {
		res = applyFollowFilter(tree, diags, opts.FollowNodes, res)
	}
	return res
}

// applyFollowFilter restricts the graph to the nodes whose name matches any
// entry in names, plus the DataSources they connect to.
func applyFollowFilter(tree *index.ProjectTree, diags map[*index.ProjectNode][]NodeDiag, names []string, res Result) Result {
	// Build a case-insensitive name→IDs index.
	nameToIDs := make(map[string][]string)
	add := func(id string, n *index.ProjectNode) {
		key := strings.ToLower(realName(n))
		nameToIDs[key] = append(nameToIDs[key], id)
	}
	for id, n := range res.GAMNodes {
		add(id, n)
	}
	for id, n := range res.DSNodes {
		add(id, n)
	}

	var nodeIDs []string
	seen := make(map[string]bool)
	for _, name := range names {
		for _, id := range nameToIDs[strings.ToLower(name)] {
			if !seen[id] {
				seen[id] = true
				nodeIDs = append(nodeIDs, id)
			}
		}
	}
	if len(nodeIDs) == 0 {
		return res // nothing matched — return full graph
	}
	return GenerateSubset(tree, diags, nodeIDs, res)
}

// ── Simplified graph generator ────────────────────────────────────────────────

// passThroughClasses are DataSource classes used as pure in-memory buffers
// between GAMs, eligible for bypass in simplified mode.
var passThroughClasses = map[string]bool{
	"GAMDataSource":          true,
	"AsynchThreadDataSource": true,
}

// sigConnEntry records one signal connection between a GAM and a DataSource.
type sigConnEntry struct {
	gam    *index.ProjectNode
	ds     *index.ProjectNode
	canon  string // canonical signal name inside the DS
	isRead bool   // true = GAM reads from DS; false = GAM writes to DS
}

// generateSimplified produces a simplified DOT graph that bypasses IOGAM nodes
// and pass-through DataSources (GAMDataSource, AsynchThreadDataSource).
//
// IOGAM bypass: for each IOGAM the edges DS_in → DS_out replace the two-hop
// path DS_in→IOGAM→DS_out. When DS_in is itself a pass-through DS the bypass
// is extended one level: writerGAM→DS_out.
//
// Pass-through DS bypass: for each qualifying DS a direct writerGAM→readerGAM
// edge (labelled with the signal name) replaces the two-hop path.
//
// The resulting DOT uses simple plain-text node labels rather than HTML-table
// port labels, making it suitable for SVG/HTML/MD static output.
func generateSimplified(tree *index.ProjectTree, diags map[*index.ProjectNode][]NodeDiag, opts genOpts) Result {
	if diags == nil {
		diags = make(map[*index.ProjectNode][]NodeDiag)
	}

	// ── ID assignment (same as generate) ─────────────────────────────────────
	dsIDMap := make(map[*index.ProjectNode]string)
	gamIDMap := make(map[*index.ProjectNode]string)
	usedDS := make(map[string]bool)
	usedGAM := make(map[string]bool)
	var allDSS, allGAMs []*index.ProjectNode

	tree.Walk(func(n *index.ProjectNode) {
		if tree.IsDataSource(n) {
			dsIDMap[n] = makeID("ds", n.Name, usedDS)
			allDSS = append(allDSS, n)
		} else if tree.IsGAM(n) {
			gamIDMap[n] = makeID("fn", n.Name, usedGAM)
			allGAMs = append(allGAMs, n)
		}
	})
	sort.Slice(allDSS, func(i, j int) bool { return dsIDMap[allDSS[i]] < dsIDMap[allDSS[j]] })
	sort.Slice(allGAMs, func(i, j int) bool { return gamIDMap[allGAMs[i]] < gamIDMap[allGAMs[j]] })

	states := extractStates(tree, gamIDMap)

	// ── State/thread filter ───────────────────────────────────────────────────
	gams := allGAMs
	if opts.stateFilter != "" {
		if si, ok := states[opts.stateFilter]; ok {
			threads := activeThreadsMap(si, opts.threadFilter)
			gamByID := make(map[string]*index.ProjectNode)
			for _, g := range allGAMs {
				gamByID[gamIDMap[g]] = g
			}
			activeSet := make(map[*index.ProjectNode]bool)
			for _, ids := range threads {
				for _, id := range ids {
					if g, ok := gamByID[id]; ok {
						activeSet[g] = true
					}
				}
			}
			var filtered []*index.ProjectNode
			for _, g := range allGAMs {
				if activeSet[g] {
					filtered = append(filtered, g)
				}
			}
			gams = filtered
		}
	}

	// ── Subset filter (for GenerateSubset in simplified mode) ─────────────────
	if opts.subsetNodes != nil {
		var fg []*index.ProjectNode
		for _, g := range gams {
			if opts.subsetNodes[g] {
				fg = append(fg, g)
			}
		}
		gams = fg
	}

	// ── Collect all signal connections ────────────────────────────────────────
	var conns []sigConnEntry
	for _, g := range gams {
		for _, dir := range []struct {
			name   string
			isRead bool
		}{{"InputSignals", true}, {"OutputSignals", false}} {
			c, ok := g.Children[dir.name]
			if !ok {
				continue
			}
			for _, sig := range c.Children {
				ds, canon := resolveSignal(tree, sig, g)
				if ds == nil {
					continue
				}
				conns = append(conns, sigConnEntry{g, ds, canon, dir.isRead})
			}
		}
	}

	// Per-DS writer and reader sets (non-deduplicated, for signal-level lookup).
	dsWriters := make(map[*index.ProjectNode]map[*index.ProjectNode]bool)
	dsReaders := make(map[*index.ProjectNode]map[*index.ProjectNode]bool)
	// Per-signal writers/readers for bypass edge construction.
	type sigKey struct {
		ds    *index.ProjectNode
		canon string
	}
	sigWriters := make(map[sigKey]*index.ProjectNode)  // one writer per signal
	sigReaders := make(map[sigKey][]*index.ProjectNode) // many readers per signal
	for _, c := range conns {
		if c.isRead {
			if dsReaders[c.ds] == nil {
				dsReaders[c.ds] = make(map[*index.ProjectNode]bool)
			}
			dsReaders[c.ds][c.gam] = true
			k := sigKey{c.ds, c.canon}
			sigReaders[k] = append(sigReaders[k], c.gam)
		} else {
			if dsWriters[c.ds] == nil {
				dsWriters[c.ds] = make(map[*index.ProjectNode]bool)
			}
			dsWriters[c.ds][c.gam] = true
			k := sigKey{c.ds, c.canon}
			if sigWriters[k] == nil {
				sigWriters[k] = c.gam
			}
		}
	}

	// ── Identify bypass candidates ────────────────────────────────────────────
	ioGAMSet := make(map[*index.ProjectNode]bool)
	for _, g := range gams {
		if isIOGAM(g.Metadata["Class"]) {
			ioGAMSet[g] = true
		}
	}

	passThroughDSSet := make(map[*index.ProjectNode]bool)
	for _, ds := range allDSS {
		if !passThroughClasses[ds.Metadata["Class"]] {
			continue
		}
		// Only bypass if at least one writer GAM exists.
		if len(dsWriters[ds]) > 0 {
			passThroughDSSet[ds] = true
		}
	}

	// ── Build bypass edges ────────────────────────────────────────────────────
	type bypassEdge struct {
		fromID string
		toID   string
		label  string
		style  string // "iogam" | "dsbypass"
	}
	var bypassEdges []bypassEdge
	seenBypass := make(map[[3]string]bool)
	addBypass := func(fromID, toID, label, style string) {
		k := [3]string{fromID, toID, label}
		if !seenBypass[k] {
			seenBypass[k] = true
			bypassEdges = append(bypassEdges, bypassEdge{fromID, toID, label, style})
		}
	}

	// IOGAM bypass: DS_in → DS_out  (or writerGAM → DS_out if DS_in is pass-through).
	for iogam := range ioGAMSet {
		gamName := realName(iogam)
		// Collect source IDs (what feeds this IOGAM).
		sourceIDs := make(map[string]bool)
		for _, c := range conns {
			if c.gam != iogam || !c.isRead {
				continue
			}
			if passThroughDSSet[c.ds] {
				// One-level chain: look through pass-through DS to its writers.
				for writerGAM := range dsWriters[c.ds] {
					if !ioGAMSet[writerGAM] {
						if id, ok := gamIDMap[writerGAM]; ok {
							sourceIDs[id] = true
						}
					}
				}
			} else {
				if id, ok := dsIDMap[c.ds]; ok {
					sourceIDs[id] = true
				}
			}
		}
		// Collect destination IDs (where this IOGAM writes).
		destIDs := make(map[string]bool)
		for _, c := range conns {
			if c.gam != iogam || c.isRead {
				continue
			}
			if id, ok := dsIDMap[c.ds]; ok {
				destIDs[id] = true
			}
		}
		for srcID := range sourceIDs {
			for dstID := range destIDs {
				addBypass(srcID, dstID, gamName, "iogam")
			}
		}
	}

	// Pass-through DS bypass: writerGAM → readerGAM per signal.
	for ds := range passThroughDSSet {
		dsName := realName(ds)
		if cont, ok := ds.Children["Signals"]; ok {
			for _, sig := range cont.Children {
				canon := realName(sig)
				k := sigKey{ds, canon}
				writer := sigWriters[k]
				readers := sigReaders[k]
				if writer == nil || ioGAMSet[writer] || len(readers) == 0 {
					continue
				}
				wID, okW := gamIDMap[writer]
				if !okW {
					continue
				}
				for _, reader := range readers {
					if ioGAMSet[reader] {
						continue
					}
					rID, okR := gamIDMap[reader]
					if !okR {
						continue
					}
					label := dsName + "." + canon
					addBypass(wID, rID, label, "dsbypass")
				}
			}
		} else {
			// No explicit Signals node — use connection list directly.
			type wrPair struct{ w, r *index.ProjectNode }
			seen := make(map[wrPair]bool)
			for _, c := range conns {
				if c.ds != ds || c.isRead {
					continue
				}
				if ioGAMSet[c.gam] {
					continue
				}
				k2 := sigKey{ds, c.canon}
				for _, reader := range sigReaders[k2] {
					if ioGAMSet[reader] {
						continue
					}
					p := wrPair{c.gam, reader}
					if seen[p] {
						continue
					}
					seen[p] = true
					wID, okW := gamIDMap[c.gam]
					rID, okR := gamIDMap[reader]
					if !okW || !okR {
						continue
					}
					addBypass(wID, rID, realName(ds)+"."+c.canon, "dsbypass")
				}
			}
		}
	}

	// ── Build regular edges (skip bypassed nodes) ─────────────────────────────
	type regularEdge struct {
		fromID  string
		toID    string
		label   string
		isWrite bool
	}
	var regularEdges []regularEdge
	seenReg := make(map[[3]string]bool)
	for _, c := range conns {
		if ioGAMSet[c.gam] || passThroughDSSet[c.ds] {
			continue
		}
		dsID, okD := dsIDMap[c.ds]
		gamID, okG := gamIDMap[c.gam]
		if !okD || !okG {
			continue
		}
		fromID, toID := dsID, gamID
		if !c.isRead {
			fromID, toID = gamID, dsID
		}
		k := [3]string{fromID, toID, c.canon}
		if !seenReg[k] {
			seenReg[k] = true
			regularEdges = append(regularEdges, regularEdge{fromID, toID, c.canon, !c.isRead})
		}
	}

	// ── Build display node lists ──────────────────────────────────────────────
	var displayGAMs []*index.ProjectNode
	for _, g := range gams {
		if !ioGAMSet[g] {
			displayGAMs = append(displayGAMs, g)
		}
	}

	// DSes to show: not bypassed, and (if state filter) connected to remaining nodes.
	displayDSSet := make(map[*index.ProjectNode]bool)
	for _, ds := range allDSS {
		if passThroughDSSet[ds] {
			continue
		}
		displayDSSet[ds] = true
	}
	if opts.stateFilter != "" {
		// Keep only DSes that appear in regular or bypass edges.
		referencedIDs := make(map[string]bool)
		for _, e := range regularEdges {
			referencedIDs[e.fromID] = true
			referencedIDs[e.toID] = true
		}
		for _, e := range bypassEdges {
			referencedIDs[e.fromID] = true
			referencedIDs[e.toID] = true
		}
		for _, g := range displayGAMs {
			referencedIDs[gamIDMap[g]] = true
		}
		for ds := range displayDSSet {
			if !referencedIDs[dsIDMap[ds]] {
				delete(displayDSSet, ds)
			}
		}
	}
	var displayDSS []*index.ProjectNode
	for _, ds := range allDSS {
		if displayDSSet[ds] {
			displayDSS = append(displayDSS, ds)
		}
	}

	// ── GAM rank computation (same Bellman-Ford as generate) ──────────────────
	gamThreadPos := make(map[string]int)
	for _, g := range allGAMs {
		gamThreadPos[gamIDMap[g]] = 0
	}
	if opts.stateFilter != "" {
		if si, ok := states[opts.stateFilter]; ok {
			for _, ids := range activeThreadsMap(si, opts.threadFilter) {
				for pos, id := range ids {
					if cur := gamThreadPos[id]; pos > cur {
						gamThreadPos[id] = pos
					}
				}
			}
		}
	} else {
		type orderEdge struct{ from, to string }
		var orderEdges []orderEdge
		for _, si := range states {
			for _, ids := range si.Threads {
				for i := 1; i < len(ids); i++ {
					orderEdges = append(orderEdges, orderEdge{ids[i-1], ids[i]})
				}
			}
		}
		for range orderEdges {
			changed := false
			for _, e := range orderEdges {
				r := gamThreadPos[e.from]
				if cur := gamThreadPos[e.to]; cur < r+1 {
					gamThreadPos[e.to] = r + 1
					changed = true
				}
			}
			if !changed {
				break
			}
		}
	}

	// ── Emit DOT ──────────────────────────────────────────────────────────────
	var sb strings.Builder
	sb.WriteString("digraph MARTe {\n")
	sb.WriteString("  bgcolor=\"transparent\";\n")
	sb.WriteString("  layout=dot;\n")
	sb.WriteString("  rankdir=LR;\n")
	sb.WriteString("  ranksep=0.7;\n")
	sb.WriteString("  nodesep=0.35;\n")
	sb.WriteString("  splines=spline;\n")
	sb.WriteString("  node [fontname=\"Helvetica\", fontsize=10, margin=\"0.15,0.08\"];\n")
	sb.WriteString("  edge [fontname=\"Helvetica\", fontsize=8, arrowsize=0.6, arrowhead=open];\n\n")

	simplNode := func(id, label, fillColor, borderColor, fontColor string, penwidth int) {
		fmt.Fprintf(&sb, "  %s [shape=box, style=\"filled,rounded\", fillcolor=%q, color=%q, penwidth=%d, fontcolor=%q, label=%q];\n",
			id, fillColor, borderColor, penwidth, fontColor, label)
	}

	// DS nodes
	for _, ds := range displayDSS {
		id := dsIDMap[ds]
		name := realName(ds)
		class := ds.Metadata["Class"]
		nd := diags[ds]
		border, pw := "#1a6a9a", 2
		if len(nd) > 0 {
			if worstDiag(nd) == DiagError {
				border = "#d73a49"
				pw = 3
			} else {
				border = "#e3b341"
				pw = 2
			}
		}
		cond := ""
		if ds.IsConditional {
			cond = "◇ "
		}
		simplNode(id, cond+name+"\n"+class, "#0b1e30", border, "#7ec8e3", pw)
	}
	sb.WriteString("\n")

	// GAM nodes (non-IOGAM only in display list)
	// With state filter: group by thread in subgraph clusters.
	if opts.stateFilter != "" {
		si := states[opts.stateFilter]
		threads := activeThreadsMap(si, opts.threadFilter)
		var threadNames []string
		for t := range threads {
			threadNames = append(threadNames, t)
		}
		sort.Strings(threadNames)
		gamByID := make(map[string]*index.ProjectNode)
		for _, g := range displayGAMs {
			gamByID[gamIDMap[g]] = g
		}
		for _, threadName := range threadNames {
			ids := threads[threadName]
			fmt.Fprintf(&sb, "  subgraph cluster_%s {\n", sanitize(threadName))
			fmt.Fprintf(&sb, "    label=<%s>;\n", he(threadName))
			sb.WriteString("    color=\"#30363d\"; penwidth=1.5;\n")
			sb.WriteString("    fontname=\"Helvetica\"; fontsize=10; fontcolor=\"#7a8899\";\n")
			for _, id := range ids {
				n, ok := gamByID[id]
				if !ok {
					continue
				}
				name := realName(n)
				class := n.Metadata["Class"]
				nd := diags[n]
				border, pw := "#383850", 1
				if len(nd) > 0 {
					if worstDiag(nd) == DiagError {
						border, pw = "#d73a49", 3
					} else {
						border, pw = "#e3b341", 2
					}
				}
				cond := ""
				if n.IsConditional {
					cond = "◇ "
				}
				simplNode(id, cond+name+"\n"+class, "#181824", border, "#c8c8d8", pw)
			}
			sb.WriteString("  }\n\n")
		}
	} else {
		for _, g := range displayGAMs {
			id := gamIDMap[g]
			name := realName(g)
			class := g.Metadata["Class"]
			nd := diags[g]
			border, pw := "#383850", 1
			if len(nd) > 0 {
				if worstDiag(nd) == DiagError {
					border, pw = "#d73a49", 3
				} else {
					border, pw = "#e3b341", 2
				}
			}
			cond := ""
			if g.IsConditional {
				cond = "◇ "
			}
			simplNode(id, cond+name+"\n"+class, "#181824", border, "#c8c8d8", pw)
		}
	}
	sb.WriteString("\n")

	// Regular edges
	for _, e := range regularEdges {
		color := "#3d6fd6" // DS→GAM read
		if e.isWrite {
			color = "#c87941" // GAM→DS write
		}
		fmt.Fprintf(&sb, "  %s -> %s [color=%q, label=%q];\n", e.fromID, e.toID, color, e.label)
	}

	// Bypass edges
	for _, e := range bypassEdges {
		var color, style string
		switch e.style {
		case "iogam":
			color, style = "#9060c0", "dashed"
		default: // dsbypass
			color, style = "#40a060", "dashed"
		}
		fmt.Fprintf(&sb, "  %s -> %s [style=%s, color=%q, fontcolor=%q, label=%q];\n",
			e.fromID, e.toID, style, color, color, e.label)
	}

	// Rank constraints: GAMs in same thread at same rank.
	sb.WriteString("\n")
	rankGroups := make(map[int][]string)
	for _, g := range displayGAMs {
		id := gamIDMap[g]
		pos := gamThreadPos[id]
		rankGroups[pos] = append(rankGroups[pos], id)
	}
	var ranks []int
	for r := range rankGroups {
		ranks = append(ranks, r)
	}
	sort.Ints(ranks)
	for _, r := range ranks {
		ids := rankGroups[r]
		if len(ids) <= 1 {
			continue
		}
		sb.WriteString("  { rank=same;")
		for _, id := range ids {
			fmt.Fprintf(&sb, " %s;", id)
		}
		sb.WriteString(" }\n")
	}

	sb.WriteString("}\n")

	// ── Build Result ──────────────────────────────────────────────────────────
	meta := make(map[string]NodeInfo)
	gamNodes := make(map[string]*index.ProjectNode)
	dsNodes := make(map[string]*index.ProjectNode)
	allGAMIDs := make(map[string]bool)

	for _, g := range displayGAMs {
		id := gamIDMap[g]
		gamNodes[id] = g
		allGAMIDs[id] = true
		inS, outS := buildGAMSigs(tree, g, diags)
		meta[id] = NodeInfo{
			Name: realName(g), Kind: "gam", Class: g.Metadata["Class"],
			Doc: g.Doc, Conditional: g.IsConditional,
			Fields: collectFields(g), InSigs: inS, OutSigs: outS, Diags: diags[g],
		}
	}
	for _, ds := range displayDSS {
		id := dsIDMap[ds]
		dsNodes[id] = ds
		allSigs := buildDSSigs(ds, dsReadSigsOf(conns, ds), dsWriteSigsOf(conns, ds), nil, diags)
		meta[id] = NodeInfo{
			Name: realName(ds), Kind: "ds", Class: ds.Metadata["Class"],
			Doc: ds.Doc, Conditional: ds.IsConditional,
			Fields: collectFields(ds), DSSigs: allSigs, Diags: diags[ds],
		}
	}

	return Result{
		DOT: sb.String(), Meta: meta, States: states,
		AllGAMIDs: allGAMIDs, GAMNodes: gamNodes, DSNodes: dsNodes, GenOpts: opts,
	}
}

// dsReadSigsOf and dsWriteSigsOf extract the read/write signal name sets for
// a specific DS from the flat connection list. Used by generateSimplified to
// populate meta without re-walking the tree.
func dsReadSigsOf(conns []sigConnEntry, ds *index.ProjectNode) map[string]bool {
	m := make(map[string]bool)
	for _, c := range conns {
		if c.ds == ds && c.isRead {
			m[c.canon] = true
		}
	}
	return m
}

func dsWriteSigsOf(conns []sigConnEntry, ds *index.ProjectNode) map[string]bool {
	m := make(map[string]bool)
	for _, c := range conns {
		if c.ds == ds && !c.isRead {
			m[c.canon] = true
		}
	}
	return m
}
