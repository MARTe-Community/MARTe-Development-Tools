package lsp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
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
	Context      CompletionContext      `json:"context"`
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

var Tree = index.NewProjectTree()
var Documents = make(map[string]string)
var ProjectRoot string
var GlobalSchema *schema.Schema
var Output io.Writer = os.Stdout

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
	Range       *Range `json:"range,omitempty"`
	RangeLength int    `json:"rangeLength,omitempty"`
	Text        string `json:"text"`
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

type RenameParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
	NewName      string                 `json:"newName"`
}

type WorkspaceEdit struct {
	Changes map[string][]TextEdit `json:"changes"`
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

		HandleMessage(msg)
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

func HandleMessage(msg *JsonRpcMessage) {
	defer func() {
		if r := recover(); r != nil {
			logger.Printf("Panic in HandleMessage: %v", r)
		}
	}()

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
				ProjectRoot = root
				logger.Printf("Scanning workspace: %s\n", root)
				if err := Tree.ScanDirectory(root); err != nil {
					logger.Printf("ScanDirectory failed: %v\n", err)
				}
				logger.Printf("Scan done")
				Tree.ResolveReferences()
				logger.Printf("Resolve done")
				GlobalSchema = schema.LoadFullSchema(ProjectRoot)
				logger.Printf("Schema done")
			}
		}

		respond(msg.ID, map[string]any{
			"capabilities": map[string]any{
				"textDocumentSync":           2, // Incremental sync
				"hoverProvider":              true,
				"definitionProvider":         true,
				"referencesProvider":         true,
				"documentFormattingProvider": true,
				"renameProvider":             true,
				"completionProvider": map[string]any{
					"triggerCharacters": []string{"=", " ", "@"},
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
			HandleDidOpen(params)
		}
	case "textDocument/didChange":
		var params DidChangeTextDocumentParams
		if err := json.Unmarshal(msg.Params, &params); err == nil {
			HandleDidChange(params)
		}
	case "textDocument/hover":
		var params HoverParams
		if err := json.Unmarshal(msg.Params, &params); err == nil {
			logger.Printf("Hover: %s:%d", params.TextDocument.URI, params.Position.Line)
			res := HandleHover(params)
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
			respond(msg.ID, HandleDefinition(params))
		}
	case "textDocument/references":
		var params ReferenceParams
		if err := json.Unmarshal(msg.Params, &params); err == nil {
			respond(msg.ID, HandleReferences(params))
		}
	case "textDocument/completion":
		var params CompletionParams
		if err := json.Unmarshal(msg.Params, &params); err == nil {
			respond(msg.ID, HandleCompletion(params))
		}
	case "textDocument/formatting":
		var params DocumentFormattingParams
		if err := json.Unmarshal(msg.Params, &params); err == nil {
			respond(msg.ID, HandleFormatting(params))
		}
	case "textDocument/rename":
		var params RenameParams
		if err := json.Unmarshal(msg.Params, &params); err == nil {
			respond(msg.ID, HandleRename(params))
		}
	}
}

func uriToPath(uri string) string {
	return strings.TrimPrefix(uri, "file://")
}

func HandleDidOpen(params DidOpenTextDocumentParams) {
	path := uriToPath(params.TextDocument.URI)
	Documents[params.TextDocument.URI] = params.TextDocument.Text
	p := parser.NewParser(params.TextDocument.Text)
	config, _ := p.Parse()

	publishParserErrors(params.TextDocument.URI, p.Errors())

	if config != nil {
		Tree.AddFile(path, config)
		Tree.ResolveReferences()
		runValidation(params.TextDocument.URI)
	}
}

func HandleDidChange(params DidChangeTextDocumentParams) {
	uri := params.TextDocument.URI
	text, ok := Documents[uri]
	if !ok {
		// If not found, rely on full sync being first or error
	}

	for _, change := range params.ContentChanges {
		if change.Range == nil {
			text = change.Text
		} else {
			text = applyContentChange(text, change)
		}
	}

	Documents[uri] = text
	path := uriToPath(uri)
	p := parser.NewParser(text)
	config, _ := p.Parse()

	publishParserErrors(uri, p.Errors())

	if config != nil {
		Tree.AddFile(path, config)
		Tree.ResolveReferences()
		runValidation(uri)
	}
}

func applyContentChange(text string, change TextDocumentContentChangeEvent) string {
	startOffset := offsetAt(text, change.Range.Start)
	endOffset := offsetAt(text, change.Range.End)

	if startOffset == -1 || endOffset == -1 {
		return text
	}

	return text[:startOffset] + change.Text + text[endOffset:]
}

func offsetAt(text string, pos Position) int {
	line := 0
	col := 0
	for i, r := range text {
		if line == pos.Line && col == pos.Character {
			return i
		}
		if line > pos.Line {
			break
		}
		if r == '\n' {
			line++
			col = 0
		} else {
			if r >= 0x10000 {
				col += 2
			} else {
				col++
			}
		}
	}
	if line == pos.Line && col == pos.Character {
		return len(text)
	}
	return -1
}

func HandleFormatting(params DocumentFormattingParams) []TextEdit {
	uri := params.TextDocument.URI
	text, ok := Documents[uri]
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

func runValidation(_ string) {
	v := validator.NewValidator(Tree, ProjectRoot)
	v.ValidateProject()

	// Group diagnostics by file
	fileDiags := make(map[string][]LSPDiagnostic)

	// Collect all known files to ensure we clear diagnostics for fixed files
	knownFiles := make(map[string]bool)
	collectFiles(Tree.Root, knownFiles)
	for _, node := range Tree.IsolatedFiles {
		collectFiles(node, knownFiles)
	}

	// Initialize all known files with empty diagnostics
	for f := range knownFiles {
		fileDiags[f] = []LSPDiagnostic{}
	}

	for _, d := range v.Diagnostics {
		severity := 1 // Error
		levelStr := "ERROR"
		if d.Level == validator.LevelWarning {
			severity = 2 // Warning
			levelStr = "WARNING"
		}

		diag := LSPDiagnostic{
			Range: Range{
				Start: Position{Line: d.Position.Line - 1, Character: d.Position.Column - 1},
				End:   Position{Line: d.Position.Line - 1, Character: d.Position.Column - 1 + 10}, // Arbitrary length
			},
			Severity: severity,
			Message:  fmt.Sprintf("%s: %s", levelStr, d.Message),
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

func publishParserErrors(uri string, errors []error) {
	diagnostics := []LSPDiagnostic{}

	for _, err := range errors {
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
		diagnostics = append(diagnostics, diag)
	}

	notification := JsonRpcMessage{
		Jsonrpc: "2.0",
		Method:  "textDocument/publishDiagnostics",
		Params: mustMarshal(PublishDiagnosticsParams{
			URI:         uri,
			Diagnostics: diagnostics,
		}),
	}
	send(notification)
}

func collectFiles(node *index.ProjectNode, files map[string]bool) {
	if node == nil {
		return
	}
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

func HandleHover(params HoverParams) *Hover {
	path := uriToPath(params.TextDocument.URI)
	line := params.Position.Line + 1
	col := params.Position.Character + 1

	res := Tree.Query(path, line, col)
	if res == nil {
		logger.Printf("No object/node/reference found")
		return nil
	}

	container := Tree.GetNodeContaining(path, parser.Position{Line: line, Column: col})

	var content string

	if res.Node != nil {
		if res.Node.Target != nil {
			content = fmt.Sprintf("**Link**: `%s` -> `%s`\n\n%s", res.Node.RealName, res.Node.Target.RealName, formatNodeInfo(res.Node.Target))
		} else {
			content = formatNodeInfo(res.Node)
		}
	} else if res.Field != nil {
		content = fmt.Sprintf("**Field**: `%s`", res.Field.Name)
	} else if res.Variable != nil {
		kind := "Variable"
		if res.Variable.IsConst {
			kind = "Constant"
		}
		content = fmt.Sprintf("**%s**: `%s`\nType: `%s`", kind, res.Variable.Name, res.Variable.TypeExpr)
		if res.Variable.DefaultValue != nil {
			content += fmt.Sprintf("\nDefault: `%s`", valueToString(res.Variable.DefaultValue, container))
		}
		if info := Tree.ResolveVariable(container, res.Variable.Name); info != nil {
			if info.Doc != "" {
				content += "\n\n" + info.Doc
			}
		}
	} else if res.Reference != nil {
		targetName := "Unresolved"
		fullInfo := ""
		targetDoc := ""

		if res.Reference.Target != nil {
			targetName = res.Reference.Target.RealName
			targetDoc = res.Reference.Target.Doc
			fullInfo = formatNodeInfo(res.Reference.Target)
		} else if res.Reference.TargetVariable != nil {
			v := res.Reference.TargetVariable
			targetName = v.Name
			kind := "Variable"
			if v.IsConst {
				kind = "Constant"
			}
			fullInfo = fmt.Sprintf("**%s**: `@%s`\nType: `%s`", kind, v.Name, v.TypeExpr)
			if v.DefaultValue != nil {
				fullInfo += fmt.Sprintf("\nDefault: `%s`", valueToString(v.DefaultValue, container))
			}
			if info := Tree.ResolveVariable(container, res.Reference.Name); info != nil {
				if info.Doc != "" {
					fullInfo += "\n\n" + info.Doc
				}
			}
		}

		content = fmt.Sprintf("**Reference**: `%s` -> `%s`", res.Reference.Name, targetName)
		if fullInfo != "" {
			content += fmt.Sprintf("\n\n---\n%s", fullInfo)
		} else if targetDoc != "" {
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

func valueToString(val parser.Value, ctx *index.ProjectNode) string {
	val = evaluate(val, ctx)
	switch v := val.(type) {
	case *parser.StringValue:
		if v.Quoted {
			return fmt.Sprintf("\"%s\"", v.Value)
		}
		return v.Value
	case *parser.IntValue:
		return v.Raw
	case *parser.FloatValue:
		return v.Raw
	case *parser.BoolValue:
		return fmt.Sprintf("%v", v.Value)
	case *parser.ReferenceValue:
		return v.Value
	case *parser.VariableReferenceValue:
		return v.Name
	case *parser.ArrayValue:
		elements := []string{}
		for _, e := range v.Elements {
			elements = append(elements, valueToString(e, ctx))
		}
		return fmt.Sprintf("{ %s }", strings.Join(elements, " "))
	default:
		return ""
	}
}

func HandleCompletion(params CompletionParams) *CompletionList {
	uri := params.TextDocument.URI
	path := uriToPath(uri)
	text, ok := Documents[uri]
	if !ok {
		return nil
	}

	lines := strings.Split(text, "\n")
	if params.Position.Line >= len(lines) {
		return nil
	}
	lineStr := lines[params.Position.Line]

	col := min(params.Position.Character, len(lineStr))

	prefix := lineStr[:col]

	// Case 4: Top-level keywords/macros
	if strings.HasPrefix(prefix, "#") && !strings.Contains(prefix, " ") {
		return &CompletionList{
			Items: []CompletionItem{
				{Label: "#package", Kind: 14, InsertText: "#package ${1:Project.URI}", InsertTextFormat: 2, Detail: "Project namespace definition"},
				{Label: "#var", Kind: 14, InsertText: "#var ${1:Name}: ${2:Type} = ${3:DefaultValue}", InsertTextFormat: 2, Detail: "Variable definition"},
				{Label: "#let", Kind: 14, InsertText: "#let ${1:Name}: ${2:Type} = ${3:Value}", InsertTextFormat: 2, Detail: "Constant variable definition"},
			},
		}
	}

	// Case 3: Variable completion
	varRegex := regexp.MustCompile(`([@])([a-zA-Z0-9_]*)$`)
	if matches := varRegex.FindStringSubmatch(prefix); matches != nil {
		container := Tree.GetNodeContaining(path, parser.Position{Line: params.Position.Line + 1, Column: col + 1})
		if container == nil {
			if iso, ok := Tree.IsolatedFiles[path]; ok {
				container = iso
			} else {
				container = Tree.Root
			}
		}
		return suggestVariables(container)
	}

	// Case 1: Assigning a value (Ends with "=" or "= ")
	if strings.Contains(prefix, "=") {
		lastIdx := strings.LastIndex(prefix, "=")
		beforeEqual := prefix[:lastIdx]

		// Find the last identifier before '='
		key := ""
		re := regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9_\-]*`)
		matches := re.FindAllString(beforeEqual, -1)
		if len(matches) > 0 {
			key = matches[len(matches)-1]
		}

		if key == "Class" {
			return suggestClasses()
		}

		container := Tree.GetNodeContaining(path, parser.Position{Line: params.Position.Line + 1, Column: col + 1})
		if container != nil {
			return suggestFieldValues(container, key, path)
		}
		return nil
	}

	// Case 2: Typing a key inside an object
	container := Tree.GetNodeContaining(path, parser.Position{Line: params.Position.Line + 1, Column: col + 1})
	if container != nil {
		if container.Parent != nil && isGAM(container.Parent) {
			if container.Name == "InputSignals" {
				return suggestGAMSignals(container, "Input")
			}
			if container.Name == "OutputSignals" {
				return suggestGAMSignals(container, "Output")
			}
		}
		return suggestFields(container)
	}

	return nil
}

func suggestGAMSignals(container *index.ProjectNode, direction string) *CompletionList {
	var items []CompletionItem

	// Find scope root
	root := container
	for root.Parent != nil {
		root = root.Parent
	}

	var walk func(*index.ProjectNode)
	processNode := func(node *index.ProjectNode) {
		if !isDataSource(node) {
			return
		}

		cls := node.Metadata["Class"]
		if cls == "" {
			return
		}

		dir := "NIL"
		if GlobalSchema != nil {
			classPath := cue.ParsePath(fmt.Sprintf("#Classes.%s.#meta.direction", cls))
			val := GlobalSchema.Value.LookupPath(classPath)
			if val.Err() == nil {
				var s string
				if err := val.Decode(&s); err == nil {
					dir = s
				}
			}
		}
		compatible := false
		switch direction {
		case "Input":
			compatible = dir == "IN" || dir == "INOUT"
		case "Output":
			compatible = dir == "OUT" || dir == "INOUT"
		default:
			compatible = false
		}

		if !compatible {
			return
		}

		signalsContainer := node.Children["Signals"]
		if signalsContainer == nil {
			return
		}

		for _, sig := range signalsContainer.Children {
			dsName := node.Name
			sigName := sig.Name

			label := fmt.Sprintf("%s:%s", dsName, sigName)
			insertText := fmt.Sprintf("%s = {\n DataSource = %s \n}", sigName, dsName)

			items = append(items, CompletionItem{
				Label:            label,
				Kind:             6, // Variable
				Detail:           "Signal from " + dsName,
				InsertText:       insertText,
				InsertTextFormat: 2, // Snippet
			})
		}
	}

	walk = func(n *index.ProjectNode) {
		processNode(n)
		for _, child := range n.Children {
			walk(child)
		}
	}
	walk(root)

	if len(items) > 0 {
		return &CompletionList{Items: items}
	}
	return nil
}

func suggestClasses() *CompletionList {
	if GlobalSchema == nil {
		return nil
	}

	classesVal := GlobalSchema.Value.LookupPath(cue.ParsePath("#Classes"))
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

	if GlobalSchema == nil {
		return nil
	}
	classPath := cue.ParsePath(fmt.Sprintf("#Classes.%s", cls))
	classVal := GlobalSchema.Value.LookupPath(classPath)
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

func suggestFieldValues(container *index.ProjectNode, field string, path string) *CompletionList {
	var root *index.ProjectNode
	if iso, ok := Tree.IsolatedFiles[path]; ok {
		root = iso
	} else {
		root = Tree.Root
	}

	var items []CompletionItem

	if field == "DataSource" {
		if list := suggestObjects(root, "DataSource"); list != nil {
			items = append(items, list.Items...)
		}
	} else if field == "Functions" {
		if list := suggestObjects(root, "GAM"); list != nil {
			items = append(items, list.Items...)
		}
	} else if field == "Type" {
		if list := suggestSignalTypes(); list != nil {
			items = append(items, list.Items...)
		}
	} else {
		if list := suggestCUEEnums(container, field); list != nil {
			items = append(items, list.Items...)
		}
	}

	// Add variables
	vars := suggestVariables(container)
	if vars != nil {
		for _, item := range vars.Items {
			// Create copy to modify label
			newItem := item
			newItem.Label = "@" + newItem.Label
			newItem.InsertText = "@" + item.Label
			items = append(items, newItem)
		}
	}

	if len(items) > 0 {
		return &CompletionList{Items: items}
	}
	return nil
}

func suggestSignalTypes() *CompletionList {
	types := []string{
		"uint8", "int8", "uint16", "int16", "uint32", "int32", "uint64", "int64",
		"float32", "float64", "string", "bool", "char8",
	}
	var items []CompletionItem
	for _, t := range types {
		items = append(items, CompletionItem{
			Label:  t,
			Kind:   13, // EnumMember
			Detail: "Signal Type",
		})
	}
	return &CompletionList{Items: items}
}

func suggestCUEEnums(container *index.ProjectNode, field string) *CompletionList {
	if GlobalSchema == nil {
		return nil
	}
	cls := container.Metadata["Class"]
	if cls == "" {
		return nil
	}

	classPath := cue.ParsePath(fmt.Sprintf("#Classes.%s.%s", cls, field))
	val := GlobalSchema.Value.LookupPath(classPath)
	if val.Err() != nil {
		return nil
	}

	op, args := val.Expr()
	var values []cue.Value
	if op == cue.OrOp {
		values = args
	} else {
		values = []cue.Value{val}
	}

	var items []CompletionItem
	for _, v := range values {
		if !v.IsConcrete() {
			continue
		}

		str, err := v.String() // Returns quoted string for string values?
		if err != nil {
			continue
		}

		// Ensure strings are quoted
		if v.Kind() == cue.StringKind && !strings.HasPrefix(str, "\"") {
			str = fmt.Sprintf("\"%s\"", str)
		}

		items = append(items, CompletionItem{
			Label:  str,
			Kind:   13, // EnumMember
			Detail: "Enum Value",
		})
	}

	if len(items) > 0 {
		return &CompletionList{Items: items}
	}
	return nil
}

func suggestObjects(root *index.ProjectNode, filter string) *CompletionList {
	if root == nil {
		return nil
	}
	var items []CompletionItem

	var walk func(*index.ProjectNode)
	walk = func(node *index.ProjectNode) {
		match := false
		switch filter {
		case "GAM":
			match = isGAM(node)
		case "DataSource":
			match = isDataSource(node)
		}

		if match {
			items = append(items, CompletionItem{
				Label:  node.Name,
				Kind:   6, // Variable
				Detail: node.Metadata["Class"],
			})
		}

		for _, child := range node.Children {
			walk(child)
		}
	}

	walk(root)
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

func HandleDefinition(params DefinitionParams) any {
	path := uriToPath(params.TextDocument.URI)
	line := params.Position.Line + 1
	col := params.Position.Character + 1

	res := Tree.Query(path, line, col)
	if res == nil {
		return nil
	}

	var targetNode *index.ProjectNode
	var targetVar *parser.VariableDefinition

	if res.Reference != nil {
		if res.Reference.Target != nil {
			targetNode = res.Reference.Target
		} else if res.Reference.TargetVariable != nil {
			targetVar = res.Reference.TargetVariable
		}
	} else if res.Node != nil {
		if res.Node.Target != nil {
			targetNode = res.Node.Target
		} else {
			targetNode = res.Node
		}
	} else if res.Variable != nil {
		targetVar = res.Variable
	}

	if targetVar != nil {
		container := Tree.GetNodeContaining(path, parser.Position{Line: line, Column: col})
		if info := Tree.ResolveVariable(container, targetVar.Name); info != nil {
			return []Location{{
				URI: "file://" + info.File,
				Range: Range{
					Start: Position{Line: targetVar.Position.Line - 1, Character: targetVar.Position.Column - 1},
					End:   Position{Line: targetVar.Position.Line - 1, Character: targetVar.Position.Column - 1 + len(targetVar.Name) + 5}, // #var + space + Name? Rough estimate
				},
			}}
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

func HandleReferences(params ReferenceParams) []Location {
	path := uriToPath(params.TextDocument.URI)
	line := params.Position.Line + 1
	col := params.Position.Character + 1

	res := Tree.Query(path, line, col)
	if res == nil {
		return nil
	}

	var targetNode *index.ProjectNode
	var targetVar *parser.VariableDefinition

	if res.Node != nil {
		targetNode = res.Node
	} else if res.Reference != nil {
		if res.Reference.Target != nil {
			targetNode = res.Reference.Target
		} else if res.Reference.TargetVariable != nil {
			targetVar = res.Reference.TargetVariable
		}
	} else if res.Variable != nil {
		targetVar = res.Variable
	}

	if targetVar != nil {
		var locations []Location
		// Declaration
		if params.Context.IncludeDeclaration {
			container := Tree.GetNodeContaining(path, parser.Position{Line: line, Column: col})
			if info := Tree.ResolveVariable(container, targetVar.Name); info != nil {
				locations = append(locations, Location{
					URI: "file://" + info.File,
					Range: Range{
						Start: Position{Line: targetVar.Position.Line - 1, Character: targetVar.Position.Column - 1},
						End:   Position{Line: targetVar.Position.Line - 1, Character: targetVar.Position.Column - 1 + len(targetVar.Name) + 5},
					},
				})
			}
		}
		// References
		for _, ref := range Tree.References {
			if ref.TargetVariable == targetVar {
				locations = append(locations, Location{
					URI: "file://" + ref.File,
					Range: Range{
						Start: Position{Line: ref.Position.Line - 1, Character: ref.Position.Column - 1},
						End:   Position{Line: ref.Position.Line - 1, Character: ref.Position.Column - 1 + len(ref.Name) + 1}, // $Name
					},
				})
			}
		}
		return locations
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
	for _, ref := range Tree.References {
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
	Tree.Walk(func(node *index.ProjectNode) {
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

func getEvaluatedMetadata(node *index.ProjectNode, key string) string {
	for _, frag := range node.Fragments {
		for _, def := range frag.Definitions {
			if f, ok := def.(*parser.Field); ok && f.Name == key {
				return valueToString(f.Value, node)
			}
		}
	}
	return node.Metadata[key]
}

func formatNodeInfo(node *index.ProjectNode) string {
	info := ""
	if class := node.Metadata["Class"]; class != "" {
		info = fmt.Sprintf("`%s:%s`\n\n", class, node.RealName[1:])
	} else {
		info = fmt.Sprintf("`%s`\n\n", node.RealName)
	}
	// Check if it's a Signal (has Type or DataSource)
	typ := getEvaluatedMetadata(node, "Type")
	ds := getEvaluatedMetadata(node, "DataSource")

	if ds == "" {
		if node.Parent != nil && node.Parent.Name == "Signals" {
			if node.Parent.Parent != nil {
				ds = node.Parent.Parent.Name
			}
		}
	}

	if typ != "" || ds != "" {
		sigInfo := "\n"
		if typ != "" {
			sigInfo += fmt.Sprintf("**Type**: `%s` ", typ)
		}
		if ds != "" {
			sigInfo += fmt.Sprintf("**DataSource**: `%s` ", ds)
		}

		// Size
		dims := getEvaluatedMetadata(node, "NumberOfDimensions")
		elems := getEvaluatedMetadata(node, "NumberOfElements")
		if dims != "" || elems != "" {
			sigInfo += fmt.Sprintf("**Size**: `[%s]`, `%s` dims ", elems, dims)
		}
		info += sigInfo
	}

	if node.Doc != "" {
		info += fmt.Sprintf("\n\n%s", node.Doc)
	}

	// Check if Implicit Signal peers exist
	if ds, _ := getSignalInfo(node); ds != nil {
		peers := findSignalPeers(node)

		// 1. Explicit Definition Fields
		var defNode *index.ProjectNode
		for _, p := range peers {
			if p.Parent != nil && p.Parent.Name == "Signals" {
				defNode = p
				break
			}
		}

		if defNode != nil {
			for _, frag := range defNode.Fragments {
				for _, def := range frag.Definitions {
					if f, ok := def.(*parser.Field); ok {
						key := f.Name
						if key != "Type" && key != "NumberOfElements" && key != "NumberOfDimensions" && key != "Class" {
							val := valueToString(f.Value, defNode)
							info += fmt.Sprintf("\n**%s**: `%s`", key, val)
						}
					}
				}
			}
		}

		extraInfo := ""
		for _, p := range peers {
			if (p.Parent.Name == "InputSignals" || p.Parent.Name == "OutputSignals") && isGAM(p.Parent.Parent) {
				gamName := p.Parent.Parent.RealName
				for _, frag := range p.Fragments {
					for _, def := range frag.Definitions {
						if f, ok := def.(*parser.Field); ok {
							key := f.Name
							if key != "DataSource" && key != "Alias" && key != "Type" && key != "Class" && key != "NumberOfElements" && key != "NumberOfDimensions" {
								val := valueToString(f.Value, p)
								extraInfo += fmt.Sprintf("\n- **%s** (%s): `%s`", key, gamName, val)
							}
						}
					}
				}
			}
		}
		if extraInfo != "" {
			info += "\n\n**Usage Details**:" + extraInfo
		}
	}

	// Find references
	var refs []string
	for _, ref := range Tree.References {
		if ref.Target == node {
			container := Tree.GetNodeContaining(ref.File, ref.Position)
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

	// Find GAM usages
	var gams []string

	// 1. Check References (explicit text references)
	for _, ref := range Tree.References {
		if ref.Target == node {
			container := Tree.GetNodeContaining(ref.File, ref.Position)
			if container != nil {
				curr := container
				for curr != nil {
					if isGAM(curr) {
						suffix := ""
						p := container
						for p != nil && p != curr {
							if p.Name == "InputSignals" {
								suffix = " (Input)"
								break
							}
							if p.Name == "OutputSignals" {
								suffix = " (Output)"
								break
							}
							p = p.Parent
						}
						gams = append(gams, curr.RealName+suffix)
						break
					}
					curr = curr.Parent
				}
			}
		}
	}

	// 2. Check Direct Usages (Nodes targeting this node)
	Tree.Walk(func(n *index.ProjectNode) {
		if n.Target == node {
			if n.Parent != nil && (n.Parent.Name == "InputSignals" || n.Parent.Name == "OutputSignals") {
				if n.Parent.Parent != nil && isGAM(n.Parent.Parent) {
					suffix := " (Input)"
					if n.Parent.Name == "OutputSignals" {
						suffix = " (Output)"
					}
					gams = append(gams, n.Parent.Parent.RealName+suffix)
				}
			}
		}
	})

	if len(gams) > 0 {
		uniqueGams := make(map[string]bool)
		info += "\n\n**Used in GAMs**:\n"
		for _, g := range gams {
			if !uniqueGams[g] {
				uniqueGams[g] = true
				info += fmt.Sprintf("- %s\n", g)
			}
		}
	}

	return info
}

func HandleRename(params RenameParams) *WorkspaceEdit {
	path := uriToPath(params.TextDocument.URI)
	line := params.Position.Line + 1
	col := params.Position.Character + 1

	res := Tree.Query(path, line, col)
	if res == nil {
		return nil
	}

	var targetNode *index.ProjectNode
	var targetField *parser.Field
	if res.Node != nil {
		if res.Node.Target != nil {
			targetNode = res.Node.Target
		} else {
			targetNode = res.Node
		}
	} else if res.Field != nil {
		targetField = res.Field
	} else if res.Reference != nil {
		if res.Reference.Target != nil {
			targetNode = res.Reference.Target
		} else {
			return nil
		}
	}

	changes := make(map[string][]TextEdit)

	addEdit := func(file string, rng Range, newText string) {
		uri := "file://" + file
		changes[uri] = append(changes[uri], TextEdit{Range: rng, NewText: newText})
	}

	if targetNode != nil {
		// Special handling for Signals (Implicit/Explicit)
		if ds, _ := getSignalInfo(targetNode); ds != nil {
			peers := findSignalPeers(targetNode)
			seenPeers := make(map[*index.ProjectNode]bool)

			for _, peer := range peers {
				if seenPeers[peer] {
					continue
				}
				seenPeers[peer] = true

				// Rename Peer Definition
				prefix := ""
				if len(peer.RealName) > 0 {
					first := peer.RealName[0]
					if first == '+' || first == '$' {
						prefix = string(first)
					}
				}
				normNewName := strings.TrimLeft(params.NewName, "+$")
				finalDefName := prefix + normNewName

				hasAlias := false
				for _, frag := range peer.Fragments {
					for _, def := range frag.Definitions {
						if f, ok := def.(*parser.Field); ok && f.Name == "Alias" {
							hasAlias = true
						}
					}
				}

				if !hasAlias {
					for _, frag := range peer.Fragments {
						if frag.IsObject {
							rng := Range{
								Start: Position{Line: frag.ObjectPos.Line - 1, Character: frag.ObjectPos.Column - 1},
								End:   Position{Line: frag.ObjectPos.Line - 1, Character: frag.ObjectPos.Column - 1 + len(peer.RealName)},
							}
							addEdit(frag.File, rng, finalDefName)
						}
					}
				}

				// Rename References to this Peer
				for _, ref := range Tree.References {
					if ref.Target == peer {
						// Handle qualified names
						if strings.Contains(ref.Name, ".") {
							if strings.HasSuffix(ref.Name, "."+peer.Name) {
								prefixLen := len(ref.Name) - len(peer.Name)
								rng := Range{
									Start: Position{Line: ref.Position.Line - 1, Character: ref.Position.Column - 1 + prefixLen},
									End:   Position{Line: ref.Position.Line - 1, Character: ref.Position.Column - 1 + len(ref.Name)},
								}
								addEdit(ref.File, rng, normNewName)
							} else if ref.Name == peer.Name {
								rng := Range{
									Start: Position{Line: ref.Position.Line - 1, Character: ref.Position.Column - 1},
									End:   Position{Line: ref.Position.Line - 1, Character: ref.Position.Column - 1 + len(ref.Name)},
								}
								addEdit(ref.File, rng, normNewName)
							}
						} else {
							rng := Range{
								Start: Position{Line: ref.Position.Line - 1, Character: ref.Position.Column - 1},
								End:   Position{Line: ref.Position.Line - 1, Character: ref.Position.Column - 1 + len(ref.Name)},
							}
							addEdit(ref.File, rng, normNewName)
						}
					}
				}
			}
			return &WorkspaceEdit{Changes: changes}
		}

		// 1. Rename Definitions
		prefix := ""
		if len(targetNode.RealName) > 0 {
			first := targetNode.RealName[0]
			if first == '+' || first == '$' {
				prefix = string(first)
			}
		}
		normNewName := strings.TrimLeft(params.NewName, "+$")
		finalDefName := prefix + normNewName

		for _, frag := range targetNode.Fragments {
			if frag.IsObject {
				rng := Range{
					Start: Position{Line: frag.ObjectPos.Line - 1, Character: frag.ObjectPos.Column - 1},
					End:   Position{Line: frag.ObjectPos.Line - 1, Character: frag.ObjectPos.Column - 1 + len(targetNode.RealName)},
				}
				addEdit(frag.File, rng, finalDefName)
			}
		}

		// 2. Rename References
		for _, ref := range Tree.References {
			if ref.Target == targetNode {
				// Handle qualified names (e.g. Pkg.Node)
				if strings.Contains(ref.Name, ".") {
					if strings.HasSuffix(ref.Name, "."+targetNode.Name) {
						prefixLen := len(ref.Name) - len(targetNode.Name)
						rng := Range{
							Start: Position{Line: ref.Position.Line - 1, Character: ref.Position.Column - 1 + prefixLen},
							End:   Position{Line: ref.Position.Line - 1, Character: ref.Position.Column - 1 + len(ref.Name)},
						}
						addEdit(ref.File, rng, normNewName)
					} else if ref.Name == targetNode.Name {
						rng := Range{
							Start: Position{Line: ref.Position.Line - 1, Character: ref.Position.Column - 1},
							End:   Position{Line: ref.Position.Line - 1, Character: ref.Position.Column - 1 + len(ref.Name)},
						}
						addEdit(ref.File, rng, normNewName)
					}
				} else {
					rng := Range{
						Start: Position{Line: ref.Position.Line - 1, Character: ref.Position.Column - 1},
						End:   Position{Line: ref.Position.Line - 1, Character: ref.Position.Column - 1 + len(ref.Name)},
					}
					addEdit(ref.File, rng, normNewName)
				}
			}
		}

		// 3. Rename Implicit Node References (Signals in GAMs relying on name match)
		Tree.Walk(func(n *index.ProjectNode) {
			if n.Target == targetNode {
				hasAlias := false
				for _, frag := range n.Fragments {
					for _, def := range frag.Definitions {
						if f, ok := def.(*parser.Field); ok && f.Name == "Alias" {
							hasAlias = true
						}
					}
				}

				if !hasAlias {
					for _, frag := range n.Fragments {
						if frag.IsObject {
							rng := Range{
								Start: Position{Line: frag.ObjectPos.Line - 1, Character: frag.ObjectPos.Column - 1},
								End:   Position{Line: frag.ObjectPos.Line - 1, Character: frag.ObjectPos.Column - 1 + len(n.RealName)},
							}
							addEdit(frag.File, rng, normNewName)
						}
					}
				}
			}
		})

		return &WorkspaceEdit{Changes: changes}
	} else if targetField != nil {
		container := Tree.GetNodeContaining(path, targetField.Position)
		if container != nil {
			for _, frag := range container.Fragments {
				for _, def := range frag.Definitions {
					if f, ok := def.(*parser.Field); ok {
						if f.Name == targetField.Name {
							rng := Range{
								Start: Position{Line: f.Position.Line - 1, Character: f.Position.Column - 1},
								End:   Position{Line: f.Position.Line - 1, Character: f.Position.Column - 1 + len(f.Name)},
							}
							addEdit(frag.File, rng, params.NewName)
						}
					}
				}
			}
		}
		return &WorkspaceEdit{Changes: changes}
	}

	return nil
}

func respond(id any, result any) {
	msg := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	}
	send(msg)
}

func send(msg any) {
	body, _ := json.Marshal(msg)
	fmt.Fprintf(Output, "Content-Length: %d\r\n\r\n%s", len(body), body)
}

func suggestVariables(container *index.ProjectNode) *CompletionList {
	items := []CompletionItem{}
	seen := make(map[string]bool)

	curr := container
	for curr != nil {
		for name, info := range curr.Variables {
			if !seen[name] {
				seen[name] = true

				doc := ""
				if info.Def.DefaultValue != nil {
					doc = fmt.Sprintf("Default: %s", valueToString(info.Def.DefaultValue, container))
				}

				kind := "Variable"
				if info.Def.IsConst {
					kind = "Constant"
				}

				items = append(items, CompletionItem{
					Label:         name,
					Kind:          6, // Variable
					Detail:        fmt.Sprintf("%s (%s)", kind, info.Def.TypeExpr),
					Documentation: doc,
				})
			}
		}
		curr = curr.Parent
	}
	return &CompletionList{Items: items}
}

func getSignalInfo(node *index.ProjectNode) (*index.ProjectNode, string) {
	if node.Parent == nil {
		return nil, ""
	}

	// Case 1: Definition
	if node.Parent.Name == "Signals" && isDataSource(node.Parent.Parent) {
		return node.Parent.Parent, node.RealName
	}

	// Case 2: Usage
	if (node.Parent.Name == "InputSignals" || node.Parent.Name == "OutputSignals") && isGAM(node.Parent.Parent) {
		dsName := ""
		sigName := node.RealName

		// Scan fields
		for _, frag := range node.Fragments {
			for _, def := range frag.Definitions {
				if f, ok := def.(*parser.Field); ok {
					if f.Name == "DataSource" {
						if v, ok := f.Value.(*parser.StringValue); ok {
							dsName = v.Value
						}
						if v, ok := f.Value.(*parser.ReferenceValue); ok {
							dsName = v.Value
						}
					}
					if f.Name == "Alias" {
						if v, ok := f.Value.(*parser.StringValue); ok {
							sigName = v.Value
						}
						if v, ok := f.Value.(*parser.ReferenceValue); ok {
							sigName = v.Value
						}
					}
				}
			}
		}

		if dsName != "" {
			dsNode := Tree.ResolveName(node, dsName, isDataSource)
			return dsNode, sigName
		}
	}
	return nil, ""
}

func findSignalPeers(target *index.ProjectNode) []*index.ProjectNode {
	dsNode, sigName := getSignalInfo(target)
	if dsNode == nil || sigName == "" {
		return nil
	}

	var peers []*index.ProjectNode

	// Add definition if exists (and not already target)
	if signals, ok := dsNode.Children["Signals"]; ok {
		if def, ok := signals.Children[index.NormalizeName(sigName)]; ok {
			peers = append(peers, def)
		}
	}

	// Find usages
	Tree.Walk(func(n *index.ProjectNode) {
		d, s := getSignalInfo(n)
		if d == dsNode && s == sigName {
			peers = append(peers, n)
		}
	})

	return peers
}

func evaluate(val parser.Value, ctx *index.ProjectNode) parser.Value {
	switch v := val.(type) {
	case *parser.VariableReferenceValue:
		name := strings.TrimLeft(v.Name, "@")
		if info := Tree.ResolveVariable(ctx, name); info != nil {
			if info.Def.DefaultValue != nil {
				return evaluate(info.Def.DefaultValue, ctx)
			}
		}
		return v
	case *parser.BinaryExpression:
		left := evaluate(v.Left, ctx)
		right := evaluate(v.Right, ctx)
		return compute(left, v.Operator, right)
	case *parser.UnaryExpression:
		right := evaluate(v.Right, ctx)
		return computeUnary(v.Operator, right)
	}
	return val
}

func compute(left parser.Value, op parser.Token, right parser.Value) parser.Value {
	if op.Type == parser.TokenConcat {
		getRaw := func(v parser.Value) string {
			if s, ok := v.(*parser.StringValue); ok {
				return s.Value
			}
			return valueToString(v, nil)
		}
		s1 := getRaw(left)
		s2 := getRaw(right)
		return &parser.StringValue{Value: s1 + s2, Quoted: true}
	}

	toInt := func(v parser.Value) (int64, bool) {
		if idx, ok := v.(*parser.IntValue); ok {
			return idx.Value, true
		}
		return 0, false
	}
	toFloat := func(v parser.Value) (float64, bool) {
		if f, ok := v.(*parser.FloatValue); ok {
			return f.Value, true
		}
		if idx, ok := v.(*parser.IntValue); ok {
			return float64(idx.Value), true
		}
		return 0, false
	}

	lI, lIsI := toInt(left)
	rI, rIsI := toInt(right)

	if lIsI && rIsI {
		var res int64
		switch op.Type {
		case parser.TokenPlus:
			res = lI + rI
		case parser.TokenMinus:
			res = lI - rI
		case parser.TokenStar:
			res = lI * rI
		case parser.TokenSlash:
			if rI != 0 {
				res = lI / rI
			}
		case parser.TokenPercent:
			if rI != 0 {
				res = lI % rI
			}
		case parser.TokenAmpersand:
			res = lI & rI
		case parser.TokenPipe:
			res = lI | rI
		case parser.TokenCaret:
			res = lI ^ rI
		}
		return &parser.IntValue{Value: res, Raw: fmt.Sprintf("%d", res)}
	}

	lF, lIsF := toFloat(left)
	rF, rIsF := toFloat(right)

	if lIsF || rIsF {
		var res float64
		switch op.Type {
		case parser.TokenPlus:
			res = lF + rF
		case parser.TokenMinus:
			res = lF - rF
		case parser.TokenStar:
			res = lF * rF
		case parser.TokenSlash:
			res = lF / rF
		}
		return &parser.FloatValue{Value: res, Raw: fmt.Sprintf("%g", res)}
	}

	return left
}

func computeUnary(op parser.Token, val parser.Value) parser.Value {
	switch op.Type {
	case parser.TokenMinus:
		if i, ok := val.(*parser.IntValue); ok {
			return &parser.IntValue{Value: -i.Value, Raw: fmt.Sprintf("%d", -i.Value)}
		}
		if f, ok := val.(*parser.FloatValue); ok {
			return &parser.FloatValue{Value: -f.Value, Raw: fmt.Sprintf("%g", -f.Value)}
		}
	case parser.TokenSymbol:
		if op.Value == "!" {
			if b, ok := val.(*parser.BoolValue); ok {
				return &parser.BoolValue{Value: !b.Value}
			}
		}
	}
	return val
}
