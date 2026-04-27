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

// genOpts controls behaviour of the internal generate function.
type genOpts struct {
	stateFilter string
	// subsetNodes, if non-nil, restricts the graph to this set of ProjectNodes.
	// IDs remain consistent because the full tree.Walk is still done for ID assignment.
	subsetNodes map[*index.ProjectNode]bool
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
func GenerateSubset(tree *index.ProjectTree, diags map[*index.ProjectNode][]NodeDiag, nodeIDs []string, existing Result) Result {
	subset := make(map[*index.ProjectNode]bool)
	for _, id := range nodeIDs {
		if n, ok := existing.GAMNodes[id]; ok {
			subset[n] = true
		}
		if n, ok := existing.DSNodes[id]; ok {
			subset[n] = true
		}
	}
	return generate(tree, diags, genOpts{subsetNodes: subset})
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
			gamByID := make(map[string]*index.ProjectNode)
			for _, g := range allGAMs {
				gamByID[gamIDMap[g]] = g
			}
			activeSet := make(map[*index.ProjectNode]bool)
			for _, ids := range si.Threads {
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

	// Phase 1: GAM thread execution positions.
	// Always derived from ALL states, regardless of stateFilter.
	// This keeps ranks consistent between the full view and any filtered view —
	// a GAM that appears in multiple states always gets the same column position
	// so switching state filters does not cause layout shifts.
	gamThreadPos := make(map[string]int) // graphviz GAM ID → thread position
	for _, si := range states {
		for _, ids := range si.Threads {
			for pos, id := range ids {
				if existing, ok := gamThreadPos[id]; !ok || pos > existing {
					gamThreadPos[id] = pos
				}
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
		var threadNames []string
		for t := range si.Threads {
			threadNames = append(threadNames, t)
		}
		sort.Strings(threadNames)
		gamByID := make(map[string]*index.ProjectNode)
		for _, g := range gams {
			gamByID[gamIDMap[g]] = g
		}

		for _, threadName := range threadNames {
			ids := si.Threads[threadName]
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

	return Result{DOT: sb.String(), Meta: meta, States: states, AllGAMIDs: allGAMIDs, GAMNodes: gamNodes, DSNodes: dsNodes}
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
