package lsp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/marte-community/marte-dev-tools/internal/formatter"
	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/logger"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/schema"
	"github.com/marte-community/marte-dev-tools/internal/validator"

	"cuelang.org/go/cue"
)



type CompletionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
	Context      CompletionContext      `json:"context,omitempty"`
}

type CompletionContext struct {
	TriggerKind int `json:"triggerKind"`
}

type CompletionItem struct {
	Label            string `json:"label"`
	Kind             int    `json:"kind"`
	Detail           string `json:"detail,omitempty"`
	Documentation    string `json:"documentation,omitempty"`
	InsertText       string `json:"insertText,omitempty"`
	InsertTextFormat int    `json:"insertTextFormat,omitempty"` // 1: PlainText, 2: Snippet
	SortText         string `json:"sortText,omitempty"`
}

type CompletionList struct {
	IsIncomplete bool             `json:"isIncomplete"`
	Items        []CompletionItem `json:"items"`
}

var tree = index.NewProjectTree()
var documents = make(map[string]string)
var projectRoot string
var globalSchema *schema.Schema

type JsonRpcMessage struct {
	Jsonrpc string          `json:"jsonrpc"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      any             `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *JsonRpcError   `json:"error,omitempty"`
}

type JsonRpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type InitializeParams struct {
	RootURI  string `json:"rootUri"`
	RootPath string `json:"rootPath"`
}

type DidOpenTextDocumentParams struct {
	TextDocument TextDocumentItem `json:"textDocument"`
}

type DidChangeTextDocumentParams struct {
	TextDocument   VersionedTextDocumentIdentifier  `json:"textDocument"`
	ContentChanges []TextDocumentContentChangeEvent `json:"contentChanges"`
}

type TextDocumentItem struct {
	URI        string `json:"uri"`
	LanguageID string `json:"languageId"`
	Version    int    `json:"version"`
	Text       string `json:"text"`
}

type VersionedTextDocumentIdentifier struct {
	URI     string `json:"uri"`
	Version int    `json:"version"`
}

type TextDocumentContentChangeEvent struct {
	Text string `json:"text"`
}

type HoverParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}

type TextDocumentIdentifier struct {
	URI string `json:"uri"`
}

type DefinitionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}

type ReferenceParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
	Context      ReferenceContext       `json:"context"`
}

type ReferenceContext struct {
	IncludeDeclaration bool `json:"includeDeclaration"`
}

type Location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}

type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type Hover struct {
	Contents any `json:"contents"`
}

type MarkupContent struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

type PublishDiagnosticsParams struct {
	URI         string          `json:"uri"`
	Diagnostics []LSPDiagnostic `json:"diagnostics"`
}

type LSPDiagnostic struct {
	Range    Range  `json:"range"`
	Severity int    `json:"severity"`
	Message  string `json:"message"`
	Source   string `json:"source"`
}

type DocumentFormattingParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Options      FormattingOptions      `json:"options"`
}

type FormattingOptions struct {
	TabSize      int  `json:"tabSize"`
	InsertSpaces bool `json:"insertSpaces"`
}

type TextEdit struct {
	Range   Range  `json:"range"`
	NewText string `json:"newText"`
}


func RunServer() {
	reader := bufio.NewReader(os.Stdin)
	for {
		msg, err := readMessage(reader)
		if err != nil {
			if err == io.EOF {
				break
			}
			logger.Printf("Error reading message: %v\n", err)
			continue
		}

		handleMessage(msg)
	}
}

func readMessage(reader *bufio.Reader) (*JsonRpcMessage, error) {
	var contentLength int
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		if line == "\r\n" {
			break
		}
		if _, err := fmt.Sscanf(line, "Content-Length: %d", &contentLength); err == nil {
			continue
		}
	}

	body := make([]byte, contentLength)
	_, err := io.ReadFull(reader, body)
	if err != nil {
		return nil, err
	}

	var msg JsonRpcMessage
	err = json.Unmarshal(body, &msg)
	return &msg, err
}

func handleMessage(msg *JsonRpcMessage) {
	switch msg.Method {
	case "initialize":
		var params InitializeParams
		if err := json.Unmarshal(msg.Params, &params); err == nil {
			root := ""
			if params.RootURI != "" {
				root = uriToPath(params.RootURI)
			} else if params.RootPath != "" {
				root = params.RootPath
			}

			if root != "" {
				projectRoot = root
				logger.Printf("Scanning workspace: %s\n", root)
				if err := tree.ScanDirectory(root); err != nil {
					logger.Printf("ScanDirectory failed: %v\n", err)
				}
				tree.ResolveReferences()
				globalSchema = schema.LoadFullSchema(projectRoot)
			}
		}

		respond(msg.ID, map[string]any{
			"capabilities": map[string]any{
				"textDocumentSync":           1, // Full sync
				"hoverProvider":              true,
				"definitionProvider":         true,
				"referencesProvider":         true,
				"documentFormattingProvider": true,
				"completionProvider": map[string]any{
					"triggerCharacters": []string{"=", " "},
				},
			},
		})
	case "initialized":
		runValidation("")
	case "shutdown":
		respond(msg.ID, nil)
	case "exit":
		os.Exit(0)
	case "textDocument/didOpen":
		var params DidOpenTextDocumentParams
		if err := json.Unmarshal(msg.Params, &params); err == nil {
			handleDidOpen(params)
		}
	case "textDocument/didChange":
		var params DidChangeTextDocumentParams
		if err := json.Unmarshal(msg.Params, &params); err == nil {
			handleDidChange(params)
		}
	case "textDocument/hover":
		var params HoverParams
		if err := json.Unmarshal(msg.Params, &params); err == nil {
			logger.Printf("Hover: %s:%d", params.TextDocument.URI, params.Position.Line)
			res := handleHover(params)
			if res != nil {
				logger.Printf("Res: %v", res.Contents)
			} else {
				logger.Printf("Res: NIL")
			}
			respond(msg.ID, res)
		} else {
			logger.Printf("not recovered hover parameters")
			respond(msg.ID, nil)
		}
	case "textDocument/definition":
		var params DefinitionParams
		if err := json.Unmarshal(msg.Params, &params); err == nil {
			respond(msg.ID, handleDefinition(params))
		}
	case "textDocument/references":
		var params ReferenceParams
		if err := json.Unmarshal(msg.Params, &params); err == nil {
			respond(msg.ID, handleReferences(params))
		}
	case "textDocument/completion":
		var params CompletionParams
		if err := json.Unmarshal(msg.Params, &params); err == nil {
			respond(msg.ID, handleCompletion(params))
		}
	case "textDocument/formatting":
		var params DocumentFormattingParams
		if err := json.Unmarshal(msg.Params, &params); err == nil {
			respond(msg.ID, handleFormatting(params))
		}
	}
}

func uriToPath(uri string) string {
	return strings.TrimPrefix(uri, "file://")
}

func handleDidOpen(params DidOpenTextDocumentParams) {
	path := uriToPath(params.TextDocument.URI)
	documents[params.TextDocument.URI] = params.TextDocument.Text
	p := parser.NewParser(params.TextDocument.Text)
	config, err := p.Parse()

	if err != nil {
		publishParserError(params.TextDocument.URI, err)
	} else {
		publishParserError(params.TextDocument.URI, nil)
	}

	if config != nil {
		tree.AddFile(path, config)
		tree.ResolveReferences()
		runValidation(params.TextDocument.URI)
	}
}

func handleDidChange(params DidChangeTextDocumentParams) {
	if len(params.ContentChanges) == 0 {
		return
	}
	text := params.ContentChanges[0].Text
	documents[params.TextDocument.URI] = text
	path := uriToPath(params.TextDocument.URI)
	p := parser.NewParser(text)
	config, err := p.Parse()

	if err != nil {
		publishParserError(params.TextDocument.URI, err)
	} else {
		publishParserError(params.TextDocument.URI, nil)
	}

	if config != nil {
		tree.AddFile(path, config)
		tree.ResolveReferences()
		runValidation(params.TextDocument.URI)
	}
}

func handleFormatting(params DocumentFormattingParams) []TextEdit {
	uri := params.TextDocument.URI
	text, ok := documents[uri]
	if !ok {
		return nil
	}

	p := parser.NewParser(text)
	config, err := p.Parse()
	if err != nil {
		return nil
	}

	var buf bytes.Buffer
	formatter.Format(config, &buf)
	newText := buf.String()

	lines := strings.Count(text, "\n")
	if len(text) > 0 && !strings.HasSuffix(text, "\n") {
		lines++
	}

	return []TextEdit{
		{
			Range: Range{
				Start: Position{0, 0},
				End:   Position{lines + 1, 0},
			},
			NewText: newText,
		},
	}
}

func runValidation(uri string) {
	v := validator.NewValidator(tree, projectRoot)
	v.ValidateProject()
	v.CheckUnused()

	// Group diagnostics by file
	fileDiags := make(map[string][]LSPDiagnostic)

	// Collect all known files to ensure we clear diagnostics for fixed files
	knownFiles := make(map[string]bool)
	collectFiles(tree.Root, knownFiles)

	// Initialize all known files with empty diagnostics
	for f := range knownFiles {
		fileDiags[f] = []LSPDiagnostic{}
	}

	for _, d := range v.Diagnostics {
		severity := 1 // Error
		if d.Level == validator.LevelWarning {
			severity = 2 // Warning
		}

		diag := LSPDiagnostic{
			Range: Range{
				Start: Position{Line: d.Position.Line - 1, Character: d.Position.Column - 1},
				End:   Position{Line: d.Position.Line - 1, Character: d.Position.Column - 1 + 10}, // Arbitrary length
			},
			Severity: severity,
			Message:  d.Message,
			Source:   "mdt",
		}

		path := d.File
		if path != "" {
			fileDiags[path] = append(fileDiags[path], diag)
		}
	}

	// Send diagnostics for all known files
	for path, diags := range fileDiags {
		fileURI := "file://" + path
		notification := JsonRpcMessage{
			Jsonrpc: "2.0",
			Method:  "textDocument/publishDiagnostics",
			Params: mustMarshal(PublishDiagnosticsParams{
				URI:         fileURI,
				Diagnostics: diags,
			}),
		}
		send(notification)
	}
}

func publishParserError(uri string, err error) {
	if err == nil {
		notification := JsonRpcMessage{
			Jsonrpc: "2.0",
			Method:  "textDocument/publishDiagnostics",
			Params: mustMarshal(PublishDiagnosticsParams{
				URI:         uri,
				Diagnostics: []LSPDiagnostic{},
			}),
		}
		send(notification)
		return
	}

	var line, col int
	var msg string
	// Try parsing "line:col: message"
	n, _ := fmt.Sscanf(err.Error(), "%d:%d: ", &line, &col)
	if n == 2 {
		parts := strings.SplitN(err.Error(), ": ", 2)
		if len(parts) == 2 {
			msg = parts[1]
		}
	} else {
		// Fallback
		line = 1
		col = 1
		msg = err.Error()
	}

	diag := LSPDiagnostic{
		Range: Range{
			Start: Position{Line: line - 1, Character: col - 1},
			End:   Position{Line: line - 1, Character: col},
		},
		Severity: 1, // Error
		Message:  msg,
		Source:   "mdt-parser",
	}

	notification := JsonRpcMessage{
		Jsonrpc: "2.0",
		Method:  "textDocument/publishDiagnostics",
		Params: mustMarshal(PublishDiagnosticsParams{
			URI:         uri,
			Diagnostics: []LSPDiagnostic{diag},
		}),
	}
	send(notification)
}

func collectFiles(node *index.ProjectNode, files map[string]bool) {
	for _, frag := range node.Fragments {
		files[frag.File] = true
	}
	for _, child := range node.Children {
		collectFiles(child, files)
	}
}

func mustMarshal(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func handleHover(params HoverParams) *Hover {
	path := uriToPath(params.TextDocument.URI)
	line := params.Position.Line + 1
	col := params.Position.Character + 1

	res := tree.Query(path, line, col)
	if res == nil {
		logger.Printf("No object/node/reference found")
		return nil
	}

	var content string

	if res.Node != nil {
		if res.Node.Target != nil {
			content = fmt.Sprintf("**Link**: `%s` -> `%s`\n\n%s", res.Node.RealName, res.Node.Target.RealName, formatNodeInfo(res.Node.Target))
		} else {
			content = formatNodeInfo(res.Node)
		}
	} else if res.Field != nil {
		content = fmt.Sprintf("**Field**: `%s`", res.Field.Name)
	} else if res.Reference != nil {
		targetName := "Unresolved"
		fullInfo := ""
		targetDoc := ""

		if res.Reference.Target != nil {
			targetName = res.Reference.Target.RealName
			targetDoc = res.Reference.Target.Doc
			fullInfo = formatNodeInfo(res.Reference.Target)
		}

		content = fmt.Sprintf("**Reference**: `%s` -> `%s`", res.Reference.Name, targetName)
		if fullInfo != "" {
			content += fmt.Sprintf("\n\n---\n%s", fullInfo)
		} else if targetDoc != "" { // Fallback if formatNodeInfo returned empty (unlikely)
			content += fmt.Sprintf("\n\n%s", targetDoc)
		}
	}

	if content == "" {
		return nil
	}

	return &Hover{
		Contents: MarkupContent{
			Kind:  "markdown",
			Value: content,
		},
	}
}

func handleCompletion(params CompletionParams) *CompletionList {
	uri := params.TextDocument.URI
	path := uriToPath(uri)
	text, ok := documents[uri]
	if !ok {
		return nil
	}

	lines := strings.Split(text, "\n")
	if params.Position.Line >= len(lines) {
		return nil
	}
	lineStr := lines[params.Position.Line]

	col := params.Position.Character
	if col > len(lineStr) {
		col = len(lineStr)
	}

	prefix := lineStr[:col]

	// Case 1: Assigning a value (Ends with "=" or "= ")
	if strings.Contains(prefix, "=") {
		parts := strings.Split(prefix, "=")
		key := strings.TrimSpace(parts[len(parts)-2])

		if key == "Class" {
			return suggestClasses()
		}

		container := tree.GetNodeContaining(path, parser.Position{Line: params.Position.Line + 1, Column: col + 1})
		if container != nil {
			return suggestFieldValues(container, key)
		}
		return nil
	}

	// Case 2: Typing a key inside an object
	container := tree.GetNodeContaining(path, parser.Position{Line: params.Position.Line + 1, Column: col + 1})
	if container != nil {
		return suggestFields(container)
	}

	return nil
}

func suggestClasses() *CompletionList {
	if globalSchema == nil {
		return nil
	}

	classesVal := globalSchema.Value.LookupPath(cue.ParsePath("#Classes"))
	if classesVal.Err() != nil {
		return nil
	}

	iter, err := classesVal.Fields()
	if err != nil {
		return nil
	}

	var items []CompletionItem
	for iter.Next() {
		label := iter.Selector().String()
		label = strings.Trim(label, "?!#")

		items = append(items, CompletionItem{
			Label:  label,
			Kind:   7, // Class
			Detail: "MARTe Class",
		})
	}
	return &CompletionList{Items: items}
}

func suggestFields(container *index.ProjectNode) *CompletionList {
	cls := container.Metadata["Class"]
	if cls == "" {
		return &CompletionList{Items: []CompletionItem{{
			Label:      "Class",
			Kind:       10, // Property
			InsertText: "Class = ",
			Detail:     "Define object class",
		}}}
	}

	if globalSchema == nil {
		return nil
	}
	classPath := cue.ParsePath(fmt.Sprintf("#Classes.%s", cls))
	classVal := globalSchema.Value.LookupPath(classPath)
	if classVal.Err() != nil {
		return nil
	}

	iter, err := classVal.Fields()
	if err != nil {
		return nil
	}

	existing := make(map[string]bool)
	for _, frag := range container.Fragments {
		for _, def := range frag.Definitions {
			if f, ok := def.(*parser.Field); ok {
				existing[f.Name] = true
			}
		}
	}
	for name := range container.Children {
		existing[name] = true
	}

	var items []CompletionItem
	for iter.Next() {
		label := iter.Selector().String()
		label = strings.Trim(label, "?!#")

		// Skip if already present
		if existing[label] {
			continue
		}

		isOptional := iter.IsOptional()
		kind := 10 // Property
		detail := "Mandatory"
		if isOptional {
			detail = "Optional"
		}

		insertText := label + " = "
		val := iter.Value()
		if val.Kind() == cue.StructKind {
			// Suggest as node
			insertText = "+" + label + " = {\n\t$0\n}"
			kind = 9 // Module
		}

		items = append(items, CompletionItem{
			Label:            label,
			Kind:             kind,
			Detail:           detail,
			InsertText:       insertText,
			InsertTextFormat: 2, // Snippet
		})
	}
	return &CompletionList{Items: items}
}

func suggestFieldValues(container *index.ProjectNode, field string) *CompletionList {
	if field == "DataSource" {
		return suggestObjects("DataSource")
	}
	if field == "Functions" {
		return suggestObjects("GAM")
	}
	return nil
}

func suggestObjects(filter string) *CompletionList {
	var items []CompletionItem
	tree.Walk(func(node *index.ProjectNode) {
		match := false
		if filter == "GAM" {
			if isGAM(node) {
				match = true
			}
		} else if filter == "DataSource" {
			if isDataSource(node) {
				match = true
			}
		}

		if match {
			items = append(items, CompletionItem{
				Label:  node.Name,
				Kind:   6, // Variable
				Detail: node.Metadata["Class"],
			})
		}
	})
	return &CompletionList{Items: items}
}

func isGAM(node *index.ProjectNode) bool {
	if node.RealName == "" || (node.RealName[0] != '+' && node.RealName[0] != '$') {
		return false
	}
	_, hasInput := node.Children["InputSignals"]
	_, hasOutput := node.Children["OutputSignals"]
	return hasInput || hasOutput
}

func isDataSource(node *index.ProjectNode) bool {
	if node.Parent != nil && node.Parent.Name == "Data" {
		return true
	}
	_, hasSignals := node.Children["Signals"]
	return hasSignals
}

func handleDefinition(params DefinitionParams) any {
	path := uriToPath(params.TextDocument.URI)
	line := params.Position.Line + 1
	col := params.Position.Character + 1

	res := tree.Query(path, line, col)
	if res == nil {
		return nil
	}

	var targetNode *index.ProjectNode
	if res.Reference != nil && res.Reference.Target != nil {
		targetNode = res.Reference.Target
	} else if res.Node != nil {
		if res.Node.Target != nil {
			targetNode = res.Node.Target
		} else {
			targetNode = res.Node
		}
	}

	if targetNode != nil {
		var locations []Location
		for _, frag := range targetNode.Fragments {
			if frag.IsObject {
				locations = append(locations, Location{
					URI: "file://" + frag.File,
					Range: Range{
						Start: Position{Line: frag.ObjectPos.Line - 1, Character: frag.ObjectPos.Column - 1},
						End:   Position{Line: frag.ObjectPos.Line - 1, Character: frag.ObjectPos.Column - 1 + len(targetNode.RealName)},
					},
				})
			}
		}
		return locations
	}

	return nil
}

func handleReferences(params ReferenceParams) []Location {
	path := uriToPath(params.TextDocument.URI)
	line := params.Position.Line + 1
	col := params.Position.Character + 1

	res := tree.Query(path, line, col)
	if res == nil {
		return nil
	}

	var targetNode *index.ProjectNode
	if res.Node != nil {
		targetNode = res.Node
	} else if res.Reference != nil && res.Reference.Target != nil {
		targetNode = res.Reference.Target
	}

	if targetNode == nil {
		return nil
	}

	// Resolve canonical target (follow link if present)
	canonical := targetNode
	if targetNode.Target != nil {
		canonical = targetNode.Target
	}

	var locations []Location
	if params.Context.IncludeDeclaration {
		for _, frag := range canonical.Fragments {
			if frag.IsObject {
				locations = append(locations, Location{
					URI: "file://" + frag.File,
					Range: Range{
						Start: Position{Line: frag.ObjectPos.Line - 1, Character: frag.ObjectPos.Column - 1},
						End:   Position{Line: frag.ObjectPos.Line - 1, Character: frag.ObjectPos.Column - 1 + len(canonical.RealName)},
					},
				})
			}
		}
	}

	// 1. References from index (Aliases)
	for _, ref := range tree.References {
		if ref.Target == canonical {
			locations = append(locations, Location{
				URI: "file://" + ref.File,
				Range: Range{
					Start: Position{Line: ref.Position.Line - 1, Character: ref.Position.Column - 1},
					End:   Position{Line: ref.Position.Line - 1, Character: ref.Position.Column - 1 + len(ref.Name)},
				},
			})
		}
	}

	// 2. References from Node Targets (Direct References)
	tree.Walk(func(node *index.ProjectNode) {
		if node.Target == canonical {
			for _, frag := range node.Fragments {
				if frag.IsObject {
					locations = append(locations, Location{
						URI: "file://" + frag.File,
						Range: Range{
							Start: Position{Line: frag.ObjectPos.Line - 1, Character: frag.ObjectPos.Column - 1},
							End:   Position{Line: frag.ObjectPos.Line - 1, Character: frag.ObjectPos.Column - 1 + len(node.RealName)},
						},
					})
				}
			}
		}
	})

	return locations
}

func formatNodeInfo(node *index.ProjectNode) string {
	info := ""
	if class := node.Metadata["Class"]; class != "" {
		info = fmt.Sprintf("`%s:%s`\n\n", class, node.RealName[1:])
	} else {
		info = fmt.Sprintf("`%s`\n\n", node.RealName)
	}
	// Check if it's a Signal (has Type or DataSource)
	typ := node.Metadata["Type"]
	ds := node.Metadata["DataSource"]

	if typ != "" || ds != "" {
		sigInfo := "\n"
		if typ != "" {
			sigInfo += fmt.Sprintf("**Type**: `%s` ", typ)
		}
		if ds != "" {
			sigInfo += fmt.Sprintf("**DataSource**: `%s` ", ds)
		}

		// Size
		dims := node.Metadata["NumberOfDimensions"]
		elems := node.Metadata["NumberOfElements"]
		if dims != "" || elems != "" {
			sigInfo += fmt.Sprintf("**Size**: `[%s]`, `%s` dims ", elems, dims)
		}
		info += sigInfo
	}

	if node.Doc != "" {
		info += fmt.Sprintf("\n\n%s", node.Doc)
	}

	// Find references
	var refs []string
	for _, ref := range tree.References {
		if ref.Target == node {
			container := tree.GetNodeContaining(ref.File, ref.Position)
			if container != nil {
				threadName := ""
				stateName := ""

				curr := container
				for curr != nil {
					if cls, ok := curr.Metadata["Class"]; ok {
						if cls == "RealTimeThread" {
							threadName = curr.RealName
						}
						if cls == "RealTimeState" {
							stateName = curr.RealName
						}
					}
					curr = curr.Parent
				}

				if threadName != "" || stateName != "" {
					refStr := ""
					if stateName != "" {
						refStr += fmt.Sprintf("State: `%s`", stateName)
					}
					if threadName != "" {
						if refStr != "" {
							refStr += ", "
						}
						refStr += fmt.Sprintf("Thread: `%s`", threadName)
					}
					refs = append(refs, refStr)
				}
			}
		}
	}

	if len(refs) > 0 {
		uniqueRefs := make(map[string]bool)
		info += "\n\n**Referenced in**:\n"
		for _, r := range refs {
			if !uniqueRefs[r] {
				uniqueRefs[r] = true
				info += fmt.Sprintf("- %s\n", r)
			}
		}
	}

	return info
}

func respond(id any, result any) {
	msg := JsonRpcMessage{
		Jsonrpc: "2.0",
		ID:      id,
		Result:  result,
	}
	send(msg)
}

func send(msg any) {
	body, _ := json.Marshal(msg)
	fmt.Printf("Content-Length: %d\r\n\r\n%s", len(body), body)
}
