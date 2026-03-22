package framework

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

type LSPTestClient struct {
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	stdout    *bufio.Reader
	rootDir   string
	mu        sync.Mutex
	nextID    int
	handlers  map[string]func(json.RawMessage)
	documents map[string]string
	version   int
	done      chan struct{}
}

func NewLSPTestClient(mdtPath, rootDir string) *LSPTestClient {
	cmd := exec.Command(mdtPath, "lsp")
	cmd.Dir = rootDir

	stdin, err := cmd.StdinPipe()
	if err != nil {
		panic(fmt.Sprintf("Failed to create stdin pipe: %v", err))
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		panic(fmt.Sprintf("Failed to create stdout pipe: %v", err))
	}

	client := &LSPTestClient{
		cmd:       cmd,
		stdin:     stdin,
		stdout:    bufio.NewReader(stdout),
		rootDir:   rootDir,
		nextID:    1,
		handlers:  make(map[string]func(json.RawMessage)),
		documents: make(map[string]string),
		version:   0,
		done:      make(chan struct{}),
	}

	if err := cmd.Start(); err != nil {
		panic(fmt.Sprintf("Failed to start LSP server: %v", err))
	}

	go client.readLoop()

	client.initialize(rootDir)

	return client
}

func (c *LSPTestClient) initialize(rootDir string) {
	params := map[string]interface{}{
		"processId": os.Getpid(),
		"rootUri":   "file://" + rootDir,
		"rootPath":  rootDir,
		"capabilities": map[string]interface{}{
			"textDocument": map[string]interface{}{
				"synchronization": map[string]interface{}{
					"willSave":          false,
					"didSave":           true,
					"willSaveWaitUntil": false,
				},
			},
		},
		"workspaceFolders": []map[string]interface{}{
			{"uri": "file://" + rootDir, "name": "test"},
		},
	}

	var result map[string]interface{}
	c.call("initialize", params, &result)

	c.notify("initialized", nil)
}

func (c *LSPTestClient) readLoop() {
	decoder := json.NewDecoder(c.stdout)
	for {
		select {
		case <-c.done:
			return
		default:
		}

		c.stdout.ReadByte()
		c.stdout.UnreadByte()

		var msg map[string]interface{}
		if err := decoder.Decode(&msg); err != nil {
			select {
			case <-c.done:
				return
			case <-time.After(10 * time.Millisecond):
				continue
			}
		}

		method, ok := msg["method"].(string)
		if !ok {
			continue
		}

		params, _ := json.Marshal(msg["params"])

		if handler, exists := c.handlers[method]; exists {
			handler(params)
		}
	}
}

func (c *LSPTestClient) call(method string, params, result interface{}) error {
	c.mu.Lock()
	id := c.nextID
	c.nextID++
	c.mu.Unlock()

	req := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}

	reqBytes, err := json.Marshal(req)
	if err != nil {
		return err
	}

	respChan := make(chan interface{})
	responseHandler := func(resp json.RawMessage) {
		var respObj map[string]interface{}
		if err := json.Unmarshal(resp, &respObj); err != nil {
			respChan <- err
			return
		}

		if errObj, ok := respObj["error"]; ok {
			respChan <- fmt.Errorf("LSP error: %v", errObj)
			return
		}

		if result != nil {
			if err := json.Unmarshal(respObj["result"].(json.RawMessage), result); err != nil {
				respChan <- err
				return
			}
		}
		respChan <- nil
	}

	c.mu.Lock()
	c.handlers[fmt.Sprintf("response-%d", id)] = func(resp json.RawMessage) {
		responseHandler(resp)
		c.mu.Lock()
		delete(c.handlers, fmt.Sprintf("response-%d", id))
		c.mu.Unlock()
	}
	c.mu.Unlock()

	_, err = c.stdin.Write(append(reqBytes, '\n'))
	if err != nil {
		return err
	}

	select {
	case resp := <-respChan:
		return resp.(error)
	case <-time.After(30 * time.Second):
		return fmt.Errorf("LSP call timeout: %s", method)
	}
}

func (c *LSPTestClient) notify(method string, params interface{}) {
	req := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	}

	reqBytes, _ := json.Marshal(req)
	c.stdin.Write(append(reqBytes, '\n'))
}

func (c *LSPTestClient) OpenFile(path, content string) string {
	uri := "file://" + c.rootDir + "/" + path

	c.documents[path] = content
	c.version++

	params := map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri":        uri,
			"languageId": "marte",
			"version":    c.version,
			"text":       content,
		},
	}

	c.notify("textDocument/didOpen", params)

	return uri
}

func (c *LSPTestClient) EditFile(path string, edits []TextEdit) {
	uri := "file://" + c.rootDir + "/" + path

	doc, ok := c.documents[path]
	if !ok {
		panic(fmt.Sprintf("Document %s not opened", path))
	}

	for _, edit := range edits {
		doc = applyEdit(doc, edit.Range, edit.NewText)
	}

	c.documents[path] = doc
	c.version++

	params := map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri":     uri,
			"version": c.version,
		},
		"contentChanges": []map[string]interface{}{
			{"text": doc},
		},
	}

	c.notify("textDocument/didChange", params)
}

func (c *LSPTestClient) GetDiagnostics(path string) []Diagnostic {
	var result struct {
		Diagnostics []map[string]interface{} `json:"diagnostics"`
	}

	uri := "textDocument/diagnostics"
	params := map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri": "file://" + c.rootDir + "/" + path,
		},
	}

	c.call(uri, params, &result)

	var diags []Diagnostic
	for _, d := range result.Diagnostics {
		diag := Diagnostic{
			Message:  d["message"].(string),
			Severity: "error",
		}
		if sev, ok := d["severity"].(float64); ok {
			if sev == 2 {
				diag.Severity = "error"
			} else if sev == 1 {
				diag.Severity = "error"
			} else if sev == 3 {
				diag.Severity = "warning"
			}
		}
		if rng, ok := d["range"].(map[string]interface{}); ok {
			if start, ok := rng["start"].(map[string]interface{}); ok {
				if line, ok := start["line"].(float64); ok {
					diag.Line = int(line) + 1
				}
				if char, ok := start["character"].(float64); ok {
					diag.Column = int(char) + 1
				}
			}
		}
		diags = append(diags, diag)
	}

	return diags
}

func (c *LSPTestClient) Hover(path string, line, char int) (string, error) {
	uri := "textDocument/hover"
	params := map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri": "file://" + c.rootDir + "/" + path,
		},
		"position": map[string]interface{}{
			"line":      line - 1,
			"character": char - 1,
		},
	}

	var result struct {
		Contents map[string]interface{} `json:"contents"`
	}

	err := c.call(uri, params, &result)
	if err != nil {
		return "", err
	}

	if result.Contents == nil {
		return "", nil
	}

	switch v := result.Contents["value"].(type) {
	case string:
		return v, nil
	default:
		return fmt.Sprintf("%v", result.Contents), nil
	}
}

func (c *LSPTestClient) Definition(path string, line, char int) ([]Location, error) {
	uri := "textDocument/definition"
	params := map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri": "file://" + c.rootDir + "/" + path,
		},
		"position": map[string]interface{}{
			"line":      line - 1,
			"character": char - 1,
		},
	}

	var result struct {
		Locations []map[string]interface{} `json:"locations"`
	}

	err := c.call(uri, params, &result)
	if err != nil {
		return nil, err
	}

	var locs []Location
	for _, l := range result.Locations {
		loc := Location{}
		if uri, ok := l["uri"].(string); ok {
			loc.URI = uri
		}
		if rng, ok := l["range"].(map[string]interface{}); ok {
			if start, ok := rng["start"].(map[string]interface{}); ok {
				if line, ok := start["line"].(float64); ok {
					loc.Range.Start.Line = int(line) + 1
				}
				if char, ok := start["character"].(float64); ok {
					loc.Range.Start.Character = int(char) + 1
				}
			}
			if end, ok := rng["end"].(map[string]interface{}); ok {
				if line, ok := end["line"].(float64); ok {
					loc.Range.End.Line = int(line) + 1
				}
				if char, ok := end["character"].(float64); ok {
					loc.Range.End.Character = int(char) + 1
				}
			}
		}
		locs = append(locs, loc)
	}

	return locs, nil
}

type Location struct {
	URI   string
	Range Range
}

func (c *LSPTestClient) Close() {
	c.notify("textDocument/didClose", map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri": "file://" + c.rootDir,
		},
	})

	close(c.done)
	c.cmd.Process.Kill()
	c.cmd.Wait()
}

func applyEdit(content string, editRange Range, newText string) string {
	lines := splitLines(content)

	startLine := editRange.Start.Line - 1
	endLine := editRange.End.Line - 1

	if startLine < 0 || startLine >= len(lines) {
		return content
	}

	startCol := editRange.Start.Character
	endCol := editRange.End.Character
	if endLine < len(lines) {
		endCol = editRange.End.Character
	} else {
		endCol = len(lines[endLine])
	}

	if endLine >= len(lines) {
		endLine = len(lines) - 1
	}

	lines[startLine] = lines[startLine][:startCol] + newText + lines[endLine][endCol:]
	if endLine > startLine {
		lines = append(lines[:startLine+1], lines[endLine+1:]...)
	}

	return joinLines(lines)
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i, r := range s {
		if r == '\n' {
			lines = append(lines, s[start:i+1])
			start = i + 1
		}
	}
	lines = append(lines, s[start:])
	return lines
}

func joinLines(lines []string) string {
	result := ""
	for _, line := range lines {
		result += line
	}
	return result
}

func (c *LSPTestClient) Document(path string) string {
	return c.documents[path]
}

func (c *LSPTestClient) Completion(path string, line, char int) ([]CompletionItem, error) {
	uri := "textDocument/completion"
	params := map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri": "file://" + c.rootDir + "/" + path,
		},
		"position": map[string]interface{}{
			"line":      line - 1,
			"character": char - 1,
		},
	}

	var result struct {
		Items []map[string]interface{} `json:"items"`
	}

	err := c.call(uri, params, &result)
	if err != nil {
		return nil, err
	}

	var items []CompletionItem
	for _, it := range result.Items {
		item := CompletionItem{}
		if label, ok := it["label"].(string); ok {
			item.Label = label
		}
		if detail, ok := it["detail"].(string); ok {
			item.Detail = detail
		}
		if insertText, ok := it["insertText"].(string); ok {
			item.InsertText = insertText
		}
		items = append(items, item)
	}

	return items, nil
}

type CompletionItem struct {
	Label      string
	Detail     string
	InsertText string
}

func (c *LSPTestClient) Symbol(path string) ([]DocumentSymbol, error) {
	uri := "textDocument/documentSymbol"
	params := map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri": "file://" + c.rootDir + "/" + path,
		},
	}

	var result []map[string]interface{}

	err := c.call(uri, params, &result)
	if err != nil {
		return nil, err
	}

	var symbols []DocumentSymbol
	for _, s := range result {
		sym := DocumentSymbol{}
		if name, ok := s["name"].(string); ok {
			sym.Name = name
		}
		if kind, ok := s["kind"].(float64); ok {
			sym.Kind = int(kind)
		}
		if rng, ok := s["range"].(map[string]interface{}); ok {
			if start, ok := rng["start"].(map[string]interface{}); ok {
				if line, ok := start["line"].(float64); ok {
					sym.Range.Start.Line = int(line) + 1
				}
			}
		}
		symbols = append(symbols, sym)
	}

	return symbols, nil
}

type DocumentSymbol struct {
	Name  string
	Kind  int
	Range Range
}

func (c *LSPTestClient) Rename(path string, line, char int, newName string) ([]WorkspaceEdit, error) {
	uri := "textDocument/rename"
	params := map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri": "file://" + c.rootDir + "/" + path,
		},
		"position": map[string]interface{}{
			"line":      line - 1,
			"character": char - 1,
		},
		"newName": newName,
	}

	var result struct {
		Changes map[string][]map[string]interface{} `json:"changes"`
	}

	err := c.call(uri, params, &result)
	if err != nil {
		return nil, err
	}

	var edits []WorkspaceEdit
	for uri, changes := range result.Changes {
		edit := WorkspaceEdit{URI: uri}
		for _, change := range changes {
			if rng, ok := change["range"].(map[string]interface{}); ok {
				var r Range
				if start, ok := rng["start"].(map[string]interface{}); ok {
					if line, ok := start["line"].(float64); ok {
						r.Start.Line = int(line) + 1
					}
					if char, ok := start["character"].(float64); ok {
						r.Start.Character = int(char) + 1
					}
				}
				if end, ok := rng["end"].(map[string]interface{}); ok {
					if line, ok := end["line"].(float64); ok {
						r.End.Line = int(line) + 1
					}
					if char, ok := end["character"].(float64); ok {
						r.End.Character = int(char) + 1
					}
				}
				text := ""
				if t, ok := change["newText"].(string); ok {
					text = t
				}
				edit.Changes = append(edit.Changes, TextEdit{Range: r, NewText: text})
			}
		}
		edits = append(edits, edit)
	}

	return edits, nil
}

type WorkspaceEdit struct {
	URI     string
	Changes []TextEdit
}
