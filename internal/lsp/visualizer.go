package lsp

import (
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/logger"
	"github.com/marte-community/marte-dev-tools/internal/parser"
)

type Visualizer struct {
	tree        *index.ProjectTree
	focusedNode *index.ProjectNode
	mu          sync.Mutex
	port        int
}

var GlobalVisualizer *Visualizer

func StartVisualizer(port int) {
	GlobalVisualizer = &Visualizer{
		port: port,
	}
	go GlobalVisualizer.serve()
}

func (v *Visualizer) SetTree(tree *index.ProjectTree) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.tree = tree
}

func (v *Visualizer) SetFocus(node *index.ProjectNode) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.focusedNode = node
}

func (v *Visualizer) serve() {
	http.HandleFunc("/", v.handleIndex)
	http.HandleFunc("/graph", v.handleGraph)
	logger.Printf("Visualizer serving on http://localhost:%d", v.port)
	if err := http.ListenAndServe(fmt.Sprintf(":%d", v.port), nil); err != nil {
		logger.Printf("Visualizer error: %v", err)
	}
}

func (v *Visualizer) handleIndex(w http.ResponseWriter, r *http.Request) {
	html := `
<!DOCTYPE html>
<html>
<head>
    <title>MARTe2 Visualizer</title>
    <script src="https://cdn.jsdelivr.net/npm/mermaid/dist/mermaid.min.js"></script>
    <script>
        mermaid.initialize({ 
            startOnLoad: true,
            theme: 'base',
            themeVariables: {
                'primaryColor': '#ffffff',
                'primaryTextColor': '#333',
                'primaryBorderColor': '#7C0000',
                'lineColor': '#F8B229',
                'secondaryColor': '#006100',
                'tertiaryColor': '#fff'
            }
        });
        function refresh() {
            fetch('/graph')
                .then(response => response.text())
                .then(text => {
                    const container = document.getElementById('graph-container');
                    if (container.getAttribute('data-last') === text) return;
                    container.setAttribute('data-last', text);
                    container.removeAttribute('data-processed');
                    container.innerHTML = text;
                    mermaid.run({
                        nodes: [container]
                    });
                    
                    // Extract node name from text if possible
                    const match = text.match(/\(\((.*?)\)\)/) || text.match(/\[(.*?)\]/);
                    if (match) {
                        document.getElementById('node-name').innerText = match[1];
                    }
                })
                .catch(err => console.error(err));
        }
        setInterval(refresh, 1000);
    </script>
    <style>
        body { font-family: 'Inter', -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; background: #f4f7f6; margin: 0; padding: 0; color: #2c3e50; }
        header { background: #8e0000; color: white; padding: 1rem 2.5rem; box-shadow: 0 4px 6px rgba(0,0,0,0.1); display: flex; align-items: center; justify-content: space-between; }
        h1 { margin: 0; font-size: 1.25rem; font-weight: 600; letter-spacing: -0.025em; }
        .status { font-size: 0.8rem; background: rgba(255,255,255,0.2); padding: 0.25rem 0.75rem; border-radius: 99px; }
        main { padding: 2rem; max-width: 1200px; margin: 0 auto; }
        #graph-container { background: white; padding: 2.5rem; border-radius: 12px; box-shadow: 0 10px 15px -3px rgba(0,0,0,0.1); min-height: 500px; display: flex; flex-direction: column; align-items: center; border: 1px solid #e2e8f0; }
        .controls { margin-bottom: 1.5rem; color: #64748b; font-size: 0.95rem; text-align: center; }
        .mermaid { width: 100%; }
        code { background: #f1f5f9; padding: 0.2rem 0.4rem; border-radius: 4px; font-family: monospace; }
    </style>
</head>
<body>
    <header>
        <h1>MARTe2 Architecture Inspector</h1>
        <div class="status" id="status-tag">Connected</div>
    </header>
    <main>
        <div class="controls">
            Live preview of <code id="node-name">None</code>. 
            Navigate your code or hover elements to update the graph.
        </div>
        <div id="graph-container" class="mermaid">
            graph TD
            A[Initializing...]
        </div>
    </main>
</body>
</html>
`
	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(html))
}

func (v *Visualizer) handleGraph(w http.ResponseWriter, r *http.Request) {
	v.mu.Lock()
	node := v.focusedNode
	tree := v.tree
	v.mu.Unlock()

	if node == nil || tree == nil {
		w.Write([]byte("graph TD\n  Start[Start editing or hover an object]"))
		return
	}

	mermaid := v.generateMermaid(tree, node)
	w.Write([]byte(mermaid))
}

func (v *Visualizer) generateMermaid(tree *index.ProjectTree, focus *index.ProjectNode) string {
	var sb strings.Builder
	sb.WriteString("graph LR\n")

	// Helper to sanitize names for Mermaid
	clean := func(s string) string {
		s = strings.ReplaceAll(s, ".", "_")
		s = strings.ReplaceAll(s, "+", "P_")
		s = strings.ReplaceAll(s, "$", "S_")
		s = strings.ReplaceAll(s, "@", "V_")
		s = strings.ReplaceAll(s, "[", "_")
		s = strings.ReplaceAll(s, "]", "_")
		return s
	}

	// 1. If focus is GAM, show signals and data sources
	if isGAM(focus) {
		v.addGAMConnections(&sb, tree, focus, clean)
	} else if isDataSource(focus) {
		v.addDSConnections(&sb, tree, focus, clean)
	} else if isSignal(focus) {
		v.addSignalConnections(&sb, tree, focus, clean)
	} else {
		// Just show the node and its immediate children if any
		nodeID := clean(focus.RealName)
		sb.WriteString(fmt.Sprintf("  %s[%s]\n", nodeID, focus.RealName))
		for _, child := range focus.Children {
			childID := clean(child.RealName)
			sb.WriteString(fmt.Sprintf("  %s --> %s[%s]\n", nodeID, childID, child.RealName))
		}
	}

	// Classes
	sb.WriteString("  classDef gam fill:#f9f,stroke:#333,stroke-width:2px;\n")
	sb.WriteString("  classDef ds fill:#bbf,stroke:#333,stroke-width:2px;\n")
	sb.WriteString("  classDef sig fill:#dfd,stroke:#333,stroke-width:1px,font-style:italic;\n")

	return sb.String()
}

func (v *Visualizer) addGAMConnections(sb *strings.Builder, tree *index.ProjectTree, gam *index.ProjectNode, clean func(string) string) {
	gamID := clean(gam.RealName)
	sb.WriteString(fmt.Sprintf("  %s((%s))\n", gamID, gam.RealName))
	sb.WriteString(fmt.Sprintf("  class %s gam\n", gamID))

	// Inputs
	if inputs, ok := gam.Children["InputSignals"]; ok {
		for _, sig := range inputs.Children {
			dsName, dsNode := v.findSignalDataSource(sig, tree)
			sigName := sig.RealName
			dsID := clean(dsName)
			if dsName == "" { dsName = "UnknownDS" }
			
			sigID := clean(gam.RealName + "_" + sigName)
			sb.WriteString(fmt.Sprintf("  %s[%s] --> %s(%s) --> %s\n", dsID, dsName, sigID, sigName, gamID))
			sb.WriteString(fmt.Sprintf("  class %s ds\n", dsID))
			sb.WriteString(fmt.Sprintf("  class %s sig\n", sigID))
			
			if dsNode != nil {
				// Style it as DS
			}
		}
	}

	// Outputs
	if outputs, ok := gam.Children["OutputSignals"]; ok {
		for _, sig := range outputs.Children {
			dsName, _ := v.findSignalDataSource(sig, tree)
			sigName := sig.RealName
			dsID := clean(dsName)
			if dsName == "" { dsName = "UnknownDS" }
			
			sigID := clean(gam.RealName + "_" + sigName)
			sb.WriteString(fmt.Sprintf("  %s --> %s(%s) --> %s[%s]\n", gamID, sigID, sigName, dsID, dsName))
			sb.WriteString(fmt.Sprintf("  class %s ds\n", dsID))
			sb.WriteString(fmt.Sprintf("  class %s sig\n", sigID))
		}
	}
}

func (v *Visualizer) addDSConnections(sb *strings.Builder, tree *index.ProjectTree, ds *index.ProjectNode, clean func(string) string) {
	dsID := clean(ds.RealName)
	sb.WriteString(fmt.Sprintf("  %s[%s]\n", dsID, ds.RealName))
	sb.WriteString(fmt.Sprintf("  class %s ds\n", dsID))

	// Find all GAMs using this DS
	tree.Walk(func(n *index.ProjectNode) {
		if isGAM(n) {
			// Check Inputs
			if inputs, ok := n.Children["InputSignals"]; ok {
				for _, sig := range inputs.Children {
					targetDS, _ := v.findSignalDataSource(sig, tree)
					if targetDS == ds.RealName || targetDS == ds.Name {
						gamID := clean(n.RealName)
						sigID := clean(n.RealName + "_" + sig.RealName)
						sb.WriteString(fmt.Sprintf("  %s --> %s(%s) --> %s((%s))\n", dsID, sigID, sig.RealName, gamID, n.RealName))
						sb.WriteString(fmt.Sprintf("  class %s gam\n", gamID))
						sb.WriteString(fmt.Sprintf("  class %s sig\n", sigID))
					}
				}
			}
			// Check Outputs
			if outputs, ok := n.Children["OutputSignals"]; ok {
				for _, sig := range outputs.Children {
					targetDS, _ := v.findSignalDataSource(sig, tree)
					if targetDS == ds.RealName || targetDS == ds.Name {
						gamID := clean(n.RealName)
						sigID := clean(n.RealName + "_" + sig.RealName)
						sb.WriteString(fmt.Sprintf("  %s((%s)) --> %s(%s) --> %s\n", gamID, n.RealName, sigID, sig.RealName, dsID))
						sb.WriteString(fmt.Sprintf("  class %s gam\n", gamID))
						sb.WriteString(fmt.Sprintf("  class %s sig\n", sigID))
					}
				}
			}
		}
	})
}

func (v *Visualizer) addSignalConnections(sb *strings.Builder, tree *index.ProjectTree, sig *index.ProjectNode, clean func(string) string) {
	// If it's a signal inside a GAM, show that GAM's connections
	if sig.Parent != nil {
		if isGAM(sig.Parent.Parent) {
			v.addGAMConnections(sb, tree, sig.Parent.Parent, clean)
			return
		}
		// If it's a signal inside a DataSource
		if isDataSource(sig.Parent.Parent) {
			v.addDSConnections(sb, tree, sig.Parent.Parent, clean)
			return
		}
	}
	sb.WriteString(fmt.Sprintf("  Sig[%s]\n", sig.RealName))
}

func (v *Visualizer) findSignalDataSource(sig *index.ProjectNode, tree *index.ProjectTree) (string, *index.ProjectNode) {
	// 1. Check local DataSource field
	for _, frag := range sig.Fragments {
		for _, def := range frag.Definitions {
			if f, ok := def.(*parser.Field); ok && f.Name == "DataSource" {
				val := tree.Evaluate(f.Value, sig)
				dsName := tree.ValueToString(val)
				if dsName != "" {
					return dsName, tree.ResolveName(sig, dsName, isDataSource)
				}
			}
		}
	}
	
	// 2. Check for DefaultDataSource in hierarchy
	curr := sig
	for curr != nil {
		for _, frag := range curr.Fragments {
			for _, def := range frag.Definitions {
				if f, ok := def.(*parser.Field); ok && f.Name == "DefaultDataSource" {
					val := tree.Evaluate(f.Value, curr)
					dsName := tree.ValueToString(val)
					if dsName != "" {
						return dsName, tree.ResolveName(curr, dsName, isDataSource)
					}
				}
			}
		}
		curr = curr.Parent
	}

	// 3. If no local field, check target (if it's a link to a DataSource signal)
	if sig.Target != nil && sig.Target.Parent != nil && sig.Target.Parent.Parent != nil {
		if isDataSource(sig.Target.Parent.Parent) {
			return sig.Target.Parent.Parent.RealName, sig.Target.Parent.Parent
		}
	}
	return "", nil
}

func isSignal(node *index.ProjectNode) bool {
	if node == nil || node.Parent == nil {
		return false
	}
	return node.Parent.Name == "Signals" || node.Parent.Name == "InputSignals" || node.Parent.Name == "OutputSignals"
}
