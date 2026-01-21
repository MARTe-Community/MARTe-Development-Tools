package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/marte-dev/marte-dev-tools/internal/index"
	"github.com/marte-dev/marte-dev-tools/internal/parser"
	"github.com/marte-dev/marte-dev-tools/internal/validator"
)

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

var tree = index.NewProjectTree()

func RunServer() {
	reader := bufio.NewReader(os.Stdin)
	for {
		msg, err := readMessage(reader)
		if err != nil {
			if err == io.EOF {
				break
			}
			fmt.Fprintf(os.Stderr, "Error reading message: %v\n", err)
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
				fmt.Fprintf(os.Stderr, "Scanning workspace: %s\n", root)
				tree.ScanDirectory(root)
				tree.ResolveReferences()
			}
		}

		respond(msg.ID, map[string]any{
			"capabilities": map[string]any{
				"textDocumentSync":   1, // Full sync
				"hoverProvider":      true,
				"definitionProvider": true,
				"referencesProvider": true,
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
			fmt.Fprintf(os.Stderr, "Hover: %s:%d\n", params.TextDocument.URI, params.Position.Line)
			res := handleHover(params)
			if res != nil {
				fmt.Fprintf(os.Stderr, "Res: %v\n", res.Contents)
			} else {
				fmt.Fprint(os.Stderr, "Res: NIL\n")
			}
			respond(msg.ID, res)
		} else {
			fmt.Fprint(os.Stderr, "not recovered hover parameters\n")
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
	}
}

func uriToPath(uri string) string {
	return strings.TrimPrefix(uri, "file://")
}

func handleDidOpen(params DidOpenTextDocumentParams) {
	path := uriToPath(params.TextDocument.URI)
	p := parser.NewParser(params.TextDocument.Text)
	config, err := p.Parse()
	if err == nil {
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
	path := uriToPath(params.TextDocument.URI)
	p := parser.NewParser(text)
	config, err := p.Parse()
	if err == nil {
		tree.AddFile(path, config)
		tree.ResolveReferences()
		runValidation(params.TextDocument.URI)
	}
}

func runValidation(uri string) {
	v := validator.NewValidator(tree)
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
			Params:  mustMarshal(PublishDiagnosticsParams{
				URI:         fileURI,
				Diagnostics: diags,
			}),
		}
		send(notification)
	}
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
		fmt.Fprint(os.Stderr, "No object/node/reference found\n")
		return nil
	}

	var content string

	if res.Node != nil {
		content = formatNodeInfo(res.Node)
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
		targetNode = res.Node
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

	var locations []Location
	if params.Context.IncludeDeclaration {
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
	}

	for _, ref := range tree.References {
		if ref.Target == targetNode {
			locations = append(locations, Location{
				URI: "file://" + ref.File,
				Range: Range{
					Start: Position{Line: ref.Position.Line - 1, Character: ref.Position.Column - 1},
					End:   Position{Line: ref.Position.Line - 1, Character: ref.Position.Column - 1 + len(ref.Name)},
				},
			})
		}
	}

	return locations
}

func formatNodeInfo(node *index.ProjectNode) string {
	class := node.Metadata["Class"]
	if class == "" {
		class = "Unknown"
	}

	info := fmt.Sprintf("**Object**: `%s`\n\n**Class**: `%s`", node.RealName, class)

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