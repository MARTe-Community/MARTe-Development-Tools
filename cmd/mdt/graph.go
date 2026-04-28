package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/marte-community/marte-dev-tools/internal/graph"
	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/logger"
	"github.com/marte-community/marte-dev-tools/internal/lsp"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/validator"
)

// fullResult bundles the generated graph data with the source tree and diagnostics.
type fullResult struct {
	allResult graph.Result
	tree      *index.ProjectTree
	nodeDiags map[*index.ProjectNode][]graph.NodeDiag
}

func runGraph(args []string) {
	var explicitFiles []string
	rootPath := ""
	projectFilter := ""
	port := 0
	overrides := make(map[string]string)

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-P" && i+1 < len(args):
			rootPath = args[i+1]
			i++
		case arg == "-p" && i+1 < len(args):
			projectFilter = args[i+1]
			i++
		case arg == "-port" && i+1 < len(args):
			fmt.Sscanf(args[i+1], "%d", &port)
			i++
		case strings.HasPrefix(arg, "-v"):
			pair := arg[2:]
			parts := strings.SplitN(pair, "=", 2)
			if len(parts) == 2 {
				overrides[parts[0]] = parts[1]
			}
		default:
			explicitFiles = append(explicitFiles, arg)
		}
	}

	if rootPath == "" && len(explicitFiles) == 0 {
		rootPath = "."
	}
	projectRoot := rootPath
	if projectRoot == "" && len(explicitFiles) > 0 {
		projectRoot = filepath.Dir(explicitFiles[0])
	}

	collectFiles := func() []string {
		files := append([]string{}, explicitFiles...)
		if rootPath != "" {
			filepath.WalkDir(rootPath, func(path string, d fs.DirEntry, err error) error {
				if err == nil && !d.IsDir() && strings.HasSuffix(path, ".marte") {
					files = append(files, path)
				}
				return nil
			})
		}
		return files
	}

	buildAll := func(files []string) fullResult {
		tree := index.NewProjectTree()
		for _, file := range files {
			content, err := os.ReadFile(file)
			if err != nil {
				continue
			}
			p := parser.NewParser(string(content))
			config, _ := p.Parse()
			if config == nil {
				continue
			}
			if projectFilter != "" {
				fileProj := ""
				if config.Package != nil {
					parts := strings.Split(config.Package.URI, ".")
					fileProj = strings.TrimSpace(parts[0])
				}
				if fileProj != projectFilter {
					continue
				}
			}
			tree.AddFile(file, config)
		}

		// Run validation
		v := validator.NewValidator(tree, projectRoot, overrides)
		v.ValidateProject(context.Background())

		nodeDiags := make(map[*index.ProjectNode][]graph.NodeDiag)
		for _, d := range v.Diagnostics {
			node := tree.GetNodeContaining(d.File, d.Position)
			if node == nil {
				continue
			}
			target := findRelevantAncestor(tree, node)
			if target == nil {
				continue
			}
			sev := graph.DiagError
			if d.Level == validator.LevelWarning {
				sev = graph.DiagWarning
			}
			nodeDiags[target] = append(nodeDiags[target], graph.NodeDiag{
				Severity: sev, Message: d.Message,
			})
		}

		return fullResult{allResult: graph.Generate(tree, nodeDiags, ""), tree: tree, nodeDiags: nodeDiags}
	}

	latestMtime := func(files []string) time.Time {
		var t time.Time
		for _, f := range files {
			if info, err := os.Stat(f); err == nil && info.ModTime().After(t) {
				t = info.ModTime()
			}
		}
		return t
	}

	var stateMu sync.RWMutex
	current := buildAll(collectFiles())

	var clientsMu sync.Mutex
	clients := make(map[chan string]bool)
	broadcast := func(msg string) {
		clientsMu.Lock()
		for ch := range clients {
			select {
			case ch <- msg:
			default:
			}
		}
		clientsMu.Unlock()
	}

	go func() {
		var lastMtime time.Time
		var lastCount int
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for range ticker.C {
			files := collectFiles()
			mtime := latestMtime(files)
			if mtime.After(lastMtime) || len(files) != lastCount {
				lastMtime = mtime
				lastCount = len(files)
				res := buildAll(files)
				stateMu.Lock()
				current = res
				stateMu.Unlock()
				broadcast("reload")
			}
		}
	}()

	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(graphHTML))
	})

	mux.HandleFunc("/api/dot", func(w http.ResponseWriter, r *http.Request) {
		stateMu.RLock()
		dot := current.allResult.DOT
		stateMu.RUnlock()
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Write([]byte(dot))
	})

	mux.HandleFunc("/api/meta", func(w http.ResponseWriter, r *http.Request) {
		stateMu.RLock()
		res := current.allResult
		stateMu.RUnlock()

		type diagJSON struct {
			Severity string `json:"severity"`
			Message  string `json:"message"`
		}
		type sigJSON struct {
			LocalName string     `json:"localName"`
			DSName    string     `json:"dsName"`
			Type      string     `json:"type"`
			NumElems  string     `json:"numElems"`
			Doc       string     `json:"doc"`
			Dir       string     `json:"dir"`
			Implicit  bool       `json:"implicit"`
			Diags     []diagJSON `json:"diags,omitempty"`
		}
		type nodeJSON struct {
			Name        string            `json:"name"`
			Kind        string            `json:"kind"`
			Class       string            `json:"class"`
			Doc         string            `json:"doc"`
			Conditional bool              `json:"conditional"`
			IOGAM       bool              `json:"iogam"`
			Fields      map[string]string `json:"fields"`
			InSigs      []sigJSON         `json:"inSigs,omitempty"`
			OutSigs     []sigJSON         `json:"outSigs,omitempty"`
			DSSigs      []sigJSON         `json:"dsSigs,omitempty"`
			Diags       []diagJSON        `json:"diags,omitempty"`
			SplitSide   string            `json:"splitSide,omitempty"`
			CloneGroup  []string          `json:"cloneGroup,omitempty"`
		}

		toDiag := func(d graph.NodeDiag) diagJSON {
			sev := "error"
			if d.Severity == graph.DiagWarning {
				sev = "warning"
			}
			return diagJSON{Severity: sev, Message: d.Message}
		}
		toSig := func(s graph.SigInfo) sigJSON {
			sj := sigJSON{
				LocalName: s.LocalName, DSName: s.DSName,
				Type: s.Type, NumElems: s.NumElems,
				Doc: s.Doc, Dir: s.Dir, Implicit: s.Implicit,
			}
			for _, d := range s.Diags {
				sj.Diags = append(sj.Diags, toDiag(d))
			}
			return sj
		}

		out := make(map[string]nodeJSON)
		for id, n := range res.Meta {
			nj := nodeJSON{
				Name: n.Name, Kind: n.Kind, Class: n.Class,
				Doc: n.Doc, Conditional: n.Conditional, IOGAM: n.IOGAM,
				Fields: n.Fields, SplitSide: n.SplitSide, CloneGroup: n.CloneGroup,
			}
			for _, s := range n.InSigs {
				nj.InSigs = append(nj.InSigs, toSig(s))
			}
			for _, s := range n.OutSigs {
				nj.OutSigs = append(nj.OutSigs, toSig(s))
			}
			for _, s := range n.DSSigs {
				nj.DSSigs = append(nj.DSSigs, toSig(s))
			}
			for _, d := range n.Diags {
				nj.Diags = append(nj.Diags, toDiag(d))
			}
			out[id] = nj
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		json.NewEncoder(w).Encode(out)
	})

	mux.HandleFunc("/api/states", func(w http.ResponseWriter, r *http.Request) {
		stateMu.RLock()
		states := current.allResult.States
		stateMu.RUnlock()

		type threadJSON struct {
			GAMIDs []string `json:"gamIds"`
		}
		type stateJSON struct {
			Threads map[string]threadJSON `json:"threads"`
		}
		out := make(map[string]stateJSON)
		for name, si := range states {
			sj := stateJSON{Threads: make(map[string]threadJSON)}
			for t, ids := range si.Threads {
				sj.Threads[t] = threadJSON{GAMIDs: ids}
			}
			out[name] = sj
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		json.NewEncoder(w).Encode(out)
	})

	mux.HandleFunc("/api/focused", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST required", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			NodeIDs []string `json:"nodeIds"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.NodeIDs) == 0 {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}
		stateMu.RLock()
		c := current
		stateMu.RUnlock()
		res := graph.GenerateSubset(c.tree, c.nodeDiags, body.NodeIDs, c.allResult)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Write([]byte(res.DOT))
	})

	mux.HandleFunc("/events", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "SSE not supported", 500)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		ch := make(chan string, 8)
		clientsMu.Lock()
		clients[ch] = true
		clientsMu.Unlock()
		defer func() { clientsMu.Lock(); delete(clients, ch); clientsMu.Unlock() }()

		for {
			select {
			case msg := <-ch:
				fmt.Fprintf(w, "data: %s\n\n", msg)
				flusher.Flush()
			case <-r.Context().Done():
				return
			}
		}
	})

	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		logger.Fatalf("Listen error: %v\n", err)
	}
	port = ln.Addr().(*net.TCPAddr).Port
	url := fmt.Sprintf("http://localhost:%d", port)
	logger.Printf("MARTe graph server running at %s\n", url)
	logger.Println("Watching for file changes. Press Ctrl+C to stop.")

	go openBrowser(url)
	if err := http.Serve(ln, mux); err != nil {
		logger.Fatalf("Server error: %v\n", err)
	}
}

func findRelevantAncestor(tree *index.ProjectTree, n *index.ProjectNode) *index.ProjectNode {
	for cur := n; cur != nil; cur = cur.Parent {
		if tree.IsGAM(cur) || tree.IsDataSource(cur) || tree.IsSignal(cur) {
			return cur
		}
	}
	return n
}

// runGraphLSP starts the graph HTTP server in LSP-driven mode.
// Call this in a goroutine before lsp.RunServer(); it reacts to GraphNotifyFn callbacks
// from the LSP to rebuild the graph and push SSE events to connected browsers.
func runGraphLSP(port int) {
	var stateMu sync.RWMutex
	var current fullResult

	var clientsMu sync.Mutex
	clients := make(map[chan string]bool)
	broadcast := func(msg string) {
		clientsMu.Lock()
		for ch := range clients {
			select {
			case ch <- msg:
			default:
			}
		}
		clientsMu.Unlock()
	}

	// buildFromLSP rebuilds the graph data from the current LSP session snapshot.
	buildFromLSP := func() fullResult {
		if lsp.GlobalSession == nil {
			return fullResult{}
		}
		views := lsp.GlobalSession.Views()
		if len(views) == 0 {
			return fullResult{}
		}
		view := views[0]
		snap := view.Snapshot()
		tree := snap.Tree()
		if tree == nil || tree.Root == nil {
			return fullResult{}
		}

		v := validator.NewValidator(tree, view.Root(), nil)
		v.ValidateProject(context.Background())

		nodeDiags := make(map[*index.ProjectNode][]graph.NodeDiag)
		for _, d := range v.Diagnostics {
			node := tree.GetNodeContaining(d.File, d.Position)
			if node == nil {
				continue
			}
			target := findRelevantAncestor(tree, node)
			if target == nil {
				continue
			}
			sev := graph.DiagError
			if d.Level == validator.LevelWarning {
				sev = graph.DiagWarning
			}
			nodeDiags[target] = append(nodeDiags[target], graph.NodeDiag{
				Severity: sev, Message: d.Message,
			})
		}

		return fullResult{
			allResult: graph.Generate(tree, nodeDiags, ""),
			tree:      tree,
			nodeDiags: nodeDiags,
		}
	}

	// Wire LSP callbacks: "reload" rebuilds the graph; "focus" sends a highlight event.
	lsp.GraphNotifyFn = func(event, data string) {
		switch event {
		case "reload":
			res := buildFromLSP()
			stateMu.Lock()
			current = res
			stateMu.Unlock()
			broadcast("reload")
		case "focus":
			broadcast("focus:" + data)
		}
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(graphHTML))
	})

	mux.HandleFunc("/api/dot", func(w http.ResponseWriter, r *http.Request) {
		stateMu.RLock()
		dot := current.allResult.DOT
		stateMu.RUnlock()
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Write([]byte(dot))
	})

	mux.HandleFunc("/api/meta", func(w http.ResponseWriter, r *http.Request) {
		stateMu.RLock()
		res := current.allResult
		stateMu.RUnlock()

		type diagJSON struct {
			Severity string `json:"severity"`
			Message  string `json:"message"`
		}
		type sigJSON struct {
			LocalName string     `json:"localName"`
			DSName    string     `json:"dsName"`
			Type      string     `json:"type"`
			NumElems  string     `json:"numElems"`
			Doc       string     `json:"doc"`
			Dir       string     `json:"dir"`
			Implicit  bool       `json:"implicit"`
			Diags     []diagJSON `json:"diags,omitempty"`
		}
		type nodeJSON struct {
			Name        string            `json:"name"`
			Kind        string            `json:"kind"`
			Class       string            `json:"class"`
			Doc         string            `json:"doc"`
			Conditional bool              `json:"conditional"`
			IOGAM       bool              `json:"iogam"`
			Fields      map[string]string `json:"fields"`
			InSigs      []sigJSON         `json:"inSigs,omitempty"`
			OutSigs     []sigJSON         `json:"outSigs,omitempty"`
			DSSigs      []sigJSON         `json:"dsSigs,omitempty"`
			Diags       []diagJSON        `json:"diags,omitempty"`
			SplitSide   string            `json:"splitSide,omitempty"`
			CloneGroup  []string          `json:"cloneGroup,omitempty"`
		}
		toDiag := func(d graph.NodeDiag) diagJSON {
			sev := "error"
			if d.Severity == graph.DiagWarning {
				sev = "warning"
			}
			return diagJSON{Severity: sev, Message: d.Message}
		}
		toSig := func(s graph.SigInfo) sigJSON {
			sj := sigJSON{
				LocalName: s.LocalName, DSName: s.DSName,
				Type: s.Type, NumElems: s.NumElems,
				Doc: s.Doc, Dir: s.Dir, Implicit: s.Implicit,
			}
			for _, d := range s.Diags {
				sj.Diags = append(sj.Diags, toDiag(d))
			}
			return sj
		}
		out := make(map[string]nodeJSON)
		for id, n := range res.Meta {
			nj := nodeJSON{
				Name: n.Name, Kind: n.Kind, Class: n.Class,
				Doc: n.Doc, Conditional: n.Conditional, IOGAM: n.IOGAM,
				Fields: n.Fields, SplitSide: n.SplitSide, CloneGroup: n.CloneGroup,
			}
			for _, s := range n.InSigs {
				nj.InSigs = append(nj.InSigs, toSig(s))
			}
			for _, s := range n.OutSigs {
				nj.OutSigs = append(nj.OutSigs, toSig(s))
			}
			for _, s := range n.DSSigs {
				nj.DSSigs = append(nj.DSSigs, toSig(s))
			}
			for _, d := range n.Diags {
				nj.Diags = append(nj.Diags, toDiag(d))
			}
			out[id] = nj
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		json.NewEncoder(w).Encode(out)
	})

	mux.HandleFunc("/api/states", func(w http.ResponseWriter, r *http.Request) {
		stateMu.RLock()
		states := current.allResult.States
		stateMu.RUnlock()
		type threadJSON struct {
			GAMIDs []string `json:"gamIds"`
		}
		type stateJSON struct {
			Threads map[string]threadJSON `json:"threads"`
		}
		out := make(map[string]stateJSON)
		for name, si := range states {
			sj := stateJSON{Threads: make(map[string]threadJSON)}
			for t, ids := range si.Threads {
				sj.Threads[t] = threadJSON{GAMIDs: ids}
			}
			out[name] = sj
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		json.NewEncoder(w).Encode(out)
	})

	mux.HandleFunc("/api/focused", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST required", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			NodeIDs []string `json:"nodeIds"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.NodeIDs) == 0 {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}
		stateMu.RLock()
		c := current
		stateMu.RUnlock()
		res := graph.GenerateSubset(c.tree, c.nodeDiags, body.NodeIDs, c.allResult)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Write([]byte(res.DOT))
	})

	mux.HandleFunc("/events", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "SSE not supported", 500)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		ch := make(chan string, 8)
		clientsMu.Lock()
		clients[ch] = true
		clientsMu.Unlock()
		defer func() { clientsMu.Lock(); delete(clients, ch); clientsMu.Unlock() }()
		for {
			select {
			case msg := <-ch:
				fmt.Fprintf(w, "data: %s\n\n", msg)
				flusher.Flush()
			case <-r.Context().Done():
				return
			}
		}
	})

	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		logger.Fatalf("Graph listen error: %v\n", err)
	}
	port = ln.Addr().(*net.TCPAddr).Port
	url := fmt.Sprintf("http://localhost:%d", port)
	logger.Printf("MARTe graph server running at %s (LSP mode)\n", url)
	go openBrowser(url)
	if err := http.Serve(ln, mux); err != nil {
		logger.Fatalf("Graph server error: %v\n", err)
	}
}

func openBrowser(url string) {
	time.Sleep(300 * time.Millisecond)
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		return
	}
	cmd.Start()
}

const graphHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>MARTe Signal Flow Graph</title>
  <style>
    *, *::before, *::after { box-sizing: border-box; margin: 0; padding: 0; }
    body {
      background: #0d1117; color: #c9d1d9;
      font-family: 'Consolas', 'Monaco', monospace;
      height: 100vh; display: flex; flex-direction: column; overflow: hidden;
    }

    /* ── Header ── */
    #header {
      background: #161b22; border-bottom: 1px solid #21262d;
      padding: 0 8px; display: flex; align-items: center; gap: 6px;
      flex-shrink: 0; height: 38px;
    }
    #header h1 { font-size: 11px; color: #58a6ff; letter-spacing: 1px; text-transform: uppercase; white-space: nowrap; }
    #legend { display: flex; gap: 6px; flex-shrink: 0; }
    .li { display: flex; align-items: center; gap: 3px; font-size: 10px; color: #8b949e; white-space: nowrap; }
    .lb { width: 8px; height: 8px; border: 1px solid; flex-shrink: 0; }
    .ll { width: 14px; height: 2px; flex-shrink: 0; border-radius: 1px; }
    #hdr-sep { width: 1px; height: 20px; background: #21262d; flex-shrink: 0; }
    .hdr-btn {
      height: 24px; min-width: 24px; padding: 0 6px;
      background: none; border: 1px solid #30363d; border-radius: 3px;
      color: #8b949e; font-size: 13px; cursor: pointer; font-family: inherit;
      display: flex; align-items: center; justify-content: center; flex-shrink: 0;
    }
    .hdr-btn:hover { background: #21262d; color: #c9d1d9; border-color: #58a6ff; }
    .hdr-btn.active { color: #58a6ff; border-color: #58a6ff; }
    #controls { display: flex; align-items: center; gap: 5px; margin-left: auto; }
    select.hdr-sel {
      background: #161b22; color: #c9d1d9; border: 1px solid #30363d;
      border-radius: 3px; font-size: 10px; padding: 2px 5px; cursor: pointer;
      font-family: inherit; height: 24px;
    }
    select.hdr-sel:focus { outline: none; border-color: #58a6ff; }
    #thread-select { display: none; }
    #sel-info { font-size: 10px; color: #e3b341; white-space: nowrap; display: none; }
    #btn-clear {
      font-size: 10px; background: none; border: 1px solid #30363d;
      color: #8b949e; border-radius: 3px; padding: 0 6px; height: 24px; cursor: pointer; display: none;
    }
    #btn-clear:hover { border-color: #8b949e; color: #c9d1d9; }
    #status { font-size: 10px; color: #6e7681; white-space: nowrap; margin-left: 4px; }

    /* ── Body ── */
    #body { flex: 1; display: flex; overflow: hidden; }

    /* ── Sidebar ── */
    #sidebar {
      width: 220px; min-width: 220px;
      background: #0d1117; border-right: 1px solid #21262d;
      display: flex; flex-direction: column; overflow: hidden;
      transition: width 0.18s ease, min-width 0.18s ease, border 0.18s ease;
    }
    #sidebar.collapsed { width: 0; min-width: 0; border-right: none; }
    #sb-header {
      background: #161b22; border-bottom: 1px solid #21262d;
      padding: 5px 6px; display: flex; align-items: center; gap: 5px; flex-shrink: 0;
    }
    #sb-title { font-size: 10px; color: #484f58; text-transform: uppercase; letter-spacing: 0.5px; white-space: nowrap; }
    #sb-filter {
      flex: 1; min-width: 0; background: #0d1117; border: 1px solid #21262d;
      border-radius: 3px; color: #c9d1d9; font-size: 10px; padding: 2px 5px;
      font-family: inherit; outline: none;
    }
    #sb-filter:focus { border-color: #58a6ff; }
    #sb-tree { flex: 1; overflow-y: auto; overflow-x: hidden; }
    .sb-section {
      font-size: 9px; color: #58676f; text-transform: uppercase; letter-spacing: 0.5px;
      padding: 8px 8px 3px; border-top: 1px solid #161b22;
    }
    .sb-section:first-child { border-top: none; }
    .sb-node {
      display: flex; align-items: center; gap: 4px; padding: 3px 8px;
      cursor: pointer; font-size: 11px; color: #b0bcc8; white-space: nowrap;
      overflow: hidden;
    }
    .sb-node:hover { background: #161b22; }
    .sb-node.focused { background: #1c2333; color: #58a6ff; }
    .sb-node.dimmed { opacity: 0.3; }
    .sb-toggle { font-size: 8px; flex-shrink: 0; color: #6a7680; width: 10px; text-align: center; }
    .sb-icon { flex-shrink: 0; font-size: 10px; }
    .sb-name { flex: 1; overflow: hidden; text-overflow: ellipsis; }
    .sb-cls { font-size: 9px; color: #484f58; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; margin-left: 2px; }
    .sb-sig {
      display: flex; align-items: center; gap: 4px; padding: 2px 8px 2px 24px;
      cursor: pointer; font-size: 10px; color: #484f58; white-space: nowrap; overflow: hidden;
    }
    .sb-sig:hover { background: #0d1a2a; color: #8b949e; }
    .sb-sig .arrow { font-size: 10px; flex-shrink: 0; }
    .sb-sig .arrow.ds-out { color: #3d8fdd; }
    .sb-sig .arrow.ds-in  { color: #c07030; }
    .sb-sig .arrow.ds-both{ color: #7878a0; }
    .sb-sig .arrow.in     { color: #4d8fdd; }
    .sb-sig .arrow.out    { color: #d07030; }
    .sb-sig-name { overflow: hidden; text-overflow: ellipsis; }

    /* ── Pin button in sidebar ── */
    .sb-pin {
      font-size: 10px; cursor: pointer; flex-shrink: 0;
      color: #3a4856; padding: 0 3px; border-radius: 2px;
      transition: color 0.15s, opacity 0.15s; opacity: 0.4; line-height: 1;
    }
    .sb-pin:hover { opacity: 1; color: #8b949e; }
    .sb-pin.pinned { color: #58a6ff; opacity: 1; }

    /* ── Graph area ── */
    #graph-wrap { flex: 1; display: flex; flex-direction: column; overflow: hidden; position: relative; }
    #error {
      display: none; padding: 7px 14px;
      background: #1a0a0a; border-bottom: 1px solid #6e2020;
      color: #f85149; font-size: 11px; flex-shrink: 0;
    }
    #graph {
      flex: 1; overflow: hidden; position: relative; cursor: default;
      transition: opacity 0.18s ease;
      background-color: #0d1117;
      background-image:
        linear-gradient(rgba(255,255,255,0.03) 1px, transparent 1px),
        linear-gradient(90deg, rgba(255,255,255,0.03) 1px, transparent 1px);
      background-size: 24px 24px;
    }
    #graph svg { display: block; width: 100%; height: 100%; }
    #sel-hint {
      position: absolute; bottom: 8px; left: 10px;
      font-size: 9px; color: #3a4856; pointer-events: none; z-index: 10; line-height: 1.7;
    }


    /* ── Search overlay ── */
    #search-overlay {
      display: none; position: fixed; inset: 0; z-index: 200;
      background: rgba(0,0,0,0.55); align-items: flex-start; justify-content: center;
    }
    #search-overlay.active { display: flex; }
    #search-box {
      margin-top: 80px; width: 480px; background: #161b22;
      border: 1px solid #30363d; border-radius: 8px; overflow: hidden;
      box-shadow: 0 8px 32px rgba(0,0,0,0.7);
    }
    #search-input {
      width: 100%; background: transparent; border: none; outline: none;
      color: #c9d1d9; font-family: inherit; font-size: 14px; padding: 12px 14px;
    }
    #search-results { max-height: 320px; overflow-y: auto; border-top: 1px solid #21262d; }
    .search-item {
      padding: 8px 14px; cursor: pointer; display: flex; align-items: center; gap: 8px;
      font-size: 12px;
    }
    .search-item:hover, .search-item.active { background: #21262d; }
    .search-item .si-kind {
      font-size: 9px; padding: 1px 5px; border-radius: 3px; text-transform: uppercase;
      letter-spacing: 0.5px; flex-shrink: 0;
    }
    .si-kind.gam  { background: #181824; color: #7878a0; border: 1px solid #383850; }
    .si-kind.ds   { background: #0b1e30; color: #4a7a9b; border: 1px solid #1a6a9a; }
    .si-kind.iogam{ background: #1c1e30; color: #8080b0; border: 1px solid #404468; }
    .si-kind.sig  { background: #071524; color: #2a5a7b; border: 1px solid #1a3a52; }
    .search-item .si-name { flex: 1; }
    .search-item .si-parent { font-size: 10px; color: #484f58; }
    #search-hint { padding: 6px 14px; font-size: 10px; color: #484f58; border-top: 1px solid #21262d; }

    /* ── Tooltip ── */
    #tooltip {
      position: fixed; z-index: 1000; background: #161b22; border: 1px solid #30363d;
      border-radius: 6px; padding: 10px 12px; font-size: 11px; color: #c9d1d9;
      max-width: 340px; pointer-events: none; display: none; line-height: 1.5;
      box-shadow: 0 4px 16px rgba(0,0,0,0.6);
    }
    #tooltip .tt-name  { font-size: 13px; font-weight: bold; color: #58a6ff; }
    #tooltip .tt-class { font-size: 10px; color: #8b949e; margin-bottom: 5px; }
    #tooltip .tt-doc   { font-size: 10px; color: #6e7681; font-style: italic; margin-bottom: 5px; white-space: pre-wrap; }
    #tooltip .tt-cond  { font-size: 10px; color: #e3b341; margin-bottom: 4px; }
    #tooltip .tt-section { font-size: 9px; color: #484f58; text-transform: uppercase; letter-spacing: 0.5px; margin: 5px 0 2px; border-top: 1px solid #21262d; padding-top: 4px; }
    #tooltip .tt-field { font-size: 10px; color: #7878a0; }
    #tooltip .tt-field span { color: #c9d1d9; }
    #tooltip .tt-sig   { font-size: 10px; }
    #tooltip .tt-sig.in  { color: #4d8fdd; }
    #tooltip .tt-sig.out { color: #d07030; }
    #tooltip .tt-sig.ds  { color: #4a7a9b; }
    #tooltip .tt-sig.implicit { color: #2a5a7b; font-style: italic; }
    #tooltip .tt-diag-error { font-size: 10px; color: #d73a49; }
    #tooltip .tt-diag-warn  { font-size: 10px; color: #e3b341; }
    #tooltip .tt-iogam-pair { font-size: 10px; color: #8b949e; display: flex; gap: 8px; justify-content: space-between; }
    #tooltip .tt-iogam-pair .inp { color: #4d8fdd; }
    #tooltip .tt-iogam-pair .out { color: #d07030; }

    /* ── Help modal ── */
    #help-overlay {
      display: none; position: fixed; inset: 0; z-index: 300;
      background: rgba(0,0,0,0.6); align-items: center; justify-content: center;
    }
    #help-overlay.active { display: flex; }
    #help-box {
      width: 500px; background: #161b22; border: 1px solid #30363d;
      border-radius: 8px; overflow: hidden; box-shadow: 0 8px 32px rgba(0,0,0,0.7);
      max-height: 80vh; overflow-y: auto;
    }
    #help-box h2 { font-size: 12px; color: #58a6ff; padding: 10px 14px; border-bottom: 1px solid #21262d; letter-spacing: 0.5px; }
    .help-section { padding: 7px 14px; border-top: 1px solid #21262d; }
    .help-section:first-of-type { border-top: none; }
    .help-section h3 { font-size: 9px; color: #484f58; text-transform: uppercase; letter-spacing: 0.5px; margin-bottom: 4px; }
    .help-row { display: flex; gap: 10px; font-size: 11px; padding: 2px 0; }
    .help-key { color: #8b949e; font-family: monospace; min-width: 130px; flex-shrink: 0; }
    .help-key kbd { background:#21262d; border:1px solid #30363d; border-radius:3px; padding:0 4px; color:#c9d1d9; font-size:10px; }
    .help-desc { color: #6e7681; }
  </style>
</head>
<body>
  <!-- Header -->
  <div id="header">
    <button class="hdr-btn active" id="btn-sidebar" title="Toggle object tree">☰</button>
    <h1>MARTe Signal Flow</h1>
    <div id="hdr-sep"></div>
    <div id="legend">
      <div class="li"><div class="lb" style="background:#0b1e30;border-color:#1a6a9a"></div>DS</div>
      <div class="li"><div class="lb" style="background:#1c1e30;border-color:#404468"></div>IOGAM</div>
      <div class="li"><div class="lb" style="background:#241c08;border-color:#604400"></div>MsgGAM</div>
      <div class="li"><div class="lb" style="background:#181824;border-color:#383850"></div>GAM</div>
      <div class="li"><div class="ll" style="background:#3d6fd6"></div>read</div>
      <div class="li"><div class="ll" style="background:#c87941"></div>write</div>
    </div>
    <div id="controls">
      <select class="hdr-sel" id="state-select" title="Filter by state"><option value="">All states</option></select>
      <select class="hdr-sel" id="thread-select" title="Filter by thread"><option value="">All threads</option></select>
      <div id="hdr-sep"></div>
      <span id="sel-info"></span>
      <button id="btn-clear">✕ Clear</button>
      <button class="hdr-btn" id="btn-focus"      title="Draw focused layout for selected nodes" style="display:none">⊞ Focus</button>
      <button class="hdr-btn" id="btn-watchlist"  title="Draw watchlist graph" style="display:none">◉ 0</button>
      <button class="hdr-btn active" id="btn-exit-focus" title="Return to full graph" style="display:none">← Full</button>
      <div id="hdr-sep"></div>
      <button class="hdr-btn" id="btn-home"    title="Reset view (Home)">⌂</button>
      <button class="hdr-btn" id="btn-zoomin"  title="Zoom in (+)">+</button>
      <button class="hdr-btn" id="btn-zoomout" title="Zoom out (−)">−</button>
      <div id="hdr-sep"></div>
      <button class="hdr-btn" id="btn-search"  title="Search (/)">⌕</button>
      <button class="hdr-btn" id="btn-help"    title="Help (?)">?</button>
      <div id="hdr-sep"></div>
      <span id="status">Loading…</span>
    </div>
  </div>

  <!-- Body -->
  <div id="body">
    <!-- Sidebar -->
    <div id="sidebar">
      <div id="sb-header">
        <span id="sb-title">Objects</span>
        <input id="sb-filter" placeholder="Filter…" autocomplete="off" spellcheck="false"/>
      </div>
      <div id="sb-tree"></div>
    </div>

    <!-- Graph -->
    <div id="graph-wrap">
      <div id="error"></div>
      <div id="graph">
        <div id="sel-hint">click · shift+click multi · bg=clear · / search</div>
      </div>
    </div>
  </div>

  <div id="tooltip"></div>

  <!-- Help overlay -->
  <div id="help-overlay">
    <div id="help-box">
      <h2>MARTe Signal Flow Graph — Features &amp; Shortcuts</h2>
      <div class="help-section">
        <h3>Navigation</h3>
        <div class="help-row"><span class="help-key"><kbd>/</kbd> or ⌕ button</span><span class="help-desc">Open node/signal search</span></div>
        <div class="help-row"><span class="help-key"><kbd>Home</kbd> / <kbd>h</kbd></span><span class="help-desc">Reset view — fit entire graph</span></div>
        <div class="help-row"><span class="help-key">+ / − buttons</span><span class="help-desc">Zoom in / out</span></div>
        <div class="help-row"><span class="help-key"><kbd>Tab</kbd></span><span class="help-desc">Cycle split DataSource clones (read ↔ write)</span></div>
      </div>
      <div class="help-section">
        <h3>Selection</h3>
        <div class="help-row"><span class="help-key">Click node</span><span class="help-desc">Select node and highlight its connections</span></div>
        <div class="help-row"><span class="help-key">Shift+click</span><span class="help-desc">Add node to multi-selection</span></div>
        <div class="help-row"><span class="help-key">Click background</span><span class="help-desc">Clear selection</span></div>
        <div class="help-row"><span class="help-key">✕ Clear button</span><span class="help-desc">Clear selection</span></div>
      </div>
      <div class="help-section">
        <h3>Focus &amp; Watchlist</h3>
        <div class="help-row"><span class="help-key">⊞ Focus button</span><span class="help-desc">Draw optimised layout for selected nodes + 1-hop neighbours</span></div>
        <div class="help-row"><span class="help-key">⊕ pin (sidebar)</span><span class="help-desc">Pin/unpin a node to the watchlist</span></div>
        <div class="help-row"><span class="help-key">◉ N button</span><span class="help-desc">Draw optimised watchlist graph (pinned nodes + their DataSources)</span></div>
        <div class="help-row"><span class="help-key">← Full button</span><span class="help-desc">Return to full graph and clear all filters</span></div>
      </div>
      <div class="help-section">
        <h3>State &amp; Thread Filtering</h3>
        <div class="help-row"><span class="help-key">State dropdown</span><span class="help-desc">Show only GAMs active in a MARTe state with optimised layout</span></div>
        <div class="help-row"><span class="help-key">Thread dropdown</span><span class="help-desc">Show only GAMs in a specific thread with optimised layout</span></div>
        <div class="help-row"><span class="help-key">← Full button</span><span class="help-desc">Clear state/thread filter and return to full graph</span></div>
      </div>
      <div class="help-section">
        <h3>Sidebar</h3>
        <div class="help-row"><span class="help-key">Click node</span><span class="help-desc">Expand signals, select in graph and zoom in</span></div>
        <div class="help-row"><span class="help-key">Ctrl+click</span><span class="help-desc">Deselect a node</span></div>
        <div class="help-row"><span class="help-key">Sidebar filter box</span><span class="help-desc">Filter object tree by node name or signal name</span></div>
        <div class="help-row"><span class="help-key">☰ button</span><span class="help-desc">Collapse / expand sidebar</span></div>
      </div>
      <div class="help-section">
        <h3>Live Reload</h3>
        <div class="help-row"><span class="help-key">Automatic</span><span class="help-desc">Graph reloads when .marte files change; pan/zoom position is preserved</span></div>
      </div>
    </div>
  </div>

  <!-- Search overlay -->
  <div id="search-overlay">
    <div id="search-box">
      <input id="search-input" placeholder="Search nodes and signals…" autocomplete="off" spellcheck="false"/>
      <div id="search-results"></div>
      <div id="search-hint">↑↓ navigate · Enter select · Esc close</div>
    </div>
  </div>

  <script src="https://unpkg.com/@viz-js/viz@3.10.0/lib/viz-standalone.js"></script>
  <script src="https://unpkg.com/svg-pan-zoom@3.6.1/dist/svg-pan-zoom.min.js"></script>
  <script>
  (() => {
    const $ = id => document.getElementById(id);
    const statusEl     = $('status');
    const errorEl      = $('error');
    const graphEl      = $('graph');
    const selInfoEl    = $('sel-info');
    const btnClear     = $('btn-clear');
    const tooltipEl    = $('tooltip');
    const stateSelEl   = $('state-select');
    const threadSelEl  = $('thread-select');
    const sidebarEl    = $('sidebar');
    const sbTreeEl     = $('sb-tree');
    const sbFilterEl   = $('sb-filter');
    const searchOverlay= $('search-overlay');
    const searchInput  = $('search-input');
    const searchResults= $('search-results');

    // ── State ──────────────────────────────────────────────────────────────
    let vizInstance      = null;
    let panZoom          = null;
    let adj              = {};    // nodeId → Set<nodeId>
    let selected         = new Set();
    let metaData         = {};    // nodeId → nodeInfo
    let statesData       = {};    // stateName → {threads: {name: {gamIds:[]}}}
    let currentState     = '';
    let currentThread    = '';
    let stateVisible     = null;  // Set<nodeId> when state filter active, else null
    let threadVisible    = null;  // Set<nodeId> when thread filter active, else null
    let sidebarExpanded  = new Set();
    let searchItems      = [];
    let searchActiveIdx  = -1;
    let sidebarOpen      = true;
    let mainDot          = null;  // always the full-graph DOT, kept fresh on reload
    let focusMode        = false; // true when showing a focused/watchlist subset
    let watchlist        = new Set(); // node IDs pinned to watchlist
    let pendingRestore   = null;  // {pan, zoom} to restore after file-change reload
    let nameToId         = {};    // node RealName → graph node ID (for LSP cursor tracking)
    let focusDebounce    = null;  // debounce timer for LSP focus events

    function esc(s) {
      return String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
    }
    function setStatus(msg) { statusEl.textContent = msg; }
    function showError(msg) { errorEl.style.display='block'; errorEl.textContent='Error: '+msg; }
    function clearError()   { errorEl.style.display='none'; }

    // ── Sidebar toggle ─────────────────────────────────────────────────────
    $('btn-sidebar').addEventListener('click', () => {
      sidebarOpen = !sidebarOpen;
      sidebarEl.classList.toggle('collapsed', !sidebarOpen);
      $('btn-sidebar').classList.toggle('active', sidebarOpen);
      setTimeout(() => { if (panZoom) panZoom.resize(); }, 200);
    });

    // ── Init ───────────────────────────────────────────────────────────────
    Viz.instance()
      .then(v => { vizInstance = v; initStates(); })
      .catch(err => showError('Viz.js: ' + err));

    async function initStates() {
      try {
        const r = await fetch('/api/states');
        statesData = await r.json();
        const names = Object.keys(statesData).sort();
        names.forEach(s => {
          const opt = document.createElement('option');
          opt.value = s; opt.textContent = s;
          stateSelEl.appendChild(opt);
        });
      } catch(_) {}
      loadGraph();
    }

    stateSelEl.addEventListener('change', () => {
      currentState  = stateSelEl.value;
      currentThread = '';
      selected.clear();
      threadVisible = null;
      rebuildThreadSelect();
      applyStateFilter();
      applyFilteredLayout();
    });

    threadSelEl.addEventListener('change', () => {
      currentThread = threadSelEl.value;
      applyThreadFilter();
      applyFilteredLayout();
    });

    function rebuildThreadSelect() {
      // Reset thread dropdown
      threadSelEl.innerHTML = '<option value="">All threads</option>';
      threadSelEl.style.display = 'none';
      if (!currentState || !statesData[currentState]) return;
      const threads = Object.keys(statesData[currentState].threads || {}).sort();
      if (!threads.length) return;
      threads.forEach(t => {
        const opt = document.createElement('option');
        opt.value = t; opt.textContent = t;
        threadSelEl.appendChild(opt);
      });
      threadSelEl.style.display = 'inline-block';
    }

    // expandDSClones adds all cloneGroup members for every DS node already in vis.
    function expandDSClones(vis) {
      [...vis].forEach(id => {
        const n = metaData[id];
        if (n && n.kind === 'ds' && n.cloneGroup) {
          n.cloneGroup.forEach(cid => vis.add(cid));
        }
      });
    }

    function applyStateFilter() {
      if (!currentState || !statesData[currentState]) {
        stateVisible = null; return;
      }
      const gamIds = new Set();
      Object.values(statesData[currentState].threads || {}).forEach(td => {
        (td.gamIds || []).forEach(id => gamIds.add(id));
      });
      const vis = new Set(gamIds);
      gamIds.forEach(id => {
        (adj[id] || new Set()).forEach(nb => {
          if (metaData[nb] && metaData[nb].kind === 'ds') vis.add(nb);
        });
      });
      expandDSClones(vis);
      stateVisible = vis;
    }

    function applyThreadFilter() {
      if (!currentThread || !currentState || !statesData[currentState]) {
        threadVisible = null; return;
      }
      const td = statesData[currentState].threads[currentThread];
      if (!td) { threadVisible = null; return; }
      const gamIds = new Set(td.gamIds || []);
      const vis = new Set(gamIds);
      // Include DS neighbours of thread GAMs (and their clone siblings)
      gamIds.forEach(id => {
        (adj[id] || new Set()).forEach(nb => {
          if (metaData[nb] && metaData[nb].kind === 'ds') vis.add(nb);
        });
      });
      expandDSClones(vis);
      threadVisible = vis;
    }

    // ── Filtered layout ────────────────────────────────────────────────────
    // Called when state/thread filter changes. Generates a focused layout for
    // the visible nodes, or returns to the full graph when no filter is active.
    async function applyFilteredLayout() {
      const vis = threadVisible ?? stateVisible;
      if (vis && vis.size > 0) {
        await loadFocusedGraph(vis);
      } else {
        focusMode = false;
        $('btn-exit-focus').style.display = 'none';
        $('btn-focus').style.display = 'none';
        if (mainDot) renderDot(mainDot, true);
        else loadGraph();
      }
      renderSidebar(sbFilterEl.value);
    }

    // ── Load ───────────────────────────────────────────────────────────────
    function loadGraph() {
      setStatus('Loading…');
      Promise.all([
        fetch('/api/dot').then(r => { if (!r.ok) throw new Error('HTTP '+r.status); return r.text(); }),
        fetch('/api/meta').then(r => r.json()).catch(() => ({})),
      ])
      .then(([dot, meta]) => {
        mainDot = dot;
        metaData = meta;
        // Build name → ID map for LSP cursor tracking.
        nameToId = {};
        Object.entries(metaData).forEach(([id, n]) => { if (!nameToId[n.name]) nameToId[n.name] = id; });
        buildSearchIndex();
        // Exit focus mode on reload (file changed → show updated full graph).
        focusMode = false;
        $('btn-exit-focus').style.display = 'none';
        renderDot(dot, false);
        renderSidebar('');
      })
      .catch(err => { showError(err.message); setStatus('Error'); });
    }

    // ── Render ─────────────────────────────────────────────────────────────
    // animate=true: fade out current graph, swap SVG, fade in.
    // View is restored from pendingRestore if set (file-change reload).
    function renderDot(dot, animate) {
      if (!vizInstance) return;

      const doRender = () => {
        let svg;
        try { svg = vizInstance.renderSVGElement(dot); }
        catch(e) { showError('Render: '+e.message); graphEl.style.opacity='1'; return; }
        clearError();

        svg.querySelectorAll('.graph > polygon, .graph > ellipse').forEach(el => {
          el.setAttribute('fill','none'); el.setAttribute('stroke','none');
        });

        const hint = $('sel-hint');
        graphEl.innerHTML = '';
        graphEl.appendChild(svg);
        graphEl.appendChild(hint);

        svg.setAttribute('width','100%'); svg.setAttribute('height','100%');

        if (panZoom) { try { panZoom.destroy(); } catch(_) {} panZoom = null; }

        panZoom = svgPanZoom(svg, {
          zoomEnabled: true, panEnabled: true, controlIconsEnabled: false,
          dblClickZoomEnabled: true, fit: true, center: true,
          minZoom: 0.03, maxZoom: 100,
        });

        // Restore saved view (file-change reload) or fit/center (layout change).
        if (pendingRestore) {
          panZoom.zoom(pendingRestore.zoom);
          panZoom.pan(pendingRestore.pan);
          pendingRestore = null;
        } else {
          panZoom.fit(); panZoom.center();
        }

        // Build adjacency
        adj = {};
        svg.querySelectorAll('.edge').forEach(el => {
          const raw = el.querySelector('title')?.textContent ?? '';
          const idx = raw.indexOf('->');
          if (idx < 0) return;
          const from = raw.substring(0,idx).trim().split(':')[0];
          const to   = raw.substring(idx+2).trim().split(':')[0];
          if (!adj[from]) adj[from] = new Set();
          if (!adj[to])   adj[to]   = new Set();
          adj[from].add(to); adj[to].add(from);
        });

        // Re-apply state/thread filters with updated adjacency
        applyStateFilter();
        applyThreadFilter();

        svg.querySelectorAll('.node').forEach(el => {
          const id = el.querySelector('title')?.textContent?.trim()?.split(':')[0];
          if (!id) return;
          el.style.cursor = 'pointer';
          el.addEventListener('click', e => {
            e.stopPropagation(); hideTooltip();
            if (e.shiftKey) {
              if (selected.has(id)) selected.delete(id); else selected.add(id);
            } else {
              selected = (selected.size===1 && selected.has(id)) ? new Set() : new Set([id]);
            }
            applyFilter(svg);
          });
          el.addEventListener('mouseover',  e => showTooltip(id, e.clientX, e.clientY));
          el.addEventListener('mousemove',  e => { if (tooltipEl.style.display!=='none') placeTooltip(e.clientX, e.clientY); });
          el.addEventListener('mouseleave', () => hideTooltip());
        });
        svg.addEventListener('click', () => { selected.clear(); applyFilter(svg); });

        selected.clear();
        applyFilter(svg);
        setStatus('Updated '+new Date().toLocaleTimeString());

        if (animate) {
          requestAnimationFrame(() => { graphEl.style.opacity = '1'; });
        }
      };

      if (animate) {
        graphEl.style.opacity = '0';
        setTimeout(doRender, 180);
      } else {
        doRender();
      }
    }

    // ── Filter ─────────────────────────────────────────────────────────────
    function applyFilter(svg) {
      const any = selected.size > 0;
      btnClear.style.display  = any ? '' : 'none';
      selInfoEl.style.display = any ? '' : 'none';
      $('btn-focus').style.display = (any && !focusMode) ? '' : 'none';
      if (any) {
        selInfoEl.textContent = selected.size===1
          ? metaData[[...selected][0]]?.name || [...selected][0]
          : selected.size+' selected';
      }

      // Compute visible set from selection
      let selVisible = null;
      if (any) {
        const hoods = [...selected].map(id => { const s=new Set(adj[id]||[]); s.add(id); return s; });
        selVisible = hoods[0];
        for (let i=1;i<hoods.length;i++) selVisible = new Set([...selVisible].filter(x=>hoods[i].has(x)));
        selected.forEach(id => selVisible.add(id));
      }

      // nodeFilter: thread > state > none (thread is always a strict subset of state)
      const nodeFilter = threadVisible ?? stateVisible;

      svg.querySelectorAll('.node').forEach(el => {
        const id = el.querySelector('title')?.textContent?.trim()?.split(':')[0];
        let visible = true;
        if (nodeFilter && !nodeFilter.has(id)) visible = false;
        if (selVisible && !selVisible.has(id)) visible = false;
        el.style.opacity = visible ? '1' : (selVisible ? '0.22' : '0.22');
        el.style.filter  = (any && selected.has(id) && visible) ? 'drop-shadow(0 0 5px #58a6ff)' : '';
      });
      svg.querySelectorAll('.edge').forEach(el => {
        const raw = el.querySelector('title')?.textContent ?? '';
        const idx = raw.indexOf('->');
        if (idx<0) { el.style.opacity='0.12'; return; }
        const from = raw.substring(0,idx).trim().split(':')[0];
        const to   = raw.substring(idx+2).trim().split(':')[0];
        let vis = true;
        if (nodeFilter && (!nodeFilter.has(from) || !nodeFilter.has(to))) vis = false;
        if (selVisible  && (!selVisible.has(from)  || !selVisible.has(to)))  vis = false;
        el.style.opacity = vis ? '1' : '0.12';
      });
    }

    btnClear.addEventListener('click', () => {
      selected.clear();
      const s = graphEl.querySelector('svg'); if (s) applyFilter(s);
      renderSidebar(sbFilterEl.value);
    });

    // ── Focused layout ─────────────────────────────────────────────────────
    async function loadFocusedGraph(nodeIds) {
      setStatus('Rendering…');
      try {
        const r = await fetch('/api/focused', {
          method: 'POST',
          headers: {'Content-Type':'application/json'},
          body: JSON.stringify({nodeIds: [...nodeIds]})
        });
        if (!r.ok) throw new Error('HTTP '+r.status);
        const dot = await r.text();
        focusMode = true;
        $('btn-exit-focus').style.display = '';
        $('btn-focus').style.display = 'none';
        renderDot(dot, true);
      } catch(e) { showError('Focused layout: '+e.message); }
    }

    $('btn-focus').addEventListener('click', () => {
      if (selected.size === 0) return;
      // Include selected nodes + their 1-hop neighbours for context.
      const ids = new Set(selected);
      selected.forEach(id => { (adj[id]||new Set()).forEach(nb => ids.add(nb)); });
      loadFocusedGraph(ids);
    });

    $('btn-exit-focus').addEventListener('click', () => {
      focusMode = false;
      currentState = '';
      currentThread = '';
      stateVisible = null;
      threadVisible = null;
      stateSelEl.value = '';
      threadSelEl.value = '';
      threadSelEl.style.display = 'none';
      $('btn-exit-focus').style.display = 'none';
      $('btn-focus').style.display = 'none';
      if (mainDot) renderDot(mainDot, true);
      renderSidebar(sbFilterEl.value);
    });

    $('btn-watchlist').addEventListener('click', () => {
      if (watchlist.size === 0) return;
      // Include watched nodes + their DS neighbours to show signal connections.
      const ids = new Set(watchlist);
      watchlist.forEach(id => {
        (adj[id]||new Set()).forEach(nb => {
          if (metaData[nb]?.kind === 'ds') ids.add(nb);
        });
      });
      loadFocusedGraph(ids);
    });

    // ── Sidebar tree ───────────────────────────────────────────────────────
    function renderSidebar(filter) {
      const fq = filter.toLowerCase().trim();
      let html = '';

      // For split DS (cloneGroup), show read clone first then write clone.
      // Deduplicate by tracking which cloneGroups we've already added.
      const seenCloneBase = new Set();
      const dsList = [];
      for (const [id, n] of Object.entries(metaData)) {
        if (n.kind !== 'ds') continue;
        if (n.cloneGroup && n.cloneGroup.length > 1) {
          const baseKey = n.cloneGroup[0]; // use first clone ID as key
          if (seenCloneBase.has(baseKey)) continue;
          seenCloneBase.add(baseKey);
          // Add all clones in order
          n.cloneGroup.forEach(cid => {
            if (metaData[cid]) dsList.push([cid, metaData[cid]]);
          });
        } else {
          dsList.push([id, n]);
        }
      }
      dsList.sort((a,b) => {
        const na = a[1].name + (a[1].splitSide||'');
        const nb = b[1].name + (b[1].splitSide||'');
        return na.localeCompare(nb);
      });
      const gamList = Object.entries(metaData).filter(([,n]) => n.kind==='gam');
      gamList.sort((a,b) => a[1].name.localeCompare(b[1].name));

      const renderNode = ([id, n]) => {
        const sigs = n.kind==='ds' ? (n.dsSigs||[]) : [...(n.inSigs||[]), ...(n.outSigs||[])];
        const matches = !fq || n.name.toLowerCase().includes(fq) || sigs.some(s=>s.localName.toLowerCase().includes(fq));
        if (!matches) return '';
        const expanded = sidebarExpanded.has(id);
        const _nf      = threadVisible ?? stateVisible;
        const dimmed   = _nf && !_nf.has(id);
        const focused  = selected.has(id);
        const icon     = n.kind==='ds' ? '◼' : (n.iogam ? '⇄' : '▶');
        const splitLbl = n.splitSide==='r' ? ' <span style="color:#1a4a6a;font-size:9px">·src</span>'
                       : n.splitSide==='w' ? ' <span style="color:#1a4a6a;font-size:9px">·snk</span>' : '';
        const pinned = watchlist.has(id);
        let h = '<div class="sb-node'+(dimmed?' dimmed':'')+(focused?' focused':'')+'" data-id="'+id+'">';
        h += '<span class="sb-toggle">'+(sigs.length?(expanded?'▼':'▶'):'')+'</span>';
        h += '<span class="sb-icon">'+icon+'</span>';
        h += '<span class="sb-name">'+esc(n.name)+splitLbl+'</span>';
        h += '<span class="sb-cls">'+esc(n.class||'')+'</span>';
        h += '<span class="sb-pin'+(pinned?' pinned':'')+'" data-pin="'+id+'" title="'+(pinned?'Remove from':'Add to')+' watchlist">⊕</span>';
        h += '</div>';
        if (expanded || fq) {
          sigs.forEach(s => {
            if (fq && !s.localName.toLowerCase().includes(fq) && !n.name.toLowerCase().includes(fq)) return;
            const dir = s.dir || (n.kind==='ds' ? 'ds-both' : (n.inSigs||[]).includes(s) ? 'in' : 'out');
            const arrow = dir==='ds-out'?'→':dir==='ds-in'?'←':dir==='in'?'←':dir==='out'?'→':'↔';
            h += '<div class="sb-sig" data-id="'+id+'" data-sig="'+esc(s.localName)+'">';
            h += '<span class="arrow '+dir+'">'+arrow+'</span>';
            h += '<span class="sb-sig-name">'+esc(s.localName)+'</span>';
            h += '</div>';
          });
        }
        return h;
      };

      if (dsList.length) {
        html += '<div class="sb-section">DataSources</div>';
        dsList.forEach(pair => { html += renderNode(pair); });
      }
      if (gamList.length) {
        html += '<div class="sb-section">GAMs</div>';
        gamList.forEach(pair => { html += renderNode(pair); });
      }

      sbTreeEl.innerHTML = html;

      sbTreeEl.querySelectorAll('.sb-node').forEach(el => {
        el.addEventListener('click', e => {
          const id = el.dataset.id;
          const n  = metaData[id];
          const sigs = n ? (n.kind==='ds' ? (n.dsSigs||[]) : [...(n.inSigs||[]), ...(n.outSigs||[])]) : [];
          if (sigs.length) {
            if (sidebarExpanded.has(id)) sidebarExpanded.delete(id);
            else sidebarExpanded.add(id);
          }
          // Ctrl/Cmd+click on an already-selected node → deselect it.
          if ((e.ctrlKey || e.metaKey) && selected.has(id)) {
            selected.delete(id);
            const svg = graphEl.querySelector('svg');
            if (svg) applyFilter(svg);
            renderSidebar(sbFilterEl.value);
          } else {
            focusNode(id);
          }
        });
      });
      sbTreeEl.querySelectorAll('.sb-sig').forEach(el => {
        el.addEventListener('click', () => focusNode(el.dataset.id));
      });
      sbTreeEl.querySelectorAll('.sb-pin').forEach(el => {
        el.addEventListener('click', e => {
          e.stopPropagation();
          const id = el.dataset.pin;
          if (watchlist.has(id)) watchlist.delete(id); else watchlist.add(id);
          updateWatchlistBtn();
          renderSidebar(sbFilterEl.value);
        });
      });
    }

    function updateWatchlistBtn() {
      const btn = $('btn-watchlist');
      if (watchlist.size > 0) {
        btn.style.display = '';
        btn.textContent = '◉ '+watchlist.size;
      } else {
        btn.style.display = 'none';
      }
    }

    sbFilterEl.addEventListener('input', () => renderSidebar(sbFilterEl.value));

    // ── Tooltip ────────────────────────────────────────────────────────────
    function showTooltip(nodeId, x, y) {
      const n = metaData[nodeId]; if (!n) return;
      let h = '';
      h += '<div class="tt-name">'+esc(n.name)+'</div>';
      const kindLabel = n.kind==='ds' ? 'DataSource' : (n.iogam ? 'IOGAM' : 'GAM');
      h += '<div class="tt-class">'+esc(n.class||'')+'  <span style="color:#484f58">'+kindLabel+'</span></div>';
      if (n.conditional) h += '<div class="tt-cond">◇ Conditional</div>';
      if (n.doc)         h += '<div class="tt-doc">'+esc(n.doc)+'</div>';

      if ((n.diags||[]).length) {
        h += '<div class="tt-section">Diagnostics</div>';
        n.diags.forEach(d => h += '<div class="'+(d.severity==='error'?'tt-diag-error':'tt-diag-warn')+'">'+(d.severity==='error'?'⊗':'△')+' '+esc(d.message)+'</div>');
      }

      const fks = Object.keys(n.fields||{}).filter(k=>k!=='Class');
      if (fks.length) {
        h += '<div class="tt-section">Configuration</div>';
        fks.forEach(k => h += '<div class="tt-field">'+esc(k)+': <span>'+esc(n.fields[k])+'</span></div>');
      }

      const sigLine = (s, arrow) => {
        let l = (s.implicit?'~ ':'')+esc(s.localName);
        if (s.type) l += ': <span style="color:#8b949e">'+esc(s.type)+(s.numElems&&s.numElems!=='1'?'['+esc(s.numElems)+']':'')+'</span>';
        if (s.dsName) l += ' <span style="color:#3a4a5a">'+arrow+' '+esc(s.dsName)+'</span>';
        if (s.doc)    l += '<br/><span style="color:#484f58;font-style:italic">'+esc(s.doc)+'</span>';
        (s.diags||[]).forEach(d => {
          l += '<div class="'+(d.severity==='error'?'tt-diag-error':'tt-diag-warn')+'" style="padding-left:10px">▸ '+esc(d.message)+'</div>';
        });
        return l;
      };

      if (n.iogam && ((n.inSigs||[]).length||(n.outSigs||[]).length)) {
        h += '<div class="tt-section">Signal Pairs (in → out)</div>';
        const cnt = Math.max((n.inSigs||[]).length,(n.outSigs||[]).length);
        for (let i=0;i<cnt;i++) {
          const inp=n.inSigs[i], out=n.outSigs[i];
          h += '<div class="tt-iogam-pair">';
          h += '<span class="inp">'+(inp?'← '+esc(inp.localName):'—')+'</span>';
          h += '<span style="color:#30363d">→</span>';
          h += '<span class="out">'+(out?esc(out.localName)+' →':'—')+'</span>';
          h += '</div>';
        }
      } else {
        if ((n.inSigs||[]).length) {
          h += '<div class="tt-section">Input Signals</div>';
          n.inSigs.forEach(s => h += '<div class="tt-sig in">'+sigLine(s,'←')+'</div>');
        }
        if ((n.outSigs||[]).length) {
          h += '<div class="tt-section">Output Signals</div>';
          n.outSigs.forEach(s => h += '<div class="tt-sig out">'+sigLine(s,'→')+'</div>');
        }
      }
      if ((n.dsSigs||[]).length) {
        h += '<div class="tt-section">Signals</div>';
        n.dsSigs.forEach(s => h += '<div class="tt-sig '+(s.implicit?'implicit':'ds')+'">'+sigLine(s,'')+'</div>');
      }

      tooltipEl.innerHTML = h;
      placeTooltip(x, y);
      tooltipEl.style.display = 'block';
    }

    function placeTooltip(x, y) {
      const tw = tooltipEl.offsetWidth||300, th = tooltipEl.offsetHeight||100;
      let tx = x+16, ty = y+12;
      if (tx+tw > window.innerWidth -10) tx = x-tw-10;
      if (ty+th > window.innerHeight-10) ty = y-th-10;
      tooltipEl.style.left = tx+'px'; tooltipEl.style.top = ty+'px';
    }
    function hideTooltip() { tooltipEl.style.display='none'; }

    // ── Zoom / Home ────────────────────────────────────────────────────────
    $('btn-home')   .addEventListener('click', () => { panZoom?.fit(); panZoom?.center(); });
    $('btn-zoomin') .addEventListener('click', () => panZoom?.zoomIn());
    $('btn-zoomout').addEventListener('click', () => panZoom?.zoomOut());

    // ── Search ─────────────────────────────────────────────────────────────
    function buildSearchIndex() {
      searchItems = [];
      Object.entries(metaData).forEach(([id, n]) => {
        const kind = n.kind==='ds' ? 'ds' : (n.iogam ? 'iogam' : 'gam');
        searchItems.push({id, name: n.name, kind, parent:'', nodeId:id});
        const sigs = [...(n.inSigs||[]),...(n.outSigs||[]),...(n.dsSigs||[])];
        sigs.forEach(s => searchItems.push({
          id: id+'__'+s.localName, name: s.localName, kind:'sig', parent: n.name, nodeId: id
        }));
      });
      searchItems.sort((a,b) => a.name.localeCompare(b.name));
    }

    function openSearch() {
      searchOverlay.classList.add('active');
      searchInput.value = '';
      renderSearchResults('');
      searchInput.focus();
    }
    function closeSearch() {
      searchOverlay.classList.remove('active');
      searchActiveIdx = -1;
    }

    function renderSearchResults(q) {
      const query = q.toLowerCase().trim();
      const hits = query ? searchItems.filter(it => it.name.toLowerCase().includes(query)) : searchItems.slice(0,60);
      searchActiveIdx = hits.length ? 0 : -1;
      searchResults.innerHTML = '';
      hits.slice(0,80).forEach((it, idx) => {
        const div = document.createElement('div');
        div.className = 'search-item'+(idx===searchActiveIdx?' active':'');
        div.dataset.nodeId = it.nodeId;
        div.innerHTML =
          '<span class="si-kind '+it.kind+'">'+it.kind+'</span>'+
          '<span class="si-name">'+esc(it.name)+'</span>'+
          (it.parent?'<span class="si-parent">'+esc(it.parent)+'</span>':'');
        div.addEventListener('click', () => { focusNode(it.nodeId); closeSearch(); });
        searchResults.appendChild(div);
      });
    }

    searchInput.addEventListener('input', () => renderSearchResults(searchInput.value));
    searchInput.addEventListener('keydown', e => {
      const items = searchResults.querySelectorAll('.search-item');
      if (e.key === 'ArrowDown') {
        e.preventDefault();
        searchActiveIdx = Math.min(searchActiveIdx+1, items.length-1);
        items.forEach((el,i) => el.classList.toggle('active', i===searchActiveIdx));
        items[searchActiveIdx]?.scrollIntoView({block:'nearest'});
      } else if (e.key === 'ArrowUp') {
        e.preventDefault();
        searchActiveIdx = Math.max(searchActiveIdx-1, 0);
        items.forEach((el,i) => el.classList.toggle('active', i===searchActiveIdx));
        items[searchActiveIdx]?.scrollIntoView({block:'nearest'});
      } else if (e.key === 'Enter') {
        const a = searchResults.querySelector('.search-item.active');
        if (a) { focusNode(a.dataset.nodeId); closeSearch(); }
      } else if (e.key === 'Escape') {
        closeSearch();
      }
    });
    searchOverlay.addEventListener('click', e => { if (e.target===searchOverlay) closeSearch(); });
    $('btn-search').addEventListener('click', openSearch);

    // ── Help ───────────────────────────────────────────────────────────────
    const helpOverlay = $('help-overlay');
    $('btn-help').addEventListener('click', () => helpOverlay.classList.toggle('active'));
    helpOverlay.addEventListener('click', e => { if (e.target===helpOverlay) helpOverlay.classList.remove('active'); });

    function focusNode(nodeId) {
      const svg = graphEl.querySelector('svg'); if (!svg || !panZoom) return;
      let targetEl = null;
      svg.querySelectorAll('.node').forEach(el => {
        const t = el.querySelector('title')?.textContent?.trim()?.split(':')[0];
        if (t === nodeId) targetEl = el;
      });
      if (!targetEl) return;
      selected = new Set([nodeId]);
      applyFilter(svg);
      renderSidebar(sbFilterEl.value);

      // Read all geometry BEFORE any transform changes to avoid stale layout reads.
      const svgRect  = svg.getBoundingClientRect();
      const nodeRect = targetEl.getBoundingClientRect();
      const svgCX    = svgRect.left + svgRect.width  / 2;
      const svgCY    = svgRect.top  + svgRect.height / 2;
      const nodeCX   = (nodeRect.left + nodeRect.right)  / 2;
      const nodeCY   = (nodeRect.top  + nodeRect.bottom) / 2;
      const curZoom  = panZoom.getZoom();

      // Natural node size at zoom=1 (initial-fit state).
      const nW = nodeRect.width  / curZoom;
      const nH = nodeRect.height / curZoom;

      // Target zoom: node fills 80% of the smaller viewport dimension.
      const newZoom = (nW > 2 && nH > 2)
        ? Math.min(Math.max(Math.min(svgRect.width * 0.8 / nW, svgRect.height * 0.8 / nH), 0.3), 8)
        : curZoom;

      // zoom(newZoom) scales around the viewport centre, so the node's new screen position is:
      //   nodeCXafter = svgCX + (nodeCX - svgCX) × scale
      // The pan delta required to bring the node to centre:
      //   dx = svgCX - nodeCXafter = (svgCX - nodeCX) × scale
      const scale = newZoom / curZoom;
      const dx = (svgCX - nodeCX) * scale;
      const dy = (svgCY - nodeCY) * scale;

      panZoom.zoom(newZoom);                          // zoom around viewport centre
      const pan = panZoom.getPan();                   // read updated pan post-zoom
      panZoom.pan({ x: pan.x + dx, y: pan.y + dy }); // shift to centre the node
    }

    // ── Key bindings ───────────────────────────────────────────────────────
    document.addEventListener('keydown', e => {
      const tag = document.activeElement.tagName;
      if (tag === 'INPUT' || tag === 'SELECT') return;
      if (e.key === '?') { e.preventDefault(); helpOverlay.classList.toggle('active'); }
      if (e.key === '/') { e.preventDefault(); openSearch(); }
      if (e.key === 'Escape') { closeSearch(); helpOverlay.classList.remove('active'); }
      if (e.key === 'Home' || e.key === 'h') { panZoom?.fit(); panZoom?.center(); }
      if (e.key === 'Tab' && selected.size === 1) {
        e.preventDefault();
        const selId = [...selected][0];
        const n = metaData[selId];
        if (n && n.cloneGroup && n.cloneGroup.length > 1) {
          const idx = n.cloneGroup.indexOf(selId);
          const nextId = n.cloneGroup[(idx + 1) % n.cloneGroup.length];
          focusNode(nextId);
        }
      }
    });

    // ── SSE ────────────────────────────────────────────────────────────────
    function connectSSE() {
      const es = new EventSource('/events');
      es.onmessage = e => {
        if (e.data === 'reload') {
          // Save current pan/zoom so loadGraph can restore it after re-render.
          // Only save when not in focus mode (focus view will be exited on reload).
          if (panZoom && !focusMode) {
            pendingRestore = {pan: panZoom.getPan(), zoom: panZoom.getZoom()};
          }
          loadGraph();
        } else if (e.data.startsWith('focus:')) {
          // LSP cursor moved to a named node — debounce and pan/zoom to it.
          const name = e.data.slice(6);
          clearTimeout(focusDebounce);
          focusDebounce = setTimeout(() => {
            const id = nameToId[name];
            if (id && !focusMode) focusNode(id);
          }, 200);
        }
      };
      es.onerror = () => { es.close(); setTimeout(connectSSE, 2000); };
    }
    connectSSE();
  })();
  </script>
</body>
</html>`
